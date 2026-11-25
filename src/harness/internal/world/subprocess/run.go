package subprocess

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/retain"
)

// DefaultMaxOutput is Run's capture limit per captured stream when
// RunOptions.MaxOutput is zero.
const DefaultMaxOutput = 16 << 20

// DefaultTailBytes is how much of what a stream wrote past the limit Run keeps
// when RunOptions.TailBytes is zero.
const DefaultTailBytes = 4 << 10

// runDrain bounds the wait for the standard streams once the direct child has
// exited, so a descendant that holds a captured stream open cannot keep Run
// waiting (DH-LIB-NODEPROC-R8); the rest of the tree is then stopped.
const runDrain = 2 * time.Second

// RunOptions says how Run bounds a run.
type RunOptions struct {
	// Timeout, when positive, is the run's deadline, counted from the call; the
	// context's own deadline applies as well.
	Timeout time.Duration
	// MaxOutput is the capture limit of each captured stream, in bytes; zero is
	// DefaultMaxOutput. One byte over it stops the whole tree and Run returns
	// subprocess/output-limit (DH-LIB-EXECA-R3).
	MaxOutput int
	// TailBytes is how much of what a stream wrote past the limit is kept in
	// Output.Tail; zero is DefaultTailBytes.
	TailBytes int
	// Grace is the wait after the polite stop when the deadline passes, the
	// context ends or the limit is exceeded. Zero, the default, kills the tree at
	// once (DH-LIB-EXECA-R5).
	Grace time.Duration
}

// Output is one captured stream.
type Output struct {
	// Bytes are the exact bytes the program wrote (DH-LIB-EXECA-R2); when
	// Exceeded, the first MaxOutput of them.
	Bytes []byte
	// Exceeded says the stream passed the limit and the run was stopped.
	Exceeded bool
	// Tail is the end of what the stream wrote past the limit before the tree was
	// stopped, at most TailBytes and cut on a rune boundary (util/retain).
	Tail string
	// Omitted counts the bytes written past the limit that Tail does not hold.
	Omitted int64
}

// Result is what Run observed.
type Result struct {
	// Started says the program started; when false, Run's error is the lookup,
	// argument or start error ("never started").
	Started bool
	// ExitCode is the direct child's exit code; -1 when it never started or a
	// signal ended it.
	ExitCode int
	// Stdout and Stderr are the captured streams. A stream the Spec set itself is
	// not captured, and its Output is empty.
	Stdout, Stderr Output
	// Owner is the tree owner that held the run.
	Owner Owner
}

// errOutputLimit is the cause Run cancels its run with when a stream passes the
// limit.
var errOutputLimit = errors.New("output limit exceeded")

// Run starts spec, captures each standard stream the Spec leaves nil, waits for
// the program and returns once its whole tree is gone. The five outcomes are told
// apart (DH-LIB-EXECA-R1):
//
//   - never started: Result.Started is false and the error is Start's;
//   - exited, with any exit code: a nil error, ExitCode set (a non-zero exit is
//     not an error);
//   - the deadline (RunOptions.Timeout or ctx's) passed: subprocess/deadline;
//   - ctx was cancelled: subprocess/canceled, wrapping context.Cause(ctx);
//   - a captured stream passed MaxOutput: subprocess/output-limit, with the
//     stream marked Exceeded; the caller is never handed a silently shortened
//     result.
//
// In the last three cases the tree is stopped with RunOptions.Grace, zero by
// default. Descendants left when the direct child exits are stopped too: the run
// does not outlive its call. If the tree cannot be confirmed gone, the error also
// holds subprocess/tree-survivors.
func Run(ctx context.Context, spec Spec, o RunOptions) (Result, error) {
	if spec.Detach {
		return Result{ExitCode: -1}, errDetached("Run")
	}
	if o.MaxOutput <= 0 {
		o.MaxOutput = DefaultMaxOutput
	}
	if o.TailBytes <= 0 {
		o.TailBytes = DefaultTailBytes
	}
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if o.Timeout > 0 {
		var stop context.CancelFunc
		runCtx, stop = context.WithTimeout(runCtx, o.Timeout)
		defer stop()
	}
	limit := func() { cancel(errOutputLimit) }
	var stdout, stderr *capture
	if spec.Stdout == nil {
		stdout = newCapture(o, limit)
		spec.Stdout = stdout
	}
	if spec.Stderr == nil {
		stderr = newCapture(o, limit)
		spec.Stderr = stderr
	}
	spec.Grace = o.Grace
	spec.waitDelay = runDrain

	p, err := Start(runCtx, spec)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	_ = p.Wait() // the exit code is read below; ErrWaitDelay only says a descendant held a stream
	// The cause is read when the direct child is done, so a deadline that passes
	// while the leftovers are stopped does not relabel a finished run.
	cause := context.Cause(runCtx)
	// Idempotent: when runCtx ended, this waits for the stop the watch started.
	stopErr := p.StopTree(context.WithoutCancel(ctx), 0)

	res := Result{Started: true, ExitCode: p.ExitCode(), Owner: p.Owner(), Stdout: stdout.output(), Stderr: stderr.output()}
	var outcome error
	switch {
	case res.Stdout.Exceeded || res.Stderr.Exceeded:
		outcome = herr.Newf(ReasonOutputLimit, "%s passed the %d-byte capture limit; the run was stopped", exceeded(res), o.MaxOutput)
	case cause == nil:
	case errors.Is(cause, context.DeadlineExceeded):
		outcome = herr.Wrap(ReasonDeadline, cause, "the run passed its deadline; the tree was stopped")
	default:
		outcome = herr.Wrap(ReasonCanceled, cause, "the run was cancelled; the tree was stopped")
	}
	if stopErr != nil {
		return res, errors.Join(outcome, stopErr)
	}
	return res, outcome
}

func exceeded(r Result) string {
	var names []string
	if r.Stdout.Exceeded {
		names = append(names, "standard output")
	}
	if r.Stderr.Exceeded {
		names = append(names, "standard error")
	}
	return strings.Join(names, " and ")
}

// capture holds a stream's exact bytes up to the limit and, past it, the tail in
// a retainer; the first byte past the limit trips the run's stop. It accepts
// every write, so the copy from the child never fails while the stop is under way.
type capture struct {
	mu       sync.Mutex
	max      int
	buf      []byte
	exceeded bool
	trip     func()
	tail     *retain.TextRetainer
}

func newCapture(o RunOptions, trip func()) *capture {
	return &capture{max: o.MaxOutput, trip: trip, tail: retain.Text(retain.Tail, o.TailBytes)}
}

func (c *capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	if room := c.max - len(c.buf); room > 0 {
		take := min(room, len(p))
		c.buf = append(c.buf, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		if !c.exceeded {
			c.exceeded = true
			c.trip()
		}
		_, _ = c.tail.Write(p)
	}
	return n, nil
}

// output is the captured stream; a nil capture (a stream the Spec set) is empty.
func (c *capture) output() Output {
	if c == nil {
		return Output{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := Output{Bytes: append([]byte(nil), c.buf...), Exceeded: c.exceeded}
	if c.exceeded {
		r := c.tail.Result()
		out.Tail, out.Omitted = r.Head+r.Tail, r.Omitted
	}
	return out
}
