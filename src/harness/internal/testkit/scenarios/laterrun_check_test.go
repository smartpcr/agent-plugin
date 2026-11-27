//go:build scenario

package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// A host caps a history page by count and by bytes, so the later-run scenarios'
// history read must follow More from each page's last seq until the history is
// complete: every event once, in order, a short byte-capped page included.
func TestHistoryPagesFollowMore(t *testing.T) {
	pages := map[int64]protocol.SessionHistoryResult{
		0:    {Events: evRange(1, 1000), More: true},
		1000: {Events: evRange(1001, 1002), More: true},
		1002: {Events: evRange(1003, 1500)},
	}
	var asked []int64
	call := func(_ context.Context, method string, params, result any) error {
		p := params.(protocol.SessionHistoryParams)
		if method != "session/history" || p.SessionID != "s1" {
			t.Fatalf("call %s %+v", method, p)
		}
		asked = append(asked, p.AfterSeq)
		*result.(*protocol.SessionHistoryResult) = pages[p.AfterSeq]
		return nil
	}
	evs, err := historyPages(context.Background(), call, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1500 {
		t.Fatalf("got %d events, want 1500", len(evs))
	}
	for i, e := range evs {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d", i, e.Seq)
		}
	}
	if len(asked) != 3 || asked[1] != 1000 || asked[2] != 1002 {
		t.Errorf("asked after %v, want [0 1000 1002]", asked)
	}
}

// A page that says more but carries no event cannot advance the cursor: the
// read fails rather than loop or stop short silently.
func TestHistoryPagesRefuseAnEmptyMorePage(t *testing.T) {
	call := func(_ context.Context, _ string, _, result any) error {
		*result.(*protocol.SessionHistoryResult) = protocol.SessionHistoryResult{More: true}
		return nil
	}
	if _, err := historyPages(context.Background(), call, "s1"); err == nil || !strings.Contains(err.Error(), "no event") {
		t.Errorf("historyPages on an empty more page: %v", err)
	}
}

func evRange(from, to int64) []protocol.Event {
	var evs []protocol.Event
	for seq := from; seq <= to; seq++ {
		evs = append(evs, protocol.Event{Seq: seq, Type: "x", Data: json.RawMessage(`{}`)})
	}
	return evs
}
