# Boot and composition (profiles, launch, configuration reload, plugin manager, plugin market): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md). Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `BOOT`. Nothing here has been built, run or tested.

| | |
|---|---|
| **Area key** | `BOOT` (cluster `A21`) |
| **Upstream packages** | 6 in the group `boot`: `app-boot`, `cmdline`, `config-editor`, `hmr`, `plugin-manager`, `plugin-market` |
| **Serves outcomes** | [DH-O-1](../design.md#dh-o-1), [DH-O-2](../design.md#dh-o-2), [DH-O-11](../design.md#dh-o-11), [DH-O-14](../design.md#dh-o-14) |
| **Depends on areas** | A22 ([extensions.md](extensions.md): the `plugin` entry, inspection, the approved copy, attach and detach), A24 ([app-cli.md](app-cli.md): command words, the exit-status table), A27 ([bundle-preset.md](bundle-preset.md): bundle content, the required ids, agent presets), A23 ([util-test-support.md](util-test-support.md): `schema`, `homedir`), A08 (`fsx` file locks), A07 (home layout, the settings form), A17 (secret names), A18 (fatal diagnostic, the one off-switch), A03 (tool registration), A19 and C7 (routes and the plugins page) |
| **Priority** | P0 for the catalogue, launch data, profiles and layers, the YAML package and the boot sequence, because nothing else starts without them. P1 for preview, validated reload and the edit path, the watchdog and the plugin manager |

## 1. Purpose

This area turns a command line into a running harness and keeps the running harness in step with its configuration. It decides which components exist in a build, which of them a profile starts and with what configuration, in what order start-up proceeds, when the harness counts as ready, what a person sees when start-up fails, how a configuration edit reaches a running process, and how the harness stops. It also owns the surface through which a person or an agent lists, enables, disables, adds and removes plugins.

Without it there is a library of components and no product: no way to choose `web` over `headless`, no preview of what will run, no rule for a bad edit, and no bounded shutdown. The loader mechanics (layer composition, the entry model, expressions, write-back) are specified in [../libs/cordis-loader.md](../libs/cordis-loader.md) and [../libs/cordis-plugins.md](../libs/cordis-plugins.md). This document says how the **application** uses them. The plugin protocol and manifest belong to area A22, the command grammar to A24, and the content of each bundle to A27 and the area that owns each component.

## 2. Vocabulary

Framework terms (component, fiber, effect, service, layer, writable layer, entry, bundle, profile) are defined in [../design.md §2](../design.md#2-vocabulary).

| Term | What it is | Why it exists |
|---|---|---|
| **Launcher** | The first stage of `dsh`: it reads its own options and the profile name, then calls boot. | One binary starts every product mode, and an app's options stay the app's. |
| **Launch data** | Facts fixed before the tree exists: profile name, harness home, working directory, binary version, whether a terminal is attached, the app's words. | Components and expressions need them, and nothing may change them later. |
| **Launch environment** | An immutable snapshot of environment variables in three layers: the process environment, the working directory's `.env`, the home's `.env`. | A value's origin stays known, and a file found on disk cannot change how the harness starts. |
| **Catalogue** | The list of every component compiled into a build: its name, config type, the services it provides and the data view it offers to expressions. | Configuration can only name what the binary contains, and the build's content can be read and checked. |
| **Layer stack** | The ordered layers of one launch: bundle layers, the profile layer, the home layer, command-line overlays, the launcher layer. | A later layer changes an earlier one without editing it. |
| **Launcher layer** | A last, generated, in-memory layer holding switches a launch forces (for example the one off-switch of [DH-ADR-9](../design.md#dh-adr-9)). | A forced switch must outrank every file. |
| **Candidate** | The tree that would result from an edit, composed and checked but not applied. | A bad edit is found before anything restarts. |
| **Static issue, load failure** | A static issue is found by reading configuration: a syntax error, an unknown key, a wrong type, an unknown component, a malformed expression. A load failure happens only when an entry runs: a port in use, a missing file. | The first can reject an edit as a whole; the second can only be reported per entry. |
| **Required entry** | An entry id in the profile definition's `Required` list: it must be present, enabled and active for start-up to succeed, and no layer may remove or disable it. | An HTTP product without its server is not running; a missing optional tool is tolerable. |
| **Stop request** | The one call that asks the process to end, `launch.App.Stop(reason, status)`. Signals, end of input, `dsh stop`, the UI and a finished one-shot run all make it. | One shutdown path with one bound, whoever asks. |
| **Safe mode** | A launch (`--safe`) in which the launcher layer switches off everything that is not compiled-in, first-party code: plugin processes and optional bundles ([DH-ADR-BOOT-12](#dh-adr-boot-12)). | After a crash or a bad plugin a person needs a host that starts, so that the cause can be removed. |
| **Readiness** | The one-time signal that the tree settled and the required-entry audit passed. | Carriers announce themselves, and reload begins, only after it. |
| **Revision** | A hash of an entry's composed config, as area A07 defines it. | An edit based on stale data is refused, in any process. |
| **Protected entry** | A required entry, or a management component. The plugin manager refuses to disable or remove it. | A person must not lock themselves out through the management surface. |
| **Watchdog** | A monitor that reports the entry, component and effect label of a cleanup that has not returned within a limit. | cordis waits for every cleanup; a wait with no name attached cannot be diagnosed. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.boot.app-boot` | `@deepseek-ai/dsh-app-boot` | other (library) | Loads environment layers, resolves a profile and its bundles, composes patches, mounts the root include, audits start-up, renders the config dump, and installs Node module resolution for profile packages (`packages/boot/app-boot/README.md:12`, `:71`) |
| `UP-PKG-packages.boot.cmdline` | `@deepseek-ai/dsh-cmdline` | definition | Provides `ctx.cmdlineArgs`, `ctx.appExit` and `ctx.appReady` before the tree mounts, and adapts an app's commander program (`packages/boot/cmdline/src/index.ts:84-88`, `:165`) |
| `UP-PKG-packages.boot.config-editor` | `@deepseek-ai/dsh-config-editor` | provider (`ctx.configEditor`) | Validates a new config for one entry, writes it into the profile patch keeping comments, and reconciles the tree (`packages/boot/config-editor/src/index.ts:86-145`) |
| `UP-PKG-packages.boot.hmr` | `@deepseek-ai/dsh-hmr` | provider (`ctx.hmr`) | One queue for config-file watching, profile recomposition and JavaScript module replacement (`packages/boot/hmr/src/index.ts:268`, `:289`, `:937`) |
| `UP-PKG-packages.boot.plugin-manager` | `@deepseek-ai/dsh-plugin-manager` | provider (`ctx.pluginManager`) and consumer (tool) | Toggles entries and bundles, installs and removes npm bundles with pnpm, updates them, and exposes `plugin_manager` (`packages/boot/plugin-manager/README.md:14`, `packages/boot/plugin-manager/src/tools.ts:88-91`) |
| `UP-PKG-packages.boot.plugin-market` | `@deepseek-ai/dsh-plugin-market` | provider (`ctx.pluginMarket`) and consumer (tool) | Reads user-configured plugin indexes over HTTP or Git, searches them, and exposes `plugin_market`; off in every shipped profile (`packages/boot/plugin-market/README.md:12`, `:27`) |

### 3.2 Behaviour

**Composition.** A profile lives at `$DSH_HOME/profiles/<name>` as an npm project: a `package.json` that lists its bundles, a `node_modules` tree and the user's `cordis.patch.yml`. Layers apply to an empty list in this order: the profile's bundles, the profile patch, the home patch, then `--patch` overlays (`docs/architecture.md:17-25`, `packages/boot/app-boot/src/profile-context.ts:74-86`). A patch replaces a row's whole config or inserts rows (`packages/boot/app-boot/README.md:65`). `--dump-config` prints the composed list with expressions verbatim and comments naming each source (`packages/boot/app-boot/README.md:77`).

**Launch facts.** The launcher loads the working directory's `.env` and the home's `.env` under the inherited environment, and refuses a file that sets a variable deciding how the process starts, such as `PATH` or any `DSH_*` name (`packages/boot/app-boot/src/index.ts:259-283`, `:161`). It then provides data-only services: `profileContext` with profile locations, start-up bundles and overlay paths (`packages/boot/app-boot/src/profile-context.ts:15-31`), the app's words, an exit request and a readiness signal. An app's start-up plugin parses the words and publishes values that config rows read through `!!js ctx.webStartup.port` (`packages/boot/cmdline/README.md:51-66`).

**Start-up.** `boot()` creates the context, mounts the loader and the root include, waits for the tree to settle and audits it (`packages/boot/app-boot/src/index.ts:1268-1305`). Start-up is best effort: every plugin that can activate does. Ten entry ids are required; one that is present, enabled and not active makes boot dispose the tree and exit non-zero (`packages/boot/app-boot/src/index.ts:967-978`, `packages/boot/app-boot/README.md:45`). An unhandled rejection or uncaught exception is fatal: one diagnostic, a release hook bounded by two seconds, exit 1 (`packages/boot/app-boot/src/index.ts:859`, `:901-935`).

**Reload.** When enabled, `dsh-hmr` watches the profile manifest, both user patches and every overlay, waits for readiness, and recomposes on change; module replacement is an opt-in (`packages/boot/hmr/README.md:27`, `:61`). The shipped base bundle enables config-only reload; headless, SDK and ACP disable it (`docs/architecture.md:27`). A settled reconciliation emits `app-boot/config-reload`, which carries no diff (`docs/subsystems/boot.md:535`).

**Editing.** `configEditor.edit` takes the profile manifest lock, derives a candidate, writes the patch atomically and reconciles; on failure it restores the previous text and reconciles again (`packages/boot/config-editor/src/index.ts:92`, `:140-145`). A plugin toggle edits only `disabled` in the last matching override and deletes an override that merely restates the lower layers (`packages/boot/plugin-manager/src/patch.ts:34-70`).

**Plugin management.** The service and the `dsh plugin` command share package operations under the manifest lock (`packages/boot/plugin-manager/src/operations.ts:602`). Installing runs `pnpm add` over a plan of registries, after an inspection that reports one of seven problems (`packages/boot/plugin-manager/README.md:50`, `:64`). The `plugin_manager` tool body itself requests `danger-full-access` or a one-call approval for every action, including listing (`packages/boot/plugin-manager/src/tools.ts:106-111`). The `plugin_market` tool is read-only and needs no approval (`packages/boot/plugin-market/README.md:87`).

### 3.3 Failure, retry, rollback and cleanup upstream

- **Start-up.** A missing or malformed root config or overlay stops start-up. An optional entry that fails to import, validate, evaluate or apply is a warning; a required one stops start-up after the tree is disposed (`packages/boot/app-boot/README.md:102-115`).
- **Reload.** A missing, malformed or invalid live patch is rejected and the running configuration stays. An entry whose config fails its schema keeps its previous instance, while its siblings' changes apply: "not a transactional rollback of sibling changes" (`packages/boot/app-boot/README.md:104-106`, `:117`).
- **Install.** A failed or cancelled installation restores `package.json` and the lockfile; downloaded files may remain. A failed removal keeps its partial state. An update is a removal and an installation with a reinstall on failure, and "a reinstallation that also fails leaves the package uninstalled" (`packages/boot/plugin-manager/README.md:58`, `:188-197`).
- **Left behind.** A lock whose holder died is taken over, but that holder's pnpm tree can still be running, so a run record guards the next operation (`packages/boot/plugin-manager/README.md:128`). Discovered `.env` values are written into `process.env` and so reach every child process (`packages/boot/app-boot/src/index.ts:269-274`).

## 4. Upstream decisions dissected

The first table holds the 18 decision notes handed to this area (paths are under `.agents/notes/`). The second holds decisions visible only in code and READMEs. Longer reasons are in the ledger rows.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `implemented/architecture/2026-08-04-configuration-source-ownership` | One precedence order for non-secret values; a discovered `.env` may not set launch-only names (`:21-30`, `:47`) | keep, with a change | The rule is right. The snapshot is no longer copied into the process environment, which the note lists as unsolved (`:60`) | [DH-ADR-BOOT-4](#dh-adr-boot-4) |
| `implemented/architecture/2026-09-09-consumer-owned-startup-strictness` | The application, not the framework, lists required entry ids; the audit runs only at boot (`:13-19`) | keep, tightened | Same policy and owner; the ids change because client module entries no longer exist, and a required id must also be present and enabled | [DH-ADR-BOOT-6](#dh-adr-boot-6) |
| `implemented/architecture/2026-09-09-profile-resolution-generations` | One immutable table says which npm package each name resolves to | change | The need (one immutable answer to "what code does this name mean") is met by the build catalogue; Node resolution does not exist | [DH-ADR-BOOT-3](#dh-adr-boot-3) |
| `implemented/architecture/2026-09-14-current-profile-plugin-management` | CLI, web and tool share operations under one lock; reload is one queue (`:13`) | change | Shared operations, one lock and one queue are kept; pnpm and module replacement go | [DH-ADR-BOOT-8](#dh-adr-boot-8), [DH-ADR-BOOT-10](#dh-adr-boot-10) |
| `implemented/architecture/2026-09-15-guided-plugin-installation` | Inspect before installing; restore files after a failed run (`:13-15`) | change | Inspection is kept. There is nothing to restore: an operation is one write of one file | [DH-ADR-BOOT-10](#dh-adr-boot-10) |
| `implemented/architecture/2026-09-16-creator-persistent-plugin-management` | An agent installs a bundle mid-conversation through `plugin_manager`, behind full access or an approval (`:13`) | change | The goal is kept for one session: the tool attaches a local plugin to its own session after an owner's consent (A22). A durable installation is an owner's action, never a tool call | [DH-ADR-BOOT-10](#dh-adr-boot-10) |
| `implemented/architecture/2026-09-18-plugin-install-registries` | A plan of npm registries with mirrors; a private registry never falls back to a public one | drop | No download ([DH-ADR-13](../design.md#dh-adr-13)). The no-fallback rule is recorded for a later registry | [DH-ADR-BOOT-11](#dh-adr-boot-11) |
| `implemented/architecture/2026-09-19-profile-resolution-lookup-order` | How Node resolves modules inside a profile | not applicable | Exists only because of Node module resolution | none |
| `implemented/architecture/2026-09-22-fatal-diagnostics-and-crash-reports` | An uncaught error is fatal, reported once, with a bounded release (`:21`) | keep the first part | Boot owns the one exit path; A18 owns the diagnostic's content. The desktop report and the client batch retry have no subject ([DH-ADR-18](../design.md#dh-adr-18), [DH-ADR-3](../design.md#dh-adr-3)) | [DH-ADR-BOOT-6](#dh-adr-boot-6) |
| `implemented/architecture/2026-09-30-profile-package-refresh-and-manifest-invalidation` | Publish a new resolution generation and expire Node's package caches after a package operation | change | The goal (a running host follows adds and removals) is met by reconciling entries; there is no cache | [DH-ADR-BOOT-10](#dh-adr-boot-10) |
| `implemented/architecture/2026-10-01-replace-changed-package-modules-on-recomposition` | One exclusive recomposition path; evict changed modules | change | One path is kept. A replaced plugin directory takes effect by restarting that entry | [DH-ADR-BOOT-7](#dh-adr-boot-7) |
| `implemented/architecture/2026-10-14-registry-bundle-updates` | The host lists newer versions and owns update recovery | drop | No updates: the operator replaces a directory ([DH-ADR-13](../design.md#dh-adr-13)) | [DH-ADR-BOOT-11](#dh-adr-boot-11) |
| `implemented/bug-fix/2026-10-28-plugin-install-failure-diagnostics` | A failed installation always shows the host's diagnostic | keep the rule | Every management result carries its diagnostic; there is no stream to forget it in | [DH-ADR-BOOT-10](#dh-adr-boot-10) |
| `implemented/feature/2026-10-02-plugin-market-discover` | A safe reader for configured index URLs and a Discover page | drop | [DH-ADR-13](../design.md#dh-adr-13): no discovery. Its reader rules are recorded for a later registry | [DH-ADR-BOOT-11](#dh-adr-boot-11) |
| `implemented/testing/2026-09-27-observation-waits-on-observed-state` | Two flaky test cases wait on the state they assert | not applicable | Upstream test maintenance; the habit is in [DH-ADR-TOOLCHAIN-2](../upstream/toolchain.md#dh-adr-toolchain-2) | none |
| `implemented/testing/2026-09-27-plugin-manager-git-child-environment` | Tests remove Git's command-line configuration before starting Git | not applicable | Upstream test maintenance; the Go manager starts no Git | none |
| `proposed/simplification/2026-09-19-config-only-hmr` | Proposal: keep live config refresh, remove module replacement (`:15-17`) | adopt | This is where the Go design stands ([DH-ADR-LOADER-8](../libs/cordis-plugins.md#dh-adr-loader-8)) | [DH-ADR-BOOT-7](#dh-adr-boot-7) |
| `proposed/simplification/2026-09-19-retire-app-bin-boot-helpers` | Proposal: remove a second config-path resolver and a second `.env` loader (`:17`) | adopt | Boot has one way to locate configuration and one environment loader | [DH-ADR-BOOT-4](#dh-adr-boot-4) |

| Decision visible in code or README | Verdict | Reason | Go decision |
|---|---|---|---|
| A profile is an npm project in the home; a bundle is an npm package with a patch file (`docs/architecture.md:17-21`) | change | Bundles are layer files embedded in the binary; a profile is a Go value plus one layer file | [DH-ADR-BOOT-1](#dh-adr-boot-1) |
| A bundle that fails to resolve or load is skipped and reported (`packages/boot/app-boot/README.md:52`) | drop | An embedded bundle cannot be missing; a build test checks every bundle | [DH-ADR-BOOT-1](#dh-adr-boot-1) |
| Version ranges (`engines.dsh`, peers) and per-profile exemptions (`packages/boot/app-boot/README.md:54-58`) | drop | First-party components have the binary's version. A plugin's compatibility is the protocol version, checked by A22; there is no exemption to grant | [DH-ADR-BOOT-11](#dh-adr-boot-11) |
| Bundle `platforms` declaration (`packages/boot/app-boot/README.md:60`) | keep | A catalogue item and a bundle may name platforms; elsewhere they are reported as unavailable | [DH-ADR-BOOT-3](#dh-adr-boot-3) |
| An empty or comments-only patch file fails boot (`packages/boot/app-boot/README.md:65`) | change | An empty file is an empty layer; failing on it punishes the natural way to clear a file | [DH-ADR-BOOT-2](#dh-adr-boot-2) |
| The telemetry opt-out is a generated last patch (`packages/boot/app-boot/src/profile-context.ts:51-54`) | keep, generalised | The launcher layer; A18 decides what it holds | [DH-ADR-BOOT-2](#dh-adr-boot-2) |
| Reload waits for readiness; writes are debounced by a two-second stability window (`packages/boot/hmr/README.md:47`, `:61`) | keep the first, drop the second | The loader compares content, and a half-written file fails to parse and is retried | [DH-ADR-BOOT-7](#dh-adr-boot-7) |
| `sanitizeProfile` moves the profile patch aside for recovery (`packages/boot/app-boot/README.md:73`) | keep | As a command, `dsh profile reset` | [DH-D-BOOT-3](#dh-d-boot-3) |
| Management components are protected against removal (`packages/boot/plugin-manager/src/index.ts:1315`) | keep | Protected entries | [DH-ADR-BOOT-10](#dh-adr-boot-10) |
| Launcher facts are provided before the tree and are not config rows (`packages/boot/cmdline/README.md:86`) | keep | Launch data | [DH-ADR-BOOT-4](#dh-adr-boot-4) |

## 5. Tech debt and its resolution

57 units: 55 assigned and 2 found. They are grouped by cause; each unit's own reason is in its ledger row.

| Group (count) | Units | How the Go design answers it |
|---|---|---|
| Node module resolution and module replacement (18) | `app-boot.1` to `.4`; `hmr.1` to `.10`, `.12` to `.15` | **Not applicable.** They describe Node's resolvers, module caches, Workers and file-time comparison. Components are linked at build time and code is never replaced in process ([DH-ADR-4](../design.md#dh-adr-4), [DH-ADR-LOADER-8](../libs/cordis-plugins.md#dh-adr-loader-8)). `hmr.6` has a plugin-process analogue (a restarted plugin that fails stays failed), which A22 owns |
| pnpm, npm registries, Git transport, client and desktop (9) | `plugin-manager.1` to `.4`, `.6` to `.8`, `.13`, `.14` | **Not applicable.** No package manager runs and nothing is downloaded ([DH-ADR-13](../design.md#dh-adr-13)); there is no client plugin runtime and no desktop shell ([DH-ADR-3](../design.md#dh-adr-3), [DH-ADR-18](../design.md#dh-adr-18)) |
| Whole-config overrides (4) | `app-boot.7`, `cmdline.3`, `config-editor.2`, `config-editor.3` | **Resolved.** An edit is a `merge` operation holding only the edited fields, so untouched fields and their expressions stay as the lower layers give them; an edit over a computed field is refused unless asked for; an operation addresses an entry at any depth by its path id ([DH-ADR-BOOT-2](#dh-adr-boot-2), [DH-ADR-BOOT-7](#dh-adr-boot-7)) |
| Launcher and app wiring (3) | `cmdline.1`, `cmdline.2`, `app-boot.5` | **Resolved.** The grammar splits at the profile name ([DH-ADR-COMMANDER-2](../libs/commander.md#dh-adr-commander-2)); the catalogue declares what each component provides, so a composition with no provider for an injected service is reported at check time; a replay run is an explicit overlay, not a file-name swap ([DH-ADR-BOOT-3](#dh-adr-boot-3), [DH-ADR-BOOT-2](#dh-adr-boot-2)) |
| Change detection and outside edits (2) | `hmr.11`, `hmr.16` | **Resolved.** Layer files are compared by content on a poll that starts from the text read at boot ([DH-ADR-CHOKIDAR-2](../libs/chokidar.md#dh-adr-chokidar-2)); a plugin added by a hand edit arrives through the same reload |
| Package operations that are not atomic (4) | `plugin-manager.5`, `.9`, `.10`, `.12` | **Resolved.** A management operation is one locked write of one file followed by reconciliation. Nothing is downloaded; the one copy (A22's approved copy) is made before the write and is merely unreferenced if the write fails; a replaced plugin takes effect when it is added again, and a running plugin can be removed in any profile ([DH-ADR-BOOT-10](#dh-adr-boot-10)) |
| The plugin market (12) | `plugin-market.1` to `.12` | **Resolved by not building the feature**, not by a mechanism: the market is dropped by [DH-ADR-BOOT-11](#dh-adr-boot-11) under [DH-ADR-13](../design.md#dh-adr-13), so these limits have nothing left to apply to. Three are recorded as requirements for a later registry: signed index content (`.3`), a server-side query for large indexes (`.6`), and edits to a source list that do not overwrite each other (`.4`) |
| Found: discovered files leak into child processes (1) | `UP-DEBT-found.A21.1` | **Resolved.** The launch environment is a snapshot that is never written into the process environment ([DH-ADR-BOOT-4](#dh-adr-boot-4)) |
| Found: a tool that asks for its own approval (1) | `UP-DEBT-found.A21.2` | **Resolved.** The tool asks nothing and changes nothing durable; the one consent an attachment needs is an interaction asked by A22 ([DH-ADR-BOOT-10](#dh-adr-boot-10)) |
| Kept on purpose (3) | `app-boot.6`, `config-editor.1`, `plugin-manager.11` | **Carried**, each a deliberate boundary. The launch environment is read once and does not follow a workspace chosen later, because a fact that can change is no longer a launch fact. Only the profile layer is written by the harness ([DH-ADR-LOADER-4](../libs/cordis-loader.md#dh-adr-loader-4)); an edit the home layer or an overlay shadows is refused with that layer's name. The running manager refuses to disable protected entries and manages only its own profile; another profile is managed from the command line, and preset composition belongs to A27 |

## 6. Go design

### 6.1 Shape

Packages under `src/harness/internal/boot`:

| Package | Component and service | Why it exists |
|---|---|---|
| `catalog` | No component. `Catalog` implements the loader's `Resolver` | The one table from a name in configuration to a compiled-in component |
| `launch` | Created before the tree. Service keys `launch.Key` (`Data`), `launch.EnvKey` (`Env`), `launch.AppKey` (`App`) | Facts and controls that exist before any entry and that no entry may replace |
| `yamlx` | Library | The one YAML dialect and the comment-preserving document edit of [../libs/yaml.md](../libs/yaml.md) §6 |
| `expr` | Library: the `Evaluator` of [DH-D-LOADER-5](../libs/cordis-loader.md#dh-d-loader-5) | The scope of [DH-ADR-BOOT-5](#dh-adr-boot-5) is assembled here |
| `profile` | Library: profile definitions, embedded bundle files, the layer stack | What a profile is in Go |
| `boot` | `Run(ctx, Request) int`: the sequence, the audit, the fatal path, shutdown, the watchdog | The only place that orders start-up, the only owner of the root tree, and the only caller that returns the exit status |
| `config` | Component `boot.config`, key `config.Key`, interface `config.Service` | The one seam through which a running host previews and changes its configuration ([DH-ADR-BOOT-14](#dh-adr-boot-14)) |
| `reload` | Component `boot.reload` | Turns on layer watching after readiness; its absence means "apply at next start" |
| `plugins`, `plugins/tool` | Component `boot.plugins` (key `plugins.Key`, interface `plugins.Manager`); component `boot.plugins.tool` | The management service, and the model-facing tool as a separate component so a preset that omits it pays no prompt tokens |

The main types (`ctx` is a `context.Context`, first in every method):

```go
// catalog: every component of a build, declared in one list. No init-time registration.
type Item struct {
    Name      string            // "storage.kv": the name configuration uses
    Component *cordis.Component
    Config    reflect.Type      // nil when the component takes no config
    Provides  []string          // services it may provide
    View      reflect.Type      // data view offered to expressions as use.<service>; nil for none
    Platforms []string          // empty means every platform
}
func Build(sets ...[]Item) (*Catalog, error) // duplicate or malformed name, nil component: error

// launch: fixed before the tree exists.
type Data struct{ Profile, Home, Cwd, Version, OS, User string; Build BuildIdentity; Args []string; TTY, Safe bool; Overlays, Words []string }
type Env interface{ Lookup(name string) (value string, from Source, ok bool) } // Source: process | project-file | home-file | system (proxy names only, lowest precedence)
type Reason int // Completed | Interrupted | Terminated | InputClosed | ClientStalled | Requested | Fatal
type App interface {
    Ready() <-chan struct{}
    Stop(r Reason, status int)   // the stop request: the first call starts the bounded shutdown and fixes the status; later calls join it
    Stopping() <-chan struct{}   // closed when the first Stop is accepted, so a carrier can stop admitting work
}

// config: preview and change the running composition. Section 6.2, "The edit seam", states the contract.
type Service interface {
    Tree(ctx) (Tree, error)                           // entries, the supplying layer of each field, a revision per entry
    Preview(ctx, ops []Op) (Candidate, []Issue)       // compose and check; starts nothing, writes nothing
    Apply(ctx, ops []Op, at Revision) (Result, error) // lock, re-read, check, write the profile layer, reconcile, wait
    Status() Status                                   // last reload: changed ids, issues, stuck cleanups, unsaved edits
}
// Op: SetEnabled(id, on) | Merge(id, path, value, overwrite) | Reset(id, path) | Insert(parent, entry) | Remove(id) | Restart(id)
// Result: Saved bool; Application: applied | overridden(layer) | failed(issues) | unsaved(error); Revision per touched entry

// plugins: entries and local plugin directories. Inspection, the approved copy and the process are A22's plugin.Service.
type Manager interface {
    List(ctx) ([]Info, error)                      // id, component, state, supplying layer, protected, plugin facts; an optional bundle's group is marked and carries its title and description keys
    Inspect(ctx, dir string) (Inspection, error)   // A22's Inspect; reads files, runs no plugin code
    Add(ctx, dir, digest string, grants []string, o AddOptions) (Result, error) // owner only: the digest the report showed; approve, copy, one Insert
    SetEnabled(ctx, id string, on bool) (Result, error) // an entry or a group id, so an optional bundle is switched as one
    Restart(ctx, id string) (Result, error)
    Remove(ctx, id string, purge bool) (Result, error) // removes the entry; purge also removes the approved copy and state
}
```

Files this area owns, all under `<home>/config/` (the home layout as a whole is area A07's):

```
<home>/config/layer.yml                          home layer: written by a person, never by the harness
<home>/config/.env                               home environment file (optional)
<home>/config/profiles/<profile>/layer.yml       the profile layer: the one writable layer, owner-only
<home>/config/profiles/<profile>/layer.lock      lock file for read-merge-replace
<home>/config/profiles/<profile>/profile.json    a custom profile only: {"extends": "web"}; written once
<home>/config/profiles/<profile>/layer.yml.bak-<unix ms>   left by `dsh profile reset`
```

### 6.2 Contracts

**The layer stack.** Later rows win. Operations are those of [DH-ADR-LOADER-4](../libs/cordis-loader.md#dh-adr-loader-4): `insert`, override by id, `merge`, `remove`. An operation's id is the entry's path in the composed tree (`group:child`), so a nested entry is addressable at any depth; a preset that runs a tree of its own ([DH-D-LOADER-10](../libs/cordis-loader.md#dh-d-loader-10)) is addressed through that tree's layers, which A27 owns.

| Order | Layer | Source | Who writes it |
|---|---|---|---|
| 1 | Bundle layers, in the profile's order | Files embedded in the binary | Nobody at run time |
| 2 | **Profile layer (writable)** | `profiles/<profile>/layer.yml` | The harness, through `config.Service`; a person may also edit it |
| 3 | Home layer | `config/layer.yml` | A person only. It applies to every profile on the home |
| 4 | Overlays | Files named by `--layer`, in order (upstream `--patch`) | A person only |
| 5 | Launcher layer | Generated in memory at launch: the safe-mode switches ([DH-ADR-BOOT-12](#dh-adr-boot-12)) and what A18 forces for the one off-switch | The launcher |

**Where entries mount.** The loader mounts each top-level entry directly under its own fiber, which injects no service; a group injects only what its own entry states. Area A01 asked for a place where no ancestor injects a service for its scope component: that is a top-level entry of the base bundle, and `DH-T-BOOT-2` fails if a built-in bundle places it under a group that carries `inject`.

**The boot sequence.** `cmd/dsh` parses its own options (A24) and calls `boot.Run`.

| Step | What happens | What a failure looks like |
|---|---|---|
| 1. Launch facts | Resolve the home (`--home`, then `DSH_HOME`, then `homedir.Default` of A23), read the launch environment, build `launch.Data` | A discovered file that sets a launch-only name: one line on standard error naming the file, the variable and the remedy; status 78; nothing applied |
| 2. Guards | Install the fatal path and start the watchdog | none |
| 3. Layers | Resolve the profile, read and parse every layer, build the launcher layer | An unknown profile: status 5. A layer that is unreadable or does not parse: `file:line:column` and the message; status 78. When the profile layer is the one that fails, the message names `dsh profile reset <name>` |
| 4. Compose and check | Compose (pure) and run the static check of [DH-ADR-BOOT-7](#dh-adr-boot-7). The preview and check commands end here | At boot a static issue fails its own entry, not the launch; it is judged in step 7 |
| 5. Tree | Create the tree without the default console exporter; provide the launch services; mount logging ([DH-D-LOADER-12](../libs/cordis-plugins.md#dh-d-loader-12)) and the loader with the catalogue, the YAML codec and the evaluator | "host preparation failed" with the cause; status 70 |
| 6. Apply and settle | Apply the composition and wait until no entry is loading, at most `StartupTimeout` | An entry still loading at the deadline counts as not active in step 7 |
| 7. Audit | Every id in the profile's `Required` list must be present, enabled and active | A required entry is absent, disabled or not active: the tree is unloaded and one report is printed (below); status 78. Other inactive entries: one warning, start-up continues |
| 8. Ready | `App.Ready` closes. Carriers announce themselves; reload begins processing changes | none |
| 9. Run | Until the first `App.Stop`, or a fatal error | A fatal error: one diagnostic and report (A18's `diag.Fatal`), the release hook bounded by `FatalReleaseTimeout`, status 70 |
| 10. Shutdown | `App.Stopping` closes; stop admitting reloads and edits; unload the root, leaves first; wait at most `ShutdownTimeout`; return the status the stop request gave | The bound is exceeded: the watchdog's list of stuck cleanups is printed; status 124. A stop request with reason `Interrupted` made during shutdown ends the wait at once |

The start-up report goes to standard error only, never to standard output, which may carry a protocol. It groups entries that failed (id, component, `file:line:column` of the entry in its layer, the cause chain) apart from entries waiting for a service (which service, and whether any entry in the composition provides it), marks required entries, and names the full diagnostic file that A18 writes. Limits are fields of the profile definition: `StartupTimeout` 60 s, `ShutdownTimeout` 20 s, `FatalReleaseTimeout` 2 s (upstream's value), `CleanupWarnAfter` 10 s. Inference: the three new defaults are proposals. `ShutdownTimeout` is the one exit bound of the process: the stdio carriers (A20) and the command line (A24) keep no deadline of their own. Statuses are those of A24's closed table ([app-cli.md §6.2.4](app-cli.md)): 0 clean, or the status a stop request gave; 5 unknown profile; 70 fatal with a report; 78 configuration that cannot be composed or a required entry that failed; 124 forced after the shutdown bound.

**Stopping.** `launch.App.Stop` is the only way to end the process, and `boot.Run` is the only code that unloads the root tree. The callers are A24's `lifecycle` package (signals, the Ctrl-C confirmation, `dsh stop` through A18's control endpoint, the UI's stop action), A20's `wire.stdio` component (end of input, `shutdown`, a stalled client) and A24's headless runner (`Completed` with the run's status). None of them holds a timer, unloads anything or calls `os.Exit`. A carrier that must settle work before the unload (the final flush of [agent-loop.md](agent-loop.md), `DH-ADR-AGENT-8`) does it in its own cleanup, which the unload runs leaves first.

**Required entries.** Each built-in profile definition has a `Required` list, built from two parts so that every fact is written once: `profile.Spine`, the ids every launchable profile needs (`session.store`, `agent.registry`, `agent.driver`, `tools.registry`, `agent.presets`), and the profile's carrier set (for `web`: `host.listen`, `host.auth`, `host.assets`, `host.web`, `host.access`, `host.stream`, `ui.shell`; for `headless`: `cli.headless`; for `sdk` and `sdk-minimal`: `wire.stdio`, `wire.sdk`; for `acp`: `wire.stdio`, `wire.acp`). Area A27 owns the ids ([bundle-preset.md §6.2](bundle-preset.md)); this area owns the rule. A custom profile inherits its base profile's list and cannot shorten it. A layer operation that removes or disables a required id is a static issue, so a carrier cannot be configured away silently. Providers that a listed entry injects need no listing: their absence leaves the listed entry pending, which the audit reports with the missing service. The `admin` composition of A24's offline commands has an empty list: a command reports a missing service itself.

**Names.** One rule for every catalogue name, entry id and group id ([DH-ADR-BOOT-13](#dh-adr-boot-13)).

| Thing | Form | Examples | Not accepted |
|---|---|---|---|
| Component (catalogue name) | `<family>.<name>` or `<family>.<name>.<part>`: two or three segments joined by dots; each segment is lower-case letters and digits, with single hyphens between words; the first segment is a family registered once in the catalogue with its owning area | `session.store`, `host.web`, `storage.spill.policy`, `ui.remote-images`, `wire.sdk`, `x.session-share` | `agents`, `tools` (one segment); `agent-driver`, `sdk-server`, `title-llm` (a hyphen where the family dot belongs); `fsTools`, `fs_tools` |
| A provider behind a registry seam | `<family>.<seam>.<provider>` | `web.fetch.http`, `office.libreoffice` (seam and family coincide) | `deepseek-fetch-api` |
| A component that only registers model-facing tools | `<family>.tools`, or `tool.<name>` for a single tool | `fs.tools`, `tool.todo`, `boot.plugins.tool` | `tool-subagent`, `query-tools` |
| Entry id | The component name when the composition mounts it once; `<component>@<instance>` for a further mount | `storage.kv`, `tool.subagent@fork` | A free name unrelated to the component |
| Group id | One segment, or `x.<feature>` for an optional bundle's group | `plugins`, `x.worktree` | A name that is also a component |
| Path id of a nested entry | Group and entry ids joined by `:` | `plugins:plugin.entry@notes` | |

The service key of a component is a Go identifier (`kv.Key`) and is not a configuration name; a provider id inside a registry (`deepseek-fetch-api`) and an agent preset or connector name (`linear`) are registry values, not catalogue names. The prefix `x.` is reserved for experimental features and `ui.` for page components. The families registered so far are `boot`, `host`, `wire`, `cli`, `update`, `util`, `ui`, `x`; `agent`, `tools`, `tool`, `llm`, `session`, `storage`, `subagent`, `team`, `jobs`, `schedule`, `goal`, `plan`, `command`, `context`, `compact`, `skill`; `human`, `permission`, `plugin`, `mcp`, `lsp`, `web`, `office`, `browser`, `computer`, `program`, `workflow`, `subprocess`, `terminal`, `shell`, `shellexec`, `sandbox`, `fs`, `deliver`; `translate`, `title` (area X6, whose experimental components carry no `x.` prefix on purpose). An optional bundle's group and its components never share a name: the groups are `x.worktree`, `x.forge`, `x.devtools`, `x.timeline`, `x.session-recall`, `x.chat-import`, `x.reasoning-translation` and `x.feature-guide`, and their components are longer (`x.worktree.service`, `x.worktree.tools`). `catalog.Build` enforces the grammar and the family list, so a name that breaks the rule fails the build's own test before any layer file can use it.

**The edit seam.** `config.Service.Apply` is the only code that writes configuration at run time ([DH-ADR-BOOT-14](#dh-adr-boot-14)). The settings forms of area A07, the saved default model of area A02, the saved default agent preset of area A27, the plugin manager and the `dsh config` and `dsh plugin` commands all call it; none calls the loader's write-back or opens a layer file.

| Clause | Contract |
|---|---|
| Addressing | An operation names an entry by its path id and a field by a config path (`children.shell.timeout`). `Tree` returns, per entry, the effective value, the value without the profile layer, the supplying layer of each field and a revision; a form (A07's `Describe`) is a read of `Tree` |
| Operations | `Merge` writes only the named field; a `null` value is written as an explicit null, which clears the value a lower layer gave (A07's "clear"). `Reset` removes the profile layer's operation for the field (A07's "reset"), and an override that then only restates lower layers is deleted. `SetEnabled`, `Insert`, `Remove` and `Restart` act on whole entries; `Restart` writes nothing |
| Order of work | Take `layer.lock`; re-read every layer; build the candidate; compare `at` with the entry's current revision; run the static check; refuse a field a higher layer supplies; write the profile layer by rename; reconcile; wait for `loader.Reloaded`; release the lock after the write |
| A batch | The operations of one call are checked and written together or not at all. Reconciliation is still per entry ([§6.3](#63-ownership-failure-retry-rollback-and-cleanup)) |
| Computed fields | A `Merge` on a field whose lower-layer value is an `!expr` is refused with `config/computed` unless `overwrite` is set. A component that saves its own state passes the value it holds; `Apply` skips a field whose value still equals the evaluated expression |
| Live fields | Whether a change restarts the entry or reaches the running component through the loader's update hook ([DH-ADR-LOADER-7](../libs/cordis-loader.md#dh-adr-loader-7)) is decided by the config type's `live` tags (A23's `schema`), never by the caller |
| Preview | `Preview(ops)` returns the candidate and its issues without writing; each issue names an entry and a config path, so a form can mark the field, and the candidate lists the entries the operations would restart |
| Result | A stale revision is not a `Result`: `Apply` returns the error `config/stale` with the entry's current revision, and nothing was written or applied. Otherwise `Saved` and `Application` are separate facts, with the new revision of each touched entry. `applied`; `overridden(layer)`; `failed(issues)` when the write stands and an entry then failed to load; `unsaved(error)` when the change runs in memory only |
| Errors | `config/stale` (revision), `config/overridden` (names the layer), `config/invalid` (issues, each with a config path and a message; for a field that came from a file the layer codec maps the path to `file:line:column`, also for an issue a component's own validator raised), `config/computed`, `config/protected` (a required or management entry), `config/busy` (lock wait), `config/stopping` |
| Who may call | Any compiled-in component that injects `config.Key`. A plugin process cannot: no capability names it. A route that calls it is owner-only (A19's access rule) |

**What expressions may read.** The names are those of [DH-ADR-LOADER-3](../libs/cordis-loader.md#dh-adr-loader-3); the rule is [DH-ADR-BOOT-5](#dh-adr-boot-5).

| Name | Content |
|---|---|
| `launch.profile`, `launch.home`, `launch.cwd`, `launch.version`, `launch.os`, `launch.tty`, `launch.safe` | The fields of `launch.Data` except `Args`, `Words`, `Overlays`, `User` and `Build`. `launch.profile` is the profile's name. The app's words are parsed by the app's own start-up component and reach config only as that component's view |
| `env.NAME` | The launch environment. A name on A17's list of secret names is refused when the expression is checked |
| `use.<service>.<field>` | The `View` type the providing component declares in the catalogue: plain data fixed for the life of the provider. A component declares one only when a shipped layer reads it: in the first build that is A18's web launch values alone; the headless, SDK and ACP carriers declare none |

**Events.** Typed keys in the forms of [../design.md §4.6](../design.md#46-event-model).

| Go event | Form | Replaces | Payload and rule |
|---|---|---|---|
| `loader.Reloaded` ([DH-D-LOADER-11](../libs/cordis-plugins.md#dh-d-loader-11)) | notify | `UP-EVT-app-boot/config-reload`, `UP-EVT-hmr/reload` | Ids created, updated, restarted and removed, and the issues. Raised after every settled reconciliation, whatever caused it |
| `config.Rejected` | notify | none (upstream only logs) | Layer label, file, line, column, message. Raised once per distinct rejected content |
| `plugins.Changed` | notify | `UP-EVT-plugin-manager/changed` | The entry ids whose listing changed. Derived from `loader.Reloaded`, so a hand edit is announced too, which upstream does not do (`docs/subsystems/boot.md:592`) |
| `plugins.Operation` | notify | `UP-EVT-plugin-manager/operation` | Operation id, action, target id, phase (`start`, `end`), outcome. Raised on the manager's own goroutine after the caller has its result, so a listener never delays an operation. No directory path, grant list or error text |
| none | n/a | `UP-EVT-hmr/change` | Dropped: the host watches layer files only ([DH-ADR-BOOT-7](#dh-adr-boot-7)) |
| none | n/a | `UP-EVT-plugin-manager/install-log`, `UP-EVT-plugin-manager/install-state`, `UP-EVT-plugin-manager/update-state` | Dropped: no package run to stream, no phases, no update ([DH-ADR-BOOT-11](#dh-adr-boot-11)) |
| none | n/a | `UP-EVT-plugin-market/changed` | Dropped with the market |

**Config.** Go structs with defaults as a Go value, decoded strictly.

| Go struct | Replaces | Fields and defaults |
|---|---|---|
| `reload.Config` | `UP-CFG-dsh-hmr` | `Debounce` (100 ms, upstream's default). `base`, `root`, `ignored` and the watcher options are gone with module replacement. Disabling the entry is the "apply at next start" switch |
| `plugins.Config` | `UP-CFG-dsh-plugin-manager` | `LockWait` (10 s; upstream's 120 s covered a pnpm run), `InspectMaxBytes` (1 MiB). The other fourteen upstream fields configure pnpm, registries, Git probes and npm file hosts |
| none | `UP-CFG-dsh-plugin-market` | Dropped with the market |

**Services.**

| Go service | Replaces | Note |
|---|---|---|
| `launch.Key` (`launch.Data`), with `launch.EnvKey` and `launch.AppKey` | `UP-SVC-profileContext` (and upstream's `cmdlineArgs`, `appExit`, `appReady`) | Data only, provided before the tree. The package-manager invocation and installation anchor are gone |
| `config.Key` (`config.Service`) | `UP-SVC-configEditor` | Adds preview, a revision check, the shadow check and the cross-process lock. `Tree` is also the "loader view with layer attribution" that A18's inventory reads; A18 takes readiness from `App.Ready` and the safe-mode flag from `Data.Safe` |
| none: `config.Service`, `loader.Restart` and the layer source | `UP-SVC-hmr` | `runExclusive` becomes the layer source's serial loop; `watchConfig` becomes the layer watch; module methods have no subject |
| `plugins.Key` (`plugins.Manager`) | `UP-SVC-pluginManager` | Six methods. The package, registry, version, exemption and detail-document methods have no subject |
| none | `UP-SVC-pluginMarket` | Dropped |

**Tools exposed to the model.** One. `plugin_manager` keeps its name with four actions: `list` and `inspect`, which read, and `attach` and `detach`, which call A22's `plugin.Service.Attach` and `Detach` for the calling agent's own session (upstream: eight actions, among them `install_bundle` and two for version exemptions). The tool can change nothing that outlives its session: it has no action that adds, enables, disables, restarts or removes an entry of the profile. `attach` asks one consent of an owner on the session's bound channel, and what that consent covers is A22's rule ([extensions.md](extensions.md), `DH-ADR-EXT-8`); the attachment ends with the live session and is not restored on resume. A result that is not plainly done carries a `nextStep` sentence, which for a durable installation names the `dsh plugin add` command and the plugins page. `plugin_market` is not exposed: there is nothing for it to read.

**How a plugin is represented.** An installed plugin is an ordinary entry of A22's component `plugin.entry` that an `insert` operation in the profile layer places in the group `plugins`. Its component, its config struct, inspection, the digest and the approved copy are A22's and are used as written ([extensions.md](extensions.md), `DH-ADR-EXT-3`, `DH-D-EXT-1`): `Add` shows A22's inspection report, and on the owner's approval A22 copies the inspected bytes into the harness home and returns the digest; this area then applies one `Insert` holding the entry A22 defines (the approved copy, the digest, the granted capabilities, the trust level). `Add` carries the digest the report showed, and A22 refuses when the directory no longer matches it. What runs is the approved copy, so a later change to the operator's directory has no effect until the plugin is added again. The one exception is A22's: an owner may mark one installed entry `pinned: false` to run in place; it is shown as unpinned everywhere and is never available to an attached plugin. Enabling and disabling are the `disabled` field. Because a plugin is an entry, the fiber that owns its process is created, restarted and unloaded by the same reconciliation as everything else.

**Safe mode.** `dsh --safe <profile>` sets `launch.Data.Safe`, and the launcher layer then holds two kinds of operation: `disabled: true` on the group `plugins`, and `disabled: true` on the group of every optional bundle. Nothing else changes: layers are still read, and safe mode never loosens the sandbox, a grant, an approval or admission; senders and the sharing gate are not affected. An entry that starts operator-configured external software may carry `disabled: !expr launch.safe` in its shipped layer; that is its owner's choice, entry by entry, and the dump shows it. As their owners specify it: no plugin process starts and no plugin can be attached ([extensions.md](extensions.md)); every MCP server and language-server entry carries the switch by default ([mcp-lsp-web-connector.md](mcp-lsp-web-connector.md)); `office.libreoffice` carries none. The management surface keeps working: `dsh plugin list` and the plugins page show each such entry as "off for this launch (safe mode)", and an edit made in safe mode is saved to the profile layer and reported as `overridden(launcher)`, so it takes effect at the next ordinary start. Safe mode lasts for the process and is shown by `dsh status` and on every page (A25's product status).

### 6.3 Ownership, failure, retry, rollback and cleanup

| Resource | Class | Owner, and who may use it |
|---|---|---|
| The tree and every entry fiber | Created | `boot.Run` owns the root; the loader's fiber owns the entries. Unloading releases exactly these |
| The profile layer file | Shared with the person who edits it, and with other `dsh` processes on the profile | Never deleted. Written only under the lock; a hand edit wins a true conflict |
| The home layer, overlays, `.env` files | The operator's | Read only |
| `layer.lock` | Created | Held only for one read-merge-replace; a dead holder's kernel lock is released by the operating system |
| A plugin directory the operator placed | The operator's | Read for inspection and for A22's copy. Never changed or deleted |
| The approved copy, the plugin process and its state directory | Created, by A22's component | The entry's fiber owns the process; the copy and the state directory are removed only by an explicit purge. This area only causes the entry to exist |

| Situation | What happens | What is undone, and what stays uncertain |
|---|---|---|
| A layer file is edited into a state with a static issue | The edit is rejected as a whole; `config.Rejected` and the log name `file:line:column`; the running tree is untouched. The next change to the file is checked afresh | Nothing to undo |
| An edit passes the check and one entry then fails to load | That entry is failed and reported in `loader.Reloaded` and `Status`; siblings keep their new state. A reload is not a transaction | Nothing is rolled back; a corrected edit retries the entry |
| `Apply` cannot take the lock within `LockWait` | `config/busy`; nothing is written or applied | Nothing |
| `Apply` cannot write (read-only file, full disk) | The change is applied in memory, the result is `unsaved`, and `Status` keeps reporting it until a write succeeds | The caller knows the edit is not saved; it is lost if the process exits first |
| `Apply` is based on a stale revision, or a higher layer supplies the field | Refused: `config/stale`, or `overridden` naming the layer. Nothing is written | Nothing |
| The process dies between the write and the reconciliation | The file holds the edit; the next start, and every other host on the profile, applies it | Nothing is half-written: the write is a rename |
| Another process edits the profile layer | This host's poll sees new content and reloads like a hand edit. With reload off it keeps its composition until restart, or until its own next `Apply`, which re-reads every layer | Two hosts may differ for one poll interval |
| `Add` names a directory with a problem | Refused with the inspection's problem list; nothing is copied or written | Nothing |
| `Add` fails after A22 made the approved copy | The entry is not written; the copy is unreferenced and A07's retention removes it | Nothing runs. **Uncertain** until retention runs: the unreferenced copy on disk |
| `Remove` or disable of an entry whose cleanup blocks | The file is saved; the entry stays unloading; the watchdog names the effect; the result is `failed` with that name | **Uncertain:** the resource behind the stuck cleanup. A restart ends it |
| A cleanup does not return during shutdown | After `ShutdownTimeout` the list of stuck cleanups is printed and the process exits with status 124 | **Uncertain:** whatever those cleanups would have released. The operating system reclaims process-owned resources; child processes are A09's tree owners to end |
| A panic in a harness goroutine, or an unrecoverable error | The one fatal path: diagnostic and report, bounded release, status 70. The failed operation is never resumed | Sessions are recovered at the next start by the session store (A05) |

## 7. Decisions

<a id="dh-adr-boot-1"></a>
### DH-ADR-BOOT-1 A profile is a compiled-in definition plus one layer file; bundles are embedded layers
- **Decision.** A built-in profile (`web`, `headless`, `sdk`, `sdk-minimal`, `acp`) is a Go value: name, ordered bundle names and limits. A bundle is a named, ordered set of layer files embedded in the binary. There is no bundle named `sdk-minimal`: that profile stacks the bundles `core`, `minimal` and `sdk` ([bundle-preset.md §6.2](bundle-preset.md)). A custom profile is a directory whose `profile.json` names the built-in profile it extends; its own content is its layer, and it inherits the base profile's `Required` list. An optional bundle may only insert entries, and inserts them inside one group that is disabled by default, so turning a bundle on or off is an ordinary `disabled` override on that group.
- **Why.** Upstream stores a profile as an npm project because bundles are npm packages. With components compiled in ([DH-ADR-4](../design.md#dh-adr-4)), a bundle is configuration and nothing else. Making an optional bundle a group removes a second writable file (the bundle list) and upstream's rule that enabling a bundle appends it and may change precedence.
- **Tradeoff.** A new bundle needs a new binary. An optional bundle cannot override another bundle's entry. A profile's bundle order cannot be changed at run time.
- **Serves.** [DH-O-2](../design.md#dh-o-2). **Verified by.** `DH-T-BOOT-2`, `DH-T-BOOT-7`.

<a id="dh-adr-boot-2"></a>
### DH-ADR-BOOT-2 The layer order is fixed, and the profile layer is the one writable layer
- **Decision.** The stack is bundles, profile layer, home layer, overlays, launcher layer. The harness writes only the profile layer, also when an overlay is present. A missing or empty layer file is an empty layer. An override that targets an entry at any depth uses its path id.
- **Why.** [DH-ADR-LOADER-4](../libs/cordis-loader.md#dh-adr-loader-4) requires exactly one writable layer; this fixes which. The home layer outranks the profile layer because it is the operator's statement for every profile. Area A07 asked which layer is writable under an overlay and whether nested entries are addressable: the profile layer, and yes.
- **Tradeoff.** A run-time edit cannot win against the home layer or an overlay. It is refused with the layer's name, so the person knows which file to edit.
- **Serves.** [DH-O-2](../design.md#dh-o-2). **Verified by.** `DH-T-BOOT-7`, `DH-T-BOOT-16`.

<a id="dh-adr-boot-3"></a>
### DH-ADR-BOOT-3 Components are declared in an explicit build catalogue
- **Decision.** Each package family exports a function that returns its `catalog.Item` values. One file lists the families, and `catalog.Build` makes the immutable catalogue that the loader uses as its `Resolver`. The harness does not call `loader.Register` and registers nothing from `init`. An item declares its config type, the services it may provide, its expression view and its platforms. The generated component catalogue is rendered from it.
- **Why.** [AR-ADR-16](../../architecture.md#ar-adr-16) resolves names from a registration table; the port's default table is a process-wide variable filled by `init` (`src/go/loader/resolver.go:24-42`). A component whose package nobody imports would then vanish silently, two tests in one process could not build different catalogues, and "what does this build contain" would have no reviewable answer. Declared provides and views let the static check find a missing provider and a mistyped `use.` field before anything starts.
- **Tradeoff.** One list that every area touches. A declared `Provides` can drift from what a component does; `DH-T-BOOT-2` compares them.
- **Serves.** [DH-O-1](../design.md#dh-o-1), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-BOOT-1`, `DH-T-BOOT-2`, `DH-T-BOOT-3`.

<a id="dh-adr-boot-4"></a>
### DH-ADR-BOOT-4 Launch facts are frozen before the tree, and a discovered file cannot change how the harness starts
- **Decision.** Before any entry exists the launcher builds `launch.Data` and the launch environment: the process environment, then the working directory's `.env`, then the home's `.env`. A file may not set a launch-only name (upstream's list by function: process launch, ambient programs, pre-run code, the `DSH_` namespace, home and proxy and endpoint routing); proxy and endpoint names are accepted from the home file only. A violation stops the launch before anything is applied. The snapshot is never written into the process environment. There is one way to locate configuration (the home and the profile name) and one environment loader. One further source sits below all three and supplies proxy names only: the static proxy setting of the operating system, read once at launch by A23's `sysproxy` reader within a two-second bound (debt entry U-K2-2; [util-test-support.md](util-test-support.md), `DH-ADR-UTIL-10`). It is frozen with the other launch facts, and the launch diagnostics show each proxy value with its source. A reader that fails, times out or does not exist on a platform yields nothing and one diagnostic line; it never stops a launch.
- **Why.** Keeps upstream's rule and removes its stated residue: discovered values reaching every child process (`UP-DEBT-found.A21.1`). Area A09 builds each child environment explicitly, so nothing depends on the leak.
- **Tradeoff.** A project's `.env` no longer reaches commands the agent runs unless the execution area passes named values on. The environment is read once: it does not follow a workspace chosen later (`app-boot.6`, carried).
- **Serves.** [DH-O-2](../design.md#dh-o-2), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-BOOT-4`, `DH-T-BOOT-5`.

<a id="dh-adr-boot-5"></a>
### DH-ADR-BOOT-5 Expressions read only frozen, declared, secret-free data
- **Decision.** An expression may read a value only if (a) it is fixed before the reading entry loads, (b) it cannot change while that entry's fiber lives unless the fiber is reloaded, (c) its type is plain data with no secret, and (d) its shape is declared, so a wrong name fails when the expression is checked. `launch.*` meets this by construction. A service offers `use.<service>` only by declaring a `View` struct in its catalogue item and computing the value once in its constructor; a test rejects a view type that contains a secret, a pointer, a function or an interface. `env.NAME` refuses secret names. A config field whose type is a credential reference accepts no expression.
- **Why.** This answers the question the loader specification left to this area ([../libs/cordis-loader.md](../libs/cordis-loader.md) §7, item 2). Without (b) a value could differ from what the dump showed; without (c) a secret would be copied into config values; without (d) a typo would surface only at load.
- **Tradeoff.** A component cannot expose live state to configuration; a consumer that needs live state injects the service and calls it. Each new view is a visible catalogue change.
- **Serves.** [DH-O-2](../design.md#dh-o-2). **Verified by.** `DH-T-BOOT-6`.

<a id="dh-adr-boot-6"></a>
### DH-ADR-BOOT-6 Start-up is best effort with one required list, one readiness signal, one fatal path and a bounded shutdown
- **Decision.** Every entry that can load does. The profile definition's `Required` list decides which failures stop start-up; a required entry must be present, enabled and active, and the audit runs once, at boot. Readiness is committed only after the audit. `boot.Run` owns the root tree and returns the exit status; everything else asks through `launch.App.Stop`, which is the one stop function of the product. A fatal error takes one path with a bounded release. Shutdown waits `ShutdownTimeout`, the one exit bound, and then exits with status 124.
- **Why.** Upstream's policy, kept, with one change that fails closed: upstream ignores a required id that a composition lacks, which lets a profile lose its carrier and still report success. Three specifications had each written a stop path (boot's exit request, the command line's stop function, the stdio carrier's exit controller) with two deadlines; one owner of the tree can have only one. Inference: the settle deadline and the shutdown bound are added because Go components load on goroutines that can block forever, and a process a supervisor cannot stop is worse than one that reports what it abandoned.
- **Tradeoff.** A renamed required entry must be renamed in the list (a test checks that every listed id exists in its profile's shipped layers). A custom profile cannot drop a carrier. A forced exit may skip cleanups; they are named.
- **Serves.** [DH-O-1](../design.md#dh-o-1), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-BOOT-10` to `DH-T-BOOT-13`, [DH-E-BOOT-1](#dh-e-boot-1).

<a id="dh-adr-boot-7"></a>
### DH-ADR-BOOT-7 A change is checked as a whole before anything is applied
- **Decision.** Every change, from a file or from `config.Service`, passes the same static check on the candidate: each changed entry's component resolves in the catalogue; its literal config decodes strictly into the component's config type on top of the defaults and passes validation; its expressions parse and type-check; each injected service has a provider declared in the composition. Any static issue in a reload rejects the whole edit. Load failures stay per entry. A write never replaces an expression silently: an edit to a computed field is refused unless the operation says to overwrite it, and a component saving its own config does not write a field whose value still equals the evaluated expression. The host watches layer files only; there is no general file-change event and no code reload.
- **Why.** [DH-O-2](../design.md#dh-o-2) promises that a bad edit leaves the running tree unchanged. Upstream applies sibling changes and leaves the bad entry on its old instance, which is a state no file describes. The expression rule answers the loader specification's open question 7.
- **Tradeoff.** One typo holds back the unrelated changes saved with it. Entries whose config holds expressions are only partly checked before load. At boot there is no previous composition to keep, so there a static issue fails its entry and the required list decides.
- **Serves.** [DH-O-2](../design.md#dh-o-2). **Verified by.** `DH-T-BOOT-15`, `DH-T-BOOT-16`, `DH-T-BOOT-18`, [DH-E-LOADER-1](../libs/cordis-plugins.md#dh-e-loader-1).

<a id="dh-adr-boot-8"></a>
### DH-ADR-BOOT-8 Hosts that share a profile coordinate through the file and a lock, not through each other
- **Decision.** The read-merge-replace of the profile layer runs under an exclusive lock on `layer.lock`, taken with area A08's `fsx` lock and a bounded wait. Other hosts learn of a change from their own poll of the file. There is no message between hosts and no cross-process transaction. Hosts on different profiles share only the home layer, which no host writes.
- **Why.** [DH-ADR-12](../design.md#dh-adr-12) lets several processes open one home. The loader's "never replace a file that changed since it was read" check leaves a window between the comparison and the rename in which two processes can both pass; the lock closes it. Area A07 asked for this lock.
- **Tradeoff.** For up to one poll interval two hosts run different compositions. A host with reload disabled lags until restart or its own next edit. An edit made in one host changes what every session on that profile sees, in every host.
- **Serves.** [DH-O-2](../design.md#dh-o-2). **Verified by.** `DH-T-BOOT-17`, [DH-E-BOOT-2](#dh-e-boot-2).

<a id="dh-adr-boot-9"></a>
### DH-ADR-BOOT-9 A cleanup that does not return is named, never force-released
- **Decision.** A watchdog owned by `boot.Run`, outside the tree, records the start and end of every effect cleanup. One that runs longer than `CleanupWarnAfter` is reported with its entry id, component name and effect label, and again at doubling intervals. At run time the fiber stays unloading and `config.Status` lists it. At shutdown the bound applies and the list is printed before exit.
- **Why.** [../libs/cordis.md](../libs/cordis.md) §7 leaves this to the boot area. Go cannot stop a goroutine, and running later cleanups past a stuck one would release a provider before its dependant, which [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1) forbids.
- **Tradeoff.** A stuck cleanup still blocks its fiber and that fiber's providers until restart. The watchdog depends on the cleanup observer of [DH-D-CORDIS-10](../libs/cordis.md#dh-d-cordis-10) to name the effect and not only the fiber.
- **Serves.** [DH-O-1](../design.md#dh-o-1), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-BOOT-19`, [DH-E-BOOT-4](#dh-e-boot-4).

<a id="dh-adr-boot-10"></a>
### DH-ADR-BOOT-10 The plugin manager manages entries and local directories for an owner; the model's tool can only attach to its own session
- **Decision.** The manager lists entries, inspects a directory, adds a plugin (A22 approves and copies; this area writes one entry), enables, disables, restarts and removes. Each change is one `config.Service.Apply`. The command line and the web page call the same service, as an owner. Protected entries are refused. The model-facing tool reads through the same service and changes nothing in the profile: its only acting calls are A22's session-scoped `Attach` and `Detach`. The tool asks nothing itself; the one consent an attachment needs is asked by A22 through the interaction registry. The manager downloads nothing and never changes the operator's directory.
- **Why.** [DH-ADR-13](../design.md#dh-adr-13): the operator places a directory. Area A22 owns manifests and grants, and its rule is that a grant binds to an approved copy and that no tool call can make a plugin durable or host-wide ([extensions.md](extensions.md), `DH-ADR-EXT-3`, `DH-ADR-EXT-8`); this record conforms to it. That rule is also what [DH-ADR-16](../design.md#dh-adr-16) requires: consent given at a prompt must not outlive the session, and a profile edit outlives every session. Upstream's approval inside the tool body is `UP-DEBT-found.A21.2`.
- **Tradeoff.** An agent cannot enable, disable or restart a profile entry, which upstream's Creator flow could; it tells the person which command or page does it. The owner decided this ([DH-ADR-30](../design.md#dh-adr-30)): session attachment only, and making a plugin or a setting durable is an owner action. A useful model-written plugin is attached again in each session until an owner installs it. In `danger-full-access` a command can still edit the layer file directly, as it can any file.
- **Serves.** [DH-O-11](../design.md#dh-o-11), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-BOOT-20`, `DH-T-BOOT-21`, `DH-T-BOOT-24`, [DH-E-BOOT-3](#dh-e-boot-3), [DH-E-BOOT-5](#dh-e-boot-5).

<a id="dh-adr-boot-11"></a>
### DH-ADR-BOOT-11 The plugin market, registries, updates and version exemptions are not built
- **Decision.** No index reader, no `plugin_market` tool, no registry plan, no update operation, no version exemptions. Replacing a plugin is: replace the directory, inspect, grant, restart. Recorded for a later registry design: fetch only configured sources and refuse redirects; never fall back from a private source to a public one; signed index content; queries answered by the server; source-list edits that do not overwrite each other.
- **Why.** [DH-ADR-13](../design.md#dh-adr-13) decides it. Exemptions existed to override npm version ranges; a plugin's compatibility is its protocol version, which either matches or does not.
- **Tradeoff.** No discovery and no update notice. A recorded exception to [DH-ADR-1](../design.md#dh-adr-1) under DH-ADR-13.
- **Serves.** [DH-O-11](../design.md#dh-o-11), [DH-O-16](../design.md#dh-o-16). **Verified by.** `DH-T-BOOT-21`; the ledger rows for the market units link here.

<a id="dh-adr-boot-12"></a>
### DH-ADR-BOOT-12 Safe mode switches off plugin processes and optional bundles through the launcher layer, and nothing else
- **Decision.** `--safe` is a launcher option of the profile launchers. It adds two kinds of operation to the launcher layer, `disabled: true` on the `plugins` group and on every optional bundle's group, and sets `launch.safe` for expressions. It reads the same layer files, never loosens the sandbox, a grant, an approval or admission, deletes nothing, and lasts for one process. Fixed commands accept and ignore it, because the `admin` composition starts none of these. A profile layer that does not parse is not a safe-mode case: the launch fails with status 78 and names `dsh profile reset`.
- **Why.** Three specifications depend on the option (the unclean-exit hint of A18 and A24, the status line of A25) and none defined it. What a person needs after a crash is a host that starts with only the code the release tested: plugin processes are third-party or model-written, and optional bundles are the experimental features. The launcher layer outranks every file, so no edit can defeat the switch, and the dump shows exactly what it turned off.
- **Tradeoff.** Recommended default, not confirmed by the owner: how much safe mode switches off is a product choice. An operator-configured program starts unless its owner's entry reads `launch.safe`; MCP and language servers do read it, LibreOffice does not. A failure inside a first-party entry is not helped by safe mode; the report and `dsh profile reset` are the tools for that. An edit made in safe mode takes effect only at the next ordinary start.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-11](../design.md#dh-o-11). **Verified by.** `DH-T-BOOT-23`, [DH-E-CLI-1](app-cli.md#dh-e-cli-1).

<a id="dh-adr-boot-13"></a>
### DH-ADR-BOOT-13 One naming rule for components, entries and groups, enforced by the catalogue
- **Decision.** The table "Names" in [§6.2](#62-contracts): `<family>.<name>[.<part>]` in lower case with hyphens only inside a segment; a registered family list; `@<instance>` for a further mount; group ids of one segment or `x.<feature>`. `catalog.Build` rejects any other name.
- **Why.** The area specifications were written in parallel and use three styles (`agent-driver`, `tools`, `host.web`). A name is typed by operators in layer files, shown in the dump, the inventory and error reports, and matched by the required list; two styles make every one of those a lookup. The dotted form is the majority and reads as "family, then thing".
- **Tradeoff.** About sixty names in other areas change before any layer file is written (requested of their owners). The family list is one more thing a new area touches.
- **Serves.** [DH-O-2](../design.md#dh-o-2), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-BOOT-1`, `DH-T-BOOT-2`.

<a id="dh-adr-boot-14"></a>
### DH-ADR-BOOT-14 `config.Service.Apply` is the only run-time writer of configuration
- **Decision.** Every change a running host makes to configuration is a call to `Apply` with the contract of "The edit seam" in [§6.2](#62-contracts). The loader's write-back and the layer source's write guard are called from package `config` only; a source check fails the build when another package does.
- **Why.** Area A07's settings forms and area A02's saved default model each described their own write to the profile layer. Two writers would mean two locks or none, two static checks, and two answers to "a higher layer shadows this field". [DH-O-2](../design.md#dh-o-2) promises one behaviour for a bad edit.
- **Tradeoff.** A component that only wants to remember one value pays for a candidate composition and a file lock; such values that are not configuration belong in A07's key-value tables instead.
- **Serves.** [DH-O-2](../design.md#dh-o-2). **Verified by.** `DH-T-BOOT-16`, `DH-T-BOOT-22`.

## 8. Deliverables

<a id="dh-d-boot-1"></a>
### DH-D-BOOT-1 A build knows its components by name, and says so
- **Result and why it matters.** Configuration can name exactly the components in the binary, a wrong name is reported with its position, and a generated catalogue lists what a build contains.
- **How it is implemented.** `catalog.Build` validates and freezes the items (name grammar `family.name`, no duplicates, no nil component, config and view types present where declared) and implements `Resolve`, telling "unknown" apart from "not on this platform". A generator renders the component catalogue from the built catalogue and the built-in profiles.
- **Components and files.** `internal/boot/catalog`; the family list beside `cmd/dsh`.
- **How names are checked.** `Build` applies the grammar and the family list of [DH-ADR-BOOT-13](#dh-adr-boot-13); a family is registered once, with its owning area, in the same file that lists the families.
- **Depends on.** [DH-D-LOADER-3](../libs/cordis-loader.md#dh-d-loader-3), [DH-D-UTIL-1](util-test-support.md#dh-d-util-1) (`schema` for config types). Every area contributes its items.
- **Priority.** P0. **Outcome.** DH-O-1, DH-O-2, DH-O-14. **Done when.** `DH-T-BOOT-1`, `DH-T-BOOT-2`, `DH-T-BOOT-3`, `DH-T-BOOT-22` pass.

<a id="dh-d-boot-2"></a>
### DH-D-BOOT-2 Launch facts, the launch environment and the expression scope
- **Result and why it matters.** Every component and expression reads the same frozen facts, a value's origin can be shown, and a file in a cloned repository cannot redirect the harness.
- **How it is implemented.** Pure functions build `Data` and `Env` from the launcher's request; a rejected file aborts before anything is used. `Env` also offers a view limited to the process and home-file sources, which A23's `egress` uses to resolve proxy settings, so a project file can never supply one. `App` is a small state machine (starting, ready, stopping) whose `Stop` keeps the first reason and status and lets later calls join. The `expr` package builds the evaluator's typed environment from `Data`, `Env` and the catalogue's views, so unknown names fail at check time.
- **Components and files.** `internal/boot/launch`, `internal/boot/expr`.
- **Depends on.** [DH-D-BOOT-1](#dh-d-boot-1), [DH-D-LOADER-4](../libs/cordis-loader.md#dh-d-loader-4), [DH-D-LOADER-5](../libs/cordis-loader.md#dh-d-loader-5), [DH-D-CORDIS-5](../libs/cordis.md#dh-d-cordis-5), [DH-D-UTIL-5](util-test-support.md#dh-d-util-5) (`homedir`, the default home location), [DH-D-HUMAN-7](interaction-credentials.md#dh-d-human-7) (the secret-name predicate). Consumed by [DH-D-CLI-1](app-cli.md#dh-d-cli-1), whose `cmdline` package parses `Data.Words`, and by [DH-D-CLI-4](app-cli.md#dh-d-cli-4) and [DH-D-SDK-3](sdk-acp-typert.md#dh-d-sdk-3), which call `App.Stop`.
- **Priority.** P0. **Outcome.** DH-O-2. **Done when.** `DH-T-BOOT-4`, `DH-T-BOOT-5`, `DH-T-BOOT-6` pass.

<a id="dh-d-boot-3"></a>
### DH-D-BOOT-3 Profiles, embedded bundles and the layer stack
- **Result and why it matters.** `dsh web` and `dsh headless` are the same binary with different compositions, and a person changes either without touching shipped files.
- **How it is implemented.** Profile definitions are Go values; bundle files are embedded with `embed.FS` and parsed by the YAML codec like any layer. `profile.Stack` returns the ordered `loader.Layer` list of §6.2 with the profile layer marked writable and the launcher layer last; a definition carries its bundle names, limits and `Required` list. `dsh profile new <name> --from <shipped>` writes `profile.json` and an empty layer and refuses a reserved word; `dsh profile reset <name>` moves the layer aside under a unique name and touches nothing else.
- **Components and files.** `internal/boot/profile`, with bundle files beside the components they mount.
- **Depends on.** [DH-D-BOOT-1](#dh-d-boot-1), [DH-D-BOOT-4](#dh-d-boot-4), [DH-D-LOADER-6](../libs/cordis-loader.md#dh-d-loader-6). Consumed by [DH-D-BUNDLE-1](bundle-preset.md#dh-d-bundle-1), which fills the definitions with bundle content and the required ids, and by [DH-D-CLI-1](app-cli.md#dh-d-cli-1), whose launcher reads `--layer` and `--safe`. The commands `dsh profile new` and `dsh profile reset` are contributed by this deliverable as `cmdline.Command` values and run in the admin tree of [DH-D-CLI-9](app-cli.md#dh-d-cli-9), which consumes this deliverable.
- **Priority.** P0. **Outcome.** DH-O-2. **Done when.** `DH-T-BOOT-2`, `DH-T-BOOT-7`, `DH-T-BOOT-8`, `DH-T-BOOT-23` pass.

<a id="dh-d-boot-4"></a>
### DH-D-BOOT-4 `yamlx`: one YAML dialect, with edits that keep comments
- **Result and why it matters.** Layer files, skill frontmatter and any other YAML are read under one strict dialect with positions, and a run-time write keeps what a person wrote.
- **How it is implemented.** The design of [../libs/yaml.md](../libs/yaml.md) §6 as written: parse to the module's node tree, walk it to reject anchors, aliases, merge keys, duplicate keys and unregistered tags, type scalars by the YAML 1.2 core rules, and build values that carry positions. `Edit` copies the tree, applies operations (insert, set, delete field, delete entry, at any path), renders the canonical layout, re-parses the result and refuses the write if data or a comment differs. The loader's YAML codec ([DH-D-LOADER-2](../libs/cordis-loader.md#dh-d-loader-2)) is a thin adapter over this package.
- **Components and files.** `internal/boot/yamlx`; module `go.yaml.in/yaml/v4` (`DH-LIB-YAML`).
- **Depends on.** `DH-LIB-YAML`, [DH-D-LOADER-1](../libs/cordis-loader.md#dh-d-loader-1).
- **Priority.** P0. **Outcome.** DH-O-2. **Done when.** `DH-T-BOOT-9` and the library specification's `DH-T-YAML-1`, `DH-T-YAML-2`, `DH-T-YAML-3`, `DH-T-YAML-4`, `DH-T-YAML-5`, `DH-T-YAML-6` and `DH-T-YAML-7` pass.

<a id="dh-d-boot-5"></a>
### DH-D-BOOT-5 The boot sequence, the audit, readiness, the fatal path and shutdown
- **Result and why it matters.** A launch either reaches readiness or ends with one report that names what failed and where it is configured, and a running harness always stops within a bound.
- **How it is implemented.** `boot.Run` is the ten steps of §6.2 as one function that returns the exit code. The audit reads entry and fiber state through the loader ([DH-D-LOADER-8](../libs/cordis-loader.md#dh-d-loader-8)) and an exporter that outlives the root's unload collects warnings and errors for the report. The first `App.Stop` starts shutdown, which disposes the root and waits on the watchdog's deadline; `Run` installs no signal handler of its own, because signals are A24's and arrive as stop requests.
- **Components and files.** `internal/boot` (`run.go`, `audit.go`, `report.go`, `fatal.go`, `shutdown.go`).
- **Depends on.** [DH-D-BOOT-1](#dh-d-boot-1), [DH-D-BOOT-2](#dh-d-boot-2), [DH-D-BOOT-3](#dh-d-boot-3), [DH-D-BOOT-4](#dh-d-boot-4), [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1), [DH-D-CORDIS-2](../libs/cordis.md#dh-d-cordis-2), [DH-D-LOADER-7](../libs/cordis-loader.md#dh-d-loader-7), [DH-D-LOADER-12](../libs/cordis-plugins.md#dh-d-loader-12). [DH-D-HOST-7](host-telemetry.md#dh-d-host-7) (`diag.Fatal` and the report file), [DH-D-CORDIS-9](../libs/cordis.md#dh-d-cordis-9) (a tree created with chosen exporters: boot passes its own), [DH-D-CLI-3](app-cli.md#dh-d-cli-3) (the exit-status table `Run` returns from).
- **Priority.** P0. **Outcome.** DH-O-1, DH-O-14. **Done when.** `DH-T-BOOT-10`, `DH-T-BOOT-11`, `DH-T-BOOT-12`, `DH-T-BOOT-13` and `DH-T-BOOT-22` pass and [DH-E-BOOT-1](#dh-e-boot-1) has passed.

<a id="dh-d-boot-6"></a>
### DH-D-BOOT-6 The composed tree can be previewed, explained and checked without starting anything
- **Result and why it matters.** An operator sees exactly what would run and which layer supplied each value, and a script can validate configuration before a deploy.
- **How it is implemented.** Three read-only operations over steps 1 to 4 of the sequence: *dump* prints the composed tree as one loadable YAML document, expressions in source form, each entry and overridden field annotated with its supplying layer, and unmatched operations listed with their layer label; *check* prints the static issues and exits non-zero if there are any; *schema* prints the JSON Schema of an entry list derived from the catalogue's config types. A variant leaves the profile layer out (upstream `--dump-default-config`). The dump is a diagnostic, not a storage format; its JSON form is deterministic.
- **Components and files.** `internal/boot/config` (`dump.go`, `check.go`).
- **Depends on.** [DH-D-BOOT-3](#dh-d-boot-3), [DH-D-LOADER-6](../libs/cordis-loader.md#dh-d-loader-6), [DH-D-UTIL-1](util-test-support.md#dh-d-util-1) (schema derivation), [DH-D-CLI-9](app-cli.md#dh-d-cli-9) (the admin tree the commands run in). The commands are `dsh config show` (`--defaults` for the variant), `dsh config validate`, `dsh config schema` and `dsh config path` ([app-cli.md §6.2.2](app-cli.md)), contributed by this area as `cmdline.Command` values.
- **Priority.** P1. **Outcome.** DH-O-2, DH-O-14. **Done when.** `DH-T-BOOT-14` passes.

<a id="dh-d-boot-7"></a>
### DH-D-BOOT-7 Validated reload and one edit path, safe across processes
- **Result and why it matters.** An edit to a layer file or through the harness restarts only the affected entries; a bad edit changes nothing and says where it is wrong; two processes never lose each other's edit.
- **How it is implemented.** The static check of [DH-ADR-BOOT-7](#dh-adr-boot-7) is one function used by boot, by preview and as a gate in front of the layer source's apply step. `config.Service.Apply` takes the lock, has the layer source re-read all layers, builds the candidate, checks the revision and the ownership index, writes operations to the profile layer, reconciles and waits for `loader.Reloaded`. `reload` starts the layer watch after `App.Ready` and stops it first at shutdown. The settings forms ([DH-D-STORAGE-8](storage.md#dh-d-storage-8)), the saved default model ([DH-D-AGENT-8](agent-loop.md#dh-d-agent-8)) and the saved default agent preset ([DH-D-BUNDLE-4](bundle-preset.md#dh-d-bundle-4)) are callers of `Apply` ([DH-ADR-BOOT-14](#dh-adr-boot-14)).
- **Components and files.** `internal/boot/config`, `internal/boot/reload`.
- **Depends on.** [DH-D-BOOT-5](#dh-d-boot-5), [DH-D-LOADER-7](../libs/cordis-loader.md#dh-d-loader-7), [DH-D-LOADER-8](../libs/cordis-loader.md#dh-d-loader-8), [DH-D-LOADER-11](../libs/cordis-plugins.md#dh-d-loader-11), `DH-LIB-NODEFS` and [DH-D-FS-1](filesystem.md#dh-d-fs-1) (`fsx` locks), [DH-D-UTIL-1](util-test-support.md#dh-d-util-1) (strict decoding with positions, the `live` tag), [DH-D-LOADER-13](../libs/cordis-loader.md#dh-d-loader-13) (`WithWriteGuard` on the layer source, which brackets read-merge-replace with the lock).
- **Priority.** P1. **Outcome.** DH-O-2. **Done when.** `DH-T-BOOT-15`, `DH-T-BOOT-16`, `DH-T-BOOT-17` and `DH-T-BOOT-18` pass, and [DH-E-BOOT-2](#dh-e-boot-2) and [DH-E-LOADER-1](../libs/cordis-plugins.md#dh-e-loader-1) have passed. DH-E-BOOT-2 rewrites a shared profile layer, so it runs in this deliverable's build stage, not in a later acceptance batch.

<a id="dh-d-boot-8"></a>
### DH-D-BOOT-8 A watchdog names a cleanup that does not return
- **Result and why it matters.** "The harness hangs on exit" becomes "the cleanup `http listener` of entry `http` has not returned for 30 s".
- **How it is implemented.** A table of in-flight cleanups keyed by effect, fed by a cleanup observer, with one timer. Entry ids come from the loader's fiber-to-entry lookup. Reports go to the log and `config.Status`; at shutdown to standard error with a goroutine dump.
- **Components and files.** `internal/boot` (`watchdog.go`).
- **Depends on.** [DH-D-BOOT-5](#dh-d-boot-5), [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1), [DH-D-LOADER-8](../libs/cordis-loader.md#dh-d-loader-8), [DH-D-CORDIS-10](../libs/cordis.md#dh-d-cordis-10) (`WithCleanupObserver`, the tree option that reports the start and end of each effect's cleanup).
- **Priority.** P1. **Outcome.** DH-O-1, DH-O-14. **Done when.** `DH-T-BOOT-19` passes and [DH-E-BOOT-4](#dh-e-boot-4) has passed.

<a id="dh-d-boot-9"></a>
### DH-D-BOOT-9 Plugins are listed, inspected, added, switched, restarted and removed from one service
- **Result and why it matters.** A person manages the profile's entries and local plugins without editing YAML and sees what a plugin asks for before it runs; an agent can look; no operation leaves a half-installed state.
- **How it is implemented.** `plugins.Manager` is a thin layer over `config.Service` and A22's `plugin.Service`: `List` joins the composed tree with fiber state and A22's descriptors; `Add` inspects, checks that the grants are a subset of what the manifest requests, has A22 make the approved copy, and applies one `Insert`; the others apply one operation each. The tool component declares the schema with `list` and `inspect`, both reads; `attach` and `detach` are added to the same tool by [DH-D-BOOT-10](#dh-d-boot-10).
- **Components and files.** `internal/boot/plugins`, `internal/boot/plugins/tool`.
- **Depends on.** [DH-D-BOOT-7](#dh-d-boot-7), [DH-D-EXT-1](extensions.md#dh-d-ext-1) (inspection, digest, approved copy), [DH-D-EXT-4](extensions.md#dh-d-ext-4) (the `plugin` entry's process), [DH-D-TOOLS-1](tools.md#dh-d-tools-1) (tool declaration), [DH-D-CLI-9](app-cli.md#dh-d-cli-9) (the admin tree its commands run in). Its commands are `dsh plugin list`, `inspect`, `add`, `remove` (`--purge`), `enable`, `disable`, `restart`, `grant` and `revoke`, contributed by this deliverable as `cmdline.Command` values; its page, [DH-D-UISET-6](ui-settings-plugins.md#dh-d-uiset-6), consumes this deliverable. Runtime prerequisite: the `plugins` group exists in the profile.
- **Priority.** P1. **Outcome.** DH-O-11. **Done when.** `DH-T-BOOT-20` and `DH-T-BOOT-21` pass and [DH-E-BOOT-3](#dh-e-boot-3) has passed. DH-E-BOOT-3 writes the profile layer and the home's approved copies, so it runs in this deliverable's build stage.

<a id="dh-d-boot-10"></a>
### DH-D-BOOT-10 An agent attaches a plugin to its own session, and to nothing else
- **Result and why it matters.** The model can try a plugin it wrote in the session it is working in, after one owner consent, and can make nothing durable ([DH-ADR-30](../design.md#dh-adr-30)). It depends on A22's attachment, which is later work than the owner's management surface, so it is its own deliverable.
- **How it is implemented.** The tool component of [DH-D-BOOT-9](#dh-d-boot-9) gains `attach` and `detach`, each forwarded to A22's `plugin.Service.Attach` and `Detach` with the calling agent as the session; neither calls `config.Service.Apply`. Until this deliverable is mounted the tool has only `list` and `inspect`.
- **Components and files.** `internal/boot/plugins/tool`.
- **Depends on.** [DH-D-BOOT-9](#dh-d-boot-9), [DH-D-EXT-7](extensions.md#dh-d-ext-7) (attach, detach and the one consent).
- **Priority.** P2. **Outcome.** DH-O-11, DH-O-7. **Done when.** `DH-T-BOOT-24` passes and [DH-E-BOOT-5](#dh-e-boot-5) has passed.

## 9. Acceptance

### 9.1 Local tests

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-BOOT-1` | DH-D-BOOT-1, DH-ADR-BOOT-3, DH-ADR-BOOT-13 | `Build` rejects a duplicate name, a nil component, a name of one segment or of four, a hyphen in place of the family dot, an upper-case letter or underscore, and an unregistered family; a second mount without `@<instance>` is a static issue; `Resolve` distinguishes an unknown name from one not available on this platform; two catalogues coexist in one process |
| `DH-T-BOOT-2` | DH-D-BOOT-1, DH-D-BOOT-3, DH-ADR-BOOT-1 | For every built-in profile, through the real codec and loader: every bundle entry has an explicit id, resolves, decodes strictly and has valid expressions; every injected service has a declared provider; no component provides a service its item does not declare; an optional bundle contains only inserts under one disabled group; the scope component has no injecting ancestor; the composed tree matches a golden file |
| `DH-T-BOOT-3` | DH-D-BOOT-1 | The generated component catalogue equals a fresh render; a stale file fails |
| `DH-T-BOOT-4` | DH-D-BOOT-2, DH-ADR-BOOT-4 | Precedence process, project file, home file with the source reported; a launch-only name in either file aborts with the file and name and applies nothing; proxy and endpoint names are accepted from the home file only; a proxy value from a fake system reader is used only when no other source names one, is reported with source `system`, and a reader that fails or exceeds its bound yields none and one diagnostic line; the process environment is byte-identical before and after |
| `DH-T-BOOT-5` | DH-D-BOOT-2, DH-ADR-BOOT-4 | Home precedence (option, variable, default; a blank variable is unset); `Data` cannot be replaced by an entry; `Ready` closes once and only after the audit; `Stop` keeps the first reason and status, later calls join, and `Stopping` closes once |
| `DH-T-BOOT-6` | DH-D-BOOT-2, DH-ADR-BOOT-5 | Each `launch` name returns its value; `launch.words` does not exist; a view type with a secret, pointer, function or interface field fails the catalogue test; `use.x.typo` and an undeclared view fail at check time with a position; a secret name under `env` is refused |
| `DH-T-BOOT-7` | DH-D-BOOT-3, DH-ADR-BOOT-1, DH-ADR-BOOT-2 | The stack order; exactly one writable layer, also with overlays; a missing or empty file is an empty layer; a custom profile extends its base; an unknown profile is an error naming the known ones; an override by path id reaches a nested entry |
| `DH-T-BOOT-8` | DH-D-BOOT-3 | `profile reset` renames the layer to a unique backup name, leaves the home layer and other profiles byte-identical, and the next launch starts from an empty layer |
| `DH-T-BOOT-9` | DH-D-BOOT-4 | The four edit operations at a nested path keep every comment; the comment of a deleted override moves to the next entry; an `!expr` scalar is unchanged; a write whose re-parse differs is refused |
| `DH-T-BOOT-10` | DH-D-BOOT-5, DH-ADR-BOOT-6 | Through the real loader, for each failure kind (bad config, constructor error, panic, missing service, failing expression, failing `disabled` expression, load past the deadline): as an optional entry it is one warning and its siblings are active; as a required entry `Run` returns 78, the tree is unloaded and a counting fake shows every effect released once. A required id that a layer removes or disables is a static issue, and at boot it fails the launch with 78; a custom profile cannot shorten its base's list; the `admin` composition has no required id |
| `DH-T-BOOT-11` | DH-D-BOOT-5 | Golden report: failed entries apart from waiting ones, required ones marked, the entry's `file:line:column`, the cause chain once; standard output is empty |
| `DH-T-BOOT-12` | DH-D-BOOT-5, DH-ADR-BOOT-6 | With virtual time: `Stop(Terminated, 0)` unloads leaves first and returns 0; `Stop(Completed, 7)` returns 7; a second `Stop` joins and does not change the status; `Stop(Interrupted, 130)` during shutdown returns at once; edits and reloads are refused with `config/stopping` after shutdown begins; a blocked cleanup makes `Run` return 124 at the bound |
| `DH-T-BOOT-13` | DH-D-BOOT-5, DH-ADR-BOOT-6 | A panic on a harness goroutine produces one diagnostic and status 70; the release hook is cut off at its timeout; a second failure during release is not reported |
| `DH-T-BOOT-14` | DH-D-BOOT-6 | Golden dump with supplying-layer comments and expressions in source form; loading the dump as a single layer composes to the same tree; the variant without the profile layer; an unmatched operation is listed with its layer; an invalid layer exits non-zero; a counting resolver shows no component was constructed |
| `DH-T-BOOT-15` | DH-D-BOOT-7, DH-ADR-BOOT-7 | Saving a layer with a syntax error, an unknown key, a wrong type, an unknown component or a malformed expression, each together with a valid change to another entry: nothing restarts, the diagnostic's position is exact, `config.Rejected` is raised once. The corrected file applies and only changed entries restart; a component with an update hook is not restarted |
| `DH-T-BOOT-16` | DH-D-BOOT-7, DH-ADR-BOOT-2, DH-ADR-BOOT-7, DH-ADR-BOOT-14 | `Apply`, one case per clause of the edit seam: a stale revision and a shadowed field are refused with no write; a merge writes only the edited field; a null is written as a null and `Reset` removes the operation; a batch with one bad operation writes nothing; a computed field is refused unless overwrite is set, and a self-save skips a field equal to its expression; a `live` field reaches the update hook without a restart; a required entry cannot be disabled or removed; re-enabling removes an override that only restates lower layers; a read-only file gives `unsaved`, and `Status` clears after a later successful write |
| `DH-T-BOOT-17` | DH-D-BOOT-7, DH-ADR-BOOT-8 | Two layer sources on one real file and lock: concurrent edits of different fields both survive; of the same field, one wins and the conflict is reported; a held lock gives `config/busy` at the bound; a lock whose holder was killed is acquired |
| `DH-T-BOOT-18` | DH-D-BOOT-7 | With `reload` disabled a file edit changes nothing until restart, while `Apply` still reconciles and picks up the other files' current content; a change saved before readiness is applied after it |
| `DH-T-BOOT-19` | DH-D-BOOT-8, DH-ADR-BOOT-9 | With virtual time: a cleanup blocked on a channel is reported with entry id, component and label after the limit and again at doubling intervals; releasing it logs completion; later cleanups of the same fiber have not run meanwhile; at shutdown every stuck cleanup is listed |
| `DH-T-BOOT-20` | DH-D-BOOT-9, DH-ADR-BOOT-10 | With a fake `plugin.Service` and a fake plugin component: each inspection problem refuses `Add` with no copy and no write; `Add` writes one insert holding the approved copy, digest and granted subset; a grant the manifest does not request is refused; a failure after the copy writes no entry; `Remove` deletes the operation, the operator's directory is byte-identical, and the approved copy goes only with `purge`; a protected entry is refused; every call raises `plugins.Operation` start and end |
| `DH-T-BOOT-21` | DH-D-BOOT-9, DH-ADR-BOOT-10, DH-ADR-BOOT-11 | Through the real pipeline with a fake `plugin.Service`: without DH-D-BOOT-10 the tool's schema has exactly `list` and `inspect`; both ask nothing; the profile layer is byte-identical after every action; a result that is not done carries `nextStep`; no built-in profile or agent preset exposes `plugin_market` |
| `DH-T-BOOT-24` | DH-D-BOOT-10, DH-ADR-BOOT-10 | Through the real pipeline with a fake `plugin.Service`: the tool's schema has exactly `list`, `inspect`, `attach` and `detach`; `attach` and `detach` reach A22 with the calling agent's session and never call `config.Service.Apply`; the profile layer is byte-identical after each |
| `DH-T-BOOT-22` | DH-D-BOOT-1, DH-D-BOOT-5, DH-ADR-BOOT-14 | Over the module's source: no call to `loader.Register`; `os.Exit` is called only in `cmd/dsh`; the root tree is disposed only in package `boot`; the loader's write-back and the write guard are called only from package `config`; every id in a `Required` list exists in that profile's shipped layers |
| `DH-T-BOOT-23` | DH-D-BOOT-3, DH-ADR-BOOT-12 | With `Safe` set: the launcher layer disables the `plugins` group and every optional bundle's group and nothing else; the composed tree otherwise equals the ordinary one; `launch.safe` is true in expressions; an `Apply` that enables a plugin is saved and reports `overridden(launcher)`; without `Safe` the launcher layer holds neither operation |

### 9.2 Real-environment scenarios

No remote lab is involved: each scenario runs the built `dsh` binary on a real machine, with the controller on the same machine. A scenario that has not run is open.

<a id="dh-e-boot-1"></a>
#### DH-E-BOOT-1 A launch that cannot serve fails loudly, and one that can serve does
- **Proves.** DH-D-BOOT-5, DH-D-BOOT-3.
- **Given.** The binary built from a recorded commit, on Windows 11 and on Linux. An empty temporary home. Profile `web`. A second program holding the HTTP port the profile uses. An overlay file that gives one optional tool entry an invalid config value.
- **When.** (1) Launch with the overlay while the port is held. (2) Release the port and launch again with the overlay. (3) Request the UI's first page with the launch token. (4) Send the termination signal.
- **Then.** In step 1 the process exits with status 78 within the start-up bound, standard error names the HTTP entry as required with the bind error, names the optional entry with the overlay's file, line and column, and gives the diagnostic file's path; standard output is empty and no child process remains. In step 2 the process reaches readiness with one warning naming the optional entry. Step 3 succeeds. After step 4 the process exits 0 and the port is free.
- **Cleanup.** Stop the port holder; delete the home. If a process survives, record its goroutine dump, kill it, and keep the home and logs.

<a id="dh-e-boot-2"></a>
#### DH-E-BOOT-2 Two processes on one home and one profile do not lose or half-apply an edit
- **Proves.** DH-D-BOOT-7, DH-ADR-BOOT-8.
- **Given.** A scenario build of the binary with two probe entries. Each probe reports its config and start count, and applies a change, through its own control hook (a test-only local socket of the scenario build that calls `config.Service.Apply` and prints the result), so nothing here needs a command of a later deliverable. Two real `dsh web` processes, A and B, on different ports, on one temporary home, on Windows 11 and on Linux, both with reload enabled. A third `dsh headless` process, C, on the same home with its own profile.
- **When.** (1) Through A's control hook, change probe 1. (2) At the same moment change different fields of probe 2 through A's and B's hooks. (3) Save a syntax error into the profile layer by hand. (4) Correct it. (5) Kill A while it holds the lock (the hook delays the write), then edit through B's hook.
- **Then.** After step 1, B reports the new value within two seconds with its process id unchanged and probe 2's start count unchanged; C reports no change. After step 2 the layer file holds both fields and both processes report both. After step 3 both processes report a rejection with the same file, line and column, and neither probe restarted. After step 4 the edit applies in both. In step 5 B's edit succeeds within the lock bound and the file parses.
- **Cleanup.** Stop the processes; delete the home. On failure keep the layer file, its backups and both logs.

<a id="dh-e-boot-3"></a>
#### DH-E-BOOT-3 A local plugin directory is inspected, granted, run, replaced, removed and left off in safe mode
- **Proves.** [DH-D-BOOT-9](#dh-d-boot-9), [DH-ADR-BOOT-10](#dh-adr-boot-10), [DH-ADR-BOOT-12](#dh-adr-boot-12). It is the boot half of the scenario [DH-ADR-13](../design.md#dh-adr-13) asks of areas A21 and A22. Attaching from a session is [DH-E-BOOT-5](#dh-e-boot-5).
- **Given.** The built binary, profile `web`, an empty temporary home, a real sandbox backend. A real plugin directory outside the home whose manifest requests two capabilities and offers one tool per capability. A second version of the same directory with a changed manifest.
- **When.** (1) `dsh plugin inspect` the directory. (2) `dsh plugin add` it, granting one capability. (3) List. (4) Call both tools. (5) Disable, then enable. (6) Replace the directory's content with the second version and restart the entry. (7) Add the directory again, then list. (8) `dsh plugin remove --purge`. (9) Add it again, stop the host and start it with `--safe`.
- **Then.** Step 1 prints both requested capabilities and starts no process. After step 2 the profile layer holds one new entry with the approved copy, the digest and one grant, and a plugin process runs as a child of the host. In step 4 the granted tool answers and the other call is denied. Step 5 stops and starts the process, with no host restart. After step 6 the running plugin is still the first version, because the approved copy runs. After step 7 the entry holds the second digest and the new process serves the second manifest. After step 8 no plugin process remains, the entry and the approved copy are gone, and the operator's directory is byte-identical to what step 6 placed there. In step 9 the host reaches readiness, no plugin process starts, and `dsh plugin list` shows the entry as off for this launch.
- **Cleanup.** Delete the home and the directories. If a plugin process survives the host, record its process tree, kill it and keep the logs.

<a id="dh-e-boot-5"></a>
#### DH-E-BOOT-5 An agent attaches a plugin to its own session, and the profile does not change
- **Proves.** [DH-D-BOOT-10](#dh-d-boot-10), [DH-ADR-BOOT-10](#dh-adr-boot-10).
- **Given.** The built binary, profile `web`, an empty temporary home, a real sandbox backend, a real model API key supplied at run time, an owner in a real browser. A plugin directory inside the session's workspace with one tool, as the model would have written it.
- **When.** (1) In a session, have the model call `plugin_manager` with `attach` for that directory; answer the consent in the browser. (2) Have the model call the plugin's tool. (3) In a second session, list the tools. (4) End the first session.
- **Then.** In (1) the consent shows A22's inspection report and the attach succeeds; the profile layer is byte-identical before and after. In (2) the tool answers. In (3) the tool does not exist. After (4) the attachment is gone, no plugin process remains, and the profile layer is still unchanged.
- **Cleanup.** Delete the home and the workspace. If a plugin process survives, record its process tree, kill it and keep the logs.

<a id="dh-e-boot-4"></a>
#### DH-E-BOOT-4 A cleanup that never returns is named, and the process still ends
- **Proves.** DH-D-BOOT-8, DH-D-BOOT-5, DH-ADR-BOOT-9.
- **Given.** A scenario build of the binary that includes one probe component whose effect cleanup blocks while a marker file exists, with the control hook of [DH-E-BOOT-2](#dh-e-boot-2) and a short `ShutdownTimeout` in the scenario build's profile definition, on Windows 11 and on Linux. Profile `headless` with the probe mounted, its task read from a standard input kept open.
- **When.** (1) Start, wait for readiness. (2) Disable the probe through its control hook. (3) Send the termination signal.
- **Then.** After step 2 the hook's result is `failed` and names the probe's entry id and effect label; the log repeats it; other entries keep serving. After step 3 the process exits with status 124 within the bound plus two seconds, and standard error lists the same entry and label with a goroutine dump.
- **Cleanup.** Remove the marker file and the home. If the process has not exited, kill it and keep the log.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Several hosts on one profile and one plugin.** Each host runs its own process of an installed plugin against one state directory. Whether a plugin may assume it is the only process on its state directory is a clause of A22's protocol, not of this document; nothing here coordinates the processes.
2. **Limitations.** A static check cannot see a load failure, so "a bad edit changes nothing" holds for static issues only. Two hosts on one profile differ for up to a poll interval. A stuck cleanup is named, not cured. The launch environment does not follow a later workspace. In `danger-full-access` a command can edit the profile layer directly. An agent cannot change the profile at all. Defaults marked as proposals are unmeasured.

## 11. Units dispositioned by this spec

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 6 | 2 ported, 3 replaced, 1 dropped |
| `UP-DEBT` | 57 (55 assigned, 2 found) | 27 resolved, 27 not-applicable, 3 carried |
| `UP-TOOL` | 2 | 1 ported, 1 dropped |
| `UP-EVT` | 9 | 3 ported, 1 replaced, 5 dropped |
| `UP-CFG` | 3 | 2 replaced, 1 dropped |
| `UP-SVC` | 5 | 3 ported, 1 replaced, 1 dropped |
| `UP-DEC` | 18 | 5 ported, 7 replaced, 3 dropped, 3 not-applicable |

Go items declared: 10 deliverables, 14 decisions, 24 local tests, 5 scenarios. The rows are in `trace/fragments/A21.jsonl`.
