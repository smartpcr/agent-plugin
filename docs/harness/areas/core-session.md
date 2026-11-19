# Core session log and scoped registration: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) (`2e39e70`) and are cited as `path:line` relative to the upstream root. Citations that start with `src/go/` are this repository's cordis port. Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `SESSION`. Nothing here has been built, run or tested.

| | |
|---|---|
| **Area key** | `SESSION` (agent A01) |
| **Upstream packages** | 2, both in group `core`: `packages/core/session` (3,490 source lines) and `packages/core/scope` |
| **Serves outcomes** | [DH-O-4](../design.md#dh-o-4) (model-visible means logged), [DH-O-5](../design.md#dh-o-5) (durable sessions, honest recovery), [DH-O-3](../design.md#dh-o-3) (bracketed turns, no unanswered call), [DH-O-1](../design.md#dh-o-1) and [DH-O-9](../design.md#dh-o-9) (owned, per-agent registrations), [DH-O-11](../design.md#dh-o-11) (plugin records) |
| **Depends on areas** | A04 (message, tool-schema and call-config vocabulary), A05 (storage behind the interface in §6.1), A06 (projection registry behind the fold hook), A23 (`schema` fingerprints, `testkit` clock). Used by A02, A03, A14, A27 and every area that logs an event |
| **Priority** | P0. No agent can run a turn without a session, and no tool or prompt section can be registered per agent without a scope |

## 1. Purpose

A session is the record of one agent conversation. This area owns that record while it is live: it accepts events in order, refuses malformed ones, tells observers what was accepted, and computes from the log exactly what the model is shown next. Storage (area A05) and read models such as titles and search (area A06) sit behind two seams: the storage contract, and a fold hook this area defines. Without it there is no single answer to "what did the model see", so resume, fork, replay and audit could each disagree.

The second package, scope, answers a different question: when a tool, prompt section or listener is registered "for one agent", who can see it and when does it go away. One fact, the context a registration was made through, decides both, so nothing can be visible to one agent and torn down with another.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **Event, envelope, payload** | An event is one immutable log entry. The envelope is the fields every event has (§6.2); the payload is its type-specific data, stored as JSON. | Storage and readers can order, validate and skip events without understanding every payload. |
| **Hot part, cold part** | The hot part of a payload is what folds and model history need. The cold part is large data that is rarely read, for example the raw timed stream of a model attempt. | Opening a session or building a request must not load megabytes that nothing reads. |
| **`Seq`, `Offset`** | `Seq` is the position of an existing event, from 0 with no gaps. `Offset` is a gap between events or a prefix length, and may equal the event count. | Two integer types stop "the last event" being confused with "the next position". |
| **Surface event, log-only event** | Five event types produce a model message: `system/message`, `developer/message`, `user/message`, `assistant/message`, `tool/result`. Every other event is log-only. | The model's history is built from the five and nothing else. |
| **Surface, surface operation, source list** | The surface is the ordered list of surface events the model currently sees. An event joins it by `append`, or by `replace` of an inclusive range of current entries. The source list names the earlier events an event was derived from or replaces. | Compaction hides old messages without deleting them, and a reader can check what each replacement removed. |
| **Derived history, node** | History is the message list computed from the surface. A node is one entry: the event's `Seq`, a revision counter and the message as JSON. | History is never stored twice, so it cannot disagree with the log. |
| **Request header, settlement** | The header is a logged snapshot of the non-history part of a request (model, settings, tool schemas). A settlement is the one event that records a finished model attempt: `assistant/message` or `assistant/attempt`. | Together with history they make every request a function of the log. |
| **Bracket, closer** | A bracket is an open/close pair in the log: turn, step, and a tool call with its result. A closer is a synthetic event that closes a bracket left open by a failure, a crash or a fork. | A reader can tell finished work from interrupted work, and providers reject a history with an unanswered call. |
| **Fold, core fold** | A fold is state computed by applying events in order. The core folds are the ones this area owns: surface and history, request header, tool history, open brackets. | The live session keeps folds in memory, not the event list. |
| **Message rewrite** | Upstream's "plugin-owned message projection": a pure function, owned by one event type, that changes the content of messages already on the surface without replacing nodes. | A feature such as image offload can change what the model is shown, durably and replayably, without code in the session core. |
| **Plugin record** | An opaque, always-ignorable, never-surface event in the `plugin:` namespace. | The only way a plugin process stores data in a session; a host without that plugin still reads the session. |
| **Feed, deliverer** | The feed is the in-process notification of accepted events. The deliverer is the one goroutine per session that sends it, in order, outside the append lock. | Observers must not slow or fail an append. |
| **Storage, lease** | Storage is the provider that makes records durable (A05). The lease is the right of one process to append to one session (A05). | Named here because the session calls them; defined by their owner. |
| **Scope, scope key** | A scope is a registration world for one agent or one preset. Its key is the opaque identity registries file entries under. The zero key means global. | One host runs agents with different tools and prompts. |
| **Registration context** | The cordis context a scope hands out. What is registered through it is visible in that scope and removed with it. | Visibility and lifetime come from one fact. |
| **Enclosing scopes** | The ordered list of scopes a scope inherits from, nearest first, for example an agent's preset. | A preset's tools are visible to every agent composed from it. |
| **Layer, shadowing** | A layer is one scope's contribution to one registry. Shadowing means the nearest scope's entry wins a name. | Per-agent variants of a tool or prompt section. |
| **Target** | A dispatch context that admits global listeners and the listeners of one scope and its enclosing scopes. | An event about one agent reaches only listeners entitled to it. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.core.session` | `@deepseek-ai/dsh-session` | definition and provider | Declares the event envelope and the core event types, holds live sessions in memory as the service `ctx.sessions`, validates and freezes appends, maintains the surface and derives model history, builds fork seeds and crash closers. |
| `UP-PKG-packages.core.scope` | `@deepseek-ai/dsh-scope` | library (no service) | Mints a tagged cordis context per scope key, keeps a parent relation between keys, filters event dispatch by scope, and supplies the layered tables scope-aware registries are built on. |

### 3.2 Behaviour

**Append.** `session.append(type, data, opts)` copies the payload in one pass that rejects anything JSON cannot round-trip, deep-freezes the result, validates surface metadata against the current surface, pushes the event at `seq = log.length`, and then calls `session/event` listeners one by one, logging and containing their failures (`packages/core/session/src/index.ts:521-545`, `:858-871`, `:972-992`, `:404-421`). An append made while another append's listeners are running is refused (`:952-964`). An array form validates every member before changing anything, then publishes them in order; the README states that this alone is not crash-atomic in storage (`packages/core/session/README.md:88`).

**The log is held in memory, whole.** `Session` keeps `log: SessionEvent[]` (`index.ts:556`). Three readers expose it synchronously, `eventAt`, `snapshotEvents` and `ownEvents`, and all three are deprecated because the full log will not always be resident (`index.ts:740`, `:753`, `:772`; `.agents/notes/implemented/architecture/2026-09-09-deprecate-synchronous-session-event-reads.md:15`).

**Surface and history.** `SurfaceManager` plans each candidate against the current ordered list of surface `seq`s and applies the plan only after commit (`packages/core/session/src/surface.ts:507-553`, `:569-591`). The rules: a surface type must carry `surfaceOp` and a log-only type must not (`:302-327`); sources are non-empty, unique, earlier, and cover every replaced node (`:330-365`); a `tool/result` replacement rewrites exactly one current result and only its content (`:455-483`); node 0, while it is the system prompt, may only be replaced by a `system/message` over exactly that node (`:491-504`); a tool addition in a `developer/message` must name one complete schema in an earlier `request/header` (`:368-394`). `deriveMessages()` walks the surface and caches one frozen message per node; a replacement or a rewrite bumps `contentGeneration` and rebuilds (`index.ts:1079-1100`).

**Message rewrites.** A plugin registers a pure `project(event, context)` for one event type through `ctx.sessions.registerMessageProjection()`, as an effect of its fiber (`surface.ts:35-47`, `index.ts:1161-1169`). An event of a type listed as needing one is refused when the function is missing (`surface.ts:535-537`; the list has one member, `image/offload`, `packages/core/session/src/known-event-types.ts:90-92`). If the plugin unloads, sessions that used the function refuse further reads (`surface.ts:745-757`). Detached readers must be handed the functions explicitly (`surface.ts:600`).

**Request state.** `requestHeader()` returns the latest `request/header` snapshot, folded incrementally (`index.ts:1007-1017`; `packages/core/session/src/request-header.ts:63-69`). `toolHistory()` folds headers and developer messages into the declared tools and the ordered additions, resolving each addition through the header it cites (`packages/core/session/src/tool-history.ts:26-62`).

**Vocabulary.** Event types are keys of a TypeScript interface that any package widens by declaration merging (`packages/core/session/src/types.ts:291`, `docs/subsystems/session.md:11`). A generated set lists the 64 types declared in the repository; a type outside it is accepted only with the envelope's `ignorable` marker, and the file records why registering names at run time was rejected: reads would depend on which plugins are mounted (`known-event-types.ts:15-20`, `:22-87`). `appendPluginRecord` writes ignorable `plugin:` records; a lint gate limits its callers to experimental packages (`index.ts:443`, `:498-504`; `packages/core/session/README.md:74`).

**Store and lifecycle.** `SessionStore` is `ctx.sessions`. `create` is `prepare` (build, not yet visible), `enter` (add to the store, install the feed) and `announce` (emit `session/created`; a synchronous throw vetoes creation) folded into one effect of the calling fiber (`index.ts:1206-1217`, `:1238-1286`, `:1311-1344`, `:1367-1396`). Removal emits `session/disposed` (`:1347-1356`). `flush(session)` dispatches `session/flush` in parallel and rethrows the first failure after all listeners settle (`:1422-1439`). Persistence is not in this package: a backend listens to `session/event` and drains on `session/flush` (`packages/core/session/README.md:28`). All four events are dispatched through a scope carrier built from the scope of the context the session was entered through (`index.ts:1313`).

**Fork and closers.** `fork(source, boundary?)` works only on a live source. It copies the prefix through `boundary`, appends `session/end-seed { inherited: true }`, then closers with cause `forked` for an open tail (`index.ts:1483-1500`, `packages/core/session/src/fork.ts:21-29`). An ordinary fork records no source; only subagents record `parentSession`, and `origin` classifies them (`types.ts:111-126`). `openTurnClosers` scans a log and returns an error `tool/result` per unanswered call (`TOOL_NOT_STARTED` when no `tool/call` was recorded, `TOOL_OUTCOME_UNKNOWN` when one was), a missing `step/end` and a `turn/end`; ids are deterministic and timestamps reuse the last real event (`packages/core/session/src/repair.ts:53-98`, `:157-197`). Inference: `docs/subsystems/session.md:713` says a boundary inside an open turn is rejected, which the code above does not do; this design follows the code.

**Scope.** `createScope(ctx, key)` starts an empty plugin fiber under `ctx` and tags its context with the key (`packages/core/scope/src/index.ts:137-147`). The agent loop mints one per agent with the agent object as its own key (`packages/core/agent-loop/src/agent.ts:148`). A key may have one parent, bound once; only the holder of the returned binding may re-link it, and cycles are rejected (`index.ts:54-82`). The preset registry uses this to make each agent a child of its preset's scope and to re-link on a preset change (`packages/preset/agent-preset-registry/src/index.ts:120`, `:271`, `:281`). The glossary still calls scope "two levels, flat" (`docs/glossary.md:13`); the source has the chain. `scopeTarget(base, key)` builds a dispatch carrier that admits untagged listeners and listeners tagged with the key or one of its ancestors: events flow up the chain, never down (`index.ts:170-185`). `ScopedLayers` keeps one global layer and lazily created per-key layers; `merge` returns global entries overlaid by the chain, nearest last; `peek` reads one exact layer and deliberately ignores the chain (`packages/core/scope/src/store.ts:180-217`). A registration is one cordis effect of the registering context, so visibility and lifetime share an owner (`store.ts:226-266`, `packages/core/scope/README.md:60`).

### 3.3 Failure, retry, rollback and cleanup upstream

| Situation | What upstream does | Left behind |
|---|---|---|
| A payload or surface rule is violated | The append throws before the log changes (`packages/core/session/README.md:60`) | Nothing |
| A `session/event` listener throws or rejects | Logged and contained; later listeners still run (`index.ts:404-421`) | The event stays committed |
| A `session/created` listener throws | Creation is vetoed; the entry is removed and `session/disposed` is emitted; the session stays read-only (`index.ts:1367-1396`) | Nothing in the store |
| A flush listener fails | `flush` rejects after all listeners settle (`index.ts:1436-1437`) | Events stay in the backend's buffer |
| The process dies mid-turn | Nothing here. On resume the agent loop appends closers from `repair.ts`, and the query service synthesises them on cold reads (`types.ts:225-231`) | **Uncertain:** whether a recorded tool call took effect |
| A message-rewrite owner unloads | Sessions that used it refuse further derivation until restored (`surface.ts:745-757`) | A live session that cannot build a request |
| A scope is disposed | Its fiber unwinds every registration; racing callers await the same completion (`packages/core/scope/src/index.ts:141-146`) | An emptied layer is reclaimed (`store.ts:257-261`) |

## 4. Upstream decisions dissected

Notes are under `.agents/notes/`; the first column gives status and file name. "ADR-n" means `DH-ADR-SESSION-n` in §7.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| implemented `2026-06-11-event-sourced-sessions` | The log is the only truth; history is derived; appends never wait for disk | keep | Nothing in it depends on the language; it is what makes resume, fork and replay one mechanism | ADR-1, [PERSISTENCE-1](../upstream/session-and-persistence.md#dh-adr-persistence-1) |
| implemented `2026-06-11-dev-invariants-over-deep-readonly` | Always-on copy and deep freeze of every event, in the session and not in a dev plugin | change | The goal stands and stays always-on. Go cannot freeze; the committed payload becomes an immutable string | ADR-3, [ARCH-11](../upstream/architecture.md#dh-adr-arch-11) |
| implemented `2026-06-18-session-surface` | An ordered surface kept by `append` and `replace` markers with cited sources | keep | The rules are properties of the log; all are ported (§6.2) | ADR-5 |
| implemented `2026-09-06-v3-canonical-session-envelopes` | One spelling per fact; required placement; strict acceptance | keep | A typed Go envelope makes most alternative spellings unrepresentable; the rest are checked at append and at open | ADR-5 |
| implemented `2026-09-02-system-prompt-as-surface-node` | The system prompt is surface node 0; a protected head | keep | One home for every model-visible message; the head rule is in the fold | ADR-5 |
| implemented `2026-09-15-first-class-tool-role-messages` | A tool result is its own tool-role message; the role set is closed | keep | The Go format starts in this shape, so the V3-to-V4 conversion has no subject | ADR-5 |
| implemented `2026-09-01-v2-embedded-assistant-streams` | One settlement per attempt embedding the raw timed stream; no chunk events | change | Keep one settlement per attempt. The stream moves to the cold part so history reads stay small, and usage stays in the hot part | ADR-5, ADR-7 |
| implemented `2026-09-17-developer-session-changes` | Tool additions and removals are a `developer/message` bound to an earlier header by `headerSeq` | keep | Exact replay of tool changes needs the historical schema, not the current registry | ADR-5 |
| implemented `2026-09-20-dynamic-tool-updates` | Headers record active tools; the session caches a capability-independent tool history | keep | The fold is a core fold; projecting it per provider route is A04's | ADR-1, ADR-7 |
| implemented `2026-09-17-persistence-attribution-policy` | A fingerprint policy lets a producer add an attribution kind without a format bump; request-only prompts are a separate type | change | The policy exists for TypeScript union fingerprints. In Go the raw payload is authoritative and decoding tolerates unknown fields and kinds, so an added kind is additive. Request-only input enters a request only through a logged event | ADR-5, ADR-7 |
| implemented `2026-09-11-plugin-owned-message-projections` | A plugin registers a pure interpreter for its event; missing interpreter refuses the log | change | Keep purity and ownership. Bind the function statically to the event type and payload version, so it can never be missing | ADR-6 |
| implemented `2026-09-09-deprecate-synchronous-session-event-reads` | Random synchronous history reads are deprecated; state comes from projections | keep | Adopted fully: the Go session never holds the event list and offers only paged reads | ADR-1 |
| implemented `2026-08-30-retain-ignorable-external-session-events` | Keep `ignorable`; one writer for `plugin:` records, limited by lint to experimental packages | change | Keep the marker and the namespace. The caller limit becomes a capability checked for plugin processes; compiled-in experimental components declare ignorable types instead | ADR-4 |
| implemented `2026-10-23-independent-session-forks` | A fork records no source; only subagents record a creator | change | Keep "copied history is not delegation". Upstream dropped the source because one field carried both meanings; with two fields the origin can be recorded without that defect | ADR-11 |
| implemented (simplification) `2026-07-28-remove-synthetic-log-only-turns` | Log-only events may stand between turns; durability is an explicit flush | keep | A turn means one model-loop execution; the bracket check constrains only bracket events | ADR-8 |
| rejected (simplification) `2026-06-20-drop-durable-step-boundaries` | Keep `step/start` and `step/end` | keep the rejection | `step/end` is the durable sign a request finished; closers and the bracket check need it | ADR-8 |
| rejected `2026-06-16-typed-event-schemas` | A runtime schema registry was declined as too large a redesign | reverse | Go has no declaration merging; a registry is the only form. Upstream's objection to composition-dependent reads is met by declaring statically | ADR-4 |
| proposed `2026-09-06-logical-session-storage-rebuild` | Consumers use a logical session and a service; storage is a private provider | adopt | Taken as the starting shape, without upstream's five migration stages | ADR-2 |
| proposed `2026-09-10-session-capability-protocols` | Small logical core plus storage, projection and query capabilities | adopt | Maps onto three Go interfaces with one owner each | ADR-2 |
| code: persistence as a listener of `session/event` and `session/flush` (`README.md:28`) | Keeps the core free of storage | change | The session calls its storage directly, so one group is one transaction and flush has one owner | ADR-2, ADR-9 |
| code: listeners run inside `append`; re-entry refused (`index.ts:952-964`) | Free on one thread | change | Go delivers outside the lock, so a listener may append | ADR-9 |
| code: `prepare`, `enter`, `announce` with a synchronous veto (`index.ts:1238-1396`) | Lets the agent fold the session into one ordered effect | keep, simplified | `Prepare` and `Publish`; the veto is an ordered dispatch | ADR-9 |
| code: `session/end-seed` is writable by anyone (`types.ts:428-430`) | Not enforced | change | The type is reserved to the core, so no component can mark live work as inherited | ADR-4 |
| code: scope key is an object; the agent is its own key (`docs/subsystems/scope.md:11`) | Identity comparison through a `WeakMap` | change | An opaque key minted by a table; no dependence on garbage collection | ADR-10 |
| code: one registration context decides visibility and lifetime (`scope/README.md:60`) | Prevents visible-in-one, torn-down-with-another | keep | The core property of the package | ADR-10 |
| code: single parent, bound once, re-linked only by the binder (`scope/src/index.ts:72-82`) | Presets enclose agents | change | An ordered list of enclosing scopes; the privilege is holding the scope object | ADR-10 |
| code: the scope fiber is a child of the minting plugin (`scope/src/index.ts:139`) | Gives scoped code the minter's services | change | Scope fibers hang under a component that injects nothing | ADR-10 |

## 5. Tech debt and its resolution

| Debt ID | What it is | Evidence | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-deprecated.packages/core/session/src/index.ts:740` | `eventAt`: synchronous read of one old event | `index.ts:745-747` | Resolved. The session interface has no such method. Old events are read with the paged `Read`, which may hit storage and takes a `context.Context` (ADR-1, DH-D-SESSION-2). |
| `UP-DEBT-deprecated.packages/core/session/src/index.ts:753` | `snapshotEvents`: synchronous copy of a range or the whole log | `index.ts:759-768` | Resolved, same mechanism. The live session does not hold the event list, so there is nothing to snapshot; fork reads its prefix from storage (ADR-1, ADR-11). |
| `UP-DEBT-deprecated.packages/core/session/src/index.ts:772` | `ownEvents`: synchronous copy of the non-inherited suffix | `index.ts:776-779` | Resolved. `Header().InheritedCount` gives the cut and `Read` from that offset gives the suffix, paged (DH-D-SESSION-2). |
| `UP-DEBT-limit.packages.core.session.1` | Fork needs a live source, and does not repair results missing from already closed steps | `README.md:208` | Resolved in both halves. Fork reads the prefix from storage, live or not (ADR-11). A step cannot be closed with an unanswered call, so a Go log has no closed step to repair (ADR-8). |
| `UP-DEBT-limit.packages.core.session.2` | One format version for the whole log, a migration chain, and review rules for bumping it | `README.md:209` | Resolved. There is no log-wide version: each event type has a payload version with converters on read (ADR-5), the store reads only its own layout ([PERSISTENCE-5](../upstream/session-and-persistence.md#dh-adr-persistence-5), [-6](../upstream/session-and-persistence.md#dh-adr-persistence-6)), and upstream files go through the deferred importer ([DH-ADR-11](../design.md#dh-adr-11)). |
| `UP-DEBT-limit.packages.core.session.3` | `turn/end` has no `refusal` or `max_turn_requests` reason until a producer emits one | `README.md:210` | Resolved for the vocabulary: payload version 1 of `turn/end` has `refusal` and `budget` (the Go name for `max_turn_requests`), and an unknown kind read from a newer log is preserved (§6.2). A02's turn budget emits `budget`, and its driver emits `refusal` when an attempt settles with `llm/content-refused` ([agent-loop.md §6.2](agent-loop.md#62-contracts)). |
| `UP-DEBT-limit.packages.core.session.4` | No tree over branched sessions | `README.md:211` | Resolved at the data level. A fork records its source and cut in its own header fields, so the branch tree is a query over headers. The view is A06's (ADR-11, §10). |
| `UP-DEBT-limit.packages.core.scope.1` | Only scope-aware APIs isolate; any other service is global even when called through a scoped context | `scope/README.md:98` | Resolved as a silent trap. A scope's context reaches no service, so "calling a service through it" fails loudly; scoped events cannot be dispatched without a key; every service that accepts registrations declares `Layered` or `Shared`, and mounting a component that registers into a `Shared` one under a scope is reported (ADR-10). A `Shared` service stays shared: that is now its declared contract. |
| `UP-DEBT-limit.packages.core.scope.2` | A context carries one key; nesting collapses to one tag; no multi-membership | `scope/README.md:99` | Resolved. Scope contexts cannot nest, because every scope fiber hangs under the scope component; hierarchy has one representation, the key's ordered list of enclosing scopes, and that list may name unrelated scopes (ADR-10). |
| `UP-DEBT-limit.packages.core.scope.3` | Handing out a scope context hands out the minter's services | `scope/README.md:100` | Resolved. The scope fiber's parent injects nothing, so a holder reaches only what its own component declares (ADR-10). Third-party code never holds a context at all ([DH-ADR-4](../design.md#dh-adr-4)). |

## 6. Go design

### 6.1 Shape

**Packages**, all under `src/harness/internal/session` (the family named in [design.md §4.2](../design.md)):

| Package | Holds | Imports |
|---|---|---|
| `session` | The contract: `Service`, `ReadSession`, `Session`, `Handle`, `Folder`, `Header`, `Request`, the live event keys, the error table | `event`, `surface`, `scope`, `llm/vocab` (A04's leaf vocabulary package) |
| `session/event` | `Seq`, `Offset`, `Record` (stored form), `Event` (reader's view), `Type[T]`, `SurfaceType[T]`, `Draft`, the static registry, the 14 core payload structs, the plugin-record grammar | `llm/vocab`, `schema` |
| `session/surface` | Pure code: plan and apply surface operations, history nodes, message rewrites, bracket tracking, closers, header and tool-history folds, `Rebuild` | `event` |
| `session/core` | The component: live sessions, append lock, deliverer, open, fork | all of the above, and the storage contract |
| `session/scope` | Keys, the table, layers, scoped events. A leaf: imports only cordis | none in the harness |

A05's storage contract and providers and A06's views are sibling packages in the same family. `session/core` imports the storage contract and never a provider.

**Components.**

| Component | Injects | Provides | Why it exists |
|---|---|---|---|
| `scope.Component` (catalogue name `session.scope`) | nothing | `scope.TableKey` (`scope.Table`) | Mints scopes. It injects nothing on purpose (ADR-10) |
| `core.Component` (catalogue name `session.core`) | the storage service (A05), the fold hook `session.FolderKey` (A06), `scope` | `session.Key` (`session.Service`), replacing `ctx.sessions` | Owns every live session in this process |

**Interfaces.** Signatures are abbreviated; `ctx` is a `context.Context` and `owner` a `*cordis.Context`.

```go
// package session; ID is a string type, and Offset is event.Offset
type Service interface {
    Prepare(ctx, owner, CreateOptions) (*Pending, error) // built, leased, invisible; Publish or Discard
    Create(ctx, owner, CreateOptions) (Handle, error)    // Prepare + Publish
    Open(ctx, owner, ID) (Handle, OpenReport, error)     // write lease; closes an interrupted turn (ADR-8)
    OpenRead(ctx, ID) (ReadSession, func(), error)       // no lease; folds what is committed; func releases it
    Fork(ctx, owner, ForkOptions) (Handle, error)        // source by ID, live or stored (ADR-11)
    Get(ID) (Session, bool)                              // access to a session live in this process
    Live() []Session                                     // copy, in creation order
    AppendLogOnly(ctx, ID, []event.Draft) (Outcome, error) // a change the model never sees, wherever the session is held (ADR-12)
}
type ReadSession interface {
    ID() ID; Header() Header                             // Header is a struct of scalars and strings
    Len() Offset; Durable() Offset                       // next seq; first seq not yet flushed
    Read(ctx, from Offset, limit int, opts ...ReadOption) ([]event.Event, error) // WithCold() adds cold parts
    History() surface.History                            // immutable snapshot of derived model history
    RequestHeader() (surface.RequestHeader, bool); ToolHistory() surface.ToolHistory
    Open() surface.Brackets                              // open turn, step and unanswered calls, as values
    Scope() scope.Key
}
type Session interface {
    ReadSession
    Append(ctx, event.Draft) (event.Receipt, error)
    AppendGroup(ctx, []event.Draft) ([]event.Receipt, error) // all or none, one storage transaction
    AppendPluginRecord(ctx, typ string, data json.RawMessage) (event.Receipt, error)
    CloseOpen(ctx, surface.Cause) ([]event.Receipt, error)   // closers for the open turn, as one group
    Flush(ctx) (Offset, error)                               // durability barrier; returns the new Durable
    NextRequest() (Request, error)                           // ADR-7
    RequestFrom(event.Seq) (Request, error)                  // an auxiliary request described by one committed event
    AsOf(func(at Offset))                                    // run fn at one consistent position; fn must not block
}
type Handle interface { Session; Close(ctx) error }          // ownership: only the creator holds it
// Request is a struct with unexported fields and no constructor (ADR-7). A04's adapter seam receives it and reads it through
// accessors: Route(), Model(), Controls(), Messages(), Tools(), ToolHistory(), Meta() (session and purpose labels), AsOf(), Digest().

// What this area offers A06: one hook, called under the append lock, in seq order, exactly once per event.
type Folder interface {
    Fold(s ReadSession, batch []event.Event) []Derived // pure CPU; cold parts absent; a panic is contained
}
```

**The storage contract is A05's, and there is one.** [session-store.md §6.1](session-store.md#61-shape) defines the contract package `store` (`Store`, `Reader`, `Writer`, `Batch`) with an in-memory provider and a conformance suite. This document declares no storage interface. The session log interface above sits on top of it, and the core relies on these clauses of it:

| The core relies on | Why | Clause in session-store.md |
|---|---|---|
| Create, open for writing under the lease, open for reading, and create a child from a stored prefix | Lifecycle and fork (ADR-11) | `Create` with `ForkOf`, `OpenWrite`, `OpenRead` |
| A batch is accepted in order without waiting for a sync; its events are one transaction, and the derived rows handed over with it commit with them or not at all | Group atomicity (ADR-2) | `Writer.Append`, the append transaction |
| A barrier for one session that returns when its accepted records are durable | `Flush` | `Writer.Flush` |
| A range read in the writing process includes records accepted and not yet flushed | `Read` and the deliverer depend on it (ADR-9) | The `Reader` row |
| A read of a given list of `seq`s | Restoring the visible nodes from a checkpoint that holds structure only | `Reader.ReadSeqs` |
| Checkpoints under reserved keys `core/*`, written when the core hands one over (turn end, close, every `checkpointEvery` events) | Opening without a replay (ADR-1) | The `checkpoints` table |
| The hot and cold parts stored and read separately | ADR-5 | The `data` and `aux` columns, `WithAux` |
| Payloads handed over and returned as immutable strings | ADR-3 | `Record` is this area's `event.Record` |
| An open refused for an undeclared required type (`session/unknown-type`) or a newer payload version (`session/newer-payload`), judged against this area's registry | ADR-4 | Schema evolution per event type |

A05 owns everything else about storage: the lease and its epoch, layout, listing, deletion, retention, export and import, backpressure when the disk is slow, and how a reader in another process follows a session (`Store.Follow`). A06 owns the projection registry behind `Folder`, its units and their checkpoints, snapshots for clients, search and queries; `Derived` is the rows a unit wants committed with the batch. `AsOf` lets A06 read all its states at one `seq`, and `surface.Classify` (current, shadowed or log-only) lets a query use the same fold as model history. The core folds are not units of A06's registry, because they must refuse a candidate before it is committed and a projection only sees committed events; they share the checkpoint table.

**What the live session holds.** Not the events. It holds the core folds: the surface (an ordered list of nodes, each `Seq`, revision, role and message JSON), the header in force, the tool history with historical schemas interned by digest, the open brackets and counters. Memory grows with what the model can see, not with the length of the log (ADR-1).

**The append path.** (1) Outside any lock, the draft's payload is encoded to JSON once and size-checked. (2) Under the session's append lock: assign `Seq` and a non-decreasing `Time`; plan the surface, rewrite and bracket transitions against the folds, changing nothing; hand the storage writer one batch holding the records, the rows returned by `Folder.Fold` and, at a checkpoint position, the core folds' own checkpoint; apply the plans. (3) Wake the deliverer. A failure in the plan or in the hand-over leaves folds, storage and feed unchanged. The deliverer keeps a cursor and reads forward from storage in pages, so there is no queue to overflow and a slow listener only lags.

**Where a copy is made, and what a caller may keep** (ADR-3):

| Boundary | What crosses | Copy | Caller may keep |
|---|---|---|---|
| `Append`, `AppendGroup` in | A typed payload inside a `Draft` | Encoded to a JSON string before `Append` returns; the session keeps only the string | Its own struct, and may change it afterwards |
| `Append` out | `Receipt{Seq, Time}` | Scalars | Forever |
| Feed, `Read` | `event.Event`: unexported fields, scalar accessors | `Raw()` returns the committed string (immutable by the language, no copy). `event.As(e, T)` decodes a fresh struct per call. `Sources()` returns a new slice | Everything returned; changing it changes nothing else |
| `History()`, `Request` | A snapshot of nodes | None needed: nodes hold scalars and strings, are reached by `At(i)` by value, and a replacement builds a new array | The snapshot, unchanged by later appends. `Node.Message()` decodes a fresh message |
| `Header()`, `Open()`, `ToolHistory()` | Structs of scalars, strings and fresh slices | By value | Forever |
| Storage | `event.Record` with string payloads | None in either direction | A05 may retain records; neither side writes to a record after hand-over |
| Message rewrite | Nodes in, messages out | The result is encoded once by the session | Nothing is retained from the function |
| `scope` merges and chains | Slices of entries | Fresh slice per call | Forever |

No exported function returns a slice, map or pointer into session memory; a test over the package's type information enforces it against a short reviewed list.

**Scope.**

```go
// package scope
type Key struct{ /* opaque, comparable; the zero Key is global */ }
type Table interface {
    New(owner *cordis.Context, label string, enclosing ...Key) (*Scope, error)
    Enclosing(Key) []Key                         // linearised, nearest first, a copy
    Declare(service string, v Visibility)        // Layered or Shared
    CheckMount(injects []string) []Diagnostic    // names Shared registries a scoped component would write to
}
func (s *Scope) Key() Key
func (s *Scope) Ctx() *cordis.Context            // the registration context; reaches no service
func (s *Scope) SetEnclosing(keys ...Key) error  // only a holder of *Scope can call it
func (s *Scope) Close() error                    // idempotent; concurrent callers wait for the same completion
func Of(ctx *cordis.Context) Key                 // the key a context carries, or the zero Key
type Event[A any] struct{ /* wraps a cordis key; can only be dispatched with a Key */ }
type Layers[L Layer] struct{ /* Global(), Peek(Key), Chain(Key) []L, Add(ctx, label, mutate) */ }
func Merged[L Layer, V any](ls *Layers[L], k Key, pick func(L) *Named[V]) []Entry[V]
```

`New` mounts an empty child fiber under the scope component's own context, tagged with the key through a context value ([DH-D-CORDIS-3](../libs/cordis.md#dh-d-cordis-3)), and registers an effect on `owner` that closes it. The owner's fiber owns the scope; the scope component is only where the fiber hangs. Only package `scope` can tag a context, so the contexts that carry a key are exactly that fiber's and its descendants', and they all unload when the scope closes. `Scope.Ctx().Lifetime()` ([DH-D-CORDIS-2](../libs/cordis.md#dh-d-cordis-2)) is the cancellation context area A02 derives an agent's from. An event about one agent is declared as a `scope.Event` and dispatched with `Notify(ctx, key, args)`: listeners registered through an untagged context, through that scope or an enclosing one, or with `cordis.Global()`, are called.

### 6.2 Contracts

**The envelope** (`event.Record`). There is no format version on the log or the header.

| Field | Go type | Rule |
|---|---|---|
| `Seq` | `Seq` | Dense from 0; assigned by the session |
| `Time` | Unix milliseconds | Assigned by the session; never less than the previous event's |
| `Type` | string | A declared type, or `plugin:<owner>/<record>` |
| `Version` | int | The payload version the writer used; 0 for plugin records |
| `Ignorable` | bool | Set from the type's declaration; always true for plugin records |
| `Op`, `Start`, `End` | surface operation | Required on the five surface types, absent on all others |
| `Sources` | `[]Seq` | If present: non-empty, unique, earlier. Forbidden on `assistant/message` |
| `Payload` | string | The hot part, as JSON |
| `Cold` | string, optional | The cold part, as JSON. Declared per type by one tagged field. No fold may depend on it |

**Declaring a type.** A package declares each type in a package-level variable: name, current payload version, payload struct, converters from older versions, an event-local validator, whether it is ignorable, and an optional rewrite function per payload version. The registry is filled during program initialisation and sealed at boot; a later declaration panics. The surface flag and the name `session/end-seed` are reserved to this area. Reading: an older version is converted to the current struct; an undeclared type or a newer version is kept as raw strings if ignorable and otherwise refuses the open with A05's `session/unknown-type` or `session/newer-payload`, naming the type and first `seq`. A declaration may also carry a pure request builder, which is what makes the type usable with `RequestFrom` (ADR-7). Decoding from the log ignores unknown fields, because the raw string, never the decoded struct, is what fork, export and storage carry.

**Core vocabulary** (replaces the members of `SessionEventMap` in `types.ts:291-440`). Names are upstream's. Area A05 holds the `UP-PERSIST` ledger rows for them.

| Type | Kind | Changes from upstream |
|---|---|---|
| `turn/start`, `turn/end` | log-only bracket | Reason kinds are upstream's seven plus `budget{limit, value}` (area A02's turn budget; the Go name for ACP's `max_turn_requests`) and `refusal`; `blocked` gains `by` and `reason`; the import-only `legacy` cancel cause is not carried; an unknown kind is preserved |
| `step/start`, `step/end` | log-only bracket | None |
| `system/message`, `developer/message`, `user/message` | surface | None. Message, source and block types are A04's |
| `assistant/message`, `assistant/attempt` | surface; log-only | The stream is the cold part. Both carry `requestAsOf` and `requestDigest` (ADR-7), and `usage` and request timing (start, first token, end) as hot fields, so that no fold parses a stream. A seeded message from an importer has none of these and names its foreign source |
| `tool/call`, `tool/result` | log-only; surface | Arguments stay the raw string the model produced. `tool/result` gains the optional spill reference area A07 asks for |
| `request/header`, `request/context` | log-only | None. Empty tool lists and empty adapter defaults are omitted by the encoder |
| `session/end-seed` | log-only | Adds the lease `epoch`; written only by the core, at a fork cut and at each write-open |

**Header** (replaces `SessionHeader`, `types.ts:95-140`): `ID`, `CreatedAt`, `Cwd`, `Origin` (empty or `subagent`), `Creator` (a subagent's direct creator), `ForkSource` and `ForkThrough` (set on every fork), `InheritedCount`, `DelegationDepth`, `Preset`. Immutable after creation. A05 stores it as columns.

**Live events** (dispatch forms from [design.md §4.6](../design.md)). All three are `scope.Event`s dispatched with the session's scope key.

| Go key | Form | Payload | Replaces |
|---|---|---|---|
| `session.Created` | ordered: the first error stops the dispatch and vetoes publication | The `Session`, `How` (`new`, `resumed` or `forked`) and, when resumed, the `OpenReport`. A listener may append: this is the hook on which another area closes its own open pairs after a restart (A17's approvals and grants, A12's runs), before any agent accepts work | `UP-EVT-session/created` |
| `session.Committed` | notify: failures contained; one deliverer per session, commit order, outside the lock | The `Session` and one `event.Event` | `UP-EVT-session/event` |
| `session.Closed` | notify | `ID`, `Header`, final `Len`, cause | `UP-EVT-session/disposed` |
| none | `Session.Flush` calls the writer | | `UP-EVT-session/flush` |

**Config** of `core.Component` (upstream has no config schema for these packages). Inference: the defaults are proposals to be measured.

| Field | Default | Validation | Meaning |
|---|---|---|---|
| `maxEventBytes` | 8 MiB | 64 KiB to 256 MiB | Largest hot part; a larger draft fails with `session/too-large` |
| `maxColdBytes` | 32 MiB | at least `maxEventBytes` | Largest cold part |
| `feedPage` | 256 | 1 to 4096 | Events the deliverer reads per pass |
| `checkpointEvery` | 500 | 50 to 100000 | Events between checkpoints of the core folds, besides turn end and close |

**Errors** (`<domain>/<reason>`). Raised by storage and defined in [session-store.md §6.2](session-store.md#62-contracts): `session/not-found`, `session/exists`, `session/owned`, `session/lease-lost`, `session/unknown-type`, `session/newer-payload`. Defined here: `session/closed`, `session/invalid-event`, `session/invalid-surface`, `session/unbalanced`, `session/invalid-boundary`, `session/too-large`, `session/reserved-type`, `session/not-detachable`, `session/held-elsewhere`, `scope/closed`, `scope/cycle`, `scope/shared-registry`.

**Tools exposed to the model:** none. **Wire messages:** none; carriers read sessions through this contract. **Persisted data:** the records and header above and one checkpoint per core fold, all stored by A05.

### 6.3 Ownership, failure, retry, rollback and cleanup

Ownership: the fiber passed as `owner` owns a session through its `Handle`, and a scope through its `*Scope`. `Get`, `OpenRead` and a `scope.Key` give access and no right to close. The session owns its writer, and through it the lease, and its deliverer goroutine. A host component that must record a log-only fact on a session it does not drive (feedback, a rename, a translation) calls `AppendLogOnly` and never opens the session itself ([DH-ADR-SESSION-12](#dh-adr-session-12)). `CreateOptions.Seed` lets an importer (A05's upstream import, X3's chat import) supply drafts with their own non-decreasing times and a header creation time; they pass the same checks as any append.

| Situation | What happens | Undone, retried, or left uncertain |
|---|---|---|
| A draft is invalid (type, size, surface rule, bracket rule) | `Append` returns the error before anything changes | Nothing to undo. The caller may fix and retry |
| One member of a group is invalid | The whole group is refused | Nothing was enqueued |
| Storage refuses the batch (lease lost, store closed) | The append fails; folds are not advanced. On `session/lease-lost` the session closes itself and notifies `session.Closed` with that cause | Events accepted earlier but not yet flushed never became durable; they were already delivered to the feed. `Durable()` tells an observer which events are safe |
| `Flush` fails | The error is returned; records stay pending in the writer | Retried by the next `Flush`. Whether work may proceed is the checkpoint policy's decision (A05, A02) |
| A feed listener fails, panics or is slow | Contained and logged; the deliverer continues. A slow listener delays later deliveries of that session only | The append stands. Nothing is retried |
| A `session.Created` listener returns an error | Publication is vetoed; the writer is closed without a commit; `session.Closed` is notified | The session never existed on disk |
| The fold hook panics | Contained; A06 marks its unit broken | The append stands |
| The host dies mid-turn | Nothing runs. At the next write-open the core appends the closers and an `end-seed` with the new epoch, as one group, before publishing the session | The turn is closed, never continued. **Uncertain:** whether a recorded tool call took effect; the result says so |
| A session is opened read-only with an open turn | The closers are computed and folded in memory; nothing is written | The stored log is unchanged |
| A session's owner fiber unloads | The handle's effect runs `Close`: refuse new appends, let the deliverer reach `Len`, notify `session.Closed`, close the writer | The lease is released by A05 |
| A scope's owner unloads, or `Close` is called | The scope fiber unloads: listeners, registrations and mounted components go, emptied layers are reclaimed, the key stops being alive | A cleanup that never returns blocks the close ([libs/cordis.md](../libs/cordis.md) §7) |
| The scope component unloads | Every scope fiber unloads first; owners' later `Close` calls do nothing | Agents see `Lifetime()` cancelled |
| Fork fails after storage created the child | The child's writer is closed without a commit | No child on disk; the source is untouched |

## 7. Decisions

<a id="dh-adr-session-1"></a>
### DH-ADR-SESSION-1 The log is the only state, and a live session holds folds, not events

- **Decision.** Keep event sourcing. The live session keeps the core folds in memory and reads old events only through a paged, cancellable `Read`. There is no synchronous access to arbitrary history.
- **Why.** Upstream deprecated its three synchronous readers because the whole log cannot stay resident; a new design should not have them.
- **Tradeoff.** Opening needs a checkpoint of the core folds plus point reads of the visible nodes, or a full streaming replay when there is none. A consumer that wants an old event pays a storage read.
- **Serves.** DH-O-4, DH-O-5. **Verified by.** DH-T-SESSION-8, DH-T-SESSION-14.

<a id="dh-adr-session-2"></a>
### DH-ADR-SESSION-2 Three roles: the session contract, a storage provider, a fold hook

- **Decision.** Consumers use `Service` and `Session`. Storage is one contract, owned by A05 and called directly by the session, which relies on the clauses in §6.1. Read models attach through the one `Folder` hook, provided by A06.
- **Why.** Upstream assembles persistence from two events, which is why a group append is not atomic on disk and why its proposed refactor exists. Direct calls give one transaction per group and one owner per duty.
- **Tradeoff.** The session core depends on two services being present; a composition that keeps nothing mounts A05's in-memory store. The core must not trust a provider blindly: a broken clause closes the session instead of folding wrong data.
- **Serves.** DH-O-5. **Verified by.** DH-T-SESSION-4, DH-T-SESSION-19.

<a id="dh-adr-session-3"></a>
### DH-ADR-SESSION-3 Immutability by encoding: the committed payload is an immutable string

- **Decision.** A payload is encoded once at append and held as a Go `string`. Readers receive that string or decode their own struct from it. Folds hold scalars and strings. The table in §6.1 is the complete list of boundaries.
- **Why.** Go cannot freeze, and a rule that depends on discipline over slices and maps fails silently under concurrency. A string is immutable by the language, so sharing it with storage, readers and goroutines needs no copy and no lock. This refines [PERSISTENCE-11](../upstream/session-and-persistence.md#dh-adr-persistence-11) for committed events; ARCH-11 still governs live payloads.
- **Tradeoff.** One encode per append and one decode per reader that wants a typed payload. A provider adapter must convert history nodes itself; node identity (`Seq`, revision) lets it cache the converted form.
- **Serves.** DH-O-4. **Verified by.** DH-T-SESSION-5.

<a id="dh-adr-session-4"></a>
### DH-ADR-SESSION-4 The vocabulary is declared statically and sealed at boot

- **Decision.** Every event type compiled into the binary is declared at program initialisation, whether or not its component is enabled. Compiled-in experimental components declare ignorable types. Plugin processes write only `plugin:` records, under their own owner segment, when granted the capability (enforced by A22).
- **Why.** Go has no declaration merging, so a registry is required. Upstream rejected run-time registration because reads would depend on composition; static declaration keeps its build-time property. It also lets a rewrite function be part of the declaration (ADR-6).
- **Tradeoff.** Process-wide state, written only before the seal. A disabled component's events stay readable, which is intended.
- **Serves.** DH-O-5, DH-O-11. **Verified by.** DH-T-SESSION-1.

<a id="dh-adr-session-5"></a>
### DH-ADR-SESSION-5 One envelope, a payload version per type, hot and cold parts

- **Decision.** The envelope of §6.2. Upstream's surface, sourcing, head-protection and tool-result rules are enforced when planning an append and when opening. Each type has its own payload version. A type may mark one field as the cold part.
- **Why.** A typed envelope gives each fact one spelling. Per-type versions keep a change local ([PERSISTENCE-6](../upstream/session-and-persistence.md#dh-adr-persistence-6)). The cold part answers the foundation's open question on where raw streams live without a second durability owner.
- **Tradeoff.** A reader that wants the stream asks for it explicitly. A change that restructures across events needs a store migration (A05).
- **Serves.** DH-O-4, DH-O-5. **Verified by.** DH-T-SESSION-1, DH-T-SESSION-2, DH-T-SESSION-9.

<a id="dh-adr-session-6"></a>
### DH-ADR-SESSION-6 A message rewrite is a pure function bound to an event type and payload version

- **Decision.** `func(ev event.Event, h surface.HistoryView) ([]surface.Replacement, error)`, declared with its event type. It receives values, has no context and no service, must validate the whole decision before returning, and may change only the content of current nodes: the session checks that message id and role are unchanged. A rewrite type cannot be a surface type. Changing a rewrite's behaviour requires a new payload version, and the old function is kept for old events.
- **Why.** Purity is what makes derived history replayable. Static binding removes upstream's "interpreter missing" and "owner unloaded" states and the need to hand functions to offline readers. Versioning the function with the payload is the rule upstream leaves unstated: otherwise a new binary would derive different history from an old log.
- **Tradeoff.** A rewrite cannot consult live state; everything it needs must be in its event. Old functions accumulate until a recorded decision drops them.
- **Serves.** DH-O-4. **Verified by.** DH-T-SESSION-15.

<a id="dh-adr-session-7"></a>
### DH-ADR-SESSION-7 Only the session can construct a model request, and a settlement records what it answered

- **Decision.** `session.Request` is the one type of a model request, and it has no exported fields or constructor. `NextRequest` builds it from the header in force (route, model, controls, tool schemas), derived history and tool history. `RequestFrom(seq)` builds an auxiliary request (a title, a summary, a review) from one committed event whose type declares a pure builder; there is no "system text" side field, because the builder puts every message into the request. Area A04 owns the seam that receives a request: its runtime and adapters accept `session.Request` and nothing else, and read it through accessors ([llm.md §6.1](llm.md#61-shape)). To keep the import graph acyclic, the vocabulary a request is made of (messages, blocks, sources, tool schema, controls, route id, failure, canonical encoding) lives in A04's leaf package `llm/vocab`, which this area imports; `llm` imports `session`. Each settlement stores `requestAsOf`, the log position the request reflected, and `requestDigest`, a SHA-256 of its canonical encoding. `surface.Rebuild` recomputes any request from a log prefix.
- **Why.** This is [ARCH-10](../upstream/architecture.md#dh-adr-arch-10) made concrete: a side channel is a compile error, and "equals what was sent" becomes a check any replay can run from the log alone.
- **Tradeoff.** No shortcut to add a field to one request. A request with no session (a route check, a "test this key" call) uses an ephemeral session on A05's in-memory store. A04's adapters are pure functions of `Request`, so the digest covers what an adapter encodes.
- **Serves.** DH-O-4. **Verified by.** DH-T-SESSION-12, DH-E-SESSION-1.

<a id="dh-adr-session-8"></a>
### DH-ADR-SESSION-8 The log refuses an unbalanced bracket, and closers are a function of fold state

- **Decision.** The append plan rejects `turn/start` inside a turn or with a number not above the last, `step/start` outside a turn or inside a step, `step/end` while a call of that step has no result, and `turn/end` with an open step. Log-only events outside a turn stay legal. Closers are computed from the bracket fold with upstream's wording, deterministic ids and reused timestamp, and with the two outcome codes area A03 owns, `tools/not-started` and `tools/outcome-unknown` ([tools.md §6.2](tools.md#62-contracts); `surface` holds them as constants and a test in A03 checks they are equal). A write-open appends them itself; a read-open folds them in memory.
- **Why.** [DH-O-3](../design.md#dh-o-3) promises no unanswered call in history. Checking in the log makes that true for every writer, and removes upstream's unrepaired closed steps. Closing on open means no writer can append into a dead turn.
- **Tradeoff.** The core knows turn, step and tool-call semantics. A driver bug shows as a refused append; `CloseOpen` is the path that always succeeds.
- **Serves.** DH-O-3, DH-O-5. **Verified by.** DH-T-SESSION-13, DH-E-SESSION-2.

<a id="dh-adr-session-9"></a>
### DH-ADR-SESSION-9 Three live events; delivery is a tailing reader; flush is a method

- **Decision.** `Created` (ordered, may veto), `Committed` (notify) and `Closed` (notify), all scoped. One deliverer per session reads forward from a cursor outside the append lock. There is no flush event.
- **Why.** An observer of a committed fact must not fail or block the commit ([ARCH-5](../upstream/architecture.md#dh-adr-arch-5)). A cursor needs no bounded queue and no overflow policy. Because listeners run outside the lock, upstream's re-entry ban and `currentBatchPublication` are not needed.
- **Tradeoff.** A listener sees an event after `Append` returned, and `Len()` may already be ahead of it. Delivery depends on storage serving the unflushed tail.
- **Serves.** DH-O-3, DH-O-5. **Verified by.** DH-T-SESSION-3, DH-T-SESSION-6, DH-T-SESSION-7.

<a id="dh-adr-session-10"></a>
### DH-ADR-SESSION-10 A scope is a fiber for lifetime and a key for visibility, with no inherited service reach

- **Decision.** As §6.1: an opaque key from a table; a tagged registration context on a fiber that hangs under a component injecting nothing and is owned by its creator; an ordered list of enclosing scopes that only a holder of `*Scope` can change; scoped events that need a key to dispatch; a `Layered` or `Shared` declaration per registry service.
- **Why.** It applies [ARCH-7](../upstream/architecture.md#dh-adr-arch-7) and removes the three upstream limits (§5). Inference, from reading `src/go/cordis/store.go:140-171` and not from running it: service resolution walks up parent fibers and stops at the root, so a fiber whose ancestors inject nothing resolves nothing by inheritance, and a component mounted under it resolves exactly what it declares.
- **Tradeoff.** It refines ARCH-7, which hangs the agent's fiber under the loop component: the driver (A02) is an agent scope's owner, never its parent, and holds the `*Scope` ([agent-loop.md §6.3](agent-loop.md#63-ownership-failure-retry-rollback-and-cleanup)). Code holding a scope context cannot fetch a service through it and must use its own; a per-preset service is reached through a `Layered` registration or A27's `Lookup`. The list of enclosing scopes is more than upstream's single parent; with one element it is the same chain.
- **Serves.** DH-O-1, DH-O-9. **Verified by.** DH-T-SESSION-17, DH-T-SESSION-18.

<a id="dh-adr-session-11"></a>
### DH-ADR-SESSION-11 Fork reads from storage, at any existing event, and records its origin apart from the creator

- **Decision.** **Recommended default, not confirmed by the owner:** a fork records `ForkSource` and `ForkThrough`, which reverses an upstream decision; if the owner overturns it, the two header fields and A05's two columns are dropped and nothing else changes, because no behaviour reads them. `Fork` names its source by id. A live source is flushed first; storage then creates the child from the stored prefix. The core appends the cut marker and, for an open tail, closers with cause `forked`, as the child's first own events. The child header records `ForkSource` and `ForkThrough`. `Creator` and `Origin` are set only for subagents. No behaviour may depend on `ForkSource`: it is lineage for display, and its target may since have been deleted.
- **Why.** Reading from storage removes the live-source limit. Recording the origin cannot be done later, and with separate fields it no longer makes a fork look like a subagent, the defect that led upstream to drop it.
- **Tradeoff.** It reverses part of an implemented upstream decision. Consumers must not order, hide or notify by `ForkSource`.
- **Serves.** DH-O-5, DH-O-9. **Verified by.** DH-T-SESSION-16.

<a id="dh-adr-session-12"></a>
### DH-ADR-SESSION-12 A log-only change reaches a session wherever it is held

- **Decision.** `Service.AppendLogOnly(ctx, id, drafts)` is the one way to record a fact the model never sees on a session the caller does not drive. It accepts only drafts of log-only types whose declaration says `Detached` (feedback, a title rename, a translation, an upload consent); a bracket, surface or undeclared type is refused with `session/not-detachable`. It has three paths and reports which one it took. (1) The session is live in this process: an ordinary append; outcome `applied{seq}`. (2) Nobody holds it: a *maintenance append*. The core takes the lease with `OpenWrite(IfFree)`, closes an interrupted turn as every write-open does (ADR-8), appends the drafts as one group, flushes and releases the lease. No agent is created, `session.Created` is not dispatched and no preset is composed, so nothing model-visible can be added. (3) Another process holds it. Only a type whose declaration also says `Queueable` may be handed over: its drafts are filed as one row in A05's request table ([DH-ADR-STORE-12](session-store.md#dh-adr-store-12)); outcome `queued{request}`. The holder's core applies a pending request as its own group append and marks the row applied in the same transaction, so a request is applied at most once. A `Detached` type that is not `Queueable` gets outcome `held-elsewhere{holder}` (error `session/held-elsewhere`): nothing is appended and nothing is filed. **The rule, stated here once:** a change that is complete in its drafts (feedback, a title rename, an upload consent) is `Queueable`; a change whose second event depends on work done after the first is committed is not. A translation is the case: its request event must be committed before the provider is called and its result appended after, so it is written on a live or a free session only, and on a session another process holds it is refused with `held-elsewhere`; [x-recommendations-misc.md](x-recommendations-misc.md) answers `translate/session-held-elsewhere` on that outcome and makes no provider call.
- **Why.** The owner's resolution of register entry U-K1-5 ([DH-ADR-40](../design.md#dh-adr-40)), and the recommended redesign of U-K2-13: with several processes on one home ([DH-ADR-12](../design.md#dh-adr-12)), a thumbs-up must not be refused because the session runs in another `dsh`, and no change may "activate" an idle session to be written. A request row carries finished drafts only, because the holder applies it without calling back into the requester; a two-phase change cannot be handed over that way.
- **Tradeoff.** A queued change is not immediate and can expire unapplied (the row then says `expired` and the caller is told). A maintenance append adds a `session/end-seed` for its epoch, as any write-open does. The path is limited to log-only types at first; a prompt is not a request.
- **Failure and cleanup.** The holder dies between reading a request and appending it: the row is still `pending`, because the mark commits with the events or not at all; the next holder, or the requester's next call once the lease is free, applies it. The requester dies: the row is applied or expires; nothing waits for it. A draft that fails validation in the holder marks the row `refused` with the error.
- **Serves.** DH-O-5, DH-O-12. **Verified by.** DH-T-SESSION-20, `DH-T-STORE-17`.

## 8. Deliverables

<a id="dh-d-session-1"></a>
### DH-D-SESSION-1 A typed, versioned event vocabulary that every reader interprets the same way

- **Result and why it matters.** One declaration per event type gives typed appends, typed reads, old-version conversion, the catalog entry and the fingerprint input. A log written by another build is either read faithfully or refused by name.
- **How it is implemented.** Generic `Type[T]` and `SurfaceType[T]` handles whose `Draft` methods differ, so a surface event cannot be drafted without an operation, nor a log-only one with it. A registry sealed at boot. Encoding by `encoding/json` with a canonical field order; the `schema` package supplies the fingerprint for A05's inventory file. The 14 core payload structs and the plugin-record grammar.
- **Components and files.** `session/event`.
- **Depends on.** [DH-D-LLM-11](llm.md#dh-d-llm-11): the leaf vocabulary package `llm/vocab`, which imports nothing from `internal/session`. [DH-D-UTIL-1](util-test-support.md#dh-d-util-1): `schema`. Runtime: none.
- **Priority.** P0. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-4](../design.md#dh-o-4), [DH-O-11](../design.md#dh-o-11).
- **Done when.** DH-T-SESSION-1 and DH-T-SESSION-2 pass and DH-E-SESSION-1, which exercises the vocabulary end to end, has passed.

<a id="dh-d-session-2"></a>
### DH-D-SESSION-2 A live session that accepts events in order, delivers them once and holds no history

- **Result and why it matters.** Appends from many goroutines get dense sequence numbers, a group is all or nothing in memory and on disk, observers see every event once in order, and memory does not grow with the log.
- **How it is implemented.** The append path and deliverer of §6.1; `Prepare`, `Publish`, `Open`, `OpenRead`, `Close`; the lifecycle events; a check at each storage call that the clause the core relies on held (the expected first `seq`, no gap, no short tail), closing the session with a classified error when it did not.
- **Components and files.** `session`, `session/core`.
- **Depends on.** DH-D-SESSION-1, DH-D-SESSION-8, [DH-D-CORDIS-2](../libs/cordis.md#dh-d-cordis-2), [DH-D-CORDIS-4](../libs/cordis.md#dh-d-cordis-4), and [DH-D-STORE-1](session-store.md#dh-d-store-1): the storage contract and its in-memory provider, which the local tests use. **Runtime prerequisite** (not a build dependency): a storage service and a folder service ([DH-D-VIEW-1](session-views.md#dh-d-view-1)) in the composition.
- **Priority.** P0. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-3](../design.md#dh-o-3).
- **Done when.** DH-T-SESSION-3 to DH-T-SESSION-8 and DH-T-SESSION-19 pass under the race detector and DH-E-SESSION-2 (a real restart) has passed.

<a id="dh-d-session-3"></a>
### DH-D-SESSION-3 Model history, the request header and the tool history are computed from the log

- **Result and why it matters.** What the model is shown is one deterministic function of the events, live, after a restart and offline.
- **How it is implemented.** Pure plan-then-apply functions in `surface` carrying every upstream rule in §3.2; history nodes with stable identity and copy-on-replace arrays; header and tool-history folds; checkpoint and restore of each fold, the checkpoint holding structure and not message text.
- **Components and files.** `session/surface`.
- **Depends on.** DH-D-SESSION-1; [DH-D-LLM-11](llm.md#dh-d-llm-11) for message and schema types with a canonical encoding.
- **Priority.** P0. **Outcome.** [DH-O-4](../design.md#dh-o-4).
- **Done when.** DH-T-SESSION-9, DH-T-SESSION-10, DH-T-SESSION-11 and DH-T-SESSION-14 pass and DH-E-SESSION-1, which confirms the derived history against a real provider, has passed.

<a id="dh-d-session-4"></a>
### DH-D-SESSION-4 A model request can only come from the log and can be rebuilt from it

- **Result and why it matters.** Nothing reaches a model that the log does not hold, and anyone with the log can recompute what was sent.
- **How it is implemented.** The opaque `Request`; `NextRequest`, `RequestFrom` and builder declarations; `requestAsOf` and `requestDigest` on settlements; `surface.Rebuild` over any reader of a log prefix.
- **Components and files.** `session`, `session/surface`.
- **Depends on.** DH-D-SESSION-3. **Used by.** The LLM runtime, which accepts only `session.Request` ([DH-D-LLM-1](llm.md#dh-d-llm-1)); the driver, which writes the settlement fields ([DH-D-AGENT-3](agent-loop.md#dh-d-agent-3)); and the builders for title and summary requests ([DH-D-VIEW-4](session-views.md#dh-d-view-4), [DH-D-CONTEXT-5](context-compaction-skill.md#dh-d-context-5)).
- **Priority.** P0. **Outcome.** [DH-O-4](../design.md#dh-o-4).
- **Done when.** DH-T-SESSION-12 passes and DH-E-SESSION-1 has passed against a real model API.

<a id="dh-d-session-5"></a>
### DH-D-SESSION-5 Brackets always balance: checked on append, closed on failure, crash and fork

- **Result and why it matters.** A history with an unanswered tool call cannot be written, and an interrupted turn is closed honestly before anyone appends again.
- **How it is implemented.** The bracket fold and plan checks of ADR-8; `Closers` as a pure function; `CloseOpen`; automatic closers on write-open and in-memory closers on read-open; `OpenReport` telling the caller what was closed.
- **Components and files.** `session/surface`, `session/core`.
- **Depends on.** DH-D-SESSION-2, DH-D-SESSION-3. **Used by.** The driver, which calls `CloseOpen` on every failure path ([DH-D-AGENT-3](agent-loop.md#dh-d-agent-3)) and reports `OpenReport` to the user ([DH-D-AGENT-6](agent-loop.md#dh-d-agent-6)).
- **Priority.** P0. **Outcome.** [DH-O-3](../design.md#dh-o-3), [DH-O-5](../design.md#dh-o-5).
- **Done when.** DH-T-SESSION-13 passes and DH-E-SESSION-2 has passed with a real process kill.

<a id="dh-d-session-6"></a>
### DH-D-SESSION-6 Features can change what the model is shown through pure, versioned message rewrites

- **Result and why it matters.** Image offload and later features alter derived content durably without code in the core and without a state in which the function is missing.
- **How it is implemented.** ADR-6: rewrite functions in the type declaration, applied in the plan, results stored as new node revisions, the identity check, and the same table used by `Rebuild`.
- **Components and files.** `session/event`, `session/surface`.
- **Depends on.** DH-D-SESSION-3. **Used by.** The first rewrite, image offload, [DH-D-CONTEXT-6](context-compaction-skill.md#dh-d-context-6).
- **Priority.** P1: no first-turn path needs it. **Outcome.** [DH-O-4](../design.md#dh-o-4).
- **Done when.** DH-T-SESSION-15 passes.

<a id="dh-d-session-7"></a>
### DH-D-SESSION-7 Any stored session can be forked at any event, with its lineage recorded

- **Result and why it matters.** Branching does not need the source to be loaded, and branches can later be shown as a tree.
- **How it is implemented.** ADR-11 over the store's create-from-prefix call; boundary validation; header lineage; the cut marker and `forked` closers.
- **Components and files.** `session/core`.
- **Depends on.** DH-D-SESSION-2, DH-D-SESSION-5; [DH-D-STORE-2](session-store.md#dh-d-store-2), which creates a child from a stored prefix by copy. **Used by.** The tree view, `Query.Lineage` ([DH-D-VIEW-5](session-views.md#dh-d-view-5)).
- **Priority.** P1. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-9](../design.md#dh-o-9).
- **Done when.** DH-T-SESSION-16 passes.

<a id="dh-d-session-8"></a>
### DH-D-SESSION-8 Per-agent and per-preset registrations with one owner and no borrowed authority

- **Result and why it matters.** A tool registered for one agent is invisible to another and gone when the agent is; a preset's contributions reach exactly its agents; holding a scope grants no service.
- **How it is implemented.** ADR-10: the table with linearised enclosing lists and cycle checks; `Layers`, `Named` and `List` with their own lock and snapshot reads; `scope.Event` over the filtered dispatch; the visibility declarations and `CheckMount`.
- **Components and files.** `session/scope`.
- **Depends on.** [DH-D-CORDIS-3](../libs/cordis.md#dh-d-cordis-3), [DH-D-CORDIS-2](../libs/cordis.md#dh-d-cordis-2). Runtime: the scope component is a top-level entry of the base bundle, so no ancestor fiber injects a service ([boot.md §6.2](boot.md#62-contracts); `DH-T-BOOT-2` fails otherwise). **Used by.** The driver and the preset registry, which create scopes and call `CheckMount` ([DH-D-AGENT-6](agent-loop.md#dh-d-agent-6), [DH-D-BUNDLE-4](bundle-preset.md#dh-d-bundle-4)), and by the tool and prompt registries built on `Layers` ([DH-D-TOOLS-1](tools.md#dh-d-tools-1)).
- **Priority.** P0. **Outcome.** [DH-O-1](../design.md#dh-o-1), [DH-O-9](../design.md#dh-o-9).
- **Done when.** DH-T-SESSION-17, DH-T-SESSION-18 and the scope clauses of DH-T-SESSION-6 pass.

<a id="dh-d-session-9"></a>
### DH-D-SESSION-9 A log-only change can be recorded on a session this process does not drive

- **Result and why it matters.** Feedback or a rename succeeds whether the session is idle, live here or running in another `dsh` process; a translation succeeds on a live or an idle session and, on a session another process holds, is refused with the named outcome `held-elsewhere`. None of them wakes an agent. This closes register entry U-K1-5, and U-K2-13 for every session no other process holds.
- **How it is implemented.** `AppendLogOnly` as in ADR-12: the `Detached` and `Queueable` flags on a type declaration; the maintenance append on a free session; a consumer in the session core that, for each session it holds, applies pending rows of the request table when the store signals a change, and marks them in the batch's transaction.
- **Components and files.** `session/core`, `session/event`.
- **Depends on.** DH-D-SESSION-2, DH-D-SESSION-5; [DH-D-STORE-11](session-store.md#dh-d-store-11) for the request table. **Used by.** First, feedback ([DH-D-HUMAN-10](interaction-credentials.md#dh-d-human-10)).
- **Priority.** P1. **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-12](../design.md#dh-o-12).
- **Done when.** DH-T-SESSION-20 passes and `DH-T-STORE-17` passes with two real processes.

## 9. Acceptance

### 9.1 Local tests

All are `go test` with the race detector; storage is A05's in-memory provider unless stated. None exists yet.

| Test ID | Proves | What is asserted |
|---|---|---|
| DH-T-SESSION-1 | DH-D-SESSION-1 | A duplicate or late declaration is refused. Every declared type encodes, decodes and re-encodes to the same bytes. An older payload version converts. An undeclared required type refuses the open by name; an undeclared ignorable one survives read, fork and export byte for byte. A plugin record outside the grammar is refused |
| DH-T-SESSION-2 | DH-D-SESSION-1 | Each invalid envelope (no operation on a surface type, an operation on a log-only type, sources on `assistant/message`, duplicate or forward sources, a reserved type from outside the core) is refused, and `Len`, `History` and the feed are unchanged |
| DH-T-SESSION-3 | DH-D-SESSION-2 | Concurrent appenders get dense `Seq` and non-decreasing `Time`; every listener sees each event once, in order |
| DH-T-SESSION-4 | DH-D-SESSION-2 | A group with one invalid member changes nothing. A valid group reaches storage as one batch; with a fault injected in the store either all or none is readable |
| DH-T-SESSION-5 | DH-D-SESSION-2 | Changing a payload after `Append`, a decoded payload, a `Sources()` result or a decoded history message changes no later read. The type-information test finds no exported function returning internal slices, maps or pointers outside the reviewed list |
| DH-T-SESSION-6 | DH-D-SESSION-2, DH-D-SESSION-8 | A failing, panicking or blocked listener does not fail or delay `Append`. A listener may append. A listener in agent A's scope does not see agent B's session; one in an enclosing scope and a global one do |
| DH-T-SESSION-7 | DH-D-SESSION-2 | A `Created` error vetoes publication and `Closed` follows. `Close` delivers every event before `Closed`. An append after close returns `session/closed`. A lost lease closes the session with that cause |
| DH-T-SESSION-8 | DH-D-SESSION-2 | `Read` returns at most `limit` events, includes the unflushed tail, and cold parts only when asked. After 100,000 log-only appends the session's heap stays under a fixed bound |
| DH-T-SESSION-9 | DH-D-SESSION-3 | For generated valid logs the incremental fold equals a fold from storage. Each replacement rule accepts and rejects as in §3.2. A fuzz target feeds arbitrary envelopes to the planner: no panic, and no state change on error |
| DH-T-SESSION-10 | DH-D-SESSION-3 | Golden files: a log gives the expected history JSON. Empty system, developer and assistant messages produce no node. A snapshot is unchanged by later appends and replacements; unchanged nodes keep their identity |
| DH-T-SESSION-11 | DH-D-SESSION-3 | The header fold returns the latest snapshot. The tool history resolves an addition through the header it cites, restarts on a series start or a changed definition, and continues when a removed name returns unchanged |
| DH-T-SESSION-12 | DH-D-SESSION-4 | For every settlement in recorded Go-format fixtures, `Rebuild` at `requestAsOf` has the digest stored in `requestDigest`. A package outside `session` cannot construct a `Request`. An auxiliary request exists only for a committed event |
| DH-T-SESSION-13 | DH-D-SESSION-5 | A table of interruption points gives the exact closers: codes, order, ids, times. `step/end` with an unanswered call is refused. A write-open appends closers once; a second open appends none. A read-open shows them and writes nothing |
| DH-T-SESSION-14 | DH-D-SESSION-3 | For each core fold, checkpoint plus suffix replay equals full replay; a checkpoint of another version is discarded |
| DH-T-SESSION-15 | DH-D-SESSION-6 | The same log gives the same history bytes, folded whole or in windows. A rewrite that changes an id or role, or targets a node not on the surface, is refused with no change. An event of payload version 1 uses the version 1 function. A surface type cannot declare a rewrite |
| DH-T-SESSION-16 | DH-D-SESSION-7 | Fork of a live and of a stored-only source at the last and at an earlier event: inherited count, cut marker, `forked` closers, lineage fields, empty `Creator`, plugin records carried. A later append to either does not appear in the other. A boundary past the end is refused |
| DH-T-SESSION-17 | DH-D-SESSION-8 | An entry registered through a scope is visible there and in scopes that list it as enclosing, nowhere else, and gone after `Close`. `Close` is idempotent under concurrent callers and the layer is reclaimed. The nearest scope wins a name; two unrelated enclosing scopes apply in the given order; a cycle is refused; a `Key` alone cannot change an enclosing list |
| DH-T-SESSION-18 | DH-D-SESSION-8 | A composition test through the real loader: resolving any spine service through `Scope.Ctx()` returns `ErrNotInjected`; a component mounted under it resolves exactly its declared services; `CheckMount` reports a `Shared` registry |
| DH-T-SESSION-19 | DH-D-SESSION-2 | With a fake store that breaks one clause at a time (a gap, a stale tail, a wrong first `seq`, a record of another session), the session closes with a classified error and neither folds nor delivers the bad data |
| DH-T-SESSION-20 | DH-D-SESSION-9 | `AppendLogOnly` on a live session appends once; on a free session it takes and releases the lease, dispatches no `session.Created`, creates no agent and leaves model history unchanged; a surface, bracket or non-`Detached` draft is refused; with a fake store that reports "held elsewhere" a `Queueable` draft is filed as one request and returns `queued`, while a `Detached` draft that is not `Queueable` returns `held-elsewhere`, files nothing and appends nothing; the holder applies a pending request once, also when it is restarted between read and append |

### 9.2 Real-environment scenarios

Scope and the fold logic need no real environment: they are in-process and deterministic. Two behaviours do.

<a id="dh-e-session-1"></a>
#### DH-E-SESSION-1 Requests received by a real model API equal requests rebuilt from the log

- **Proves.** DH-D-SESSION-4, DH-D-SESSION-3, DH-D-SESSION-1.
- **Given.** The built `dsh` binary. A new temporary harness home. A real provider account for one of the four protocol drivers, its key supplied at run time through the environment and never written to disk. A loopback forwarding proxy, set as that provider's base address, which relays to the real API and records each request body. A workspace holding one text file. Areas A04, A05 and A24 delivered.
- **When.** (1) Start the host in the headless profile. (2) Send a prompt that makes the model read the file with a tool and answer. (3) After the turn ends, send a second prompt. (4) Export the session. (5) In the test process, for each settlement in the export, rebuild the request at `requestAsOf` and encode it with the same adapter.
- **Then.** The proxy recorded as many requests as the log has settlements. Each rebuilt body equals the recorded body byte for byte. Each recomputed digest equals `requestDigest`. If the provider failed an attempt, that attempt has an `assistant/attempt` settlement and its request still matches. Any recorded request with no settlement fails the scenario.
- **Cleanup.** Stop the host and the proxy; delete the temporary home and the recordings. If a comparison failed, keep the export, the recordings and the host log.

<a id="dh-e-session-2"></a>
#### DH-E-SESSION-2 A host killed during a tool call resumes into a history a real provider accepts

- **Proves.** DH-D-SESSION-5, DH-D-SESSION-2.
- **Given.** The built binary, a new temporary home on A05's real store, a real provider key as above, a workspace in `workspace-write` mode, and a shell tool (A09). Areas A02, A05 and A09 delivered.
- **When.** (1) Prompt the model to run a command that sleeps for two minutes. (2) Wait until the `tool/call` event is durable, observed through the event stream. (3) Export the session, then kill the host process without a graceful stop. (4) Start a new host on the same home and resume the session. (5) Export again. (6) Send a prompt asking what happened.
- **Then.** The second export begins with the first, byte for byte. It then holds, in order, one `tool/result` with code `tools/outcome-unknown` citing the `tool/call`, `step/end`, `turn/end` with reason `interrupted`, and `session/end-seed` with a higher epoch, and nothing else. Resuming a second time adds no closer. The provider answers step 6 with success and an `assistant/message` is appended, which shows the history has no unanswered call. Whether the sleeping process survived is area A09's assertion, not this scenario's.
- **Cleanup.** Stop the host; end any leftover sleeping process by the recorded process id; delete the temporary home. If an assertion failed, keep both exports and the logs of both hosts.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

Open:

1. **Recommended default, not confirmed by the owner.** ADR-11 records a fork's origin, which upstream removed on purpose. If overturned, the two header fields are dropped and the branch tree stays impossible.
2. **Every area with a registry** must declare it `Layered` or `Shared` (ADR-10). No document lists the declarations yet; `CheckMount` is only as good as that list, and the implementation plan needs a step that collects it.

Settled with other areas, for the record: the storage contract is A05's alone (§6.1); A04's runtime and adapters accept `session.Request` (ADR-7); A06's registry is the `Folder` and the core folds stay in the core; the driver owns an agent's scope and is not its parent (ADR-10), and tears down in the order of [agent-loop.md §6.3](agent-loop.md#63-ownership-failure-retry-rollback-and-cleanup); the scope component is a top-level bundle entry ([boot.md](boot.md)); a plugin process writes `plugin:` records only under the `session.records.write` capability, with the owner segment set by the host ([extensions.md](extensions.md)); A27 decides when an agent's enclosing list may change.

Limitations:

- The scope design rests on a reading of `src/go/cordis/store.go` and on cordis features that are specified but not built ([DH-D-CORDIS-2](../libs/cordis.md#dh-d-cordis-2), [-3](../libs/cordis.md#dh-d-cordis-3), [-4](../libs/cordis.md#dh-d-cordis-4)). `Lookup`, the port's unchecked resolution, ignores injection by design and is not restricted here.
- A scope separates trusted compiled-in components. It is not a security boundary; plugin processes are confined by their grants ([DH-ADR-4](../design.md#dh-adr-4)).
- Any compiled-in component with a `Session` can append any declared event, including surface events. Only bracket, surface and reserved-type rules are enforced.
- An event delivered on the feed may not be durable yet. A hard process loss before a settlement still loses that attempt's stream, as upstream.
- Size limits, page size and checkpoint interval are unmeasured proposals. The cost of one decode per typed read is unmeasured.
- `requestDigest` proves the `Request` value, not the bytes on the wire; the wire is covered by A04's tests and by DH-E-SESSION-1.

## 11. Units dispositioned by this spec

Ledger rows are in `trace/fragments/A01.jsonl`.

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 2 | ported 2 |
| `UP-DEBT` | 10 | resolved 10 |
| `UP-EVT` | 4 | ported 3, replaced 1 (`session/flush`) |
| `UP-SVC` | 1 | ported 1 (`ctx.sessions` becomes `session.Key`) |
| `UP-DEC` (handed over) | 19 | ported 14, replaced 5 (immutability by freezing, message projections, the rejected schema registry, the attribution policy, independent forks) |
| Go items declared | 43 | 12 decisions, 9 deliverables, 20 tests, 2 scenarios |
