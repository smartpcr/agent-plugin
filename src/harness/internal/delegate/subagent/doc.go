// Package subagent is the delegation seam (DH-D-SUBAGENT-1): any component
// delegates a task to a child agent through Runtime.Start, and every child has
// exactly one owner, the component that started it. The child's activation is
// a fiber under that owner holding the child's agent handle, so the owner's
// unload stops the child's turn, tears the agent down (agent-loop.md §6.3, T1
// to T8) and settles its result before the owner's Wait returns.
//
// This is the local, in-process manager of subagent-team.md §6.2 at its
// lifecycle points A1 admit, A2 reserve, A4 materialise, A5 accept, A6 publish,
// and S1 to S7 from close to deliver. The ledger (A3, S5), slots and start
// budgets, providers and the subagent tools arrive with DH-D-SUBAGENT-2 onward.
//
// Deferred tests (decision M1-D-LOOP-3 B; impl.md's DH-S-29 section names each
// one). The grandchild wait (a child that delegated is not released until its
// own child settles, then wakes with the notice) and the capability check at
// A1 with the output-schema check at S3 move to DH-S-29, steps 29.1 to 29.4,
// with the ledger, notices, providers and steering they need. So do the
// interrupt and time-limit legs of the settle-order golden, and its clause
// "Settled never precedes the record", which needs S5; the natural-end,
// dispose and owner-teardown legs are proven in this package
// (TestSettleOrderGolden).
//
//dsh:test DH-T-SUBAGENT-3 later DH-S-29
//dsh:test DH-T-SUBAGENT-4 later DH-S-29
//dsh:test DH-T-SUBAGENT-14 later DH-S-29
package subagent
