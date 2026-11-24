package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
)

// The M2-C-6 pre-merge rule (implementation-plan Stage 2.4, README.md section
// "Sandbox policy text (M2-C-6)"): no shipped sandbox policy text merges without a
// passed DH-E-SANDBOX-7 run on its exact bytes.
const (
	// PolicyTextDir is the product directory of the per-platform policy texts,
	// relative to the repository root.
	PolicyTextDir = "src/harness/internal/world/sandbox/policy/text"
	// PolicyTextStage is the stage whose record holds the DH-E-SANDBOX-7 rows.
	PolicyTextStage = "DH-S-17"
	// PolicyTextScenario is the scenario that proves a text.
	PolicyTextScenario = "DH-E-SANDBOX-7"
	// PolicyTextRule names the rule in every refusal.
	PolicyTextRule = "M2-C-6"
	// PolicyTextMinEscalated and PolicyTextEscalations are the pass bar: at least
	// 10 of the 18 escalation prompts escalated under that text.
	PolicyTextMinEscalated = 10
	PolicyTextEscalations  = 18
)

// policyTextPlatforms are the platforms a text file may be for: <platform>.md.
var policyTextPlatforms = []string{"linux", "darwin", "windows"}

var (
	policyRunID     = regexp.MustCompile(`(?:^|[;\s])run=([^;\s]+)`)
	policyTextProof = regexp.MustCompile(`(?:^|[;\s])(linux|darwin|windows)\.md sha256=([0-9a-f]{64}) escalated=(\d+)/(\d+)`)
	policyCommit    = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

// PolicyTextFile is one file under PolicyTextDir and the verdict on it.
type PolicyTextFile struct {
	Path   string // slash-separated, relative to the root
	SHA256 string
	// Proof is the passing row, "" when refused.
	Proof string
	// Refusal says why the file may not merge, "" when it may.
	Refusal string
}

// PolicyTextReport is the result of CheckPolicyText.
type PolicyTextReport struct {
	Files []PolicyTextFile
	// Containment is how the run's commit was checked to contain the file:
	// "git", or why it cannot be, in which case every text is refused.
	Containment string
}

// Refused is the files that may not merge.
func (r PolicyTextReport) Refused() []PolicyTextFile {
	var out []PolicyTextFile
	for _, f := range r.Files {
		if f.Refusal != "" {
			out = append(out, f)
		}
	}
	return out
}

// sandbox7Proof is one platform entry of a passed DH-E-SANDBOX-7 row.
type sandbox7Proof struct {
	line      int
	commit    string
	run       string
	sha       string
	escalated int
	of        int
}

// CheckPolicyText applies M2-C-6 to the tree at root. A tree with no file under
// PolicyTextDir passes. Every file there must be <platform>.md and must have a
// DH-E-SANDBOX-7 row of the DH-S-17 record with Result passed, a commit, a run id
// and, in Artifacts, "<platform>.md sha256=<hex> escalated=<n>/18" with n at least
// 10 and the hex equal to the file's SHA-256. The run id must name an intact run
// record runs/<id>.txt (runRecordCommit) whose commit is the row's, and that
// commit must contain the file with those bytes, checked through git; root must
// be a git checkout's top level, or every text is refused (fail closed). An error
// is a tree the check cannot read.
func CheckPolicyText(ctx context.Context, root string) (PolicyTextReport, error) {
	var rep PolicyTextReport
	dir := filepath.Join(root, filepath.FromSlash(PolicyTextDir))
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return rep, nil
	}
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
		rep.Files = append(rep.Files, PolicyTextFile{Path: filepath.ToSlash(rel), SHA256: hex.EncodeToString(sum[:])})
		return nil
	})
	if err != nil {
		return rep, fmt.Errorf("policy-text: read %s: %w", PolicyTextDir, err)
	}
	if len(rep.Files) == 0 {
		return rep, nil
	}
	slices.SortFunc(rep.Files, func(a, b PolicyTextFile) int { return strings.Compare(a.Path, b.Path) })

	proofs, recErr := readSandbox7Proofs(root)
	verify, containment := commitVerifier(ctx, root)
	rep.Containment = containment
	for i := range rep.Files {
		f := &rep.Files[i]
		platform, ok := policyTextPlatform(f.Path)
		switch {
		case !ok:
			f.Refusal = fmt.Sprintf("not a platform text: a file under %s/ is <platform>.md for one of %s", PolicyTextDir, strings.Join(policyTextPlatforms, ", "))
		case recErr != nil:
			f.Refusal = recErr.Error()
		default:
			f.Proof, f.Refusal = judgePolicyText(root, *f, platform, proofs[platform], verify)
		}
	}
	return rep, nil
}

// policyTextPlatform is the platform of a file directly under PolicyTextDir.
func policyTextPlatform(rel string) (string, bool) {
	if path.Dir(rel) != PolicyTextDir {
		return "", false
	}
	name, ok := strings.CutSuffix(path.Base(rel), ".md")
	return name, ok && slices.Contains(policyTextPlatforms, name)
}

// readSandbox7Proofs reads the platform entries of the passed DH-E-SANDBOX-7 rows
// of the DH-S-17 record, keyed by platform.
func readSandbox7Proofs(root string) (map[string][]sandbox7Proof, error) {
	rec, err := closure.ReadRecord(root, closure.DefaultStory, PolicyTextStage)
	if err != nil {
		return nil, err
	}
	out := map[string][]sandbox7Proof{}
	found := false
	for _, row := range rec.Scenarios.Rows {
		if row.Get("scenario") != PolicyTextScenario {
			continue
		}
		found = true
		if row.Get("result") != "passed" {
			continue
		}
		art := row.Get("artifacts")
		run := ""
		if m := policyRunID.FindStringSubmatch(art); m != nil {
			run = m[1]
		}
		for _, m := range policyTextProof.FindAllStringSubmatch(art, -1) {
			n, _ := strconv.Atoi(m[3])
			of, _ := strconv.Atoi(m[4])
			out[m[1]] = append(out[m[1]], sandbox7Proof{line: row.Line, commit: row.Get("commit"), run: run, sha: m[2], escalated: n, of: of})
		}
	}
	if !found {
		return nil, fmt.Errorf("%s has no %s row", rec.Path, PolicyTextScenario)
	}
	return out, nil
}

// judgePolicyText returns the proof of f, or the most specific reason none of
// the platform's proofs holds.
func judgePolicyText(root string, f PolicyTextFile, platform string, proofs []sandbox7Proof, verify func(commit, rel, sha string) error) (string, string) {
	record := closure.RecordPath(closure.DefaultStory, PolicyTextStage)
	if len(proofs) == 0 {
		return "", fmt.Sprintf("%s has no passed %s row naming %s.md with its sha256 and escalation count", record, PolicyTextScenario, platform)
	}
	var reasons []string
	for _, p := range proofs {
		at := fmt.Sprintf("%s:%d", record, p.line)
		switch {
		case p.sha != f.SHA256:
			reasons = append(reasons, fmt.Sprintf("%s records sha256 %s, the file is %s (edited after the run)", at, p.sha, f.SHA256))
		case p.of != PolicyTextEscalations || p.escalated < PolicyTextMinEscalated || p.escalated > p.of:
			reasons = append(reasons, fmt.Sprintf("%s records %d/%d escalations, the bar is at least %d of %d", at, p.escalated, p.of, PolicyTextMinEscalated, PolicyTextEscalations))
		case p.run == "":
			reasons = append(reasons, fmt.Sprintf("%s names no run (run=<id> in Artifacts)", at))
		case !policyCommit.MatchString(p.commit):
			reasons = append(reasons, fmt.Sprintf("%s names no commit (7 to 40 lower-case hex digits)", at))
		default:
			commit, err := runRecordCommit(root, p.run, p.commit)
			if err == nil {
				err = verify(commit, f.Path, f.SHA256)
			}
			if err != nil {
				reasons = append(reasons, fmt.Sprintf("%s: run %s: %v", at, p.run, err))
				continue
			}
			return fmt.Sprintf("%s run %s at %s (%s/%s.txt), %d/%d escalated (%s)", PolicyTextScenario, p.run, commit, PolicyRunsDir, p.run, p.escalated, p.of, at), ""
		}
	}
	return "", strings.Join(reasons, "; ")
}

// PolicyRunsDir holds the local run records (docs/stories/harness-DSH-M1/evidence/
// README.md, local-run-record-format): runs/<run id>.txt, written by
// "harness-gate --evidence", is the run a row's run=<id> names.
var PolicyRunsDir = closure.EvidenceDir(closure.DefaultStory) + "/runs"

// policyRunRecordID is a run-record id, <lane>-<sha7>-<UTC yyyymmddThhmmZ>.
var (
	policyRunRecordID = regexp.MustCompile(`^[a-z][a-z0-9]*-([0-9a-f]{7})-\d{8}T\d{4}Z$`)
	policyFullCommit  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// runRecordCommit resolves run id to the full commit its run record names: the
// record runs/<id>.txt must exist under root, be intact (its final sha256 line is
// the SHA-256 of every byte before it), record a clean tree and exit 0, and its
// commit must start with the id's sha7 and with the row's commit.
func runRecordCommit(root, id, rowCommit string) (string, error) {
	m := policyRunRecordID.FindStringSubmatch(id)
	if m == nil {
		return "", fmt.Errorf("%q is not a run-record id (<lane>-<sha7>-<yyyymmddThhmmZ>)", id)
	}
	rel := PolicyRunsDir + "/" + id + ".txt"
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("no run record %s", rel)
	}
	text := strings.TrimSuffix(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	i := strings.LastIndex(text, "\nsha256: ")
	if i < 0 {
		return "", fmt.Errorf("run record %s is not intact: it has no final sha256 line", rel)
	}
	body, last := text[:i+1], text[i+len("\nsha256: "):]
	if sum := sha256.Sum256([]byte(body)); last != hex.EncodeToString(sum[:]) {
		return "", fmt.Errorf("run record %s is not intact: its final sha256 line is not the SHA-256 of the bytes before it", rel)
	}
	fields := map[string]string{}
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	for _, line := range lines {
		if k, v, ok := strings.Cut(line, ": "); ok {
			if _, seen := fields[k]; !seen {
				fields[k] = v
			}
		}
	}
	commit := fields["commit"]
	switch {
	case lines[0] != "harness-gate evidence record":
		return "", fmt.Errorf("%s is not a harness-gate evidence record", rel)
	case !policyFullCommit.MatchString(commit):
		return "", fmt.Errorf("run record %s names no full commit", rel)
	case fields["tree"] != "clean":
		return "", fmt.Errorf("run record %s was not taken on a clean tree", rel)
	case lines[len(lines)-1] != "exit: 0":
		return "", fmt.Errorf("run record %s did not exit 0", rel)
	case !strings.HasPrefix(commit, m[1]):
		return "", fmt.Errorf("the run id's commit %s is not the record's commit %s", m[1], commit)
	case !strings.HasPrefix(commit, rowCommit):
		return "", fmt.Errorf("the row's commit %s is not the run's commit %s (%s)", rowCommit, commit, rel)
	}
	return commit, nil
}

// commitVerifier checks, through git, that a commit contains a file with the
// given SHA-256. It needs root to be a git checkout's top level holding the
// commit; when it is not, every check fails: a text whose run cannot be
// verified does not merge (M2-C-6 fails closed).
func commitVerifier(ctx context.Context, root string) (func(commit, rel, sha string) error, string) {
	top, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--show-toplevel").Output()
	if err != nil || !sameDir(strings.TrimSpace(string(top)), root) {
		const why = "unavailable: the root is not a git checkout's top level, so no run's commit can be checked"
		return func(string, string, string) error { return errors.New("commit containment " + why) }, why
	}
	return func(commit, rel, sha string) error {
		b, err := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "blob", commit+":"+rel).Output()
		if err != nil {
			return fmt.Errorf("commit %s does not contain %s (or is not in this clone; fetch it)", commit, rel)
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != sha {
			return fmt.Errorf("commit %s holds %s with sha256 %s, not the file's", commit, rel, got)
		}
		return nil
	}, "git"
}

func sameDir(a, b string) bool {
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

// policyTextGate is the policy-text gate of Gates: CheckPolicyText on the root.
func (c *Config) policyTextGate(ctx context.Context, w io.Writer) error {
	refusal, err := reportPolicyText(ctx, c.Root, w)
	if err != nil {
		return err
	}
	return refusal
}

// reportPolicyText prints the verdict on every text file and returns the
// refusal, nil when the tree may merge; err is a tree it cannot read.
func reportPolicyText(ctx context.Context, root string, w io.Writer) (refusal, err error) {
	rep, err := CheckPolicyText(ctx, root)
	if err != nil {
		return nil, err
	}
	if len(rep.Files) == 0 {
		fmt.Fprintf(w, "policy-text: ok: no sandbox policy text under %s/ (%s)\n", PolicyTextDir, PolicyTextRule)
		return nil, nil
	}
	for _, f := range rep.Files {
		if f.Refusal != "" {
			fmt.Fprintf(w, "policy-text: REFUSED %s: %s (sha256 %s): %s\n", PolicyTextRule, f.Path, f.SHA256, f.Refusal)
		} else {
			fmt.Fprintf(w, "policy-text: ok: %s (sha256 %s): %s\n", f.Path, f.SHA256, f.Proof)
		}
	}
	fmt.Fprintf(w, "policy-text: commit containment: %s\n", rep.Containment)
	if refused := rep.Refused(); len(refused) > 0 {
		return fmt.Errorf("%s: %d of %d sandbox policy text files have no passed %s run on their exact bytes; a text merges only with one (tools/harness-gate/README.md, section Sandbox policy text)",
			PolicyTextRule, len(refused), len(rep.Files), PolicyTextScenario), nil
	}
	return nil, nil
}

// runPolicyText is "harness-gate policy-text [--root dir]": the policy-text gate
// alone. Exit 0 when the tree may merge, 1 when a text is refused, 2 on a usage
// error or a tree it cannot read.
func runPolicyText(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root, ok := subcommandFlags("policy-text", args, stderr, nil)
	if !ok {
		return 2
	}
	refusal, err := reportPolicyText(ctx, root, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate policy-text: %v\n", err)
		return 2
	}
	if refusal != nil {
		fmt.Fprintf(stdout, "policy-text: FAILED: %v\n", refusal)
		return 1
	}
	return 0
}
