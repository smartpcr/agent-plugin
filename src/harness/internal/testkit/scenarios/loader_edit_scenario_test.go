//go:build scenario

package scenarios

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
)

// probeLine is one line of a probe report: "<id> pid=<pid> starts=<n> config=<json>"
// (the scenario build's probe entries print it through the inspection command).
var probeLine = regexp.MustCompile(`(?m)^(\S+) pid=(\d+) starts=(\d+) config=(.*)$`)

// probeState is what a probe reports.
type probeState struct {
	pid, starts int
	config      string
}

// probes runs the inspection command and parses every probe it reports.
func probes(t *testing.T, line string) map[string]probeState {
	t.Helper()
	out := mustShell(t, line)
	states := map[string]probeState{}
	for _, m := range probeLine.FindAllStringSubmatch(out, -1) {
		pid, _ := strconv.Atoi(m[2])
		starts, _ := strconv.Atoi(m[3])
		states[m[1]] = probeState{pid: pid, starts: starts, config: strings.TrimSpace(m[4])}
	}
	return states
}

// eventually polls the inspection command until ok holds or within passes.
func eventually(t *testing.T, line string, within time.Duration, ok func(map[string]probeState) bool) map[string]probeState {
	t.Helper()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	deadline := time.Now().Add(within)
	for {
		states := probes(t, line)
		if ok(states) {
			return states
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %s the probes report %+v", within, states)
		}
		<-tick.C
	}
}

// saveLikeAnEditor writes text beside path and renames it over path.
func saveLikeAnEditor(t *testing.T, path, text string) {
	t.Helper()
	tmp := path + ".editor-tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

const (
	// The user layer: above the patch, never written by dsh; it touches no probe.
	loaderReadOnly = `# read-only to dsh: a person's layer
[]
`
	// The profile patch over the scenario build's bundle, which inserts probe-a and probe-b.
	loaderPatch = `# the user's patch; this comment must survive every write
- id: probe-a
  merge:
    value: one
`
)

// TestRunningHarnessFollowsProfilePatchEdit: a running harness follows an edit to
// its profile patch (libs/cordis-plugins.md DH-E-LOADER-1; DH-D-LOADER-2,
// DH-D-LOADER-7, DH-D-LOADER-11; DH-D-BOOT-7). Executed in DH-S-17 (step 17.7),
// on Windows 11 and on Linux; authored in DH-S-6.
//
// Given the scenario build of the harness binary whose bundle inserts two probe
// entries, probe-a and probe-b, that report their config and start count through the
// inspection command, a temporary home holding one read-only layer (the user layer,
// never written by dsh) and one writable profile patch with comments, and reload
// enabled, when (1) the binary starts
// against the home, (2) the patch is saved the way an editor does (temporary file
// and rename) changing probe-a's config, (3) invalid YAML is saved, (4) a corrected
// file is saved, (5) probe-b is disabled through the harness's own configuration
// command and (6) the harness is stopped, then after (2) probe-a reports the new
// value within two seconds with the process id and probe-b's start count unchanged;
// after (3) a diagnostic names the file, line and column and both probes report
// their previous state; after (4) the edit applies; after (5) the patch holds an
// override for probe-b, still holds its comments and the read-only layer's hash is
// unchanged; after (6) the exit code is 0 and no temporary file remains beside the
// patch. Cleanup: the home is deleted; a process still running after ten seconds is
// killed, and the home and log are kept for diagnosis (DSH_SCENARIO_REPORT).
//
// Inputs are shell command lines over {home}, {profile} and {log}:
// DSH_SCENARIO_LOADER_START runs the harness in the foreground until stopped, writing
// its log to {log}, DSH_SCENARIO_LOADER_INSPECT prints the probe report, DSH_SCENARIO_LOADER_DISABLE
// disables probe-b through the configuration command and DSH_SCENARIO_LOADER_STOP
// stops the harness; DSH_SCENARIO_LOADER_PROFILE names the profile.
//
//dsh:scenario DH-E-LOADER-1
func TestRunningHarnessFollowsProfilePatchEdit(t *testing.T) {
	name := need(t, "DSH_SCENARIO_LOADER_PROFILE", "the profile the scenario build runs")
	dir := t.TempDir()
	vars := map[string]string{"home": filepath.Join(dir, "home"), "profile": name, "log": filepath.Join(dir, "harness.log")}
	start := expand(need(t, "DSH_SCENARIO_LOADER_START", "command running the harness on {home} with {profile} until stopped, its log written to {log}"), vars)
	inspect := expand(need(t, "DSH_SCENARIO_LOADER_INSPECT", "command printing the probe report of the harness on {home}"), vars)
	disable := expand(need(t, "DSH_SCENARIO_LOADER_DISABLE", "configuration command disabling probe-b on {home}"), vars)
	stop := expand(need(t, "DSH_SCENARIO_LOADER_STOP", "command stopping the harness on {home}"), vars)

	home := vars["home"]
	readOnly, patch := profile.UserLayerPath(home), profile.LayerPath(home, name)
	for path, text := range map[string]string{readOnly: loaderReadOnly, patch: loaderPatch} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	readOnlyHash := fileHash(t, readOnly)

	// (1)
	harness := startShell(t, start)
	t.Cleanup(func() {
		if !harness.exited() {
			_, _ = shell(t, stop)
		}
	})
	before := eventually(t, inspect, 30*time.Second, func(s map[string]probeState) bool {
		return s["probe-a"].pid != 0 && s["probe-b"].pid != 0
	})

	// (2)
	saveLikeAnEditor(t, patch, strings.Replace(loaderPatch, "value: one", "value: changed", 1))
	after := eventually(t, inspect, 2*time.Second, func(s map[string]probeState) bool {
		return strings.Contains(s["probe-a"].config, "changed")
	})
	if after["probe-a"].pid != before["probe-a"].pid {
		t.Errorf("the process changed: pid %d -> %d", before["probe-a"].pid, after["probe-a"].pid)
	}
	if after["probe-b"].starts != before["probe-b"].starts {
		t.Errorf("probe-b restarted: starts %d -> %d", before["probe-b"].starts, after["probe-b"].starts)
	}

	// (3)
	saveLikeAnEditor(t, patch, "# broken\n- id: probe-a\n  merge: {value: [\n")
	diagnostic := regexp.MustCompile(regexp.QuoteMeta(patch) + `:\d+:\d+`)
	logText := func() string { data, _ := os.ReadFile(vars["log"]); return string(data) }
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for !diagnostic.MatchString(logText()) {
		if time.Now().After(deadline) {
			t.Fatalf("no diagnostic naming %s:line:column in the log:\n%s", patch, logText())
		}
		<-tick.C
	}
	if now := probes(t, inspect); now["probe-a"] != after["probe-a"] || now["probe-b"] != after["probe-b"] {
		t.Errorf("a bad patch changed the probes: %+v -> %+v", after, now)
	}

	// (4)
	saveLikeAnEditor(t, patch, strings.Replace(loaderPatch, "value: one", "value: corrected", 1))
	eventually(t, inspect, 2*time.Second, func(s map[string]probeState) bool {
		return strings.Contains(s["probe-a"].config, "corrected")
	})

	// (5)
	mustShell(t, disable)
	data, err := os.ReadFile(patch)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(data); !strings.Contains(text, "probe-b") || !strings.Contains(text, "disabled") ||
		!strings.Contains(text, "# the user's patch; this comment must survive every write") {
		t.Errorf("the patch lacks probe-b's override or its comments:\n%s", text)
	}
	if fileHash(t, readOnly) != readOnlyHash {
		t.Error("the read-only layer changed")
	}

	// (6)
	mustShell(t, stop)
	select {
	case <-harness.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("the harness did not exit within ten seconds; home kept at %s", home)
	}
	harness.wait(t)
	entries, err := os.ReadDir(filepath.Dir(patch))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(patch) && !strings.HasSuffix(e.Name(), ".lock") {
			t.Errorf("a temporary file remains beside the patch: %s", e.Name())
		}
	}
}
