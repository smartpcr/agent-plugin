package hostclient_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/hostclient"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

const origin = "http://127.0.0.1:3080"

func found(o string, s hostclient.State, pid int) hostclient.Found {
	return hostclient.Found{Record: launch.Record{Origin: o, PID: pid}, State: s}
}

// The second-launch decision: a live host on the origin is reused, refused when a
// listener flag is given, reported when it does not answer; another origin, an
// unknown one or a stale record starts a host, and every stale record is listed.
//
//dsh:test DH-T-CLI-7
func TestDecide(t *testing.T) {
	live := found(origin, hostclient.Live, 10)
	stale := found(origin, hostclient.Stale, 11)
	other := found("http://127.0.0.1:4000", hostclient.Live, 12)
	cases := []struct {
		name  string
		found []hostclient.Found
		want  hostclient.Want
		kind  hostclient.Kind
		pid   int
		stale int
	}{
		{"none", nil, hostclient.Want{Origin: origin}, hostclient.Start, 0, 0},
		{"reuse", []hostclient.Found{other, live}, hostclient.Want{Origin: origin}, hostclient.Reuse, 10, 0},
		{"refuse", []hostclient.Found{live}, hostclient.Want{Origin: origin, Changes: []string{"--tls-cert"}}, hostclient.Refuse, 10, 0},
		{"other address", []hostclient.Found{other}, hostclient.Want{Origin: origin}, hostclient.Start, 0, 0},
		{"unknown origin", []hostclient.Found{live}, hostclient.Want{}, hostclient.Start, 0, 0},
		{"stale replaced", []hostclient.Found{stale}, hostclient.Want{Origin: origin}, hostclient.Start, 0, 1},
		{"unreachable", []hostclient.Found{stale, found(origin, hostclient.Unreachable, 13)}, hostclient.Want{Origin: origin}, hostclient.Unreach, 13, 1},
	}
	for _, c := range cases {
		a := hostclient.Decide(c.found, c.want)
		if a.Kind != c.kind || a.Host.Record.PID != c.pid || len(a.Stale) != c.stale {
			t.Errorf("%s: Decide = kind %d pid %d stale %d, want %d %d %d", c.name, a.Kind, a.Host.Record.PID, len(a.Stale), c.kind, c.pid, c.stale)
		}
	}
	for _, flag := range hostclient.ListenerFlags {
		if a := hostclient.Decide([]hostclient.Found{live}, hostclient.Want{Origin: origin, Changes: hostclient.Changes([]string{flag, "v"})}); a.Kind != hostclient.Refuse {
			t.Errorf("%s on the live origin = kind %d, want a refusal", flag, a.Kind)
		}
	}
	refuse := hostclient.Decide([]hostclient.Found{live}, hostclient.Want{Origin: origin, Changes: []string{"--public-url"}})
	if err := refuse.Err(); herr.Kind(err) != hostclient.ReasonAlreadyRunning || !strings.Contains(err.Error(), "process 10") || !strings.Contains(err.Error(), "--public-url") {
		t.Errorf("refusal = %v", err)
	}
	unreach := hostclient.Decide([]hostclient.Found{found(origin, hostclient.Unreachable, 13)}, hostclient.Want{Origin: origin})
	if err := unreach.Err(); herr.Kind(err) != hostclient.ReasonUnreachable || !strings.Contains(err.Error(), "process 13") {
		t.Errorf("unreachable = %v", err)
	}
	got := hostclient.Changes([]string{"--safe", "--tls-key=k.pem", "--trusted-host", "a", "--tls-key", "x"})
	if !slices.Equal(got, []string{"--tls-key", "--trusted-host"}) {
		t.Errorf("Changes = %v", got)
	}
}

func writeRecord(t *testing.T, runDir string, r launch.Record) launch.Paths {
	t.Helper()
	p := launch.PathsFor(runDir, r.Origin)
	r.Socket = p.Socket
	b, _ := json.Marshal(r)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Record, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A record with a free lock is stale and explained once: the report is written for
// the dead process and the same path is named at every later start, with --safe.
// A record whose lock is held but whose endpoint is silent is unreachable.
//
//dsh:test DH-T-CLI-7
func TestListStaleAndExplain(t *testing.T) {
	l := temphome.New(t)
	started := time.Now().Add(-time.Minute)
	writeRecord(t, l.Run(), launch.Record{Origin: origin, PID: 4242, StartedAt: started, Profile: "web", Version: "v1"})
	held := writeRecord(t, l.Run(), launch.Record{Origin: "http://127.0.0.1:4000", PID: 4343})
	lock, err := fsx.TryLock(held.Lock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock() }()

	fs, err := hostclient.List(context.Background(), l.Run())
	if err != nil || len(fs) != 2 {
		t.Fatalf("List = %v %v, want 2 records", fs, err)
	}
	states := map[int]hostclient.State{fs[0].Record.PID: fs[0].State, fs[1].Record.PID: fs[1].State}
	if states[4242] != hostclient.Stale || states[4343] != hostclient.Unreachable {
		t.Fatalf("states = %v", states)
	}
	a := hostclient.Decide(fs, hostclient.Want{})
	n := hostclient.Explain(l, a.Stale[0], time.Now())
	b, err := os.ReadFile(n.Report)
	if err != nil || !strings.Contains(string(b), "pid:     4242") || filepath.Dir(n.Report) != l.Reports() {
		t.Fatalf("report %s = %q %v", n.Report, b, err)
	}
	if line := n.Line(); !strings.Contains(line, n.Report) || !strings.Contains(line, "--safe") || !strings.Contains(line, "process 4242") {
		t.Errorf("notice = %q", line)
	}
	if again := hostclient.Explain(l, a.Stale[0], time.Now().Add(2*time.Second)); again.Report != n.Report {
		t.Errorf("second notice names %s, want the first report %s", again.Report, n.Report)
	}
}

// A record that cannot be read or decoded is left out and named, by its path and
// reason, in List's error; the readable records are still returned, and a record
// removed before it is read is a clean exit, not a failure.
//
//dsh:test DH-T-CLI-7
func TestListNamesBadRecords(t *testing.T) {
	for name, c := range map[string]struct {
		make   func(t *testing.T, path string)
		reason herr.Reason
	}{
		"malformed":  {func(t *testing.T, p string) { temphome.Write(t, p, "{not json") }, hostclient.ReasonBadRecord},
		"unreadable": {func(t *testing.T, p string) { _ = os.MkdirAll(p, 0o700) }, hostclient.ReasonRecord},
	} {
		t.Run(name, func(t *testing.T) {
			l := temphome.New(t)
			writeRecord(t, l.Run(), launch.Record{Origin: origin, PID: 4242})
			bad := filepath.Join(l.Run(), "web-bad.json")
			c.make(t, bad)
			fs, err := hostclient.List(context.Background(), l.Run())
			if len(fs) != 1 || fs[0].Record.PID != 4242 {
				t.Errorf("List kept %+v, want the readable record", fs)
			}
			if herr.Kind(err) != c.reason || !strings.Contains(err.Error(), bad) {
				t.Errorf("List error = %v, want %s naming %s", err, c.reason, bad)
			}
		})
	}
	if fs, err := hostclient.List(context.Background(), filepath.Join(t.TempDir(), "none")); err != nil || len(fs) != 0 {
		t.Errorf("List of a missing run dir = %v %v", fs, err)
	}
}

// A live host answers host.status and launch.open through the client; a host that
// has neither channel is launch/no-channel; a clean exit leaves no record.
//
//dsh:test DH-T-CLI-7
func TestLiveHost(t *testing.T) {
	l := temphome.New(t)
	env := map[string]string{"DISPLAY": ":0"}
	opened := make(chan string, 4)
	h, err := launch.Start(context.Background(), launch.Options{
		Config: launch.Config{RunDir: l.Run(), Profile: "web"}, Origin: origin,
		Mint:   func() (string, error) { return origin + "/#t=secret", nil },
		Opener: func(_ context.Context, url string) error { opened <- url; return nil },
		Getenv: func(k string) string { return env[k] },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	<-opened // the start's own announcement

	fs, err := hostclient.List(context.Background(), l.Run())
	if err != nil || len(fs) != 1 || fs[0].State != hostclient.Live || fs[0].Status.PID != os.Getpid() || fs[0].Status.Origin != origin {
		t.Fatalf("List = %+v %v", fs, err)
	}
	a := hostclient.Decide(fs, hostclient.Want{Origin: origin})
	how, err := hostclient.Open(context.Background(), a.Host, false)
	if err != nil || how != launch.Opened || len(opened) != 1 {
		t.Fatalf("Open = %q %v, opens %d", how, err, len(opened))
	}
	if w := hostclient.Where(how, a.Host.Record.PID); strings.Contains(w, "secret") || !strings.Contains(w, "opened") {
		t.Errorf("Where = %q", w)
	}
	if _, err := hostclient.Open(context.Background(), a.Host, true); herr.Kind(err) != launch.ReasonNoChannel {
		t.Errorf("print-only Open without a terminal = %v, want %s", err, launch.ReasonNoChannel)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if fs, err := hostclient.List(context.Background(), l.Run()); err != nil || len(fs) != 0 {
		t.Errorf("List after a clean stop = %+v %v", fs, err)
	}
}
