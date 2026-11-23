package tool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/policy"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/tool"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

const scenarioSession = "s-spill-read"

type noIn struct{}

// pipeline is the real tool pipeline on a scope table, with the spill policy
// and spill_read mounted over sp the way policy.Mount mounts the policy.
func pipeline(t *testing.T, sp spill.Store) (*cordis.Context, tools.Service, *policy.Policy) {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	f, err := root.Use(scope.Component, cordis.None{})
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	var tab scope.Table
	g, err := root.Inject([]string{scope.TableKey.Name()}, func(c *cordis.Context) error {
		var err error
		tab, err = scope.TableKey.Get(c)
		return err
	})
	if err == nil {
		err = g.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	svc := core.NewWith(root, tab, core.Config{Checkpoint: checkpoint.Default{}})
	pol, _, err := policy.Mount(root, svc, sp, policy.Config{ReadTool: tool.Name, Now: func() time.Time {
		return time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, mounted, err := tool.Mount(root, svc, sp, nil, tool.Config{MaxBytes: pol.Budget()}); err != nil || !mounted {
		t.Fatalf("mount spill_read: mounted %v, %v", mounted, err)
	}
	return root, svc, pol
}

// openStep is a session in st whose step's assistant message asks for calls.
func openStep(t *testing.T, root *cordis.Context, st *sqlite.Store, id string, calls []tools.CallRequest) session.Handle {
	t.Helper()
	h, err := session.NewService(root, st, session.Config{}).Create(bg, nil, session.CreateOptions{Header: session.Header{ID: id}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("go"))
	if err != nil {
		t.Fatal(err)
	}
	var uses []vocab.Block
	for _, c := range calls {
		uses = append(uses, vocab.ToolUseBlock(c.ID, c.Name, json.RawMessage(c.Arguments)))
	}
	asked, err := vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, uses...)
	if err != nil {
		t.Fatal(err)
	}
	for _, mk := range []func() (event.Draft, error){
		func() (event.Draft, error) { return event.TurnStartType.Draft(event.TurnStart{Turn: 1}) },
		func() (event.Draft, error) { return event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1}) },
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
		func() (event.Draft, error) {
			return event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: asked})
		},
	} {
		d, err := mk()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.Append(bg, d); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func execute(t *testing.T, svc tools.Service, h session.Handle, who agent.Ref, calls ...tools.CallRequest) []tools.Result {
	t.Helper()
	v, err := svc.Resolve(bg, tools.Target{Agent: who})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Release()
	res, err := svc.ExecuteBatch(bg, tools.Batch{View: v, Log: h, Calls: calls})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != len(calls) {
		t.Fatalf("%d results for %d calls", len(res.Results), len(calls))
	}
	return res.Results
}

// logged is the tool/result payloads of h by call id.
func logged(t *testing.T, h session.Handle) map[string]event.ToolResultPayload {
	t.Helper()
	evs, err := h.Read(bg, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]event.ToolResultPayload{}
	for _, ev := range evs {
		if ev.Type() != "tool/result" {
			continue
		}
		var p event.ToolResultPayload
		if err := json.Unmarshal([]byte(ev.Raw()), &p); err != nil {
			t.Fatal(err)
		}
		out[p.CallID] = p
	}
	return out
}

// Scenario: The model reads the rest of a spilled result (plan 16.3,
// M1-G-PIPE-1). Given a 2 MB result spilled on the SQLite-backed store with
// spill_read mounted, When spill_read is called with offset 1 MiB and limit
// 32 KiB, and then with a pattern, Then each result is the exact slice, is
// within budget and has no spill field. A session outside the owner's line gets
// spill/not-owner.
//
//dsh:test DH-T-STORAGE-13
func TestModelReadsTheRestOfASpilledResult(t *testing.T) {
	l := home.New(t.TempDir())
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.OpenHome(bg, l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	sp, err := local.New(l, local.DefaultConfig, local.WithLineage(local.ForkLineage(st)))
	if err != nil {
		t.Fatal(err)
	}
	root, svc, pol := pipeline(t, sp)
	full := numbered(2 << 20)
	if _, err := svc.Register(root, tools.Define(tools.Spec[noIn, string]{
		Name: "big", Description: "Prints 2 MB.",
		Run: func(context.Context, noIn, tools.Run) (string, error) { return full, nil },
	})); err != nil {
		t.Fatal(err)
	}
	me := agent.Ref{Session: session.ID(scenarioSession), Incarnation: 1}

	// The 2 MB result is spilled, and its notice names spill_read and the ref.
	h := openStep(t, root, st, scenarioSession, []tools.CallRequest{{ID: "c1", Name: "big", Arguments: `{}`}})
	big := execute(t, svc, h, me, tools.CallRequest{ID: "c1", Name: "big", Arguments: `{}`})[0]
	if big.IsError || big.Spill == nil || big.Spill.Bytes != int64(len(full)) || len(big.Text) > pol.Budget() {
		t.Fatalf("big: %+v", big.Spill)
	}
	ref := big.Spill.Path
	if !strings.Contains(big.Text, "saved to "+ref+",") || !strings.Contains(big.Text, "Use "+tool.Name) {
		t.Fatalf("the notice does not name spill_read and the ref: %q", big.Text[len(big.Text)-300:])
	}

	// spill_read by offset and limit, then by pattern.
	const offset, limit = 1 << 20, 32 << 10
	first := 1<<20/13 + 1 // the first whole line after 1 MiB
	pattern := fmt.Sprintf(`^line %07d$|^line %07d$`, first+10, first+500)
	args := func(v map[string]any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	calls := []tools.CallRequest{
		{ID: "r1", Name: tool.Name, Arguments: args(map[string]any{"ref": ref, "offset": offset, "limit": limit})},
		{ID: "r2", Name: tool.Name, Arguments: args(map[string]any{"ref": ref, "offset": offset, "limit": limit, "pattern": pattern})},
	}
	h2 := openStep(t, root, st, scenarioSession+"-2", calls)
	res := execute(t, svc, h2, me, calls...)
	wantGrep := fmt.Sprintf("%d:line %07d\n%d:line %07d\n", (first+10)*13, first+10, (first+500)*13, first+500)
	for i, want := range []string{full[offset : offset+limit], wantGrep} {
		r := res[i]
		if r.IsError || r.Text != want {
			t.Fatalf("%s: error %v (%s), %d bytes, want %d: %q", r.CallID, r.IsError, r.Code, len(r.Text), len(want), r.Text[:min(len(r.Text), 200)])
		}
		if r.Spill != nil || r.Truncated || len(r.Text) > pol.Budget() {
			t.Fatalf("%s: spill %+v, truncated %v, %d bytes of a %d budget", r.CallID, r.Spill, r.Truncated, len(r.Text), pol.Budget())
		}
		if p := logged(t, h2)[r.CallID]; p.Spill != nil {
			t.Fatalf("%s: the logged result has a spill field %+v", r.CallID, p.Spill)
		}
	}

	// Another session, not forked from the owner, may not read it.
	stranger := agent.Ref{Session: session.ID("s-stranger"), Incarnation: 1}
	sc := []tools.CallRequest{{ID: "x1", Name: tool.Name, Arguments: args(map[string]any{"ref": ref})}}
	h3 := openStep(t, root, st, "s-stranger", sc)
	if r := execute(t, svc, h3, stranger, sc...)[0]; !r.IsError || !strings.Contains(r.Text, string(spill.ReasonNotOwner)) {
		t.Fatalf("a stranger: %+v", r)
	}
}
