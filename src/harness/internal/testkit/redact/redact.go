// Package redact is the test kit's identity filter for the lines a manual check
// prints when it runs on the user's own signed-in Claude Code or GitHub Copilot
// CLI (gap-closure plan 16.5.6 for DH-S-9, 17.4.2 for the M1 check of DH-S-11).
// Those lines are pasted into committed evidence, so nothing that names the
// signed-in person or could act as their credential may survive in them.
//
// A Filter masks five things, each replaced by Mask:
//   - the token shapes (TokenShape): Anthropic keys and OAuth tokens (sk-ant-),
//     GitHub tokens (gho_, ghu_, ghp_, ghs_, ghr_, github_pat_) and bearer
//     credentials;
//   - the value of an Authorization, Proxy-Authorization, Cookie or Set-Cookie
//     header, in a header line or a JSON field;
//   - an email address;
//   - a path whose last part names a credential or token file;
//   - the literal identity values the Filter was given (the account name, the
//     machine's user name).
//
// After masking, Apply looks again with looser detectors (a bare token prefix
// counts) and names every pattern that still matches. The caller fails its
// test on any such name and withholds the line, so a pattern the masks cannot
// place is reported, never printed.
package redact

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Mask replaces every masked value: the harness's one redaction mark.
const Mask = herr.Redacted

// The pattern names Apply and Detect report.
const (
	PatternToken     = "token"
	PatternPrefix    = "token-prefix"
	PatternHeader    = "header"
	PatternEmail     = "email"
	PatternTokenPath = "token-path"
	PatternIdentity  = "identity"
)

// TokenShape matches the token shapes a signed-in client's output could carry
// (plan 16.5.6; R-11 re-scoped): Anthropic keys and OAuth tokens (sk-ant-),
// GitHub tokens (gho_, ghu_, ghp_, ghs_, ghr_, github_pat_) and bearer
// credentials. Each shape needs a value after its prefix; a bare prefix is left
// to the detector.
var TokenShape = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}|sk-ant-[A-Za-z0-9_-]+|\bgh[oupsr]_[A-Za-z0-9_]+|\bgithub_pat_[A-Za-z0-9_]+`)

var (
	// tokenPrefix is the detector of a token prefix anywhere, with or without a
	// value or a word boundary.
	tokenPrefix = regexp.MustCompile(`(?i)sk-ant-|gh[oupsr]_|github_pat_`)
	// header is a credential-bearing header and its value, up to the end of the
	// line or the closing quote of a JSON string: group 1 the name, group 2 the
	// separator, group 3 the value.
	header = regexp.MustCompile(`(?i)\b((?:proxy-)?authorization|set-cookie|cookie)("?\s*[:=]\s*"?)([^"\r\n]+)`)
	email  = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)
	// tokenPath is a path (at least one separator; a drive, a home "~" or a
	// JSON-escaped separator allowed) whose last part names a credential or
	// token file: the Claude Code credentials file, the GitHub CLI's hosts file,
	// the Copilot apps and token files, .netrc.
	tokenPath = regexp.MustCompile(`(?i)(?:[A-Za-z]:)?[\\/]*(?:[^\s"'\\/:*?<>|,]+[\\/]+)+[^\s"'\\/:*?<>|,]*(?:credential|token|oauth|auth\.json|hosts\.ya?ml|hosts\.json|apps\.json|\.netrc)[^\s"'\\/:*?<>|,]*`)
)

// Filter masks the patterns of the package and the identity values it holds.
// The zero value masks the patterns only.
type Filter struct {
	identity []*regexp.Regexp
}

// New is a Filter that also masks each identity value, without regard to case
// and as a whole word where it starts or ends with a letter or digit. An empty
// value or one of a single character is ignored (it would mask most of a line
// and identifies no one), and so is a part of Mask itself, which the filter
// would otherwise find again in every masked line.
func New(identity ...string) *Filter {
	f := &Filter{}
	seen := map[string]bool{}
	for _, v := range identity {
		v = strings.TrimSpace(v)
		key := strings.ToLower(v)
		if len([]rune(v)) < 2 || seen[key] || strings.Contains(Mask, key) {
			continue
		}
		seen[key] = true
		expr := regexp.QuoteMeta(v)
		r := []rune(v)
		if word(r[0]) {
			expr = `\b` + expr
		}
		if word(r[len(r)-1]) {
			expr += `\b`
		}
		f.identity = append(f.identity, regexp.MustCompile(`(?i)`+expr))
	}
	return f
}

func word(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// Apply is text with every pattern masked, and the names of the patterns
// Detect still finds in the result, sorted; none when the text is clean.
func (f *Filter) Apply(text string) (string, []string) {
	out := header.ReplaceAllString(text, "${1}${2}"+Mask)
	out = TokenShape.ReplaceAllString(out, Mask)
	out = email.ReplaceAllString(out, Mask)
	out = tokenPath.ReplaceAllString(out, Mask)
	for _, re := range f.identity {
		out = re.ReplaceAllString(out, Mask)
	}
	return out, f.Detect(out)
}

// Detect names the patterns text holds, sorted, without masking anything. A
// header counts only when its value is not Mask.
func (f *Filter) Detect(text string) []string {
	var found []string
	add := func(name string, hit bool) {
		if hit && !slices.Contains(found, name) {
			found = append(found, name)
		}
	}
	add(PatternToken, TokenShape.MatchString(text))
	add(PatternPrefix, tokenPrefix.MatchString(text))
	add(PatternEmail, email.MatchString(text))
	add(PatternTokenPath, tokenPath.MatchString(text))
	for _, m := range header.FindAllStringSubmatch(text, -1) {
		add(PatternHeader, strings.TrimSpace(m[3]) != Mask)
	}
	for _, re := range f.identity {
		add(PatternIdentity, re.MatchString(text))
	}
	slices.Sort(found)
	return found
}
