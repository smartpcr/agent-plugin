// Package herr is the in-process value behind the harness's one error table
// (DH-D-UTIL-5). Every failure carries a Reason of the form "<domain>/<reason>",
// declared as a constant by the package that owns the failure; the same code then
// appears in a tool result, a log line and a wire error. The src/sdk codec does not
// import this package: it reads the code from any error in the chain that has a
// Reason() string method, which *Error has.
//
// herr imports the standard library only (RM-6), so every package can return it.
//
// A value that must never be printed is wrapped in Secret before it reaches a
// message: every formatting path (fmt verbs, JSON, text, slog) prints a fixed
// placeholder, so an error built around a secret cannot leak it however deeply it
// is wrapped.
package herr

import (
	"fmt"
	"io"
	"log/slog"
)

// Reason is a "<domain>/<reason>" error code. Both parts are lower-case ASCII
// letters, digits and hyphens, and start with a letter.
type Reason string

// Domain returns the part before the slash, or "" when r is not well formed.
func (r Reason) Domain() string {
	if !r.Valid() {
		return ""
	}
	for i := 0; i < len(r); i++ {
		if r[i] == '/' {
			return string(r[:i])
		}
	}
	return ""
}

// Valid reports whether r has the form "<domain>/<reason>". A package that
// declares reasons proves them with a declaration test that calls Valid.
func (r Reason) Valid() bool {
	slash := -1
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '/':
			if slash >= 0 {
				return false
			}
			slash = i
		case c >= 'a' && c <= 'z':
		case (c >= '0' && c <= '9') || c == '-':
			if i == 0 || i == slash+1 {
				return false
			}
		default:
			return false
		}
	}
	return slash > 0 && slash < len(r)-1
}

// Error is the harness error: a reason, a message for people, and an optional
// cause reachable through errors.Unwrap.
type Error struct {
	reason Reason
	msg    string
	cause  error
}

// New returns an error with reason r and message msg.
func New(r Reason, msg string) *Error { return &Error{reason: r, msg: msg} }

// Newf returns an error with reason r and a formatted message. Wrap a value that
// must not be printed in Secret before passing it.
func Newf(r Reason, format string, args ...any) *Error {
	return &Error{reason: r, msg: fmt.Sprintf(format, args...)}
}

// Wrap returns an error with reason r and message msg around cause. A nil cause
// gives nil, so `return herr.Wrap(r, err, msg)` is safe after any call.
func Wrap(r Reason, cause error, msg string) error {
	if cause == nil {
		return nil
	}
	return &Error{reason: r, msg: msg, cause: cause}
}

// Reason returns the error's own code as a string: the one-method interface the
// src/sdk codec looks for. Kind and ReasonOf read the innermost code of a chain.
func (e *Error) Reason() string { return string(e.reason) }

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.cause }

// Error formats "<reason>: <msg>: <cause>", leaving out empty parts.
func (e *Error) Error() string {
	s := string(e.reason)
	if e.msg != "" {
		s += ": " + e.msg
	}
	if e.cause != nil {
		s += ": " + e.cause.Error()
	}
	return s
}

// Format makes every verb print Error(), so %#v cannot dump fields.
func (e *Error) Format(f fmt.State, verb rune) {
	if verb == 'q' {
		fmt.Fprintf(f, "%q", e.Error())
		return
	}
	io.WriteString(f, e.Error())
}

// Is matches a target *Error of the same reason that has no message and no cause,
// so a package can declare sentinels: var ErrX = herr.New(ReasonX, "").
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.msg == "" && t.cause == nil && t.reason == e.reason
}

// reasoner is the interface the sdk codec reads; foreign error types may have it.
type reasoner interface{ Reason() string }

// ReasonOf returns the innermost reason in err's chain, looking through
// fmt.Errorf %w wrapping and errors.Join, and through any error type that has a
// Reason() string method.
func ReasonOf(err error) (Reason, bool) { return innermost(err) }

// Kind returns the innermost reason in err's chain, or "" when it has none.
func Kind(err error) Reason {
	r, _ := innermost(err)
	return r
}

// innermost walks the chain depth first and returns the deepest reason. On a join
// the first branch that holds a reason wins.
func innermost(err error) (Reason, bool) {
	if err == nil {
		return "", false
	}
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		if r, ok := innermost(u.Unwrap()); ok {
			return r, true
		}
	case interface{ Unwrap() []error }:
		for _, c := range u.Unwrap() {
			if r, ok := innermost(c); ok {
				return r, true
			}
		}
	}
	if r, ok := err.(reasoner); ok && r.Reason() != "" {
		return Reason(r.Reason()), true
	}
	return "", false
}

// Has reports whether r appears anywhere in err's chain.
func Has(err error, r Reason) bool {
	if err == nil {
		return false
	}
	if x, ok := err.(reasoner); ok && Reason(x.Reason()) == r {
		return true
	}
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		return Has(u.Unwrap(), r)
	case interface{ Unwrap() []error }:
		for _, c := range u.Unwrap() {
			if Has(c, r) {
				return true
			}
		}
	}
	return false
}

// Redacted is what every formatting path prints for a Secret.
const Redacted = "[redacted]"

// Secret marks a value that must never be printed, logged or encoded. Reveal is
// the only way to read it.
type Secret string

// Reveal returns the value for its one use.
func (s Secret) Reveal() string { return string(s) }

// String implements fmt.Stringer.
func (Secret) String() string { return Redacted }

// GoString implements fmt.GoStringer.
func (Secret) GoString() string { return Redacted }

// Format makes every fmt verb, including %x and %q, print Redacted.
func (Secret) Format(f fmt.State, _ rune) { io.WriteString(f, Redacted) }

// MarshalText implements encoding.TextMarshaler, and so JSON.
func (Secret) MarshalText() ([]byte, error) { return []byte(Redacted), nil }

// LogValue implements slog.LogValuer.
func (Secret) LogValue() slog.Value { return slog.StringValue(Redacted) }
