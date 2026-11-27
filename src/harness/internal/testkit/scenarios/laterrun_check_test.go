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
		-1:   {Events: evRange(1, 1000), More: true},
		1000: {Events: evRange(1001, 1002), More: true},
		1002: {Events: evRange(1003, 1500)},
	}
	var asked []int64
	evs, err := historyPages(context.Background(), wireHistory(t, pages, &asked), "s1")
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
	if len(asked) != 3 || asked[0] != -1 || asked[1] != 1000 || asked[2] != 1002 {
		t.Errorf("asked after %v, want [-1 1000 1002]", asked)
	}
}

// A byte-capped first page can hold seq 0 alone: the next request must carry
// afterSeq 0 on the wire, which the host reads as "after seq 0", not omit it,
// which it reads as "from seq 0" and answers with the first page again.
func TestHistoryPagesSendAZeroCursor(t *testing.T) {
	pages := map[int64]protocol.SessionHistoryResult{
		-1: {Events: evRange(0, 0), More: true},
		0:  {Events: evRange(1, 3)},
	}
	var asked []int64
	evs, err := historyPages(context.Background(), wireHistory(t, pages, &asked), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 4 || evs[0].Seq != 0 || evs[3].Seq != 3 {
		t.Fatalf("got %d events %v, want seq 0..3 once each", len(evs), evs)
	}
	if len(asked) != 2 || asked[0] != -1 || asked[1] != 0 {
		t.Errorf("asked after %v, want [-1 0]", asked)
	}
}

// wireHistory is a session/history host over the serialized params, as
// sdkserver reads them: an omitted afterSeq is the first page (-1), so a
// dropped zero cursor shows as a repeated -1, which fails the read.
func wireHistory(t *testing.T, pages map[int64]protocol.SessionHistoryResult, asked *[]int64) historyCall {
	return func(_ context.Context, method string, params, result any) error {
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		var p struct {
			SessionID string `json:"sessionId"`
			AfterSeq  *int64 `json:"afterSeq"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || method != "session/history" || p.SessionID != "s1" {
			t.Fatalf("call %s %s: %v", method, raw, err)
		}
		after := int64(-1)
		if p.AfterSeq != nil {
			after = *p.AfterSeq
		}
		if len(*asked) > 0 && after == (*asked)[len(*asked)-1] {
			t.Fatalf("asked after %d twice (%s): the cursor did not advance", after, raw)
		}
		*asked = append(*asked, after)
		*result.(*protocol.SessionHistoryResult) = pages[after]
		return nil
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

// A signed-out client route fails its attempt and its turn with
// llm/sign-in-required; signInFailure names the first such event, the reason
// noteSignIn records for the scenario's report row (DH-E-UTIL-2), and finds
// nothing in a history that failed otherwise.
func TestSignInFailureNamesTheCode(t *testing.T) {
	ev := func(seq int64, typ, data string) protocol.Event {
		return protocol.Event{Seq: seq, Type: typ, Data: json.RawMessage(data)}
	}
	signedOut := []protocol.Event{
		ev(0, "turn/start", `{}`),
		ev(1, "assistant/attempt", `{"failure":{"code":"llm/sign-in-required","class":"auth"}}`),
		ev(2, "turn/end", `{"turn":1,"reason":{"kind":"failed","failure":{"code":"llm/sign-in-required"}}}`),
	}
	if got := signInFailure(signedOut); !strings.HasPrefix(got, "llm/sign-in-required: event 1 assistant/attempt") {
		t.Errorf("signInFailure = %q, want the attempt at seq 1 named", got)
	}
	if got := signInFailure(signedOut[2:]); !strings.Contains(got, "event 2 turn/end") {
		t.Errorf("signInFailure of the turn end alone = %q", got)
	}
	other := []protocol.Event{ev(1, "assistant/attempt", `{"failure":{"code":"llm/rate-limited"}}`), ev(2, "turn/end", `{"reason":{"kind":"completed"}}`)}
	if got := signInFailure(other); got != "" {
		t.Errorf("signInFailure of another failure = %q, want none", got)
	}
}

func evRange(from, to int64) []protocol.Event {
	var evs []protocol.Event
	for seq := from; seq <= to; seq++ {
		evs = append(evs, protocol.Event{Seq: seq, Type: "x", Data: json.RawMessage(`{}`)})
	}
	return evs
}
