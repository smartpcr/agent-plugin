package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

// "harness-gate scenarios --stage DH-S-n --evidence FILE" runs the credentialed
// scenarios of one acceptance stage on the development host (M1-D-TOOL-6 (a),
// M1-G-TOOL-16): a hosted runner cannot hold the user's signed-in session, so the
// scenarios that need one run where the user signed in. The run is one 12.3.5
// evidence record (Config.RunEvidence): the clean-tree check, the header, then two
// gates that stop at the first failure:
//
//   - preflight: DSH_REQUIRE_SCENARIOS is the stage's index entry, and
//     acceptance.PreflightWith checks the index, the authored tests, the built
//     stages and, through signInProbe, the sign-in of every planned scenario's
//     client. It prints each sign-in line (no token material). A failure names
//     the scenario and the client, and no scenario starts.
//   - scenarios: per phase (DH-S-17: TOOLCHAIN-1, the rest, SANDBOX-7) and module,
//     go test -tags scenario -count=1 -p 1 -timeout 110m -v -run <phase> ./...
//     with DSH_REQUIRE_SCENARIOS set to the phase's IDs and its full output in the
//     record; a skip anywhere, or a planned test that did not pass, fails the run
//     (DH-ADR-TOOLCHAIN-6: a skip is never a pass). Of a routed scenario the
//     claude-code leg decides and the copilot-cli leg is recorded (M2-DEC-4).

// signInProbe is the sign-in probe of the preflight and scenarios subcommands:
// each client's own status command, run with the credential variables removed
// from the environment. Tests replace it; no test runs a real client.
var signInProbe = acceptance.StatusProbe(execRunner(probeEnv(os.Environ())))

// probeTimeout bounds one sign-in status command.
const probeTimeout = 30 * time.Second

// probeScrubbed are the variables a client prefers to its stored sign-in (the
// names the signed-in routes remove, docs/harness/libs/claude-code-cli.md and
// github-copilot-cli.md): with any of them set, a status check would report a key
// or token, not the user's session.
var probeScrubbed = []string{
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
	"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN",
}

// probeEnv is environ without the probeScrubbed variables.
func probeEnv(environ []string) []string {
	return slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
		return slices.ContainsFunc(probeScrubbed, func(name string) bool { return envKey(kv) == envKey(name+"=") })
	})
}

// execRunner runs a status command in env, with probeTimeout. Its standard error
// is discarded, so nothing the client prints there reaches a record.
func execRunner(env []string) acceptance.CommandRunner {
	return func(ctx context.Context, name string, args ...string) ([]byte, int, error) {
		ctx, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		path, err := exec.LookPath(name)
		if err != nil {
			return nil, 0, err
		}
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env = env
		var out bytes.Buffer
		cmd.Stdout = &out
		err = cmd.Run()
		var exit *exec.ExitError
		switch {
		case ctx.Err() != nil:
			return nil, 0, fmt.Errorf("timed out after %s", probeTimeout)
		case errors.As(err, &exit):
			return out.Bytes(), exit.ExitCode(), nil
		case err != nil:
			return nil, 0, err
		}
		return out.Bytes(), 0, nil
	}
}

// runScenarios is "harness-gate scenarios [--root dir] --stage DH-S-n --evidence
// file". It returns 2 on a usage or setup error (a dirty tree included, naming the
// paths, before anything runs), 1 when the preflight or a scenario fails, and 0
// when every planned scenario passed.
func runScenarios(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var stage, evidence string
	root, ok := subcommandFlags("scenarios", args, stderr, func(f *flag.FlagSet) {
		f.StringVar(&stage, "stage", "", "the acceptance stage whose scenarios run, an entry of "+acceptance.IndexPath+", e.g. DH-S-17")
		f.StringVar(&evidence, "evidence", "", "the evidence record to write (required)")
	})
	if !ok {
		return 2
	}
	stage = strings.TrimSpace(stage)
	if _, ok := acceptance.StageNumber(stage); !ok {
		fmt.Fprintf(stderr, "harness-gate scenarios: --stage %q is not a stage ID (DH-S-n)\n", stage)
		return 2
	}
	if evidence == "" {
		fmt.Fprintln(stderr, "harness-gate scenarios: --evidence is required: a credentialed run is recorded (M1-D-TOOL-6)")
		return 2
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate scenarios: %v\n", err)
		return 2
	}
	commit, err := CleanCommit(ctx, abs)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate scenarios: --evidence: %v\n", err)
		return 2
	}
	if evidence, err = filepath.Abs(evidence); err != nil {
		fmt.Fprintf(stderr, "harness-gate scenarios: %v\n", err)
		return 2
	}
	cfg, err := NewConfig(abs)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate scenarios: %v\n", err)
		return 2
	}
	record := append([]string{"scenarios"}, args...)
	gates := func(c *Config) []Gate { return c.ScenarioGates(stage, signInProbe) }
	return cfg.RunEvidence(ctx, evidence, commit, record, gates, stdout, stderr)
}

// ScenarioGates are the two gates of a scenarios run of stage: preflight, then the
// planned scenario tests. The second runs only after the first passed (Run).
func (c *Config) ScenarioGates(stage string, probe acceptance.SignInProbe) []Gate {
	var plan acceptance.Plan
	return []Gate{
		{Name: "preflight", Title: "the index entry of " + stage + ", its scenario tests, built stages and the sign-in of every client it needs", Run: func(ctx context.Context, w io.Writer) error {
			x, scenarios, err := c.acceptance()
			if err != nil {
				return err
			}
			ids, err := x.Required(stage)
			if err != nil {
				return err
			}
			env := map[string]string{acceptance.RequireEnv: strings.Join(ids, ",")}
			fmt.Fprintf(w, "preflight: %s=%s\n", acceptance.RequireEnv, env[acceptance.RequireEnv])
			p, err := acceptance.PreflightWith(ctx, env, x, scenarios, probe)
			for _, s := range p.SignIns {
				fmt.Fprintf(w, "preflight: %s\n", s)
			}
			if err != nil {
				return fmt.Errorf("no scenario is started:\n%w", err)
			}
			plan = p
			fmt.Fprintf(w, "preflight: passed; %d required scenarios\n", len(plan.Scenarios))
			return nil
		}},
		{Name: "scenarios", Title: "go test -tags scenario of the planned scenarios; a skip or a planned test that did not pass fails", Run: func(ctx context.Context, w io.Writer) error {
			return c.runPlan(ctx, w, stage, plan)
		}},
	}
}

// stageOrder is the run order of an acceptance stage whose scenarios must not run in
// the order go test finds them: the IDs of First run, each to its end, before every
// other planned scenario, and those of Last after all of them. DH-S-17 runs the
// record-then-replay scenario first and the policy-text escalation last
// (architecture.md rule 7, R-03; impl.md DH-S-17).
var stageOrder = map[string]struct{ First, Last []string }{
	"DH-S-17": {First: []string{"DH-E-TOOLCHAIN-1"}, Last: []string{"DH-E-SANDBOX-7"}},
}

// phases splits plan into the go test runs of stage, in order: the First IDs of
// stageOrder, the rest in plan order, then the Last IDs. A stage with no order is one
// phase; empty phases are dropped, and every planned scenario is in exactly one.
func phases(stage string, plan acceptance.Plan) []acceptance.Plan {
	order, ok := stageOrder[stage]
	if !ok {
		return []acceptance.Plan{plan}
	}
	var first, middle, last acceptance.Plan
	for _, s := range plan.Scenarios {
		switch {
		case slices.Contains(order.First, s.ID):
			first.Scenarios = append(first.Scenarios, s)
		case slices.Contains(order.Last, s.ID):
			last.Scenarios = append(last.Scenarios, s)
		default:
			middle.Scenarios = append(middle.Scenarios, s)
		}
	}
	var out []acceptance.Plan
	for _, p := range []acceptance.Plan{first, middle, last} {
		if len(p.Scenarios) > 0 {
			out = append(out, p)
		}
	}
	return out
}

// runPlan runs the planned scenario tests of stage phase by phase (phases), each
// phase grouped by module, with the full output written to w. Each go test run sets
// DSH_REQUIRE_SCENARIOS to its own IDs, so the test kit fails a required scenario of
// that run that skips or writes no row. Every phase runs, whatever an earlier one
// found, so each scenario's result is kept.
func (c *Config) runPlan(ctx context.Context, w io.Writer, stage string, plan acceptance.Plan) error {
	if len(plan.Scenarios) == 0 {
		fmt.Fprintln(w, "scenarios: the stage requires no scenario")
		return nil
	}
	var errs []error
	for _, phase := range phases(stage, plan) {
		if err := c.runPhase(ctx, w, phase); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// scenarioTimeout bounds one go test run of a phase: real-model scenarios outlast go
// test's 10-minute default (SANDBOX-7 alone is 108 model runs, R-03). -p 1 keeps
// packages from running scenarios side by side on one signed-in account.
const scenarioTimeout = "110m"

// runPhase runs one phase of a plan, grouped by module.
func (c *Config) runPhase(ctx context.Context, w io.Writer, plan acceptance.Plan) error {
	var mods []string
	byMod := map[string]acceptance.Plan{}
	for _, s := range plan.Scenarios {
		mod := c.moduleOf(filepath.Join(c.Root, filepath.FromSlash(s.File)))
		if mod == "" {
			return fmt.Errorf("%s (%s) is in none of the modules", s.ID, s.File)
		}
		if _, ok := byMod[mod]; !ok {
			mods = append(mods, mod)
		}
		p := byMod[mod]
		p.Scenarios = append(p.Scenarios, s)
		byMod[mod] = p
	}
	var errs []error
	for _, mod := range mods {
		p := byMod[mod]
		ids := make([]string, 0, len(p.Scenarios))
		for _, s := range p.Scenarios {
			ids = append(ids, s.ID)
		}
		args := []string{"test", "-tags", acceptance.BuildTag, "-count=1", "-p", "1", "-timeout", scenarioTimeout, "-v", "-run", p.RunPattern(), "./..."}
		fmt.Fprintf(w, "scenarios: %s: %s=%s go %s\n", c.rel(mod), acceptance.RequireEnv, strings.Join(ids, ","), strings.Join(args, " "))
		var out bytes.Buffer
		cmd := exec.CommandContext(ctx, c.Go, args...)
		cmd.Dir = mod
		cmd.Env = c.env(acceptance.RequireEnv + "=" + strings.Join(ids, ","))
		cmd.Stdout = io.MultiWriter(w, &out)
		cmd.Stderr = cmd.Stdout
		runErr := cmd.Run()
		recorded, err := scenarioVerdict(out.String(), p.Tests(), runErr)
		for _, r := range recorded {
			fmt.Fprintf(w, "scenarios: recorded leg: %s (kept; does not decide the stage, M2-DEC-4)\n", r)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.rel(mod), err))
		}
	}
	return errors.Join(errs...)
}

// requiredLeg and recordedLeg are the subtests of a scenario with one subtest per
// client route (forEachRoute in signedin_routes_test.go): the claude-code leg must
// pass; the copilot-cli leg is run and its result kept, and its failure does not fail
// the stage (M2-DEC-4, M1-D-LOOP-6 A).
const (
	requiredLeg = "claude-code"
	recordedLeg = "copilot-cli"
)

// scenarioVerdict fails a scenario run whose output has a skip anywhere, a build or
// setup failure, a failing test or subtest other than a recorded leg, or a planned
// test that did not pass, and names each. A planned test whose only failures are in
// its recorded leg passes when its required leg passed. recorded lists every recorded
// leg's result, "<Test>/copilot-cli passed" or "... failed". A non-zero go test exit
// is an error unless a recorded-leg failure accounts for it.
func scenarioVerdict(out string, tests []string, runErr error) (recorded []string, err error) {
	var errs []error
	passed := map[string]bool{}
	failed := map[string]bool{}
	var failures []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "[build failed]") || strings.HasSuffix(line, "[setup failed]") {
			errs = append(errs, fmt.Errorf("go test: %s", line))
		}
		for _, verdict := range []string{"PASS", "FAIL", "SKIP"} {
			rest, ok := strings.CutPrefix(line, "--- "+verdict+": ")
			if !ok {
				continue
			}
			name, _, _ := strings.Cut(rest, " ")
			switch verdict {
			case "PASS":
				passed[name] = true
			case "FAIL":
				failed[name] = true
				failures = append(failures, name)
			case "SKIP":
				errs = append(errs, fmt.Errorf("%s skipped; a skipped scenario is never a pass (DH-ADR-TOOLCHAIN-6)", name))
			}
			if top, leg, _ := strings.Cut(name, "/"); leg == recordedLeg && verdict != "SKIP" {
				recorded = append(recorded, top+"/"+recordedLeg+" "+strings.ToLower(verdict)+"ed")
			}
		}
	}
	// A failing subtest outside a recorded leg fails its test; a failing recorded
	// leg only explains its parent's failure.
	recordedFailed := false
	otherFailed := map[string]bool{}
	for _, name := range failures {
		top, leg, sub := strings.Cut(name, "/")
		switch {
		case !sub:
		case leg == recordedLeg || strings.HasPrefix(leg, recordedLeg+"/"):
			recordedFailed = true
		default:
			otherFailed[top] = true
			errs = append(errs, fmt.Errorf("%s failed", name))
		}
	}
	legPassed := func(t string) bool {
		return failed[t] && !otherFailed[t] && passed[t+"/"+requiredLeg] && failed[t+"/"+recordedLeg]
	}
	for _, name := range failures {
		if !strings.Contains(name, "/") && !slices.Contains(tests, name) && !legPassed(name) {
			errs = append(errs, fmt.Errorf("%s failed", name))
		}
	}
	for _, t := range tests {
		if !passed[t] && !legPassed(t) {
			errs = append(errs, fmt.Errorf("required scenario %s did not pass", t))
		}
	}
	if runErr != nil && !(recordedFailed && len(errs) == 0) {
		errs = append(errs, fmt.Errorf("go test: %w", runErr))
	}
	return recorded, errors.Join(errs...)
}

// moduleOf is the module of c.Modules that holds path, the deepest one.
func (c *Config) moduleOf(path string) string {
	best := ""
	for _, m := range c.Modules {
		if rel, err := filepath.Rel(m, path); err == nil && !strings.HasPrefix(rel, "..") && len(m) > len(best) {
			best = m
		}
	}
	return best
}
