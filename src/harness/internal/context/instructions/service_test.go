package instructions_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/context/instructions"
	"github.com/smartpcr/agent-plugin/src/harness/internal/context/request"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/ops"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	shelltools "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/tools"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return a, b
}

// dirs is a working-directory lookup a test moves.
type dirs struct {
	mu        sync.Mutex
	path, top string
}

func (d *dirs) Access(session.ID) (workdir.Access, error) { return d, nil }
func (d *dirs) Current() (workdir.WorkingDirectory, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return workdir.WorkingDirectory{Path: d.path, Root: d.top, State: workdir.Available}, nil
}
func (d *dirs) Path() (string, error) { w, _ := d.Current(); return w.Path, nil }
func (d *dirs) Root() (string, error) { w, _ := d.Current(); return w.Root, nil }
func (d *dirs) move(p string)         { d.mu.Lock(); d.path = p; d.mu.Unlock() }

// sent keeps every request the driver streamed.
type sent struct {
	driver.Models
	mu   sync.Mutex
	reqs []session.Request
}

func (s *sent) Prepare(ctx context.Context, rt vocab.RouteID, model string, c vocab.Controls) (llm.Prepared, error) {
	p, err := s.Models.Prepare(ctx, rt, model, c)
	return streamed{Prepared: p, s: s}, err
}

func (s *sent) last(t *testing.T) []vocab.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return must(s.reqs[len(s.reqs)-1].Messages())
}

type streamed struct {
	llm.Prepared
	s *sent
}

func (p streamed) Stream(ctx context.Context, req session.Request, sink llm.AttemptSink) iter.Seq[vocab.Chunk] {
	p.s.mu.Lock()
	p.s.reqs = append(p.s.reqs, req)
	p.s.mu.Unlock()
	return p.Prepared.Stream(ctx, req, sink)
}

type stubAgent struct {
	agent.Agent
	ref agent.Ref
}

func (a stubAgent) Ref() agent.Ref      { return a.ref }
func (a stubAgent) ScopeKey() scope.Key { return scope.Key{} }

// shadow is a compaction seam that, when armed, replaces every instructions
// message on the surface with one checkpoint.
type shadow struct {
	armed atomic.Bool
	log   func() request.Log
}

func (c *shadow) Consider(ctx context.Context, _ agent.Ref, _ request.Trigger) (*request.Result, error) {
	if !c.armed.Swap(false) {
		return nil, nil
	}
	log := c.log()
	h := log.History()
	var seqs []event.Seq
	for i := range h.Len() {
		n := h.At(i)
		if m, err := n.Message(); err == nil {
			if src, ok := m.Source(); ok && src.Kind == request.KindInstructions {
				seqs = append(seqs, n.Seq())
			}
		}
	}
	if len(seqs) == 0 {
		return nil, nil
	}
	// A replacement must cite every node it covers, not just the instructions.
	first, last := seqs[0], seqs[len(seqs)-1]
	var cited []event.Seq
	for i := range h.Len() {
		if s := h.At(i).Seq(); s >= first && s <= last {
			cited = append(cited, s)
		}
	}
	cp := must(vocab.NewMessage(vocab.RoleUser, vocab.TextBlock("Summary: the instructions were read.")))
	cp = must(cp.WithSource(vocab.Source{Kind: "compaction-checkpoint", Producers: []string{"test.compact"}, Data: json.RawMessage(`{"id":"c-1"}`)}))
	d := must(event.UserMessage.Draft(event.Replace(first, last), event.UserMessagePayload{Message: cp}, cited...))
	rc, err := log.Append(ctx, d)
	if err != nil {
		return nil, err
	}
	return &request.Result{ID: "c-1", Checkpoint: rc.Seq, Shadowed: seqs, Outcome: "summarized"}, nil
}

// failing is a required contributor that rejects the step while armed.
type failing struct{ armed atomic.Bool }

func (*failing) Name() string   { return request.KindClock }
func (*failing) Required() bool { return true }
func (f *failing) Contribute(context.Context, request.Input) (request.Output, error) {
	if f.armed.Swap(false) {
		return request.Output{}, errors.New("the clock is down")
	}
	return request.Output{}, nil
}

// instructionsIn is the text of each instructions message of msgs.
func instructionsIn(msgs []vocab.Message) []string {
	var out []string
	for _, m := range msgs {
		if src, ok := m.Source(); ok && src.Kind == request.KindInstructions {
			out = append(out, string(must(json.Marshal(m.Blocks()))))
		}
	}
	return out
}

// TestServiceFollowsTheSession: the service on a driven agent with the real
// pipeline, ops and provider, and no watch, so only the session's events move
// it. The first step enters the baseline; a file-tool write of AGENTS.md (the
// seam's fs.Observed) puts the new text in the next request; a working
// directory change brings the subdirectory's file; a compaction that shadows
// the instructions has the baseline entered again in that step; a step a later
// required contributor rejected does not count as seen, so the next one
// carries the change; a shell's rewrite is seen once its settled call names its
// working directory (tools.Settled).
//
//dsh:test DH-T-CONTEXT-6
func TestServiceFollowsTheSession(t *testing.T) {
	bg := context.Background()
	ws := t.TempDir()
	sub := filepath.Join(ws, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for p, text := range map[string]string{filepath.Join(ws, "AGENTS.md"): "rules v1\n", filepath.Join(sub, "AGENTS.md"): "sub rules\n"} {
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	for _, c := range []*cordis.Component{scope.Component, core.Component} {
		must(0, must(root.Use(c, cordis.None{})).Wait())
	}
	var (
		svc tools.Service
		tab scope.Table
	)
	g := must(root.Inject([]string{tools.Key.Name(), scope.TableKey.Name()}, func(c *cordis.Context) (err error) {
		if svc, err = tools.Key.Get(c); err != nil {
			return err
		}
		tab, err = scope.TableKey.Get(c)
		return err
	}))
	must(0, g.Wait())

	const id = "s-instr"
	sess := must(session.NewService(root, memstore.New(), session.Config{}).Create(bg, nil, session.CreateOptions{Header: session.Header{ID: id}}))
	t.Cleanup(func() { _ = sess.Close(bg) })
	comp := &shadow{log: func() request.Log { return sess }}
	pipe, _ := must2(request.New(root, request.Options{Table: tab, Compact: comp, Sessions: func(agent.Ref) (request.Log, error) { return sess, nil }}))
	clock := &failing{}
	must(pipe.Register(root, clock))
	wd := &dirs{path: ws, top: ws}
	provider := local.New()
	isvc := must(instructions.Start(root, instructions.ServiceOptions{FS: provider, Workdir: wd, Pipeline: pipe, Views: views.New()}))
	seam := ops.New(root, provider, &sandboxtest.Resolver{Default: sandbox.WorkspaceWrite, Workdir: wd}, ops.Options{Workdir: wd})

	const route vocab.RouteID = "r/instr"
	rt := llm.New()
	fakemodel.Register(t, rt, route, fakemodel.NewTransport(t,
		fakemodel.Reply("1"), fakemodel.Reply("2"), fakemodel.Reply("3"), fakemodel.Reply("4"), fakemodel.Reply("5"),
		fakemodel.Reply("6"), fakemodel.Reply("7")))
	models := &sent{Models: rt}
	inbox := must(agent.OpenInbox(bg, sess))
	mod := must(agent.OpenModel(bg, sess, agent.ModelOptions{Initial: agent.ModelSelection{Route: route}}))
	drv := must(driver.Start(driver.Options{
		Context: root, Agent: stubAgent{ref: agent.Ref{Session: id, Incarnation: 1}},
		Session: sess, Inbox: inbox, Model: mod, Models: models, Tools: svc, Prompt: must(prompt.New(root, tab, prompt.Config{})),
	}))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(bg, 10*time.Second)
		defer cancel()
		_ = drv.Close(ctx)
	})
	turn := func(text string) {
		t.Helper()
		must(inbox.Send(bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: text}))
		ctx, cancel := context.WithTimeout(bg, 10*time.Second)
		defer cancel()
		if err := drv.WaitIdle(ctx); err != nil {
			t.Fatalf("wait idle: %v", err)
		}
	}
	expect := func(step, want string, form string) {
		t.Helper()
		got := instructionsIn(models.last(t))
		if len(got) == 0 || !strings.Contains(got[len(got)-1], want) {
			t.Fatalf("%s: the request's instructions %q do not hold %q", step, got, want)
		}
		rec := must(isvc.Visible(bg, sess))
		if rec.Form != form {
			t.Fatalf("%s: instructions.visible form %q, want %q", step, rec.Form, form)
		}
	}

	turn("one")
	expect("first step", "rules v1", instructions.FormBaseline)

	write := func(text string) {
		t.Helper()
		must(seam.Write(bg, ops.Call{Session: id, CallID: "c-" + text}, fs.Ref{Path: filepath.Join(ws, "AGENTS.md")}, []byte(text), fs.NoGuard))
	}
	write("rules v2\n")
	turn("two")
	expect("after a file-tool write", "rules v2", instructions.FormUpdate)

	wd.move(sub)
	turn("three")
	expect("after a directory change", "sub rules", instructions.FormUpdate)

	comp.armed.Store(true)
	turn("four")
	expect("after a compaction shadowed it", "sub rules", instructions.FormBaseline)

	write("rules v3\n")
	clock.armed.Store(true)
	turn("five")
	turn("six")
	expect("after a rejected step", "rules v3", instructions.FormUpdate)

	// A shell rewrites the file: no fs.Observed, no watch; the settled shell
	// call's working directory is the touch.
	if err := os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("sub rules v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	turn("seven")
	if got := instructionsIn(models.last(t)); len(got) != 0 && strings.Contains(got[len(got)-1], "sub rules v2") {
		t.Fatalf("an untouched chain was read again: %q", got)
	}
	ref := agent.Ref{Session: id, Incarnation: 1}
	call := tools.NewCall("c-sh", "bash", []byte("{}"), tools.OriginModel, "", ref, nil, nil)
	tools.Settled.Notify(root, scope.Key{}, tools.NewSettlement(call, tools.Result{CallID: "c-sh", Name: "bash", Value: shelltools.Result{Cwd: sub}}, "{}"))
	turn("eight")
	expect("after a shell call", "sub rules v2", instructions.FormUpdate)
	if n := len(models.reqs); n != 7 {
		t.Fatalf("%d requests sent, want 7 (the rejected step sends none)", n)
	}
}
