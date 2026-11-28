package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
//     go test -tags scenario -count=1 -p 1 -timeout <left> -v -run <phase> ./...
//     with DSH_REQUIRE_SCENARIOS set to the phase's IDs, DSH_SCENARIO_ROUTES to
//     claude-code and its full output in the record; a skip anywhere, a non-zero
//     exit or a planned test that did not pass fails the run (DH-ADR-TOOLCHAIN-6:
//     a skip is never a pass). The routed scenarios then run their copilot-cli
//     legs in a recorded run whose pass or failure is kept and decides nothing; a
//     recorded leg that did not run fails (M2-DEC-4).
//     All runs share scenarioBudget, inside the job's 2 h.

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
// (architecture.md rule 7, R-03; impl.md DH-S-17). DH-S-18 runs the release
// rehearsal first, and every later scenario runs on the binary it verified
// (Handoff; impl.md 18.1).
var stageOrder = map[string]struct {
	First, Last []string
	Handoff     bool
}{
	"DH-S-17": {First: []string{"DH-E-TOOLCHAIN-1"}, Last: []string{"DH-E-SANDBOX-7"}},
	"DH-S-18": {First: []string{"DH-E-TOOLCHAIN-2"}, Handoff: true},
}

// The rehearsal hand-off of a Handoff stage: the First phase runs with
// RehearsalDirEnv naming a directory the run owns, where TestToolchainRehearsal
// leaves the six binaries and, only when every check passed, RehearsalManifest
// ("<sha256>  <name>" per binary). The later phases run with DSH_BIN naming this
// host's binary from it, its SHA-256 checked against the manifest; with no verified
// binary they are not run, so no scenario of the stage falls back to a build.
const (
	RehearsalDirEnv   = "DSH_REHEARSAL_DIR"
	RehearsalManifest = "rehearsal.sha256"
)

// rehearsalBinary is the binary of goos/goarch the rehearsal left in dir and its
// SHA-256, which must be the one RehearsalManifest records.
func rehearsalBinary(dir, goos, goarch string) (string, string, error) {
	name := "dsh-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	data, err := os.ReadFile(filepath.Join(dir, RehearsalManifest))
	if err != nil {
		return "", "", fmt.Errorf("no verified rehearsal (%s): %w", RehearsalManifest, err)
	}
	want := ""
	for _, line := range strings.Split(string(data), "\n") {
		if sum, n, ok := strings.Cut(strings.TrimSpace(line), "  "); ok && n == name {
			want = sum
		}
	}
	if want == "" {
		return "", "", fmt.Errorf("%s records no %s", RehearsalManifest, name)
	}
	bin := filepath.Join(dir, name)
	bdata, err := os.ReadFile(bin)
	if err != nil {
		return "", "", fmt.Errorf("rehearsal binary: %w", err)
	}
	sum := sha256.Sum256(bdata)
	if got := hex.EncodeToString(sum[:]); got != want {
		return "", "", fmt.Errorf("rehearsal binary %s: SHA-256 %s, %s records %s", name, got, RehearsalManifest, want)
	}
	return bin, want, nil
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
// phase grouped by module, with the full output written to w, inside
// scenarioBudget. Every phase runs, whatever an earlier one found, so each
// scenario's result is kept; a run the budget no longer covers is not started and
// its scenarios are "not run", never passed.
func (c *Config) runPlan(ctx context.Context, w io.Writer, stage string, plan acceptance.Plan) error {
	if len(plan.Scenarios) == 0 {
		fmt.Fprintln(w, "scenarios: the stage requires no scenario")
		return nil
	}
	deadline := time.Now().Add(scenarioBudget)
	fmt.Fprintf(w, "scenarios: budget %s for every go test run of the plan (tech-spec: 2 h per job)\n", scenarioBudget)
	return runOrdered(w, stage, plan, func(phase acceptance.Plan, extra ...string) error {
		return c.runPhase(ctx, w, phase, deadline, extra...)
	})
}

// runOrdered runs the phases of plan (phases) through run, every phase whatever an
// earlier one found. On a Handoff stage whose plan holds a First ID, the First
// phase runs with RehearsalDirEnv naming a directory owned by this run, and every
// later phase runs with DSH_BIN naming the rehearsal binary of this host checked
// against RehearsalManifest; without one the later phases are not run and fail. A
// plan without a First ID (an S18 variant that does not rehearse) is not handed
// anything.
func runOrdered(w io.Writer, stage string, plan acceptance.Plan, run func(phase acceptance.Plan, extra ...string) error) error {
	order := stageOrder[stage]
	isFirst := func(s acceptance.Scenario) bool { return slices.Contains(order.First, s.ID) }
	handoffStage := order.Handoff && slices.ContainsFunc(plan.Scenarios, isFirst)
	var handoff []string
	dir := ""
	if handoffStage {
		d, err := os.MkdirTemp("", "harness-gate-rehearsal-")
		if err != nil {
			return fmt.Errorf("rehearsal hand-off: %w", err)
		}
		defer os.RemoveAll(d)
		dir = d
	}
	var errs []error
	for _, phase := range phases(stage, plan) {
		var err error
		switch {
		case !handoffStage:
			err = run(phase)
		case slices.ContainsFunc(phase.Scenarios, isFirst):
			if err = run(phase, RehearsalDirEnv+"="+dir); err != nil {
				break
			}
			bin, sum, herr := rehearsalBinary(dir, runtime.GOOS, runtime.GOARCH)
			if herr != nil {
				err = fmt.Errorf("rehearsal hand-off: %w", herr)
				break
			}
			fmt.Fprintf(w, "scenarios: rehearsal hand-off: %s=%s sha256 %s\n", "DSH_BIN", bin, sum)
			if m, rerr := os.ReadFile(filepath.Join(dir, RehearsalManifest)); rerr == nil {
				for _, l := range strings.Split(strings.TrimSpace(string(m)), "\n") {
					fmt.Fprintf(w, "scenarios: rehearsal checksum: %s\n", strings.TrimSpace(l))
				}
			}
			handoff = []string{"DSH_BIN=" + bin}
		case handoff == nil:
			err = fmt.Errorf("%s not run: no verified rehearsal binary (%s did not pass first; impl.md 18.1)", strings.Join(planIDs(phase), ", "), strings.Join(order.First, ", "))
			fmt.Fprintf(w, "scenarios: %v\n", err)
		default:
			err = run(phase, handoff...)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// scenarioBudget bounds all go test runs of one plan together: the s17 job has
// two hours (tech-spec sec. 5, "2 h per job"; timeout-minutes 120), and its steps
// before the scenarios (entry, the route preflights, preflight) take the rest. Each
// go test run gets the budget left as its -timeout. A plan that does not fit is
// sharded (implementation-plan Stage 7.6), never given a longer job. -p 1 keeps
// packages from running scenarios side by side on one signed-in account.
// TestScenarioGatesS17BudgetSpent lowers it to prove a spent budget starts no
// further go test run, names the later phases and the recorded leg "not run", and
// fails the gate.
var scenarioBudget = 100 * time.Minute

// The route selection of forEachRoute (signedin_routes_test.go): the
// required run of a phase runs the claude-code legs, which decide the stage; the
// recorded run runs the copilot-cli legs of the routed scenarios, whose results are
// kept and decide nothing (M2-DEC-4, M1-D-LOOP-6 A).
const (
	routesEnv   = "DSH_SCENARIO_ROUTES"
	requiredLeg = "claude-code"
	recordedLeg = "copilot-cli"
)

// runPhase runs one phase of a plan, grouped by module: per module the required
// run, then the recorded run of the scenarios the required run showed to be routed.
// extra ("KEY=value") is set in both runs (the rehearsal hand-off).
func (c *Config) runPhase(ctx context.Context, w io.Writer, plan acceptance.Plan, deadline time.Time, extra ...string) error {
	mods, byMod, err := c.modulePlans(plan)
	if err != nil {
		return err
	}
	var errs []error
	for _, mod := range mods {
		p := byMod[mod]
		ids := strings.Join(planIDs(p), ",")
		// The required run: the scenarios' own verdict, with no exception for
		// any leg; DSH_REQUIRE_SCENARIOS makes the test kit check their rows.
		out, runErr, err := c.goTestRun(ctx, w, mod, p, deadline, append([]string{acceptance.RequireEnv + "=" + ids, routesEnv + "=" + requiredLeg}, extra...)...)
		if err == nil {
			err = scenarioVerdict(out, p.Tests(), runErr)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.rel(mod), err))
		}
		routed := routedTests(out, p.Tests())
		if len(routed) == 0 {
			continue
		}
		// The recorded run: only the routed scenarios, only their copilot-cli
		// legs, their rows in a report of their own. A leg that ran, passed or
		// failed, is kept and decides nothing; a leg that did not run (a run that
		// never started, a build failure, a skip, no result line) fails the gate,
		// since the recorded leg must run (M2-DEC-4).
		var rp acceptance.Plan
		for _, s := range p.Scenarios {
			if slices.Contains(routed, s.Test) {
				rp.Scenarios = append(rp.Scenarios, s)
			}
		}
		env := append([]string{acceptance.RequireEnv + "=", routesEnv + "=" + recordedLeg}, extra...)
		if r := lookupEnv(c.env(), "DSH_SCENARIO_REPORT"); r != "" {
			env = append(env, "DSH_SCENARIO_REPORT="+r+"."+recordedLeg)
		}
		rout, rrun, rerr := c.goTestRun(ctx, w, mod, rp, deadline, env...)
		if rerr != nil {
			rrun = rerr
		}
		legs, err := recordedLegs(rout, rrun, routed)
		for _, r := range legs {
			fmt.Fprintf(w, "scenarios: recorded leg: %s (kept; does not decide the stage, M2-DEC-4)\n", r)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.rel(mod), err))
		}
	}
	return errors.Join(errs...)
}

// modulePlans groups plan by the module holding each scenario's file, the modules
// in plan order.
func (c *Config) modulePlans(plan acceptance.Plan) ([]string, map[string]acceptance.Plan, error) {
	var mods []string
	byMod := map[string]acceptance.Plan{}
	for _, s := range plan.Scenarios {
		mod := c.moduleOf(filepath.Join(c.Root, filepath.FromSlash(s.File)))
		if mod == "" {
			return nil, nil, fmt.Errorf("%s (%s) is in none of the modules", s.ID, s.File)
		}
		if _, ok := byMod[mod]; !ok {
			mods = append(mods, mod)
		}
		p := byMod[mod]
		p.Scenarios = append(p.Scenarios, s)
		byMod[mod] = p
	}
	return mods, byMod, nil
}

// goTestRun runs the tests of p in mod with kv set, its output to w and returned.
// runErr is go test's own exit; err is a run that did not start because the plan's
// budget is spent.
func (c *Config) goTestRun(ctx context.Context, w io.Writer, mod string, p acceptance.Plan, deadline time.Time, kv ...string) (out string, runErr, err error) {
	left := time.Until(deadline).Truncate(time.Second)
	if left < time.Second {
		return "", nil, fmt.Errorf("%s not run: the %s scenario budget is spent", strings.Join(planIDs(p), ", "), scenarioBudget)
	}
	args := []string{"test", "-tags", acceptance.BuildTag, "-count=1", "-p", "1", "-timeout", left.String(), "-v", "-run", p.RunPattern(), "./..."}
	fmt.Fprintf(w, "scenarios: %s: %s go %s\n", c.rel(mod), strings.Join(kv, " "), strings.Join(args, " "))
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, c.Go, args...)
	cmd.Dir = mod
	cmd.Env = c.env(kv...)
	cmd.Stdout = io.MultiWriter(w, &buf)
	cmd.Stderr = cmd.Stdout
	runErr = cmd.Run()
	return buf.String(), runErr, nil
}

// lookupEnv is the value of name in env, "" when unset.
func lookupEnv(env []string, name string) string {
	v := ""
	for _, kv := range env {
		if k, val, ok := strings.Cut(kv, "="); ok && k == name {
			v = val
		}
	}
	return v
}

// routedTests are the tests of tests whose run in out has a claude-code subtest
// result: the scenarios forEachRoute runs per client route.
func routedTests(out string, tests []string) []string {
	var routed []string
	for _, t := range tests {
		for _, v := range []string{"PASS", "FAIL", "SKIP"} {
			if strings.Contains(out, "--- "+v+": "+t+"/"+requiredLeg+" ") {
				routed = append(routed, t)
				break
			}
		}
	}
	return routed
}

// recordedLegs is, per routed test, the result of its copilot-cli leg in the
// recorded run's output: "<Test>/copilot-cli passed" or "failed", which decide
// nothing, or "skipped" or "not run (...)", which err names: a recorded leg must
// run and leave its result (M2-DEC-4), so one that did not is never a pass.
func recordedLegs(out string, runErr error, routed []string) ([]string, error) {
	var res []string
	var errs []error
	for _, t := range routed {
		result := "not run (no copilot-cli result line)"
		if runErr != nil && out == "" {
			result = "not run (" + runErr.Error() + ")"
		}
		for _, v := range []string{"PASS", "FAIL", "SKIP"} {
			if strings.Contains(out, "--- "+v+": "+t+"/"+recordedLeg+" ") {
				result = map[string]string{"PASS": "passed", "FAIL": "failed", "SKIP": "skipped"}[v]
				break
			}
		}
		res = append(res, t+"/"+recordedLeg+" "+result)
		if result != "passed" && result != "failed" {
			errs = append(errs, fmt.Errorf("recorded leg %s/%s %s; it must run and its result be kept (M2-DEC-4)", t, recordedLeg, result))
		}
	}
	return res, errors.Join(errs...)
}

// scenarioVerdict fails a scenario run whose output has a skip anywhere, a build or
// setup failure, a non-zero go test exit, or a planned test without a PASS line,
// and names each. It makes no exception for any subtest: a recorded leg never runs
// in a required run.
func scenarioVerdict(out string, tests []string, runErr error) error {
	var errs []error
	if runErr != nil {
		errs = append(errs, fmt.Errorf("go test: %w", runErr))
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "[build failed]") || strings.HasSuffix(line, "[setup failed]") {
			errs = append(errs, fmt.Errorf("go test: %s", line))
		}
		if t, ok := strings.CutPrefix(line, "--- SKIP: "); ok {
			name, _, _ := strings.Cut(t, " ")
			errs = append(errs, fmt.Errorf("%s skipped; a skipped scenario is never a pass (DH-ADR-TOOLCHAIN-6)", name))
		}
	}
	for _, t := range tests {
		if !strings.Contains(out, "--- PASS: "+t+" ") {
			errs = append(errs, fmt.Errorf("required scenario %s did not pass", t))
		}
	}
	return errors.Join(errs...)
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
