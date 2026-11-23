package redact_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/redact"
)

// TestFilterMasksEachPattern: each pattern is masked wherever it sits in a
// line, the line around it is kept, and nothing survives.
func TestFilterMasksEachPattern(t *testing.T) {
	f := redact.New("octocat", "Mona Lisa")
	for _, tc := range []struct{ in, want string }{
		{"key sk-ant-api03-AbC_d-9 end", "key " + redact.Mask + " end"},
		{"oauth sk-ant-oat01-xyz", "oauth " + redact.Mask},
		{"gho_16C7e42F292c6912E7710c838347Ae178B4a", redact.Mask},
		{"a ghu_abc b ghp_abc c ghs_abc d ghr_abc", "a " + redact.Mask + " b " + redact.Mask + " c " + redact.Mask + " d " + redact.Mask},
		{"pat github_pat_11ABCDEFG_xyz.", "pat " + redact.Mask + "."},
		{"x Bearer eyJhbGciOi.J9.x-y", "x " + redact.Mask},
		{"Authorization: Bearer abcdefgh12345678", "Authorization: " + redact.Mask},
		{"proxy-authorization=Basic dXNlcjpwYXNz", "proxy-authorization=" + redact.Mask},
		{"Cookie: session=abc; user=x", "Cookie: " + redact.Mask},
		{`{"set-cookie":"a=b; Path=/","n":1}`, `{"set-cookie":"` + redact.Mask + `","n":1}`},
		{"mail octo.cat+dsh@example.co.uk now", "mail " + redact.Mask + " now"},
		{`at C:\Users\someone\.claude\.credentials.json then`, "at " + redact.Mask + " then"},
		{`json "C:\\Users\\someone\\.claude\\.credentials.json"`, `json "` + redact.Mask + `"`},
		{"/home/someone/.config/gh/hosts.yml", redact.Mask},
		{"~/.copilot/token.json", redact.Mask},
		{"/home/someone/.netrc", redact.Mask},
		{"signed in as OctoCat today", "signed in as " + redact.Mask + " today"},
		{"user mona lisa.", "user " + redact.Mask + "."},
	} {
		got, survived := f.Apply(tc.in)
		if got != tc.want || len(survived) != 0 {
			t.Errorf("Apply(%q) = %q, survived %v; want %q", tc.in, got, survived, tc.want)
		}
	}
}

// TestFilterKeepsWhatOnlyResemblesAPattern: text that only resembles a pattern
// is kept as it is.
func TestFilterKeepsWhatOnlyResemblesAPattern(t *testing.T) {
	f := redact.New("octocat")
	for _, keep := range []string{
		"session 8a1b2c3d-0000-4000-8000-000000000001",
		"the bearer of news", "high_ghost", "sk-other", "octocats", "tokenUsage=12",
		"--system-prompt-file {attempt}/system.txt", "internal/llm/provider/claudecode",
		"M1 request 1: asOf=7 sent=sha256:00ff rebuilt=sha256:00ff equal=true",
	} {
		if got, survived := f.Apply(keep); got != keep || len(survived) != 0 {
			t.Errorf("Apply(%q) = %q, survived %v; want it kept", keep, got, survived)
		}
	}
}

// TestFilterNamesASurvivingPattern: a pattern the masks cannot place -- a bare
// or glued token prefix -- is named by Apply, so the caller withholds the line.
func TestFilterNamesASurvivingPattern(t *testing.T) {
	var f redact.Filter
	for _, in := range []string{"a truncated gho_", "glued xghp_abc", "bare sk-ant- prefix", "GITHUB_PAT_"} {
		if _, survived := f.Apply(in); !slices.Equal(survived, []string{redact.PatternPrefix}) {
			t.Errorf("Apply(%q) survived %v, want [%s]", in, survived, redact.PatternPrefix)
		}
	}
}

// TestDetectNamesEveryPattern: Detect names each pattern once, sorted, and a
// masked header value is not a finding.
func TestDetectNamesEveryPattern(t *testing.T) {
	f := redact.New("octocat")
	line := "octocat a@b.io gho_x /h/u/.credentials Cookie: v=1"
	want := []string{redact.PatternEmail, redact.PatternHeader, redact.PatternIdentity, redact.PatternToken, redact.PatternPrefix, redact.PatternTokenPath}
	slices.Sort(want)
	if got := f.Detect(line); !slices.Equal(got, want) {
		t.Errorf("Detect = %v, want %v", got, want)
	}
	if got := f.Detect("Cookie: " + redact.Mask); len(got) != 0 {
		t.Errorf("a masked header value = %v", got)
	}
	if got := f.Detect("nothing here"); len(got) != 0 {
		t.Errorf("a clean line = %v", got)
	}
}

// TestNewIgnoresValuesThatIdentifyNoOne: an empty, one-character, repeated or
// mask-part identity value adds no pattern.
func TestNewIgnoresValuesThatIdentifyNoOne(t *testing.T) {
	f := redact.New("", " ", "x", "redacted", "Octocat", "octocat", "@dsh")
	got, survived := f.Apply("x redacted octocat @dsh")
	if want := "x redacted " + redact.Mask + " " + redact.Mask; got != want || len(survived) != 0 {
		t.Errorf("Apply = %q, survived %v; want %q", got, survived, want)
	}
	if strings.Contains(got, "octocat") {
		t.Error("the identity survived")
	}
}
