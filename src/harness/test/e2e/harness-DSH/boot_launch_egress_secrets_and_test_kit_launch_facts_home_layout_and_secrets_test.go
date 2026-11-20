//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/expr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// lfhsOuterT is the suite's *testing.T; homes come from its TempDir.
var lfhsOuterT *testing.T

var lfhsErrUnauthorized = errors.New("unauthorized")

// lfhsFakeSource returns one value and counts lookups.
type lfhsFakeSource struct {
	value   string
	lookups int
}

func (*lfhsFakeSource) Origin() string { return "fake" }

func (f *lfhsFakeSource) Lookup(_ context.Context, _ secret.Ref) (secret.Value, bool, error) {
	f.lookups++
	return secret.NewValue(f.value), true, nil
}

type lfhsState struct {
	// home
	layout   home.Layout
	checkErr error

	// secret
	src      *lfhsFakeSource
	store    *secret.Store
	ref      secret.Ref
	cfgDump  string
	uses     int
	revealed string
	useErr   error
	logs     bytes.Buffer
	captured []string

	// launch
	data   launch.Data
	eval   *expr.Evaluator
	result any
}

// ---- Home created private ----

func (s *lfhsState) emptyHome() error {
	s.layout = home.New(lfhsOuterT.TempDir())
	return nil
}

func (s *lfhsState) layoutCheckRuns() error {
	s.checkErr = home.Ensure(s.layout)
	return nil
}

func (s *lfhsState) layoutCheckRunsAgain() error {
	s.checkErr = home.Check(s.layout)
	return nil
}

func (s *lfhsState) everyDirPrivate() error {
	if s.checkErr != nil {
		return fmt.Errorf("layout check failed: %v", s.checkErr)
	}
	for _, dir := range s.layout.Dirs() {
		if err := home.CheckPrivate(dir); err != nil {
			return fmt.Errorf("%s: %v", dir, err)
		}
		if err := lfhsAssertPrivate(dir); err != nil {
			return err
		}
	}
	return nil
}

func (s *lfhsState) loosen(name string) error {
	if name != home.SpillDir+"/" {
		return fmt.Errorf("unexpected directory %q", name)
	}
	if err := lfhsLoosen(s.layout.Spill()); err != nil {
		return err
	}
	if err := lfhsAssertPrivate(s.layout.Spill()); err == nil {
		return errors.New("loosening left spill/ private")
	}
	return nil
}

func (s *lfhsState) checkFailsNaming(name string) error {
	if s.checkErr == nil {
		return errors.New("the check passed on a loosened home")
	}
	if !herr.Has(s.checkErr, home.ReasonNotPrivate) {
		return fmt.Errorf("reason of %v is not %s", s.checkErr, home.ReasonNotPrivate)
	}
	if !strings.Contains(s.checkErr.Error(), name) {
		return fmt.Errorf("error %q does not name %s", s.checkErr, name)
	}
	return nil
}

// ---- Secret resolved per use and redacted ----

func (s *lfhsState) fakeSource(value string) error {
	s.src = &lfhsFakeSource{value: value}
	s.store = secret.NewStore(s.src)
	return nil
}

func (s *lfhsState) configNaming(ref string) error {
	var cfg struct {
		APIKey secret.Ref `json:"apiKey"`
		Model  string     `json:"model"`
	}
	if err := json.Unmarshal([]byte(`{"apiKey":"`+ref+`","model":"chat"}`), &cfg); err != nil {
		return err
	}
	s.ref = cfg.APIKey
	s.store.Name(s.ref)
	dump, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	s.cfgDump = string(dump) + fmt.Sprintf(" %+v", cfg)
	return nil
}

func (s *lfhsState) failingUse() error {
	log := slog.New(slog.NewJSONHandler(&s.logs, nil))
	s.useErr = s.store.Use(context.Background(), s.ref, func(v secret.Value) error {
		s.uses++
		s.revealed = v.Reveal()
		log.Info("calling provider", "key", v, "ref", s.ref)
		return fmt.Errorf("deepseek: %w: bad key %q (Bearer %s)", lfhsErrUnauthorized, v.Reveal(), v.Reveal())
	})
	if s.useErr == nil {
		return errors.New("the failing use returned no error")
	}
	if !errors.Is(s.useErr, lfhsErrUnauthorized) {
		return fmt.Errorf("error %v does not match the provider's", s.useErr)
	}
	if errors.Unwrap(s.useErr) != nil {
		return errors.New("the masked error unwraps to the unmasked one")
	}
	return nil
}

func (s *lfhsState) captureErrorAndLogs() error {
	slog.New(slog.NewJSONHandler(&s.logs, nil)).Error("use failed", "err", s.useErr)
	slog.New(slog.NewTextHandler(&s.logs, nil)).Error("use failed", "err", s.useErr, "ref", s.ref)
	e := s.useErr
	s.captured = []string{
		e.Error(), fmt.Sprintf("%v", e), fmt.Sprintf("%+v", e), fmt.Sprintf("%#v", e), fmt.Sprintf("%q", e),
		s.logs.String(), s.cfgDump,
	}
	return nil
}

func (s *lfhsState) usedOnce() error {
	if s.uses != 1 {
		return fmt.Errorf("used %d times, want 1", s.uses)
	}
	if s.revealed != s.src.value {
		return fmt.Errorf("the use received %d bytes, not the source's value", len(s.revealed))
	}
	return nil
}

func (s *lfhsState) readOnce() error {
	if s.src.lookups != 1 {
		return fmt.Errorf("source read %d times, want 1", s.src.lookups)
	}
	return nil
}

func (s *lfhsState) appearsNowhere(value string) error {
	for i, c := range s.captured {
		if strings.Contains(c, value) {
			return fmt.Errorf("capture %d holds the secret value", i)
		}
	}
	return nil
}

func (s *lfhsState) errorNamesRef(ref string) error {
	if !strings.Contains(s.useErr.Error(), ref) {
		return fmt.Errorf("error %q does not name %s", s.useErr, ref)
	}
	if !strings.Contains(s.cfgDump, ref) {
		return fmt.Errorf("configuration dump %q does not name %s", s.cfgDump, ref)
	}
	return nil
}

func (s *lfhsState) logsRedacted() error {
	if !strings.Contains(s.logs.String(), secret.Redacted) {
		return fmt.Errorf("logs lack %q: %s", secret.Redacted, s.logs.String())
	}
	return nil
}

// ---- Launch facts feed the expression scope ----

func (s *lfhsState) launchFacts(cwd, osName string) error {
	s.data = launch.Data{Profile: "default", Home: "/h", Cwd: cwd, Version: launch.DevelVersion, OS: osName}
	env := launch.NewSnapshot(osName, []string{"EDITOR=vim"})
	e, err := expr.New(launch.Scope(s.data, env, nil))
	if err != nil {
		return err
	}
	s.eval = e
	return nil
}

func (s *lfhsState) evaluated(source string) error {
	quoted, err := json.Marshal(source)
	if err != nil {
		return err
	}
	node, err := loader.CodecFor("layer.json").Decode("layer.json", quoted)
	if err != nil {
		return err
	}
	if node.Kind != loader.ExprNode {
		return fmt.Errorf("%s decoded as kind %v, not an expression", source, node.Kind)
	}
	s.result, err = s.eval.Eval(node.Text, loader.Scope{Kind: loader.DisabledScope})
	return err
}

func (s *lfhsState) yields(want string) error {
	if s.result != want {
		return fmt.Errorf("got %#v, want %q", s.result, want)
	}
	return nil
}

func (s *lfhsState) exprYields(source, want string) error {
	got, err := s.eval.Eval(source, loader.Scope{Kind: loader.DisabledScope})
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s = %#v, want %q", source, got, want)
	}
	return nil
}

func InitializeScenario_boot_launch_egress_secrets_and_test_kit_launch_facts_home_layout_and_secrets(ctx *godog.ScenarioContext) {
	s := &lfhsState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = lfhsState{}
		return c, nil
	})

	ctx.Step(`^an empty temporary directory as the home$`, s.emptyHome)
	ctx.Step(`^the layout check runs$`, s.layoutCheckRuns)
	ctx.Step(`^the layout check runs again$`, s.layoutCheckRunsAgain)
	ctx.Step(`^every directory of the layout exists private to its owner$`, s.everyDirPrivate)
	ctx.Step(`^the test loosens "([^"]*)" for this operating system$`, s.loosen)
	ctx.Step(`^the check fails as not private naming "([^"]*)"$`, s.checkFailsNaming)

	ctx.Step(`^a fake secret source returning "([^"]*)"$`, s.fakeSource)
	ctx.Step(`^configuration naming the reference "([^"]*)"$`, s.configNaming)
	ctx.Step(`^the reference is resolved for a use that fails echoing the value$`, s.failingUse)
	ctx.Step(`^the error and log lines of the failed use are captured$`, s.captureErrorAndLogs)
	ctx.Step(`^the value was used exactly once$`, s.usedOnce)
	ctx.Step(`^the source was read exactly once$`, s.readOnce)
	ctx.Step(`^"([^"]*)" appears in no captured string$`, s.appearsNowhere)
	ctx.Step(`^the captured error names the reference "([^"]*)"$`, s.errorNamesRef)
	ctx.Step(`^the captured logs show the redaction marker$`, s.logsRedacted)

	ctx.Step(`^in-memory launch facts with cwd "([^"]*)" and OS "([^"]*)"$`, s.launchFacts)
	ctx.Step(`^"([^"]*)" is evaluated$`, s.evaluated)
	ctx.Step(`^it yields "([^"]*)"$`, s.yields)
	ctx.Step(`^"([^"]*)" yields "([^"]*)"$`, s.exprYields)
}

func TestE2E_boot_launch_egress_secrets_and_test_kit_launch_facts_home_layout_and_secrets(t *testing.T) {
	lfhsOuterT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_boot_launch_egress_secrets_and_test_kit_launch_facts_home_layout_and_secrets,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"boot_launch_egress_secrets_and_test_kit_launch_facts_home_layout_and_secrets.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
