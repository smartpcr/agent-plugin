# DH-S-11 evidence

Decisions governing this record ([decisions.md](../decisions.md)): `m1-operator-run`, `context-package-name`, `agent-28-staged-acceptance`, `models-signed-in-claude-copilot` with M1-D-PIPE-3, M1-D-PIPE-4 and M1-D-CLOSE-3 (the M1 run moves to the signed-in route, so `dh-s-11-closure-path` is superseded), `platforms-windows-wsl`, M1-D-CLOSE-1, M1-D-CLOSE-4 and M1-D-LOOP-6, and the open `retro-entry-order-s8-s11`. The restated definition of done is decisions.md sec. 1.1. Logged by gap-closure-plan Stage 12.1; merge commit: recorded when the Stage 12.1 change merges. Added by plan Phase 17, all in force: the owner's answers M1-D-LOOP-1 to M1-D-LOOP-5 (2026-11-23) and, with M1-D-LOOP-5, `turn-budget-defaults-s11`.

Closure status: closed at 1a2b3c4

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The stage itself closes at plan step 17.4.9, which **has not closed it yet**: plan Phase 17
built the prompt assembly, the delegation seam proofs, the context pipeline and the M1
probe on the signed-in routes (section ["Gap closure (plan Phase 17)"](#gap-closure-plan-phase-17),
every test passing in both gate lanes at `fda4b98`), but the owner has not run the M1
manual check (plan 17.4.3) and has not reviewed the seven retargeted scenarios (see
[Closure](#closure)). The records before "Gap closure (plan Phase 17)" are history. The
fields of impl.md sec. 7.3 and the per-step manual-check register, with the M1 check
re-targeted to the signed-in Claude and Copilot routes, are at the end of this file, in
"Evidence record (impl.md sec. 7.3)"; how to fill them is in
[the evidence runbook](README.md).

DH-S-11 is the agent loop, milestone M1. Stage 11.1 built impl.md steps 11.1 and
11.2, Stage 11.2 built 11.3 and 11.4 with the no-unanswered-call property test, and
Stage 11.3 built 11.5 and 11.6. Stage 11.4 built step 11.7, added the flow F2 golden
and its SQLite twin (TC-11a, TC-11b), authored the remaining `DH-E-*` scenarios of
DH-S-1 to DH-S-11, and scripted the M1 manual check. Plan Phase 17 closed the gaps
M1-G-LOOP-1 to -16 that the review found in this work.

**DH-S-11 is not closed yet.** The ticks after plan step 17.4.6 (Phase 17), in impl.md and
the story plan alike: steps 11.1, 11.2, 11.5 and 11.6 are ticked as built (11.6 was
unticked as a known-false tick and re-ticked in the same change, because every test
M1-D-LOOP-3 B keeps in M1 now passes); steps 11.3, 11.4 and 11.7 stay unticked, because
the M1 manual check that covers them (rows 11.3, 11.4 and 11.7 of the register below) has
not run. Built is not done: no step's `DH-E` scenario has run. The plan's "Close DH-S-11
and the story" step stays unticked, and `built-through` stays at DH-S-8
(`CheckBuiltThrough` allows no more while DH-S-9 and DH-S-11 are open). See
[Closure](#closure).

Machine: Windows, go toolchain per `src/harness/go.mod` (floor `go 1.26.0`).

<!-- Immutable fixture of harness-gate entry (implementation-plan Stage 1.2): the head of the live record at c1457ed, status line set to "closed at 1a2b3c4". -->
