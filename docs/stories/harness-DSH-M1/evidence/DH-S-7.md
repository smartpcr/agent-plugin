# DH-S-7 evidence

DH-S-7 is the SQLite store, leases and recovery. This file holds the evidence of its
stages as they are built. **Closure status: closed by Stage 7.3** (see "Closure"
at the end). Stage 7.1 (the database), Stage 7.2 (leases and crash recovery) and
Stage 7.3 (export and import, follow and duties, deletion and repair) are recorded
below. impl.md steps 7.1 to 7.6 are ticked as built. **Until step 16.8 passes, the
SQLite store is used on scratch homes only.**

## Stage 7.1 -- SQLite Driver Measurement and Atomic Appends

### Fact F-2: the pinned driver, measured first

The driver was pinned and measured before anything else was built on it. The pin is
in `src/harness/go.mod`, with a reason comment on each line:

| Module | Version | Why |
| --- | --- | --- |
| `modernc.org/sqlite` | v1.62.0 | DH-ADR-SQLITE-1: pure Go, so no cgo and no system SQLite (NG-4). One importer, `internal/session/store/sqlite`, enforced by an archtest rule. BSD-3-Clause (RM-10). |
| `modernc.org/libc` | v1.80.1 | The libc translation that driver release was generated against. The two are pinned together and move only together, after a new F-2 run. |

`go mod tidy` added these indirect requirements of the driver:
`github.com/dustin/go-humanize` v1.1.0, `github.com/mattn/go-isatty` v0.0.24,
`github.com/ncruces/go-strftime` v1.1.0, `github.com/remyoudompheng/bigfft`,
`modernc.org/mathutil` v1.7.1 and `modernc.org/memory` v1.12.1.

**Measurement.** Run with
`go -C src/harness test -count=1 -run '^TestBudgetStoreAppend$' -v ./internal/session/store/sqlite`
(the test is `src/harness/internal/session/store/sqlite/budget_test.go`). The test:

- opens 20 new `dsh.db` files, each in a test-kit temp home; each open includes the
  migrations;
- reopens each of those files once;
- on one session, makes 1,000 single-event appends, each followed by `Writer.Flush`.
  The append and the flush are also timed separately.

Machine: Windows 11 Enterprise, 13th Gen Intel Core i9-13900K, Samsung SSD 990 PRO
(NVMe), go1.27.4 windows/amd64, the module's floor is `go 1.26.0`.

Current run (every commit at `synchronous=FULL`, see "Durability" below):

| Measurement | n | p50 | p95 | p99 | max |
| --- | --- | --- | --- | --- | --- |
| open, new file (creates it and runs the migrations) | 20 | 7.94 ms | 19.71 ms | 19.71 ms | 38.83 ms |
| open, existing file | 20 | 3.70 ms | 7.14 ms | 7.14 ms | 18.25 ms |
| append (one event, WAL commit synced, `synchronous=FULL`) | 1000 | 0.69 ms | 2.34 ms | 4.95 ms | 15.18 ms |
| flush (waits for any commit in flight; no extra sync) | 1000 | <0.5 ms | 0.52 ms | 0.78 ms | 4.45 ms |
| append + flush | 1000 | 0.83 ms | 2.50 ms | 5.22 ms | 15.18 ms |

The first measurement, before the durability change, committed appends at
`synchronous=NORMAL` and synced in a separate barrier commit: append p95 0.66 ms,
flush p95 2.58 ms, append + flush p95 3.15 ms. Syncing each commit moves the fsync
from the flush into the append and costs nothing end to end. On Windows the Go
monotonic clock ticks about every 0.5 ms, so a call that finishes inside one tick
reads as 0; such a p50 is given as "<0.5 ms".

**Budgets.** These are compiled into the test: `DSH_*` and `GO*` cannot relax them,
and the budget gate clears both anyway. They were set from the run above, with
headroom for slower disks and the fsync cost of CI runners:

| Budget | Value | Measured p95 | Headroom |
| --- | --- | --- | --- |
| open, new file | 250 ms | 19.71 ms | ~12x |
| append | 10 ms | 2.34 ms | ~4x |
| append + flush | 50 ms | 2.50 ms | ~20x |

The figures are printed with the prefix `F-2` for this record. The budgets are
asserted in uninstrumented builds, which is how the budget gate runs `TestBudget*`:
alone and without `-race` (QG-7). The test gate also runs the test under `-race`,
together with every other package, to catch data races. There the full workload runs
and the figures are logged, but the budgets are not asserted. One such run under load
measured an open p95 of 347 ms and an append p95 of 13 ms, which reflects the
instrumentation and the load, not the driver.

### What was built (DH-D-STORE-2)

The package is `src/harness/internal/session/store/sqlite`.

- **One database per home.** It is `<home>/dsh.db`, opened by
  `OpenHome(ctx, home.Layout)` or `Open(ctx, path)`. A missing file is first created
  owner-only (0600).
- **Open sequence.** Before anything writes, the open checks the file's identity and
  layout, and refuses with the file left byte-identical in these cases:
  - `application_id` is not `0x44534831` ("DSH1"), or the id is 0 but the schema is
    not empty: `store/foreign-database`;
  - the file is not SQLite at all: `store/foreign-database`;
  - `user_version` is newer than this build's migrations: `store/newer-layout`.
  
  Then the open enters WAL. If the journal mode does not become `wal`, it refuses
  with `store/no-wal`. Last, it applies the numbered migrations, one per write
  transaction, and re-checks the version inside the transaction.
- **Schema (architecture.md section 4.3).** Migration 1 creates these STRICT tables:
  `sessions`, `events`, `checkpoints`, `hosts`, `duties`, `requests`.
  - `events` is `WITHOUT ROWID`, keyed by `(session, seq)`.
  - Triggers abort any `UPDATE` of an event, and any `DELETE` of an event unless its
    session is being deleted.
  - Sessions are keyed by `uid`, an `AUTOINCREMENT` that is never reused. A handle on
    a deleted session therefore never reads a later session that takes the same id.
- **Atomic multi-event appends.**
  - Each `Append` is one `BEGIN IMMEDIATE` transaction on the instance's single write
    connection. It holds every record, every checkpoint upsert and the
    `sessions.next_seq` update, which is guarded by holder, epoch and `next_seq`.
  - Any refusal, by the contract's checks or by a constraint of the database, rolls
    the whole transaction back.
  - Every driver error is translated once into a harness code (`store/busy`,
    `store/constraint`, `store/corrupt`, ...). Only the message is carried over, never
    the driver's type (DH-LIB-SQLITE-R7).
- **Durability and the flush barrier.** session-store.md defines an event as durable
  once its transaction committed with `synchronous = FULL`, and requires that another
  process sees only durable events.
  - The write connection runs `synchronous=FULL`, and `Open` refuses a connection that
    does not report it. Every commit syncs the WAL before it returns, so every row
    another process can read is durable.
  - `Summary.Durable` is the committed `next_seq` in every instance. There is no
    per-process bookkeeping that another instance could miss.
  - `Flush` takes the write mutex, so it returns only after any commit in flight on
    this instance, which was already synced.
- **Lifecycle.**
  - Acquiring a lease (`Create`, `OpenWrite`) runs under the write mutex: it checks
    that the store is not closing, commits the lease and registers the writer before
    releasing the mutex. `Store.Close` takes its writer snapshot under the same mutex,
    so it releases every lease acquired before it started. An acquisition that starts
    after it is refused with `session/closed` and changes nothing.
  - `Writer.Close` marks the writer closed and unregisters it only after the lease
    release commits. A failed release (for example `store/busy`) leaves the writer
    open and registered, so it can be retried.
  - `Store.Close` closes nothing when a release fails. It returns the error, and a
    later call retries.
  - Concurrent `Store.Close` calls run one at a time, from the first check to the last pool closed. A call that waited returns `nil` if the one before it succeeded; if that one failed, the waiting call retries the release itself. The write connection and pool are closed and cleared under the write mutex.
- **Reads.** Every read runs in one read transaction on a bounded read pool
  (`query_only`), so each read sees one snapshot.
  - Reads are paged with `Read(from, limit)` and `ReadSeqs`.
  - The cold part is read only with `WithAux`.
  - A seq past `MaxInt64` is answered in Go and never bound.
- **Listing.** Sessions are ordered by `last_event_at DESC, created_at DESC, id` with
  a keyset cursor, and can be filtered by cwd and by creator.

### Test results

The command is `go -C src/harness test -race -shuffle=on -count=2 ./internal/session/store/... ./internal/archtest/...`.
It passes.

| Scenario / ID | Test | Result |
| --- | --- | --- |
| Conformance suite on SQLite (`DH-T-STORE-1`, TC-07) | `TestConformance`: `storetest.Run` with a factory opening `dsh.db` in a temp home (or under `DSH_E2E_STORE_HOME`). All 21 cases that pass on memstore pass. | pass |
| Multi-event append is atomic (TC-07) | `TestMultiEventAppendIsAtomic`: the batch runs through the real `Writer.Append`, and a trigger the test installs makes the database refuse it. In one case the third event is refused; in the other the checkpoint is refused after all three events were inserted. Each time, a fresh `sql.Open` reads no new event, `next_seq` unchanged and the old checkpoint. With the trigger dropped, the same batch commits whole. | pass |
| Flush is durable (TC-07) | `TestFlushSurvivesKill`: a re-executed child appends and flushes, then is killed before closing anything. Reopening the store shows all five rows, all durable. | pass |
| Another process sees only durable events | `TestOtherProcessSeesOnlyDurable`: a child process appends five events without flushing and reports its write connection at `synchronous=2` (FULL). Another instance in the parent checks that what it reads equals `Durable`, which equals `NextSeq` (5). It checks before the child's flush, after it, and after the child is killed. | pass |
| Failed close is retryable | `TestCloseRetriesAFailedRelease`: an outside connection holds the write lock, and `Writer.Close` and `Store.Close` both return `store/busy`. The leases stay held and new writers are refused. Once the lock is released, both calls succeed and `IfFree` opens in another instance succeed. | pass |
| No lease orphaned by shutdown | `TestCloseRacingAcquisitions`: 20 rounds, each running 8 `OpenWrite` and 8 `Create` calls concurrently with `Store.Close`. Every acquisition either returns a writer or `session/closed`, and no round leaves a lease held. | pass |
| Concurrent closes are serialized | `TestConcurrentClose`: 8 `Store.Close` calls run at once twice. The first time an outside write lock is held, and every call returns `store/busy` while both leases stay held. The second time the lock is released: every call returns `nil` only after the pools are closed, and no lease or host row remains. It runs clean under `-race`, with no `DATA RACE` report. | pass |
| Driver budget (`DH-T-STORE-18`, F-2) | `TestBudgetStoreAppend` | pass, figures above |
| Log immutable in the schema | `TestEventsImmutableInSchema` | pass |
| Open refusals leave the file unchanged | `TestOpenRefusesUnchanged`: newer layout, foreign id, unrelated schema, not SQLite | pass |
| Reopen keeps records, checkpoints, epoch; WAL; layout version | `TestReopen` | pass |
| Lease shared across instances on one home | `TestTwoInstancesShareTheLease` | pass |
| Error translation (DH-LIB-SQLITE-R7) | `TestTranslate` | pass |
| One importer of the driver | archtest rule "only internal/session/store/sqlite imports the SQLite driver" | pass |

Package statement coverage is 89.6%; the gate's floor is 85%.

`DH-T-STORE-2` to `-4` and the rest of `DH-T-STORE-18` belong to later stages of this
phase, so they are not part of 7.1:

- the crash-kill loop of `DH-T-STORE-5`;
- dead-holder takeover with the liveness lock;
- the open of a session of more than 100,000 events;
- the heavy-tailed listing corpus.

## Stage 7.2 -- Session Leases and Crash Recovery

Built in `lease.go`, `open.go` and `handles.go` of the same package, with the
checkpoint helper in `internal/session/checkpoint`. This section records its
crash-injection results, which the exit evidence of impl.md DH-S-7 names.

| Scenario / ID | Test | Result |
| --- | --- | --- |
| Second writer refused across processes (`DH-T-STORE-6`) | `TestSecondWriterRefusedAcrossProcesses`: while process A holds S, a second writer is refused, from another process and from A's, with A's identity, and S's log holds only A's events. | pass |
| Dead holder taken over (`DH-T-STORE-6`, `-7`) | `TestDeadHolderTakenOver`: after A is killed, B takes its liveness lock inside the write transaction, the lease at epoch+1, and removes A's lock file and hosts row; an append with A's epoch writes nothing. `TestRaceForDeadLeaseHasOneWinner`: one winner. `TestForcedTakeoverAcrossProcesses`: the displaced holder's next append is `session/lease-lost`. | pass |
| Liveness lock (`DH-T-STORE-7`) | `TestLivenessLockSurvivesOSOpenAndClose`, `TestCloseReleasesTheLivenessLock` | pass |
| Crash between append and flush (`DH-T-STORE-8`, `-9`, crash injection) | `TestCrashBetweenAppendAndFlushRecoversHonestly`: a child exits between the tool call's append and any flush. Reopening takes its lease and appends the closers (an outcome-unknown result, step/end, turn/end interrupted), durable before the session is published. | pass |
| Crash-kill loop (`DH-T-STORE-5`, crash injection) | `TestKilledAppenderLeavesContiguousLog`: a child appending and flushing is killed at a random point five times on one session; every reopen finds a contiguous log holding at least the last event reported flushed. | pass |
| Bounded shutdown (DH-ADR-STORE-3) | `TestCloseIsBoundedWhileAWriteWaitsOnAnotherConnection` | pass |

The guarded scenarios DH-E-STORE-1 and DH-E-STORE-2 were authored in that stage
(`store_two_process_scenario_test.go`, `store_crash_recovery_scenario_test.go`) and
run in step 16.8.

## Stage 7.3 -- Export Import Follow and Store Repair

### What was built

- **Export and import (DH-D-STORE-7, `export.go`).** `Store.Export(ctx, w, id,
  ExportOptions{Gzip})` writes one readable JSON-lines file:
  - a header line `{"dsh_export":1,"session":{...},"inherited":n}`;
  - one line per event (seq, time, type, payload version, flags, the stored payload
    and cold part as JSON strings, byte for byte);
  - a trailer `{"dsh_export_end":{"events":n,"sha256":...}}` whose digest covers every
    byte before it.
  `Store.Import(ctx, r, ImportOptions{NewID, Limit})` reads the whole file (at most
  `Limit`, default 1 GiB, counted after decompression, so a gzip bomb is stopped)
  and validates it before it writes anything: the format number, the digest, the
  count, contiguous seqs, nothing after the trailer, and the same admission as
  `OpenWrite`. Then one transaction inserts the session unheld, with `imported_from`
  naming the source id. An id already present, or still being deleted, is refused
  with `session/exists`; `NewID` imports under another id.
- **Follow and duties (DH-D-STORE-8, `follow.go`).** `Store.Follow(ctx, id, from)`
  returns a `Subscription` that polls `PRAGMA data_version` on a connection of its
  own, outside the bounded read pool (so any number of followers never starve
  `Stat`, `List`, `OpenRead` or `Export`), every follow interval (`WithFollowInterval`, default 250 ms), and reads
  only when another connection committed. Each read is a short read transaction, so
  a follower never holds the WAL back. The subscription ends with `session/deleted`
  when the session is deleted, with the context's error when it is cancelled, and
  with nil on `Close` or `Store.Close`. Once-per-home duties use the `duties` table:
  `AcquireDuty` takes a free or dead holder's duty with the epoch raised;
  `RunDuty(ctx, name, every, fn)` runs `fn` only on the holder and only when the last
  run is older than `every`, and records the run under an epoch guard. Calls of one
  duty within one instance are serialized from the due check to the recorded run,
  so two concurrent calls run `fn` once.
  `Store.Close` releases the instance's duties.
- **Deletion, retention, check and rebuild (DH-D-STORE-6, `maintain.go`).**
  - `Delete` first tombstones the session and its transitive subagent children in
    one transaction (state `deleting`, epoch raised). From that commit they are
    invisible to `List`, `Stat`, `OpenRead`, `OpenWrite` and `Follow`, and their id
    cannot be reused. It then removes the spill directory
    (`<home>/spill/<SpillName(id)>`), deletes the events in chunks
    (`WithDeleteChunk`, default 1,000), and finally deletes the checkpoints, requests
    and the row. A fork is not deleted with its parent.
  - Every step can be repeated. `Open` runs `FinishDeletions`, which takes over every
    tombstone whose holder is gone, so an interrupted deletion finishes on the next
    open. A deletion `Open` cannot finish (a spill directory it cannot remove, a
    failing database) fails `Open` with that error; the instance is released and
    the tombstone stays for the next `Open`. When its hosts row cannot be removed
    either, its liveness file stays unlocked, so the next `Open` finds it dead.
  - `Retire(ctx, maxAge)` deletes the unheld sessions whose last event is older than
    `maxAge`. Its first read is only a shortlist: inside each tombstoning
    transaction the session must still be the same one (its uid) and still be
    old, and must not be held. `RunRetention` runs it as the once-per-home
    `retention` duty.
  - `Check` (`dsh store check`) is read-only and runs in one snapshot: SQLite's
    integrity check; for each session, its row count and last seq against
    `next_seq`; checkpoints past a session's end; rows of no session. Tombstones are
    listed, not counted as damage.
  - `Rebuild` refolds checkpoints from the log through an injected `Refold`. With
    `Verify` it only reports the differences. Without it, it replaces the stored
    checkpoints in a write transaction guarded by an unchanged `next_seq`.

### Test results

The command is `go -C src/harness test -race -shuffle=on -count=1 ./internal/session/... ./internal/archtest/...`.
It passes.

| Scenario / ID | Test | Result |
| --- | --- | --- |
| Export then import round-trips (`DH-T-STORE-13`, TC-07, golden) | `TestExportImportRoundTrip`: a fixed 50-event fixture in home H1 is exported, and the file equals the committed `testdata/export.golden`. Imported into an empty H2, H2's event rows equal H1's (type, version, payload, seq), and exporting again from H2 gives the same bytes, plain and gzip. | pass |
| Import refuses an id already present (`DH-T-STORE-13`) | `TestImportRefusals`: an empty file, a file truncated mid-line, a missing trailer, an altered payload byte, a dropped event, bytes after the trailer, another format, not JSON, and an event the registry refuses are each refused with nothing written; an id the home holds is refused with the home unchanged. | pass |
| Import bounded | `TestImportStopsAGzipBomb` (`store/export-too-large`), `TestExportMissing` | pass |
| Follower sees another process's commits (`DH-T-STORE-14`, TC-07) | `TestFollowerSeesAnotherProcessesCommits`: a re-executed child appends 10 events, 20 ms apart; the parent follows at a 50 ms interval and receives all 10 in order, each within the 2 s bound of its commit; afterwards a `wal_checkpoint(TRUNCATE)` succeeds, so the follower held no reader. | pass |
| Follow in one process | `TestFollowInProcess`: a follower sees its own process's commits, a subscription from a later seq starts there, deleting the session ends it with `session/deleted`, and `Store.Close` ends the rest. | pass |
| One runner per duty (`DH-T-STORE-14`) | `TestDutyHasOneHolderAndMovesAfterAKill`: a duty has one holder across processes, is not run again within its period, and moves to another instance once its holder is killed; `Close` releases an instance's duties. | pass |
| One run per duty within one instance (`DH-T-STORE-14`) | `TestConcurrentRunDutyRunsOnce`: two concurrent `RunDuty` calls with a one-hour period; the second never enters `fn` while the first runs, and exactly one reports a run. | pass |
| Followers leave the read pool free | `TestFollowersLeaveTheReadPoolFree`: with 8 live followers (the read pool has 4 connections), `Stat`, `List`, `OpenRead`/`Read`, `Export`, `Check` and a ninth `Follow` answer within a 5 s context, and all 9 followers deliver the next commit. | pass |
| Interrupted deletion finishes (`DH-T-STORE-11`) | `TestInterruptedDeletionFinishesOnOpen`: a re-executed child deletes a 1,000-event session with a subagent child and is killed after 500 events. The session is invisible at once and its spill directory is gone; reopening the store finishes the deletion; no row or spill file of either session remains, the fork survives, and `Check` is clean. | pass |
| Deletion refused while held (`DH-T-STORE-11`) | `TestDeleteRefusedWhileHeld`: a session held by another process, or a subagent child of it, is refused and nothing changes; this instance's own writer blocks it too until closed; deletion then runs in chunks and removes checkpoints and requests with the row. | pass |
| Retention (`DH-T-STORE-11`) | `TestRetentionRetiresOnlyUnheldOldSessions`: off by default (age 0 deletes nothing), finishes tombstones, deletes only unheld sessions past the age, and runs as one duty per home. | pass |
| Retention revalidates its shortlist (`DH-T-STORE-11`) | `TestRetireRevalidatesItsShortlist`: between the shortlist and the deletions one session gains a recent event and another id is deleted and created again; both survive, and only the untouched session is deleted. | pass |
| Open fails on an unfinishable deletion (`DH-T-STORE-11`) | `TestOpenFailsWhenADeletionCannotFinish`: with spill removal failing, `Open` over a tombstone fails with `store/io` naming the session, leaves no hosts row and keeps the tombstone. Again with another connection taking the write lock during the failed `Open`'s cleanup: its hosts row cannot be removed, so it keeps its liveness file unlocked; once the lock is released the next `Open` finds it dead, takes the tombstone over and finishes the deletion, and `Check` is clean (without the kept file the deletion stays stranded). | pass |
| Check reports damage (`DH-T-STORE-12`) | `TestCheckReportsDamage`: a session whose row count was damaged on purpose, a checkpoint past the end, and rows of no session are reported. | pass |
| Rebuild and verify (`DH-T-STORE-12`) | `TestRebuildAndVerify`: rebuilt checkpoints equal the incrementally built ones; verify reports a row damaged on purpose and changes nothing; rebuild repairs it. | pass |
| Failures change nothing | `TestMaintenanceFailsCleanly`: with a cancelled context and on a closed store, each maintenance call fails and leaves the session, the import target and the duties unchanged; a failing refold stops a rebuild; a delete chunk of 1 still deletes every row. | pass |
| Conformance on SQLite (`DH-T-STORE-1`) | `TestConformance`, including the `Delete` cases, now served by the chunked deletion. | pass |

Package statement coverage is 85.8%; the gate's floor is 85%. The new test files
are clean under the project's analyzers (no `time.Sleep`, every goroutine released
with `t.Cleanup`).

### Scenarios authored

| ID | File | Runs in |
| --- | --- | --- |
| DH-E-STORE-6 (guarded) | `src/harness/internal/testkit/scenarios/store_deletion_scenario_test.go` | DH-S-16, step 16.8, scratch homes only |
| DH-E-STORE-5 | `src/harness/internal/testkit/scenarios/store_follow_request_scenario_test.go` | DH-S-35, step 35.7 |

Both are listed in `test/harness/acceptance-index.yaml`, and `harness-gate preflight`
finds their tests. Their inputs are shell command lines, because the launcher and
the admin tree (`dsh session delete`, `dsh store check`) do not exist yet. **Until
step 16.8 passes, the SQLite store is used on scratch homes only.**

### Not built here

- The contract has no `Deleting` notification, so `Delete` sends none.
- Search rows, view rows and blob references do not exist yet, so deletion has none
  to remove. DH-E-STORE-6's image collection is checked through its input command.
- Follow, duties, export, check and rebuild are methods of `*sqlite.Store`, not of
  the `store.Store` contract. The contract notes that Follow is later work.

## Manual checks of impl.md DH-S-7 (scratch home)

Run on the machine above. The store tests were pointed at a scratch home with
`DSH_E2E_STORE_HOME=%TEMP%\dsh-scratch-<n>`, so every database lived there and not in
a user home. The command was
`go -C src/harness test -count=1 -v -run '<names>' ./internal/session/store/sqlite/`.
The directory was empty afterwards and was deleted.

| Check | Test program | Recorded |
| --- | --- | --- |
| Kill a test program between an append and its flush; record what the reopened store holds | `TestCrashBetweenAppendAndFlushRecoversHonestly` | The reopened store holds the appended events and the closers: an outcome-unknown result, step/end and turn/end interrupted. pass |
| Open one database from two test programs; record the refused second writer | `TestSecondWriterRefusedAcrossProcesses` | The second program's writer is refused with the first's identity; the log holds only the first's events. pass |
| Export a session, import it into an empty home and compare | `TestExportImportRoundTrip` | The 52-line file (header, 50 events, trailer) equals the golden; the event rows of both homes are equal. pass |
| Import and delete on a scratch home only | `TestInterruptedDeletionFinishesOnOpen`, `TestImportRefusals` | pass |
| Also run there | `TestConformance`, `TestFollowerSeesAnotherProcessesCommits`, `TestDutyHasOneHolderAndMovesAfterAKill` | pass |

Driver measurement, rerun at closure (`TestBudgetStoreAppend`, same machine):

| Measurement | n | p50 | p95 | p99 | max |
| --- | --- | --- | --- | --- | --- |
| open, new file | 20 | 12.95 ms | 19.70 ms | 19.70 ms | 69.66 ms |
| open, existing file | 20 | 5.86 ms | 8.05 ms | 8.05 ms | 8.85 ms |
| append | 1000 | 1.03 ms | 1.80 ms | 3.26 ms | 25.74 ms |
| flush | 1000 | <0.5 ms | 0.52 ms | 0.55 ms | 1.05 ms |
| append + flush | 1000 | 1.05 ms | 1.90 ms | 3.26 ms | 25.74 ms |

All are within the budgets of Stage 7.1.

## Closure

| Exit evidence (impl.md DH-S-7) | Where | Status |
| --- | --- | --- |
| Conformance suite passing against SQLite | Stage 7.1 and 7.3 test tables (`TestConformance`) | met |
| The driver measurement (F-2) | Stage 7.1 "Fact F-2", rerun above | met |
| Crash-injection tests of DH-D-STORE-4 | Stage 7.2 table | met |
| Manual checks | "Manual checks" above, on a scratch home | met |
| Scenarios authored | DH-E-STORE-1, -2 (7.2), -5, -6 (7.3), in the acceptance index | met; they run in steps 16.8 and 35.7 |

impl.md steps 7.1 to 7.6 are ticked as built. `built-through` in the acceptance
index is raised to DH-S-7.