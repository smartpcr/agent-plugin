//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/sandboxstatus"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/variants"
)

// The recorded "dsh sandbox status" reports of sandboxstatus/testdata, by the
// machine kind (the Preflight column text) each one is the output of.
var vfrFixtures = map[string]string{
	"bwrap Full":        "bwrap-full.txt",
	"landlock ABI >= 5": "landlock-partial.txt",
	"landlock ABI 1-4":  "landlock-abi1-4.txt",
	"Unconfined":        "unconfined.txt",
}

var (
	vfrBuildOnce sync.Once
	vfrGateBin   string
	vfrBuildErr  error
)

// vfrModuleRoot is tools/harness-gate, seen from tools/harness-gate/test/e2e/harness-DSH-M2.
func vfrModuleRoot() (string, error) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	if !bytes.Contains(data, []byte("module github.com/smartpcr/agent-plugin/tools/harness-gate")) {
		return "", fmt.Errorf("%s is not the harness-gate module", root)
	}
	return root, nil
}

func vfrRepoRoot() (string, error) {
	mod, err := vfrModuleRoot()
	if err != nil {
		return "", err
	}
	return filepath.Dir(filepath.Dir(mod)), nil
}

func vfrBuildGate() (string, error) {
	vfrBuildOnce.Do(func() {
		mod, err := vfrModuleRoot()
		if err != nil {
			vfrBuildErr = err
			return
		}
		dir, err := os.MkdirTemp("", "harness-gate-vfr-e2e-")
		if err != nil {
			vfrBuildErr = err
			return
		}
		name := "harness-gate"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		vfrGateBin = filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-o", vfrGateBin, ".")
		cmd.Dir = mod
		cmd.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool {
			return strings.HasPrefix(strings.ToUpper(kv), "GOWORK=")
		}), "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			vfrBuildErr = fmt.Errorf("go build harness-gate: %v\n%s", err, out)
		}
	})
	return vfrGateBin, vfrBuildErr
}

type vfrResult struct {
	code           int
	stdout, stderr string
}

func vfrExec(ctx context.Context, stdin []byte, args ...string) (vfrResult, error) {
	bin, err := vfrBuildGate()
	if err != nil {
		return vfrResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	r := vfrResult{stdout: out.String(), stderr: errb.String()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		r.code = exit.ExitCode()
	default:
		return r, err
	}
	return r, nil
}

type vfrState struct {
	root     string
	stage    string
	index    *acceptance.Index
	fleet    *variants.Fleet
	ids      []string
	problems []string

	reports  map[string]string // machine kind -> recorded report
	service  string
	read     string // the machine kind whose report is evaluated
	failures map[string]error
}

func (s *vfrState) givenFleetAndIndex(stage string) error {
	root, err := vfrRepoRoot()
	if err != nil {
		return err
	}
	if stage != variants.Stage {
		return fmt.Errorf("the variant fleet serves %s, not %s", variants.Stage, stage)
	}
	x, err := acceptance.ReadIndex(root)
	if err != nil {
		return err
	}
	f, err := variants.Read(root, closure.DefaultStory)
	if err != nil {
		return err
	}
	ids, err := x.Required(stage)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return fmt.Errorf("the %s entry of the index lists no scenario", stage)
	}
	if len(f.Variants) == 0 {
		return fmt.Errorf("%s lists no variant", f.Path)
	}
	s.root, s.stage, s.index, s.fleet, s.ids = root, stage, x, f, ids
	return nil
}

func (s *vfrState) whenCrossReferenced() error {
	s.problems = s.fleet.Check(s.index, s.stage)
	return nil
}

func (s *vfrState) thenNoProblem() error {
	if len(s.problems) > 0 {
		return fmt.Errorf("the variant list does not cover %s:\n  %s", s.stage, strings.Join(s.problems, "\n  "))
	}
	return nil
}

// thenEveryScenarioCovered recomputes the coverage from the parsed rows rather
// than trusting Check alone.
func (s *vfrState) thenEveryScenarioCovered() error {
	removed := map[string]bool{}
	for _, r := range s.fleet.Removals {
		removed[r.Scenario] = true
	}
	var missing []string
	for _, id := range s.ids {
		covered := removed[id]
		for _, v := range s.fleet.Variants {
			if v.InScope() && slices.Contains(v.Scenarios, id) {
				covered = true
			}
		}
		if !covered {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("no in-scope variant or owner removal for %s (M2-DOD-4)", strings.Join(missing, ", "))
	}
	return nil
}

func (s *vfrState) thenMacOSOutOfScope(scope string) error {
	n := 0
	for _, v := range s.fleet.Variants {
		if strings.Contains(strings.ToLower(v.Label+" "+v.Platform), "macos") {
			n++
			if v.Scope != scope {
				return fmt.Errorf("macOS row %s reads %q, want %q", v.Label, v.Scope, scope)
			}
		}
	}
	if n == 0 {
		return errors.New("no macOS row; M2-EN-2 lists them out of scope")
	}
	return nil
}

func (s *vfrState) thenCoverageGolden(name string) error {
	mod, err := vfrModuleRoot()
	if err != nil {
		return err
	}
	want, err := os.ReadFile(filepath.Join(mod, "variants", "testdata", name))
	if err != nil {
		return err
	}
	got := s.fleet.Coverage(s.index, s.stage)
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		return fmt.Errorf("coverage differs from variants/testdata/%s:\n%s", name, got)
	}
	return nil
}

func (s *vfrState) thenCommandPasses(ctx context.Context) error {
	r, err := vfrExec(ctx, nil, "variants", "--root", s.root)
	if err != nil {
		return err
	}
	if r.code != 0 || !strings.Contains(r.stdout, "harness-gate variants: passed: every "+s.stage+" scenario has an in-scope variant or an owner removal") {
		return fmt.Errorf("harness-gate variants exited %d:\n%s%s", r.code, r.stdout, r.stderr)
	}
	for _, id := range s.ids {
		if !strings.Contains(r.stdout, id+"\n  runs on: ") {
			return fmt.Errorf("harness-gate variants does not list %s:\n%s", id, r.stdout)
		}
	}
	return nil
}

// thenFailsWithout drops the named rows from the committed file and checks
// again: the scenario they alone run must be reported uncovered.
func (s *vfrState) thenFailsWithout(a, b, id, ref string) error {
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(variants.Path(closure.DefaultStory))))
	if err != nil {
		return err
	}
	var kept []string
	dropped := 0
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "| `"+a+"` |") || strings.HasPrefix(line, "| `"+b+"` |") {
			dropped++
			continue
		}
		kept = append(kept, line)
	}
	if dropped != 2 {
		return fmt.Errorf("dropped %d rows, want the 2 rows %s and %s", dropped, a, b)
	}
	f, err := variants.Parse("variants.md", strings.NewReader(strings.Join(kept, "\n")))
	if err != nil {
		return err
	}
	p := strings.Join(f.Check(s.index, s.stage), "\n")
	if !strings.Contains(p, id+" of "+s.stage+" has no in-scope variant") || !strings.Contains(p, ref) {
		return fmt.Errorf("without %s and %s the check reported %q, want %s uncovered (%s)", a, b, p, id, ref)
	}
	return nil
}

func (s *vfrState) givenRecordedOutputs(string) error {
	mod, err := vfrModuleRoot()
	if err != nil {
		return err
	}
	s.reports = map[string]string{}
	for kind, f := range vfrFixtures {
		b, err := os.ReadFile(filepath.Join(mod, "sandboxstatus", "testdata", f))
		if err != nil {
			return err
		}
		s.reports[kind] = string(b)
	}
	return nil
}

func (s *vfrState) whenPreflightEvaluates(service, kind string) error {
	if _, ok := s.reports[kind]; !ok {
		return fmt.Errorf("no recorded %q output", kind)
	}
	es := sandboxstatus.ExpectationsOf(service)
	if len(es) == 0 {
		return fmt.Errorf("%s has no sandbox preflight", service)
	}
	s.service, s.read, s.failures = service, kind, map[string]error{}
	for _, e := range es {
		_, err := sandboxstatus.Preflight(service, e.Text, s.reports[kind])
		s.failures[e.Text] = err
	}
	return nil
}

func (s *vfrState) thenFailsForEveryKind(service string) error {
	if service != s.service {
		return fmt.Errorf("evaluated %s, not %s", s.service, service)
	}
	for kind, err := range s.failures {
		if err == nil {
			return fmt.Errorf("%s preflight %q passed a %q output", service, kind, s.read)
		}
	}
	return nil
}

func (s *vfrState) thenFailureNames(service, want string) error {
	for kind, err := range s.failures {
		if err == nil || !strings.Contains(err.Error(), service) || !strings.Contains(err.Error(), want) {
			return fmt.Errorf("%s preflight %q: %v; want a failure naming %q and %q", service, kind, err, service, want)
		}
	}
	return nil
}

func (s *vfrState) thenCommandExitsOne(ctx context.Context) error {
	for kind := range s.failures {
		r, err := vfrExec(ctx, []byte(s.reports[s.read]), "sandbox-preflight", "--service", s.service, "--preflight", kind)
		if err != nil {
			return err
		}
		if r.code != 1 || !strings.HasPrefix(r.stdout, "harness-gate sandbox-preflight: FAILED: ") || !strings.Contains(r.stdout, s.service) {
			return fmt.Errorf("sandbox-preflight --service %s --preflight %q on %q exited %d with %q, want 1 naming the service", s.service, kind, s.read, r.code, r.stdout+r.stderr)
		}
	}
	// A landlock check naming no machine kind fails too: it is never a pass.
	r, err := vfrExec(ctx, []byte(s.reports[s.read]), "sandbox-preflight", "--service", s.service)
	if err != nil {
		return err
	}
	if len(sandboxstatus.ExpectationsOf(s.service)) > 1 && r.code != 1 {
		return fmt.Errorf("sandbox-preflight --service %s without --preflight exited %d with %q, want 1", s.service, r.code, r.stdout+r.stderr)
	}
	return nil
}

func (s *vfrState) thenPassesOnlyOwn(ctx context.Context, service string) error {
	for _, e := range sandboxstatus.ExpectationsOf(service) {
		for kind, report := range s.reports {
			_, err := sandboxstatus.Preflight(service, e.Text, report)
			if own := kind == e.Text; own != (err == nil) {
				return fmt.Errorf("%s preflight %q on the %q output: err %v, want pass only on its own machine", service, e.Text, kind, err)
			}
		}
		r, err := vfrExec(ctx, []byte(s.reports[e.Text]), "sandbox-preflight", "--service", service, "--preflight", e.Text)
		if err != nil {
			return err
		}
		if r.code != 0 || !strings.HasPrefix(r.stdout, "harness-gate sandbox-preflight: passed: backend landlock") {
			return fmt.Errorf("sandbox-preflight --preflight %q on its own machine exited %d with %q", e.Text, r.code, r.stdout+r.stderr)
		}
	}
	return nil
}

func InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_e2_variant_fleet_and_runbooks(ctx *godog.ScenarioContext) {
	s := &vfrState{}
	ctx.Step(`^the committed variants file and the "([^"]*)" entry of the acceptance index$`, s.givenFleetAndIndex)
	ctx.Step(`^the index check cross-references them$`, s.whenCrossReferenced)
	ctx.Step(`^the check reports no problem$`, s.thenNoProblem)
	ctx.Step(`^every S18 scenario has at least one in-scope variant or an owner decision removing it$`, s.thenEveryScenarioCovered)
	ctx.Step(`^every macOS row reads "([^"]*)"$`, s.thenMacOSOutOfScope)
	ctx.Step(`^the coverage equals the committed golden "([^"]*)"$`, s.thenCoverageGolden)
	ctx.Step(`^the harness-gate variants command exits 0 and lists every S18 scenario$`, s.thenCommandPasses)
	ctx.Step(`^without the "([^"]*)" and "([^"]*)" rows the check fails naming "([^"]*)" and "([^"]*)"$`, s.thenFailsWithout)
	ctx.Step(`^the recorded "([^"]*)" outputs$`, s.givenRecordedOutputs)
	ctx.Step(`^the "([^"]*)" preflight evaluates the "([^"]*)" output$`, s.whenPreflightEvaluates)
	ctx.Step(`^it fails for every "([^"]*)" machine kind$`, s.thenFailsForEveryKind)
	ctx.Step(`^each failure names "([^"]*)" and "([^"]*)"$`, s.thenFailureNames)
	ctx.Step(`^the harness-gate sandbox-preflight command exits 1 for it for every machine kind$`, s.thenCommandExitsOne)
	ctx.Step(`^the "([^"]*)" preflight passes only the output of its own machine kind$`, s.thenPassesOnlyOwn)
}

func TestE2E_dh_s_18_acceptance_on_real_operating_systems_e2_variant_fleet_and_runbooks(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_e2_variant_fleet_and_runbooks,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_18_acceptance_on_real_operating_systems_e2_variant_fleet_and_runbooks.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the DH-S-18 E2 variant fleet and runbooks e2e scenarios failed")
	}
}
