package kv_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/kv"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

var bg = context.Background()

type pref struct {
	Theme string `json:"theme"`
	Size  int    `json:"size"`
}

func openDB(t *testing.T, path string) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(bg, path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = s.Close(bg) })
	return s
}

func spec() kv.Spec[pref] {
	return kv.Spec[pref]{Namespace: "prefs", Version: 2, Authority: kv.Authoritative}
}

type recorder struct {
	mu sync.Mutex
	cs []kv.Change
}

func (r *recorder) add(c kv.Change) { r.mu.Lock(); r.cs = append(r.cs, c); r.mu.Unlock() }
func (r *recorder) take() []kv.Change {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.cs
	r.cs = nil
	return out
}

func TestReasonsAreWellFormed(t *testing.T) {
	for _, r := range []herr.Reason{kv.ReasonBusy, kv.ReasonRecordVersion, kv.ReasonUndecodable, kv.ReasonAuthority, kv.ReasonInvalid} {
		if !r.Valid() || r.Domain() != "storage" {
			t.Errorf("%q is not a storage reason", r)
		}
	}
}

// A committed write survives a restart; a failed callback leaves memory and the
// database unchanged and raises nothing.
//
//dsh:test DH-T-STORAGE-1
func TestUpdateCommitsAtomicallyAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.db")
	db := openDB(t, path)
	ns, err := kv.Open(bg, db, spec())
	if err != nil {
		t.Fatal(err)
	}
	var rec recorder
	ns.Subscribe(rec.add)
	if err := ns.Update(bg, func(tx *kv.Txn[pref]) error {
		if err := tx.Put("a", pref{Theme: "dark", Size: 12}); err != nil {
			return err
		}
		return tx.Put("b", pref{Theme: "light"})
	}); err != nil {
		t.Fatal(err)
	}
	if got := rec.take(); len(got) != 2 || got[0].Key != "a" || got[1].Key != "b" || got[0].Origin != kv.Local || got[0].Revision != 1 {
		t.Fatalf("changes = %+v", got)
	}
	boom := errors.New("boom")
	if err := ns.Update(bg, func(tx *kv.Txn[pref]) error {
		tx.Delete("a")
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("failed update = %v, want boom", err)
	}
	if got := rec.take(); len(got) != 0 {
		t.Fatalf("a failed update raised %+v", got)
	}
	if _, ok, _ := ns.Get("a"); !ok {
		t.Fatal("a failed update changed memory")
	}
	if err := db.Close(bg); err != nil {
		t.Fatal(err)
	}

	ns2, err := kv.Open(bg, openDB(t, path), spec())
	if err != nil {
		t.Fatal(err)
	}
	if v, ok, err := ns2.Get("a"); err != nil || !ok || v != (pref{Theme: "dark", Size: 12}) {
		t.Fatalf("after restart a = %+v %v %v", v, ok, err)
	}
	if ns2.Revision() != 1 || len(ns2.Keys()) != 2 {
		t.Fatalf("after restart revision %d keys %v", ns2.Revision(), ns2.Keys())
	}
}

// Two instances over one file: Fresh sees the other's write as Foreign, and an
// Update over a stale snapshot reads the stored records, not its cache.
//
//dsh:test DH-T-STORAGE-2
func TestFreshSeesForeignWrites(t *testing.T) {
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
	if err := a.Update(bg, func(tx *kv.Txn[pref]) error { return tx.Put("k", pref{Size: 1}) }); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := b.Get("k"); ok {
		t.Fatal("b saw a's write before Fresh")
	}
	if err := b.Fresh(bg); err != nil {
		t.Fatal(err)
	}
	if got := rec.take(); len(got) != 1 || got[0].Origin != kv.Foreign || got[0].Op != kv.OpPut {
		t.Fatalf("fresh changes = %+v", got)
	}
	if err := a.Update(bg, func(tx *kv.Txn[pref]) error { return tx.Put("k", pref{Size: 2}) }); err != nil {
		t.Fatal(err)
	}
	if err := b.Update(bg, func(tx *kv.Txn[pref]) error {
		v, _, err := tx.Get("k")
		if err != nil {
			return err
		}
		v.Size++
		return tx.Put("k", v)
	}); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := b.Get("k"); v.Size != 3 {
		t.Fatalf("read-modify-write over a stale cache gave %d, want 3", v.Size)
	}
	if got := rec.take(); len(got) != 2 || got[0].Origin != kv.Foreign || got[1].Origin != kv.Local {
		t.Fatalf("changes = %+v", got)
	}
	if err := a.Fresh(bg); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := a.Get("k"); v.Size != 3 || a.Revision() != b.Revision() {
		t.Fatalf("a = %+v at %d, b at %d", v, a.Revision(), b.Revision())
	}
}

func rawPut(t *testing.T, db kv.DB, ns, key string, version int, value string) {
	t.Helper()
	if err := db.WriteTx(bg, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(bg, `INSERT INTO kv (namespace, key, version, value, updated_at) VALUES (?, ?, ?, ?, 0)
			ON CONFLICT (namespace, key) DO UPDATE SET version = excluded.version, value = excluded.value`, ns, key, version, value)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// An older record is converted on read; a newer one fails only its own read.
//
//dsh:test DH-T-STORAGE-3
func TestRecordVersions(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "dsh.db"))
	if _, err := kv.Open(bg, db, spec()); err != nil {
		t.Fatal(err)
	}
	rawPut(t, db, "prefs", "old", 1, `{"colour":"blue"}`)
	rawPut(t, db, "prefs", "new", 3, `{"whatever":true}`)
	rawPut(t, db, "prefs", "cur", 2, `{"theme":"x"}`)
	sp := spec()
	sp.Convert = map[int]kv.Converter[pref]{1: func(raw json.RawMessage) (pref, error) {
		var v1 struct{ Colour string }
		err := json.Unmarshal(raw, &v1)
		return pref{Theme: v1.Colour}, err
	}}
	ns, err := kv.Open(bg, db, sp)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok, err := ns.Get("old"); err != nil || !ok || v.Theme != "blue" {
		t.Fatalf("old = %+v %v %v", v, ok, err)
	}
	if _, _, err := ns.Get("new"); !errors.Is(err, kv.ErrRecordVersion) {
		t.Fatalf("new = %v, want storage/record-version", err)
	}
	if v, _, err := ns.Get("cur"); err != nil || v.Theme != "x" {
		t.Fatalf("cur = %+v %v", v, err)
	}
	// Without the converter, the old record is refused, not guessed at.
	ns2, err := kv.Open(bg, db, spec())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ns2.Get("old"); !errors.Is(err, kv.ErrRecordVersion) {
		t.Fatalf("old without converter = %v", err)
	}
}

// An undecodable record fails an authoritative namespace and is dropped from a
// derived one; nothing is deleted from the database.
//
//dsh:test DH-T-STORAGE-3
func TestUndecodableByAuthority(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "dsh.db"))
	if _, err := kv.Open(bg, db, spec()); err != nil {
		t.Fatal(err)
	}
	rawPut(t, db, "prefs", "bad", 2, `not json`)
	if _, err := kv.Open(bg, db, spec()); !errors.Is(err, kv.ErrUndecodable) {
		t.Fatalf("authoritative open = %v, want storage/undecodable", err)
	}

	dsp := kv.Spec[pref]{Namespace: "cache", Version: 1, Authority: kv.Derived}
	if _, err := kv.Open(bg, db, dsp); err != nil {
		t.Fatal(err)
	}
	rawPut(t, db, "cache", "bad", 1, `not json`)
	rawPut(t, db, "cache", "ok", 1, `{"size":4}`)
	var dropped []string
	dsp.OnDrop = func(key string, _ error) { dropped = append(dropped, key) }
	ns, err := kv.Open(bg, db, dsp)
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 1 || dropped[0] != "bad" || len(ns.Keys()) != 1 {
		t.Fatalf("dropped %v keys %v", dropped, ns.Keys())
	}
	var n int
	if err := db.ReadTx(bg, func(tx *sql.Tx) error {
		return tx.QueryRowContext(bg, `SELECT count(*) FROM kv WHERE namespace = 'cache'`).Scan(&n)
	}); err != nil || n != 2 {
		t.Fatalf("rows = %d %v; a dropped record must stay on disk", n, err)
	}

	if _, err := kv.Open(bg, db, kv.Spec[pref]{Namespace: "cache", Version: 1, Authority: kv.Authoritative}); !errors.Is(err, kv.ErrAuthority) {
		t.Fatalf("reopen with another authority = %v", err)
	}
}
