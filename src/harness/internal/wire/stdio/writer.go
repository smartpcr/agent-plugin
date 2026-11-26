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

// Writer is the bounded writer to the client: writes reach the underlying writer
// one at a time, and a write that is not accepted within the stall bound -- waiting
// for its turn included -- ends the writer. On that end it asks stop once for
// (launch.ClientStalled, output.Stalled) and every later write fails at once with
// stdio/client-stalled. The writer holds no exit timer: the exit is boot's.
//
// The write that stalled stays blocked in the underlying writer until its owner
// closes it; the writer has copied the bytes, so the caller may reuse its slice.
type Writer struct {
	w     io.Writer
	stall time.Duration
	stop  func(launch.Reason, int)

	// turn holds one token while a write is in the underlying writer.
	turn chan struct{}
	dead chan struct{}
	once sync.Once
	err  error
}

// NewWriter returns a writer to w whose writes are bounded by stall
// (DefaultWriteStall when not positive); stop is asked once when a write stalls.
func NewWriter(w io.Writer, stall time.Duration, stop func(launch.Reason, int)) *Writer {
	if stall <= 0 {
		stall = DefaultWriteStall
	}
	return &Writer{w: w, stall: stall, stop: stop, turn: make(chan struct{}, 1), dead: make(chan struct{})}
}

// Write writes p whole, or fails. A stall fails with stdio/client-stalled.
func (w *Writer) Write(p []byte) (int, error) {
	select {
	case <-w.dead:
		return 0, w.err
	default:
	}
	timer := time.NewTimer(w.stall)
	defer timer.Stop()
	select {
	case w.turn <- struct{}{}:
	case <-timer.C:
		return 0, w.stalled()
	case <-w.dead:
		return 0, w.err
	}
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	line := bytes.Clone(p)
	go func() {
		n, err := w.w.Write(line)
		<-w.turn
		done <- result{n, err}
	}()
	select {
	case r := <-done:
		return r.n, r.err
	case <-timer.C:
		return 0, w.stalled()
	}
}

// Stalled is closed once a write has stalled.
func (w *Writer) Stalled() <-chan struct{} { return w.dead }

func (w *Writer) stalled() error {
	w.once.Do(func() {
		w.err = herr.New(ReasonClientStalled, "stdio: the client accepted no bytes for "+w.stall.String())
		close(w.dead)
		if w.stop != nil {
			w.stop(launch.ClientStalled, output.Stalled)
		}
	})
	return w.err
}
