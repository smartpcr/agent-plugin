// Package workdir is the contract half of the working-directory fold (C31,
// DH-D-VIEW-10; cycle C-3 of impl.md sec. 10.2): the WorkingDirectory fold record,
// the read-only Access view readers get, and the workdir/unavailable code. Every
// tool of a session works in the same directory, and only the host moves it, so
// this contract has no setter. The fold itself lands with Stage 2.2; the fake for
// tests is session/workdir/workdirtest.
package workdir

import (
	"fmt"
	"path/filepath"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// CodeUnavailable: the working directory no longer exists and no recovery root
// is valid, or the session has none.
const CodeUnavailable herr.Reason = "workdir/unavailable"

// FoldKey is the key of the fold registered with session/views.
const FoldKey = "workingDirectory"

// State says whether the directory exists.
type State string

// The two states.
const (
	Available   State = "available"
	Unavailable State = "unavailable"
)

// WorkingDirectory is the fold record of one session (architecture.md sec. 4.1).
type WorkingDirectory struct {
	// Path is the absolute, cleaned path of the session's working directory.
	Path string `json:"path"`
	// Workspace is the id of the storage/workspace entry Path belongs to.
	Workspace string `json:"workspace"`
	// Root is the project root the fence uses: the repository root, or a linked
	// worktree's own root. Path lies at or below it.
	Root string `json:"root"`
	// Revision increments on every accepted change.
	Revision int64 `json:"revision"`
	// State is available or unavailable.
	State State `json:"state"`
}

// Validate reports whether w is a well-formed record: absolute, clean paths with
// Path at or below Root, a known state and a non-negative revision.
func (w WorkingDirectory) Validate() error {
	for name, p := range map[string]string{"path": w.Path, "root": w.Root} {
		if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return fmt.Errorf("workdir: %s %q is not an absolute, clean path", name, p)
		}
	}
	if rel, err := filepath.Rel(w.Root, w.Path); err != nil || rel == ".." || (len(rel) > 2 && rel[:3] == ".."+string(filepath.Separator)) {
		return fmt.Errorf("workdir: path %q is not below root %q", w.Path, w.Root)
	}
	if w.State != Available && w.State != Unavailable {
		return fmt.Errorf("workdir: state %q is not available or unavailable", w.State)
	}
	if w.Revision < 0 {
		return fmt.Errorf("workdir: revision %d is negative", w.Revision)
	}
	return nil
}

// Access is the read-only view of one session's working directory. Each method
// answers from the fold as it is now; when the directory is unavailable, Path
// and Root return a workdir/unavailable error and Current returns the record
// with that error.
type Access interface {
	Current() (WorkingDirectory, error)
	Path() (string, error)
	Root() (string, error)
}

// Lookup gives readers (the policy resolver, the file and shell tools) the Access
// of a session. An unknown session is workdir/unavailable.
type Lookup interface {
	Access(s session.ID) (Access, error)
}

// Key is the service the working-directory component provides.
var Key = cordis.NewServiceKey[Lookup]("session.workdir")
