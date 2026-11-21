package workspace_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/kv"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/workspace"
)

func service(t *testing.T, st *sqlite.Store) session.Service {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	return session.NewService(root, st, session.Config{})
}

func layoutOf(t *testing.T, st *sqlite.Store) (version int64, tables map[string]bool) {
	t.Helper()
	tables = map[string]bool{}
	if err := st.ReadTx(bg, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(bg, `PRAGMA user_version`).Scan(&version); err != nil {
			return err
		}
		rows, err := tx.QueryContext(bg, `SELECT name FROM sqlite_schema WHERE type = 'table'`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				return err
			}
			tables[n] = true
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return version, tables
}

// checkSession reads the session back through the core: every event, in order.
func checkSession(t *testing.T, st *sqlite.Store, id session.ID, want int) {
	t.Helper()
	r, release, err := service(t, st).OpenRead(bg, id)
	if err != nil {
		t.Fatalf("open the layout-1 session: %v", err)
	}
	defer release()
	evs, err := r.Read(bg, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	d, err := event.RequestContext.Draft(event.RequestContextPayload{Key: "k", Value: "v"})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range evs {
		if ev.Type() == d.Type() {
			n++
		}
	}
	if n != want {
		t.Fatalf("the session has %d context events of %d, want %d", n, len(evs), want)
	}
}

// A dsh.db written at layout 1 (sessions only) upgrades through migration 2:
// its sessions survive, the machine-state tables appear, the typed accessors
// work, and the result survives another reopen.
func TestLayoutOneUpgradesToMachineState(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "dsh.db")
	folder := filepath.Join(home, "project")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}

	// The layout-1 fixture: sessions written by this build, then every later
	// migration undone, so the file holds exactly layout 1's objects.
	current := sqlite.LayoutVersion
	old, err := sqlite.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := service(t, old).Create(bg, nil, session.CreateOptions{Scope: scope.Key{}})
	if err == nil {
		for i := 0; i < 7 && err == nil; i++ {
			var d event.Draft
			if d, err = event.RequestContext.Draft(event.RequestContextPayload{Key: "k", Value: "v"}); err == nil {
				_, err = h.Append(bg, d)
			}
		}
		if cerr := h.Close(bg); err == nil {
			err = cerr
		}
	}
	if err == nil {
		err = old.WriteTx(bg, func(tx *sql.Tx) error {
			for _, q := range []string{
				`DROP INDEX sessions_prompt`,
				`ALTER TABLE sessions DROP COLUMN last_prompt_at`,
				`DROP TABLE workspace_first_use`,
				`DROP TABLE session_flags`,
				`DROP TABLE workspace_sessions`,
				`DROP TABLE workspaces`,
				`DROP TABLE kv`,
				`DROP TABLE kv_ns`,
				`PRAGMA user_version = 1`,
			} {
				if _, err := tx.ExecContext(bg, q); err != nil {
					return fmt.Errorf("%s: %w", q, err)
				}
			}
			return nil
		})
	}
	if v, tables := layoutOf(t, old); v != 1 || tables["workspaces"] || tables["kv"] || tables["kv_ns"] || !tables["events"] {
		t.Errorf("fixture layout %d tables %v, want 1 with sessions only", v, tables)
	}
	if cerr := old.Close(bg); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	id := h.ID()

	// Upgrade.
	st := openDB(t, path)
	if v, tables := layoutOf(t, st); v != int64(current) || !tables["workspaces"] || !tables["kv"] || !tables["kv_ns"] {
		t.Fatalf("upgraded layout %d tables %v, want %d with machine state", v, tables, current)
	}
	checkSession(t, st, id, 7)
	reg, err := workspace.Open(bg, st)
	if err != nil {
		t.Fatal(err)
	}
	w, err := reg.Add(bg, folder)
	if err != nil {
		t.Fatal(err)
	}
	type note struct{ Text string }
	ns, err := kv.Open(bg, st, kv.Spec[note]{Namespace: "notes", Version: 1, Authority: kv.Authoritative})
	if err != nil {
		t.Fatal(err)
	}
	if err := ns.Update(bg, func(tx *kv.Txn[note]) error { return tx.Put("a", note{Text: "kept"}) }); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(bg); err != nil {
		t.Fatal(err)
	}

	// Reopen the upgraded database: no second migration, everything kept.
	st = openDB(t, path)
	if v, _ := layoutOf(t, st); v != int64(current) {
		t.Fatalf("reopened layout %d, want %d", v, current)
	}
	checkSession(t, st, id, 7)
	reg, err = workspace.Open(bg, st)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reg.Get(bg, w.ID); err != nil || got != w {
		t.Fatalf("workspace after reopen = %+v %v, want %+v", got, err, w)
	}
	ns, err = kv.Open(bg, st, kv.Spec[note]{Namespace: "notes", Version: 1, Authority: kv.Authoritative})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok, err := ns.Get("a"); err != nil || !ok || v.Text != "kept" {
		t.Fatalf("record after reopen = %+v %v %v", v, ok, err)
	}
}
