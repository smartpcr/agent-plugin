// Package conform is the tool conformance helpers (DH-D-TOOLS-3, DH-T-TOOLS-9):
// checks a tool's author runs in its tests to prove a declaration behaves under
// the guarded pipeline. It drives the real pipeline, never the body alone.
package conform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

// DefaultWithin is the grace Cancellation allows when within is zero.
const DefaultWithin = time.Second

// Cancellation runs one call of d with args through the pipeline under a budget
// that ends at once, and fails when the body does not return within within of
// its context's cancellation: a body that ignores its context would be detached
// in production and leave the call's outcome unknown (DH-ADR-TOOLS-4). A body
// that finishes before its budget ends passes too. Empty args are "{}".
func Cancellation(d *tools.Declaration, args json.RawMessage, within time.Duration) error {
	if d == nil {
		return errors.New("conform: a declaration is required")
	}
	if err := d.Err(); err != nil {
		return err
	}
	if within <= 0 {
		within = DefaultWithin
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	root := cordis.New()
	root.Loggers().ClearExporters()
	defer root.Dispose()
	f, err := root.Use(scope.Component, cordis.None{})
	if err != nil {
		return err
	}
	if err := f.Wait(); err != nil {
		return err
	}
	var tab scope.Table
	g, err := root.Inject([]string{scope.TableKey.Name()}, func(c *cordis.Context) error {
		var err error
		tab, err = scope.TableKey.Get(c)
		return err
	})
	if err != nil {
		return err
	}
	if err := g.Wait(); err != nil {
		return err
	}
	svc := core.NewWith(root, tab, core.Config{Grace: within, Checkpoint: checkpoint.Default{},
		Timeouts: map[string]time.Duration{d.Name(): time.Nanosecond}})
	if _, err := svc.Register(root, d); err != nil {
		return err
	}
	ag := agent.Ref{Session: session.ID("conform"), Incarnation: 1}
	v, err := svc.Resolve(context.Background(), tools.Target{Agent: ag})
	if err != nil {
		return err
	}
	defer v.Release()
	res, err := svc.ExecuteBatch(context.Background(), tools.Batch{View: v, Log: &memLog{},
		Calls: []tools.CallRequest{{ID: "conform-1", Name: d.Name(), Arguments: string(args)}}})
	if err != nil {
		return err
	}
	r := res.Results[0]
	switch {
	case r.Code == tools.CodeOutcomeUnknown:
		return fmt.Errorf("conform: %s did not return within %s of its context's cancellation: %s", d.Name(), within, r.Text)
	case r.Stage != tools.StageShape && r.Stage != tools.StageBody:
		return fmt.Errorf("conform: the call of %s never reached its body: %s", d.Name(), r.Text)
	}
	return nil
}

// memLog is a Log that keeps nothing: the helper reads only the Result.
type memLog struct {
	mu  sync.Mutex
	seq event.Seq
}

func (l *memLog) Append(context.Context, event.Draft) (event.Receipt, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	return event.Receipt{Seq: l.seq}, nil
}

func (l *memLog) Flush(context.Context) (event.Offset, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.seq, nil
}
