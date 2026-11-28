//go:build e2e

package e2e

// Step definitions of dh_s_18_acceptance_on_real_operating_systems_dh_s_18_closure_record.feature:
// the DH-S-18 close (implementation-plan Stage 8.10, M2-DOD-4, M2-C-2). proof: in-process
// and golden -- the built harness-gate runs over scratch trees holding the committed
// acceptance index and variants.md and the committed fixture
// tools/harness-gate/testdata/closure/s18, and its output is compared with
// expected_s18.golden. The committed record is checked to carry the fixture's rows and
// its gate state to agree with its status line; it is never closed by this suite.

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
	s18closeStage    = "DH-S-18"
	s18closeRecord   = "docs/stories/harness-DSH-M2/evidence/DH-S-18.md"
	s18closeVariants = "docs/stories/harness-DSH-M2/evidence/variants.md"
	s18closeIndex    = "test/harness/acceptance-index.yaml"
	s18closeFixture  = "tools/harness-gate/testdata/closure/s18"
	s18closeGolden   = "tools/harness-gate/testdata/closure/expected_s18.golden"
	// s18closeNotRunRow is the row stageclose_test.go's not-ready states rewrite, so the
	// gate's output over it is the golden's.
	s18closeNotRunPrefix = "| DH-E-EXEC-10 | linux-e2:no-user-manager | acceptance | required |"
	s18closeNotRunRow    = s18closeNotRunPrefix + " E2 linux-e2-no-user-manager |  | not run (no-user-manager machine not provisioned) |  |"
)

var (
	s18closeGateOnce sync.Once
	s18closeGateBin  string
	s18closeGateErr  error
	s18closeGateTmp  string
)

type s18closeRow struct {
	id, variant, venue, leg, env, commit, result, artifacts string
}

func (r s18closeRow) key() string { return r.id + "|" + r.variant + "|" + r.venue + "|" + r.leg }

// s18closeVariant is one row of variants.md's Variants table.
type s18closeVariant struct {
	label     string
	scenarios []string
	inScope   bool
}

type s18closeRun struct {
	code   int
	stdout string
}

type s18closeState struct {
	root string

	index      string // committed acceptance index (HEAD)
	variantsMD string // committed variants.md (HEAD)
	liveStatus string
	liveRows   []s18closeRow
	ids        []string
	fleet      []s18closeVariant

	fixtureSrc  string // committed fixture record, LF
	fixtureRows []s18closeRow

	scratches []string
	scratch   string // scratch root the current scenario edits
	edit      func([]string) ([]string, error)
	variantID string
	variant   string
	last      s18closeRun
	golden    map[string]s18closeRun
}

func s18closeIsRegular(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func s18closeRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if s18closeIsRegular(filepath.Join(dir, "go.work")) && s18closeIsRegular(filepath.Join(dir, filepath.FromSlash(s18closeIndex))) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no repository root (go.work + test/harness/acceptance-index.yaml) above the working directory")
		}
		dir = parent
	}
}

func s18closeCommand(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func s18closeLF(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

// gate builds harness-gate from this checkout once per test binary (GOWORK=off: the
// tool is its own module, as its README runs it).
func (s *s18closeState) gate() (string, error) {
	s18closeGateOnce.Do(func() {
		tmp, err := os.MkdirTemp("", "s18close-gate-")
		if err != nil {
			s18closeGateErr = err
			return
		}
		s18closeGateTmp = tmp
		bin := filepath.Join(tmp, "harness-gate")
		if filepath.Separator == '\\' {
			bin += ".exe"
		}
		out, err := s18closeCommand(filepath.Join(s.root, "tools", "harness-gate"), []string{"GOWORK=off", "GOFLAGS="}, "go", "build", "-o", bin, ".")
		if err != nil {
			s18closeGateErr = fmt.Errorf("go build harness-gate: %v\n%s", err, out)
			return
		}
		s18closeGateBin = bin
	})
	return s18closeGateBin, s18closeGateErr
}

func (s *s18closeState) runGate(sub, root string) (s18closeRun, error) {
	bin, err := s.gate()
	if err != nil {
		return s18closeRun{}, err
	}
	cmd := exec.Command(bin, sub, "--root", root, "--stage", s18closeStage)
	cmd.Dir = root
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return s18closeRun{}, fmt.Errorf("run harness-gate %s: %v", sub, err)
		}
		code = ee.ExitCode()
	}
	if code == 2 {
		return s18closeRun{}, fmt.Errorf("harness-gate %s exited 2 (usage)\nstdout:\n%s\nstderr:\n%s", sub, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		return s18closeRun{}, fmt.Errorf("harness-gate %s wrote to stderr:\n%s", sub, stderr.String())
	}
	return s18closeRun{code: code, stdout: s18closeLF(stdout.String())}, nil
}

// s18closeParse returns a record's status value and its Scenarios table rows.
func s18closeParse(src string) (string, []s18closeRow, error) {
	var status string
	var rows []s18closeRow
	in, fence := false, false
	for _, l := range strings.Split(s18closeLF(src), "\n") {
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
		rows = append(rows, s18closeRow{cells[0], cells[1], cells[2], cells[3], cells[4], cells[5], cells[6], cells[7]})
	}
	if status == "" {
		return "", nil, errors.New("no status line")
	}
	return status, rows, nil
}

var s18closeIndexID = regexp.MustCompile(`^\s+- id: (DH-E-[A-Z0-9-]+)\s*$`)

// s18closeStageIDs returns the IDs the acceptance index lists under DH-S-18.
func s18closeStageIDs(src string) []string {
	var ids []string
	in := false
	for _, l := range strings.Split(s18closeLF(src), "\n") {
		if strings.HasPrefix(l, "  DH-S-") {
			in = strings.TrimSpace(l) == s18closeStage+":"
			continue
		}
		if len(l) > 0 && l[0] != ' ' && l[0] != '#' {
			in = false
		}
		if in {
			if m := s18closeIndexID.FindStringSubmatch(l); m != nil {
				ids = append(ids, m[1])
			}
		}
	}
	slices.Sort(ids)
	return ids
}

// s18closeParseVariants reads the Variants table of variants.md.
func s18closeParseVariants(src string) ([]s18closeVariant, error) {
	var out []s18closeVariant
	in := false
	for _, l := range strings.Split(s18closeLF(src), "\n") {
		if strings.HasPrefix(l, "## ") {
			in = strings.TrimSpace(l) == "## Variants"
			continue
		}
		if !in || !strings.HasPrefix(l, "| `") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(l), "|"), "|")
		if len(cells) != 8 {
			return nil, fmt.Errorf("variants row with %d cells, want 8: %q", len(cells), l)
		}
		v := s18closeVariant{label: strings.Trim(strings.TrimSpace(cells[0]), "`")}
		for _, sc := range strings.Split(cells[6], ",") {
			if sc = strings.TrimSpace(sc); sc != "" {
				v.scenarios = append(v.scenarios, sc)
			}
		}
		switch scope := strings.TrimSpace(cells[7]); {
		case scope == "in scope":
			v.inScope = true
		case strings.HasPrefix(scope, "out of scope ("):
		default:
			return nil, fmt.Errorf("variant %s has scope %q", v.label, scope)
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, errors.New("no Variants table")
	}
	return out, nil
}

// s18closeLabel maps a record row's variant to its variants.md label: the record keeps the
// labels it was created with (Stage 1.1), read as its Scenarios intro says; the Linux
// (E2) aggregate rows map to none.
func s18closeLabel(scenario, label string) string {
	switch label {
	case "Linux (WSL)":
		switch scenario {
		case "DH-E-CHOKIDAR-2":
			return "linux-wsl:smb"
		case "DH-E-STORAGE-6":
			return "linux-wsl:two-account"
		}
		return "linux-wsl"
	case "Windows":
		switch scenario {
		case "DH-E-FS-2":
			return "windows-host"
		case "DH-E-CHOKIDAR-2":
			return "windows-host:smb"
		case "DH-E-STORAGE-6":
			return "windows-host:two-account"
		}
		return "windows-hosted"
	case "Linux ext4":
		return "linux-wsl"
	case "Windows NTFS":
		return "windows-hosted"
	case "Linux guest":
		return "vm:linux-guest"
	case "Windows guest":
		return "vm:windows-guest"
	case "macOS":
		return "macos"
	case "Linux (E2)", "Linux (E2, over SSH)":
		return ""
	}
	return label
}

func (s *s18closeState) variantOf(label string) (s18closeVariant, bool) {
	i := slices.IndexFunc(s.fleet, func(v s18closeVariant) bool { return v.label == label })
	if i < 0 {
		return s18closeVariant{}, false
	}
	return s.fleet[i], true
}

func s18closeKeys(rows []s18closeRow) []string {
	var keys []string
	for _, r := range rows {
		keys = append(keys, r.key())
	}
	slices.Sort(keys)
	return keys
}

func (s *s18closeState) gitShow(rel string) (string, error) {
	out, err := s18closeCommand(s.root, nil, "git", "show", "HEAD:"+rel)
	if err != nil {
		return "", fmt.Errorf("git show HEAD:%s: %v\n%s", rel, err, out)
	}
	work, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	if s18closeLF(string(work)) != s18closeLF(out) {
		return "", fmt.Errorf("%s differs from HEAD: the gate would read an uncommitted file", rel)
	}
	return s18closeLF(out), nil
}

func (s *s18closeState) gitClean(rel string) error {
	out, err := s18closeCommand(s.root, nil, "git", "ls-files", "--error-unmatch", "--", rel)
	if err != nil {
		return fmt.Errorf("%s is not tracked: %v\n%s", rel, err, out)
	}
	out, err = s18closeCommand(s.root, nil, "git", "status", "--porcelain", "--untracked-files=all", "--", rel)
	if err != nil {
		return fmt.Errorf("git status: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("%s differs from the commit:\n%s", rel, out)
	}
	return nil
}

// tree writes a scratch root holding the committed index and variants.md and the
// fixture record with the given status line and the scenario's edit applied.
func (s *s18closeState) tree(status string, edit func([]string) ([]string, error)) (string, error) {
	src := s.fixtureSrc
	if strings.Count(src, "\nClosure status: open\n") != 1 {
		return "", errors.New("the fixture has no single \"Closure status: open\" line")
	}
	src = strings.Replace(src, "\nClosure status: open\n", "\nClosure status: "+status+"\n", 1)
	if edit != nil {
		lines, err := edit(strings.Split(src, "\n"))
		if err != nil {
			return "", err
		}
		src = strings.Join(lines, "\n")
	}
	root, err := os.MkdirTemp("", "s18close-tree-")
	if err != nil {
		return "", err
	}
	s.scratches = append(s.scratches, root)
	for rel, body := range map[string]string{s18closeRecord: src, s18closeIndex: s.index, s18closeVariants: s.variantsMD} {
		dst := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, []byte(body), 0o644); err != nil {
			return "", err
		}
	}
	return root, nil
}

var s18closeGoldenHead = regexp.MustCompile(`^== (\S+): (ready|close) --stage DH-S-18 \(exit (\d+)\)$`)

func (s *s18closeState) loadGolden() error {
	if s.golden != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(s18closeGolden)))
	if err != nil {
		return err
	}
	s.golden = map[string]s18closeRun{}
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
	for _, l := range strings.SplitAfter(s18closeLF(string(data)), "\n") {
		if m := s18closeGoldenHead.FindStringSubmatch(strings.TrimSuffix(l, "\n")); m != nil {
			flush()
			key = m[1] + "/" + m[2]
			var code int
			fmt.Sscanf(m[3], "%d", &code)
			s.golden[key] = s18closeRun{code: code}
			continue
		}
		if strings.HasPrefix(l, "stderr: ") {
			return fmt.Errorf("golden section %s records stderr", key)
		}
		body.WriteString(l)
	}
	flush()
	if len(s.golden) != 8 {
		return fmt.Errorf("%s has %d sections, want 8 (4 states x ready, close)", s18closeGolden, len(s.golden))
	}
	return nil
}

func (s *s18closeState) matchGolden(state, sub string, got s18closeRun) error {
	if err := s.loadGolden(); err != nil {
		return err
	}
	want, ok := s.golden[state+"/"+sub]
	if !ok {
		return fmt.Errorf("%s has no section %s: %s", s18closeGolden, state, sub)
	}
	if got.code != want.code || got.stdout != want.stdout {
		return fmt.Errorf("%s %s: exit %d, want %d\n--- got\n%s--- want\n%s", state, sub, got.code, want.code, got.stdout, want.stdout)
	}
	return nil
}

// ---- Background ----

func (s *s18closeState) givenCommitted() error {
	root, err := s18closeRepoRoot()
	if err != nil {
		return err
	}
	s.root = root
	rec, err := s.gitShow(s18closeRecord)
	if err != nil {
		return err
	}
	if s.liveStatus, s.liveRows, err = s18closeParse(rec); err != nil {
		return fmt.Errorf("%s: %v", s18closeRecord, err)
	}
	if s.index, err = s.gitShow(s18closeIndex); err != nil {
		return err
	}
	if s.ids = s18closeStageIDs(s.index); len(s.ids) != 19 {
		return fmt.Errorf("the committed index lists %d DH-S-18 IDs, want 19: %v", len(s.ids), s.ids)
	}
	if s.variantsMD, err = s.gitShow(s18closeVariants); err != nil {
		return err
	}
	if s.fleet, err = s18closeParseVariants(s.variantsMD); err != nil {
		return fmt.Errorf("%s: %v", s18closeVariants, err)
	}
	for _, id := range s.ids {
		if !slices.ContainsFunc(s.liveRows, func(r s18closeRow) bool { return r.id == id }) {
			return fmt.Errorf("the committed record has no row of index ID %s", id)
		}
	}
	return nil
}

func (s *s18closeState) fixtureCarriesLiveRows(tree string) error {
	rel := s18closeFixture
	if tree != "s18" {
		return fmt.Errorf("unknown fixture %q", tree)
	}
	if err := s.gitClean(rel); err != nil {
		return err
	}
	src, err := s.gitShow(rel + "/" + s18closeRecord)
	if err != nil {
		return err
	}
	status, rows, err := s18closeParse(src)
	if err != nil {
		return fmt.Errorf("fixture %s: %v", tree, err)
	}
	if status != "open" {
		return fmt.Errorf("fixture %s reads %q, want open", tree, status)
	}
	if got, want := s18closeKeys(rows), s18closeKeys(s.liveRows); !slices.Equal(got, want) {
		return fmt.Errorf("fixture %s scenario rows (id|variant|venue|leg)\n%v\nare not the committed record's\n%v", tree, got, want)
	}
	s.fixtureSrc, s.fixtureRows = src, rows
	return nil
}

// ---- Scenario: No partial closure ----

func (s *s18closeState) givenNotRun(variant, id, result string) error {
	if !slices.ContainsFunc(s.fixtureRows, func(r s18closeRow) bool { return r.id == id && r.variant == variant }) {
		return fmt.Errorf("the fixture has no %s row of %s", variant, id)
	}
	if !strings.HasPrefix(s18closeNotRunRow, "| "+id+" | "+variant+" |") || !strings.Contains(s18closeNotRunRow, "| "+result+" (") {
		return fmt.Errorf("the golden's not-ready row %q is not the %s %s row reading %s", s18closeNotRunRow, id, variant, result)
	}
	s.variantID, s.variant = id, variant
	s.edit = func(lines []string) ([]string, error) {
		i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, s18closeNotRunPrefix) })
		if i < 0 {
			return nil, fmt.Errorf("fixture has no row %q", s18closeNotRunPrefix)
		}
		lines[i] = s18closeNotRunRow
		return lines, nil
	}
	return nil
}

func (s *s18closeState) variantInScope(id string) error {
	v, ok := s.variantOf(s.variant)
	if !ok {
		return fmt.Errorf("variants.md has no variant %s", s.variant)
	}
	if !v.inScope || !slices.Contains(v.scenarios, id) {
		return fmt.Errorf("variants.md variant %s: in scope %v, scenarios %v; want in scope listing %s", s.variant, v.inScope, v.scenarios, id)
	}
	return nil
}

func s18closeOutOfScope(r s18closeRow) bool { return strings.HasPrefix(r.result, "out of scope (") }

func (s *s18closeState) othersPassed() error {
	for _, r := range s.fixtureRows {
		if r.id == s.variantID && r.variant == s.variant {
			continue
		}
		if s18closeOutOfScope(r) {
			continue
		}
		if r.result != "passed" || r.commit == "" {
			return fmt.Errorf("fixture row %s [%s] reads %q (commit %q)", r.id, r.variant, r.result, r.commit)
		}
	}
	return nil
}

func (s *s18closeState) runOn(sub, status string) error {
	root, err := s.tree(status, s.edit)
	if err != nil {
		return err
	}
	s.last, err = s.runGate(sub, root)
	return err
}

func (s *s18closeState) notReadyOneBlocker(code int, marker, named, kind string) error {
	if s.last.code != code || !strings.Contains(s.last.stdout, marker+", 1 blocker:") {
		return fmt.Errorf("exit %d, want %d %q with one blocker\n%s", s.last.code, code, marker, s.last.stdout)
	}
	blockers := 0
	for _, l := range strings.Split(s.last.stdout, "\n") {
		if strings.HasPrefix(l, "  - ") {
			blockers++
			if !strings.HasPrefix(l, "  - "+named+": ") || !strings.Contains(l, kind) {
				return fmt.Errorf("blocker %q does not name %s as %s", l, named, kind)
			}
		}
	}
	if blockers != 1 {
		return fmt.Errorf("%d blocker lines, want 1\n%s", blockers, s.last.stdout)
	}
	return nil
}

func (s *s18closeState) refusedNaming(code int, reason, named string) error {
	if s.last.code != code || !strings.Contains(s.last.stdout, "REFUSED "+reason) || !strings.Contains(s.last.stdout, "  - "+named+": ") {
		return fmt.Errorf("exit %d, want %d refused %s naming %s\n%s", s.last.code, code, reason, named, s.last.stdout)
	}
	return nil
}

func (s *s18closeState) outputIsGolden(state, sub string) error {
	return s.matchGolden(state, sub, s.last)
}

// liveProbe sets each row of id alone to not run on a scratch tree and requires ready
// to refuse naming exactly that row's variant.
func (s *s18closeState) liveProbe(id, _ string) error {
	n := 0
	for _, r := range s.fixtureRows {
		if r.id != id {
			continue
		}
		n++
		prefix := "| " + r.id + " | " + r.variant + " | " + r.venue + " | " + r.leg + " |"
		root, err := s.tree("open", func(lines []string) ([]string, error) {
			i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, prefix) })
			if i < 0 {
				return nil, fmt.Errorf("fixture has no row %q", prefix)
			}
			lines[i] = prefix + " " + r.env + " |  | not run (probe preflight failed) |  |"
			return lines, nil
		})
		if err != nil {
			return err
		}
		if s.last, err = s.runGate("ready", root); err != nil {
			return err
		}
		if err := s.notReadyOneBlocker(1, "NOT READY", id+" ["+r.variant+"]", "not run"); err != nil {
			return fmt.Errorf("%s [%s] not run: %v", id, r.variant, err)
		}
	}
	if n < 2 {
		return fmt.Errorf("the fixture has %d rows of %s, want at least 2 to probe", n, id)
	}
	return nil
}

// ---- Scenario: S18 ready then closed ----

func (s *s18closeState) fixtureAllPassed(tree string, n int) error {
	if tree != "s18" {
		return fmt.Errorf("unknown fixture %q", tree)
	}
	if len(s.ids) != n {
		return fmt.Errorf("the index lists %d DH-S-18 scenarios, want %d", len(s.ids), n)
	}
	passed := map[[2]string]bool{}
	for _, r := range s.fixtureRows {
		if !slices.Contains(s.ids, r.id) {
			return fmt.Errorf("fixture row %s is not a DH-S-18 index ID", r.id)
		}
		label := s18closeLabel(r.id, r.variant)
		if s18closeOutOfScope(r) {
			if v, ok := s.variantOf(label); !ok || v.inScope {
				return fmt.Errorf("fixture row %s [%s] reads out of scope, but variants.md %s is not an out-of-scope variant", r.id, r.variant, label)
			}
			continue
		}
		if r.leg != "required" || r.result != "passed" || r.commit == "" {
			return fmt.Errorf("fixture row %s [%s] reads leg %q result %q commit %q", r.id, r.variant, r.leg, r.result, r.commit)
		}
		passed[[2]string{r.id, label}] = true
	}
	var missing []string
	covered := map[string]bool{}
	for _, v := range s.fleet {
		if !v.inScope {
			continue
		}
		for _, id := range v.scenarios {
			covered[id] = true
			if !passed[[2]string{id, v.label}] {
				missing = append(missing, id+" ["+v.label+"]")
			}
		}
	}
	for _, id := range s.ids {
		if !covered[id] {
			missing = append(missing, id+" has no in-scope variant")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the fixture has no passed row of: %s", strings.Join(missing, "; "))
	}
	s.edit = nil
	return nil
}

func (s *s18closeState) liveGateAgrees() error {
	ready, err := s.runGate("ready", s.root)
	if err != nil {
		return err
	}
	closed, err := s.runGate("close", s.root)
	if err != nil {
		return err
	}
	open := slices.ContainsFunc(s.liveRows, func(r s18closeRow) bool {
		return r.leg == "required" && r.result != "passed" && !s18closeOutOfScope(r)
	})
	switch s.liveStatus {
	case "open":
		if closed.code != 1 || !strings.Contains(closed.stdout, "REFUSED not-yet-closed") {
			return fmt.Errorf("the committed record reads open: close exit %d, want 1 refused not-yet-closed\n%s", closed.code, closed.stdout)
		}
		if open && ready.code != 1 {
			return fmt.Errorf("the committed record has required rows not passed but ready exits %d\n%s", ready.code, ready.stdout)
		}
		if !open && ready.code != 0 {
			return fmt.Errorf("the committed record has every required row passed but ready exits %d\n%s", ready.code, ready.stdout)
		}
		for _, r := range s.liveRows {
			if strings.HasPrefix(r.result, "not run") && !strings.Contains(ready.stdout, r.id+" ["+r.variant+"]") {
				return fmt.Errorf("ready does not name the not-run row %s [%s]\n%s", r.id, r.variant, ready.stdout)
			}
		}
	case "closed":
		if ready.code != 0 || closed.code != 0 || open {
			return fmt.Errorf("the committed record reads closed: ready exit %d, close exit %d, rows open %v; want 0, 0, none\n%s%s", ready.code, closed.code, open, ready.stdout, closed.stdout)
		}
	default:
		return fmt.Errorf("the committed record's status line reads %q", s.liveStatus)
	}
	return nil
}

func (s *s18closeState) readyWhileOpen() error {
	root, err := s.tree("open", nil)
	if err != nil {
		return err
	}
	s.scratch = root
	s.last, err = s.runGate("ready", root)
	return err
}

func (s *s18closeState) exitsZeroGolden(state string) error {
	if s.last.code != 0 {
		return fmt.Errorf("exit %d, want 0\n%s", s.last.code, s.last.stdout)
	}
	return s.matchGolden(state, "ready", s.last)
}

func (s *s18closeState) closeStillOpenRefused(reason string) error {
	r, err := s.runGate("close", s.scratch)
	if err != nil {
		return err
	}
	if r.code != 1 || !strings.Contains(r.stdout, "REFUSED "+reason) {
		return fmt.Errorf("close on the open record: exit %d, want 1 refused %s\n%s", r.code, reason, r.stdout)
	}
	return s.matchGolden("open_ready", "close", r)
}

func (s *s18closeState) scratchRecord() string {
	return filepath.Join(s.scratch, filepath.FromSlash(s18closeRecord))
}

func (s *s18closeState) transitionStatus(line string) error {
	src, err := os.ReadFile(s.scratchRecord())
	if err != nil {
		return err
	}
	text := string(src)
	if strings.Count(text, "\nClosure status: open\n") != 1 {
		return errors.New("the filled record has no single \"Closure status: open\" line")
	}
	text = strings.Replace(text, "\nClosure status: open\n", "\n"+line+"\n", 1)
	return os.WriteFile(s.scratchRecord(), []byte(text), 0o644)
}

func (s *s18closeState) onlyStatusDiffers() error {
	got, err := os.ReadFile(s.scratchRecord())
	if err != nil {
		return err
	}
	a, b := strings.Split(string(got), "\n"), strings.Split(s.fixtureSrc, "\n")
	if len(a) != len(b) {
		return fmt.Errorf("the transitioned record has %d lines, the fixture %d", len(a), len(b))
	}
	var diff []string
	for i := range a {
		if a[i] != b[i] {
			diff = append(diff, fmt.Sprintf("%d: %q -> %q", i+1, b[i], a[i]))
		}
	}
	if len(diff) != 1 || !strings.Contains(diff[0], `"Closure status: open" -> "Closure status: closed"`) {
		return fmt.Errorf("the transitioned record differs from the fixture in: %v", diff)
	}
	return nil
}

func (s *s18closeState) subExitsZeroGolden(sub, state string) error {
	r, err := s.runGate(sub, s.scratch)
	if err != nil {
		return err
	}
	if r.code != 0 {
		return fmt.Errorf("%s on the closed record: exit %d, want 0\n%s", sub, r.code, r.stdout)
	}
	return s.matchGolden(state, sub, r)
}

func (s *s18closeState) fixtureUnchanged(tree string) error {
	if tree != "s18" {
		return fmt.Errorf("unknown fixture %q", tree)
	}
	return s.gitClean(s18closeFixture)
}

func InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_dh_s_18_closure_record(ctx *godog.ScenarioContext) {
	s := &s18closeState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = s18closeState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		for _, d := range s.scratches {
			os.RemoveAll(d)
		}
		return c, nil
	})

	ctx.Step(`^the committed DH-S-18 record, acceptance index and variants\.md of this checkout$`, s.givenCommitted)
	ctx.Step(`^the committed fixture "([^"]+)" carries the committed record's scenario rows$`, s.fixtureCarriesLiveRows)

	ctx.Step(`^a record fixture where the "([^"]+)" variant of "([^"]+)" is "([^"]+)"$`, s.givenNotRun)
	ctx.Step(`^that variant is in scope for "([^"]+)" in variants\.md$`, s.variantInScope)
	ctx.Step(`^every other in-scope row of the fixture passed$`, s.othersPassed)
	ctx.Step(`^"harness-gate (ready|close) --stage DH-S-18" runs on the record reading "(open|closed)"$`, s.runOn)
	ctx.Step(`^it exits (\d+) "([^"]+)" with exactly one blocker, naming "([^"]+)" as "([^"]+)"$`, s.notReadyOneBlocker)
	ctx.Step(`^it exits (\d+) refused "([^"]+)" naming "([^"]+)"$`, s.refusedNaming)
	ctx.Step(`^its output is the "([^"]+)" (ready|close) output of the golden$`, s.outputIsGolden)
	ctx.Step(`^the refusal is live: each row of "([^"]+)" alone set "(not run)" makes ready refuse S18 naming that row's variant$`, s.liveProbe)

	ctx.Step(`^the committed fixture "([^"]+)" with all (\d+) scenarios passed on every in-scope variant of variants\.md$`, s.fixtureAllPassed)
	ctx.Step(`^the committed record's gate state agrees with its status line$`, s.liveGateAgrees)
	ctx.Step(`^"harness-gate ready --stage DH-S-18" runs on the filled record while it reads open$`, s.readyWhileOpen)
	ctx.Step(`^it exits 0 with the "([^"]+)" ready output of the golden$`, s.exitsZeroGolden)
	ctx.Step(`^"harness-gate close --stage DH-S-18" on the record still reading open is refused "([^"]+)"$`, s.closeStillOpenRefused)
	ctx.Step(`^the closing change sets only the status line to "([^"]+)"$`, s.transitionStatus)
	ctx.Step(`^the transitioned record differs from the fixture only in its status line$`, s.onlyStatusDiffers)
	ctx.Step(`^"harness-gate (ready|close) --stage DH-S-18" exits 0 with the "([^"]+)" (?:ready|close) output of the golden$`, s.subExitsZeroGolden)
	ctx.Step(`^the committed fixture "([^"]+)" is unchanged from the commit$`, s.fixtureUnchanged)
}

func TestE2E_dh_s_18_acceptance_on_real_operating_systems_dh_s_18_closure_record(t *testing.T) {
	t.Cleanup(func() {
		if s18closeGateTmp != "" {
			os.RemoveAll(s18closeGateTmp)
		}
	})
	suite := godog.TestSuite{
		Name:                "dh_s_18_acceptance_on_real_operating_systems_dh_s_18_closure_record",
		ScenarioInitializer: InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_dh_s_18_closure_record,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_18_acceptance_on_real_operating_systems_dh_s_18_closure_record.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
