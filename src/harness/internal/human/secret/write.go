package secret

import (
	"context"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Reasons of the managed store, the .env sources and leases.
const (
	ReasonDotEnv   herr.Reason = "secret/bad-env-file"
	ReasonReadOnly herr.Reason = "secret/read-only"
	ReasonStale    herr.Reason = "secret/stale-revision"
	ReasonKeyLost  herr.Reason = "secret/key-lost"
	ReasonCorrupt  herr.Reason = "secret/store-corrupt"
	ReasonBusy     herr.Reason = "secret/store-busy"
	ReasonRevoked  herr.Reason = "secret/revoked"
)

// Revisions a caller passes to Set.
const (
	// AnyRevision writes whatever revision the reference has: a plain
	// `dsh secret set`.
	AnyRevision = ""
	// RevisionAbsent is the revision of a reference the store does not hold; a
	// Set with it creates the entry only if nobody has.
	RevisionAbsent = "0"
)

// Writer is a source that can be written: the managed store (package
// secret/file). Its writes are one-way: nothing reads a value back except Lookup.
type Writer interface {
	Source
	// Set stores v under r if r's current revision is ifRevision (AnyRevision
	// skips the check), and returns the new revision. A stale revision fails with
	// ReasonStale and changes nothing.
	Set(ctx context.Context, r Ref, v Value, ifRevision string) (string, error)
	// Unset removes r; removing an absent reference is not an error.
	Unset(ctx context.Context, r Ref) error
	// Stat describes r's entry without its value.
	Stat(ctx context.Context, r Ref) (Stat, error)
	// Stored lists the references the store holds.
	Stored(ctx context.Context) ([]Ref, error)
}

// Stat is what a Writer says about one entry.
type Stat struct {
	Present    bool
	Revision   string // RevisionAbsent when not present
	Protection string // "file", later "keychain"
	Reason     string // why a present-looking store gives nothing, e.g. "key-lost"
}

// Info describes a reference without its value.
type Info struct {
	Ref        Ref
	Configured bool
	Source     string // the origin that supplies the value now
	Protection string // the managed store's protection, when it is the source or writable
	Preview    string // a masked preview: "****" and at most the last four characters
	Revision   string // the managed store's revision of the entry
	Reason     string // why the managed store gives nothing, when it says
}

// writer is the first writable source, or nil.
func (s *Store) writer() Writer {
	for _, src := range s.sources {
		if w, ok := src.(Writer); ok {
			return w
		}
	}
	return nil
}

// Set stores v under r in the managed store, refusing an empty value and a stale
// revision, and revokes every lease of r: its holders take the new value at their
// next Acquire. The value is never written into the process environment.
func (s *Store) Set(ctx context.Context, r Ref, v Value, ifRevision string) (string, error) {
	if r.IsZero() {
		return "", herr.New(ReasonBadRef, "no secret reference given")
	}
	if v.IsZero() {
		return "", herr.Newf(ReasonEmpty, "%s: an empty value is refused; unset the reference instead", r)
	}
	w := s.writer()
	if w == nil {
		return "", herr.Newf(ReasonReadOnly, "%s: no writable secret store is configured", r)
	}
	rev, err := w.Set(ctx, r, v, ifRevision)
	if err != nil {
		return "", err
	}
	s.Name(r)
	s.revoke(r, "rotated")
	return rev, nil
}

// Unset removes r from the managed store and revokes every lease of r; a lower
// source (the launch environment, a .env file) then supplies it, if one has it.
func (s *Store) Unset(ctx context.Context, r Ref) error {
	if r.IsZero() {
		return herr.New(ReasonBadRef, "no secret reference given")
	}
	w := s.writer()
	if w == nil {
		return herr.Newf(ReasonReadOnly, "%s: no writable secret store is configured", r)
	}
	if err := w.Unset(ctx, r); err != nil {
		return err
	}
	s.revoke(r, "unset")
	return nil
}

// Describe reports whether r is configured, by which source, with a masked
// preview, and the managed store's revision and protection. It releases nothing.
func (s *Store) Describe(ctx context.Context, r Ref) (Info, error) {
	if r.IsZero() {
		return Info{}, herr.New(ReasonBadRef, "no secret reference given")
	}
	info := Info{Ref: r}
	if w := s.writer(); w != nil {
		st, err := w.Stat(ctx, r)
		if err != nil {
			return Info{}, err
		}
		info.Revision, info.Protection, info.Reason = st.Revision, st.Protection, st.Reason
	}
	for _, src := range s.sources {
		v, ok, err := src.Lookup(ctx, r)
		if err != nil {
			return Info{}, err
		}
		if !ok {
			continue
		}
		info.Configured, info.Source, info.Preview = !v.IsZero(), src.Origin(), preview(v.Reveal())
		break
	}
	return info, nil
}

// NameStored names every reference the managed store holds, so References and
// IsSecretName (and through them BuildEnv) know a stored name that configuration
// does not mention.
func (s *Store) NameStored(ctx context.Context) error {
	w := s.writer()
	if w == nil {
		return nil
	}
	refs, err := w.Stored(ctx)
	if err != nil {
		return err
	}
	s.Name(refs...)
	return nil
}

// preview masks v: "****" and, for a value of 16 characters or more, its last
// four.
func preview(v string) string {
	const shown = 4
	r := []rune(v)
	if len(r) < 16 {
		return "****"
	}
	return "****" + string(r[len(r)-shown:])
}
