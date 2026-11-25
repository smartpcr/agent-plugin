package local_test

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/fstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// link makes link point at the directory target, skipping the test on a lane
// that can make no directory link.
func link(t *testing.T, target, link string) string {
	t.Helper()
	kind, err := makeLink(target, link)
	if errors.Is(err, fstest.ErrLinkUnsupported) {
		t.Skipf("no directory link on this lane: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return kind
}

// A Ref that names no absolute path is fs/invalid-path on every call, before
// anything is read or changed; a relative path with an absolute Dir is joined.
func TestRefsThatNameNoPathAreRefused(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p := sandboxtest.Policy(sandbox.WorkspaceWrite, dir)
	l := local.New()
	writeFile(t, filepath.Join(dir, "f"), "x")
	good := fs.Ref{Path: filepath.Join(dir, "f")}
	for name, r := range map[string]fs.Ref{
		"empty":                 {},
		"NUL byte":              {Path: filepath.Join(dir, "a\x00b")},
		"relative with no Dir":  {Path: "f"},
		"relative, Dir too":     {Path: "f", Dir: "rel"},
		"relative, Dir escapes": {Path: "f\x00", Dir: dir},
	} {
		_, err := l.Stat(ctx, r)
		wantCode(t, name+": Stat", err, fs.CodeInvalidPath)
		_, _, err = l.Open(ctx, r)
		wantCode(t, name+": Open", err, fs.CodeInvalidPath)
		_, _, err = l.ReadDir(ctx, r, 0)
		wantCode(t, name+": ReadDir", err, fs.CodeInvalidPath)
		_, err = l.Write(ctx, r, []byte("y"), fs.NoGuard, p)
		wantCode(t, name+": Write", err, fs.CodeInvalidPath)
		_, err = l.Delete(ctx, r, fs.NoGuard, p)
		wantCode(t, name+": Delete", err, fs.CodeInvalidPath)
		_, err = l.Move(ctx, r, fs.Ref{Path: filepath.Join(dir, "g")}, fs.MoveOptions{}, p)
		wantCode(t, name+": Move source", err, fs.CodeInvalidPath)
		_, err = l.Move(ctx, good, r, fs.MoveOptions{}, p)
		wantCode(t, name+": Move destination", err, fs.CodeInvalidPath)
	}
	if b, err := os.ReadFile(good.Path); err != nil || string(b) != "x" {
		t.Fatalf("f = %q, %v after the refused calls", b, err)
	}
	in, err := l.Stat(ctx, fs.Ref{Path: "f", Dir: dir})
	if err != nil || !in.Exists || in.Type != fs.TypeFile {
		t.Fatalf("relative f in %s = %+v, %v", dir, in, err)
	}
}

// A call whose context has already ended reads and changes nothing.
func TestEndedContextIsRefused(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	writeFile(t, f, "x")
	p := sandboxtest.Policy(sandbox.WorkspaceWrite, dir)
	l := local.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := l.Stat(ctx, fs.Ref{Path: f})
	wantCode(t, "Stat", err, fs.CodeAborted)
	_, _, err = l.Open(ctx, fs.Ref{Path: f})
	wantCode(t, "Open", err, fs.CodeAborted)
	_, err = l.Write(ctx, fs.Ref{Path: f}, []byte("y"), fs.NoGuard, p)
	wantCode(t, "Write", err, fs.CodeAborted)
	_, err = l.Delete(ctx, fs.Ref{Path: f}, fs.NoGuard, p)
	wantCode(t, "Delete", err, fs.CodeAborted)
	if b, err := os.ReadFile(f); err != nil || string(b) != "x" {
		t.Fatalf("f = %q, %v", b, err)
	}
}

// A path through a regular file is fs/not-directory; opening a directory is
// fs/not-regular-file, as is writing over one; a directory cannot be moved into
// itself, nor can a move overwrite a directory or with one.
func TestWrongKindsAreRefused(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f, d := filepath.Join(dir, "f"), filepath.Join(dir, "d")
	writeFile(t, f, "x")
	mkdirs(t, filepath.Join(d, "sub"))
	p := sandboxtest.Policy(sandbox.WorkspaceWrite, dir)
	l := local.New()

	_, err := l.Stat(ctx, fs.Ref{Path: filepath.Join(f, "below")})
	wantCode(t, "Stat through a file", err, fs.CodeNotDirectory)
	_, err = l.Write(ctx, fs.Ref{Path: filepath.Join(f, "below")}, []byte("y"), fs.NoGuard, p)
	wantCode(t, "Write through a file", err, fs.CodeNotDirectory)
	_, _, err = l.Open(ctx, fs.Ref{Path: d})
	wantCode(t, "Open a directory", err, fs.CodeNotRegularFile)
	_, _, err = l.Open(ctx, fs.Ref{Path: filepath.Join(dir, "missing")})
	wantCode(t, "Open a missing file", err, fs.CodeNotFound)
	_, err = l.Write(ctx, fs.Ref{Path: d}, []byte("y"), fs.NoGuard, p)
	wantCode(t, "Write over a directory", err, fs.CodeNotRegularFile)
	_, err = l.Move(ctx, fs.Ref{Path: d}, fs.Ref{Path: filepath.Join(d, "sub", "d")}, fs.MoveOptions{}, p)
	wantCode(t, "Move a directory into itself", err, fs.CodeInvalidPath)
	_, err = l.Move(ctx, fs.Ref{Path: f}, fs.Ref{Path: d}, fs.MoveOptions{Overwrite: true}, p)
	wantCode(t, "Move a file over a directory", err, fs.CodeNotRegularFile)
	_, err = l.Move(ctx, fs.Ref{Path: d}, fs.Ref{Path: f}, fs.MoveOptions{Overwrite: true}, p)
	wantCode(t, "Move a directory over a file", err, fs.CodeNotRegularFile)
	if got := entries(t, dir); len(got) != 3 {
		t.Fatalf("the tree changed: %v", got)
	}
}

// A write root not created yet is created by the first write below it; a
// missing directory above a write root is outside every root and is never
// created, so the call is fs/not-found and leaves nothing behind.
func TestWriteRootIsCreatedButNotItsParents(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	l := local.New()
	r := filepath.Join(tmp, "R")
	out, err := l.Write(ctx, fs.Ref{Path: filepath.Join(r, "a", "f")}, []byte("x"), fs.CreateIfAbsent, sandboxtest.Policy(sandbox.WorkspaceWrite, r))
	if err != nil || !out.Created {
		t.Fatalf("write below a root not made yet: %+v, %v", out, err)
	}
	if b, err := os.ReadFile(filepath.Join(r, "a", "f")); err != nil || string(b) != "x" {
		t.Fatalf("R/a/f = %q, %v", b, err)
	}

	deep := filepath.Join(tmp, "missing", "R")
	p := sandboxtest.Policy(sandbox.WorkspaceWrite, deep)
	_, err = l.Write(ctx, fs.Ref{Path: filepath.Join(deep, "f")}, []byte("x"), fs.NoGuard, p)
	wantCode(t, "write below a root whose parent is missing", err, fs.CodeNotFound)
	_, err = l.Delete(ctx, fs.Ref{Path: filepath.Join(deep, "f")}, fs.NoGuard, p)
	wantCode(t, "delete below a root whose parent is missing", err, fs.CodeNotFound)
	if _, err := os.Lstat(filepath.Join(tmp, "missing")); !errors.Is(err, iofs.ErrNotExist) {
		t.Fatalf("the missing parent was created: %v", err)
	}
}

// A write root that is a regular file is not a directory the call can work
// under: the write fails fs/not-directory and the file keeps its bytes.
func TestWriteRootThatIsAFileIsRefused(t *testing.T) {
	r := filepath.Join(t.TempDir(), "R")
	writeFile(t, r, "file")
	_, err := local.New().Write(context.Background(), fs.Ref{Path: r}, []byte("x"), fs.NoGuard, sandboxtest.Policy(sandbox.WorkspaceWrite, r))
	wantCode(t, "write a root that is a file", err, fs.CodeNotDirectory)
	if b, err := os.ReadFile(r); err != nil || string(b) != "file" {
		t.Fatalf("R = %q, %v", b, err)
	}
}

// A create-if-absent write whose name is taken after it was checked, while
// its content is staged, never replaces the file that appeared: fs/already-exists,
// the other bytes kept and no staged file left.
func TestCreateIfAbsentLosesToAFileThatAppears(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	t.Cleanup(local.AfterStaging(func() { writeFile(t, f, "other") }))
	_, err := local.New().Write(context.Background(), fs.Ref{Path: f}, []byte("mine"), fs.CreateIfAbsent, sandboxtest.Policy(sandbox.WorkspaceWrite, dir))
	wantCode(t, "create racing another creator", err, fs.CodeAlreadyExists)
	if b, err := os.ReadFile(f); err != nil || string(b) != "other" {
		t.Fatalf("f = %q, %v; want the other creator's bytes", b, err)
	}
	for _, e := range entries(t, dir) {
		if strings.HasPrefix(filepath.Base(e), fsx.StagePrefix) {
			t.Fatalf("staged file left behind: %s", e)
		}
	}
}

// ReadDir does not list the files fsx is staging, and a listing longer than
// the limit is cut and says so.
func TestReadDirHidesStagedFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, fsx.StagePrefix+"123"), "staged")
	writeFile(t, filepath.Join(dir, "a"), "a")
	writeFile(t, filepath.Join(dir, "b"), "b")
	l := local.New()
	ents, more, err := l.ReadDir(context.Background(), fs.Ref{Path: dir}, 0)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name)
	}
	if err != nil || more || !slices.Equal(names, []string{"a", "b"}) {
		t.Fatalf("ReadDir = %v more=%v, %v; want a and b only", names, more, err)
	}
	if ents, more, err := l.ReadDir(context.Background(), fs.Ref{Path: dir}, 1); err != nil || !more || len(ents) != 1 {
		t.Fatalf("ReadDir limit 1 = %v more=%v, %v", ents, more, err)
	}
}

// A cycle of links is fs/io (too many links), never a hang.
func TestLinkCycleIsRefused(t *testing.T) {
	tmp := t.TempDir()
	a, b := filepath.Join(tmp, "a"), filepath.Join(tmp, "b")
	link(t, b, a)
	t.Cleanup(func() { _ = os.Remove(a) })
	link(t, a, b)
	t.Cleanup(func() { _ = os.Remove(b) })
	_, err := local.New().Stat(context.Background(), fs.Ref{Path: filepath.Join(a, "f")})
	wantCode(t, "Stat through a link cycle", err, fs.CodeIO)
	if !strings.Contains(err.Error(), "too many links") {
		t.Fatalf("Stat through a link cycle: %v", err)
	}
}

// A link whose target can no longer be read (removed between the Lstat and
// the Readlink) is still not taken for a file or a directory, and a path
// through it is a tree that is changing: fs/not-directory once the budget of
// retries is spent.
func TestLinkThatCannotBeReadIsNotFollowed(t *testing.T) {
	tmp := t.TempDir()
	target, ln := filepath.Join(tmp, "target"), filepath.Join(tmp, "ln")
	mkdirs(t, target)
	kind := link(t, target, ln)
	t.Cleanup(func() { _ = os.Remove(ln) })
	t.Cleanup(local.FailReadlink())
	l := local.New()
	in, err := l.Stat(context.Background(), fs.Ref{Path: ln, NoFollowFinal: true})
	want := fs.TypeSymlink
	if kind == "junction" {
		want = fs.TypeOther // a junction is irregular, not a symbolic link, to Lstat
	}
	if err != nil || !in.Exists || in.Type != want {
		t.Fatalf("Stat(%s %s) = %+v, %v; want type %s", kind, ln, in, err, want)
	}
	_, err = l.Write(context.Background(), fs.Ref{Path: filepath.Join(ln, "f")}, []byte("x"), fs.NoGuard, sandboxtest.Policy(sandbox.WorkspaceWrite, tmp))
	wantCode(t, "write through the unreadable link", err, fs.CodeNotDirectory)
	if got := entries(t, target); len(got) != 0 {
		t.Fatalf("the link's target changed: %v", got)
	}
}

// A delete guarded by the version of a link (read without following it)
// deletes the link, never what it points at.
func TestDeleteGuardedLinkDeletesTheLink(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	target, ln := filepath.Join(tmp, "target"), filepath.Join(tmp, "ln")
	mkdirs(t, target)
	writeFile(t, filepath.Join(target, "keep"), "k")
	link(t, target, ln)
	t.Cleanup(func() { _ = os.Remove(ln) })
	l := local.New()
	in, err := l.Stat(ctx, fs.Ref{Path: ln, NoFollowFinal: true})
	if err != nil || in.Type != fs.TypeSymlink {
		t.Fatalf("Stat(link) = %+v, %v", in, err)
	}
	out, err := l.Delete(ctx, fs.Ref{Path: ln}, fs.ReplaceIfVersion{Version: in.Version}, sandboxtest.Policy(sandbox.WorkspaceWrite, tmp))
	if err != nil || !out.Existed {
		t.Fatalf("guarded delete of the link = %+v, %v", out, err)
	}
	if _, err := os.Lstat(ln); !errors.Is(err, iofs.ErrNotExist) {
		t.Fatalf("the link is still there: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(target, "keep")); err != nil || string(b) != "k" {
		t.Fatalf("the link's target lost its content: %q, %v", b, err)
	}
}

// A link that keeps reappearing on the path after each resolution, every time
// in a place the fence admits, is followed again at most eight times
// (filesystem.md sec. 6.1); then the call is fs/sandbox-denied and nothing was
// written.
func TestRelinkingIsBoundedToEightAttempts(t *testing.T) {
	tmp := t.TempDir()
	r, k := filepath.Join(tmp, "R"), filepath.Join(tmp, "K")
	mkdirs(t, r)
	partner := map[string]string{"R": k, "K": r}
	isLink := func(p string) bool {
		fi, err := os.Lstat(p)
		return err == nil && fi.Mode()&(iofs.ModeSymlink|iofs.ModeIrregular) != 0
	}
	t.Cleanup(func() {
		for _, p := range []string{r, k} {
			if isLink(p) {
				_ = os.Remove(p)
			}
		}
	})
	if _, err := makeLink(tmp, filepath.Join(tmp, "probe")); errors.Is(err, fstest.ErrLinkUnsupported) {
		t.Skipf("no directory link on this lane: %v", err)
	}
	_ = os.Remove(filepath.Join(tmp, "probe"))

	// Each time the call is about to open its root (R or K, whichever the path
	// resolved to), that root becomes a link to its partner: the previous link
	// is removed, the real directory moved to the partner's name and a link put
	// in its place. The next resolution follows the link to the partner, a root
	// the fence admits, which the next swap turns into a link in its turn.
	opened := 0
	t.Cleanup(local.SetBeforeOpenRoot(func(dir string) {
		opened++
		other, ok := partner[filepath.Base(dir)]
		if !ok {
			t.Errorf("opened root %s, want R or K", dir)
			return
		}
		if isLink(other) {
			if err := os.Remove(other); err != nil {
				t.Errorf("remove the link %s: %v", other, err)
				return
			}
		}
		if err := os.Rename(dir, other); err != nil {
			t.Errorf("move %s to %s: %v", dir, other, err)
			return
		}
		if _, err := makeLink(other, dir); err != nil {
			t.Errorf("link %s -> %s: %v", dir, other, err)
		}
	}))
	_, err := local.New().Write(context.Background(), fs.Ref{Path: filepath.Join(r, "f")}, []byte("x"), fs.NoGuard, sandboxtest.Policy(sandbox.WorkspaceWrite, r, k))
	wantCode(t, "write while a link keeps reappearing", err, fs.CodeSandboxDenied)
	if opened != 8 {
		t.Fatalf("the root was opened %d times, want 8", opened)
	}
	for _, p := range []string{r, k} {
		if !isLink(p) {
			if got := entries(t, p); len(got) != 0 {
				t.Fatalf("%s holds %v", p, got)
			}
		}
	}
}

// The lock directory must be a real directory: one that cannot be created, or
// that is a link, fails every mutation fs/io before anything is written.
func TestUnusableLockDirFailsMutations(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "work")
	mkdirs(t, dir)
	src := filepath.Join(dir, "src")
	writeFile(t, src, "s")
	p := sandboxtest.Policy(sandbox.WorkspaceWrite, dir)
	blocker := filepath.Join(tmp, "blocker")
	writeFile(t, blocker, "a file where the lock directory's parent should be")
	real := filepath.Join(tmp, "real-locks")
	mkdirs(t, real)
	linked := filepath.Join(tmp, "linked-locks")
	link(t, real, linked)
	t.Cleanup(func() { _ = os.Remove(linked) })

	for name, lockDir := range map[string]string{
		"under a file": filepath.Join(blocker, "locks"),
		"a link":       linked,
	} {
		l := local.NewWithOptions(local.Options{LockDir: lockDir})
		_, err := l.Write(ctx, fs.Ref{Path: filepath.Join(dir, "f")}, []byte("x"), fs.NoGuard, p)
		wantCode(t, name+": Write", err, fs.CodeIO)
		_, err = l.Delete(ctx, fs.Ref{Path: src}, fs.NoGuard, p)
		wantCode(t, name+": Delete", err, fs.CodeIO)
		_, err = l.Move(ctx, fs.Ref{Path: src}, fs.Ref{Path: filepath.Join(dir, "dst")}, fs.MoveOptions{}, p)
		wantCode(t, name+": Move", err, fs.CodeIO)
	}
	if got := entries(t, dir); len(got) != 1 {
		t.Fatalf("the work directory changed: %v", got)
	}
	if got := entries(t, real); len(got) != 0 {
		t.Fatalf("lock files were planted through the link: %v", got)
	}
}

// With no LockDir the provider uses DefaultLockDir: under the user's cache
// directory, or a per-user name in the temporary directory when that is
// unknown.
func TestDefaultLockDir(t *testing.T) {
	if cache, err := os.UserCacheDir(); err == nil && cache != "" {
		if got := local.DefaultLockDir(); got != filepath.Join(cache, "dsh", "fs-locks") {
			t.Fatalf("DefaultLockDir = %s, want below %s", got, cache)
		}
	}
	switch runtime.GOOS {
	case "windows":
		t.Setenv("LocalAppData", "")
	case "darwin", "ios", "plan9":
		t.Setenv("HOME", "")
	default:
		t.Setenv("XDG_CACHE_HOME", "")
		t.Setenv("HOME", "")
	}
	if _, err := os.UserCacheDir(); err == nil {
		t.Skip("the user cache directory is still known")
	}
	want := filepath.Join(os.TempDir(), "dsh-fs-locks")
	if uid := os.Getuid(); uid >= 0 {
		want = filepath.Join(os.TempDir(), fmt.Sprintf("dsh-fs-locks-%d", uid))
	}
	if got := local.DefaultLockDir(); got != want {
		t.Fatalf("DefaultLockDir with no cache directory = %s, want %s", got, want)
	}
}

// The component provides the provider under fs.Key, configured by its Options:
// its locks are taken in the configured LockDir.
func TestComponentProvidesTheSeam(t *testing.T) {
	root := testkit.Tree(t)
	locks, dir := t.TempDir(), t.TempDir()
	f, err := root.Use(local.Component, local.Options{LockDir: locks})
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	seam, err := fs.Key.Get(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seam.Write(context.Background(), fs.Ref{Path: filepath.Join(dir, "f")}, []byte("x"), fs.NoGuard, sandboxtest.Policy(sandbox.WorkspaceWrite, dir)); err != nil {
		t.Fatal(err)
	}
	if got := entries(t, locks); len(got) != 1 || !strings.HasSuffix(got[0], ".lock") {
		t.Fatalf("lock directory holds %v, want one lock file", got)
	}
}
