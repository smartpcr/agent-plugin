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
	"regexp"
	"slices"
	"sort"
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
// every editor save writes back a version already acknowledged, an edit reported
// applied and then missing is one an editor save overwrote with an earlier
// acknowledged version, the final file is well formed (unique marker lines then the
// anchor) and its SHA-256 is the version an edit returned or an editor save logged
// for a write no other write began after (the last acknowledged version); and when
// one process is killed while it holds the file lock (frozen, its lock probed, then
// killed), the other's next edit takes effect on disk within the lock timeout.
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
// until stop exists, each save writing back the whole of what it read, logging each
// save as "<start> <end> <sha256>" (RFC 3339 times); DSH_SCENARIO_FS3_LAYER a layer template over {endpoint} and {locks}
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

	var edits []fs3Ack
	var stale int
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
			edits = append(edits, fs3Ack{what: "edit " + c.Tag, start: fs3Time(t, o.CalledAt), end: fs3Time(t, o.DoneAt), sum: fs3Version(t, c.Tag, o.Raw), marker: c.Tag})
		}
	}
	for _, p := range procs {
		for i := range fs3Edits {
			if tag := fmt.Sprintf("%s%04d", p.name, i); !called[tag] {
				t.Fatalf("edit %s was never called: process %s ran %d of its %d edits", tag, p.name, len(p.outs), fs3Edits)
			}
		}
	}
	finalBytes, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	v, err := fs3CheckVersions(fs3Anchor+"\n", edits, editorSaves, finalBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d edits applied, %d refused %s, %d applied then overwritten by an editor save of a version without them; %d editor saves",
		len(edits), stale, codeStaleVersion, v.lost, len(editorSaves))
	if stale == 0 {
		t.Logf("no edit was refused %s: the processes and the editor did not contend", codeStaleVersion)
	}
	got := fs3Hash(t, file)
	t.Logf("the final file is the version %s acknowledged by %s", got, v.last)
	t.Logf("file before %s, after %s", before, got)

	fs3KillLeg(t, procs[0].h, procs[0].session, procs[0].m, procs[1].h, procs[1].session, procs[1].m, file, locks, lockTimeout)
}

// fs3KillLeg kills a while it holds the file lock and requires b's next edit to
// take effect within the lock timeout. Holding is observed, not assumed: a is
// frozen (fs3Freeze), every lock file under locks is probed with a non-blocking
// exclusive lock (fs3LockHeld), and a is killed while frozen only when one is
// held; otherwise a resumes and is frozen again a moment later. b is idle and the
// editor stopped, so the held lock is a's.
func fs3KillLeg(t *testing.T, a *sdkHost, aSession string, am *scriptedModel, b *sdkHost, bSession string, bm *scriptedModel, file, locks string, timeout time.Duration) {
	t.Helper()
	for i := range fs3Batch {
		am.script(fs3Edit(file, fmt.Sprintf("k%04d", i)))
	}
	a.prompt(t, aSession, "Run the scripted calls.")
	waitUntil(t, time.Minute, "the killed process's first edit", func() bool { return am.pending() < fs3Batch-1 })

	pid := a.Process.Pid()
	held, tries := "", 0
	// Each refreeze waits for a tick, so a runs between probes.
	tick := time.NewTicker(3 * time.Millisecond)
	defer tick.Stop()
	for deadline := time.Now().Add(time.Minute); held == "" && time.Now().Before(deadline) && am.pending() > 0; tries++ {
		resume, err := fs3Freeze(pid)
		if err != nil {
			t.Fatalf("freeze process %d: %v", pid, err)
		}
		files, err := filepath.Glob(filepath.Join(locks, "*.lock"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			ok, err := fs3LockHeld(f)
			if err != nil {
				t.Fatalf("probe lock %s: %v", f, err)
			}
			if ok {
				held = f
				break
			}
		}
		if held != "" {
			break
		}
		if err := resume(); err != nil {
			t.Fatalf("resume process %d: %v", pid, err)
		}
		<-tick.C
	}
	if held == "" {
		t.Fatalf("process %d was never observed holding a file lock under %s in %d freezes", pid, locks, tries)
	}
	if err := a.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	t.Logf("killed process %d while it held %s (freeze %d), with %d of its edits not made", pid, filepath.Base(held), tries+1, am.pending())

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
	// The survivor's edit is on disk: the file is the version it returned and holds
	// its marker, and the lock the killed process held is free again.
	if v, got := fs3Version(t, "after-kill", outs[0].Raw), fs3Hash(t, file); v != got {
		t.Fatalf("the edit after the kill returned version %s, but the file is %s", v, got)
	}
	if !slices.Contains(fs3Read(t, file), "after-kill") {
		t.Fatal("the edit after the kill succeeded, but its marker is not in the file")
	}
	if still, err := fs3LockHeld(held); err != nil || still {
		t.Fatalf("the killed process's lock %s is still held (%v) after the other process's edit", held, err)
	}
	t.Logf("the other process edited %v after the kill (lock timeout %v)", took, timeout)
}

// fs3Edit is one edit inserting marker before the anchor.
func fs3Edit(file, marker string) scriptedCall {
	return scriptedCall{Name: "edit", Tag: marker, Args: map[string]any{
		"file_path": file, "old_string": fs3Anchor, "new_string": marker + "\n" + fs3Anchor,
	}}
}

// fs3Ack is one acknowledged version of the file: what wrote it, when (the write
// call's start and acknowledgement) and the SHA-256 of the whole file; marker is
// the marker an edit inserted.
type fs3Ack struct {
	what, sum, marker string
	start, end        time.Time
}

// fs3Verdict is what fs3CheckVersions established: how many applied edits an
// editor save overwrote, and which acknowledgement the final file is.
type fs3Verdict struct {
	lost int
	last string
}

// fs3CheckVersions checks the file's history against what dsh acknowledged; it
// takes nothing on the editor's word.
//
// Every version's content is derived from start and dsh's edits alone: each
// successful edit inserts its marker before the anchor of a version already
// derived, and its returned SHA-256 must be that result's digest. An edit whose
// version no derived version yields is an invented or torn write.
//
// An editor save writes back the whole of a version it read, so its digest must
// be a version derived from dsh's edits (a save never vouches for itself or for
// another save), produced before the save ended.
//
// An edit reported applied whose marker is missing from final is permitted only
// when an editor save ended after the edit began and wrote back a derived version
// that lacks the marker: the editor overwrote it with an earlier version, and
// dsh lost nothing.
//
// final must be a derived version and that of an acknowledgement no other
// acknowledged write began after (the last acknowledged version).
func fs3CheckVersions(start string, edits []fs3Ack, saves []fs3Save, final []byte) (fs3Verdict, error) {
	content := map[string]string{fs3Sum(start): start}
	since := map[string]time.Time{fs3Sum(start): {}}
	order := []string{fs3Sum(start)}
	pending := append([]fs3Ack(nil), edits...)
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].end.Before(pending[j].end) })
	for len(pending) > 0 {
		var rest []fs3Ack
		for _, e := range pending {
			found := false
			for k := len(order) - 1; k >= 0 && !found; k-- {
				base := content[order[k]]
				if fs3HasLine(base, e.marker) || since[order[k]].After(e.end) {
					continue
				}
				next := strings.Replace(base, fs3Anchor, e.marker+"\n"+fs3Anchor, 1)
				if fs3Sum(next) != e.sum {
					continue
				}
				found = true
				if _, dup := content[e.sum]; !dup {
					content[e.sum], since[e.sum] = next, e.start
					order = append(order, e.sum)
				}
			}
			if !found {
				rest = append(rest, e)
			}
		}
		if len(rest) == len(pending) {
			e := rest[0]
			return fs3Verdict{}, fmt.Errorf("%s returned version %s, which is no version of the file with its marker inserted before the anchor: an invented or torn write", e.what, e.sum)
		}
		pending = rest
	}
	for _, s := range saves {
		at, ok := since[s.sum]
		if !ok {
			return fs3Verdict{}, fmt.Errorf("an editor save (%s to %s) wrote %s, which is no version dsh's edits produced: a torn or invented write", s.start, s.end, s.sum)
		}
		if at.After(s.end) {
			return fs3Verdict{}, fmt.Errorf("an editor save ending %s wrote %s, a version first written at %s", s.end, s.sum, at)
		}
	}
	finalText := string(final)
	lost := 0
	for _, e := range edits {
		if fs3HasLine(finalText, e.marker) {
			continue
		}
		excused := false
		for _, s := range saves {
			if !s.end.Before(e.start) && !fs3HasLine(content[s.sum], e.marker) {
				excused = true
				break
			}
		}
		if !excused {
			return fs3Verdict{}, fmt.Errorf("%s returned version %s and is missing from the final file, and no editor save after it wrote back a version without it: the edit was lost", e.what, e.sum)
		}
		lost++
	}
	got := fs3Sum(finalText)
	if _, ok := content[got]; !ok {
		return fs3Verdict{}, fmt.Errorf("the final file %s is no version dsh's edits produced", got)
	}
	acks := append([]fs3Ack{{what: "the starting file", sum: fs3Sum(start)}}, edits...)
	for _, s := range saves {
		acks = append(acks, fs3Ack{what: "an editor save", start: s.start, end: s.end, sum: s.sum})
	}
	for _, c := range acks {
		if c.sum != got {
			continue
		}
		later := false
		for _, x := range acks {
			if x.start.After(c.end) {
				later = true
				break
			}
		}
		if !later {
			return fs3Verdict{lost: lost, last: c.what}, nil
		}
	}
	return fs3Verdict{}, fmt.Errorf("the final file %s is not the version of the last acknowledged write (edit result or editor save)", got)
}

// fs3HasLine reports whether text holds line as a whole line.
func fs3HasLine(text, line string) bool {
	return strings.HasPrefix(text, line+"\n") || strings.Contains(text, "\n"+line+"\n")
}

// fs3Sum is the hex SHA-256 of text.
func fs3Sum(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// fs3VersionRE is the version a successful edit result names (world/fs/tools:
// "Edited file <path> (version sha256:<hex>)").
var fs3VersionRE = regexp.MustCompile(`\(version sha256:([0-9a-f]{64})\)`)

// fs3Version is the SHA-256 of the whole file a successful edit result reports.
func fs3Version(t *testing.T, tag string, raw []byte) string {
	t.Helper()
	m := fs3VersionRE.FindSubmatch(raw)
	if m == nil {
		t.Fatalf("successful edit %s returned no sha256 version: %s", tag, raw)
	}
	return string(m[1])
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
