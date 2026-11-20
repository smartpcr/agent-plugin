package jsonrpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestTable(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Table {
		if !ValidReason(e.Reason) {
			t.Errorf("reason %q is not <domain>/<reason>", e.Reason)
		}
		if seen[e.Reason] {
			t.Errorf("reason %q is listed twice", e.Reason)
		}
		seen[e.Reason] = true
		if got, ok := Lookup(e.Reason); !ok || got != e {
			t.Errorf("Lookup(%q) = %v, %v", e.Reason, got, ok)
		}
		if e.When == "" {
			t.Errorf("reason %q has no description", e.Reason)
		}
	}
	if _, ok := Lookup("jsonrpc/nope"); ok {
		t.Error("Lookup found a reason not in the table")
	}
	for _, bad := range []string{"", "x", "/x", "x/", "X/y", "a/b/c", "a/b_c"} {
		if ValidReason(bad) {
			t.Errorf("ValidReason(%q) = true", bad)
		}
	}
}

func TestNewErrorPanicsOutsideTable(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("newError accepted a reason not in the table")
		}
	}()
	newError("jsonrpc/nope", "")
}

func TestErrorForms(t *testing.T) {
	e := newError(ReasonBusy, "full")
	if e.Error() != "jsonrpc/busy: full" {
		t.Fatalf("Error() = %q", e.Error())
	}
	if (&Error{Reason: ReasonBusy}).Error() != ReasonBusy {
		t.Fatal("an error without a message should print its reason")
	}
	wrapped := fmt.Errorf("outer: %w", e)
	if !errors.Is(wrapped, &Error{Reason: ReasonBusy}) || errors.Is(wrapped, &Error{Reason: ReasonClosed}) {
		t.Fatal("errors.Is should match by reason")
	}
	if e.Is(errBroken) {
		t.Fatal("Is matched a non-*Error")
	}
	if !HasReason(wrapped, ReasonBusy) || HasReason(errBroken, ReasonBusy) {
		t.Fatal("HasReason mismatch")
	}

	b, err := json.Marshal(&Error{Code: 1, Message: "m", Reason: "a/b", Details: json.RawMessage(`{"k":1}`)})
	if err != nil || string(b) != `{"code":1,"message":"m","data":{"code":"a/b","details":{"k":1}}}` {
		t.Fatalf("Marshal = %s, %v", b, err)
	}
	b, err = json.Marshal(&Error{Code: 1, Message: "m"})
	if err != nil || string(b) != `{"code":1,"message":"m"}` {
		t.Fatalf("Marshal without data = %s, %v", b, err)
	}
}

func TestDefaultReason(t *testing.T) {
	for code, want := range map[int]string{
		CodeParseError:  ReasonParseError,
		CodeCancelled:   ReasonCancelled,
		CodeApplication: ReasonInternal,
		-1:              ReasonInternal,
	} {
		if got := defaultReason(code); got != want {
			t.Errorf("defaultReason(%d) = %q, want %q", code, got, want)
		}
	}
}

type reasonErr struct{ reason, msg string }

func (e reasonErr) Error() string  { return e.msg }
func (e reasonErr) Reason() string { return e.reason }

func TestToWire(t *testing.T) {
	own := newError(ReasonBusy, "x")
	if toWire(fmt.Errorf("w: %w", own)) != own {
		t.Fatal("an *Error should pass through")
	}
	got := toWire(reasonErr{"session/not-found", "session/not-found: s1"})
	if got.Code != CodeApplication || got.Reason != "session/not-found" || got.Message != "s1" {
		t.Fatalf("toWire(reasoner) = %+v", got)
	}
	got = toWire(reasonErr{"session/not-found", "plain"})
	if got.Message != "plain" {
		t.Fatalf("message = %q", got.Message)
	}
	for _, err := range []error{errors.New("secret path /etc"), reasonErr{"Bad Reason", "secret"}} {
		got := toWire(err)
		if got.Reason != ReasonInternal || got.Message != "internal error" {
			t.Fatalf("toWire(%v) = %+v; internal text must not cross the wire", err, got)
		}
	}
}
