package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
)

// buildTestDsh builds cmd/dsh with the testhook tag, which adds the keyless
// fake route item llm.testkit-route (boot/builtin/testhook.go).
func buildTestDsh(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the dsh binary")
	}
	return launcher.Build(t, filepath.Join("..", ".."), "./cmd/dsh", "testhook")
}

// fakeModel serves the kit's SSE dialect on loopback: every attempt answers
// text and finishes with stop. Until release is closed it holds each reply after
// noting the request on arrived.
type fakeModel struct {
	url     string
	arrived chan struct{}
	release chan struct{}
}

func newFakeModel(t *testing.T, text string, hold bool) *fakeModel {
	t.Helper()
	m := &fakeModel{arrived: make(chan struct{}, 16), release: make(chan struct{})}
	if !hold {
		close(m.release)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case m.arrived <- struct{}{}:
		default:
		}
		select {
		case <-m.release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		data, _ := json.Marshal(map[string]string{"text": text})
		fmt.Fprintf(w, "event: text\ndata: %s\n\n", data)
		fmt.Fprint(w, "event: usage\ndata: {\"input\":3,\"output\":1}\n\n")
		fmt.Fprint(w, "event: finish\ndata: {\"reason\":\"stop\"}\n\n")
	}))
	t.Cleanup(func() {
		select {
		case <-m.release:
		default:
			close(m.release)
		}
		srv.Close()
	})
	m.url = srv.URL
	return m
}

// modelHome is a temp home whose headless profile layer inserts the fake route.
func modelHome(t *testing.T, m *fakeModel, insert ...string) string {
	t.Helper()
	root := temphome.New(t).Root()
	routeLayer(t, root, m, insert...)
	return root
}

// routeLayer (re)writes the headless layer of root: the fake route of m and
// the further insert entries.
func routeLayer(t *testing.T, root string, m *fakeModel, insert ...string) {
	t.Helper()
	temphome.Layer(t, root, profile.Headless,
		"- insert:\n    - id: route\n      name: llm.testkit-route\n      config:\n        id: fake\n        url: "+strconv.Quote(m.url)+"\n"+strings.Join(insert, ""))
}

// headless starts bin as `dsh --home root headless args...` with the trace
// switch on and returns the command; wait collects its result.
func headless(t *testing.T, ctx context.Context, bin, root string, args ...string) *proc {
	t.Helper()
	cmd := exec.CommandContext(ctx, bin, append([]string{"--home", root, "headless"}, args...)...)
	cmd.Env = append(os.Environ(), TraceEnv+"=1")
	cmd.Dir = t.TempDir()
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &proc{cmd: cmd, out: &out, errOut: &errOut, done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	return p
}

// proc is a started dsh process and its streams.
type proc struct {
	cmd         *exec.Cmd
	out, errOut *bytes.Buffer
	done        chan error
}

func waitRun(t *testing.T, p *proc) run {
	t.Helper()
	cmd, out, errOut := p.cmd, p.out, p.errOut
	err := <-p.done
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatalf("%v: %v", cmd.Args, err)
	}
	return run{cmd.ProcessState.ExitCode(), out.String(), errOut.String()}
}

// TC-13b: `dsh headless "say hello"` against the keyless fake route prints the
// interval's last assistant text and nothing else on stdout, and exits 0;
// progress goes to stderr only (U-K2-4).
//
//dsh:test DH-T-CLI-12
func TestHeadlessPrintsTheLastAssistantText(t *testing.T) {
	bin := buildTestDsh(t)
	root := modelHome(t, newFakeModel(t, "hello", false))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r := waitRun(t, headless(t, ctx, bin, root, "say hello"))
	if r.status != output.OK || r.stdout != "hello\n" {
		t.Fatalf("dsh headless: %+v", r)
	}
	if !strings.Contains(r.stderr, "dsh headless: run ses_") {
		t.Errorf("no progress line on stderr:\n%s", r.stderr)
	}
}

// TC-13b: under --json every stdout line is an export record and the last is
// the run result.
//
//dsh:test DH-T-CLI-12
func TestHeadlessJSONWritesExportLines(t *testing.T) {
	bin := buildTestDsh(t)
	root := modelHome(t, newFakeModel(t, "hello", false))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r := waitRun(t, headless(t, ctx, bin, root, "--json", "say hello"))
	if r.status != output.OK {
		t.Fatalf("dsh headless --json: %+v", r)
	}
	lines := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("stdout has %d lines:\n%s", len(lines), r.stdout)
	}
	types := map[string]bool{}
	for i, line := range lines[:len(lines)-1] {
		var rec struct {
			Seq  *int64 `json:"seq"`
			Type string `json:"type"`
			Data string `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Seq == nil || rec.Type == "" || !json.Valid([]byte(rec.Data)) {
			t.Fatalf("line %d is not an export record (%v): %s", i, err, line)
		}
		types[rec.Type] = true
	}
	for _, want := range []string{"assistant/message", "turn/end"} {
		if !types[want] {
			t.Errorf("no %s export line in:\n%s", want, r.stdout)
		}
	}
	var res struct{ Type, Session, Reason, Text string }
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil || res.Type != "run/result" || res.Text != "hello" || res.Reason != "completed" || res.Session == "" {
		t.Errorf("last line is not the run result (%v): %s", err, lines[len(lines)-1])
	}
}

// TC-13b, INV-10: `dsh sandbox status` beside a headless run blocked on the
// model reply exits 0 and builds no agent, model client or carrier; the run then
// completes with status 0.
//
//dsh:test DH-T-CLI-16
func TestAdminCommandBesideALiveHeadlessRun(t *testing.T) {
	bin := buildTestDsh(t)
	m := newFakeModel(t, "hello", true)
	root := modelHome(t, m)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p := headless(t, ctx, bin, root, "say hello")
	select {
	case <-m.arrived:
	case err := <-p.done:
		t.Fatalf("the headless run ended before the model replied (%v):\n%s", err, p.errOut)
	case <-ctx.Done():
		t.Fatal("the headless run never reached the model")
	}
	r := dshProcess(t, bin, "", "--home", root, "sandbox", "status")
	if r.status != output.OK {
		t.Fatalf("sandbox status beside the run: %+v", r)
	}
	for _, part := range strings.Split(traceLine(t, r.stderr), ",") {
		if profile.Acting(part, part) || strings.HasPrefix(part, "llm.") || strings.HasPrefix(part, "agent") || strings.HasPrefix(part, "cli.") {
			t.Errorf("sandbox status built %s", part)
		}
	}
	close(m.release)
	if h := waitRun(t, p); h.status != output.OK || h.stdout != "hello\n" {
		t.Fatalf("the headless run: %+v", h)
	}
}

// The run binds its agent preset (the default, standard) and applies
// --approval-policy never: the session's log records both.
//
//dsh:test DH-T-CLI-13
func TestHeadlessBindsThePresetAndPolicy(t *testing.T) {
	bin := buildTestDsh(t)
	root := modelHome(t, newFakeModel(t, "hello", false))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r := waitRun(t, headless(t, ctx, bin, root, "--json", "--approval-policy", "never", "say hello"))
	lines := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
	var res struct{ Session string }
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); r.status != output.OK || err != nil {
		t.Fatalf("dsh headless: %+v (%v)", r, err)
	}
	st, err := sqlite.Open(ctx, home.New(root).DB())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close(ctx) }()
	rd, err := st.OpenRead(ctx, res.Session)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rd.Close(ctx) }()
	recs, err := rd.Read(ctx, 0, 0, store.ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var preset, policy bool
	for _, rec := range recs {
		preset = preset || rec.Type == "agent-preset/selected" && strings.Contains(rec.Payload, `"preset":"standard"`)
		policy = policy || rec.Type == "approval/policy" && strings.Contains(rec.Payload, `"policy":"never"`)
	}
	if !preset || !policy {
		t.Fatalf("preset standard bound %v, policy never set %v in %+v", preset, policy, recs)
	}
}

// `dsh headless --command` runs the command through the registry: its output is
// printed, command/run is logged and no model request is made.
//
//dsh:test DH-T-CLI-12
func TestHeadlessRunsACommand(t *testing.T) {
	bin := buildTestDsh(t)
	m := newFakeModel(t, "hello", false)
	root := modelHome(t, m, "    - id: echo\n      name: human.testkit-command\n")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r := waitRun(t, headless(t, ctx, bin, root, "--command", "/echo hi"))
	if r.status != output.OK || r.stdout != "hi\n" {
		t.Fatalf("dsh headless --command: %+v", r)
	}
	r = waitRun(t, headless(t, ctx, bin, root, "--json", "--command", "/echo hi"))
	if r.status != output.OK || !strings.Contains(r.stdout, `"type":"command/run"`) || !strings.Contains(r.stdout, `"type":"command/done"`) {
		t.Fatalf("dsh headless --json --command: %+v", r)
	}
	select {
	case <-m.arrived:
		t.Fatal("a command made a model request")
	default:
	}
}

// An --agent-preset the registry does not hold is refused with status 5 before
// any model request.
func TestHeadlessRefusesAnUnknownPreset(t *testing.T) {
	bin := buildTestDsh(t)
	m := newFakeModel(t, "hello", false)
	root := modelHome(t, m)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r := waitRun(t, headless(t, ctx, bin, root, "--agent-preset", "ghost", "say hello"))
	if r.status != output.Invalid || r.stdout != "" {
		t.Fatalf("dsh headless --agent-preset ghost: %+v", r)
	}
	select {
	case <-m.arrived:
		t.Fatal("a refused run made a model request")
	default:
	}
}

// A command that never reaches the model runs on a home with no model route.
//
//dsh:test DH-T-CLI-12
func TestHeadlessRunsACommandWithoutARoute(t *testing.T) {
	bin := buildTestDsh(t)
	root := temphome.New(t).Root()
	temphome.Layer(t, root, profile.Headless, "- insert:\n    - id: echo\n      name: human.testkit-command\n")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r := waitRun(t, headless(t, ctx, bin, root, "--command", "/echo hi"))
	if r.status != output.OK || r.stdout != "hi\n" {
		t.Fatalf("dsh headless --command on a route-free home: %+v", r)
	}
}

// lastResult is the run/result line that ends a --json run's stdout.
func lastResult(t *testing.T, r run) (res struct{ Type, Session, Reason, Text string }) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil || res.Type != "run/result" {
		t.Fatalf("no run/result line (%v): %+v", err, r)
	}
	return res
}

// A second process resumes the session with --session: it prints only its own
// interval's answer and exports only that interval's records.
//
//dsh:test DH-T-CLI-12
func TestHeadlessResumesASession(t *testing.T) {
	bin := buildTestDsh(t)
	root := modelHome(t, newFakeModel(t, "one", false))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	first := waitRun(t, headless(t, ctx, bin, root, "--json", "first"))
	res := lastResult(t, first)
	if first.status != output.OK || res.Text != "one" {
		t.Fatalf("first run: %+v", first)
	}
	routeLayer(t, root, newFakeModel(t, "two", false))
	second := waitRun(t, headless(t, ctx, bin, root, "--json", "--session", res.Session, "second"))
	got := lastResult(t, second)
	if second.status != output.OK || got.Session != res.Session || got.Text != "two" || got.Reason != "completed" {
		t.Fatalf("resumed run: %+v", second)
	}
	if strings.Contains(second.stdout, `\"one\"`) || strings.Contains(second.stdout, `\"first\"`) {
		t.Fatalf("the resumed run exported the first interval:\n%s", second.stdout)
	}
	third := waitRun(t, headless(t, ctx, bin, root, "--session", res.Session, "third"))
	if third.status != output.OK || third.stdout != "two\n" {
		t.Fatalf("resumed text run: %+v", third)
	}
}

// A resumed session whose recorded agent preset is no longer declared is
// refused with status 5, with no fallback and no model request.
//
//dsh:test DH-T-CLI-13
func TestHeadlessResumeRefusesARemovedPreset(t *testing.T) {
	bin := buildTestDsh(t)
	m := newFakeModel(t, "one", false)
	extra := "    - id: preset.extra\n      name: agent.preset\n      config:\n        id: extra\n        name: Extra\n"
	root := modelHome(t, m, extra)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	first := waitRun(t, headless(t, ctx, bin, root, "--json", "--agent-preset", "extra", "first"))
	res := lastResult(t, first)
	if first.status != output.OK {
		t.Fatalf("first run: %+v", first)
	}
	later := newFakeModel(t, "two", false)
	routeLayer(t, root, later)
	r := waitRun(t, headless(t, ctx, bin, root, "--session", res.Session, "second"))
	if r.status != output.Invalid || r.stdout != "" || !strings.Contains(r.stderr, `"extra"`) {
		t.Fatalf("resume with a removed preset: %+v", r)
	}
	select {
	case <-later.arrived:
		t.Fatal("a refused resume made a model request")
	default:
	}
}
