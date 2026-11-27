//go:build e2e

package e2e

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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
)

// hslBin is dsh built once, with the testhook tag that adds the keyless fake
// route llm.testkit-route, by the suite's test function before godog starts.
var hslBin string

const (
	hslTracePrefix = "dsh: trace: components: "
	hslRouteName   = "llm.testkit-route"
	hslBound       = time.Minute
)

// hslModel serves the fake route's SSE dialect on loopback: every attempt
// answers text and finishes with stop, after release is closed.
type hslModel struct {
	srv      *httptest.Server
	text     string
	arrived  chan struct{}
	release  chan struct{}
	released sync.Once
}

func (m *hslModel) open() {
	m.released.Do(func() { close(m.release) })
}

func (m *hslModel) serve(w http.ResponseWriter, r *http.Request) {
	select {
	case m.arrived <- struct{}{}:
	default:
	}
	select {
	case <-m.release:
	case <-r.Context().Done():
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	data, _ := json.Marshal(map[string]string{"text": m.text})
	fmt.Fprintf(w, "event: text\ndata: %s\n\n", data)
	fmt.Fprint(w, "event: usage\ndata: {\"input\":3,\"output\":1}\n\n")
	fmt.Fprint(w, "event: finish\ndata: {\"reason\":\"stop\"}\n\n")
}

// hslProc is a started dsh process with its captured streams.
type hslProc struct {
	cmd    *exec.Cmd
	stdout bytes.Buffer
	stderr bytes.Buffer
	done   chan error
}

type hslResult struct {
	status         int
	stdout, stderr string
}

func (r hslResult) String() string {
	return fmt.Sprintf("status %d\nstdout:\n%s\nstderr:\n%s", r.status, r.stdout, r.stderr)
}

type hslState struct {
	tmp    string
	root   string
	model  *hslModel
	ctx    context.Context
	cancel context.CancelFunc

	headless *hslProc
	last     hslResult
	admin    hslResult

	sdk      *exec.Cmd
	sdkIn    io.WriteCloser
	sdkOut   io.ReadCloser
	sdkErr   bytes.Buffer
	sdkDone  chan struct{}
	sdkTook  time.Duration
	sdkLayer string
	ending   string
}

func (s *hslState) builtWithRoute(text string) error {
	if hslBin == "" {
		return errors.New("the dsh build produced no binary")
	}
	var err error
	if s.tmp, err = os.MkdirTemp("", "dshhsl-"); err != nil {
		return err
	}
	s.root = filepath.Join(s.tmp, "home")
	if err := home.Ensure(home.New(s.root)); err != nil {
		return err
	}
	s.model = &hslModel{text: text, arrived: make(chan struct{}, 16), release: make(chan struct{})}
	s.model.open()
	s.model.srv = httptest.NewServer(http.HandlerFunc(s.model.serve))
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 3*time.Minute)
	return s.writeLayer(profile.Headless, "")
}

// writeLayer writes the profile patch of name: the fake route item with
// routeExtra appended to its config, then the further top-level entries.
func (s *hslState) writeLayer(name, routeExtra string, more ...string) error {
	text := "- insert:\n    - id: route\n      name: " + hslRouteName + "\n      config:\n        id: fake\n        url: " +
		strconv.Quote(s.model.srv.URL) + "\n" + routeExtra + strings.Join(more, "")
	path := profile.LayerPath(s.root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o600)
}

func (s *hslState) holdReply() error {
	s.model.release = make(chan struct{})
	s.model.released = sync.Once{}
	return nil
}

func (s *hslState) releaseReply() error {
	s.model.open()
	return nil
}

// hslWords splits a command line into words, a double-quoted run being one word.
func hslWords(line string) ([]string, error) {
	var words []string
	var cur strings.Builder
	in, have := false, false
	for _, r := range line {
		switch {
		case r == '"':
			in, have = !in, true
		case r == ' ' && !in:
			if have {
				words = append(words, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if in {
		return nil, fmt.Errorf("unbalanced quote in %q", line)
	}
	if have {
		words = append(words, cur.String())
	}
	if len(words) == 0 || words[0] != "dsh" {
		return nil, fmt.Errorf("command line %q does not start with dsh", line)
	}
	return words[1:], nil
}

func (s *hslState) start(line string) (*hslProc, error) {
	args, err := hslWords(line)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(s.tmp, "cwd-")
	if err != nil {
		return nil, err
	}
	p := &hslProc{done: make(chan error, 1)}
	p.cmd = exec.CommandContext(s.ctx, hslBin, append([]string{"--home", s.root}, args...)...)
	p.cmd.Env = append(os.Environ(), cli.TraceEnv+"=1")
	p.cmd.Dir = dir
	p.cmd.Stdout, p.cmd.Stderr = &p.stdout, &p.stderr
	if err := p.cmd.Start(); err != nil {
		return nil, err
	}
	go func() { p.done <- p.cmd.Wait() }()
	return p, nil
}

func (p *hslProc) wait(within time.Duration) (hslResult, error) {
	select {
	case err := <-p.done:
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			return hslResult{}, fmt.Errorf("%v: %w", p.cmd.Args, err)
		}
		return hslResult{p.cmd.ProcessState.ExitCode(), p.stdout.String(), p.stderr.String()}, nil
	case <-time.After(within):
		return hslResult{}, fmt.Errorf("%v did not exit within %s", p.cmd.Args, within)
	}
}

func (s *hslState) runHeadless(line string) error {
	p, err := s.start(line)
	if err != nil {
		return err
	}
	s.last, err = p.wait(2 * time.Minute)
	return err
}

func (s *hslState) stdoutExactly(text string) error {
	if s.last.stdout != text+"\n" {
		return fmt.Errorf("stdout is %q, want %q:\n%s", s.last.stdout, text+"\n", s.last)
	}
	return nil
}

func (s *hslState) headlessStatus(want int) error {
	if s.last.status != want {
		return fmt.Errorf("dsh headless exited %d, want %d:\n%s", s.last.status, want, s.last)
	}
	return nil
}

func (s *hslState) lines() []string {
	return strings.Split(strings.TrimSuffix(s.last.stdout, "\n"), "\n")
}

func (s *hslState) exportRecords() error {
	lines := s.lines()
	if len(lines) < 2 {
		return fmt.Errorf("stdout has %d lines, want export records and a run result:\n%s", len(lines), s.last)
	}
	for i, line := range lines[:len(lines)-1] {
		var rec struct {
			Seq  *int64 `json:"seq"`
			Type string `json:"type"`
			Data string `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Seq == nil || rec.Type == "" || !json.Valid([]byte(rec.Data)) {
			return fmt.Errorf("stdout line %d is not an export record (%v): %s", i, err, line)
		}
	}
	return nil
}

func (s *hslState) exportTypes(a, b string) error {
	lines := s.lines()
	types := map[string]bool{}
	for _, line := range lines[:len(lines)-1] {
		var rec struct{ Type string }
		if json.Unmarshal([]byte(line), &rec) == nil {
			types[rec.Type] = true
		}
	}
	for _, want := range []string{a, b} {
		if !types[want] {
			return fmt.Errorf("no %s export record in:\n%s", want, s.last.stdout)
		}
	}
	return nil
}

func (s *hslState) runResult(text string) error {
	lines := s.lines()
	last := lines[len(lines)-1]
	var res struct{ Type, Session, Reason, Text string }
	if err := json.Unmarshal([]byte(last), &res); err != nil {
		return fmt.Errorf("the last stdout line does not parse (%v): %s", err, last)
	}
	if res.Type != "run/result" || res.Text != text || res.Reason != "completed" || res.Session == "" {
		return fmt.Errorf("the last stdout line is not the completed run result with text %q: %s", text, last)
	}
	return nil
}

func (s *hslState) headlessBlocked(line string) error {
	p, err := s.start(line)
	if err != nil {
		return err
	}
	s.headless = p
	select {
	case <-s.model.arrived:
		return nil
	case err := <-p.done:
		return fmt.Errorf("the headless run ended (%v) before the model replied:\n%s", err, p.stderr.String())
	case <-time.After(2 * time.Minute):
		return errors.New("the headless run never reached the fake model")
	}
}

func (s *hslState) runAdmin(line string) error {
	p, err := s.start(line)
	if err != nil {
		return err
	}
	s.admin, err = p.wait(2 * time.Minute)
	return err
}

func (s *hslState) adminOK() error {
	if s.admin.status != output.OK {
		return fmt.Errorf("the admin command exited %d, want 0:\n%s", s.admin.status, s.admin)
	}
	return nil
}

func (s *hslState) adminTraceInert() error {
	var found []string
	for _, line := range strings.Split(s.admin.stderr, "\n") {
		if list, ok := strings.CutPrefix(strings.TrimSuffix(line, "\r"), hslTracePrefix); ok {
			found = append(found, list)
		}
	}
	if len(found) != 1 {
		return fmt.Errorf("stderr holds %d component trace lines, want one:\n%s", len(found), s.admin)
	}
	for _, part := range strings.Split(found[0], ",") {
		part = strings.TrimSpace(part)
		if profile.Acting(part, part) || strings.HasPrefix(part, "agent") {
			return fmt.Errorf("the admin command constructed %q (trace %q)", part, found[0])
		}
	}
	return nil
}

func (s *hslState) headlessCompletes(want int, text string) error {
	if s.headless == nil {
		return errors.New("no headless run is live")
	}
	r, err := s.headless.wait(2 * time.Minute)
	if err != nil {
		return err
	}
	s.headless = nil
	if r.status != want || r.stdout != text+"\n" {
		return fmt.Errorf("the headless run beside the admin command: want status %d and %q:\n%s", want, text+"\n", r)
	}
	return nil
}

func (s *hslState) sdkSetup(setup string) error {
	switch setup {
	case "as it is":
		return s.writeLayer(profile.SDK, "")
	case "with a write stall of 1s":
		return s.writeLayer(profile.SDK, "", "- id: wire.stdio\n  name: wire.stdio\n  merge:\n    writeStall: 1s\n")
	case "with a release that never returns":
		return s.writeLayer(profile.SDK, "        holdStop: true\n")
	}
	return fmt.Errorf("unknown sdk setup %q", setup)
}

// hslPing is one request the server answers with an error; its long id makes
// each answer large enough to fill a pipe in a few hundred lines.
func hslPing(n int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":"%s-%d","method":"test/unknown"}`+"\n", strings.Repeat("x", 2048), n)
}

func (s *hslState) sdkStart(line, ending string) error {
	args, err := hslWords(line)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp(s.tmp, "cwd-")
	if err != nil {
		return err
	}
	s.sdk = exec.CommandContext(s.ctx, hslBin, append([]string{"--home", s.root}, args...)...)
	s.sdk.Env = os.Environ()
	s.sdk.Dir = dir
	s.sdk.Stderr = &s.sdkErr
	if s.sdkIn, err = s.sdk.StdinPipe(); err != nil {
		return err
	}
	if s.sdkOut, err = s.sdk.StdoutPipe(); err != nil {
		return err
	}
	if err := s.sdk.Start(); err != nil {
		return err
	}
	s.sdkDone = make(chan struct{})
	go func() {
		defer close(s.sdkDone)
		_ = s.sdk.Wait()
	}()
	s.ending = ending
	start := time.Now()
	switch ending {
	case "the client closes stdin", "the forced deadline elapses":
		// One answered request proves the server is up before the client ends.
		go func() { _, _ = io.Copy(io.Discard, s.sdkOut) }()
		if _, err := io.WriteString(s.sdkIn, hslPing(0)); err != nil {
			return err
		}
		_ = s.sdkIn.Close()
	case "the client stops reading":
		go func() {
			for i := 0; ; i++ {
				if _, err := io.WriteString(s.sdkIn, hslPing(i)); err != nil {
					return
				}
			}
		}()
	default:
		return fmt.Errorf("unknown sdk ending %q", ending)
	}
	select {
	case <-s.sdkDone:
	case <-time.After(hslBound):
		return fmt.Errorf("dsh --profile sdk did not exit within %s:\n%s", hslBound, s.sdkErr.String())
	}
	s.sdkTook = time.Since(start)
	return nil
}

func (s *hslState) sdkStatus(want int) error {
	got := s.sdk.ProcessState.ExitCode()
	if got != want {
		return fmt.Errorf("dsh --profile sdk (%s) exited %d, want %d:\n%s", s.ending, got, want, s.sdkErr.String())
	}
	if want == output.Forced {
		if bound := profile.DefaultLimits().ShutdownTimeout; s.sdkTook < bound/2 {
			return fmt.Errorf("exited 124 after %s, before the %s shutdown bound", s.sdkTook, bound)
		}
	}
	return nil
}

func (s *hslState) cleanup() error {
	if s.model != nil {
		s.model.open()
	}
	if s.headless != nil && s.headless.cmd.Process != nil {
		_ = s.headless.cmd.Process.Kill()
		<-s.headless.done
	}
	if s.sdk != nil && s.sdkDone != nil {
		if s.sdk.Process != nil {
			_ = s.sdk.Process.Kill()
		}
		<-s.sdkDone
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.model != nil && s.model.srv != nil {
		s.model.srv.Close()
	}
	if s.tmp == "" {
		return nil
	}
	// Windows releases a stopped process's handles on the home shortly after
	// the process is reported gone.
	var err error
	for range 40 {
		if err = os.RemoveAll(s.tmp); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return err
}

func InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_headless_and_sdk_launchers(ctx *godog.ScenarioContext) {
	s := &hslState{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if cerr := s.cleanup(); cerr != nil && err == nil {
			return c, cerr
		}
		return c, err
	})
	ctx.Step(`^the built dsh with the keyless fake route answering "([^"]*)" on a temp home$`, s.builtWithRoute)
	ctx.Step(`^the fake route holds its reply$`, s.holdReply)
	ctx.Step(`^the fake route releases its reply$`, s.releaseReply)
	ctx.Step(`^"(.*)" runs on the temp home$`, s.runHeadless)
	ctx.Step(`^stdout is exactly "([^"]*)" and a newline$`, s.stdoutExactly)
	ctx.Step(`^the headless exit status is (\d+)$`, s.headlessStatus)
	ctx.Step(`^every stdout line but the last parses as an export record$`, s.exportRecords)
	ctx.Step(`^the export records include "([^"]*)" and "([^"]*)"$`, s.exportTypes)
	ctx.Step(`^the last stdout line is the run result with text "([^"]*)"$`, s.runResult)
	ctx.Step(`^"(.*)" is running on the temp home, blocked on the fake-model reply$`, s.headlessBlocked)
	ctx.Step(`^"(.*)" runs on the same home with the component trace on$`, s.runAdmin)
	ctx.Step(`^the admin command exits 0$`, s.adminOK)
	ctx.Step(`^its component trace lists no agent, model client or carrier$`, s.adminTraceInert)
	ctx.Step(`^the headless run completes with status (\d+) and prints "([^"]*)"$`, s.headlessCompletes)
	ctx.Step(`^the sdk profile layer of the temp home inserts the fake route (.+)$`, s.sdkSetup)
	ctx.Step(`^"(.*)" starts on the temp home and (.+)$`, s.sdkStart)
	ctx.Step(`^the sdk launcher exits with status (\d+)$`, s.sdkStatus)
}

func TestE2E_dh_s_16_profiles_launchers_and_evidence_runners_headless_and_sdk_launchers(t *testing.T) {
	// Build here, on t's own goroutine: launcher.Build fails t directly, which
	// must not happen from a godog scenario subtest.
	hslBin = launcher.Build(t, filepath.Join("..", "..", ".."), "./cmd/dsh", "testhook")
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_headless_and_sdk_launchers,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_16_profiles_launchers_and_evidence_runners_headless_and_sdk_launchers.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
