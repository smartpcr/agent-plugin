package protocol

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// fake returns a Handle registration of name whose body answers the zero R.
func fake[P, R any](name string) Registration {
	return Handle(name, func(context.Context, P) (R, error) {
		var r R
		return r, nil
	})
}

// everyRequest registers a fake handler of the row's types for every request of
// Methods, in table order.
func everyRequest() []Registration {
	return []Registration{
		fake[InitializeParams, InitializeResult]("initialize"),
		fake[SessionCreateParams, SessionCreateResult]("session/create"),
		fake[SessionResumeParams, SessionResumeResult]("session/resume"),
		fake[SessionListParams, SessionListResult]("session/list"),
		fake[SessionPromptParams, SessionPromptResult]("session/prompt"),
		fake[SessionQueryParams, SessionQueryResult]("session/query"),
		fake[SessionRef, LastSeqResult]("session/wait"),
		fake[SessionCancelParams, SessionCancelResult]("session/cancel"),
		fake[SessionRef, LastSeqResult]("session/close"),
		fake[SessionHistoryParams, SessionHistoryResult]("session/history"),
		fake[ModelSetParams, ModelSetResult]("session/model/set"),
		fake[SessionRef, WorkingDirectoryResult]("session/working-directory/get"),
		fake[WorkingDirectorySetParams, WorkingDirectoryResult]("session/working-directory/set"),
		fake[Empty, Empty]("shutdown"),
		fake[ApprovalRequest, ApprovalResult]("approval/request"),
		fake[QuestionRequest, QuestionResult]("question/ask"),
		fake[ToolCallParams, ToolCallResult]("tool/call"),
	}
}

// TestRegisterMatchesTheTableBothWays is the stage scenario "Table and handlers
// match both ways": every request row of Methods registered is accepted; one row
// too few and one registration too many fail naming the method.
//
//dsh:test DH-T-SDK-6
func TestRegisterMatchesTheTableBothWays(t *testing.T) {
	all := everyRequest()
	handlers, err := Register(Methods, all...)
	if err != nil {
		t.Fatalf("Register(every row) = %v", err)
	}
	var want []string
	for _, m := range Methods {
		if m.Kind == Request {
			want = append(want, m.Name)
		}
	}
	got := slices.Sorted(func(yield func(string) bool) {
		for name := range handlers {
			if !yield(name) {
				return
			}
		}
	})
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("handlers = %v, want %v", got, want)
	}

	short := slices.DeleteFunc(slices.Clone(all), func(r Registration) bool { return r.Name == "session/history" })
	if _, err := Register(Methods, short...); err == nil || !strings.Contains(err.Error(), `request "session/history" of the method table has no handler`) {
		t.Fatalf("Register(one short) = %v", err)
	}
	extra := append(slices.Clone(all), fake[Empty, Empty]("session/fork"))
	if _, err := Register(Methods, extra...); err == nil || !strings.Contains(err.Error(), `"session/fork" is registered but is not a request`) {
		t.Fatalf("Register(one extra) = %v", err)
	}
}

// TestRegisterRefusals: a type mismatch, a duplicate, a notification row, a nil
// handler and a missing row are each reported, all in one joined error.
//
//dsh:test DH-T-SDK-6
func TestRegisterRefusals(t *testing.T) {
	table := []Method{
		req[SessionRef, LastSeqResult]("a", FromClient, ""),
		req[Empty, Empty]("b", FromClient, ""),
		req[Empty, Empty]("c", FromClient, ""),
		note[CancelParams]("d.note", FromHost, ""),
	}
	_, err := Register(table,
		fake[SessionRef, Empty]("a"),
		fake[Empty, Empty]("b"),
		fake[Empty, Empty]("b"),
		fake[CancelParams, Empty]("d.note"),
		Registration{Name: "c", Params: reflect.TypeFor[Empty](), Result: reflect.TypeFor[Empty]()},
	)
	if err == nil {
		t.Fatal("Register accepted a broken set")
	}
	for _, want := range []string{
		`"a" is registered as protocol.SessionRef -> protocol.Empty; the method table says protocol.SessionRef -> protocol.LastSeqResult`,
		`"b" is registered twice`,
		`"d.note" is registered but is not a request`,
		`"c" is registered without a handler`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), `request "a" of the method table has no handler`) {
		t.Error("a mistyped registration was also reported as missing")
	}
}

// TestHandleDecodesStrictly: a registered handler decodes its params with
// DecodeParams, so an unknown field never reaches the body.
//
//dsh:test DH-T-SDK-6, DH-T-SDK-7
func TestHandleDecodesStrictly(t *testing.T) {
	called := false
	r := Handle("session/wait", func(_ context.Context, p SessionRef) (LastSeqResult, error) {
		called = true
		return LastSeqResult{LastSeq: int64(len(p.SessionID))}, nil
	})
	got, err := r.Handler(context.Background(), json.RawMessage(`{"sessionId":"abc"}`))
	if err != nil || got != (LastSeqResult{LastSeq: 3}) {
		t.Fatalf("handler = %+v, %v", got, err)
	}
	called = false
	if _, err := r.Handler(context.Background(), json.RawMessage(`{"sessionId":"a","x":1}`)); !jsonrpc.HasReason(err, jsonrpc.ReasonInvalidParams) || called {
		t.Fatalf("handler with an unknown field = %v (called %v)", err, called)
	}
}

// TestRequests: the rows a host serves are the client's requests and the rows a
// client serves are the host's; no notification is listed.
//
//dsh:test DH-T-SDK-6
func TestRequests(t *testing.T) {
	host, client := Requests(FromClient), Requests(FromHost)
	if len(host)+len(client) != len(everyRequest()) {
		t.Fatalf("%d + %d request rows, want %d", len(host), len(client), len(everyRequest()))
	}
	for _, m := range append(host, client...) {
		if m.Kind != Request {
			t.Errorf("%s is a %s", m.Name, m.Kind)
		}
	}
	if host[0].Name != InitializeMethod || client[0].Name != "approval/request" {
		t.Fatalf("first rows %s, %s", host[0].Name, client[0].Name)
	}
}

// TestEveryReasonIsDeclared: every jsonrpc/* and sdk/* reason of the error table
// (and so of schema.json) is one of the declared constants, and every constant has
// a row.
//
//dsh:test DH-T-SDK-6
func TestEveryReasonIsDeclared(t *testing.T) {
	declared := []string{
		jsonrpc.ReasonParseError, jsonrpc.ReasonInvalidRequest, jsonrpc.ReasonMethodNotFound,
		jsonrpc.ReasonInvalidParams, jsonrpc.ReasonInternal, jsonrpc.ReasonCancelled, jsonrpc.ReasonBusy,
		jsonrpc.ReasonLineTooLong, jsonrpc.ReasonTooManyPending, jsonrpc.ReasonClosed,
		jsonrpc.ReasonWriteStalled, jsonrpc.ReasonUnknownResponse,
		ReasonNotInitialized, ReasonAlreadyInitialized, ReasonVersionUnsupported, ReasonSessionNotOwned,
		ReasonSessionLive, ReasonSubagentSession, ReasonInvalidContent, ReasonShuttingDown,
	}
	var doc struct {
		Errors []struct {
			Reason string `json:"reason"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(Schema(), &doc); err != nil {
		t.Fatal(err)
	}
	var inFile []string
	for _, e := range doc.Errors {
		inFile = append(inFile, e.Reason)
	}
	if !slices.Equal(slices.Sorted(slices.Values(inFile)), slices.Sorted(slices.Values(declared))) {
		t.Fatalf("schema.json reasons %v\ndeclared %v", inFile, declared)
	}
}
