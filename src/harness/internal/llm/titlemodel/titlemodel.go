// Package titlemodel is the session titles' model (title.Model) over the llm
// runtime: one buffered auxiliary call per title request, its one attempt
// returned as the record title settles. It sits in llm because nothing under
// internal/session imports the runtime (DH-D-LLM-1).
package titlemodel

import (
	"context"
	"errors"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/title"
)

// Name is the component's catalogue name.
const Name = "llm.title-model"

// errOnce ends Complete after the first attempt that did not succeed: a title
// request has exactly one attempt, so the route's retry policy is not used.
var errOnce = errors.New("titlemodel: one attempt per title request")

type model struct{ rt *llm.Runtime }

// New is the title.Model over rt.
func New(rt *llm.Runtime) title.Model { return model{rt: rt} }

func (m model) Attempt(ctx context.Context, req session.Request) (title.Attempt, error) {
	var rec *llm.Attempt
	_, err := m.rt.Complete(ctx, req, llm.AttemptSinkFunc(func(a llm.Attempt) error {
		rec = &a
		if !a.Succeeded() {
			return errOnce
		}
		return nil
	}))
	if rec == nil {
		return title.Attempt{}, err
	}
	return title.Attempt{
		RequestAsOf: rec.RequestAsOf, RequestDigest: rec.RequestDigest,
		Message: rec.Message, Finish: rec.Finish, Usage: rec.Usage, ServedBy: rec.ServedBy,
		Duration: time.Duration(max(rec.Timing.End-rec.Timing.Start, 0)) * time.Millisecond,
	}, nil
}

// Component is llm.title-model: it provides New over the runtime under
// title.ModelKey.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	rt, err := llm.RuntimeKey.Get(ctx)
	if err != nil {
		return err
	}
	_, err = title.ModelKey.Provide(ctx, New(rt))
	return err
}, cordis.WithInject(llm.RuntimeKey.Name()))
