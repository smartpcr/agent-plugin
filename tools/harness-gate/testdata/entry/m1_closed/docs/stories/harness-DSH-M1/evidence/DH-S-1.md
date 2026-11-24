# DH-S-1 evidence

Decisions governing this record: [decisions.md](../decisions.md), chiefly `platforms-windows-wsl` (the gate lanes are Windows native and Linux under WSL; the macOS rows below are historical) and `models-signed-in-claude-copilot` (the preflight no longer asks for a model key).

Closure status: closed at 1a2b3c4

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The stage records below are history: under M1-D-CLOSE-1 every tick made while the gate
was red is provisional and is re-verified at that commit. The fields of impl.md sec. 7.3
and the per-step manual-check register are at the end of this file, in "Evidence record
(impl.md sec. 7.3)"; how to fill them is in [the evidence runbook](README.md).

**Scope change (2026-11-22, decision `platforms-windows-wsl`).** impl.md DH-S-1 asks for
the gate to pass "on Linux, macOS and Windows". For M1 the gate of record runs on two
systems: Windows (native) and Linux under WSL on the development host. macOS has left M1
scope: a macOS row reads "out of scope (`platforms-windows-wsl`)" and is never a pass or a
skip. No stage brings macOS back yet (`macos-return-stage`, open). Cross-compiling
`cmd/dsh` for macOS stays a gate (QG-8). The upstream rules this overrides carry DC-3
notes in [toolchain.md](../../../harness/upstream/toolchain.md#dh-d-toolchain-4).

<!-- Immutable fixture of harness-gate entry (implementation-plan Stage 1.2): the head of the live record at c1457ed, status line set to "closed at 1a2b3c4". -->
