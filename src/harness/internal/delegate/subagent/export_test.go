package subagent

// Refusing is closed once the manager behind rt has begun unloading, so every
// later Start is refused with ErrInactive: the signal a test waits on instead of
// polling Start (M1-G-GATE-13).
func Refusing(rt Runtime) <-chan struct{} { return rt.(*manager).refusing }
