# DH-S-9 evidence

Decisions governing this record ([decisions.md](../decisions.md)): `models-signed-in-claude-copilot` with M1-D-PIPE-3 and M1-D-PIPE-4 (the manual check below moves to the signed-in Claude and Copilot routes, so `dh-s-9-closure-path` is superseded), `dh-s-9-scenario-review`, `inherited-vet-gate-failure`, `archtest-llm-edges-exception`, `built-through-may-skip-s8` (superseded), M1-D-CLOSE-1 and M1-D-CLOSE-4, and the open `retro-entry-order-s8-s11`. Logged by gap-closure-plan Stage 12.1; merge commit: recorded when the Stage 12.1 change merges. Added by plan Phase 16: M1-D-PIPE-2 (the driver waits before a retry), M1-D-PIPE-5 (now in force: DeepSeek kept, keyless and fake-tested), M1-D-PIPE-6, -7 and -8 (the client routes), and the lead settlements `dh-d-llm-12-numbering`, `llm-client-contract-code`, `copilot-usage-not-reported` and `rule-6-additions-phase-16`.

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit;
the stage itself is re-closed by plan step 16.6.3, which **has not closed it yet**: plan
Phase 16 built the signed-in client routes and the retry wait (sections "Stage 16.4" and
"Stage 16.5" below, all tests passing in both gate lanes at `f72f435`), but the owner's
signed-in manual check (plan 16.5.9) has not run and the re-authored scenarios await the
owner's review (see [Closure](#closure)). The records before "Stage 16.4" are history. The
fields of impl.md sec. 7.3 and the per-step manual-check register, re-targeted to the
signed-in Claude and Copilot routes, are at the end of this file, in "Evidence record
(impl.md sec. 7.3)"; how to fill them is in [the evidence runbook](README.md).

DH-S-9 is the DeepSeek provider, retry and usage. Stage 9.1 (the Messages provider,
#68/#69) built impl.md step 9.1. Stage 9.2 built steps 9.2 and 9.3 and authored
DH-S-9's three scenarios. **DH-S-9 is not closed yet**: its manual check (below)
has not been run and its scenarios await review in the PR, so impl.md steps 9.1 to
9.3 stay unticked and `built-through` in the acceptance index stays at its baseline
(DH-S-7). See [Closure](#closure) for what remains.

Machine: Windows 11 Enterprise, 13th Gen Intel Core i9-13900K, go1.27.4
windows/amd64; the module's floor is `go 1.26.0`.

<!-- Immutable fixture of harness-gate entry (implementation-plan Stage 1.2): the head of the live record at c1457ed, status line set to "open". -->
