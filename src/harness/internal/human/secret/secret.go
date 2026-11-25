// Package secret is the secret-reference store of the harness (DH-D-HUMAN-7,
// SP-11): configuration names a secret (secret://deepseek/api-key) and never holds
// it; the value is resolved per use, in the order the store's sources are given:
// the managed store (package secret/file, the one writable source), the launch
// environment snapshot, the project .env and the home .env (DotEnvSource), or an
// owner-only file under <home>/secrets/ (FileSource). It never enters
// configuration, a log line, an error text or the process environment. The
// operating-system keychain is DH-S-35's.
//
// A Value prints as "[redacted]" through every fmt verb, JSON, text and slog;
// Reveal is the only way out, and a source check (internal/archtest) lists the
// packages allowed to call it. Store.Use hands a value to one operation and masks
// it out of the error that operation returns; Store.Mask masks every value this
// process has released. Store.Acquire returns a Lease whose Revoked channel closes
// when Set or Unset changes the reference (or Refresh sees another process change
// it), so a holder never keeps using a rotated key.
package secret

import (
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Scheme prefixes every reference.
const Scheme = "secret://"

// Redacted is what every printed form of a Value is.
const Redacted = herr.Redacted

// Reasons of the errors this package returns.
const (
	ReasonBadRef        herr.Reason = "secret/bad-reference"
	ReasonNotConfigured herr.Reason = "secret/not-configured"
	ReasonEmpty         herr.Reason = "secret/empty"
	ReasonSource        herr.Reason = "secret/source"
	ReasonUse           herr.Reason = "secret/use-failed"
)

// Ref names one secret value: secret://<segment>/<segment>..., each segment
// lower-case letters and digits with single hyphens between words. The zero Ref
// names nothing.
type Ref struct{ path string }

// Parse reads a reference. The error never quotes s, which may be a value pasted
// where a reference belongs.
func Parse(s string) (Ref, error) {
	if !strings.HasPrefix(s, Scheme) {
		return Ref{}, herr.New(ReasonBadRef, "a secret is named by a reference such as secret://deepseek/api-key, never by its value")
	}
	path := strings.TrimPrefix(s, Scheme)
	segments := strings.Split(path, "/")
	if len(segments) < 2 {
		return Ref{}, herr.New(ReasonBadRef, "a secret reference has at least two segments: secret://<owner>/<name>")
	}
	for _, seg := range segments {
		if !validSegment(seg) {
			return Ref{}, herr.New(ReasonBadRef, "a secret reference segment is lower-case letters and digits with single hyphens")
		}
	}
	return Ref{path: path}, nil
}

// MustParse is Parse for a literal; it panics on a malformed reference.
func MustParse(s string) Ref {
	r, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return r
}

func validSegment(s string) bool {
	if s == "" || s[0] == '-' || s[len(s)-1] == '-' || strings.Contains(s, "--") {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// IsZero reports the zero Ref.
func (r Ref) IsZero() bool { return r.path == "" }

// String is the reference as configuration writes it.
func (r Ref) String() string {
	if r.path == "" {
		return ""
	}
	return Scheme + r.path
}

// Segments returns the path segments: ["deepseek", "api-key"].
func (r Ref) Segments() []string {
	if r.path == "" {
		return nil
	}
	return strings.Split(r.path, "/")
}

// EnvName is the environment variable that supplies the reference:
// secret://deepseek/api-key is DEEPSEEK_API_KEY.
func (r Ref) EnvName() string {
	return strings.ToUpper(strings.NewReplacer("/", "_", "-", "_").Replace(r.path))
}

// MarshalText writes the reference, so a configuration dump shows the name only.
func (r Ref) MarshalText() ([]byte, error) { return []byte(r.String()), nil }

// UnmarshalText reads a reference from configuration and refuses anything else,
// so a value can never be configured in place of its name.
func (r *Ref) UnmarshalText(b []byte) error {
	parsed, err := Parse(string(b))
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}

// Value is a secret value. Every printed, marshalled or logged form is
// "[redacted]"; Reveal is the only way out.
type Value struct{ s string }

// NewValue wraps a value a source read.
func NewValue(s string) Value { return Value{s: s} }

// Reveal returns the value. Call it at the point of use only.
func (v Value) Reveal() string { return v.s }

// IsZero reports an empty value.
func (v Value) IsZero() bool { return v.s == "" }

// String implements fmt.Stringer.
func (Value) String() string { return Redacted }

// GoString implements fmt.GoStringer.
func (Value) GoString() string { return Redacted }

// Format prints Redacted for every verb.
func (Value) Format(f fmt.State, _ rune) { io.WriteString(f, Redacted) }

// MarshalText implements encoding.TextMarshaler.
func (Value) MarshalText() ([]byte, error) { return []byte(Redacted), nil }

// MarshalJSON implements json.Marshaler.
func (Value) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }

// LogValue implements slog.LogValuer.
func (Value) LogValue() slog.Value { return slog.StringValue(Redacted) }
