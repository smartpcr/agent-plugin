//go:build e2e

package e2e

// Step definitions of
// dh_s_17_acceptance_on_a_real_model_session_and_agent_loop_acceptance.feature
// (DH-S-17 Stage 7.3: DH-E-SESSION-1, -2, DH-E-AGENT-1 to -4, TC-20s, M2-C-5).
//
// dsh is built with the testhook tag and driven over the SDK protocol as a
// client would (`dsh --home <scratch> --profile sdk`, raw JSON-RPC). Route
// "claude-code" is an in-process loopback model instance scripted per scenario;
// files are read by the real read tool (fs.tools, mounted by a testhook layer,
// since the sdk host serves no client tools yet) and
// the long tool is the testhook's testkit_sleep, and the shell child of
// DH-E-AGENT-3 a real pwsh or bash process started by shellexec.tools over the
// testhook shell registry. The persisted log is read back
// from the scratch home's SQLite store with the host stopped. The evidence rows
// are read from the committed DH-S-17 record and the gate's order from
// tools/harness-gate. Helpers shared with the Stage 7.2 suite: s17rPersisted,
// s17rSection, s17rCells, s17rEnv, s17rSHA and shkKeylessEnv.

import (
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
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

const (
	s17aRoute      = "claude-code"
	s17aCall       = 2 * time.Minute
	s17aTurn       = 3 * time.Minute
	s17aSleepLine  = "testkit_sleep: running"
	s17aShown      = "The first sentence was shown before the cancel."
	s17aNeverShown = " This sentence must never be shown."
	s17aReadTool   = "read" // fs.tools' read
	s17aContent    = "forty-two"
)

var (
	s17aOuterT *testing.T
	s17aDsh    string
	// s17aPromptCount matches the prompt count an evidence row's Artifacts cite.
	s17aPromptCount = regexp.MustCompile(`(?i)\bprompt[ _-]?count\s*[:= ]\s*\d+|\b\d+\s+prompts?\b`)
)

// s17aReply is a route instance's reply to request i (0-based) of the scenario.
type s17aReply func(i int, w http.ResponseWriter, r *http.Request)

// s17aServer is the ephemeral instance of route "claude-code": it answers with
// the scenario's script and keeps every request body it received.
type s17aServer struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies []model.Body
	script s17aReply
}

func newS17aServer() *s17aServer {
	r := &s17aServer{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		var b model.Body
		_ = json.Unmarshal(raw, &b)
		r.mu.Lock()
		r.bodies = append(r.bodies, b)
		i, script := len(r.bodies)-1, r.script
		r.mu.Unlock()
		if script == nil {
			http.Error(w, "unscripted request", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		script(i, w, req)
	}))
	return r
}

func (r *s17aServer) setScript(f s17aReply) {
	r.mu.Lock()
	r.script = f
	r.mu.Unlock()
}

func (r *s17aServer) received() []model.Body {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.bodies)
}

func s17aText(w io.Writer, text string) {
	data, _ := json.Marshal(map[string]string{"text": text})
	fmt.Fprintf(w, "event: text\ndata: %s\n\nevent: usage\ndata: {\"input\":8,\"output\":4}\n\nevent: finish\ndata: {\"reason\":\"stop\"}\n\n", data)
}

func s17aTool(w io.Writer, i int, name, args string) {
	fmt.Fprintf(w, "event: tool\ndata: {\"id\":\"toolu_s17a%04d\",\"name\":%q,\"args\":%s}\n\n", i, name, args)
	fmt.Fprint(w, "event: usage\ndata: {\"input\":8,\"output\":4}\n\nevent: finish\ndata: {\"reason\":\"tool_calls\"}\n\n")
}

func s17aRead(w io.Writer, i int, file string) {
	s17aTool(w, i, s17aReadTool, `{"file_path":`+strconv.Quote(file)+`}`)
}

// s17aHost is one running `dsh --home <root> --profile sdk` and the JSON-RPC
// client connected to it.
type s17aHost struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	conn   *jsonrpc.Conn
	stderr *testkit.Buffer
	done   chan struct{}

	mu   sync.Mutex
	runs []protocol.RunCompletedNotification
}

// s17aStart launches dsh on root in ws; stream declares the stream capability.
// The client declares no tool: the sdk host serves no client tools yet, so the
// scenarios' file reads go to the host's own read tool (s17aState.layer).
func s17aStart(root, ws string, stream bool) (*s17aHost, error) {
	h := &s17aHost{stderr: &testkit.Buffer{}, done: make(chan struct{})}
	h.cmd = exec.Command(s17aDsh, "--home", root, "--profile", "sdk")
	h.cmd.Dir, h.cmd.Env, h.cmd.Stderr = ws, shkKeylessEnv(os.Environ()), h.stderr
	var err error
	if h.stdin, err = h.cmd.StdinPipe(); err != nil {
		return nil, err
	}
	out, err := h.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := h.cmd.Start(); err != nil {
		return nil, err
	}
	go func() { defer close(h.done); _ = h.cmd.Wait() }()
	h.conn = jsonrpc.Serve(context.Background(), out, h.stdin, jsonrpc.Options{OnNotify: h.notify})
	var init protocol.InitializeResult
	if err := h.call("initialize", protocol.InitializeParams{
		ProtocolVersions: []int{protocol.Version},
		ClientInfo:       protocol.Implementation{Name: "dsh-e2e-s17a", Version: "1"},
		Capabilities:     protocol.ClientCapabilities{Stream: stream},
	}, &init); err != nil {
		h.kill()
		return nil, err
	}
	return h, nil
}

func (h *s17aHost) notify(method string, raw json.RawMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if method == "session.run.completed" {
		var n protocol.RunCompletedNotification
		if json.Unmarshal(raw, &n) == nil {
			h.runs = append(h.runs, n)
		}
	}
}

func (h *s17aHost) call(method string, params, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), s17aCall)
	defer cancel()
	if err := h.conn.Call(ctx, method, params, result); err != nil {
		return fmt.Errorf("%s: %v\nstderr:\n%s", method, err, h.stderr)
	}
	return nil
}

// until polls cond until it holds, the host exits or within passes.
func (h *s17aHost) until(within time.Duration, what string, cond func() bool) error {
	deadline := time.Now().Add(within)
	for {
		h.mu.Lock()
		ok := cond()
		h.mu.Unlock()
		if ok {
			return nil
		}
		select {
		case <-h.done:
			return fmt.Errorf("dsh exited (%v) before %s\nstderr:\n%s", h.cmd.ProcessState, what, h.stderr)
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no %s within %s\nstderr:\n%s", what, within, h.stderr)
		}
	}
}

func (h *s17aHost) awaitRun(session, msg string) (protocol.RunCompletedNotification, error) {
	var got protocol.RunCompletedNotification
	err := h.until(s17aTurn, "run result of "+msg, func() bool {
		for _, r := range h.runs {
			if r.SessionID == session && r.Run.MessageID == msg {
				got = r
				return true
			}
		}
		return false
	})
	return got, err
}

// history reads every stored event of session, page by page from seq 0.
func (h *s17aHost) history(session string) ([]protocol.Event, error) {
	type params struct {
		SessionID string `json:"sessionId"`
		AfterSeq  int64  `json:"afterSeq"`
		Limit     int    `json:"limit"`
	}
	var evs []protocol.Event
	for after := int64(-1); ; {
		var page protocol.SessionHistoryResult
		if err := h.call("session/history", params{session, after, 1000}, &page); err != nil {
			return nil, err
		}
		evs = append(evs, page.Events...)
		if !page.More {
			return evs, nil
		}
		if len(page.Events) == 0 {
			return nil, errors.New("session/history said more but returned no event")
		}
		after = page.Events[len(page.Events)-1].Seq
	}
}

func (h *s17aHost) sleeping() error {
	return h.until(time.Minute, "running testkit_sleep", func() bool { return strings.Contains(h.stderr.String(), s17aSleepLine) })
}

// kill ends dsh with no grace, as a crash would; testkit_sleep runs inside it.
func (h *s17aHost) kill() {
	_ = h.cmd.Process.Kill()
	<-h.done
	_ = h.conn.Close()
}

// stop asks dsh to shut down and ends its input, as a client's close does, and
// waits for it to exit within its shutdown bound.
func (h *s17aHost) stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), s17aCall)
	defer cancel()
	_ = h.conn.Call(ctx, "shutdown", nil, nil)
	_ = h.stdin.Close()
	bound := profile.DefaultLimits().ShutdownTimeout + 10*time.Second
	select {
	case <-h.done:
		_ = h.conn.Close()
		return nil
	case <-time.After(bound):
		h.kill()
		return fmt.Errorf("dsh did not exit within %s of its stop\nstderr:\n%s", bound, h.stderr)
	}
}

// s17aEvent is one line of a persisted log, as exported from the SQLite store.
type s17aEvent struct {
	Seq  *int64 `json:"seq"`
	Type string `json:"type"`
	Data string `json:"data"`
}

type s17aState struct {
	repo, module string
	route        *s17aServer
	root, ws     string
	host         *s17aHost
	hosts        []*s17aHost
	session      string
	prompts      int
	before       []protocol.Event // the history before a stop or a kill
	after        []protocol.Event // the history after it
	resumed      protocol.SessionResumeResult
	runs         []protocol.RunCompletedNotification
	logs         [][]s17aEvent // the persisted logs of the scenario
	rebuilt      [][]string    // requests rebuilt from the persisted log, per rebuild
	digests      []string
	cutOff       bool // dsh closed the cancelled turn's response (AGENT-2)
	sleepCall    string
	children     []int // the pids the shell commands recorded (AGENT-3)

	gateOut string
}

func (s *s17aState) start(stream bool) error {
	h, err := s17aStart(s.root, s.ws, stream)
	if err != nil {
		return err
	}
	s.host, s.hosts = h, append(s.hosts, h)
	return nil
}

func (s *s17aState) create(permission string) error {
	var r protocol.SessionCreateResult
	if err := s.host.call("session/create", protocol.SessionCreateParams{Cwd: s.ws, PermissionMode: permission,
		Model: &protocol.ModelRoute{Provider: s17aRoute, Model: model.ModelName}}, &r); err != nil {
		return err
	}
	if s.session = r.SessionID; s.session == "" {
		return errors.New("session/create named no session")
	}
	return nil
}

func (s *s17aState) prompt(text string) (string, error) {
	var r protocol.SessionPromptResult
	if err := s.host.call("session/prompt", protocol.SessionPromptParams{SessionID: s.session,
		Content: []protocol.ContentBlock{{Type: "text", Text: text}}}, &r); err != nil {
		return "", err
	}
	if r.MessageID == "" {
		return "", errors.New("session/prompt returned no message id")
	}
	s.prompts++
	return r.MessageID, nil
}

func (s *s17aState) run(text string) (protocol.RunCompletedNotification, error) {
	msg, err := s.prompt(text)
	if err != nil {
		return protocol.RunCompletedNotification{}, err
	}
	run, err := s.host.awaitRun(s.session, msg)
	s.runs = append(s.runs, run)
	return run, err
}

// persist stops the host when it still runs and reads the persisted log back
// from the SQLite store; with rebuild it also rebuilds every settled request.
func (s *s17aState) persist(rebuild bool) ([]s17aEvent, error) {
	if s.host != nil {
		select {
		case <-s.host.done:
		default:
			if err := s.host.stop(); err != nil {
				return nil, err
			}
		}
	}
	raw, canon, digests, err := s17rPersisted(s.root, s.session, rebuild)
	if err != nil {
		return nil, err
	}
	var evs []s17aEvent
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e s17aEvent
		if json.Unmarshal([]byte(line), &e) == nil && e.Seq != nil && e.Type != "" {
			evs = append(evs, e)
		}
	}
	s.logs = append(s.logs, evs)
	if rebuild {
		s.rebuilt, s.digests = append(s.rebuilt, canon), digests
	}
	return evs, nil
}

func (s *s17aState) lastLog() []s17aEvent {
	if len(s.logs) == 0 {
		return nil
	}
	return s.logs[len(s.logs)-1]
}

func s17aTypes[E any](evs []E, typ func(E) string) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = typ(e)
	}
	return out
}

func s17aOf(evs []protocol.Event, types ...string) []protocol.Event {
	return slices.DeleteFunc(slices.Clone(evs), func(e protocol.Event) bool { return !slices.Contains(types, e.Type) })
}

func s17aIn(evs []protocol.Event, run protocol.RunCompletedNotification) []protocol.Event {
	return slices.DeleteFunc(slices.Clone(evs), func(e protocol.Event) bool { return e.Seq < run.FirstSeq || e.Seq > run.LastSeq })
}

type s17aReason struct {
	Reason struct {
		Kind  string `json:"kind"`
		Cause string `json:"cause"`
		Limit string `json:"limit"`
		Value int    `json:"value"`
	} `json:"reason"`
}

func s17aTurnEnd(evs []protocol.Event) (s17aReason, error) {
	var end s17aReason
	ends := s17aOf(evs, "turn/end")
	if len(ends) != 1 {
		return end, fmt.Errorf("%d turn/end, want 1: %v", len(ends), s17aTypes(evs, func(e protocol.Event) string { return e.Type }))
	}
	return end, json.Unmarshal(ends[0].Data, &end)
}

// s17aPaired checks every call of evs has exactly one tool/result naming its call
// id. A call is a durable tool/call (a host tool) or a tool_use block of an
// assistant/message (a client tool, which the log records no tool/call for).
func s17aPaired(evs []protocol.Event) error {
	calls, results := map[string]int64{}, map[string]int{}
	for _, e := range evs {
		switch e.Type {
		case "tool/call":
			var c struct {
				CallID string `json:"call_id"`
			}
			if err := json.Unmarshal(e.Data, &c); err != nil || c.CallID == "" {
				return fmt.Errorf("tool/call %d names no call: %s", e.Seq, e.Data)
			}
			calls[c.CallID] = e.Seq
		case "assistant/message":
			var m struct {
				Message *struct {
					Blocks []struct {
						ToolUse *struct {
							ID string `json:"id"`
						} `json:"tool_use"`
					} `json:"blocks"`
				} `json:"message"`
			}
			if err := json.Unmarshal(e.Data, &m); err != nil || m.Message == nil {
				continue
			}
			for _, b := range m.Message.Blocks {
				if b.ToolUse != nil {
					calls[b.ToolUse.ID] = e.Seq
				}
			}
		case "tool/result":
			var r struct {
				CallID string `json:"call_id"`
			}
			_ = json.Unmarshal(e.Data, &r)
			if _, ok := calls[r.CallID]; !ok {
				return fmt.Errorf("tool/result %d answers call %q, asked by no earlier event", e.Seq, r.CallID)
			}
			results[r.CallID]++
		}
	}
	for id, seq := range calls {
		if results[id] != 1 {
			return fmt.Errorf("call %s (seq %d) has %d tool/result, want 1", id, seq, results[id])
		}
	}
	return nil
}

// ---- Background ----

func (s *s17aState) givenBuilt() error {
	if s17aDsh == "" {
		return errors.New("the dsh build produced no binary")
	}
	return nil
}

func (s *s17aState) givenRoute(id string) error {
	if id != s17aRoute {
		return fmt.Errorf("this stage serves route %q, not %q", s17aRoute, id)
	}
	s.route = newS17aServer()
	s.root, s.ws = temphome.New(s17aOuterT).Root(), s17aOuterT.TempDir()
	return nil
}

// The layer kinds: the real read tool (fs.tools over fs.ops, fs.local and
// sandbox.policy) in the workspace; the long tool testkit_sleep; the real shell
// tools (shellexec.tools over the testhook shell registry and sandbox.local)
// running in the workspace.
const (
	s17aPlain = iota
	s17aFiles
	s17aSleep
	s17aShell
)

// layer writes the sdk layer of root: the route instance and the tools of kind.
func (s *s17aState) layer(kind int) {
	text := "- insert:\n    - id: route\n      name: llm.testkit-route\n      config:\n        id: " + strconv.Quote(s17aRoute) +
		"\n        url: " + strconv.Quote(s.route.srv.URL) + "\n"
	wd := "    - id: wd\n      name: session.testkit-workdir\n      config:\n        root: " + strconv.Quote(s.ws) + "\n"
	switch kind {
	case s17aFiles:
		text += "    - id: fs.local\n      name: fs.local\n      config:\n        lockDir: " + strconv.Quote(s17aOuterT.TempDir()) + "\n" + wd +
			"    - id: sandbox.policy\n      name: sandbox.policy\n      config:\n        defaultMode: workspace-write" +
			"\n    - id: fs.ops\n      name: fs.ops\n    - id: fs.tools\n      name: fs.tools\n"
	case s17aSleep:
		text += "    - id: sleep\n      name: tools.testkit-sleep\n"
	case s17aShell:
		// danger-full-access: the stop and kill paths are under test, not the
		// confinement, and an unconfined start runs on every lane's chain.
		text += "    - id: fs.local\n      name: fs.local\n      config:\n        lockDir: " + strconv.Quote(s17aOuterT.TempDir()) + "\n" + wd +
			"    - id: sandbox.policy\n      name: sandbox.policy\n      config:\n        defaultMode: danger-full-access" +
			"\n    - id: sandbox.local\n      name: sandbox.local\n      config:\n        canaryDir: " + strconv.Quote(s17aOuterT.TempDir()) +
			"\n    - id: shells\n      name: shellexec.testkit-shells" +
			"\n    - id: shellexec.tools\n      name: shellexec.tools\n"
	}
	temphome.Layer(s17aOuterT, s.root, profile.SDK, text)
}

func (s *s17aState) givenToolHost(tool, files string) error {
	if tool != s17aReadTool {
		return fmt.Errorf("the file tool is %s, not %s", s17aReadTool, tool)
	}
	for _, f := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(`"`+files+`"`, -1) {
		if err := os.WriteFile(filepath.Join(s.ws, f[1]), []byte("the content of "+f[1]+": "+s17aContent+"\n"), 0o600); err != nil {
			return err
		}
	}
	s.layer(s17aFiles)
	if err := s.start(false); err != nil {
		return err
	}
	return s.create("")
}

func (s *s17aState) givenSleepHost(tool string) error {
	if tool != "testkit_sleep" {
		return fmt.Errorf("the long tool is testkit_sleep, not %s", tool)
	}
	s.layer(s17aSleep)
	s.route.setScript(func(i int, w http.ResponseWriter, _ *http.Request) {
		if i == 0 {
			s17aTool(w, i, "testkit_sleep", "{}")
			return
		}
		s17aText(w, "The command was cut off when the host stopped.")
	})
	if err := s.start(false); err != nil {
		return err
	}
	return s.create("")
}

func (s *s17aState) givenStreamHost() error {
	s.layer(s17aPlain)
	if err := s.start(true); err != nil {
		return err
	}
	return s.create("")
}

// s17aShellTool is the shell tool of this lane and the command it runs: the
// command writes its own process id to pid.txt in the workspace, then sleeps 60
// seconds in that same process.
func s17aShellTool() (name, command string) {
	if runtime.GOOS == "windows" {
		return "pwsh", "Set-Content -NoNewline -Path pid.txt -Value $PID; Start-Sleep -Seconds 60"
	}
	return "bash", "echo $$ > pid.txt; exec sleep 60"
}

func (s *s17aState) givenShellHost() error {
	s.layer(s17aShell)
	name, command := s17aShellTool()
	args, _ := json.Marshal(map[string]string{"command": command, "description": "record the pid and sleep 60 seconds"})
	s.route.setScript(func(i int, w http.ResponseWriter, _ *http.Request) {
		if i == 0 {
			s17aTool(w, i, name, string(args))
			return
		}
		s17aText(w, "The command was cut off when the host stopped.")
	})
	if err := s.start(false); err != nil {
		return err
	}
	return s.create("")
}

// shellRunning prompts the shell command and waits until its process has
// recorded its id and its tool/call is durable; the child is killed at cleanup
// should it outlive the scenario.
func (s *s17aState) shellRunning() error {
	if _, err := s.prompt("Run the long shell command."); err != nil {
		return err
	}
	pidFile := filepath.Join(s.ws, "pid.txt")
	var pid int
	if err := s.host.until(time.Minute, "the shell child's pid.txt", func() bool {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(data)))
		pid = n
		return err == nil && n > 0
	}); err != nil {
		return err
	}
	s.children = append(s.children, pid)
	s17aOuterT.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil && s17aAlive(pid) {
			_ = p.Kill()
		}
	})
	return s.host.until(time.Minute, "a durable tool/call of the shell command", func() bool {
		evs, err := s.host.history(s.session)
		return err == nil && len(s17aOf(evs, "tool/call")) == 1
	})
}

// s17aAlive reports whether process pid runs.
func s17aAlive(pid int) bool {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
		return err == nil && strings.Contains(string(out), fmt.Sprintf(`"%d"`, pid))
	}
	return exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil
}

// ---- DH-E-SESSION-1 ----

func (s *s17aState) whenThreeTurns(file, _ string) error {
	s.route.setScript(func(i int, w http.ResponseWriter, _ *http.Request) {
		switch i {
		case 0:
			s17aRead(w, i, file)
		case 1:
			s17aText(w, "The notes say the answer is forty-two.")
		default:
			s17aText(w, fmt.Sprintf("Answer %d: forty-two.", i))
		}
	})
	for _, p := range []string{"Read " + file + " and tell me the answer.", "In one word, what was the answer?", "Thank you."} {
		run, err := s.run(p)
		if err != nil {
			return err
		}
		if run.FinishReason != "completed" {
			return fmt.Errorf("the turn of %q finished %q, want completed", p, run.FinishReason)
		}
	}
	var err error
	if s.before, err = s.host.history(s.session); err != nil {
		return err
	}
	if reads, err := s17aReads(s.before); err != nil || !slices.Equal(reads, []string{file}) {
		return fmt.Errorf("the first turn read %v, want %s returning its content: %v", reads, file, err)
	}
	return nil
}

func (s *s17aState) whenStoppedAndReadBack() error {
	if _, err := s.persist(true); err != nil {
		return err
	}
	if _, err := s.persist(true); err != nil {
		return err
	}
	if err := s.start(false); err != nil {
		return err
	}
	var err error
	s.after, err = s.host.history(s.session)
	return err
}

func (s *s17aState) thenHistoryEqual() error {
	if len(s.before) == 0 || len(s.after) != len(s.before) {
		return fmt.Errorf("history read back has %d events, before the stop %d", len(s.after), len(s.before))
	}
	for i := range s.before {
		a, _ := json.Marshal(s.before[i])
		b, _ := json.Marshal(s.after[i])
		if string(a) != string(b) {
			return fmt.Errorf("event %d differs:\nbefore: %s\nafter:  %s", i, a, b)
		}
	}
	return nil
}

func (s *s17aState) thenDigestsReceived() error {
	if len(s.digests) == 0 {
		if _, err := s.persist(true); err != nil {
			return err
		}
	}
	got := s.route.received()
	settled := 0
	for _, e := range s.lastLog() {
		if e.Type == "assistant/message" || e.Type == "assistant/attempt" {
			settled++
		}
	}
	if len(got) != len(s.digests) || settled != len(s.digests) {
		return fmt.Errorf("the route received %d requests, the log settles %d and rebuilds %d", len(got), settled, len(s.digests))
	}
	for i, d := range s.digests {
		if got[i].Digest != d || got[i].Route != s17aRoute {
			return fmt.Errorf("request %d rebuilt with digest %s; the route %q received %s", i, d, got[i].Route, got[i].Digest)
		}
	}
	return nil
}

// thenRebuildTwice rebuilds the requests from the persisted log until there are
// two rebuilds and holds the last two to the same bytes, request for request.
func (s *s17aState) thenRebuildTwice() error {
	for len(s.rebuilt) < 2 {
		if _, err := s.persist(true); err != nil {
			return err
		}
	}
	a, b := s.rebuilt[len(s.rebuilt)-2], s.rebuilt[len(s.rebuilt)-1]
	if len(a) == 0 || len(a) != len(b) {
		return fmt.Errorf("the two rebuilds hold %d and %d requests, want the same non-zero number", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			return fmt.Errorf("request %d rebuilt twice differs:\n%s\n---\n%s", i, a[i], b[i])
		}
	}
	return nil
}

func s17aNoGap(evs []s17aEvent) error {
	if len(evs) == 0 {
		return errors.New("the persisted log is empty")
	}
	for i, e := range evs {
		if *e.Seq != int64(i) {
			return fmt.Errorf("the persisted log has seq %d at position %d: a gap", *e.Seq, i)
		}
	}
	return nil
}

func (s *s17aState) thenNoGap() error {
	if _, err := s.persist(false); err != nil {
		return err
	}
	return s17aNoGap(s.lastLog())
}

func (s *s17aState) thenPromptCount(id string, n int) error {
	if len(s.logs) == 0 {
		if _, err := s.persist(false); err != nil {
			return err
		}
	}
	// One home's last log; AGENT-3 runs one prompt on each of two homes.
	logs := [][]s17aEvent{s.lastLog()}
	if id == "DH-E-AGENT-3" {
		logs = s.agent3Logs()
	}
	users := 0
	for _, log := range logs {
		for _, e := range log {
			if e.Type == "user/message" {
				users++
			}
		}
	}
	record := fmt.Sprintf("| %s | Linux (E1) | e2e | in-process | claude-code | %d prompts |", id, s.prompts)
	s17aOuterT.Logf("record: %s", record)
	if s.prompts != n || users != n || !s17aPromptCount.MatchString(record) {
		return fmt.Errorf("%s sent %d prompts, its persisted log holds %d user/message, want %d (record %q)", id, s.prompts, users, n, record)
	}
	return nil
}

// ---- DH-E-SESSION-2 ----

func (s *s17aState) whenDurableCall(_ string) error {
	if _, err := s.prompt("Run the long command with testkit_sleep."); err != nil {
		return err
	}
	if err := s.host.sleeping(); err != nil {
		return err
	}
	// The tool body runs after its tool/call is committed, so the call is durable
	// now; session/history reads it back from the store.
	evs, err := s.host.history(s.session)
	if err != nil {
		return err
	}
	calls := s17aOf(evs, "tool/call")
	if len(calls) != 1 {
		return fmt.Errorf("%d durable tool/call while testkit_sleep runs, want 1", len(calls))
	}
	var c struct {
		CallID string `json:"call_id"`
	}
	_ = json.Unmarshal(calls[0].Data, &c)
	s.sleepCall, s.before = c.CallID, evs
	return nil
}

func (s *s17aState) whenKilled() error {
	s.host.kill()
	return nil
}

func (s *s17aState) resume() (protocol.SessionResumeResult, error) {
	var r protocol.SessionResumeResult
	err := s.host.call("session/resume", protocol.SessionResumeParams{SessionID: s.session,
		Model: &protocol.ModelRoute{Provider: s17aRoute, Model: model.ModelName}}, &r)
	return r, err
}

func (s *s17aState) whenResumed() error {
	if err := s.start(false); err != nil {
		return err
	}
	var err error
	if s.resumed, err = s.resume(); err != nil {
		return err
	}
	s.after, err = s.host.history(s.session)
	return err
}

func (s *s17aState) thenClosedInterrupted() error {
	if !s.resumed.ClosedInterruptedTurn {
		return errors.New("session/resume closedInterruptedTurn = false, want true")
	}
	return nil
}

func (s *s17aState) thenPrefix() error {
	if len(s.before) == 0 || len(s.after) < len(s.before) {
		return fmt.Errorf("history after the resume has %d events, before the kill %d", len(s.after), len(s.before))
	}
	for i := range s.before {
		a, _ := json.Marshal(s.before[i])
		b, _ := json.Marshal(s.after[i])
		if string(a) != string(b) {
			return fmt.Errorf("event %d changed across the kill:\nbefore: %s\nafter:  %s", i, a, b)
		}
	}
	return nil
}

func (s *s17aState) thenClosers() error {
	closers := s.after[len(s.before):]
	types := s17aTypes(closers, func(e protocol.Event) string { return e.Type })
	if !slices.Equal(types, []string{"tool/result", "step/end", "turn/end", "session/end-seed"}) {
		return fmt.Errorf("the resume appended %v, want tool/result, step/end, turn/end, session/end-seed", types)
	}
	if d := string(closers[0].Data); !strings.Contains(d, "tools/outcome-unknown") || s.sleepCall == "" || !strings.Contains(d, s.sleepCall) {
		return fmt.Errorf("the closing tool/result is not outcome-unknown for call %q: %s", s.sleepCall, d)
	}
	var end s17aReason
	if err := json.Unmarshal(closers[2].Data, &end); err != nil || end.Reason.Kind != "interrupted" {
		return fmt.Errorf("the closing turn/end reads %s, want interrupted", closers[2].Data)
	}
	return nil
}

func (s *s17aState) thenSecondResumeNoCloser() error {
	s.host.kill()
	if err := s.start(false); err != nil {
		return err
	}
	r, err := s.resume()
	if err != nil {
		return err
	}
	if r.ClosedInterruptedTurn {
		return errors.New("the second resume closed an interrupted turn again")
	}
	evs, err := s.host.history(s.session)
	if err != nil {
		return err
	}
	for _, e := range evs[len(s.after):] {
		if slices.Contains([]string{"tool/result", "step/end", "turn/end"}, e.Type) {
			return fmt.Errorf("the second resume appended the closer %s: %s", e.Type, e.Data)
		}
	}
	return nil
}

func (s *s17aState) thenNextTurnCompletes() error {
	run, err := s.run("What happened to the command you ran?")
	if err != nil {
		return err
	}
	evs, err := s.host.history(s.session)
	if err != nil {
		return err
	}
	in := s17aIn(evs, run)
	end, err := s17aTurnEnd(in)
	if err != nil {
		return err
	}
	if end.Reason.Kind != "completed" || len(s17aOf(in, "assistant/message")) == 0 {
		return fmt.Errorf("the turn after the resume ended %+v with %d assistant/message", end.Reason, len(s17aOf(in, "assistant/message")))
	}
	return nil
}

// ---- DH-E-AGENT-1 ----

func (s *s17aState) whenOnePrompt(file string) error {
	s.route.setScript(func(i int, w http.ResponseWriter, _ *http.Request) {
		if i == 0 {
			s17aRead(w, i, file)
			return
		}
		s17aText(w, "The note says forty-two.")
	})
	_, err := s.run("Read " + file + " and say what it says.")
	if err != nil {
		return err
	}
	s.after, err = s.host.history(s.session)
	return err
}

func (s *s17aState) thenOneTurn() error {
	if n := len(s17aOf(s.after, "turn/start")); n != 1 {
		return fmt.Errorf("%d turn/start, want 1", n)
	}
	end, err := s17aTurnEnd(s.after)
	if err != nil {
		return err
	}
	if end.Reason.Kind != "completed" {
		return fmt.Errorf("the turn ended %+v, want completed", end.Reason)
	}
	return nil
}

func (s *s17aState) thenBracketed(min int) error {
	open, steps := false, 0
	for _, e := range s17aOf(s.after, "step/start", "step/end") {
		switch {
		case e.Type == "step/start" && !open:
			open, steps = true, steps+1
		case e.Type == "step/end" && open:
			open = false
		default:
			return fmt.Errorf("%s at seq %d out of order", e.Type, e.Seq)
		}
	}
	if open || steps < min {
		return fmt.Errorf("%d steps (one left open: %v), want at least %d closed", steps, open, min)
	}
	return nil
}

func (s *s17aState) thenPairedSettled() error {
	if err := s17aPaired(s.after); err != nil {
		return err
	}
	if reads, err := s17aReads(s.after); err != nil || len(reads) == 0 {
		return fmt.Errorf("the turn's reads %v did not return the file: %v", reads, err)
	}
	// Each step's attempts are settled exactly once: the last by an
	// assistant/message, any earlier one by an assistant/attempt.
	var settled []string
	for _, e := range s.after {
		switch e.Type {
		case "step/start":
			settled = nil
		case "assistant/message", "assistant/attempt":
			settled = append(settled, e.Type)
		case "step/end":
			if len(settled) == 0 || settled[len(settled)-1] != "assistant/message" || slices.Index(settled, "assistant/message") != len(settled)-1 {
				return fmt.Errorf("a step closed at seq %d settles %v", e.Seq, settled)
			}
		}
	}
	return nil
}

// ---- DH-E-AGENT-2 ----

// The sdk host does not send session.stream yet (wire/sdkserver/forward.go), so
// "shown" is observed where the chunk leaves the route: the route flushes the
// first chunk, holds the response open, and reports when dsh cuts it off.
func (s *s17aState) whenCancelAfterFirstChunk() error {
	sent, cut := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	s.route.setScript(func(i int, w http.ResponseWriter, r *http.Request) {
		if i > 0 {
			s17aText(w, "I said: "+s17aShown)
			return
		}
		data, _ := json.Marshal(map[string]string{"text": s17aShown})
		fmt.Fprintf(w, "event: text\ndata: %s\n\n", data)
		w.(http.Flusher).Flush()
		close(sent)
		select {
		case <-r.Context().Done():
			close(cut)
			return
		case <-release:
		}
		fmt.Fprintf(w, "event: text\ndata: {\"text\":%q}\n\n", s17aNeverShown)
	})
	defer close(release)
	msg, err := s.prompt("Write a very long answer.")
	if err != nil {
		return err
	}
	select {
	case <-sent:
	case <-time.After(time.Minute):
		return errors.New("the route was asked for no answer within 1m")
	}
	// Loopback delivery of a flushed chunk takes far less than this.
	time.Sleep(500 * time.Millisecond)
	var c protocol.SessionCancelResult
	if err := s.host.call("session/cancel", protocol.SessionCancelParams{SessionID: s.session, Scope: "turn", Inbox: "keep"}, &c); err != nil {
		return err
	}
	if !c.Cancelled {
		return errors.New("session/cancel cancelled nothing")
	}
	run, err := s.host.awaitRun(s.session, msg)
	s.runs = append(s.runs, run)
	if err != nil {
		return err
	}
	select {
	case <-cut:
		s.cutOff = true
	case <-time.After(10 * time.Second):
	}
	return nil
}

func (s *s17aState) whenRepeat(text string) error {
	if _, err := s.run(text); err != nil {
		return err
	}
	var err error
	s.after, err = s.host.history(s.session)
	return err
}

func (s *s17aState) thenAbortedUser() error {
	end, err := s17aTurnEnd(s17aIn(s.after, s.runs[0]))
	if err != nil {
		return err
	}
	if end.Reason.Kind != "aborted" || end.Reason.Cause != "user" {
		return fmt.Errorf("turn 1 ended %+v, want aborted with cause user", end.Reason)
	}
	return nil
}

func (s *s17aState) thenKeptShown() error {
	settled := s17aOf(s17aIn(s.after, s.runs[0]), "assistant/message", "assistant/attempt")
	if len(settled) != 1 || settled[0].Type != "assistant/message" {
		return fmt.Errorf("turn 1 settles %v, want one assistant/message", s17aTypes(settled, func(e protocol.Event) string { return e.Type }))
	}
	var m struct {
		Message *struct {
			Blocks []struct {
				Text *struct {
					Text string `json:"text"`
				} `json:"text"`
				ToolUse json.RawMessage `json:"tool_use"`
			} `json:"blocks"`
		} `json:"message"`
	}
	if err := json.Unmarshal(settled[0].Data, &m); err != nil || m.Message == nil {
		return fmt.Errorf("the interrupted assistant/message has no message: %s", settled[0].Data)
	}
	var kept strings.Builder
	for _, b := range m.Message.Blocks {
		if len(b.ToolUse) > 0 && string(b.ToolUse) != "null" {
			return fmt.Errorf("the interrupted message carries a tool call: %s", settled[0].Data)
		}
		if b.Text != nil {
			kept.WriteString(b.Text.Text)
		}
	}
	if kept.String() != s17aShown {
		return fmt.Errorf("the interrupted message holds %q, want the chunk the route sent %q", kept.String(), s17aShown)
	}
	return nil
}

func (s *s17aState) thenNoLateChunk() error {
	if !s.cutOff {
		return errors.New("dsh did not cut off the route's response after the cancel: the stream was not stopped")
	}
	for _, e := range s.after {
		if strings.Contains(string(e.Data), strings.TrimSpace(s17aNeverShown)) {
			return fmt.Errorf("event %d (%s) holds text sent only after the cancel", e.Seq, e.Type)
		}
	}
	return nil
}

func (s *s17aState) thenNextRequestCarriesShown() error {
	if _, err := s.persist(true); err != nil {
		return err
	}
	reqs := s.rebuilt[len(s.rebuilt)-1]
	if len(reqs) != 2 {
		return fmt.Errorf("%d requests rebuilt from the log, want the cancelled one and turn 2's", len(reqs))
	}
	if !strings.Contains(reqs[1], s17aShown) {
		return fmt.Errorf("turn 2's request rebuilt from the log does not carry %q:\n%s", s17aShown, reqs[1])
	}
	return nil
}

// ---- DH-E-AGENT-3 ----

func (s *s17aState) whenRunAStops(_ string) error {
	if err := s.shellRunning(); err != nil {
		return err
	}
	if err := s.host.stop(); err != nil {
		return err
	}
	_, err := s.persist(false)
	return err
}

// thenChildGone: the shell command run A started does not outlive the stopped
// host. dsh has exited when stop returns; a moment is left for the system to
// reap the process the stop ladder ended.
func (s *s17aState) thenChildGone() error {
	if len(s.children) == 0 {
		return errors.New("no shell child recorded its pid")
	}
	pid := s.children[len(s.children)-1]
	deadline := time.Now().Add(3 * time.Second)
	for s17aAlive(pid) {
		if time.Now().After(deadline) {
			return fmt.Errorf("the shell command (pid %d) outlived the stopped host", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func (s *s17aState) thenRunAHonest() error {
	types := s17aTypes(s.lastLog(), func(e s17aEvent) string { return e.Type })
	i := slices.Index(types, "tool/call")
	if i < 0 {
		return fmt.Errorf("run A's log has no tool/call: %v", types)
	}
	tail := slices.DeleteFunc(slices.Clone(types[i+1:]), func(t string) bool {
		return t != "tool/result" && t != "step/end" && t != "turn/end"
	})
	if !slices.Equal(tail, []string{"tool/result", "step/end", "turn/end"}) {
		return fmt.Errorf("after the call run A's log holds %v, want tool/result, step/end, turn/end", types[i+1:])
	}
	for _, e := range s.lastLog() {
		if e.Type == "turn/end" {
			var end s17aReason
			if err := json.Unmarshal([]byte(e.Data), &end); err != nil || end.Reason.Kind != "aborted" || end.Reason.Cause != "disposed" {
				return fmt.Errorf("run A's turn/end reads %s, want aborted with cause disposed", e.Data)
			}
		}
	}
	return nil
}

func (s *s17aState) thenLogNoGap() error { return s17aNoGap(s.lastLog()) }

func (s *s17aState) agent3Logs() [][]s17aEvent {
	if len(s.logs) < 2 {
		return s.logs
	}
	return [][]s17aEvent{s.logs[0], s.logs[len(s.logs)-1]}
}

func (s *s17aState) whenRunBKills(_ string) error {
	s.route.srv.Close() // run A's host has exited
	s.route = newS17aServer()
	s.root, s.ws = temphome.New(s17aOuterT).Root(), s17aOuterT.TempDir()
	if err := s.givenShellHost(); err != nil {
		return err
	}
	if err := s.shellRunning(); err != nil {
		return err
	}
	var err error
	if s.before, err = s.host.history(s.session); err != nil {
		return err
	}
	s.host.kill()
	if err := s.start(false); err != nil {
		return err
	}
	if s.resumed, err = s.resume(); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond) // a continued turn would ask the route by now
	if s.after, err = s.host.history(s.session); err != nil {
		return err
	}
	_, err = s.persist(false)
	return err
}

func (s *s17aState) thenRunBResumed() error {
	if !s.resumed.ClosedInterruptedTurn || len(s.after) <= len(s.before) {
		return fmt.Errorf("run B's resume closed no interrupted turn (%v, %d -> %d events)", s.resumed.ClosedInterruptedTurn, len(s.before), len(s.after))
	}
	added := s.after[len(s.before):]
	types := s17aTypes(added, func(e protocol.Event) string { return e.Type })
	if !slices.Equal(types, []string{"tool/result", "step/end", "turn/end", "session/end-seed"}) ||
		!strings.Contains(string(added[0].Data), "tools/outcome-unknown") {
		return fmt.Errorf("run B's resume appended %v (%s), want an outcome-unknown tool/result and the closers", types, added[0].Data)
	}
	if n := len(s.route.received()); n != 1 {
		return fmt.Errorf("the route received %d requests in run B, want 1: the turn was continued", n)
	}
	return nil
}

// ---- DH-E-AGENT-4 ----

func (s *s17aState) whenBudgetLoop(a, b, c string) error {
	files := []string{a, b, c}
	var second sync.Mutex
	turn2 := -1
	s.route.setScript(func(i int, w http.ResponseWriter, _ *http.Request) {
		second.Lock()
		t2 := turn2
		second.Unlock()
		switch {
		case t2 < 0:
			s17aRead(w, i, files[i%len(files)])
		case i == t2:
			s17aRead(w, i, "4.txt")
		default:
			s17aText(w, "I have read 4.txt as well.")
		}
	})
	if _, err := s.run("Read every file, one at a time, and never stop."); err != nil {
		return err
	}
	second.Lock()
	turn2 = len(s.route.received())
	second.Unlock()
	return nil
}

func (s *s17aState) whenContinue(text, _ string) error {
	if _, err := s.run(text); err != nil {
		return err
	}
	var err error
	s.after, err = s.host.history(s.session)
	return err
}

func (s *s17aState) thenBudgetDefault() error {
	evs := s17aIn(s.after, s.runs[0])
	end, err := s17aTurnEnd(evs)
	if err != nil {
		return err
	}
	want := driver.DefaultBudget().MaxSteps
	if end.Reason.Kind != "budget" || end.Reason.Limit != "steps" || end.Reason.Value != want {
		return fmt.Errorf("turn 1 ended %+v, want budget{limit: steps, value: %d}", end.Reason, want)
	}
	if n := len(s17aOf(evs, "step/start")); n != want {
		return fmt.Errorf("turn 1 ran %d steps under a step budget of %d", n, want)
	}
	return nil
}

func (s *s17aState) thenBudgetPaired() error {
	if err := s17aPaired(s.after); err != nil {
		return err
	}
	reads, err := s17aReads(s.after)
	if err != nil {
		return err
	}
	if n := len(s17aOf(s.after, "tool/result")); n != len(reads) || n == 0 {
		return fmt.Errorf("%d tool/result for %d read calls", n, len(reads))
	}
	return nil
}

func (s *s17aState) thenProgress() error {
	first, err := s17aReads(s17aIn(s.after, s.runs[0]))
	if err != nil {
		return err
	}
	later, err := s17aReads(s17aIn(s.after, s.runs[1]))
	if err != nil {
		return err
	}
	for _, name := range later {
		if !slices.Contains(first, name) {
			return nil
		}
	}
	return fmt.Errorf("turn 2 read %v, all read in turn 1 already", later)
}

// s17aReads lists the files the durable read calls of evs name, in log order,
// and checks each call's tool/result is a success holding the file's content.
func s17aReads(evs []protocol.Event) ([]string, error) {
	files, byCall := []string{}, map[string]string{}
	for _, e := range evs {
		switch e.Type {
		case "tool/call":
			var c struct {
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			var a struct {
				FilePath string `json:"file_path"`
			}
			if err := json.Unmarshal(e.Data, &c); err != nil || c.Name != s17aReadTool || json.Unmarshal([]byte(c.Arguments), &a) != nil {
				return nil, fmt.Errorf("tool/call %d is not a read of a file: %s", e.Seq, e.Data)
			}
			files, byCall[c.CallID] = append(files, a.FilePath), a.FilePath
		case "tool/result":
			var r struct {
				CallID  string `json:"call_id"`
				IsError bool   `json:"is_error"`
			}
			_ = json.Unmarshal(e.Data, &r)
			if f, ok := byCall[r.CallID]; ok && (r.IsError || !strings.Contains(string(e.Data), s17aContent)) {
				return nil, fmt.Errorf("the read of %s did not return its content: %s", f, e.Data)
			}
		}
	}
	return files, nil
}

// ---- The gate's order ----

func (s *s17aState) givenIndex() error {
	if _, err := os.Stat(filepath.Join(s.repo, "test", "harness", "acceptance-index.yaml")); err != nil {
		return err
	}
	return nil
}

func (s *s17aState) gateCmd(args ...string) *exec.Cmd {
	cmd := exec.Command("go", args...)
	cmd.Dir, cmd.Env = filepath.Join(s.repo, "tools", "harness-gate"), s17rEnv()
	return cmd
}

func (s *s17aState) whenRequire() error {
	out, err := s.gateCmd("run", ".", "require", "--stage", "DH-S-17", "--root", s.repo).CombinedOutput()
	s.gateOut = string(out)
	if err != nil {
		return fmt.Errorf("harness-gate require: %v\n%s", err, out)
	}
	return nil
}

func s17aIDs(list string) []string {
	var ids []string
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(list, -1) {
		ids = append(ids, m[1])
	}
	return ids
}

func (s *s17aState) thenListed(list string) error {
	lines := strings.FieldsFunc(s.gateOut, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	for _, id := range s17aIDs(`"` + list + `"`) {
		if !slices.Contains(lines, id) {
			return fmt.Errorf("require does not list %s:\n%s", id, s.gateOut)
		}
	}
	return nil
}

func (s *s17aState) thenLate(stage, a, b string) error {
	src, err := os.ReadFile(filepath.Join(s.repo, "tools", "harness-gate", "scenariorun.go"))
	if err != nil {
		return err
	}
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	_, body, ok := strings.Cut(text, "var stageOrder = ")
	if !ok {
		return errors.New("scenariorun.go has no stageOrder")
	}
	_, entry, ok := strings.Cut(body, strconv.Quote(stage)+": {")
	if !ok {
		return fmt.Errorf("stageOrder has no %s", stage)
	}
	entry, _, _ = strings.Cut(entry, "\n\t},")
	late := regexp.MustCompile(`Late:\s*\[\]string\{([^}]*)\}`).FindStringSubmatch(entry)
	if late == nil || !strings.Contains(late[1], strconv.Quote(a)) || !strings.Contains(late[1], strconv.Quote(b)) {
		return fmt.Errorf("the %s entry of stageOrder holds no Late phase of %s and %s:\n%s", stage, a, b, entry)
	}
	for _, id := range []string{"DH-E-SESSION-1", "DH-E-AGENT-1", "DH-E-AGENT-2", "DH-E-AGENT-4"} {
		if strings.Contains(entry, strconv.Quote(id)) {
			return fmt.Errorf("the single-process %s is ordered in stageOrder; it belongs to the middle phase", id)
		}
	}
	return nil
}

func (s *s17aState) thenPhaseTest(name string) error {
	out, err := s.gateCmd("test", "-run", "^"+name+"$", "-v", ".").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: "+name) {
		return fmt.Errorf("%s did not pass (%v):\n%s", name, err, out)
	}
	return nil
}

// ---- The evidence rows ----

type s17aRows struct {
	record string
	rows   map[string]map[string]string
}

var s17aEvidence s17aRows

func (s *s17aState) givenRecord(rel, status string) error {
	cmd := exec.Command("git", "show", "HEAD:"+rel)
	cmd.Dir = s.repo
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git show HEAD:%s: %v", rel, err)
	}
	md := strings.ReplaceAll(string(out), "\r\n", "\n")
	if strings.Count(md, "\n"+status+"\n") != 1 {
		return fmt.Errorf("the record does not read exactly one %q", status)
	}
	s17aEvidence = s17aRows{record: md}
	return nil
}

func (s *s17aState) whenRowsRead(list string) error {
	body, ok := s17rSection(s17aEvidence.record, "## Scenarios")
	if !ok {
		return errors.New(`the record has no "## Scenarios" section`)
	}
	s17aEvidence.rows = map[string]map[string]string{}
	var header []string
	for _, l := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "|") {
			continue
		}
		cells := s17rCells(l)
		switch {
		case header == nil:
			for _, c := range cells {
				header = append(header, strings.ToLower(c))
			}
		case strings.Trim(strings.Join(cells, ""), "-: ") == "" || len(cells) != len(header):
		default:
			r := map[string]string{}
			for i, h := range header {
				r[h] = cells[i]
			}
			if strings.EqualFold(r["leg"], "required") {
				if _, dup := s17aEvidence.rows[r["scenario"]]; dup {
					return fmt.Errorf("two required rows of %s", r["scenario"])
				}
				s17aEvidence.rows[r["scenario"]] = r
			}
		}
	}
	for _, id := range s17aIDs(`"` + list + `"`) {
		if s17aEvidence.rows[id] == nil {
			return fmt.Errorf("the record has no required row of %s", id)
		}
	}
	return nil
}

func s17aStageIDs() []string {
	return []string{"DH-E-SESSION-1", "DH-E-SESSION-2", "DH-E-AGENT-1", "DH-E-AGENT-2", "DH-E-AGENT-3", "DH-E-AGENT-4"}
}

func (s *s17aState) thenRowsPassedOrOwed() error {
	for _, id := range s17aStageIDs() {
		r := s17aEvidence.rows[id]
		res := strings.ToLower(strings.TrimSpace(r["result"]))
		switch {
		case res == "passed" || strings.HasPrefix(res, "passed ("):
			if !s17rSHA.MatchString(r["commit"]) || !s17aPromptCount.MatchString(r["artifacts"]) {
				return fmt.Errorf("%s reads passed without a commit SHA and a prompt count (M2-C-5): %v", id, r)
			}
		case strings.HasPrefix(res, "not run (") && strings.HasSuffix(res, ")") && len(res) > len("not run ()"):
			if strings.TrimSpace(r["commit"]) != "" || strings.TrimSpace(r["artifacts"]) != "" {
				return fmt.Errorf("%s did not run yet claims commit %q and artifacts %q", id, r["commit"], r["artifacts"])
			}
		default:
			return fmt.Errorf("%s reads %q, neither passed nor \"not run (<reason>)\"", id, r["result"])
		}
	}
	return nil
}

func (s *s17aState) thenBlockersSection(heading string) error {
	open := false
	for _, id := range s17aStageIDs() {
		open = open || strings.HasPrefix(strings.ToLower(s17aEvidence.rows[id]["result"]), "not run")
	}
	if !open {
		return nil
	}
	body, ok := s17rSection(s17aEvidence.record, "## "+heading)
	if !ok || !strings.Contains(body, "What clears it") || strings.Count(body, "\n|") < 3 {
		return fmt.Errorf("the record has no %q section with a table naming what clears it", heading)
	}
	if !strings.Contains(body, "prompt count") || !strings.Contains(body, "M2-C-5") {
		return fmt.Errorf("the %q section does not say each row records the prompt count (M2-C-5)", heading)
	}
	return nil
}

func InitializeScenario_dh_s_17_acceptance_on_a_real_model_session_and_agent_loop_acceptance(ctx *godog.ScenarioContext) {
	s := &s17aState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = s17aState{}
		module, err := s17rModuleDir()
		if err != nil {
			return c, err
		}
		s.module, s.repo = module, filepath.Dir(filepath.Dir(module))
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		for _, h := range s.hosts {
			select {
			case <-h.done:
			default:
				h.kill()
			}
		}
		if s.route != nil {
			s.route.srv.Close()
		}
		return c, err
	})

	ctx.Step(`^dsh built from this checkout with the testhook tag$`, s.givenBuilt)
	ctx.Step(`^a scratch home whose route "([^"]*)" is served by an in-process model instance$`, s.givenRoute)
	ctx.Step(`^an sdk host on the scratch home with the file tool "([^"]*)" over a workspace holding (.+)$`, s.givenToolHost)
	ctx.Step(`^an sdk host on the scratch home with the long tool "([^"]*)"$`, s.givenSleepHost)
	ctx.Step(`^an sdk host on the scratch home with the stream capability$`, s.givenStreamHost)

	ctx.Step(`^a session runs three turns, the first reading "([^"]*)" with "([^"]*)"$`, s.whenThreeTurns)
	ctx.Step(`^the host is stopped and a fresh host reads the session back from the store$`, s.whenStoppedAndReadBack)
	ctx.Step(`^the history read back equals the history before the stop, event for event$`, s.thenHistoryEqual)
	ctx.Step(`^every request rebuilt from the persisted log has the digest the route received, one per settlement in log order$`, s.thenDigestsReceived)
	ctx.Step(`^rebuilding the requests twice gives the same bytes$`, s.thenRebuildTwice)
	ctx.Step(`^the persisted log runs seq 0 to n with no gap$`, s.thenNoGap)
	ctx.Step(`^the record of "([^"]*)" cites a prompt count of (\d+), equal to the user messages in its persisted logs?$`, s.thenPromptCount)

	ctx.Step(`^a session's turn calls "([^"]*)" and the tool/call is durable while the tool runs$`, s.whenDurableCall)
	ctx.Step(`^the host is killed without a graceful stop$`, s.whenKilled)
	ctx.Step(`^a new host on the same home resumes the session$`, s.whenResumed)
	ctx.Step(`^session/resume reports that it closed an interrupted turn$`, s.thenClosedInterrupted)
	ctx.Step(`^the history after the resume begins with the history before the kill, event for event$`, s.thenPrefix)
	ctx.Step(`^the resume appended exactly tool/result, step/end, turn/end and session/end-seed, the result outcome-unknown for the call and the turn interrupted$`, s.thenClosers)
	ctx.Step(`^a second resume on another new host appends no closer$`, s.thenSecondResumeNoCloser)
	ctx.Step(`^the next turn completes with an assistant/message$`, s.thenNextTurnCompletes)

	ctx.Step(`^one prompt that needs "([^"]*)" runs to idle$`, s.whenOnePrompt)
	ctx.Step(`^the log holds one turn/start and one turn/end completed$`, s.thenOneTurn)
	ctx.Step(`^the turn has at least (\d+) steps, each step/start closed by its step/end in order$`, s.thenBracketed)
	ctx.Step(`^every tool/call has its tool/result and every step settles exactly once$`, s.thenPairedSettled)

	ctx.Step(`^a prompt's answer sends its first chunk and the client cancels the turn keeping the inbox$`, s.whenCancelAfterFirstChunk)
	ctx.Step(`^"([^"]*)" is sent after the session is idle$`, s.whenRepeat)
	ctx.Step(`^the first turn ends aborted with cause user$`, s.thenAbortedUser)
	ctx.Step(`^its one settlement is an assistant/message holding the sent text and no tool call$`, s.thenKeptShown)
	ctx.Step(`^dsh cut off the first turn's response and no text sent after the cancel is in the log$`, s.thenNoLateChunk)
	ctx.Step(`^the request of the second turn rebuilt from the persisted log carries the text shown before the cancel$`, s.thenNextRequestCarriesShown)

	ctx.Step(`^run A stops the host gracefully while "([^"]*)" runs$`, s.whenRunAStops)
	ctx.Step(`^an sdk host on the scratch home with the real shell tool, whose command records its pid and sleeps 60 seconds$`, s.givenShellHost)
	ctx.Step(`^the shell command run A started no longer runs$`, s.thenChildGone)
	ctx.Step(`^before any resume the persisted log of run A holds the call's tool/result, step/end and turn/end aborted with cause disposed$`, s.thenRunAHonest)
	ctx.Step(`^the persisted log of run [AB] runs seq 0 to n with no gap$`, s.thenLogNoGap)
	ctx.Step(`^run B kills a fresh host on a fresh home while "([^"]*)" runs, and a new host resumes the session$`, s.whenRunBKills)
	ctx.Step(`^the resume of run B appends an outcome-unknown tool/result and the closers, and the turn is not continued$`, s.thenRunBResumed)

	ctx.Step(`^a prompt whose every step reads one of "([^"]*)", "([^"]*)" and "([^"]*)" runs to idle$`, s.whenBudgetLoop)
	ctx.Step(`^"([^"]*)" is sent and its turn reads "([^"]*)" before it answers$`, s.whenContinue)
	ctx.Step(`^the first turn ends budget with limit steps at the default step budget, after exactly that many steps$`, s.thenBudgetDefault)
	ctx.Step(`^every tool/call of either turn has its tool/result, each a read returning the file's content$`, s.thenBudgetPaired)
	ctx.Step(`^the second turn reads a file the first turn did not$`, s.thenProgress)

	ctx.Step(`^the committed test/harness/acceptance-index\.yaml$`, s.givenIndex)
	ctx.Step(`^"harness-gate require --stage DH-S-17" lists the stage's scenarios$`, s.whenRequire)
	ctx.Step(`^it lists (.+)$`, s.thenListed)
	ctx.Step(`^the gate's stageOrder of "([^"]*)" holds "([^"]*)" and "([^"]*)" in its Late phase, after the single-process scenarios$`, s.thenLate)
	ctx.Step(`^the harness-gate phase test "([^"]*)" passes$`, s.thenPhaseTest)

	ctx.Step(`^the committed record "([^"]*)" still reading "([^"]*)"$`, s.givenRecord)
	ctx.Step(`^the required rows of (.+) are read$`, s.whenRowsRead)
	ctx.Step(`^each row reads passed with a commit SHA and artifacts citing its prompt count, or reads "not run \(<reason>\)" with no commit and no artifacts$`, s.thenRowsPassedOrOwed)
	ctx.Step(`^while any of them is not run, the "([^"]*)" section names what clears each blocker and that each row records the prompt count \(M2-C-5\)$`, s.thenBlockersSection)
}

func TestE2E_dh_s_17_acceptance_on_a_real_model_session_and_agent_loop_acceptance(t *testing.T) {
	// Build here, on t's own goroutine: launcher.Build fails t directly, which
	// must not happen from a godog scenario subtest.
	s17aOuterT = t
	s17aDsh = launcher.Build(t, filepath.Join("..", "..", ".."), "./cmd/dsh", "testhook")
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_17_acceptance_on_a_real_model_session_and_agent_loop_acceptance,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_17_acceptance_on_a_real_model_session_and_agent_loop_acceptance.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
