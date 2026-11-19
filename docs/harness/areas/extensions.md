# Extensions: plugins as supervised processes, and read-only inspection: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md) and are cited as `path:line` under the upstream root. Statements that are
> reasoning and not read from source open with "Inference"; sections 6 to 9 are design and are inference
> throughout. IDs in this document use the area key `EXT`. Nothing here was built, run or tested.

| | |
|---|---|
| **Area key** | `EXT` (agent A22) |
| **Upstream packages** | 4, all in `packages/extensions`: `cordis-host-runner`, `cordis-client-runner`, `tool-cordis`, `ui-cordis` |
| **Serves outcomes** | [DH-O-11](../design.md#dh-o-11) (this area owns it: a plugin is a supervised, least-privilege process), [DH-O-1](../design.md#dh-o-1) (everything a plugin contributes is released with its fiber), [DH-O-6](../design.md#dh-o-6) (a plugin's tools and its own tool calls pass the one pipeline), [DH-O-7](../design.md#dh-o-7) (confinement, consent), [DH-O-2](../design.md#dh-o-2) (the composed tree can be inspected) |
| **Depends on areas** | A03 (tool registry, pipeline, direct calls), A09 (the one process starter, stop ladder), A10 (policy resolver `ForPlugin`, sandbox seam), A15 (the MCP fleet and its supervision rules), A17 (interaction registry, consent, secret store), A20 (the JSON-RPC codec), A21 (the management surface: commands, pages, layer edits, the model-facing management tool), A01 and A05 (plugin records, the committed-event feed), A14 (skill provider registry), A18 (inventory and the plugins page), A07 (home layout, retention), A19 (routes and event stream), A23 (`schema`, version ranges), A27 (preset inventory) |
| **Priority** | P1: no first working harness needs a plugin, but [DH-O-11](../design.md#dh-o-11) and upstream's shipped "Creator" path do. P2 for plugins the model attaches to its own session and for the inspection tool |

## 1. Purpose

This area answers one question: **how does code that is not compiled into `dsh` add to a running harness?** Upstream's answer is to evaluate it inside the host process and the browser page. The Go answer is a separate process that talks to the host over a small protocol and can do only what the operator granted ([DH-ADR-4](../design.md#dh-adr-4), [DH-ADR-13](../design.md#dh-adr-13)). The area therefore owns one shared piece that several other areas wait for: **the plugin manifest, the plugin protocol's methods, and the capability names.** It also keeps the second thing upstream's packages do: a read-only tool with which the model looks at the composed harness before it writes or configures anything.

Without this area the harness could be extended only by recompiling it, an MCP server could offer tools but nothing else, and the model could not see what is mounted in the host it runs in.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **Component, plugin** | A component is compiled into `dsh`. A **plugin** is only ever a separate process ([../design.md §2](../design.md#2-vocabulary)). Upstream calls both "plugin" | Keeps "trusted host code" and "untrusted outside code" apart in every sentence |
| **Dynamic package, host half, browser half** (upstream) | A versioned pair of JavaScript sources: the host half is evaluated in the host process, the browser half in the page | What this area replaces |
| **Plugin directory, manifest** | A local directory the operator (or the model) wrote, holding the program and one file, `dsh-plugin.json`, that states identity, how to start it and what it asks for | The harness can decide everything about a plugin before running one byte of it |
| **Plugin id** | The manifest's `name`. It prefixes the plugin's tools, names its records in a session and its directories in the harness home | One name, so a tool, a log record and a process can be traced to one directory |
| **Trust level** | `built-in` (a component), `installed` (a directory an owner approved through the management surface) or `model-authored` (a directory attached from inside a session) ([DH-ADR-SECURITY-4](../upstream/security.md#dh-adr-security-4)) | The level sets the default confinement and which capabilities may be held |
| **Capability** | A named permission from a closed table, sometimes with parameters (which tools, which event types) | The unit the operator reads and decides on |
| **Grant, grant record** | A grant is the operator's yes to one capability for one plugin at one digest. The record is where that yes is stored | Access is written down, shown and revocable; it is never implied by the manifest |
| **Digest, approved copy** | The digest is a SHA-256 over the manifest and every file of the directory. The approved copy is a private, host-owned copy of exactly those bytes, and it is what runs | A grant is for bytes someone looked at, not for a path whose content can change |
| **Contribution** | Something a plugin adds to the host: a tool, an event subscription, a skill provider | Each is an effect of the plugin's fiber, so removal is one operation |
| **Protocol session, generation** | One run of the process from handshake to exit. A generation is the full tool set of one successful listing | Tools change as a set; pending calls belong to one session |
| **Capability gate** | The one function every message from a plugin passes before any handler | "Deny by default" has a single place |
| **Supervisor** | The loop that starts, watches, restarts and stops one plugin process | A crash or hang must cost the plugin's calls, never the host |
| **Install, attach** | Install: an owner adds a plugin for the whole host, kept across restarts. Attach: an agent's session gets a plugin until the session ends | Standing access and one-session access have different givers ([DH-ADR-EXT-8](#dh-adr-ext-8)) |
| **Inspect provider** | A named, read-only source of facts about the running host that the inspection tool can query | The model reads the real composition instead of guessing |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.extensions.cordis-host-runner` | `@deepseek-ai/dsh-cordis-host-runner` | provider | Provides `ctx.dynamicCordisRunner` (a registry of process-local dynamic packages and the lifecycle of their host halves in a `node:vm` realm) and `ctx.cordisInspect` (the registry behind the inspection tool) |
| `UP-PKG-packages.extensions.cordis-client-runner` | `@deepseek-ai/dsh-cordis-client-runner` | consumer (browser) | Evaluates a browser half in the page, mounts it through the client loader, answers run requests and client-side inspection queries |
| `UP-PKG-packages.extensions.tool-cordis` | `@deepseek-ai/dsh-tool-cordis` | consumer | The model-facing tool `cordis_inspect` and five host inspect providers |
| `UP-PKG-packages.extensions.ui-cordis` | `@deepseek-ai/dsh-client-ui-cordis` | consumer (browser) | A sidebar panel that lists and operates dynamic packages, and cards for historical `cordis_define`, `cordis_run`, stop and undefine calls |

**How much of it is logic.** `tool-cordis` has 896,186 bytes under `src`; 879,589 of them (98.1%, 11,915 of 12,261 lines) are one generated file, `src/api-catalog.ts`, whose header says "Generated by scripts/gen-cordis-api.ts — do not edit by hand" (`packages/extensions/tool-cordis/src/api-catalog.ts:2`). The hand-written code is 346 lines in five files. `cordis-client-runner` is similar: `slot-catalog.ts` (367,866 bytes) and `api-catalog.ts` (77,585 bytes) are generated (`packages/extensions/cordis-client-runner/src/client/slot-catalog.ts:2`), which is 83% of its 537,095 bytes. Inference: the Go inspection work is one small tool over catalogues the harness already generates ([DH-ADR-TOOLCHAIN-10](../upstream/toolchain.md#dh-adr-toolchain-10)); the size of the upstream group is not a measure of the work.

### 3.2 Behaviour

A citation that begins `src/` or `README.md` continues the package path of the citation before it.

**Where upstream stands at the pinned commit.** The group no longer gives the model a way to create code in the host: "no model tool creates dynamic definitions" (`packages/extensions/cordis-host-runner/README.md:12`, `:123`; `packages/extensions/ui-cordis/README.md:28`). Persistent plugins written by the model are npm bundles installed through Plugin Manager (`packages/extensions/README.md:12`), which belongs to area A21. What is left here is three things.

**1. Inspection.** `cordis_inspect` (`packages/extensions/tool-cordis/src/index.ts:22`) returns the provider directory when called without a query (`:44`) and otherwise forwards one `{platform, provider, method, input}` query to `ctx.cordisInspect` (`:45-52`). It is declared safe to run concurrently because every provider method is read-only (`:41-42`). Five host providers are registered once per process (`src/host.ts:16-19`; `src/providers.ts:43-93`): `Service` and `Event` serve the generated catalogue, `Config` walks the live Loader tree, `Runtime` returns the running version, and `Tool` returns the tool schemas the asking agent can call. `Config` lists entries in pages of 25, at most 100 (`src/config.ts:54-55`), gives each one of five statuses (`:18`) and projects one entry's config into a JSON Schema document (`:119-125`); it returns shapes, never values. Client providers (slots, theme, events) are answered by a connected browser page: the registry emits `cordis/inspect-query`, and the first valid page response within `clientInspectTimeoutMs` (10 s) wins (`packages/extensions/cordis-host-runner/README.md:50`; `src/index.ts:149`). Failures are eight `INSPECT_*` codes (`src/inspect-registry.ts:28-36`).

**2. The process-local runner.** `ctx.dynamicCordisRunner` injects `tools` (`packages/extensions/cordis-host-runner/src/index.ts:144-145`). A programmatic caller `define`s a package: the source of each half is compiled to check its syntax, ids are minted, and the definition is recorded against the asking session (`README.md:96`; `src/registry.ts:56`). Versions are immutable; two pointers name the running and the target version (`README.md:80`). `run` evaluates a host half in a fresh `node:vm` realm with a timeout on its synchronous part (`src/sandbox.ts:231`; `src/index.ts:148`, `:921`) and starts the returned plugin as a child fiber of a `cordis-dynamic` group (`src/index.ts:1257`; `src/lifecycle.ts:22-45`). The plugin receives a façade, not the real context: ten lifecycle verbs plus the services it declared (`src/guard.ts:705`). A package with a browser half arms an approval, emits `cordis/request-run` and suspends until a page walks `runHostHalf`, `getClientCode` and `resolveRequestRun` (`README.md:96`); an approval may cover future versions of the same plugin (`src/index.ts:371`). `stop` disposes the fiber, which removes everything the plugin registered, "because everything the plugin registered is an effect on its fiber" (`src/lifecycle.ts:5-7`), and broadcasts `cordis/dynamic-retract`. Outcomes and failures are sent to the owning session as user messages (`src/index.ts:1041-1172`).

**3. The browser side.** A browser half is plain JavaScript evaluated as an async function that receives `React`, `console`, `styles` and `host` (`packages/extensions/cordis-client-runner/README.md:32`; `src/client/evaluator.ts:173`); it registers UI into slots and reaches its own host half through `host.call`. The panel lists every definition of every session and lets any open page approve a pending run (`packages/extensions/ui-cordis/README.md:32`, `:42`).

**Trust.** Upstream says plainly that none of this confines anything: "The sandbox isolates globals but is not a security boundary ... Treat a dynamic package like bash access" (`packages/extensions/cordis-host-runner/README.md:66`), and in code "is not containment: host-realm helper functions remain an escape route" (`src/sandbox.ts:6-7`).

**Contracts.** Six events, all `emit` and all forwarded to browsers (`src/types.ts:371-401`); one config schema with two fields, `vmTimeoutMs` (5000) and `clientInspectTimeoutMs` (10000) (`src/index.ts:147-150`); two services; one model tool; nothing persisted.

### 3.3 Failure, retry, rollback and cleanup upstream

A host half that fails to start is disposed before the error is rethrown, so a failed run never stays mounted (`src/lifecycle.ts:29-33`). A tool a package defined fails with one of two codes (`README.md:62`). A run that needs a page suspends with no timeout where no page is connected (`README.md:141-142`); an asynchronous host half escapes the timeout (`:143`); a page that answers with a stale revision is refused and the request stays suspended (`:144`). Nothing is persisted: a restart clears every definition, a page reload unloads every browser half, and only the historical tool cards remain (`README.md:60`; `packages/extensions/ui-cordis/README.md:12`). Nothing is retried and nothing a package did is rolled back.

## 4. Upstream decisions dissected

No decision note was handed to this area; every row is a decision visible in code or a README.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| Evaluate plugin source inside the host, in a `node:vm` realm behind a façade | Cooperative packages stay inspectable and disposable (`src/sandbox.ts:1-7`) | drop | Go cannot load code into a running process, and upstream itself says the realm confines nothing | [DH-ADR-EXT-1](#dh-adr-ext-1) |
| Dual-half packages: a browser half evaluated in the page | A plugin can add UI (`cordis-client-runner/README.md:32`) | drop | There is no client plugin runtime ([DH-ADR-3](../design.md#dh-adr-3)); HTML from an untrusted process would need its own sanitising boundary | [DH-ADR-EXT-1](#dh-adr-ext-1), [DH-ADR-EXT-5](#dh-adr-ext-5) |
| Everything a plugin registers is an effect on its fiber; stop is dispose | One owner, one removal (`src/lifecycle.ts:5-7`) | keep | It is the property [DH-O-1](../design.md#dh-o-1) asks for; the fiber now also owns a process | [DH-ADR-EXT-5](#dh-adr-ext-5) |
| The façade is a whitelist of verbs and declared services | A package can reach only what it named (`src/guard.ts:705`) | keep the idea, change the mechanism | The whitelist becomes the capability table, enforced across a process boundary instead of inside one heap | [DH-ADR-EXT-6](#dh-adr-ext-6) |
| Versions are immutable packages with run and update pointers | A definition's history is explicit (`README.md:80`) | change | A directory has one content at a time; identity is its digest, and a change is a new approval | [DH-ADR-EXT-3](#dh-adr-ext-3) |
| Definitions are session-scoped and process-local | Other sessions read them as absent; restart clears them (`README.md:60`) | keep for attached plugins | It matches a grant that ends with its session; installed plugins are host-wide and durable | [DH-ADR-EXT-8](#dh-adr-ext-8) |
| An approval may cover future versions; any page may answer | Fewer prompts (`src/index.ts:371`; `ui-cordis/README.md:42`) | drop | A yes for code not yet written is standing consent to unknown bytes; who may answer is area A17's rule | [DH-ADR-EXT-3](#dh-adr-ext-3), [DH-ADR-EXT-8](#dh-adr-ext-8) |
| Outcomes steer the owning session as user messages | The model learns what happened (`src/index.ts:1041-1172`) | change | A plugin must not write text the model reads as the user's; it reaches the model through tool results and the host reports state | [DH-ADR-EXT-5](#dh-adr-ext-5) |
| One read-only inspection tool over a provider registry; catalogue generated from source | Exact queries instead of loading all declarations (`tool-cordis/README.md:28`, `:61`) | keep | Cheap in tokens and cannot drift from the code | [DH-ADR-EXT-9](#dh-adr-ext-9) |
| Client inspect providers answered by a page, first valid answer wins | The slot surface lives in the browser (`README.md:50`) | drop | No slot surface and no page-side code exist to describe | [DH-ADR-EXT-9](#dh-adr-ext-9) |
| Host providers register once per process; the tool is mounted per preset | A preset that omits the tool pays no tokens (`src/host.ts:16-19`) | keep | Same split as every Go seam: a service component and a tool component | [DH-ADR-EXT-9](#dh-adr-ext-9) |
| A plugin's tool fails with a closed pair of codes | The model can tell a thrown error from a bad result (`README.md:62`) | keep | Becomes `plugin/tool-error` and `plugin/invalid-result` | [DH-ADR-EXT-4](#dh-adr-ext-4) |

## 5. Tech debt and its resolution

All 20 units are README limitations. They are grouped by cause; each unit's own reason is in its ledger row.

| Group (cause) | Units | How the Go design answers it | resolved / not-applicable / carried |
|---|---|---|---|
| **1. A run needs a browser page to answer and to load code** | 8: `cordis-client-runner.1`, `.2`; `cordis-host-runner.2`, `.3`, `.5`, `.6`; `ui-cordis.5`, `.6` | No code loads in a page. Enabling is one consent in area A17's registry: it settles once, only the bound channel may answer, and with nobody to ask it is refused at once, so nothing suspends. What a plugin asks for is in its manifest before anything runs. Staleness is replaced by the digest ([DH-ADR-EXT-3](#dh-adr-ext-3), [DH-ADR-EXT-8](#dh-adr-ext-8)) | 7 / 1 / 0 |
| **2. Panel and card state is rebuilt in the browser from announcements** | 6: `cordis-host-runner.1`; `ui-cordis.1`, `.2`, `.3`, `.4`, `.7` | The host holds one descriptor per plugin from the moment it is known and announces every change; pages are rendered from it. An attach is a logged event that carries its own labels. Nothing renders plugin UI after a load ([DH-D-EXT-8](#dh-d-ext-8), [DH-D-EXT-7](#dh-d-ext-7)) | 4 / 2 / 0 |
| **3. A rule kept in two hand-written places, or with no enforcement point** | 3: `cordis-client-runner.3`, `.4`; `cordis-host-runner.4` | One capability table drives the gate, the documentation and the inspect provider. A deadline is enforced on a process the host can kill, not on a synchronous slice of a script ([DH-ADR-EXT-6](#dh-adr-ext-6), [DH-D-EXT-4](#dh-d-ext-4)) | 2 / 1 / 0 |
| **4. npm packaging** | 1: `cordis-host-runner.7` | A runtime dependency of generated JavaScript files; Go has none | 0 / 1 / 0 |
| **5. What inspection covers** | 2: `tool-cordis.1`, `.2` | Preset-mounted entries are listed, because the provider reads area A18's inventory, which includes presets. Inspection stays read-only on purpose: that half of `.1` is carried as a design property ([DH-ADR-EXT-9](#dh-adr-ext-9)) | 1 / 0 / 1 |

Totals: 14 resolved, 5 not-applicable, 1 carried.

## 6. Go design

### 6.1 Shape

Packages sit under `src/harness/internal/wire/plugin`, the family [../design.md §4.2](../design.md#42-go-module-layout) names for the plugin protocol host, and `internal/host/inspect`. The JSON-RPC codec is not among them: by the owner's decision ([DH-ADR-26](../design.md#dh-adr-26)) it is `sdk/jsonrpc` in the third module `src/sdk` ([DH-D-SDK-1](sdk-acp-typert.md#dh-d-sdk-1)), which these packages import, and no codec code lives in `src/harness`.

| Package | Component | Provides | Injects (owner) | Why it exists |
|---|---|---|---|---|
| `wire/plugin` | none (contract) | Types `Manifest`, `Capability`, `Grant`, `Descriptor`, `Status`; `plugin.Key`; method, capability and error names; the notify key `plugin.StateChanged` | nothing | The one package other areas import |
| `wire/plugin/manifest` | none (library) | `Inspect(dir) Report`, `Digest`, `Copy` | nothing; standard library and `schema` (A23) | Everything decided before a process exists; usable by `dsh` commands without a host |
| `wire/plugin/proto` | none (library) | One Go struct per message; the version table; the JSON Schema of the protocol, derived through `schema` | `sdk/jsonrpc` of module `src/sdk` (A20) | Plugin authors, the gate and the inspect provider read one definition |
| `wire/plugin/host` | `plugin.host`, one per host, in the base bundle, in the group `plugins` | `plugin.Service` | `subprocess.HostKey` (A09), `policy.Key` and `sandbox.Key` (A10), `tools.Key` (A03), the session store (A01); optional: approvals (A17), secret store (A17), skills (A14), `mcp.Key` (A15) | Owns every plugin's child fiber, supervisor, gate and contributions |
| `wire/plugin/entry` | `plugin.entry`, one loader entry `plugin.entry@<name>` per installed plugin, in the group `plugins` | nothing | `plugin.Key` | Makes an installed plugin an ordinary loader entry: enabled, disabled, restarted and attributed to a layer like any other |
| `wire/plugin/kit` | none (test support) | A scriptable fake plugin, a reference plugin, the conformance suite | nothing | One suite proves a plugin in any language against the protocol |
| `host/inspect`, `host/inspect/tool` | `host.inspect`; `tool.inspect` | `inspect.Key`; the tool `harness_inspect` | inventory (A18), `tools.Key`, `plugin.Key` (optional) | The read-only view; the tool is separate so a preset that omits it pays no tokens |

```go
// package plugin
type Service interface {
    Inspect(ctx context.Context, dir string) (manifest.Report, error) // reads files; runs nothing
    Plugins() []Descriptor                                            // installed and attached, in every state
    Run(owner *cordis.Context, e Entry) (*cordis.EffectHandle, error) // called by a `plugin.entry` entry; one child fiber
    Attach(ctx context.Context, r AttachRequest) (Descriptor, error)  // session scope; asks for consent (A17)
    Detach(ctx context.Context, agent agent.ID, id ID) error
    Restart(ctx context.Context, id ID) error                         // operator action; fresh restart budget
}
type Descriptor struct { // a value copy; what area A18's inventory and the plugins page read
    ID ID; Title, Version, Dir, Digest string; Trust Trust; Scope Scope; Protocol string
    Requested, Granted []Capability; Status Status; Confinement sandbox.Confinement
    Tools []string; Restarts int; LastExit *Exit; SourceChanged bool
}
// package inspect
type Provider interface {
    Info() ProviderInfo // id, description, methods with input and output JSON Schema
    Query(ctx context.Context, method string, in json.RawMessage, by agent.Ref) (json.RawMessage, error)
}
type Service interface {
    Register(owner *cordis.Context, p Provider) (*cordis.EffectHandle, error)
    List() []ProviderInfo
    Query(ctx context.Context, provider, method string, in json.RawMessage, by agent.Ref) (json.RawMessage, error)
}
```

`Status` is the closed set of area A15's MCP fleet ([mcp-lsp-web-connector.md §6.2](mcp-lsp-web-connector.md#62-contracts)) with two additions: `starting`, `running`, `restarting{attempt, next}`, `waiting-grant{reason}` (no grant record, or the digest changed), `waiting-credentials{ref}`, `disabled`, `unavailable{reason}`, `closed`, `failed{stage, message}`, `stopping-unconfirmed`.

### 6.2 Contracts

**The manifest** is `dsh-plugin.json` at the directory root: UTF-8 JSON, at most 64 KiB, decoded strictly, so an unknown key or wrong type is an error with line and column ([DH-ADR-EXT-2](#dh-adr-ext-2)).

| Field | Form | Meaning |
|---|---|---|
| `manifest` | integer, required | The manifest format, `1`. Any other value is refused |
| `name` | `[a-z][a-z0-9-]{1,31}`, required | The plugin id. Unique in a host |
| `version` | semantic version, required | Shown to the operator and stored in the grant record |
| `title`, `description` | text, at most 80 and 500 characters | Shown as third-party text, never interpolated |
| `protocol` | `plugin` or `mcp` | What the process speaks on its standard input and output |
| `protocolVersions` | list of integers (for `plugin`) | Checked by `Inspect`, so an incompatible plugin is refused before it runs |
| `run` | `command`, `args`, `env` | An argument list, never a shell string. `command` is a program name looked up on the child's `PATH`, or a path beginning `./` inside the directory. `env` values are literal text |
| `platforms` | optional list of `linux`, `darwin`, `windows` | `plugin/unsupported-platform` before any start |
| `requires.dsh` | version range | Checked with area A23's helper against the running version |
| `capabilities` | list of `{name, parameters, reason}` | What the plugin asks for. `reason` is shown beside the request |
| `credentials` | list of `{name, env, reason}` | A request that the operator map a secret into one environment variable |
| `limits` | `startupTimeout`, `callTimeout` | May only lower the host's values |

There is no install, build or update step: the harness runs nothing from the directory except `run`, and downloads nothing ([DH-ADR-13](../design.md#dh-adr-13)).

**Handshake and version negotiation** ([DH-ADR-WIRE-9](../upstream/wire-and-api.md#dh-adr-wire-9) fixes the framing and the order).

1. The host starts the process and waits for the sandbox to confirm confinement (area A10's launch steps). A process that was never confined never receives a byte.
2. The host sends `initialize`: the protocol versions it supports (today `[1]`), the plugin id, the state directory, the trust level, the granted capabilities with their parameters, and the limits (line length, pending requests, call deadline).
3. The plugin answers with the one version it chose from the offered list, and its name and version. Any other request before this answer is refused as `plugin/not-initialized`.
4. The host checks the answer. A version it did not offer is `plugin/version-unsupported`; a name or version that differs from the manifest is `plugin/identity-mismatch`. Both stop the plugin and are not retried, because a restart cannot change them.
5. The host sends `initialized`, then pulls `tools/list` (and `skills/list`) when those capabilities are granted. The plugin is **ready** when the handshake and the first listings succeeded within `startupTimeout`; only then is its first generation registered and its status `running`.

Versions are integers. A change that an older peer would misread is a new integer; adding an optional field or a new capability is not.

**Methods of protocol version 1.** H is the host, P the plugin. Every P to H message passes the capability gate first.

| Method | Direction | Kind | Needs | What it does |
|---|---|---|---|---|
| `initialize`, `initialized` | H to P | request, notification | nothing | The handshake above |
| `ping` | both | request | nothing | Liveness, with a deadline |
| `shutdown` | H to P | request | nothing | Finish and exit; the host then closes the plugin's input |
| `$/cancel` | both | notification | nothing | Names a request id; the receiver answers that request as cancelled |
| `log` | P to H | notification | nothing | `{level, message}`, rate-limited, written to the host log under the plugin's label. Standard error is logged the same way |
| `host/describe` | P to H | request | nothing | The plugin's own grants, limits and the host version |
| `tools/list` | H to P | request | `tools.offer` | Pages of `{name, description, inputSchema}` |
| `tools/call` | H to P | request | `tools.offer` | `{callId, name, arguments, session, agent}`; the reply is `{content, isError}`, where content is text or a reference to a file in the state directory (no inline binary) |
| `tools/changed` | P to H | notification | `tools.offer` | The host lists again and swaps one generation |
| `host/callTool` | P to H | request | `tools.call` | A nested tool call, only while the plugin is serving a `tools/call` |
| `host/subscribe`, `host/unsubscribe` | P to H | request | `session.events.read` | Starts or ends delivery of the granted event types |
| `events/notify`, `events/gap` | H to P | notification | (subscription) | Committed events in order per session; a gap notice when the plugin read too slowly |
| `host/appendRecord` | P to H | request | `session.records.write` | Appends one plugin record to a session |
| `skills/list`, `skills/read`; `skills/changed` | H to P; P to H | request; notification | `skills.provide` | Area A14's provider contract, carried over the protocol |

Errors use the shared `<domain>/<reason>` table: `plugin/capability-denied` (naming the capability), `plugin/unknown-method`, `plugin/not-initialized`, `plugin/version-unsupported`, `plugin/identity-mismatch`, `plugin/no-call-context`, `plugin/line-too-long`, `plugin/overloaded`, `plugin/connection-closed` (sent; **outcome unknown**), `plugin/unavailable` (not sent), `plugin/tool-error`, `plugin/invalid-result`, and at inspection `plugin/manifest-invalid`, `plugin/unsupported-platform`, `plugin/too-large`, `plugin/consent-unavailable`.

**Capability names, and exactly what each grants.** The table is closed; a manifest that names anything else is invalid.

| Capability | Parameters | A grant allows | An attached (model-authored) plugin may hold it |
|---|---|---|---|
| `tools.offer` | none | The host lists and calls the plugin's tools. Each is registered through `tools.Dynamic` ([tools.md §6.1](tools.md#61-shape)) as `plugin__<id>__<raw>`, with effects unknown (so the default policy asks), never overlapping, results bounded by the pipeline | yes |
| `tools.call` | `tools`: exact names | `host/callTool` for a listed tool while serving a call. It enters the pipeline at stage 0 as origin `direct`, with the served call as parent and that call's agent as the attributed agent; each nested call that needs approval asks that agent's answerer | yes |
| `session.events.read` | `types`: exact event type names, no wildcard | Copies of committed session events of those types | yes, its own session only |
| `session.records.write` | none | One `plugin:<id>/<record>` record ([core-session.md §2](core-session.md#2-vocabulary)) of at most 64 KiB, into a session the plugin has served a call for or received an event from. The owner segment is set by the host | yes, its own session only |
| `skills.provide` | none | Registration with area A14's skill registry as source class `plugin` | no |
| `net.outbound` | none | The sandbox policy's network setting is `allowed`; without it the setting is `denied` | yes |
| `credential.use` | `name`: one entry of `credentials` | The operator maps a credential reference to that entry; the value is placed in the named environment variable at start and is added to the secret store's mask | no |

No grant, at any trust level, lets a plugin answer or create an interaction, create or revoke a session grant, switch a preset or policy, read a secret through a method, write another owner's records, choose its own principal, register a guard or a policy listener, or have the host start another program.

**What a plugin can contribute, and what it deliberately cannot** ([DH-ADR-EXT-5](#dh-adr-ext-5)).

| Extension point | Version 1 | Why |
|---|---|---|
| Tools | offered | The reason most plugins exist; the pipeline already bounds them |
| Calling back into the host | tools only, nested in a served call | Rule 6 of [../design.md §6](../design.md#6-security-model): no entry skips the pipeline. A call needs an agent to attribute and to ask |
| Listening to events | committed session events, read-only, after the fact | An observer cannot change an outcome. Interception (`tools.Decide`, `tools.Shape`) is not offered: an untrusted process must not be able to return `Allow` |
| Persisting into a session | plugin records | Always ignorable, never model-visible ([DH-ADR-PERSISTENCE-2](../upstream/session-and-persistence.md#dh-adr-persistence-2)) |
| Providing a seam | skills only | A provider under a built-in tool (filesystem, web, model adapter) inherits that tool's trust; each needs its own capability and review, later |
| UI fragments | not offered | HTML from an untrusted process needs a sanitising boundary that no area specifies; a plugin's tools get the generic tool card |
| Injecting or steering messages | not offered | Text the model reads as the user's is an injection channel |
| Registering further processes (MCP servers, vendor CLIs) | not offered | The operator inspected one `run` command, not the commands it might register later |

**Grants: requested, recorded, shown, revoked.**

| | Installed plugin (host scope) | Attached plugin (session scope) |
|---|---|---|
| Requested by | The manifest. An owner starts the flow through the management surface (A21), which calls `Inspect` and shows the report: identity, digest, start command, each capability with its parameters and reason, and the sandbox backend's status | The manifest. The model asks through A21's management tool, which calls `Attach` |
| Decided by | An owner, capability by capability; a credential request is answered by choosing a credential reference | An owner on the session's bound channel, through one `consent` interaction (A17): yes or no to the whole request. With no answerer it is `plugin/consent-unavailable` |
| Recorded in | The plugin's loader entry in the writable layer: `dir`, `digest`, `grants`, `credentials`, `acceptUnenforced` | The durable session event `plugin-host/attached`, beside A17's asked-and-decided pair |
| Shown in | The inventory (A18, [DH-D-HOST-10](host-telemetry.md#dh-d-host-10)) and the plugins page (C7, [DH-D-UISET-6](ui-settings-plugins.md#dh-d-uiset-6)), `dsh plugin` output (A21) and the `Plugins` inspect provider | The same, plus the transcript |
| Revoked by | Editing the entry; the loader restarts that one entry with the smaller set. Disabling or removing the entry ends everything | `Detach`, the agent's disposal or the session's end; logged as `plugin-host/detached`. Never restored on resume |

A grant set is fixed for one protocol session: the plugin learns it in `initialize`, and a change is a stop and a start ([DH-ADR-EXT-6](#dh-adr-ext-6)).

**Supervision** uses area A15's rules and numbers ([DH-ADR-EXT-7](#dh-adr-ext-7)).

| Phase | Rule |
|---|---|
| Start | Verify the approved copy against the recorded digest. Build the environment explicitly (scrubbed base, manifest `env`, mapped credentials). Ask `policy.Resolver.ForPlugin` for `read-only` with the state directory as the only write root. Start the argument list through `subprocess.HostKey` with pipes and a collected standard-error tail, working directory the approved copy |
| Readiness | The handshake and first listings within `startupTimeout` (30 s) |
| Liveness | `ping` every 15 s while idle, 5 s deadline, two misses mean hung. A cancelled request not answered within `cancelGrace` (5 s), or an outbound queue that stays full, also means hung. A line over `maxLineBytes` (1 MiB) loses framing and is treated as a crash |
| Pending calls on a crash or hang | A call already sent fails as `plugin/connection-closed`, and its text tells the model the outcome is unknown. A call not yet sent fails as `plugin/unavailable`. Nested calls the plugin had in flight are cancelled and settle in the log before the outer result. Nothing is replayed |
| Restart | Stop first, then start: two copies never run. Delays double from 500 ms to 30 s for at most 10 attempts; the last generation stays registered meanwhile. An exhausted budget removes the plugin's contributions as a set, logs it, and sets `closed`; `Restart` gives a fresh budget. A changed source directory restarts nothing |
| Shutdown | Close the gate (authority first), remove contributions, send `shutdown`, close input, terminate the tree, kill the tree, each bounded by `closeTimeout` (5 s); then confirm the tree is gone. If it cannot be confirmed the status is `stopping-unconfirmed` and no replacement starts |

A manifest with `protocol: mcp` is not supervised here at all: the plugin's fiber registers one entry with the MCP fleet (`mcp.Service.Register`, `scope: host`, name the plugin id, confinement from the grant), and the fleet starts, supervises and names its tools `mcp__<id>__<raw>`. Such a plugin can hold only `tools.offer`, `net.outbound` and `credential.use`.

**Events, config, services.**

| Upstream unit | Go replacement | Form |
|---|---|---|
| `UP-EVT-cordis/dynamic-package` | `plugin.StateChanged` with status `running` | Capability event, notify; payload is the `Descriptor` |
| `UP-EVT-cordis/dynamic-retract` | `plugin.StateChanged` with a stopped status, or the descriptor's removal | The same key |
| `UP-EVT-cordis/request-run` | None of this area's: the pending `consent` item in A17's registry | Delivered by A17 to the bound answerer |
| `UP-EVT-cordis/request-run-resolved` | None of this area's: A17's settlement notice for that item | A17 |
| `UP-EVT-cordis/inspect-query`, `UP-EVT-cordis/inspect-query-resolved` | None: no inspect provider lives in a page | Dropped ([DH-ADR-EXT-9](#dh-adr-ext-9)) |
| `UP-CFG-dsh-cordis-host-runner` | `host.Config`: `modelAuthored` (`ask`; or `off`), `maxPlugins` (32), `maxAttachedPerSession` (4), `startupTimeout` (30 s), `callTimeout` (60 s), `pingInterval` (15 s), `pingTimeout` (5 s), `cancelGrace` (5 s), `closeTimeout` (5 s), `restart{initialDelay 500 ms, maxDelay 30 s, maxAttempts 10}`, `maxLineBytes` (1 MiB), `maxPending` (64), `maxTools` (128), `maxSchemaBytes` (65,536), `copy{maxFiles 4096, maxBytes 64 MiB}`, `requireFull` (false). And `plugin.Entry`: `dir`, `digest`, `grants`, `credentials`, `acceptUnenforced`, `pinned` (true), `overrides{callTimeout, allowTools, denyTools, disclosure}` | Strict decoding; positive durations; absolute `dir`. `vmTimeoutMs` becomes the start-up and call deadlines; `clientInspectTimeoutMs` has no successor |
| `UP-SVC-dynamicCordisRunner` | `plugin.Key` with `plugin.Service` | §6.1 |
| `UP-SVC-cordisInspect` | `inspect.Key` with `inspect.Service` | §6.1 |

**Persisted data.** The grant record (in the writable config layer). Approved copies under `<home>/plugins/<id>/code/<digest>/` and state under `<home>/plugins/<id>/state/`; for an attached plugin both live under `<home>/plugins/attached/<session>/<id>/`. Two durable, log-only session event types, each holding the whole value: `plugin-host/attached` (id, title, version, digest, trust, granted capabilities, who consented) and `plugin-host/detached` (id, reason). Plugin records, through `AppendPluginRecord` ([core-session.md §6](core-session.md#6-go-design)).

**Tools exposed to the model.** `harness_inspect` replaces `UP-TOOL-dsh-tool-cordis.cordis_inspect`: the same two-step shape (no argument lists providers; `{provider, method, input}` queries one), `Effects: none`, may overlap. The `platform` argument is gone. Providers: `Entries` (the composed tree from A18's inventory: id, component, phase, supplying layer, in pages of 25 up to 100), `Config` (one entry's config JSON Schema; shapes, never values), `Tools` (the asking agent's visible tools), `Events` (session event types with payload schemas), `Plugins` (descriptors), `PluginProtocol` (the manifest schema, the method and capability tables, the error codes) and `Runtime` (version, platform, protocol versions). A plugin's own tools appear under its prefix. The tool with which the model asks to attach or detach belongs to A21.

**Placing upstream's two client packages** ([DH-ADR-3](../design.md#dh-adr-3)).

| Upstream | What the user saw | Go placement |
|---|---|---|
| `ui-cordis` panel and badge | Every definition, its state, run and stop | Rows of the server-rendered plugins page (C7, [DH-D-UISET-6](ui-settings-plugins.md#dh-d-uiset-6); data from A18's inventory, controls from A21), built from `Descriptor` and re-rendered on `plugin.StateChanged` through the event stream (A19) |
| Approve and decline in the panel | A pending run | The `consent` fragment (A17), in the transcript and on the plugins page |
| `cordis_define`, `cordis_run`, stop and undefine cards | A record of each lifecycle call | No such tools exist. `plugin-host/attached` and `plugin-host/detached` render as transcript notices; `harness_inspect` uses the generic tool card |
| `tool.view.cordis` slot, and all of `cordis-client-runner` | A plugin's own UI in the page | Recorded drop ([DH-ADR-EXT-1](#dh-adr-ext-1)) |

Capability names, their one-line descriptions and the status words are entries in the message catalogue, in English and Chinese.

### 6.3 Ownership, failure, retry, rollback and cleanup

| Resource | Class | Owner | Released when |
|---|---|---|---|
| The plugin process and its tree | Created | The plugin's child fiber, under `plugin.host` | Fiber unload: the stop ladder; A09's tree owner if the host dies |
| Tool registrations, event subscriptions, the skill provider, an MCP fleet entry | Created | The same fiber; each is a cordis effect acquired through its context | Fiber unload, before the process is asked to stop; tools leave as one set |
| The gate's grant set | Access | The fiber | Closed first on unload, so a dying plugin has no authority |
| An attached plugin | Created | The same kind of fiber, plus an effect in the asking agent's scope | The agent's disposal, `Detach`, or `plugin.host` unloading |
| The approved copy | Created | The `plugin.host` component | A newer digest is approved, or the entry is removed; registered with A07's retention. For an attached plugin, at detach |
| The state directory | Created; previously owned after a restart | The harness home | Only by an explicit purge of an installed plugin (A21); at detach for an attached one |
| The plugin directory | Shared | The operator, or the workspace | Never modified, never deleted |
| The grant record | Access | The owner who wrote it; stored in the writable layer | Edited or removed through the management surface |
| A pending call to the plugin | Created | The tool call | Settles exactly once: result, cancellation, or a failure from the table below |

| Situation | What happens | Undone, and what stays uncertain |
|---|---|---|
| The manifest is invalid, the directory too large, or a link leaves the directory | `Inspect` returns every problem; nothing is copied or started | Nothing to undo |
| Copying fails half-way | The staging directory is removed; no grant is written | Nothing remains |
| The approved copy does not match its recorded digest at start | `failed{stage: verify}`; not started | The copy is kept for the operator to examine |
| The source directory changes after approval | The approved copy keeps running; the descriptor shows `SourceChanged`; the new bytes run only after a new approval | Nothing; an unpinned entry (`pinned: false`, operator-only) is the stated exception |
| No sandbox backend can give the requested policy | `unavailable{reason}`; the policy is never loosened | Nothing ran |
| Handshake timeout, wrong version, identity mismatch | The stop ladder; `failed{stage: handshake}`. Only a timeout is retried, under the budget | Nothing was registered |
| The process crashes or hangs during calls | As the supervision table; the host and other plugins continue | **Uncertain:** what the plugin did before it failed |
| A call arrives without its grant | `plugin/capability-denied`; the process keeps running; denials are counted on the descriptor | Nothing ran |
| A listing fails or a tool name collides | The previous generation is kept; the attempted set is refused whole | Nothing changes |
| The plugin reads events too slowly | Events for it are dropped and `events/gap` says which; a session is never delayed | The plugin's view has a hole it is told about |
| Attach is refused, or the plugin fails to start after consent | `plugin-host/detached{reason}` is appended if `attached` was; the copy and state are removed | The consent pair stays in the log as a fact |
| A session with an attached plugin is resumed after its host stopped | The plugin is not restored; the holder appends `plugin-host/detached{reason: not-restored}` | The model sees the smaller tool list in the next request header |
| The stop cannot be confirmed | `stopping-unconfirmed`; the handle and status outlive the fiber; the next `Restart` or unload retries | **Uncertain:** a surviving process, named in the status |
| The host is killed | The operating-system owner ends the tree where one exists (Windows job, Linux cgroup); the plugin also sees its input close | **Uncertain:** on macOS a plugin that ignores end of input may survive (A09) |

## 7. Decisions

<a id="dh-adr-ext-1"></a>
### DH-ADR-EXT-1 No code is loaded into the host or the page: a plugin is a directory and a process

- **Decision.** Upstream's dynamic packages, the `node:vm` host half, the browser half and their run round trip are not built. "The model writes a plugin" now means: the model writes a plugin directory whose program is in a language already on the machine, and asks for it to be attached; an owner's consent is required.
- **Why.** [DH-ADR-4](../design.md#dh-adr-4), [DH-ADR-8](../design.md#dh-adr-8) and [DH-ADR-3](../design.md#dh-adr-3) leave no in-process interpreter and no client plugin runtime, and upstream states that its realm is not a security boundary.
- **Tradeoff. What can no longer be done:** a plugin cannot inject arbitrary host services, listen to or intercept arbitrary events, or provide a service; it cannot add UI (slots, panels, a tool's own view, themes); it cannot be changed in place, because a change is a new approval and a restart that loses in-memory state; it needs an interpreter or a built binary on the machine, which differs by platform; and each tool call costs a message round trip. Gained: real confinement, crash isolation, any language, and installed plugins that survive a restart.
- **Serves.** [DH-O-11](../design.md#dh-o-11). **Verified by.** `DH-T-EXT-5`, [DH-E-EXT-1](#dh-e-ext-1), [DH-E-EXT-3](#dh-e-ext-3).

<a id="dh-adr-ext-2"></a>
### DH-ADR-EXT-2 The manifest is one strict JSON file with an argument-list start command

- **Decision.** `dsh-plugin.json`, decoded strictly, with the fields of §6.2; `run` is an argument list; there is no install or build hook; a manifest format number guards future changes.
- **Why.** Every language can write JSON, and the file is digested byte for byte. An argument list cannot hide a second command. A hook would run unreviewed code before any grant exists.
- **Tradeoff.** Hand-written configuration elsewhere is YAML ([DH-ADR-LOADER-1](../libs/cordis-loader.md#dh-adr-loader-1)); a plugin author gets no comments. A plugin that needs dependencies must arrive with them or use what the machine has.
- **Serves.** [DH-O-11](../design.md#dh-o-11). **Verified by.** `DH-T-EXT-1`.

<a id="dh-adr-ext-3"></a>
### DH-ADR-EXT-3 A grant binds to exact bytes: a digest and an approved copy

- **Decision.** At approval the host computes a SHA-256 over the manifest and every file (sorted relative paths, sizes and contents; links that leave the directory are refused; limits on files and bytes), copies those bytes into the harness home, and records the digest in the grant. The copy is what runs, read-only. A different digest is a different plugin as far as consent goes. There is no "approve future versions".
- **Why.** Inference: a start-time check of the source directory would not be enough, because an interpreter may load a file long after start, and the model can write to a directory in its workspace. The home is outside every write root.
- **Tradeoff.** Disk use doubles for the plugin, large directories are refused, and a development loop needs a new approval per change. An owner may mark one installed entry `pinned: false` to run in place; it is shown as unpinned everywhere and is never available to an attached plugin.
- **Serves.** [DH-O-11](../design.md#dh-o-11), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-EXT-2`, [DH-E-EXT-3](#dh-e-ext-3).

<a id="dh-adr-ext-4"></a>
### DH-ADR-EXT-4 Protocol version 1 is shaped like MCP: lists are pulled, changes are announced, versions are integers

- **Decision.** The method set of §6.2. The host pulls `tools/list` and `skills/list`; the plugin announces `tools/changed`; readiness is the handshake plus the first listings, with no extra message. The host offers versions and the plugin picks one. Error codes come from the shared table.
- **Why.** A plugin author who knows MCP already knows the model, and the host can treat a plugin generation exactly as area A15 treats an MCP generation. A pulled list is idempotent; a stream of register calls is not.
- **Tradeoff.** A plugin cannot register one tool at a time. Two tool protocols exist side by side; the plugin protocol exists only for what MCP cannot express (nested calls into the pipeline, events, records, skills under grants).
- **Serves.** [DH-O-11](../design.md#dh-o-11). **Verified by.** `DH-T-EXT-3`, [DH-E-EXT-1](#dh-e-ext-1).

<a id="dh-adr-ext-5"></a>
### DH-ADR-EXT-5 Version 1 offers tools, nested tool calls, committed events, plugin records and skills; every contribution is an effect of the plugin's fiber

- **Decision.** The extension-point table of §6.2. Each contribution is acquired as a cordis effect through the context of the plugin's child fiber, so unloading that fiber removes all of them and then stops the process. Interception, seam providers other than skills, UI fragments, message injection and registering further processes are not offered.
- **Why.** Upstream's rule that "everything the plugin registered is an effect on its fiber" is what makes stop exact; the process is one more effect. Each excluded point would let an untrusted process decide for the host or speak as the user.
- **Tradeoff.** This narrows [DH-ADR-SECURITY-4](../upstream/security.md#dh-adr-security-4)'s "call a named host service" to "call a named tool through the pipeline". Upstream plugins that add UI or change the loop have no counterpart. Each later extension point is a new capability and, where needed, a new protocol version.
- **Serves.** [DH-O-1](../design.md#dh-o-1), [DH-O-6](../design.md#dh-o-6), [DH-O-11](../design.md#dh-o-11). **Verified by.** `DH-T-EXT-8`, `DH-T-EXT-9`, `DH-T-EXT-10`, [DH-E-EXT-1](#dh-e-ext-1).

<a id="dh-adr-ext-6"></a>
### DH-ADR-EXT-6 Capabilities are a closed table checked on every inbound message; a change of grants is a restart

- **Decision.** One table in `wire/plugin` maps each plugin-to-host method to the capability and parameters it needs. The gate consults it before any handler; a method with no row cannot be dispatched. The grant set is fixed for a protocol session.
- **Why.** Upstream keeps its whitelist in two hand-mirrored copies. One table can be tested exhaustively and printed for the operator. A fixed set means the plugin, the gate and the operator's screen cannot disagree.
- **Tradeoff.** Revoking one capability restarts the plugin and loses its in-memory state. Parameters are exact names, so a new tool or event type needs a new grant.
- **Serves.** [DH-O-11](../design.md#dh-o-11). **Verified by.** `DH-T-EXT-4`, `DH-T-EXT-5`, [DH-E-EXT-1](#dh-e-ext-1).

<a id="dh-adr-ext-7"></a>
### DH-ADR-EXT-7 One supervision mechanism: the MCP fleet's rules, and tool-only plugins are fleet entries

- **Decision.** A plugin process uses the same starter, policy resolver, stop ladder, status vocabulary, restart numbers and "keep the tools, fail the calls honestly" rule as an MCP server ([DH-ADR-CONNECT-2](mcp-lsp-web-connector.md#dh-adr-connect-2), [DH-ADR-CONNECT-3](mcp-lsp-web-connector.md#dh-adr-connect-3)). A `protocol: mcp` plugin is registered with the fleet and not supervised here. A restart stops the old process before starting the new one; nothing watches the source directory.
- **Why.** Two supervisors with different budgets and states would be two things to learn and test. Stop-before-start answers the question [../libs/cordis-plugins.md](../libs/cordis-plugins.md) §7 left to this area: two copies of one plugin would share a state directory.
- **Tradeoff.** A restart has a gap in which calls fail as not sent. The supervisor loop is specified inside A15's fleet; until it is shared code this area mirrors it and one conformance test covers both (§10).
- **Serves.** [DH-O-11](../design.md#dh-o-11), [DH-O-1](../design.md#dh-o-1). **Verified by.** `DH-T-EXT-6`, `DH-T-EXT-7`, `DH-T-EXT-13`, [DH-E-EXT-2](#dh-e-ext-2), [DH-E-EXT-4](#dh-e-ext-4).

<a id="dh-adr-ext-8"></a>
### DH-ADR-EXT-8 A grant given inside a session ends with it; a standing grant comes only from an owner through the management surface

- **Decision.** Decided by the owner ([DH-ADR-30](../design.md#dh-adr-30)): session attachment only, and the `plugin_manager` tool has `list`, `inspect`, `attach` and `detach`. The model can only ask to **attach** a plugin to its own session. That needs one consent from an owner on the bound channel, covers one digest, is logged, and is never restored. Tools of an attached plugin are registered in the asking agent's scope. Making a plugin host-wide and durable is an owner's action in the management surface; no tool call can do it. `modelAuthored: off` removes attaching altogether.
- **Why.** It applies [DH-ADR-16](../design.md#dh-adr-16) to plugins: consent given at a prompt must not outlive the session. Upstream's Creator path lets one approved call change the profile for every session ([../upstream/decisions-2.md](../upstream/decisions-2.md), row 1).
- **Tradeoff.** A useful model-written plugin must be approved again in each session until an owner installs it. Consent is all-or-nothing, because the `consent` kind answers yes or no.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-11](../design.md#dh-o-11). **Verified by.** `DH-T-EXT-11`, [DH-E-EXT-3](#dh-e-ext-3).

<a id="dh-adr-ext-9"></a>
### DH-ADR-EXT-9 Inspection is host-only, read-only and generated

- **Decision.** Keep the tool's two-step shape and the provider registry; rename it `harness_inspect`; drop the client platform and its two round-trip events; build every answer from live registries or from schemas derived from Go types. No provider method changes anything.
- **Why.** The model still needs facts before it writes a plugin or proposes a config change, and what it now needs most is the plugin protocol itself. Upstream's 880 KB catalogue describes in-process APIs a plugin can no longer call.
- **Tradeoff.** No description of the UI's structure. The tool cannot fix what it shows; changes go through settings (A07) and plugin management (A21). That keeps one upstream limit on purpose.
- **Serves.** [DH-O-2](../design.md#dh-o-2), [DH-O-11](../design.md#dh-o-11). **Verified by.** `DH-T-EXT-12`.

<a id="dh-adr-ext-10"></a>
### DH-ADR-EXT-10 A plugin runs read-only with one state directory and no network unless granted; an effect the system cannot enforce is shown and recorded, never assumed

- **Decision.** Every plugin, at both trust levels, runs under `read-only` with its state directory as the only write root, on the host (`subprocess.HostKey`), with network `denied` unless `net.outbound` is granted. Enforcement `partial` is accepted by default, but the inspection report and the consent show the backend and each effect it cannot govern, and where an asked-for effect cannot be enforced at all (network denial and reads on Windows) the grant must list it in `acceptUnenforced` or the plugin does not start. `requireFull` refuses anything less than `full`.
- **Why.** [DH-ADR-SECURITY-4](../upstream/security.md#dh-adr-security-4) sets the defaults; [sandbox-ssh.md](sandbox-ssh.md) refuses `network: denied` on Windows and asked this area what a plugin does there. Requiring `full` for model-authored plugins would make them impossible on Windows, where enforcement is always partial.
- **Tradeoff.** On Windows a plugin can read the user's files and reach the network once the owner accepts that in writing. A plugin always runs on the host machine, also when the session's world is a remote one.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-11](../design.md#dh-o-11). **Verified by.** `DH-T-EXT-14`, [DH-E-EXT-1](#dh-e-ext-1).

## 8. Deliverables

<a id="dh-d-ext-1"></a>
### DH-D-EXT-1 A plugin directory is read, checked, digested and copied before anything runs

- **Result and why it matters.** An operator, a command or the host can say what a directory is and asks for, and fix its bytes, without starting it. Every later guarantee refers to that digest.
- **How it is implemented.** A pure library: strict decoding with positions, the manifest type's JSON Schema from `schema`, a deterministic walk (sorted paths, no link that leaves the root, limits), SHA-256, and a copy into a staging directory that is renamed into place only when its digest matches. `Inspect` returns every problem at once, plus platform, version-range and protocol-version results.
- **Components and files.** `wire/plugin/manifest`, the `Manifest` types in `wire/plugin`.
- **Depends on.** [DH-D-UTIL-1](util-test-support.md#dh-d-util-1) and [DH-D-UTIL-4](util-test-support.md#dh-d-util-4) (`schema` and the version-range helper); [DH-D-STORAGE-2](storage.md#dh-d-storage-2) and [DH-D-STORAGE-9](storage.md#dh-d-storage-9) (the `<home>/plugins` directory in the home layout and a retention registration). No runtime prerequisite.
- **Serves.** [DH-O-11](../design.md#dh-o-11), [DH-O-7](../design.md#dh-o-7). **Priority.** P1.
- **Done when.** `DH-T-EXT-1` and `DH-T-EXT-2` pass and [DH-E-EXT-1](#dh-e-ext-1) has passed.

<a id="dh-d-ext-2"></a>
### DH-D-EXT-2 The plugin protocol, version 1, with a conformance kit (shared piece)

- **Result and why it matters.** One written definition of every message, the handshake and the errors, from which the host, the documentation, the inspect provider and a plugin author's tests all come.
- **How it is implemented.** One Go struct per message with the method and version tables beside them; the protocol's JSON Schema is derived and checked for freshness. A host-side session type wraps `sdk/jsonrpc` (module `src/sdk`): handshake state, request correlation, cancellation, bounded pending and outbound queues. The kit holds a scriptable fake plugin (hang, flood, crash, ignore shutdown) and a suite that any plugin program can be run against.
- **Components and files.** `wire/plugin/proto`, `wire/plugin/kit`.
- **Depends on.** [DH-D-SDK-1](sdk-acp-typert.md#dh-d-sdk-1): the newline-delimited JSON-RPC codec with a line limit, `$/cancel`, bidirectional requests on one pipe pair and a bounded writer ([DH-ADR-WIRE-5](../upstream/wire-and-api.md#dh-adr-wire-5), [DH-ADR-WIRE-9](../upstream/wire-and-api.md#dh-adr-wire-9)); [DH-D-UTIL-1](util-test-support.md#dh-d-util-1) (`schema`).
- **Serves.** [DH-O-11](../design.md#dh-o-11). **Priority.** P1.
- **Done when.** `DH-T-EXT-3` passes and [DH-E-EXT-1](#dh-e-ext-1), which runs the suite against a plugin written in a second language, has passed.

<a id="dh-d-ext-3"></a>
### DH-D-EXT-3 A plugin can do only what was granted: the capability table, the gate and the grant record (shared piece)

- **Result and why it matters.** Every message from a plugin is checked against grants an owner gave, and those grants can be read and withdrawn. This is the "least-privilege" half of [DH-O-11](../design.md#dh-o-11).
- **How it is implemented.** The capability table of §6.2 as data; a gate function called by the protocol session before dispatch, holding an immutable grant set; grant validation at entry load (every grant names a requested capability with parameters no wider than requested); the `plugin.Entry` config type; credential mapping resolved through the secret store at start.
- **Components and files.** `wire/plugin` (table, types), `wire/plugin/host` (gate), `wire/plugin/entry`.
- **Depends on.** [DH-D-EXT-1](#dh-d-ext-1), [DH-D-EXT-2](#dh-d-ext-2), [DH-D-LOADER-8](../libs/cordis-loader.md#dh-d-loader-8); [DH-D-HUMAN-7](interaction-credentials.md#dh-d-human-7) (`secret.Store.Acquire` for a mapped credential). The commands and pages that show an inspection report and write the entry depend on this ([DH-D-BOOT-9](boot.md#dh-d-boot-9), [DH-D-UISET-6](ui-settings-plugins.md#dh-d-uiset-6)).
- **Serves.** [DH-O-11](../design.md#dh-o-11), [DH-O-7](../design.md#dh-o-7). **Priority.** P1.
- **Done when.** `DH-T-EXT-4` and `DH-T-EXT-5` pass and [DH-E-EXT-1](#dh-e-ext-1) has passed.

<a id="dh-d-ext-4"></a>
### DH-D-EXT-4 A plugin process is started confined, watched, restarted within a budget and stopped as a tree

- **Result and why it matters.** A plugin that crashes, hangs, floods or ignores shutdown costs its own pending calls and nothing else. This is the "supervised" half of [DH-O-11](../design.md#dh-o-11).
- **How it is implemented.** `Run` creates one child fiber per plugin. Its body runs the start sequence and a supervisor loop of §6.2 on a virtual-time-friendly clock; the process handle, the gate and the status cell are effects. Status changes are published as `plugin.StateChanged` with a value copy, and `Descriptor` values are read from the status cell; both belong to this deliverable, because the host inventory reads them.
- **Components and files.** `wire/plugin/host`.
- **Depends on.** [DH-D-EXT-1](#dh-d-ext-1), [DH-D-EXT-2](#dh-d-ext-2), [DH-D-EXT-3](#dh-d-ext-3), [DH-D-EXEC-1](execution.md#dh-d-exec-1), [DH-D-EXEC-2](execution.md#dh-d-exec-2), [DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1), [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1), [DH-D-CORDIS-4](../libs/cordis.md#dh-d-cordis-4), [DH-D-LOADER-11](../libs/cordis-plugins.md#dh-d-loader-11). Under `--safe` no plugin process is started, an attach is refused and every plugin reads `disabled by safe mode`; safe mode never loosens a grant, the sandbox or an approval. Runtime prerequisite: a working sandbox backend, and the plugin's interpreter or binary on the host.
- **Serves.** [DH-O-11](../design.md#dh-o-11), [DH-O-1](../design.md#dh-o-1), [DH-O-7](../design.md#dh-o-7). **Priority.** P1.
- **Done when.** `DH-T-EXT-6`, `DH-T-EXT-7` and `DH-T-EXT-14` pass and [DH-E-EXT-1](#dh-e-ext-1) and [DH-E-EXT-2](#dh-e-ext-2) have passed.

<a id="dh-d-ext-5"></a>
### DH-D-EXT-5 A plugin's tools and nested calls belong to its fiber and pass the host's own paths

- **Result and why it matters.** A plugin's tool is an ordinary tool: validated, policed, approved, bounded and logged. Disabling the plugin removes everything it added, exactly once.
- **How it is implemented.** Tools: one `tools.Dynamic` registration per listed tool, swapped per generation, whose body sends `tools/call` and maps the reply. Nested calls: a table from served call id to its pipeline context; `host/callTool` becomes `tools.Service.Call` with that parent. Event subscriptions, records and skills are [DH-D-EXT-9](#dh-d-ext-9).
- **Components and files.** `wire/plugin/host`.
- **Depends on.** [DH-D-EXT-4](#dh-d-ext-4), [DH-D-TOOLS-1](tools.md#dh-d-tools-1), [DH-D-TOOLS-2](tools.md#dh-d-tools-2), [DH-D-CORDIS-3](../libs/cordis.md#dh-d-cordis-3); [DH-D-STORAGE-3](storage.md#dh-d-storage-3) (storing a file a tool result refers to).
- **Serves.** [DH-O-11](../design.md#dh-o-11), [DH-O-6](../design.md#dh-o-6), [DH-O-1](../design.md#dh-o-1). **Priority.** P1.
- **Done when.** `DH-T-EXT-8` and `DH-T-EXT-9` pass and [DH-E-EXT-1](#dh-e-ext-1) has passed.

<a id="dh-d-ext-9"></a>
### DH-D-EXT-9 A plugin's event subscriptions, records and skills belong to its fiber

- **Result and why it matters.** A plugin can follow the committed events it was granted, keep its own records in a session and offer skills, and disabling it removes each of these exactly once. Split out of [DH-D-EXT-5](#dh-d-ext-5) so that its lower priority has its own entry.
- **How it is implemented.** Events: a feed listener that copies granted types into a bounded per-plugin queue and never blocks; a stalled plugin gets `events/gap`. Records: `AppendPluginRecord` with the owner segment set by the host. Skills: a provider registered with area A14 under the capability `skills.provide`.
- **Components and files.** `wire/plugin/host`.
- **Depends on.** [DH-D-EXT-5](#dh-d-ext-5); [DH-D-SESSION-2](core-session.md#dh-d-session-2) (the committed-event feed and `AppendPluginRecord`); [DH-D-CONTEXT-8](context-compaction-skill.md#dh-d-context-8) (`RegisterProvider`).
- **Serves.** [DH-O-11](../design.md#dh-o-11), [DH-O-1](../design.md#dh-o-1). **Priority.** P2.
- **Done when.** `DH-T-EXT-10` passes.

<a id="dh-d-ext-6"></a>
### DH-D-EXT-6 A tool-only plugin that speaks MCP runs as an entry of the MCP fleet

- **Result and why it matters.** An existing MCP server becomes a plugin by adding a manifest, and gains inspection, a digest and grants, with no second supervisor.
- **How it is implemented.** For `protocol: mcp` the plugin fiber builds an `mcp.Entry` from the manifest and the grant (`read-only`, the state directory as the only root, network from `net.outbound`, credentials as environment) and registers it as an effect; the descriptor's status is read from the fleet's handle.
- **Components and files.** `wire/plugin/host`.
- **Depends on.** [DH-D-EXT-1](#dh-d-ext-1), [DH-D-EXT-3](#dh-d-ext-3), [DH-D-CONNECT-2](mcp-lsp-web-connector.md#dh-d-connect-2), [DH-LIB-MCP](../libs/modelcontextprotocol.md). Runtime prerequisite: the server program on the host.
- **Serves.** [DH-O-11](../design.md#dh-o-11). **Priority.** P1.
- **Done when.** `DH-T-EXT-13` passes and [DH-E-EXT-4](#dh-e-ext-4) has passed.

<a id="dh-d-ext-7"></a>
### DH-D-EXT-7 The model can write a plugin and have it attached to its own session after an owner consents

- **Result and why it matters.** Upstream's "the agent extends itself" survives as far as a process boundary allows, with the consent visible and bounded to one session.
- **How it is implemented.** `Attach` runs: inspect; check `modelAuthored`, the per-session cap and the capabilities an attached plugin may hold; ask one `consent` whose display is the inspection report; copy; append `plugin-host/attached`; start as in [DH-D-EXT-4](#dh-d-ext-4) with tools registered in the asking agent's scope. A registered projection of the two event types lets a resumed session close an attachment that was not restored. Failures are returned to the model as every problem found, with the plugin's standard-error tail, so it can correct the directory.
- **Components and files.** `wire/plugin/host`.
- **Depends on.** [DH-D-EXT-4](#dh-d-ext-4), [DH-D-EXT-5](#dh-d-ext-5), [DH-D-HUMAN-1](interaction-credentials.md#dh-d-human-1), [DH-D-HUMAN-2](interaction-credentials.md#dh-d-human-2) (the `consent` kind, with the inspection report as its display; it answers yes or no and carries no selection); [DH-D-SESSION-1](core-session.md#dh-d-session-1) (this area declares its two `plugin-host/*` event types as log-only) and [DH-D-SESSION-2](core-session.md#dh-d-session-2) (the `not-restored` closer is appended on `session.Created` with `How: resumed`). The model-facing management tool that calls `Attach` and `Detach` depends on this ([DH-D-BOOT-10](boot.md#dh-d-boot-10)).
- **Serves.** [DH-O-11](../design.md#dh-o-11), [DH-O-7](../design.md#dh-o-7). **Priority.** P2.
- **Done when.** `DH-T-EXT-11` passes and [DH-E-EXT-3](#dh-e-ext-3) has passed. That scenario makes an approved copy under `<home>/plugins/attached` and removes it at detach, so it runs in this deliverable's build stage.

<a id="dh-d-ext-8"></a>
### DH-D-EXT-8 The model and the operator can inspect the composed harness and its plugins, read-only

- **Result and why it matters.** Before writing a plugin or proposing a setting, the model reads the real tree, its config shapes, its own tools and the plugin protocol; the plugins page shows every plugin's state without a client-side registry.
- **How it is implemented.** `host.inspect` holds a registry of providers (registration is an effect); `tool.inspect` registers `harness_inspect`. First-party providers read the inventory, the tool registry, the event registry and `plugin.Service`; `PluginProtocol` serves the derived schema. `Descriptor` and `plugin.StateChanged` are what the inventory consumes.
- **Components and files.** `host/inspect`, `host/inspect/tool`, the descriptor in `wire/plugin`.
- **Depends on.** [DH-D-EXT-2](#dh-d-ext-2), [DH-D-HOST-10](host-telemetry.md#dh-d-host-10), [DH-D-TOOLS-1](tools.md#dh-d-tools-1); [DH-D-BUNDLE-4](bundle-preset.md#dh-d-bundle-4) (presets in the inventory). The plugins page, its routes and controls depend on this ([DH-D-UISET-6](ui-settings-plugins.md#dh-d-uiset-6)).
- **Serves.** [DH-O-2](../design.md#dh-o-2), [DH-O-11](../design.md#dh-o-11). **Priority.** P2. The `Descriptor` and `plugin.StateChanged` that the host inventory reads are part of [DH-D-EXT-4](#dh-d-ext-4) (P1), not of this deliverable.
- **Done when.** `DH-T-EXT-12` passes.

## 9. Acceptance

### 9.1 Local tests

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-EXT-1` | [DH-D-EXT-1](#dh-d-ext-1) | Each invalid manifest (unknown key, bad name, shell string in `command`, unknown capability, parameters missing) is refused with every problem and its position; platform, version range and protocol versions are checked; a fuzz target covers the decoder |
| `DH-T-EXT-2` | [DH-D-EXT-1](#dh-d-ext-1) | The digest is the same for the same bytes on every system and changes with any byte, name or added file; a link leaving the directory and a directory over the limits are refused; a failed copy leaves nothing; a tampered copy is detected at start; a changed source sets `SourceChanged` and runs nothing new |
| `DH-T-EXT-3` | [DH-D-EXT-2](#dh-d-ext-2) | Over in-memory pipes: any request before `initialize` settles as `plugin/not-initialized`; version chosen, none in common, and one not offered; identity mismatch; readiness only after the first listings; the derived protocol schema equals the committed one |
| `DH-T-EXT-4` | [DH-D-EXT-3](#dh-d-ext-3) | Enumerating the method table: every plugin-to-host method without its grant is `plugin/capability-denied`; with the grant but an unlisted tool or event type it is denied; a method with no table row cannot be dispatched; a grant wider than the request is refused at load |
| `DH-T-EXT-5` | [DH-D-EXT-3](#dh-d-ext-3) | An import-direction check and a run-time probe show that no plugin message can reach answering an interaction, creating a session grant, switching a preset or revealing a secret; a mapped credential appears only in the child's environment and is masked in logs |
| `DH-T-EXT-6` | [DH-D-EXT-4](#dh-d-ext-4) | With the fake plugin and a virtual clock: missed pings, an unanswered cancel, a full outbound queue, an over-long line, a crash and an ignored `shutdown` each end in the stop ladder; delays and the budget match the numbers; `closed` removes the set; `Restart` resets; an unconfirmed stop blocks a replacement; old and new processes never overlap |
| `DH-T-EXT-7` | [DH-D-EXT-4](#dh-d-ext-4) | On a crash, a sent call reports outcome unknown and an unsent one says so; nothing is sent twice; a nested call in flight is cancelled and logged as settled before the outer result; the host and a second plugin keep working |
| `DH-T-EXT-8` | [DH-D-EXT-5](#dh-d-ext-5) | Through the real loader: disabling the entry leaves no tool, feed listener, skill provider, fleet entry or process; enabling again works; editing the grants restarts only that entry with the smaller set |
| `DH-T-EXT-9` | [DH-D-EXT-5](#dh-d-ext-5) | A plugin tool is named with the prefix, receives only schema-valid arguments, is asked about under the default policy, is denied through the executor and has a bounded result; a name collision refuses the generation whole; `host/callTool` is logged under its parent, its approval is not covered by the outer one, and outside a served call it is `plugin/no-call-context` |
| `DH-T-EXT-10` | [DH-D-EXT-9](#dh-d-ext-9) | Only granted event types arrive, after commit and in order; a stalled plugin gets `events/gap` and the feed is not delayed; a record's owner segment is the plugin id whatever the plugin sent; an attached plugin sees no other session |
| `DH-T-EXT-11` | [DH-D-EXT-7](#dh-d-ext-7) | With a fake answerer: no answerer and `modelAuthored: off` each refuse before anything is copied; a yes appends `attached` before the start; a start failure appends `detached` and removes copy and state; agent disposal detaches; a resumed session has no plugin and gains `detached{not-restored}`; the cap and the forbidden capabilities are enforced |
| `DH-T-EXT-12` | [DH-D-EXT-8](#dh-d-ext-8) | The provider directory is a golden file; `Entries` pages and names the supplying layer and a preset-only entry; `Config` returns a schema and no values; unknown provider, method and target are distinct errors; the interface has no mutating method |
| `DH-T-EXT-13` | [DH-D-EXT-6](#dh-d-ext-6) | A `protocol: mcp` manifest yields a fleet entry with read-only confinement and the plugin id as its name; capabilities beyond the three allowed are refused; the entry leaves with the fiber |
| `DH-T-EXT-14` | [DH-D-EXT-4](#dh-d-ext-4) | The policy request is read-only with the state directory as the only root, on the host key; network is denied without `net.outbound`; where a fake backend reports an effect unenforceable the start is refused unless `acceptUnenforced` lists it; `requireFull` refuses partial |

### 9.2 Real-environment scenarios

<a id="dh-e-ext-1"></a>
#### DH-E-EXT-1 A real plugin from a local directory is inspected, granted, confined and limited to its grants

- **Proves.** [DH-D-EXT-1](#dh-d-ext-1) to [DH-D-EXT-5](#dh-d-ext-5); the scenario [DH-ADR-13](../design.md#dh-adr-13) names.
- **Given.** The built binary on Linux (bubblewrap, then Landlock), macOS and Windows; a fresh home; a plugin directory whose program is written in Python and asks for `tools.offer`, `tools.call` for one tool, and `session.records.write`; an owner at the command line; a model provider replayed from a recording.
- **When.** (1) Inspect the directory. (2) Approve it, granting everything except `session.records.write`. (3) Run the conformance suite against it. (4) Have the model call the plugin's tool, which makes its granted nested call, tries `host/appendRecord`, and tries to write a file outside its state directory. (5) Revoke `tools.call`. (6) Disable the entry.
- **Then.** (1) The report shows identity, digest, command, the three requests and the backend, and nothing has run. (3) The suite passes. (4) The tool result is in the log with the nested call under it; the record append is `plugin/capability-denied`; the outside write fails and the confinement reported matches the backend (on Windows the start is refused until the unenforced effects are accepted). (5) The plugin restarts and the nested call is now denied. (6) The tool is gone from the next request header and no process of the plugin remains.
- **Cleanup.** Remove the entry, `<home>/plugins` and the home. If a process survives, keep its id, the host log and the standard-error tail.

<a id="dh-e-ext-2"></a>
#### DH-E-EXT-2 A plugin that crashes, hangs or refuses to stop never takes the host with it

- **Proves.** [DH-D-EXT-4](#dh-d-ext-4).
- **Given.** The built binary on each system; two installed plugins built from the kit's reference program with fault switches; a session with a replayed model.
- **When.** (1) Kill the first plugin's process from the operating system during a slow call. (2) Make it stop reading its input. (3) Make it exit at every start. (4) Stop the host while the second plugin ignores `shutdown`. (5) On Linux and Windows, kill the host process outright.
- **Then.** (1) That call ends as outcome unknown, the turn continues, the second plugin answers, and the first restarts. (2) It is declared hung within the ping bound and restarted. (3) After ten attempts it is `closed` and its tools are removed as a set. (4) The host exits within the ladder's bounds and no plugin process remains. (5) No plugin process remains.
- **Cleanup.** Remove the home. On macOS record any survivor of step 5 as the stated limit, not as a pass.

<a id="dh-e-ext-3"></a>
#### DH-E-EXT-3 The model writes a plugin, an owner consents in a real browser, and a later edit does not run

- **Proves.** [DH-D-EXT-7](#dh-d-ext-7), [DH-D-EXT-1](#dh-d-ext-1).
- **Given.** The built binary; a workspace; a real browser on the web profile; a recorded model turn that writes a small plugin directory with the file tools and asks to attach it. The same recording run once more in the headless profile.
- **When.** (1) Run the turn; answer the consent in the browser. (2) Let the model call the new tool. (3) Let the model overwrite the plugin's program in the workspace and call the tool again. (4) End the session and resume it. (5) Run the headless copy.
- **Then.** (1) The consent shows the digest, command, capabilities and backend; after yes the log holds `plugin-host/attached`. (2) The tool answers. (3) The answer still comes from the approved bytes and the descriptor shows `SourceChanged`. (4) The plugin is gone and the log holds `detached{not-restored}`. (5) The attach is refused as `plugin/consent-unavailable` and nothing was copied or started.
- **Cleanup.** Remove the workspace and the home; confirm `<home>/plugins/attached` is empty before removal and keep it if not.

<a id="dh-e-ext-4"></a>
#### DH-E-EXT-4 An MCP server packaged as a plugin directory runs through the fleet under the plugin's grant

- **Proves.** [DH-D-EXT-6](#dh-d-ext-6).
- **Given.** The built binary; a real stdio MCP server program installed on the machine; a plugin directory with a `protocol: mcp` manifest for it.
- **When.** Approve it with `tools.offer` only; call one tool; have the server attempt a network connection; disable the entry.
- **Then.** The tool is listed as `mcp__<id>__<raw>` and answers; the connection fails where the backend governs the network; after disabling, the fleet has no such entry and no process remains.
- **Cleanup.** Remove the entry and the home.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Settled with other areas.** The codec is `sdk/jsonrpc` in module `src/sdk`; the protocol's own types stay in `internal/wire/plugin`. With boot ([boot.md](boot.md)): `plugin.Entry` is the config struct of an entry (`dir` is the approved copy; `digest`, `grants`, `credentials`, `pinned`; the trust level is `installed` for an entry and `model-authored` for an attach). `Add` has this area make the approved copy and then writes one entry; if the write fails the copy is left unreferenced and retention removes it. `dsh plugin remove --purge` asks this area to remove the approved copy and the state directory. The model's `plugin_manager` tool has `list`, `inspect`, `attach` and `detach` only, and attaches to its own session only. `requires.dsh` is checked with `version.ParseRange` (A23). `manifest.Report` carries the confinement facts as data (backend, state, each effect the backend cannot govern, the exposure list), so the plugins page and the consent prompt draw them without parsing text. Several hosts on one home share one state directory per plugin, so an entry holds a kernel lock on it (`fsx`, A08) and a second host shows the plugin as in use by another process and starts no second copy. The `consent` kind carries the inspection report as its display, is asked by this area through `approval.Service`, and has no selection, so consent stays all-or-nothing ([interaction-credentials.md](interaction-credentials.md) §6.2). `ForPlugin` takes the manifest's network setting, and the `Confinement` it leads to names every effect the backend cannot enforce before the first start ([sandbox-ssh.md](sandbox-ssh.md) §6.1); a plugin's network is denied by default and nothing forces `require: full` ([DH-ADR-EXT-10](#dh-adr-ext-10)). A plugin process is an untrusted tree: it cannot reach the host's control endpoint or its UI ([DH-ADR-HOST-12](host-telemetry.md#dh-adr-host-12)). A tool-only plugin is one entry of the MCP fleet, `scope: host`, `read-only` with one write root, and the fleet's supervisor loop is reused, not copied ([mcp-lsp-web-connector.md](mcp-lsp-web-connector.md)). A plugin's child uses the host key ([execution.md](execution.md)). The record capability is `session.records.write`, with the owner segment set by the host; the skill capability is `skills.provide`, with `skills/list`, `skills/read` and `skills/changed`; no version 1 capability registers a server entry, a vendor CLI or a skill-set download. The inventory reads `Descriptor` and `plugin.StateChanged`. A plugin can call tools only, never "a named host service" (rule 6 of [../design.md §6](../design.md#6-security-model)), and an attached plugin is owned by a child fiber of `plugin.host` bound to the agent, not by a loader entry.
2. **What `--safe` means here.** The launcher layer disables the whole `plugins` group ([DH-ADR-BOOT-12](boot.md#dh-adr-boot-12)), so no installed plugin process starts, and `Attach` is refused with `plugin/safe-mode`. Safe mode never weakens the sandbox, a grant or an approval.
3. **Not verified, because it needs a run.** Whether each backend can run a common interpreter (Python, Node) read-only with one write root; the cost of digesting and copying a directory with a large dependency tree, and so whether the 4,096-file and 64 MiB limits are right; whether an interpreter's own cache writes (for example byte-code files) fail harmlessly under a read-only copy.
4. **Limits kept.** A plugin cannot add UI, intercept, or provide a seam other than skills. Windows cannot confine reads or deny the network. A model-written plugin is approved per session. Inspection changes nothing (the one carried unit, a scope boundary of [DH-ADR-EXT-9](#dh-adr-ext-9) owned by stage `DH-S-59` of [../implementation-plan.md](../implementation-plan.md#dh-s-59)). No registry, download or update ([DH-ADR-13](../design.md#dh-adr-13)). Whether a public Go package for plugin authors is wanted is a later decision ([../design.md §8](../design.md#8-conflicts-and-gaps-between-the-foundation-recommendations), CF-13); the protocol schema and the kit are what an author gets now.

## 11. Units dispositioned by this spec

Ledger fragment: `trace/fragments/A22.jsonl`.

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 4 | 2 replaced, 1 merged, 1 dropped |
| `UP-DEBT` | 20 | 14 resolved, 5 not-applicable, 1 carried |
| `UP-TOOL` | 1 | 1 replaced (`harness_inspect`) |
| `UP-EVT` | 6 | 4 replaced, 2 dropped |
| `UP-CFG` | 1 | 1 replaced |
| `UP-SVC` | 2 | 2 replaced |
| `UP-DEC` (handed over) | 0 | none |
| Go items | 10 decisions, 9 deliverables, 14 tests, 4 scenarios | |
