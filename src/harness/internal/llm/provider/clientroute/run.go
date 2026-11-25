package clientroute

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// The runner's defaults.
const (
	// DefaultGrace is how long a cancelled attempt's tree has between the polite
	// and the forced stop (claude-code-cli.md sec. 6, "Cancel").
	DefaultGrace = 2 * time.Second
	// DefaultMaxLine bounds one line of the client's standard output.
	DefaultMaxLine = 8 << 20
	// DefaultStderrBytes bounds what is kept of standard error, which is only
	// searched for a marker and never logged.
	DefaultStderrBytes = 4 << 10
)

// RunSpec says how to start one attempt's client.
type RunSpec struct {
	Client  string
	Program subprocess.Program
	// Args follow the program.
	Args []string
	// Dir is the working directory: the attempt directory.
	Dir string
	Env subprocess.Env
	// Stdin is written to standard input, which is then closed.
	Stdin []byte
	// Grace (DefaultGrace) is given to the tree when ctx ends; MaxLine
	// (DefaultMaxLine) bounds a line of standard output; StderrBytes
	// (DefaultStderrBytes) bounds what is kept of standard error.
	Grace       time.Duration
	MaxLine     int
	StderrBytes int
}

// Run is one started client: its process tree, its standard output read as
// bounded lines, and a bounded window on its standard error.
type Run struct {
	proc   *subprocess.Process
	out    *os.File
	lines  *jsonrpc.Reader
	stderr *window
	stop   sync.Once
	err    error
}

// Start starts the client once in a tree owner, with s.Stdin on standard input.
// When ctx ends the whole tree is stopped with s.Grace. A start the process
// package refuses for its arguments (a batch shim and an argument holding a
// character cmd.exe would change) is llm/invalid-request before anything runs;
// any other start failure is llm/transport.
func Start(ctx context.Context, s RunSpec) (*Run, error) {
	grace, maxLine, keep := s.Grace, s.MaxLine, s.StderrBytes
	if grace <= 0 {
		grace = DefaultGrace
	}
	if maxLine <= 0 {
		maxLine = DefaultMaxLine
	}
	if keep <= 0 {
		keep = DefaultStderrBytes
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, Fail(vocab.CodeTransport, "%s: the output pipe could not be made", s.Client)
	}
	batch := subprocess.Refuse
	if s.Program.Batch {
		batch = subprocess.AllowChecked
	}
	errw := &window{max: keep}
	p, err := subprocess.Start(ctx, subprocess.Spec{
		Argv: append([]string{s.Program.Path}, s.Args...), Dir: s.Dir, Env: s.Env,
		Stdin: bytes.NewReader(s.Stdin), Stdout: pw, Stderr: errw, Grace: grace, Batch: batch, Launch: sandbox.Trusted("clientroute: the operator's signed-in client; its confinement is decided at DH-S-13"),
	})
	_ = pw.Close()
	if err != nil {
		_ = pr.Close()
		if herr.Has(err, subprocess.ReasonBatchRefused) || herr.Has(err, subprocess.ReasonBadArgument) || herr.Has(err, subprocess.ReasonLineTooLong) {
			return nil, Fail(vocab.CodeInvalidRequest, "%s: the client cannot be started with these arguments (%s)", s.Client, reasonOf(err))
		}
		return nil, Fail(vocab.CodeTransport, "%s: the client could not be started (%s)", s.Client, reasonOf(err))
	}
	return &Run{proc: p, out: pr, lines: jsonrpc.NewReader(pr, maxLine), stderr: errw}, nil
}

func reasonOf(err error) string {
	if r, ok := herr.ReasonOf(err); ok {
		return string(r)
	}
	return "unclassified"
}

// ErrLineTooLong is returned by ReadLine for a line over the bound; the reader
// cannot find the next line after it.
var ErrLineTooLong = errors.New("clientroute: a line of the client's output is over the bound")

// ReadLine is the next line of standard output, without its newline, valid
// until the next call; io.EOF once the output has ended (the client and every
// process holding its output have exited, or the tree was stopped).
func (r *Run) ReadLine() ([]byte, error) {
	line, err := r.lines.ReadLine()
	if err != nil && jsonrpc.HasReason(err, jsonrpc.ReasonLineTooLong) {
		return nil, ErrLineTooLong
	}
	return line, err
}

// Tapped is next with tap shown each line it returns, before the caller sees
// it; a nil tap is next itself. It is the routes' Config.Tap: a diagnostic of
// the operator's manual check (plan 16.5.6), never used to log a line.
func Tapped(next func() ([]byte, error), tap func([]byte)) func() ([]byte, error) {
	if tap == nil {
		return next
	}
	return func() ([]byte, error) {
		line, err := next()
		if err == nil {
			tap(line)
		}
		return line, err
	}
}

// Pid is the client's process id.
func (r *Run) Pid() int { return r.proc.Pid() }

// Wait waits up to d for the client to exit and returns its exit code, or -1
// when it is still running.
func (r *Run) Wait(d time.Duration) int {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-r.proc.Exited():
		return r.proc.ExitCode()
	case <-t.C:
		return -1
	}
}

// StderrHas reports whether the kept window of standard error holds marker.
// Standard error is never returned or logged (R9).
func (r *Run) StderrHas(marker string) bool { return r.stderr.has(marker) }

// Stop stops the whole tree, giving it grace after the polite stop, and closes
// the output. It is idempotent; the first call's error is returned again.
func (r *Run) Stop(grace time.Duration) error {
	r.stop.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), grace+subprocess.DefaultSettle)
		defer cancel()
		if err := r.proc.StopTree(ctx, grace); err != nil {
			r.err = fmt.Errorf("clientroute: the client's tree did not stop: %w", err)
		}
		_ = r.out.Close()
	})
	return r.err
}

// window keeps the first max bytes written and drops the rest.
type window struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (w *window) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if room := w.max - len(w.buf); room > 0 {
		w.buf = append(w.buf, b[:min(room, len(b))]...)
	}
	return len(b), nil
}

func (w *window) has(marker string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Contains(w.buf, []byte(marker))
}
