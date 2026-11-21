package attachments_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/attachments"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/blob"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

var bg = context.Background()

func newStore(t *testing.T, cfg attachments.Config) *attachments.Store {
	t.Helper()
	l := home.New(t.TempDir())
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	b, err := blob.New(l)
	if err != nil {
		t.Fatal(err)
	}
	s, err := attachments.New(b, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSaveFileStoredAndReadBackVerified(t *testing.T) {
	s := newStore(t, attachments.Config{})
	fr, err := s.SaveFile(bg, "../report.csv", strings.NewReader("a,b\n1,2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if fr.Kind != blob.KindFile || strings.ContainsAny(fr.Name, `/\`) || fr.Name == ".." {
		t.Fatalf("file ref %+v", fr)
	}
	r, err := s.Open(bg, fr.Ref)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	r.Close()
	if err != nil || string(got) != "a,b\n1,2\n" {
		t.Fatalf("read back %q, %v", got, err)
	}

	path, ok := s.HostPath(fr)
	if !ok || filepath.Base(path) != fr.Name {
		t.Fatalf("host path %q %v, want one named %q", path, ok, fr.Name)
	}
	if ref, ok := s.LookupPath(path); !ok || ref != fr.Ref {
		t.Fatalf("LookupPath(named link) = %+v %v", ref, ok)
	}
	// Saving the same bytes again under the same name is one object, one link.
	again, err := s.SaveFile(bg, "../report.csv", strings.NewReader("a,b\n1,2\n"))
	if err != nil || again != fr {
		t.Fatalf("second save %+v, %v", again, err)
	}
}

// Scenario: Corrupted attachment detected (DH-D-STORAGE-3, TC-08), through the
// attachment store.
func TestCorruptedAttachmentDetected(t *testing.T) {
	s := newStore(t, attachments.Config{})
	fr, err := s.SaveFile(bg, "notes.txt", strings.NewReader("the original bytes"))
	if err != nil {
		t.Fatal(err)
	}
	path, _ := s.HostPath(fr)
	b, _ := os.ReadFile(path)
	b[0] ^= 0x20
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := s.Open(bg, fr.Ref)
	if err == nil {
		_, err = io.ReadAll(r)
		r.Close()
	}
	if !herr.Has(err, blob.ReasonCorrupt) || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("read of a flipped byte = %v; want attachment/corrupt digest mismatch", err)
	}
}

func TestFileOverLimitRefused(t *testing.T) {
	s := newStore(t, attachments.Config{MaxFileBytes: 4})
	if _, err := s.SaveFile(bg, "big", strings.NewReader("12345")); !herr.Has(err, blob.ReasonTooLarge) {
		t.Fatalf("SaveFile over the limit = %v", err)
	}
}

func TestHostPathAndLookupEdges(t *testing.T) {
	s := newStore(t, attachments.Config{})
	fr, err := s.SaveFile(bg, "a.txt", strings.NewReader("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.HostPath(attachments.FileRef{}); ok {
		t.Fatal("a zero ref has a host path")
	}
	object, ok := s.HostPath(attachments.FileRef{Ref: fr.Ref, Name: "../other"})
	if !ok || filepath.Base(object) != fr.Digest {
		t.Fatalf("an unsafe name gave %q %v, want the object", object, ok)
	}
	if ref, ok := s.LookupPath(object); !ok || ref != fr.Ref {
		t.Fatalf("LookupPath(object) = %+v %v", ref, ok)
	}
	named, _ := s.HostPath(fr)
	dir := filepath.Dir(named)
	stray := filepath.Join(dir, "stray.txt")
	if err := os.WriteFile(stray, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{stray, filepath.Join(t.TempDir(), "x"), filepath.Join(filepath.Dir(dir), "x"), filepath.Join(dir, "..", "..", "zz", fr.Digest, "a.txt")} {
		if _, ok := s.LookupPath(p); ok {
			t.Fatalf("LookupPath(%s) matched", p)
		}
	}
	// A different file planted under a stored name is never taken for the link.
	other, err := s.SaveFile(bg, "b.txt", strings.NewReader("other"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.HostPath(other)
	if err := os.WriteFile(filepath.Join(filepath.Dir(b), "planted.txt"), []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveFile(bg, "planted.txt", strings.NewReader("other")); !herr.Has(err, blob.ReasonCorrupt) {
		t.Fatalf("SaveFile over a planted name = %v", err)
	}
}
