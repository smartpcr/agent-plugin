# Session store (formats, migrations and storage backends): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) (`2e39e70`). Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `STORE`. Nothing here was compiled, run or measured; every test and scenario names evidence that does not exist yet.

| | |
|---|---|
| **Area key** | `STORE` (agent A05) |
| **Upstream packages** | 11, all under `packages/session`: the persistence seam, two backends, six format and migration packages, the checkpoint policy and the vendor log upload |
| **Serves outcomes** | [DH-O-5](../design.md#dh-o-5) (owner), [DH-O-3](../design.md#dh-o-3), [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14) |
| **Depends on areas** | A01 (session, envelope, event registry, closer function), A02 and A03 (the callers of the checkpoint policy), A04 (request contributions), A06 (projections, search text), A07 (home layout, blobs, other tables), A08 (`fsx` file lock), A18 (the off-switch), A23 (`schema` fingerprint), A24 and A19 (commands and routes) |
| **Priority** | P0 for the store, leases, checkpoints and schema evolution: no session survives a restart without them. P1 for deletion, export and cross-process following. P2 for the vendor upload and the deferred upstream import |

## 1. Purpose

The session store is where a session's event log lives between the moment an event is accepted and the day the user deletes the session. It gives the rest of the harness four promises: an event that was **flushed** survives a crash; only **one process at a time** appends to a session, although several `dsh` processes may share one harness home ([DH-ADR-12](../design.md#dh-adr-12)); a log written by an older or newer binary is either read correctly or **refused with a reason**, never misread; and a session can be listed, exported and deleted. Without this area a crash loses the conversation, two processes can interleave appends, and a format change silently corrupts old sessions.

Upstream spends most of this area's code on reading old file formats. The Go store reads only its own format ([DH-ADR-2](../design.md#dh-adr-2)); upstream sessions enter through a separate, deferred import command ([DH-ADR-11](../design.md#dh-adr-11)).

## 2. Vocabulary

Terms from [../design.md §2](../design.md) (session, session event, `seq`, surface, projection, checkpoint, lease, harness home) keep their meaning. This area adds:

| Term | What it is | Why it exists |
|---|---|---|
| **Store, reader, writer** | The store is the service that keeps sessions. A reader is an open view of one session. A writer is a reader that may also append; at most one exists per session across all processes. | One object carries write ownership, so there is one place to enforce it. |
| **Batch** | What a writer appends at once: consecutive events, plus the projection checkpoints, search documents and blob references that describe exactly those events. | They must become durable together or not at all. |
| **Accepted, durable** | *Accepted*: the store has taken a batch and fixed its order. *Durable*: the transaction that holds it has committed and been synced to disk. | A producer must not wait for the disk on every event, but must be able to demand durability before a side effect. |
| **Flush barrier** | A call that returns only when everything accepted before it is durable. | It is the only call that promises survival. |
| **Durability checkpoint** | A flush barrier placed by policy before a model request, before a tool body, and at a step boundary. | The record of a request must be on disk before its side effect happens. |
| **Instance, instance lock** | An instance is one running store in one `dsh` process, with a random id. Its instance lock is an operating-system lock on a file of its own, held for the instance's whole life. | The kernel releases the lock when the process dies, which is the only proof of death that needs no timer. |
| **Epoch** | A counter in the session's row, raised every time write ownership is taken. | A writer that lost ownership still holds the old number, and its next append is refused inside the transaction. |
| **Takeover** | Taking write ownership of a session whose row names another instance. | Routine once several processes share a home. |
| **Layout version, payload version, export format number** | Three separate integers: the shape of the database tables; the shape of one event type's `data`, stored with each row; the shape of an export file. | A change to one event type must not force a change to every stored event. |
| **Upcaster** | A registered function that turns one event type's payload at version N into version N+1, applied when a row is read. | Old rows stay readable and are never rewritten. |
| **Persisted-type inventory** | A committed file with one fingerprint per persisted type and version, checked by a test. | A persisted type must not change by accident. |
| **Tombstone** | A session row marked `deleting`: invisible to listing and refused on open, while its rows are removed in chunks. | A large deletion must survive a crash half-way. |
| **Duty lease** | The right of one instance to perform a once-per-home job, held like a session lease. | Retention sweeps and similar work must not run in every process. |
| **Generation, migration edge** (upstream only) | One file per format version of a session; a converter from version N to N+1. | Explains what the Go design does not carry. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.session.session-persistence` | `dsh-session-persistence` | definition | The abstract `ctx.sessionPersistence` service, the handle contract, error classes, stored-record validation and two shared contract suites (`packages/session/session-persistence/README.md:83`). |
| `UP-PKG-packages.session.session-persistence-jsonl` | `dsh-session-persistence-jsonl` | provider (shipped) | One directory per session with one file per format generation, Zstandard frames, a kernel write lock, torn-tail recovery, and migration of old generations on open (`packages/session/session-persistence-jsonl/README.md:59-95`). |
| `UP-PKG-packages.session.session-persistence-sqlite` | `dsh-session-persistence-sqlite` | provider (optional) | One SQLite database for all sessions, importing existing JSONL history on first open (`packages/session/session-persistence-sqlite/README.md:42`, `:55`). See §3.3. |
| `UP-PKG-packages.session.session-format` | `dsh-session-format` | other (library) | The stage machinery that pushes each stored row once through a chain of adjacent migrations (`packages/session/session-format/README.md:12`). |
| `UP-PKG-packages.session.session-format-catalog` | `dsh-session-format-catalog` | other (library) | The build-time list of codecs and migration edges from format 0 to the current one; mounted plugins cannot change it (`packages/session/session-format-catalog/README.md:12`). |
| `UP-PKG-packages.session.session-format-v0-to-v1` | `dsh-session-format-v0-to-v1` | other (migration) | Frozen reader of format 0 and the identity edge to 1; refuses every unknown event type. |
| `UP-PKG-packages.session.session-format-v1-to-v2` | `dsh-session-format-v1-to-v2` | other (migration) | Folds stream chunks into assistant events and renumbers every event. |
| `UP-PKG-packages.session.session-format-v2-to-v3` | `dsh-session-format-v2-to-v3` | other (migration) | Moves the system prompt into `system/message` events and renumbers again. |
| `UP-PKG-packages.session.session-format-v3-to-v4` | `dsh-session-format-v3-to-v4` | other (migration) | Turns tool results into tool-role messages; needs facts from child sessions; also holds the format 4 codec. |
| `UP-PKG-packages.session.session-checkpoint-policy` | `dsh-session-checkpoint-policy` | consumer (policy) | Three listeners that flush before a model request, before a top-level tool body and at each step boundary (`packages/session/session-checkpoint-policy/src/index.ts:64`, `:70`, `:79`). |
| `UP-PKG-packages.session.session-log-deepseek` | `dsh-session-log-deepseek` | consumer (vendor) | Attaches the not-yet-accepted part of the session log to requests to the official DeepSeek API, on by default (`packages/session/session-log-deepseek/src/index.ts:60-61`). |

### 3.2 Behaviour

**The seam.** `ctx.sessionPersistence` has five methods: `create`, `open(id, 'read' | 'write')`, `stat`, `list` and a service-wide `flush`. `create` and `open` return a handle with `read`, `append`, `flush` and `close` (`packages/session/session-persistence/README.md:36-51`). An append is "accepted, ordered, and visible"; only a resolved flush promises survival (`docs/subsystems/persistence.md:67-91`). One handle type serves both kinds of access, and a write on a read handle fails at run time (`docs/subsystems/persistence.md:11`).

**Who writes.** The agent loop acquires the write handle before it publishes a session. The backend listens once to `session/event`, `session/flush` and `session/disposed` and routes each event by session id into that handle's write-behind buffer, which waits at most 200 ms before writing (`packages/session/session-persistence/README.md:59`, `packages/session/session-persistence-jsonl/src/storage.ts:37`). A session for which nobody acquired a handle persists nothing (`packages/session/session-persistence/README.md:154`).

**Checkpoints.** The policy plugin has no state. It delays construction of the provider stream until the session is flushed, flushes before a top-level tool body (a nested call skips it, `packages/session/session-checkpoint-policy/src/index.ts:71`), and flushes at `agent/pre-step`. A failed flush means the adapter or tool body does not run (`packages/session/session-checkpoint-policy/README.md:48-52`).

**Files and formats.** The shipped backend keeps `session.vN.jsonl[.zstd]` files, never rewrites a committed one, and publishes a new generation beside the old one when the format changes (`packages/session/session-persistence-jsonl/README.md:59-75`). The writer is at format 4 (`packages/core/session/src/types.ts:90`), first published in `dsh-v0.2.0-rc.2` (`docs/session-format-status.md:44-47`). A read open migrates an old file in memory; only a write open publishes the successor (`packages/session/session-persistence-jsonl/README.md:89`). The six format packages total 7,485 source lines ([../upstream/session-and-persistence.md](../upstream/session-and-persistence.md) §6.3).

**Write ownership.** A write handle holds a kernel lock for its whole life: `flock` on `session.lock` on POSIX and a named semaphore on Windows. The kernel releases it when the holder dies. A live but stuck holder keeps it, and there is deliberately no expiry (`packages/session/session-persistence-jsonl/src/lease.ts:1-13`).

**Vendor upload.** The upload plugin injects `deepseekLlmApiExtensions` and `sessions` (`packages/session/session-log-deepseek/src/index.ts:44`) and registers the request field `dsh_session_log`. Each request carries the longest run of events after the accepted watermark that fits `maxBytes` (8 MiB). After HTTP 2xx it appends `session-log-deepseek/delivery-accepted` (`src/index.ts:253`). The base bundle mounts it (`packages/bundle/base/cordis.patch.yml:43-44`), so a user who changes nothing uploads message text, tool arguments and results and workspace paths (`.agents/notes/implemented/architecture/2026-09-14-session-log-upload-default.md:28`).

### 3.3 Is the SQLite session backend removed? No

An upstream note marked "implemented" says the package, its tests and its configuration "are absent" (`.agents/notes/implemented/simplification/2026-08-30-jsonl-only-session-persistence.md:17`). At the pinned commit the opposite is true:

- The package exists at version `0.2.2-rc.5` with ten source files (`packages/session/session-persistence-sqlite/package.json`).
- The command-line application depends on it (`apps/cli/package.json:66`), and the product policy lists it as `optional`, "selected through global or profile Cordis patches" (`scripts/product-package-policy.ts:35`).
- It has a configuration catalogue entry (`docs/config-catalog.md:3427-3446`), and tests in other packages import it (for example `packages/core/agent-loop/tests/fork-source.spec.ts:11`).
- No shipped bundle mounts it: the bundles mount only the JSONL backend (`packages/bundle/base/cordis.patch.yml:142-143`).
- The subsystem page contradicts itself: line 7 says the repository "ships" the JSONL provider "as its provider", while line 346 lists the SQLite provider as shipped (`docs/subsystems/persistence.md:7`, `:346`).

Inference: the note records a removal that was later reversed, and the package was rebuilt in a different shape, because the present package depends on the JSONL backend and imports its history, which the note never mentions. The checkout holds a single commit, so the history could not be read to confirm the order of events. **The package is present and optional; the note is stale.** This is recorded as `UP-DEBT-found.A05.1`.

### 3.4 Failure, retry, rollback and cleanup upstream

- **Crash during a write.** A torn final line or frame is discarded on read and truncated by the next writer (`packages/session/session-persistence-jsonl/README.md:79`). The SQLite backend relies on the transaction.
- **A failed background write.** The events stay buffered, automatic writing pauses, and the next explicit flush retries and reports (`docs/subsystems/persistence.md:106`).
- **A turn cut by a crash.** The store returns the log as written. On resume the agent loop computes closers with `interruptedTurnClosers` (`packages/core/session/src/repair.ts:209`) and appends them (`packages/core/agent-loop/src/index.ts:809`): an error result per unanswered tool call, coded `TOOL_NOT_STARTED` or `TOOL_OUTCOME_UNKNOWN` (`repair.ts:15`, `:18`), a missing `step/end`, and `turn/end` as interrupted.
- **A failed migration.** The source file is untouched and nothing is published.
- **Never cleaned up.** Session files, old generations and lock files are never deleted; there is no deletion API (`packages/session/session-persistence/README.md:155`).
- **Left uncertain.** Whether a tool that was running took effect; an upload accepted just before a crash is sent again (`packages/session/session-log-deepseek/README.md:93`).

## 4. Upstream decisions dissected

The first fifteen rows are the decision notes handed to this area; paths are under `.agents/notes/`. The remaining rows are decisions visible only in code or READMEs.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `implemented/architecture/2026-08-10-session-log-version-mechanism` | One integer for the whole log; refuse a newer log and name the direction; an unknown event type refuses unless marked `ignorable`. | change | The refusal rules are kept from the first release, because a shipped reader cannot be fixed later. The single integer is replaced: it forces a rewrite of every event when one type changes. | [DH-ADR-STORE-5](#dh-adr-store-5) |
| `implemented/architecture/2026-08-27-handle-based-session-persistence` | Five service methods returning per-session handles; ownership is the handle; repair moved out of storage. | keep, with two changes | Handles map to Go interfaces with `Close`. Changed: readers and writers are two types, and no listener routes events into storage. | [DH-ADR-STORE-1](#dh-adr-store-1) |
| `implemented/architecture/2026-08-31-alpha-historical-unknown-event-refusal` | A migration refuses every unknown event, even an `ignorable` one, because it cannot prove an opaque payload still means the same. | keep, moved | There is no format 0 to migrate. The rule lives on in the one place the Go design converts between formats: the upstream import. | [DH-ADR-STORE-11](#dh-adr-store-11) |
| `implemented/architecture/2026-08-31-released-session-format-migrations` | Streaming stages per version step, after a whole-file migration exhausted 16 GB on a 116 MB session. | drop the chain, keep the lesson | No chain exists. Its lesson, bounded memory for any conversion, binds the layout migrations, the export and the importer. | [DH-ADR-STORE-5](#dh-adr-store-5), [DH-ADR-STORE-11](#dh-adr-store-11) |
| `implemented/architecture/2026-09-05-read-only-session-migration-preparation` | A read open converts in memory and never writes; only a write open publishes. | keep, simpler | Upcasters run on read and stored rows are never rewritten by anyone, so there is no publication step at all. | [DH-ADR-STORE-5](#dh-adr-store-5) |
| `implemented/architecture/2026-09-14-session-log-upload-default` | Upload is on by default; the switch is read per request; re-enabling sends what was not accepted. | change | The owner decided the default is off ([DH-ADR-9](../design.md#dh-adr-9)). The per-request read and the resume from the watermark are kept. | [DH-ADR-STORE-10](#dh-adr-store-10) |
| `proposed/architecture/2026-07-29-durable-last-activity-index` | Proposal, not built: store the last human prompt time where listing reads it. Blocked upstream because a JSONL header cannot be rewritten. | adopt | The session row holds it and the append transaction updates it, so it cannot lag the log. | [DH-ADR-STORE-2](#dh-adr-store-2) |
| `proposed/architecture/2026-09-10-session-data-compatibility` | Proposal: old sessions keep opening; old generations are never overwritten; derived indexes are rebuildable and never the recovery authority. | change scope | The promise is given for the Go store's own data. Upstream's data is covered only by the import command. | [DH-ADR-STORE-5](#dh-adr-store-5), [DH-ADR-STORE-11](#dh-adr-store-11) |
| `implemented/architecture/2026-09-17-native-v4-read-validation` | One mandatory pass checks relationships between events before a session is exposed; an unfinished tail may stay open. | keep, split | The store checks structure (order, known types, known versions). The relationship pass needs event meaning, so it is the session's (area A01) and runs once before a session is published. | [DH-ADR-STORE-1](#dh-adr-store-1) |
| `implemented/architecture/2026-09-24-bounded-session-log-upload` | The upload is capped at 8 MiB per request; an oversized event blocks the upload but never fails the model request. | keep | An unbounded request body is wrong in any runtime. | [DH-ADR-STORE-10](#dh-adr-store-10) |
| `implemented/feature/2026-08-31-cross-process-session-write-lease` | The kernel arbitrates: a lock held for the life of the write handle, released on death, no expiry. | change | The kernel lock stays as proof that a holder is alive. Ownership itself moves into the database with an epoch, which removes the stuck-holder and lost-lock-file limits. | [DH-ADR-STORE-4](#dh-adr-store-4) |
| `implemented/process/2026-10-08-session-reader-compatibility-review` | The version changes only when existing readers would misread the new data. | keep, per type | The same test decides whether one event type's payload version changes. | [DH-ADR-STORE-5](#dh-adr-store-5) |
| `implemented/simplification/2026-08-30-jsonl-only-session-persistence` | One first-party store behind a neutral contract; the SQLite provider removed. | keep the rule | One first-party store, and it is SQLite ([DH-ADR-PERSISTENCE-3](../upstream/session-and-persistence.md#dh-adr-persistence-3)). The note's factual claim is stale (§3.3). | [DH-ADR-STORE-1](#dh-adr-store-1) |
| `rejected/simplification/2026-06-20-fold-session-persistence-interface` | Rejected: keep the storage contract in its own package. | keep the rejection | The session (consumer), the SQLite store (provider) and the test store all import the contract; a consumer must not import a provider. | [DH-ADR-STORE-1](#dh-adr-store-1) |
| `rejected/simplification/2026-06-20-truncate-interrupted-turns` | Rejected: do not throw away an interrupted final turn. | keep the rejection | A turn can hold hours of durable work. | [DH-ADR-PERSISTENCE-10](../upstream/session-and-persistence.md#dh-adr-persistence-10), [DH-D-STORE-4](#dh-d-store-4) |
| Code: 200 ms write-behind window (`packages/session/session-persistence-jsonl/src/storage.ts:37`) | Batch live events before writing. | drop | A writer goroutine that commits as soon as it is free batches by itself, without a timer. | [DH-ADR-STORE-3](#dh-adr-store-3) |
| Code: deferred materialization (`docs/subsystems/persistence.md:102`) | A created session leaves nothing until its first append. | keep | The session row is inserted by the first batch. | [DH-ADR-STORE-2](#dh-adr-store-2) |
| Code: frames of 64 events and linked segments in the SQLite backend (`packages/session/session-persistence-sqlite/README.md:40`, `:49`) | Compress groups of events; a fork references its parent's prefix. | drop | One row per event makes paging and deletion plain; a fork copies its prefix inside the database. | [DH-ADR-STORE-2](#dh-adr-store-2) |
| Code: group commit with one savepoint per batch (`packages/session/session-persistence-sqlite/README.md:63`) | Batches queued together share a transaction; one failing batch does not fail its peers. | keep | Same mechanism in the Go writer. | [DH-ADR-STORE-3](#dh-adr-store-3) |
| Code: append-only triggers and identity pragmas (`packages/session/session-persistence-sqlite/src/sqlite.ts:32-33`, `:305-306`) | The database itself refuses a rewrite of history and recognises its own file. | keep | Extended so that deletion of a whole session remains possible. | [DH-ADR-STORE-2](#dh-adr-store-2) |
| Code: nested tool calls skip the checkpoint (`session-checkpoint-policy/src/index.ts:71`) | Only top-level calls are flushed before their body. | keep | After a crash the outer call is reported as "outcome unknown", which covers what a script did through the tool command line. Area A03 confirms. | [DH-D-STORE-4](#dh-d-store-4) |
| Code: lease and Windows binding copied into both backends (`session-persistence-sqlite/src/lease.ts:30-31`) | Duplicate instead of sharing a helper. | drop | One store, one implementation. | [DH-ADR-STORE-4](#dh-adr-store-4) |

## 5. Tech debt and its resolution

Unit IDs are shortened: `limit.<package>.<n>` stands for `UP-DEBT-limit.packages.session.<package>.<n>`, and `marker.<n>` for `UP-DEBT-marker.packages/session/session-log-deepseek/src/index.ts:<n>`. Evidence is the line of the bullet in `packages/session/<package>/README.md`.

| Debt ID | What it is | Evidence | Resolution in Go |
|---|---|---|---|
| `limit.session-checkpoint-policy.1` | A durable record of the intent to run a tool, not of its effect. | `:114` | **Carried.** No local mechanism can observe an external effect. The call id is handed to every tool body as an idempotency key (area A03), and recovery says "outcome unknown". |
| `limit.session-checkpoint-policy.2` | No checkpoint inside a running model attempt; a crash loses frames not yet settled. | `:115` | **Carried.** Logging every chunk would put the disk in the stream path. Nothing model-visible is lost; the user loses text that was on screen ([../design.md §5](../design.md)). |
| `limit.session-checkpoint-policy.3` | A call with no result is recorded as unknown, never retried. | `:116` | **Carried**, on purpose: retrying a call that may have taken effect is the worse error ([DH-ADR-PERSISTENCE-10](../upstream/session-and-persistence.md#dh-adr-persistence-10)). |
| `limit.session-persistence.6` | An interrupted turn is closed, never continued. | `:157` | **Carried**, on purpose: continuing would repeat a model request whose stream is lost. [DH-D-STORE-4](#dh-d-store-4) specifies the closers. |
| `limit.session-log-deepseek.1` | An upload accepted just before a crash is sent again. | `:93` | **Carried, narrowed.** At-least-once is inherent to an acknowledgement over HTTP. The acceptance record is committed as soon as the writer is free, not after a 200 ms window, and each event carries session id and `seq`, so a receiver can remove duplicates. |
| `limit.session-log-deepseek.3` | One event larger than `maxBytes` blocks the upload behind it. | `:95` | Resolved on one condition (register entry U-K1-11): the oversized event is sent as an elision record that keeps the log contiguous by number, and the upload continues ([DH-ADR-STORE-10](#dh-adr-store-10), [DH-D-STORE-9](#dh-d-store-9)). The condition is the vendor accepting such a line, a recommended default not confirmed by the owner and settled by [DH-E-STORE-4](#dh-e-store-4); if the vendor refuses it, the upload stops at that event as before and the settings page shows the event and its size. |
| `limit.session-log-deepseek.2`, `marker.199` | No field when a request has no live session; the meaning of absence is undefined. | `:94`, `src/index.ts:199` | Resolved: the contract states it. A request not attributed to a session this process holds carries no field and records nothing ([DH-D-STORE-9](#dh-d-store-9)). |
| `limit.session-log-deepseek.4` | A provider that rejects the field fails the model request, every time. | `:96` | Resolved: a rejection opens a breaker. The uploader stops attaching the field for that session and reports it; the transport resends that request once without contributions ([llm.md §6.3](llm.md#63-ownership-failure-retry-rollback-and-cleanup), [DH-ADR-STORE-10](#dh-adr-store-10)). |
| `marker.258` | No immediate checkpoint after an accepted upload. | `src/index.ts:258` | Resolved: the writer commits on demand, so the record is durable without a separate checkpoint ([DH-ADR-STORE-3](#dh-adr-store-3)). |
| `limit.session-format.1`, `limit.session-format-v1-to-v2.2` | A migration keeps the whole result and a renumbering table in memory. | `:101`; `:107` | Resolved: no migration chain ([DH-ADR-PERSISTENCE-5](../upstream/session-and-persistence.md#dh-adr-persistence-5)). Reads are paged from the database and `seq` is never renumbered ([DH-D-STORE-2](#dh-d-store-2)). |
| `limit.session-format.2` | Only whole-log integer versions; no way to evolve one type. | `:102` | Resolved: a payload version per event type with upcasters ([DH-D-STORE-5](#dh-d-store-5)). |
| `limit.session-format-catalog.1`, `.2` | The migration list is closed at build time; nobody outside can add an edge. | `:99`, `:100` | Resolved: there is no catalogue of formats. Upcasters are registered with their event type, which is first-party Go code ([DH-D-STORE-5](#dh-d-store-5)). |
| `limit.session-format-v0-to-v1.1`, `limit.session-format-v1-to-v2.1` | Unknown plugin events refuse the migration, even when `ignorable`. | `:102`; `:106` | Resolved for the store: an unknown `ignorable` record is kept byte for byte, because no stored row is ever converted ([DH-D-STORE-5](#dh-d-store-5)). The importer keeps the strict rule on purpose. |
| `limit.session-format-v0-to-v1.2`, `limit.session-format-v1-to-v2.3`, `limit.session-format-v2-to-v3.2` | An edge neither publishes files nor offers a downgrade. | `:103`; `:108`; `:197` | Resolved: no generations or successor files exist ([DH-ADR-PERSISTENCE-3](../upstream/session-and-persistence.md#dh-adr-persistence-3)). The Go rule for its own layout is in [DH-ADR-STORE-5](#dh-adr-store-5): forward only, with a backup. |
| `limit.session-format-v2-to-v3.1` | The old preset id `code` is ambiguous in old files. | `:196` | Resolved: formats 0 to 3 are never read ([DH-ADR-11](../design.md#dh-adr-11)). |
| `limit.session-format-v3-to-v4.1`, `.4` | The converter refuses source forms it does not cover, such as nested tool-result wrappers. | `:333`, `:336` | Resolved: the store has no converter. The importer reads only files already at format 4 and refuses a whole file it cannot map ([DH-D-STORE-10](#dh-d-store-10)). |
| `limit.session-format-v3-to-v4.2`, `.3` | The accepted format 4 baseline and the readers a future format 5 would need. | `:334`, `:335` | Resolved: there is no whole-log version to finalise; each type evolves alone under the inventory test ([DH-D-STORE-5](#dh-d-store-5)). |
| `limit.session-format-v3-to-v4.5` | Prefixed extension fields are kept but mean nothing until a consumer understands them. | `:337` | Resolved: the importer maps a field it knows and refuses a file with one it does not ([DH-D-STORE-10](#dh-d-store-10)). |
| `limit.session-format-v3-to-v4.6`, `.7`, `.8` | A parent needs its child logs; unknown child modes; nothing across storage roots. | `:338`, `:339`, `:340` | Resolved for the store: a parent row and its children are ordinary rows of one database, linked by a column. The importer imports a root as a whole and reports children it did not find ([DH-D-STORE-10](#dh-d-store-10)). |
| `limit.session-persistence-jsonl.1`, `.2` | Migration keeps the configured compression; the old flat layout does not load. | `:166`, `:167` | Resolved: one database, no per-root encoding and no file layouts ([DH-ADR-PERSISTENCE-3](../upstream/session-and-persistence.md#dh-adr-persistence-3)). |
| `limit.session-persistence-jsonl.3` | Compressed logs cannot be read line by line. | `:168` | Resolved: the export is plain lines, optionally gzip, and the database is stock SQLite ([DH-D-STORE-7](#dh-d-store-7)). |
| `limit.session-persistence-jsonl.4`, `limit.session-persistence.4` | Nothing deletes a session. | `:169`; `:155` | Resolved: delete and retention ([DH-D-STORE-6](#dh-d-store-6)). |
| `limit.session-persistence-jsonl.5`, `limit.session-persistence.1` | A stuck holder blocks writers; removing the lock file forfeits exclusion; locks are unreliable on network filesystems; the Windows lock name is per login session; the seam itself promises nothing across processes. | `:170`; `:152` | Resolved: the epoch check makes an explicit takeover safe, a lost lock file cannot corrupt a log, the Windows lock is a file lock, a network home is refused, and the lease is part of the contract suite ([DH-D-STORE-3](#dh-d-store-3), [DH-ADR-STORE-8](#dh-adr-store-8)). |
| `limit.session-persistence-jsonl.6` | cordis may remove consumers before the provider has joined their cleanup. | `:171` | Resolved: [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1) makes unload complete and ordered; the store's own drain is in [DH-D-STORE-4](#dh-d-store-4). |
| `limit.session-persistence-jsonl.7` | Publishing a new file needs hard links on POSIX. | `:172` | Resolved: no file is published; a session is created by a transaction ([DH-D-STORE-2](#dh-d-store-2)). |
| `limit.session-persistence-jsonl.8` | Writing on POSIX needs a prebuilt native add-on for `flock`. | `:173` | Not applicable: Node has no file-lock call. Go reaches it through `golang.org/x/sys` (`DH-LIB-NODEFSXSYS`). |
| `limit.session-persistence-sqlite.1`, `.2`, `.3` | The first read imports and so writes; kept JSONL files go stale; every writer must use this provider. | `:120`, `:121`, `:122` | Resolved: nothing is imported on open and there is one store. Import is an explicit one-way command ([DH-ADR-11](../design.md#dh-adr-11), [DH-D-STORE-10](#dh-d-store-10)). |
| `limit.session-persistence-sqlite.4` | A future event-format change needs its own database migration. | `:123` | Resolved: upcasters on read; stored rows stay as written ([DH-D-STORE-5](#dh-d-store-5)). |
| `limit.session-persistence.2` | Reloading the backend under live sessions makes their writers fail. | `:153` | Resolved: the store is a service the sessions depend on, so they unload first and close their writers; the store has no option that needs a reload ([DH-D-STORE-4](#dh-d-store-4)). |
| `limit.session-persistence.3` | A session persists only if someone acquired a handle; `create` plus `flush` alone stores nothing. | `:154` | Resolved: no listener path exists. Events reach storage only through the writer a session holds ([DH-ADR-STORE-1](#dh-adr-store-1); area A01 confirms the constructor). |
| `limit.session-persistence.5` | `list()` returns everything, unpaged. | `:156` | Resolved: a paged, filtered query on indexed columns ([DH-D-STORE-2](#dh-d-store-2)). |
| `UP-DEBT-found.A05.1` | An "implemented" note says the SQLite backend is absent while the package exists and the CLI depends on it. | §3.3 | Resolved: one store, and the persisted catalogue is generated from the running registry, so a document cannot disagree with the code ([DH-D-STORE-5](#dh-d-store-5)). |
| `UP-DEBT-found.A05.2` | The lease and the Windows binding are copied into both backends. | `packages/session/session-persistence-sqlite/src/lease.ts:30-31` | Resolved: one implementation ([DH-D-STORE-3](#dh-d-store-3)). |

The carried rows (five in the ledger as the debt review left it; the sixth, the oversized upload event, is resolved by the elision record of [DH-ADR-STORE-10](#dh-adr-store-10)) name stage [DH-S-47](../impl.md#dh-s-47), the known limits and scope boundaries of the core areas.

## 6. Go design

### 6.1 Shape

| Package (under `src/harness/internal/session`) | What it holds | Why it is separate |
|---|---|---|
| `store` | The contract: `Store`, `Reader`, `Writer`, `Batch`, `Record`, `Summary`, error values; the sub-packages `storetest` (the conformance suite) and `memstore` (an in-memory store for tests). | The session (area A01) and every provider import it; an import-direction test stops a consumer importing a provider. |
| `store/sqlite` (built). Proposed: two packages, `sqlitedb` (the driver part) and `sqlitestore` (the session part) | The only package that imports the SQLite driver (`DH-LIB-SQLITE-R9`), and `store.Store` on top of it: the open sequence, both connection pools, the one write connection that every write of the instance uses in turn, layout migrations, the error table, the instance lock, duty leases, the change counter, and the session tables, the append transaction, leases, reads, listing, fork and deletion. Proposed: a writer goroutine and a backup in the driver part. | Areas A07 and A13 keep their tables in the same database without touching the driver: `sqlite.Store.WriteTx(ctx, fn)` runs one write transaction on the instance's write connection and `sqlite.Store.ReadTx(ctx, fn)` one read transaction. Proposed: `sqlitedb.Tx(ctx, fn)` on the writer goroutine and `sqlitedb.Read(ctx, fn)`. |
| `persisted` | Payload-version decoding, the upcaster rule, the inventory file, the recorded rows and their test. | The one place a persisted type is checked. |
| `checkpoint` | The durability checkpoint policy. | A composition may replace the schedule. |
| `export` | The line-per-event export codec and its import. | Used by commands, routes, fixtures and the importer. |
| `logupload` | The opt-in vendor upload. | Off by default; removable. |
| `upstreamimport` | The deferred reader and translator for upstream format 4. | Never linked into the store's read path. |

| Component | Provides | Injects |
|---|---|---|
| `session.store` | Service key `store.Key`, type `store.Store` (replaces `ctx.sessionPersistence`) | The harness home path and the logger from boot |
| `session.checkpoint` | Service key `checkpoint.Key` (`Policy`: `Before(ctx, session, point) error`, with points `attempt`, `tool-body` and `step-end`) | `session.Key` (A01). The driver (A02) calls it before each model attempt and at each step end; the tool pipeline (A03) calls it at stage 5 for a root call |
| `session.logupload` | Service key `logupload.Key` (replaces `ctx.sessionLogDeepSeek`) | The session service (A01), the request-contribution registry `transport.ContributionsKey` (A04), the data-egress switch (A18) |

| Interface | Method | Meaning |
|---|---|---|
| `Store` | `Create(header, options) (Writer, error)` | A new session and its writer. `options.ForkOf` names a parent and a cut. |
| | `OpenWrite(id, options) (Writer, error)` | Take write ownership. `options.Takeover` is `IfFree` (default) or `Force`. |
| | `OpenRead(id) (Reader, error)` | Observe without ownership. |
| | `Stat(id)`, `List(query) (Page, error)`, `Instances()` | A summary of one session (header, `next_seq`, `imported_from`, lease epoch, and the lease holder's instance, process id, kind and liveness); a filtered page of summaries with a cursor; the live instances of the home with their declared roles. None reads events. |
| | `Delete(id, options) error` | Remove a session ([DH-ADR-STORE-7](#dh-adr-store-7)). |
| | `Flush() error` | A barrier over every writer of this instance. |
| | `FileRequest(id, kind, drafts)`, `Requests(id)`, `WatchRequest(request)` | File a log-only change for a session another live process holds; the pending requests of a session this process holds; the outcome of one request ([DH-ADR-STORE-12](#dh-adr-store-12)). |
| | `Follow(id, afterSeq) (Subscription, error)`, `Changes() (Subscription, error)` | Durable events of a session, whichever process writes it. `Changes` is a coarse signal that another process committed something to the home (a new, changed or deleted session), from the same poll; a session list re-reads on it. |
| `Reader` | `Header()`, `Read(from, limit, options)`, `ReadSeqs(seqs, options)`, `Checkpoints()`, `Close()` | `Read` returns a window of records in `seq` order; `ReadSeqs` returns the records at a given list of positions (the session core restores its visible history nodes with it); `options.WithAux` also loads the second payload column. A reader in the writing process (the `Writer`, or `OpenRead` on a session that process holds) sees every accepted record, flushed or not; a reader in any other process sees durable records only. |
| `Writer` | all of `Reader`, plus `Epoch()`, `Append(batch) error`, `Flush() error` | Built: `Append` commits the batch with a full sync before it returns, so an event is durable once `Append` returns (see "The append transaction" in §6.2). Proposed: `Append` means accepted and ordered, not yet durable. `Flush` is the barrier for this session. `Close` flushes, releases the lease and cannot be cancelled. |

Every method takes a `context.Context` first (`DH-LIB-SQLITE-R8`). This table is the one storage contract behind the session log: area A01's session core calls it directly and declares no second one ([core-session.md §6.1](core-session.md#61-shape) lists the clauses it relies on, each stated here). A `Record` is A01's `event.Record`: the envelope fields, the payload version and the two payload parts as immutable Go strings holding the stored bytes. The store never re-encodes them, and neither side writes to a record after hand-over. Decoding into a typed struct is done by `persisted.Decode` against A01's registry. The store writes a checkpoint when it is handed one: the session core decides for its `core/*` keys and area A06's registry for every other key.

**As built (M1).** The first package table names the SQLite package that exists: one package, `src/harness/internal/session/store/sqlite`, where this design proposed `sqlitedb` and `sqlitestore`; this correction leaves its other rows unchanged. The `Writer` row states the built append, which commits before it returns. Corrected by plan step 15.3.10 of [the M1 gap-closure plan](../../stories/harness-DSH-M1/gap-closure-plan.md) (gap M1-G-STORE-29); the append wording follows owner decision M1-D-STORE-A (a) in [the M1 decision log](../../stories/harness-DSH-M1/decisions.md).

### 6.2 Contracts

**The database.** One file, `<home>/dsh.db`, with SQLite's `-wal` and `-shm` side files, in WAL mode with `synchronous = FULL`; instance lock files in `<home>/run/`. These are the built paths of area A07's home layout (`storage/home`: `Layout.DB`, `Layout.Run`). Proposed: `<home>/store/dsh.sqlite` and `<home>/store/run/` (corrected by plan step 15.3.10, gap M1-G-STORE-29). All tables are `STRICT`.

| Table | Key and main columns | Rules |
|---|---|---|
| `hosts` | `instance` (key), `pid`, `kind`, `roles`, `binary_version`, `started_at` | One row per live instance. `roles` is what the instance declared at open (for example `schedule-fire`, read by area A13). A row whose lock can be taken belongs to a dead process and is swept. |
| `sessions` | `id` (key); the fields of area A01's `Header` as columns (`created_at`, `cwd`, `origin`, `creator`, `fork_source`, `fork_through`, `inherited_count`, `delegation_depth`, `preset`); `next_seq`; `lease_epoch`; `lease_holder` (an instance or null); `state` (`active` or `deleting`); `last_event_at`; `last_prompt_at`; `bytes`; `imported_from` | Indexed for listing by `last_prompt_at`, `cwd` and `creator`. `next_seq` is the number of stored events. The two fork columns follow [DH-ADR-SESSION-11](core-session.md#dh-adr-session-11), a recommended default not confirmed by the owner. |
| `events` | `(session, seq)` (key, no row id); `time`; `type`; `pv` (payload version); `ignorable`; `surface` (0 log-only, 1 append, 2 replace); `env` (JSON: replaced range and cited sources, or null); `codec`; `data`; `aux` | A trigger aborts every `UPDATE`. A trigger aborts a `DELETE` unless the session's `state` is `deleting`. |
| `checkpoints` | `(session, key, state_version)`; `seq`; `state` | The newest checkpoint per fold key and fold version, so two binaries with different fold versions never replace each other's rows. Keys under `core/` are reserved to the session core (A01). Derived data: replaced in place. |
| `session_summary`, `turn_index`, `event_class`, `event_link` | `session`, then `turn` or `seq` | Area A06's derived view rows ([session-views.md §6.2](session-views.md#62-contracts) owns their columns and content); this area creates them, commits them with the batch and deletes them with the session. **Not built in M1:** by owner decision M1-D-STORE-C (a) the four built-in views (list summary, turn index, statistics, event index) keep their state as checkpoint documents, one `checkpoints` row per session, view and fold version, and these tables are revisited at [DH-S-23](../impl.md#dh-s-23) (plan step 15.3.10, gap M1-G-STORE-24). |
| `search_docs`, `event_fts` | `search_docs(id, session, seq, type, time)`; `event_fts` is an FTS5 table whose row id is `search_docs.id`, tokenizer `unicode61` ([DH-ADR-SQLITE-3](../libs/sqlite.md#dh-adr-sqlite-3)) | Area A06 supplies the text and owns the queries; whether the index also stores the text is A06's choice after measurement M3 of [../libs/sqlite.md](../libs/sqlite.md). |
| `blob_refs` | `(session, seq, ref)` | Which stored object an event references. Area A07 owns its meaning and the collector. |
| `duties` | `name` (key), `holder`, `epoch`, `last_run_at` | Once-per-home maintenance: retention, blob collection (A07), rebuild sweeps. Schedules do not use it: a due occurrence is claimed row by row ([DH-ADR-JOBS-7](jobs-schedule-goal-plan.md#dh-adr-jobs-7)). |
| `requests` | `id` (key), `session`, `created_at`, `requester` (an instance), `kind`, `drafts` (JSON: the log-only drafts), `state` (`pending`, `applied`, `refused`, `expired`), `seq` (first `seq` when applied), `error`, `settled_at` | A log-only change handed to the holder of a session by a process that does not hold it ([DH-ADR-STORE-12](#dh-adr-store-12)). Indexed by `(session, state)`. Deleted with the session; settled rows are pruned by the retention duty after `Requests.Keep`. |

`codec` is 0 (JSON text) in the first layout. The column exists so that compression can be added without a layout change; an unknown codec refuses the session. `aux` holds the part of a payload that history reads do not need (the raw provider stream of an assistant event); the event type declares which field that is. The identity pragmas `application_id` and `user_version` mark the file and its layout version.

**The append transaction.** A session's append lock (area A01) assigns `seq`, folds projections and hands the store a batch. The store's writer goroutine, one per instance, takes every queued batch from every session and runs one transaction:

1. `BEGIN IMMEDIATE`, waiting at most the busy timeout for another process.
2. Per batch, inside its own savepoint, a guard: `UPDATE sessions SET next_seq = next_seq + n, ... WHERE id = ? AND lease_holder = me AND lease_epoch = mine AND next_seq = first AND state = 'active'`. Exactly one row must change. Otherwise the savepoint is rolled back and that batch fails with `session/lease-lost`, `session/seq-conflict` or `session/deleted`; its peers continue.
3. Insert the batch's events and blob references. Then, inside a nested savepoint, write the derived rows handed over with the batch: checkpoints, search documents and A06's view rows. A failure there rolls back the derived rows only: the events commit and the affected folds read as `behind` until they catch up, so derived state is never ahead of the log and never costs an event.
4. `COMMIT`. Then each batch's waiters are released and flush barriers behind it are woken.

The guard is the contiguity check and the lease check in one statement, so a gap, a duplicate and a stale writer are all impossible by construction. The first batch of a new session also inserts its `sessions` row; a fork's first transaction copies the parent's prefix with one `INSERT ... SELECT`, together with the prefix's blob references.

**As built: the synchronous writer** (owner decision M1-D-STORE-A (a) in [the M1 decision log](../../stories/harness-DSH-M1/decisions.md); plan step 15.1.4 of [the M1 gap-closure plan](../../stories/harness-DSH-M1/gap-closure-plan.md), gap M1-G-STORE-4). The store has no writer goroutine and no queue. Each instance has one write connection, and every write takes it in turn: `Append` runs one transaction for its own batch and returns only after the commit, which `synchronous = FULL` has synced. So an event is durable once `Append` returns, and nothing is ever accepted but not yet durable. That has two consequences. First, there is no backlog to bound while appends are synchronous: the `MaxBacklogBytes` bound and the `store/backlog` error below belong to the group-commit writer this section proposes, and are not built; `DH-T-STORE-4`'s backlog clause asserts instead that an event is durable once `Append` returns. Second, `Flush` stays the barrier of the contract, but every event it covers has already committed. The writer goroutine above, which groups the queued batches of every session into one transaction, stays the proposal; building it with the 64 MiB bound was option (b) of M1-D-STORE-A, not chosen.

**What "durable" means.** An event is durable when its transaction has committed with `synchronous = FULL`, that is, after SQLite synced the write-ahead file. A durable event survives the death of the process. It survives an operating-system crash or power loss to the extent the filesystem and device honour the sync; that is SQLite's guarantee on the filesystem in use, it cannot be tested locally, and no test here claims it. `Writer.Flush` returns nil only when every event accepted for that session before the call is durable. Within one process a reader sees accepted events at once; another process sees only durable ones.

**The lease.** Rules, in the order they apply:

1. An instance holds an exclusive operating-system lock on `run/<instance>.lock` from open to close, taken with the `fsx` lock primitive of area A08 (`flock` on Unix, `LockFileEx` on Windows). It is never taken on the database file, because POSIX locks on that file are dropped by any open-and-close of it in the same process (`DH-LIB-SQLITE-R11`). Inference: on Windows the locked range lies beyond the end of the empty file, so a program that reads the file is not refused; scenario [DH-E-STORE-1](#dh-e-store-1) checks it.
2. `OpenWrite` and `Create` run in a write transaction. If `lease_holder` is null, or names an instance whose lock file can be locked (its process is dead), the caller sets `lease_holder` to itself and raises `lease_epoch`. Probing happens only inside that transaction, so two processes cannot both conclude the holder is dead.
3. If the holder is alive, the caller gets `session/owned` with the holder's process id, kind and start time, and nothing changes. A second writer in the same process gets the same error from an in-memory table.
4. `Takeover = Force` takes the lease from a live holder. It is an explicit action by the owner of the home, never automatic, and no timer ever triggers it (CF-10 in [../design.md §8](../design.md)).
5. A writer that lost its lease learns it at its next append or flush: `session/lease-lost`. Events it had accepted but not committed are discarded; they were never durable. The store emits `store.LeaseLost`; the session core (A01) closes the session and notifies `session.Closed` with that cause, on which the driver (A02) stops the agent.
6. `Close` clears `lease_holder` and keeps the epoch. Readers never touch a lease.

**Schema evolution per event type** ([DH-ADR-PERSISTENCE-6](../upstream/session-and-persistence.md#dh-adr-persistence-6)). The writer stores the registered current version of the event's type in `pv`. On read: equal to the current version, decode; lower, apply the registered upcasters one step at a time and then decode; higher, refuse the session with `session/newer-payload` ("written by a newer dsh; upgrade"). A type that is not registered refuses the session with `session/unknown-type` unless the row is `ignorable`, in which case the record is returned as raw bytes. Stored rows are never rewritten, and export and fork copy raw bytes, so a field an older binary does not know is not lost by passing through it. The check that stops an accidental change is in [DH-D-STORE-5](#dh-d-store-5).

**As built (M1): the open judges only what lies past the newest checkpoint** (owner decision M1-D-STORE-F in [the M1 decision log](../../stories/harness-DSH-M1/decisions.md); plan step 15.3.1 of [the M1 gap-closure plan](../../stories/harness-DSH-M1/gap-closure-plan.md), gap M1-G-STORE-21). The paragraph above, read as "the open refuses a session that holds an unknown or newer record anywhere in its log", stays as the proposal. Judging every record at open made opening and listing grow with the log (a 100,000-event open took about 3.1 s, and the query's first list page about 540 ms and 726 MB). The built rule keeps the same refusals but moves some of them later:
- `OpenRead` and `OpenWrite` judge the records from the newest checkpoint's `seq` to the head. A session with no checkpoint is judged whole.
- A record below the newest checkpoint is judged when `Read` or `ReadSeqs` returns it, by the same rule (`store.Admit`) and with the same typed errors: `session/unknown-type`, `session/newer-payload` and `session/invalid-event`. No refused record is ever returned.
- `Fork` still judges the whole prefix it inherits; export copies raw bytes as before.

What this weakens: a session whose refused record lies before its newest checkpoint now opens, and the refusal comes at the first read that reaches that record. What it adds: a reader also judges records another process appended after the reader opened. Tests: `TestOpenIsBoundedByTheNewestCheckpoint` (`DH-T-STORE-18`: an open judges 2 records at 100, 1,000 and 10,000 events) and `TestReadsJudgeWhatTheOpenDidNot` (`DH-T-STORE-10`). After the change (Windows): the 100,000-event open about 1.27 s, the query's first list page about 22 ms and 7 MB.

**Config** (replaces `UP-CFG-dsh-session-persistence-jsonl` and `UP-CFG-dsh-session-persistence-sqlite`). None of the upstream fields survives: `root` is the harness home chosen at boot, `compression` becomes the export's gzip option (gzip is the only compression an export uses, [DH-ADR-STORE-9](#dh-adr-store-9)), and `listConcurrency` and `frameEvents` have no counterpart. Defaults are proposals until measurements M1 and M6 of [../libs/sqlite.md](../libs/sqlite.md) exist.

| `store.Config` field | Default | Meaning |
|---|---|---|
| `BusyTimeout` | 5 s (upstream's value, `packages/session/session-persistence-sqlite/src/sqlite.ts:42`) | Longest wait for another process's lock before `store/busy`. |
| `CommitRetries` | 3 | How often the writer retries a transaction that met `busy`. |
| `MaxBacklogBytes` | 64 MiB per session | Proposed: bound on accepted, not yet durable events; beyond it `Append` fails with `store/backlog`. Not built: there is no backlog while appends are synchronous (M1-D-STORE-A (a), "As built: the synchronous writer" above). |
| `ShutdownFlushTimeout` | 10 s | Bound on the final barrier at unload. |
| `FollowInterval` | 250 ms | Poll interval while someone follows a session of another process. |
| `ReadPoolSize` | 4 | Read connections ([DH-ADR-SQLITE-2](../libs/sqlite.md#dh-adr-sqlite-2)). |
| `Retention.MaxAge` | 0 (keep forever) | Age after which an unheld session is deleted by the retention duty. |
| `Requests.TTL`, `Requests.Keep` | 10 min, 7 days | How long a filed request may stay `pending` before it expires; how long a settled row is kept ([DH-ADR-STORE-12](#dh-adr-store-12)). |

`logupload.Config` (replaces `UP-CFG-dsh-session-log-deepseek`): `Enabled`, default **false**, read for each request; `MaxBytes`, default 8 MiB, at least 1.

**Events and errors.** Three live events, all in the notify form of [../design.md §4.6](../design.md): `store.LeaseLost{Session, Epoch}`; `store.Degraded{Reason}` (a session's queue stalled, or the store became unusable); and `store.Deleting{Session, Reader}`, sent after a deletion's tombstone has committed and before its first chunk is removed, so that areas A07 (spill files) and X2 (worktrees) can release what they keep for the session while its folds are still readable. A failing `Deleting` listener is logged and does not stop the deletion; the event is sent again when an interrupted deletion is resumed, so listeners are idempotent. Errors this area adds to the one error table: `store/busy`, `store/unusable`, `store/corrupt`, `store/newer-layout`, `store/foreign-database`, `store/no-wal` (proposed as `store/unsupported-filesystem`; built name, plan 15.1.3), `store/migration-blocked`, `store/migration-failed` (plan 15.1.2), `store/disk-full`, `store/backlog`; `session/not-found`, `session/exists`, `session/owned`, `session/lease-lost`, `session/deleted`, `session/seq-conflict`, `session/unknown-type`, `session/newer-payload`, `session/corrupt`. This area exposes no tool to the model and no wire message; commands and routes that call it belong to areas A24 and A19.

**The export file** ([DH-ADR-PERSISTENCE-12](../upstream/session-and-persistence.md#dh-adr-persistence-12)). Line 1 is a header: `{"dsh_export":1, "session":{...}, "inherited":n}`. Each following line is one event: `seq`, `time`, `type`, `pv`, the envelope fields, and `data` and `aux` as the stored bytes. The last line is a trailer with the event count and a SHA-256 of everything before it, so a truncated file is detected. The file is deterministic and may be gzip compressed (`DH-ADR-NODEZLIB-1` in [../libs/node-fs-path-os-zlib.md](../libs/node-fs-path-os-zlib.md)).

**Persisted event vocabulary: the 64 upstream types.** Each row is one `UP-PERSIST` unit. "Area" is the cluster key from [../trace/clusters.json](../trace/clusters.json) of the area whose upstream package declares the type and which therefore defines the Go type; the declaration is cited from `docs/persistence-catalog.md`. This area stores every kept type the same way: a registered type with a payload version, covered by the inventory of [DH-D-STORE-5](#dh-d-store-5). The vocabulary rule is [DH-ADR-PERSISTENCE-2](../upstream/session-and-persistence.md#dh-adr-persistence-2). "Kept" means an event of that meaning stays in the vocabulary unless the named area drops the feature; Go type names are that area's to choose.

| Upstream type | Kind | Declared at | Go | Area | Note |
|---|---|---|---|---|---|
| `agent-preset/selected` | log-only | `packages/preset/agent-preset-registry/src/session.ts:30` | kept | A27 | State event, last one wins. |
| `agent/inbox/spliced` | log-only | `packages/core/agent/src/types.ts:110` | kept | A02 | One change to the pending-input list. |
| `approval/asked` | log-only | `packages/interaction/user-approval/src/types.ts:44` | kept | A17 | Paired with `decided` by id. |
| `approval/decided` | log-only | `packages/interaction/user-approval/src/types.ts:55` | kept | A17 | Carries the attributed outcome. |
| `approval/policy` | log-only | `packages/interaction/user-approval/src/index.ts:41` | kept | A17 | Unchanged. Session grants are two new types of A17's, `grant/created` and `grant/ended` ([DH-ADR-16](../design.md#dh-adr-16)). |
| `assistant/attempt` | log-only | `packages/core/session/src/types.ts:367` | kept | A01 | Raw stream goes to the `aux` column. |
| `assistant/message` | surface | `packages/core/session/src/types.ts:353` | kept | A01 | Raw stream goes to the `aux` column. |
| `command/done` | log-only | `packages/interaction/commands/src/types.ts:112` | kept | A17 | Paired with `run` by id. |
| `command/run` | log-only | `packages/interaction/commands/src/types.ts:105` | kept | A17 | |
| `compaction/end` | log-only | `packages/compaction/compaction/src/types.ts:72` | kept | A14 | Bracket closed by its owner, not by crash recovery. |
| `compaction/prune` | log-only | `packages/compaction/compaction/src/types.ts:82` | kept | A14 | |
| `compaction/start` | log-only | `packages/compaction/compaction/src/types.ts:24` | kept | A14 | |
| `compaction/summary` | log-only | `packages/compaction/compaction/src/types.ts:34` | kept | A14 | |
| `deliverables/presented` | log-only | `packages/deliverables/tool-present/src/types.ts:15` | kept | A08 | |
| `developer/message` | surface | `packages/core/session/src/types.ts:321` | kept | A01 | |
| `feedback/message-delete` | log-only | `packages/feedback/message-feedback/src/types.ts:58` | kept | A17 | |
| `feedback/message-put` | log-only | `packages/feedback/message-feedback/src/types.ts:56` | kept | A17 | |
| `feedback/record` | log-only | `packages/feedback/command-feedback/src/types.ts:40` | kept | A17 | |
| `goal/change` | log-only | `packages/goal/goal/src/domain.ts:66` | kept | A13 | Whole new state. |
| `hook/invoked` | log-only | `packages/experimental/hook-protocol/src/types.ts:19` | kept | X5 | Experimental; exists only if X5 ports hooks. |
| `hook/result` | log-only | `packages/experimental/hook-protocol/src/types.ts:31` | kept | X5 | As above. |
| `image/offload` | log-only | `packages/compaction/compaction-image-offload/src/projection.ts:25` | kept | A14 | Read by a message projection. |
| `job/registered` | log-only | `packages/jobs/jobs/src/types.ts:59` | kept | A13 | |
| `job/settled` | log-only | `packages/jobs/jobs/src/types.ts:64` | kept | A13 | |
| `llm/retry` | log-only | `packages/llm/llm-retry/src/types.ts:9` | kept | A04 | |
| `llm/retry-started` | log-only | `packages/llm/llm-retry/src/types.ts:11` | kept | A04 | |
| `model/selection` | log-only | `packages/core/agent/src/types.ts:121` | kept | A02 | |
| `permission/preset` | log-only | `packages/interaction/permission-presets/src/index.ts:61` | kept | A17 | |
| `plan/mode` | log-only | `packages/plan/plan-mode/src/index.ts:55` | kept | A13 | |
| `request/context` | log-only | `packages/core/session/src/types.ts:414` | kept | A01 | Written by the driver (A02). |
| `request/header` | log-only | `packages/core/session/src/types.ts:402` | kept | A01 | Large snapshots: the reason reads are paged. |
| `sandbox/mode` | log-only | `packages/sandbox/sandbox-policy/src/session-mode.ts:33` | kept | A10 | |
| `schedule/change` | log-only | `packages/schedule/schedule/src/types.ts:625` | **not kept** | A13 | Dead upstream: schedules live in host storage (`docs/persistence-catalog.md:809`). |
| `session/end-seed` | log-only | `packages/core/session/src/types.ts:439` | kept | A01 | The row's `inherited_count` repeats the cut for listing. |
| `session/title` | log-only | `packages/session/session-title/src/index.ts:78` | kept | A06 | |
| `session/title-llm-request` | log-only | `packages/session/session-title-llm/src/index.ts:63` | kept | A06 | |
| `session-log-deepseek/delivery-accepted` | log-only | `packages/session/session-log-deepseek/src/types.ts:96` | kept | A05 | This area's only type ([DH-ADR-STORE-10](#dh-adr-store-10)). |
| `step/end` | log-only | `packages/core/session/src/types.ts:311` | kept | A01 | May be written by crash recovery. |
| `step/start` | log-only | `packages/core/session/src/types.ts:309` | kept | A01 | |
| `subagent/catalog` | log-only | `packages/subagent/subagent/src/catalog.ts:52` | changed | A12 | Replaced by `subagent/started` and `subagent/settled` in the root's log ([DH-ADR-SUBAGENT-2](subagent-team.md#dh-adr-subagent-2)). |
| `subagent/descriptor` | log-only | `packages/subagent/subagent/src/descriptor.ts:38` | changed | A12 | No Go type: its facts are the child's header fields (A01) and `subagent/started`. |
| `subagent/model-selection-policy` | log-only | `packages/subagent/tool-subagent/src/model-selection-state.ts:12` | **not kept** | A12 | Dead upstream: neither written nor read (`docs/persistence-catalog.md:968`). |
| `system/message` | surface | `packages/core/session/src/types.ts:342` | kept | A01 | |
| `team/member` | log-only | `packages/team/agent-team/src/types.ts:179` | **not kept** | A12 | Historical upstream: declared only so that old logs decode (`docs/persistence-catalog.md:1012`). Members are folded from `subagent/started` ([DH-ADR-SUBAGENT-11](subagent-team.md#dh-adr-subagent-11)). |
| `team/message/delivered` | log-only | `packages/team/agent-team/src/types.ts:185` | **not kept** | A12 | Historical upstream, as above. A message between agents is an inbox splice (A02). |
| `team/message/queued` | log-only | `packages/team/agent-team/src/types.ts:183` | **not kept** | A12 | Historical upstream: "new sends use the target Agent inbox" (`docs/persistence-catalog.md:1043`). |
| `team/mode` | log-only | `packages/team/agent-team/src/types.ts:177` | kept | A12 | |
| `team/task` | log-only | `packages/team/agent-team/src/types.ts:181` | kept | A12 | |
| `todo/write` | log-only | `packages/todo/tool-todo/src/types.ts:33` | kept | A03 | |
| `tool/call` | log-only | `packages/core/session/src/types.ts:373` | kept | A01 | Durable before the tool body runs. |
| `tool/ptc-dispatch` | log-only | `packages/core/tool-executor-ptc/src/types.ts:61` | changed | A03 | No guest runtime ([DH-ADR-8](../design.md#dh-adr-8)); becomes "nested call settled" for the tool command line. |
| `tool/ptc-dispatch-start` | log-only | `packages/core/tool-executor-ptc/src/types.ts:45` | changed | A03 | Becomes "nested call started". |
| `tool/result` | surface | `packages/core/session/src/types.ts:387` | kept | A01 | May be written by crash recovery. |
| `tool-workflow/agent-end` | log-only | `packages/workflow/tool-workflow/src/types.ts:57` | kept | A11 | Exists only if A11 keeps workflow runs as events. |
| `tool-workflow/agent-start` | log-only | `packages/workflow/tool-workflow/src/types.ts:52` | kept | A11 | As above. |
| `tool-workflow/run-end` | log-only | `packages/workflow/tool-workflow/src/types.ts:62` | kept | A11 | As above. |
| `tool-workflow/run-start` | log-only | `packages/workflow/tool-workflow/src/types.ts:47` | kept | A11 | As above. |
| `turn/end` | log-only | `packages/core/session/src/types.ts:307` | kept | A01 | Reason `interrupted` is written only by crash recovery. |
| `turn/start` | log-only | `packages/core/session/src/types.ts:298` | kept | A01 | |
| `user/message` | surface | `packages/core/session/src/types.ts:319` | kept | A01 | A human prompt updates `last_prompt_at`. |
| `web/deepseek-search-llm-request` | log-only | `packages/web/web-search-deepseek/src/provider.ts:90` | kept | A15 | |
| `working-directory/change` | log-only | `packages/session/working-directory/src/index.ts:62` | **not kept** | A06 | Dead upstream: no writer, readers ignore it (`docs/persistence-catalog.md:1326`). |
| `workspace/changes` | log-only | `packages/deliverables/workspace-changes/src/types.ts:106` | kept | A08 | |
| `workspace-root/change` | log-only | `packages/session/working-directory/src/index.ts:67` | kept | A06 | |

Types with no upstream unit are declared by their areas and stored the same way; they have no row above because the table lists upstream units. Among them: `grant/created`, `grant/ended` and `feedback/withdrawn` (A17; `approval/asked` there carries a `kind`, `approval` or `consent`), `subagent/started`, `subagent/settled` and `subagent/dismissed` (A12), `compaction/request` and `compaction/attempt` (A14), `session/title-attempt` and the three `session-upload/*` types (A06), `tool/subcall`, `tool/subresult`, `tool/late-return` and `tool/disclosed` (A03), `llm/retry-abandoned` (A04).

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership.** The `session.store` fiber owns the two connection pools, the writer goroutine, the instance lock and its `hosts` row, the follow pollers and any duty lease it holds. A writer is an effect of the fiber that opened it (the session's owner); closing it releases the lease. Sessions, agents and area A07's tables have **access** to the store, not ownership: none can close it. A session written by an earlier run is *previously owned*: it is reopened under a new epoch and deleted only by an explicit call.

| Situation | What happens | Undone, retried or left uncertain |
|---|---|---|
| The process dies inside a transaction | The transaction never commits; SQLite replays the write-ahead file at the next open | Nothing to clean. Accepted, not yet durable events are lost; they were never promised |
| A commit meets another process's lock | The writer waits the busy timeout and retries the whole transaction `CommitRetries` times, then fails the waiting barriers with `store/busy` | Batches stay queued in order; the next flush retries |
| A commit fails for another reason (disk full, I/O error) | Rolled back; the affected sessions are marked stalled and `store.Degraded` is emitted; the next `Flush` retries once and returns the error | Nothing dropped. The checkpoint policy fails closed: the model request or tool body does not run |
| Commit and rollback both fail | The outcome is unknown. The store marks itself `store/unusable` and refuses all work until the component is reloaded | **Uncertain:** whether the last transaction is on disk. The reopened database is the answer |
| One batch fails its guard | Its savepoint is rolled back; other sessions in the same transaction commit | The failing writer is closed for appends |
| The backlog bound is reached | `Append` returns `store/backlog`; the driver ends the turn | Nothing accepted is dropped |
| The holder of a lease dies | The kernel releases its instance lock; the next `OpenWrite` takes the lease and raises the epoch | The `hosts` row and lock file of the dead instance are removed by whoever notices |
| A stale writer appends | Refused by the guard; nothing is written | Its uncommitted events are discarded and counted in the log message |
| The host dies mid-turn | See [DH-D-STORE-4](#dh-d-store-4): the next writer closes the turn | **Uncertain:** whether a recorded tool call took effect |
| The host dies between a blob write and its event | No row references the blob; area A07's collector removes it | Never a dangling reference |
| The host dies during a deletion | The tombstone remains; the retention duty finishes the deletion | The session is already invisible and unopenable |
| A stored record has an unknown type or a newer payload version | Proposed: the open refuses the session. As built (M1, M1-D-STORE-F): the open refuses it when the record lies at or past the newest checkpoint's `seq`; a record below it is refused by the first `Read` or `ReadSeqs` that reaches it, with the same typed error | Nothing is written and no refused record is returned; the session stays as stored |
| A layout migration fails | Its transaction rolls back; the store refuses to open | The backup taken before it is the rollback, and it is kept |
| The backup before a migration fails | The migration is not attempted | Nothing changed |
| Another instance is alive when a newer binary needs to migrate | Refused with `store/migration-blocked`, naming the live processes | Nothing changed |
| A follow poll fails | The subscription ends with the error; the reader may resume from its last `seq` | No event is delivered twice or out of order |
| Shutdown | The store stops admitting appends, runs one final barrier bounded by `ShutdownFlushTimeout`, clears its leases, removes its `hosts` row, truncates the write-ahead file and releases the instance lock | If the bound is exceeded, the sessions with events that did not become durable are named in the log |
| The database is damaged | `store/corrupt`; nothing is read partially. `dsh` offers the integrity check and per-session export of whatever still reads | **Uncertain:** which sessions are affected until the check runs |

## 7. Decisions

<a id="dh-adr-store-1"></a>
### DH-ADR-STORE-1 One contract package with typed readers and writers; the store returns the log as written
- **Decision.** The contract is its own package. `Reader` and `Writer` are two interface types. Events reach storage only through a `Writer`; no component listens to session events to persist them. The store validates structure only (contiguous `seq`, registered or `ignorable` type, known payload version, decodable bytes). Checking relationships between events and closing an interrupted turn belong to the session and its owner.
- **Why.** A compile-time split removes upstream's run-time `SessionReadOnlyError`. Upstream's listener path is why "a session nobody acquired a handle for persists nothing" can happen silently. A store that interprets meaning would have to know every event type.
- **Tradeoff.** The session constructor must be given a writer, so tests need the in-memory store. A structurally valid log with contradictory events is only caught by the session's own pass (area A01).
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-4](../design.md#dh-o-4). **Verified by.** `DH-T-STORE-1`, `DH-T-STORE-10`.

<a id="dh-adr-store-2"></a>
### DH-ADR-STORE-2 One row per event, the payload in two columns, history protected by the database, forks by copy
- **Decision.** The layout of §6.2: one `events` row per event; `data` plus an optional `aux` column for the part history reads skip; triggers that refuse every update and every delete outside a session deletion; the session row carries `next_seq`, the lease and the last human prompt time; a fork copies its parent's prefix inside the database; a session row appears with its first batch.
- **Why.** One row per event makes paging, deletion and search joins plain SQL, where upstream's 64-event frames need decoding. Large raw streams are the one workload where a published benchmark shows the pure-Go driver three times slower ([../libs/sqlite.md](../libs/sqlite.md) §5.2), so they are kept out of the common read. Copying a prefix keeps deletion and search unaware of sharing.
- **Tradeoff.** A fork costs disk in proportion to its prefix. No compression in the first layout; the `codec` column reserves it. Whether `aux` in the same row is fast enough is unmeasured (M2); if not, it moves to a side table by a layout migration.
- **Serves.** [DH-O-5](../design.md#dh-o-5). **Verified by.** `DH-T-STORE-2`, `DH-T-STORE-3`, `DH-T-STORE-11`.

<a id="dh-adr-store-3"></a>
### DH-ADR-STORE-3 Commit on demand with group commit; durable means committed with a full sync
- **Decision.** One writer goroutine per instance commits whenever batches are queued; what arrives during a commit forms the next transaction. There is no batching timer. Each batch has its own savepoint. `Flush` is a barrier, defined in §6.2. The queue is bounded. Unload waits for one final barrier, bounded by configuration. Nothing is checkpointed inside a running model attempt: an attempt becomes durable as its one settlement, so a process lost mid-attempt loses that attempt's stream, by this decision.
- **Why.** The writer batches naturally under load and adds no latency when idle, which replaces upstream's 200 ms window. It answers the open point of [../design.md §5](../design.md): shutdown waits, with a bound.
- **Tradeoff.** Every commit is a disk sync, so an idle session pays one sync per small batch. A process killed between acceptance and commit loses those events; only a checkpoint closes that window.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-3](../design.md#dh-o-3). **Verified by.** `DH-T-STORE-3`, `DH-T-STORE-4`, `DH-T-STORE-5`.

<a id="dh-adr-store-4"></a>
### DH-ADR-STORE-4 A lease is an epoch in the session row plus one liveness lock file per process
- **Decision.** The six rules of §6.2. Liveness is proven by one lock file per instance, not one per session and never the database file. Every acquisition raises the epoch, and every append checks it in its own transaction. Nothing expires by time. A forced takeover exists as an explicit owner action.
- **Why.** [DH-ADR-12](../design.md#dh-adr-12) makes takeover routine. One lock per process means one descriptor however many sessions a host holds, and the same record of live instances serves duty leases and migration safety. The epoch check is what makes taking a lease from a stuck process safe, which upstream could not offer.
- **Tradeoff.** A loser learns at its next append, not at once. A forced takeover leaves the old process's running tools running; the log says "outcome unknown". Liveness depends on file locks, so the home must be local ([DH-ADR-STORE-8](#dh-adr-store-8)).
- **Serves.** [DH-O-5](../design.md#dh-o-5). **Verified by.** `DH-T-STORE-6`, `DH-T-STORE-7`, [DH-E-STORE-1](#dh-e-store-1).

<a id="dh-adr-store-5"></a>
### DH-ADR-STORE-5 Three version layers, each refusing in a named direction; a version changes only when an older binary would misread
- **Decision.** (a) Layout version: forward-only steps in one transaction, after a backup made through SQLite, and only when no other instance is alive; a newer database is refused unchanged. (b) Payload version per event type, read as in §6.2. A new optional field keeps the version; the version rises exactly when a binary without the change would misread the row, and that binary then refuses the session instead. (c) Export format number. A committed inventory and a test guard (b).
- **Why.** The first shipped binary's refusals can never be added later. Upstream's criterion, reader behaviour and not structural difference, is the cheapest rule that is still safe; applied per type it never forces a rewrite of other events.
- **Tradeoff.** A change that restructures across events needs a layout migration and is deliberately expensive. Two binaries of different versions cannot share a home across a layout change. Upcasters are kept until a recorded decision removes one.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-STORE-2`, `DH-T-STORE-10`.

<a id="dh-adr-store-6"></a>
### DH-ADR-STORE-6 Another process's commits are seen by polling the database's change counter; once-per-home work uses duty leases
- **Decision.** `Follow` on a session held by another process polls SQLite's `data_version` on one pinned read connection, only while a subscriber exists, and then reads the rows after the last delivered `seq`. No read transaction stays open between polls. A named job that must run once per home takes a row in `duties` under the same liveness rule as a session lease.
- **Why.** [../design.md §4.7](../design.md) leaves both questions to this area. Polling needs no channel between processes and works for any mix of hosts. An open read transaction would let the write-ahead file grow without bound.
- **Tradeoff.** A follower lags by up to the interval and sees only durable events. Inference: `data_version` behaves as documented on the chosen driver; `DH-T-STORE-14` checks it.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-STORE-14`, [DH-E-STORE-5](#dh-e-store-5).

<a id="dh-adr-store-7"></a>
### DH-ADR-STORE-7 Deletion is a tombstone followed by bounded chunks; retention is off by default
- **Decision.** `Delete` takes the session's lease, marks the row `deleting` and raises the epoch in one transaction, sends `store.Deleting`, then removes events, checkpoints, search rows and blob references in bounded chunks and finally the row. Sessions created as subagent children of the deleted session are deleted with it; forks are not. Automatic retention is disabled unless the operator sets an age.
- **Why.** Deleting a hundred thousand rows in one transaction blocks every other writer on the home. Deleting history nobody asked to delete is the more harmful default.
- **Tradeoff.** Deletion is irreversible and not instantaneous. Disk use is unbounded until the operator configures retention, and the page that shows storage use must say so.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-STORE-11`, [DH-E-STORE-6](#dh-e-store-6).

<a id="dh-adr-store-8"></a>
### DH-ADR-STORE-8 The home must be a local filesystem that supports WAL; files are owner-only; no encryption at rest
- **Decision.** If the database cannot enter WAL mode the store refuses the home with `store/no-wal` (proposed as `store/unsupported-filesystem`; the built code is `store/no-wal`, plan 15.1.3); there is no rollback-journal fallback. The directory is created with mode `0700` and the files with `0600`. The store does not encrypt.
- **Why.** Leases rest on file locks and on shared memory between processes; a journal-mode fallback would keep the database working while quietly breaking the lease. The chosen driver was not checked for encryption ([../libs/sqlite.md](../libs/sqlite.md) §7).
- **Tradeoff.** A home on a network share or synced folder is not supported. On Windows, privacy rests on the user profile's access-control list, which the harness does not change. Every session is readable by anything that runs as the user.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-STORE-2`.

<a id="dh-adr-store-9"></a>
### DH-ADR-STORE-9 The export file is the portable form: raw payload bytes, its own format number, gzip
- **Decision.** The format of §6.2. Export copies stored bytes and never re-encodes through a struct. Import validates the whole file, then writes it in one transaction; a file with an existing id is refused unless the caller asks for a new id.
- **Why.** Raw bytes make the round trip exact and keep fields a binary does not know. gzip is readable everywhere and is in the standard library, so it is the one compression of an export; Zstandard is not used (`DH-ADR-NODEZLIB-1`).
- **Tradeoff.** A snapshot, not a live view. Referenced blobs are not included.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-STORE-13`.

<a id="dh-adr-store-10"></a>
### DH-ADR-STORE-10 Session-log upload is an opt-in component, and its acceptance record stays a logged event
- **Decision.** The upload is kept as a component that is off until the user turns it on and that obeys the one off-switch ([DH-ADR-9](../design.md#dh-adr-9)). It sends export-format lines, bounded by `MaxBytes`. Acceptance is appended to the session as an event. A rejected request opens a per-session breaker. An event larger than `MaxBytes` does not stop the upload: its place is taken by an *elision record*, an export line with the event's `seq`, `time`, `type`, byte size and SHA-256 and the marker `omitted`, so the uploaded log stays contiguous by number and continues (register entry U-K1-11; recommended default, not confirmed by the owner, and conditional on the vendor accepting such a line, which is settled by the same exit gate as the format itself).
- **Why.** The owner's decision fixes the default. Keeping the acceptance in the log gives the user a durable record of what left the machine and when. An optional field must not keep failing model requests.
- **Tradeoff.** No parity with upstream's default. Inference: the vendor's endpoint was built for upstream's event format; whether it accepts the Go format is unknown and is the exit gate of [DH-D-STORE-9](#dh-d-store-9).
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-STORE-15`, [DH-E-STORE-4](#dh-e-store-4).

<a id="dh-adr-store-11"></a>
### DH-ADR-STORE-11 Upstream sessions enter only through a separate command that refuses whole files
- **Decision.** `upstreamimport` reads upstream format 4 only, translates each event through an explicit table, and writes a session in one transaction. A file with an older generation, an unknown event type (also when `ignorable`), an unknown extension field, a missing referenced blob or a log the session core's bracket rules refuse (an unanswered call in a closed step) is refused whole and nothing is written. It streams. No package of the store imports it.
- **Why.** [DH-ADR-11](../design.md#dh-adr-11). An opaque payload may hold `seq` references that a translation invalidates, which is upstream's own reason for strict refusal.
- **Tradeoff.** Some sessions will not import. Holders of formats 0 to 3 must first open them for writing with upstream. The translation table cannot be written until the other areas have fixed their Go types.
- **Serves.** [DH-O-5](../design.md#dh-o-5). **Verified by.** `DH-T-STORE-16`, [DH-E-STORE-3](#dh-e-store-3).

<a id="dh-adr-store-12"></a>
### DH-ADR-STORE-12 A request table hands a log-only change to the process that holds the session
- **Decision.** The owner's resolution of register entry U-K1-5 ([DH-ADR-40](../design.md#dh-adr-40)). The home database has a `requests` table (§6.2). A process that does not hold a session files a change there with `Store.FileRequest(session, kind, drafts)`, in a transaction that checks the session exists and is held by a live instance other than the caller; a free session is not queued for, because the caller can write it itself ([DH-ADR-SESSION-12](core-session.md#dh-adr-session-12)). Each instance that holds at least one session polls the change counter of [DH-ADR-STORE-6](#dh-adr-store-6) and hands pending rows of its sessions to the session core through `Store.Requests(session)`. The core appends the drafts as one group, and the batch carries the request id: the append transaction sets the row to `applied` with the first `seq`, so events and mark commit together. A draft the core refuses sets `refused` with the error. A row still `pending` after `Requests.TTL` (default 10 minutes) is set to `expired` by whichever instance sees it. The requester learns the outcome from `Store.WatchRequest(id)`, which uses the same poll. The path carries log-only drafts only, at first; the first user is feedback ([interaction-credentials.md](interaction-credentials.md)). A caller normally reaches the table through `session.Service.AppendLogOnly`, which picks among the live, free and held-elsewhere paths; calling `FileRequest` directly is the same third path for a caller that already knows another process holds the session.
- **Why.** One writer per session ([DH-ADR-12](../design.md#dh-adr-12)) gave a non-holder a way to read a session and none to change it, so a thumbs-up from a second `dsh` process was refused. A table needs no listener in the command-line host and reuses the poll and the lease check that exist.
- **Tradeoff.** A change arrives up to one poll interval late and can expire when the holder is stuck; the caller is told and nothing is applied later behind its back. Every holding process now polls while it holds a session. A prompt is deliberately not a request: input has its own path and its own ordering rules.
- **Failure and cleanup.** The holder dies between reading a row and appending: the row is still `pending`, and the next holder, or the requester's own maintenance append once the lease is free, applies it; never twice, because the mark commits with the events. The requester dies: the row settles without it. A deleted session deletes its rows. Settled rows are pruned by the retention duty.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-STORE-17`, [DH-E-STORE-5](#dh-e-store-5).

## 8. Deliverables

Implementation order: 1, 2 and 5 first (5 must exist before the first event type is registered), then 3, 4 and 7. The store may be enabled for real sessions only when 1 to 5 are done. 7 is P0 although no turn needs it, because the export is how the P0 scenarios and the replay fixtures read a session, and the first carriers ([DH-D-CLI-2](app-cli.md#dh-d-cli-2), [DH-D-API-5](api.md#dh-d-api-5)) depend on it. 6 and 8 follow; 9 and 10 are last and independent of each other.

<a id="dh-d-store-1"></a>
### DH-D-STORE-1 A store contract with a conformance suite and an in-memory reference
- **Result and why it matters.** Every consumer programs against one small interface, and any store must pass one suite. Other areas can build and test before SQLite is wired.
- **How it is implemented.** The interfaces of §6.1 and the error values; `storetest`, a suite that takes a factory and asserts ordering, read-your-writes, barrier semantics, single writer, refusal classes and paging; `memstore`, which keeps batches in memory and models "accepted" and "durable" as two counters so that tests can simulate a crash between them.
- **Components and files.** `internal/session/store`, `store/storetest`, `store/memstore`.
- **Depends on.** [DH-D-SESSION-1](core-session.md#dh-d-session-1): the header and envelope types. No runtime prerequisite.
- **Priority.** P0. **Outcome.** [DH-O-5](../design.md#dh-o-5). **Done when.** `DH-T-STORE-1` passes on `memstore`.

<a id="dh-d-store-2"></a>
### DH-D-STORE-2 The SQLite store: one database per home with atomic appends, a flush barrier, paged reads and listing
- **Result and why it matters.** Sessions survive a crash, open quickly and can be listed and forked, with far less code than a file backend.
- **How it is implemented.** `sqlitedb` runs the open sequence (identity and layout check before any pragma that writes, owner-only creation, bounded retry into WAL, registration of the instance), owns one write connection and a bounded read pool, translates driver errors once, and runs the writer goroutine. `sqlitestore` creates the layout of §6.2, implements the append transaction, the barrier, `Read` with windows, `List` as a keyset-paged query, `Stat`, and fork by `INSERT ... SELECT`. Layout migrations are an ordered list applied under [DH-ADR-STORE-5](#dh-adr-store-5).
- **Components and files.** `internal/session/sqlitedb`, `internal/session/sqlitestore`, component `session.store`.
- **Depends on.** [DH-D-STORE-1](#dh-d-store-1); `DH-LIB-SQLITE` (provisional until measured); [DH-D-STORAGE-2](storage.md#dh-d-storage-2) for the home path. **Used by.** Its tables are filled by [DH-D-VIEW-2](session-views.md#dh-d-view-2) and [DH-D-VIEW-6](session-views.md#dh-d-view-6) (derived rows and search text) and [DH-D-STORAGE-3](storage.md#dh-d-storage-3) (`blob_refs`). Runtime prerequisite: a local filesystem that supports WAL.
- **Priority.** P0. **Outcome.** [DH-O-5](../design.md#dh-o-5). **Done when.** `DH-T-STORE-1` to `DH-T-STORE-5` pass on Windows, Linux and macOS (for M1: Windows and Linux (WSL); macOS removed from M1 by OWNER-1, decision `platforms-windows-wsl` in [the M1 decision log](../../stories/harness-DSH-M1/decisions.md), plan step 15.3.6), `DH-T-STORE-18` passes inside its budgets, and [DH-E-STORE-1](#dh-e-store-1) and [DH-E-STORE-2](#dh-e-store-2) have passed.

<a id="dh-d-store-3"></a>
### DH-D-STORE-3 One writer per session across processes, with safe takeover
- **Result and why it matters.** Several `dsh` processes share a home without corrupting a log; a crashed holder never blocks a successor, and a stuck one can be displaced safely.
- **How it is implemented.** The instance lock through `fsx`; the `hosts` table; lease acquisition, probing and release inside write transactions; the guard of the append transaction; the forced takeover option; the `store.LeaseLost` event; a sweep of dead instances at open.
- **Components and files.** `sqlitedb` (instance lock, `hosts`), `sqlitestore` (lease rows).
- **Depends on.** [DH-D-STORE-2](#dh-d-store-2); `DH-LIB-NODEFSXSYS`; [DH-D-FS-1](filesystem.md#dh-d-fs-1) for the `fsx` lock primitive. **Used by.** [DH-D-AGENT-6](agent-loop.md#dh-d-agent-6), which stops an agent whose session closed with `lease-lost`.
- **Priority.** P0. **Outcome.** [DH-O-5](../design.md#dh-o-5). **Done when.** `DH-T-STORE-6` and `DH-T-STORE-7` pass and [DH-E-STORE-1](#dh-e-store-1) has passed on all three systems. A takeover defect could put two writers on one log, so this scenario runs in the build stage, not in a later acceptance batch; until it has passed, several processes on one home are not enabled.

<a id="dh-d-store-4"></a>
### DH-D-STORE-4 Durability checkpoints, honest crash recovery and a bounded shutdown
- **Result and why it matters.** No model request or tool side effect happens without a durable record of what caused it; after a crash the interrupted turn is closed and the model is told which outcomes are unknown.
- **How it is implemented.** `checkpoint.Policy` is called by the owners of three points, never attached as an optional listener: the driver calls it before a provider attempt starts and at each step end ([DH-D-AGENT-3](agent-loop.md#dh-d-agent-3)), and the tool pipeline calls it before a root call's body, after policy, guards and approval ([DH-D-TOOLS-2](tools.md#dh-d-tools-2)). A nested call adds no flush: it rides on its outer call's checkpoint. The default policy flushes the session at all three points; a composition may mount another schedule, and with no policy mounted the callers flush directly, so the default fails closed. A flush error is returned, so the work does not run; a cancellation that arrives during the flush yields `tools/aborted-before-start`. Recovery is the session core's: `session.Service.Open` ([DH-D-SESSION-5](core-session.md#dh-d-session-5)) takes the lease through `OpenWrite`, computes the closers, appends them with the new epoch's `session/end-seed` as the first batch and flushes before the session is published. The closers are an error result per unanswered tool call (`tools/not-started` when no call record exists, `tools/outcome-unknown` when it does, with text that permits a retry only for read-only or idempotent work), a missing `step/end`, and `turn/end` with reason `interrupted`. A read-only observer applies the same closers in memory and writes nothing. Shutdown follows [DH-ADR-STORE-3](#dh-adr-store-3).
- **Components and files.** `internal/session/checkpoint`, component `session.checkpoint`.
- **Depends on.** [DH-D-STORE-2](#dh-d-store-2), [DH-D-STORE-3](#dh-d-store-3), [DH-D-SESSION-5](core-session.md#dh-d-session-5), [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1).
- **Priority.** P0. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-3](../design.md#dh-o-3). **Done when.** `DH-T-STORE-5`, `DH-T-STORE-8` and `DH-T-STORE-9` pass and [DH-E-STORE-2](#dh-e-store-2) has passed.

<a id="dh-d-store-5"></a>
### DH-D-STORE-5 Each event type evolves by itself, and a persisted type cannot change by accident
- **Result and why it matters.** A format change costs a function and a test. An unreviewed change to anything persisted fails the build. Old and newer data are read correctly or refused with a direction.
- **How it is implemented.** `persisted.Decode` implements the read rule of §6.2. The inventory is one committed JSON file with a line per registered type and version (and per envelope, header and projection state type), holding the fingerprint that the `schema` package computes ([DH-ADR-SCHEMA-4](../libs/zod.md#dh-adr-schema-4)). A test boots the registries of the default composition, recomputes the fingerprints and compares. A difference fails until the file is regenerated with the golden-file switch and a line is added to the notes file beside it: the type, old and new version, and one sentence saying why an older binary reads or refuses the new rows. A changed fingerprint with an unchanged version needs the word `compatible` and that reason. For every type and version ever listed, a recorded row under `testdata` must still decode. The persisted-type catalogue of [DH-O-14](../design.md#dh-o-14) is rendered from the same registry.
- **Components and files.** `internal/session/persisted` with `inventory.json`, `CHANGES.md`, `testdata/rows`.
- **Depends on.** [DH-D-STORE-1](#dh-d-store-1); [DH-D-SESSION-1](core-session.md#dh-d-session-1) for the registry; [DH-D-UTIL-1](util-test-support.md#dh-d-util-1) for `schema` fingerprints.
- **Priority.** P0. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-STORE-10` passes.

<a id="dh-d-store-6"></a>
### DH-D-STORE-6 Sessions can be deleted, old ones retired, and the store checked and rebuilt
- **Result and why it matters.** Storage a user can clean up, and a way to find and repair damage to derived data.
- **How it is implemented.** `Delete` as in [DH-ADR-STORE-7](#dh-adr-store-7), which also removes a child that was created and never published ([subagent-team.md](subagent-team.md)); a retention duty that finishes tombstones and deletes unheld sessions older than the configured age; and two maintenance operations with one name each. **Check** runs SQLite's integrity check and verifies each session's row count against `next_seq`. **Rebuild** is the one operation that discards derived data and refolds it: for one session or the whole home it drops the checkpoints, A06's view rows and the search documents and replays the events through the session core and A06's registry (`projection.Registry.Rebuild`); with its verify option it refolds into memory, compares with the stored rows and changes nothing. There is no second operation that rebuilds only the search index.
- **Components and files.** `sqlitestore`; the commands are `dsh store check` and `dsh store rebuild` (`--session <id>`), named by area A24 ([DH-D-CLI-7](app-cli.md#dh-d-cli-7)).
- **Depends on.** [DH-D-STORE-2](#dh-d-store-2), [DH-D-STORE-3](#dh-d-store-3), [DH-D-STORE-8](#dh-d-store-8) for the duty. **Used by.** [DH-D-STORAGE-9](storage.md#dh-d-storage-9), which collects the blobs a deleted session leaves unreferenced.
- **Priority.** P1. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-STORE-11` and `DH-T-STORE-12` pass and [DH-E-STORE-6](#dh-e-store-6) has passed. Deletion removes data, so that scenario runs in this deliverable's build stage.

<a id="dh-d-store-7"></a>
### DH-D-STORE-7 A session can be exported to one readable file and imported back
- **Result and why it matters.** Backup, sharing, bug reports and test fixtures without file-based live storage.
- **How it is implemented.** A streaming writer and reader for the format of §6.2 with bounded memory; decompression is bounded in output size; import validates before writing.
- **Components and files.** `internal/session/export`; the command is [DH-D-CLI-7](app-cli.md#dh-d-cli-7) and the download route [DH-D-API-5](api.md#dh-d-api-5); the multi-session ZIP archive that wraps these files is [DH-D-VIEW-8](session-views.md#dh-d-view-8).
- **Depends on.** [DH-D-STORE-2](#dh-d-store-2), [DH-D-STORE-5](#dh-d-store-5); `DH-LIB-NODEZLIB`.
- **Priority.** P0. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-STORE-13` passes.

<a id="dh-d-store-8"></a>
### DH-D-STORE-8 A process can follow a session another process writes, and once-per-home jobs have one runner
- **Result and why it matters.** A web host shows a session that a command-line run is writing; retention does not run in every process.
- **How it is implemented.** `Follow` as in [DH-ADR-STORE-6](#dh-adr-store-6): within the holding process it reads the session's own feed (area A01); otherwise it polls. The duty API offers acquire, run and release on the `duties` table.
- **Components and files.** `sqlitedb` (change counter, duties), `sqlitestore` (`Follow`).
- **Depends on.** [DH-D-STORE-2](#dh-d-store-2), [DH-D-STORE-3](#dh-d-store-3). **Used by.** The event stream ([DH-D-API-4](api.md#dh-d-api-4)) and, for duties, by [DH-D-STORAGE-9](storage.md#dh-d-storage-9). Schedules do not use a duty ([DH-ADR-JOBS-7](jobs-schedule-goal-plan.md#dh-adr-jobs-7)).
- **Priority.** P1. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-12](../design.md#dh-o-12). **Done when.** `DH-T-STORE-14` passes and [DH-E-STORE-5](#dh-e-store-5) has passed.

<a id="dh-d-store-9"></a>
### DH-D-STORE-9 Session-log upload to the vendor, only for a user who opted in
- **Result and why it matters.** A user who wants to share session logs with the model vendor can; nobody else sends anything.
- **How it is implemented.** The component registers one request-extension field with the DeepSeek adapter. Per request it reads `Enabled` and the off-switch, applies registered exclusions, folds the highest accepted `seq` from the session's own acceptance events, and attaches the longest run of export lines after it that fits `MaxBytes`. On HTTP 2xx it appends the acceptance event. An event larger than the limit is sent as an elision record ([DH-ADR-STORE-10](#dh-adr-store-10)) and shown in the settings page with its size. A rejection opens the breaker of [DH-ADR-STORE-10](#dh-adr-store-10).
- **Components and files.** `internal/session/logupload`, component `session.logupload`, service `logupload.Key` with `RegisterExclusion`.
- **Depends on.** [DH-D-STORE-2](#dh-d-store-2), [DH-D-STORE-7](#dh-d-store-7); [DH-D-LLM-2](llm.md#dh-d-llm-2) for the request-contribution registry (a contribution that cannot be prepared is omitted; when the vendor rejects a request that carried contributions, the transport sends one replacement request without them inside the same attempt and tells each contributor, which is when this component opens its breaker); [DH-D-HOST-4](host-telemetry.md#dh-d-host-4) for the off-switch. **Used by.** The setting is shown by [DH-D-UISET-3](ui-settings-plugins.md#dh-d-uiset-3).
- **Priority.** P2. **Outcome.** [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-STORE-15` passes and [DH-E-STORE-4](#dh-e-store-4) has passed. If the vendor refuses the Go format, the component is not shipped and the question returns to the owner.

<a id="dh-d-store-10"></a>
### DH-D-STORE-10 A deferred one-way command imports upstream format 4 sessions
- **Result and why it matters.** People who used upstream can bring their current-format history. Deferred by [DH-ADR-11](../design.md#dh-adr-11): it is not a condition for the store.
- **How it is implemented.** A reader for upstream's physical form (a header line, one event per line, concatenated Zstandard frames, range-encoded source lists), a translation table from each upstream type to Go events, a `seq` map kept only when the translation changes the number of events, blob copy before events, one transaction per session, and a dry-run mode that lists every refusal.
- **Components and files.** `internal/session/upstreamimport`; the command is area A24's.
- **Depends on.** [DH-D-STORE-5](#dh-d-store-5), [DH-D-STORE-7](#dh-d-store-7); `DH-LIB-NODEZLIBZSTD` (Zstandard is read here only, for upstream's files; nothing the Go store or its export writes uses it); [DH-D-STORAGE-3](storage.md#dh-d-storage-3) for blobs. The translation table needs every area's persisted Go types.
- **Priority.** P2, after the core. **Outcome.** [DH-O-5](../design.md#dh-o-5). **Done when.** `DH-T-STORE-16` passes and [DH-E-STORE-3](#dh-e-store-3) has passed.

<a id="dh-d-store-11"></a>
### DH-D-STORE-11 A process that does not hold a session can hand its holder a log-only change
- **Result and why it matters.** Feedback given in the web UI on a session that a terminal `dsh` is running is recorded, not refused. Closes register entry U-K1-5.
- **How it is implemented.** The `requests` table and its three store calls (`FileRequest`, `Requests`, `WatchRequest`) of [DH-ADR-STORE-12](#dh-adr-store-12); the request id carried on a batch and marked in the append transaction; expiry and pruning in the retention duty; the poll shared with `Follow`.
- **Components and files.** `sqlitestore`, `store` (the three calls), `memstore` (the same calls, for the session core's tests).
- **Depends on.** [DH-D-STORE-2](#dh-d-store-2), [DH-D-STORE-3](#dh-d-store-3), [DH-D-STORE-8](#dh-d-store-8). **Used by.** [DH-D-SESSION-9](core-session.md#dh-d-session-9).
- **Priority.** P1. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-12](../design.md#dh-o-12). **Done when.** `DH-T-STORE-17` passes and [DH-E-STORE-5](#dh-e-store-5) has passed.

## 9. Acceptance

### 9.1 Local tests

All use real database files in a temporary directory; none uses a mock of SQLite. Tests that need a second process start a helper built from the test binary. "D-n" abbreviates `DH-D-STORE-n`.

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-STORE-1` | D-1, D-2 | The conformance suite passes on `memstore` and on the SQLite store: order, read-your-writes, barrier, single writer, refusals, paging. |
| `DH-T-STORE-2` | D-2 | Open sequence: a foreign `application_id` and a newer layout are refused with the file byte-identical; a home where WAL cannot be entered is refused; files are owner-only; a migration takes a backup first, is refused while another instance is alive, and rolls back on failure. |
| `DH-T-STORE-3` | D-2 | Fault injection at each step of the append transaction: events and blob references are all present or all absent; derived rows (checkpoints, view rows, search rows) are present with their events or absent as a group, never ahead of them; an `UPDATE` of an event and a stray `DELETE` are aborted by the triggers; a fork equals its parent's prefix. |
| `DH-T-STORE-4` | D-2 | `Flush` returns only after the commit; a failed commit keeps the batch and the next flush succeeds; a failed rollback makes the store unusable; the backlog bound refuses an append (while appends are synchronous, this clause asserts that an event is durable once `Append` returns: owner decision M1-D-STORE-A (a), plan step 15.1.4). |
| `DH-T-STORE-5` | D-2, D-4 | A helper process appending and flushing is killed at random points in a loop; every reopen finds a contiguous log that contains the last flushed event. |
| `DH-T-STORE-6` | D-3 | A second writer is refused in the same process and from a second process, with the holder's identity; after the holder is killed the lease is taken and the epoch rises; a stale writer's append writes nothing; two processes racing for a dead holder's lease produce one winner; a forced takeover works. |
| `DH-T-STORE-7` | D-3 | While an instance is alive, opening and closing the database file with `os` in that process does not release its liveness lock (Linux, macOS); another process can read the lock file (Windows). For M1: Windows and Linux (WSL); macOS removed from M1 by OWNER-1 (decision `platforms-windows-wsl` in [the M1 decision log](../../stories/harness-DSH-M1/decisions.md); plan step 15.3.6). |
| `DH-T-STORE-8` | D-4 | With fake callers at the three points: the flush precedes the provider call and the tool body; with no policy mounted the callers still flush; a flush error means neither is invoked; a cancellation during the flush yields `tools/aborted-before-start`; a nested call adds no flush. |
| `DH-T-STORE-9` | D-4 | For an interruption at each point of a step, the closers are the expected events, are the first batch under the new epoch and are durable before publication; a read-only observer writes nothing; shutdown drains within its bound and names what it could not. |
| `DH-T-STORE-10` | D-5 | A changed struct fails the inventory test until file and note are updated; every recorded row decodes; an upcaster chain yields the current struct; a newer payload version and an unknown required type refuse with their direction (as built, M1-D-STORE-F, plan step 15.3.1: at open when the record lies at or past the newest checkpoint, otherwise at the first `Read` or `ReadSeqs` that reaches it, and a reader also judges records appended after it opened); an unknown `ignorable` record is byte-identical after read, fork and export. |
| `DH-T-STORE-11` | D-6 | Deletion is refused while another instance holds the session; a process killed mid-deletion leaves a tombstone that the duty finishes; no row of the session remains; a fork and its blob references survive deletion of the parent; subagent children are deleted; retention deletes only unheld sessions past the age. |
| `DH-T-STORE-12` | D-6 | After a rebuild the search index, the view rows and the checkpoints equal the incrementally built ones; the verify option reports a row damaged on purpose and changes nothing; the check reports a session whose row count was damaged on purpose. |
| `DH-T-STORE-13` | D-7 | Export, import, export gives two byte-identical files; a truncated or altered file is refused and nothing is written; a small input that expands hugely is stopped at the limit; an existing id is refused. |
| `DH-T-STORE-14` | D-8 | Events committed by a second process reach a follower once and in order; no read transaction is open between polls; a duty has one holder and moves after its holder is killed. |
| `DH-T-STORE-15` | D-9 | With a recording endpoint: a fresh home sends no field; the off-switch silences an enabled upload; the byte boundary and the watermark are exact; an event above the limit is sent as an elision record and the next request continues after it; an exclusion skips a request; a rejection opens the breaker and later requests carry no field. |
| `DH-T-STORE-16` | D-10 | Fixture files written in upstream format 4 import to the expected event lists; a file with an unknown type, an older generation or a missing blob is refused whole with the database unchanged. |
| `DH-T-STORE-17` | D-11 | With a helper process holding the session: a filed request is applied once and its row says `applied` with the `seq`; filing for a free or missing session is refused; killing the holder between read and append leaves the row `pending` and the next holder applies it once; an unapplied row expires after the TTL and the watcher is told; a refused draft sets `refused`; deleting the session removes its rows. |
| `DH-T-STORE-18` | D-2 | Budget tests ([DH-ADR-TOOLCHAIN-14](../upstream/toolchain.md#dh-adr-toolchain-14)), benchmarks behind the `bench` build tag: opening one session of more than 100,000 events from its checkpoints; forking it; listing on the heavy-tailed session corpus; each again in the memory-limited rerun. A case fails when it misses its budget. The numbers are set by the first measured run (M2 and M4 of [../libs/sqlite.md](../libs/sqlite.md)). As built (M1): the budgets are provisional from that run and pinned at impl.md step 16.7 (M1-D-STORE-B (a), held open as H-34), and the open from checkpoints judges only the records past the newest checkpoint, so an open judges a fixed number of records whatever the log's length (M1-D-STORE-F, plan step 15.3.1). |

### 9.2 Real-environment scenarios

<a id="dh-e-store-1"></a>
#### DH-E-STORE-1 Two real `dsh` processes share one home: one writer, and safe takeover
- **Proves.** [DH-D-STORE-3](#dh-d-store-3), [DH-D-STORE-2](#dh-d-store-2). A takeover defect could put two writers on one log, so this scenario runs in the build stage of [DH-D-STORE-3](#dh-d-store-3).
- **Given.** The built `dsh` binary on Windows, Linux and macOS in turn; an empty home on a local disk; the replay model provider, so no model key is needed; processes A and B each started as a headless host on that home by the scenario controller (`go test` under the scenario build tag, on the machine under test).
- **When.** (1) A creates a session and runs a turn. (2) B tries to resume the same session. (3) A is killed by the operating system during a second turn. (4) B resumes the session and runs a turn. (5) A second copy of A is started and tries to write the session.
- **Then.** At (2) B is refused with `session/owned` naming A's process id, and nothing in the log changes. At (4) B's resume succeeds, the epoch has risen by one, and the interrupted turn is closed with `turn/end` interrupted. At (5) the new process is refused with `session/owned` naming B. The log read from the database has `seq` 0 to n with no gap and no duplicate, and A's lock file and `hosts` row are gone. Each process step has a 60 second timeout; a timeout fails the scenario.
- **Cleanup.** Stop both processes and delete the home. If the log has a gap, keep the home, both process logs and the output of the integrity check.

<a id="dh-e-store-5"></a>
#### DH-E-STORE-5 A process that does not hold a session follows it and hands its holder a change
- **Proves.** [DH-D-STORE-8](#dh-d-store-8), [DH-D-STORE-11](#dh-d-store-11).
- **Given.** The built `dsh` binary on Windows, Linux and macOS in turn; an empty home on a local disk; the replay model provider; processes A and B each started as a headless host on that home by the scenario controller.
- **When.** (1) A creates a session and runs a turn. (2) B follows the session read-only while A runs a second turn. (3) B files one log-only request (a feedback entry) for it. (4) B files a second request and A is killed before it applies it; B then takes the free session's lease through a maintenance append.
- **Then.** At (2) B receives every event of the turn once, in order, within twice the follow interval. At (3) the request row becomes `applied` and the feedback event is in the log once. At (4) the second request is applied exactly once, by B, and its row says so. No `seq` gap or duplicate.
- **Cleanup.** Stop both processes and delete the home. If a request was applied twice or not at all, keep the home and both process logs.

<a id="dh-e-store-2"></a>
#### DH-E-STORE-2 A host killed mid-turn recovers honestly
- **Proves.** [DH-D-STORE-4](#dh-d-store-4), [DH-D-STORE-2](#dh-d-store-2).
- **Given.** The built binary started as a headless host; a real model API key supplied at run time; a real shell under the platform's sandbox; an empty home; a workspace directory with one file.
- **When.** (1) Prompt the agent to run a shell command that sleeps for a minute and then writes a file. (2) Once the tool call is recorded, kill the host process. (3) Start the host again and resume the session. (4) Send a new prompt.
- **Then.** After (3) the session shows the turn as interrupted and the tool call with an "outcome unknown" result; no event recorded before the kill is missing; the first model request after (4) contains that result.
- **Cleanup.** Stop the host, kill any surviving child process, delete the home and the workspace. If recovery fails, keep the database with its side files.

<a id="dh-e-store-6"></a>
#### DH-E-STORE-6 A deleted session leaves nothing behind, and an interrupted deletion finishes
- **Proves.** [DH-D-STORE-6](#dh-d-store-6). Deletion removes data, so this scenario runs in the build stage of [DH-D-STORE-6](#dh-d-store-6).
- **Given.** The built binary started as a headless host; an empty home on a local disk; the replay model provider; two sessions, one with an attached image and one forked from it that shares the image.
- **When.** (1) Delete the first session. (2) Start a deletion of a large session (10,000 events) and kill the host after its first chunk; start the host again. (3) Run the integrity check.
- **Then.** After (1) no row of the first session remains, the fork and the shared image survive, and the image no longer referenced by any session is collected. After (2) the tombstoned session is invisible at once and, once the retention duty has run, no row of it remains. (3) reports no damage.
- **Cleanup.** Delete the home. If a row survived, keep the database with its side files.

<a id="dh-e-store-3"></a>
#### DH-E-STORE-3 Sessions written by a real upstream release at format 4 are imported and an older one is refused
- **Proves.** [DH-D-STORE-10](#dh-d-store-10).
- **Given.** A session root written by a published upstream release at format 4, produced outside this repository and copied in as data: at least one plain session, one with a subagent child, one compacted, and one still at format 3. An empty Go home.
- **When.** Run the import command in dry-run mode, then for real, then open each imported session in the web UI.
- **Then.** The three format 4 sessions import, and their derived message history equals the history upstream derives from the same files (recorded with upstream's own export). The format 3 session is refused with a message that says to upgrade it with upstream. The upstream files are byte-identical afterwards.
- **Cleanup.** Delete the Go home. Keep the upstream root and the refusal report if a result differs.

<a id="dh-e-store-4"></a>
#### DH-E-STORE-4 The real DeepSeek API accepts an opted-in session-log upload, and nothing is sent before opting in or after the off-switch
- **Proves.** [DH-D-STORE-9](#dh-d-store-9).
- **Given.** The built binary started as a headless host; a real DeepSeek API key supplied at run time; an empty home; a proxy that records request bodies; one prompt whose tool result is larger than `MaxBytes`.
- **When.** (1) Run one turn with default settings. (2) Turn the upload on and run two turns. (3) Use the one off-switch and run a turn.
- **Then.** At (1) and (3) no request carries the field. At (2) the first request carries the events up to its own request, the API answers 2xx, an acceptance event is in the log, and the second request starts after the accepted `seq`; the oversized event travels as an elision record and the API accepts it. If the API rejects the field, the model request still completes through the one replacement request without contributions, the breaker opens, later requests carry no field, and the scenario is recorded as failed.
- **Cleanup.** Delete the home and the proxy capture, which contains session text.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Driver.** The SQLite driver is provisional ([DH-ADR-SQLITE-1](../libs/sqlite.md#dh-adr-sqlite-1)). Every default in §6.2, the `aux` column and the per-row payload depend on measurements M1, M2 and M6, which nobody has taken.
2. **Windows lock range.** `fsx` offers the non-blocking exclusive lock on a range beyond the end of a file that the lease needs ([DH-D-FS-1](filesystem.md#dh-d-fs-1)). That a reader of the lock file is not refused is unverified until [DH-E-STORE-1](#dh-e-store-1) has passed.
3. **Vendor.** Whether the DeepSeek endpoint accepts a log in the Go format, and an elision record in it, is unknown until [DH-E-STORE-4](#dh-e-store-4) has passed.
4. **Mixed binary versions on one home** work only while their layout versions are equal.
5. **Not designed here.** Compression of stored payloads, encryption at rest, a home on a network filesystem, salvage from a damaged database beyond per-session export, and bundling referenced blobs into an export (the archive of [DH-D-VIEW-8](session-views.md#dh-d-view-8) carries attachments).
6. **Limitation of the evidence.** Process death is tested; power loss is not. The tests prove the stated contracts for the tested build and operating systems, not for every filesystem.

## 11. Units dispositioned by this spec

Ledger fragment: [../trace/fragments/A05.jsonl](../trace/fragments/A05.jsonl).

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 11 | 3 ported, 2 replaced, 6 dropped |
| `UP-DEBT` | 48 (46 assigned, 2 found) | 42 resolved, 1 not-applicable, 5 carried (as the debt review re-issued them) |
| `UP-CFG` | 3 | 3 replaced |
| `UP-SVC` | 2 | 2 ported |
| `UP-PERSIST` | 64 | 54 ported, 4 replaced, 6 dropped |
| `UP-DEC` | 15 | 8 ported, 7 replaced (final verdicts in §4) |

Go items declared: `DH-ADR-STORE-1` to `DH-ADR-STORE-12`, `DH-D-STORE-1` to `DH-D-STORE-11`, `DH-T-STORE-1` to `DH-T-STORE-18`, `DH-E-STORE-1` to `DH-E-STORE-6`.
