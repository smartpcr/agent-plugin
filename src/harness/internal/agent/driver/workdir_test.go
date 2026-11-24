package driver_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
)

// statSeam is the filesystem seam's Stat over the real filesystem: a missing
// path is not an error (TC-01b).
type statSeam struct{}

func (statSeam) Stat(_ context.Context, r fs.Ref) (fs.Info, error) {
	fi, err := os.Stat(r.Path)
	if errors.Is(err, os.ErrNotExist) {
		return fs.Info{}, nil
	}
	if err != nil {
		return fs.Info{}, err
	}
	typ := fs.TypeFile
	if fi.IsDir() {
		typ = fs.TypeDir
	}
	return fs.Info{Exists: true, Type: typ, Target: fs.Target{Display: r.Path}}, nil
}

// newWorkdirLife is newLife whose driver hosts a working-directory service
// over the real filesystem.
func newWorkdirLife(t *testing.T, turns ...fakemodel.Turn) (*life, *workdir.Service) {
	t.Helper()
	reg := views.New()
	wd := must(workdir.New(reg, statSeam{}))
	return newLife(t, lifeOptions{folder: reg, workdir: wd, turns: turns}), wd
}

func tempDirs(t *testing.T, names ...string) []string {
	t.Helper()
	base := must(filepath.EvalSymlinks(t.TempDir()))
	var out []string
	for _, n := range names {
		d := filepath.Join(base, n)
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

func waitIdle(t *testing.T, a agent.Agent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	must(0, agent.WaitIdle(ctx, a))
}

// requestHas counts the messages of the request actually sent whose text
// contains sub.
func requestHas(t *testing.T, req session.Request, sub string) int {
	t.Helper()
	n := 0
	for _, m := range must(req.Messages()) {
		var b strings.Builder
		for _, bl := range m.Blocks() {
			if bl.Text != nil {
				b.WriteString(bl.Text.Text)
			}
		}
		if strings.Contains(b.String(), sub) {
			n++
		}
	}
	return n
}

// workdirNotices is the inbox messages from the working directory the
// session's log admitted, in order.
func workdirNotices(t *testing.T, s session.ReadSession) []agent.InboxMessage {
	t.Helper()
	var out []agent.InboxMessage
	for _, ev := range ofType(must(s.Read(bg, 0, 0)), agent.SplicedType.Name()) {
		for _, m := range must(agent.SplicedType.As(ev)).Inserted {
			if m.Source == workdir.NoticeSource {
				out = append(out, m)
			}
		}
	}
	return out
}

const lostText = "is no longer available"

// Stage 2.2 at the host: the request carries the context line, a host move
// reaches the agent's inbox and the next request, and once the directory is
// gone the guard refuses each tool call with workdir/unavailable while exactly
// one notice reaches the inbox and the model (TC-01b).
func TestWorkdirReachesRequestsToolsAndInbox(t *testing.T) {
	dirs := tempDirs(t, "a", "b")
	a, b := dirs[0], dirs[1]
	l, wd := newWorkdirLife(t,
		fakemodel.Reply("one"),
		fakemodel.Calls(echo("c1")), fakemodel.Reply("two"),
		fakemodel.Calls(echo("c2"), echo("c3")), fakemodel.Reply("three"))
	h := must(l.reg.Create(l.root, agent.CreateOptions{Session: "w", Model: agent.ModelSelection{Route: route}, Cwd: a}))
	ag := h.Agent()
	must(agent.Send(bg, ag, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "go"}))
	waitIdle(t, ag)
	if n := requestHas(t, l.sent.requests()[0], workdir.ContextLine(a)); n != 1 {
		t.Fatalf("the first request carries the context line of %s %d times, want once", a, n)
	}

	must(wd.Change(bg, "w", b))
	must(agent.Send(bg, ag, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "move on"}))
	waitIdle(t, ag)
	// The moved notice and the new runtime-context snapshot both say where.
	if n := requestHas(t, l.sent.requests()[1], workdir.ContextLine(b)); n != 2 {
		t.Fatalf("the request after the move names %s %d times, want the notice and the context line", b, n)
	}

	for _, d := range dirs {
		if err := os.RemoveAll(d); err != nil {
			t.Fatal(err)
		}
	}
	must(agent.Send(bg, ag, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "third"}))
	waitIdle(t, ag)

	results := map[string]event.ToolResultPayload{}
	for _, ev := range ofType(must(ag.Session().Read(bg, 0, 0)), event.ToolResult.Name()) {
		p := must(event.ToolResult.As(ev))
		results[p.CallID] = p
	}
	if r := results["c1"]; r.IsError {
		t.Fatalf("c1 ran in an existing directory and failed: %+v", r)
	}
	for _, id := range []string{"c2", "c3"} {
		r, ok := results[id]
		raw, _ := json.Marshal(r)
		if !ok || !r.IsError || !strings.Contains(string(raw), string(workdir.CodeUnavailable)) {
			t.Fatalf("%s in a deleted directory = %s, want refused with workdir/unavailable", id, raw)
		}
	}
	var lost int
	for _, m := range workdirNotices(t, ag.Session()) {
		if strings.Contains(m.Text, lostText) {
			lost++
		}
	}
	if lost != 1 {
		t.Fatalf("%d unavailable notices reached the inbox, want exactly one", lost)
	}
	reqs := l.sent.requests()
	if len(reqs) != 5 || requestHas(t, reqs[4], lostText) != 1 {
		t.Fatalf("the request after the refused calls does not carry the notice once (%d requests)", len(reqs))
	}
	must(0, h.Dispose(bg))
	if _, err := wd.Access("w"); herr.Kind(err) != workdir.CodeUnavailable {
		t.Fatalf("Access after the agent was disposed: %v", err)
	}
}

// brokenContext is the service whose prompt context line cannot be read: the
// fold's storage fails.
type brokenContext struct {
	*workdir.Service
	err error
}

func (b brokenContext) ContextLine(session.ID) (string, error) { return "", b.err }

// A context line that cannot be read for any reason but an unavailable
// directory fails the step with driver/prompt: no request goes to the model
// without the working directory it requires.
func TestWorkdirContextReadFailureIsNotSwallowed(t *testing.T) {
	dir := tempDirs(t, "a")[0]
	reg := views.New()
	svc := must(workdir.New(reg, statSeam{}))
	broken := errors.New("checkpoint store unreadable")
	l := newLife(t, lifeOptions{folder: reg, workdir: brokenContext{Service: svc, err: broken}})
	h := must(l.reg.Create(l.root, agent.CreateOptions{Session: "x", Model: agent.ModelSelection{Route: route}, Cwd: dir}))
	must(agent.Send(bg, h.Agent(), agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "go"}))
	waitIdle(t, h.Agent())
	if n := len(l.sent.requests()); n != 0 {
		t.Fatalf("%d requests went out without the working-directory context", n)
	}
	ends := turnEnds(t, must(h.Agent().Session().Read(bg, 0, 0)))
	if len(ends) != 1 || ends[0].Kind != event.ReasonError || ends[0].By != "driver/prompt" || !strings.Contains(ends[0].Detail, broken.Error()) {
		t.Fatalf("turn ends %+v, want one driver/prompt error naming the read failure", ends)
	}
	must(0, h.Dispose(bg))
}

// A resume is a check: the fold is rebuilt from the log, a directory gone
// while the agent was down puts one notice in the resumed agent's inbox, and
// the next turn's request carries it with the context line (TC-01b).
func TestWorkdirResumeChecksTheDirectory(t *testing.T) {
	dir := tempDirs(t, "a")[0]
	l, wd := newWorkdirLife(t, fakemodel.Reply("one"), fakemodel.Reply("two"))
	h := must(l.reg.Create(l.root, agent.CreateOptions{Session: "r", Model: agent.ModelSelection{Route: route}, Cwd: dir}))
	must(agent.Send(bg, h.Agent(), agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "go"}))
	waitIdle(t, h.Agent())
	must(0, h.Dispose(bg))
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	h2 := must(l.reg.Resume(l.root, agent.CreateOptions{Session: "r", Model: agent.ModelSelection{Route: route}}))
	ns := workdirNotices(t, h2.Agent().Session())
	if len(ns) != 1 || !strings.Contains(ns[0].Text, lostText) {
		t.Fatalf("the resume queued %+v, want one unavailable notice", ns)
	}
	if _, err := wd.Access("r"); herr.Kind(err) != workdir.CodeUnavailable {
		t.Fatalf("Access of the resumed session: %v", err)
	}

	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	must(agent.Send(bg, h2.Agent(), agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "wake"}))
	waitIdle(t, h2.Agent())
	req := l.sent.requests()[1]
	if requestHas(t, req, lostText) != 1 || requestHas(t, req, workdir.ContextLine(dir)) != 1 {
		t.Fatalf("the request after the resume lacks the notice or the context line of %s", dir)
	}
	if ns := workdirNotices(t, h2.Agent().Session()); len(ns) != 1 {
		t.Fatalf("the directory's return queued more notices: %+v", ns)
	}
	must(0, h2.Dispose(bg))
}

// spliceStore is a store whose writers fail the flush of a batch that admits a
// working-directory notice while fail is positive, and report whether such an
// admission is still waiting for a flush.
type spliceStore struct {
	store.Store
	fail  atomic.Int32
	dirty atomic.Bool
}

type spliceWriter struct {
	store.Writer
	s *spliceStore
	// armed: the batch since the last flush admits a workdir notice.
	armed atomic.Bool
}

func (s *spliceStore) Create(ctx context.Context, h store.Header, o store.CreateOptions) (store.Writer, error) {
	w, err := s.Store.Create(ctx, h, o)
	if err != nil {
		return nil, err
	}
	return &spliceWriter{Writer: w, s: s}, nil
}

func (s *spliceStore) OpenWrite(ctx context.Context, id string, o store.OpenOptions) (store.Writer, error) {
	w, err := s.Store.OpenWrite(ctx, id, o)
	if err != nil {
		return nil, err
	}
	return &spliceWriter{Writer: w, s: s}, nil
}

func (w *spliceWriter) Append(ctx context.Context, b store.Batch) error {
	if err := w.Writer.Append(ctx, b); err != nil {
		return err
	}
	for _, r := range b.Records {
		if r.Type == agent.SplicedType.Name() && strings.Contains(r.Payload, `"source":"`+workdir.NoticeSource+`"`) {
			w.armed.Store(true)
			w.s.dirty.Store(true)
		}
	}
	return nil
}

var errFlushDown = errors.New("flush down")

func (w *spliceWriter) Flush(ctx context.Context) error {
	if w.armed.Load() && w.s.fail.Load() > 0 {
		w.s.fail.Add(-1)
		return errFlushDown
	}
	if err := w.Writer.Flush(ctx); err != nil {
		return err
	}
	w.armed.Store(false)
	w.s.dirty.Store(false)
	return nil
}

// A notice the inbox admitted but could not flush is not acknowledged by the
// duplicate the retry meets: the retry syncs the inbox first, so the notice is
// durable before the service stops owing it, and is admitted once.
func TestWorkdirNoticeIsDurableBeforeItIsAcknowledged(t *testing.T) {
	dirs := tempDirs(t, "a", "b")
	reg := views.New()
	wd := must(workdir.New(reg, statSeam{}))
	st := &spliceStore{Store: memstore.New()}
	l := newLife(t, lifeOptions{folder: reg, workdir: wd, store: st})
	h := must(l.reg.Create(l.root, agent.CreateOptions{Session: "d", Model: agent.ModelSelection{Route: route}, Cwd: dirs[0]}))

	st.fail.Store(1)
	if _, err := wd.Change(bg, "d", dirs[1]); !workdir.OnlyUndelivered(err) || !errors.Is(err, errFlushDown) {
		t.Fatalf("Change whose notice flush fails: %v", err)
	}
	if !st.dirty.Load() {
		t.Fatal("the notice's admission was not appended")
	}

	st.fail.Store(1)
	if _, err := wd.Ensure(bg, "d"); !workdir.OnlyUndelivered(err) || !errors.Is(err, errFlushDown) {
		t.Fatalf("Ensure while the inbox still cannot flush: %v", err)
	}
	if !st.dirty.Load() {
		t.Fatal("the notice was acknowledged while its admission is not durable")
	}

	must(wd.Ensure(bg, "d"))
	if st.dirty.Load() {
		t.Fatal("Ensure acknowledged the notice without making the inbox durable")
	}
	if ns := workdirNotices(t, h.Agent().Session()); len(ns) != 1 || ns[0].Text != workdir.ContextLine(dirs[1]) {
		t.Fatalf("inbox holds %+v, want the moved notice once", ns)
	}
	must(0, h.Dispose(bg))
}
