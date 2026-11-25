package shellexec

import (
	"slices"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Registry holds the mounted executors by dialect (shellexec.Key,
// DH-ADR-EXEC-7). A tool whose dialect has no executor reports it unavailable.
type Registry struct {
	mu    sync.Mutex
	execs map[Dialect]*Executor
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{execs: map[Dialect]*Executor{}} }

// Register mounts e under its dialect and returns the release that unmounts it.
// A second executor of one dialect is refused.
func (r *Registry) Register(e *Executor) (release func(), err error) {
	if e == nil {
		return nil, herr.New(ReasonBadConfig, "no executor to register")
	}
	d := e.Dialect()
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.execs[d]; ok {
		return nil, herr.Newf(ReasonBadConfig, "a %s executor is already registered", d)
	}
	r.execs[d] = e
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.execs[d] == e {
			delete(r.execs, d)
		}
	}, nil
}

// Get returns the executor of d, or shellexec/not-found when none is mounted.
func (r *Registry) Get(d Dialect) (*Executor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.execs[d]; ok {
		return e, nil
	}
	return nil, herr.Newf(ReasonNotFound, "no %s shell is available", d)
}

// Dialects lists the mounted dialects, sorted.
func (r *Registry) Dialects() []Dialect {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Dialect, 0, len(r.execs))
	for d := range r.execs {
		out = append(out, d)
	}
	slices.Sort(out)
	return out
}
