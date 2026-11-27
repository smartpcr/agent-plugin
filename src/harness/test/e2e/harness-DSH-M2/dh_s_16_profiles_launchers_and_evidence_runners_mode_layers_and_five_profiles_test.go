//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	hostlaunch "github.com/smartpcr/agent-plugin/src/harness/internal/host/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// mlpT is the suite's testing.T: the fake model transport reports through it.
var mlpT *testing.T

// mlpComposedGolden is the committed golden of TC-08g, relative to this package.
var mlpComposedGolden = filepath.Join("..", "..", "..", "internal", "boot", "profile", "testdata", "composed.golden")

const (
	mlpRoute    vocab.RouteID = "fake"
	mlpListener               = "host.listen"
	mlpWait                   = 60 * time.Second
)

// mlpSteps are the real boot steps, keeping the composed tree.
type mlpSteps struct {
	boot.Real
	tree *[]loader.EntryOptions
}

func (c mlpSteps) Compose(ctx context.Context, s *boot.State) error {
	err := c.Real.Compose(ctx, s)
	*c.tree = s.Composed
	return err
}

type mlpState struct {
	// boot scenarios
	def       profile.Definition
	tmp       string
	homeDir   string
	opts      boot.Options
	tree      []loader.EntryOptions
	app       *launch.Control
	stderr    *testkit.Buffer
	clientIn  *io.PipeWriter
	clientOut *io.PipeReader
	serverOut *io.PipeWriter
	done      chan int
	joined    chan struct{}
	status    int
	stopped   bool
	reply     string // the fake route's scripted reply; empty scripts no turn
	transport *fakemodel.Transport
	scripted  int // number of turns scripted on transport

	// golden scenario
	defs     []profile.Definition
	layers   map[string][]loader.Layer
	composed string
	problems []string
}

func (s *mlpState) definition(name string) (profile.Definition, error) {
	for _, d := range profile.Definitions() {
		if d.Name == name {
			return d, nil
		}
	}
	return profile.Definition{}, fmt.Errorf("no launchable profile %q", name)
}

// ---- Each profile boots (TC-08g plus TC-11b) ----

func (s *mlpState) definitionsAre(list string) error {
	var got []string
	for _, d := range profile.Definitions() {
		got = append(got, d.Name)
	}
	if want := hplList(list); !slices.Equal(got, want) {
		return fmt.Errorf("profile definitions = %v, want %v", got, want)
	}
	return nil
}

// tempHome prepares a fresh home under a short temp directory (web's control
// socket path is bounded) and the launch options: the built catalogue plus an
// x.route component that registers the keyless fake route, mounted by an
// overlay for every profile with the model runtime; web's overlay binds a free
// port and opens no browser.
func (s *mlpState) tempHome(name string) error {
	return s.tempHomeScripted(name, "")
}

// tempHomeScripted is tempHome with the fake route scripted to answer one prompt
// with reply; an empty reply scripts no turn, so any model call fails.
func (s *mlpState) tempHomeScripted(name, reply string) error {
	def, err := s.definition(name)
	if err != nil {
		return err
	}
	s.def = def
	s.reply = reply
	s.tmp, err = os.MkdirTemp("", "dshm-")
	if err != nil {
		return err
	}
	s.homeDir = filepath.Join(s.tmp, "home")
	if _, err := os.Stat(s.homeDir); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("temp home %s already exists (%v)", s.homeDir, err)
	}
	var overlays []string
	if name != profile.Admin {
		layer := "- insert:\n    - id: route\n      name: x.route\n"
		if name == profile.Web {
			layer += "- id: host.listen\n  name: host.listen\n  merge:\n    port: 0\n" +
				"- id: host.launch\n  name: host.launch\n  merge:\n    open: false\n    print: false\n"
		}
		overlay := filepath.Join(s.tmp, "route.yml")
		if err := os.WriteFile(overlay, []byte(layer), 0o600); err != nil {
			return err
		}
		overlays = []string{overlay}
	}
	var turns []fakemodel.Turn
	if reply != "" {
		turns = []fakemodel.Turn{fakemodel.Reply(reply)}
	}
	transport := fakemodel.NewTransport(mlpT, turns...)
	s.transport, s.scripted = transport, len(turns)
	route := catalog.Item{Name: "x.route", Component: cordis.Define("x.route", func(ctx *cordis.Context, _ cordis.None) error {
		rt, err := llm.RuntimeKey.Get(ctx)
		if err != nil {
			return err
		}
		_, err = ctx.Effect("x.route", func() (cordis.Disposer, error) {
			reg, err := rt.Register("x.route", llm.Route{ID: mlpRoute, Models: []llm.Model{{Name: fakemodel.ModelName}},
				Adapter: fakemodel.NewAdapter(transport)})
			if err != nil {
				return nil, err
			}
			return func() error { reg.Close(); return nil }, nil
		})
		return err
	}, cordis.WithInject(llm.RuntimeKey.Name()))}
	cat, err := catalog.Build(append(builtin.Sets(), []catalog.Item{route})...)
	if err != nil {
		return fmt.Errorf("catalogue: %w", err)
	}
	s.opts = boot.Options{
		Request: launch.Request{Args: []string{"dsh", name}, Cwd: s.tmp, Home: s.homeDir, Profile: name, Overlays: overlays},
		Profile: def,
		Catalog: cat,
		Steps:   mlpSteps{tree: &s.tree},
	}
	return nil
}

func (s *mlpState) processRuns(name string) error {
	if s.def.Name != name {
		return fmt.Errorf("prepared %q, asked to run %q", s.def.Name, name)
	}
	s.app = launch.NewApp()
	s.stderr = &testkit.Buffer{}
	s.opts.App, s.opts.Stderr = s.app, s.stderr
	if name == profile.SDK {
		serverIn, cin := io.Pipe()
		cout, serverOut := io.Pipe()
		s.clientIn, s.clientOut, s.serverOut = cin, cout, serverOut
		s.opts.Stdio = &launch.Stdio{In: serverIn, Out: serverOut}
	}
	// The headless runner, given no task words, reads its task from an input
	// nobody writes, so the launch runs until the client ends it.
	if name == profile.Headless {
		in, w := io.Pipe()
		s.clientIn, s.serverOut = w, w
		s.opts.Stdio = &launch.Stdio{In: in, Out: io.Discard}
	}
	s.done, s.joined = make(chan int, 1), make(chan struct{})
	opts := s.opts
	go func() {
		defer close(s.joined)
		s.done <- boot.Process(context.Background(), opts)
	}()
	return nil
}

func (s *mlpState) reachesReady() error {
	select {
	case <-s.app.Ready():
	case status := <-s.done:
		s.stopped = true
		return fmt.Errorf("%s ended with %d before ready:\n%s", s.def.Name, status, s.stderr.String())
	case <-time.After(mlpWait):
		return fmt.Errorf("%s never reached ready:\n%s", s.def.Name, s.stderr.String())
	}
	if _, err := os.Stat(home.New(s.homeDir).DB()); err != nil {
		return fmt.Errorf("%s is ready without the session store: %w", s.def.Name, err)
	}
	return nil
}

func mlpMountsListener(tree []loader.EntryOptions) bool {
	for _, e := range tree {
		if e.Disabled {
			continue
		}
		if e.Name == mlpListener || mlpMountsListener(e.Children) {
			return true
		}
	}
	return false
}

func (s *mlpState) treeListener(mode string) error {
	want := mode == "mounts"
	if got := mlpMountsListener(s.tree); got != want {
		return fmt.Errorf("%s composed tree mounts %s = %v, want %v: only web binds a port", s.def.Name, mlpListener, got, want)
	}
	return nil
}

func (s *mlpState) runtimeRecords() []string {
	recs, _ := filepath.Glob(filepath.Join(home.New(s.homeDir).Run(), "web-*.json"))
	return recs
}

func (s *mlpState) homeRecord(mode string) error {
	recs := s.runtimeRecords()
	if mode == "holds no" {
		if len(recs) > 0 {
			return fmt.Errorf("%s run holds runtime records %v: no listener was bound", s.def.Name, recs)
		}
		return nil
	}
	if len(recs) != 1 {
		return fmt.Errorf("%s is ready with runtime records %v, want one", s.def.Name, recs)
	}
	b, err := os.ReadFile(recs[0])
	if err != nil {
		return err
	}
	var rec hostlaunch.Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return fmt.Errorf("runtime record %s: %w", b, err)
	}
	if rec.Profile != profile.Web || rec.PID != os.Getpid() {
		return fmt.Errorf("runtime record names pid %d profile %q, want this process %d, web", rec.PID, rec.Profile, os.Getpid())
	}
	u, err := url.Parse(rec.Origin)
	if err != nil || u.Port() == "" || u.Port() == "0" {
		return fmt.Errorf("runtime record origin %q names no bound port (%v)", rec.Origin, err)
	}
	c, err := net.DialTimeout("tcp", u.Host, 10*time.Second)
	if err != nil {
		return fmt.Errorf("web's origin %s accepts no connection: %w", rec.Origin, err)
	}
	return c.Close()
}

func (s *mlpState) driveSDK() error {
	if s.def.Name != profile.SDK {
		return fmt.Errorf("%s has no sdk client", s.def.Name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn := jsonrpc.Serve(ctx, s.clientOut, s.clientIn, jsonrpc.Options{})
	defer conn.Close()
	var init protocol.InitializeResult
	if err := conn.Call(ctx, protocol.InitializeMethod, protocol.InitializeParams{ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "e2e", Version: "0"}}, &init); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if init.ServerInfo.Name != "dsh" {
		return fmt.Errorf("serverInfo = %+v, want dsh", init.ServerInfo)
	}
	var created protocol.SessionCreateResult
	if err := conn.Call(ctx, "session/create", protocol.SessionCreateParams{Model: &protocol.ModelRoute{Provider: string(mlpRoute), Model: fakemodel.ModelName}}, &created); err != nil {
		return fmt.Errorf("session/create: %w", err)
	}
	var prompted protocol.SessionPromptResult
	if err := conn.Call(ctx, "session/prompt", protocol.SessionPromptParams{SessionID: created.SessionID, MessageID: "m1", Content: []protocol.ContentBlock{{Type: "text", Text: "ping"}}}, &prompted); err != nil {
		return fmt.Errorf("session/prompt: %w", err)
	}
	var waited protocol.LastSeqResult
	if err := conn.Call(ctx, "session/wait", protocol.SessionRef{SessionID: created.SessionID}, &waited); err != nil {
		return fmt.Errorf("session/wait: %w", err)
	}
	var history protocol.SessionHistoryResult
	if err := conn.Call(ctx, "session/history", protocol.SessionHistoryParams{SessionID: created.SessionID, Types: []string{"assistant/message"}}, &history); err != nil {
		return fmt.Errorf("session/history: %w", err)
	}
	if len(history.Events) != 1 {
		return fmt.Errorf("history holds %d assistant messages, want the fake route's one: %+v", len(history.Events), history)
	}
	var payload struct {
		Message vocab.Message `json:"message"`
	}
	if err := json.Unmarshal(history.Events[0].Data, &payload); err != nil {
		return fmt.Errorf("decode assistant/message: %w (%s)", err, history.Events[0].Data)
	}
	var text strings.Builder
	for _, b := range payload.Message.Blocks() {
		if b.Kind == vocab.KindText && b.Text != nil {
			text.WriteString(b.Text.Text)
		}
	}
	if text.String() != s.reply {
		return fmt.Errorf("assistant message text = %q, want the scripted reply %q", text.String(), s.reply)
	}
	if err := s.requestsMatchScript(); err != nil {
		return err
	}
	if recs := s.runtimeRecords(); len(recs) > 0 {
		return fmt.Errorf("sdk wrote runtime records %v", recs)
	}
	return nil
}

// requestsMatchScript fails the scenario unless the fake transport received
// exactly its scripted turns. The transport's own leftover and unscripted checks
// report through the suite-level mlpT, so only this ties them to the scenario.
func (s *mlpState) requestsMatchScript() error {
	if s.transport == nil {
		return nil
	}
	if got := len(s.transport.Requests()); got != s.scripted {
		return fmt.Errorf("%s: fake model received %d request(s), want exactly the %d scripted", s.def.Name, got, s.scripted)
	}
	return nil
}

// clientEnds is each profile's clean stop: the sdk client closes its input;
// every other profile's client completes.
func (s *mlpState) clientEnds() error {
	if s.def.Name == profile.SDK {
		return s.clientIn.Close()
	}
	s.app.Stop(launch.Completed, output.OK)
	return nil
}

func (s *mlpState) stopsCleanly() error {
	select {
	case s.status = <-s.done:
		s.stopped = true
	case <-time.After(mlpWait):
		return fmt.Errorf("%s did not stop:\n%s", s.def.Name, s.stderr.String())
	}
	if s.status != output.OK {
		return fmt.Errorf("%s stopped with %d, want %d:\n%s", s.def.Name, s.status, output.OK, s.stderr.String())
	}
	if strings.Contains(s.stderr.String(), "dsh: fatal:") {
		return fmt.Errorf("%s printed a fatal message:\n%s", s.def.Name, s.stderr.String())
	}
	return nil
}

func (s *mlpState) sdkInputClosed() error {
	if r, _, _ := s.app.Result(); r != launch.InputClosed {
		return fmt.Errorf("sdk stopped for %q, want %q", r, launch.InputClosed)
	}
	return nil
}

func (s *mlpState) noRecordAfterStop() error {
	if !s.stopped {
		return errors.New("the profile has not stopped")
	}
	if recs := s.runtimeRecords(); len(recs) > 0 {
		return fmt.Errorf("%s run still holds runtime records %v after the stop", s.def.Name, recs)
	}
	return nil
}

func (s *mlpState) cleanup() error {
	var errs []error
	if s.app != nil && !s.stopped {
		s.app.Stop(launch.Requested, output.OK)
		if s.clientIn != nil {
			_ = s.clientIn.Close()
		}
		select {
		case <-s.joined:
		case <-time.After(30 * time.Second):
			errs = append(errs, fmt.Errorf("%s did not return after the cleanup stopped it", s.def.Name))
		}
	}
	if s.clientIn != nil {
		_ = s.clientIn.Close()
		_ = s.serverOut.Close()
	}
	if s.tmp != "" {
		if err := os.RemoveAll(s.tmp); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ---- Mode layer composition (TC-08g) ----

// committedLayers are the embedded bundles of every profile definition, in the
// order composed.golden records them: the launchable five, then the other
// built-ins.
func (s *mlpState) committedLayers() error {
	s.defs = profile.Definitions()
	for _, d := range profile.Builtins() {
		if !slices.ContainsFunc(s.defs, func(o profile.Definition) bool { return o.Name == d.Name }) {
			s.defs = append(s.defs, d)
		}
	}
	s.layers = map[string][]loader.Layer{}
	for _, def := range s.defs {
		for _, name := range def.Bundles {
			layer, err := profile.Bundle(name)
			if err != nil {
				return fmt.Errorf("%s: bundle %s: %w", def.Name, name, err)
			}
			s.layers[def.Name] = append(s.layers[def.Name], layer)
		}
	}
	return nil
}

func (s *mlpState) layersComposed() error {
	var out bytes.Buffer
	for _, def := range s.defs {
		tree, issues, err := profile.Compose(def, s.layers[def.Name])
		if err != nil {
			s.problems = append(s.problems, fmt.Sprintf("%s: compose: %v", def.Name, err))
		}
		var skipped []string
		for _, issue := range issues {
			if !strings.HasSuffix(issue.Message, " matches no entry") {
				s.problems = append(s.problems, fmt.Sprintf("%s: compose issue: %v", def.Name, issue))
				continue
			}
			skipped = append(skipped, issue.Error())
		}
		b, err := json.MarshalIndent(tree, "", "  ")
		if err != nil {
			return err
		}
		out.WriteString("# " + def.Name + "\n")
		if len(def.Required) > 0 {
			out.WriteString("# required: " + strings.Join(def.Required, ", ") + "\n")
		}
		for _, sk := range skipped {
			out.WriteString("# skipped, target not built: " + sk + "\n")
		}
		out.Write(b)
		out.WriteString("\n")
	}
	s.composed = out.String()
	return nil
}

func (s *mlpState) composesCleanly() error {
	if len(s.problems) > 0 {
		return fmt.Errorf("composition problems:\n%s", strings.Join(s.problems, "\n"))
	}
	return nil
}

func (s *mlpState) equalsGolden() error {
	want, err := os.ReadFile(mlpComposedGolden)
	if err != nil {
		return fmt.Errorf("read committed golden: %w", err)
	}
	if !bytes.Equal(want, []byte(s.composed)) {
		return fmt.Errorf("composed layers differ from %s:\n%s", mlpComposedGolden, golden.Diff(string(want), s.composed))
	}
	return nil
}

func InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_mode_layers_and_five_profiles(ctx *godog.ScenarioContext) {
	s := &mlpState{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		// cleanup stops the profile first, so no request can arrive after the check.
		if cerr := errors.Join(s.cleanup(), s.requestsMatchScript()); cerr != nil && err == nil {
			return c, cerr
		}
		return c, err
	})
	ctx.Step(`^the launchable profile definitions are exactly "([^"]*)"$`, s.definitionsAre)
	ctx.Step(`^a temp home for the "([^"]*)" profile with the keyless fake route$`, s.tempHome)
	ctx.Step(`^a temp home for the "([^"]*)" profile with the keyless fake route scripted to reply "([^"]*)"$`, s.tempHomeScripted)
	ctx.Step(`^boot\.Process runs the "([^"]*)" profile in-test$`, s.processRuns)
	ctx.Step(`^the profile reaches ready with its embedded sqlite session store open$`, s.reachesReady)
	ctx.Step(`^the composed tree (mounts|mounts no) a host\.listen listener$`, s.treeListener)
	ctx.Step(`^the home (holds|holds no) a runtime record whose origin accepts a TCP connection$`, s.homeRecord)
	ctx.Step(`^the sdk client initializes, opens a session on the fake route and its prompt is answered once$`, s.driveSDK)
	ctx.Step(`^the profile's client ends$`, s.clientEnds)
	ctx.Step(`^the profile stops with status 0 and prints no fatal message$`, s.stopsCleanly)
	ctx.Step(`^the sdk profile stopped because its input closed$`, s.sdkInputClosed)
	ctx.Step(`^the home holds no runtime record after the stop$`, s.noRecordAfterStop)

	ctx.Step(`^the committed mode layers of every profile definition$`, s.committedLayers)
	ctx.Step(`^the layers are composed$`, s.layersComposed)
	ctx.Step(`^every profile composes with no failure and only pending-target issues$`, s.composesCleanly)
	ctx.Step(`^the composed output equals the committed composed\.golden byte for byte$`, s.equalsGolden)
}

func TestE2E_dh_s_16_profiles_launchers_and_evidence_runners_mode_layers_and_five_profiles(t *testing.T) {
	mlpT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_mode_layers_and_five_profiles,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_16_profiles_launchers_and_evidence_runners_mode_layers_and_five_profiles.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
