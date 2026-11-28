//go:build scenario

package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/storage/home until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
)

// host4Failures are the three ways the test-only crash component fails on request.
var host4Failures = []string{"load", "panic", "fault"}

// host4Report is the report path standard error names.
var host4Report = regexp.MustCompile(`(\S+\.(?:txt|log|json))`)

// host4NamedReport returns the first file named in stderr that lies inside the
// reports directory. Other files (the layer, logs) may be named first, and
// siblings such as "<reports>-old/x.txt" are not inside it.
func host4NamedReport(stderr, reports string) (string, bool) {
	for _, m := range host4Report.FindAllStringSubmatch(stderr, -1) {
		rel, err := filepath.Rel(reports, m[1])
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		return m[1], true
	}
	return "", false
}

// TestDyingHostLeavesReadableReport: a host that dies leaves a readable report on
// each operating system (areas/host-telemetry.md DH-E-HOST-4; DH-D-HOST-7).
// Executed in DH-S-18 (step 18.2) on Windows, macOS and Linux; authored in DH-S-6.
//
// Given the binary built from this checkout, a fresh home and a test-only component,
// enabled by configuration, that can fail at load, panic in a goroutine or trigger
// a runtime fault on request, when for each of the three failures the host is
// started, the failure triggered, standard error and the crash directory read, and
// the host started again, then each run exits non-zero; standard error names a
// report file; the file is owner-only and holds the cause and stacks; the next
// start prints the unclean-exit line with that path and the safe-mode option, and a
// start in safe mode reaches the UI. After twelve failures ten reports remain.
// Cleanup: the home is deleted; if a failure left no report, standard error and the
// directory listing are kept (logged).
//
// A host killed with no grace (SIGKILL, TerminateProcess) runs first
// (implementation-plan Stage 8.3, TC-21s3): it writes nothing, so the next start
// writes the report for its record, names it in the unclean-exit line with the
// safe-mode option, and the file is owner-only and names the killed process. Until
// DH-S-22 builds the UI, "reaches the UI" is the readiness probe answering
// (M2-DEC-6, Q m2-host4-ui-clause).
//
// Inputs: DSH_SCENARIO_HOST4_LAYER is the config layer text enabling the crash
// component, with {failure} standing for load, panic or fault; only the three
// failure legs and the twelve failures read it, so the killed and safe-mode legs
// run without it. DSH_SCENARIO_HOST4_ARGS are the ";"-separated arguments after the
// global options that start the host (for example "web;--no-open;--port;0");
// DSH_SCENARIO_HOST4_READY is a shell command line over {home} and {bin} (the
// binary under test) that exits 0 once the UI of a host running on {home} answers.
// A running host is stopped with the termination signal: SIGTERM on POSIX, closing
// its console on Windows.
//
//dsh:scenario DH-E-HOST-4
func TestDyingHostLeavesReadableReport(t *testing.T) {
	args := needList(t, "DSH_SCENARIO_HOST4_ARGS", "arguments starting the host, ';'-separated")
	ready := need(t, "DSH_SCENARIO_HOST4_READY", "command exiting 0 once the UI of the host on {home} answers")
	bin := dshBinary(t)
	dir := host4Dir(t)
	root := filepath.Join(dir, "home")
	layout := home.New(root)
	env := []string{"DSH_HOME=" + root}
	check := expand(ready, map[string]string{"home": root, "bin": bin})

	// answers waits until the readiness probe exits 0 for the host h runs.
	answers := func(t *testing.T, h *dshHost, what string) {
		t.Helper()
		h.waitFor(t, 30*time.Second, what, func(string) string {
			if _, err := shell(t, check); err == nil {
				return "ready"
			}
			return ""
		})
	}
	// uncleanLine waits for the unclean-exit line of h that names a report under
	// the reports directory and the safe-mode option, and returns the report.
	uncleanLine := func(t *testing.T, h *dshHost, what string) string {
		t.Helper()
		var report string
		h.waitFor(t, time.Minute, what, func(out string) string {
			for _, line := range strings.Split(out, "\n") {
				if !strings.Contains(line, "--safe") {
					continue
				}
				if r, ok := host4NamedReport(line, layout.Reports()); ok {
					report = r
					return line
				}
			}
			return ""
		})
		return report
	}

	t.Run("killed", func(t *testing.T) {
		first := startDsh(t, env, bin, append([]string{"--home", root}, args...)...)
		answers(t, first, "answer from the first start")
		pid := first.pid()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := first.Process.StopTree(ctx, 0); err != nil {
			t.Fatalf("killing the host: %v", err)
		}
		if !first.await(time.Minute) {
			t.Fatalf("the killed host did not exit within a minute:\n%s", first.output())
		}

		next := startDsh(t, env, bin, append([]string{"--home", root}, args...)...)
		report := uncleanLine(t, next, "unclean-exit line naming a report under "+layout.Reports()+" and the safe-mode option")
		f, err := os.Open(report)
		if err != nil {
			t.Fatalf("the named report: %v\n%s", err, next.output())
		}
		defer f.Close()
		if err := home.CheckPrivateFile(f); err != nil {
			t.Errorf("the report is not owner-only: %v", err)
		}
		data, err := os.ReadFile(report)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "unclean exit") || !strings.Contains(string(data), "pid:     "+strconv.Itoa(pid)) {
			t.Errorf("the report names no unclean exit of process %d:\n%s", pid, data)
		}
		stopHost(t, next)
	})

	crash := func(t *testing.T, failure string) dshRun {
		t.Helper()
		layerT := need(t, "DSH_SCENARIO_HOST4_LAYER", "config layer enabling the crash component with {failure}")
		layer := filepath.Join(dir, "crash-"+failure+".yml")
		if err := os.WriteFile(layer, []byte(expand(layerT, map[string]string{"failure": failure})), 0o600); err != nil {
			t.Fatal(err)
		}
		return runDsh(t, time.Minute, env, bin, append([]string{"--home", root, "--layer", layer}, args...)...)
	}
	keep := func(t *testing.T, r dshRun) {
		t.Helper()
		entries, _ := os.ReadDir(layout.Reports())
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Logf("kept for diagnosis: stderr:\n%s\n%s holds %v", r.stderr, layout.Reports(), names)
	}

	for _, failure := range host4Failures {
		t.Run(failure, func(t *testing.T) {
			r := crash(t, failure)
			if r.code == 0 {
				t.Fatalf("the %s failure exited 0", failure)
			}
			report, ok := host4NamedReport(r.stderr, layout.Reports())
			if !ok {
				keep(t, r)
				t.Fatalf("standard error names no report under %s", layout.Reports())
			}
			f, err := os.Open(report)
			if err != nil {
				keep(t, r)
				t.Fatalf("the named report: %v", err)
			}
			defer f.Close()
			if err := home.CheckPrivateFile(f); err != nil {
				t.Errorf("the report is not owner-only: %v", err)
			}
			data, err := os.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), failure) || !strings.Contains(string(data), "goroutine ") {
				t.Errorf("the report holds no cause naming %q or no stacks:\n%s", failure, data)
			}

			// The next start is a healthy host: it prints the unclean-exit line and
			// keeps running until it is sent the termination signal.
			next := startDsh(t, env, bin, append([]string{"--home", root}, args...)...)
			next.waitFor(t, time.Minute, "unclean-exit line naming "+report+" and the safe-mode option", func(out string) string {
				for _, line := range strings.Split(out, "\n") {
					if strings.Contains(line, report) && strings.Contains(line, "--safe") {
						return line
					}
				}
				return ""
			})
			stopHost(t, next)
		})
	}

	t.Run("safe mode reaches the UI", func(t *testing.T) {
		h := startDsh(t, env, bin, append([]string{"--home", root, "--safe"}, args...)...)
		answers(t, h, "answer from the UI of the safe-mode start")
		stopHost(t, h)
	})

	t.Run("twelve failures keep ten reports", func(t *testing.T) {
		for i := range 12 {
			if r := crash(t, host4Failures[i%len(host4Failures)]); r.code == 0 {
				t.Fatalf("failure %d exited 0", i+1)
			}
		}
		entries, err := os.ReadDir(layout.Reports())
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 10 {
			t.Errorf("%d reports remain after twelve failures, want 10", len(entries))
		}
	})
}

// host4Dir is a fresh directory under a short temp name, removed at cleanup: the
// web host's control socket lives in <home>/run, and a Unix socket path is bounded
// (108 bytes on Windows and Linux), which t.TempDir under this test's long name
// exceeds on Windows ("bind: invalid argument"). A process just killed may hold a
// file for a moment on Windows, so the removal is retried for up to ten seconds.
func host4Dir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dsh-h4-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		deadline := time.Now().Add(10 * time.Second)
		for {
			err := os.RemoveAll(dir)
			if err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("removing %s: %v", dir, err)
				return
			}
			<-tick.C
		}
	})
	return dir
}

// stopHost sends h the termination signal and fails the scenario unless it exits
// within a minute.
func stopHost(t *testing.T, h *dshHost) {
	t.Helper()
	if err := terminate(t, h); err != nil {
		t.Fatalf("the termination signal: %v", err)
	}
	if !h.await(time.Minute) {
		t.Fatalf("the host did not exit within a minute of the termination signal:\n%s", h.output())
	}
}
