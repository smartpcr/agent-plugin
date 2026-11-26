//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/auth"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/kv"
)

// hlceTree is one profile booted in-test: the components of its carrier set that
// stand behind host.launch (host.listen and host.auth when the profile binds a
// listener) and host.launch itself, which every tree loads so a tree without a
// listener proves the component stays inert.
type hlceTree struct {
	profile string
	root    *cordis.Context
	fibers  []*cordis.Fiber
	launchF *cordis.Fiber
	db      *sqlite.Store
	l       listen.Listener
	svc     auth.Service
	host    *launch.Host
}

func (t *hlceTree) close() {
	if t == nil {
		return
	}
	for i := len(t.fibers) - 1; i >= 0; i-- {
		t.fibers[i].Dispose()
	}
	t.fibers = nil
	if t.root != nil {
		t.root.Dispose()
		t.root = nil
	}
	if t.db != nil {
		_ = t.db.Close(context.Background())
		t.db = nil
	}
}

func (t *hlceTree) keep(f *cordis.Fiber, err error) (*cordis.Fiber, error) {
	if err != nil {
		return nil, err
	}
	t.fibers = append(t.fibers, f)
	return f, f.Wait()
}

// hlceBindsListener reports whether the built-in profile name requires host.listen.
func hlceBindsListener(name string) (bool, error) {
	def, err := profile.Lookup(name)
	if err != nil {
		return false, err
	}
	return slices.Contains(def.Required, "host.listen"), nil
}

// hlceBoot boots profile name's host plane over the home at layout. The launch
// config turns the browser and the terminal off: an e2e run must neither start the
// system browser nor print a credential into a test log.
func hlceBoot(layout home.Layout, name string) (*hlceTree, error) {
	listens, err := hlceBindsListener(name)
	if err != nil {
		return nil, err
	}
	t := &hlceTree{profile: name, root: cordis.New()}
	t.root.Loggers().ClearExporters()
	t.root.Loggers().AddExporter(&cordis.MemoryExporter{})
	if listens {
		if t.db, err = sqlite.Open(context.Background(), filepath.Join(layout.Root(), name+".db")); err != nil {
			return t, fmt.Errorf("open the SQLite home: %w", err)
		}
		if _, err := kv.DBKey.Provide(t.root, kv.DB(t.db)); err != nil {
			return t, err
		}
		var port uint16
		if _, err := t.keep(t.root.Use(listen.Component, listen.Config{Port: &port, ShutdownGrace: 2 * time.Second})); err != nil {
			return t, fmt.Errorf("load host.listen: %w", err)
		}
		if _, err := t.keep(t.root.Use(auth.Component, auth.Config{})); err != nil {
			return t, fmt.Errorf("load host.auth: %w", err)
		}
		if t.l, err = listen.Key.Get(t.root); err != nil {
			return t, err
		}
		if t.svc, err = auth.Key.Get(t.root); err != nil {
			return t, err
		}
	}
	off := false
	cfg := launch.Config{RunDir: layout.Run(), Open: &off, Print: &off, Profile: name, Version: "e2e"}
	if t.launchF, err = t.keep(t.root.Use(launch.Component, cfg)); err != nil {
		return t, fmt.Errorf("load host.launch in the %s profile: %w", name, err)
	}
	if listens {
		if st := t.launchF.State(); st != cordis.Active {
			return t, fmt.Errorf("host.launch in the %s profile is %v, want active", name, st)
		}
		if t.host, err = launch.Key.Get(t.root); err != nil {
			return t, err
		}
	} else if st := t.launchF.State(); st != cordis.Pending {
		return t, fmt.Errorf("host.launch in the %s profile, which binds no listener, is %v, want pending", name, st)
	}
	return t, nil
}

type hlceState struct {
	home     string
	layout   home.Layout
	listener *hlceTree
	headless *hlceTree
	link     string
	reply    []byte
	result   map[string]json.RawMessage
}

func (s *hlceState) close() {
	s.headless.close()
	s.listener.close()
	if s.home != "" {
		for range 40 {
			if os.RemoveAll(s.home) == nil {
				break
			}
			<-time.After(50 * time.Millisecond)
		}
	}
}

func (s *hlceState) tempHome() error {
	// A short root keeps the control socket's path under the system's limit.
	dir, err := os.MkdirTemp("", "dsh-hlce-")
	if err != nil {
		return err
	}
	s.home = dir
	s.layout = home.New(filepath.Join(dir, "home"))
	return home.Ensure(s.layout)
}

func (s *hlceState) bootBoth(listener, headless string) error {
	var err error
	if s.listener, err = hlceBoot(s.layout, listener); err != nil {
		return err
	}
	if s.listener.l == nil {
		return fmt.Errorf("the %s profile binds no listener", listener)
	}
	if s.headless, err = hlceBoot(s.layout, headless); err != nil {
		return err
	}
	if s.headless.l != nil {
		return fmt.Errorf("the %s profile binds a listener", headless)
	}
	return nil
}

func (s *hlceState) bootListener(name string) error {
	var err error
	if s.listener, err = hlceBoot(s.layout, name); err != nil {
		return err
	}
	if s.listener.host == nil {
		return fmt.Errorf("the %s profile started no host.launch", name)
	}
	return nil
}

func (s *hlceState) records() ([]string, error) {
	return filepath.Glob(filepath.Join(s.layout.Run(), "*.json"))
}

func (s *hlceState) exactlyOneRecord() error {
	got, err := s.records()
	if err != nil {
		return err
	}
	if len(got) != 1 {
		return fmt.Errorf("%s holds runtime records %v, want exactly one", s.layout.Run(), got)
	}
	return nil
}

func (s *hlceState) readRecord() ([]byte, launch.Record, error) {
	var rec launch.Record
	got, err := s.records()
	if err != nil || len(got) != 1 {
		return nil, rec, fmt.Errorf("runtime records %v (%v), want exactly one", got, err)
	}
	b, err := os.ReadFile(got[0])
	if err != nil {
		return nil, rec, err
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return b, rec, fmt.Errorf("decode %s: %w", got[0], err)
	}
	return b, rec, nil
}

func (s *hlceState) recordIsListeners() error {
	_, rec, err := s.readRecord()
	if err != nil {
		return err
	}
	origin := s.listener.l.Origin()
	want := launch.PathsFor(s.layout.Run(), origin)
	got, _ := s.records()
	if got[0] != want.Record {
		return fmt.Errorf("record at %s, want the listener's %s", got[0], want.Record)
	}
	if rec.Origin != origin || rec.PID != os.Getpid() || rec.Socket != want.Socket || rec.Profile != s.listener.profile {
		return fmt.Errorf("record = %+v, want origin %s, pid %d, socket %s, profile %s", rec, origin, os.Getpid(), want.Socket, s.listener.profile)
	}
	if fi, err := os.Stat(rec.Socket); err != nil || fi.IsDir() {
		return fmt.Errorf("the record's control socket %s: %v", rec.Socket, err)
	}
	return nil
}

func hlceCredentialKey(k string) bool {
	lk := strings.ToLower(k)
	return strings.Contains(lk, "token") || strings.Contains(lk, "cookie") || strings.Contains(lk, "link")
}

func (s *hlceState) recordNoCredential() error {
	b, _, err := s.readRecord()
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	for k := range m {
		if hlceCredentialKey(k) {
			return fmt.Errorf("the record has a %q field: %s", k, b)
		}
	}
	if strings.Contains(string(b), "token=") || strings.Contains(string(b), auth.CookieName(s.listener.l.Origin())) {
		return fmt.Errorf("the record carries a credential: %s", b)
	}
	return nil
}

func (s *hlceState) onlyListenerFiles() error {
	entries, err := os.ReadDir(s.layout.Run())
	if err != nil {
		return err
	}
	p := launch.PathsFor(s.layout.Run(), s.listener.l.Origin())
	want := []string{filepath.Base(p.Lock), filepath.Base(p.Record), filepath.Base(p.Socket)}
	sort.Strings(want)
	var got []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "web-") {
			got = append(got, e.Name())
		}
	}
	sort.Strings(got)
	if !slices.Equal(got, want) {
		return fmt.Errorf("%s holds %v, want only %v", s.layout.Run(), got, want)
	}
	return nil
}

func (s *hlceState) unloadListener() error {
	s.listener.launchF.Dispose()
	return nil
}

func (s *hlceState) noRecord() error {
	got, err := s.records()
	if err != nil {
		return err
	}
	if len(got) != 0 {
		return fmt.Errorf("records after unload = %v", got)
	}
	return nil
}

func (s *hlceState) mint() error {
	var err error
	s.link, err = s.listener.svc.Mint()
	if err != nil {
		return err
	}
	if !strings.Contains(s.link, "token=") {
		return fmt.Errorf("the minted link has no token")
	}
	return nil
}

// call dials the record's control socket, not the host's in-process value, so it
// reaches the host the way a second dsh does.
func (s *hlceState) call(method string) error {
	_, rec, err := s.readRecord()
	if err != nil {
		return err
	}
	c, err := net.Dial("unix", rec.Socket)
	if err != nil {
		return fmt.Errorf("dial the control socket: %w", err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintf(c, `{"jsonrpc":"2.0","id":1,"method":%q}`+"\n", method); err != nil {
		return err
	}
	if s.reply, err = bufio.NewReader(c).ReadBytes('\n'); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(s.reply, &env); err != nil {
		return fmt.Errorf("%s: %w in %q", method, err, s.reply)
	}
	if e, ok := env["error"]; ok {
		return fmt.Errorf("%s failed: %s", method, e)
	}
	if err := json.Unmarshal(env["result"], &s.result); err != nil {
		return fmt.Errorf("%s result %s: %w", method, env["result"], err)
	}
	return nil
}

func (s *hlceState) replyAddressAndPID() error {
	if s.result == nil {
		return errors.New("no reply")
	}
	var origin string
	var pid int
	if err := json.Unmarshal(s.result["origin"], &origin); err != nil {
		return fmt.Errorf("reply origin %s: %w", s.result["origin"], err)
	}
	if err := json.Unmarshal(s.result["pid"], &pid); err != nil {
		return fmt.Errorf("reply pid %s: %w", s.result["pid"], err)
	}
	if origin != s.listener.l.Origin() {
		return fmt.Errorf("reply origin %q, want the listener's %q", origin, s.listener.l.Origin())
	}
	u, err := url.Parse(origin)
	if err != nil || u.Port() == "" || u.Port() == "0" {
		return fmt.Errorf("reply origin %q names no address with a port (%v)", origin, err)
	}
	conn, err := net.DialTimeout("tcp", u.Host, 5*time.Second)
	if err != nil {
		return fmt.Errorf("reply origin %q is not a listening address: %w", origin, err)
	}
	_ = conn.Close()
	if pid != os.Getpid() {
		return fmt.Errorf("reply pid %d, want %d", pid, os.Getpid())
	}
	return nil
}

func (s *hlceState) replyNoCredentialField() error {
	if s.result == nil {
		return errors.New("no reply")
	}
	var walk func(prefix string, raw json.RawMessage) error
	walk = func(prefix string, raw json.RawMessage) error {
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil {
			return nil
		}
		for k, v := range m {
			if hlceCredentialKey(k) {
				return fmt.Errorf("the reply has a %q field: %s", prefix+k, s.reply)
			}
			if err := walk(prefix+k+".", v); err != nil {
				return err
			}
		}
		return nil
	}
	for k, v := range s.result {
		if hlceCredentialKey(k) {
			return fmt.Errorf("the reply has a %q field: %s", k, s.reply)
		}
		if err := walk(k+".", v); err != nil {
			return err
		}
	}
	return nil
}

func (s *hlceState) replyNoSecrets() error {
	_, tok, ok := strings.Cut(s.link, "token=")
	if !ok || tok == "" {
		return errors.New("no launch link was minted")
	}
	if strings.Contains(string(s.reply), tok) {
		return fmt.Errorf("the reply carries the launch token: %s", s.reply)
	}
	if name := auth.CookieName(s.listener.l.Origin()); strings.Contains(string(s.reply), name) {
		return fmt.Errorf("the reply carries the session cookie name %s: %s", name, s.reply)
	}
	return nil
}

func InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_host_launch_and_control_endpoint(ctx *godog.ScenarioContext) {
	var s *hlceState
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		s = &hlceState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return c, nil
	})
	ctx.Step(`^a temp home$`, func() error { return s.tempHome() })
	ctx.Step(`^a listener profile "([^"]*)" and a "([^"]*)" profile boot in-test$`, func(l, h string) error { return s.bootBoth(l, h) })
	ctx.Step(`^"<home>/run/" holds exactly one runtime record$`, func() error { return s.exactlyOneRecord() })
	ctx.Step(`^that runtime record is the listener's, with its origin, this process's pid and its control socket$`, func() error { return s.recordIsListeners() })
	ctx.Step(`^that runtime record holds no token, cookie or launch link$`, func() error { return s.recordNoCredential() })
	ctx.Step(`^"<home>/run/" holds no host\.launch file but the listener's record, lock and control socket$`, func() error { return s.onlyListenerFiles() })
	ctx.Step(`^the listener profile is unloaded$`, func() error { return s.unloadListener() })
	ctx.Step(`^"<home>/run/" holds no runtime record$`, func() error { return s.noRecord() })
	ctx.Step(`^a running listener host booted from the "([^"]*)" profile$`, func(p string) error { return s.bootListener(p) })
	ctx.Step(`^a launch link minted by that host's host\.auth$`, func() error { return s.mint() })
	ctx.Step(`^"([^"]*)" is called over the local socket$`, func(m string) error { return s.call(m) })
	ctx.Step(`^the reply has the listener's address and this process's pid$`, func() error { return s.replyAddressAndPID() })
	ctx.Step(`^the reply has no token or cookie field$`, func() error { return s.replyNoCredentialField() })
	ctx.Step(`^the reply carries neither the launch link's token nor the session cookie's name$`, func() error { return s.replyNoSecrets() })
}

func TestE2E_dh_s_15_listener_sign_in_stdio_and_sdk_server_host_launch_and_control_endpoint(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_host_launch_and_control_endpoint,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_15_listener_sign_in_stdio_and_sdk_server_host_launch_and_control_endpoint.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the host-launch-and-control-endpoint e2e scenarios failed")
	}
}
