package sqlite_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// The crash children (DH-T-STORE-5, DH-T-STORE-8) are TestCrashChild with a role:
//
//   - turn: through the session core on the SQLite store, appends turn/start,
//     step/start, a user message, an assistant message asking for tool call c1
//     and the tool/call record of c1; the crash hook (sqlite.SetAfterAppend)
//     reports "crashing id=<session>" and exits the process right after the
//     tool/call commits, before any flush or close.
//   - loop: holds session "s" (creating it, or taking the dead previous child's
//     lease) and appends and flushes one event at a time, reporting "flushed
//     next=<n>" after each flush, until killed.
const (
	crashChildEnv = "DSH_SQLITE_CRASH_CHILD"
	crashRoleEnv  = "DSH_SQLITE_CRASH_ROLE"
	crashExit     = 3
)

func TestCrashChild(t *testing.T) {
	path := os.Getenv(crashChildEnv)
	if path == "" {
		t.Skip("the child of the crash tests")
	}
	s, err := sqlite.Open(bg, path, sqlite.WithKind("child"))
	if err != nil {
		t.Fatal(err)
	}
	switch os.Getenv(crashRoleEnv) {
	case "turn":
		crashTurn(t, s)
	case "loop":
		appendLoop(t, s)
	default:
		t.Fatalf("unknown role %q", os.Getenv(crashRoleEnv))
	}
	t.Fatal("the child was not stopped")
}

func crashTurn(t *testing.T, s *sqlite.Store) {
	root := cordis.New()
	root.Loggers().ClearExporters()
	svc := session.NewService(root, s, session.Config{})
	h, err := svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: "crash"}})
	if err != nil {
		t.Fatal(err)
	}
	sqlite.SetAfterAppend(func(id string, b store.Batch) {
		for _, r := range b.Records {
			if r.Type == event.ToolCall.Name() {
				fmt.Printf("crashing id=%s\n", id)
				os.Exit(crashExit)
			}
		}
	})
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("search"))
	if err != nil {
		t.Fatal(err)
	}
	asked, err := vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, vocab.ToolUseBlock("c1", "grep", json.RawMessage(`{"q":"x"}`)))
	if err != nil {
		t.Fatal(err)
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
			t.Fatal(err)
		}
		if _, err := h.Append(bg, d); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("the crash hook did not fire on the tool/call")
}

func appendLoop(t *testing.T, s *sqlite.Store) {
	w, err := s.OpenWrite(bg, "s", store.OpenOptions{})
	if err != nil {
		if w, err = s.Create(bg, store.Header{ID: "s"}, store.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := s.Stat(bg, "s")
	if err != nil {
		t.Fatal(err)
	}
	next := sum.NextSeq
	if next > 0 {
		// Keep the event times of the previous children's records.
		last, err := w.Read(bg, next-1, 1, store.ReadOptions{})
		if err != nil || len(last) != 1 {
			t.Fatalf("last record: %v, %v", last, err)
		}
		ms = max(ms, last[0].Time)
	}
	for ; ; next++ {
		if err := w.Append(bg, store.Batch{First: next, Records: []store.Record{call(t, next)}}); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(bg); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("flushed next=%d\n", next+1)
	}
}

func startCrashChild(t *testing.T, path, role string) *child {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashChild$", "-test.count=1", "-test.v")
	cmd.Env = append(childEnviron(), crashChildEnv+"="+path, crashRoleEnv+"="+role)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c := &child{t: t, cmd: cmd, out: bufio.NewScanner(out)}
	t.Cleanup(func() { stop(cmd) })
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return c
}

// storedTypes is the type of every row of session id, in seq order, read by a
// connection that shares nothing with any store.
func storedTypes(t *testing.T, path, id string) ([]string, []string) {
	t.Helper()
	rs, err := fresh(t, path).Query(`SELECT events.seq, events.type, events.data FROM events
		JOIN sessions ON events.session = sessions.uid WHERE sessions.id = ? ORDER BY events.seq`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var types, data []string
	for want := int64(0); rs.Next(); want++ {
		var (
			seq     int64
			typ, dt string
		)
		if err := rs.Scan(&seq, &typ, &dt); err != nil {
			t.Fatal(err)
		}
		if seq != want {
			t.Fatalf("row %d has seq %d: the log has a gap", want, seq)
		}
		types, data = append(types, typ), append(data, dt)
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return types, data
}

// DH-T-STORE-8 / DH-T-STORE-9 / TC-07: a child appends turn/start, step/start
// and a tool call through the session core and exits between the tool/call's
// append and any flush. Reopening the session for write takes the dead child's
// lease, appends the closers (an outcome-unknown result for the call, step/end,
// turn/end interrupted) with the new epoch's end seed as its first batch, and
// makes them durable before the session is published: the persisted rows are
// balanced.
func TestCrashBetweenAppendAndFlushRecoversHonestly(t *testing.T) {
	path := dbPath(t)
	c := startCrashChild(t, path, "turn")
	line := c.expect("crashing")
	id := field(t, line, "id")
	if err := c.cmd.Wait(); err == nil || c.cmd.ProcessState.ExitCode() != crashExit {
		t.Fatalf("the child exited with %v; want the crash hook's exit %d:\n%s", err, crashExit, strings.Join(c.log, "\n"))
	}
	c.cmd.Process = nil
	before, _ := storedTypes(t, path, id)
	wantBefore := []string{event.TurnStartType.Name(), event.StepStartType.Name(), event.UserMessage.Name(),
		event.AssistantMessage.Name(), event.ToolCall.Name()}
	if !slices.Equal(before, wantBefore) {
		t.Fatalf("rows the crashed child left: %v; want %v", before, wantBefore)
	}

	s := open(t, path)
	root := cordis.New()
	root.Loggers().ClearExporters()
	t.Cleanup(root.Dispose)
	svc := session.NewService(root, s, session.Config{})
	h, rep, err := svc.Open(bg, nil, session.ID(id), session.OpenOptions{})
	if err != nil {
		t.Fatalf("Open after the crash: %v", err)
	}
	defer h.Close(bg)
	if !rep.Interrupted || len(rep.Closers) != 4 || h.Open().IsOpen() {
		t.Fatalf("open report %+v, brackets %+v", rep, h.Open())
	}
	if n := len(rep.Was.Calls); n != 1 || !rep.Was.Calls[0].Started || rep.Was.Calls[0].ID != "c1" {
		t.Fatalf("the open call as found: %+v", rep.Was.Calls)
	}

	types, data := storedTypes(t, path, id)
	want := append(slices.Clone(wantBefore), event.ToolResult.Name(), event.StepEndType.Name(),
		event.TurnEndType.Name(), event.EndSeed.Name())
	if !slices.Equal(types, want) {
		t.Fatalf("persisted rows %v; want %v", types, want)
	}
	if !strings.Contains(data[5], string(surface.CodeToolOutcomeUnknown)) || !strings.Contains(data[5], `"c1"`) {
		t.Fatalf("the closer of c1: %s; want an outcome-unknown result", data[5])
	}
	var end event.TurnEnd
	if err := json.Unmarshal([]byte(data[7]), &end); err != nil || end.Reason.Kind != event.ReasonInterrupted {
		t.Fatalf("turn/end %s (%v); want reason interrupted", data[7], err)
	}
	var seed event.EndSeedPayload
	if err := json.Unmarshal([]byte(data[8]), &seed); err != nil || seed.Epoch != 2 {
		t.Fatalf("end seed %s (%v); want epoch 2", data[8], err)
	}
	sum, err := s.Stat(bg, id)
	if err != nil || sum.LeaseEpoch != 2 || sum.Durable != sum.NextSeq || sum.NextSeq != 9 {
		t.Fatalf("after recovery: %+v, %v; want 9 durable rows at epoch 2", sum, err)
	}
	// A read-only observer of the recovered log finds nothing open.
	rs, release, err := svc.OpenRead(bg, session.ID(id))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if rs.Open().IsOpen() || rs.Len() != 9 {
		t.Fatalf("read-only view after recovery: open %+v, len %d", rs.Open(), rs.Len())
	}
}

// DH-T-STORE-5 / TC-07: a child appending and flushing one event at a time is
// killed at a random point, five times over on one session, each child taking
// the dead one's lease; every reopen finds a contiguous log holding at least
// the last event a child reported flushed.
func TestKilledAppenderLeavesContiguousLog(t *testing.T) {
	path := dbPath(t)
	rnd := rand.New(rand.NewPCG(rand.Uint64(), 7))
	flushed := int64(0)
	for round := 0; round < 5; round++ {
		c := startCrashChild(t, path, "loop")
		for n := 1 + rnd.IntN(20); n > 0; n-- {
			v, err := strconv.ParseInt(field(t, c.expect("flushed"), "next"), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			flushed = v
		}
		c.kill()
		types, _ := storedTypes(t, path, "s")
		if int64(len(types)) < flushed {
			t.Fatalf("round %d: %d rows after the kill; the child reported %d flushed", round, len(types), flushed)
		}
		_, next := countEvents(t, fresh(t, path), "s")
		if next != int64(len(types)) {
			t.Fatalf("round %d: next seq %d over %d contiguous rows", round, next, len(types))
		}
	}
	sum, err := open(t, path).Stat(bg, "s")
	if err != nil || sum.LeaseEpoch != 5 {
		t.Fatalf("after five children: %+v, %v; want the epoch raised by each takeover", sum, err)
	}
}
