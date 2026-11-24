# DH-S-10 evidence

Decisions that govern M1 records, this one included, are logged in [decisions.md](../decisions.md).

Closure status: closed at 1a2b3c4

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The first closure of the stage (the "DH-S-10 was closed" paragraph below) is kept as
history. It was **reopened by M1-G-PIPE-1..8**: impl.md 10.3, 10.4 and 10.6 had been
ticked without the approval policy, the resume closer, the guarded-pipeline cases, grant
subjects, the spill reader and images in the budget. Plan Phase 16 built those (Stages
16.1 to 16.3) and **re-closed the stage at `f72f435`**, where both local lanes pass all
12 gates: see [Gap closure (M1-G-PIPE-1 to -8)](#gap-closure-m1-g-pipe-1-to-8) and
[Closure](#closure). Under M1-D-CLOSE-1 every tick is still re-verified at the M1
closure commit. The fields of impl.md sec. 7.3 and the per-step manual-check register
are at the end of this file, in "Evidence record (impl.md sec. 7.3)"; how to fill them
is in [the evidence runbook](README.md).

DH-S-10 is the tool pipeline, approvals and human commands. Stage 10.1 (#72/#73)
built impl.md steps 10.1 and 10.2, Stage 10.2 (#74/#75) built 10.3 and 10.4, and
Stage 10.3 (#76, `69c5f17`, merged into this branch) built 10.5 and 10.6 and
authored `DH-E-TOOLS-3`. Stage 10.4 built steps 10.7 to 10.10 and authored
`DH-E-HUMAN-6`.

**DH-S-10 was closed (history; reopened by M1-G-PIPE-1..8, re-closed at `f72f435`):**
impl.md steps 10.1 to 10.10 were ticked as built, and the combined branch passed the
tests below. The three manual checks below are scripted by tests here; their
interactive runs are for the operator. The tables in "What was built" and "Tests" are
that first closure's record: some outcome names in them (`no-answerer`) are the
pre-M1-D-PIPE-1 vocabulary, which version 1 records still decode to (see the outcome
table under Stage 16.1 below).

Machine: Windows, go toolchain per `src/harness/go.mod` (floor `go 1.26.0`).

<!-- Immutable fixture of harness-gate entry (implementation-plan Stage 1.2): the head of the live record at c1457ed, status line set to "closed at 1a2b3c4". -->
