# DH-S-8 evidence

Decisions that govern M1 records, this one included, are logged in [decisions.md](../decisions.md).

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The stage records below are history: under M1-D-CLOSE-1 every tick made while the gate
was red is provisional and is re-verified at that commit. Plan Phase 15 added the
"Stage 8.1 addendum (M1-G-STORE)" and wrote the Stage 8.2 closure (plan step 15.3.7). The
fields of impl.md sec. 7.3 and the per-step manual-check register are at the end of this
file, in "Evidence record (impl.md sec. 7.3)"; how to fill them is in
[the evidence runbook](README.md).

DH-S-8 is machine state, views and spill. This file holds the evidence of its
stages as they are built. Stage 8.1 (typed machine state and stored folds), its
Phase 15 addendum, Stage 8.2 (built-in views, projects and listing) and Stage 8.3 (spill
store and attachments) are recorded below. **Closing DH-S-8 was Stage 8.2's step**
(operator decision `dsh-s8-closure-waits-stage-8-2`): plan step 15.3.7 did it, at gate
commit `6e7514c`. It records the refold-equals-stored test for every built-in view,
ticks impl.md steps 8.3 to 8.5 and raises `built-through` in the acceptance index to
DH-S-8. All seven impl.md steps 8.1 to 8.7 are now ticked as built. Built is not done:
DH-E-STORAGE-1, -2 and -3 are authored and not run, and the held-open entries H-33, H-35
and H-36 stay open.

Machine for every run below: Windows 11 Enterprise, 13th Gen Intel Core i9-13900K,
Samsung SSD 990 PRO (NVMe), go1.27.4 windows/amd64; the module's floor is
`go 1.26.0`.

<!-- Immutable fixture of harness-gate entry (implementation-plan Stage 1.2): the head of the live record at c1457ed, status line set to "open". -->
