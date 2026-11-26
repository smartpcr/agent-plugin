// Package stdio is component wire.stdio's owner of the process's standard input and
// output (C48, DH-D-SDK-3, DH-ADR-SDK-11). Claim succeeds once per process: it keeps
// private handles to the real streams, points os.Stdout at the log (standard error
// by default) so a stray print from any compiled-in code never reaches the client,
// and points os.Stdin at the null device. Writes to the client go through the
// bounded Writer.
//
// The owner never ends the process itself. It asks launch.App.Stop once: with
// (InputClosed, 0) when the client is gone -- its input ends, a write to it fails,
// or the server reports a shutdown -- and with (ClientStalled, 125) when a write
// stalls. The exit bound and the forced
// status 124 are boot's (M2-C-10).
package stdio

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// ReasonClaimed is the error of a second Claim in one process.
const ReasonClaimed herr.Reason = "stdio/claimed"

// claimed is set by the first Claim; no claim is ever released.
var claimed atomic.Bool

// Config says how the owner ends and where stray output goes.
type Config struct {
	// App is asked to stop once, on the first ending. Required.
	App launch.App
	// WriteStall bounds one write to the client (DefaultWriteStall when not
	// positive). A codec over Output should use a bound no shorter, so the stall is
	// seen here first.
	WriteStall time.Duration
	// Log is where os.Stdout points after the claim: every stray write lands there.
	// Nil is the real standard error.
	Log *os.File
}

// Owner holds the process's real standard input and output.
type Owner struct {
	app  launch.App
	in   io.Reader
	out  *Writer
	once sync.Once
}

// Claim takes the process's standard input and output. A second call fails with
// stdio/claimed and changes nothing.
func Claim(cfg Config) (*Owner, error) {
	if cfg.App == nil {
		return nil, errors.New("stdio: Claim needs the App to stop")
	}
	if !claimed.CompareAndSwap(false, true) {
		return nil, herr.New(ReasonClaimed, "stdio: standard input and output are already claimed")
	}
	null, err := os.Open(os.DevNull)
	if err != nil {
		claimed.Store(false)
		return nil, fmt.Errorf("stdio: open the null device for os.Stdin: %w", err)
	}
	log := cfg.Log
	if log == nil {
		log = os.Stderr
	}
	// A Go program that writes to a closed pipe on descriptor 1 dies of SIGPIPE
	// unless the signal is ignored; ignored, the write fails with EPIPE and the
	// Writer turns it into the InputClosed ending.
	signal.Ignore(syscall.SIGPIPE)
	o := &Owner{app: cfg.App}
	in, out := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = null, log
	o.in = &input{r: in, end: o.InputClosed}
	o.out = NewWriter(out, cfg.WriteStall, o.end)
	return o, nil
}

// Input is the client's input. Its first read error, end of file included, is the
// InputClosed ending.
func (o *Owner) Input() io.Reader { return o.in }

// Output is the bounded writer to the client.
func (o *Owner) Output() *Writer { return o.out }

// InputClosed is the ending of a client that is gone or asked to shut down: the
// first ending of the owner asks App.Stop(InputClosed, 0).
func (o *Owner) InputClosed() { o.end(launch.InputClosed, output.OK) }

// end asks the App to stop on the owner's first ending; later endings join it.
func (o *Owner) end(r launch.Reason, status int) {
	o.once.Do(func() { o.app.Stop(r, status) })
}

// input reports its first read error as the end of the client's input.
type input struct {
	r   io.Reader
	end func()
}

func (i *input) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if err != nil {
		i.end()
	}
	return n, err
}
