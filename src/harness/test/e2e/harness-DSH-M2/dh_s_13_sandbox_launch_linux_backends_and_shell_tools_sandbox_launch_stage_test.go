//go:build e2e

package e2e

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// slsReexecEnv marks this e2e test binary re-executed as the sandbox-launch stage
// or as the marker command the stage becomes. It has no DSH_ prefix because the
// subprocess environment builder strips DSH_* names.
const slsReexecEnv = "SANDBOXLAUNCHE2E_REEXEC"

// slsMarkerRole is the command: it creates the file named by its argument, so the
// marker exists exactly when a command ran.
const slsMarkerRole = "sls-marker"

// init dispatches a re-executed binary before the test framework starts, so this
// stage needs no package-wide TestMain that sibling stages could collide with.
func init() {
	if os.Getenv(slsReexecEnv) != "1" {
		return
	}
	args := os.Args[1:]
	if code, ok := launch.ServeIfAsked(args, os.Stderr); ok {
		os.Exit(code)
	}
	if len(args) == 2 && args[0] == slsMarkerRole {
		if err := os.WriteFile(args[1], []byte("ran"), 0o644); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(3)
}

type slsBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *slsBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *slsBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

type slsDecodeCase struct {
	name  string
	frame []byte
	valid bool
	want  launch.Ready
	got   launch.Ready
	err   error
}

type slsWorld struct {
	dir    string
	plan   launch.Plan
	ch     *launch.Channel
	p      *subprocess.Process
	marker string
	stderr *slsBuffer
	ready  launch.Ready
	exited bool
	code   int

	cases []*slsDecodeCase
}

func (w *slsWorld) timeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// start re-executes the test binary as `sandbox-launch` with w.plan and the marker
// helper as its command.
func (w *slsWorld) start() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	w.dir, err = os.MkdirTemp("", "sls-e2e-")
	if err != nil {
		return err
	}
	w.marker = filepath.Join(w.dir, "marker")
	w.plan.Argv = []string{exe, slsMarkerRole, w.marker}
	w.ch, err = launch.NewChannel()
	if err != nil {
		return fmt.Errorf("NewChannel: %w", err)
	}
	w.plan = w.ch.Bind(w.plan)
	if err := w.plan.Validate(); err != nil {
		return fmt.Errorf("the bound plan is invalid: %w", err)
	}
	env := subprocess.Env{slsReexecEnv + "=1"}
	if dir := os.Getenv(subprocesstest.CoverDirEnv); dir != "" {
		env = append(env, subprocesstest.CoverDirEnv+"="+dir)
	}
	w.stderr = &slsBuffer{}
	w.p, err = subprocess.Start(context.Background(), subprocess.Spec{
		Argv:   append([]string{exe, launch.Mode}, w.plan.Args()...),
		Env:    env,
		Stderr: w.stderr,
		Launch: subprocesstest.Launch,
		Extra:  w.ch.Extra(),
	})
	w.ch.CloseChildEnds()
	if err != nil {
		return fmt.Errorf("start the stage: %w", err)
	}
	return nil
}

func (w *slsWorld) wait() int {
	if !w.exited {
		_ = w.p.Wait()
		w.code = w.p.ExitCode()
		w.exited = true
	}
	return w.code
}

func (w *slsWorld) markerExists() (bool, error) {
	_, err := os.Stat(w.marker)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return err == nil, nil
}

func (w *slsWorld) cleanup() {
	if w.ch != nil {
		w.ch.Close()
	}
	if w.p != nil && !w.exited {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = w.p.StopTree(ctx, 0)
		cancel()
	}
	if w.dir != "" {
		_ = os.RemoveAll(w.dir)
	}
}

// --- Scenario: No command before go ---

func (w *slsWorld) givenStageWithBackend(mode, backend string) error {
	if mode != launch.Mode {
		return fmt.Errorf("the stage mode is %q, want %q", launch.Mode, mode)
	}
	w.plan = launch.Plan{Kind: launch.Unconfined, Backend: sandbox.Backend(backend)}
	return w.start()
}

func (w *slsWorld) whenReadReadyThenClose() error {
	ctx, cancel := w.timeout()
	defer cancel()
	r, err := w.ch.ReadReady(ctx)
	if err != nil {
		return fmt.Errorf("ReadReady: %w; stage stderr %q", err, w.stderr.String())
	}
	w.ready = r
	if ok, err := w.markerExists(); err != nil || ok {
		return fmt.Errorf("the command ran before go (marker exists=%v, err=%v)", ok, err)
	}
	w.ch.Reject()
	return nil
}

func (w *slsWorld) thenMarkerNeverExists() error {
	w.wait()
	ok, err := w.markerExists()
	if err != nil {
		return err
	}
	if ok {
		return errors.New("the marker exists: the command ran without go")
	}
	if got := w.stderr.String(); !strings.Contains(got, "no command ran") {
		return fmt.Errorf("stage stderr = %q, want it to say no command ran", got)
	}
	return nil
}

func (w *slsWorld) thenStageExitsNonZero() error {
	code := w.wait()
	if code == 0 {
		return errors.New("the stage exited 0 after the host closed the channel without go")
	}
	if code != launch.ExitFailed {
		return fmt.Errorf("the stage exited %d, want %d", code, launch.ExitFailed)
	}
	return nil
}

// --- Scenario: Unconfined plan reaches go ---

func (w *slsWorld) givenModePlan(mode, backend string) error {
	m, err := sandbox.ParseMode(mode)
	if err != nil {
		return err
	}
	if m != sandbox.DangerFullAccess {
		return fmt.Errorf("mode %s is not an unconfined plan", m)
	}
	w.plan = launch.Plan{Kind: launch.Unconfined, Backend: sandbox.Backend(backend)}
	return w.start()
}

func (w *slsWorld) whenHostAnswersGo() error {
	ctx, cancel := w.timeout()
	defer cancel()
	r, err := w.ch.ReadReady(ctx)
	if err != nil {
		return fmt.Errorf("ReadReady: %w; stage stderr %q", err, w.stderr.String())
	}
	w.ready = r
	if err := r.Agrees(w.plan); err != nil {
		return fmt.Errorf("the ready disagrees with its plan: %w", err)
	}
	if err := w.ch.Go(); err != nil {
		return fmt.Errorf("Go: %w", err)
	}
	if err := w.ch.AwaitExec(ctx); err != nil {
		return fmt.Errorf("AwaitExec: %w; stage stderr %q", err, w.stderr.String())
	}
	if code := w.wait(); code != 0 {
		return fmt.Errorf("the command exited %d, want 0; stage stderr %q", code, w.stderr.String())
	}
	return nil
}

func (w *slsWorld) thenMarkerExists() error {
	ok, err := w.markerExists()
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("the marker does not exist: the command did not run after go")
	}
	return nil
}

// reportedKind derives the confinement kind from the ready frame the stage sent,
// not from the plan the test wrote: the frame must agree with exactly one kind.
func (w *slsWorld) reportedKind() (launch.Kind, error) {
	var agreed []launch.Kind
	for _, k := range []launch.Kind{launch.Confined, launch.Unconfined} {
		if w.ready.Agrees(launch.Plan{Kind: k, Backend: w.ready.Backend}) == nil {
			agreed = append(agreed, k)
		}
	}
	if len(agreed) != 1 {
		return "", fmt.Errorf("the ready frame %+v agrees with kinds %v, want exactly one", w.ready, agreed)
	}
	return agreed[0], nil
}

func (w *slsWorld) thenConfinementIs(kind, canary string) error {
	got, err := w.reportedKind()
	if err != nil {
		return err
	}
	if !strings.EqualFold(string(got), kind) {
		return fmt.Errorf("the stage reported kind %s, want %s", got, kind)
	}
	if w.ready.Canary != launch.Canary(canary) {
		return fmt.Errorf("ready canary = %s, want %s", w.ready.Canary, canary)
	}
	if w.ready.Backend != sandbox.BackendNone {
		return fmt.Errorf("ready backend = %s, want none", w.ready.Backend)
	}
	if len(w.ready.Governed) != 0 {
		return fmt.Errorf("an unconfined stage claims to govern %v", w.ready.Governed)
	}
	return nil
}

// --- Scenario: Ready message decoding ---

func slsFrame(body string) []byte {
	b := make([]byte, 4, 4+len(body))
	binary.BigEndian.PutUint32(b, uint32(len(body)))
	return append(b, body...)
}

func (w *slsWorld) givenFrames() error {
	encoded, err := launch.EncodeReady(launch.Ready{
		Backend:  sandbox.BackendBwrap,
		Governed: []sandbox.Effect{"fs-write", "network"},
		Canary:   launch.CanaryRefused,
	})
	if err != nil {
		return fmt.Errorf("EncodeReady: %w", err)
	}
	none := slsFrame(`{"msg":"ready","backend":"none","governed":[],"canary":"not-tried"}`)
	w.cases = []*slsDecodeCase{
		{name: "encoded bwrap ready", frame: encoded, valid: true, want: launch.Ready{
			Backend: sandbox.BackendBwrap, Governed: []sandbox.Effect{"fs-write", "network"}, Canary: launch.CanaryRefused}},
		{name: "hand-built none ready", frame: none, valid: true, want: launch.Ready{
			Backend: sandbox.BackendNone, Governed: []sandbox.Effect{}, Canary: launch.CanaryNotTried}},
		{name: "empty input", frame: []byte{}},
		{name: "cut header", frame: encoded[:2]},
		{name: "header only", frame: encoded[:4]},
		{name: "cut body", frame: encoded[:len(encoded)-1]},
		{name: "length longer than body", frame: append([]byte{0, 0, 0, 40}, `{"msg":"ready"`...)},
	}
	return nil
}

func (w *slsWorld) whenDecode() error {
	if len(w.cases) == 0 {
		return errors.New("no frames to decode")
	}
	for _, c := range w.cases {
		c.got, c.err = launch.DecodeReady(c.frame)
	}
	return nil
}

func (w *slsWorld) thenValidFramesYield() error {
	n := 0
	for _, c := range w.cases {
		if !c.valid {
			continue
		}
		n++
		if c.err != nil {
			return fmt.Errorf("%s: DecodeReady error %v", c.name, c.err)
		}
		if c.got.Backend != c.want.Backend || c.got.Canary != c.want.Canary || !slices.Equal(c.got.Governed, c.want.Governed) {
			return fmt.Errorf("%s: DecodeReady = %+v, want %+v", c.name, c.got, c.want)
		}
	}
	if n == 0 {
		return errors.New("no valid frames were decoded")
	}
	return nil
}

func (w *slsWorld) thenTruncatedReturn(code string) error {
	if code != string(sandbox.CodeLaunchFailed) {
		return fmt.Errorf("the scenario names %q, the package's code is %q", code, sandbox.CodeLaunchFailed)
	}
	n := 0
	for _, c := range w.cases {
		if c.valid {
			continue
		}
		n++
		if c.err == nil {
			return fmt.Errorf("%s: DecodeReady accepted a truncated frame as %+v", c.name, c.got)
		}
		if !herr.Has(c.err, herr.Reason(code)) {
			return fmt.Errorf("%s: DecodeReady error %v, want %s", c.name, c.err, code)
		}
	}
	if n == 0 {
		return errors.New("no truncated frames were decoded")
	}
	return nil
}

func InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_sandbox_launch_stage(ctx *godog.ScenarioContext) {
	w := &slsWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = slsWorld{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.cleanup()
		return c, err
	})

	ctx.Step(`^the test binary re-executed as "([^"]*)" with the "([^"]*)" backend and a marker-writing helper as the command$`, w.givenStageWithBackend)
	ctx.Step(`^the host reads ready and then closes the channel without go$`, w.whenReadReadyThenClose)
	ctx.Step(`^the marker file never exists$`, w.thenMarkerNeverExists)
	ctx.Step(`^the stage exits non-zero$`, w.thenStageExitsNonZero)

	ctx.Step(`^a "([^"]*)" plan with backend "([^"]*)"$`, w.givenModePlan)
	ctx.Step(`^the host answers go$`, w.whenHostAnswersGo)
	ctx.Step(`^the marker exists$`, w.thenMarkerExists)
	ctx.Step(`^the reported confinement is "([^"]*)" with canary "([^"]*)"$`, w.thenConfinementIs)

	ctx.Step(`^byte strings for valid and truncated ready frames$`, w.givenFrames)
	ctx.Step(`^the decoder parses them$`, w.whenDecode)
	ctx.Step(`^valid frames yield the backend and effects$`, w.thenValidFramesYield)
	ctx.Step(`^truncated frames return "([^"]*)"$`, w.thenTruncatedReturn)
}

func TestE2E_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_sandbox_launch_stage(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_sandbox_launch_stage,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_13_sandbox_launch_linux_backends_and_shell_tools_sandbox_launch_stage.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
