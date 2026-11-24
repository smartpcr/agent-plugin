package sandbox_test

import (
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// The zero Launch and a Trusted plan without a reason are no plan; a Trusted plan
// with a reason is valid, trusted and reports its reason.
func TestTrustedNeedsAReason(t *testing.T) {
	for name, l := range map[string]sandbox.Launch{"zero": {}, "empty reason": sandbox.Trusted("  ")} {
		if l.Valid() || l.IsTrusted() || l.Reason() != "" || l.String() != "no launch plan" {
			t.Errorf("%s: Valid=%v IsTrusted=%v Reason=%q String=%q; want no plan", name, l.Valid(), l.IsTrusted(), l.Reason(), l.String())
		}
	}
	l := sandbox.Trusted(" the ssh client ")
	if !l.Valid() || !l.IsTrusted() || l.Reason() != "the ssh client" || l.String() != "trusted (the ssh client)" {
		t.Fatalf("Trusted: Valid=%v IsTrusted=%v Reason=%q String=%q", l.Valid(), l.IsTrusted(), l.Reason(), l.String())
	}
}
