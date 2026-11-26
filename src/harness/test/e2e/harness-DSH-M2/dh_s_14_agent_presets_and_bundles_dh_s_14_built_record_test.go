//go:build e2e

package e2e

// Step definitions of dh_s_14_agent_presets_and_bundles_dh_s_14_built_record.feature:
// the merged commit of DH-S-14 passed every gate on both local lanes, the coverage
// check included, as its evidence record says (M2-DOD-1; proof: service:go-toolchain,
// the lanes' --evidence records verified byte for byte), and the committed record is
// ready for "harness-gate ready --stage DH-S-14 --built" while it reads open (C57;
// proof: golden, with live probes that the check reads the fields it reports on).

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

var s14rOuterT *testing.T

// s14rCommitEnv names the merged commit of DH-S-14 explicitly; when unset it is
// found on the checkout's history by s14rCommitSubject.
const (
	s14rCommitEnv     = "DSH_E2E_DHS14_COMMIT"
	s14rCommitSubject = `^\[impl\] DH-S-14 Built Record`
	s14rRunsDir       = "docs/stories/harness-DSH-M1/evidence/runs"
	s14rIndex         = "test/harness/acceptance-index.yaml"
)

type s14rLane struct {
	name, path string
	data       []byte
}

type s14rState struct {
	root   string
	commit string
	lanes  map[string]*s14rLane

	record    string // repository path of the stage record
	committed string // the record as committed at HEAD
	gate      string // a built harness-gate binary
	readyOut  string
	readyCode int
}

func s14rIsRegular(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func s14rRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if s14rIsRegular(filepath.Join(dir, "go.work")) && s14rIsRegular(filepath.Join(dir, filepath.FromSlash(s14rIndex))) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no repository root (go.work + test/harness/acceptance-index.yaml) above the working directory")
		}
		dir = parent
	}
}

func s14rCommand(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *s14rState) ensureRoot() error {
	if s.root != "" {
		return nil
	}
	root, err := s14rRepoRoot()
	if err != nil {
		return err
	}
	s.root = root
	return nil
}

// ---- Scenario: S14 gate green on both lanes ----

var s14rHex = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (s *s14rState) givenMergedCommit() error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	ref := strings.TrimSpace(os.Getenv(s14rCommitEnv))
	if ref == "" {
		out, err := s14rCommand(s.root, nil, "git", "log", "--format=%H", "-E", "--grep", s14rCommitSubject, "-n", "1", "HEAD")
		if err != nil {
			return fmt.Errorf("git log --grep: %v\n%s", err, out)
		}
		ref = strings.TrimSpace(out)
		if ref == "" {
			return fmt.Errorf("no commit %q on this checkout's history; set %s", s14rCommitSubject, s14rCommitEnv)
		}
	}
	out, err := s14rCommand(s.root, nil, "git", "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return fmt.Errorf("git rev-parse %s: %v\n%s", ref, err, out)
	}
	s.commit = strings.TrimSpace(out)
	if !s14rHex.MatchString(s.commit) {
		return fmt.Errorf("unexpected commit %q", s.commit)
	}
	if out, err := s14rCommand(s.root, nil, "git", "merge-base", "--is-ancestor", s.commit, "HEAD"); err != nil {
		return fmt.Errorf("%s is not on this checkout's history: %v\n%s", s.commit, err, out)
	}
	return nil
}

// lanesRan finds the --evidence record each lane script wrote for the merged
// commit (lanes/win.ps1 and lanes/wsl.sh name it <lane>-<sha7>-<UTC stamp>.txt
// under s14rRunsDir); the latest one of each lane is the lane's result.
func (s *s14rState) lanesRan(win, wsl string) error {
	s.lanes = map[string]*s14rLane{}
	for _, script := range []string{win, wsl} {
		lane := strings.TrimSuffix(strings.TrimSuffix(script, ".ps1"), ".sh")
		data, err := os.ReadFile(filepath.Join(s.root, "tools", "harness-gate", "lanes", script))
		if err != nil {
			return fmt.Errorf("lane script %s: %v", script, err)
		}
		if !bytes.Contains(data, []byte("--evidence")) || !bytes.Contains(data, []byte(lane+"-")) {
			return fmt.Errorf("lane script %s does not write a %s-<sha7> evidence record", script, lane)
		}
		pattern := filepath.Join(s.root, filepath.FromSlash(s14rRunsDir), lane+"-"+s.commit[:7]+"-*Z.txt")
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			return fmt.Errorf("no %s lane record of %s (%s): run tools/harness-gate/lanes/%s on that commit and commit its record", lane, s.commit[:7], filepath.ToSlash(pattern), script)
		}
		slices.Sort(matches)
		p := matches[len(matches)-1]
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		s.lanes[lane] = &s14rLane{name: lane, path: filepath.ToSlash(rel), data: b}
	}
	return nil
}

// s14rHeader returns the "key: value" lines of the record before "--- output ---".
func s14rHeader(data []byte) map[string]string {
	h := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		if line == "--- output ---" {
			break
		}
		if k, v, ok := strings.Cut(line, ": "); ok {
			h[k] = v
		}
	}
	return h
}

func (s *s14rState) laneRecordIntact(lane, goos string) error {
	l, ok := s.lanes[lane]
	if !ok {
		return fmt.Errorf("no %s lane record", lane)
	}
	data := bytes.ReplaceAll(l.data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(data, []byte("harness-gate evidence record\n")) {
		return fmt.Errorf("%s is not a harness-gate evidence record", l.path)
	}
	// The trailer is the SHA-256 of every byte before it (evidence.go): the record
	// is the lane's own output, not edited since.
	body := bytes.TrimSuffix(data, []byte("\n"))
	i := bytes.LastIndexByte(body, '\n')
	if i < 0 {
		return fmt.Errorf("%s has no sha256 trailer", l.path)
	}
	sum := sha256.Sum256(data[:i+1])
	if last := string(body[i+1:]); last != "sha256: "+hex.EncodeToString(sum[:]) {
		return fmt.Errorf("%s: trailer %q does not match the record's SHA-256 %s", l.path, last, hex.EncodeToString(sum[:]))
	}
	h := s14rHeader(data)
	if h["commit"] != s.commit {
		return fmt.Errorf("%s records commit %q, want %s", l.path, h["commit"], s.commit)
	}
	if !strings.HasPrefix(h["tree"], "clean") {
		return fmt.Errorf("%s: tree %q is not clean", l.path, h["tree"])
	}
	if !strings.HasPrefix(h["goos/goarch"], goos+"/") {
		return fmt.Errorf("%s ran on %q, want %s", l.path, h["goos/goarch"], goos)
	}
	if !strings.Contains(h["command"], "--evidence") || !strings.Contains(filepath.ToSlash(h["command"]), "/"+filepath.Base(l.path)) {
		return fmt.Errorf("%s: command %q did not write this record", l.path, h["command"])
	}
	return nil
}

var (
	s14rGateLine = regexp.MustCompile(`(?m)^harness-gate: gate (\d+)/(\d+) (\S+) --`)
	s14rPassLine = regexp.MustCompile(`(?m)^harness-gate: PASS (\S+) \(`)
	s14rFailLine = regexp.MustCompile(`(?m)^harness-gate: FAIL`)
	s14rAllLine  = regexp.MustCompile(`(?m)^harness-gate: PASSED all (\d+) gates$`)
)

// s14rPassed returns the gates the record ran, in order, and the ones it passed.
func s14rPassed(l *s14rLane) (string, [][]string, map[string]bool) {
	text := strings.ReplaceAll(string(l.data), "\r\n", "\n")
	passed := map[string]bool{}
	for _, m := range s14rPassLine.FindAllStringSubmatch(text, -1) {
		passed[m[1]] = true
	}
	return text, s14rGateLine.FindAllStringSubmatch(text, -1), passed
}

func (s *s14rState) everyGatePasses() error {
	for _, lane := range []string{"win", "wsl"} {
		l := s.lanes[lane]
		text, gates, passed := s14rPassed(l)
		if len(gates) == 0 {
			return fmt.Errorf("%s runs no gate", l.path)
		}
		total := gates[0][2]
		for i, g := range gates {
			if g[1] != strconv.Itoa(i+1) || g[2] != total {
				return fmt.Errorf("%s: gate line %q out of order", l.path, g[0])
			}
			if !passed[g[3]] {
				return fmt.Errorf("%s: gate %s did not pass", l.path, g[3])
			}
		}
		if strconv.Itoa(len(gates)) != total {
			return fmt.Errorf("%s ran %d of %s gates", l.path, len(gates), total)
		}
		if s14rFailLine.MatchString(text) {
			return fmt.Errorf("%s reports a failed gate", l.path)
		}
		if all := s14rAllLine.FindStringSubmatch(text); all == nil || all[1] != total {
			return fmt.Errorf("%s does not report PASSED all %s gates", l.path, total)
		}
		if v := s14rTrailerField(text, "exit"); v != "0" {
			return fmt.Errorf("%s exits %q, want 0", l.path, v)
		}
	}
	return nil
}

// gateAmongPassing: the named gate is one the lane ran and passed, so a gate list
// that drops it (the coverage check of M2-DOD-1) cannot pass this scenario.
func (s *s14rState) gateAmongPassing(name string) error {
	for _, lane := range []string{"win", "wsl"} {
		l := s.lanes[lane]
		_, gates, passed := s14rPassed(l)
		ran := slices.ContainsFunc(gates, func(g []string) bool { return g[3] == name })
		if !ran || !passed[name] {
			return fmt.Errorf("%s: gate %s ran %v, passed %v", l.path, name, ran, passed[name])
		}
	}
	return nil
}

func s14rTrailerField(text, key string) string {
	_, after, ok := strings.Cut(text, "--- end of output ---\n")
	if !ok {
		return ""
	}
	for _, line := range strings.Split(after, "\n") {
		if v, ok := strings.CutPrefix(line, key+": "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// evidenceRecordsLanes requires the stage record to name, on one line each, the
// lane script, its record file and the merged commit, with a passing result.
func (s *s14rState) evidenceRecordsLanes(record string) error {
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(record)))
	if err != nil {
		return err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.Contains(text, s.commit[:7]) {
		return fmt.Errorf("%s does not name the merged commit %s", record, s.commit[:7])
	}
	scripts := map[string]string{"win": "win.ps1", "wsl": "wsl.sh"}
	for _, lane := range []string{"win", "wsl"} {
		base := filepath.Base(s.lanes[lane].path)
		found := slices.ContainsFunc(strings.Split(text, "\n"), func(line string) bool {
			return strings.Contains(line, scripts[lane]) && strings.Contains(line, base) &&
				strings.Contains(line, s.commit[:7]) && strings.Contains(strings.ToLower(line), "exit 0")
		})
		if !found {
			return fmt.Errorf("%s has no line naming %s, its record %s, the commit %s and exit 0", record, scripts[lane], base, s.commit[:7])
		}
	}
	return nil
}

// ---- Scenario: Record complete ----

// s14rCells splits a Markdown table row on its unescaped pipes.
func s14rCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			cur.WriteString(`\|`)
			i++
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(line[i])
		}
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

type s14rTable struct {
	head  map[string]int // lower-cased column name -> index
	rows  [][]string
	lines []int // the line index of each row
}

// s14rReadTable reads the table under "## <heading>" of the record's lines.
func s14rReadTable(lines []string, heading string) (s14rTable, error) {
	t := s14rTable{head: map[string]int{}}
	in, header := false, false
	for i, line := range lines {
		if strings.HasPrefix(line, "## ") {
			if in {
				break
			}
			in = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == heading
			continue
		}
		if !in || !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cells := s14rCells(line)
		switch {
		case !header:
			for j, c := range cells {
				t.head[strings.ToLower(c)] = j
			}
			header = true
		case strings.Trim(strings.Join(cells, ""), "-: ") == "":
		default:
			t.rows = append(t.rows, cells)
			t.lines = append(t.lines, i)
		}
	}
	if !header {
		return t, fmt.Errorf("no table under %q", "## "+heading)
	}
	return t, nil
}

var s14rSHA = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func (s *s14rState) givenCommittedRecord(record, status string) error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	out, err := s14rCommand(s.root, nil, "git", "show", "HEAD:"+record)
	if err != nil {
		return fmt.Errorf("git show HEAD:%s: %v\n%s", record, err, out)
	}
	s.record = record
	s.committed = strings.ReplaceAll(out, "\r\n", "\n")
	if !slices.Contains(strings.Split(s.committed, "\n"), status) {
		return fmt.Errorf("the committed %s has no line %q", record, status)
	}
	return nil
}

// ready runs the built gate binary's "ready" on a scratch root holding the
// committed acceptance index and the given record text.
func (s *s14rState) ready(record string) (string, int, error) {
	if s.gate == "" {
		bin := filepath.Join(s14rOuterT.TempDir(), "harness-gate")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		out, err := s14rCommand(filepath.Join(s.root, "tools", "harness-gate"), []string{"GOWORK=off", "GOFLAGS="}, "go", "build", "-o", bin, ".")
		if err != nil {
			return "", 0, fmt.Errorf("go build harness-gate: %v\n%s", err, out)
		}
		s.gate = bin
	}
	index, err := s14rCommand(s.root, nil, "git", "show", "HEAD:"+s14rIndex)
	if err != nil {
		return "", 0, fmt.Errorf("git show HEAD:%s: %v\n%s", s14rIndex, err, index)
	}
	probe := s14rOuterT.TempDir()
	for p, text := range map[string]string{s14rIndex: index, s.record: record} {
		to := filepath.Join(probe, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return "", 0, err
		}
		if err := os.WriteFile(to, []byte(text), 0o644); err != nil {
			return "", 0, err
		}
	}
	cmd := exec.Command(s.gate, "ready", "--stage", "DH-S-14", "--built", "--root", probe)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0, nil
	case errors.As(err, &exit):
		return string(out), exit.ExitCode(), nil
	default:
		return "", 0, fmt.Errorf("harness-gate ready: %v", err)
	}
}

func (s *s14rState) runReady(command string) error {
	if command != "harness-gate ready --stage DH-S-14 --built" {
		return fmt.Errorf("unexpected command %q", command)
	}
	out, code, err := s.ready(s.committed)
	s.readyOut, s.readyCode = out, code
	return err
}

func (s *s14rState) exits0WithGolden(golden string) error {
	if s.readyCode != 0 {
		return fmt.Errorf("harness-gate ready exits %d, want 0:\n%s", s.readyCode, s.readyOut)
	}
	want, err := os.ReadFile(filepath.FromSlash(golden))
	if err != nil {
		return err
	}
	got := strings.ReplaceAll(s.readyOut, "\r\n", "\n")
	if w := strings.ReplaceAll(string(want), "\r\n", "\n"); got != w {
		return fmt.Errorf("harness-gate ready output differs from %s:\n got: %q\nwant: %q", golden, got, w)
	}
	return nil
}

// noMissingField: the output names no blocker, and every step and manual check row
// of the record carries the commit and the fields "ready" requires of it.
func (s *s14rState) noMissingField() error {
	for _, bad := range []string{"NOT READY", "no commit", "not a commit SHA", "manual check", "no seen", "no operator"} {
		if strings.Contains(s.readyOut, bad) {
			return fmt.Errorf("harness-gate ready reports %q:\n%s", bad, s.readyOut)
		}
	}
	lines := strings.Split(s.committed, "\n")
	steps, err := s14rReadTable(lines, "Steps")
	if err != nil {
		return err
	}
	if len(steps.rows) == 0 {
		return errors.New("the Steps table has no row")
	}
	for _, r := range steps.rows {
		if c := r[steps.head["commit"]]; !s14rSHA.MatchString(c) {
			return fmt.Errorf("step %s: commit %q", r[steps.head["step"]], c)
		}
	}
	manual, err := s14rReadTable(lines, "Manual checks register")
	if err != nil {
		return err
	}
	if len(manual.rows) == 0 {
		return errors.New("the Manual checks register has no row")
	}
	for _, r := range manual.rows {
		for _, col := range []string{"seen", "operator"} {
			if r[manual.head[col]] == "" {
				return fmt.Errorf("manual check of step %s: no %s", r[manual.head["step"]], col)
			}
		}
		if c := r[manual.head["commit"]]; !s14rSHA.MatchString(c) || r[manual.head["label"]] != "`manual`" && r[manual.head["label"]] != "manual" {
			return fmt.Errorf("manual check of step %s: commit %q, label %q", r[manual.head["step"]], c, r[manual.head["label"]])
		}
	}
	return nil
}

func (s *s14rState) statusNotInspected() error {
	const open, closed = "Closure status: open", "Closure status: closed"
	edited := strings.Replace(s.committed, "\n"+open+"\n", "\n"+closed+"\n", 1)
	if edited == s.committed {
		return fmt.Errorf("the record has no line %q to change", open)
	}
	out, code, err := s.ready(edited)
	if err != nil {
		return err
	}
	want := strings.Replace(s.readyOut, "(it reads open)", "(it reads closed)", 1)
	if code != 0 || out != want {
		return fmt.Errorf("the record reading closed: exit %d, output %q; want exit 0, %q", code, out, want)
	}
	return nil
}

// withoutCommit clears the commit cell of the step's row in the table under
// heading, checks "ready" refuses it naming the gap, and that the committed
// record still passes (the refusal is the cleared cell's, not the probe's).
func (s *s14rState) withoutCommit(heading, step, naming string) error {
	lines := strings.Split(s.committed, "\n")
	t, err := s14rReadTable(lines, heading)
	if err != nil {
		return err
	}
	ci, ok := t.head["commit"]
	if !ok {
		return fmt.Errorf("%s has no commit column", heading)
	}
	row := slices.IndexFunc(t.rows, func(r []string) bool { return r[t.head["step"]] == step })
	if row < 0 {
		return fmt.Errorf("%s has no row of step %s", heading, step)
	}
	cells := slices.Clone(t.rows[row])
	if !s14rSHA.MatchString(cells[ci]) {
		return fmt.Errorf("%s, step %s: commit %q is not a SHA to clear", heading, step, cells[ci])
	}
	cells[ci] = ""
	edited := slices.Clone(lines)
	edited[t.lines[row]] = "| " + strings.Join(cells, " | ") + " |"
	out, code, err := s.ready(strings.Join(edited, "\n"))
	if err != nil {
		return err
	}
	if code != 1 || !strings.Contains(out, "NOT READY") || !strings.Contains(out, naming) {
		return fmt.Errorf("the record without the commit of %s step %s: exit %d, want 1 naming %q:\n%s", heading, step, code, naming, out)
	}
	return nil
}

func (s *s14rState) withoutStepCommit(step, naming string) error {
	return s.withoutCommit("Steps", step, naming)
}

func (s *s14rState) withoutManualCommit(step, naming string) error {
	return s.withoutCommit("Manual checks register", step, naming)
}

func InitializeScenario_dh_s_14_agent_presets_and_bundles_dh_s_14_built_record(ctx *godog.ScenarioContext) {
	s := &s14rState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = s14rState{}
		return c, nil
	})

	ctx.Step(`^the merged commit of stage DH-S-14$`, s.givenMergedCommit)
	ctx.Step(`^harness-gate runs on "([^"]*)" and "([^"]*)" for that commit$`, s.lanesRan)
	ctx.Step(`^the "([^"]*)" lane record is an intact clean-tree evidence record of that commit on "([^"]*)"$`, s.laneRecordIntact)
	ctx.Step(`^every gate of each lane record passes and the record exits 0$`, s.everyGatePasses)
	ctx.Step(`^the "([^"]*)" gate is among the passing gates of each lane record$`, s.gateAmongPassing)
	ctx.Step(`^"([^"]*)" records both lane results with the commit SHA$`, s.evidenceRecordsLanes)

	ctx.Step(`^the committed "([^"]*)" reading "([^"]*)"$`, s.givenCommittedRecord)
	ctx.Step(`^"([^"]*)" runs on it$`, s.runReady)
	ctx.Step(`^it exits 0 with the output of "([^"]*)"$`, s.exits0WithGolden)
	ctx.Step(`^it reports no missing manual check or commit field$`, s.noMissingField)
	ctx.Step(`^the status line is not inspected: the record reading "Closure status: closed" is ready the same$`, s.statusNotInspected)
	ctx.Step(`^the check is live: the record without the commit of step "([^"]*)" is not ready, naming "([^"]*)"$`, s.withoutStepCommit)
	ctx.Step(`^the check is live: the record without the commit of the manual check of step "([^"]*)" is not ready, naming "([^"]*)"$`, s.withoutManualCommit)
}

func TestE2E_dh_s_14_agent_presets_and_bundles_dh_s_14_built_record(t *testing.T) {
	s14rOuterT = t
	defer func() { s14rOuterT = nil }()
	suite := godog.TestSuite{
		Name:                "dh_s_14_agent_presets_and_bundles_dh_s_14_built_record",
		ScenarioInitializer: InitializeScenario_dh_s_14_agent_presets_and_bundles_dh_s_14_built_record,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_14_agent_presets_and_bundles_dh_s_14_built_record.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
