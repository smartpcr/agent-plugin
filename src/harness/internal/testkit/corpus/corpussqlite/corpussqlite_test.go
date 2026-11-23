package corpussqlite

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/corpus"
)

var bg = context.Background()

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	st, err := sqlite.Open(bg, filepath.Join(t.TempDir(), "dsh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(bg); err != nil {
			t.Error(err)
		}
	})
	return st
}

// A seeded home lists in the order Home returns, the longest session first,
// and every session holds its whole log and the checkpoints of a closed
// session at its head: the core folds' and the four built-in views'.
func TestHomeSeedsLogsAndCheckpoints(t *testing.T) {
	st := openStore(t)
	var shapes []corpus.Shape
	for _, i := range []int{0, 2, 4, 6, 9} {
		sh, _ := corpus.Anchor(i)
		shapes = append(shapes, sh)
	}
	ids := Home(t, st, shapes)
	if len(ids) != len(shapes) || ids[0] != "corpus-0004-a09" {
		t.Fatalf("Home = %v", ids)
	}
	page, err := st.List(bg, store.Query{Order: store.OrderPrompt, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, s := range page.Summaries {
		listed = append(listed, s.Header.ID)
	}
	if !slices.Equal(listed, ids) {
		t.Fatalf("listed %v, Home returned %v", listed, ids)
	}
	want := []string{surface.CheckpointKey, views.EventIndexKey, views.ListSummaryKey, views.StatsKey, views.TurnIndexKey}
	for r, id := range []string{"corpus-0000-a00", "corpus-0004-a09"} {
		sum, err := st.Stat(bg, id)
		if err != nil || int(sum.NextSeq) < shapes[[]int{0, 4}[r]].Events {
			t.Fatalf("Stat(%s) = %+v, %v", id, sum, err)
		}
		rd, err := st.OpenRead(bg, id)
		if err != nil {
			t.Fatal(err)
		}
		cps, err := rd.Checkpoints(bg)
		_ = rd.Close(bg)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, cp := range cps {
			keys = append(keys, cp.Key)
			if cp.Seq != sum.NextSeq-1 {
				t.Fatalf("%s: checkpoint %s at seq %d, head %d", id, cp.Key, cp.Seq, sum.NextSeq)
			}
		}
		slices.Sort(keys)
		if !slices.Equal(keys, want) {
			t.Fatalf("%s: checkpoints %v, want %v", id, keys, want)
		}
	}
}

func TestSeedReportsItsFailures(t *testing.T) {
	st := openStore(t)
	sh, _ := corpus.Anchor(0)
	if n := Seed(t, st, store.Header{ID: "one"}, sh, Epoch); int(n) < sh.Events {
		t.Fatalf("Seed = %d records", n)
	}
	root := testkit.Tree(t)
	if _, err := seed(root, st, store.Header{ID: "one"}, sh, Epoch); err == nil {
		t.Fatal("seeding an existing id did not fail")
	}
	if _, err := seed(root, st, store.Header{ID: "two"}, corpus.Shape{}, Epoch); err == nil {
		t.Fatal("seeding a shape of no turns did not fail")
	}
	recs, err := corpus.Log(sh, Epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := coreCheckpoint(bg, st, "missing", recs); err == nil {
		t.Fatal("a core checkpoint of a missing session did not fail")
	}
	if err := viewRows(bg, root, st, "missing"); err == nil {
		t.Fatal("the view rows of a missing session did not fail")
	}
	// Without the core checkpoint the open replays the log: refused, since a
	// seeded session must open as the product reopens a closed one.
	if err := st.Seed(bg, store.Header{ID: "bare"}, recs); err != nil {
		t.Fatal(err)
	}
	if err := viewRows(bg, root, st, "bare"); err == nil {
		t.Fatal("a session without its core checkpoint opened as seeded")
	}
}
