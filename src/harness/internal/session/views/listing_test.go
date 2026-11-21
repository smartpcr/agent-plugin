package views_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/query"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/workspace"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

// renderGroups is a listing as stable text: no temp paths, no wall times.
func renderGroups(groups []query.Group) string {
	var sb strings.Builder
	for _, g := range groups {
		if g.Ungrouped {
			sb.WriteString("(ungrouped)\n")
		} else {
			fmt.Fprintf(&sb, "project %s (position %d)\n", g.Workspace.Title, g.Workspace.Position)
		}
		for _, e := range g.Sessions {
			pin := " "
			if e.Flags.Pinned {
				pin = "*"
			}
			fmt.Fprintf(&sb, "  %s %-6s prompts=%d turns=%d events=%d last=%d\n", pin, e.ID, e.Summary.Prompts, e.Summary.Turns, e.Events, e.Summary.LastPromptAt)
			fmt.Fprintf(&sb, "      first prompt at seq %d\n", e.Summary.FirstPrompt)
			fmt.Fprintf(&sb, "      preview: %q\n", e.Summary.Preview)
		}
	}
	return sb.String()
}

// Scenario: Listing groups by project (TC-08). Given three sessions in two
// projects, when sessions are listed by project, then the result equals the
// committed golden listing: projects in registry order, pinned first, then the
// most recent prompt; a hidden session is not listed.
func TestListingGroupsByProject(t *testing.T) {
	home := t.TempDir()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	st := must(sqlite.Open(bg, filepath.Join(home, "dsh.db")))
	var now atomic.Int64
	now.Store(1_700_000_000_000)
	reg := views.New()
	must(views.RegisterBuiltins(reg))
	svc := session.NewService(root, st, session.Config{Folder: reg, Clock: func() int64 { return now.Add(1000) }})
	defer func() {
		root.Dispose()
		_ = st.Close(bg)
	}()
	ws := must(workspace.Open(bg, st))
	dirs := map[string]workspace.Workspace{}
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(home, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		dirs[name] = must(ws.Add(bg, dir))
	}
	// id, project, turns
	for _, s := range []struct {
		id, project string
		turns       []int
	}{{"s-a1", "alpha", []int{1, 2}}, {"s-b1", "beta", []int{3}}, {"s-a2", "alpha", []int{4}}, {"s-a3", "alpha", []int{5}}} {
		h := must(svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: s.id, Cwd: dirs[s.project].Path}}))
		for _, n := range s.turns {
			if _, err := h.AppendGroup(bg, turnDrafts(n)); err != nil {
				t.Fatal(err)
			}
		}
		if err := h.Close(bg); err != nil {
			t.Fatal(err)
		}
		if err := ws.Attach(bg, dirs[s.project].ID, session.ID(s.id)); err != nil {
			t.Fatal(err)
		}
	}
	// s-a1 is pinned (so it leads alpha though s-a2 prompted later); s-a3 is
	// archived, so it is not listed.
	if err := ws.Pin(bg, "s-a1", true); err != nil {
		t.Fatal(err)
	}
	if err := ws.SetArchive(bg, "s-a3", workspace.ArchiveArchived); err != nil {
		t.Fatal(err)
	}

	q := query.New(st, query.WithWorkspaces(ws))
	listing := must(q.ListByProject(bg, query.ListOptions{}))
	if listing.Truncated || listing.Examined != 4 {
		t.Fatalf("listing examined %d, truncated %v", listing.Examined, listing.Truncated)
	}
	golden.Assert(t, "list_by_project", renderGroups(listing.Groups))

	all := must(q.As(query.Operator()).ListByProject(bg, query.ListOptions{}))
	if n := len(all.Groups[0].Sessions); n != 3 {
		t.Fatalf("with hidden, alpha lists %d sessions, want 3", n)
	}
}
