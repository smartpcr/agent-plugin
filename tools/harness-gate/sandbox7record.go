package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A DH-E-SANDBOX-7 acceptance record (implementation-plan Stage 7.6: "the job
// records the branch commit, run id and the SHA-256 of each text file") is the
// run a passed DH-S-17 row's run=<id> names. The s17 job writes it with
// "harness-gate sandbox7-record" after the escalation runs, on the clean
// checkout of the commit it tested; it runs no gate, so a first or re-proved text
// is recorded before the policy-text gate ever admits it:
//
//	harness-gate acceptance record
//	scenario: DH-E-SANDBOX-7
//	commit: <git rev-parse HEAD, 40 hex digits>
//	tree: clean (git status --porcelain --untracked-files=all is empty)
//	ci run: <the CI run id, or "none">
//	recorded: <UTC>
//	text: <path> sha256=<hex> escalated=<n>/18
//	...
//	result: passed | failed
//	sha256: <hex>
//
// The final sha256 is the SHA-256 of every byte before that line, as in an
// evidence record. The file is runs/<id>.txt, id <lane>-<sha7>-<UTC
// yyyymmddThhmmZ> (local-run-record-format).
const (
	sandbox7RecordHeader = "harness-gate acceptance record"
	evidenceRecordHeader = "harness-gate evidence record"
)

// CleanTreeField is the tree field of every record taken on a clean tree, as
// both producers write it and the policy-text gate requires it.
const CleanTreeField = "clean (git status --porcelain --untracked-files=all is empty)"

var sandbox7TextLine = regexp.MustCompile(`^(\S+) sha256=([0-9a-f]{64}) escalated=(\d+)/(\d+)$`)

// sandbox7Text is one text line of an acceptance record.
type sandbox7Text struct {
	Path      string
	SHA256    string
	Escalated int
	Of        int
}

// sandbox7Run is a parsed acceptance record.
type sandbox7Run struct {
	Commit string
	Result string
	Texts  []sandbox7Text
}

// formatSandbox7Record is the bytes of an acceptance record, final sha256 line
// included.
func formatSandbox7Record(commit, ciRun string, at time.Time, texts []sandbox7Text) string {
	var b strings.Builder
	line := func(key, value string) { fmt.Fprintf(&b, "%s: %s\n", key, value) }
	b.WriteString(sandbox7RecordHeader + "\n")
	line("scenario", PolicyTextScenario)
	line("commit", commit)
	line("tree", CleanTreeField)
	if ciRun == "" {
		ciRun = "none"
	}
	line("ci run", ciRun)
	line("recorded", at.UTC().Format(time.RFC3339))
	result := "passed"
	for _, t := range texts {
		line("text", fmt.Sprintf("%s sha256=%s escalated=%d/%d", t.Path, t.SHA256, t.Escalated, t.Of))
		if t.Of != PolicyTextEscalations || t.Escalated < PolicyTextMinEscalated || t.Escalated > t.Of {
			result = "failed"
		}
	}
	line("result", result)
	sum := sha256.Sum256([]byte(b.String()))
	return b.String() + "sha256: " + hex.EncodeToString(sum[:]) + "\n"
}

// policyTextFiles is every file under PolicyTextDir of root with its SHA-256,
// sorted by path; none when the directory is absent.
func policyTextFiles(root string) ([]PolicyTextFile, error) {
	dir := filepath.Join(root, filepath.FromSlash(PolicyTextDir))
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var files []PolicyTextFile
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		files = append(files, PolicyTextFile{Path: filepath.ToSlash(rel), SHA256: hex.EncodeToString(sum[:])})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", PolicyTextDir, err)
	}
	slices.SortFunc(files, func(a, b PolicyTextFile) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

// WriteSandbox7Record records a DH-E-SANDBOX-7 run on the clean checkout at
// root: the commit, the SHA-256 of every text file and its escalation count
// (escalated, keyed by platform, one for each text and no other). It writes
// runs/<lane>-<sha7>-<now>.txt under root and returns the run id and whether
// every text met the bar. A dirty tree, a tree with no text, a missing or extra
// count and an existing record are errors; nothing is written then.
func WriteSandbox7Record(ctx context.Context, root, lane, ciRun string, escalated map[string]int, now time.Time) (id string, passed bool, err error) {
	if !regexp.MustCompile(`^[a-z][a-z0-9]*$`).MatchString(lane) {
		return "", false, fmt.Errorf("lane %q is not lower-case letters and digits", lane)
	}
	commit, err := CleanCommit(ctx, root)
	if err != nil {
		return "", false, err
	}
	if !policyFullCommit.MatchString(commit) {
		return "", false, fmt.Errorf("HEAD %q is not a full sha1 commit", commit)
	}
	files, err := policyTextFiles(root)
	if err != nil {
		return "", false, err
	}
	if len(files) == 0 {
		return "", false, fmt.Errorf("no sandbox policy text under %s/ to record", PolicyTextDir)
	}
	var texts []sandbox7Text
	seen := map[string]bool{}
	for _, f := range files {
		platform, ok := policyTextPlatform(f.Path)
		if !ok {
			return "", false, fmt.Errorf("%s is not a platform text (<platform>.md for one of %s)", f.Path, strings.Join(policyTextPlatforms, ", "))
		}
		n, ok := escalated[platform]
		if !ok {
			return "", false, fmt.Errorf("no escalation count for %s (--escalated %s=<n>)", f.Path, platform)
		}
		if n < 0 || n > PolicyTextEscalations {
			return "", false, fmt.Errorf("escalation count %s=%d is not 0 to %d", platform, n, PolicyTextEscalations)
		}
		seen[platform] = true
		texts = append(texts, sandbox7Text{Path: f.Path, SHA256: f.SHA256, Escalated: n, Of: PolicyTextEscalations})
	}
	for platform := range escalated {
		if !seen[platform] {
			return "", false, fmt.Errorf("an escalation count for %s, which has no text under %s/", platform, PolicyTextDir)
		}
	}
	id = fmt.Sprintf("%s-%s-%s", lane, commit[:7], now.UTC().Format("20060102T1504Z"))
	dir := filepath.Join(root, filepath.FromSlash(PolicyRunsDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, err
	}
	body := formatSandbox7Record(commit, ciRun, now, texts)
	f, err := os.OpenFile(filepath.Join(dir, id+".txt"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", false, fmt.Errorf("run record %s/%s.txt: %w", PolicyRunsDir, id, err)
	}
	_, werr := io.WriteString(f, body)
	if err := errors.Join(werr, f.Close()); err != nil {
		return "", false, err
	}
	passed = true
	for _, t := range texts {
		passed = passed && t.Escalated >= PolicyTextMinEscalated
	}
	return id, passed, nil
}

// readSandbox7Run reads and checks the acceptance record of run id under root:
// it must exist, be intact, be a DH-E-SANDBOX-7 acceptance record taken on a
// clean tree with a full commit that starts with the id's sha7.
func readSandbox7Run(root, id string) (sandbox7Run, error) {
	var run sandbox7Run
	m := policyRunRecordID.FindStringSubmatch(id)
	if m == nil {
		return run, fmt.Errorf("%q is not a run-record id (<lane>-<sha7>-<yyyymmddThhmmZ>)", id)
	}
	rel := PolicyRunsDir + "/" + id + ".txt"
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return run, fmt.Errorf("no run record %s", rel)
	}
	text := strings.TrimSuffix(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	i := strings.LastIndex(text, "\nsha256: ")
	if i < 0 {
		return run, fmt.Errorf("run record %s is not intact: it has no final sha256 line", rel)
	}
	body, last := text[:i+1], text[i+len("\nsha256: "):]
	if sum := sha256.Sum256([]byte(body)); last != hex.EncodeToString(sum[:]) {
		return run, fmt.Errorf("run record %s is not intact: its final sha256 line is not the SHA-256 of the bytes before it", rel)
	}
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if lines[0] == evidenceRecordHeader {
		return run, fmt.Errorf("%s is a quality-gate evidence record (harness-gate --evidence), not a %s acceptance record (harness-gate sandbox7-record)", rel, PolicyTextScenario)
	}
	if lines[0] != sandbox7RecordHeader {
		return run, fmt.Errorf("%s is not a %s acceptance record", rel, PolicyTextScenario)
	}
	fields := map[string]string{}
	for _, line := range lines[1:] {
		k, v, ok := strings.Cut(line, ": ")
		if !ok {
			return run, fmt.Errorf("run record %s: line %q is not <key>: <value>", rel, line)
		}
		if k == "text" {
			t := sandbox7TextLine.FindStringSubmatch(v)
			if t == nil {
				return run, fmt.Errorf("run record %s: text line %q is not <path> sha256=<hex> escalated=<n>/<of>", rel, v)
			}
			n, _ := strconv.Atoi(t[3])
			of, _ := strconv.Atoi(t[4])
			run.Texts = append(run.Texts, sandbox7Text{Path: t[1], SHA256: t[2], Escalated: n, Of: of})
			continue
		}
		if _, dup := fields[k]; dup {
			return run, fmt.Errorf("run record %s has two %q lines", rel, k)
		}
		fields[k] = v
	}
	run.Commit, run.Result = fields["commit"], fields["result"]
	switch {
	case fields["scenario"] != PolicyTextScenario:
		return run, fmt.Errorf("run record %s is of scenario %q, not %s", rel, fields["scenario"], PolicyTextScenario)
	case !policyFullCommit.MatchString(run.Commit):
		return run, fmt.Errorf("run record %s names no full commit", rel)
	case fields["tree"] != CleanTreeField:
		return run, fmt.Errorf("run record %s was not taken on a clean tree (tree: %q)", rel, fields["tree"])
	case !strings.HasPrefix(run.Commit, m[1]):
		return run, fmt.Errorf("the run id's commit %s is not the record's commit %s", m[1], run.Commit)
	}
	return run, nil
}

// runSandbox7Record is "harness-gate sandbox7-record [--root dir] --escalated
// linux=<n>[,darwin=<n>,...] [--lane s17] [--ci-run id]": the s17 job's record of
// a DH-E-SANDBOX-7 run on the checkout at root. Exit 0 when every text met the
// bar, 1 when the run is recorded failed, 2 when nothing is recorded.
func runSandbox7Record(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var lane, ciRun, counts *string
	root, ok := subcommandFlags("sandbox7-record", args, stderr, func(f *flag.FlagSet) {
		counts = f.String("escalated", "", "escalations of the 18 per platform text, e.g. linux=12,darwin=11,windows=13")
		lane = f.String("lane", "s17", "the lane of the run id <lane>-<sha7>-<UTC>")
		ciRun = f.String("ci-run", "", "the CI run id of the job, recorded as is")
	})
	if !ok {
		return 2
	}
	escalated := map[string]int{}
	for _, kv := range strings.Split(*counts, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		n, err := strconv.Atoi(v)
		if !ok || err != nil || k == "" {
			fmt.Fprintf(stderr, "harness-gate sandbox7-record: --escalated %q: want <platform>=<n>[,...]\n", *counts)
			return 2
		}
		if _, dup := escalated[k]; dup {
			fmt.Fprintf(stderr, "harness-gate sandbox7-record: --escalated names %s twice\n", k)
			return 2
		}
		escalated[k] = n
	}
	id, passed, err := WriteSandbox7Record(ctx, root, *lane, *ciRun, escalated, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate sandbox7-record: %v\n", err)
		return 2
	}
	result := "failed"
	if passed {
		result = "passed"
	}
	fmt.Fprintf(stdout, "sandbox7-record: %s %s: %s/%s.txt (name it in the DH-S-17 row as run=%s)\n", PolicyTextScenario, result, PolicyRunsDir, id, id)
	if !passed {
		return 1
	}
	return 0
}
