package sandboxstatus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Scenario "Preflight parses status" (implementation-plan Stage 6.10): of the
// recorded `dsh sandbox status` reports bwrap Full, landlock Partial and
// Unconfined, only the first passes the guarded preflight (architecture.md I-8).
func TestGuardedAcceptsOnlyBwrapFull(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		want    Status
		pass    bool
	}{
		{"bwrap-full.txt", Status{State: "Full", Backend: "bwrap"}, true},
		{"landlock-partial.txt", Status{State: "Partial", Backend: "landlock"}, false},
		{"unconfined.txt", Status{State: "Unconfined", Backend: "none"}, false},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("testdata", tc.fixture))
			if err != nil {
				t.Fatal(err)
			}
			got, err := Guarded(string(b))
			if got != tc.want {
				t.Errorf("status = %+v, want %+v", got, tc.want)
			}
			if pass := err == nil; pass != tc.pass {
				t.Fatalf("passed = %v, want %v (err %v)", pass, tc.pass, err)
			}
			if err != nil && !strings.Contains(err.Error(), "needs backend bwrap, state Full") {
				t.Errorf("the failure does not name what is required: %v", err)
			}
		})
	}
}

// A report that is bwrap but not Full, Full but not bwrap, or not a report at
// all fails too: the preflight never skips.
func TestGuardedFailsEverythingElse(t *testing.T) {
	for name, report := range map[string]string{
		"bwrap Partial":    "state:   Partial\nbackend: bwrap\n",
		"landlock Full":    "state:   Full\nbackend: landlock\n",
		"lower-case full":  "state:   full\nbackend: bwrap\n",
		"empty":            "",
		"no backend line":  "state:   Full\n",
		"no state line":    "backend: bwrap\n",
		"empty state":      "state:\nbackend: bwrap\n",
		"two state lines":  "state:   Full\nstate:   Partial\nbackend: bwrap\n",
		"an error message": "dsh: sandbox status: home is locked\n",
	} {
		t.Run(name, func(t *testing.T) {
			if s, err := Guarded(report); err == nil {
				t.Errorf("passed with %s", s)
			}
		})
	}
}

// CRLF line ends, as a report piped through a Windows console has, read the same.
func TestParseAcceptsCRLF(t *testing.T) {
	s, err := Guarded("state:   Full\r\nbackend: bwrap\r\nbackends:\r\n  bwrap: available\r\n")
	if err != nil || s != (Status{State: "Full", Backend: "bwrap"}) {
		t.Fatalf("Guarded = %+v, %v", s, err)
	}
}

// Scenario "Preflight refuses a wrong machine" (implementation-plan Stage 8.1;
// e2e-scenarios.md Phase 9): each service's preflight passes only the recorded
// report of its own machine. service:linux-landlock-only fails a "bwrap Full"
// report and service:linux-unconfined a "landlock" one, each naming the expected
// state.
func TestPreflightRefusesAWrongMachine(t *testing.T) {
	fixtures := map[string]string{}
	for _, f := range []string{"bwrap-full.txt", "landlock-partial.txt", "unconfined.txt"} {
		b, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		fixtures[f] = string(b)
	}
	own := map[string]string{
		"service:linux-sandbox-bwrap": "bwrap-full.txt",
		"service:linux-landlock-only": "landlock-partial.txt",
		"service:linux-unconfined":    "unconfined.txt",
	}
	want := map[string]string{
		"service:linux-sandbox-bwrap": "want backend bwrap, state Full",
		"service:linux-landlock-only": "want backend landlock, state Full or Partial",
		"service:linux-unconfined":    "want state Unconfined, the missing effects named",
	}
	if len(own) != len(Expectations) {
		t.Fatalf("%d expectations, %d covered here", len(Expectations), len(own))
	}
	for _, e := range Expectations {
		for f, report := range fixtures {
			t.Run(e.Service+"/"+f, func(t *testing.T) {
				_, err := Preflight(e.Service, report)
				if f == own[e.Service] {
					if err != nil {
						t.Fatalf("its own machine failed: %v", err)
					}
					return
				}
				if err == nil {
					t.Fatalf("passed %s", f)
				}
				if !strings.Contains(err.Error(), want[e.Service]) || !strings.Contains(err.Error(), e.Service) {
					t.Errorf("the failure does not name the service and expected state %q: %v", want[e.Service], err)
				}
			})
		}
	}
}

// The edges: an Unconfined report naming no missing effect, a landlock state
// other than Full or Partial, an unknown service and a report that is not one.
func TestPreflightEdges(t *testing.T) {
	for name, tc := range map[string]struct{ service, report string }{
		"unconfined, missing none":  {"service:linux-unconfined", "state:   Unconfined\nbackend: none\nmissing: none\n"},
		"unconfined, no missing":    {"service:linux-unconfined", "state:   Unconfined\nbackend: none\n"},
		"landlock Unconfined":       {"service:linux-landlock-only", "state:   Unconfined\nbackend: landlock\n"},
		"unknown service":           {"service:windows-host", "state:   Full\nbackend: bwrap\n"},
		"landlock, not a report":    {"service:linux-landlock-only", "dsh: no such command\n"},
		"bwrap, landlock Full read": {"service:linux-sandbox-bwrap", "state:   Full\nbackend: landlock\n"},
	} {
		t.Run(name, func(t *testing.T) {
			if s, err := Preflight(tc.service, tc.report); err == nil {
				t.Errorf("passed with %s", s)
			}
		})
	}
	if _, err := Preflight("service:linux-landlock-only", "state:   Full\r\nbackend: landlock\r\n"); err != nil {
		t.Errorf("landlock Full failed: %v", err)
	}
}
