package stdio_test

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/stdio"
)

type stopCall struct {
	reason launch.Reason
	status int
}

// A write the client never accepts ends the writer when the bound passes: the
// write and every later one fail with stdio/client-stalled, and stop is asked
// exactly once for (ClientStalled, 125).
//
//dsh:test DH-T-SDK-22
func TestBoundedWriterStallAsksStopOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pr, pw := io.Pipe()
		var calls []stopCall
		w := stdio.NewWriter(pw, time.Second, func(r launch.Reason, status int) {
			calls = append(calls, stopCall{r, status})
		})
		start := time.Now()
		if _, err := w.Write([]byte("{}\n")); !herr.Has(err, stdio.ReasonClientStalled) {
			t.Fatalf("stalled write: %v, want %s", err, stdio.ReasonClientStalled)
		}
		if waited := time.Since(start); waited != time.Second {
			t.Fatalf("the stall was seen after %v, want 1s", waited)
		}
		if _, err := w.Write([]byte("{}\n")); !herr.Has(err, stdio.ReasonClientStalled) {
			t.Fatalf("write after the stall: %v, want %s", err, stdio.ReasonClientStalled)
		}
		if time.Since(start) != time.Second {
			t.Fatal("a write after the stall waited")
		}
		select {
		case <-w.Ended():
		default:
			t.Fatal("Ended is open after a stall")
		}
		want := []stopCall{{launch.ClientStalled, output.Stalled}}
		if len(calls) != 1 || calls[0] != want[0] {
			t.Fatalf("stop calls %v, want %v", calls, want)
		}
		// Closing the pipe releases the write that stalled.
		if err := pr.Close(); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
	})
}

// A client that accepts each write in time gets every byte in order, and stop is
// never asked; the writer copies the bytes, so the caller may reuse its slice.
func TestBoundedWriterPassesWritesThrough(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var got bytes.Buffer
		stops := 0
		w := stdio.NewWriter(&got, time.Second, func(launch.Reason, int) { stops++ })
		line := []byte("{\"a\":1}\n")
		for range 3 {
			if n, err := w.Write(line); err != nil || n != len(line) {
				t.Fatalf("write: %d, %v", n, err)
			}
		}
		line[0] = 'x'
		if want := bytes.Repeat([]byte("{\"a\":1}\n"), 3); !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("written %q, want %q", got.Bytes(), want)
		}
		if stops != 0 {
			t.Fatalf("stop asked %d times without a stall", stops)
		}
	})
}

// failWriter fails every write with err and counts the calls.
type failWriter struct {
	err   error
	calls int
}

func (f *failWriter) Write([]byte) (int, error) {
	f.calls++
	return 0, f.err
}

// A failed underlying write is the client gone: it fails with stdio/output-failed
// wrapping the cause, asks stop exactly once for (InputClosed, 0), and no later
// write reaches the failed output.
//
//dsh:test DH-T-SDK-22
func TestBoundedWriterFailureAsksStopOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		out := &failWriter{err: io.ErrClosedPipe}
		var calls []stopCall
		w := stdio.NewWriter(out, time.Second, func(r launch.Reason, status int) {
			calls = append(calls, stopCall{r, status})
		})
		for range 3 {
			_, err := w.Write([]byte("{}\n"))
			if !herr.Has(err, stdio.ReasonOutputFailed) || !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("write to a failed output: %v, want %s wrapping %v", err, stdio.ReasonOutputFailed, io.ErrClosedPipe)
			}
		}
		if out.calls != 1 {
			t.Fatalf("the failed output got %d writes, want 1", out.calls)
		}
		select {
		case <-w.Ended():
		default:
			t.Fatal("Ended is open after a failed write")
		}
		want := stopCall{launch.InputClosed, output.OK}
		if len(calls) != 1 || calls[0] != want {
			t.Fatalf("stop calls %v, want [%v]", calls, want)
		}
	})
}

// refusingWriter fails every write with err.
type refusingWriter struct{ err error }

func (r refusingWriter) Write([]byte) (int, error) { return 0, r.err }

// A writer with no bound uses DefaultWriteStall and may have no stop: Err is nil
// while it runs, and after a failed write it is the stdio/output-failed ending,
// wrapping the cause.
//
//dsh:test DH-T-SDK-22
func TestWriterErrWithDefaultsAndNoStop(t *testing.T) {
	cause := errors.New("pipe closed")
	w := stdio.NewWriter(refusingWriter{cause}, 0, nil)
	if err := w.Err(); err != nil {
		t.Fatalf("Err before any write = %v", err)
	}
	if _, err := w.Write([]byte("{}\n")); !herr.Has(err, stdio.ReasonOutputFailed) {
		t.Fatalf("failed write: %v, want %s", err, stdio.ReasonOutputFailed)
	}
	if err := w.Err(); !herr.Has(err, stdio.ReasonOutputFailed) || !errors.Is(err, cause) {
		t.Fatalf("Err after a failed write = %v, want %s wrapping %v", err, stdio.ReasonOutputFailed, cause)
	}
}
