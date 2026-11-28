package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/variants"
)

var updateGolden = flag.Bool("update", false, "rewrite the committed goldens under testdata/ (refused under CI)")

// closureTree copies a fixture tree into a fresh temp dir so a run cannot touch testdata.
func closureTree(t *testing.T, dir string) string {
	t.Helper()
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS(dir)); err != nil {
		t.Fatalf("copy %s: %v", dir, err)
	}
	return tmp
}

func runClosureGate(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestStageClosureGolden runs ready and close over the four record states of
// implementation-plan Stage 1.1 step 5: open and not ready, open and ready, closed
// and ready, closed and not ready.
func TestStageClosureGolden(t *testing.T) {
	trees := []string{"open_not_ready", "open_ready", "closed_ready", "closed_not_ready"}
	var got strings.Builder
	for _, tree := range trees {
		for _, sub := range []string{"ready", "close"} {
			root := closureTree(t, filepath.Join("testdata", "closure", tree))
			code, stdout, stderr := runClosureGate(t, sub, "--root", root, "--stage", "DH-S-16", "--built")
			fmt.Fprintf(&got, "== %s: %s --built (exit %d)\n%s", tree, sub, code, stdout)
			if stderr != "" {
				fmt.Fprintf(&got, "stderr: %s", stderr)
			}
		}
	}
	golden := filepath.Join("testdata", "closure", "expected.golden")
	if *updateGolden {
		if os.Getenv("CI") != "" {
			t.Fatal("-update is refused under CI: regenerate the golden locally and review the diff")
		}
		if err := os.WriteFile(golden, []byte(got.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update locally to create it): %v", err)
	}
	if norm := strings.ReplaceAll(string(want), "\r\n", "\n"); norm != got.String() {
		t.Errorf("stage-closure output differs from %s\n--- got\n%s--- want\n%s", golden, got.String(), norm)
	}
}

// The exit codes the golden records, asserted directly so a regenerated golden
// cannot silently flip a refusal into a pass.
func TestStageClosureExitCodes(t *testing.T) {
	cases := []struct {
		tree, sub string
		code      int
		marker    string
	}{
		{"open_not_ready", "ready", 1, "NOT READY"},
		{"open_ready", "ready", 0, ": ready;"},
		{"closed_ready", "ready", 0, ": ready;"},
		{"closed_not_ready", "ready", 1, "NOT READY"},
		{"open_not_ready", "close", 1, "REFUSED " + closure.NotYetClosed},
		{"open_ready", "close", 1, "REFUSED " + closure.NotYetClosed},
		{"closed_ready", "close", 0, ": closed;"},
		{"closed_not_ready", "close", 1, "REFUSED " + closure.ClosedButNotReady},
	}
	for _, c := range cases {
		t.Run(c.tree+"/"+c.sub, func(t *testing.T) {
			root := closureTree(t, filepath.Join("testdata", "closure", c.tree))
			code, stdout, stderr := runClosureGate(t, c.sub, "--root", root, "--stage", "DH-S-16", "--built")
			if code != c.code || !strings.Contains(stdout, c.marker) {
				t.Errorf("exit %d, want %d; stdout must contain %q\nstdout:\n%s\nstderr:\n%s", code, c.code, c.marker, stdout, stderr)
			}
		})
	}
}

// TestStageClosureS17Golden is the DH-S-17 acceptance close (implementation-plan Stage
// 7.7, M2-C-2, M2-DOD-3): ready then close, without --built, over the four record
// states. closed_not_ready (one row not run, status closed) is refused
// closed-but-not-ready.
func TestStageClosureS17Golden(t *testing.T) {
	trees := []string{"open_not_ready", "open_ready", "closed_ready", "closed_not_ready"}
	var got strings.Builder
	for _, tree := range trees {
		for _, sub := range []string{"ready", "close"} {
			root := closureTree(t, filepath.Join("testdata", "closure", tree))
			code, stdout, stderr := runClosureGate(t, sub, "--root", root, "--stage", "DH-S-17")
			fmt.Fprintf(&got, "== %s: %s --stage DH-S-17 (exit %d)\n%s", tree, sub, code, stdout)
			if stderr != "" {
				fmt.Fprintf(&got, "stderr: %s", stderr)
			}
		}
	}
	golden := filepath.Join("testdata", "closure", "expected_s17.golden")
	if *updateGolden {
		if os.Getenv("CI") != "" {
			t.Fatal("-update is refused under CI: regenerate the golden locally and review the diff")
		}
		if err := os.WriteFile(golden, []byte(got.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update locally to create it): %v", err)
	}
	if norm := strings.ReplaceAll(string(want), "\r\n", "\n"); norm != got.String() {
		t.Errorf("DH-S-17 closure output differs from %s\n--- got\n%s--- want\n%s", golden, got.String(), norm)
	}
}

// The DH-S-17 exit codes, asserted directly so a regenerated golden cannot flip a
// refusal into a pass.
func TestStageClosureS17ExitCodes(t *testing.T) {
	cases := []struct {
		tree, sub string
		code      int
		marker    string
	}{
		{"open_not_ready", "ready", 1, "NOT READY"},
		{"open_ready", "ready", 0, ": ready;"},
		{"closed_ready", "ready", 0, ": ready;"},
		{"closed_not_ready", "ready", 1, "NOT READY"},
		{"open_not_ready", "close", 1, "REFUSED " + closure.NotYetClosed},
		{"open_ready", "close", 1, "REFUSED " + closure.NotYetClosed},
		{"closed_ready", "close", 0, ": closed;"},
		{"closed_not_ready", "close", 1, "REFUSED " + closure.ClosedButNotReady},
	}
	for _, c := range cases {
		t.Run(c.tree+"/"+c.sub, func(t *testing.T) {
			root := closureTree(t, filepath.Join("testdata", "closure", c.tree))
			code, stdout, stderr := runClosureGate(t, c.sub, "--root", root, "--stage", "DH-S-17")
			if code != c.code || !strings.Contains(stdout, c.marker) {
				t.Errorf("exit %d, want %d; stdout must contain %q\nstdout:\n%s\nstderr:\n%s", code, c.code, c.marker, stdout, stderr)
			}
		})
	}
}

// s18States are the four record states of the DH-S-18 close. Each is the committed
// fixture (every in-scope variant passed) with its status line and, when not ready, the
// linux-e2:no-user-manager row of DH-E-EXEC-10 set to not run.
type s18State struct {
	name, status string
	ready        bool
}

var s18States = []s18State{
	{"open_not_ready", "open", false},
	{"open_ready", "open", true},
	{"closed_ready", "closed", true},
	{"closed_not_ready", "closed", false},
}

const s18NotRunRow = "| DH-E-EXEC-10 | linux-e2:no-user-manager | acceptance | required |"

// s18Tree is a root holding the committed acceptance index and variants.md and the
// fixture DH-S-18 record in the given state.
func s18Tree(t *testing.T, status string, ready bool) string {
	t.Helper()
	repo := filepath.Join("..", "..")
	root := closureTree(t, filepath.Join("testdata", "closure", "s18"))
	for _, rel := range []string{acceptance.IndexPath, variants.Path(closure.DefaultStory)} {
		b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
		if err != nil {
			t.Skipf("not run inside the repository: %v", err)
		}
		dst := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	recPath := filepath.Join(root, filepath.FromSlash(closure.RecordPath(closure.DefaultStory, "DH-S-18")))
	b, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	src = strings.Replace(src, "\nClosure status: open\n", "\nClosure status: "+status+"\n", 1)
	if !ready {
		lines := strings.Split(src, "\n")
		idx := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, s18NotRunRow) })
		if idx < 0 {
			t.Fatalf("fixture has no row %q", s18NotRunRow)
		}
		lines[idx] = s18NotRunRow + " E2 linux-e2-no-user-manager |  | not run (no-user-manager machine not provisioned) |  |"
		src = strings.Join(lines, "\n")
	}
	if err := os.WriteFile(recPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestStageClosureS18Golden is the DH-S-18 acceptance close (implementation-plan Stage
// 8.10, M2-DOD-4, M2-C-2): ready then close, without --built, over the four record
// states, against the committed index and variants.md. The committed record itself is
// judged by TestRecordLiveS18 once its runs are recorded.
func TestStageClosureS18Golden(t *testing.T) {
	var got strings.Builder
	for _, st := range s18States {
		for _, sub := range []string{"ready", "close"} {
			root := s18Tree(t, st.status, st.ready)
			code, stdout, stderr := runClosureGate(t, sub, "--root", root, "--stage", "DH-S-18")
			fmt.Fprintf(&got, "== %s: %s --stage DH-S-18 (exit %d)\n%s", st.name, sub, code, stdout)
			if stderr != "" {
				fmt.Fprintf(&got, "stderr: %s", stderr)
			}
		}
	}
	golden := filepath.Join("testdata", "closure", "expected_s18.golden")
	if *updateGolden {
		if os.Getenv("CI") != "" {
			t.Fatal("-update is refused under CI: regenerate the golden locally and review the diff")
		}
		if err := os.WriteFile(golden, []byte(got.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update locally to create it): %v", err)
	}
	if norm := strings.ReplaceAll(string(want), "\r\n", "\n"); norm != got.String() {
		t.Errorf("DH-S-18 closure output differs from %s\n--- got\n%s--- want\n%s", golden, got.String(), norm)
	}
}

// The DH-S-18 exit codes, asserted directly so a regenerated golden cannot flip a
// refusal into a pass; a not-ready state names the variant not run (M2-DOD-4).
func TestStageClosureS18ExitCodes(t *testing.T) {
	cases := []struct {
		state, sub string
		code       int
		marker     string
	}{
		{"open_not_ready", "ready", 1, "NOT READY"},
		{"open_ready", "ready", 0, ": ready;"},
		{"closed_ready", "ready", 0, ": ready;"},
		{"closed_not_ready", "ready", 1, "NOT READY"},
		{"open_not_ready", "close", 1, "REFUSED " + closure.NotYetClosed},
		{"open_ready", "close", 1, "REFUSED " + closure.NotYetClosed},
		{"closed_ready", "close", 0, ": closed;"},
		{"closed_not_ready", "close", 1, "REFUSED " + closure.ClosedButNotReady},
	}
	for _, c := range cases {
		t.Run(c.state+"/"+c.sub, func(t *testing.T) {
			i := slices.IndexFunc(s18States, func(s s18State) bool { return s.name == c.state })
			root := s18Tree(t, s18States[i].status, s18States[i].ready)
			code, stdout, stderr := runClosureGate(t, c.sub, "--root", root, "--stage", "DH-S-18")
			if code != c.code || !strings.Contains(stdout, c.marker) {
				t.Errorf("exit %d, want %d; stdout must contain %q\nstdout:\n%s\nstderr:\n%s", code, c.code, c.marker, stdout, stderr)
			}
			if !s18States[i].ready && !strings.Contains(stdout, "DH-E-EXEC-10 [linux-e2:no-user-manager]") {
				t.Errorf("the refusal must name the variant not run\n%s", stdout)
			}
		})
	}
}

// s18Variant maps a DH-S-18 row's variant to its variants.md label: the labels the
// record was created with (Stage 1.1) read as the record's Scenarios intro says, and
// the Linux (E2) aggregate rows map to none.
func s18Variant(scenario, label string) string {
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

// s18Uncovered returns each in-scope variant and scenario pair of the committed
// variants.md that has no row in rec (with passed, a passed row), and each scenario of
// the index's DH-S-18 entry the record has no row of.
func s18Uncovered(t *testing.T, repo string, rec *closure.Record, passed bool) []string {
	t.Helper()
	fleet, err := variants.Read(repo, closure.DefaultStory)
	if err != nil {
		t.Fatal(err)
	}
	x, err := acceptance.ReadIndex(repo)
	if err != nil {
		t.Fatal(err)
	}
	have, scenarios := map[[2]string]bool{}, map[string]bool{}
	for _, r := range rec.Scenarios.Rows {
		scenarios[r.Get("scenario")] = true
		if !passed || r.Get("result") == "passed" {
			have[[2]string{r.Get("scenario"), s18Variant(r.Get("scenario"), r.Get("variant"))}] = true
		}
	}
	var out []string
	for _, v := range fleet.Variants {
		if !v.InScope() {
			continue
		}
		label := strings.Trim(v.Label, "`")
		for _, s := range v.Scenarios {
			if !have[[2]string{s, label}] {
				out = append(out, s+" ["+label+"]")
			}
		}
	}
	ids, err := x.Required("DH-S-18")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if !scenarios[id] {
			out = append(out, id+" has no row")
		}
	}
	return out
}

// TestS18EveryVariantHasARow: the fixture the golden closes passes all 19 scenarios on
// every in-scope variant of the committed variants.md, and the committed record has a
// row for each of them (implementation-plan Stage 8.10 step 1; M2-DOD-4).
func TestS18EveryVariantHasARow(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := s18Tree(t, "open", true)
	fixture, err := closure.ReadRecord(root, closure.DefaultStory, "DH-S-18")
	if err != nil {
		t.Fatal(err)
	}
	x, err := acceptance.ReadIndex(repo)
	if err != nil {
		t.Fatal(err)
	}
	if ids, err := x.Required("DH-S-18"); err != nil || len(ids) != 19 {
		t.Fatalf("the index's DH-S-18 entry lists %d scenarios (%v), want 19", len(ids), err)
	}
	if missing := s18Uncovered(t, repo, fixture, true); len(missing) > 0 {
		t.Errorf("the fixture has no passed row of: %s", strings.Join(missing, "; "))
	}
	live, err := closure.ReadRecord(repo, closure.DefaultStory, "DH-S-18")
	if err != nil {
		t.Fatal(err)
	}
	if missing := s18Uncovered(t, repo, live, false); len(missing) > 0 {
		t.Errorf("the committed record has no row of: %s", strings.Join(missing, "; "))
	}
}

// TestRecordLiveS18 runs the two-step protocol on the committed DH-S-18 record: while
// it reads open, close is refused not-yet-closed; once it reads closed, ready and close
// both exit 0 (implementation-plan Stage 8.10).
func TestRecordLiveS18(t *testing.T) {
	root := filepath.Join("..", "..")
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(abs, filepath.FromSlash(closure.RecordPath(closure.DefaultStory, "DH-S-18")))); err != nil {
		t.Skipf("not run inside the repository: %v", err)
	}
	rec, err := closure.ReadRecord(abs, closure.DefaultStory, "DH-S-18")
	if err != nil {
		t.Fatal(err)
	}
	readyCode, readyOut, _ := runClosureGate(t, "ready", "--root", root, "--stage", "DH-S-18")
	closeCode, closeOut, _ := runClosureGate(t, "close", "--root", root, "--stage", "DH-S-18")
	switch rec.Status {
	case closure.StatusOpen:
		if closeCode != 1 || !strings.Contains(closeOut, "REFUSED "+closure.NotYetClosed) {
			t.Errorf("open record: close exit %d, want 1 refused %s\n%s", closeCode, closure.NotYetClosed, closeOut)
		}
	case closure.StatusClosed:
		if readyCode != 0 || closeCode != 0 {
			t.Errorf("closed record: ready exit %d, close exit %d; want both 0\n%s%s", readyCode, closeCode, readyOut, closeOut)
		}
	default:
		t.Errorf("status line reads %q; want open or closed", rec.StatusValue)
	}
}

// TestS17CopilotLegKept: the close of DH-S-17 reads a copilot-cli result for each of
// DH-E-LLM-1, DH-E-LLM-3 and DH-E-VIEW-1 (M2-DEC-4). A failed leg is kept and does not
// block (closed_ready records LLM-3's as failed); a missing or not-run leg does, even
// while the claude-code leg of the same scenario passed.
func TestS17CopilotLegKept(t *testing.T) {
	const tree = "closed_ready"
	recPath := filepath.FromSlash(closure.RecordPath(closure.DefaultStory, "DH-S-17"))
	src, err := os.ReadFile(filepath.Join("testdata", "closure", tree, recPath))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	for _, id := range []string{"DH-E-LLM-1", "DH-E-LLM-3", "DH-E-VIEW-1"} {
		prefix := "| " + id + " | copilot-cli | acceptance | recorded |"
		idx := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, prefix) })
		if idx < 0 {
			t.Fatalf("fixture %s has no copilot-cli row of %s", tree, id)
		}
		edits := map[string][]string{
			"missing": slices.Delete(slices.Clone(lines), idx, idx+1),
			"not run": slices.Replace(slices.Clone(lines), idx, idx+1, prefix+" E1 runner:s17-linux |  | not run (copilot not signed in) |  |"),
			"failed":  slices.Replace(slices.Clone(lines), idx, idx+1, prefix+" E1 runner:s17-linux | 0a1b2c3 | failed (route refused) | run-copilot |"),
		}
		for name, edited := range edits {
			t.Run(id+"/"+name, func(t *testing.T) {
				root := closureTree(t, filepath.Join("testdata", "closure", tree))
				if err := os.WriteFile(filepath.Join(root, recPath), []byte(strings.Join(edited, "\n")), 0o644); err != nil {
					t.Fatal(err)
				}
				code, stdout, _ := runClosureGate(t, "close", "--root", root, "--stage", "DH-S-17")
				if name == "failed" {
					if code != 0 {
						t.Errorf("a failed recorded leg is kept and does not block: exit %d\n%s", code, stdout)
					}
					return
				}
				if code != 1 || !strings.Contains(stdout, "REFUSED "+closure.ClosedButNotReady) || !strings.Contains(stdout, id+" [copilot-cli]") {
					t.Errorf("a %s copilot-cli leg of %s: exit %d, want 1 refused %s naming it\n%s", name, id, code, closure.ClosedButNotReady, stdout)
				}
			})
		}
	}
}

// TestRecordLiveS17 runs the two-step protocol on the committed DH-S-17 record: while
// it reads open, close is refused not-yet-closed; once it reads closed, ready and close
// both exit 0 (implementation-plan Stage 7.7).
func TestRecordLiveS17(t *testing.T) {
	root := filepath.Join("..", "..")
	recPath := filepath.Join(root, filepath.FromSlash(closure.RecordPath(closure.DefaultStory, "DH-S-17")))
	if _, err := os.Stat(recPath); err != nil {
		t.Skipf("not run inside the repository: %v", err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := closure.ReadRecord(abs, closure.DefaultStory, "DH-S-17")
	if err != nil {
		t.Fatal(err)
	}
	readyCode, readyOut, _ := runClosureGate(t, "ready", "--root", root, "--stage", "DH-S-17")
	closeCode, closeOut, _ := runClosureGate(t, "close", "--root", root, "--stage", "DH-S-17")
	switch rec.Status {
	case closure.StatusOpen:
		if closeCode != 1 || !strings.Contains(closeOut, "REFUSED "+closure.NotYetClosed) {
			t.Errorf("open record: close exit %d, want 1 refused %s\n%s", closeCode, closure.NotYetClosed, closeOut)
		}
	case closure.StatusClosed:
		if readyCode != 0 || closeCode != 0 {
			t.Errorf("closed record: ready exit %d, close exit %d; want both 0\n%s%s", readyCode, closeCode, readyOut, closeOut)
		}
	default:
		t.Errorf("status line reads %q; want open or closed", rec.StatusValue)
	}
}

func TestStageClosureUsage(t *testing.T) {
	root := closureTree(t, filepath.Join("testdata", "closure", "open_ready"))
	if code, _, _ := runClosureGate(t, "ready", "--root", root, "--stage", "S16"); code != 2 {
		t.Errorf("a bad --stage exits %d, want 2", code)
	}
	if code, stdout, _ := runClosureGate(t, "ready", "--root", root, "--stage", "DH-S-18"); code != 1 || !strings.Contains(stdout, "not found") && !strings.Contains(stdout, "no such file") && !strings.Contains(stdout, "cannot find") {
		t.Errorf("a missing record exits %d, want 1 naming it; stdout:\n%s", code, stdout)
	}
	if code, _, stderr := runClosureGate(t, "close", "--root", root, "--stage", "DH-S-16"); code != 2 || !strings.Contains(stderr, "--built") {
		t.Errorf("a build stage without --built exits %d, want 2 naming --built; stderr:\n%s", code, stderr)
	}
	if code, _, _ := runClosureGate(t, "ready", "--root", root, "--stage", "DH-S-17", "--built"); code != 2 {
		t.Errorf("an acceptance stage with --built exits %d, want 2", code)
	}
	if code, _, _ := runClosureGate(t, "records", "--root", root, "--story", "harness:NOPE"); code != 2 {
		t.Errorf("an unknown story exits %d, want 2", code)
	}
}

// TestRecordsCreated: the seven records and closure.md as Stage 1.1 created them
// hold their shape, and a record missing a sec. 7.3 field is refused.
func TestRecordsCreated(t *testing.T) {
	root := closureTree(t, filepath.Join("testdata", "records", "created"))
	if code, stdout, stderr := runClosureGate(t, "records", "--root", root); code != 0 {
		t.Fatalf("records on the created tree: exit %d\n%s%s", code, stdout, stderr)
	}
	path := filepath.Join(root, filepath.FromSlash(closure.RecordPath(closure.DefaultStory, "DH-S-13")))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(b), "### Cleanup evidence", "### Cleanup", 1)
	if broken == string(b) {
		t.Fatal("fixture DH-S-13.md has no Cleanup evidence heading")
	}
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := runClosureGate(t, "records", "--root", root)
	if code != 1 || !strings.Contains(stdout, "DH-S-13") || !strings.Contains(stdout, "Cleanup evidence") {
		t.Errorf("a record missing a field: exit %d, want 1 naming DH-S-13 and the field\n%s", code, stdout)
	}

	// An open record may leave cells empty, but deleting an owed row is refused.
	path16 := filepath.Join(root, filepath.FromSlash(closure.RecordPath(closure.DefaultStory, "DH-S-16")))
	b16, err := os.ReadFile(path16)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(strings.ReplaceAll(string(b16), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, "| Windows double-click |") {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(path16, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ = runClosureGate(t, "records", "--root", root)
	if code != 1 || !strings.Contains(stdout, `owner default "Windows double-click" has no row`) {
		t.Errorf("a deleted owner default: exit %d, want 1 naming it\n%s", code, stdout)
	}
}

// TestRecordsLive checks the repository's own M2 records and closure record.
func TestRecordsLive(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(closure.ClosureRecordPath(closure.DefaultStory)))); err != nil {
		t.Skipf("not run inside the repository: %v", err)
	}
	if code, stdout, stderr := runClosureGate(t, "records", "--root", root); code != 0 {
		t.Fatalf("records on the repository: exit %d\n%s%s", code, stdout, stderr)
	}
}

// TestAcceptanceIndexLiveM2Keys: the index names every stage M2 runs or authors for
// (implementation-plan Stage 1.1 step 3), and built-through has not gone backwards.
func TestAcceptanceIndexLiveM2Keys(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(acceptance.IndexPath))); err != nil {
		t.Skipf("not run inside the repository: %v", err)
	}
	x, err := acceptance.ReadIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"DH-S-16", "DH-S-17", "DH-S-18", "DH-S-22", "DH-S-27", "DH-S-39", "DH-S-48"} {
		if _, ok := x.Stage(s); !ok {
			t.Errorf("index has no %s key", s)
		}
	}
	n, ok := acceptance.StageNumber(x.BuiltThrough)
	if !ok || n < 8 {
		t.Errorf("built-through %q, want DH-S-8 or later", x.BuiltThrough)
	}
}
