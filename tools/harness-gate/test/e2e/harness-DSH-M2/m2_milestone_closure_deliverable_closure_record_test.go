//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
)

// dcrIndex lists DH-S-16's one indexed scenario, so the in-memory DH-S-16 record
// passes ready --built and close on a single passed row.
const dcrIndex = `built-through: DH-S-8
stages:
  DH-S-16:
    - id: DH-E-STORE-1
      authored: DH-S-7
      credential: none
`

var (
	dcrStageRef = regexp.MustCompile(`\bDH-S-[1-9][0-9]*\b`)
	dcrHeldRef  = regexp.MustCompile(`\bH-[1-9][0-9]*\b`)
)

// dcrStageRecord is a DH-S-16 record reading status, with its steps, manual checks and
// owner defaults complete and the given scenario rows.
func dcrStageRecord(status string, rows ...string) string {
	return "# DH-S-16 evidence\n\nClosure status: " + status + `

## Steps

| Step | Deliverable | Commit | Notes |
| --- | --- | --- | --- |
| 16.1 | DH-D-BUNDLE-3 | 0123abc | |

## Scenarios

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |
` + strings.Join(rows, "\n") + `

## Manual checks register

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |
| 16.3 | one headless prompt | answered | tester | 0123abc | ` + "`manual`" + ` |

## Owner defaults (M2-C-4)

| Default | Raised in | Confirmed | By | Commit |
| --- | --- | --- | --- | --- |
| ` + "`dsh headless`" + ` progress | U-K2-4 | confirmed as is | owner | 0123abc |
`
}

func dcrRow(id, result string) string {
	return "| " + id + " | Linux (WSL) | guarded | required | wsl lane | 0123abc | " + result + " | run.txt |"
}

// dcrRepoRoot is the repository root, two levels above the harness-gate module, which
// is three levels above this package.
func dcrRepoRoot() (string, error) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".."))
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(root, "tools", "harness-gate", "go.mod")); err != nil {
		return "", fmt.Errorf("%s is not the repository root: %w", root, err)
	}
	return root, nil
}

type dcrState struct {
	story    closure.Story
	index    *acceptance.Index
	table    *closure.Record
	records  map[string]*closure.Record
	problems []string

	deliverable, scenario string

	root    string
	closure *closure.Record
	rows    []closure.Row
}

func (s *dcrState) parseTable(rows string) (*closure.Record, error) {
	src := "# closure\n\n## Deliverables\n\n" +
		"| Deliverable | Priority | Built in | State | Finishing stage or held-open item | Proving scenarios |\n" +
		"| --- | --- | --- | --- | --- | --- |\n" + rows + "\n\n## Held-open items\n\n" +
		"| Item | Deliverable | What waits | State | Closes at |\n| --- | --- | --- | --- | --- |\n"
	return closure.ParseDocument("closure.md", strings.NewReader(src), closure.DeliverablesHeading, closure.HeldOpenHeading)
}

func (s *dcrState) inMemoryTableMarkingDone(id, scenario string) error {
	// The story's real shape (BuiltIn, Finishing), without the rows its committed stage
	// records owe, so the in-memory record is judged only on what this scenario varies.
	s.story = closure.Stories[closure.DefaultStory]
	s.story.Obligations = nil
	if got := s.story.BuiltIn[id]; got != "DH-S-16" {
		return fmt.Errorf("%s is built in %q by the story, want DH-S-16", id, got)
	}
	x, err := acceptance.ParseIndex(strings.NewReader(dcrIndex))
	if err != nil {
		return fmt.Errorf("ParseIndex: %w", err)
	}
	s.index = x
	s.deliverable, s.scenario = id, scenario
	t, err := s.parseTable(fmt.Sprintf("| %s | P0 | DH-S-16 | done | DH-S-16 (local tests) | %s, DH-E-STORE-1 |", id, scenario))
	if err != nil {
		return err
	}
	if d := t.Extra[closure.DeliverablesHeading]; d == nil || len(d.Rows) != 1 || d.Rows[0].Get("state") != "done" {
		return errors.New("in-memory closure table does not hold one done row")
	}
	s.table = t
	return nil
}

func (s *dcrState) stageRecordWithoutRow(scenario string) error {
	rec, err := closure.ParseRecord("DH-S-16.md", strings.NewReader(dcrStageRecord("closed", dcrRow("DH-E-STORE-1", "passed"))))
	if err != nil {
		return err
	}
	for _, r := range rec.Scenarios.Rows {
		if r.Get("scenario") == scenario {
			return fmt.Errorf("stage record has a row for %s", scenario)
		}
	}
	// The record itself must be built and closed, so the only gap is the unrun scenario.
	if b := s.story.Ready(s.index, "DH-S-16", rec, true); len(b) != 0 {
		return fmt.Errorf("fixture DH-S-16 record is not built: %q", b)
	}
	if refusal, b := s.story.Close(s.index, "DH-S-16", rec, true); refusal != "" {
		return fmt.Errorf("fixture DH-S-16 record does not close (%s): %q", refusal, b)
	}
	s.records = map[string]*closure.Record{"DH-S-16": rec}
	return nil
}

func (s *dcrState) predicateRuns() error {
	s.problems = closure.ProofProblems(s.story, s.index, s.table, s.records)
	return nil
}

func (s *dcrState) refusesNaming(id, scenario string) error {
	if len(s.problems) != 1 {
		return fmt.Errorf("predicate gave %d problems, want exactly 1: %q", len(s.problems), s.problems)
	}
	if !strings.Contains(s.problems[0], id) || !strings.Contains(s.problems[0], scenario) {
		return fmt.Errorf("refusal %q does not name %s and %s", s.problems[0], id, scenario)
	}
	return nil
}

func (s *dcrState) refusalSaysUnrun() error {
	p := s.problems[0]
	for _, want := range []string{"reads done", "proving scenario " + s.scenario, "unrun", "M2-C-3"} {
		if !strings.Contains(p, want) {
			return fmt.Errorf("refusal %q lacks %q", p, want)
		}
	}
	return nil
}

func (s *dcrState) acceptsOncePassed(scenario string) error {
	rec, err := closure.ParseRecord("DH-S-16.md", strings.NewReader(dcrStageRecord("closed", dcrRow("DH-E-STORE-1", "passed"), dcrRow(scenario, "passed"))))
	if err != nil {
		return err
	}
	if p := closure.ProofProblems(s.story, s.index, s.table, map[string]*closure.Record{"DH-S-16": rec}); len(p) != 0 {
		return fmt.Errorf("with %s passed the predicate still refuses: %q", scenario, p)
	}
	return nil
}

func (s *dcrState) committedClosureRecord(rel string) error {
	root, err := dcrRepoRoot()
	if err != nil {
		return err
	}
	if want := closure.ClosureRecordPath(closure.DefaultStory); rel != want {
		return fmt.Errorf("closure record path %q, the gate reads %q", rel, want)
	}
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	defer f.Close()
	rec, err := closure.ParseDocument(rel, f, closure.DeliverablesHeading, closure.HeldOpenHeading)
	if err != nil {
		return err
	}
	s.root, s.closure = root, rec
	s.story = closure.Stories[closure.DefaultStory]
	return nil
}

func (s *dcrState) countsRows() error {
	d := s.closure.Extra[closure.DeliverablesHeading]
	if d == nil || !d.Present {
		return fmt.Errorf("%s has no %s table", s.closure.Path, closure.DeliverablesHeading)
	}
	s.rows = d.Rows
	return nil
}

func (s *dcrState) deliverableCounts(total, p0, p1 int) error {
	seen := map[string]bool{}
	var n0, n1 int
	for _, r := range s.rows {
		id := r.Get("deliverable")
		if seen[id] {
			return fmt.Errorf("%s is listed twice", id)
		}
		seen[id] = true
		want, ok := s.story.Deliverables[id]
		if !ok {
			return fmt.Errorf("%s is not a deliverable of %s", id, s.story.ID)
		}
		if got := r.Get("priority"); got != want {
			return fmt.Errorf("%s: priority %q, tech-spec gives %s", id, got, want)
		}
		switch r.Get("priority") {
		case "P0":
			n0++
		case "P1":
			n1++
		}
	}
	if len(seen) != total || n0 != p0 || n1 != p1 {
		return fmt.Errorf("%d deliverables (%d P0, %d P1), want %d (%d P0, %d P1)", len(seen), n0, n1, total, p0, p1)
	}
	if len(s.story.Deliverables) != total || s.story.P0 != p0 || s.story.P1 != p1 {
		return fmt.Errorf("gate story shape has %d (%d P0, %d P1), want %d (%d P0, %d P1)", len(s.story.Deliverables), s.story.P0, s.story.P1, total, p0, p1)
	}
	return nil
}

func (s *dcrState) atMostDone(limit int, state string) error {
	if len(s.story.DoneCandidates) != limit {
		return fmt.Errorf("story has %d done-candidates, want %d", len(s.story.DoneCandidates), limit)
	}
	n := 0
	for _, r := range s.rows {
		if r.Get("state") != state {
			continue
		}
		n++
		if !slices.Contains(s.story.DoneCandidates, r.Get("deliverable")) {
			return fmt.Errorf("%s reads %s but is not a done-candidate", r.Get("deliverable"), state)
		}
	}
	if n > limit {
		return fmt.Errorf("%d rows read %s, at most %d may", n, state, limit)
	}
	return nil
}

func (s *dcrState) builtRowsNameStageOrH(state string) error {
	n := 0
	for _, r := range s.rows {
		if r.Get("state") != state {
			continue
		}
		n++
		id := r.Get("deliverable")
		if b := r.Get("built in"); !dcrStageRef.MatchString(b) || b != s.story.BuiltIn[id] {
			return fmt.Errorf("%s: Built in %q, tech-spec builds it in %s", id, b, s.story.BuiltIn[id])
		}
		fin := r.Get("finishing stage or held-open item")
		if !dcrStageRef.MatchString(fin) && !dcrHeldRef.MatchString(fin) {
			return fmt.Errorf("%s reads %s but %q names no stage or H-item", id, state, fin)
		}
	}
	if n == 0 {
		return fmt.Errorf("no row reads %s: nothing to check", state)
	}
	return nil
}

func (s *dcrState) noProblemsAgainstCommitted() error {
	if p := closure.ClosureProblems(s.story, s.closure); len(p) != 0 {
		return fmt.Errorf("closure shape problems:\n%s", strings.Join(p, "\n"))
	}
	x, err := acceptance.ReadIndex(s.root)
	if err != nil {
		return fmt.Errorf("ReadIndex: %w", err)
	}
	if p := closure.CheckRecords(s.root, s.story, x); len(p) != 0 {
		return fmt.Errorf("records check (with the closure predicate) refuses the committed records:\n%s", strings.Join(p, "\n"))
	}
	return nil
}

func InitializeScenario_m2_milestone_closure_deliverable_closure_record(ctx *godog.ScenarioContext) {
	s := &dcrState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = dcrState{}
		return c, nil
	})
	ctx.Step(`^an in-memory closure table marking "([^"]+)" done with proving scenario "([^"]+)"$`, s.inMemoryTableMarkingDone)
	ctx.Step(`^a closed, built DH-S-16 stage record in which "([^"]+)" has no row$`, s.stageRecordWithoutRow)
	ctx.Step(`^the closure predicate runs$`, s.predicateRuns)
	ctx.Step(`^it refuses naming "([^"]+)" and "([^"]+)"$`, s.refusesNaming)
	ctx.Step(`^the refusal says the proving scenario is unrun under M2-C-3$`, s.refusalSaysUnrun)
	ctx.Step(`^once "([^"]+)" has a passed row with its evidence the predicate accepts the row$`, s.acceptsOncePassed)
	ctx.Step(`^the committed closure record "([^"]+)"$`, s.committedClosureRecord)
	ctx.Step(`^the closure check counts rows$`, s.countsRows)
	ctx.Step(`^there are (\d+) deliverables, (\d+) P0 and (\d+) P1$`, s.deliverableCounts)
	ctx.Step(`^at most (\d+) rows read "([^"]+)", each a done-candidate of the story$`, s.atMostDone)
	ctx.Step(`^every "([^"]+)" row names a stage or H-item$`, s.builtRowsNameStageOrH)
	ctx.Step(`^the closure shape check and the closure predicate find no problem against the committed stage records$`, s.noProblemsAgainstCommitted)
}

func TestE2E_m2_milestone_closure_deliverable_closure_record(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_m2_milestone_closure_deliverable_closure_record,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"m2_milestone_closure_deliverable_closure_record.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("deliverable closure record scenarios failed")
	}
}
