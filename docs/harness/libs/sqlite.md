# SQLite driver (`node:sqlite`): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go module facts were read on 2026-11-18 with `go list -m -json`,
> `go mod download -json` and by reading files in the module cache (Go 1.27.4, default
> proxy and checksum database). Published performance figures are quoted, never measured
> here. Nothing was compiled, run or benchmarked.

| | |
|---|---|
| **Library ID** | `DH-LIB-SQLITE` |
| **Upstream unit** | `UP-DEP-node:sqlite` |
| **Upstream version** | Node built-in (`DatabaseSync`, `StatementSync`); no version of its own |
| **Kind** | Node built-in |
| **Used by** | `session` (session store), `session-query` (full-text search), `storage` (key-value backend), `experimental` (a stub for the browser worker) |
| **Go choice** | named Go module `modernc.org/sqlite` v1.62.0 through `database/sql`, **provisional until the measurements in §5.3 are taken** |
| **Related areas** | A05, the session store, which owns the driver package ([../areas/session-store.md](../areas/session-store.md)); A06, session search ([../areas/session-views.md](../areas/session-views.md)); A07, machine-state tables ([../areas/storage.md](../areas/storage.md)); decisions [DH-ADR-PERSISTENCE-3](../upstream/session-and-persistence.md#dh-adr-persistence-3), [-4](../upstream/session-and-persistence.md#dh-adr-persistence-4), [-8](../upstream/session-and-persistence.md#dh-adr-persistence-8), [-9](../upstream/session-and-persistence.md#dh-adr-persistence-9); owner decisions [DH-ADR-12](../design.md#dh-adr-12) (several processes share one home) and [DH-ADR-15](../design.md#dh-adr-15) (the `dsh` binary is pure Go) |

## 1. What it is and why upstream needs it

SQLite is a database engine that runs inside the program that uses it. There is no
server: the database is one file, and the engine is a library. It gives **transactions**
(a group of changes that is stored completely or not at all, also across a crash), which
is what a session store otherwise has to build by hand from temporary files, renames and
sync calls.

Upstream uses Node's built-in binding, `node:sqlite`, in three optional packages: an
alternative session store, the full-text session search, and an alternative key-value
backend. Upstream chose the built-in on purpose and rejected the faster native package
`better-sqlite3`, because the built-in "works inside the single-file executable where a
native addon would complicate packaging"
(`.agents/notes/rejected/simplification/2026-07-26-dependency-swaps-rejected-by-nih-audit.md:61`).

In the Go design SQLite stops being optional. One database per harness home holds every
session, the search index, projection checkpoints and machine state
([DH-ADR-PERSISTENCE-3](../upstream/session-and-persistence.md#dh-adr-persistence-3),
-8, -9). The driver is therefore a dependency of every turn: each flush before a model
request is a commit through it. Go faces the same packaging force upstream did. The
fastest driver needs a C compiler, and the `dsh` binary must build without one
([DH-ADR-15](../design.md#dh-adr-15)).

Terms used below.

| Term | What it is | Why it matters here |
|---|---|---|
| **Driver** | The Go package that embeds or binds SQLite and plugs into `database/sql`, the standard library's database interface. | Callers written against `database/sql` do not change when the driver does. |
| **cgo** | Go's mechanism for calling C code. It needs a C compiler for every target platform. | A cgo dependency ends cross-compiling every platform from one build machine, and the owner ruled it out for `dsh` ([DH-ADR-15](../design.md#dh-adr-15)). |
| **WAL** | Write-ahead log: a journal mode in which changes are appended to a side file. Readers do not block the writer and the writer does not block readers. | The store has one writer and many readers ([design.md §4.5](../design.md#45-concurrency-model)). |
| **FTS5** | SQLite's full-text search extension: a table type that keeps an inverted index of words. | Session search ([DH-ADR-PERSISTENCE-8](../upstream/session-and-persistence.md#dh-adr-persistence-8)). |
| **Tokenizer** | The part of FTS5 that splits text into the terms that are indexed. | It decides what a search can find, and it is where Chinese, Japanese and Korean text goes wrong. |
| **Busy timeout** | How long a connection waits for a lock held by another connection before it fails with "busy". | Several processes may open one home ([DH-ADR-12](../design.md#dh-adr-12)). |
| **VFS** | SQLite's operating-system layer: file reads, writes, sync and locks. | Each driver supplies it differently, and two processes are only safe when their locks are compatible. |

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API or SQL feature used |
|---|---|---|
| `packages/session/session-persistence-sqlite/src/sqlite.ts:217-226` | Open the session database. The import is deferred to first use. Busy timeout 5,000 ms (`:42`), `foreign_keys = ON`, `synchronous = FULL`, WAL | `new DatabaseSync(path, { timeout })`, `exec` |
| `sqlite.ts:224-234` | Retry the switch to WAL while it reports "busy", because "SQLite can bypass its busy handler during the journal-mode lock upgrade" | error `errcode`, a retry loop |
| `sqlite.ts:235-244`, `:282-283` | Recognise its own file by `application_id` and `user_version`; refuse an unrelated database (`:239`) or another layout version (`:242`) | `PRAGMA` reads |
| `sqlite.ts:305-306` | Make stored frames append-only inside the database | triggers that `RAISE(ABORT)` |
| `sqlite.ts:334-350` | Transactions; when both `COMMIT` and `ROLLBACK` fail the outcome is recorded as unknown and the provider stops (`:350`) | `BEGIN <mode>`, `isTransaction` |
| `sqlite.ts:820-828`, `:52` | Group commit: batches from several sessions share one transaction, each in its own savepoint, at most 1,024 events per group | `SAVEPOINT`, `ROLLBACK TO` |
| `sqlite.ts:251-253` | Report a damaged or foreign file as corruption | `SQLITE_CORRUPT`, `SQLITE_NOTADB` from `errcode & 0xff` |
| `packages/session-query/session-query-sqlite/src/schema.ts:52-70` | Open the search index, again with a deferred import; identity checks run before any pragma that writes | `PRAGMA application_id`, `user_version`, `journal_mode` |
| `schema.ts:127-136`, `:158-167` | Two FTS5 tables (stored sessions, and a temporary one for live sessions) with `tokenize = 'unicode61'` and unindexed metadata columns | FTS5 |
| `packages/session-query/session-query-sqlite/src/index.ts:816-820`, `:839-843` | Search: the query is bound as a parameter to `MATCH ?`; `highlight()` marks the matches that ranking counts | FTS5 `MATCH`, `highlight()` |
| `index.ts:455-476` | Reconcile the index with the store in one write transaction | `BEGIN IMMEDIATE` |
| `schema.ts:109`, `:124` | Typed columns | `STRICT` tables |
| `packages/storage/storage-sqlite/src/schema.ts:66-81`, `src/unit.ts:57-70`, `:152-163` | Key-value rows, one JSON document per row; prepared statements are kept and reused | `prepare`, `run`, `get`, `all` |
| `packages/storage/storage-sqlite/src/index.ts:36`, `README.md:53` | Journal mode is configurable (`wal`, `delete`, `truncate`, `persist`) for file systems where WAL does not work | `PRAGMA journal_mode` |
| `packages/experimental/webworker-runtime/src/node/builtin_modules/mock/sqlite.ts:11-14` | A stand-in that throws, so that host code loads in a browser worker | none of the real module |
| 13 test files, for example `packages/session/session-persistence-sqlite/tests/fixtures/store.ts:5` | Open the database directly to inspect or damage it | `DatabaseSync` |

What the harness needs from SQLite is therefore a short list: WAL with full
synchronisation, a busy timeout, savepoints, triggers, `STRICT` tables, the identity
pragmas, reusable prepared statements, numeric error codes, and FTS5 with `MATCH` and
`highlight()`. Upstream loads no SQLite extension and registers no custom function.

## 3. Tech debt upstream carries

No `UP-DEBT` unit is assigned to this specification: `node:sqlite` is built in, so there
is no patch or version pin. Search performed: every import of `node:sqlite` (static,
dynamic and type-only), the "Known Limitations and Deferred Work" section of the three
consuming packages' READMEs, and `patches/` (the one hit,
`patches/@yao-pkg__pkg@6.21.0.patch:153`, belongs to the packager). The limits below are
caused by the driver or by how it is used. Their ledger units belong to the persistence
areas; they are listed because each becomes a requirement here.

| What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|
| The driver is synchronous: a write or a search blocks the only JavaScript thread and a running statement cannot be interrupted | `packages/storage/storage-sqlite/README.md:130`, `packages/session-query/session-query-sqlite/README.md:144` | A call blocks one goroutine. Cancelling the call's context interrupts the statement (`DH-LIB-SQLITE-R8`) |
| The import is deferred so that Node's "experimental" warning appears late or never | `packages/session-query/session-query-sqlite/README.md:67`, `packages/bundle/web-app/cordis.patch.yml:25` | Not applicable: a Go import prints nothing |
| No busy wait in the storage backend: a competing writer is rejected at once | `packages/storage/storage-sqlite/README.md:131` | One busy-timeout policy for every connection (`DH-LIB-SQLITE-R4`) |
| The switch to WAL needs a hand-written retry loop | `packages/session/session-persistence-sqlite/src/sqlite.ts:224-234` | Inference: this is SQLite behaviour, not Node behaviour, so it follows the engine into Go. The open sequence keeps a bounded retry (`DH-LIB-SQLITE-R12`) |
| The open sequence (owner-only file, identity check, pragmas) is written twice | `packages/storage/storage-sqlite/README.md:134` | One database, one open function in one package (`DH-ADR-SQLITE-2`) |
| Search matches whole tokens only; the trigram alternative "was measured and rejected" for index size and two-character support | `packages/session-query/session-query-sqlite/README.md:145`, `:158` | `DH-ADR-SQLITE-3` |
| One process must own each search index | `packages/session-query/session-query-sqlite/README.md:146` | The index lives in the session database and shares its locking ([DH-ADR-PERSISTENCE-8](../upstream/session-and-persistence.md#dh-adr-persistence-8)) |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-SQLITE-R1` | The `dsh` binary builds with `CGO_ENABLED=0` for every release target and the store works in that build. | [DH-ADR-15](../design.md#dh-adr-15), [DH-ADR-TOOLCHAIN-13](../upstream/toolchain.md#dh-adr-toolchain-13) |
| `DH-LIB-SQLITE-R2` | A commit returns only after the data is synced. After a crash the database holds exactly the committed transactions. WAL mode, `synchronous = FULL`. | `sqlite.ts:223-226`, [DH-ADR-PERSISTENCE-4](../upstream/session-and-persistence.md#dh-adr-persistence-4) |
| `DH-LIB-SQLITE-R3` | One writer and many readers work at the same time. A reader sees one consistent snapshot and never a half-written batch. | [design.md §4.5](../design.md#45-concurrency-model) |
| `DH-LIB-SQLITE-R4` | A second process that opens the same file waits for a bounded time and then gets one classified "busy" error. The file format and the locking are those of stock SQLite, so the `sqlite3` command-line tool can read a copy. | `sqlite.ts:42`, [DH-ADR-12](../design.md#dh-adr-12) |
| `DH-LIB-SQLITE-R5` | FTS5 is available with `MATCH`, `highlight()` and a ranking function. Query text is always a bound parameter and always one quoted phrase. | `index.ts:816-820`, `README.md:59` |
| `DH-LIB-SQLITE-R6` | Savepoints, triggers, `STRICT` tables, JSON functions, `application_id` and `user_version` are available, and prepared statements can be reused. | §2 |
| `DH-LIB-SQLITE-R7` | Driver errors are translated once into harness errors: busy, constraint, corrupt or not a database, disk full, read-only, interrupted. No driver error type crosses the store interface. | `sqlite.ts:229`, `:251-253` |
| `DH-LIB-SQLITE-R8` | Every database call takes a `context.Context`. Cancelling it stops a long statement within a bounded time and leaves the connection usable. | `README.md:144` (the limit being removed) |
| `DH-LIB-SQLITE-R9` | Exactly one harness package imports the driver. Every other package uses the store interface. | `DH-ADR-SQLITE-2` |
| `DH-LIB-SQLITE-R10` | A consistent backup copy can be taken while the store is in use, through SQLite itself (its backup interface or `VACUUM INTO`). | Backup before a layout migration, [DH-ADR-PERSISTENCE-6](../upstream/session-and-persistence.md#dh-adr-persistence-6) |
| `DH-LIB-SQLITE-R11` | The host never opens or closes the database file through the `os` package while the store is open, and the lease's liveness lock is a separate file. On Linux the driver's lock mode that survives such a close is switched on before the first open. | §6.3 |
| `DH-LIB-SQLITE-R12` | The open sequence checks identity and layout version before any pragma that writes, refuses an unrelated or newer database without changing it, creates the file owner-only, and retries the first switch to WAL for a bounded time. | `sqlite.ts:213`, `:224-244`; `schema.ts:55-70` |
| `DH-LIB-SQLITE-R13` | Search behaves the same on every candidate driver: no tokenizer that only one driver can provide. | `DH-ADR-SQLITE-3` |
| `DH-LIB-SQLITE-R14` | The licence of the driver and of everything it embeds allows distribution in an MIT-licensed binary and appears in the third-party notices. | [design.md §8](../design.md#8-conflicts-and-gaps-between-the-foundation-recommendations) (notices gap) |

## 5. Go choice

**Decision.** `modernc.org/sqlite` v1.62.0, used only through `database/sql`, is the
driver of the `dsh` binary. The choice is **provisional**: it becomes final when the
measurements in §5.3 are taken and the owner accepts them. The fallback order is fixed
now so that a failed measurement is a one-package change and not a redesign.

**Why.** It is the only candidate that meets every requirement without a C compiler, an
extra registration step or a version-0 interface. It is the same decision upstream made
for the same reason (§1).

### 5.1 The drivers compared

| | `modernc.org/sqlite` (chosen) | `github.com/ncruces/go-sqlite3` (first fallback) | `github.com/mattn/go-sqlite3` (cgo) |
|---|---|---|---|
| **Version, release date** | v1.62.0, 2026-11-07 | v0.37.0, 2026-11-07 | v1.14.53, 2026-11-05 |
| **How SQLite gets into the binary** | SQLite's C source translated to Go by a tool, one generated file set per platform (`lib/`) | A WebAssembly build of SQLite translated to Go by `wasm2go` (`README.md`, first section) | The C source compiled by the C compiler at build time (`sqlite3-binding.c`) |
| **cgo** | No (`doc.go:5`: "CGo-free port") | No (`README.md`: "cgo-free") | **Yes.** `README.md:83`: `CGO_ENABLED=1` and a `gcc` on the path are required |
| **Minimum Go version** | 1.26.0 (`go.mod`) | 1.26.0 (`go.mod`) | 1.21 (`go.mod`) |
| **SQLite version** | 3.54.1 (`doc.go:64-85`) | Not read | 3.54.1 (`sqlite3-binding.h:150`) |
| **`database/sql`** | Yes, driver name `sqlite` | Yes, in the `driver` sub-package; also a direct interface | Yes, driver name `sqlite3` |
| **FTS5** | Compiled in: `-DSQLITE_ENABLE_FTS5` is on the generator line of every platform file, for example `lib/sqlite_linux_amd64.go:1` | A separate package, `ext/fts5`, that must be registered on each connection (`ext/fts5/fts5.go:14`) | Only with the build tag `sqlite_fts5` (`sqlite3_opt_fts5.go:6`) |
| **Tokenizers** | The four that stock FTS5 has: `unicode61`, `ascii`, `porter`, `trigram` (constructors found in `lib/`) | Stock FTS5; the build's tokenizer list was not read | The four stock ones (constructors found in `sqlite3-binding.c`) |
| **A tokenizer written in Go** | No interface for it was found: outside the generated code the word "tokenizer" appears only in an unrelated comment | Yes: `RegisterCustom` and a `Tokenizer` interface (`ext/fts5/fts5.go:21`, `:80`) | Not from Go; it needs C code |
| **ICU (Unicode library)** | Not compiled in (no ICU flag on the generator line, no ICU symbol in `lib/`) | An `ext/unicode` package built on Go's `x/text` | Build tag `sqlite_icu`, which links the system ICU libraries (`sqlite3_opt_icu.go:6-11`) |
| **WAL** | SQLite's own code, translated | Its own Go operating-system layer: memory-mapped shared memory on Unix and Windows (`vfs/README.md`, "Write-Ahead Logging") | SQLite's own code |
| **File locks between processes** | SQLite's own: POSIX record locks on Unix, `LockFileEx` on Windows. On Linux an opt-in switch to open-file-description locks (`ofd.go:25-76`) | Open-file-description locks on Linux and macOS, `LockFileEx` on Windows; the document says it is "not as battle tested as the original" and states that the default is compatible with stock SQLite (`vfs/README.md`) | SQLite's own |
| **Platforms** | Windows, Linux and macOS on amd64 and arm64, and 15 further pairs (`doc.go:64-85`) | Windows, Linux and macOS on amd64 and arm64, and more (`README.md`, "Testing") | Wherever a C compiler targets |
| **Licence** | BSD 3-Clause (`LICENSE`); embeds SQLite, public domain (`LICENSE-SQLITE`), and the `sqlite-vec` extension, MIT (`LICENSE-SQLITE_VEC`); a full third-party list and a software bill of materials ship in the module | MIT (`LICENSE`) | MIT (`LICENSE`) |
| **Release cadence** | 164 tagged versions; the last ten between 2026-07-15 and 2026-11-07 | 111 tagged versions; the last ten between 2026-05-28 and 2026-11-07 | 68 tagged versions; the last ten between 2026-04-29 and 2026-11-05 |
| **Stated costs** | CPU-bound work is slower than C (§5.2). `go.mod` must pin the exact `modernc.org/libc` version the driver was validated with (`doc.go:42-48`); the driver's `go.mod` names v1.80.1 while the latest `libc` is v1.82.2, so a blanket dependency upgrade breaks the pairing | "Memory usage will be higher than alternatives" because each connection runs in its own sandbox (`README.md`, "Caveats"). Version 0: no compatibility promise | A C toolchain per target; cross-compiling needs a cross compiler (`README.md:229-238`) |

Also checked and not candidates:

- `zombiezen.com/go/sqlite` v1.4.2 (2025-05-23, ISC-style licence). It is built on the
  modernc translation and "deliberately does not provide a `database/sql` driver"
  (`README.md:10`), so it fails `DH-LIB-SQLITE-R9`'s purpose (a driver swap would rewrite
  every query), and its last release is 18 months old.
- `crawshaw.io/sqlite` v0.3.2: last release 2020-06-07. Not maintained.

Method for every row: `go list -m -json <module>@latest`, `go list -m -versions`,
`go mod download -json`, then reading the named files in the module cache, 2026-11-18.

### 5.2 Published performance reports

None of these numbers was produced for this document. They show where the risk is, not
what the harness will see.

| Source | What it reports | What it means for the harness |
|---|---|---|
| The chosen driver's own documentation (`doc.go:92-131` in v1.62.0), measured by its authors in September 2026 on linux/amd64 | CPU time per query against C SQLite 3.53.4 built with the same options: 2.0 times for an unindexed sort over 584,000 rows, 1.9 times for a grouped aggregate, 1.3 times for an indexed correlated subquery. "I/O-bound work is dominated by the operating system either way." Throughput over four connections scaled at least as well as C | A flush is a sync, so the commit path should be dominated by the disk. Search, listing and index maintenance are CPU-bound and pay the ratio |
| `github.com/cvilsmeier/go-sqlite-bench`, run of 2026-03-23, Debian 12 on an 11th-generation Intel laptop, Go 1.26.0; each test run twice, described by its author as "not very scientific" (page read 2026-11-18) | Insert one million rows in one transaction: mattn 1,480 ms, modernc 2,419 ms, ncruces 2,719 ms. Query them: 871, 758 and 850 ms. Read 10,000 rows of 50,000 bytes each: mattn 122 ms, ncruces 151 ms, modernc 401 ms. Eight goroutines reading: 2,395, 2,355 and 2,061 ms. Author's conclusion: "We cannot declare a clear winner, it all depends on the use case" | The pure-Go drivers are within a factor of two of C for small rows. **Large rows are the outlier:** modernc is about three times slower than C when reading them, and assistant messages and tool results are large rows |
| "The SQLite Drivers Benchmarks Game", `modernc.org/sqlite-bench` v1.1.18, kept by the chosen driver's own maintainers; versions mattn v1.14.53, modernc v1.62.0, ncruces v0.37.0, Go 1.27.3 (page read 2026-11-18) | One point per best time across the same five workloads on many platforms: ncruces 104, mattn 80, modernc 24. The page says "Do not trust benchmarks, write your own" | The chosen driver is the slowest of the three by its maintainers' own scorecard. The first fallback is the fastest, and it is also pure Go |
| The chosen driver's change log (`CHANGELOG.md:12`) | v1.61.0 made prepared statements cost "about twice what it did in v1.60.1"; v1.62.0 fixed it | Speed changes between releases. The measurement is a gate on every driver upgrade, not a one-time check |

### 5.3 What must be measured before the choice is final

Each item names the workload, why it decides something, and what happens on failure.
The owner sets the thresholds; this document does not invent them.

| # | Measure, on Windows, Linux and macOS, for modernc and ncruces, with mattn as the reference | Why it decides something |
|---|---|---|
| M1 | Time of one flush: a batch of events and their index rows committed in WAL mode with `synchronous = FULL`, at the checkpoint policy's rate (before each model request, each top-level tool body and each step boundary) | Every turn waits for it ([DH-ADR-PERSISTENCE-4](../upstream/session-and-persistence.md#dh-adr-persistence-4)). Expected to be the disk, not the driver; this confirms it |
| M2 | Write and read of large payloads: events from 50 KB to several megabytes, and opening a session with thousands of them | The one workload where a published report shows a three-fold gap (§5.2) |
| M3 | Cost that the search index adds to each append, and the index size on disk, for each tokenization option of `DH-ADR-SQLITE-3`, on English and on Chinese, Japanese and Korean text | [DH-ADR-PERSISTENCE-8](../upstream/session-and-persistence.md#dh-adr-persistence-8) moves indexing from search time to append time |
| M4 | Search latency and listing latency over ten thousand sessions | CPU-bound; pays the 1.3 to 2.0 ratio |
| M5 | Time to cancel a running search | `DH-LIB-SQLITE-R8` |
| M6 | Two processes writing to one home: waiting time and the rate of "busy" errors | [DH-ADR-12](../design.md#dh-adr-12): sharing a home is the normal case, so this is a required measurement |
| M7 | Size of the `dsh` binary and build time, with and without the driver; memory per open connection | [DH-O-14](../design.md#dh-o-14): one binary per platform. The translated source is large enough that the module's own documentation discusses Go's 500 MB module size cap (`README.md`, "Generated sources") |
| M8 | The same M1 to M4 after each driver upgrade | The v1.61.0 regression |

If modernc fails a threshold, repeat with ncruces. If both fail, the question returns to
the owner: the only faster option is cgo, which [DH-ADR-15](../design.md#dh-adr-15)
forbids in the `dsh` binary, so no fallback inside this design remains.

### 5.4 Decisions

<a id="dh-adr-sqlite-1"></a>
#### DH-ADR-SQLITE-1 A pure-Go driver, accepted on measurement, with a fixed fallback order

- **Decision.** The `dsh` binary uses `modernc.org/sqlite`. The choice is final only
  after the §5.3 measurements. First fallback: `github.com/ncruces/go-sqlite3`, also pure
  Go. There is no second fallback: `github.com/mattn/go-sqlite3` needs cgo, the owner
  made pure Go a hard rule for `dsh` ([DH-ADR-15](../design.md#dh-adr-15)), and no other
  artifact exists that could carry it ([DH-ADR-18](../design.md#dh-adr-18)). mattn stays
  in §5.1 and §5.3 as the speed reference only.
- **Why.** No dependency of `dsh` may require cgo ([DH-ADR-15](../design.md#dh-adr-15)). Of the two pure-Go
  drivers, modernc runs SQLite's own file and lock code, has a version-1 interface and
  has FTS5 compiled in; ncruces replaces the operating-system layer with its own, is
  version 0, and states higher memory use.
- **Tradeoff.** The slowest of the three candidates by its maintainers' own scorecard; a
  larger binary; a pinned `libc` companion module; and a `go 1.26.0` line in its
  `go.mod`, which the harness module must meet. That is not this driver's cost alone:
  ncruces and nine other chosen modules declare the same line, and it is the floor the
  design adopts ([../design.md](../design.md), CF-4).
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-14](../design.md#dh-o-14).
- **Verified by.** `DH-T-SQLITE-1` (the build), `DH-T-SQLITE-8` (the measurements).

<a id="dh-adr-sqlite-2"></a>
#### DH-ADR-SQLITE-2 One package owns the driver; everything else sees `database/sql` behind the store interface

- **Decision.** A single harness package imports the driver. It owns the connection
  string, the pragmas, the open sequence, the error translation and the backup call. All
  SQL runs through `database/sql`. There are two pools on the one file: a write pool of
  exactly one connection whose transactions begin with the write lock already taken, and
  a bounded read pool.
- **Why.** It makes `DH-ADR-SQLITE-1`'s fallback real: the three candidates differ in
  driver name, connection-string keys and error types, and in nothing a query sees. One
  write connection matches SQLite's one-writer rule and the one writer goroutine of
  [DH-ADR-PERSISTENCE-4](../upstream/session-and-persistence.md#dh-adr-persistence-4).
  The read pool is bounded because `database/sql` "opens connections without limit by
  default" and each carries its own page cache (`doc.go:116-118`).
- **Tradeoff.** No driver-specific fast path. The same published benchmark shows a
  direct (non-`database/sql`) interface, `zombiezen.com/go/sqlite`, reading the
  one-million-row table in 263 ms against 758 to 871 ms for the three `database/sql`
  drivers; that speed is given up for the ability to swap.
- **Serves.** [DH-O-5](../design.md#dh-o-5).
- **Verified by.** `DH-T-SQLITE-2` (import check), `DH-T-SQLITE-3` (the contract suite
  runs unchanged on each pure-Go driver).

<a id="dh-adr-sqlite-3"></a>
#### DH-ADR-SQLITE-3 Search tokenization uses only what stock FTS5 has; language handling is done in Go

- **Decision.** The search index uses a tokenizer built into stock FTS5. Anything a
  language needs beyond that is done by the harness in Go before text reaches the index
  and before a query reaches `MATCH`. No tokenizer written in C, and none registered
  through one driver's private interface. Which stock tokenizer and which preparation
  are chosen is decided by measurement M3 in the session-search area; the recommended
  candidate to measure first is the last row of the table below.
- **Why.** The persistence document asks which tokenizer handles Chinese, Japanese and
  Korean. The answer is that no stock tokenizer does, and the two ways to add one tie
  the product to a driver:

  | Option | Chinese and Japanese (written without spaces) | Korean (written with spaces) | Short queries | Works on |
  |---|---|---|---|---|
  | `unicode61` (upstream's choice, `schema.ts:135`) | Token characters are every letter and digit category, and separators are spaces and punctuation ([sqlite.org/fts5.html §4.3.1](https://sqlite.org/fts5.html), read 2026-11-18). Inference: a sentence with no spaces is therefore one token, and only a whole run or a prefix of it can match | Whole space-separated words only; a word with a grammatical ending attached does not match its stem | Any length | Every driver |
  | `trigram` | Any substring of three or more characters matches | Same | "Substrings consisting of fewer than 3 unicode characters do not match any rows" (same page, §4.3.5). Inference: most Chinese words are two characters, so the commonest queries find nothing. Upstream measured it and rejected it for index size and two-character support (`README.md:158`) | Every driver |
  | The ICU library's word segmentation | Dictionary-based word boundaries | Yes | Any | Not FTS5: the page lists four built-in tokenizers and no ICU one. mattn's `sqlite_icu` tag links the system ICU libraries, which needs cgo and a library on the user's machine |
  | A custom tokenizer registered with FTS5 | Whatever it implements | Same | Same | ncruces from Go; mattn from C; modernc: no Go interface found |
  | **Preparation in Go, then `unicode61`** (recommended to measure first) | Before indexing, each run of Chinese, Japanese or Korean characters is rewritten as overlapping two-character pieces separated by spaces; a query is rewritten the same way and searched as a phrase. Inference: a phrase of consecutive overlapping pairs matches exactly where the original characters are adjacent, so this is substring search for two or more characters | Same treatment; a stem matches inside a longer word | Two characters work. One character needs a prefix query, which the area must decide whether to allow | Every driver: it uses no driver feature |

- **Tradeoff.** The index holds rewritten text, so `highlight()` offsets no longer point
  into the original; the harness computes snippets and match counts from the event text
  in Go. The index is larger than plain `unicode61` for such text (M3 measures by how
  much). There is no dictionary, so ranking by linguistic relevance is not attempted.
- **Serves.** [DH-O-5](../design.md#dh-o-5) ("searched").
- **Verified by.** `DH-T-SQLITE-6`.

## 6. Design notes

### 6.1 Shape

Inference: the session-store area owns the package (proposed place
`internal/session/sqlitestore`, in the family [design.md §4.2](../design.md#42-go-module-layout)
assigns to the store). This specification fixes only what the driver forces.

| Element | What it is | Driver-specific part |
|---|---|---|
| `Open(ctx, path, options)` | Runs the open sequence of `DH-LIB-SQLITE-R12` and returns the two pools | The connection string. modernc takes `_pragma=...`, `_txlock=immediate` and `_busy_timeout` and validates every key before applying any (`driver.go:112-141`, `:200`); ncruces takes `_pragma` and `_txlock` (`driver/driver.go:11-13`, `:79`) |
| Per-connection settings | `busy_timeout`, `foreign_keys`, `synchronous = FULL`, read-only for the read pool | They are set through the connection string or a connection hook, never with a statement on a pooled connection: "a physical connection returned to [the pool] keeps whatever was done on it" (`doc.go:171-173`) |
| Error translation | One function from driver error to the harness error table | Each driver has its own error type carrying SQLite's numeric code |
| Backup | A consistent copy while in use | modernc exposes SQLite's backup interface (`backup.go:29-79`); `VACUUM INTO` is plain SQL and works everywhere |

### 6.2 Ownership, failure and cleanup

- **Ownership.** The store component's fiber owns both pools and the lease lock file.
  Unloading the fiber stops admitting work, lets the writer goroutine commit or roll back
  what it holds, closes the read pool, then the write pool. Sessions and agents have
  *access* to the store through its interface; none of them can close it.
- **A failed commit.** The transaction is rolled back and the batch stays queued
  ([DH-ADR-PERSISTENCE-4](../upstream/session-and-persistence.md#dh-adr-persistence-4)).
  If the rollback also fails, the outcome is unknown: the store marks itself unusable
  and refuses further work until reopened, as upstream does (`sqlite.ts:350`).
- **Busy.** A statement that meets another process's lock waits up to the busy timeout
  and then fails with the classified error. The writer goroutine retries the whole
  transaction a bounded number of times; a reader returns the error. WAL has three
  documented cases in which a query reports "busy" regardless of the timeout (another
  connection in exclusive mode, recovery after a crash, and the last connection closing;
  [sqlite.org/wal.html §9](https://sqlite.org/wal.html), read 2026-11-18), so "busy" is
  always a handled outcome and never a programming error.
- **Cancellation.** A cancelled context interrupts the statement; the transaction it
  belonged to is rolled back and the connection returns to the pool.
- **The write-ahead file grows while a reader holds a snapshot.** If some reader is
  always inside a transaction, "the WAL file will grow without bound" (same page, §6).
  No read transaction may outlive one request, and in particular none may stay open for
  the life of a server-sent-events connection. Clean shutdown checkpoints and truncates
  the file.
- **Crash.** Nothing to clean up: on the next open SQLite replays the write-ahead file.
  The `-wal` and `-shm` side files are part of the database and are never deleted by the
  harness.

### 6.3 Two processes, and one trap inside a single process

All processes that use the database "must be on the same host computer; WAL does not
work over a network filesystem" ([sqlite.org/wal.html §1](https://sqlite.org/wal.html)).
Whether a home on a network or synced folder is supported, and with which journal mode,
is open question 7 of the persistence document; this specification only requires that
the store detect that it could not enter WAL mode and say so.

The trap: on Unix, SQLite's default locks are POSIX record locks, which "the kernel
drops ... at any close(2) of any descriptor of that file", so that opening and closing
the database file anywhere in the process, "a third-party library included", "silently
strips SQLite's own transaction locks" (`ofd.go:31-43`). Both modernc and mattn use
those locks. This is why `DH-LIB-SQLITE-R11` exists:

- Backup, hashing and size checks go through SQLite, never through `os.Open`.
- The lease's liveness lock ([DH-ADR-PERSISTENCE-4](../upstream/session-and-persistence.md#dh-adr-persistence-4))
  is taken on its own lock file beside the database, never on the database file.
- On Linux the store calls the driver's switch to open-file-description locks before
  the first open and logs the result. The switch exists only on Linux; on macOS the two
  rules above are the whole protection.

## 7. Risks and open questions

1. **The choice is unmeasured.** §5.3. Until then every statement about speed is someone
   else's benchmark.
2. **No cgo fallback.** Pure Go is a hard rule for `dsh`
   ([DH-ADR-15](../design.md#dh-adr-15)), so the decision rests on modernc and ncruces
   alone. If measurement rejects both, that is a new question for the owner, not a quiet
   exception.
3. **Minimum Go version.** Both pure-Go drivers declare `go 1.26.0` (read from each
   module's `go.mod` on 2026-11-19 with `go mod download -json`). The harness module's
   floor is therefore at least 1.26.0 whichever driver is used; the floor and its
   evidence table are recorded in [../design.md](../design.md), CF-4.
4. **The `libc` pin.** `modernc.org/libc` must stay at the version the driver names.
   An automated dependency updater must be told to move the two together.
5. **Several processes per home.** [DH-ADR-12](../design.md#dh-adr-12) makes lease
   takeover and "busy" handling routine paths. They need M6 and a scenario with two
   real processes on each operating system, which area A05 owns
   ([../areas/session-store.md](../areas/session-store.md)).
6. **Network and synced folders.** See §6.3.
7. **Encryption at rest.** Not required by any upstream use. modernc v1.62.0 was not
   checked for it; ncruces ships encrypting file layers. Persistence open question 8
   decides whether it is wanted.
8. **Tokenization.** `DH-ADR-SQLITE-3` fixes the constraint, not the answer. The
   session-search area owns M3 and the single-character question.
9. **New harness code.** The driver package (open sequence, error table, backup, import
   check) and the text preparation for search are own code for the session-store and
   session-search areas to schedule. This specification declares no deliverable.
10. **Not checked.** The SQLite version and tokenizer list inside the ncruces build; the
    binary size of any candidate; whether `CGO_ENABLED=0` builds succeed for each release
    target (stated by both pure-Go projects, proven only by `DH-T-SQLITE-1`).

## 8. Verification

Local tests against real database files in a temporary directory. Nothing has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-SQLITE-1` | local, build | `R1`, `R14` | The release build runs with `CGO_ENABLED=0` for each target and the store's tests pass on Windows, Linux and macOS runners; a check compares the `modernc.org/libc` version in `go.mod` with the one the driver's `go.mod` names; the notices file lists the driver and SQLite |
| `DH-T-SQLITE-2` | local, static check | `R9` | A source check fails when a non-test file outside the driver package imports any SQLite driver |
| `DH-T-SQLITE-3` | local | `R2`, `R3`, `R6`, `DH-ADR-SQLITE-2` | The store contract suite: a reader during a write sees the earlier snapshot; savepoint rollback keeps peer batches; triggers refuse an update of an event row; a process killed in a loop always reopens to a contiguous committed prefix. The suite also runs with the first fallback driver behind a build tag |
| `DH-T-SQLITE-4` | local, two processes | `R4`, `R7`, `R11` | A second process holds the write lock: the first waits for the timeout and returns the classified busy error. On Linux and macOS a helper opens and closes the database file with `os` inside the holding process while a second process tries to write; the second process is still refused |
| `DH-T-SQLITE-5` | local | `R8`, `R10`, `R12` | A deliberately slow query returns within the bound after its context is cancelled and the connection serves the next query; a backup taken during appends opens and passes an integrity check; a file with a foreign `application_id`, and one with a newer `user_version`, are refused byte-for-byte unchanged |
| `DH-T-SQLITE-6` | local | `R5`, `R13`, `DH-ADR-SQLITE-3` | A fixed corpus of English, Chinese, Japanese and Korean sentences with expected hits per query, including two-character queries and queries containing FTS5 operators, quotes and `*`; results are identical on both pure-Go drivers |
| `DH-T-SQLITE-7` | local | `R7` | Each SQLite result code in the table maps to its harness error on each driver: a full disk (a size-limited database), a read-only file, a corrupted header, a constraint violation |
| `DH-T-SQLITE-8` | local benchmark | `DH-ADR-SQLITE-1` | The workloads M1 to M7 of §5.3 as Go benchmarks under `test/bench`, recorded per operating system with the driver versions; a recorded result, not a pass by itself. The owner's thresholds turn it into a gate |

Real-environment scenarios for the store (a host killed mid-turn, two real `dsh`
processes on one home) belong to the session-store area specification.

## 9. Units dispositioned by this spec

- `UP-DEP-node:sqlite`: replaced by `DH-LIB-SQLITE` under `DH-ADR-SQLITE-1`.

Go items declared: `DH-LIB-SQLITE`, `DH-ADR-SQLITE-1`, `DH-ADR-SQLITE-2`,
`DH-ADR-SQLITE-3`, `DH-T-SQLITE-1` to `DH-T-SQLITE-8`.
