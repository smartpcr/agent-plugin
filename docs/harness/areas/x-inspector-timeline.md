# Experimental: diagnostics, session log and statistics pages, Timeline, session history tools, chat import: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) (`2e39e70`) and are cited as `path:line` relative to the upstream root; a bare `<package>/…` path is under `packages/experimental/`. Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `XINSPECT`. Nothing here has been built, run or tested. Upstream marks all eight packages experimental: under evaluation, no support commitment (`packages/experimental/README.md:26`).

| | |
|---|---|
| **Area key** | `XINSPECT` (agent X3) |
| **Upstream packages** | 8 under `packages/experimental`: `inspector`, `inspector-profile`, `session-inspector`, `client-ui-stats`, `timeline`, `session-search`, `tool-session-query`, `deepseek-chat-import` |
| **Serves outcomes** | [DH-O-14](../design.md#dh-o-14), [DH-O-12](../design.md#dh-o-12), [DH-O-5](../design.md#dh-o-5), [DH-O-4](../design.md#dh-o-4), [DH-O-6](../design.md#dh-o-6), [DH-O-2](../design.md#dh-o-2) |
| **Depends on areas** | A06 ([session-views.md](session-views.md): folds, `Query`, search, titles), A05 ([session-store.md](session-store.md): store, delete, `imported_from`), A01 (session create, event types, request builders), A03 (tool pipeline, `Restrict`, `Guard`), A04 (auxiliary model call, usage and timing fields), A07 (attachments, workspace membership, retention), A18 ([host-telemetry.md](host-telemetry.md): inventory, local mode, control endpoint), A19 (routes, stream), A21, A22 (loader view, `inspect` registry), A23 (`egress`), A24 (commands), A27 (optional bundles), C1, C4, C5, C6 (shell, transcript, trajectory, side panel) |
| **Priority** | P2 throughout. Every component is a default-off entry under `internal/x/` ([design.md §4.2](../design.md#42-go-module-layout)); none is needed for a first working harness or for parity on the main paths |

## 1. Purpose

This area holds the tools for looking inside a running harness and its history, plus two features that upstream parked in the same group. It answers five questions. **What is running?** A page that shows the live cordis tree, the recent outbound HTTP exchanges, and a way to take a profile. **What exactly is in this session?** A page that shows every logged event, and a table that says where each turn's time and tokens went. **What is the agent working on?** Timeline: a task tree with progress, and short reports written at intervals. **What did we do before?** Five tools that let the model search and read earlier sessions. **Can I bring my chat history?** An importer for the archive that the DeepSeek chat website exports.

Without it a developer debugging the harness has only log files and an external debugger, long sessions show a flat todo list, and conversations held on the vendor's website stay there. Nothing else in the product depends on any of it.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **DevTools, CDP** | Chrome DevTools is the browser's developer panel. The Chrome DevTools Protocol (CDP) is the debugging protocol it speaks to a JavaScript engine. | Upstream's inspector is a CDP target; a Go process has no JavaScript engine to debug. |
| **Realm** | Upstream's word for one JavaScript runtime: the host process, or one browser page running client plugins. | Upstream inspects both; the Go UI has no client runtime ([DH-ADR-3](../design.md#dh-adr-3)). |
| **Runtime tree** | The live cordis tree: fibers, their state, what each injects and provides, and the effects each holds. The *composed tree* is what configuration asks for; the runtime tree is what exists now. | A component that failed, restarted or leaked an effect is visible only in the runtime tree. |
| **HTTP exchange, capture ring** | One outbound request with its response, as the host saw it. The ring is a bounded in-memory list of the most recent ones. | Shows retries, status codes and timing that the session log does not record. |
| **Profile, execution trace** | A profile is a sample of where a Go program spends processor time or memory (`runtime/pprof`). An execution trace records scheduler events over a period (`runtime/trace`). | Go's replacements for the CPU and heap panels of DevTools. |
| **Raw log, trajectory** | The raw log is every committed event of a session, in order. The trajectory (area C5) is a curated ledger of activity records built from it. | A developer needs the first; a user reads the second. |
| **Step row** | One row of statistics for one step: when it started and ended, time to first token, model time, tool time, tokens by bucket. | Comparing turns needs figures per step, not only session totals. |
| **Task tree, weight, progress** | A tree of tasks the agent maintains. A weight says how large a task is relative to its siblings; progress is the weighted share that is complete. | A flat todo list cannot show how far a long job is. |
| **Timed report, level** | A short summary the model writes in the background at a minimum interval. A higher level summarises the saved reports of the level below. | A person returning after hours reads three reports, not a transcript. |
| **Chat export, branch** | The ZIP archive the vendor's chat website produces. Each conversation in it is a tree of messages; a branch is one path from the root to a leaf. | A session is a linear log, so one branch must be chosen. |
| **Optional bundle** | A named, disabled group of entries that the operator switches on (A27, [bundle-preset.md](bundle-preset.md)). | The one way an experimental feature is enabled. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | For whom | What it does |
|---|---|---|---|---|
| `UP-PKG-packages.experimental.inspector` | `dsh-experimental-inspector` | provider, host and client | harness developer | A CDP target in a worker thread: console evaluation and debugging of the host, the cordis tree as the Elements panel, captured host `fetch` calls as the Network panel, served with a bundled DevTools frontend (`inspector/README.md:12`, `:40`, `:53`). |
| `UP-PKG-packages.experimental.inspector-profile` | `dsh-experimental-inspector-profile` | bundle | harness developer | One switch, "Developer tools", that mounts the inspector with fetch capture, the session inspector and the statistics view (`inspector-profile/cordis.patch.yml:1-10`). |
| `UP-PKG-packages.experimental.session-inspector` | `dsh-experimental-session-inspector` | client plugin | harness developer | A sidebar page with two tables over data already in the browser: the raw session log grouped by turn and step, and the chat's node objects (`session-inspector/README.md:12`, `:27`). |
| `UP-PKG-packages.experimental.client-ui-stats` | `dsh-experimental-client-ui-stats` | client plugin | operator, curious user | A "Statistics" tab: per-turn and per-step context size, cache hits, first-token latency, model and tool time (`client-ui-stats/README.md:12`). |
| `UP-PKG-packages.experimental.timeline` | `dsh-timeline` | provider, host and client | ordinary user and the model | Task trees that replace the todo tool, a progress bar, timed reports, discussions (`timeline/README.md:12`, `:35`, `:45`). Service `ctx.timeline`. |
| `UP-PKG-packages.experimental.session-search` | `dsh-experimental-session-search` | bundle | operator (switch), the model | Mounts the query tools with a private in-memory search index (`session-search/cordis.patch.yml:1-14`). |
| `UP-PKG-packages.experimental.tool-session-query` | `dsh-tool-session-query` | model-facing consumer | the model | Five read-only tools over `ctx.sessionQuery` (`tool-session-query/src/index.ts:65-121`). |
| `UP-PKG-packages.experimental.deepseek-chat-import` | `dsh-deepseek-chat-import` | provider, host and client | ordinary user | Uploads a chat export and stores its recent conversations as sessions in a workspace (`deepseek-chat-import/README.md:12`). Service `ctx.deepseekChatImport`. |

### 3.2 Behaviour

**Inspector.** The host plugin starts a worker thread that owns all CDP state, attaches Node's inspector to the host main thread, and prints a `devtools://` address (`inspector/README.md:53`, `:99`; `inspector/src/host/plugin.ts:170`). It serves the DevTools frontend and a WebSocket relay under `inspector/devtools`, both for an owner peer only (`plugin.ts:94-98`, `:133-137`). The frontend is compiled from a pinned snapshot of `chrome-devtools-frontend` (`README.md:42`). Host and browser pages run the same collector, which turns reachable contexts and fibers into a snapshot that the worker shows as a document tree (`README.md:63`, `:123`). With `captureFetch`, on by default, the plugin replaces `globalThis.fetch` and records the complete address, every header, both bodies, status and timing, **without redacting anything** (`inspector/src/host/inspection/network.ts:49`; `README.md:136`). The CDP socket has no token: binding to `127.0.0.1` is its only protection, and it grants arbitrary code execution in the host (`README.md:147`). The config has 24 fields, almost all sizing the worker's queues (`README.md:70-95`).

**Session inspector.** One sidebar page, no configuration, no change to session data (`session-inspector/README.md:27`). "Raw log" groups records between `turn/start` and `turn/end` with nested steps, shows stream chunks as child rows, filters by type fragment and pages through the browser's loaded window (`:29-35`). "Chat group" walks the chat's in-memory objects lazily, follows references between nodes, and locates or picks elements in the rendered chat by reading its DOM attributes (`:41-55`).

**Statistics.** Six client-side definitions fold the loaded event window into turn and step rows (`client-ui-stats/README.md:49`). The totals row reads the host projections `sessionStats` and `tokenUsage` when they exist and falls back to the loaded window otherwise (`:67`). Optional rows show compaction, retries, approvals and request headers (`:69`). Upstream itself points to the Trajectory view for "the full event ledger" and says the two "read the same Session" (`:32`).

**Timeline.** The model plans with five tools: `timeline_tree`, `timeline_tasks`, `timeline_pull`, `timeline_read`, `timeline_reply` (`timeline/src/tools.ts:46`, `:124`, `:271`, `:352`, `:502`). Every change is one of twelve `plugin:timeline/*` records in the session log, and a projection restores trees, reports and discussions (`timeline/src/records.ts:256-311`; `README.md:79`). A task update writes the whole tree, up to 2 MiB (`timeline/src/checkpoint.ts:16`). While "Replace Todo" is on, the plugin hides `todo_write` from each agent and guards it (`timeline/src/guidance.ts:169`, `:195`); subagents lose the planning tools and report to the agent that owns the plan (`timeline/src/delegation.ts:71`). After 24 steps without a successful update the model is reminded (`README.md:41`). Timed reports run in the background during active work at minimum intervals of 15 minutes, 1 hour and 4 hours; each is a separate model call whose request is logged as `plugin:timeline/request`, reusing the latest main request's prefix for the provider's cache (`timeline/src/policy.ts:8`; `timeline/src/reports.ts:196-202`; `README.md:107-111`). An owner can discuss a task or report, set a priority and ask for a pause, over an owner-only channel (`timeline/src/storage.ts:81`). The one typed event, `timeline/priority`, tells the plugin's own reminder module that a user edit moved a task into or out of the top priority (`storage.ts:56`; `timeline/src/reminders.ts:41`).

**Session history tools.** `session_search` (one best event per earlier session), `session_event_search`, `session_trace`, `session_event_trace` and `session_event_read` (`tool-session-query/README.md:45-51`). Authority comes from the calling agent: a target session is readable only when its `cwd` string equals the caller's (`tool-session-query/src/workspace-access.ts:81-97`). Results carry no cursor, offset or model-chosen limit; a capped search asks the model to narrow the query (`README.md:53`). A fixed guidance section is added to every request (`src/index.ts:51-63`). The `session-search` bundle gives these tools their own in-memory index that is rebuilt after each restart (`session-search/README.md:44`, `:70`).

**Chat import.** The owner uploads the ZIP; the host keeps the whole upload in memory (up to 512 MiB), selects the newest 1,000 conversations from the export's index, stores their images, converts each conversation's selected branch into turn, step, message and tool events with the original times, and writes each as a stored session without creating an agent (`deepseek-chat-import/README.md:50-51`, `:57-59`, `:117`). Web searches become `web_search` and `web_fetch` calls with inferred results (`:62`). Progress and the outcome stream back as lines of JSON (`:109`). One import runs at a time (`deepseek-chat-import/src/index.ts:120-131`).

### 3.3 Failure, retry, rollback and cleanup upstream

- **Inspector.** A worker that cannot start on the platform leaves the plugin loaded with a route that answers 503; any other start failure rolls back what was registered (`inspector/src/host/plugin.ts:69-76`, `:178-183`). A worker that exits later is not restarted (`README.md:170`).
- **Timeline.** A failed report writes `plugin:timeline/failure`; one repair request may follow a draft that fails validation (`README.md:111`). Unloading waits for pending session writes (`timeline/src/storage.ts:74-77`).
- **Tools.** Every query failure passes one sanitiser that keeps cancellation and hides provider detail behind a fixed code (`tool-session-query/README.md:57`).
- **Chat import.** Failures found before conversion write nothing. Later, each conversation stands alone: a failed one is skipped, and an archive that breaks mid-way fails after earlier sessions were written, **without listing them** (`deepseek-chat-import/README.md:70`). Images stored for a conversation that then fails stay with no reference, and a session that could not be added to the workspace stays stored, because upstream has no removal (`:164-165`). Cancelling keeps what was written (`:83`).

## 4. Upstream decisions dissected

The first two rows are the decision notes handed to this area (under `.agents/notes/implemented/feature/`). `XI-n` is short for `DH-ADR-XINSPECT-n` in [§7](#7-decisions).

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `2026-09-24-session-inspector` | The inspector reads data the browser already holds, adds no stream or storage, navigates object graphs by weak references and finds chat elements through the DOM; a default-off bundle also starts the debugging worker with unredacted capture (`:13-26`) | Change | The host owns the data in Go: it pages the log itself, so no "loaded window", and there is no object graph or client DOM contract to navigate. The bundle idea is kept | [XI-4](#dh-adr-xinspect-4), [XI-1](#dh-adr-xinspect-1) |
| `2026-11-11-deepseek-chat-export-import` | Browser upload, archive in memory, newest conversations first, one branch per conversation, direct store writes with original times, no live agent (`:20-29`, `:175`) | Keep the mapping, change the mechanics | Branch choice, per-conversation isolation and cold writes are right. Memory buffering, fabricated tool calls and upload-only entry are not | [XI-7](#dh-adr-xinspect-7) |
| A CDP target with code evaluation in the host | `inspector/README.md:147` | Drop | No engine to attach to; an endpoint that runs code as the user, guarded only by a loopback bind, contradicts [design.md §6](../design.md#6-security-model) | [XI-1](#dh-adr-xinspect-1) |
| Capture every `fetch`, unredacted, on by default once the bundle is on | `inspector/README.md:136` | Change | Masked by default, bodies off by default, at the one client factory | [XI-2](#dh-adr-xinspect-2) |
| The inspector service lets any plugin publish observation records to DevTools | `inspector/README.md:114-118` | Drop | Its only consumer is the CDP worker. Logs and the fatal diagnostic are A18's | [XI-1](#dh-adr-xinspect-1) |
| Statistics folded in the browser from the loaded window | `client-ui-stats/README.md:49`, `:73` | Change | A host fold over the whole log has no window to trim | [XI-4](#dh-adr-xinspect-4) |
| Timeline state as whole-value records in the session log | `timeline/README.md:79`, `:118` | Keep | It is the rule of [design.md §4.6](../design.md#46-event-model) | [XI-5](#dh-adr-xinspect-5) |
| Timeline replaces the todo tool by masking and guarding it | `timeline/src/guidance.ts:169`, `:195` | Keep | A03 offers `Restrict` and a deny-only guard for exactly this | [XI-5](#dh-adr-xinspect-5) |
| Report requests are logged with the prefix they reuse; a failed draft gets one repair call | `timeline/README.md:111` | Keep logging, drop the repair call | The log rule is [DH-O-4](../design.md#dh-o-4). A second unprompted call doubles the cost of a failure; the next interval retries | [XI-5](#dh-adr-xinspect-5) |
| Query tools authorise by exact `cwd` string and expose no cursor | `tool-session-query/README.md:53` | Change | A06's visibility uses workspace membership, and its keyset cursors do not go stale | [XI-6](#dh-adr-xinspect-6) |
| The search bundle owns a second, in-memory index | `session-search/README.md:44` | Drop | The index lives in the store and is on by default ([DH-ADR-VIEW-5](session-views.md#dh-adr-view-5)) | [XI-6](#dh-adr-xinspect-6) |
| Uploads are never staged on disk | chat-import note `:229` | Change | Go's ZIP reader needs random access, and 512 MiB of heap per import is the larger harm | [XI-7](#dh-adr-xinspect-7) |
| No host path may be typed for an import | chat-import note `:225` | Change | True for a browser; a command line on the host machine has the path | [XI-7](#dh-adr-xinspect-7) |

## 5. Tech debt and its resolution

All 49 units are bullets under "Known Limitations and Deferred Work" in the eight READMEs; each has its own row and reason in `trace/fragments/X3.jsonl`. They fall into seven groups.

| Group | Units (count) | How the Go design answers it |
|---|---|---|
| **A. Limits of a debugging target that is not built** | `inspector.2`, `.3`, `.4`, `.5`, `.6`, `.9` (6) | Not applicable: they describe the CDP relay, client-realm debugging and the worker thread ([XI-1](#dh-adr-xinspect-1)). The page itself is an ordinary document that the browser's own developer tools inspect. |
| **B. What capture and profiling miss or cost** | `inspector.1`, `.7`, `.8`; `inspector-profile.1`, `.2` (5) | Resolved. Capture sits at the one client factory, so no caller escapes it; bodies are off by default and copied through a bounded prefix buffer; profiles come from Go's runtime; there is no code-execution endpoint to warn about; the data is on owner-only pages, and no `dsh debug` command exists, because A18's control endpoint takes no registered method ([DH-D-XINSPECT-1](#dh-d-xinspect-1), [-2](#dh-d-xinspect-2)). |
| **C. A view computed in the browser from a loaded window** | `session-inspector.1` to `.4`, `.6`; `client-ui-stats.2`, `.3`, `.5`, `.6`, `.7`; `session-inspector.5`, `client-ui-stats.4`, `.9` (13) | Ten resolved: the host pages the whole log and folds the whole log, so there is no window, no ghost turn, no missing start time, and a wall-clock figure exists ([DH-D-XINSPECT-3](#dh-d-xinspect-3)). Three not applicable: a suggestion list's keyboard state, contention for a shared client page, and browser object-URL ownership are React behaviour. |
| **D. Kept on purpose** | `client-ui-stats.1`, `.8`; `timeline.2`, `.3`; `deepseek-chat-import.7`, `.10`, `.14` (7) | Carried, each with a justification in its ledger row: experimental views stay off by default ([design.md §4.2](../design.md#42-go-module-layout)); timing is derived from committed events only; a tree update is a whole-value event; a pause is a request to the agent, not a cancellation; one vendor format generation is read; files whose bytes the export lacks cannot be restored; content the vendor's site does not show as reply text is not imported. Stage [DH-S-59](../implementation-plan.md#dh-s-59). |
| **E. Timeline's visible window** | `timeline.1` (1) | Resolved: reports are derived rows paged by day, so nothing limits the view to seven days. |
| **F. Search and query tools** | `session-search.1`; `tool-session-query.1`, `.2`, `.3` (4) | Resolved by A06's design, which the tools use as written: a persistent index, a continuation cursor, workspace membership by canonical path, and the pipeline's result bound ([DH-D-XINSPECT-6](#dh-d-xinspect-6)). |
| **G. The archive reader and the import's leftovers** | `deepseek-chat-import.1` to `.5`, `.8`, `.9`, `.11`, `.12`, `.13`, `.15`, `.16`; `.6` (13) | Twelve resolved: a staged file read by `archive/zip` (a limit stops the work, the directory is the only source of entries, nothing is truncated: `DH-LIB-FFLATE-R2`); a result that always lists what was written; a path or directory as input; duplicate detection; unreferenced images collected; an unattached session discarded; one title limit; coded reasons; no fabricated tool records ([DH-D-XINSPECT-7](#dh-d-xinspect-7)). One not applicable: the second upload path served the dropped browser-worker preview ([DH-ADR-17](../design.md#dh-adr-17)). |

Totals: 32 resolved, 10 not applicable, 7 carried.

## 6. Go design

### 6.1 Shape

Packages are under `src/harness/internal/x`. Each row is one component, mounted only by its optional bundle. Names follow the one rule of [boot.md](boot.md) (`DH-ADR-BOOT-13`, §6.2 "Names"): the four optional bundles are the groups `x.devtools`, `x.timeline`, `x.session-recall` and `x.chat-import` (this document's short names `devtools`, `timeline`, `session-recall` and `chat-import` mean those groups); a group and a component never share a name, so the Timeline service component is `x.timeline.tree`; the component that only registers the history tools is `x.recall.tools`. The one command, `dsh session import-chat`, is contributed as a `cmdline.Command` value ([app-cli.md §6.1](app-cli.md)), and the import's upload route sets its own `Route.Limits` ([api.md](api.md)). The live tree is read through `Fiber.Children` and `Fiber.Provides` of the cordis port ([DH-D-CORDIS-11](../libs/cordis.md#dh-d-cordis-11)).

| Package | Component and service | Optional bundle | Why it exists |
|---|---|---|---|
| `x/inspect` | `x.inspect.tree` (registers a provider with A22's `inspect` registry), `x.inspect.http` (`inspect.RecorderKey`), `x.inspect.log`; injects A18's `inventory.Key`, A23's `egress.Key`, A06's `query.Key` | `devtools` | The diagnostic pages |
| `x/inspect/profile` | `x.inspect.profile`, no service; injects A18's listener facts | `devtools`, with its own `disabled: true` | Profiling is the one sensitive part, so it has a second switch |
| `x/stats` | `x.stats`; registers the `stepStats` row projection with `projection.Key` | `devtools` | Per-step figures and the Statistics page |
| `x/timeline` | `x.timeline.tree` (`timeline.Key`, `Service`); `x.timeline.reports`; injects `projection.Key`, `tools.Key`, the agent registry, the LLM runtime | `timeline` | Task trees, progress, reports, discussions |
| `x/recall` | `x.recall.tools`, no service; injects `query.Key`, `tools.Key` | `session-recall` | The five model-facing history tools |
| `x/chatimport` | `x.chatimport` (`chatimport.Key`, `Importer`); injects the session service (A01), `attachments.Key`, `workspace.Key`, `title.Key` | `chat-import` | The chat export importer |

```go
// package inspect
type Node struct {
    FiberID int; Name, Entry, State, Err string // Entry: the loader entry behind the fiber, from loader.EntryOf (DH-D-LOADER-8)
    Injects []Dep; Provides []string; Effects []string; Children []Node
}
func Snapshot(root *cordis.Context, maxNodes int) (tree Node, truncated bool) // read-only: Fiber.ID, Name, State, Err, Inject, Effects
type Exchange struct {
    ID uint64; Purpose, Method, URL string; Status int; Start time.Time; FirstByte, Total time.Duration
    RequestHeader, ResponseHeader http.Header // masked before they are stored
    RequestBody, ResponseBody []byte          // nil unless Config.Bodies; a masked prefix
    RequestBytes, ResponseBytes int64; Truncated bool; Err string
}
type Recorder interface { Recent(limit int) []Exchange; Get(id uint64) (Exchange, bool); Clear() }

// package timeline
type Service interface {
    View(ctx context.Context, s session.Ref) (View, projection.Cut, error) // trees, progress, discussions; reports are paged rows
    Edit(ctx context.Context, s *session.Session, by access.Principal, e Edit) error // discuss, priority, pause, continue, remove
    Export(ctx context.Context, s session.Ref, tree TreeID, w io.Writer) error
    Import(ctx context.Context, s *session.Session, r io.Reader, o ImportOptions) (TreeID, error)
}

// package chatimport
type Importer interface {
    Limits() Limits
    Plan(ctx context.Context, src fs.FS, o Options) (Plan, error) // dry run: what would be written or skipped, and why
    Run(ctx context.Context, src fs.FS, o Options, progress func(done, total int)) (Result, error)
}
type Options struct { Workspace workspace.ID; MaxSessions int; Copy bool }
type Result struct { Sessions []session.ID; Skipped []Skip; OmittedByLimit, AlreadyImported int }
```

`src` is an `fs.FS` so that a ZIP (`archive/zip.Reader`) and an extracted directory (`os.DirFS`) take one code path. Inference: `Snapshot` needs nothing new from the cordis port for fibers, state, injections and effect labels (this repository's `src/go/cordis/fiber.go:160-219`); the services a fiber provides and its child fibers need a read-only accessor, asked of the cordis owner in [§10](#10-open-questions-and-limitations).

**What each page shows, and where it comes from.** Every page is host-rendered HTML, owner-only, with text from the catalogue namespaces `x.inspect`, `x.stats`, `x.timeline` and `x.chatimport` in English and Chinese ([DH-ADR-UICORE-7](ui-core.md#dh-adr-uicore-7)); upstream's six further languages follow C1's P2 rule.

| Page or region | Content | Source | Live |
|---|---|---|---|
| `/debug/tree` | The runtime tree: one row per fiber with entry id, state, error, injected and provided services, effect labels; a link to the entry on A21's plugins page | `inspect.Snapshot`, joined with A18's `inventory.Snapshot` for layer and plugin process facts | Re-fetched on `inventory.Changed` |
| `/debug/http` | The capture ring, newest first; one exchange in detail | `Recorder` | Manual refresh |
| `/debug/profile` | A form: kind, seconds; the download | `runtime/pprof`, `runtime/trace` | no |
| `/sessions/{sid}/log` | Every event, grouped by turn and step, with `seq`, time, type, surface class and a one-line preview; filters by type and class, as ordinary form fields that the host applies on each request (no list state in the browser); jump to a `seq`; event detail as JSON, the cold part on request; a "views at this cut" panel listing each fold's state and `AsOfSeq`; a link from each row to the transcript and trajectory record of the same `seq` | `Query.Events`, `Query.Event`, `projection.Registry.Snapshot` and `Status` | New rows appended from the session's stream |
| `/sessions/{sid}/stats` | Turn rows that open into step rows; a totals row; optional rows for compaction, retries and approvals; "latest or cumulative" context basis | `step_stats` rows; A06's `stats` view and A04's `tokenUsage` for totals; `Query.Events` for the optional rows | Changed rows on `projection.Changed` |
| Timeline strip, panel and cards | A progress strip contributed to C4's `conversation.docks`; the tree, reports and discussions as the side panel (`shell.aside`); a progress card per update through C4's tool-card registry | `timeline.Service.View`, `timeline_report` rows | `projection.Changed` |
| Import dialog | File, workspace picker, progress, result | `chatimport.Importer` | A state region for the running operation |

The raw log page and C5's trajectory are different views of one log and are **not** merged: the trajectory is a product view of activity records, the raw log is every event unshaped. They share `Query` and link to each other by `seq`. The statistics table and the trajectory's timing overview show the same per-step numbers, so they share one fold: this area registers `stepStats` and C5 may read it.

### 6.2 Contracts

**Events.** No live event crosses a component boundary here. `UP-EVT-timeline/priority` has no Go event: producer and consumer are one package, so the edit handler calls the notice function directly and the notice reaches the model through the inbox. Durable types (log-only, registered with A01, state-carrying ones hold the whole value): `timeline/tree` (the whole tree, with its origin: model, import or copy), `timeline/remove`, `timeline/select`, `timeline/report`, `timeline/annotation`, `timeline/reply`, `timeline/delivery`, `timeline/priority`, `timeline/report-request` and `timeline/report-attempt`. The last two replace upstream's `request` and `failure` records and follow A06's rule for an auxiliary call ([DH-ADR-VIEW-6](session-views.md#dh-adr-view-6)): request flushed before dispatch, one attempt event per request. The chat importer declares no event type.

**Config.** Go structs with defaults as Go values; unknown keys fail the load.

| Go struct | Fields and defaults | Replaces |
|---|---|---|
| `inspect.Config` | `MaxExchanges` 2,000; `Bodies` false; `MaxBodyBytes` 65,536; `MaxTotalBytes` 64 MiB; `MaxTreeNodes` 2,048; `LogPageSize` 200 | `UP-CFG-dsh-experimental-inspector`. Six of upstream's 24 fields map onto these; the 18 that size the worker, the CDP bridge and client sources have no counterpart |
| `profile.Config` | `MaxSeconds` 60; `Kinds` (`cpu`, `heap`, `allocs`, `goroutine`, `block`, `mutex`, `trace`) | new |
| `timeline.Config` | `ReplaceTodo` true; `ReminderSteps` 24; `MaxTasks` 500; `MaxTrees` 30; `MaxDiscussions` 512; `Reports` false (recommended default, not confirmed by the owner; upstream: true); `Interval` 15 min (minimum 1 min); `RollupFactors` `[4, 4]` (levels at most 7 days); `MaxConcurrent` 2; `Timeout` 120 s; `MaxResponseBytes` 65,536; `ReportMaxTokens` 4,096; `ReasoningEffort` empty; `Language` `ui`; `TaskGuidance`, `ReportGuidance`, `LevelGuidance` | `UP-CFG-dsh-timeline` (`timeline/src/config.ts:82-164`). Display settings (`progressDecimals`, `segmentedProgress`, `autoOpenSidebar`) become per-viewer preferences in C1's store |
| `recall.Config` | `MaxSearchResults` 100 (capped by A06's `MaxLimit`); `SearchTimeout` 30 s | `UP-CFG-dsh-tool-session-query` |
| `chatimport.Config` | `MaxSessions` 1,000; `MaxArchiveBytes` 512 MiB; `MaxEntryBytes` 256 MiB | `UP-CFG-dsh-deepseek-chat-import`. `maxTitleBytes` is gone: the title service's limit applies |

**Services.** `UP-SVC-deepseekChatImport` becomes `chatimport.Key`. Upstream's `ctx.timeline` becomes `timeline.Key`. `ctx.inspector` was given a first row by area A02; its tree query lands in `inspect.Snapshot` and its `publish` method is dropped.

**Persisted data.** Two derived tables, declared through `projection.Definition.Tables` and deleted with their session: `step_stats` (session, turn, step: trigger, start, end, first token, model time, tool time as sum and as union of intervals, tokens by A04's four buckets, attempts, failed attempts, tool and nested-call counts) and `timeline_report` (session, level, start: end, body, source reports, request `seq`). Timeline trees and discussions are a value projection. The capture ring is memory only. The importer writes ordinary sessions and sets A05's `imported_from` column to `deepseek-chat/<conversation id>@<updated_at>`.

**Tools exposed to the model.** All through A03's pipeline; schemas derived from Go types.

| Tool | Arguments | Result and bounds | Upstream unit |
|---|---|---|---|
| `session_search` | Upstream's names, all kept (`docs/tool-catalog.md:2048-2140`): `query`, `session_ids`, `created_at_from`, `created_at_to`, `parent_session_ids`, `include_root_sessions`, `event_seq_from`, `event_seq_to`, `event_time_from`, `event_time_to`, `event_types`, `event_surfaces`; added: `mode` (`ranked`, `literal`) and `cursor` | `Query.Search` grouped by session, never the caller's session, restricted to matching events whose `seq` and commit time fall in the given windows: id, title, strongest event `seq`, snippet, `next_cursor`. Page of `MaxSearchResults` | `UP-TOOL-…session_search`, changed: cursor and mode added, `availability` dropped (below); no argument renamed |
| `session_event_search` | `query`, `mode`, `session_id` (default: current), `seq_from`, `seq_to`, `time_from`, `time_to`, `event_types`, `surfaces`, `cursor` | `Query.Search` in one session; in the current session it ends before the step that made the call | `…session_event_search`, changed likewise |
| `session_trace` | `session_id` | `Query.Lineage`: ancestors and descendants up to `LineageMaxNodes`; a boundary the caller may not see is a marker with no id | `…session_trace` |
| `session_event_trace` | `session_id`, `seq` | `Query.Trace`: what replaced the event and what cites it | `…session_event_trace` |
| `session_event_read` | `session_id`, `seq`, `before`, `after` | `Query.Event`: the hot part of one event and at most `ReadWindowMax` neighbour summaries per side; the pipeline's result bound applies last | `…session_event_read`, changed: bounded |
| `timeline_tree`, `timeline_tasks`, `timeline_pull`, `timeline_read`, `timeline_reply` | as upstream (`timeline/src/tools.ts`) | Whole-tree writes bounded by `MaxTasks` and A01's event size limit | not inventoried as units |

Visibility for the five history tools is A06's agent constructor with the configured policy (`own-workspace` by default), so a session outside it is reported exactly like a missing one. Upstream's `availability` filter is dropped: one store has no "live or stored" source to choose from. All five are read-only and may run in parallel.

**Routes** (declared through A19's `web.Routes`; all `access.Owner()`). `debug.index`, `debug.tree`, `debug.http`, `debug.http.clear`, `debug.profile` (a `Download`); `session.log`, `session.log.event`, `session.stats`; `timeline.panel`, `timeline.reports`, `timeline.edit`, `timeline.export`, `timeline.import`; `chatimport.upload` (an `Upload` whose body limit is `MaxArchiveBytes`, set through `Route.Limits`) and `chatimport.status`. A composition without a bundle has none of its routes. **Command**, contributed as a `cmdline.Command`: `dsh session import-chat <zip-or-directory> --workspace <id> [--dry-run] [--copy]` (class `Admin`). The three `dsh debug` commands of the first draft are dropped: they needed a method on A18's control endpoint, whose method set is closed because any same-user process can call it ([host-telemetry.md](host-telemetry.md), `DH-ADR-HOST-12`). The pages give the same data.

**HTTP capture.** `x.inspect.http` registers one transport wrapper with `egress`, so every client the factory builds is observed, whenever it was created. Before an exchange enters the ring: the values of `Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie` and any header whose name contains `key`, `token` or `secret` become `[redacted]`; so do query parameters with such names and every occurrence of a secret value the credential store has released in this process. Bodies are not kept unless `Bodies` is set; then a prefix of at most `MaxBodyBytes` is copied as the caller reads, through a fixed buffer, and masked the same way. Not observed: traffic of child processes (plugins, MCP servers, sandboxed commands) and WebSocket frames. Nothing in `x/inspect` can send: it holds no `sharing.Permit` ([DH-ADR-9](../design.md#dh-adr-9)).

**Profiling.** The routes exist only when the `x.inspect.profile` entry is enabled, answer only an owner, and refuse with `debug/remote-refused` unless the listener is in A18's local mode. One capture runs at a time (`debug/busy`); `seconds` is clamped to `MaxSeconds`. The handlers call `runtime/pprof` and `runtime/trace` directly. A profile holds function names, stacks and sizes, never memory contents; no route writes a heap dump or the command line.

**Chat import mapping.** Kept from upstream: the newest `MaxSessions` conversations by the export's index; the branch ending at `current_message_id`, else at the largest message id; one turn per user message; reasoning and answer text as assistant content in fragment order; a content-filtered round dropped with its question; another unfinished or template reply ends its turn with an error; event times from the export, never decreasing; the title pinned as a user title. Changed: a web search or opened page becomes Markdown text in the assistant message (query, then one link line per result), not a tool call; a file whose bytes the export lacks becomes a text note with its name and size, not a file handle; an excluded reply's error is a code (`chatimport/reply-<reason>`) that the transcript renders in the reader's language.

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership and access.** Each component's fiber owns its routes, tool registrations, the capture ring, the transport wrapper, a running profile, the report scheduler's goroutine and timer, and a running import with its staging file. Sessions own their derived rows and, once published, the imported sessions belong to nobody special: they are ordinary sessions. Reading a page is access, never ownership: no page in this area changes the tree, a session or a setting, except the Timeline edit and the import, which are explicit owner actions.

| Situation | What happens | Undone, retried, uncertain |
|---|---|---|
| A diagnostic component fails to load, or is disabled while a page is open | Its routes vanish with its fiber; an open page shows A19's "gone" fragment | Nothing else is affected: no spine service injects it |
| The capture ring is full | The oldest exchange is dropped; the page shows how many were dropped | Nothing to undo; the ring ends with the process |
| A profile request is cancelled, or the component unloads during it | The profile stops, the slot is freed, the partial download is abandoned | The user starts another |
| The `stepStats` fold panics or its row write fails | A06's rules apply: the append stands, the key is `failed` or `behind`, the page says so | Rebuilt by `Registry.Rebuild` |
| A Timeline tool call is invalid, or exceeds `MaxTasks` | The pipeline returns the error to the model; no event is appended | The model corrects the call |
| A report call fails, times out, is cancelled, or returns a draft that does not validate | One `timeline/report-attempt` with the outcome; no report row | Retried at the next due time, never at once. A host death between request and attempt is closed as `interrupted` by the next lease holder. **Uncertain:** whether the provider billed it |
| The Timeline bundle is switched off mid-session | Tools and the todo restriction leave with the fiber; events stay in the log; `todo_write` is visible again from the next step | Switching it on again folds the same events |
| A user edit arrives for a session this host does not lease | Refused with `session/owned`; the panel stays readable | The user opens the session where it is leased |
| An import is cancelled, the host dies, or the archive breaks mid-way | Each conversation is prepared invisibly (A01's `Prepare`), its images stored, its membership written, then published; a conversation not yet published is discarded. Published sessions stay and the result lists them | The staging file is removed on every exit path, or by A07's 24-hour staging retention after a crash. Images of a discarded conversation are unreferenced and collected ([DH-D-STORAGE-9](storage.md#dh-d-storage-9)). Re-running skips what `imported_from` already names |
| A conversation cannot be mapped or stored | It is skipped with a coded reason and the next older conversation takes its place | Nothing of it is visible |
| The archive's version, manifest or index is unusable, or it exceeds a limit | Refused whole before any write | Nothing was written |

## 7. Decisions

<a id="dh-adr-xinspect-1"></a>
### DH-ADR-XINSPECT-1 No debugging target: the DevTools hub, code evaluation and client inspection are not built
- **Decision.** The Go harness has no CDP endpoint, no evaluation of expressions in the host, no breakpoint relay, no client-realm inspection and no `publish` observation API. Option B of [libs/chrome-devtools-frontend.md](../libs/chrome-devtools-frontend.md) (a hand-written CDP endpoint for the two synthetic panels) is declined. The two harness-specific views, the tree and HTTP capture, become host pages ([DH-D-XINSPECT-1](#dh-d-xinspect-1)); stepping through the host is done with an external Go debugger.
- **Why.** [DH-ADR-CHROMEDEVTOOLSFRONTEND-1](../libs/chrome-devtools-frontend.md#dh-adr-chromedevtoolsfrontend-1) removed the frontend; a CDP endpoint with four empty panels would mislead, and CDP changes with Chrome. Evaluation is remote code execution guarded by a loopback bind (`inspector/README.md:147`), which a model-run command on the same machine can reach.
- **Tradeoff.** No expression evaluator and no familiar panels. A developer uses two tools (pages and a debugger) where upstream had one.
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** DH-T-XINSPECT-3.

<a id="dh-adr-xinspect-2"></a>
### DH-ADR-XINSPECT-2 Diagnostics are read-only, owner-only, bounded, masked by default and never sent
- **Decision.** As "HTTP capture" in [§6.2](#62-contracts) and the tree page in [§6.1](#61-shape). Diagnostic pages change nothing. Capture masks credentials before storing, keeps no body unless asked, lives in memory, and has no export.
- **Why.** `DH-LIB-CHROMEDEVTOOLSFRONTEND-R2` requires the two views to survive. Upstream's capture records keys and cookies in full (`inspector/README.md:136`); a debugging aid must not become the easiest place to steal a key from.
- **Tradeoff.** A masked header cannot be used to replay a request by hand. With `Bodies` on, prompts and tool output sit in memory a second time, which the setting's description says. Masking by name and by released value is not a guarantee for a secret the harness never saw.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-2](../design.md#dh-o-2). **Verified by.** DH-T-XINSPECT-1, DH-T-XINSPECT-2, DH-T-XINSPECT-3, DH-E-XINSPECT-1.

<a id="dh-adr-xinspect-3"></a>
### DH-ADR-XINSPECT-3 Profiles are exposed to the owner on a local host, behind a second switch
- **Decision.** As "Profiling" in [§6.2](#62-contracts): a separate disabled entry inside the `devtools` bundle; owner and local mode both required; a closed list of kinds; one capture at a time with a clamped duration; a command-line twin through the host's control endpoint. The package `net/http/pprof` is not imported anywhere in the harness.
- **Why.** `DH-LIB-NODEINSPECTOR-R1` asks for CPU, heap and trace capture, off by default, loopback and owner only. Inference: importing `net/http/pprof` registers its handlers on the process-wide default mux as a side effect (`net/http/pprof/pprof.go:95-103`, Go 1.27.4), including one that prints the command line; calling the runtime packages from the harness's own routes avoids relying on "nobody serves the default mux".
- **Tradeoff.** `go tool pprof` cannot be pointed at a URL without the launch cookie, so the command writes a file instead. A CPU profile slows the host while it runs. Refusing remote mode means a remote operator must use the command on the host machine.
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** DH-T-XINSPECT-4, DH-E-XINSPECT-1.

<a id="dh-adr-xinspect-4"></a>
### DH-ADR-XINSPECT-4 The raw log and the statistics are host pages over `Query` and one fold; the chat-node view and the element picker are dropped
- **Decision.** As the two session rows of the page table in [§6.1](#61-shape). `stepStats` is a row projection in A06's registry. The "Chat group" presentation, object-reference navigation and the click-to-pick mask are not built; a log row links to the transcript and trajectory by `seq` instead.
- **Why.** The host has the whole log and A06 already pages, classifies and bounds it. Upstream's second presentation inspects JavaScript objects of the chat engine, which do not exist ([DH-ADR-3](../design.md#dh-adr-3)); a link that navigates also does what upstream's locator could not: it opens the view and loads the history that holds the target.
- **Tradeoff.** No inspection of render state (there is none to inspect beyond the HTML). One more derived table. The reverse jump, from a transcript element to its log row, needs a per-node link that C4 would have to render in developer mode; it is left out.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Verified by.** DH-T-XINSPECT-5, DH-T-XINSPECT-6, DH-T-XINSPECT-7, DH-E-XINSPECT-1.

<a id="dh-adr-xinspect-5"></a>
### DH-ADR-XINSPECT-5 Timeline is a compiled-in, default-off component whose report calls are logged auxiliary calls
- **Decision.** Port Timeline with its five tools, typed durable events, a fold, the todo replacement through A03's `Restrict` and deny-only guard, and owner edits. A report is requested by a flushed `timeline/report-request` event, built into a model request only from that event, and settled by exactly one `timeline/report-attempt`. There is no repair call. Reports are rows, paged by day.
- **Why.** Timeline changes what the model plans with, so it cannot be a plugin process; its state is exactly the whole-value, log-first shape the design asks for. Report calls send conversation text to a provider and cost tokens without a user action, so they follow the same rule as titles ([DH-O-4](../design.md#dh-o-4)).
- **Tradeoff.** Two or three log events per report, and a whole tree per task update (kept on purpose, [§5](#5-tech-debt-and-its-resolution) group D). Recommended default, not confirmed by the owner: reports are off until the user turns `Reports` on in the bundle's settings card, where upstream turns them on with the bundle. A report is a model call nobody asked for at that moment, so the reading that fails closed is a second, visible switch; the cost is stated on the card and counted in `tokenUsage`. A rejected draft waits a full interval. Scope boundary of this record: Pause and Continue on the timeline are requests that reach the agent as a notice and take effect when the model records them in its plan, and stopping a running turn is the loop's own cancel ([DH-ADR-ARCH-3](../upstream/architecture.md#dh-adr-arch-3)) shown as the separate Stop control, because a panel click must never abort a tool call in flight.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-4](../design.md#dh-o-4). **Verified by.** DH-T-XINSPECT-8, DH-T-XINSPECT-9, DH-T-XINSPECT-10, DH-E-XINSPECT-2.

<a id="dh-adr-xinspect-6"></a>
### DH-ADR-XINSPECT-6 The history tools are a thin, default-off consumer of `Query`; there is no second index
- **Decision.** `x.recall.tools` registers the five tools and one prompt section, and adds nothing else: no index, no authorisation code, no sanitiser. `session-search` is merged into A06's search ([DH-D-VIEW-6](session-views.md#dh-d-view-6)); what remains of it is the bundle switch.
- **Why.** A06 states that these tools use `Query` and its bounds ([session-views.md §6.2](session-views.md)). Everything upstream's packages add beyond the tool surface (a private index, `cwd` string matching, a failure sanitiser, exclusive scheduling because cursors went stale) repairs a query service that trusted every caller.
- **Tradeoff.** The tools are only as good as A06's ranking. A cursor is one more argument the model can get wrong; an invalid one is a plain argument error. Mounting them costs five schemas and one section on every request, which is why they stay off by default.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-6](../design.md#dh-o-6). **Verified by.** DH-T-XINSPECT-11, DH-E-XINSPECT-3.

<a id="dh-adr-xinspect-7"></a>
### DH-ADR-XINSPECT-7 Chat import is its own importer, separate from the upstream-session import, sharing only the write path
- **Decision.** The chat importer is a separate P2 component, not a mode of the deferred command of [DH-ADR-11](../design.md#dh-adr-11) and [DH-D-STORE-10](session-store.md#dh-d-store-10). It reads a staged file or a directory, skips a conversation it cannot map and never refuses the whole archive for it, writes text where upstream fabricated tool calls, and records the source in `imported_from`. It shares with the upstream import what A01 and A05 offer both: creating a session with given times, blobs before events, and all-or-nothing visibility per session.
- **Why.** The two sources differ in everything that matters. An upstream session is the harness's own event vocabulary, so a faithful translation exists and an unmappable file is refused whole ([DH-ADR-STORE-11](session-store.md#dh-adr-store-11)). A chat export is a lossy foreign format holding thousands of conversations; refusing all for one bad file would make it useless. A tool call that this harness never made would also sit in history without a matching request header (`deepseek-chat-import/README.md:169`).
- **Tradeoff.** Imported searches show as links, not search cards. A staging copy of private data exists on disk for the length of the import. Two importers must both track changes to the session vocabulary. Scope boundary of this record: the chat importer keeps one branch of each conversation and leaves out the other branches, content-filtered rounds, risk disclaimers, tips, page-find and partial-read records, encrypted link bodies, the pinned flag, per-token times and accumulated token usage ([§6.2](#62-contracts) has the mapping), because a session is a linear log and the dropped records are either never shown as reply text by the vendor's site or must not enter the model's context.
- **Serves.** [DH-O-5](../design.md#dh-o-5). **Verified by.** DH-T-XINSPECT-12, DH-T-XINSPECT-13, DH-T-XINSPECT-14, DH-E-XINSPECT-4.

## 8. Deliverables

<a id="dh-d-xinspect-1"></a>
### DH-D-XINSPECT-1 A developer sees the live component tree and recent outbound HTTP on an owner-only page
- **Result and why it matters.** "Which component failed, what does it hold, and what did the host just send?" is answered without a debugger and without exposing a key.
- **How it is implemented.** A locked, read-only walk of the cordis tree into `Node` values, joined with A18's inventory; a transport wrapper at `egress` feeding a ring bounded by count and bytes; masking as in [§6.2](#62-contracts); pages through A19's routes; the tree also as a provider in A22's `inspect` registry, so `harness_inspect` can return it; the `devtools` optional bundle that mounts these with the log and statistics pages.
- **Components and files.** `x/inspect`. **Depends on.** [DH-D-HOST-10](host-telemetry.md#dh-d-host-10), [DH-D-EXT-8](extensions.md#dh-d-ext-8), `DH-D-LOADER-8`, `DH-D-UTIL-2`, `DH-D-API-1`, `DH-D-UICORE-1`. [DH-D-CORDIS-11](../libs/cordis.md#dh-d-cordis-11) (`Fiber.Children`, `Fiber.Provides`), [DH-D-UTIL-2](util-test-support.md#dh-d-util-2) (a transport-wrapper registration on `egress`), [DH-D-HUMAN-7](interaction-credentials.md#dh-d-human-7) (released secret values), [DH-D-BUNDLE-6](bundle-preset.md#dh-d-bundle-6) (the bundle row).
- **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-2](../design.md#dh-o-2). **Priority.** P2. **Done when.** DH-T-XINSPECT-1, DH-T-XINSPECT-2 and DH-T-XINSPECT-3 pass and [DH-E-XINSPECT-1](#dh-e-xinspect-1) has passed.

<a id="dh-d-xinspect-2"></a>
### DH-D-XINSPECT-2 The owner of a local host can take a CPU, memory or scheduler profile from it
- **Result and why it matters.** A slow or growing host can be measured in place with Go's standard tools.
- **How it is implemented.** One handler per kind over `runtime/pprof` and `runtime/trace`, a single-capture slot, the local-mode and owner checks, the context of the request as the stop signal; the profile is a download of the owner's browser.
- **Components and files.** `x/inspect/profile`. **Depends on.** [DH-D-XINSPECT-1](#dh-d-xinspect-1), `DH-LIB-NODEINSPECTOR`, `DH-D-HOST-1`. [DH-D-HOST-3](host-telemetry.md#dh-d-host-3) (the listener's `Mode()`).
- **Outcome.** [DH-O-14](../design.md#dh-o-14). **Priority.** P2. **Done when.** DH-T-XINSPECT-3 and DH-T-XINSPECT-4 pass and [DH-E-XINSPECT-1](#dh-e-xinspect-1) has passed.

<a id="dh-d-xinspect-3"></a>
### DH-D-XINSPECT-3 Any session can be read event by event, and its time and tokens are broken down per turn and step
- **Result and why it matters.** A developer checks what was logged; an operator sees which step was slow, cold in the cache or large.
- **How it is implemented.** The log page renders bounded pages from `Query.Events` and appends live rows from the session stream; the `stepStats` fold pairs each step's start, attempts, first token, tool calls and end from the hot fields A04 and A02 write, attributing a nested script call to the step of its outer call; the statistics page reads its rows by turn.
- **Components and files.** `x/inspect` (log), `x/stats`. **Depends on.** [DH-D-VIEW-2](session-views.md#dh-d-view-2), [DH-D-VIEW-3](session-views.md#dh-d-view-3), [DH-D-VIEW-5](session-views.md#dh-d-view-5), [DH-D-LLM-6](llm.md#dh-d-llm-6), `DH-D-API-4`, `DH-D-UICONV-1`. [DH-D-UIWORK-1](ui-work.md#dh-d-uiwork-1) (an address for a trajectory record by `seq`), [DH-D-UICONV-8](ui-conversation.md#dh-d-uiconv-8) (a transcript window around a `seq`). The trajectory's timing overview reads area A06's `stats` view, not this area's `stepStats` fold.
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Priority.** P2. **Done when.** DH-T-XINSPECT-3, DH-T-XINSPECT-5, DH-T-XINSPECT-6 and DH-T-XINSPECT-7 pass and [DH-E-XINSPECT-1](#dh-e-xinspect-1) has passed.

<a id="dh-d-xinspect-4"></a>
### DH-D-XINSPECT-4 The agent keeps a weighted task tree that a person can follow, discuss and steer
- **Result and why it matters.** A long job shows how far it is and what blocks it, across turns, forks and restarts.
- **How it is implemented.** Ten registered event types; a value projection for trees and discussions with progress computed from weights; five tools; `Restrict` and a guard for `todo_write` per agent scope when `ReplaceTodo` is on; planning tools withheld from subagents, whose results the owning agent records; a reminder after `ReminderSteps`; owner edits through `Service.Edit`, which queue inbox notices for priority changes and for pause or continue requests; tree export and import as a versioned JSON file; the strip, panel and cards of [§6.1](#61-shape).
- **Components and files.** `x/timeline`. **Depends on.** [DH-D-VIEW-1](session-views.md#dh-d-view-1), `DH-D-TOOLS-1`, `DH-D-TOOLS-2`, `DH-D-SESSION-1`, `DH-D-UICORE-2`. [DH-D-AGENT-2](agent-loop.md#dh-d-agent-2) (the inbox notice entry), [DH-D-SUBAGENT-1](subagent-team.md#dh-d-subagent-1) (the parent of a child agent at creation), [DH-D-UICONV-1](ui-conversation.md#dh-d-uiconv-1) and [DH-D-UICONV-4](ui-conversation.md#dh-d-uiconv-4) (`conversation.docks`, the tool-card registry), [DH-D-UISIDE-4](ui-sidebar-workspace.md#dh-d-uiside-4) (the side panel).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P2. **Done when.** DH-T-XINSPECT-8 and DH-T-XINSPECT-9 pass and [DH-E-XINSPECT-2](#dh-e-xinspect-2) has passed.

<a id="dh-d-xinspect-5"></a>
### DH-D-XINSPECT-5 Timed reports summarise the work at several intervals, and every report call is in the log
- **Result and why it matters.** A person who was away reads what changed in the last quarter hour, hour and four hours, and can audit what each summary cost.
- **How it is implemented.** A scheduler owned by the component's fiber checks, at each turn start and while a turn is active, which level is due from the `timeline_report` rows; level 0 summarises activity since its last report, higher levels summarise saved reports; each call follows [DH-ADR-XINSPECT-5](#dh-adr-xinspect-5) with purpose `timeline-report`; the reports view is a day per page with marks from `DH-LIB-D3TIME`'s ladder in the viewer's time zone.
- **Components and files.** `x/timeline/report`. **Depends on.** [DH-D-XINSPECT-4](#dh-d-xinspect-4), `DH-D-LLM-1`, `DH-D-SESSION-3`. [DH-D-SESSION-4](core-session.md#dh-d-session-4) (a declared request builder for `timeline/report-request` that may cite a committed request prefix), [DH-D-LLM-3](llm.md#dh-d-llm-3) (the buffered one-shot call that area A06's titles also use). Runtime prerequisite: a configured model route.
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-4](../design.md#dh-o-4). **Priority.** P2. **Done when.** DH-T-XINSPECT-10 passes and [DH-E-XINSPECT-2](#dh-e-xinspect-2) has passed.

<a id="dh-d-xinspect-6"></a>
### DH-D-XINSPECT-6 The model can search and read earlier sessions of its workspace
- **Result and why it matters.** An agent starts a task by finding what was done before, and cannot read what its caller may not see.
- **How it is implemented.** Five tool declarations that translate arguments into `Query` calls with the agent's visibility, exclude the caller's own session or current step, and return typed results; one prompt section; the `session-recall` bundle.
- **Components and files.** `x/recall`. **Depends on.** [DH-D-VIEW-5](session-views.md#dh-d-view-5), [DH-D-VIEW-6](session-views.md#dh-d-view-6), `DH-D-TOOLS-1`, `DH-D-TOOLS-2`.
- **Outcome.** [DH-O-5](../design.md#dh-o-5), [DH-O-6](../design.md#dh-o-6). **Priority.** P2. **Done when.** DH-T-XINSPECT-11 passes and [DH-E-XINSPECT-3](#dh-e-xinspect-3) has passed.

<a id="dh-d-xinspect-7"></a>
### DH-D-XINSPECT-7 Conversations exported from the vendor's chat website become sessions that can be continued
- **Result and why it matters.** A person keeps their history when they move to the harness, with original titles and times.
- **How it is implemented.** A pure converter from one conversation file to event drafts; an orchestrator that reads the index, then per conversation stores images, prepares the session, attaches it to the workspace and publishes it; `archive/zip` over a staged file or `os.DirFS` over a directory, each entry read through a reader that fails at the limit; a single-import slot; a host-tracked operation with progress and a final result; the upload form and the `dsh session import-chat` command.
- **Components and files.** `x/chatimport`. **Depends on.** `DH-D-SESSION-2`, [DH-D-STORE-7](session-store.md#dh-d-store-7), [DH-D-STORAGE-7](storage.md#dh-d-storage-7), [DH-D-STORAGE-9](storage.md#dh-d-storage-9), [DH-D-VIEW-4](session-views.md#dh-d-view-4), `DH-LIB-FFLATE`, `DH-D-UICORE-8`. [DH-D-SESSION-2](core-session.md#dh-d-session-2) and [DH-D-STORE-10](session-store.md#dh-d-store-10) (seed drafts with caller-given times, an assistant message from a foreign source, and a lookup by `imported_from`: the write path shared with the deferred upstream import), [DH-D-CLI-7](app-cli.md#dh-d-cli-7) (the command).
- **Outcome.** [DH-O-5](../design.md#dh-o-5). **Priority.** P2. **Done when.** DH-T-XINSPECT-12, DH-T-XINSPECT-13 and DH-T-XINSPECT-14 pass and [DH-E-XINSPECT-4](#dh-e-xinspect-4) has passed.

## 9. Acceptance

### 9.1 Local tests

`D-n` is short for `DH-D-XINSPECT-n`.

| Test ID | Proves | What is asserted |
|---|---|---|
| DH-T-XINSPECT-1 | D-1 | A composed test tree snapshots to a golden value: every fiber once, with state, entry, injections and effect labels; `MaxTreeNodes` truncates and says so; the walk runs no component code; no exported function of the package changes the tree |
| DH-T-XINSPECT-2 | D-1 | With A23's recording transport: every client from `egress` is observed, including one built before capture was enabled; secret headers, query values and released secret values are masked; no body without `Bodies`; with it, at most `MaxBodyBytes` and no growth for a slow reader; the ring evicts by count and bytes; the caller receives identical bytes |
| DH-T-XINSPECT-3 | D-1, D-2, D-3 | Every route of the area refuses a non-owner; with a bundle off its routes are not found; an import check shows that `x/inspect` cannot reach a sender and that no package holds a CDP or evaluation endpoint |
| DH-T-XINSPECT-4 | D-2 | Disabled entry: not found. Remote mode: `debug/remote-refused`. Local: a CPU capture parses as a profile; `seconds` is clamped; a second capture gets `debug/busy`; cancelling or unloading frees the slot; no package imports `net/http/pprof`; no command-line or heap-dump route exists |
| DH-T-XINSPECT-5 | D-3 | Golden HTML of the log page for a recorded session: grouping, both filters, jump to `seq`, page size; the cold part is read only for a detail request; rows read per page do not grow with the log; a session outside the caller's visibility is not found |
| DH-T-XINSPECT-6 | D-3 | `stepStats` passes A06's fold equivalence property; golden logs for a retry, a cancelled step, parallel tools (sum and union), nested script calls, compaction between steps and a steered turn; totals equal A06's `stats` and A04's `tokenUsage` |
| DH-T-XINSPECT-7 | D-3 | Keyless replay of a recorded session through the built binary: the statistics table equals a golden file; every step has a start time; paging by turn is stable while the session grows |
| DH-T-XINSPECT-8 | D-4 | Progress from weights (weight 3 done and weight 1 pending give 75%; splitting keeps the parent's share); limits; a fork keeps trees and discussions, starts a new report history and drops pending pause requests; export then import round-trips; importing the same file selects the existing tree unless a copy is asked |
| DH-T-XINSPECT-9 | D-4 | In a composed host through the executor: `todo_write` is absent and denied while `ReplaceTodo` is on and returns when the bundle goes off; a subagent has no planning tool; a reminder after `ReminderSteps`; one notice per priority change into or out of the top priority; a non-owner edit is refused; unload leaves no timer or goroutine |
| DH-T-XINSPECT-10 | D-5 | With the replay provider: the request sent equals the one rebuilt from `timeline/report-request`, which is flushed first; exactly one attempt for success, rejection, timeout, cancellation and restart; never more than `MaxConcurrent`; none while idle; an overdue report on resume; no call with `Reports` off; no second call after a rejected draft; usage is in `tokenUsage` |
| DH-T-XINSPECT-11 | D-6 | Through the executor and in keyless replay: another workspace's session is indistinguishable from a missing one; a symbolic link to the workspace is the same workspace; the caller's session and the calling step are excluded; a cursor round-trips; `session_search` accepts every upstream argument name, and its event windows (`event_seq_*`, `event_time_*`) exclude a session whose only match lies outside them while the creation window excludes by session; lineage markers carry no id; results stay within the pipeline bound; tools and section are absent with the bundle off |
| DH-T-XINSPECT-12 | D-7 | The pure converter on golden conversations: branch by `current_message_id` and by fallback; a filtered round dropped; an excluded reply ends its turn with a code; times never decrease; searches are text and no tool event exists; a file without bytes is a note; the title is normalised and pinned |
| DH-T-XINSPECT-13 | D-7 | With synthetic archives and directories: a limit stops inflation; duplicate names and an unsupported version are refused with nothing written; a failing conversation leaves no session and no membership; a second run skips by `imported_from` and `Copy` does not; cancellation keeps and lists published sessions; a second import gets `chatimport/busy`; the staging file is gone on every exit path; `Plan` writes nothing |
| DH-T-XINSPECT-14 | D-7 | Through the route fake: a non-owner is refused; a body over the limit is cut at the limit; the status region ends with the result, listing written sessions also after a failure |

### 9.2 Real-environment scenarios

<a id="dh-e-xinspect-1"></a>
#### DH-E-XINSPECT-1 In a real browser: tree, masked HTTP capture, a profile, the log and the statistics of a real session
- **Proves.** DH-D-XINSPECT-1, DH-D-XINSPECT-2, DH-D-XINSPECT-3.
- **Given.** A built `dsh` web host on loopback with a temporary home, the `devtools` bundle and its profile entry enabled, a real browser, one real model API key supplied at run time, and the Go toolchain on the test machine.
- **When.** (1) Send a prompt that makes two tool calls. (2) Open the tree page and disable one optional entry in settings. (3) Open the HTTP page. (4) Download a five-second CPU profile and open it with `go tool pprof`. (5) Open the session's log and statistics pages. (6) Restart the host on a non-loopback address with TLS and request a profile.
- **Then.** (2) The entry's row leaves the tree without a reload. (3) The model request is listed with its status and timing; the key appears nowhere in the page source. (4) The tool reads the profile. (5) The log shows every `seq` of the session's export once and in order; the totals row equals the usage the provider reported. (6) The request is refused with `debug/remote-refused`, while the tree and HTTP pages still answer the owner.
- **Cleanup.** Stop the host, delete the home and the downloaded profile. On failure keep the session export and the host log.

<a id="dh-e-xinspect-2"></a>
#### DH-E-XINSPECT-2 A real model plans with the task tree, survives a host kill, and a timed report is logged before it is sent
- **Proves.** DH-D-XINSPECT-4, DH-D-XINSPECT-5.
- **Given.** A web host with the `timeline` bundle on, `Reports` switched on and `Interval` set to one minute, a real model API key, a real browser, a recording proxy in front of the provider.
- **When.** (1) Ask for a task with at least four steps. (2) Kill the host during the second step and start it again. (3) Let the turn run past one minute. (4) Set a task to the top priority and ask for a pause in the panel.
- **Then.** (1) A tree and a progress strip appear; no `todo_write` call is made. (2) The panel shows the same tree, restored from the log. (3) The log holds `timeline/report-request` before one `timeline/report-attempt`, and the proxy saw exactly that request; the report appears in the panel. (4) The next model request carries one priority notice, and the tree shows the pause once the model records it.
- **Cleanup.** Stop the host and the proxy; delete the home. Keep the export if an assertion fails.

<a id="dh-e-xinspect-3"></a>
#### DH-E-XINSPECT-3 A real model finds earlier work in its own workspace and nothing outside it
- **Proves.** DH-D-XINSPECT-6.
- **Given.** One home with the `session-recall` bundle on; two finished sessions in workspace A that mention a planted phrase and one in workspace B that mentions it too; a real model API key.
- **When.** In a new session in workspace A, ask the model what was decided earlier about the phrase.
- **Then.** The log shows a `session_search` call whose result names the two sessions of workspace A and not the one in B; a follow-up `session_event_read` returns an event of one of them; asking the model to read the session in B by id returns the same error as an unknown id.
- **Cleanup.** Delete the home.

<a id="dh-e-xinspect-4"></a>
#### DH-E-XINSPECT-4 A real export from the vendor's chat website is imported twice and one conversation is continued
- **Proves.** DH-D-XINSPECT-7.
- **Given.** An archive the tester exported from their own account on the vendor's chat website, supplied at run time and never committed; it holds at least one conversation with an image, one with a web search and one with a regenerated reply. A web host with the `chat-import` bundle on, a real browser, a real model API key.
- **When.** (1) Run `dsh session import-chat --dry-run` on the archive. (2) Import it through the web form into a workspace. (3) Open three imported sessions. (4) Send a message in one. (5) Run the command without `--dry-run` on the same archive. (6) Start an import and kill the host half-way, then restart.
- **Then.** (1) A plan is printed and the home is unchanged. (2) The dialog reports the count; titles and dates match the website. (3) The image shows, the search shows as links, the regenerated conversation shows the branch the website showed. (4) The model answers with the imported history as context. (5) Nothing new is written and the result counts every conversation as already imported. (6) No half-written session is listed, the staging file is gone or is removed by the next retention run, and a re-run completes the rest.
- **Cleanup.** Delete the home and the staged copies. The archive is the tester's private data: delete it from the test machine.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Assumed of other documents (requests sent), as clauses, not as their interfaces.** *Cordis port, settled:* the accessor is [DH-D-CORDIS-11](../libs/cordis.md#dh-d-cordis-11) (`Fiber.Children`, `Fiber.Provides`). *A23:* `egress` accepts one transport wrapper as an effect, applied to every client it builds, and passes the purpose. *Settled:* A18's control endpoint takes no registered method, so the `dsh debug` commands are dropped and the pages remain; an upload route sets its own `Route.Limits` (A19). *A01 and A05, settled ([core-session.md §6.3](core-session.md), [session-views.md](session-views.md)):* `CreateOptions.Seed` takes drafts with their own times and a header creation time; a seeded assistant message names a foreign source and has no usage or digest; `Store.Stat` returns `imported_from`; an event type declares its own request builder for `RequestFrom`, so `timeline/report-request` declares one. *A24, settled:* the one command is this area's `cmdline.Command`. *A27:* four optional bundle rows (`devtools`, `timeline`, `session-recall`, `chat-import`). *C4, C5:* addresses by `seq`; whether the trajectory's overview reads `stepStats`.
2. **The brief for this area called Timeline a view of history and token use.** Upstream's Timeline is a planning feature with model tools and background model calls (`timeline/README.md:103-107`). It is designed here as such; only the statistics table is a view.
3. **Recommended default, not confirmed by the owner.** Timeline reports are off until switched on ([DH-ADR-XINSPECT-5](#dh-adr-xinspect-5)); upstream turns them on with the bundle. **Not decided here.** Whether imported reasoning is sent to a model on resume is A04's cross-provider history rule.
4. **Unmeasured.** The cost of the `stepStats` fold per append, the memory of a full capture ring with bodies, and import speed for a large archive.
5. **Limits kept.** The seven carried rows of [§5](#5-tech-debt-and-its-resolution). Capture does not see child-process traffic. Masking is by name and known value. Profiles are refused on a remote host. The importer reads one generation of a format the vendor may change without notice.

## 11. Units dispositioned by this spec

68 assigned units, all with a row in `trace/fragments/X3.jsonl`: 8 `UP-PKG` (3 ported, 3 replaced, 2 merged), 49 `UP-DEBT` (32 resolved, 10 not-applicable, 7 carried), 5 `UP-TOOL` (ported, three of them changed), 1 `UP-EVT` (replaced), 4 `UP-CFG` (3 ported, 1 replaced), 1 `UP-SVC` (ported). The same fragment holds final verdicts for the 2 `UP-DEC` notes (1 replaced, 1 ported) and declares 7 decisions (`DH-ADR-XINSPECT-1` to `-7`), 7 deliverables (`DH-D-XINSPECT-1` to `-7`), 14 tests (`DH-T-XINSPECT-1` to `-14`) and 4 scenarios (`DH-E-XINSPECT-1` to `-4`).
