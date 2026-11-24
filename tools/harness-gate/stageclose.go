package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
)

// The two-step stage-closure protocol (C57; M2-C-2; README.md, "Stage closure"):
//
//	harness-gate ready --stage DH-S-n [--built] [--story harness:DSH-M2] [--root dir]
//	harness-gate close --stage DH-S-n [--built] [--story harness:DSH-M2] [--root dir]
//	harness-gate records [--story harness:DSH-M2] [--root dir]
//
// ready checks a record that still reads "Closure status: open" and ignores the line;
// close re-runs every ready condition and requires the line to read closed. Both exit
// 0 when the stage passes, 1 naming each blocker, 2 on a usage or setup error.

type closeFlags struct {
	root, stage, story string
	built              bool
	s                  closure.Story
}

func parseCloseFlags(name string, args []string, stderr io.Writer) (closeFlags, bool) {
	var f closeFlags
	root, ok := subcommandFlags(name, args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&f.stage, "stage", "", "the stage to check, e.g. DH-S-16")
		fs.StringVar(&f.story, "story", closure.DefaultStory, "the story whose evidence/<stage>.md is read")
		fs.BoolVar(&f.built, "built", false, "a build-stage close: steps, manual checks, owner defaults and guarded rows")
	})
	if !ok {
		return f, false
	}
	f.root = root
	if _, ok := acceptance.StageNumber(f.stage); !ok {
		fmt.Fprintf(stderr, "harness-gate %s: --stage %q is not a stage ID (DH-S-n)\n", name, f.stage)
		return f, false
	}
	s, known := closure.Stories[strings.TrimSpace(f.story)]
	if !known {
		fmt.Fprintf(stderr, "harness-gate %s: unknown story %q\n", name, f.story)
		return f, false
	}
	f.s = s
	return f, true
}

// mode names the kind of close in the output.
func (f closeFlags) mode() string {
	if f.built {
		return "built"
	}
	return "acceptance"
}

// runReady is "harness-gate ready" (implementation-plan Stage 1.1 step 1).
func runReady(args []string, stdout, stderr io.Writer) int {
	f, ok := parseCloseFlags("ready", args, stderr)
	if !ok {
		return 2
	}
	x, code := readIndexFor("ready", f.root, stderr)
	if x == nil {
		return code
	}
	head := fmt.Sprintf("harness-gate ready: %s (%s, %s)", f.stage, closure.RecordPath(f.story, f.stage), f.mode())
	rec, err := readRecordAt(f)
	if err != nil {
		printBlockers(stdout, head+": NOT READY", []string{err.Error()})
		return 1
	}
	if blockers := f.s.Ready(x, f.stage, rec, f.built); len(blockers) > 0 {
		printBlockers(stdout, head+": NOT READY", blockers)
		return 1
	}
	fmt.Fprintf(stdout, "%s: ready; the status line is not read (it reads %s)\n", head, rec.Status)
	return 0
}

// runClose is "harness-gate close" (implementation-plan Stage 1.1 step 3).
func runClose(args []string, stdout, stderr io.Writer) int {
	f, ok := parseCloseFlags("close", args, stderr)
	if !ok {
		return 2
	}
	x, code := readIndexFor("close", f.root, stderr)
	if x == nil {
		return code
	}
	head := fmt.Sprintf("harness-gate close: %s (%s, %s)", f.stage, closure.RecordPath(f.story, f.stage), f.mode())
	rec, err := readRecordAt(f)
	if err != nil {
		printBlockers(stdout, head+": REFUSED "+closure.NotYetClosed, []string{err.Error()})
		return 1
	}
	if refusal, blockers := f.s.Close(x, f.stage, rec, f.built); refusal != "" {
		printBlockers(stdout, head+": REFUSED "+refusal, blockers)
		return 1
	}
	fmt.Fprintf(stdout, "%s: closed; every ready condition holds and the status line reads %q\n", head, rec.StatusValue)
	return 0
}

// runRecords is "harness-gate records": the shape of every stage record of the story
// and of its closure record (e2e-scenarios.md Phase 1, "Records and index skeleton").
func runRecords(args []string, stdout, stderr io.Writer) int {
	var story string
	root, ok := subcommandFlags("records", args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&story, "story", closure.DefaultStory, "the story whose evidence records are checked")
	})
	if !ok {
		return 2
	}
	s, known := closure.Stories[story]
	if !known {
		fmt.Fprintf(stderr, "harness-gate records: unknown story %q\n", story)
		return 2
	}
	x, code := readIndexFor("records", root, stderr)
	if x == nil {
		return code
	}
	abs, _ := filepath.Abs(root)
	if problems := closure.CheckRecords(abs, s, x); len(problems) > 0 {
		printBlockers(stdout, "harness-gate records: "+story+": FAILED", problems)
		return 1
	}
	fmt.Fprintf(stdout, "harness-gate records: %s: %d stage records and %s hold their shape\n", story, len(s.Stages), closure.ClosureRecordPath(story))
	return 0
}

func readIndexFor(name, root string, stderr io.Writer) (*acceptance.Index, int) {
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate %s: %v\n", name, err)
		return nil, 2
	}
	x, err := acceptance.ReadIndex(abs)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate %s: %v\n", name, err)
		return nil, 2
	}
	return x, 0
}

func readRecordAt(f closeFlags) (*closure.Record, error) {
	abs, err := filepath.Abs(f.root)
	if err != nil {
		return nil, err
	}
	return closure.ReadRecord(abs, f.story, f.stage)
}

func printBlockers(w io.Writer, head string, blockers []string) {
	noun := "blockers"
	if len(blockers) == 1 {
		noun = "blocker"
	}
	fmt.Fprintf(w, "%s, %d %s:\n", head, len(blockers), noun)
	for _, b := range blockers {
		fmt.Fprintf(w, "  - %s\n", b)
	}
}
