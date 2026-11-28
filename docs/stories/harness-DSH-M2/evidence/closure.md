# harness:DSH-M2 closure record

Closure record of story `harness:DSH-M2` (M2-DOD-6; tech-spec sec. 3.2; inconsistency check
M2-I-5): one row per deliverable of the story with its state, the stage that finishes it or the
held-open item that keeps it open, and the scenarios or local tests that prove it; then one row per
held-open item. `harness-gate records` checks this file's shape (35 deliverables, 30 P0 and 5 P1;
only the 11 done-candidates may read `done`; every row not done names a finishing stage or held-open
item; a row for each of H-1, H-2, H-3, H-4, H-10 and H-21).

**States.** `not built` until the stage that builds the deliverable has merged its step; `built` once
the stage is built (its evidence record passes `harness-gate ready --built`); `done` only when the
proving scenarios have passed and are recorded in an evidence record (M2-DEC-5). *Built* and *done*
are never merged (M2-C-3). At most the 11 done-candidates of tech-spec sec. 3.2 (VIEW-10,
SANDBOX-1, HUMAN-4, FS-6, SANDBOX-4, EXEC-3, BUNDLE-1, SDK-6, CLI-9, SDK-8, VIEW-4) can be `done`
at M2; the other 24 stay `built` with the finishing stage or held-open item named here.

**Updated by.** Implementation-plan step 6.12 (S12..S16 states), Phase 7 (deliverables S17
finishes), Phase 8 (deliverables S18 finishes) and the final M2 closure step, which declares M2
reached only when DH-S-17 and DH-S-18 are closed.

M2 reached: no.

## Deliverables

The finishing stage of a done-candidate is the stage whose scenarios (or, where the deliverable's
"Done when" names only local tests, whose local tests) prove it. "Proving" is the deliverable's
"Done when" line in its area document.

| Deliverable | Priority | Built in | State | Finishing stage or held-open item | Proving scenarios |
| --- | --- | --- | --- | --- | --- |
| DH-D-VIEW-10 | P0 | DH-S-12 | not built | DH-S-18 (step 18.6) | DH-E-VIEW-5 |
| DH-D-FS-4 | P0 | DH-S-12 | not built | DH-S-28 | DH-E-FS-1, DH-E-FS-3, DH-E-FS-5 |
| DH-D-SANDBOX-1 | P0 | DH-S-12 | not built | DH-S-17 (step 17.9) | DH-E-SANDBOX-7 |
| DH-D-HUMAN-4 | P1 | DH-S-12 | not built | DH-S-12 (local tests) | DH-T-HUMAN-7 (no DH-E scenario) |
| DH-D-FS-6 | P0 | DH-S-12 | not built | DH-S-18 (step 18.5) | DH-E-FS-2 |
| DH-D-SANDBOX-2 | P0 | DH-S-13 | not built | DH-S-39 | DH-E-SANDBOX-1, DH-E-SANDBOX-2 |
| DH-D-SANDBOX-3 | P0 | DH-S-13 | not built | DH-S-39 | DH-E-SANDBOX-2 |
| DH-D-SANDBOX-4 | P0 | DH-S-13 | not built | DH-S-18 (step 18.4) | DH-E-SANDBOX-1 |
| DH-D-EXEC-3 | P0 | DH-S-13 | not built | DH-S-13 (local tests) | DH-T-EXEC-7, DH-T-NODEPROC-9 (no DH-E scenario) |
| DH-D-EXEC-4 | P0 | DH-S-13 | not built | DH-S-39 | DH-E-EXEC-8 |
| DH-D-EXEC-8 | P0 | DH-S-13 | not built | DH-S-39 | DH-E-EXEC-7 |
| DH-D-EXEC-9 | P0 | DH-S-13 | not built | DH-S-39 | DH-E-EXEC-8 |
| DH-D-CONTEXT-2 | P0 | DH-S-13 | not built | DH-S-32 | DH-E-CONTEXT-1, DH-E-CONTEXT-2 |
| DH-D-BUNDLE-1 | P0 | DH-S-14 | not built | DH-S-14 (local tests) | DH-T-BUNDLE-2, DH-T-BUNDLE-5 (no DH-E scenario) |
| DH-D-BUNDLE-2 | P0 | DH-S-14 | not built | DH-S-22 | DH-E-BUNDLE-3 |
| DH-D-BUNDLE-4 | P0 | DH-S-14 | not built | DH-S-48 | DH-E-BUNDLE-2 |
| DH-D-BUNDLE-5 | P0 | DH-S-14 | not built | DH-S-48 | DH-E-BUNDLE-2 |
| DH-D-BUNDLE-6 | P0 | DH-S-14 | not built | DH-S-22 | DH-E-BUNDLE-4 |
| DH-D-HOST-1 | P0 | DH-S-15 | not built | DH-S-22 | DH-E-HOST-2 |
| DH-D-HOST-2 | P0 | DH-S-15 | not built | DH-S-22 | DH-E-HOST-1, DH-E-HOST-2, DH-E-HOST-7, DH-E-HOST-8, DH-E-HOST-9 |
| DH-D-HOST-3 | P0 | DH-S-15 | not built | DH-S-39 | DH-E-HOST-1 |
| DH-D-API-1 | P0 | DH-S-15 | not built | DH-S-48 | DH-E-API-5 |
| DH-D-SDK-3 | P0 | DH-S-15 | not built | DH-S-39 | DH-E-SDK-2 |
| DH-D-SDK-4 | P0 | DH-S-15 | not built | held open: H-1, H-2, H-3 | DH-E-SDK-1, DH-E-SDK-2 |
| DH-D-SDK-5 | P0 | DH-S-15 | not built | held open: H-4 | DH-E-SDK-1 |
| DH-D-SDK-6 | P0 | DH-S-15 | not built | DH-S-17 (step 17.8) | DH-E-SDK-1 |
| DH-D-BUNDLE-3 | P0 | DH-S-16 | not built | DH-S-22 | DH-E-BUNDLE-1, DH-E-BUNDLE-3, DH-E-BUNDLE-4 |
| DH-D-CLI-2 | P0 | DH-S-16 | not built | DH-S-27 (step 27.8) | DH-E-CLI-1, DH-E-CLI-2 |
| DH-D-CLI-4 | P0 | DH-S-16 | not built | DH-S-27 (step 27.8) | DH-E-CLI-1, DH-E-CLI-2 |
| DH-D-CLI-5 | P0 | DH-S-16 | not built | DH-S-27 (step 27.8) | DH-E-CLI-1 |
| DH-D-CLI-9 | P0 | DH-S-16 | not built | DH-S-16 (local tests) | DH-T-CLI-4, DH-T-CLI-16 (no DH-E scenario) |
| DH-D-SDK-8 | P1 | DH-S-16 | not built | DH-S-17 (step 17.8) | DH-E-SDK-1 |
| DH-D-UTIL-7 | P1 | DH-S-16 | not built | held open: H-10 | DH-E-TOOLCHAIN-1 |
| DH-D-UTIL-8 | P1 | DH-S-16 | not built | DH-S-39 | DH-E-UTIL-2 |
| DH-D-VIEW-4 | P1 | DH-S-16 | not built | DH-S-17 (step 17.6) | DH-E-VIEW-1 |

**Noted while prefilling.** DH-D-BUNDLE-6's "Done when" names DH-E-BUNDLE-4, which impl.md DH-S-14
runs in DH-S-48, while tech-spec sec. 3.2 keeps BUNDLE-6 built until DH-S-22. This record follows the
tech-spec (the plan's bound, M2-I-5); the S22 close must either run BUNDLE-4 or move the row to
DH-S-48.

## Phase 7 (DH-S-17)

implementation-plan Stage 7.7, taken up on `ac85a1f`: DH-S-17 is not closed (its record reads
`Closure status: open`; `harness-gate ready --stage DH-S-17` exits 1, every scenario row `not run`
because harness:DSH-M1 is not closed, M2-DEC-1), so S17 finishes no deliverable and no row above
changes. When the S17 rows pass, these move:

- M2 done-candidates S17 finishes: DH-D-SANDBOX-1 (step 17.9, DH-E-SANDBOX-7), DH-D-SDK-6 and
  DH-D-SDK-8 (step 17.8, DH-E-SDK-1) and DH-D-VIEW-4 (step 17.6, DH-E-VIEW-1) read `done` only once
  they are `built` and their rows in [DH-S-17.md](DH-S-17.md) read `passed`; DH-D-SDK-4 and
  DH-D-SDK-5 keep H-1 to H-4 and DH-D-UTIL-7 keeps H-10 whatever DH-E-SDK-1 and DH-E-TOOLCHAIN-1 show.
- M1 deliverables S17 finishes: the M1 scenarios it runs (DH-E-TOOLCHAIN-1, DH-E-SESSION-1, -2,
  DH-E-AGENT-1 to -4, DH-E-LLM-1, -3, DH-E-TOOLS-1, DH-E-VIEW-1, DH-E-LOADER-1) are recorded in
  [DH-S-17.md](DH-S-17.md); harness:DSH-M1 has no closure record yet
  (`docs/stories/harness-DSH-M1/evidence/closure.md` does not exist), so they are marked there when
  that record is written.

## Held-open items

Items impl.md holds open across M2 (M2-I-5). H-21 is the M1 item DH-S-16 step 16.10 closes: `dsh config show` and `dsh config validate` run in the admin tree, construct no component, and DH-T-BOOT-14's command tests (`cli`: `TestConfigShowConstructsNothing`, `TestConfigShowKeepsEveryValue`, `TestConfigValidateRejectsBadConfig`, `TestConfigValidateRejectsLayerFaults`, `TestConfigCommandsProcess`; `boot/config`: `TestDumpYAMLRoundTrips`, the loadable `--format yaml` dump against its golden and composed back to the same tree) pass.

| Item | Deliverable | What waits | State | Closes at |
| --- | --- | --- | --- | --- |
| H-1 | DH-D-SDK-4 | the schedule activation hook | open | the stage that builds schedules (after M2) |
| H-2 | DH-D-SDK-4 | validation against more than one provider route | open | after M2 |
| H-3 | DH-D-SDK-4 | inline image admission | open | after M2 |
| H-4 | DH-D-SDK-5 | a goal's stop signal | open | after M2 |
| H-10 | DH-D-UTIL-7 | replay of sessions with children, until the lineage records exist | open | after M2 |
| H-21 | DH-D-BOOT-6 | the boot preview through `dsh config show` and `dsh config validate` in the admin tree | closed | DH-S-16 (step 16.10) |
