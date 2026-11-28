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

// Expectation is one preflight of a provided sandbox service (architecture.md
// provided services table; e2e-scenarios.md Phase 9 pre-test bootstrap): the
// backend and states a machine must report, and the effects its "missing:"
// line must or must not name. Text is the preflight as evidence/variants.md
// writes it in the Preflight column; a service with machines that differ in
// what they govern has one expectation per machine kind, told apart by Text.
type Expectation struct {
	Service string
	Backend string   // "" accepts any backend
	States  []string // one of these
	Text    string
	// NamesMissing requires the report's "missing:" line to name the effects.
	NamesMissing bool
	// Missing are effects the "missing:" line must name; Governed are effects
	// it must not name.
	Missing, Governed []string
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
	if len(e.Missing) > 0 {
		want = append(want, "missing "+strings.Join(e.Missing, ", "))
	}
	if len(e.Governed) > 0 {
		want = append(want, "not missing "+strings.Join(e.Governed, ", "))
	}
	return strings.Join(want, ", ")
}

// Expectations are the sandbox preflights an S18 variant can name. The two
// Landlock ones tell the kernels apart by what the report says is missing
// (src/harness/internal/world/sandbox/launch/landlock.go, landlockGates): below
// the reference interface version 5 the rule set cannot govern fs-write, so an
// ABI 1-4 kernel is Partial naming fs-write, and an ABI >= 5 kernel governs
// fs-write and harness-home and names neither.
var Expectations = []Expectation{
	{Service: "service:linux-sandbox-bwrap", Backend: WantBackend, States: []string{WantState}, Text: "bwrap Full"},
	{Service: "service:linux-landlock-only", Backend: "landlock", States: []string{"Full", "Partial"}, Text: "landlock ABI >= 5",
		Governed: []string{"fs-write", "harness-home"}},
	{Service: "service:linux-landlock-only", Backend: "landlock", States: []string{"Partial"}, Text: "landlock ABI 1-4",
		Missing: []string{"fs-write"}},
	{Service: "service:linux-unconfined", States: []string{"Unconfined"}, Text: "Unconfined", NamesMissing: true},
}

// ExpectationsOf returns the preflights of service, in Expectations order.
func ExpectationsOf(service string) []Expectation {
	var es []Expectation
	for _, e := range Expectations {
		if e.Service == service {
			es = append(es, e)
		}
	}
	return es
}

// ExpectationOf returns the preflight of service whose Text is text.
func ExpectationOf(service, text string) (Expectation, bool) {
	for _, e := range ExpectationsOf(service) {
		if e.Text == text {
			return e, true
		}
	}
	return Expectation{}, false
}

// Texts returns the Text of each expectation, quoted, joined for a message.
func Texts(es []Expectation) string {
	q := make([]string, 0, len(es))
	for _, e := range es {
		q = append(q, fmt.Sprintf("%q", e.Text))
	}
	return strings.Join(q, ", ")
}

// Preflight parses report and returns its status with nil when it is what
// the machine of service's preflight text must report; otherwise an error
// naming what was read and the expected state. text may be empty only when
// service has one preflight. An unknown service or text is an error, never a
// pass.
func Preflight(service, text, report string) (Status, error) {
	es := ExpectationsOf(service)
	if len(es) == 0 {
		known := make([]string, 0, len(Expectations))
		for _, x := range Expectations {
			if !slices.Contains(known, x.Service) {
				known = append(known, x.Service)
			}
		}
		return Status{}, fmt.Errorf("%q has no sandbox preflight; known: %s", service, strings.Join(known, ", "))
	}
	var e Expectation
	switch {
	case text == "" && len(es) == 1:
		e = es[0]
	case text == "":
		return Status{}, fmt.Errorf("%s has more than one preflight (%s); name the variant's", service, Texts(es))
	default:
		var ok bool
		if e, ok = ExpectationOf(service, text); !ok {
			return Status{}, fmt.Errorf("%s has no preflight %q; known: %s", service, text, Texts(es))
		}
	}
	s, err := Parse(report)
	if err != nil {
		return s, err
	}
	ok := (e.Backend == "" || s.Backend == e.Backend) && slices.Contains(e.States, s.State)
	m := field(report, "missing")
	if ok && e.NamesMissing {
		ok = m != "" && m != "none"
	}
	missing := effects(m)
	for _, x := range e.Missing {
		ok = ok && slices.Contains(missing, x)
	}
	for _, x := range e.Governed {
		ok = ok && !slices.Contains(missing, x)
	}
	if !ok {
		return s, fmt.Errorf("%s preflight %q failed: read %s, missing %s; want %s", e.Service, e.Text, s, orNone(m), e)
	}
	return s, nil
}

// effects splits a "missing:" value into its effects; "none" and "" are none.
func effects(v string) []string {
	if v == "" || v == "none" {
		return nil
	}
	var out []string
	for _, x := range strings.Split(v, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func orNone(v string) string {
	if v == "" {
		return "none"
	}
	return v
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
