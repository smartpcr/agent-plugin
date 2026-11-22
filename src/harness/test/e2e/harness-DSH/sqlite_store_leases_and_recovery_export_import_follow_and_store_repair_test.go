//go:build e2e

package e2e

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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

// The re-executed children of this stage (service:os-process). init dispatches a
// child before TestMain, so a child runs no test. Roles, on the home named by
// eifrHomeEnv:
//
//   - append: creates session eifrFollowID and reports "ready"; on "go" it
//     appends 10 tool calls, one batch each, reporting
//     "committed seq=<n> at=<unix ns>" after each commit; before the next commit it
//     waits for the parent's "next", sent once B has received the event, so B sees
//     every commit on its own.
//   - delete: deletes session eifrDeleteID in chunks of eifrDeleteChunk events;
//     after the chunk that brings the removed events to eifrStopAtEnv it reports
//     "chunk deleted=<n>" and stops there, as a process killed mid-deletion would.
//
// No child closes its store: the parent kills it.
const (
	eifrRoleEnv     = "DSH_E2E_EIFR_ROLE"
	eifrHomeEnv     = "DSH_E2E_EIFR_HOME"
	eifrStopAtEnv   = "DSH_E2E_EIFR_STOP_AT"
	eifrDeleteChunk = 100
	eifrFollowID    = "f"
	eifrDeleteID    = "big"
	eifrTimeout     = 60 * time.Second
	eifrInterval    = 50 * time.Millisecond
	eifrGoldenRel   = "../../../internal/session/store/sqlite/testdata/export.golden"
)

var eifrBG = context.Background()

// eifrOuterT is the suite's *testing.T, for the temp homes under t.TempDir().
var eifrOuterT *testing.T

func init() {
	role := os.Getenv(eifrRoleEnv)
	if role == "" {
		return
	}
	if err := eifrChild(role, os.Getenv(eifrHomeEnv)); err != nil {
		fmt.Printf("child error %v\n", err)
		os.Exit(2)
	}
	os.Exit(0)
}

// --- children ---

func eifrChild(role, root string) error {
	if root == "" {
		return errors.New("no home")
	}
	in := bufio.NewScanner(os.Stdin)
	switch role {
	case "append":
		s, err := sqlite.OpenHome(eifrBG, home.New(root), sqlite.WithKind("e2e-child"))
		if err != nil {
			return err
		}
		w, err := s.Create(eifrBG, store.Header{ID: eifrFollowID}, store.CreateOptions{})
		if err != nil {
			return err
		}
		fmt.Println("ready")
		for in.Scan() && in.Text() != "go" {
		}
		for seq := store.Seq(0); seq < 10; seq++ {
			rec, err := eifrCall(seq, time.Now().UnixMilli())
			if err != nil {
				return err
			}
			if err := w.Append(eifrBG, store.Batch{First: seq, Records: []store.Record{rec}}); err != nil {
				return err
			}
			fmt.Printf("committed seq=%d at=%d\n", seq, time.Now().UnixNano())
			if seq < 9 {
				for in.Scan() && in.Text() != "next" {
				}
			}
		}
		fmt.Println("appended")
	case "delete":
		stopAt, err := strconv.ParseInt(os.Getenv(eifrStopAtEnv), 10, 64)
		if err != nil || stopAt <= 0 {
			return fmt.Errorf("bad %s %q: %v", eifrStopAtEnv, os.Getenv(eifrStopAtEnv), err)
		}
		s, err := sqlite.OpenHome(eifrBG, home.New(root), sqlite.WithKind("e2e-child"), sqlite.WithDeleteChunk(eifrDeleteChunk))
		if err != nil {
			return err
		}
		stopped := make(chan struct{})
		sqlite.E2ESetAfterDeleteChunk(func(_ string, deleted int64) {
			if deleted >= stopAt {
				fmt.Printf("chunk deleted=%d\n", deleted)
				close(stopped)
				select {} // killed here, between two chunks
			}
		})
		done := make(chan error, 1)
		go func() { done <- s.Delete(eifrBG, eifrDeleteID, store.DeleteOptions{}) }()
		select {
		case <-stopped:
		case err := <-done:
			return fmt.Errorf("the deletion ended before the injected stop: %v", err)
		}
	default:
		return fmt.Errorf("unknown role %q", role)
	}
	for in.Scan() {
	}
	return errors.New("the parent did not kill the child")
}

func eifrCall(seq store.Seq, ms int64) (store.Record, error) {
	d, err := event.ToolCall.Draft(event.ToolCallPayload{CallID: fmt.Sprintf("c%d", seq), Name: "grep", Arguments: `{}`})
	if err != nil {
		return store.Record{}, err
	}
	return d.Stamp(seq, ms)
}

// --- parent side ---

type eifrProc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan string
	log   []string
	done  bool
}

func eifrSpawn(root, role string, extra ...string) (*eifrProc, error) {
	cmd := exec.Command(os.Args[0], "-test.run=^$", "-test.count=1")
	var env []string
	for _, kv := range os.Environ() {
		up := strings.ToUpper(kv)
		if strings.HasPrefix(up, "DSH_HOME=") || strings.HasPrefix(up, eifrRoleEnv+"=") || strings.HasPrefix(up, eifrHomeEnv+"=") || strings.HasPrefix(up, eifrStopAtEnv+"=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(append(env, eifrRoleEnv+"="+role, eifrHomeEnv+"="+root), extra...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &eifrProc{cmd: cmd, stdin: stdin, lines: make(chan string, 64)}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			p.lines <- sc.Text()
		}
		close(p.lines)
	}()
	return p, nil
}

// expect is the child's next report line starting with prefix.
func (p *eifrProc) expect(prefix string) (string, error) {
	deadline := time.After(eifrTimeout)
	for {
		select {
		case line, ok := <-p.lines:
			if !ok {
				return "", fmt.Errorf("the child ended before %q:\n%s", prefix, strings.Join(p.log, "\n"))
			}
			p.log = append(p.log, line)
			if strings.HasPrefix(line, "child error") {
				return "", fmt.Errorf("the child failed: %s", line)
			}
			if strings.HasPrefix(line, prefix) {
				return line, nil
			}
		case <-deadline:
			return "", fmt.Errorf("no %q from the child within %s:\n%s", prefix, eifrTimeout, strings.Join(p.log, "\n"))
		}
	}
}

func (p *eifrProc) send(line string) error {
	_, err := fmt.Fprintln(p.stdin, line)
	return err
}

// kill ends the child without a clean stop and reaps it.
func (p *eifrProc) kill() {
	if p.done {
		return
	}
	p.done = true
	_ = p.cmd.Process.Kill()
	_ = p.cmd.Wait()
	_ = p.stdin.Close()
}

func eifrField(line, key string) (string, error) {
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v, nil
		}
	}
	return "", fmt.Errorf("no %s= in %q", key, line)
}

// eifrFixture is the fixed session of the committed export golden, the same
// fixture internal/session/store/sqlite's own round-trip test records: 50 events
// at fixed times, mostly tool calls (one payload with characters JSON escapes for
// HTML), and every fifth an ignorable record of a type this build does not know,
// carrying every envelope field and an aux part.
func eifrFixture(id string) (store.Header, []store.Record, error) {
	base := time.Date(2025, 7, 1, 12, 0, 0, 0, time.UTC).UnixMilli()
	h := store.Header{ID: id, CreatedAt: time.Date(2025, 7, 1, 11, 59, 0, 0, time.UTC),
		Cwd: `C:\work\repo`, Preset: "default"}
	recs := make([]store.Record, 0, 50)
	for seq := store.Seq(0); seq < 50; seq++ {
		at := base + int64(seq)*1000
		if seq%5 == 4 {
			recs = append(recs, store.Record{Seq: seq, Time: at, Type: "future/thing", Version: 3, Ignorable: true,
				Op: event.OpReplace, Start: seq - 2, End: seq - 1, Sources: []store.Seq{seq - 3, seq - 1},
				Payload: fmt.Sprintf(`{"n": %d,  "kept":"as written"}`, seq), Cold: `{"raw":"stream <&>"}`})
			continue
		}
		args := `{"q":"x"}`
		if seq == 7 {
			args = `{"q":"a<b && c>d \u00e9"}`
		}
		d, err := event.ToolCall.Draft(event.ToolCallPayload{CallID: fmt.Sprintf("c%d", seq), Name: "grep", Arguments: args})
		if err != nil {
			return h, nil, err
		}
		r, err := d.Stamp(seq, at)
		if err != nil {
			return h, nil, err
		}
		recs = append(recs, r)
	}
	return h, recs, nil
}

type eifrState struct {
	cleanups []func()

	// export / import
	h1, h2 home.Layout
	s1, s2 *sqlite.Store
	file   []byte

	// follow and deletion
	root   string
	layout home.Layout
	proc   *eifrProc
	spill  string
	total  int
	store  *sqlite.Store
	report sqlite.CheckReport
}

func (s *eifrState) cleanup(fn func()) { s.cleanups = append(s.cleanups, fn) }

// newHome makes a home: under t.TempDir() (embedded gate path) or a fresh
// directory under DSH_E2E_STORE_HOME (compose path).
func (s *eifrState) newHome() (home.Layout, error) {
	var base string
	if v := os.Getenv("DSH_E2E_STORE_HOME"); v != "" {
		dir, err := os.MkdirTemp(v, "e2e-eifr-")
		if err != nil {
			return home.Layout{}, err
		}
		s.cleanup(func() { os.RemoveAll(dir) })
		base = dir
	} else if eifrOuterT != nil {
		base = eifrOuterT.TempDir()
	} else {
		return home.Layout{}, errors.New("no outer *testing.T; run through TestE2E_sqlite_store_leases_and_recovery_export_import_follow_and_store_repair")
	}
	l := home.New(filepath.Join(base, "home"))
	return l, home.Ensure(l)
}

func (s *eifrState) open(l home.Layout, opts ...sqlite.Option) (*sqlite.Store, error) {
	st, err := sqlite.OpenHome(eifrBG, l, opts...)
	if err != nil {
		return nil, err
	}
	s.cleanup(func() { _ = st.Close(eifrBG) })
	return st, nil
}

func (s *eifrState) spawn(role string, extra ...string) (*eifrProc, error) {
	p, err := eifrSpawn(s.root, role, extra...)
	if err != nil {
		return nil, err
	}
	s.cleanup(p.kill)
	return p, nil
}

// eventRows is every events row of session id in seq order (sequence, type,
// payload version, payload and the rest of the row), read by a fresh connection.
func eifrEventRows(l home.Layout, id string) ([]string, error) {
	db, err := sql.Open("sqlite", l.DB())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rs, err := db.Query(`SELECT e.seq, e.time, e.type, e.pv, e.ignorable, e.surface, e.env, e.data, e.cold
		FROM events e JOIN sessions s ON s.uid = e.session WHERE s.id = ? ORDER BY e.seq`, id)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var (
			seq, tm, pv, ign, surf int64
			typ, data              string
			env, cold              sql.NullString
		)
		if err := rs.Scan(&seq, &tm, &typ, &pv, &ign, &surf, &env, &data, &cold); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("%d|%d|%s|%d|%d|%d|%v|%s|%v", seq, tm, typ, pv, ign, surf, env, data, cold))
	}
	return out, rs.Err()
}

func eifrCount(l home.Layout, q string, args ...any) (int64, error) {
	db, err := sql.Open("sqlite", l.DB())
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var n int64
	err = db.QueryRow(q, args...).Scan(&n)
	return n, err
}

// --- Scenario: Export then import round-trips ---

func (s *eifrState) sessionInH1(id string, n int) error {
	var err error
	if s.h1, err = s.newHome(); err != nil {
		return err
	}
	if s.s1, err = s.open(s.h1); err != nil {
		return err
	}
	h, recs, err := eifrFixture(id)
	if err != nil {
		return err
	}
	if len(recs) != n {
		return fmt.Errorf("the fixture has %d events; want %d", len(recs), n)
	}
	return s.s1.Seed(eifrBG, h, recs)
}

func (s *eifrState) exportImport() error {
	var err error
	if s.h2, err = s.newHome(); err != nil {
		return err
	}
	if s.s2, err = s.open(s.h2); err != nil {
		return err
	}
	if n, err := eifrCount(s.h2, `SELECT count(*) FROM sessions`); err != nil || n != 0 {
		return fmt.Errorf("H2 holds %d sessions before the import (%v); want it empty", n, err)
	}
	var buf bytes.Buffer
	if err := s.s1.Export(eifrBG, &buf, "export-fixture", sqlite.ExportOptions{}); err != nil {
		return fmt.Errorf("Export: %w", err)
	}
	s.file = buf.Bytes()
	h, err := s.s2.Import(eifrBG, bytes.NewReader(s.file), sqlite.ImportOptions{})
	if err != nil {
		return fmt.Errorf("Import: %w", err)
	}
	if h.ID != "export-fixture" || h.Cwd != `C:\work\repo` || h.Preset != "default" {
		return fmt.Errorf("imported header %+v", h)
	}
	return nil
}

func (s *eifrState) rowsEqual(id string) error {
	want, err := eifrEventRows(s.h1, id)
	if err != nil {
		return err
	}
	have, err := eifrEventRows(s.h2, id)
	if err != nil {
		return err
	}
	if len(want) != 50 {
		return fmt.Errorf("H1 holds %d rows of %q; want 50", len(want), id)
	}
	if strings.Join(want, "\n") != strings.Join(have, "\n") {
		return fmt.Errorf("H2 rows differ from H1's:\nH1 (%d):\n%s\nH2 (%d):\n%s", len(want), strings.Join(want, "\n"),
			len(have), strings.Join(have, "\n"))
	}
	r, err := s.s2.OpenRead(eifrBG, id)
	if err != nil {
		return err
	}
	read, err := r.Read(eifrBG, 0, 0, store.ReadOptions{WithAux: true})
	if err != nil {
		return err
	}
	_, recs, err := eifrFixture(id)
	if err != nil {
		return err
	}
	if len(read) != len(recs) {
		return fmt.Errorf("H2 reads %d records; want %d", len(read), len(recs))
	}
	for i := range recs {
		if !read[i].Equal(recs[i]) {
			return fmt.Errorf("record %d in H2 = %+v; want %+v", i, read[i], recs[i])
		}
	}
	return nil
}

func (s *eifrState) equalsGolden(name string) error {
	if name != "export" {
		return fmt.Errorf("unknown golden %q", name)
	}
	want, err := os.ReadFile(filepath.FromSlash(eifrGoldenRel))
	if err != nil {
		return fmt.Errorf("the committed golden: %w", err)
	}
	if !bytes.Equal(want, s.file) {
		return fmt.Errorf("the export differs from %s:\n%s", eifrGoldenRel, golden.Diff(string(want), string(s.file)))
	}
	return nil
}

func (s *eifrState) exportAgain(id string) error {
	var buf bytes.Buffer
	if err := s.s2.Export(eifrBG, &buf, id, sqlite.ExportOptions{}); err != nil {
		return err
	}
	if !bytes.Equal(buf.Bytes(), s.file) {
		return fmt.Errorf("export, import, export: the two files differ:\n%s", golden.Diff(string(s.file), buf.String()))
	}
	return nil
}

// --- Scenario: Follower sees another process's commits ---

type eifrFollowState struct {
	sub       *sqlite.Subscription
	committed map[store.Seq]time.Time
}

func (s *eifrState) appenderA(n int, id string) error {
	if n != 10 || id != eifrFollowID {
		return fmt.Errorf("the appender child appends 10 events to %q", eifrFollowID)
	}
	var err error
	if s.layout, err = s.newHome(); err != nil {
		return err
	}
	s.root = s.layout.Root()
	if s.proc, err = s.spawn("append"); err != nil {
		return err
	}
	_, err = s.proc.expect("ready")
	return err
}

var eifrFollow eifrFollowState

func (s *eifrState) bFollows(id string) error {
	var err error
	if s.store, err = s.open(s.layout, sqlite.WithFollowInterval(eifrInterval)); err != nil {
		return err
	}
	sub, err := s.store.Follow(eifrBG, id, 0)
	if err != nil {
		return fmt.Errorf("Follow: %w", err)
	}
	s.cleanup(sub.Close)
	eifrFollow = eifrFollowState{sub: sub, committed: map[store.Seq]time.Time{}}
	return s.proc.send("go")
}

func (s *eifrState) observesInOrder(n, bound int) error {
	f := &eifrFollow
	limit := time.Duration(bound) * time.Second
	for want := store.Seq(0); want < store.Seq(n); want++ {
		select {
		case rec, ok := <-f.sub.Events():
			if !ok {
				return fmt.Errorf("the subscription ended at seq %d: %v", want, f.sub.Err())
			}
			got := time.Now()
			if rec.Seq != want || rec.Type != "tool/call" {
				return fmt.Errorf("B received seq %d (%s); want seq %d, in order", rec.Seq, rec.Type, want)
			}
			for f.committed[want].IsZero() {
				line, err := s.proc.expect("committed")
				if err != nil {
					return err
				}
				sq, err := eifrField(line, "seq")
				if err != nil {
					return err
				}
				at, err := eifrField(line, "at")
				if err != nil {
					return err
				}
				k, _ := strconv.Atoi(sq)
				ns, _ := strconv.ParseInt(at, 10, 64)
				f.committed[store.Seq(k)] = time.Unix(0, ns)
			}
			// A reports its commit after Append returned, so B may see the row first.
			if lag := got.Sub(f.committed[want]); lag > limit {
				return fmt.Errorf("seq %d reached B %s after its commit; want within %s", want, lag, limit)
			}
			if want < store.Seq(n)-1 {
				if err := s.proc.send("next"); err != nil {
					return err
				}
			}
		case <-time.After(10 * time.Second):
			return fmt.Errorf("B never received seq %d", want)
		}
	}
	return nil
}

func (s *eifrState) nothingPastEnd() error {
	if _, err := s.proc.expect("appended"); err != nil {
		return err
	}
	select {
	case rec, ok := <-eifrFollow.sub.Events():
		if ok {
			return fmt.Errorf("B received seq %d again or past the end", rec.Seq)
		}
		return fmt.Errorf("the subscription ended: %v", eifrFollow.sub.Err())
	case <-time.After(5 * eifrInterval):
	}
	return nil
}

// --- Scenario: Interrupted deletion finishes ---

func (s *eifrState) bigSession(id string, n int) error {
	if id != eifrDeleteID {
		return fmt.Errorf("the deleter child deletes %q", eifrDeleteID)
	}
	var err error
	if s.layout, err = s.newHome(); err != nil {
		return err
	}
	s.root = s.layout.Root()
	s.total = n
	st, err := sqlite.OpenHome(eifrBG, s.layout)
	if err != nil {
		return err
	}
	at := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	recs := make([]store.Record, n)
	for i := range recs {
		if recs[i], err = eifrCall(store.Seq(i), at.Add(time.Duration(i)*time.Second).UnixMilli()); err != nil {
			return err
		}
	}
	if err := st.Seed(eifrBG, store.Header{ID: id}, recs); err != nil {
		return err
	}
	if err := st.Close(eifrBG); err != nil {
		return err
	}
	s.spill = filepath.Join(s.layout.Spill(), sqlite.SpillName(id))
	if err := os.MkdirAll(s.spill, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.spill, "0123-out.txt"), []byte("spilled"), 0o600)
}

func (s *eifrState) deletionKilled(id string, half int) error {
	if half <= 0 || half >= s.total || half%eifrDeleteChunk != 0 {
		return fmt.Errorf("stop after %d of %d events: want a multiple of the %d-event chunk strictly between 0 and the total", half, s.total, eifrDeleteChunk)
	}
	var err error
	if s.proc, err = s.spawn("delete", fmt.Sprintf("%s=%d", eifrStopAtEnv, half)); err != nil {
		return err
	}
	line, err := s.proc.expect("chunk")
	if err != nil {
		return err
	}
	v, err := eifrField(line, "deleted")
	if err != nil {
		return err
	}
	if k, _ := strconv.Atoi(v); k != half {
		return fmt.Errorf("the deleter reported %q; want it stopped after %d events", line, half)
	}
	left, err := eifrCount(s.layout, `SELECT count(*) FROM events WHERE session IN (SELECT uid FROM sessions WHERE id = ?)`, id)
	if err != nil {
		return err
	}
	if want := int64(s.total - half); left != want {
		return fmt.Errorf("%d events left mid-deletion; want %d", left, want)
	}
	if n, err := eifrCount(s.layout, `SELECT count(*) FROM sessions WHERE id = ? AND state = 'deleting'`, id); err != nil || n != 1 {
		return fmt.Errorf("%d tombstones of %q mid-deletion (%v); want 1", n, id, err)
	}
	s.proc.kill()
	if left, err := eifrCount(s.layout, `SELECT count(*) FROM events`); err != nil || left != int64(s.total-half) {
		return fmt.Errorf("%d events after the kill (%v); want %d left for the reopen to finish", left, err, s.total-half)
	}
	return nil
}

func (s *eifrState) reopen() error {
	var err error
	s.store, err = s.open(s.layout)
	return err
}

func (s *eifrState) deletionCompleted() error {
	var err error
	if s.report, err = s.store.Check(eifrBG); err != nil {
		return err
	}
	if len(s.report.Deleting) != 0 {
		return fmt.Errorf("still being deleted after the reopen: %v", s.report.Deleting)
	}
	if _, err := s.store.Stat(eifrBG, eifrDeleteID); !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("Stat(%s) after the reopen = %v; want not-found", eifrDeleteID, err)
	}
	return nil
}

func (s *eifrState) noRow(id string) error {
	for _, q := range []string{
		`SELECT count(*) FROM sessions WHERE id = ?`,
		`SELECT count(*) FROM events WHERE session IN (SELECT uid FROM sessions WHERE id = ?)`,
		`SELECT count(*) FROM checkpoints WHERE session IN (SELECT uid FROM sessions WHERE id = ?)`,
		`SELECT count(*) FROM requests WHERE session = ?`,
	} {
		n, err := eifrCount(s.layout, q, id)
		if err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("%q left %d rows: %s", id, n, q)
		}
	}
	if n, err := eifrCount(s.layout, `SELECT count(*) FROM events`); err != nil || n != 0 {
		return fmt.Errorf("%d events in the home after the deletion (%v); want 0", n, err)
	}
	return nil
}

func (s *eifrState) noSpill(id string) error {
	dir := filepath.Join(s.layout.Spill(), sqlite.SpillName(id))
	if dir != s.spill {
		return fmt.Errorf("spill directory %s; want %s", dir, s.spill)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		return fmt.Errorf("the spill directory of %q: %v; want it gone", id, err)
	}
	return nil
}

func (s *eifrState) checkClean() error {
	if !s.report.Clean() || len(s.report.Deleting) != 0 {
		return fmt.Errorf("Check = %+v; want clean, nothing being deleted", s.report)
	}
	return nil
}

func InitializeScenario_sqlite_store_leases_and_recovery_export_import_follow_and_store_repair(ctx *godog.ScenarioContext) {
	s := &eifrState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = eifrState{}
		eifrFollow = eifrFollowState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		for i := len(s.cleanups) - 1; i >= 0; i-- {
			s.cleanups[i]()
		}
		return c, nil
	})

	ctx.Step(`^a session "([^"]*)" with (\d+) events in home H1$`, s.sessionInH1)
	ctx.Step(`^it is exported from H1 and imported into empty home H2$`, s.exportImport)
	ctx.Step(`^H2's event rows of "([^"]*)" equal H1's in type, version, payload and sequence$`, s.rowsEqual)
	ctx.Step(`^the export file equals the committed golden "([^"]*)"$`, s.equalsGolden)
	ctx.Step(`^exporting "([^"]*)" again from H2 gives the same bytes$`, s.exportAgain)

	ctx.Step(`^process A appending (\d+) events to session "([^"]*)" in a temp home$`, s.appenderA)
	ctx.Step(`^process B follows session "([^"]*)"$`, s.bFollows)
	ctx.Step(`^B observes all (\d+) events in order within (\d+) seconds of each commit$`, s.observesInOrder)
	ctx.Step(`^B observes no event past the end$`, s.nothingPastEnd)

	ctx.Step(`^a session "([^"]*)" with (\d+) events and a spill file in a temp home$`, s.bigSession)
	ctx.Step(`^a deletion of "([^"]*)" killed after removing (\d+) of its events$`, s.deletionKilled)
	ctx.Step(`^the store is reopened$`, s.reopen)
	ctx.Step(`^the deletion has completed$`, s.deletionCompleted)
	ctx.Step(`^no row of "([^"]*)" remains$`, s.noRow)
	ctx.Step(`^no spill file of "([^"]*)" remains$`, s.noSpill)
	ctx.Step(`^check reports clean$`, s.checkClean)
}

func TestE2E_sqlite_store_leases_and_recovery_export_import_follow_and_store_repair(t *testing.T) {
	eifrOuterT = t
	defer func() { eifrOuterT = nil }()
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_sqlite_store_leases_and_recovery_export_import_follow_and_store_repair,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"sqlite_store_leases_and_recovery_export_import_follow_and_store_repair.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
