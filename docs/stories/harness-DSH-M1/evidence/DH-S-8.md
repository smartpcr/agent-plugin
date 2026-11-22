# DH-S-8 evidence

Decisions that govern M1 records, this one included, are logged in [decisions.md](../decisions.md).

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The stage records below are history: under M1-D-CLOSE-1 every tick made while the gate
was red is provisional and is re-verified at that commit, and plan step 15.3.7 writes
the Stage 8.2 closure. The fields of impl.md sec. 7.3 and the per-step manual-check
register are at the end of this file, in "Evidence record (impl.md sec. 7.3)"; how to
fill them is in [the evidence runbook](README.md).

DH-S-8 is machine state, views and spill. This file holds the evidence of its
stages as they are built. Stage 8.1 (typed machine state and stored folds) and
Stage 8.3 (spill store and attachments) are recorded below. **Closing DH-S-8 is
Stage 8.2's step** (operator decision `dsh-s8-closure-waits-stage-8-2`; the step
is in implementation-plan.md Stage 8.2): it adds the refold-equals-stored test
for every built-in view, ticks impl.md steps 8.3 to 8.5 and raises
`built-through` in the acceptance index to DH-S-8 (with DH-E-STORAGE-1 and -2,
which it also authors). impl.md steps 8.1, 8.2, 8.6 and 8.7 are ticked as built.

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
- Image admission, request images and crops (DH-D-STORAGE-4); the draft record
  and quarantine (DH-D-STORAGE-10).
- The blob collector itself (DH-D-STORAGE-9): the lock it takes (`LockCollection`)
  and the `blob_refs` rows it reads are built here; the walk and the deletion are
  not. A draft's own reference list belongs to DH-D-STORAGE-10.
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

## Closure

| Exit evidence (impl.md DH-S-8) | Where | Status |
| --- | --- | --- |
| Refold-equals-stored test for every built-in view | Stage 8.2 (impl.md step 8.3) | owned by Stage 8.2's closure step |
| Manual checks | "Manual checks" above | met |
| Scenarios authored | DH-E-STORAGE-3 (8.3) in the acceptance index; DH-E-STORAGE-1, -2 by Stage 8.2 | Stage 8.3's part met |
| Spill addressing | storage.md DH-D-STORAGE-5: owner hash + 96 random bits + safe name | **resolved** by operator decision `spill-addressing` (storage.md governs); implementation-plan.md Stage 8.3 step 8.6 amended to match |

Stage 8.3's evidence is complete. DH-S-8 closes, and `built-through` is raised to
DH-S-8, with Stage 8.2's closure step (steps 8.3 to 8.5, the refold-equals-stored
test and DH-E-STORAGE-1 and -2).

M1 gate repair (plan 13.2.1, 13.2.3, 13.2.5; [DH-S-1.md, "M1 gate repair"](DH-S-1.md#m1-gate-repair)): `internal/storage/blob/gclock_test.go` `TestSaversWaitForTheCollector` runs in a `synctest` bubble ("Put still waits" is `synctest.Wait`, not a 30 ms settle) and replaces the `waitFor` poll with a signal from the lock (`SetAfterLockShared`, a test seam `afterLockShared` in `blob.go`, nil outside tests), with P-C for both savers; `storage/workspace/archive_test.go` (helper `goJoined`) and `util/fsx/replace_test.go` `TestLockShared` (the cleanup unlocks, then joins the waiter) release and join every goroutine they start. Assertions unchanged.

## Evidence record (impl.md sec. 7.3)

These are the eight fields of [impl.md sec. 7.3](../../../harness/impl.md#73-the-evidence-record),
in the format of [the evidence runbook](README.md#the-evidence-record-headings). Plan step
18.1.7 fills them at the frozen M1 closure commit; plan steps 15.2 and 15.3.7 add the
"Stage 8.1 addendum" and Stage 8.2 sections before that. Until then each field points to
the history above, which stays exactly as it was recorded and is not closure evidence
(M1-D-CLOSE-1).

### Source and build identity

At closure: not yet recorded (plan 18.1.7). Historical: no commit was recorded; Stage 8.1
names its PR (#63).

### Environment and fixture

At closure: both local lanes, from their run records. macOS: out of scope
(`platforms-windows-wsl`). Historical: the Windows machine named at the top of this file.
Fixtures: temporary SQLite homes; a 5,242,880-byte result.

### Exact tests and commands

At closure: the lane scripts, and `go -C src/harness test -race -count=1
./internal/storage/... ./internal/session/views/`. Historical: the commands under Stage
8.1 and "Tests (TC-08)".

### Run identifiers

At closure: the run-record ids of both lanes, in the runbook's template (plan 18.1.3).
WSL lane: not run. Windows lane: not run. macOS: out of scope (`platforms-windows-wsl`).
Historical: no run id was recorded. DH-E-STORAGE-3 runs in DH-S-39 and has no run id yet.

### Results

At closure: not yet recorded. Historical: the test tables of Stage 8.1 and Stage 8.3.

### Manual checks

impl.md DH-S-8 names two checks: change a fold's version and record that its stored
state is rebuilt on open (step 8.2); write a five-megabyte result and record the bounded
head and the recoverable rest (step 8.6). The other steps take the rule-3 default; for
steps 8.3 to 8.5 the tests are written by plan Stage 15.3, so those rows cannot run before
it. Historical record: "Manual checks of impl.md DH-S-8". How to run and fill the
register: [the evidence runbook](README.md#manual-checks). Composed tests run as
`go -C src/harness test -count=1 -v -run '<names>' <package>`.

#### Manual checks register

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 8.1 | rule-3 default: program state survives restarts in one typed store | composed test: `TestMachineStateSurvivesRestart` in `./internal/storage/workspace/`, `TestUpdateCommitsAtomicallyAndSurvivesRestart` in `./internal/storage/kv/` | one lane | | | | `manual` |
| 8.2 | change a fold's version: its stored state is rebuilt on open | composed test: `TestChangedFoldVersionRebuilds` in `./internal/session/views/`; record the version and seq before and after | one lane | | | | `manual` |
| 8.3 | rule-3 default: the built-in views equal a refold of the log | composed test: `TestRefoldEqualsStoredForEveryBuiltinView`, written by plan 15.3.7 (not runnable before it) | one lane | | | | `manual` |
| 8.4 | rule-3 default: projects group sessions; a hidden session never keeps working | composed test: the hidden-session tests of plan 15.3 (named there; not runnable before it) | one lane | | | | `manual` |
| 8.5 | rule-3 default: sessions are listed, observed, traced and followed across forks | composed test: the listing tests of plan 15.3.1 to 15.3.3 (named there; not runnable before them) | one lane | | | | `manual` |
| 8.6 | write a five-megabyte result: the bounded head and the recoverable rest | composed test: `TestLargeResultSpilledAndRecoverable` in `./internal/storage/spill/local/`; record the result size, the logged row size and head under the bound, and the byte-identical read-back | Windows, WSL | | | | `manual` |
| 8.7 | rule-3 default: attachments are kept durably and read back verified | composed test: `TestCorruptedObjectDetected` in `./internal/storage/blob/`, `TestCorruptedAttachmentDetected` in `./internal/storage/attachments/` | one lane | | | | `manual` |

### Cleanup evidence

At closure: every temporary home is gone; `git status --porcelain` empty after the run.
Historical: none recorded.

### Open items

At closure: plan 18.1.11 lists them. Now open: steps 8.3 to 8.5 and the Stage 8.2
closure (plan Stages 15.2 and 15.3); the items under "Not built here"; DH-E-STORAGE-1, -2
and -3, which run in later stages.
