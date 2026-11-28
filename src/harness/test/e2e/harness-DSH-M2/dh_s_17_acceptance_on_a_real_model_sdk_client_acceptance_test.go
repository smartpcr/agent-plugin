//go:build e2e

package e2e

// Step definitions of
// dh_s_17_acceptance_on_a_real_model_sdk_client_acceptance.feature (DH-S-17
// Stage 7.5, DH-E-SDK-1, TC-20s, M2-C-10).
//
//   - Built binary: the testhook dsh named by DSH_BIN runs as a child process
//     (`--profile sdk`) on a scratch home whose sdk layer serves route
//     "claude-code" from a loopback model instance and mounts the file tools on a
//     scratch workspace. The Go client (src/sdk/sdk) drives it over the child's
//     stdio; both directions are recorded, and after the close the persisted
//     history is read back from the home's SQLite store. On the S17 machine
//     (DSH_SCENARIO_SDK1_* set) the real scenario test must pass as well.
//   - Approval: the same Go client against the real SDK server, answerer and
//     tool pipeline in process, whose policy asks before every write.

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
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/sdkserver"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
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
	// scaClosing is the model's closing text of the write run: the run's result
	// must be it, and not the text before the tool call.
	scaClosing = "The note now reads allowed."
)

// scaLiveInputs are the S17 machine's inputs of TestSDKDrivesBuiltBinaryOverStdio.
var scaLiveInputs = []string{"DSH_SCENARIO_SDK1_LAYER", "DSH_SCENARIO_SDK1_LONG_PROMPT", "DSH_SCENARIO_CLAUDE_CODE_MODEL", "DSH_SCENARIO_CLAUDE_CODE_EFFORT"}

// scaTurns are the route instance's replies in request order, in the kit's SSE
// dialect: a write of note.txt, then the closing answer. Any later request is
// held open until the host drops it (the turn the client cancels).
var scaTurns = []string{
	"event: text\ndata: {\"text\":\"I will write the note.\"}\n\n" +
		"event: tool\ndata: {\"id\":\"toolu_sca01\",\"name\":\"write\",\"args\":{\"file_path\":\"note.txt\",\"content\":\"allowed\\n\"}}\n\n" +
		"event: usage\ndata: {\"input\":10,\"output\":5}\n\n" +
		"event: finish\ndata: {\"reason\":\"tool_calls\"}\n\n",
	"event: text\ndata: {\"text\":" + strconv.Quote(scaClosing) + "}\n\n" +
		"event: usage\ndata: {\"input\":20,\"output\":4}\n\n" +
		"event: finish\ndata: {\"reason\":\"stop\"}\n\n",
}

var (
	scaOuterT *testing.T
	// scaTesthookBin is dsh built with the testhook tag by the suite's test
	// function; scaOperatorBin is DSH_BIN as the suite found it (the S17 build).
	scaTesthookBin string
	scaOperatorBin string
)

// scaModel is the ephemeral instance of route "claude-code".
type scaModel struct {
	srv      *httptest.Server
	stop     chan struct{}
	held     chan struct{}
	heldOnce sync.Once
	n        atomic.Int32
}

func newScaModel() *scaModel {
	m := &scaModel{stop: make(chan struct{}), held: make(chan struct{})}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		n := int(m.n.Add(1)) - 1
		w.Header().Set("Content-Type", "text/event-stream")
		if n < len(scaTurns) {
			_, _ = io.WriteString(w, scaTurns[n])
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

type scaState struct {
	ctx    context.Context
	cancel context.CancelFunc

	// Built binary
	restoreBin        func()
	model             *scaModel
	root, ws, note    string
	cmd               *exec.Cmd
	done              chan struct{}
	stderr            scaWire
	inbound, outbound scaWire
	h                 *sdk.Harness
	sess              *sdk.Session
	asked             atomic.Int32
	run               sdk.RunResult
	mark              int
	closeErr          error
	log               []scaEvent

	// In-process host
	ip *scaHost
}

// ---- Built binary ----

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
		"    - id: fs.ops\n      name: fs.ops\n    - id: fs.tools\n      name: fs.tools\n")
	return nil
}

func (s *scaState) whenStarts(answer string) error {
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
			Approval: func(context.Context, protocol.ApprovalRequest) protocol.ApprovalResult {
				s.asked.Add(1)
				return protocol.ApprovalResult{Outcome: answer}
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

func (s *scaState) whenRunsWrite(string) error {
	var err error
	s.run, err = s.sess.Run(s.ctx, sdk.Input{Text: "Append the line 'allowed' to note.txt with your file tool, then say done."})
	if err != nil {
		return fmt.Errorf("run: %v\nstderr:\n%s", err, s.stderr.String())
	}
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

func (s *scaState) thenRunCompleted(want string) error {
	if s.run.FinishReason != "completed" || s.run.FinalResponse != scaClosing {
		return fmt.Errorf("the run ended %q with %q, want completed with %q", s.run.FinishReason, s.run.FinalResponse, scaClosing)
	}
	if b, err := os.ReadFile(s.note); err != nil || string(b) != want+"\n" {
		return fmt.Errorf("note.txt reads %q (%v), want %q", b, err, want+"\n")
	}
	return nil
}

// thenApprovalGap asserts both halves of the built binary's approval leg: the
// client offered to answer, and the host never asked. When the sdk profile
// gains a policy that asks before writes and wire.sdk its interaction registry,
// this step fails and the approval half moves onto the built binary.
func (s *scaState) thenApprovalGap() error {
	advertised := false
	for _, f := range s.outbound.frames() {
		if f.Method == protocol.InitializeMethod {
			var p protocol.InitializeParams
			advertised = json.Unmarshal(f.Params, &p) == nil && p.Capabilities.Approvals
		}
	}
	if !advertised {
		return errors.New("the client's initialize did not advertise approvals")
	}
	for _, f := range s.inbound.frames() {
		if f.Method == sdkserver.ApprovalRequestMethod {
			return fmt.Errorf("the built host sent approval/request: the gap is closed, move the approval half onto the built binary: %s", f.raw)
		}
	}
	if n := s.asked.Load(); n != 0 {
		return fmt.Errorf("the approval handler ran %d times", n)
	}
	log, err := s.persisted()
	if err != nil {
		return err
	}
	for _, e := range log {
		if strings.HasPrefix(e.Type, "approval/") {
			return fmt.Errorf("the persisted log holds %s: the write was put to approval: %s", e.Type, e.Data)
		}
		if e.Type == "tool/result" && strings.Contains(e.Data, "toolu_sca01") && strings.Contains(e.Data, `"is_error":true`) {
			return fmt.Errorf("the write failed: %s", e.Data)
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

// thenLive runs the real scenario test where the S17 inputs are set; where none
// is set the in-process route instance above is the proof. A partly configured
// machine fails and names the missing inputs rather than passing silently.
func (s *scaState) thenLive() error {
	var missing []string
	for _, name := range scaLiveInputs[:2] {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 2 {
		return nil
	}
	for _, name := range scaLiveInputs[2:] {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the signed-in route is partly configured: %s unset", strings.Join(missing, ", "))
	}
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
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: "+scaLiveTest) {
		return fmt.Errorf("on the signed-in route %s must pass (%v):\n%s", scaLiveTest, err, out)
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
	if s.run.FinalResponse != scaClosing || s.run.FirstSeq > s.run.LastSeq {
		return fmt.Errorf("session.run.completed = %+v, want text %q over a seq range", s.run, scaClosing)
	}
	return nil
}

func (s *scaState) thenResultPersisted() error {
	log, err := s.persisted()
	if err != nil {
		return err
	}
	var last *scaEvent
	for i, e := range log {
		if e.Type == "assistant/message" && e.Seq >= s.run.FirstSeq && e.Seq <= s.run.LastSeq {
			last = &log[i]
		}
	}
	if last == nil {
		return fmt.Errorf("the persisted history holds no assistant/message in seq %d..%d", s.run.FirstSeq, s.run.LastSeq)
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
	if text.String() != s.run.FinalResponse {
		return fmt.Errorf("session.run.completed text %q, the persisted last assistant message (seq %d) %q", s.run.FinalResponse, last.Seq, text.String())
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

// ---- In-process host ----

const scaHostRoute vocab.RouteID = "r/sca"

type scaWriteIn struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

type scaPrompt struct{}

func (scaPrompt) Assemble(_ context.Context, in prompt.Input) (prompt.Assembly, error) {
	a := prompt.Assembly{Sections: []prompt.RenderedSection{{Name: "e2e", Order: prompt.OrderHarness, Owner: "sdk1-e2e", Text: "You are the e2e agent."}}}
	for _, d := range in.View.Tools() {
		a.Tools = append(a.Tools, d.Schema())
	}
	fp, err := vocab.Digest(a.Tools)
	a.Fingerprint = fp
	return a, err
}

func (scaPrompt) Render(a prompt.Assembly) (string, error) { return a.Sections[0].Text, nil }

func scaWait(f *cordis.Fiber, err error) error {
	if err != nil {
		return err
	}
	return f.Wait()
}

// scaHost is the real SDK server, answerer and tool pipeline in process, served
// to the Go client over pipes.
type scaHost struct {
	cleanups []func()
	note     string
	h        *sdk.Harness
	sess     *sdk.Session
	answer   atomic.Value
	mu       sync.Mutex
	asked    []protocol.ApprovalRequest
	runs     []sdk.RunResult
	notes    []string
}

func (s *scaState) givenHost() error {
	ip := &scaHost{}
	s.ip = ip
	ws := scaOuterT.TempDir()
	ip.note = filepath.Join(ws, "note.txt")
	if err := os.WriteFile(ip.note, []byte("original\n"), 0o600); err != nil {
		return err
	}
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	ip.cleanups = append(ip.cleanups, root.Dispose)
	if err := scaWait(root.Use(scope.Component, cordis.None{})); err != nil {
		return err
	}
	var tab scope.Table
	if err := scaWait(root.Inject([]string{scope.TableKey.Name()}, func(c *cordis.Context) (err error) {
		tab, err = scope.TableKey.Get(c)
		return err
	})); err != nil {
		return err
	}
	hreg := human.New()
	ip.cleanups = append(ip.cleanups, hreg.Close)
	tsvc := core.NewWith(root, tab, core.Config{Approval: approval.New(approval.Config{Registry: hreg})})
	if _, err := tsvc.Register(root, tools.Define(tools.Spec[scaWriteIn, string]{
		Name: "write", Description: "Create a file, or replace a file's whole content.", Effects: []tools.Effect{tools.EffectFiles},
		Run: func(_ context.Context, in scaWriteIn, _ tools.Run) (string, error) {
			return "wrote " + in.FilePath, os.WriteFile(filepath.Join(ws, filepath.Base(in.FilePath)), []byte(in.Content), 0o600)
		},
	})); err != nil {
		return err
	}
	if _, err := tsvc.Decide(root, "ask-before-writes", func(_ context.Context, c tools.Call) tools.Decision {
		if c.Name() == "write" {
			return tools.Ask("writes a file", "")
		}
		return tools.Allow()
	}); err != nil {
		return err
	}
	svc := session.NewService(root, memstore.New(), session.Config{})
	reg := agent.NewRegistry(root)
	rt := llm.New()
	model.Register(scaOuterT, rt, scaHostRoute, model.NewTransport(scaOuterT,
		model.Calls(model.Tool("toolu_scaa", "write", `{"file_path":"note.txt","content":"allowed\n"}`)), model.Reply("Allowed write done."),
		model.Calls(model.Tool("toolu_scar", "write", `{"file_path":"note.txt","content":"rejected\n"}`)), model.Reply("Rejected write skipped.")))
	var drvCtx *cordis.Context
	if err := scaWait(root.Apply("driver", func(c *cordis.Context) error { drvCtx = c; return nil })); err != nil {
		return err
	}
	f, err := driver.NewFactory(driver.FactoryOptions{Context: drvCtx, Sessions: svc, Scopes: tab, Models: rt, Tools: tsvc, Prompt: scaPrompt{}})
	if err != nil {
		return err
	}
	if _, err := reg.SetFactory(root, f); err != nil {
		return err
	}
	srv, err := sdkserver.New(sdkserver.Options{
		Owner: root, Agents: reg, Sessions: svc, Routes: rt,
		Info: protocol.Implementation{Name: "dsh", Version: "e2e"}, Limits: protocol.Limits{MaxFrameBytes: 1 << 20},
		Interactions: hreg,
	})
	if err != nil {
		return err
	}
	conn, err := srv.Connect()
	if err != nil {
		return err
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = conn.Serve(context.Background(), inR, outW, jsonrpc.Options{})
		_ = outW.Close()
	}()
	ip.cleanups = append(ip.cleanups, func() {
		_ = inW.Close()
		_ = outR.Close()
		select {
		case <-served:
		case <-time.After(30 * time.Second):
		}
		_ = inR.Close()
		_ = conn.Close(context.Background())
	})
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 2*scaWithin)
	ip.h, err = sdk.Connect(s.ctx, sdk.Transport{In: outR, Out: inW}, sdk.Options{
		ClientInfo: protocol.Implementation{Name: "dsh-sdk1-e2e", Version: "1"},
		Handlers: sdk.Handlers{Approval: func(_ context.Context, r protocol.ApprovalRequest) protocol.ApprovalResult {
			ip.mu.Lock()
			ip.asked = append(ip.asked, r)
			ip.mu.Unlock()
			return protocol.ApprovalResult{Outcome: ip.answer.Load().(string)}
		}},
	})
	if err != nil {
		return err
	}
	ip.cleanups = append(ip.cleanups, func() { _ = ip.h.Close(context.Background()) })
	ip.sess, err = ip.h.Create(s.ctx, sdk.CreateOptions{Model: &protocol.ModelRoute{Provider: string(scaHostRoute), Model: model.ModelName}})
	return err
}

func (s *scaState) whenHostRun(answer string) error {
	ip := s.ip
	ip.answer.Store(answer)
	r, err := ip.sess.Run(s.ctx, sdk.Input{Text: "Write note.txt with your file tool."})
	if err != nil {
		return err
	}
	b, err := os.ReadFile(ip.note)
	if err != nil {
		return err
	}
	ip.runs, ip.notes = append(ip.runs, r), append(ip.notes, string(b))
	return nil
}

func (s *scaState) thenAskedPerWrite(tool string) error {
	ip := s.ip
	ip.mu.Lock()
	defer ip.mu.Unlock()
	if len(ip.asked) != 2 {
		return fmt.Errorf("the handler was asked %d times, want once per write (2)", len(ip.asked))
	}
	for i, r := range ip.asked {
		if r.ToolName != tool || r.SessionID != ip.sess.ID || r.InteractionID == "" {
			return fmt.Errorf("approval/request %d = %+v, want tool %s on session %s", i, r, tool, ip.sess.ID)
		}
	}
	return nil
}

func (s *scaState) thenWriteOutcomes() error {
	ip := s.ip
	if len(ip.notes) != 2 || ip.notes[0] != "allowed\n" || ip.notes[1] != "allowed\n" {
		return fmt.Errorf("note.txt read %q after the two runs, want \"allowed\\n\" both times", ip.notes)
	}
	evs, err := ip.sess.History(s.ctx, ip.runs[1].FirstSeq-1, 1000)
	if err != nil {
		return err
	}
	for _, e := range evs {
		d := strings.ToLower(string(e.Data))
		if e.Seq <= ip.runs[1].LastSeq && e.Type == "tool/result" && strings.Contains(d, "toolu_scar") && strings.Contains(d, "reject") {
			return nil
		}
	}
	return fmt.Errorf("the rejected write left no refused tool/result in seq %d..%d", ip.runs[1].FirstSeq, ip.runs[1].LastSeq)
}

func (s *scaState) thenHostRunsCompleted() error {
	want := []string{"Allowed write done.", "Rejected write skipped."}
	for i, r := range s.ip.runs {
		if r.FinishReason != "completed" || r.FinalResponse != want[i] {
			return fmt.Errorf("run %d ended %q with %q, want completed with %q", i+1, r.FinishReason, r.FinalResponse, want[i])
		}
	}
	return nil
}

func (s *scaState) cleanup() {
	if s.ip != nil {
		for i := len(s.ip.cleanups) - 1; i >= 0; i-- {
			s.ip.cleanups[i]()
		}
	}
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
	s := &scaState{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.cleanup()
		return c, err
	})
	ctx.Step(`^DSH_BIN set to dsh built from this checkout with the testhook tag$`, s.givenBin)
	ctx.Step(`^a scratch home whose sdk profile layer serves route "claude-code" from an in-process model instance and mounts the file tools on a scratch workspace holding note\.txt$`, s.givenHome)
	ctx.Step(`^a Go program on src/sdk/sdk starts "\$DSH_BIN --home <scratch> --profile sdk" over stdio with an approval handler answering "([^"]*)"$`, s.whenStarts)
	ctx.Step(`^it creates a session on route "([^"]*)" in the scratch workspace$`, s.whenCreates)
	ctx.Step(`^it runs a prompt whose model writes "([^"]*)" to note\.txt$`, s.whenRunsWrite)
	ctx.Step(`^it prompts a turn the model holds open and cancels it with inbox discard once the turn's request reached the model$`, s.whenCancelsHeld)
	ctx.Step(`^it closes the session and the connection$`, s.whenCloses)
	ctx.Step(`^create answered a session id whose cwd is the scratch workspace$`, s.thenCreated)
	ctx.Step(`^the run completed with the model's closing text and note\.txt reads "([^"]*)"$`, s.thenRunCompleted)
	ctx.Step(`^the client advertised approvals, yet no approval/request crossed the wire and the write ran unasked: .+$`, s.thenApprovalGap)
	ctx.Step(`^the cancel reply followed the held turn's turn/end on the wire$`, s.thenCancelOrdered)
	ctx.Step(`^the host exited 0 once the connection closed, and no dsh process remains$`, s.thenExited)
	ctx.Step(`^every line the host wrote to stdout was a JSON-RPC 2\.0 frame \(M2-C-10\)$`, s.thenFramesOnly)
	ctx.Step(`^on the S17 machine TestSDKDrivesBuiltBinaryOverStdio also passes on the signed-in route$`, s.thenLive)
	ctx.Step(`^an SDK host in process whose tool pipeline asks before every write and whose sdk channel answerer is mounted, served to the Go client over stdio pipes$`, s.givenHost)
	ctx.Step(`^the client runs a prompt whose model writes note\.txt and answers its approval "([^"]*)"$`, s.whenHostRun)
	ctx.Step(`^the handler was asked exactly once per write, each time for tool "([^"]*)" on the client's session$`, s.thenAskedPerWrite)
	ctx.Step(`^the allowed write changed note\.txt and the rejected write left it unchanged with a refused tool result$`, s.thenWriteOutcomes)
	ctx.Step(`^both runs completed with the model's closing text$`, s.thenHostRunsCompleted)
	ctx.Step(`^the run's session\.run\.completed text is the model's closing text$`, s.thenResultClosing)
	ctx.Step(`^it equals the last assistant message of the run in the persisted history read back from the scratch home's SQLite store$`, s.thenResultPersisted)
	ctx.Step(`^the persisted history read back has no gap or duplicate seq$`, s.thenNoGap)
}

func TestE2E_dh_s_17_acceptance_on_a_real_model_sdk_client_acceptance(t *testing.T) {
	scaOuterT = t
	scaOperatorBin = os.Getenv(sdk.BinEnv)
	// Build here, on t's own goroutine: launcher.Build fails t directly, which
	// must not happen from a godog scenario subtest.
	scaTesthookBin = launcher.Build(t, filepath.Join("..", "..", ".."), "./cmd/dsh", "testhook")
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_17_acceptance_on_a_real_model_sdk_client_acceptance,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_17_acceptance_on_a_real_model_sdk_client_acceptance.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
