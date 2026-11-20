//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// cvTimeout bounds every blocking step so a deadlock fails instead of hanging.
const cvTimeout = 5 * time.Second

func cvWithin(what string, fn func()) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return nil
	case <-time.After(cvTimeout):
		return fmt.Errorf("%s did not return within %s", what, cvTimeout)
	}
}

type cvHits struct {
	mu    sync.Mutex
	calls map[string]int
	order []string
}

func (h *cvHits) hit(label string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.calls == nil {
		h.calls = map[string]int{}
	}
	h.calls[label]++
	h.order = append(h.order, label)
}

func (h *cvHits) count(label string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls[label]
}

type cvVerdict struct {
	Text string
	Seen string
}

type contextValuesWorld struct {
	root *cordis.Context
	logs *cordis.MemoryExporter
	hits *cvHits

	// scoped dispatch
	event   *cordis.EventKey[string]
	agents  map[string]*cordis.Fiber
	emitErr error

	// notify
	notifyErr    error
	notifyPanic  any
	errorsBefore int

	// around
	guard     *cordis.AroundKey[string, cvVerdict]
	result    cvVerdict
	aroundErr error
}

func (w *contextValuesWorld) newRoot() {
	if w.root != nil {
		return
	}
	root := cordis.New()
	root.Loggers().ClearExporters()
	w.logs = &cordis.MemoryExporter{}
	root.Loggers().AddExporter(w.logs)
	w.root = root
	w.hits = &cvHits{}
}

func (w *contextValuesWorld) load(name string, body func(ctx *cordis.Context) error) (*cordis.Fiber, error) {
	fiber, err := w.root.Apply(name, body)
	if err != nil {
		return nil, fmt.Errorf("apply %s: %w", name, err)
	}
	var waitErr error
	if err := cvWithin("wait "+name, func() { waitErr = fiber.Wait() }); err != nil {
		return nil, err
	}
	if waitErr != nil {
		return nil, fmt.Errorf("load %s: %w", name, waitErr)
	}
	return fiber, nil
}

func (w *contextValuesWorld) errorMessages() []cordis.LogMessage {
	var out []cordis.LogMessage
	for _, m := range w.logs.Messages() {
		if m.Level == cordis.LevelError {
			out = append(out, m)
		}
	}
	return out
}

// --- Scenario: Scoped dispatch reaches one subtree ---

func (w *contextValuesWorld) twoAgentSubtrees(a, b string) error {
	w.newRoot()
	w.event = cordis.NewEvent[string]("agent.fact")
	w.agents = map[string]*cordis.Fiber{}
	for _, name := range []string{a, b} {
		label := name
		fiber, err := w.load(label, func(ctx *cordis.Context) error {
			_, err := w.event.On(ctx, func(*cordis.Context, string) error {
				w.hits.hit(label)
				return nil
			})
			return err
		})
		if err != nil {
			return err
		}
		w.agents[label] = fiber
	}
	// Sanity: an unscoped emit reaches both subtrees, so the scoped result is meaningful.
	if err := w.event.Emit(w.root, "probe"); err != nil {
		return fmt.Errorf("unscoped emit: %w", err)
	}
	if w.hits.count(a) != 1 || w.hits.count(b) != 1 {
		return fmt.Errorf("unscoped emit should reach both subtrees, got %s=%d %s=%d", a, w.hits.count(a), b, w.hits.count(b))
	}
	w.hits = &cvHits{}
	return nil
}

func (w *contextValuesWorld) emitScopedTo(name string) error {
	scope, ok := w.agents[name]
	if !ok {
		return fmt.Errorf("unknown subtree %q", name)
	}
	if err := cvWithin("EmitScoped", func() { w.emitErr = w.event.EmitScoped(w.root, scope, "x") }); err != nil {
		return err
	}
	if w.emitErr != nil {
		return fmt.Errorf("EmitScoped: %w", w.emitErr)
	}
	return nil
}

func (w *contextValuesWorld) listenerRan(name string, times int) error {
	if got := w.hits.count(name); got != times {
		return fmt.Errorf("listener of subtree %q ran %d times, want %d", name, got, times)
	}
	return nil
}

// --- Scenario: A listener cannot veto a notified fact ---

func (w *contextValuesWorld) threeNotifyListeners() error {
	w.newRoot()
	w.event = cordis.NewEvent[string]("notified.fact")
	for i, name := range []string{"l1", "l2", "l3"} {
		label, panics := name, i == 1
		if _, err := w.load(label, func(ctx *cordis.Context) error {
			_, err := w.event.On(ctx, func(*cordis.Context, string) error {
				w.hits.hit(label)
				if panics {
					panic("boom")
				}
				return nil
			})
			return err
		}); err != nil {
			return err
		}
	}
	w.errorsBefore = len(w.errorMessages())
	return nil
}

func (w *contextValuesWorld) factNotified() error {
	return cvWithin("Notify", func() {
		defer func() { w.notifyPanic = recover() }()
		w.event.Notify(w.root, "f")
	})
}

func (w *contextValuesWorld) notifyReturnsNormally() error {
	if w.notifyPanic != nil {
		return fmt.Errorf("Notify propagated a panic: %v", w.notifyPanic)
	}
	return nil
}

func (w *contextValuesWorld) listenersOneAndThreeRan() error {
	w.hits.mu.Lock()
	order := strings.Join(w.hits.order, ",")
	w.hits.mu.Unlock()
	if order != "l1,l2,l3" {
		return fmt.Errorf("listeners ran in order %q, want l1,l2,l3", order)
	}
	return nil
}

func (w *contextValuesWorld) panicRecordedOnce() error {
	msgs := w.errorMessages()[w.errorsBefore:]
	if len(msgs) != 1 {
		texts := make([]string, len(msgs))
		for i, m := range msgs {
			texts[i] = m.Text
		}
		return fmt.Errorf("want exactly 1 error record, got %d: %q", len(msgs), texts)
	}
	if !strings.Contains(msgs[0].Text, "boom") {
		return fmt.Errorf("error record does not mention the panic: %q", msgs[0].Text)
	}
	if msgs[0].Name != "l2" {
		return fmt.Errorf("panic recorded on logger %q, want the owner l2", msgs[0].Name)
	}
	return nil
}

// --- Scenario: Around key wraps in registration order ---

func (w *contextValuesWorld) twoAroundListeners(reqA, reqB, resB, resA string) error {
	w.newRoot()
	w.guard = cordis.NewAround[string, cvVerdict]("guard")
	wrap := func(in, out string) error {
		_, err := w.guard.On(w.root, func(_ *cordis.Context, r string, next func(string) (cvVerdict, error)) (cvVerdict, error) {
			v, err := next(r + in)
			v.Text += out
			return v, err
		})
		return err
	}
	if err := wrap(reqA, resA); err != nil {
		return err
	}
	return wrap(reqB, resB)
}

func (w *contextValuesWorld) keyCalledWithInnerHandler() error {
	return cvWithin("Around", func() {
		w.result, w.aroundErr = w.guard.Around(w.root, "", func(r string) (cvVerdict, error) {
			return cvVerdict{Text: "ok:", Seen: r}, nil
		})
	})
}

func (w *contextValuesWorld) handlerSees(want string) error {
	if w.aroundErr != nil {
		return fmt.Errorf("Around: %w", w.aroundErr)
	}
	if w.result.Seen != want {
		return fmt.Errorf("handler saw %q, want %q", w.result.Seen, want)
	}
	return nil
}

func (w *contextValuesWorld) callerReceivesSuffixed(suffix string) error {
	if w.aroundErr != nil {
		return fmt.Errorf("Around: %w", w.aroundErr)
	}
	if w.result.Text != "ok:"+suffix {
		return fmt.Errorf("caller received %q, want %q", w.result.Text, "ok:"+suffix)
	}
	return nil
}

func InitializeScenario_cordis_port_guarantees_context_values_and_scoped_dispatch(ctx *godog.ScenarioContext) {
	var w *contextValuesWorld
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		w = &contextValuesWorld{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if w != nil && w.root != nil {
			_ = cvWithin("dispose root", w.root.Dispose)
		}
		return c, nil
	})

	ctx.Step(`^two agent subtrees "([^"]+)" and "([^"]+)" each listening on the same event key$`, func(a, b string) error { return w.twoAgentSubtrees(a, b) })
	ctx.Step(`^an event is emitted scoped to subtree "([^"]+)"$`, func(name string) error { return w.emitScopedTo(name) })
	ctx.Step(`^the listener of subtree "([^"]+)" ran (\d+) times?$`, func(name string, n int) error { return w.listenerRan(name, n) })

	ctx.Step(`^three Notify listeners where the second panics$`, func() error { return w.threeNotifyListeners() })
	ctx.Step(`^the fact is notified$`, func() error { return w.factNotified() })
	ctx.Step(`^Notify returns normally$`, func() error { return w.notifyReturnsNormally() })
	ctx.Step(`^listeners one and three ran$`, func() error { return w.listenersOneAndThreeRan() })
	ctx.Step(`^the panic is recorded once$`, func() error { return w.panicRecordedOnce() })

	ctx.Step(`^two around listeners that append "([^"]*)" and "([^"]*)" to the request and "([^"]*)" and "([^"]*)" to the result$`, func(a, b, rb, ra string) error { return w.twoAroundListeners(a, b, rb, ra) })
	ctx.Step(`^the key is called with an inner handler$`, func() error { return w.keyCalledWithInnerHandler() })
	ctx.Step(`^the handler sees the request "([^"]*)"$`, func(s string) error { return w.handlerSees(s) })
	ctx.Step(`^the caller receives the result suffixed "([^"]*)"$`, func(s string) error { return w.callerReceivesSuffixed(s) })
}

func TestE2E_cordis_port_guarantees_context_values_and_scoped_dispatch(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "cordis_port_guarantees_context_values_and_scoped_dispatch",
		ScenarioInitializer: InitializeScenario_cordis_port_guarantees_context_values_and_scoped_dispatch,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"cordis_port_guarantees_context_values_and_scoped_dispatch.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
