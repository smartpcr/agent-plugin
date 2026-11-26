package sdkserver_test

import (
	"context"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/sdkserver"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// An idempotent resume of a forwarded session keeps its forwarder: the events
// already told are not told again, and later ones follow on with no gap.
func TestIdempotentResumeKeepsTheForwarder(t *testing.T) {
	r := newRig(t)
	cl := connectTold(t, r.server())
	root, _ := cl.create()
	kid := subagent(t, r, root)
	appendRecords(t, kid, 2)
	var resumed protocol.SessionResumeResult
	cl.call("session/resume", protocol.SessionResumeParams{SessionID: root}, &resumed)
	if resumed.SessionID != root {
		t.Fatalf("idempotent resume = %+v", resumed)
	}
	appendRecords(t, kid, 1)
	var seqs []int64
	for len(seqs) < 3 {
		e := decode[protocol.SessionEventNotification](t, cl.await(sdkserver.EventNotification))
		if e.SessionID == string(kid.ID()) {
			seqs = append(seqs, e.Event.Seq)
		}
	}
	for i, s := range seqs {
		if s != int64(i) {
			t.Fatalf("subagent told seqs %v, want 0, 1, 2 once each", seqs)
		}
	}
}

// A forwarded subagent that closes is told every event up to its close, its
// close's own included, while its root stays open.
func TestClosedSubagentIsDrained(t *testing.T) {
	r := newRig(t)
	cl := connectTold(t, r.server())
	root, _ := cl.create()
	kid := subagent(t, r, root)
	appendRecords(t, kid, 2)
	for told := false; !told; {
		e := decode[protocol.SessionEventNotification](t, cl.await(sdkserver.EventNotification))
		told = e.SessionID == string(kid.ID()) && e.Event.Seq == 1
	}
	if err := kid.Close(context.Background()); err != nil {
		t.Fatalf("close the subagent: %v", err)
	}
	last := int64(kid.Len()) - 1
	for seen := int64(1); seen < last; {
		e := decode[protocol.SessionEventNotification](t, cl.await(sdkserver.EventNotification))
		if e.SessionID != string(kid.ID()) {
			continue
		}
		if e.Event.Seq != seen+1 {
			t.Fatalf("subagent told seq %d after %d", e.Event.Seq, seen)
		}
		seen = e.Event.Seq
	}
	// The root is still driven and forwarded.
	var page protocol.SessionHistoryResult
	cl.call("session/history", protocol.SessionHistoryParams{SessionID: root, Limit: 1}, &page)
}
