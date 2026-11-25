package subprocess

import (
	"context"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// DefaultScopeTimeout bounds the one request for a delegated scope at provider
// start when ProviderOptions.ScopeTimeout is zero.
const DefaultScopeTimeout = 5 * time.Second

// UserManager asks the user service manager for a delegated scope (U-K1-1,
// DH-ADR-EXEC-2). The provider start calls it at most once per process, and only
// on Linux when the host's own control group is not writable; it is never called
// per command. SystemdUserManager is the real one; tests pass a fake.
type UserManager interface {
	// DelegateScope asks for a new transient scope named unit, with delegation,
	// that contains the process pid, and returns once the manager answered or ctx
	// ended. A nil error means the manager accepted; the caller then checks that
	// pid is inside unit.
	DelegateScope(ctx context.Context, unit string, pid int) error
}

// ProviderOptions say how the process provider starts (OpenRegistry).
type ProviderOptions struct {
	// RunDir is the home's runtime directory (home.Layout.Run(), "<home>/run"),
	// which must exist; the record of live owners, owners.json, is kept there.
	// Empty keeps no record and reports no leftovers.
	RunDir string
	// UserManager is asked for the delegated scope; nil is SystemdUserManager.
	// Only Linux asks.
	UserManager UserManager
	// ScopeTimeout bounds the scope request; zero is DefaultScopeTimeout. A
	// manager that has not answered by then counts as absent.
	ScopeTimeout time.Duration
	// ControlSocket, when set, is the host's control-socket path, which Start
	// keeps out of every child environment (SetControlSocket).
	ControlSocket string
	// Announce, when set, is called once per process with the chosen owner when
	// that owner is a fallback (Owner.Fallback is set), so the weaker rung is
	// announced once; every handle reports it as well.
	Announce func(Owner)
}

// OpenRegistry is the provider start (DH-D-EXEC-2, M1-G-BOOT-3). It chooses the
// tree owner of this process once: on Linux, when the host's own control group
// is not writable, it first asks the user service manager, once and within
// ScopeTimeout, for a delegated scope that holds the host, and makes every
// per-command control group inside it; a refusal or no answer leaves the
// process-group rung, announced once. A rung already chosen, because a command
// started before the provider, is kept.
//
// With a RunDir it then reads the owner record a previous host on the same home
// left and returns its leftovers: the owners of a host that is no longer running,
// with the members still alive. They are reported and never stopped, since a
// crashed host's survivors are no longer provably this host's (ownership is not
// access). From then on the registry records each tree it holds in the file, and
// removes it once the tree is gone. The registry is usable even when the error,
// subprocess/tree-owner, says the record could not be read; it then keeps none.
func OpenRegistry(ctx context.Context, o ProviderOptions) (*Registry, []Leftover, error) {
	if o.ScopeTimeout <= 0 {
		o.ScopeTimeout = DefaultScopeTimeout
	}
	if o.ControlSocket != "" {
		SetControlSocket(o.ControlSocket)
	}
	prepareOwner(ctx, o)
	if o.Announce != nil {
		if owner := TreeOwner(); owner.Fallback != "" {
			announceOnce.Do(func() { o.Announce(owner) })
		}
	}
	r := NewRegistry()
	if o.RunDir == "" {
		return r, nil, nil
	}
	rec, left, err := openRecord(ctx, o.RunDir)
	if err != nil {
		return r, nil, herr.Wrap(ReasonTreeOwner, err, "read the owner record in "+o.RunDir)
	}
	r.record = rec
	return r, left, nil
}

var announceOnce sync.Once

// DelegatedScope is the name of the scope unit the user service manager
// delegated to this process at provider start, in which every per-command
// control group is made; "" when none was asked for or granted, and always on
// systems other than Linux.
func DelegatedScope() string { return delegatedScope() }

// OwnerRecordErr is the last failure to write the owner record; nil when the
// record is current or the registry keeps none.
func (r *Registry) OwnerRecordErr() error { return r.record.lastErr() }
