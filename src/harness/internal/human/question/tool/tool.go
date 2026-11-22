// Package tool is the ask_user_question tool (DH-D-HUMAN-5): one tools.Define
// declaration over the question service.
package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/question"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// Name is the model-facing name.
const Name = "ask_user_question"

// In is the tool's input.
type In struct {
	// Question is what to ask the user.
	Question string `json:"question"`
	// Choices are offered answers separated by "|"; "" for free text. (A
	// string, not an array: the model-facing schema admits one type name per
	// field, and a Go slice derives a nullable array.)
	Choices string `json:"choices,omitempty"`
	// Wait blocks until the answer or the deadline; false moves on at once and
	// the answer arrives later as a user message naming this call.
	Wait bool `json:"wait,omitempty"`
	// TimeoutSeconds is the deadline; 0 is the service's default.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// Out is the tool's result.
type Out struct {
	ID string `json:"id"`
	// Status is answered, pending, declined or no-answerer.
	Status string `json:"status"`
	Answer string `json:"answer,omitempty"`
	By     string `json:"by,omitempty"`
}

// Resolver finds the session and the inbox of the calling agent.
type Resolver func(ref agent.Ref) (question.Log, question.Inbox, error)

// Run is the tool's body.
func Run(ctx context.Context, svc *question.Service, resolve Resolver, in In, run tools.Run) (Out, error) {
	if svc == nil || resolve == nil {
		return Out{}, errors.New("ask_user_question: no question service is mounted")
	}
	if in.TimeoutSeconds < 0 {
		return Out{}, errors.New("ask_user_question: timeout_seconds is not negative")
	}
	log, inbox, err := resolve(run.Agent)
	if err != nil {
		return Out{}, fmt.Errorf("ask_user_question: %w", err)
	}
	r, err := svc.Ask(ctx, log, inbox, question.Question{Agent: run.Agent, Call: run.CallID, Text: in.Question, Choices: choices(in.Choices),
		Wait: in.Wait, Timeout: time.Duration(in.TimeoutSeconds) * time.Second})
	if err != nil {
		return Out{}, err
	}
	out := Out{ID: r.ID, Status: string(r.Outcome)}
	if r.Outcome == question.OutcomeAnswered {
		out.Answer, out.By = r.Text, r.By.Principal
	}
	return out, nil
}

func choices(s string) []string {
	var out []string
	for c := range strings.SplitSeq(s, "|") {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// Declare is the tool's declaration over svc.
func Declare(svc *question.Service, resolve Resolver) *tools.Declaration {
	return tools.Define(tools.Spec[In, Out]{
		Name: Name,
		Description: "Ask the user a question. With wait, block until the answer or the deadline; " +
			"otherwise continue at once: a late answer arrives as a user message naming this call.",
		Run: func(ctx context.Context, in In, run tools.Run) (Out, error) {
			return Run(ctx, svc, resolve, in, run)
		},
		Effects: []tools.Effect{tools.EffectSession},
	})
}
