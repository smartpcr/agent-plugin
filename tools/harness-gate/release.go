package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// The release check (implementation-plan Stage 9.2; M2-DOD-7, NG-7; tech-spec sec. 4.6:
// nothing built in M2 is enabled for users and no build is published):
//
//	harness-gate release [--root dir]
//
// It reads committed files only:
//
//   - each release file (ReleaseFiles) equals its copy in ReleaseBasePath, the story's
//     base commit, whose git blob ID the golden records, the check recomputes and,
//     in a git checkout, compares with `git rev-parse <base>:<path>` (skipped, with
//     the reason, only outside a checkout or in a shallow clone lacking the base);
//   - no release file names dsh or src/harness, and no other workflow runs on a tag
//     push or a release event (its on: key read in string, sequence or mapping form by
//     tagTriggers) or runs goreleaser, so no dsh release job exists;
//   - every default profile's egress set in EgressGoldenPath (computed from the shipped
//     bundles by profile.Egress and held to it by TestDefaultProfilesSendNothing) is
//     empty, the headless and sdk profiles included.
//
// It exits 0 when all hold, 1 naming each problem, 2 on a usage error.

const (
	ReleaseBasePath  = "tools/harness-gate/testdata/release_base.golden"
	EgressGoldenPath = "src/harness/internal/boot/profile/testdata/egress.golden"
	workflowsDir     = ".github/workflows"
)

// ReleaseFiles are the files that publish a build.
var ReleaseFiles = []string{".github/workflows/release.yml", ".goreleaser.yaml"}

// sendNothingProfiles are the default profiles NG-7 names: they send nothing until a
// route is configured.
var sendNothingProfiles = []string{"headless", "sdk"}

var (
	dshPattern        = regexp.MustCompile(`(?i)\bdsh\b|src/harness|cmd/dsh`)
	onKeyPattern      = regexp.MustCompile(`^(on|"on"|'on'|true)\s*:(.*)$`)
	goreleaserPattern = regexp.MustCompile(`(?i)goreleaser`)
	egressLinePattern = regexp.MustCompile(`^([a-z][a-z0-9-]*): \[(.*)\]$`)
	baseHeaderPattern = regexp.MustCompile(`^# base: ([0-9a-f]{40})$`)
)

// releaseCopy is one file of the base golden.
type releaseCopy struct {
	path, blob string
	content    []byte
}

// parseReleaseBase reads the base golden: "#" header lines (one of them "# base:
// <sha>"), then for each file a "=== <path> <blob>" line and its content.
func parseReleaseBase(src []byte) (base string, copies []releaseCopy, err error) {
	lines := strings.SplitAfter(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	var cur *releaseCopy
	for i, l := range lines {
		if rest, ok := strings.CutPrefix(l, "=== "); ok {
			f := strings.Fields(rest)
			if len(f) != 2 {
				return "", nil, fmt.Errorf("%s:%d: want \"=== <path> <git blob ID>\", got %q", ReleaseBasePath, i+1, strings.TrimSpace(l))
			}
			copies = append(copies, releaseCopy{path: f[0], blob: f[1]})
			cur = &copies[len(copies)-1]
			continue
		}
		if cur != nil {
			cur.content = append(cur.content, l...)
			continue
		}
		if m := baseHeaderPattern.FindStringSubmatch(strings.TrimRight(l, "\n")); m != nil {
			base = m[1]
		} else if l != "" && !strings.HasPrefix(l, "#") {
			return "", nil, fmt.Errorf("%s:%d: header line %q is not a comment", ReleaseBasePath, i+1, strings.TrimSpace(l))
		}
	}
	if base == "" {
		return "", nil, fmt.Errorf("%s: no \"# base: <commit SHA>\" line", ReleaseBasePath)
	}
	return base, copies, nil
}

// gitBlobID is the git object ID of content as a blob.
func gitBlobID(content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// firstDifference is the first line where a and b differ, 1-based, with b's line.
func firstDifference(a, b []byte) (int, string) {
	al, bl := strings.Split(string(a), "\n"), strings.Split(string(b), "\n")
	for i := range max(len(al), len(bl)) {
		if i >= len(al) || i >= len(bl) || al[i] != bl[i] {
			if i < len(bl) {
				return i + 1, bl[i]
			}
			return i + 1, "(end of file)"
		}
	}
	return 0, ""
}

func readLF(root, rel string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")), err
}

// anchorReleaseBase ties the golden to real history: for each copy, the blob git holds
// for <base>:<path> must be the blob the golden records, so a golden rewritten together
// with its release file is refused. It returns how the anchor was checked and the
// problems found. A root that is not a git checkout's top level (a copied tree) and a
// shallow clone that lacks the base commit skip the anchor with the reason; any other
// clone lacking the base commit fails, so the anchor cannot be skipped silently.
func anchorReleaseBase(root, base string, copies []releaseCopy) (anchor string, problems []string) {
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}
	top, err := git("rev-parse", "--show-toplevel")
	if err != nil || !sameDir(top, root) {
		return "base commit not checked against git history: the root is not a git checkout's top level", nil
	}
	if _, err := git("cat-file", "-e", base+"^{commit}"); err != nil {
		if shallow, _ := git("rev-parse", "--is-shallow-repository"); shallow == "true" {
			return fmt.Sprintf("base commit not checked against git history: this shallow clone lacks %s (fetch it to check)", short(base)), nil
		}
		return "", []string{fmt.Sprintf("%s: base commit %s is not in this clone, so its copies cannot be tied to history (M2-DOD-7)", ReleaseBasePath, base)}
	}
	for _, c := range copies {
		got, err := git("rev-parse", "--verify", "--quiet", base+":"+c.path)
		if err != nil || got == "" {
			problems = append(problems, fmt.Sprintf("%s: base commit %s holds no %s (M2-DOD-7)", ReleaseBasePath, short(base), c.path))
		} else if got != c.blob {
			problems = append(problems, fmt.Sprintf("%s: %s is blob %s at the base commit %s, not %s as the golden records (M2-DOD-7)", ReleaseBasePath, c.path, got, short(base), c.blob))
		}
	}
	return "blob IDs checked against git history", problems
}

// CheckRelease is the release check over the tree at root; it returns the base commit
// the release files were compared with, the number of default profiles read, and the
// problems found.
func CheckRelease(root string) (base string, profiles int, problems []string) {
	base, profiles, _, problems = checkRelease(root)
	return base, profiles, problems
}

// checkRelease is CheckRelease that also returns how the golden was anchored to git
// history (anchorReleaseBase).
func checkRelease(root string) (base string, profiles int, anchor string, problems []string) {
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	golden, err := readLF(root, ReleaseBasePath)
	if err != nil {
		add("no base golden: %v", err)
	}
	base, copies, err := parseReleaseBase(golden)
	if golden != nil && err != nil {
		add("%v", err)
	}
	byPath := map[string]releaseCopy{}
	for _, c := range copies {
		if _, twice := byPath[c.path]; twice {
			add("%s: %s is given twice", ReleaseBasePath, c.path)
		}
		byPath[c.path] = c
		if !slices.Contains(ReleaseFiles, c.path) {
			add("%s: %s is not a release file (%s)", ReleaseBasePath, c.path, strings.Join(ReleaseFiles, ", "))
		}
		if got := gitBlobID(c.content); got != c.blob {
			add("%s: the copy of %s hashes to blob %s, not %s, its blob at the base commit", ReleaseBasePath, c.path, got, c.blob)
		}
	}
	if base != "" {
		var anchorProblems []string
		anchor, anchorProblems = anchorReleaseBase(root, base, copies)
		problems = append(problems, anchorProblems...)
	}
	for _, rel := range ReleaseFiles {
		cur, err := readLF(root, rel)
		if err != nil {
			add("release file %s: %v", rel, err)
			continue
		}
		if c, ok := byPath[rel]; !ok {
			add("%s has no copy of %s", ReleaseBasePath, rel)
		} else if !bytes.Equal(cur, c.content) {
			n, line := firstDifference(c.content, cur)
			add("%s differs from its copy at the base commit %s: line %d reads %q (M2-DOD-7)", rel, short(base), n, line)
		}
		for i, l := range strings.Split(string(cur), "\n") {
			if dshPattern.MatchString(l) {
				add("%s:%d names dsh: a dsh release job or build (M2-DOD-7): %q", rel, i+1, strings.TrimSpace(l))
			}
		}
	}
	workflows, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(workflowsDir), "*.y*ml"))
	if err != nil {
		add("%s: %v", workflowsDir, err)
	}
	for _, path := range workflows {
		rel := workflowsDir + "/" + filepath.Base(path)
		if slices.Contains(ReleaseFiles, rel) {
			continue
		}
		cur, err := readLF(root, rel)
		if err != nil {
			add("%s: %v", rel, err)
			continue
		}
		for _, t := range tagTriggers(string(cur)) {
			add("%s:%d: only %s may run on a tag push or a release, or run goreleaser (M2-DOD-7): %s", rel, t.line, ReleaseFiles[0], t.why)
		}
		for i, l := range strings.Split(string(cur), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "#") {
				continue
			}
			if goreleaserPattern.MatchString(l) {
				add("%s:%d: only %s may run on a tag push or a release, or run goreleaser (M2-DOD-7): %q", rel, i+1, ReleaseFiles[0], strings.TrimSpace(l))
			}
		}
	}

	egress, err := readLF(root, EgressGoldenPath)
	if err != nil {
		add("no default egress set: %v", err)
		return base, 0, anchor, problems
	}
	seen := map[string]bool{}
	for i, l := range strings.Split(string(egress), "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		m := egressLinePattern.FindStringSubmatch(l)
		if m == nil {
			add("%s:%d: want \"<profile>: [<kind> <endpoint>, ...]\", got %q", EgressGoldenPath, i+1, l)
			continue
		}
		seen[m[1]] = true
		if strings.TrimSpace(m[2]) != "" {
			add("%s:%d: default profile %s sends %s; the default egress set must be empty (NG-7)", EgressGoldenPath, i+1, m[1], m[2])
		}
	}
	for _, p := range sendNothingProfiles {
		if !seen[p] {
			add("%s has no egress set for the default profile %s (NG-7)", EgressGoldenPath, p)
		}
	}
	return base, len(seen), anchor, problems
}

// tagTrigger is one way a workflow's on: key starts it on a tag push or a release.
type tagTrigger struct {
	line int
	why  string
}

// tagTriggers reads each top-level on: key of a workflow, in any of its forms (a
// string, a sequence or a mapping, in block or flow style), and returns each trigger
// that runs the workflow on a tag push or a release: the release event, the create
// event (a tag created), and a push that filters on tags or tags-ignore or names no
// branches or branches-ignore filter (GitHub then runs it for tag pushes too). An on:
// key it cannot read is itself returned, so an unusual form fails closed.
func tagTriggers(src string) (found []tagTrigger) {
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		m := onKeyPattern.FindStringSubmatch(stripYAMLComment(lines[i]))
		if m == nil {
			continue
		}
		onLine := i + 1
		var block []yLine
		for i+1 < len(lines) {
			t := stripYAMLComment(lines[i+1])
			if t != "" && t[0] != ' ' {
				break
			}
			i++
			if strings.TrimSpace(t) != "" {
				block = append(block, yLine{indent: len(t) - len(strings.TrimLeft(t, " ")), text: strings.TrimSpace(t), line: i + 1})
			}
		}
		n, err := parseOnValue(strings.TrimSpace(m[2]), onLine, block)
		if err != nil {
			found = append(found, tagTrigger{onLine, fmt.Sprintf("its on: key cannot be read, so its triggers cannot be checked: %v", err)})
			continue
		}
		found = append(found, onTagTriggers(n, onLine)...)
	}
	return found
}

// onTagTriggers is tagTriggers for one parsed on: value.
func onTagTriggers(n *yNode, onLine int) (found []tagTrigger) {
	type event struct {
		name string
		line int
		cfg  *yNode
	}
	var events []event
	switch {
	case n.isMap:
		for k, key := range n.keys {
			events = append(events, event{key, n.keyLines[k], n.items[k]})
		}
	case n.isSeq:
		for _, it := range n.items {
			if it.isSeq || it.isMap {
				found = append(found, tagTrigger{it.line, "an on: sequence item is not an event name"})
				continue
			}
			events = append(events, event{it.scalar, it.line, nil})
		}
	case n.scalar != "":
		events = append(events, event{n.scalar, n.line, nil})
	}
	for _, ev := range events {
		switch ev.name {
		case "release":
			found = append(found, tagTrigger{ev.line, "it runs on a release event"})
		case "create":
			found = append(found, tagTrigger{ev.line, "it runs on the create event, which a tag push raises"})
		case "push":
			if ev.cfg == nil || !ev.cfg.isMap {
				found = append(found, tagTrigger{ev.line, "its push trigger has no branches filter, so it runs on every tag push"})
				continue
			}
			filtered := false
			for k, key := range ev.cfg.keys {
				switch key {
				case "tags", "tags-ignore":
					found = append(found, tagTrigger{ev.cfg.keyLines[k], fmt.Sprintf("its push trigger filters on %s, so it runs on a tag push", key)})
					filtered = true
				case "branches", "branches-ignore":
					filtered = true
				}
			}
			if !filtered {
				found = append(found, tagTrigger{ev.line, "its push trigger has no branches or tags filter, so it runs on every tag push"})
			}
		}
	}
	return found
}

// yLine is one non-blank, comment-free line of a block, with its indent and number.
type yLine struct {
	indent int
	text   string
	line   int
}

// yNode is a node of the YAML subset a workflow's on: key is written in: a scalar, a
// sequence or a mapping (in block or flow style), with the line it starts on. A node
// that is none of them is null.
type yNode struct {
	line         int
	scalar       string
	isSeq, isMap bool
	items        []*yNode // the sequence's items, or the mapping's values
	keys         []string
	keyLines     []int
}

// stripYAMLComment drops a "#" comment (at the start or after a blank, outside quotes)
// and trailing blanks.
func stripYAMLComment(l string) string {
	var quote byte
	for i := 0; i < len(l); i++ {
		c := l[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#' && (i == 0 || l[i-1] == ' ' || l[i-1] == '\t'):
			return strings.TrimRight(l[:i], " \t\r")
		}
	}
	return strings.TrimRight(l, " \t\r")
}

// parseOnValue parses an on: key's value: its inline text after the colon and the more
// indented lines under it.
func parseOnValue(inline string, line int, block []yLine) (*yNode, error) {
	if inline != "" {
		if inline[0] == '[' || inline[0] == '{' {
			for _, b := range block {
				inline += " " + b.text
			}
			return parseYAMLFlow(inline, line)
		}
		if len(block) > 0 {
			return nil, fmt.Errorf("line %d: unexpected %q after an inline value", block[0].line, block[0].text)
		}
		return parseYAMLFlow(inline, line)
	}
	if len(block) == 0 {
		return &yNode{line: line}, nil
	}
	n, i, err := parseYAMLBlock(block, 0, block[0].indent)
	if err == nil && i < len(block) {
		err = fmt.Errorf("line %d: unexpected %q", block[i].line, block[i].text)
	}
	return n, err
}

func isDashItem(text string) bool { return text == "-" || strings.HasPrefix(text, "- ") }

// parseYAMLBlock parses the block node starting at ls[i], whose lines are at indent; it
// returns the node and the index of the first line after it.
func parseYAMLBlock(ls []yLine, i, indent int) (*yNode, int, error) {
	start := ls[i]
	if isDashItem(start.text) {
		n := &yNode{isSeq: true, line: start.line}
		for i < len(ls) && ls[i].indent == indent && isDashItem(ls[i].text) {
			var child *yNode
			var err error
			if rest := strings.TrimLeft(ls[i].text[1:], " "); rest == "" {
				if i+1 < len(ls) && ls[i+1].indent > indent {
					child, i, err = parseYAMLBlock(ls, i+1, ls[i+1].indent)
				} else {
					child, i = &yNode{line: ls[i].line}, i+1
				}
			} else {
				// "- key: value" starts a node indented to the text after the dash.
				off := indent + len(ls[i].text) - len(rest)
				ls[i] = yLine{indent: off, text: rest, line: ls[i].line}
				child, i, err = parseYAMLBlock(ls, i, off)
			}
			if err != nil {
				return nil, i, err
			}
			n.items = append(n.items, child)
		}
		if i < len(ls) && ls[i].indent > indent {
			return nil, i, fmt.Errorf("line %d: unexpected indentation of %q", ls[i].line, ls[i].text)
		}
		return n, i, nil
	}
	n := &yNode{isMap: true, line: start.line}
	for i < len(ls) && ls[i].indent == indent && !isDashItem(ls[i].text) {
		key, val, ok := splitYAMLKey(ls[i].text)
		if !ok {
			if len(n.keys) == 0 {
				s, err := parseYAMLFlow(ls[i].text, ls[i].line)
				return s, i + 1, err
			}
			return nil, i, fmt.Errorf("line %d: %q is not a key", ls[i].line, ls[i].text)
		}
		line := ls[i].line
		i++
		var child *yNode
		var err error
		switch {
		case val == "":
			if i < len(ls) && (ls[i].indent > indent || ls[i].indent == indent && isDashItem(ls[i].text)) {
				child, i, err = parseYAMLBlock(ls, i, ls[i].indent)
			} else {
				child = &yNode{line: line}
			}
		case val[0] == '|' || val[0] == '>':
			for i < len(ls) && ls[i].indent > indent {
				i++
			}
			child = &yNode{line: line, scalar: val}
		default:
			if val[0] == '[' || val[0] == '{' {
				for i < len(ls) && ls[i].indent > indent {
					val += " " + ls[i].text
					i++
				}
			}
			child, err = parseYAMLFlow(val, line)
		}
		if err != nil {
			return nil, i, err
		}
		n.keys, n.keyLines, n.items = append(n.keys, key), append(n.keyLines, line), append(n.items, child)
	}
	if i < len(ls) && ls[i].indent > indent {
		return nil, i, fmt.Errorf("line %d: unexpected indentation of %q", ls[i].line, ls[i].text)
	}
	return n, i, nil
}

// splitYAMLKey splits a block mapping line "key: value" (the key plain or quoted).
func splitYAMLKey(text string) (key, val string, ok bool) {
	if text == "" || text[0] == '[' || text[0] == '{' {
		return "", "", false
	}
	p := &yamlFlowParser{s: text}
	if text[0] == '"' || text[0] == '\'' {
		k, err := p.quoted()
		if err != nil {
			return "", "", false
		}
		p.skipSpace()
		if p.pos >= len(text) || text[p.pos] != ':' || (p.pos+1 < len(text) && text[p.pos+1] != ' ') {
			return "", "", false
		}
		return k, strings.TrimSpace(text[p.pos+1:]), true
	}
	for i := 0; i < len(text); i++ {
		if text[i] == ':' && (i+1 == len(text) || text[i+1] == ' ') {
			return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+1:]), true
		}
	}
	return "", "", false
}

// parseYAMLFlow parses a scalar or a flow sequence or mapping that fills s.
func parseYAMLFlow(s string, line int) (*yNode, error) {
	p := &yamlFlowParser{s: s, line: line}
	n, err := p.value()
	if err != nil {
		return nil, err
	}
	if p.skipSpace(); p.pos != len(s) {
		return nil, fmt.Errorf("line %d: unexpected %q", line, s[p.pos:])
	}
	return n, nil
}

type yamlFlowParser struct {
	s          string
	pos, depth int
	line       int
}

func (p *yamlFlowParser) skipSpace() {
	for p.pos < len(p.s) && (p.s[p.pos] == ' ' || p.s[p.pos] == '\t') {
		p.pos++
	}
}

func (p *yamlFlowParser) fail(want string) error {
	if p.pos >= len(p.s) {
		return fmt.Errorf("line %d: %q ends where %s was expected", p.line, p.s, want)
	}
	return fmt.Errorf("line %d: %q has %q where %s was expected", p.line, p.s, p.s[p.pos:], want)
}

func (p *yamlFlowParser) value() (*yNode, error) {
	p.skipSpace()
	if p.pos >= len(p.s) {
		return &yNode{line: p.line}, nil
	}
	switch p.s[p.pos] {
	case '[':
		p.pos++
		p.depth++
		n := &yNode{isSeq: true, line: p.line}
		for {
			if p.skipSpace(); p.pos < len(p.s) && p.s[p.pos] == ']' {
				p.pos++
				p.depth--
				return n, nil
			}
			it, err := p.value()
			if err != nil {
				return nil, err
			}
			n.items = append(n.items, it)
			if p.skipSpace(); p.pos < len(p.s) && p.s[p.pos] == ',' {
				p.pos++
			} else if p.pos >= len(p.s) || p.s[p.pos] != ']' {
				return nil, p.fail(`"," or "]"`)
			}
		}
	case '{':
		p.pos++
		p.depth++
		n := &yNode{isMap: true, line: p.line}
		for {
			if p.skipSpace(); p.pos < len(p.s) && p.s[p.pos] == '}' {
				p.pos++
				p.depth--
				return n, nil
			}
			k, err := p.value()
			if err != nil {
				return nil, err
			}
			if k.isSeq || k.isMap {
				return nil, p.fail("a key")
			}
			v := &yNode{line: p.line}
			if p.skipSpace(); p.pos < len(p.s) && p.s[p.pos] == ':' {
				p.pos++
				if p.skipSpace(); p.pos < len(p.s) && p.s[p.pos] != ',' && p.s[p.pos] != '}' {
					if v, err = p.value(); err != nil {
						return nil, err
					}
				}
			}
			n.keys, n.keyLines, n.items = append(n.keys, k.scalar), append(n.keyLines, p.line), append(n.items, v)
			if p.skipSpace(); p.pos < len(p.s) && p.s[p.pos] == ',' {
				p.pos++
			} else if p.pos >= len(p.s) || p.s[p.pos] != '}' {
				return nil, p.fail(`"," or "}"`)
			}
		}
	case '"', '\'':
		s, err := p.quoted()
		return &yNode{line: p.line, scalar: s}, err
	}
	start := p.pos
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if p.depth > 0 && (c == ',' || c == ']' || c == '}') {
			break
		}
		if c == ':' && (p.pos+1 == len(p.s) || strings.IndexByte(" ,]}", p.s[p.pos+1]) >= 0) {
			break
		}
		p.pos++
	}
	if p.pos == start {
		return nil, p.fail("a value")
	}
	return &yNode{line: p.line, scalar: strings.TrimSpace(p.s[start:p.pos])}, nil
}

// quoted reads a single- or double-quoted scalar at p.pos.
func (p *yamlFlowParser) quoted() (string, error) {
	q := p.s[p.pos]
	p.pos++
	var b strings.Builder
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		p.pos++
		switch {
		case q == '\'' && c == '\'':
			if p.pos < len(p.s) && p.s[p.pos] == '\'' {
				b.WriteByte('\'')
				p.pos++
				continue
			}
			return b.String(), nil
		case q == '"' && c == '\\' && p.pos < len(p.s):
			b.WriteByte(p.s[p.pos])
			p.pos++
		case q == '"' && c == '"':
			return b.String(), nil
		default:
			b.WriteByte(c)
		}
	}
	return "", p.fail("a closing quote")
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// runRelease is "harness-gate release".
func runRelease(args []string, stdout, stderr io.Writer) int {
	root, ok := subcommandFlags("release", args, stderr, nil)
	if !ok {
		return 2
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate release: %v\n", err)
		return 2
	}
	base, profiles, anchor, problems := checkRelease(abs)
	if len(problems) > 0 {
		printBlockers(stdout, "harness-gate release: FAILED", problems)
		return 1
	}
	fmt.Fprintf(stdout, "harness-gate release: %s equal their copies at the base commit %s (%s); no dsh release job; the default egress set of %d profiles is empty\n",
		strings.Join(ReleaseFiles, " and "), short(base), anchor, profiles)
	return 0
}
