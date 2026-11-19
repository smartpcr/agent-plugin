# The web UI core (page shell, regions, the first-party script, islands, view state, shortcuts, UI text, upload): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) (`2e39e70`) and are cited as `path:line` relative to the upstream root. Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `UICORE`. Nothing here has been built, run or tested.

| | |
|---|---|
| **Area key** | `UICORE` (agent C1) |
| **Upstream packages** | 14, all under `packages/client`: `config`, `connection`, `file-upload`, `hmr`, `locale`, `modules`, `product-analytics`, `resources`, `script-realm`, `script-realm-iframe`, `shortcuts`, `store`, `ui-slots`, `web` |
| **Serves outcomes** | [DH-O-12](../design.md#dh-o-12) first; also [DH-O-1](../design.md#dh-o-1), [DH-O-7](../design.md#dh-o-7), [DH-O-14](../design.md#dh-o-14) |
| **Depends on areas** | A19 (routes, access, event stream: [api.md](api.md), used as written), A18 (listener, assets mount, sharing gate, reporting channels: [host-telemetry.md](host-telemetry.md)), A07 (`kv`, attachments: [storage.md](storage.md)), A21 (loader, settled-reload notice), A23 (`schema`). Areas C2 to C7 and X1 to X6 build on this one; it depends on none of them |
| **Priority** | P0 for the page shell, the view contract, the region registry, the stream client, the island loader and the message catalogue: six other UI areas cannot render a page without them. P1 for view state, shortcuts, focus rules and upload. P2 for usage events and upstream's six further languages. Each deliverable states its one priority in [§8](#8-deliverables) |

## 1. Purpose

A person uses the harness in a browser tab. This area is the frame every page lives in and the small amount of browser-side code that makes server-rendered pages feel live: the page layout and its named places, the way a feature puts its user interface into a place it does not own, the one script that keeps a page connected to the host, the rule for the few JavaScript widgets, where state that must live in the browser is kept, keyboard shortcuts and focus, the text in English and Chinese, and file upload.

Upstream solves the same needs with a second program: a cordis application that runs inside the browser, loads plugin code at run time, keeps a copy of host state and re-renders it with React. The Go host renders HTML ([DH-ADR-3](../design.md#dh-adr-3)), so most of that program has no counterpart. What it *guaranteed* to the user does: a feature appears and disappears without a reload, nothing typed is lost when the connection drops, a page never silently runs old code against a new host. This specification restates each guarantee on the server-rendered design ([§6.2](#62-contracts)) and gives six other UI areas (C2 to C7) one set of contracts to build on.

## 2. Vocabulary

Terms defined elsewhere are used as written: **view**, **fragment**, **swap**, **out-of-band swap**, **island**, **Content-Security-Policy** ([libs/go-ui-stack.md](../libs/go-ui-stack.md) §1); **route**, **region**, **tab**, **interest**, **cursor**, **`reset`**, **park**, **request id** ([api.md](api.md) §2); **peer**, **principal** ([../design.md](../design.md) §2).

| Term | What it is | Why it exists |
|---|---|---|
| **Client plugin, slot** (upstream) | A client plugin is JavaScript a package ships for the browser. A slot is a named place in the React tree that plugins register components into. | The two things this area replaces. |
| **Page shell** | The HTML document around every page: `<head>`, landmarks, the notice area, the dialog host, the script and style references. | One layout, so that every page has the same connection state, focus rules and policy. |
| **Region (of the shell registry)** | A named place whose *owner* declares it and other components fill it. It is rendered on the host. A19's "region" (an element with a stable `id`) is the same thing seen from the browser: every registry region renders into one such element. | Features place UI without importing each other. |
| **Contribution** | One component's entry in a region: an id, an order, and a function that renders a fragment. It is an effect of the component that made it. | Disabling the component removes its UI with nothing left to clean up. |
| **Election, phase** | In a region that shows one contribution, election is choosing it: each candidate says whether it applies. A phase is a named group that is always tried before the next group. | "A pending approval outranks a read-only notice" is a rule, not two numbers that happen to differ. |
| **Composition epoch** | A counter the shell increases when the set of contributions, shortcuts or islands changes. | An open page can tell that what it shows is no longer what the host would render. |
| **Release identity** | The version and source revision of the running `dsh` binary. | A page loaded from one binary must notice that another now answers. |
| **First-party script** | The one JavaScript file the project writes and every page loads (`dsh.js` and its modules). | It does the jobs HTML and htmx cannot; it holds no feature logic. |
| **Framed island** | An island mounted in a same-origin `<iframe>` whose document carries a wider policy than the page. | Third-party widget code (the terminal) that needs what the page's policy forbids. |
| **View state** | State that changes what is shown and nothing else: a collapsed panel, a scroll position, text typed into a plain form field and not yet submitted. The composer's unsent message is not view state: it is the host-stored draft record that area C3 owns ([DH-ADR-UICHAT-4](ui-chat.md#dh-adr-uichat-4)). | It must not be lost by a swap or a reload, and must not become a second copy of host state. |
| **Host preference, per-tab store** | A host preference is view state the host stores per principal and renders from. The per-tab store is a small keyed store the first-party script keeps in the browser's `sessionStorage`. | Two of the four places of [DH-ADR-REACT-1](../libs/react.md#dh-adr-react-1). |
| **Message catalogue, namespace, message key** | The catalogue is the table of product-written UI text per language. A namespace groups one component's keys; a key names one text (`common.cancel`). | The UI ships in English and Chinese; text is chosen per request. |
| **Command, chord, profile** | A command is a named action a shortcut can trigger. A chord is a physical key plus modifiers. A profile is the set of defaults for one platform (`web:macos`, `web:windows`, `web:linux`). | Key layouts and browser-reserved keys differ per device. |
| **Staged upload, chip** | A staged upload is a stored file that an unsent message refers to. A chip is its visible token in the composer. | Bytes leave the browser when a file is picked, not when the message is sent. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.client.web` | `dsh-client-web` | other (boot kernel) | Builds the module system from the host's boot graph, activates every client plugin, then hands the page to the renderer; a plain-DOM boot page shows a required plugin's failure (`packages/client/web/README.md:12`, `:38`, `:42`). |
| `UP-PKG-packages.client.modules` | `dsh-client-modules` | provider, host and client | Host half scans `dsh.client` declarations into the boot graph and serves bundles and package assets under `/plugins`; browser half is a lazy module table (`packages/client/modules/README.md:12`, `:34`, `:39`). |
| `UP-PKG-packages.client.hmr` | `dsh-client-hmr` | consumer | Streams the plugin graph to open pages so that enabling, disabling, a config change or a rebuilt bundle takes effect without a reload (`packages/client/hmr/README.md:12`, `:62`). |
| `UP-PKG-packages.client.config` | `dsh-client-config` | definition (types only) | Types for the live-updatable host config fields a browser plugin receives (`packages/client/config/README.md:12`, `:32`). |
| `UP-PKG-packages.client.connection` | `dsh-client-connection` | provider, host and client | `ctx.connection`: authenticated calls, the source check, the authentication provider registry, and "generations" with reconnect (`packages/client/connection/README.md:12`, `:41`, `:49`, `:60`). |
| `UP-PKG-packages.client.resources` | `dsh-client-resources` | definition and provider | `ctx.resources` and `useResource`: an address `dsh-resource://<type>/...` names live data; one provider per protocol streams it (`packages/client/resources/README.md:11`, `:36`, `:61`). |
| `UP-PKG-packages.client.store` | `dsh-client-store` | definition | Observable snapshot stores (Zustand, Immer) with optional `localStorage` persistence (`packages/client/store/README.md:11`, `:34`). |
| `UP-PKG-packages.client.ui-slots` | `dsh-client-ui-slots` | definition | The slot registry: five kinds, three scopes, store seats, component factories, load-time ownership checks (`packages/client/ui-slots/README.md:12`, `:28`, `:48`). |
| `UP-PKG-packages.client.locale` | `dsh-client-locale` | provider | `ctx.locale`: eight shipped languages, typed dictionaries with Chinese and English required, a host-stored preference, language packs from plugins (`packages/client/locale/README.md:12`, `:34`, `:40`). |
| `UP-PKG-packages.client.shortcuts` | `dsh-client-shortcuts` | provider | `ctx.shortcuts`: a command registry with per-platform defaults, physical-key matching and device-local overrides (`packages/client/shortcuts/README.md:12`, `:30`, `:34`). |
| `UP-PKG-packages.client.file-upload` | `dsh-client-file-upload` | provider, host and client | Uploads a file for one session and returns a receipt a later prompt cites; progress and cancellation (`packages/client/file-upload/README.md:12`, `:35`, `:47`). |
| `UP-PKG-packages.client.product-analytics` | `dsh-client-product-analytics` | consumer | Desktop reports selected interactions to a collector, on by default, with no user-facing control (`packages/client/product-analytics/README.md:12`, `:24`). |
| `UP-PKG-packages.client.script-realm` | `dsh-client-script-realm` | definition | A registry of named script-environment providers; not in the default roster (`packages/client/script-realm/README.md:12`, `:26`). |
| `UP-PKG-packages.client.script-realm-iframe` | `dsh-client-script-realm-iframe` | provider | A hidden same-origin iframe as a script environment; not a security boundary (`packages/client/script-realm-iframe/README.md:12`, `:31`, `:53`). |

### 3.2 Behaviour

**Boot.** The host writes a boot graph into the page; the browser builds a module table, creates one cordis entry per client plugin and waits for all of them. Only when the roster has settled, and no required plugin is inactive, does the renderer draw the one built-in slot, `root` (`docs/subsystems/web-client.md:24-26`). Until then the user sees a spinner; an optional plugin that failed is named only in the console (`packages/client/web/README.md:133`).

**Composition.** A plugin calls `ctx.slots.register()` to occupy a slot and, in the same call, declares the child slots its component renders. A slot has a cardinality (`single`, `list`, `keyed`, `chain`, `hoc`) and a scope (`root`, `session`, `session-maybe`); registering into an undeclared slot fails at load (`docs/subsystems/slots.md:23-29`, `:60-69`). Registrations are cordis effects, so disposing a plugin removes its UI and collapses the slots it declared (`:29`). Components never receive the cordis context: data arrives as derived props, and shared view state lives in a declared store (`:103`, `:237`).

**Staying current.** An open page follows the host's plugin graph over a stream. Enabling or disabling a plugin adds or removes its entry; a live config field reaches the plugin as an observable reference without a remount, with secret positions removed on the host (`packages/client/modules/README.md:47`, `:52`). A page applies updates only while the host serves the release it booted from; otherwise it asks the user to reload (`:59`).

**Connection.** Calls are HTTP POST; streams share one WebSocket. A "generation" starts only when the host's `ready` item arrives, after host listeners are attached, so a baseline read cannot race ahead of live events (`packages/client/connection/README.md:60`). When it ends, the client retries with 50 to 100% jitter under caps of 0.5, 1, 2, 4, 8 and 10 s; browser `offline` suspends attempts and `online` restarts them (`:62-64`). Every request first passes a source check (`Host` must be loopback, the bind address or a trusted host; a cross-site `Origin` is refused), then a credential provider (`:49`, `:43`).

**Live data by address.** A component that knows data only by address reads it with `useResource`; all readers of one address share one stream, and a pin keeps it open while its tab is hidden (`packages/client/resources/README.md:36`, `:66`, `:74`).

**Text, keys, files.** All product-written text comes from typed dictionaries; data written by a user, a model or a provider is shown verbatim (`.agents/notes/implemented/architecture/2026-08-23-locale-owned-client-ui-copy.md:13`). Shortcut overrides are stored per device, a new binding takes effect only after it was saved, and unreadable stored data is never overwritten (`packages/client/shortcuts/README.md:34`, `:38`). A picked file is uploaded at once through one first-in-first-out queue; the prompt later cites its receipt (`.agents/notes/implemented/feature/2026-08-26-generic-file-upload.md:15`).

### 3.3 Failure, retry, rollback and cleanup upstream

- **A plugin fails to load.** A required one stops the boot page with a named reason; an optional one is left out and reported in the console (`packages/client/web/README.md:42`, `:133`).
- **A hot replacement fails.** After the old plugin instance was torn down, a failed replacement does not restore it (`packages/client/hmr/README.md:74`, `:118`). If the replacement of the connection or gateway plugin fails, the page gets no further updates until it is reloaded (`:119`).
- **A connection is lost.** Retries never stop; each stream re-reads its baseline; notifications sent meanwhile are lost (`docs/subsystems/web-client.md:81-87`).
- **An upload fails.** It restarts from the first byte; staged receipts live in host memory, so a host restart between upload and send loses them, and draft attachments are lost on reload (`packages/client/file-upload/README.md:88`; `.agents/notes/implemented/feature/2026-10-21-composer-image-upload-on-add.md:27-28`).
- **Shortcut storage is unreadable.** Edits are disabled and the last accepted bindings stay active (`packages/client/shortcuts/README.md:38`).
- **Cleanup.** A resource record stays in the page for its lifetime after its last reader left (`packages/client/resources/README.md:94`); disabling analytics does not clear queued events (`packages/client/product-analytics/README.md:87`).

## 4. Upstream decisions dissected

The first 22 rows are the decision notes handed to this area, named by file under `.agents/notes/` (`impl/` is `implemented/`, `prop/` is `proposed/`). The rest are visible only in READMEs. `UICORE-n` is short for `DH-ADR-UICORE-n` in [§7](#7-decisions). The ledger rows hold the full reasoning.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `impl/architecture/2026-07-22-slot-type-chain-implementation` | One `register` call occupies a slot, declares children, a store and injected data | Replace | The need (place UI in a place you do not own, with enforced ownership) stays; React props and stores go | [UICORE-2](#dh-adr-uicore-2) |
| `impl/architecture/2026-07-23-client-plugin-loading-model` | The browser runs the cordis loader over a lazy module table, with hot reload | Replace | No plugin code runs in the page; the host loader already governs lifetimes | [UICORE-1](#dh-adr-uicore-1) |
| `impl/architecture/2026-07-28-api-browser-trust-boundary` | One source check for all `/api` routes, plus "every POST is JSON" | Keep, change one half | An HTML form cannot post JSON, so the cross-site defence is the origin check and the cookie rule, not the media type | [UICORE-1](#dh-adr-uicore-1); A18 |
| `impl/architecture/2026-08-15-client-shells-and-dynamic-packages` | Four build layers with rules for what a bundler may inline | Not applicable | Exists to control a JavaScript bundler | none |
| `impl/architecture/2026-08-20-client-session-conversation-ownership` | Controller, adapter, renderer, component; components read only derived props | Replace | The layers collapse on the host; the rule "a view gets data, never a service" is kept | [UICORE-3](#dh-adr-uicore-3) |
| `impl/architecture/2026-08-23-locale-owned-client-ui-copy` | All product text from dictionaries, data verbatim, enforced by a source check | Keep | Language-neutral; the check becomes a template test | [UICORE-7](#dh-adr-uicore-7) |
| `impl/architecture/2026-09-05-client-resource-model` | Live data is named by address only; readers share one stream | Replace | An address is a fragment URL and an interest of the tab; the cache moves to the host | [UICORE-2](#dh-adr-uicore-2) |
| `impl/architecture/2026-09-10-component-factories-and-local-slots` | A reusable assembly whose caller chooses its inner parts | Replace | A Go view function with named fragment parameters; no registry is needed | [UICORE-3](#dh-adr-uicore-3) |
| `prop/architecture/2026-08-08-semantic-composer-chain-phases` | Proposed: elect by named phases, then priority | Adopt | It fixes a real ordering defect and costs one field | [UICORE-2](#dh-adr-uicore-2) |
| `impl/architecture/2026-09-19-window-drag-coverage-contract` | One declaration of the macOS window's draggable rows | Drop | No desktop window exists | [DH-ADR-18](../design.md#dh-adr-18) |
| `impl/architecture/2026-09-20-device-local-shortcut-preferences` | Overrides per device; save before activating; never overwrite unreadable data | Keep, web half | One host serves devices with different keyboards and browsers | [UICORE-6](#dh-adr-uicore-6) |
| `impl/architecture/2026-10-19-client-plugin-package-assets` | Images are files with a content hash in the name, not base64 in a bundle | Replace | First-party assets are embedded and content-addressed; there is no plugin bundle | [UICORE-3](#dh-adr-uicore-3) |
| `impl/architecture/2026-11-03-hoc-slot-kind` | A fifth slot kind that wraps the owner's content in layers | Replace | A `Wrap` region nests HTML on the host; nothing remounts | [UICORE-2](#dh-adr-uicore-2) |
| `impl/architecture/2026-11-05-client-module-metadata` | Stylesheets belong to the document, not to script loading | Replace | Stylesheets are links in the page head; an island's sheet is in the island table | [UICORE-4](#dh-adr-uicore-4) |
| `impl/architecture/2026-11-07-browser-authentication-development` | How a credential provider registers and owns its peer | Keep | A18's `listen.Authenticate` is that seam; handlers never see a token | [UICORE-1](#dh-adr-uicore-1); A18 |
| `impl/architecture/2026-11-07-browser-authentication-providers` | Authentication is its own service; providers are tried in order | Keep | One admission for pages, fragments and streams | [UICORE-1](#dh-adr-uicore-1); A18 |
| `impl/architecture/2026-11-15-client-config-sync` | Live config fields reach browser plugins without a remount; secrets stay on the host | Replace | The host renders from current config; a change refreshes regions | [UICORE-2](#dh-adr-uicore-2) |
| `impl/feature/2026-08-26-generic-file-upload` | Upload when picked, one queue, receipts, content-addressed storage | Keep | Language-neutral; staging becomes durable | [UICORE-8](#dh-adr-uicore-8) |
| `impl/feature/2026-10-14-host-release-change-refresh-prompt` | Apply updates only from the release the page booted from; otherwise prompt | Keep, simpler | A page loaded from one binary still meets another after an upgrade | [UICORE-2](#dh-adr-uicore-2) |
| `impl/feature/2026-10-21-composer-image-upload-on-add` | Images upload when added, so errors show early and a send carries references | Keep | Same route as files | [UICORE-8](#dh-adr-uicore-8) |
| `prop/architecture/2026-11-15-client-config-sync-migration` | Proposed: move each browser config read to config sync | Drop | It migrates code that is not carried; its inventory is used as a checklist | [DH-ADR-3](../design.md#dh-adr-3) |
| `prop/feature/2026-08-04-task-surface` | Proposed: a declarative, model-supplied panel rendered by the product | Not taken up here | It fits the view contract but is a tool and an interaction, owned by A17 and C4 | [UICORE-3](#dh-adr-uicore-3) |
| The first frame waits for every client plugin | `packages/client/web/README.md:12` | Change | A page is one complete response; a failed component's UI is absent and listed | [UICORE-3](#dh-adr-uicore-3) |
| Listeners attach before the baseline; `ready` opens a generation | `packages/client/connection/README.md:60` | Keep | It is A19's connect order; the script adds nothing | A19 |
| Jittered retry caps; `offline` suspends; a "reconnect now" command | `connection/README.md:62-64` | Keep | Proven numbers; the user needs a visible state and a button | [UICORE-4](#dh-adr-uicore-4) |
| Physical key codes, a `primary` modifier, duplicates rejected at registration | `shortcuts/README.md:30` | Keep | Layout-independent and checkable at load | [UICORE-6](#dh-adr-uicore-6) |
| On the web, shortcuts are dispatched after local handlers; composition is protected | `shortcuts/README.md:50` | Keep | A page must not take keys from a text field or an input method | [UICORE-6](#dh-adr-uicore-6) |
| Persisted stores use `localStorage` | `store/README.md:34` | Change | Preferences that shape rendering move to the host | [UICORE-5](#dh-adr-uicore-5) |
| Eight languages; Chinese and English required for every key | `locale/README.md:12`, `:40` | Keep, staged | Two complete baselines first, six more as data | [UICORE-7](#dh-adr-uicore-7) |
| Analytics on by default with account and device identity | `product-analytics/README.md:12`, `:28` | Drop | [DH-ADR-9](../design.md#dh-adr-9) | [UICORE-9](#dh-adr-uicore-9) |
| Uploads run in a dedicated Worker; receipts live in host memory | `file-upload/README.md:35`, `:47` | Change | A file body needs no Worker; a staged file must survive a restart | [UICORE-8](#dh-adr-uicore-8) |

## 5. Tech debt and its resolution

All 38 units are grouped by cause; each unit's own reason is in its ledger row. `L:` stands for `UP-DEBT-limit.packages.client.`. Carried rows name stage [DH-S-59](../impl.md#dh-s-59), where the known limits and scope boundaries of this part of the product are stated and checked.

| Group (cause) | Units | How the Go design answers it |
|---|---|---|
| **A. Bundles, the module graph and the client build** | `L:modules.1`, `.2`, `.4` to `.8`, `L:store.2`, `L:file-upload.4`, `L:ui-slots.2`, `L:config.1`, `L:hmr.1` (12) | Not applicable. Each exists because plugin code is built by an npm toolchain, delivered as bundles and replaced in a running page, or because TypeScript erases types. The Go UI has embedded assets named by content hash and no code replacement ([DH-ADR-LOADER-8](../libs/cordis-plugins.md#dh-adr-loader-8)). |
| **B. Script environments for client code** | `L:script-realm.1`, `L:script-realm-iframe.1` (2) | Not applicable: there is no client plugin to place. Where isolation is wanted the design uses real browser mechanisms: a framed island with its own policy, and a sandboxed frame for HTML previews (area C6). |
| **C. Keeping an open page in step with the host** | `L:hmr.2`, `.3`, `L:modules.3`, `.9`, `L:web.1`, `L:ui-slots.1`, `L:resources.1`, `.2` (8) | Resolved. A refresh that fails leaves the old HTML in place; the stream client is static and retries for ever; the release identity is in every baseline and in `GET /ui/state`; a failed component is listed in the notice area; contributions are indexed by region and owner; interests are capped and expire on the host; a source that keeps working after `stop` fails a test ([DH-D-UICORE-2](#dh-d-uicore-2), [DH-D-UICORE-3](#dh-d-uicore-3)). |
| **D. The connection and its trust checks** | `L:connection.1` to `.4` (4) | Resolved. No request body is base64 inside JSON: forms are small and uploads are streamed; a network listener needs TLS; there are no API peers; each browser can be signed out. A18 implements these ([host-telemetry.md](host-telemetry.md) §7); this area's upload client never buffers a file ([DH-D-UICORE-8](#dh-d-uicore-8)). |
| **E. Upload transport** | `L:file-upload.1`, `.2`, `.3` (3) | `.2` and `.3` resolved: the client uploads `File` objects, which can be read again and have a size, so a retry needs no new object and progress has a total. `.1` carried: one streamed request per file, no resumable offsets, as A19 already records ([api.md](api.md) §5 group G). |
| **F. Language** | `L:locale.1`, `.2` (2) | Resolved. Registrations hold message keys, never text, and text is resolved per request; the catalogue selects plural forms and sets the text direction ([DH-D-UICORE-7](#dh-d-uicore-7)). |
| **G. Browser-reserved keys** | `L:shortcuts.1` (1) | Carried. A page cannot take a chord the browser keeps (new tab, close tab, settings), and with no desktop window ([DH-ADR-18](../design.md#dh-adr-18)) there is no native interception. Web defaults avoid reserved chords and every command also has a visible control. |
| **H. View state per browser profile** | `L:store.1` (1) | Resolved: preferences that shape rendering are stored by the host per principal ([DH-D-UICORE-5](#dh-d-uicore-5)). |
| **I. Analytics** | `L:product-analytics.1` to `.5` (5) | `.1`, `.2`, `.5` resolved by removal: nothing samples the machine and there is no native launcher ([DH-ADR-UICORE-9](#dh-adr-uicore-9)). `.3` resolved by A18's permit, which stops a sender when consent is withdrawn. `.4` carried: no durable outbox, because usage data on disk would have to be purged on opt-out. |

Totals: 21 resolved, 14 not applicable, 3 carried.

## 6. Go design

### 6.1 Shape

Packages are under `src/harness/internal/ui`, the family [../design.md §4.2](../design.md#42-go-module-layout) gives to the UI areas. Routes and streams are A19's; this area registers its few routes through `web.Routes` like any other feature.

| Package | Component and service key | Why it exists |
|---|---|---|
| `ui` | No component. Types `Req`, `SafeHTML`, `View[T]`; the template helper set; the base policy | The contract every view in C2 to C7 is written against, and the two types A19 already names ([api.md](api.md) §6.1) |
| `ui/shell` | Component `ui.shell`; `shell.Key` (`Shell`) | The page layout, the region registry, the island table, the composition epoch |
| `ui/msg` | Component `ui.msg`; `msg.Key` (`Catalog`) | UI text per language, resolved per request |
| `ui/keys` | Component `ui.keys`; `keys.Key` (`Registry`) | Shortcut commands and the table the script dispatches from |
| `ui/prefs` | Component `ui.prefs`; `prefs.Key` (`Store`) | View state the host must know to render the first response |
| `ui/assets` | No component. The embedded file system, the asset manifest and the first-party script | What A18's `host.assets` serves ([host-telemetry.md](host-telemetry.md) §6.1) |
| `ui/usage` | Component `ui.usage`, no service | Declares this area's usage events; default-off |

```go
// package ui
type Req struct {                // built once per request or stream by shell.Env; A19's rq.Env() returns it
    Lang language.Tag; Dir string // "ltr" | "rtl"
    Zone *time.Location; Theme string
    Principal access.Principal; Tab string; Platform string // "macos" | "windows" | "linux", from the user agent
    Partial bool                 // true when HX-Request is present
}
func (r Req) T(k msg.Key, args ...any) string // catalogue text in r.Lang; the first integer argument selects the plural form
type SafeHTML struct{ /* unexported */ }      // two constructors only: a registered view's output, and FromSanitizer
func FromSanitizer(b []byte) SafeHTML         // called only by area C4's sanitiser; wraps b in a container that carries hx-disable
type View[T any] struct{ /* template name */ }
func Register[T any](name string) View[T]     // package-level; ui.Check() at start-up fails on a missing template
func (v View[T]) Render(w io.Writer, r Req, data T) error  // buffers, then writes; an error writes nothing
func (v View[T]) HTML(r Req, data T) (SafeHTML, error)
const BasePolicy = "default-src 'none'; ..."  // exactly the policy of DH-ADR-GOUISTACK-3
func SetUserText(f func(string) string)       // filled once by area C3 (credential masking); identity until then

// package shell
type Shell interface {
    Env(r *http.Request, p access.Principal) ui.Req
    Page(w io.Writer, rq ui.Req, p Page) error                // the layout around one fragment
    Declare(owner *cordis.Context, d Region) error            // the owner of a place names it; an effect of owner
    Contribute(owner *cordis.Context, c Contribution) error   // an effect of owner
    Render(ctx context.Context, rq ui.Req, region string, in Scope) ui.SafeHTML // the {{region}} helper
    Island(owner *cordis.Context, i Island) error             // an effect of owner
    Touch(owner *cordis.Context)                              // "what I render changed" (a live config field): bumps the epoch
    Epoch() uint64
}
type Page struct{ Title string; Main ui.SafeHTML; Session session.ID; Cursor uint64 } // zero Session: a page that shows none
type Region struct{ Name string; Kind Kind; Phases []string; Doc string }               // Kind: One | List | Keyed | Wrap
type Scope struct{ Session session.ID; Key string; Data any }                          // Data: the owner's typed value for this place
type Contribution struct {
    Region, ID, Key, Phase string; Order, Priority int
    Match  func(rq ui.Req, in Scope) bool // optional; pure, no I/O; only for Kind One
    Render func(ctx context.Context, rq ui.Req, in Scope, inner ui.SafeHTML) (ui.SafeHTML, error)
}
type Island struct{ Tag, Module, Stylesheet string; Policy web.FramePolicy; Fallback msg.Key } // a non-zero Policy means framed; its additions come from A19's closed list

// package msg
type Catalog interface {
    Register(owner *cordis.Context, namespace string, files fs.FS) error // <namespace>.<lang>.json; en and zh must hold every key
    Languages() []Language                                               // tag, own name, direction, fallback
    Text(lang language.Tag, k Key, args ...any) string
    Match(stored, acceptLanguage string) language.Tag                    // stored preference, else the header, else English
}
// package keys
type Command struct{ ID string; Label msg.Key; Group string; Default map[string]Chord; Target string; Fixed, InFocus bool }
// InFocus: Target is looked up inside the nearest ancestor of the focused element that carries data-key-scope (one card), not in the page.
type Registry interface {
    Register(owner *cordis.Context, c Command) error
    Table(rq ui.Req) ui.SafeHTML // the dispatch table of #ui-keys
    Commands(rq ui.Req) []Info   // id, label, group, fixed, default chord for the request's profile: data for C2's help page
}
// package prefs
func Declare[T any](owner *cordis.Context, name string, def T) (Pref[T], error) // T is validated by the schema package
func DeclareKeyed[T any](owner *cordis.Context, name string, def T) (Keyed[T], error) // one value per principal and caller-given key (a workspace root); Get and Set take the key
func (p Pref[T]) Get(ctx context.Context, who access.Principal) T
func (p Pref[T]) Set(ctx context.Context, who access.Principal, v T) error
```

**The page shell.** `Shell.Page` renders one document. A19's `web` calls it for every `Page`, `Fragment` and `List` answer that has no `HX-Request` header ([api.md](api.md) §6.2); with the header the fragment is sent alone. Area C2 owns how the landmarks are laid out (grid, resizable panes, the dock) and every visual rule; this area owns which elements exist and what each is for.

| Element (`id`) | Registry region and kind | Contents, and who fills it |
|---|---|---|
| `<html>` | none | `lang`, `dir` and the theme attribute from `ui.Req`; `data-release` and `data-epoch` at render time |
| `<head>` | `shell.head` (List) | One `<base>` so that every reference is document-relative ([DH-ADR-HOST-2](host-telemetry.md#dh-adr-host-2)); the `htmx-config` and island-table `<meta>` elements; the manifest link; stylesheet links (C2's tokens first); every import map registered with A19's `web.Routes.AllowInlineData`, written by the shell from `web`'s list; then `htmx.min.js` and `dsh.js` as deferred same-origin files. A `shell.head` contribution may add `<link>` and `<meta>` elements only; one that renders a `<script>` or `<style>` element fails `ui.Check` |
| `<body>` (forgery defence) | none | `hx-headers` also carries the peer's form token as `X-DSH-Token`; the token is A18's `Peer.FormToken()`, which reaches the layout through `rq.Env()`, and the `reqid` helper writes it into every form as the hidden `_token` field, so a post without script passes too ([api.md](api.md), `DH-ADR-API-9`) |
| `<body>` | none | `hx-headers` with the tab id (`X-DSH-Tab`), which is data, so htmx sends it on every request; a skip link to `#main` |
| `#notices` | `shell.notices` (List) | Connection state, the release prompt, the "paused" note of a parked tab, components that failed to load, and every error fragment A19 retargets here. Also the sign-in notice of [DH-ADR-28](../design.md#dh-adr-28): when area A18 reports a new browser sign-in, every open tab shows "a new browser signed in" with the time and one action, "sign out everything" (a form posting A18's `POST /auth/revoke-all`); it arrives as a `notice` event and stays until dismissed. The banner for a browser below the supported floor ([DH-ADR-39](../design.md#dh-adr-39)) is markup of this layout, always in the page and revealed by area C2's `@supports` rule, so it needs no script |
| `#nav` (`<nav>`) | `shell.nav` (List) | Workspaces and sessions (C6), entry links (C5, C7) |
| `#header` (`<header>`) | `shell.header` (List) | Page title and global actions |
| `#main` (`<main>`) | none | The route's fragment; `hx-target` of ordinary navigation |
| `#aside` (`<aside>`) | `shell.aside` (Keyed) | The side panel named in the URL (C6) |
| `#dialogs` | none | Swap target for `<dialog>` fragments; the script opens one as a modal when it arrives |
| `#announce` | none | A polite live region: one sentence when an answer starts and one when it ends, never the streamed text |
| `#ui-state`, `#ui-keys` (hidden) | none | Release and epoch; the shortcut table. Both are state regions of the stream topic `ui` |

**Template helpers.** One function map, registered with every template; no view defines its own.

| Helper | What it emits | Rule it enforces |
|---|---|---|
| `t` | Catalogue text for a key, in the request's language | Product text never appears as a literal in a template |
| `region` | The contributions of a region for a scope | A feature never calls another feature's view |
| `live` | A container with a stable `id` that can fetch itself (`hx-get`, `hx-trigger="dsh:reset from:body"`) and registers its interest through `rq.Tab().Watch` | Every live region survives `reset` ([DH-ADR-API-4](api.md#dh-adr-api-4)) |
| `href`, `asset` | A document-relative URL for a named route; an asset URL with the content hash in its name | Works under a path prefix; assets can be cached for ever |
| `domid` | A DOM id from a kind and a stable key | Updates address one node ([`DH-LIB-REACT-R2`](../libs/react.md)) |
| `reqid` | The hidden request-id field of a state-changing form (the id is A19's), from `rq.NewRequestID()`, together with the hidden `_token` field holding the peer's form token | A retried submit takes effect once, and a form posted without script still carries the peer's form token |
| `can` | Whether `access.Allows` lets this principal use a named route | The page shows only what the route would accept |
| `time` | A `<time>` element in the viewer's zone | No client-side date formatting |
| `kbd` | The default chord of a command for the request's platform | Hints match the dispatch table |
| `utext` | Text a person wrote (a title, an excerpt, a queued message), passed through the filter set by `ui.SetUserText` | A credential pasted into a message is masked wherever the text is printed ([DH-ADR-UICHAT-7](ui-chat.md#dh-adr-uichat-7)) |
| `island` | The custom element with its attributes and a server-rendered fallback inside | An island that fails to load still shows something |

**The region registry.** The owner of a place calls `Declare`; any component calls `Contribute`. Both are effects, so unloading a component removes its contributions, and unloading an owner removes the region and everything in it. `One` shows a single contribution: candidates are tried by phase order, then ascending `Priority`, and the first whose `Match` is true (or absent) wins. `List` shows all, by `Order` then registration. `Keyed` shows the contribution whose `Key` equals the scope's. `Wrap` nests each contribution around the owner's content, lowest `Order` outermost. Declaring a region twice, contributing to an undeclared one, a `Phase` the region does not list and a duplicate `(Region, Key, ID, Priority)` all fail the component that tried. A reusable assembly with caller-chosen parts needs no registry: it is a view whose data has `SafeHTML` fields.

**The first-party script.** `dsh.js` is an ES module that imports its siblings from the same directory; there is no bundler and no generated code. A test caps its total size. It holds no session data and renders nothing.

| Module | Job | Rules |
|---|---|---|
| `stream` | Opens the tab's `EventSource` with `after` and `tab`; passes each event's fragment to `htmx.swap`; drops a replayed `session` event; turns `reset` into the DOM event `dsh:reset` on `<body>`; honours `park`; re-creates a closed stream; after each swap raises the DOM event `dsh:swapped` on the replaced element, which a view may use as an htmx trigger | As [DH-ADR-GOUISTACK-4](../libs/go-ui-stack.md#dh-adr-gouistack-4) and [-5](../libs/go-ui-stack.md#dh-adr-gouistack-5). An event name it does not know is swapped like `view` ([api.md](api.md) §10, item 8). `htmx:oobErrorNoTarget` is not an error |
| `conn` | Shows one of `connected`, `connecting`, `offline`, `paused`, `host not reachable`, `signed out`, `stalled` by toggling markup already in `#notices` (the word for how to start the host again in "host not reachable" and "signed out", the launch word, is not this area's: it comes from area A25's contribution to `shell.notices`, [app-web.md](app-web.md) `DH-D-WEBAPP-5`; without that contribution the notice names no command); retries with the configured caps; `offline` stops and `online` restarts; a "reconnect now" button | Before re-creating a stream it requests `GET /ui/state`: 200 means resume, 401 means signed out, no answer means unreachable. It waits the growing delay between attempts and honours `Retry-After` on a 429 or 503. After `ReconnectGiveUp` attempts in a row that delivered no event (A19's `stream.Config`, 8, sent to the script) the state is `stalled`, shown as "not live, reload": the module stops retrying that stream ([§6.3](#63-ownership-failure-retry-rollback-and-cleanup)) |
| `state` | Compares `#ui-state` with the values the page was rendered at. A new epoch raises `dsh:reset`; a new release shows the reload prompt | Never reloads by itself |
| `islands` | Imports an island's module the first time its element is on the page; retries a failed import when the element next appears | Reads the table from a `<meta>` element, not from script |
| `keep` | The per-tab store: scroll positions of elements with `data-scroll-key`; text of fields with `data-draft`; "following the tail" | Uses `sessionStorage`; works with it unavailable; cleared for a form when its request succeeds. A field that a swap or a stream replacement re-creates gets its kept text back, matched by `id` |
| `tick` | Advances every `<time data-since>` element once a second from the start time the host wrote into it | Display only: it fetches nothing, and the host's next render replaces the value |
| `focus` | After a swap, returns focus to the element with the same `id`, or to the one marked `data-focus`; opens a swapped-in `<dialog data-modal>` with `showModal()` | Never moves focus while the user is typing elsewhere |
| `keys` | Matches `keydown` against `#ui-keys` plus the device's overrides and activates the command's target element; exports `list`, `set` and `reset` for C2's shortcut page | [DH-ADR-UICORE-6](#dh-adr-uicore-6). `set` refuses a chord that another editable command or a fixed command already has, and names that command. Escape is not dispatched while a modal `<dialog>` or a popover is open: the browser closes that layer |
| `zone`, `attention` | The time-zone cookie; the attention stream and notifications | As [DH-ADR-GOUISTACK-8](../libs/go-ui-stack.md#dh-adr-gouistack-8) and [-9](../libs/go-ui-stack.md#dh-adr-gouistack-9) |
| `upload` (loaded as an island) | The `<dsh-upload>` element of [DH-D-UICORE-8](#dh-d-uicore-8) | Loaded only where a file input is shown. While its queue is not empty the element carries the attribute `busy` and raises `dsh:upload-busy` when that changes, so the composer can hold Send |

**How an island is declared, loaded and fed.** (1) The owning component calls `Shell.Island` with the element name, the module path inside the embedded assets, an optional stylesheet, and the policy directives it needs beyond the base. (2) The layout renders the table of loaded islands into a `<meta>` element. (3) A view places the element with the `island` helper: configuration as attributes (addresses, limits, translated labels), initial content as ordinary child elements, which double as the fallback. (4) The script imports the module when the element first appears; the element's `connectedCallback` and `disconnectedCallback` are its whole lifetime. (5) The island talks to the host with same-origin `fetch` (the script exports a helper that adds the tab header) or its one WebSocket, and tells the page something by dispatching a DOM event that htmx can use as a trigger. (6) An island with a non-zero `Policy` is placed as an `<iframe>`. For each such island the shell registers one route of A19's kind `Frame` (`GET /ui/frames/<tag>`, name `ui.frame.<tag>`) with that `Policy`, so `web` writes the wider policy and `frame-ancestors 'self'`; an addition outside A19's closed list (`style-src 'unsafe-inline'`, `worker-src blob:`, `script-src 'wasm-unsafe-eval'`) fails `Shell.Island`; the page and the frame exchange a closed set of `postMessage` messages (`init`, focus in and out, a shortcut the island did not use, resize, theme), each checked for origin. `init` carries the attributes of the host element (a terminal's socket address, labels), so the frame URL names the island and nothing else: no id travels in a query, which keeps A19's URL rule. An island never holds the only copy of anything.

**Living under a policy with no inline script.** A19's `web` is the only writer of the `Content-Security-Policy` header and of every other security header ([api.md](api.md), `DH-ADR-API-9`). This area never writes a header: it supplies the base value `ui.BasePolicy` and makes every page fit the composed policy. `web` adds to `script-src` one `'sha256-…'` source per import map registered through `web.Routes.AllowInlineData`, and nothing else. What the shell and the loader do so that nothing else is needed:

| Rule | Mechanism |
|---|---|
| No inline script, handler or style anywhere | Templates cannot emit `<script>`, `<style>`, a `style` attribute, an `on…` attribute or `hx-on`: `ui.Check` and a golden test reject them, also inside a `shell.head` contribution. Per-user values are a stylesheet route (C2); a size set by data is a class or a custom property set through the style object by a kit island |
| Configuration reaches script as data | The `htmx-config` `<meta>` (evaluation, script-tag processing and indicator styles off; 422 and 409 answers swap into the form's target and other errors into `#notices`), the island table `<meta>`, attributes on `<html>` and on island elements. No script reads configuration from script text |
| Only a registered import map may be inline | A browser treats an import map as an inline script. A component registers its text with `web.Routes.AllowInlineData(owner, "importmap", text)`; `web` hashes it and composes the policy when the route table is swapped. The shell writes exactly the registered blocks, byte for byte, into the head ahead of the first module script; `ui.Check` and a golden test allow those blocks and no other inline script. A registration changes the composition epoch, so a page loaded before it shows the reload prompt instead of failing imports |
| Modules load from the binary only | The `islands` module calls `import()` with an asset URL from the table; no `eval`, no `Function`, no `blob:` or `data:` script, no worker from a string. A worker is a same-origin asset file |
| A framed island has its own document | A route of kind `Frame` per framed island, whose `Policy` adds only entries of A19's closed list, always with `frame-ancestors 'self'`. The page's own `frame-src 'self'` admits it. A browser that does not honour a hash for an import map leaves the composer on its fallback, a framed island ([DH-ADR-UICHAT-3](ui-chat.md#dh-adr-uichat-3)) |
| State-changing requests are same-origin and carry the form token | The layout puts the peer's token on `<body>` for htmx (`X-DSH-Token`) and every form carries `_token`; an island's `fetch` helper adds the same header. A page never changes state with a `GET` |
| Browser features are off | `web` writes a `Permissions-Policy` that denies every feature. The one allowance, `microphone=(self)`, is registered with `web.Routes.AllowFeature(owner, "microphone")` as an effect by the voice-input component ([x-cua-browser-voice.md](x-cua-browser-voice.md)), not by this area |

### 6.2 Contracts

**What the client runtime guaranteed, and where the guarantee lives now.** This table is the disposition of the machinery ([DH-ADR-UICORE-1](#dh-adr-uicore-1)).

| Upstream machinery | Guarantee to the user | Where it lives in the Go design |
|---|---|---|
| Boot kernel, module table (`web`, `modules`) | The UI starts, and a failure is visible, not a blank page | One HTTP response per page, complete before any script runs; a failed component is listed in `#notices`; with no host, the browser's own error |
| Graph sync and hot reload (`hmr`) | Enabling, disabling or reconfiguring a feature shows in open pages without a reload | Contributions are effects; a change bumps the epoch; open pages refresh their regions |
| Release check (`modules`) | A page never mixes code from two releases without telling the user | `#ui-state` carries the release on every connect; a difference prompts for a reload |
| Config sync (`config`, `modules`) | A config change shows without a reload; a secret never reaches the browser | Views render from current config; the secret type prints `[redacted]` ([../design.md §6](../design.md#6-security-model), rule 4) |
| Connection generations (`connection`) | Nothing falls between a baseline and live updates; the page reconnects by itself and says what state it is in | A19's connect order and cursor resume; the `conn` module |
| Reconnect without loss | Text the user typed is not lost when a request fails or the page reloads | A failed request swaps nothing; fields marked `data-draft` are kept per tab; the composer's draft is host-stored (C3); a retried form is idempotent by request id |
| Resource model (`resources`) | Two views of one thing agree, and a hidden view is current when shown again | One host state, rendered per reader; a `live` region is replaced whole; a region re-fetches itself on `reset` |
| Store engine (`store`) | A collapsed panel stays collapsed; a preference survives a reload | The four places of [DH-ADR-UICORE-5](#dh-adr-uicore-5) |
| Slot ownership checks (`ui-slots`) | A feature cannot draw into a place nobody offered, and removing it leaves no debris | `Declare` and `Contribute` fail at load; effects clean up |
| Script realms | none to the user | Not built |

**Routes this area registers** (through A19's `web.Routes`; names are final for this area).

| Method and pattern (name) | Returns and rules |
|---|---|
| `GET /ui/state` (`ui.state`) | The `#ui-state` and `#ui-keys` fragments. Access: A19's rule `access.Admitted()`, every admitted principal, also one with no global binding such as a share viewer ([x-liveshare-webworker.md](x-liveshare-webworker.md)); it shows release, epoch and shortcuts only. Never wakes a session |
| `PUT /ui/prefs/{name}` (`ui.pref`) | Sets one declared preference of the calling principal; `{name}` is opaque; a keyed preference takes its key as the query parameter `key`, which is view state; an undeclared name is 404, a value of the wrong shape is 422 |
| `GET /ui/frames/<tag>` (`ui.frame.<tag>`, kind `Frame`) | The document of one framed island; one route per island, registered by `Shell.Island` with the island's `Policy` and removed with its owner. The URL has no query; an unframed island has no such route |
| `GET /` (`ui.root`) | `303` to the session list, or to `/setup` while area A25's `setup.State` (an optional injection) is neither ready nor deferred ([app-web.md](app-web.md)). `ui.shell` is on the `web` profile's required list, so a host without a shell does not start |
| `GET /assets/...` | Not a route: A18's `host.assets` mount serves `ui/assets` ([DH-D-HOST-1](host-telemetry.md#dh-d-host-1)) |

**Stream.** One state source, topic `ui`, registered with A19's hub and watched by the layout for every tab. Its regions are `#ui-state` and `#ui-keys`; both are complete on connect and replaced whole on change. No new event name is needed. The upload route (`POST /sessions/{sid}/uploads`) and every other route and event are A19's.

**Events.**

| Go event (dispatch form) | Replaces | Payload and rule |
|---|---|---|
| `shell.Changed` (notify) | new | The new epoch. Raised once per settled loader reload ([DH-D-LOADER-11](../libs/cordis-plugins.md#dh-d-loader-11)) or after 250 ms without a further change, so a burst of loads is one refresh |
| none | `UP-EVT-connection/request` | A waterfall that let a plugin hold or refuse requests before the body was read; upstream's desktop used it to pause work during an installation (`packages/client/connection/README.md:55`). There is no desktop installer ([DH-ADR-18](../design.md#dh-adr-18)) and A18's admission pipeline has no middleware seat |

**Config.** Go structs with defaults as Go values; unknown keys fail the load ([DH-ADR-SCHEMA-3](../libs/zod.md#dh-adr-schema-3)).

| Go struct | Fields and defaults | Replaces |
|---|---|---|
| `shell.Config` | `Reconnect{Base 500 ms, Max 10 s, Factor 2, Jitter 0.5}`; `HiddenGrace` 60 s (how long a hidden tab keeps its stream); `SettleDelay` 250 ms; `DevDir` empty (a directory to read templates and assets from, development only) | `UP-CFG-dsh-client-connection` (`recovery`; `trustedHosts` is A18's `listen.Config.TrustedHosts`, `maxRequestBodyBytes` is A19's `web.Config.MaxFormBytes` and the attachment limit); `UP-CFG-dsh-client-hmr` (`pollIntervalMs` has no counterpart: `DevDir` re-reads on each request) |
| `keys.Config` | `StopSequence` 500 ms (live): the longest gap between two Escape presses that stop a reply | `UP-CFG-dsh-client-shortcuts` |
| `msg.Config` | `Default` `en`; `PacksDir` `<home>/locales` | new |
| none | Analytics is switched by A18's `report.Config.Analytics`; the machine-query fields are gone | `UP-CFG-dsh-client-product-analytics` |

**Services.**

| Upstream unit | Go replacement |
|---|---|
| `UP-SVC-clientModules` | `shell.Key`, interface `Shell` (layout, regions, island table) with the embedded `ui/assets`; it composes no bundle graph |
| `UP-SVC-config` | None: a component reads its own typed config struct and renders from it |
| `UP-SVC-connection` | None in this area: A18's `listen.Key` owns the socket, the source check and `listen.Authenticate` |
| `UP-SVC-fileUploads` | Merged into A19's route `session.upload` over A07's `attachments.Key`; the staged-receipt table becomes the draft record |
| `UP-SVC-productAnalytics` | A18's `report.Key` (`Reporter`), with this area's events declared in `ui/usage` |

**Persisted data.** Host preferences in A07's `kv`, namespace `ui-prefs`, keyed by principal and name (and key, for a keyed preference), authoritative. There is one store for everything a person chooses about how the UI looks: `ui.msg` declares the language preference `ui.language` here, and area C2 declares the appearance preference (theme, text sizes, fonts) here ([DH-ADR-UIKIT-8](ui-kit.md#dh-adr-uikit-8)); neither is a cookie or a settings value, so both follow the principal to every browser. In the browser: `sessionStorage` keys under `dsh.tab.` (the per-tab store), `localStorage` key `dsh.keybindings.v1` (shortcut overrides) and the time-zone cookie. No session event. **Tools exposed to the model:** none, as upstream (`packages/client/connection/README.md:72`).

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership and access.** A region, a contribution, an island entry, a command, a preference declaration and a catalogue namespace are effects of the fiber that registered them. The shell owns the epoch and the layout. The browser owns its tab, its storage and its permissions; the host can read none of them. A contribution has access to the scope its owner passes and to whatever services its component injected; it owns nothing on the page.

| Situation | What happens | Undone, retried, uncertain |
|---|---|---|
| A component fails to load, or is disabled | Its contributions, commands and islands do not exist; pages render without them; `#notices` lists a failure for the owner | Nothing to undo. Open pages refresh after the settle delay |
| A contribution's `Render` fails or panics | That contribution is replaced by a small "could not show this" fragment and logged once per registration; a failing `Wrap` renders its inner content unwrapped; the rest of the page is complete | Nothing partial is written |
| A view fails | A 500 answer with a request id; nothing is swapped, so the page keeps what it showed ([`DH-LIB-GOUISTACK-R13`](../libs/go-ui-stack.md)) | The user retries |
| A region refresh fails | The old HTML stays; a notice says the page may be out of date and offers a reload | Retried on the next `dsh:reset` |
| The connection drops, or the host stops | The `conn` module shows the state and retries with growing delays, without end; forms stay filled; a submit made meanwhile fails visibly and can be repeated | **Uncertain:** whether a submit whose answer was lost took effect; the request id makes the repeat harmless |
| An event cannot be rendered | Nothing special on the page: the host sends a placeholder for that `seq` as an ordinary `session` event, the cursor moves on and the stream stays open ([api.md](api.md), `DH-ADR-API-10`) | Nothing; a fixed build draws the event properly |
| A stream keeps ending without delivering anything (a store that cannot be read, a host that answers 429 or 503, a proxy that cuts the stream) | Each attempt resumes from the page's cursor after the growing delay, or after `Retry-After` when the host sent one. After `ReconnectGiveUp` attempts in a row with no event, the page shows `stalled` ("not live, reload") with two buttons: "try again", which resets the count, and "reload". It does not retry by itself again and never reloads by itself. Other tabs and other sessions are unaffected | The transcript up to the cursor stays as drawn, and forms still post. **Uncertain:** what lies after the cursor |
| The host is replaced by another release | The page keeps working and shows the reload prompt; fragments from the new host are still applied | **Uncertain:** a new fragment may need styles the old page lacks; the prompt says so |
| The first-party script or htmx fails to load | Pages are still complete documents: links navigate, forms post and answer with a redirect. No live updates | The user reloads |
| An island's module fails to load | The fallback inside the element stays; the import is retried when the element next appears | Nothing |
| Browser storage is unavailable, or the shortcut record is unreadable or of a newer version | The per-tab store works in memory. Shortcut defaults apply, editing is disabled, and the stored bytes are never rewritten | The user repairs or clears the record |
| An upload is cancelled or fails | The request is aborted and the row offers a retry from the first byte; other files in the queue continue | **Uncertain:** bytes already stored are unreferenced and A07's collector removes them |
| A language pack is malformed | The pack is rejected whole with file and line; the built-in languages are unaffected | Nothing |

## 7. Decisions

<a id="dh-adr-uicore-1"></a>
### DH-ADR-UICORE-1 The client runtime is not built; each of its guarantees is restated on the host
- **Decision.** Not built: the browser-side cordis tree, the module table and bundle route, hot code replacement, config sync to plugins, the resource registry, the store engine, script realms, and the multiplexed connection with generations. The table in [§6.2](#62-contracts) names, for each, the guarantee it gave and the mechanism that now gives it. Admission (source check, credential provider seam) is A18's and is used as written; this area adds one finding to it: upstream's "every POST must be JSON" fence cannot apply to HTML forms, whose content types a cross-site page may send without a preflight, so the cross-site defence rests on the origin check, `SameSite=Strict` and `http.CrossOriginProtection` ([host-telemetry.md](host-telemetry.md) §6.1, step 5).
- **Why.** Each part exists because the browser was a second program with its own state and its own code loading ([DH-ADR-3](../design.md#dh-adr-3)). The guarantees are about users.
- **Tradeoff.** No plugin code runs in a page, so a plugin process can contribute only data the host knows how to render. Every interaction is a round trip.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7). **Verified by.** DH-T-UICORE-2, DH-T-UICORE-5, [DH-E-UICORE-1](#dh-e-uicore-1), [DH-E-UICORE-2](#dh-e-uicore-2).

<a id="dh-adr-uicore-2"></a>
### DH-ADR-UICORE-2 Features place UI through a host-side region registry, and one state element tells open pages when the composition or the release changed
- **Decision.** The registry of [§6.1](#61-shape): four kinds, election by phase then priority, contributions as effects, failures at load for every ownership mistake. The shell keeps a composition epoch and publishes it, with the release identity, in the state region `#ui-state`. When a page sees a new epoch it raises the same `dsh:reset` event that A19's `reset` raises, so every region fetches itself again; when it sees a new release it asks the user to reload. A region inside a transcript node is refreshed with its node, because only its owner has the node's data.
- **Why.** Upstream's slot registry answers a real question (who may draw where, and what happens when they leave) and its hot reload answers another (how an open page learns). Cordis effects answer the first on the host for free ([DH-O-1](../design.md#dh-o-1)). Reusing `reset` for the second means no new stream event and no second refresh path to test.
- **Tradeoff.** A composition change makes every open tab re-fetch its regions, including the transcript window; it is rare and coalesced. Upstream's scopes (`session`, `session-maybe`) become a field of the scope value, checked at run time, not by a type. Store seats and injected hooks have no counterpart: a contribution is a function that closes over its component's services.
- **Serves.** [DH-O-1](../design.md#dh-o-1), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICORE-4, DH-T-UICORE-5, DH-T-UICORE-6, [DH-E-UICORE-1](#dh-e-uicore-1).

<a id="dh-adr-uicore-3"></a>
### DH-ADR-UICORE-3 One page shell and one view contract: a view gets data, a page is complete without script, untrusted markup is always fenced
- **Decision.** (a) Every page is the layout of [§6.1](#61-shape) around one fragment, and is readable and usable before any script runs. (b) A view is a registered template with one typed data value and the request envelope `ui.Req`; it receives no service, no cordis context and no request. (c) `SafeHTML` has two constructors, and the sanitiser's constructor adds the `hx-disable` container itself, so sanitised model output cannot be placed without it ([libs/micromark.md](../libs/micromark.md)). (d) Assets are embedded, named by content hash and referenced relative to one `<base>`. (e) Templates use only the shared helpers. A declarative panel supplied by the model (upstream's proposed Task Surface) would be one more registered view over a validated description; it is not scheduled here, and its tool and submission rules belong to areas A17 and C4.
- **Why.** Upstream's rule that components never receive the cordis context (`docs/subsystems/slots.md:103`) is what keeps presentation testable; the Go form is a pure function from data to bytes, which is also what lets one view serve a page, an htmx answer and a stream event ([`DH-LIB-GOUISTACK-R1`](../libs/go-ui-stack.md)). The fence must not depend on each template author remembering an attribute.
- **Tradeoff.** A handler must gather everything a view shows before rendering. A wrong field name is found by a test, not the compiler ([DH-ADR-GOUISTACK-1](../libs/go-ui-stack.md#dh-adr-gouistack-1)).
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7). **Verified by.** DH-T-UICORE-1, DH-T-UICORE-2, DH-T-UICORE-3, [DH-E-UICORE-1](#dh-e-uicore-1).

<a id="dh-adr-uicore-4"></a>
### DH-ADR-UICORE-4 One first-party script with a fixed list of jobs; islands are declared by components and framed when they need more than the base policy
- **Decision.** The modules of [§6.1](#61-shape) and nothing else. The script has no knowledge of any feature, keeps no session data, never evaluates text as code and never assigns HTML except through `htmx.swap`. It and every island find the elements they act on by `data-*` attributes and stable ids, never by a class name or an element type alone. A page holds no inline script, handler or style; the only inline block is an import map that a component registered with A19's `web`, which allows it by hash ([§6.1](#61-shape), "Living under a policy with no inline script"). Copying to the clipboard is C2's `k-copy` kit island, not a job of this script. Reconnection keeps upstream's numbers, and a stream that fails repeatedly at one place stops retrying ([§6.3](#63-ownership-failure-retry-rollback-and-cleanup)). An island is registered by its component, loaded when its element appears, fed by attributes and child elements, and framed if it declares extra policy. A stylesheet reaches the page as a link in the head or as an entry of the island table, never from script.
- **Why.** [libs/go-ui-stack.md](../libs/go-ui-stack.md) fixes the mechanisms; seven areas need one owner for the file, or it grows into a client application by increments.
- **Tradeoff.** Hand-written JavaScript with no type checker and no local test runner: without Node in the build ([`DH-LIB-GOUISTACK-R3`](../libs/go-ui-stack.md)) its behaviour is proven only in a real browser. A feature that wants new browser behaviour must fit an existing job or become an island.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICORE-7, DH-T-UICORE-8, [DH-E-UICORE-2](#dh-e-uicore-2), [DH-E-UICORE-3](#dh-e-uicore-3).

<a id="dh-adr-uicore-5"></a>
### DH-ADR-UICORE-5 Browser-side state has four homes, and only shortcut overrides are stored per device
- **Decision.** This applies [DH-ADR-REACT-1](../libs/react.md#dh-adr-react-1). The URL holds navigation. A host preference holds anything the first response must already reflect: collapsed panels, pane sizes, grouping, filters. The DOM holds what the browser keeps by itself (an open `<details>`, focus, selection); an element that must survive a swap carries `hx-preserve`. The per-tab store holds scroll positions and unsent text of plain fields. The composer's draft is host-stored by area C3. `localStorage` is used for one thing: shortcut overrides.
- **Why.** Upstream mixes all of these in one store with `localStorage`, so a second window starts from other values and nothing is linkable (`packages/client/store/README.md:34`).
- **Tradeoff.** Changing a preference is a request. A preference follows the principal to every browser, which would be wrong for a pane width on a small screen; C2's layout therefore clamps a stored size to the viewport and its narrow layout ignores it, so no preference is stored per device class.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICORE-9, [DH-E-UICORE-2](#dh-e-uicore-2).

<a id="dh-adr-uicore-6"></a>
### DH-ADR-UICORE-6 Shortcuts are commands registered on the host and dispatched from a rendered table; overrides stay on the device; the browser's own keys stay with the browser
- **Decision.** A command names the element that performs it, so a key and a click run the same code path. Defaults are declared per web profile with physical key codes; a duplicate id or overlapping defaults fail registration. The script dispatches after local handlers, never during input-method composition, and never takes an unmodified key from a text field. Overrides are a versioned record in `localStorage`: a new binding is active only after it was saved; an unreadable or newer record disables editing and is not rewritten; another tab's change is picked up from the `storage` event. Focus follows three rules: stable ids for everything focusable in a swap target, focus returned after a swap, dialogs as native modal `<dialog>` elements. A framed island gets first refusal on keys and forwards what it does not use.
- **Why.** The upstream note's reasons hold: one host serves devices with different keyboards, and a binding that is active but not saved lies about the next start (`.agents/notes/implemented/architecture/2026-09-20-device-local-shortcut-preferences.md:9-15`).
- **Tradeoff.** Overrides do not follow the user. Chords the browser reserves cannot be bound, and two-key chords, which upstream offers only on the desktop, are not offered.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICORE-10, DH-T-UICORE-11, [DH-E-UICORE-3](#dh-e-uicore-3).

<a id="dh-adr-uicore-7"></a>
### DH-ADR-UICORE-7 UI text comes from a message catalogue resolved per request; English and Chinese are complete; data is never translated
- **Decision.** Text lives in embedded JSON files per namespace and language. English and Chinese must hold every key with the same placeholders; a test fails otherwise. A registration anywhere (a command label, a region title) stores a key, and the text is looked up when a response is rendered. The language is the stored preference, else the best match of `Accept-Language`, else English; changing it reloads the page. Plural forms and text direction are the catalogue's job. Language packs as data files in the harness home are loaded by the same catalogue; upstream's six further languages are their own later deliverable ([DH-D-UICORE-10](#dh-d-uicore-10)). Text written by a user, a model, a tool or a provider is shown verbatim. An island gets its labels as attributes.
- **Why.** The upstream rule and its reasons carry over unchanged; resolving per request removes upstream's "text read once at registration" limit by construction.
- **Tradeoff.** A missing translation is found by a test, not a compiler. The check on templates finds literal text, not a wrongly chosen key.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICORE-3, DH-T-UICORE-12, DH-T-UICORE-13, [DH-E-UICORE-4](#dh-e-uicore-4).

<a id="dh-adr-uicore-8"></a>
### DH-ADR-UICORE-8 A file is uploaded when it is picked, as one streamed request, and a staged file is as durable as the draft that names it
- **Decision.** The base is a plain multipart form posting to A19's upload route, which works without script. The `<dsh-upload>` island adds drop and paste, a first-in-first-out queue, progress with a total, cancel and retry. Images use the same route and are validated when added. A stored file is referenced by the session's draft record (A07), so it survives a reload and a host restart and is collected only when no draft and no event refers to it.
- **Why.** Upstream's reasons for upload-on-add hold (errors appear early; a send carries references). Its receipts live in host memory and its draft attachments in page memory, which loses both on a restart or reload.
- **Tradeoff.** No resumable upload: a failed transfer of a large file over a slow link starts again. Bytes reach the host before the user decides to send; removing a chip leaves them to the collector.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICORE-14, [DH-E-UICORE-4](#dh-e-uicore-4).

<a id="dh-adr-uicore-9"></a>
### DH-ADR-UICORE-9 Usage events are recorded on the host from a closed catalogue, and nothing is sent until the user opts in
- **Decision.** The browser sends no analytics request. A usage event is recorded by the route handler that served the interaction, as a typed event declared through A18's `report.Declare` under kind `analytics`, with enumerated fields only. It leaves the machine only with a permit from A18's sharing gate, which is off by default ([DH-ADR-9](../design.md#dh-adr-9)). Nothing samples the machine, and no account or device identity is attached.
- **Why.** Every interaction is already a request, so a second, script-side sender would only add a path that could bypass the gate.
- **Tradeoff.** Interactions that never reach the host (hover, scroll) are not counted. With no collector named, opting in sends nothing ([DH-ADR-HOST-6](host-telemetry.md#dh-adr-host-6)).
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** DH-T-UICORE-15.

## 8. Deliverables

<a id="dh-d-uicore-1"></a>
### DH-D-UICORE-1 Every page is one complete document in one shell, written against one view contract
- **Result and why it matters.** A UI area writes a template and a typed data struct and gets a page with the same landmarks, policy, language and connection behaviour as every other page. A person sees the conversation before any script has loaded.
- **How it is implemented.** The `ui` package holds the view registry of [libs/go-ui-stack.md §6](../libs/go-ui-stack.md), the helper map and the two `SafeHTML` constructors; `ui.Check` runs every registered view's template lookup at start-up. `shell.Page` renders the layout from a template whose regions call `Shell.Render`. `shell.Env` builds `ui.Req` from the stored language preference, `Accept-Language`, the zone cookie, the peer, and the colour scheme that C2's `theme.Key` reports for the principal when that optional service is present (`system` otherwise). `ui/assets` embeds scripts, styles and the manifest with a SHA-256 per file; the `asset` helper puts the hash in the URL.
- **Components and files.** `internal/ui`, `internal/ui/shell`, `internal/ui/assets`.
- **Depends on.** [DH-D-UICORE-7](#dh-d-uicore-7); [DH-D-API-1](api.md#dh-d-api-1) and [DH-D-API-2](api.md#dh-d-api-2) (`web` calls `Shell.Page` and `Shell.Env`, composes the policy from `ui.BasePolicy` and writes every security header, and gives the layout the form token through `rq.Env()` and a request id through `rq.NewRequestID()`); [DH-D-HOST-1](host-telemetry.md#dh-d-host-1) (`host.assets` serves the embedded files). The shell renders without the kit's stylesheet. Runtime prerequisite: none beyond the host.
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P0. **Done when.** DH-T-UICORE-1, DH-T-UICORE-2 and DH-T-UICORE-3 pass and [DH-E-UICORE-1](#dh-e-uicore-1) has passed.

<a id="dh-d-uicore-2"></a>
### DH-D-UICORE-2 A feature contributes UI to a place it does not own, and its UI leaves with it, in open pages too
- **Result and why it matters.** Disabling a component in configuration removes its buttons, panels and cards from every open tab without a reload, and enabling it brings them back. A page that outlived a binary upgrade says so.
- **How it is implemented.** The registry is a map from region name to declaration and contributions, replaced copy-on-write under the shell's lock; each registration's cleanup removes its entry. `Render` buffers each contribution separately and contains a failure. The shell listens for the loader's settled-reload notice, bumps the epoch, raises `shell.Changed` and re-renders the `ui` state source. The `live` helper gives a region its self-fetch attributes. The release identity comes from the build information of the binary.
- **Components and files.** `internal/ui/shell`.
- **Depends on.** [DH-D-UICORE-1](#dh-d-uicore-1), [DH-D-UICORE-3](#dh-d-uicore-3), [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1), [DH-D-CORDIS-4](../libs/cordis.md#dh-d-cordis-4), [DH-D-LOADER-11](../libs/cordis-plugins.md#dh-d-loader-11). [DH-D-API-4](api.md#dh-d-api-4) (a state source with topic `ui`, and `Tab.Watch` from the layout), [DH-D-HOST-10](host-telemetry.md#dh-d-host-10) (the inventory of failed components for the notice).
- **Outcome.** [DH-O-1](../design.md#dh-o-1), [DH-O-12](../design.md#dh-o-12). **Priority.** P0, the registry, the refresh of open pages and the release prompt together. **Done when.** DH-T-UICORE-4, DH-T-UICORE-5, DH-T-UICORE-6 and DH-T-UICORE-16 pass and [DH-E-UICORE-1](#dh-e-uicore-1) has passed.

<a id="dh-d-uicore-3"></a>
### DH-D-UICORE-3 An open page stays connected, says what state it is in, and loses nothing the user typed
- **Result and why it matters.** After a network cut, a sleeping laptop, a parked tab or a host restart the page resumes by itself; while it cannot, the user sees why and what they typed is still there.
- **How it is implemented.** The `stream`, `conn`, `state` and `keep` modules of [§6.1](#61-shape), about a screen of code each, and the `ui.state` route. The reconnect numbers and the hidden-tab grace reach the script as attributes on `<html>`. The state markup for all seven connection states is in the layout from the start, so showing one needs no request. The `conn` module counts consecutive attempts on one stream that delivered no event, honours `Retry-After`, and stops at A19's `ReconnectGiveUp`.
- **Components and files.** `internal/ui/assets` (`dsh.js`, `stream.js`, `conn.js`, `state.js`, `keep.js`), `internal/ui/shell`.
- **Depends on.** [DH-D-UICORE-1](#dh-d-uicore-1), [DH-D-API-4](api.md#dh-d-api-4) (the stream routes, event names and cursor rules as written). Runtime prerequisite: a browser on the supported list ([libs/go-ui-stack.md §7](../libs/go-ui-stack.md), item 7).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P0. **Done when.** DH-T-UICORE-7 and DH-T-UICORE-8 pass and [DH-E-UICORE-2](#dh-e-uicore-2) has passed.

<a id="dh-d-uicore-4"></a>
### DH-D-UICORE-4 A component can ship a browser widget that loads only when shown and cleans up when removed
- **Result and why it matters.** The composer, the terminal and the upload control work without a bundler and without loading anything on pages that do not show them.
- **How it is implemented.** `Shell.Island` validates the entry (a custom-element name, a module in the manifest, a frame policy from A19's closed list) and adds it to the table the layout renders; for a framed island it registers the `Frame` route. The layout writes the import maps that components registered with `web.Routes.AllowInlineData`, unchanged, ahead of the first module script. The embedded asset manifest has one entry per file with its content hash and, for a vendored file, its source, version and licence ([app-web.md](app-web.md), `DH-D-WEBAPP-2`). The `islands` module watches for listed elements with a `MutationObserver` and imports modules on demand. A frame route serves a minimal document with the island's module, stylesheet and policy; a small frame-side module relays the closed message set, `init` first.
- **Components and files.** `internal/ui/shell`, `internal/ui/assets` (`islands.js`, `frame.js`).
- **Depends on.** [DH-D-UICORE-1](#dh-d-uicore-1), [DH-D-UICORE-3](#dh-d-uicore-3). [DH-D-API-1](api.md#dh-d-api-1) and [DH-D-API-2](api.md#dh-d-api-2) (the route kind `Frame` with its policy, `AllowInlineData`, and the headers `web` writes on a page and on a frame document).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P0, because the composer is an island ([DH-ADR-21](../design.md#dh-adr-21)). **Done when.** DH-T-UICORE-7 passes and [DH-E-UICORE-3](#dh-e-uicore-3) has passed.

<a id="dh-d-uicore-5"></a>
### DH-D-UICORE-5 Collapsed panels, pane sizes, scroll positions and unsent text survive swaps, reloads and other tabs
- **Result and why it matters.** The page looks the way the user left it, in every tab, with no flash of a default layout.
- **How it is implemented.** `prefs.Declare` registers a typed preference over a `kv` namespace; views read it when rendering, and a control changes it with `hx-put` to `ui.pref` (a `<details>` posts on its `toggle` event). The `keep` module saves and restores scroll offsets per URL and element key, and field text per form key, in `sessionStorage`.
- **Components and files.** `internal/ui/prefs`, `internal/ui/assets` (`keep.js`).
- **Depends on.** [DH-D-UICORE-1](#dh-d-uicore-1), [DH-D-UICORE-3](#dh-d-uicore-3). [DH-D-STORAGE-1](storage.md#dh-d-storage-1) (the `kv` namespace `ui-prefs`), [DH-D-UTIL-1](util-test-support.md#dh-d-util-1) (`schema` validation of a preference value).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UICORE-9 passes and [DH-E-UICORE-2](#dh-e-uicore-2) has passed.

<a id="dh-d-uicore-6"></a>
### DH-D-UICORE-6 The UI can be driven from the keyboard, with shortcuts a person can change on their device, and focus that survives live updates
- **Result and why it matters.** A keyboard or screen-reader user can do everything a mouse user can; a streaming answer does not steal focus or flood a screen reader.
- **How it is implemented.** `keys.Registry` checks and stores commands and renders the table for the request's platform into `#ui-keys`. The `keys` module merges the device's overrides, matches physical codes, handles the double-Escape sequence and exports `list`, `set` and `reset` for area C2's shortcut settings page. The `focus` module and the layout's landmarks, skip link and live regions implement the rules of [DH-ADR-UICORE-6](#dh-adr-uicore-6).
- **Components and files.** `internal/ui/keys`, `internal/ui/assets` (`keys.js`, `focus.js`).
- **Depends on.** [DH-D-UICORE-2](#dh-d-uicore-2), [DH-D-UICORE-3](#dh-d-uicore-3), [DH-D-UICORE-7](#dh-d-uicore-7).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UICORE-10 and DH-T-UICORE-11 pass and [DH-E-UICORE-3](#dh-e-uicore-3) has passed.

<a id="dh-d-uicore-7"></a>
### DH-D-UICORE-7 The UI speaks English and Chinese, chosen per request
- **Result and why it matters.** Both upstream baselines remain possible, and a fragment pushed on a stream is in the language of the tab that receives it.
- **How it is implemented.** `msg.Catalog` loads each namespace's files at registration, checks the two baselines against each other, and answers lookups through a fallback chain that ends at English. Language matching and plural selection use `golang.org/x/text` (`language`, `feature/plural`; item `DH-LIB-UICORE`: v0.44.0, BSD-3-Clause, checked on 2026-11-18 with `go list -m -json` and by reading `LICENSE`, `language/match.go` and `feature/plural/plural.go` in the module cache). A template test rejects literal text.
- **Components and files.** `internal/ui/msg`.
- **Depends on.** No implementation dependency. `Match` takes the stored value as an argument, so the language follows `Accept-Language` until the preference store exists.
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P0: the catalogue with English and Chinese complete, and the loading of language packs from the home. Upstream's six further languages are [DH-D-UICORE-10](#dh-d-uicore-10). **Done when.** DH-T-UICORE-3, DH-T-UICORE-12 and DH-T-UICORE-13 pass and [DH-E-UICORE-4](#dh-e-uicore-4) has passed.

<a id="dh-d-uicore-8"></a>
### DH-D-UICORE-8 A person can add files and images to a message by picking, dropping or pasting, and sees progress
- **Result and why it matters.** Large files do not freeze the page, a refused image is reported when it is added, and what was added is still there after a reload.
- **How it is implemented.** The `<dsh-upload>` element wraps the plain form. With script it takes files from the input, from a drop on the element named by its `for` attribute and from a paste, sends one `XMLHttpRequest` per file in order (the browser streams the file from disk), shows a `<progress>` per file from upload progress events, aborts on cancel, and swaps each answer (the chip fragment) with `htmx.swap`. Limits and labels arrive as attributes; the host enforces the limits.
- **Components and files.** `internal/ui/assets` (`upload.js`), a view for the control in `internal/ui/shell`.
- **Depends on.** [DH-D-UICORE-4](#dh-d-uicore-4), [DH-D-API-5](api.md#dh-d-api-5) (`session.upload` and its answer), [DH-D-STORAGE-3](storage.md#dh-d-storage-3) (`attachments.Store`, and the `draft` namespace that keeps a staged file alive).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UICORE-14 passes and [DH-E-UICORE-4](#dh-e-uicore-4) has passed.

<a id="dh-d-uicore-9"></a>
### DH-D-UICORE-9 Usage of the shell can be counted, by a user who opted in
- **Result and why it matters.** The project can learn which views are used without any data leaving a machine by default.
- **How it is implemented.** `ui/usage` declares two events, a page view (view name from a closed set) and a shortcut use (command id of a built-in command, else `custom`), and reports them from the shell's handlers. Other UI areas declare their own events the same way.
- **Components and files.** `internal/ui/usage`.
- **Depends on.** [DH-D-UICORE-1](#dh-d-uicore-1), [DH-D-HOST-6](host-telemetry.md#dh-d-host-6) (`report.Key`, `report.Declare`), [DH-D-HOST-4](host-telemetry.md#dh-d-host-4) (the sharing gate).
- **Outcome.** [DH-O-14](../design.md#dh-o-14). **Priority.** P2. **Done when.** DH-T-UICORE-15 passes. A real collector is exercised by area A18's own scenario, not by this deliverable.

<a id="dh-d-uicore-10"></a>
### DH-D-UICORE-10 The UI also speaks upstream's six further languages
- **Result and why it matters.** A person whose language is one of the six further languages upstream ships reads the product in it; split from [DH-D-UICORE-7](#dh-d-uicore-7) so that the first working harness waits only for English and Chinese.
- **How it is implemented.** One embedded catalogue file per namespace for each of the six languages, translated from the English baseline, with their own names, plural rules and text direction entries in `Languages()`; a key a translation lacks falls back to English, as for a pack.
- **Components and files.** `internal/ui/msg` (catalogue data only).
- **Depends on.** [DH-D-UICORE-7](#dh-d-uicore-7) (the catalogue, matching and fallback).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P2. **Done when.** DH-T-UICORE-17 passes.

## 9. Acceptance

### 9.1 Local tests

Run with `net/http/httptest`, an HTML parser (`DH-LIB-DOMTEST`), fakes of other areas' services and the real loader where stated. **They cannot run JavaScript**: the behaviour of the first-party script is proven only by the scenarios of §9.2. Nothing has been run.

| Test ID | Proves | What is asserted |
|---|---|---|
| DH-T-UICORE-1 | D-1 | Every registered page renders inside the layout with each landmark once, one `<base>`, `lang` and `dir`, the locked `htmx-config`, the tab header, and only document-relative references; a fragment has identical bytes inside a page and alone; `ui.Check` fails on a view with no template |
| DH-T-UICORE-2 | D-1 | Golden: with every `<script>` removed, a session page still holds the transcript, the session list and a prompt form that posts and is answered by a redirect |
| DH-T-UICORE-3 | D-1, D-7 | A walk over all templates finds no natural-language text node and no literal `title`, `alt`, `placeholder` or `aria-label`; no template defines its own function; a hostile-string table through every helper yields no new element, attribute or `hx-*`; a static check finds `FromSanitizer` called only from the sanitiser package and its output always inside an `hx-disable` container |
| DH-T-UICORE-4 | D-2 | Registry rules: undeclared region, second declaration, unknown phase and duplicate entry fail; list order; election by phase before priority; keyed dispatch; wrap nesting order; a contribution without `Match` in a `One` region is a candidate |
| DH-T-UICORE-5 | D-2 | Through the real loader: disabling a component removes its contributions, commands and islands and bumps the epoch once per settled reload; re-enabling restores them; an owner's unload removes its region and what others put in it |
| DH-T-UICORE-6 | D-2 | A contribution that errs or panics yields the placeholder and the rest of the page; a failing wrap yields its inner content; `ui.state` answers the current epoch and release, 401 without a peer; `Touch` from an update hook bumps the epoch |
| DH-T-UICORE-7 | D-3, D-4 | Each embedded script equals its manifest hash; the first-party script is under its size cap and contains no `eval`, `Function(`, `innerHTML` or `document.write`; every island in the table has a module in the manifest; a framed island is served only by its own `Frame` route, a frame policy outside A19's closed list fails `Shell.Island`, and an unframed island has no frame route. No rendered page, fragment or `shell.head` contribution holds a `<script>` without `src`, a `<style>`, a `style` attribute, an `on…` attribute or `hx-on`, except the import maps registered through `AllowInlineData`, which the layout writes byte for byte before the first module script; every vendored entry of the asset manifest has a source, a version and a licence; `<body>` carries `X-DSH-Tab` and `X-DSH-Token` in `hx-headers`, and every rendered form carries `_token` (A19's DH-T-API-25 checks the header and the forgery rule themselves) |
| DH-T-UICORE-8 | D-3 | The layout carries the reconnect and grace values of `shell.Config`, A19's `ReconnectGiveUp` and the markup of all seven connection states, hidden; every region made by `live` has an id, a self-fetch address and the `dsh:reset` trigger; the sign-in notice of [DH-ADR-28](../design.md#dh-adr-28) is rendered for a new browser session with its time and a "sign out everything" form that carries `_token` and posts A18's revoke-all route |
| DH-T-UICORE-9 | D-5 | A preference set through one request is in the next page rendered for the same principal on another connection and not for another principal; an undeclared name is 404; a value of the wrong shape is 422; a `<details>` renders open or closed from its preference |
| DH-T-UICORE-10 | D-6 | A duplicate command id and overlapping defaults in one profile fail registration; the table differs per platform and holds keys, not text; a command leaves with its owner; a fixed command cannot be overridden |
| DH-T-UICORE-11 | D-6 | In every rendered page: a skip link, one `<main>`, the polite live region, an accessible name on every control and dialog, and an `id` on every focusable element inside a swap target |
| DH-T-UICORE-12 | D-7 | English and Chinese hold every key of every namespace with equal placeholders; the language choice table (stored, header, fallback); plural forms for both; `dir` for a right-to-left tag; a key that is missing at run time renders as the key and fails the test run |
| DH-T-UICORE-13 | D-7 | A language pack with a syntax error or an unknown placeholder is rejected whole with file and line; a pack cannot replace a built-in language; removing a pack returns its users to their fallback |
| DH-T-UICORE-14 | D-8 | The upload control renders its limits and labels as attributes and contains a working plain form; a multi-file multipart post through the route fake yields one chip per file; a body cut off mid-file yields no chip |
| DH-T-UICORE-15 | D-9 | With a fresh home and a recording transport no request leaves; with a permit only declared events leave; an event with a free-text field fails declaration; no event carries an account, device or session id |
| DH-T-UICORE-17 | D-10 | Each of the six further languages is offered by `Languages()` with its own name and direction, every key it holds has the English placeholders, and a missing key falls back to English; `Match` picks each from a matching `Accept-Language` |
| DH-T-UICORE-16 | D-2 | The `ui` state source, and every source a `live` region registers in the test composition, stops all its goroutines within a deadline after its `stop` is called |

### 9.2 Real-environment scenarios

Each needs a real browser driving the built binary through the driver chosen in [libs/playwright.md](../libs/playwright.md); the replay model provider stands in for a model API. A scenario that is skipped, blocked or failed stays open. Each proves the stated contracts for the tested build, browser and operating system.

<a id="dh-e-uicore-1"></a>
#### DH-E-UICORE-1 A feature appears and disappears in an open page, and a new release is announced
- **Proves.** [DH-D-UICORE-1](#dh-d-uicore-1), [DH-D-UICORE-2](#dh-d-uicore-2).
- **Given.** The built `dsh` in its web profile on loopback with a temporary harness home; a real Chromium with script enabled and a second profile with script disabled; a test component that contributes one button to `shell.header` and one panel to `shell.aside`.
- **When.** (1) Open a session in both profiles. (2) Type a half-finished line into a plain form field. (3) Disable the test component in the writable configuration layer, then enable it. (4) Stop the host and start a build with another revision on the same home.
- **Then.** (1) Both profiles show the transcript and can send a prompt. (3) Without a reload the button and panel vanish and return in the scripted profile, the typed text is still in its field, and no console error is logged. (4) The page shows "host not reachable", then reconnects and shows the reload prompt; choosing reload gives a page from the new build.
- **Cleanup.** Restore the layer, stop the host, delete the home and both profiles. On failure keep the host log and the browser console.

<a id="dh-e-uicore-2"></a>
#### DH-E-UICORE-2 A page reconnects through every kind of interruption without losing input or layout
- **Proves.** [DH-D-UICORE-3](#dh-d-uicore-3), [DH-D-UICORE-5](#dh-d-uicore-5).
- **Given.** The built `dsh` in its web profile on loopback with a temporary harness home and the replay model provider; a real Chromium, signed in as the owner, showing a long session scrolled to its middle with one panel collapsed; a second browser profile signed in as the same owner.
- **When.** (1) Set the browser offline, submit a form, set it online and submit again. (2) Hide the tab longer than the grace period and show it. (3) Sign the browser out from the second profile. (4) Sign in again and reload. (5) Open the session in the second profile. (6) With a test build whose renderer fails on one marked event, send a message that produces that event. (7) Put a proxy in front of the host that ends every stream at once, wait, choose "try again", remove the proxy fault and choose "try again" once more.
- **Then.** (1) The first submit fails visibly with the text kept; the state goes from offline to connected without a reload; the second submit takes effect once. (2) The tab resumes with no gap in the transcript. (3) The page shows "signed out" and how to start `dsh`, and sends nothing further. (4) The scroll position and the collapsed panel are as they were. (5) The second profile shows the same collapsed panel. (6) A placeholder row appears in place of the event, later events follow and the stream stays open. (7) The stream is retried with growing delays `ReconnectGiveUp` times; then the page shows "not live, reload" and makes no further stream request by itself; the first "try again" repeats that once; the second resumes from the cursor with no gap.
- **Cleanup.** Remove the proxy, stop the host, delete the home and both browser profiles. On failure keep the host log and the browser console.

<a id="dh-e-uicore-3"></a>
#### DH-E-UICORE-3 Islands load on demand, and the keyboard reaches everything
- **Proves.** [DH-D-UICORE-4](#dh-d-uicore-4), [DH-D-UICORE-6](#dh-d-uicore-6).
- **Given.** The built `dsh` in its web profile on loopback with a temporary harness home and the replay model provider, on Windows, macOS and Linux, each with a real Chromium-family browser signed in as the owner; a test island (unframed) and a framed test island that needs an inline style; a Chinese input method installed; a browser that records policy violations and network requests.
- **When.** (1) Open a page with no island, then swap in each island, then swap them out. (2) Walk the page with the keyboard only: skip link, navigation, a dialog, back. (3) Trigger a command by its default chord, rebind it, reload, and trigger it by the new chord in a second tab. (4) Type with the input method while a chord's key is part of a composition. (5) Corrupt the stored shortcut record and reload. (6) Let an answer stream while focus is in a text field.
- **Then.** (1) No island module is requested before its element appears; removing an element ends its timers and sockets; the framed island runs under its own policy and the page records no violation. (2) Every control is reachable and named; closing the dialog returns focus to its opener. (3) The new binding works in both tabs and the old one does not. (4) No command fires during composition. (5) Defaults apply, editing is disabled, the stored bytes are unchanged. (6) Focus and caret stay; the accessibility tree shows one status announcement at the start and one at the end.
- **Cleanup.** Clear the browser storage, stop the host, delete the home.

<a id="dh-e-uicore-4"></a>
#### DH-E-UICORE-4 The UI switches to Chinese, and files are uploaded with progress and survive a reload
- **Proves.** [DH-D-UICORE-7](#dh-d-uicore-7), [DH-D-UICORE-8](#dh-d-uicore-8).
- **Given.** The built `dsh` in its web profile on loopback with a temporary harness home and the replay model provider; a real Chromium signed in as the owner on a session; a 200 MiB file, a small text file, a valid image and an undecodable image; a second browser profile whose preferred language is Chinese and that has no stored preference.
- **When.** (1) Open the UI in the second profile. (2) In the first, switch the language to Chinese in settings. (3) Pick the large file and cancel it half-way, then retry; drop the text file; paste the two images. (4) Reload the page. (5) Restart the host and send the message.
- **Then.** (1) The page is in Chinese with `lang` set. (2) Every visible string, the island labels and the next streamed fragment are Chinese; file names and model output are unchanged. (3) Progress shows a total and advances; the cancel stops the request; the retry completes; the bad image is refused when added and the others become chips. (4) and (5) The chips are still there and the sent message refers to the three stored files.
- **Cleanup.** Delete the session, stop the host, delete the home and the profiles.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Settled with [api.md](api.md).** `web` writes every security header, composing the page policy from `ui.BasePolicy` (`DH-ADR-API-9`); an import map is registered with `AllowInlineData` and the microphone allowance with `AllowFeature`; each framed island has its own `Frame` route with a `Route.Policy`; state-changing requests are same-origin and carry `Peer.FormToken()`, which reaches the layout through `rq.Env()`; a render failure is a fallback row or a placeholder, and the script stops after `ReconnectGiveUp` attempts and shows `stalled` (`DH-ADR-API-10`); `access.Admitted()` is the rule for every admitted principal; `rq.NewRequestID()` gives the request id; `rq.Env()` returns `shell.Env(...)`. The names `X-DSH-Tab` (header), `dsh:reset` and `dsh:swapped` (DOM events) are this area's and final.
2. **Assumed of other areas.** A18: asset names with a content hash are served with a long cache lifetime. A21: the settled-reload notice of `DH-D-LOADER-11` reaches a harness component. A22: a plugin process contributes data only; if it is ever to contribute static assets or a region, that is a new capability with its own decision.
3. **Not verified, because it needs a run.** That every supported browser accepts a hash source for an import map and resolves later dynamic imports through it; that `htmx.swap` applies a fragment for an event the page has no main target for; that a `<meta>` island table and `hx-headers` on `<body>` behave as read in htmx 2.0.11; that `showModal()` and focus return work in each supported browser; that a `storage` event reaches other tabs in each; the size cap of the script; the cost of a full region refresh on a long transcript.
4. **The script has no local tests.** Without Node there is no runner for JavaScript unit tests; the static checks of DH-T-UICORE-7 and the four scenarios are its only evidence. If that proves too thin, a browser-run test page driven by the scenario driver is the next step, not a Node toolchain.
5. **Limits kept.** No resumable upload. Browser-reserved chords cannot be bound; no two-key chords. No durable analytics outbox. Shortcut overrides do not follow the user. A composition change refreshes every open tab. Six of upstream's eight languages come later ([DH-D-UICORE-10](#dh-d-uicore-10), P2). A notification-level change of language needs a page reload.

## 11. Units dispositioned by this spec

Ledger fragment: `trace/fragments/C1.jsonl`.

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 14 | 3 ported, 9 replaced, 2 dropped |
| `UP-DEBT` | 38 | 21 resolved, 14 not applicable, 3 carried |
| `UP-EVT` | 1 | 1 dropped |
| `UP-CFG` | 4 | 1 ported, 3 replaced |
| `UP-SVC` | 5 | 3 replaced, 1 merged, 1 dropped |
| `UP-DEC` (handed over) | 22 | 9 ported, 9 replaced, 3 dropped, 1 not applicable |

Go items declared: `DH-ADR-UICORE-1` to `-9`, `DH-D-UICORE-1` to `-9`, `DH-T-UICORE-1` to `-16`, `DH-E-UICORE-1` to `-4`, `DH-LIB-UICORE`.
