package secret

import (
	"context"
	"log/slog"
	"slices"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Lease is a value held for a span of use, such as one model request or a child
// started with the value as an explicit entry. Its holder watches Revoked: when
// the reference is rotated or unset, the channel closes and the holder ends the
// use and acquires again. The store owns the value; the holder releases the lease.
type Lease struct {
	store  *Store
	ref    Ref
	value  Value
	origin string

	mu      sync.Mutex
	revoked chan struct{}
	cause   string
	done    bool
}

// Acquire resolves r now and returns a lease on the value. The value is released
// for masking as Resolve releases it.
func (s *Store) Acquire(ctx context.Context, r Ref) (*Lease, error) {
	v, origin, err := s.Resolve(ctx, r)
	if err != nil {
		return nil, err
	}
	l := &Lease{store: s, ref: r, value: v, origin: origin, revoked: make(chan struct{})}
	s.mu.Lock()
	if s.leases == nil {
		s.leases = map[Ref][]*Lease{}
	}
	s.leases[r] = append(s.leases[r], l)
	s.mu.Unlock()
	return l, nil
}

// Ref names the leased reference.
func (l *Lease) Ref() Ref { return l.ref }

// Value is the leased value.
func (l *Lease) Value() Value { return l.value }

// Reveal returns the leased value at its point of use.
func (l *Lease) Reveal() string { return l.value.Reveal() }

// Source is the origin that supplied the value: "store", "env", "project-env",
// "home-env".
func (l *Lease) Source() string { return l.origin }

// Revoked closes when the reference is rotated or unset, or the lease is
// released.
func (l *Lease) Revoked() <-chan struct{} { return l.revoked }

// Err is nil while the lease holds, and a ReasonRevoked error naming the cause
// ("rotated", "unset", "released") after.
func (l *Lease) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cause == "" {
		return nil
	}
	return herr.Newf(ReasonRevoked, "%s: the leased value was %s", l.ref, l.cause)
}

// Release ends the lease; a second Release is a no-op.
func (l *Lease) Release() {
	l.end("released")
	s := l.store
	s.mu.Lock()
	defer s.mu.Unlock()
	held := slices.DeleteFunc(s.leases[l.ref], func(x *Lease) bool { return x == l })
	if len(held) == 0 {
		delete(s.leases, l.ref)
	} else {
		s.leases[l.ref] = held
	}
}

// end closes the signal once, recording why.
func (l *Lease) end(cause string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		return false
	}
	l.done, l.cause = true, cause
	close(l.revoked)
	return true
}

// String names the lease, never its value.
func (l *Lease) String() string { return "lease " + l.ref.String() + " from " + l.origin }

// LogValue implements slog.LogValuer.
func (l *Lease) LogValue() slog.Value { return slog.StringValue(l.String()) }

// revoke signals every lease of r and forgets them.
func (s *Store) revoke(r Ref, cause string) {
	s.mu.Lock()
	held := s.leases[r]
	delete(s.leases, r)
	s.mu.Unlock()
	for _, l := range held {
		l.end(cause)
	}
}

// Refresh looks up every leased reference again and revokes the leases whose
// value or origin has changed or gone: a write made by another process, seen when
// the store's file changes. It returns how many leases it revoked.
func (s *Store) Refresh(ctx context.Context) (int, error) {
	s.mu.Lock()
	refs := make([]Ref, 0, len(s.leases))
	for r := range s.leases {
		refs = append(refs, r)
	}
	s.mu.Unlock()
	revoked := 0
	for _, r := range refs {
		v, origin, found, err := s.lookup(ctx, r)
		if err != nil {
			return revoked, err
		}
		s.mu.Lock()
		var stale, kept []*Lease
		for _, l := range s.leases[r] {
			if !found || origin != l.origin || v.Reveal() != l.value.Reveal() {
				stale = append(stale, l)
			} else {
				kept = append(kept, l)
			}
		}
		if len(kept) == 0 {
			delete(s.leases, r)
		} else {
			s.leases[r] = kept
		}
		s.mu.Unlock()
		cause := "rotated"
		if !found {
			cause = "unset"
		}
		for _, l := range stale {
			if l.end(cause) {
				revoked++
			}
		}
	}
	return revoked, nil
}

// lookup is Resolve without naming or releasing: the first source's value.
func (s *Store) lookup(ctx context.Context, r Ref) (Value, string, bool, error) {
	for _, src := range s.sources {
		v, ok, err := src.Lookup(ctx, r)
		if err != nil {
			return Value{}, "", false, err
		}
		if ok {
			return v, src.Origin(), true, nil
		}
	}
	return Value{}, "", false, nil
}
