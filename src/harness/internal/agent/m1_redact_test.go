package agent_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/redact"
)

// The M1 check's identity filter (gap-closure plan 17.4.2, M1-G-LOOP-12). The
// check (m1_manual_test.go, build tag manual) runs on the user's own signed-in
// Claude Code or GitHub Copilot CLI, and the operator pastes what it prints
// into evidence/DH-S-11.md. Every line it prints passes m1Redact first. The
// filter is the test kit's (testkit/redact), shared with the DH-S-9 check's
// token shapes; this file and its self-check build without the manual tag, so
// the normal gate runs the self-check.

// m1Fatal is what m1Redact reports a surviving pattern to: the test's
// *testing.T, or a recorder in the self-check.
type m1Fatal interface {
	Helper()
	Fatalf(format string, args ...any)
}

// m1Filter masks the token shapes, credential headers, email addresses and
// token file paths of testkit/redact, and the identity values: the operator's
// signed-in account names and this machine's user name.
func m1Filter(identity ...string) *redact.Filter { return redact.New(identity...) }

// m1Redact is text with every line passed through f. A line in which a pattern
// survives the masks is never returned: m1Redact fails t, naming the patterns
// and the line's number but not its text.
func m1Redact(t m1Fatal, f *redact.Filter, text string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		out, survived := f.Apply(line)
		if len(survived) > 0 {
			t.Fatalf("m1Redact: line %d still matches %s after masking; the line is withheld", i+1, strings.Join(survived, ", "))
			return ""
		}
		lines[i] = out
	}
	return strings.Join(lines, "\n")
}

// m1Recorder stands in for *testing.T in the self-check.
type m1Recorder struct{ failures []string }

func (*m1Recorder) Helper() {}

func (r *m1Recorder) Fatalf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// TestM1RedactionCatchesIdentity is the plan 17.4 scenario "Redaction catches
// identity": printed lines holding a fake email, a bearer header and a gho_
// token, and the account name, a cookie, a token file path and the other token
// prefixes, come out with every value masked and the rest of each line kept;
// a pattern that survives the masks (a bare or glued token prefix) fails the
// test without printing the line.
func TestM1RedactionCatchesIdentity(t *testing.T) {
	f := m1Filter("octocat")
	secrets := []string{
		"octo.cat@example.com",
		"Bearer abcdefgh12345678",
		"gho_16C7e42F292c6912E7710c838347Ae178B4a",
		"octocat",
		"session=abc123; theme=dark",
		`C:\Users\octocat\.claude\.credentials.json`,
		"/home/octocat/.config/gh/hosts.yml",
		"sk-ant-oat01-AbCd", "ghu_abc", "ghp_abc", "ghs_abc", "ghr_abc", "github_pat_11AB_cd",
	}
	printed := strings.Join([]string{
		"M1 header: commit=0123abc route=claude-code client=claude-code version=2.1.312",
		"M1 bracketed log: account octo.cat@example.com signed in as Octocat",
		"Authorization: Bearer abcdefgh12345678",
		`{"cookie":"session=abc123; theme=dark","note":"gho_16C7e42F292c6912E7710c838347Ae178B4a"}`,
		`token file C:\Users\octocat\.claude\.credentials.json and /home/octocat/.config/gh/hosts.yml`,
		"shapes sk-ant-oat01-AbCd ghu_abc ghp_abc ghs_abc ghr_abc github_pat_11AB_cd",
		"M1 request 1: asOf=7 sent=sha256:00ff rebuilt=sha256:00ff equal=true session=8a1b2c3d…",
	}, "\n")
	rec := &m1Recorder{}
	out := m1Redact(rec, f, printed)
	if len(rec.failures) != 0 {
		t.Fatalf("m1Redact failed a line it can mask: %v", rec.failures)
	}
	for _, s := range secrets {
		if strings.Contains(strings.ToLower(out), strings.ToLower(s)) {
			t.Errorf("%q survived:\n%s", s, out)
		}
	}
	for _, kept := range []string{
		"M1 header: commit=0123abc route=claude-code client=claude-code version=2.1.312",
		"M1 bracketed log: account " + redact.Mask + " signed in as " + redact.Mask,
		"Authorization: " + redact.Mask,
		`"note":"` + redact.Mask + `"`,
		"M1 request 1: asOf=7 sent=sha256:00ff rebuilt=sha256:00ff equal=true session=8a1b2c3d…",
	} {
		if !strings.Contains(out, kept) {
			t.Errorf("the masked lines lack %q:\n%s", kept, out)
		}
	}
	if n := strings.Count(out, "\n"); n != strings.Count(printed, "\n") {
		t.Errorf("the masked text has %d line breaks, the printed %d", n, strings.Count(printed, "\n"))
	}

	// A pattern the masks cannot place fails the test and withholds the line.
	for _, line := range []string{"M1 final reply: a truncated gho_", "M1 cancel: glued xghp_abc", "ok\nbare sk-ant- prefix"} {
		rec := &m1Recorder{}
		if got := m1Redact(rec, f, line); got != "" || len(rec.failures) != 1 {
			t.Fatalf("m1Redact(%q) = %q with failures %v, want the line withheld and one failure", line, got, rec.failures)
		}
		if msg := rec.failures[0]; !strings.Contains(msg, redact.PatternPrefix) || strings.Contains(msg, "gho_") || strings.Contains(msg, "xghp") || strings.Contains(msg, "sk-ant-") {
			t.Errorf("the failure %q must name the pattern and not print the line", msg)
		}
	}
}
