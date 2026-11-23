//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// TestMain lets the e2e test binary be re-executed as a child or grandchild by
// subprocesstest (TC-06c): with SUBPROCESSTEST_ROLE set it plays that role and
// exits; otherwise it runs the tests.
func TestMain(m *testing.M) { subprocesstest.Main(m) }

// lhptOuterT is the suite's *testing.T: subprocesstest helpers need a
// testing.TB, which godog's per-scenario TestingT does not expose.
var lhptOuterT *testing.T

const lhptPlainSetting = "PLAIN_SETTING"

type lhptState struct {
	// error table
	secret string
	inner  error
	outer  error
	kind   herr.Reason

	// process tree
	proc       *subprocess.Process
	child      int
	grandchild int
	stopErr    error
	stopTook   time.Duration

	// environment
	restoreEnv []func()
	childOut   string
}

// ---- Error kinds map to one table ----

func (s *lhptState) innerError(reason, secret string) error {
	s.secret = secret
	s.inner = herr.Newf(herr.Reason(reason), "start %s with key %v", "/bin/tool", herr.Secret(secret))
	return nil
}

func (s *lhptState) wrapThreeDeep(first, last string) error {
	if s.inner == nil {
		return errors.New("no inner error")
	}
	e1 := herr.Wrap(herr.Reason(first), s.inner, "launch")
	e2 := fmt.Errorf("boot step: %w", e1)
	s.outer = herr.Wrap(herr.Reason(last), e2, "run")
	if s.outer == nil {
		return errors.New("Wrap returned nil for a non-nil cause")
	}
	return nil
}

func (s *lhptState) readKind() error {
	s.kind = herr.Kind(s.outer)
	return nil
}

func (s *lhptState) kindIs(want string) error {
	if string(s.kind) != want {
		return fmt.Errorf("herr.Kind = %q, want %q (error %v)", s.kind, want, s.outer)
	}
	r, ok := herr.ReasonOf(s.outer)
	if !ok || string(r) != want {
		return fmt.Errorf("herr.ReasonOf = %q, %v; want %q", r, ok, want)
	}
	if !herr.Has(s.outer, herr.Reason(want)) {
		return fmt.Errorf("herr.Has does not see %q", want)
	}
	if !errors.Is(s.outer, herr.New(herr.Reason(want), "")) {
		return fmt.Errorf("errors.Is does not match a %q sentinel", want)
	}
	return nil
}

func (s *lhptState) kindValid(domain string) error {
	if !s.kind.Valid() {
		return fmt.Errorf("reason %q is not <domain>/<reason>", s.kind)
	}
	if s.kind.Domain() != domain {
		return fmt.Errorf("domain of %q = %q, want %q", s.kind, s.kind.Domain(), domain)
	}
	return nil
}

func (s *lhptState) noSecretInFormatting() error {
	renderings := map[string]string{"Error()": s.outer.Error()}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X"} {
		renderings[verb] = fmt.Sprintf(verb, s.outer)
	}
	if b, err := json.Marshal(map[string]any{"err": s.outer, "msg": s.outer.Error()}); err == nil {
		renderings["json"] = string(b)
	} else {
		return fmt.Errorf("json.Marshal: %v", err)
	}
	var text, js bytes.Buffer
	slog.New(slog.NewTextHandler(&text, nil)).Error("failed", "err", s.outer, "key", herr.Secret(s.secret))
	slog.New(slog.NewJSONHandler(&js, nil)).Error("failed", "err", s.outer, "key", herr.Secret(s.secret))
	renderings["slog text"] = text.String()
	renderings["slog json"] = js.String()

	hexSecret := fmt.Sprintf("%x", s.secret)
	for how, out := range renderings {
		if strings.Contains(out, s.secret) || strings.Contains(strings.ToLower(out), hexSecret) {
			return fmt.Errorf("%s leaks the secret: %q", how, out)
		}
	}
	if !strings.Contains(renderings["Error()"], herr.Redacted) {
		return fmt.Errorf("Error() = %q, want the %q placeholder", renderings["Error()"], herr.Redacted)
	}
	for _, code := range []string{"cli/command-failed", "boot/launch-failed", "subprocess/start-failed"} {
		if !strings.Contains(renderings["Error()"], code) {
			return fmt.Errorf("Error() = %q does not name %q", renderings["Error()"], code)
		}
	}
	return nil
}

// ---- Process tree stopped and awaited ----

func (s *lhptState) startTree() error {
	if lhptOuterT == nil {
		return errors.New("suite *testing.T not set")
	}
	s.proc, s.child, s.grandchild = subprocesstest.Tree(lhptOuterT)
	if s.child <= 0 || s.grandchild <= 0 || s.child == s.grandchild {
		return fmt.Errorf("tree child reported pids child=%d grandchild=%d", s.child, s.grandchild)
	}
	return nil
}

func (s *lhptState) bothRunning() error {
	if !subprocesstest.Alive(s.child) || !subprocesstest.Alive(s.grandchild) {
		return fmt.Errorf("tree not running: child %d alive=%v, grandchild %d alive=%v",
			s.child, subprocesstest.Alive(s.child), s.grandchild, subprocesstest.Alive(s.grandchild))
	}
	return nil
}

func (s *lhptState) stopTree(secs int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	s.stopErr = s.proc.StopTree(ctx, time.Duration(secs)*time.Second)
	s.stopTook = time.Since(start)
	return nil
}

func (s *lhptState) stopTreeOK() error {
	if s.stopErr != nil {
		return fmt.Errorf("StopTree: %v (kind %q)", s.stopErr, herr.Kind(s.stopErr))
	}
	return nil
}

func (s *lhptState) bothGone() error {
	// Checked immediately after StopTree returned: no polling, so this proves
	// StopTree awaited both exits. Exited, not !Alive: the orphaned grandchild is
	// reaped by its reaper on its own time, and kill(pid, 0) still finds the
	// zombie until then (M1-G-BOOT-36).
	if !subprocesstest.Exited(s.child) || !subprocesstest.Exited(s.grandchild) {
		return fmt.Errorf("StopTree returned after %v with child exited=%v, grandchild exited=%v (owner %+v)",
			s.stopTook, subprocesstest.Exited(s.child), subprocesstest.Exited(s.grandchild), s.proc.Owner())
	}
	return nil
}

func (s *lhptState) treeGoneAndIdempotent() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.proc.TreeGone(ctx); err != nil {
		return fmt.Errorf("TreeGone after StopTree: %v", err)
	}
	if err := s.proc.StopTree(ctx, 0); err != nil {
		return fmt.Errorf("second StopTree: %v", err)
	}
	return nil
}

// ---- Child environment holds no secret ----

func (s *lhptState) setParentEnv(name, value string) {
	old, had := os.LookupEnv(name)
	_ = os.Setenv(name, value)
	s.restoreEnv = append(s.restoreEnv, func() {
		if had {
			_ = os.Setenv(name, old)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}

func (s *lhptState) parentHasSecret(name string) error {
	s.setParentEnv(name, "sk-dummy-never-leaks")
	s.setParentEnv(lhptPlainSetting, "kept")
	if v, ok := os.LookupEnv(name); !ok || v == "" {
		return fmt.Errorf("parent environment does not hold %s", name)
	}
	return nil
}

func (s *lhptState) probeSpec() subprocess.Spec {
	return subprocesstest.Spec(lhptOuterT, subprocesstest.RoleEnvProbe,
		"DSH_DEEPSEEK_API_KEY", lhptPlainSetting)
}

func (s *lhptState) runProbe(spec subprocess.Spec) error {
	if lhptOuterT == nil {
		return errors.New("suite *testing.T not set")
	}
	s.childOut = strings.ReplaceAll(subprocesstest.Output(lhptOuterT, spec), "\r\n", "\n")
	if s.childOut == "" {
		return errors.New("child printed nothing")
	}
	return nil
}

func (s *lhptState) startWithList() error {
	spec := s.probeSpec()
	spec.Env = append(spec.Env, "EXPLICIT_SETTING=1")
	return s.runProbe(spec)
}

func (s *lhptState) startWithBuiltEnv() error {
	spec := s.probeSpec()
	spec.Env = subprocess.BuildEnv(os.Environ(), subprocess.EnvOptions{Entries: spec.Env})
	if spec.Env.Get("DSH_DEEPSEEK_API_KEY") != "" {
		return fmt.Errorf("BuildEnv kept the secret: %q", spec.Env)
	}
	return s.runProbe(spec)
}

func (s *lhptState) childReports(name, state string) error {
	line := name + "=" + state
	for _, l := range strings.Split(s.childOut, "\n") {
		if strings.TrimSpace(l) == line {
			return nil
		}
	}
	return fmt.Errorf("child reported %q, want line %q", s.childOut, line)
}

func InitializeScenario_boot_launch_egress_secrets_and_test_kit_leaf_helpers_and_process_trees(ctx *godog.ScenarioContext) {
	s := &lhptState{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s.proc != nil {
			sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = s.proc.StopTree(sctx, 0)
			cancel()
		}
		for i := len(s.restoreEnv) - 1; i >= 0; i-- {
			s.restoreEnv[i]()
		}
		return c, err
	})

	ctx.Step(`^an in-memory error with reason "([^"]*)" carrying the secret "([^"]*)"$`, s.innerError)
	ctx.Step(`^the error is wrapped three levels deep by "([^"]*)", a fmt %w wrap and "([^"]*)"$`, s.wrapThreeDeep)
	ctx.Step(`^herr\.Kind is read$`, s.readKind)
	ctx.Step(`^it returns the innermost reason "([^"]*)"$`, s.kindIs)
	ctx.Step(`^the reason is a valid "<domain>/<reason>" code with domain "([^"]*)"$`, s.kindValid)
	ctx.Step(`^no formatting of the error includes the secret value$`, s.noSecretInFormatting)

	ctx.Step(`^the test binary re-exec'd as a child that spawns a grandchild ignoring SIGTERM$`, s.startTree)
	ctx.Step(`^both the child and the grandchild are running$`, s.bothRunning)
	ctx.Step(`^StopTree runs with a (\d+) s grace$`, s.stopTree)
	ctx.Step(`^StopTree returns without error$`, s.stopTreeOK)
	ctx.Step(`^both processes are gone when StopTree returns$`, s.bothGone)
	ctx.Step(`^the tree is reported gone and a second StopTree is a no-op$`, s.treeGoneAndIdempotent)

	ctx.Step(`^a parent environment containing "([^"]*)" set to a dummy value$`, s.parentHasSecret)
	ctx.Step(`^a child is started through subprocess with a given env list$`, s.startWithList)
	ctx.Step(`^a child is started through subprocess with an env built from the parent environment$`, s.startWithBuiltEnv)
	ctx.Step(`^the child reports "([^"]*)" as (absent|present)$`, s.childReports)
}

func TestE2E_boot_launch_egress_secrets_and_test_kit_leaf_helpers_and_process_trees(t *testing.T) {
	lhptOuterT = t
	defer func() { lhptOuterT = nil }()
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_boot_launch_egress_secrets_and_test_kit_leaf_helpers_and_process_trees,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"boot_launch_egress_secrets_and_test_kit_leaf_helpers_and_process_trees.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
