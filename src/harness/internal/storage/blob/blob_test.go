package blob_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/blob"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

var bg = context.Background()

func newStore(t *testing.T) (*blob.Store, home.Layout) {
	t.Helper()
	l := home.New(t.TempDir())
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	s, err := blob.New(l)
	if err != nil {
		t.Fatal(err)
	}
	return s, l
}

func put(t *testing.T, s *blob.Store, data string) blob.Ref {
	t.Helper()
	ref, err := s.Put(bg, blob.KindFile, strings.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func read(s *blob.Store, ref blob.Ref) ([]byte, error) {
	r, err := s.Open(bg, ref)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// rewrite changes a stored (read-only) object in place.
func rewrite(t *testing.T, path string, f func([]byte) []byte) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, f(b), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPutAndReadBack(t *testing.T) {
	s, l := newStore(t)
	data := strings.Repeat("attachment bytes ", 1000)
	ref := put(t, s, data)
	sum := sha256.Sum256([]byte(data))
	if ref.Digest != hex.EncodeToString(sum[:]) || ref.Size != int64(len(data)) || ref.Kind != blob.KindFile {
		t.Fatalf("ref %+v", ref)
	}
	if ref.String() != "file:"+ref.Digest {
		t.Fatalf("String() = %q", ref.String())
	}
	path, _ := s.Path(ref)
	if want := filepath.Join(l.Blobs(), "v1", "file", ref.Digest[:2], ref.Digest); path != want {
		t.Fatalf("path %s, want %s", path, want)
	}
	got, err := read(s, ref)
	if err != nil || string(got) != data {
		t.Fatalf("read back %d bytes, err %v", len(got), err)
	}
	if ents, _ := os.ReadDir(filepath.Join(l.Blobs(), "tmp")); len(ents) != 0 {
		t.Fatalf("%d staging files left", len(ents))
	}
	if got, ok := s.Lookup(path); !ok || got != ref {
		t.Fatalf("Lookup = %+v %v", got, ok)
	}
	if _, ok := s.Lookup(filepath.Join(l.Root(), "elsewhere")); ok {
		t.Fatal("Lookup matched a path outside the store")
	}
}

func TestEqualBytesAreOneObject(t *testing.T) {
	s, _ := newStore(t)
	a, b := put(t, s, "same"), put(t, s, "same")
	if a != b {
		t.Fatalf("%+v != %+v", a, b)
	}
	if err := s.Touch(bg, a); err != nil {
		t.Fatal(err)
	}
}

// Scenario: Corrupted attachment detected (DH-D-STORAGE-3, TC-08): one byte
// flipped on disk fails the read with a digest mismatch.
func TestCorruptedObjectDetected(t *testing.T) {
	s, _ := newStore(t)
	ref := put(t, s, "the bytes a person attached")
	path, _ := s.Path(ref)
	rewrite(t, path, func(b []byte) []byte { b[3] ^= 0x01; return b })

	got, err := read(s, ref)
	if !herr.Has(err, blob.ReasonCorrupt) || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("read of a flipped byte = %q, %v; want attachment/corrupt digest mismatch", got, err)
	}
}

func TestTruncatedAndMissingObjects(t *testing.T) {
	s, _ := newStore(t)
	ref := put(t, s, "0123456789")
	path, _ := s.Path(ref)
	rewrite(t, path, func(b []byte) []byte { return b[:5] })
	if _, err := read(s, ref); !herr.Has(err, blob.ReasonCorrupt) {
		t.Fatalf("read of a truncated object = %v", err)
	}
	if err := s.Touch(bg, ref); !herr.Has(err, blob.ReasonCorrupt) {
		t.Fatalf("Touch of a truncated object = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := read(s, ref); !herr.Has(err, blob.ReasonNotFound) {
		t.Fatalf("read of a missing object = %v", err)
	}
	if err := s.Touch(bg, ref); !herr.Has(err, blob.ReasonNotFound) {
		t.Fatalf("Touch of a missing object = %v", err)
	}
	if _, err := s.Open(bg, blob.Ref{Kind: blob.KindFile, Digest: "../../x"}); !herr.Has(err, blob.ReasonNotFound) {
		t.Fatalf("Open of a malformed ref = %v", err)
	}
}

func TestTooLargeLeavesNoObject(t *testing.T) {
	s, l := newStore(t)
	data := bytes.Repeat([]byte("x"), 100)
	if _, err := s.Put(bg, blob.KindData, bytes.NewReader(data), 99); !herr.Has(err, blob.ReasonTooLarge) {
		t.Fatalf("Put over the limit = %v", err)
	}
	sum := sha256.Sum256(data)
	path, _ := s.Path(blob.Ref{Kind: blob.KindData, Digest: hex.EncodeToString(sum[:]), Size: 100})
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("an object was stored: %v", err)
	}
	if ents, _ := os.ReadDir(filepath.Join(l.Blobs(), "tmp")); len(ents) != 0 {
		t.Fatalf("%d staging files left", len(ents))
	}
	if _, err := s.Put(bg, blob.KindData, bytes.NewReader(data), 100); err != nil {
		t.Fatalf("Put at the limit = %v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("disk gone") }

func TestPutAndOpenErrors(t *testing.T) {
	s, l := newStore(t)
	if _, err := s.Put(bg, blob.Kind("video"), strings.NewReader("x"), 0); err == nil {
		t.Fatal("Put of an unknown kind succeeded")
	}
	if _, err := s.Put(bg, blob.KindData, failingReader{}, 0); !herr.Has(err, blob.ReasonReadFailed) {
		t.Fatalf("Put of a failing reader = %v", err)
	}
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := s.Put(ctx, blob.KindData, strings.NewReader("x"), 0); err == nil {
		t.Fatal("Put on a cancelled context succeeded")
	}
	if _, err := s.Open(ctx, blob.Ref{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open on a cancelled context = %v", err)
	}
	if err := s.Touch(ctx, blob.Ref{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Touch on a cancelled context = %v", err)
	}
	if err := s.Touch(bg, blob.Ref{Kind: blob.KindFile, Digest: "zz"}); !herr.Has(err, blob.ReasonNotFound) {
		t.Fatalf("Touch of a malformed ref = %v", err)
	}
	if ents, _ := os.ReadDir(filepath.Join(l.Blobs(), "tmp")); len(ents) != 0 {
		t.Fatalf("%d staging files left after failed saves", len(ents))
	}
	if _, err := blob.New(home.New(t.TempDir())); err == nil {
		t.Fatal("New without a blobs directory succeeded")
	}
}

func TestReadDetectsAGrownObjectAndANonFile(t *testing.T) {
	s, _ := newStore(t)
	ref := put(t, s, "0123456789")
	path, _ := s.Path(ref)
	r, err := s.Open(bg, ref)
	if err != nil {
		t.Fatal(err)
	}
	// The object grows after the length check: the stream fails once past it.
	rewrite(t, path, func(b []byte) []byte { return append(b, "more bytes"...) })
	if _, err := io.ReadAll(r); !herr.Has(err, blob.ReasonCorrupt) {
		t.Fatalf("read of a grown object = %v", err)
	}
	if _, err := r.Read(make([]byte, 1)); !herr.Has(err, blob.ReasonCorrupt) {
		t.Fatalf("a read after the failure = %v", err)
	}
	r.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(bg, ref); err == nil {
		t.Fatal("Open of a directory in place of an object succeeded")
	}
	if _, ok := s.Lookup(path); ok {
		t.Fatal("Lookup matched a directory")
	}
	if err := s.Touch(bg, ref); !herr.Has(err, blob.ReasonCorrupt) {
		t.Fatalf("Touch of a directory = %v", err)
	}
}

func TestLookupRefusesForeignShapes(t *testing.T) {
	s, l := newStore(t)
	ref := put(t, s, "x")
	for _, p := range []string{
		filepath.Join(l.Blobs(), "v1", "file", ref.Digest),
		filepath.Join(l.Blobs(), "v1", "file", "00", ref.Digest),
		filepath.Join(l.Blobs(), "v1", "video", ref.Digest[:2], ref.Digest),
		filepath.Join(l.Blobs(), "v1", "file", ref.Digest[:2], "notadigest"),
	} {
		if _, ok := s.Lookup(p); ok {
			t.Fatalf("Lookup(%s) matched", p)
		}
	}
	if _, ok := s.Path(blob.Ref{Kind: blob.KindFile, Digest: strings.Repeat("A", 64)}); ok {
		t.Fatal("an upper-case digest has a path")
	}
}

func TestADifferentObjectUnderTheNameIsNotAdopted(t *testing.T) {
	s, _ := newStore(t)
	ref := put(t, s, "0123456789")
	path, _ := s.Path(ref)
	rewrite(t, path, func(b []byte) []byte { return b[:3] })
	if _, err := s.Put(bg, blob.KindFile, strings.NewReader("0123456789"), 0); !herr.Has(err, blob.ReasonCorrupt) {
		t.Fatalf("Put over a damaged object of the same name = %v", err)
	}
}

// recordSyncs records every directory the package syncs, failing them with fail.
func recordSyncs(t *testing.T, fail error) *[]string {
	var dirs []string
	blob.SetSyncDir(t, func(d string) error {
		dirs = append(dirs, filepath.Clean(d))
		return fail
	})
	return &dirs
}

func TestPublishSyncsTheObjectDirectory(t *testing.T) {
	s, _ := newStore(t)
	dirs := recordSyncs(t, nil)
	ref := put(t, s, "durable")
	path, _ := s.Path(ref)
	if len(*dirs) != 1 || (*dirs)[0] != filepath.Dir(path) {
		t.Fatalf("synced %v, want the object's directory", *dirs)
	}
	put(t, s, "durable")
	if len(*dirs) != 2 || (*dirs)[1] != filepath.Dir(path) {
		t.Fatalf("adopting synced %v, want the object's directory again", *dirs)
	}
}

func TestPublishFailsWhenTheDirectorySyncFails(t *testing.T) {
	s, _ := newStore(t)
	existing := put(t, s, "already here")
	recordSyncs(t, errors.New("sync failed"))
	if _, err := s.Put(bg, blob.KindFile, strings.NewReader("new bytes"), 0); !herr.Has(err, home.ReasonIO) {
		t.Fatalf("Put of a new object with a failing sync = %v", err)
	}
	if _, err := s.Put(bg, blob.KindFile, strings.NewReader("already here"), 0); !herr.Has(err, home.ReasonIO) {
		t.Fatalf("Put of an existing object with a failing sync = %v", err)
	}
	if got, err := read(s, existing); err != nil || string(got) != "already here" {
		t.Fatalf("the existing object reads %q, %v", got, err)
	}
}

// A same-length object with a flipped byte is not adopted by a later Put of the
// original bytes, nor re-validated by Touch.
func TestSameSizeCorruptObjectNotAdopted(t *testing.T) {
	s, _ := newStore(t)
	ref := put(t, s, "original bytes")
	path, _ := s.Path(ref)
	rewrite(t, path, func(b []byte) []byte { b[3] ^= 0x01; return b })
	if _, err := s.Put(bg, blob.KindFile, strings.NewReader("original bytes"), 0); !herr.Has(err, blob.ReasonCorrupt) {
		t.Fatalf("Put over a same-size corrupt object = %v, want %s", err, blob.ReasonCorrupt)
	}
	if err := s.Touch(bg, ref); !herr.Has(err, blob.ReasonCorrupt) {
		t.Fatalf("Touch of a same-size corrupt object = %v, want %s", err, blob.ReasonCorrupt)
	}
	if _, err := read(s, ref); !herr.Has(err, blob.ReasonCorrupt) {
		t.Fatalf("read of the corrupt object = %v", err)
	}
}
