package cordis

import (
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
)

// ErrInactive is returned when an effect, service or component is created on a
// context whose fiber has been disposed.
var ErrInactive = errors.New("cordis: cannot create effect on inactive context")

// ErrNotInjected is wrapped by a ServiceError for a service the calling fiber did not
// declare (and that no fiber on its walk to the root provides).
var ErrNotInjected = errors.New("cordis: service not injected")

// ErrUnavailable is wrapped by a ServiceError for a declared service that currently
// has no active provider.
var ErrUnavailable = errors.New("cordis: service unavailable")

// ErrRootFiber is returned by Restart and Update on the root fiber, which has no
// component to reload.
var ErrRootFiber = errors.New("cordis: the root fiber has no component to reload")

// ServiceError reports a failed service lookup. Err is ErrNotInjected or
// ErrUnavailable, so callers can test it with errors.Is.
type ServiceError struct {
	Name string
	Err  error
}

func (e *ServiceError) Error() string {
	if errors.Is(e.Err, ErrNotInjected) {
		return fmt.Sprintf("cordis: cannot get service %q without inject", e.Name)
	}
	return fmt.Sprintf("cordis: cannot get required service %q in inactive context", e.Name)
}

func (e *ServiceError) Unwrap() error { return e.Err }

// ConfigIssue is one problem found while resolving a component's config.
type ConfigIssue struct {
	Message string
	// Path names the offending field (dot separated); empty for the config as a whole.
	Path string
}

// ConfigError reports a config rejected by a component's schema (upstream's
// ValidationError). A fiber whose config is rejected fails its load.
type ConfigError struct {
	Issues []ConfigIssue
}

func (e *ConfigError) Error() string {
	var b strings.Builder
	b.WriteString("invalid config:")
	for _, issue := range e.Issues {
		b.WriteString("\n  - ")
		b.WriteString(issue.Message)
		if issue.Path != "" {
			b.WriteString(" (at ")
			b.WriteString(issue.Path)
			b.WriteString(")")
		}
	}
	return b.String()
}

// asConfigError keeps a ConfigError as it is and wraps any other error as one issue.
func asConfigError(err error) error {
	var configError *ConfigError
	if errors.As(err, &configError) {
		return err
	}
	return &ConfigError{Issues: []ConfigIssue{{Message: err.Error()}}}
}

// AggregateError carries several failures, such as the listeners of a parallel event.
// The logger flattens it: each member is logged on its own, and an aggregate with no
// members is logged as itself.
type AggregateError struct {
	Message string
	Errors  []error
}

func (e *AggregateError) Error() string { return e.Message }

// Unwrap exposes the members to errors.Is and errors.As.
func (e *AggregateError) Unwrap() []error { return e.Errors }

// PanicError is a panic recovered from component code (a body, Init, an effect or a
// cleanup) and turned into an ordinary failure.
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v", e.Value) }

// Unwrap returns the panic value when it is an error.
func (e *PanicError) Unwrap() error {
	if err, ok := e.Value.(error); ok {
		return err
	}
	return nil
}

// protect runs fn, turning a panic into a PanicError.
func protect(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Value: r, Stack: debug.Stack()}
		}
	}()
	return fn()
}
