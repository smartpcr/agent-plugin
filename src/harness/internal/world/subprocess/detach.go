package subprocess

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Detached is the owner kind a detached launch reports: no tree owner.
const Detached OwnerKind = "detached"

// drainer is a tree owner that copies the child's streams itself (the Windows
// in-job creation); the waiter drains it after exec.Cmd.Wait.
type drainer interface{ drain() error }

// errDetached is the refusal of a stop or a tree wait on a detached launch.
func errDetached(op string) error {
	return herr.Newf(ReasonDetached, "%s: a detached launch is never stopped or awaited by the host", op)
}

// checkDetachStreams refuses a stream the host would have to keep copying for a
// detached child: only the null device and files are allowed.
func checkDetachStreams(spec Spec) error {
	streams := []struct {
		name string
		mode any
	}{{"Stdin", spec.Stdin}, {"Stdout", spec.Stdout}, {"Stderr", spec.Stderr}}
	for _, s := range streams {
		switch s.mode.(type) {
		case nil, discardMode, inheritMode, *os.File:
		default:
			return herr.Newf(ReasonDetached, "Spec.%s of a detached launch must be nil, Discard, Inherit or a file; the host keeps no copy running for it", s.name)
		}
	}
	return nil
}

// startDetachedProcess starts the child with no tree owner and no watch, and
// reaps it when it exits, so no zombie is left on POSIX.
func startDetachedProcess(spec Spec, build func() *exec.Cmd) (*Process, error) {
	cmd, err := startDetached(build)
	if err != nil {
		return nil, herr.Wrap(ReasonStartFailed, err, "start "+filepath.Base(spec.Argv[0])+" detached")
	}
	p := &Process{cmd: cmd, owner: noOwner{}, report: Owner{Kind: Detached}, detached: true,
		exited: make(chan struct{}), releasedC: make(chan struct{}), released: true}
	close(p.releasedC)
	go func() {
		p.waitErr = cmd.Wait()
		close(p.exited)
	}()
	return p, nil
}

// Detached reports whether p is a detached launch.
func (p *Process) Detached() bool { return p.detached }

// noOwner is the owner of a detached launch: nothing.
type noOwner struct{}

func (noOwner) start(func() *exec.Cmd) (*exec.Cmd, error) { return nil, errDetached("start") }
func (noOwner) report() Owner                             { return Owner{Kind: Detached} }
func (noOwner) polite() error                             { return nil }
func (noOwner) kill() error                               { return nil }
func (noOwner) empty() (bool, error)                      { return true, nil }
func (noOwner) describe() string                          { return "no tree owner (detached)" }
func (noOwner) release()                                  {}
