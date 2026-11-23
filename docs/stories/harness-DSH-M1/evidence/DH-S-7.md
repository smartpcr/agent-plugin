# DH-S-7 evidence

Decisions that govern M1 records, this one included, are logged in [decisions.md](../decisions.md).

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The "Closure status: closed by Stage 7.3" statement below and the "met" rows of
"Closure" are the stage's own closure, kept as history: under M1-D-CLOSE-1 every tick
made while the gate was red is provisional and is re-verified at that commit. Plan
Stage 15.1 built the missing store tests; they are recorded in
["Gap closure (M1-G-STORE)"](#gap-closure-m1-g-store), with both gate runs at `6e7514c`. The fields of impl.md sec. 7.3 and the
per-step manual-check register are at the end of this file, in "Evidence record
(impl.md sec. 7.3)"; how to fill them is in [the evidence runbook](README.md).

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

Historical (replaced by "Gap closure (M1-G-STORE)" below, plan step 15.1.6): this
paragraph said that `DH-T-STORE-2` to `-4` and the rest of `DH-T-STORE-18` belonged to
later stages. The crash-kill loop (`DH-T-STORE-5`) and dead-holder takeover were built in
Stage 7.2; `DH-T-STORE-2` to `-4` and the rest of `DH-T-STORE-18` were not built until
plan Phase 15.

## Gap closure (M1-G-STORE)

**What this section records.** The audit of the M1 story (gaps M1-G-STORE-1 to -9 and
-21, -30 in [gap-analysis.md](../gap-analysis.md)) found that DH-S-7 had been ticked
without several of the store tests its Done-when lines name. Plan
[Stage 15.1](../gap-closure-plan.md#stage-151-safe-layout-upgrade-and-store-proof) built
them, with step 15.3.1 (the bounded open) and step 15.3.5 (the POSIX tests on WSL). This
section maps each test ID to the tests that now prove it, with the commit, the commands,
the two gate runs and the budget figures. It is the evidence of "built". It is not
"done": the `DH-E-STORE` scenarios still run later (impl.md step 16.8, step 35.7).

**Words used here.**
- A **layout** is the database's table design, numbered by SQLite's `user_version`. A
  newer `dsh` upgrades an older layout before it uses the file.
- A **backup** is the copy of `dsh.db` taken (with `VACUUM INTO`) before an upgrade, kept
  in `<home>/backup/`. It is the way back if the upgrade fails or the user returns to the
  older binary. The upgrade never deletes it; `Store.Backups` and `Check` list it.
- A **checkpoint** (in the store) is a stored row of derived state with the `seq` it
  reflects. The open reads from the newest one instead of from the start of the log.
- A **provisional budget** is a time and memory limit set from the first measured run.
  The benchmark fails when it is missed, but the number is final only when pinned at
  impl.md step 16.7 (held open as H-34, below).

### Build identity and commands

- **Built in:** `f4a422d`, `8d8abfa`, `cb1c07a`, `2fd1758`, `3695cc2`, with the test-only
  fix `6e7514c` (the corpus Budget test renamed so the coverage gate runs it). The
  decisions are M1-D-STORE-A, -B and -F ([decisions.md](../decisions.md)).
- **Gate evidence commit:** `6e7514cdb6fbc8f105238e7e655fb20e3102166b`.
- **Commands:**
  - both lane scripts (`lanes/win.ps1`, `lanes/wsl.sh`), which run `harness-gate` with
    `--evidence` (12 gates; the `bench` tag is now part of the vet and staticcheck layer
    set `replay,scenario,bench`, so the bench files are compiled by the gate);
  - `go -C src/harness test -race ./internal/session/store/...`, per package: pass;
  - the bench runs (Windows; WSL pending):
    `go -C src/harness test -tags bench -run '^$' -bench BudgetStore -benchtime 20x -timeout 60m ./internal/session/store/sqlite/`,
    each also with `GOMEMLIMIT=4GiB`.

### Gate runs at 6e7514c

Both lanes ran the gate of record at one commit and passed all 12 gates.

| Field | WSL lane | Windows lane |
| --- | --- | --- |
| Run-record id | `wsl-6e7514c-20261123T0602Z` | `win-6e7514c-20261123T0602Z` |
| Record file and SHA-256 | [runs/wsl-6e7514c-20261123T0602Z.txt](runs/wsl-6e7514c-20261123T0602Z.txt), `e643fb9c05106103196578bbc483b4fa199c066baa3d3be5d7f3c5fcfbefa815` | [runs/win-6e7514c-20261123T0602Z.txt](runs/win-6e7514c-20261123T0602Z.txt), `69bd371846922b7e1f30eb65c11de10da067267112447a95f8594485fc71a6cf` |
| Output SHA-256 (`sha256:` line) | `f234657b2553526a9acb3c92fb53a6cad1a561ae5f5f7e5dd6c754b38cd55511` | `b67f2d17137df1f60c6a43a9712138e72d1f047325f99fdd9e3495567b9f5c67` |
| Rehearsal file and SHA-256 | [runs/wsl-6e7514c-20261123T0602Z-rehearse.txt](runs/wsl-6e7514c-20261123T0602Z-rehearse.txt), `e42fa7ab08dd64658cfaaa14f1d42a47626ebf923bf7fbe72203eaa3319b9b12`; six binaries, each `vcs.modified=false`, nothing published | -- (WSL lane only) |
| Operator | the lead (lane script) | the lead (lane script) |
| Commit | `6e7514cdb6fbc8f105238e7e655fb20e3102166b` | the same |
| git status --porcelain | empty (`tree: clean`) | empty (`tree: clean`) |
| System | Ubuntu 24.04.5 LTS (`Ubuntu-24.04`), kernel `6.18.58.1-microsoft-standard-WSL2`; `wsl.exe --version` not copied for this run | Microsoft Windows [Version 10.0.28000.3501] |
| Go | go1.27.4 linux/amd64 (`/usr/local/go`) | go1.27.4 windows/amd64 (`C:\Program Files\Go`) |
| CC and gcc | `gcc`, gcc (Ubuntu 13.3.0-6ubuntu2~24.04.1) 13.3.0 | `gcc`, gcc (Rev5, Built by MSYS2 project) 16.1.0 |
| CGO_ENABLED, TMPDIR, id -u | not copied; the lane preflight requires `TMPDIR` on ext4 and a non-root user, and the gate sets `CGO_ENABLED=1` for `test` | not copied, --, not elevated (lane preflight) |
| Checkout file system | ext4 (WSL-native clone) | NTFS |
| systemctl --user | not recorded for this run | -- |
| Started, ended (UTC) | 2026-11-23T06:02:58Z, 06:10:39Z | 2026-11-23T06:02:59Z, 06:36:02Z |
| Exit status | 0 | 0 |
| Result | `harness-gate: PASSED all 12 gates` | `harness-gate: PASSED all 12 gates` |
| Reasoned exceptions | 41 (typeassert 38, SA1019 1, SA2001 1, U1000 1) | the same |
| Gate times (selected) | test 2m47s, coverage 4m11s, self 28s | test 5m3s, coverage 17m45s, self 6m34s |
| CI run ids (supplementary) | none | none |
| macOS | out of scope (`platforms-windows-wsl`) | |

An earlier WSL run at `3695cc2`
([runs/wsl-3695cc2-20261123T0553Z.txt](runs/wsl-3695cc2-20261123T0553Z.txt)) failed at
`coverage`: `testkit/corpus` was at 65.4%, because the coverage gate skips tests named
`^TestBudget` and the package's only Budget test was `TestBudgetRunsBothRuns`. `6e7514c`
renames it. The Windows run at `3695cc2` was stopped by the lead after that result; no
record was kept. The `3695cc2` run is kept as history and is not closure evidence.

### Test IDs and results

Every test below passed in both gate runs at `6e7514c`.

| Test ID | Plan step | Tests (package `session/store/sqlite` unless named) | What they show | Result |
| --- | --- | --- | --- | --- |
| `DH-T-STORE-2` (upgrade) | 15.1.1, 15.1.2 | `TestUpgradeRefusedWhileOlderInstanceLives`, `TestUpgradeProbesAgainInsideItsTransaction`, `TestFailedUpgradeRollsBackAndKeepsTheBackup`, `TestFailedBackupChangesNothing` (`migrate_test.go`) | A re-executed child holding the home at layout N-1 blocks the upgrade: `store/migration-blocked` names its pid, kind and start time, `user_version` stays N-1 and no backup is made; after the child is killed, the upgrade runs and makes one backup. The liveness probe runs again inside the upgrade transaction. A failed upgrade from N-1 and from N-2 rolls back: `store/migration-failed` names the kept backup, the database reads at its old layout with every row, and the backup passes `integrity_check` and is owner-only. A failed backup (a full disk, a failed check) leaves `dsh.db` byte-identical. Layout 7 adds `hosts.binary_version` (it rebuilds the `hosts` table instead of `ALTER`, because the tests lower the layout by hand). A new database (layout 0) gets no backup and no probe. | pass |
| `DH-T-STORE-2` (open sequence) | 15.1.3 | `TestOpenRefusesWithoutWAL`, `TestStoreFilesAreOwnerOnly` | A journal mode that is not WAL is refused with `store/no-wal`. `dsh.db` and `run/` are owner-only: POSIX `0600`/`0700` on WSL ext4, the DACL checked by `home.CheckPrivate` on Windows. | pass; on WSL PASS, not SKIP (15.3.5) |
| `DH-T-STORE-3` | 15.1.4, 15.2.1 | `TestMultiEventAppendIsAtomic`, `TestAppendFaultAtEachStep`, `TestForkEqualsParentPrefixRowForRow`, `TestEventsImmutableInSchema`; `storetest` cases `DerivedRowRefused`, `UpsertDerived`, `CheckpointsOnlyMoveForward` on SQLite and `memstore` | A fault at each of 10 points of the append commits nothing. The checkpoint case is inverted (15.2.1): when the database refuses a derived row, the three events still commit, the row keeps its older seq, `DerivedDropped` is 1 and one `Degraded` notice is sent. A fork equals its parent's prefix row for row. The schema's triggers keep events immutable. | pass |
| `DH-T-STORE-4` | 15.1.4 | `TestFailedCommitKeepsTheBatch`, `TestFailedRollbackMakesTheStoreUnusable`, `TestOtherProcessSeesOnlyDurable`, `TestFlushSurvivesKill` | A failed commit keeps the batch and the next `Flush` succeeds. After a failed rollback, every later write and read call returns `store/unusable` (checked twice), with one store-wide `Degraded` "unusable" notice. The backlog clause, per M1-D-STORE-A (a): an event is durable once `Append` returns. Known limit: `database/sql` ends the transaction on a failed commit, so a failed commit is not followed by a rollback; "commit and rollback both fail" is tested as a refused statement followed by a failing rollback. | pass |
| `DH-T-STORE-7` | 15.3.5 | `TestLivenessLockSurvivesOSOpenAndClose` (`lease_test.go`) | The liveness lock survives an unrelated open and close of its file. The test carries no `//dsh:test` directive. | pass; on WSL PASS, not SKIP |
| `DH-T-STORE-8` | 15.1.7 | `agent/driver` `TestMountedCheckpointGovernsEveryPoint`, `TestFailedStepEndCheckpointEndsTheTurn`, `TestFailedAttemptCheckpointSkipsTheAttempt` | A recording policy mounted on the driver's context sees attempt, tool-body, step-end, attempt, step-end, in that order. The tool body goes through `checkpoint.Before` (`tools/core/pipeline.go:350`) with the registry's policy, so one policy governs both points only when it is mounted where both see it. A failed step-end checkpoint ends the turn; a failed attempt checkpoint skips the attempt. Side effect: one more flush per step under the default policy. | pass |
| `DH-T-STORE-10` (bounded open) | 15.3.1 | `TestReadsJudgeWhatTheOpenDidNot` (`openbound_test.go`) | Under M1-D-STORE-F, a record below the newest checkpoint that the open did not judge is refused, with its typed error, by the first `Read` or `ReadSeqs` that reaches it, and is never returned. | pass |
| `DH-T-STORE-18` | 15.1.5, 15.3.1 | `BenchmarkBudgetStore` (`budget_bench_test.go`, `-tags bench`); `TestOpenIsBoundedByTheNewestCheckpoint` (`openbound_test.go`) | The open from checkpoints, the fork and the first list page over the corpus, each also under `GOMEMLIMIT`, within provisional budgets (figures below). An open judges 2 records at 100, 1,000 and 10,000 events. | pass (Windows bench; WSL bench pending); budgets provisional (H-34) |

The heavy-tailed corpus is `internal/testkit/corpus` (the 22 anchors of UP-DEC
2026-09-28), with a 100,000-event seeder writing through `sqlite.Store.Seed` with
checkpoints (15.1.5). The driver's continuation benchmark (`DH-T-AGENT-28`, DH-S-11) now
takes its ladder from the corpus: p50 rung 1.6 ms, p90 6.2 ms, p99 (381 turns) 27.7 ms,
and the largest rung, capped at 1,024 turns, 71 ms.

### Budget figures (F-2, `DH-T-STORE-18`)

Windows: i9-13900K, go1.27.4, `-benchtime 20x`, after the bounded open of 15.3.1. Each
p95 is the range over the plain run and the `GOMEMLIMIT=4GiB` run. The budgets are
about 3 times the worst p95 (M1-D-STORE-B (a)) and are provisional (H-34). The 4 GiB
limit never bound: the heap after an operation stayed at or below 250 MB.

| Case | Windows p95 | WSL p95 | Budget |
| --- | --- | --- | --- |
| store open, 100,000 events, from checkpoints | 1.25-1.28 s (1.91 GB/op) | 1.25-1.28 s (1.91 GB/op) | 4 s |
| store fork of that session | 1.31-1.49 s | 0.89-0.90 s (721 MB/op) | 4.5 s |
| store first list page over the corpus | 1.0-1.5 ms (the Windows clock ticks about every 0.5 ms) | 0.24-0.30 ms (75 KB/op) | 5 ms |

The WSL column is from `runs/wsl-6e7514c-bench.txt` (sha256 `2b9f3065905c822aeb921ce346713039447eb13efd133bff250bb1d6ebfe4fe6`): WSL Ubuntu-24.04 at `6e7514c`, go1.27.4, `-tags bench -benchtime 20x`, every case PASS within its budget.

Before the bounded open (the first measured run, Windows): store open 2.95-3.28 s and
2.08 GB; fork 1.43-1.55 s. The query and listing figures are in
[DH-S-8.md, "Stage 8.2 -- Builtin Views, Projects and Session Listing"](DH-S-8.md#stage-82----builtin-views-projects-and-session-listing).

### Manual check: upgrade a scratch home made by the previous binary

`manual`, M1-D-CLOSE-2 option A (a scripted probe, watched): this is the 15.1 exit check.
It does not fill the impl.md register below, which plan step 18.1.5 runs at the frozen
closure commit, and it ticks no scenario box.

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 7.1 (plan 15.1 exit) | upgrade a scratch home made by the previous commit's binary; record the backup and the layout before and after | probe: a program built from `git archive 1109c62` (the Phase 14 closing commit, layout 6) creates a scratch home with session `s` (3 tool-call events); a program built from the current tree with `go build -overlay` (nothing added to the tree) opens it; then a second open | Windows | the lead (Claude), scripted probe | `3695cc271e735943cd943b743b332f58b2d34de7` (store code identical to `6e7514c`) | Before: layout 6. After: "opened at layout 7"; session `s` next seq 3, durable 3, 3 records read. Backup `backup/dsh-L6-1795413214887159700.db` (131,072 bytes) kept and listed by `Backups()` and by `Check`. The second open did no further upgrade (still 1 backup). The binary version read `(devel)+3695cc2...dirty` only because an untracked run record was in the tree. | `manual` |

### What stays open

- The bench figures on WSL (filled by the lead after the WSL run).
- H-34: the `DH-T-STORE-18` budgets are provisional until impl.md step 16.7.
- The guarded scenarios DH-E-STORE-1, -2 and -6 are authored and not run (impl.md step
  16.8); DH-E-STORE-5 runs at step 35.7. Until step 16.8 passes, the SQLite store is
  used on scratch homes only.
- `TestLivenessLockSurvivesOSOpenAndClose` has no `//dsh:test` directive.

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
| Conformance suite passing against SQLite | Stage 7.1 and 7.3 test tables (`TestConformance`) | met; re-verified in both gate runs at `6e7514c` ("Gap closure (M1-G-STORE)") |
| The driver measurement (F-2) | Stage 7.1 "Fact F-2", rerun above; the `DH-T-STORE-18` budget figures in "Gap closure (M1-G-STORE)" | met for the append measurement (Windows). The `DH-T-STORE-18` budgets are met on Windows at `6e7514c`; the WSL figures are pending (filled by the lead); the budgets are provisional (H-34) |
| Crash-injection tests of DH-D-STORE-4 | Stage 7.2 table; `DH-T-STORE-8` in "Gap closure (M1-G-STORE)" | met; both gate runs at `6e7514c` |
| The store tests the Done-when lines name (`DH-T-STORE-2`, `-3`, `-4`, `-8`, `-10`, `-18`; M1-G-STORE) | "Gap closure (M1-G-STORE)" | met at `6e7514c`, both lanes (historical: missing until plan Phase 15, though the stage was marked closed) |
| Manual checks | "Manual checks" above, on a scratch home (historical); the 15.1 upgrade check in "Gap closure (M1-G-STORE)" (`manual`, `3695cc2`) | recorded as `manual`; the per-step register below stays open until plan step 18.1.5 |
| Scenarios authored | DH-E-STORE-1, -2 (7.2), -5, -6 (7.3), in the acceptance index | authored, not run; they run in steps 16.8 and 35.7, so no DH-S-7 deliverable is "done" yet |

impl.md steps 7.1 to 7.6 are ticked as built. `built-through` in the acceptance
index was raised to DH-S-7 by this stage, and to DH-S-8 by plan step 15.3.7. Plan step
15.3.8 confirmed the step 7.1 tick against Stage 15.1 at `6e7514c`, with H-34 open; the
story plan's Stages 7.1 and 7.2 are ticked, citing this file. The gate of record for
these ticks is the pair of run records `wsl-6e7514c-20261123T0602Z` and
`win-6e7514c-20261123T0602Z`, in place of a CI run id.

M1 gate repair (plan 13.2.2, 13.2.3; [DH-S-1.md, "M1 gate repair"](DH-S-1.md#m1-gate-repair)): `internal/session/store/sqlite/shutdown_test.go` waits on two signals from `export_test.go` instead of polling and sleeping -- `WriteHeldSignal` (a test seam on the writer mutex, `ctxMutex.held`, nil outside tests) and `BusyWaitSignal` (a call context that notices the driver's `busyHandler` asking it for `Done`), so `Close` is called while the append is really in the busy wait -- and its cleanup rolls back the other connection, then joins the append (P-C); `internal/session/query/visibility_test.go` `TestListPagesByLastPromptAsOf` runs on the SQLite store in a `synctest` bubble (virtual prompt milliseconds, an `appended` channel instead of the counter poll, P-C for the writer). Assertions unchanged.

M1 gate triage (plan 13.7.1, [M1-G-GATE-15.1](../gap-analysis.md)): concurrent `Open` of one fresh database no longer fails -- identify, the switch into WAL and the migrations run under `run/open.lock`, and `identify` reads its three values in one snapshot; `TestDutyHasOneHolderAndMovesAfterAKill` under `-race` went from 4 failures in 20 to 0 in 40 (Windows native, go1.27.4, uncommitted on `105859e`; `manual`, not gate evidence).

## Evidence record (impl.md sec. 7.3)

These are the eight fields of [impl.md sec. 7.3](../../../harness/impl.md#73-the-evidence-record),
in the format of [the evidence runbook](README.md#the-evidence-record-headings). Plan step
18.1.7 fills them at the frozen M1 closure commit. Plan Stage 15.1 added the "Gap closure
(M1-G-STORE)" section and fills the Phase 15 lines below. The history above stays exactly
as it was recorded and is not closure evidence (M1-D-CLOSE-1).

### Source and build identity

At closure: not yet recorded (plan 18.1.7). Phase 15 (gap closure): commit
`6e7514cdb6fbc8f105238e7e655fb20e3102166b`, one commit for the three modules; the
rehearsal built six `cmd/dsh` binaries at it, each `vcs.modified=false` (checksums in
the rehearsal record). Historical: the driver pins (`modernc.org/sqlite` v1.62.0,
`modernc.org/libc` v1.80.1) under "Fact F-2"; no commit was recorded.

### Environment and fixture

At closure (18.1.7): both local lanes, from their run records at the closure commit.
Phase 15 (gap closure), from the two `6e7514c` run records:
- WSL lane: Ubuntu 24.04.5 LTS (`Ubuntu-24.04`) on WSL2, kernel
  `6.18.58.1-microsoft-standard-WSL2`, go1.27.4 linux/amd64, gcc 13.3.0, a WSL-native
  clone on ext4, `CI=1` for the children.
- Windows lane: Windows 10.0.28000.3501, go1.27.4 windows/amd64, gcc 16.1.0 (MSYS2),
  NTFS checkout, `CI=1` for the children.
- Bench machine (Windows figures): i9-13900K. The WSL bench figures are pending.
- macOS: out of scope (`platforms-windows-wsl`).

Historical: the Windows machine named under "Fact F-2". Fixtures: scratch homes
(`DSH_E2E_STORE_HOME`), the committed `testdata/export.golden`, and (Phase 15) the
heavy-tailed corpus `internal/testkit/corpus` with its 100,000-event seeder.

### Exact tests and commands

At closure: the lane scripts, and `go -C src/harness test -race -count=1
./internal/session/store/...`. Phase 15: the commands in "Gap closure (M1-G-STORE)".
Historical: the commands under each stage's "Test results" and "Manual checks of impl.md
DH-S-7".

### Run identifiers

At closure (18.1.3, 18.1.7): the run-record ids of both lanes at the frozen closure
commit, in the runbook's template; not yet run. Phase 15 (gap closure), at `6e7514c`:
WSL lane `wsl-6e7514c-20261123T0602Z` (file SHA-256
`e643fb9c05106103196578bbc483b4fa199c066baa3d3be5d7f3c5fcfbefa815`; rehearsal
`wsl-6e7514c-20261123T0602Z-rehearse.txt`); Windows lane `win-6e7514c-20261123T0602Z`
(file SHA-256 `69bd371846922b7e1f30eb65c11de10da067267112447a95f8594485fc71a6cf`); the
full template is in "Gate runs at 6e7514c". macOS: out of scope (`platforms-windows-wsl`).
Historical: no run id was recorded before Phase 15. The guarded scenarios DH-E-STORE-1,
-2 and -6 run in impl.md step 16.8 and have no run id yet.

### Results

At closure: not yet recorded. Phase 15: both lanes `PASSED all 12 gates` at `6e7514c`;
per test ID, "Test IDs and results" in "Gap closure (M1-G-STORE)". Historical: the test
tables of Stages 7.1, 7.2 and 7.3 and the driver measurements.

### Manual checks

impl.md DH-S-7 names four checks: kill a test program between an append and its flush
and record what the reopened store holds (step 7.3); open one database from two test
programs and record the refused second writer (step 7.2); export a session, import it
into an empty home and compare (step 7.4); import and delete are checked on a scratch
home only (step 7.6). Steps 7.1 and 7.5 take the rule-3 default. Historical record:
"Manual checks of impl.md DH-S-7 (scratch home)". How to run and fill the register:
[the evidence runbook](README.md#manual-checks). Every row runs with
`DSH_E2E_STORE_HOME` set to a new, empty scratch directory, never a user home, as
`go -C src/harness test -count=1 -v -run '<names>' ./internal/session/store/sqlite/`.

#### Manual checks register

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 7.1 | rule-3 default: one database per home, atomic appends, a flush barrier | composed test: `TestConformance`, `TestMultiEventAppendIsAtomic`, `TestBudgetStoreAppend`; record the `F-2` lines | Windows, WSL | | | | `manual` |
| 7.2 | open one database from two test programs: the second writer is refused | composed test: `TestSecondWriterRefusedAcrossProcesses`; record the refusal and the first writer's identity | one lane | | | | `manual` |
| 7.3 | kill a test program between an append and its flush: what the reopened store holds | composed test: `TestCrashBetweenAppendAndFlushRecoversHonestly`; record the events and the closers (outcome-unknown result, `step/end`, `turn/end` interrupted) | one lane | | | | `manual` |
| 7.4 | export a session, import it into an empty home and compare | composed test: `TestExportImportRoundTrip`; record the file's line count and that the event rows of both homes are equal | one lane | | | | `manual` |
| 7.5 | rule-3 default: a process follows a session another process writes; once-per-home duties | composed test: `TestFollowerSeesAnotherProcessesCommits`, `TestDutyHasOneHolderAndMovesAfterAKill` | one lane | | | | `manual` |
| 7.6 | import and delete on a scratch home only | composed test: `TestInterruptedDeletionFinishesOnOpen`, `TestImportRefusals`; record that nothing of the deleted session remains and `Check` is clean | one lane | | | | `manual` |

### Cleanup evidence

At closure: the scratch directory is listed after the run (empty) and then deleted; no
user home was touched. Historical: "The directory was empty afterwards and was deleted",
under "Manual checks of impl.md DH-S-7".

### Open items

At closure: plan 18.1.11 lists them. Now open:
- the `DH-T-STORE-18` bench figures on WSL (the lead fills them after the WSL run);
- the held-open entry H-34 ("Held open" below): provisional budgets until impl.md 16.7;
- the guarded scenarios DH-E-STORE-1, -2 and -6 (impl.md step 16.8) and DH-E-STORE-5
  (step 35.7), authored and not run; until step 16.8 passes, the SQLite store is used on
  scratch homes only;
- the per-step manual-check register, run at plan step 18.1.5;
- `TestLivenessLockSurvivesOSOpenAndClose` (`DH-T-STORE-7`) carries no `//dsh:test`
  directive.

Built by Phase 15: the store gaps of plan Stage 15.1 (M1-G-STORE-1 to -5, -8, -9, and
-6 with its budgets held open as H-34 and its WSL figures pending), the bounded open (M1-G-STORE-21, M1-D-STORE-F) and the POSIX runs on WSL (M1-G-STORE-30),
recorded in "Gap closure (M1-G-STORE)".

#### Held open

A held-open entry is a part of a deliverable that is ticked as built but is not done:
the part that waits is not final, and its step is not done, until the closing step
lands. H-34 is the M1 story's entry for DH-S-7: decision M1-D-STORE-B (a) makes the
`DH-T-STORE-18` budgets provisional from their first measured run (plan step 15.1.5). A
*provisional budget* is a time and memory limit taken from the first measured run: the
benchmark fails when it is missed, but the number is not final until it is pinned at
its closing step. The entry is not H-10, which is the replay of a session with children
(lead settlement `budget-held-open-not-h10`). The DH-S-8 entries H-33 and H-35 are in
[DH-S-8.md, "Held open"](DH-S-8.md#held-open). impl.md carries pointer lines to these
lists (DC-2); its table is unchanged. The closing stage records the entry it closes.

| ID | Step | Deliverable | The part held open | Gap | Closing step | Status |
| --- | --- | --- | --- | --- | --- | --- |
| H-34 | 7.1 | DH-D-STORE-2 | The final budgets of `DH-T-STORE-18`: the open of a session above 100,000 events from its checkpoints, its fork and the first list page over the heavy-tailed corpus, each also under `GOMEMLIMIT`. Until then the budgets are the provisional ones of plan step 15.1.5 | M1-G-STORE-6, M1-G-STORE-7 | impl.md 16.7 (M1-D-STORE-B (a)) | open |
