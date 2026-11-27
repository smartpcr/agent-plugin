//go:build scenario

package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
	"github.com/smartpcr/agent-plugin/src/sdk/sdk"
)

// TestSDKDrivesBuiltBinaryOverStdio is the DH-E-SDK-1 scenario
// (docs/harness/areas/sdk-acp-typert.md; DH-D-SDK-4, -5, -6, -8; tech-spec sec.
// 4.5 and implementation-plan Stage 6.11: authored in DH-S-16, run in DH-S-17 by
// Stage 7.5 on Linux E1): a Go program on src/sdk/sdk drives the built binary
// over stdio through a full session on the signed-in Claude Code route.
//
// Given the built dsh, a scratch home whose sdk profile layer
// (DSH_SCENARIO_SDK1_LAYER) asks before writes, a workspace with one file and a
// program on the Go client with approval and question handlers, its process zone
// TZ=UTC,
// When it initializes with timeZone Asia/Tokyo, runs a prompt that writes and
// allows it, runs one that writes and rejects it, runs one whose model asks a
// question and answers "blue", cancels DSH_SCENARIO_SDK1_LONG_PROMPT with inbox
// discard, kills dsh mid-turn, starts a second host on the same home, resumes and
// reads history from 0,
// Then the clock context is in Asia/Tokyo, not UTC; each run result equals the
// last assistant message of its turn; the rejected write left the file unchanged
// with a refused result; the answer reaches the model and a duplicate answer to
// the same request is not applied; the cancel reply follows turn/end on the wire;
// the client sees end of file and the exit status; resume reports
// closedInterruptedTurn and history has no gap or duplicate seq; stdout held
// frames only (M2-C-10); and no dsh process remains.
// Cleanup: the home and workspace are removed by scenario.Run; a remaining
// process fails the scenario naming its pid.
//
// Inputs: DSH_SCENARIO_SDK1_LAYER, a layer template file ({workspace} expanded)
// whose policy asks before writes; DSH_SCENARIO_SDK1_LONG_PROMPT, a prompt whose
// turn runs for minutes; DSH_SCENARIO_CLAUDE_CODE_MODEL and _EFFORT
// (signedin_routes_test.go).
//
//dsh:scenario DH-E-SDK-1
func TestSDKDrivesBuiltBinaryOverStdio(t *testing.T) {
	scenario.Run(t, "DH-E-SDK-1", scenario.Needs{
		Bin: true, OS: []string{"linux"}, Deadline: 30 * time.Minute,
		Env: []string{"DSH_SCENARIO_SDK1_LAYER", "DSH_SCENARIO_SDK1_LONG_PROMPT", "DSH_SCENARIO_CLAUDE_CODE_MODEL", "DSH_SCENARIO_CLAUDE_CODE_EFFORT"},
	}, func(s *scenario.S) {
		ctx := s.Context()
		note := filepath.Join(s.Workspace, "note.txt")
		if err := os.WriteFile(note, []byte("original\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		profileLayer(t, s.Home, "sdk", layerInput(t, "DSH_SCENARIO_SDK1_LAYER", "a layer asking before writes", map[string]string{"workspace": s.Workspace}))
		route := &protocol.ModelRoute{Provider: claudeCodeRoute.ID, Model: claudeCodeRoute.model(t), ReasoningEffort: claudeCodeRoute.effort(t)}
		long := need(t, "DSH_SCENARIO_SDK1_LONG_PROMPT", "a prompt whose turn runs for minutes")

		var approve atomic.Value
		approve.Store("allowed-once")
		var asked atomic.Int32
		h, p := sdk1Start(t, s, sdk.Handlers{
			Approval: func(context.Context, protocol.ApprovalRequest) protocol.ApprovalResult {
				return protocol.ApprovalResult{Outcome: approve.Load().(string)}
			},
			Question: func(_ context.Context, r protocol.QuestionRequest) (protocol.QuestionResult, error) {
				asked.Add(1)
				var res protocol.QuestionResult
				for _, q := range r.Questions {
					res.Answers = append(res.Answers, protocol.Answer{ID: q.ID, Custom: "blue"})
				}
				return res, nil
			},
		})
		sess, err := h.Create(ctx, sdk.CreateOptions{Cwd: s.Workspace, Model: route})
		if err != nil {
			t.Fatal(err)
		}
		run := func(text string) sdk.RunResult {
			t.Helper()
			r, err := sess.Run(ctx, sdk.Input{Text: text})
			if err != nil {
				t.Fatalf("run %q: %v\nstderr:\n%s", text, err, p.stderr.String())
			}
			return r
		}

		allowed := run("Append the line 'allowed' to note.txt with your file tool, then say done.")
		approve.Store("rejected")
		before, _ := os.ReadFile(note)
		rejected := run("Overwrite note.txt with the single word 'rejected' using your file tool.")
		if after, _ := os.ReadFile(note); !bytes.Equal(before, after) {
			t.Errorf("the rejected write changed note.txt: %q, want %q", after, before)
		}
		asked0 := asked.Load()
		answered := run("Ask me with your question tool which colour I prefer, then repeat my answer.")
		if asked.Load() == asked0 {
			t.Fatal("the model asked no question (question/ask)")
		}
		p.duplicateAnswer(t)

		// The cancel: its reply must follow the turn's turn/end on the wire. The
		// cursor is taken before the prompt, so the step/start awaited is this
		// turn's and not one of an earlier run.
		cursor := p.inbound.lines()
		if _, err := sess.Prompt(ctx, sdk.Input{Text: long}); err != nil {
			t.Fatal(err)
		}
		p.awaitEvent(t, "step/start", cursor)
		mark := p.inbound.lines()
		if err := sess.Cancel(ctx, sdk.CancelOptions{Inbox: "discard"}); err != nil {
			t.Fatalf("cancel: %v", err)
		}
		p.cancelAfterTurnEnd(t, mark)

		// The kill mid-turn, once this turn's own step has started.
		cursor = p.inbound.lines()
		if _, err := sess.Prompt(ctx, sdk.Input{Text: long}); err != nil {
			t.Fatal(err)
		}
		p.awaitEvent(t, "step/start", cursor)
		pid := p.cmd.Process.Pid
		if err := p.cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-h.Done():
		case <-time.After(30 * time.Second):
			t.Fatal("the client saw no end of the connection 30s after the kill")
		}
		if err := h.Err(); err == nil || !strings.Contains(err.Error(), "EOF") {
			t.Errorf("the client's end is %v, want end of file", err)
		}
		if err := p.cmd.Wait(); err == nil || p.cmd.ProcessState.Success() {
			t.Errorf("the killed host's exit status is %v, want a failure", p.cmd.ProcessState)
		}
		_ = h.Close(context.WithoutCancel(ctx))
		goneWithin(t, pid, "the killed dsh", 10*time.Second)
		p.framesOnly(t)

		// A second host on the same home resumes and reads history from 0.
		evs, closed, pid2 := sdk1Resume(t, s, sess.ID)
		if !closed {
			t.Error("resume did not report closedInterruptedTurn")
		}
		for i := 1; i < len(evs); i++ {
			if evs[i].Seq != evs[i-1].Seq+1 {
				t.Errorf("history seq %d follows %d: a gap or a duplicate", evs[i].Seq, evs[i-1].Seq)
			}
		}
		if req := ofType(evs, "request/context"); !sdk1Zoned(t, req) {
			t.Error("no clock context of the first request names Asia/Tokyo")
		}
		for _, r := range []sdk.RunResult{allowed, rejected, answered} {
			if got := lastAssistantText(t, inRun(evs, r.FirstSeq, r.LastSeq)); got != r.FinalResponse {
				t.Errorf("run %s: result %q, want the turn's last assistant message %q", r.MessageID, r.FinalResponse, got)
			}
		}
		if res := ofType(inRun(evs, rejected.FirstSeq, rejected.LastSeq), "tool/result"); !anyHolds(res, "reject", "refus", "denied") {
			t.Error("the rejected write left no refused tool/result in the log")
		}
		if !anyHolds(ofType(inRun(evs, answered.FirstSeq, answered.LastSeq), "assistant/message", "tool/call"), "blue") {
			t.Error("no assistant message or tool input after the question holds the answer \"blue\"")
		}
		goneWithin(t, pid2, "the second dsh", 10*time.Second)
	})
}

// sdk1Pipes are the first host's process and both directions of its stdio.
type sdk1Pipes struct {
	cmd               *exec.Cmd
	in                io.Writer
	inbound, outbound *frameLog
	stderr            *frameLog
}

// sdk1Start starts dsh --profile sdk itself (TZ=UTC), so both directions of its
// stdio are recorded, and connects the Go client to it (sdk.Connect) with
// timeZone Asia/Tokyo.
func sdk1Start(t *testing.T, s *scenario.S, handlers sdk.Handlers) (*sdk.Harness, *sdk1Pipes) {
	t.Helper()
	p := &sdk1Pipes{inbound: &frameLog{}, outbound: &frameLog{}, stderr: &frameLog{}}
	p.cmd = exec.Command(s.Bin, "--home", s.Home, "--profile", "sdk")
	p.cmd.Dir, p.cmd.Env, p.cmd.Stderr = s.Workspace, append(os.Environ(), "TZ=UTC", "DSH_HOME="+s.Home), p.stderr
	stdin, err := p.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.cmd.Process.Kill() })
	p.in = &lockedWriter{w: io.MultiWriter(stdin, p.outbound)}
	h, err := sdk.Connect(s.Context(), sdk.Transport{In: io.TeeReader(stdout, p.inbound), Out: writeCloser{p.in, stdin}},
		sdk.Options{TimeZone: "Asia/Tokyo", Handlers: handlers, ClientInfo: protocol.Implementation{Name: "dsh-sdk1", Version: "1"}})
	if err != nil {
		t.Fatalf("connect: %v\nstderr:\n%s", err, p.stderr.String())
	}
	return h, p
}

// duplicateAnswer sends the answer of the last question/ask again, to the same
// request id: the host must not apply it, and stays up and answering.
func (p *sdk1Pipes) duplicateAnswer(t *testing.T) {
	t.Helper()
	var ask, answer json.RawMessage
	for _, f := range p.inbound.frames() {
		if f.Method == "question/ask" {
			ask = f.ID
		}
	}
	for _, f := range p.outbound.frames() {
		if ask != nil && bytes.Equal(f.ID, ask) && f.Method == "" {
			answer = f.Raw
		}
	}
	if answer == nil {
		t.Fatal("no answer to question/ask on the wire")
	}
	if _, err := p.in.Write(append(bytes.Clone(answer), '\n')); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	if p.cmd.ProcessState != nil {
		t.Fatal("the host exited after a duplicate answer")
	}
	t.Logf("host diagnostics after the duplicate answer:\n%s", p.stderr.String())
}

// awaitEvent waits for a session.event of typ on the wire after line from.
func (p *sdk1Pipes) awaitEvent(t *testing.T, typ string, from int) {
	t.Helper()
	for end := time.Now().Add(5 * time.Minute); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		for _, f := range p.inbound.frames()[from:] {
			if f.eventType() == typ {
				return
			}
		}
	}
	t.Fatalf("no %s event within 5m\nstderr:\n%s", typ, p.stderr.String())
}

// cancelAfterTurnEnd checks, among the frames read since line from, that the
// session/cancel reply follows the turn/end event.
func (p *sdk1Pipes) cancelAfterTurnEnd(t *testing.T, from int) {
	t.Helper()
	var id json.RawMessage
	for _, f := range p.outbound.frames() {
		if f.Method == "session/cancel" {
			id = f.ID
		}
	}
	end, reply := -1, -1
	for i, f := range p.inbound.frames()[from:] {
		if f.eventType() == "turn/end" && end < 0 {
			end = i
		}
		if f.Method == "" && bytes.Equal(f.ID, id) {
			reply = i
		}
	}
	if end < 0 || reply < end {
		t.Errorf("the cancel reply is frame %d and turn/end frame %d: the reply must follow turn/end", reply, end)
	}
}

// framesOnly fails when a line the host wrote to stdout is not a JSON-RPC frame.
func (p *sdk1Pipes) framesOnly(t *testing.T) {
	t.Helper()
	for i, f := range p.inbound.frames() {
		if !f.ok {
			t.Errorf("stdout line %d is not a frame (M2-C-10): %q", i+1, f.Raw)
		}
	}
}

// sdk1Resume starts a second host on the home, resumes session over raw
// JSON-RPC (sdk.Session does not expose closedInterruptedTurn), reads its
// history from 0 and closes; it returns the events, closedInterruptedTurn and
// the host's pid.
func sdk1Resume(t *testing.T, s *scenario.S, session string) ([]protocol.Event, bool, int) {
	t.Helper()
	ctx := s.Context()
	cmd := exec.Command(s.Bin, "--home", s.Home, "--profile", "sdk")
	cmd.Dir, cmd.Env = s.Workspace, append(os.Environ(), "TZ=UTC", "DSH_HOME="+s.Home)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	out := &frameLog{}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	c := jsonrpc.Serve(ctx, io.TeeReader(stdout, out), stdin, jsonrpc.Options{})
	var init protocol.InitializeResult
	var res protocol.SessionResumeResult
	if err := c.Call(ctx, protocol.InitializeMethod, protocol.InitializeParams{ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "dsh-sdk1", Version: "1"}, TimeZone: "Asia/Tokyo"}, &init); err != nil {
		t.Fatalf("initialize the second host: %v", err)
	}
	if err := c.Call(ctx, "session/resume", protocol.SessionResumeParams{SessionID: session}, &res); err != nil {
		t.Fatalf("resume: %v", err)
	}
	evs, err := historyPages(ctx, c.Call, session)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Call(ctx, "shutdown", protocol.Empty{}, nil)
	_ = stdin.Close()
	_ = cmd.Wait()
	(&sdk1Pipes{inbound: out}).framesOnly(t)
	return evs, res.ClosedInterruptedTurn, cmd.Process.Pid
}

// sdk1Zoned reports whether the first clock context among the request/context
// events names Asia/Tokyo (or its +09:00 offset) and not UTC.
func sdk1Zoned(t *testing.T, evs []protocol.Event) bool {
	t.Helper()
	for _, e := range evs {
		var c struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		decode(t, e, &c)
		if c.Key == "clock" {
			return (strings.Contains(c.Value, "Asia/Tokyo") || strings.Contains(c.Value, "+09:00")) && !strings.Contains(c.Value, "UTC")
		}
	}
	return false
}

// lastAssistantText is the text of the last assistant/message among evs.
func lastAssistantText(t *testing.T, evs []protocol.Event) string {
	t.Helper()
	msgs := ofType(evs, "assistant/message")
	if len(msgs) == 0 {
		return ""
	}
	var m wireSettlement
	decode(t, msgs[len(msgs)-1], &m)
	var text strings.Builder
	if m.Message != nil {
		for _, b := range m.Message.Blocks {
			if b.Kind == "text" && b.Text != nil {
				text.WriteString(b.Text.Text)
			}
		}
	}
	return text.String()
}

// anyHolds reports whether the payload of one of evs holds one of words, case
// folded.
func anyHolds(evs []protocol.Event, words ...string) bool {
	for _, e := range evs {
		data := strings.ToLower(string(e.Data))
		for _, w := range words {
			if strings.Contains(data, w) {
				return true
			}
		}
	}
	return false
}

// frameLog records what crossed one direction of a stdio connection.
type frameLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *frameLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(b)
}

func (l *frameLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// lines is the number of complete lines recorded.
func (l *frameLog) lines() int { return strings.Count(l.String(), "\n") }

// frame is one recorded line, decoded as far as a JSON-RPC frame goes.
type frame struct {
	Raw    []byte
	ok     bool
	Method string          `json:"method"`
	ID     json.RawMessage `json:"id"`
	Params json.RawMessage `json:"params"`
}

// eventType is the event type of a session.event notification, else empty.
func (f frame) eventType() string {
	if f.Method != "session.event" {
		return ""
	}
	var n struct {
		Event protocol.Event `json:"event"`
	}
	_ = json.Unmarshal(f.Params, &n)
	return n.Event.Type
}

// frames decodes every complete line recorded; ok marks a JSON-RPC 2.0 object.
func (l *frameLog) frames() []frame {
	s := l.String()
	var out []frame
	for _, line := range strings.SplitAfter(s[:strings.LastIndex(s, "\n")+1], "\n") {
		if line == "" {
			continue
		}
		f := frame{Raw: []byte(strings.TrimSuffix(line, "\n"))}
		var head struct {
			Version string `json:"jsonrpc"`
		}
		f.ok = json.Unmarshal(f.Raw, &head) == nil && head.Version == "2.0" && json.Unmarshal(f.Raw, &f) == nil
		out = append(out, f)
	}
	return out
}

// lockedWriter serializes writes, so a frame the scenario writes never
// interleaves with one the client writes.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

// writeCloser writes through w and closes c.
type writeCloser struct {
	io.Writer
	c io.Closer
}

func (w writeCloser) Close() error { return w.c.Close() }
