package fs

import (
	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
)

// Mutation is the payload of BeforeMutation: what is about to change.
type Mutation struct {
	Session session.ID
	// CallID is the tool call the mutation belongs to; "" for a direct caller.
	CallID string
	Op     Op
	Target Target
}

// Observation is the payload of Observed: what a call saw of one file.
type Observation struct {
	Session session.ID
	CallID  string
	Op      Op
	Target  Target
	// Present is false when the file is absent (deleted, moved away, missing).
	Present bool
	// Version is the observed version, with its digest when the content was
	// read or written; zero when absent.
	Version Version
}

// The live events of the seam (filesystem.md sec. 6.2). ops is their one
// dispatcher.
var (
	// BeforeMutation is ordered: ops emits it, once per target, after the fence
	// admitted the call and before the provider runs. A listener's error (or
	// panic) stops the mutation; nothing is written.
	BeforeMutation = cordis.NewEvent[Mutation]("fs.before-mutation")
	// Observed is a notification: ops notifies it after a read or a mutation
	// succeeded. A listener cannot fail the call.
	Observed = cordis.NewEvent[Observation]("fs.observed")
)
