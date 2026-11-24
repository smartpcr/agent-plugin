# DH-S-5 evidence

Decisions that govern M1 records, this one included, are logged in [decisions.md](../decisions.md).

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The stage records below are history: under M1-D-CLOSE-1 every tick made while the gate
was red is provisional and is re-verified at that commit. The fields of impl.md sec. 7.3
and the per-step manual-check register are at the end of this file, in "Evidence record
(impl.md sec. 7.3)"; how to fill them is in [the evidence runbook](README.md).

Stages 5.1 and 5.2 (impl.md steps 5.1 to 5.8: the typed event registry, the scope
layers, the store contract with memstore and its conformance suite, event
versioning, the live session, the core folds, the rebuilt request and the
brackets) were built by the earlier workstreams of this phase. Stage 5.3 (steps 5.9
to 5.13) closes DH-S-5. Every result below was re-run with all thirteen
deliverables applied.

<!-- Immutable fixture of harness-gate entry (implementation-plan Stage 1.2): the head of the live record at c1457ed, status line set to "open". -->
