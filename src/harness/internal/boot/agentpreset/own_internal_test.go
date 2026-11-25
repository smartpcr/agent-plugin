package agentpreset

import (
	"errors"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
)

// emptyCatalogue holds no component: a declaration without children needs none.
type emptyCatalogue struct{}

func (emptyCatalogue) Lookup(name string) (catalog.Item, error) {
	return catalog.Item{}, errors.New("no component " + name)
}

func (emptyCatalogue) Resolve(string) (*cordis.Component, bool) { return nil, false }

// newEmptyRegistry is a registry with the one childless declaration "empty".
func newEmptyRegistry(t *testing.T) (*Registry, *cordis.Context) {
	t.Helper()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	r, err := New(root, Options{Catalogue: emptyCatalogue{}, Scopes: scope.NewTable(root), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Load(Config{Default: "empty"}, []Declaration{{ID: "empty"}}); err != nil {
		t.Fatal(err)
	}
	return r, root
}

// foreignLease is a Lease no registry handed out; it counts its releases.
type foreignLease struct{ released int }

func (*foreignLease) Key() scope.Key { return scope.Key{} }
func (*foreignLease) Info() Info     { return Info{ID: "empty"} }
func (l *foreignLease) Release()     { l.released++ }

// leases is the lease count over r's live revisions.
func leases(r *Registry) (n int) {
	for _, rs := range r.Revisions() {
		n += rs.Leases
	}
	return n
}

// own refuses a lease this registry did not hand out: it is given back (released
// once), so no count is leaked, and an error names its type. The registry's own
// lease passes through unchanged.
func TestOwnRefusesALeaseNotOfThisRegistry(t *testing.T) {
	r, root := newEmptyRegistry(t)
	other, otherRoot := newEmptyRegistry(t)

	fl := &foreignLease{}
	if got, err := r.own(fl); err == nil || got != nil || !strings.Contains(err.Error(), "*agentpreset.foreignLease is not a lease of this registry") {
		t.Fatalf("own(foreign) = %v, %v; want a refusal naming its type", got, err)
	}
	if fl.released != 1 {
		t.Fatalf("a refused foreign lease was released %d times, want once", fl.released)
	}

	l, err := other.Acquire(otherRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	if n := leases(other); n != 1 {
		t.Fatalf("the other registry counts %d leases, want 1", n)
	}
	if got, err := r.own(l); err == nil || got != nil || !strings.Contains(err.Error(), "is not a lease of this registry") {
		t.Fatalf("own(another registry's lease) = %v, %v; want a refusal", got, err)
	}
	if n := leases(other); n != 0 {
		t.Fatalf("the refused lease left %d counted on its registry, want 0", n)
	}
	if _, ok := r.Of(scope.Key{}); ok || len(r.bindings) != 0 {
		t.Fatalf("a refused lease was published as a binding: %v", r.bindings)
	}

	if got, err := r.own(nil); err == nil || got != nil {
		t.Fatalf("own(nil) = %v, %v; want a refusal", got, err)
	}

	mine, err := r.Acquire(root, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.own(mine)
	if err != nil || got != mine {
		t.Fatalf("own(own lease) = %v, %v; want the lease itself", got, err)
	}
	if n := leases(r); n != 1 {
		t.Fatalf("an accepted lease changed the count to %d, want 1", n)
	}
	mine.Release()
}
