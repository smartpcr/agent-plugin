// Command harness-gate runs the repository's quality gates: one fixed, sequential
// list that stops at the first failure, names it and exits non-zero (QG-1,
// DH-ADR-TOOLCHAIN-9). The same command decides "this commit passes" locally and
// in CI:
//
//	GOWORK=off go -C tools/harness-gate run . --root ../..
//
// The gates, in order: gofmt, vet (go vet and the project's own analyzers), test
// (race, shuffle), coverage (merged across processes, 85% per package),
// trace (tools/harness-trace verify, then the trace-tests check through built-through),
// scenario-index (test/harness/acceptance-index.yaml
// against the authored *_scenario_test.go files), policy-text (M2-C-6: no shipped
// sandbox policy text without a passed DH-E-SANDBOX-7 run on it), budget
// (performance budgets) and
// cross-compile (cmd/dsh for six targets). See README.md.
//
// When --root holds a go.mod, the gates run on that one module (the seeded-violation
// modules of testdata/ are run this way); otherwise they run on the three product
// modules src/go, src/sdk and src/harness.
//
// Subcommands, each with --root:
//
//	rehearse [--out dir]   the release rehearsal: a goreleaser snapshot of
//	                       .goreleaser.dsh.yaml (six archives, checksums.txt, each
//	                       archive verified), SHA-256 and embedded version of each
//	                       binary printed, nothing published (DH-D-TOOLCHAIN-5,
//	                       gap-closure 19.1.1); the keyless replay is open (19.1.2)
//	preflight              the scenario workflow's first step: fails when the
//	                       index disagrees with the scenario tests, an ID of
//	                       DSH_REQUIRE_SCENARIOS has no scenario test, its index
//	                       entry requires a stage past built-through, or the client
//	                       its credential names is not signed in on this host
//	                       (DH-D-TOOLCHAIN-4, M1-G-TOOL-16); it reads no model key
//	                       (models-signed-in-claude-copilot)
//	require --stage DH-S-n [--variant label [--story s] [--inputs]]
//	                       prints the index entry of the stage being accepted, the
//	                       value of DSH_REQUIRE_SCENARIOS; with --variant only the
//	                       IDs that S18 variant runs (variants package); with
//	                       --inputs the DSH_SCENARIO_* inputs their tests read
//	variants [--story s]   the S18 variant fleet of evidence/variants.md cross-
//	                       referenced with the DH-S-18 index entry (M2-DOD-4):
//	                       prints the coverage, exits 1 on any problem (variantscmd.go)
//	ack-probe --listen addr --token t --log file [--send cmd]
//	                       service:ack-recorder preflight: a test acknowledgment is
//	                       recorded and read back (ackprobe.go)
//	scenarios --stage DH-S-n --evidence file
//	                       runs that stage's scenarios on the development host,
//	                       where the user is signed in (M1-D-TOOL-6 (a)): the
//	                       preflight with the sign-in checks, then go test -tags
//	                       scenario of the plan, as one evidence record
//	                       (scenariorun.go)
//	variant-run --stage DH-S-n --scenarios IDS
//	                       the scenario step of an s18 variant job: IDS (require
//	                       --variant) in the stage's order, DH-E-TOOLCHAIN-2 first
//	                       and every later scenario on the rehearsal binary it
//	                       verified (DSH_BIN; impl.md 18.1); a skip or a test
//	                       that did not pass fails (variantrun.go)
//	ready --stage DH-S-n [--built] [--story s]
//	                       the first step of a stage close (C57; M2-C-2): every
//	                       condition of docs/stories/<story>/evidence/<stage>.md
//	                       holds; the status line is not read (stageclose.go)
//	close --stage DH-S-n [--built] [--story s]
//	                       the final check of the closing PR: every ready
//	                       condition and the status line reading closed
//	records [--story s]    every stage record of the story and its closure.md
//	                       hold their shape; a closed record also passes close
//	entry [--story s]      the first step of every guarded and acceptance run
//	                       (M2-DEC-1, risk M2-R-04): the prior story's records all
//	                       read closed, its closure record lists every deliverable
//	                       and built-through has reached its last stage (entry.go)
//	guarded --lane wsl|windows [--stage DH-S-16] --evidence file
//	                       the lane scripts' guarded mode (Stage 6.12, impl.md
//	                       16.8): entry, require --stage, the lane's selection
//	                       and inputs, dsh built once, then go test -tags
//	                       scenario of the plan on scratch homes, as one evidence
//	                       record with the scratch tree listed before and after
//	                       and one Scenarios-table row per ID (guardedrun.go)
//	policy-text            the M2-C-6 pre-merge rule alone (also a gate of the
//	                       list): no file under
//	                       src/harness/internal/world/sandbox/policy/text/
//	                       unless DH-S-17.md holds a passed DH-E-SANDBOX-7 row
//	                       with that file's SHA-256 (policytext.go)
//	sandbox7-record --escalated linux=<n>[,...] [--lane s17] [--ci-run id]
//	                       the s17 job's DH-E-SANDBOX-7 acceptance record of
//	                       the clean checkout: commit, each text's SHA-256 and
//	                       escalations, written to evidence/runs/<id>.txt; it
//	                       runs no gate (sandbox7record.go)
//	sandbox-preflight [--status file] [--service s [--preflight text]]
//	                       the guarded preflight of the WSL lane (Stage 6.10,
//	                       architecture.md I-8): a "dsh sandbox status" report
//	                       (standard input by default) passes only as backend
//	                       bwrap, state Full; anything else exits 1, never a
//	                       skip; --service decides an S18 sandbox service's
//	                       preflight instead (sandboxpreflight.go)
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/analysis/unitchecker"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/analyzers"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/variants"
)

func main() {
	if os.Getenv(vetToolEnv) == "1" {
		// Started by "go vet -vettool" from the vet gate: run the own analyzers.
		unitchecker.Main(analyzers.All...)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run parses args, runs the gates (or a subcommand) and returns the exit status: 0
// when every gate passed, 1 when one failed, 2 on a usage or setup error.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "rehearse":
			return runRehearse(ctx, args[1:], stdout, stderr)
		case "preflight":
			return runPreflight(ctx, args[1:], stdout, stderr)
		case "require":
			return runRequire(args[1:], stdout, stderr)
		case "scenarios":
			return runScenarios(ctx, args[1:], stdout, stderr)
		case "variant-run":
			return runVariantRun(ctx, args[1:], stdout, stderr)
		case "trace-tests":
			return runTraceTests(args[1:], stdout, stderr)
		case "ready":
			return runReady(args[1:], stdout, stderr)
		case "close":
			return runClose(args[1:], stdout, stderr)
		case "records":
			return runRecords(args[1:], stdout, stderr)
		case "entry":
			return runEntry(args[1:], stdout, stderr)
		case "guarded":
			return runGuarded(ctx, args[1:], stdout, stderr)
		case "policy-text":
			return runPolicyText(ctx, args[1:], stdout, stderr)
		case "sandbox7-record":
			return runSandbox7Record(ctx, args[1:], stdout, stderr)
		case "sandbox-preflight":
			return runSandboxPreflight(args[1:], os.Stdin, stdout, stderr)
		case "variants":
			return runVariants(args[1:], stdout, stderr)
		case "ack-probe":
			return runAckProbe(args[1:], stdout, stderr)
		}
	}
	flags := flag.NewFlagSet("harness-gate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root, or the directory of a single module to gate")
	evidence := flags.String("evidence", "", "write an evidence record of the run (header, full output, exit status, SHA-256) to this file; refused on a tree that is not clean")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "harness-gate: unexpected arguments %q\n", flags.Args())
		return 2
	}
	if *evidence != "" {
		return runEvidence(ctx, *root, *evidence, args, stdout, stderr)
	}
	cfg, err := NewConfig(*root)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate: %v\n", err)
		return 2
	}
	printToolchain(stdout, cfg)
	return ExitCode(Run(ctx, stdout, Gates(cfg)))
}

// runEvidence is "harness-gate --root dir --evidence file": the clean-tree check
// first (exit 2 naming the dirty paths, no file written and no gate started), then
// the floor check of NewConfig, then the gates recorded through Config.RunEvidence.
func runEvidence(ctx context.Context, root, path string, args []string, stdout, stderr io.Writer) int {
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate: %v\n", err)
		return 2
	}
	commit, err := CleanCommit(ctx, abs)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate: --evidence: %v\n", err)
		return 2
	}
	if path, err = filepath.Abs(path); err != nil {
		fmt.Fprintf(stderr, "harness-gate: %v\n", err)
		return 2
	}
	cfg, err := NewConfig(abs)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate: %v\n", err)
		return 2
	}
	return cfg.RunEvidence(ctx, path, commit, args, Gates, stdout, stderr)
}

// runRehearse is "harness-gate rehearse [--root dir] [--out dir]": the release
// rehearsal of DH-D-TOOLCHAIN-5. Without --out the binaries go to a temporary
// directory that is removed afterwards.
func runRehearse(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var out string
	root, ok := subcommandFlags("rehearse", args, stderr, func(f *flag.FlagSet) {
		f.StringVar(&out, "out", "", "directory for the binaries (default: a temporary directory, removed afterwards)")
	})
	if !ok {
		return 2
	}
	cfg, err := NewConfig(root)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate rehearse: %v\n", err)
		return 2
	}
	printToolchain(stdout, cfg)
	if out == "" {
		tmp, err := os.MkdirTemp("", "harness-gate-rehearse-")
		if err != nil {
			fmt.Fprintf(stderr, "harness-gate rehearse: %v\n", err)
			return 2
		}
		defer os.RemoveAll(tmp)
		out = tmp
	} else if out, err = filepath.Abs(out); err != nil {
		fmt.Fprintf(stderr, "harness-gate rehearse: %v\n", err)
		return 2
	}
	if _, err := cfg.Rehearse(ctx, stdout, out); err != nil {
		fmt.Fprintf(stdout, "harness-gate rehearse: FAILED: %v\n", err)
		return 1
	}
	return 0
}

// runPreflight is "harness-gate preflight [--root dir]", the first job of the
// credentialed scenario workflow (DH-ADR-TOOLCHAIN-6, -12). It decides from the
// process environment through acceptance.PreflightWith, checking each planned
// scenario's sign-in through signInProbe, prints each sign-in line and starts
// nothing. When GITHUB_OUTPUT is set it appends count=, run= (the go test -run
// expression) and tests= (the test names) for the scenario job.
func runPreflight(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root, ok := subcommandFlags("preflight", args, stderr, nil)
	if !ok {
		return 2
	}
	x, scenarios, err := readAcceptance(root)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate preflight: %v\n", err)
		return 2
	}
	plan, err := acceptance.PreflightWith(ctx, acceptance.Environ(os.Environ()), x, scenarios, signInProbe)
	for _, s := range plan.SignIns {
		fmt.Fprintf(stdout, "harness-gate preflight: %s\n", s)
	}
	if err != nil {
		fmt.Fprintf(stdout, "harness-gate preflight: FAILED, no scenario is started:\n%v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "harness-gate preflight: passed; %d required scenarios", len(plan.Scenarios))
	for _, s := range plan.Scenarios {
		fmt.Fprintf(stdout, "\n  %s  %s (%s)", s.ID, s.Test, s.File)
	}
	fmt.Fprintln(stdout)
	if path := os.Getenv("GITHUB_OUTPUT"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(stderr, "harness-gate preflight: %v\n", err)
			return 2
		}
		_, err = fmt.Fprintf(f, "count=%d\nrun=%s\ntests=%s\n", len(plan.Scenarios), plan.RunPattern(), strings.Join(plan.Tests(), " "))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			fmt.Fprintf(stderr, "harness-gate preflight: %v\n", err)
			return 2
		}
	}
	return 0
}

// runRequire is "harness-gate require [--root dir] --stage DH-S-n": it prints the
// comma-separated scenario IDs of the index entry of the stage being accepted, the
// value of DSH_REQUIRE_SCENARIOS for that run. An empty --stage prints an empty
// line; a stage the index does not list fails. With --variant (an s18 job, Stage
// 8.1) it prints only the IDs that variant of the story's evidence/variants.md
// runs, after the variant list passes its cross-reference with the index. With
// --inputs it prints instead the space-separated DSH_SCENARIO_* inputs the scenario
// tests of those IDs read (variants.InputsFor), the inputs the job must set.
func runRequire(args []string, stdout, stderr io.Writer) int {
	var stage, variant, story string
	var inputs bool
	root, ok := subcommandFlags("require", args, stderr, func(f *flag.FlagSet) {
		f.StringVar(&stage, "stage", "", "the acceptance stage being run, e.g. DH-S-18")
		f.StringVar(&variant, "variant", "", "an S18 variant label of evidence/variants.md, e.g. linux-e2:landlock")
		f.StringVar(&story, "story", closure.DefaultStory, "the story whose evidence/variants.md --variant reads")
		f.BoolVar(&inputs, "inputs", false, "print the run-time inputs the required scenarios read")
	})
	if !ok {
		return 2
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate require: %v\n", err)
		return 2
	}
	x, err := acceptance.ReadIndex(abs)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate require: %v\n", err)
		return 2
	}
	ids, err := x.Required(strings.TrimSpace(stage))
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate require: %v\n", err)
		return 1
	}
	goos := ""
	if variant != "" {
		fleet, err := variants.Read(abs, story)
		if err != nil {
			fmt.Fprintf(stderr, "harness-gate require: %v\n", err)
			return 2
		}
		if ids, err = fleet.Require(x, strings.TrimSpace(stage), variant); err != nil {
			fmt.Fprintf(stderr, "harness-gate require: %v\n", err)
			return 1
		}
		v, _ := fleet.Variant(variant)
		goos = v.GOOS()
	}
	if inputs {
		_, scenarios, err := readAcceptance(abs)
		if err != nil {
			fmt.Fprintf(stderr, "harness-gate require: %v\n", err)
			return 2
		}
		names, err := variants.InputsFor(abs, scenarios, ids, goos)
		if err != nil {
			fmt.Fprintf(stderr, "harness-gate require: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, strings.Join(names, " "))
		return 0
	}
	fmt.Fprintln(stdout, strings.Join(ids, ","))
	return 0
}

// readAcceptance reads the index and the scenario tests of the product modules
// under root; it needs no go command.
func readAcceptance(root string) (*acceptance.Index, []acceptance.Scenario, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	modules, err := discoverModules(abs)
	if err != nil {
		return nil, nil, err
	}
	return (&Config{Root: abs, Modules: modules}).acceptance()
}
