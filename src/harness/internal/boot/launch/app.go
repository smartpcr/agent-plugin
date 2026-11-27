package launch

import (
	"io"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// Reason is why the process stops (boot.md §6.2, "Stopping").
type Reason string

// The stop reasons.
const (
	Completed     Reason = "completed"
	Interrupted   Reason = "interrupted"
	Terminated    Reason = "terminated"
	InputClosed   Reason = "input-closed"
	ClientStalled Reason = "client-stalled"
	Requested     Reason = "requested"
	Fatal         Reason = "fatal"
)

// App is the one way to end the process. Ready closes once, after the boot audit;
// Stop asks for the end with a reason and the exit status, and Stopping closes on the
// first Stop. Only boot.Run unloads the tree; a caller of Stop holds no timer.
type App interface {
	Ready() <-chan struct{}
	Stop(r Reason, status int)
	Stopping() <-chan struct{}
}

// AppKey is the service under which the boot tree provides the App.
var AppKey = cordis.NewServiceKey[App]("boot.app")

// DataKey is the service under which the boot tree provides the launch's data: an
// app's start-up component (cli.headless) reads its own words there.
var DataKey = cordis.NewServiceKey[Data]("boot.launch")

// ProcessesKey is the service under which the boot tree provides the registry of the
// process trees its components started; shutdown force-stops what is still there
// when the bound passes.
var ProcessesKey = cordis.NewServiceKey[*subprocess.Registry]("boot.processes")

// Stdio is a client stream pair the launcher hands the stdio carrier in place of
// the process's standard input and output: an in-process boot (a test) runs the
// sdk profile over pipes without claiming the process's streams.
type Stdio struct {
	In  io.Reader
	Out io.Writer
}

// StdioKey is the service under which the boot tree provides Stdio when the
// launcher passed one; absent, wire.stdio claims the process's streams.
var StdioKey = cordis.NewServiceKey[Stdio]("boot.stdio")

// Control is the App that boot.Run owns: it closes Ready and reads the stop.
type Control struct {
	mu       sync.Mutex
	ready    chan struct{}
	stopping chan struct{}
	urgent   chan struct{}
	isReady  bool
	stopped  bool
	isUrgent bool
	reason   Reason
	status   int
}

var _ App = (*Control)(nil)

// NewApp returns a Control that is neither ready nor stopping.
func NewApp() *Control {
	return &Control{ready: make(chan struct{}), stopping: make(chan struct{}), urgent: make(chan struct{})}
}

// Ready closes once the boot audit passed.
func (c *Control) Ready() <-chan struct{} { return c.ready }

// Stopping closes on the first Stop.
func (c *Control) Stopping() <-chan struct{} { return c.stopping }

// Stop asks the process to end. The first call fixes the reason and the status; a
// later call joins it, except that an Interrupted stop during shutdown ends the
// shutdown wait at once (Urgent).
func (c *Control) Stop(r Reason, status int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.stopped {
		c.stopped, c.reason, c.status = true, r, status
		close(c.stopping)
		return
	}
	if r == Interrupted && !c.isUrgent {
		c.isUrgent = true
		close(c.urgent)
	}
}

// MarkReady closes Ready and reports whether it is closed; later calls do nothing.
// Once a stop was asked Ready never closes: a launch that stops during start-up
// is never announced.
func (c *Control) MarkReady() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.isReady && !c.stopped {
		c.isReady = true
		close(c.ready)
	}
	return c.isReady
}

// Urgent closes when an Interrupted stop arrives after the first stop.
func (c *Control) Urgent() <-chan struct{} { return c.urgent }

// Result returns the reason and status of the first Stop, and whether there was one.
func (c *Control) Result() (Reason, int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reason, c.status, c.stopped
}
