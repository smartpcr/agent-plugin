//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/lifecycle"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
)

// obsBin is dsh built once with the testhook tag (fake route llm.testkit-route
// and the long fake tool testkit_sleep), by the suite's test function.
var obsBin string

const (
	obsRouteName   = "llm.testkit-route"
	obsSleepItem   = "tools.testkit-sleep"
	obsSleepLine   = "testkit_sleep: running"
	obsStepTimeout = 10 * time.Second
)

var obsRunLine = regexp.MustCompile(`dsh headless: run (ses_[0-9a-f]+) on route`)

// obsStop is one recorded Stop call.
type obsStop struct {
	reason launch.Reason
	status int
}

// obsApp is launch.Control with every Stop call counted.
type obsApp struct {
	*launch.Control
	mu    sync.Mutex
	calls []obsStop
}

func (a *obsApp) Stop(r launch.Reason, status int) {
	a.mu.Lock()
	a.calls = append(a.calls, obsStop{r, status})
	a.mu.Unlock()
	a.Control.Stop(r, status)
}

func (a *obsApp) stops() []obsStop {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]obsStop(nil), a.calls...)
}

func (a *obsApp) urgent() bool {
	select {
	case <-a.Urgent():
		return true
	default:
		return false
	}
}

type obsState struct {
	// the in-process state machine
	app      *obsApp
	signals  chan os.Signal
	triggers chan lifecycle.Trigger
	cancel   context.CancelFunc
	finished chan struct{}
	last     lifecycle.State
	mu       sync.Mutex
	state    lifecycle.State

	// the built dsh
	tmp      string
	root     string
	model    *httptest.Server
	ctx      context.Context
	stopCtx  context.CancelFunc
	cmd      *exec.Cmd
	stderr   *testkit.Buffer
	stdout   *testkit.Buffer
	done     chan struct{}
	sent     time.Time
	took     time.Duration
	exitCode int
}

// ---- in-process: the state machine ----

func (s *obsState) machine() error {
	s.app = &obsApp{Control: launch.NewApp()}
	s.signals = make(chan os.Signal)
	s.triggers = make(chan lifecycle.Trigger)
	s.finished = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	in := lifecycle.Inputs{Signals: s.signals, Triggers: s.triggers, Observe: func(st lifecycle.State) {
		s.mu.Lock()
		s.state = st
		s.mu.Unlock()
	}}
	go func() {
		defer close(s.finished)
		s.last = lifecycle.Watch(ctx, s.app, in)
	}()
	return nil
}

func (s *obsState) current() lifecycle.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *obsState) ended() bool {
	select {
	case <-s.finished:
		return true
	default:
		return false
	}
}

// fence returns once Watch handled every input sent before it: the channels
// are unbuffered and Watch handles one input per loop, so its receiving the
// no-op trigger 0 means the earlier inputs are done. A Watch that ended (a
// forced stop) handles nothing more.
func (s *obsState) fence() error {
	select {
	case s.triggers <- 0:
		return nil
	case <-s.finished:
		return nil
	case <-time.After(obsStepTimeout):
		return errors.New("the state machine did not take the fence input")
	}
}

func (s *obsState) fire(name string) error {
	var sig os.Signal
	var t lifecycle.Trigger
	switch name {
	case "ctrl-c":
		sig = os.Interrupt
	case "sigterm":
		sig = syscall.SIGTERM
	case "sighup":
		sig = syscall.SIGHUP
	case "stdin end":
		t = lifecycle.InputEnd
	case "dsh stop":
		t = lifecycle.Request
	case "stdio end":
		t = lifecycle.StdioEnd
	case "stalled client":
		t = lifecycle.Stalled
	default:
		return fmt.Errorf("unknown trigger %q", name)
	}
	var err error
	if sig != nil {
		select {
		case s.signals <- sig:
		case <-s.finished:
			err = fmt.Errorf("the state machine ended before %s", name)
		case <-time.After(obsStepTimeout):
			err = fmt.Errorf("the state machine did not take %s", name)
		}
	} else {
		select {
		case s.triggers <- t:
		case <-s.finished:
			err = fmt.Errorf("the state machine ended before %s", name)
		case <-time.After(obsStepTimeout):
			err = fmt.Errorf("the state machine did not take %s", name)
		}
	}
	if err != nil {
		return err
	}
	return s.fence()
}

func (s *obsState) followers() error {
	if err := s.fire("dsh stop"); err != nil {
		return err
	}
	return s.fire("stdin end")
}

func (s *obsState) stoppedOnce(reason string, status int) error {
	got := s.app.stops()
	if len(got) != 1 || got[0] != (obsStop{launch.Reason(reason), status}) {
		return fmt.Errorf("Stop calls %v, want exactly one (%s, %d)", got, reason, status)
	}
	return nil
}

func (s *obsState) stateIs(want string) error {
	if got := s.current().String(); got != want {
		return fmt.Errorf("state %s, want %s", got, want)
	}
	if s.ended() {
		return fmt.Errorf("the state machine ended in %s", s.last)
	}
	return nil
}

func (s *obsState) notForced() error {
	if s.app.urgent() {
		return errors.New("the shutdown was forced")
	}
	return nil
}

func (s *obsState) forced() error {
	if !s.app.urgent() {
		return errors.New("the shutdown wait was not ended: launch.Control is not urgent")
	}
	return nil
}

func (s *obsState) endedIn(want string) error {
	select {
	case <-s.finished:
	case <-time.After(obsStepTimeout):
		return fmt.Errorf("Watch is still running in %s, want it ended %s", s.current(), want)
	}
	if got := s.last.String(); got != want {
		return fmt.Errorf("Watch ended %s, want %s", got, want)
	}
	return nil
}

func (s *obsState) stoppedTwice(first string, status int, second string) error {
	got := s.app.stops()
	if len(got) != 2 || got[0] != (obsStop{launch.Reason(first), status}) || got[1].reason != launch.Reason(second) {
		return fmt.Errorf("Stop calls %v, want (%s, %d) then (%s, _)", got, first, status, second)
	}
	return nil
}

func (s *obsState) records(reason string, status int) error {
	r, st, ok := s.app.Result()
	if !ok || r != launch.Reason(reason) || st != status {
		return fmt.Errorf("the launch records (%s, %d, %v), want (%s, %d)", r, st, ok, reason, status)
	}
	return nil
}

// ---- service:os-process: the built dsh ----

// serveModel is the fake route's SSE dialect: the first reply calls
// testkit_sleep, every later one answers text.
func (s *obsState) serveModel() http.HandlerFunc {
	var mu sync.Mutex
	n := 0
	return func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if first {
			fmt.Fprint(w, "event: tool\ndata: {\"id\":\"c1\",\"name\":\"testkit_sleep\",\"args\":{}}\n\n")
			fmt.Fprint(w, "event: finish\ndata: {\"reason\":\"tool_calls\"}\n\n")
			return
		}
		data, _ := json.Marshal(map[string]string{"text": "slept"})
		fmt.Fprintf(w, "event: text\ndata: %s\n\nevent: finish\ndata: {\"reason\":\"stop\"}\n\n", data)
	}
}

func (s *obsState) builtWithSleep() error {
	if obsBin == "" {
		return errors.New("the dsh build produced no binary")
	}
	var err error
	if s.tmp, err = os.MkdirTemp("", "dshobs-"); err != nil {
		return err
	}
	s.root = filepath.Join(s.tmp, "home")
	if err := home.Ensure(home.New(s.root)); err != nil {
		return err
	}
	s.model = httptest.NewServer(s.serveModel())
	s.ctx, s.stopCtx = context.WithTimeout(context.Background(), 3*time.Minute)
	text := "- insert:\n    - id: route\n      name: " + obsRouteName + "\n      config:\n        id: fake\n        url: " +
		strconv.Quote(s.model.URL) + "\n    - id: sleep\n      name: " + obsSleepItem + "\n"
	path := profile.LayerPath(s.root, profile.Headless)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o600)
}

func (s *obsState) startHeadless(line string) error {
	line = strings.ReplaceAll(line, `\"`, `"`)
	const pre = `dsh headless "`
	if !strings.HasPrefix(line, pre) || !strings.HasSuffix(line, `"`) {
		return fmt.Errorf("unexpected command line %q", line)
	}
	prompt := strings.TrimSuffix(strings.TrimPrefix(line, pre), `"`)
	dir, err := os.MkdirTemp(s.tmp, "cwd-")
	if err != nil {
		return err
	}
	s.cmd = exec.CommandContext(s.ctx, obsBin, "--home", s.root, "headless", prompt)
	s.cmd.Env, s.cmd.Dir = os.Environ(), dir
	s.stdout, s.stderr = &testkit.Buffer{}, &testkit.Buffer{}
	s.cmd.Stdout, s.cmd.Stderr = s.stdout, s.stderr
	obsStoppable(s.cmd)
	if err := s.cmd.Start(); err != nil {
		return err
	}
	s.done = make(chan struct{})
	go func() { defer close(s.done); _ = s.cmd.Wait() }()
	return nil
}

func (s *obsState) sleepRunning() error {
	deadline := time.After(time.Minute)
	for !strings.Contains(s.stderr.String(), obsSleepLine) {
		select {
		case <-s.done:
			return fmt.Errorf("dsh headless exited %d before the tool ran:\n%s", s.cmd.ProcessState.ExitCode(), s.stderr)
		case <-deadline:
			return fmt.Errorf("testkit_sleep never ran:\n%s", s.stderr)
		case <-time.After(20 * time.Millisecond):
		}
	}
	return nil
}

func (s *obsState) terminate() error {
	// Stamp before signalling: on Windows obsTerminate waits for the WM_CLOSE
	// closer, so dsh may already be stopping when it returns. Over-counting the
	// closer's startup keeps the ShutdownTimeout bound conservative.
	s.sent = time.Now()
	return obsTerminate(s.cmd)
}

func (s *obsState) exitsWithinBound() error {
	bound := profile.DefaultLimits().ShutdownTimeout
	select {
	case <-s.done:
	case <-time.After(bound + 5*time.Second):
		return fmt.Errorf("dsh headless did not exit within %s of the signal:\n%s", bound, s.stderr)
	}
	s.took = time.Since(s.sent)
	s.exitCode = s.cmd.ProcessState.ExitCode()
	if s.took > bound {
		return fmt.Errorf("exited %s after the signal, past the %s ShutdownTimeout", s.took, bound)
	}
	return nil
}

func (s *obsState) exitStatus(want int) error {
	if s.exitCode != want {
		return fmt.Errorf("status %d, want %d:\n%s", s.exitCode, want, s.stderr)
	}
	return nil
}

func (s *obsState) turnClosed() error {
	m := obsRunLine.FindStringSubmatch(s.stderr.String())
	if m == nil {
		return fmt.Errorf("no run line on stderr:\n%s", s.stderr)
	}
	ctx := context.Background()
	st, err := sqlite.Open(ctx, home.New(s.root).DB())
	if err != nil {
		return err
	}
	defer func() { _ = st.Close(ctx) }()
	rd, err := st.OpenRead(ctx, m[1])
	if err != nil {
		return err
	}
	defer func() { _ = rd.Close(ctx) }()
	recs, err := rd.Read(ctx, 0, 0, store.ReadOptions{})
	if err != nil {
		return err
	}
	called, closed := false, false
	var types []string
	for _, rec := range recs {
		types = append(types, rec.Type)
		called = called || rec.Type == event.ToolCall.Name()
		closed = called && (closed || rec.Type == event.TurnEndType.Name())
	}
	if !called || !closed {
		return fmt.Errorf("tool call logged %v, turn closed after it %v; the log of %s holds %v", called, closed, m[1], types)
	}
	return nil
}

func (s *obsState) cleanup() error {
	if s.cancel != nil {
		s.cancel()
		<-s.finished
	}
	if s.cmd != nil && s.cmd.Process != nil && s.done != nil {
		select {
		case <-s.done:
		default:
			_ = s.cmd.Process.Kill()
			<-s.done
		}
	}
	if s.stopCtx != nil {
		s.stopCtx()
	}
	if s.model != nil {
		s.model.Close()
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

func InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_one_bounded_stop_path(ctx *godog.ScenarioContext) {
	s := &obsState{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if cerr := s.cleanup(); cerr != nil && err == nil {
			return c, cerr
		}
		return c, err
	})
	ctx.Step(`^the stop state machine watching a counting fake Stop with no busy work$`, s.machine)
	ctx.Step(`^the trigger "([^"]*)" fires$`, s.fire)
	ctx.Step(`^a "dsh stop" request and the end of standard input follow it$`, s.followers)
	ctx.Step(`^Stop was called exactly once with reason "([^"]*)" and status (\d+)$`, s.stoppedOnce)
	ctx.Step(`^the state machine is "([^"]*)"$`, s.stateIs)
	ctx.Step(`^the shutdown was not forced$`, s.notForced)
	ctx.Step(`^the shutdown was forced$`, s.forced)
	ctx.Step(`^the state machine ends "([^"]*)"$`, s.endedIn)
	ctx.Step(`^Stop was called twice, the first with reason "([^"]*)" and status (\d+) and the second with reason "([^"]*)"$`, s.stoppedTwice)
	ctx.Step(`^the launch records reason "([^"]*)" and status (\d+)$`, s.records)
	ctx.Step(`^the built dsh with the fake route calling testkit_sleep on a temp home$`, s.builtWithSleep)
	ctx.Step(`^"(.*)" is running on the temp home$`, s.startHeadless)
	ctx.Step(`^testkit_sleep is running$`, s.sleepRunning)
	ctx.Step(`^the lane's termination signal is sent to dsh headless$`, s.terminate)
	ctx.Step(`^dsh headless exits within ShutdownTimeout$`, s.exitsWithinBound)
	ctx.Step(`^the headless exit status is (\d+)$`, s.exitStatus)
	ctx.Step(`^the session log holds the tool call followed by a closing turn end$`, s.turnClosed)
}

func TestE2E_dh_s_16_profiles_launchers_and_evidence_runners_one_bounded_stop_path(t *testing.T) {
	// Build here, on t's own goroutine: launcher.Build fails t directly, which
	// must not happen from a godog scenario subtest.
	obsBin = launcher.Build(t, filepath.Join("..", "..", ".."), "./cmd/dsh", "testhook")
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_one_bounded_stop_path,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_16_profiles_launchers_and_evidence_runners_one_bounded_stop_path.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
