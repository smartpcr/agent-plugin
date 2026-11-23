package views_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

const periodicKey = "test/periodic"

// periodic counts events, its row written by the Periodic cadence.
func periodic(calls *atomic.Int64) views.Definition[int] {
	return views.Definition[int]{Key: periodicKey, Version: 1, Stored: true, Cadence: views.Periodic,
		Init: func(session.Header) int { return 0 },
		Apply: func(n int, _ event.Event) (int, bool) {
			if calls != nil {
				calls.Add(1)
			}
			return n + 1, true
		}}
}

// countedSession is a read session whose log reads are counted; its rows are
// listed as the wrapped session's.
type countedSession struct {
	session.ReadSession
	reads *atomic.Int64
}

func (c countedSession) Read(ctx context.Context, from session.Offset, limit int, opts ...session.ReadOption) ([]event.Event, error) {
	c.reads.Add(1)
	return c.ReadSession.Read(ctx, from, limit, opts...)
}

func (c countedSession) Checkpoints(ctx context.Context) ([]store.Checkpoint, error) {
	//lint:ignore typeassert every session the tests wrap is a live or read-only open, both Checkpointed
	return c.ReadSession.(session.Checkpointed).Checkpoints(ctx)
}

// bootAt is one more process over path: its own store instance and service.
func bootAt(t *testing.T, path string, reg *views.Registry) (*sqlite.Store, session.Service) {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	st := must(sqlite.Open(bg, path))
	t.Cleanup(func() {
		root.Dispose()
		_ = st.Close(bg)
	})
	return st, session.NewService(root, st, session.Config{Folder: reg})
}

func appendN(t *testing.T, h session.Session, n int) {
	t.Helper()
	for done := 0; done < n; {
		k := min(100, n-done)
		ds := make([]event.Draft, k)
		for i := range ds {
			ds[i] = ctxDraft("v")
		}
		if _, err := h.AppendGroup(bg, ds); err != nil {
			t.Fatal(err)
		}
		done += k
	}
}

// Scenario: Exact refuses a long catch-up (M1-G-STORE-11). Given a session
// 2,500 events past its newest row, read by a process that is not the holder
// (the holder keeps its lease), When it is read at Exact, Then view/behind
// carries both positions and nothing was read from the log or folded; and a
// Stored read returns at once: the row's value, behind, with the row's seq.
//
//dsh:test DH-T-VIEW-7
func TestExactRefusesALongCatchUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.db")
	// The holder writes its Periodic row with the first batch only.
	holderReg := views.New(views.WithCheckpointEveryEvents(1_000_000))
	must(views.Register(holderReg, periodic(nil)))
	_, holderSvc := bootAt(t, path, holderReg)
	h := must(holderSvc.Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(bg) })
	appendN(t, h, 1)
	appendN(t, h, 2_500)
	if _, err := h.Flush(bg); err != nil {
		t.Fatal(err)
	}

	var calls, reads atomic.Int64
	reg := views.New()
	fold := must(views.Register(reg, periodic(&calls)))
	_, svc := bootAt(t, path, reg)
	rs, release, err := svc.OpenRead(bg, h.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	r := countedSession{rs, &reads}

	_, err = reg.Snapshot(bg, r, nil, views.Exact)
	var ve *views.Error
	if !errors.Is(err, views.ErrBehind) || !errors.As(err, &ve) || ve.AsOfSeq != 1 || ve.HeadSeq != 2_501 {
		t.Fatalf("Exact read = %v (%+v), want view/behind at 1 of 2501", err, ve)
	}
	if reads.Load() != 0 || calls.Load() != 0 {
		t.Fatalf("the refused Exact read read the log %d times and folded %d events", reads.Load(), calls.Load())
	}

	snap := must(reg.Snapshot(bg, r, []string{periodicKey}, views.Stored))
	v, ok := snap.Value(periodicKey)
	if got := must(fold.Get(snap)); !ok || got != 1 || v.State != views.Behind || v.AsOfSeq != 1 || snap.HeadSeq != 2_501 || snap.AsOf != 1 {
		t.Fatalf("Stored read = %d %+v (head %d, as of %d), want 1, behind at 1 of 2501", got, v, snap.HeadSeq, snap.AsOf)
	}
	if reads.Load() != 0 || calls.Load() != 0 {
		t.Fatalf("the Stored read read the log %d times and folded %d events", reads.Load(), calls.Load())
	}

	// With a bound above the gap, Exact catches up from the row: 2,500 events.
	wide := views.New(views.WithMaxCatchUpEvents(3_000))
	wfold := must(views.Register(wide, periodic(&calls)))
	snap = must(wide.Snapshot(bg, r, nil, views.Exact))
	if got := must(wfold.Get(snap)); got != 2_501 || calls.Load() != 2_500 || snap.AsOf != 2_501 {
		t.Fatalf("a wide Exact read = %d after %d folds at %d, want 2501 after 2500", got, calls.Load(), snap.AsOf)
	}
	if v, _ := snap.Value(periodicKey); v.State != views.Current || v.AsOfSeq != 2_501 {
		t.Fatalf("a wide Exact value = %+v", v)
	}
}

// Stored never folds: a key with no row, a row of another version, a row that
// does not decode and a fold that is not stored are pending (Get is
// view/pending, never a zero value); a row at the head is current; an empty log
// is the initial state, current. A followed session is read from memory at
// either freshness, and a panicking fold fails its own key only.
//
//dsh:test DH-T-VIEW-7
func TestStoredReportsPendingBehindAndCurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.db")
	writer := views.New()
	must(views.Register(writer, stored(1, nil)))
	must(views.Register(writer, decFold(nil)))
	_, wsvc := bootAt(t, path, writer)
	h := must(wsvc.Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	empty := must(wsvc.Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	appendN(t, h, 5)
	must(0, h.Close(bg))
	must(0, empty.Close(bg))

	var calls, reads atomic.Int64
	reg := views.New()
	cur := must(views.Register(reg, stored(1, &calls)))
	other := must(views.Register(reg, views.Definition[int]{Key: "test/other", Version: 1, Stored: true,
		Init: func(session.Header) int { return 7 }, Apply: func(n int, _ event.Event) (int, bool) { calls.Add(1); return n, false }}))
	bad := must(views.Register(reg, decFold(&calls)))
	mem := must(views.Register(reg, count()))
	_, svc := bootAt(t, path, reg)
	rs, release, err := svc.OpenRead(bg, h.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	snap := must(reg.Snapshot(bg, countedSession{rs, &reads}, nil, views.Stored))
	if v, _ := snap.Value(storedKey); must(cur.Get(snap)) != 5 || v != (views.Value{State: views.Current, AsOfSeq: 5}) {
		t.Fatalf("a row at the head = %d %+v", must(cur.Get(snap)), v)
	}
	for _, k := range []string{"test/other", bad.Key(), mem.Key()} {
		if v, ok := snap.Value(k); !ok || v.State != views.Pending || v.AsOfSeq != 0 {
			t.Fatalf("%s = %+v (%v), want pending", k, v, ok)
		}
	}
	if _, err := other.Get(snap); !errors.Is(err, views.ErrPending) {
		t.Fatalf("Get of a pending key = %v, want view/pending", err)
	}
	if reads.Load() != 0 || calls.Load() != 0 || snap.HeadSeq != 5 || snap.AsOf != 5 {
		t.Fatalf("Stored read %d times, folded %d, head %d as of %d", reads.Load(), calls.Load(), snap.HeadSeq, snap.AsOf)
	}

	es, release2, err := svc.OpenRead(bg, empty.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer release2()
	esnap := must(reg.Snapshot(bg, es, []string{"test/other"}, views.Stored))
	if v, _ := esnap.Value("test/other"); must(other.Get(esnap)) != 7 || v.State != views.Current || esnap.HeadSeq != 0 {
		t.Fatalf("an empty log = %d %+v", must(other.Get(esnap)), v)
	}

	// A followed session is read from memory at either freshness; a panicking
	// fold is failed on its own.
	live := views.New()
	lc := must(views.Register(live, count()))
	boom := must(views.Register(live, views.Definition[int]{Key: "test/boom", Version: 1,
		Init:  func(session.Header) int { return 0 },
		Apply: func(int, event.Event) (int, bool) { panic("bad fold") }}))
	e := newEnv(t, live)
	lh := e.create(t)
	appendN(t, lh, 3)
	for _, f := range []views.Freshness{views.Stored, views.Exact} {
		s := must(live.Snapshot(bg, lh, nil, f))
		if v, _ := s.Value(lc.Key()); must(lc.Get(s)) != 3 || v != (views.Value{State: views.Current, AsOfSeq: 3}) {
			t.Fatalf("freshness %d: count = %d %+v", f, must(lc.Get(s)), v)
		}
		if v, _ := s.Value(boom.Key()); v.State != views.Failed {
			t.Fatalf("freshness %d: the panicking fold = %+v", f, v)
		}
		if _, err := boom.Get(s); !errors.Is(err, views.ErrFailed) {
			t.Fatalf("freshness %d: Get of the failed fold = %v", f, err)
		}
	}
	if _, err := live.Snapshot(bg, lh, nil, views.Freshness(9)); !errors.Is(err, views.ErrInvalid) {
		t.Fatalf("an unknown freshness = %v", err)
	}
	if _, err := live.Snapshot(bg, lh, []string{"test/none"}, views.Stored); !errors.Is(err, views.ErrUnknownKey) {
		t.Fatalf("an unknown key = %v", err)
	}
}

// StoredState reads one definition's row without a session: an empty log is
// current; no row, a row at or past the head or one that does not decode is
// pending; a row below the head is behind with its seq.
//
//dsh:test DH-T-VIEW-7
func TestStoredStateOfARow(t *testing.T) {
	d := stored(1, nil)
	row := func(seq event.Seq, state string) []store.Checkpoint {
		return []store.Checkpoint{{Key: storedKey, StateVersion: 1, Seq: seq, State: state}, {Key: storedKey, StateVersion: 2, Seq: seq + 5, State: "99"}}
	}
	for _, c := range []struct {
		name  string
		cps   []store.Checkpoint
		head  event.Seq
		state int
		want  views.Value
	}{
		{"empty log", nil, 0, 0, views.Value{State: views.Current}},
		{"no row", nil, 4, 0, views.Value{State: views.Pending}},
		{"current", row(3, "4"), 4, 4, views.Value{State: views.Current, AsOfSeq: 4}},
		{"behind", row(1, "2"), 4, 2, views.Value{State: views.Behind, AsOfSeq: 2}},
		{"past the head", row(4, "5"), 4, 0, views.Value{State: views.Pending}},
		{"undecodable", row(3, "{"), 4, 0, views.Value{State: views.Pending}},
	} {
		if s, v := views.StoredState(d, session.Header{}, c.cps, c.head); s != c.state || v != c.want {
			t.Errorf("%s: %d %+v, want %d %+v", c.name, s, v, c.state, c.want)
		}
	}
	if _, v := views.StoredState(count(), session.Header{}, row(3, "4"), 4); v.State != views.Pending {
		t.Errorf("a fold that is not stored = %+v", v)
	}
	var badRow []store.Checkpoint
	for _, c := range row(3, "4") {
		c.Key = "test/bad-dec"
		badRow = append(badRow, c)
	}
	if _, v := views.StoredState(decFold(nil), session.Header{}, badRow, 4); v.State != views.Pending {
		t.Errorf("a row whose decoder panics = %+v", v)
	}
	for v, want := range map[views.ValueState]string{views.Current: "current", views.Behind: "behind", views.Pending: "pending",
		views.Failed: "failed", views.ValueState(9): "ValueState(9)"} {
		if v.String() != want {
			t.Errorf("%d.String() = %q, want %q", v, v.String(), want)
		}
	}
}
