package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/guarded"
)

// "harness-gate guarded --lane wsl|windows --stage DH-S-16 --evidence FILE" is the
// guarded mode of the lane scripts (harness:DSH-M2 step 16.8, implementation-plan
// Stage 6.12; M2-C-1, M2-EN-6): one evidence record (Config.RunEvidence) of the
// clean checkout whose gates stop at the first failure:
//
//   - entry: harness-gate entry, the mandatory first step (M2-DEC-1, risk
//     M2-R-04): no scenario runs before the prior story is closed;
//   - require: harness-gate require --stage, the lane's selection (guarded.Select:
//     the Windows lane records DH-E-STORE-2 out of scope by impl.md 16.8), the
//     index checked against the scenario tests and every requirement built but
//     the stage itself (guarded.RequiresBuilt);
//   - inputs: every run-time input the planned scenarios read is set, a program
//     input resolves (service:git-and-editor), a signed-in route is signed in;
//   - build: dsh built once from this checkout, its SHA-256 recorded (DSH_BIN);
//   - scenarios: go test -tags scenario -count=1 -p 1 -v of the plan in the
//     scenario package with TMPDIR, TMP and TEMP on a scratch directory, so every
//     scratch home and workspace (t.TempDir) is under it; the scratch tree listed
//     before and after (the cleanup evidence), a skip or a planned test that did
//     not pass fails, and one Scenarios-table row per required ID is printed.

// guardedTimeout bounds the scenario go test run: STORE-2 waits on a real model
// and FS-3 runs 2,000 edits.
const guardedTimeout = "90m"

// runGuarded is "harness-gate guarded". It returns 2 on a usage or setup error (a
// dirty tree included), 1 when a gate fails and 0 when every planned scenario
// passed.
func runGuarded(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var laneFlag, stage, evidence string
	root, ok := subcommandFlags("guarded", args, stderr, func(f *flag.FlagSet) {
		f.StringVar(&laneFlag, "lane", "", "the lane running it: wsl or windows")
		f.StringVar(&stage, "stage", "DH-S-16", "the build stage whose guarded scenarios run")
		f.StringVar(&evidence, "evidence", "", "the evidence record to write (required)")
	})
	if !ok {
		return 2
	}
	lane, err := guarded.ParseLane(laneFlag)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate guarded: --lane: %v\n", err)
		return 2
	}
	stage = strings.TrimSpace(stage)
	if _, ok := acceptance.StageNumber(stage); !ok {
		fmt.Fprintf(stderr, "harness-gate guarded: --stage %q is not a stage ID (DH-S-n)\n", stage)
		return 2
	}
	if evidence == "" {
		fmt.Fprintln(stderr, "harness-gate guarded: --evidence is required: a guarded run is recorded (impl.md sec. 7.3)")
		return 2
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate guarded: %v\n", err)
		return 2
	}
	commit, err := CleanCommit(ctx, abs)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate guarded: --evidence: %v\n", err)
		return 2
	}
	if evidence, err = filepath.Abs(evidence); err != nil {
		fmt.Fprintf(stderr, "harness-gate guarded: %v\n", err)
		return 2
	}
	cfg, err := NewConfig(abs)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate guarded: %v\n", err)
		return 2
	}
	scratch, err := os.MkdirTemp("", "harness-gate-guarded-")
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate guarded: %v\n", err)
		return 2
	}
	record := append([]string{"guarded"}, args...)
	run := &guardedRun{lane: lane, stage: stage, commit: commit, scratch: scratch, probe: signInProbe}
	code := cfg.RunEvidence(ctx, evidence, commit, record, run.gates, stdout, stderr)
	// A failed run keeps the scratch tree (TMPDIR, the homes a failing scenario
	// left and the built dsh) for inspection; only a passing run removes it.
	if code == 0 {
		if err := os.RemoveAll(scratch); err != nil {
			fmt.Fprintf(stderr, "harness-gate guarded: removing scratch %s: %v\n", scratch, err)
		}
		return code
	}
	fmt.Fprintf(stderr, "harness-gate guarded: scratch kept: %s\n", scratch)
	return code
}

// guardedRun is the state one guarded run's gates share.
type guardedRun struct {
	lane    guarded.Lane
	stage   string
	commit  string
	scratch string
	probe   acceptance.SignInProbe

	plan acceptance.Plan
	out  []guarded.Excluded
	bin  string
	sum  string
}

func (g *guardedRun) gates(c *Config) []Gate {
	return []Gate{
		{Name: "entry", Title: "harness-gate entry: the prior story is closed (M2-DEC-1)", Run: func(ctx context.Context, w io.Writer) error {
			return guardedEntry(c.Root, w)
		}},
		{Name: "require", Title: "harness-gate require --stage " + g.stage + ", the lane's selection and the index", Run: func(ctx context.Context, w io.Writer) error {
			return g.require(c, w)
		}},
		{Name: "inputs", Title: "every run-time input of the planned scenarios set, programs resolved, sign-ins checked", Run: func(ctx context.Context, w io.Writer) error {
			return g.inputs(ctx, c, w)
		}},
		{Name: "build", Title: "dsh built once from this checkout (DSH_BIN)", Run: func(ctx context.Context, w io.Writer) error {
			return g.build(ctx, c, w)
		}},
		{Name: "scenarios", Title: "go test -tags scenario of the plan on scratch homes; a skip or a planned test that did not pass fails", Run: func(ctx context.Context, w io.Writer) error {
			return g.scenarios(ctx, c, w)
		}},
	}
}

// guardedEntry is runEntry's check of the default story, as a gate.
func guardedEntry(root string, w io.Writer) error {
	e, ok := closure.Entries[closure.DefaultStory]
	if !ok {
		return fmt.Errorf("story %s has no entry condition", closure.DefaultStory)
	}
	in, err := closure.ReadEntryInputs(root, e)
	if err != nil {
		return err
	}
	if unmet := e.Check(in); len(unmet) > 0 {
		for _, u := range unmet {
			fmt.Fprintf(w, "entry: blocker: %s\n", u)
		}
		return fmt.Errorf("%s: %s is not closed (M2-DEC-1); no scenario may start, %d blockers", e.Story, e.Prior, len(unmet))
	}
	fmt.Fprintf(w, "entry: %s: %s is closed: %s\n", e.Story, e.Prior, e.Summary())
	return nil
}

func (g *guardedRun) require(c *Config, w io.Writer) error {
	x, scenarios, err := c.acceptance()
	if err != nil {
		return err
	}
	ids, err := x.Required(g.stage)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "require: harness-gate require --stage %s: %s\n", g.stage, strings.Join(ids, ","))
	run, out := guarded.Select(g.lane, ids)
	g.out = out
	for _, e := range out {
		fmt.Fprintf(w, "require: %s on the %s lane: %s\n", e.ID, g.lane, e.Result())
	}
	errs := []error{acceptance.Check(x, scenarios), guarded.RequiresBuilt(x, g.stage, run)}
	for _, id := range run {
		i := slices.IndexFunc(scenarios, func(s acceptance.Scenario) bool { return s.ID == id })
		if i < 0 {
			errs = append(errs, fmt.Errorf("required scenario %s has no scenario test", id))
			continue
		}
		g.plan.Scenarios = append(g.plan.Scenarios, scenarios[i])
		fmt.Fprintf(w, "require: %s  %s (%s)\n", id, scenarios[i].Test, scenarios[i].File)
	}
	if err := errors.Join(errs...); err != nil {
		g.plan = acceptance.Plan{}
		return fmt.Errorf("no scenario is started:\n%w", err)
	}
	fmt.Fprintf(w, "require: %s=%s\n", acceptance.RequireEnv, strings.Join(run, ","))
	return nil
}

func (g *guardedRun) inputs(ctx context.Context, c *Config, w io.Writer) error {
	lookup := func(name string) (string, bool) {
		for _, kv := range slices.Backward(c.Environ) {
			if envKey(kv) == envKey(name+"=") {
				return kv[len(name)+1:], true
			}
		}
		return "", false
	}
	var errs []error
	for _, s := range g.plan.Scenarios {
		src, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(s.File)))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		names, err := guarded.Inputs(s.File, src, s.Test)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		fmt.Fprintf(w, "inputs: %s reads %s\n", s.ID, strings.Join(names, ", "))
		if missing := guarded.Missing(names, lookup); len(missing) > 0 {
			errs = append(errs, fmt.Errorf("%s: unset: %s (the lane's --inputs file exports them)", s.ID, strings.Join(missing, ", ")))
		}
		for _, n := range names {
			if service, ok := guarded.Programs[n]; ok {
				if v, set := lookup(n); set && strings.TrimSpace(v) != "" {
					if _, err := exec.LookPath(v); err != nil {
						errs = append(errs, fmt.Errorf("%s: %s preflight failed: %s=%s does not resolve to a program: %v", s.ID, service, n, v, err))
					}
				} else {
					errs = append(errs, fmt.Errorf("%s: %s preflight failed: %s is unset", s.ID, service, n))
				}
			}
		}
		if cred, ok := guarded.SignIn[s.ID]; ok {
			st, err := g.probe(ctx, cred)
			switch {
			case err != nil:
				errs = append(errs, fmt.Errorf("%s: sign-in %s: %v", s.ID, cred, err))
			case !st.SignedIn:
				errs = append(errs, fmt.Errorf("%s: sign-in %s preflight failed: `%s`: %s", s.ID, cred, st.Command, st.Detail))
			default:
				fmt.Fprintf(w, "inputs: %s: sign-in %s via `%s`: %s\n", s.ID, cred, st.Command, st.Detail)
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("no scenario is started:\n%w", err)
	}
	return nil
}

// guardedBuildArgs builds the guarded lane's dsh in src/harness with the
// testhook tag: the keyless replay provider (llm.replay) the stage's scenarios
// load is registered only in that build (internal/boot/builtin/testhook.go).
func guardedBuildArgs(bin string) []string {
	return []string{"build", "-tags", "testhook", "-o", bin, "./cmd/dsh"}
}

func (g *guardedRun) build(ctx context.Context, c *Config, w io.Writer) error {
	mod := c.moduleOf(filepath.Join(c.Root, "src", "harness", "cmd", "dsh"))
	if mod == "" {
		return errors.New("src/harness is not one of the modules")
	}
	g.bin = filepath.Join(g.scratch, "dsh")
	if runtime.GOOS == "windows" {
		g.bin += ".exe"
	}
	if err := c.goStep(ctx, w, mod, c.env(), guardedBuildArgs(g.bin)...); err != nil {
		return err
	}
	sum, err := fileSHA256(g.bin)
	if err != nil {
		return err
	}
	g.sum = sum
	fmt.Fprintf(w, "build: DSH_BIN=%s sha256 %s\n", g.bin, sum)
	return nil
}

func (g *guardedRun) scenarios(ctx context.Context, c *Config, w io.Writer) error {
	if len(g.plan.Scenarios) == 0 {
		fmt.Fprintln(w, "scenarios: the lane runs no scenario of", g.stage)
		g.rows(w, nil)
		return nil
	}
	mod := c.moduleOf(filepath.Join(c.Root, filepath.FromSlash(g.plan.Scenarios[0].File)))
	pkg := "./" + filepath.ToSlash(mustRel(mod, filepath.Dir(filepath.Join(c.Root, filepath.FromSlash(g.plan.Scenarios[0].File)))))
	tmp, report := filepath.Join(g.scratch, "tmp"), filepath.Join(g.scratch, "report.jsonl")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		return err
	}
	listScratch(w, "before", tmp)
	args := []string{"test", "-tags", acceptance.BuildTag, "-count=1", "-p", "1", "-v", "-timeout", guardedTimeout, "-run", g.plan.RunPattern(), pkg}
	fmt.Fprintf(w, "scenarios: %s: go %s\n", c.rel(mod), strings.Join(args, " "))
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, c.Go, args...)
	cmd.Dir = mod
	cmd.Env = c.env("TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp, "DSH_BIN="+g.bin,
		acceptance.RequireEnv+"="+strings.Join(planIDs(g.plan), ","), "DSH_SCENARIO_REPORT="+report)
	cmd.Stdout = io.MultiWriter(w, &out)
	cmd.Stderr = cmd.Stdout
	runErr := cmd.Run()
	listScratch(w, "after", tmp)
	ev := evidenceLines(out.String())
	fmt.Fprintf(w, "cleanup evidence: %d lines the scenarios logged before removing their scratch homes\n", len(ev))
	for _, l := range ev {
		fmt.Fprintln(w, "cleanup evidence:", l)
	}
	got := reportResults(report)
	g.rows(w, got)
	_, verdict := scenarioVerdict(out.String(), g.plan.Tests(), runErr)
	if verdict != nil {
		fmt.Fprintf(w, "scratch kept: %s\n", filepath.ToSlash(g.scratch))
	}
	return verdict
}

// rows prints one Scenarios-table row of the stage record per required ID: the
// report's result of each planned one ("not run (no report row)" when it wrote
// none) and the decision of each excluded one.
func (g *guardedRun) rows(w io.Writer, got map[string]string) {
	sha7 := g.commit
	if len(sha7) > 7 {
		sha7 = sha7[:7]
	}
	row := func(id, result string) {
		fmt.Fprintf(w, "row: | %s | %s | guarded | required | scratch home | %s | %s | |\n", id, g.lane.Variant(), sha7, result)
	}
	for _, id := range planIDs(g.plan) {
		r, ok := got[id]
		if !ok {
			r = "not run (no report row)"
		}
		row(id, r)
	}
	for _, e := range g.out {
		row(e.ID, e.Result())
	}
}

// evidenceLines picks the scenarios' "evidence:" log lines out of `go test -v`
// output, each after the test that logged it: the home's files, the deleted
// sessions' absence and the log's seq range, observed before the scenario's
// cleanup removed its scratch home.
func evidenceLines(out string) []string {
	var lines []string
	test := ""
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimRight(l, "\r")
		if f := strings.Fields(l); len(f) == 3 && f[0] == "===" && (f[1] == "RUN" || f[1] == "CONT") {
			test = f[2]
			continue
		}
		if _, ev, ok := strings.Cut(l, ": evidence: "); ok {
			lines = append(lines, test+": "+ev)
		}
	}
	return lines
}

func planIDs(p acceptance.Plan) []string {
	ids := make([]string, 0, len(p.Scenarios))
	for _, s := range p.Scenarios {
		ids = append(ids, s.ID)
	}
	return ids
}

// reportResults reads the scenario helper's report (C55 rows, one JSON line each)
// and returns each ID's result, the first one that is not passed when it wrote
// several.
func reportResults(path string) map[string]string {
	got := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return got
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var row struct{ Kind, ID, Result string }
		if json.Unmarshal(sc.Bytes(), &row) != nil || row.Kind != "scenario" || row.ID == "" {
			continue
		}
		if prev, ok := got[row.ID]; !ok || prev == "passed" {
			got[row.ID] = row.Result
		}
	}
	return got
}

// listScratch prints every entry under dir with its size: before the run it is
// empty; after it, a passing scenario has removed its scratch home and workspace,
// and a failing one names the home it kept.
func listScratch(w io.Writer, when, dir string) {
	n := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		n++
		kind, size := "file", int64(0)
		if d.IsDir() {
			kind = "dir"
		} else if info, ierr := d.Info(); ierr == nil {
			size = info.Size()
		}
		fmt.Fprintf(w, "scratch %s: %s %s %d\n", when, kind, filepath.ToSlash(mustRel(dir, p)), size)
		return nil
	})
	if err != nil {
		fmt.Fprintf(w, "scratch %s: listing %s: %v\n", when, dir, err)
	}
	fmt.Fprintf(w, "scratch %s: %s holds %d entries\n", when, filepath.ToSlash(dir), n)
}

func mustRel(base, p string) string {
	if r, err := filepath.Rel(base, p); err == nil {
		return r
	}
	return p
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
