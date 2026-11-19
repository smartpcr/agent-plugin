# Upstream decisions, part 1: architecture notes

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

Upstream deepseek-harness records each lasting design decision in a file it calls an
**Agent Note**: "the *why* and *what we gave up*, the parts code and docs can't carry"
(`.agents/notes/README.md:5`). A note's path encodes two things
(`.agents/notes/README.md:9`):

- **Lifecycle**, the top-level folder. `implemented/` means the decision shipped and
  the note is kept current with the code; `proposed/` means it was reviewed but is not
  built, or only partly; `rejected/` means it was considered and declined, and is kept
  only because its reasoning prevents a tempting mistake
  (`.agents/notes/README.md:12-14`).
- **Class**, the nested folder: `feature`, `bug-fix`, `simplification`, `architecture`,
  `process` or `testing` (`.agents/notes/README.md:25-32`). Upstream calls this the
  class; the Go ledger calls it the note's category.

Every note in this batch is in the `architecture` class, which upstream defines as "a
structural decision about the shipped source" (`.agents/notes/README.md:30`). That is
why almost every row below is a product decision and very few are about tooling.

The notes matter to the Go redesign because they hold the reasons behind upstream's
structure. [DH-ADR-2](../design.md#dh-adr-2) makes upstream the source of requirements
and not a contract, so each decision has to be judged: keep it, reach the same goal
another way, or drop it. The traceability check ([DH-ADR-6](../design.md#dh-adr-6))
requires a verdict for every note.

## How to read the tables

Each row is one note. The columns are:

| Column | Meaning |
|---|---|
| # | Position of the note in the assignment file `trace/assignments/D1.jsonl`. It is only a handle for cross-references inside this document. |
| Note | The note's title, linked to the note at the pinned upstream commit. |
| Summary | The decision in one or two plain sentences. |
| Kind | What sort of decision it is, judged from its content: product architecture, product feature, bug fix, simplification, testing practice, development process or documentation. This is independent of upstream's class. |
| Area | The upstream path the decision is mainly about. "(cross-cutting)" means it applies across the product or is about process. |
| First verdict | The ledger disposition proposed for the Go redesign, with any global decision it rests on. |
| Why | The reason for the verdict. |

The verdicts are the ledger dispositions defined in [../trace/README.md](../trace/README.md):

| Verdict | Meaning here |
|---|---|
| **ported** | The Go design should keep this decision, expressed in Go. |
| **replaced** | The goal stands, but the Go design reaches it by a different mechanism. The link names the global decision that forces the change: [DH-ADR-2](../design.md#dh-adr-2) (Go-native mechanisms and formats), [DH-ADR-3](../design.md#dh-adr-3) (HTML rendered by the Go host instead of the React client) or [DH-ADR-4](../design.md#dh-adr-4) (compiled-in components and plugin processes instead of npm plugin loading and hot reload). |
| **dropped** | The Go design should not do this. The link names the decision that explains why. |
| **not-applicable** | The note is about upstream's development process, TypeScript or npm tooling, or contributor documentation. This verdict is final. |

**These are first verdicts.** A `ported`, `replaced` or `dropped` verdict here is a
recommendation. The agent that designs the affected area writes the final row and links
it to the deliverable or decision that carries it. A `ported` row with no link in this
document is therefore expected, not an omission. Inference: where a row's "Why" says how
Go would do something, that is this document's reasoning, not something read from
upstream.

For a **proposed** note the verdict answers "should the Go design adopt the idea?".
`ported` means adopt it. For the **rejected** note the verdict answers "does upstream's
rejection still hold in Go?".

## Counts

By first verdict:

| Verdict | Implemented | Proposed | Rejected | Total |
|---|---:|---:|---:|---:|
| ported | 70 | 7 | 0 | 77 |
| replaced | 30 | 2 | 1 | 33 |
| dropped | 1 | 1 | 0 | 2 |
| not-applicable | 2 | 2 | 0 | 4 |
| **Total** | **103** | **12** | **1** | **116** |

Of the 33 `replaced` rows, 18 rest on DH-ADR-3 (the user interface), 9 on DH-ADR-4
(plugins and packaging) and 12 on DH-ADR-2 (Go-native mechanisms); 6 rows rest on two
of them.

By kind of decision:

| Kind | Notes |
|---|---:|
| product architecture | 95 |
| product feature | 9 |
| simplification | 6 |
| development process | 3 |
| bug fix | 2 |
| documentation | 1 |
| testing practice | 0 |
| **Total** | **116** |

## Category: architecture

All 116 notes are in upstream's `architecture` class. They are listed in three tables by
lifecycle, in upstream's date order.

### Implemented upstream (103 notes)

| # | Note | Summary | Kind | Area | First verdict | Why |
|---:|---|---|---|---|---|---|
| 1 | [Source-owned session immutability][n1] | The session validates each event, takes a detached JSON copy and deep-freezes it before it enters the log, so no caller can later rewrite stored history. The protection is always on and lives in the session, not in an optional development plugin. | product architecture | `packages/core/session` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2)) | The goal (history that no holder of a reference can rewrite) stands, but JavaScript's Object.freeze has no Go equivalent; the Go session should own an encoded or copied record and hand out copies or read-only views instead. |
| 2 | [Event-sourced sessions with derived message history][n2] | A session is an append-only log of typed events and is the single source of truth; the message history sent to the model is derived from it, and replay or fork means seeding a new session with an existing log. Appends never wait for disk; persistence buffers writes and drains them at a flush checkpoint at each turn end. | product architecture | `packages/core/session` | **ported** | Event sourcing does not depend on the language and is what makes replay, fork and request reconstruction possible, so the Go design should keep it. |
| 3 | [Capability seams — Service Definition / Service Provider / Consumer roles][n3] | Every swappable capability is split into three roles: a Service Definition (the contract), a Service Provider (one implementation) and a Consumer (the tool or plugin that uses it). Consumers never import a provider's types. | product architecture | (cross-cutting) | **ported** | The split maps directly onto a Go interface, a component that registers an implementation, and a tool that depends only on the interface; it is what lets local, sandboxed and remote providers be swapped without changing the tool schemas the model sees. |
| 4 | [Two LLM adapters as a design-verification twin][n4] | Two model adapters built on different internals are shipped against the one streaming vocabulary from the start, so that anything the vocabulary cannot express for both is treated as a vocabulary bug. | product architecture | `packages/llm` | **ported** | The Go design should keep proving the provider-neutral stream contract with two independent adapters; the second one cannot be the npm pi-ai library, so the LLM area must name a Go counterpart. |
| 5 | [Session surface — an ordered projection over the event log][n5] | The ordered list of events that produce model messages (the surface) is derived and cached, and is maintained by markers in the log. Two operations, append and replace-range, let plugins such as compaction rewrite history durably and record which events each replacement used. | product architecture | `packages/core/session` | **ported** | Surface operations are what make compaction and other history edits replayable and checkable; nothing about them depends on JavaScript. |
| 6 | [Bounded recovery for transient LLM request failures][n6] | A failed model request carries one serializable failure record (message, code, status, retry-after, request id) with no policy in it. A listener on the request-error extension point decides whether to retry, within fixed bounds. | product architecture | `packages/llm/llm-retry` | **ported** | Separating failure facts from retry policy keeps adapters simple and recovery bounded; the Go design should keep the same split. |
| 7 | [Web capability seam - stable tools over multiple providers][n7] | Web access is one capability: a web service owns provider registration and selection, providers register search or fetch backends, and one tool package owns the model-facing web_search and web_fetch schemas. Providers never register tools. | product architecture | `packages/web` | **ported** | This is the capability-seam pattern applied to web access; it keeps the model contract stable while search vendors with very different response shapes come and go. |
| 8 | [Event-domain semantics — session is the fact log, agent is the live event channel][n8] | Events fall into three domains with one job each: session events are the durable, replayable fact log; agent events are live runtime signals that carry the live agent; tools events are the tool registry and execution pipeline. A fact that must survive replay goes in the session log, never only on a live event. | product architecture | `packages/core` | **ported** | The boundary rule keeps live rendering and replay on one path; it is a design rule, not a language feature, and the Go event taxonomy should follow it. |
| 9 | [Tagged render-intent union for tool-call presentation][n9] | A tool declares how its call and its result are shown through a tagged union (generic, terminal or diff card) with a required tag, instead of a bag of optional fields. | product architecture | `packages/core/tools` | **ported** | A closed, tagged set of card kinds is easy to express in Go and gives the server-side renderer one switch; keep the model. |
| 10 | [Prompt variables and tool-guidance ownership][n10] | Every fact in the system prompt has exactly one owner: the loop supplies route facts such as the model name as variables, each tool owns its own description and usage guidance, and the persona owns role and behaviour. Prompt assembly takes a per-agent context. | product architecture | `packages/core/system-prompt` | **ported** | Single ownership of prompt facts prevents drift between what the harness knows and what the prompt says; keep it. |
| 11 | [Every LLM request is reconstructable from the session log][n11] | Anything that reaches a model request must be reconstructable from the session log and the content-addressed objects it references, so anyone with the log and the same code version can rebuild every request byte for byte. Stable request prefixes for provider caching follow from this. | product architecture | `packages/core/agent-loop` | **ported** | Reconstructable requests give replay equivalence and provider prefix-cache hits; this is a core invariant worth keeping. |
| 12 | [A shared timeout/deadline primitive, with hard-kill left to each capability][n12] | A small library of pure functions owns timeout timing and classification (clamp a requested timeout, create a deadline, tell a timeout from a cancellation); each capability keeps its own hard kill. There is deliberately no central timeout service. | product architecture | `packages/util/timeout` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2)) | Go's standard context package already provides deadlines, cancellation and a recorded cause, so a separate timeout library is unnecessary; the rule that each capability owns its own hard kill is kept. |
| 13 | [Tool-call timeout policy as a plugin][n13] | Tool-call timeout is a policy plugin: the tool pipeline has an around-dispatch extension point, and the plugin wraps any tool that declares a timeout with a derived cancellation signal. A tool that declares no timeout gets none. | product architecture | `packages/guard/timeout-policy` | **ported** | An around-dispatch hook plus a policy component is the right shape in Go too, with a derived context carrying the deadline. |
| 14 | [Tool output spill policy][n14] | An oversized tool result is replaced by a bounded preview plus a locator to a private spill file that holds the complete text, which the model can read later with ordinary file tools. A thin storage interface, a local backend and a policy plugin are separate packages. | product architecture | `packages/spill` | **ported** | Bounded previews with retrievable full output protect the context window without losing data; the three-role split ports directly. |
| 15 | [Single-file executable SDK runtime distribution (single-exe)][n15] | The Python SDK ships a single-file executable built with the pkg tool in Node single-executable mode. It serves JSON-RPC over standard input and output, and the plugins it starts are chosen by a configuration file supplied from outside. | product architecture | `scripts` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2), [DH-ADR-4](../design.md#dh-adr-4)) | The goal (one executable per platform that needs no runtime installed and serves the SDK protocol) stands, but a Go binary is already a single file, so the pkg tool, its virtual filesystem and its patch are not needed; composition comes from configuration over compiled-in components. |
| 16 | [Provider-routed LLM adapters and a generic pi-ai backend][n16] | Model adapters are registered by provider name, and every request carries a provider and a model; one provider has exactly one adapter owner. A generic adapter can serve many providers. | product architecture | `packages/llm/llm` | **ported** | Separating which provider owns a request from which model it runs is needed for gateways and for switching models inside a session; keep it, with a Go counterpart for the generic pi-ai backend. |
| 17 | [Initiating Agent scope over AsyncLocalStorage][n17] | The agent that started an operation is carried implicitly through asynchronous calls with Node's AsyncLocalStorage, so deep infrastructure can ask which agent initiated the work without it being passed as a parameter. | product architecture | `packages/core/agent` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2)) | Go has no async-local storage; the same need is met by carrying the initiating agent as a value in the context.Context that every call already receives. |
| 18 | [Cooperative tool cancellation at the registry boundary][n18] | Every tool invocation must carry a cancellation signal owned by its caller; the registry supplies no default. Only the around-dispatch stage may replace the signal, for example to add a deadline. | product architecture | `packages/core/tools` | **ported** | This is already the Go convention: a context.Context passed as the first argument, with wrappers deriving child contexts. |
| 19 | [Routed model context and compaction policy][n19] | Each adapter reports the context window for an exact provider and model route; token measurement stays model-agnostic; compaction thresholds are resolved per route instead of from one global window. | product architecture | `packages/compaction` | **ported** | Per-route capacity is required as soon as one process talks to models of different sizes; keep it. |
| 20 | [The slot system standard — single register, four props shares, and the framework store seat][n20] | The browser page is composed at run time from plugins through one slot registry: only the root slot exists up front, and a plugin registers into a slot, declares its child slots and its store, and supplies pure React components whose props are derived from four sources. | product architecture | `packages/client/ui-slots` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The need (features contribute UI into named regions owned by a parent) stands, but with server-rendered HTML it becomes named template regions that Go components fill on the server; React props, stores and hooks do not exist. |
| 21 | [Client plugin loading — lazy factories, Cordis lifecycle, and hot reload][n21] | The browser runs the same plugin mechanism as the host: a client module system loads lazy plugin factories, the plugin loader governs their lifecycle, and changed plugins are hot-reloaded in the page. | product architecture | `packages/client/modules` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3), [DH-ADR-4](../design.md#dh-adr-4)) | The Go design has no JavaScript plugin runtime in the browser; UI contributions are rendered by server components, and a change reaches the page by re-rendering, not by reloading modules. |
| 22 | [Separate context injection from turn execution][n22] | inject() is the only way to add supplementary model-facing input, and a turn means exactly one execution of the model loop. Each injected context is an ordinary identified user message whose source names its producer. | simplification | `packages/core/agent` | **ported** | One path for injected context removes three overlapping mechanisms and keeps the log uniform; keep it. |
| 23 | [One harness home resolver][n23] | One resolver decides where harness user data lives: an explicitly configured path, then the DSH_HOME variable, then ~/.dsh. An empty variable counts as unset, and all user data stays under that one root. | simplification | `packages/util/home-paths` | **ported** | A single home resolver prevents components from disagreeing about where data lives, and it is trivial to keep in Go. |
| 24 | [Compiler-independent Typert type model][n24] | The Typert generator uses the TypeScript compiler only to extract types, then works from its own compiler-independent model of packages, services, events and types when it emits runtime schemas and reflection data. | development process | `packages/typert/generator` | **not-applicable** | TypeScript tooling: the generator exists because TypeScript types are erased at run time. Go types are available at run time through reflection and at build time through the standard go/types packages, so there is no separate type model to design. |
| 25 | [One carrier-level browser-trust boundary for all `/api` routes][n25] | Browser trust is enforced once for the whole /api prefix: every POST must declare application/json, and every request must present a Host that is loopback or explicitly trusted, with any Origin matching it. This blocks cross-site simple requests and DNS rebinding against the local API. | product architecture | `packages/client/connection` | **ported** | A local HTTP API that can run shell commands needs these two fences whatever the server language; in Go they become one middleware in front of every route, including the htmx and server-sent-event routes. |
| 26 | [Create every message as an identified immutable value][n26] | Every message (user, assistant or tool result) gets an opaque id when it is created and keeps it through the inbox, the durable log and the model request. A message is immutable after creation. | product architecture | `packages/llm/llm` | **ported** | One identity per message through every representation removes correlation guesswork; Go keeps the rule and enforces immutability by not exposing mutable fields. |
| 27 | [Portable consumers over filesystem and subprocess execution worlds][n27] | The filesystem service and the subprocess service together define one execution world; terminals, language servers and shells consume those two interfaces instead of host APIs, so a remote provider needs no separate terminal or language-server package. | product architecture | `packages/subprocess/subprocess` | **ported** | This is what lets SSH and sandboxed execution work without duplicating consumers; the Go design should define the same two interfaces. |
| 28 | [dsh source launch through the tsx ESM hook][n28] | The dsh command is launched from TypeScript source through the tsx ESM hook, because Node 26 removed the flag the earlier launch method relied on. | development process | `apps/cli` | **not-applicable** | TypeScript tooling: a Go program is compiled, so there is no source-launch path, transformer hook or path mapping to choose. |
| 29 | [Projected token usage and context occupancy][n29] | Token totals and context occupancy are durable session projections folded from the whole log, so they survive paging and compaction and include failed attempts. The browser no longer derives them from the conversation nodes it happens to have loaded. | product architecture | `packages/llm/token-meter` | **ported** | Billing and context-pressure numbers must come from the full log, not from a window of the UI; keep the projections. |
| 30 | [Terminal LLM stream failures][n30] | The LLM runtime is the one place that turns an adapter failure into a terminal finish record (error or aborted). Errors from middleware, consumers or message assembly stay thrown as defects and never enter request recovery. | product architecture | `packages/llm/llm` | **ported** | One normalization boundary gives the loop a single failure shape to handle; in Go the adapter returns an error or a terminal chunk and the runtime normalizes both. |
| 31 | [credential boundaries, whole-snapshot requests, and atomic route registration][n31] | The stored credential document belongs to the credential provider alone and is never loaded into the process environment, so a key stored from the UI stays writable and rotatable. The documentation states plainly that stored credentials are not protected from the model's own tools. | product architecture | `packages/credentials/credentials-local` | **ported** | Keeping stored secrets out of the ambient environment, and being honest about where the boundary is, are both required in Go. |
| 32 | [Follow-up enqueue and owned run boundaries][n32] | Sending a follow-up only enqueues the message and returns its id; no single message owns the activity that follows. Only high-level run() helpers return a result, defined as the last committed assistant message between the enqueue receipt and the next idle state. | product architecture | `packages/core/agent` | **ported** | Steering, injected context and queued messages make per-message results unsound; the enqueue-only contract with explicitly owned runs should be kept in the Go SDK protocol. |
| 33 | [Goal-owned durable events][n33] | Goal state is recorded by its own durable goal/change event that carries the whole goal snapshot and is appended by the goal service; it is no longer encoded inside an inbox message. | product architecture | `packages/goal/goal` | **ported** | A domain's durable state should not depend on message scheduling; keep the domain-owned whole-value event. |
| 34 | [Typert Gateway Targeted Method Calls][n34] | A business service marks methods as remotely callable with a decorator; the Typert generator produces a typed client projection from them, and the API gateway carries each call as one request and one result. | product architecture | `packages/api/gateway` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3), [DH-ADR-2](../design.md#dh-adr-2)) | The browser no longer calls typed methods from generated TypeScript: the Go host handles form and htmx requests directly, and remote callers such as the SDKs use the JSON-RPC protocol, so decorators and generated projections are not needed. |
| 35 | [One ordering for configuration sources, and what a discovered file may not decide][n35] | Every non-secret setting resolves in one order: explicit value for this run, user settings, composition, the launching shell's environment, discovered .env files, then defaults. A discovered .env file may not set sensitive names such as PATH. | product architecture | `packages/boot/app-boot` | **ported** | One precedence order with a known origin for each value is what makes a configured value explainable; keep it in the Go configuration loader. |
| 36 | [Profile plugin bundles replace the fixed surface overlays][n36] | Every launch mode is a profile: a directory with a package.json that lists npm plugin dependencies and an ordered list of bundles, each bundle being an npm package that contributes a configuration patch, plus the user's own patch. | product architecture | `packages/bundle` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | Layered profiles and bundles are kept as configuration, but a bundle cannot be an npm package installed by pnpm; in Go a bundle is a named configuration layer over compiled-in components plus declared plugin processes. |
| 37 | [Client Conversation business-node assembly and keyed Chat snapshots][n37] | The browser builds the conversation view through an assembly engine: business plugins register event definitions, view plugins register builders, and the client session class only feeds the engine a window of events. | product architecture | `packages/client/ui-conversation` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The fold from session events to conversation nodes is still needed, but it runs in the Go host, which renders the nodes as HTML fragments; the client-side engine and keyed React snapshots go away. |
| 38 | [Cancelled streams finalize their delivered prefix][n38] | When a model stream is cancelled, the text the user already saw is committed as an assistant message marked interrupted, so follow-up prompts and forks include it. Tool calls in the cancelled output are left out. | bug fix | `packages/core/agent-loop` | **ported** | The model history must contain what the user read; the rule does not depend on the language. |
| 39 | [Forked children preserve the parent request prefix][n39] | A forked child agent keeps the same tool list and system prompt prefix as its parent and receives its task after the inherited history, so the provider can reuse the cached request prefix. | product architecture | `packages/subagent/subagent-fork-in-process` | **ported** | Prefix reuse is the whole payoff of fork over spawn; keep the byte-identical prefix rule. |
| 40 | [What stays host-plane once presets own the agent plane][n40] | After per-session agent presets took over model-facing configuration, services whose state is per process, such as the token meter, tasks and goals, stay in the host composition; entry points join agents to presets explicitly. | product architecture | `packages/preset/agent-preset` | **ported** | The Go design needs the same line between per-process services and per-agent composition, whatever form presets take. |
| 41 | [Session log versioning — one integer, an upgrade chain, and a per-event ignorable marker][n41] | The session log format has one integer version. A reader upgrades older logs through a chain of single-step upgraders, refuses newer logs with a message that names the direction, and may skip an unknown event only if that event is marked ignorable. | product architecture | `packages/session/session-format` | **ported** | The Go format is new, but it needs the same rules from its first release, because the refusal behaviour of the first shipped reader can never be added to copies already installed. |
| 42 | [Credential records and authorization flows][n42] | Credentials gain a second kind: besides an API key behind an environment-variable name, a plugin can hold an opaque grant record under a key scoped by the owning plugin, obtained through a registered authorization flow such as a browser login. | product architecture | `packages/credentials/credentials` | **ported** | OAuth-style grants are needed for account login and third-party providers; keep the two key spaces and plugin-scoped ownership, with the pi-ai specifics replaced by the Go LLM adapters. |
| 43 | [Client shell layering and dynamic package boundaries][n43] | The browser application is layered into a compilation shell, a framework-free startup kernel, static shared libraries and dynamically loaded client packages, with explicit rules for what each may bundle or import. | product architecture | `packages/client/web` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | These layers exist to control what a JavaScript bundler inlines and which module instances are shared. With server-rendered HTML there is one shell template and a few self-contained script islands, so the layering has no counterpart. |
| 44 | [Session history, control state, and Remote event transport][n44] | Browser data comes in three kinds with different recovery rules: the session log is paged and followed by sequence cursor, control state is sent as complete snapshots, and notifications are fire-and-forget. The API gateway owns the stream transport; controllers own their own wire types. | product architecture | `packages/api/gateway` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3), [DH-ADR-2](../design.md#dh-adr-2)) | The three recovery rules are kept, but the carrier changes from WebSocket remote streams consumed by a client runtime to server-sent events that carry rendered fragments and resume by sequence number. |
| 45 | [Session projections as a required reader seam][n45] | A plugin whose behaviour reads projected session state must declare the projection registry as a required dependency and wait for it; a missing registry or key is an error, never a silent default. | product architecture | `packages/session/session-projection` | **ported** | Required dependencies that fail loudly are how the Go cordis port already models services; keep the rule. |
| 46 | [Client Session, Conversation, and UI ownership layers][n46] | The web client is layered as controller and domain object, then UI adapter, then renderer, then slot component; domain objects publish React-free observable sources and components read only from slot props. | product architecture | `packages/client` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The ownership idea (domain state is separate from presentation) is kept, but the layers collapse: the Go host holds the domain state and renders templates from it, so there are no observable sources, adapters or hooks in the browser. |
| 47 | [DeepSeek LLM API request extensions for session logs and plugin packages][n47] | A registry lets plugins add extra top-level fields to requests sent to the official DeepSeek API; it is used to upload the session log and the list of plugin packages. Extensions are best effort and never fail the model request. | product feature | `packages/llm/deepseek-llm-api-extensions` | **ported** | It is a product feature of the official DeepSeek adapter and should be kept; the package-list field must be redefined for compiled-in components and plugin processes. |
| 48 | [One dsh launcher for application profiles][n48] | Every application process starts through the one dsh command and a named profile (web, headless, sdk, sdk-minimal, acp); there are no separate application binaries with their own complete configuration. | product architecture | `apps/cli` | **ported** | One launcher that owns composition, environment discovery and shutdown suits a single Go binary even better; keep it. |
| 49 | [Client-Derived Presentation from Raw Session Tool Events][n49] | Session history sent to the browser contains only raw persisted events; the client derives tool cards from the tool name, arguments and result, and the host does not run tool presenters while reading history. | product architecture | `packages/client/ui-tool` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The rule that history reads stay free of the tool registry is kept, but the derivation moves to the Go host's rendering layer, which builds card HTML from the raw events. |
| 50 | [Locale-owned client UI copy][n50] | All product-authored wording in the client comes from typed locale dictionaries; data authored by users, models or providers is shown verbatim, and shared UI primitives receive their text as required props with no built-in language fallback. | product architecture | `packages/client/locale` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The rule (product text comes from a catalogue, data is never translated) is kept, but the catalogue is read by Go templates on the server instead of being passed as typed React props. |
| 51 | [Browser launch-token authentication][n51] | The host authenticates every API call before dispatch. It creates a random token at launch, the browser exchanges it once for a signed cookie, and the Host and Origin checks still run first. | product feature | `packages/host/authentication-token` | **ported** | A loopback Host header is not proof of local authority, so token authentication is required for the Go server as well. |
| 52 | [Package and update the Electron desktop application][n52] | The desktop application is a small Electron shell with a pinned pnpm. One release number fixes the exact harness version, the application owns a reserved desktop profile, and an update replaces the whole release. | product architecture | `apps/desktop` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3), [DH-ADR-4](../design.md#dh-adr-4)) | The goals (no runtime to install, one release identity, a reserved profile, one update flow) stand, but the shell is a Go webview application, and there is no bundled pnpm because plugins are not npm packages. |
| 53 | [Session observations and projection-owned client state][n53] | Every exact read of a session goes through one retained observation that picks the source (live or stored) and fixes one immutable read cut; values shown to clients come from registered projections over that cut, never from client-side reconstruction. | product architecture | `packages/session-query/session-query` | **ported** | One consistent read cut prevents the header, events and projections from describing different moments; keep it as the read path of the Go session service. |
| 54 | [Adjacent Agents share one Steer send_message operation][n54] | Parent and child agents use one send_message operation in both directions; it always delivers by steering the target and records one durable source that names the sender. | simplification | `packages/subagent/subagent` | **ported** | One operation for messages between adjacent agents replaces two direction-specific tools and schemas; keep it. |
| 55 | [Handle-based session persistence][n55] | Session persistence is five service methods (create, open, stat, list, flush) that return per-session handles with read, append, flush and close. Ownership is the handle, not a shared coordinator that adopts sessions by listening to events. | product architecture | `packages/session/session-persistence` | **ported** | Explicit handles make ownership and release clear and map naturally to a Go interface with a Close method; keep it. |
| 56 | [One outbound proxy policy, installed before anything can request][n56] | One outbound proxy policy is resolved once from the launch environment and installed globally before any plugin can make a request, because Node's fetch ignores the proxy variables. The project's .env file may not set a proxy. | product architecture | `packages/util/http-proxy` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2)) | Go's HTTP client honours the proxy variables by default, so nothing has to be installed globally; the Go design keeps the policy (resolved once from the launch snapshot, project .env refused) by giving every component one shared HTTP transport. |
| 57 | [One Remote failure vocabulary for ctx.remote][n57] | All remote calls report failure through one error type that carries a code and typed details; domains no longer define their own error families and mapping functions. | simplification | `packages/typert/protocol` | **ported** | One coded error type across the API boundary is simple to keep in Go and serves the JSON-RPC protocol used by SDKs and plugin processes. |
| 58 | [Retain ignorable session events for external plugins][n58] | The session event envelope keeps its optional ignorable marker, and one helper writes ignorable plugin records in a plugin namespace, because a third-party plugin depends on readers accepting its informational events. | product architecture | `packages/core/session` | **ported** | Plugin processes in the Go design will write their own events, and a host without that plugin must still be able to read the session, so the marker and the plugin namespace are needed. |
| 59 | [Alpha Session migration refuses every unknown historical event][n59] | The migration from the alpha v0 session format refuses any event type it does not know, even one marked ignorable, because a migration must prove that every payload it copies is still valid. | product architecture | `packages/session/session-format-v0-to-v1` | **dropped** ([DH-ADR-2](../design.md#dh-adr-2)) | The Go design does not read upstream session files, so there is no v0 format to migrate. The general lesson (a format migration refuses what it cannot validate) belongs to the versioning rules of the Go session format. |
| 60 | [Explicit Agent identity at runtime boundaries][n60] | Agent identity is passed explicitly wherever it is needed (setup functions, event payloads, remote requests); a cordis context owns registrations and cleanup only and offers no way to look up its agent. | product architecture | `packages/core/agent` | **ported** | Keeping lifetime ownership (the context) apart from domain identity (the agent) avoids a class of wrong-agent bugs, and explicit parameters are the Go norm. |
| 61 | [Released Session formats migrate through stateful streaming stages][n61] | Old session formats are upgraded by streaming stages, one per version step, each holding only its own state. This replaced a whole-file migration that exhausted memory on a 116 MB session. | product architecture | `packages/session/session-format` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2)) | The Go design starts at its own format version and has no upstream formats to migrate, but any future upgrade step must stream with bounded memory, which is the rule this decision establishes. |
| 62 | [Parent-owned subagent catalog events][n62] | The parent session records each direct child agent in its own durable catalog events, which are the authority for finding children; nothing scans all sessions or opens child logs to discover them. | product architecture | `packages/subagent/subagent` | **ported** | The creator already knows the fact, so recording it where it is created keeps discovery cost independent of the number of sessions; keep it. |
| 63 | [Embed Assistant streams in v2 attempt settlements][n63] | The session log has no per-token chunk events. Each model attempt is stored as one record that embeds its compact timed stream: assistant/message for a visible response, assistant/attempt for a failed or retried one. | product architecture | `packages/core/session` | **ported** | One record per attempt keeps exact stream replay while cutting the event count; the Go format should start in this shape. |
| 64 | [The system prompt is surface node 0][n64] | The system prompt is an ordinary surface event, system/message, placed first; changing the prompt uses the same append and replace operations as any other message. | product architecture | `packages/core/session` | **ported** | Treating the prompt like every other model-visible message removes a special case from request reconstruction; keep it. |
| 65 | [One output ring, one projection, one event stream for background jobs][n65] | Each background job has one output ring that serves both the model's consuming read and observers' offset reads, plus one projection and one event stream; this replaced duplicated vocabularies and listener families. | simplification | `packages/jobs/jobs` | **ported** | One store with two cursors is the simpler and correct shape; keep it. |
| 66 | [Canonical feedback log and request delivery][n66] | Message ratings and remarks are log-only events in the session log itself, not a separate store; edits use per-item versions and reject stale writes. | product architecture | `packages/feedback/message-feedback` | **ported** | Keeping feedback in the canonical log means export and delivery need no second source; keep it. |
| 67 | [Client resource model][n67] | Anything a UI component reads live is a resource identified only by a dsh-resource address; the address's protocol names the one provider that streams it, and components share one copy through a useResource hook. | product architecture | `packages/client/resources` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | Addressable, shared live data is still wanted, but in the Go design an address maps to a server route that renders a fragment and a server-sent-event stream that updates it; the hook and the client cache disappear. |
| 68 | [Explicit-feedback-only upload through OpenTelemetry][n68] | Telemetry upload is mounted in feedback-only mode for everyone: only a new explicit feedback event in a session authorizes uploading that session's history up to that point. Ordinary activity never authorizes an upload. | product feature | `packages/session/session-telemetry-otel` | **ported** | Consent tied to an explicit user action is a product and privacy rule the Go design must keep. |
| 69 | [Historical Session reads prepare before write publication][n69] | Opening an old-format session for reading waits only for the in-memory migration; writing the migrated file happens separately, and only a write open waits for it. | product architecture | `packages/session/session-persistence-jsonl` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2)) | There are no upstream formats to migrate in the Go design; the rule it should keep for its own future upgrades is that a read-only open never writes to storage or waits for publication. |
| 70 | [Workspace file service][n70] | A dedicated host service reads workspace files for the UI with bounded line pages, byte windows and capped whole-file reads, names files by absolute path in the session's execution world, and reports changes; the session controller holds no file code. | product architecture | `packages/api/workspace-files` | **ported** | The Go UI needs the same bounded file reads through the session's filesystem backend; only the client-side provider half becomes server rendering. |
| 71 | [Canonical V3 Session event envelopes][n71] | Session events use one canonical envelope: every model-visible event must carry its surface operation, log-only events may carry only type, sequence, time, data and the ignorable marker, and each fact has exactly one spelling. | product architecture | `packages/core/session` | **ported** | A strict canonical envelope prevents two readers from disagreeing about the same record; the Go format should adopt the same strictness from its first version. |
| 72 | [Prebuilt system primitives][n72] | File locking and the Landlock launcher are shipped as prebuilt native packages per platform, so nothing is compiled when a user installs the harness. | product architecture | `native/system` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2)) | The goal (no compiler needed at install) is met by Go itself: the binary calls the locking and Landlock system calls directly, so there is no native addon family to build and publish. |
| 73 | [Bundle the Desktop runtime and retain external plugins][n73] | The desktop build ships the production package graph as application resources so nothing is installed at first start, while npm plugins installed by the user live in the desktop profile and survive upgrades. | product architecture | `apps/desktop` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3), [DH-ADR-4](../design.md#dh-adr-4)) | A Go desktop binary already contains its runtime; the part to keep is that user-installed plugins and their state live outside the application bundle and survive upgrades, as plugin processes. |
| 74 | [Desktop welcome window material][n74] | The desktop welcome and sign-in window is a separate native Electron window with its own small React bundle and a narrow preload bridge; it reuses the web launch URL for authentication and adds no host endpoint. | product feature | `apps/desktop` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | First-run onboarding with one clear startup owner is kept, but it is a page served by the Go host inside the webview shell; whether the blurred native window material is available depends on the shell library. |
| 75 | [Document preview and file addresses][n75] | Document preview separates watching a file (a shared resource observed by address) from reading its content (ordinary bounded host reads); the host resolves every path through the session filesystem. | product architecture | `packages/client/ui-sidebar-documentpreview` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The host half (bounded, authorized reads) is kept; the observation half moves from a client resource hook to server-sent updates for the preview panel. |
| 76 | [Awaited per-agent initialization][n76] | Agent creation runs its initialization listeners one at a time and waits for each; the agent's first model request starts only after all have finished, and a failure fails creation. | product architecture | `packages/core/agent` | **ported** | Tools and start-up context that are installed asynchronously must be in place before the first request; keep the awaited, ordered initialization. |
| 77 | [Consumer-owned startup strictness][n77] | The application, not the plugin framework, decides which components are required: boot checks a fixed list of entry ids after start-up and fails if a listed, enabled entry is not active; any other inactive entry is only a warning. | product architecture | `packages/boot/app-boot` | **ported** | Best-effort loading with an explicit required list is the right policy for the Go loader too; the list itself changes because the client module entries no longer exist. |
| 78 | [Deprecate synchronous reads of arbitrary Session events][n78] | Reading arbitrary old events from a session synchronously is deprecated, because the full log will not always be held in memory; state needed after resume must come from durable event fields and projections. | product architecture | `packages/core/session` | **ported** | The Go session interface should simply not offer synchronous random access to history; the direction is adopted, and the lint rule and deprecation markers are then unnecessary. |
| 79 | [Producer-owned message sources at the V4 edge][n79] | A message's source kind names its producer directly (for example system-prompt or compact-basic) instead of a generic plugin wrapper; the V3 to V4 migration converts old wrappers with a fixed table. | product architecture | `packages/llm/llm` | **ported** | Producer-owned source kinds are kept as the native shape of the Go format; the V3 to V4 conversion table is not needed because upstream files are not read. |
| 80 | [Add immutable profile resolution generations][n80] | Profile start-up computes one immutable answer to which npm package each name resolves to and installs it into Node's module resolvers; a change publishes a complete successor in one step, with no symlinks or generated proxy packages on disk. | product architecture | `packages/boot/app-boot` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | Node module resolution does not exist in Go. The goal (one atomic, immutable description of the plugin set for a process) is met by the compiled-in component registry plus an immutable manifest of plugin processes. |
| 81 | [Workspace file read authority][n81] | Reading or inspecting a named file follows the session filesystem backend's read authority, so a path outside the workspace is readable when the backend allows it; listing and change watching stay limited to the workspace. | product architecture | `packages/api/workspace-files` | **ported** | One read policy, owned by the filesystem backend, avoids a second inconsistent check in the UI layer; keep it, together with the stated exposure of HTML preview. |
| 82 | [Reusable component factories with local slots][n82] | Beside ordinary slots, a package can register a reusable component factory that any parent may render several times, each parent choosing the components for the factory's local slots. | product architecture | `packages/client/ui-slots` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | Reusable UI assemblies with caller-chosen parts become Go template partials that take named blocks; the React factory registry is not needed. |
| 83 | [Run Desktop through the shared Web application][n83] | The desktop application runs the same web application through the same profile runner; the native shell owns only windows, menus, native dialogs, recovery and updates, and there is no second backend. | product architecture | `apps/desktop` | **ported** ([DH-ADR-3](../design.md#dh-adr-3)) | This is the decision DH-ADR-3 already takes for the Go design: one UI, wrapped by a native shell. |
| 84 | [Durable image offload events][n84] | When a request exceeds a route's image budget, a plugin records a durable image/offload event that names the oldest images to omit, then retries; the omission stays stable across later requests, route switches and compaction. | product architecture | `packages/compaction/compaction-image-offload` | **ported** | Recording the omission keeps the model-visible image set reconstructable from the log; keep it. |
| 85 | [Public package manifest fields][n85] | One public type describes the package.json fields the harness reads for a plugin (name, version, engines and the dsh section); internal packaging metadata is deliberately left out of it. | product architecture | `packages/util/package-manifest` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | Plugin authors still need one documented manifest, but an external plugin is a process with its own manifest file, not an npm package, so the format is new. |
| 86 | [Plugin-owned message projections][n86] | A plugin that owns an event which changes derived message content supplies a pure projection function for it; the session applies it atomically and refuses a log whose required projection is missing. | product architecture | `packages/core/session` | **ported** | This keeps feature-specific message transformations out of the session core while keeping derivation deterministic; keep it. |
| 87 | [POSIX SSH execution providers][n87] | Remote execution uses an OpenSSH alias to reach a POSIX helper installed on the remote host; the filesystem, subprocess and sandbox providers share that helper, with separate SSH channels for administration and for each program stream. | product architecture | `packages/ssh` | **ported** | SSH as the remote execution world is kept; a Go helper binary is easy to cross-compile and install, and the provider structure stays the same. |
| 88 | [Sandboxed Node execution for PTC][n88] | Each model-written program for programmatic tool calling runs in a fresh Node process, confined by the same sandbox provider as shell commands and owned by the subprocess service; no worker thread or persistent kernel remains. | product architecture | `packages/ptc-runtime/ptc-runtime-node` | **ported** | Per-call process isolation under the session's sandbox policy is kept. Open point for the area owner: a Go host has no Node executable of its own, so the guest runtime must be chosen and located. |
| 89 | [Subprocess control pipe][n89] | A subprocess can be given a dedicated control pipe on file descriptor 7, separate from standard output and error, so a host protocol never mixes with program output; consumers own the framing. | product architecture | `packages/subprocess/subprocess` | **ported** | A separate control channel is needed for the same reason in Go and is supported by passing extra files to the child process; the Windows handle-inheritance details need their own Go implementation. |
| 90 | [Browser-use provider registration and Session ownership][n90] | The browser-use service only registers one provider name per composition; each provider brings its own tools through the normal tool pipeline, and browser resources belong to the exact agent and session that opened them. | product architecture | `packages/browser-use/browser-use` | **ported** | Not forcing a common browser action interface before a second real consumer exists is sound, and providers that speak MCP fit the plugin-process model directly. |
| 91 | [Computer-use provider registration][n91] | The computer-use service only registers one provider name per composition and defines no shared action interface; each provider exposes its own tool catalogue. | product architecture | `packages/computer-use/computer-use` | **ported** | The reasoning is the same as for browser use: one active provider, and no premature common interface. |
| 92 | [Session working directory][n92] | Each session has one logged effective working directory, owned by one service and changed only by host operations; tools read it from that owner, and it is not placed in the system prompt. | product architecture | `packages/session/working-directory` | **ported** | One logged owner for the current directory keeps tools consistent and the prompt prefix stable; keep it. |
| 93 | [Reuse the PTC Node sandbox for workflows][n93] | Dynamic workflows run model-written JavaScript inside the same sandboxed process runtime as programmatic tool calling instead of a second launcher; the script VM is not treated as a security boundary. | product architecture | `packages/workflow/workflow-ptc` | **ported** | One sandboxed guest runtime for all model-written code avoids duplicating confinement and cleanup; keep it, subject to the same guest-runtime question as programmatic tool calling. |
| 94 | [Current-profile plugin management shares CLI transactions][n94] | The web plugin manager and the dsh plugin command call the same package operations under one profile lock, and hot reload applies module, include and profile changes through one queue so that a half-written manifest is never loaded. | product architecture | `packages/boot/plugin-manager` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | Shared operations under one writer lock are kept, but installing a plugin no longer runs pnpm and applying it no longer hot-swaps modules: the host starts, restarts or stops plugin processes. |
| 95 | [DeepSeek account browser login][n95] | DeepSeek account login is an authorization flow behind an account service: the provider registers the flow, stores the grant, treats local cancellation as final, and reports success only after the credential is saved. | product feature | `packages/account/deepseek-account` | **ported** | Account login is a product feature; the rules to keep are that local cancellation is authoritative and that success is reported only after the credential is stored. |
| 96 | [Independent LibreOffice kit ownership][n96] | The LibreOffice conversion engine lives in its own repository with its own releases; the harness consumes an exact published version and owns only the adapter, authorization, conversion lifetime and UI. | product architecture | `packages/document/office-kit` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2)) | Keeping the engine outside the harness build is right, but it cannot be consumed as an npm package with a Node worker; the Go host must drive a conversion engine as a separate process. |
| 97 | [Default DeepSeek Session-log upload][n97] | Uploading the session log to the official DeepSeek API is on by default; setting enabled to false turns it off, the value is read for each request, and re-enabling sends whatever was not yet accepted. | product feature | `packages/session/session-log-deepseek` | **ported** | It is upstream's shipped product default and is carried over for parity; because it sends user data by default, the product owner should confirm the default for the Go design. |
| 98 | [Web app-owned routes are document-relative][n98] | The served page declares one base element and every browser reference to an application route is relative to the document, so the application works behind a proxy that mounts it under a path prefix. | product architecture | `packages/host/webserver` | **ported** | Server-rendered pages and htmx requests need the same rule to work behind a prefix-stripping proxy; keep it. |
| 99 | [Bounded shared Office conversion][n99] | Office-to-PDF conversion goes through one shared queue with bounded readers, queue length and source bytes and a result cache keyed by content digest; cancelling one consumer does not cancel a conversion that another consumer shares. | product architecture | `packages/document/office-kit` | **ported** | Bounded, shared conversion is a resource-safety rule that does not depend on the engine; keep it. |
| 100 | [Client Session references, reference sources, and UI status][n100] | In the browser, each view or operation holds its own counted reference to a client session object and names itself as the source; there is no global current session. | product architecture | `packages/client/ui-session` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The rule that there is no global current session is kept, but client-side session objects and reference counts are not needed when each rendered view is bound to a session id on the server. |
| 101 | [Native Desktop fatal recovery][n101] | A fatal start-up failure in the desktop application shows one native dialog per process, independent of the web page, with a bounded diagnostic, a crash-report file, and the choices exit, restart, or disable third-party bundles and restart. | product feature | `apps/desktop` | **ported** | A recovery path that does not depend on the failing page is needed for any webview shell; disabling third-party bundles becomes disabling plugin processes. |
| 102 | [First-class tool-role messages][n102] | A tool result is stored as its own tool-role message, matching what model providers expect, instead of a block inside a user message. | product architecture | `packages/core/session` | **ported** | The Go format should store tool results as tool-role messages from its first version; the V3 to V4 conversion is not needed. |
| 103 | [Guided plugin installation][n103] | The host inspects an install request before running it and reports one of seven specific problems; a failed or cancelled installation restores the profile's files. | product feature | `packages/boot/plugin-manager` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | Inspecting before installing and rolling back on failure are kept, applied to plugin process packages instead of pnpm and package.json. |

### Proposed upstream (12 notes)

Where it matters for the verdict, the summary says how much of the proposal upstream has built, as far as the pinned checkout shows.

| # | Note | Summary | Kind | Area | First verdict | Why |
|---:|---|---|---|---|---|---|
| 104 | [Required cancellation through tool-reachable capability seams][n104] | Proposal (not built upstream): every asynchronous capability operation reachable from a tool must take a cancellation signal as a required parameter, so cancellation cannot be dropped between a tool and the work it starts. | product architecture | (cross-cutting) | **ported** | Adopt. In Go this is the ordinary convention of a context.Context first parameter on every capability method, so the Go design has it from the start with no migration. |
| 105 | [Domain KV storage capability seam and the workspace entity][n105] | Proposal (storage and workspace packages have since shipped upstream): a storage hub with pluggable backends (JSON files, SQLite) and a domain key-value form for data that does not belong to one session, first used by a workspace entity that owns a list of sessions. | product architecture | `packages/storage` | **ported** | Adopt. The Go design needs a place for cross-session data such as workspaces and session metadata, and a backend registry behind one interface fits Go well. |
| 106 | [Session projections and command lifecycle logging][n106] | Proposal (the projection registry has since shipped upstream): a state-carrying event must hold the complete new state, never a delta; domains register pure fold functions in a host projection registry; and slash-command results are logged so they can be recovered. | product architecture | `packages/session/session-projection` | **ported** | Adopt. Whole-value events with registered folds are the basis for server-rendered state in the Go UI, where the latest value can always replace the previous one. |
| 107 | [Storage root placement and derived-medium recovery][n107] | Proposal (root placement shipped, recovery not built): all storage lives under one root in the harness home instead of the launch directory, and a storage domain that holds only derived data declares that a damaged medium is reset and rebuilt instead of rejected. | product architecture | `packages/storage/storage-domain` | **ported** | Adopt both parts: one storage root beside the sessions, and an explicit authoritative-or-derived declaration that decides whether damage is fatal or self-healing. |
| 108 | [Record last activity in the session index][n108] | Proposal (not built upstream): store the time of the last human prompt in the session index, so that listing stored sessions sorts correctly without opening each log or relying on an optional cache. | product feature | `packages/session/session-persistence` | **ported** ([DH-ADR-2](../design.md#dh-adr-2)) | Adopt the goal. Upstream left the storage shape open because its JSONL header cannot be rewritten; the Go format is new, so the session index can hold this value from the start. |
| 109 | [Semantic phases for composer-chain election][n109] | Proposal (not built upstream): the composer area chooses what to show by named phases (a pending question or approval first, a read-only restriction second) instead of one global numeric priority. | bug fix | `packages/client/ui-slots` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | Adopt the rule that resolving a pending interaction outranks a restriction, but implement it where the Go host selects the composer template; the client slot chain does not exist. |
| 110 | [Logical session and storage rebuild][n110] | Proposal (not built upstream): consumers depend on a logical session interface and a session service that creates, opens, forks and flushes sessions; a private storage provider interface owns durable data, migration, repair and the writer lease. | product architecture | `packages/core/session` | **ported** | Adopt as the starting shape. A new Go design can define the session interface, the service and the storage provider separately from day one, without upstream's staged migration. |
| 111 | [Session capability protocols][n111] | Proposal (not built upstream): the session system is a small logical core plus separate capabilities (storage, projection, query); product code knows only the logical session and the session service, and only composition and providers know physical implementations. | product architecture | `packages/core/session` | **ported** | Adopt. It states the target boundaries of the same refactor and fits Go interfaces directly. |
| 112 | [Session historical data compatibility][n112] | Proposal: the session refactor must not change stored bytes; old sessions keep opening after an upgrade, old generations are never overwritten, and search indexes are derived data that can be rebuilt. | product architecture | `packages/session/session-format` | **dropped** ([DH-ADR-2](../design.md#dh-adr-2)) | The promise to keep upstream's stored sessions readable does not apply, because the Go design does not read upstream session files. Its lasting rules (never overwrite an old generation, a derived index is never the recovery authority) are carried by the versioning and storage decisions. |
| 113 | [Session developer transition rules][n113] | Proposal: rules for upstream contributors on which session API to use at each stage of the staged session refactor. | development process | (cross-cutting) | **not-applicable** | Upstream development process: transition rules for contributors during a staged refactor of existing code. The Go design starts at the end state, so there is no transition to manage. |
| 114 | [Session refactor FAQ][n114] | Proposal: a question-and-answer page that explains the names and layers of the session refactor to contributors. | documentation | (cross-cutting) | **not-applicable** | Upstream documentation for contributors about a refactor in progress; it records no decision beyond the notes it explains. |
| 115 | [Two-stage Composer and DraftEditor isolation][n115] | Proposal (first stage built upstream): split the message composer into a session-owned draft model (text, attachments, submission) and per-view editor instances that own only DOM and caret state, as a mechanical extraction first and a behaviour change second. | simplification | `packages/client/ui-conversation` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The split is kept and becomes natural: the Go host owns the draft and attachments per session, and the rich-text input is a small script island that owns only the caret and DOM. The two-step refactoring plan is upstream process. |

### Rejected upstream (1 note)

| # | Note | Summary | Kind | Area | First verdict | Why |
|---:|---|---|---|---|---|---|
| 116 | [Runtime schemas for the event vocabulary (Zod vs the merge-extensible-map pattern)][n116] | Rejected upstream: replacing the compile-time extensible type maps for events and content blocks with a runtime schema registry was declined as a repository-wide redesign with too little benefit. | product architecture | `packages/core/session` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2)) | The rejection does not hold in Go. Go has no declaration merging, so components and plugin processes can only extend the event vocabulary through a runtime registry that maps each event type to a Go type and a validator. |

## Patterns worth carrying into the Go design

These nine patterns are the decisions in this batch that most shape the product. Each
entry gives the decision, why it is needed, what it costs, and what the Go design should
check. Row numbers refer to the tables above. The "In Go" lines are inference.

### 1. The session log is the only source of truth, and every model request can be rebuilt from it

- **Decision.** A session is an append-only log of typed events; the message history is
  derived from it (`.agents/notes/implemented/architecture/2026-06-11-event-sourced-sessions.md:13`).
  Anything that reaches a model request must be reconstructable from the log and the
  immutable objects it references
  (`.agents/notes/implemented/architecture/2026-07-05-reconstructable-requests.md:17`).
  History edits such as compaction are recorded as surface operations, and the system
  prompt is an ordinary first message (rows 2, 5, 11, 64, 86).
- **Why.** Replay, fork, debugging and provider prefix caching all depend on it. Stable
  request prefixes are a consequence, not a separate feature
  (`.agents/notes/implemented/architecture/2026-07-05-reconstructable-requests.md:19`).
- **Tradeoff.** No component may rewrite a request on the fly without logging what it
  changed, and the log grows with everything the model saw.
- **In Go.** Keep the invariant and test it directly: rebuild each request of a recorded
  session from the log alone and compare bytes.

### 2. Three roles for every swappable capability

- **Decision.** A capability has a Service Definition (the contract), a Service Provider
  (an implementation) and a Consumer (the tool the model sees); consumers never import a
  provider's types
  (`.agents/notes/implemented/architecture/2026-06-13-capability-seams.md:15-19`). Web
  access, spill storage and execution worlds all follow it (rows 3, 7, 14, 27, 87).
- **Why.** Swapping a local executor for a sandboxed or remote one must not change the
  tool schema the model was prompted with.
- **Tradeoff.** Three packages where one would compile; the split pays off only when a
  second provider exists.
- **In Go.** An interface, a component that registers an implementation, and a tool
  that depends on the interface. The check is that a tool package imports no provider
  package.

### 3. Durable facts and live signals are different channels, and durable state is whole-value

- **Decision.** A fact that must survive replay is a session event; a live interception
  or status signal is an agent event
  (`.agents/notes/implemented/architecture/2026-06-30-event-domain-semantics.md:27`).
  A state-carrying event holds the complete new state, never a delta
  (`.agents/notes/proposed/architecture/2026-07-27-session-projection-and-command-log.md:23`),
  and derived state is a registered pure fold over the log that readers must treat as a
  required dependency (rows 8, 33, 45, 53, 106).
- **Why.** Live rendering and replay then share one path, and a missed update is
  repaired by the next one.
- **Tradeoff.** Whole-value events are larger than deltas.
- **In Go.** This is the natural feed for server-rendered views: the host folds events
  into state and re-renders the affected fragment. Verify by replaying a log into a
  fresh host and comparing projections with the live ones.

### 4. Failure facts are separate from retry policy, with one place that normalizes failures

- **Decision.** A model request failure is one serializable record with no policy in it;
  retryability "belongs to policy"
  (`.agents/notes/implemented/architecture/2026-06-21-bounded-llm-request-recovery.md:41`).
  The LLM runtime is the single boundary that turns an adapter failure into a terminal
  record, and defects elsewhere stay errors
  (`.agents/notes/implemented/architecture/2026-07-29-terminal-llm-stream-failures.md:17`)
  (rows 6, 30, 38, 84).
- **Why.** The loop handles one failure shape, recovery is bounded, and a bug in
  middleware is never mistaken for a transient provider error and retried.
- **Tradeoff.** Adapters lose the freedom to decide for themselves whether to retry.
- **In Go.** An adapter returns an error or a terminal chunk; the runtime converts both
  to the same record. Test with an adapter that fails at each stage.

### 5. Cancellation is required and owned by the caller, end to end

- **Decision.** Every tool invocation carries a cancellation signal owned by its caller,
  with no default
  (`.agents/notes/implemented/architecture/2026-07-19-cooperative-tool-cancellation.md:17`),
  and upstream proposes extending the same requirement to every capability operation a
  tool can reach
  (`.agents/notes/proposed/architecture/2026-07-19-required-cancellation-through-tool-capability-seams.md:17`)
  (rows 12, 13, 18, 104).
- **Why.** An optional signal is silently dropped one call later, and the work it
  should have stopped keeps running.
- **Tradeoff.** Every signature carries one more parameter.
- **In Go.** This is `context.Context` as the first parameter everywhere, with the
  timeout policy deriving a child context. Upstream needs a migration to get there; the
  Go design starts there.

### 6. Format rules that must be right in the first release

- **Decision.** One integer format version, an upgrade chain, refusal that names the
  direction, and an explicit per-event "ignorable" marker
  (`.agents/notes/implemented/architecture/2026-08-10-session-log-version-mechanism.md:15-19`);
  one canonical envelope with one spelling per fact
  (`.agents/notes/implemented/architecture/2026-09-06-v3-canonical-session-envelopes.md:13`)
  (rows 41, 58, 63, 71, 79, 102).
- **Why.** Upstream calls the runtime that ships first "the floor for every later decision": refusal
  behaviour missing from the first reader can never be added to copies already installed
  (`.agents/notes/implemented/architecture/2026-08-10-session-log-version-mechanism.md:9`).
- **Tradeoff.** Strictness makes the first format slower to design.
- **In Go.** The Go format is new ([DH-ADR-2](../design.md#dh-adr-2)), so it can start
  where upstream arrived after four versions: embedded attempt streams, tool-role
  messages, producer-owned sources. Any later upgrade step must stream with bounded
  memory, the lesson of the 116 MB session that exhausted a 16 GB process
  (`.agents/notes/implemented/architecture/2026-08-31-released-session-format-migrations.md:9`).

### 7. Persistence through owned handles, behind a logical session

- **Decision.** Persistence is five service methods that return per-session handles
  (`.agents/notes/implemented/architecture/2026-08-27-handle-based-session-persistence.md:13`).
  Upstream proposes going further: consumers see a logical session and a session
  service, and only a private storage provider knows formats, leases and files
  (`.agents/notes/proposed/architecture/2026-09-06-logical-session-storage-rebuild.md:15`)
  (rows 55, 78, 110, 111).
- **Why.** Ownership is explicit: whoever holds the write handle releases it. Access is
  separate: any reader may open a read handle. The earlier design let a coordinator
  adopt any session it heard about, which hid who owned what.
- **Tradeoff.** Callers must close what they open.
- **In Go.** Define the session interface, the service and the storage provider
  separately from the start, and do not offer synchronous random access to history.

### 8. The local web API is fenced once, in front of every route

- **Decision.** Every `/api` POST must be `application/json`, and every request must
  present a loopback or trusted Host with a matching Origin
  (`.agents/notes/implemented/architecture/2026-07-28-api-browser-trust-boundary.md:15-16`).
  The whole API is authenticated before dispatch with a launch token exchanged for a
  signed cookie
  (`.agents/notes/implemented/architecture/2026-08-24-browser-token-authentication.md:15-17`).
  Browser references to application routes are relative to the document so the
  application works under a proxy prefix (rows 25, 51, 98).
- **Why.** The API can run shell commands as the operating-system user. A browser turns
  that user into a confused deputy through cross-site requests and DNS rebinding.
- **Tradeoff.** Non-browser clients must send the right content type and Host.
- **In Go.** One middleware chain in front of all handlers, including htmx and
  server-sent-event routes. A real-browser scenario is needed to prove it; a unit test
  of the middleware is separate evidence.

### 9. Start-up and configuration have one owner each

- **Decision.** Every process starts through the one `dsh` command and a named profile
  (`.agents/notes/implemented/architecture/2026-08-22-single-dsh-application-launcher.md:17`).
  Settings resolve in one precedence order
  (`.agents/notes/implemented/architecture/2026-08-04-configuration-source-ownership.md:21`).
  The application, not the plugin framework, lists which components are required at
  start-up; any other failure is a warning
  (`.agents/notes/implemented/architecture/2026-09-09-consumer-owned-startup-strictness.md:13`)
  (rows 23, 35, 48, 77).
- **Why.** A second launcher or a second resolver drifts, and then two parts of the
  product disagree about where data lives or which value won.
- **Tradeoff.** A deployment that wants a different policy must ship its own profile.
- **In Go.** One binary makes this easier. The required list changes: upstream's list
  includes the client module registry and connection
  (`.agents/notes/implemented/architecture/2026-09-09-consumer-owned-startup-strictness.md:15`),
  which have no Go counterpart.

Two smaller product rules are also worth keeping as written: uploads of session content
are authorized only by an explicit feedback action (row 68,
`.agents/notes/implemented/architecture/2026-09-05-nonofficial-feedback-otel.md:13`), and
the credential documentation says plainly that stored secrets are not protected from the
model's own tools (row 31,
`.agents/notes/implemented/architecture/2026-07-30-credential-boundaries-and-atomic-registration.md:19`).

## Decisions that exist only because of JavaScript or npm

These notes solve a problem that Go does not have, or solve a real problem with a
mechanism that only exists in JavaScript, Node or npm. They are the main source of
`replaced` and `not-applicable` verdicts. The right-hand column is inference.

| Row | Upstream decision | The JavaScript or npm fact behind it | What happens in Go |
|---:|---|---|---|
| 28 | Launch `dsh` from source through the tsx hook | Node must transform TypeScript at start; Node 26 removed the flag used before (`.agents/notes/implemented/architecture/2026-07-29-dsh-source-launch-tsx-esm.md:11`) | Nothing: Go is compiled. Not applicable. |
| 24 | A compiler-independent type model for the Typert generator | Runtime schemas and reflection text have to be generated from the TypeScript source (`.agents/notes/implemented/architecture/2026-07-27-compiler-independent-typert-model.md:9`), because TypeScript types do not exist at run time | Go types exist at run time. Not applicable. |
| 34 | Remote method calls through decorators and generated client projections | The browser client is a separate TypeScript program that needs typed stubs (`.agents/notes/implemented/architecture/2026-08-02-typert-remote-method-calls.md:19`) | The host renders HTML; SDKs and plugins use JSON-RPC. |
| 15 | Single-file executable built with pkg in SEA mode, with a patch to pkg | A Node application is a directory of files plus a runtime (`.agents/notes/implemented/architecture/2026-07-10-single-file-executable-sdk-runtime-distribution.md:17`, `:24`) | `go build` already produces one file. |
| 72 | Prebuilt native addon family for file locking | A dependency compiled a native addon during `npm install` (`.agents/notes/implemented/architecture/2026-09-07-prebuilt-system-primitives.md:9`) | The Go binary makes the system calls itself. |
| 17 | Initiating agent carried by AsyncLocalStorage | Implicit context across `await` needs a Node facility (`.agents/notes/implemented/architecture/2026-07-15-agent-initiator-scope.md:15`) | A value in `context.Context`. |
| 1 | Deep-freeze every stored event | `readonly` types give no run-time protection (`.agents/notes/implemented/architecture/2026-06-11-dev-invariants-over-deep-readonly.md:9`, `:25`) | The session keeps its own copy and hands out copies. |
| 12 | A shared timeout library | Timeout timing and classification had been written three different ways on top of abort signals (`.agents/notes/implemented/architecture/2026-07-06-timeout-deadline-library.md:9`) | `context.WithTimeout` and `context.Cause` in the standard library. |
| 56 | Install one proxy dispatcher globally before anything can request | Node's `fetch` ignores `HTTP_PROXY` and `HTTPS_PROXY` (`.agents/notes/implemented/architecture/2026-08-27-outbound-proxy-policy.md:9`) | Go's HTTP transport reads them by default; the policy becomes one shared transport. |
| 80 | Immutable profile resolution generations | Plugin packages are found through Node's ESM and CommonJS resolvers (`.agents/notes/implemented/architecture/2026-09-09-profile-resolution-generations.md:15`) | A compiled-in registry and a manifest of plugin processes. |
| 36, 85 | Profiles and bundles as npm packages; a public `package.json` manifest type | Plugins are npm packages managed by pnpm (`.agents/notes/implemented/architecture/2026-08-05-profile-plugin-bundles.md:13`) | Bundles become configuration layers; external plugins get their own manifest. |
| 94, 103 | Plugin management shares pnpm transactions; guided installation with `pnpm view` | Installing a plugin means running pnpm in a profile directory (`.agents/notes/implemented/architecture/2026-09-15-guided-plugin-installation.md:13`) | Installing a plugin means placing and starting a plugin process. Inspect-first and rollback are kept. |
| 21, 43 | Client plugin loading and hot reload; client shell layering | "the browser has no Node module system", and npm dependency sections do not say what a bundler inlines (`.agents/notes/implemented/architecture/2026-07-23-client-plugin-loading-model.md:13`, `.agents/notes/implemented/architecture/2026-08-15-client-shells-and-dynamic-packages.md:11`) | No browser module system or bundler; one shell template and a few script islands. |
| 20, 82, 37, 46, 67, 100, 109, 115 | Slots, component factories, conversation assembly, client layering, resources, session references, composer phases, draft editor | The page is a React application "composed at runtime from independently loaded plugins" (`.agents/notes/implemented/architecture/2026-07-22-slot-type-chain-implementation.md:11`) | The same needs are met by server-side templates and state; see [DH-ADR-3](../design.md#dh-adr-3). |
| 52, 73, 74 | Electron packaging, bundled runtime, welcome window | The desktop shell is Electron and must carry Node, pnpm and a browser engine (`.agents/notes/implemented/architecture/2026-08-25-electron-desktop-packaging-and-updates.md:21`) | Nothing: there is no desktop application ([DH-ADR-18](../design.md#dh-adr-18)). The `dsh` binary serves the UI to the user's own browser, and each desktop duty is ported to the web UI or the command line or dropped on record by area A25. |
| 96 | LibreOffice kit consumed as an exact npm version with a Node worker | The engine is packaged for Node (`.agents/notes/implemented/architecture/2026-09-14-independent-libreoffice-kit.md:15`) | The engine is driven as a separate process. |
| 116 | Runtime schema registry for events rejected | "a plugin cannot declaration-merge a Zod schema", so with the compile-time extension pattern a registry meant a repository-wide rewrite (`.agents/notes/rejected/architecture/2026-06-16-typed-event-schemas.md:20`) | Go has no declaration merging, so a run-time registry is the only way to extend the vocabulary. The rejection does not hold. |

Three notes are kept as `ported` but contain a detail of the same kind that the area
owner should not carry over: the deprecation markers and lint override for synchronous
session reads (row 78,
`.agents/notes/implemented/architecture/2026-09-09-deprecate-synchronous-session-event-reads.md:17`),
the `ELECTRON_RUN_AS_NODE` handling in the code-execution runtime (row 88,
`.agents/notes/implemented/architecture/2026-09-11-sandboxed-node-ptc-runtime.md:17`),
and the conversion tables from older upstream session formats (rows 79 and 102).

## Open questions for the area owners

1. **Guest runtime for model-written code (rows 88, 93).** Upstream runs each program in
   a fresh Node process and gets the Node executable for free because the host is Node
   (`.agents/notes/implemented/architecture/2026-09-11-sandboxed-node-ptc-runtime.md:15`).
   Settled by the owner: the Go host has no guest runtime and embeds no interpreter. A
   model-written script is an ordinary sandboxed command that calls tools through the
   `dsh tool call` command line, and every call passes the guarded pipeline
   ([DH-ADR-8](../design.md#dh-adr-8); area A11 restates workflows on it).
2. **The second model adapter (rows 4, 16, 42).** The twin-adapter rule
   (`.agents/notes/implemented/architecture/2026-06-13-twin-llm-adapters.md:18`) needs a
   second, independently built adapter. Upstream's is an npm library; the LLM area must
   name the Go counterpart and decide what happens to the grant formats that library owns.
3. **Default upload of the session log (row 97).** Upstream enables it by default
   (`.agents/notes/implemented/architecture/2026-09-14-session-log-upload-default.md:12`).
   Settled by the owner, against the first verdict: upload is off, and everything that
   leaves the machine is opt-in under one off-switch
   ([DH-ADR-9](../design.md#dh-adr-9)).
4. **Session format upgrades (rows 59, 61, 69, 112).** The Go store has no upstream
   formats to read. Upstream's current format enters only through a separate, deferred
   import command that refuses a file it cannot map
   ([DH-ADR-11](../design.md#dh-adr-11)); area A05 states where the surviving rules
   live ([../areas/session-store.md](../areas/session-store.md)).
5. **Welcome window material (row 74).** Settled: there is no native window
   ([DH-ADR-18](../design.md#dh-adr-18)). First-run onboarding is a page of the web UI.
6. **Office conversion engine (rows 96, 99).** Settled by the owner: an
   operator-installed LibreOffice run headless as a child process, nothing bundled
   ([DH-ADR-20](../design.md#dh-adr-20)); the bounded shared queue is kept by area A16.

<!-- Links to the notes at the pinned upstream commit -->

[n1]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-06-11-dev-invariants-over-deep-readonly.md
[n2]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-06-11-event-sourced-sessions.md
[n3]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-06-13-capability-seams.md
[n4]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-06-13-twin-llm-adapters.md
[n5]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-06-18-session-surface.md
[n6]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-06-21-bounded-llm-request-recovery.md
[n7]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-06-24-web-capability-seam.md
[n8]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-06-30-event-domain-semantics.md
[n9]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-02-tool-render-intent-union.md
[n10]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-05-prompt-variables-and-tool-guidance-ownership.md
[n11]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-05-reconstructable-requests.md
[n12]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-06-timeout-deadline-library.md
[n13]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-07-tool-call-timeout-policy.md
[n14]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-08-tool-output-spill-files.md
[n15]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-10-single-file-executable-sdk-runtime-distribution.md
[n16]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-14-provider-routed-llm-adapters.md
[n17]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-15-agent-initiator-scope.md
[n18]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-19-cooperative-tool-cancellation.md
[n19]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-20-routed-model-context-and-compaction-policy.md
[n20]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-22-slot-type-chain-implementation.md
[n21]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-23-client-plugin-loading-model.md
[n22]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-24-separate-context-injection-from-turn-execution.md
[n23]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-24-single-harness-home-resolver.md
[n24]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-27-compiler-independent-typert-model.md
[n25]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-28-api-browser-trust-boundary.md
[n26]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-28-identified-immutable-message-values.md
[n27]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-28-portable-execution-world-consumers.md
[n28]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-29-dsh-source-launch-tsx-esm.md
[n29]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-29-projected-token-usage-and-request-context.md
[n30]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-29-terminal-llm-stream-failures.md
[n31]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-30-credential-boundaries-and-atomic-registration.md
[n32]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-30-followup-enqueue-and-owned-runs.md
[n33]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-07-31-goal-owned-durable-events.md
[n34]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-02-typert-remote-method-calls.md
[n35]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-04-configuration-source-ownership.md
[n36]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-05-profile-plugin-bundles.md
[n37]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-09-client-conversation-node-assembly.md
[n38]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-10-cancelled-stream-prefix-finalize.md
[n39]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-10-fork-children-stay-one-shot.md
[n40]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-10-host-plane-ownership-after-presets.md
[n41]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-10-session-log-version-mechanism.md
[n42]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-13-credential-records-and-authorization-flows.md
[n43]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-15-client-shells-and-dynamic-packages.md
[n44]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-18-session-history-and-event-transport.md
[n45]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-19-session-projection-mandatory-seam.md
[n46]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-20-client-session-conversation-ownership.md
[n47]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-21-deepseek-llm-api-request-extensions.md
[n48]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-22-single-dsh-application-launcher.md
[n49]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-23-client-derived-tool-presentation.md
[n50]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-23-locale-owned-client-ui-copy.md
[n51]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-24-browser-token-authentication.md
[n52]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-25-electron-desktop-packaging-and-updates.md
[n53]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-25-session-observations-and-projection-owned-client-state.md
[n54]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-27-adjacent-agent-steer-messaging.md
[n55]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-27-handle-based-session-persistence.md
[n56]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-27-outbound-proxy-policy.md
[n57]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-28-ctx-remote-failure-vocabulary.md
[n58]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-30-retain-ignorable-external-session-events.md
[n59]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-31-alpha-historical-unknown-event-refusal.md
[n60]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-31-explicit-agent-runtime-identity.md
[n61]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-08-31-released-session-format-migrations.md
[n62]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-01-parent-owned-subagent-catalog.md
[n63]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-01-v2-embedded-assistant-streams.md
[n64]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-02-system-prompt-as-surface-node.md
[n65]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-03-jobs-seam-consolidation.md
[n66]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-05-canonical-feedback-log.md
[n67]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-05-client-resource-model.md
[n68]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-05-nonofficial-feedback-otel.md
[n69]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-05-read-only-session-migration-preparation.md
[n70]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-05-workspace-files-service.md
[n71]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-06-v3-canonical-session-envelopes.md
[n72]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-07-prebuilt-system-primitives.md
[n73]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-08-desktop-bundled-runtime-and-external-plugins.md
[n74]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-08-desktop-welcome-window-material.md
[n75]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-08-document-preview-operations.md
[n76]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-09-awaited-agent-creation.md
[n77]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-09-consumer-owned-startup-strictness.md
[n78]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-09-deprecate-synchronous-session-event-reads.md
[n79]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-09-producer-owned-message-sources.md
[n80]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-09-profile-resolution-generations.md
[n81]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-09-workspace-file-read-authority.md
[n82]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-10-component-factories-and-local-slots.md
[n83]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-10-desktop-web-wrapper.md
[n84]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-10-image-offload-events.md
[n85]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-10-public-package-manifest.md
[n86]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-11-plugin-owned-message-projections.md
[n87]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-11-posix-ssh-runtime.md
[n88]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-11-sandboxed-node-ptc-runtime.md
[n89]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-11-subprocess-control-pipe.md
[n90]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-12-browser-use-provider-registration.md
[n91]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-12-computer-use-provider-registration.md
[n92]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-13-session-working-directory.md
[n93]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-13-workflow-ptc-sandbox-reuse.md
[n94]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-14-current-profile-plugin-management.md
[n95]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-14-deepseek-account-login.md
[n96]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-14-independent-libreoffice-kit.md
[n97]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-14-session-log-upload-default.md
[n98]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-14-web-document-relative-app-routes.md
[n99]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-15-bounded-office-conversion.md
[n100]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-15-client-session-references.md
[n101]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-15-desktop-native-fatal-recovery.md
[n102]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-15-first-class-tool-role-messages.md
[n103]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/architecture/2026-09-15-guided-plugin-installation.md
[n104]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/architecture/2026-07-19-required-cancellation-through-tool-capability-seams.md
[n105]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/architecture/2026-07-24-domain-kv-storage-and-workspace.md
[n106]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/architecture/2026-07-27-session-projection-and-command-log.md
[n107]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/architecture/2026-07-28-storage-root-and-derived-medium-recovery.md
[n108]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/architecture/2026-07-29-durable-last-activity-index.md
[n109]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/architecture/2026-08-08-semantic-composer-chain-phases.md
[n110]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/architecture/2026-09-06-logical-session-storage-rebuild.md
[n111]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/architecture/2026-09-10-session-capability-protocols.md
[n112]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/architecture/2026-09-10-session-data-compatibility.md
[n113]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/architecture/2026-09-10-session-developer-transition.md
[n114]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/architecture/2026-09-10-session-refactor-faq.md
[n115]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/architecture/2026-09-14-composer-model-and-draft-editor.md
[n116]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/rejected/architecture/2026-06-16-typed-event-schemas.md
