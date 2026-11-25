package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
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

// codeFaulty is the error code faults injects.
const codeFaulty herr.Reason = "fs/permission-denied"

// faults wraps a provider and fails, by base name, the calls a test names:
// stat, readDir, open and write fail with codeFaulty, crash fails a write with
// an error that has no code; shrink reports a file's
// size as 0 when opened (it grew after it was measured); breakRead makes its
// reader fail after the first byte; version rewrites every version the
// provider reports.
type faults struct {
	fs.FS
	stat, readDir, open, write, crash, shrink, breakRead []string
	version                                              func(fs.Version) fs.Version
}

func hit(names []string, r fs.Ref) bool { return slices.Contains(names, filepath.Base(r.Path)) }

func refused(op string, r fs.Ref) error {
	return herr.Newf(codeFaulty, "%s %s refused by the test provider", op, r.Path)
}

func (f faults) ver(v fs.Version) fs.Version {
	if f.version == nil {
		return v
	}
	return f.version(v)
}

func (f faults) Stat(ctx context.Context, r fs.Ref) (fs.Info, error) {
	if hit(f.stat, r) {
		return fs.Info{}, refused("stat", r)
	}
	in, err := f.FS.Stat(ctx, r)
	in.Version = f.ver(in.Version)
	return in, err
}

func (f faults) ReadDir(ctx context.Context, r fs.Ref, limit int) ([]fs.DirEntry, bool, error) {
	if hit(f.readDir, r) {
		return nil, false, refused("readdir", r)
	}
	return f.FS.ReadDir(ctx, r, limit)
}

// brokenReader yields one byte, then fails.
type brokenReader struct {
	io.ReadCloser
	n int
}

func (b *brokenReader) Read(p []byte) (int, error) {
	if b.n > 0 {
		return 0, errors.New("the disk went away mid-read")
	}
	b.n++
	return b.ReadCloser.Read(p[:min(len(p), 1)])
}

func (f faults) Open(ctx context.Context, r fs.Ref) (io.ReadCloser, fs.Info, error) {
	if hit(f.open, r) {
		return nil, fs.Info{}, refused("open", r)
	}
	rc, in, err := f.FS.Open(ctx, r)
	if err != nil {
		return rc, in, err
	}
	in.Version = f.ver(in.Version)
	if hit(f.shrink, r) {
		in.Size = 0
	}
	if hit(f.breakRead, r) {
		rc = &brokenReader{ReadCloser: rc}
	}
	return rc, in, nil
}

func (f faults) Write(ctx context.Context, r fs.Ref, data []byte, g fs.Guard, p sandbox.Policy) (fs.WriteOutcome, error) {
	if hit(f.write, r) {
		return fs.WriteOutcome{}, refused("write", r)
	}
	if hit(f.crash, r) {
		return fs.WriteOutcome{}, errors.New("the write crashed")
	}
	out, err := f.FS.Write(ctx, r, data, g, p)
	out.Version = f.ver(out.Version)
	return out, err
}

func withFaults(f faults) rigOptions {
	return rigOptions{wrap: func(p fs.FS) fs.FS { f.FS = p; return f }}
}

// failingStore is a spill store whose saves fail.
type failingStore struct{ spill.Store }

func (failingStore) Create(context.Context, spill.Ref, int64) (spill.Writer, error) {
	return nil, errors.New("the spill volume is full")
}

// wantError runs one call and checks it fails with code, its text holding each
// of more.
func (r *rig) wantError(name string, args map[string]any, code herr.Reason, more ...string) string {
	r.t.Helper()
	res, _ := r.run(name, args)
	if !res.IsError || !strings.Contains(res.Text, string(code)) {
		r.t.Fatalf("%s %v: want %s, got %+v", name, args, code, res)
	}
	for _, s := range more {
		if !strings.Contains(res.Text, s) {
			r.t.Fatalf("%s %v: the error does not say %q:\n%s", name, args, s, res.Text)
		}
	}
	return res.Text
}

// wantText runs one call and checks it succeeds, its text holding each of more.
func (r *rig) wantText(name string, args map[string]any, more ...string) htools.Result {
	r.t.Helper()
	res, _ := r.run(name, args)
	if res.IsError {
		r.t.Fatalf("%s %v: %s", name, args, res.Text)
	}
	for _, s := range more {
		if !strings.Contains(res.Text, s) {
			r.t.Fatalf("%s %v: the result does not say %q:\n%s", name, args, s, res.Text)
		}
	}
	return res
}

// read on a directory lists one level, sorted, directories marked with a
// slash; over ReadLimit entries it says the directory has more.
func TestReadListsADirectory(t *testing.T) {
	r := newRigWith(t, rigOptions{cfg: fstools.Config{ReadLimit: 3}})
	r.put("d/b.txt", "b")
	r.put("d/a.txt", "a")
	r.put("d/sub/c.txt", "c")
	res := r.wantText(fstools.ReadName, map[string]any{"file_path": "d"},
		"<path>"+r.path("d")+"</path>\n<type>directory</type>\n<entries>\na.txt\nb.txt\nsub/\n\n(3 entries)\n</entries>")
	if res.Spill != nil {
		t.Fatalf("a directory listing was spilled: %+v", res.Spill)
	}
	r.put("d/e.txt", "e")
	r.wantText(fstools.ReadName, map[string]any{"file_path": "d"},
		"(Showing 3 entries; the directory has more. Use glob to find a file in it.)")
}

// read refuses a missing file and a path the provider cannot resolve or list,
// and says when the offset is past the end.
func TestReadRefusalsAndThePastTheEndFooter(t *testing.T) {
	r := newRigWith(t, withFaults(faults{stat: []string{"secret.txt"}, readDir: []string{"locked"}}))
	r.put("two.txt", "one\ntwo\n")
	r.put("locked/x.txt", "x")
	r.wantError(fstools.ReadName, map[string]any{"file_path": "nope.txt"}, fs.CodeNotFound, r.path("nope.txt")+" does not exist")
	r.wantError(fstools.ReadName, map[string]any{"file_path": "secret.txt"}, codeFaulty)
	r.wantError(fstools.ReadName, map[string]any{"file_path": "locked"}, codeFaulty)
	r.wantText(fstools.ReadName, map[string]any{"file_path": "two.txt", "offset": 9},
		"(Offset 9 is beyond the end of the file - total 2 lines)\n</content>")
	r.put("empty.txt", "")
	r.wantText(fstools.ReadName, map[string]any{"file_path": "empty.txt", "offset": 5}, "(End of file - total 0 lines)")
}

// A text tool reads at most TextMaxBytes: a file measured over it is refused,
// as is one that grew past it after it was measured, and a read that fails
// part-way is fs/io.
func TestTextToolsBoundAndCheckWhatTheyRead(t *testing.T) {
	r := newRigWith(t, rigOptions{
		cfg: fstools.Config{TextMaxBytes: 8},
		wrap: func(p fs.FS) fs.FS {
			return faults{FS: p, shrink: []string{"grew.txt"}, breakRead: []string{"flaky.txt"}}
		},
	})
	r.put("big.txt", "123456789")
	r.put("grew.txt", "123456789")
	r.put("flaky.txt", "abc")
	for _, tool := range []string{fstools.ReadName, fstools.EditName} {
		args := func(p string) map[string]any {
			if tool == fstools.EditName {
				return map[string]any{"file_path": p, "old_string": "1", "new_string": "x"}
			}
			return map[string]any{"file_path": p}
		}
		r.wantError(tool, args("big.txt"), fs.CodeTooLarge, "is 9 bytes, more than the 8")
		r.wantError(tool, args("grew.txt"), fs.CodeTooLarge, "is more than the 8 bytes")
		r.wantError(tool, args("flaky.txt"), fs.CodeIO, "the disk went away mid-read")
	}
	if r.disk("big.txt") != "123456789" || r.disk("grew.txt") != "123456789" {
		t.Fatal("a refused edit changed a file")
	}
}

// When even the first line does not fit the byte cap, that line is shown cut
// to it, so paging with offset always advances; the footer says so and the
// whole file is saved.
func TestReadCutsAnOversizedFirstLineToTheByteCap(t *testing.T) {
	r := newRigWith(t, rigOptions{cfg: fstools.Config{ReadMaxBytes: 32}})
	full := strings.Repeat("w", 100) + "\nsecond\nthird\n"
	r.put("wide.txt", full)
	res, logged := r.run(fstools.ReadName, map[string]any{"file_path": "wide.txt"})
	if res.IsError {
		t.Fatal(res.Text)
	}
	for _, s := range []string{
		"<content>\n1: " + strings.Repeat("w", 28) + "\n\n",
		"(Output capped at 32 bytes. Showing lines 1-1 of 3. Use offset=2 to continue.)",
		"(Line 1 was cut to fit the 32-byte cap.)",
	} {
		if !strings.Contains(res.Text, s) {
			t.Fatalf("the read does not say %q:\n%s", s, res.Text)
		}
	}
	if saved := r.spilled(res, logged, r.lastSession()); string(saved) != full {
		t.Fatalf("saved %q", saved)
	}
	r.wantText(fstools.ReadName, map[string]any{"file_path": "wide.txt", "offset": 2}, "2: second\n3: third\n\n(End of file - total 3 lines)")
}

func (r *rig) lastSession() session.ID { return session.ID(fmt.Sprintf("s-fs-tools-%d", r.n)) }

// Without a spill store a cut read cites nothing; with a store that fails the
// footer says the whole file could not be saved. The page itself is the same.
func TestReadSpillWithoutAStoreOrWithAFailingOne(t *testing.T) {
	body := strings.Repeat("line\n", 10)
	for name, tc := range map[string]struct {
		store func(spill.Store) spill.Store
		note  string
	}{
		"none":    {func(spill.Store) spill.Store { return nil }, ""},
		"failing": {func(s spill.Store) spill.Store { return failingStore{s} }, "(The whole file could not be saved: the spill volume is full)"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRigWith(t, rigOptions{store: tc.store, cfg: fstools.Config{ReadLimit: 3}})
			r.put("ten.txt", body)
			res := r.wantText(fstools.ReadName, map[string]any{"file_path": "ten.txt"}, "(Showing lines 1-3 of 10. Use offset=4 to continue.)")
			if res.Spill != nil || spillRef.MatchString(res.Text) {
				t.Fatalf("the read cites a spill: %+v", res)
			}
			if tc.note != "" && !strings.Contains(res.Text, tc.note) {
				t.Fatalf("the read does not say %q:\n%s", tc.note, res.Text)
			}
			if tc.note == "" && strings.Contains(res.Text, "could not be saved") {
				t.Fatalf("a read with no store reports a failed save:\n%s", res.Text)
			}
		})
	}
}

// write refuses content over TextMaxBytes and a path outside the workspace,
// creating nothing.
func TestWriteRefusals(t *testing.T) {
	r := newRigWith(t, rigOptions{cfg: fstools.Config{TextMaxBytes: 4}})
	r.wantError(fstools.WriteName, map[string]any{"file_path": "a.txt", "content": "12345"}, fs.CodeTooLarge, "the content is 5 bytes, more than the 4")
	outside := filepath.Join(t.TempDir(), "o.txt")
	r.wantError(fstools.WriteName, map[string]any{"file_path": outside, "content": "x"}, fs.CodeSandboxDenied)
	for _, p := range []string{r.path("a.txt"), outside} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s: %v, want not to exist", p, err)
		}
	}
	r.wantText(fstools.WriteName, map[string]any{"file_path": "a.txt", "content": "1234"}, "Created file "+r.path("a.txt"))
	r.wantText(fstools.WriteName, map[string]any{"file_path": "a.txt", "content": "abcd"}, "Updated file "+r.path("a.txt"))
}

// The version a result names is the content digest when the provider has one,
// else its metadata token, else "unknown".
func TestResultVersionFallsBackToMetadataThenUnknown(t *testing.T) {
	for name, tc := range map[string]struct {
		strip func(fs.Version) fs.Version
		want  string
	}{
		"metadata": {func(v fs.Version) fs.Version { return fs.Version{Meta: "m-1"} }, "(version meta:m-1)"},
		"none":     {func(fs.Version) fs.Version { return fs.Version{} }, "(version unknown)"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRigWith(t, withFaults(faults{version: tc.strip}))
			r.wantText(fstools.WriteName, map[string]any{"file_path": "a.txt", "content": "x"}, "Created file "+r.path("a.txt")+" "+tc.want)
		})
	}
	// A provider that reports no versions is not asked to guard a delete
	// against one: the patch deletes the file unguarded.
	r := newRigWith(t, withFaults(faults{version: func(fs.Version) fs.Version { return fs.Version{} }}))
	r.put("gone.txt", "x\n")
	r.wantText(fstools.ApplyPatchName, map[string]any{"input": "*** Begin Patch\n*** Delete File: gone.txt\n*** End Patch"}, "D "+r.path("gone.txt"))
	if _, err := os.Stat(r.path("gone.txt")); !os.IsNotExist(err) {
		t.Fatalf("gone.txt: %v", err)
	}
}

// edit refuses an empty or unchanged old_string, a missing file and a result
// over TextMaxBytes; a provider that refuses the write leaves the file as it
// was. None of them changes the file.
func TestEditRefusals(t *testing.T) {
	r := newRigWith(t, rigOptions{
		cfg:  fstools.Config{TextMaxBytes: 12},
		wrap: func(p fs.FS) fs.FS { return faults{FS: p, write: []string{"ro.txt"}} },
	})
	r.put("a.txt", "abc\n")
	r.put("ro.txt", "abc\n")
	edit := func(p, old, repl string) map[string]any {
		return map[string]any{"file_path": p, "old_string": old, "new_string": repl}
	}
	r.wantError(fstools.EditName, edit("a.txt", "", "x"), fs.CodeEditNotFound, "old_string is empty; use write to create a file")
	r.wantError(fstools.EditName, edit("a.txt", "abc", "abc"), fs.CodeEditNotFound, "old_string and new_string are the same")
	r.wantError(fstools.EditName, edit("missing.txt", "abc", "x"), fs.CodeNotFound)
	r.wantError(fstools.EditName, edit("a.txt", "b", "0123456789"), fs.CodeTooLarge, "would be 13 bytes, more than the 12")
	r.wantError(fstools.EditName, edit("ro.txt", "b", "B"), codeFaulty)
	if r.disk("a.txt") != "abc\n" || r.disk("ro.txt") != "abc\n" {
		t.Fatalf("a refused edit changed a file: %q, %q", r.disk("a.txt"), r.disk("ro.txt"))
	}
	if _, err := os.Stat(r.path("missing.txt")); !os.IsNotExist(err) {
		t.Fatalf("an edit created missing.txt: %v", err)
	}
}

// apply_patch preflights every hunk against the files as they are, and a
// refused hunk leaves every file untouched.
func TestApplyPatchPreflightRefusals(t *testing.T) {
	r := newRigWith(t, rigOptions{
		cfg:  fstools.Config{TextMaxBytes: 16},
		wrap: func(p fs.FS) fs.FS { return faults{FS: p, stat: []string{"unstat.txt"}} },
	})
	r.put("a.txt", "one\n")
	r.put("big.txt", strings.Repeat("x", 17))
	r.put("latin1.txt", "caf\xe9\n")
	r.put("dir/inner.txt", "x")
	patch := func(body string) map[string]any {
		return map[string]any{"input": "*** Begin Patch\n*** Update File: a.txt\n@@\n-one\n+two\n" + body + "*** End Patch"}
	}
	for _, tc := range []struct {
		body string
		code herr.Reason
		says string
	}{
		{"*** Add File: a.txt\n+x\n", fs.CodeAlreadyExists, "Add File a.txt: the file already exists"},
		{"*** Delete File: none.txt\n", fs.CodeNotFound, "Delete File none.txt: the file does not exist"},
		{"*** Update File: none.txt\n@@\n-a\n+b\n", fs.CodeNotFound, "Update File none.txt: the file does not exist"},
		{"*** Update File: a.txt\n@@\n-two\n+" + strings.Repeat("y", 20) + "\n", fs.CodeTooLarge, "Update File a.txt: the result is 21 bytes, more than the 16"},
		{"*** Update File: dir\n@@\n-a\n+b\n", fs.CodeNotRegularFile, "is not a regular file"},
		{"*** Update File: big.txt\n@@\n-a\n+b\n", fs.CodeTooLarge, "is 17 bytes, more than the 16"},
		{"*** Update File: latin1.txt\n@@\n-a\n+b\n", fs.CodeNotText, "is not UTF-8 text"},
		{"*** Add File: unstat.txt\n+x\n", codeFaulty, "unstat.txt refused"},
		{"*** Delete File: unstat.txt\n", codeFaulty, "unstat.txt refused"},
		{"*** Update File: unstat.txt\n@@\n-a\n+b\n", codeFaulty, "unstat.txt refused"},
		{"*** Update File: a.txt\n*** Move to: unstat.txt\n@@\n-two\n+three\n", codeFaulty, "unstat.txt refused"},
	} {
		r.wantError(fstools.ApplyPatchName, patch(tc.body), tc.code, tc.says)
		if got := r.disk("a.txt"); got != "one\n" {
			t.Fatalf("%q: a.txt is %q after a refused patch", tc.body, got)
		}
	}
	if _, err := os.Stat(r.path("unstat.txt")); !os.IsNotExist(err) {
		t.Fatalf("unstat.txt: %v", err)
	}
}

// A patch that does not parse, or names no file, is patch/syntax, runs alone,
// and changes nothing.
func TestApplyPatchSyntaxAndEmptyPatches(t *testing.T) {
	r := newRig(t)
	ds, err := fstools.Declarations(ops.New(r.root, local.New(), nil, ops.Options{}), nil, fstools.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var ap *htools.Declaration
	for _, d := range ds {
		if d.Name() == fstools.ApplyPatchName {
			ap = d
		}
	}
	for _, input := range []string{"not a patch", "*** Begin Patch\n*** End Patch"} {
		r.wantError(fstools.ApplyPatchName, map[string]any{"input": input}, "patch/syntax")
		args, err := json.Marshal(map[string]any{"input": input})
		if err != nil {
			t.Fatal(err)
		}
		if o := ap.Classify(args); !o.IsExclusive() {
			t.Fatalf("%q is classified %+v, want exclusive", input, o)
		}
	}
}

// A BOM survives an apply_patch update, and a Delete File hunk removes the file
// and names it.
func TestApplyPatchKeepsTheMarkAndDeletes(t *testing.T) {
	r := newRig(t)
	r.put("bom.txt", "\xef\xbb\xbfone\n")
	r.put("gone.txt", "x\n")
	r.wantText(fstools.ApplyPatchName, map[string]any{"input": "*** Begin Patch\n" +
		"*** Update File: bom.txt\n@@\n-one\n+two\n" +
		"*** Delete File: gone.txt\n" +
		"*** End Patch"}, "M "+r.path("bom.txt"), "D "+r.path("gone.txt"))
	if got := r.disk("bom.txt"); got != "\xef\xbb\xbftwo\n" {
		t.Fatalf("bom.txt is %q", got)
	}
	if _, err := os.Stat(r.path("gone.txt")); !os.IsNotExist(err) {
		t.Fatalf("gone.txt: %v", err)
	}
}

// A hunk that fails at commit stops the patch: the completed hunks stay, the
// failed one is named with its error, the rest are listed as not executed, and
// the result carries the failure's code.
func TestApplyPatchCommitFailuresStopThePatch(t *testing.T) {
	for _, tc := range []struct {
		name, body, failed, rest string
	}{
		{"add", "*** Add File: denied.txt\n+x\n*** Update File: b.txt\n*** Move to: c.txt\n@@\n-b\n+c\n",
			"Failed:\nAdd File denied.txt: ", "Not executed:\nUpdate File b.txt (move to c.txt)\n"},
		{"update", "*** Update File: denied.txt\n@@\n-d\n+e\n*** Add File: n.txt\n+n\n",
			"Failed:\nUpdate File denied.txt: ", "Not executed:\nAdd File n.txt\n"},
		{"move", "*** Update File: b.txt\n*** Move to: denied.txt\n@@\n-b\n+c\n",
			"Failed:\nUpdate File b.txt (move to denied.txt): ", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRigWith(t, withFaults(faults{write: []string{"denied.txt"}}))
			r.put("a.txt", "a\n")
			r.put("b.txt", "b\n")
			if tc.name == "update" {
				r.put("denied.txt", "d\n")
			}
			text := r.wantError(fstools.ApplyPatchName, map[string]any{"input": "*** Begin Patch\n" +
				"*** Update File: a.txt\n@@\n-a\n+A\n" + tc.body + "*** End Patch"}, codeFaulty,
				"The patch stopped part-way; nothing was rolled back.\nCompleted:\nM "+r.path("a.txt"), tc.failed, tc.rest,
				"Re-read the files before patching again.")
			if tc.rest == "" && strings.Contains(text, "Not executed:") {
				t.Fatalf("the last hunk failed but some are not executed:\n%s", text)
			}
			if r.disk("a.txt") != "A\n" || r.disk("b.txt") != "b\n" {
				t.Fatalf("a %q, b %q", r.disk("a.txt"), r.disk("b.txt"))
			}
			for _, n := range []string{"c.txt", "n.txt"} {
				if _, err := os.Stat(r.path(n)); !os.IsNotExist(err) {
					t.Fatalf("%s: %v", n, err)
				}
			}
		})
	}
}

// A commit failure whose error has no code is reported as fs/io.
func TestApplyPatchUncodedCommitFailureIsIO(t *testing.T) {
	r := newRigWith(t, withFaults(faults{crash: []string{"a.txt"}}))
	r.put("a.txt", "a\n")
	r.wantError(fstools.ApplyPatchName, map[string]any{"input": "*** Begin Patch\n*** Update File: a.txt\n@@\n-a\n+b\n*** End Patch"},
		fs.CodeIO, "Failed:\nUpdate File a.txt: the write crashed")
	if r.disk("a.txt") != "a\n" {
		t.Fatalf("a.txt is %q", r.disk("a.txt"))
	}
}

// A move onto an existing file replaces it, guarded by the version preflight
// saw, and removes the source.
func TestApplyPatchMovesOntoAnExistingFile(t *testing.T) {
	r := newRig(t)
	r.put("from.txt", "a\n")
	r.put("to.txt", "old\n")
	r.wantText(fstools.ApplyPatchName, map[string]any{"input": "*** Begin Patch\n" +
		"*** Update File: from.txt\n*** Move to: to.txt\n@@\n-a\n+b\n*** End Patch"},
		"R "+r.path("from.txt")+" -> "+r.path("to.txt")+" (version "+digest("b\n")+")")
	if r.disk("to.txt") != "b\n" {
		t.Fatalf("to.txt is %q", r.disk("to.txt"))
	}
	if _, err := os.Stat(r.path("from.txt")); !os.IsNotExist(err) {
		t.Fatalf("from.txt: %v", err)
	}
}

// A move is two effects: when the source cannot be removed after the
// destination was written, the error says the destination was written, with
// its version, and both files exist.
func TestApplyPatchMoveWhoseSourceCannotBeRemoved(t *testing.T) {
	r := newRigWith(t, rigOptions{wrap: func(p fs.FS) fs.FS { return unsynced{p} }})
	r.put("from.txt", "a\n")
	text := r.wantError(fstools.ApplyPatchName, map[string]any{"input": "*** Begin Patch\n" +
		"*** Update File: from.txt\n*** Move to: to.txt\n@@\n-a\n+b\n*** End Patch"}, fs.CodeIO,
		"Failed:\nUpdate File from.txt (move to to.txt): ",
		r.path("to.txt")+" was written (version "+digest("b\n")+"; the file is in place but its durability is unknown) but the source could not be removed")
	if !strings.Contains(text, "the disk refused the delete") {
		t.Fatalf("the cause is lost:\n%s", text)
	}
	if r.disk("from.txt") != "a\n" || r.disk("to.txt") != "b\n" {
		t.Fatalf("from %q, to %q", r.disk("from.txt"), r.disk("to.txt"))
	}
}

// glob and grep refuse a missing path, a path the provider cannot resolve and
// an invalid pattern.
func TestSearchRefusals(t *testing.T) {
	r := newRigWith(t, withFaults(faults{stat: []string{"hidden"}}))
	r.put("s/a.txt", "a\n")
	for _, tool := range []string{fstools.GlobName, fstools.GrepName} {
		r.wantError(tool, map[string]any{"pattern": "a", "path": "nope"}, fs.CodeNotFound, r.path("nope")+" does not exist")
		r.wantError(tool, map[string]any{"pattern": "a", "path": "hidden"}, codeFaulty)
	}
	r.wantError(fstools.GlobName, map[string]any{"pattern": "[", "path": "s"}, "search/invalid-pattern")
	r.wantError(fstools.GrepName, map[string]any{"pattern": "(", "path": "s"}, "search/invalid-pattern")
}

// grep searches one file when its path is a file, reports no matches plainly,
// lists matching files and marks a line cut at GrepMaxLineBytes.
func TestGrepOutputModes(t *testing.T) {
	r := newRigWith(t, rigOptions{cfg: fstools.Config{GrepMaxLineBytes: 8}})
	r.put("s/a.txt", "needle one\nhay\n")
	r.put("s/b.txt", "needle two\n")
	r.put("s/c.txt", "hay\n")
	r.wantText(fstools.GrepName, map[string]any{"pattern": "needle", "path": "s/b.txt"},
		"Found 1 matches\n\n"+r.path("s/b.txt")+":\n  Line 1: needle t... (line truncated)")
	if res := r.wantText(fstools.GrepName, map[string]any{"pattern": "absent", "path": "s"}); res.Text != "No matches found" {
		t.Fatalf("no matches: %q", res.Text)
	}
	res := r.wantText(fstools.GrepName, map[string]any{"pattern": "needle", "path": "s", "output": "files"})
	lines := strings.Split(res.Text, "\n")
	if lines[0] != "Found 2 files" || len(lines) != 3 || !slices.Contains(lines, r.path("s/a.txt")) || !slices.Contains(lines, r.path("s/b.txt")) {
		t.Fatalf("files: %q", res.Text)
	}
}

// When even the complete list grep saves is capped, the footer says it is the
// first results only.
func TestGrepSavesTheFirstResultsWhenTheSaveIsCapped(t *testing.T) {
	r := newRigWith(t, rigOptions{cfg: fstools.Config{GrepMaxMatches: 2, SpillMaxMatches: 3}})
	for i := range 5 {
		r.put(fmt.Sprintf("s/f%d.txt", i), "hit\n")
	}
	res, logged := r.run(fstools.GrepName, map[string]any{"pattern": "hit", "path": "s"})
	if res.IsError || !strings.Contains(res.Text, "(Results capped at 2. Use offset=2 to see more.)") ||
		!strings.Contains(res.Text, "(The list of the first 3 results (the most saved), ") {
		t.Fatalf("grep: %s", res.Text)
	}
	if saved := string(r.spilled(res, logged, r.lastSession())); !strings.HasPrefix(saved, "Found 3 matches\n") {
		t.Fatalf("saved %q", saved)
	}
}

// A directory or file the provider cannot read makes a search incomplete, and
// the result says why.
func TestSearchSaysWhenItIsIncomplete(t *testing.T) {
	r := newRigWith(t, withFaults(faults{readDir: []string{"locked"}, open: []string{"sealed.txt"}}))
	r.put("s/a.txt", "hit\n")
	r.put("s/sealed.txt", "hit\n")
	r.put("s/locked/b.txt", "hit\n")
	const footer = "(The search is incomplete: unreadable. Narrow the path or the pattern.)"
	res := r.wantText(fstools.GlobName, map[string]any{"pattern": "**/*.txt", "path": "s"}, footer, r.path("s/a.txt"))
	if strings.Contains(res.Text, "b.txt") {
		t.Fatalf("glob listed a file in an unreadable directory:\n%s", res.Text)
	}
	res = r.wantText(fstools.GrepName, map[string]any{"pattern": "hit", "path": "s"}, footer, "Found 1 matches\n\n"+r.path("s/a.txt"))
	if strings.Contains(res.Text, "sealed.txt") {
		t.Fatalf("grep matched an unreadable file:\n%s", res.Text)
	}
	r.wantText(fstools.GrepName, map[string]any{"pattern": "absent", "path": "s"}, "No matches found\n"+footer)
}

// An empty path argument is the working directory: glob and grep there declare
// a read of ".".
func TestEmptyPathIsTheWorkingDirectory(t *testing.T) {
	r := newRig(t)
	r.put("here.txt", "hit\n")
	r.wantText(fstools.GlobName, map[string]any{"pattern": "*.txt"}, r.path("here.txt"))
	r.wantText(fstools.GrepName, map[string]any{"pattern": "hit"}, r.path("here.txt")+":\n  Line 1: hit")
	ds, err := fstools.Declarations(ops.New(r.root, local.New(), nil, ops.Options{}), nil, fstools.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		if d.Name() != fstools.GlobName && d.Name() != fstools.GrepName {
			continue
		}
		if got := d.Classify(json.RawMessage(`{"pattern":"x"}`)).ReadKeys(); !slices.Equal(got, []string{"fs:."}) {
			t.Fatalf("%s with no path reads %q", d.Name(), got)
		}
	}
}

// newRegistry is a tool registry over a scope table, for mounting the tools
// without the rig.
func newRegistry(t *testing.T) (*cordis.Context, htools.Service) {
	t.Helper()
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
	return root, core.NewWith(root, tab, core.Config{Checkpoint: checkpoint.Default{}})
}

func names(svc htools.Service) []string {
	var out []string
	for _, e := range svc.Catalogue() {
		out = append(out, e.Name)
	}
	return out
}

// The component registers the enabled tools over the injected operation
// service and registry; its catalogue item carries its config type.
func TestComponentMountsTheEnabledTools(t *testing.T) {
	root, svc := newRegistry(t)
	wd := dirs{t.TempDir()}
	o := ops.New(root, local.New(), &sandboxtest.Resolver{Default: sandbox.WorkspaceWrite, Workdir: wd}, ops.Options{Workdir: wd})
	if _, err := ops.Key.Provide(root, o); err != nil {
		t.Fatal(err)
	}
	if _, err := htools.Key.Provide(root, svc); err != nil {
		t.Fatal(err)
	}
	off := false
	f, err := root.Use(fstools.Component, fstools.Config{ApplyPatch: &off})
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	want := []string{fstools.EditName, fstools.GlobName, fstools.GrepName, fstools.ReadName, fstools.WriteName}
	if got := names(svc); !slices.Equal(got, want) {
		t.Fatalf("registered %q, want %q", got, want)
	}

	c, err := catalog.Build([]catalog.Item{fstools.Item()})
	if err != nil {
		t.Fatal(err)
	}
	it, err := c.Lookup(fstools.Name)
	if err != nil || it.Component != fstools.Component || it.Config != reflect.TypeFor[fstools.Config]() {
		t.Fatalf("item %+v, %v", it, err)
	}
}

// A mistyped dependency fails the component's load and registers nothing.
func TestComponentFailsOnAMistypedDependency(t *testing.T) {
	for _, bad := range []string{ops.Key.Name(), htools.Key.Name()} {
		t.Run(bad, func(t *testing.T) {
			root, svc := newRegistry(t)
			values := map[string]any{
				ops.Key.Name():    ops.New(root, local.New(), nil, ops.Options{}),
				htools.Key.Name(): svc,
			}
			values[bad] = "not a service"
			for name, v := range values {
				if _, err := root.Provide(name, v); err != nil {
					t.Fatal(err)
				}
			}
			f, err := root.Use(fstools.Component, fstools.Config{})
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Wait(); err == nil {
				t.Fatalf("the component loaded with %s mistyped", bad)
			}
			if got := names(svc); len(got) != 0 {
				t.Fatalf("a failed load registered %q", got)
			}
		})
	}
}

// Declarations needs the operation service, and Mount refuses a second set of
// the same tools in one scope, leaving the first in place.
func TestMountRefusals(t *testing.T) {
	root, svc := newRegistry(t)
	if _, err := fstools.Declarations(nil, nil, fstools.Config{}); err == nil {
		t.Fatal("Declarations without an operation service succeeded")
	}
	if _, err := fstools.Mount(root, svc, nil, nil, fstools.Config{}); err == nil {
		t.Fatal("Mount without an operation service succeeded")
	}
	o := ops.New(root, local.New(), nil, ops.Options{})
	if _, err := fstools.Mount(root, svc, o, nil, fstools.Config{}); err != nil {
		t.Fatal(err)
	}
	if _, err := fstools.Mount(root, svc, o, nil, fstools.Config{}); err == nil {
		t.Fatal("a second mount in the same scope succeeded")
	}
	if got := names(svc); len(got) != 6 {
		t.Fatalf("registered %q after a refused second mount", got)
	}
}
