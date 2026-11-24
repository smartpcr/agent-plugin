package workdir

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

// Cause says why the directory moved.
type Cause string

// The causes of a workspace-root/change (session-views.md sec. 6.2).
const (
	// CauseMove is a host operation that chose the directory.
	CauseMove Cause = "move"
	// CauseRecovery is the fall back to the original project after the
	// directory vanished.
	CauseRecovery Cause = "recovery"
)

// ChangePayload is workspace-root/change: the whole new value of the session's
// working directory. It is the only event the fold consumes, and no tool can
// append it: only Service.Change and recovery do.
type ChangePayload struct {
	Directory string `json:"directory"`
	Workspace string `json:"workspace"`
	Root      string `json:"root"`
	Cause     Cause  `json:"cause"`
}

func validChange(p ChangePayload) error {
	if p.Cause != CauseMove && p.Cause != CauseRecovery {
		return fmt.Errorf("workspace-root/change: cause %q is not move or recovery", p.Cause)
	}
	if p.Workspace == "" {
		return errors.New("workspace-root/change: workspace is required")
	}
	return WorkingDirectory{Path: p.Directory, Workspace: p.Workspace, Root: p.Root, State: Available}.Validate()
}

// ChangeType is the durable, log-only event a move or a recovery records.
var ChangeType = event.MustDefine(event.Default(), event.Spec[ChangePayload]{Name: "workspace-root/change", Version: 1, Validate: validChange})

// ContextKey is the request/context key of the prompt context line.
const ContextKey = "cwd"

// ContextLine is the prompt context line that tells the model where it works.
func ContextLine(path string) string { return "Working directory: " + path }

// initial is the fold's state before any change: the header's original project,
// available, at revision 0, or the zero record (unavailable) when the header
// names none.
func initial(h session.Header) WorkingDirectory {
	if h.Cwd == "" || !filepath.IsAbs(h.Cwd) {
		return WorkingDirectory{State: Unavailable}
	}
	p := filepath.Clean(h.Cwd)
	return WorkingDirectory{Path: p, Workspace: p, Root: p, State: Available}
}

// FoldView is the workingDirectory fold: Init from the header's original
// project, each workspace-root/change replaces the value and bumps the revision.
// It is stored with every commit, so a list or a resume reads it without replay.
func FoldView() views.Definition[WorkingDirectory] {
	return views.Definition[WorkingDirectory]{
		Key: FoldKey, Version: 1, Stored: true,
		Types: []string{ChangeType.Name()},
		Init:  initial,
		Apply: func(w WorkingDirectory, ev event.Event) (WorkingDirectory, bool) {
			p, err := ChangeType.As(ev)
			if err != nil {
				return w, false
			}
			return WorkingDirectory{Path: p.Directory, Workspace: p.Workspace, Root: p.Root, Revision: w.Revision + 1, State: Available}, true
		},
	}
}
