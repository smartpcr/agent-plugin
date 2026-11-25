package launch

import (
	"context"
	"os"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// Channel is the host's end of one stage's pair: it sends go on control and reads
// ready and exec-failed on status. The child's ends travel in Extra. Each method
// is called once, in the order of the exchange: Extra and Bind before the start,
// CloseChildEnds after it, ReadReady, then Go and AwaitExec, or Reject.
type Channel struct {
	control, status *os.File // the host's ends
	child           []subprocess.Channel

	closeOnce sync.Once
}

// NewChannel creates the pair.
func NewChannel() (*Channel, error) {
	controlR, controlW, err := os.Pipe()
	if err != nil {
		return nil, herr.Wrap(sandbox.CodeLaunchFailed, err, "create the control pipe")
	}
	statusR, statusW, err := os.Pipe()
	if err != nil {
		_ = controlR.Close()
		_ = controlW.Close()
		return nil, herr.Wrap(sandbox.CodeLaunchFailed, err, "create the status pipe")
	}
	return &Channel{control: controlW, status: statusR, child: []subprocess.Channel{{File: controlR}, {File: statusW}}}, nil
}

// Extra is the child's two ends, for subprocess.Spec.Extra.
func (c *Channel) Extra() []subprocess.Channel { return c.child }

// Bind returns p with the child's numbers of the two ends filled in.
func (c *Channel) Bind(p Plan) Plan {
	p.Control = subprocess.ChildFD(c.child, 0)
	p.Status = subprocess.ChildFD(c.child, 1)
	return p
}

// CloseChildEnds closes the host's copies of the child's ends once the stage has
// started (or failed to), so end of file reaches each side when the other is gone.
func (c *Channel) CloseChildEnds() {
	for _, ch := range c.child {
		_ = ch.File.Close()
	}
}

// ReadReady reads the stage's ready. End of file (the stage died), a malformed
// frame, or the end of ctx (the launch time limit) is sandbox/launch-failed; on
// the last the status end is closed. The caller then rejects and stops the stage.
func (c *Channel) ReadReady(ctx context.Context) (Ready, error) {
	f, err := c.read(ctx)
	if closed(err) {
		return Ready{}, herr.Wrap(sandbox.CodeLaunchFailed, err, "the stage ended before ready")
	}
	if err != nil {
		return Ready{}, err
	}
	return f.ready()
}

// Go sends go and closes the control end: the stage may become the command.
func (c *Channel) Go() error {
	err := writeFrame(c.control, frame{Msg: msgGo})
	_ = c.control.Close()
	return err
}

// Reject is the host's refusal: it closes both of its ends without go, so the
// stage reads end of file and exits with no command run.
func (c *Channel) Reject() { c.Close() }

// AwaitExec waits, after Go, for the stage to become the command: end of file on
// status means the command started (its ends closed on exec), an exec-failed
// frame is sandbox/exec-failed with the stage's reason, anything else is
// sandbox/launch-failed. It closes the status end.
func (c *Channel) AwaitExec(ctx context.Context) error {
	defer c.Close()
	f, err := c.read(ctx)
	switch {
	case closed(err):
		return nil
	case err != nil:
		return err
	case f.Msg == msgExecFailed && f.Error != nil && f.Backend == nil && f.Governed == nil && f.Canary == nil:
		return herr.New(sandbox.CodeExecFailed, *f.Error)
	}
	return failed("the stage sent %q after go", f.Msg)
}

// Close closes the host's ends; it may be called more than once.
func (c *Channel) Close() {
	c.closeOnce.Do(func() {
		_ = c.control.Close()
		_ = c.status.Close()
	})
}

// read reads one frame from status, bounded by ctx. When ctx ends first the
// status end is closed; a read that a closed pipe does not interrupt (Windows)
// returns once the caller stops the stage.
func (c *Channel) read(ctx context.Context) (frame, error) {
	type result struct {
		f   frame
		err error
	}
	done := make(chan result, 1)
	go func() {
		f, err := readFrame(c.status)
		done <- result{f, err}
	}()
	select {
	case r := <-done:
		return r.f, r.err
	case <-ctx.Done():
		_ = c.status.Close()
		return frame{}, herr.Wrap(sandbox.CodeLaunchFailed, ctx.Err(), "wait for the stage")
	}
}
