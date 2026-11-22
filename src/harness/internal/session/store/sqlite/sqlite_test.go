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
	"sync"
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
//
//dsh:test DH-T-STORE-1
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

// countRows is what a fresh connection reads of session id: its events, its
// next seq and its checkpoints.
func countRows(t *testing.T, db *sql.DB, id string) (events, next, checkpoints int64) {
	t.Helper()
	if err := db.QueryRow(`SELECT count(*) FROM events JOIN sessions ON events.session = sessions.uid WHERE sessions.id = ?`, id).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT next_seq FROM sessions WHERE id = ?`, id).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM checkpoints JOIN sessions ON checkpoints.session = sessions.uid WHERE sessions.id = ?`, id).Scan(&checkpoints); err != nil {
		t.Fatal(err)
	}
	return events, next, checkpoints
}

func countEvents(t *testing.T, db *sql.DB, id string) (events, next int64) {
	t.Helper()
	events, next, _ = countRows(t, db, id)
	return events, next
}

// TC-07 / DH-D-STORE-2: an append of three events whose third the database
// itself refuses leaves none of the three. The refusal comes from a trigger the
// test installs, so the batch passes every check of Writer.Append and the
// failure is the database's, met after two rows were already inserted in the
// transaction.
func TestMultiEventAppendIsAtomic(t *testing.T) {
	path := dbPath(t)
	s := open(t, path)
	w, err := s.Create(bg, store.Header{ID: "s"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(bg, store.Batch{First: 0, Records: []store.Record{call(t, 0)},
		Checkpoints: []store.Checkpoint{{Key: "core/x", StateVersion: 1, Seq: 0, State: `{"n":0}`}}}); err != nil {
		t.Fatal(err)
	}
	db := fresh(t, path)
	batch := store.Batch{First: 1, Records: []store.Record{call(t, 1), call(t, 2), call(t, 3)},
		Checkpoints: []store.Checkpoint{{Key: "core/x", StateVersion: 1, Seq: 3, State: `{"n":3}`}}}
	for _, tc := range []struct{ name, trigger string }{
		{"third event refused", `CREATE TRIGGER refuse_test BEFORE INSERT ON events WHEN NEW.seq = 3
			BEGIN SELECT RAISE(ABORT, 'test: the third row is refused'); END`},
		{"checkpoint refused after the events", `CREATE TRIGGER refuse_test BEFORE UPDATE ON checkpoints
			BEGIN SELECT RAISE(ABORT, 'test: the checkpoint is refused'); END`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(tc.trigger); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := db.Exec(`DROP TRIGGER refuse_test`); err != nil {
					t.Fatal(err)
				}
			}()
			err := w.Append(bg, batch)
			if !errors.Is(err, &store.Error{Code: sqlite.CodeConstraint}) || !strings.Contains(err.Error(), "test: the") {
				t.Fatalf("Append = %v, want %s from the test trigger", err, sqlite.CodeConstraint)
			}
			if events, next, cks := countRows(t, fresh(t, path), "s"); events != 1 || next != 1 || cks != 1 {
				t.Fatalf("after the refused append a fresh connection reads %d events, next seq %d, %d checkpoints; want 1, 1, 1", events, next, cks)
			}
			var state string
			if err := fresh(t, path).QueryRow(`SELECT state FROM checkpoints`).Scan(&state); err != nil || state != `{"n":0}` {
				t.Fatalf("checkpoint after the refused append = %q, %v", state, err)
			}
			if sum, err := s.Stat(bg, "s"); err != nil || sum.NextSeq != 1 || sum.Durable != 1 {
				t.Fatalf("Stat after the refused append = %+v, %v", sum, err)
			}
		})
	}
	// The same batch, once nothing refuses it, commits whole.
	if err := w.Append(bg, batch); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(bg); err != nil {
		t.Fatal(err)
	}
	if events, next, cks := countRows(t, fresh(t, path), "s"); events != 4 || next != 4 || cks != 1 {
		t.Fatalf("a fresh connection reads %d events, next seq %d, %d checkpoints; want 4, 4, 1", events, next, cks)
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

// TestFlushChild is the re-executed child of TestFlushSurvivesKill and
// TestOtherProcessSeesOnlyDurable. It appends five single-event batches
// without flushing and reports "appended sync=<synchronous>"; then, per line
// of stdin, "flush" flushes and reports "flushed". It never closes anything:
// the parent kills it.
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
	level, err := s.Synchronous(bg)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("appended sync=%d\n", level)
	for in := bufio.NewScanner(os.Stdin); in.Scan(); {
		if in.Text() == "flush" {
			if err := w.Flush(bg); err != nil {
				t.Fatal(err)
			}
			fmt.Println("flushed")
		}
	}
	t.Fatal("the parent did not kill the child")
}

// child runs TestFlushChild on path: send writes a line to its stdin, expect
// reads its stdout up to a line starting with prefix and returns that line,
// kill kills it and waits.
type child struct {
	t     *testing.T
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Scanner
	log   []string
}

func startChild(t *testing.T, path string) *child {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestFlushChild$", "-test.count=1", "-test.v")
	cmd.Env = append(childEnviron(), childEnv+"="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c := &child{t: t, cmd: cmd, stdin: stdin, out: bufio.NewScanner(out)}
	t.Cleanup(func() { stop(cmd) })
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return c
}

func (c *child) send(line string) {
	c.t.Helper()
	if _, err := fmt.Fprintln(c.stdin, line); err != nil {
		c.t.Fatal(err)
	}
}

func (c *child) expect(prefix string) string {
	c.t.Helper()
	for c.out.Scan() {
		c.log = append(c.log, c.out.Text())
		if strings.HasPrefix(c.out.Text(), prefix) {
			return c.out.Text()
		}
	}
	c.t.Fatalf("the child never reported %q:\n%s", prefix, strings.Join(c.log, "\n"))
	return ""
}

func (c *child) kill() { stop(c.cmd) }

// stop kills cmd, if it still runs, and waits for it.
func stop(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	cmd.Process.Kill() //nolint:errcheck // may be dead already
	cmd.Wait()         //nolint:errcheck // killed
	cmd.Process = nil
}

// TC-07 / DH-D-STORE-2: once Flush returns, the rows survive the death of the
// process that wrote them, killed before it closed anything.
func TestFlushSurvivesKill(t *testing.T) {
	path := dbPath(t)
	c := startChild(t, path)
	c.expect("appended")
	c.send("flush")
	c.expect("flushed")
	c.kill()
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
	if err != nil || sum.NextSeq != 5 || sum.Durable != 5 || sum.Holder == nil {
		t.Fatalf("Stat after the kill = %+v, %v; want 5 durable records and the dead holder's lease", sum, err)
	}
}

// session-store.md "What durable means": another process sees only durable
// events. The child commits five appends and does not flush; this process,
// another instance, must read exactly the events it reports durable, before
// the child's flush, after it, and after the child is killed. The child's
// write connection runs synchronous=FULL, which is what makes its unflushed
// commits durable rather than merely visible.
func TestOtherProcessSeesOnlyDurable(t *testing.T) {
	path := dbPath(t)
	c := startChild(t, path)
	if line := c.expect("appended"); line != "appended sync=2" {
		t.Fatalf("child reported %q; want its write connection at synchronous=FULL (2)", line)
	}
	s := open(t, path)
	visibleIsDurable := func(when string, want store.Seq) {
		t.Helper()
		sum, err := s.Stat(bg, "s")
		if err != nil {
			t.Fatalf("%s: Stat: %v", when, err)
		}
		r, err := s.OpenRead(bg, "s")
		if err != nil {
			t.Fatalf("%s: OpenRead: %v", when, err)
		}
		got, err := r.Read(bg, 0, 0, store.ReadOptions{})
		if err != nil {
			t.Fatalf("%s: Read: %v", when, err)
		}
		if sum.NextSeq != want || sum.Durable != want || store.Seq(len(got)) != sum.Durable {
			t.Fatalf("%s: another process reads %d events, Stat next %d durable %d; want %d of each",
				when, len(got), sum.NextSeq, sum.Durable, want)
		}
		if sum.Holder == nil || sum.Holder.Instance == s.Instance() {
			t.Fatalf("%s: holder %+v; want the child's", when, sum.Holder)
		}
	}
	visibleIsDurable("before the child's flush", 5)
	c.send("flush")
	c.expect("flushed")
	visibleIsDurable("after the child's flush", 5)
	c.kill()
	visibleIsDurable("after the child was killed", 5)
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

// holdWriteLock takes the database's write lock from a connection outside the
// store, as another process would, until the returned function releases it.
func holdWriteLock(t *testing.T, path string) (release func()) {
	t.Helper()
	conn, err := fresh(t, path).Conn(bg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(bg, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			if _, err := conn.ExecContext(bg, `ROLLBACK`); err != nil {
				t.Error(err)
			}
			conn.Close()
		})
	}
	t.Cleanup(release)
	return release
}

// A release that fails (another process holds the write lock) keeps the
// writer open and registered: Writer.Close and Store.Close both return the
// error, change nothing, and succeed when called again, after which the lease
// is free for an IfFree open elsewhere.
func TestCloseRetriesAFailedRelease(t *testing.T) {
	path := dbPath(t)
	s, err := sqlite.Open(bg, path, sqlite.WithBusyTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(bg) })
	w, err := s.Create(bg, store.Header{ID: "w"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(bg, store.Header{ID: "u"}, store.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	other := open(t, path)
	release := holdWriteLock(t, path)
	busy := &store.Error{Code: sqlite.CodeBusy}
	if err := w.Close(bg); !errors.Is(err, busy) {
		t.Fatalf("Writer.Close under another process's write lock = %v, want %s", err, sqlite.CodeBusy)
	}
	if err := s.Close(bg); !errors.Is(err, busy) {
		t.Fatalf("Store.Close under another process's write lock = %v, want %s", err, sqlite.CodeBusy)
	}
	for _, id := range []string{"w", "u"} {
		if sum, err := other.Stat(bg, id); err != nil || sum.Holder == nil || sum.Holder.Instance != s.Instance() {
			t.Fatalf("after the failed closes %s reads %+v, %v; want still held", id, sum, err)
		}
	}
	if _, err := s.OpenWrite(bg, "w", store.OpenOptions{Takeover: store.Force}); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("OpenWrite on a closing store = %v, want %v", err, store.ErrClosed)
	}
	release()
	if err := w.Close(bg); err != nil {
		t.Fatalf("Writer.Close retried = %v", err)
	}
	ow, err := other.OpenWrite(bg, "w", store.OpenOptions{})
	if err != nil {
		t.Fatalf("IfFree after the retried Writer.Close = %v", err)
	}
	if err := ow.Close(bg); err != nil {
		t.Fatal(err)
	}
	if _, err := other.OpenWrite(bg, "u", store.OpenOptions{}); err == nil {
		t.Fatal("IfFree took u before the failed Store.Close was retried")
	}
	if err := s.Close(bg); err != nil {
		t.Fatalf("Store.Close retried = %v", err)
	}
	uw, err := other.OpenWrite(bg, "u", store.OpenOptions{})
	if err != nil {
		t.Fatalf("IfFree after the retried Store.Close = %v", err)
	}
	if err := uw.Close(bg); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(bg); err != nil {
		t.Fatalf("Store.Close after success = %v", err)
	}
	if n := countWhere(t, fresh(t, path), `SELECT count(*) FROM hosts WHERE instance = ?`, s.Instance()); n != 0 {
		t.Fatalf("%d host rows of the closed instance", n)
	}
}

func countWhere(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// OpenWrite and Create racing Store.Close: each either returns session/closed
// having changed nothing, or returns a writer whose lease Close releases. No
// round leaves a lease held by the closed instance.
func TestCloseRacingAcquisitions(t *testing.T) {
	const rounds, n = 20, 8
	for round := range rounds {
		path := dbPath(t)
		s, err := sqlite.Open(bg, path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close(bg) })
		for i := range n {
			if err := s.Seed(bg, store.Header{ID: fmt.Sprintf("o%d", i)}, nil); err != nil {
				t.Fatal(err)
			}
		}
		var (
			wg       sync.WaitGroup
			start    = make(chan struct{})
			errs     = make(chan error, 2*n)
			closeErr error
		)
		for i := range n {
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				_, err := s.OpenWrite(bg, fmt.Sprintf("o%d", i), store.OpenOptions{})
				errs <- err
			}()
			go func() {
				defer wg.Done()
				<-start
				_, err := s.Create(bg, store.Header{ID: fmt.Sprintf("c%d", i)}, store.CreateOptions{})
				errs <- err
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			closeErr = s.Close(bg)
		}()
		close(start)
		wg.Wait()
		close(errs)
		if closeErr != nil {
			t.Fatalf("round %d: Close = %v", round, closeErr)
		}
		for err := range errs {
			if err != nil && !errors.Is(err, store.ErrClosed) {
				t.Fatalf("round %d: acquisition racing Close = %v", round, err)
			}
		}
		if held := countWhere(t, fresh(t, path), `SELECT count(*) FROM sessions WHERE lease_holder IS NOT NULL`); held != 0 {
			t.Fatalf("round %d: %d leases left held by the closed instance", round, held)
		}
	}
}

// closeAll calls s.Close from n goroutines at once and returns their results.
func closeAll(s *sqlite.Store, n int) []error {
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = s.Close(bg)
			if errs[i] == nil && !s.TornDown() {
				errs[i] = errors.New("Close returned nil before the pools were closed")
			}
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

// Concurrent Store.Close calls run one at a time through the whole teardown.
// While a release fails each call returns the error and closes nothing; once
// the release can succeed, exactly the leases and the host row are released,
// every call returns nil and the pools are closed once (run under -race).
func TestConcurrentClose(t *testing.T) {
	const callers = 8
	path := dbPath(t)
	s, err := sqlite.Open(bg, path, sqlite.WithBusyTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(bg) })
	for _, id := range []string{"a", "b"} {
		if _, err := s.Create(bg, store.Header{ID: id}, store.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	release := holdWriteLock(t, path)
	for i, err := range closeAll(s, callers) {
		if !errors.Is(err, &store.Error{Code: sqlite.CodeBusy}) {
			t.Fatalf("Close %d under another process's write lock = %v, want %s", i, err, sqlite.CodeBusy)
		}
	}
	db := fresh(t, path)
	if held := countWhere(t, db, `SELECT count(*) FROM sessions WHERE lease_holder = ?`, s.Instance()); held != 2 {
		t.Fatalf("%d leases held after the failed closes, want 2", held)
	}
	release()
	for i, err := range closeAll(s, callers) {
		if err != nil {
			t.Fatalf("Close %d = %v", i, err)
		}
	}
	if held := countWhere(t, db, `SELECT count(*) FROM sessions WHERE lease_holder IS NOT NULL`); held != 0 {
		t.Fatalf("%d leases held after Close", held)
	}
	if n := countWhere(t, db, `SELECT count(*) FROM hosts WHERE instance = ?`, s.Instance()); n != 0 {
		t.Fatalf("%d host rows of the closed instance", n)
	}
	if _, err := s.Stat(bg, "a"); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("Stat after Close = %v", err)
	}
}
