package boot

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// fakeRoute is the keyless route the profiles boot with: an x.route entry that
// registers it on llm.runtime for as long as the entry's fiber lives.
const fakeRoute vocab.RouteID = "fake"

// listenerComponent is the component that binds the web listener (host/listen).
const listenerComponent = "host.listen"

// composedSteps are the real steps, keeping the composed tree for the test.
type composedSteps struct {
	Real
	tree *[]loader.EntryOptions
}

func (c composedSteps) Compose(ctx context.Context, s *State) error {
	err := c.Real.Compose(ctx, s)
	*c.tree = s.Composed
	return err
}

// DH-D-BUNDLE-3 (C42), TC-08g plus TC-11b: each of the five profiles boots through
// boot.Process on a temp home with the built catalogue and a keyless fake route,
// reaches ready and stops cleanly with status 0. While each runs, its tree mounts
// no listener and the home holds no runtime record (M2-C-13): headless and sdk
// bind no port. The sdk carrier serves its client over pipes -- initialize, a
// session on the fake route, a prompt answered by it -- and the client closing its
// input stops the process (InputClosed, 0).
func TestFiveProfilesBootThroughProcessAndStopCleanly(t *testing.T) {
	var names []string
	for _, def := range profile.Definitions() {
		names = append(names, def.Name)
	}
	if want := []string{profile.Admin, profile.Core, profile.Web, profile.Headless, profile.SDK}; strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("profile definitions = %v, want %v", names, want)
	}
	for _, def := range profile.Definitions() {
		t.Run(def.Name, func(t *testing.T) {
			if def.Name == profile.Web {
				webNotYetLaunchable(t, def)
				return
			}
			bootProfile(t, def)
		})
	}
}

// launchOptions are the Options of a launch of def on a fresh temp home. A profile
// with the model runtime (every one but admin) mounts the fake route by an
// overlay, scripted with turns.
func launchOptions(t *testing.T, def profile.Definition, tree *[]loader.EntryOptions, turns ...fakemodel.Turn) (Options, string) {
	t.Helper()
	homeDir := temphome.Dir(t)
	root := filepath.Dir(homeDir)
	var overlays []string
	if def.Name != profile.Admin {
		overlay := filepath.Join(root, "route.yml")
		temphome.Write(t, overlay, "- insert:\n    - id: route\n      name: x.route\n")
		overlays = []string{overlay}
	}
	route := catalog.Item{Name: "x.route", Component: cordis.Define("x.route", func(ctx *cordis.Context, _ cordis.None) error {
		rt, err := llm.RuntimeKey.Get(ctx)
		if err != nil {
			return err
		}
		_, err = ctx.Effect("x.route", func() (cordis.Disposer, error) {
			reg, err := rt.Register("x.route", llm.Route{ID: fakeRoute, Models: []llm.Model{{Name: fakemodel.ModelName}},
				Adapter: fakemodel.NewAdapter(fakemodel.NewTransport(t, turns...))})
			if err != nil {
				return nil, err
			}
			return func() error { reg.Close(); return nil }, nil
		})
		return err
	}, cordis.WithInject(llm.RuntimeKey.Name()))}
	cat, err := catalog.Build(append(builtin.Sets(), []catalog.Item{route})...)
	if err != nil {
		t.Fatal(err)
	}
	return Options{
		Request: launch.Request{Args: []string{"dsh", def.Name}, Cwd: root, Home: homeDir, Profile: def.Name, Overlays: overlays},
		Profile: def,
		Catalog: cat,
		Steps:   composedSteps{tree: tree},
	}, homeDir
}

func bootProfile(t *testing.T, def profile.Definition) {
	var tree []loader.EntryOptions
	var turns []fakemodel.Turn
	if def.Name == profile.SDK {
		turns = append(turns, fakemodel.Reply("pong"))
	}
	opts, homeDir := launchOptions(t, def, &tree, turns...)
	app := launch.NewApp()
	var stderr testkit.Buffer
	opts.App, opts.Stderr = app, &stderr
	// The sdk client's ends: it writes the server's input and reads its output.
	var clientIn *io.PipeWriter
	var clientOut *io.PipeReader
	if def.Name == profile.SDK {
		serverIn, cin := io.Pipe()
		cout, serverOut := io.Pipe()
		clientIn, clientOut = cin, cout
		t.Cleanup(func() { cin.Close(); serverOut.Close() })
		opts.Stdio = &launch.Stdio{In: serverIn, Out: serverOut}
	}
	done := runJoined(t, app, func() int { return Process(context.Background(), opts) })
	select {
	case <-app.Ready():
	case status := <-done:
		t.Fatalf("%s ended with %d before ready:\n%s", def.Name, status, stderr.String())
	case <-time.After(60 * time.Second):
		t.Fatalf("%s never reached ready:\n%s", def.Name, stderr.String())
	}

	// While running: the session store is open, no entry binds a listener and
	// the home holds no runtime record.
	if _, err := os.Stat(home.New(homeDir).DB()); err != nil {
		t.Errorf("%s is ready without the session store: %v", def.Name, err)
	}
	if mountsListener(tree) {
		t.Errorf("%s mounts %s: only web binds a port", def.Name, listenerComponent)
	}
	noRuntimeRecord(t, homeDir)

	if def.Name == profile.SDK {
		driveSDK(t, clientIn, clientOut)
		noRuntimeRecord(t, homeDir)
		// The client ending its input is the sdk profile's clean stop.
		clientIn.Close()
	} else {
		app.Stop(launch.Completed, output.OK)
	}
	select {
	case status := <-done:
		if status != output.OK {
			t.Fatalf("%s stopped with %d, want %d:\n%s", def.Name, status, output.OK, stderr.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("%s did not stop:\n%s", def.Name, stderr.String())
	}
	if def.Name == profile.SDK {
		if r, _, _ := app.Result(); r != launch.InputClosed {
			t.Errorf("sdk stopped for %q, want %q", r, launch.InputClosed)
		}
	}
	if strings.Contains(stderr.String(), "dsh: fatal:") {
		t.Errorf("%s printed a fatal message:\n%s", def.Name, stderr.String())
	}
	noRuntimeRecord(t, homeDir)
}

// driveSDK is the sdk client over the pipes: initialize, a session on the fake
// route, a prompt the route answers, and the wait for its turn.
func driveSDK(t *testing.T, in io.Writer, out io.Reader) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn := jsonrpc.Serve(ctx, out, in, jsonrpc.Options{})
	defer conn.Close()
	var init protocol.InitializeResult
	if err := conn.Call(ctx, protocol.InitializeMethod, protocol.InitializeParams{ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "boot-test", Version: "0"}}, &init); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if init.ServerInfo.Name != "dsh" {
		t.Errorf("serverInfo = %+v, want dsh", init.ServerInfo)
	}
	var created protocol.SessionCreateResult
	if err := conn.Call(ctx, "session/create", protocol.SessionCreateParams{Model: &protocol.ModelRoute{Provider: string(fakeRoute), Model: fakemodel.ModelName}}, &created); err != nil {
		t.Fatalf("session/create: %v", err)
	}
	ref := protocol.SessionRef{SessionID: created.SessionID}
	var prompted protocol.SessionPromptResult
	if err := conn.Call(ctx, "session/prompt", protocol.SessionPromptParams{SessionID: created.SessionID, MessageID: "m1", Content: []protocol.ContentBlock{{Type: "text", Text: "ping"}}}, &prompted); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	var waited protocol.LastSeqResult
	if err := conn.Call(ctx, "session/wait", ref, &waited); err != nil {
		t.Fatalf("session/wait: %v", err)
	}
	var history protocol.SessionHistoryResult
	if err := conn.Call(ctx, "session/history", protocol.SessionHistoryParams{SessionID: created.SessionID, Types: []string{"assistant/message"}}, &history); err != nil {
		t.Fatalf("session/history: %v", err)
	}
	if len(history.Events) != 1 {
		t.Errorf("history holds %d assistant messages, want the fake route's one: %+v", len(history.Events), history)
	}
}

// webNotYetLaunchable pins web's state at this step: its carriers (host.listen to
// ui.shell) are not in the build catalogue, so boot refuses it with status 78
// naming them. The test fails once web launches, until web boots here instead,
// bound port and runtime record asserted while it runs (DH-S-16 16.1, part 3).
func webNotYetLaunchable(t *testing.T, def profile.Definition) {
	var tree []loader.EntryOptions
	opts, _ := launchOptions(t, def, &tree)
	var stderr testkit.Buffer
	opts.Stderr = &stderr
	if status := Process(context.Background(), opts); status != output.Config {
		t.Fatalf("web ended with %d, want %d while its carriers are unbuilt:\n%s", status, output.Config, stderr.String())
	}
	if !strings.Contains(stderr.String(), listenerComponent) {
		t.Errorf("web's refusal does not name %s:\n%s", listenerComponent, stderr.String())
	}
}

// mountsListener reports whether tree holds an enabled host.listen entry.
func mountsListener(tree []loader.EntryOptions) bool {
	for _, e := range tree {
		if e.Disabled {
			continue
		}
		if e.Name == listenerComponent || mountsListener(e.Children) {
			return true
		}
	}
	return false
}

// noRuntimeRecord fails t when <home>/run holds a runtime record (web-<hash>.json,
// host/launch PathsFor): only a listener profile writes one.
func noRuntimeRecord(t *testing.T, homeDir string) {
	t.Helper()
	recs, _ := filepath.Glob(filepath.Join(home.New(homeDir).Run(), "web-*.json"))
	if len(recs) > 0 {
		t.Errorf("%s/run holds runtime records %v: no listener was bound", homeDir, recs)
	}
}
