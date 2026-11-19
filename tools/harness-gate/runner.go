package main

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Gate is one step of the fixed gate list (QG-1). Run writes what it did to w and
// returns an error when the gate fails. There is no "skipped" outcome: a gate that
// cannot run fails (QG-4).
type Gate struct {
	Name  string // printed on failure, e.g. "gofmt"
	Title string // what the gate checks
	Run   func(ctx context.Context, w io.Writer) error
}

// Result is the outcome of one gate that ran.
type Result struct {
	Name    string
	Err     error
	Elapsed time.Duration
}

// Passed reports whether the gate passed.
func (r Result) Passed() bool { return r.Err == nil }

// Run executes gates in order and stops at the first failure: no later gate is
// started. It prints every gate as it starts and ends, and on failure a final line
// naming the failed gate. It returns the results of the gates that ran.
func Run(ctx context.Context, w io.Writer, gates []Gate) []Result {
	var results []Result
	for i, g := range gates {
		fmt.Fprintf(w, "harness-gate: gate %d/%d %s -- %s\n", i+1, len(gates), g.Name, g.Title)
		start := time.Now()
		err := ctx.Err()
		if err == nil {
			err = g.Run(ctx, w)
		}
		r := Result{Name: g.Name, Err: err, Elapsed: time.Since(start).Round(time.Millisecond)}
		results = append(results, r)
		if err != nil {
			fmt.Fprintf(w, "harness-gate: FAIL %s (%s)\n%v\n", g.Name, r.Elapsed, err)
			fmt.Fprintf(w, "harness-gate: FAILED at gate %s\n", g.Name)
			return results
		}
		fmt.Fprintf(w, "harness-gate: PASS %s (%s)\n", g.Name, r.Elapsed)
	}
	fmt.Fprintf(w, "harness-gate: PASSED all %d gates\n", len(gates))
	return results
}

// ExitCode is the process exit status for results: 0 when every gate passed, 1
// when one failed.
func ExitCode(results []Result) int {
	for _, r := range results {
		if !r.Passed() {
			return 1
		}
	}
	return 0
}
