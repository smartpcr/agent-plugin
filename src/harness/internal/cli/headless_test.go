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
func modelHome(t *testing.T, m *fakeModel) string {
	t.Helper()
	root := temphome.New(t).Root()
	temphome.Layer(t, root, profile.Headless,
		"- insert:\n    - id: route\n      name: llm.testkit-route\n      config:\n        id: fake\n        url: "+strconv.Quote(m.url)+"\n")
	return root
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
