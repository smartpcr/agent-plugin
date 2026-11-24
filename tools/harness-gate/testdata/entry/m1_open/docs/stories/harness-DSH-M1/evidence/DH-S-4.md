# DH-S-4 evidence

Decisions that govern M1 records, this one included, are logged in [decisions.md](../decisions.md).

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The stage records below are history: under M1-D-CLOSE-1 every tick made while the gate
was red is provisional and is re-verified at that commit. The fields of impl.md sec. 7.3
and the per-step manual-check register are at the end of this file, in "Evidence record
(impl.md sec. 7.3)"; how to fill them is in [the evidence runbook](README.md).

Stages 4.1 to 4.3 (impl.md steps 4.1 to 4.9: the JSON-RPC codec, the shape schema,
the SDK protocol types, the util leaves, the model vocabulary, `fsx`, `textdiff`,
`search` and the watcher) were built by the earlier workstreams of this phase.
Stage 4.4 (steps 4.10 to 4.14) closes DH-S-4. Every result below was re-run with all
fourteen deliverables applied.

<!-- Immutable fixture of harness-gate entry (implementation-plan Stage 1.2): the head of the live record at c1457ed, status line set to "open". -->
