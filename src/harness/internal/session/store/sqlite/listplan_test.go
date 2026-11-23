package sqlite

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// plan is SQLite's query plan of query with args, one detail per line.
func plan(t *testing.T, s *Store, query string, args ...any) string {
	t.Helper()
	rows, err := s.rdb.QueryContext(obg, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var (
			id, parent, unused int
			detail             string
		)
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

// The rows a list page reads do not grow with the store (DH-T-VIEW-15,
// "rows read per page do not grow with the corpus"): each statement of a page
// is a range search of its order's index, never a scan of the sessions table
// or of the index from its top. The prompt order's stable statement seeks to
// the cut on the first page and to the cursor on a later one, its touched
// statement to the sessions prompted after the cut; the recent order seeks to
// its cursor. The counts that cross the store's interface are compared at 100
// and 10,000 sessions by session/query's TestListRowsPerPageDoNotGrow.
//
//dsh:test DH-T-VIEW-15
func TestListPagesSearchTheirIndex(t *testing.T) {
	s := openBoundStore(t)
	// Some sessions, and no ANALYZE statistics: the planner as the product
	// runs it.
	base := time.Now().Add(-time.Hour)
	for i := range 200 {
		h := store.Header{ID: fmt.Sprintf("s%03d", i), CreatedAt: base.Add(time.Duration(i) * time.Millisecond)}
		if err := s.Seed(obg, h, nil); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().UnixNano()
	pcut := store.PromptCut(at)
	where := []string{"state = 'active'", "created_at <= ?"}
	for _, after := range []bool{false, true} {
		stable, touched := promptQueries(where, after)
		args := []any{at, pcut}
		if after {
			args = append(args, at-1, at-1, "s100")
		} else {
			args = append(args, at)
		}
		args = append(args, 21)
		for name, c := range map[string]struct {
			sql  string
			args []any
		}{"stable": {stable, args}, "touched": {touched, []any{at, pcut, pcut}}} {
			p := plan(t, s, c.sql, c.args...)
			if !strings.Contains(p, "SEARCH sessions USING INDEX sessions_prompt") {
				t.Fatalf("prompt order, %s (cursor %v): the plan is not a search of sessions_prompt:\n%s", name, after, p)
			}
		}
	}
	p := plan(t, s, recentQuery([]string{"state = 'active'"}, true), at, at, at, at, "s100", 21)
	if !strings.Contains(p, "SEARCH sessions USING INDEX sessions_recent") {
		t.Fatalf("recent order with a cursor: the plan is not a search of sessions_recent:\n%s", p)
	}
}
