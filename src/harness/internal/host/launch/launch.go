// Package launch is host.launch (DH-D-HOST-3, docs/harness/areas/host-telemetry.md
// §6.1, "Reaching the host"): how a person, or a second dsh, reaches a listening
// host.
//
// Once the listener is ready, the host mints a launch link, prints it on its own
// terminal (never through the logger) and, unless disabled, started through SSH or
// without a display, hands it to the system browser. It writes the runtime record
// <home>/run/web-<hash of origin>.json while holding a kernel lock on the sibling
// .lock file, and serves the control endpoint on a Unix domain socket in the same
// owner-only directory: newline-delimited JSON-RPC (src/sdk/jsonrpc) with a closed
// set of three methods, host.status, host.stop and launch.open, none of which
// returns a credential. Every connection passes the peer rule before a request is
// read. Only a profile that binds a listener loads this component, so headless and
// sdk processes write no record (M2-C-13).
package launch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// Reasons this package raises.
const (
	// ReasonConfig: the entry config is unusable (no run directory).
	ReasonConfig herr.Reason = "launch/config"
	// ReasonTaken: another live host holds the record of this home and origin.
	ReasonTaken herr.Reason = "launch/taken"
	// ReasonIO: the record, its lock or the socket could not be made.
	ReasonIO herr.Reason = "launch/io"
	// ReasonNoChannel: the host has neither a terminal to print a link on nor a
	// display to open one on.
	ReasonNoChannel herr.Reason = "launch/no-channel"
	// ReasonNoStop: host.stop was asked of a host composed without a stop function.
	ReasonNoStop herr.Reason = "launch/no-stop"
	// ReasonPrint: the host's terminal refused the launch link.
	ReasonPrint herr.Reason = "launch/print-failed"
)

// The control endpoint's methods: the complete, closed set.
const (
	MethodStatus = "host.status"
	MethodStop   = "host.stop"
	MethodOpen   = "launch.open"
)

// How launch.open reached the person.
const (
	Opened  = "opened"
	Printed = "printed"
)

// maxFrame bounds one control-endpoint line; every request is a few bytes.
const maxFrame = 64 << 10

// Config is host.launch's entry config (host-telemetry.md §6.2).
type Config struct {
	// RunDir is the home's runtime directory, home.Layout.Run(); required.
	RunDir string `json:"runDir,omitempty"`
	// Open hands the link to the system browser; nil is true.
	Open *bool `json:"open,omitempty"`
	// Print writes the link on the host's terminal; nil is true.
	Print *bool `json:"print,omitempty"`
	// Profile and Version are written into the runtime record.
	Profile string `json:"profile,omitempty"`
	Version string `json:"version,omitempty"`
}

func (c Config) open() bool  { return c.Open == nil || *c.Open }
func (c Config) print() bool { return c.Print == nil || *c.Print }

// Record is the runtime record: what a second dsh reads to find this host. It
// holds no credential.
type Record struct {
	Origin    string    `json:"origin"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"startedAt"`
	Version   string    `json:"version,omitempty"`
	Profile   string    `json:"profile,omitempty"`
	Socket    string    `json:"socket"`
}

// Paths are the runtime record, its lock and the control socket of one origin.
type Paths struct{ Record, Lock, Socket string }

// PathsFor names the files of origin's host under runDir. The hash keeps the names
// short, so the socket path fits the system's limit.
func PathsFor(runDir, origin string) Paths {
	sum := sha256.Sum256([]byte(origin))
	base := filepath.Join(runDir, "web-"+hex.EncodeToString(sum[:6]))
	return Paths{Record: base + ".json", Lock: base + ".lock", Socket: base + ".sock"}
}

// Work is the summary of running work that host.status and host.stop report.
type Work struct {
	// Commands is the number of live process trees the host started.
	Commands int `json:"commands"`
}

// Status is host.status's reply.
type Status struct {
	Origin    string    `json:"origin"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"startedAt"`
	UptimeMS  int64     `json:"uptimeMs"`
	Profile   string    `json:"profile,omitempty"`
	Version   string    `json:"version,omitempty"`
	Work      Work      `json:"work"`
}

// StopParams are host.stop's parameters: Ask returns the work summary and stops
// nothing.
type StopParams struct {
	Ask bool `json:"ask,omitempty"`
}

// StopResult is host.stop's reply.
type StopResult struct {
	Stopping bool `json:"stopping"`
	Work     Work `json:"work"`
}

// OpenParams are launch.open's parameters: Print prints the link on the host's
// terminal and opens no browser (a second dsh started with --no-open).
type OpenParams struct {
	Print bool `json:"print,omitempty"`
}

// OpenResult is launch.open's reply: only which channel was used, never the link.
type OpenResult struct {
	How string `json:"how"`
}

// Opener hands one URL to the system browser.
type Opener func(ctx context.Context, url string) error

// Options are what Start needs besides the config; the component fills them from
// the tree.
type Options struct {
	Config Config
	// Origin is the listener's origin.
	Origin string
	// Mint returns a fresh launch link (auth.Service.Mint).
	Mint func() (string, error)
	// Ready closes when boot reports the required entries loaded; nil is ready.
	Ready <-chan struct{}
	// Stop ends the process (boot's App.Stop(Requested, 0)); nil refuses host.stop.
	Stop func()
	// Work summarises running work; nil is none.
	Work func() Work
	// Trees names the process tree behind a connection; nil applies no peer rule.
	Trees listen.Trees
	// Terminal is the host's terminal; nil has none.
	Terminal io.Writer
	// Opener opens the browser; nil is the system browser.
	Opener Opener
	// Getenv reads the launch environment (SSH, display); nil is os.Getenv.
	Getenv func(string) string
	// Warn reports a failure that does not stop the host; nil drops it.
	Warn func(format string, args ...any)
}

// Host is a running host.launch: it holds the record's lock and serves the socket.
type Host struct {
	o       Options
	paths   Paths
	started time.Time
	lock    *fsx.FileLock
	ln      net.Listener
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	once    sync.Once
	// unprotect releases this host's control-socket path from subprocess's
	// protected set; other hosts' paths stay protected.
	unprotect func()
}

// Start takes the record's lock, replaces a stale record, listens on the control
// socket and writes the record; then, once Ready closes, it prints the link and
// hands it to the browser. A record whose lock another live host holds is
// launch/taken. ctx bounds the announcement and every request.
func Start(ctx context.Context, o Options) (*Host, error) {
	if o.Config.RunDir == "" {
		return nil, herr.New(ReasonConfig, "launch: no run directory")
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Opener == nil {
		o.Opener = openBrowser
	}
	if o.Warn == nil {
		o.Warn = func(string, ...any) {}
	}
	if o.Work == nil {
		o.Work = func() Work { return Work{} }
	}
	if err := home.EnsurePrivateDir(o.Config.RunDir); err != nil {
		return nil, err
	}
	h := &Host{o: o, paths: PathsFor(o.Config.RunDir, o.Origin), started: time.Now(), conns: map[net.Conn]struct{}{}}
	lock, err := fsx.TryLock(h.paths.Lock)
	if errors.Is(err, fsx.ErrLocked) {
		return nil, herr.Newf(ReasonTaken, "launch: another host already serves %s on this home", o.Origin)
	}
	if err != nil {
		return nil, herr.Wrap(ReasonIO, err, "launch: lock the runtime record")
	}
	h.lock = lock
	// With the lock held, a socket or record left here is a dead host's.
	if err := removeIfPresent(h.paths.Socket); err != nil {
		_ = lock.Unlock()
		return nil, herr.Wrap(ReasonIO, err, "launch: remove a stale control socket")
	}
	if h.ln, err = net.Listen("unix", h.paths.Socket); err != nil {
		_ = lock.Unlock()
		return nil, herr.Wrap(ReasonIO, err, "launch: listen on the control socket")
	}
	h.unprotect = subprocess.ProtectControlSocket(h.paths.Socket)
	h.ctx, h.cancel = context.WithCancel(ctx)
	if err := h.writeRecord(); err != nil {
		_ = h.Close()
		return nil, err
	}
	h.wg.Add(2)
	go h.accept()
	go h.announce()
	return h, nil
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (h *Host) writeRecord() error {
	b, err := json.Marshal(Record{Origin: h.o.Origin, PID: os.Getpid(), StartedAt: h.started.UTC(),
		Version: h.o.Config.Version, Profile: h.o.Config.Profile, Socket: h.paths.Socket})
	if err != nil {
		return herr.Wrap(ReasonIO, err, "launch: encode the runtime record")
	}
	if err := fsx.WriteFile(h.paths.Record, b, fsx.Options{Perm: 0o600}); err != nil {
		return herr.Wrap(ReasonIO, err, "launch: write the runtime record")
	}
	return nil
}

// Paths are this host's record, lock and socket.
func (h *Host) Paths() Paths { return h.paths }

// Close stops serving, then removes the record before releasing its lock, so a
// reader never sees a free lock beside a clean host's record. A second Close is a
// no-op.
func (h *Host) Close() error {
	var err error
	h.once.Do(func() {
		h.cancel()
		_ = h.ln.Close()
		h.mu.Lock()
		for c := range h.conns {
			_ = c.Close()
		}
		h.mu.Unlock()
		h.wg.Wait()
		h.unprotect()
		err = errors.Join(removeIfPresent(h.paths.Record), removeIfPresent(h.paths.Socket), h.lock.Unlock())
	})
	return err
}

// channels decides, from the config and the launch environment, whether the host
// may print a link and whether it may hand one to the browser (DH-LIB-OPEN-R5).
func channels(cfg Config, terminal io.Writer, getenv func(string) string, goos string) (canPrint, canOpen bool) {
	canPrint = cfg.print() && terminal != nil
	ssh := getenv("SSH_CONNECTION") != "" || getenv("SSH_CLIENT") != "" || getenv("SSH_TTY") != ""
	display := goos == "windows" || goos == "darwin" || getenv("DISPLAY") != "" || getenv("WAYLAND_DISPLAY") != ""
	return canPrint, cfg.open() && !ssh && display
}

func (h *Host) channels() (bool, bool) {
	return channels(h.o.Config, h.o.Terminal, h.o.Getenv, runtime.GOOS)
}

// announce waits for Ready, then prints the link and opens the browser.
func (h *Host) announce() {
	defer h.wg.Done()
	if h.o.Ready != nil {
		select {
		case <-h.o.Ready:
		case <-h.ctx.Done():
			return
		}
	}
	canPrint, canOpen := h.channels()
	if !canPrint && !canOpen {
		return
	}
	link, err := h.o.Mint()
	if err != nil {
		h.o.Warn("launch: mint a launch link: %v", err)
		return
	}
	if canPrint {
		if err := h.print(link); err != nil {
			h.o.Warn("launch: the launch link could not be printed (%s)", herr.Kind(err))
		}
	}
	if canOpen {
		h.open(link)
	}
}

// print writes link on the host's terminal. The error never carries the link: the
// writer's own error is reduced to its kind.
func (h *Host) print(link string) error {
	if _, err := fmt.Fprintf(h.o.Terminal, "dsh: open %s\n", link); err != nil {
		return herr.Newf(ReasonPrint, "launch: the host's terminal refused the launch link (%s)", herr.Kind(err))
	}
	return nil
}

// open hands link to the browser; a failure warns without the link
// (DH-LIB-OPEN-R4).
func (h *Host) open(link string) bool {
	if err := h.o.Opener(h.ctx, link); err != nil {
		h.o.Warn("launch: the browser could not be opened (%s); use the link printed on the host's terminal", herr.Kind(err))
		return false
	}
	return true
}

// Open is launch.open: the host mints a link itself and opens or prints it. The
// caller learns only which happened.
func (h *Host) Open(printOnly bool) (string, error) {
	canPrint, canOpen := h.channels()
	if !canPrint && (printOnly || !canOpen) {
		return "", herr.New(ReasonNoChannel, "launch: the host has neither a terminal nor a display")
	}
	link, err := h.o.Mint()
	if err != nil {
		return "", err
	}
	if !printOnly && canOpen && h.open(link) {
		return Opened, nil
	}
	if !canPrint {
		return "", herr.New(ReasonNoChannel, "launch: the browser could not be opened and the host has no terminal")
	}
	if err := h.print(link); err != nil {
		return "", err
	}
	return Printed, nil
}

// Status is host.status.
func (h *Host) Status() Status {
	return Status{Origin: h.o.Origin, PID: os.Getpid(), StartedAt: h.started.UTC(),
		UptimeMS: time.Since(h.started).Milliseconds(), Profile: h.o.Config.Profile,
		Version: h.o.Config.Version, Work: h.o.Work()}
}

// Stop is host.stop: with ask it returns the work summary and stops nothing.
func (h *Host) Stop(ask bool) (StopResult, error) {
	r := StopResult{Work: h.o.Work()}
	if ask {
		return r, nil
	}
	if h.o.Stop == nil {
		return r, herr.New(ReasonNoStop, "launch: this host has no stop function")
	}
	h.o.Stop()
	r.Stopping = true
	return r, nil
}

func (h *Host) accept() {
	defer h.wg.Done()
	for {
		c, err := h.ln.Accept()
		if err != nil {
			return
		}
		h.mu.Lock()
		if h.ctx.Err() != nil {
			h.mu.Unlock()
			_ = c.Close()
			return
		}
		h.conns[c] = struct{}{}
		h.wg.Add(1)
		h.mu.Unlock()
		go h.serve(c)
	}
}

// admit is the peer rule of the control endpoint: a peer that cannot be named, or
// that is inside an untrusted tree, is refused before a request is read.
func (h *Host) admit(c net.Conn) bool {
	if h.o.Trees == nil {
		return true
	}
	tree, err := h.o.Trees.PeerTree(c)
	if err != nil {
		h.o.Warn("launch: a control connection was refused: %v", err)
		return false
	}
	return tree.Trust != subprocess.Untrusted
}

func (h *Host) serve(c net.Conn) {
	defer func() {
		_ = c.Close()
		h.mu.Lock()
		delete(h.conns, c)
		h.mu.Unlock()
		h.wg.Done()
	}()
	if !h.admit(c) {
		return
	}
	rc := jsonrpc.Serve(h.ctx, c, c, jsonrpc.Options{MaxFrameBytes: maxFrame, MaxInFlight: 4, Methods: h.methods()})
	select {
	case <-rc.Done():
	case <-h.ctx.Done():
		_ = rc.Close()
	}
}

// methods is the closed method set; nothing outside this package can add one.
func (h *Host) methods() map[string]jsonrpc.Handler {
	return map[string]jsonrpc.Handler{
		MethodStatus: func(context.Context, json.RawMessage) (any, error) { return h.Status(), nil },
		MethodStop: func(_ context.Context, raw json.RawMessage) (any, error) {
			var p StopParams
			if err := decode(raw, &p); err != nil {
				return nil, err
			}
			return h.Stop(p.Ask)
		},
		MethodOpen: func(_ context.Context, raw json.RawMessage) (any, error) {
			var p OpenParams
			if err := decode(raw, &p); err != nil {
				return nil, err
			}
			how, err := h.Open(p.Print)
			if err != nil {
				return nil, err
			}
			return OpenResult{How: how}, nil
		},
	}
}

func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Reason: jsonrpc.ReasonInvalidParams, Message: "the parameters do not decode"}
	}
	return nil
}
