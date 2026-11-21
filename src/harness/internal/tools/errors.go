package tools

import (
	"strings"
)

// Code is a "<domain>/<reason>" error code.
type Code string

// The codes this package and its registry raise at registration and step
// resolution. The pipeline's outcome codes (areas/tools.md §6.2) arrive with it.
const (
	// CodeInvalidDeclaration: a declaration that cannot be registered (a bad name,
	// no body, a schema that cannot be derived or leaves the model-facing subset).
	CodeInvalidDeclaration Code = "tools/invalid-declaration"
	// CodeDuplicate: a second tool of one name in one layer.
	CodeDuplicate Code = "tools/duplicate"
	// CodeUnscoped: Restrict through a context that carries no scope.
	CodeUnscoped Code = "tools/unscoped"
	// CodeActivationFailed: a When condition failed, so the step view was not built.
	CodeActivationFailed Code = "tools/activation-failed"
	// CodeInvalidArguments: arguments that do not decode into the input type.
	CodeInvalidArguments Code = "tools/invalid-arguments"
	// CodeInvalidOutput: a body's value that is not of its declared output shape.
	CodeInvalidOutput Code = "tools/invalid-output"
)

// Error carries a Code, the tool it concerns and, for schema failures, every
// offending path; errors.Is matches a sentinel of the same code.
type Error struct {
	Code Code
	Tool string
	Msg  string
	// Issues lists every violation, each prefixed with its path.
	Issues []string
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(string(e.Code))
	if e.Tool != "" {
		b.WriteString(": tool ")
		b.WriteString(e.Tool)
	}
	if e.Msg != "" {
		b.WriteString(": ")
		b.WriteString(e.Msg)
	}
	if len(e.Issues) > 0 {
		b.WriteString(": ")
		b.WriteString(strings.Join(e.Issues, "; "))
	}
	return b.String()
}

// Is matches a sentinel of the same code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Tool == "" && t.Msg == "" && len(t.Issues) == 0 && t.Code == e.Code
}

// Sentinels for errors.Is.
var (
	ErrInvalidDeclaration = &Error{Code: CodeInvalidDeclaration}
	ErrDuplicate          = &Error{Code: CodeDuplicate}
	ErrUnscoped           = &Error{Code: CodeUnscoped}
	ErrActivationFailed   = &Error{Code: CodeActivationFailed}
	ErrInvalidArguments   = &Error{Code: CodeInvalidArguments}
	ErrInvalidOutput      = &Error{Code: CodeInvalidOutput}
)
