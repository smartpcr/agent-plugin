package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
)

// known is the view keys the binary of the prune tests registers.
var known = map[string]int{"list/summary": 2}

// pruneFixture opens a store on a fresh home whose session "a" holds four
// derived rows: an unknown key, an old version of a known key, the known key at
// its version, and a core key.
func pruneFixture(t *testing.T) (string, *sqlite.Store) {
	t.Helper()
	path := dbPath(t)
	s := open(t, path)
	w, err := s.Create(bg, store.Header{ID: "a"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(bg, store.Batch{First: 0, Records: []store.Record{call(t, 0)}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(bg); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(bg, func(tx *sql.Tx) error {
		for _, r := range []struct {
			key string
			v   int
		}{{"gone/view", 1}, {"list/summary", 1}, {"list/summary", 2}, {"core/surface", 1}} {
			if _, err := tx.ExecContext(bg, `INSERT INTO checkpoints (session, key, state_version, seq, state)
				VALUES ((SELECT uid FROM sessions WHERE id = 'a'), ?, ?, 0, '{}')`, r.key, r.v); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return path, s
}

// rowsLeft is every checkpoints row as "key@version", sorted.
func rowsLeft(t *testing.T, path string) []string {
	t.Helper()
	rs, err := fresh(t, path).Query(`SELECT key, state_version FROM checkpoints`)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var (
			k string
			v int
		)
		if err := rs.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out = append(out, k+"@"+strconv.Itoa(v))
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

var allRows = []string{"core/surface@1", "gone/view@1", "list/summary@1", "list/summary@2"}

// DH-T-VIEW-8 (prune clause), M1-G-STORE-16: with no other live instance the
// prune removes the unknown key and the old version only; the known key at its
// version and the core key stay.
//
//dsh:test DH-T-VIEW-8
func TestPruneRemovesOnlyUnknownAndOldRows(t *testing.T) {
	path, s := pruneFixture(t)
	// A live instance of the same build does not block it; nor does a dead one
	// of another build.
	same := open(t, path)
	if err := same.WriteTx(bg, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(bg, `INSERT INTO hosts (instance, pid, kind, started_at, binary_version) VALUES ('77-deadbeef0000', 77, 'gone', 1, 'older')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "run", "77-deadbeef0000.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if blocked, err := s.PruneBlockers(bg); err != nil || len(blocked) != 0 {
		t.Fatalf("PruneBlockers = %v, %v; want none", blocked, err)
	}
	ran, rep, err := s.RunPrune(bg, known, 0)
	if err != nil || !ran || rep.Deleted != 2 || len(rep.Blocked) != 0 {
		t.Fatalf("RunPrune = %v, %+v, %v; want ran, 2 deleted", ran, rep, err)
	}
	if got, want := rowsLeft(t, path), []string{"core/surface@1", "list/summary@2"}; !slices.Equal(got, want) {
		t.Fatalf("rows left %v, want %v", got, want)
	}
	// Nothing is left to prune.
	if rep, err := s.PruneDerived(bg, known); err != nil || rep.Deleted != 0 {
		t.Fatalf("second PruneDerived = %+v, %v", rep, err)
	}
	// A binary that registers no view keeps only the core's rows.
	if rep, err := s.PruneDerived(bg, nil); err != nil || rep.Deleted != 1 {
		t.Fatalf("PruneDerived(nil) = %+v, %v; want 1 deleted", rep, err)
	}
	if got := rowsLeft(t, path); !slices.Equal(got, []string{"core/surface@1"}) {
		t.Fatalf("rows left %v, want the core row only", got)
	}
}

// DH-T-VIEW-8 (prune clause), M1-G-STORE-16: while a live instance of another
// build shares the home, the prune deletes nothing and names it; once that
// instance is gone it runs.
//
//dsh:test DH-T-VIEW-8
func TestPruneWaitsForAnOlderLiveHost(t *testing.T) {
	path, s := pruneFixture(t)
	older, err := sqlite.Open(bg, path, sqlite.WithBinaryVersion("v0.0.1-older"), sqlite.WithKind("older"))
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := s.PruneBlockers(bg)
	if err != nil || len(blocked) != 1 || !strings.Contains(blocked[0], `kind older`) || !strings.Contains(blocked[0], `"v0.0.1-older"`) {
		t.Fatalf("PruneBlockers = %v, %v; want the older instance", blocked, err)
	}
	ran, rep, err := s.RunPrune(bg, known, 0)
	if err != nil || ran || rep.Deleted != 0 || len(rep.Blocked) != 1 {
		t.Fatalf("RunPrune = %v, %+v, %v; want not ran, blocked", ran, rep, err)
	}
	// The guard inside the transaction holds on its own.
	rep, err = s.PruneDerived(bg, known)
	if err != nil || rep.Deleted != 0 || len(rep.Blocked) != 1 {
		t.Fatalf("PruneDerived = %+v, %v; want blocked, nothing deleted", rep, err)
	}
	if got := rowsLeft(t, path); !slices.Equal(got, allRows) {
		t.Fatalf("rows left %v, want all %v", got, allRows)
	}
	// A hosts row without a lock file reads as alive, as for a lease.
	if err := older.Close(bg); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(bg, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(bg, `INSERT INTO hosts (instance, pid, kind, started_at) VALUES ('78-0123456789ab', 78, 'unlocked', 1)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if blocked, err := s.PruneBlockers(bg); err != nil || len(blocked) != 1 || !strings.Contains(blocked[0], `(unrecorded)`) {
		t.Fatalf("PruneBlockers = %v, %v; want the unlocked row", blocked, err)
	}
	if err := s.WriteTx(bg, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(bg, `DELETE FROM hosts WHERE instance = '78-0123456789ab'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ran, rep, err = s.RunPrune(bg, known, 0)
	if err != nil || !ran || rep.Deleted != 2 {
		t.Fatalf("RunPrune once the older instance closed = %v, %+v, %v", ran, rep, err)
	}
}

// The prune refuses a closed store and a cancelled context.
func TestPruneRefusals(t *testing.T) {
	path := dbPath(t)
	s, err := sqlite.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneDerived(bg, known); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("PruneDerived after Close = %v", err)
	}
	if _, _, err := s.RunPrune(bg, known, 0); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("RunPrune after Close = %v", err)
	}
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := s.PruneBlockers(ctx); err == nil {
		t.Fatal("PruneBlockers with a cancelled context succeeded")
	}
	if _, err := s.PruneDerived(ctx, known); err == nil {
		t.Fatal("PruneDerived with a cancelled context succeeded")
	}
}
