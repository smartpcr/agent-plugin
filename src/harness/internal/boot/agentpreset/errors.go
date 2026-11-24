package agentpreset

import (
	"errors"
	"strings"
)

// Code is an "agentpreset/<reason>" error code (bundle-preset.md §6.2).
type Code string

// The codes this package raises.
const (
	// CodeNotFound: no declaration has the id; the error lists the available ids.
	CodeNotFound Code = "agentpreset/not-found"
	// CodeInvalid: the declarations do not load statically, or the revision of the
	// id is broken; the error carries the diagnostic.
	CodeInvalid Code = "agentpreset/invalid"
	// CodeLocked: the session has started a turn, so its preset is fixed.
	CodeLocked Code = "agentpreset/locked"
	// CodeClosed: the registry has unloaded.
	CodeClosed Code = "agentpreset/closed"
)

// Error carries a Code; errors.Is matches a sentinel of the same code.
type Error struct {
	Code Code
	Msg  string
	// Available is the declared ids, for CodeNotFound.
	Available []string
	// Issues are the static issues, for a CodeInvalid of Load.
	Issues []Issue
}

func (e *Error) Error() string {
	if e.Msg == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Msg
}

// Is matches a sentinel of the same code.
func (e *Error) Is(target error) bool {
	var t *Error
	return errors.As(target, &t) && t.Msg == "" && t.Code == e.Code
}

// Sentinels for errors.Is.
var (
	ErrNotFound = &Error{Code: CodeNotFound}
	ErrInvalid  = &Error{Code: CodeInvalid}
	ErrLocked   = &Error{Code: CodeLocked}
	ErrClosed   = &Error{Code: CodeClosed}
)

func notFound(id string, available []string) *Error {
	msg := "no preset " + quote(id) + "; available: " + strings.Join(available, ", ")
	if len(available) == 0 {
		msg = "no preset " + quote(id) + "; none is declared"
	}
	return &Error{Code: CodeNotFound, Msg: msg, Available: available}
}

func invalid(issues []Issue) *Error {
	lines := make([]string, len(issues))
	for i, is := range issues {
		lines[i] = is.String()
	}
	return &Error{Code: CodeInvalid, Msg: strings.Join(lines, "; "), Issues: issues}
}

func quote(s string) string { return `"` + s + `"` }
