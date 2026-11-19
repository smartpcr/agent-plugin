# Upstream decisions, part 2: architecture notes

> **Status: design only, first verdicts.** This document classifies 116 of upstream's
> 464 current decision notes. Nothing described here was built, run or tested. Upstream
> facts are read at the commit recorded in [../README.md](../README.md) and cited as
> `path:line` relative to the upstream root.
>
> The verdict column is the first classification, made before the owner's decisions of
> 2026-11-18 ([../design.md](../design.md) §7.1) and before the area specifications
> existed. The final disposition of each note is the ledger row written by the area
> that owns it ([../trace/README.md](../trace/README.md)); where that row differs from
> a verdict here, the ledger governs. The sections "Decisions that exist only because
> of JavaScript or npm" and "Open questions for the area owners" were brought up to
> date with the owner's decisions on 2026-11-19.

## What an agent note is

Upstream deepseek-harness writes down each lasting design decision in a Markdown file it
calls an **Agent Note**. A note "records a decision or proposal that affects this
codebase" and holds the reasons that code cannot carry (`.agents/notes/README.md:5`).
The path of a note says two things about it (`.agents/notes/README.md:9`):

- **Lifecycle**, the top-level folder. `implemented/` means the decision shipped,
  `proposed/` means it was reviewed but is not built or only partly built, and
  `rejected/` means it was considered and declined (`.agents/notes/README.md:12-14`).
- **Class**, the nested folder: `feature`, `bug-fix`, `simplification`, `architecture`,
  `process` or `testing`. The Go ledger calls the class the note's *category*.

All 116 notes in this part are in the `architecture` class, which upstream defines as a
structural decision about the shipped source (`.agents/notes/README.md:30`). Tooling and
workflow decisions go in the `process` class instead (`.agents/notes/README.md:31`).
That is why only one row below is `not-applicable`. The notes here are dated 2026-09-16
to 2026-10-29: 113 are implemented upstream and 3 are proposed.

The notes matter because they give the reason behind upstream's structure, which the
code alone does not. [DH-ADR-2](../design.md#dh-adr-2) makes upstream the source of
requirements and not a contract, so each decision must be judged: keep it, reach the
same goal another way, or drop it. [DH-ADR-6](../design.md#dh-adr-6) requires that
verdict for every note.

## How to read the tables

Each row is one note.

| Column | Meaning |
|---|---|
| # | Position of the note in `trace/assignments/D2.jsonl`. It is only a handle for cross-references inside this document. |
| Note | The note's title, linked to the note at the pinned upstream commit. |
| Summary | The decision in one or two plain sentences, read from the note's Decision (or Proposal) section. |
| Kind | What sort of decision it is, judged from its content: product architecture, product feature, bug fix, simplification, testing practice, development process or documentation. This is independent of upstream's class. |
| Area | The upstream path the decision is mainly about. "(cross-cutting)" means it applies across the product. |
| First verdict | The ledger disposition proposed for the Go redesign, with any global decision it rests on. |
| Why | The reason for the verdict. |

The verdicts are the ledger dispositions defined in [../trace/README.md](../trace/README.md):

| Verdict | Meaning here |
|---|---|
| **ported** | The Go design should make the same choice, expressed in Go. |
| **replaced** | The goal stands, but the Go design reaches it by a different mechanism. The link names the global decision that forces the change: [DH-ADR-2](../design.md#dh-adr-2) (upstream file formats and wire contracts need not be kept), [DH-ADR-3](../design.md#dh-adr-3) (HTML rendered by the Go host instead of the React client and Electron shell) or [DH-ADR-4](../design.md#dh-adr-4) (compiled-in components and plugin processes instead of npm plugin loading and in-process hot reload). |
| **dropped** | The Go design should not do this. The link names the decision that explains why. |
| **not-applicable** | The note exists only because of JavaScript, Node or npm, or is about upstream's own development process. This verdict is final. |

**These are first verdicts.** The agent that designs the affected area writes the final
row and links it to the deliverable or decision that carries it. A `ported` row without
a link is therefore expected here, not an omission.

Three rules were applied the same way to every row:

1. **Summary is upstream fact, Why is judgement.** The Summary column restates the note.
   The Why column is this document's reasoning. Where it says what the Go design would
   do, that is inference, not something read from upstream.
2. **Behaviour versus client structure.** About a quarter of these notes are about the
   web client or the desktop shell. A note that fixes *what the product does* (for
   example "closing the window hides it and the host keeps running") is `ported`, even
   though the code that does it moves from the browser or Electron into the Go host.
   When these verdicts were written the design still had a Go desktop shell; the owner
   has since removed the desktop application
   ([DH-ADR-18](../design.md#dh-adr-18)), so a desktop behaviour is now ported to the
   web UI or the command line, or dropped on record, by area A25
   ([../areas/app-web.md](../areas/app-web.md)). A note that fixes *how the React client or Electron is built inside*
   (component identity, slots, client stores, DOM retention, Electron storage
   partitions) is `replaced` with a link to DH-ADR-3: the goal remains and the mechanism
   does not exist in server-rendered HTML.
3. **Proposed notes.** For a note that upstream has not built, the verdict answers
   "should the Go design adopt the idea?". `ported` means adopt it.

### Terms used in the summaries

| Term | Meaning |
|---|---|
| Host | The upstream server process that runs agents, tools and storage. The browser client and the desktop shell talk to it. |
| Profile | A named composition of plugins and their configuration that a host boots, for example `web` or headless. It is stored as loader configuration plus patch files. |
| Bundle | A package that adds a group of plugins to a profile through a configuration patch. An *optional bundle* is one that is off until the user selects it. |
| Preset (agent preset) | A named list of plugins from which one agent is composed, for example `standard` or `minimal`. |
| Loader | The cordis component that turns configuration into running plugins and reconciles them when the configuration changes. |
| HMR | Hot module replacement: upstream reloads changed plugin code inside the running host. |
| Remote, Typert | Typert is upstream's generator that turns TypeScript method signatures into typed calls between the client and the host. One such method is a *Remote*. |
| Peer, principal | A peer is one caller connection to the host. Its principal is the identity that permission decisions read. |
| Projection | A value folded from a session's event log, such as its title or its token count. The *projection cache* saves these values so that a session list need not replay every log. |
| Request header | The event that records what one model request was assembled from, including the active tools. |
| PTC | The mode in which the model writes a program that calls tools, instead of making one tool call per step. A *PTC runtime* is the child process that runs those programs. |
| CUA | Computer use: the agent operates desktop applications through a native helper process (screenshots, accessibility and input). |
| Slot, Factory | The React client's two extension points: a Slot is a named place where plugins put components, a Factory is a named reusable component. |
| Island | A small self-contained piece of JavaScript inside a server-rendered page ([DH-ADR-3](../design.md#dh-adr-3)). |

## Counts

By first verdict:

| Verdict | Implemented | Proposed | Total |
|---|---:|---:|---:|
| ported | 79 | 2 | 81 |
| replaced | 31 | 1 | 32 |
| dropped | 2 | 0 | 2 |
| not-applicable | 1 | 0 | 1 |
| **Total** | **113** | **3** | **116** |

Of the 32 `replaced` rows, 23 rest on DH-ADR-3 (the user interface), 8 on DH-ADR-4 (plugins and packaging) and 3 on DH-ADR-2 (formats and wire contracts); 2 rows rest on two of them. Both `dropped` rows rest on DH-ADR-2.

By kind of decision:

| Kind | Notes |
|---|---:|
| product architecture | 73 |
| product feature | 30 |
| simplification | 7 |
| bug fix | 6 |
| testing practice | 0 |
| development process | 0 |
| documentation | 0 |
| **Total** | **116** |

## Category: architecture

All 116 notes are in upstream's `architecture` class. They are listed in two tables by
lifecycle, in the order of the assignment file, which is upstream's date order.

### Implemented upstream (113 notes)

| # | Note | Summary | Kind | Area | First verdict | Why |
|---:|---|---|---|---|---|---|
| 1 | [Creator mode installs persistent plugin bundles][n1] | The agent can install a plugin bundle (a package plus a config patch) mid-conversation through the existing `plugin_manager` tool, in the Creator and Standard presets. Every call needs the full-access sandbox mode or a one-call approval, because a profile change can load code with host permissions and affects other sessions. | product feature | `packages/boot/plugin-manager` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | The goal stands: an agent adds a capability and uses it in the same conversation, behind an explicit approval. In Go a bundle cannot be an npm package loaded into the host; it is a plugin process or an MCP configuration entry, so the install path and the risk being approved both change. |
| 2 | [Experimental voice input with explicit recognition providers][n2] | Voice input is an optional, default-off bundle with an explicit recognition provider chosen by id and no fallback between providers. Recordings and transcripts stay transient: recognition only inserts text into the draft and never starts a turn or writes a session event. | product feature | `packages/experimental/speech-to-text` | **ported** | The rules are about user control and privacy, not about JavaScript: no silent provider fallback (a local failure must not upload speech to a cloud service), no resources allocated until used, and late results discarded when the draft or session changed. The recording widget is a browser island under DH-ADR-3; the provider registry is a Go component. |
| 3 | [Host-owned scheduled messages][n3] | Scheduled messages live in a host-owned storage domain, not in a session, so they survive restart and fire while the target session is not loaded. The store is authoritative: an unreadable or invalid file is a load failure and is never read as 'no tasks'. | product architecture | `packages/schedule/schedule` | **ported** | A timer attached to a live agent cannot wake a session that is not loaded, in any language. The fail-closed load rule prevents a corrupt file from silently deleting reminders. The version 1 read compatibility in the note is not needed (DH-ADR-2). |
| 4 | [User-terminal permissions][n4] | The sidebar terminal a person types into runs with the operating-system user's own permissions, not inside the agent's sandbox, and asks for no agent approval. Changing the agent's sandbox mode leaves user terminals running, and their input and output are not model input. | product feature | `packages/api/terminal-controller` | **ported** | The separation between what the person may do and what the agent may do is a product rule. Without it a person must widen the agent's sandbox to do manual work. The subprocess provider's scrubbing of credential environment variables still applies. |
| 5 | [Account provider and active request cancellation][n5] | The API-key route and the account route are separate provider routes, each with exactly one credential source and no fallback to the other. Signing out cancels every running agent whose latest logged request used the account route, keeping their inboxes. | product architecture | `packages/llm/llm-deepseek-account` | **ported** | Falling back between credentials would silently change who is billed. Reading the route from the request already logged in the session avoids a second 'current provider' field that can drift. |
| 6 | [Developer messages for incremental session changes][n6] | A change to the set of tools the model can see is recorded in the session as a structured `developer/message` event, in conversation order. An addition stores only the tool name and points at the earlier request header that holds its schema. | product architecture | `packages/core/session` | **ported** | Providers can only keep the cached conversation prefix if tool changes are replayed exactly, including after restart and fork; free-form reminder text would lose the tool identity. The Go event format may differ (DH-ADR-2) but the record and its strict reference checks are needed. |
| 7 | [Native CUA objects independent of evaluator ownership][n7] | The shared computer-use (CUA) library owns application and window objects, consent and logged dispatch; the macOS package owns the Swift helper and its connection. The object API has no interpreter of its own: it takes a dispatcher, so every operation goes through the normal tool path for authorization, logging and image admission. | product architecture | `packages/experimental/cua` | **ported** | Keeping the object API free of an evaluator means variable persistence and cancellation are decided once, by the execution host. The Swift helper is already a separate native process, so a Go host talks to it the same way. |
| 8 | [Mandatory native V4 relationship validation][n8] | Every read of a stored session runs one mandatory pass that checks relationships between events (turn and step order, tool calls and their results, retry chains, compaction spans, the protected system head) before the session is exposed. An unfinished tail may stay open, but a closing event must settle what it closes. | product architecture | `packages/session/session-format` | **ported** | An event can be well-formed by itself and still contradict earlier events; checking on read stops a damaged log from reaching the agent loop. The Go format needs the same single validation pass. The V3 and V4 history and the JavaScript prototype-property check do not carry over (DH-ADR-2). |
| 9 | [Recorded user-source attribution compatibility][n9] | A producer may add a new 'attribution-only' kind to the source field of persisted user messages without a session format version bump, under a recorded compatibility policy checked against schema fingerprints. Prompts that are only sent to the model and never stored use a separate input type with no id or source. | product architecture | `packages/core/session` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2)) | The goal stands: adding an attribution label must not break older readers, and request-only prompts must not pose as session messages. The mechanism is tied to upstream's TypeScript union fingerprints and its released formats; the Go design states its own forward-compatibility rule (readers keep unknown source kinds) in its event schema. |
| 10 | [Writer acquisition for blank Session reuse][n10] | Opening a workspace reuses an existing blank session only after the host has acquired that session's writer lock; if another host holds the lock it creates a new session. The agent keeps the lock for its lifetime, and no process id is persisted. | product architecture | `packages/api/session-controller` | **ported** | Two hosts sharing one storage directory can otherwise pick the same blank session. Acquire-then-use closes the race that check-then-release leaves open, and it does not depend on the runtime. |
| 11 | [Standalone schedules and Session targets][n11] | A schedule delivers to one of three targets: an existing session, a session created once at first delivery, or a new session per delivery. Session ids are reserved before each create attempt, so a retry after a failed delivery reuses the same session. | product feature | `packages/schedule/schedule` | **ported** | Reserving the id before the attempt is what makes a delivery retry safe (no duplicate sessions). The target model and the title rule for created sessions are product behaviour. |
| 12 | [Binary workspace file transfer][n12] | File previews receive raw bytes instead of base64 text: one `readBytes` Remote method with an optional byte range, with the generated RPC codec carrying byte buffers as multipart attachments. | product architecture | `packages/api/workspace-files` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The goal (no base64 overhead, ranged reads, one authorization path) stands. With a server-rendered UI the browser fetches file bytes over ordinary HTTP with a Range header from the Go host, which already shares routing, session lookup and error handling; the Typert byte codec is not needed. |
| 13 | [Declarative Agent presets and retained revisions][n13] | An agent preset is ordinary loader configuration: an identity, display data and a list of child plugins. A registry owns each preset revision; running agents keep the revision they started with until they are disposed, and a preset that fails to activate stays listed but rejects new agents. | product architecture | `packages/preset/agent-preset-registry` | **ported** | Presets as loader configuration remove a second configuration system, and retained revisions let a preset be edited while agents run. In Go the child list names compiled-in components (DH-ADR-4), which fits the existing Go loader. |
| 14 | [Plugin install registries][n14] | Plugin installs ask an ordered plan of npm registries (the configured one, then mirrors such as npmmirror), so installs work where registry.npmjs.org is unreachable. A private registry is always asked alone and never falls back to a public one. | product feature | `packages/boot/plugin-manager` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | The mechanism is pnpm and npm registries, which do not exist in Go. Two goals carry to whatever source the Go design downloads plugin processes from: a configurable mirror for networks that cannot reach the default, and no fallback from a private source to a public one (a same-named public package would be someone else's code). |
| 15 | [Volatile configuration references][n15] | A config field declared 'volatile' can change without restarting its plugin: consumers hold a stable reference and read the current value at use. The loader compares configs by schema; only when nothing but volatile fields changed does it keep the instance and emit an update event. | product architecture | `vendor/loader` | **ported** | Restarting a component to change one value disposes its services and effects. The Go loader needs the same split between fields that require a remount and fields that are read live; this is a gap to close in the Go cordis port (DH-ADR-5). |
| 16 | [Bind one concrete Web address][n16] | The web listener binds exactly one concrete IPv4 or IPv6 address, loopback by default, and rejects wildcard addresses such as 0.0.0.0. The Host-header check accepts the bind address itself; any other name must be listed as a trusted host. | product architecture | `packages/host/webserver` | **ported** | A wildcard bind exposed every interface and made the host infer trust from sampled LAN addresses. One explicit address keeps configured trust and actual reachability the same thing, which matters equally for a Go listener. |
| 17 | [Keep live configuration in the owning profile][n17] | Every configurable value is declared once, in its plugin's config schema. The settings page only enumerates the volatile fields and writes edits to the active profile's patch file through normal loader reconciliation. An edit that a higher-priority overlay would shadow fails before it is saved. | product architecture | `packages/settings/settings` | **ported** | One owner for schema, default, storage and change notification removes the duplicate settings registrations upstream had. Refusing shadowed edits avoids a save that appears to work and changes nothing. |
| 18 | [Resolve profile modules along the ancestor chain with an interception layer][n18] | Defines how Node resolves a module imported from inside a profile: Node's normal ancestor `node_modules` search, plus one interception that maps declared peer dependencies of linked packages to the host's runtime copy. | product architecture | `packages/boot/app-boot` | **not-applicable** | Exists only because of Node module resolution and the npm package layout. Go components are linked at build time and plugin processes share no modules with the host (DH-ADR-4), so there is no lookup order to define. |
| 19 | [Projection cache read-only face matches the lifecycle identity; the client store separates cached and sequenced rows][n19] | The projection cache (saved per-session derived values such as the title) has two read paths: a listing path keyed only by facts in the session header, and a fold path that also compares the fork cut. The client keeps cached rows apart from rows that carry a real sequence number. | bug fix | `packages/session/session-projection-cache` | **ported** | After a restart forked sessions showed no title, because the listing could not supply the fork cut that the cache key demanded. The identity split is host logic and ports. The rule that a cached value never replaces a sequenced one also holds for a server-rendered list, where the host applies it. |
| 20 | [Remote duplex stream: one kind of stream, an uplink channel, and an invocation context][n20] | Every Remote stream is bidirectional over one multiplexed WebSocket: the method's return type declares the downlink and uplink item types, and the host method reads the uplink and the caller identity from an invocation context. Reconnect and resume are left to higher layers. | product architecture | `packages/api/gateway` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3), [DH-ADR-2](../design.md#dh-adr-2)) | The Go UI receives live updates over server-sent events and sends input as ordinary HTTP requests, so a generated duplex RPC stream is not its transport. What carries: one kind of stream, an explicit per-invocation context (caller identity, cancellation signal), and resume owned by the layer above. The terminal island and the SDK may still need a duplex channel, which the API area must decide. |
| 21 | [Timed user questions as two settlements][n21] | A question to the user with a timeout settles twice: the tool call returns 'pending' when the timeout ends so the agent can continue, and a later answer arrives as a durable user message. A pending result is neither a skipped answer nor an approval. | product feature | `packages/interaction/user-questions` | **ported** | It stops a question from blocking work that does not depend on it, while keeping the question answerable after the agent moved on or the session was reopened. This is agent-loop and interaction behaviour, independent of the client. |
| 22 | [Window drag coverage as a contract][n22] | On the macOS desktop app, which parts of the window can be dragged is declared once: each chrome row marks itself in markup, one CSS rule makes marked rows draggable, and tests run an executable model of the drag-region rule against a manifest of rows. | product architecture | `packages/client/web` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The duplication it removes came from React packages each hand-writing drag regions. With server-rendered templates there is one shell layout, so the single-declaration goal is met by construction; the Go desktop shell has its own drag-region convention that the desktop spec must name. |
| 23 | [Keep shortcut preferences device-local and commit before activation][n23] | Keyboard shortcut overrides are stored per device (browser storage on the web, a shell-owned file on desktop), not in the host. A new binding takes effect only after it was saved, and unreadable or newer-version preference data is never overwritten. | product architecture | `packages/client/shortcuts` | **ported** | One host serves several devices with different operating systems and browser restrictions, so bindings cannot be host state. Commit-before-activate and never-overwrite-unreadable are storage rules that hold for the Go desktop shell and the browser island alike. |
| 24 | [Active tool headers and stateful request projection][n24] | Each model request header records the assembled active tools. The loop compares names with the previous header and logs one developer message with additions and removals; the session caches a fold of this tool history that requests and compaction reuse. | product architecture | `packages/core/session` | **ported** | Plugin and MCP tools change mid-conversation, and providers differ in how they accept that. A capability-independent history lets each provider adapter project it its own way without re-declaring or dropping tools. It is needed in Go because plugin processes come and go the same way. |
| 25 | [Stable Sidebar tab mounting and Session retention][n25] | A sidebar page that holds a live embedded browser keeps the same DOM ancestors through session switches, tab selection, pane moves, floating and docking, because Electron's webview resets its guest when it is disconnected from the document. | product architecture | `packages/client/ui-sidebar-right` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The goal stands: an embedded page must not reload when the user switches tab or session. The mechanism is specific to React re-parenting and Electron's webview element. In the Go UI the equivalent rule is that htmx swaps never replace the container of a retained widget, and the embedded browser depends on what the system webview offers. |
| 26 | [Definition-owned grouping with generic Node/Group assembly][n26] | Grouping chat content into expandable groups is owned by registered group definitions; the builder and assembler stay generic. All three display densities share one grouping result, so that switching density does not rebuild React component instances. | product architecture | `packages/client/ui-conversation` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The separation (business grouping rules apart from a generic assembler) is worth keeping in the Go view model. The instance-identity constraints exist only for React; server-rendered HTML computes groups in the host and renders them. |
| 27 | [Desktop close shortcuts follow page focus][n27] | The close shortcut closes the focused sidebar page; only when no page can close does it close the desktop window. A stale target never falls back to another page or to the window. | product feature | `apps/desktop` | **ported** | This is user-visible desktop behaviour, and on Windows closing the last window stops the host, so the no-fallback rule protects running work. The Go desktop shell (DH-ADR-3) implements it with its own menu and window calls. |
| 28 | [Ship experimental capabilities as optional bundles][n28] | Experimental and optional capabilities are offered as default-off 'optional bundles' on the plugins page, each with an icon and a localized title. Their dependencies ship with every installation, so a bundle is admitted only if it adds no heavy runtime and works without extra configuration. | product architecture | `packages/util/official-bundles` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | The goal stands: experimental capabilities are visible and one switch away, not hand-installed. In Go every first-party capability is already compiled in, so an optional bundle is a named, default-off configuration group; the admission test becomes binary size and external executables, not npm download cost. |
| 29 | [Fatal diagnostics and Desktop crash reports][n29] | An uncaught error is fatal: the host writes one full diagnostic, runs its release hook under a timeout and exits, never resuming the failed operation. Output spill files are best-effort, the desktop app writes a crash-report file before showing its fatal dialog, and a client script batch that failed in transport is retried once but never re-executed. | product architecture | `packages/boot/app-boot` | **ported** | Only the failing code knows which state is still valid, so continuing turns a visible crash into silently wrong output. Go's default for a panic in a goroutine is already to exit; the design must add the full diagnostic, the bounded release and the crash-report file. The client batch-retry part has no counterpart (DH-ADR-3). |
| 30 | [Account-scoped Platform browser storage][n30] | The embedded Platform page gets browser storage partitioned by a hash of the origin and the account id, so dismissed notices persist per account. Opening a partition clears everything except localStorage, and a disposable partition is used while the account id is unknown. | product architecture | `apps/desktop` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The goal stands: page preferences persist per account and no credentials are left behind. Electron session partitions are the mechanism; a system webview hosted by a Go shell has different, per-platform storage controls, so the desktop spec must restate this with what the chosen shell supports. |
| 31 | [Schedule deletes task rows and bounds delivery history][n31] | Deleting a schedule removes its row and its delivery records; there is no 'deleted' state and no recycle bin. Delivery history is bounded by age (default 30 days) and count (default 200), applied on the only write path. | simplification | `packages/schedule/schedule` | **ported** | Soft delete added a state every reader had to skip, and unbounded history made each rewrite of the task grow. Both rules are storage design, independent of language. |
| 32 | [Tool calls own preparation, dispatch, and results by callId][n32] | A tool call is shown in three stages keyed by its call id: 'preparing' as soon as the model streams a tool name, 'start' when the complete call arrives, 'result' when the result arrives. The preparing stage is display only and changes no session event and no dispatch timing. | product feature | `packages/client/ui-chat` | **ported** | Without it the UI shows no activity for the seconds a model spends streaming a long argument such as file content. The three-stage model moves into the Go host's view of the conversation and is pushed to the page over server-sent events. |
| 33 | [Node PTC execution modes and in-memory state][n33] | The runtime for PTC programs (PTC is the mode in which the model writes a program that calls tools) has two modes: 'per-call' starts a fresh process each time; 'persistent-in-memory' keeps one process per owning agent so variables survive between calls. State kept across restarts ('durable') is explicitly not offered. | product architecture | `packages/ptc-runtime/ptc-runtime-node` | **ported** | Interactive automation needs object references to survive between model calls, and tying the state to an explicit owner gives it a clear release point. The runtime is already a child process, so the Go host can supervise it the same way; which JavaScript engine runs the programs is for the PTC area to decide. |
| 34 | [Hide the Desktop window on close and confirm interruptible quits][n34] | Closing the desktop window hides it and leaves the host running; Windows gets a tray icon and a first-close notice. Quitting asks the host whether agents, jobs or scheduled reminders would stop, and shows a confirmation only when they would. | product feature | `apps/desktop` | **ported** | It prevents losing the current page state on close and cutting running tasks on quit without warning. The behaviour is independent of Electron; the Go shell (DH-ADR-3) needs a tray icon, a native message box and a host query for active work. |
| 35 | [Native HTTPS on the Web listener][n35] | The web listener can serve HTTPS itself from a certificate file and a key file. Invalid or mismatched key material fails start-up with no fallback to HTTP, and the listener's actual protocol, not forwarded headers, decides the `Secure` cookie attribute. | product feature | `packages/host/webserver` | **ported** | A directly reached listener must be able to encrypt launch tokens and cookies without a reverse proxy. Go's standard library serves TLS directly; the fail-closed rule and the rule that TLS grants no extra authority carry unchanged. |
| 36 | [Bounded DeepSeek Session-log upload][n36] | The session-log upload attached to model requests is capped (8 MiB by default): each request carries the longest run of events after the acknowledged watermark that fits. An event too large to fit blocks the upload with a warning but never fails the model request. | bug fix | `packages/session/session-log-deepseek` | **ported** | An unbounded first upload could exceed the JavaScript string limit and then fail on every retry. That limit is specific to the V8 engine, but an unbounded request body is a problem in any runtime, and the rule that an optional extension field must not fail the base request is worth keeping. |
| 37 | [DeepSeek account models come from the account listing and gate by client version][n37] | On the account route the model list comes from the account service, loaded lazily per credential, refreshed in the background and falling back to the last good list or the built-in catalog. A listed model is offered only if its declared minimum client version is met; unknown models without that declaration stay hidden. | product feature | `packages/llm/llm-deepseek-account` | **ported** | New models reach users without a release, and an old build cannot request a model it cannot drive. Inference: the Go client needs its own identity and version in that gate, which is a question for the provider area. |
| 38 | [Incremental tool-argument scanning and argument order during preparation][n38] | While a tool call's arguments are still streaming, a lazy scanner indexes top-level keys and reads only the fields the display asks for, without parsing the whole JSON. Tool schemas put short descriptive fields before long ones so that they arrive first. | product architecture | `packages/util/values` | **ported** | The preparing stage needs a path or a description before the full argument exists, and re-parsing a growing JSON text on every delta costs quadratic time. In Go the scanner runs in the host that renders the row; the argument-order rule is about model behaviour and carries as is. |
| 39 | [Preset-scoped Schedule tools][n39] | The model-facing schedule tools are a separate plugin mounted per agent preset; the schedule service itself stays host-wide. A preset that does not mount the tools pays no prompt tokens for their schemas. | product architecture | `packages/schedule/tool-schedule` | **ported** | Registering tools from the service attached them to every root agent, including the minimal preset. Splitting a host service from its model-facing consumer is a pattern the Go composition should follow for every capability. |
| 40 | [Preset-scoped time context][n40] | The plugin that injects the current time into the conversation is mounted only in presets that have the schedule tools; the minimal preset gets none. | product architecture | `packages/context/time-context` | **ported** | A context injection belongs to the preset that consumes it; otherwise every step in every preset pays for a message that nothing uses. This is a composition rule, not a mechanism. |
| 41 | [Delivered reminders are framed as scheduled messages from the user][n41] | A delivered reminder is a user-role message that opens with a fixed line saying it is a scheduled message from the user. The earlier wording that told the model to treat the payload as untrusted was removed. | product feature | `packages/schedule/schedule` | **ported** | A message cannot credibly declare its own content untrusted, and the model may have written the prompt itself. Stating only the origin is accurate. Prompt wording is independent of the implementation language. |
| 42 | [Shared time vocabulary for wall-clock selectors][n42] | One utility package owns the time vocabulary accepted at boundaries: strict instant and zone parsers, durations, and bounded cron and recurrence-rule (RRULE) values. Parsers return 'accepted' or 'rejected' with a stable reason code, and each caller maps the reason to its own error text. | simplification | `packages/util/time` | **ported** | Three packages had grown private, slightly different parsers. One Go package with typed reasons gives the same result; part of it is covered by the standard `time` package, the recurrence rules are not. |
| 43 | [Session-log OTLP strings and bounded requests][n43] | Session-log telemetry goes through a shared telemetry (OTLP) plugin with its own queue bounded by bytes and by count. Each record is measured once after serialization; one oversized record is reported and skipped without blocking later ones. | product architecture | `packages/telemetry/otel` | **ported** | Count-only batching cannot bound a request once large tool results are JSON-escaped. Byte-bounded batching, and the rule that one bad record does not block the queue, apply to a Go exporter as well. |
| 44 | [Application consent for native Computer Use][n44] | The computer-use executor asks the user for consent per target application before any operation on it, using the user-questions service, and stores grants per profile. This consent is separate from general tool approval and from operating-system privileges. | product architecture | `packages/experimental/cua` | **ported** | macOS privileges authorize the helper, not the user's choice of application, and retained window objects can outlive a decision. Enforcement at every dispatch is a security rule that must hold in Go. |
| 45 | [Progressive Office skills with ordinary shell execution][n45] | Office guidance is a compact router skill plus three format-specific authoring skills that the model loads by name, so ordinary editing does not pay for rendering instructions. Rendering uses a private, randomly named temporary directory in the workspace that is removed afterwards. | product feature | `packages/skill/skill-office` | **ported** | Progressive loading is a prompt-budget decision, and the skills are data files. The note's mounting and shell-execution parts are superseded by later Office notes; one of them, 'Office skills in every deployment', is in this batch. |
| 46 | [Offer web_search and web_fetch only while the selected provider is ready][n46] | The web search and web fetch tools are offered to the model at each step only if the selected provider has a credential for the current model route. The check reads configuration only and makes no network call. | product feature | `packages/web/tool-web` | **ported** | Otherwise a session is offered a tool whose every call fails with a credential error. A per-step readiness check on the provider interface is cheap and language-neutral. |
| 47 | [Open the Session when migration is required][n47] | Background actions (fork from the session list, listing projections) do not trigger a session format migration. They check the stored format first and, if migration is needed, refuse and offer to open the session, which is the only action that migrates. | product architecture | `packages/api/session-controller` | **ported** | A migration is slow and writes a new generation, so it should follow an explicit open. The Go format starts at one version (DH-ADR-2), but the rule should be in place before its first migration exists. |
| 48 | [Refresh runtime resolution after package operations and expire HMR package configuration][n48] | After a plugin package is installed, enabled, disabled or removed while the host runs, the host publishes a new module-resolution generation, and hot reload expires cached package manifests. | product architecture | `packages/boot/app-boot` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | The goal stands: the running host follows plugin installs and removals without a restart. The mechanism is Node's module and package-configuration caches. In Go, installing or removing a plugin starts or stops a plugin process, and there is no cache to refresh. |
| 49 | [Initial draft content and structured reference restoration][n49] | Starting a session can prefill the composer with a prompt, with an explicit rule for when a draft already exists (keep, replace or append). Drafts keep structured references (file, folder and session chips), and the initial content is in place before the input is first read. | product architecture | `packages/client/ui-conversation` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The prefill options and the rule that prefilling never sends are product behaviour to keep. The mechanism (input model objects, React mount order) is client-internal; in the Go UI the draft document lives in the rich-text island and the host supplies the initial content with the page. |
| 50 | [Replace plugins whose loaded package files changed during profile recomposition][n50] | When a profile is recomposed, plugins whose loaded package files changed on disk are evicted from Node's module cache and restarted; all recomposition goes through one exclusive path. Installing over an installed package is refused, so an upgrade is a removal followed by an install. | product architecture | `packages/boot/hmr` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | Node caches a module by file URL, so an upgraded package would keep running old code. A plugin process is replaced by stopping it and starting the new binary, which removes the cause. One exclusive recomposition path is still the right shape for the Go loader. |
| 51 | [Anonymous translation remains separate from model history][n51] | Translating the model's reasoning for display is an optional bundle with an explicitly chosen provider (Bing by default, or Google) and no fallback. Translated text never replaces stored assistant output and never enters model input. | product feature | `packages/experimental/translator` | **ported** | Enabling the bundle is consent to send reasoning text to the chosen service, so a failure must not pick another receiver. Keeping translation outside model history is a correctness rule. The display wrapper is rendered by the host (DH-ADR-3). |
| 52 | [Discover Official bundles without installing their runtimes][n52] | The Claude Code and Codex subagent integrations appear in an installation-owned catalog with embedded metadata and icons, so they can be discovered offline, but their runtimes are installed only when the user selects them. | product feature | `packages/util/official-bundles` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | The goal stands: heavy integrations are discoverable but not paid for by everyone. Inference: in Go the integration code is small and compiled in; what is 'on demand' is the external agent program it drives, so the catalog entry reports whether that executable is present instead of installing an npm package. |
| 53 | [Paid native Flash translation uses independent queries][n53] | Paid reasoning translation is an explicit alternative that uses only the built-in DeepSeek routes and the exact `deepseek-flash` model. Each fragment is one fresh text-only request with no conversation history, and the exact request is recorded in the session before it is sent. | product feature | `packages/experimental/translator` | **ported** | Routing through an arbitrary configured model would hide who is billed and who receives the text. Recording the request before dispatch makes the charge auditable. Both rules are independent of the runtime. |
| 54 | [Machine translation results use shared Session records][n54] | Translation requests and results are stored as plugin records in the session they belong to. The request is durable before dispatch and the result before it is shown; a result is reused only for the same provider, language and recipe. | product architecture | `packages/experimental/translator` | **ported** | A display-only cache repeats external requests, and charges, after a restart. Storing them as ignorable plugin records keeps them out of model history. The Go session log needs the same kind of plugin record. |
| 55 | [Shared agents root joins the user-global instruction chain][n55] | User-global instructions are read from two ordered files: the harness home's `AGENTS.md`, then the shared `~/.agents/AGENTS.md` used by other agent tools. Identical content is rendered once, and both files are re-checked on resume so that an edit appears as a change notice. | product feature | `packages/context/agent-instructions` | **ported** | People who run several agent tools keep shared instructions in one place; copying them into the harness home creates two sources of truth. File locations and de-duplication are product behaviour. |
| 56 | [Preserve Web and Desktop plugin identities across experimental consolidation][n56] | When packages moved into the experimental directory they kept their published npm names and their existing configuration row ids, enforced by a typed name map, so that installed Web and Desktop profiles kept working. | product architecture | `packages/experimental` | **dropped** ([DH-ADR-2](../design.md#dh-adr-2)) | The Go design has no published npm names and no installed upstream profiles to keep working, and it is not required to load them. Inference: the lesson behind the note is to give each Go component an identifier that does not encode its maturity tier, so that promoting it later needs no rename. |
| 57 | [Desktop shell configuration independent of the Host][n57] | The desktop shell owns its own settings file for anything it needs before the host starts or while the host is down (updates, crash recovery). Host and plugin settings stay in loader configuration. | product architecture | `apps/desktop` | **ported** | Ownership follows runtime dependency: a function that must work without the host cannot read host configuration. The rule holds for a Go shell process; if shell and host become one process the split may collapse, which the desktop spec must decide. |
| 58 | [Retained PTC processes follow file policy][n58] | A retained PTC process starts through the same sandbox provider as a per-call one and keeps one resolved file policy until it is released. An approved one-time escalation runs in a fresh temporary environment that cannot see or change the retained variables. | product architecture | `packages/ptc-runtime/ptc-runtime-node` | **ported** | A process that outlives one call can also outlive a one-time approval or a policy change, which would be a sandbox escape by retention. The rule is a security invariant for any long-lived child process. |
| 59 | [Claimed commands as atomic composer chips][n59] | A slash command the composer has recognised becomes one atomic chip node in the editor instead of styled text. Deleting the chip releases the command claim, and undo restores it. | bug fix | `packages/client/ui-conversation` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The chip replaced a text transform that looped inside the Lexical editor library. The behaviour (a recognised command is one unit, and the claim follows the document) should be kept; the editor is a JavaScript island whose library and node model the UI area chooses. |
| 60 | [Shared native CUA process with Agent-scoped state][n60] | One native computer-use helper process per user and per executable content digest is shared by all agents and hosts, elected by file locks and ended when its last connection closes. It runs as a signed application started through the system launcher so that it holds its own privacy permissions. | product architecture | `packages/experimental/cua-macos` | **ported** | The constraint is in macOS: screen-capture identity follows the executable path, and permissions follow the launched application. It applies to a Go host exactly as it does to Node. |
| 61 | [Domain image Slots and a shared gallery Factory][n61] | User messages, assistant output, trajectory records and tool results each declare their own image slot, and the attachment plugin fills all four with one shared gallery component. | product architecture | `packages/client/ui-attachment` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | Slots and factories are the React client's extension mechanism. Server-rendered HTML gets the same reuse from one gallery template partial called from four places; whether third parties can replace it depends on the UI extension model the Go design adopts. |
| 62 | [Peer-scoped connect handshake][n62] | Automation callers (CLI, MCP) obtain their own peer through a connect endpoint and must opt in to receive human interactions; the default is none. Browsers keep sharing one owner peer that may answer. | product architecture | `packages/api/gateway` | **ported** | A method-only caller that is subscribed automatically can receive and answer an approval meant for a person. Opt-in participation and per-peer cleanup are security rules; the cookie versions and the redirect are wire detail that need not be kept (DH-ADR-2). |
| 63 | [PTC owns policy-change cleanup waits][n63] | Changing a session's permission mode commits immediately. The PTC executor observes the change, cancels affected calls and makes its own next call wait for that cleanup, so permission setters never wait. | product architecture | `packages/core/tool-executor-ptc` | **ported** | Making every setter await process cleanup spread one component's lifecycle into unrelated callers. The owner of a slow cleanup absorbs the wait; the same ownership rule suits every Go component that supervises a process. |
| 64 | [Standard reasoning Body Slot and reusable Content Factory][n64] | The reasoning body is a named slot that plugins can replace or wrap, and the standard Markdown rendering of reasoning is a separately registered factory that replacements can call. | product architecture | `packages/client/ui-chat` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | It separates 'where content goes' from 'the default way to draw it', so that a wrapper such as translation reuses the default. In Go templates the same split is a named block plus a reusable partial; the registration machinery is React-specific. |
| 65 | [Stored credentials take precedence over the launch environment][n65] | A credential saved through the product wins over the same name in the launch environment or in a `.env` file, for every provider. A save always writes the winning layer, and the answer reports which layer supplied the value. | product architecture | `packages/credentials/credentials-local` | **ported** | With the opposite order a key exported by the shell silently overrode what the user saved in the UI, and saving was refused. One precedence for all providers is a product rule. |
| 66 | [User-text candidate scheduling and Facts-driven refresh][n66] | References inside user text (sessions, files, skills, commands) are recognised by registered rules. Each rule returns its nearest match from a position in the original text, and one loop resolves overlaps deterministically and reuses work when known facts change. | product architecture | `packages/client/ui-conversation` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The algorithm is pure logic and is worth keeping. In the Go UI it runs in the host when the message is rendered, so the incremental refresh that exists to avoid React re-rendering is unnecessary. Go string offsets are bytes, not UTF-16 units. |
| 67 | [Chat group-tree positions and exchanges][n67] | A turn started by a notice (a finished subagent, a schedule, a background job) is not a new user message: it does not fold the previous turn. The 'latest' part of the chat is the latest exchange, defined by typed positions of nodes in a group tree. | product feature | `packages/client/ui-chat` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The visible rule (notices do not collapse what the reader is looking at) should be kept. The group-tree typing lives in the client's node model; the Go host computes exchanges when it renders the conversation. |
| 68 | [Exclusive Conversation matches][n68] | A conversation node definition can claim an event exclusively for its target, so that a generic definition does not also render it. Two claims on one event are an error that names both. | product architecture | `packages/client/ui-conversation` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | It prevents double rendering when a specific renderer and a generic one both match. A server-side renderer registry needs the same rule (the specific renderer wins, conflicts are errors), expressed as Go dispatch. |
| 69 | [Headless jobs omit the interactive credential store][n69] | The headless profile disables the stored-credential, authorization and account plugins. A headless job resolves keys only from its environment and `.env` files and never reads or writes the interactive credential file. | product architecture | `packages/bundle/headless` | **ported** | A job sharing the harness home with a desktop user would otherwise run on that user's saved account instead of the key given to the job. It is a composition choice the Go headless profile repeats. |
| 70 | [Peer principals and method-level Remote access control][n70] | Every caller connection (peer) carries a principal, the identity that permission decisions read. Each Remote method declares the resource it acts on, and the gateway asks a role-based access service before dispatch. There is no implicit operator fallback. | product architecture | `packages/api/remote-access` | **ported** | Without it a host method cannot tell whom a call speaks for, or restrict it. Principal, peer, per-method resources and one decision point are the authorization model for the Go API. Inference: Go handlers declare their resource at registration instead of through a TypeScript decorator. |
| 71 | [Asynchronous projection registration and retained readers][n71] | Registering a session projection (a value folded from the event log) returns an awaitable handle. Each domain service keeps its handles private and exposes business methods, and a retained handle stays readable while its owner's cleanup drains. | product architecture | `packages/session/session-projection` | **ported** | Readers that looked up by registry key lost access the moment a plugin unloaded, although an agent still needed the state while shutting down. Handle ownership fits Go well (a value the owner closes) and supports configuration reload. |
| 72 | [One fold motion for collapsing on the next message][n72] | Collapsing the previous exchange when the next message arrives uses one animation path that moves row heights and scroll position together, in batches. | bug fix | `packages/client/ui-chat` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | Two fold paths produced a visible jump. The goal (no jump when earlier work collapses) stands; frame-by-frame scroll driving is client JavaScript. Inference: the Go UI should first try native collapse with CSS scroll anchoring and add script only if that is not enough. |
| 73 | [Context breakdown retains only system prompts][n73] | The context-breakdown projection keeps only the surviving system prompts and one running total, instead of one entry per message. Its saved state no longer grows with conversation length. | simplification | `packages/llm/token-meter` | **ported** | Per-message entries made the projection cache megabytes large and slow to validate on every session listing. Bounded projection state is a rule for every Go projection. |
| 74 | [Continuous CUA hover without cross-call ownership][n74] | Computer-use hover is an explicit mouse-move operation that never presses a button and never restores the previous pointer position afterwards. No operation gives an agent ownership of an application across calls. | product feature | `packages/experimental/cua` | **ported** | Restoring the pointer, or priming with a click, dismissed the hover content. The rule of no ownership across calls keeps the shared helper simple. The behaviour belongs to the native helper and is unaffected by the host language. |
| 75 | [Background hover and screenshot coordinates][n75] | By default hover uses an accessibility route that does not move the user's real pointer; moving the system pointer needs an explicit request and, when configured, user approval. The host converts screenshot pixel coordinates to window points so that the model does not have to. | product feature | `packages/experimental/cua` | **ported** | Not disturbing the user's pointer, and not making the model do display-scaling arithmetic, are product rules implemented in the helper and the provider's geometry code. |
| 76 | [Subagent identity in required runtime context][n76] | A subagent's identity (its own, its parent's and its root's session id) is supplied as required runtime context derived from the live subagent tree, not written into its first task message. | product architecture | `packages/subagent/tool-subagent-control` | **ported** | Identity in the first message can be removed by compaction and is wrong after a fork. Deriving it from the live tree at each request keeps it current. This is agent-loop behaviour and language-neutral. |
| 77 | [Plugin entry customization: region-owned pins and More menus][n77] | The left sidebar, the conversation header and the right sidebar guide each keep their own list of hidden plugin entries and draw their own 'More' menu. One plugin supplies the shared menu content, including 'browse plugins' and 'create your plugin'. | product feature | `packages/client/ui-entry-customization` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The feature (hide rarely used entries, per region, persisted) stands. In the Go UI each region is a template that filters its entries from a stored preference; the slot registration that shares the menu content is React-specific. |
| 78 | [Registry and Git bundle updates][n78] | Third-party bundles installed from a registry or from Git can be updated from the plugins page. The host lists newer compatible versions, performs the update as remove-then-install and owns recovery if the install fails. | product feature | `packages/boot/plugin-manager` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | The goal stands: an update path owned by the host that cannot leave the plugin uninstalled after a failure. Version listing through `pnpm view` and npm peer-dependency rules do not exist in Go; the plugin-process spec needs its own version source, compatibility check and rollback. |
| 79 | [Boot the Preview Worker from the shipped profile and fetch lazy chunks on demand][n79] | The experimental browser Preview, which runs the whole host inside a Web Worker, boots from the same shipped profile as the served web deployment instead of a configuration dumped at pack time, and fetches lazy code chunks on demand. | product architecture | `packages/experimental/webworker-runtime` | **dropped** ([DH-ADR-2](../design.md#dh-adr-2)) | The Preview is possible because the upstream host is JavaScript and can run in a browser. Inference: a Go host could only do this compiled to WebAssembly, which no global decision plans. This first verdict must be confirmed by a recorded decision in the owning area, because DH-ADR-1 allows a part to be left out only that way. |
| 80 | [Agent Teams teammates are subagents][n80] | A team member is an ordinary subagent identified by its session id; there is no separate team roster and there are no member names. Every root agent and its descendants form the team, and the team feature adds only task tools and task policy. | simplification | `packages/team/agent-team` | **ported** | A second roster duplicated identity, recovery and teardown, and forced profiles to disable ordinary delegation. One identity model is simpler to build in Go from the start. |
| 81 | [Separate byte budgets for the user-global and project instruction chains][n81] | User-global and project instruction files are rendered under separate byte budgets, so a large project chain can no longer push the user's global instructions out. | bug fix | `packages/context/agent-instructions` | **ported** | With one shared budget the broadest files were dropped first, and those are the user's own. It is a prompt-assembly rule. |
| 82 | [Precompute the steering batch start in Conversation Node Definition state][n82] | Whether a steering message starts a new batch after the running turn's output is computed once in conversation-node state by the node definitions, instead of being worked out at render time. | product architecture | `packages/client/ui-chat` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | It exists to keep a reserved 44px slot stable in the React chat. A server-side view model computes this fact when it builds the conversation; the definition-state mechanism does not carry. |
| 83 | [Communication follows the subagent tree][n83] | Listing agents and sending messages work across the whole subagent tree under one root, not only between parent and child. The sender must be a live member of the same tree, and delivery re-checks that at final admission. | product architecture | `packages/subagent/tool-subagent-control` | **ported** | A team shares one task board across the tree, so adjacent-only messaging forced relays through parents. Authorization by tree membership with a final re-check is the rule to carry. |
| 84 | [Serve Session-list rows from a summary key set][n84] | A session-list row carries every registered client view except those its owner marks as excluded from the summary (turn outline, statistics, todos and similar), so that the list payload does not grow with log length. | product architecture | `packages/session/session-projection` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The exclusion mark is a TypeScript type-level device for a payload sent to a client store. A server-rendered list reads only the values its row template shows. The goal still applies to any list endpoint the Go API offers to SDK clients. |
| 85 | [Compress the Remote stream WebSocket with permessage-deflate][n85] | The multiplexed stream WebSocket is compressed with permessage-deflate at level 1 by default, and frames under 1,024 characters are sent uncompressed. | product feature | `packages/api/gateway` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The goal (less traffic over tunnels and slow links without adding latency to small token frames) stands. The Go UI streams over server-sent events, so compression is HTTP response compression with a flush after each event; the tuning is redone for that transport. |
| 86 | [Client plugin package assets served by URL][n86] | A client plugin package ships its images as files served under `/plugins/<package>/assets/` with a content hash in the file name, instead of embedding them as base64 in the JavaScript bundle. | product architecture | `packages/client/modules` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3), [DH-ADR-4](../design.md#dh-adr-4)) | There is no client plugin bundle in the Go design. Inference: first-party assets are embedded in the binary and served with content-hashed names; how a plugin process contributes static assets is for the plugin protocol to define. |
| 87 | [Durable Computer Use consent within one profile][n87] | Computer-use grants and per-conversation decisions are kept in the profile's domain storage, not in plugin configuration or process memory. Every operation reopens the state under the profile file lock, and revocation generations stop a stale handle or a late answer from restoring access. | product architecture | `packages/experimental/cua` | **ported** | Grants in configuration tied each change to config reconciliation, and in-memory decisions were lost on restart and invisible to other host processes. Revocation that holds across processes is a security requirement. |
| 88 | [File drag ownership outside the application layout][n88] | Dragging files and images is owned by one client plugin that keeps the original files alive during a drag and shows failures in a shell overlay, so the application layout has no image-specific provider. | product architecture | `packages/client/ui-file-drag` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The ownership problem (feature state hidden in a React ancestor) does not arise without a component tree. Browser drag and drop stays a small script island, and the rule that the original file survives its source closing still applies to it. |
| 89 | [Pi-ai system reminder fallback][n89] | For models without native mid-conversation system messages, the multi-provider adapter turns each later system message into a user message wrapped in `<system-reminder>` before sending. Stored messages keep their original roles. | bug fix | `packages/llm/llm-pi-ai` | **ported** | Folding later system messages into the first prompt invalidates the provider's cached prefix on every update. The trigger is the behaviour of the pi-ai library, but the rule (the adapter adapts, the log stays truthful) applies to whatever Go adapter serves models without that capability. |
| 90 | [Team task boards share ordinary subagent waits][n90] | The team feature owns only task storage, dependencies and compare-and-set task commands. Identity, discovery, messaging, interruption and waiting come from the ordinary subagent tools, and task state is read from the task journal, not delivered as messages. | simplification | `packages/team/agent-team` | **ported** | It removes a second roster and a second wake-up channel. Fewer overlapping mechanisms is a direct simplification for the Go build. |
| 91 | [New Session reuses a blank Session, matching a requested preset][n91] | New Session reuses the workspace's first blank session, keeping its unsent draft. A request that names a preset skips a blank session that lists a different preset. | product feature | `packages/client/ui-workspace` | **ported** | Creating a fresh session on every click lost the draft, and letting a preset flow take over any blank session moved an ordinary draft under the wrong preset. Inference: with no client store the selection moves into the host, next to the writer-lock rule of the blank-reuse note. |
| 92 | [PTC Script evaluation and state retention][n92] | The PTC runtime treats program syntax and state retention as two separate settings: `programFormat` says what syntax is accepted, `retainsState` says whether variables survive between calls. A 'script' evaluator is added that can run either fresh or retained. | product architecture | `packages/ptc-runtime/ptc-runtime-node` | **ported** | Inferring memory lifetime from syntax prevented valid combinations. Two independent settings are the right interface for any runtime. The evaluator internals (Node `vm`, the REPL await transform, the Acorn parser) belong to the JavaScript runtime process and are not host design. |
| 93 | [Team availability is selected per Session][n93] | Team mode is a saved choice on the root session, default off, made in the model menu; it is not a plugin switch in the shared profile. Turning it off keeps the task journal and the child agents. | product feature | `packages/team/agent-team` | **ported** | A profile-level switch changed other sessions and needed a config edit to try collaboration once. Per-session selection is product behaviour. |
| 94 | [The workspace tree is per-pane state drawn inside each file tab, and the plugin mounting it decides where files open][n94] | The workspace file tree is drawn inside each file tab, but its state (expansion, width, scroll, last revealed file) is kept per pane, so it survives tab switches. Hidden trees hold no directory watch on the host. | product architecture | `packages/client/ui-sidebar-files` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | State per pane exists because the dock unmounts unselected tabs. The goals (tree state survives tab switches, no watch for a hidden tree) stand; where the state lives in a server-rendered UI is the UI area's decision. |
| 95 | [Context menu contributions][n95] | Context-menu actions are contributions registered against typed target families and described by plain data fields, so a menu can be drawn without loading the contributor's React component. | product architecture | `packages/client/ui-context-menu` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | Plain-data actions resolved for a typed target suit server rendering well: the host resolves the actions and renders the menu. The registration through client plugin slots is replaced; contributions come from Go components or plugin processes. |
| 96 | [Replaceable product icons through an SVG sprite][n96] | Each product icon is a symbol in one hidden SVG sprite and is drawn with `<use>`. A theme can replace the artwork, and replaced icons still follow the current colour and the light or dark palette. | product architecture | `packages/client/ui-theme` | **ported** | An SVG sprite is plain HTML and CSS, so it works unchanged in server-rendered templates and needs no JavaScript. It also keeps icon markup small in every rendered fragment. |
| 97 | [Schedule rule storage and shared recurrence][n97] | There is one schedule storage domain. Cron expressions and recurrence rules are ordinary timing kinds in it, with parsing and occurrence arithmetic in the shared time package, and there is no second store or importer. | simplification | `packages/schedule/schedule` | **ported** | A parallel store with an importer duplicated timing and storage authority. The Go design starts with one domain; the version 1 read compatibility is not needed (DH-ADR-2). |
| 98 | [Session snapshots, passive subscriptions, and Web aggregation][n98] | Session access is split into six independent operations: a snapshot and an observation each for history, projections and state. Follow and wait are compositions of these, and subscribing does not force a read. | product architecture | `packages/api/session-controller` | **ported** | Mixing point reads, subscriptions and the web conversation opening made passive observers do unnecessary reads and left a gap between read and subscribe. The Go API and the server-rendered conversation view both build on the same six operations. |
| 99 | [whenAccess registration that follows access][n99] | A client plugin can register sidebar entries, panels and streams 'while access holds': the registration is installed when the current caller gains a permission and removed when it loses it. | product architecture | `packages/api/remote-access` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The Go host decides what to render per request from the caller's permissions, so entries a caller may not use are not in the page. What must be designed instead is re-rendering when a permission changes during a session. |
| 100 | [The official DeepSeek route follows the official model listing until its catalog is customized][n100] | The API-key route follows the official model listing only while it talks to the official endpoint and the user has not customised the model catalog. A customised catalog wins whole and stops listing requests. | product feature | `packages/llm/llm-deepseek-api-key` | **ported** | New official models reach API-key users without a release, while a user's edited catalog, or a gateway behind a custom base URL, is never overridden. This is product behaviour of the provider. |
| 101 | [User controls address the target subagent][n101] | A browser prompt or a user interruption for a subagent addresses only that child session, and each method declares that session as its access resource. Access to a parent or to the global resource does not imply access to the child. | product architecture | `packages/subagent/subagent` | **ported** | A caller-supplied parent id proved consistency, not identity or permission. Per-target authorization follows from the principal model in the remote-access note. |
| 102 | [Subagent tools own communication guidance][n102] | The subagent messaging tool plugin owns the guidance about how and when a child reports to its parent, emitted as runtime context only when its tool is actually available. The continuation manager owns result delivery. | product architecture | `packages/subagent/tool-subagent-control` | **ported** | The manager used to detect one tool implementation through a hidden marker. Guidance that travels with the tool that needs it is the same composition rule as the preset-scoped notes. |
| 103 | [Ordinary forks have no durable creator relationship][n103] | An ordinary fork copies history but records no link to its source session. Only subagents record their creator, and `origin: subagent` classifies them. | product architecture | `packages/core/session` | **ported** | Using one parent field for both copied history and delegation made forks look like subagents, with false child notifications and hidden sessions. The Go session header should have separate fields from the start. |
| 104 | [Image reads use attachment-store access][n104] | Reading an image needs permission on the attachment store plus a complete content-addressed reference. The host no longer scans the session's history to prove that the session mentions the image. | simplification | `packages/api/session-controller` | **ported** | The scan cost grew with history length and located nothing, because the path follows from the content hash. Inference: the Go design keeps this, but must state plainly that any authorised caller holding a reference can read any image in the store. |
| 105 | [macOS consent shared by bundle identifier][n105] | Computer-use consent on macOS is keyed by the application's bundle identifier, so one decision covers all installed versions. The executable path is the fallback identity when there is no bundle identifier. | product feature | `packages/experimental/cua-macos` | **ported** | Path and signature identities split one application into many grants and excluded unsigned development builds. The identity rule lives with the native helper. |
| 106 | [Office skills in every deployment with one Office Kit package][n106] | Office skills are mounted in every deployment and read from one package's asset directory. One Office Kit package owns locating LibreOffice Kit, and a deployment without it lacks the capability instead of failing to load. | product architecture | `packages/skill/skill-office` | **ported** | Three separate lookups, and a load failure when the kit was absent, are removed. Inference: in Go the skill files are embedded in the binary, and the absent-dependency rule (degrade, do not fail start-up) applies to every optional external tool. |
| 107 | [Session writable-directory effects][n107] | A plugin can grant one live session write access to an extra directory. The grant is an effect that ends when the plugin unloads and is not restored from history on resume or fork. An invalid extra path stops execution with a named error. | product architecture | `packages/sandbox/sandbox-policy` | **ported** | Deployment-wide roots cannot express a grant for one session, or one that ends with its plugin. Effect-scoped grants map directly onto the Go cordis lifecycle. Disposal affects later operations only; processes already running keep their launch grants, which is a stated limit. |
| 108 | [Plugin CLIs by absolute path in runtime context][n108] | A plugin that installs a vendor command-line tool outside PATH registers its absolute path, and the harness tells the model in required runtime context to run it by that path. The harness does not change the environment that commands run in. | product architecture | `packages/connector/cli-account` | **ported** | The model must run the plugin's own copy, and altering PATH for every shell would change how all commands behave. Telling the model is the least invasive mechanism and is language-neutral. |
| 109 | [Runtime download of upstream skills][n109] | Vendor-published skill guides are not copied into the repository. A plugin pins them by repository, commit, size and digest; the host downloads the archive when the plugin loads, verifies it and caches it under the harness home. | product architecture | `packages/connector/upstream-skills` | **ported** | It avoids redistributing another company's content and keeps large vendored files out of the source tree; that reason holds for a Go binary. A failed download must not block account sign-in, and the download host is configurable for offline installs. |
| 110 | [Development signing identity for source-built CUA helpers][n110] | Source builds of the computer-use helper are a separate 'Dev' application with its own bundle identifier, optionally signed with a developer-chosen certificate, so that macOS permissions survive rebuilds and do not replace the release helper's grants. | product architecture | `packages/experimental/cua-macos` | **ported** | macOS ties privacy grants to the bundle identifier and the code signature. Anyone who builds the Swift helper from source for the Go harness meets the same constraint, so the development variant is kept; the signing set-up guide in the note is contributor workflow. |
| 111 | [Retained PTC interruption][n111] | On timeout or cancel of a retained PTC program the host revokes its authority at once and asks the runtime to stop cooperatively. Only if no acknowledgement arrives within a grace period is the process killed, and the result says whether the variables survived. | product architecture | `packages/ptc-runtime/ptc-runtime-node` | **ported** | Killing the process on every timeout lost unrelated retained objects. Revoke first, then cooperative stop, then bounded kill is the cancellation pattern for any supervised child process in Go. |
| 112 | [Web provider selection - independent menus over registered providers][n112] | Users choose the search provider and the fetch provider independently, from the lists of providers registered in the host. By default search follows the conversation's model route and fetch uses local HTTP. | product feature | `packages/web/web` | **ported** | One combined control tied fetch billing and access policy to the search choice, and could not show providers added by other plugins. An observable provider registry with two independent selections ports directly. |
| 113 | [Windows additional directory grants stay in file tools][n113] | On Windows, extra writable directories granted to a session are enforced only by the harness file tools (`write`, `edit`); sandboxed processes keep workspace and private-temp access only. The model is told the difference. | product architecture | `packages/sandbox/sandbox-windows-acl` | **ported** | This is a recorded limitation: extending the ACL-based process sandbox per directory was judged too fragile. The first verdict keeps it; the sandbox area decides whether a Go Windows backend can enforce extra roots and so remove the limit. |

### Proposed upstream (3 notes)

None of these three is built upstream. The verdict says whether the Go design should
adopt the idea.

| # | Note | Summary | Kind | Area | First verdict | Why |
|---:|---|---|---|---|---|---|
| 114 | [Unified Typert/Remote metadata][n114] | Proposed, not built: generate one ordinary metadata artifact per Remote method (description, named-input parser, JSON Schema, result metadata) so that CLI help, argument parsing and MCP tool discovery work offline, with input strictness chosen by the consumer. | product architecture | `packages/typert` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2)) | Adopt the goal, not the mechanism. A single description of each API method that feeds CLI help, MCP tool schemas and SDK types is valuable in Go too; Typert's TypeScript analysis and its two compiler programs are not available. Inference: the Go API spec should define methods once as Go descriptors and generate the rest. |
| 115 | [Present single-Session migration through follow redirection][n115] | Proposed, not built: when opening an old session needs a format migration, the history read returns a typed 'migration' reference; the client follows its progress and failure, then returns to the history read. Progress never enters the session log. | product feature | `packages/api/session-controller` | **ported** | Adopt, at low priority. The Go format has no migrations at first (DH-ADR-2), but the first one will need visible progress and failure that stay out of the event log. The proposal leaves open whether 'done' means prepared in memory or published, and the Go spec must choose. |
| 116 | [Keep cleanup dependencies usable until consumers finish][n116] | Proposed, not built: the owner of a resource must keep it usable until its consumers have finished their asynchronous cleanup (stop new work, settle accepted work, drain, then release). The note lists five upstream paths where a sibling disposer closes a resource early: session persistence, storage, authorization, SSH and the Windows sandbox. | product architecture | `vendor/cordis` | **ported** | Adopt as a design rule. The Go cordis port already releases a fiber's effects one at a time in reverse order (AR-ADR-8 in docs/architecture.md), which removes the concurrent-sibling cause; each of the five Go components still needs the sequence of stop admission, settle, drain, release, and a test that pauses a consumer during provider disposal. |

## Patterns worth carrying into the Go design

These ten patterns are the decisions in this part that most shape the product. Each
entry gives the decision, why it is needed, what it costs, and what the Go design should
do and check. Row numbers refer to the tables above. The "In Go" lines are inference.

### 1. A resource stays usable until its consumers have finished cleaning up

- **Decision.** The owner of a resource keeps it usable through its consumers' complete
  asynchronous cleanup: stop admitting new work, settle accepted work, drain, then
  release (row 116,
  `.agents/notes/proposed/architecture/2026-10-09-cordis-cleanup-dependency-lifetimes.md:15`).
  Upstream has not built this. The same ownership idea is already shipped in two smaller
  places: the component that owns a slow cleanup waits for it itself, so callers that
  only change a setting never wait (row 63,
  `.agents/notes/implemented/architecture/2026-10-09-ptc-owned-policy-cleanup.md:13`),
  and a projection handle stays readable while its owner drains (row 71,
  `.agents/notes/implemented/architecture/2026-10-12-session-projection-registration-handles.md:13`).
- **Why.** Upstream cordis starts sibling disposers concurrently
  (`.agents/notes/proposed/architecture/2026-10-09-cordis-cleanup-dependency-lifetimes.md:11`).
  The note traces five paths where a resource closes while a consumer still needs it:
  final session events are not persisted, a delivered reminder is not committed and can
  be delivered again, a successful credential write is reported as failed, SSH cleanup
  requests are refused, and a Windows sandbox directory is removed under a live process.
- **Tradeoff.** Shutdown takes longer, and a wrongly composed dependency can hold a
  resource forever or deadlock. Bounded waits must stay.
- **In Go.** The Go cordis port already releases a fiber's effects one at a time, newest
  first ([AR-ADR-8](../../architecture.md#ar-adr-8)), which removes the concurrent-sibling
  cause. Each of the five components still needs the four-step sequence written into its
  specification, and a test that pauses a consumer before its last use of the resource,
  starts disposal of the provider, releases the pause and checks the final write landed.

### 2. Live configuration has one owner and two kinds of field

- **Decision.** Every configurable value is declared once, in its plugin's config
  schema. A field marked volatile can change without restarting the plugin; any other
  change remounts it (row 15,
  `.agents/notes/implemented/architecture/2026-09-18-volatile-config-references.md:15`).
  The settings page writes to the active profile's patch file through the ordinary
  loader path and refuses an edit that a higher-priority layer would shadow (row 17,
  `.agents/notes/implemented/architecture/2026-09-19-profile-owned-live-configuration.md:13`).
- **Why.** A second settings store duplicated schemas, defaults, persistence and change
  events. Remounting a component to change one value disposes its services.
- **Tradeoff.** A consumer must read a volatile value at the moment of use and must not
  copy it at start-up.
- **In Go.** A search of `src/go` finds no volatile-field support in the Go loader. Add a typed live reference that a
  component reads on use, and make the loader's config comparison aware of which fields
  are live. Check with a test that changes a volatile field and asserts that the
  component instance and its services were not replaced.

### 3. A host service and its model-facing tools are separate components

- **Decision.** The schedule service is host-wide; the schedule tools are a separate
  plugin mounted per agent preset (row 39,
  `.agents/notes/implemented/architecture/2026-09-24-preset-scoped-schedule-tools.md:13`).
  The time injection is mounted only where those tools are (row 40,
  `.agents/notes/implemented/architecture/2026-09-24-preset-scoped-time-context.md:15`).
  Messaging guidance travels with the messaging tool (row 102,
  `.agents/notes/implemented/architecture/2026-10-22-subagent-tool-owned-communication.md:13`).
- **Why.** Tool schemas and injected context cost prompt tokens on every request. A
  preset that cannot use them should not pay for them.
- **Tradeoff.** More components and more rows in each preset.
- **In Go.** Every capability is at least two components: the service, and the tools or
  context that expose it to the model. Check by asserting that the request header of a
  `minimal`-style preset contains none of an unmounted capability's tools or context.

### 4. A change to the visible tools is a durable, ordered session record

- **Decision.** Each request header records the active tools; the loop logs additions
  and removals as one structured developer message that points at the header holding
  the schema (rows 6 and 24,
  `.agents/notes/implemented/architecture/2026-09-17-developer-session-changes.md:13`,
  `.agents/notes/implemented/architecture/2026-09-20-dynamic-tool-updates.md:13`).
- **Why.** Plugin and MCP tools appear and disappear during a conversation. Providers
  can keep their cached prefix only if the history of tool changes is replayed exactly,
  also after a restart or a fork.
- **Tradeoff.** More event types, and strict reference checks when a log is read.
- **In Go.** Needed more than upstream, because plugin processes can crash and restart
  ([DH-ADR-4](../design.md#dh-adr-4)). Check by replaying a recorded session in which a
  tool is removed and restored, and comparing the rebuilt requests with the originals.

### 5. Fail closed and fail loud

- **Decision.** A store that holds authority treats an unreadable file as a load
  failure, never as "empty" (row 3,
  `.agents/notes/implemented/architecture/2026-09-16-host-schedule-storage.md:17`). A
  session log is checked for consistency between events on every read (row 8,
  `.agents/notes/implemented/architecture/2026-09-17-native-v4-read-validation.md:15`).
  Bad TLS material stops start-up with no fallback to HTTP (row 35,
  `.agents/notes/implemented/architecture/2026-09-23-native-https-web-listener.md:13`),
  and a wildcard bind address is rejected (row 16,
  `.agents/notes/implemented/architecture/2026-09-19-concrete-web-bind-address.md:13`).
  An uncaught error ends the process after one complete diagnostic (row 29,
  `.agents/notes/implemented/architecture/2026-09-22-fatal-diagnostics-and-crash-reports.md:21`).
- **Why.** Each lenient alternative hides damage: reminders vanish, a broken log reaches
  the model, tokens travel in clear text, a half-updated object keeps running.
- **Tradeoff.** The product stops in cases where it could have limped on. Optional parts
  go the other way on purpose: a spill file or an upload extension that fails is dropped
  and the main operation continues (rows 29 and 36).
- **In Go.** State for each store and listener which failures stop it. A panic in any
  goroutine already ends a Go process; the design adds the diagnostic file and a bounded
  release step. Check each rule with a corrupted-input test.

### 6. No silent fallback across a trust or billing boundary

- **Decision.** The two DeepSeek routes never use each other's credential (row 5,
  `.agents/notes/implemented/architecture/2026-09-17-account-provider-signout.md:13`).
  A failed local speech recogniser never uploads to a cloud one (row 2,
  `.agents/notes/implemented/architecture/2026-09-16-experimental-voice-input.md:15`).
  A translation failure never picks another service (row 51,
  `.agents/notes/implemented/architecture/2026-10-05-anonymous-reasoning-translation.md:15`).
  A private package registry never falls through to a public one (row 14,
  `.agents/notes/implemented/architecture/2026-09-18-plugin-install-registries.md:13`).
  Credential precedence is one explicit order (row 65,
  `.agents/notes/implemented/architecture/2026-10-09-stored-credential-precedence.md:15`),
  and the headless profile leaves the interactive store out altogether (row 69,
  `.agents/notes/implemented/architecture/2026-10-12-headless-environment-auth.md:13`).
- **Why.** A fallback that changes who is billed, who receives user data or whose code
  is installed is a decision the user did not make.
- **Tradeoff.** More visible failures, and the user must choose a provider explicitly.
- **In Go.** Make it a review rule for every provider registry: selection is by exact
  id, and a fallback list may only contain entries of the same trust class. Check with
  a test per registry that fails the selected provider and asserts no other is called.

### 7. Callers have an identity, methods name their resource, and automation opts in

- **Decision.** Each connection (peer) carries a principal, and each host method
  declares the resource it acts on, with one access decision before dispatch (row 70,
  `.agents/notes/implemented/architecture/2026-10-12-peer-principal-and-remote-access.md:30`).
  Automation callers get their own peer and receive no human interactions unless they
  ask (row 62,
  `.agents/notes/implemented/architecture/2026-10-09-operator-scoped-connect-handshake.md:13`).
  Access to a parent session does not imply access to a child (row 101,
  `.agents/notes/implemented/architecture/2026-10-22-subagent-target-user-controls.md:13`).
- **Why.** Without it a script that only calls methods can receive and answer an
  approval meant for a person, and no method can be restricted by caller.
- **Tradeoff.** Every method must declare a resource, and lists must be filtered by it.
- **In Go.** Put the principal in the request context from the first release, and make
  the resource a required part of registering a handler, so that a method without one
  does not compile or does not register. Check with a table test over every method.

### 8. Supervised child processes: revoke first, stop cooperatively, then kill

- **Decision.** On timeout or cancel the host revokes the program's authority at once,
  asks the runtime to stop, and kills the process only after a bounded grace period
  (row 111,
  `.agents/notes/implemented/architecture/2026-10-29-retained-ptc-interruption.md:13`).
  A retained process keeps the file policy it started with, and a one-time escalation
  runs in a fresh process that cannot see retained state (row 58,
  `.agents/notes/implemented/architecture/2026-10-08-persistent-ptc-sandbox.md:13`).
- **Why.** Killing on every timeout loses retained state. Not revoking first lets a
  stopping program keep calling tools. A process that outlives an approval would keep
  the approval's rights.
- **Tradeoff.** A stop can take up to the grace period, and the result must say whether
  the state survived.
- **In Go.** The Go design has more supervised processes than upstream: every external
  plugin is one ([DH-ADR-4](../design.md#dh-adr-4)). Use the same three steps for all of
  them. Check with a program that ignores the stop request and one that honours it.

### 9. Anything that grows with history is bounded at its one write path

- **Decision.** Projection state does not keep one entry per message (row 73,
  `.agents/notes/implemented/architecture/2026-10-13-context-breakdown-system-prompt-state.md:13`).
  Schedule delivery history is capped by age and count (row 31,
  `.agents/notes/implemented/architecture/2026-09-22-schedule-hard-delete-and-bounded-history.md:15`).
  The session-log upload is capped in bytes (row 36,
  `.agents/notes/implemented/architecture/2026-09-24-bounded-session-log-upload.md:12`).
  Telemetry batches are bounded by bytes, not only by count (row 43,
  `.agents/notes/implemented/architecture/2026-09-25-session-log-otel-byte-limits.md:15`).
  Session-list rows leave out values no row shows (row 84).
- **Why.** Each of these was found as a real cost: megabytes of cache validated on every
  session listing, a task document that grew on every delivery, a request too large to
  serialize.
- **Tradeoff.** Old detail is discarded, and a reader must be told that earlier records
  are no longer available.
- **In Go.** For each projection, stored document and outbound request, the
  specification states the bound and where it is applied. Check with a long synthetic
  session that the stored size stays flat.

### 10. Give an operation its identity before the attempt

- **Decision.** A schedule reserves the session id before each attempt to create the
  session, so that a retry reuses it (row 11,
  `.agents/notes/implemented/architecture/2026-09-17-standalone-schedules.md:15`). A
  timed question settles twice, once as "pending" for the tool call and once as a
  durable user message when the answer arrives (row 21,
  `.agents/notes/implemented/architecture/2026-09-19-timed-user-question-two-settlements.md:15`).
  A blank session is reused only after its writer lock is held (row 10,
  `.agents/notes/implemented/architecture/2026-09-17-process-local-blank-sessions.md:13`).
- **Why.** These are the places where a retry, a late answer or a second host would
  otherwise create duplicates or lose a result.
- **Tradeoff.** A reserved id can stay unused, and a pending question needs a second
  delivery path.
- **In Go.** Treat "reserve, then act, then confirm" as the standard shape of any
  operation that crosses a process or a restart. Check each with a test that fails the
  action after the reservation and retries.

## Decisions that exist only because of JavaScript or npm

These notes either solve a problem that Go does not have, or solve a real problem with a
mechanism that only exists in JavaScript, Node, npm, React or Electron. They are the
source of most `replaced` verdicts. The right-hand column is inference.

| Row | Upstream decision | The JavaScript or npm fact behind it | What happens in Go |
|---:|---|---|---|
| 18 | Module lookup order inside a profile | Node searches ancestor `node_modules` folders, and upstream intercepts peer dependencies (`.agents/notes/implemented/architecture/2026-09-19-profile-resolution-lookup-order.md:15`) | Nothing. Components are linked at build time. Not applicable. |
| 48, 50 | Refresh module resolution after a package operation; replace plugins whose files changed | Resolution was built once at start (`.agents/notes/implemented/architecture/2026-09-30-profile-package-refresh-and-manifest-invalidation.md:11`), and Node caches an evaluated module by file URL (`.agents/notes/implemented/architecture/2026-10-01-replace-changed-package-modules-on-recomposition.md:9`) | A plugin is a process. Upgrading it is stop, replace the binary, start. The one exclusive recomposition path is kept. |
| 14, 78 | Registry plan with mirrors; registry and Git updates | Installs run `pnpm add` (`.agents/notes/implemented/architecture/2026-09-18-plugin-install-registries.md:9`) and versions are read with `pnpm view` (`.agents/notes/implemented/architecture/2026-10-14-registry-bundle-updates.md:13`) | No npm. The plugin-process design needs its own source, mirror setting, version list and rollback. "Private never falls back to public" is kept. |
| 28, 52 | Optional bundles; on-demand official bundles | A bundle's npm dependencies are downloaded with every installation (`.agents/notes/implemented/architecture/2026-09-21-experimental-capabilities-as-optional-bundles.md:15`), and native integrations bring their own runtimes (`.agents/notes/implemented/architecture/2026-10-05-official-on-demand-bundles.md:9`) | First-party code is compiled in. "Optional" becomes a default-off configuration group; "on demand" becomes detecting an external executable. |
| 56 | Keep plugin identities across the experimental move | Published npm package names are part of installed profiles (`.agents/notes/implemented/architecture/2026-10-06-web-desktop-plugin-identity-compatibility.md:13`) | Dropped. Lesson kept: a component identifier should not encode its maturity. |
| 86 | Client plugin assets served by URL | The browser half of a client plugin is a lazily loaded JavaScript bundle (`.agents/notes/implemented/architecture/2026-10-19-client-plugin-package-assets.md:9`) | No client bundle. Assets are embedded and served with content-hashed names. |
| 79 | Preview Worker boots the shipped profile | The host is JavaScript, so it can run inside a browser Web Worker (`.agents/notes/implemented/architecture/2026-10-14-webworker-preview-profile-parity.md:9`) | Dropped by the owner ([DH-ADR-17](../design.md#dh-adr-17)). |
| 12, 20, 114 | Binary results, duplex streams and method metadata for Remotes | Typert derives codecs and call shapes from TypeScript types (`.agents/notes/implemented/architecture/2026-09-17-workspace-file-binary-transfer.md:15`, `.agents/notes/implemented/architecture/2026-09-19-remote-duplex-stream.md:63`, `.agents/notes/proposed/architecture/2026-09-20-unified-typert-remote-metadata.md:13`) | Go types exist at run time. File bytes go over plain HTTP, live updates over server-sent events, and one Go descriptor per method feeds CLI help, MCP schemas and SDK types. |
| 9, 84 | Attribution compatibility policy; summary-excluded projections | Compatibility is judged from fingerprints of TypeScript unions (`.agents/notes/implemented/architecture/2026-09-17-persistence-attribution-policy.md:9`), and exclusion is a type-only mark (`.agents/notes/implemented/architecture/2026-10-18-session-list-summary-projections.md:15`) | The Go event schema states its own rule for unknown values. A server-rendered list reads only what it shows. |
| 25, 30 | Stable sidebar mounting; account-scoped browser storage | Electron's webview resets when it leaves the document (`.agents/notes/implemented/architecture/2026-09-20-sidebar-retained-tab-layout.md:9`), and Electron offers storage partitions (`.agents/notes/implemented/architecture/2026-09-22-platform-browser-storage.md:13`) | Nothing: the embedded browser pane and embedded sign-in pages are dropped, and links and sign-in open in the system browser ([DH-ADR-18](../design.md#dh-adr-18)). |
| 26, 68, 82 | Group assembly, exclusive matches and steering batch state in the chat | React rebuilds component instances when parents or keys change (`.agents/notes/implemented/architecture/2026-09-21-conversation-build-groups.md:9`); the rest follows from the client's node-definition model (`.agents/notes/implemented/architecture/2026-10-12-exclusive-conversation-matches.md:15`, `.agents/notes/implemented/architecture/2026-10-16-steering-batch-start-in-definition-state.md:9`) | The host builds the conversation view and renders it. The rules (specific renderer wins, grouping is a registered rule) move into Go. |
| 61, 64, 99 | Image slots, reasoning slot and factory, `whenAccess` | Slots and Factories are the React client's plugin mechanism (`.agents/notes/implemented/architecture/2026-10-09-reasoning-content-slot-factory.md:15`) | Template blocks and partials rendered by the host. Third-party replacement is open question 7. |
| 59 | Command chip in the composer | A text transform looped inside the Lexical editor (`.agents/notes/implemented/architecture/2026-10-09-composer-command-chip.md:9`) | The editor is an island. Its library is the UI area's choice. |
| 85 | WebSocket compression | The `ws` library ignores its size threshold in the chosen mode, so the mux decides per frame (`.agents/notes/implemented/architecture/2026-10-18-websocket-permessage-deflate.md:19`) | Nothing on the live paths: there is no multiplexed socket, the event stream is not compressed and the terminal socket has compression off by default ([../libs/compression.md](../libs/compression.md), [../libs/ws.md](../libs/ws.md)). Ordinary HTTP compression applies to pages and assets. |
| 36 | Bounded session-log upload | A long log exceeds the JavaScript engine's string limit (`.agents/notes/implemented/architecture/2026-09-24-bounded-session-log-upload.md:8`) | No such limit, but the byte cap is kept as a bound on request size. |
| 29 | Fatal diagnostics (parts) | Node's `uncaughtException` hook and a retry rule for client script batches (`.agents/notes/implemented/architecture/2026-09-22-fatal-diagnostics-and-crash-reports.md:21`, `.agents/notes/implemented/architecture/2026-09-22-fatal-diagnostics-and-crash-reports.md:27`) | A Go panic already ends the process. The batch rule has no counterpart. The diagnostic and the crash report are kept. |
| 92 | Script evaluation in the PTC runtime | Node's `vm` module and its REPL transform for top-level `await` (`.agents/notes/implemented/architecture/2026-10-20-ptc-script-evaluation-and-retention.md:17`) | Nothing: the Go host runs no JavaScript and embeds no interpreter ([DH-ADR-8](../design.md#dh-adr-8)). |
| 89 | System-reminder fallback | The pi-ai npm library merges later system messages into the first prompt (`.agents/notes/implemented/architecture/2026-10-19-pi-ai-system-reminder-fallback.md:9`) | The library is gone; the rule is kept for any model without mid-conversation system messages. |

## Open questions for the area owners

1. **Browser Preview (row 79).** Settled by the owner: the in-browser preview runtime
   is dropped ([DH-ADR-17](../design.md#dh-adr-17)), a recorded exception to
   [DH-ADR-1](../design.md#dh-adr-1).
2. **Runtime for model-written programs (rows 33, 58, 63, 92, 111).** Upstream's runtime
   is a Node child process
   (`.agents/notes/implemented/architecture/2026-09-23-best-effort-persistent-ptc.md:13`).
   Settled by the owner: no engine is chosen, because none is built. A model-written
   script is an ordinary sandboxed command that calls tools through the `dsh tool call`
   command line ([DH-ADR-8](../design.md#dh-adr-8)). Area A11 says what becomes of the
   rules in these rows; the stop order (revoke, stop, kill) is the one every child
   process follows ([../design.md](../design.md) §4.4).
3. **A duplex channel (rows 20, 85).** Server-sent events are one-way. Settled: the
   terminal island has one WebSocket per terminal
   ([DH-ADR-WIRE-2](wire-and-api.md#dh-adr-wire-2), [../libs/ws.md](../libs/ws.md)),
   and SDK clients have a two-way JSON-RPC stream on stdio
   ([DH-ADR-WIRE-5](wire-and-api.md#dh-adr-wire-5)).
4. **Embedded pages in the desktop shell (rows 25, 30).** Settled by the owner: there
   is no desktop shell and no embedded page; links and sign-in open in the system
   browser ([DH-ADR-18](../design.md#dh-adr-18)).
5. **Plugin distribution (rows 1, 14, 28, 52, 78).** Five notes describe install, mirror,
   optional selection, discovery and update for npm bundles. Settled by the owner: a
   plugin is a local directory with a manifest that the operator places; the harness
   inspects it, asks for capability grants and runs it, and downloads, discovers and
   updates nothing. A registry may be added later behind the same manifest
   ([DH-ADR-13](../design.md#dh-adr-13)).
6. **Client version gate (rows 37, 100).** The model listing hides models whose declared
   minimum client version is newer than the running client
   (`.agents/notes/implemented/architecture/2026-09-24-deepseek-account-remote-model-catalog.md:14`).
   Area A04 decides which client name and version the Go harness reports. Whether
   reporting them to a vendor counts as data leaving the machine is a question the
   owner has not answered; the design treats it as opt-in until then
   ([DH-ADR-9](../design.md#dh-adr-9)).
7. **User-interface extension by plugins (rows 61, 64, 77, 95, 99).** Upstream lets
   client plugins replace or wrap parts of the page. The UI area must decide what a
   plugin process may contribute: data-only actions (row 95 fits this well), named
   template blocks, or nothing.
8. **Shell and host as one process or two (rows 23, 34, 57).** Shell-owned settings exist
   because the shell must work while the host is down
   (`.agents/notes/implemented/architecture/2026-10-08-desktop-shell-configuration.md:15`).
   Settled by the owner: there is no shell ([DH-ADR-18](../design.md#dh-adr-18)), so
   nothing must work while the host is down. Update and recovery are commands of the
   `dsh` binary (area A24).
9. **Volatile fields in the Go loader (rows 15, 17, 100).** Settled by the loader
   specification: a component that can take new config without a restart registers the
   port's update hook, and schema volatility metadata is not ported
   ([DH-ADR-LOADER-7](../libs/cordis-loader.md#dh-adr-loader-7),
   [DH-ADR-SCHEMA-5](../libs/schemastery.md#dh-adr-schema-5)).
10. **Scope of image reads (row 104).** Upstream lets any authorised caller that holds a
    complete reference read any image in the store
    (`.agents/notes/implemented/architecture/2026-10-23-store-scoped-image-reads.md:13`).
    The Go design should confirm this is acceptable once several principals exist
    (row 70).
11. **Windows extra directories (row 113).** Upstream defers process-level enforcement
    (`.agents/notes/implemented/architecture/2026-10-29-windows-file-tool-directory-grants.md:15`).
    The sandbox area decides whether the Go Windows backend can lift the limit.
12. **Migration progress (row 115).** The proposal leaves open what "done" means
    (`.agents/notes/proposed/architecture/2026-09-29-session-migration-follow-redirect.md:21`).
    The session area must choose before the first Go format migration.

[n1]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-16-creator-persistent-plugin-management.md
[n2]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-16-experimental-voice-input.md
[n3]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-16-host-schedule-storage.md
[n4]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-16-user-terminal-permissions.md
[n5]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-17-account-provider-signout.md
[n6]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-17-developer-session-changes.md
[n7]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-17-experimental-cua-object-api.md
[n8]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-17-native-v4-read-validation.md
[n9]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-17-persistence-attribution-policy.md
[n10]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-17-process-local-blank-sessions.md
[n11]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-17-standalone-schedules.md
[n12]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-17-workspace-file-binary-transfer.md
[n13]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-18-declarative-agent-presets.md
[n14]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-18-plugin-install-registries.md
[n15]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-18-volatile-config-references.md
[n16]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-19-concrete-web-bind-address.md
[n17]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-19-profile-owned-live-configuration.md
[n18]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-19-profile-resolution-lookup-order.md
[n19]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-19-projection-cache-listing-identity-and-cached-rows.md
[n20]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-19-remote-duplex-stream.md
[n21]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-19-timed-user-question-two-settlements.md
[n22]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-19-window-drag-coverage-contract.md
[n23]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-20-device-local-shortcut-preferences.md
[n24]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-20-dynamic-tool-updates.md
[n25]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-20-sidebar-retained-tab-layout.md
[n26]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-21-conversation-build-groups.md
[n27]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-21-desktop-page-close-shortcuts.md
[n28]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-21-experimental-capabilities-as-optional-bundles.md
[n29]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-22-fatal-diagnostics-and-crash-reports.md
[n30]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-22-platform-browser-storage.md
[n31]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-22-schedule-hard-delete-and-bounded-history.md
[n32]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-22-tool-call-three-phases.md
[n33]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-23-best-effort-persistent-ptc.md
[n34]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-23-desktop-close-to-background-and-quit-confirmation.md
[n35]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-23-native-https-web-listener.md
[n36]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-24-bounded-session-log-upload.md
[n37]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-24-deepseek-account-remote-model-catalog.md
[n38]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-24-preparing-tool-arguments.md
[n39]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-24-preset-scoped-schedule-tools.md
[n40]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-24-preset-scoped-time-context.md
[n41]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-24-scheduled-message-framing.md
[n42]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-24-shared-time-vocabulary.md
[n43]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-25-session-log-otel-byte-limits.md
[n44]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-26-cua-application-consent.md
[n45]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-27-progressive-office-skills.md
[n46]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-27-web-search-offered-by-provider-readiness.md
[n47]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-29-explicit-session-history-preparation.md
[n48]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-30-profile-package-refresh-and-manifest-invalidation.md
[n49]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-30-structured-draft-initialization.md
[n50]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-01-replace-changed-package-modules-on-recomposition.md
[n51]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-05-anonymous-reasoning-translation.md
[n52]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-05-official-on-demand-bundles.md
[n53]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-05-paid-native-flash-reasoning-translation.md
[n54]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-05-persistent-machine-translation.md
[n55]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-05-shared-agents-root-instructions.md
[n56]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-06-web-desktop-plugin-identity-compatibility.md
[n57]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-08-desktop-shell-configuration.md
[n58]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-08-persistent-ptc-sandbox.md
[n59]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-09-composer-command-chip.md
[n60]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-09-cua-shared-native-helper.md
[n61]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-09-message-node-factories-and-delegates.md
[n62]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-09-operator-scoped-connect-handshake.md
[n63]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-09-ptc-owned-policy-cleanup.md
[n64]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-09-reasoning-content-slot-factory.md
[n65]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-09-stored-credential-precedence.md
[n66]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-10-user-text-json-nodes.md
[n67]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-11-chat-group-tree-exchanges.md
[n68]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-12-exclusive-conversation-matches.md
[n69]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-12-headless-environment-auth.md
[n70]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-12-peer-principal-and-remote-access.md
[n71]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-12-session-projection-registration-handles.md
[n72]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-13-chat-single-fold-motion.md
[n73]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-13-context-breakdown-system-prompt-state.md
[n74]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-13-cua-continuous-hover.md
[n75]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-13-cua-hover-routes-and-coordinates.md
[n76]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-13-team-identity-runtime-context.md
[n77]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-14-plugin-entry-customization.md
[n78]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-14-registry-bundle-updates.md
[n79]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-14-webworker-preview-profile-parity.md
[n80]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-15-agent-teams-as-subagents.md
[n81]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-16-separate-instruction-chain-budgets.md
[n82]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-16-steering-batch-start-in-definition-state.md
[n83]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-16-tree-wide-agent-communication.md
[n84]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-18-session-list-summary-projections.md
[n85]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-18-websocket-permessage-deflate.md
[n86]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-19-client-plugin-package-assets.md
[n87]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-19-cua-profile-consent-storage.md
[n88]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-19-file-drag-plugin-ownership.md
[n89]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-19-pi-ai-system-reminder-fallback.md
[n90]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-19-team-task-board-inbox.md
[n91]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-20-new-session-blank-reuse-by-preset.md
[n92]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-20-ptc-script-evaluation-and-retention.md
[n93]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-20-session-team-mode.md
[n94]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-20-workspace-tree-state-per-pane.md
[n95]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-21-context-menu-contributions.md
[n96]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-21-replaceable-icon-sprite.md
[n97]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-21-schedule-rule-storage.md
[n98]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-21-session-history-stream-and-snapshot.md
[n99]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-21-when-access.md
[n100]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-22-deepseek-official-model-listing.md
[n101]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-22-subagent-target-user-controls.md
[n102]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-22-subagent-tool-owned-communication.md
[n103]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-23-independent-session-forks.md
[n104]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-23-store-scoped-image-reads.md
[n105]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-25-cua-bundle-id-consent.md
[n106]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-26-cross-deployment-office-kit.md
[n107]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-26-session-writable-directory-effects.md
[n108]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-27-plugin-cli-absolute-paths.md
[n109]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-28-upstream-skills-runtime-download.md
[n110]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-29-cua-dev-signing-identity.md
[n111]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-29-retained-ptc-interruption.md
[n112]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-29-web-provider-selection.md
[n113]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-10-29-windows-file-tool-directory-grants.md
[n114]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/architecture/2026-09-20-unified-typert-remote-metadata.md
[n115]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/architecture/2026-09-29-session-migration-follow-redirect.md
[n116]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/architecture/2026-10-09-cordis-cleanup-dependency-lifetimes.md
