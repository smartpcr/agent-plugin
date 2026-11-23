package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc/jsonrpctest"
)

// TestNegotiate is the stage scenario "Version negotiation": a host serving [1]
// picks 1 for an offer of [1,2] and refuses [3] with sdk/version-unsupported whose
// details are [1]; the highest common version wins.
//
//dsh:test DH-T-SDK-7
func TestNegotiate(t *testing.T) {
	if v, err := Negotiate([]int{1, 2}, []int{1}); err != nil || v != 1 {
		t.Fatalf("Negotiate([1,2], [1]) = %d, %v", v, err)
	}
	if v, err := Negotiate([]int{1, 3, 2}, []int{2, 1, 3}); err != nil || v != 3 {
		t.Fatalf("Negotiate([1,3,2], [2,1,3]) = %d, %v", v, err)
	}
	_, err := Negotiate([]int{3}, []int{1})
	var e *jsonrpc.Error
	if !jsonrpc.HasReason(err, ReasonVersionUnsupported) || !asError(err, &e) || string(e.Details) != `[1]` || e.Code != jsonrpc.CodeApplication {
		t.Fatalf("Negotiate([3], [1]) = %#v", err)
	}
	if _, err := Negotiate(nil, []int{2, 1, 2}); !asError(err, &e) || string(e.Details) != `[1,2]` {
		t.Fatalf("Negotiate(none, [2,1,2]) details = %s", e.Details)
	}
	if _, err := Negotiate([]int{1}, nil); !asError(err, &e) || string(e.Details) != `[]` {
		t.Fatalf("Negotiate([1], none) details = %s", e.Details)
	}
}

func asError(err error, e **jsonrpc.Error) bool {
	v, ok := err.(*jsonrpc.Error)
	*e = v
	return ok
}

// gatedPeer serves a Gate over the jsonrpctest peer: initialize negotiates against
// [1] and session/list decodes its params strictly.
func gatedPeer(t *testing.T) (*Gate, *jsonrpctest.Peer) {
	regs, err := Register([]Method{Methods[0], Methods[3]},
		Handle(InitializeMethod, func(_ context.Context, p InitializeParams) (InitializeResult, error) {
			v, err := Negotiate(p.ProtocolVersions, []int{Version})
			return InitializeResult{ProtocolVersion: v}, err
		}),
		Handle("session/list", func(_ context.Context, p SessionListParams) (SessionListResult, error) {
			return SessionListResult{Sessions: []SessionSummary{}, NextCursor: p.Cursor}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	g := NewGate(regs)
	_, p := jsonrpctest.NewPeer(t, jsonrpc.Options{Methods: g.Methods()})
	return g, p
}

// TestGate is the stage scenario "Calls gated on initialize": over the fake peer,
// session/list before initialize is sdk/not-initialized, a failed initialize can be
// retried, a second initialize is sdk/already-initialized, and an unknown param
// field is jsonrpc/invalid-params naming $.foo.
//
//dsh:test DH-T-SDK-7
func TestGate(t *testing.T) {
	g, p := gatedPeer(t)
	p.Request(jsonrpc.Int64ID(1), "session/list", map[string]any{})
	p.ExpectError(jsonrpc.Int64ID(1), ReasonNotInitialized)

	p.Request(jsonrpc.Int64ID(2), InitializeMethod, map[string]any{"protocolVersions": []int{3}})
	p.ExpectError(jsonrpc.Int64ID(2), ReasonVersionUnsupported)
	if g.Initialized() {
		t.Fatal("a failed initialize opened the gate")
	}

	p.Request(jsonrpc.Int64ID(3), InitializeMethod, map[string]any{"protocolVersions": []int{1, 2}})
	var res InitializeResult
	if r := p.ExpectResponse(jsonrpc.Int64ID(3)); r.Error != nil || json.Unmarshal(r.Result, &res) != nil || res.ProtocolVersion != 1 {
		t.Fatalf("initialize = %+v", r)
	}
	if !g.Initialized() {
		t.Fatal("the gate is closed after initialize")
	}
	p.Request(jsonrpc.Int64ID(4), InitializeMethod, map[string]any{"protocolVersions": []int{1}})
	p.ExpectError(jsonrpc.Int64ID(4), ReasonAlreadyInitialized)

	p.Request(jsonrpc.Int64ID(5), "session/list", map[string]any{"cursor": "c1"})
	if r := p.ExpectResponse(jsonrpc.Int64ID(5)); r.Error != nil || string(r.Result) != `{"sessions":[],"nextCursor":"c1"}` {
		t.Fatalf("session/list = %+v", r)
	}
	p.Request(jsonrpc.Int64ID(6), "session/list", map[string]any{"foo": 1})
	e := p.ExpectError(jsonrpc.Int64ID(6), jsonrpc.ReasonInvalidParams)
	if string(e.Details) != `{"issues":[{"path":"$.foo","message":"unknown field"}]}` {
		t.Fatalf("details = %s", e.Details)
	}
}

// TestGateInitializeRunsOnce: while initialize runs, another request is
// sdk/not-initialized and a second initialize is sdk/already-initialized.
//
//dsh:test DH-T-SDK-7
func TestGateInitializeRunsOnce(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	g := NewGate(map[string]jsonrpc.Handler{
		InitializeMethod: func(context.Context, json.RawMessage) (any, error) {
			close(started)
			<-release
			return Empty{}, nil
		},
		"shutdown": func(context.Context, json.RawMessage) (any, error) { return Empty{}, nil },
	})
	_, p := jsonrpctest.NewPeer(t, jsonrpc.Options{Methods: g.Methods()})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	p.Request(jsonrpc.Int64ID(1), InitializeMethod, nil)
	<-started
	p.Request(jsonrpc.Int64ID(2), "shutdown", nil)
	p.ExpectError(jsonrpc.Int64ID(2), ReasonNotInitialized)
	p.Request(jsonrpc.Int64ID(3), InitializeMethod, nil)
	p.ExpectError(jsonrpc.Int64ID(3), ReasonAlreadyInitialized)
	close(release)
	if r := p.ExpectResponse(jsonrpc.Int64ID(1)); r.Error != nil {
		t.Fatalf("initialize = %+v", r.Error)
	}
	p.Request(jsonrpc.Int64ID(4), "shutdown", nil)
	if r := p.ExpectResponse(jsonrpc.Int64ID(4)); r.Error != nil || string(r.Result) != `{}` {
		t.Fatalf("shutdown = %+v", r)
	}
}

type inner struct {
	B int `json:"b"`
}

type embedded struct {
	E string `json:"e"`
}

type strict struct {
	embedded
	*inner `json:"-"`
	A      int              `json:"a"`
	In     inner            `json:"in"`
	List   []inner          `json:"list"`
	Map    map[string]inner `json:"map"`
	Ptr    *inner           `json:"ptr"`
	Raw    json.RawMessage  `json:"raw"`
	Any    any              `json:"any"`
	Bare   int
	Dash   int       `json:"-"`
	Arr    [1]inner  `json:"arr"`
	Text   textValue `json:"text"`
}

type textValue struct{ s string }

func (v *textValue) UnmarshalText(b []byte) error { v.s = string(b); return nil }

// TestDecodeParams: decoding is strict, and every unknown field is reported with its
// path through objects, arrays and maps, matched as encoding/json matches names.
//
//dsh:test DH-T-SDK-7
func TestDecodeParams(t *testing.T) {
	v, err := DecodeParams[strict](json.RawMessage(`{"e":"x","a":1,"In":{"b":2},"list":[{"b":3}],"map":{"k":{"b":4}},"ptr":{"b":5},"raw":{"zz":1},"any":{"zz":1},"Bare":6,"arr":[{"b":7}],"text":"t"}`))
	if err != nil || v.E != "x" || v.In.B != 2 || v.List[0].B != 3 || v.Map["k"].B != 4 || v.Ptr.B != 5 || v.Bare != 6 || v.Text.s != "t" {
		t.Fatalf("DecodeParams = %+v, %v", v, err)
	}
	if v, err := DecodeParams[strict](nil); err != nil || v.A != 0 {
		t.Fatalf("absent params = %+v, %v", v, err)
	}
	for _, tc := range []struct{ raw, details string }{
		{`{"foo":1}`, `{"issues":[{"path":"$.foo","message":"unknown field"}]}`},
		{`{"in":{"zz":1},"list":[{"b":1},{"q":2}],"map":{"k":{"w":1}},"ptr":{"p":1},"arr":[{"r":1}],"top":1,"skip":1,"Dash":1}`,
			`{"issues":[{"path":"$.Dash","message":"unknown field"},{"path":"$.arr[0].r","message":"unknown field"},{"path":"$.in.zz","message":"unknown field"},{"path":"$.list[1].q","message":"unknown field"},{"path":"$.map.k.w","message":"unknown field"},{"path":"$.ptr.p","message":"unknown field"},{"path":"$.skip","message":"unknown field"},{"path":"$.top","message":"unknown field"}]}`},
		{`{"in":{"b":"x"}}`, `{"issues":[{"path":"$.in.b","message":"want int, got string"}]}`},
		{`[1]`, `{"issues":[{"path":"$","message":"want protocol.strict, got array"}]}`},
		{`{"a":1} {"a":2}`, `{"issues":[{"path":"$","message":"trailing data after the params value"}]}`},
		{`{"a":`, `{"issues":[{"path":"$","message":"unexpected EOF"}]}`},
	} {
		_, err := DecodeParams[strict](json.RawMessage(tc.raw))
		var e *jsonrpc.Error
		if !asError(err, &e) || e.Reason != jsonrpc.ReasonInvalidParams || e.Code != jsonrpc.CodeInvalidParams {
			t.Fatalf("DecodeParams(%s) = %v", tc.raw, err)
		}
		if string(e.Details) != tc.details {
			t.Errorf("DecodeParams(%s) details\n%s\nwant\n%s", tc.raw, e.Details, tc.details)
		}
		if !strings.HasPrefix(e.Message, "the parameters do not decode: $") {
			t.Errorf("message = %q", e.Message)
		}
	}
	// A name the walker cannot place still names the field.
	if got := issuesOf(json.RawMessage(`not json`), reflectTypeOfStrict, errUnknownFoo); len(got) != 1 || got[0].Path != "$.foo" {
		t.Fatalf("fallback issues = %+v", got)
	}
}

// TestSDKErrorPanicsOutsideTable: an SDK error can only carry a tabled reason.
//
//dsh:test DH-T-SDK-6
func TestSDKErrorPanicsOutsideTable(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("sdkError accepted a reason not in the table")
		}
	}()
	sdkError("sdk/nope", "")
}

var (
	reflectTypeOfStrict = reflect.TypeFor[strict]()
	errUnknownFoo       = errors.New(`json: unknown field "foo"`)
)
