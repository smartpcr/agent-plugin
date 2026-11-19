# Upstream reference: the session log, persistence formats and storage

> **Status: design only, nothing built or run.** This is the foundation reference for agent F5 (ID key `PERSISTENCE`). It describes how upstream deepseek-harness (`dsh`) stores what happened, read at the commit recorded in the harness README (`2e39e70`), and recommends decisions for the Go design. Upstream facts are cited as `path:line` relative to the upstream root. A sentence that is reasoning and not read from source opens with "Inference". Line counts quoted for upstream source were counted with `wc -l` over each package's `src` directory on 2026-11-18.

## 1. Purpose

A harness session can run for hours, call hundreds of tools and cost real money. Three things must be true of its record: it survives a crash, it can be continued or branched later, and anyone can see exactly what the model was shown. Upstream meets all three with one idea: **the session is an append-only log of events, and everything else is computed from that log**.

This document explains that log, how upstream writes it to disk, how its format has changed, how read models (projections and search) are built on it, and where the data that is not a session lives. It then judges each upstream choice for the Go design. Section 11 holds the recommendations; sections 2 to 10 are the evidence.

Readers who design Go storage should read sections 3, 5, 6 and 9 before section 11.

## 2. Vocabulary

Terms are listed in the order the rest of the document needs them. Upstream uses "checkpoint" for two different things; both are listed.

| Term | Meaning | Why it exists |
|---|---|---|
| Session | One agent conversation: an ordered list of events plus a small header (`docs/subsystems/session.md:5`). | It is the unit that is created, resumed, forked, listed and searched. |
| Session event | One typed, immutable fact in the log, for example "the user said this" or "this tool returned that". | A fact that must survive a reload has to be an event (`docs/architecture.md:71`). |
| Envelope | The fields every event has, around its type-specific `data`: `type`, `seq`, `time`, `data`, optional `ignorable`, and for surface events `surfaceOp` and `sourceEventSeqs` (`docs/persistence-catalog.md:12`). | Storage and readers can order, validate and skip events without understanding every payload. |
| `seq` | The event's position in its session, counted from 0 with no gaps (`docs/subsystems/session.md:230`). | It is the event's identity; other events and read models refer to it. |
| Surface event | One of five event types that produce a message the model sees: `system/message`, `developer/message`, `user/message`, `assistant/message`, `tool/result` (`docs/subsystems/session.md:311-316`). | The model's history is built from these and nothing else. |
| Log-only event | Every other event. It is stored and replayed but never becomes a model message. | Audit, state and bookkeeping must be durable without changing what the model sees. |
| Surface, surface operation | The ordered list of surface events currently visible to the model. An event joins it by `append`, or by `replace` of an inclusive range of earlier surface events (`docs/subsystems/session.md:335-340`). | Compaction replaces old messages with a summary without deleting the originals from the log. |
| Derived history | The message list computed from the surface by `deriveMessages()` (`docs/subsystems/session.md:698`). | History is never stored twice, so it cannot disagree with the log. |
| Turn, step | A step is one model request plus the tools it calls; a turn is zero or more steps (`docs/architecture.md:83`). `turn/*` and `step/*` events bracket them. | Brackets let a reader tell finished work from interrupted work. |
| Request header | A full snapshot of the non-history part of a model request (call configuration, adapter defaults, tool schemas), logged as `request/header` (`docs/subsystems/session.md:185`). | With it, every request is a pure function of the log. |
| Session header | Metadata stored beside the log, not in it: format version, id, creation time, working directory, lineage flags, delegation depth, agent preset (`docs/subsystems/persistence.md:147-192`). | Listing sessions must not require reading their events. |
| Seed, inherited event count, end-seed marker | A fork starts from a copy of a prefix of its parent (the seed). The inherited event count is the exact length of that prefix. A `session/end-seed` event marks where inherited history stops (`docs/subsystems/session.md:768-774`). | Inherited history and the child's own work are otherwise byte-identical, so nothing could tell whose an unfinished bracket is. |
| Handle | One open channel onto a stored session, opened for `read` or `write`, with `read`, `append`, `flush`, `close` (`docs/subsystems/persistence.md:41-99`). | All log access goes through one object, so single-writer ownership has one place to be enforced. |
| Write ownership, lease | The right to append to one session. One writer at a time; a second write open is refused. The JSONL backend backs it with a kernel lock so it dies with the process (`packages/session/session-persistence-jsonl/src/lease.ts:1-13`). | Two writers interleaving appends would corrupt the sequence. Ownership (who appends and who must close the handle) is separate from access (anyone may open a read handle). |
| Append, flush | `append` accepts and orders a batch; `flush` is the only call that promises the batch survives a crash (`docs/subsystems/persistence.md:67-91`). | Producers are not blocked on disk for every event, yet can demand durability at chosen points. |
| Durability checkpoint | A flush at a point chosen by policy: before a model request, before a top-level tool runs, at each step boundary (`packages/session/session-checkpoint-policy/README.md:48`). | A recorded request or tool call must be on disk before its side effect happens. |
| Materialization | The moment a created session first exists on disk. Upstream defers it to the first append or flush (`docs/subsystems/persistence.md:102`). | A session that was created and abandoned leaves no file. |
| Torn tail | Bytes at the end of a file from a write that never completed. | A crash can cut a write in half; readers must never see the half. |
| Format version | The integer in the header that says how to interpret the whole log. The current writer is 4 (`packages/core/session/src/types.ts:90`). | A reader must know whether it can faithfully interpret a file. |
| Generation | One file per format version for the same session, for example `session.v3.jsonl.zstd` beside `session.v4.jsonl.zstd` (`docs/architecture.md:128`). | Upstream never rewrites an old file; an upgrade writes a new one next to it. |
| Exclusive publication | Making a finished file appear under its final name only if that name is free, never by overwriting. | Two processes racing to create or upgrade the same session cannot clobber each other. |
| Codec, migration edge, stage, chain | A codec reads one version's physical rows. An edge converts version N to N+1. A stage is one edge's mutable state for one file. The chain runs all needed edges in order (`packages/session/session-format/README.md:44`). | Old files stay readable after the format changes. |
| `ignorable` | An envelope flag meaning "a reader that does not know this type may skip it". Without it, an unknown type makes the reader refuse the session (`docs/subsystems/session.md:276-286`). | Forgetting the flag fails safe (a refusal) instead of silently dropping something that mattered. |
| Plugin record | An `ignorable` event in the `plugin:` namespace, written by experimental packages (`docs/subsystems/session.md:788`). | Experimental state can be stored without entering the reviewed event vocabulary. |
| Projection unit, fold | A pure function `apply(state, event) -> state` with an initial state, owned by one domain (`docs/subsystems/session-projection.md:46-105`). Folding means applying it to events in order. | Current values (todo list, title, statistics) are needed constantly and must not be recomputed by every reader. |
| Snapshot, `asOfSeq` | All client-visible projection values for one session read at one sequence number (`docs/subsystems/session-projection.md:112-123`). | A page of history and the state shown beside it must describe the same moment. |
| Projection checkpoint | A stored copy of a unit's state with the `seq` it covers (`packages/session/session-projection/README.md:100`). | Opening or listing an old session should not replay its whole log. |
| Revision | An opaque change token for one stored session; equal tokens mean "unchanged" (`docs/subsystems/persistence.md:304`). | Caches of derived data need a cheap way to know the log did not move. |
| Query corpus | All sessions a query can see: live ones in memory preferred over their stored copies (`docs/subsystems/session-query.md:5`). | A search must reflect events that are not flushed yet. |
| Storage hub, backend, domain | The hub is a table of named backends; a backend owns one medium (a directory or a database); a domain is one owner's typed tables in a backend (`docs/subsystems/storage.md:11`, `:47`, `:55-92`). | Everything that is not a session log needs durable storage too. |
| Attachment | An image or file stored once by content hash; events hold only a reference (`docs/subsystems/attachment.md:5`). | Binary payloads would bloat the log and cannot be deduplicated inside it. |
| Spill, locator | Text too large to show the model inline is saved to a file; the model gets a head and tail plus a locator and a hint for reading the rest (`docs/subsystems/spill.md:5`, `:74`). | Large tool output must not consume the context window, yet must stay reachable. |

## 3. The session log

### 3.1 The envelope

Every event is `{ type, seq, time, data }`, with `time` in Unix epoch milliseconds, plus an optional `ignorable: true` (`docs/subsystems/session.md:268-291`). Surface events must also carry `surfaceOp`; log-only events must not (`docs/subsystems/session.md:296`). A surface event other than `assistant/message` may carry `sourceEventSeqs`, the complete list of earlier events it was derived from or replaces; an assistant message instead embeds the exact timed stream the provider sent (`docs/subsystems/session.md:362`).

All `data` must be lossless JSON. `Session.append` copies and validates the payload before it commits, rejecting values JSON cannot round-trip (`packages/core/session/README.md:125`), so the in-memory log always equals what a backend can store (`docs/subsystems/session.md:831`).

### 3.2 Ordering and immutability guarantees

| Guarantee | Statement | Source |
|---|---|---|
| Contiguous order | Events are numbered from 0 with no gaps; an append whose first `seq` is not the stored next `seq` is rejected. | `packages/session/session-persistence/README.md:87` |
| Append-only | Committed events are never rewritten. A change of meaning is a new event (for example a `replace`), not an edit. | `docs/subsystems/persistence.md:381` |
| Immutable values | An appended event is deep-frozen in memory, so no reader can mutate logged history. | `packages/core/session/README.md:46`, `packages/core/session/src/index.ts:542` |
| Valid prefix on read | A read returns a contiguous prefix; a torn tail is never returned; repeated reads on one handle never go backwards. | `docs/subsystems/persistence.md:56-58` |
| Read your writes | Once an append or flush resolves, any read started afterwards on the same backend instance sees at least that prefix. | `docs/subsystems/persistence.md:35-39` |
| Durable only after flush | An append may be buffered; only a resolved flush survives a crash. | `docs/subsystems/persistence.md:67-91` |
| Fail closed | An unknown event type without `ignorable`, or a newer format version, refuses the session; it is never read partially. | `docs/subsystems/persistence.md:197` |

A group append validates every candidate before changing anything, so observers see the whole group or none of it; upstream states that this alone does not make the group crash-atomic in storage (`packages/core/session/README.md:88`).

### 3.3 How model history is derived

The surface is itself a fold over the log. An `append` adds a node at the tail. A `replace` removes an inclusive range of current nodes and inserts the new event in their place; the replaced events stay in the log and become "shadowed" (`docs/subsystems/session.md:340`). `deriveMessages()` then maps each surface node to one message: a user message keeps its exact content, an assistant message keeps provider, model and content, a tool result becomes a tool-role message, and an assistant message with empty content is skipped (`docs/subsystems/session.md:700-703`). Everything else is structural and produces no message (`docs/subsystems/session.md:705`).

The system prompt is part of this history, not of the request header: the first `system/message` is surface node 0, and a prompt change replaces that node or appends a new one (`docs/subsystems/session.md:300`). The rest of a request (model, sampling settings, tool schemas) is rebuilt by taking the latest `request/header` snapshot (`docs/subsystems/session.md:185`).

A plugin that changes the content of existing messages (image offload is the shipped example) registers a pure "message projection"; the session refuses to append or restore such an event when its interpreter is missing (`docs/subsystems/session.md:367`).

### 3.4 What "model-visible means logged" requires

Upstream's rule: every model request must be reconstructable from the log (`docs/architecture.md:130`). In practice this demands five things.

1. Any new input the model can see needs a session event. There is no side channel.
2. The request envelope is logged as a full snapshot whenever it changes or a new message series starts (`docs/subsystems/session.md:185`). Upstream accepts the growth this causes (`packages/core/session/README.md:110`).
3. Each model attempt settles as exactly one event: `assistant/message` when it produced a message, `assistant/attempt` when it failed, was retried or was cancelled. Both embed the raw timed stream (`docs/architecture.md:126`).
4. The request is flushed to disk before it is sent, so a crash cannot have sent something the log does not hold (`packages/session/session-checkpoint-policy/README.md:48`).
5. Binary inputs are stored durably before the event that references them is appended (`docs/subsystems/attachment.md:7`).

What is deliberately not logged: live stream chunks before settlement. A hard process loss before an attempt settles leaves no trace of that attempt (`docs/architecture.md:126`).

## 4. Event families

The generated catalog lists 64 event types in 35 families; 5 are surface events and 59 are log-only (`docs/persistence-catalog.md:167-1361`). The catalog covers this repository only; external plugins are outside it (`docs/persistence-catalog.md:12`). "Owner" is the package that declares the type. Inference: by upstream convention the declaring package is also the writer, except for the core lifecycle events, which are declared in `packages/core/session/src/types.ts` and written by the agent loop as shown in the turn flow (`docs/architecture.md:85-108`).

| Family (catalog line) | Events | What it records | Owner |
|---|---|---|---|
| `turn/*` (`:1248`) | `start`, `end` | A turn opening and closing, with the reason it ended. `interrupted` and `forked` reasons are only ever written by crash repair and fork construction (`docs/subsystems/session.md:749-756`). | core session; agent loop writes |
| `step/*` (`:904`) | `start`, `end` | One model request plus its tool calls. | core session; agent loop writes |
| `system/*` (`:979`) | `message` (surface) | The rendered system prompt as a history node. | core session; agent loop writes |
| `developer/*` (`:495`) | `message` (surface) | Tool additions and removals made mid-session, bound to an earlier `request/header`. Added in format 4. | core session; agent loop writes |
| `user/*` (`:1286`) | `message` (surface) | A human prompt, injected context, or a goal continuation; `source` tells them apart. | core session; agent loop writes |
| `assistant/*` (`:279`) | `message` (surface), `attempt` | The assembled reply with its raw stream and token usage; or an attempt that produced no message. | core session; agent loop writes |
| `tool/*` (`:1090`) | `call`, `result` (surface), `ptc-dispatch-start`, `ptc-dispatch` | A tool request with its raw arguments, its result, and sub-calls made from inside a `run_code` program. | core session; `packages/core/tool-executor-ptc` |
| `request/*` (`:741`) | `header`, `context` | The full request envelope snapshot; and the route's capacity metadata. | core session; agent loop writes |
| `session/*` (`:819`) | `end-seed`, `title`, `title-llm-request` | The inherited-history boundary (only the session constructor and fork builder may write it, `docs/subsystems/session.md:164`); the latest title; the request that generated a title. | core session; `packages/session/session-title`, `session-title-llm` |
| `agent/*` (`:169`) | `inbox/spliced` | One change to an agent's pending-message list. | `packages/core/agent` |
| `model/*` (`:690`) | `selection` | The model selection that applies from the next step. | `packages/core/agent` |
| `agent-preset/*` (`:192`) | `selected` | The agent preset chosen after creation. | `packages/preset/agent-preset-registry` |
| `approval/*` (`:212`) | `asked`, `decided`, `policy` | An approval question and its outcome (paired by id); a policy switch, last one wins. | `packages/interaction/user-approval` |
| `permission/*` (`:706`) | `preset` | The selected permission preset. | `packages/interaction/permission-presets` |
| `sandbox/*` (`:778`) | `mode` | A sandbox mode switch, last one wins. | `packages/sandbox/sandbox-policy` |
| `plan/*` (`:724`) | `mode` | Plan mode on or off, last one wins. | `packages/plan/plan-mode` |
| `command/*` (`:326`) | `run`, `done` | A slash command entering its handler and settling, paired by `commandId`. | `packages/interaction/commands` |
| `compaction/*` (`:369`) | `start`, `summary`, `prune`, `end` | A compaction bracket, the summary and its model-call facts, and the cost record of a model-free prune. | `packages/compaction/compaction` |
| `image/*` (`:617`) | `offload` | Permanent omission of selected input images from later requests. | `packages/compaction/compaction-image-offload` |
| `llm/*` (`:666`) | `retry`, `retry-started` | Request recovery attempts. | `packages/llm/llm-retry` |
| `hook/*` (`:567`) | `invoked`, `result` | A hook command run and its outcome, paired by `handlerId`. | `packages/experimental/hook-protocol` |
| `goal/*` (`:552`) | `change` | The complete goal state after a change, or a clear marker. | `packages/goal/goal` |
| `todo/*` (`:1075`) | `write` | A todo-list write. | `packages/todo/tool-todo` |
| `job/*` (`:636`) | `registered`, `settled` | A background job's registration and terminal record. | `packages/jobs/jobs` |
| `schedule/*` (`:801`) | `change` | Historical only. New tasks live in host storage; these events are inert (`docs/persistence-catalog.md:809`). | `packages/schedule/schedule` |
| `subagent/*` (`:928`) | `catalog`, `descriptor`, `model-selection-policy` | A child's identity in its parent's log; the child's own identity and mode; a retired policy record that current releases neither write nor read (`docs/persistence-catalog.md:968`). | `packages/subagent/subagent`, `tool-subagent` |
| `team/*` (`:1005`) | `mode`, `member`, `task`, `message/queued`, `message/delivered` | Agent-team mode, membership, task board and message delivery. | `packages/team/agent-team` |
| `tool-workflow/*` (`:1190`) | `run-start`, `agent-start`, `agent-end`, `run-end` | A workflow run and each member's start and settlement. | `packages/workflow/tool-workflow` |
| `deliverables/*` (`:480`) | `presented` | Inference from the name and owner: deliverables shown to the user (payload not read for this document). | `packages/deliverables/tool-present` |
| `workspace/*` (`:1340`) | `changes` | That a finished turn's changed files were summarized; the summary itself stays on the host. | `packages/deliverables/workspace-changes` |
| `workspace-root/*`, `working-directory/*` (`:1357`, `:1318`) | `change` | A move of the session's active directory. `working-directory/change` has no writer now and readers ignore it (`docs/persistence-catalog.md:1326`). | `packages/session/working-directory` |
| `feedback/*` (`:514`) | `record`, `message-put`, `message-delete` | Human remarks about the session and per-message feedback. | `packages/feedback/command-feedback`, `message-feedback` |
| `session-log-deepseek/*` (`:884`) | `delivery-accepted` | Inference from the name: acknowledgement that an uploaded log was accepted. | `packages/session/session-log-deepseek` |
| `web/*` (`:1305`) | `deepseek-search-llm-request` | Inference from the name: the model request a web-search provider made. | `packages/web/web-search-deepseek` |

Three patterns recur and matter for the Go design. State events carry the whole new value, never a delta, so the last one wins (`packages/session/session-projection/README.md:54`). Paired events share an id (`callId`, `commandId`, `handlerId`) instead of relying on adjacency. And three events above are already dead but cannot be removed, because old logs contain them.

## 5. On-disk formats

### 5.1 The persistence service

One abstract service, `ctx.sessionPersistence`, offers `create`, `open(id, 'read' | 'write')`, `stat`, `list` and a service-wide `flush`; `create` and `open` return a handle (`docs/subsystems/persistence.md:377-454`). `stat` and `list` return the header, an optional format status, a revision token and optional cheap counts without reading events (`docs/subsystems/persistence.md:319-336`). Two backends ship; both pass one shared contract test suite (`docs/subsystems/persistence.md:343`).

Live events reach storage through a write-behind buffer. The backend listens to `session/event`, copies each event into the write handle's buffer, and starts a fixed batching window on the first pending event (`docs/subsystems/persistence.md:106`); the window is 200 ms (`packages/session/session-persistence-jsonl/src/storage.ts:37`). `session/flush` cancels the wait and drains everything.

### 5.2 The JSONL backend (the shipped default)

**Layout and naming.** One directory per session under a readable project directory derived from the working directory: `<root>/--<normalized-cwd>--/<encoded-id>/` (`packages/session/session-persistence-jsonl/README.md:61-73`). Session ids are escaped to one safe path segment (`:75`). Format 0 is named `session.jsonl[.zstd]`; format N from 1 up is `session.vN.jsonl[.zstd]` (`docs/architecture.md:128`). A lock file `session.lock` sits beside them on POSIX (`packages/session/session-persistence-jsonl/src/lease.ts:40`).

**Framing.** The first line is a physical header: `type: 'session'`, `version`, `id`, `createdAt`, `isSeeded`, `delegationDepth` and optional `cwd`, `parentSession`, `origin`, `agentPreset` (`packages/session/session-persistence-jsonl/src/format.ts:83-94`). Each later line is one event. `sourceEventSeqs` is range-encoded: a run of three or more consecutive numbers is stored as a `[start, end]` pair (`packages/session/session-persistence-jsonl/README.md:113`).

**Compression.** By default the file is a concatenation of independent checksummed Zstandard frames: one frame for the header, then one frame per appended batch, at the default compression level. `compression: 'none'` stores the same lines as plain text. A root uses one encoding; mixing or converting is unsupported (`packages/session/session-persistence-jsonl/README.md:113`).

**Generations.** Operations always select the numerically highest generation present (`packages/session/session-persistence-jsonl/README.md:75`). A committed generation is never renamed, replaced or deleted (`docs/architecture.md:128`). A generation newer than the build refuses the session even if an older readable one is still there (`docs/subsystems/persistence.md:197`).

**Exclusive publication.** A new file is written to a temporary name, synced, then published under its final name with `link()` on POSIX, which fails if the name exists, followed by a sync of the directory (`packages/session/session-persistence-jsonl/src/index.ts:1415-1431`). Windows has no directory sync, so the backend calls `MoveFileExW` with `MOVEFILE_WRITE_THROUGH` and no replacement (`packages/session/session-persistence-jsonl/src/win32.ts:4-9`).

**Write path.** `create` writes nothing. The first append publishes header plus first batch as above. Each later batch is appended and synced before the append resolves; if the write or sync fails, the file is truncated back to its previous length (`packages/session/session-persistence-jsonl/README.md:79`, `src/index.ts:1525-1566`).

### 5.3 The SQLite backend (optional)

One database at `<root>/.segments/sessions.sqlite` holds all sessions (`packages/session/session-persistence-sqlite/README.md:42`). Events are grouped into frames of at most 64, each frame one row of JSON lines keyed by segment and start sequence, compressed with Zstandard when that is smaller (`:49`, `:75`). A segment is a run of events with a link to its predecessor, so a fork references its parent's stored prefix instead of copying it (`:40`). The database runs in WAL mode with `synchronous = FULL` (`packages/session/session-persistence-sqlite/src/sqlite.ts:223-226`). Batches queued together share one transaction, each inside its own savepoint (`README.md:63`). On first open of a session it imports the existing JSONL history through the migration chain and leaves the JSONL bytes in place (`:55`).

### 5.4 What each backend guarantees

| Situation | JSONL | SQLite |
|---|---|---|
| Crash during an append | An incomplete final raw line is discarded. A torn final Zstandard frame yields only its complete records; the next writer truncates the torn bytes and rewrites those records before its first new batch (`README.md:79`). | Inference: the open transaction never commits, so the database returns to the last committed batch; upstream relies on SQLite's WAL for this and adds no code of its own. |
| Write or sync error that is caught | The file is rolled back to its prior length; the batch stays buffered and is retried at the next flush (`README.md:79`, `docs/subsystems/persistence.md:106`). | The failing batch's savepoint is rolled back and its peers commit; if the commit or rollback result is unknown the provider is unusable until restart (`README.md:63`). |
| Damage inside committed data | A checksum, decompression or structure failure in a complete frame is reported as corruption; nothing is repaired (`README.md:79`). | A missing frame, short frame, unknown codec or undecodable bytes reject the read as corruption (`README.md:75`). |
| Two writers, same process | Second write open is refused (`docs/subsystems/persistence.md:11`). | Same. |
| Two writers, different processes | A kernel lock per session: `flock` on POSIX, a named semaphore on Windows. It dies with the holder. A live but stuck holder blocks writers until it exits; there is deliberately no expiry (`src/lease.ts:1-13`). Advisory locks are unreliable on some network filesystems (`README.md:170`). | The session's artifact lease plus SQLite's own locking; every process writing an imported session must use this provider (`README.md:122`). |
| Readers during a write | Allowed; readers never touch the lock (`src/lease.ts:19`). | Allowed; WAL readers share the database with the writer (`README.md:12`). |
| Created but never written | Nothing on disk; the session never existed (`README.md:79`). | Inference: the same contract applies, because the seam defines it (`docs/subsystems/persistence.md:102`). |
| Deleting a session | No API. Files accumulate until removed by hand (`README.md:169`). | No API (`packages/session/session-persistence/README.md:155`). |
| Listing | Every call walks the directory tree and reads changed headers; unpaginated and unfiltered (`README.md:89`, `packages/session/session-persistence/README.md:156`). | Reads indexed metadata; still unpaginated at the seam. |

## 6. Format versions and migrations

### 6.1 The five formats

| Format | Source checkpoint | What changed from the previous one |
|---|---|---|
| 0 | tag `dsh-v0.1.2-rc.1` | First format shipped in a release. Assistant deltas are packed several to a row; the header stores the fork cut as a number (`packages/session/session-format-v0-to-v1/README.md:43`). |
| 1 | PR #3349 | Identity plus a fixed list of clean-ups: `steering/message` becomes `user/message`, `compact/*` becomes `compaction/*`, `turn/start.trigger` is removed, legacy messages get ids. Retired event types make the migration refuse (`packages/session/session-format-v0-to-v1/README.md:47`). |
| 2 | tag `dsh-v0.1.3-alpha.2` | Top-level `assistant/chunk` events disappear: their stream is embedded in the matching `assistant/message`, or in a new `assistant/attempt`. Events are renumbered and every stored reference to a `seq` is remapped. One event per row; the fork cut moves from the header to a tagged `session/end-seed` event (`packages/session/session-format-v1-to-v2/README.md:12`, `:50-52`). |
| 3 | PR #4320 | The system prompt moves out of `request/header` into `system/message` events, which inserts events and renumbers again. The preset id `code` becomes `ptc` (`packages/session/session-format-v2-to-v3/README.md:56-68`). |
| 4 (current) | current checkout | Tool results become first-class tool-role messages instead of user messages wrapping a result block; message sources are renamed; evidenced interrupted turns are closed; missing child entries are added to the parent's subagent catalog; unknown ignorable types move into the `plugin:` namespace (`packages/session/session-format-v3-to-v4/README.md:12`, `:69`, `:85`). |

The "source checkpoint" column is from `docs/persistence-changes/historical-formats/README.md:29-35`. The same table shows the vocabulary growing from 54 roots and 415 types to 67 roots and 640 types.

### 6.2 How a migration chain runs

1. `stat` and `list` read only the header of the highest generation and translate it; they never read events or write anything (`docs/architecture.md:128`).
2. A read open of an old generation decodes each physical row once, pushes it through one stage per edge (0 to 1, 1 to 2, and so on), validates the result as current, and returns it in memory. Nothing is published (`packages/session/session-persistence-jsonl/README.md:89`).
3. A write open does the same, then encodes the result to a temporary file in the same directory, verifies it in a worker thread, rechecks that the source did not change, and publishes the current-format successor exclusively. The source stays byte-identical (`README.md:89`).
4. A migration that cannot map something refuses, leaves the source untouched and publishes nothing (`docs/cookbook/adding-a-session-format-version.md:65`).
5. The 3-to-4 edge cannot run on a session alone. It needs facts from the session's child sessions, so the backend scans every header under the root and decodes the direct children's bodies (`README.md:93-95`).

The chain is assembled at build time from one package per edge; mounting plugins cannot add or reorder edges (`packages/session/session-format-catalog/README.md:48`).

### 6.3 The cost upstream pays

| Cost | Evidence |
|---|---|
| Code that only reads old files | 7,485 source lines across `session-format`, `session-format-catalog` and the four edge packages, against 5,201 for the JSONL backend itself. |
| A redesign forced by scale | The first whole-file migration turned a 116 MB session into an operation that exhausted a 16 GB process; one real log expanded to 9.14 million version-0 events before folding to 72,784 current ones (`.agents/notes/implemented/architecture/2026-08-31-released-session-format-migrations.md:9`, `:16`). |
| Cost that grows with the store | For old generations, `stat` and `list` do work proportional to the number of sessions under the root; read-only opens never publish, so cold processes repeat the migration (`packages/session/session-persistence-jsonl/README.md:95`). |
| Type fingerprints | A machine inventory of every reachable persisted type with a SHA-256 digest: `docs/persistence-schema.json`, 1,356 lines; the readable catalog is 9,493 lines (`docs/persistence-catalog.md:16`). |
| Change records | Every structural change to a persisted type needs an acknowledgement. Each of the 26 dated records is four files: English prose, Chinese prose, a pairing record and a full schema snapshot (`docs/persistence-changes/README.md:33-40`, `:51`). The snapshots total about 680 KB. |
| Review process | A classifier sorts changes into "same version" and "needs compatibility review"; the author explains both reader directions; a finalization checkpoint locks accepted history; a separate release record tracks what was published (`docs/persistence-changes/README.md:42`, `:63`; `docs/session-format-status.md:20-24`). |
| Tooling | About 8,000 lines of scripts and their tests under `scripts/` whose names contain `persistence`, plus the catalog generator and the one-time migration script. |
| A version means a family | A log resumed by a newer build mixes old and new record variants under one version number, so "each integer identifies a family of supported representations, not one fixed schema" (`docs/cookbook/adding-a-session-format-version.md:31`). |

Inference: two of the four steps (1 to 2, 2 to 3) renumber events, which is why they cost the most. Renumbering is forced by the choice to make `seq` both the identity and the dense position of an event while also letting a migration add or remove events.

## 7. Projections and queries

### 7.1 Projections

**Registration.** A domain registers one unit per state key: a key, a schema that validates stored state, `init(header, inheritedEventCount)`, a synchronous `apply`, an optional client `view`, and a `stateVersion` (`docs/subsystems/session-projection.md:55-105`). Registration belongs to the registering plugin and is removed when it unloads; two registrants with the same key and version share one unit, different versions are rejected (`:235`).

**Folding.** The registry subscribes to `session/event` once and applies every committed event to every unit; domains hold no subscriptions and clients never fold events themselves (`docs/subsystems/session-projection.md:5`). A unit that does not care about an event must return the same state object, which upstream uses to detect "no change" by reference (`:68-74`). State for a unit registered late, or for a session older than the registry, is built lazily by folding the in-memory log (`:235`).

**Reading.** `snapshot(session)` is synchronous and returns client views with the `seq` they reflect, so a caller can read a history page and its state in the same instant (`docs/subsystems/session-projection.md:155`). A session list row carries a reduced "summary" set of keys (`:163`).

**Caches.** A separate plugin stores one checkpoint document per session at `<root>/session_projcache/sessions/<id>.json` (`packages/session/session-projection-cache/README.md:65`). It writes at session creation, at every `turn/end`, at disposal, and in between every 200 events or 5 seconds as configured (`:44-45`, `:57`). The rule is "the log leads, the cache follows": events are flushed before the checkpoint is written, so a crash leaves the cache behind, never ahead (`:65`). A checkpoint whose version or schema does not match is discarded or moved aside as a backup, never migrated (`:67`, `:81`). Nothing ever evicts checkpoints (`:127`).

### 7.2 Queries and full-text search

The query service gives exact reads over the corpus: list and filter sessions, read one event with neighbours, trace what replaced or cites an event, and follow fork lineage (`docs/subsystems/session-query.md:111`, `:265-331`). Each event is classified as `current`, `shadowed` or `log-only` using the same surface fold as model history (`:11`). A cold session is read through a read handle, balanced in memory if its last turn was interrupted, and cached per id against the revision token (`docs/subsystems/persistence.md:114`).

Search is a separate, optional provider built on SQLite FTS5 (`packages/session-query/session-query-sqlite/README.md:12`).

- **What is indexed.** One document per event holding "semantic text": messages, tool calls and results, todos, and failure detail. Reasoning blocks, structural events and stream chunks are not indexed (`docs/subsystems/session-query.md:147`).
- **Where.** In a dedicated derived database that is never the session store. Stored sessions use durable tables; live sessions use connection-local temporary tables that shadow them (`README.md:87`, `:104`).
- **Freshness.** Every search first lists persistence snapshots, compares revisions, re-reads changed logs and commits the difference in one transaction (`README.md:104`).
- **Query safety.** The query text is quoted as one literal phrase, so FTS operators are data, not syntax (`README.md:59`).
- **Ranking and paging.** More highlighted matches first, then shorter documents, then time, session and `seq`. Cursors are bound to a corpus generation and go stale when the corpus changes (`README.md:61`, `:89`).
- **Limits.** The `unicode61` tokenizer matches whole tokens, not substrings (`README.md:63`). The driver is synchronous and blocks the JavaScript thread during a match (`README.md:144`). One process owns each index (`README.md:146`).

## 8. Non-session storage

| Store | What it holds | Where and how | Guarantees and gaps |
|---|---|---|---|
| Storage hub with JSON backend | Named domains of typed records: the workspace registry, schedules, the projection cache and three experimental stores (`packages/workspace/workspace/src/spec.ts:76`, `packages/schedule/schedule/src/storage.ts:135`, `packages/session/session-projection-cache/src/spec.ts:100`). | One pretty-printed file per unit, republished whole on every write, or one file per record (`packages/storage/storage-json/README.md:76-80`). | Temporary file, sync, atomic rename. No cross-process lock: last writer wins. No write-through rename on Windows. A per-record batch delete is not crash-atomic (`README.md:143-145`). |
| Storage hub with SQLite backend | The same domains, one JSON document per row in one database (`packages/storage/storage-sqlite/README.md:73`). | `node:sqlite`, WAL by default. | The driver blocks the JavaScript thread; no busy wait; only the current physical layout opens (`README.md:130-132`). |
| Domain layer | The typed API over either backend. Reads are synchronous from memory; a write reaches the disk first, then memory, then emits `domain/changed` (`docs/subsystems/storage.md:143`). | Versioned per domain; older versions are accepted only if the owner lists them, and values are never converted by storage (`docs/subsystems/storage.md:100-110`). | An unknown write outcome quarantines the domain until it is closed and reopened (`docs/subsystems/storage.md:47`). |
| Attachments | Images and generic files referenced from events. | Content-addressed objects under `<DSH_HOME>/attachments/v1/objects/<prefix>/<sha256>`, written to a temporary file, synced and published by exclusive hard link, with the directory chain synced (`packages/attachment/attachment-local/README.md:84`). | Reads verify the digest and image header against the reference. Nothing is ever deleted and nothing collects unreferenced objects (`README.md:137`). |
| Spill | Full text of oversized tool results and session references. | `<root>/session-<hash>/<random>-<safeName>`, created exclusively with owner-only permissions; the locator is the path (`docs/subsystems/spill.md:92`). | A save failure keeps the original inline result. Files older than 30 days are swept once at start-up only (`packages/spill/spill-local/README.md:44`, `:59`, `:126`). A fork inherits its parent's locators; the files are not copied (`docs/subsystems/spill.md:32-33`). |
| Settings | Values of configuration fields that plugins mark as editable at run time. | Written through the active profile's configuration patch file, not through the storage hub (`packages/settings/settings/README.md:12`). | A legacy `settings.yaml` is imported once and renamed to `settings.yaml.imported` (`README.md:35`). |
| Workspace | An ordered list of project directories and which sessions belong to each. | A storage-hub domain. | Removing a project never deletes folders, files or sessions (`packages/workspace/workspace/README.md:170`). |
| Working directory | The session's active directory. | Not a store: it is the header's `cwd` plus `workspace-root/change` events (`docs/subsystems/working-directory.md:5`). | Follows the log. |

## 9. Failure, retry, rollback and cleanup

**A turn interrupted by a crash.** The stored log ends with an open `turn/start`. Persistence does not truncate or repair it, because a single turn can be very large and its events were durably written (`docs/subsystems/persistence.md:110`). Repair belongs to the reader that holds write ownership: on resume the agent loop appends an error result for each unanswered tool call, any missing `step/end`, and `turn/end` with reason `interrupted`. A call the model requested but that was never recorded as started gets `TOOL_NOT_STARTED`; a recorded call with no result gets `TOOL_OUTCOME_UNKNOWN`, which tells the model to retry only read-only or idempotent work and otherwise verify or ask (`packages/session/session-persistence-jsonl/README.md:149`). Read-only observers apply the same closers in memory and write nothing (`docs/subsystems/persistence.md:112`). An interrupted turn is always closed, never continued (`packages/session/session-persistence/README.md:157`).

**What is not repaired.** Core closes turn, step and tool brackets only. A bracket owned by a plugin, such as an open `compaction/start`, is left for its owner, which uses the end-seed marker to recognise it as dead (`docs/subsystems/session.md:774`).

**An unsealed physical tail.** The fragment of a write that never resolved is discarded on read and truncated by the next writer before its first append; complete records recovered from a torn frame are rewritten first (`docs/subsystems/persistence.md:110`). Recovery does not mask refusals: a retired required shape refuses the file even when it follows a damaged row (`packages/session/session-persistence-jsonl/README.md:81`).

**A failed background write.** The events stay buffered in order, automatic writing pauses, and the failure is logged. The next explicit flush retries and, if it fails again, rejects to its caller (`docs/subsystems/persistence.md:106`). With the checkpoint policy mounted that rejection is fail-closed: the model request or tool body does not run (`packages/session/session-checkpoint-policy/README.md:52`).

**A failed create or fork.** Write ownership is released; history already committed stays stored (`docs/subsystems/persistence.md:391-392`). In the SQLite backend the child's transaction rolls back (`packages/session/session-persistence-sqlite/README.md:40`).

**A failed or raced migration.** The source generation is never touched. If the source changed after it was read, a read open retries once and a write open is refused (`packages/session/session-persistence-jsonl/README.md:93`). A temporary file left by a failed attempt is removed on a best-effort basis (`packages/session/session-persistence-jsonl/src/generation.ts:800-815`).

**What is never renamed or deleted.** Committed session generations (`docs/architecture.md:128`); the POSIX lock file (`packages/session/session-persistence-jsonl/src/lease.ts:22-25`); attachments; projection checkpoints. Session logs, attachments and checkpoints therefore grow without bound, and upstream offers no retention surface for any of them. Only spill files have an age-based sweep.

**What is left uncertain.** A tool that was running at the crash may or may not have had its effect; the log says so instead of guessing. An attempt that never settled is lost entirely. A storage-hub write that failed after the file was replaced may or may not be on disk, and the domain must be reopened to find out (`docs/subsystems/storage.md:104`).

## 10. What exists only because of JavaScript or Node

| Upstream mechanism | Why JavaScript or Node needs it | In Go |
|---|---|---|
| Deep-freezing every event and derived message; the `detached` or `shared-frozen` flag on every read (`packages/core/session/src/index.ts:542`, `docs/subsystems/persistence.md:245`) | Objects are shared mutable references; nothing else stops a consumer editing logged history. | Not applicable. See [DH-ADR-PERSISTENCE-11](#dh-adr-persistence-11). |
| A copy-and-validate pass on every append that rejects BigInt, cycles, sparse arrays, `-0` and stateful getters (`packages/core/session/README.md:125`) | Any JavaScript value can be handed to `append`. | Payloads are typed structs; the encoder defines what is storable. |
| Branded number types for `seq` and offsets that vanish at run time (`docs/subsystems/session.md:252`) | TypeScript has no distinct integer types. | Named integer types. |
| Declaration merging to extend the event map, and the rule that a `switch` over events must not be exhaustive (`docs/subsystems/session.md:294`) | Any package can widen a shared interface at compile time. | A run-time registry. See [DH-ADR-PERSISTENCE-2](#dh-adr-persistence-2). |
| A catalog and fingerprint inventory generated from the TypeScript type checker (`docs/persistence-catalog.md:16`) | The persisted schema is an erased structural type; it has to be extracted to be compared. | Reflection over registered structs at test time. |
| `koffi` (a foreign-function library) to call `MoveFileExW` and create a named semaphore, copied into two packages (`packages/session/session-persistence-jsonl/src/win32.ts:56-60`, `packages/session/session-persistence-sqlite/src/win32.ts:58-62`) | Node exposes neither a write-through rename nor a kernel lock on Windows. | System calls are available without a foreign-function layer. |
| A prebuilt native addon for `flock` (`packages/session/session-persistence-jsonl/src/lease.ts:34`) | Node has no file-lock API; a missing addon rejects all writes (`README.md:173`). | Same as above. |
| A decoder that reaches into Node's private Zstandard handle to decode frames synchronously (`packages/session/session-persistence-jsonl/src/zstd-private-decoder.ts:2`, `:12-22`) | The public API is asynchronous per frame, which was too slow for hundreds of thousands of frames. | A synchronous streaming decoder is the normal API. |
| A worker thread to verify a migrated file (`packages/session/session-persistence-jsonl/src/migration-verifier.ts:142`) | Verification would block the single event loop. | A goroutine. |
| Synchronous `node:sqlite` blocking the event loop, and deferring its import to avoid an "experimental" warning (`packages/storage/storage-sqlite/README.md:130`, `packages/session-query/session-query-sqlite/README.md:67`) | The built-in driver is synchronous and experimental. | A database call blocks one goroutine, not the process. |
| Units must be synchronous so a snapshot is a consistent cut (`docs/subsystems/session-projection.md:52`) | Consistency comes for free from running on one thread. | The same property needs an explicit per-session lock or a read transaction. This is the one item here that becomes harder in Go. |
| "No change" detected by object identity (`docs/subsystems/session-projection.md:68-74`) | Reference equality is the cheap comparison. | `apply` reports whether it changed the state. |
| A registration result that is both a function and a promise, for old callers (`packages/session/session-projection/README.md:86`) | Backwards compatibility inside the plugin API. | Not applicable. |

## 11. Recommendations for the Go design

The Go design is not required to read upstream session files ([DH-ADR-2](../design.md#dh-adr-2)). That one fact removes the reason for most of sections 5 and 6: generations, exclusive publication of successors, frozen codecs and the migration chain all exist to keep already-released files readable. The recommendations below keep upstream's model of the log and replace its storage mechanics. Every "Verified by" entry names evidence to be produced later; none exists yet.

<a id="dh-adr-persistence-1"></a>
### DH-ADR-PERSISTENCE-1 Keep the append-only session log as the single source of truth

- **Decision.** Keep. A session is an append-only list of typed events numbered from 0 without gaps. Model history, client state, search documents and statistics are all computed from it. Nothing the model can see may bypass the log, and the request envelope is logged as a full snapshot whenever it changes.
- **Why.** Resume, fork, replay, audit and crash recovery all reduce to "read the log and fold it". This is upstream's strongest design asset and none of it depends on JavaScript.
- **Tradeoff.** Logs grow without bound and carry large values (full tool schemas in each header snapshot, a raw stream in each assistant event), so readers need windows and checkpoints. Every feature that influences the model must define an event.
- **Serves.** Sessions that can be resumed, branched and audited.
- **Verified by.** A local test that rebuilds every recorded model request from the log alone and compares it with what was sent. A real-environment scenario that stops the host mid-turn and resumes against a real model API.
- **Evidence.** `docs/architecture.md:126`, `:130`; `docs/subsystems/session.md:5`, `:185`.

<a id="dh-adr-persistence-2"></a>
### DH-ADR-PERSISTENCE-2 A registered event vocabulary with a small, stable envelope

- **Decision.** Change the mechanism, keep the rules. The envelope keeps `seq`, `time`, `type`, `data`, `ignorable`, and the surface placement (`append` or `replace` with cited sources). Event types are registered at start-up in a Go registry that records the type name, its payload struct, its payload version and whether it is a surface event. An unknown type without `ignorable` refuses the session. Plugins that run as separate processes ([DH-ADR-4](../design.md#dh-adr-4)) may write only opaque, always-ignorable, never-surface records under a `plugin:` namespace. The three dead upstream events (`schedule/change`, `subagent/model-selection-policy`, `working-directory/change`) are not carried over.
- **Why.** Go has no declaration merging, and third-party code is out of process. A registry gives one place to list, validate and document every persisted type, which upstream can only do with a generator.
- **Tradeoff.** A plugin process cannot add a model-visible event type; it reaches the model through events the host already defines (an injected user message, a tool result). Adding a first-party event means a code change and a registry entry.
- **Serves.** A reviewable persisted vocabulary and safe handling of data from other builds.
- **Verified by.** Local tests: duplicate names are rejected; every registered type round-trips; an unknown required type refuses; an unknown ignorable record is preserved unchanged.
- **Evidence.** `docs/subsystems/session.md:11`, `:276-286`, `:294`, `:788`; `docs/persistence-catalog.md:809`, `:968`, `:1326`.

<a id="dh-adr-persistence-3"></a>
### DH-ADR-PERSISTENCE-3 One SQLite database per harness home is the session store, behind a narrow interface

- **Decision.** Change. The live store is one SQLite database in WAL mode holding a `sessions` table (header fields as columns) and an `events` table keyed by `(session, seq)` with `time`, `type`, a payload codec number and the payload as JSON. Callers use a Go interface equivalent to upstream's service and handle (create, open for read or write, stat, list with paging and filters, flush), and one contract test suite that any other engine would have to pass. Per-session JSONL files are not the live format. The payload codec column exists from the first version so that compression can be added later without a schema change.
- **Why.** Nearly all of upstream's file-backend code manages atomicity by hand: torn-tail and torn-frame recovery, no-overwrite publication, directory sync chains, a Windows foreign-function rename, a native lock addon, header scans for listing, and revision fingerprints. A transactional engine provides each of these by construction. Upstream already ships this alternative and uses it to solve fork prefix sharing and group commit.
- **Tradeoff.** A session is no longer a plain file someone can read or copy by itself; [DH-ADR-PERSISTENCE-12](#dh-adr-persistence-12) restores that as an export. One damaged database affects every session, where upstream's damage is per file; mitigations are an integrity check, online backup and export. WAL needs a local filesystem. A pure-Go driver is slower than C SQLite and must be measured before it is accepted.
- **Serves.** Crash-safe storage with far less code, and the paged, filtered listing the server-rendered UI needs ([DH-ADR-3](../design.md#dh-adr-3)).
- **Verified by.** The contract suite. A benchmark of append and flush under the checkpoint policy and of listing with ten thousand sessions. Real-environment runs on Windows, Linux and macOS.
- **Evidence.** `packages/session/session-persistence-jsonl/README.md:79`, `:89-95`, `:113`, `:166-173`; `packages/session/session-persistence-sqlite/README.md:12`, `:63`, `:75`; `packages/session/session-persistence/README.md:155-156`.

<a id="dh-adr-persistence-4"></a>
### DH-ADR-PERSISTENCE-4 Crash safety from transactions, durability from an explicit flush barrier

- **Decision.** Keep the contract, change the mechanism. An append is one transaction inserting a contiguous batch; the `(session, seq)` key plus a next-`seq` check makes gaps, duplicates and interleaved writers impossible. "Appended" means accepted and ordered; "flushed" means committed with a full sync. One writer goroutine per database commits pending batches from all sessions together, replacing upstream's 200 ms window. The checkpoint policy is kept as is: flush before a model request, before a top-level tool body, and at each step boundary, and do not proceed if the flush fails. Write ownership is a lease row per session carrying an epoch that every append transaction checks; the owner proves it is alive by holding an operating-system file lock that the kernel releases when the process dies.
- **Why.** What upstream promises is right; how the file backend delivers it is where the code and the platform-specific risk are. Checking the epoch inside the transaction means a stalled writer that wakes up cannot damage the log, which is the reason upstream refuses lease expiry.
- **Tradeoff.** Every flush is a disk sync, so throughput is bounded by sync latency, as it is upstream. A writer that lost its lease learns at its next append, not at the moment it lost it.
- **Serves.** No lost or duplicated work after a crash; no side effect without a durable record of the request that caused it.
- **Verified by.** Local: fault injection at each step of a commit, asserting the stored prefix; a test that kills the process in a loop and asserts a contiguous prefix containing the last flushed event; a two-process test for lease takeover and rejection of the stale writer. Real power loss cannot be tested locally: the stated guarantee is what SQLite's WAL with full synchronisation provides on the filesystem in use.
- **Evidence.** `docs/subsystems/persistence.md:106`, `:381`; `packages/session/session-checkpoint-policy/README.md:48`, `:52`; `packages/session/session-persistence-jsonl/src/storage.ts:37`; `packages/session/session-persistence-jsonl/src/lease.ts:1-13`; `packages/session/session-persistence-sqlite/src/sqlite.ts:223-226`.

<a id="dh-adr-persistence-5"></a>
### DH-ADR-PERSISTENCE-5 Do not read upstream format generations; no migration chain

- **Decision.** Drop. The Go store reads only its own format. Upstream formats 0 to 3 are never supported. Upstream format 4 is not read by the store either; a one-way importer for it may be added later as a separate command outside the store's read path. That importer is deferred scope, not a condition for completing the store; the owner has since decided that it is built, as a separate command scheduled after the core ([DH-ADR-11](../design.md#dh-adr-11)). Someone holding older upstream logs first upgrades them with upstream, whose write open publishes a format-4 successor and which ships a bulk migration script.
- **Why.** The chain exists because upstream released format 0 and then changed it four times. The Go harness has released nothing. Porting 7,485 lines of frozen readers, two of which renumber every event and one of which must scan sibling sessions, would import exactly the debt [DH-ADR-2](../design.md#dh-adr-2) is meant to leave behind.
- **Tradeoff.** People who used upstream cannot open their history in the Go harness on day one. Upstream's recorded sessions cannot be used as test fixtures, which [DH-ADR-2](../design.md#dh-adr-2) already accepts.
- **Serves.** A store with one format to reason about.
- **Verified by.** Review: no Go package depends on a legacy codec. If the importer is built, a real-environment scenario imports logs written by a real upstream release and compares derived history.
- **Evidence.** `docs/architecture.md:128`; `docs/cookbook/adding-a-session-format-version.md:121-130`; section 6.3.

<a id="dh-adr-persistence-6"></a>
### DH-ADR-PERSISTENCE-6 Evolve the schema per event type, not per log

- **Decision.** Change. Three layers, one rule each. (a) Store layout: an integer in the database; forward-only migrations applied in one transaction at open, after an automatic backup copy; a database newer than the binary is refused with a message to upgrade the harness. (b) Event payloads: each registered type has an integer payload version stored with every row; older versions are converted to the current struct by registered functions when read; stored rows are never rewritten; adding an optional field needs no new version. (c) Review gate: a test builds a canonical description of every registered payload struct, hashes it, and compares it with a committed file; changing a persisted type fails until that file is updated in the same change with a one-line compatibility note. A small set of recorded rows per type and version must keep decoding.
- **Why.** Upstream versions the whole log, so a change to one event's shape can force a rewrite and renumbering of every event. Versioning each payload keeps a change local. Upstream already treats "add a higher `data.version` and keep reading the old one" as a safe same-version change, so this generalises its own cheapest path. The gate keeps upstream's real insight, that a persisted type must not change silently, without the bilingual records and schema snapshots.
- **Tradeoff.** A change that must restructure across events cannot be written as a per-event conversion; it needs an explicit store migration and is deliberately expensive. An in-place migration loses upstream's safety net of an untouched source file; the backup replaces it. Every conversion function must be kept until a recorded decision drops it.
- **Serves.** Format changes that cost a function and a test instead of a package and a review process.
- **Verified by.** The hash test; the recorded-row decode test; a migration test from every earlier store layout; a refusal test for a newer layout.
- **Evidence.** `docs/cookbook/adding-a-session-format-version.md:23`, `:31`; `docs/persistence-changes/README.md:33-42`, `:51-63`; `docs/session-format-status.md:20-24`; `docs/persistence-catalog.md:16`.

<a id="dh-adr-persistence-7"></a>
### DH-ADR-PERSISTENCE-7 Projections are registered pure folds whose checkpoints commit with the events they reflect

- **Decision.** Keep the model, change the storage. A projection is a key, a state version, an initial state built from the header, and a pure `apply(state, event)` that also reports whether the state changed, with an optional client view. One registry drives every committed event through every unit in `seq` order under the session's append lock, so a caller can read a history page and all client views at one `seq`. Checkpoints (state, state version, the `seq` covered) live in a table of the same database and are written in the same transaction as the events they cover, at turn end, at session close and every N events. A checkpoint with a different state version is discarded and refolded, never migrated. The model's message history is the first projection.
- **Why.** Upstream's "the log leads, the cache follows" ordering, its stale and backed-up records, and its identity matching on four header fields all exist because the cache is a different store from the log. In one transaction they cannot disagree.
- **Tradeoff.** Units must be deterministic and cheap, because a slow one slows every append. Long sessions depend on checkpoints to open quickly.
- **Serves.** Current state without replay, and a consistent page-plus-state read for the server-rendered UI.
- **Verified by.** A property test that checkpoint plus suffix replay equals full replay for every unit; a test that a version change discards the checkpoint; a race-detector run of concurrent append and snapshot.
- **Evidence.** `docs/subsystems/session-projection.md:55-105`, `:155`, `:235`; `packages/session/session-projection-cache/README.md:57`, `:65`, `:67`, `:127-130`.

<a id="dh-adr-persistence-8"></a>
### DH-ADR-PERSISTENCE-8 Full-text search is SQLite FTS5 in the same database

- **Decision.** Change. One FTS5 table holds a semantic document per event (session, `seq`, type, time, text), written in the same transaction as the event. Upstream's extraction rule is kept: index messages, tool calls and results, todos and failure detail; skip reasoning, structural events and stream chunks. Query text is always bound as one literal phrase. Ranking is deterministic and paging uses the sort key, not a corpus generation. A rebuild command recreates the index from the events. The tokenizer is an open question.
- **Why.** Upstream's index is a separate database that must be reconciled against the store on every search, and whose cursors break whenever any session changes. Writing the document with the event removes the reconciliation, the revision tokens and the live-versus-stored shadow tables.
- **Tradeoff.** The cost of indexing is paid on every append instead of at search time. Whether an event is current or shadowed changes when a later replacement lands, so that class must be computed at query time or updated by the replacing transaction.
- **Serves.** Search that is always current, with one store to back up.
- **Verified by.** Local tests: characters with meaning in FTS syntax are matched literally; ranking is stable across runs; the incrementally built index equals a rebuilt one.
- **Evidence.** `docs/subsystems/session-query.md:111`, `:147`, `:151`; `packages/session-query/session-query-sqlite/README.md:59-63`, `:87-89`, `:104`, `:144-146`; `packages/session-query/session-query-sqlite/src/schema.ts:127-135`.

<a id="dh-adr-persistence-9"></a>
### DH-ADR-PERSISTENCE-9 Machine state in tables, blobs in files, user configuration in files

- **Decision.** Drop the storage hub, its two interchangeable backends and its two file layouts. State the program owns (workspace registry, schedules, permission stores) becomes tables, or one namespaced key-value table with a version per record, in the same database, behind a small typed Go API that keeps upstream's order of effects: disk first, then memory, then a change notification. Attachments stay content-addressed files verified on read. Spill stays plain files the model reads with its ordinary file tools. Both gain what upstream lacks: retention that knows which sessions still reference an object. Configuration a person edits stays in files.
- **Why.** The hub's generality (backend table, facets, forms, routes) serves six domains, three of them experimental, and gives two failure models to document. Blobs belong in files because tools and the sandbox address them by path.
- **Tradeoff.** Operators lose readable JSON state files and get a database plus an export command. A blob and the event that references it are not one transaction, so the order "store the blob, then append the event" must be kept; a crash in between leaves an unreferenced blob, which is garbage, never a dangling reference.
- **Serves.** One durable store with one failure model; bounded disk use.
- **Verified by.** Local tests for the key-value API's ordering and for refusing an unknown record version; a test that kills the process between blob write and event append and checks that no event references a missing object; a retention test that an object referenced by a fork survives deletion of its parent.
- **Evidence.** `docs/subsystems/storage.md:5`, `:11`, `:47`, `:143`; `packages/storage/storage-json/README.md:143-145`; `packages/storage/storage-sqlite/README.md:130-134`; `docs/subsystems/attachment.md:5-7`; `packages/attachment/attachment-local/README.md:84`, `:137`; `docs/subsystems/spill.md:32-33`, `:92`.

<a id="dh-adr-persistence-10"></a>
### DH-ADR-PERSISTENCE-10 The store never repairs meaning; the resuming writer closes an interrupted turn; deletion is added

- **Decision.** Keep, and add what is missing. The store returns the log as written. On resume, the writer that holds the lease appends closers: an error result for each unanswered tool call (distinguishing "never started" from "outcome unknown"), a missing `step/end`, and `turn/end` with reason `interrupted`. Read-only observers apply the same closers in memory. Committed events are never rewritten. New: an explicit delete-session operation and a retention policy, which remove a session's events, checkpoints and index rows in one transaction and release its blob references.
- **Why.** Truncating an interrupted turn would destroy durable work, and telling the model that a tool's outcome is unknown is the honest state. Upstream's lack of any deletion is a recorded limitation that a product cannot keep.
- **Tradeoff.** An interrupted turn is closed, never continued. A tool with side effects may have run; the model is told to verify, not to retry blindly. Deletion is irreversible and must refuse while a writer holds the session.
- **Serves.** Honest recovery, and storage a user can clean up.
- **Verified by.** Local tests producing the right closers for an interruption at each point of a step. A real-environment scenario: Given a session running a long shell command, When the host process is killed and restarted, Then the resumed session shows the turn as interrupted with an unknown-outcome result and accepts a new prompt; Cleanup deletes the session and confirms its rows and unshared blobs are gone.
- **Evidence.** `docs/subsystems/persistence.md:110-112`; `packages/session/session-persistence-jsonl/README.md:149`, `:169`; `packages/session/session-persistence/README.md:155-157`; `docs/subsystems/session.md:749`, `:774`.

<a id="dh-adr-persistence-11"></a>
### DH-ADR-PERSISTENCE-11 Immutability by ownership and copying, not by freezing

- **Decision.** Change. Committed events are immutable by interface: the session hands out values or read-only accessors, and a payload is decoded into a struct the reader owns. There is no run-time freeze, no aliasing flag on reads and no validate-by-copy pass on append.
- **Why.** Upstream's freeze, snapshot and `detached` or `shared-frozen` machinery compensates for shared mutable objects. Go's typed values and explicit copies give the same protection where it matters.
- **Tradeoff.** Go cannot make a slice or map deeply immutable, so the rule depends on API discipline and review. Copying costs allocations on hot read paths, which calls for a cache of decoded events.
- **Serves.** Logged history that cannot be edited through a reader.
- **Verified by.** Race-detector runs of concurrent readers and one appender; a review check that no exported method returns an internal slice or map.
- **Evidence.** `packages/core/session/src/index.ts:542`, `:697`; `packages/core/session/README.md:46`, `:125`; `docs/subsystems/persistence.md:245`.

<a id="dh-adr-persistence-12"></a>
### DH-ADR-PERSISTENCE-12 A line-per-event export format for portability, not for live storage

- **Decision.** Keep the useful half of JSONL. An export writes one session as a header line followed by one event per line, optionally compressed; an import reads it back. It is used for backup, sharing, bug reports, test fixtures and as the target shape of any importer. It carries its own format number.
- **Why.** "One readable artifact per session" has real value for people and external tools. It does not require the live store to be that artifact.
- **Tradeoff.** An export is a snapshot, and there are two formats to keep in step.
- **Serves.** Portability and inspection without file-based live storage.
- **Verified by.** A round-trip test: export, import, export again, and compare the two files byte for byte.
- **Evidence.** `packages/session/session-persistence-jsonl/README.md:32`, `:168`; `docs/subsystems/persistence.md:116-118`.

### 11.1 Verdict on each upstream decision and debt item

| Upstream decision or debt | Verdict | How the Go design treats it | Decision |
|---|---|---|---|
| Append-only event log as the source of truth | Keep | Unchanged in substance. | 1 |
| "Model-visible means logged"; full request-header snapshots | Keep | Unchanged. | 1 |
| One settled event per model attempt with its raw stream | Keep | The stream goes in the optional `aux` column of the event's own row, which history reads skip ([DH-ADR-STORE-2](../areas/session-store.md#dh-adr-store-2)); whether that is fast enough is unmeasured. | 1 |
| Event map extended by declaration merging; generated catalog | Change | A run-time registry; the catalog is rendered from it. | 2 |
| `ignorable` flag; refuse unknown required types | Keep | Unchanged. | 2 |
| Plugin records in a `plugin:` namespace | Keep | Becomes the only way a plugin process persists into a session. | 2 |
| Dead event types kept for old logs (debt) | Resolved | Not carried; there are no old Go logs. | 2 |
| Service plus per-session handle, single writer | Keep | Same shape as a Go interface. Several `dsh` processes may open one home ([DH-ADR-12](../design.md#dh-adr-12)); a session still has one writer, the process that holds its lease. | 3, 4 |
| Per-session JSONL files, generations, never rename or delete | Drop | No successor files exist to protect. | 3, 5 |
| Zstandard frame per batch with torn-frame recovery | Drop | Nothing in the Go store is compressed in its first layout (a `codec` column reserves it), and a session export is gzip, the only compression an export uses ([DH-ADR-STORE-9](../areas/session-store.md#dh-adr-store-9)). Zstandard is read only by the deferred import command, which must decode upstream's frames (`DH-LIB-NODEZLIBZSTD` in [../libs/node-fs-path-os-zlib.md](../libs/node-fs-path-os-zlib.md)). | 3, 12 |
| Foreign-function rename and native lock addon (debt) | Not applicable | Exists only because of Node. | 3, 4 |
| Unpaginated, unfiltered `list` (debt) | Resolved | Listing is an indexed, paged query. | 3 |
| Buffered append, explicit flush, checkpoint policy | Keep | Same contract over transactions. | 4 |
| No lease expiry | Keep, and add an epoch | Nothing expires by time. A holder is alive while it holds its kernel file lock; taking over a dead holder's session bumps the epoch, and every append checks the epoch inside its transaction, so a stale writer is refused ([../design.md](../design.md) §8, CF-10; [DH-ADR-STORE-4](../areas/session-store.md#dh-adr-store-4)). The first text of this row said expiry "becomes safe"; no document uses time-based expiry. | 4 |
| Adjacent migration chain, formats 0 to 4 | Drop | The store reads only its own format. Upstream's current format enters through a separate, deferred, one-way import command that refuses a file it cannot map ([DH-ADR-11](../design.md#dh-adr-11); [DH-ADR-STORE-11](../areas/session-store.md#dh-adr-store-11)). | 5 |
| Type fingerprints, change records, finalization and release records | Change | One hash test and a short note per change. | 6 |
| Projection units and registry | Keep | Same model with an explicit "changed" result. | 7 |
| Projection cache in a separate JSON store | Change | Checkpoints commit with their events. | 7 |
| Separate derived search database with reconciliation | Change | Index rows commit with their events. | 8 |
| Storage hub with JSON and SQLite backends | Drop | Tables in the one database. No table holds approval rules: nothing about approvals is stored across sessions ([DH-ADR-16](../design.md#dh-adr-16)). | 9 |
| No cross-process lock on JSON state (debt) | Resolved | The database serialises writers. | 9 |
| Content-addressed attachments stored before the event | Keep | Unchanged, plus retention. | 9 |
| Spill files with a locator and a retrieval hint | Keep | Unchanged, plus reference-aware retention. | 9 |
| The reader, not the store, closes an interrupted turn | Keep | Unchanged. | 10 |
| No deletion or retention anywhere (debt) | Resolved | Delete and retention are part of the store; retention is off by default ([DH-ADR-STORE-7](../areas/session-store.md#dh-adr-store-7)). | 9, 10 |
| Session-log upload to the vendor on by default ([decisions-1.md](decisions-1.md), row 97) | Change | Off. Upload is an opt-in component under the one off-switch ([DH-ADR-9](../design.md#dh-adr-9); [DH-ADR-STORE-10](../areas/session-store.md#dh-adr-store-10)). | 9 |
| Deep-freeze immutability | Not applicable | Exists only because of JavaScript. | 11 |

### 11.2 Go modules checked

Checked on 2026-11-18 with `go list -m -json <module>@latest` and, for the first three, `go mod download -json` followed by reading the `LICENSE` file in the module cache (Go 1.27.4, default proxy and checksum database). Choosing and specifying a library belongs to the library specifications; this table only records that the modules named in this document exist.

| Module | Version, release date | Licence | Purpose here | What was checked |
|---|---|---|---|---|
| `modernc.org/sqlite` | v1.62.0, 2026-11-07 | BSD 3-Clause | SQLite driver with no C compiler needed. | Module, version, licence file. Its generated source records the build flags `-DSQLITE_ENABLE_FTS5` and `-DSQLITE_ENABLE_JSON1` on line 1 of `lib/sqlite_windows.go`, and contains the FTS5 trigram tokenizer. Performance not measured. |
| `github.com/klauspost/compress` | v1.20.4, 2026-11-09 | The licence file contains BSD 3-Clause, Apache 2.0 and MIT texts for different parts | Reading upstream's Zstandard frames in the deferred import command ([DH-ADR-11](../design.md#dh-adr-11)). Not used for exports, which are gzip. | Module, version, licence file. Which text governs the `zstd` package was not determined. |
| `golang.org/x/sys` | v0.50.0, 2026-11-06 | BSD 3-Clause | File locks for the process-liveness lock. | Module, version, licence file. |
| `github.com/ncruces/go-sqlite3` | v0.37.0, 2026-11-07 | Not read | Alternative SQLite driver. | Existence and version only. |
| `github.com/mattn/go-sqlite3` | v1.14.53, 2026-11-05 | Not read | SQLite driver that needs a C compiler. Not a candidate for `dsh`, which is pure Go ([DH-ADR-15](../design.md#dh-adr-15)); kept as the speed reference in [../libs/sqlite.md](../libs/sqlite.md). | Existence and version only. |

## 12. Open questions

1. **Driver choice and speed.** Is a pure-Go SQLite driver fast enough for the checkpoint policy's sync rate and for large assistant payloads? Still open: the choice in [../libs/sqlite.md](../libs/sqlite.md) is provisional until its measurements are taken, and a cgo driver is not available as a fallback ([DH-ADR-15](../design.md#dh-adr-15)). Zstandard is not in the Go standard library; the export format uses gzip ([DH-ADR-STORE-9](../areas/session-store.md#dh-adr-store-9)).
2. **Processes sharing one home.** Settled by the owner: several `dsh` processes open the database themselves, with per-session leases ([DH-ADR-12](../design.md#dh-adr-12)). Area A05 specifies takeover, how one process sees another's commits and once-per-home duties ([DH-ADR-STORE-4](../areas/session-store.md#dh-adr-store-4), [DH-ADR-STORE-6](../areas/session-store.md#dh-adr-store-6)).
3. **Fork storage.** Settled by area A05: a fork copies the parent prefix ([DH-ADR-STORE-2](../areas/session-store.md#dh-adr-store-2)).
4. **Where assistant streams live.** Settled by area A05: the payload has two columns, `data` and an optional `aux` for the part history reads skip, which holds the raw stream ([DH-ADR-STORE-2](../areas/session-store.md#dh-adr-store-2)). If measurement shows the same row is too slow, it moves to a side table by a layout migration.
5. **Search tokenizer.** `unicode61` does not segment Chinese, Japanese or Korean text, and upstream documents its token-only matching as a limit. The trigram tokenizer gives substring matching at the cost of index size. Needs a decision with measurements.
6. **Importer for upstream format 4.** Settled by the owner: a separate, deferred, one-way import command for upstream's current format, outside the store's read path and scheduled after the core ([DH-ADR-11](../design.md#dh-adr-11)). Area A05 owns it ([DH-ADR-STORE-11](../areas/session-store.md#dh-adr-store-11)). The Go event vocabulary differs, so it is a translation that refuses whole files, not a copy.
7. **Network and synced folders.** Settled by area A05: the home must be a local filesystem that supports WAL, and the store says so when it cannot enter WAL mode ([DH-ADR-STORE-8](../areas/session-store.md#dh-adr-store-8)). Upstream's own SQLite storage backend offers rollback-journal modes for that case (`packages/storage/storage-sqlite/README.md:53`); the Go store does not.
8. **File permissions and encryption at rest.** Settled by area A05: files are owner-only and there is no encryption at rest ([DH-ADR-STORE-8](../areas/session-store.md#dh-adr-store-8)).
9. **Retention defaults.** Settled by area A05 for sessions: retention is off by default ([DH-ADR-STORE-7](../areas/session-store.md#dh-adr-store-7)). Attachments and spill files follow area A07 ([../areas/storage.md](../areas/storage.md)).
