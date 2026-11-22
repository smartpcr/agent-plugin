// Package driver is the agent loop (DH-D-AGENT-1). This file is its step tool
// set (DH-D-AGENT-10): the tools of one step, resolved once at point 4 and held
// until point 13.
package driver

import (
	"context"
	"fmt"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// StepTools is one step's tool set. It is resolved once when the step is
// proposed, for the agent and the step's model selection, and is fixed for the
// step and its retries: the schemas the model is shown and the tools its calls
// may run are the same set, whatever registers or unregisters meanwhile. A
// call to a tool outside it ends tools/not-started; the next step resolves
// afresh.
type StepTools struct {
	svc  tools.Service
	view tools.StepView
}

// ResolveStep resolves the step tool set for a, at its scope, under sel.
func ResolveStep(ctx context.Context, svc tools.Service, a agent.Agent, sel agent.ModelSelection) (*StepTools, error) {
	if svc == nil {
		return nil, fmt.Errorf("driver: the step tool set needs the tool service")
	}
	v, err := svc.Resolve(ctx, tools.Target{Agent: a.Ref(), Scope: a.ScopeKey(), Model: sel})
	if err != nil {
		return nil, fmt.Errorf("driver: resolve the step tool set: %w", err)
	}
	return &StepTools{svc: svc, view: v}, nil
}

// View is the step's view, for prompt assembly and nested calls.
func (s *StepTools) View() tools.StepView { return s.view }

// Schemas is the function schemas the model is shown this step, sorted by name.
func (s *StepTools) Schemas() []vocab.ToolSchema {
	ds := s.view.Tools()
	out := make([]vocab.ToolSchema, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Schema())
	}
	return out
}

// Execute runs the model's calls of this step as one bounded batch (point 12):
// the scheduler overlaps calls only when their resource keys do not conflict,
// at most the registry's maxParallel at once, and commits the results in the
// model's order.
func (s *StepTools) Execute(ctx context.Context, log tools.Log, calls []tools.CallRequest) (tools.BatchResult, error) {
	return s.svc.ExecuteBatch(ctx, tools.Batch{View: s.view, Log: log, Origin: tools.OriginModel, Calls: calls})
}

// Release ends the step's hold (point 13); it is idempotent.
func (s *StepTools) Release() { s.view.Release() }
