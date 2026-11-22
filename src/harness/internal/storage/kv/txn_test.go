package kv_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/kv"
)

// Open refuses a malformed spec before touching the database.
func TestOpenRefusesAMalformedSpec(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "dsh.db"))
	conv := func(json.RawMessage) (pref, error) { return pref{}, nil }
	for name, c := range map[string]struct {
		db   kv.DB
		spec kv.Spec[pref]
	}{
		"no database":      {nil, spec()},
		"no name":          {db, kv.Spec[pref]{Version: 1, Authority: kv.Derived}},
		"version 0":        {db, kv.Spec[pref]{Namespace: "x", Authority: kv.Derived}},
		"no authority":     {db, kv.Spec[pref]{Namespace: "x", Version: 1}},
		"converter at cur": {db, kv.Spec[pref]{Namespace: "x", Version: 2, Authority: kv.Derived, Convert: map[int]kv.Converter[pref]{2: conv}}},
		"converter at 0":   {db, kv.Spec[pref]{Namespace: "x", Version: 2, Authority: kv.Derived, Convert: map[int]kv.Converter[pref]{0: conv}}},
	} {
		if _, err := kv.Open(bg, c.db, c.spec); !errors.Is(err, kv.ErrInvalid) {
			t.Errorf("%s: Open = %v, want storage/invalid", name, err)
		}
	}
	var n int
	if err := db.ReadTx(bg, func(tx *sql.Tx) error {
		return tx.QueryRowContext(bg, `SELECT count(*) FROM kv_ns WHERE namespace = 'x'`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("a refused spec registered its namespace: %d rows, %v", n, err)
	}
}

// The revision helpers refuse a malformed or unknown namespace, and Classify
// names only the store's busy code storage/busy.
func TestRevisionHelpersAndClassify(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "dsh.db"))
	if err := db.WriteTx(bg, func(tx *sql.Tx) error { return kv.EnsureNamespace(bg, tx, "", kv.Derived) }); !errors.Is(err, kv.ErrInvalid) {
		t.Fatalf("EnsureNamespace with no name = %v", err)
	}
	if err := db.WriteTx(bg, func(tx *sql.Tx) error { return kv.EnsureNamespace(bg, tx, "x", "other") }); !errors.Is(err, kv.ErrInvalid) {
		t.Fatalf("EnsureNamespace with an unknown authority = %v", err)
	}
	if err := db.ReadTx(bg, func(tx *sql.Tx) error {
		_, err := kv.Revision(bg, tx, "missing")
		return err
	}); !errors.Is(err, kv.ErrInvalid) {
		t.Fatalf("Revision of an unregistered namespace = %v", err)
	}
	var rev int64
	if err := db.WriteTx(bg, func(tx *sql.Tx) error {
		if err := kv.EnsureNamespace(bg, tx, "x", kv.Derived); err != nil {
			return err
		}
		var err error
		rev, err = kv.BumpRevision(bg, tx, "x")
		return err
	}); err != nil || rev != 1 {
		t.Fatalf("BumpRevision = %d, %v; want 1", rev, err)
	}

	busy := &store.Error{Code: sqlite.CodeBusy, Msg: "locked"}
	if err := kv.Classify(busy); !errors.Is(err, kv.ErrBusy) || !errors.Is(err, busy) {
		t.Fatalf("Classify(busy) = %v, want storage/busy wrapping the store error", err)
	}
	other := &store.Error{Code: store.CodeClosed, Msg: "closed"}
	if err := kv.Classify(other); err != other {
		t.Fatalf("Classify(closed) = %v, want it unchanged", err)
	}
}

// A transaction sees its own staged writes and deletes over the stored records;
// deleting a missing key is no change; a delete commits as an OpDelete Change,
// and an Update whose writes change nothing raises none. A cancelled listener
// hears nothing more.
func TestTxnViewAndDeletes(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "dsh.db"))
	ns, err := kv.Open(bg, db, spec())
	if err != nil {
		t.Fatal(err)
	}
	if ns.Name() != "prefs" {
		t.Fatalf("Name = %q", ns.Name())
	}
	var rec recorder
	cancel := ns.Subscribe(rec.add)
	if err := ns.Update(bg, func(tx *kv.Txn[pref]) error {
		for _, k := range []string{"a", "b", "c"} {
			if err := tx.Put(k, pref{Theme: k}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec.take()
	if err := ns.Update(bg, func(tx *kv.Txn[pref]) error {
		if err := tx.Delete("b"); err != nil {
			return err
		}
		if err := tx.Delete("missing"); err != nil {
			return err
		}
		if _, ok, err := tx.Get("b"); ok || err != nil {
			t.Errorf("Get of a staged delete = %v, %v", ok, err)
		}
		if err := tx.Put("d", pref{Size: 4}); err != nil {
			return err
		}
		if err := tx.Put("a", pref{Theme: "A"}); err != nil {
			return err
		}
		if v, ok, err := tx.Get("a"); !ok || err != nil || v.Theme != "A" {
			t.Errorf("Get of a staged put = %+v %v %v", v, ok, err)
		}
		if v, ok, err := tx.Get("c"); !ok || err != nil || v.Theme != "c" {
			t.Errorf("Get of a stored record = %+v %v %v", v, ok, err)
		}
		if _, ok, err := tx.Get("missing"); ok || err != nil {
			t.Errorf("Get of a missing key = %v, %v", ok, err)
		}
		if got := tx.Keys(); !slices.Equal(got, []string{"a", "c", "d"}) {
			t.Errorf("Keys in the transaction = %v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := rec.take()
	want := []kv.Change{
		{Namespace: "prefs", Key: "a", Op: kv.OpPut, Revision: 2, Origin: kv.Local},
		{Namespace: "prefs", Key: "b", Op: kv.OpDelete, Revision: 2, Origin: kv.Local},
		{Namespace: "prefs", Key: "d", Op: kv.OpPut, Revision: 2, Origin: kv.Local},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("changes = %+v, want %+v", got, want)
	}
	if got := ns.Keys(); !slices.Equal(got, []string{"a", "c", "d"}) {
		t.Fatalf("Keys after the commit = %v", got)
	}

	// Writing the stored value again bumps the revision but changes no key.
	if err := ns.Update(bg, func(tx *kv.Txn[pref]) error { return tx.Put("c", pref{Theme: "c"}) }); err != nil {
		t.Fatal(err)
	}
	if got := rec.take(); len(got) != 0 || ns.Revision() != 3 {
		t.Fatalf("an unchanged put raised %+v at revision %d", got, ns.Revision())
	}

	cancel()
	if err := ns.Update(bg, func(tx *kv.Txn[pref]) error { return tx.Delete("c") }); err != nil {
		t.Fatal(err)
	}
	if got := rec.take(); len(got) != 0 {
		t.Fatalf("a cancelled listener heard %+v", got)
	}
}

// A foreign delete is raised as a Foreign OpDelete by Fresh, and Fresh with
// nothing new raises nothing.
func TestFreshRaisesAForeignDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.db")
	a, err := kv.Open(bg, openDB(t, path), spec())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Update(bg, func(tx *kv.Txn[pref]) error { return tx.Put("k", pref{Size: 1}) }); err != nil {
		t.Fatal(err)
	}
	b, err := kv.Open(bg, openDB(t, path), spec())
	if err != nil {
		t.Fatal(err)
	}
	var rec recorder
	b.Subscribe(rec.add)
	if err := b.Fresh(bg); err != nil || len(rec.take()) != 0 {
		t.Fatalf("Fresh with nothing new: %v", err)
	}
	if err := a.Update(bg, func(tx *kv.Txn[pref]) error { return tx.Delete("k") }); err != nil {
		t.Fatal(err)
	}
	if err := b.Fresh(bg); err != nil {
		t.Fatal(err)
	}
	if got := rec.take(); len(got) != 1 || got[0].Op != kv.OpDelete || got[0].Origin != kv.Foreign || got[0].Key != "k" {
		t.Fatalf("foreign delete = %+v", got)
	}
	if _, ok, _ := b.Get("k"); ok {
		t.Fatal("b still holds the deleted key")
	}
}

type nan struct{ F float64 }

// A record a newer build wrote is refused by Put and Delete unless the
// transaction allows the downgrade; an empty key and an unencodable value are
// storage/invalid; a failing converter makes the read storage/undecodable.
func TestPutAndDeleteGuards(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "dsh.db"))
	conv := kv.Spec[pref]{Namespace: "conv", Version: 2, Authority: kv.Authoritative,
		Convert: map[int]kv.Converter[pref]{1: func(json.RawMessage) (pref, error) { return pref{}, errors.New("cannot convert") }}}
	if _, err := kv.Open(bg, db, conv); err != nil {
		t.Fatal(err)
	}
	rawPut(t, db, "conv", "older", 1, `{}`)
	if _, err := kv.Open(bg, db, conv); !errors.Is(err, kv.ErrUndecodable) {
		t.Fatalf("a failing converter = %v, want storage/undecodable", err)
	}

	if _, err := kv.Open(bg, db, spec()); err != nil {
		t.Fatal(err)
	}
	rawPut(t, db, "prefs", "newer", 3, `{"future":1}`)
	ns, err := kv.Open(bg, db, spec())
	if err != nil {
		t.Fatal(err)
	}
	for name, fn := range map[string]func(tx *kv.Txn[pref]) error{
		"put":    func(tx *kv.Txn[pref]) error { return tx.Put("newer", pref{}) },
		"delete": func(tx *kv.Txn[pref]) error { return tx.Delete("newer") },
	} {
		if err := ns.Update(bg, fn); !errors.Is(err, kv.ErrRecordVersion) {
			t.Errorf("%s over a newer record = %v, want storage/record-version", name, err)
		}
	}
	if err := ns.Update(bg, func(tx *kv.Txn[pref]) error { return tx.Put("", pref{}) }); !errors.Is(err, kv.ErrInvalid) {
		t.Fatalf("Put with an empty key = %v", err)
	}
	if err := ns.Update(bg, func(tx *kv.Txn[pref]) error {
		tx.AllowDowngrade()
		return tx.Put("newer", pref{Theme: "mine"})
	}); err != nil {
		t.Fatalf("an allowed downgrade: %v", err)
	}
	if v, ok, err := ns.Get("newer"); err != nil || !ok || v.Theme != "mine" {
		t.Fatalf("after the downgrade = %+v %v %v", v, ok, err)
	}

	odd, err := kv.Open(bg, db, kv.Spec[nan]{Namespace: "odd", Version: 1, Authority: kv.Derived})
	if err != nil {
		t.Fatal(err)
	}
	if err := odd.Update(bg, func(tx *kv.Txn[nan]) error { return tx.Put("k", nan{F: math.NaN()}) }); !errors.Is(err, kv.ErrInvalid) {
		t.Fatalf("Put of an unencodable value = %v, want storage/invalid", err)
	}
	if len(odd.Keys()) != 0 || odd.Revision() != 0 {
		t.Fatalf("a refused Put changed the namespace: %v at %d", odd.Keys(), odd.Revision())
	}
}

// An Update that meets an undecodable record another instance wrote into an
// authoritative namespace fails and changes nothing.
func TestUpdateRefusesAForeignUndecodableRecord(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "dsh.db"))
	ns, err := kv.Open(bg, db, spec())
	if err != nil {
		t.Fatal(err)
	}
	rawPut(t, db, "prefs", "bad", 2, `not json`)
	if err := db.WriteTx(bg, func(tx *sql.Tx) error { _, err := kv.BumpRevision(bg, tx, "prefs"); return err }); err != nil {
		t.Fatal(err)
	}
	if err := ns.Update(bg, func(tx *kv.Txn[pref]) error { return tx.Put("k", pref{}) }); !errors.Is(err, kv.ErrUndecodable) {
		t.Fatalf("Update over a foreign undecodable record = %v", err)
	}
	if len(ns.Keys()) != 0 || ns.Revision() != 0 {
		t.Fatalf("the failed Update changed memory: %v at %d", ns.Keys(), ns.Revision())
	}
}

// lostCommit runs each write transaction and then rolls it back, reporting an
// error, as a commit whose outcome is unknown would look to the caller.
type lostCommit struct {
	*sqlite.Store
	lose bool
}

var errLost = errors.New("the commit's outcome is unknown")

func (l *lostCommit) WriteTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if !l.lose {
		return l.Store.WriteTx(ctx, fn)
	}
	err := l.Store.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		return errLost
	})
	return err
}

// failingRead fails every read transaction while fail is set, as a database
// locked past the busy bound would.
type failingRead struct {
	*sqlite.Store
	fail bool
}

func (f *failingRead) ReadTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if f.fail {
		return &store.Error{Code: sqlite.CodeBusy, Msg: "locked"}
	}
	return f.Store.ReadTx(ctx, fn)
}

// A write whose outcome is unknown leaves memory as it was and raises nothing,
// and the next Fresh reloads even though the stored revision did not move. A
// busy read fails Fresh with storage/busy.
func TestUnknownCommitOutcomeReloads(t *testing.T) {
	db := &lostCommit{Store: openDB(t, filepath.Join(t.TempDir(), "dsh.db"))}
	ns, err := kv.Open(bg, db, spec())
	if err != nil {
		t.Fatal(err)
	}
	var rec recorder
	ns.Subscribe(rec.add)
	db.lose = true
	if err := ns.Update(bg, func(tx *kv.Txn[pref]) error { return tx.Put("k", pref{Size: 1}) }); !errors.Is(err, errLost) {
		t.Fatalf("Update = %v, want the lost commit", err)
	}
	if _, ok, _ := ns.Get("k"); ok || len(rec.take()) != 0 {
		t.Fatal("a lost commit changed memory or raised a change")
	}
	db.lose = false
	rawPut(t, db, "prefs", "seen", 2, `{"size":7}`) // stored, revision unchanged
	if err := ns.Fresh(bg); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := ns.Get("seen"); err != nil || !ok || v.Size != 7 {
		t.Fatalf("Fresh after a lost commit did not reload: %+v %v %v", v, ok, err)
	}
	if got := rec.take(); len(got) != 1 || got[0].Key != "seen" || got[0].Origin != kv.Foreign {
		t.Fatalf("reload changes = %+v", got)
	}

	fr := &failingRead{Store: db.Store}
	fr.fail = true
	if _, err := kv.Open(bg, fr, spec()); !errors.Is(err, kv.ErrBusy) {
		t.Fatalf("Open over a busy read = %v, want storage/busy", err)
	}
	fr.fail = false
	opened, err := kv.Open(bg, fr, spec())
	if err != nil {
		t.Fatal(err)
	}
	fr.fail = true
	if err := opened.Fresh(bg); !errors.Is(err, kv.ErrBusy) {
		t.Fatalf("Fresh over a busy read = %v, want storage/busy", err)
	}
	if v, ok, _ := opened.Get("seen"); !ok || v.Size != 7 {
		t.Fatal("a failed Fresh dropped the snapshot")
	}
}

// Run refreshes on its timer, so a foreign write reaches a listener without a
// call to Fresh, and returns the context's error when it ends. On virtual time.
func TestRunRefreshesOnItsTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "dsh.db")
		a, err := kv.Open(bg, openDB(t, path), spec())
		if err != nil {
			t.Fatal(err)
		}
		b, err := kv.Open(bg, openDB(t, path), spec())
		if err != nil {
			t.Fatal(err)
		}
		var rec recorder
		b.Subscribe(rec.add)
		ctx, cancel := context.WithCancel(bg)
		ran, done := make(chan error, 1), make(chan struct{})
		// P-C: the cleanup cancels Run, then joins it.
		t.Cleanup(func() { cancel(); <-done })
		go func() {
			defer close(done)
			ran <- b.Run(ctx, 0) // the default interval, one second
		}()
		if err := a.Update(bg, func(tx *kv.Txn[pref]) error { return tx.Put("k", pref{Size: 1}) }); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := rec.take(); len(got) != 0 {
			t.Fatalf("Run refreshed before its interval: %+v", got)
		}
		<-time.After(1500 * time.Millisecond)
		synctest.Wait()
		if got := rec.take(); len(got) != 1 || got[0].Origin != kv.Foreign || got[0].Key != "k" {
			t.Fatalf("after one interval = %+v", got)
		}
		cancel()
		if err := <-ran; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
	})
}
