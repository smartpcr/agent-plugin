//go:build scenario

package scenarios

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// fs3Edits is the number of edit calls each process issues (areas/filesystem.md
// DH-E-FS-3).
const fs3Edits = 1_000

// fs3Batch is the number of edit calls one prompt's turn carries; the layer's
// step budget must allow it.
const fs3Batch = 100

// fs3Anchor is the line every edit inserts its marker before.
const fs3Anchor = "ANCHOR"

// TestTwoProcessesAndAnEditorChangeOneFile: two dsh processes and an outside
// editor change one file (areas/filesystem.md DH-E-FS-3; DH-D-FS-4;
// e2e-scenarios.md TC-19s1). Authored in DH-S-12 (implementation-plan Stage
// 2.8); executed in DH-S-16 on the guarded lane (Linux/WSL and Windows; macOS out
// of scope by decision platforms-windows-wsl). It needs the headless SDK launcher
// (DH-S-16).
//
// Given two built dsh processes, one session each in workspace-write on one
// scratch workspace, and the editor save script DSH_SCENARIO_FS3_EDITOR saving
// the same file throughout (write a temporary file, rename); when both sessions
// issue 1,000 edit calls on the file, each inserting a unique marker line before
// the anchor; then every edit either took effect or returned fs/stale-version,
// none is reported applied and then missing unless an editor save overlapped it
// (the editor's whole write of what it read is a sequence of whole writes, not a
// lost edit), the final file is well formed (unique marker lines then the anchor)
// and equals the last acknowledged version; and when one process is killed mid
// run, the other's next edit succeeds within the lock timeout.
//
// The edits are scripted (scripted_model_test.go) and reach the file through
// dsh's whole tool pipeline. Operator decision fs-scripted-model-vs-replay: the
// scripted endpoint switches to the replay provider when DH-S-16 lands. Each
// launched process gets its own home (launcher.Start); "one harness home" is met
// for the file lock by the layer's {locks} directory both processes share, and
// this deviation is recorded in evidence/DH-S-12.md.
//
// Cleanup: the editor and both processes are stopped and the workspace removed;
// when a reported edit is missing, both session logs are kept (keepExport).
//
// Inputs: DSH_SCENARIO_FS3_EDITOR an executable run as "<editor> <file> <log>
// <stop>" that saves file with an editor's save sequence every few milliseconds
// until stop exists, logging each save as "<start> <end> <sha256>" (RFC 3339
// times); DSH_SCENARIO_FS3_LAYER a layer template over {endpoint} and {locks}
// registering route DSH_SCENARIO_FS3_ROUTE on {endpoint}, enabling edit, placing
// file locks under {locks} and allowing fs3Batch+1 steps a turn;
// DSH_SCENARIO_FS3_ROUTE and DSH_SCENARIO_FS3_MODEL the route and model;
// DSH_SCENARIO_FS3_LOCK_TIMEOUT the configured lock timeout (a Go duration).
//
//dsh:scenario DH-E-FS-3
func TestTwoProcessesAndAnEditorChangeOneFile(t *testing.T) {
	editor := need(t, "DSH_SCENARIO_FS3_EDITOR", "the editor save script")
	layerT := need(t, "DSH_SCENARIO_FS3_LAYER", "a layer over {endpoint} and {locks}")
	route := &protocol.ModelRoute{
		Provider: need(t, "DSH_SCENARIO_FS3_ROUTE", "the route the layer registers on the scripted endpoint"),
		Model:    need(t, "DSH_SCENARIO_FS3_MODEL", "a model of that route"),
	}
	lockTimeout, err := time.ParseDuration(need(t, "DSH_SCENARIO_FS3_LOCK_TIMEOUT", "the configured lock timeout"))
	if err != nil {
		t.Fatalf("DSH_SCENARIO_FS3_LOCK_TIMEOUT: %v", err)
	}
	bin := dshBinary(t)

	ws, locks := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(ws, "shared.txt")
	if err := os.WriteFile(file, []byte(fs3Anchor+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := fs3Hash(t, file)

	type proc struct {
		name    string
		m       *scriptedModel
		h       *sdkHost
		session string
		outs    []*toolOutcome
	}
	procs := []*proc{{name: "a"}, {name: "b"}}
	for _, p := range procs {
		p.m = startScriptedModel(t)
		layer := writeLayer(t, expand(layerT, map[string]string{"endpoint": p.m.URL, "locks": filepath.ToSlash(locks)}))
		p.h = startSDK(t, bin, nil, []string{"--layer", layer})
		p.session = p.h.createIn(t, route, ws, "workspace-write")
		p.h.keepExport(t, p.session)
	}

	saves := fs3StartEditor(t, editor, file)
	t.Run("edits", func(t *testing.T) {
		for _, p := range procs {
			t.Run(p.name, func(t *testing.T) {
				t.Parallel()
				for first := 0; first < fs3Edits; first += fs3Batch {
					for i := first; i < min(first+fs3Batch, fs3Edits); i++ {
						p.m.script(fs3Edit(file, fmt.Sprintf("%s%04d", p.name, i)))
					}
					p.outs = append(p.outs, toolOutcomes(t, p.h.runPrompt(t, p.session, "Run the scripted calls.", 10*time.Minute))...)
					if n := p.m.pending(); n != 0 {
						t.Fatalf("process %s left %d of its scripted edits uncalled after the batch from %d", p.name, n, first)
					}
				}
			})
		}
	})
	editorSaves := saves()
	if t.Failed() {
		return
	}

	final := fs3Read(t, file)
	present := map[string]bool{}
	for i, line := range final[:len(final)-1] {
		if present[line] || !strings.HasPrefix(line, "a") && !strings.HasPrefix(line, "b") {
			t.Fatalf("line %d of the final file, %q, is not a unique marker: the file is not a whole write", i+1, line)
		}
		present[line] = true
	}
	if final[len(final)-1] != fs3Anchor {
		t.Fatalf("the final file does not end with the anchor: %q", final[len(final)-1])
	}

	var applied, stale, lostToEditor int
	var lastAt time.Time
	var lastMarker string
	called := map[string]bool{}
	for _, p := range procs {
		for _, o := range p.outs {
			c, ok := p.m.call(o.ID)
			if !ok {
				t.Fatalf("call %s was not scripted", o.ID)
			}
			if o.calls != 1 || o.results != 1 || called[c.Tag] || !strings.HasPrefix(c.Tag, p.name) {
				t.Fatalf("edit %s: %d calls, %d results, seen before %v, by process %s; want each marker of its own process once with one result", c.Tag, o.calls, o.results, called[c.Tag], p.name)
			}
			called[c.Tag] = true
			if o.IsError {
				if !o.has(codeStaleVersion) {
					t.Fatalf("edit %s returned neither success nor %s: %s", c.Tag, codeStaleVersion, o.Raw)
				}
				stale++
				continue
			}
			applied++
			from, to := fs3Time(t, o.CalledAt), fs3Time(t, o.DoneAt)
			if to.After(lastAt) {
				lastAt, lastMarker = to, c.Tag
			}
			if present[c.Tag] {
				continue
			}
			if !fs3Overlaps(editorSaves, from, to) {
				t.Fatalf("edit %s was reported applied, is missing from the final file, and no editor save overlapped it", c.Tag)
			}
			lostToEditor++
		}
	}
	for _, p := range procs {
		for i := range fs3Edits {
			if tag := fmt.Sprintf("%s%04d", p.name, i); !called[tag] {
				t.Fatalf("edit %s was never called: process %s ran %d of its %d edits", tag, p.name, len(p.outs), fs3Edits)
			}
		}
	}
	t.Logf("%d edits applied, %d refused %s, %d applied then overwritten by an overlapping editor save; %d editor saves",
		applied, stale, codeStaleVersion, lostToEditor, len(editorSaves))
	if stale == 0 {
		t.Logf("no edit was refused %s: the processes and the editor did not contend", codeStaleVersion)
	}
	got := fs3Hash(t, file)
	if last := editorSaves[len(editorSaves)-1]; last.end.After(lastAt) {
		if got != last.sum {
			t.Fatalf("the final file %s is not the editor's last acknowledged save %s", got, last.sum)
		}
	} else if !present[lastMarker] {
		t.Fatalf("the final file lacks %s, the last acknowledged edit", lastMarker)
	}
	t.Logf("file before %s, after %s", before, got)

	fs3KillLeg(t, procs[0].h, procs[0].session, procs[0].m, procs[1].h, procs[1].session, procs[1].m, file, lockTimeout)
}

// fs3KillLeg kills a mid-run and requires b's next edit to succeed within the
// lock timeout.
func fs3KillLeg(t *testing.T, a *sdkHost, aSession string, am *scriptedModel, b *sdkHost, bSession string, bm *scriptedModel, file string, timeout time.Duration) {
	t.Helper()
	for i := range fs3Batch {
		am.script(fs3Edit(file, fmt.Sprintf("k%04d", i)))
	}
	a.prompt(t, aSession, "Run the scripted calls.")
	waitUntil(t, time.Minute, "the killed process's first edit", func() bool { return am.pending() < fs3Batch-1 })
	if err := a.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	t.Logf("killed process %d with %d of its edits not made", a.Process.Pid(), am.pending())

	bm.script(fs3Edit(file, "after-kill"))
	started := time.Now()
	outs := toolOutcomes(t, b.runPrompt(t, bSession, "Run the scripted call.", timeout+time.Minute))
	took := time.Since(started)
	if len(outs) != 1 || outs[0].IsError {
		t.Fatalf("the edit after the kill answered %+v, want one success", outs)
	}
	if took > timeout+10*time.Second {
		t.Fatalf("the edit after the kill took %v, over the lock timeout %v", took, timeout)
	}
	t.Logf("the other process edited %v after the kill (lock timeout %v)", took, timeout)
}

// fs3Edit is one edit inserting marker before the anchor.
func fs3Edit(file, marker string) scriptedCall {
	return scriptedCall{Name: "edit", Tag: marker, Args: map[string]any{
		"file_path": file, "old_string": fs3Anchor, "new_string": marker + "\n" + fs3Anchor,
	}}
}

// fs3Save is one editor save.
type fs3Save struct {
	start, end time.Time
	sum        string
}

// fs3StartEditor starts the editor script; the returned func stops it and reads
// its log of saves.
func fs3StartEditor(t *testing.T, editor, file string) func() []fs3Save {
	t.Helper()
	dir := t.TempDir()
	logPath, stop := filepath.Join(dir, "saves.log"), filepath.Join(dir, "stop")
	cmd := exec.CommandContext(t.Context(), editor, file, logPath, stop)
	out, err := os.Create(filepath.Join(dir, "editor.out"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the editor script: %v", err)
	}
	var once sync.Once
	var saves []fs3Save
	stopIt := func() []fs3Save {
		once.Do(func() {
			_ = os.WriteFile(stop, nil, 0o600)
			if err := cmd.Wait(); err != nil {
				t.Errorf("the editor script: %v", err)
			}
			_ = out.Close()
			f, err := os.Open(logPath)
			if err != nil {
				t.Fatalf("the editor's log: %v", err)
			}
			defer f.Close()
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				fields := strings.Fields(sc.Text())
				if len(fields) != 3 {
					t.Fatalf("editor log line %q: want <start> <end> <sha256>", sc.Text())
				}
				saves = append(saves, fs3Save{fs3Time(t, fields[0]), fs3Time(t, fields[1]), fields[2]})
			}
			if len(saves) == 0 {
				t.Fatal("the editor script logged no save")
			}
		})
		return saves
	}
	t.Cleanup(func() {
		stopIt()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	})
	return stopIt
}

// fs3Overlaps reports whether a save's window overlaps from..to.
func fs3Overlaps(saves []fs3Save, from, to time.Time) bool {
	for _, s := range saves {
		if !s.end.Before(from) && !s.start.After(to) {
			return true
		}
	}
	return false
}

func fs3Time(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("time %q: %v", s, err)
	}
	return v
}

func fs3Read(t *testing.T, file string) []string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), "\n")
}

func fs3Hash(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
