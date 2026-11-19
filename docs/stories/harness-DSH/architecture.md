# harness:DSH -- Architecture (milestone M1 of the `dsh` harness)

> Story: "clone deepseek harness, given context in docs\harness of this repo, and design
> and impl doc, i want you to follow impl" (13 points).

This document is the architecture of the first slice of `dsh`, the Go re-implementation
("clone") of the DeepSeek agent harness that [docs/harness](../../harness/README.md)
specifies. It follows the implementation plan [docs/harness/impl.md](../../harness/impl.md)
literally: same stage IDs (`DH-S-n`), same deliverable IDs (`DH-D-...`), same module and
package layout as [docs/harness/design.md](../../harness/design.md) sec. 4.2. It does
not re-decide anything those documents settle; it pins which components this story
builds, how they talk, what data they own, how the primary flows run end to end, and
how each acceptance area is proven on a gate host that has no docker and no services.

Sibling plan documents (written in parallel; this file does not duplicate them):
[tech-spec.md](tech-spec.md) (field-level contracts, error codes, settings),
[implementation-plan.md](implementation-plan.md) (ordered work items and commits),
[e2e-scenarios.md](e2e-scenarios.md) (scenario scripts). All three MUST honor the
[Test & Environment Contract](#test--environment-contract) below.

---

## 1. Scope

### 1.1 What "follow impl" means for this story

impl.md orders the whole product into stages; milestone **M1** ("an agent runs turns
against a real model with one tool, every step logged") is reached when **DH-S-11** is
built (impl.md sec. 3.3). This story is that milestone: the eleven build stages
**DH-S-1 .. DH-S-11** (about 106 checklist steps), in impl.md order, with each stage's
entry conditions respected. Per impl.md sec. 1.4 a build stage closes as **built**,
not **done**. Its `DH-E-*` scenarios are **authored here and executed at the stage
impl.md assigns to each one; none is executed in this story** (the schedule is in the
Test & Environment Contract, rule 2). Until guarded acceptance step 16.8 passes, the
SQLite store built here is used on **scratch homes only**.

### 1.2 Stages and deliverables in scope

| Stage | Result (impl.md) | Deliverables |
|---|---|---|
| DH-S-1 | Three modules, quality gates, CI, release rehearsal | DH-D-TOOLCHAIN-1..5 |
| DH-S-2 | cordis changes in `src/go` | DH-D-CORDIS-1,2,3,4,5,7,8,9 |
| DH-S-3 | loader changes in `src/go` | DH-D-LOADER-1,3,4,6,7,8,10,11,13 |
| DH-S-4 | SDK codec, schema and leaf packages | DH-D-SDK-1,2; DH-D-UTIL-1,3,4; DH-D-LLM-11; DH-D-FS-1,2,3; DH-D-LOADER-2,5,12; DH-D-BOOT-1,4 |
| DH-S-5 | Session core contracts (in memory) | DH-D-SESSION-1..5,8; DH-D-STORE-1,5; DH-D-LLM-1; DH-D-AGENT-1,4; DH-D-HUMAN-1; DH-D-VIEW-1 |
| DH-S-6 | Boot, launch, egress, secrets, test kit (17 deliverables, one dependency circle C-1) | DH-D-UTIL-2,5,6; DH-D-EXEC-1,2; DH-D-BOOT-2,3,5,6,7; DH-D-STORAGE-2,8; DH-D-HUMAN-7; DH-D-CLI-1,3; DH-D-HOST-4,7 |
| DH-S-7 | SQLite store, leases, recovery | DH-D-STORE-2,3,4,6,7,8 |
| DH-S-8 | Machine state, views, spill | DH-D-STORAGE-1,3,5,7; DH-D-VIEW-2,3,5 |
| DH-S-9 | First model provider (DeepSeek), retry, usage | DH-D-LLM-2,3,6 |
| DH-S-10 | Tool pipeline, approvals, human commands | DH-D-TOOLS-1,2,3,5; DH-D-STORAGE-6; DH-D-AGENT-2; DH-D-HUMAN-2,3,5,6 |
| DH-S-11 | Agent loop (M1 reached) | DH-D-AGENT-3,5,6,7,10; DH-D-SUBAGENT-1; DH-D-CONTEXT-1 |

### 1.3 Out of scope

- Every stage from DH-S-12 on: working directory and file tools, launchers, the SDK
  server (`DH-S-15`, the first live transport of the `jsonrpc` codec), the admin tree
  and replay runner (`DH-S-16`), the web UI, MCP, sandboxing.
- Executing any `DH-E-*` scenario. They are authored only; each runs at the stage
  impl.md schedules (contract rule 2).
- Held-open items: H-21 (`dsh config` commands of DH-D-BOOT-6 wait for DH-S-16);
  titles (DH-D-VIEW-4 moved to DH-S-16); the OS keychain (`DH-S-35`).
- No change to the .NET port under `src/net`.

---

## 2. Starting point in the repository

| Path (today) | What exists | What this story does to it |
|---|---|---|
| `src/go/go.mod` | Module `github.com/smartpcr/agent-plugin/src/go`, `go 1.24`, standard library only | Raise `go` line to `1.26.0` (step 1.1); stays standard library only |
| `src/go/cordis` | The cordis port: `Context` (`New`, `Use`, `Apply`, `Inject`, `Provide`, `Effect`, `EffectAsync`, `EffectIter`, `Isolate`, `Intercept`, `Get`/`Set`/`Lookup`, `OnUpdate`), `Fiber` (`State`, `Restart`, `Update`, `Dispose`, `Wait`, `Err`), `Component` (`Define`, `Construct`, `WithInject`, `WithSchema`, `WithDefaults`), `EventKey` (`On`/`Emit`/`Parallel`), `BailKey`, `WaterfallKey` | Eight DH-D-CORDIS changes (sec. 5.1) |
| `src/go/loader` | Entry/config loader for cordis trees | Nine DH-D-LOADER changes (sec. 5.2) |
| `src/go/timer` | Timer service | Unchanged; used through the test kit's virtual clock |
| `src/go/internal/testkit` | Port-local test helpers | Unchanged (the harness test kit is new, `src/harness/internal/testkit`) |
| `src/go/cmd/cordis-sample` | The port's example program | Its output is the golden baseline of TC-02b |
| `tools/harness-trace` | Traceability checker (`verify`) | Becomes one gate of `tools/harness-gate` |
| `.github/workflows/ci.yml`, `release.yml`, `.goreleaser.yaml` | Existing CI / release | Add the keyless `harness` job (3 OS) and the release rehearsal |
| `src/sdk`, `src/harness`, `go.work`, `tools/harness-gate` | Do not exist | Created in DH-S-1 |
| `src/net` | .NET port | Untouched |

---

## 3. Components

### 3.1 Module and package map

```
 repo root
 |-- go.work                     (dev only; ignored by the release build)
 |-- tools/harness-gate          C01  gate runner (fixed sequential gate list)
 |-- tools/harness-trace         (existing) traceability gate
 |
 |-- src/go        module 1: stdlib only
 |     |-- cordis                C02  component tree, fibers, events, config
 |     |-- loader                C03  layered entries, diagnostics, write-back
 |     `-- timer                 (unchanged)
 |
 |-- src/sdk       module 2: stdlib only, imports neither src/go nor src/harness
 |     |-- jsonrpc               C04  bounded line-delimited JSON-RPC codec
 |     `-- protocol              C05  SDK types, method table, embedded schema
 |
 `-- src/harness   module 3: the product, imports src/go + src/sdk (no replace)
       |-- cmd/dsh               C16  main only
       `-- internal/
             |-- schema          C06  derive / validate / fingerprint shapes
             |-- llm/vocab       C07  Message, Block, tool schema, Controls (leaf; no request type)
             |-- util/...        C08  herr, ident, homedir, mediatype, watch,
             |                        fsx, search, textdiff
             |-- util/egress     C19  outbound policy + permits
             |-- boot/...        C08/C15 catalog, yamlx, expr, launch, profile,
             |                        config, reload
             |-- cli/...         C16  command tree, output contract
             |-- session/...     C09 C10 C11 C21  log, registry, folds, stores
             |-- storage         C22  typed tables, spill, attachments
             |-- llm             C12 C23 C24  runtime, deepseek provider, retry, usage
             |-- tools           C25  registry, pipeline, scheduler, prompt assembly
             |-- human           C14 C18 C26  interactions, secrets, approvals, grants
             |-- agent/...       C13 C27 C28  contract, driver, inbox
             |-- delegate        C29  delegation seam (one owner per child)
             |-- context/request C30  logged ordered context pipeline
             |-- world/subprocess C17 the one place a process is started
             |-- host            C15  fatal path, egress off-switch
             `-- testkit         C20  real components vs scripted boundaries
```

Import rules (enforced by import-direction tests and the gate runner, design.md
sec. 4.2): `src/go` and `src/sdk` never import each other or `src/harness`; inside
`src/harness` every implementation package is under `internal/`; leaf packages (C06,
C07, C08) import no area; `internal/llm/vocab` is imported by `session`, and `llm`
imports `session`, so there is no cycle; a consumer never imports a provider
sub-package (ARCH-6). Sub-package names below the family level are the areas' own
and are indicative here; the area specification owns the final name.

### 3.2 Host process (what one `dsh` process composes at M1)

```
 +----------------------------- dsh process -----------------------------------+
 | cmd/dsh -> C16 cli tree -> C15 boot: launch facts, home, profile, layers    |
 |                                  |                                          |
 |                                  v  cordis root Context (C02), loader (C03) |
 |  +------------------------- composed component tree ---------------------+  |
 |  | logging  storage(C22)  store/sqlite(C21)  session(C09,C11)            |  |
 |  | secrets(C18)  egress(C19)  llm runtime(C12) + deepseek provider(C23)  |  |
 |  | tools(C25)  human: interactions(C14) approvals/grants(C26)            |  |
 |  | agent registry(C13) -> agent scope fiber -> driver(C27) + inbox(C28)  |  |
 |  |                                   context pipeline(C30)  delegate(C29)|  |
 |  +-----------------------------------------------------------------------+  |
 |        |  SQL (WAL)              | HTTPS (permitted)        | spawn          |
 +--------|-------------------------|--------------------------|---------------+
          v                         v                          v
   <home>/dsh.db, run/*.lock   api.deepseek.com          child process tree
   spill/, blobs/, config/     (Anthropic Messages wire)  (C17, stopped as a tree)
```

At M1 no carrier (web, SDK server, TUI) is composed; tests and the manual M1 check
drive the tree from Go through the test kit (C20) and the agent contract (C13).

### 3.3 Component table

| ID | Component | Package | Responsibility | Stage | Spec |
|---|---|---|---|---|---|
| C01 | Gate runner | `tools/harness-gate` | Runs a fixed sequential list of gates (format, vet + own analyzers, race + shuffle tests, coverage floor 85% per package merged across processes, trace verify, perf budgets, cross-compile); names the failing gate; no baseline file | S-1 | upstream/toolchain.md |
| C02 | cordis | `src/go/cordis` | Component tree; final unload; per-fiber `context.Context`; context values + scoped dispatch; `Notify`; `AroundKey`; raw/effective/candidate config; strict decode; tree log exporters | S-2 | libs/cordis.md |
| C03 | loader | `src/go/loader` | Codec seam with positions; entry schema; expression seam; layer composition + ownership index; write-back to one layer; nested loader; restart one entry; settled-reload notice; write guard | S-3 | libs/cordis-loader.md, libs/cordis-plugins.md |
| C04 | JSON-RPC codec | `src/sdk/jsonrpc` | Bounded line framing, one `<domain>/<reason>` error table | S-4 | areas/sdk-acp-typert.md |
| C05 | SDK protocol | `src/sdk/protocol` | Go types, method table, derived embedded schema file | S-4 | areas/sdk-acp-typert.md |
| C06 | schema | `internal/schema` | One description of data shapes: derive JSON Schema, validate, fingerprint | S-4 | libs/zod.md |
| C07 | model vocabulary | `internal/llm/vocab` | Leaf vocabulary only: `Message`, `Block`, sources, tool schema, `Controls`, route id, failure, canonical encoding for digests. Holds **no request type** (the one request type is C09's opaque `session.Request`) | S-4 | areas/llm.md (DH-D-LLM-11) |
| C08 | leaf helpers | `internal/util/*`, `internal/boot/{yamlx,expr,catalog}` | errors, ids, home path, media types, file primitives, diff, search, watcher, YAML dialect, expressions, logging component, component catalog | S-4, S-6 | areas/util-test-support.md, areas/filesystem.md, areas/boot.md |
| C09 | session core | `internal/session` | Typed versioned event registry; live session with ordered once-only delivery; brackets checked on append and closed on failure; folds for history / request header / tool history; opaque `session.Request` built by `NextRequest` (loop) and `RequestFrom(seq)` (auxiliary) | S-5 | areas/core-session.md |
| C10 | store contract | `internal/session/store` (+ in-memory reference) | `Store` interface, conformance suite, persisted-type inventory | S-5 | areas/session-store.md |
| C11 | folds and views | `internal/session/views` | Fold registry read at one cut; stored state never ahead of log; built-in views; list / observe / follow | S-5, S-8 | areas/session-views.md |
| C12 | LLM runtime | `internal/llm` | One runtime for every model call: route, attempt, settle as exactly one event | S-5 | areas/llm.md (DH-D-LLM-1) |
| C13 | agent contract | `internal/agent` | Find, address and own agents; three typed steering points | S-5 | areas/agent-loop.md |
| C14 | interaction registry | `internal/human` | One registry for pending approvals and questions | S-5 | areas/interaction-credentials.md |
| C15 | boot and host | `internal/boot/*`, `internal/host` | Launch facts, home layout check, profiles + bundles + layer stack, boot sequence, audit, readiness, fatal path (one message + report), bounded shutdown, preview, validated reload, settings edit path, egress off-switch | S-6 | areas/boot.md, areas/host-telemetry.md, areas/storage.md |
| C16 | CLI | `cmd/dsh`, `internal/cli/*` | One command tree, grammar, help, usage errors, generated reference, one print/exit contract | S-6 | areas/app-cli.md |
| C17 | subprocess | `internal/world/subprocess` | The one place a program is started (argument list only); stop and await a whole process tree on every OS | S-6 | areas/execution.md |
| C18 | secret store | `internal/human` (credentials) | Config names a secret; value resolved per use behind one store | S-6 | areas/interaction-credentials.md (DH-D-HUMAN-7) |
| C19 | egress | `internal/util/egress` | Every outbound connection follows one policy; nothing leaves by default | S-6 | areas/util-test-support.md, areas/host-telemetry.md |
| C20 | test kit | `internal/testkit` | Real components against scripted boundaries (fake model, fake transport, fake answerer), virtual time, temp homes | S-6 | areas/util-test-support.md (DH-D-UTIL-6) |
| C21 | SQLite store | `internal/session/store/sqlite` | One DB per home, atomic appends, flush barrier, one writer per session with safe takeover, checkpoints, crash recovery, export/import, follow, duties, delete/check/repair | S-7 | areas/session-store.md, libs/sqlite.md |
| C22 | storage | `internal/storage` | Typed machine-state tables, workspace registry, spill store, attachments, settings | S-8 | areas/storage.md |
| C23 | DeepSeek provider + retry | `internal/llm` (provider, retry) | Shared transport, Messages driver to the `deepseek-official` route, key auth via C18 through C19; bounded retry outside the adapter; buffered one-shot calls | S-9 | areas/llm.md (DH-D-LLM-2,3) |
| C24 | usage | `internal/llm` (usage fold) | Token usage and context occupancy read from settlements | S-9 | areas/llm.md (DH-D-LLM-6) |
| C25 | tools | `internal/tools` | Declare once, register in a scope; deterministic prompt + tool list; the 8-stage guarded pipeline; deadlines; result budget; bounded ordered batch | S-10 | areas/tools.md |
| C26 | approvals and grants | `internal/human` (approval, grants) | One closed outcome + attributed audit record; questions; human commands; session grants (see, revoke, never copied) | S-10 | areas/interaction-credentials.md |
| C27 | agent driver | `internal/agent` (driver) | One goroutine per agent: model selection, turns and steps bracketed and bounded, cancellation with cause, lifecycle transaction | S-11 | areas/agent-loop.md |
| C28 | inbox | `internal/agent` (inbox) | Input accepted any time, durable at once, runs at most once, taken at boundaries | S-10 | areas/agent-loop.md (DH-D-AGENT-2) |
| C29 | delegation seam | `internal/delegate` | Any component delegates through one seam; every child has one owner | S-11 | areas/subagent-team.md |
| C30 | context pipeline | `internal/context/request` | Context reaches the model through one logged ordered pipeline | S-11 | areas/context-compaction-skill.md |

**Spine direction rule.** Spine components (C09-C14, C21-C30) never import a carrier;
carriers (web, SDK server, launchers; later stages) subscribe to spine events as
listeners. Nothing in this story builds a carrier, so the rule is checked by the
import-direction test only.

---

## 4. Data model

Entities are grouped by owner. Field lists are the **key** fields this architecture
relies on; full field sets, ranges and error codes are owned by the area specification
of each owner and detailed in `tech-spec.md`. Every durable entity lives in the one
SQLite database of the harness home (`DH-ADR-12`, design.md sec. 4.7); in unit tests
the same contracts are served by the in-memory reference store (C10).

### 4.1 Harness home (owned by C15 boot, C21 store, C22 storage)

| Path under the home | Holds | Created by |
|---|---|---|
| `dsh.db`, `dsh.db-wal`, `dsh.db-shm` | The one SQLite database (WAL) | C21 on first open |
| `run/<instance>.lock` | One liveness lock file per running `dsh` process (never the DB file) | C21 |
| `spill/` | Private full text of oversized tool results | C22 |
| `blobs/` | Content-addressed attachments | C22 |
| `config/` | Home patch layer (the one writable layer) | C15 |
| owner-only secrets file | Secret values (OS keychain is DH-S-35, out of scope) | C18 |
| `reports/` | Fatal-path report, written once | C15 (DH-D-HOST-7) |

The directories are checked private at start (DH-D-STORAGE-2); a home on a network
filesystem is refused.

### 4.2 Session log (owned by C09 session core)

**SessionEvent** -- one immutable log entry.

| Field | Type | Meaning |
|---|---|---|
| `Seq` | `uint64` | Position from 0, contiguous, no gaps; assigned under the per-session append lock |
| `Time` | timestamp | Commit time (closers reuse the last real event's time) |
| `Type` | string | Registered type name, e.g. `turn/start`, `tool/result` |
| `PV` | int | Payload version; older versions are converted on read |
| `Data` | raw JSON | Payload; the raw string, not the decoded struct, is what fork, export and storage carry |
| `Ignorable` | bool | Unknown-but-ignorable types are preserved raw; otherwise open is refused (`session/unknown-type`, `session/newer-payload`) |
| `Surface` | enum 0/1/2 | log-only, append, replace -- only surface types may be non-zero |
| `Env` | JSON or null | Replaced range and cited `Sources` (non-empty, unique, earlier) |

**EventTypeDecl** -- package-level declaration: name, current `PV`, payload struct,
converters from older versions, validator, `Ignorable`, surface flag, `Detached`,
`Queueable`, optional request builder. The registry is sealed at boot; a later
declaration panics. `session/end-seed` and the surface flag are reserved to the core.

**Event types used at M1** (areas/core-session.md sec. 6):

| Type | Kind | Key payload fields |
|---|---|---|
| `system/message`, `developer/message`, `user/message` | surface | message blocks (C07 types) |
| `assistant/message` | surface; settlement | blocks, `requestAsOf`, `requestDigest`, `usage`, timing (start, first token, end) |
| `assistant/attempt` | log-only; settlement of a failed attempt | failure class, `requestAsOf`, `requestDigest`, timing |
| `request/header` | log-only | model, settings, tool schemas of a request |
| `turn/start`, `turn/end` | log-only bracket | turn number; end reason, one closed set (areas/agent-loop.md sec. 6.2): `completed`, `max-tokens` (sticky across later steps), `blocked{by, reason}`, `aborted{cause}` (cause: user, parent, extension, disposed, budget), `error{failure}` (incl. `extension/failed` with listener name), `refusal{failure}` (`llm/content-refused`), `budget{limit, value}`; `interrupted` is written only by the session core when it closes a turn at open/recovery |
| `step/start`, `step/end` | log-only bracket | step number |
| `tool/call` | log-only | call id, tool name, raw argument string as the model produced it |
| `tool/result` | surface | call id, content blocks, error code (`tools/...`), optional spill reference |
| `llm/retry`, `llm/retry-started`, `llm/retry-abandoned` | log-only | failure class, attempt number, wait |
| approval / grant records | log-only | attributed audit record of an outcome; grant given or revoked (names owned by area A17) |
| `session/end-seed` | log-only, core-only | `inherited`, lease `epoch`; written at each write-open and fork cut |

**SessionHeader** -- `id`, `created_at`, `cwd`, `origin`, `creator`, `fork_source`,
`fork_through`, `inherited_count`, `delegation_depth`, `preset`.

**Derived, never stored twice:** model history (fold over the surface), request header
fold, tool history fold, bracket state. A loop step's request is a pure function of the log
(`Session.NextRequest()` at the committed cut; auxiliary calls use
`Session.RequestFrom(seq)`); `requestDigest` is SHA-256 over the canonical encoding of
C07, and `surface.Rebuild` recomputes the request from the log prefix ending at
`requestAsOf`. That is
what makes the "request sent equals request rebuilt from the log" proof possible
(DH-O-4).

### 4.3 SQLite schema (owned by C21)

| Table | Key | Key columns | Invariant |
|---|---|---|---|
| `sessions` | `id` | header columns, `next_seq`, `lease_epoch`, `lease_holder`, `state` (`active`/`deleting`), `last_event_at`, `bytes`, `imported_from` | `next_seq` = number of stored events |
| `events` | `(session, seq)` | `time`, `type`, `pv`, `ignorable`, `surface`, `env`, `data` | A trigger aborts every `UPDATE`, and every `DELETE` unless the session is `deleting` |
| `checkpoints` | `(session, key, state_version)` | `seq`, `state` | Never ahead of the log; `core/` keys reserved |
| `hosts` | instance | process id, kind | One row per live instance, paired with `run/<instance>.lock` |
| `duties` | `name` | `holder`, `epoch`, `last_run_at` | Once-per-home maintenance lease (DH-D-STORE-8) |
| `requests` | `id` | `session`, `kind`, `drafts`, `state` (`pending`/`applied`/`refused`/`expired`) | Log-only changes handed to the holder; applied at most once |
| machine-state tables (C22) | per table | workspace registry, settings | Disk, then memory, then change notice; no approval rule or grant is ever stored |

**Append guard** (one savepoint per batch, in the writer goroutine's transaction):
`UPDATE sessions SET next_seq = next_seq + n WHERE id = ? AND lease_holder = me AND
lease_epoch = mine AND next_seq = first AND state = 'active'` -- exactly one row must
change, else the batch fails with `session/lease-lost`, `session/seq-conflict` or
`session/deleted`.

The schema uses only what `modernc.org/sqlite` (pure Go, provisional pick, impl.md
fact F-2) compiles in: WAL, triggers, savepoints. No loadable extension is needed --
this is what allows the embedded-database proof strategy of TC-07, TC-08, TC-10b and TC-11b.

### 4.4 Lease (owned by C21)

| Field | Where | Meaning |
|---|---|---|
| `lease_holder` | `sessions` row | Instance id of the one writer, or null |
| `lease_epoch` | `sessions` row | Raised on every acquisition; checked by every append |
| liveness lock | `run/<instance>.lock` | Kernel lock (`flock` / `LockFileEx`) held for the life of the process |
| `Takeover` | `OpenWrite` option | `IfFree` (default) or `Force` (explicit owner action) |

Nothing expires by time. A dead holder is detected only inside the acquiring write
transaction, by successfully locking its instance file, so two processes cannot both
conclude the holder is dead.

### 4.5 Model vocabulary, request and runtime (owned by C07, C09, C12, C23)

`internal/llm/vocab` (C07) is the leaf vocabulary and has **no request type**; the one
request type is `session.Request` (C09), opaque, per DH-ADR-SESSION-7.

| Entity | Key fields |
|---|---|
| `Message` | role, ordered `Block`s |
| `Block` | closed set: text, reasoning (with replay signature), tool-use (id, name, raw args), tool-result, image |
| `session.Request` (C09) | unexported fields, no constructor; read via accessors `Route()`, `Model()`, `Controls()`, `Messages()`, `Tools()`, `ToolHistory()`, `Meta()` (session + purpose), `AsOf()`, `Digest()`; built only by `NextRequest()` (loop step) or `RequestFrom(seq)` (auxiliary) |
| Settlement attribution | every `assistant/message` / `assistant/attempt` (loop or auxiliary) stores `requestAsOf` and `requestDigest` |
| `Route` | provider id `deepseek-official`, protocol Anthropic Messages, base URL, `CredentialRef`, catalogue entry |
| `Failure` | closed class (rate-limit, overloaded, auth, bad-request, network, refused), HTTP status, retry-after, cause; auth failure is never retried |
| `Usage` | input / output / cache tokens, read from settlements by a fold |

### 4.6 Tools and interactions (owned by C25, C26, C14)

| Entity | Key fields |
|---|---|
| `ToolDecl` | name, description, input type (schema derived by C06), output type, `Overlap(args)`, resource keys (read/write), timeout override |
| `Call` | call id, tool name, raw args, decoded args, agent, turn, step (listeners get a copy) |
| `Decision` | sealed: `allow`, `deny{reason}`, `ask{subject}`; a guard can only deny |
| `Interaction` | id, kind (approval / question), agent, subject, created, state (pending / answered / late / denied-no-answerer) |
| `ApprovalOutcome` | closed: approve once, approve for session (creates a grant), deny; no answerer = deny; plus attributed audit record |
| `SessionGrant` | session id, scope kind (directory / application), subject, granted by, time; in memory with its session, logged, never copied to a resume, fork or subagent |
| `SpillRef` | spill id, byte size, bounded head shown to the model |
| registry settings | `maxParallel` 10, `defaultTimeout` 5m, `grace` 5s, `maxArgumentBytes` 1 MiB, `maxResultBytes` 256 KiB |

### 4.7 Agent and driver state (owned by C13, C27, C28)

| Entity | Key fields |
|---|---|
| `Agent` | id, session id, preset, scope fiber, state (idle / running / closing) |
| `AgentHandle` | `Agent` plus the right to dispose it; held only by its owner |
| `InboxItem` | id, kind (prompt, steer, cancel, command), payload, durable `seq` once logged; taken at turn/step boundaries; runs at most once |
| `TurnState` | turn, step, open calls, first cancel cause + scope + inbox choice, turn and step contexts (children of the retained agent context), turn-budget counters (owned by the one driver goroutine) |

### 4.8 Configuration (owned by C03, C15)

| Entity | Key fields |
|---|---|
| `Layer` | name, order, source (bundle / profile patch / home patch / CLI overlay), operations (insert, override by id, merge, remove), `writable` (exactly one) |
| `Entry` | id, component name, config (raw / effective / candidate), `disabled` (may be an expression), required flag |
| `OwnershipIndex` | field path -> layer that supplied it |
| `Diagnostic` | file, line, column, message |
| `Profile` | name, `Required` list; at M1 tests compose a test profile with no carrier |
| `LaunchFacts` | home path, instance id, process id, platform, launch environment snapshot |
| `CredentialRef` | secret name; value resolved per request through C18 |
| `EgressPermit` | data kind, destination, allowed; every sender defaults to off |

### 4.9 Wire (owned by C04, C05)

`jsonrpc` frames are single-line JSON-RPC 2.0 messages with a byte bound; errors use
the one `<domain>/<reason>` table. At M1 the codec is exercised by its own tests and
fuzz targets and by protocol schema derivation; its first live transport is DH-S-15.

---

## 5. Interfaces between components

Signatures are illustrative Go that pin the **shape and direction** of each seam; the
area specifications own the final identifiers and `tech-spec.md` owns field-level
detail. Every blocking call takes a `context.Context` (from the caller's fiber, C02)
and every error carries a `<domain>/<reason>` code from `herr` (C08).

### 5.1 cordis additions (C02, DH-S-2)

```go
// DH-D-CORDIS-1: Dispose is complete, exclusive and final; Wait returns after
// every child fiber, effect and listener of the scope has stopped.
// DH-D-CORDIS-2: every loaded fiber owns a context cancelled by its unload.
func (f *Fiber) Ctx() context.Context
// DH-D-CORDIS-3: typed context values and dispatch scoped to one subtree (one agent).
func WithValue[T any](c *Context, key ValueKey[T], v T) *Context
func (k EventKey[T]) EmitScoped(c *Context, scope *Fiber, v T)
// DH-D-CORDIS-4: a listener's panic/error is contained and reported, never vetoes.
func (k EventKey[T]) Notify(c *Context, v T)
// DH-D-CORDIS-7: around interception with separate request and result types.
type AroundKey[Req, Res any] struct{ /* ... */ }
func (k AroundKey[Req, Res]) Around(c *Context, h func(ctx context.Context, r Req, next func(Req) (Res, error)) (Res, error))
func (k AroundKey[Req, Res]) Invoke(c *Context, r Req, inner func(Req) (Res, error)) (Res, error)
// DH-D-CORDIS-5/8: raw, effective and candidate config; strict decode refuses
// unknown fields; AcceptUpdate is the one live-config hook.
// DH-D-CORDIS-9: New(opts ...Option) takes the tree's log exporters.
```

### 5.2 loader (C03, DH-S-3)

| Operation | Shape | Used by |
|---|---|---|
| Decode with positions | `Codec.Decode(src []byte) (Node, error)`; `Node` carries file:line:col | C15 diagnostics |
| Compose | `Compose(layers []Layer) (Tree, OwnershipIndex, []Diagnostic)` | C15 boot, preview |
| Write back | `LayerSource.Write(entryID, path, value)` to the single writable layer, behind the write guard | C15 settings edit path |
| Restart one entry | `Loader.Restart(entryID)`, then a settled-reload notice | C15 validated reload |

### 5.3 Session core and store (C09, C10, C21)

```go
type Store interface {                       // C10 contract; memory reference + C21 sqlite
    Create(ctx context.Context, h SessionHeader) (SessionID, error)
    OpenWrite(ctx context.Context, id SessionID, o OpenOptions) (Writer, error) // lease
    OpenRead(ctx context.Context, id SessionID) (Reader, error)
    List(ctx context.Context, q ListQuery) ([]SessionSummary, error)
}
type Writer interface {
    Append(ctx context.Context, first uint64, evs []SessionEvent) error  // append guard
    Flush(ctx context.Context) error                                     // durability barrier
    Epoch() uint64
    Close(ctx context.Context) error
}
type Reader interface {
    Read(ctx context.Context, from, to uint64) iter.Seq2[SessionEvent, error]
    Stat(ctx context.Context) (SessionStat, error)
}

// C09 live session: the only path to the log for every component
// (areas/core-session.md sec. 6.1).
type Session interface {
    Append(ctx context.Context, d event.Draft) (event.Receipt, error)
    AppendGroup(ctx context.Context, ds []event.Draft) ([]event.Receipt, error) // all or none
    CloseOpen(ctx context.Context, c surface.Cause) ([]event.Receipt, error)    // closers, one group
    Flush(ctx context.Context) (Offset, error)                                  // durability barrier
    NextRequest() (Request, error)            // the loop step's request (DH-ADR-SESSION-7)
    RequestFrom(seq event.Seq) (Request, error) // an auxiliary request from ONE committed event
    AsOf(fn func(at Offset))                    // one consistent position
}
// Request is opaque: unexported fields, no exported constructor. Only the session
// builds one; the LLM seam reads it through accessors Route(), Model(), Controls(),
// Messages(), Tools(), ToolHistory(), Meta(), AsOf(), Digest().
type Request struct{ /* unexported */ }
```

Rules at the seam: only the session core assigns `Seq` and only the session constructs
a `session.Request`; `NextRequest` builds the loop step's request from the header in
force (route, model, controls, tool schemas), derived history and tool history at the
committed cut; `RequestFrom(seq)` is reserved for **auxiliary** calls (title, summary,
review) described by one committed event whose type declares a pure builder; the
leaf package `internal/llm/vocab` (C07) holds only the vocabulary a request is made
of (messages, blocks, tool schema, controls, route id, failure, canonical encoding) --
it has no request type. The store never decodes `Data`; no component writes a store
directly; the conformance suite runs unchanged against both stores.

### 5.4 LLM runtime and provider (C12, C23, C24)

```go
// package llm imports session for the one request type (areas/llm.md sec. 6.1).
type Runtime interface {
    Prepare(ctx context.Context, r RouteID, model string, c Controls) (Prepared, error)
    // Buffered auxiliary call. The request comes from Session.RequestFrom(seq); the
    // sink receives the one attempt record so the auxiliary settlement is logged and
    // attributed (requestAsOf, requestDigest, purpose from Request.Meta()).
    Complete(ctx context.Context, req session.Request, sink AttemptSink) (vocab.Message, error)
}
type Prepared interface { // what the driver holds; the raw adapter Attempt is not reachable
    // Once per attempt; always ends with exactly one Finish chunk. The runtime turns an
    // error, panic, missing finish or cancellation into that Finish and one attempt
    // record, which the CALLER (driver C27) logs as exactly one settlement event.
    Stream(ctx context.Context, req session.Request) iter.Seq[Chunk]
}
type Adapter interface {  // provider (C23): pure function of request + captured call
    Prepare(ctx context.Context, r RouteID, model string) (Call, error)
}
type Call interface {
    Attempt(ctx context.Context, req session.Request) iter.Seq2[Chunk, error] // one provider attempt
}
```

Retry is not an adapter concern: a failed attempt is offered to the
`agent.AttemptFailed` hook (sec. 5.6), whose decision (`Retry{After}` or `GiveUp`) is
bounded by the DH-D-LLM-3 policy and its `AttemptsLeft`.

The DeepSeek provider sends through C19 (`egress.Client` with an `EgressPermit` for
the "model request" kind to the route's host) and resolves its key per request through
C18; the key value never enters config, log or error text.

### 5.5 Tools, approvals, interactions (C25, C26, C14)

```go
func Define[In, Out any](name string, spec ToolSpec[In, Out]) ToolDecl
type Registry interface {
    Register(scope *cordis.Context, t ToolDecl) (dispose func())
    Snapshot(agent AgentID) ToolSet                                  // fixed once per step
    ExecuteBatch(ctx context.Context, calls []Call) []Result         // bounded, ordered commit
}
type Approvals interface {                                           // C26
    Request(ctx context.Context, a AgentID, subject Subject) (ApprovalOutcome, error)
}
type Interactions interface {                                        // C14
    Open(ctx context.Context, i Interaction) (InteractionID, <-chan Answer)
    Answer(id InteractionID, by Principal, a Answer) error           // late answer: recorded, ignored
}
```

Pipeline stages for each call (areas/tools.md): (1) resolve the tool in the step's
`ToolSet`; (2) bound, decode and validate arguments; (3) policy listeners (an
`AroundKey`, may return a `Decision`); (4) guards (deny only); (5) approval through C26
on `ask` -- no answerer means deny; (6) checkpoint flush; (7) run the body under its
deadline (`defaultTimeout`, then `grace`); (8) shape the result (budget, spill through
C22) and append `tool/result`. Error codes: `tools/not-started`,
`tools/outcome-unknown`, `tools/timeout`, `tools/aborted-before-start`.

### 5.6 Agents (C13, C27, C28, C29, C30)

```go
type Registry interface {                                        // C13
    Create(ctx context.Context, spec AgentSpec) (AgentHandle, error)  // one transaction
    Resume(ctx context.Context, id SessionID, o ResumeOptions) (AgentHandle, error)
    Lookup(id AgentID) (Agent, bool)
}
type Agent interface {
    Send(ctx context.Context, item InboxItem) (Receipt, error) // durable at once (C28)
    // o.Scope: Turn|Step; o.Inbox: Keep|Discard -- required, no zero-argument cancel.
    // Cause is a closed set: user, parent, extension, disposed, budget. Never waits.
    Cancel(cause Cause, o CancelOptions) error
}
type Delegator interface {                                     // C29
    Delegate(ctx context.Context, owner AgentHandle, spec AgentSpec) (AgentHandle, error)
}
type ContextStage interface {                                  // C30
    Contribute(ctx context.Context, d *RequestDraft) error
}
```

The steering points (DH-D-AGENT-4, areas/agent-loop.md sec. 6.2 and DH-ADR-AGENT-4)
are three `AroundKey`s with **closed decision types**; they are behavioral contracts,
not renameable hooks:

| Hook | When | Request | Decision |
|---|---|---|---|
| `agent.PreStep` | Point 4, before `step/start`; may append to the session | `PreStepRequest{Agent, Turn, Step, Messages, Selection, Previous}` | `Enter{Messages, StartsSeries}` or `Reject{Reason}` (turn ends `blocked`) |
| `agent.AttemptFailed` | Point 11, after a failed attempt settled | `AttemptFailure{Agent, Turn, Step, Attempt, Failure, Policy, AttemptsLeft}` | `Retry{After, Reason}` (back to point 7, same step; no re-assembly, no `PreStep`) or `GiveUp{}` |
| `agent.TurnStopping` | Point 14, only when no input is pending and tools owe no request | `StopRequest{Agent, Turn, Steps, Reason}` | `AllowStop{}` or `KeepOpen{Messages, Reason}`; registered via `agent.OnTurnStopping`, a merging helper that never hands out `next`: any one `KeepOpen` keeps the turn open, messages joined in registration order; never asked after abort, error or budget |

A listener error or panic in any of the three ends the turn as `error`
(`extension/failed`, listener named); the next turn still runs.

### 5.7 Boot, CLI, secrets, egress, subprocess, test kit (C15-C20)

| Seam | Shape |
|---|---|
| Boot | `boot.Run(ctx, LaunchFacts, Profile, overlays) (*cordis.Context, error)`; the fatal path writes one message and one `reports/` file; shutdown is bounded by `ShutdownTimeout` 20 s, exit status 124 when exceeded |
| CLI output | every command returns `(Result, error)`; one renderer maps it to stdout/stderr and an exit code |
| Secret | `secret.Store.Resolve(ctx, CredentialRef) (Credential, error)`, per use |
| Egress | `egress.Client(kind, dest) (*http.Client, error)`; refuses without a permit (`egress/not-permitted`) |
| Subprocess | `subprocess.Start(ctx, argv, opts) (*Proc, error)`; `Proc.StopTree(ctx, grace) error` stops and awaits the whole tree |
| Test kit | `testkit.New(t)` gives a temp home, a virtual clock, a scripted model, a fake transport serving recorded SSE and a fake answerer; everything else is the real component |

---

## 6. Sequence flows

Notation: `A -> B: msg` is a call, `-->` a return, `[log] type` an appended event.

### 6.1 F1 -- Boot of a composed tree (DH-S-6)

```
cli(C16)     boot(C15)          loader(C03)     cordis(C02)    store(C21)/storage(C22)
  |-- Run ----->|                   |                |                 |
  |             |-- launch facts, resolve home, check dirs private     |
  |             |-- Compose(bundle, profile patch, home patch, CLI) -->|
  |             |<-- tree + ownership index + diagnostics (file:line:col)
  |             |   diagnostic on a required entry => fatal path:      |
  |             |   one message, one reports/ file, exit != 0          |
  |             |-- Apply entries in dependency order ---------------->|
  |             |                                    |-- open dsh.db, hosts row,
  |             |                                    |   lock run/<inst>.lock
  |             |<-- readiness (all required entries ready) -----------|
  |<-- ready ---|   audit: every egress kind off unless permitted      |
```

### 6.2 F2 -- One M1 turn with a tool call and an approval (DH-S-11)

```
caller  inbox(C28)  driver(C27)  session(C09)+store(C21)  llm(C12,C23)  tools(C25)  approvals(C26)
  |-Send->|            |               |                     |             |             |
  |       |-[log] inbox splice (message id, durable) ------->|             |             |
  |<-Receipt{MessageID, Seq}           |                     |             |             |
  |       |--claim---->|-[log] turn/start + claim splice (one batch) ----->|            |
  |       |            | point 4: capture Selection, resolve ToolSet, assemble prompt;     |
  |       |            |   agent.PreStep --> Enter{Messages} | Reject (turn/end blocked)  |
  |       |            |-[log] step/start ------------------>|             |             |
  |       |            |-Prepare(route deepseek-official, model, controls) ->|            |
  |       |            | point 8 (admit, ONE batch, committed FIRST):        |            |
  |       |            |-[log] user/message, request/header (model, controls,|           |
  |       |            |       tool schemas, credential ref name), request/context ------>|
  |       |            | point 9: checkpoint, THEN build at the committed cut:            |
  |       |            |-Flush ----------------------------->| (durable before I/O)      |
  |       |            |-NextRequest() --------------------->| header in force + history |
  |       |            |<-- session.Request{asOf = cut, digest} (opaque) |  + tool history|
  |       |            |-Prepared.Stream(req) ------------------------------>|           |
  |       |            |                                     |--HTTPS through egress permit
  |       |            |<-- chunks ... exactly one Finish + attempt record ---|          |
  |       |            |-[log] assistant/message (tool-use, requestAsOf, requestDigest) ->|
  |       |            |-ExecuteBatch(calls, step ToolSet) ------------------>|          |
  |       |            |                                     | [log] tool/call; validate, policy, guards
  |       |            |                                     |             |-Request---->|
  |       |            |                                     |             |  (no answerer => deny)
  |       |            |                                     |             |<--outcome---|
  |       |            |                                     | [log] approval record; Flush; run body
  |       |            |<--[log] tool/result (spill if > maxResultBytes) ---|             |
  |       |            |-[log] step/end; tools owe a request -> step 2: admit -> Flush ->|
  |       |            |   NextRequest -> Stream -> text only -> [log] step/end           |
  |       |            | point 14: agent.TurnStopping --> AllowStop (or KeepOpen -> step 3)
  |       |            |-[log] turn/end{completed}; Flush (point 16) ------>|             |
```

Ordering rule: the step's header and context are **committed before** the request is
built, so `NextRequest` always sees this step's model, settings and tool schemas; the
flush makes that cut durable before any byte leaves the machine. Invariant checked by
the M1 proof: the `requestDigest` captured at the fake transport equals the digest of
`surface.Rebuild` over the log prefix ending at `requestAsOf`, for every attempt.

### 6.3 F3 -- Attempt failure and bounded retry (DH-S-9)

```
driver: (admitted batch already committed + flushed) req := NextRequest()
driver -> llm: Prepared.Stream(req) --> Finish{failure overloaded, retry-after 2s}
driver: [log] assistant/attempt{failure, requestAsOf, requestDigest}   (point 10)
driver -> hooks: agent.AttemptFailed(AttemptFailure{..., Attempt 1, Failure, Policy,
                 AttemptsLeft}) ; llm.retry listener checks class + step cap:
        [log] llm/retry{attempt 1, delay, failure, policy} --> Retry{After 2s}
driver: waits on the step context and the (virtual) clock; [log] llm/retry-started
driver: back to point 7 in the SAME step: Prepare (bind) again, then point 8 admission
        and the point 9 checkpoint run again. NOT repeated: prompt assembly, PreStep,
        and admission of the user messages (first attempt only).
        - nothing changed since the failed attempt: admission appends nothing new;
          Flush; NextRequest at the committed cut; Stream
        - an AttemptFailed listener appended a surface replacement (e.g. image offload,
          compaction): admission logs a NEW request/header (new request series) and
          any changed request/context; Flush; NextRequest at the NEW committed cut
          (new requestAsOf, new requestDigest); Stream
        --> [log] assistant/message{requestAsOf, requestDigest}
auth failure: not retryable -> [log] llm/retry-abandoned{not retryable} -> GiveUp{};
        one assistant/attempt, no llm/retry; turn/end{error{failure}}.
policy exhausted: [log] llm/retry-abandoned{exhausted} -> GiveUp{};
        turn/end{error{failure}}.
content refused: turn/end{refusal{failure llm/content-refused}} (no retry).
listener error or panic: turn/end{error{failure extension/failed, listener}}.
```

Each attempt's settlement records its own `requestAsOf` and `requestDigest`; the F2
proof (digest at transport equals `surface.Rebuild` at `requestAsOf`) holds per
attempt whether or not the cut moved between attempts.

### 6.4 F4 -- Cancellation with a cause, scope and inbox choice (DH-S-11, DH-ADR-AGENT-5)

Context tree (DH-ADR-AGENT-5): fiber lifetime > agent > turn > step > attempt | tool
call. `Cancel` cancels only the **turn** or **step** context; the fiber lifetime and
agent contexts, and therefore the driver goroutine and the inbox, survive.

```
caller -> agent: Cancel(CauseUser, CancelOptions{Scope: Turn, Inbox: Keep})
agent: records the cause (first cause wins); cancels the TURN context; returns at once
driver: in-flight Stream ends with one Finish{cancelled}
        -> [log] assistant/attempt{cancelled} (or assistant/message for a kept prefix)
driver: running tool -> pipeline grace -> detached -> tool/result{tools/outcome-unknown};
        unstarted calls -> tool/result{tools/aborted-before-start | tools/not-started}
driver: closers and final writes use cleanupCtx :=
        context.WithTimeout(context.WithoutCancel(turnCtx), cleanupBudget)
        -> [log] step/end, turn/end{aborted{cause user}}; Flush(cleanupCtx)
inbox:  Keep    -> pending items stay parked; a later wake opens the next turn
        Discard -> [log] claim splice {discarded{cause}}; live notice InboxDiscarded
driver: back to idle on the SAME agent context; a waking Send that arrived during the
        cancel is queued as a new turn
Scope: Step -> only the step context is cancelled; step/end is written and the driver
        continues at point 14 of the same turn
property: no tool/call is left without a tool/result on any exit path
```

### 6.5 F5 -- Crash recovery on write-open (DH-S-7, DH-D-STORE-4)

```
proc B -> store: OpenWrite(id, IfFree)
store (one tx): A's run/A.lock can be locked => A is dead; lease_epoch++; holder = B
store: read the tail; open brackets found (turn 3, step 2, call c7 unanswered)
store: append closers with the last real event's time:
       tool/result{c7, tools/outcome-unknown}, step/end, turn/end{interrupted}
store: append session/end-seed{epoch}; commit; only then publish the session
```

### 6.6 F6 -- Two processes: refusal and takeover (DH-S-7, DH-D-STORE-3)

```
A: OpenWrite(id)            -> holder A, epoch 4
B: OpenWrite(id, IfFree)    -> A.lock busy -> refused (lease held)
B: OpenWrite(id, Force)     -> epoch 5, holder B
A: Append(first = n)        -> guard WHERE lease_epoch = 4 matches 0 rows -> session/lease-lost
A: driver ends its turn as interrupted in memory; A writes no further event
```

### 6.7 F7 -- Gate run (DH-S-1)

```
CI job (linux, macos, windows) -> go run ./tools/harness-gate
gate runner: gofmt -> vet + own analyzers -> go test -race -shuffle=on (virtual time)
          -> coverage merge, 85% floor per package -> harness-trace verify
          -> performance budget tests -> GOOS/GOARCH cross-compile of cmd/dsh
first failure: print the gate's name, exit != 0 (no baseline, no exemptions)
```

---

## 7. Concurrency and failure invariants

| Invariant | Owner | Mechanism |
|---|---|---|
| One writer per session across processes | C21 | Lease epoch + per-instance liveness lock; append guard |
| One driver goroutine per agent; `TurnState` is never shared | C27 | The inbox is the only way in; items are taken at turn/step boundaries |
| Tool calls overlap only when resource keys do not conflict; results commit in the model's order | C25 | Scheduler capped by `maxParallel` |
| Model-visible means logged | C09, C12, C27 | Only the session constructs a `session.Request` (`NextRequest` for loop steps after the admit batch is committed, `RequestFrom(seq)` for auxiliary calls); the driver logs exactly one settlement per attempt with `requestAsOf` and `requestDigest` |
| Brackets always balance | C09, C21 | Checked on append; closers on failure and on write-open recovery |
| Checkpoint before irreversible work | C21 | Flush before a provider attempt, before a tool body and at step end; a failed flush means the work does not run |
| Unload is final | C02 | Fiber context cancelled; `Wait` returns after every effect has stopped |
| Nothing leaves the machine by default | C19, C15 | One permit per data kind; the boot audit lists them |
| Bounded shutdown | C15, C17 | 20 s budget, process trees stopped, exit status 124 on overrun |

The full failure table is design.md sec. 5; this story implements the rows owned by
the components above and no others.

---

## Test & Environment Contract

The gate host has **no docker and no pre-provisioned services**. Strategy words are
used strictly: `in-process` means **zero external I/O** (no file, no child process,
no socket, no database) -- pure logic on in-memory fakes and the virtual clock.
Anything that touches an OS resource is a `service:<name>` row, and the table names
how the gate host provides it. A `service:` criterion may never be proved `inline`
with a mock in its place. Downstream documents (implementation-plan, e2e-scenarios)
MUST use these strategies and TC ids.

**Local services the gate host provides (none needs docker, a server or a DSN):**

| Service | What it is | How the gate provides it |
|---|---|---|
| `service:filesystem` | A private scratch directory | `t.TempDir()` on the gate host's local disk, created and removed per test; never the developer's real home |
| `service:os-process` | Child processes and process trees | The test binary re-executes itself as the child (`os.Args[0]` with a helper env var); no external program is installed |
| `service:go-toolchain` | `go build`, `go vet`, `go test`, cross-compile | The pinned Go toolchain the gate itself runs with (`go 1.26.0` line, fact F-1); the gate already requires it |
| `service:loopback-http` | Ephemeral HTTP(S) servers | `net/http/httptest` bound to `127.0.0.1:0`, started and closed in the test; no external network |
| `service:sqlite` | The SQLite engine | **Embedded / ephemeral**: `modernc.org/sqlite` (pure Go) compiled into the test binary; database files under `service:filesystem`. The schema needs no non-bundled extension (WAL, triggers, savepoints only -- sec. 4.3), so an embedded build is sufficient and no DSN is needed |

**Acceptance areas.** Each area is split into a zero-I/O logic row (`a`) and, where
it exists, an integration row (`b`) naming every external dependency.

| ID | Acceptance area | Proof strategy | Dependency | How the gate provides it |
|---|---|---|---|---|
| TC-01a | Gate-runner logic (DH-S-1): gate ordering, first-failure reporting, coverage-floor arithmetic, coverage-profile merge, analyzer decision functions | `in-process` | none | Table tests over in-memory gate results, in-memory coverage profiles, and analyzer predicates called on ASTs parsed from in-memory source strings (`go/parser.ParseFile` with a `src` argument); no package loading, no `analysistest` |
| TC-01b | Gate runner end to end, CI matrix, cross-compile, release rehearsal (DH-S-1) | `service:go-toolchain` + `service:filesystem` + `service:os-process` | Go toolchain; scratch modules; `go test` subprocesses | Seeded violations (a data race, an unformatted file, a package under the floor) written as modules in `t.TempDir()`; the gate launches `go vet`/`go test -race` on them as child processes; `GOOS`/`GOARCH` cross-compile of `cmd/dsh` into the temp dir; own analyzers proved with `golang.org/x/tools/go/analysis/analysistest`, which loads fixture packages from committed `testdata/` directories through the toolchain's package loader |
| TC-02a | cordis changes (DH-S-2) | `in-process` | none | Race tests named by DH-T-CORDIS on in-memory contexts; the existing `src/go/cordis` tests keep passing |
| TC-02b | cordis behavior preserved (DH-S-2) | `golden` | committed golden | A golden of `cmd/cordis-sample` output, committed BEFORE the first DH-S-2 edit and compared after each change (only intended diffs are re-recorded, with the one update switch) |
| TC-03a | loader merge, ownership, diagnostics logic (DH-S-3) | `in-process` + `golden` | committed goldens | Layer documents parsed from in-memory bytes; golden diagnostics (file:line:col) |
| TC-03b | loader file layers and write-back (DH-S-3) | `service:filesystem` + `golden` | scratch directory | Layer files written to `t.TempDir()`; written-back files compared with golden files (comments kept) |
| TC-04a | Codec, schema, protocol, vocabulary (DH-S-4) | `in-process` + `golden` | committed goldens | Golden derived schema of `src/sdk/protocol`; golden persisted-type fingerprints; `jsonrpc` / `yamlx` fuzz seed corpora run as unit tests on in-memory buffers |
| TC-04b | Filesystem leaves and watcher (DH-S-4, DH-D-FS-1..3) | `service:filesystem` | scratch directory + OS file-change notification | Real watcher on a `t.TempDir()` tree; events awaited with a bounded deadline |
| TC-05 | Session core contracts (DH-S-5) | `in-process` | none | Store conformance suite against the in-memory reference store; `NextRequest` / `RequestFrom(seq)` rebuild-equals-digest tests; bracket property tests; import-direction test (`llm/vocab` has no request type) |
| TC-06a | Boot, CLI, secrets, egress, test kit -- logic (DH-S-6) | `in-process` | none | Composition, result-to-exit-code mapping, permit decisions and secret redaction on in-memory fakes |
| TC-06b | Boot homes, fatal-path report, secret files (DH-S-6) | `service:filesystem` | scratch home | Temp homes in `t.TempDir()`; private-directory checks; one `reports/` file on the fatal path |
| TC-06c | Subprocess trees, bounded shutdown (DH-S-6) | `service:os-process` | child processes | Re-exec of the test binary as the child and grandchild; `StopTree` awaited; exit status 124 on overrun |
| TC-06d | Egress client (DH-S-6) | `service:loopback-http` | ephemeral loopback servers | `httptest` on `127.0.0.1:0`, with and without a permit (`egress/not-permitted`) |
| TC-07 | SQLite store, leases, recovery, export/import (DH-S-7) | `service:sqlite` + `service:filesystem` + `service:os-process` | SQLite engine; temp home; second process | Embedded provision above; two-process lease and crash-injection tests re-exec the test binary against the same temp home |
| TC-08 | Machine state, views, spill, attachments (DH-S-8) | `service:sqlite` + `service:filesystem` | SQLite engine; temp home | Embedded provision above; refold-equals-stored test for every built-in view; spill files in the temp home |
| TC-09a | DeepSeek provider decode, retry decisions, usage fold (DH-S-9) | `in-process` + `golden` | committed recorded SSE fixtures | Fixtures (success, overloaded with retry-after, bad key, mid-stream drop) fed through the test kit's in-memory fake transport; one settlement per attempt; `AttemptFailed` decisions on the virtual clock |
| TC-09b | Provider over HTTP (DH-S-9) | `service:loopback-http` | ephemeral loopback server | The same fixtures served over `httptest`; request bytes at the server equal `surface.Rebuild` at `requestAsOf`. The real DeepSeek API is a **manual** check (impl.md), never a gate criterion |
| TC-10a | Tool pipeline, approvals, grants, inbox, questions, human commands -- logic (DH-S-10) | `in-process` | none | Test kit with the in-memory session store, virtual time and a fake answerer; deny-through-executor; deadline and grace on the virtual clock |
| TC-10b | Persisted tool results, spill, durable inbox (DH-S-10) | `service:sqlite` + `service:filesystem` | SQLite engine; temp home | **Unconditional** for these cases: the SQLite store is always composed; result over `maxResultBytes` spilled to the temp home; inbox splice durable before `Send` returns; flush-before-body checked against the database |
| TC-11a | Agent loop, M1 (DH-S-11) | `in-process` + `golden` | scripted model | Scripted fake model on the in-memory store drives turns, steps, `PreStep` / `AttemptFailed` / `TurnStopping`, cancellation (scope Turn/Step, inbox Keep/Discard) and lifecycle; keyless golden of the bracketed log of flow F2; property "no tool call is left unanswered on any exit path" |
| TC-11b | M1 persisted run (DH-S-11) | `service:sqlite` + `service:filesystem` | SQLite engine; temp home | Flow F2 against the SQLite store; checkpoint-before-I/O order observed in the database. The real-model M1 run is **manual** |
| TC-X1 | Cross-cutting quality | `service:go-toolchain` | Go toolchain | Race detector and shuffle on every test; 85% coverage floor per package (merged across re-exec'd processes); import-direction tests |
| TC-X2 | Physical / hardware | `lab` | -- | **None** in this story. Linux control-group ownership (register U-K1-1) falls back to process groups on the gate host, and the fallback is reported |

Rules for downstream documents:

1. `implementation-plan.md` cites a TC id as the proof of each work item and adds no
   docker, database server or external-network test. A work item proved by a `b`/`c`/`d`
   row may not substitute an in-memory fake for the named service.
2. `e2e-scenarios.md` marks every `DH-E-*` scenario of DH-S-1..11 as **authored here;
   executed at the stage impl.md assigns; none executed in this story**, and keeps
   impl.md's schedule, including its deferrals:

   | Scenarios | Executed at (impl.md) |
   |---|---|
   | DH-E-STORE-1, DH-E-STORE-2, DH-E-STORE-6 | guarded step 16.8 (scratch homes, Linux/macOS/Windows) |
   | DH-E-STORE-5 (request table) | step 35.7 |
   | DH-E-STORAGE-1 / -2 / -3 | DH-S-19 (19.7) / DH-S-23 / DH-S-39 |
   | DH-E-TOOLS-1; DH-E-TOOLS-3; DH-E-HUMAN-6 | DH-S-17 (17.5); DH-S-39; DH-S-22 (22.10) |
   | DH-E-BOOT-1; DH-E-BOOT-2; DH-E-UTIL-1 | DH-S-22 (22.3); guarded step 21.13; DH-S-39 |
   | DH-E-LOADER-1 | DH-S-17 (17.7) |
   | other scenarios of DH-S-5, S-9, S-11 / of DH-S-4 | DH-S-17 / DH-S-18 |

   Until step 16.8 passes, the store built here is used on **scratch homes only**;
   no scenario above is a gate criterion of this story.
3. Every golden is committed with the code that produces it; the TC-02b golden is
   committed before the code it protects changes.

---
## 8. Risks and open items

| Item | Risk | Handling |
|---|---|---|
| Size | 11 stages and about 106 steps for 13 points | Open question `dsh-story-slice`; recommended: keep M1 and land it stage by stage |
| SQLite driver (fact F-2) | `modernc.org/sqlite` is provisional until measured | Step 7.1 measures it first; the store contract (C10) isolates the choice |
| Go floor (fact F-1) | `go 1.26.0` line for all three modules | Checked by the DH-S-1 gates; the local toolchain is newer and builds it |
| DH-S-6 dependency circle C-1 | 14 deliverables depend on each other | Built contract-first; the stage closes only when all 17 are built at one commit |
| Turn budget defaults | Not confirmed by the owner (impl.md sec. 9.1) | Used as configurable defaults; recorded in `tech-spec.md` |
| H-21 | `dsh config` commands need the DH-S-16 admin tree | Preview and explain are built as a Go API only |
