// Package file is the managed secret store (DH-D-HUMAN-7, DH-ADR-HUMAN-9): the one
// writable source of secret.Store, kept as one encrypted file under
// <home>/secrets/.
//
// Layout of the secrets directory:
//
//	store.json  the envelope: {"format":"dsh-secret-store/1","protection":"file",
//	            "generation":N,"nonce":<base64>,"sealed":<base64>}
//	store.lock  the cross-process lock every writer holds (fsx.Lock, waited for a
//	            bounded time)
//	store.key   the 32-byte data key, owner-only (protection "file"; the keychain
//	            backend of DH-D-HUMAN-8 replaces it)
//
// The sealed part is AES-256-GCM over {"entries":[{"ref","value","revision"}]},
// with the envelope's format, protection and generation as additional data, so a
// tampered header fails like a wrong key. The generation counts the writes; an
// entry's revision is the generation that last wrote it, so a caller that read
// revision R and writes with ifRevision R is refused if anybody wrote the entry
// since. A write reads, changes and replaces the whole file under the lock with
// fsx.Replace (stage, sync, rename, directory sync), so two processes setting
// different references both keep their write, and a process killed before the
// rename leaves the old file whole.
//
// Errors name the store file and a byte offset or an entry number, never content:
// a malformed file may hold a value. A store whose key is missing or does not
// open it reads as absent with reason "key-lost", so the environment fallbacks
// still resolve, and writes are refused until the store is reset.
package file

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// File names under the secrets directory.
const (
	StoreFileName = "store.json"
	LockFileName  = "store.lock"
	KeyFileName   = "store.key"
)

// Origin is the store's origin in descriptions and leases.
const Origin = "store"

// DefaultLockWait bounds the wait for another writer's lock.
const DefaultLockWait = 10 * time.Second

// maxStoreFile bounds a store read.
const maxStoreFile = 4 << 20

// Options configures a store.
type Options struct {
	// Keys holds the data key; nil keeps it in KeyFile(dir).
	Keys Keys
	// LockWait bounds the wait for the lock; zero is DefaultLockWait.
	LockWait time.Duration
}

// Store is the managed store over one secrets directory.
type Store struct {
	dir      string
	keys     Keys
	lockWait time.Duration
}

var _ secret.Writer = (*Store)(nil)

// New returns the store kept in dir (home.Layout.Secrets). Nothing is read or
// created until the first call.
func New(dir string, opt Options) *Store {
	s := &Store{dir: dir, keys: opt.Keys, lockWait: opt.LockWait}
	if s.keys == nil {
		s.keys = KeyFile(dir)
	}
	if s.lockWait <= 0 {
		s.lockWait = DefaultLockWait
	}
	return s
}

// Origin implements secret.Source.
func (*Store) Origin() string { return Origin }

// Protection names where the data key is kept.
func (s *Store) Protection() string { return s.keys.Protection() }

// state is the store as read: whether a file exists, whether its key opened it,
// and its contents.
type state struct {
	exists     bool
	keyLost    bool
	generation uint64
	entries    []entry
}

func (st state) find(r secret.Ref) (entry, bool) {
	i := slices.IndexFunc(st.entries, func(e entry) bool { return e.Ref == r.String() })
	if i < 0 {
		return entry{}, false
	}
	return st.entries[i], true
}

// Lookup implements secret.Source. A store with a lost key has no value.
func (s *Store) Lookup(ctx context.Context, r secret.Ref) (secret.Value, bool, error) {
	st, err := s.read(ctx)
	if err != nil || st.keyLost {
		return secret.Value{}, false, err
	}
	e, ok := st.find(r)
	if !ok {
		return secret.Value{}, false, nil
	}
	return secret.NewValue(e.Value), true, nil
}

// Stat implements secret.Writer.
func (s *Store) Stat(ctx context.Context, r secret.Ref) (secret.Stat, error) {
	st, err := s.read(ctx)
	if err != nil {
		return secret.Stat{}, err
	}
	out := secret.Stat{Revision: secret.RevisionAbsent, Protection: s.Protection()}
	if st.keyLost {
		out.Reason = "key-lost"
		return out, nil
	}
	if e, ok := st.find(r); ok {
		out.Present, out.Revision = true, revision(e.Revision)
	}
	return out, nil
}

// Stored implements secret.Writer: the stored references in order.
func (s *Store) Stored(ctx context.Context) ([]secret.Ref, error) {
	st, err := s.read(ctx)
	if err != nil || st.keyLost {
		return nil, err
	}
	refs := make([]secret.Ref, 0, len(st.entries))
	for _, e := range st.entries {
		refs = append(refs, secret.MustParse(e.Ref)) // checked by open
	}
	return refs, nil
}

// Revision is the store's generation: it changes on every write, so a watcher
// compares it to know whether to refresh leases.
func (s *Store) Revision(ctx context.Context) (string, error) {
	st, err := s.read(ctx)
	if err != nil {
		return "", err
	}
	return revision(st.generation), nil
}

// Set implements secret.Writer.
func (s *Store) Set(ctx context.Context, r secret.Ref, v secret.Value, ifRevision string) (string, error) {
	if r.IsZero() {
		return "", herr.New(secret.ReasonBadRef, "no secret reference given")
	}
	if v.IsZero() {
		return "", herr.Newf(secret.ReasonEmpty, "%s: an empty value is refused; unset the reference instead", r)
	}
	var rev string
	err := s.modify(ctx, func(st *state) (bool, error) {
		cur, ok := st.find(r)
		have := secret.RevisionAbsent
		if ok {
			have = revision(cur.Revision)
		}
		if ifRevision != secret.AnyRevision && ifRevision != have {
			return false, herr.Newf(secret.ReasonStale, "%s: the stored revision is %s, not %s; read it again and retry", r, have, ifRevision)
		}
		st.generation++
		st.entries = slices.DeleteFunc(st.entries, func(e entry) bool { return e.Ref == r.String() })
		st.entries = append(st.entries, entry{Ref: r.String(), Value: v.Reveal(), Revision: st.generation})
		rev = revision(st.generation)
		return true, nil
	})
	if err != nil {
		return "", err
	}
	return rev, nil
}

// Unset implements secret.Writer.
func (s *Store) Unset(ctx context.Context, r secret.Ref) error {
	if r.IsZero() {
		return herr.New(secret.ReasonBadRef, "no secret reference given")
	}
	return s.modify(ctx, func(st *state) (bool, error) {
		if _, ok := st.find(r); !ok {
			return false, nil
		}
		st.generation++
		st.entries = slices.DeleteFunc(st.entries, func(e entry) bool { return e.Ref == r.String() })
		return true, nil
	})
}

// afterStage runs once a write has staged the new file and before it is renamed
// over the old one. Tests replace it to kill a writer at that point.
var afterStage = func() {}

// modify runs change on the current state under the lock and, when it reports a
// change, replaces the file durably.
func (s *Store) modify(ctx context.Context, change func(*state) (bool, error)) error {
	if err := home.EnsurePrivateDir(s.dir); err != nil {
		return herr.Wrap(secret.ReasonSource, err, "the secrets directory is refused")
	}
	wait, cancel := context.WithTimeout(ctx, s.lockWait)
	defer cancel()
	lock, err := fsx.Lock(wait, filepath.Join(s.dir, LockFileName))
	if err != nil {
		if ctx.Err() == nil && wait.Err() != nil {
			return herr.Newf(secret.ReasonBusy, "another process held the secret store's lock for %s", s.lockWait)
		}
		return herr.Wrap(secret.ReasonBusy, err, "the secret store's lock cannot be taken")
	}
	defer lock.Unlock()
	// A writer killed before its rename left its staged file; the next writer
	// removes those older than a day, as fsx asks of a stager.
	_, _ = fsx.SweepStaged(s.dir, 24*time.Hour)

	st, err := s.read(ctx)
	if err != nil {
		return err
	}
	if st.keyLost {
		return herr.New(secret.ReasonKeyLost, "the secret store's key is lost, so its values cannot be read or kept; reset the store to start again")
	}
	key, err := s.keys.Create(ctx)
	if err != nil {
		return err
	}
	changed, err := change(&st)
	if err != nil || !changed {
		return err
	}
	slices.SortFunc(st.entries, func(a, b entry) int {
		switch {
		case a.Ref < b.Ref:
			return -1
		case a.Ref > b.Ref:
			return 1
		}
		return 0
	})
	data, err := seal(key, s.Protection(), st.generation, st.entries)
	if err != nil {
		return err
	}
	err = fsx.Replace(filepath.Join(s.dir, StoreFileName), func(w io.Writer) error {
		if _, err := w.Write(data); err != nil {
			return err
		}
		afterStage()
		return nil
	}, fsx.Options{Perm: 0o600, Durable: true})
	if err != nil {
		return herr.Wrap(secret.ReasonSource, err, "the secret store cannot be written")
	}
	return nil
}

// read reads the store file. A missing directory or file is an empty store.
func (s *Store) read(ctx context.Context) (state, error) {
	if _, err := os.Lstat(s.dir); errors.Is(err, fs.ErrNotExist) {
		return state{}, nil
	}
	if err := home.CheckPrivate(s.dir); err != nil {
		return state{}, herr.Wrap(secret.ReasonSource, err, "the secrets directory is refused")
	}
	path := filepath.Join(s.dir, StoreFileName)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return state{}, nil
	}
	if err != nil {
		return state{}, herr.Wrap(secret.ReasonSource, err, StoreFileName+" cannot be read")
	}
	if !info.Mode().IsRegular() {
		return state{}, herr.New(secret.ReasonSource, StoreFileName+" is refused: it is not a regular file")
	}
	f, err := fsx.Open(path)
	if err != nil {
		return state{}, herr.Wrap(secret.ReasonSource, err, StoreFileName+" cannot be read")
	}
	defer f.Close()
	if err := home.CheckPrivateFile(f); err != nil {
		return state{}, herr.Wrap(secret.ReasonSource, err, StoreFileName+" is refused")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxStoreFile+1))
	if err != nil {
		return state{}, herr.Wrap(secret.ReasonSource, err, StoreFileName+" cannot be read")
	}
	if len(data) > maxStoreFile {
		return state{}, herr.Newf(secret.ReasonCorrupt, "%s is larger than %d bytes", StoreFileName, maxStoreFile)
	}
	env, err := parseEnvelope(data)
	if err != nil {
		return state{}, err
	}
	key, ok, err := s.keys.Load(ctx)
	if err != nil {
		return state{}, err
	}
	if !ok {
		return state{exists: true, keyLost: true, generation: env.Generation}, nil
	}
	entries, opened, err := open(key, env)
	if err != nil {
		return state{}, err
	}
	if !opened {
		return state{exists: true, keyLost: true, generation: env.Generation}, nil
	}
	return state{exists: true, generation: env.Generation, entries: entries}, nil
}

func revision(n uint64) string { return strconv.FormatUint(n, 10) }
