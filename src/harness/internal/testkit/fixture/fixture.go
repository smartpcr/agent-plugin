// Package fixture is the recorded-session kit of the replay tests (DH-D-UTIL-7,
// C54): the scenario manifest, the fixed table of typed tokens that normalises a
// run's export, requests and paths, the guard that a committed fixture is a fixed
// point of that table, and workspace tree copy and comparison.
//
// Normalisation is a fixed table, applied after export: each kind of value that
// differs between two runs of the same scenario (a session or message id, a
// clock reading, a duration, the scratch home and workspace) is replaced by a
// token naming its kind and, for an identity, its order of first appearance, so
// equal ids get equal tokens and two runs compare byte for byte. "Fix fixtures, not
// normalisers": a value the table does not name stays as it is, and a difference
// in it fails the comparison.
package fixture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Manifest is a scenario's scenario.json.
type Manifest struct {
	// Profile is the profile the run starts; "headless" is the one driver built.
	Profile string `json:"profile"`
	// Input is the prompt the driver sends.
	Input string `json:"input"`
	// Recording is "authored" (a written script, refreshed keylessly) or "live".
	Recording string `json:"recording"`
	// Route and Model are the recorded route and model, registered as a stub.
	Route string `json:"route"`
	Model string `json:"model,omitempty"`
	// OS lists the operating systems the scenario runs on; empty is every one.
	OS []string `json:"os,omitempty"`
}

// ReadManifest reads and checks dir/scenario.json.
func ReadManifest(dir string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(dir, "scenario.json"))
	if err != nil {
		return m, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("%s: %w", dir, err)
	}
	switch {
	case m.Profile != "headless":
		return m, fmt.Errorf("%s: profile %q: only headless is driven", dir, m.Profile)
	case m.Input == "" || m.Route == "":
		return m, fmt.Errorf("%s: scenario.json needs input and route", dir)
	case m.Recording != "authored" && m.Recording != "live":
		return m, fmt.Errorf("%s: recording %q is neither authored nor live", dir, m.Recording)
	}
	return m, nil
}

// kind is one row of the token table: values matching re become <name:n>.
type kind struct {
	name string
	re   *regexp.Regexp
	// group, when 1, says submatch 1 is the value and the rest of the match is
	// kept.
	group int
	// plain kinds (clock readings, durations) get the one token <name>: whether
	// two of them are equal is chance, not identity, so it is not recorded.
	plain bool
}

// table is the fixed table of typed tokens. Its order matters: a longer, more
// specific form is replaced before a shorter one could match inside it.
var table = []kind{
	{name: "session", re: regexp.MustCompile(`ses_[0-9A-Za-z]{8,}`)},
	{name: "msg", re: regexp.MustCompile(`msg_[0-9a-f]{16,}`)},
	{name: "in", re: regexp.MustCompile(`in_[0-9a-f]{16,}`)},
	{name: "call", re: regexp.MustCompile(`call_[0-9A-Za-z]{8,}`)},
	{name: "time", plain: true, re: regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]\d\d:\d\d)`)},
	// Clock readings (epoch milliseconds or more) and durations, by the JSON key
	// that holds them, plain or inside an escaped payload string.
	{name: "time", plain: true, re: regexp.MustCompile(`\\?"(?:time|start|end|first_token|[a-z_]*_at)\\?":(-?\d{10,})`), group: 1},
	{name: "ms", plain: true, re: regexp.MustCompile(`\\?"[a-z_]*(?:_ms|_ns)\\?":(\d+)`), group: 1},
	// A request digest covers the run's ids, so it differs between runs; the
	// requests themselves are compared normalised instead.
	{name: "digest", re: regexp.MustCompile(`\\?"request_digest\\?":\\?"(sha256:[0-9a-f]{64})\\?"`), group: 1},
	{name: "sha256", re: regexp.MustCompile(`\\?"sha256\\?":\\?"([0-9a-f]{64})\\?"`), group: 1},
}

// Normalizer replaces the table's values, and the literal paths Paths names,
// with typed tokens.
type Normalizer struct {
	// Paths maps a literal (a scratch directory) to its token name; the longest
	// literal is replaced first, in its plain, JSON-escaped and slash forms.
	Paths map[string]string

	seen map[string]map[string]int // kind -> value -> n
}

// Normalize returns b with every value of the table replaced. Identity tokens
// are numbered per kind in order of first appearance across every call on n, so
// a value seen in the log and again in a request gets one token.
func (n *Normalizer) Normalize(b []byte) []byte {
	if n.seen == nil {
		n.seen = map[string]map[string]int{}
	}
	s := string(b)
	paths := make([]string, 0, len(n.Paths))
	for p := range n.Paths {
		paths = append(paths, p)
	}
	slices.SortFunc(paths, func(a, b string) int { return len(b) - len(a) })
	for _, p := range paths {
		tok := "<" + n.Paths[p] + ">"
		once := escape(p)
		for _, form := range []string{escape(once), once, filepath.ToSlash(p), p} {
			if form != "" {
				s = strings.ReplaceAll(s, form, tok)
			}
		}
	}
	for _, k := range table {
		s = k.re.ReplaceAllStringFunc(s, func(m string) string {
			if k.group == 0 {
				return n.token(k, m)
			}
			sub := k.re.FindStringSubmatchIndex(m)
			tok := n.token(k, m[sub[2]:sub[3]])
			if m[sub[2]-1] == ':' {
				// A number becomes a string, in the quoting of its context, so
				// the line stays JSON.
				q := `"`
				if strings.HasPrefix(m, `\"`) {
					q = `\"`
				}
				tok = q + tok + q
			}
			return m[:sub[2]] + tok + m[sub[3]:]
		})
	}
	return []byte(s)
}

// escape is s as it appears inside a JSON string.
func escape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func (n *Normalizer) token(k kind, v string) string {
	if k.plain {
		return "<" + k.name + ">"
	}
	kind := k.name
	m := n.seen[kind]
	if m == nil {
		m = map[string]int{}
		n.seen[kind] = m
	}
	i, ok := m[v]
	if !ok {
		i = len(m) + 1
		m[v] = i
	}
	return fmt.Sprintf("<%s:%d>", kind, i)
}

// Guard fails when b, a committed fixture, is not a fixed point of the table: a
// value the table would replace is still in it (an unscrubbed id, clock reading
// or digest), or it holds one of the literal paths.
func Guard(b []byte, paths ...string) error {
	n := &Normalizer{Paths: map[string]string{}}
	for _, p := range paths {
		n.Paths[p] = "path"
	}
	if got := n.Normalize(b); !bytes.Equal(got, b) {
		at := firstDiff(got, b)
		return fmt.Errorf("fixture: not normalised; the first difference is at byte %d: %q", at, b[max(0, at-40):min(len(b), at+40)])
	}
	return nil
}

func firstDiff(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

// Diff is empty when got and want are byte-identical, else a report of the
// first differing line.
func Diff(name string, got, want []byte) string {
	if bytes.Equal(got, want) {
		return ""
	}
	g, w := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
	for i := range max(len(g), len(w)) {
		gl, wl := "(no line)", "(no line)"
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return fmt.Sprintf("%s: line %d differs:\n got: %s\nwant: %s", name, i+1, gl, wl)
		}
	}
	return name + ": differs"
}

// Tree reads every regular file under dir, by slash-separated relative path.
// Anything but a directory or a regular file fails: the oracle is plain files.
func Tree(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("fixture: %s is not a regular file", p)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		out[filepath.ToSlash(rel)] = b
		return err
	})
	return out, err
}

// CompareTrees fails, naming each, every file that is missing, extra or different
// between the trees under got and want.
func CompareTrees(got, want string) error {
	g, err := Tree(got)
	if err != nil {
		return err
	}
	w, err := Tree(want)
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range sortedKeys(w) {
		gb, ok := g[name]
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("%s: missing", name))
		case !bytes.Equal(gb, w[name]):
			errs = append(errs, errors.New(Diff(name, gb, w[name])))
		}
	}
	for _, name := range sortedKeys(g) {
		if _, ok := w[name]; !ok {
			errs = append(errs, fmt.Errorf("%s: not expected", name))
		}
	}
	return errors.Join(errs...)
}

func sortedKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// CopyTree copies the regular files under src into dst, creating directories.
func CopyTree(src, dst string) error {
	files, err := Tree(src)
	if err != nil {
		return err
	}
	for name, b := range files {
		p := filepath.Join(dst, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return err
		}
	}
	return nil
}
