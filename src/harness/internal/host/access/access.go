// Package access is area A19's contract of who may do what (docs/harness/areas/api.md
// §6.1). This stage holds only the principal, which host.auth puts on every admitted
// peer (DH-D-HOST-2); the rules, resources and the decision function arrive with
// DH-D-API-3.
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
