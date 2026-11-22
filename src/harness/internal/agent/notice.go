package agent

import (
	"fmt"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// ModelSwitchNotice names the optional agent.model-switch-notice component.
const ModelSwitchNotice = "agent.model-switch-notice"

// SwitchNoticeText is the user message the notice adds when a step's route
// differs from the previous step's.
func SwitchNoticeText(from, to ModelSelection) string {
	return fmt.Sprintf("[model switch] This step runs on %s; the previous step ran on %s.", describe(to), describe(from))
}

func describe(s ModelSelection) string {
	if s.Model == "" {
		return string(s.Route)
	}
	return string(s.Route) + " (" + s.Model + ")"
}

// RegisterModelSwitchNotice registers the notice on ctx as a PreStep listener:
// when the proposed step's route differs from the previous step's selection it
// appends one short user message to the batch, then lets the rest decide. The
// first step, and a step on the same route, are left alone.
func RegisterModelSwitchNotice(ctx *cordis.Context) (*cordis.EffectHandle, error) {
	return PreStep.On(ctx, func(_ *cordis.Context, req PreStepRequest, next func(PreStepRequest) (PreStepDecision, error)) (PreStepDecision, error) {
		if req.Previous != nil && req.Previous.Route != req.Selection.Route {
			msg, err := vocab.NewMessage(vocab.RoleUser, vocab.TextBlock(SwitchNoticeText(*req.Previous, req.Selection)))
			if err != nil {
				return nil, err
			}
			req.Messages = append(req.Messages, msg)
		}
		return next(req)
	})
}
