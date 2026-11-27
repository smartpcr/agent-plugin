// Package hostclient is the client half of host reuse (DH-D-CLI-5, C52): it lists
// the runtime records of a home, tells a live host from a dead one by its record's
// lock, calls the control endpoint (host.status, launch.open), decides what a
// second launch does (DH-ADR-CLI-4) and explains an unclean exit with its report
// and --safe. It only reads the records; the host (host/launch) owns them.
package hostclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/host/diag"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// Reasons this package raises.
const (
	// ReasonNotRunning: no live host has a record on this home.
	ReasonNotRunning herr.Reason = "host/not-running"
	// ReasonUnreachable: a record's lock is held but its control endpoint does not
	// answer; the process id is named and nothing is replaced.
	ReasonUnreachable herr.Reason = "host/unreachable"
	// ReasonAlreadyRunning: a second launch on the address of a live host asked for
	// a flag that would change its listener.
	ReasonAlreadyRunning herr.Reason = "host/already-running"
	// ReasonNoReport: the home holds no crash report.
	ReasonNoReport herr.Reason = "host/no-report"
)

// CallTimeout bounds one control-endpoint call.
const CallTimeout = 5 * time.Second

// ReadyWait bounds how long Open waits for a host that is still starting.
const ReadyWait = 15 * time.Second

// State is what a record's endpoint and lock say about its host.
type State int

const (
	// Live: the endpoint answered host.status.
	Live State = iota
	// Unreachable: the lock is held, the endpoint does not answer.
	Unreachable
	// Stale: the lock is free and the record is still there: an unclean exit.
	Stale
)

func (s State) String() string {
	switch s {
	case Live:
		return "live"
	case Unreachable:
		return "unreachable"
	}
	return "stale"
}

// Found is one runtime record and the state of its host.
type Found struct {
	Path   string
	Record launch.Record
	State  State
	// Status is host.status's reply of a live host.
	Status launch.Status
}

// List reads every runtime record under runDir, in name order, and probes each:
// first the control endpoint, then, when it does not answer, the record's lock. A
// record removed while it is probed was a clean exit and is left out. A missing
// runDir has no records.
func List(ctx context.Context, runDir string) ([]Found, error) {
	paths, err := filepath.Glob(filepath.Join(runDir, "web-*.json"))
	if err != nil {
		return nil, err
	}
	slices.Sort(paths)
	var out []Found
	for _, p := range paths {
		if f, ok := probe(ctx, p); ok {
			out = append(out, f)
		}
	}
	return out, nil
}

func probe(ctx context.Context, path string) (Found, bool) {
	f := Found{Path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		return f, false
	}
	if err := json.Unmarshal(b, &f.Record); err != nil {
		// A host writes its record whole (fsx), so this file is no host's.
		return f, false
	}
	// The endpoint first: testing the lock of a live host would be harmless, but
	// testing it while a host starts could make that start launch/taken.
	if f.Record.Socket != "" {
		if err := Call(ctx, f.Record.Socket, launch.MethodStatus, nil, &f.Status); err == nil {
			f.State = Live
			return f, true
		}
	}
	lock, err := fsx.TryLock(strings.TrimSuffix(path, ".json") + ".lock")
	if err != nil {
		// Held (fsx.ErrLocked), or not testable: a host may be behind it.
		f.State = Unreachable
		return f, true
	}
	defer func() { _ = lock.Unlock() }()
	// A clean host removes its record before it frees the lock.
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return f, false
	}
	f.State = Stale
	return f, true
}

// Call sends one request to the control endpoint at socket and decodes the reply
// into result. An error the host returned keeps its reason (launch/no-channel,
// launch/not-ready); a connection that cannot be made or answered is
// host/unreachable.
func Call(ctx context.Context, socket, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return herr.Wrap(ReasonUnreachable, err, "the control endpoint does not answer")
	}
	rc := jsonrpc.Serve(ctx, c, c, jsonrpc.Options{})
	defer func() { _ = rc.Close(); _ = c.Close() }()
	err = rc.Call(ctx, method, params, result)
	var je *jsonrpc.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &je) && strings.HasPrefix(je.Reason, "launch/"):
		return herr.New(herr.Reason(je.Reason), je.Message)
	}
	return herr.Wrap(ReasonUnreachable, err, "the control endpoint does not answer")
}

// Open is launch.open on a live host: the host opens its browser or prints a link
// on its own terminal and returns which, never the link. A host still starting
// (launch/not-ready) is asked again until ReadyWait passes.
func Open(ctx context.Context, f Found, printOnly bool) (string, error) {
	deadline := time.Now().Add(ReadyWait)
	for {
		var r launch.OpenResult
		err := Call(ctx, f.Record.Socket, launch.MethodOpen, launch.OpenParams{Print: printOnly}, &r)
		if err == nil {
			return r.How, nil
		}
		if herr.Kind(err) != launch.ReasonNotReady || time.Now().After(deadline) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// ListenerFlags are the web app's flags that would change a running listener; a
// second launch that gives one on the address of a live host is refused
// (DH-ADR-CLI-4 clause b).
var ListenerFlags = []string{"--tls-cert", "--tls-key", "--public-url", "--trusted-host", "--insecure-remote"}

// Changes returns the ListenerFlags among the app words, once each.
func Changes(words []string) []string {
	var out []string
	for _, f := range ListenerFlags {
		if slices.ContainsFunc(words, func(w string) bool { return w == f || strings.HasPrefix(w, f+"=") }) {
			out = append(out, f)
		}
	}
	return out
}

// Want is what a launch that binds a listener asks for.
type Want struct {
	// Origin is the listener origin the launch would bind; "" when it cannot be
	// known before binding (port 0), which never matches a running host.
	Origin string
	// Changes are the ListenerFlags the launch was given.
	Changes []string
}

// Kind is what a launch does.
type Kind int

const (
	// Start: no live host serves the origin; boot the profile (DH-ADR-CLI-4 c, d).
	Start Kind = iota
	// Reuse: call launch.open on Host and exit 0 (a).
	Reuse
	// Refuse: host/already-running naming Host (b).
	Refuse
	// Unreach: host/unreachable naming Host; nothing is replaced.
	Unreach
)

// Action is the decision of Decide.
type Action struct {
	Kind Kind
	// Host is the record the action is about; zero for Start.
	Host Found
	// Changes are the listener flags a Refuse names.
	Changes []string
	// Stale are the dead hosts' records, each explained at this start.
	Stale []Found
}

// Decide is DH-ADR-CLI-4 as a pure function of the records found and the launch:
// a live host on the same origin is reused, or refused when a listener flag is
// given; one that does not answer is reported; anything else starts a host, which
// replaces a stale record of its origin. Every stale record is listed for its
// notice.
func Decide(found []Found, want Want) Action {
	var a Action
	for _, f := range found {
		if f.State == Stale {
			a.Stale = append(a.Stale, f)
			continue
		}
		if a.Kind != Start || want.Origin == "" || f.Record.Origin != want.Origin {
			continue
		}
		a.Host = f
		switch {
		case f.State == Unreachable:
			a.Kind = Unreach
		case len(want.Changes) > 0:
			a.Kind, a.Changes = Refuse, slices.Clone(want.Changes)
		default:
			a.Kind = Reuse
		}
	}
	return a
}

// Err is the error of a Refuse or Unreach action, naming the host's process; nil
// for the others.
func (a Action) Err() error {
	r := a.Host.Record
	switch a.Kind {
	case Refuse:
		return herr.Newf(ReasonAlreadyRunning, "a host (process %d) already serves %s on this home and %s would change its listener; stop it first",
			r.PID, r.Origin, strings.Join(a.Changes, ", "))
	case Unreach:
		return Unanswered(a.Host)
	}
	return nil
}

// Unanswered is host/unreachable for f, naming its process.
func Unanswered(f Found) error {
	return herr.Newf(ReasonUnreachable, "the host of %s (process %d) holds its record but does not answer on its control endpoint",
		f.Record.Origin, f.Record.PID)
}

// Where is the line a reusing launch prints: where to look, never a link.
func Where(how string, pid int) string {
	if how == launch.Opened {
		return fmt.Sprintf("opened in the browser of the host (process %d)", pid)
	}
	return fmt.Sprintf("the link is on the terminal of the host (process %d)", pid)
}

// Notice is one dead host's explanation: its report and the safe-mode hint.
type Notice struct {
	Origin string `json:"origin"`
	PID    int    `json:"pid"`
	// Report is the crash report's path; "" when none could be written.
	Report string `json:"report"`
}

// Explain returns the notice of the stale record f under the home l: the report
// the dead host wrote, else the one written for it once (diag.UncleanReport).
func Explain(l home.Layout, f Found, now time.Time) Notice {
	r := f.Record
	u := diag.Unclean{Origin: r.Origin, PID: r.PID, Profile: r.Profile, Version: r.Version, StartedAt: r.StartedAt}
	crash := filepath.Join(l.Run(), diag.CrashFile)
	if fi, err := os.Stat(crash); err == nil && fi.Size() > 0 {
		u.CrashFile = crash
	}
	n := Notice{Origin: r.Origin, PID: r.PID}
	n.Report, _ = diag.UncleanReport(l.Reports(), u, now)
	return n
}

// Line is the unclean-exit line printed on the diagnostic stream.
func (n Notice) Line() string {
	report := n.Report
	if report == "" {
		report = "none could be written"
	}
	return fmt.Sprintf("dsh: the host of %s (process %d) did not stop cleanly; report: %s; "+
		"start with --safe (dsh --safe web) to turn plugins and optional bundles off", n.Origin, n.PID, report)
}
