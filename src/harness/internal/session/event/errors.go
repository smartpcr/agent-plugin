package event

import (
	"errors"
	"fmt"
)

// Code is a "<domain>/<reason>" error code.
type Code string

// The codes this package raises. session/unknown-type and session/newer-payload are
// the store's open refusals (session-store.md §6.2), judged against this registry.
const (
	CodeUnknownType        Code = "session/unknown-type"
	CodeNewerPayload       Code = "session/newer-payload"
	CodeInvalidEvent       Code = "session/invalid-event"
	CodeInvalidSurface     Code = "session/invalid-surface"
	CodeReservedType       Code = "session/reserved-type"
	CodeInvalidDeclaration Code = "session/invalid-declaration"
)

// Error is an error carrying a Code. errors.Is matches any *Error of the same code
// against a sentinel below.
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string {
	if e.Msg == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Msg
}

// Is matches a sentinel of the same code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Msg == "" && t.Code == e.Code
}

// Sentinels for errors.Is.
var (
	ErrUnknownType        = &Error{Code: CodeUnknownType}
	ErrNewerPayload       = &Error{Code: CodeNewerPayload}
	ErrInvalidEvent       = &Error{Code: CodeInvalidEvent}
	ErrInvalidSurface     = &Error{Code: CodeInvalidSurface}
	ErrReservedType       = &Error{Code: CodeReservedType}
	ErrInvalidDeclaration = &Error{Code: CodeInvalidDeclaration}
)

// ErrSealed is wrapped by the declaration error of a type declared after Seal.
var ErrSealed = errors.New("the registry is sealed")

// ErrDuplicate is wrapped by the declaration error of a name declared twice.
var ErrDuplicate = errors.New("the type is already declared")

func errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// UnknownTypeError refuses a record whose type this build does not declare and
// that is not ignorable. It names the type and the first position holding it.
type UnknownTypeError struct {
	Type string
	Seq  Seq
}

func (e *UnknownTypeError) Error() string {
	return fmt.Sprintf("%s: type %q at seq %d is not declared by this build and is not ignorable", CodeUnknownType, e.Type, e.Seq)
}

// Code is session/unknown-type.
func (e *UnknownTypeError) Code() Code { return CodeUnknownType }

// Is matches ErrUnknownType.
func (e *UnknownTypeError) Is(target error) bool { return target == ErrUnknownType }

// NewerPayloadError refuses a record written with a payload version newer than the
// one this build declares: the direction is "upgrade the reader", and no value is
// fabricated from it.
type NewerPayloadError struct {
	Type    string
	Version int // the record's version
	Known   int // the newest version this build reads
	Seq     Seq
}

func (e *NewerPayloadError) Error() string {
	return fmt.Sprintf("%s: type %q at seq %d has payload version %d; this build reads up to version %d (a newer build wrote it)",
		CodeNewerPayload, e.Type, e.Seq, e.Version, e.Known)
}

// Code is session/newer-payload.
func (e *NewerPayloadError) Code() Code { return CodeNewerPayload }

// Is matches ErrNewerPayload.
func (e *NewerPayloadError) Is(target error) bool { return target == ErrNewerPayload }
