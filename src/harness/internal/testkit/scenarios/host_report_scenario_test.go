//go:build scenario

package scenarios

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
)

// host4Failures are the three ways the test-only crash component fails on request.
var host4Failures = []string{"load", "panic", "fault"}

// host4Report is the report path standard error names.
var host4Report = regexp.MustCompile(`(\S+\.(?:txt|log|json))`)

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
// Inputs: DSH_SCENARIO_HOST4_LAYER is the config layer text enabling the crash
// component, with {failure} standing for load, panic or fault; DSH_SCENARIO_HOST4_ARGS
// are the ";"-separated arguments after the global options that start the host
// (for example "web"); DSH_SCENARIO_HOST4_READY is a shell command line over {home}
// that exits 0 once the UI of a host running on {home} answers. A running host is
// stopped with the termination signal: SIGTERM on POSIX, closing its console on
// Windows.
//
//dsh:scenario DH-E-HOST-4
func TestDyingHostLeavesReadableReport(t *testing.T) {
	layerT := need(t, "DSH_SCENARIO_HOST4_LAYER", "config layer enabling the crash component with {failure}")
	args := needList(t, "DSH_SCENARIO_HOST4_ARGS", "arguments starting the host, ';'-separated")
	ready := need(t, "DSH_SCENARIO_HOST4_READY", "command exiting 0 once the UI of the host on {home} answers")
	bin := dshBinary(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "home")
	layout := home.New(root)
	env := []string{"DSH_HOME=" + root}

	crash := func(t *testing.T, failure string) dshRun {
		t.Helper()
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
			m := host4Report.FindStringSubmatch(r.stderr)
			if m == nil || !strings.HasPrefix(filepath.Clean(m[1]), filepath.Clean(layout.Reports())) {
				keep(t, r)
				t.Fatalf("standard error names no report under %s", layout.Reports())
			}
			report := m[1]
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
		check := expand(ready, map[string]string{"home": root})
		h.waitFor(t, 30*time.Second, "answer from the UI of the safe-mode start", func(string) string {
			if _, err := shell(t, check); err == nil {
				return "ready"
			}
			return ""
		})
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
