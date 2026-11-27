//go:build scenario

package scenarios

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/boot/profile until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
)

// TestTwoProcessesOnOneHomeAndOneProfile: two processes on one home and one profile
// (areas/boot.md DH-E-BOOT-2; DH-D-BOOT-7, DH-D-LOADER-13). Guarded; executed in
// DH-S-21 (step 21.13); authored in DH-S-6.
//
// Given one temporary home, processes A and B running the same profile (dsh web) and
// process C running another profile (headless) with its own layer, each built with a
// control hook that applies an edit through config.Service.Apply, when (1) A changes
// probe1, (2) A and B edit different fields of probe2 at the same moment, (3) a syntax
// error is saved into the shared profile layer, (4) it is fixed and (5) A is killed
// while it holds the layer lock and B then edits, then after (1) B reports the change
// within two seconds and C reports none; after (2) both fields are in the file (one
// edit may have been retried after a stale-source refusal, none is lost); after (3)
// A and B both report the same file:line:col rejection and neither restarted; after
// (4) both apply the fix; after (5) B's edit succeeds within the lock bound.
//
// Inputs are shell command lines over {home}, {proc} (a, b or c) and {edit}:
// DSH_SCENARIO_BOOT2_START starts process {proc} in the foreground,
// DSH_SCENARIO_BOOT2_HOOK applies the edit {edit} ("<id>.<field>=<value>") through
// {proc}'s control hook, DSH_SCENARIO_BOOT2_INSPECT prints {proc}'s probe report
// (as DH-E-LOADER-1 parses it), DSH_SCENARIO_BOOT2_REJECTION prints {proc}'s last
// reload rejection, DSH_SCENARIO_BOOT2_HOLD makes {proc} take the layer lock and keep
// it, DSH_SCENARIO_BOOT2_KILL kills {proc} without a clean stop, and
// DSH_SCENARIO_BOOT2_PROFILE names the shared profile.
//
//dsh:scenario DH-E-BOOT-2
func TestTwoProcessesOnOneHomeAndOneProfile(t *testing.T) {
	name := need(t, "DSH_SCENARIO_BOOT2_PROFILE", "the profile processes a and b share")
	home := filepath.Join(t.TempDir(), "home")
	with := func(proc, edit string) map[string]string {
		return map[string]string{"home": home, "proc": proc, "edit": edit}
	}
	startT := need(t, "DSH_SCENARIO_BOOT2_START", "command starting process {proc} on {home}")
	hookT := need(t, "DSH_SCENARIO_BOOT2_HOOK", "command applying {edit} through {proc}'s control hook")
	inspectT := need(t, "DSH_SCENARIO_BOOT2_INSPECT", "command printing {proc}'s probe report")
	rejectT := need(t, "DSH_SCENARIO_BOOT2_REJECTION", "command printing {proc}'s last reload rejection")
	holdT := need(t, "DSH_SCENARIO_BOOT2_HOLD", "command making {proc} take and keep the layer lock")
	killT := need(t, "DSH_SCENARIO_BOOT2_KILL", "command killing process {proc} without a clean stop")
	hook := func(proc, edit string) (string, error) { return shell(t, expand(hookT, with(proc, edit))) }
	inspect := func(proc string) string { return expand(inspectT, with(proc, "")) }

	patch := profile.LayerPath(home, name)
	if err := os.MkdirAll(filepath.Dir(patch), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(patch, []byte("# shared profile layer\n[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	procs := map[string]*running{}
	for _, p := range []string{"a", "b", "c"} {
		procs[p] = startShell(t, expand(startT, with(p, "")))
	}
	before := map[string]map[string]probeState{}
	for _, p := range []string{"a", "b", "c"} {
		before[p] = eventually(t, inspect(p), 30*time.Second, func(s map[string]probeState) bool {
			return s["probe1"].pid != 0 && s["probe2"].pid != 0
		})
	}

	// (1)
	if out, err := hook("a", "probe1.value=one"); err != nil {
		t.Fatalf("A's edit failed: %v\n%s", err, out)
	}
	eventually(t, inspect("b"), 2*time.Second, func(s map[string]probeState) bool {
		return strings.Contains(s["probe1"].config, "one")
	})
	if now := probes(t, inspect("c")); now["probe1"] != before["c"]["probe1"] {
		t.Errorf("C, on another profile, changed: %+v -> %+v", before["c"]["probe1"], now["probe1"])
	}

	// (2)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	for proc, edit := range map[string]string{"a": "probe2.left=L", "b": "probe2.right=R"} {
		wg.Add(1)
		go func(proc, edit string) {
			defer wg.Done()
			var err error
			for try := 0; try < 5; try++ {
				var out string
				if out, err = hook(proc, edit); err == nil || !strings.Contains(out, "config/stale") {
					break
				}
			}
			results <- err
		}(proc, edit)
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Errorf("a concurrent edit was lost: %v", err)
		}
	}
	data, err := os.ReadFile(patch)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(data); !strings.Contains(text, "L") || !strings.Contains(text, "R") {
		t.Errorf("the profile layer lost a field:\n%s", text)
	}

	// (3)
	settled := map[string]map[string]probeState{"a": probes(t, inspect("a")), "b": probes(t, inspect("b"))}
	if err := os.WriteFile(patch, []byte("- id: probe1\n  merge: {value: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	position := regexp.MustCompile(regexp.QuoteMeta(patch) + `:\d+:\d+`)
	rejection := func(proc string) string {
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		deadline := time.Now().Add(5 * time.Second)
		for {
			out, _ := shell(t, expand(rejectT, with(proc, "")))
			if m := position.FindString(out); m != "" {
				return m
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s reported no positioned rejection: %s", proc, out)
			}
			<-tick.C
		}
	}
	if ra, rb := rejection("a"), rejection("b"); ra != rb {
		t.Errorf("A and B report different rejections: %s vs %s", ra, rb)
	}
	for _, p := range []string{"a", "b"} {
		now := probes(t, inspect(p))
		for id, s := range settled[p] {
			if now[id].pid != s.pid || now[id].starts != s.starts {
				t.Errorf("%s restarted %s after a bad layer: %+v -> %+v", p, id, s, now[id])
			}
		}
	}

	// (4)
	if err := os.WriteFile(patch, []byte("- id: probe1\n  merge:\n    value: fixed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a", "b"} {
		eventually(t, inspect(p), 2*time.Second, func(s map[string]probeState) bool {
			return strings.Contains(s["probe1"].config, "fixed")
		})
	}

	// (5)
	mustShell(t, expand(holdT, with("a", "")))
	mustShell(t, expand(killT, with("a", "")))
	select {
	case <-procs["a"].done:
	case <-time.After(10 * time.Second):
		t.Fatal("A did not die")
	}
	began := time.Now()
	var out string
	for try := 0; try < 3; try++ {
		if out, err = hook("b", "probe1.value=after-kill"); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("B's edit after A died holding the lock failed: %v\n%s", err, out)
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("B's edit took %s, beyond the lock bound", took)
	}
}
