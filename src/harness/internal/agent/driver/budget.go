package driver

import (
	"fmt"
	"time"
)

// The budget defaults (tech-spec §5.5, DH-ADR-AGENT-7). They are not yet
// confirmed by their owner (impl.md §9.1); they live here only.
const (
	DefaultMaxSteps           = 500
	DefaultMaxAttemptsPerStep = 8
)

// The budget limits a turn/end budget{limit} names.
const (
	LimitSteps    = "steps"
	LimitDuration = "duration"
)

// Budget bounds one turn. The step limit is checked before each proposed step
// (point 3) and ends the turn as budget{steps, MaxSteps} after the step before it
// has closed, leaving parked input parked. The time limit cancels the turn with
// cause budget; its turn/end is budget{duration, milliseconds}.
type Budget struct {
	// MaxSteps is the most steps one turn runs; 0 is unlimited. A config must
	// write the 0: an absent budget is DefaultBudget.
	MaxSteps int `json:"maxSteps"`
	// MaxAttemptsPerStep is the most model attempts one step makes, retries
	// included; 0 is DefaultMaxAttemptsPerStep.
	MaxAttemptsPerStep int `json:"maxAttemptsPerStep,omitempty"`
	// MaxDuration is the most wall time one turn runs; 0 is none.
	MaxDuration time.Duration `json:"maxDuration,omitempty"`
}

// DefaultBudget is the budget of an agent whose config names none.
func DefaultBudget() Budget {
	return Budget{MaxSteps: DefaultMaxSteps, MaxAttemptsPerStep: DefaultMaxAttemptsPerStep}
}

// Validate refuses negative limits.
func (b Budget) Validate() error {
	if b.MaxSteps < 0 || b.MaxAttemptsPerStep < 0 || b.MaxDuration < 0 {
		return fmt.Errorf("driver: budget limits are not negative: %+v", b)
	}
	return nil
}

func (b Budget) attempts() int {
	if b.MaxAttemptsPerStep <= 0 {
		return DefaultMaxAttemptsPerStep
	}
	return b.MaxAttemptsPerStep
}

// stepsLeft reports whether a turn that has used used steps may propose one more.
func (b Budget) stepsLeft(used int) bool { return b.MaxSteps == 0 || used < b.MaxSteps }
