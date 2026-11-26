package launch_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/auth"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/kv"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	shellenv "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/env"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

var bg = context.Background()

// syncBuf is a terminal the announcement goroutine writes to.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// opener records the URLs handed to the browser.
type opener struct {
	mu   sync.Mutex
	urls []string
	err  error
}

func (o *opener) open(_ context.Context, url string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.urls = append(o.urls, url)
	return o.err
}

func (o *opener) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.urls)
}

func use(t *testing.T, r *cordis.Context, c *cordis.Component, cfg any) *cordis.Fiber {
	t.Helper()
	f, err := r.Use(c, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Dispose)
	if err := f.Wait(); err != nil {
		t.Fatal(err)
	}
	return f
}

func root() *cordis.Context {
	r := cordis.New()
	r.Loggers().ClearExporters()
	return r
}

// listening is a listener profile's tree: a SQLite home, host.listen on
// 127.0.0.1:0, host.auth and host.launch writing under runDir.
func listening(t *testing.T, runDir string, term *syncBuf, op *opener) (*cordis.Context, *cordis.Fiber) {
	t.Helper()
	t.Cleanup(launch.UseOptions(func(o *launch.Options) {
		o.Terminal, o.Opener = term, op.open
		o.Getenv = func(k string) string {
			if k == "DISPLAY" {
				return ":0"
			}
			return ""
		}
	}))
	store, err := sqlite.Open(bg, filepath.Join(t.TempDir(), "dsh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(bg) })
	r := root()
	if _, err := kv.DBKey.Provide(r, kv.DB(store)); err != nil {
		t.Fatal(err)
	}
	if _, err := shellenv.Key.Provide(r, shellenv.NewRegistry(shellenv.Builtins{})); err != nil {
		t.Fatal(err)
	}
	use(t, r, listen.Component, listen.Config{Port: new(uint16(0))})
	use(t, r, auth.Component, auth.Config{})
	f := use(t, r, launch.Component, launch.Config{RunDir: runDir, Profile: "web", Version: "test"})
	return r, f
}

func records(t *testing.T, runDir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(runDir, "web-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for deadline := time.Now().Add(5 * time.Second); !ok(); <-tick.C {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// call sends one JSON-RPC request over the socket and returns the raw reply.
func call(t *testing.T, socket, method, params string) map[string]json.RawMessage {
	t.Helper()
	c, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	req := `{"jsonrpc":"2.0","id":1,"method":"` + method + `"`
	if params != "" {
		req += `,"params":` + params
	}
	if _, err := c.Write([]byte(req + "}\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	var reply map[string]json.RawMessage
	if err := json.Unmarshal(line, &reply); err != nil {
		t.Fatalf("%s: %v in %q", method, err, line)
	}
	return reply
}

// Only a listener profile writes a runtime record; a headless tree, which has no
// listener, leaves host.launch pending and <home>/run/ without one (TC-11b).
//
//dsh:test DH-T-HOST-15
func TestOneRecord(t *testing.T) {
	l := temphome.New(t)
	term, op := &syncBuf{}, &opener{}
	r, f := listening(t, l.Run(), term, op)
	use(t, root(), launch.Component, launch.Config{RunDir: l.Run(), Profile: "headless"})

	got := records(t, l.Run())
	if len(got) != 1 {
		t.Fatalf("records = %v, want exactly the listener's", got)
	}
	l0, err := listen.Key.Get(r)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(got[0])
	if err != nil {
		t.Fatal(err)
	}
	var rec launch.Record
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	want := launch.PathsFor(l.Run(), l0.Origin())
	if got[0] != want.Record || rec.Origin != l0.Origin() || rec.PID != os.Getpid() || rec.Profile != "web" || rec.Socket != want.Socket {
		t.Fatalf("record %s = %+v, want origin %s at %s", got[0], rec, l0.Origin(), want.Record)
	}
	if strings.Contains(strings.ToLower(string(b)), "token") || strings.Contains(string(b), "#") {
		t.Fatalf("record holds a credential: %s", b)
	}

	eventually(t, "the announcement", func() bool { return op.count() == 1 && strings.Contains(term.String(), l0.Origin()) })

	reg, err := shellenv.Key.Get(r)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := reg.Snapshot(bg)
	if err != nil || !slices.Contains(snap, launch.WebURL+"="+l0.Origin()) {
		t.Fatalf("managed snapshot = %v %v, want %s=%s", snap, err, launch.WebURL, l0.Origin())
	}

	f.Dispose()
	if slices.ContainsFunc(reg.List(), func(v shellenv.Var) bool { return v.Name == launch.WebURL }) {
		t.Fatalf("%s still declared after unload", launch.WebURL)
	}
	if left := records(t, l.Run()); len(left) != 0 {
		t.Fatalf("records after unload = %v", left)
	}
	if _, err := os.Stat(want.Socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket after unload: %v", err)
	}
}

// host.status answers with the origin and the pid and carries no credential; no
// method returns the launch link (TC-11b).
//
//dsh:test DH-T-HOST-16
func TestNoCredential(t *testing.T) {
	l := temphome.New(t)
	term, op := &syncBuf{}, &opener{}
	r, _ := listening(t, l.Run(), term, op)
	h, err := launch.Key.Get(r)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the announcement", func() bool { return op.count() == 1 })
	sock := h.Paths().Socket

	reply := call(t, sock, launch.MethodStatus, "")
	var st map[string]any
	if err := json.Unmarshal(reply["result"], &st); err != nil {
		t.Fatalf("status reply %v: %v", reply, err)
	}
	if st["origin"] != h.Status().Origin || st["pid"] != float64(os.Getpid()) {
		t.Fatalf("status = %v", st)
	}
	for k := range st {
		if lk := strings.ToLower(k); strings.Contains(lk, "token") || strings.Contains(lk, "cookie") || strings.Contains(lk, "link") {
			t.Fatalf("status carries %q: %v", k, st)
		}
	}

	reply = call(t, sock, launch.MethodOpen, `{"print":true}`)
	if string(reply["result"]) != `{"how":"printed"}` {
		t.Fatalf("launch.open print = %s", reply["result"])
	}
	reply = call(t, sock, launch.MethodOpen, "")
	if string(reply["result"]) != `{"how":"opened"}` {
		t.Fatalf("launch.open = %s", reply["result"])
	}
	for _, u := range op.urls {
		for _, m := range []string{launch.MethodStatus, launch.MethodOpen} {
			if strings.Contains(string(call(t, sock, m, `{"print":true}`)["result"]), u) {
				t.Fatalf("%s returned the link", m)
			}
		}
	}

	reply = call(t, sock, launch.MethodStop, `{"ask":true}`)
	if string(reply["result"]) != `{"stopping":false,"work":{"commands":0}}` {
		t.Fatalf("host.stop ask = %s", reply["result"])
	}
	reply = call(t, sock, launch.MethodStop, "")
	if !strings.Contains(string(reply["error"]), string(launch.ReasonNoStop)) {
		t.Fatalf("host.stop without a stop function = %s", reply["error"])
	}
	reply = call(t, sock, "session.list", "")
	if !strings.Contains(string(reply["error"]), "method-not-found") {
		t.Fatalf("unknown method = %v", reply)
	}
}

func start(t *testing.T, runDir string, o launch.Options) *launch.Host {
	t.Helper()
	o.Config.RunDir = runDir
	if o.Origin == "" {
		o.Origin = "http://127.0.0.1:1"
	}
	if o.Mint == nil {
		o.Mint = func() (string, error) { return o.Origin + "/#t=secret", nil }
	}
	if o.Opener == nil {
		o.Opener = func(context.Context, string) error { return nil }
	}
	h, err := launch.Start(bg, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

// A record whose lock is held is launch/taken; one left by a dead host is replaced.
func TestTaken(t *testing.T) {
	l := temphome.New(t)
	h := start(t, l.Run(), launch.Options{})
	_, err := launch.Start(bg, launch.Options{Config: launch.Config{RunDir: l.Run()}, Origin: "http://127.0.0.1:1"})
	if herr.Kind(err) != launch.ReasonTaken {
		t.Fatalf("second Start = %v, want %s", err, launch.ReasonTaken)
	}
	p := h.Paths()
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Record, []byte(`{"pid":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	start(t, l.Run(), launch.Options{})
	b, err := os.ReadFile(p.Record)
	if err != nil || !strings.Contains(string(b), `"pid":`+itoa(os.Getpid())) {
		t.Fatalf("stale record not replaced: %s %v", b, err)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

type trees struct {
	tree subprocess.Tree
	err  error
}

func (f trees) PeerTree(net.Conn) (subprocess.Tree, error) { return f.tree, f.err }

// The peer rule: a peer in an untrusted tree, or one that cannot be named, is
// refused before a request is read; one outside every tree is served.
//
//dsh:test DH-T-HOST-31
func TestPeerRule(t *testing.T) {
	for _, c := range []struct {
		name  string
		trees trees
		serve bool
	}{
		{"outside", trees{tree: subprocess.Tree{Trust: subprocess.Outside}}, true},
		{"trusted", trees{tree: subprocess.Tree{Trust: subprocess.Trusted}}, true},
		{"untrusted", trees{tree: subprocess.Tree{Trust: subprocess.Untrusted}}, false},
		{"unknown", trees{err: errors.New("no peer")}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := start(t, temphome.New(t).Run(), launch.Options{Trees: c.trees})
			conn, err := net.Dial("unix", h.Paths().Socket)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			_, _ = conn.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"host.status"}` + "\n"))
			line, err := bufio.NewReader(conn).ReadBytes('\n')
			if served := err == nil && bytes.Contains(line, []byte(`"result"`)); served != c.serve {
				t.Fatalf("served = %v (%q, %v), want %v", served, line, err, c.serve)
			}
		})
	}
}

// host.stop calls the stop function unless it only asks.
func TestStop(t *testing.T) {
	stopped := 0
	h := start(t, temphome.New(t).Run(), launch.Options{Stop: func() { stopped++ },
		Work: func() launch.Work { return launch.Work{Commands: 2} }})
	if r, err := h.Stop(true); err != nil || r.Stopping || r.Work.Commands != 2 || stopped != 0 {
		t.Fatalf("ask = %+v %v (stopped %d)", r, err, stopped)
	}
	if r, err := h.Stop(false); err != nil || !r.Stopping || stopped != 1 {
		t.Fatalf("stop = %+v %v (stopped %d)", r, err, stopped)
	}
}

// launch.open falls back to printing when the browser fails, and fails with no
// channel at all.
func TestOpenFallback(t *testing.T) {
	term := &syncBuf{}
	h := start(t, temphome.New(t).Run(), launch.Options{Terminal: term, Ready: make(chan struct{}),
		Getenv: func(k string) string { return map[string]string{"DISPLAY": ":0"}[k] },
		Opener: func(context.Context, string) error { return herr.New("open/failed", "no browser") }})
	if how, err := h.Open(false); err != nil || how != launch.Printed || !strings.Contains(term.String(), "#t=secret") {
		t.Fatalf("Open = %q %v, terminal %q", how, err, term.String())
	}
	off := false
	none := start(t, temphome.New(t).Run(), launch.Options{Config: launch.Config{Open: &off}, Ready: make(chan struct{})})
	if _, err := none.Open(false); herr.Kind(err) != launch.ReasonNoChannel {
		t.Fatalf("Open with no channel = %v", err)
	}
}

type brokenTerm struct{}

func (brokenTerm) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// A terminal that refuses the link fails launch.open and warns at announcement,
// and neither the error nor the warning carries the link.
func TestPrintFails(t *testing.T) {
	var mu sync.Mutex
	var warns []string
	off := false
	h := start(t, temphome.New(t).Run(), launch.Options{Config: launch.Config{Open: &off}, Terminal: brokenTerm{},
		Warn: func(f string, a ...any) { mu.Lock(); warns = append(warns, fmt.Sprintf(f, a...)); mu.Unlock() }})
	eventually(t, "the announcement warning", func() bool { mu.Lock(); defer mu.Unlock(); return len(warns) == 1 })
	how, err := h.Open(true)
	if herr.Kind(err) != launch.ReasonPrint || how != "" {
		t.Fatalf("Open on a broken terminal = %q %v, want %s", how, err, launch.ReasonPrint)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(err.Error(), "secret") || strings.Contains(warns[0], "secret") {
		t.Fatalf("the link leaked: %v / %q", err, warns[0])
	}
}

// Two hosts on different origins each keep their socket out of child
// environments; closing one leaves the survivor's protected.
func TestTwoHosts(t *testing.T) {
	run := temphome.New(t).Run()
	a := start(t, run, launch.Options{Origin: "http://127.0.0.1:1"})
	b := start(t, run, launch.Options{Origin: "http://127.0.0.1:2"})
	missing := filepath.Join(t.TempDir(), "none")
	spawn := func(sock string) error {
		_, err := subprocess.Start(bg, subprocess.Spec{Argv: []string{missing}, Env: subprocess.Env{"X=" + sock},
			Launch: sandbox.Untrusted("launch test")})
		return err
	}
	pa := a.Paths().Socket
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if herr.Kind(spawn(b.Paths().Socket)) != subprocess.ReasonBadArgument {
		t.Fatal("the surviving host's socket reached a child environment")
	}
	if herr.Kind(spawn(pa)) == subprocess.ReasonBadArgument {
		t.Fatal("the closed host's socket is still protected")
	}
}

func TestChannels(t *testing.T) {
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	off := false
	term := &bytes.Buffer{}
	for _, c := range []struct {
		name            string
		cfg             launch.Config
		term            io.Writer
		getenv          func(string) string
		goos            string
		wantPrint, open bool
	}{
		{"windows", launch.Config{}, term, env(), "windows", true, true},
		{"linux display", launch.Config{}, term, env("WAYLAND_DISPLAY", "w"), "linux", true, true},
		{"linux no display", launch.Config{}, term, env(), "linux", true, false},
		{"ssh", launch.Config{}, term, env("SSH_CONNECTION", "x"), "darwin", true, false},
		{"no-open", launch.Config{Open: &off}, term, env(), "windows", true, false},
		{"no-print", launch.Config{Print: &off}, term, env(), "windows", false, true},
		{"no terminal", launch.Config{}, nil, env(), "windows", false, true},
	} {
		p, o := launch.Channels(c.cfg, c.term, c.getenv, c.goos)
		if p != c.wantPrint || o != c.open {
			t.Errorf("%s: print %v open %v, want %v %v", c.name, p, o, c.wantPrint, c.open)
		}
	}
}

func TestBrowserEnv(t *testing.T) {
	got := launch.BrowserEnv([]string{"PATH=/bin", "DSH_HOME=/h", "Path=x", "OPENAI_API_KEY=k", "DISPLAY=:0", "BAD"})
	if strings.Join(got, ";") != "PATH=/bin;Path=x;DISPLAY=:0" {
		t.Fatalf("env = %v", got)
	}
}
