# Settings, data sharing, models, accounts, plugins, permission presets and skills pages: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) (`2e39e70`) and are cited as `path:line` relative to the upstream root. Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `UISET`. Nothing here has been built, run or tested.

| | |
|---|---|
| **Area key** | `UISET` (agent C7) |
| **Upstream packages** | 15, all under `packages/client`: `ui-settings`, `ui-settings-general`, `ui-settings-models`, `ui-settings-account`, `ui-account-notifications`, `ui-settings-agent-loop`, `ui-settings-shell`, `ui-settings-subagent`, `ui-settings-web-search`, `ui-settings-session-log`, `ui-plugin-manager`, `ui-plugin-market`, `ui-connector-account`, `ui-permission-presets`, `ui-skill` |
| **Serves outcomes** | [DH-O-12](../design.md#dh-o-12) first; also [DH-O-2](../design.md#dh-o-2) (edit a running composition), [DH-O-14](../design.md#dh-o-14) (opt-in sharing is visible), [DH-O-11](../design.md#dh-o-11) (grants are asked for), [DH-O-7](../design.md#dh-o-7) (secrets, presets, grants), [DH-O-8](../design.md#dh-o-8), [DH-O-1](../design.md#dh-o-1) |
| **Depends on areas** | A19 ([api.md](api.md): routes, forms, stream), A07 ([storage.md](storage.md): `settings.Forms`), A21 ([boot.md](boot.md): `config.Service`, `plugins.Manager`), A22 ([extensions.md](extensions.md): manifest, report, descriptor), A18 ([host-telemetry.md](host-telemetry.md): sharing gate, inventory, directory browser), A17 ([interaction-credentials.md](interaction-credentials.md): secret store, account, presets, grants), A04 ([llm.md](llm.md): routes, catalogue, route check), A15 ([mcp-lsp-web-connector.md](mcp-lsp-web-connector.md): fleet, account views, web providers), A14 ([context-compaction-skill.md](context-compaction-skill.md): skill registry), A12, A09, A02, A03 (config types shown as cards), A23 (`schema`), A25 ([app-web.md](app-web.md): first run, about, this browser), C1 ([ui-core.md](ui-core.md): shell, regions, catalogue, preferences), C2 (layout and form partials), C3 ([ui-chat.md](ui-chat.md): composer, `/` menu), C4 ([ui-conversation.md](ui-conversation.md): renderer, tool cards, grants strip) |
| **Priority** | P1 for the settings framework, the sharing page, models, plugins, connections, presets and skills: a first working harness can be configured from layer files and the first-run page of area A25, but [DH-ADR-9](../design.md#dh-adr-9) requires each sharing switch to be visible before any sender ships. P2 for the vendor account panel and for restart-requiring edits |

## 1. Purpose

A person who runs the harness needs one place to see and change how it is set up: which model providers exist and with which keys, what leaves the machine, which plugin directories run and what they were granted, which accounts are linked, how much the agent may do without asking, and which skills it can use. This area is those pages. It owns no state. Every value it shows belongs to another area's service, and every change goes through that service's own edit path; the pages are views and forms.

Upstream builds the same pages from fifteen browser packages that each register React components into slots, keep a browser copy of host state and validate in the browser. In the Go design the host renders the pages ([DH-ADR-3](../design.md#dh-adr-3)), so the framework those packages share has a new job: let any compiled-in component put a settings card on the page **without writing browser code**, and derive the common case, a form for a config struct, from the Go type itself ([../design.md §4.9](../design.md#49-configuration-model)).

## 2. Vocabulary

Used as written from their owners: **view**, **fragment**, **region**, **contribution**, **message catalogue**, **host preference**, **per-tab store** ([ui-core.md §2](ui-core.md#2-vocabulary)); **route**, **interest**, **request id** ([api.md §2](api.md)); **settings form**, **live field**, **revision** ([storage.md §2](storage.md)); **layer**, **candidate** ([boot.md §2](boot.md)); **manifest**, **capability**, **grant**, **digest**, **approved copy**, **trust level** ([extensions.md §2](extensions.md)); **sender**, **kind**, **permit**, **off-switch** ([host-telemetry.md §2](host-telemetry.md)); **credential reference**, **permission preset**, **session grant**, **interaction** ([interaction-credentials.md §2](interaction-credentials.md)); **account view** ([mcp-lsp-web-connector.md §2](mcp-lsp-web-connector.md)). A model **route** is one configured provider endpoint ([llm.md](llm.md)); an HTTP route is always called "URL route" here when the two could be confused.

| Term | What it is | Why it exists |
|---|---|---|
| **Slot, companion package, describe mirror** (upstream) | A slot is a named place in the React tree. A companion package is a browser-only package with an empty host half, shipped so that a host namespace gets a page. The describe mirror is the browser's copy of the host's settings description. | The three mechanisms this area replaces. |
| **Settings page, section** | The page at `/settings`. A section is one entry of its navigation with a URL of its own (`?section=models`). | One place to look, linkable and usable with the back button. |
| **Card** | The form for one composed entry's config, shown in a section: fields, where each value comes from, Save and Reset. | The unit a component contributes and a person saves. |
| **Generated card, written card** | A generated card is derived from the entry's Go config type with no UI code. A written card is a registered template for the same form. | Most settings need no design; a few (model providers) need a layout. |
| **Draft** | What a person typed into a card and has not saved. | Nothing is written until Save, and a conflict must not lose it. |
| **Save outcome** | The host's statement of what a save did: applied in place, applied by restarting one component, saved but overridden, saved but failed, not saved. | "Saved" alone hides whether the running system changed. |
| **Field state** | `live` (applies without a restart), `restart` (a card offers it; saving restarts its entry after a confirmation) or `file-only` (shown, edited in the layer file). | Says before the click what a change will cost. |
| **Inspection report** | Area A22's `manifest.Report`: what a plugin directory is, what it asks for and what cannot be enforced, read without running it. | The thing a person consents to. |
| **Sharing page** | The settings section that lists every sender of area A18's gate with its switch, and the off-switch. | [DH-ADR-9](../design.md#dh-adr-9): each kind of outbound sharing is a visible setting. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.client.ui-settings` | `dsh-client-ui-settings` | definition and provider | `ctx.configForms`: one browser mirror of the host's settings description, one write queue per host entry, and the slot types settings pages register into; it renders nothing (`packages/client/ui-settings/README.md:12`, `:34`, `:45`, `:63`). |
| `UP-PKG-packages.client.ui-settings-general` | `dsh-client-ui-settings-general` | provider (shell) | The Settings panel: navigation, the General section (Coding Tools switch, current version), "open configuration file", onboarding step order, the connection indicator and the release-change dialog (`packages/client/ui-settings-general/README.md:38`, `:48-50`, `:56`, `:60`). |
| `UP-PKG-packages.client.ui-settings-models` | `dsh-client-ui-settings-models` | consumer | The Models page: one row per provider, a hand-written editor, write-only API keys, add and delete, the first-run credential step and a versioned welcome notice (`packages/client/ui-settings-models/README.md:46-48`, `:74`, `:78`, `:112`). |
| `UP-PKG-packages.client.ui-settings-account` | `dsh-client-ui-settings-account` | consumer | The account launcher and Account section (profile, balance, usage and top-up links), sign-in and sign-out, and three desktop-only parts: an illustrated introduction, an embedded vendor page, a questionnaire link (`packages/client/ui-settings-account/README.md:33`, `:41-47`, `:55`, `:62`). |
| `UP-PKG-packages.client.ui-account-notifications` | `dsh-client-ui-account-notifications` | consumer | Polls the vendor for account notices, shows them as cards and reports "displayed", "clicked" and "dismissed" (`packages/client/ui-account-notifications/README.md:25`, `:31-33`). |
| `UP-PKG-packages.client.ui-settings-agent-loop` | `dsh-client-ui-settings-agent-loop` | consumer (companion) | A page for one number, the parallel tool-call cap; its host half is an empty `apply` (`packages/client/ui-settings-agent-loop/README.md:28`, `:38`). |
| `UP-PKG-packages.client.ui-settings-shell` | `dsh-client-ui-settings-shell` | consumer (companion) | Command timeout and per-stream output cap of the composed shell executor (`packages/client/ui-settings-shell/README.md:28`, `:38`). |
| `UP-PKG-packages.client.ui-settings-subagent` | `dsh-client-ui-settings-subagent` | consumer (companion) | Recursion depth, parallel capacity and the model routes subagents may use, saved as two revision-fenced writes (`packages/client/ui-settings-subagent/README.md:28-32`). |
| `UP-PKG-packages.client.ui-settings-web-search` | `dsh-client-ui-settings-web-search` | consumer (companion) | Search and fetch provider menus, result count, billing choice and the server-side search settings (`packages/client/ui-settings-web-search/README.md:28-36`). |
| `UP-PKG-packages.client.ui-settings-session-log` | `dsh-client-ui-settings-session-log` | consumer (companion) | One General row: the switch for uploading the session log with vendor API requests (`packages/client/ui-settings-session-log/README.md:28`). |
| `UP-PKG-packages.client.ui-plugin-manager` | `dsh-client-ui-plugin-manager` | consumer and provider | The Plugins page: lists bundles and their rows, installs npm, Git or local packages by running pnpm, switches, updates and removes them, hosts other packages' configuration pages; `ctx.pluginRegistryProbe` races two npm registries (`packages/client/ui-plugin-manager/README.md:34`, `:65-69`, `:87`, `:92`, `:140`). |
| `UP-PKG-packages.client.ui-plugin-market` | `dsh-client-ui-plugin-market` | consumer | The Discover tab: index sources, search, install and update from an index (`packages/client/ui-plugin-market/README.md:27`, `:35-37`). |
| `UP-PKG-packages.client.ui-connector-account` | `dsh-client-ui-connector-account` | service and consumer | `ctx.connectorAccounts`: a host registry of connector accounts whose display the host computes; the browser shows the section, tag and prompt and opens the authorization page (`packages/client/ui-connector-account/README.md:28`, `:60`, `:71`). |
| `UP-PKG-packages.client.ui-permission-presets` | `dsh-client-ui-permission-presets` | consumer | The default-preset row in General, and the composer control and `/permission` picker for the current session (`packages/client/ui-permission-presets/README.md:34`, `:40`). |
| `UP-PKG-packages.client.ui-skill` | `dsh-client-ui-skill` | consumer | Skill candidates in the `/` menu, the skill tool row, and a preview of a skill's file when its `/name` is clicked (`packages/client/ui-skill/README.md:28`, `:32`, `:38-40`). |

### 3.2 Behaviour

**The settings framework.** The host's settings service describes, per profile entry, the fields a plugin marked volatile ([storage.md §3](storage.md)). `ui-settings` reads that description once into a browser mirror, refreshed on `settings/document-updated` and on reconnect, and hands each feature a form object for its entry: resolved value, inherited value, the user's override, a revision and writability (`packages/client/ui-settings/README.md:34`, `:67`). A page is a React component a client plugin registers into `settings.section` (Settings) or `plugins.item` (the Plugins page); a page for a namespace another plugin owns registers through `whileServed`, so it exists only while the host serves that namespace (`:40`, `:45`). Editors are staged: nothing is written until Save, an emptied field saves as a reset, an overridden field carries a badge, and a write carries the revision it was read at, so a concurrent change is refused and the draft is kept (`packages/client/ui-settings-shell/README.md:28`; `packages/client/ui-settings-models/README.md:106`). The browser validates against a serialised schema and the host validates the whole config again (`packages/client/ui-settings/README.md:51`). Every authenticated browser shares the one host document (`.agents/notes/implemented/bug-fix/2026-09-15-authenticated-browser-host-preferences.md:13`).

**Models and keys.** The page joins three host reads: settings, credential metadata and the adapter directory. A key is typed into one field, stored write-only under a derived `<ROUTE>_API_KEY` reference, and shown afterwards only as a host-made masked preview; the value never reaches the browser (`packages/client/ui-settings-models/README.md:46-48`). Settings are saved before the credential, so a failed credential step can be retried alone (`:106`).

**Account.** Sign-in in a plain browser pre-opens a tab during the click and navigates it once the host returns the authorization address (`packages/client/ui-settings-account/README.md:33`). Profile and balance are read when the section opens; usage and top-up are links that never carry a token (`:41`, `:101`). Notification polling starts as soon as an account is stored (`packages/client/ui-account-notifications/README.md:25`).

**Plugins.** "Add plugin" takes a package name, Git address, tarball or local path and runs pnpm on the host, with a registry chosen by a ping race, a progress dialog, cancellation and a recovery request for a lost answer (`packages/client/ui-plugin-manager/README.md:63-69`). Rows show a fibre phase; switches edit the profile patch (`:87`). Discover reads index sources the user added and installs from them (`packages/client/ui-plugin-market/README.md:27-35`). Connector accounts register on the host; the host decides phase, text and buttons, and the browser that started an operation opens its authorization page (`.agents/notes/implemented/architecture/2026-11-06-connector-account-registry.md:24`).

**Permission and skills.** The picker submits the command line `/permission <preset>`; the General row writes the default for later sessions only (`packages/client/ui-permission-presets/README.md:34`, `:40`). Skill candidates are fetched per session and cached in the browser until one of three events clears the cache; the tool row is drawn only from the recorded call and result (`packages/client/ui-skill/README.md:38`, `:54`).

### 3.3 Failure, retry, rollback and cleanup upstream

- **A refused write** refreshes the mirror and keeps the editor open with the host's diagnostic (`packages/client/ui-settings/README.md:51`; `packages/client/ui-settings-models/README.md:38`).
- **Key deletion succeeds and the settings write fails:** the editor reports the half-done state and offers a retry (`packages/client/ui-settings-models/README.md:54`). Deleting a provider removes its credential only when the reference is the derived one (`:150`).
- **A page reload during an installation** loses the tracked request and its output; the host keeps no completed result (`packages/client/ui-plugin-manager/README.md:195`). A cancelled installation restores the manifest and lockfile; downloaded files may remain (`:69`).
- **A blocked pop-up** leaves a copy-link route for sign-in and an "open" button for connectors (`packages/client/ui-settings-account/README.md:33`; `packages/client/ui-connector-account/README.md:128`).
- **A failed notification poll** keeps the current cards (`packages/client/ui-account-notifications/README.md:52`).

## 4. Upstream decisions dissected

The first two rows are the decision notes handed to this area. The rest are visible only in READMEs. `UISET-n` is short for `DH-ADR-UISET-n` in [§7](#7-decisions); `R:` stands for `packages/client/`.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `implemented/architecture/2026-11-06-connector-account-registry` | One host registry holds every connector account; the host decides what is shown; the browser only displays | Keep the rule, drop the registry | "The host decides, the page displays" is the whole Go UI. Area A15's account view already is that host-computed display, so a second registry would only relay it | [UISET-7](#dh-adr-uiset-7) |
| `implemented/bug-fix/2026-09-15-authenticated-browser-host-preferences` | Authentication, not locality, decides who shares the settings store | Keep | A server-rendered page has no local store to fall back to; one revision-checked document is the only model | [UISET-2](#dh-adr-uiset-2) |
| Pages are client plugins in slots; a namespace's page exists while the host serves it (`R:ui-settings/README.md:40`, `:45`) | No feature imports another, and a page never outlives its namespace | Replace | The need stays; a card is an effect of a host component, so it leaves with its owner by construction | [UISET-1](#dh-adr-uiset-1) |
| One companion package per namespace, with an empty host half (`R:ui-settings-shell/README.md:38`) | The client loader serves browser code only for a loader row | Not applicable | Exists for the client module loader. A config type is enough to get a card | [UISET-1](#dh-adr-uiset-1) |
| Official configuration pages live on the Plugins page, plugin-owned pages in Settings (`R:ui-plugin-manager/README.md:92`) | Upstream calls every unit a plugin | Change | Here a component's settings are settings; the plugins page is about plugin processes | [UISET-1](#dh-adr-uiset-1) |
| Staged editing; an emptied field resets; an "Overridden" badge (`R:ui-settings-shell/README.md:28`) | A half-typed number must not reconfigure a running system | Keep | It is also what an HTML form does without script | [UISET-2](#dh-adr-uiset-2) |
| A write carries its revision; a conflict keeps the draft (`R:ui-settings/README.md:34`) | Two tabs or a hand edit must not overwrite each other | Keep | Area A07 computes the revision from content, so two processes agree on it | [UISET-2](#dh-adr-uiset-2) |
| The browser validates a serialised schema, then the host validates again (`R:ui-settings/README.md:51`) | Fast feedback | Change | Two validators can disagree; the host is the only one, plus the browser's own input attributes | [UISET-2](#dh-adr-uiset-2) |
| The Models editor is hand-written and covers curated fields only (`R:ui-settings-models/README.md:149`) | A designed layout was preferred to schema-generic coverage | Change | A written card for the common fields and a generated fold for every other live field | [UISET-6](#dh-adr-uiset-6) |
| A key is write-only; the page shows a host-masked preview (`R:ui-settings-models/README.md:46`) | The value must never reach a browser | Keep, tighten | The key is its own form, and a rejected key is never rendered back | [UISET-6](#dh-adr-uiset-6) |
| "Show secret" eye control on a draft key (`R:ui-settings-models/README.md:46`) | Check a pasted key | Drop | It needs script that handles a secret; the masked preview after saving answers the same question | [UISET-6](#dh-adr-uiset-6) |
| Script opens the authorization page after the call answers (`R:ui-settings-account/README.md:33`) | The address is known only then | Change | A link the person clicks is never blocked | [UISET-7](#dh-adr-uiset-7) |
| Desktop introduction, embedded vendor page, update-channel row (`R:ui-settings-account/README.md:43`, `:62`; `R:ui-settings-general/README.md:66`) | Native surfaces of the desktop application | Drop | No desktop application; first run is area A25's setup page | [DH-ADR-18](../design.md#dh-adr-18) |
| The account menu opens a third-party questionnaire prefilled with account id, version and device data (`R:ui-settings-account/README.md:47`) | Feedback outside the product | Drop | Identity sent to a third party with no preview; area A17's feedback form is the one path | [DH-ADR-9](../design.md#dh-adr-9) |
| Notices are polled once an account is stored, and interactions are reported (`R:ui-account-notifications/README.md:25`, `:31`) | The vendor wants to reach users | Change | Unasked vendor traffic; a setting, off by default (area A17) | [UISET-7](#dh-adr-uiset-7) |
| Session-log upload is one row among General preferences (`R:ui-settings-session-log/README.md:28`) | It is "a preference" | Change | It sends data; it belongs with every other sender | [UISET-4](#dh-adr-uiset-4) |
| Install from npm, Git or a path by running pnpm; a Discover tab (`R:ui-plugin-manager/README.md:67`; `R:ui-plugin-market/README.md:27`) | Plugins are npm packages | Drop the channel, keep "inspect first" | [DH-ADR-13](../design.md#dh-adr-13): local directories, no download or discovery | [UISET-5](#dh-adr-uiset-5) |
| Other packages add tabs, badges and sections to the Plugins page (`R:ui-plugin-manager/README.md:108`, `:124`) | A plugin describes itself | Change | A compiled-in component contributes through a region; a plugin process contributes no UI ([extensions.md §10](extensions.md)) | [UISET-5](#dh-adr-uiset-5) |
| The picker submits `/permission <preset>`; `custom` is display only (`R:ui-permission-presets/README.md:34`) | One durable path for a switch | Keep | The form and the command call the same service | [UISET-8](#dh-adr-uiset-8) |
| The skill row is drawn from the recorded call, never the current catalogue; candidates are cached per session (`R:ui-skill/README.md:38`, `:54`) | Replay must be stable; a fetch per keystroke was costly | Keep the first, drop the cache | The host renders from its own registry on request | [UISET-9](#dh-adr-uiset-9) |
| "Open configuration file" on loopback pages only (`R:ui-settings-general/README.md:56`) | Only a local page can see the host's editor | Drop | Area A19 dropped the native-editor call; the page shows the path | [UISET-3](#dh-adr-uiset-3) |

## 5. Tech debt and its resolution

All 47 units are grouped by cause; each unit's own reason is in its ledger row. `L:` stands for `UP-DEBT-limit.packages.client.`. Carried rows name stage [DH-S-59](../implementation-plan.md#dh-s-59), where the known limits and scope boundaries of this part of the product are stated and checked.

| Group (cause) | Units | How the Go design answers it |
|---|---|---|
| **A. Plugins as npm packages: pnpm runs, registries, a market index** | `L:ui-plugin-manager.1` to `.3`, `.6` to `.9`, `L:ui-plugin-market.1` to `.3` (10) | No package is installed and no index is read ([DH-ADR-13](../design.md#dh-adr-13), [DH-ADR-BOOT-11](boot.md#dh-adr-boot-11)). Five units that describe pnpm and registry mechanics are not applicable. Five are resolved: adding a plugin is one inspection and one layer write, so its diagnostic is on the page, nothing is lost on a reload, and no remote icon or document is fetched ([DH-D-UISET-6](#dh-d-uiset-6)). |
| **B. The desktop shell and its introduction** | `L:ui-settings-account.2`, `.3`, `.5`, `L:ui-settings-general.3` (4) | Resolved by removal: no caption, embedded vendor page or illustrated introduction exists ([DH-ADR-18](../design.md#dh-adr-18)), and first run stores no completion marker ([app-web.md](app-web.md#dh-adr-webapp-3)). |
| **C. A browser copy of host state that goes stale or probes on load** | `L:ui-settings-subagent.1`, `L:ui-skill.3`, `.4`, `.5`, `L:ui-connector-account.3`, `.4` (6) | Resolved. A page is rendered from the owning service when requested and its region is replaced when that service says it changed; opening a page starts no check; there is no registry to load ([DH-ADR-UISET-1](#dh-adr-uiset-1), [DH-ADR-UISET-9](#dh-adr-uiset-9)). |
| **D. A page that shows less than the host knows** | `L:ui-plugin-manager.4`, `.5`, `L:ui-connector-account.1`, `.2`, `L:ui-settings-models.1`, `.2`, `.3`, `.5`, `.6`, `L:ui-settings-agent-loop.1`, `L:ui-settings-shell.1`, `L:ui-settings-web-search.1`, `.2`, `L:ui-settings-session-log.1`, `L:ui-settings-general.2`, `L:ui-skill.1`, `L:ui-permission-presets.1`, `.3` (18) | Resolved. Cards are generated from the composed entry's own type, so every live field and every composed variant appears; lists come from the owning registry (every model route, every entry and plugin with its failure reason, every provider with its availability reason, every credential reference with its users) ([DH-D-UISET-1](#dh-d-uiset-1), [-4](#dh-d-uiset-4), [-6](#dh-d-uiset-6), [-7](#dh-d-uiset-7)). |
| **E. What belongs to one browser** | `L:ui-settings.1`, `L:ui-connector-account.5` (2) | Resolved. Configuration is shared on purpose; a choice that should differ per browser has a home of its own ([ui-core.md](ui-core.md#dh-adr-uicore-5)). An authorization page is a link the person clicks ([DH-ADR-UISET-7](#dh-adr-uiset-7)). |
| **F. Limits kept on purpose, or set by the vendor** | `L:ui-account-notifications.1`, `L:ui-settings-account.1`, `.4`, `L:ui-permission-presets.2`, `L:ui-skill.2`, `L:ui-settings-general.1`, `L:ui-settings-models.4` (7) | Carried, each narrowed. Vendor notices stay plain text with two actions. Profile and balance depend on vendor web endpoints. Vendor sign-in needs a loopback address, and the page now says so before the click. `auto` cannot be a default. A skill reference is text. Mobile keyboards are checked by emulation only. The provider picker is searchable, but product names need a field in area A04's catalogue. |

Totals: 35 resolved, 5 not applicable, 7 carried.

## 6. Go design

### 6.1 Shape

Packages are under `src/harness/internal/ui`. Each is one cordis component that registers URL routes through area A19's `web.Routes`, views through `ui.Register`, and regions and contributions through area C1's `Shell`. A composition that does not mount a component has neither its pages nor its routes.

| Package | Component; service | Injects (owner) | Why it exists |
|---|---|---|---|
| `ui/settingspage` | `ui.settings`; `settingspage.Key` (`Registry`) | `settings.Key` (A07), `config.Key` (A21), `shell.Key`, `msg.Key`, `prefs.Key` (C1), the component catalogue (A21) | The page, its sections, generated and written cards, the form protocol |
| `ui/settingspage/general` | `ui.settings.general`; none | `settingspage.Key`, `preset.Key` (A17, optional) | The General section and the rows this area owns |
| `ui/sharingpage` | `ui.settings.sharing`; none | `sharing` gate (A18), `settingspage.Key` | The sharing page |
| `ui/modelspage` | `ui.settings.models`; none | `llm` runtime and catalogue (A04), `secret.Key` (A17), `settingspage.Key` | Routes, keys, model overlays, route check |
| `ui/accountpage` | `ui.settings.account`; none | `account.Key`, `authz.Key`, `interact.Key` (A17) | The vendor account panel and notices; mounted only with `account.deepseek` |
| `ui/pluginspage` | `ui.plugins`; none | `plugins.Manager` (A21), `plugin.Key` (A22), `inventory.Key` and `dirbrowse` (A18) | Plugin directories and composed entries |
| `ui/connectionspage` | `ui.settings.connections`; none | `mcp.Key`, `web.Key` (A15), `settingspage.Key` | MCP servers, connector accounts, web providers |
| `ui/permissionui` | `ui.permission`; none | `preset.Key`, `approval.Key` (A17) | The preset control and picker |
| `ui/skillui` | `ui.skills`; none | `skill.Key` (A14), `toolcard.Key`, `render.Key` (C4) | Skills page, preview, the `skill` tool card |

```go
// package settingspage: illustrative, not final
type Registry interface {
    Section(owner *cordis.Context, s Section) error // an effect of owner
    Card(owner *cordis.Context, c Card) error       // an effect of owner; replaces the generated card of that component
}
type Section struct {
    ID string; Title msg.Key; Order int
    Body func(ctx context.Context, rq ui.Req) (ui.SafeHTML, error) // nil: the section shows the cards placed in it
}
type Card struct {
    Component string       // catalogue name, e.g. "shell.bash"; every composed entry of it gets the card
    Config    reflect.Type // must equal the catalogue's config type for Component, or registration fails
    Section   string; Order int; Title, Help msg.Key
    Fields    []Field             // nil: every live field, in struct order
    View      ui.View[CardData]   // zero: the generated view
}
type Field struct {
    Path string; Control Control; Label, Help msg.Key
    Options func(ctx context.Context) []Option // for a choice whose values are host state (routes, presets)
    Restart bool                                // offered although not live: saving restarts the entry
}
type CardData struct { // all a card view receives (ui-core: a view gets data, never a service)
    Form settings.Form; Fields []FieldData // value, source layer, state, problem, option list
    Draft map[string]string; Outcome *Outcome
}
func Decode(c Card, f settings.Form, posted url.Values) ([]settings.PathOp, []Problem) // used by A19's settings.write handler
```

**The settings page.** `GET /settings?section=<id>` (area A19's `settings.read`) renders the shell of area C1 around a two-part view: a `<nav>` of sections in `Order`, and the selected section. It is an ordinary scrolling page, not a modal panel, so a section has an address and a phone browser scrolls it natively. A section with no `Body` lists, in `Order`, the cards placed in it; an entry with live fields and no placement appears under **Advanced**, so nothing configurable is hidden. Above the sections the page shows `config.Service.Status()` whenever the last reload had issues or an edit is unsaved: a hand-edited layer file that was rejected is named with line and column in the place people look. Regions this area declares with `Shell.Declare`: `settings.general.rows` (List: rows of other areas, such as appearance from C2 and composer preferences from C3), `plugins.detail` (List: sections on a plugin or entry page) and `models.route` (Keyed by route: extra content on a provider row). The settings entry in `shell.nav`, and the shortcut command `settings.open`, are contributions of `ui.settings`.

**How a component gets a settings card.** (1) *Nothing to do:* a component whose config struct has fields tagged live ([CF-3](../design.md#8-conflicts-and-gaps-between-the-foundation-recommendations)) is described by `settings.Forms.Describe` and gets a generated card. Its labels are catalogue keys `settings.<component>.<path>.label` and `.help`, registered by the component with `msg.Catalog`; a test fails for a live field without both in English and Chinese. (2) *Placement:* a `Card` with only `Section`, `Order` and `Title` moves the generated card out of Advanced. (3) *A written card:* `View` names a registered template that receives the same `CardData` and posts the same fields, so layout is free and the write path is not. `Card.Config` ties the registration to the Go type: a card for a type the catalogue does not hold, or a `Field.Path` the type lacks, fails the component at load.

**Controls.** One table maps the `schema` kind of a field to a control; a written card may choose another control of the same kind.

| Field kind (from the Go type) | Control | Notes |
|---|---|---|
| boolean | checkbox styled as a switch | |
| integer, number, byte size | number input with `min`, `max`, `step` from the type | the browser checks range before the host does |
| duration | number and a unit select | stored as a duration |
| closed set of strings | select, or radio buttons under five options | option labels from the catalogue |
| string; list of strings | text input; one entry per line | |
| credential reference | a link to the reference's own key form ([§6.2](#62-contracts)) | presence and masked preview only |
| struct; map | a nested fieldset; a table with add and remove rows | |
| anything else | read-only text and the layer file's path | never a broken editor |

**Where each upstream package went.**

| Upstream package | What the person does | Placement | Host state and its owner |
|---|---|---|---|
| `ui-settings`, `ui-settings-general` | Opens settings, picks a section, edits and saves | Page `/settings`; cards as fragments | Composed config through `settings.Forms` (A07); the connection indicator and release prompt are C1's; version and "this browser" are A25's |
| `ui-settings-agent-loop`, `-shell`, `-subagent` | Changes a few numbers; picks subagent routes | Generated cards in section `agent`; one written field (`Options` from A04's routes) | `tools` and `driver` config (A03, A02), `shell/bash.Config` or `shell/pwsh.Config` (A09), `subagent.Config` and the live setting behind `subagent.RoutesKey` (A12) |
| `ui-settings-session-log` | Turns session-log upload on | A row of the sharing page | `logupload.Config.Enabled` (A05), through the gate (A18) |
| `ui-settings-models` | Adds a provider, enters a key, edits models | Section `models`; written card per route | `keyed.Config.Routes`, vendor route config, catalogue (A04); secret store (A17) |
| `ui-settings-account`, `ui-account-notifications` | Signs in and out, reads balance, reads notices | Section `account`; a contribution to `shell.notices` | `account.Service`, `authz.Service` (A17) |
| `ui-settings-web-search`, `ui-connector-account` | Picks providers, links an account | Section `connections` | `web.Service`, `mcp.Service`, `connector.AccountView` (A15) |
| `ui-plugin-manager` | Adds, switches, restarts, removes a plugin | Pages `/plugins`, `/plugins/{pid}` | `plugins.Manager` (A21), `plugin.Descriptor` (A22), inventory (A18) |
| `ui-plugin-market` | Browses and installs from an index | Recorded drop ([DH-ADR-13](../design.md#dh-adr-13)) | none |
| `ui-permission-presets` | Chooses a preset; sets the default | Fragment `permission.picker`; a General row | `preset.Service`, `permissions` projection, `Grants` (A17) |
| `ui-skill` | Lists skills, reads one, sees a skill call | Fragments in `#aside`; a tool card; the `/` menu is C3's | `skill.Registry` (A14); logged tool events |

No view of this area needs an island: every interaction is a link or a form.

### 6.2 Contracts

**The form protocol** ([DH-ADR-UISET-2](#dh-adr-uiset-2), [DH-ADR-UISET-3](#dh-adr-uiset-3)).

1. A card is rendered at a revision. Each field shows its effective value, a badge for where it comes from (default, bundle, "Overridden" when the writable layer sets it, "Set by `<layer>`" when a higher layer does, in which case it is read-only), and its field state.
2. The person edits. Nothing is sent. Field text is kept by the per-tab store (`data-draft`), so a swap or reload does not lose it.
3. Save is `PUT /settings/{entry}` with the fields, the revision and the request id. `Decode` turns it into path operations: `set` for a changed value, `reset` for an emptied field or a ticked "Reset to default", `clear` for an explicit "none". An untouched field produces no operation, so a save never pins a value the person did not change.
4. The answer is the card's region with a save outcome:

| Result | What the card shows | What changed |
|---|---|---|
| Applied in place | "Saved. Applies from the next use." | The layer file and the running value |
| Applied by restart | "Saved. `<component>` was restarted." Taken from `loader.Reloaded`, not assumed | As above; that one entry restarted |
| 422, a field failed validation | The same form, a message beside each field, the draft intact; a credential field is re-rendered empty | Nothing |
| 409 `settings/conflict` | Current values at the new revision, the draft kept in the inputs, each field that changed elsewhere marked "now `<value>`" | Nothing; saving again uses the new revision |
| Shadowed by a higher layer | The message names the layer; the field becomes read-only | Nothing |
| `settings/unsaved` | "The layer file could not be written: `<error>`" with a retry | Nothing on disk |
| Saved, then the component failed | The card shows the entry's failure from the inventory beside the field, with "Reset to default" | The file is saved and the entry is failed: not rolled back |

5. **A change made elsewhere.** A card region is an interest (`settings`, keyed by entry). On `settings.FormChanged` the stream replaces it in every tab that shows it, and the per-tab store puts the tab's unsaved text back into the new markup. Another `dsh` process that shares the home writes the same layer file; this host's layer watch reloads it ([boot.md §6.3](boot.md)) and the same event follows, at most one poll interval later. A save made inside that window is refused as a conflict, because `Apply` re-reads every layer under the file lock.
6. **A field that needs a restart** and is offered by a written card (`Field.Restart`) is saved in two steps: `POST /settings/{entry}/preview` composes a candidate with `config.Service.Preview`, writes nothing, and answers a dialog naming every entry that would restart; `POST /settings/{entry}/apply` applies it with `config.Service.Apply` at the previewed revision. Every other non-live field is `file-only`: the card shows its value and the writable layer's path (`Forms.DocumentPath()`), and saving that file applies it through the validated reload.

**URL routes this area registers** through `web.Routes`, under the rules of [api.md §6.2](api.md#62-contracts). Area A19 owns the route table, the stream and the routes `settings.read`, `settings.write` and the credential routes, which this area's views are rendered by. Access is `access.Owner()` unless stated; `S` is `access.Session("sid")`. Every state-changing form carries a request id.

| Method and pattern (name) | Returns and rules |
|---|---|
| `POST /settings/{entry}/preview`, `POST /settings/{entry}/apply` (`settings.preview`, `settings.apply`) | Step 6 above. `{entry}` is opaque |
| `GET /plugins`, `GET /plugins/{pid}` (`plugin.list`, `plugin.detail`) | Pages. `{pid}` is opaque: an entry id or plugin id |
| `POST /plugins/inspect` (`plugin.inspect`) | Form: a directory. Answer: the inspection report with the grant form. Runs no plugin code |
| `POST /plugins` (`plugin.add`) | Form: directory, the digest the report showed, the ticked capabilities, credential choices. `HX-Location` to the new plugin |
| `PUT /plugins/{pid}/enabled`, `PUT /plugins/{pid}/grants`, `POST /plugins/{pid}/restart`, `DELETE /plugins/{pid}` | One `plugins.Manager` operation each; the answer shows `saved` and `application` separately, as the manager reports them |
| `GET /providers/new?q=`, `POST /providers`, `DELETE /providers/{route}` (`provider.pick`, `provider.add`, `provider.remove`) | The searchable catalogue list with "custom endpoint" pinned; create and remove a model route. `{route}` is opaque |
| `POST /providers/{route}/check`, `POST /providers/{route}/discover` | Area A04's route check (states that it sends a real request) and model discovery |
| `GET /account`, `POST /account/sign-in`, `POST /account/sign-in/{aid}/cancel`, `GET`, `POST /account/sign-out`, `POST /account/notices/{nid}/dismiss` | The panel reads profile and balance only now. Sign-in answers a fragment with a link. `GET` sign-out is the confirmation, which lists turns running on the account route |
| `POST /connections/{name}/authorize`, `.../cancel`, `.../sign-out`, `.../restart`, `.../probe` | One `mcp.Service` call each; `{name}` is opaque |
| `GET /sessions/{sid}/permission`, `PUT /sessions/{sid}/permission` (`permission.picker`, `permission.set`) | The picker; the switch (`MayResume`). Access: `S` to read, owner to set |
| `GET /sessions/{sid}/skills`, `GET /sessions/{sid}/skills/{name}` (`skill.list`, `skill.preview`) | The session's catalogue; one skill's text through C4's renderer. Access: `S`. Never wakes a session |

**Stream.** State sources registered with A19's hub, all on the `view` event, each a whole region replaced by `id`; no new event name. `settings` (key: entry) on `settings.FormChanged`; `sharing` on `sharing.Changed`; `plugins` on `plugin.StateChanged` and `inventory.Changed`; `providers` on `llm.ModelsChanged` and a credential change; `account` on `account.SignedOut`, `account.Expired` and `interact.Changed` for the peer; `connections` on the fleet's status change and `web.ProvidersChanged`; `permission` (key: session) on the `permissions` projection, `preset.CatalogChanged` and `grant.Changed`; `skills` (key: session) on `skill.Changed`.

**The sharing page** ([DH-ADR-UISET-4](#dh-adr-uiset-4)). The first control is the off-switch, labelled "Send nothing from this machine". It writes the live field `sharing.Off`; when `Gate.Off()` reports it locked by `DSH_SHARING_OFF`, it is shown on, disabled, with that reason. Below it is one row per entry of `Gate.Senders()`, never a list this area keeps: the sender's own description (what is sent, to whom, under which identity), the destination or "no destination is configured", the state (`off`, `on`, `unavailable`, `stopped by the switch`) and the switch, which is a one-field form on the entry that owns the setting. There is no "turn everything on". The text for the kinds area A18 lists today:

| Kind | What it sends, in the page's words | Setting it switches (owner) |
|---|---|---|
| `session-log-contribution` | "With each request to the vendor's model, the new lines of this session's log since the last upload." | `logupload.Config.Enabled` (A05) |
| `session-upload` | "When you send feedback and confirm the preview: the events of that session, with secrets masked." | `upload.Config.Enabled` (A06) |
| `feedback` | "The text and rating you typed in a feedback form, after you have seen exactly what will be sent." | the feedback form's send step (A17) |
| `model-request-extras` | "Added to model requests on a route: an installation id, a session label, the list of installed components, the client name and version. Each is switched per route." The row opens the per-route switches | `transport.Send` of each route (A04) |
| `analytics` | "Counts of which features were used: fixed event names with numbers and yes or no values, never text you typed, under an anonymous installation id." | `report.Config.Analytics` (A18) |
| `telemetry` | "Start, crash and component-failure codes." | `report.Config.Telemetry` (A18) |
| `update-check` | "Once a day, a question to the release address whether a newer version exists." | `update.Config.check` (A24) |

A row whose sender attaches the installation id says that one "will be created" until it exists (A17's `identity`). A closing list names what the page does **not** switch, in A18's words: model requests to a provider the user configured, web search and fetch, MCP, SSH and plugin traffic, a clicked remote image, sign-in.

**Plugins** ([DH-ADR-UISET-5](#dh-adr-uiset-5)). `/plugins` lists plugin processes (`plugin.Service.Plugins()`: installed, and attached to a session, read-only) and composed entries (the inventory: component, layer, phase, diagnostic), each entry linking to its card. Adding one: (1) the person types or browses to a directory (A18's directory browser); (2) `plugin.inspect` shows the report: name, version, protocol, the start command as an argument list, problems (any problem removes the Add button), the digest and size, each requested capability with its parameters, the manifest's stated reason and the catalogue's plain meaning, each requested credential with a select of existing references, and the confinement facts, including every effect the backend cannot enforce; (3) every capability is an unticked checkbox; (4) `plugin.add` calls `Manager.Add` with the directory, the digest that was shown and the ticked subset. If the directory changed in between, the digest differs, nothing is written and the new report is shown. A plugin page shows status with its reason (`failed{stage, message}`, `waiting-grant`), restarts and last exit, tools, requested against granted capabilities (changing them restarts that entry), and a banner when `SourceChanged` is set: "The directory changed after approval; the approved copy still runs. Inspect again to approve the new content." Remove asks first and says what stays: the operator's directory is never touched.

**Models and keys** ([DH-ADR-UISET-6](#dh-adr-uiset-6)). The list is `llm.Runtime.Routes()`: every registered route has a row; a route no config entry of the page can edit is read-only and names the component that provides it. A row shows protocol, endpoint host, credential state, model count and the last listing error. Creating a route writes the route and its credential reference (`<ROUTE>_API_KEY` unless one is named) in one operation list; the route id is then fixed, because other configuration and logged sessions name it. The key is a separate form on the row that posts to A19's credential route: `type="password"`, `autocomplete="new-password"`, never a `value`; a stored key shows `secret.Store.Describe`'s masked preview, its source and its protection. An environment-sourced key is shown as such and cannot be replaced or deleted from the page. Removing a route offers "also delete the stored key" only when the reference is in the managed store and no other entry names it; the credentials list (A19) shows every reference with the entries that use it, so a key that is no longer used is visible and deletable.

**Events.** This area declares no live event and no durable session event; it consumes the events named under "Stream". **Tools exposed to the model:** none.

**Config.** This area declares no config struct of its own: what a page shows is the config of the component it edits.

| Upstream unit | Go replacement |
|---|---|
| `UP-CFG-dsh-client-ui-settings-models` (`credentialOnboarding`) | Area A25's `setup.Config.Enabled`, which turns the first-run page off for a provisioned deployment |
| `UP-CFG-dsh-client-ui-account-notifications` (`notificationPollIntervalMs`) | Area A17's `account.deepseek` config: `Notifications` (false). The interval is requested of A17 ([§10](#10-open-questions-and-limitations)) |
| `UP-CFG-dsh-client-ui-settings-account` (questionnaire address, introduction progress) | None: both subjects are dropped ([DH-ADR-9](../design.md#dh-adr-9), [DH-ADR-18](../design.md#dh-adr-18)) |
| `UP-CFG-dsh-client-ui-plugin-manager` (registry probe) | None: no registry is contacted ([DH-ADR-13](../design.md#dh-adr-13)) |

**Services.**

| Upstream unit | Go replacement |
|---|---|
| `UP-SVC-connectorAccounts` | None in this area. Merged into area A15's `connector` contract (`AccountView`, `AccountSource`) and `mcp.Service.Accounts()`; the page renders those values |
| `UP-SVC-pluginRegistryProbe` | None: dropped with the registry ([DH-ADR-13](../design.md#dh-adr-13)) |
| (new) | `settingspage.Key`, interface `Registry` |

**Persisted data.** None of its own. Host preferences declared through C1's `prefs.Declare`: `coding-tools` (true) and `settings.section` (the last opened section). Unsaved field text lives in the per-tab store.

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership and access.** A section, a card, a region, a URL route and a stream source are effects of the fiber that registered them. This area owns nothing else. A layer file is the operator's and is written only by area A21 under its lock; a plugin directory is the operator's and is only read; a secret is the store's; a session grant is its session's. A page gives an owner principal *access* to those services' own operations and adds none.

| Situation | What happens | Undone, retried, uncertain |
|---|---|---|
| A component with a card is disabled or fails to load | Its card and section are absent on the next render; a failed entry is listed with its diagnostic on `/plugins` | Nothing to clean up |
| A written card's view fails | The generated card is rendered in its place with a notice, and the failure is logged once | Nothing partial is written |
| A save is refused (validation, conflict, shadow) | The outcome table of [§6.2](#62-contracts) | Nothing was written |
| The connection drops during a save | The form stays filled; the person submits again | **Uncertain** whether the first submit applied; the request id makes the repeat harmless, and a repeat after a successful write reports a conflict that shows the new value |
| A save applies and the component then fails | The card and `/plugins` show the failure; nothing is rolled back automatically | The person resets the field; the previous value is shown as the inherited one |
| The route was created and the key write failed | The row exists and says "no key stored"; the key form is still there | Retried by submitting the key form again |
| `plugin.add` is refused, or the answer is lost | Refused: nothing is written. Lost: the list shows whether the entry exists | No partial copy: the approved copy is made by A22 when the entry starts |
| A plugin fails after it was added | Its status and last exit are on its page; restart gives a fresh budget (A22) | **Uncertain:** what the plugin did before it failed |
| A sign-in is cancelled, abandoned, or the host restarts | The pending attempt is an interaction of A17: a reload re-attaches, a cancel is final, a restart abandons it | Nothing stored before success |
| Sign-out while a turn uses the account route | The confirmation lists those turns; they end with cause `revoked` (A17) | The vendor-side token may stay valid if revocation fails; the panel says so |
| The sharing gate or a sender is not mounted | No row: a sender that is not composed cannot send | Nothing |
| A switch is turned off while a request is in flight | The permit ends and the request is cancelled (A18) | **Uncertain** whether it had arrived; the row says "stopped" |

## 7. Decisions

<a id="dh-adr-uiset-1"></a>
### DH-ADR-UISET-1 A settings card is an effect of a host component, generated from its config type unless a template is registered; settings is one page with an address per section
- **Decision.** The registry of [§6.1](#61-shape). Every composed entry with live fields has a card without any UI code. A component may place it, or register a template for the same form, and both registrations are effects. Plugin-process management is a different page.
- **Why.** Upstream needs a browser package per namespace, an empty host half to carry it and a "while served" rule to remove it. With the form derived from the type ([../design.md §4.9](../design.md#49-configuration-model)) and the registration tied to a fiber ([DH-O-1](../design.md#dh-o-1)), the three mechanisms have nothing left to do.
- **Tradeoff.** A generated card is plain. Every live field needs label and help text in two languages, which is a duty on every area and is checked by a test, not by a compiler. A plugin process cannot have a card.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-1](../design.md#dh-o-1), [DH-O-2](../design.md#dh-o-2). **Verified by.** DH-T-UISET-1, DH-T-UISET-2, [DH-E-UISET-1](#dh-e-uiset-1).

<a id="dh-adr-uiset-2"></a>
### DH-ADR-UISET-2 One form protocol: staged, revision-checked, validated only on the host, and a conflict never loses the draft
- **Decision.** Steps 1 to 5 of [§6.2](#62-contracts). The settings document is shared by every admitted browser and every process on the home; the revision is the only arbiter. The browser's part is the input attributes the type implies.
- **Why.** Upstream's staged editing, revision fence and "authentication is the boundary" are sound and language-neutral. Its second validator in the browser exists to avoid a round trip that a form already makes.
- **Tradeoff.** Validation feedback costs a request. A region replaced from the stream relies on C1's per-tab store to put unsaved text back ([§10](#10-open-questions-and-limitations)).
- **Serves.** [DH-O-2](../design.md#dh-o-2), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UISET-3, DH-T-UISET-4, DH-T-UISET-6, [DH-E-UISET-1](#dh-e-uiset-1).

<a id="dh-adr-uiset-3"></a>
### DH-ADR-UISET-3 A save says what the loader did; a restart is previewed and confirmed; everything else is honestly file-only
- **Decision.** Every field has a state. A live field is saved through `settings.Forms.Apply` and the outcome is read from `loader.Reloaded`. A restart field is offered only where a written card names it, and is saved through `config.Service.Preview` and `Apply` after a dialog that names what restarts. Any other field is shown read-only with the layer file's path. The page never opens an editor on the host.
- **Why.** Area A07 lets a form change only what cannot restart anything; area A21 already has a checked edit path for the rest. Offering both through one confirmation keeps one write path and no surprise.
- **Tradeoff.** Two URL routes beside A19's. A restart edit is not a transaction across entries ([../design.md §5](../design.md#5-failure-retry-rollback-and-cleanup)): the preview shows what will restart, not whether it will start.
- **Serves.** [DH-O-2](../design.md#dh-o-2). **Verified by.** DH-T-UISET-5, DH-T-UISET-22.

<a id="dh-adr-uiset-4"></a>
### DH-ADR-UISET-4 Everything that leaves the machine is switched on one page that is generated from the gate
- **Decision.** The sharing page of [§6.2](#62-contracts): the off-switch first, then exactly the gate's senders, each in the sender's own words, then what is not switched. No sharing switch appears on any other page except as a link to this one. No control turns several kinds on.
- **Why.** [DH-ADR-9](../design.md#dh-adr-9) says "each a visible setting". A list the page kept by hand could omit a sender; a list read from the gate cannot, because a component may send only with a permit from that gate ([DH-ADR-HOST-5](host-telemetry.md#dh-adr-host-5)).
- **Tradeoff.** The page is only as complete as the gate. The switches live in several entries, so the page is several small forms, and the page needs each sender's settings address from A18 ([§10](#10-open-questions-and-limitations)).
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Verified by.** DH-T-UISET-8, DH-T-UISET-9, [DH-E-UISET-2](#dh-e-uiset-2).

<a id="dh-adr-uiset-5"></a>
### DH-ADR-UISET-5 The plugins page manages local directories: inspect, consent on the report, grant; there is no market
- **Decision.** The flow of [§6.2](#62-contracts). Consent is a form whose checkboxes start empty and whose submit names the digest it saw. `ui-plugin-market`, the registry probe, install progress, version lists and update prompts are not built. A compiled-in component adds to a plugin page through the `plugins.detail` region; a plugin process adds nothing.
- **Why.** [DH-ADR-13](../design.md#dh-adr-13). Binding the form to the digest makes the page's consent the same fact as A22's grant record ([DH-ADR-EXT-3](extensions.md#dh-adr-ext-3)).
- **Tradeoff.** No discovery and no update notice: the operator replaces a directory and approves it again. A long report must be read; nothing is pre-ticked to shorten it.
- **Serves.** [DH-O-11](../design.md#dh-o-11), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UISET-16, DH-T-UISET-17, DH-T-UISET-18, [DH-E-UISET-4](#dh-e-uiset-4).

<a id="dh-adr-uiset-6"></a>
### DH-ADR-UISET-6 Model routes are listed from the runtime; a key is its own write-only form and is never rendered back
- **Decision.** As [§6.2](#62-contracts). A written card covers the common fields and a generated fold covers every other live field of the route. A secret input never has a value, a rejected key is re-rendered empty, and no script touches a key.
- **Why.** Listing from `Routes()` removes "a route that renders nowhere". Separating the key form removes upstream's half-saved state inside one Save. [../design.md §6](../design.md#6-security-model), rule 4.
- **Tradeoff.** Two saves to add a provider with a key. No "show what I typed". Cloud sign-in routes ([DH-ADR-25](../design.md#dh-adr-25)) appear only when their components exist.
- **Serves.** [DH-O-8](../design.md#dh-o-8), [DH-O-7](../design.md#dh-o-7). **Verified by.** DH-T-UISET-10 to DH-T-UISET-13, [DH-E-UISET-3](#dh-e-uiset-3).

<a id="dh-adr-uiset-7"></a>
### DH-ADR-UISET-7 The host decides what an account shows; an authorization page is a link the person clicks; vendor traffic needs an action or an opt-in
- **Decision.** Connector and vendor accounts are rendered from host-computed views (A15, A17). Starting a sign-in answers a fragment with an ordinary link to the authorization address (a local host also asks the operating system to open it). Opening a page starts no check. Profile and balance are read when the panel is opened. Notices are off by default; when on, the host reports "dismissed" and "clicked" when the person acts and "displayed" when a notice is first rendered into a response. The questionnaire link, the desktop introduction and the embedded vendor page are not built.
- **Why.** A click is a user gesture, so no pop-up blocker applies and no tab is pre-opened. [DH-ADR-9](../design.md#dh-adr-9), [DH-ADR-18](../design.md#dh-adr-18), [DH-ADR-HUMAN-11](interaction-credentials.md#dh-adr-human-11).
- **Tradeoff.** One more click to sign in. "Displayed" means rendered, not seen. Vendor sign-in still needs a loopback address; the panel says so instead of failing. Scope boundary of this record: a vendor account notice is shown as plain text with two actions (dismiss, and open the account section), and a failed poll keeps the cards already shown, because rendering the vendor's markup or links would mean trusting a third party's content inside an owner's page.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Verified by.** DH-T-UISET-14, DH-T-UISET-15, DH-T-UISET-19, [DH-E-UISET-5](#dh-e-uiset-5).

<a id="dh-adr-uiset-8"></a>
### DH-ADR-UISET-8 Choosing a preset is one host form over the preset service; grants are shown, not reimplemented
- **Decision.** The picker lists area A17's presets with catalogue text for the shipped ones and the operator's own text for others, marks the current one (`custom` is a label, never a choice), and states how many session grants the switch will revoke. `danger-full-access` needs a second confirmation. `auto` appears only if the experimental review layer registers it. The default for new sessions is an ordinary field of the `permission.presets` card. Listing and revoking grants is area C4's strip ([ui-conversation.md](ui-conversation.md#dh-adr-uiconv-7)), which the picker embeds.
- **Why.** A switch is an owner action that revokes grants ([DH-ADR-HUMAN-6](interaction-credentials.md#dh-adr-human-6)); the person must see that before confirming. One grants view avoids two that disagree.
- **Tradeoff.** The control's seat in the composer row depends on C3 ([§10](#10-open-questions-and-limitations)).
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UISET-20, [DH-E-UISET-6](#dh-e-uiset-6).

<a id="dh-adr-uiset-9"></a>
### DH-ADR-UISET-9 Skills are rendered from the host registry on request; a skill call is drawn from the log alone
- **Decision.** The skills view is `Registry.Snapshot` for the session's view: winners, shadowed definitions, provider status and diagnostics, with the source class shown. A preview is untrusted Markdown through C4's renderer. The `skill` tool card reads only the logged call and result. There is no browser cache of the catalogue. Switching a source on or off is a field of area A14's provider config.
- **Why.** Upstream's cache and its three invalidation events are the cause of five limits. Replay must not change when the installed skills change.
- **Tradeoff.** A per-skill off switch does not exist until A14 offers one ([§10](#10-open-questions-and-limitations)). A reference stays text, by A14's rule.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-4](../design.md#dh-o-4). **Verified by.** DH-T-UISET-21, [DH-E-UISET-6](#dh-e-uiset-6).

## 8. Deliverables

<a id="dh-d-uiset-1"></a>
### DH-D-UISET-1 Any component's live settings can be edited in the browser, and a bad or stale edit changes nothing
- **Result and why it matters.** One page lists everything that can be changed while the system runs, shows where each value comes from, and reports what a save did. Without it, configuration is a YAML file.
- **How it is implemented.** `settingspage.Registry` stores sections and cards as effects and checks each card against the component catalogue at load. The generated view walks the `settings.Form` schema with the control table; `Decode` builds path operations; the outcome is assembled from `Apply`'s result, `loader.Reloaded` and the inventory. The `settings` stream source replaces regions on `settings.FormChanged`.
- **Components and files.** `internal/ui/settingspage`. **Depends on.** [DH-D-STORAGE-8](storage.md#dh-d-storage-8), [DH-D-API-7](api.md#dh-d-api-7), [DH-D-BOOT-7](boot.md#dh-d-boot-7). [DH-D-UICORE-1](ui-core.md#dh-d-uicore-1), [DH-D-UICORE-2](ui-core.md#dh-d-uicore-2), [DH-D-UICORE-5](ui-core.md#dh-d-uicore-5) and [DH-D-UICORE-7](ui-core.md#dh-d-uicore-7) (the shell, regions, per-tab store and catalogue), [DH-D-UIKIT-1](ui-kit.md#dh-d-uikit-1) and [DH-D-UIKIT-4](ui-kit.md#dh-d-uikit-4) (form, badge and dialog partials), [DH-D-UTIL-1](util-test-support.md#dh-d-util-1) (schema kinds and the live tag). Runtime prerequisite: an owner principal.
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-2](../design.md#dh-o-2), [DH-O-1](../design.md#dh-o-1). **Priority.** P1. **Done when.** DH-T-UISET-1, DH-T-UISET-2, DH-T-UISET-3, DH-T-UISET-4, DH-T-UISET-5 and DH-T-UISET-6 pass and [DH-E-UISET-1](#dh-e-uiset-1) has passed.

<a id="dh-d-uiset-2"></a>
### DH-D-UISET-2 The General section holds the everyday preferences, whoever owns them
- **Result and why it matters.** Language, appearance, coding tools, the default permission preset and the composer preferences are in one section, with the writable layer's path and links to "about" and "this browser".
- **How it is implemented.** The section renders the `settings.general.rows` region. This area contributes the language row (it sets area C1's preference `ui.language` through `ui.pref`; the choice follows the principal, not the browser), the coding-tools row (a host preference) and the default-preset row; areas C2, C3 and C4 contribute theirs.
- **Components and files.** `internal/ui/settingspage/general`. **Depends on.** [DH-D-UISET-1](#dh-d-uiset-1), [DH-D-HUMAN-4](interaction-credentials.md#dh-d-human-4). [DH-D-UICORE-5](ui-core.md#dh-d-uicore-5) and [DH-D-UICORE-7](ui-core.md#dh-d-uicore-7) (the language preference `ui.language`). Area A25 contributes the about page and the this-browser fragment.
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UISET-7 passes and [DH-E-UISET-1](#dh-e-uiset-1) has passed.

<a id="dh-d-uiset-3"></a>
### DH-D-UISET-3 One page shows every kind of data that can leave the machine, each with its switch, and one switch that stops them all
- **Result and why it matters.** A person can read, in plain words, what would be sent and to whom before turning anything on, and can stop everything at once. It is the visible half of [DH-ADR-9](../design.md#dh-adr-9).
- **How it is implemented.** The section body renders `Gate.Off()` and `Gate.Senders()`; each switch is a one-field form on the entry that owns it, found through the settings address that `sharing.Status` carries for each kind, so this area keeps no table of kinds (A18 lists ten today, among them `account-notices`, `translation` and `speech-audio`); the `sharing` source replaces the region on `sharing.Changed`. The off-switch is labelled for what it does ([DH-ADR-27](../design.md#dh-adr-27)): "stops everything the harness would send on its own". Beneath it the page lists what the switch does not stop, because the user asks for each explicitly: a model request, an explicit `dsh update`, a live share the user created, a code-host status read the user configured. The update check and the client version are two separate switches, both off until turned on ([DH-ADR-32](../design.md#dh-adr-32)).
- **Components and files.** `internal/ui/sharingpage`. **Depends on.** [DH-D-UISET-1](#dh-d-uiset-1), [DH-D-HOST-4](host-telemetry.md#dh-d-host-4) (the gate, its kinds table and each sender's settings address). **Enable condition:** no sender ships in a `web` profile before this page does.
- **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UISET-8 and DH-T-UISET-9 pass and [DH-E-UISET-2](#dh-e-uiset-2) has passed.

<a id="dh-d-uiset-4"></a>
### DH-D-UISET-4 A person adds a model provider, stores its key and edits its models, and no page ever shows the key
- **Result and why it matters.** Any key-authenticated provider on four protocols is set up in the browser and works without a restart; the key stays behind the secret store.
- **How it is implemented.** A section over `Routes()`; a written card per route kind with a generated fold; the searchable catalogue picker, which shows each vendor's display name with its identifier as secondary text and filters on the name, the identifier and the search aliases (register entry U-K2-11; the name and aliases are fields of area A04's embedded catalogue, and a vendor without a name is shown by its identifier); the separate key form over A19's credential routes; route check and discovery as actions that render A04's classified results.
- **Components and files.** `internal/ui/modelspage`. **Depends on.** [DH-D-UISET-1](#dh-d-uiset-1), [DH-D-LLM-4](llm.md#dh-d-llm-4), [DH-D-HUMAN-7](interaction-credentials.md#dh-d-human-7), [DH-D-API-7](api.md#dh-d-api-7). Area A25's first-run page asks for the first key through the same form. Runtime prerequisite: a provider account.
- **Outcome.** [DH-O-8](../design.md#dh-o-8), [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UISET-10, DH-T-UISET-11, DH-T-UISET-12 and DH-T-UISET-13 pass and [DH-E-UISET-3](#dh-e-uiset-3) has passed.

<a id="dh-d-uiset-5"></a>
### DH-D-UISET-5 A person signs in to the vendor account, sees profile and balance, signs out, and may opt in to notices
- **Result and why it matters.** The account can replace a key, and the vendor is contacted only when the person acts or has opted in.
- **How it is implemented.** The panel over `account.Service`; sign-in as an `authz` attempt whose pending state is an interaction; balances formatted on the host; notices as a contribution to `shell.notices` fed by A17's poll.
- **Components and files.** `internal/ui/accountpage`. **Depends on.** [DH-D-UISET-1](#dh-d-uiset-1), [DH-D-HUMAN-8](interaction-credentials.md#dh-d-human-8), [DH-D-HUMAN-9](interaction-credentials.md#dh-d-human-9). Runtime prerequisites: the `account.deepseek` component, a vendor account, a loopback address.
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Priority.** P2. **Done when.** DH-T-UISET-14 and DH-T-UISET-15 pass and [DH-E-UISET-5](#dh-e-uiset-5) has passed.

<a id="dh-d-uiset-6"></a>
### DH-D-UISET-6 A person adds a plugin from a local directory after reading what it asks for, and manages it afterwards
- **Result and why it matters.** The grant a plugin runs under is the one a person ticked for the bytes they were shown; a failed plugin says why.
- **How it is implemented.** Views over `plugins.Manager`, `plugin.Descriptor` and the inventory; the inspect and add forms of [§6.2](#62-contracts); the `plugins` stream source.
- **Components and files.** `internal/ui/pluginspage`. **Depends on.** [DH-D-BOOT-9](boot.md#dh-d-boot-9), [DH-D-EXT-1](extensions.md#dh-d-ext-1), [DH-D-EXT-3](extensions.md#dh-d-ext-3), [DH-D-HOST-10](host-telemetry.md#dh-d-host-10), [DH-D-HOST-8](host-telemetry.md#dh-d-host-8), [DH-D-API-1](api.md#dh-d-api-1). Runtime prerequisite: a plugin directory with a manifest.
- **Outcome.** [DH-O-11](../design.md#dh-o-11), [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UISET-16, DH-T-UISET-17 and DH-T-UISET-18 pass and [DH-E-UISET-4](#dh-e-uiset-4) has passed.

<a id="dh-d-uiset-7"></a>
### DH-D-UISET-7 A person sees MCP servers, links connector accounts and chooses web providers, with the reason when something is unusable
- **Result and why it matters.** "Why does the model have no web search" is answered on the page that fixes it.
- **How it is implemented.** A section over `mcp.Service.Servers()` and `Accounts()`, rendering each account view's phase and actions as given; provider menus over `web.Service.Providers` with each availability reason, and the provider "automatic" resolves to now; provider settings as cards.
- **Components and files.** `internal/ui/connectionspage`. **Depends on.** [DH-D-UISET-1](#dh-d-uiset-1), [DH-D-CONNECT-2](mcp-lsp-web-connector.md#dh-d-connect-2), [DH-D-CONNECT-4](mcp-lsp-web-connector.md#dh-d-connect-4), [DH-D-CONNECT-6](mcp-lsp-web-connector.md#dh-d-connect-6), [DH-D-CONNECT-9](mcp-lsp-web-connector.md#dh-d-connect-9).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-10](../design.md#dh-o-10). **Priority.** P1. **Done when.** DH-T-UISET-19 passes and [DH-E-UISET-5](#dh-e-uiset-5) has passed.

<a id="dh-d-uiset-8"></a>
### DH-D-UISET-8 A person chooses how much the agent may do, for this session and for new ones, and sees the grants in force
- **Result and why it matters.** The one "Permissions" choice of area A17 is reachable from the conversation, and a switch never silently drops or keeps a grant.
- **How it is implemented.** A control contributed to area C3's seat `composer.controls` that shows the current permission preset and a grant count; the picker fragment, which is also what a bare `/permission` opens; `permission.set` calling `preset.Service` with the admitted actor. A Permissions section of the settings page shows, read-only, what protects this machine ([DH-ADR-28](../design.md#dh-adr-28); the data is area A18's and A10's): the sandbox state of the backend in use, A18's exposure list, and `Listener.PeerRule()` (`Exact`, `BestEffort` or `NotApplied`) with each takeover gap that exists on this machine in plain words, the same list `dsh sandbox status` prints; and the signed-in browsers with the "sign out everything" action. With no gap the section says so; it never hides the list.
- **Components and files.** `internal/ui/permissionui`. **Depends on.** [DH-D-HUMAN-3](interaction-credentials.md#dh-d-human-3), [DH-D-HUMAN-4](interaction-credentials.md#dh-d-human-4), [DH-D-UISET-2](#dh-d-uiset-2). [DH-D-UICHAT-1](ui-chat.md#dh-d-uichat-1) (the seat `composer.controls`), [DH-D-UICHAT-3](ui-chat.md#dh-d-uichat-3) (the picker hook of the command menu), [DH-D-UICONV-6](ui-conversation.md#dh-d-uiconv-6) (the grants strip, which says that a grant is never copied to a resumed session, a fork or a subagent), [DH-D-HOST-2](host-telemetry.md#dh-d-host-2) (the signed-in browsers, `PeerRule()` and the revoke-all route for the Permissions section), [DH-D-SANDBOX-3](sandbox-ssh.md#dh-d-sandbox-3) (the sandbox state and the takeover gaps of this machine).
- **Outcome.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UISET-20 passes and [DH-E-UISET-6](#dh-e-uiset-6) has passed.

<a id="dh-d-uiset-9"></a>
### DH-D-UISET-9 A person sees which skills a session has, where each comes from and why one is hidden, and reads a skill before using it
- **Result and why it matters.** A skill that arrived with a repository is visibly not the user's own, and a skill call in the transcript shows what was loaded.
- **How it is implemented.** The list and preview fragments over `skill.Registry`; a card on A14's provider config for the source switches; the `skill` card registered with `toolcard.Registry`; a text rule that links a logged skill invocation to the preview.
- **Components and files.** `internal/ui/skillui`. **Depends on.** [DH-D-CONTEXT-8](context-compaction-skill.md#dh-d-context-8), [DH-D-CONTEXT-9](context-compaction-skill.md#dh-d-context-9), [DH-D-UISET-1](#dh-d-uiset-1). [DH-D-UICONV-2](ui-conversation.md#dh-d-uiconv-2) and [DH-D-UICONV-4](ui-conversation.md#dh-d-uiconv-4) (the renderer and the card registry), [DH-D-UICHAT-3](ui-chat.md#dh-d-uichat-3) (skills in the `/` menu), [DH-D-UICHAT-5](ui-chat.md#dh-d-uichat-5) (the `usertext` view for a skill mention in sent text).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-4](../design.md#dh-o-4). **Priority.** P1. **Done when.** DH-T-UISET-21 passes and [DH-E-UISET-6](#dh-e-uiset-6) has passed.

<a id="dh-d-uiset-10"></a>
### DH-D-UISET-10 A setting that needs a restart can be changed in the browser after a preview of what restarts
- **Result and why it matters.** Fewer settings send a person to a YAML file, and none restarts anything unannounced.
- **How it is implemented.** `Field.Restart`, the two routes of step 6 in [§6.2](#62-contracts), and a dialog view over the candidate.
- **Components and files.** `internal/ui/settingspage`. **Depends on.** [DH-D-UISET-1](#dh-d-uiset-1), [DH-D-BOOT-7](boot.md#dh-d-boot-7) (with a candidate that lists the entries an operation restarts), [DH-D-BOOT-6](boot.md#dh-d-boot-6) (the preview).
- **Outcome.** [DH-O-2](../design.md#dh-o-2). **Priority.** P2. **Done when.** DH-T-UISET-22 passes.

## 9. Acceptance

### 9.1 Local tests

Fakes stand in for other areas' services unless "real loader" is stated. `D-n` is short for `DH-D-UISET-n`.

| Test ID | Proves | What is asserted |
|---|---|---|
| DH-T-UISET-1 | D-1 | A duplicate section, a card for an unknown component, a `Config` type that differs from the catalogue's and a field path the type lacks each fail registration; unloading the owner removes the card and the generated one returns |
| DH-T-UISET-2 | D-1 | Through the real loader, for each shipped profile: every entry with live fields renders a card (golden files); every live field has label and help in English and Chinese; controls follow the kind table; no template of this area holds literal product text |
| DH-T-UISET-3 | D-1 | `Decode`: changed is `set`, emptied or reset is `reset`, "none" is `clear`, untouched is no operation; an unknown field and a non-live path are refused |
| DH-T-UISET-4 | D-1 | 422 keeps the draft and writes nothing; 409 shows current values with the draft kept and marks fields changed elsewhere; a shadowed field names its layer and is read-only; `settings/unsaved` shows the error |
| DH-T-UISET-5 | D-1 | The outcome reads "restarted" only when `loader.Reloaded` lists the entry as restarted; a component that fails after a save shows its diagnostic on the card; a rejected reload is shown above the sections with file, line and column |
| DH-T-UISET-6 | D-1 | `settings.FormChanged` replaces the card region in a second tab's stream; two form services on the same layer files reach an open card after the poll; a non-owner gets 403 and no navigation entry |
| DH-T-UISET-7 | D-2 | Rows appear in contribution order; a language change is stored and the next response is Chinese; the default-preset options equal the presets allowed as a default; the layer path is shown and no route opens an editor |
| DH-T-UISET-8 | D-3 | With a fake gate of N senders: exactly N rows in gate order with each description and one of four states; a sender with no settings address renders read-only; the "not switched here" list is present and names a live share and a code-host status read; the page holds no table of kinds of its own |
| DH-T-UISET-9 | D-3 | The off-switch writes `sharing.Off`; locked by the environment it is disabled with the reason; through the real loader on a fresh home every sender of the `web` profile has a row and every switch is off; no other page of this area holds a sharing switch |
| DH-T-UISET-10 | D-4 | Every `Routes()` route has a row; a route without an editable entry is read-only; a route with a listing error stays editable; the picker filters by id and by name and keeps "custom endpoint" pinned |
| DH-T-UISET-11 | D-4 | With a known secret in the store, no response of any view of this area contains it; a key input has `autocomplete="new-password"` and no `value`; a rejected key is re-rendered empty; the picker finds a vendor by display name, identifier or alias and shows the name first |
| DH-T-UISET-12 | D-4 | Creating a route writes route and credential reference in one operation list; the id cannot be edited; removal offers key deletion only for an unshared managed-store reference |
| DH-T-UISET-13 | D-4 | Check and discover each call area A04 once per request id and render the classified result; the check button's text says a real request is sent |
| DH-T-UISET-14 | D-5 | Signed out: a sign-in control, or the loopback explanation when the public address is not loopback; start renders an anchor with the attempt's address and `rel="noopener noreferrer"`; a pending attempt is shown after a reload; profile and balance fail independently and never show zero; the balance format table; no link carries a token |
| DH-T-UISET-15 | D-5 | With notices off, no view causes a poll (recording fake) and no region is rendered; with notices on, text is escaped, two actions exist, a dismissal is reported once |
| DH-T-UISET-16 | D-6 | List and detail show status with its reason, the supplying layer, requested against granted capabilities and the `SourceChanged` banner; `plugin.StateChanged` replaces the region; a protected entry has no switch |
| DH-T-UISET-17 | D-6 | The report lists every requested capability with its reason, all unticked; a problem removes Add; `plugin.add` passes exactly the ticked subset and the shown digest; a changed digest writes nothing and shows the new report; the route table holds no install, index or update route |
| DH-T-UISET-18 | D-6 | Enable, grants, restart and remove are one manager operation each and idempotent by request id; the remove dialog states that the directory stays; a non-owner gets 403 |
| DH-T-UISET-19 | D-7 | An account view's phase and actions are rendered as given; a failed entry still shows its account; opening the section calls no probe and no authorize; authorize renders an anchor; menus list every provider with its reason; "automatic" shows the resolved provider or the ambiguity |
| DH-T-UISET-20 | D-8 | The picker lists presets with catalogue text in both languages and an operator preset's text verbatim; `custom` is marked and not selectable; the grant count is stated; `permission.set` passes the admitted actor and refuses a non-owner; `auto` is absent unless registered; the Permissions section renders each `PeerRule()` value and every gap a fake inventory reports, renders "none on this machine" for an empty list, and carries the revoke-all form |
| DH-T-UISET-21 | D-9 | The list shows winners, shadowed definitions and provider status with source classes; a hostile `SKILL.md` adds no element, attribute or handler; `skill.Changed` replaces the region; the tool card's bytes are equal before and after the catalogue changes |
| DH-T-UISET-22 | D-10 | A preview names the restarting entries and writes nothing; apply requires the previewed revision; a stale one is 409; a field not marked `Restart` is refused |

### 9.2 Real-environment scenarios

Each runs the built `dsh` binary with the `web` profile and a real browser driven by the controller. A fake is never counted here.

<a id="dh-e-uiset-1"></a>
#### DH-E-UISET-1 A live setting is edited from two browsers and a second process, and a bad edit changes nothing
- **Proves.** DH-D-UISET-1, DH-D-UISET-2.
- **Given.** A temporary home; two real `dsh web` processes A and B on it; browser 1 on A in English, browser 2 on B; a probe entry that reports its config and start count.
- **When.** (1) In browser 1 change the tool parallelism and save. (2) In browser 2, type a new value into the same card without saving. (3) In browser 1 save another value. (4) In browser 2 save. (5) Save a value below the minimum. (6) Switch browser 1 to Chinese. (7) Edit the layer file by hand with a syntax error, then correct it.
- **Then.** (1) The card says applied, the probe's start count is unchanged, and B's card shows the value within the poll interval. (3) Browser 2's card shows the new value and still holds the typed draft. (4) is refused as a conflict with the draft intact; a second save applies. (5) shows a message beside the field and the file is unchanged. (6) Every label is Chinese. (7) Both hosts keep their running values, both settings pages name the file, line and column of the rejection, and the corrected file applies.
- **Cleanup.** Stop both processes and delete the home. If a process does not stop, keep the home and the layer file.

<a id="dh-e-uiset-2"></a>
#### DH-E-UISET-2 The sharing page turns each kind on and off against a recording collector, and the off-switch stops all of them
- **Proves.** DH-D-UISET-3.
- **Given.** A fresh home; a local recording HTTP server configured as every destination; a real model API key supplied at run time.
- **When.** (1) Open the sharing page and run one turn. (2) Turn on analytics; use the UI. (3) Turn on session-log upload; run a turn. (4) Set the off-switch; run a turn. (5) Restart with `DSH_SHARING_OFF=1` and open the page.
- **Then.** (1) Every switch is off and the recorder saw nothing. (2) Only catalogue analytics events arrive. (3) The model request carries the log lines. (4) Nothing further arrives and every row reads "stopped by the switch". (5) The switch is on, disabled, and names the variable.
- **Cleanup.** Stop the host and the recorder; delete the home. Keep the recorder's log if any assertion failed.

<a id="dh-e-uiset-3"></a>
#### DH-E-UISET-3 A provider is added with a real key that never appears in a page, a response or a log
- **Proves.** DH-D-UISET-4.
- **Given.** A fresh home; a real API key for one OpenAI-compatible provider supplied at run time; the browser's network log captured.
- **When.** (1) Add the provider from the catalogue. (2) Store the key. (3) Paste `NAME=value` as a key. (4) Run the route check and discovery. (5) Run one turn on the route. (6) Remove the route with "also delete the stored key".
- **Then.** No captured response body, page source, host log or layer file contains the key. (2) The row shows a masked preview. (3) is refused and the field is empty. (4) reports a classified success and lists models. (5) The turn completes. (6) The route and the stored key are gone.
- **Cleanup.** Delete the home and remove the keychain item the store created; report if it cannot be removed.

<a id="dh-e-uiset-4"></a>
#### DH-E-UISET-4 A plugin directory is inspected, granted in part, run, changed, approved again and removed
- **Proves.** DH-D-UISET-6.
- **Given.** A temporary home; area A22's reference plugin in a directory, asking for two capabilities; a real model API key; an outbound-request recorder.
- **When.** (1) Inspect the directory. (2) Add it with one capability ticked. (3) Ask the model to use both of its tools. (4) Change a file in the directory. (5) Inspect again, approve, restart. (6) Kill the plugin process. (7) Remove the plugin.
- **Then.** (1) Both capabilities are shown unticked and no process started. (2) One process runs; its page lists one grant. (3) One call succeeds and one is denied. (4) The banner appears and the old copy still runs. (5) The new digest is shown and runs. (6) The page shows the exit and the restart. (7) No process remains and the directory is byte-identical. The recorder saw no request except to the model.
- **Cleanup.** Delete the home. If a process remains, record its id and keep the home.

<a id="dh-e-uiset-5"></a>
#### DH-E-UISET-5 Accounts are linked through the system browser from the account panel and the connections section
- **Proves.** DH-D-UISET-5, DH-D-UISET-7.
- **Given.** A vendor test account and a real MCP server that requires OAuth, both supplied at run time; a host on loopback; a second host reached through a non-loopback name.
- **When.** (1) Open both sections. (2) Start vendor sign-in, reload the page, finish in the opened tab. (3) Open the panel. (4) Connect the MCP account. (5) Choose a web search provider that has no credential. (6) Sign out of both. (7) Open the account section on the second host.
- **Then.** (1) No request left the machine. (2) The pending attempt survives the reload and ends "signed in". (3) Profile and balance are requested now. (4) The server's tools appear. (5) The menu shows the reason. (6) Both records are gone. (7) The panel explains that sign-in needs a loopback address and offers no start control.
- **Cleanup.** Sign out, revoke the test grants at the provider, delete both homes.

<a id="dh-e-uiset-6"></a>
#### DH-E-UISET-6 A preset switch revokes a session grant, and a skill is listed, changed on disk and called
- **Proves.** DH-D-UISET-8, DH-D-UISET-9.
- **Given.** A temporary workspace with a project skill and a sibling directory; a real model API key; preset `workspace-write`.
- **When.** (1) Have the model write to the sibling directory and choose "allow for this session". (2) Open the picker and switch to `read-only`. (3) Ask for another write. (4) Open the skills list. (5) Edit the skill's description on disk. (6) Invoke the skill. (7) Delete the skill file and reload the session.
- **Then.** (2) The picker states one grant will be revoked; afterwards the strip is empty and the transcript has a preset row. (3) A prompt appears. (4) The skill is listed as from the workspace. (5) The list updates without a reload. (6) A skill card shows the loaded text. (7) The card is unchanged and the list no longer holds the skill.
- **Cleanup.** Delete the workspace and the home.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Requests to A19 and A07.** That the `settings.write` handler uses `settingspage.Decode` and this area's card views; that `settings.Form` carries, per field, its `schema` kind, constraints, supplying layer and catalogue key. Design here assumes both.
2. **Settled with [host-telemetry.md](host-telemetry.md).** `sharing.Status` carries the settings address of each kind, so the sharing page keeps no table; the descriptions in [§6.2](#62-contracts) are catalogue text for the kinds shipped today, and a kind without text shows the sender's own description. Notice polling is the kind `account-notices`. The off-switch stops senders only ([DH-ADR-27](../design.md#dh-adr-27)).
3. **Requests to A21 and A22.** `plugins.Manager.Add` should take the digest the report showed; `config.Candidate` should list the entries an operation restarts; `manifest.Report` should carry the confinement facts as data.
4. **Request to A17.** A live poll interval beside `Notifications`; the notice list and report calls on `account.Service`; key-format validation in the store; an event when a credential reference changes.
5. **Request to A15.** One listing of every `AccountSource` (the fleet's and the command-line accounts'), and a status-change event for the fleet. Design here uses `mcp.Service.Accounts()`.
6. **Open, owner [context-compaction-skill.md](context-compaction-skill.md) (listed open there).** A per-skill off switch (a live list of disabled names applied in `Snapshot`). It is not designed, so the skills page of [DH-D-UISET-9](#dh-d-uiset-9) switches sources only and shows each skill read-only with its source and the reason it is unavailable.
7. **Open, owner [llm.md](llm.md).** Whether every field of a keyed route is live; until it says, the models page treats a route field as needing a save and a route check. The display name and search aliases per catalogue vendor are that document's catalogue fields (register entry U-K2-11), drawn as [DH-D-UISET-4](#dh-d-uiset-4) says.
8. **Ownership, settled.** This area owns the plugins page ([DH-D-UISET-6](#dh-d-uiset-6)); areas A21, A22 and A18 supply its data and register no page of their own ([extensions.md](extensions.md) is asked to say so). The page calls `plugins.Manager` as an owner ([boot.md](boot.md)): add shows area A22's report and then approves, remove offers a purge choice, an optional bundle is switched by its group id (`SetEnabled`), and in safe mode an entry is shown "off for this launch". Every settings write goes through `config.Service.Apply`, and a card shows its error codes as they are: `config/stale` (the 409 of the form protocol), `config/overridden`, `config/computed` and `config/protected` (the field is read-only, with the reason). It owns the permission preset control, which sits in area C3's `composer.controls`; "preset" without a qualifier in this document means the permission preset of area A17, and the agent preset of [bundle-preset.md](bundle-preset.md) is drawn by [ui-work.md](ui-work.md). The language row writes area C1's preference and the appearance rows are area C2's. Unsaved field text is kept by C1's `keep` module across swaps and stream replacements.
9. **Assumed of other areas where two of their documents disagree (requests sent).** The live parallel cap is the `tools` component's `maxParallel` (settled by [tools.md](tools.md)); its generated card shows it. The model can attach and detach a plugin for its session and nothing more; making a plugin or a setting durable is an owner action on the plugins page or the command line ([DH-ADR-30](../design.md#dh-adr-30)).
10. **Limits kept.** A plugin process has no settings card and adds no UI. A restart edit is previewed, not transactional. Settings are owner-only. With the layer watch disabled, a card can show a value another process already changed until this host's next save. The carried units of [§5](#5-tech-debt-and-its-resolution) name stage [DH-S-59](../implementation-plan.md#dh-s-59).

## 11. Units dispositioned by this spec

68 assigned units, all with a row in `trace/fragments/C7.jsonl`: 15 `UP-PKG` (10 replaced, 4 merged, 1 dropped), 47 `UP-DEBT` (35 resolved, 5 not-applicable, 7 carried), 4 `UP-CFG` (1 replaced, 1 merged, 2 dropped), 2 `UP-SVC` (1 merged, 1 dropped). The same fragment holds final verdicts for the 2 `UP-DEC` notes (1 merged, 1 ported) and declares 9 decisions (`DH-ADR-UISET-1` to `-9`), 10 deliverables (`DH-D-UISET-1` to `-10`), 22 local tests (`DH-T-UISET-1` to `-22`) and 6 scenarios (`DH-E-UISET-1` to `-6`).
