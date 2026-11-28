package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
)

// toolModel serves the kit's SSE dialect: its first reply calls testkit_sleep,
// every later one answers text.
func toolModel(t *testing.T) string {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if first {
			fmt.Fprint(w, "event: tool\ndata: {\"id\":\"c1\",\"name\":\"testkit_sleep\",\"args\":{}}\n\n")
			fmt.Fprint(w, "event: finish\ndata: {\"reason\":\"tool_calls\"}\n\n")
			return
		}
		data, _ := json.Marshal(map[string]string{"text": "slept"})
		fmt.Fprintf(w, "event: text\ndata: %s\n\nevent: finish\ndata: {\"reason\":\"stop\"}\n\n", data)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// sleepRunning is the line the testhook tool testkit_sleep writes once its body
// runs (boot/builtin/testhook.go).
const sleepRunning = "testkit_sleep: running"

var runLine = regexp.MustCompile(`dsh headless: run (ses_[0-9a-f]+) on route`)

// TC-14b, DH-D-CLI-4: `dsh headless` running a long fake tool gets the
// termination signal -- SIGTERM on Linux, a closed console on Windows -- and exits
// 0 within ShutdownTimeout, and the session's log closes the turn: a turn/end
// after the tool call.
//
//dsh:test DH-T-CLI-9
func TestSignalDuringAToolCallClosesTheTurn(t *testing.T) {
	bin := buildTestDsh(t)
	root := temphome.New(t).Root()
	temphome.Layer(t, root, profile.Headless,
		"- insert:\n    - id: route\n      name: llm.testkit-route\n      config:\n        id: fake\n        url: "+strconv.Quote(toolModel(t))+
			"\n    - id: sleep\n      name: tools.testkit-sleep\n")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--home", root, "headless", "sleep, please")
	cmd.Env, cmd.Dir = os.Environ(), t.TempDir()
	stdout, stderr := &testkit.Buffer{}, &testkit.Buffer{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	stoppable(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	go func() { defer close(done); _ = cmd.Wait() }()
	for !strings.Contains(stderr.String(), sleepRunning) {
		select {
		case <-done:
			t.Fatalf("dsh headless exited %d before the tool ran:\n%s", cmd.ProcessState.ExitCode(), stderr)
		case <-ctx.Done():
			t.Fatalf("the tool never ran:\n%s", stderr)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err := terminateProcess(t, cmd); err != nil {
		t.Fatal(err)
	}
	sent := time.Now()
	bound := profile.DefaultLimits().ShutdownTimeout
	select {
	case <-done:
	case <-time.After(bound + 5*time.Second):
		t.Fatalf("dsh headless did not exit within %s of the signal:\n%s", bound, stderr)
	}
	took := time.Since(sent)
	if took > bound {
		t.Errorf("exited %s after the signal, past the %s bound", took, bound)
	}
	if got := cmd.ProcessState.ExitCode(); got != output.OK {
		t.Errorf("status %d, want %d:\n%s", got, output.OK, stderr)
	}
	m := runLine.FindStringSubmatch(stderr.String())
	if m == nil {
		t.Fatalf("no run line on stderr:\n%s", stderr)
	}
	st, err := sqlite.Open(ctx, home.New(root).DB())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close(ctx) }()
	rd, err := st.OpenRead(ctx, m[1])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rd.Close(ctx) }()
	recs, err := rd.Read(ctx, 0, 0, store.ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	called, closed := false, false
	var types []string
	for _, rec := range recs {
		types = append(types, rec.Type)
		called = called || rec.Type == event.ToolCall.Name()
		closed = called && (closed || rec.Type == event.TurnEndType.Name())
	}
	t.Logf("exited %d %s after the signal; the log holds %v", cmd.ProcessState.ExitCode(), took, types)
	if !called || !closed {
		t.Fatalf("tool call logged %v, turn closed after it %v; the log holds %v", called, closed, types)
	}
}
