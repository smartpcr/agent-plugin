# Session views (projections, titles, statistics, telemetry, query and search): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) (`2e39e70`) and are cited as `path:line` relative to the upstream root. Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `VIEW`. Nothing here has been built, run or tested.

| | |
|---|---|
| **Area key** | `VIEW` (agent A06) |
| **Upstream packages** | 15: eleven under `packages/session` (`session-projection`, `session-projection-cache`, `session-stats`, `session-turn-outline`, `session-title`, `session-title-llm`, `session-title-first-prompt-llm`, `session-telemetry`, `session-telemetry-otel`, `working-directory`, `message-quotes`) and four under `packages/session-query` |
| **Serves outcomes** | [DH-O-4](../design.md#dh-o-4), [DH-O-5](../design.md#dh-o-5), [DH-O-6](../design.md#dh-o-6), [DH-O-7](../design.md#dh-o-7), [DH-O-8](../design.md#dh-o-8), [DH-O-10](../design.md#dh-o-10), [DH-O-12](../design.md#dh-o-12), [DH-O-13](../design.md#dh-o-13), [DH-O-14](../design.md#dh-o-14) |
| **Depends on areas** | A01 (session, event registry, committed-event feed), A05 (store transaction, tables, leases, delete), A02 (inbox, turn boundaries), A03 (tool pipeline, prompt context), A04 (LLM runtime), A07 (workspace registry, attachments), A08 (filesystem seam), A10 (sandbox policy), A17 (feedback, identity, secrets), A18 (exporter, off-switch), A19 (routes and event stream), A24 (commands) |
| **Priority** | P0 for the fold mechanism, query core and working directory, because the first working harness needs current state, a session list and a directory for tools. P1 for titles, search, list tools, export and the richer folds. P2 for session upload |

## 1. Purpose

The session log is the only source of truth, and nobody wants to read a log. This area turns it into the things people and programs actually look at: the current title of a session, its directory, a list of sessions ordered by last activity, an outline of turns, running statistics, a search box, an exportable transcript. Each of these is a **view**: a value computed from the log that can be thrown away and computed again.

Without this area every reader would replay whole logs, two readers could show different moments of the same session, a restart would open long sessions slowly, and search would need a second database that drifts from the first. The area also holds two things that ride on views: the model call that writes a title, which must not become a side channel around the log, and the upload of a session to a collector, which must not happen unless the user asked for it ([DH-ADR-9](../design.md#dh-adr-9)).

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **View** | Any value derived from committed session events. It is never written by hand and never read by the model unless it is first logged. | Names the whole area: everything here can be rebuilt from the log. |
| **Committed event** | An event the session has accepted and numbered (`seq`). It is durable after the flush that covers it. | A fold may only see facts that cannot be withdrawn. |
| **Fold, projection** | A pure function `apply(state, event)` with an initial state, registered under a key. A *value projection* keeps one state value; a *row projection* also writes rows into a derived table. | Current state without replay; one mechanism for state, indexes and search. |
| **State, client view** | State is what the fold keeps. The client view is the part shown outside the host. | A fold may need bookkeeping (an open step, a draft) that no reader should see. |
| **State version** | An integer on a definition. Stored state with another version is ignored. | A changed fold must never continue from state the old fold produced. |
| **Projection checkpoint** | Stored state with the `seq` it covers, committed in the same transaction as the events up to that `seq`. | Opening or listing a session must not replay its log, and stored state must never be ahead of stored events. |
| **Cut, `asOfSeq`, head** | A cut is one position in one session's log. `asOfSeq` is the cut a value reflects. The head is the last committed `seq`. | A page of history and the state beside it must describe the same moment. |
| **Current, behind, pending** | The three states of a value handed to a reader: equal to the head; valid at an older cut; not available yet (a rebuild is running). | A reader is told what it holds instead of receiving a silent default. |
| **Catch-up, rebuild** | Catch-up folds the events after a stored cut. A rebuild folds from `seq` 0 because no usable state exists. | The two costs differ by orders of magnitude and are reported differently. |
| **Corpus** | All sessions in the harness home that a caller is allowed to see. | Queries are defined over it, whichever process wrote each session. |
| **Visibility** | A value that says which sessions a caller may see. Every query needs one. | Separates access (who may read) from ownership (who may write, by lease). |
| **Surface class** | `current` (the model sees it), `shadowed` (replaced, for example by compaction) or `log-only`. | Search and traces must agree with what the model was shown. |
| **Lineage** | The parent chain and descendant tree created by forks and subagents. | A fork inherits a prefix of its parent; readers must tell inherited from own events. |
| **Semantic document** | The searchable text of one event: message text, tool arguments and results, todos, failure detail. | Structural events and reasoning are noise in a search result. |
| **FTS5, CJK** | FTS5 is SQLite's full-text index. CJK is Chinese, Japanese and Korean text, written without spaces between words (Korean with spaces but with endings attached). | The stock index splits on spaces, so CJK needs preparation ([libs/sqlite.md](../libs/sqlite.md), `DH-ADR-SQLITE-3`). |
| **Auxiliary model call** | A model request that is not a step of the agent's conversation, such as title generation. | It costs money and sends user text to a provider, so it must be logged and attributed like any other. |
| **Title source, pin** | A title comes from the built-in fallback, a provider or the user. A user title is pinned: automatic titles stop. | The user's choice must not be overwritten by a model. |
| **Session upload, consent** | Sending a session's events to a collector over OTLP (the OpenTelemetry log protocol). Consent is one explicit confirmation by the user for one prefix of one session. | Upstream calls this "session telemetry"; the name hides that it carries the whole conversation. |
| **Working directory, original project** | The working directory is where a session's relative paths resolve now. The original project is the directory in the immutable session header. | A session can move into another checkout without changing which project it belongs to. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.session.session-projection` | `dsh-session-projection` | definition and driver | Registry `ctx.sessionProjections`: one subscription to `session/event`, every committed event through every unit, snapshots and a change feed (`docs/subsystems/session-projection.md:5`, `:235`). |
| `UP-PKG-packages.session.session-projection-cache` | `dsh-session-projection-cache` | provider | Stores one checkpoint document per session in a separate JSON store, written behind the log (`packages/session/session-projection-cache/README.md:57`, `:65`). |
| `UP-PKG-packages.session.session-stats` | `dsh-session-stats` | consumer of the registry | Unit `sessionStats`: turns, steps, model, tool, first-token and decode times over the whole log (`packages/session/session-stats/README.md:40-47`). |
| `UP-PKG-packages.session.session-turn-outline` | `dsh-session-turn-outline` | consumer of the registry | Unit `turnOutline`: one entry per turn with a prompt and response preview (`packages/session/session-turn-outline/README.md:40-47`). |
| `UP-PKG-packages.session.session-title` | `dsh-session-title` | definition and provider | `ctx.sessionTitle`: latest-wins `session/title` events, a deterministic fallback, rename, refresh, one optional provider (`packages/session/session-title/README.md:32`, `:57`). |
| `UP-PKG-packages.session.session-title-llm` | `dsh-session-title-llm` | shared library | Executes a title request: route, byte ceiling, deadline, request logging, stream assembly (`packages/session/session-title-llm/README.md:38`). |
| `UP-PKG-packages.session.session-title-first-prompt-llm` | `dsh-session-title-first-prompt-llm` | provider | Titles a fresh session from its first human message with one model call (`packages/session/session-title-first-prompt-llm/README.md:32`). |
| `UP-PKG-packages.session.session-telemetry` | `dsh-session-telemetry` | definition | Capture coordinator, record vocabulary, redaction waterfall, hand-off cursor (`docs/subsystems/session-telemetry.md:5`, `:144`). |
| `UP-PKG-packages.session.session-telemetry-otel` | `dsh-session-telemetry-otel` | provider | Uploads a session prefix as OpenTelemetry log records after feedback (`packages/session/session-telemetry-otel/README.md:36`, `:72`). |
| `UP-PKG-packages.session.working-directory` | `dsh-working-directory` | definition and provider | `ctx.workingDirectory`: one logged directory per session, validation, recovery, model context (`docs/subsystems/working-directory.md:5-17`). |
| `UP-PKG-packages.session.message-quotes` | `dsh-message-quotes` | client plugin | A "quote selection" action and blockquote display for user messages (`packages/session/message-quotes/README.md:28`, `:38`). |
| `UP-PKG-packages.session-query.session-query` | `dsh-session-query` | definition with concrete reads | `ctx.sessionQuery`: list, observe, bounded reads, filters, traces, lineage; search is abstract (`packages/session-query/session-query/README.md:34-44`, `:84`). |
| `UP-PKG-packages.session-query.session-query-sqlite` | `dsh-session-query-sqlite` | provider | The search half on SQLite FTS5 in a separate derived database (`packages/session-query/session-query-sqlite/README.md:87-89`). |
| `UP-PKG-packages.session-query.tool-session-list` | `dsh-tool-session-list` | model-facing consumer | Tools `list_workspace` and `list_session` (`docs/tool-catalog.md:1872-1920`). |
| `UP-PKG-packages.session-query.session-log-export` | `dsh-session-log-export` | host routes and client UI | ZIP download of a session with descendants and attachments, and "copy as Markdown" (`packages/session-query/session-log-export/README.md:28`, `:101`). |

### 3.2 Behaviour

**Projections.** A domain registers a unit: key, state schema, `init(header, inheritedEventCount)`, a synchronous `apply`, an optional client `view`, and `stateVersion` (`docs/subsystems/session-projection.md:55-105`). The registry drives every committed event through every unit, in registration order. A unit that ignores an event must return the same object, because "no change" is detected by reference identity (`packages/session/session-projection/README.md:54`). `snapshot(session)` is synchronous, so a carrier that reads it in the same tick as a history page gets both at one `asOfSeq` (`docs/subsystems/session-projection.md:155`). A list row carries only the "summary" keys; `turnOutline`, `sessionStats`, `todos`, `imageLimits`, `contextPressure` and `contextBreakdown` are excluded (`packages/session/session-projection/README.md:106`). State lives in memory; a unit registered late, or a session older than the registry, folds lazily over the in-memory log (`docs/subsystems/session-projection.md:235`).

**Checkpoints.** The cache package writes the whole checkpoint document at session creation, at every `turn/end`, at disposal, and in between every 200 events or 5 seconds as the base bundle configures (`packages/session/session-projection-cache/README.md:57`; `packages/bundle/base/cordis.patch.yml:197-201`). It flushes the log first, so the cache can be behind the log and never ahead (`README.md:65`). A record is accepted only if four header fields match (`formatVersion`, `createdAt`, `cwd`, `isSeeded`) and, for folding, the inherited event count too (`README.md:61`, `:65`). A row whose version differs is discarded, never migrated (`README.md:81`).

**Statistics and outline.** `sessionStats` counts `step/end` events, because the loop appends exactly one per entered step, and pairs `tool/call` with `tool/result` by call id (`packages/session/session-stats/README.md:67`, `:86`). `turnOutline` anchors each entry at its `turn/start`, fills the prompt from the first human `user/message`, and commits the response preview at `turn/end` (`packages/session/session-turn-outline/README.md:65`). Both are mounted only by the web bundle (`packages/bundle/web-app/cordis.patch.yml:98-104`).

**Titles.** Every accepted title is a log-only `session/title` event with the message `seq`s it was derived from and its source; the latest wins (`docs/subsystems/session-title.md:43-53`). On a human message the service writes the fallback (leading words of the first eligible message) and, for a fresh session that is neither a fork nor a subagent, schedules the provider (`packages/session/session-title/src/index.ts:347-357`, `:520-522`). The provider call starts only after the main request's route is logged (`:540-550`). The shared executor rejects input over `maxInputBytes` (`packages/session/session-title-llm/src/index.ts:247-250`), appends `session/title-llm-request` with the exact system text, messages, route, output cap and reasoning effort, and only then streams the request, tagged `purpose: 'session-title'` (`:265-288`). The response is not logged; an accepted result becomes a `session/title` event. An automatic failure logs a warning and keeps the current title; a late result from superseded work cannot append (`packages/session/session-title/README.md:65`, `:93`). The title never enters the model's history (`README.md:123`).

**Session "telemetry".** The seam can capture every session event as a "ledger" record, live or on demand, and passes each record through the `session-telemetry/record` waterfall, which ships no redaction rule (`docs/subsystems/session-telemetry.md:60`, `:144`). The one shipped backend captures on demand with full history (`packages/session/session-telemetry-otel/src/index.ts:220-221`) and only when a new feedback event of the session itself is appended (`:46-48`); direct `emit()` calls are no-ops (`:251`). What is then sent is the whole log from `seq` 0 to the feedback event, one OTLP log record per event, with an anonymous installation id (`packages/session/session-telemetry-otel/README.md:72`). The base bundle mounts it in `FEEDBACK_ONLY` mode for every user (`packages/bundle/base/cordis.patch.yml:219-222`). Hand-off is best effort: a cursor in a `WeakMap` marks "handed off", not "delivered" (`packages/session/session-telemetry/src/coordinator.ts:58`).

**Query.** Every read resolves one observation of a session: a live session wins, otherwise the stored log is read whole through a read handle and balanced in memory if its last turn was interrupted (`packages/session-query/session-query/README.md:82`, `:108`). `readEvent` returns one event with at most `readWindowMax` (50) neighbours on each side (`README.md:60`). Events are classified `current`, `shadowed` or `log-only` with the same surface fold the model history uses (`docs/subsystems/session-query.md:11`). `traceSession` returns the known ancestors and the descendant tree; `traceEvent` returns what replaced an event and what cites it (`docs/subsystems/session-query.md:226-262`, `:301-331`). Filters are ANDed clauses; the `text` clause is a literal, case-insensitive scan (`:111`).

**Search.** One document per event; query text is one literal phrase; ranking is "more highlighted matches, then shorter documents, then time, session, `seq`"; a cursor goes stale when the corpus changes (`packages/session-query/session-query-sqlite/README.md:59-61`). Before each search the provider lists every stored session, compares revisions and re-reads changed logs (`README.md:104`). The tokenizer is `unicode61`, which matches whole tokens only (`README.md:63`). The shipped bundles turn search off (`packages/bundle/base/cordis.patch.yml:161-165`).

**Tools the model can call.** `list_workspace` and `list_session` return a JSON array, 20 entries by default and at most 100, paged by `offset` (`docs/tool-catalog.md:1872-1920`; `packages/session-query/tool-session-list/src/list.ts:38-57`). `list_session` omits subagent sessions, orders by latest human prompt, and reads every candidate before cutting the page (`list.ts:109-121`). Mounting the tools grants read access to every workspace and session (`packages/session-query/tool-session-list/README.md:32`). The richer query tools (`session_event_read` and others) are in `packages/experimental/tool-session-query`, owned by area X3.

**Working directory.** A projection initialised from the header's `cwd` folds `workspace-root/change` events (`packages/session/working-directory/src/index.ts:113-119`). `get` reads it without I/O; `ensure` validates it and recovers to the original project; `set` validates and records a move that a host operation chose (`docs/subsystems/working-directory.md:9`). The model has no tool that moves it. Relative paths, new processes, instruction and skill discovery, the Files tree and the sandbox `workspace-write` boundary all follow it; existing terminals keep their own directory; project membership stays with the original project (`docs/subsystems/working-directory.md:13`). The model learns the value from a required user-context snapshot, never from the system prompt (`:17`; `src/index.ts:121-142`).

**Export and quotes.** `/export` and a header menu download `dsh-session-<id>.zip` (logs of the session and its descendants, attachments, a list of missing attachments) or copy a Markdown transcript built from the raw committed log (`packages/session-query/session-log-export/README.md:34`, `:81`). A quote is ordinary Markdown: the composer prefixes each selected line with `> `, and the display recognises such lines (`packages/session/message-quotes/README.md:38`, `:59`).

### 3.3 Failure, retry, rollback and cleanup upstream

- **A unit throws.** The error reaches the session's listener containment; listeners still get the changes made before the throw (`packages/session/session-projection/README.md:133`).
- **A checkpoint write fails.** A warning; the cache stays stale; the next write heals it (`packages/session/session-projection-cache/README.md:57`). Nothing ever deletes a checkpoint (`:127`).
- **A plugin unloads.** Its key leaves snapshots; its stored row stops advancing and cold lists serve the older value until the key registers again (`packages/session/session-projection/README.md:171`).
- **A title call fails or is cancelled.** The request event stays in the log with no outcome event; the fallback stays (`packages/session/session-title-llm/README.md:75`).
- **Search index trouble.** A failed reconciliation commits nothing and the next search retries (`packages/session-query/session-query-sqlite/README.md:104`).
- **Upload.** Whatever is queued at a crash is lost; a restart can send prefixes again; there is no outbox, watermark or acknowledgement (`packages/session/session-telemetry-otel/README.md:142`).
- **Directory gone.** `ensure` records a move back to the original project; if that is gone too, every model turn is rejected (`packages/session/working-directory/README.md:85`).
- **Export.** A failure after streaming starts is visible only in the browser's download manager (`packages/session-query/session-log-export/README.md:87`).

## 4. Upstream decisions dissected

The first eleven rows are the decision notes handed to this area (all under `.agents/notes/`); the rest are visible only in code and READMEs. "Go decision" names the record in [§7](#7-decisions) or an earlier one; `VIEW-n` is short for `DH-ADR-VIEW-n`. Paths in the code rows omit their leading `packages/session/`, `packages/session-query/`, `packages/bundle/` or `docs/subsystems/`.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `…2026-08-19-session-projection-mandatory-seam` | A reader of projected state requires the registry; a missing registry or key is an error, never a default. One change frame per committed event. | Keep | A silent default hides a composition error; cordis already fails a component whose required service is missing. | [VIEW-2](#dh-adr-view-2) |
| `…2026-08-25-session-observations-and-projection-owned-client-state` | Every exact read uses one observation with one cut; client values come only from projections. | Keep the rule, change the mechanism | The prepared-session cache and its leases exist because a cold read loads a whole file. A read transaction gives the cut. | [VIEW-4](#dh-adr-view-4) |
| `…2026-09-05-nonofficial-feedback-otel` | Only a new, own, explicit feedback event authorises an upload; inherited feedback does not. Default on. | Keep the consent rule, change the default | The owner decided that nothing leaves the machine by default and that feedback shows what will be sent. | [DH-ADR-9](../design.md#dh-adr-9), [VIEW-7](#dh-adr-view-7) |
| `…2026-09-13-session-working-directory` | One logged directory per session, owned by one service; the header keeps the original project. | Keep | It keeps every tool on the same directory and the prompt prefix stable. | [VIEW-8](#dh-adr-view-8) |
| `…2026-07-27-session-projection-and-command-log` (proposed) | Whole-value state events and registered folds; command runs are logged. | Keep the projection half | Whole values let the latest fragment replace the previous one in a server-rendered page. The command-log half belongs to the commands area (A17). | [VIEW-1](#dh-adr-view-1) |
| `…2026-09-19-projection-cache-listing-identity-and-cached-rows` | The cache's list read matches a four-field identity without the fork cut; clients keep "cached" and "sequenced" values apart. | Replace | Both rules repair a cache that is a different store with its own sequence space. A row keyed by session in the session's own database needs no identity witness, and every `seq` is the log's. | [VIEW-2](#dh-adr-view-2) |
| `…2026-10-12-session-projection-registration-handles` | Registration is awaited and returns a typed handle that stays readable after unload. | Keep typed handles, drop the rest | Readers outlived owners because of upstream's teardown order; the Go port unloads dependents first. The callable-promise shim is JavaScript compatibility. | [VIEW-1](#dh-adr-view-1) |
| `…2026-10-18-session-list-summary-projections` | A list row carries a declared subset of keys. | Keep as a field | The rule is right; the type-level mark is TypeScript. | [VIEW-3](#dh-adr-view-3) |
| `…2026-11-03-session-workspace-root` | Only host operations move the directory; the model's `working_directory` tool is removed; one read. | Keep | A model-chosen directory would move the write boundary without approval. | [VIEW-8](#dh-adr-view-8) |
| `…2026-10-13-text-derived-message-quotes` | A quote is plain Markdown recognised at display time; no stored quote index. | Keep | No metadata to keep in step; the host renderer recognises it. | [DH-D-VIEW-8](#dh-d-view-8) |
| `…2026-09-19-trim-session-query-convenience-api` (proposed) | Remove four whole-log read methods nobody calls. | Adopt | The Go query interface has no method that returns a whole log. | [VIEW-4](#dh-adr-view-4) |
| The registry drives; domains hold no subscriptions | `docs/subsystems/session-projection.md:5` | Keep | One place orders folds and owns the cut. | [VIEW-1](#dh-adr-view-1) |
| "No change" by object identity | `session-projection/README.md:54` | Change | Go has no cheap identity for values; `apply` returns `changed`. | [VIEW-1](#dh-adr-view-1) |
| Units synchronous so a snapshot is a cut | `session-projection.md:52` | Change | Go folds under the session's append lock. | [VIEW-1](#dh-adr-view-1) |
| Every unit sees every event | `session-projection/README.md:170` | Change | A definition declares the event types it consumes. | [VIEW-1](#dh-adr-view-1) |
| Checkpoints in a separate store, written behind the log on timers | `session-projection-cache/README.md:57-65` | Change | They commit with their events ([PERSISTENCE-7](../upstream/session-and-persistence.md#dh-adr-persistence-7)). | [VIEW-2](#dh-adr-view-2) |
| A version mismatch discards, never migrates | `session-projection-cache/README.md:81` | Keep | Derived state is cheaper to rebuild than to migrate. | [VIEW-2](#dh-adr-view-2) |
| Search in a separate derived database, reconciled per search | `session-query-sqlite/README.md:87-88` | Change | Index rows commit with events ([PERSISTENCE-8](../upstream/session-and-persistence.md#dh-adr-persistence-8)); here they are a row projection. | [VIEW-3](#dh-adr-view-3) |
| Query text is one literal phrase | `session-query-sqlite/README.md:59` | Keep | Search operators typed by a user or a model stay data. | [VIEW-5](#dh-adr-view-5) |
| `unicode61`; trigram measured and rejected | `session-query-sqlite/README.md:158` | Change | CJK text is prepared in Go before indexing. | [VIEW-5](#dh-adr-view-5) |
| Cursors bound to a corpus generation | `session-query-sqlite/README.md:89` | Change | Keyset cursors on per-document sort keys do not go stale. | [VIEW-5](#dh-adr-view-5) |
| Search off in shipped bundles | `base/cordis.patch.yml:153-165` | Change | Inference: off avoids importing Node's experimental SQLite module; that cost does not exist in Go. Indexing is on unless configured off. | [VIEW-5](#dh-adr-view-5) |
| The query service does not authorise callers | `session-query/README.md:160` | Change | Every query takes a visibility. | [VIEW-4](#dh-adr-view-4) |
| Title request logged before dispatch; response not logged | `session-title-llm/src/index.ts:273-288` | Keep and complete | The request rule is right; the missing outcome event breaks "one attempt, one event". | [VIEW-6](#dh-adr-view-6) |
| At most one title provider | `session-title/README.md:141` | Change | Providers register by id; configuration selects one. | [VIEW-6](#dh-adr-view-6) |
| Forks and subagent sessions never get an automatic title | `session-title/src/index.ts:522` | Change for forks, keep for subagents | Two list rows with one title are indistinguishable. | [VIEW-6](#dh-adr-view-6) |
| No redaction rules shipped; a listener may replace the chain | `session-telemetry.md:144` | Change | A built-in baseline that cannot be removed or skipped. | [VIEW-7](#dh-adr-view-7) |
| Live capture and operational records (`agent-error`, `shutdown`) | `session-telemetry/README.md:40` | Drop | No shipped backend uses them (`session-telemetry-otel/src/index.ts:220`), and continuous capture contradicts per-submission consent. | [VIEW-7](#dh-adr-view-7) |
| Export only through a browser download | `session-log-export/README.md:157` | Change | The archive writes to any writer; a command writes a file. | [VIEW-9](#dh-adr-view-9) |

## 5. Tech debt and its resolution

IDs are shortened. `depr:` stands for `UP-DEBT-deprecated.packages/session/`; every other row is `UP-DEBT-limit.packages.session.<name>.<n>` or, for the four query packages, `UP-DEBT-limit.packages.session-query.<name>.<n>`. The evidence for a `limit` row is bullet `<n>` under "Known Limitations and Deferred Work" in that package's `README.md`; for a `depr:` row it is the cited line. "Resolved" means the Go design removes the cause; "n/a" means the cause is JavaScript, React or upstream's hot reload; "Carried" means the limit is kept on purpose, with the reason, and its ledger row names the placeholder stage `DH-S-0` until the implementation plan assigns a real one.

| Debt ID | What it is | Resolution in Go |
|---|---|---|
| `depr:session-projection/src/index.ts:454` | Untyped `stateOf(session, key)` kept for old plugins | Resolved. Only the typed handle reads state ([DH-D-VIEW-1](#dh-d-view-1)). |
| `depr:session-projection/src/index.ts:537` | `cachedSnapshot` that may omit keys | Resolved. A snapshot returns every requested key with an explicit state. |
| `depr:working-directory/src/index.ts:59` | Dead event `working-directory/change` | Resolved. The Go vocabulary never registers it ([PERSISTENCE-2](../upstream/session-and-persistence.md#dh-adr-persistence-2)). |
| `session-projection.1` | No lazy per-value read; a tail page carries every key | Resolved. Every read names its keys; large values are read through paged accessors (the turn index is rows). |
| `session-projection.2` | Key presence is not a per-session capability signal | Carried, on purpose. The table stays host-wide because a fold is a function of the log and is defined for every session, live or not. The hazard is removed: no reader infers anything from presence, since the host renders, an unknown key is an error, and whether a feature is on is asked of the composition. |
| `session-projection.3` | Every unit touched per event | Resolved. Dispatch by declared event type. |
| `session-projection.4` | A released unit's row stops advancing and is served as current | Resolved. A row carries its `seq`; a reader sees `behind`; the next registration catches up from it. |
| `session-projection.5` | A released definition stays referenced for the process lifetime | n/a. It follows from hot-reloading plugin code and from readers that outlive their owner; Go has neither. |
| `session-projection.6` | Cells live in memory only | Resolved. Checkpoints are rows in the session database. |
| `session-projection.7` | Purity is a review concern | Resolved. Folds live in leaf packages with a checked import list, and every definition runs the equivalence and determinism tests ([DH-T-VIEW-4](#91-local-tests)). |
| `session-projection-cache.1` | No eviction or retention | Resolved. Rows are deleted with their session, and a prune removes rows no binary registers. |
| `session-projection-cache.2` | Coarse per-session interval timer | Resolved. No timers: cadence is tied to commits. |
| `session-projection-cache.3` | The cache cannot refold from the log | Resolved. The registry reads the store and catches up itself. |
| `session-projection-cache.4` | Every record-schema change needs archived fixtures | Resolved. Stored state is disposable; one property test replaces fixtures. |
| `session-stats.1` | Steps count attempts, not visible output | Resolved. The view reports closed steps and steps with visible output separately. |
| `session-stats.2` | Cancelled steps untimed; unseen model time counted | Resolved. Every settled attempt is timed from its own event; one figure is "all model time". Needs timing fields from A04 ([§10](#10-open-questions-and-limitations)). |
| `session-stats.3` | Log-scoped, not surface-scoped | Resolved. Both are offered and labelled: the fold is log-scoped, and `Query` counts `current` events. |
| `session-stats.4`, `session-turn-outline.4` | Absent outside the web bundle; clients fall back to their own fold | Resolved. No client fold exists; a composition without the entry shows no strip. |
| `session-turn-outline.1` | The whole outline is pushed on every change | Resolved. The turn index is rows; one fragment per changed entry. |
| `session-turn-outline.2` | No response preview for an open turn | Resolved. The open turn shows the latest committed assistant text, marked unsettled. |
| `session-turn-outline.3` | A turn without text is labelled by number | Resolved. Entries carry a kind (text, images, files, command, tools only) and a count. |
| `session-title.1` | No unpin, search or list indexing | Resolved. `Unpin`; title events are search documents; the title is an indexed list column. |
| `session-title.2` | One provider only | Resolved. Registry seam; one active provider selected by id. |
| `session-title-llm.1` | Each provider interprets output its own way | Resolved. One acceptance rule in the generator. |
| `session-title-llm.2` | Over-limit input is rejected | Resolved. Input is reduced at message boundaries; the log records what was sent. |
| `session-title-llm.3` | Reasoning effort chosen in provider code | Resolved. One config field, default `lowest`. |
| `session-title-first-prompt-llm.1` | The first message may go stale | Carried, on purpose, for the default cadence: one model call per session is the cost the default accepts. The remedies are `Refresh`, which summarises all prompts and is a user action in every carrier, and another provider cadence (area X6). |
| `session-title-first-prompt-llm.2` | Forks never retitle | Resolved. A fork retitles on its first own prompt unless pinned. |
| `session-telemetry.1`, `session-telemetry-otel.3` | Best effort: no outbox, watermark or retry | Resolved. Authorisation and delivery progress are session events; an upload resumes after its watermark and is delivered at least once. That a sent record cannot be recalled is outside any client's power; the confirmation states it. |
| `session-telemetry.2` | No built-in redaction | Resolved. A baseline redactor is always on. |
| `session-telemetry.3` | Redaction uses whatever rules are mounted later | Resolved. The consent records the rule-set fingerprint; a different rule set sends nothing. |
| `session-telemetry-otel.1`, `.2` | Experimental SDK; collector behaviour belongs to the SDK | Resolved. No SDK is linked (`DH-ADR-OTEL-1`); the exporter is harness code owned by A18. |
| `session-telemetry-otel.4` | Feedback given while the plugin is off is never uploaded | Resolved. Off means no consent was asked, so there is nothing to replay; a confirmed upload is a logged fact and resumes. |
| `session-query.1`, `session-query-sqlite.1` | No caller authorisation | Resolved. Visibility is a required argument ([VIEW-4](#dh-adr-view-4)). |
| `session-query.2` | No search provider registry or fallback | Resolved. Search is part of the store-backed query component; with indexing off it reports `query/search-disabled` and exact reads still work. |
| `session-query.3` | Exact reads replay whole logs | Resolved. Range reads by `seq`; class and links are derived rows. |
| `session-query.4`, `session-query-sqlite.3` | Literal scan is unranked and per session; ranked search misses substrings | Resolved. One search request with two modes over the whole corpus, so substring recall no longer needs a second, per-session call; CJK substrings also match in ranked mode. A token index still ranks by tokens for space-separated scripts. |
| `session-query-sqlite.2` | A running search blocks the thread and cannot be interrupted | Resolved. A goroutine and a cancellable context (`DH-LIB-SQLITE-R8`). |
| `session-query-sqlite.4` | One process owns the index | Resolved. Index rows are written in the event's transaction by whichever process holds the lease. |
| `tool-session-list.1` | Cold metadata missing or stale; `null` title ambiguous | Resolved. List columns commit with events; `title_state` says `current`, `behind` or `pending`. |
| `tool-session-list.2` | Paging is unstable and reads every candidate | Resolved. Keyset paging ordered "as of the first call"; work is bounded by the page. A session deleted or archived meanwhile is no longer returned, which is intended. |
| `tool-session-list.3` | Blank sessions are listed | Resolved. `include_blank`, default false. |
| `tool-session-list.4` | Unscoped listing only unit-tested | Resolved. [DH-T-VIEW-18](#91-local-tests) runs both forms through the executor in replay. |
| `tool-session-list.5` | Access is runtime-wide | Resolved. Tool visibility defaults to the caller's workspace. |
| `session-log-export.1` | Browser download only | Resolved. `dsh session export --out`. |
| `session-log-export.2` | Late failures show only in the download manager | Resolved. An export is a host-tracked operation with a final status and a manifest. |
| `session-log-export.3` | Transcript is committed history, raw log only | Carried in part, on purpose: unsettled stream text is in no view, because a view reads committed events only ([design.md §4.6](../design.md#46-event-model)). Removed: the transcript states its cut, marks an open turn, and offers `log` or `surface` scope. |
| `session-log-export.4` | Transcript covers one session | Resolved. Option to include descendants. |
| `session-log-export.5` | Whole transcript buffered; clipboard may reject | Resolved. The host streams; over a size cap the UI offers a file, never a truncated copy. |
| `session-log-export.6` | A clipboard write cannot be undone on plugin disposal | n/a. It describes a client plugin unloading during a copy; there is no client plugin runtime ([DH-ADR-3](../design.md#dh-adr-3)). |
| `session-log-export.7` | The source path is not an atomic claim | Resolved. There is no per-session file; id, cut and events come from one read transaction. |
| `message-quotes.1` | No lazy continuation or fenced-code handling | Carried in part, on purpose: no lazy continuation, which would swallow the user's own text typed after a quote. Removed: lines inside a fenced block are not quotes. |
| `working-directory.1` | Sessions from older releases resume in the original project | Resolved. No Go log holds a model-moved directory; an importer ([DH-ADR-11](../design.md#dh-adr-11)) decides for upstream logs. |
| `working-directory.2` | Both directories missing: every turn rejected, repair only through the SDK | Carried, on purpose: the rejection stays, because the write boundary comes from the directory and guessing one would not fail closed. Removed: the dead end. The typed error makes the UI, the command line and the SDK offer "choose a directory". |

## 6. Go design

### 6.1 Shape

All packages are under `src/harness/internal/session`, in the family that [design.md §4.2](../design.md#42-go-module-layout) assigns to A01, A05 and A06.

| Package | Component and service key | Why it exists |
|---|---|---|
| `projection` (proposed; built inside `views`) | Built: `session/views` holds the one driver of folds (`Definition`, `Register`, `Registry`, `Handle`, `Snapshot`) beside the built-in folds. Proposed: component `session.projection`; service `projection.Key` (`Registry`). Spine. Injects the session store. | The one driver of folds: registration, dispatch, snapshots, checkpoints, catch-up, rebuild. |
| `projection/folds/...` (proposed, not built) | No component: leaf packages holding pure fold functions. Built: the built-in folds are functions in `session/views` (`builtin.go`, `RegisterBuiltins`) | Purity is checked by imports, not by review. |
| `views` | Component `session.views`; registers the built-in folds: list summary, turn index, statistics, event class and links | A preset that does not want statistics removes one entry. |
| `title` | Component `session.title` (`title.Key`, `Titles`), registry `title.Providers`; component `session.title.llm` registers the first-prompt provider. Injects `projection` and the LLM runtime | Titles and their model call. |
| `query` | Component `session.query` (`query.Key`, `Query`); component `session.tools` registers `list_session` and `list_workspace` | Reads for the UI, the SDK and tools; tools are a separate component so that a preset without them pays no prompt tokens. |
| `search` | Row projection and text preparation, used by `query` | Indexing is a fold; preparation is pure Go. |
| `transcript` | Functions only: `WriteArchive`, `WriteTranscript`, `Quote`, `RecognizeQuotes` | Carriers (route, command) own the transport. |
| `upload` | Component `session.upload` (`upload.Key`, `Uploads`), registry `upload.Redactors`. Default-off entry | The only code here that sends anything off the machine. |
| `workdir` | Component `session.workdir` (`workdir.Key`, `Service`). Injects `projection` and the filesystem seam | One owner of each session's directory. |

**As built (M1).** The fold registry this section calls `projection` exists as package `src/harness/internal/session/views`, together with the four built-in folds; there is no `projection` package, so `projection.Register`, `projection.Changed` and `projection.Config` below read as `views.Register` and so on where they are built. `query` exists as `session/query`; this correction leaves the other rows unchanged. The code sketch below is the proposed interface (stage 15.2 of [the M1 gap-closure plan](../../stories/harness-DSH-M1/gap-closure-plan.md) adds `Freshness`, `Value` and `Registry.Snapshot`). Corrected by plan step 15.3.10 (gap M1-G-STORE-29).

```go
// package projection
type Definition[S any] struct {
    Key     string
    Version int       // bump when the state shape or the meaning of the fold changes
    Types   []string  // event types consumed; nil means all and needs a stated reason
    Cadence Cadence   // EveryCommit (small state shown in lists) or Periodic
    Summary bool      // carried on list rows; requires EveryCommit
    Tables  []Table   // derived tables a row projection writes; empty for a value projection
    Init    func(h session.Header, inherited session.Offset) S
    Apply   func(s S, ev event.Event, out Rows) (next S, changed bool)
    View    func(s S) any // nil: host-only state
}
func Register[S any](ctx *cordis.Context, r Registry, d Definition[S]) (Handle[S], error)
type Handle[S any] interface {
    State(ctx context.Context, s session.ID, f Freshness) (S, Cut, error)
}
type Registry interface {
    Snapshot(ctx context.Context, s session.ID, keys []string, f Freshness) (Snapshot, error)
    Rebuild(ctx context.Context, key string, scope RebuildScope) error
    Status(ctx context.Context) []FoldStatus // per key: version, sessions behind, pending, failed
}
type Snapshot struct { AsOfSeq, HeadSeq event.Seq; Values map[string]Value }
type Value struct { State ValueState; AsOfSeq event.Seq; View any } // current | behind | pending
type Freshness int // Exact: catch up or fail with view/behind. Stored: never fold, never block
```

**How a fold runs.** The registry is the implementation of the `Folder` hook that area A01 defines ([core-session.md §6.1](core-session.md)): one call per accepted batch, under the append lock, in `seq` order, returning the transaction parts that area A05 commits with that batch. (1) *Accept.* Under the session's append lock ([design.md §4.5](../design.md#45-concurrency-model)) the session numbers the event; the registry calls the folds registered for its type and collects each changed state and each row operation. In-memory state is now exact at that `seq`. Folds see the hot part of a payload only, never the raw stream. (2) *Commit.* The store's writer commits the batch: the events, then, inside a savepoint, the row operations in order, the latest state of each `EveryCommit` key that changed, each `Periodic` state that is due (turn end, session close, every `CheckpointEveryEvents` events), and one statement that advances `seq` on this process's `EveryCommit` rows. So this area, not the store, decides when a checkpoint is written. (3) *Announce.* The session's deliverer raises `projection.Changed` once per event that changed at least one view, in commit order.

**What a reader sees.** In the process that holds the session's lease, a snapshot is read from memory at one position (A01's `AsOf`), so `AsOfSeq` is the `seq` a history page read in the same call ends at. In any other process, or for a session nobody holds, it is one read transaction over the stored rows and the head. `Stored` returns rows as they are, each with its own `AsOfSeq`, and never blocks: this is what a session list uses. `Exact` folds the missing suffix inside the same read transaction, up to `MaxCatchUpEvents`, and otherwise fails with `view/behind` and the two positions. A key with no usable row is `pending`, never a zero value. A fold that changed (a new `Version`) is rebuilt per session on first touch and by a background sweep; until then its readers see `pending`.

```go
// package query (ctx is context.Context throughout)
type Query interface {
    List(ctx, v Visibility, f ListFilter, p Page) (ListPage, error)                   // keyset, "as of" the first call
    Observe(ctx, v Visibility, id session.ID, o ObserveOptions) (*Observation, error) // header, cut, range reader, snapshot
    Event(ctx, v Visibility, id session.ID, seq event.Seq, before, after int) (EventWindow, error)
    Events(ctx, v Visibility, id session.ID, f EventFilter, p Page) (EventPage, error) // seq, type, time, class; a Page may ask for turn-aligned bounds
    Surface(ctx, v Visibility, id session.ID, p Page) (SurfacePage, error)
    Trace(ctx, v Visibility, id session.ID, seq event.Seq) (EventTrace, error)
    Lineage(ctx, v Visibility, id session.ID, o LineageOptions) (Lineage, error)
    Titles(ctx, v Visibility, ids []session.ID) ([]TitleResult, error)
    Search(ctx, v Visibility, r SearchRequest) (SearchPage, error)                    // Mode: Ranked or Literal; with one Session it returns that session's matching seqs, ungrouped
    TreeMessages(ctx, v Visibility, root session.ID, p Page) (MessagePage, error)     // messages between agents of one tree (source agent-message), newest first, keyset-paged
}
// Titles, previews and snippets leave Query through the credential store's Mask (A17), so a stored row never needs rewriting when a secret is added.
// TreeMessages is the query behind the team message timeline (ui-work.md, register entry U-K2-6): this is the design, not a fallback. It reads the
// sessions of the tree that A12's ledger names under root, and returns per message its time, sender, recipient, bounded text, and the recipient's
// session and seq for a link. It is a read of the event index, bounded by the page and by LineageMaxNodes sessions; a session the caller may not see is left out.
// package title
type Titles interface {
    Get(s session.ID) (Title, bool)
    Rename(ctx, s session.Session, text string) (Title, error) // pins
    Unpin(ctx, s session.Session) (Title, error)
    Refresh(ctx, s session.Session) (Title, error)             // returns the provider's error
}
type Provider interface { ID() string; Cadence() Cadence; Prompt(in Input) (Prompt, error) } // Cadence: FirstPrompt | EveryPrompt
// Input: every eligible own human prompt (bounded by MaxInputBytes), and the current title with its source. Any area registers a fold (X6's translations) with projection.Register.
// package workdir
type Service interface {
    Get(s session.ID) string                          // no I/O
    Ensure(ctx, a agent.Agent) (string, error)         // validate, recover, record
    Set(ctx, a agent.Agent, path string) (string, error) // a person's choice, through a carrier; moves the anchor
    SetForCall(ctx, a agent.Agent, path string, call tools.CallID) (string, error) // inside the anchor only (VIEW-8)
}
// package upload
type Uploads interface {
    Status() Status                                    // off | unavailable | ready, destination, identity
    Preview(ctx, s session.ID, through event.Seq) (Preview, error)
    Records(ctx, s session.ID, p Preview, pg Page) ([]Record, error) // the redacted records a Preview counted, paged
    Authorise(ctx, s session.Session, p Preview) error
    Cancel(ctx, s session.Session) error
}
```

A `Visibility` can only be made by three constructors: from an admitted peer's principal and its `access.Filter` (carriers, A19), from an agent and the tool visibility policy (tools), or for a named host component. Its zero value sees nothing.

### 6.2 Contracts

**Events.** Live events use the dispatch forms of [design.md §4.6](../design.md#46-event-model).

| Go event or type | Kind and form | Payload | Replaces |
|---|---|---|---|
| `projection.Changed` | live, notify | session, `seq`, changed keys with their views | `onChanged` and `onEventChanged` callbacks |
| `workdir.Access` | live, around; result `proceed` or `deny(reason)` | agent, requested path or none | `UP-EVT-working-directory/access` |
| `upload.Redactors` | not an event: an ordered registry of `func(Record) (Record, error)` | one record | `UP-EVT-session-telemetry/record`. A redactor can change or withhold a record and cannot skip another redactor |
| `session/title` | durable, log-only, whole value | title, source, `messageSeqs`, `pinned`, `requestSeq` | upstream event of the same name |
| `session/title-request` | durable, log-only | provider id, route, system text, messages, output cap, effort, `messageSeqs`, purpose | `session/title-llm-request` |
| `session/title-attempt` | durable, log-only | `requestSeq`, A01's `requestAsOf` and `requestDigest`, outcome (`accepted`, `rejected`, `failed`, `cancelled`, `superseded`, `interrupted`), finish, usage, duration, returned text, error class | new |
| `workspace-root/change` | durable, log-only, whole value | directory, cause (`move`, `recovery`) | upstream event of the same name |
| `session-upload/authorised`, `/delivered`, `/ended` | durable, log-only | through `seq`, consent event `seq`, destination, rule-set fingerprint; delivered through `seq`; reason | new; replace the in-memory cursor |

**Config.** Each is a Go struct with defaults as a Go value ([SCHEMA-3](../libs/zod.md#dh-adr-schema-3)); unknown keys fail the load.

| Go struct | Fields and defaults | Replaces |
|---|---|---|
| `projection.Config` | `CheckpointEveryEvents` 200; `MaxCatchUpEvents` 2,000; `RebuildConcurrency` 2; `SlowFoldWarning` 5 ms | `UP-CFG-dsh-session-projection-cache`. The interval field is gone with the timer |
| `query.Config` | `DefaultLimit` 20; `MaxLimit` 100; `SnippetRunes` 240; `ReadWindowMax` 50; `SearchIndex` on; `MaxCandidates` 5,000; `LineageMaxNodes` 500; tool `Visibility` `own-workspace` | `UP-CFG-dsh-session-query-sqlite`. `path`, `openAt`, `journalMode` and the two cache fields have no counterpart: the index is in the store A05 opens |
| `title.Config` | `FallbackMaxWords` 5; `FallbackMaxBytes` 40; `MaxTitleBytes` 80; `Provider` `first-prompt` | `UP-CFG-dsh-session-title`. Upstream has no defaults; these are the base bundle's (`packages/bundle/base/cordis.patch.yml:60-65`) |
| `title.GeneratorConfig` | `TargetWords` 5; `TargetCJKCharacters` 10; `MaxInputBytes` 4,096; `MaxOutputTokens` 4,096; `Timeout` 60 s; `Route` empty (the session's logged route); `ReasoningEffort` `lowest`; `Forks` `retitle` | `UP-CFG-dsh-session-title-first-prompt-llm` (`cordis.patch.yml:67-77`) |
| `upload.Config` | `Enabled` false; `Endpoint` empty; `Headers` (credential references); `MaxRequestBytes` 4,000,000 (may only be lowered); `MaxAttempts` 5 | `UP-CFG-dsh-session-telemetry-otel`. `mode` becomes `Enabled`; processor settings belong to A18's channel |
| `workdir.Config` | `DefaultDirectory` empty (the launch directory); must be absolute | `UP-CFG-dsh-working-directory` |
| `transcript.Config` | `CompressionLevel` 6 (0 to 9); `MaxCopyBytes` 1,000,000 | `UP-CFG-dsh-session-log-export` |

**Services.** `UP-SVC-sessionProjections` becomes `projection.Key`; `UP-SVC-sessionProjectionCache` is merged into it; `UP-SVC-sessionQuery` becomes `query.Key`; `UP-SVC-sessionTitle` becomes `title.Key`; `UP-SVC-workingDirectory` becomes `workdir.Key`; `UP-SVC-sessionTelemetry` is replaced by `upload.Key`, which has no `emit` method.

**Persisted data.** All derived, all in the session database, all keyed by session and deleted with it. Area A05 owns the tables and the transaction ([session-store.md §6.2](session-store.md)); this area owns what goes in them. All five are in A05's layout.

**As built: the built-in views are checkpoint documents** (owner decision M1-D-STORE-C (a) in [the M1 decision log](../../stories/harness-DSH-M1/decisions.md); plan step 15.3.10 of [the M1 gap-closure plan](../../stories/harness-DSH-M1/gap-closure-plan.md), gap M1-G-STORE-24). A *checkpoint document* is one `checkpoints` row per session, view key and fold version, whose `state` column holds the whole fold state as JSON and whose `seq` says how far into the log it reflects. The four built-in views keep their state that way: list summary (`view/list-summary`), turn index (`view/turn-index`), statistics (`view/stats`) and event index (`view/event-index`). The tables `session_summary`, `turn_index`, `event_class` and `event_link` below are not built. Why: the documents already give the guarantees this area needs (written in the batch's transaction, never ahead of the log, rebuilt when a fold's version changes), and building the tables would mean a new store layout in M1. The cost is that a view's values are not columns, so the database cannot filter or index on them; how often each document is written is set by its cadence (plan step 15.2.4). The tables are revisited at [DH-S-23](../impl.md#dh-s-23), with search (`search_docs`, `event_fts`), which is not built either.

| Table | Key | Holds | Written by |
|---|---|---|---|
| `checkpoints` | session, key, state version | state as JSON, `seq` covered | the registry (A01 writes its `core/*` keys) |
| `session_summary` | session | title, title source, blank flag (false from the first inbox insertion, not the first turn), the last completed turn with its `turn/end` reason, directory, as plain columns for filtering. Ordering uses `sessions.last_prompt_at`, which A05 maintains | folds `title`, `listSummary`, `workingDirectory` |
| `turn_index` | session, turn | start `seq`, prompt and response preview with kind and count, settled flag. The row exists from `turn/start`, so an open turn is in the outline with the flag false | fold `turnIndex` |
| `event_class`, `event_link` | session, `seq` | surface class and replaced-by, updated by the transaction of the replacing event with A01's `surface.Classify`; who cites whom; and the tool call an event belongs to (call, result, nested calls, approval pair), so `Events` filtered by call id answers "which events belong to this call". A derived row is read by its key at `Exact` freshness through `Query` | fold `eventIndex` |
| `search_docs`, `event_fts` (FTS5) | session, `seq` | prepared text (indexed); original semantic text (stored, not indexed). A fork's inherited events are not indexed again: a search inside a fork also reads its ancestors' documents below each cut | fold `search` |

**Tools exposed to the model.** Both go through the guarded pipeline (A03).

| Tool | Arguments | Result | Bounds |
|---|---|---|---|
| `list_session` (changed) | `workspace_id`, `archived` (false), `include_blank` (false), `limit` 1 to 100 (20), `cursor` | `{items: [{id, title, title_state, workspace_id, created_at, updated_at}], next_cursor, as_of}`; never subagent sessions | At most 100 items; a title is at most `MaxTitleBytes`; only sessions the caller's visibility admits; the pipeline's result bound applies last |
| `list_workspace` (changed) | `limit`, `cursor` | `{items: [{id, title, path, created_at, updated_at}], next_cursor}` from A07's registry | Same |

An `offset` argument is not offered: it makes the store count rows it then discards. The experimental query tools (area X3) call `Query` with the same visibility and get the same bounds: a window of at most `ReadWindowMax` events per side, snippets of at most `SnippetRunes`, pages of at most `MaxLimit`.

**Search.** Text is prepared before indexing and before matching: each run of Han, Hiragana, Katakana or Hangul characters (`unicode` tables, standard library) becomes overlapping two-character pieces plus the run's last character, separated by spaces; other text is left to `unicode61`. A ranked query is the prepared text bound as one quoted phrase; a one-character CJK query is a prefix query on that character. Order is: more phrase occurrences, then shorter documents, then newer, then session and `seq`. Every key is a property of one document, so a keyset cursor stays valid when other sessions change. At most `MaxCandidates` matches are ranked, and the page says when that cut the result. Snippets are cut from the original text in Go. Literal mode scans the original text column for a case-insensitive substring under the same filters and cap. A cross-session search groups by session and returns its strongest event.

**Titles.** The generator, not the provider, makes the call. Order of work for one generation: check the revision; build the prompt (the provider's part); reduce over-limit input (keep the first prompt and the newest prompts that fit, clip one message at a UTF-8 boundary with an elision mark); append `session/title-request` and flush it; build the request from that committed event with A01's `RequestFrom(seq)`, the only way to make one, and send it through the LLM runtime, attributed in the log to purpose `session-title` and this session, with no turn or step identity (whether the provider is also sent a session id or purpose marker is A04's route setting, off by default); append exactly one `session/title-attempt`; if accepted (finish `stop`, non-empty first line, within the byte limit after cleaning), append `session/title` in the same append group. The route is the session's logged main route or the configured one; if it is unavailable the attempt fails and no other route is tried. There is no automatic retry: `Refresh` is the retry.

**Statistics.** The `stats` view holds turns, closed steps, steps with visible output, attempts and failed attempts, model time over all settled attempts, tool time, first-token latency and decode rate. It is local state: no component exports it.

**Session upload.** Nothing is captured continuously. An upload exists only when all of these hold: `upload.Config.Enabled` is on, the off-switch (A18) is not set, an endpoint is configured, and the user confirmed a preview for this submission. The preview is computed from the records that would be sent: event count, bytes, first and last event time, destination, the identity attached, what the redactors masked, and that sent data cannot be recalled. `Authorise` appends `session-upload/authorised`; the uploader sends the prefix from `seq` 0 through the consent event, one record per event, through A18's channel; each acknowledged batch appends `session-upload/delivered`. A fork's inherited authorisation is ignored: the fold starts counting at the inherited cut.

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership and access.** The registering fiber owns a registration and nothing else. The session owns its derived rows: they are deleted in the store's delete transaction and by no unload. A handle, a snapshot and a `Query` give access, never ownership. A derived row may be written by any process, because a pure fold gives every writer the same answer; a write is an upsert that can only raise a row's `seq`. Appending (titles, directory moves, upload progress) needs the session's lease like any append. The title goroutine and the upload channel are effects of their component's fiber.

| Situation | What happens | Undone, retried, uncertain |
|---|---|---|
| A fold panics or returns invalid state | The append stands. That key is `failed` for that session at its last good `seq`; `Status` and the local log name it; other folds continue | Retried by a rebuild or after a restart. Nothing is rolled back |
| A derived write fails in the commit | The savepoint rolls back; the events commit; the affected keys are `behind` | Catch-up at the next read or commit |
| Stored state does not decode or has another version | Ignored; the key is `pending` until refolded | The old row is replaced, never migrated |
| A component unloads, or a config edit removes a fold | Its key leaves snapshots; its rows stay at their `seq` | The next registration catches up from them |
| The host dies during a rebuild | Rows carry the `seq` they cover; the sweep resumes | Readers see `pending` or `behind` meanwhile |
| Another process appends to a session this process lists | Its commit wrote the rows; this process reads them. A key the other binary does not fold is `behind` | Catch-up on an `Exact` read |
| A fork is created | The creator folds the inherited prefix once and writes the child's rows before the fork is listed | A failure leaves the child `pending` |
| A title call fails, times out or is cancelled | One `session/title-attempt`; the title stands; a local warning. The turn is never delayed | No retry. A late result for an older revision is refused under the append lock |
| The host dies between title request and attempt | The next lease holder appends an attempt with outcome `interrupted` | Uncertain: whether the provider received and billed the request |
| The search index is off or damaged | `query/search-disabled` or `query/index-failed`; exact reads keep working; `dsh store rebuild` ([DH-D-STORE-6](session-store.md#dh-d-store-6)) rebuilds | The index is derived: dropping it loses nothing |
| A search or list is cancelled | The statement stops within the driver's bound; the connection is reused | Nothing to undo |
| The collector is down | Bounded retries inside A18's channel, then `session-upload/ended` after `MaxAttempts` | The user can retry. Uncertain: whether a batch cut off mid-flight arrived; the receiver deduplicates by session and `seq` |
| The setting is turned off, or the off-switch is set, during an upload | The channel closes without draining; `session-upload/ended` records `cancelled`; it is not resumed later | Records already posted stay posted |
| The rule set differs from the one confirmed | Nothing is sent; the upload ends as `needs-reconfirmation` | The user confirms a new preview |
| The directory disappeared | `Ensure` records a move to the original project and queues a notice | If that is gone too: `workdir/unavailable`, the turn is rejected, nothing is appended |
| `Set` fails validation, or an access listener denies | Nothing is recorded | The directory is unchanged |
| A notice cannot be queued after the move committed | The move stands; a warning; the next request carries the directory | Nothing is reverted |
| A descendant or attachment cannot be read during an export | The entry is omitted and listed in the manifest; status `complete-with-omissions` | The command writes to a temporary name and renames on success; a failed stream is reported as `failed` |

## 7. Decisions

<a id="dh-adr-view-1"></a>
### DH-ADR-VIEW-1 A projection is a typed pure fold with declared inputs, driven by one registry
- **Decision.** A definition is a Go value with a key, a version, the event types it consumes, a cadence, `Init`, and `Apply(state, event, rows) (state, changed)`. `Register` returns a typed handle. One registry runs folds under the session's append lock. Fold functions live in leaf packages whose imports are checked. The registry is the one implementation of A01's `Folder` hook. The model's history is not one of its units: history, the request header, the tool history and the open brackets are A01's core folds, kept inside the session because they must refuse a candidate event before it is committed, while a projection only ever sees committed events ([core-session.md §6.1](core-session.md#61-shape)). They share the `checkpoints` table under reserved `core/*` keys, and a query that needs "current or shadowed" calls A01's `surface.Classify`, so there is one fold of history and no second copy to disagree with it.
- **Why.** Upstream gets a consistent cut from a single thread and "no change" from object identity; Go has neither, so both become explicit. Declared types remove the per-event visit to every unit.
- **Tradeoff.** A slow fold slows every append, so duration is measured and a slow fold is reported. Purity is enforced by package layout, which costs a package per fold family.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-VIEW-1 to -4.

<a id="dh-adr-view-2"></a>
### DH-ADR-VIEW-2 Stored state commits with its events; a reader is told current, behind or pending
- **Decision.** Checkpoints are rows keyed by session and key and stamped with the state version, written in the transaction of the events they cover: `EveryCommit` for small state that lists show, `Periodic` for large or fast-changing state. A version mismatch discards. Rebuilds run lazily on first touch and in a bounded background sweep. Every value carries its state and `AsOfSeq`; a missing registry or key is an error.
- **Why.** It applies [PERSISTENCE-7](../upstream/session-and-persistence.md#dh-adr-persistence-7) and adds the per-commit cadence, without which a list row could be up to 200 events stale. It removes the identity matching, backup files and "cached versus sequenced" rule of the upstream cache.
- **Tradeoff.** One more statement per commit, and a state write when a list value changes. After a fold change, cold sessions are `pending` until the sweep reaches them.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-14](../design.md#dh-o-14). **Verified by.** DH-T-VIEW-5 to -8, DH-E-VIEW-2, DH-E-VIEW-3.

<a id="dh-adr-view-3"></a>
### DH-ADR-VIEW-3 Indexes are projections: list columns, turn index, event classes and search documents
- **Decision.** A row projection writes rows into derived tables through the same fold, version and rebuild mechanism. `Summary` on a definition says whether a list row carries it.
- **Why.** Upstream has four mechanisms for derived data: memory cells, a cache file, a search database with reconciliation, and whole-log replay for traces. One mechanism gives one rebuild path, one failure model and one place to test "derived equals refolded".
- **Tradeoff.** Derived tables become part of the store layout that A05 migrates. An event whose class changes later (a replacement) updates derived rows, never event rows.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-VIEW-1, -6, -9, -13.

<a id="dh-adr-view-4"></a>
### DH-ADR-VIEW-4 Every query takes a visibility and is bounded; no method returns a whole log
- **Decision.** `Query` methods require a `Visibility` whose zero value sees nothing. A session outside it is reported as not found. Reads are ranges, pages or windows with configured maxima; lineage is bounded by node count and says when it was cut.
- **Why.** Upstream's query service trusts every caller and leaves policy to each tool (`packages/session-query/session-query/README.md:160`), which [design.md §6](../design.md#6-security-model) rule 2 forbids: enforce where the decision is made.
- **Tradeoff.** Host components must construct a named internal visibility, which is more ceremony. A caller that truly needs every event iterates pages.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-6](../design.md#dh-o-6), [DH-O-13](../design.md#dh-o-13). **Verified by.** DH-T-VIEW-13 to -15, -18.

<a id="dh-adr-view-5"></a>
### DH-ADR-VIEW-5 Search prepares CJK text in Go over stock FTS5, with a ranked and a literal mode
- **Decision.** As described under "Search" in [§6.2](#62-contracts). The preparation is the candidate `DH-ADR-SQLITE-3` recommends measuring first; it is provisional until measurement M3 (DH-T-VIEW-17) records index size and append cost. Inference: adding the run's last character as its own token makes one-character queries work as prefix queries; DH-T-VIEW-16 must confirm it. Indexing is on by default.
- **Why.** No stock tokenizer segments CJK text, and a custom one ties the product to one driver ([libs/sqlite.md](../libs/sqlite.md)). Literal mode answers the substring case for other scripts.
- **Tradeoff.** A larger index for CJK text; snippets and occurrence counts depend on prepared text; no linguistic ranking. If M3 rejects the cost, the fallback is `unicode61` alone with literal mode for CJK, recorded as a new decision. Scope boundary of this record: session search matches exact tokens, prefixes and literal substrings, and does not tolerate typing errors; this is a product choice because stock FTS5 has no fuzzy matching, a spelling index would add a second index and a custom extension, and exact matches give results a person can predict.
- **Serves.** [DH-O-5](../design.md#dh-o-5). **Verified by.** DH-T-VIEW-16, DH-T-VIEW-17, DH-E-VIEW-6.

<a id="dh-adr-view-6"></a>
### DH-ADR-VIEW-6 A title call is logged before it is sent, attributed, and settled by exactly one event
- **Decision.** As described under "Titles" in [§6.2](#62-contracts): request event flushed before dispatch, purpose and session attribution, one attempt event per request, no cross-route fallback, no automatic retry, one active provider chosen by id. A fork retitles on its first own prompt unless its title is pinned; subagent sessions never generate a title.
- **Why.** The title model sees the user's prompts and costs money. Logging the exact request keeps [DH-O-4](../design.md#dh-o-4) true in the form "everything any model was sent is in the log"; the attempt event gives [DH-O-8](../design.md#dh-o-8) for auxiliary calls. The title text is log-only and never reaches the conversation model.
- **Tradeoff.** Two or three log events per title. One more model call per fork. A provider that fails leaves the fallback until the user refreshes. Scope boundary of this record: the default `first-prompt` cadence titles a session once, from its first prompt, and never retitles it automatically; `Refresh`, or choosing the every-prompt cadence, is the remedy for a title that no longer fits, because one bounded model call per session is the cost the product chose for a default title.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-8](../design.md#dh-o-8). **Verified by.** DH-T-VIEW-10 to -12, DH-E-VIEW-1.

<a id="dh-adr-view-7"></a>
### DH-ADR-VIEW-7 Session upload is opt-in, confirmed per submission, logged, redacted and resumable
- **Decision.** As described under "Session upload" in [§6.2](#62-contracts). A baseline redactor always runs last: it masks every secret value the credential store has released in this process and recognised secret shapes (bearer tokens, private-key blocks, common key prefixes). Live capture, operational records and the `sharing` disclosure are not built.
- **Why.** The owner decided the default and the confirmation ([DH-ADR-9](../design.md#dh-adr-9), `DH-ADR-OTEL-3`). This area answers the question that record left open for it: ship rules. Recording consent and progress in the log makes the upload a fold, so a restart resumes it and the UI can show its state.
- **Tradeoff.** Pattern redaction is not a guarantee, and the preview says so. Delivery is at least once. The publisher receives almost nothing by default.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Verified by.** DH-T-VIEW-21, DH-T-VIEW-22, DH-E-VIEW-4.

<a id="dh-adr-view-8"></a>
### DH-ADR-VIEW-8 One working directory per session; a person moves it anywhere, a tool call only inside the anchor
- **Decision.** `workdir.Service` owns it. The header's directory is the immutable original project: identity, workspace membership and recovery destination. A move is a `workspace-root/change` event recorded by `Set`, `SetForCall` or recovery. The service grants no write root. The sandbox resolver (A10) reads `Get` as the workspace root when it resolves a call, so a move changes what later calls may write. **The rule, stated here once: the model can never widen the write boundary by moving the directory.** The *anchor* is the directory a person last chose with `Set`, or else the header's original project. `Set` is reachable only from a carrier acting for an owner principal. A host component acting for a tool call uses `SetForCall`, which is refused with `workdir/outside-anchor` unless the canonical target is the anchor or inside it, never moves the anchor, and records the call id in the event. No tool of the default composition calls it: the model uses paths and a per-call directory on shell tools. The only callers are the experimental `enter_worktree` and `exit_worktree` ([x-forge-worktree.md](x-forge-worktree.md)), mounted only with their entry. Recovery (a vanished directory falls back to the anchor) is dispatched through `workdir.Access` with reason `recover`, so a component may deny it; a denied recovery leaves the session `workdir/unavailable`. Running processes and registered extra roots are untouched.
- **Why.** Upstream removed the model's directory tool after finding it could move the write boundary (`.agents/notes/implemented/architecture/2026-11-03-session-workspace-root.md:9-13`).
- **Tradeoff.** The model cannot "cd" for the session. A session whose two directories are both gone cannot run a turn until a person or an SDK program chooses one.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10), [DH-O-4](../design.md#dh-o-4). **Verified by.** DH-T-VIEW-23, DH-T-VIEW-24, DH-E-VIEW-5.

<a id="dh-adr-view-9"></a>
### DH-ADR-VIEW-9 Exports write to any writer and end with a status
- **Decision.** `WriteArchive` and `WriteTranscript` take an `io.Writer` and a visibility. The archive ends with a manifest of what it holds and what it omitted. An HTTP export is a host-tracked operation whose final status reaches the tab's event stream; the command writes a file atomically. Two boundaries are part of this decision (recommended defaults, not confirmed by the owner). A transcript and an archive hold committed events only: text of an attempt that has not settled is in no export, because only a committed event can be replayed. A quote is a line that carries the quote mark and nothing else is: the recogniser never guesses from wording, so a quoted passage survives copying exactly when its marks do.
- **Why.** It removes the browser-only path and the failures that only the download manager saw.
- **Tradeoff.** The host keeps a small record per running export. The per-session line format inside the archive is A05's ([PERSISTENCE-12](../upstream/session-and-persistence.md#dh-adr-persistence-12)), so two areas must agree on it.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-VIEW-19, DH-E-VIEW-6.

## 8. Deliverables

<a id="dh-d-view-1"></a>
### DH-D-VIEW-1 Folds are registered once and read at one cut
- **Result and why it matters.** Any component can keep log-derived state by registering a fold, and any reader gets several values and a history page that describe the same `seq`. Without it each feature subscribes and replays on its own.
- **How it is implemented.** The registry of [§6.1](#61-shape): a dispatch table from event type to folds, built at registration; folding under the append lock; in-memory state per leased session; the change event; typed handles; the import check for fold packages.
- **Components and files.** `session/projection`, `session/projection/folds`. **Depends on.** `DH-D-CORDIS-1`, `DH-D-CORDIS-3`, `DH-D-CORDIS-4`. [DH-D-SESSION-2](core-session.md#dh-d-session-2): the `Folder` hook under the append lock, the event type registry and the committed-event deliverer.
- **Priority.** P0. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-12](../design.md#dh-o-12). **Done when.** DH-T-VIEW-1 to -4 pass, including under the race detector, and DH-E-VIEW-2 has passed.

<a id="dh-d-view-2"></a>
### DH-D-VIEW-2 Stored state is never ahead of the log, and a changed fold rebuilds itself
- **Result and why it matters.** A long session opens and lists without replay; after a crash nothing derived contradicts the log; after an upgrade that changes a fold, readers see `pending`, then the new value, never the old one.
- **How it is implemented.** Derived writes travel with their events into the store's transaction inside a savepoint; the two cadences; `Stored` and `Exact` reads; lazy and background rebuild with monotone upserts; fork seeding; a prune for unknown keys; `Status` for the operator.
- **Components and files.** `session/projection`. **Depends on.** [DH-D-VIEW-1](#dh-d-view-1). [DH-D-STORE-2](session-store.md#dh-d-store-2): derived writes in a savepoint of the append transaction, one read transaction over events and derived rows (`sqlitedb.Read`), cascade on delete, the table layout.
- **Priority.** P0. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-14](../design.md#dh-o-14). **Done when.** DH-T-VIEW-1 and DH-T-VIEW-5 to -8 pass and DH-E-VIEW-2 and DH-E-VIEW-3 have passed.

<a id="dh-d-view-3"></a>
### DH-D-VIEW-3 The built-in views: list summary, turn index, statistics, event index
- **Result and why it matters.** The session list can order by last human prompt and hide blank sessions; a long conversation has a navigable outline; the statistics strip counts the whole session; traces and classes need no replay.
- **How it is implemented.** Four fold families in leaf packages, registered by the `session.views` component: `listSummary` (the blank flag; the last prompt time is A05's column), `turnIndex`, `stats`, `eventIndex` (class, replaced-by, citations). Each is specified by golden logs.
- **Components and files.** `session/views`, `session/projection/folds/{summary,turns,stats,events}`. **Depends on.** [DH-D-VIEW-2](#dh-d-view-2). [DH-D-SESSION-1](core-session.md#dh-d-session-1): timing and usage are hot fields of the attempt and message events.
- **Priority.** P1. **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Done when.** DH-T-VIEW-9 passes and DH-E-VIEW-2 has passed.

<a id="dh-d-view-4"></a>
### DH-D-VIEW-4 Sessions have titles, and the model call that writes one is in the log
- **Result and why it matters.** Every session with a text prompt has a title at once (the fallback), usually a better one seconds later, and the user's own title wins. Each title call can be audited and costed.
- **How it is implemented.** The `title` component: normalisation, the fallback, `Rename`, `Unpin`, `Refresh`, a revision counter per session, the provider registry, and the generator of [§6.2](#62-contracts). Folds `title` (summary) and `titleInput` (first and latest eligible message, own-prompt count).
- **Components and files.** `session/title`. **Depends on.** [DH-D-VIEW-1](#dh-d-view-1), [DH-D-VIEW-2](#dh-d-view-2). [DH-D-SESSION-4](core-session.md#dh-d-session-4) for `RequestFrom`; [DH-D-LLM-3](llm.md#dh-d-llm-3) for `Runtime.Complete`, which takes that request, whose `Meta` carries the purpose; the replay provider is [DH-D-UTIL-7](util-test-support.md#dh-d-util-7). Runtime prerequisite: a configured model route.
- **Priority.** P1. **Outcome.** [DH-O-4](../design.md#dh-o-4), [DH-O-8](../design.md#dh-o-8), [DH-O-12](../design.md#dh-o-12). **Done when.** DH-T-VIEW-10 to -12 pass and DH-E-VIEW-1 has passed.

<a id="dh-d-view-5"></a>
### DH-D-VIEW-5 Sessions can be listed, observed, traced and followed across forks, within what the caller may see
- **Result and why it matters.** The UI, the SDK and tools read sessions through one interface that pages, bounds and filters by access. Without it each carrier would query the store its own way.
- **How it is implemented.** `Query` over the store's read transaction: the keyset list on A05's `last_prompt_at` index with "as of" ordering (the index scan merged with the few sessions touched since the first call), summary values joined per page, `Observe` with a range reader and snapshot, windows, traces from `event_class` and `event_link`, lineage from header parents with a node bound, the visibility constructors.
- **Components and files.** `session/query`. **Depends on.** [DH-D-VIEW-2](#dh-d-view-2), [DH-D-VIEW-3](#dh-d-view-3). [DH-D-STORE-2](session-store.md#dh-d-store-2) (read transaction, fork storage); [DH-D-STORAGE-7](storage.md#dh-d-storage-7) (workspace membership and archive flags). **Used by.** [DH-D-API-5](api.md#dh-d-api-5), [DH-D-SDK-4](sdk-acp-typert.md#dh-d-sdk-4).
- **Priority.** P0. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-13](../design.md#dh-o-13). **Done when.** DH-T-VIEW-13 to -15 pass, DH-T-VIEW-25 passes inside its budgets, and DH-E-VIEW-3 has passed.

<a id="dh-d-view-6"></a>
### DH-D-VIEW-6 Full-text search finds text in any script, in every process, as soon as it is committed
- **Result and why it matters.** A user or a model finds earlier work by words, including Chinese, Japanese and Korean, without a second database.
- **How it is implemented.** The `search` row projection (extraction rule kept from upstream: messages, tool calls and results, todos, failure detail, plus titles; no reasoning), text preparation, the two modes, keyset paging, grouping by session. Rebuilding the index is part of the one rebuild operation of [DH-D-STORE-6](session-store.md#dh-d-store-6), which calls `Registry.Rebuild`; there is no separate reindex.
- **Components and files.** `session/search`, `session/query`. **Depends on.** [DH-D-VIEW-5](#dh-d-view-5), `DH-LIB-SQLITE`. [DH-D-STORE-6](session-store.md#dh-d-store-6): the one rebuild operation. **Used by.** The rebuild command of [DH-D-CLI-7](app-cli.md#dh-d-cli-7).
- **Priority.** P1. **Outcome.** [DH-O-5](../design.md#dh-o-5). **Done when.** DH-T-VIEW-16 passes, DH-T-VIEW-17 (the measurement) passes with its result recorded and accepted, DH-T-VIEW-25 passes, and DH-E-VIEW-3 and DH-E-VIEW-6 have passed.

<a id="dh-d-view-7"></a>
### DH-D-VIEW-7 The model can list workspaces and sessions, bounded and scoped
- **Result and why it matters.** An agent can find a session to refer to, and cannot enumerate what its caller may not see.
- **How it is implemented.** The `session.tools` component registers the two tools of [§6.2](#62-contracts) with schemas derived from Go types; visibility comes from the initiating agent and the configured policy.
- **Components and files.** `session/query/tools`. **Depends on.** [DH-D-VIEW-5](#dh-d-view-5). [DH-D-TOOLS-2](tools.md#dh-d-tools-2) (pipeline); [DH-D-STORAGE-7](storage.md#dh-d-storage-7) (workspace registry).
- **Priority.** P1. **Outcome.** [DH-O-6](../design.md#dh-o-6), [DH-O-5](../design.md#dh-o-5). **Done when.** DH-T-VIEW-14 and DH-T-VIEW-18 pass.

<a id="dh-d-view-8"></a>
### DH-D-VIEW-8 A session can be taken away as an archive or a transcript, and quotes are plain text
- **Result and why it matters.** A person can hand a session to someone else, or paste a readable copy, from the UI or the command line, and is told when the copy is incomplete.
- **How it is implemented.** Streaming ZIP writing with the standard library (`DH-LIB-FFLATE`), entry names checked, a manifest; a Markdown formatter with `log` and `surface` scope, an open-turn mark and optional descendants; `Quote` and `RecognizeQuotes` for the composer and the message renderer.
- **Components and files.** `session/transcript`. **Depends on.** [DH-D-VIEW-5](#dh-d-view-5), `DH-LIB-FFLATE` (ZIP writing). [DH-D-STORE-7](session-store.md#dh-d-store-7) (the per-session line format); [DH-D-STORAGE-3](storage.md#dh-d-storage-3) (attachments). **Used by.** Routes ([DH-D-API-5](api.md#dh-d-api-5)), commands ([DH-D-CLI-7](app-cli.md#dh-d-cli-7)), the composer and the renderer ([DH-D-UICHAT-2](ui-chat.md#dh-d-uichat-2), [DH-D-UICONV-2](ui-conversation.md#dh-d-uiconv-2)).
- **Priority.** P1. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-12](../design.md#dh-o-12). **Done when.** DH-T-VIEW-19 and DH-T-VIEW-20 pass and DH-E-VIEW-6 has passed.

<a id="dh-d-view-9"></a>
### DH-D-VIEW-9 A session is uploaded only after the user confirmed exactly what is sent
- **Result and why it matters.** A fresh installation sends nothing. A user who wants to share a session with feedback sees its size and redactions first, and can see afterwards whether it arrived.
- **How it is implemented.** The `session.upload` component: `Status`, `Preview`, `Authorise`, `Cancel`; the `upload` fold; the redactor registry with the baseline; an uploader that reads events after the watermark and hands records to A18's channel.
- **Components and files.** `session/upload`. **Depends on.** [DH-D-VIEW-2](#dh-d-view-2), `DH-LIB-OTEL`. [DH-D-HOST-5](host-telemetry.md#dh-d-host-5) and [DH-D-HOST-4](host-telemetry.md#dh-d-host-4) (the exporter channel with per-batch acknowledgement, the off-switch); [DH-D-HUMAN-7](interaction-credentials.md#dh-d-human-7) (released secret values for the redactor). **Used by.** The feedback form that calls `Preview` and `Authorise` is [DH-D-HUMAN-10](interaction-credentials.md#dh-d-human-10), which depends on this deliverable. Runtime prerequisite: an operator-configured endpoint.
- **Priority.** P2. **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Done when.** DH-T-VIEW-21 and DH-T-VIEW-22 pass and DH-E-VIEW-4 has passed.

<a id="dh-d-view-10"></a>
### DH-D-VIEW-10 Every tool of a session works in the same directory, and only the host moves it
- **Result and why it matters.** File tools, shells, discovery and the sandbox agree on one directory; entering a worktree does not change which project the session belongs to.
- **How it is implemented.** The `session.workdir` component: the `workingDirectory` fold (summary), per-session serialisation, validation through the filesystem seam, recovery, the `workdir.Access` key, the context text `Current working directory: <quoted path>.` contributed to prompt assembly, and a notice through the inbox after each move.
- **Components and files.** `session/workdir`. **Depends on.** [DH-D-VIEW-1](#dh-d-view-1), [DH-D-VIEW-2](#dh-d-view-2). [DH-D-TOOLS-5](tools.md#dh-d-tools-5) (context entries are logged before they are sent), [DH-D-AGENT-2](agent-loop.md#dh-d-agent-2) (the inbox, for notices), [DH-D-FS-4](filesystem.md#dh-d-fs-4) (filesystem seam). **Used by.** The policy resolver ([DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1)); a child starts in its creator's directory ([DH-D-SUBAGENT-4](subagent-team.md#dh-d-subagent-4)).
- **Priority.** P0. **Outcome.** [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10), [DH-O-4](../design.md#dh-o-4). **Done when.** DH-T-VIEW-23 and DH-T-VIEW-24 pass and DH-E-VIEW-5 has passed.

## 9. Acceptance

### 9.1 Local tests

`D-n` in the "Proves" column is short for `DH-D-VIEW-n`.

| Test ID | Proves | What is asserted |
|---|---|---|
| DH-T-VIEW-1 | D-1, D-2 | Property: for every registered definition and generated logs, state and rows from a checkpoint at any cut plus the suffix equal a full fold |
| DH-T-VIEW-2 | D-1 | Race detector: with concurrent appends, every snapshot equals a refold through its `AsOfSeq`, and a page read in the same call ends at it |
| DH-T-VIEW-3 | D-1 | A component that needs the registry fails to load without it; two versions of one key are refused; an unknown key is an error; a fold is not called for a type it did not declare |
| DH-T-VIEW-4 | D-1 | The import check fails a fold package that imports I/O, clocks or randomness; folding a log twice gives identical bytes; a slow fold is reported |
| DH-T-VIEW-5 | D-2 | Fault injection at each step of a commit: stored state is never ahead of stored events; a failing derived write loses no event |
| DH-T-VIEW-6 | D-2 | A version bump makes the old row unusable; readers see `pending` then `current`; a rebuild killed half-way resumes; rebuilt rows equal a fresh fold |
| DH-T-VIEW-7 | D-2 | `Stored` never blocks and reports `behind` with the right `seq`; `Exact` catches up or fails with `view/behind`; a panicking fold fails one key only |
| DH-T-VIEW-8 | D-2 | A fork's state equals a fold of its logical log with its inherited count; deleting a session leaves no derived row; prune removes only unknown keys |
| DH-T-VIEW-9 | D-3 | Golden logs for list summary, turn index (kinds, unsettled then settled), statistics (every attempt timed, counts by outcome) and event index (class after a replacement) |
| DH-T-VIEW-10 | D-4 | Control and direction characters are removed; truncation never splits a character; fallback limits; rename pins; unpin restores automatic titles |
| DH-T-VIEW-11 | D-4 | With the replay provider: the request sent equals the one rebuilt from `session/title-request`; that event is flushed first; exactly one attempt event for success, error, timeout, cancel, supersede and restart |
| DH-T-VIEW-12 | D-4 | One automatic call for a fresh session; a fork retitles once unless pinned; a subagent session never; a stale result cannot append; over-limit input is reduced as specified; an unavailable route is not replaced; a turn is not delayed |
| DH-T-VIEW-13 | D-5 | Windows clamp to the maximum; classes equal the surface fold; traces and lineage are correct across forks, with an unresolved parent and with the node bound |
| DH-T-VIEW-14 | D-5, D-7 | The zero visibility sees nothing; an agent's scope cannot read another workspace; a hidden session is indistinguishable from a missing one, in every method |
| DH-T-VIEW-15 | D-5 | Paging returns each visible session once while others gain prompts or are created; rows read per page do not grow with the corpus; a 10,000-session benchmark is recorded |
| DH-T-VIEW-16 | D-6 | A fixed corpus in English, Chinese, Japanese and Korean: expected hits for one-, two- and many-character queries; search operators are literal; order is stable; an incremental index equals a rebuilt one; literal mode finds a substring inside a token; cancellation returns within the bound |
| DH-T-VIEW-17 | D-6 | Benchmark (measurement M3): append cost and index size for each tokenisation option. A recorded result, not a pass by itself |
| DH-T-VIEW-18 | D-7 | Through the executor and in keyless replay: invalid arguments never reach the tool; limits hold; a cursor round-trips; scoped and unscoped listing; result size within the bound |
| DH-T-VIEW-19 | D-8 | Golden archive (session, descendants, attachments, manifest with an omission); unsafe entry names refused; transcript in both scopes with an open turn; memory use independent of size; the command leaves no partial file |
| DH-T-VIEW-20 | D-8 | Quote lines with up to three leading spaces; nothing inside a fenced block; no lazy continuation; encode then recognise round-trips |
| DH-T-VIEW-21 | D-9 | A fresh home sends nothing to a recording endpoint; enabled but unconfirmed sends nothing; an inherited authorisation sends nothing; the off-switch stops without a drain and nothing resumes; a changed rule set sends nothing |
| DH-T-VIEW-22 | D-9 | The watermark moves only on acknowledgement; a restart resumes after it; released secret values and known shapes are masked; a failing redactor withholds the record; the preview equals what is sent |
| DH-T-VIEW-23 | D-10 | `Get` does no I/O; `Set` validates, serialises and records the canonical path; `SetForCall` accepts the anchor and paths inside it, refuses anything else with `workdir/outside-anchor`, and never moves the anchor; recovery goes through `workdir.Access`; both missing, or a denied recovery, gives `workdir/unavailable` and no event; a denial changes nothing; the header never changes |
| DH-T-VIEW-25 | D-5, D-6 | Budget tests ([DH-ADR-TOOLCHAIN-14](../upstream/toolchain.md#dh-adr-toolchain-14)), benchmarks behind the `bench` build tag: the first list page and a ranked search on the heavy-tailed session corpus, and a snapshot of a session above 100,000 events, stay inside their budgets, also in the memory-limited rerun (numbers from M3 and M4 of [../libs/sqlite.md](../libs/sqlite.md)). `TreeMessages` returns the messages between agents of one tree in time order, bounded by its page, and nothing outside the caller's visibility; a literal search with one session returns that session's `seq`s |
| DH-T-VIEW-24 | D-10 | In the default composition no registered tool can move the directory (driven through the pipeline); with the worktree entry mounted, a tool call can move it inside the anchor and no sequence of calls ends outside it; the directory context is a logged event before the request; the next call's policy uses the new directory; an existing terminal keeps its own |

### 9.2 Real-environment scenarios

<a id="dh-e-view-1"></a>
#### DH-E-VIEW-1 A real model titles a session, and a failing provider leaves the fallback
- **Proves.** DH-D-VIEW-4.
- **Given.** A built `dsh`, an empty temporary home, no API key anywhere, and, under OWNER-2, a real model reached only through the user's own installed official client, signed in by the user beforehand, on its signed-in client route ([llm.md](llm.md#dh-d-llm-12) DH-D-LLM-12: `claude-code`, then `copilot-cli`), registered by a configuration layer with the default title configuration. Re-authored from "one real model API key" by `docs/stories/harness-DSH-M1/gap-closure-plan.md` step 16.5.8 (M1-D-CLOSE-4 A). File: `src/harness/internal/testkit/scenarios/view_title_scenario_test.go`, test `TestModelTitlesSession`, one subtest per route.
- **When.** Per route: (1) send one prompt through the headless runner. (2) Read the session's events. (3) Start a second dsh whose title route is the other client route with that client signed out (its configuration home, `CLAUDE_CONFIG_DIR` or `COPILOT_HOME`, an empty temporary directory), and send one prompt.
- **Then.** (1) The list shows a fallback title and then a generated one. (2) The log holds `session/title-request` before any `session/title-attempt`, one attempt with outcome `accepted`, and a `session/title` that cites it; the accepted attempt records the client invocation of the route (client, version, argument and standard-input digests), the boundary where SP-1 holds on a client route (M1-D-PIPE-4 A). No recording proxy can sit between a client and its vendor, so the byte equality of the logged request and what the client received is left to DH-E-SESSION-1 and DH-T-LLM-20. (3) The second session keeps its fallback, holds one attempt with outcome `failed`, and its turn completed normally. The `claude-code` leg must pass; the `copilot-cli` leg is run and recorded either way (M1-D-LOOP-6 A). Step (3) of the Claude leg signs the Copilot client out; if an empty `COPILOT_HOME` does not, the layer names a title route that fails by other means, since only the failed attempt is asserted.
- **Cleanup.** Delete the homes and the empty configuration home. Nothing of the user's sign-in is touched. Keep both session exports if an assertion fails.

<a id="dh-e-view-2"></a>
#### DH-E-VIEW-2 After the host is killed mid-turn, every view equals a refold of the log
- **Proves.** DH-D-VIEW-1, DH-D-VIEW-2, DH-D-VIEW-3.
- **Given.** A built `dsh` on Windows, Linux and macOS, a temporary home on a local disk, a session with at least 500 events and a turn that is running a long shell command.
- **When.** Kill the host process. Start it again. Read the session list without opening the session; then open it; then run `dsh store rebuild` with its verify option.
- **Then.** No stored state has a `seq` above the stored head. The list row is `current` or says `behind` with its `seq`. After opening, every view equals a fresh fold. The verify command reports no difference between stored and refolded rows.
- **Cleanup.** Delete the home; keep the database file if an assertion fails.

<a id="dh-e-view-3"></a>
#### DH-E-VIEW-3 A second process lists and searches a session another process is writing
- **Proves.** DH-D-VIEW-2, DH-D-VIEW-5, DH-D-VIEW-6.
- **Given.** Two real `dsh` processes on one home ([DH-ADR-12](../design.md#dh-adr-12)): A holds a session's lease and runs turns; B is a web host.
- **When.** While A appends, B lists sessions, opens the session read-only and searches for a word A just committed.
- **Then.** B's values never carry a `seq` above the head it read in the same transaction; the word is found after A's commit and not before; B never writes an event; a "busy" database gives B one classified error, not a partial page.
- **Cleanup.** Stop both processes and delete the home.

<a id="dh-e-view-4"></a>
#### DH-E-VIEW-4 Nothing is uploaded without consent, and a confirmed upload survives a restart
- **Proves.** DH-D-VIEW-9.
- **Given.** A real OpenTelemetry Collector on loopback that writes received logs to a file, a temporary home, a session whose tool output contains a planted secret value.
- **When.** (1) Use the session and submit feedback with default settings. (2) Turn session upload on, submit feedback, decline the preview, then accept it, and kill the host after the first batch. (3) Restart. (4) Set the off-switch during another upload.
- **Then.** (1) The collector file is empty. (2) Nothing arrives for the declined submission; the preview's counts match what later arrives. (3) The upload resumes after the recorded watermark and the collector ends with one record per event up to the feedback event, with the planted value masked. (4) No request is made after the switch, and the session shows the upload as cancelled.
- **Cleanup.** Stop the collector, delete its file and the home.

<a id="dh-e-view-5"></a>
#### DH-E-VIEW-5 A host move into a real worktree changes where tools work and what they may write
- **Proves.** DH-D-VIEW-10.
- **Given.** A real git repository with a linked worktree, a real sandbox backend in `workspace-write` mode, a real model API key, a session opened in the repository.
- **When.** (1) Ask the model to change the session directory. (2) Move the session into the worktree through the host operation. (3) Run a relative file write. (4) Delete the worktree directory and send a prompt. (5) Delete the repository too and send a prompt.
- **Then.** (1) No tool exists that does it. (2) One `workspace-root/change`; the header is unchanged; the next request carries the new directory as a logged context message. (3) The write lands in the worktree; a write to the original project is denied by the sandbox. (4) The session recovers to the original project and logs it. (5) The turn is rejected with `workdir/unavailable` and the UI offers a directory picker.
- **Cleanup.** Remove the repository, the worktree and the home.

<a id="dh-e-view-6"></a>
#### DH-E-VIEW-6 In a real browser: search in two scripts, an export with a final status, a transcript copy
- **Proves.** DH-D-VIEW-6, DH-D-VIEW-8.
- **Given.** A web host, a real browser, a session with English and Chinese messages, one image attachment whose file was deleted, one subagent session.
- **When.** Search for an English word and a two-character Chinese word; download the archive; copy the transcript; run `dsh session export --out` for the same session.
- **Then.** Both searches show the right events with snippets. The page shows the export as complete with one omission; the archive opens, holds both sessions, and its manifest names the missing attachment. The clipboard holds the transcript, which states its cut. The command's file holds the same entries as the download.
- **Cleanup.** Delete the downloaded files and the home.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Exporter acknowledgement (host-telemetry.md, [DH-D-HOST-5](host-telemetry.md#dh-d-host-5)).** The upload watermark moves only if the exporter channel reports acknowledgement per batch. That document does not state it yet; without it `session-upload/delivered` cannot be written and an upload ends as `unavailable`, never as best effort. Which destination exists once a user opts in is that area's question; here an empty endpoint means `unavailable`.
2. **Closed from interaction-credentials.md.** The feedback form calls `Preview`, `Records` and `Authorise` (`DH-D-HUMAN-10`), and the credential store hands released values to the baseline redactor through `Store.Mask`, which prints nothing.
3. **Unmeasured.** Index cost for CJK preparation (M3); the cost of per-commit state writes; rebuild time for a large home. Thresholds are the owner's.
4. **Limits kept.** The five carried rows of [§5](#5-tech-debt-and-its-resolution). Redaction is by pattern and exact value, not by understanding. Unsettled stream text is in no view. A sent record cannot be recalled. Delivery of an upload is at least once.
5. **Not decided here.** Whether a model may list sessions outside its workspace is a policy value (`all`) the operator sets; nothing wider than the owner's own visibility is possible.
6. **Settled with other areas, for the record.** A05 holds the five derived tables, commits derived rows in a savepoint and keys checkpoints by fold version; this area decides when its checkpoints are written, stores the original text in the index and does not index a fork's inherited events again. Catch-up reads A05's `Reader`, which includes the unflushed tail in the writing process. `session/title-request` declares its request builder in its own type declaration. A directory notice is an `Inject` send (A02). The working directory is the workspace root the sandbox resolver reads (A10 states the same). The experimental query tools (X3) call `Query`; a second title cadence (X6) is a second `title.Provider` whose `Cadence()` is `EveryPrompt`.

## 11. Units dispositioned by this spec

91 assigned units, all with a row in `trace/fragments/A06.jsonl`: 15 `UP-PKG` (9 ported, 4 merged, 2 replaced), 59 `UP-DEBT` (52 resolved, 2 not-applicable, 5 carried on purpose), 2 `UP-TOOL` (ported, changed), 2 `UP-EVT` (1 ported, 1 replaced), 7 `UP-CFG` (4 ported, 3 replaced), 6 `UP-SVC` (4 ported, 1 merged, 1 replaced). The same fragment holds final verdicts for the 11 `UP-DEC` notes (10 ported, 1 replaced) and declares 9 decisions (`DH-ADR-VIEW-1` to `-9`), 10 deliverables (`DH-D-VIEW-1` to `-10`), 24 tests (`DH-T-VIEW-1` to `-24`) and 6 scenarios (`DH-E-VIEW-1` to `-6`).
