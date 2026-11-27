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
