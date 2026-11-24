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

## Stage 8.1 -- Typed Machine State and Stored Folds

Built and merged before this stage (#63). Its tests, rerun here with
`go -C src/harness test -count=1 ./internal/storage/... ./internal/session/views/`:

| Scenario / step | Test | Result |
| --- | --- | --- |
| 8.1 Machine state survives restart (DH-D-STORAGE-1) | `storage/workspace` `TestMachineStateSurvivesRestart`; `storage/kv` `TestUpdateCommitsAtomicallyAndSurvivesRestart` | pass |
| 8.2 Changed fold version rebuilds (DH-D-VIEW-2) | `session/views` `TestChangedFoldVersionRebuilds` | pass |

## Stage 8.1 addendum (M1-G-STORE)

**What this section records.** The audit found that step 8.2 (stored fold state is never
ahead of the log) was ticked while its tests were missing and its behaviour differed from
the spec: a failing view rolled back its events, a reader was never told it held stale
state, and an unheld session's rows were never refreshed (gaps M1-G-STORE-10 to -20 in
[gap-analysis.md](../gap-analysis.md)). Plan
[Stage 15.2](../gap-closure-plan.md#stage-152-fresh-behind-or-pending-stored-views-done-right)
built the missing behaviour and tests. This section is the evidence of "built" for that
stage; it is not "done" (no `DH-E` scenario of DH-D-VIEW-2 has run).

**Words used here.**
- A **view** (a fold) turns a session's log into a small state, for example the list
  summary. Its **stored row** (a derived row in `checkpoints`) holds that state with the
  `seq` it reflects and the view's version.
- A reader asks for one of two **freshness** modes. `Stored` returns the stored row as it
  is and never folds or blocks. `Exact` catches up from the row by folding at most
  `MaxCatchUpEvents` (2,000) events, and otherwise fails with `view/behind`.
- Every value says its **state**: `current` (the row reflects the head), `behind` (the row
  is older than the head; its `AsOfSeq` says how old), `pending` (no row of this version
  exists yet) or `failed` (this process failed to fold it).
- A **cadence** says when a row is written: `EveryCommit`, or `Periodic` (every
  `CheckpointEveryEvents`, 200, events and at each turn end).
- The **rebuild sweep** is the once-per-home `views-rebuild` duty that folds, read-only,
  the sessions lacking a row of the current version and writes them with
  `UpsertDerived`. Because each row carries its `seq`, the rows are the progress record: a
  killed sweep resumes where it stopped.
- **Prune** deletes rows of keys this binary does not register and rows of older
  versions, never `core/` rows, and only when every live host runs the same binary
  (`hosts.binary_version`, added by plan 15.1.2).

**Build identity.** Built in `8d8abfa` (15.2.1, 15.2.5, 15.2.7), `ec21abc` (15.2.2 to
15.2.4), `cb1c07a` (an append's checkpoint only moves forward), `fafe8ce` (15.2.9 to
15.2.11), `0baa7cf` (15.2.6, 15.2.8) and `3695cc2` (the prompt fix below). Decision
M1-D-STORE-E (a) and the lead settlements `pending-sessions-listed-when-blank-hidden` and
`fold-failed-in-process-only` ([decisions.md](../decisions.md)) apply. Gate evidence: both
lanes `PASSED all 12 gates` at `6e7514c`, run records `wsl-6e7514c-20261123T0602Z` and
`win-6e7514c-20261123T0602Z` (the full template is in
[DH-S-7.md, "Gate runs at 6e7514c"](DH-S-7.md#gate-runs-at-6e7514c)); `go test -race` per
package also passed.

### Test IDs and results

Every test below passed in both gate runs at `6e7514c`. Package `session/views` unless
named.

| Test ID | Plan step | Tests | What they show | Result |
| --- | --- | --- | --- | --- |
| `DH-T-VIEW-1` | 15.2.9 | `TestCheckpointPlusSuffixEqualsFullFold` (`property_test.go`) | 500 logs (seed `0x1529d5a`, logged): 13,521 events, 1,683 rows, 733 replaced entries. For every built-in view, a row taken at a random cut plus the rest of the log equals the full fold. One log in five comes from `corpus.Log`, the rest from a seeded generator (the test kit has no other scripted-log helper). | pass |
| `DH-T-VIEW-5` | 15.2.1 | `session/store/sqlite` `TestMultiEventAppendIsAtomic`, `TestAppendFaultAtEachStep` | A failing view costs no event: when the database refuses a derived row, the three events commit, the row keeps its older seq, `DerivedDropped` is 1 and one `Degraded` notice is sent. That a row older than the head then reads `behind` at `Stored` is shown by `TestStoredReportsPendingBehindAndCurrent` (`DH-T-VIEW-7`), not inside these two tests. | pass |
| `DH-T-VIEW-6` | 15.2.6, 15.2.11 | `TestInterruptedRebuildResumes` (`rebuild_test.go`), `TestChangedFoldVersionRebuilds` | 50 sessions with version-1 rows, the fold now at version 2: a sweep child is stopped by a write gate after 20 writes and killed. Readers meanwhile saw 20 `current` and 30 `pending`, never a version-1 value. The restarted sweep wrote the other 30, and all 50 rows equal a fresh fold. | pass |
| `DH-T-VIEW-7` | 15.2.2, 15.2.3 | `TestExactRefusesALongCatchUp`, `TestStoredReportsPendingBehindAndCurrent`, `TestStoredStateOfARow` (`freshness_test.go`); `session/query` `TestListingReadsStoredSummaries`; `TestDispatchByTypeAndContainedPanic` | A session 2,500 events past its newest row, read by a process that is not the holder: `Exact` fails `view/behind` with `AsOfSeq` 1 and `HeadSeq` 2501; `Stored` returns `behind` at once, with no log read. A stale fold lists as `pending` with zero log reads. | pass |
| `DH-T-VIEW-8` | 15.2.7, 15.2.11 | `session/store/sqlite` `TestPruneRemovesOnlyUnknownAndOldRows`, `TestPruneWaitsForAnOlderLiveHost`; `TestForkStateEqualsFoldOfItsLogicalLog`, `TestDeletedSessionLeavesNoDerivedRow` (`lineage_test.go`) | Prune removes only the unknown-key and old-version rows and keeps the known and `core/surface` rows; with a live older host it deletes nothing. A fork's state equals a fold of its logical log; a deleted session leaves no derived row. | pass |
| `DH-T-VIEW-9` | 15.2.10, 15.2.11 | `TestBuiltinViewGoldens` (`views/testdata/builtin/*.golden`: `list_summary`, `turn_index_unsettled`, `turn_index_settled`, `stats`, `event_index_replaced`), `TestRefoldEqualsStoredForEveryBuiltinView` | Each built-in view's state over a fixed log equals its committed golden; the stored state of every built-in view equals a refold (Stage 8.2 below). | pass |

**The cadence of 15.2.4** (no `DH-T` ID). Tests: `TestPeriodicAndEveryCommitCadences`,
`TestCadenceAndConfigValidation`, `TestEventIndexIsWrittenPeriodically`. Bytes upserted
into `checkpoints` while 10,000 events are appended:

| Rows | after 2,500 events | after 5,000 | after 10,000 | Growth |
| --- | --- | --- | --- | --- |
| every-commit rows (list summary, statistics) | 319,513 B | 645,068 B | 1,301,826 B | linear |
| event index (`Periodic`) | 1,704,884 B | 6,970,081 B | 28,768,077 B | about n^2/k (H-36) |
| `core/` rows | 4,492 B | 15,467 B | 56,920 B | -- |

The event index was written 101 times (100 turn ends and the close), 55.6 to 56.9 bytes
per covered event, so each write is linear in the events it covers, and 101 writes are
within the bound turns + n/200 + 2 (100 + 50 + 2 = 152). The views read at `Exact` after
the close, and those a reopening process restores from the rows, equal a refold
(`TestEventIndexIsWrittenPeriodically`). Writing the event index on every batch would
have upserted about 675 MB. Because the built-in views are whole checkpoint
documents (M1-D-STORE-C (a)), the event index's total grows as about n^2/k, where k is
the turn length; under M1-D-STORE-E (a) the test asserts what holds, and linear growth is
held open as H-36 until the view tables at DH-S-23.

**Status of 15.2.8** (no `DH-T` ID). `Registry.Status` returns, per view key and version,
the sessions `behind`, `pending` and `failed`. Tests: `TestStatusCountsBehindPendingAndFailed`
(on SQLite and `memstore`), `TestStatusCountsADroppedRowUntilItIsWritten`,
`TestRebuildMarksFailures`, `TestFoldCountsAndPending` (`session/store/sqlite` and
`memstore`). `Failed` is this process's knowledge only and is lost on restart
(`fold-failed-in-process-only`).

**A defect found by the 15.2.10 golden: a compaction counted as a prompt.** A
`user/message` with a `Replace` operation (a compaction's summary) was counted as a human
prompt in the list summary and in `last_prompt_at`, `PromptKey` and the prompt-order
list. It is fixed by `store.IsPrompt`; the list summary's fold version goes from 1 to 2;
the test is `TestCompactionIsNotAPrompt` (`prompt_test.go`). Left as it is: the layout-4
backfill step still fills `last_prompt_at` from every `user/message`, so a home upgraded
from layout 3 or older may hold a compaction's time until its next prompt (scratch homes
only); and `TurnIndex.Prompt` still takes the turn's first `user/message` of any
operation.

### Manual check: bump a fold's version on 50 sessions

`manual`, M1-D-CLOSE-2 option A (a scripted composed test, watched): this is the 15.2 exit
check. It does not fill the impl.md register below (plan step 18.1.5) and ticks no
scenario box.

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 8.2 (plan 15.2 exit) | bump a fold's version on a scratch home with 50 sessions; record `Status` moving from `pending` to `current` | composed test: `go -C src/harness test -count=1 -v -run '^TestManualCheckStatusAfterAVersionBump$' ./internal/session/views/` | WSL (also Windows earlier) | the lead (Claude), scripted composed test | `6e7514cdb6fbc8f105238e7e655fb20e3102166b` (WSL) | "before the rebuild current=0 behind=0 pending=50 failed=0"; "after the views-rebuild duty current=50 behind=0 pending=0 failed=0". Windows, run earlier: the same lines. | `manual` |

## Stage 8.2 -- Builtin Views, Projects and Session Listing

**What this section records.** Stage 8.2 of the story plan builds impl.md steps 8.3 (the
built-in views), 8.4 (projects group sessions; a hidden session never keeps working) and
8.5 (sessions are listed, observed, traced and followed across forks), and it closes
DH-S-8 (operator decision `dsh-s8-closure-waits-stage-8-2`). Its code and its three
scenario tests were merged in #64; the closure was never written (gap M1-G-STORE-25).
Plan step 15.3.7 writes it here, after Phase 15 added the tests the steps were missing
(15.3.1 to 15.3.3) and the storage proofs of 15.3.4 and 15.3.5. Gate evidence: both lanes
`PASSED all 12 gates` at `6e7514c` (as in the addendum above).

### Step 8.3: the built-in views

The four built-in views are the list summary, the turn index, the statistics and the
event index. They stay checkpoint documents, not the spec's tables, until DH-S-23
(M1-D-STORE-C (a)).

| Scenario / ID | Test | Result |
| --- | --- | --- |
| Refold equals stored for every built-in view (DH-S-8 exit evidence, TC-08; `DH-T-VIEW-9`) | `session/views` `TestRefoldEqualsStoredForEveryBuiltinView`: a fixed 200-event session in a temp SQLite home; each of the four views refolded from the log equals its stored state (and the state through the row's `seq`); the event index's classes equal the surface fold's, and every shadowed entry was replaced | pass |
| Golden state of each view (`DH-T-VIEW-9`) | `TestBuiltinViewGoldens` (addendum above) | pass |
| A checkpoint plus the suffix equals the full fold (`DH-T-VIEW-1`) | `TestCheckpointPlusSuffixEqualsFullFold` (addendum above) | pass |

### Step 8.4: projects and hidden sessions

| Scenario / ID | Test | Result |
| --- | --- | --- |
| Listing groups by project (TC-08) | `session/views` `TestListingGroupsByProject`: three listed sessions in two projects (a fourth, archived, is not listed); the listing equals the committed golden `session/views/testdata/list_by_project.golden` (projects in registry order, the pinned session first, then by last prompt) | pass |
| Hidden session stops working (TC-08 logic) | `storage/workspace` `TestHiddenSessionStopsWorking`: hiding the session sends its agent one stop with cause `hidden`, its next step is rejected by the workspace guard, and after unarchiving the step enters again | pass |

### Step 8.5: listing, observing, tracing and following

| Scenario / ID | Plan step | Test (package `session/query` unless named) | Result |
| --- | --- | --- | --- |
| Trace across forks (`DH-T-VIEW-13`) | 15.3.3 | `TestTraceAcrossForks` (`query_test.go`) | pass |
| A caller sees only what it may (`DH-T-VIEW-14`) | 15.3.3 | `TestCallerSeesOnlyWhatItMay` (`visibility_test.go`) | pass |
| List pages are bounded (`DH-T-VIEW-15`) | 15.3.3 | `TestListPagesAreBounded` (`visibility_test.go`) | pass |
| Paging stays stable while sessions change (`DH-T-VIEW-15`) | 15.3.1 | `TestListPagingIsStableWhileSessionsChange` (`paging_test.go`): 1,000 sessions; a writer adds prompts and creates sessions between pages; every visible session appears exactly once | pass |
| Rows read per page do not grow (`DH-T-VIEW-15`) | 15.3.1 | `TestListRowsPerPageDoNotGrow` (`paging_test.go`): summaries, opens, rows and records read per page are `[20 20 100 0]` at 100 and at 10,000 sessions, on the first and a near-last page | pass |
| Each list statement searches its index | 15.3.1 | `session/store/sqlite` `TestListPagesSearchTheirIndex` (`listplan_test.go`): `EXPLAIN QUERY PLAN` shows `SEARCH` for every list statement | pass |
| The listing reads stored summaries (`DH-T-VIEW-7`) | 15.2.3 | `TestListingReadsStoredSummaries` (`stored_test.go`) | pass |
| The open is bounded (`DH-T-STORE-18`, `-10`; M1-D-STORE-F) | 15.3.1 | `session/store/sqlite` `TestOpenIsBoundedByTheNewestCheckpoint`, `TestReadsJudgeWhatTheOpenDidNot` ([DH-S-7.md, "Gap closure (M1-G-STORE)"](DH-S-7.md#gap-closure-m1-g-store)) | pass |
| 10k-session list benchmark (`DH-T-VIEW-15`) | 15.3.1 | `BenchmarkBudgetList10k` (`paging_bench_test.go`, `-tags bench`) | pass on Windows (figures below); WSL pending |
| First list page over the corpus; snapshot of a session above 100,000 events (`DH-T-VIEW-25`, DH-D-VIEW-5 clauses) | 15.3.2 | `BenchmarkBudgetQuery` (`budget_bench_test.go`, `-tags bench`) | pass on Windows (figures below); WSL pending; budgets provisional (H-35) |

`DH-T-VIEW-25`'s ranked-search, literal-search and `TreeMessages` clauses are not part of
step 8.5: under M1-D-STORE-B (a) they belong to DH-D-VIEW-6 at DH-S-23 (impl.md step
23.9).

**Budget figures (`DH-T-VIEW-15`, `DH-T-VIEW-25`).** Windows: i9-13900K, go1.27.4,
`-benchtime 20x`, after the bounded open of 15.3.1; each p95 is the range over the plain
run and the `GOMEMLIMIT=4GiB` run, which never bound (the heap after an operation stayed
at or below 250 MB). Budgets are about 3 times the worst p95 and provisional (M1-D-STORE-B
(a); H-35 for `DH-T-VIEW-25`). Command:
`go -C src/harness test -tags bench -run '^$' -bench 'BudgetQuery|BudgetList10k' -benchtime 20x -timeout 60m ./internal/session/query/`.

| Case | Windows p95 | WSL p95 | Budget |
| --- | --- | --- | --- |
| query: first list page over the corpus | 21.3-23.0 ms (7.1 MB) | 8.9-9.4 ms (6.7 MB) | 70 ms |
| query: snapshot of a session above 100,000 events | 47.3-47.9 ms (67.5 MB) | 44.1-47.8 ms (67.3 MB) | 150 ms |
| list: 10,000 sessions, first page | 31.3-32.1 ms | 10.4-11.5 ms | 100 ms |
| list: 10,000 sessions, deep page | 22.0-22.6 ms | 7.0 ms | 70 ms |

The WSL column is from `runs/wsl-6e7514c-bench.txt` (sha256 `2b9f3065905c822aeb921ce346713039447eb13efd133bff250bb1d6ebfe4fe6`): WSL Ubuntu-24.04 at `6e7514c`, go1.27.4, `-tags bench -benchtime 20x`, every case PASS within its budget.

Before the bounded open (the first measured run, Windows): query first page 530-547 ms
and 726 MB; snapshot 578-733 ms and 790 MB. The store's own cases (open, fork, store first
page) are in [DH-S-7.md](DH-S-7.md#budget-figures-f-2-dh-t-store-18). Open: the
10,000-session first page is about 40% slower than the 1,000-session home's at equal row
counts, and the cause was not found.

### Storage proofs (plan 15.3.4, 15.3.5)

| Test ID | Tests | What they show | Result |
| --- | --- | --- | --- |
| `DH-T-STORAGE-7` | `storage/blob` `TestKilledPublisherNeverLeavesADanglingCitation` (`crash_test.go`) | 20 rounds: a re-executed child `Put`s a blob and then appends an event citing it, killed at a rotating point (between publish and append, after the append, mid `Put` or append). After each reopen every `blob_refs` object exists and verifies its length and digest, `ReferencedBy` holds, and the row count equals the event count. The seed is logged (8845788985185224334 in one run) and replays with `DSH_BLOB_CRASH_SEED`. The draft clause waits for H-33. | pass |
| `DH-T-STORAGE-1` (tagged) | `storage/kv` `TestUpdateCommitsAtomicallyAndSurvivesRestart`, `TestUnknownCommitOutcomeReloads`; `storage/workspace` `TestMachineStateSurvivesRestart` | | pass |
| `DH-T-STORAGE-2` (tagged) | `storage/kv` `TestFreshSeesForeignWrites`, `TestFreshRaisesAForeignDelete`, `TestRunRefreshesOnItsTimer`, `TestUnknownCommitOutcomeReloads`; `storage/workspace` `TestRegistryOverABusyDatabase` | | pass |
| `DH-T-STORAGE-3` (tagged) | `storage/kv` `TestRecordVersions`, `TestUndecodableByAuthority`, `TestPutAndDeleteGuards`, `TestUpdateRefusesAForeignUndecodableRecord` | | pass |
| `DH-T-STORAGE-5` (tagged) | `storage/blob` `TestPutAndReadBack`, `TestEqualBytesAreOneObject`, `TestCorruptedObjectDetected`, `TestTruncatedAndMissingObjects`, `TestTooLargeLeavesNoObject`, `TestReadDetectsAGrownObjectAndANonFile`, `TestLookupRefusesForeignShapes`, `TestSameSizeCorruptObjectNotAdopted` | | pass |
| `DH-T-STORAGE-6` (tagged) | `storage/blob` `TestADifferentObjectUnderTheNameIsNotAdopted`, `TestPublishSyncsTheObjectDirectory`, `TestPublishFailsWhenTheDirectorySyncFails`, `TestRetryAfterFailedPublishLeavesTheObjectReadOnly` | The image-batch clause waits for DH-D-STORAGE-4. | pass |
| POSIX tests on WSL (M1-G-STORE-30) | `session/store/sqlite` `TestLivenessLockSurvivesOSOpenAndClose` (`DH-T-STORE-7`), `TestStoreFilesAreOwnerOnly`; `storage/spill/local` `TestPlantedLinkRefused`, `TestSpillFilesArePrivate` (Unix) | Run on WSL at `6e7514c` with `TMPDIR=/tmp` on ext4, go1.27.4: each PASS, none SKIP. They also run inside the WSL gate lane. | pass |

**Clauses the tagged tests do not fully assert.** A tag says which ID a test serves; it
does not make the ID fully proven. These clauses have no test yet, so these IDs are
**not** claimed as fully proven:
- `DH-T-STORAGE-1`: no `Changed` listener on a second connection is asserted, and no test
  shows that concurrent `Update` calls do not interleave.
- `DH-T-STORAGE-2`: `storage/busy` is shown only through fake busy databases.
- `DH-T-STORAGE-3`: "rewritten only on its next write" is not checked.
- `DH-T-STORAGE-5`: no wrong-length or wrong-dimension `Ref` case (the dimension case
  waits for DH-D-STORAGE-4).
- `DH-T-STORAGE-6`: only a directory-sync fault is injected; no object-file sync fault.
  The image-batch clause waits for DH-D-STORAGE-4.
- `DH-T-STORAGE-7`: the draft clause waits for the draft record (H-33).

### Scenarios authored

| ID | File | Runs in | Status |
| --- | --- | --- | --- |
| DH-E-STORAGE-1 (two processes share machine state; an archive with stop ends the other process's turn) | `src/harness/internal/testkit/scenarios/storage_workspace_scenario_test.go` | DH-S-19 | authored, **not run** |
| DH-E-STORAGE-2 (an image survives a crash mid-upload, a restart, a fork whose parent is deleted, and collection) | `src/harness/internal/testkit/scenarios/storage_image_scenario_test.go` | DH-S-23 | authored, **not run** |

Both are in `test/harness/acceptance-index.yaml` (`authored: DH-S-8`), and
`harness-gate preflight` binds each to its test. No acceptance-run evidence exists for
either, and neither is claimed as passing.

### Manual checks of steps 8.3 to 8.5

impl.md DH-S-8 names no check for steps 8.3 to 8.5, so each takes the rule-3 default:
start the step's component in a composed tree, exercise it once and record that the
behaviour of its Result line was seen. Each row below is a scripted composed test run
with `-v` and watched (M1-D-CLOSE-2 option A, the plan's default; the decision is still
open). They are `manual` records of "built": they do not fill the per-step register at
the end of this file (plan step 18.1.5) and tick no scenario box.

Run on Windows (go1.27.4 windows/amd64) at `d10440b28d7628a6d84a40af79093458322d1535`,
2026-11-23 about 06:40Z. `d10440b` adds only run records to `6e7514c`
(`git diff 6e7514c d10440b -- src tools` is empty), so the code is the gate commit's.

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 8.3 | rule-3 default: the built-in views equal a refold of the log | composed test: `go -C src/harness test -count=1 -v -run '^(TestRefoldEqualsStoredForEveryBuiltinView\|TestListingGroupsByProject)$' ./internal/session/views/` | Windows | Claude (records agent for the lead), scripted composed test | `d10440b` | `--- PASS: TestRefoldEqualsStoredForEveryBuiltinView (0.08s)`. The test prints no figures; its pass means the four stored views of the 200-event session equal their refolds. | `manual` |
| 8.4 | rule-3 default: projects group sessions; a hidden session never keeps working | the same run for `TestListingGroupsByProject`; `go -C src/harness test -count=1 -v -run '^TestHiddenSessionStopsWorking$' ./internal/storage/workspace/` | Windows | Claude (records agent for the lead), scripted composed test | `d10440b` | `--- PASS: TestListingGroupsByProject (0.08s)` (the listing equals the golden: project alpha with `s-a1` pinned first, then `s-a2`; project beta with `s-b1`; archived `s-a3` absent). `--- PASS: TestHiddenSessionStopsWorking (0.00s)`. | `manual` |
| 8.5 | rule-3 default: sessions are listed, observed, traced and followed across forks | composed test: `go -C src/harness test -count=1 -v -run '^(TestTraceAcrossForks\|TestCallerSeesOnlyWhatItMay\|TestListPagesAreBounded\|TestListPagingIsStableWhileSessionsChange\|TestListRowsPerPageDoNotGrow\|TestListingReadsStoredSummaries)$' ./internal/session/query/` | Windows | Claude (records agent for the lead), scripted composed test | `d10440b` | All six PASS (paging-stable 3.20 s; rows-per-page 33.86 s). Printed: "100 sessions: [summaries opens rows records] first page [20 20 100 0], deep page [20 20 100 0]" and the same line for 10000 sessions (seeded in 33.3 s). | `manual` |

## Stage 8.3 -- Spill Store and Attachments

### What was built

| Step | Package | What it does |
| --- | --- | --- |
| 8.6 DH-D-STORAGE-5 | `internal/storage/spill` | The contract: `Store` (`Reserve`, `Create`, `Open`, `Stat`, `Delete`, `HostPath`), the id form `<16 hex of SHA-256(session)>/<24 hex random>-<sanitised name>` (at most `MaxIDLen` bytes), `ParseID` (refuses any string that could leave `<home>/spill`), `SanitizeName`, `Save` (a text over the bound is saved whole and returns its head, cut on a rune boundary, plus the `event.Spill` reference) and `Head`. |
| 8.6 | `internal/storage/spill/local` | The provider over `<home>/spill`: owner directories `0700` / protected DACL, files created exclusively and private from their first byte (`home.CreatePrivateFile`), a writer that enforces the size limit while writing and removes the file on over-limit, failure or `Abort`, file and directory synced on `Close`; reads through `os.Root` (no escape, no link out of the root) for the owner and sessions forked from it (`ForkLineage` over `Store.Stat`'s `ForkSource`), `spill/not-owner` for anyone else; `Delete` keeps files another session cites. The owner directory is the name the session store's deletion already removes (`sqlite.SpillName`). |
| 8.7 DH-D-STORAGE-3 | `internal/storage/blob` | Content-addressed objects at `<home>/blobs/v1/<kind>/<2 hex>/<sha256>`: streamed into a private staging file in `blobs/tmp/` while hashed, synced, published by an exclusive hard link, directory synced, then read-only; an equal object already there is success only once its length and digest verify (a same-size object with a flipped byte is `attachment/corrupt` and is never adopted), and then is made read-only (a publish that failed on its directory sync left it writable), gets its time updated and has its directory synced. `Open` checks the length and verifies the digest while streaming: a changed byte fails with `attachment/corrupt` "digest mismatch", a truncated or grown object with `attachment/corrupt`, a missing one with `attachment/not-found`. |
| 8.7 (gc.lock, blob_refs) | `internal/storage/blob`, `internal/session/store/sqlite`, `internal/util/fsx` | Built now by operator decision `defer-gc-lock-blob-refs`. `Put` and `Touch` hold `<home>/blobs/gc.lock` shared (`fsx.LockShared`: `flock(LOCK_SH)` / `LockFileEx` without the exclusive flag); `LockCollection` takes it exclusively for a collection batch, so no object is removed between a save and the event that cites it. Payloads that cite objects implement `blob.Referrer` (`BlobRefs() []string`, "<kind>:<id>"; `event.ToolResultPayload` cites `spill:<id>`); the SQLite store's layout 3 adds `blob_refs (session, seq, ref)`, written in the append, import and seed transactions (`store.BlobRefs`), copied with a fork's prefix, removed with the session, and backfilled from logged tool results when an older database is opened. `blob.Store.ReferencedBy` reads it through `blob.Index` (`sqlite.Store.Cites`). A session deletion consults the same rows: it keeps each spill file another active session (a fork) cites and removes the rest without following links (inherited files in a foreign directory are removed through an `os.Root` handle on `<home>/spill`, and a directory that is a link or junction is left alone), and the last citing session's deletion removes the kept file; the step repeats unchanged when an interrupted deletion is finished by the next open. `sqlite.Store.CitedByOther` is the spill store's `local.Citer`. Layout 4 adds `lineage (session, depth, ancestor)`: every ancestor id of a fork, written by every path that inserts a session with a fork source (fork, import, seed: `recordLineage`, completed by the named ancestor's own record when the home holds it) and backfilled from the `fork_source` chain, so `sqlite.Store.Ancestors` (the spill store's `local.Ancestry`) still authorizes a grandchild's read of an inherited file after the intermediate fork is deleted. The record is not taken as complete: the spill store also walks the header chain, and either answer suffices. |
| 8.7 | `internal/storage/attachments` | Files a person attaches: one `file` object plus a read-only named link `<home>/blobs/v1/names/<2 hex>/<sha256>/<name>` for file tools; size limit enforced while streaming (`attachment/too-large`); reads verified through the blob store. |
| shared | `internal/storage/home` | `EnsurePrivateDir` (syncs the parent of the directory it creates or adopts before success, so a retry after a failed sync, or a writer adopting another's new directory, never publishes below an entry that is not durable; `Ensure` creates missing ancestors of the home one level at a time, syncing each one's parent and removing a level whose parent sync failed so a retry syncs it, then syncs the home's parent and the home; `TestEnsureSyncsNewAncestors`), `CreatePrivateFile` (Windows: `CreateFile` with a protected DACL in its security attributes; POSIX: `O_EXCL` `0600`), `SafeSegment`. `internal/util/fsx.SyncDir`. |

### Tests (TC-08)

Run with `go -C src/harness test -count=1 -v ./internal/storage/...`:

| Scenario (implementation-plan Stage 8.3) | Test | Result |
| --- | --- | --- |
| Large result spilled and recoverable | `spill/local` `TestLargeResultSpilledAndRecoverable` | pass. A 5,242,880-byte result through `spill.Save` with a 16,384-byte bound, logged as a `tool/result` v2 event in a SQLite home: the row is 16,616 bytes (head 16,383 bytes, cut before a two-byte rune, plus the `spill` field); reading the reference returns all 5,242,880 bytes byte-identical; its owner directory equals `sqlite.SpillName`. |
| Corrupted attachment detected | `blob` `TestCorruptedObjectDetected`; `attachments` `TestCorruptedAttachmentDetected` | pass: one byte flipped on disk, the read fails with `attachment/corrupt` "digest mismatch". |
| Spill files are private | `spill/local` `TestSpillFilesArePrivate` (`private_windows_test.go`, `private_unix_test.go`) | pass on Windows: the file, its owner directory and `spill/` have `SE_DACL_PROTECTED` and every ACE, read with `GetNamedSecurityInfo`, is for the owner SID, `SYSTEM` or `Administrators`. The Unix test (umask 0, file `0600`, directories `0700`) is vetted for linux and darwin here and runs on the CI's Linux job. (Vetting for darwin is a cross-build check, not a macOS run; macOS is out of scope, `platforms-windows-wsl`.) |

DH-T-STORAGE-9 parts: `TestSanitizeName`, `TestEscapingIDsRefused`, `FuzzParseID`,
`TestOverLimitAndAbortLeaveNoFile`, `TestCreateIsExclusive`,
`TestOpenOwnerForkUnrelated`, `TestForkLineageFollowsForkSource`,
`TestDeleteKeepsCitedFiles`, `TestPlantedLinkRefused` (skipped on this machine:
creating a symbolic link needs a privilege it does not hold; it runs on Linux CI).
Blob and attachment edges: `TestTooLargeLeavesNoObject`,
`TestTruncatedAndMissingObjects`, `TestReadDetectsAGrownObjectAndANonFile`,
`TestADifferentObjectUnderTheNameIsNotAdopted`, `TestSameSizeCorruptObjectNotAdopted`, `TestHostPathAndLookupEdges`.
Durability (each records the directories synced and injects a failing sync): `home`
`TestNewDirectorySyncsItsParent`, `blob` `TestPublishSyncsTheObjectDirectory` and
`TestPublishFailsWhenTheDirectorySyncFails`, `TestRetryAfterFailedPublishLeavesTheObjectReadOnly`, `attachments` `TestNamedLinkSynced` and
`TestSaveFileFailsWhenTheLinkSyncFails`, `spill/local` `TestCloseSyncsTheSessionDirectory`
and `TestCloseSyncFailureRemovesTheFile`.
Collection lock and references: `fsx` `TestLockShared`; `blob`
`TestSaversWaitForTheCollector` (Put and Touch wait under `LockCollection` and go
on once it is released; a saver mid-Put excludes the collector, not another
saver) and `TestReferencedByReadsTheIndex`; `session/store/sqlite`
`TestBlobRefsFollowAppendForkAndDelete`, `TestBlobRefsBackfilledByTheMigration`, `TestDeletionKeepsSpillFilesAForkCites`, `TestInterruptedDeletionKeepsSpillFilesAForkCites`, `TestSessionStoreIsTheSpillCiter`, `TestNestedForkReadsInheritedSpillAfterAncestorsAreDeleted` (p -> f -> g, p and f deleted, home reopened, g reads byte-identical, an unrelated session refused) and `TestImportedForkReadsInheritedSpillAfterAncestorsAreDeleted` (export f, import as f2, fork g; both read before and after p and f are deleted and the home reopened), `TestIncompleteRecordedLineageFallsBackToTheHeaders`, `TestDeletionNeverFollowsAPlantedSpillLink` (an outside sentinel behind a junction/symlink in `spill/` survives; fails against the previous path-joined removal) (all use a real `OpenHome` home and `spill/local` files: the fork reads the parent's bytes back byte-identical after the parent's deletion, also when that deletion is interrupted and finished by the next `OpenHome`).

Statement coverage of the new packages (merged across the storage tests): `spill`
88.1%, `spill/local` 91.1%, `blob` 87.8%, `attachments` 89.5%.

### Not built here (later steps of the storage area)

- The spill policy and `spill_read` tool (DH-D-STORAGE-6): the shaping listener
  that prices a result, builds the notice and calls `spill.Save`. Stage 8.3 builds
  the store it uses.
- Image admission, request images and crops (DH-D-STORAGE-4); quarantine
  (DH-D-STORAGE-10).
- The draft record (part of DH-D-STORAGE-3, step 8.7, by storage.md; corrected by plan
  step 15.3.9, gap M1-G-STORE-28): held open as H-33 (["Held open"](#held-open)),
  closing at DH-S-21's composer step (decision M1-D-STORE-D (b)). Historical: this
  list first assigned the draft record to DH-D-STORAGE-10, against storage.md.
- The blob collector itself (DH-D-STORAGE-9): the lock it takes (`LockCollection`)
  and the `blob_refs` rows it reads are built here; the walk and the deletion are
  not. A draft's own reference list is part of the draft record (H-33).
- Spill retention by `MaxAge` (DH-D-STORAGE-2 retention); the field is configured
  and validated.

### Scenario authored

`DH-E-STORAGE-3` (a five-megabyte command output bounded and recoverable under a
real sandbox), `src/harness/internal/testkit/scenarios/storage_spill_scenario_test.go`,
`TestFiveMegabyteOutputBoundedAndRecoverable`. Indexed under DH-S-39 (authored:
DH-S-8); it needs the real sandbox backends and the spill policy, so it runs
there, once per OS. `harness-gate require --stage DH-S-39` lists it and
`harness-gate preflight` with `DSH_REQUIRE_SCENARIOS=DH-E-STORAGE-3` binds it to
its test. It compiles under the `scenario` build tag (`go vet -tags scenario`) but has **not been run**: no acceptance-run evidence exists for it yet, and it is not claimed as passing here.

## Manual checks of impl.md DH-S-8

| Check | Test | Record |
| --- | --- | --- |
| Change a fold's version and record that its stored state is rebuilt on open | `TestChangedFoldVersionRebuilds` | A stored fold at version 1 over 20 events, re-registered at version 2: on open the stored state is rebuilt, records version 2 and its seq is the log head. pass |
| Write a five-megabyte result and record the bounded head and the recoverable rest | `TestLargeResultSpilledAndRecoverable` | 5,242,880 bytes; logged row 16,616 bytes with a 16,383-byte head under a 16,384-byte bound; the rest read back byte-identical through the reference. pass |

## Held open

A held-open entry is a part of a deliverable that is ticked as built (or will be) but
is not done: the part that waits is not enabled, and its step is not done, until the
closing step lands. H-33, H-35 and H-36 are the M1 story's entries for DH-S-8, added by
Phase 15 of the plan: decision M1-D-STORE-D (b) holds the draft record open (plan step 15.3.9),
M1-D-STORE-B (a) makes the `DH-T-VIEW-25` budgets provisional from their first
measured run (plan step 15.3.2), and M1-D-STORE-E (a) holds open linear growth of the
event index's writes until the views become tables (plan step 15.2.4). A *provisional budget* is a time and memory limit
taken from the first measured run: the benchmark fails when it is missed, but the
number is not final until it is pinned at its closing step. The entry is not H-10,
which is the replay of a session with children (lead settlement
`budget-held-open-not-h10`). The DH-S-7 entry H-34 holds the `DH-T-STORE-18` budgets
([DH-S-7.md, "Held open"](DH-S-7.md#held-open)). impl.md carries pointer lines to these
lists (DC-2); its table is unchanged. Each closing stage records the entries it closes.

| ID | Step | Deliverable | The part held open | Gap | Closing step | Status |
| --- | --- | --- | --- | --- | --- | --- |
| H-33 | 8.7 | DH-D-STORAGE-3 | The draft record: one record per session in the `kv` namespace `draft` that lists its staged uploads, so an unsent draft and its files survive a reload and no file a draft names is collected; with it `DH-T-STORAGE-7`'s draft clause (a draft's staged file survives a collection run and is collected after the draft is deleted) | M1-G-STORE-28 | impl.md 21.2, DH-S-21's composer step (M1-D-STORE-D (b)) | open |
| H-35 | 8.5 | DH-D-VIEW-5 | The final budgets of `DH-T-VIEW-25`'s DH-D-VIEW-5 clauses: the first list page over the heavy-tailed corpus and a snapshot of a session above 100,000 events, each also under `GOMEMLIMIT`. Until then the budgets are the provisional ones of plan step 15.3.2. The test's ranked-search, literal-search and `TreeMessages` clauses are not in this entry: they belong to DH-D-VIEW-6 at DH-S-23 (impl.md step 23.9) | M1-G-STORE-22, M1-G-STORE-7 | impl.md 16.7 (M1-D-STORE-B (a)) | open |
| H-36 | 8.2 | DH-D-VIEW-2 | Linear growth of the bytes the event index upserts. The four built-in views are checkpoint documents (M1-D-STORE-C (a)), so each `Periodic` event-index write is the whole document and the total grows as about n^2/k, where k is the turn length (28.8 MB over 10,000 events, measured at plan step 15.2.4). Until then the test asserts what holds: the every-commit rows grow linearly, each event-index write is linear in the events it covers, and the writes are at most turns + n/200 + 2 | M1-G-STORE-13 | the view tables at DH-S-23 (M1-D-STORE-E (a)) | open |

Mechanical fix (plan step 15.3.7; allowed and noted under DC-2): H-36's step was first
written as 8.1. Its deliverable, DH-D-VIEW-2, is step 8.2 (gap M1-G-STORE-13 also says
step 8.2), so this table and the two impl.md pointer lines that name H-36 now read 8.2.

## Closure

| Exit evidence (impl.md DH-S-8) | Where | Status |
| --- | --- | --- |
| Refold-equals-stored test for every built-in view | Stage 8.2, step 8.3 (`TestRefoldEqualsStoredForEveryBuiltinView`, `DH-T-VIEW-9`) | **met** at `6e7514c`, both lanes |
| Stored fold state never ahead of the log (step 8.2; M1-G-STORE-10 to -20) | "Stage 8.1 addendum (M1-G-STORE)" | **met** at `6e7514c`, both lanes; linear growth of the event index's writes held open (H-36) |
| Listing, tracing and paging (step 8.5; M1-G-STORE-21 to -23) | Stage 8.2, step 8.5 | **met** at `6e7514c`, both lanes; benchmark figures met on Windows, WSL pending (lead fills); `DH-T-VIEW-25` budgets provisional (H-35) |
| Storage proofs (step 8.7, 8.1; M1-G-STORE-27, -30) | Stage 8.2, "Storage proofs" | **met** at `6e7514c` for the asserted clauses; the clauses listed under "Clauses the tagged tests do not fully assert" are open, and the draft clause of `DH-T-STORAGE-7` is H-33 |
| Manual checks | "Manual checks" above (historical); the 15.2 Status check (`manual`, `6e7514c`, WSL) and the 8.3 to 8.5 checks (`manual`, `d10440b`, Windows) | recorded as `manual`; the per-step register stays open until plan step 18.1.5 |
| Scenarios authored | DH-E-STORAGE-3 (8.3); DH-E-STORAGE-1, -2 (8.2), in the acceptance index | authored, **not run**; they run in DH-S-39, DH-S-19 and DH-S-23, so no DH-S-8 deliverable that they prove is "done" |
| Spill addressing | storage.md DH-D-STORAGE-5: owner hash + 96 random bits + safe name | **resolved** by operator decision `spill-addressing` (storage.md governs); implementation-plan.md Stage 8.3 step 8.6 amended to match |

DH-S-8 is closed as built by plan step 15.3.7: impl.md steps 8.3 to 8.5 are ticked with
the story plan's Stage 8.2 steps and its three scenario boxes, and `built-through` is
DH-S-8. The gate of record is the pair of run records `wsl-6e7514c-20261123T0602Z` and
`win-6e7514c-20261123T0602Z`, in place of a CI run id. The record's own
`Closure status:` line stays `open` until plan step 18.1.14.

M1 gate repair (plan 13.2.1, 13.2.3, 13.2.5; [DH-S-1.md, "M1 gate repair"](DH-S-1.md#m1-gate-repair)): `internal/storage/blob/gclock_test.go` `TestSaversWaitForTheCollector` runs in a `synctest` bubble ("Put still waits" is `synctest.Wait`, not a 30 ms settle) and replaces the `waitFor` poll with a signal from the lock (`SetAfterLockShared`, a test seam `afterLockShared` in `blob.go`, nil outside tests), with P-C for both savers; `storage/workspace/archive_test.go` (helper `goJoined`) and `util/fsx/replace_test.go` `TestLockShared` (the cleanup unlocks, then joins the waiter) release and join every goroutine they start. Assertions unchanged.

## Evidence record (impl.md sec. 7.3)

These are the eight fields of [impl.md sec. 7.3](../../../harness/impl.md#73-the-evidence-record),
in the format of [the evidence runbook](README.md#the-evidence-record-headings). Plan step
18.1.7 fills them at the frozen M1 closure commit. Plan Stage 15.2 and step 15.3.7 added
the "Stage 8.1 addendum (M1-G-STORE)" and Stage 8.2 sections and fill the Phase 15 lines
below. The history above stays exactly as it was recorded and is not closure evidence
(M1-D-CLOSE-1).

### Source and build identity

**At the closure commit (plan 18.1.7).** Commit `593ceaa3efd55d41bf0eeaced5ba7e26acc71966`: one commit for the three modules, on branch `m1-phase-18` over `main` 896c760. Go `go1.27.4` (the go floor). The WSL rehearsal built six `dsh` binaries, each with `vcs.revision=593ceaa…` and `vcs.modified=false`; their SHA-256 values are in [wsl-593ceaa-20261123T2122Z-rehearse.txt](runs/wsl-593ceaa-20261123T2122Z-rehearse.txt).

At closure: not yet recorded (plan 18.1.7). Phase 15 (gap closure): commit
`6e7514cdb6fbc8f105238e7e655fb20e3102166b`; the 8.3 to 8.5 manual checks ran at
`d10440b`, which adds only run records to it. Historical: no commit was recorded; Stage
8.1 names its PR (#63) and Stage 8.2 its PR (#64).

### Environment and fixture

**At the closure commit (plan 18.1.7).** Windows 11 Enterprise 10.0.28000 native, with `CC` MSYS2 gcc. WSL Ubuntu 24.04.5 LTS, kernel 6.18.58.1-microsoft-standard-WSL2, ext4 clone, gcc 13.3.0. macOS is out of this story (`macos-return-stage`). The manual checks ran on the same workstation (decision `manual-checks-run-by-lead`). Signed-in clients: Claude Code 2.1.312 and GitHub Copilot CLI 1.0.115-7, Windows only. Fixtures: those named by this stage's register rows.

At closure (18.1.7): both local lanes, from their run records at the closure commit.
Phase 15 (gap closure), from the two `6e7514c` run records: WSL lane Ubuntu 24.04.5 LTS
on WSL2, kernel `6.18.58.1-microsoft-standard-WSL2`, go1.27.4, gcc 13.3.0, ext4 clone;
Windows lane Windows 10.0.28000.3501, go1.27.4, gcc 16.1.0 (MSYS2), NTFS checkout (the
full template is in [DH-S-7.md, "Gate runs at 6e7514c"](DH-S-7.md#gate-runs-at-6e7514c)).
Bench figures: Windows i9-13900K; WSL pending. macOS: out of scope
(`platforms-windows-wsl`). Historical: the Windows machine named at the top of this file.
Fixtures: temporary SQLite homes; a 5,242,880-byte result; the committed goldens
`session/views/testdata/list_by_project.golden` and `views/testdata/builtin/*.golden`;
the heavy-tailed corpus `internal/testkit/corpus`.

### Exact tests and commands

**At the closure commit (plan 18.1.7).** `tools/harness-gate/lanes/win.ps1` and `tools/harness-gate/lanes/wsl.sh` (the gate with `--evidence`; `rehearse` in WSL). `DSH_REQUIRE_SCENARIOS` is unset. For the register: `evidence/runs/manual-593ceaa/run-register.sh` (Windows, and `--os wsl`), with the by-hand steps of `by-hand-checklist.md`; the per-row commands are in this record's register.

At closure: the lane scripts, and `go -C src/harness test -race -count=1
./internal/storage/... ./internal/session/views/ ./internal/session/query/`. Phase 15:
the lane scripts, `go test -race` per package, and the bench and manual-check commands in
"Stage 8.1 addendum (M1-G-STORE)" and "Stage 8.2". Historical: the commands under Stage
8.1 and "Tests (TC-08)".

### Run identifiers

**At the closure commit (plan 18.1.7).** Windows lane `win-593ceaa-20261123T2122Z` ([record](runs/win-593ceaa-20261123T2122Z.txt)) and WSL lane `wsl-593ceaa-20261123T2122Z` ([record](runs/wsl-593ceaa-20261123T2122Z.txt), [rehearse](runs/wsl-593ceaa-20261123T2122Z-rehearse.txt)). Register logs: `register-windows-593ceaa-20261123T2213Z` and `register-wsl-593ceaa-20261123T2237Z` in [runs/manual-593ceaa/](runs/manual-593ceaa/byhand-results.md). The WSL rehearse record was rebuilt from the lane's tee'd output after the original file was deleted by mistake; the gate record's own `sha256:` line verifies it byte for byte.

At closure (18.1.3, 18.1.7): the run-record ids of both lanes at the frozen closure
commit; not yet run. Phase 15 (gap closure), at `6e7514c`: WSL lane
`wsl-6e7514c-20261123T0602Z` (file SHA-256
`e643fb9c05106103196578bbc483b4fa199c066baa3d3be5d7f3c5fcfbefa815`), Windows lane
`win-6e7514c-20261123T0602Z` (file SHA-256
`69bd371846922b7e1f30eb65c11de10da067267112447a95f8594485fc71a6cf`). macOS: out of scope
(`platforms-windows-wsl`). Historical: no run id was recorded before Phase 15.
DH-E-STORAGE-1, -2 and -3 run in DH-S-19, DH-S-23 and DH-S-39 and have no run id yet.

### Results

**At the closure commit (plan 18.1.7).** Both lanes `PASSED all 12 gates`, with `trace-tests` in the `trace` gate. `trace-tests` for DH-S-1 to DH-S-11 reports required 310, tagged 288, deferred 22, missing 0. This stage's register: see "Manual checks register" (every row PASS or recorded as seen; not run and pending rows are open, never a pass).

At closure: not yet recorded. Phase 15: both lanes `PASSED all 12 gates` at `6e7514c`;
per test ID in "Stage 8.1 addendum (M1-G-STORE)" and "Stage 8.2". Historical: the test
tables of Stage 8.1 and Stage 8.3.

### Manual checks

impl.md DH-S-8 names two checks: change a fold's version and record that its stored
state is rebuilt on open (step 8.2); write a five-megabyte result and record the bounded
head and the recoverable rest (step 8.6). The other steps take the rule-3 default; for
steps 8.3 to 8.5 the tests were completed by plan Stages 15.2 and 15.3, and their Phase
15 `manual` records are in "Stage 8.2", "Manual checks of steps 8.3 to 8.5". Historical record: "Manual checks of impl.md DH-S-8". How to run and fill the
register: [the evidence runbook](README.md#manual-checks). Composed tests run as
`go -C src/harness test -count=1 -v -run '<names>' <package>`.

#### Manual checks register

**Run at the M1 closure commit (plan 18.1.5, 2026-11-23).** The lead Claude Code session ran every row below at `593ceaa3efd55d41bf0eeaced5ba7e26acc71966`, at the owner's instruction of 2026-11-23 ("you should do that"); the owner did not watch these runs. This departs from M1-D-CLOSE-2 A (a named person runs it at the recorded commit, watches it and records it `manual`) and from the runbook rule that signed-in checks are never run from an agent session, and it needs to be recorded as an owner decision. Logs: `register-windows-593ceaa-20261123T2213Z.log`, `register-wsl-593ceaa-20261123T2237Z.log`, `byhand-windows-593ceaa.log`, `byhand-windows-593ceaa-part2.log`, `byhand-wsl-593ceaa.log`, `signedin-dhs9-593ceaa.log`, `signedin-m1-claude-593ceaa.log` and `signedin-m1-copilot-593ceaa.log`. The WSL rehearse record (`wsl-593ceaa-20261123T2122Z-rehearse.txt`) was rebuilt from the lane's tee'd output after the original file was deleted by mistake.

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 8.1 | rule-3 default: program state survives restarts in one typed store | composed test: `TestMachineStateSurvivesRestart` in `./internal/storage/workspace/`, `TestUpdateCommitsAtomicallyAndSurvivesRestart` in `./internal/storage/kv/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestMachineStateSurvivesRestart`, `TestUpdateCommitsAtomicallyAndSurvivesRestart`. | `manual` |
| 8.2 | change a fold's version: its stored state is rebuilt on open | composed test: `TestChangedFoldVersionRebuilds` in `./internal/session/views/`; record the version and seq before and after | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestChangedFoldVersionRebuilds`; no version or seq lines were printed under `-v`; the rebuild assertions passed. | `manual` |
| 8.3 | rule-3 default: the built-in views equal a refold of the log | composed test: `TestRefoldEqualsStoredForEveryBuiltinView` in `./internal/session/views/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestRefoldEqualsStoredForEveryBuiltinView`. | `manual` |
| 8.4 | rule-3 default: projects group sessions; a hidden session never keeps working | composed test: `TestListingGroupsByProject` in `./internal/session/views/`, `TestHiddenSessionStopsWorking` in `./internal/storage/workspace/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestListingGroupsByProject`, `TestHiddenSessionStopsWorking`. | `manual` |
| 8.5 | rule-3 default: sessions are listed, observed, traced and followed across forks | composed test: `TestTraceAcrossForks`, `TestCallerSeesOnlyWhatItMay`, `TestListPagesAreBounded`, `TestListPagingIsStableWhileSessionsChange`, `TestListRowsPerPageDoNotGrow`, `TestListingReadsStoredSummaries` in `./internal/session/query/`; record the rows-per-page lines | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestListPagingIsStableWhileSessionsChange`, `TestListRowsPerPageDoNotGrow`, `TestTraceAcrossForks`, `TestListingReadsStoredSummaries`, `TestCallerSeesOnlyWhatItMay`, `TestListPagesAreBounded`; rows per page: `100 sessions: [summaries opens rows records] first page [20 20 100 0], deep page [20 20 100 0]` and `10000 sessions: ... first page [20 20 100 0], deep page [20 20 100 0]`. | `manual` |
| 8.6 | write a five-megabyte result: the bounded head and the recoverable rest | composed test: `TestLargeResultSpilledAndRecoverable` in `./internal/storage/spill/local/`; record the result size, the logged row size and head under the bound, and the byte-identical read-back | Windows, WSL | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS on both lanes. Windows (`register-windows-593ceaa-20261123T2213Z.log`) and WSL (`register-wsl-593ceaa-20261123T2237Z.log`): `--- PASS: TestLargeResultSpilledAndRecoverable`; `logged tool/result row: 16616 bytes (head 16383 of 5242880 bytes, bound 16384)` on both; the byte-identical read-back is the test's assertion. | `manual` |
| 8.7 | rule-3 default: attachments are kept durably and read back verified | composed test: `TestCorruptedObjectDetected` in `./internal/storage/blob/`, `TestCorruptedAttachmentDetected` in `./internal/storage/attachments/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestCorruptedObjectDetected`, `TestCorruptedAttachmentDetected`. | `manual` |

### Cleanup evidence

**At the closure commit (plan 18.1.7).** Every throwaway worktree of the manual checks was removed, and `git status --porcelain` of the main tree was empty before and after. The signed-in runs left no `claude` or `copilot` process (PID sets equal before and after). The WSL clone's tags (7, all created 2026-11-17) were unchanged, and nothing was published.

At closure: every temporary home is gone; `git status --porcelain` empty after the run.
Historical: none recorded.

### Open items

**At the closure commit (plan 18.1.7).** For this stage: H-33, H-35 and H-36 ("Held open" above). The M1-wide list is [DH-S-11 "Open items at M1"](DH-S-11.md#open-items-at-m1-plan-18111).

At closure: plan 18.1.11 lists them. Now open:
- the bench figures on WSL for `DH-T-VIEW-15` and `DH-T-VIEW-25` (the lead fills them
  after the WSL run);
- the held-open entries H-33, H-35 and H-36 (["Held open"](#held-open));
- the storage clauses listed under "Clauses the tagged tests do not fully assert"
  (Stage 8.2);
- the 10,000-session first list page being about 40% slower than the 1,000-session
  home's at equal row counts: cause not found;
- after the prompt fix, the layout-4 backfill of `last_prompt_at` and
  `TurnIndex.Prompt` still treat every `user/message` as a prompt ("Stage 8.1 addendum");
- DH-E-STORAGE-1, -2 and -3, authored and not run (DH-S-19, DH-S-23, DH-S-39);
- the items under "Not built here";
- the per-step manual-check register, run at plan step 18.1.5.

Closed as built by Phase 15: steps 8.3 to 8.5 and the Stage 8.2 closure (plan steps
15.3.7, 15.3.8).
