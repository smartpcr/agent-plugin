package secret

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Source supplies secret values. A source that has no value for a reference
// returns ok false and no error; an error never carries a value.
type Source interface {
	// Origin names the source in descriptions and errors: "env", "file".
	Origin() string
	// Lookup reads the value of r now; it caches nothing.
	Lookup(ctx context.Context, r Ref) (v Value, ok bool, err error)
}

// EnvSource reads Ref.EnvName from lookup, which boot supplies from the launch
// environment snapshot (never from a later os.Getenv).
func EnvSource(lookup func(name string) (string, bool)) Source { return envSource{lookup} }

type envSource struct{ lookup func(string) (string, bool) }

func (envSource) Origin() string { return "env" }

func (s envSource) Lookup(_ context.Context, r Ref) (Value, bool, error) {
	if s.lookup == nil {
		return Value{}, false, nil
	}
	v, ok := s.lookup(r.EnvName())
	if !ok {
		return Value{}, false, nil
	}
	return NewValue(v), true, nil
}

// FileSource reads secret://a/b from the file <dir>/a/b, where dir is
// home.Layout.Secrets. The read is rooted at dir: no link, at any depth, may lead
// outside it, and the final entry must be a regular file, not a link. The opened
// file must be private to its owner (the home's rule); one trailing line ending is
// dropped.
func FileSource(dir string) Source { return fileSource{dir} }

type fileSource struct{ dir string }

func (fileSource) Origin() string { return "file" }

// maxSecretFile bounds a secret file read.
const maxSecretFile = 64 << 10

func (s fileSource) Lookup(_ context.Context, r Ref) (Value, bool, error) {
	if _, err := os.Lstat(s.dir); errors.Is(err, fs.ErrNotExist) {
		return Value{}, false, nil
	}
	if err := home.CheckPrivate(s.dir); err != nil {
		return Value{}, false, herr.Wrap(ReasonSource, err, r.String()+": the secrets directory is refused")
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return Value{}, false, herr.Wrap(ReasonSource, pathless(err), r.String()+": the secrets directory cannot be opened")
	}
	defer root.Close()

	rel := filepath.Join(r.Segments()...)
	info, err := root.Lstat(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return Value{}, false, nil
	}
	if err != nil {
		return Value{}, false, herr.Wrap(ReasonSource, pathless(err), r.String()+": the secret file is refused")
	}
	if !info.Mode().IsRegular() {
		return Value{}, false, herr.Newf(ReasonSource, "%s: the secret file is refused: it is not a regular file", r)
	}
	f, err := root.Open(rel)
	if err != nil {
		return Value{}, false, herr.Wrap(ReasonSource, pathless(err), r.String()+": the secret file cannot be read")
	}
	defer f.Close()
	if err := home.CheckPrivateFile(f); err != nil {
		return Value{}, false, herr.Wrap(ReasonSource, err, r.String()+": the secret file is refused")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSecretFile+1))
	if err != nil {
		return Value{}, false, herr.Wrap(ReasonSource, pathless(err), r.String()+": the secret file cannot be read")
	}
	if len(data) > maxSecretFile {
		return Value{}, false, herr.Newf(ReasonSource, "%s: the secret file is larger than %d bytes", r, maxSecretFile)
	}
	v := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	return NewValue(v), true, nil
}

// pathless drops the path of a *fs.PathError; the caller names the reference.
func pathless(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// Store resolves references through its sources in order: the first source with a
// value wins. It caches no value; it remembers released values only to mask them.
type Store struct {
	sources []Source

	mu       sync.Mutex
	named    []Ref
	released []string
}

// NewStore returns a store over sources, highest precedence first.
func NewStore(sources ...Source) *Store {
	return &Store{sources: slices.Clone(sources)}
}

// Name records references that configuration names, so References and IsSecretName
// know them before any is resolved.
func (s *Store) Name(refs ...Ref) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range refs {
		if !r.IsZero() && !slices.Contains(s.named, r) {
			s.named = append(s.named, r)
		}
	}
}

// References returns the references configuration has named, in naming order.
func (s *Store) References() []Ref {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.named)
}

// Resolve reads the value of r now, from the first source that has it. Each call
// reads again; a caller resolves per use and drops the value after it.
func (s *Store) Resolve(ctx context.Context, r Ref) (Value, string, error) {
	if r.IsZero() {
		return Value{}, "", herr.New(ReasonBadRef, "no secret reference given")
	}
	s.Name(r)
	for _, src := range s.sources {
		v, ok, err := src.Lookup(ctx, r)
		if err != nil {
			return Value{}, "", err
		}
		if !ok {
			continue
		}
		if v.IsZero() {
			return Value{}, "", herr.Newf(ReasonEmpty, "%s: the %s source holds an empty value", r, src.Origin())
		}
		s.release(v.Reveal())
		return v, src.Origin(), nil
	}
	return Value{}, "", herr.Newf(ReasonNotConfigured,
		"%s is not configured: set %s or write %s/%s under the home",
		r, r.EnvName(), home.SecretsDir, strings.Join(r.Segments(), "/"))
}

// Use resolves r and calls fn once with the value. The error fn returns is masked:
// its text, and the text of everything it wraps, never shows a released value.
// errors.Is still matches what fn's error matched.
func (s *Store) Use(ctx context.Context, r Ref, fn func(Value) error) error {
	v, _, err := s.Resolve(ctx, r)
	if err != nil {
		return err
	}
	if err := fn(v); err != nil {
		return newUseError(r, s.MaskString(err.Error()), err)
	}
	return nil
}

func (s *Store) release(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(s.released, v) {
		s.released = append(s.released, v)
		// Longest first, so a value that contains another is masked whole.
		slices.SortFunc(s.released, func(a, b string) int { return len(b) - len(a) })
	}
}

// Mask replaces every value this store has released, and its quoted form, with
// Redacted.
func (s *Store) Mask(p []byte) []byte { return []byte(s.MaskString(string(p))) }

// MaskString is Mask for a string.
func (s *Store) MaskString(text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.released {
		text = strings.ReplaceAll(text, v, Redacted)
		if q := strconv.Quote(v); q[1:len(q)-1] != v {
			text = strings.ReplaceAll(text, q[1:len(q)-1], Redacted)
		}
	}
	return text
}

// IsSecretName reports whether an environment variable holds a secret: the
// variable of a reference configuration named, or a name shaped like a credential
// (…_API_KEY, …_TOKEN, …_SECRET, …_PASSWORD). It is the predicate env.NAME in
// expressions refuses (DH-ADR-BOOT-5) and the names a child environment drops.
func (s *Store) IsSecretName(name string) bool {
	u := strings.ToUpper(name)
	if s != nil {
		s.mu.Lock()
		named := slices.ContainsFunc(s.named, func(r Ref) bool { return r.EnvName() == u })
		s.mu.Unlock()
		if named {
			return true
		}
	}
	return SecretShaped(u)
}

// secretSuffixes mark credential-shaped variable names.
var secretSuffixes = []string{"_API_KEY", "_APIKEY", "_TOKEN", "_SECRET", "_PASSWORD", "_PASSWD", "_CREDENTIALS", "_PRIVATE_KEY"}

// SecretShaped reports a variable name shaped like a credential.
func SecretShaped(name string) bool {
	u := strings.ToUpper(name)
	for _, suf := range secretSuffixes {
		if u == suf[1:] || strings.HasSuffix(u, suf) {
			return true
		}
	}
	return false
}

// newUseError masks cause. The result holds no field that can print the unmasked
// error: only the masked text, the reason, and a closure for errors.Is, which fmt
// prints as an address at most.
func newUseError(r Ref, msg string, cause error) *useError {
	reason := string(ReasonUse)
	if rr, ok := herr.ReasonOf(cause); ok {
		reason = string(rr)
	}
	return &useError{ref: r, msg: msg, reason: reason, is: func(target error) bool { return errors.Is(cause, target) }}
}

// useError is the masked error of a failed use. It does not unwrap, so no caller
// can reach the unmasked text; Is delegates to the original chain.
type useError struct {
	ref    Ref
	msg    string
	reason string
	is     func(error) bool
}

func (e *useError) Error() string { return e.ref.String() + ": " + e.msg }

func (e *useError) Is(target error) bool { return e.is != nil && e.is(target) }

func (e *useError) Reason() string { return e.reason }

// Format makes every verb print Error(), so %#v and %+v cannot dump fields.
func (e *useError) Format(f fmt.State, verb rune) {
	if verb == 'q' {
		fmt.Fprintf(f, "%q", e.Error())
		return
	}
	io.WriteString(f, e.Error())
}

// GoString implements fmt.GoStringer.
func (e *useError) GoString() string { return e.Error() }

// LogValue implements slog.LogValuer.
func (e *useError) LogValue() slog.Value { return slog.StringValue(e.Error()) }
