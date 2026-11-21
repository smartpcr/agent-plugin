# DH-S-8 evidence

DH-S-8 is machine state, views and spill. This file holds the evidence of its
stages as they are built. **Closure status: open -- waiting on Stage 8.2** (see
"Closure" at the end). Stage 8.1 (typed machine state and stored folds) and
Stage 8.3 (spill store and attachments) are recorded below. Stage 8.2 (built-in
views, projects and session listing; impl.md steps 8.3 to 8.5) is built on its
own branch and has not landed on this one, so the refold-equals-stored test for
every built-in view, which is the stage's exit evidence, is not here yet.
impl.md steps 8.1, 8.2, 8.6 and 8.7 are ticked as built; 8.3 to 8.5 are ticked
when Stage 8.2 lands, and `built-through` in the acceptance index is raised to
DH-S-8 then (it also needs DH-E-STORAGE-1 and -2, which Stage 8.2 authors).

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
| 8.7 | `internal/storage/attachments` | Files a person attaches: one `file` object plus a read-only named link `<home>/blobs/v1/names/<2 hex>/<sha256>/<name>` for file tools; size limit enforced while streaming (`attachment/too-large`); reads verified through the blob store. |
| shared | `internal/storage/home` | `EnsurePrivateDir` (syncs the parent of the directory it creates or adopts before success, so a retry after a failed sync, or a writer adopting another's new directory, never publishes below an entry that is not durable; `Ensure` syncs the home's parent and the home), `CreatePrivateFile` (Windows: `CreateFile` with a protected DACL in its security attributes; POSIX: `O_EXCL` `0600`), `SafeSegment`. `internal/util/fsx.SyncDir`. |

### Tests (TC-08)

Run with `go -C src/harness test -count=1 -v ./internal/storage/...`:

| Scenario (implementation-plan Stage 8.3) | Test | Result |
| --- | --- | --- |
| Large result spilled and recoverable | `spill/local` `TestLargeResultSpilledAndRecoverable` | pass. A 5,242,880-byte result through `spill.Save` with a 16,384-byte bound, logged as a `tool/result` v2 event in a SQLite home: the row is 16,616 bytes (head 16,383 bytes, cut before a two-byte rune, plus the `spill` field); reading the reference returns all 5,242,880 bytes byte-identical; its owner directory equals `sqlite.SpillName`. |
| Corrupted attachment detected | `blob` `TestCorruptedObjectDetected`; `attachments` `TestCorruptedAttachmentDetected` | pass: one byte flipped on disk, the read fails with `attachment/corrupt` "digest mismatch". |
| Spill files are private | `spill/local` `TestSpillFilesArePrivate` (`private_windows_test.go`, `private_unix_test.go`) | pass on Windows: the file, its owner directory and `spill/` have `SE_DACL_PROTECTED` and every ACE, read with `GetNamedSecurityInfo`, is for the owner SID, `SYSTEM` or `Administrators`. The Unix test (umask 0, file `0600`, directories `0700`) is vetted for linux and darwin here and runs on the CI's Linux job. |

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

Statement coverage of the new packages (merged across the storage tests): `spill`
88.1%, `spill/local` 91.1%, `blob` 87.3%, `attachments` 89.5%.

### Not built here (later steps of the storage area)

- The spill policy and `spill_read` tool (DH-D-STORAGE-6): the shaping listener
  that prices a result, builds the notice and calls `spill.Save`. Stage 8.3 builds
  the store it uses.
- Image admission, request images and crops (DH-D-STORAGE-4); the draft record
  and quarantine (DH-D-STORAGE-10).
- The blob collector and its `gc.lock`, and `ReferencedBy` over `blob_refs`
  (DH-D-STORAGE-9, DH-D-STORE-2). `Touch` restarts an object's grace period for it.
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
| Refold-equals-stored test for every built-in view | Stage 8.2 (impl.md step 8.3) | **pending**: Stage 8.2 has not landed on this branch |
| Manual checks | "Manual checks" above | met |
| Scenarios authored | DH-E-STORAGE-3 (8.3) in the acceptance index; DH-E-STORAGE-1, -2 by Stage 8.2 | partly met |
| Spill addressing (implementation-plan Stage 8.3 says "addressed by digest") | storage.md DH-D-STORAGE-5 prescribes owner hash + random id | **open**: conflicting documents, waiting for the operator |

impl.md steps 8.1, 8.2, 8.6 and 8.7 are ticked as built. The stage closes, and
`built-through` is raised to DH-S-8, when Stage 8.2 lands with steps 8.3 to 8.5,
its refold-equals-stored test and DH-E-STORAGE-1 and -2.
