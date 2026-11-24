// Package workdirtest is the fake working directory: an in-memory Lookup whose
// Access views a test sets directly, so the resolver and the file tools can be
// tested without the fold (TC-01a). Only tests and other fakes import it
// (archtest).
package workdirtest

import (
	"fmt"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Lookup is a fake workdir.Lookup. The zero value is ready; it is safe for
// concurrent use.
type Lookup struct {
	mu   sync.Mutex
	dirs map[session.ID]*Access
}

// New returns an empty Lookup.
func New() *Lookup { return &Lookup{} }

// Set gives session s the working directory path with project root root, at
// the next revision, available, and returns its Access. Workspace is root.
func (l *Lookup) Set(s session.ID, path, root string) *Access {
	l.mu.Lock()
	if l.dirs == nil {
		l.dirs = map[session.ID]*Access{}
	}
	a, ok := l.dirs[s]
	if !ok {
		a = &Access{}
		l.dirs[s] = a
	}
	l.mu.Unlock()
	a.Set(workdir.WorkingDirectory{Path: path, Workspace: root, Root: root, State: workdir.Available})
	return a
}

// Access implements workdir.Lookup.
func (l *Lookup) Access(s session.ID) (workdir.Access, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if a, ok := l.dirs[s]; ok {
		return a, nil
	}
	return nil, herr.Newf(workdir.CodeUnavailable, "session %s has no working directory", s)
}

// Access is a fake workdir.Access. The zero value is unavailable.
type Access struct {
	mu sync.Mutex
	wd workdir.WorkingDirectory
}

// Set replaces the record; its Revision becomes one more than the last, and a
// record that does not validate panics, since it is a test's mistake.
func (a *Access) Set(wd workdir.WorkingDirectory) {
	a.mu.Lock()
	defer a.mu.Unlock()
	wd.Revision = a.wd.Revision + 1
	if err := wd.Validate(); err != nil {
		panic(fmt.Sprintf("workdirtest: %v", err))
	}
	a.wd = wd
}

// Gone marks the directory unavailable, as the fold does when it is deleted.
func (a *Access) Gone() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.wd.State = workdir.Unavailable
	a.wd.Revision++
}

// Current implements workdir.Access.
func (a *Access) Current() (workdir.WorkingDirectory, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.wd.State != workdir.Available {
		return a.wd, herr.Newf(workdir.CodeUnavailable, "working directory %q is unavailable", a.wd.Path)
	}
	return a.wd, nil
}

// Path implements workdir.Access.
func (a *Access) Path() (string, error) {
	wd, err := a.Current()
	if err != nil {
		return "", err
	}
	return wd.Path, nil
}

// Root implements workdir.Access.
func (a *Access) Root() (string, error) {
	wd, err := a.Current()
	if err != nil {
		return "", err
	}
	return wd.Root, nil
}
