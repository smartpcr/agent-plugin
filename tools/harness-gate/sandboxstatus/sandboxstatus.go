// Package sandboxstatus reads the text report of `dsh sandbox status` (C37,
// src/harness/internal/cli/sandbox.go) and decides the guarded preflight of the
// WSL lane (implementation-plan Stage 6.10, architecture.md I-8,
// service:linux-sandbox-bwrap): only backend bwrap with state Full passes;
// every other report, and one that cannot be read, fails. Nothing is skipped.
// Preflight decides the same for each sandbox service an S18 variant names
// (Stage 8.1).
package sandboxstatus

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The report values the guarded preflight requires.
const (
	WantBackend = "bwrap"
	WantState   = "Full"
)

// Status is the head of a `dsh sandbox status` report: the state (Full,
// Partial, Unconfined) and the backend a workspace-write command gets.
type Status struct {
	State   string
	Backend string
}

func (s Status) String() string { return fmt.Sprintf("backend %s, state %s", s.Backend, s.State) }

// Parse reads the "state:" and "backend:" lines of a text report. Each must
// appear exactly once with a non-empty value; CRLF line ends are accepted.
func Parse(report string) (Status, error) {
	var s Status
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(report, "\r\n", "\n"), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || (key != "state" && key != "backend") {
			continue
		}
		if seen[key] {
			return Status{}, fmt.Errorf("not a dsh sandbox status report: it has two %q lines", key)
		}
		seen[key] = true
		value = strings.TrimSpace(value)
		if value == "" {
			return Status{}, fmt.Errorf("not a dsh sandbox status report: its %q line is empty", key)
		}
		if key == "state" {
			s.State = value
		} else {
			s.Backend = value
		}
	}
	var errs []error
	for _, key := range []string{"state", "backend"} {
		if !seen[key] {
			errs = append(errs, fmt.Errorf("it has no %q line", key))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return Status{}, fmt.Errorf("not a dsh sandbox status report: %w", err)
	}
	return s, nil
}

// Expectation is the preflight of one provided sandbox service (architecture.md
// provided services table; e2e-scenarios.md Phase 9 pre-test bootstrap): the
// backend and states a machine of that service must report. Text is the
// preflight as evidence/variants.md writes it.
type Expectation struct {
	Service string
	Backend string   // "" accepts any backend
	States  []string // one of these
	Text    string
	// NamesMissing requires the report's "missing:" line to name the effects.
	NamesMissing bool
}

func (e Expectation) String() string {
	var want []string
	if e.Backend != "" {
		want = append(want, "backend "+e.Backend)
	}
	want = append(want, "state "+strings.Join(e.States, " or "))
	if e.NamesMissing {
		want = append(want, "the missing effects named")
	}
	return strings.Join(want, ", ")
}

// Expectations are the sandbox services an S18 variant can name.
var Expectations = []Expectation{
	{Service: "service:linux-sandbox-bwrap", Backend: WantBackend, States: []string{WantState}, Text: "bwrap Full"},
	{Service: "service:linux-landlock-only", Backend: "landlock", States: []string{"Full", "Partial"}, Text: "landlock"},
	{Service: "service:linux-unconfined", States: []string{"Unconfined"}, Text: "Unconfined", NamesMissing: true},
}

// ExpectationOf returns the preflight of service.
func ExpectationOf(service string) (Expectation, bool) {
	for _, e := range Expectations {
		if e.Service == service {
			return e, true
		}
	}
	return Expectation{}, false
}

// Preflight parses report and returns its status with nil when it is what
// service's machine must report; otherwise an error naming what was read and
// the expected state. An unknown service is an error, never a pass.
func Preflight(service, report string) (Status, error) {
	e, ok := ExpectationOf(service)
	if !ok {
		known := make([]string, 0, len(Expectations))
		for _, x := range Expectations {
			known = append(known, x.Service)
		}
		return Status{}, fmt.Errorf("%q has no sandbox preflight; known: %s", service, strings.Join(known, ", "))
	}
	s, err := Parse(report)
	if err != nil {
		return s, err
	}
	ok = (e.Backend == "" || s.Backend == e.Backend) && slices.Contains(e.States, s.State)
	if ok && e.NamesMissing {
		m := field(report, "missing")
		ok = m != "" && m != "none"
	}
	if !ok {
		return s, fmt.Errorf("%s preflight %q failed: read %s; want %s", e.Service, e.Text, s, e)
	}
	return s, nil
}

// field returns the trimmed value of the first "key:" line of report.
func field(report, key string) string {
	for _, line := range strings.Split(strings.ReplaceAll(report, "\r\n", "\n"), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && k == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Guarded parses report and returns its status with nil when it is backend
// bwrap, state Full, the only status a guarded run accepts; otherwise an error
// naming what was read and what is required.
func Guarded(report string) (Status, error) {
	s, err := Parse(report)
	if err != nil {
		return s, err
	}
	if s.Backend != WantBackend || s.State != WantState {
		return s, fmt.Errorf("%s; a guarded run needs backend %s, state %s (service:linux-sandbox-bwrap, architecture.md I-8)", s, WantBackend, WantState)
	}
	return s, nil
}
