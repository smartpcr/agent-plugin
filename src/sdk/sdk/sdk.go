// Package sdk is the Go client of the harness (DH-D-SDK-8, C53): a Go program
// starts `dsh --home <dir> --profile sdk` as a child, or connects to pipes it owns,
// and drives sessions over the SDK protocol. It imports the standard library,
// src/sdk/jsonrpc and src/sdk/protocol only (architecture.md sec. 3.4 rule 5).
//
// Cancelling the context of a call stops the wait and tells the host with
// $/cancel; it never stops agent work. A host request (an approval, a question)
// runs its handler on its own goroutine with a context that ends when the host
// withdraws the request; the codec then answers jsonrpc/cancelled, so a late
// answer is never sent. A handler that ignores its context cannot be stopped.
package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	//lint:ignore onespawn this public client module is not the harness's spawn path: its Start is the os/exec child of sdk-acp-typert.md sec. 6.1, and it cannot import the harness
	"os/exec"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// BinEnv names the dsh binary when Options.Bin is empty.
const BinEnv = "DSH_BIN"

// DefaultShutdownGrace is the wait after each rung of the close ladder: the
// host's default ShutdownTimeout (20s) plus a margin.
const DefaultShutdownGrace = 25 * time.Second

// Handlers answer the host's requests. A nil handler is not advertised, so the
// host never sends that request and treats it as refused.
type Handlers struct {
	Approval func(ctx context.Context, r protocol.ApprovalRequest) protocol.ApprovalResult
	Question func(ctx context.Context, r protocol.QuestionRequest) (protocol.QuestionResult, error)
}

// Options configure a client.
type Options struct {
	// Bin is the dsh binary; empty takes $DSH_BIN and nothing else.
	Bin string
	// Home is the harness home the child runs on; Start refuses an empty one.
	Home string
	// Dir is the child's working directory; empty is the caller's.
	Dir string
	// Env entries ("NAME=value") are added to the caller's environment.
	Env []string
	// Stderr receives the child's diagnostics; nil discards them.
	Stderr io.Writer
	// ClientInfo names the program in initialize.
	ClientInfo protocol.Implementation
	// TimeZone is sent in initialize only when set.
	TimeZone string
	// Defaults apply to every session the connection creates.
	Defaults *protocol.Defaults
	Handlers Handlers
	// ShutdownGrace is the wait after each rung of Close (DefaultShutdownGrace).
	ShutdownGrace time.Duration
}

// Transport is a pair of pipes the caller owns: In reads the host's output, Out
// writes the host's input and is closed by Harness.Close.
type Transport struct {
	In  io.Reader
	Out io.WriteCloser
}

// Rung names the step of the close ladder during which the child exited.
type Rung string

// The rungs of the close ladder, in order. RungNone is a child that exited
// before Close, or a connection without a child.
const (
	RungNone      Rung = ""
	RungShutdown  Rung = "shutdown"
	RungTerminate Rung = "terminate"
	RungKill      Rung = "kill"
)

// Ending is how a started child ended.
type Ending struct {
	Rung     Rung
	ExitCode int
}

// Harness is one connection to a host, and the child process when Start made it.
type Harness struct {
	conn  *jsonrpc.Conn
	out   io.WriteCloser
	ownIn io.ReadCloser // the host's output when Start made the pipe; Connect's In stays the caller's
	grace time.Duration
	info  protocol.InitializeResult

	cmd       *exec.Cmd
	exited    chan struct{}
	terminate func() error
	kill      func() error

	handlers sync.WaitGroup
	mu       sync.Mutex
	closing  bool // set once Close has stopped admitting handlers
	running  int
	ending   Ending
	runs     map[runKey]chan protocol.RunCompletedNotification
	subs     map[string][]*Subscription
	subsErr  error // why the subscriptions ended; set when the connection ends

	closeOnce sync.Once
	closeErr  error
}

// Start launches `dsh --home <Home> --profile sdk` and initializes it. It fails
// before any process starts when Home is empty or no binary is named. A child
// that fails initialize, or answers another protocol version, is closed.
func Start(ctx context.Context, o Options) (*Harness, error) {
	if o.Home == "" {
		return nil, errors.New("sdk: Options.Home is required")
	}
	bin := o.Bin
	if bin == "" {
		bin = os.Getenv(BinEnv)
	}
	if bin == "" {
		return nil, fmt.Errorf("sdk: no dsh binary: set Options.Bin or %s", BinEnv)
	}
	hostIn, out, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("sdk: %w", err)
	}
	in, hostOut, err := os.Pipe()
	if err != nil {
		hostIn.Close()
		out.Close()
		return nil, fmt.Errorf("sdk: %w", err)
	}
	cmd := exec.Command(bin, "--home", o.Home, "--profile", "sdk")
	cmd.Dir = o.Dir
	cmd.Env = append(os.Environ(), o.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = hostIn, hostOut, o.Stderr
	err = cmd.Start()
	// The child holds its own ends now; the parent keeps only its ends, so the
	// host's output reads end of file once the child is gone.
	hostIn.Close()
	hostOut.Close()
	if err != nil {
		in.Close()
		out.Close()
		return nil, fmt.Errorf("sdk: start %s: %w", bin, err)
	}
	h := newHarness(o, in, out)
	h.ownIn, h.cmd, h.exited = in, cmd, make(chan struct{})
	h.terminate = func() error { return terminate(cmd.Process) }
	h.kill = cmd.Process.Kill
	go func() {
		_ = cmd.Wait()
		h.mu.Lock()
		h.ending.ExitCode = cmd.ProcessState.ExitCode()
		h.mu.Unlock()
		close(h.exited)
	}()
	if err := h.initialize(ctx, o); err != nil {
		return nil, errors.Join(err, h.Close(context.WithoutCancel(ctx)))
	}
	return h, nil
}

// Connect initializes a host on pipes and a process the caller owns.
func Connect(ctx context.Context, t Transport, o Options) (*Harness, error) {
	if t.In == nil || t.Out == nil {
		return nil, errors.New("sdk: Transport needs In and Out")
	}
	h := newHarness(o, t.In, t.Out)
	if err := h.initialize(ctx, o); err != nil {
		return nil, errors.Join(err, h.Close(context.WithoutCancel(ctx)))
	}
	return h, nil
}

func newHarness(o Options, in io.Reader, out io.WriteCloser) *Harness {
	h := &Harness{out: out, grace: o.ShutdownGrace, runs: map[runKey]chan protocol.RunCompletedNotification{}, subs: map[string][]*Subscription{}}
	if h.grace <= 0 {
		h.grace = DefaultShutdownGrace
	}
	methods := map[string]jsonrpc.Handler{}
	if f := o.Handlers.Approval; f != nil {
		methods["approval/request"] = h.handler(func(ctx context.Context, raw json.RawMessage) (any, error) {
			var r protocol.ApprovalRequest
			if err := json.Unmarshal(raw, &r); err != nil {
				return nil, err
			}
			return f(ctx, r), nil
		})
	}
	if f := o.Handlers.Question; f != nil {
		methods["question/ask"] = h.handler(func(ctx context.Context, raw json.RawMessage) (any, error) {
			var r protocol.QuestionRequest
			if err := json.Unmarshal(raw, &r); err != nil {
				return nil, err
			}
			return f(ctx, r)
		})
	}
	h.conn = jsonrpc.Serve(context.Background(), in, out, jsonrpc.Options{Methods: methods, OnNotify: h.notify})
	go func() {
		<-h.conn.Done()
		h.endSubscriptions(fmt.Errorf("sdk: the connection ended: %w", h.conn.Err()))
	}()
	return h
}

// errClosing refuses a handler that would start after Close stopped admitting
// them; the connection is closed by then, so the answer is never sent.
var errClosing = errors.New("sdk: the client is closed")

// handler counts f while it runs, so Close can report handlers that outlive it.
// Admission and counting share the lock Close takes to stop admitting, so a
// request the codec dispatched before closing but scheduled after it never runs.
func (h *Harness) handler(f jsonrpc.Handler) jsonrpc.Handler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		h.mu.Lock()
		if h.closing {
			h.mu.Unlock()
			return nil, errClosing
		}
		h.running++
		h.handlers.Add(1)
		h.mu.Unlock()
		defer func() {
			h.mu.Lock()
			h.running--
			h.mu.Unlock()
			h.handlers.Done()
		}()
		return f(ctx, raw)
	}
}

func (h *Harness) initialize(ctx context.Context, o Options) error {
	info := o.ClientInfo
	if info.Name == "" {
		info.Name = "dsh-go-sdk"
	}
	p := protocol.InitializeParams{
		ProtocolVersions: []int{protocol.Version},
		ClientInfo:       info,
		TimeZone:         o.TimeZone,
		Capabilities:     protocol.ClientCapabilities{Approvals: o.Handlers.Approval != nil, Questions: o.Handlers.Question != nil},
		Defaults:         o.Defaults,
	}
	if err := h.conn.Call(ctx, protocol.InitializeMethod, p, &h.info); err != nil {
		return fmt.Errorf("sdk: initialize: %w", err)
	}
	if h.info.ProtocolVersion != protocol.Version {
		return fmt.Errorf("sdk: the host answered protocol version %d, this client speaks %d", h.info.ProtocolVersion, protocol.Version)
	}
	return nil
}

// Info is the host's initialize result.
func (h *Harness) Info() protocol.InitializeResult { return h.info }

// Done is closed when the connection has ended.
func (h *Harness) Done() <-chan struct{} { return h.conn.Done() }

// Err is why the connection ended, or nil while it runs.
func (h *Harness) Err() error { return h.conn.Err() }

// Pid is the child's process id, or 0 for a connection without a child.
func (h *Harness) Pid() int {
	if h.cmd == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

// Ending is how the child ended; valid once Close has returned.
func (h *Harness) Ending() Ending {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ending
}

// Close runs the close ladder once: `shutdown` and the end of the host's input,
// then a wait of ShutdownGrace; terminate (SIGTERM; nothing to send on
// Windows) and a wait; kill and a wait. Ending ctx skips to the kill. It always
// releases the pipes it owns, and returns an error for a failed rung, a child
// still alive after the kill wait, or handlers still running.
func (h *Harness) Close(ctx context.Context) error {
	h.closeOnce.Do(func() { h.closeErr = h.close(ctx) })
	return h.closeErr
}

func (h *Harness) close(ctx context.Context) error {
	errs := []error{h.rung(ctx, RungShutdown, func() error { return h.shutdown(ctx) })}
	if h.cmd != nil {
		errs = append(errs, h.rung(ctx, RungTerminate, h.terminate), h.rung(ctx, RungKill, h.kill))
		select {
		case <-h.exited:
		default:
			errs = append(errs, fmt.Errorf("sdk: pid %d did not exit after the close ladder", h.cmd.Process.Pid))
		}
	}
	h.mu.Lock()
	h.closing = true
	h.mu.Unlock()
	errs = append(errs, h.conn.Close())
	if h.ownIn != nil {
		errs = append(errs, h.ownIn.Close())
	}
	if n := h.stragglers(); n > 0 {
		errs = append(errs, fmt.Errorf("sdk: %d handler(s) still running after close; a handler that ignores its context cannot be stopped", n))
	}
	return errors.Join(errs...)
}

// shutdown asks the host to stop, unless the connection has already ended, and
// always closes the host's input, which the client owns either way.
func (h *Harness) shutdown(ctx context.Context) error {
	var err error
	select {
	case <-h.conn.Done():
	default:
		sctx, cancel := context.WithTimeout(ctx, h.grace)
		err = h.conn.Call(sctx, "shutdown", protocol.Empty{}, nil)
		cancel()
		if err != nil && !jsonrpc.HasReason(err, jsonrpc.ReasonClosed) {
			err = fmt.Errorf("sdk: shutdown: %w", err)
		} else {
			err = nil
		}
	}
	if cerr := h.out.Close(); cerr != nil {
		err = errors.Join(err, fmt.Errorf("sdk: close the host's input: %w", cerr))
	}
	return err
}

// rung acts, then waits up to the grace for the child to exit, and records the
// rung it exited in. Only the shutdown rung acts on a child already gone (it
// releases the host's input); a signal to a child that exited meanwhile
// (os.ErrProcessDone) is no failure. A signal that fails is returned without
// the wait. The terminate wait also ends with ctx; the kill wait does not, so
// Close returns once the child is gone or the grace says it will not go.
// Without a child only the act runs.
func (h *Harness) rung(ctx context.Context, r Rung, act func() error) error {
	if h.cmd == nil {
		return act()
	}
	select {
	case <-h.exited:
		if r == RungShutdown {
			return act()
		}
		return nil
	default:
	}
	err := act()
	switch {
	case errors.Is(err, errNothingToSend), errors.Is(err, os.ErrProcessDone):
		err = nil
		if r != RungKill {
			return nil
		}
	case err != nil && r != RungShutdown:
		return fmt.Errorf("sdk: %s pid %d: %w", r, h.cmd.Process.Pid, err)
	}
	var stop <-chan struct{}
	if r != RungKill {
		stop = ctx.Done()
	}
	timer := time.NewTimer(h.grace)
	defer timer.Stop()
	select {
	case <-h.exited:
		h.mu.Lock()
		h.ending.Rung = r
		h.mu.Unlock()
	case <-timer.C:
	case <-stop:
	}
	return err
}

// stragglers waits up to the grace for handlers to return and counts the rest.
// Admission is closed by then, so no handler starts after the wait begins.
func (h *Harness) stragglers() int {
	done := make(chan struct{})
	go func() { h.handlers.Wait(); close(done) }()
	timer := time.NewTimer(h.grace)
	defer timer.Stop()
	select {
	case <-done:
		return 0
	case <-timer.C:
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.running
}

// errNothingToSend is a terminate rung with no signal on this platform.
var errNothingToSend = errors.New("sdk: no terminate signal on this platform")
