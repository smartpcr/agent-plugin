# Profile bundles and agent presets: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". IDs in this document use the area key `BUNDLE`. Paths under
> `packages/` are relative to the upstream root.

| | |
|---|---|
| **Area key** | `BUNDLE` (agent A27) |
| **Upstream packages** | 9: six under `packages/bundle` (`base`, `web-app`, `headless`, `sdk-app`, `sdk-minimal`, `acp-app`) and three under `packages/preset` (`agent-preset-registry`, `agent-preset`, `persona`) |
| **Serves outcomes** | [DH-O-2](../design.md#dh-o-2) (the content of the layered composition), [DH-O-1](../design.md#dh-o-1), [DH-O-4](../design.md#dh-o-4), [DH-O-9](../design.md#dh-o-9), [DH-O-13](../design.md#dh-o-13), [DH-O-14](../design.md#dh-o-14), [DH-O-15](../design.md#dh-o-15) |
| **Depends on areas** | A21 ([boot.md](boot.md): profiles, the catalogue, the layer stack, the one edit path), A24 ([app-cli.md](app-cli.md): launchers, the headless runner), A01 ([core-session.md](core-session.md): scopes), A02 ([agent-loop.md](agent-loop.md): agent creation), A03 ([tools.md](tools.md): prompt and tool registries), A05 and A06 (event type, fold), A12 ([subagent-team.md](subagent-team.md)), A18, A20, and every area whose components the layers mount |
| **Priority** | P0: no profile starts without its layers, and no agent has a tool or a persona without a preset |

## 1. Purpose

Area A21 owns *how* a harness is composed from layers. This area owns *what the shipped
product is made of*: which components each profile mounts, with which defaults, and why;
and how one host runs agents with different tools and prompts. Without it there is a
loader and a catalogue of components but no product: `dsh web` would start an empty tree.
Two things are specified. **Bundles** are the shipped configuration, embedded in the
binary and stacked by the five profiles. **Presets** compose one agent's tools, prompt
sections and skills for one session, isolated from other agents. The area also states
what keeps the shipped configuration and the built binary in agreement. Upstream ships
its one-shot runner inside a bundle package; in Go that runner belongs to area A24, and
this area keeps only its layer.

## 2. Vocabulary

Framework terms (entry, layer, writable layer, realm, fiber) are defined in
[../design.md §2](../design.md#2-vocabulary); profile definition, catalogue, layer stack
and optional bundle in [boot.md §2](boot.md).

| Term | What it is | Why it exists |
|---|---|---|
| **Layer file** | One embedded YAML file of layer operations (`insert`, override, `merge`, `remove`). Its path is its label in the ownership index. | A config dump can say which shipped file supplied a value. |
| **Bundle** | A named, ordered list of layer files ([DH-ADR-BOOT-1](boot.md#dh-adr-boot-1)). A file may belong to two bundles. | Profiles share content without copying it. |
| **Carrier entry** | The entry that makes a profile reachable: the listener, the headless runner, the SDK or ACP server. | A profile without it starts and serves nobody. |
| **Host plane** | Entries mounted once per process: stores, registries, carriers, policy. | State that is per process must have one owner. |
| **Agent plane** | What contributes to one agent: its tools, prompt sections, skill sources, per-agent services. | Two sessions in one host can differ. |
| **Agent preset, declaration** | An agent preset is a named agent-plane composition: one agent's tools, prompt sections and skills. Its declaration is the entry that states its id, display data and children. In this document "preset" alone always means an agent preset. | The agent plane is configuration like everything else. |
| **Permission preset** | Area A17's term for a named pair of sandbox mode and approval policy ([interaction-credentials.md](interaction-credentials.md), DH-D-HUMAN-4). It is a different thing with a different owner. On every surface a user or a program sees (a flag, a wire field, a route, a label) the two are written out as "agent preset" and "permission preset" (`--agent-preset`, `agentPreset`; `--permission-mode`, `permissionMode`), never as a bare "preset". | Two unrelated choices were both called "preset"; a person who picks one must not think they picked the other. |
| **Child** | One component of a preset, held in the declaration's config under a stable key. | A layer can change one child without restating the others. |
| **Revision** | One mounted instance of a declaration: a scope, a private loader tree and a private realm. An edit creates a new revision and retires the old one. | A preset can be edited while agents that use it keep running. |
| **Lease** | A counted reference to a revision, released by its holder's fiber. | A retired revision is disposed exactly when nobody uses it. |
| **Digest** | A hash of a revision's resolved children and the harness version. | The session log can show that a session's composition changed. |
| **Blank session** | A session whose log holds no `turn/start`. | The only state in which a preset may be changed. |
| **Optional list** | The embedded list of catalogue components that no shipped layer mounts, each with an owner and a reason. | "Compiled in but unused" is a decision, not an accident. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.bundle.base` | `@deepseek-ai/dsh-base` | other (config) | One `insert` of 101 rows over an empty root: the shared core of `web`, `headless`, `sdk` and `acp` (`packages/bundle/base/cordis.patch.yml:15`). No code (`src/index.ts` is 9 lines). |
| `UP-PKG-packages.bundle.web-app` | `@deepseek-ai/dsh-web-app` | other (config) and provider | The browser layer: 102 inserted rows, 24 overrides, four preset files (`package.json:49-58`), a start-up provider for the web flags and a glue plugin that prints the URL and opens the browser (`src/index.ts:1-12`). |
| `UP-PKG-packages.bundle.headless` | `@deepseek-ai/dsh-headless` | carrier | A 46-line layer plus the one-shot runner, its flag parser and its JSON stream (`src/index.ts`, `src/startup.ts`, `src/json-stream.ts`). |
| `UP-PKG-packages.bundle.sdk-app` | `@deepseek-ai/dsh-sdk-app` | other (config) and provider | Mounts the SDK JSON-RPC server over base; the provider accepts the command line and ends the process when standard input closes (`src/index.ts:55-62`). |
| `UP-PKG-packages.bundle.acp-app` | `@deepseek-ai/dsh-acp-app` | other (config) and provider | The same for the ACP server (`src/index.ts:103-110`). |
| `UP-PKG-packages.bundle.sdk-minimal` | `@deepseek-ai/dsh-sdk-minimal` | other (config) | A complete 37-row tree that does not use base (`cordis.patch.yml:1-3`). |
| `UP-PKG-packages.preset.agent-preset-registry` | `@deepseek-ai/dsh-agent-preset-registry` | definition and provider | `ctx.agentPresets`: declarations, mounted revisions, agent bindings, selection, the session record (`src/index.ts:53-413`). |
| `UP-PKG-packages.preset.agent-preset` | `@deepseek-ai/dsh-agent-preset` | consumer | A row whose config is one declaration; it registers it and returns the disposer (`src/index.ts:12-29`). Also ships the `dsh-doc` skill directory. |
| `UP-PKG-packages.preset.persona` | `@deepseek-ai/dsh-persona` | consumer | Registers a persona prefix and suffix for one scope and can suppress optional runtime context (`src/index.ts:62-74`). |

### 3.2 Behaviour

**Profiles and bundles.** A profile is a directory in the harness home whose `package.json`
lists bundles; a bundle is an npm package whose manifest names one or more patch files.
The tree is composed over an empty root: each bundle's patches in order, then the
profile's own patch, the home patch and `--patch` overlays (`docs/architecture.md:17-25`).
Only bundles listed directly contribute; nothing is applied transitively
(`.agents/notes/implemented/architecture/2026-08-05-profile-plugin-bundles.md:26`). A
patch replaces a row's whole `config`, so a row whose value differs by mode is restated in
every mode bundle (`packages/bundle/base/cordis.patch.yml:6-10`). Row order carries no
load meaning (`:12-13`).

| Bundle | Builds on | What it mounts and its notable settings |
|---|---|---|
| `base` | nothing | Model runtime and adapters with default `deepseek-official` / `deepseek-flash` (`:90-94`); session store, projections, storage; sandbox with mode from `DSH_PERMISSION_MODE`, default `workspace-write`, approval `ask` (`:249-268`); one shell stack per operating system (`:255-263`, `:287-293`); file, search, job, skill, goal, todo, web, subagent, team and workflow tools; plan mode with a 13-line prompt (`:347-361`); settings, credentials, account; session telemetry in mode `FEEDBACK_ONLY` to a vendor collector (`:219-233`) and the vendor session-log row (`:43-44`); the programmatic-tool-calling runtime, its executor disabled (`:418-419`, `:533-535`); config reload on, plugin manager present only under a profile (`:16-32`). |
| `web-app` | `base` | Web server from the start-up flags, default `127.0.0.1:3080` (`packages/bundle/web-app/cordis.patch.yml:188-197`); gateway, controllers and 65 rows naming client packages; schedule, workspace, MCP fleet; desktop-only analytics rows (`:37-54`). It then **disables 22 base rows** (`:526-623`) and mounts the preset registry with default `standard` (`:628-632`): the agent plane moves into presets. |
| `headless` | `base` | The start-up provider and the runner (`packages/bundle/headless/cordis.patch.yml:14-25`); restates the sandbox policy to add `--add-dir` roots (`:28-33`); disables reload and the credential store, authorization and account rows (`:35-46`). |
| `sdk-app` | `base` | The SDK server (`packages/bundle/sdk-app/cordis.patch.yml:11-21`); disables the title model call and reload (`:8-9`, `:23-24`); adds office, schedule, time, ask-user and present rows (`:35-87`). |
| `acp-app` | `base` | The ACP bridge with its own model default (`packages/bundle/acp-app/cordis.patch.yml:15-20`); disables the title model call and reload. |
| `sdk-minimal` | nothing | The SDK server, one adapter keyed by `DEEPSEEK_API_KEY`, `read_image` as the only file tool (`packages/bundle/sdk-minimal/cordis.patch.yml:47-53`), a persistent shell, compaction, sandbox mode `danger-full-access` (`:70-74`), a persona from `DSH_SYSTEM_PROMPT` (`:113-118`), the vendor session-log row (`:20-21`). No settings, credential store, policy presets or web tools (`README.md:97`). |

**Two planes, written twice.** `base` mounts the model-facing rows process-wide for the
single-session surfaces. `web-app` disables them and declares them again inside each
preset, "disabling rather than deleting" so that a reordered composition cannot bring
them back (`packages/bundle/web-app/cordis.patch.yml:509-518`). The plan-mode prompt
therefore exists in four files. Services whose state is per process (jobs, goals, the
token meter, the skill and subagent registries) stay in the host plane (`:535-599`).

**Presets.** A declaration row holds `id`, display fields and `plugins`, a child entry list
(`packages/preset/agent-preset/src/index.ts:16-23`). The registry activates each
declaration eagerly: it creates a scope it owns, mounts the children in an in-memory
loader tree whose commits are not persisted, and audits the result
(`packages/preset/agent-preset-registry/src/index.ts:118-137`, `src/mount.ts:9-24`, `:204-216`).
A row that fails, or a service that leaks into the root realm, rejects the mount; the
declaration stays listed as broken and the application still starts (`src/index.ts:132-136`).
A preset that provides a service must put provider and consumers in one group with an
`isolate` realm (`packages/bundle/web-app/presets/standard.patch.yml:51-55`). An agent
joins a revision by making its scope a child of the revision's scope (`src/index.ts:280-289`);
a child agent joins its parent's exact revision (`:312-326`). Updating a declaration
retires the old revision, which is disposed when its last user leaves (`:103-112`,
`:163-167`). `select` changes a session's preset only before its first turn and appends
`agent-preset/selected` (`:363-382`); the `agentPreset` projection is the header value
advanced by those events (`src/session.ts:37-46`). After a restart the recorded id is
resolved against the current definition, and a missing one is refused (`README.md:60`).
Upstream ships four presets: `standard`, `ptc` (the programmatic executor in place of the
inspection tool), `minimal` (a complete persona, `read_image`, a persistent shell,
compaction) and `cordis`, shown as "Creator" (a persona for customising the harness).

**The headless runner** waits for the tree, validates the permission preset and extra
roots, reads the task from an argument or standard input, creates an agent or adopts the
session `--session-id` names, sends one user message and waits until the agent and its
children are quiet (`packages/bundle/headless/src/index.ts:320-420`). It prints the final
answer, or with `--json` a bounded projection of the run (`src/json-stream.ts:16-19`), and
exits 0 only for a completed turn (`src/index.ts:432-440`). Adoption refuses another
working directory, a subagent session and any session recorded under a preset (`:219-240`).

### 3.3 Failure, retry, rollback and cleanup upstream

| Situation | What upstream does | What is left |
|---|---|---|
| A bundle cannot be resolved or parsed | It is skipped and reported; the rest composes (`packages/boot/app-boot/README.md:52`) | A tree that may lack required services |
| A preset row fails or leaks a service | The revision's scope is disposed; the declaration is listed as broken; new bindings are refused (`src/index.ts:132-136`, `:256-259`) | Running agents keep their old revision |
| A preset row waits for a host service | It stays mounted and is audited again on every read (`src/index.ts:150-161`) | Nothing |
| An agent is created without joining a preset | No error: it sees the empty global layer and "cannot act" (`.agents/notes/implemented/architecture/2026-08-10-host-plane-ownership-after-presets.md:17`) | A silent, useless agent |
| The process restarts | Revisions are process-local; the recorded id binds the current definition (`README.md:99`) | An unrecorded change of composition |
| Headless: the process ends while a job runs | The job's settlement may be missing from the log (`packages/bundle/headless/README.md:184`) | An unsettled interval |

## 4. Upstream decisions dissected

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `UP-DEC-implemented.architecture.2026-08-05-profile-plugin-bundles` | Every launch mode is a profile of ordered npm bundles plus user patches; one composition function for boot and dump | change | Ordered explicit layers, no transitive application and one function are kept. A bundle cannot be an npm package: code is compiled in ([DH-ADR-4](../design.md#dh-adr-4)) | [ADR-1](#dh-adr-bundle-1) |
| `UP-DEC-implemented.architecture.2026-08-10-host-plane-ownership-after-presets` | Per-process state stays in the host; entry points join agents to presets explicitly, with no check | keep the rule, change the join | The ownership rule is right and becomes a check. The unchecked join is the cause of the silent agent | [ADR-3](#dh-adr-bundle-3) |
| `UP-DEC-implemented.architecture.2026-09-18-declarative-agent-presets` | A preset is loader configuration; the registry owns revisions; failures stay listed | keep, with two changes | Whole-list override becomes a keyed child map that layers merge into; hand-written realms become automatic | [ADR-4](#dh-adr-bundle-4), [ADR-5](#dh-adr-bundle-5), [ADR-6](#dh-adr-bundle-6) |
| `UP-DEC-implemented.architecture.2026-10-12-headless-environment-auth` | A headless job reads keys only from its environment | keep | A job sharing a home must not run on the interactive user's saved account | [ADR-9](#dh-adr-bundle-9) |
| Code: mode bundles restate whole config (`base/cordis.patch.yml:6-10`) | Follows from whole-config replacement | drop | `merge` changes one field ([DH-ADR-LOADER-4](../libs/cordis-loader.md#dh-adr-loader-4)) | [ADR-1](#dh-adr-bundle-1) |
| Code: the agent plane is in `base` and again in presets (`web-app/cordis.patch.yml:509-518`) | Single-session surfaces predate presets | change | Two copies drift; one plane serves every profile | [ADR-3](#dh-adr-bundle-3) |
| Code: `sdk-minimal` is a second complete tree | An explicit kernel with nothing optional | change | The same kernel is the first layer of every profile | [ADR-2](#dh-adr-bundle-2) |
| Code: platform and environment switches as `!!js` (16 in base) | One file serves three systems | keep as `!expr` | [DH-ADR-LOADER-3](../libs/cordis-loader.md#dh-adr-loader-3), [DH-ADR-BOOT-5](boot.md#dh-adr-boot-5) | [ADR-8](#dh-adr-bundle-8) |
| Code: protocol profiles and headless disable reload | One connection must not see its server replaced | keep | The reload entry is mounted by `web` only | [ADR-1](#dh-adr-bundle-1) |
| Code: eager activation; a broken preset does not stop start-up | Diagnostics before first use; repairable in the UI | keep | Same reasons | [ADR-4](#dh-adr-bundle-4) |
| Code: leak audit after mounting (`src/mount.ts:69-83`) | A preset service must not replace a host service | change | The registry builds the realm itself, so a leak cannot be written | [ADR-5](#dh-adr-bundle-5) |
| Code: version admission of preset rows (`src/mount.ts:196-198`) | Third-party packages may not match the runtime | drop | No package is loaded into the host ([DH-ADR-4](../design.md#dh-adr-4)) | [ADR-4](#dh-adr-bundle-4) |
| Code: select only while blank, logged, idempotent (`src/index.ts:363-382`) | The preset decides what the model sees | keep, add a digest | [DH-O-4](../design.md#dh-o-4) | [ADR-6](#dh-adr-bundle-6) |
| Code: a child joins the parent's exact revision (`src/index.ts:312-326`) | Parent and child agree on tools | keep | Same | [ADR-7](#dh-adr-bundle-7) |
| Code: persona is scope-only (`persona/src/index.ts:4-8`) | The global persona has one owner | keep | One owner per prompt fact ([tools.md](tools.md), `DH-ADR-TOOLS-8`) | [ADR-4](#dh-adr-bundle-4) |
| Code: the runner, its flags and its capped JSON stream live in a bundle package | A bundle carries the code its rows mount | change | A carrier is code and a bundle is configuration; the runner is A24's and its stream is the log ([app-cli.md](app-cli.md) §6.2.2) | [ADR-9](#dh-adr-bundle-9) |
| Code: headless adoption refuses any session recorded under a preset (`src/index.ts:226-233`) | The headless bundle composes no preset | change | Every profile composes presets, so the recorded one is bound | [ADR-3](#dh-adr-bundle-3) |

## 5. Tech debt and its resolution

Forty units, grouped by cause. Each unit's own reason is in its ledger row.

| Group (count) | Units | How the Go design answers it | Result |
|---|---|---|---|
| **Whole-config replacement** (3) | `base.1`, `base.2`, `agent-preset-registry.1` | `merge` changes one field of a row or one child of a preset ([ADR-1](#dh-adr-bundle-1), [ADR-4](#dh-adr-bundle-4)). The registry unit also says a preset is no sandbox and that old revisions do not survive a restart; the last clause stays | 2 resolved, 1 carried |
| **A profile can be composed into a useless or noisy product** (6) | `acp-app.1`, `sdk-app.5`, `acp-app.2`, `sdk-app.6`, `sdk-minimal.4`, `base.4` | A custom profile extends a built-in one and cannot drop its carrier's bundle ([DH-ADR-BOOT-1](boot.md#dh-adr-boot-1)); carriers are required entries. Standard output is claimed once by A20's `wire.stdio` and no third-party code runs in the host. The file fence is not a second provider (A08), and two providers of one service are a static issue | 6 resolved |
| **Configuration read once on stdio profiles** (2) | `acp-app.3`, `sdk-app.8` | Kept: these profiles do not mount the reload entry, because a restarted spine entry would need standard input and output a second time ([ADR-1](#dh-adr-bundle-1)) | 2 carried |
| **Node, npm and bundler facts** (2) | `web-app.1`, `headless.5` | Assets are embedded; there is one binary and one way to start it | 2 not applicable |
| **Headless start-up and policy wiring** (3) | `headless.2`, `headless.3`, `headless.4` | No start-up row exists to disable; A13 appends a job's settlement before the final flush; A10 applies extra roots to processes on Windows too ([ADR-9](#dh-adr-bundle-9)) | 3 resolved |
| **Headless output** (4) | `headless.10`, `headless.6`, `headless.7`, `headless.8` | A24's `--json` is the session's events in the export format, uncut. A24 now removes two more (debt entry U-K2-4, [app-cli.md](app-cli.md) `DH-ADR-CLI-3`): a progress line is written at the start of the run and of each step, and reasoning goes to the diagnostic stream only when it is a terminal or `--reasoning show` is given. The result stream carries only the answer, which is kept | 1 resolved in the ledger; `headless.6` and `.7` resolved since, their rows re-issued by the debt reviewers; 1 carried |
| **Definition of a one-shot or embedded product** (6) | `headless.1`, `headless.9`, `sdk-app.1`, `sdk-app.4`, `sdk-app.7`, `sdk-minimal.1` | Kept on purpose, each narrowed: adoption no longer refuses a preset; a web host on the same home shows SDK sessions ([DH-ADR-12](../design.md#dh-adr-12)); the minimal profile is a subset | 6 carried |
| **Protocol or model limits a layer cannot remove** (3) | `sdk-app.2`, `sdk-app.3`, `sdk-minimal.2` | The time zone is now a protocol field (debt entry U-K2-5: `timeZone` on `initialize` and `session/create`, [sdk-acp-typert.md §6.2](sdk-acp-typert.md)); a question still needs an answerer; `read_image` on a text-only route stays a tool error | `sdk-app.2` resolved since, its row re-issued by the debt reviewers; 2 carried |
| **Slash commands unreachable from the SDK** (1) | `sdk-minimal.3` | The command registry is in `core` and the `minimal` preset mounts the compact command; the SDK runs commands through its query method | 1 resolved |
| **Listener and browser hand-off** (6) | `web-app.2` to `web-app.7` | Owned by A18 ([host-telemetry.md](host-telemetry.md)); the `web` layer only sets defaults. Plain HTTP on a network address is refused by default and certificates are re-read. No certificate management, no observation of the browser, no hand-off over SSH, `BROWSER` only from the environment and no wildcard bind are kept | 1 resolved, 5 carried |
| **Sandbox behaviour documented in a bundle** (1) | `base.3` | A10's contract; the layer selects only the mode | 1 carried |
| **Preset and persona limits** (3) | `agent-preset.1`, `persona.2`, `persona.1` | Duplicate ids and a missing registry are static issues; named contexts can be suppressed. A persona still cannot be mounted globally | 2 resolved, 1 carried |

Totals: 16 resolved, 2 not applicable, 22 carried. Inference: the carried share is high
because most of these bullets state what a product is (one task per run, no graphical
client, no wildcard bind) and not a defect; each row says what was narrowed.

## 6. Go design

### 6.1 Shape

Area A21 owns the mechanism this area fills: profile definitions and embedded bundle
files in `internal/boot/profile`, the catalogue of components (`catalog.Item` with
`Provides` and `Platforms`), the layer stack, the static check and the one edit path
`config.Service` ([boot.md §6.1](boot.md)). This area adds two packages and the content.

| Package (under `src/harness/internal/boot`) | Component | Provides | Injects | Why it exists |
|---|---|---|---|---|
| `profile` (A21's) | none | This area's content: the bundle files, each profile's bundle list, the optional list, and the tests of [D-6](#dh-d-bundle-6) | nothing | What the product is made of |
| `agentpreset` | `agent.presets` (registry), `agent.preset` (declaration) | `agentpreset.Key` (`Service`); the durable type `agent-preset/selected`; the `agentPreset` fold; notify key `agentpreset.Selected` | `scope.TableKey` (A01), `projection.Key` (A06), the loader and the catalogue (A21); fills the composer seat (request to A02) | Declarations, revisions, leases, selection |
| `agentpreset/persona` | `agent.persona` | nothing | `prompt.Key` (A03) | A preset changes an agent's identity, not only its tools |

Inference: `agentpreset` sits in the `internal/boot` family because a preset is
composition, and is named so to avoid A17's `human/preset` (permission presets).

```go
// package agentpreset
type Service interface {
    List() []Info                    // every declaration in roster order; Broken is set when unusable
    Resolve(id string) (Info, error) // "" means the default; starts nothing
    Default() string                 // the saved choice, else Config.Default
    Acquire(owner *cordis.Context, id string) (Lease, error)            // the current revision
    AcquireFrom(owner *cordis.Context, holder scope.Key) (Lease, error) // the exact revision a live agent holds
    Of(agent scope.Key) (Info, bool)
    Select(ctx context.Context, a agent.Agent, id string) (Info, error) // blank sessions only
    Lookup(agent scope.Key, service string) (any, bool)                 // a service a preset provides privately
    Inventory() []Composition                                           // children, their states and each child's supplying layer, for A18's inventory and C5's viewer
}
type Lease interface{ Key() scope.Key; Info() Info; Release() }
type Info struct{ ID, Name, Description string; Order int; Digest, Broken string }
```

**A declaration** is an ordinary top-level entry of component `agent.preset`. Its config
is `Declaration{ID, Name, Description, Order, Extends, Children map[string]Child}` with
`Child{Component string; Disabled bool; Config any}`. Children are a map, not a list, so
the existing `merge` operation reaches one child or one field of it, and a null removes
a child. `Extends` names another declaration whose resolved children are merged under
this one's. The declaration's validator resolves every child's component in the
catalogue and decodes its config strictly, so a bad child is a static issue of A21's
check ([DH-ADR-BOOT-7](boot.md#dh-adr-boot-7)) and rejects the edit before anything
changes. A child's config may use `os`, `env`, `launch` and `home` expressions, not
`use`.

**A revision.** For each declaration the registry creates one `scope.Scope`
([core-session.md](core-session.md) §6.1) owned by the registry's fiber, derives from
its context one realm that is private for every service the children's catalogue items
say they provide, and mounts the children there with a private loader instance
([DH-D-LOADER-10](../libs/cordis-loader.md#dh-d-loader-10)) that has no writable layer.
Children register tools, prompt sections, commands and skill sources through that
context, so they are filed under the revision's scope key. An agent whose scope lists the
key as an enclosing scope sees them; no other agent does.

### 6.2 Contracts

**Profiles.** The five built-in definitions of [DH-ADR-BOOT-1](boot.md#dh-adr-boot-1);
this table is their content. The layer order above the bundles is A21's: profile layer
(the one writable layer, `<home>/config/profiles/<profile>/layer.yml`), home layer,
overlays, launcher layer.

| Profile | Bundles, in order | Carrier entry | Reload entry | Default preset |
|---|---|---|---|---|
| `web` | `core`, `base`, `presets`, `web` | `host.listen` | mounted | `standard` |
| `headless` | `core`, `base`, `presets`, `headless` | `cli.headless` | absent | `standard` |
| `sdk` | `core`, `base`, `presets`, `sdk` | `wire.sdk` | absent | `standard` |
| `acp` | `core`, `base`, `presets`, `acp` | `wire.acp` | absent | `standard` |
| `sdk-minimal` | `core`, `minimal`, `sdk` | `wire.sdk` | absent | `minimal` |

`sdk-minimal` is the name of a profile only; the bundle it adds is `minimal`. The
`admin` bundle alone is the composition A24's admin commands run in
([app-cli.md](app-cli.md), `DH-ADR-CLI-2`); it is not a launchable profile and has no
required entry.

**Required entries.** The rule is A21's ([boot.md §6.2](boot.md)): each profile
definition has a `Required` list, every id on it must be present, enabled and active,
and no layer may remove or disable one. The ids are this area's. `profile.Spine`, in
every launchable profile: `session.store`, `agent.registry`, `agent.driver`,
`tools.registry` and `agent.presets`. The carrier set, per profile: `web` adds `host.listen`,
`host.auth`, `host.assets`, `host.web`, `host.access`, `host.stream` and `ui.shell`
(the set [app-web.md §6.1](app-web.md) needs); `headless` adds `cli.headless`; `sdk`
and `sdk-minimal` add `wire.stdio` and `wire.sdk`; `acp` adds `wire.stdio` and
`wire.acp`.

**Layers.** A component is named by its owner's catalogue name, and the catalogue
accepts only names of the form A21 fixes ([boot.md](boot.md), `DH-ADR-BOOT-13`), so the
layer files are written from the catalogue; the files, checked by `DH-T-BOOT-2` and
[D-6](#dh-d-bundle-6), are the authority.

| Go bundle (files) | Replaces upstream | Contains | Deliberate differences from upstream's default |
|---|---|---|---|
| `admin` (`admin.yml`) | nothing: upstream's offline commands read files directly | What an offline command needs and nothing that acts: logging, `session.store`, `storage.kv`, `storage.blob`, `session.projection`, `session.query`, `human.secrets`, `boot.config`, `sandbox.local`, `host.sharing` | No carrier, agent, model adapter or plugin process, as A24 requires |
| `core` (`admin.yml`, `core.yml`) | the kernel that `base` and `sdk-minimal` both spell out | Host plane only, no model-facing entry. `core.yml` adds: timer, `session.scope` (top level, as [boot.md §6.2](boot.md) places it), `session.core`, `session.checkpoint`, `session.views`, `session.workdir`, attachments, spill store and policy, `agent.registry`, `agent.driver` with no configured agent, `agent.default-model`, `tools.registry`, `tools.prompt`, `llm.runtime`, `llm.retry`, `llm.meter`, `llm.routes`, `llm.deepseek.api-key`, `human.interactions`, `human.approval`, `human.commands`, `fs.local`, `fs.ops`, `subprocess.local`, `sandbox.policy`, `jobs.registry`, `agent.presets`, `host.conversation` (A19's carrier-neutral commands and the schedule activator) | SQLite store in place of JSONL files and JSON key-value files; no executor rows (one executor); the sharing gate is in the kernel, so no later layer can send before it exists |
| `base` (`base.yml`, plus one optional bundle per experimental feature) | the rest of `base` | Product host plane: settings forms, workspace registry, `session.title` and `session.title.llm`, secret keychain, `human.authorization`, `human.identity`, `human.feedback`, `permission.presets`, `human.questions`, `subagent.manager` with `subagent.spawn` and `subagent.fork`, `team.board`, `schedule.service`, `goal.service` and `goal.rounds`, `mcp.fleet`, `lsp.service`, `web.service` and `web.fetch.http`, `skill.registry`, `skill.office`, `office.service` and `office.libreoffice`, `terminal.service` and both shells, `shellexec.env`, `tools.endpoint`, `tools.repeat-guard`, `compact.imageoffload`, `boot.plugins` and its `plugins` group, `plugin.host`, `host.inspect`, `host.otlplog`, `host.report`, `host.inventory`. `title.all-prompts` (area X6). As `disabled: true` entries: `session.logupload`, `session.upload`, the vendor account, and, only in a build made with the cloud tag, the cloud sign-in providers (`llm.bedrock`, `llm.vertex`, `llm.azure-entra`, `llm.oauth`; the default binary does not link them, a recommended default not confirmed by the owner). The optional bundles are the groups `x.worktree`, `x.forge`, `x.devtools`, `x.timeline`, `x.session-recall`, `x.chat-import`, `x.feature-guide` and `x.reasoning-translation`; the last ships without the `translate.web` entry, which is specified and not in the shipped bundle ([DH-ADR-36](../design.md#dh-adr-36)) | No telemetry URL and no mode: the exporter is mounted and the gate is shut ([DH-ADR-9](../design.md#dh-adr-9)). Vendor log upload is a disabled entry. No market or package-inventory rows ([DH-ADR-13](../design.md#dh-adr-13)). No model-facing row at all |
| `presets` (`presets/standard.yml`, `script.yml`, `minimal.yml`, `creator.yml`) | the four preset files of `web-app` and the 22 agent-plane rows of `base` | One declaration per file; see "Shipped presets" | Used by every profile, not only web |
| `web` (`web.yml`) | `web-app` | `boot.reload`, `host.listen`, `host.auth`, `host.assets`, `host.launch`, `host.web`, `host.access`, `host.stream`, the route components, `host.userterm`, `host.wsfiles`, `dirbrowse.local`, `host.openpath`, `deliver.changes`, `context.fileref`, `context.sessionref`, `update.check` (off), the page components of areas C1 to C7 | No client roster, gateway or remote-procedure rows ([DH-ADR-3](../design.md#dh-adr-3)). No desktop analytics, embedded browser pane or bundled Python runtime ([DH-ADR-18](../design.md#dh-adr-18)). The glue plugin is A18's `host.launch`. Nothing to disable |
| `headless` (`headless.yml`) | the layer of `headless` | `cli.headless` (A24's runner); `human.secrets` limited to environment sources; `human.authorization` disabled; `tool.ask-user` removed from each preset | One field merged where upstream restates a row. The runner is not in this area |
| `sdk` (`sdk.yml`) | `sdk-app` | `wire.stdio`, `wire.sdk`; `session.title.llm` disabled | No office-kit, runtime-download or client rows: office tools are in the preset and work only where LibreOffice is installed ([DH-ADR-20](../design.md#dh-adr-20)) |
| `acp` (`acp.yml`) | `acp-app` | `wire.stdio`, `wire.acp`; `session.title.llm` disabled; `tool.ask-user`, `plan.mode` and the goal children removed from `standard` | No second model default: the one in `core` applies. The removals follow what ACP cannot express ([sdk-acp-typert.md](sdk-acp-typert.md) §6.2) |
| `minimal` (`presets/minimal.yml`, `minimal.yml`) | `sdk-minimal` | The `minimal` declaration; registry default `minimal`; prompt identity and optional runtime context off; persona from `env.DSH_SYSTEM_PROMPT` | Sandbox mode stays `workspace-write`: a disposable container opts into `danger-full-access` through `DSH_PERMISSION_MODE` or the SDK's per-session mode. No vendor log row |

**Shipped presets.**

| Preset | Upstream | Children | Priority |
|---|---|---|---|
| `standard` | `standard` | `agent.persona` ("You are a coding agent powered by the {{model}} model."), `context.instructions`, `context.clock`, the shell tool of the operating system, `fs.tools` (with `apply_patch` on, as the owner decided in [DH-ADR-35](../design.md#dh-adr-35)), the search tools, `tool.jobs`, `tool.schedule`, `skill.filesystem`, `skill.tool`, `command.goal`, `tool.goal`, `plan.mode`, `compact.basic`, `compact.command`, `compact.prune`, `subagent.tools` (`interrupt: false`), `tool.subagent@spawn` and `tool.subagent@fork`, `workflow.engine`, `tool.workflow`, `tool.ask-user`, `tool.todo`, `tool.web`, `deliver.present`, `office.tools` | P0 |
| `script` | `ptc` | Extends `standard`; adds `program.mode` (script-calling mode, [ptc-workflow.md](ptc-workflow.md)). The workflow tool stays, because it no longer needs a guest runtime | P1 |
| `minimal` | `minimal` | `agent.persona` (complete, no optional context), `fs.tools` with only `read_image`, the terminal host with the persistent shell tool of the operating system, `compact.basic`, `compact.command` | P1 |
| `creator` | `cordis` | Extends `standard`; adds a second persona sentence, `boot.plugins.tool` (A21's `plugin_manager`) and an embedded `harness-docs` skill | P2 |

Computer use is in no preset: its only provider is an MCP server the operator installs
([DH-ADR-19](../design.md#dh-adr-19)), so `computer.service` and `computer.tools` are on the
optional list with the per-host, per-server and per-connector components. So are the
external subagent providers of area A12, `subagent.sdk-child`, `subagent.acp`,
`subagent.claude-code` and `subagent.codex` ([subagent-team.md §6.1](subagent-team.md)):
each is mounted once per instance an operator configures (`@<instance>` for a further
one), so no shipped layer mounts one; `base` mounts only the in-process providers
`subagent.spawn` and `subagent.fork`. The `minimal`
preset is in `presets` as well as in `minimal`, so `dsh sdk` with agent preset `minimal` gives
the small capability set on the full host plane, and `dsh sdk-minimal` gives it on the
kernel-only host plane.

**Precedence inside a preset** has two parts, and they answer different questions.
*What the declaration contains* is decided at composition: a child is the config path
`children.<key>` of its declaration entry, and any later layer may `merge` into it, set
a field or delete it with a null, exactly as for any other config field. An edit from
the UI or a tool is `config.Service.Apply` with a `Merge` or `Reset` on that path; it is
written to the profile layer, and refused with the layer's name when the home layer or an
overlay shadows it. A user's edit therefore touches one child, and a later release that
adds a child to the shipped map still reaches that user. *What an agent sees* is decided
at each step by scope: the agent's own registrations, then its revision's, then the
global layer, the nearest winning a name ([core-session.md](core-session.md) §2). A
preset can add tools, sections and listeners. A child that would write to a shared
registry is refused, so a preset cannot replace the sandbox policy, the approval service
or a guard.

**What a subagent inherits.** A child that names no preset holds a lease on its
creator's exact revision, so both see the same tools even after an edit. A child that
names one (the `spawn` provider only, [subagent-team.md](subagent-team.md)
`DH-ADR-SUBAGENT-10`) gets that preset's current revision. The id is the child header's
`Preset`. A child inherits nothing from its creator's own scope, and area A12 decides
which inherited tools are denied to it. On resume a child binds the current revision of
its recorded id, independently of its creator.

**Events, config and services.**

| Upstream unit | Go replacement | Form or fields |
|---|---|---|
| `UP-EVT-agent-preset/selected` | Notify key `agentpreset.Selected`, a `scope.Event` dispatched with the session's scope key after the durable event commits | `{SessionID, Preset, Digest, Cause}`; consumers drop state derived from the composition |
| durable `agent-preset/selected` ([session-store.md](session-store.md) lists it as kept) | Declared by `agentpreset`, version 1, log-only, not ignorable | `{preset, digest, cause}` with cause `create`, `select`, `resume`, `inherit` or `rebind`; the whole new value |
| `UP-CFG-dsh-agent-preset-registry` | `agentpreset.Config` | `Default` (required; an unknown id is a static issue); `SelectedDefault` (live, saved through `config.Service`) |
| `UP-CFG-dsh-agent-preset` | `agentpreset.Declaration` | `ID` (required, unique), `Name`, `Description`, `Order`, `Extends`, `Children` |
| `UP-CFG-dsh-persona` | `persona.Config` | `Prefix` (required), `Suffix` (empty), `Complete` (false), `IncludeRuntimeContext` (true), `SuppressContexts` (names, checked at load) |
| `UP-CFG-dsh-headless` | none | Task, session, output mode and permission choices are app words that A24's `cli.headless` parses; no config struct |
| `UP-CFG-dsh-sdk-app` | none | Its one field named the profile in help text; the name is `launch.profile` |
| `UP-CFG-dsh-web-app` | A18's launch and listener config | `openBrowser`, `printUrl` and `publicUrl` are theirs; `surfaceContext` becomes the `DSH_WEB_URL` variable registered with `shell.env` (request to A18) |
| `UP-SVC-agentPresets` | `agentpreset.Key`, interface `Service` | §6.1 |
| `UP-SVC-headlessStartup` | none | The parsed words stay inside A24's runner. No shipped layer reads them, so the runner declares no expression view ([DH-ADR-BOOT-5](boot.md#dh-adr-boot-5)); `--add-dir` reaches the sandbox policy as a per-session root that the runner registers, not through configuration |
| `UP-SVC-webApp` | A18's `host.launch` | The advertised URL |

Errors: `agentpreset/not-found` (with the available ids), `agentpreset/invalid` (with the
diagnostic), `agentpreset/locked`.

**Headless.** The runner, its flags, its streams and its exit statuses are specified in
[app-cli.md §6.2.2](app-cli.md) and are not redefined here. This area supplies the
layer. The runner follows four preset rules, stated in [app-cli.md](app-cli.md)
(`DH-ADR-CLI-7`): a fresh run uses the registry's default unless `--agent-preset` names
one; a resumed run binds the session's recorded preset; a recorded preset that no longer
exists is refused before any model call; and the runner never falls back to another.

### 6.3 Ownership, failure, retry, rollback and cleanup

Ownership: the registry's fiber owns every revision (scope, private loader, realm). A
declaration entry owns only its registration; unloading it retires its revision. A lease
gives access and delays disposal; its holder's fiber releases it. An agent's creator
owns the agent; the revision it uses is shared and may be undone only by the registry.
Bundle files are owned by the binary and are never written.

| Situation | What happens | What is undone, what stays uncertain |
|---|---|---|
| A shipped layer names an unknown component or fails strict decoding | The build fails (`DH-T-BOOT-2`); it cannot ship | Nothing |
| A user's edit breaks a declaration statically (unknown child component, bad config, unknown `Extends`, a cycle, a duplicate id) | A21's check rejects the whole edit with file, line and column; the running composition is kept ([DH-ADR-BOOT-7](boot.md#dh-adr-boot-7)) | Nothing changed |
| A required entry, a carrier among them, is not active after the tree settles | A21's audit unloads the tree and prints one report; exit 1 ([boot.md §6.2](boot.md)) | Nothing was served |
| A child of a revision fails to load, waits for a service that never comes, or would write to a shared registry | The revision's scope is closed, which unwinds every child; the declaration is listed as broken with one line per child; `Acquire` returns `agentpreset/invalid` | Agents on the previous revision keep it. No retry until the declaration or a host entry changes |
| A declaration is edited or removed while agents use it | A new revision is built; the old one is retired and closed when its last lease is released | Nothing; running agents are not disturbed |
| Agent creation fails after `Acquire` | The owner's fiber releases the lease during the driver's rollback | The count returns to its value |
| `Select` fails after the scope was re-enclosed (the append is refused) | The old revision is re-enclosed and the new lease released | No event was written. If the process dies between the two steps nothing durable changed and resume binds the recorded preset |
| A session is resumed and its recorded preset no longer exists | Resume fails with `agentpreset/not-found`; an owner may resume it with a named replacement, logged with cause `rebind` | The session stays readable and exportable |
| The registry unloads (shutdown, or `scope` or `projection` is replaced) | It empties the composer seat; the driver disposes the agents composed through it; then revisions close | Sessions are durable and can be resumed |
| A cleanup inside a revision never returns | The revision's close blocks; A21's watchdog names the stuck effect | **Uncertain:** that resource may leak |

## 7. Decisions

<a id="dh-adr-bundle-1"></a>
### DH-ADR-BUNDLE-1 Seven embedded bundles are stacked by five profiles, and only `web` follows configuration edits

- **Decision.** The tables of §6.2 are the content of the profile definitions of [DH-ADR-BOOT-1](boot.md#dh-adr-boot-1). A mode layer changes a lower layer with `merge` or `remove` and never restates a row. Each profile has one carrier entry; it and the entries it cannot serve without are that profile's carrier set in the `Required` list. `boot.reload` is mounted by `web` only. Experimental features are optional bundles, each one disabled group. Scope boundary of this record (unit `bundle.sdk-app.1`): the stdio profiles (`sdk`, `sdk-minimal`, `acp`) mount no web listener, because the program that started the process owns it and talks over its pipes; a person who wants to watch the same sessions in a browser runs `dsh web` on the same home, where the sessions are visible ([DH-ADR-12](../design.md#dh-adr-12)).
- **Why.** The code is compiled in ([DH-ADR-4](../design.md#dh-adr-4)), so what remains of a bundle is its configuration. On a stdio profile a reload that restarts a spine entry would have to claim standard input and output twice; a one-shot run ends before an edit matters.
- **Tradeoff.** A shipped default changes only with a release. Stdio and one-shot profiles need a restart to see a configuration change.
- **Serves.** DH-O-2, DH-O-14. **Verified by.** DH-T-BUNDLE-2, DH-T-BUNDLE-5.

<a id="dh-adr-bundle-2"></a>
### DH-ADR-BUNDLE-2 The host plane is split into `admin`, `core` and `base`; the minimal profile is a subset, not a copy

- **Decision.** `admin` is what an offline command needs. `core` is `admin` plus the agent kernel, and every profile stacks it first. The profile `sdk-minimal` is kept and stacks `core`, `minimal`, `sdk`; there is no bundle of that name. The `minimal` preset also ships in `presets`.
- **Why.** Upstream's minimal bundle exists so that an embedding program, typically an evaluation run in a disposable workspace, gets a small, explicit tree with a fixed prompt (`packages/bundle/sdk-minimal/README.md:28-37`). It repeats about thirty rows of `base`. As a first layer the kernel is written once, and a profile that runs on it alone proves the spine needs no product service. The capability set is also wanted on the full host plane, so it ships as a preset too; [boot.md](boot.md) lists the profile and [app-cli.md](app-cli.md) launches it by name as `dsh sdk-minimal`.
- **Tradeoff.** `core` must stay free of anything optional, which a test holds. Three host-plane files instead of one.
- **Serves.** DH-O-2, DH-O-13. **Verified by.** DH-T-BUNDLE-3, DH-E-BUNDLE-3, DH-E-BUNDLE-5.

<a id="dh-adr-bundle-3"></a>
### DH-ADR-BUNDLE-3 One agent plane: every agent in every profile is composed by a preset

- **Decision.** No shipped host-plane layer mounts a model-facing component: a component that is a child of a shipped preset is not also a top-level entry, unless a short reviewed list says it may be both. Agent creation resolves a preset (the named one, else the default) and fails when it cannot. Per-process state stays in the host plane, as upstream's note rules; the rule is enforced by `CheckMount` ([ADR-5](#dh-adr-bundle-5)).
- **Why.** Upstream keeps two copies of the agent plane and 22 switches between them, and an agent that misses the join silently has no tools. One plane removes the copies, and a join that is part of creation cannot be missed.
- **Tradeoff.** Headless, SDK and ACP sessions now record a preset, and an agent with no preset is an error where upstream allowed it.
- **Serves.** DH-O-2, DH-O-9. **Verified by.** DH-T-BUNDLE-4, DH-T-BUNDLE-9.

<a id="dh-adr-bundle-4"></a>
### DH-ADR-BUNDLE-4 A preset is an ordinary entry whose config holds a keyed map of children; the registry owns revisions and agents hold leases

- **Decision.** Keep upstream: declarations are ordinary configuration; activation is eager; a broken declaration stays listed and refuses new bindings; a retired revision lives until its last lease. Change: children are a map edited by `merge` through `config.Service`, never a list replaced whole; a declaration may extend another. Duplicate ids, an unknown `Extends` and a bad child are static issues. Version admission of child rows is not built. Scope boundary of this record (unit `preset.persona.1`): the persona component mounts only as a child of an agent preset and refuses to load at the top level, because who the agent is belongs to one composition; a global persona would silently apply to every preset and undo their differences.
- **Why.** A second configuration system for presets would need its own precedence and editing rules; a map under an ordinary entry needs no new loader feature and no second edit path, which answers [boot.md §10](boot.md) item 5. Registry ownership is what lets a save not revoke a running agent's tools.
- **Tradeoff.** Children have no order and cannot be nested; neither is needed once realms are automatic. Retired revisions use memory while referenced. Eager activation costs start-up time for presets nobody selects.
- **Serves.** DH-O-1, DH-O-2. **Verified by.** DH-T-BUNDLE-7, DH-T-BUNDLE-8, DH-T-BUNDLE-10, DH-E-BUNDLE-2.

<a id="dh-adr-bundle-5"></a>
### DH-ADR-BUNDLE-5 A revision is one scope and one automatic private realm

- **Decision.** The registry places a revision's children in one realm that is private for exactly the services their catalogue items declare under `Provides`. Outside code reaches such a service for one agent through `Lookup`. Each child's injections are passed to `scope.Table.CheckMount`; a child that would write to a shared registry makes the revision broken.
- **Why.** Upstream asks the author to wrap provider and consumers in a group with a hand-written realm and then audits for leaks after mounting. Building the realm from declared facts makes the leak impossible to write and removes three groups from each preset file. `CheckMount` turns the host-plane rule into a check.
- **Tradeoff.** It relies on `Provides` being true, which `DH-T-BOOT-2` compares. Two children of one preset cannot run separate instances of one service.
- **Serves.** DH-O-1. **Verified by.** DH-T-BUNDLE-9, DH-T-BUNDLE-11.

<a id="dh-adr-bundle-6"></a>
### DH-ADR-BUNDLE-6 A session's preset is fixed at its first turn and every binding is logged with a digest

- **Decision.** `Select` is accepted only for a blank session, runs as an agent maintenance task so that no turn can start inside it, and is a no-op when the session already runs that revision. The registry appends `agent-preset/selected` whenever the bound preset or digest differs from the fold's state: at creation, on a selection, and on a resume that finds a changed definition. Resume binds the current definition of the recorded id. Inference: an owner-only rebind is added for a recorded preset that no longer exists.
- **Why.** The preset decides the tool schemas and prompt sections the model sees, so it belongs in the log ([DH-O-4](../design.md#dh-o-4)). Upstream cannot keep an old revision across a restart and leaves the change unrecorded; the digest records it.
- **Tradeoff.** One log-only event per session and one more after each change. A resumed session can still get a different composition; the log now says when.
- **Serves.** DH-O-4, DH-O-5. **Verified by.** DH-T-BUNDLE-12, DH-T-BUNDLE-13.

<a id="dh-adr-bundle-7"></a>
### DH-ADR-BUNDLE-7 A child agent joins its creator's exact revision unless it names a preset

- **Decision.** As "What a subagent inherits" in §6.2.
- **Why.** A parent that delegates a task described in terms of its own tools must get a child that has them.
- **Tradeoff.** A long-lived parent keeps a retired revision alive for its children. After a restart parent and child may bind different revisions of one id.
- **Serves.** DH-O-9. **Verified by.** DH-T-BUNDLE-14.

<a id="dh-adr-bundle-8"></a>
### DH-ADR-BUNDLE-8 Shipped defaults follow the owner's decisions, and every difference from upstream is recorded

- **Decision.** The "Deliberate differences" column of §6.2 is the record. In short: nothing that sends data is enabled ([DH-ADR-9](../design.md#dh-adr-9)); no desktop row ([DH-ADR-18](../design.md#dh-adr-18)); no interpreter or executor row, script calling is a preset choice ([DH-ADR-8](../design.md#dh-adr-8)); no plugin market ([DH-ADR-13](../design.md#dh-adr-13)); computer use on the optional list ([DH-ADR-19](../design.md#dh-adr-19)); nothing bundled or downloaded for office work ([DH-ADR-20](../design.md#dh-adr-20)); the minimal profile is confined. Platform and environment switches stay, as `!expr` over `os` and `env`.
- **Why.** A default is a product decision that users never see being made.
- **Tradeoff.** A user coming from upstream finds telemetry, account sign-in and full access off.
- **Serves.** DH-O-14, DH-O-7. **Verified by.** DH-T-BUNDLE-6, DH-E-BUNDLE-3, DH-E-BUNDLE-5.

<a id="dh-adr-bundle-9"></a>
### DH-ADR-BUNDLE-9 The headless bundle becomes a layer; its runner belongs to the command-line area; keys come from the environment

- **Decision.** Upstream's headless package is split: the runner, flags, streams and exit statuses are A24's `cli.headless`; this area keeps `headless.yml`. Keep upstream's note: the layer limits the secret store to environment sources and disables the authorization entry, so no saved key or sign-in is read or changed. The layer has no start-up row and restates no policy row. Scope boundary of this record (units `bundle.headless.1`, `bundle.headless.9`): `dsh headless` runs one task, or resumes one session for one more task, and exits; it is not a daemon or a queue, because unattended long-lived work is the web host's job and a one-shot run must end so that its exit status means something. A resume is refused for a session created in another working directory (its file tools would act on a different tree than the operator named) and for a subagent's session (it belongs to its root, [DH-ADR-SDK-4](sdk-acp-typert.md#dh-adr-sdk-4)); a session held by another process is refused by [DH-ADR-12](../design.md#dh-adr-12).
- **Why.** A carrier is code and a bundle is configuration; two specifications of one runner would diverge. A job sharing a home must run on the key it was given.
- **Tradeoff.** The package's output limits are A24's; it removes two of the three (U-K2-4) and keeps "the result stream carries only the answer". A provider that needs a saved sign-in fails in headless unless the operator re-enables the store in the profile layer.
- **Serves.** DH-O-13, DH-O-7. **Verified by.** DH-T-BUNDLE-17, DH-E-BUNDLE-1.

<a id="dh-adr-bundle-10"></a>
### DH-ADR-BUNDLE-10 Every catalogue component is mounted by a shipped layer or is on the optional list

- **Decision.** A21's `DH-T-BOOT-2` already proves that shipped layers name only components the build has, decode strictly and match a golden tree. This area adds what that test does not ask: every catalogue item is a top-level entry or a preset child of some shipped bundle (enabled or disabled), or is on the optional list with an owner and a reason, and never both; each profile composes cleanly for each operating system; the plane rule of [ADR-3](#dh-adr-bundle-3) holds; each profile boots on an empty home without an outbound request.
- **Why.** "Only names what exists" is half of honesty. The other half is that nothing is compiled in and silently unreachable, and that a default cannot start sending.
- **Tradeoff.** Adding a component means touching a layer or the optional list in the same change.
- **Serves.** DH-O-2, DH-O-15, DH-O-14. **Verified by.** DH-T-BUNDLE-1, DH-T-BUNDLE-2, DH-T-BUNDLE-4, DH-T-BUNDLE-6, DH-E-BUNDLE-4.

## 8. Deliverables

Order: [DH-D-BUNDLE-1](#dh-d-bundle-1) and [DH-D-BUNDLE-6](#dh-d-bundle-6) first, with
the host-plane layers of [DH-D-BUNDLE-2](#dh-d-bundle-2); then
[DH-D-BUNDLE-4](#dh-d-bundle-4), the `standard` preset of
[DH-D-BUNDLE-5](#dh-d-bundle-5) and the `headless`, `web` and `sdk` layers of
[DH-D-BUNDLE-3](#dh-d-bundle-3), after which those profiles may be enabled; then the
P1 profiles and presets ([DH-D-BUNDLE-8](#dh-d-bundle-8),
[DH-D-BUNDLE-9](#dh-d-bundle-9)) and last the `creator` preset
([DH-D-BUNDLE-10](#dh-d-bundle-10)). A layer file or a preset whose entries name
components of later deliverables grows with them: each later deliverable adds its own
entry or child, and `DH-T-BOOT-2` holds every file to the catalogue of the build at each
step. Priority never weakens a "done when".

<a id="dh-d-bundle-1"></a>
### DH-D-BUNDLE-1 The content of the five profile definitions and the bundle membership

- **Result and why it matters.** `dsh <profile>` has a composition to start, and the required-entry list names what must be alive for it to be a product.
- **How it is implemented.** The bundle-to-files and profile-to-bundles tables of §6.2 are written as the Go values A21's `profile` package defines; the spine ids are the constant `profile.Spine` and each definition's `Required` list is that constant plus the profile's carrier set; the `admin` bundle is exported for A24's admin tree.
- **Components and files.** `internal/boot/profile` (content only). **Depends on.** [DH-D-LOADER-6](../libs/cordis-loader.md#dh-d-loader-6), [DH-D-BOOT-3](boot.md#dh-d-boot-3) (profile definitions, embedded bundles, the `Required` rule). Profile selection and the admin tree are [DH-D-CLI-1](app-cli.md#dh-d-cli-1) and [DH-D-CLI-9](app-cli.md#dh-d-cli-9), which consume this deliverable. **Priority.** P0. **Outcome.** [DH-O-2](../design.md#dh-o-2), [DH-O-14](../design.md#dh-o-14).
- **Done when.** DH-T-BUNDLE-2 and DH-T-BUNDLE-5 pass.

<a id="dh-d-bundle-2"></a>
### DH-D-BUNDLE-2 The host-plane layers `admin`, `core` and `base`

- **Result and why it matters.** One composition for offline commands, one kernel that can run an agent alone, and one product layer over it, with defaults that send nothing.
- **How it is implemented.** Three layer files written from §6.2, one entry per component with only the fields that differ from the component's Go defaults; each experimental feature is an optional bundle. `base.yml` is part of every P0 profile except `sdk-minimal`, so it is P0 too: it is first written with the entries of the P0 components, and each later deliverable that builds a component of §6.2 adds that component's entry in the same change.
- **Components and files.** `admin.yml`, `core.yml`, `base.yml`. **Depends on.** [DH-D-BUNDLE-1](#dh-d-bundle-1). The components each file mounts are built by the areas named in §6.2. **Priority.** P0. **Outcome.** [DH-O-2](../design.md#dh-o-2), [DH-O-14](../design.md#dh-o-14).
- **Done when.** DH-T-BUNDLE-3 and DH-T-BUNDLE-6 pass and [DH-E-BUNDLE-3](#dh-e-bundle-3) has passed.

<a id="dh-d-bundle-3"></a>
### DH-D-BUNDLE-3 The mode layers and profiles `web`, `headless` and `sdk`

- **Result and why it matters.** The same binary is a web application, a one-shot job and an SDK server. The `sdk` profile is in this P0 deliverable because its carrier, [DH-D-SDK-3](sdk-acp-typert.md#dh-d-sdk-3) and [DH-D-SDK-4](sdk-acp-typert.md#dh-d-sdk-4), is P0.
- **How it is implemented.** `web.yml`, `headless.yml` and `sdk.yml`, each a few `insert`, `merge` and `remove` operations over the layers below it.
- **Components and files.** The three files. **Depends on.** [DH-D-BUNDLE-2](#dh-d-bundle-2), [DH-D-BUNDLE-5](#dh-d-bundle-5), [DH-D-HOST-1](host-telemetry.md#dh-d-host-1) and [DH-D-API-1](api.md#dh-d-api-1) (the first entries of `web`), [DH-D-SDK-3](sdk-acp-typert.md#dh-d-sdk-3) and [DH-D-SDK-4](sdk-acp-typert.md#dh-d-sdk-4) (the entries of `sdk`). The other entries of `web.yml` are added by the deliverables that build them. The headless runner and the launchers ([DH-D-CLI-2](app-cli.md#dh-d-cli-2)) consume this deliverable. **Priority.** P0. **Outcome.** [DH-O-2](../design.md#dh-o-2), [DH-O-13](../design.md#dh-o-13).
- **Done when.** DH-T-BUNDLE-2 and DH-T-BUNDLE-17 pass and [DH-E-BUNDLE-1](#dh-e-bundle-1), [DH-E-BUNDLE-3](#dh-e-bundle-3) and [DH-E-BUNDLE-4](#dh-e-bundle-4) have passed.

<a id="dh-d-bundle-8"></a>
### DH-D-BUNDLE-8 The mode layers and profiles `acp` and `sdk-minimal`

- **Result and why it matters.** The same binary is also an ACP server and a minimal SDK server. They were the P1 part of [DH-D-BUNDLE-3](#dh-d-bundle-3).
- **How it is implemented.** `acp.yml` and `minimal.yml`, as in §6.2; `sdk-minimal` stacks `core`, `minimal` and `sdk`.
- **Components and files.** The two files. **Depends on.** [DH-D-BUNDLE-3](#dh-d-bundle-3), [DH-D-BUNDLE-9](#dh-d-bundle-9) (the `minimal` preset), [DH-D-SDK-9](sdk-acp-typert.md#dh-d-sdk-9) (the ACP carrier). **Priority.** P1. **Outcome.** [DH-O-2](../design.md#dh-o-2), [DH-O-13](../design.md#dh-o-13).
- **Done when.** DH-T-BUNDLE-2 passes for both profiles and [DH-E-BUNDLE-5](#dh-e-bundle-5) has passed.

<a id="dh-d-bundle-4"></a>
### DH-D-BUNDLE-4 The preset registry: declarations, revisions, leases, selection and the session record

- **Result and why it matters.** One host runs agents with different capability sets; a preset can be edited while agents run; the log says which composition a session ran.
- **How it is implemented.** The registry keeps a map of declarations and a list of revisions with a lease count, under one mutex with no component code run under it. A declaration's validator resolves `Extends` and checks each child. A revision is built as [ADR-5](#dh-adr-bundle-5) describes and audited after the host tree settles. `Select` and the binding at creation follow [ADR-6](#dh-adr-bundle-6); the fold is registered with area A06.
- **Components and files.** `internal/boot/agentpreset`. **Depends on.** [DH-D-LOADER-10](../libs/cordis-loader.md#dh-d-loader-10), [DH-D-CORDIS-3](../libs/cordis.md#dh-d-cordis-3), [DH-D-SESSION-8](core-session.md#dh-d-session-8) (scopes and `CheckMount`), [DH-D-AGENT-6](agent-loop.md#dh-d-agent-6) (the composer seat, `Registry.SetComposer` and `Recompose`), [DH-D-BOOT-1](boot.md#dh-d-boot-1) (the catalogue), [DH-D-BOOT-7](boot.md#dh-d-boot-7) (`config.Service`, through which the saved default is written), [DH-D-VIEW-1](session-views.md#dh-d-view-1) (the fold registry). **Priority.** P0. **Outcome.** [DH-O-1](../design.md#dh-o-1), [DH-O-2](../design.md#dh-o-2), [DH-O-4](../design.md#dh-o-4), [DH-O-9](../design.md#dh-o-9).
- **Done when.** DH-T-BUNDLE-7, DH-T-BUNDLE-8, DH-T-BUNDLE-9, DH-T-BUNDLE-10, DH-T-BUNDLE-11, DH-T-BUNDLE-12, DH-T-BUNDLE-13 and DH-T-BUNDLE-14 pass and [DH-E-BUNDLE-2](#dh-e-bundle-2) has passed.

<a id="dh-d-bundle-5"></a>
### DH-D-BUNDLE-5 The `standard` preset and the persona component

- **Result and why it matters.** A new session has tools, a persona and skills without any user configuration, and a preset can change who the agent is.
- **How it is implemented.** `presets/standard.yml`. `agent.persona` registers the scoped prefix and suffix sections, a `Complete` section, and `Suppress` for all or named optional contexts through `prompt.Service` ([tools.md](tools.md) §6.1); it refuses to load outside a preset. Each child of `standard` is a component of the area that owns it (A03, A08, A09, A11 to A17, A21); the file is first written with the children whose deliverables are P0, and each later deliverable adds its child.
- **Components and files.** `presets/standard.yml`, `agentpreset/persona`. **Depends on.** [DH-D-BUNDLE-4](#dh-d-bundle-4), [DH-D-TOOLS-5](tools.md#dh-d-tools-5) (`prompt.Service`, for the persona). **Priority.** P0. **Outcome.** [DH-O-2](../design.md#dh-o-2), [DH-O-4](../design.md#dh-o-4).
- **Done when.** DH-T-BUNDLE-15 (for `standard`) and DH-T-BUNDLE-16 pass and [DH-E-BUNDLE-2](#dh-e-bundle-2) has passed.

<a id="dh-d-bundle-9"></a>
### DH-D-BUNDLE-9 The `script` and `minimal` presets

- **Result and why it matters.** An agent can run in script-calling mode, and an embedding program can choose a small, explicit capability set. They were the P1 part of [DH-D-BUNDLE-5](#dh-d-bundle-5).
- **How it is implemented.** `presets/script.yml` extends `standard` and adds `program.mode`; `presets/minimal.yml` is the complete small declaration of §6.2.
- **Components and files.** `presets/script.yml`, `presets/minimal.yml`. **Depends on.** [DH-D-BUNDLE-5](#dh-d-bundle-5), [DH-D-PTC-1](ptc-workflow.md#dh-d-ptc-1) (`program.mode`). **Priority.** P1. **Outcome.** [DH-O-2](../design.md#dh-o-2), [DH-O-4](../design.md#dh-o-4).
- **Done when.** DH-T-BUNDLE-15 passes for `script` and `minimal`.

<a id="dh-d-bundle-10"></a>
### DH-D-BUNDLE-10 The `creator` preset

- **Result and why it matters.** An agent that writes plugins for this harness has the documentation and the plugin tool it needs. It was the P2 part of [DH-D-BUNDLE-5](#dh-d-bundle-5).
- **How it is implemented.** `presets/creator.yml` extends `standard` and adds a second persona sentence, `boot.plugins.tool` and the embedded `harness-docs` skill.
- **Components and files.** `presets/creator.yml`. **Depends on.** [DH-D-BUNDLE-5](#dh-d-bundle-5), [DH-D-BOOT-10](boot.md#dh-d-boot-10) (the tool with `attach`), [DH-D-CONTEXT-8](context-compaction-skill.md#dh-d-context-8) (skills embedded in the binary). **Priority.** P2. **Outcome.** [DH-O-2](../design.md#dh-o-2), [DH-O-11](../design.md#dh-o-11).
- **Done when.** DH-T-BUNDLE-15 passes for `creator`.

<a id="dh-d-bundle-6"></a>
### DH-D-BUNDLE-6 The coverage check and the optional list

- **Result and why it matters.** A component cannot ship unreachable by accident, a shipped default cannot start sending, and each profile is known to compose on each operating system.
- **How it is implemented.** Tests beside the bundle files run A21's pure composition and static check for every profile, the `admin` composition and each operating system; walk the catalogue against the entries and preset children of all shipped bundles and `optional.yml`; and boot each profile with A23's recording transport.
- **Components and files.** `internal/boot/profile` (tests), `optional.yml`. **Depends on.** [DH-D-BUNDLE-1](#dh-d-bundle-1), [DH-D-BOOT-1](boot.md#dh-d-boot-1) (the catalogue and its naming rule), [DH-D-BOOT-3](boot.md#dh-d-boot-3) (`Compose`, the static check and the golden tree of `DH-T-BOOT-2`), [DH-D-UTIL-6](util-test-support.md#dh-d-util-6) (the recording transport). **Priority.** P0. **Outcome.** [DH-O-2](../design.md#dh-o-2), [DH-O-14](../design.md#dh-o-14), [DH-O-15](../design.md#dh-o-15).
- **Done when.** DH-T-BUNDLE-1, DH-T-BUNDLE-2, DH-T-BUNDLE-4 and DH-T-BUNDLE-6 pass and [DH-E-BUNDLE-4](#dh-e-bundle-4) has passed.

## 9. Acceptance

### 9.1 Local tests

| Test ID | Proves | What is asserted |
|---|---|---|
| DH-T-BUNDLE-1 | D-6 | Every catalogue item is an entry or a preset child of a shipped bundle, or is on the optional list, and not both; the optional list names only catalogue items; a planted unlisted component fails the test |
| DH-T-BUNDLE-2 | D-1, D-3, D-6, D-8 | Each profile and the `admin` composition pass A21's static check for `windows`, `linux` and `darwin`, with an empty environment and with each documented variable; exactly one shell stack is enabled per system |
| DH-T-BUNDLE-3 | D-2 | Through the real loader: `core` with a fake carrier, the replay provider and a one-tool test preset runs one turn; `core` mounts no child of any shipped preset; `admin` mounts no carrier, agent, model adapter or plugin entry |
| DH-T-BUNDLE-4 | D-6 | No component is both a top-level entry and a preset child in shipped bundles unless on the reviewed list; a planted violation fails |
| DH-T-BUNDLE-5 | D-1 | Each profile's composed tree holds its carrier and the listed spine ids, enabled; `boot.reload` is present in `web` only; every required id exists in some profile |
| DH-T-BUNDLE-6 | D-2, D-6 | Each profile booted on an empty home with a recording transport makes no outbound request before the first prompt; the exporter is mounted and the sharing gate is shut |
| DH-T-BUNDLE-7 | D-4 | A declaration activates eagerly; a failing child leaves it listed as broken, the host starts, `Acquire` returns `agentpreset/invalid` with the child's error; a child waiting for a host service becomes usable when the service appears |
| DH-T-BUNDLE-8 | D-4 | Editing a child retires the revision; an agent holding a lease keeps its tools; a new agent gets the new revision; the retired revision is closed at its last release and its fibers are gone |
| DH-T-BUNDLE-9 | D-4 | A tool mounted by preset A is invisible to an agent of preset B and to the global layer; an agent created with an unknown preset fails and leaves no registry entry or lease |
| DH-T-BUNDLE-10 | D-4 | A `merge` at `children.<key>` changes one child, a null removes it, and the supplying layer is reported per field; `Extends` merges the base's children and follows a later edit of the base; a cycle, an unknown base, a duplicate id, an unknown default and a child with an unknown key are static issues with a position; an edit shadowed by the home layer is refused with that layer's name |
| DH-T-BUNDLE-11 | D-4 | A service provided inside a preset is not resolvable from the host realm, is resolvable by a sibling child, and is returned by `Lookup` for an agent of that preset only; a child that injects a shared registry breaks the revision |
| DH-T-BUNDLE-12 | D-4 | `Select` on a blank session re-encloses, appends one event and notifies; a second identical call appends nothing; after a `turn/start` it returns `agentpreset/locked`; a refused append restores the old binding |
| DH-T-BUNDLE-13 | D-4 | Resume binds the recorded id; an unchanged digest appends nothing; a changed one appends cause `resume`; a missing id fails with the available ids, and an owner rebind is logged |
| DH-T-BUNDLE-14 | D-4 | A child with no preset holds its creator's revision after an edit; a child naming a preset gets the current one; releasing the parent first does not close the revision under the child |
| DH-T-BUNDLE-15 | D-5, D-9, D-10 | Golden, one case per shipped preset in the build (`standard` for D-5, `script` and `minimal` for D-9, `creator` for D-10): the step tool set and assembled prompt on each operating system; `script` and `creator` differ from `standard` only by their additions; the `acp` and `headless` profiles offer no question tool |
| DH-T-BUNDLE-16 | D-5 | Persona: a scoped prefix shadows the global one for that preset only; `Complete` leaves one section; a named suppression removes one context and an unknown or required name fails load; mounting it at the top level fails with a message naming the prompt component's field |
| DH-T-BUNDLE-17 | D-3 | Headless layer: with one key saved in the secret store and another in the environment, the route resolves the environment key and the store file is unchanged; the disabled authorization entry leaves no pending entry in the start-up report |

### 9.2 Real-environment scenarios

<a id="dh-e-bundle-1"></a>
#### DH-E-BUNDLE-1 A one-shot job runs on its own key and resumes under its recorded preset

- **Proves.** [DH-D-BUNDLE-3](#dh-d-bundle-3).
- **Given.** One machine per supported operating system with the built binary; a home in which a `web` run saved a different, invalid key and created a blank session; a profile layer that declares a second agent preset `lean` (it extends `standard` and removes the web tool), so the scenario needs no P1 preset; a real model API whose valid key is supplied only in the job's environment; a workspace with one text file; the real sandbox backend.
- **When.** (1) `dsh headless --json` with a task that reads the file. (2) Resume that session with a second task. (3) Select `lean` for the blank session in the browser, send one prompt there, stop the web host, and resume that session with `dsh headless`. (4) Remove the `lean` declaration from the profile layer and resume again.
- **Then.** (1) succeeds and the provider saw the environment key; the saved key file is unchanged. (2) continues the same session with the `standard` tools. (3) the request header in the log lists the `lean` tools, not the default's. (4) the run is refused before any model request, naming the missing preset and the available ones.
- **Cleanup.** Delete the home and the workspace. If a run hangs, keep the session export and the diagnostic stream.

<a id="dh-e-bundle-2"></a>
#### DH-E-BUNDLE-2 Two presets in one host, and an edit while a session runs

- **Proves.** [DH-D-BUNDLE-4](#dh-d-bundle-4), [DH-D-BUNDLE-5](#dh-d-bundle-5).
- **Given.** The `web` profile on an empty home with the `lean` declaration of [DH-E-BUNDLE-1](#dh-e-bundle-1) in its profile layer, a real browser, a real model API, a workspace.
- **When.** (1) Create one session with `standard` and one with `lean`, and prompt both. (2) While the first is in a turn, remove the web tool from `standard` in the settings page. (3) Create a third session with `standard`. (4) Edit `standard` so that one child cannot load. (5) Restart the host and continue the first session.
- **Then.** (1) the logged request headers show different tool sets. (2) the running turn keeps the web tool and only the profile layer file changed. (3) the third session lacks it. (4) the roster shows `standard` as broken with the child's error, new sessions with it are refused, the host and both old sessions keep working. (5) the first session's log gains one `agent-preset/selected` with cause `resume` and a new digest.
- **Cleanup.** Stop the host, delete the home. Keep the layer file and the session exports if a step fails.

<a id="dh-e-bundle-3"></a>
#### DH-E-BUNDLE-3 The `web`, `headless` and `sdk` profiles send nothing but model requests

- **Proves.** [DH-D-BUNDLE-2](#dh-d-bundle-2), [DH-D-BUNDLE-3](#dh-d-bundle-3).
- **Given.** The built binary behind a recording proxy that is the only route out; an empty home; a real model API; for `sdk` a program built on the Go SDK client; for `web` a real browser.
- **When.** Start each of the three profiles, run one prompt that uses a shell tool, and stop it.
- **Then.** The proxy log holds requests to the model provider only.
- **Cleanup.** Delete the home; keep the proxy log on failure.

<a id="dh-e-bundle-5"></a>
#### DH-E-BUNDLE-5 The `acp` and `sdk-minimal` profiles send nothing but model requests, and the minimal profile is confined

- **Proves.** [DH-D-BUNDLE-8](#dh-d-bundle-8).
- **Given.** The built binary behind a recording proxy that is the only route out; an empty home; a real model API; for `sdk-minimal` a program built on the Go SDK client; for `acp` a real ACP client.
- **When.** Start each of the two profiles, run one prompt that uses a shell tool, and stop it. For `sdk-minimal`, ask for a write outside the workspace.
- **Then.** The proxy log holds requests to the model provider only. `sdk-minimal` offers exactly the persistent shell and `read_image`, and the outside write is refused.
- **Cleanup.** Delete the home; keep the proxy log on failure.

<a id="dh-e-bundle-4"></a>
#### DH-E-BUNDLE-4 The release binary agrees with its own layers on every platform

- **Proves.** [DH-D-BUNDLE-3](#dh-d-bundle-3), [DH-D-BUNDLE-6](#dh-d-bundle-6).
- **Given.** The release archive for each published platform on a real machine of that platform, with nothing else installed.
- **When.** Run `dsh <profile> --help` for each profile in the build, start each with an empty home and stop it, then start `web` and stop it.
- **Then.** No start-up report holds an issue; the enabled shell stack matches the system; office and computer-use capabilities report `unavailable` and nothing is downloaded; the host starts and stops cleanly.
- **Cleanup.** Remove the temporary home.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

Clauses this design relies on that their owners have not yet confirmed in their own
text (each sent as a request; the deliverable that names it cannot close until then):

1. **A18.** `host.launch` replaces the glue plugin; the inventory reads `Inventory()`; `host.launch` registers `DSH_WEB_URL` with `shellexec.env`.
2. **A14, A16, A17.** An embedded `harness-docs` skill; office tools reported `unavailable`, not hidden, when LibreOffice is missing; the question tool hidden when no answerer is bound.

Settled by their owners and used as written: the composer seat is `Registry.SetComposer`
and `Registry.Recompose(ref, preset)` of [agent-loop.md §6.1](agent-loop.md), with the
driver holding the `*scope.Scope` and calling `SetEnclosing`; a second loader instance
mounts under a scope's context with no writable layer
([DH-D-LOADER-10](../libs/cordis-loader.md#dh-d-loader-10)); the `agentPreset` fold
registers through `projection.Register`, and this area declares `agent-preset/selected`.

Limitations: a preset separates trusted compiled-in components and is not a security
boundary. A resumed session may run a different composition from the one it started
with; the digest records it and does not prevent it. The default model and provider are
upstream's and are area A04's to confirm. The digest covers configuration and the
version string, not behaviour changes inside one version of a component. Whether eager
activation of four presets is noticeable at start-up can only be learned by measuring.

## 11. Units dispositioned by this spec

59 assigned units and 4 decision notes, in `trace/fragments/A27.jsonl`: 9 `UP-PKG`
(4 ported, 2 replaced, 3 merged), 40 `UP-DEBT` (16 resolved, 2 not applicable,
22 carried), 1 `UP-EVT` (ported), 6 `UP-CFG` (3 ported, 3 replaced), 3 `UP-SVC`
(1 ported, 2 replaced), 4 `UP-DEC` (3 ported, 1 replaced). Go items declared: 9
deliverables (`DH-D-BUNDLE-1` to `-6` and `-8` to `-10`), 10 decisions, 17 local tests,
5 scenarios.
