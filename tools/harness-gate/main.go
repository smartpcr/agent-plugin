// Command harness-gate runs the repository's quality gates: one fixed, sequential
// list that stops at the first failure, names it and exits non-zero (QG-1,
// DH-ADR-TOOLCHAIN-9). The same command decides "this commit passes" locally and
// in CI:
//
//	GOWORK=off go -C tools/harness-gate run . --root ../..
//
// The gates, in order: gofmt, vet (go vet and the project's own analyzers), test
// (race, shuffle), coverage (merged across processes, 85% per package),
// trace (tools/harness-trace verify), budget (performance budgets) and
// cross-compile (cmd/dsh for six targets). See README.md.
//
// When --root holds a go.mod, the gates run on that one module (the seeded-violation
// modules of testdata/ are run this way); otherwise they run on the three product
// modules src/go, src/sdk and src/harness.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"golang.org/x/tools/go/analysis/unitchecker"

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

// run parses args, runs the gates and returns the exit status: 0 when every gate
// passed, 1 when one failed, 2 on a usage or setup error.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("harness-gate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root, or the directory of a single module to gate")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "harness-gate: unexpected arguments %q\n", flags.Args())
		return 2
	}
	cfg, err := NewConfig(*root)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate: %v\n", err)
		return 2
	}
	return ExitCode(Run(ctx, stdout, Gates(cfg)))
}
