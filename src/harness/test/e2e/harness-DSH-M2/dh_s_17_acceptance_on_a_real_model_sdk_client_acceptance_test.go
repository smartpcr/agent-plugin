//go:build e2e

package e2e

// Step definitions of
// dh_s_17_acceptance_on_a_real_model_sdk_client_acceptance.feature (DH-S-17
// Stage 7.5, DH-E-SDK-1, TC-20s, M2-C-10).
//
//   - Fixture proof: the testhook dsh named by DSH_BIN runs as a child process
//     (`--profile sdk`) on a scratch home whose sdk layer serves route
//     "claude-code" from a loopback stand-in model, puts every write to approval
//     (tools.testkit-ask) and mounts the file tools on a scratch workspace. The Go
//     client (src/sdk/sdk) drives it over the child's stdio and answers the
//     host's approval/request; both directions are recorded, and after the close
//     the persisted history is read back from the home's SQLite store.
//   - Acceptance on the signed-in route (@lab-s17): the real scenario test runs
//     where the S17 inputs are set, and is skipped (never passed) elsewhere.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/sdkserver"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
	"github.com/smartpcr/agent-plugin/src/sdk/sdk"
)

const (
	scaRoute    = "claude-code"
	scaScenario = "DH-E-SDK-1"
	scaLiveTest = "TestSDKDrivesBuiltBinaryOverStdio"
	scaWithin   = 2 * time.Minute
	// scaLiveWithin bounds the live signed-in-route run (its scenario.Needs
	// Deadline is 30m).
	scaLiveWithin = 35 * time.Minute
)

// scaLiveInputs are the S17 machine's inputs of TestSDKDrivesBuiltBinaryOverStdio.
var scaLiveInputs = []string{"DSH_SCENARIO_SDK1_LAYER", "DSH_SCENARIO_SDK1_LONG_PROMPT", "DSH_SCENARIO_CLAUDE_CODE_MODEL", "DSH_SCENARIO_CLAUDE_CODE_EFFORT"}

// scaWrite is one write run of the stand-in model: its tool call writes content
// to note.txt, and closing is its closing text, which the run's result must be
// (not the text before the tool call).
type scaWrite struct{ call, content, closing string }

var scaWrites = map[string]scaWrite{
	"allowed":  {"toolu_scaa", "allowed", "The note now reads allowed."},
	"rejected": {"toolu_scar", "rejected", "The write was rejected."},
}

func (w scaWrite) turns() []string {
	args, _ := json.Marshal(map[string]string{"file_path": "note.txt", "content": w.content + "\n"})
	return []string{
		"event: text\ndata: {\"text\":\"I will write the note.\"}\n\n" +
			"event: tool\ndata: {\"id\":\"" + w.call + "\",\"name\":\"write\",\"args\":" + string(args) + "}\n\n" +
			"event: usage\ndata: {\"input\":10,\"output\":5}\n\n" +
			"event: finish\ndata: {\"reason\":\"tool_calls\"}\n\n",
		"event: text\ndata: {\"text\":" + strconv.Quote(w.closing) + "}\n\n" +
			"event: usage\ndata: {\"input\":20,\"output\":4}\n\n" +
			"event: finish\ndata: {\"reason\":\"stop\"}\n\n",
	}
}

var (
	scaOuterT *testing.T
	// scaTesthookBin is dsh built with the testhook tag by the suite's test
	// function; scaOperatorBin is DSH_BIN as the suite found it (the S17 build).
	scaTesthookBin string
	scaOperatorBin string
)

// scaModel is the loopback stand-in of route "claude-code": it answers the
// queued turns in request order and holds any later request open until the host
// drops it (the turn the client cancels).
type scaModel struct {
	srv      *httptest.Server
	stop     chan struct{}
	held     chan struct{}
	heldOnce sync.Once
	mu       sync.Mutex
	turns    []string
}

func newScaModel() *scaModel {
	m := &scaModel{stop: make(chan struct{}), held: make(chan struct{})}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		m.mu.Lock()
		next, ok := "", len(m.turns) > 0
		if ok {
			next, m.turns = m.turns[0], m.turns[1:]
		}
		m.mu.Unlock()
		if ok {
			_, _ = io.WriteString(w, next)
			return
		}
		_, _ = io.WriteString(w, "event: text\ndata: {\"text\":\"thinking\"}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		m.heldOnce.Do(func() { close(m.held) })
		select {
		case <-r.Context().Done():
		case <-m.stop:
		}
	}))
	return m
}

func (m *scaModel) queue(turns ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.turns = append(m.turns, turns...)
}

func (m *scaModel) close() {
	close(m.stop)
	m.srv.Close()
}

// scaWire records what crossed one direction of a stdio connection.
type scaWire struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *scaWire) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(b)
}

func (w *scaWire) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// scaFrame is one recorded line; ok marks a JSON-RPC 2.0 object.
type scaFrame struct {
	raw    string
	ok     bool
	Method string          `json:"method"`
	ID     json.RawMessage `json:"id"`
	Params json.RawMessage `json:"params"`
}

// eventType is the event type of a session.event notification, else empty.
func (f scaFrame) eventType() string {
	if f.Method != "session.event" {
		return ""
	}
	var n struct {
		Event protocol.Event `json:"event"`
	}
	_ = json.Unmarshal(f.Params, &n)
	return n.Event.Type
}

// frames decodes every complete line recorded.
func (w *scaWire) frames() []scaFrame {
	s := w.String()
	var out []scaFrame
	for _, line := range strings.Split(s[:strings.LastIndex(s, "\n")+1], "\n") {
		if line == "" {
			continue
		}
		f := scaFrame{raw: line}
		var head struct {
			Version string `json:"jsonrpc"`
		}
		f.ok = json.Unmarshal([]byte(line), &head) == nil && head.Version == "2.0" && json.Unmarshal([]byte(line), &f) == nil
		out = append(out, f)
	}
	return out
}

type scaWriteCloser struct {
	io.Writer
	io.Closer
}

// scaEvent is one exported log line.
type scaEvent struct {
	Seq  int64  `json:"seq"`
	Type string `json:"type"`
	Data string `json:"data"`
}

// scaRun is one write run: the result and note.txt read right after it.
type scaRun struct {
	write  scaWrite
	result sdk.RunResult
	note   string
}

type scaState struct {
	ctx    context.Context
	cancel context.CancelFunc

	restoreBin        func()
	model             *scaModel
	root, ws, note    string
	cmd               *exec.Cmd
	done              chan struct{}
	stderr            scaWire
	inbound, outbound scaWire
	h                 *sdk.Harness
	sess              *sdk.Session

	mu      sync.Mutex
	answers []string
	asked   []protocol.ApprovalRequest

	runs     []scaRun
	mark     int
	closeErr error
	log      []scaEvent
}

func (s *scaState) givenBin() error {
	if scaTesthookBin == "" {
		return errors.New("the testhook dsh was not built")
	}
	prev, had := os.LookupEnv(sdk.BinEnv)
	if err := os.Setenv(sdk.BinEnv, scaTesthookBin); err != nil {
		return err
	}
	s.restoreBin = func() {
		if had {
			_ = os.Setenv(sdk.BinEnv, prev)
		} else {
			_ = os.Unsetenv(sdk.BinEnv)
		}
	}
	return nil
}

func (s *scaState) givenHome() error {
	s.model = newScaModel()
	s.root, s.ws = temphome.New(scaOuterT).Root(), scaOuterT.TempDir()
	s.note = filepath.Join(s.ws, "note.txt")
	if err := os.WriteFile(s.note, []byte("original\n"), 0o600); err != nil {
		return err
	}
	temphome.Layer(scaOuterT, s.root, profile.SDK, "- insert:\n"+
		"    - id: route\n      name: llm.testkit-route\n      config:\n        id: "+scaRoute+"\n        url: "+strconv.Quote(s.model.srv.URL)+"\n"+
		"    - id: fs.local\n      name: fs.local\n      config:\n        lockDir: "+strconv.Quote(scaOuterT.TempDir())+"\n"+
		"    - id: wd\n      name: session.testkit-workdir\n      config:\n        root: "+strconv.Quote(s.ws)+"\n"+
		"    - id: sandbox.policy\n      name: sandbox.policy\n      config:\n        defaultMode: workspace-write\n"+
		"    - id: fs.ops\n      name: fs.ops\n    - id: fs.tools\n      name: fs.tools\n"+
		"    - id: ask\n      name: tools.testkit-ask\n      config:\n        tools: [write]\n")
	return nil
}

// whenStarts starts the host; the approval handler answers the approvals in
// order, one per answer (`"a" then "b"`), and refuses any further one.
func (s *scaState) whenStarts(answers string) error {
	for _, a := range strings.Split(answers, " then ") {
		s.answers = append(s.answers, strings.Trim(a, `"`))
	}
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 2*scaWithin)
	bin := os.Getenv(sdk.BinEnv)
	if bin == "" {
		return fmt.Errorf("%s is not set", sdk.BinEnv)
	}
	s.cmd = exec.Command(bin, "--home", s.root, "--profile", "sdk")
	s.cmd.Dir, s.cmd.Env, s.cmd.Stderr = s.ws, append(os.Environ(), "DSH_HOME="+s.root), &s.stderr
	stdin, err := s.cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := s.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := s.cmd.Start(); err != nil {
		return err
	}
	s.done = make(chan struct{})
	go func() { _ = s.cmd.Wait(); close(s.done) }()
	s.h, err = sdk.Connect(s.ctx, sdk.Transport{In: io.TeeReader(stdout, &s.inbound), Out: scaWriteCloser{io.MultiWriter(stdin, &s.outbound), stdin}},
		sdk.Options{ClientInfo: protocol.Implementation{Name: "dsh-sdk1-e2e", Version: "1"}, Handlers: sdk.Handlers{
			Approval: func(_ context.Context, r protocol.ApprovalRequest) protocol.ApprovalResult {
				s.mu.Lock()
				defer s.mu.Unlock()
				s.asked = append(s.asked, r)
				if len(s.asked) > len(s.answers) {
					return protocol.ApprovalResult{Outcome: "rejected"}
				}
				return protocol.ApprovalResult{Outcome: s.answers[len(s.asked)-1]}
			},
		}})
	if err != nil {
		return fmt.Errorf("connect: %v\nstderr:\n%s", err, s.stderr.String())
	}
	return nil
}

func (s *scaState) whenCreates(route string) error {
	var err error
	s.sess, err = s.h.Create(s.ctx, sdk.CreateOptions{Cwd: s.ws, Model: &protocol.ModelRoute{Provider: route, Model: model.ModelName}})
	if err != nil {
		return fmt.Errorf("%v\nstderr:\n%s", err, s.stderr.String())
	}
	return nil
}

func (s *scaState) whenRunsWrite(content string) error {
	w, ok := scaWrites[content]
	if !ok {
		return fmt.Errorf("no stand-in write of %q", content)
	}
	s.model.queue(w.turns()...)
	r, err := s.sess.Run(s.ctx, sdk.Input{Text: "Write the line '" + content + "' to note.txt with your file tool."})
	if err != nil {
		return fmt.Errorf("run: %v\nstderr:\n%s", err, s.stderr.String())
	}
	b, err := os.ReadFile(s.note)
	if err != nil {
		return err
	}
	s.runs = append(s.runs, scaRun{write: w, result: r, note: string(b)})
	return nil
}

func (s *scaState) whenCancelsHeld() error {
	if _, err := s.sess.Prompt(s.ctx, sdk.Input{Text: "Think about the note for a long while."}); err != nil {
		return err
	}
	select {
	case <-s.model.held:
	case <-s.done:
		return fmt.Errorf("dsh exited before the held turn reached the model:\n%s", s.stderr.String())
	case <-time.After(scaWithin):
		return fmt.Errorf("the held turn's request never reached the model:\n%s", s.stderr.String())
	}
	s.mark = len(s.inbound.frames())
	if err := s.sess.Cancel(s.ctx, sdk.CancelOptions{Inbox: "discard"}); err != nil {
		return fmt.Errorf("cancel: %v\nstderr:\n%s", err, s.stderr.String())
	}
	return nil
}

func (s *scaState) whenCloses() error {
	s.closeErr = errors.Join(s.sess.Close(s.ctx), s.h.Close(s.ctx))
	select {
	case <-s.done:
	case <-time.After(scaWithin):
		return fmt.Errorf("dsh did not exit within %s of the close:\n%s", scaWithin, s.stderr.String())
	}
	return nil
}

func (s *scaState) thenCreated() error {
	if s.sess.ID == "" {
		return errors.New("session/create answered no session id")
	}
	got, err1 := os.Stat(s.sess.Cwd)
	want, err2 := os.Stat(s.ws)
	if err1 != nil || err2 != nil || !os.SameFile(got, want) {
		return fmt.Errorf("session/create answered cwd %q, want the scratch workspace %q", s.sess.Cwd, s.ws)
	}
	return nil
}

// thenAsked checks the approval round trip on the wire: one approval/request
// per write from the host, each answered by the client's reply of the same id
// carrying the handler's outcome.
func (s *scaState) thenAsked(tool string) error {
	in, out := s.inbound.frames(), s.outbound.frames()
	var ids []json.RawMessage
	for _, f := range in {
		if f.Method == sdkserver.ApprovalRequestMethod {
			ids = append(ids, f.ID)
		}
	}
	if len(ids) != len(s.runs) {
		return fmt.Errorf("the host sent %d approval/request, want one per write (%d)", len(ids), len(s.runs))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.asked) != len(s.runs) {
		return fmt.Errorf("the handler was asked %d times, want %d", len(s.asked), len(s.runs))
	}
	for i, r := range s.asked {
		if r.ToolName != tool || r.SessionID != s.sess.ID || r.InteractionID == "" {
			return fmt.Errorf("approval/request %d = %+v, want tool %s on session %s", i+1, r, tool, s.sess.ID)
		}
	}
	for i, id := range ids {
		answered := false
		for _, f := range out {
			if f.Method == "" && bytes.Equal(f.ID, id) && strings.Contains(f.raw, strconv.Quote(s.answers[i])) {
				answered = true
			}
		}
		if !answered {
			return fmt.Errorf("approval/request %s got no reply carrying %q", id, s.answers[i])
		}
	}
	return nil
}

func (s *scaState) thenWriteOutcomes(want string) error {
	if len(s.runs) != 2 || s.runs[0].note != want+"\n" || s.runs[1].note != want+"\n" {
		var notes []string
		for _, r := range s.runs {
			notes = append(notes, r.note)
		}
		return fmt.Errorf("note.txt read %q after the runs, want %q after both", notes, want+"\n")
	}
	log, err := s.persisted()
	if err != nil {
		return err
	}
	for _, e := range log {
		if e.Type != "tool/result" {
			continue
		}
		d := strings.ToLower(e.Data)
		if strings.Contains(d, s.runs[0].write.call) && strings.Contains(d, `"is_error":true`) {
			return fmt.Errorf("the allowed write failed: %s", e.Data)
		}
		if strings.Contains(d, s.runs[1].write.call) && strings.Contains(d, "reject") {
			return nil
		}
	}
	return errors.New("the rejected write left no refused tool/result in the persisted log")
}

func (s *scaState) thenRunsCompleted() error {
	for i, r := range s.runs {
		if r.result.FinishReason != "completed" || r.result.FinalResponse != r.write.closing {
			return fmt.Errorf("run %d ended %q with %q, want completed with %q", i+1, r.result.FinishReason, r.result.FinalResponse, r.write.closing)
		}
	}
	return nil
}

func (s *scaState) thenCancelOrdered() error {
	var id json.RawMessage
	for _, f := range s.outbound.frames() {
		if f.Method == "session/cancel" {
			id = f.ID
		}
	}
	if id == nil {
		return errors.New("no session/cancel crossed the wire")
	}
	end, reply := -1, -1
	for i, f := range s.inbound.frames()[s.mark:] {
		if f.eventType() == "turn/end" && end < 0 {
			end = i
		}
		if f.Method == "" && bytes.Equal(f.ID, id) {
			reply = i
		}
	}
	if end < 0 || reply < end {
		return fmt.Errorf("the cancel reply is frame %d and turn/end frame %d after the cancel: the reply must follow turn/end", reply, end)
	}
	return nil
}

func (s *scaState) thenExited() error {
	if s.closeErr != nil {
		return fmt.Errorf("the close: %v\nstderr:\n%s", s.closeErr, s.stderr.String())
	}
	if st := s.cmd.ProcessState; st == nil || !st.Exited() || st.ExitCode() != 0 {
		return fmt.Errorf("dsh ended %v, want exit status 0:\n%s", st, s.stderr.String())
	}
	return nil
}

func (s *scaState) thenFramesOnly() error {
	out := s.inbound.String()
	if out == "" || !strings.HasSuffix(out, "\n") {
		return fmt.Errorf("stdout is empty or ends inside a line: %q", out[max(0, len(out)-200):])
	}
	for i, f := range s.inbound.frames() {
		if !f.ok {
			return fmt.Errorf("stdout line %d is not a JSON-RPC frame (M2-C-10): %q", i+1, f.raw)
		}
	}
	return nil
}

// persisted is the session's log exported from the scratch home's SQLite store,
// once the host has exited.
func (s *scaState) persisted() ([]scaEvent, error) {
	if s.log != nil {
		return s.log, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	st, err := sqlite.Open(ctx, home.New(s.root).DB())
	if err != nil {
		return nil, fmt.Errorf("opening the scratch home's SQLite store: %v", err)
	}
	defer func() { _ = st.Close(ctx) }()
	var buf bytes.Buffer
	if err := st.Export(ctx, &buf, s.sess.ID, sqlite.ExportOptions{}); err != nil {
		return nil, fmt.Errorf("exporting session %s: %v", s.sess.ID, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var e scaEvent
		if json.Unmarshal([]byte(line), &e) == nil && e.Type != "" {
			s.log = append(s.log, e)
		}
	}
	if len(s.log) == 0 {
		return nil, errors.New("the SQLite store holds no event of the session")
	}
	return s.log, nil
}

func (s *scaState) thenResultClosing() error {
	if len(s.runs) != 1 {
		return fmt.Errorf("%d runs, want 1", len(s.runs))
	}
	r := s.runs[0]
	if r.result.FinalResponse != r.write.closing || r.result.FirstSeq > r.result.LastSeq {
		return fmt.Errorf("session.run.completed = %+v, want text %q over a seq range", r.result, r.write.closing)
	}
	return nil
}

func (s *scaState) thenResultPersisted() error {
	log, err := s.persisted()
	if err != nil {
		return err
	}
	run := s.runs[0].result
	var last *scaEvent
	for i, e := range log {
		if e.Type == "assistant/message" && e.Seq >= run.FirstSeq && e.Seq <= run.LastSeq {
			last = &log[i]
		}
	}
	if last == nil {
		return fmt.Errorf("the persisted history holds no assistant/message in seq %d..%d", run.FirstSeq, run.LastSeq)
	}
	var m struct {
		Message struct {
			Blocks []struct {
				Kind string `json:"kind"`
				Text *struct {
					Text string `json:"text"`
				} `json:"text"`
			} `json:"blocks"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(last.Data), &m); err != nil {
		return fmt.Errorf("assistant/message %d: %v", last.Seq, err)
	}
	var text strings.Builder
	for _, b := range m.Message.Blocks {
		if b.Kind == "text" && b.Text != nil {
			text.WriteString(b.Text.Text)
		}
	}
	if text.String() != run.FinalResponse {
		return fmt.Errorf("session.run.completed text %q, the persisted last assistant message (seq %d) %q", run.FinalResponse, last.Seq, text.String())
	}
	return nil
}

func (s *scaState) thenNoGap() error {
	log, err := s.persisted()
	if err != nil {
		return err
	}
	for i := 1; i < len(log); i++ {
		if log[i].Seq != log[i-1].Seq+1 {
			return fmt.Errorf("persisted seq %d follows %d: a gap or a duplicate", log[i].Seq, log[i-1].Seq)
		}
	}
	return nil
}

// ---- @lab-s17: the signed-in Claude Code CLI route ----

type scaLive struct {
	out []byte
	err error
}

// givenLiveInputs fails a partly configured machine, naming what is missing.
// Off the S17 machine (no input set) the suite does not run the scenario at all.
func (l *scaLive) givenLiveInputs() error {
	var missing []string
	for _, name := range scaLiveInputs {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == len(scaLiveInputs) {
		return errors.New("not the S17 machine: no signed-in route input is set (the suite filters @lab-s17 out here)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("the signed-in route is partly configured: %s unset", strings.Join(missing, ", "))
	}
	return nil
}

func (l *scaLive) whenLiveRuns() error {
	mod, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		return err
	}
	bin := scaOperatorBin
	if bin == "" {
		bin = filepath.Join(scaOuterT.TempDir(), "dsh")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		build := exec.Command("go", "build", "-o", bin, "./cmd/dsh")
		build.Dir, build.Env = mod, scaEnv()
		if out, err := build.CombinedOutput(); err != nil {
			return fmt.Errorf("build dsh: %v\n%s", err, out)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), scaLiveWithin+time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-tags", "scenario", "-count=1", "-timeout", scaLiveWithin.String(), "-v", "-run", "^"+scaLiveTest+"$", "./internal/testkit/scenarios/")
	cmd.WaitDelay = 10 * time.Second
	cmd.Dir = mod
	cmd.Env = scaEnv(sdk.BinEnv+"="+bin, "DSH_REQUIRE_SCENARIOS="+scaScenario)
	l.out, l.err = cmd.CombinedOutput()
	return nil
}

func (l *scaLive) thenLivePasses() error {
	if l.err != nil || !strings.Contains(string(l.out), "--- PASS: "+scaLiveTest) {
		return fmt.Errorf("on the signed-in route %s must pass (%v):\n%s", scaLiveTest, l.err, l.out)
	}
	return nil
}

// scaEnv is the host environment with GOWORK=off, no inherited GOFLAGS or
// DSH_BIN, plus set.
func scaEnv(set ...string) []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		u := strings.ToUpper(kv)
		return strings.HasPrefix(u, "GOWORK=") || strings.HasPrefix(u, "GOFLAGS=") || strings.HasPrefix(u, sdk.BinEnv+"=")
	})
	return append(append(env, "GOWORK=off"), set...)
}

func (s *scaState) cleanup() {
	if s.cmd != nil && s.done != nil {
		select {
		case <-s.done:
		default:
			_ = s.cmd.Process.Kill()
			<-s.done
		}
	}
	if s.model != nil {
		s.model.close()
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.restoreBin != nil {
		s.restoreBin()
	}
}

func InitializeScenario_dh_s_17_acceptance_on_a_real_model_sdk_client_acceptance(ctx *godog.ScenarioContext) {
	s, l := &scaState{}, &scaLive{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.cleanup()
		return c, err
	})
	ctx.Step(`^DSH_BIN set to dsh built from this checkout with the testhook tag$`, s.givenBin)
	ctx.Step(`^a scratch home whose sdk profile layer serves route "claude-code" from a loopback stand-in model, asks before every write, and mounts the file tools on a scratch workspace holding note\.txt$`, s.givenHome)
	ctx.Step(`^a Go program on src/sdk/sdk starts "\$DSH_BIN --home <scratch> --profile sdk" over stdio with an approval handler answering (.+)$`, s.whenStarts)
	ctx.Step(`^it creates a session on route "([^"]*)" in the scratch workspace$`, s.whenCreates)
	ctx.Step(`^it runs a prompt whose model writes "([^"]*)" to note\.txt$`, s.whenRunsWrite)
	ctx.Step(`^it prompts a turn the model holds open and cancels it with inbox discard once the turn's request reached the model$`, s.whenCancelsHeld)
	ctx.Step(`^it closes the session and the connection$`, s.whenCloses)
	ctx.Step(`^create answered a session id whose cwd is the scratch workspace$`, s.thenCreated)
	ctx.Step(`^the host sent approval/request once per write, each for tool "([^"]*)" on the session, and the handler answered each$`, s.thenAsked)
	ctx.Step(`^the allowed write changed note\.txt to "([^"]*)" and the rejected write left it so with a refused tool result$`, s.thenWriteOutcomes)
	ctx.Step(`^both runs completed with the model's closing text$`, s.thenRunsCompleted)
	ctx.Step(`^the cancel reply followed the held turn's turn/end on the wire$`, s.thenCancelOrdered)
	ctx.Step(`^the host exited 0 once the connection closed, and no dsh process remains$`, s.thenExited)
	ctx.Step(`^every line the host wrote to stdout was a JSON-RPC 2\.0 frame \(M2-C-10\)$`, s.thenFramesOnly)
	ctx.Step(`^the run's session\.run\.completed text is the model's closing text$`, s.thenResultClosing)
	ctx.Step(`^it equals the last assistant message of the run in the persisted history read back from the scratch home's SQLite store$`, s.thenResultPersisted)
	ctx.Step(`^the persisted history read back has no gap or duplicate seq$`, s.thenNoGap)
	ctx.Step(`^the S17 machine's signed-in Claude Code CLI inputs DSH_SCENARIO_SDK1_LAYER, DSH_SCENARIO_SDK1_LONG_PROMPT, DSH_SCENARIO_CLAUDE_CODE_MODEL and DSH_SCENARIO_CLAUDE_CODE_EFFORT$`, l.givenLiveInputs)
	ctx.Step(`^TestSDKDrivesBuiltBinaryOverStdio runs against the release dsh named by DSH_BIN with DSH_REQUIRE_SCENARIOS=DH-E-SDK-1$`, l.whenLiveRuns)
	ctx.Step(`^it passes$`, l.thenLivePasses)
}

func TestE2E_dh_s_17_acceptance_on_a_real_model_sdk_client_acceptance(t *testing.T) {
	scaOuterT = t
	scaOperatorBin = os.Getenv(sdk.BinEnv)
	// Build here, on t's own goroutine: launcher.Build fails t directly, which
	// must not happen from a godog scenario subtest.
	scaTesthookBin = launcher.Build(t, filepath.Join("..", "..", ".."), "./cmd/dsh", "testhook")
	// Off the S17 machine the signed-in route does not exist: @lab-s17 is left
	// out of the run and reported as a skipped subtest, so it never counts as a
	// passed scenario. Any input set (even partly) runs it.
	tags := ""
	if !slices.ContainsFunc(scaLiveInputs, func(n string) bool { return strings.TrimSpace(os.Getenv(n)) != "" }) {
		tags = "~@lab-s17"
		t.Run("lab-s17 DH-E-SDK-1 on the signed-in Claude Code CLI route", func(t *testing.T) {
			t.Skipf("not run, not proven: %s unset (the S17 machine's signed-in route)", strings.Join(scaLiveInputs, ", "))
		})
	}
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_17_acceptance_on_a_real_model_sdk_client_acceptance,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_17_acceptance_on_a_real_model_sdk_client_acceptance.feature"},
			Tags:     tags,
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
