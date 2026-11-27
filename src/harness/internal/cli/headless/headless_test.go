package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

type nopLog struct{}

func (nopLog) Append(context.Context, event.Draft) (event.Receipt, error) {
	return event.Receipt{Seq: 1}, nil
}

// syncBuf is a goroutine-safe diagnostic stream.
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

// TestTerminalAnswersApprovals (DH-T-CLI-13): the cli answerer shows the
// approval on the diagnostic stream and answers from the terminal's next line,
// as principal cli on channel cli; y allows once, anything else or a closed
// terminal denies.
func TestTerminalAnswersApprovals(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		allowed  bool
	}{
		{"yes allows once", "y\n", true},
		{"no denies", "n\n", false},
		{"a closed terminal denies", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := cordis.New()
			t.Cleanup(func() { root.Dispose() })
			reg := human.New()
			diag := &syncBuf{}
			if _, err := reg.RegisterAnswerer(root, ChannelCLI, newTerminal(reg, strings.NewReader(tc.in), diag)); err != nil {
				t.Fatal(err)
			}
			me := agent.Ref{Session: session.ID("s-cli"), Incarnation: 1}
			if _, err := reg.Bind(root, me, human.Binding{Channel: ChannelCLI}); err != nil {
				t.Fatal(err)
			}
			d, err := approval.New(approval.Config{Registry: reg}).Request(t.Context(), nopLog{},
				approval.Request{Agent: me, Tool: "bash", Call: "call-1", Origin: "model", Display: `{"cmd":"ls"}`})
			if err != nil {
				t.Fatal(err)
			}
			want := approval.By{Principal: "cli", Channel: "cli", Peer: "terminal"}
			if d.Approved() != tc.allowed || d.By != want {
				t.Fatalf("decision %+v, want approved=%v by %+v", d, tc.allowed, want)
			}
			if tc.allowed && d.Outcome != approval.OutcomeAllowedOnce {
				t.Fatalf("outcome %s, want allowed-once", d.Outcome)
			}
			if !strings.Contains(diag.String(), "approve bash") {
				t.Fatalf("no prompt on the diagnostic stream: %q", diag.String())
			}
		})
	}
}

type idleApp struct{ ready, stopping chan struct{} }

func (a idleApp) Ready() <-chan struct{}    { return a.ready }
func (a idleApp) Stopping() <-chan struct{} { return a.stopping }
func (idleApp) Stop(launch.Reason, int)     {}

// TestReadTaskBound: a task of exactly the bound is read whole; one byte more is
// refused, never cut and run.
func TestReadTaskBound(t *testing.T) {
	for _, tc := range []struct {
		in   string
		fail bool
	}{{"abcd", false}, {"abcde", true}} {
		r := &runner{app: idleApp{make(chan struct{}), make(chan struct{})}, in: strings.NewReader(tc.in), maxTask: 4}
		task, ok, err := r.readTask(t.Context())
		if !ok {
			t.Fatalf("%q: not read", tc.in)
		}
		if tc.fail != errors.Is(err, errTaskTooLong) || (!tc.fail && task != tc.in) || (tc.fail && task != "") {
			t.Fatalf("%q: task %q, err %v", tc.in, task, err)
		}
	}
}

// TestParseCommand: --command takes a "/name rest" line and no task words.
func TestParseCommand(t *testing.T) {
	a, err := Parse([]string{"--command", "/echo hi"})
	if err != nil || a.Command != "/echo hi" || a.Stdin || a.Task != "" {
		t.Fatalf("Parse: %+v, %v", a, err)
	}
	for _, words := range [][]string{{"--command", "echo"}, {"--command=/echo", "task"}} {
		if _, err := Parse(words); err == nil {
			t.Errorf("Parse(%q): %v, want a usage error", words, err)
		}
	}
}

// assistant is a committed assistant/message event with blocks.
func assistant(t *testing.T, seq event.Seq, blocks ...vocab.Block) event.Event {
	t.Helper()
	m, err := vocab.NewMessage(vocab.RoleAssistant, blocks...)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(event.AssistantMessagePayload{Message: m})
	if err != nil {
		t.Fatal(err)
	}
	return event.Of(event.Record{Seq: seq, Type: event.AssistantMessage.Name(), Version: event.AssistantMessage.Version(), Payload: string(raw)})
}

// TestResultIsTheLastAssistantMessage: a later assistant message with no text
// (tool calls only) replaces an earlier one's text, in text and JSON results.
func TestResultIsTheLastAssistantMessage(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		var out bytes.Buffer
		r := &runner{out: &out, diag: io.Discard}
		f := &follower{r: r, json: asJSON}
		f.handle(assistant(t, 1, vocab.TextBlock("looking")))
		f.handle(assistant(t, 2, vocab.ToolUseBlock("c1", "read", json.RawMessage(`{}`))))
		if f.text != "" {
			t.Fatalf("json %v: text %q kept from an earlier message", asJSON, f.text)
		}
		out.Reset()
		if err := r.result("s", f); err != nil {
			t.Fatal(err)
		}
		if asJSON && strings.Contains(out.String(), "looking") || !asJSON && out.Len() != 0 {
			t.Fatalf("json %v: result %q", asJSON, out.String())
		}
	}
}
