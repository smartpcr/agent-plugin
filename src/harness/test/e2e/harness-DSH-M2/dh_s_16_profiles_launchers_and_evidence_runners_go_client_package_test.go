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
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
	"github.com/smartpcr/agent-plugin/src/sdk/sdk"
)

// gcpBin is dsh built once, with the testhook tag that adds the keyless fake
// route llm.testkit-route, by the suite's test function before godog starts.
var gcpBin string

const (
	// gcpFakeHostEnv makes this test binary a fake host: "ignore" answers
	// initialize and shutdown but never exits on its own; "ignore-term" also
	// ignores SIGTERM. Any mode first writes gcpMarkEnv's file, when set.
	gcpFakeHostEnv = "DSH_E2E_GCP_FAKE_HOST"
	gcpMarkEnv     = "DSH_E2E_GCP_MARK"
	gcpBound       = 3 * time.Minute
)

func init() {
	if mode, ok := os.LookupEnv(gcpFakeHostEnv); ok {
		gcpFakeHost(mode)
	}
}

func gcpFakeHost(mode string) {
	if mark := os.Getenv(gcpMarkEnv); mark != "" {
		_ = os.WriteFile(mark, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}
	if mode == "ignore-term" {
		signal.Ignore(syscall.SIGTERM)
	}
	conn := jsonrpc.Serve(context.Background(), os.Stdin, os.Stdout, jsonrpc.Options{Methods: map[string]jsonrpc.Handler{
		protocol.InitializeMethod: func(context.Context, json.RawMessage) (any, error) {
			return protocol.InitializeResult{ProtocolVersion: protocol.Version, ServerInfo: protocol.Implementation{Name: "gcp-fake"}}, nil
		},
		"shutdown": func(context.Context, json.RawMessage) (any, error) { return protocol.Empty{}, nil },
	}})
	<-conn.Done()
	// The end of the input is ignored; a pending timer keeps the runtime from
	// reporting a deadlock while the ladder escalates.
	<-time.After(time.Hour)
	os.Exit(0)
}

type gcpWorld struct {
	tmp    string
	ctx    context.Context
	cancel context.CancelFunc
	model  *httptest.Server
	stderr strings.Builder

	opts    sdk.Options
	h       *sdk.Harness
	pid     int
	result  sdk.RunResult
	closeAt time.Duration
	closeE  error
	startE  error
	mark    string
}

func (w *gcpWorld) begin() error {
	var err error
	if w.tmp, err = os.MkdirTemp("", "dshgcp-"); err != nil {
		return err
	}
	w.ctx, w.cancel = context.WithTimeout(context.Background(), gcpBound)
	return nil
}

func (w *gcpWorld) cleanup() {
	if w.h != nil {
		_ = w.h.Close(context.Background())
	}
	if w.model != nil {
		w.model.Close()
	}
	if w.cancel != nil {
		w.cancel()
	}
	if w.tmp != "" {
		_ = os.RemoveAll(w.tmp)
	}
}

func (w *gcpWorld) diag() string { return "\nchild stderr:\n" + w.stderr.String() }

// --- Scenario: Client drives the built binary ---

func (w *gcpWorld) givenDSHBin() error {
	if gcpBin == "" {
		return errors.New("the dsh build produced no binary")
	}
	if err := w.begin(); err != nil {
		return err
	}
	// Options.Bin stays empty, so Start must take $DSH_BIN.
	return os.Setenv(sdk.BinEnv, gcpBin)
}

func (w *gcpWorld) givenFakeRoute(text string) error {
	w.model = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/event-stream")
		data, _ := json.Marshal(map[string]string{"text": text})
		fmt.Fprintf(rw, "event: text\ndata: %s\n\n", data)
		fmt.Fprint(rw, "event: usage\ndata: {\"input\":3,\"output\":1}\n\n")
		fmt.Fprint(rw, "event: finish\ndata: {\"reason\":\"stop\"}\n\n")
	}))
	dir := filepath.Join(w.tmp, "work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	w.opts = sdk.Options{Home: filepath.Join(w.tmp, "home"), Dir: dir, Stderr: &w.stderr}
	// dsh makes the home private on its first start; the layer written into it
	// afterwards inherits that.
	first, err := sdk.Start(w.ctx, w.opts)
	if err != nil {
		return fmt.Errorf("first start: %w%s", err, w.diag())
	}
	if err := first.Close(w.ctx); err != nil {
		return fmt.Errorf("first close: %w%s", err, w.diag())
	}
	layer := profile.LayerPath(w.opts.Home, profile.SDK)
	if err := os.MkdirAll(filepath.Dir(layer), 0o700); err != nil {
		return err
	}
	route := "- insert:\n    - id: route\n      name: llm.testkit-route\n      config:\n        id: fake\n        url: " + strconv.Quote(w.model.URL) + "\n"
	return os.WriteFile(layer, []byte(route), 0o600)
}

func (w *gcpWorld) whenClientRuns(prompt string) error {
	h, err := sdk.Start(w.ctx, w.opts)
	if err != nil {
		return fmt.Errorf("start: %w%s", err, w.diag())
	}
	w.h, w.pid = h, h.Pid()
	if w.pid <= 0 {
		return fmt.Errorf("Start made no child: pid %d", w.pid)
	}
	s, err := h.Create(w.ctx, sdk.CreateOptions{Model: &protocol.ModelRoute{Provider: "fake", Model: "scripted"}})
	if err != nil {
		return fmt.Errorf("create: %w%s", err, w.diag())
	}
	if w.result, err = s.Run(w.ctx, sdk.Input{Text: prompt}); err != nil {
		return fmt.Errorf("run: %w%s", err, w.diag())
	}
	return nil
}

func (w *gcpWorld) thenFinalResponse(want string) error {
	if w.result.FinalResponse != want {
		return fmt.Errorf("final response %q, want %q", w.result.FinalResponse, want)
	}
	return nil
}

func (w *gcpWorld) thenClosedAtShutdown() error {
	if err := w.h.Close(w.ctx); err != nil {
		return fmt.Errorf("close: %w%s", err, w.diag())
	}
	if e := w.h.Ending(); e != (sdk.Ending{Rung: sdk.RungShutdown, ExitCode: 0}) {
		return fmt.Errorf("ending %+v, want the shutdown rung with status 0%s", e, w.diag())
	}
	return nil
}

func (w *gcpWorld) thenPidGone() error {
	if w.pid <= 0 {
		return errors.New("no child pid was recorded")
	}
	ctx, cancel := context.WithTimeout(w.ctx, 10*time.Second)
	defer cancel()
	if err := procwait.Poll(ctx, func() bool { return !subprocesstest.Alive(w.pid) }); err != nil {
		return fmt.Errorf("pid %d still alive: %w", w.pid, err)
	}
	return nil
}

// --- Scenario Outline: Shutdown ladder escalates ---

func (w *gcpWorld) givenIgnoringChild(ignores string) error {
	if err := w.begin(); err != nil {
		return err
	}
	mode := map[string]string{
		"ignores the end of its input":             "ignore",
		"ignores the end of its input and SIGTERM": "ignore-term",
	}[ignores]
	if mode == "" {
		return fmt.Errorf("unknown child behaviour %q", ignores)
	}
	w.opts = sdk.Options{Bin: os.Args[0], Home: filepath.Join(w.tmp, "home"), Env: []string{gcpFakeHostEnv + "=" + mode}, Stderr: &w.stderr}
	return nil
}

func (w *gcpWorld) whenClosesWithGrace(ms int) error {
	w.opts.ShutdownGrace = time.Duration(ms) * time.Millisecond
	h, err := sdk.Start(w.ctx, w.opts)
	if err != nil {
		return fmt.Errorf("start: %w%s", err, w.diag())
	}
	w.h, w.pid = h, h.Pid()
	start := time.Now()
	w.closeE = h.Close(w.ctx)
	w.closeAt = time.Since(start)
	return nil
}

func (w *gcpWorld) thenClosedWithin(sec int) error {
	if w.closeE != nil {
		return fmt.Errorf("close: %w%s", w.closeE, w.diag())
	}
	if limit := time.Duration(sec) * time.Second; w.closeAt > limit {
		return fmt.Errorf("Close took %v, want at most %v", w.closeAt, limit)
	}
	return nil
}

func (w *gcpWorld) thenEndedAtRung(unix, windows string) error {
	want := sdk.Rung(unix)
	if runtime.GOOS == "windows" {
		// Windows has no terminate signal: the ladder goes from shutdown to kill.
		want = sdk.Rung(windows)
	}
	if got := w.h.Ending().Rung; got != want {
		return fmt.Errorf("the child ended at rung %q, want %q on %s", got, want, runtime.GOOS)
	}
	return nil
}

// --- Scenario: Missing home refused ---

func (w *gcpWorld) givenNoHome() error {
	if err := w.begin(); err != nil {
		return err
	}
	w.mark = filepath.Join(w.tmp, "started")
	w.opts = sdk.Options{Bin: os.Args[0], Env: []string{gcpFakeHostEnv + "=ignore", gcpMarkEnv + "=" + w.mark}, ShutdownGrace: 500 * time.Millisecond}
	return nil
}

func (w *gcpWorld) whenStart() error {
	w.h, w.startE = sdk.Start(w.ctx, w.opts)
	return nil
}

func (w *gcpWorld) thenStartRefused(want string) error {
	if w.h != nil {
		return fmt.Errorf("Start returned a client (pid %d), want none", w.h.Pid())
	}
	if w.startE == nil || !strings.Contains(w.startE.Error(), want) {
		return fmt.Errorf("Start error %v, want one containing %q", w.startE, want)
	}
	return nil
}

func (w *gcpWorld) thenNoProcess() error {
	// The same options with a home do start the marking child, so an absent
	// mark is the refusal and not a child that cannot record its start.
	probe := w.opts
	probe.Home = filepath.Join(w.tmp, "home")
	probeMark := filepath.Join(w.tmp, "probe-started")
	probe.Env = []string{gcpFakeHostEnv + "=ignore", gcpMarkEnv + "=" + probeMark}
	h, err := sdk.Start(w.ctx, probe)
	if err != nil {
		return fmt.Errorf("control start with a home: %w", err)
	}
	defer h.Close(context.Background())
	if _, err := os.Stat(probeMark); err != nil {
		return fmt.Errorf("the control child left no mark: %w", err)
	}
	if _, err := os.Stat(w.mark); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("a child started without a home: stat %s = %v", w.mark, err)
	}
	return nil
}

func InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_go_client_package(ctx *godog.ScenarioContext) {
	w := &gcpWorld{}
	prevBin, hadBin := os.LookupEnv(sdk.BinEnv)
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = gcpWorld{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.cleanup()
		if hadBin {
			_ = os.Setenv(sdk.BinEnv, prevBin)
		} else {
			_ = os.Unsetenv(sdk.BinEnv)
		}
		return c, err
	})

	ctx.Step(`^DSH_BIN set to the dsh binary built once by the test$`, w.givenDSHBin)
	ctx.Step(`^the keyless fake route answering "([^"]*)" in the sdk profile of a fresh home$`, w.givenFakeRoute)
	ctx.Step(`^the client starts, creates a session and runs the prompt "([^"]*)"$`, w.whenClientRuns)
	ctx.Step(`^the run returns the fake's text "([^"]*)"$`, w.thenFinalResponse)
	ctx.Step(`^Close exits the child at the shutdown rung with status 0$`, w.thenClosedAtShutdown)
	ctx.Step(`^testkit/procwait sees the child's pid gone$`, w.thenPidGone)

	ctx.Step(`^a child that acknowledges shutdown but (.+)$`, w.givenIgnoringChild)
	ctx.Step(`^the client closes it with a grace of (\d+) milliseconds$`, w.whenClosesWithGrace)
	ctx.Step(`^Close returns without error within (\d+) seconds$`, w.thenClosedWithin)
	ctx.Step(`^the child ended at the "([^"]*)" rung on Unix or the "([^"]*)" rung on Windows$`, w.thenEndedAtRung)

	ctx.Step(`^Options without a home naming a child that records its own start$`, w.givenNoHome)
	ctx.Step(`^Start is called$`, w.whenStart)
	ctx.Step(`^Start returns the error "([^"]*)" and no client$`, w.thenStartRefused)
	ctx.Step(`^no process was started$`, w.thenNoProcess)
}

func TestE2E_dh_s_16_profiles_launchers_and_evidence_runners_go_client_package(t *testing.T) {
	// Build here, on t's own goroutine: launcher.Build fails t directly, which
	// must not happen from a godog scenario subtest.
	gcpBin = launcher.Build(t, filepath.Join("..", "..", ".."), "./cmd/dsh", "testhook")
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_go_client_package,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_16_profiles_launchers_and_evidence_runners_go_client_package.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
