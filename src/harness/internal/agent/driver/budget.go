package driver

import (
	"fmt"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/schema"
)

// The budget defaults (tech-spec §5.5, DH-ADR-AGENT-7): 500 steps, 8 attempts
// per step, no time limit. The owner confirmed them in writing on 2026-11-23
// (M1 decision M1-D-LOOP-5; turn-budget-defaults-s11 is in force), so they stay
// constants (DC-4); impl.md §9.1 records the confirmation by a pointer line.
// They live here only.
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

// budgetJSON is Budget without its methods, so its encoding is the fields'.
type budgetJSON Budget

// MarshalJSON writes MaxDuration as its text, "30m" (schema.Marshal), so a
// config states a time limit with its unit and the bytes match the derived shape.
func (b Budget) MarshalJSON() ([]byte, error) { return schema.Marshal(budgetJSON(b)) }

// UnmarshalJSON reads MaxDuration from its text; a number there is refused.
func (b *Budget) UnmarshalJSON(raw []byte) error {
	return schema.Unmarshal(raw, (*budgetJSON)(b))
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
