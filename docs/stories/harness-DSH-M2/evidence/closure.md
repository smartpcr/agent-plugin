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
finishes), Phase 8 (deliverables S18 finishes) and the final M2 closure step (Stage 9.2), which
declares M2 reached only when DH-S-17 and DH-S-18 are closed and the step 16.8 guarded scenarios
have passed (`harness-gate milestone`).

M2 reached: no.

## Deliverables

The finishing stage of a done-candidate is the stage whose scenarios (or, where the deliverable's
"Done when" names only local tests, whose local tests) prove it. "Proving" is the deliverable's
"Done when" line in its area document.

| Deliverable | Priority | Built in | State | Finishing stage or held-open item | Proving scenarios |
| --- | --- | --- | --- | --- | --- |
| DH-D-VIEW-10 | P0 | DH-S-12 | built | DH-S-18 (step 18.6) | DH-E-VIEW-5 |
| DH-D-FS-4 | P0 | DH-S-12 | built | DH-S-28 | DH-E-FS-1, DH-E-FS-3, DH-E-FS-5 |
| DH-D-SANDBOX-1 | P0 | DH-S-12 | built | DH-S-17 (step 17.9) | DH-E-SANDBOX-7 |
| DH-D-HUMAN-4 | P1 | DH-S-12 | built | DH-S-12 (local tests) | DH-T-HUMAN-7 (no DH-E scenario) |
| DH-D-FS-6 | P0 | DH-S-12 | built | DH-S-18 (step 18.5) | DH-E-FS-2 |
| DH-D-SANDBOX-2 | P0 | DH-S-13 | not built | DH-S-39 | DH-E-SANDBOX-1, DH-E-SANDBOX-2 |
| DH-D-SANDBOX-3 | P0 | DH-S-13 | not built | DH-S-39 | DH-E-SANDBOX-2 |
| DH-D-SANDBOX-4 | P0 | DH-S-13 | not built | DH-S-18 (step 18.4) | DH-E-SANDBOX-1 |
| DH-D-EXEC-3 | P0 | DH-S-13 | not built | DH-S-13 (local tests) | DH-T-EXEC-7, DH-T-NODEPROC-9 (no DH-E scenario) |
| DH-D-EXEC-4 | P0 | DH-S-13 | not built | DH-S-39 | DH-E-EXEC-8 |
| DH-D-EXEC-8 | P0 | DH-S-13 | not built | DH-S-39 | DH-E-EXEC-7 |
| DH-D-EXEC-9 | P0 | DH-S-13 | not built | DH-S-39 | DH-E-EXEC-8 |
| DH-D-CONTEXT-2 | P0 | DH-S-13 | not built | DH-S-32 | DH-E-CONTEXT-1, DH-E-CONTEXT-2 |
| DH-D-BUNDLE-1 | P0 | DH-S-14 | built | DH-S-14 (local tests) | DH-T-BUNDLE-2, DH-T-BUNDLE-5 (no DH-E scenario) |
| DH-D-BUNDLE-2 | P0 | DH-S-14 | built | DH-S-22 | DH-E-BUNDLE-3 |
| DH-D-BUNDLE-4 | P0 | DH-S-14 | built | DH-S-48 | DH-E-BUNDLE-2 |
| DH-D-BUNDLE-5 | P0 | DH-S-14 | built | DH-S-48 | DH-E-BUNDLE-2 |
| DH-D-BUNDLE-6 | P0 | DH-S-14 | built | DH-S-22 | DH-E-BUNDLE-4 |
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

## Phase 8 (DH-S-18)

implementation-plan Stage 8.10, taken up on `1eb8f67`: DH-S-18 is not closed (its record reads
`Closure status: open`; `harness-gate ready --stage DH-S-18` exits 1 with 46 blockers, every in-scope
scenario row `not run` because harness:DSH-M1 is not closed, M2-DEC-1; see
[DH-S-18.md](DH-S-18.md#stage-810-closure-attempt)), so S18 finishes no deliverable and no row above
changes. When the S18 rows pass, these move:

- M2 done-candidates S18 finishes: DH-D-SANDBOX-4 (step 18.4, DH-E-SANDBOX-1 on every `linux-e2:*`
  machine of its set), DH-D-FS-6 (step 18.5, DH-E-FS-2 on `linux-wsl`, `windows-host` and
  `windows-host:smb`) and DH-D-VIEW-10 (step 18.6, DH-E-VIEW-5 on `linux-wsl`) read `done` only once
  they are `built` and those rows in [DH-S-18.md](DH-S-18.md) read `passed`.
- M2 deliverables S18 proves in part and does not finish: DH-D-FS-4 (DH-E-FS-1; stays to DH-S-28),
  DH-D-SANDBOX-2 and DH-D-SANDBOX-3 (DH-E-SANDBOX-1; stay to DH-S-39) and DH-D-CONTEXT-2
  (DH-E-CONTEXT-2; stays to DH-S-32). Their rows keep their finishing stage whatever S18 shows.
- Deliverables of other stories S18 proves (`docs/harness/trace/ledger.jsonl`): DH-D-HOST-7
  (DH-E-HOST-4), DH-D-EXEC-2 (DH-E-EXEC-1, DH-E-EXEC-10) and DH-D-STORAGE-2 (DH-E-STORAGE-6), and the
  library requirements and decisions of TOOLCHAIN-2, NATIVESYSTEM-1 to -4, RIPGREP-1 and -2,
  CHOKIDAR-1 and -2 and NODEFS-1. They are recorded in [DH-S-18.md](DH-S-18.md) and marked in
  harness:DSH-M1's closure record when it is written (it does not exist yet).

## Phase 9 (Stage 9.1)

implementation-plan Stage 9.1, taken up on `4053b1b`. `harness-gate records` now checks every state
above against the stage records (`closure/proof.go`, M2-C-3): a `built` or `done` row needs its
Built-in stage to pass `harness-gate ready --stage <id> --built`; a `done` row also needs every
proving DH-E scenario passed with its evidence in a stage record, every stage its finishing column
names closed, and every H-item it names closed. A row may not read further than that evidence.

| Built in | `ready --built` on `4053b1b` | Rows | State |
| --- | --- | --- | --- |
| DH-S-12 | exit 0 | VIEW-10, FS-4, SANDBOX-1, HUMAN-4, FS-6 | built |
| DH-S-13 | exit 1, 1 blocker: owner default I2 Q4 not confirmed | SANDBOX-2, -3, -4, EXEC-3, -4, -8, -9, CONTEXT-2 | not built |
| DH-S-14 | exit 0 | BUNDLE-1, -2, -4, -5, -6 | built |
| DH-S-15 | exit 1, 10 blockers: steps 15.1 to 15.8 have no commit SHA; owner defaults I2 Q5 and U-K2-5 not confirmed | HOST-1, -2, -3, API-1, SDK-3 to SDK-6 | not built |
| DH-S-16 | exit 1, 13 blockers: DH-E-STORE-1, -2, -6 and DH-E-FS-3 guarded rows `not run` (M1 not closed, M2-DEC-1); manual checks of steps 16.3 and 16.4 not recorded | BUNDLE-3, CLI-2, -4, -5, -9, SDK-8, UTIL-7, UTIL-8, VIEW-4 | not built |

So 10 rows read `built`, 25 `not built` and none `done`. No done-candidate can be `done` at this
commit: DH-S-17 and DH-S-18 are open (every acceptance row `not run`, M2-DEC-1), and the local-test
candidates (HUMAN-4, EXEC-3, BUNDLE-1, CLI-9) are recorded only by their stage's closure, and DH-S-12,
-13, -14 and -16 read `Closure status: open`. Every finishing stage and held-open item above is the
one tech-spec sec. 3.2 names (24 stay built: S22, S27, S28, S32, S39, S48 and H-1 to H-4, H-10);
H-21 is closed. M2-DOD-6 (every row `built` or `done`) is not met until DH-S-13, -15 and -16 pass
`ready --built`.

**Owner defaults (M2-C-4, Q `m2-unconfirmed-defaults`).** Each stage record's "Owner defaults
(M2-C-4)" table, as committed:

| Stage | Default | Raised in | Recorded |
| --- | --- | --- | --- |
| DH-S-12 | Full-access label | I2 Q3 | confirmed as written (operator) |
| DH-S-12 | UTF-8-only text tools | core §6.3 | confirmed as written (operator) |
| DH-S-13 | `partial` when delegated execution cannot be stopped | I2 Q4 | not confirmed (applied in step 13.4) |
| DH-S-15 | No SDK session grant | I2 Q5 | not confirmed |
| DH-S-15 | Client time zone | U-K2-5 | not confirmed |
| DH-S-16 | Second `dsh web` reuses the host | I3 Q1 | confirmed as written (operator, `12c2be8`) |
| DH-S-16 | Windows double-click | I3 Q8 | confirmed as written (operator, `12c2be8`) |
| DH-S-16 | `dsh headless` progress | U-K2-4 | confirmed as written (operator, `12c2be8`) |

DH-S-14, DH-S-17 and DH-S-18 have none. The three unconfirmed defaults wait on the owner's answer;
they are recorded in the DH-S-13 and DH-S-15 records when it is given.

## Phase 9 (Stage 9.2)

implementation-plan Stage 9.2, taken up on `c3fdce0`. M2 is reached when two things hold
(impl.md sec. 3.3, M2 row):
- DH-S-17 and DH-S-18 are closed;
- the Stage 6.12 guarded scenarios have passed: the step 16.8 rows DH-E-STORE-1, -2, -6 and
  DH-E-FS-3 of the DH-S-16 record.

`harness-gate milestone` judges this predicate (`closure/milestone.go`). `harness-gate records`
refuses an `M2 reached: yes.` line unless the predicate passes and `built-through` has advanced to
DH-S-18.

| Check on `c3fdce0` | Result |
| --- | --- |
| `harness-gate milestone` | exit 1, 9 blockers. DH-S-17 is not closed (status `open`, 18 blockers). DH-S-18 is not closed (status `open`, 47 blockers). 7 DH-S-16 guarded rows read `not run` (M1 not closed, M2-DEC-1): DH-E-STORE-1 (Linux (WSL), Windows), DH-E-STORE-2 (Linux (WSL)), DH-E-STORE-6 (Linux (WSL), Windows) and DH-E-FS-3 (Linux (WSL), Windows). |
| `built-through` (`test/harness/acceptance-index.yaml`) | stays `DH-S-11`: no stage of the story has closed, and a stage raises it only when it closes |
| `harness-gate release` (M2-DOD-7, NG-7) | exit 0. `.github/workflows/release.yml` (blob `4ab2c44`) and `.goreleaser.yaml` (blob `e79e5cd`) equal their copies at the story's base commit `896c760` (`tools/harness-gate/testdata/release_base.golden`). Neither file names `dsh`, no other workflow runs on a tag push or uses goreleaser, and all 7 default profiles send nothing (`src/harness/internal/boot/profile/testdata/egress.golden`, kept honest by `TestDefaultProfilesSendNothing`). |
| Release tags (`git tag --contains 896c760`) | only `evidence/m1-phase-18`, `evidence/m1-phase-19` and `evidence/m2-gate-repair`. No `v*` or `src/go/v*` tag contains the story's base. `v0.1.2`, `v0.1.3` and `v0.1.4` predate it. |
| `release.yml` runs (`gh run list --workflow release.yml`) | the last is run 44078147586 (`v0.1.4`, 2026-11-17), before the story's base. No run since. |

So M2 is not reached, and this record reads `M2 reached: no.` Nothing is enabled for users and no
build is published. The line changes to `yes` only once three things hold: DH-S-17 and DH-S-18 are
closed, the step 16.8 guarded rows have passed, and `built-through` reads DH-S-18.

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
