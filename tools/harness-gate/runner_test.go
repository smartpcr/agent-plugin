package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

// fakeGates returns n gates named g1..gn that record their calls; gate fail (1-based)
// returns an error.
func fakeGates(n, fail int, called *[]string) []Gate {
	var gates []Gate
	for i := 1; i <= n; i++ {
		name := "g" + string(rune('0'+i))
		gates = append(gates, Gate{Name: name, Title: "fake " + name, Run: func(context.Context, io.Writer) error {
			*called = append(*called, name)
			if i == fail {
				return errors.New("seeded failure")
			}
			return nil
		}})
	}
	return gates
}

func TestRunStopsAtFirstFailingGate(t *testing.T) {
	var called []string
	var out bytes.Buffer
	results := Run(context.Background(), &out, fakeGates(7, 3, &called))

	if want := []string{"g1", "g2", "g3"}; !slices.Equal(called, want) {
		t.Errorf("gates run = %v, want %v (gates four onward must never run)", called, want)
	}
	if got := ExitCode(results); got == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if !strings.Contains(out.String(), "harness-gate: FAILED at gate g3\n") {
		t.Errorf("stdout does not name the third gate:\n%s", out.String())
	}
	if strings.Contains(out.String(), "g4") {
		t.Errorf("stdout mentions a gate after the failure:\n%s", out.String())
	}
	if last := results[len(results)-1]; last.Name != "g3" || last.Passed() {
		t.Errorf("last result = %+v, want failed g3", last)
	}
}

func TestRunPassesEveryGateInOrder(t *testing.T) {
	var called []string
	var out bytes.Buffer
	results := Run(context.Background(), &out, fakeGates(7, 0, &called))
	if want := []string{"g1", "g2", "g3", "g4", "g5", "g6", "g7"}; !slices.Equal(called, want) {
		t.Errorf("gates run = %v, want %v", called, want)
	}
	if got := ExitCode(results); got != 0 {
		t.Errorf("exit code = %d, want 0", got)
	}
	if !strings.Contains(out.String(), "PASSED all 7 gates") {
		t.Errorf("stdout:\n%s", out.String())
	}
}

func TestRunDoesNotStartAGateAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var called []string
	results := Run(ctx, io.Discard, fakeGates(3, 0, &called))
	if len(called) != 0 || ExitCode(results) == 0 {
		t.Errorf("called %v, exit %d; want no gate run and a failure", called, ExitCode(results))
	}
}

// TestGateListIsFixed pins the order of QG-1: the 12 gates of M1-D-GATE-23 plus
// the policy-text gate of M2-C-6 (implementation-plan Stage 2.4): gofmt, vet + own
// analyzers, staticcheck, module hygiene, race/shuffle tests, coverage,
// harness-trace verify, scenario index, policy text, budgets, cross-compile,
// vulncheck and the gate's own tests (DH-T-TOOLCHAIN-9).
func TestGateListIsFixed(t *testing.T) {
	var names []string
	for _, g := range Gates(&Config{}) {
		names = append(names, g.Name)
	}
	want := []string{"gofmt", "vet", "staticcheck", "modules", "test", "coverage", "trace", "scenario-index", "policy-text", "budget", "cross-compile", "vulncheck", "self"}
	if !slices.Equal(names, want) {
		t.Errorf("gates = %v, want %v", names, want)
	}
}
