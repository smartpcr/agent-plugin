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
// against the authored *_scenario_test.go files), budget (performance budgets) and
// cross-compile (cmd/dsh for six targets). See README.md.
//
// When --root holds a go.mod, the gates run on that one module (the seeded-violation
// modules of testdata/ are run this way); otherwise they run on the three product
// modules src/go, src/sdk and src/harness.
//
// Subcommands, each with --root:
//
//	rehearse [--out dir]   the release rehearsal: cmd/dsh for six targets, pure Go,
//	                       -trimpath, SHA-256 and embedded version printed, nothing
//	                       published (DH-D-TOOLCHAIN-5)
//	preflight              the scenario workflow's first step: fails when the
//	                       index disagrees with the scenario tests, an ID of
//	                       DSH_REQUIRE_SCENARIOS has no scenario test, its index
//	                       entry requires a stage past built-through, or the client
//	                       its credential names is not signed in on this host
//	                       (DH-D-TOOLCHAIN-4, M1-G-TOOL-16); it reads no model key
//	                       (models-signed-in-claude-copilot)
//	require --stage DH-S-n prints the index entry of the stage being accepted, the
//	                       value of DSH_REQUIRE_SCENARIOS
//	scenarios --stage DH-S-n --evidence file
//	                       runs that stage's scenarios on the development host,
//	                       where the user is signed in (M1-D-TOOL-6 (a)): the
//	                       preflight with the sign-in checks, then go test -tags
//	                       scenario of the plan, as one evidence record
//	                       (scenariorun.go)
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
		case "trace-tests":
			return runTraceTests(args[1:], stdout, stderr)
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
// line; a stage the index does not list fails.
func runRequire(args []string, stdout, stderr io.Writer) int {
	var stage string
	root, ok := subcommandFlags("require", args, stderr, func(f *flag.FlagSet) {
		f.StringVar(&stage, "stage", "", "the acceptance stage being run, e.g. DH-S-18")
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
