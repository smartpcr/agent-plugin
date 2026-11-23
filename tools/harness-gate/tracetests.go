package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

// runTraceTests is "harness-gate trace-tests [--root dir] [--stage DH-S-n]"
// (M1-G-CLOSE-9): for every step of DH-S-1..n (default: the index's built-through)
// it prints the DH-T IDs the step and the "Done when" lines of its deliverables
// require, which of them a test names or a doc.go deferral backed by impl.md
// defers, and which are missing. It exits 1 on a missing ID or a problem (a bad
// deferral, a misplaced directive, a broken link), 2 when its inputs cannot be
// read. It is a report, not yet a gate (step 18.1.4 makes it one).
func runTraceTests(args []string, stdout, stderr io.Writer) int {
	var stage string
	root, ok := subcommandFlags("trace-tests", args, stderr, func(f *flag.FlagSet) {
		f.StringVar(&stage, "stage", "", "check DH-S-1 through this stage (default: built-through of the acceptance index)")
	})
	if !ok {
		return 2
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate trace-tests: %v\n", err)
		return 2
	}
	r, err := acceptance.TraceTests(abs, strings.TrimSpace(stage))
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate trace-tests: %v\n", err)
		return 2
	}
	r.Write(stdout)
	if err := r.Err(); err != nil {
		fmt.Fprintf(stdout, "harness-gate trace-tests: FAILED:\n%v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "harness-gate trace-tests: passed")
	return 0
}
