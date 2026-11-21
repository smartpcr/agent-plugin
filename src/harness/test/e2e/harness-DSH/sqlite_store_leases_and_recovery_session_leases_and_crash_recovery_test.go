//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
)

// The re-executed children of this stage (service:os-process). init dispatches a
// child before TestMain, so a child runs no test and the parent's run has no
// skipped placeholder test. Roles, all on the home named by slcrHomeEnv:
//
//   - hold: creates session slcrSession with three tool/call events, flushes and
//     reports "holding instance=<id> epoch=<n> pid=<pid>"; then appends one
//     event per "append" line on stdin, reporting "append ok" or
//     "append err=<code>".
//   - open: opens the store, reports "ready instance=<id> pid=<pid>", waits for
//     "go" and tries OpenWrite on slcrSession, reporting "result opened
//     epoch=<n>" or "result refused code=<code> holder=<id> pid=<pid>"; when it
//     opened, it then serves "append" lines like hold.
//   - crash: through the session core appends turn/start, step/start, a user
//     message, an assistant message asking for tool call c1 and the tool/call
//     of c1, reports "crashing id=<session>" and exits with slcrCrashExit before
//     any flush or close.
//
// No child closes its store: the parent kills it (or it exits in place).
const (
	slcrRoleEnv   = "DSH_E2E_SLCR_ROLE"
	slcrHomeEnv   = "DSH_E2E_SLCR_HOME"
	slcrSession   = "S"
	slcrCrashID   = "crash"
	slcrCrashExit = 3
	slcrTimeout   = 60 * time.Second
)

var slcrBG = context.Background()

// slcrOuterT is the suite's *testing.T, for the temp homes under t.TempDir().
var slcrOuterT *testing.T

func init() {
	role := os.Getenv(slcrRoleEnv)
	if role == "" {
		return
	}
	if err := slcrChild(role, os.Getenv(slcrHomeEnv)); err != nil {
		fmt.Printf("child error %v\n", err)
		os.Exit(2)
	}
	os.Exit(0)
}

// --- children ---

func slcrChild(role, root string) error {
	if root == "" {
		return errors.New("no home")
	}
	s, err := sqlite.OpenHome(slcrBG, home.New(root), sqlite.WithKind("e2e-child"))
	if err != nil {
		return err
	}
	in := bufio.NewScanner(os.Stdin)
	switch role {
	case "hold":
		w, err := s.Create(slcrBG, store.Header{ID: slcrSession}, store.CreateOptions{})
		if err != nil {
			return err
		}
		for seq := store.Seq(0); seq < 3; seq++ {
			if err := slcrAppendOne(w, seq, "a"); err != nil {
				return err
			}
		}
		if err := w.Flush(slcrBG); err != nil {
			return err
		}
		fmt.Printf("holding instance=%s epoch=%d pid=%d\n", s.Instance(), w.Epoch(), os.Getpid())
		return slcrServeAppends(s, w, in, "a")
	case "open":
		fmt.Printf("ready instance=%s pid=%d\n", s.Instance(), os.Getpid())
		for in.Scan() && in.Text() != "go" {
		}
		w, err := s.OpenWrite(slcrBG, slcrSession, store.OpenOptions{})
		var owned *store.OwnedError
		switch {
		case err == nil:
			fmt.Printf("result opened epoch=%d\n", w.Epoch())
			return slcrServeAppends(s, w, in, "b")
		case errors.As(err, &owned):
			fmt.Printf("result refused code=%s is-owned=%t holder=%s pid=%d since=%d\n", slcrWireCode(err),
				errors.Is(err, store.ErrOwned), owned.Holder.Instance, owned.Holder.PID, owned.Holder.Since.UnixNano())
		default:
			fmt.Printf("result error code=%s %v\n", slcrCodeOf(err), err)
		}
		for in.Scan() {
		}
		return nil
	case "crash":
		return slcrCrashTurn(s)
	}
	return fmt.Errorf("unknown role %q", role)
}

// slcrServeAppends appends one tool/call, tagged by who, per "append" line.
func slcrServeAppends(s *sqlite.Store, w store.Writer, in *bufio.Scanner, who string) error {
	for in.Scan() {
		if in.Text() != "append" {
			continue
		}
		sum, err := s.Stat(slcrBG, slcrSession)
		if err != nil {
			return err
		}
		if err := slcrAppendOne(w, sum.NextSeq, who); err != nil {
			fmt.Printf("append err=%s\n", slcrCodeOf(err))
			continue
		}
		fmt.Println("append ok")
	}
	return nil
}

// slcrAppendOne appends a tool/call at seq whose call id names its writer, at a
// time not before the session's last event (another process may have stamped it).
func slcrAppendOne(w store.Writer, seq store.Seq, who string) error {
	ms := time.Now().UnixMilli()
	if seq > 0 {
		last, err := w.Read(slcrBG, seq-1, 1, store.ReadOptions{})
		if err != nil {
			return err
		}
		if len(last) == 1 && last[0].Time > ms {
			ms = last[0].Time
		}
	}
	rec, err := slcrCall(seq, who, ms)
	if err != nil {
		return err
	}
	return w.Append(slcrBG, store.Batch{First: seq, Records: []store.Record{rec}})
}

func slcrCall(seq store.Seq, who string, ms int64) (store.Record, error) {
	d, err := event.ToolCall.Draft(event.ToolCallPayload{CallID: fmt.Sprintf("%s%d", who, seq), Name: "grep", Arguments: `{"q":"x"}`})
	if err != nil {
		return store.Record{}, err
	}
	return d.Stamp(seq, ms)
}

func slcrCrashTurn(s *sqlite.Store) error {
	root := cordis.New()
	root.Loggers().ClearExporters()
	svc := session.NewService(root, s, session.Config{})
	h, err := svc.Create(slcrBG, nil, session.CreateOptions{Header: session.Header{ID: slcrCrashID}})
	if err != nil {
		return err
	}
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("search"))
	if err != nil {
		return err
	}
	asked, err := vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, vocab.ToolUseBlock("c1", "grep", json.RawMessage(`{"q":"x"}`)))
	if err != nil {
		return err
	}
	drafts := []func() (event.Draft, error){
		func() (event.Draft, error) { return event.TurnStartType.Draft(event.TurnStart{Turn: 1}) },
		func() (event.Draft, error) { return event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1}) },
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
		func() (event.Draft, error) {
			return event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: asked})
		},
		func() (event.Draft, error) {
			return event.ToolCall.Draft(event.ToolCallPayload{CallID: "c1", Name: "grep", Arguments: `{"q":"x"}`})
		},
	}
	for _, mk := range drafts {
		d, err := mk()
		if err != nil {
			return err
		}
		if _, err := h.Append(slcrBG, d); err != nil {
			return err
		}
	}
	// Every append above committed; nothing is flushed or closed.
	fmt.Printf("crashing id=%s\n", slcrCrashID)
	os.Exit(slcrCrashExit)
	return nil
}

// slcrWireCode is the code a refusal carries on the wire: the <domain>/<reason>
// prefix of its message (core-session.md errors), as the production error
// formats it. No mapping is applied.
func slcrWireCode(err error) string {
	code, _, _ := strings.Cut(err.Error(), ":")
	return code
}

func slcrCodeOf(err error) store.Code {
	var owned *store.OwnedError
	if errors.As(err, &owned) {
		return store.CodeOwned
	}
	var se *store.Error
	if errors.As(err, &se) {
		return se.Code
	}
	return "?"
}

// --- parent side ---

type slcrProc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan string
	log   []string
	done  chan struct{}
}

func slcrSpawn(root, role string) (*slcrProc, error) {
	cmd := exec.Command(os.Args[0], "-test.run=^$", "-test.count=1")
	var env []string
	for _, kv := range os.Environ() {
		up := strings.ToUpper(kv)
		if strings.HasPrefix(up, "DSH_HOME=") || strings.HasPrefix(up, slcrRoleEnv+"=") || strings.HasPrefix(up, slcrHomeEnv+"=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, slcrRoleEnv+"="+role, slcrHomeEnv+"="+root)
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
	p := &slcrProc{cmd: cmd, stdin: stdin, lines: make(chan string, 64), done: make(chan struct{})}
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
func (p *slcrProc) expect(prefix string) (string, error) {
	deadline := time.After(slcrTimeout)
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
			return "", fmt.Errorf("no %q from the child within %s:\n%s", prefix, slcrTimeout, strings.Join(p.log, "\n"))
		}
	}
}

func (p *slcrProc) send(line string) error {
	_, err := fmt.Fprintln(p.stdin, line)
	return err
}

// wait reaps the child once; later calls return the first result.
func (p *slcrProc) wait() {
	select {
	case <-p.done:
	default:
		_ = p.cmd.Wait()
		close(p.done)
	}
}

// kill ends the child without a clean stop and reaps it.
func (p *slcrProc) kill() {
	select {
	case <-p.done:
		return
	default:
	}
	_ = p.cmd.Process.Kill()
	p.wait()
}

func slcrField(line, key string) string {
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v
		}
	}
	return ""
}

type slcrState struct {
	root     string
	layout   home.Layout
	cleanups []func()

	a, b          *slcrProc
	aInst, aPID   string
	bInst         string
	bResult       string
	observer      *sqlite.Store
	staleErr      error
	staleFlushErr error

	// crash
	crashBefore []string
	report      session.OpenReport
	reopened    *sqlite.Store
	rows        []slcrRow

	// checkpoint
	faults     *slcrFaults
	handle     session.Handle
	calls      int
	toolErr    error
	stepEndErr error
}

type slcrRow struct{ typ, data string }

// newHome makes the scenario's home: under t.TempDir() (embedded gate path) or a
// fresh directory under DSH_E2E_STORE_HOME (compose path).
func (s *slcrState) newHome() error {
	var base string
	if v := os.Getenv("DSH_E2E_STORE_HOME"); v != "" {
		dir, err := os.MkdirTemp(v, "e2e-slcr-")
		if err != nil {
			return err
		}
		s.cleanups = append(s.cleanups, func() { os.RemoveAll(dir) })
		base = dir
	} else if slcrOuterT != nil {
		base = slcrOuterT.TempDir()
	} else {
		return errors.New("no outer *testing.T; run through TestE2E_sqlite_store_leases_and_recovery_session_leases_and_crash_recovery")
	}
	s.root = filepath.Join(base, "home")
	s.layout = home.New(s.root)
	return home.Ensure(s.layout)
}

func (s *slcrState) spawn(role string) (*slcrProc, error) {
	p, err := slcrSpawn(s.root, role)
	if err != nil {
		return nil, err
	}
	s.cleanups = append(s.cleanups, p.kill)
	return p, nil
}

func (s *slcrState) open() (*sqlite.Store, error) {
	st, err := sqlite.OpenHome(slcrBG, s.layout, sqlite.WithKind("e2e-parent"))
	if err != nil {
		return nil, err
	}
	s.cleanups = append(s.cleanups, func() { _ = st.Close(slcrBG) })
	return st, nil
}

// fresh is a connection to dsh.db sharing nothing with any store.
func (s *slcrState) fresh() (*sql.DB, error) { return sql.Open("sqlite", s.layout.DB()) }

// persisted is every row of session id, in seq order, as a fresh connection reads it.
func (s *slcrState) persisted(id string) ([]slcrRow, error) {
	db, err := s.fresh()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rs, err := db.Query(`SELECT events.seq, events.type, events.data FROM events
		JOIN sessions ON events.session = sessions.uid WHERE sessions.id = ? ORDER BY events.seq`, id)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []slcrRow
	for want := int64(0); rs.Next(); want++ {
		var (
			seq int64
			r   slcrRow
		)
		if err := rs.Scan(&seq, &r.typ, &r.data); err != nil {
			return nil, err
		}
		if seq != want {
			return nil, fmt.Errorf("row %d has seq %d: the log has a gap", want, seq)
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

func (s *slcrState) nextSeq(id string) (int64, error) {
	db, err := s.fresh()
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var n int64
	err = db.QueryRow(`SELECT next_seq FROM sessions WHERE id = ?`, id).Scan(&n)
	return n, err
}

// --- scenario 1 and 2: leases ---

func (s *slcrState) aHolds(id string) error {
	if id != slcrSession {
		return fmt.Errorf("the children hold session %q, not %q", slcrSession, id)
	}
	if err := s.newHome(); err != nil {
		return err
	}
	a, err := s.spawn("hold")
	if err != nil {
		return err
	}
	s.a = a
	line, err := a.expect("holding")
	if err != nil {
		return err
	}
	if slcrField(line, "epoch") != "1" {
		return fmt.Errorf("A reported %q; want epoch 1", line)
	}
	s.aInst, s.aPID = slcrField(line, "instance"), slcrField(line, "pid")
	if s.aPID != strconv.Itoa(a.cmd.Process.Pid) {
		return fmt.Errorf("A reported pid %s, its process is %d", s.aPID, a.cmd.Process.Pid)
	}
	return nil
}

// bReady starts B and has it open the store (and run its open-time sweep).
func (s *slcrState) bReady() error {
	b, err := s.spawn("open")
	if err != nil {
		return err
	}
	s.b = b
	line, err := b.expect("ready")
	if err != nil {
		return err
	}
	s.bInst = slcrField(line, "instance")
	if s.bInst == "" || s.bInst == s.aInst {
		return fmt.Errorf("B reported %q", line)
	}
	return nil
}

func (s *slcrState) bOpens(id string) error {
	if id != slcrSession {
		return fmt.Errorf("B opens session %q, not %q", slcrSession, id)
	}
	if s.b == nil {
		if err := s.bReady(); err != nil {
			return err
		}
	}
	if err := s.b.send("go"); err != nil {
		return err
	}
	line, err := s.b.expect("result")
	s.bResult = line
	return err
}

// bRefused: B's OpenWrite was refused with exactly code, as production emits it,
// naming A's live instance, pid and lease start.
func (s *slcrState) bRefused(code string) error {
	line := s.bResult
	if !strings.HasPrefix(line, "result refused") || slcrField(line, "code") != code || slcrField(line, "is-owned") != "true" {
		return fmt.Errorf("process B: %q; want refused with %s", line, code)
	}
	if slcrField(line, "holder") != s.aInst || slcrField(line, "pid") != s.aPID || slcrField(line, "since") == "" {
		return fmt.Errorf("process B was refused naming %s pid %s; want A (%s, pid %s)",
			slcrField(line, "holder"), slcrField(line, "pid"), s.aInst, s.aPID)
	}
	return nil
}

// refusedInProcess: a second writer opened in this (third) process meets the
// same refusal, with the same code, naming A.
func (s *slcrState) refusedInProcess(code string) error {
	st, err := s.open()
	if err != nil {
		return err
	}
	_, err = st.OpenWrite(slcrBG, slcrSession, store.OpenOptions{})
	var owned *store.OwnedError
	if err == nil || slcrWireCode(err) != code || !errors.Is(err, store.ErrOwned) ||
		!errors.As(err, &owned) || owned.Holder.Instance != s.aInst || strconv.Itoa(owned.Holder.PID) != s.aPID {
		return fmt.Errorf("a second writer in this process = %v; want %s naming A", err, code)
	}
	return nil
}

// onlyAs: the log holds A's three events and nothing else, and A still holds it.
func (s *slcrState) onlyAs(id string) error {
	rows, err := s.persisted(id)
	if err != nil {
		return err
	}
	if len(rows) != 3 {
		return fmt.Errorf("the log of %s holds %d events; want only A's 3", id, len(rows))
	}
	for i, r := range rows {
		if r.typ != event.ToolCall.Name() || !strings.Contains(r.data, fmt.Sprintf(`"a%d"`, i)) {
			return fmt.Errorf("row %d is %s %s; want A's tool call a%d", i, r.typ, r.data, i)
		}
	}
	if next, err := s.nextSeq(id); err != nil || next != 3 {
		return fmt.Errorf("next seq %d (%v); want 3", next, err)
	}
	st, err := s.open()
	if err != nil {
		return err
	}
	sum, err := st.Stat(slcrBG, id)
	if err != nil || sum.LeaseEpoch != 1 || sum.Holder == nil || sum.Holder.Instance != s.aInst {
		return fmt.Errorf("after the refusal: %+v, %v; want A's lease at epoch 1", sum, err)
	}
	return nil
}

func (s *slcrState) aStillAppends(id string) error {
	if err := s.a.send("append"); err != nil {
		return err
	}
	if line, err := s.a.expect("append"); err != nil || line != "append ok" {
		return fmt.Errorf("A after the refusal: %q, %v", line, err)
	}
	rows, err := s.persisted(id)
	if err != nil {
		return err
	}
	if len(rows) != 4 || !strings.Contains(rows[3].data, `"a3"`) {
		return fmt.Errorf("after A's append the log holds %d rows; want A's 4", len(rows))
	}
	return nil
}

func (s *slcrState) lockFile(inst string) string { return filepath.Join(s.layout.Run(), inst+".lock") }

func (s *slcrState) hostsRows(inst string) (int64, error) {
	db, err := s.fresh()
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var n int64
	err = db.QueryRow(`SELECT count(*) FROM hosts WHERE instance = ?`, inst).Scan(&n)
	return n, err
}

func (s *slcrState) bOpenedWhileALives() error {
	if err := s.bReady(); err != nil {
		return err
	}
	// B's open-time sweep left the live A alone.
	if _, err := os.Stat(s.lockFile(s.aInst)); err != nil {
		return fmt.Errorf("A's liveness lock file while A lives: %v", err)
	}
	if n, err := s.hostsRows(s.aInst); err != nil || n != 1 {
		return fmt.Errorf("A's hosts rows while A lives: %d, %v", n, err)
	}
	return nil
}

func (s *slcrState) killA() error {
	s.a.kill()
	if s.a.cmd.ProcessState == nil || s.a.cmd.ProcessState.Success() {
		return fmt.Errorf("A was not killed: %v", s.a.cmd.ProcessState)
	}
	return nil
}

func (s *slcrState) bHoldsAt(epoch int) error {
	if !strings.HasPrefix(s.bResult, "result opened") || slcrField(s.bResult, "epoch") != strconv.Itoa(epoch) {
		return fmt.Errorf("process B: %q; want opened at epoch %d", s.bResult, epoch)
	}
	st, err := s.open()
	if err != nil {
		return err
	}
	s.observer = st
	sum, err := st.Stat(slcrBG, slcrSession)
	if err != nil || sum.LeaseEpoch != uint64(epoch) || sum.Holder == nil || sum.Holder.Instance != s.bInst || sum.NextSeq != 3 {
		return fmt.Errorf("after the takeover: %+v, %v; want B (%s) at epoch %d over A's 3 events", sum, err, s.bInst, epoch)
	}
	return nil
}

// aGone: B's probe inside OpenWrite's write transaction acquired A's lock and
// removed its file and hosts row (B's open-time sweep ran while A lived).
func (s *slcrState) aGone() error {
	if _, err := os.Stat(s.lockFile(s.aInst)); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("A's lock file after the takeover: %v; want it gone", err)
	}
	if n, err := s.hostsRows(s.aInst); err != nil || n != 0 {
		return fmt.Errorf("A's hosts rows after the takeover: %d, %v; want none", n, err)
	}
	return nil
}

func (s *slcrState) staleRejected(code string) error {
	stale, err := sqlite.E2EStaleWriter(slcrBG, s.observer, slcrSession, s.aInst, 1)
	if err != nil {
		return err
	}
	rec, err := slcrCall(3, "a", time.Now().UnixMilli())
	if err != nil {
		return err
	}
	s.staleErr = stale.Append(slcrBG, store.Batch{First: 3, Records: []store.Record{rec}})
	s.staleFlushErr = stale.Flush(slcrBG)
	want := &store.Error{Code: store.Code(code)}
	if !errors.Is(s.staleErr, want) {
		return fmt.Errorf("an append with A's epoch = %v; want %s", s.staleErr, code)
	}
	if !errors.Is(s.staleFlushErr, want) {
		return fmt.Errorf("a flush with A's epoch = %v; want %s", s.staleFlushErr, code)
	}
	return nil
}

func (s *slcrState) logHolds(n int) error {
	rows, err := s.persisted(slcrSession)
	if err != nil {
		return err
	}
	next, err := s.nextSeq(slcrSession)
	if err != nil {
		return err
	}
	if len(rows) != n || next != int64(n) {
		return fmt.Errorf("the log holds %d events, next seq %d; want %d", len(rows), next, n)
	}
	return nil
}

func (s *slcrState) bAppends(id string) error {
	if err := s.b.send("append"); err != nil {
		return err
	}
	if line, err := s.b.expect("append"); err != nil || line != "append ok" {
		return fmt.Errorf("B's append as the new holder: %q, %v", line, err)
	}
	rows, err := s.persisted(id)
	if err != nil {
		return err
	}
	if len(rows) != 4 || !strings.Contains(rows[3].data, `"b3"`) {
		return fmt.Errorf("after B's append the log holds %d rows; want B's event at seq 3", len(rows))
	}
	return nil
}

// --- scenario 3: crash recovery ---

func (s *slcrState) crashChild() error {
	if err := s.newHome(); err != nil {
		return err
	}
	c, err := s.spawn("crash")
	if err != nil {
		return err
	}
	if _, err := c.expect("crashing"); err != nil {
		return err
	}
	c.wait()
	if code := c.cmd.ProcessState.ExitCode(); code != slcrCrashExit {
		return fmt.Errorf("the child exited %d; want the crash exit %d:\n%s", code, slcrCrashExit, strings.Join(c.log, "\n"))
	}
	rows, err := s.persisted(slcrCrashID)
	if err != nil {
		return err
	}
	want := []string{event.TurnStartType.Name(), event.StepStartType.Name(), event.UserMessage.Name(),
		event.AssistantMessage.Name(), event.ToolCall.Name()}
	s.crashBefore = nil
	for _, r := range rows {
		s.crashBefore = append(s.crashBefore, r.typ)
	}
	if strings.Join(s.crashBefore, ",") != strings.Join(want, ",") {
		return fmt.Errorf("rows the crashed child left: %v; want %v", s.crashBefore, want)
	}
	return nil
}

func (s *slcrState) reopen() error {
	st, err := s.open()
	if err != nil {
		return err
	}
	s.reopened = st
	root := cordis.New()
	root.Loggers().ClearExporters()
	s.cleanups = append(s.cleanups, root.Dispose)
	svc := session.NewService(root, st, session.Config{})
	h, rep, err := svc.Open(slcrBG, nil, session.ID(slcrCrashID), session.OpenOptions{})
	if err != nil {
		return fmt.Errorf("Open after the crash: %w", err)
	}
	s.cleanups = append(s.cleanups, func() { _ = h.Close(slcrBG) })
	s.report = rep
	if h.Open().IsOpen() {
		return fmt.Errorf("brackets still open after recovery: %+v", h.Open())
	}
	s.rows, err = s.persisted(slcrCrashID)
	return err
}

func (s *slcrState) reportedInterrupted() error {
	if !s.report.Interrupted || len(s.report.Closers) == 0 {
		return fmt.Errorf("open report %+v; want interrupted with closers", s.report)
	}
	if n := len(s.report.Was.Calls); n != 1 || s.report.Was.Calls[0].ID != "c1" {
		return fmt.Errorf("the open calls as found: %+v; want c1", s.report.Was.Calls)
	}
	return nil
}

func (s *slcrState) balanced() error {
	if len(s.rows) <= len(s.crashBefore) {
		return fmt.Errorf("recovery appended nothing: %d rows", len(s.rows))
	}
	for i, typ := range s.crashBefore {
		if s.rows[i].typ != typ {
			return fmt.Errorf("row %d recorded before the crash changed: %s, then %s", i, typ, s.rows[i].typ)
		}
	}
	depth := map[string]int{}
	calls := map[string]int{}
	for _, r := range s.rows {
		switch r.typ {
		case event.TurnStartType.Name():
			depth["turn"]++
		case event.TurnEndType.Name():
			depth["turn"]--
		case event.StepStartType.Name():
			depth["step"]++
		case event.StepEndType.Name():
			depth["step"]--
		case event.ToolCall.Name():
			var p event.ToolCallPayload
			if err := json.Unmarshal([]byte(r.data), &p); err != nil {
				return err
			}
			calls[p.CallID]++
		case event.ToolResult.Name():
			for id := range calls {
				if strings.Contains(r.data, strconv.Quote(id)) {
					calls[id]--
				}
			}
		}
		for k, v := range depth {
			if v < 0 {
				return fmt.Errorf("a %s closes more than it opened", k)
			}
		}
	}
	for k, v := range depth {
		if v != 0 {
			return fmt.Errorf("%d %s bracket(s) left open: %v", v, k, s.types())
		}
	}
	for id, v := range calls {
		if v != 0 {
			return fmt.Errorf("tool call %s has %d unmatched record(s): %v", id, v, s.types())
		}
	}
	return nil
}

func (s *slcrState) types() []string {
	out := make([]string, len(s.rows))
	for i, r := range s.rows {
		out[i] = r.typ
	}
	return out
}

func (s *slcrState) turnClosedAs(reason string) error {
	for _, r := range s.rows[len(s.crashBefore):] {
		if r.typ != event.TurnEndType.Name() {
			continue
		}
		var end event.TurnEnd
		if err := json.Unmarshal([]byte(r.data), &end); err != nil {
			return err
		}
		if string(end.Reason.Kind) != reason || end.Reason.Kind != event.ReasonInterrupted || end.Reason.Cause != string(surface.CauseInterrupted) {
			return fmt.Errorf("turn/end reason %+v; want kind and cause %q", end.Reason, reason)
		}
		return nil
	}
	return fmt.Errorf("recovery wrote no turn/end: %v", s.types())
}

// stepClosedInterrupted: step/end carries no reason of its own (event.StepEnd is
// turn and step), so the cut step is closed as interrupted when its only
// step/end is one of the closers the recovering open appended, names the step
// the crash cut, and precedes the closer turn/end of reason interrupted
// (design.md, "The host dies mid-turn"; core-session.md DH-T-SESSION-13).
func (s *slcrState) stepClosedInterrupted() error {
	was := s.report.Was
	if !was.StepOpen || !was.TurnOpen || was.Turn != 1 || was.Step != 1 {
		return fmt.Errorf("brackets as found %+v; want turn 1 step 1 open", was)
	}
	closer := map[int]bool{}
	for _, r := range s.report.Closers {
		closer[int(r.Seq)] = true
	}
	stepAt, turnAt := -1, -1
	for i, r := range s.rows {
		switch r.typ {
		case event.StepEndType.Name():
			if stepAt >= 0 {
				return fmt.Errorf("two step/end rows: %v", s.types())
			}
			stepAt = i
			var end event.StepEnd
			if err := json.Unmarshal([]byte(r.data), &end); err != nil {
				return err
			}
			if end.Turn != was.Turn || end.Step != was.Step {
				return fmt.Errorf("step/end %+v; want the cut step %d.%d", end, was.Turn, was.Step)
			}
		case event.TurnEndType.Name():
			var end event.TurnEnd
			if err := json.Unmarshal([]byte(r.data), &end); err != nil {
				return err
			}
			if end.Reason.Kind == event.ReasonInterrupted {
				turnAt = i
			}
		}
	}
	switch {
	case stepAt < 0:
		return fmt.Errorf("no persisted step/end: %v", s.types())
	case stepAt < len(s.crashBefore) || !closer[stepAt]:
		return fmt.Errorf("step/end at seq %d is not a recovery closer (closers %v)", stepAt, s.report.Closers)
	case turnAt < 0 || !closer[turnAt] || turnAt <= stepAt:
		return fmt.Errorf("step/end at seq %d is not followed by the closer turn/end interrupted (at %d): %v", stepAt, turnAt, s.types())
	}
	return nil
}

func (s *slcrState) interruptedResult() error {
	for _, r := range s.rows[len(s.crashBefore):] {
		if r.typ == event.ToolResult.Name() && strings.Contains(r.data, `"c1"`) &&
			strings.Contains(r.data, string(surface.CodeToolOutcomeUnknown)) {
			return nil
		}
	}
	return fmt.Errorf("no interrupted (%s) result for c1 among %v", surface.CodeToolOutcomeUnknown, s.types())
}

func (s *slcrState) allDurable() error {
	sum, err := s.reopened.Stat(slcrBG, slcrCrashID)
	if err != nil {
		return err
	}
	if sum.Durable != sum.NextSeq || int(sum.NextSeq) != len(s.rows) || sum.LeaseEpoch != 2 {
		return fmt.Errorf("after recovery: %+v over %d rows; want all durable at epoch 2", sum, len(s.rows))
	}
	return nil
}

// --- scenario 4: checkpoint ---

var slcrErrDisk = errors.New("injected: disk full")

// slcrFaults wraps the real SQLite store; while fail is set, every writer's
// Flush returns slcrErrDisk without reaching the store.
type slcrFaults struct {
	store.Store
	fail    atomic.Bool
	flushes atomic.Int64
}

func (f *slcrFaults) Create(ctx context.Context, h store.Header, o store.CreateOptions) (store.Writer, error) {
	w, err := f.Store.Create(ctx, h, o)
	if err != nil {
		return nil, err
	}
	return &slcrFaultWriter{Writer: w, f: f}, nil
}

type slcrFaultWriter struct {
	store.Writer
	f *slcrFaults
}

func (w *slcrFaultWriter) Flush(ctx context.Context) error {
	w.f.flushes.Add(1)
	if w.f.fail.Load() {
		return slcrErrDisk
	}
	return w.Writer.Flush(ctx)
}

func (s *slcrState) appendDraft(d event.Draft, err error) error {
	if err != nil {
		return err
	}
	_, err = s.handle.Append(slcrBG, d)
	return err
}

func (s *slcrState) faultyStore() error {
	if err := s.newHome(); err != nil {
		return err
	}
	st, err := s.open()
	if err != nil {
		return err
	}
	s.faults = &slcrFaults{Store: st}
	root := cordis.New()
	root.Loggers().ClearExporters()
	s.cleanups = append(s.cleanups, root.Dispose)
	h, err := session.NewService(root, s.faults, session.Config{}).Create(slcrBG, nil, session.CreateOptions{Header: session.Header{ID: "cp"}})
	if err != nil {
		return err
	}
	s.handle = h
	s.cleanups = append(s.cleanups, func() { s.faults.fail.Store(false); _ = h.Close(slcrBG) })
	if err := s.appendDraft(event.TurnStartType.Draft(event.TurnStart{Turn: 1})); err != nil {
		return err
	}
	if err := s.appendDraft(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1})); err != nil {
		return err
	}
	s.faults.fail.Store(true)
	return nil
}

func (s *slcrState) checkpointBeforeBody() error {
	before := s.faults.flushes.Load()
	s.toolErr = checkpoint.RunTool(slcrBG, checkpoint.Mounted(nil), s.handle, false, func(context.Context) error {
		s.calls++
		return nil
	})
	if s.faults.flushes.Load() == before {
		return errors.New("the tool-body checkpoint did not flush")
	}
	return nil
}

func (s *slcrState) callsStay(n int) error {
	if s.calls != n {
		return fmt.Errorf("the body ran %d times behind a failed flush; want %d", s.calls, n)
	}
	return nil
}

func (s *slcrState) toolCheckpointFails(code string) error {
	var ce *checkpoint.Error
	if !errors.As(s.toolErr, &ce) || ce.Point != checkpoint.ToolBody || string(ce.Code) != code {
		return fmt.Errorf("tool-body checkpoint = %v; want a %s checkpoint.Error at tool-body", s.toolErr, code)
	}
	if !errors.Is(s.toolErr, checkpoint.ErrNotStarted) || !errors.Is(s.toolErr, slcrErrDisk) {
		return fmt.Errorf("tool-body checkpoint = %v; want tools/not-started wrapping the injected error", s.toolErr)
	}
	return nil
}

func (s *slcrState) stepEndFails() error {
	if err := s.appendDraft(event.StepEndType.Draft(event.StepEnd{Turn: 1, Step: 1})); err != nil {
		return err
	}
	s.stepEndErr = checkpoint.Before(slcrBG, nil, s.handle, checkpoint.StepEnd)
	var ce *checkpoint.Error
	if !errors.As(s.stepEndErr, &ce) || ce.Point != checkpoint.StepEnd ||
		!errors.Is(s.stepEndErr, checkpoint.ErrFailed) || !errors.Is(s.stepEndErr, slcrErrDisk) {
		return fmt.Errorf("step-end checkpoint = %v; want session/checkpoint-failed wrapping the injected error", s.stepEndErr)
	}
	if d := s.handle.Durable(); d == s.handle.Len() {
		return fmt.Errorf("durable offset %d reached the log's end %d through a failed flush", d, s.handle.Len())
	}
	return nil
}

func InitializeScenario_sqlite_store_leases_and_recovery_session_leases_and_crash_recovery(ctx *godog.ScenarioContext) {
	s := &slcrState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = slcrState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		for i := len(s.cleanups) - 1; i >= 0; i-- {
			s.cleanups[i]()
		}
		return c, nil
	})

	ctx.Step(`^process A holding the lease on session "([^"]*)" in a temp home$`, s.aHolds)
	ctx.Step(`^process B opens "([^"]*)" for writing$`, s.bOpens)
	ctx.Step(`^B is refused with "([^"]*)" naming process A$`, s.bRefused)
	ctx.Step(`^a second writer in another store instance is refused with "([^"]*)" naming process A$`, s.refusedInProcess)
	ctx.Step(`^the log of "([^"]*)" contains only A's events$`, s.onlyAs)
	ctx.Step(`^A still appends to "([^"]*)"$`, s.aStillAppends)

	ctx.Step(`^process B has opened the store while A lives$`, s.bOpenedWhileALives)
	ctx.Step(`^process A is killed$`, s.killA)
	ctx.Step(`^B holds "S" at epoch (\d+)$`, s.bHoldsAt)
	ctx.Step(`^A's liveness lock file and hosts row are gone$`, s.aGone)
	ctx.Step(`^a stale append carrying A's epoch is rejected with "([^"]*)"$`, s.staleRejected)
	ctx.Step(`^the log of "S" still holds (\d+) events$`, s.logHolds)
	ctx.Step(`^B appends to "([^"]*)"$`, s.bAppends)

	ctx.Step(`^a child that appends turn/start, step/start and a tool call then exits before flush$`, s.crashChild)
	ctx.Step(`^the store is reopened for write$`, s.reopen)
	ctx.Step(`^the session is reported interrupted$`, s.reportedInterrupted)
	ctx.Step(`^the persisted rows contain balanced brackets$`, s.balanced)
	ctx.Step(`^the turn is closed as "([^"]*)"$`, s.turnClosedAs)
	ctx.Step(`^the cut step's persisted step/end is a closer of the interrupted turn$`, s.stepClosedInterrupted)
	ctx.Step(`^the tool call has an interrupted result$`, s.interruptedResult)
	ctx.Step(`^every persisted row is durable$`, s.allDurable)

	ctx.Step(`^a store whose flush returns an injected error$`, s.faultyStore)
	ctx.Step(`^a checkpoint precedes a tool body$`, s.checkpointBeforeBody)
	ctx.Step(`^the body's call counter stays (\d+)$`, s.callsStay)
	ctx.Step(`^the tool-body checkpoint fails with "([^"]*)"$`, s.toolCheckpointFails)
	ctx.Step(`^the step ends with a checkpoint failure$`, s.stepEndFails)
}

func TestE2E_sqlite_store_leases_and_recovery_session_leases_and_crash_recovery(t *testing.T) {
	slcrOuterT = t
	defer func() { slcrOuterT = nil }()
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_sqlite_store_leases_and_recovery_session_leases_and_crash_recovery,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"sqlite_store_leases_and_recovery_session_leases_and_crash_recovery.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
