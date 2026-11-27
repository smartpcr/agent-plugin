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
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret/file"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
)

// atsBin is the dsh binary built once by the suite's test function before
// godog starts, so a build failure fails that test on its own goroutine.
var atsBin string

// atsReferenceGolden is the committed golden of TC-13g, relative to this package.
var atsReferenceGolden = filepath.Join("..", "..", "..", "internal", "cli", "testdata", "reference.golden")

// atsTracePrefix begins the component trace line an admin command prints on
// standard error when cli.TraceEnv is set.
const atsTracePrefix = "dsh: trace: components: "

type atsState struct {
	bin     string
	tmp     string
	homeDir string
	status  int
	stdout  string
	stderr  string
}

// atsBuild returns the dsh binary the suite built before running scenarios.
func atsBuild() (string, error) {
	if atsBin == "" {
		return "", errors.New("the dsh build produced no binary")
	}
	return atsBin, nil
}

func (s *atsState) builtWithTempHome() error {
	bin, err := atsBuild()
	if err != nil {
		return err
	}
	s.bin = bin
	if s.tmp == "" {
		if s.tmp, err = os.MkdirTemp("", "dsha-"); err != nil {
			return err
		}
	}
	// Each Given gets its own home, so a later Given starts from an empty store.
	s.homeDir = filepath.Join(s.tmp, fmt.Sprintf("home-%d", time.Now().UnixNano()))
	if _, err := os.Stat(s.homeDir); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("temp home %s already exists (%v)", s.homeDir, err)
	}
	return nil
}

func (s *atsState) builtTree() error {
	bin, err := atsBuild()
	if err != nil {
		return err
	}
	s.bin = bin
	return nil
}

// run executes the built binary in its own process with --home on the temp
// home (when one is prepared), stdin and the component trace switch on.
func (s *atsState) run(line, stdin string) error {
	words := strings.Fields(line)
	if len(words) == 0 || words[0] != "dsh" {
		return fmt.Errorf("command line %q does not start with dsh", line)
	}
	var args []string
	if s.homeDir != "" {
		args = append(args, "--home", s.homeDir)
	}
	args = append(args, words[1:]...)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.bin, args...)
	cmd.Env = append(os.Environ(), cli.TraceEnv+"=1")
	dir, err := os.MkdirTemp(s.tmpDir(), "cwd-")
	if err != nil {
		return err
	}
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return fmt.Errorf("%s %v: %w", s.bin, args, err)
	}
	s.status, s.stdout, s.stderr = cmd.ProcessState.ExitCode(), out.String(), errOut.String()
	return nil
}

func (s *atsState) tmpDir() string {
	if s.tmp == "" {
		s.tmp, _ = os.MkdirTemp("", "dsha-")
	}
	return s.tmp
}

func (s *atsState) runsWithTrace(line string) error { return s.run(line, "") }

func (s *atsState) runsWithStdin(line, value string) error { return s.run(line, value+"\n") }

func (s *atsState) runsWithNoStdin(line string) error { return s.run(line, "") }

func (s *atsState) streams() string {
	return fmt.Sprintf("status %d\nstdout:\n%s\nstderr:\n%s", s.status, s.stdout, s.stderr)
}

// ---- Admin tree builds no agent (TC-13b) ----

func (s *atsState) statusReport() error {
	if s.status != output.OK {
		return fmt.Errorf("sandbox status exited %d, want %d:\n%s", s.status, output.OK, s.streams())
	}
	if !strings.HasPrefix(s.stdout, "state:   ") || !strings.Contains(s.stdout, "\nbackend: ") || !strings.Contains(s.stdout, "\nbackends:\n") {
		return fmt.Errorf("sandbox status printed no C37 report:\n%s", s.streams())
	}
	return nil
}

func (s *atsState) trace() (string, error) {
	var found []string
	for _, line := range strings.Split(s.stderr, "\n") {
		if list, ok := strings.CutPrefix(strings.TrimSuffix(line, "\r"), atsTracePrefix); ok {
			found = append(found, list)
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf("stderr holds %d component trace lines, want one:\n%s", len(found), s.streams())
	}
	return found[0], nil
}

func (s *atsState) traceActsOnNothing() error {
	list, err := s.trace()
	if err != nil {
		return err
	}
	if list == "" {
		return errors.New("the component trace is empty")
	}
	for _, c := range strings.Split(list, ",") {
		if profile.Acting(c, c) || strings.HasPrefix(c, "llm.") || strings.HasPrefix(c, "agent.") || strings.HasPrefix(c, "carrier.") {
			return fmt.Errorf("the admin tree built %s (trace %q): an admin command builds no agent, model client or carrier", c, list)
		}
	}
	return nil
}

func (s *atsState) traceIs(want string) error {
	list, err := s.trace()
	if err != nil {
		return err
	}
	if list != want {
		return fmt.Errorf("component trace = %q, want %q", list, want)
	}
	return nil
}

func (s *atsState) homeNotCreated() error {
	if _, err := os.Stat(s.homeDir); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("the read-only admin view created the home %s (%v)", s.homeDir, err)
	}
	return nil
}

// ---- Secret only from stdin (TC-13b) ----

func (s *atsState) exitsOKReporting(line string) error {
	if s.status != output.OK || s.stdout != line+"\n" {
		return fmt.Errorf("secret set: want status %d and %q:\n%s", output.OK, line, s.streams())
	}
	return nil
}

func (s *atsState) store() *file.Store {
	return file.New(home.New(s.homeDir).Secrets(), file.Options{})
}

func (s *atsState) storeHolds(ref, value string) error {
	r, err := secret.Parse(ref)
	if err != nil {
		return err
	}
	v, ok, err := s.store().Lookup(context.Background(), r)
	if err != nil {
		return fmt.Errorf("lookup %s: %w", ref, err)
	}
	if !ok {
		return fmt.Errorf("the store of %s does not hold %s", s.homeDir, ref)
	}
	if v.Reveal() != value {
		return fmt.Errorf("the store holds %s with a value of %d bytes, want the %d bytes read from stdin", ref, len(v.Reveal()), len(value))
	}
	return nil
}

func (s *atsState) storeLacks(ref string) error {
	r, err := secret.Parse(ref)
	if err != nil {
		return err
	}
	st, err := s.store().Stat(context.Background(), r)
	if err != nil {
		return fmt.Errorf("stat %s: %w", ref, err)
	}
	if st.Present {
		return fmt.Errorf("a refused command stored %s", ref)
	}
	return nil
}

func (s *atsState) exitsUsage(want int) error {
	if want != output.Usage {
		return fmt.Errorf("the scenario names usage status %d, the CLI's is %d", want, output.Usage)
	}
	if s.status != output.Usage {
		return fmt.Errorf("secret set with the value in argv exited %d, want the usage status %d:\n%s", s.status, output.Usage, s.streams())
	}
	return nil
}

func (s *atsState) noStreamCarries(value string) error {
	if strings.Contains(s.stdout, value) || strings.Contains(s.stderr, value) {
		return fmt.Errorf("the refused value %q reached a stream:\n%s", value, s.streams())
	}
	return nil
}

// ---- Command reference (TC-13g) ----

func (s *atsState) renderedBy(line string) error {
	if err := s.run(line, ""); err != nil {
		return err
	}
	if s.status != output.OK {
		return fmt.Errorf("%s exited %d:\n%s", line, s.status, s.streams())
	}
	return nil
}

func (s *atsState) equalsReferenceGolden() error {
	want, err := os.ReadFile(atsReferenceGolden)
	if err != nil {
		return fmt.Errorf("read committed golden: %w", err)
	}
	if !bytes.Equal(want, []byte(s.stdout)) {
		return fmt.Errorf("the rendered reference differs from %s:\n%s", atsReferenceGolden, golden.Diff(string(want), s.stdout))
	}
	return nil
}

func (s *atsState) equalsInProcess() error {
	if ref := cli.Reference(); ref != s.stdout {
		return fmt.Errorf("the built binary's reference differs from cli.Reference():\n%s", golden.Diff(ref, s.stdout))
	}
	return nil
}

func (s *atsState) cleanup() error {
	if s.tmp == "" {
		return nil
	}
	return os.RemoveAll(s.tmp)
}

func InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_admin_tree_and_secret_set(ctx *godog.ScenarioContext) {
	s := &atsState{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if cerr := s.cleanup(); cerr != nil && err == nil {
			return c, cerr
		}
		return c, err
	})
	ctx.Step(`^the built dsh binary and a temp home that does not exist yet$`, s.builtWithTempHome)
	ctx.Step(`^"([^"]*)" runs with the component trace on$`, s.runsWithTrace)
	ctx.Step(`^it exits 0 with the sandbox status report$`, s.statusReport)
	ctx.Step(`^its component trace lists no agent, model client or carrier$`, s.traceActsOnNothing)
	ctx.Step(`^its component trace is exactly the read-only admin view "([^"]*)"$`, s.traceIs)
	ctx.Step(`^the temp home was not created$`, s.homeNotCreated)

	ctx.Step(`^"([^"]*)" runs with "([^"]*)" on stdin$`, s.runsWithStdin)
	ctx.Step(`^"([^"]*)" runs with nothing on stdin$`, s.runsWithNoStdin)
	ctx.Step(`^it exits 0 and reports "([^"]*)"$`, s.exitsOKReporting)
	ctx.Step(`^the secret store of the temp home holds "([^"]*)" with the value "([^"]*)"$`, s.storeHolds)
	ctx.Step(`^it exits with the usage status (\d+)$`, s.exitsUsage)
	ctx.Step(`^no stream carries "([^"]*)"$`, s.noStreamCarries)
	ctx.Step(`^the secret store of the temp home does not hold "([^"]*)"$`, s.storeLacks)

	ctx.Step(`^the command tree of the built dsh binary$`, s.builtTree)
	ctx.Step(`^the reference is rendered by "([^"]*)"$`, s.renderedBy)
	ctx.Step(`^it equals the committed cli/testdata/reference\.golden byte for byte$`, s.equalsReferenceGolden)
	ctx.Step(`^it equals the in-process rendering of the command tree$`, s.equalsInProcess)
}

func TestE2E_dh_s_16_profiles_launchers_and_evidence_runners_admin_tree_and_secret_set(t *testing.T) {
	// Build ./cmd/dsh of this checkout here, on t's own goroutine: launcher.Build
	// fails t directly, which must not happen from a godog scenario subtest.
	atsBin = launcher.Build(t, filepath.Join("..", "..", ".."), "./cmd/dsh")
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_admin_tree_and_secret_set,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_16_profiles_launchers_and_evidence_runners_admin_tree_and_secret_set.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
