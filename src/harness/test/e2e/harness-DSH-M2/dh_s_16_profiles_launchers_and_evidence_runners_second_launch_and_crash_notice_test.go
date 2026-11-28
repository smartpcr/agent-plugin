//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/auth"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
)

// slcBin is dsh built once with the testhook tag (fake route llm.testkit-route
// and the opener that writes slcOpened instead of starting a browser).
var slcBin string

const (
	slcOpened      = "testkit-opener: opened"
	slcStepTimeout = 30 * time.Second
)

var slcNotice = regexp.MustCompile(`did not stop cleanly; report: (\S+);`)

// slcState is one scenario: a temp home, the first `dsh web` and the results
// of the later dsh runs on that home.
type slcState struct {
	tmp, root string
	port      int
	ctx       context.Context
	cancel    context.CancelFunc

	cmd    *exec.Cmd
	stderr *testkit.Buffer
	done   chan struct{}

	status         int
	stdout, errout string
	report         string
}

// slcEnv is a launch's environment: a display, so the host may open a browser,
// and no SSH session.
func slcEnv() []string {
	env := []string{"DISPLAY=:0"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "SSH_") && !strings.HasPrefix(kv, "DISPLAY=") {
			env = append(env, kv)
		}
	}
	return env
}

func (s *slcState) running() error {
	if slcBin == "" {
		return errors.New("dsh was not built")
	}
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 2*time.Minute)
	// A short home: the control socket's path is bounded (about 100 bytes).
	dir, err := os.MkdirTemp("", "dshe-")
	if err != nil {
		return err
	}
	s.tmp, s.root = dir, filepath.Join(dir, "home")
	if err := home.Ensure(home.New(s.root)); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.port = ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	layer := "- insert:\n    - id: route\n      name: llm.testkit-route\n      config:\n        id: fake\n        url: \"http://127.0.0.1:1\"\n" +
		"- id: host.listen\n  name: host.listen\n  merge:\n    port: " + strconv.Itoa(s.port) + "\n"
	path := profile.LayerPath(s.root, profile.Web)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(layer), 0o600); err != nil {
		return err
	}

	s.stderr, s.done = &testkit.Buffer{}, make(chan struct{})
	s.cmd = exec.CommandContext(s.ctx, slcBin, "--home", s.root, "web")
	s.cmd.Env, s.cmd.Dir, s.cmd.Stderr = slcEnv(), s.tmp, s.stderr
	if err := s.cmd.Start(); err != nil {
		return err
	}
	go func() { defer close(s.done); _ = s.cmd.Wait() }()
	return s.waitOpened(1)
}

// waitOpened waits until the first host has opened its browser n times.
func (s *slcState) waitOpened(n int) error {
	deadline := time.After(slcStepTimeout)
	for strings.Count(s.stderr.String(), slcOpened) < n {
		select {
		case <-s.done:
			return fmt.Errorf("dsh web exited %d before opening its browser %d times:\n%s", s.cmd.ProcessState.ExitCode(), n, s.stderr)
		case <-deadline:
			return fmt.Errorf("dsh web opened its browser %d times, want %d:\n%s", strings.Count(s.stderr.String(), slcOpened), n, s.stderr)
		case <-time.After(20 * time.Millisecond):
		}
	}
	return nil
}

// run runs the built dsh on the home and keeps its status and output.
func (s *slcState) run(args ...string) error {
	ctx, cancel := context.WithTimeout(s.ctx, slcStepTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, slcBin, append([]string{"--home", s.root}, args...)...)
	stdout, stderr := &testkit.Buffer{}, &testkit.Buffer{}
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = slcEnv(), s.tmp, stdout, stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return err
	}
	if ctx.Err() != nil {
		return fmt.Errorf("dsh %q did not exit within %s:\n%s", args, slcStepTimeout, stderr)
	}
	s.status, s.stdout, s.errout = cmd.ProcessState.ExitCode(), stdout.String(), stderr.String()
	return nil
}

func (s *slcState) pid() string { return strconv.Itoa(s.cmd.Process.Pid) }

func (s *slcState) secondWeb() error { return s.run("web") }

func (s *slcState) reusedHost() error {
	want := "dsh web: opened in the browser of the host (process " + s.pid() + ")"
	if s.status != output.OK || !strings.Contains(s.errout, want) {
		return fmt.Errorf("second dsh web exited %d, want 0 with %q:\n%s", s.status, want, s.errout)
	}
	return nil
}

// slcLink matches a sign-in link as auth.(*Auth).Mint shapes it:
// <audience>/?token=<tok>.
var slcLink = regexp.MustCompile(`https?://\S+/\?token=\S+|\?token=`)

// realLink mints a link with the real host.auth, on the scenario's port, so
// slcLink is known to match what a host prints and cannot drift from it.
func (s *slcState) realLink() (string, error) {
	db, err := sqlite.Open(s.ctx, filepath.Join(s.tmp, "mint.db"))
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close(s.ctx) }()
	a, err := auth.New(s.ctx, db, auth.Options{Audience: "http://127.0.0.1:" + strconv.Itoa(s.port)})
	if err != nil {
		return "", err
	}
	defer func() { _ = a.Close() }()
	return a.Mint()
}

func (s *slcState) noLink() error {
	link, err := s.realLink()
	if err != nil {
		return fmt.Errorf("mint a real sign-in link: %w", err)
	}
	if !slcLink.MatchString("dsh: open " + link + "\n") {
		return fmt.Errorf("the link pattern %s does not match a real sign-in link %q", slcLink, link)
	}
	for name, out := range map[string]string{"stdout": s.stdout, "stderr": s.errout} {
		if slcLink.MatchString(out) {
			return fmt.Errorf("the second launch printed a sign-in link on %s:\n%s", name, out)
		}
	}
	return nil
}

func (s *slcState) openedAgain() error {
	if err := s.waitOpened(2); err != nil {
		return err
	}
	if n := strings.Count(s.stderr.String(), slcOpened); n != 2 {
		return fmt.Errorf("the running host opened its browser %d times, want 2:\n%s", n, s.stderr)
	}
	return nil
}

func (s *slcState) records() ([]string, error) {
	return filepath.Glob(filepath.Join(home.New(s.root).Run(), "web-*.json"))
}

func (s *slcState) oneListener() error {
	select {
	case <-s.done:
		return fmt.Errorf("the first host exited %d:\n%s", s.cmd.ProcessState.ExitCode(), s.stderr)
	default:
	}
	recs, err := s.records()
	if err != nil {
		return err
	}
	if len(recs) != 1 {
		return fmt.Errorf("runtime records %v, want one host", recs)
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(s.port), 5*time.Second)
	if err != nil {
		return fmt.Errorf("the host's listener on port %d: %w", s.port, err)
	}
	_ = conn.Close()
	if err := s.run("status"); err != nil {
		return err
	}
	if s.status != output.OK || !strings.Contains(s.stdout, "process "+s.pid()) {
		return fmt.Errorf("dsh status exited %d, want 0 listing process %s:\n%s\n%s", s.status, s.pid(), s.stdout, s.errout)
	}
	if n := strings.Count(s.stdout, "process "); n != 1 {
		return fmt.Errorf("dsh status lists %d hosts, want 1:\n%s", n, s.stdout)
	}
	return nil
}

// kill ends the first host outright: SIGKILL on Unix, TerminateProcess on
// Windows, so it writes nothing on the way out.
func (s *slcState) kill() error {
	if err := s.cmd.Process.Kill(); err != nil {
		return err
	}
	select {
	case <-s.done:
	case <-time.After(slcStepTimeout):
		return errors.New("the killed host did not exit")
	}
	recs, err := s.records()
	if err != nil {
		return err
	}
	if len(recs) != 1 {
		return fmt.Errorf("after the kill the runtime records are %v, want the killed host's record left behind", recs)
	}
	return nil
}

func (s *slcState) status4() error { return s.run("status") }

func (s *slcState) namesKilled() error {
	if s.status != output.Unavailable {
		return fmt.Errorf("dsh status exited %d, want %d:\n%s", s.status, output.Unavailable, s.errout)
	}
	if !strings.Contains(s.errout, "(process "+s.pid()+") did not stop cleanly") {
		return fmt.Errorf("dsh status does not name process %s as crashed:\n%s", s.pid(), s.errout)
	}
	return nil
}

func (s *slcState) namesReport() error {
	m := slcNotice.FindStringSubmatch(s.errout)
	if m == nil || m[1] == "none" {
		return fmt.Errorf("dsh status names no crash report:\n%s", s.errout)
	}
	s.report = m[1]
	fi, err := os.Stat(s.report)
	if err != nil {
		return fmt.Errorf("the named crash report: %w", err)
	}
	if fi.IsDir() || fi.Size() == 0 {
		return fmt.Errorf("the named crash report %s is empty or a directory", s.report)
	}
	return nil
}

func (s *slcState) suggestsSafe() error {
	if !strings.Contains(s.errout, "start with --safe (dsh --safe web)") {
		return fmt.Errorf("dsh status does not suggest --safe:\n%s", s.errout)
	}
	return nil
}

func (s *slcState) crashSame() error {
	if err := s.run("crash"); err != nil {
		return err
	}
	if s.status != output.OK || strings.TrimSpace(s.stdout) != s.report {
		return fmt.Errorf("dsh crash exited %d with %q, want 0 with %s:\n%s", s.status, s.stdout, s.report, s.errout)
	}
	return nil
}

func (s *slcState) cleanup() error {
	if s.cmd != nil && s.cmd.Process != nil && s.done != nil {
		select {
		case <-s.done:
		default:
			_ = s.cmd.Process.Kill()
			<-s.done
		}
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.tmp == "" {
		return nil
	}
	// Windows releases a stopped process's handles on the home shortly after
	// the process is reported gone.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var err error
	if procwait.Poll(ctx, func() bool { err = os.RemoveAll(s.tmp); return err == nil }) == nil {
		return nil
	}
	return err
}

func InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_second_launch_and_crash_notice(ctx *godog.ScenarioContext) {
	s := &slcState{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if cerr := s.cleanup(); cerr != nil && err == nil {
			return c, cerr
		}
		return c, err
	})
	ctx.Step(`^a built "dsh web" running on a temp home$`, s.running)
	ctx.Step(`^a second "dsh web" starts on the same home$`, s.secondWeb)
	ctx.Step(`^the second launch exits 0 after launch\.open on the running host$`, s.reusedHost)
	ctx.Step(`^the second launch printed no link$`, s.noLink)
	ctx.Step(`^the running host opened its browser a second time$`, s.openedAgain)
	ctx.Step(`^only one listener exists on the home$`, s.oneListener)
	ctx.Step(`^the first host is killed outright$`, s.kill)
	ctx.Step(`^"dsh status" runs on the same home$`, s.status4)
	ctx.Step(`^dsh status exits 4 naming the killed host$`, s.namesKilled)
	ctx.Step(`^it names a crash report path that exists$`, s.namesReport)
	ctx.Step(`^it suggests --safe$`, s.suggestsSafe)
	ctx.Step(`^"dsh crash" prints the same report path$`, s.crashSame)
}

func TestE2E_dh_s_16_profiles_launchers_and_evidence_runners_second_launch_and_crash_notice(t *testing.T) {
	// Build here, on t's own goroutine: launcher.Build fails t directly, which
	// must not happen from a godog scenario subtest.
	slcBin = launcher.Build(t, filepath.Join("..", "..", ".."), "./cmd/dsh", "testhook")
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_second_launch_and_crash_notice,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_16_profiles_launchers_and_evidence_runners_second_launch_and_crash_notice.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
