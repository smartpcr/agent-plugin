package herr_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

const (
	reasonRejected herr.Reason = "human/secret-rejected"
	reasonAuth     herr.Reason = "llm/auth"
	reasonTurn     herr.Reason = "agent/turn-failed"
)

// TestKindInnermostAndNoSecret is the TC-06a scenario "Error kinds resolve to one
// table and never leak secrets" (DH-T-UTIL-8).
func TestKindInnermostAndNoSecret(t *testing.T) {
	const value = "sk-test-123"
	secret := herr.Secret(value)
	inner := herr.Newf(reasonRejected, "key %s refused (%v, %q, %x, %#v, %+v)", secret, secret, secret, secret, secret, secret)
	mid := herr.Wrap(reasonAuth, inner, "authenticate")
	outer := fmt.Errorf("turn 3: %w", herr.Wrap(reasonTurn, mid, "step"))

	if got := herr.Kind(outer); got != reasonRejected {
		t.Fatalf("Kind = %q, want the innermost %q", got, reasonRejected)
	}
	if r, ok := herr.ReasonOf(outer); !ok || r != reasonRejected {
		t.Fatalf("ReasonOf = %q, %v", r, ok)
	}
	var e *herr.Error
	if !errors.As(outer, &e) || e.Reason() != string(reasonTurn) {
		t.Fatalf("errors.As found %v", e)
	}

	var logged strings.Builder
	slog.New(slog.NewJSONHandler(&logged, nil)).Error("failed", "err", outer, "key", secret)
	js, _ := json.Marshal(map[string]any{"key": secret, "err": outer.Error()})
	for _, text := range []string{
		outer.Error(),
		fmt.Sprintf("%v|%+v|%#v|%s|%q|%x", outer, outer, outer, outer, outer, outer),
		fmt.Sprintf("%v|%+v|%#v|%s|%q|%x|%X", mid, mid, mid, mid, mid, mid, mid),
		fmt.Sprint(secret), fmt.Sprintf("%#v", struct{ K herr.Secret }{secret}),
		string(js), logged.String(),
	} {
		if strings.Contains(text, value) {
			t.Fatalf("formatted text leaks the secret: %s", text)
		}
	}
	if !strings.Contains(outer.Error(), herr.Redacted) {
		t.Fatalf("formatted text lost the placeholder: %s", outer)
	}
	if secret.Reveal() != value {
		t.Fatal("Reveal lost the value")
	}
}

func TestReasonOfThroughJoinAndForeignTypes(t *testing.T) {
	if _, ok := herr.ReasonOf(errors.New("plain")); ok {
		t.Fatal("a plain error has a reason")
	}
	if herr.Kind(nil) != "" || herr.Wrap(reasonAuth, nil, "x") != nil {
		t.Fatal("nil handling")
	}
	joined := errors.Join(errors.New("a"), fmt.Errorf("b: %w", herr.New(reasonAuth, "x")))
	if herr.Kind(joined) != reasonAuth {
		t.Fatalf("Kind through Join = %q", herr.Kind(joined))
	}
	if herr.Kind(foreign{}) != "store/busy" {
		t.Fatal("a foreign Reason() method is not read")
	}
	wrapped := herr.Wrap(reasonTurn, herr.New(reasonAuth, "x"), "y")
	if !herr.Has(wrapped, reasonTurn) || !herr.Has(wrapped, reasonAuth) || herr.Has(wrapped, reasonRejected) {
		t.Fatal("Has")
	}
	sentinel := herr.New(reasonAuth, "")
	if !errors.Is(wrapped, sentinel) || errors.Is(wrapped, herr.New(reasonRejected, "")) {
		t.Fatal("errors.Is against a sentinel")
	}
	if got := wrapped.Error(); got != "agent/turn-failed: y: llm/auth: x" {
		t.Fatalf("Error() = %q", got)
	}
}

type foreign struct{}

func (foreign) Error() string  { return "busy" }
func (foreign) Reason() string { return "store/busy" }

// TestReasonDeclarations: a reason not of the form <domain>/<reason> fails the
// declaration test (DH-T-UTIL-8).
func TestReasonDeclarations(t *testing.T) {
	for _, r := range []herr.Reason{"egress/not-permitted", "session/lease-lost", "a/b", "x2/y-3"} {
		if !r.Valid() {
			t.Errorf("%q is refused", r)
		}
	}
	for _, r := range []herr.Reason{"", "noslash", "/x", "x/", "a/b/c", "A/b", "a b/c", "-a/b", "a/-b", "1a/b", "a/b c"} {
		if r.Valid() {
			t.Errorf("%q is accepted", r)
		}
	}
	if herr.Reason("egress/not-permitted").Domain() != "egress" || herr.Reason("bad").Domain() != "" {
		t.Fatal("Domain")
	}
}

// A herr error satisfies the one-method interface the src/sdk codec looks for.
var _ interface{ Reason() string } = (*herr.Error)(nil)
