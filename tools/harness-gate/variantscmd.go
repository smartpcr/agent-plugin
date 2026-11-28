package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/variants"
)

// runVariants is "harness-gate variants [--root dir] [--story s]": the index check
// of the S18 variant fleet (implementation-plan Stage 8.1, M2-DOD-4). It reads the
// story's evidence/variants.md and the DH-S-18 entry of the index, prints the
// coverage of each scenario and exits 0 when every scenario has an in-scope variant
// or an owner removal and every row is well formed; 1 naming each problem; 2 when a
// file cannot be read.
func runVariants(args []string, stdout, stderr io.Writer) int {
	var story string
	root, ok := subcommandFlags("variants", args, stderr, func(f *flag.FlagSet) {
		f.StringVar(&story, "story", closure.DefaultStory, "the story whose evidence/variants.md is checked")
	})
	if !ok {
		return 2
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate variants: %v\n", err)
		return 2
	}
	x, err := acceptance.ReadIndex(abs)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate variants: %v\n", err)
		return 2
	}
	fleet, err := variants.Read(abs, story)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate variants: %v\n", err)
		return 2
	}
	if problems := fleet.Check(x, variants.Stage); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(stdout, "harness-gate variants: FAILED: %s\n", p)
		}
		return 1
	}
	fmt.Fprint(stdout, fleet.Coverage(x, variants.Stage))
	fmt.Fprintf(stdout, "harness-gate variants: passed: every %s scenario has an in-scope variant or an owner removal\n", variants.Stage)
	return 0
}
