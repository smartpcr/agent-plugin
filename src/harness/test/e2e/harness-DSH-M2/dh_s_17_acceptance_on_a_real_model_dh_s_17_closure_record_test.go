//go:build e2e

package e2e

// Step definitions of dh_s_17_acceptance_on_a_real_model_dh_s_17_closure_record.feature:
// the two-step close of DH-S-17 (implementation-plan Stage 7.7, M2-DOD-3, M2-C-2) and
// the copilot-cli legs it keeps (M2-DEC-4). proof: golden -- the built harness-gate runs
// over the committed fixtures of tools/harness-gate/testdata/closure/ and its output is
// compared with expected_s17.golden; the committed live record and index are checked to
// be in the fixtures' form and the live record's gate state to agree with its status line.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"
)

const (
	s17cStage      = "DH-S-17"
	s17cRecord     = "docs/stories/harness-DSH-M2/evidence/DH-S-17.md"
	s17cIndex      = "test/harness/acceptance-index.yaml"
	s17cFixtureDir = "tools/harness-gate/testdata/closure"
	s17cGolden     = "tools/harness-gate/testdata/closure/expected_s17.golden"
)

var (
	s17cGateOnce sync.Once
	s17cGateBin  string
	s17cGateErr  error
	s17cGateTmp  string
)

// s17cRow is one row of a record's Scenarios table.
type s17cRow struct {
	id, variant, venue, leg, env, commit, result, artifacts string
}

func (r s17cRow) key() string { return r.id + "|" + r.variant + "|" + r.venue + "|" + r.leg }

type s17cRun struct {
	code   int
	stdout string
}

type s17cState struct {
	root string

	liveRecord string
	liveRows   []s17cRow
	liveStatus string
	liveIDs    []string

	fixture    string // the committed fixture read by the Copilot-leg scenario
	scratch    string // scratch root holding the filled record
	scratchRec string
	last       s17cRun
	golden     map[string]s17cRun
	reading    []string
}

func s17cIsRegular(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func s17cRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if s17cIsRegular(filepath.Join(dir, "go.work")) && s17cIsRegular(filepath.Join(dir, filepath.FromSlash(s17cIndex))) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no repository root (go.work + test/harness/acceptance-index.yaml) above the working directory")
		}
		dir = parent
	}
}

func s17cCommand(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *s17cState) ensureRoot() error {
	if s.root != "" {
		return nil
	}
	root, err := s17cRepoRoot()
	if err != nil {
		return err
	}
	s.root = root
	return nil
}

// gate builds harness-gate from this checkout once per test binary (GOWORK=off: the
// tool is its own module, as its README runs it).
func (s *s17cState) gate() (string, error) {
	s17cGateOnce.Do(func() {
		tmp, err := os.MkdirTemp("", "s17c-gate-")
		if err != nil {
			s17cGateErr = err
			return
		}
		s17cGateTmp = tmp
		bin := filepath.Join(tmp, "harness-gate")
		if filepath.Separator == '\\' {
			bin += ".exe"
		}
		out, err := s17cCommand(filepath.Join(s.root, "tools", "harness-gate"), []string{"GOWORK=off", "GOFLAGS="}, "go", "build", "-o", bin, ".")
		if err != nil {
			s17cGateErr = fmt.Errorf("go build harness-gate: %v\n%s", err, out)
			return
		}
		s17cGateBin = bin
	})
	return s17cGateBin, s17cGateErr
}

func (s *s17cState) runGate(sub, root string) (s17cRun, error) {
	bin, err := s.gate()
	if err != nil {
		return s17cRun{}, err
	}
	cmd := exec.Command(bin, sub, "--root", root, "--stage", s17cStage)
	cmd.Dir = root
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return s17cRun{}, fmt.Errorf("run harness-gate %s: %v", sub, err)
		}
		code = ee.ExitCode()
	}
	if code == 2 {
		return s17cRun{}, fmt.Errorf("harness-gate %s exited 2 (usage)\nstdout:\n%s\nstderr:\n%s", sub, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		return s17cRun{}, fmt.Errorf("harness-gate %s wrote to stderr:\n%s", sub, stderr.String())
	}
	return s17cRun{code: code, stdout: strings.ReplaceAll(stdout.String(), "\r\n", "\n")}, nil
}

// s17cParse returns a record's status value and its Scenarios table rows.
func s17cParse(src string) (string, []s17cRow, error) {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var status string
	var rows []s17cRow
	in, fence := false, false
	for _, l := range lines {
		if strings.HasPrefix(l, "```") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		if v, ok := strings.CutPrefix(l, "Closure status:"); ok {
			if status != "" {
				return "", nil, errors.New("a second status line")
			}
			status = strings.TrimSpace(v)
			continue
		}
		if strings.HasPrefix(l, "## ") {
			in = strings.TrimSpace(l) == "## Scenarios"
			continue
		}
		if !in || !strings.HasPrefix(l, "| DH-E-") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(l), "|"), "|")
		if len(cells) != 8 {
			return "", nil, fmt.Errorf("scenario row with %d cells, want 8: %q", len(cells), l)
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows = append(rows, s17cRow{cells[0], cells[1], cells[2], cells[3], cells[4], cells[5], cells[6], cells[7]})
	}
	if status == "" {
		return "", nil, errors.New("no status line")
	}
	return status, rows, nil
}

var s17cIndexID = regexp.MustCompile(`^\s+- id: (DH-E-[A-Z0-9-]+)\s*$`)

// s17cStageIDs returns the IDs the acceptance index lists under DH-S-17.
func s17cStageIDs(src string) []string {
	var ids []string
	in := false
	for _, l := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(l, "  DH-S-") {
			in = strings.TrimSpace(l) == s17cStage+":"
			continue
		}
		if len(l) > 0 && l[0] != ' ' && l[0] != '#' {
			in = false
		}
		if in {
			if m := s17cIndexID.FindStringSubmatch(l); m != nil {
				ids = append(ids, m[1])
			}
		}
	}
	slices.Sort(ids)
	return ids
}

func s17cKeys(rows []s17cRow) []string {
	var keys []string
	for _, r := range rows {
		keys = append(keys, r.key())
	}
	slices.Sort(keys)
	return keys
}

func (s *s17cState) fixturePath(tree string) string {
	return filepath.Join(s.root, filepath.FromSlash(s17cFixtureDir), tree)
}

func (s *s17cState) readFixture(tree string) (string, []s17cRow, []string, error) {
	rec, err := os.ReadFile(filepath.Join(s.fixturePath(tree), filepath.FromSlash(s17cRecord)))
	if err != nil {
		return "", nil, nil, err
	}
	status, rows, err := s17cParse(string(rec))
	if err != nil {
		return "", nil, nil, fmt.Errorf("fixture %s: %v", tree, err)
	}
	idx, err := os.ReadFile(filepath.Join(s.fixturePath(tree), filepath.FromSlash(s17cIndex)))
	if err != nil {
		return "", nil, nil, err
	}
	return status, rows, s17cStageIDs(string(idx)), nil
}

func (s *s17cState) copyFixture(tree string) (string, error) {
	tmp, err := os.MkdirTemp("", "s17c-"+tree+"-")
	if err != nil {
		return "", err
	}
	if err := os.CopyFS(tmp, os.DirFS(s.fixturePath(tree))); err != nil {
		return "", err
	}
	return tmp, nil
}

var s17cGoldenHead = regexp.MustCompile(`^== (\S+): (ready|close) --stage DH-S-17 \(exit (\d+)\)$`)

func (s *s17cState) loadGolden() error {
	if s.golden != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(s17cGolden)))
	if err != nil {
		return err
	}
	s.golden = map[string]s17cRun{}
	var key string
	var body strings.Builder
	flush := func() {
		if key != "" {
			r := s.golden[key]
			r.stdout = body.String()
			s.golden[key] = r
		}
		body.Reset()
	}
	for _, l := range strings.SplitAfter(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if m := s17cGoldenHead.FindStringSubmatch(strings.TrimSuffix(l, "\n")); m != nil {
			flush()
			key = m[1] + "/" + m[2]
			var code int
			fmt.Sscanf(m[3], "%d", &code)
			s.golden[key] = s17cRun{code: code}
			continue
		}
		if strings.HasPrefix(l, "stderr: ") {
			return fmt.Errorf("golden section %s records stderr", key)
		}
		body.WriteString(l)
	}
	flush()
	return nil
}

func (s *s17cState) matchGolden(tree, sub string, got s17cRun) error {
	if err := s.loadGolden(); err != nil {
		return err
	}
	want, ok := s.golden[tree+"/"+sub]
	if !ok {
		return fmt.Errorf("%s has no section %s: %s", s17cGolden, tree, sub)
	}
	if got.code != want.code || got.stdout != want.stdout {
		return fmt.Errorf("%s %s: exit %d, want %d\n--- got\n%s--- want\n%s", tree, sub, got.code, want.code, got.stdout, want.stdout)
	}
	return nil
}

// ---- shared Given ----

func (s *s17cState) givenCommittedRecord() error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	rec, err := s17cCommand(s.root, nil, "git", "show", "HEAD:"+s17cRecord)
	if err != nil {
		return fmt.Errorf("git show HEAD:%s: %v\n%s", s17cRecord, err, rec)
	}
	work, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(s17cRecord)))
	if err != nil {
		return err
	}
	if strings.ReplaceAll(string(work), "\r\n", "\n") != strings.ReplaceAll(rec, "\r\n", "\n") {
		return fmt.Errorf("%s differs from HEAD: the gate would read an uncommitted record", s17cRecord)
	}
	status, rows, err := s17cParse(rec)
	if err != nil {
		return fmt.Errorf("%s: %v", s17cRecord, err)
	}
	idx, err := s17cCommand(s.root, nil, "git", "show", "HEAD:"+s17cIndex)
	if err != nil {
		return fmt.Errorf("git show HEAD:%s: %v\n%s", s17cIndex, err, idx)
	}
	s.liveRecord, s.liveStatus, s.liveRows, s.liveIDs = rec, status, rows, s17cStageIDs(idx)
	if len(s.liveIDs) != 14 {
		return fmt.Errorf("the committed index lists %d DH-S-17 IDs, want 14: %v", len(s.liveIDs), s.liveIDs)
	}
	for _, id := range s.liveIDs {
		if !slices.ContainsFunc(rows, func(r s17cRow) bool { return r.id == id }) {
			return fmt.Errorf("the committed record has no row of index ID %s", id)
		}
	}
	return nil
}

// ---- Scenario: S17 ready then closed ----

func (s *s17cState) fixturesInRecordForm(a, b, c string) error {
	want := s17cKeys(s.liveRows)
	for _, tree := range []string{a, b, c} {
		_, rows, ids, err := s.readFixture(tree)
		if err != nil {
			return err
		}
		if !slices.Equal(ids, s.liveIDs) {
			return fmt.Errorf("fixture %s index lists %v under DH-S-17, the committed index %v", tree, ids, s.liveIDs)
		}
		if got := s17cKeys(rows); !slices.Equal(got, want) {
			return fmt.Errorf("fixture %s scenario rows (id|variant|venue|leg)\n%v\nare not the committed record's\n%v", tree, got, want)
		}
	}
	return nil
}

func (s *s17cState) liveGateAgrees() error {
	ready, err := s.runGate("ready", s.root)
	if err != nil {
		return err
	}
	closed, err := s.runGate("close", s.root)
	if err != nil {
		return err
	}
	allPassed := !slices.ContainsFunc(s.liveRows, func(r s17cRow) bool {
		return r.leg == "required" && r.result != "passed"
	})
	switch s.liveStatus {
	case "open":
		if !strings.Contains(closed.stdout, "REFUSED not-yet-closed") || closed.code != 1 {
			return fmt.Errorf("the committed record reads open: close exit %d, want 1 refused not-yet-closed\n%s", closed.code, closed.stdout)
		}
		if !allPassed && ready.code != 1 {
			return fmt.Errorf("the committed record has required rows not passed but ready exits %d\n%s", ready.code, ready.stdout)
		}
		if allPassed && (ready.code != 0 || !strings.Contains(ready.stdout, ": ready; the status line is not read (it reads open)")) {
			return fmt.Errorf("the committed record reads open with every required row passed: ready exit %d, want 0 ready\n%s", ready.code, ready.stdout)
		}
		for _, r := range s.liveRows {
			if strings.HasPrefix(r.result, "not run") && !strings.Contains(ready.stdout, r.id+" ["+r.variant+"]") {
				return fmt.Errorf("ready does not name the not-run row %s [%s]\n%s", r.id, r.variant, ready.stdout)
			}
		}
	case "closed":
		if ready.code != 0 || closed.code != 0 {
			return fmt.Errorf("the committed record reads closed: ready exit %d, close exit %d, want both 0\n%s%s", ready.code, closed.code, ready.stdout, closed.stdout)
		}
		if !allPassed {
			return errors.New("the committed record reads closed with a required row not passed")
		}
	default:
		return fmt.Errorf("the committed record's status line reads %q", s.liveStatus)
	}
	return nil
}

func (s *s17cState) readyWhileOpen() error {
	status, _, _, err := s.readFixture("open_ready")
	if err != nil {
		return err
	}
	if status != "open" {
		return fmt.Errorf("fixture open_ready reads %q", status)
	}
	s.scratch, err = s.copyFixture("open_ready")
	if err != nil {
		return err
	}
	s.scratchRec = filepath.Join(s.scratch, filepath.FromSlash(s17cRecord))
	s.last, err = s.runGate("ready", s.scratch)
	return err
}

func (s *s17cState) exitsZeroWithGolden(tree string) error {
	if s.last.code != 0 {
		return fmt.Errorf("exit %d, want 0\n%s", s.last.code, s.last.stdout)
	}
	return s.matchGolden(tree, "ready", s.last)
}

func (s *s17cState) everyIDPassed(n int) error {
	src, err := os.ReadFile(s.scratchRec)
	if err != nil {
		return err
	}
	_, rows, err := s17cParse(string(src))
	if err != nil {
		return err
	}
	if len(s.liveIDs) != n {
		return fmt.Errorf("%d index IDs, want %d", len(s.liveIDs), n)
	}
	for _, id := range s.liveIDs {
		if !slices.ContainsFunc(rows, func(r s17cRow) bool {
			return r.id == id && r.leg == "required" && r.result == "passed" && r.commit != ""
		}) {
			return fmt.Errorf("%s has no required row that passed with a commit", id)
		}
	}
	return nil
}

func (s *s17cState) closeStillOpenRefused(reason string) error {
	r, err := s.runGate("close", s.scratch)
	if err != nil {
		return err
	}
	if r.code != 1 || !strings.Contains(r.stdout, "REFUSED "+reason) {
		return fmt.Errorf("close on the open record: exit %d, want 1 refused %s\n%s", r.code, reason, r.stdout)
	}
	return s.matchGolden("open_ready", "close", r)
}

func (s *s17cState) transitionStatus(line string) error {
	src, err := os.ReadFile(s.scratchRec)
	if err != nil {
		return err
	}
	text := string(src)
	if strings.Count(text, "\nClosure status: open\n") != 1 {
		return errors.New("the filled record has no single \"Closure status: open\" line")
	}
	text = strings.Replace(text, "\nClosure status: open\n", "\n"+line+"\n", 1)
	return os.WriteFile(s.scratchRec, []byte(text), 0o644)
}

func (s *s17cState) transitionedIsFixture(tree string) error {
	got, err := os.ReadFile(s.scratchRec)
	if err != nil {
		return err
	}
	want, err := os.ReadFile(filepath.Join(s.fixturePath(tree), filepath.FromSlash(s17cRecord)))
	if err != nil {
		return err
	}
	dropTitle := func(b []byte) string {
		_, rest, _ := strings.Cut(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		return rest
	}
	if dropTitle(got) != dropTitle(want) {
		return fmt.Errorf("the transitioned record differs from fixture %s beyond its title", tree)
	}
	return nil
}

func (s *s17cState) subExitsZeroGolden(sub, tree string) error {
	r, err := s.runGate(sub, s.scratch)
	if err != nil {
		return err
	}
	if r.code != 0 {
		return fmt.Errorf("%s on the closed record: exit %d, want 0\n%s", sub, r.code, r.stdout)
	}
	return s.matchGolden(tree, sub, r)
}

func (s *s17cState) gitClean(path string) error {
	out, err := s17cCommand(s.root, nil, "git", "ls-files", "--error-unmatch", "--", path)
	if err != nil {
		return fmt.Errorf("%s is not tracked: %v\n%s", path, err, out)
	}
	out, err = s17cCommand(s.root, nil, "git", "status", "--porcelain", "--untracked-files=all", "--", path)
	if err != nil {
		return fmt.Errorf("git status: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("%s differs from the commit:\n%s", path, out)
	}
	return nil
}

func (s *s17cState) immutableFixture(tree, result, status string) error {
	s.fixture = tree
	if err := s.gitClean(s17cFixtureDir + "/" + tree); err != nil {
		return err
	}
	got, rows, _, err := s.readFixture(tree)
	if err != nil {
		return err
	}
	if got != status {
		return fmt.Errorf("fixture %s reads %q, want %q", tree, got, status)
	}
	n := 0
	for _, r := range rows {
		if strings.HasPrefix(r.result, result) {
			n++
		} else if r.leg == "required" && r.result != "passed" {
			return fmt.Errorf("fixture %s row %s [%s] reads %q", tree, r.id, r.variant, r.result)
		}
	}
	if n != 1 {
		return fmt.Errorf("fixture %s has %d rows %q, want exactly one", tree, n, result)
	}
	return nil
}

func (s *s17cState) closeInPlace() error {
	var err error
	s.last, err = s.runGate("close", s.fixturePath(s.fixture))
	return err
}

func (s *s17cState) refusedNaming(reason, id string) error {
	if s.last.code == 0 || !strings.Contains(s.last.stdout, "REFUSED "+reason) || !strings.Contains(s.last.stdout, id+" [") {
		return fmt.Errorf("exit %d, want non-zero refused %s naming %s\n%s", s.last.code, reason, id, s.last.stdout)
	}
	return nil
}

func (s *s17cState) outputIsGolden(tree, sub string) error { return s.matchGolden(tree, sub, s.last) }

func (s *s17cState) fixtureUnchanged() error { return s.gitClean(s17cFixtureDir + "/" + s.fixture) }

// ---- Scenario: Copilot leg kept ----

func (s *s17cState) givenFixture(tree string) error {
	s.fixture = tree
	return s.gitClean(s17cFixtureDir + "/" + tree)
}

func (s *s17cState) closeReads(a, b, c string) error {
	s.reading = []string{a, b, c}
	for _, id := range s.reading {
		if !slices.Contains(s.liveIDs, id) {
			return fmt.Errorf("%s is not a DH-S-17 index ID", id)
		}
	}
	return nil
}

func (s *s17cState) liveHasLeg(variant, venue, leg string) error {
	for _, id := range s.reading {
		var found []s17cRow
		for _, r := range s.liveRows {
			if r.id == id && r.variant == variant {
				found = append(found, r)
			}
		}
		if len(found) != 1 {
			return fmt.Errorf("the committed record has %d %s rows of %s, want 1", len(found), variant, id)
		}
		if found[0].venue != venue || found[0].leg != leg || found[0].result == "" {
			return fmt.Errorf("the committed %s row of %s reads venue %q, leg %q, result %q", variant, id, found[0].venue, found[0].leg, found[0].result)
		}
	}
	return nil
}

func (s *s17cState) fixtureLegResult(variant string) error {
	_, rows, _, err := s.readFixture(s.fixture)
	if err != nil {
		return err
	}
	for _, id := range s.reading {
		i := slices.IndexFunc(rows, func(r s17cRow) bool { return r.id == id && r.variant == variant })
		if i < 0 {
			return fmt.Errorf("fixture %s has no %s row of %s", s.fixture, variant, id)
		}
		r := rows[i]
		if !(r.result == "passed" || strings.HasPrefix(r.result, "failed")) || r.commit == "" {
			return fmt.Errorf("fixture %s %s row of %s reads %q (commit %q)", s.fixture, variant, id, r.result, r.commit)
		}
	}
	return nil
}

func (s *s17cState) closeKeepsFailed(id string) error {
	_, rows, _, err := s.readFixture(s.fixture)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(rows, func(r s17cRow) bool {
		return r.id == id && r.variant == "copilot-cli" && strings.HasPrefix(r.result, "failed")
	}) {
		return fmt.Errorf("fixture %s does not record %s's copilot-cli leg as failed", s.fixture, id)
	}
	root, err := s.copyFixture(s.fixture)
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	r, err := s.runGate("close", root)
	if err != nil {
		return err
	}
	if r.code != 0 {
		return fmt.Errorf("close exit %d with the failed %s leg, want 0\n%s", r.code, id, r.stdout)
	}
	return s.matchGolden(s.fixture, "close", r)
}

// probeLeg edits the copilot-cli row of each read ID on a scratch copy of the fixture
// (deleting it, or marking it not run) and requires close to refuse naming that leg.
func (s *s17cState) probeLeg(variant, reason string, edit func(row string) (string, bool)) error {
	src, err := os.ReadFile(filepath.Join(s.fixturePath(s.fixture), filepath.FromSlash(s17cRecord)))
	if err != nil {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	for _, id := range s.reading {
		prefix := "| " + id + " | " + variant + " |"
		i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, prefix) })
		if i < 0 {
			return fmt.Errorf("fixture %s has no %s row of %s", s.fixture, variant, id)
		}
		edited := slices.Clone(lines)
		if repl, keep := edit(edited[i]); keep {
			edited[i] = repl
		} else {
			edited = slices.Delete(edited, i, i+1)
		}
		root, err := s.copyFixture(s.fixture)
		if err != nil {
			return err
		}
		err = os.WriteFile(filepath.Join(root, filepath.FromSlash(s17cRecord)), []byte(strings.Join(edited, "\n")), 0o644)
		var r s17cRun
		if err == nil {
			r, err = s.runGate("close", root)
		}
		os.RemoveAll(root)
		if err != nil {
			return err
		}
		if r.code != 1 || !strings.Contains(r.stdout, "REFUSED "+reason) || !strings.Contains(r.stdout, id+" ["+variant+"]") {
			return fmt.Errorf("%s leg of %s edited: close exit %d, want 1 refused %s naming %s [%s]\n%s", variant, id, r.code, reason, id, variant, r.stdout)
		}
	}
	return nil
}

func (s *s17cState) withoutLeg(variant, reason string) error {
	return s.probeLeg(variant, reason, func(string) (string, bool) { return "", false })
}

func (s *s17cState) legNotRun(variant, reason string) error {
	return s.probeLeg(variant, reason, func(row string) (string, bool) {
		cells := strings.Split(strings.Trim(row, "|"), "|")
		cells[5], cells[6], cells[7] = "  ", " not run (copilot not signed in) ", "  "
		return "|" + strings.Join(cells, "|") + "|", true
	})
}

func InitializeScenario_dh_s_17_acceptance_on_a_real_model_dh_s_17_closure_record(ctx *godog.ScenarioContext) {
	s := &s17cState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = s17cState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s.scratch != "" {
			os.RemoveAll(s.scratch)
		}
		return c, nil
	})

	ctx.Step(`^the committed DH-S-17 record and acceptance index of this checkout$`, s.givenCommittedRecord)
	ctx.Step(`^the committed fixtures "([^"]+)", "([^"]+)" and "([^"]+)" carry a DH-S-17 record in the committed record's form$`, s.fixturesInRecordForm)
	ctx.Step(`^the committed record's gate state agrees with its status line$`, s.liveGateAgrees)
	ctx.Step(`^"harness-gate ready --stage DH-S-17" runs on the filled record while it reads open$`, s.readyWhileOpen)
	ctx.Step(`^it exits 0 with the "([^"]+)" ready output of the golden$`, s.exitsZeroWithGolden)
	ctx.Step(`^every one of the (\d+) index IDs has a required row that passed$`, s.everyIDPassed)
	ctx.Step(`^"harness-gate close --stage DH-S-17" on the record still reading open is refused "([^"]+)"$`, s.closeStillOpenRefused)
	ctx.Step(`^the closing change sets only the status line to "([^"]+)"$`, s.transitionStatus)
	ctx.Step(`^the transitioned record is the committed fixture "([^"]+)" but for its title$`, s.transitionedIsFixture)
	ctx.Step(`^"harness-gate ready --stage DH-S-17" exits 0 with the "([^"]+)" ready output of the golden$`, func(tree string) error { return s.subExitsZeroGolden("ready", tree) })
	ctx.Step(`^"harness-gate close --stage DH-S-17" exits 0 with the "([^"]+)" close output of the golden$`, func(tree string) error { return s.subExitsZeroGolden("close", tree) })
	ctx.Step(`^the immutable fixture "([^"]+)" with exactly one scenario row "([^"]+)" and status "([^"]+)"$`, s.immutableFixture)
	ctx.Step(`^"harness-gate close --stage DH-S-17" runs on the fixture in place$`, s.closeInPlace)
	ctx.Step(`^it exits non-zero refused "([^"]+)" naming "([^"]+)"$`, s.refusedNaming)
	ctx.Step(`^its output is the "([^"]+)" close output of the golden$`, func(tree string) error { return s.outputIsGolden(tree, "close") })
	ctx.Step(`^the fixture is unchanged from the commit$`, s.fixtureUnchanged)

	ctx.Step(`^the committed fixture "([^"]+)"$`, s.givenFixture)
	ctx.Step(`^the close check reads "([^"]+)", "([^"]+)" and "([^"]+)"$`, s.closeReads)
	ctx.Step(`^each has exactly one "([^"]+)" row with venue "([^"]+)" and leg "([^"]+)" in the committed record$`, s.liveHasLeg)
	ctx.Step(`^each has a "([^"]+)" row in the fixture whose result is passed or failed, never missing$`, s.fixtureLegResult)
	ctx.Step(`^"harness-gate close --stage DH-S-17" on the fixture exits 0 with the failed "([^"]+)" leg kept$`, s.closeKeepsFailed)
	ctx.Step(`^the check is live: without the "([^"]+)" row of each, close is refused "([^"]+)" naming it$`, s.withoutLeg)
	ctx.Step(`^the check is live: with the "([^"]+)" row of each "not run", close is refused "([^"]+)" naming it$`, s.legNotRun)
}

func TestE2E_dh_s_17_acceptance_on_a_real_model_dh_s_17_closure_record(t *testing.T) {
	t.Cleanup(func() {
		if s17cGateTmp != "" {
			os.RemoveAll(s17cGateTmp)
		}
	})
	suite := godog.TestSuite{
		Name:                "dh_s_17_acceptance_on_a_real_model_dh_s_17_closure_record",
		ScenarioInitializer: InitializeScenario_dh_s_17_acceptance_on_a_real_model_dh_s_17_closure_record,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_17_acceptance_on_a_real_model_dh_s_17_closure_record.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
