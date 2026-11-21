# DH-S-7 evidence

DH-S-7 is the SQLite store, leases and recovery. This file holds the evidence of its
stages as they are built. **Closure status: open.** Stage 7.1 is recorded below. The
leases, the liveness check, recovery and the crash-kill loop (`DH-T-STORE-5`) are the
later stages of this phase.

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

| Measurement | n | p50 | p95 | p99 | max |
| --- | --- | --- | --- | --- | --- |
| open, new file (creates it and runs the migrations) | 20 | 9.56 ms | 17.26 ms | 17.26 ms | 23.60 ms |
| open, existing file | 20 | 4.90 ms | 11.10 ms | 11.10 ms | 18.15 ms |
| append (one event, WAL commit, `synchronous=NORMAL`) | 1000 | <0.5 ms | 0.66 ms | 2.65 ms | 4.84 ms |
| flush (barrier commit, `synchronous=FULL`) | 1000 | 0.52 ms | 2.58 ms | 5.97 ms | 12.69 ms |
| append + flush | 1000 | 0.92 ms | 3.15 ms | 6.35 ms | 12.69 ms |

An earlier run on the same machine gave an append + flush p95 of 2.73 ms and a flush
p95 of 2.31 ms. On Windows the Go monotonic clock ticks about every 0.5 ms, so an
append that finishes inside one tick reads as 0. The p50 of the append row is
therefore given as "<0.5 ms".

**Budgets.** These are compiled into the test: `DSH_*` and `GO*` cannot relax them,
and the budget gate clears both anyway. They were set from the run above, with
headroom for slower disks and the fsync cost of CI runners:

| Budget | Value | Measured p95 | Headroom |
| --- | --- | --- | --- |
| open, new file | 250 ms | 17.26 ms | ~14x |
| append | 10 ms | 0.66 ms | ~15x |
| append + flush | 50 ms | 3.15 ms | ~16x |

The test passes, also under `-race`. The figures are printed with the prefix `F-2`
for this record.

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
  `sessions`, `events`, `checkpoints`, `hosts`, `duties`, `requests`, `barrier`.
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
- **Flush barrier.**
  - Appends commit with `synchronous=NORMAL`. Such a commit survives the death of the
    process.
  - `Flush` commits one row of `barrier` with `synchronous=FULL`. That fsyncs the WAL,
    and with it every earlier frame.
  - `Flush` returns only after that commit. `Writer.Close` and `Store.Close` flush.
  - `Summary.Durable` is `NextSeq` except for sessions this instance has committed to
    since the last barrier.
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
| Multi-event append is atomic (TC-07) | `TestMultiEventAppendIsAtomic`: three events, the third repeats a primary key. A fresh `sql.Open` reads none of the three and `next_seq` is unchanged. A contract-refused third record behaves the same way. | pass |
| Flush is durable (TC-07) | `TestFlushSurvivesKill`: a re-executed child appends and flushes, then is killed before closing anything. Reopening the store shows all five rows. | pass |
| Driver budget (`DH-T-STORE-18`, F-2) | `TestBudgetStoreAppend` | pass, figures above |
| Log immutable in the schema | `TestEventsImmutableInSchema` | pass |
| Open refusals leave the file unchanged | `TestOpenRefusesUnchanged`: newer layout, foreign id, unrelated schema, not SQLite | pass |
| Reopen keeps records, checkpoints, epoch; WAL; layout version | `TestReopen` | pass |
| Lease shared across instances on one home | `TestTwoInstancesShareTheLease` | pass |
| Error translation (DH-LIB-SQLITE-R7) | `TestTranslate` | pass |
| One importer of the driver | archtest rule "only internal/session/store/sqlite imports the SQLite driver" | pass |

Package statement coverage is 88.4%; the gate's floor is 85%.

`DH-T-STORE-2` to `-4` and the rest of `DH-T-STORE-18` belong to later stages of this
phase, so they are not part of 7.1:

- the crash-kill loop of `DH-T-STORE-5`;
- dead-holder takeover with the liveness lock;
- the open of a session of more than 100,000 events;
- the heavy-tailed listing corpus.
