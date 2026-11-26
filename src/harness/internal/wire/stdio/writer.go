package stdio

import (
	"bytes"
	"io"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// ReasonClientStalled is the error of a write the peer did not accept within the
// stall bound, and of every write after it.
const ReasonClientStalled herr.Reason = "stdio/client-stalled"

// DefaultWriteStall bounds one write when the caller leaves the bound unset; it is
// the codec's own default, so a codec over the writer and the writer agree.
const DefaultWriteStall = jsonrpc.DefaultWriteStall

// ReasonOutputFailed is the error of a write the underlying writer failed, and of
// every write after it.
const ReasonOutputFailed herr.Reason = "stdio/output-failed"

// Writer is the bounded writer to the client: writes reach the underlying writer
// one at a time, and a write that is not accepted within the stall bound -- waiting
// for its turn included -- ends the writer. Two endings are terminal, and each asks
// stop once, the first only:
//
//   - a stall asks (launch.ClientStalled, output.Stalled) and every later write
//     fails at once with stdio/client-stalled;
//   - an error of the underlying writer (the client closed its end) is the client
//     gone: it asks (launch.InputClosed, output.OK) and every later write fails at
//     once with stdio/output-failed wrapping the first error.
//
// No underlying write starts once the writer has ended: admission and the end are
// decided under one lock. The writer holds no exit timer: the exit is boot's.
//
// The write that stalled stays blocked in the underlying writer until its owner
// closes it; the writer has copied the bytes, so the caller may reuse its slice.
type Writer struct {
	w     io.Writer
	stall time.Duration
	stop  func(launch.Reason, int)

	// turn holds one token while a write is in the underlying writer.
	turn chan struct{}
	// dead is closed when the writer ends; err is set first, under mu.
	dead chan struct{}
	mu   sync.Mutex
	err  error

	// admit, when set, runs after a write passed the first end check; tests use it
	// to pause a write in the window before it competes for the turn.
	admit func()
}

// NewWriter returns a writer to w whose writes are bounded by stall
// (DefaultWriteStall when not positive); stop is asked once when the writer ends.
func NewWriter(w io.Writer, stall time.Duration, stop func(launch.Reason, int)) *Writer {
	if stall <= 0 {
		stall = DefaultWriteStall
	}
	return &Writer{w: w, stall: stall, stop: stop, turn: make(chan struct{}, 1), dead: make(chan struct{})}
}

// Write writes p whole, or fails. A stall fails with stdio/client-stalled, a
// failed underlying write with stdio/output-failed.
func (w *Writer) Write(p []byte) (int, error) {
	if err := w.ended(); err != nil {
		return 0, err
	}
	if w.admit != nil {
		w.admit()
	}
	timer := time.NewTimer(w.stall)
	defer timer.Stop()
	select {
	case w.turn <- struct{}{}:
	case <-timer.C:
		return 0, w.stalled()
	case <-w.dead:
		return 0, w.ended()
	}
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	line := bytes.Clone(p)
	// The turn may be won after the writer ended (the stalled write returned and
	// released it): the end is checked and the write started under the same lock
	// that publishes the end, so a write either starts before the end or never.
	w.mu.Lock()
	if err := w.err; err != nil {
		w.mu.Unlock()
		<-w.turn
		return 0, err
	}
	go func() {
		n, err := w.w.Write(line)
		<-w.turn
		done <- result{n, err}
	}()
	w.mu.Unlock()
	select {
	case r := <-done:
		if r.err == nil && r.n < len(line) {
			r.err = io.ErrShortWrite
		}
		if r.err != nil {
			return r.n, w.failed(r.err)
		}
		return r.n, nil
	case <-timer.C:
		return 0, w.stalled()
	}
}

// Ended is closed once the writer has ended, by a stall or a failed write.
func (w *Writer) Ended() <-chan struct{} { return w.dead }

// Err returns why the writer ended, or nil while it runs.
func (w *Writer) Err() error { return w.ended() }

func (w *Writer) ended() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func (w *Writer) stalled() error {
	return w.end(herr.New(ReasonClientStalled, "stdio: the client accepted no bytes for "+w.stall.String()),
		launch.ClientStalled, output.Stalled)
}

func (w *Writer) failed(cause error) error {
	return w.end(herr.Wrap(ReasonOutputFailed, cause, "stdio: writing to the client failed"),
		launch.InputClosed, output.OK)
}

// end publishes the first ending and asks stop for it; a later ending joins the
// first and returns its error.
func (w *Writer) end(err error, r launch.Reason, status int) error {
	w.mu.Lock()
	if w.err != nil {
		err = w.err
		w.mu.Unlock()
		return err
	}
	w.err = err
	close(w.dead)
	w.mu.Unlock()
	if w.stop != nil {
		w.stop(r, status)
	}
	return err
}
