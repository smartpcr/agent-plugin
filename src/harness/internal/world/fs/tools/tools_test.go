package tools_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/meter"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	spilllocal "github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/policy"
	htools "github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/ops"
	fstools "github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

var bg = context.Background()

// dirs is the session's working directory: the workspace root.
type dirs struct{ root string }

func (d dirs) Access(session.ID) (workdir.Access, error) { return d, nil }
func (d dirs) Current() (workdir.WorkingDirectory, error) {
	return workdir.WorkingDirectory{Path: d.root, Root: d.root}, nil
}
func (d dirs) Path() (string, error) { return d.root, nil }
func (d dirs) Root() (string, error) { return d.root, nil }

// rig is the real tool pipeline with the six tools mounted over the local
// provider, fenced to a workspace in t.TempDir(), with a local spill store and a
// SQLite session log in a temp home.
type rig struct {
	t       *testing.T
	ws      string
	root    *cordis.Context
	svc     htools.Service
	st      *sqlite.Store
	spill   spill.Store
	n       int
	handles map[session.ID]session.Handle
	seqs    map[session.ID]event.Seq // the seq of each session's tool/result
}

// rigOptions vary the rig: wrap wraps the local provider, cfg is the tools'
// configuration, policy mounts the real spill policy as the pipeline's seat.
type rigOptions struct {
	wrap   func(fs.FS) fs.FS
	cfg    fstools.Config
	policy bool
}

func newRig(t *testing.T) *rig { return newRigWith(t, rigOptions{}) }

func newRigWith(t *testing.T, opt rigOptions) *rig {
	t.Helper()
	ws := t.TempDir()
	if real, err := filepath.EvalSymlinks(ws); err == nil {
		ws = real
	}
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	f, err := root.Use(scope.Component, cordis.None{})
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	var tab scope.Table
	g, err := root.Inject([]string{scope.TableKey.Name()}, func(c *cordis.Context) error {
		var err error
		tab, err = scope.TableKey.Get(c)
		return err
	})
	if err == nil {
		err = g.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	svc := core.NewWith(root, tab, core.Config{Checkpoint: checkpoint.Default{}})

	l := home.New(t.TempDir())
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.OpenHome(bg, l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	sp, err := spilllocal.New(l, spilllocal.DefaultConfig, spilllocal.WithLineage(spilllocal.ForkLineage(st)))
	if err != nil {
		t.Fatal(err)
	}

	wd := dirs{ws}
	var provider fs.FS = local.New()
	if opt.wrap != nil {
		provider = opt.wrap(provider)
	}
	o := ops.New(root, provider, &sandboxtest.Resolver{Default: sandbox.WorkspaceWrite, Workdir: wd}, ops.Options{Workdir: wd})
	if _, err := fstools.Mount(root, svc, o, sp, opt.cfg); err != nil {
		t.Fatal(err)
	}
	if opt.policy {
		if _, _, err := policy.Mount(root, svc, sp, policy.Config{MaxInlineTokens: policy.MinInlineTokens, ReadTool: "spill_read"}); err != nil {
			t.Fatal(err)
		}
	}
	return &rig{t: t, ws: ws, root: root, svc: svc, st: st, spill: sp,
		handles: map[session.ID]session.Handle{}, seqs: map[session.ID]event.Seq{}}
}

// run executes one call in a new session step and returns its result and the
// raw tool/result event the log holds for it.
func (r *rig) run(name string, args map[string]any) (htools.Result, string) {
	r.t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		r.t.Fatal(err)
	}
	r.n++
	id := session.ID(fmt.Sprintf("s-fs-tools-%d", r.n))
	c := htools.CallRequest{ID: "c1", Name: name, Arguments: string(raw)}
	h := r.openStep(id, c)
	v, err := r.svc.Resolve(bg, htools.Target{Agent: agent.Ref{Session: id, Incarnation: 1}})
	if err != nil {
		r.t.Fatal(err)
	}
	defer v.Release()
	res, err := r.svc.ExecuteBatch(bg, htools.Batch{View: v, Log: h, Calls: []htools.CallRequest{c}})
	if err != nil {
		r.t.Fatal(err)
	}
	if len(res.Results) != 1 {
		r.t.Fatalf("%d results", len(res.Results))
	}
	evs, err := h.Read(bg, 0, 0)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, ev := range evs {
		if ev.Type() == "tool/result" {
			r.seqs[id] = ev.Seq()
			return res.Results[0], ev.Raw()
		}
	}
	r.t.Fatal("no tool/result was logged")
	return htools.Result{}, ""
}

func (r *rig) openStep(id session.ID, c htools.CallRequest) session.Handle {
	r.t.Helper()
	h, err := session.NewService(r.root, r.st, session.Config{}).Create(bg, nil, session.CreateOptions{Header: session.Header{ID: string(id)}})
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = h.Close(context.Background()) })
	r.handles[id] = h
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("go"))
	if err != nil {
		r.t.Fatal(err)
	}
	asked, err := vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, vocab.ToolUseBlock(c.ID, c.Name, json.RawMessage(c.Arguments)))
	if err != nil {
		r.t.Fatal(err)
	}
	for _, mk := range []func() (event.Draft, error){
		func() (event.Draft, error) { return event.TurnStartType.Draft(event.TurnStart{Turn: 1}) },
		func() (event.Draft, error) { return event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1}) },
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
		func() (event.Draft, error) {
			return event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: asked})
		},
	} {
		d, err := mk()
		if err != nil {
			r.t.Fatal(err)
		}
		if _, err := h.Append(bg, d); err != nil {
			r.t.Fatal(err)
		}
	}
	return h
}

func (r *rig) path(name string) string { return filepath.Join(r.ws, filepath.FromSlash(name)) }

func (r *rig) put(name, content string) {
	r.t.Helper()
	p := r.path(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) disk(name string) string {
	r.t.Helper()
	b, err := os.ReadFile(r.path(name))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b)
}

// jsonText is s as it appears inside a JSON string (a Windows path's
// backslashes are escaped in the log).
func jsonText(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func digest(s string) string {
	d := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(d[:])
}

// Scenario: Tools change files exactly as logged (TC-03b). Given a workspace in
// t.TempDir(), When write, edit and apply_patch run through the tool pipeline,
// Then the files on disk equal the expected bytes and each tool result in the
// log names the same path and version.
func TestToolsChangeFilesExactlyAsLogged(t *testing.T) {
	r := newRig(t)
	r.put("crlf.txt", "one\r\ntwo\r\n")
	steps := []struct {
		tool  string
		args  map[string]any
		files map[string]string // the expected bytes on disk after the step
		named []string          // the files the result names, with their versions
	}{
		{fstools.WriteName, map[string]any{"file_path": "a.txt", "content": "alpha\nbeta\n"},
			map[string]string{"a.txt": "alpha\nbeta\n"}, []string{"a.txt"}},
		{fstools.WriteName, map[string]any{"file_path": "dir/new.txt", "content": "fresh"},
			map[string]string{"dir/new.txt": "fresh"}, []string{"dir/new.txt"}},
		{fstools.EditName, map[string]any{"file_path": "a.txt", "old_string": "beta", "new_string": "gamma"},
			map[string]string{"a.txt": "alpha\ngamma\n"}, []string{"a.txt"}},
		{fstools.EditName, map[string]any{"file_path": "crlf.txt", "old_string": "one\ntwo", "new_string": "uno\ndos"},
			map[string]string{"crlf.txt": "uno\r\ndos\r\n"}, []string{"crlf.txt"}},
		// A literal match whose replacement spans lines takes the file's CRLF.
		{fstools.EditName, map[string]any{"file_path": "crlf.txt", "old_string": "dos", "new_string": "second\nthird"},
			map[string]string{"crlf.txt": "uno\r\nsecond\r\nthird\r\n"}, []string{"crlf.txt"}},
		{fstools.ApplyPatchName, map[string]any{"input": "*** Begin Patch\n" +
			"*** Add File: b.txt\n+bee\n" +
			"*** Update File: a.txt\n@@\n alpha\n-gamma\n+delta\n" +
			"*** Update File: dir/new.txt\n*** Move to: moved.txt\n@@\n-fresh\n+moved\n" +
			"*** End Patch"},
			map[string]string{"b.txt": "bee\n", "a.txt": "alpha\ndelta\n", "moved.txt": "moved\n"}, []string{"b.txt", "a.txt", "moved.txt"}},
	}
	for i, s := range steps {
		res, logged := r.run(s.tool, s.args)
		if res.IsError {
			t.Fatalf("step %d %s: %s", i, s.tool, res.Text)
		}
		for name, want := range s.files {
			if got := r.disk(name); got != want {
				t.Fatalf("step %d %s: %s is %q, want %q", i, s.tool, name, got, want)
			}
		}
		for _, name := range s.named {
			line := r.path(name) + " (version " + digest(r.disk(name)) + ")"
			if !strings.Contains(res.Text, line) {
				t.Fatalf("step %d %s: the result does not name %q:\n%s", i, s.tool, line, res.Text)
			}
			if !strings.Contains(logged, jsonText(line)) {
				t.Fatalf("step %d %s: the logged result does not name %q:\n%s", i, s.tool, line, logged)
			}
		}
	}
	if _, err := os.Stat(r.path("dir/new.txt")); !os.IsNotExist(err) {
		t.Fatalf("the moved source still exists: %v", err)
	}
}

// A patch whose second hunk does not match changes nothing: every hunk is
// preflighted before the first write.
func TestApplyPatchPreflightsEveryHunk(t *testing.T) {
	r := newRig(t)
	r.put("a.txt", "one\n")
	res, _ := r.run(fstools.ApplyPatchName, map[string]any{"input": "*** Begin Patch\n" +
		"*** Add File: b.txt\n+bee\n" +
		"*** Update File: a.txt\n@@\n-missing\n+x\n" +
		"*** End Patch"})
	if !res.IsError || !strings.Contains(res.Text, "fs/patch-mismatch") {
		t.Fatalf("a mismatching patch: %+v", res)
	}
	if _, err := os.Stat(r.path("b.txt")); !os.IsNotExist(err) {
		t.Fatalf("the first hunk ran before the second was checked: %v", err)
	}
	if got := r.disk("a.txt"); got != "one\n" {
		t.Fatalf("a.txt is %q", got)
	}
}

// An edit whose old_string is ambiguous, or absent, changes nothing.
func TestEditRefusesAmbiguousAndMissingText(t *testing.T) {
	r := newRig(t)
	r.put("a.txt", "x x\n")
	for _, tc := range []struct {
		args map[string]any
		code string
	}{
		{map[string]any{"file_path": "a.txt", "old_string": "x", "new_string": "y"}, "fs/ambiguous-edit"},
		{map[string]any{"file_path": "a.txt", "old_string": "z", "new_string": "y"}, "fs/edit-not-found"},
	} {
		if res, _ := r.run(fstools.EditName, tc.args); !res.IsError || !strings.Contains(res.Text, tc.code) {
			t.Fatalf("%v: %+v", tc.args, res)
		}
	}
	res, _ := r.run(fstools.EditName, map[string]any{"file_path": "a.txt", "old_string": "x", "new_string": "y", "replace_all": true})
	if res.IsError || r.disk("a.txt") != "y y\n" || !strings.Contains(res.Text, "2 occurrences") {
		t.Fatalf("replace_all: %+v, %q", res, r.disk("a.txt"))
	}
}

// Text tools are UTF-8 only (impl.md sec. 9.1): read and edit refuse UTF-16
// behind a mark, Latin-1 and NUL bytes with fs/not-text, and edit writes
// nothing; UTF-8 behind a mark is read and its mark kept by an edit.
func TestTextToolsAreUTF8Only(t *testing.T) {
	r := newRig(t)
	refused := map[string]string{
		"utf16.txt":  "\xff\xfeh\x00i\x00",
		"latin1.txt": "caf\xe9\n",
		"nul.txt":    "a\x00b\n",
	}
	for name, content := range refused {
		r.put(name, content)
		if res, _ := r.run(fstools.ReadName, map[string]any{"file_path": name}); !res.IsError || !strings.Contains(res.Text, "fs/not-text") {
			t.Fatalf("read %s: %+v", name, res)
		}
		if res, _ := r.run(fstools.EditName, map[string]any{"file_path": name, "old_string": "a", "new_string": "b"}); !res.IsError || !strings.Contains(res.Text, "fs/not-text") {
			t.Fatalf("edit %s: %+v", name, res)
		}
		if got := r.disk(name); got != content {
			t.Fatalf("edit changed %s: %q", name, got)
		}
	}
	r.put("bom.txt", "\xef\xbb\xbfh\u00e9llo\n")
	if res, _ := r.run(fstools.ReadName, map[string]any{"file_path": "bom.txt"}); res.IsError || !strings.Contains(res.Text, "1: h\u00e9llo\n") {
		t.Fatalf("read a UTF-8 file with a mark: %+v", res)
	}
	if res, _ := r.run(fstools.EditName, map[string]any{"file_path": "bom.txt", "old_string": "llo", "new_string": "y"}); res.IsError || r.disk("bom.txt") != "\xef\xbb\xbfh\u00e9y\n" {
		t.Fatalf("edit a UTF-8 file with a mark: %+v, %q", res, r.disk("bom.txt"))
	}
}

// spawnRecorder puts a fake rg first and alone on PATH: any start of it appends
// to the returned log file. An in-process grep leaves the log empty.
func spawnRecorder(t *testing.T) (log string) {
	t.Helper()
	bin := t.TempDir()
	log = filepath.Join(t.TempDir(), "spawns.log")
	if runtime.GOOS == "windows" {
		script := "@echo off\r\necho rg %*>>\"" + log + "\"\r\n"
		if err := os.WriteFile(filepath.Join(bin, "rg.bat"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATHEXT", ".BAT")
	} else {
		script := "#!/bin/sh\necho rg \"$@\" >> '" + log + "'\n"
		if err := os.WriteFile(filepath.Join(bin, "rg"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	return log
}

// Scenario: grep needs no rg (TC-03b). Given PATH scrubbed to a directory that
// holds only a recording fake rg, When grep searches 1,000 files, Then it
// returns matches and the fake records zero spawns.
func TestGrepNeedsNoRg(t *testing.T) {
	r := newRig(t)
	for i := range 1000 {
		body := fmt.Sprintf("file %d\nnothing here\n", i)
		if i%10 == 0 {
			body += fmt.Sprintf("the needle %d\n", i)
		}
		r.put(fmt.Sprintf("tree/d%02d/f%04d.txt", i/100, i), body)
	}
	log := spawnRecorder(t)
	res, _ := r.run(fstools.GrepName, map[string]any{"pattern": `needle \d+`, "path": "tree"})
	if res.IsError || !strings.HasPrefix(res.Text, "Found 100 matches\n") || !strings.Contains(res.Text, "  Line 3: the needle 990") {
		t.Fatalf("grep: %.500s", res.Text)
	}
	files, _ := r.run(fstools.GrepName, map[string]any{"pattern": `needle`, "path": "tree", "output": "count", "include": "f00[0-4]*.txt"})
	if files.IsError || !strings.HasPrefix(files.Text, "Found matches in 5 files\n") {
		t.Fatalf("grep count: %.500s", files.Text)
	}
	if b, err := os.ReadFile(log); err == nil || !os.IsNotExist(err) {
		t.Fatalf("rg was started: %q, %v", b, err)
	}
}

var spillRef = regexp.MustCompile(`saved to spill (\S+)\.\)`)

// Scenario: Large result spills (TC-03b). Given a 5 MiB file, When read runs
// with the default bound, Then the result is truncated at the bound and carries
// a spill reference whose content equals the full file.
func TestLargeResultSpills(t *testing.T) {
	r := newRig(t)
	var b strings.Builder
	for i := 0; b.Len() < 5<<20; i++ {
		fmt.Fprintf(&b, "%07d %s\n", i, strings.Repeat("x", 56))
	}
	full := b.String()
	r.put("big.txt", full)
	res, logged := r.run(fstools.ReadName, map[string]any{"file_path": "big.txt"})
	if res.IsError {
		t.Fatal(res.Text)
	}
	content := res.Text[strings.Index(res.Text, "<content>\n")+len("<content>\n"):]
	content = content[:strings.Index(content, "\n\n(")+1]
	if len(content) > fstools.DefaultReadMaxBytes || len(content) < fstools.DefaultReadMaxBytes-100 {
		t.Fatalf("the numbered lines are %d bytes; the bound is %d", len(content), fstools.DefaultReadMaxBytes)
	}
	if !strings.Contains(res.Text, "(Output capped at 51200 bytes. Showing lines 1-") {
		t.Fatalf("no truncation footer: %s", res.Text[len(res.Text)-300:])
	}
	id := session.ID(fmt.Sprintf("s-fs-tools-%d", r.n))
	if saved := r.spilled(res, logged, id); string(saved) != full {
		t.Fatalf("the spilled file is %d bytes, want the %d of the file", len(saved), len(full))
	}

	// The citation is durable: a fork of the session through the tool/result
	// still reads the file byte-identical after the owner is deleted.
	r.forkRetains(id, res.Spill.Path, full)

	// A page the caller chose is not spilled.
	page, _ := r.run(fstools.ReadName, map[string]any{"file_path": "big.txt", "offset": 10, "limit": 2})
	if page.IsError || page.Spill != nil || spillRef.MatchString(page.Text) || !strings.Contains(page.Text, "10: 0000009 ") || !strings.Contains(page.Text, "Use offset=12 to continue.") {
		t.Fatalf("a page: %s", page.Text)
	}
}

// forkRetains forks session id through its tool/result, deletes id, and checks
// the fork still reads spill path as want.
func (r *rig) forkRetains(id session.ID, path, want string) {
	r.t.Helper()
	if err := r.handles[id].Close(bg); err != nil {
		r.t.Fatal(err)
	}
	fork := "fork-of-" + string(id)
	f, err := r.st.Create(bg, store.Header{ID: fork}, store.CreateOptions{ForkOf: &store.ForkOf{ID: string(id), Through: store.Seq(r.seqs[id])}})
	if err != nil {
		r.t.Fatal(err)
	}
	if err := f.Close(bg); err != nil {
		r.t.Fatal(err)
	}
	if err := r.st.Delete(bg, string(id), store.DeleteOptions{}); err != nil {
		r.t.Fatal(err)
	}
	if kept := r.openSpill(fork, path); string(kept) != want {
		r.t.Fatalf("after the owner was deleted the fork read %d bytes, want %d", len(kept), len(want))
	}
}

// spilled checks that res cites one saved complete result in its text, in
// Result.Spill and in the logged tool/result's spill field, and that the
// session store tracks the citation; it returns the saved bytes.
func (r *rig) spilled(res htools.Result, logged string, id session.ID) []byte {
	r.t.Helper()
	m := spillRef.FindStringSubmatch(res.Text)
	if m == nil || res.Spill == nil || res.Spill.Path != m[1] {
		r.t.Fatalf("the text cites %v, Result.Spill is %+v: %s", m, res.Spill, res.Text[max(0, len(res.Text)-300):])
	}
	var p struct {
		Spill *event.Spill `json:"spill"`
	}
	if err := json.Unmarshal([]byte(logged), &p); err != nil || p.Spill == nil || *p.Spill != *res.Spill {
		r.t.Fatalf("the logged spill is %+v (%v), want %+v", p.Spill, err, res.Spill)
	}
	if ok, err := r.st.Cites(bg, string(id), "spill:"+res.Spill.Path); err != nil || !ok {
		r.t.Fatalf("the session store does not track the citation of %s: %v", res.Spill.Path, err)
	}
	saved := r.openSpill(string(id), res.Spill.Path)
	if int64(len(saved)) != res.Spill.Bytes {
		r.t.Fatalf("the spill file is %d bytes; the reference says %d", len(saved), res.Spill.Bytes)
	}
	return saved
}

func (r *rig) openSpill(caller, id string) []byte {
	r.t.Helper()
	rc, err := r.spill.Open(bg, caller, id)
	if err != nil {
		r.t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		r.t.Fatal(err)
	}
	return b
}

// A single 5 MiB line fits the line cap but not the character cap: read clips
// it and still saves the whole file, though no line was omitted.
func TestReadSpillsASingleLongLine(t *testing.T) {
	r := newRig(t)
	full := strings.Repeat("\u00e9abcdefg", 5<<20/9) + "\n"
	r.put("one.txt", full)
	res, logged := r.run(fstools.ReadName, map[string]any{"file_path": "one.txt"})
	if res.IsError || !strings.Contains(res.Text, "(End of file - total 1 lines)") || !strings.Contains(res.Text, "(Lines longer than 2000 characters were truncated.)") {
		t.Fatalf("read: %.300s ... %s", res.Text, res.Text[max(0, len(res.Text)-300):])
	}
	if len(res.Text) > fstools.DefaultReadMaxBytes {
		t.Fatalf("the result is %d bytes", len(res.Text))
	}
	if saved := r.spilled(res, logged, session.ID(fmt.Sprintf("s-fs-tools-%d", r.n))); string(saved) != full {
		t.Fatalf("the spilled file is %d bytes, want %d", len(saved), len(full))
	}
}

// glob and grep over their page offer offset and, with a spill store, save the
// complete list; a result that fits its page saves nothing.
func TestGlobAndGrepSpillTheCompleteList(t *testing.T) {
	r := newRigWith(t, rigOptions{cfg: fstools.Config{GlobMaxResults: 5, GrepMaxMatches: 7}})
	for i := range 30 {
		r.put(fmt.Sprintf("s/f%02d.txt", i), "hit\nmiss\n")
	}
	res, logged := r.run(fstools.GlobName, map[string]any{"pattern": "*.txt", "path": "s"})
	if res.IsError || !strings.Contains(res.Text, "(Showing 5 of 30 paths; 25 omitted. Use offset=5 to see more.)") {
		t.Fatalf("glob: %s", res.Text)
	}
	saved := strings.Split(strings.TrimSuffix(string(r.spilled(res, logged, session.ID(fmt.Sprintf("s-fs-tools-%d", r.n)))), "\n"), "\n")
	if len(saved) != 30 || !strings.HasPrefix(res.Text, strings.Join(saved[:5], "\n")+"\n") {
		t.Fatalf("the saved list has %d paths and does not start with the page: %v", len(saved), saved[:min(len(saved), 6)])
	}

	res, logged = r.run(fstools.GrepName, map[string]any{"pattern": "hit", "path": "s"})
	if res.IsError || !strings.HasPrefix(res.Text, "Found 7 matches\n") || !strings.Contains(res.Text, "(Results capped at 7. Use offset=7 to see more.)") {
		t.Fatalf("grep: %s", res.Text)
	}
	all := string(r.spilled(res, logged, session.ID(fmt.Sprintf("s-fs-tools-%d", r.n))))
	if !strings.HasPrefix(all, "Found 30 matches\n") || strings.Count(all, "  Line 1: hit") != 30 {
		t.Fatalf("the saved grep list: %.300s", all)
	}

	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{fstools.GlobName, map[string]any{"pattern": "f0[0-4].txt", "path": "s"}},
		{fstools.GrepName, map[string]any{"pattern": "hit", "path": "s", "include": "f0[0-4].txt"}},
	} {
		if res, _ := r.run(c.tool, c.args); res.IsError || res.Spill != nil || spillRef.MatchString(res.Text) || strings.Contains(res.Text, "offset=") {
			t.Fatalf("%s within its page: %+v", c.tool, res)
		}
	}

	// With the real spill policy as the pipeline's seat, a page over the seat's
	// budget is still the tools' own: the seat leaves it, and the complete list
	// stays the cited, tracked spill a fork keeps after the owner is deleted.
	r = newRigWith(t, rigOptions{policy: true, cfg: fstools.Config{GlobMaxResults: 40, GrepMaxMatches: 40}})
	for i := range 60 {
		r.put(fmt.Sprintf("s/f%02d.txt", i), "hit\nmiss\n")
	}
	seat := policy.MinInlineTokens * meter.CharsPerToken
	for _, c := range []struct {
		tool  string
		args  map[string]any
		check func(saved string) bool
	}{
		{fstools.GlobName, map[string]any{"pattern": "*.txt", "path": "s"}, func(s string) bool { return strings.Count(s, "\n") == 60 }},
		{fstools.GrepName, map[string]any{"pattern": "hit", "path": "s"}, func(s string) bool {
			return strings.HasPrefix(s, "Found 60 matches\n") && strings.Count(s, "  Line 1: hit") == 60
		}},
	} {
		res, logged := r.run(c.tool, c.args)
		if res.IsError || res.Truncated || len(res.Text) <= seat || !strings.Contains(res.Text, "offset=40") {
			t.Fatalf("%s under the policy (%d bytes, budget %d): %+v", c.tool, len(res.Text), seat, res)
		}
		id := session.ID(fmt.Sprintf("s-fs-tools-%d", r.n))
		saved := string(r.spilled(res, logged, id))
		if !c.check(saved) {
			t.Fatalf("%s saved %.300s", c.tool, saved)
		}
		r.forkRetains(id, res.Spill.Path, saved)
	}

	// SearchMaxBytes cuts a page short of its count; the complete list is saved.
	r = newRigWith(t, rigOptions{cfg: fstools.Config{GlobMaxResults: 40, GrepMaxMatches: 40, SearchMaxBytes: 1500}})
	for i := range 60 {
		r.put(fmt.Sprintf("s/f%02d.txt", i), "hit\nmiss\n")
	}
	res, logged = r.run(fstools.GrepName, map[string]any{"pattern": "hit", "path": "s"})
	listing, _, _ := strings.Cut(res.Text, "\n\n(")
	if res.IsError || len(listing) > 1500 || !strings.Contains(res.Text, "(Output capped at 1500 bytes; showing ") {
		t.Fatalf("grep over SearchMaxBytes (%d bytes): %s", len(listing), res.Text)
	}
	if all := string(r.spilled(res, logged, session.ID(fmt.Sprintf("s-fs-tools-%d", r.n)))); strings.Count(all, "  Line 1: hit") != 60 {
		t.Fatalf("the saved grep list: %.300s", all)
	}
	res, logged = r.run(fstools.GlobName, map[string]any{"pattern": "*.txt", "path": "s"})
	listing, _, _ = strings.Cut(res.Text, "\n\n(")
	if res.IsError || len(listing) > 1500 || strings.Contains(res.Text, "(Showing 40 of") || !strings.Contains(res.Text, " of 60 paths;") {
		t.Fatalf("glob over SearchMaxBytes (%d bytes): %s", len(listing), res.Text)
	}
	if saved := string(r.spilled(res, logged, session.ID(fmt.Sprintf("s-fs-tools-%d", r.n)))); strings.Count(saved, "\n") != 60 {
		t.Fatalf("the saved glob list: %.300s", saved)
	}

	// A first result longer than the whole ceiling is cut to it at a UTF-8
	// boundary, said so, and saved whole; the offset moves past it.
	r = newRigWith(t, rigOptions{cfg: fstools.Config{SearchMaxBytes: 16}})
	r.put("one/only.txt", "x\n")
	res, logged = r.run(fstools.GlobName, map[string]any{"pattern": "*.txt", "path": "one"})
	listing, _, _ = strings.Cut(res.Text, "\n\n(")
	if res.IsError || len(listing) > 16 || listing == "" || !strings.Contains(res.Text, "(Output capped at 16 bytes; the path above is cut short.)") || strings.Contains(res.Text, "offset=") {
		t.Fatalf("one long path (%d bytes): %s", len(listing), res.Text)
	}
	if saved := string(r.spilled(res, logged, session.ID(fmt.Sprintf("s-fs-tools-%d", r.n)))); saved != r.path("one/only.txt")+"\n" {
		t.Fatalf("the saved path %q", saved)
	}
	r.put("one/also.txt", "x\n")
	if res, _ = r.run(fstools.GlobName, map[string]any{"pattern": "*.txt", "path": "one"}); !strings.Contains(res.Text, "(Showing 1 of 2 paths; 1 omitted. Use offset=1 to see more.)") {
		t.Fatalf("two long paths: %s", res.Text)
	}

	r = newRigWith(t, rigOptions{cfg: fstools.Config{SearchMaxBytes: 200}})
	line := "hit " + strings.Repeat("\u00e9", 700)
	r.put("g/long.txt", line+"\n")
	res, logged = r.run(fstools.GrepName, map[string]any{"pattern": "hit", "path": "g"})
	listing, _, _ = strings.Cut(res.Text, "\n\n(")
	if res.IsError || len(listing) > 200 || !utf8.ValidString(listing) || !strings.HasPrefix(listing, "Found 1 matches\n") ||
		!strings.Contains(res.Text, "(Output capped at 200 bytes; the result above is cut short.)") || strings.Contains(res.Text, "offset=") {
		t.Fatalf("one long match (%d bytes): %s", len(listing), res.Text)
	}
	if all := string(r.spilled(res, logged, session.ID(fmt.Sprintf("s-fs-tools-%d", r.n)))); !strings.Contains(all, "  Line 1: "+line+"\n") {
		t.Fatalf("the saved grep list: %.300s", all)
	}
	r.put("g/long2.txt", line+"\n")
	if res, _ = r.run(fstools.GrepName, map[string]any{"pattern": "hit", "path": "g"}); !strings.Contains(res.Text, "cut short. Use offset=1 to see more.)") {
		t.Fatalf("two long matches: %s", res.Text)
	}
}

// apply_patch refuses an Add over the text ceiling before any hunk publishes.
func TestApplyPatchAddRespectsTheTextCeiling(t *testing.T) {
	r := newRigWith(t, rigOptions{cfg: fstools.Config{TextMaxBytes: 16}})
	r.put("a.txt", "one\n")
	res, _ := r.run(fstools.ApplyPatchName, map[string]any{"input": "*** Begin Patch\n" +
		"*** Update File: a.txt\n@@\n-one\n+two\n" +
		"*** Add File: big.txt\n+" + strings.Repeat("x", 32) + "\n" +
		"*** End Patch"})
	if !res.IsError || !strings.Contains(res.Text, "fs/too-large") {
		t.Fatalf("an oversized Add: %+v", res)
	}
	if _, err := os.Stat(r.path("big.txt")); !os.IsNotExist(err) || r.disk("a.txt") != "one\n" {
		t.Fatalf("a hunk published: %v, a.txt %q", err, r.disk("a.txt"))
	}
}

// unsynced is a provider whose writes land but whose sync fails, and whose
// deletes fail.
type unsynced struct{ fs.FS }

func (u unsynced) Write(ctx context.Context, ref fs.Ref, data []byte, g fs.Guard, p sandbox.Policy) (fs.WriteOutcome, error) {
	out, err := u.FS.Write(ctx, ref, data, g, p)
	if err == nil {
		out.DurabilityUnknown = errors.New("fsync failed")
	}
	return out, err
}

func (unsynced) Delete(context.Context, fs.Ref, fs.Guard, sandbox.Policy) (fs.DeleteOutcome, error) {
	return fs.DeleteOutcome{}, herr.New(fs.CodeIO, "the disk refused the delete")
}

// apply_patch says which of its writes have unknown durability, in a success
// and in the completed part of a partial failure, as write and edit do.
func TestApplyPatchReportsUnknownDurability(t *testing.T) {
	r := newRigWith(t, rigOptions{wrap: func(p fs.FS) fs.FS { return unsynced{p} }})
	const note = "the file is in place but its durability is unknown"
	r.put("m.txt", "one\n")
	r.put("gone.txt", "x\n")
	res, _ := r.run(fstools.ApplyPatchName, map[string]any{"input": "*** Begin Patch\n" +
		"*** Add File: a.txt\n+a\n" +
		"*** Update File: m.txt\n@@\n-one\n+two\n" +
		"*** End Patch"})
	if res.IsError || strings.Count(res.Text, note) != 2 {
		t.Fatalf("success: %s", res.Text)
	}
	// The delete fails at commit: the Add before it completed, unsynced, and
	// the update after it never ran.
	res, _ = r.run(fstools.ApplyPatchName, map[string]any{"input": "*** Begin Patch\n" +
		"*** Add File: b.txt\n+b\n" +
		"*** Delete File: gone.txt\n" +
		"*** Update File: m.txt\n@@\n-two\n+three\n" +
		"*** End Patch"})
	completed := "Completed:\nA " + r.path("b.txt") + " (version " + digest("b\n") + "); " + note + "\nFailed:\nDelete File gone.txt"
	if !res.IsError || !strings.Contains(res.Text, completed) || !strings.Contains(res.Text, "Not executed:\nUpdate File m.txt") {
		t.Fatalf("partial failure: %s", res.Text)
	}
	if r.disk("b.txt") != "b\n" || r.disk("m.txt") != "two\n" || r.disk("gone.txt") != "x\n" {
		t.Fatalf("files: b %q, m %q, gone %q", r.disk("b.txt"), r.disk("m.txt"), r.disk("gone.txt"))
	}
}

// glob lists newest first, at most the bound, and says how many it left out.
func TestGlobNewestFirstAndBounded(t *testing.T) {
	r := newRig(t)
	for i := range 105 {
		r.put(fmt.Sprintf("g/f%03d.txt", i), "x")
	}
	r.put("g/.hidden.txt", "x")
	newest := r.path("g/f007.txt")
	future := mustTime(t, r.path("g/f000.txt")).Add(time.Hour)
	if err := os.Chtimes(newest, future, future); err != nil {
		t.Fatal(err)
	}
	res, _ := r.run(fstools.GlobName, map[string]any{"pattern": "*.txt", "path": "g"})
	if res.IsError || !strings.HasPrefix(res.Text, newest+"\n") || !strings.Contains(res.Text, "(Showing 100 of 106 paths; 6 omitted. Use offset=100 to see more.)") {
		t.Fatalf("glob: %s", res.Text[len(res.Text)-200:])
	}
	none, _ := r.run(fstools.GlobName, map[string]any{"pattern": "*.go", "path": "g"})
	if none.IsError || none.Text != "No files found" {
		t.Fatalf("no match: %+v", none)
	}
}

func mustTime(t *testing.T, p string) time.Time {
	t.Helper()
	in, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return in.ModTime()
}

// read, glob and grep declare overlapping reads on their path; the writers
// declare writes on every path they change.
func TestOverlapKeys(t *testing.T) {
	r := newRig(t)
	ds, err := fstools.Declarations(ops.New(r.root, local.New(), nil, ops.Options{}), nil, fstools.Config{})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]*htools.Declaration{}
	for _, d := range ds {
		by[d.Name()] = d
	}
	if len(by) != 6 {
		t.Fatalf("%d tools", len(by))
	}
	classify := func(name, args string) htools.Overlap { return by[name].Classify(json.RawMessage(args)) }
	for _, name := range []string{fstools.ReadName, fstools.GlobName, fstools.GrepName} {
		args := `{"pattern":"x","path":"a.txt"}`
		if name == fstools.ReadName {
			args = `{"file_path":"a.txt"}`
		}
		o := classify(name, args)
		if len(o.ReadKeys()) != 1 || len(o.WriteKeys()) != 0 {
			t.Fatalf("%s: reads %v, writes %v", name, o.ReadKeys(), o.WriteKeys())
		}
		if !o.Conflicts(classify(fstools.WriteName, `{"file_path":"a.txt","content":""}`)) {
			t.Fatalf("%s does not conflict with a write of its path", name)
		}
		if o.Conflicts(classify(fstools.ReadName, `{"file_path":"a.txt"}`)) {
			t.Fatalf("%s conflicts with a read", name)
		}
	}
	p := classify(fstools.ApplyPatchName, `{"input":"*** Begin Patch\n*** Add File: x.txt\n+a\n*** Update File: y.txt\n*** Move to: z.txt\n@@\n-a\n+b\n*** End Patch"}`)
	if got := p.WriteKeys(); len(got) != 3 {
		t.Fatalf("apply_patch writes %v", got)
	}
	off := false
	ds, err = fstools.Declarations(ops.New(r.root, local.New(), nil, ops.Options{}), nil, fstools.Config{ApplyPatch: &off})
	if err != nil || len(ds) != 5 {
		t.Fatalf("applyPatch off: %d tools, %v", len(ds), err)
	}
}
