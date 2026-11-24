# DH-S-6 evidence

Decisions governing this record ([decisions.md](../decisions.md)): `platforms-windows-wsl` (the macOS rows below are historical), M1-D-CLOSE-1 (ticks made while the gate was red are provisional until the closure commit), and M1-D-BOOT-1 (b), M1-D-BOOT-2 (b) and M1-D-BOOT-3 (A), in force since 2026-11-22: the cut-short parts of seven deliverables and `DH-T-HOST-17` are held open (H-24 to H-32, ["Held open"](#held-open)), and the usage-error status is 64. Logged by gap-closure-plan Stage 12.1; merge commit: recorded when the Stage 12.1 change merges.

Closure status: closed at 1a2b3c4

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The "Closure status: closed" paragraph below is the Stage 6.7 closure, kept as history:
under M1-D-CLOSE-1 every tick made while the gate was red is provisional and is
re-verified at that commit, and plan Stage 14.6 re-records the manual checks on the two
M1 lanes. The fields of impl.md sec. 7.3 and the per-step manual-check register are at
the end of this file, in "Evidence record (impl.md sec. 7.3)"; how to fill them is in
[the evidence runbook](README.md).

Stages 6.1 to 6.6 (impl.md steps 6.1 to 6.16) were built by the earlier workstreams
of this phase, merged to `main` up to `f394c24`. Stage 6.7 (step 6.17 and the circle
closure) carries the closure record for DH-S-6. Every result below was re-run on one
tree: `f394c24` plus this stage's change, the commit that carries this record. All
seventeen deliverables are built at that commit. _Corrected (gap-closure 14.6.11,
M1-D-BOOT-1 (b)): seven of them, steps 6.2, 6.3, 6.6, 6.7, 6.15, 6.16 and 6.17, were
cut short when the story plan was written. They stay ticked as built, and each cut
part is held open with its closing step under ["Held open"](#held-open)._

**Closure status: closed.** Every closure item is recorded below. The manual checks
of impl.md DH-S-6, including "a child that ignores termination ... on each operating
system", are recorded on Windows, Linux and macOS (macOS: historical, `platforms-windows-wsl`): Windows 11 on this runner, and all
three systems in `harness-manual-checks` run
[44860891149](https://github.com/smartpcr/agent-plugin/actions/runs/44860891149)
(success; jobs 136713767008 Linux, 136713766919 macOS (historical), 136713767118 Windows) at
commit `42d0d7dbd163a46e3e395619d3c9f53c5aafff1a`, go1.26.0. The commit that carries
this record differs from `42d0d7d` only in this file, the plan's tick and the
workflow's push paths (which now include this file, so the closing commit's push runs
the same checks on all three systems again). No code or test changed, so the results
hold at the closing commit.

> **Historical (decision `platforms-windows-wsl`, 2026-11-22).** Every macOS result in
> this record (the macOS jobs of runs 44860891149 and 44861208866, the macOS column of
> the manual-check table and the macOS owner line) was recorded before macOS left M1
> scope. It is kept as history and counts as neither a pass nor a skip for M1; the M1
> lanes are Windows (native) and Linux (WSL).

<!-- Immutable fixture of harness-gate entry (implementation-plan Stage 1.2): the head of the live record at c1457ed, status line set to "closed at 1a2b3c4". -->
