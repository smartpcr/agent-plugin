// Package sandboxstatus reads the text report of `dsh sandbox status` (C37,
// src/harness/internal/cli/sandbox.go) and decides the guarded preflight of the
// WSL lane (implementation-plan Stage 6.10, architecture.md I-8,
// service:linux-sandbox-bwrap): only backend bwrap with state Full passes;
// every other report, and one that cannot be read, fails. Nothing is skipped.
package sandboxstatus

import (
	"errors"
	"fmt"
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
