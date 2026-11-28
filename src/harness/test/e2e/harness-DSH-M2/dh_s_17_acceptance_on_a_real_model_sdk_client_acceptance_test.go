//go:build e2e

package e2e

// Step definitions of
// dh_s_17_acceptance_on_a_real_model_sdk_client_acceptance.feature (DH-S-17
// Stage 7.5, DH-E-SDK-1, TC-20s, M2-C-10).
//
//   - The testhook dsh named by DSH_BIN runs as a child process (`--profile
//     sdk`) on a scratch home whose sdk layer mounts the real claude-code route
//     (llm.testkit-claude-code) over an ephemeral stand-in Claude Code client
//     (testkit/claudestandin) that replays a loopback model's stream-json, puts
//     every write to approval (tools.testkit-ask) and mounts the file tools on a
//     scratch workspace. The Go client (src/sdk/sdk) drives it over the child's
//     stdio and answers the host's approval/request; both directions are
//     recorded, and after the close the persisted history is read back from the
//     home's SQLite store.
//   - While the cancelled turn is held, dsh's process tree (the stand-in client
//     and the listing server it started) is read from the OS process table, so
//     the close can assert the whole tree stopped and cleanup can kill leftovers.

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
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/sdkserver"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
	"github.com/smartpcr/agent-plugin/src/sdk/sdk"
)

const (
	scaRoute = "claude-code"
	// scaModelName is a model alias of the claude-code route's catalogue.
	scaModelName = "sonnet"
	// scaStandinURLEnv is claudestandin.URLEnv (a main package, not importable).
	scaStandinURLEnv = "CLAUDESTANDIN_URL"
	scaWithin        = 2 * time.Minute
)

// scaWrite is one write run of the stand-in model: its tool call writes content
// to note.txt, and closing is its closing text, which the run's result must be
// (not the text before the tool call).
type scaWrite struct{ call, content, closing string }

var scaWrites = map[string]scaWrite{
	"allowed":  {"toolu_scaa", "allowed", "The note now reads allowed."},
	"rejected": {"toolu_scar", "rejected", "The write was rejected."},
}

// The Claude Code client's stream-json lines (claude-code-cli.md sec. 5.2 and
// 5.4, as the route's own fixtures in llm/provider/claudecode/testdata); the
// stand-in client fills the {{...}} fields of its run.
const (
	scaInit  = `{"type":"system","subtype":"init","cwd":"{{cwd}}","session_id":"{{session_id}}","tools":"{{tools}}","mcp_servers":"{{mcp_servers}}","model":"claude-sonnet-4-5","permissionMode":"dontAsk","slash_commands":[],"apiKeySource":"none","claude_code_version":"{{version}}","output_style":"default","agents":[],"skills":[],"plugins":[],"uuid":"00000000-0000-4000-8000-000000000001"}`
	scaStart = `{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_sca","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}}},"session_id":"{{session_id}}","parent_tool_use_id":null}`
)

func scaEventLine(ev string) string {
	return `{"type":"stream_event","event":` + ev + `,"session_id":"{{session_id}}","parent_tool_use_id":null}`
}

func scaJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func scaLines(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func scaTextBlock(i int, text string) []string {
	return []string{
		scaEventLine(fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, i)),
		scaEventLine(fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%s}}`, i, scaJSON(text))),
		scaEventLine(fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, i)),
	}
}

// turns are the two client runs of one write: the model's call of the harness
// tool write (denied by the client's own rules and returned unexecuted, as the
// real client does), then its closing text.
func (w scaWrite) turns() []string {
	input := map[string]string{"file_path": "note.txt", "content": w.content + "\n"}
	use := scaJSON(map[string]any{"type": "tool_use", "id": w.call, "name": "mcp__dsh__write", "input": input})
	call := append([]string{scaInit, scaStart}, scaTextBlock(0, "I will write the note.")...)
	call = append(call,
		scaEventLine(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"`+w.call+`","name":"mcp__dsh__write","input":{}}}`),
		scaEventLine(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":`+scaJSON(scaJSON(input))+`}}`),
		scaEventLine(`{"type":"content_block_stop","index":1}`),
		`{"type":"assistant","message":{"id":"msg_sca","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"I will write the note."},`+use+`],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":1}},"parent_tool_use_id":null,"session_id":"{{session_id}}"}`,
		scaEventLine(`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":14}}`),
		scaEventLine(`{"type":"message_stop"}`),
		`{"type":"system","subtype":"permission_denied","tool_name":"mcp__dsh__write","tool_use_id":"`+w.call+`","session_id":"{{session_id}}"}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"`+w.call+`","content":"Permission to use mcp__dsh__write has been denied.","is_error":true}]},"parent_tool_use_id":null,"session_id":"{{session_id}}"}`,
		`{"type":"result","subtype":"error_max_turns","is_error":true,"duration_ms":10,"duration_api_ms":9,"num_turns":1,"session_id":"{{session_id}}","total_cost_usd":0,"usage":{"input_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":14},"modelUsage":{},"permission_denials":[{"tool_name":"mcp__dsh__write","tool_use_id":"`+w.call+`","tool_input":`+scaJSON(input)+`}],"stop_reason":"tool_use"}`)
	closing := append([]string{scaInit, scaStart}, scaTextBlock(0, w.closing)...)
	closing = append(closing,
		`{"type":"assistant","message":{"id":"msg_sca","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":`+scaJSON(w.closing)+`}],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":20,"output_tokens":1}},"parent_tool_use_id":null,"session_id":"{{session_id}}"}`,
		scaEventLine(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":4}}`),
		scaEventLine(`{"type":"message_stop"}`),
		`{"type":"result","subtype":"success","is_error":false,"duration_ms":10,"duration_api_ms":9,"num_turns":1,"session_id":"{{session_id}}","total_cost_usd":0,"usage":{"input_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":4},"modelUsage":{},"permission_denials":[],"result":`+scaJSON(w.closing)+`,"stop_reason":"end_turn"}`)
	return []string{scaLines(call...), scaLines(closing...)}
}

var (
	scaOuterT *testing.T
	// scaTesthookBin is dsh built with the testhook tag by the suite's test
	// function; scaStandinBin is the ephemeral stand-in Claude Code client.
	scaTesthookBin string
	scaStandinBin  string
)

// scaModel is the loopback model behind the ephemeral stand-in client
// (testkit/claudestandin) that the real claude-code route starts in dsh: each
// client run POSTs its arguments, rendered request and listed tools, and is
// answered with the queued turns' stream-json lines in request order; any later
// run is held open after its first lines until the route stops the client (the
// turn the SDK client cancels).
type scaModel struct {
	srv      *httptest.Server
	stop     chan struct{}
	held     chan struct{}
	heldOnce sync.Once
	mu       sync.Mutex
	turns    []string
	runs     []scaClientRun
}

// scaClientRun is what one run of the stand-in client posted.
type scaClientRun struct {
	Args  []string `json:"args"`
	Stdin string   `json:"stdin"`
	Tools []string `json:"tools"`
}

func newScaModel() *scaModel {
	m := &scaModel{stop: make(chan struct{}), held: make(chan struct{})}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var run scaClientRun
		_ = json.NewDecoder(r.Body).Decode(&run)
		w.Header().Set("Content-Type", "application/x-ndjson")
		m.mu.Lock()
		m.runs = append(m.runs, run)
		next, ok := "", len(m.turns) > 0
		if ok {
			next, m.turns = m.turns[0], m.turns[1:]
		}
		m.mu.Unlock()
		if ok {
			_, _ = io.WriteString(w, next)
			return
		}
		_, _ = io.WriteString(w, scaLines(append([]string{scaInit, scaStart}, scaTextBlock(0, "thinking")[:2]...)...))
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

func (m *scaModel) clientRuns() []scaClientRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.runs)
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
	// tree is dsh's descendants seen while the cancelled turn was held.
	tree []scaProc
}

// scaProc is one process table entry; name is the lower-case image base name
// without ".exe", so a reused pid of another program does not match.
type scaProc struct {
	pid, ppid int
	name      string
}

const scaStandinName = "claudestandin"

func scaProcs() ([]scaProc, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
			`Get-CimInstance Win32_Process | ForEach-Object { "$($_.ProcessId) $($_.ParentProcessId) $($_.Name)" }`)
	} else {
		cmd = exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=", "-o", "comm=")
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing processes: %v", err)
	}
	var ps []scaProc
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		name := strings.TrimSuffix(strings.ToLower(filepath.Base(strings.Join(f[2:], " "))), ".exe")
		ps = append(ps, scaProc{pid: pid, ppid: ppid, name: name})
	}
	return ps, nil
}

// scaTree is every live descendant of root.
func scaTree(root int) ([]scaProc, error) {
	ps, err := scaProcs()
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{root: true}
	var tree []scaProc
	for grew := true; grew; {
		grew = false
		for _, p := range ps {
			if !seen[p.pid] && seen[p.ppid] {
				seen[p.pid], grew = true, true
				tree = append(tree, p)
			}
		}
	}
	return tree, nil
}

// scaAlive is the recorded processes still running the same program.
func scaAlive(recorded []scaProc) ([]scaProc, error) {
	ps, err := scaProcs()
	if err != nil {
		return nil, err
	}
	var alive []scaProc
	for _, r := range recorded {
		for _, p := range ps {
			if p.pid == r.pid && p.name == r.name {
				alive = append(alive, r)
			}
		}
	}
	return alive, nil
}

// recordTree records dsh's descendants while the held turn runs: the stand-in
// client and, under it, the listing server it started from --mcp-config.
func (s *scaState) recordTree() error {
	tree, err := scaTree(s.cmd.Process.Pid)
	if err != nil {
		return err
	}
	s.tree = tree
	for _, c := range tree {
		if c.name != scaStandinName {
			continue
		}
		for _, l := range tree {
			if l.ppid == c.pid {
				return nil
			}
		}
	}
	return fmt.Errorf("dsh (pid %d) has no running %s with a listing server under it while the turn is held: %+v", s.cmd.Process.Pid, scaStandinName, tree)
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
		"    - id: route\n      name: llm.testkit-claude-code\n      config:\n        program: "+strconv.Quote(scaStandinBin)+"\n"+
		"        env: ["+strconv.Quote(scaStandinURLEnv+"="+s.model.srv.URL)+"]\n        tempDir: "+strconv.Quote(scaOuterT.TempDir())+"\n"+
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
	s.sess, err = s.h.Create(s.ctx, sdk.CreateOptions{Cwd: s.ws, Model: &protocol.ModelRoute{Provider: route, Model: scaModelName}})
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
	if err := s.recordTree(); err != nil {
		return err
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
	allowed, rejected := s.runs[0].write.call, s.runs[1].write.call
	results := map[string][]event.ToolResultPayload{}
	for _, e := range log {
		if e.Type != event.ToolResult.Name() {
			continue
		}
		var p event.ToolResultPayload
		if err := json.Unmarshal([]byte(e.Data), &p); err != nil {
			return fmt.Errorf("tool/result %d: %v", e.Seq, err)
		}
		results[p.CallID] = append(results[p.CallID], p)
	}
	for _, call := range []string{allowed, rejected} {
		if n := len(results[call]); n != 1 {
			return fmt.Errorf("the persisted log holds %d tool/result for %s, want exactly 1 (results by call: %v)", n, call, scaResultCalls(results))
		}
	}
	if p := results[allowed][0]; p.IsError {
		return fmt.Errorf("the allowed write %s failed: %q", allowed, scaResultText(p))
	}
	p := results[rejected][0]
	refusal := string(tools.CodeApprovalRejected) + ": "
	if text := scaResultText(p); !p.IsError || !strings.HasPrefix(text, refusal) {
		return fmt.Errorf("the rejected write %s settled is_error=%v with %q, want is_error=true with the approval refusal %q...", rejected, p.IsError, text, refusal)
	}
	return nil
}

// scaResultText is the text of a tool/result's tool_result blocks.
func scaResultText(p event.ToolResultPayload) string {
	var text strings.Builder
	for _, b := range p.Message.Blocks() {
		if b.ToolResult == nil {
			continue
		}
		for _, part := range b.ToolResult.Parts {
			if part.Text != nil {
				text.WriteString(part.Text.Text)
			}
		}
	}
	return text.String()
}

func scaResultCalls(results map[string][]event.ToolResultPayload) map[string]int {
	n := map[string]int{}
	for call, ps := range results {
		n[call] = len(ps)
	}
	return n
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
	if len(s.tree) == 0 {
		return errors.New("no process tree of the held turn was recorded")
	}
	// A tree stop may take a moment to be reflected in the process table.
	deadline := time.Now().Add(10 * time.Second)
	for {
		alive, err := scaAlive(s.tree)
		if err != nil {
			return err
		}
		if len(alive) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("after dsh exited, processes of the held turn's tree remain: %+v", alive)
		}
		time.Sleep(200 * time.Millisecond)
	}
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

// thenThroughClient checks that every model request of the session went through
// the real claude-code route to its client: each run of the stand-in client was
// started with the route's session id and MCP configuration, listed the harness
// tool from dsh's listing server, and was sent the prompt it answered.
func (s *scaState) thenThroughClient(tool string) error {
	runs := s.model.clientRuns()
	if want := 2*len(s.runs) + 1; len(runs) != want {
		return fmt.Errorf("the claude-code client ran %d times, want %d (two per write, one held turn)", len(runs), want)
	}
	prompts := []string{}
	for _, r := range s.runs {
		prompts = append(prompts, "Write the line '"+r.write.content+"' to note.txt", "Write the line '"+r.write.content+"' to note.txt")
	}
	prompts = append(prompts, "Think about the note for a long while.")
	for i, r := range runs {
		if !slices.Contains(r.Args, "--session-id") || !slices.Contains(r.Args, "--mcp-config") || !slices.Contains(r.Args, "-p") {
			return fmt.Errorf("client run %d started with %q, not the route's print-mode arguments", i+1, r.Args)
		}
		if !slices.Contains(r.Tools, "mcp__dsh__"+tool) {
			return fmt.Errorf("client run %d listed %q from dsh's listing server, want mcp__dsh__%s", i+1, r.Tools, tool)
		}
		if !strings.Contains(r.Stdin, prompts[i]) {
			return fmt.Errorf("client run %d was not sent the prompt %q:\n%s", i+1, prompts[i], r.Stdin)
		}
	}
	return nil
}
func (s *scaState) cleanup() {
	if s.cmd != nil && s.done != nil {
		select {
		case <-s.done:
		default:
			if tree, err := scaTree(s.cmd.Process.Pid); err == nil {
				s.tree = append(s.tree, tree...)
			}
			_ = s.cmd.Process.Kill()
			<-s.done
		}
	}
	if alive, err := scaAlive(s.tree); err == nil {
		for _, p := range alive {
			if proc, err := os.FindProcess(p.pid); err == nil {
				_ = proc.Kill()
			}
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
	ctx.Step(`^a scratch home whose sdk profile layer mounts the real claude-code route over an ephemeral stand-in Claude Code client, asks before every write, and mounts the file tools on a scratch workspace holding note\.txt$`, s.givenHome)
	ctx.Step(`^a Go program on src/sdk/sdk starts "\$DSH_BIN --home <scratch> --profile sdk" over stdio with an approval handler answering (.+)$`, s.whenStarts)
	ctx.Step(`^it creates a session on route "([^"]*)" in the scratch workspace$`, s.whenCreates)
	ctx.Step(`^it runs a prompt whose model writes "([^"]*)" to note\.txt$`, s.whenRunsWrite)
	ctx.Step(`^it prompts a turn the model holds open and cancels it with inbox discard once the turn's request reached the model$`, s.whenCancelsHeld)
	ctx.Step(`^it closes the session and the connection$`, s.whenCloses)
	ctx.Step(`^create answered a session id whose cwd is the scratch workspace$`, s.thenCreated)
	ctx.Step(`^every model request went through the claude-code route's client, which listed the harness tool "([^"]*)" and was sent the prompt$`, s.thenThroughClient)
	ctx.Step(`^the host sent approval/request once per write, each for tool "([^"]*)" on the session, and the handler answered each$`, s.thenAsked)
	ctx.Step(`^the allowed write changed note\.txt to "([^"]*)" and the rejected write left it so with a refused tool result$`, s.thenWriteOutcomes)
	ctx.Step(`^both runs completed with the model's closing text$`, s.thenRunsCompleted)
	ctx.Step(`^the cancel reply followed the held turn's turn/end on the wire$`, s.thenCancelOrdered)
	ctx.Step(`^the host exited 0 once the connection closed, and no dsh process remains$`, s.thenExited)
	ctx.Step(`^every line the host wrote to stdout was a JSON-RPC 2\.0 frame \(M2-C-10\)$`, s.thenFramesOnly)
	ctx.Step(`^the run's session\.run\.completed text is the model's closing text$`, s.thenResultClosing)
	ctx.Step(`^it equals the last assistant message of the run in the persisted history read back from the scratch home's SQLite store$`, s.thenResultPersisted)
	ctx.Step(`^the persisted history read back has no gap or duplicate seq$`, s.thenNoGap)
}

func TestE2E_dh_s_17_acceptance_on_a_real_model_sdk_client_acceptance(t *testing.T) {
	scaOuterT = t
	// Build here, on t's own goroutine: launcher.Build fails t directly, which
	// must not happen from a godog scenario subtest.
	mod := filepath.Join("..", "..", "..")
	scaTesthookBin = launcher.Build(t, mod, "./cmd/dsh", "testhook")
	scaStandinBin = launcher.Build(t, mod, "./internal/testkit/claudestandin")
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
