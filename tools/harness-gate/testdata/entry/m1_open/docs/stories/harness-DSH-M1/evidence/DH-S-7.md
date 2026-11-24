# DH-S-7 evidence

Decisions that govern M1 records, this one included, are logged in [decisions.md](../decisions.md).

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The "Closure status: closed by Stage 7.3" statement below and the "met" rows of
"Closure" are the stage's own closure, kept as history: under M1-D-CLOSE-1 every tick
made while the gate was red is provisional and is re-verified at that commit. Plan
Stage 15.1 built the missing store tests; they are recorded in
["Gap closure (M1-G-STORE)"](#gap-closure-m1-g-store), with both gate runs at `6e7514c`. The fields of impl.md sec. 7.3 and the
per-step manual-check register are at the end of this file, in "Evidence record
(impl.md sec. 7.3)"; how to fill them is in [the evidence runbook](README.md).

DH-S-7 is the SQLite store, leases and recovery. This file holds the evidence of its
stages as they are built. **Closure status: closed by Stage 7.3** (see "Closure"
at the end). Stage 7.1 (the database), Stage 7.2 (leases and crash recovery) and
Stage 7.3 (export and import, follow and duties, deletion and repair) are recorded
below. impl.md steps 7.1 to 7.6 are ticked as built. **Until step 16.8 passes, the
SQLite store is used on scratch homes only.**

<!-- Immutable fixture of harness-gate entry (implementation-plan Stage 1.2): the head of the live record at c1457ed, status line set to "open". -->
