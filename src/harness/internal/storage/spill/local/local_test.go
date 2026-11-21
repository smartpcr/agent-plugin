package local_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

var bg = context.Background()

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func newHome(t *testing.T) home.Layout {
	t.Helper()
	l := home.New(t.TempDir())
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	return l
}

func newStore(t *testing.T, l home.Layout, opts ...local.Option) *local.Store {
	t.Helper()
	s, err := local.New(l, local.DefaultConfig, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func readAll(t *testing.T, s spill.Store, caller, id string) []byte {
	t.Helper()
	r, err := s.Open(bg, caller, id)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	return must(io.ReadAll(r))
}

// Scenario: Large result spilled and recoverable (DH-D-STORAGE-5, TC-08). A 5 MB
// tool result stored through the spill store: the logged row holds a head no
// larger than the bound and the reference; reading the reference returns all
// 5 MB byte-identical.
func TestLargeResultSpilledAndRecoverable(t *testing.T) {
	l := newHome(t)
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	defer root.Dispose()
	st := must(sqlite.OpenHome(bg, l))
	defer func() {
		if err := st.Close(bg); err != nil {
			t.Fatal(err)
		}
	}()
	svc := session.NewService(root, st, session.Config{})
	sp := newStore(t, l, local.WithLineage(local.ForkLineage(st)))

	h := must(svc.Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	id := string(h.ID())

	const headBound = 16 << 10
	full := make([]byte, 5<<20)
	_, _ = rand.Read(full)
	// Make it text with multi-byte runes around the cut.
	for i := range full {
		full[i] = 'a' + full[i]%26
	}
	copy(full[headBound-1:], "\u00e9")
	head, ref, err := spill.Save(bg, sp, id, "out.log", full, headBound)
	if err != nil {
		t.Fatal(err)
	}
	if ref == nil || ref.Bytes != int64(len(full)) {
		t.Fatalf("reference %+v, want %d bytes", ref, len(full))
	}
	if len(head) > headBound || !bytes.HasPrefix(full, head) {
		t.Fatalf("head of %d bytes is not a prefix within %d", len(head), headBound)
	}

	user := must(vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("run it")))
	call := must(vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, vocab.ToolUseBlock("c1", "shell", json.RawMessage(`{}`))))
	result := must(vocab.RestoreMessage("r1", vocab.RoleTool, nil, vocab.ToolResultBlock("c1", false, vocab.TextPart(string(head)))))
	must(h.Append(bg, must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user}))))
	must(h.Append(bg, must(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: call}))))
	must(h.Append(bg, must(event.ToolResult.Draft(event.Append(), event.ToolResultPayload{CallID: "c1", Message: result, Spill: ref}))))
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}

	r := must(st.OpenRead(bg, id))
	defer r.Close(bg)
	recs := must(r.Read(bg, 0, 0, store.ReadOptions{}))
	var logged *event.ToolResultPayload
	for _, rec := range recs {
		if rec.Type == "tool/result" {
			logged = new(event.ToolResultPayload)
			if err := json.Unmarshal([]byte(rec.Payload), logged); err != nil {
				t.Fatal(err)
			}
			if len(rec.Payload) > headBound+4096 {
				t.Fatalf("the logged row is %d bytes, the head bound is %d", len(rec.Payload), headBound)
			}
			t.Logf("logged tool/result row: %d bytes (head %d of %d bytes, bound %d)", len(rec.Payload), len(head), len(full), headBound)
		}
	}
	if logged == nil || logged.Spill == nil || *logged.Spill != *ref {
		t.Fatalf("logged spill %+v, want %+v", logged, ref)
	}
	if blocks := logged.Message.Blocks(); len(blocks) != 1 {
		t.Fatalf("logged result has %d blocks", len(blocks))
	}
	if got := readAll(t, sp, id, logged.Spill.Path); !bytes.Equal(got, full) {
		t.Fatalf("read back %d bytes, not byte-identical to the %d saved", len(got), len(full))
	}
	// The owner's directory is the one session deletion removes.
	if dir, _, _ := spill.ParseID(ref.Path); dir != sqlite.SpillName(id) {
		t.Fatalf("owner dir %s, the session store removes %s", dir, sqlite.SpillName(id))
	}
}

func TestSmallResultIsNotSpilled(t *testing.T) {
	sp := newStore(t, newHome(t))
	head, ref, err := spill.Save(bg, sp, "s1", "x", []byte("short"), 16)
	if err != nil || ref != nil || string(head) != "short" {
		t.Fatalf("head %q ref %+v err %v", head, ref, err)
	}
}

func TestHeadNeverSplitsARune(t *testing.T) {
	text := []byte("ab\u4e16cd")
	for n := 0; n <= len(text); n++ {
		h := spill.Head(text, n)
		if len(h) > n || !strings.HasPrefix(string(text), string(h)) || !isValidUTF8(h) {
			t.Fatalf("Head(%d) = %q", n, h)
		}
	}
}

func isValidUTF8(b []byte) bool { return strings.ToValidUTF8(string(b), "\x00") == string(b) }

// DH-T-STORAGE-9: names are sanitised into one safe segment.
func TestSanitizeName(t *testing.T) {
	for _, in := range []string{"../../etc/passwd", `a\b`, "..", "", "CON", "nul.txt", "a\x00b", strings.Repeat("x", 300), "/abs"} {
		got := spill.SanitizeName(in)
		if got == "" || got == "." || got == ".." || strings.ContainsAny(got, "/\\\x00") || len(got) > spill.MaxNameLen {
			t.Fatalf("SanitizeName(%q) = %q", in, got)
		}
		if strings.EqualFold(strings.SplitN(got, ".", 2)[0], "con") || strings.EqualFold(strings.SplitN(got, ".", 2)[0], "nul") {
			t.Fatalf("SanitizeName(%q) = %q is a device name", in, got)
		}
		id := spill.NewID("s", in)
		if len(id) > spill.MaxIDLen {
			t.Fatalf("id %q is longer than %d", id, spill.MaxIDLen)
		}
		if _, _, err := spill.ParseID(id); err != nil {
			t.Fatalf("NewID(%q) = %q does not parse: %v", in, id, err)
		}
	}
}

// DH-T-STORAGE-9: an id that could leave <home>/spill is refused.
func TestEscapingIDsRefused(t *testing.T) {
	l := newHome(t)
	sp := newStore(t, l)
	if err := os.WriteFile(filepath.Join(l.Root(), "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := spill.OwnerDir("s1")
	for _, id := range []string{
		"../secret", dir + "/../../secret", dir + "/" + strings.Repeat("0", spill.RandomLen) + "-../x",
		filepath.Join(l.Root(), "secret"), "", dir, dir + "/", dir + `\..\..\secret`,
	} {
		if _, err := sp.Open(bg, "s1", id); !herr.Has(err, spill.ReasonNotFound) {
			t.Fatalf("Open(%q) = %v, want spill/not-found", id, err)
		}
		if _, ok := sp.HostPath(id); ok {
			t.Fatalf("HostPath(%q) has a path", id)
		}
	}
}

func FuzzParseID(f *testing.F) {
	f.Add(spill.NewID("s", "x.txt"))
	f.Add("../a")
	f.Fuzz(func(t *testing.T, id string) {
		dir, file, err := spill.ParseID(id)
		if err != nil {
			return
		}
		rel := filepath.Join(dir, file)
		if !filepath.IsLocal(rel) || strings.ContainsAny(file, "/\\") || file == ".." {
			t.Fatalf("ParseID(%q) accepted an escaping path", id)
		}
	})
}

func TestOverLimitAndAbortLeaveNoFile(t *testing.T) {
	l := newHome(t)
	sp := newStore(t, l)
	ref := sp.Reserve("s1", "big")
	w := must(sp.Create(bg, ref, 8))
	if _, err := w.Write([]byte("0123456789")); !herr.Has(err, spill.ReasonTooLarge) {
		t.Fatalf("Write past the limit = %v", err)
	}
	if _, err := sp.Stat(bg, ref.ID); !herr.Has(err, spill.ReasonNotFound) {
		t.Fatalf("Stat after an over-limit write = %v", err)
	}

	ref = sp.Reserve("s1", "aborted")
	w = must(sp.Create(bg, ref, 0))
	must(w.Write([]byte("partial")))
	if err := w.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Stat(bg, ref.ID); !herr.Has(err, spill.ReasonNotFound) {
		t.Fatalf("Stat after Abort = %v", err)
	}
}

func TestCreateIsExclusive(t *testing.T) {
	sp := newStore(t, newHome(t))
	ref := sp.Reserve("s1", "x")
	w := must(sp.Create(bg, ref, 0))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Create(bg, ref, 0); err == nil {
		t.Fatal("a second Create of the same id succeeded")
	}
	if _, err := sp.Create(bg, spill.Ref{ID: ref.ID, Owner: "s2"}, 0); !herr.Has(err, spill.ReasonNotOwner) {
		t.Fatalf("Create for another owner = %v", err)
	}
}

type lineage map[string]string

func (m lineage) Parent(_ context.Context, id string) (string, error) { return m[id], nil }

// DH-T-STORAGE-9: the owner and a fork read the file; an unrelated session gets
// spill/not-owner.
func TestOpenOwnerForkUnrelated(t *testing.T) {
	sp := newStore(t, newHome(t), local.WithLineage(lineage{"fork": "owner", "forkfork": "fork"}))
	_, ref, err := spill.Save(bg, sp, "owner", "x", []byte("0123456789"), 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"owner", "fork", "forkfork"} {
		if got := readAll(t, sp, c, ref.Path); string(got) != "0123456789" {
			t.Fatalf("%s read %q", c, got)
		}
	}
	if _, err := sp.Open(bg, "stranger", ref.Path); !herr.Has(err, spill.ReasonNotOwner) {
		t.Fatalf("an unrelated session's Open = %v", err)
	}
}

type citer map[string]bool

func (c citer) CitedByOther(_ context.Context, id, _ string) (bool, error) { return c[id], nil }

func TestDeleteKeepsCitedFiles(t *testing.T) {
	l := newHome(t)
	cited := citer{}
	sp := newStore(t, l, local.WithCiter(cited))
	_, a, _ := spill.Save(bg, sp, "s1", "a", []byte("aaaa"), 1)
	_, b, _ := spill.Save(bg, sp, "s1", "b", []byte("bbbb"), 1)
	cited[b.Path] = true
	if err := sp.Delete(bg, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Stat(bg, a.Path); !herr.Has(err, spill.ReasonNotFound) {
		t.Fatalf("uncited file survived: %v", err)
	}
	if _, err := sp.Stat(bg, b.Path); err != nil {
		t.Fatalf("cited file was removed: %v", err)
	}
	delete(cited, b.Path)
	if err := sp.Delete(bg, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(l.Spill(), spill.OwnerDir("s1"))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the emptied owner directory remains: %v", err)
	}
}

// A link planted where the owner's directory goes is not followed.
func TestPlantedLinkRefused(t *testing.T) {
	l := newHome(t)
	sp := newStore(t, l)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(l.Spill(), spill.OwnerDir("s1"))); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := spill.Save(bg, sp, "s1", "x", []byte("0123456789"), 2); err == nil {
		t.Fatal("a save through a planted link succeeded")
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Fatalf("the save wrote %d entries outside the spill root", len(ents))
	}
}

type statter map[string]string

func (m statter) Stat(_ context.Context, id string) (store.Summary, error) {
	if id == "broken" {
		return store.Summary{}, errors.New("no such session")
	}
	return store.Summary{Header: store.Header{ID: id, ForkSource: m[id]}}, nil
}

func TestForkLineageFollowsForkSource(t *testing.T) {
	sp := newStore(t, newHome(t), local.WithLineage(local.ForkLineage(statter{"child": "owner"})))
	_, ref, _ := spill.Save(bg, sp, "owner", "x", []byte("0123456789"), 2)
	if got := readAll(t, sp, "child", ref.Path); string(got) != "0123456789" {
		t.Fatalf("fork read %q", got)
	}
	if _, err := sp.Open(bg, "broken", ref.Path); err == nil || herr.Has(err, spill.ReasonNotOwner) {
		t.Fatalf("a lineage failure = %v, want it reported", err)
	}
}

func TestNewRefusesABadSpillDirectory(t *testing.T) {
	l := home.New(t.TempDir())
	if _, err := local.New(l, local.Config{}); err == nil {
		t.Fatal("New without a spill directory succeeded")
	}
	if err := os.WriteFile(l.Spill(), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := local.New(l, local.Config{}); !herr.Has(err, home.ReasonNotDirectory) {
		t.Fatalf("New over a file = %v", err)
	}
	if _, err := local.New(newHome(t), local.Config{MaxAge: -1}); err == nil {
		t.Fatal("a negative MaxAge was accepted")
	}
	s := newStore(t, newHome(t))
	if got := s.Config(); got.MaxSaveBytes != local.DefaultConfig.MaxSaveBytes {
		t.Fatalf("Config() = %+v", got)
	}
}

func TestWriterLifecycle(t *testing.T) {
	sp := newStore(t, newHome(t))
	ref := sp.Reserve("s1", "w")
	w := must(sp.Create(bg, ref, 0))
	must(w.Write([]byte("abc")))
	if w.Written() != 3 {
		t.Fatalf("Written() = %d", w.Written())
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("a second Close = %v", err)
	}
	if err := w.Abort(); err != nil {
		t.Fatalf("Abort after Close = %v", err)
	}
	if _, err := w.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Write after Close = %v", err)
	}
	info, err := sp.Stat(bg, ref.ID)
	if err != nil || info.Size != 3 || info.ID != ref.ID {
		t.Fatalf("Stat = %+v, %v", info, err)
	}

	aborted := must(sp.Create(bg, sp.Reserve("s1", "a"), 0))
	_ = aborted.Abort()
	if _, err := aborted.Write([]byte("x")); !herr.Has(err, spill.ReasonNotFound) {
		t.Fatalf("Write after Abort = %v", err)
	}
}

func TestCancelledAndMalformed(t *testing.T) {
	sp := newStore(t, newHome(t))
	ctx, cancel := context.WithCancel(bg)
	cancel()
	ref := sp.Reserve("s1", "x")
	if _, err := sp.Create(ctx, ref, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create on a cancelled context = %v", err)
	}
	if _, err := sp.Stat(ctx, ref.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stat on a cancelled context = %v", err)
	}
	if _, err := sp.Create(bg, spill.Ref{ID: "../x", Owner: "s1"}, 0); !herr.Has(err, spill.ReasonNotFound) {
		t.Fatalf("Create of a malformed id = %v", err)
	}
	if _, err := sp.Stat(bg, "../x"); !herr.Has(err, spill.ReasonNotFound) {
		t.Fatalf("Stat of a malformed id = %v", err)
	}
	if _, err := sp.Open(bg, "s1", ref.ID); !herr.Has(err, spill.ReasonNotFound) {
		t.Fatalf("Open of a reserved but unsaved id = %v", err)
	}
	if err := sp.Delete(bg, "nobody"); err != nil {
		t.Fatalf("Delete of an owner with no files = %v", err)
	}
}

func TestDirectoryIsNotAFile(t *testing.T) {
	l := newHome(t)
	sp := newStore(t, l)
	ref := sp.Reserve("s1", "d")
	if err := os.MkdirAll(filepath.Join(l.Spill(), filepath.FromSlash(ref.ID)), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Open(bg, "s1", ref.ID); !herr.Has(err, spill.ReasonNotFound) {
		t.Fatalf("Open of a directory = %v", err)
	}
	if _, err := sp.Stat(bg, ref.ID); !herr.Has(err, spill.ReasonNotFound) {
		t.Fatalf("Stat of a directory = %v", err)
	}
	if err := os.WriteFile(filepath.Join(l.Spill(), spill.OwnerDir("f")), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sp.Delete(bg, "f"); !herr.Has(err, home.ReasonNotDirectory) {
		t.Fatalf("Delete over a file = %v", err)
	}
	if _, _, err := spill.Save(bg, sp, "f", "x", []byte("0123"), 1); err == nil {
		t.Fatal("a save over a file in place of the owner directory succeeded")
	}
}

type failingCiter struct{}

func (failingCiter) CitedByOther(context.Context, string, string) (bool, error) {
	return false, errors.New("store down")
}

func TestDeleteKeepsFilesWhenCitationsAreUnknown(t *testing.T) {
	sp := newStore(t, newHome(t), local.WithCiter(failingCiter{}))
	_, ref, _ := spill.Save(bg, sp, "s1", "a", []byte("aaaa"), 1)
	if err := sp.Delete(bg, "s1"); err == nil {
		t.Fatal("Delete hid the citation failure")
	}
	if _, err := sp.Stat(bg, ref.Path); err != nil {
		t.Fatalf("a file whose citations are unknown was removed: %v", err)
	}
}

func TestCloseSyncsTheSessionDirectory(t *testing.T) {
	l := newHome(t)
	sp := newStore(t, l)
	var dirs []string
	local.SetSyncDir(t, func(d string) error { dirs = append(dirs, filepath.Clean(d)); return nil })
	ref := sp.Reserve("s1", "out")
	w := must(sp.Create(bg, ref, 0))
	must(w.Write([]byte("abc")))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	dir, _, _ := spill.ParseID(ref.ID)
	if want := filepath.Join(l.Spill(), dir); len(dirs) != 1 || dirs[0] != want {
		t.Fatalf("synced %v, want %s", dirs, want)
	}
}

func TestCloseSyncFailureRemovesTheFile(t *testing.T) {
	sp := newStore(t, newHome(t))
	local.SetSyncDir(t, func(string) error { return errors.New("sync failed") })
	ref := sp.Reserve("s1", "out")
	w := must(sp.Create(bg, ref, 0))
	must(w.Write([]byte("abc")))
	if err := w.Close(); !herr.Has(err, home.ReasonIO) {
		t.Fatalf("Close with a failing sync = %v", err)
	}
	if _, err := sp.Stat(bg, ref.ID); !herr.Has(err, spill.ReasonNotFound) {
		t.Fatalf("Stat after a failed Close = %v, want %s", err, spill.ReasonNotFound)
	}
}
