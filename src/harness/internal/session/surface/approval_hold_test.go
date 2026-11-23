package surface_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// aprRecord stands in for human/approval's audit payloads, which this package
// cannot import: the brackets read only id, call and outcome.
type aprRecord struct {
	ID      string `json:"id"`
	Call    string `json:"call,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

// The approval types, declared on the default registry of this test binary as
// human/approval declares them in the program.
var (
	aprAsked   = event.MustDefine(event.Default(), event.Spec[aprRecord]{Name: surface.ApprovalAskedType, Version: 1})
	aprDecided = event.MustDefine(event.Default(), event.Spec[aprRecord]{Name: surface.ApprovalDecidedType, Version: 1})
)

// A recorded call whose approval was asked and not given never ran its body, so
// the resume closer says approval/unavailable ("did not run"), not
// tools/outcome-unknown: c1's approval is pending, c3's was refused (its result
// never came), and c2's was allowed, so c2 may have run and stays
// outcome-unknown. A record for another id, no call, a call that is not open or
// not started changes nothing. The hold survives a checkpoint (version 2); a
// version-1 checkpoint is refused, so the log is folded from the start.
//
//dsh:test DH-T-HUMAN-4
func TestApprovalHoldsACallOutOfOutcomeUnknown(t *testing.T) {
	l := newLog(t)
	l.add(turnStart(1))
	l.add(stepStart(1, 1))
	l.add(assistant(t, "m-a", call("c1", "bash"), call("c2", "grep"), call("c3", "edit"), call("c4", "read")))
	l.add(toolCall("c1", "bash"))
	l.add(aprAsked.Draft(aprRecord{ID: "apr-1", Call: "c1"}))
	l.add(aprDecided.Draft(aprRecord{ID: "apr-other", Call: "c1", Outcome: "allowed-once"}))
	l.add(toolCall("c2", "grep"))
	l.add(aprAsked.Draft(aprRecord{ID: "apr-2", Call: "c2"}))
	l.add(aprDecided.Draft(aprRecord{ID: "apr-2", Call: "c2", Outcome: "allowed-once"}))
	l.add(toolCall("c3", "edit"))
	l.add(aprAsked.Draft(aprRecord{ID: "apr-3", Call: "c3"}))
	l.add(aprDecided.Draft(aprRecord{ID: "apr-3", Call: "c3", Outcome: "rejected"}))
	// Records that name nothing the brackets hold.
	l.add(aprAsked.Draft(aprRecord{ID: "apr-4", Call: "c4"})) // c4 is not started
	l.add(aprAsked.Draft(aprRecord{ID: "apr-5", Call: "c9"}))
	l.add(aprAsked.Draft(aprRecord{ID: "apr-6"}))

	want := map[string]string{"c1": "apr-1", "c2": "", "c3": "apr-3", "c4": ""}
	check := func(b surface.Brackets) {
		t.Helper()
		for _, c := range b.Calls {
			if c.Held != want[c.ID] {
				t.Errorf("call %s held by %q, want %q", c.ID, c.Held, want[c.ID])
			}
		}
	}
	check(l.st.Open())

	ds, err := surface.Closers(l.st.Open(), surface.CauseInterrupted, l.st.Len())
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]string{"c1": surface.CodeApprovalUnavailable, "c2": surface.CodeToolOutcomeUnknown,
		"c3": surface.CodeApprovalUnavailable, "c4": surface.CodeToolNotStarted}
	for i, id := range []string{"c1", "c2", "c3", "c4"} {
		raw := ds[i].Raw()
		if !strings.Contains(raw, `"call_id":"`+id+`"`) || !strings.Contains(raw, codes[id]+": ") {
			t.Errorf("closer %d is %s, want %s for %s", i, raw, codes[id], id)
		}
		if codes[id] == surface.CodeApprovalUnavailable && !strings.Contains(raw, "did not run") {
			t.Errorf("closer for %s does not say the call did not run: %s", id, raw)
		}
	}

	state, err := l.st.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := surface.Restore(context.Background(), nil, surface.CheckpointVersion, state, readSeqs(l.recs))
	if err != nil {
		t.Fatal(err)
	}
	check(restored.Open())
	if _, err := surface.Restore(context.Background(), nil, 1, state, readSeqs(l.recs)); !errors.Is(err, surface.ErrCheckpoint) {
		t.Fatalf("a version-1 checkpoint: %v, want refused", err)
	}
	replayed, err := surface.Replay(nil, l.recs)
	if err != nil {
		t.Fatal(err)
	}
	check(replayed.Open())
}
