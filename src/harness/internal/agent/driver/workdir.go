// This file is the driver's side of the working-directory component (C31,
// DH-D-VIEW-10): the host that attaches each created or resumed session, so the
// directory is checked on resume; the guard that checks it before each tool
// call; the inbox the component's notices reach; and the prompt context line
// each request carries.

package driver

import (
	"context"
	"errors"
	"fmt"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Workdir is the working-directory component the driver hosts;
// *workdir.Service is one.
type Workdir interface {
	Attach(ctx context.Context, s session.Session, n workdir.Notifier) (workdir.WorkingDirectory, error)
	Detach(id session.ID)
	Ensure(ctx context.Context, id session.ID) (workdir.WorkingDirectory, error)
	ContextLine(id session.ID) (string, error)
}

const (
	// WorkdirGuard is the id of the tool guard that checks the working
	// directory before each of an agent's tool calls.
	WorkdirGuard = "session.workdir"
	// WorkdirContext is the name of the runtime context that carries the
	// prompt context line ("Working directory: <path>").
	WorkdirContext = "workdir"
)

// workdirNotices queues the component's notices in the agent's inbox as
// next-step input with source workdir, under the notice's id: the running turn
// takes it at its next step, an idle agent with its next turn. A notice the
// session already admitted is delivered.
func workdirNotices(in *instance) workdir.Notifier {
	return workdir.NotifierFunc(func(ctx context.Context, n workdir.Notice) error {
		_, err := in.Send(ctx, agent.ListNextStep, agent.InboxMessage{ID: n.ID, Source: workdir.NoticeSource, By: WorkdirGuard, Text: n.Text})
		if errors.Is(err, agent.ErrDuplicateMessage) {
			return nil
		}
		return err
	})
}

// attachWorkdir is creation step 3's working-directory part: the session is
// attached (on a resume this is the resume check), and the guard is filed in
// the agent's scope, so it goes with the agent. A directory found unavailable,
// or a notice not yet queued, does not fail the creation: the notice is in the
// inbox or still owed, and the guard refuses tool calls until a person moves
// the directory.
func (f *factory) attachWorkdir(ctx context.Context, in *instance) error {
	wd := f.o.Workdir
	in.wd = wd
	if _, err := wd.Attach(ctx, in.sess, workdirNotices(in)); err != nil && !herr.Has(err, workdir.CodeUnavailable) && !workdir.OnlyUndelivered(err) {
		return err
	}
	id := in.ref.Session
	_, err := f.o.Tools.Guard(in.sc.Ctx(), WorkdirGuard, func(ctx context.Context, _ tools.Call) *tools.Denial {
		if _, err := wd.Ensure(ctx, id); err != nil && !workdir.OnlyUndelivered(err) {
			return &tools.Denial{Code: tools.CodeDenied, Reason: err.Error()}
		}
		return nil
	})
	return err
}

// withWorkdir adds the prompt context line to a, when the driver has the
// component. A directory that is unavailable (gone, or the session detached)
// is expected: the line is omitted, its notice is in the inbox and the guard
// refuses tool calls. Any other failure (a failed fold read) is returned, so
// no request goes out without the context it requires.
func (d *Driver) withWorkdir(a prompt.Assembly) (prompt.Assembly, error) {
	if d.o.Workdir == nil {
		return a, nil
	}
	line, err := d.o.Workdir.ContextLine(d.o.Session.ID())
	if herr.Kind(err) == workdir.CodeUnavailable {
		return a, nil
	}
	if err != nil {
		return a, fmt.Errorf("driver: the working-directory context: %w", err)
	}
	a.Contexts = append(a.Contexts, prompt.Snapshot{Name: WorkdirContext, Owner: WorkdirGuard, Text: line})
	return a, nil
}
