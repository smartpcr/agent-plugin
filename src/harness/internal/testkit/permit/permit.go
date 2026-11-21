// Package permit is the test kit's egress permit (DH-D-UTIL-6). util/egress takes
// a permit as an interface so that it need not import host/sharing; tests of
// egress and of anything that hands it a permit use this one. A permit's consent
// lasts until Withdraw or the end of the test, whichever comes first.
package permit

import (
	"context"
	"testing"
)

// Permit is a granted permit for one kind and destination.
type Permit struct {
	kind, dest string
	ctx        context.Context
	cancel     context.CancelFunc
}

// New is a permit for kind and dest, withdrawn when the test ends.
func New(t testing.TB, kind, dest string) *Permit {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &Permit{kind: kind, dest: dest, ctx: ctx, cancel: cancel}
}

// Kind is the kind of traffic the permit grants.
func (p *Permit) Kind() string { return p.kind }

// Destination is the destination the permit grants.
func (p *Permit) Destination() string { return p.dest }

// Context ends when consent is withdrawn.
func (p *Permit) Context() context.Context { return p.ctx }

// Withdraw withdraws consent.
func (p *Permit) Withdraw() { p.cancel() }
