//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
)

// csvBin is the dsh binary built once by the suite's test function before godog
// starts, so a build failure fails that test on its own goroutine.
var csvBin string

// csvTracePrefix begins the component trace line printed on stderr when
// cli.TraceEnv is set; "none" follows it when no component was constructed.
const csvTracePrefix = "dsh: trace: components: "

type csvState struct {
	tmp     string
	homeDir string
	before  map[string]string
	status  int
	stdout  string
	stderr  string
}

func (s *csvState) withUserLayer(text string) error {
	if csvBin == "" {
		return errors.New("the dsh build produced no binary")
	}
	var err error
	if s.tmp, err = os.MkdirTemp("", "dshcsv-"); err != nil {
		return err
	}
	s.homeDir = filepath.Join(s.tmp, "home")
	path := profile.UserLayerPath(s.homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return err
	}
	s.before, err = csvHomeFiles(s.homeDir)
	return err
}

func (s *csvState) userLayerSetsLevel(id, level string) error {
	return s.withUserLayer(fmt.Sprintf("- id: %s\n  merge:\n    level: %s\n", id, level))
}

func (s *csvState) userLayerDefaultPreset(preset string) error {
	return s.withUserLayer("- insert:\n    - id: permissions\n      name: permission.presets\n      config:\n        defaultPreset: " + preset + "\n")
}

// run executes the built binary in its own process with --home on the temp home
// and the component trace switch on, from a working directory outside the home.
func (s *csvState) run(line string) error {
	words := strings.Fields(line)
	if len(words) == 0 || words[0] != "dsh" {
		return fmt.Errorf("command line %q does not start with dsh", line)
	}
	args := append([]string{"--home", s.homeDir}, words[1:]...)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, csvBin, args...)
	cmd.Env = append(os.Environ(), cli.TraceEnv+"=1")
	dir, err := os.MkdirTemp(s.tmp, "cwd-")
	if err != nil {
		return err
	}
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return fmt.Errorf("%s %v: %w", csvBin, args, err)
	}
	s.status, s.stdout, s.stderr = cmd.ProcessState.ExitCode(), out.String(), errOut.String()
	return nil
}

func (s *csvState) streams() string {
	return fmt.Sprintf("status %d\nstdout:\n%s\nstderr:\n%s", s.status, s.stdout, s.stderr)
}

func (s *csvState) exitsOK() error {
	if s.status != output.OK {
		return fmt.Errorf("exited %d, want %d:\n%s", s.status, output.OK, s.streams())
	}
	return nil
}

func (s *csvState) prints(line string) error {
	if !strings.Contains(s.stdout, line+"\n") {
		return fmt.Errorf("stdout lacks the line %q:\n%s", line, s.streams())
	}
	return nil
}

func (s *csvState) printsValue(field, value, layer string) error {
	return s.prints(fmt.Sprintf("    %s = %s  # %s", field, value, layer))
}

func (s *csvState) printsEntry(entry, layer string) error {
	return s.prints(fmt.Sprintf("  %s  # %s", entry, layer))
}

func (s *csvState) traceEmpty() error {
	var found []string
	for _, line := range strings.Split(s.stderr, "\n") {
		if list, ok := strings.CutPrefix(strings.TrimSuffix(line, "\r"), csvTracePrefix); ok {
			found = append(found, list)
		}
	}
	if len(found) != 1 {
		return fmt.Errorf("stderr holds %d component trace lines, want one:\n%s", len(found), s.streams())
	}
	if found[0] != "none" {
		return fmt.Errorf("the preview constructed components %q, want none", found[0])
	}
	return nil
}

func (s *csvState) homeUnchanged() error {
	after, err := csvHomeFiles(s.homeDir)
	if err != nil {
		return err
	}
	if !maps.Equal(s.before, after) {
		return fmt.Errorf("the preview changed the home:\nbefore %v\nafter  %v", s.before, after)
	}
	return nil
}

func (s *csvState) exitsConfig(want int) error {
	if want != output.Config {
		return fmt.Errorf("the scenario names config status %d, the CLI's is %d", want, output.Config)
	}
	if s.status != output.Config {
		return fmt.Errorf("config validate exited %d, want %d:\n%s", s.status, output.Config, s.streams())
	}
	return nil
}

func (s *csvState) namesPreset(preset string) error {
	want := fmt.Sprintf("permissions.config.defaultPreset: default permission preset %q is not a preset of the table", preset)
	if !strings.Contains(s.stderr, want) {
		return fmt.Errorf("stderr does not name the preset with %q:\n%s", want, s.streams())
	}
	return nil
}

func (s *csvState) notValid() error {
	if strings.Contains(s.stdout, "valid:") {
		return fmt.Errorf("a rejected configuration was reported valid:\n%s", s.streams())
	}
	return nil
}

func (s *csvState) cleanup() error {
	if s.tmp == "" {
		return nil
	}
	return os.RemoveAll(s.tmp)
}

func csvHomeFiles(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			files[path] = "dir"
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files[path] = info.ModTime().String() + " " + strconv.FormatInt(info.Size(), 10)
		return nil
	})
	return files, err
}

func InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_config_show_and_validate_close_h_21(ctx *godog.ScenarioContext) {
	s := &csvState{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if cerr := s.cleanup(); cerr != nil && err == nil {
			return c, cerr
		}
		return c, err
	})
	ctx.Step(`^the built dsh binary and a temp home whose user layer sets "([^"]*)" level "([^"]*)"$`, s.userLayerSetsLevel)
	ctx.Step(`^the built dsh binary and a temp home whose user layer sets the default permission preset to "([^"]*)"$`, s.userLayerDefaultPreset)
	ctx.Step(`^"([^"]*)" runs with the component trace on$`, s.run)
	ctx.Step(`^it exits 0$`, s.exitsOK)
	ctx.Step(`^it prints "([^"]*)"$`, s.prints)
	ctx.Step(`^it prints the value "([^"]*)" as "((?:[^"\\]|\\.)*)" from the layer "([^"]*)"$`, func(field, value, layer string) error {
		return s.printsValue(field, strings.ReplaceAll(value, `\"`, `"`), layer)
	})
	ctx.Step(`^it prints the entry "([^"]*)" from the layer "([^"]*)"$`, s.printsEntry)
	ctx.Step(`^the component trace is empty$`, s.traceEmpty)
	ctx.Step(`^the temp home is unchanged$`, s.homeUnchanged)
	ctx.Step(`^it exits with the config status (\d+)$`, s.exitsConfig)
	ctx.Step(`^stderr names the unresolvable default permission preset "([^"]*)"$`, s.namesPreset)
	ctx.Step(`^stdout does not report the configuration valid$`, s.notValid)
}

func TestE2E_dh_s_16_profiles_launchers_and_evidence_runners_config_show_and_validate_close_h_21(t *testing.T) {
	// Build ./cmd/dsh of this checkout here, on t's own goroutine: launcher.Build
	// fails t directly, which must not happen from a godog scenario subtest.
	csvBin = launcher.Build(t, filepath.Join("..", "..", ".."), "./cmd/dsh")
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_config_show_and_validate_close_h_21,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_16_profiles_launchers_and_evidence_runners_config_show_and_validate_close_h_21.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
