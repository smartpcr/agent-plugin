package sandbox_test

import (
	"context"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// The zero Launch and a Trusted plan without a reason are no plan; a Trusted plan
// with a reason is valid, trusted and reports its reason.
func TestTrustedNeedsAReason(t *testing.T) {
	for name, l := range map[string]sandbox.Launch{"zero": {}, "empty reason": sandbox.Trusted("  "), "empty untrusted reason": sandbox.Untrusted("")} {
		if l.Valid() || l.IsTrusted() || l.Reason() != "" || l.String() != "no launch plan" {
			t.Errorf("%s: Valid=%v IsTrusted=%v Reason=%q String=%q; want no plan", name, l.Valid(), l.IsTrusted(), l.Reason(), l.String())
		}
	}
	l := sandbox.Trusted(" the ssh client ")
	if !l.Valid() || !l.IsTrusted() || l.Reason() != "the ssh client" || l.String() != "trusted (the ssh client)" {
		t.Fatalf("Trusted: Valid=%v IsTrusted=%v Reason=%q String=%q", l.Valid(), l.IsTrusted(), l.Reason(), l.String())
	}
}

// An Untrusted plan is valid, not trusted, and says why it is unconfined.
func TestUntrustedIsValidAndNotTrusted(t *testing.T) {
	l := sandbox.Untrusted(" git in a workspace ")
	if !l.Valid() || l.IsTrusted() || l.Reason() != "git in a workspace" || l.String() != "unconfined, untrusted (git in a workspace)" {
		t.Fatalf("Untrusted: Valid=%v IsTrusted=%v Reason=%q String=%q", l.Valid(), l.IsTrusted(), l.Reason(), l.String())
	}
}

// Helper is the explicit unconfined plan of a signed-in client route: valid, not
// trusted, carrying its policy and reporting unconfined, backend none; it is
// refused for a policy no resolver issued, an unnamed kind or a confining mode.
func TestHelperIsAnUnconfinedPlanFromAHelperPolicy(t *testing.T) {
	p := sandboxtest.Policy(sandbox.DangerFullAccess)
	l, err := sandbox.Helper(p, " claude-code ")
	if err != nil {
		t.Fatal(err)
	}
	if !l.Valid() || l.IsTrusted() || l.Stage() != nil || l.Policy().Mode() != sandbox.DangerFullAccess {
		t.Fatalf("Helper: Valid=%v IsTrusted=%v Stage=%v Mode=%s", l.Valid(), l.IsTrusted(), l.Stage(), l.Policy().Mode())
	}
	if l.Reason() != "helper claude-code, unconfined by its policy" {
		t.Fatalf("Reason = %q", l.Reason())
	}
	if c := l.Confinement(); c.State != sandbox.StateUnconfined || c.Backend != sandbox.BackendNone || !c.Valid() {
		t.Fatalf("Confinement = %+v", c)
	}
	if _, err := sandbox.Helper(sandbox.Policy{}, "claude-code"); err == nil {
		t.Fatal("Helper accepted a zero policy")
	}
	if _, err := sandbox.Helper(p, " "); herr.Kind(err) != sandbox.CodeUnavailable {
		t.Fatalf("Helper with no kind = %v, want %s", err, sandbox.CodeUnavailable)
	}
	if _, err := sandbox.Helper(sandboxtest.Policy(sandbox.WorkspaceWrite, t.TempDir()), "claude-code"); herr.Kind(err) != sandbox.CodeUnavailable {
		t.Fatalf("Helper with a confining policy = %v, want %s", err, sandbox.CodeUnavailable)
	}
}

type nopStage struct{}

func (nopStage) Wrap([]string, []uintptr, sandbox.Pass) (sandbox.Wrapped, error) {
	return sandbox.Wrapped{}, nil
}
func (nopStage) Confirm(context.Context) (sandbox.Confinement, error) {
	return sandbox.Confinement{}, nil
}
func (nopStage) Release(context.Context) error { return nil }

// Staged needs a stage, a reason and an issued policy; its confinement is
// unknown until the stage confirms one.
func TestStagedNeedsAStageAReasonAndAPolicy(t *testing.T) {
	p := sandboxtest.Policy(sandbox.DangerFullAccess)
	for name, l := range map[string]sandbox.Launch{
		"nil stage":   sandbox.Staged(p, nil, "x"),
		"no reason":   sandbox.Staged(p, nopStage{}, " "),
		"zero policy": sandbox.Staged(sandbox.Policy{}, nopStage{}, "x"),
	} {
		if l.Valid() {
			t.Errorf("%s: Staged is valid", name)
		}
	}
	l := sandbox.Staged(p, nopStage{}, "bwrap")
	if !l.Valid() || l.IsTrusted() || l.Stage() == nil || l.Confinement().Valid() {
		t.Fatalf("Staged: Valid=%v IsTrusted=%v Confinement=%+v", l.Valid(), l.IsTrusted(), l.Confinement())
	}
}

// A Confinement is valid when its state and backend agree.
func TestConfinementValidity(t *testing.T) {
	for _, c := range []struct {
		c    sandbox.Confinement
		want bool
	}{
		{sandbox.Confinement{State: sandbox.StateUnconfined, Backend: sandbox.BackendNone}, true},
		{sandbox.Confinement{State: sandbox.StateFull, Backend: sandbox.BackendBwrap}, true},
		{sandbox.Confinement{State: sandbox.StatePartial, Backend: sandbox.BackendBwrap}, true},
		{sandbox.Confinement{State: sandbox.StateFull, Backend: sandbox.BackendNone}, false},
		{sandbox.Confinement{State: sandbox.StateUnconfined, Backend: sandbox.BackendBwrap}, false},
		{sandbox.Confinement{}, false},
	} {
		if got := c.c.Valid(); got != c.want {
			t.Errorf("%+v.Valid() = %v, want %v", c.c, got, c.want)
		}
	}
}
