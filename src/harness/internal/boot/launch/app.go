package launch

import (
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

// ProcessesKey is the service under which the boot tree provides the registry of the
// process trees its components started; shutdown force-stops what is still there
// when the bound passes.
var ProcessesKey = cordis.NewServiceKey[*subprocess.Registry]("boot.processes")

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

// MarkReady closes Ready; later calls do nothing.
func (c *Control) MarkReady() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.isReady {
		c.isReady = true
		close(c.ready)
	}
}

// Urgent closes when an Interrupted stop arrives after the first stop.
func (c *Control) Urgent() <-chan struct{} { return c.urgent }

// Result returns the reason and status of the first Stop, and whether there was one.
func (c *Control) Result() (Reason, int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reason, c.status, c.stopped
}
