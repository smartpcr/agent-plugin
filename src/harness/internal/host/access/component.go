package access

import (
	"context"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// Name is the catalogue name of Component.
const Name = "host.access"

// Key is the access service.
var Key = cordis.NewServiceKey[Service]("host.access")

// Resource is what an action acts on. Kind "global" has no ID.
type Resource struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

// Decision is the answer of Decide: allowed, or denied with a reason.
type Decision struct {
	Allowed bool
	Reason  string
}

// ReasonNoPolicy denies a principal whose kind no policy covers.
const ReasonNoPolicy = "access/no-policy"

// Service decides what a principal may do (api.md §6.2). Decide and Allows are the
// one decision function: Allows is it for rendering and filters.
type Service interface {
	Decide(ctx context.Context, p Principal, action string, rs []Resource) Decision
	Allows(ctx context.Context, p Principal, action string, r Resource) bool
}

// ownerOnly is the service with no registered policy (DH-D-API-3): the owner path
// is a constant and every other principal is denied, so access fails closed until
// a policy is registered.
type ownerOnly struct{}

func (ownerOnly) Decide(_ context.Context, p Principal, _ string, _ []Resource) Decision {
	if p.IsOwner() {
		return Decision{Allowed: true}
	}
	return Decision{Reason: ReasonNoPolicy}
}

func (o ownerOnly) Allows(ctx context.Context, p Principal, action string, r Resource) bool {
	return o.Decide(ctx, p, action, []Resource{r}).Allowed
}

// Component is host.access: it provides the owner-only service under Key.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	_, err := Key.Provide(ctx, Service(ownerOnly{}))
	return err
})
