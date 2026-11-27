package access_test

import (
	"context"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/access"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
)

// With no registered policy host.access allows the owner and denies every other
// principal, by Decide and by Allows alike (DH-D-API-3: it fails closed).
func TestAccessAllowsOnlyTheOwnerWithoutAPolicy(t *testing.T) {
	root := testkit.Tree(t)
	f, err := root.Use(access.Component, cordis.None{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(); err != nil {
		t.Fatal(err)
	}
	svc, err := access.Key.Get(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, global := context.Background(), access.Resource{Kind: "global"}
	if d := svc.Decide(ctx, access.Owner(), "session.read", []access.Resource{global}); !d.Allowed {
		t.Errorf("owner denied: %+v", d)
	}
	for _, p := range []access.Principal{{Kind: "viewer", ID: "v1"}, {}} {
		if d := svc.Decide(ctx, p, "session.read", []access.Resource{global}); d.Allowed || d.Reason != access.ReasonNoPolicy {
			t.Errorf("%+v decided %+v, want denied %s", p, d, access.ReasonNoPolicy)
		}
		if svc.Allows(ctx, p, "session.read", global) {
			t.Errorf("%+v allowed", p)
		}
	}
}
