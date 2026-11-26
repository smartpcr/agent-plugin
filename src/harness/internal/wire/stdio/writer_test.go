package stdio_test

import (
	"bytes"
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
		case <-w.Stalled():
		default:
			t.Fatal("Stalled is open after a stall")
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
