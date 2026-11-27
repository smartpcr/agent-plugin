package cli

// Host-client commands and the second launch (DH-D-CLI-5, C52): `dsh status` and
// `dsh crash` read the home's runtime records and reports through hostclient; a
// profile launch first explains every dead host's record, and a launch that would
// bind a listener reuses the live host of its origin (DH-ADR-CLI-4).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/cmdline"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/hostclient"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/diag"
	hostlaunch "github.com/smartpcr/agent-plugin/src/harness/internal/host/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// layoutOf resolves the home of req (--home, DSH_HOME, the default).
func layoutOf(req launch.Request) (home.Layout, error) {
	d, _, err := launch.Build(req)
	if err != nil {
		return home.Layout{}, err
	}
	return home.New(d.Home), nil
}

// commandLayout is the home of a fixed command: the launcher's --home resolved.
func (st *state) commandLayout() (home.Layout, error) {
	if st.env.probeErr != nil {
		return home.Layout{}, st.env.probeErr
	}
	req := st.env.Request
	req.Args, req.Home, req.ReadFile = slices.Clone(req.Args), st.home, nil
	return layoutOf(req)
}

// explain prints the unclean-exit line of every stale record on stderr and returns
// the notices.
func explain(l home.Layout, stale []hostclient.Found, stderr io.Writer) []hostclient.Notice {
	var out []hostclient.Notice
	for _, f := range stale {
		n := hostclient.Explain(l, f, time.Now())
		_, _ = fmt.Fprintln(stderr, n.Line())
		out = append(out, n)
	}
	return out
}

// warnRecords prints the records List could not read, each naming its path; the
// command or launch goes on with the records it could.
func warnRecords(stderr io.Writer, err error) {
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "dsh: warning: %s\n", strings.Join(strings.Fields(err.Error()), " "))
	}
}

// statusHost is one host of `dsh status --json`; its fields are part of the
// contract.
type statusHost struct {
	State string `json:"state"`
	hostlaunch.Status
}

// statusDoc is the JSON form of `dsh status --json`.
type statusDoc struct {
	Hosts   []statusHost        `json:"hosts"`
	Unclean []hostclient.Notice `json:"unclean"`
}

// runStatus is `dsh status`: the hosts of the home, and the unclean-exit line of
// every dead one. Every host whose record is held is listed, the one that does not
// answer with state "unreachable". No host at all is host/not-running; a host that
// does not answer is host/unreachable naming its process, and a record that cannot
// be read host/record or host/bad-record naming its path: the text lines or the
// JSON document are written first, and the status is the failure's, in both
// output modes.
func runStatus(st *state, _ *cmdline.Values) error {
	l, err := st.commandLayout()
	if err != nil {
		return err
	}
	found, failure := hostclient.List(st.ctx, l.Run())
	doc := statusDoc{Hosts: []statusHost{}, Unclean: []hostclient.Notice{}}
	var stale []hostclient.Found
	for _, f := range found {
		switch f.State {
		case hostclient.Live:
			doc.Hosts = append(doc.Hosts, statusHost{State: f.State.String(), Status: f.Status})
		case hostclient.Unreachable:
			r := f.Record
			doc.Hosts = append(doc.Hosts, statusHost{State: f.State.String(), Status: hostlaunch.Status{
				Origin: r.Origin, PID: r.PID, StartedAt: r.StartedAt, Profile: r.Profile, Version: r.Version}})
			if failure == nil {
				failure = hostclient.Unanswered(f)
			}
		case hostclient.Stale:
			stale = append(stale, f)
		}
	}
	if n := explain(l, stale, st.env.Stderr); n != nil {
		doc.Unclean = n
	}
	if len(doc.Hosts) == 0 {
		if failure != nil {
			return failure
		}
		return herr.New(hostclient.ReasonNotRunning, "no host is running on this home")
	}
	if st.res.JSON() {
		if err := st.res.Value(doc); err != nil {
			return err
		}
	} else {
		for _, h := range doc.Hosts {
			line := fmt.Sprintf("%s\tprocess %d\tprofile %s\t%s", h.Origin, h.PID, h.Profile, h.State)
			if h.State == hostclient.Live.String() {
				line += fmt.Sprintf("\tup %s\tcommands %d", (time.Duration(h.UptimeMS) * time.Millisecond).Round(time.Second), h.Work.Commands)
			}
			if err := st.res.Line(line); err != nil {
				return err
			}
		}
	}
	if failure == nil {
		return nil
	}
	// The result is written; the failure is named on stderr and only its status
	// is returned, so a JSON result stays one document.
	_, _ = fmt.Fprintf(st.env.Stderr, "dsh status: %s\n", strings.Join(strings.Fields(failure.Error()), " "))
	return output.Exit(output.Status(failure))
}

// crashDoc is the JSON form of `dsh crash --json`.
type crashDoc struct {
	Report string `json:"report"`
}

// runCrash is `dsh crash`: the path of the home's newest crash report, after the
// reports of dead hosts were written. None is host/no-report.
func runCrash(st *state, _ *cmdline.Values) error {
	l, err := st.commandLayout()
	if err != nil {
		return err
	}
	found, err := hostclient.List(st.ctx, l.Run())
	warnRecords(st.env.Stderr, err)
	explain(l, hostclient.Decide(found, hostclient.Want{}).Stale, st.env.Stderr)
	names, _ := diag.Reports(l.Reports())
	if len(names) == 0 {
		return herr.New(hostclient.ReasonNoReport, "this home holds no crash report")
	}
	path := filepath.Join(l.Reports(), names[len(names)-1])
	if st.res.JSON() {
		return st.res.Value(crashDoc{Report: path})
	}
	return st.res.Line(path)
}

// beforeLaunch runs before boot: it explains every dead host's record, and when
// the composition binds a listener it decides the second launch. It returns true
// when the launch ends here: a live host on the same origin was asked to open
// itself (status 0), or refused, or did not answer. A home or a composition it
// cannot read leaves the launch to boot, which reports it.
func beforeLaunch(ctx context.Context, l Launch) (bool, error) {
	layout, err := layoutOf(l.Request)
	if err != nil {
		return false, nil
	}
	found, err := hostclient.List(ctx, layout.Run())
	warnRecords(l.Stderr, err)
	if len(found) == 0 {
		return false, nil
	}
	want, listens := listenerWant(ctx, l)
	a := hostclient.Decide(found, want)
	explain(layout, a.Stale, l.Stderr)
	if !listens {
		// headless, sdk and acp never look for a host: they coexist under the
		// session lease.
		return false, nil
	}
	switch a.Kind {
	case hostclient.Start:
		return false, nil
	case hostclient.Reuse:
		how, err := hostclient.Open(ctx, a.Host, slices.Contains(l.Request.Words, "--no-open"))
		if err != nil {
			return true, err
		}
		_, _ = fmt.Fprintf(l.Stderr, "dsh %s: %s\n", l.Request.Profile, hostclient.Where(how, a.Host.Record.PID))
		return true, nil
	}
	return true, a.Err()
}

// listenerWant previews the launch's composition: whether it holds an enabled
// host.listen entry, and the origin that entry would bind. Port 0, a config that
// does not decode, or a preview that fails leaves the origin unknown, so a new
// host starts.
func listenerWant(ctx context.Context, l Launch) (hostclient.Want, bool) {
	p, _ := boot.PreviewLaunch(ctx, boot.Options{Request: l.Request, Profile: l.Profile, Stderr: io.Discard}, false)
	if p == nil || p.Report == nil {
		return hostclient.Want{}, false
	}
	for _, e := range p.Report.Entries() {
		if e.Name != listen.Name || e.Disabled || e.DisabledExpr != "" {
			continue
		}
		want := hostclient.Want{Changes: hostclient.Changes(l.Request.Words)}
		var cfg listen.Config
		if len(e.Config) > 0 && json.Unmarshal(e.Config, &cfg) != nil {
			return want, true
		}
		addr, err := cfg.Address()
		if err != nil || addr.Port() == 0 {
			return want, true
		}
		scheme := "http://"
		if cfg.TLS.CertFile != "" {
			scheme = "https://"
		}
		want.Origin = scheme + addr.String()
		return want, true
	}
	return hostclient.Want{}, false
}
