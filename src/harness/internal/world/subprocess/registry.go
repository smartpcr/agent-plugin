package subprocess

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// Registry holds the process trees a running host started, so that a shutdown
// whose bound passed can force-stop the trees whose owners' cleanups are stuck.
type Registry struct {
	mu    sync.Mutex
	procs []*Process
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{} }

// Add registers p and returns the function that removes it again; the owner calls
// remove once the tree is gone.
func (r *Registry) Add(p *Process) (remove func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.procs = append(r.procs, p)
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if i := slices.Index(r.procs, p); i >= 0 {
				r.procs = slices.Delete(r.procs, i, i+1)
			}
		})
	}
}

// Live returns the registered processes.
func (r *Registry) Live() []*Process {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.procs)
}

// StopAll stops every registered tree with the grace given, in parallel, and
// returns the joined failures. Each tree is removed once it is gone.
func (r *Registry) StopAll(ctx context.Context, grace time.Duration) error {
	procs := r.Live()
	errs := make([]error, len(procs))
	var wg sync.WaitGroup
	for i, p := range procs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if errs[i] = p.StopTree(ctx, grace); errs[i] == nil {
				r.mu.Lock()
				if j := slices.Index(r.procs, p); j >= 0 {
					r.procs = slices.Delete(r.procs, j, j+1)
				}
				r.mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}
