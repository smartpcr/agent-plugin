package sqlite_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/storetest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
)

var bg = context.Background()

// dbPath is the dsh.db of a fresh home: a test-kit temp home, or a fresh
// directory under DSH_E2E_STORE_HOME when the compose run mounts one.
func dbPath(t *testing.T) string {
	t.Helper()
	if base := os.Getenv("DSH_E2E_STORE_HOME"); base != "" {
		dir, err := os.MkdirTemp(base, "case-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		return filepath.Join(dir, "dsh.db")
	}
	return temphome.New(t).DB()
}

func open(t *testing.T, path string) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(bg, path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	t.Cleanup(func() {
		if err := s.Close(bg); err != nil {
			t.Errorf("Store.Close: %v", err)
		}
	})
	return s
}

// DH-T-STORE-1 / TC-07: the conformance suite passes on SQLite.
func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store { return open(t, dbPath(t)) })
}

var ms = time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

func call(t testing.TB, seq store.Seq) store.Record {
	t.Helper()
	d, err := event.ToolCall.Draft(event.ToolCallPayload{CallID: fmt.Sprintf("c%d", seq), Name: "grep", Arguments: `{"q":"x"}`})
	if err != nil {
		t.Fatal(err)
	}
	ms++
	r, err := d.Stamp(seq, ms)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// fresh is a new connection to path that shares nothing with the store.
func fresh(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func countEvents(t *testing.T, db *sql.DB, id string) (events, next int64) {
	t.Helper()
	if err := db.QueryRow(`SELECT count(*) FROM events JOIN sessions ON events.session = sessions.uid WHERE sessions.id = ?`, id).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT next_seq FROM sessions WHERE id = ?`, id).Scan(&next); err != nil {
		t.Fatal(err)
	}
	return events, next
}

// TC-07 / DH-D-STORE-2: an append of three events whose third violates a
// constraint of the database leaves none of the three.
func TestMultiEventAppendIsAtomic(t *testing.T) {
	path := dbPath(t)
	s := open(t, path)
	w, err := s.Create(bg, store.Header{ID: "s"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(bg, store.Batch{First: 0, Records: []store.Record{call(t, 0)}}); err != nil {
		t.Fatal(err)
	}
	// The third row repeats the first's seq: the primary key refuses it after
	// the session row and two events were already written in the transaction.
	bad := []store.Record{call(t, 1), call(t, 2), call(t, 1)}
	err = s.InsertUnchecked(bg, "s", bad)
	if !errors.Is(err, &store.Error{Code: sqlite.CodeConstraint}) {
		t.Fatalf("InsertUnchecked = %v, want %s", err, sqlite.CodeConstraint)
	}
	if events, next := countEvents(t, fresh(t, path), "s"); events != 1 || next != 1 {
		t.Fatalf("after the refused append a fresh connection reads %d events, next seq %d; want 1, 1", events, next)
	}
	// The same through the contract: a batch whose third record is refused.
	third := call(t, 3)
	third.Type = ""
	err = w.Append(bg, store.Batch{First: 1, Records: []store.Record{call(t, 1), call(t, 2), third}})
	if !errors.Is(err, store.ErrInvalidBatch) {
		t.Fatalf("Append = %v, want %v", err, store.ErrInvalidBatch)
	}
	if events, next := countEvents(t, fresh(t, path), "s"); events != 1 || next != 1 {
		t.Fatalf("after the refused batch a fresh connection reads %d events, next seq %d; want 1, 1", events, next)
	}
	if err := w.Append(bg, store.Batch{First: 1, Records: []store.Record{call(t, 1), call(t, 2), call(t, 3)}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(bg); err != nil {
		t.Fatal(err)
	}
	if events, next := countEvents(t, fresh(t, path), "s"); events != 4 || next != 4 {
		t.Fatalf("a fresh connection reads %d events, next seq %d; want 4, 4", events, next)
	}
}

// The log is immutable in the database itself: an UPDATE or DELETE of an event
// outside a session's deletion is aborted by a trigger.
func TestEventsImmutableInSchema(t *testing.T) {
	path := dbPath(t)
	s := open(t, path)
	w, err := s.Create(bg, store.Header{ID: "s"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(bg, store.Batch{First: 0, Records: []store.Record{call(t, 0)}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(bg); err != nil {
		t.Fatal(err)
	}
	db := fresh(t, path)
	for _, q := range []string{`UPDATE events SET data = '{}'`, `DELETE FROM events`} {
		_, err := db.Exec(q)
		if err == nil || !strings.Contains(err.Error(), "session/immutable") {
			t.Fatalf("%s = %v, want the immutability trigger", q, err)
		}
	}
	if events, _ := countEvents(t, db, "s"); events != 1 {
		t.Fatalf("%d events after the refused writes", events)
	}
}

// Records, checkpoints and the lease survive a close and reopen; the
// migrations are applied once.
func TestReopen(t *testing.T) {
	path := dbPath(t)
	s, err := sqlite.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.Create(bg, store.Header{ID: "s", Cwd: "/w"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	recs := []store.Record{call(t, 0), call(t, 1)}
	if err := w.Append(bg, store.Batch{First: 0, Records: recs,
		Checkpoints: []store.Checkpoint{{Key: "core/x", StateVersion: 1, Seq: 1, State: `{"a":1}`}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(bg); err != nil { // closes w
		t.Fatal(err)
	}
	if err := s.Close(bg); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := w.Append(bg, store.Batch{First: 2, Records: []store.Record{call(t, 2)}}); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("Append after Store.Close = %v", err)
	}
	s2 := open(t, path)
	sum, err := s2.Stat(bg, "s")
	if err != nil {
		t.Fatal(err)
	}
	if sum.NextSeq != 2 || sum.Durable != 2 || sum.Holder != nil || sum.LeaseEpoch != 1 || sum.Header.Cwd != "/w" {
		t.Fatalf("Stat after reopen = %+v", sum)
	}
	r, err := s2.OpenRead(bg, "s")
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Read(bg, 0, 0, store.ReadOptions{WithAux: true})
	if err != nil || len(got) != 2 || !got[0].Equal(recs[0]) || !got[1].Equal(recs[1]) {
		t.Fatalf("Read after reopen = %+v, %v", got, err)
	}
	cks, err := r.Checkpoints(bg)
	if err != nil || len(cks) != 1 || cks[0].State != `{"a":1}` {
		t.Fatalf("Checkpoints after reopen = %+v, %v", cks, err)
	}
	var version, app int64
	db := fresh(t, path)
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA application_id`).Scan(&app); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if version != int64(sqlite.LayoutVersion) || app != sqlite.ApplicationID || mode != "wal" {
		t.Fatalf("user_version %d, application_id %#x, journal_mode %q", version, app, mode)
	}
}

// DH-LIB-SQLITE-R12: a database of another application, of a newer layout, or
// a file that is not SQLite is refused with its own code and left unchanged.
func TestOpenRefusesUnchanged(t *testing.T) {
	newer := dbPath(t)
	mk := func(path string, stmts ...string) {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, q := range stmts {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk(newer, fmt.Sprintf(`PRAGMA application_id = %d`, sqlite.ApplicationID), `CREATE TABLE t (x)`,
		fmt.Sprintf(`PRAGMA user_version = %d`, sqlite.LayoutVersion+1))
	foreign := filepath.Join(t.TempDir(), "dsh.db")
	mk(foreign, `PRAGMA application_id = 7`, `CREATE TABLE t (x)`)
	unrelated := filepath.Join(t.TempDir(), "dsh.db")
	mk(unrelated, `CREATE TABLE t (x)`)
	text := filepath.Join(t.TempDir(), "dsh.db")
	if err := os.WriteFile(text, bytes.Repeat([]byte("not a database\n"), 400), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		want       store.Code
	}{
		{"newer layout", newer, sqlite.CodeNewerLayout},
		{"foreign application", foreign, sqlite.CodeForeign},
		{"unrelated schema", unrelated, sqlite.CodeForeign},
		{"not SQLite", text, sqlite.CodeForeign},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			s, err := sqlite.Open(bg, tc.path)
			if err == nil {
				s.Close(bg)
				t.Fatal("Open succeeded")
			}
			if !errors.Is(err, &store.Error{Code: tc.want}) {
				t.Fatalf("Open = %v, want %s", err, tc.want)
			}
			after, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("the refused database was changed")
			}
			for _, side := range []string{"-wal", "-shm"} {
				if fi, err := os.Stat(tc.path + side); err == nil && fi.Size() > 0 {
					t.Fatalf("the refused open left %s of %d bytes", side, fi.Size())
				}
			}
		})
	}
}

const childEnv = "DSH_SQLITE_FLUSH_CHILD"

// TestFlushChild is the re-executed child of TestFlushSurvivesKill: it appends,
// flushes, reports, and waits to be killed.
func TestFlushChild(t *testing.T) {
	path := os.Getenv(childEnv)
	if path == "" {
		t.Skip("the child of TestFlushSurvivesKill")
	}
	s, err := sqlite.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.Create(bg, store.Header{ID: "s"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for seq := store.Seq(0); seq < 5; seq++ {
		if err := w.Append(bg, store.Batch{First: seq, Records: []store.Record{call(t, seq)}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(bg); err != nil {
		t.Fatal(err)
	}
	fmt.Println("flushed")
	io.Copy(io.Discard, os.Stdin) // until the parent kills this process
	t.Fatal("the parent did not kill the child")
}

// TC-07 / DH-D-STORE-2: once Flush returns, the rows survive the death of the
// process that wrote them, killed before it closed anything.
func TestFlushSurvivesKill(t *testing.T) {
	path := dbPath(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestFlushChild$", "-test.count=1", "-test.v")
	cmd.Env = append(childEnviron(), childEnv+"="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			cmd.Process.Kill() //nolint:errcheck // may be dead already
			cmd.Wait()         //nolint:errcheck // killed
		}
	})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	flushed := false
	var log []string
	for sc := bufio.NewScanner(out); sc.Scan(); {
		log = append(log, sc.Text())
		if sc.Text() == "flushed" {
			flushed = true
			break
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait() //nolint:errcheck // killed
	cmd.Process = nil
	if !flushed {
		t.Fatalf("the child never flushed:\n%s", strings.Join(log, "\n"))
	}
	s := open(t, path)
	r, err := s.OpenRead(bg, "s")
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Read(bg, 0, 0, store.ReadOptions{})
	if err != nil || len(got) != 5 {
		t.Fatalf("after the kill: %d records, %v; want 5", len(got), err)
	}
	sum, err := s.Stat(bg, "s")
	if err != nil || sum.NextSeq != 5 || sum.Holder == nil {
		t.Fatalf("Stat after the kill = %+v, %v; want 5 records and the dead holder's lease", sum, err)
	}
}

// childEnviron is this process's environment without DSH_HOME, so a child never
// opens the developer's real home.
func childEnviron() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "DSH_HOME=") {
			env = append(env, kv)
		}
	}
	return env
}

// OpenHome opens <home>/dsh.db; the options stamp the holder's kind and the
// clock of a header without a creation time.
func TestOpenHomeAndOptions(t *testing.T) {
	l := temphome.New(t)
	at := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	s, err := sqlite.OpenHome(bg, l, sqlite.WithKind("cli"), sqlite.WithClock(func() time.Time { return at }),
		sqlite.WithRegistry(event.Default()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(bg) })
	if s.Path() != l.DB() {
		t.Fatalf("Path = %s, want %s", s.Path(), l.DB())
	}
	w, err := s.Create(bg, store.Header{ID: "s"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close(bg)
	sum, err := s.Stat(bg, "s")
	if err != nil {
		t.Fatal(err)
	}
	if h := sum.Holder; h == nil || h.Kind != "cli" || h.Instance != s.Instance() || h.PID != os.Getpid() || !h.Since.Equal(at) {
		t.Fatalf("Holder = %+v", sum.Holder)
	}
	if !sum.Header.CreatedAt.Equal(at) || !w.Header().CreatedAt.Equal(at) {
		t.Fatalf("CreatedAt = %v, want the store clock %v", sum.Header.CreatedAt, at)
	}
	untimed := call(t, 0)
	untimed.Time = 0
	if err := w.Append(bg, store.Batch{First: 0, Records: []store.Record{untimed}}); err != nil {
		t.Fatal(err)
	}
	if sum, err := s.Stat(bg, "s"); err != nil || !sum.LastEventAt.Equal(at) {
		t.Fatalf("LastEventAt of an untimed record = %v, %v; want the store clock", sum.LastEventAt, err)
	}
	if fi, err := os.Stat(l.DB()); err != nil || fi.Size() == 0 {
		t.Fatalf("dsh.db: %v, %v", fi, err)
	}
}

// Two instances on one home: the lease is the database's, so each sees the
// other's holder, and a forced takeover in one is lease-lost in the other.
func TestTwoInstancesShareTheLease(t *testing.T) {
	path := dbPath(t)
	a, b := open(t, path), open(t, path)
	if a.Instance() == b.Instance() {
		t.Fatal("two instances share an id")
	}
	wa, err := a.Create(bg, store.Header{ID: "s"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := wa.Append(bg, store.Batch{First: 0, Records: []store.Record{call(t, 0)}}); err != nil {
		t.Fatal(err)
	}
	_, err = b.OpenWrite(bg, "s", store.OpenOptions{})
	var owned *store.OwnedError
	if !errors.As(err, &owned) || owned.Holder.Instance != a.Instance() {
		t.Fatalf("OpenWrite in b = %v, want owned by a", err)
	}
	if _, err := b.Create(bg, store.Header{ID: "s"}, store.CreateOptions{}); !errors.Is(err, store.ErrExists) {
		t.Fatalf("Create in b = %v", err)
	}
	if sum, err := b.Stat(bg, "s"); err != nil || sum.NextSeq != 1 || sum.Durable != 1 {
		t.Fatalf("Stat in b = %+v, %v", sum, err)
	}
	wb, err := b.OpenWrite(bg, "s", store.OpenOptions{Takeover: store.Force})
	if err != nil {
		t.Fatal(err)
	}
	err = wa.Append(bg, store.Batch{First: 1, Records: []store.Record{call(t, 1)}})
	if !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("stale append in a = %v", err)
	}
	if err := wa.Flush(bg); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("stale flush in a = %v", err)
	}
	if err := wb.Append(bg, store.Batch{First: 1, Records: []store.Record{call(t, 1)}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(bg); err != nil { // closes the displaced writer
		t.Fatal(err)
	}
	if sum, err := b.Stat(bg, "s"); err != nil || sum.Holder == nil || sum.Holder.Instance != b.Instance() {
		t.Fatalf("closing a released b's lease: %+v, %v", sum, err)
	}
	if err := wb.Close(bg); err != nil {
		t.Fatal(err)
	}
}

// Refusals that need no session: arguments, a cancelled context, a closed store.
func TestRefusals(t *testing.T) {
	cancelled, cancel := context.WithCancel(bg)
	cancel()
	if _, err := sqlite.Open(cancelled, dbPath(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open(cancelled) = %v", err)
	}
	if _, err := sqlite.Open(bg, filepath.Join(t.TempDir(), "a?b.db")); err == nil {
		t.Fatal("a path holding a URI delimiter was opened")
	}
	if _, err := sqlite.Open(bg, filepath.Join(t.TempDir(), "missing", "dsh.db")); err == nil {
		t.Fatal("a database in a missing directory was opened")
	}
	path := dbPath(t)
	s := open(t, path)
	if _, err := s.Create(bg, store.Header{}, store.CreateOptions{}); !errors.Is(err, store.ErrInvalidBatch) {
		t.Fatalf("Create without an id = %v", err)
	}
	if err := s.Seed(bg, store.Header{}, nil); !errors.Is(err, store.ErrInvalidBatch) {
		t.Fatalf("Seed without an id = %v", err)
	}
	if err := s.Seed(bg, store.Header{ID: "x"}, []store.Record{call(t, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(bg, store.Header{ID: "x"}, nil); !errors.Is(err, store.ErrExists) {
		t.Fatalf("Seed twice = %v", err)
	}
	for name, err := range map[string]error{
		"Seed":   s.Seed(cancelled, store.Header{ID: "y"}, nil),
		"Flush":  s.Flush(cancelled),
		"Delete": s.Delete(cancelled, "x", store.DeleteOptions{}),
		"OpenWrite": func() error {
			_, err := s.OpenWrite(cancelled, "x", store.OpenOptions{})
			return err
		}(),
		"OpenRead": func() error { _, err := s.OpenRead(cancelled, "x"); return err }(),
		"Stat":     func() error { _, err := s.Stat(cancelled, "x"); return err }(),
		"List":     func() error { _, err := s.List(cancelled, store.Query{}); return err }(),
	} {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s(cancelled) = %v", name, err)
		}
	}
	r, err := s.OpenRead(bg, "x")
	if err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"Read":        func() error { _, err := r.Read(cancelled, 0, 0, store.ReadOptions{}); return err }(),
		"ReadSeqs":    func() error { _, err := r.ReadSeqs(cancelled, []store.Seq{0}, store.ReadOptions{}); return err }(),
		"Checkpoints": func() error { _, err := r.Checkpoints(cancelled); return err }(),
	} {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s(cancelled) = %v", name, err)
		}
	}
	w, err := s.OpenWrite(bg, "x", store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(cancelled); !errors.Is(err, context.Canceled) {
		t.Errorf("Writer.Flush(cancelled) = %v", err)
	}
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(bg, store.Header{ID: "z"}, store.CreateOptions{}); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("Create after Close = %v", err)
	}
	if _, err := s.Stat(bg, "x"); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("Stat after Close = %v", err)
	}
	if err := w.Flush(bg); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("Flush after Close = %v", err)
	}
	if err := s.Flush(bg); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("Store.Flush after Close = %v", err)
	}
}
