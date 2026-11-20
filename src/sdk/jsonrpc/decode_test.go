package jsonrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

// checkDecode is the property every input must hold: Decode either returns a frame
// that re-encodes to a line Decode reads back to the same bytes, or fails with an
// *Error of Table; the line reader over the same bytes fails only with table errors
// or io.EOF. A panic fails the caller.
func checkDecode(t *testing.T, b []byte) {
	t.Helper()
	f, err := Decode(b)
	if err != nil {
		assertTableError(t, err, ReasonParseError, ReasonInvalidRequest)
	} else {
		enc, err := Encode(f)
		if err != nil {
			t.Fatalf("Encode(Decode(%q)) failed: %v", b, err)
		}
		f2, err := Decode(enc)
		if err != nil {
			t.Fatalf("Decode(Encode(Decode(%q))) = %q failed: %v", b, enc, err)
		}
		if reflect.TypeOf(f) != reflect.TypeOf(f2) {
			t.Fatalf("frame kind changed over a round trip: %T then %T", f, f2)
		}
		enc2, err := Encode(f2)
		if err != nil || !bytes.Equal(enc, enc2) {
			t.Fatalf("encoding is not stable: %q then %q (%v)", enc, enc2, err)
		}
	}
	r := NewReader(bytes.NewReader(b), 64)
	for range 1 + bytes.Count(b, []byte{'\n'}) {
		_, err := r.Read()
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		assertTableError(t, err, ReasonParseError, ReasonInvalidRequest, ReasonLineTooLong)
		if HasReason(err, ReasonLineTooLong) {
			break
		}
	}
}

func assertTableError(t *testing.T, err error, allowed ...string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v (%T) is not a table error", err, err)
	}
	if _, ok := Lookup(e.Reason); !ok {
		t.Fatalf("reason %q is not in the table", e.Reason)
	}
	for _, a := range allowed {
		if e.Reason == a {
			return
		}
	}
	t.Fatalf("reason %q, want one of %v", e.Reason, allowed)
}

// TestDecodeSeeds feeds every compiled-in seed to the decoder (TC-04a).
func TestDecodeSeeds(t *testing.T) {
	if len(decodeSeeds) == 0 {
		t.Fatal("no seeds")
	}
	for i := range decodeSeeds {
		seed := string(decodeSeeds[i]) // a copy: the fuzz seeds stay compiled-in literals (QG-6)
		checkDecode(t, []byte(seed))
	}
}

func TestDecodeFrames(t *testing.T) {
	cases := []struct {
		line string
		want Frame
	}{
		{`{"jsonrpc":"2.0","id":1,"method":"session/list"}`, &Request{ID: Int64ID(1), Method: "session/list"}},
		{`{"method":"m","jsonrpc":"2.0","id":"s","params":{"a":1}}`, &Request{ID: StringID("s"), Method: "m", Params: json.RawMessage(`{"a":1}`)}},
		{` {"jsonrpc":"2.0","method":"n","params":[]} `, &Notification{Method: "n", Params: json.RawMessage(`[]`)}},
		{`{"jsonrpc":"2.0","id":-3,"result":null}`, &Response{ID: Int64ID(-3), Result: json.RawMessage(`null`)}},
		{`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"no"}}`,
			&Response{ID: Int64ID(1), Error: &Error{Code: -32601, Message: "no", Reason: ReasonMethodNotFound}}},
		{`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"x","data":null}}`,
			&Response{ID: Int64ID(1), Error: &Error{Code: -32000, Message: "x", Reason: ReasonInternal}}},
		{`{"jsonrpc":"2.0","id":null,"error":{"code":-1,"message":"x","data":{"code":"session/not-found","details":{"k":1}}}}`,
			&Response{Error: &Error{Code: -1, Message: "x", Reason: "session/not-found", Details: json.RawMessage(`{"k":1}`)}}},
	}
	for _, tc := range cases {
		got, err := Decode([]byte(tc.line))
		if err != nil {
			t.Fatalf("Decode(%s): %v", tc.line, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Decode(%s) = %#v, want %#v", tc.line, got, tc.want)
		}
	}
}

func TestDecodeRefusals(t *testing.T) {
	cases := []struct{ line, reason, mention string }{
		{``, ReasonParseError, "not JSON"},
		{`nope`, ReasonParseError, "not JSON"},
		{`42`, ReasonParseError, "not a JSON object"},
		{`{} {}`, ReasonParseError, "not JSON"},
		{`[]`, ReasonInvalidRequest, "batch"},
		{`{"id":1,"method":"x"}`, ReasonInvalidRequest, "jsonrpc"},
		{`{"jsonrpc":2,"id":1,"method":"x"}`, ReasonInvalidRequest, "jsonrpc"},
		{`{"jsonrpc":"2.0","id":true,"method":"x"}`, ReasonInvalidRequest, "id"},
		{`{"jsonrpc":"2.0","id":"\ud800x","method":"x","id":2}`, ReasonInvalidRequest, "repeated"},
		{`{"jsonrpc":"2.0","id":1,"method":"x","x":1}`, ReasonInvalidRequest, "unknown member"},
		{`{"jsonrpc":"2.0","id":1,"method":""}`, ReasonInvalidRequest, "method"},
		{`{"jsonrpc":"2.0","id":1,"method":7}`, ReasonInvalidRequest, "method"},
		{`{"jsonrpc":"2.0","id":1,"method":"x","error":{}}`, ReasonInvalidRequest, "carries no"},
		{`{"jsonrpc":"2.0","id":1,"method":"x","params":null}`, ReasonInvalidRequest, "params"},
		{`{"jsonrpc":"2.0","id":null,"method":"x"}`, ReasonInvalidRequest, "never null"},
		{`{"jsonrpc":"2.0","result":1}`, ReasonInvalidRequest, "needs \"id\""},
		{`{"jsonrpc":"2.0","id":1,"result":1,"params":{}}`, ReasonInvalidRequest, "params"},
		{`{"jsonrpc":"2.0","id":1}`, ReasonInvalidRequest, "exactly one"},
		{`{"jsonrpc":"2.0","id":null,"result":1}`, ReasonInvalidRequest, "null id"},
		{`{"jsonrpc":"2.0","id":1,"error":{"code":1}}`, ReasonInvalidRequest, "error"},
		{`{"jsonrpc":"2.0","id":1,"error":{"code":1.5,"message":"m"}}`, ReasonInvalidRequest, "error"},
		{`{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":"m","x":1}}`, ReasonInvalidRequest, "error"},
		{`{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":"m","data":{}}}`, ReasonInvalidRequest, "domain"},
		{`{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":"m","data":{"code":"x"}}}`, ReasonInvalidRequest, "domain"},
		{`{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":"m","data":{"code":"a/b","more":1}}}`, ReasonInvalidRequest, "domain"},
	}
	for _, tc := range cases {
		_, err := Decode([]byte(tc.line))
		if !HasReason(err, tc.reason) {
			t.Errorf("Decode(%s) = %v, want %s", tc.line, err, tc.reason)
			continue
		}
		if !strings.Contains(err.Error(), tc.mention) {
			t.Errorf("Decode(%s) = %v, want a message mentioning %q", tc.line, err, tc.mention)
		}
	}
}

// A well-formed request round-trips and exactly one newline frames it.
func TestRequestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	want := &Request{ID: Int64ID(1), Method: "session/list"}
	if err := NewWriter(&buf, 0).Write(want); err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(buf.Bytes(), []byte{'\n'}); n != 1 || !bytes.HasSuffix(buf.Bytes(), []byte{'\n'}) {
		t.Fatalf("line %q holds %d newlines, want one at the end", buf.Bytes(), n)
	}
	got, err := NewReader(&buf, 0).Read()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestEncode(t *testing.T) {
	cases := []struct {
		f    Frame
		want string
	}{
		{&Request{ID: StringID("a\n"), Method: "m", Params: json.RawMessage("{\n \"a\": 1\n}")}, `{"jsonrpc":"2.0","id":"a\n","method":"m","params":{"a":1}}`},
		{&Notification{Method: "n"}, `{"jsonrpc":"2.0","method":"n"}`},
		{&Response{ID: Int64ID(2)}, `{"jsonrpc":"2.0","id":2,"result":null}`},
		{&Response{Error: &Error{Code: -32700, Message: "m", Reason: ReasonParseError}},
			`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"m","data":{"code":"jsonrpc/parse-error"}}}`},
		{&Response{ID: Int64ID(1), Error: &Error{Code: 1, Message: "m"}}, `{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":"m"}}`},
	}
	for _, tc := range cases {
		got, err := Encode(tc.f)
		if err != nil || string(got) != tc.want {
			t.Errorf("Encode(%#v) = %s, %v; want %s", tc.f, got, err, tc.want)
		}
	}
	refused := []Frame{
		&Request{Method: "m"},
		&Request{ID: Int64ID(1)},
		&Notification{Method: "n", Params: json.RawMessage(`1`)},
		&Notification{Method: "n", Params: json.RawMessage(`{`)},
		&Response{ID: Int64ID(1), Result: json.RawMessage(`1`), Error: &Error{}},
		&Response{Result: json.RawMessage(`1`)},
		&Response{ID: Int64ID(1), Error: &Error{Details: json.RawMessage(`{`)}},
		nil,
	}
	for _, f := range refused {
		if out, err := Encode(f); err == nil {
			t.Errorf("Encode(%#v) = %s, want an error", f, out)
		}
	}
}

func TestID(t *testing.T) {
	for _, tc := range []struct {
		id   ID
		want string
	}{{ID{}, "null"}, {Int64ID(-9), "-9"}, {StringID(`q"`), `"q\""`}} {
		if got := tc.id.String(); got != tc.want {
			t.Errorf("%#v.String() = %s, want %s", tc.id, got, tc.want)
		}
	}
	if !(ID{}).IsNull() || Int64ID(0).IsNull() {
		t.Error("IsNull is wrong")
	}
	if _, ok := parseID(json.RawMessage(`"\x"`)); ok {
		t.Error("a bad string id was accepted")
	}
	if _, ok := parseID(nil); ok {
		t.Error("an empty id was accepted")
	}
}
