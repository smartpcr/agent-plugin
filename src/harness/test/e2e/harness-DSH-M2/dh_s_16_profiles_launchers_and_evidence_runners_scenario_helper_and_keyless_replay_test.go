//go:build e2e

package e2e

// Step definitions of
// dh_s_16_profiles_launchers_and_evidence_runners_scenario_helper_and_keyless_replay.feature
// (DH-S-16 stage "Scenario Helper and Keyless Replay"; TC-17a, TC-16g, TC-16b).
//
//   - Missing input fails, not skips: this test binary is re-executed as a real
//     scenario that calls scenario.Run with Needs.Bin, DSH_BIN blank and its ID on
//     DSH_REQUIRE_SCENARIOS; its exit, output and report row are asserted.
//   - Internal import forbidden: the gate's scenarioimport analyzer runs under
//     analysistest over the committed tools/harness-gate/analyzers testdata, in a
//     scratch module that replaces the gate module with this checkout's.
//   - Replay oracle: dsh built with the testhook tag replays the committed
//     test/replay recording keylessly into a scratch home; the log exported from
//     the run's SQLite store, the workspace and the model requests are compared to
//     the committed oracle, byte for byte.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/fixture"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

var shkOuterT *testing.T

// shkBin is dsh built once with the testhook tag (the llm.replay route).
var shkBin string

const (
	// shkChildEnv makes TestE2E_shkRequiredScenarioChild run its scenario; the
	// value is the scenario ID and the marker path, tab-separated.
	shkChildEnv  = "DSH_E2E_SHK_CHILD"
	shkChildTest = "TestE2E_shkRequiredScenarioChild"
	shkWithin    = 2 * time.Minute
	shkMaxOutput = 4 << 20
)

type shkChild struct {
	out  string
	code int
	rows []shkRow
}

type shkWorld struct {
	// Missing input
	id       string
	needsBin bool
	binUnset bool
	required bool
	marker   string
	run      shkChild
	contrast shkChild

	// Internal import
	fixtureDir string
	fixturePkg string
	diags      []string

	// Replay oracle
	recording string
	log       []byte
	requests  map[string][]byte
	workspace string
}

func shkModuleDir() (string, error) {
	dir, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd", "dsh")); err != nil {
		return "", fmt.Errorf("%s is not the harness module: %v", dir, err)
	}
	return dir, nil
}

// ---- Scenario: Missing input fails, not skips ----

func (w *shkWorld) givenScenarioRequiringBin(id string) error {
	w.id, w.needsBin = id, true
	return nil
}

func (w *shkWorld) givenBinUnset() error {
	w.binUnset = true
	return nil
}

func (w *shkWorld) givenRequired(id string) error {
	if id != w.id {
		return fmt.Errorf("listed %q, the scenario is %q", id, w.id)
	}
	w.required = true
	return nil
}

// runChild re-executes this test binary as the scenario with DSH_BIN blank and,
// when required, its ID on DSH_REQUIRE_SCENARIOS.
func (w *shkWorld) runChild(required bool) (shkChild, error) {
	var c shkChild
	if !w.needsBin || !w.binUnset {
		return c, errors.New("the scenario must need DSH_BIN and DSH_BIN must be unset")
	}
	exe, err := os.Executable()
	if err != nil {
		return c, err
	}
	dir := shkOuterT.TempDir()
	report := filepath.Join(dir, "report.jsonl")
	w.marker = filepath.Join(dir, "body-ran")
	req := ""
	if required {
		req = "DH-E-OTHER, " + w.id
	}
	ctx, cancel := context.WithTimeout(context.Background(), shkWithin)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^"+shkChildTest+"$", "-test.v", "-test.count=1")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		shkChildEnv+"="+w.id+"\t"+w.marker,
		shkBinEnv+"=",
		shkRequireEnv+"="+req,
		shkReportEnv+"="+report,
	)
	out, err := cmd.CombinedOutput()
	c.out = string(out)
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		c.code = ee.ExitCode()
	default:
		return c, fmt.Errorf("running the scenario: %v\n%s", err, out)
	}
	if ctx.Err() != nil {
		return c, fmt.Errorf("the scenario did not end within %s\n%s", shkWithin, out)
	}
	f, err := os.Open(report)
	if err != nil {
		return c, fmt.Errorf("the scenario wrote no report: %v\n%s", err, out)
	}
	defer f.Close()
	if c.rows, err = shkReadReport(f); err != nil {
		return c, err
	}
	return c, nil
}

func (w *shkWorld) whenRuns() error {
	if !w.required {
		return errors.New("the scenario is not listed in DSH_REQUIRE_SCENARIOS")
	}
	var err error
	w.run, err = w.runChild(true)
	return err
}

func (w *shkWorld) thenFailsWith(msg string) error {
	if w.run.code == 0 {
		return fmt.Errorf("the scenario run exited 0, want a failure:\n%s", w.run.out)
	}
	if !strings.Contains(w.run.out, w.id+": "+msg) {
		return fmt.Errorf("the output does not say %q for %s:\n%s", msg, w.id, w.run.out)
	}
	if !strings.Contains(w.run.out, "--- FAIL: "+shkChildTest) || strings.Contains(w.run.out, "--- SKIP: "+shkChildTest) {
		return fmt.Errorf("the scenario test did not fail (or skipped):\n%s", w.run.out)
	}
	return nil
}

func (w *shkWorld) thenBodyNeverRan() error {
	if _, err := os.Stat(w.marker); err == nil {
		return errors.New("the body ran although its need was unmet")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func shkOneRow(c shkChild, id string) (shkRow, error) {
	var got []shkRow
	for _, r := range c.rows {
		if r.ID == id {
			got = append(got, r)
		}
	}
	if len(got) != 1 {
		return shkRow{}, fmt.Errorf("%d report rows for %s, want 1: %+v\n%s", len(got), id, c.rows, c.out)
	}
	return got[0], nil
}

func (w *shkWorld) thenRowFailed(result, name string) error {
	r, err := shkOneRow(w.run, w.id)
	if err != nil {
		return err
	}
	if string(r.Result) != result || !strings.Contains(r.Reason, name) {
		return fmt.Errorf("row %+v, want result %q with a reason naming %s", r, result, name)
	}
	if err := shkCheck(w.run.rows, []string{w.id}); err == nil || !strings.Contains(err.Error(), shkRequiredNotRun) {
		return fmt.Errorf("shkCheck accepted the failed required row: %v", err)
	}
	return nil
}

func (w *shkWorld) thenUnlistedSkipped(result string) error {
	c, err := w.runChild(false)
	if err != nil {
		return err
	}
	w.contrast = c
	if c.code != 0 {
		return fmt.Errorf("the unlisted scenario run exited %d, want 0:\n%s", c.code, c.out)
	}
	r, err := shkOneRow(c, w.id)
	if err != nil {
		return err
	}
	if string(r.Result) != result || !strings.Contains(r.Reason, shkBinEnv) {
		return fmt.Errorf("unlisted row %+v, want %q naming %s", r, result, shkBinEnv)
	}
	if strings.Contains(c.out, "required scenario skipped") {
		return fmt.Errorf("an unlisted scenario was treated as required:\n%s", c.out)
	}
	return nil
}

// ---- Scenario: Internal import forbidden ----

const shkProbeTest = `package probe

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/analyzers"
)

func TestProbe(t *testing.T) {
	for _, r := range analysistest.Run(t, os.Getenv("SHK_TESTDATA"), analyzers.ScenarioImport, os.Getenv("SHK_PKG")) {
		for _, d := range r.Diagnostics {
			fmt.Printf("SHK-DIAG\t%s\t%s\n", r.Pass.Fset.Position(d.Pos), d.Message)
		}
	}
}
`

func (w *shkWorld) givenAnalyzerFixture(pkg, imp string) error {
	mod, err := shkModuleDir()
	if err != nil {
		return err
	}
	w.fixtureDir = filepath.Join(mod, "..", "..", "tools", "harness-gate", "analyzers", "testdata")
	w.fixturePkg = pkg
	files, err := filepath.Glob(filepath.Join(w.fixtureDir, "src", pkg, "*_test.go"))
	if err != nil || len(files) == 0 {
		return fmt.Errorf("no committed test file in testdata/src/%s (%v)", pkg, err)
	}
	found := false
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if bytes.Contains(b, []byte(strconv.Quote(imp))) && bytes.Contains(b, []byte(`"internal/testkit/scenario"`)) {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("no committed scenario file in testdata/src/%s imports %q", pkg, imp)
	}
	if _, err := os.Stat(filepath.Join(w.fixtureDir, "src", filepath.FromSlash(imp))); err != nil {
		return fmt.Errorf("the fixture package %s is missing: %v", imp, err)
	}
	return nil
}

func (w *shkWorld) whenAnalyzerRuns() error {
	gate, err := filepath.Abs(filepath.Join(w.fixtureDir, "..", ".."))
	if err != nil {
		return err
	}
	tmp := shkOuterT.TempDir()
	gomod := fmt.Sprintf("module dsh.e2e/shkprobe\n\ngo 1.27.4\n\nrequire github.com/smartpcr/agent-plugin/tools/harness-gate v0.0.0\n\nreplace github.com/smartpcr/agent-plugin/tools/harness-gate => %s\n",
		strconv.Quote(filepath.ToSlash(gate)))
	sum, err := os.ReadFile(filepath.Join(gate, "go.sum"))
	if err != nil {
		return err
	}
	for name, b := range map[string][]byte{"go.mod": []byte(gomod), "go.sum": sum, "probe_test.go": []byte(shkProbeTest)} {
		if err := os.WriteFile(filepath.Join(tmp, name), b, 0o644); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-run", "^TestProbe$", ".")
	cmd.Dir = tmp
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "SHK_TESTDATA="+w.fixtureDir, "SHK_PKG="+w.fixturePkg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("analysistest over the committed fixture: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "SHK-DIAG\t"); ok {
			w.diags = append(w.diags, rest)
		}
	}
	return nil
}

func (w *shkWorld) thenDiagnostics(n int) error {
	if len(w.diags) != n {
		return fmt.Errorf("%d diagnostics, want %d: %q", len(w.diags), n, w.diags)
	}
	return nil
}

func (w *shkWorld) thenDiagnosticNames(msg string) error {
	for _, d := range w.diags {
		if strings.Contains(d, msg) && strings.Contains(d, "x_test.go") {
			return nil
		}
	}
	return fmt.Errorf("no diagnostic in x_test.go says %q: %q", msg, w.diags)
}

// ---- Scenario: Replay oracle ----

func (w *shkWorld) givenRecording(name, under string) error {
	mod, err := shkModuleDir()
	if err != nil {
		return err
	}
	w.recording = filepath.Join(mod, filepath.FromSlash(under), "testdata", name)
	for _, f := range []string{"scenario.json", "session.jsonl", "log.expected.jsonl", "requests.expected", "workspace", "workspace.expected"} {
		if _, err := os.Stat(filepath.Join(w.recording, f)); err != nil {
			return fmt.Errorf("the committed recording lacks %s: %v", f, err)
		}
	}
	return nil
}

func (w *shkWorld) givenBuilt() error {
	if shkBin == "" {
		mod, err := shkModuleDir()
		if err != nil {
			return err
		}
		shkBin = launcher.Build(shkOuterT, mod, "./cmd/dsh", "testhook")
	}
	return nil
}

var shkProviderCredentials = []string{
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
	"OPENAI_API_KEY", "GH_TOKEN", "GITHUB_TOKEN", "COPILOT_GITHUB_TOKEN",
}

// shkKeylessEnv blanks every provider credential and every credential-shaped host
// variable in the child, so the replay runs without a key.
func shkKeylessEnv(host []string) []string {
	kept := subprocess.BuildEnv(host, subprocess.EnvOptions{})
	out := make([]string, 0, len(shkProviderCredentials))
	for _, name := range shkProviderCredentials {
		out = append(out, name+"=")
	}
	for _, kv := range host {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" || strings.HasPrefix(strings.ToUpper(name), subprocess.HarnessPrefix) || kept.Has(name) {
			continue
		}
		out = append(out, name+"=")
	}
	return out
}

func (w *shkWorld) whenReplays() error {
	t := shkOuterT
	m, err := fixture.ReadManifest(w.recording)
	if err != nil {
		return err
	}
	root := temphome.New(t).Root()
	ws, reqs := t.TempDir(), t.TempDir()
	if err := fixture.CopyTree(filepath.Join(w.recording, "workspace"), ws); err != nil {
		return err
	}
	temphome.Layer(t, root, profile.Headless, "- insert:\n    - id: replay\n      name: llm.replay\n      config:\n"+
		"        scenario: "+strconv.Quote(w.recording)+"\n        mode: replay\n        route: "+strconv.Quote(m.Route)+"\n"+
		"        model: "+strconv.Quote(m.Model)+"\n        requests: "+strconv.Quote(reqs)+"\n")

	h, err := launcher.Run(t, shkWithin, launcher.Program{
		Argv: []string{shkBin, "--home", root, "headless", "--json", m.Input},
		Dir:  ws, Env: shkKeylessEnv(os.Environ()), MaxOutput: shkMaxOutput,
	})
	out, errOut := h.Stdout.String(), h.Stderr.String()
	switch {
	case err != nil:
		return fmt.Errorf("replay: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	case h.Process.ExitCode() != 0:
		return fmt.Errorf("replay exited %d\nstdout:\n%s\nstderr:\n%s", h.Process.ExitCode(), out, errOut)
	case h.Stdout.Dropped() > 0 || h.Stderr.Dropped() > 0:
		return errors.New("the replay wrote past its output cap")
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var res struct{ Type, Session string }
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil || res.Type != "run/result" || res.Session == "" {
		return fmt.Errorf("the last stdout line is not a run/result (%v):\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	st, err := sqlite.Open(ctx, home.New(root).DB())
	if err != nil {
		return fmt.Errorf("opening the run's SQLite store: %v", err)
	}
	var log bytes.Buffer
	err = st.Export(ctx, &log, res.Session, sqlite.ExportOptions{})
	if cerr := st.Close(ctx); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("exporting session %s: %v", res.Session, err)
	}
	if log.Len() == 0 {
		return errors.New("the SQLite store holds no log for the run")
	}

	n := &fixture.Normalizer{Paths: map[string]string{root: "home", ws: "workspace", w.recording: "scenario", reqs: "requests"}}
	w.log = n.Normalize(log.Bytes())
	w.workspace = ws
	files, err := fixture.Tree(reqs)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for k := range files {
		names = append(names, k)
	}
	slices.Sort(names)
	w.requests = map[string][]byte{}
	for _, name := range names {
		w.requests[name] = n.Normalize(files[name])
	}
	return nil
}

func (w *shkWorld) thenLogIdentical(name string) error {
	want, err := os.ReadFile(filepath.Join(w.recording, name))
	if err != nil {
		return err
	}
	if d := fixture.Diff(name, w.log, want); d != "" {
		return errors.New(d)
	}
	return nil
}

func (w *shkWorld) thenWorkspaceIdentical(name string) error {
	return fixture.CompareTrees(w.workspace, filepath.Join(w.recording, name))
}

func (w *shkWorld) thenRequestsIdentical(name string) error {
	want, err := fixture.Tree(filepath.Join(w.recording, name))
	if err != nil {
		return err
	}
	if len(want) == 0 {
		return fmt.Errorf("%s is empty", name)
	}
	var got, exp []string
	for k := range w.requests {
		got = append(got, k)
	}
	for k := range want {
		exp = append(exp, k)
	}
	slices.Sort(got)
	slices.Sort(exp)
	if !slices.Equal(got, exp) {
		return fmt.Errorf("requests: got %v, want %v", got, exp)
	}
	for _, k := range exp {
		if d := fixture.Diff(name+"/"+k, w.requests[k], want[k]); d != "" {
			return errors.New(d)
		}
	}
	return nil
}

func InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_scenario_helper_and_keyless_replay(ctx *godog.ScenarioContext) {
	w := &shkWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = shkWorld{}
		return c, nil
	})

	ctx.Step(`^a scenario "([^"]*)" requiring DSH_BIN$`, w.givenScenarioRequiringBin)
	ctx.Step(`^DSH_BIN is unset$`, w.givenBinUnset)
	ctx.Step(`^"([^"]*)" is listed in DSH_REQUIRE_SCENARIOS$`, w.givenRequired)
	ctx.Step(`^the scenario runs$`, w.whenRuns)
	ctx.Step(`^it fails with "([^"]*)"$`, w.thenFailsWith)
	ctx.Step(`^its body never ran$`, w.thenBodyNeverRan)
	ctx.Step(`^its report row is "([^"]*)" with reason naming "([^"]*)"$`, w.thenRowFailed)
	ctx.Step(`^the same scenario not listed in DSH_REQUIRE_SCENARIOS is reported "([^"]*)"$`, w.thenUnlistedSkipped)

	ctx.Step(`^the committed analysistest fixture scenario "([^"]*)" importing "([^"]*)"$`, w.givenAnalyzerFixture)
	ctx.Step(`^the scenarioimport analyzer runs over it$`, w.whenAnalyzerRuns)
	ctx.Step(`^it reports exactly (\d+) diagnostics?$`, w.thenDiagnostics)
	ctx.Step(`^the diagnostic names "([^"]*)"$`, w.thenDiagnosticNames)

	ctx.Step(`^the committed recording "([^"]*)" under "([^"]*)"$`, w.givenRecording)
	ctx.Step(`^dsh built from this checkout$`, w.givenBuilt)
	ctx.Step(`^the built binary replays it without a key into a scratch home$`, w.whenReplays)
	ctx.Step(`^the normalized persisted log read back from the SQLite store is byte-identical to "([^"]*)"$`, w.thenLogIdentical)
	ctx.Step(`^the workspace tree is byte-identical to "([^"]*)"$`, w.thenWorkspaceIdentical)
	ctx.Step(`^the model requests are byte-identical to "([^"]*)"$`, w.thenRequestsIdentical)
}

func TestE2E_dh_s_16_profiles_launchers_and_evidence_runners_scenario_helper_and_keyless_replay(t *testing.T) {
	if os.Getenv(shkChildEnv) != "" {
		return
	}
	shkOuterT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_scenario_helper_and_keyless_replay,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_16_profiles_launchers_and_evidence_runners_scenario_helper_and_keyless_replay.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
