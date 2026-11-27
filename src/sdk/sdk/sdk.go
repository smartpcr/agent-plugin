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
	conn   *jsonrpc.Conn
	out    io.Closer
	grace  time.Duration
	info   protocol.InitializeResult
	cmd    *exec.Cmd
	exited chan struct{}

	handlers sync.WaitGroup
	mu       sync.Mutex
	running  int
	ending   Ending
	runs     map[runKey]chan protocol.RunCompletedNotification

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
	h.cmd, h.exited = cmd, make(chan struct{})
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
	h := &Harness{out: out, grace: o.ShutdownGrace, runs: map[runKey]chan protocol.RunCompletedNotification{}}
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
	return h
}

// handler counts f while it runs, so Close can report handlers that outlive it.
func (h *Harness) handler(f jsonrpc.Handler) jsonrpc.Handler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		h.mu.Lock()
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
// Windows) and a wait; kill. Ending ctx skips to the kill. It returns once the
// child has exited, with an error naming the handlers still running.
func (h *Harness) Close(ctx context.Context) error {
	h.closeOnce.Do(func() { h.closeErr = h.close(ctx) })
	return h.closeErr
}

func (h *Harness) close(ctx context.Context) error {
	var errs []error
	h.rung(ctx, RungShutdown, func() error {
		select {
		case <-h.conn.Done():
		default:
			sctx, cancel := context.WithTimeout(ctx, h.grace)
			err := h.conn.Call(sctx, "shutdown", protocol.Empty{}, nil)
			cancel()
			if err != nil && !jsonrpc.HasReason(err, jsonrpc.ReasonClosed) {
				errs = append(errs, fmt.Errorf("sdk: shutdown: %w", err))
			}
		}
		return h.out.Close()
	})
	if h.cmd != nil {
		h.rung(ctx, RungTerminate, func() error { return terminate(h.cmd.Process) })
		h.rung(ctx, RungKill, h.cmd.Process.Kill)
		<-h.exited
	}
	_ = h.conn.Close()
	if n := h.stragglers(); n > 0 {
		errs = append(errs, fmt.Errorf("sdk: %d handler(s) still running after close; a handler that ignores its context cannot be stopped", n))
	}
	return errors.Join(errs...)
}

// rung acts unless the child is gone, then waits up to the grace (the kill
// until the exit), or until ctx ends, and records the rung the child exited in.
// Without a child only the act runs.
func (h *Harness) rung(ctx context.Context, r Rung, act func() error) {
	if h.cmd == nil {
		_ = act()
		return
	}
	select {
	case <-h.exited:
		return
	default:
	}
	if err := act(); errors.Is(err, errNothingToSend) {
		return
	}
	if r == RungKill {
		<-h.exited
	} else {
		timer := time.NewTimer(h.grace)
		defer timer.Stop()
		select {
		case <-h.exited:
		case <-timer.C:
			return
		case <-ctx.Done():
			return
		}
	}
	h.mu.Lock()
	h.ending.Rung = r
	h.mu.Unlock()
}

// stragglers waits up to the grace for handlers to return and counts the rest.
// The connection is closed by then, so no handler starts after the wait begins.
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
