package file

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// DH-T-HUMAN-11: the store's generation counts its writes, so a watcher can tell
// a changed file; an Unset of an absent reference writes nothing.
//
//dsh:test DH-T-HUMAN-11
func TestRevisionCountsWrites(t *testing.T) {
	ctx := context.Background()
	s := New(tempSecrets(t), Options{})
	want := func(rev string) {
		t.Helper()
		if got, err := s.Revision(ctx); err != nil || got != rev {
			t.Fatalf("Revision = %q, %v; want %q", got, err, rev)
		}
	}
	want("0")
	if err := s.Unset(ctx, ref); err != nil {
		t.Fatal(err)
	}
	want("0")
	if _, err := s.Set(ctx, ref, secret.NewValue(fakeStored), ""); err != nil {
		t.Fatal(err)
	}
	want("1")
	if err := s.Unset(ctx, ref); err != nil {
		t.Fatal(err)
	}
	want("2")
	if err := s.Unset(ctx, ref); err != nil {
		t.Fatal(err)
	}
	want("2")
	if refs, err := s.Stored(ctx); err != nil || len(refs) != 0 {
		t.Fatalf("Stored after Unset = %v, %v", refs, err)
	}
}

// DH-T-HUMAN-11: the zero reference is refused by every write.
//
//dsh:test DH-T-HUMAN-11
func TestWritesRefuseTheZeroReference(t *testing.T) {
	ctx := context.Background()
	s := New(tempSecrets(t), Options{})
	if _, err := s.Set(ctx, secret.Ref{}, secret.NewValue(fakeStored), ""); !herr.Has(err, secret.ReasonBadRef) {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Unset(ctx, secret.Ref{}); !herr.Has(err, secret.ReasonBadRef) {
		t.Fatalf("Unset: %v", err)
	}
}

// DH-T-HUMAN-11: with the key lost, every read gives nothing and every write is
// refused, Unset included, so no write replaces values it could not read.
//
//dsh:test DH-T-HUMAN-11
func TestLostKeyRefusesEveryWrite(t *testing.T) {
	ctx := context.Background()
	dir := tempSecrets(t)
	s := New(dir, Options{})
	if _, err := s.Set(ctx, ref, secret.NewValue(fakeStored), ""); err != nil {
		t.Fatal(err)
	}
	// A key of the wrong length is no key.
	if err := os.WriteFile(filepath.Join(dir, KeyFileName), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if refs, err := s.Stored(ctx); err != nil || refs != nil {
		t.Fatalf("Stored with the key lost = %v, %v", refs, err)
	}
	if st, err := s.Stat(ctx, ref); err != nil || st.Present || st.Reason != "key-lost" || st.Revision != secret.RevisionAbsent {
		t.Fatalf("Stat with the key lost = %+v, %v", st, err)
	}
	if rev, err := s.Revision(ctx); err != nil || rev != "1" {
		t.Fatalf("Revision with the key lost = %q, %v; the header still reads", rev, err)
	}
	if err := s.Unset(ctx, ref); !herr.Has(err, secret.ReasonKeyLost) {
		t.Fatalf("Unset with the key lost: %v", err)
	}

	// With no store file, a key file that holds no key is refused at the first
	// write rather than replaced.
	if err := os.Remove(filepath.Join(dir, StoreFileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, ref, secret.NewValue(fakeStored), ""); !herr.Has(err, secret.ReasonKeyLost) {
		t.Fatalf("Set over a malformed key file: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, KeyFileName)); err != nil || string(b) != "short" {
		t.Fatalf("the malformed key file was replaced (%v)", err)
	}
}

// DH-T-HUMAN-11: a store file or key file that is not a regular file, an
// oversized store file, and a secrets directory whose parent is missing are
// refused by name, without reading through them.
//
//dsh:test DH-T-HUMAN-11
func TestRefusedFiles(t *testing.T) {
	ctx := context.Background()

	t.Run("store file is a directory", func(t *testing.T) {
		dir := tempSecrets(t)
		if err := os.Mkdir(filepath.Join(dir, StoreFileName), 0o700); err != nil {
			t.Fatal(err)
		}
		_, _, err := New(dir, Options{}).Lookup(ctx, ref)
		if !herr.Has(err, secret.ReasonSource) || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("Lookup: %v", err)
		}
	})
	t.Run("key file is a directory", func(t *testing.T) {
		dir := tempSecrets(t)
		if err := os.Mkdir(filepath.Join(dir, KeyFileName), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := New(dir, Options{}).Set(ctx, ref, secret.NewValue(fakeStored), "")
		if !herr.Has(err, secret.ReasonSource) || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("Set: %v", err)
		}
	})
	t.Run("oversized store file", func(t *testing.T) {
		dir := tempSecrets(t)
		big := make([]byte, maxStoreFile+1)
		if err := os.WriteFile(filepath.Join(dir, StoreFileName), big, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := New(dir, Options{}).Lookup(ctx, ref)
		if !herr.Has(err, secret.ReasonCorrupt) || !strings.Contains(err.Error(), "larger than") {
			t.Fatalf("Lookup: %v", err)
		}
	})
	t.Run("missing parent", func(t *testing.T) {
		s := New(filepath.Join(t.TempDir(), "gone", "secrets"), Options{})
		if _, ok, err := s.Lookup(ctx, ref); ok || err != nil {
			t.Fatalf("Lookup of a missing directory = %v, %v; want absent", ok, err)
		}
		if _, err := s.Set(ctx, ref, secret.NewValue(fakeStored), ""); !herr.Has(err, secret.ReasonSource) {
			t.Fatalf("Set: %v", err)
		}
	})
	t.Run("secrets directory not private", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("a mode bit does not widen a Windows access list")
		}
		dir := tempSecrets(t)
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		if _, _, err := New(dir, Options{}).Lookup(ctx, ref); !herr.Has(err, secret.ReasonSource) {
			t.Fatalf("Lookup: %v", err)
		}
	})
}

// fakeKeys is a key backend that fails or hands out a key that is not one.
type fakeKeys struct {
	key     []byte
	loadErr error
}

func (fakeKeys) Protection() string { return "fake" }

func (k fakeKeys) Load(context.Context) ([]byte, bool, error) {
	return k.key, k.key != nil, k.loadErr
}

func (k fakeKeys) Create(context.Context) ([]byte, error) {
	if k.loadErr != nil {
		return nil, k.loadErr
	}
	return k.key, nil
}

// DH-T-HUMAN-11: a key backend's failure is the store's failure, and a key of a
// size AES does not take is refused before anything is written.
//
//dsh:test DH-T-HUMAN-11
func TestKeyBackendFailures(t *testing.T) {
	ctx := context.Background()
	dir := tempSecrets(t)
	if _, err := New(dir, Options{}).Set(ctx, ref, secret.NewValue(fakeStored), ""); err != nil {
		t.Fatal(err)
	}
	boom := herr.New("keys/unavailable", "the key service is unavailable")
	failing := New(dir, Options{Keys: fakeKeys{loadErr: boom}})
	if _, _, err := failing.Lookup(ctx, ref); !errors.Is(err, boom) {
		t.Fatalf("Lookup with a failing backend: %v", err)
	}
	if _, err := failing.Stat(ctx, ref); !errors.Is(err, boom) {
		t.Fatalf("Stat with a failing backend: %v", err)
	}
	if _, err := failing.Stored(ctx); !errors.Is(err, boom) {
		t.Fatalf("Stored with a failing backend: %v", err)
	}

	// A seven-byte key opens nothing, so the existing file reads as key-lost.
	odd := New(dir, Options{Keys: fakeKeys{key: []byte("7 bytes")}})
	if st, err := odd.Stat(ctx, ref); err != nil || st.Reason != "key-lost" || st.Protection != "fake" {
		t.Fatalf("Stat with a malformed key = %+v, %v", st, err)
	}
	// With no file yet, the same key is refused at seal time.
	fresh := New(tempSecrets(t), Options{Keys: fakeKeys{key: []byte("7 bytes")}})
	if _, err := fresh.Set(ctx, ref, secret.NewValue(fakeStored), ""); !herr.Has(err, secret.ReasonKeyLost) {
		t.Fatalf("Set with a malformed key: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fresh.dir, StoreFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a store file was written with a malformed key (%v)", err)
	}
}

// DH-T-HUMAN-11: a caller that gives up while another writer holds the lock gets
// its own cancellation, not the bounded-wait report.
//
//dsh:test DH-T-HUMAN-11
func TestLockWaitEndsWithTheCaller(t *testing.T) {
	dir := tempSecrets(t)
	held, err := fsx.TryLock(filepath.Join(dir, LockFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Unlock() })
	ctx, cancel := context.WithCancelCause(context.Background())
	stop := errors.New("the caller stopped")
	cancel(stop)
	_, err = New(dir, Options{}).Set(ctx, ref, secret.NewValue(fakeStored), "")
	if !herr.Has(err, secret.ReasonBusy) || !errors.Is(err, stop) || strings.Contains(err.Error(), "held the secret store's lock for") {
		t.Fatalf("Set with a cancelled caller: %v", err)
	}
}
