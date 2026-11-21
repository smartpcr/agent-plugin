package views_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

// badEnc stops encoding once it reaches 3: with an error, or a panic.
type badEnc struct {
	N     int
	Panic bool
}

func (b badEnc) MarshalJSON() ([]byte, error) {
	if b.N >= 3 {
		if b.Panic {
			panic("MarshalJSON blew up")
		}
		return nil, errors.New("MarshalJSON refused")
	}
	return json.Marshal(b.N)
}

func encFold(key string, panics bool) views.Definition[badEnc] {
	return views.Definition[badEnc]{Key: key, Version: 1, Stored: true,
		Init:  func(session.Header) badEnc { return badEnc{Panic: panics} },
		Apply: func(b badEnc, _ event.Event) (badEnc, bool) { b.N++; return b, true }}
}

// badDec is an int whose stored rows never decode.
type badDec int

func (*badDec) UnmarshalJSON([]byte) error { panic("UnmarshalJSON blew up") }

func decFold(calls *atomic.Int64) views.Definition[badDec] {
	return views.Definition[badDec]{Key: "test/bad-dec", Version: 1, Stored: true,
		Init: func(session.Header) badDec { return 0 },
		Apply: func(n badDec, _ event.Event) (badDec, bool) {
			if calls != nil {
				calls.Add(1)
			}
			return n + 1, true
		}}
}

// A stored fold whose state stops encoding (an error or a panic) is failed on
// its own; a healthy sibling keeps folding and its row keeps advancing.
func TestEncodeFailureFailsOnlyThatFold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.db")
	reg := views.New()
	ok := must(views.Register(reg, stored(1, nil)))
	errF := must(views.Register(reg, encFold("test/enc-error", false)))
	panF := must(views.Register(reg, encFold("test/enc-panic", true)))
	st, svc, stop := boot(t, path, reg)
	defer stop()
	h := must(svc.Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	defer h.Close(bg)
	for range 5 {
		must(h.Append(bg, ctxDraft("v")))
	}
	snap := must(reg.Read(bg, h))
	if got := must(ok.Get(snap)); got != 5 {
		t.Fatalf("healthy sibling = %d, want 5", got)
	}
	for _, g := range []views.Handle[badEnc]{errF, panF} {
		if _, err := g.Get(snap); !errors.Is(err, views.ErrFailed) {
			t.Errorf("%s = %v, want view/failed", g.Key(), err)
		}
	}
	if row := checkpointsOf(t, st, h.ID())[1]; row.Seq != 4 || row.State != "5" {
		t.Fatalf("healthy row = %+v, want seq 4 state 5", row)
	}
}

// A stored row that panics decoding neither aborts the open nor an unfollowed
// read: that fold is rebuilt from the log, and a healthy sibling is restored
// from its own row without refolding.
func TestDecodeFailureRebuildsOnlyThatFold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.db")
	reg1 := views.New()
	must(views.Register(reg1, stored(1, nil)))
	must(views.Register(reg1, decFold(nil)))
	_, svc, stop := boot(t, path, reg1)
	h := must(svc.Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	for range 4 {
		must(h.Append(bg, ctxDraft("v")))
	}
	id := h.ID()
	must(0, h.Close(bg))
	stop()

	var okCalls, badCalls atomic.Int64
	reg2 := views.New()
	ok := must(views.Register(reg2, stored(1, &okCalls)))
	bad := must(views.Register(reg2, decFold(&badCalls)))
	_, svc, stop = boot(t, path, reg2)
	defer stop()
	h2, _, err := svc.Open(bg, nil, id, session.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	snap := must(reg2.Read(bg, h2))
	if got := must(ok.Get(snap)); got != 4 || okCalls.Load() != 0 {
		t.Fatalf("healthy = %d after %d refolds, want 4 after 0", got, okCalls.Load())
	}
	if got := must(bad.Get(snap)); got != 4 || badCalls.Load() != 4 {
		t.Fatalf("undecodable fold = %d after %d refolds, want 4 after 4", got, badCalls.Load())
	}
	must(0, h2.Close(bg))

	reg3 := views.New()
	bad3 := must(views.Register(reg3, decFold(nil)))
	r, release, err := svc.OpenRead(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got := must(bad3.Get(must(reg3.Read(bg, r)))); got != 4 {
		t.Fatalf("unfollowed read of the undecodable fold = %d, want 4", got)
	}
}
