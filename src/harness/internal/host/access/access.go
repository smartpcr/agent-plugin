// Package access is area A19's contract of who may do what (docs/harness/areas/api.md
// §6.1). It holds the principal, which host.auth puts on every admitted peer
// (DH-D-HOST-2), and host.access, the decision function with no registered policy:
// the owner is allowed and every other principal denied (DH-D-API-3). Rules, kinds
// and policies arrive with the routes and the first policy that need them.
package access

// Principal is who a request acts as. Kind KindOwner is built in and has no ID.
type Principal struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

// KindOwner is the person who started the host.
const KindOwner = "owner"

// Owner is the owner principal.
func Owner() Principal { return Principal{Kind: KindOwner} }

// IsOwner reports whether p is the owner.
func (p Principal) IsOwner() bool { return p.Kind == KindOwner }
