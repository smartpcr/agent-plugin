package subagent

import "github.com/smartpcr/agent-plugin/src/harness/internal/session"

// Refusing is closed once the manager behind rt has begun unloading, so every
// later Start is refused with ErrInactive: the signal a test waits on instead of
// polling Start (M1-G-GATE-13).
func Refusing(rt Runtime) <-chan struct{} { return rt.(*manager).refusing }

// SetTrace installs fn as the settle-point recorder of the manager behind rt:
// fn is called at S1 to S7 (S5 arrives with the ledger) with the child, the
// point and a detail. Call it before the first Start.
func SetTrace(rt Runtime, fn func(child session.ID, point, detail string)) {
	rt.(*manager).trace = fn
}
