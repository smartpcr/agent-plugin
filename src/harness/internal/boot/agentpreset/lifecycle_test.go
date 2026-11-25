package agentpreset_test

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
)

// hookSession runs onRead before the first Read and onAppend before the first
// Append of the wrapped session.
type hookSession struct {
	session.Session
	readOnce, appendOnce sync.Once
	onRead, onAppend     func()
}

func (s *hookSession) Read(ctx context.Context, from session.Offset, limit int, opts ...session.ReadOption) ([]event.Event, error) {
	if s.onRead != nil {
		s.readOnce.Do(s.onRead)
	}
	return s.Session.Read(ctx, from, limit, opts...)
}

func (s *hookSession) Append(ctx context.Context, d event.Draft) (event.Receipt, error) {
	if s.onAppend != nil {
		s.appendOnce.Do(s.onAppend)
	}
	return s.Session.Append(ctx, d)
}

func types(t *testing.T, h session.Handle) []string {
	t.Helper()
	evs, err := h.Read(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Type()
	}
	return out
}

func entry(t *testing.T, id, name string, cfg any) loader.EntryOptions {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return loader.EntryOptions{ID: id, Name: name, Config: raw}
}

// bundle-preset.md §6.1, §6.3: agent.presets and agent.preset entries of a host
// loader populate the registry as the tree loads (a default declared after the
// registry is no error); unloading a declaration entry retires its revision,
// which closes at its last lease's release.
func TestComponentsPopulateTheRegistry(t *testing.T) {
	fx := newFixture(t)
	root := cordis.New()
	t.Cleanup(root.Dispose)
	other := standard("v2")
	other.ID = "other"
	base := []loader.EntryOptions{
		{ID: "scope", Name: "session.scope"},
		entry(t, "presets", agentpreset.RegistryName, agentpreset.Config{Default: "standard"}),
		entry(t, "standard", agentpreset.DeclarationName, standard("v1")),
	}
	layer := func(es ...loader.EntryOptions) loader.Layer {
		return loader.Layer{Label: "host", Ops: []loader.Op{{Kind: loader.OpInsert, Entries: es}}}
	}
	host, err := loader.New(root, loader.Options{Resolver: fx.cat}, layer(append(slices.Clone(base), entry(t, "other", agentpreset.DeclarationName, other))...))
	if err != nil {
		t.Fatal(err)
	}
	host.WaitAll()
	reg, err := agentpreset.Key.Get(root)
	if err != nil {
		t.Fatalf("agent.presets provides no registry: %v", err)
	}
	var ids []string
	for _, in := range reg.List() {
		if in.Broken != "" {
			t.Fatalf("%s is broken: %s", in.ID, in.Broken)
		}
		ids = append(ids, in.ID)
	}
	if strings.Join(ids, ",") != "other,standard" || reg.Default() != "standard" || fx.live.Load() != 2 {
		t.Fatalf("roster %v, default %q, %d children live", ids, reg.Default(), fx.live.Load())
	}
	l, err := reg.Acquire(root, "other")
	if err != nil {
		t.Fatal(err)
	}

	if err := host.ApplyLayers([]loader.Layer{layer(base...)}); err != nil {
		t.Fatal(err)
	}
	host.WaitAll()
	if _, err := reg.Resolve("other"); !errors.Is(err, agentpreset.ErrNotFound) {
		t.Fatalf("resolve other after its entry unloaded = %v", err)
	}
	if r, ok := revision(reg.Revisions(), "other", 1); !ok || !r.Retired || r.Leases != 1 {
		t.Fatalf("other's revision = %+v %v, want retired with its one lease", r, ok)
	}
	l.Release()
	if _, ok := revision(reg.Revisions(), "other", 1); ok || fx.live.Load() != 1 {
		t.Fatalf("other's revision outlived its last lease (%d children live)", fx.live.Load())
	}

	// A duplicate id fails its own entry and leaves the other's revision alone.
	dup := entry(t, "dup", agentpreset.DeclarationName, standard("v3"))
	if err := host.ApplyLayers([]loader.Layer{layer(append(slices.Clone(base), dup)...)}); err != nil {
		t.Fatal(err)
	}
	host.WaitAll()
	if f := host.Fiber(host.Find("dup")); f == nil || !errors.Is(f.Err(), agentpreset.ErrInvalid) {
		t.Fatalf("a duplicate declaration loaded: %v", f)
	}
	if in, _ := reg.Resolve("standard"); in.Revision != 1 {
		t.Fatalf("standard rebuilt to rev %d by a refused duplicate", in.Revision)
	}
}

// §6.3: a child that waits for a service that never comes leaves its revision
// broken once the tree settles, and Acquire refuses it.
func TestPendingChildIsBroken(t *testing.T) {
	e := newEnv(t, memstore.New())
	if err := e.reg.Load(agentpreset.Config{Default: "needy"}, []agentpreset.Declaration{
		{ID: "needy", Children: map[string]*agentpreset.Child{"tool.needs": {}}},
	}); err != nil {
		t.Fatal(err)
	}
	if r, ok := revision(e.reg.Revisions(), "needy", 1); !ok || !strings.Contains(r.Broken, "tool.missing") {
		t.Fatalf("needy's revision = %+v %v, want broken naming tool.missing", r, ok)
	}
	if l, err := e.reg.Acquire(e.root, ""); !errors.Is(err, agentpreset.ErrInvalid) || l != nil {
		t.Fatalf("acquire needy = %v %v, want agentpreset/invalid", l, err)
	}
}

// DH-T-BUNDLE-7, D-4: a child waiting for a host service is audited as it stands,
// not when it was built: the same revision becomes usable once the host provides
// the service, and broken again once it is withdrawn.
func TestLateHostServiceMakesThePresetUsable(t *testing.T) {
	e := newEnv(t, memstore.New())
	if err := e.reg.Load(agentpreset.Config{Default: "needy"}, []agentpreset.Declaration{
		{ID: "needy", Children: map[string]*agentpreset.Child{"tool.needs": {}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.reg.Acquire(e.root, ""); !errors.Is(err, agentpreset.ErrInvalid) {
		t.Fatalf("acquire before the provider = %v, want agentpreset/invalid", err)
	}
	// The child reacts to the host's service on the tree's own schedule: wait for
	// its state to change, then audit the revision as it stands.
	states := make(chan cordis.FiberState, 64)
	t.Cleanup(e.root.Internal().OnStatus(func(f *cordis.Fiber, _, next cordis.FiberState) {
		if f.Name() == "tool.needs" {
			select {
			case states <- next:
			default:
				t.Errorf("tool.needs state %v dropped: the test's buffer is full", next)
			}
		}
	}))
	await := func(want cordis.FiberState, what string) {
		t.Helper()
		timeout := time.After(10 * time.Second)
		for {
			select {
			case st := <-states:
				if st == want {
					return
				}
			case <-timeout:
				t.Fatalf("tool.needs never became %v %s", want, what)
			}
		}
	}
	h, err := e.root.Provide("tool.missing", "late")
	if err != nil {
		t.Fatal(err)
	}
	await(cordis.Active, "after the host provided tool.missing")
	l, err := e.reg.Acquire(e.root, "")
	if err != nil {
		t.Fatalf("acquire after the host provided tool.missing = %v", err)
	}
	if in := l.Info(); in.Revision != 1 || in.Broken != "" {
		t.Fatalf("lease info %+v, want the same revision 1, usable", in)
	}
	l.Release()

	h.Dispose()
	await(cordis.Pending, "after tool.missing was withdrawn")
	if in, _ := e.reg.Resolve("needy"); !strings.Contains(in.Broken, "tool.missing") {
		t.Fatalf("needy after tool.missing was withdrawn = %+v, want broken naming it", in)
	}
}

// DH-ADR-BUNDLE-6: a turn that tries to start while Select is binding waits for
// it, so the selection lands on a blank session and the turn follows it.
func TestSelectExcludesTurnStart(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, memstore.New())
	other := standard("v2")
	other.ID = "other"
	if err := e.reg.Load(agentpreset.Config{Default: "standard"}, []agentpreset.Declaration{standard("v1"), other}); err != nil {
		t.Fatal(err)
	}
	h, _, tg := e.agent(t, "s1")
	g := tg.Maintainer.(*gate)
	started := make(chan error, 1)
	var turns sync.WaitGroup
	// The turn waits only for the gate, which Select gives back when it returns.
	t.Cleanup(turns.Wait)
	hs := &hookSession{Session: h}
	hs.onRead = func() {
		turns.Add(1)
		go func() {
			defer turns.Done()
			g.mu.Lock() // the driver's turn takes the gate before it opens
			defer g.mu.Unlock()
			d, err := event.TurnStartType.Draft(event.TurnStart{Turn: 1})
			if err == nil {
				_, err = h.Append(ctx, d)
			}
			started <- err
		}()
		select { // a turn not held back commits here, inside the selection
		case err := <-started:
			started <- err
		case <-time.After(150 * time.Millisecond):
		}
	}
	tg.Session = hs
	if _, err := e.reg.Select(ctx, tg, "other"); err != nil {
		t.Fatalf("select = %v", err)
	}
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	got := types(t, h)
	sel, turn := slices.Index(got, agentpreset.SelectedType.Name()), slices.Index(got, "turn/start")
	if sel < 0 || turn < 0 || sel > turn {
		t.Fatalf("log %v: the selection is not before the turn", got)
	}
	if _, err := e.reg.Select(ctx, tg, "standard"); !errors.Is(err, agentpreset.ErrLocked) {
		t.Fatalf("select after the turn = %v", err)
	}
	tg.Maintainer = nil
	if _, err := e.reg.Select(ctx, tg, "standard"); err == nil {
		t.Fatal("a select without a maintenance gate ran")
	}
}

// Two bindings of one agent race: they are serialised, so one lease stays and the
// registry, the agent's enclosing scopes and the fold agree on the last one.
func TestConcurrentBindsOfOneAgent(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, memstore.New())
	other := standard("v2")
	other.ID = "other"
	if err := e.reg.Load(agentpreset.Config{Default: "standard"}, []agentpreset.Declaration{standard("v1"), other}); err != nil {
		t.Fatal(err)
	}
	h, sc, tg := e.agent(t, "s1")
	firstIn, release := make(chan struct{}), make(chan struct{})
	letGo := sync.OnceFunc(func() { close(release) })
	tg.Session = &hookSession{Session: h, onAppend: func() { close(firstIn); <-release }}
	var wg sync.WaitGroup
	t.Cleanup(func() { letGo(); wg.Wait() })
	errs := make([]error, 2)
	firstDone := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer close(firstDone)
		_, errs[0] = e.reg.Bind(ctx, tg, "standard", agentpreset.CauseCreate)
	}()
	select {
	case <-firstIn:
	case <-firstDone:
		t.Fatalf("the first bind returned without appending: %v", errs[0])
	}
	go func() { defer wg.Done(); _, errs[1] = e.reg.Bind(ctx, tg, "other", agentpreset.CauseCreate) }()
	awaitBlockedInLockTarget(t)
	letGo()
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("binds = %v", errs)
	}
	leases := 0
	for _, r := range e.reg.Revisions() {
		leases += r.Leases
	}
	of, _ := e.reg.Of(sc.Key())
	b := e.binding(t, h)
	root, _ := e.reg.Lookup(sc.Key(), "tool.read")
	want := map[string]string{"standard": "v1", "other": "v2"}[b.Preset]
	if leases != 1 || of.ID != b.Preset || of.Digest != b.Digest || root != want || len(e.scopes.Enclosing(sc.Key())) != 1 {
		t.Fatalf("leases %d, Of %s, fold %+v, sees %v, encloses %v", leases, of.ID, b, root, e.scopes.Enclosing(sc.Key()))
	}
}

// awaitBlockedInLockTarget returns once a goroutine is parked on a mutex inside
// Registry.lockTarget: a bind queued behind the one that holds its target's lock.
func awaitBlockedInLockTarget(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n == len(buf) {
			buf = make([]byte, 2*len(buf))
			continue
		}
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			// The header names the wait: "goroutine 7 [sync.Mutex.Lock]:".
			head, _, _ := strings.Cut(g, "\n")
			parked := strings.Contains(head, "Mutex") || strings.Contains(head, "semacquire")
			if parked && strings.Contains(g, ".(*Registry).lockTarget(") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the second bind never queued on the target's lock")
		}
		runtime.Gosched()
	}
}

// Lease.Info and the roster are read while reloads that keep the revision rename
// it (run with -race).
func TestLeaseInfoDuringMetadataReloads(t *testing.T) {
	e := newEnv(t, memstore.New())
	cfg := agentpreset.Config{Default: "standard"}
	if err := e.reg.Load(cfg, []agentpreset.Declaration{standard("v1")}); err != nil {
		t.Fatal(err)
	}
	l, err := e.reg.Acquire(e.root, "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { <-done }) // the reloads end on their own
	go func() {
		defer close(done)
		for i := range 50 {
			d := standard("v1")
			d.Name = "standard " + strconv.Itoa(i)
			if err := e.reg.Load(cfg, []agentpreset.Declaration{d}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for {
		select {
		case <-done:
			if in := l.Info(); in.Revision != 1 || in.Name != "standard 49" {
				t.Fatalf("lease info %+v, want rev 1 renamed", in)
			}
			return
		default:
			_ = l.Info()
			_ = e.reg.List()
		}
	}
}

// After the registry's owner unloads the roster is empty and nothing binds.
func TestUnloadEmptiesTheRoster(t *testing.T) {
	e := newEnv(t, memstore.New())
	if err := e.reg.Load(agentpreset.Config{Default: "standard"}, []agentpreset.Declaration{standard("v1")}); err != nil {
		t.Fatal(err)
	}
	e.root.Dispose()
	if l := e.reg.List(); len(l) != 0 || len(e.reg.Revisions()) != 0 || e.fx.live.Load() != 0 {
		t.Fatalf("after unload: roster %+v, %d children live", l, e.fx.live.Load())
	}
	if _, err := e.reg.Acquire(e.root, ""); !errors.Is(err, agentpreset.ErrClosed) {
		t.Fatalf("acquire after unload = %v", err)
	}
}
