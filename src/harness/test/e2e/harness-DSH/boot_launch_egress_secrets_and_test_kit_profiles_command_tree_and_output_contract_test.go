//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

const pctBundleLabel = profile.BundleLabel + "test"

type pctState struct {
	// stack
	layers []loader.Layer
	tree   []loader.EntryOptions
	index  *loader.Index

	// dispatch
	status         int
	stdout, stderr string

	// reference
	reference string
}

func (s *pctState) parse(label, file, data string) error {
	layer, issues, err := profile.ParseLayer(label, file, []byte(data))
	if err != nil {
		return fmt.Errorf("parse %s: %w", label, err)
	}
	if len(issues) > 0 {
		return fmt.Errorf("parse %s: issues %v", label, issues)
	}
	s.layers = append(s.layers, layer)
	return nil
}

// ---- Layer stack precedence ----

func (s *pctState) bundleLayer(model string) error {
	return s.parse(pctBundleLabel, "bundle.yml",
		"- insert:\n    - id: agent\n      name: test.agent\n      config:\n        model: "+model+"\n")
}

func (s *pctState) profileLayer(model string) error {
	return s.parse(profile.ProfileLabel, "profile.yml", "- id: agent\n  merge:\n    model: "+model+"\n")
}

func (s *pctState) emptyUserLayer() error {
	return s.parse(profile.UserLabel, "user.yml", "")
}

func (s *pctState) stackComposes() error {
	tree, index, issues := loader.Compose(s.layers)
	if len(issues) > 0 {
		return fmt.Errorf("compose issues: %v", issues)
	}
	s.tree, s.index = tree, index
	return nil
}

func (s *pctState) composedModel(want string) error {
	if len(s.tree) != 1 {
		return fmt.Errorf("tree has %d entries, want 1", len(s.tree))
	}
	var cfg struct{ Model string }
	if err := json.Unmarshal(s.tree[0].Config, &cfg); err != nil {
		return err
	}
	if cfg.Model != want {
		return fmt.Errorf("model = %q, want %q", cfg.Model, want)
	}
	return nil
}

func (s *pctState) ownerOfModelIsProfile() error {
	owner, _ := s.index.Owner("agent", "config.model")
	if owner != profile.ProfileLabel {
		return fmt.Errorf("owner of config.model = %q, want %q", owner, profile.ProfileLabel)
	}
	return nil
}

func (s *pctState) ownerOfEntryIsBundle() error {
	owner, _ := s.index.Owner("agent", "")
	if owner != pctBundleLabel {
		return fmt.Errorf("owner of entry = %q, want %q", owner, pctBundleLabel)
	}
	return nil
}

// ---- Usage error exits 2 ----

func (s *pctState) commandTree() error { return nil }

func (s *pctState) dispatch(line string) error {
	words := strings.Fields(line)
	if len(words) == 0 || words[0] != "dsh" {
		return fmt.Errorf("command line %q must start with dsh", line)
	}
	var out, errOut bytes.Buffer
	env := cli.Env{
		Request: launch.Request{Args: words,
			Build: launch.BuildIdentity{Version: "v0.0.0-e2e", Revision: "e2e", GoVersion: "go-e2e"}},
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut,
	}
	s.status = cli.Main(context.Background(), env)
	s.stdout, s.stderr = out.String(), errOut.String()
	return nil
}

func (s *pctState) exitCode(want int) error {
	if s.status != want {
		return fmt.Errorf("exit code = %d, want %d (stderr %q)", s.status, want, s.stderr)
	}
	if want == 2 && output.Usage != 2 {
		return fmt.Errorf("output.Usage = %d, want 2", output.Usage)
	}
	return nil
}

func (s *pctState) usageLinePlusHint(flag string) error {
	lines := strings.Split(strings.TrimSuffix(s.stderr, "\n"), "\n")
	if !strings.HasSuffix(s.stderr, "\n") || len(lines) != 2 {
		return fmt.Errorf("stderr = %q, want exactly two lines", s.stderr)
	}
	if !strings.HasPrefix(lines[0], "dsh: cli/usage: ") || !strings.Contains(lines[0], flag) {
		return fmt.Errorf("usage line = %q, want a cli/usage line naming %s", lines[0], flag)
	}
	if lines[1] != output.HelpHint {
		return fmt.Errorf("hint line = %q, want %q", lines[1], output.HelpHint)
	}
	return nil
}

func (s *pctState) stdoutEmpty() error {
	if s.stdout != "" {
		return fmt.Errorf("stdout = %q, want empty", s.stdout)
	}
	return nil
}

// ---- Generated CLI reference is current ----

func (s *pctState) referenceGenerated() error {
	s.reference = cli.Reference()
	if s.reference == "" {
		return fmt.Errorf("reference is empty")
	}
	return nil
}

func (s *pctState) equalsGolden(rel string) error {
	// The suite runs in <module>/test/e2e/harness-DSH.
	p := filepath.Join(append([]string{"..", "..", ".."}, strings.Split(rel, "/")...)...)
	data, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("read golden %s: %w", p, err)
	}
	want := strings.ReplaceAll(string(data), "\r\n", "\n")
	if want != s.reference {
		return fmt.Errorf("reference differs from %s:\n%s", rel, golden.Diff(want, s.reference))
	}
	return nil
}

func (s *pctState) catalogPrintsReference(line string, code int) error {
	if err := s.dispatch(line); err != nil {
		return err
	}
	if s.status != code {
		return fmt.Errorf("%s exit code = %d, want %d (stderr %q)", line, s.status, code, s.stderr)
	}
	if s.stdout != s.reference {
		return fmt.Errorf("%s output differs from the reference:\n%s", line, golden.Diff(s.reference, s.stdout))
	}
	return nil
}

func InitializeScenario_boot_launch_egress_secrets_and_test_kit_profiles_command_tree_and_output_contract(ctx *godog.ScenarioContext) {
	s := &pctState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = pctState{}
		return c, nil
	})

	ctx.Step(`^an in-memory bundle layer setting model "([^"]*)"$`, s.bundleLayer)
	ctx.Step(`^a profile layer setting model "([^"]*)"$`, s.profileLayer)
	ctx.Step(`^an empty user layer$`, s.emptyUserLayer)
	ctx.Step(`^the stack composes$`, s.stackComposes)
	ctx.Step(`^the composed model is "([^"]*)"$`, s.composedModel)
	ctx.Step(`^the ownership index names the profile layer as owner of the model$`, s.ownerOfModelIsProfile)
	ctx.Step(`^the ownership index names the bundle layer as owner of the entry$`, s.ownerOfEntryIsBundle)

	ctx.Step(`^the command tree$`, s.commandTree)
	ctx.Step(`^"([^"]*)" is dispatched in process with captured streams$`, s.dispatch)
	ctx.Step(`^the mapped exit code is (\d+)$`, s.exitCode)
	ctx.Step(`^stderr holds one usage line naming "([^"]*)" plus the help hint$`, s.usageLinePlusHint)
	ctx.Step(`^stdout is empty$`, s.stdoutEmpty)

	ctx.Step(`^the reference is generated$`, s.referenceGenerated)
	ctx.Step(`^it equals the committed golden "([^"]*)"$`, s.equalsGolden)
	ctx.Step(`^"([^"]*)" prints the same reference with exit code (\d+)$`, s.catalogPrintsReference)
}

func TestE2E_boot_launch_egress_secrets_and_test_kit_profiles_command_tree_and_output_contract(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "boot_launch_egress_secrets_and_test_kit_profiles_command_tree_and_output_contract",
		ScenarioInitializer: InitializeScenario_boot_launch_egress_secrets_and_test_kit_profiles_command_tree_and_output_contract,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"boot_launch_egress_secrets_and_test_kit_profiles_command_tree_and_output_contract.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
