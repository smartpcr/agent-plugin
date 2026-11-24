package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
)

// Scenario "Entry subcommand over immutable fixtures" (implementation-plan Stage 1.2):
// m1_open exits 1 with the committed golden as its output, m1_closed exits 0.
func TestEntryGolden(t *testing.T) {
	root := closureTree(t, filepath.Join("testdata", "entry", "m1_open"))
	code, stdout, stderr := runClosureGate(t, "entry", "--story", "harness:DSH-M2", "--root", root)
	if code != 1 || stderr != "" {
		t.Errorf("m1_open: exit %d, want 1; stderr:\n%s", code, stderr)
	}
	golden := filepath.Join("testdata", "entry", "m1_open.golden")
	if *updateGolden {
		if os.Getenv("CI") != "" {
			t.Fatal("-update is refused under CI: regenerate the golden locally and review the diff")
		}
		if err := os.WriteFile(golden, []byte(stdout), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update locally to create it): %v", err)
	}
	if norm := strings.ReplaceAll(string(want), "\r\n", "\n"); norm != stdout {
		t.Errorf("entry output differs from %s\n--- got\n%s--- want\n%s", golden, stdout, norm)
	}
	for _, s := range closure.Entries["harness:DSH-M2"].PriorStages {
		if !strings.Contains(stdout, "  - "+s+": ") {
			t.Errorf("m1_open: output does not name %s", s)
		}
	}
	if !strings.Contains(stdout, "reads DH-S-8; want DH-S-11") {
		t.Errorf("m1_open: output does not name built-through DH-S-8")
	}

	root = closureTree(t, filepath.Join("testdata", "entry", "m1_closed"))
	code, stdout, stderr = runClosureGate(t, "entry", "--story", "harness:DSH-M2", "--root", root)
	if code != 0 || !strings.Contains(stdout, "harness:DSH-M1 is closed") {
		t.Errorf("m1_closed: exit %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}

func TestEntryUsage(t *testing.T) {
	root := closureTree(t, filepath.Join("testdata", "entry", "m1_closed"))
	if code, _, stderr := runClosureGate(t, "entry", "--root", root, "--story", "harness:DSH-M1"); code != 2 || !strings.Contains(stderr, "no entry condition") {
		t.Errorf("a story without an entry condition exits %d, want 2; stderr:\n%s", code, stderr)
	}
	if code, _, _ := runClosureGate(t, "entry", "--root", root, "extra"); code != 2 {
		t.Errorf("an extra argument exits %d, want 2", code)
	}
	if code, _, stderr := runClosureGate(t, "entry", "--root", t.TempDir()); code != 2 || !strings.Contains(stderr, "acceptance-index.yaml") {
		t.Errorf("a root without an index exits %d, want 2; stderr:\n%s", code, stderr)
	}
}

// Scenario "Live entry result agrees with live records" (implementation-plan Stage
// 1.2): whatever the state of the committed M1 records and index, entry passes if and
// only if all eleven read closed, the closure record exists and built-through is at
// least DH-S-11, and otherwise names exactly the unmet conditions. The oracle reads
// the files independently of the closure package.
func TestEntryLiveAgreesWithRecords(t *testing.T) {
	repo := filepath.Join("..", "..")
	e := closure.Entries["harness:DSH-M2"]
	code, stdout, stderr := runClosureGate(t, "entry", "--root", repo)
	if code == 2 {
		t.Fatalf("entry on the repository exits 2: %s", stderr)
	}
	met := true
	for _, s := range e.PriorStages {
		closed := liveStatusClosed(t, filepath.Join(repo, filepath.FromSlash(closure.RecordPath(e.Prior, s))))
		named := strings.Contains(stdout, "  - "+s+": ")
		if closed == named {
			t.Errorf("%s: closed=%v but the output names it=%v", s, closed, named)
		}
		met = met && closed
	}
	_, err := os.Stat(filepath.Join(repo, filepath.FromSlash(closure.ClosureRecordPath(e.Prior))))
	exists := err == nil
	if missing := strings.Contains(stdout, "no "+e.Prior+" closure record"); missing == exists {
		t.Errorf("closure record exists=%v but the output names it missing=%v", exists, missing)
	}
	contentProblems := strings.Contains(stdout, "  - "+closure.ClosureRecordPath(e.Prior)+": ")
	met = met && exists && !contentProblems
	bt := liveBuiltThrough(t, filepath.Join(repo, "test", "harness", "acceptance-index.yaml"))
	reached := bt >= 11
	if named := strings.Contains(stdout, "  - built-through in "); named == reached {
		t.Errorf("built-through DH-S-%d reached=%v but the output names it=%v", bt, reached, named)
	}
	met = met && reached
	if (code == 0) != met {
		t.Errorf("entry exits %d but the live conditions are met=%v\nstdout:\n%s", code, met, stdout)
	}
	t.Logf("live entry (exit %d):\n%s", code, stdout)
}

// liveStatusClosed reads the first line of path that starts "Closure status:".
func liveStatusClosed(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	inFence := false
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \r")
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if v, ok := strings.CutPrefix(line, "Closure status:"); ok && !inFence {
			v = strings.TrimSpace(v)
			return v == "closed" || strings.HasPrefix(v, "closed at ")
		}
	}
	return false
}

func liveBuiltThrough(t *testing.T, path string) int {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(src), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "built-through: DH-S-"); ok {
			n, err := strconv.Atoi(strings.Fields(v)[0])
			if err != nil {
				t.Fatalf("built-through %q: %v", line, err)
			}
			return n
		}
	}
	t.Fatal("no built-through line")
	return 0
}
