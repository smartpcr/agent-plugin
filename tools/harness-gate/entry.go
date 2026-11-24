package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
)

// runEntry is "harness-gate entry [--story harness:DSH-M2] [--root dir]"
// (implementation-plan Stage 1.2; M2-DEC-1 as amended by M1 gap-closure step 12.1.9):
// the mandatory first step of every guarded and acceptance run of the story (risk
// M2-R-04). It exits 0 when the prior story is closed, 1 naming each unmet condition,
// 2 on a usage error or an unreadable record or index. The gate venue does not call it.
func runEntry(args []string, stdout, stderr io.Writer) int {
	var story string
	root, ok := subcommandFlags("entry", args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&story, "story", closure.DefaultStory, "the story whose entry condition is checked")
	})
	if !ok {
		return 2
	}
	e, known := closure.Entries[strings.TrimSpace(story)]
	if !known {
		fmt.Fprintf(stderr, "harness-gate entry: story %q has no entry condition\n", story)
		return 2
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate entry: %v\n", err)
		return 2
	}
	in, err := closure.ReadEntryInputs(abs, e)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate entry: %v\n", err)
		return 2
	}
	head := fmt.Sprintf("harness-gate entry: %s: %s is not closed (M2-DEC-1)", e.Story, e.Prior)
	if unmet := e.Check(in); len(unmet) > 0 {
		printBlockers(stdout, head+"; no run may start", unmet)
		return 1
	}
	fmt.Fprintf(stdout, "harness-gate entry: %s: %s is closed: %s\n", e.Story, e.Prior, e.Summary())
	return 0
}
