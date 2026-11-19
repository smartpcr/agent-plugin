# The web application as a product, and what became of the desktop application: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) (`2e39e70`) and are cited as `path:line` relative to the upstream root. Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `WEBAPP`. Nothing here has been built, run or tested.

| | |
|---|---|
| **Area key** | `WEBAPP` (agent A25) |
| **Upstream packages** | 5 applications: `apps/web`, `apps/desktop`, `apps/desktop-host`, `apps/desktop-publisher`, `apps/plugin-market-generator` |
| **Serves outcomes** | [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14), [DH-O-15](../design.md#dh-o-15), [DH-O-16](../design.md#dh-o-16) |
| **Depends on areas** | A18 (listener, launch, host reuse, assets mount, fatal diagnostic, inventory: [host-telemetry.md](host-telemetry.md)), A19 (routes, event stream, access: [api.md](api.md)), A24 (`dsh web`, `dsh stop`, `dsh update`, the stop function, the update check: [app-cli.md](app-cli.md)), A27 (the `web` profile: [bundle-preset.md](bundle-preset.md)), A21 (readiness and the required-entry audit: [boot.md](boot.md)), C1 (page shell, region registry, embedded assets, first-party script: [ui-core.md](ui-core.md)), A23 (the replay provider and the scenario helper: [util-test-support.md](util-test-support.md)), A17 (secret store), A04 (which models can be called), C2 to C7 (views and islands) |
| **Priority** | P0 for the assembly, the embedded assets and the browser test scaffold: without them there is no web product and no proof of one. P1 for first run, the lifecycle states and the budgets. P2 for the installable web app |

## 1. Purpose

Other areas specify parts: views (C1 to C7), routes and the stream (A19), the listener (A18), the profile's contents (A27). This area specifies **the product those parts make**: what `dsh web` is from the moment a person runs it to the moment they stop it, what a first-time user and a user on a second device see, how the files a page needs get into the binary, how a real browser proves the whole thing works, and how fast it must be.

It has a second job. Upstream ships a desktop application, and the owner decided that the Go product has none ([DH-ADR-18](../design.md#dh-adr-18)). This document goes through everything upstream's desktop application, its private host, its release publisher and its plugin market generator do, gives each duty one answer, and states in one place ([§6.5](#65-what-a-user-loses-without-a-desktop-application)) what a user loses. Without it, "port desktop to web" would be a sentence nobody could check.

## 2. Vocabulary

Framework terms, **host**, **profile**, **harness home**, **fragment** and **island** are defined in [../design.md §2](../design.md#2-vocabulary); **launch token**, **runtime record**, **local mode** and **remote mode** in [host-telemetry.md §2](host-telemetry.md); **attention stream** and **park** in [api.md §2](api.md); **region**, **contribution**, **release** and **epoch** in [ui-core.md §2](ui-core.md); **stop function** in [app-cli.md §2](app-cli.md); **required entry** and **readiness** in [boot.md §2](boot.md).

| Term | What it is | Why it exists |
|---|---|---|
| **Assembly** | The web product as composed: the `web` profile, the embedded assets, the start sequence and the product-level pages (setup, about, stop). | A set of correct parts can still fail to start, or start with a part missing. Somebody must own "it works when put together". |
| **Embedded asset, asset record** | A file a page loads (script, stylesheet, font, icon), compiled into the binary. The record lists each file's hash, size, source, version and licence. | The UI and the host can never be different versions, and nothing is fetched from another origin ([go-ui-stack.md](../libs/go-ui-stack.md), `R3`, `R5`). |
| **Setup page, usable model** | The page a person sees while no model can be called. A model is usable when a provider is configured and its credential resolves. | A harness with no model does nothing; the first screen must say so and fix it. |
| **Web app manifest** | A small JSON file that tells a browser the site's name, icons and that it may open in a window of its own ("install"). | The nearest web equivalent of a desktop window with its own taskbar entry. |
| **Desktop duty** | One thing upstream's desktop application did for its user or operator. | The unit of the table in [§6.4](#64-every-desktop-duty-and-its-answer). |
| **Product status** | The few facts about the running product a person must see: version, an available update, a pending restart, an unclean previous exit, a start without plugins, a network listener without TLS. | With no native dialog and no tray, the page is the only place to say them. |
| **Browser lane** | The set of tests in which a real browser drives the built binary, with recorded model output and no API key. | A server-rendered page still has behaviour only a browser shows: streams, focus, scrolling, islands. |
| **Budget** | A reviewed constant in source that a measured time or size must stay under. | "Fast enough" becomes a failing test, not an opinion. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-apps.web` | `@deepseek-ai/dsh-web-frontend` | consumer (build entry) | A Vite build over the client shell library; its `dist/` is what `dsh web` serves (`apps/web/package.json:3`). 38 KB of source and 4.5 MB of tests: it is mostly the browser test suite. |
| `UP-PKG-apps.desktop` | `@deepseek-ai/dsh-desktop` | other (native shell) | An Electron shell around the complete web application, with windows, menus, tray, recovery, update, installers and a bundled runtime (`apps/desktop/README.md:40`). 657 files. |
| `UP-PKG-apps.desktop-host` | `@deepseek-ai/dsh-desktop-host` | consumer | The child process Electron starts in Node mode: it runs the shared web profile and answers the shell over private IPC (`apps/desktop-host/src/index.ts:1`; `docs/architecture.md:53`). |
| `UP-PKG-apps.desktop-publisher` | `@deepseek-ai/dsh-desktop-publisher` | other (release tool) | Uploads retained desktop packages to one vendor's storage, changes a release's rollout percentage and refreshes website installers (`apps/desktop-publisher/README.md:11`). |
| `UP-PKG-apps.plugin-market-generator` | `@deepseek-ai/dsh-plugin-market-generator` | other (reference tool) | Reads a list of npm packages and Git repositories and writes one `index.json` for the plugin market's Discover page; it never runs the packages' code (`apps/plugin-market-generator/README.md:5`). |

### 3.2 Behaviour

**The web application is an entry, not an application.** `index.html` holds a root element, one module script, links to the install manifest and icons, and a build-time title (`apps/web/index.html:6-16`). `main.ts` constructs the shell from the client library and runs it (`apps/web/src/main.ts:20`, `:37`). It cannot be served alone: the Vite configuration refuses its own development server, because only the host can inject the boot manifest the shell needs (`apps/web/vite.config.ts:12-14`, `:48-54`). `dsh web` boots the web profile, prints a URL that carries a credential, and opens the browser unless told not to or under SSH (`packages/bundle/web-app/README.md:57-62`, `:89`). A source checkout must be built first; with no `dist/` start-up stops with a hint (`:223`). The build also emits the brand font's licence beside the font (`vite.config.ts:33-45`).

**Install manifest.** Four icons rendered from the official tile (192 and 512 pixel, a maskable 512 and a 180 pixel Apple touch icon), `display: standalone`, relative `start_url` and `scope`, and no service worker (`apps/web/public/manifest.webmanifest:2-28`; note `2026-10-13-web-install-icons-and-launch-display.md:13-17`, `:31`).

**The browser test suite.** `apps/web/tests` holds 251 scenario files, 109 committed expected outputs and their fixtures. They boot the real web composition in-process and drive it with a real Chromium over real HTTP; recorded sessions are replayed as model output, so no key is needed (`apps/web/tests/README.md:5`; note `2026-07-24-web-gui-browser-e2e-lane.md:13`, `:19`). Two scenarios also run in WebKit (`README.md:5`). Three families are covered: live turns (tool execution, cancellation, failure, retry, questions, steering), cold history (rendering, search, trajectory) and browser lifecycle (first send, reload recovery, theme and language, workspaces), plus an 88-turn scroll contract (note `:49`). Visible states are compared with normalised accessibility-tree snapshots (`:39`). Every scenario fails on a page error, on a connection-loss warning and on an unconsumed recording, because a self-healing client would otherwise hide a broken stream (`:35`). An inactive component in the shipped composition is a failure, not a warning (`README.md:19`). One scenario runs the real command against a real model and skips without a key (`apps/web/tests/smoke-real.e2e.ts:1-6`).

**Performance gates.** A required benchmark opens a synthetic 240-turn session in Chromium, loads every older page, opens the trajectory view and types a draft while a reply streams; budgets are constants in source and no environment variable may loosen them (`benchmarks/long-session-browser/README.md:5`, `:13`; `benchmarks/AGENTS.md:3`, `:13`). A second gate rebuilds an unfinished 100,000-delta message after a reconnect (`benchmarks/active-stream-reconnect/README.md:5`). The numbers are in [DH-ADR-WEBAPP-7](#dh-adr-webapp-7).

**The desktop application** loads the same web assets in an Electron window, starts the host as a child, and forwards requests with a credential the page never holds (`apps/desktop/README.md:40-42`). Around that it adds what a web page cannot do: a welcome window that checks for a credential before the workspace opens (`:224`); menus, a Windows tray icon and a close-to-background rule (`:76-80`); a quit question that asks the host what would be interrupted (`:80`); native fatal recovery (`:184`); an embedded browser pane and embedded vendor account pages (`:156`, `:52`); an update flow with separate download and restart approvals and a vendor-controlled mandatory-update policy (`:286-292`, `:330-359`); installers for Windows and macOS (`:262-270`); and a bundled Node, pnpm and Python with data and Office libraries (`:50`, `:110`). One release number fixes the shell, the web client, the host and the bundled runtime together (`:124`). The desktop host adds the IPC ends of those features: credential receipt, readiness, quit inspection, update admission, fatal diagnostics (`apps/desktop-host/src/index.ts:24`, `:85`, `:168`, `:178`; `quit-inspection.ts:16-23`; `update-tasks.ts:23-28`). [§6.4](#64-every-desktop-duty-and-its-answer) lists every duty.

**The publisher and the generator** are command-line tools outside the product. The publisher's workflows own operator inputs, credentials and approvals, and it installs only its own dependencies with lifecycle scripts off (`apps/desktop-publisher/README.md:21`). The generator downloads and reads files only, with size limits, integrity checks and Git hooks disabled (`apps/plugin-market-generator/README.md:74`).

### 3.3 Failure, retry, rollback and cleanup upstream

A missing web build stops start-up with a build hint and no fallback to serving source (`packages/bundle/web-app/README.md:223`). A desktop start-up failure shows one native dialog per process with exit, restart, "disable third-party plugins, back up the profile patch and restart", and a diagnostic report (`apps/desktop/README.md:184`). A failed update install restores the current version's host and keeps the download for a retry (`:292`); a host that cannot be drained refuses the install and unlocks admission (`:290`). The Windows uninstaller removes Electron's own data and never the harness home (`:270`). The generator keeps a source's last good result when it cannot be read, and still reports the failure (`apps/plugin-market-generator/README.md:44`). A browser scenario's scaffold owns a temporary home and a loopback port and tears both down (note `2026-07-24`, `:19`).

## 4. Upstream decisions dissected

The first 24 rows are the decision notes handed to this area; paths are under `.agents/notes/`. Verdict words: **keep**, **change**, **drop**; "not built" means the mechanism is gone and the goal is met by what the last column names. Long reasoning is in the ledger rows.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `implemented/architecture/2026-08-25-electron-desktop-packaging-and-updates` | A small Electron shell; one release number for shell, client and host; a reserved profile; whole-release updates | Not built; goal kept | One binary contains host and UI, so they cannot differ; `dsh update` is the one update flow. **Lesson kept:** a release build ignores development overrides (`:96`) | [DH-ADR-WEBAPP-1](#dh-adr-webapp-1) |
| `…/2026-09-08-desktop-bundled-runtime-and-external-plugins` | Ship the package graph in the application; user plugins live in the profile and survive upgrades | Not built; goal kept | Nothing is installed at first start because nothing is separate; plugin directories sit outside the binary and an update never touches the home | [DH-ADR-WEBAPP-1](#dh-adr-webapp-1), [DH-ADR-13](../design.md#dh-adr-13) |
| `…/2026-09-08-desktop-welcome-window-material` | A separate native welcome window owns credential onboarding; a skip is valid for one process | Change | A host-rendered setup page. **Lessons kept:** one owner of onboarding, a skip is never stored, the real product callbacks (`:27-33`). Native blur is dropped | [DH-ADR-WEBAPP-3](#dh-adr-webapp-3) |
| `…/2026-09-10-desktop-web-wrapper` | Desktop runs the shared web application; no second backend | No longer applies | There is one client kind, a browser on the HTTP path, so "no second backend" holds by construction | [DH-ADR-WEBAPP-2](#dh-adr-webapp-2) |
| `…/2026-09-15-desktop-native-fatal-recovery` | One native dialog per process, independent of the failing page | Change | One diagnostic on standard error and a report file (A18, `DH-ADR-HOST-10`); the page shows "host not reachable" from markup it already holds. **Lesson kept:** recovery must not depend on what failed; the first report wins | [DH-ADR-WEBAPP-4](#dh-adr-webapp-4) |
| `…/2026-09-21-desktop-page-close-shortcuts` | The close shortcut closes the focused page and never falls back to something else when its target is stale | No longer applies | The browser owns its close-tab key, and closing a tab stops nothing. **Lesson kept** for UI shortcuts: a stale target is a no-op | [DH-ADR-WEBAPP-4](#dh-adr-webapp-4) |
| `…/2026-09-22-platform-browser-storage` | Per-account storage partitions for embedded vendor pages | Drop | Embedded account pages are dropped; the pages open in the system browser, which keeps their preferences itself | [DH-ADR-18](../design.md#dh-adr-18) |
| `…/2026-09-23-desktop-close-to-background-and-quit-confirmation` | Close hides the window; quit asks what would be interrupted; a Windows tray icon | Change | Closing tabs never stops the host; stopping asks. **Lessons kept:** a two-second inspection deadline counts as "work is running", a second request joins the open question, an operating-system shutdown never waits (`:17`). The tray is dropped | [DH-ADR-WEBAPP-4](#dh-adr-webapp-4) |
| `…/2026-10-08-desktop-shell-configuration` | The shell keeps its own settings file for what must work while the host is down | Merged | One process: the update channel is ordinary configuration and a flag. **Lesson kept:** `dsh update` and the fatal diagnostic must not need a composed tree | [DH-ADR-WEBAPP-4](#dh-adr-webapp-4) |
| `implemented/feature/2026-09-14-desktop-primary-runtime` | Ship Python, Node, pnpm and data libraries; never change `PATH` | Drop | Nothing is bundled ([DH-ADR-18](../design.md#dh-adr-18)); area A14 already dropped the tool that reports it. A stated loss ([§6.5](#65-what-a-user-loses-without-a-desktop-application)) | [DH-ADR-WEBAPP-8](#dh-adr-webapp-8) |
| `…/2026-09-20-desktop-browser-webview` | A retained Electron guest browser with per-workspace storage | Drop | The owner dropped the embedded browser pane | [DH-ADR-18](../design.md#dh-adr-18) |
| `…/2026-10-13-web-install-icons-and-launch-display` | Four install icons, `standalone`, no service worker, a measured maskable safe area | Keep | Static files served unchanged; the safe-area check is kept | [DH-ADR-WEBAPP-5](#dh-adr-webapp-5) |
| `proposed/feature/2026-09-08-desktop-mandatory-update-api` | A vendor endpoint that can block the application until it updates | Drop | Dropped by the owner | [DH-ADR-24](../design.md#dh-adr-24) |
| `…/2026-09-08-desktop-update-extensions` | Deferred ideas: independent desktop revisions, install on next launch, replacing a pending update | Drop | No desktop revision exists and no download is ever pending; full SemVer comparison is kept | [DH-ADR-18](../design.md#dh-adr-18) |
| `…/2026-09-08-desktop-update-policy-and-installation` | Discover automatically, download and restart only with approval, account for running tasks | Change | Discovery is optional and opt-in; the download is a command; an update never stops a host. **Lesson kept:** approval for version A is never turned into installing C (`:27`) | [DH-ADR-WEBAPP-4](#dh-adr-webapp-4) |
| `implemented/process/2026-09-09-parallel-macos-notarization` | Notarise the update archive and disk image in parallel from isolated copies | Not built | One binary, no bundle, no disk image. **Lesson kept:** a step that mutates an artifact works on its own copy | `DH-ADR-ELECTRONBUILDER-2` |
| `…/2026-09-17-windows-runtime-signature-cache` | Cache signed copies of unchanged runtime files | Not applicable | A speed-up for signing thousands of bundled files; the Go release signs two | none |
| `…/2026-09-17-windows-signature-completion` | Sign once with the hardware key, timestamp separately, retry only the timestamp | Keep | Still true for `dsh.exe`: a timestamp error must never cause a second use of the private key | `DH-ADR-ELECTRONBUILDER-2` |
| `implemented/simplification/2026-09-19-remove-desktop-profile-core-cleanup` | Stop pruning `node_modules` in the profile | Not applicable | npm tooling; a Go profile holds configuration only | none |
| `implemented/testing/2026-07-24-web-gui-browser-e2e-lane` | A keyless, deterministic real-browser lane with replayed sessions | Keep, change | The method is kept; it drives the built binary, and most checks move below the browser | [DH-ADR-WEBAPP-6](#dh-adr-webapp-6) |
| `…/2026-09-10-desktop-local-updater-qualification` | Run the real updater against a loopback feed with inert bytes | Change | The same idea for `dsh update`: a private feed and a test key | `DH-ADR-ELECTRONUPDATER-3` |
| `…/2026-09-14-desktop-installed-update-journal` | A whitelisted journal that survives the installer replacing the application | No longer applies | `dsh update` is a foreground command that outlives the swap and prints its own record | `DH-ADR-ELECTRONUPDATER-3` |
| `…/2026-09-14-desktop-installed-update-materials` | Test updates under a random identity so real clients can never take them | Change | A test key does it: a real binary trusts only the production key | `DH-ADR-ELECTRONUPDATER-1` |
| `…/2026-09-27-spreadsheet-selection-by-keyboard-before-reflow` | Repair of one browser test of a spreadsheet widget | Not applicable | Upstream test maintenance. **Lesson kept:** assert that an action changed something, never the initial state | [DH-ADR-WEBAPP-6](#dh-adr-webapp-6) |
| The web entry cannot be served alone; the host injects a boot manifest (`apps/web/vite.config.ts:12-14`) | The shell needs start-up data only the host has | Drop | Pages are rendered by the host with their data in them | [DH-ADR-WEBAPP-1](#dh-adr-webapp-1) |
| An inactive component in the shipped web composition fails every scenario (`apps/web/tests/README.md:19`) | The product only warns; the tests refuse | Keep | Kept as a composition test and as a visible product status | [DH-D-WEBAPP-1](#dh-d-webapp-1) |
| Desktop listens on a random port, web on 3080 (`apps/desktop/README.md:40`) | Avoid a collision between the two | Change | One product, one fixed default port, because the origin carries the cookie, the notification permission and the installed app | `DH-ADR-GOUISTACK-9` |
| The uninstaller never touches the harness home (`apps/desktop/README.md:270`) | Removing the program must not remove the user's work | Keep | Removing `dsh` is deleting one file | `DH-LIB-ELECTRONBUILDER-R7` |
| The publisher needs no build credential and the builder no upload credential (`apps/desktop-publisher/README.md:21`) | Separate duties | Keep the rule | The tool is dropped; the rule is `DH-LIB-COS-R1` | [DH-ADR-WEBAPP-8](#dh-adr-webapp-8) |
| Performance budgets are source constants on a shipped artifact with synthetic input (`benchmarks/AGENTS.md:9-15`) | Budgets must not drift or leak user data | Keep | Restated for the web product | [DH-ADR-WEBAPP-7](#dh-adr-webapp-7) |

## 5. Tech debt and its resolution

One unit was assigned. Thirteen more were found in sections the inventory does not read ("Known limitations", "Limitations", "Limits") and are defined in the fragment as `UP-DEBT-found.A25.1` to `.13`. Per-unit reasons are in the ledger rows.

| Group (count) | Units | Cause | How the Go design answers it |
|---|---|---|---|
| Desktop build tooling (1) | `UP-DEBT-marker.apps/desktop/scripts/desktop-ci-version.mjs:13` | A dated TODO to remove legacy version-mode aliases from the desktop CI script | Not applicable: there is no desktop build, and the release version comes from the tag (`DH-LIB-ELECTRONBUILDER-R8`) |
| Desktop limits that existed because of the shell or npm (3) | `found.A25.1`, `.3`, `.5` (`apps/desktop/README.md:387`, `:390`, `:392`) | Sign-in button of the welcome window; pnpm lifecycle scripts without an approval dialog; an x64 payload on an arm64 host | Not applicable: no welcome window, no pnpm, no payload |
| Two installations sharing one home (1) | `found.A25.4` (`:391`) | Desktop and command line share data but keep separate package trees | Resolved: one binary, and several processes share a home under per-session leases ([DH-ADR-12](../design.md#dh-adr-12)) |
| Release steps that need the production environment (1) | `found.A25.2` (`:389`) | Signing, notarisation, update hosting and installed-update qualification | **Carried in part.** Update qualification no longer needs it (a private feed and a test key). Platform signatures still need real certificates in the protected release environment; no design removes that |
| Limits of tools that are not built (7) | `found.A25.6` to `.12` (`apps/desktop-publisher/README.md:44-45`; `apps/plugin-market-generator/README.md:80-84`) | A private tool tied to the workspace lockfile; an index generator bound by API rate limits and public registries | Not applicable: neither tool is built ([DH-ADR-WEBAPP-8](#dh-adr-webapp-8)) |
| Browser tests split across two compiler programs by convention (1) | `found.A25.13` (`apps/web/tests/README.md:41-51`) | A scenario must be listed in two TypeScript projects and must not import client packages; "nothing mechanically enforces this" | Not applicable: one Go module. The one boundary that remains, who may import the browser driver, is a test (`DH-LIB-PLAYWRIGHT-R6`) |

Totals: 12 not applicable, 1 resolved, 1 carried (a platform limit; stage `DH-S-59`, where the implementation plan documents the known limits).

## 6. Go design

### 6.1 Shape

There is no web application package to build and no `internal/desktop` ([../design.md §4.2](../design.md#42-go-module-layout)). What this area adds sits in the `internal/ui` family and in the two evidence trees.

| Package | Kind | Provides | Injects | Why it exists |
|---|---|---|---|---|
| `internal/ui/assets` (C1's package, used as written in [ui-core.md §6.1](ui-core.md)) | library | The embedded file tree and its manifest | nothing | This area adds no package here. It adds the brand files (web app manifest, icons, favicons), whose names and product name come from C2's `brand.Identity` ([ui-kit.md](ui-kit.md), `DH-D-UIKIT-9`), and the product-level checks of [DH-D-WEBAPP-2](#dh-d-webapp-2) |
| `internal/ui/setup` | component `ui.setup` | `setup.Key` (`Readiness`); three routes; one contribution to `shell.notices` | `web.Key` (A19); `shell.Key` (C1); `secret.Key` (A17); the model listing (A04) | First run: says whether a model can be called and collects one credential |
| `internal/ui/product` | component `ui.product` | `product.Key` (`Info`); notify key `product.Changed`; five routes; one stream source; contributions to `shell.head` and `shell.notices` | `web.Key`, `stream.Key` (A19); `shell.Key` (C1); `listen.Key`, `auth.Key`, `diag.Key` (A18); `update.Status` and the stop function (A24) | Product status, "about", stop from the UI, the update notice, and what is specific to one browser |
| `test/scenario/web` | test support, build tag `scenario` | `web.Start(t, opts)`: a built binary, a temporary home, a replay model, a browser | `DH-LIB-PLAYWRIGHT`; A23's `scenario.Run` and `internal/llm/replay` | The scaffold every browser scenario of every UI area uses |
| `test/bench/web` | tests, build tag `bench` | The budgeted browser workflow | the scaffold | The budgets of [DH-ADR-WEBAPP-7](#dh-adr-webapp-7) |

```go
package setup
type State struct{ Ready, Deferred bool; Usable []string } // Usable: ids of models that can be called now
type Readiness interface {
    State(ctx context.Context) (State, error) // computed on each call; never cached across a config change
    Defer()                                   // "set up later": memory only, ends with this fiber
}

package product
type Status struct {
    Version, Commit, Profile string; Mode listen.Mode // listen.Mode is A18's, used as written
    Update *update.Status                             // A24's last verified result; nil when there is none
    RestartPending, UncleanLastExit, WithoutPlugins, InsecureRemote bool
}
type Info interface{ Status() Status }
```

**How the web application starts.** Each step names its owner; this area owns the order and the result.

1. `dsh web` selects the `web` profile (A24, `DH-D-CLI-2`; A27) after the fatal diagnostic is installed (A18, `DH-D-HOST-7`).
2. Boot composes the layers, loads the tree and audits the **required entries** (A21, `DH-ADR-BOOT-6`). A required entry that is not active unloads the tree and exits with one report. Any other inactive entry is one warning at start-up; in the web product it is also shown in every page's notice area (C1, from A18's inventory) and fails the composition test of [DH-D-WEBAPP-1](#dh-d-webapp-1). The web product's required set is the `Required` list of the `web` profile definition ([boot.md §6.2](boot.md) for the rule, [bundle-preset.md §6.2](bundle-preset.md) for the ids): the spine (`session.store`, `agent.registry`, `agent.driver`, `tools.registry`, `agent.presets`) and the web carrier set (`host.listen`, `host.auth`, `host.assets`, `host.web`, `host.access`, `host.stream`, `ui.shell`). Each must be present, enabled and active, and no layer can remove one.
3. After readiness, A18's `launch` prints the URL, opens the system browser unless disabled, and writes the runtime record (`DH-D-HOST-3`). Stream routes answer 503 until then ([api.md §6.2](api.md)).
4. The browser exchanges the token for its cookie and lands on the root page. While `setup.State` is neither ready nor deferred, that page is the setup page; otherwise it is the session view.
5. The host runs until it is stopped. A second `dsh web` on the same home and address opens a tab on the running host and exits (A24, `DH-ADR-CLI-4`).

**First run.** On a fresh home no model is usable. The setup page offers, in the browser's language: one provider and its API key (and an optional base address), a sign-in with a vendor account where that component is in the profile (it opens in the system browser, [DH-ADR-18](../design.md#dh-adr-18)), and "set up later". Saving stores the key through A17's `secret.Store` under the reference the provider's route names, re-reads `State`, and shows the session view with no restart. Until a model can be called, every other page carries one notice with a link to setup, contributed to C1's `shell.notices`. "Set up later" calls `Defer`: the session view opens with a notice, and the next host process asks again. The page also states, in one sentence with a link to the settings, that nothing leaves the machine until the user turns it on ([DH-ADR-9](../design.md#dh-adr-9)). A user who configured any usable provider from the command line or a config layer never sees the page (kept from `apps/web/tests/onboarding-usable-provider.e2e.ts:1-6`).

**A second device.** A browser on another machine is admitted as the owner through the same token exchange (A19, `DH-ADR-API-6`), and only when the listener has TLS or the operator set the explicit flag (A18, `DH-ADR-HOST-2`). Running `dsh web --no-open` again on the host machine makes the running host print a fresh one-time link on its own terminal to hand over; the second command itself is never given the link, because the control endpoint returns no credential (A18, `DH-ADR-HOST-12`). Every new browser sign-in raises a notice in the pages already open, with a "sign out everything" action. What that user sees and does not see:

| Aspect | On the second device |
|---|---|
| Sessions, transcripts, approvals, jobs, settings | The same, live. Two browsers on one session both follow the stream; an approval answered on one shows as settled on the other (A17) |
| Language, appearance, open panels | Shared: they are host preferences per principal ([ui-core.md §6.2](ui-core.md), [ui-kit.md](ui-kit.md) `DH-ADR-UIKIT-8`), and both browsers are the owner |
| Time zone, notification choice | Its own: they belong to the browser |
| An unsent draft | Shared: the composer draft is a host record per session and principal ([ui-chat.md](ui-chat.md) `DH-ADR-UICHAT-4`) |
| Scroll position | Its own: it is per tab |
| Directory browser, terminals, file downloads | They act on the **host machine**: a terminal is a shell there, a download brings bytes to the device |
| "Open in an application on the host" | Absent: local mode only (A18, `DH-ADR-HOST-9`) |
| Notifications, install, more than four live tabs | Only over TLS; plain HTTP from another machine is not a secure context (`DH-ADR-GOUISTACK-9`) |
| Sign-out | Signs out that browser only; "revoke all" signs out every browser (A18) |

A second *device* is not a second *host*. A second `dsh` process on the same home is a separate host with its own listener; a session it holds shows there as "open elsewhere" ([DH-ADR-12](../design.md#dh-adr-12); `session/writer-held` in [api.md](api.md)).

**The product in the browser over time.** Closing every tab stops nothing. `ui.product` contributes one live element, `#product-status`, to C1's `shell.notices` region and feeds it from the stream: an update notice with the command that installs it and "restart to finish updating" after `dsh update` replaced the binary under a running host (both [DH-D-WEBAPP-10](#dh-d-webapp-10), present when the update check is built); a notice after an unclean exit, naming the report; a start in safe mode (`--safe`: plugin processes and optional bundles are off for this launch, [boot.md](boot.md) `DH-ADR-BOOT-12`), read from `launch.Data.Safe`; and a network listener without TLS. The same element and `/about` also show the listener's `PeerRule()` (whether a process the host started is refused exactly or by best effort) and the sandbox exposure list of the running machine, both as A18 and A10 report them. Three further states are C1's and are used as written ([ui-core.md §6.1](ui-core.md)): its `conn` module shows "host not reachable" from markup already in the page and keeps trying to resume; its notice area lists components that failed to load; and when the host that answers again is a different release, its `state` module shows a reload prompt and never reloads by itself, while its `keep` module holds unsent text across the reload. That prompt is the web form of upstream reloading the page after a host replacement (`apps/desktop/README.md:292`). This area supplies the words those states need (the launch command for this profile) and proves the sequence on the assembled product ([DH-E-WEBAPP-2](#dh-e-webapp-2), [DH-E-WEBAPP-3](#dh-e-webapp-3)).

### 6.2 Contracts

Routes are declared through A19's `web.Routes` as written in [api.md §6.1](api.md); all carry `access.Owner()`.

| Method and pattern (name) | Kind | Rule |
|---|---|---|
| `GET /setup` (`setup.show`) | Page | Shows state; a ready product answers `303` to the root |
| `POST /setup/credential` (`setup.save`) | Action, form `{Provider, Key, BaseURL}` | The key is passed to A17 and never echoed, logged or re-rendered; a store failure re-renders the form (422) and stores nothing |
| `POST /setup/later` (`setup.later`) | Action | Calls `Defer` |
| `GET /about` (`product.about`) | Page | Version, commit, profile, home path, origin, mode, third-party notices, product status |
| `GET /host/stop` (`product.stop.ask`) | Fragment | What stopping would interrupt: the work summary of A18's `host.stop` in its "ask first" mode, the same one `dsh stop` and `dsh status` show ([app-cli.md §10](app-cli.md)). No answer in two seconds is shown as "work is running" |
| `POST /host/stop` (`product.stop`) | Action | Calls A24's one stop function with reason `Requested` (`DH-ADR-CLI-5`); the response is written before unload begins |
| `POST /updates/check` (`product.update.check`) | Action | "Check now": one check through A24's `update.check`, under the same rule as `dsh update --check` (`DH-ADR-CLI-8`). It never downloads |
| `GET /settings/this-browser` (`product.browser`) | Fragment | Notification choice, install hint, "sign out this browser" |

| Other contract | Statement | Replaces |
|---|---|---|
| Stream source `product` (class `State`, element `#product-status`) | Registered with `stream.Hub`; complete on connect, replaced whole on `product.Changed`. The element is a `live` container inside C1's `shell.notices` | Upstream's update indicator in the account row and the desktop update dialogs |
| Event `product.Changed` (notify) | Carries the new `Status`; raised when `update.Status`, the restart-pending fact or the listener's mode changes | none upstream |
| Contributions to `shell.head` | The manifest link, the Apple touch icon link, light and dark favicons; all document-relative | `apps/web/index.html:6-11` |
| `app.webmanifest`, `icon-192.png`, `icon-512.png`, `icon-maskable-512.png`, `apple-touch-icon.png`, two favicons, in C1's `ui/assets` | Served by A18's assets mount without a credential, because a browser fetches a manifest without cookies. `start_url` and `scope` are relative (`../`), so they stay inside a proxy prefix | `apps/web/public/*` |
| `setup.Config` | `Enabled` true. Off for a deployment provisioned by configuration | none (`dshOnboarding` preload, `apps/desktop/README.md:394`) |
| `product.Config` | `StopFromUI` true (live); `UpdateNotice` true (live); `InstallHint` true (live) | Fields of the desktop shell's `settings.json` that still have a subject (`apps/desktop/README.md:302-316`) |
| Per-browser cookie `dsh-notify` (`on` or absent, not `HttpOnly`) | Lets the host render the choice; the permission itself is the browser's | none |

**Persisted data.** None of its own. The credential is A17's; "set up later" is memory; the notification choice is a cookie. **Tools exposed to the model:** none. No assigned `UP-EVT`, `UP-CFG`, `UP-SVC`, `UP-TOOL` or `UP-PERSIST` unit belongs to these five applications.

### 6.3 Ownership, failure, retry, rollback and cleanup

| Resource | Owner (who releases it) | Access (who may use it) | Released when |
|---|---|---|---|
| The embedded file tree (C1's `ui/assets`, with this area's brand files) | The binary; it is immutable | Any source-checked request under `/assets/` | Never; a new build is a new tree |
| The "set up later" flag | The `ui.setup` fiber | Owner requests | Unload, or a credential is saved |
| Routes and the `product` stream source | The fiber that declared them | The owner | That fiber unloads; the routes then answer 404 |
| A browser tab, an installed web app window, the notification permission, the `dsh-notify` cookie | The user and the browser | The host can ask the operating system to open one URL, nothing more | By the user. Removing `dsh` leaves them behind; the user guide says how to remove them |
| A scenario's host process, temporary home, browser process and profile | The scenario | Only that scenario | Test end, also after a failure or a timeout; on failure the artifacts of `DH-LIB-PLAYWRIGHT-R4` are kept first |

| Failure | What happens | Undone, or left uncertain |
|---|---|---|
| A page references a file that is not embedded, or an embedded file has no record | A local test fails; it cannot happen at run time in a built binary | Nothing ships |
| An entry that is not required fails to load | The product starts with one warning (A21); the entry is named in every page's notice area (C1) and in the inventory (A18) | The feature is absent, visibly |
| Saving the credential fails | The form is shown again with the reason; `State` is unchanged | Nothing was stored |
| The saved key is wrong | Setup does not call the provider. The first turn fails with the provider's authentication error and a link back to the settings | **Uncertain** until a turn runs; see [§10](#10-open-questions-and-limitations), item 5 |
| The host stops or dies while a tab is open | "Host not reachable"; on return the stream resumes by cursor and shows every event once ([api.md](api.md)); an interrupted turn is closed by whoever resumes the session ([../design.md §5](../design.md#5-failure-retry-rollback-and-cleanup)) | **Uncertain:** whether a tool call in flight took effect |
| The host returns as a different release | C1's reload prompt; nothing reloads by itself; unsent text is kept by C1's `keep` module in the tab's own storage | **Uncertain:** text typed where that storage is unavailable is lost on reload |
| Stop is requested from the UI and work is running | The confirmation lists it; cancelling changes nothing; a second request joins the open question | Nothing until confirmed |
| The update check fails or a manifest fails verification | No notice; logged (`DH-ADR-ELECTRONUPDATER-2`) | Nothing |
| A scenario runs where no browser exists | Reported as not run; the scenario stays open (`DH-LIB-PLAYWRIGHT-R5`) | Not a pass |

### 6.4 Every desktop duty and its answer

Rows `E1` to `E26` are the rows of [libs/electron.md §2](../libs/electron.md), re-checked here against `apps/desktop/README.md` and `apps/desktop-host/src`. Rows `X1` to `X18` are duties that table does not list. Answers: **Web** (the browser or a host-rendered page), **CLI** (the `dsh` command), **N/A** (the need existed only because of Electron, Node or npm), **Dropped** (not built, by the decision named). "Agree" means this area confirms the library's answer.

| # | Duty (evidence under `apps/desktop/README.md` unless stated) | Answer | Provided by, or the decision |
|---|---|---|---|
| E1 | The main window shows the web UI with a credential the page never holds (`:40-42`) | Web. Agree | A18 `DH-D-HOST-2`, `DH-D-HOST-3` |
| E2 | The host as a child process with private IPC (`:42`; `apps/desktop-host/src/index.ts:24`) | N/A. Agree | One process |
| E3 | A bundled runtime: host packages, pnpm, Node, Python (`:50`, `:110`, `:126`) | Dropped. Agree | [DH-ADR-WEBAPP-8](#dh-adr-webapp-8) |
| E4 | Single instance: a second launch shows the first (`:127`) | CLI. Agree | A18 `DH-D-HOST-3`; A24 `DH-ADR-CLI-4` |
| E5 | The deep link `dsh://open` (`:76`, `:396`) | Dropped. Agree | `DH-ADR-ELECTRON-1` |
| E6 | File associations | None upstream. Agree: no `fileAssociations` and no `open-file` handler found | n/a |
| E7 | Application menu, About, context menus, custom Windows caption (`:46`, `:168`) | Web; the caption is dropped. Agree | C2 for menus; `GET /about` ([DH-D-WEBAPP-5](#dh-d-webapp-5)) |
| E8 | Native folder, message and save dialogs (`:48`, `:80`) | Web. Agree | A18 `DH-D-HOST-8`; HTML dialogs (C2); browser downloads |
| E9 | The Windows tray icon (`:78`) | Dropped. Agree | `DH-ADR-ELECTRON-1`; [DH-ADR-WEBAPP-4](#dh-adr-webapp-4) |
| E10 | A notification, taskbar flash and Dock bounce (`:359`) | Web. Agree | [DH-D-WEBAPP-4](#dh-d-webapp-4); attention stream (A19) |
| E11 | Close hides the window; quit asks (`:76`, `:80`) | Web and CLI. Agree | [DH-D-WEBAPP-5](#dh-d-webapp-5); A24 `DH-D-CLI-4` |
| E12 | Shutdown and log-off skip the question; a check after sleep (`:84`, `:286`) | CLI; the check after sleep is dropped. Agree | A24 `DH-ADR-CLI-5` |
| E13 | The embedded browser pane (`:156`) | Dropped. Agree | [DH-ADR-18](../design.md#dh-adr-18) |
| E14 | Embedded vendor account pages (`:52`) | Dropped. Agree | [DH-ADR-18](../design.md#dh-adr-18) |
| E15 | Update dialogs and a blocking overlay (`:282`, `:353`) | Web: one notice. Agree | [DH-D-WEBAPP-5](#dh-d-webapp-5) |
| E16 | The page-to-native bridge, including a machine description (`:152`) | Dropped. Agree. The machine description is also refused by A18 `DH-ADR-HOST-6` (no device identity) | `DH-ADR-ELECTRON-1` |
| E17 | Dragging a file out of the window (`:154`; `apps/desktop-host/src/file-drag.ts:16-24`) | Dropped. Agree | `DH-ADR-ELECTRON-1`; a download link (A19) |
| E18 | Downloads with a save dialog; copy an image (`:375`) | Web. Agree | Browser download and clipboard |
| E19 | Microphone for the main frame only (`:58`) | Web. Agree | A `Permissions-Policy` header (C1, X4) |
| E20 | Shortcuts taken before the page; a bindings file outside the home (`:94-96`) | Web, reduced. Agree | C1 `DH-ADR-UICORE-6`; overrides stay on the device |
| E21 | Developer tools in packaged builds (`:60`) | N/A. Agree | The browser's own |
| E22 | Theme source, vibrancy, acrylic (`:34`, `:232`) | Web for the theme; the materials are dropped. Agree | C2 |
| E23 | Native fatal recovery with five actions (`:184`) | CLI. Agree. The start without plugins is safe mode (`dsh --safe web`, [boot.md](boot.md) `DH-ADR-BOOT-12`); setting the profile patch aside with its exact bytes is `dsh profile reset <name>` ([boot.md](boot.md) `DH-D-BOOT-3`), which the launch names when that layer is what stops start-up | A18 `DH-D-HOST-7`; A21 `DH-ADR-BOOT-12`, `DH-D-BOOT-3`; A24 `DH-D-CLI-5`; C1 for the tab |
| E24 | Importing the login-shell environment for a GUI launch (`:188`) | N/A. Agree | `dsh` starts from a shell |
| E25 | Links open in the system browser; reveal a file in the file manager; popups denied (`:375`) | Web. **Disagree on one point:** the library says "reveal in file manager" is a host route owned by `open.md`. The finished host spec refuses it: the host opens workspace directories only, never a file (`DH-ADR-HOST-9`), and `open.md` defines no such requirement. This area follows the host spec. What the web UI offers instead is C6's: "Show in Files", copy path, download, and for a local owner "Open in" on the session's workspace directory ([ui-sidebar-workspace.md](ui-sidebar-workspace.md), `DH-ADR-UISIDE-8`) | A18 `DH-D-HOST-9`; C6 `DH-ADR-UISIDE-8`; C4 for links |
| E26 | Installing a `dsh` terminal command from the application (`:66-70`) | N/A. Agree | The product is the command |
| X1 | The welcome window: credential check before the workspace, API-key form, "set up later" (`:222-228`) | Web | [DH-D-WEBAPP-3](#dh-d-webapp-3) |
| X2 | Sign-in through the system browser with a loopback callback; return to Welcome when an account expires (`:56`, `:396-400`) | Web | A17; an expired account with no other usable model shows the setup page |
| X3 | A language cache for dialogs shown before the host is up; shell text in eight languages (`:36`, `:164`) | N/A and Web | Language is resolved per request (C1 `DH-ADR-UICORE-7`: English and Chinese complete, the other six later); terminal diagnostics are A24's |
| X4 | A shell settings file that works while the host is down (`:302`) | N/A | [DH-ADR-WEBAPP-4](#dh-adr-webapp-4) |
| X5 | Update channel selector, Stable or Development (`:316`) | Web and CLI | A setting and a flag (`DH-LIB-ELECTRONUPDATER-R7`) |
| X6 | Update: periodic check, authorised download, verification, separate restart approval (`:286-290`) | CLI and Web | A24 `DH-D-CLI-8`; the notice in [DH-D-WEBAPP-5](#dh-d-webapp-5) |
| X7 | Update admission: lock new requests, drain, re-check tasks, restore the host if the install fails (`:290-292`; `apps/desktop-host/src/update-tasks.ts:23-28`) | N/A | An update never stops or restarts a host (`DH-LIB-ELECTRONUPDATER-R5`) |
| X8 | The mandatory-update policy and its test sign-in window (`:330-359`) | Dropped | [DH-ADR-24](../design.md#dh-adr-24) |
| X9 | Quit inspection: running agents, queued input, jobs and armed schedules, with a two-second deadline (`:80`; `apps/desktop-host/src/quit-inspection.ts:16-23`) | Web and CLI | `GET /host/stop`; A24 `DH-ADR-CLI-5` |
| X10 | Desktop analytics, and client headers that name platform, version, locale and architecture (`:38`, `:54`) | Web, opt-in only | A18 sharing gate (`DH-D-HOST-4`); A04 for request extras |
| X11 | Windows installer and uninstaller; macOS disk image (`:262-270`) | Dropped | `DH-ADR-ELECTRONBUILDER-3` |
| X12 | Signing every executable in the runtime tree; notarisation (`:108`) | CLI release tooling | `DH-ADR-ELECTRONBUILDER-2` (toolchain) |
| X13 | A reserved `desktop` profile that only the application may boot; plugin commands while the application is closed (`:127`, `:142`, `:150`) | N/A | One binary, one set of profiles; processes share a home by lease |
| X14 | Account credentials handed to the shell for the embedded pages (`apps/desktop-host/src/platform-session.ts:5-10`) | Dropped | With E14 |
| X15 | Plugin-manager operation records kept by the shell for the diagnostic report (`apps/desktop-host/src/operation-diagnostics.ts:5-12`) | CLI | The log ring in the crash report and the inventory history (A18) |
| X16 | Console interrupts delivered to the CLI in Electron's Node mode through a native binding (`apps/desktop-host/src/windows-cli-signals.ts:9-17`) | N/A | Go receives console events itself |
| X17 | A random port per run; old cookies purged at each start (`:40`) | Web | A fixed default port; stale cookies expired at the exchange (`DH-LIB-ELECTRON-R6`) |
| X18 | Development commands: reload page, restart application and host, debug ports (`:194-202`) | N/A | Browser reload; restart `dsh` |

Result: 44 duties. 14 are Web, 5 CLI, 4 Web and CLI, 11 not applicable or absent upstream, 10 dropped by a recorded decision. One disagreement with the library table (E25) and two gaps behind one row (E23).

### 6.5 What a user loses without a desktop application

For the owner: the cost of [DH-ADR-18](../design.md#dh-adr-18) in one place. Each item is a consequence of a row above.

| # | Lost | What remains |
|---|---|---|
| 1 | **An always-running presence.** No tray icon, no Dock icon. The host is a terminal process and closing that terminal stops it. Double-clicking `dsh.exe` on Windows does not start the product (open in [app-cli.md §10](app-cli.md)). Nothing starts the host after a reboot, so schedules do not fire until someone runs `dsh` | Tabs can be closed freely. An operator may run `dsh` under the system's own service manager; the product installs none |
| 2 | **A bundled runtime.** No Python, Node, pnpm, numpy, pandas or Office libraries arrive with the product. An agent on a machine without them cannot use them | The programs already on the machine; LibreOffice where the operator installed it ([DH-ADR-20](../design.md#dh-adr-20)) |
| 3 | **An installer.** No Start-menu or Applications entry, no uninstaller, no one-click install; the user unpacks an archive and edits `PATH`. A first run on macOS with no network may be refused (`DH-ADR-ELECTRONBUILDER-2`) | One file; removing it is the uninstall |
| 4 | **A window the product owns.** The UI sits inside browser chrome unless the browser can install it; desktop Firefox cannot. No custom caption or translucent material | The installed web app where offered |
| 5 | **Being told when nothing is open.** Notifications come only from an open browser that was given permission | The tab-title marker; the state is in the page at the next visit |
| 6 | **A way back from other programs.** No `dsh://open` link, no file association (upstream had none) | A bookmark, the installed app, or running `dsh` again |
| 7 | **The embedded browser pane and in-application account pages** | Links and sign-in open in the system browser |
| 8 | **Native file gestures.** No system folder dialog with its bookmarks, no drag out to the desktop, no path from a dropped file (its content is uploaded), no "reveal this file" | The host directory browser, downloads, "open the workspace in an application" for a local owner |
| 9 | **Keys the browser keeps** (close tab, new tab, developer tools) cannot be bound | Every other binding |
| 10 | **In-application update.** No download button, no restart button, no forced update; the check is off until turned on | A notice and `dsh update`, then a restart by hand |
| 11 | **One pinned rendering engine.** The UI must work in every supported browser, and which those are is undecided | Whatever browser the user has |
| 12 | **A private cookie jar.** A general browser sends the loopback cookie to other local servers on the same address (A18, open question 1) | `HttpOnly`, `SameSite=Strict`, signed cookies |
| 13 | **A dialog when start-up fails.** The message is text in a terminal | One diagnostic, a report file, a start without plugins |

What is gained is stated in [DH-ADR-18](../design.md#dh-adr-18): one artifact, no second update path, no bundled engine. Upstream's disk image alone is 214.84 MiB (note `2026-09-09-parallel-macos-notarization.md:27`).

## 7. Decisions

<a id="dh-adr-webapp-1"></a>
### DH-ADR-WEBAPP-1 The web product is compiled into the binary: one artifact, one version, no asset directory

- **Decision.** Every file a page loads is embedded in `dsh` through C1's one `ui/assets` tree with its manifest. There is no separate front-end build, no directory to point the host at, and no boot manifest. A release build has no switch that serves assets or templates from disk; a development build tag may. The tree holds nothing a page does not use, and its total size is under a reviewed constant.
- **Why.** Upstream needs one release number to keep shell, client and host from drifting, and a start-up check for a missing build (`apps/desktop/README.md:124`; `packages/bundle/web-app/README.md:223`). Embedding makes both impossible states unrepresentable.
- **Tradeoff.** Changing a stylesheet means a new binary. The binary grows by the UI's bytes, which are unmeasured; a cap is a reviewed constant. Vendored browser files are invisible to tools that read the Go module graph, so the record feeds the notices file.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-WEBAPP-3`, `DH-T-WEBAPP-4`, [DH-E-WEBAPP-1](#dh-e-webapp-1).

<a id="dh-adr-webapp-2"></a>
### DH-ADR-WEBAPP-2 One table answers every desktop duty, and it is a test

- **Decision.** [§6.4](#64-every-desktop-duty-and-its-answer) is the contract for [DH-ADR-18](../design.md#dh-adr-18). `apps/desktop` is not built. `apps/desktop-host` is replaced by the `dsh` host itself. A duty may be added to the table but never removed; a "dropped" row names a decision and a "web" or "CLI" row names who provides it.
- **Why.** The owner asked that each desktop duty be ported or dropped on record. Eighteen duties were missing from the library table, among them first-run onboarding and the reserved desktop profile, so the library table alone would have lost them.
- **Tradeoff.** Two tables exist (the library's and this one); `DH-T-WEBAPP-14` fails when they diverge. The table records answers, not quality: a web equivalent can be worse than the native feature, which [§6.5](#65-what-a-user-loses-without-a-desktop-application) says plainly.
- **Serves.** [DH-O-16](../design.md#dh-o-16), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-WEBAPP-14`, [DH-E-WEBAPP-2](#dh-e-webapp-2).

<a id="dh-adr-webapp-3"></a>
### DH-ADR-WEBAPP-3 First run is a host-rendered setup page with one owner and no stored "skipped" flag

- **Decision.** As [§6.1](#61-shape). Readiness is computed, not remembered: "is any model usable now". "Set up later" lasts for one host process. The setup page is the only place that asks for a first credential; the models settings page stays for later changes.
- **Why.** Upstream found that two credential dialogs with separate completion flags ask twice, and that a stored skip hides the question from a user who still has no credential (note `2026-09-08-desktop-welcome-window-material.md:27-29`).
- **Tradeoff.** A long-running host asks again only after a restart, so a user who deferred sees a quiet notice in between. Setup does not test the key against the provider ([§10](#10-open-questions-and-limitations), item 5).
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-WEBAPP-5`, `DH-T-WEBAPP-6`, [DH-E-WEBAPP-1](#dh-e-webapp-1).

<a id="dh-adr-webapp-4"></a>
### DH-ADR-WEBAPP-4 The host is a foreground process the user owns; the page reports its state and nothing is installed to keep it running

- **Decision.** `dsh web` runs until a signal, `dsh stop` or the UI's stop action ends it. The product registers no service, no login item and no tray. Stopping from the UI reaches A24's one stop function and shows the same work summary as the terminal. The update channel and every other former shell setting are ordinary configuration. The page carries the states of [§6.1](#61-shape): update notice, restart pending, unclean exit, a start without plugins, and C1's "host not reachable" and reload prompt.
- **Why.** [DH-ADR-18](../design.md#dh-adr-18) removes the shell that kept upstream alive in the background. Installing a service would bring back per-platform install code and a second owner of the process. This answers open question 3 of [libs/electron.md](../libs/electron.md) the same way [app-cli.md §10](app-cli.md) does: a service is documentation, not a command.
- **Tradeoff.** Item 1 of [§6.5](#65-what-a-user-loses-without-a-desktop-application). Under a service manager nobody answers the stop question, so the signal path's bounded unload applies.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-WEBAPP-9` to `DH-T-WEBAPP-11`, [DH-E-WEBAPP-2](#dh-e-webapp-2).

<a id="dh-adr-webapp-5"></a>
### DH-ADR-WEBAPP-5 An installable web app manifest with no service worker; notifications are one browser's choice

- **Decision.** **Recommended default, not confirmed by the owner** (an inference from "port desktop to web"; `DH-ADR-GOUISTACK-9` proposes it). The host serves the manifest and icons of [§6.2](#62-contracts); every page links them. No service worker is registered anywhere. The notification choice lives with the browser that made it.
- **Why.** It is the one browser feature that gives the UI a window and a taskbar entry of its own, and it costs four static files. Upstream decided the same for its web application and rejected a service worker because offline start needs its own cache-invalidation contract (note `2026-10-13`, `:31`).
- **Tradeoff.** Only some browsers install sites. The installed window opens without a launch token: it works while the cookie is valid, and otherwise shows a page that says to run `dsh`. An installed shortcut outlives the binary.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-WEBAPP-7`, `DH-T-WEBAPP-8`, [DH-E-WEBAPP-4](#dh-e-webapp-4).

<a id="dh-adr-webapp-6"></a>
### DH-ADR-WEBAPP-6 Views are proven without a browser; a bounded keyless browser lane drives the built binary

- **Decision.** Upstream's 251 browser scenarios are sorted by what they need, not carried one for one:

  | Upstream family (examples under `apps/web/tests`) | Go evidence |
  |---|---|
  | Rendering of history: Markdown, math, code cards, tool rows (`markdown-*.e2e.ts`, `todo-row.expected.e2e.ts`) | Local golden HTML per view (UI areas); no browser |
  | Composition and catalogue (`shipped-composition.e2e.ts`, `built-boot.expected.e2e.ts`) | Local composition test, `DH-T-WEBAPP-1` |
  | Live turns: tools, cancel, retry, steering, questions, approvals | Composition tests over HTTP and the stream with the replay provider; one browser scenario per family (C3, C4) |
  | Stream recovery: reload mid-stream, connection loss, host restart, proxy prefix (`session-replay-reload`, `connection-recovery`, `server-restart`, `public-mount`) | Real browser: `DH-E-API-1`, `DH-E-HOST-2`, [DH-E-WEBAPP-2](#dh-e-webapp-2) |
  | Scrolling, focus, keyboard, input methods, geometry, phone width (`chat-scroll-contract`, `composer-*`, `schedule-phone`) | Real browser, owned by C2, C3 and C4 |
  | Terminal (`sidebar-terminal`, `pwsh-terminal`) | Real browser: `DH-E-API-3` |
  | First run, install manifest (`onboarding-*`, `default-workspace`, `pwa-manifest`) | [DH-E-WEBAPP-1](#dh-e-webapp-1), [DH-E-WEBAPP-4](#dh-e-webapp-4) |
  | Desktop-marked scenarios, about ten files (`desktop-*.e2e.ts`, `shortcuts-desktop`, `window-drag-*`, `caption-overlays`) | Not carried: no desktop |
  | Client plugins, plugin market, embedded browser, preview runtime (`plugin-hmr-lifecycle`, `plugin-market-*`, `sidebar-browser`, `preview-boot`) | Not carried ([DH-ADR-3](../design.md#dh-adr-3), [-13](../design.md#dh-adr-13), [-18](../design.md#dh-adr-18), [-17](../design.md#dh-adr-17)) |
  | Real model (`smoke-real.e2e.ts`) | The credentialed workflow; skipped means open |

  The lane's rules, kept from upstream: the built binary and the shipped profile, with only the model replaced by replay; a fresh home, browser and profile per scenario; pinned clocks; waiting on state, never on time; failure on any page error, console error, unexpected stream `reset` or unconsumed recording; and an assertion must observe a change the action caused.
- **Why.** HTML is produced on the host, so most of what upstream needed a browser to see is the output of a function. What remains is behaviour only an engine has. Driving the binary instead of an in-process composition removes upstream's stated gap between the scaffold and the shipped command (note `2026-07-24`, `:75`).
- **Tradeoff.** Fewer browser scenarios means less accidental coverage of layout. Golden HTML is more brittle than an accessibility snapshot; whether the driver can read the accessibility tree was not verified. The lane drives Chromium (`DH-ADR-PLAYWRIGHT-1`) and runs every scenario there; current Firefox and Safari run the smaller set of engine-sensitive scenarios through each browser's own driver (`DH-ADR-PLAYWRIGHT-2`), as the owner decided ([DH-ADR-39](../design.md#dh-adr-39)).
- **Serves.** [DH-O-15](../design.md#dh-o-15), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-WEBAPP-12`, [DH-E-WEBAPP-5](#dh-e-webapp-5).

<a id="dh-adr-webapp-7"></a>
### DH-ADR-WEBAPP-7 The web product has named budgets, held as reviewed constants

- **Decision.** The workload is upstream's: a synthetic 240-turn session with 40 tool results and 20 code fences, and a reply of 120 deltas at 16 ms (`benchmarks/long-session-browser/README.md:17`). Three fresh samples; the median is judged. **Inference:** the starting ceilings are upstream's CI ceilings, to be recalibrated on this project's reference runner before they gate; none was measured.

  | Endpoint | Upstream ceiling and evidence | Go starting ceiling |
  |---|---|---|
  | Open the session: transcript shown, composer editable | 1125 ms (`long-session.bench.ts:17-20`, `:132`) | 1125 ms |
  | Slowest "load earlier" page | 875 ms (`:139`) | 875 ms |
  | First trajectory view | 650 ms (`:140`) | 650 ms |
  | Enter to first visible reply; whole reply | 2750 ms; 2500 ms above the 1984 ms the paced replay itself takes (`long-session.bench.ts:16`, `:23`, `:448`; `benchmarks/support/calibration.ts:13-15`) | the same |
  | Typing a draft while the reply streams | 1250 ms (`long-session.bench.ts:16`) | 1250 ms |
  | Longest main-thread stall while chunks arrive every 16 ms | 250 ms (`apps/web/stress-tests/reasoning-chunks.stress.ts:20-21`) | 250 ms |
  | Resuming an unfinished 100,000-delta message | 125 ms and 30 MiB in the client (`benchmarks/active-stream-reconnect/README.md:5`) | 125 ms for the host to render the `live` baseline; response bytes capped |
  | Requests at first page load | Exactly 2 describe calls and 3 credential reads (`apps/web/tests/startup-rpc-budget.e2e.ts:10-12`) | One document, the recorded assets, two streams; no request afterwards without a user action |
  | `dsh web` to a printed URL on a fresh home; embedded UI bytes; first-party script bytes | none | Constants to be set from the first measurement |

  A 1,000-session, 500-turn diagnostic stays outside the gate, as upstream keeps it (`apps/web/tests/complex-history.perf.ts:40-43`; note `2026-07-24`, `:81`). Area C4's `DH-E-UICONV-7` measures the transcript's own costs (element counts, bytes per frame, host CPU) and sets its limits from its first run. This gate is the product-level ceiling above it: a first measurement may lower a constant here, and raising one above upstream's needs a recorded reason.
- **Why.** A page drawn by round trips has different costs from a client application. The same user paths must stay inside the same bounds, or the redesign has made the product slower without anyone deciding so.
- **Tradeoff.** Wall-clock budgets on shared runners need headroom and can flake ([DH-ADR-TOOLCHAIN-14](../upstream/toolchain.md#dh-adr-toolchain-14)). Two frames after a DOM change prove a chance to paint, not a painted frame.
- **Serves.** [DH-O-15](../design.md#dh-o-15), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-WEBAPP-13`, [DH-E-WEBAPP-6](#dh-e-webapp-6).

<a id="dh-adr-webapp-8"></a>
### DH-ADR-WEBAPP-8 The release publisher, the market index generator and the bundled runtime are not built

- **Decision.** `apps/desktop-publisher` is dropped under [DH-ADR-24](../design.md#dh-adr-24) (a): releases are GitHub release assets, and its five durable rules are `DH-LIB-COS-R1` to `-R5`. `apps/plugin-market-generator` is dropped under [DH-ADR-13](../design.md#dh-adr-13): with no download or discovery there is no index to generate. The primary runtime is dropped under [DH-ADR-18](../design.md#dh-adr-18).
- **Why.** Each exists to feed something the owner removed: a vendor's download domain, a plugin market, a desktop payload.
- **Tradeoff.** Item 2 of [§6.5](#65-what-a-user-loses-without-a-desktop-application). If a plugin registry is added later it needs an index and its generator again; the generator's safety rules (read only, bounded sizes, integrity checks, no hooks, `apps/plugin-market-generator/README.md:74`) are the starting point. Three recorded exceptions to [DH-ADR-1](../design.md#dh-adr-1).
- **Serves.** [DH-O-16](../design.md#dh-o-16), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-WEBAPP-14`; the ledger rows link here.

## 8. Deliverables

<a id="dh-d-webapp-1"></a>
### DH-D-WEBAPP-1 `dsh web` starts one assembled product, and a missing part is visible

- **Result and why it matters.** One command yields a signed-in page backed by every part of the shipped profile, or a diagnostic. A part that failed is never silently absent.
- **How it is implemented.** No new mechanism: the start order of [§6.1](#61-shape) is the sum of A24's launcher, A21's boot and A18's launch. This area adds the assertion that the sum holds: a composition test loads the shipped `web` profile through the real loader on a temporary home, reads A18's inventory and fails on any entry that is not active, and a second test fixes the order "readiness, then URL".
- **Components and files.** `internal/ui/product` (tests); the `web` profile (A27); `internal/cli` (A24).
- **Depends on.** `DH-LIB-GOUISTACK`, `DH-LIB-OPEN`, [DH-D-BUNDLE-3](bundle-preset.md#dh-d-bundle-3) (the `web` profile) and [DH-D-BUNDLE-1](bundle-preset.md#dh-d-bundle-1) (its required set), [DH-D-BOOT-5](boot.md#dh-d-boot-5) (the audit and readiness), [DH-D-HOST-3](host-telemetry.md#dh-d-host-3) (launch and reuse), [DH-D-HOST-10](host-telemetry.md#dh-d-host-10) (the inventory), [DH-D-CLI-2](app-cli.md#dh-d-cli-2) (the launcher).
- **Priority.** P0. **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-WEBAPP-1` and `DH-T-WEBAPP-2` pass and [DH-E-WEBAPP-1](#dh-e-webapp-1) has passed.

<a id="dh-d-webapp-2"></a>
### DH-D-WEBAPP-2 The binary carries every file a page loads, nothing a page does not, and stays inside a size budget

- **Result and why it matters.** The UI works with no network and no install step, each third-party file can be traced to a version and a licence, and the binary does not grow unnoticed.
- **How it is implemented.** C1 owns the tree, its manifest and the per-file hash check (`DH-T-UICORE-7`). This area adds what only the assembled product can check: every embedded file is referenced by a registered view, the island table or the web app manifest; every vendored file's manifest entry names a source, a version and a licence, and a font ships with its licence file beside it (as `apps/web/vite.config.ts:33-45`); the notices step reads the manifest; the tree's total bytes are under a constant; and the release build ignores the development override.
- **Components and files.** Tests beside `internal/ui/assets`; the brand files in it.
- **Depends on.** `DH-LIB-GOUISTACK` (`R3`, `R5`), `DH-LIB-NOTICES`, [DH-D-UICORE-4](ui-core.md#dh-d-uicore-4) (the asset tree and a manifest entry that carries source, version and licence), [DH-D-HOST-1](host-telemetry.md#dh-d-host-1) (the assets mount). The files are those of areas C2 to C7.
- **Priority.** P0. **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-WEBAPP-3` and `DH-T-WEBAPP-4` pass and [DH-E-WEBAPP-1](#dh-e-webapp-1) has passed.

<a id="dh-d-webapp-3"></a>
### DH-D-WEBAPP-3 A first-time user is taken from an empty home to a first answer

- **Result and why it matters.** Nobody meets a blank product that cannot answer. This replaces the desktop welcome window.
- **How it is implemented.** `ui.setup` as [§6.1](#61-shape) and [DH-ADR-WEBAPP-3](#dh-adr-webapp-3): a pure readiness function over A04's and A17's data, three routes, a view in the message catalogue's languages. The key field uses `autocomplete="new-password"` (kept from `apps/desktop/README.md:220`).
- **Components and files.** `internal/ui/setup`.
- **Depends on.** [DH-D-WEBAPP-1](#dh-d-webapp-1), [DH-D-HUMAN-7](interaction-credentials.md#dh-d-human-7) (`secret.Store`), [DH-D-LLM-4](llm.md#dh-d-llm-4) (a model listing that says which models can be called now), [DH-D-UICORE-2](ui-core.md#dh-d-uicore-2) (`shell.notices`) and [DH-D-UICORE-7](ui-core.md#dh-d-uicore-7) (the message catalogue), [DH-D-API-1](api.md#dh-d-api-1) (routes), [DH-D-UICORE-1](ui-core.md#dh-d-uicore-1) (the shell's root route, which answers `303` to `/setup` while `setup.State` is neither ready nor deferred; a route has one owner, so the redirect is the root route's own).
- **Priority.** P1 (a key can be supplied with `dsh secret set` before this exists). **Outcome.** [DH-O-12](../design.md#dh-o-12). **Done when.** `DH-T-WEBAPP-5` and `DH-T-WEBAPP-6` pass and [DH-E-WEBAPP-1](#dh-e-webapp-1) has passed.

<a id="dh-d-webapp-4"></a>
### DH-D-WEBAPP-4 The UI can have a window of its own and can ask for attention

- **Result and why it matters.** The nearest equivalents of a desktop window and a desktop notification, with no native code.
- **How it is implemented.** The manifest and four icons as committed files with a recorded source; the "this browser" fragment with the notification choice (the click is the gesture a browser requires before it asks permission) and a test notification. The stream and script are A19's and C1's.
- **Components and files.** `internal/ui/assets` (brand files), `internal/ui/product`.
- **Depends on.** [DH-D-WEBAPP-2](#dh-d-webapp-2), `DH-LIB-GOUISTACK` (`R14`, `R15`), [DH-D-API-4](api.md#dh-d-api-4) (the attention stream), [DH-D-UICORE-3](ui-core.md#dh-d-uicore-3) (the script that shows notifications).
- **Priority.** P1. **Outcome.** [DH-O-12](../design.md#dh-o-12). **Done when.** `DH-T-WEBAPP-7` and `DH-T-WEBAPP-8` pass and [DH-E-WEBAPP-4](#dh-e-webapp-4) has passed.

<a id="dh-d-webapp-5"></a>
### DH-D-WEBAPP-5 The page tells the truth about the host: status, stop, and a host that went away

- **Result and why it matters.** What upstream said with dialogs, a tray and an overlay is said in the page: the user knows what is running, what stopping would interrupt, that the last run ended uncleanly, and that the host came back as another release.
- **How it is implemented.** `ui.product` folds three inputs into `Status`: the unclean-exit fact from A18's `diag`, whether the host started with `--safe`, and the listener's mode; the update facts are added by [DH-D-WEBAPP-10](#dh-d-webapp-10) when it is mounted (an optional injection, so this deliverable does not wait for it). It contributes `#product-status` to `shell.notices`, registers one stream source and declares the routes of [§6.2](#62-contracts). "Host not reachable" and the reload prompt are C1's (`DH-D-UICORE-3`); this area supplies the launch command they show.
- **Components and files.** `internal/ui/product`.
- **Depends on.** [DH-D-WEBAPP-1](#dh-d-webapp-1), `DH-LIB-ELECTRON` (`R3`, `R7`), [DH-D-BOOT-2](boot.md#dh-d-boot-2) (the stop function `launch.App.Stop` and the safe-mode flag), [DH-D-HOST-3](host-telemetry.md#dh-d-host-3) (the work summary of `host.status` and `host.stop`), [DH-D-HOST-7](host-telemetry.md#dh-d-host-7) (`diag`), [DH-D-UICORE-2](ui-core.md#dh-d-uicore-2) and [DH-D-UICORE-3](ui-core.md#dh-d-uicore-3) (`shell.notices`, the connection states and the reload prompt).
- **Priority.** P1. **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-WEBAPP-9`, `DH-T-WEBAPP-10` and `DH-T-WEBAPP-11` pass and [DH-E-WEBAPP-2](#dh-e-webapp-2) and [DH-E-WEBAPP-3](#dh-e-webapp-3) have passed.

<a id="dh-d-webapp-10"></a>
### DH-D-WEBAPP-10 The page says that an update exists and that a restart is pending

- **Result and why it matters.** A user who turned the update check on ([DH-ADR-32](../design.md#dh-adr-32)) sees a found release and the command that installs it, and after `dsh update` sees "restart to finish updating" in every open page. It depends on `dsh update`, which is later work, so it is its own deliverable.
- **How it is implemented.** `ui.product` injects A24's `update.Status` (the last verified result and `RestartPending`) when the `update.check` component is mounted, adds the two rows to `Status`, and offers "check now", which asks the update component for a manifest and never for an archive.
- **Components and files.** `internal/ui/product`.
- **Depends on.** [DH-D-WEBAPP-5](#dh-d-webapp-5), [DH-D-CLI-8](app-cli.md#dh-d-cli-8) (`update.Status` and the restart-pending record), `DH-LIB-ELECTRONUPDATER` (`R2`, `R5`).
- **Priority.** P2. **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-WEBAPP-15` passes.

<a id="dh-d-webapp-6"></a>
### DH-D-WEBAPP-6 A real browser drives the real product without a key

- **Result and why it matters.** Every UI area writes browser scenarios against one scaffold with one set of rules, and a broken stream cannot pass.
- **How it is implemented.** `web.Start` is called inside A23's `scenario.Run`, which records the outcome and reports "skipped" when a need is unmet. It builds nothing: it runs the `dsh` binary under test with a temporary home, `--port 0`, `--no-open` and a layer that mounts A23's replay component in place of a provider, reads the printed URL, and starts a browser with a fresh profile. It installs the tripwires of [DH-ADR-WEBAPP-6](#dh-adr-webapp-6), pins both clocks, and on failure saves the artifacts before cleanup. Helpers seed sessions through the store's own interface, never by writing files.
- **Components and files.** `test/scenario/web`.
- **Depends on.** [DH-D-WEBAPP-1](#dh-d-webapp-1), `DH-LIB-PLAYWRIGHT`, [DH-D-UTIL-8](util-test-support.md#dh-d-util-8) (`scenario.Run`), [DH-D-UTIL-7](util-test-support.md#dh-d-util-7) (the replay component and its "script left over" failure), [DH-D-STORE-7](session-store.md#dh-d-store-7) (seeding and export).
- **Priority.** P0: the scaffold and the one smoke scenario. The scenario families of [DH-ADR-WEBAPP-6](#dh-adr-webapp-6) are written by the UI areas against this scaffold, as parts of their own deliverables and at those deliverables' priorities. **Outcome.** [DH-O-15](../design.md#dh-o-15), [DH-O-12](../design.md#dh-o-12). **Done when.** `DH-T-WEBAPP-12` passes and [DH-E-WEBAPP-5](#dh-e-webapp-5) has passed.

<a id="dh-d-webapp-7"></a>
### DH-D-WEBAPP-7 The web product's speed is a gate

- **Result and why it matters.** A change that makes opening a long session or typing during a reply slower fails a test.
- **How it is implemented.** The workflow of [DH-ADR-WEBAPP-7](#dh-adr-webapp-7) on the scaffold with a deterministic synthetic session; three fresh hosts and browsers; a report with raw samples and medians. Budgets are constants; no environment variable is read.
- **Components and files.** `test/bench/web`.
- **Depends on.** [DH-D-WEBAPP-6](#dh-d-webapp-6), `DH-LIB-PLAYWRIGHT`.
- **Priority.** P1. **Outcome.** [DH-O-15](../design.md#dh-o-15), [DH-O-12](../design.md#dh-o-12). **Done when.** `DH-T-WEBAPP-13` passes and [DH-E-WEBAPP-6](#dh-e-webapp-6) has passed.

<a id="dh-d-webapp-8"></a>
### DH-D-WEBAPP-8 Every desktop duty has a checked answer, and the user guide says what changed

- **Result and why it matters.** [DH-ADR-18](../design.md#dh-adr-18) stays true as the product changes, and a former desktop user can find what replaced each thing.
- **How it is implemented.** The table of [§6.4](#64-every-desktop-duty-and-its-answer) as data in a test. Three pages for the documentation website ([DH-ADR-10](../design.md#dh-adr-10)): install and start, "coming from the desktop application" ([§6.5](#65-what-a-user-loses-without-a-desktop-application)), and running `dsh` under a service manager.
- **Components and files.** `internal/ui/product` (test data); the documentation tree.
- **Depends on.** [DH-D-WEBAPP-5](#dh-d-webapp-5), `DH-LIB-ELECTRON` (`R10`). Runtime prerequisite for the user-guide page: the documentation website's generator and gate ([DH-ADR-10](../design.md#dh-adr-10)), which no deliverable ID carries yet.
- **Priority.** P1. **Outcome.** [DH-O-16](../design.md#dh-o-16), [DH-O-12](../design.md#dh-o-12). **Done when.** `DH-T-WEBAPP-14` passes and [DH-E-WEBAPP-2](#dh-e-webapp-2) has passed.

## 9. Acceptance

### 9.1 Local tests

These run in the Go test process with fakes or an in-process listener. They are not evidence of browser behaviour.

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-WEBAPP-1` | DH-D-WEBAPP-1 | The shipped `web` profile, through the real loader on a temporary home, reaches readiness with every entry active in A18's inventory and no start-up warning; the root page answers after a token exchange. The `headless` and `sdk` profiles register no UI route |
| `DH-T-WEBAPP-2` | DH-D-WEBAPP-1 | With a fake launcher: no URL before readiness; each entry of the web required set, made to fail in turn, gives one report, a non-zero exit and no URL; a failing optional entry starts the product and is named in the rendered notice area |
| `DH-T-WEBAPP-3` | DH-D-WEBAPP-2 | Every embedded file is referenced by a registered view, the island table or the web app manifest, and every such reference exists; the tree's total bytes are under the constant; the release build ignores the disk override |
| `DH-T-WEBAPP-4` | DH-D-WEBAPP-2 | No rendered page names another origin; every vendored manifest entry has a source, a version and a licence; each font has its licence file; the notices input lists every vendored entry |
| `DH-T-WEBAPP-5` | DH-D-WEBAPP-3 | Readiness table: no usable model shows setup; any usable provider does not; saving through a fake credential service flips `State` with no restart; `Defer` holds until the fiber is recreated; a non-owner gets 403 |
| `DH-T-WEBAPP-6` | DH-D-WEBAPP-3 | A store failure re-renders the form and stores nothing; the key never appears in a response body, a log line or an error; `setup.Config.Enabled` false removes the routes |
| `DH-T-WEBAPP-7` | DH-D-WEBAPP-4 | The manifest has the stated fields and relative addresses; icon dimensions read from the PNG headers are 192, 512, 512 and 180; every page links manifest and touch icon; under a path prefix all of them resolve inside it; no page or script registers a service worker |
| `DH-T-WEBAPP-8` | DH-D-WEBAPP-4 | The "this browser" fragment follows the `dsh-notify` cookie; two cookie jars render differently; "sign out this browser" calls `auth.SignOut` for that browser only |
| `DH-T-WEBAPP-9` | DH-D-WEBAPP-5 | Status table: unclean exit, started with `--safe`, a network listener without TLS; without the update component mounted no update row exists. Each renders in `#product-status` and on `/about`, is complete in the stream's baseline, and is replaced whole on `product.Changed` |
| `DH-T-WEBAPP-10` | DH-D-WEBAPP-5 | Stop: nothing running stops at once; running work lists agents, queued input, jobs and armed schedules; a two-second inspection timeout under a fake clock is shown as running; a second request joins; no route of this area can start a download or replace the binary |
| `DH-T-WEBAPP-11` | DH-D-WEBAPP-5 | In the assembled web profile, the "host not reachable" notice and the signed-out page name the real launch word for this profile, in English and Chinese; `/about` shows the same release as C1's `#ui-state` |
| `DH-T-WEBAPP-12` | DH-D-WEBAPP-6 | With a stub binary and a stub browser: the scaffold removes the home and both process trees after success, failure and timeout; a missing browser reports not-run; an unconsumed recording, a console error and an unexpected `reset` each fail the scenario |
| `DH-T-WEBAPP-13` | DH-D-WEBAPP-7 | The synthetic session is byte-identical across runs; a zero budget fails; an environment variable cannot change a budget; the report holds raw samples and the judged median |
| `DH-T-WEBAPP-14` | DH-D-WEBAPP-8 | Every row of [§6.4](#64-every-desktop-duty-and-its-answer) has one of the four answers; a dropped row names a decision; a web or CLI row names a provider; the 26 rows of `libs/electron.md` §2 are all present |
| `DH-T-WEBAPP-15` | DH-D-WEBAPP-10 | With a fake `update.Status`: "update available" names the version and the install command, "restart to finish updating" appears when `RestartPending` is set; both render in `#product-status` and on `/about`, are complete in the stream's baseline and are replaced whole on `product.Changed`; "check now" asks for a manifest and never an archive; with the off-switch set the automatic check shows as off and "check now" still runs ([DH-ADR-27](../design.md#dh-adr-27)) |

### 9.2 Real-environment scenarios

The environment is a real browser driving the built binary. The model is the replay provider unless stated, so no key is needed.

<a id="dh-e-webapp-1"></a>
#### DH-E-WEBAPP-1 A first-time user goes from an empty home to a first answer

- **Proves.** DH-D-WEBAPP-1, DH-D-WEBAPP-2, DH-D-WEBAPP-3.
- **Given.** The built binary on Windows, macOS and Linux; an empty temporary home; no model credential in the environment; the network disconnected except loopback; a Chromium with a fresh profile as the default browser; a local stand-in provider that accepts one key.
- **When.** (1) Run `dsh web`. (2) Choose "set up later", then stop and start the host. (3) Enter the key and save. (4) Send a message. (5) Stop and start the host and reload.
- **Then.** (1) One tab opens on the setup page, in the browser's language, with no token in the address; the browser's network log shows no request to another origin. (2) The session view opens with a notice; after the restart the setup page is back. (3) The session view appears with no restart; the key is in no response and no log. (4) The answer streams. (5) No setup page; the session is there.
- **Cleanup.** Stop the host and the stand-in, delete the home and the browser profile. If the key appears anywhere, keep the log and the network record.

<a id="dh-e-webapp-2"></a>
#### DH-E-WEBAPP-2 Tabs close and the host stays; stopping asks; a host that went away is visible and recovers

- **Proves.** DH-D-WEBAPP-5, DH-D-WEBAPP-8.
- **Given.** The built binary on each operating system; a temporary home; a session whose replayed turn runs a long tool call; a real browser.
- **When.** (1) Start the turn and close every tab. (2) Run `dsh web` again. (3) Choose stop in the UI while the turn runs, then cancel. (4) Kill the host process from the operating system. (5) Start it again. (6) Stop from the UI with nothing running.
- **Then.** (1) The host process is still running and the session log keeps growing. (2) A tab opens on the same host showing the turn; no second listener exists. (3) The confirmation names the running agent, the same summary `dsh status` prints; after cancel the turn continues. (4) The tab shows "host not reachable" with the start command, without a reload. (5) The tab resumes by itself: every event appears once and in order, the interrupted turn is shown as closed, and the unclean-exit notice names the crash report. (6) The host exits zero with no question; no process remains.
- **Cleanup.** Confirm no `dsh` process remains, delete the home. If a duplicate or missing event was shown, keep the session export and the browser's network log.

<a id="dh-e-webapp-3"></a>
#### DH-E-WEBAPP-3 A second device follows the same session, and a host replaced by another release makes old pages offer a reload

- **Proves.** [DH-D-WEBAPP-5](#dh-d-webapp-5).
- **Given.** Two machines on one network; the built binary of version N with a TLS certificate the second machine trusts; a second build of version N+1 placed beside it; a browser on each machine.
- **When.** (1) Start the host with TLS in a terminal; open it on the first machine; run `dsh web --no-open` again and open on the second machine the link the running host printed on its own terminal. (2) Send a message on the first. (3) Type an unsent draft on the second. (4) Stop the host, put the N+1 binary in place of the N binary by hand, and start it again.
- **Then.** (1) Both show the session list; the second command printed where to look and no link; the first page shows the sign-in notice; the second has no "open in" action and the owner's language. (2) Both show the answer streaming. (3) The first shows the same draft. (4) Both pages show "host not reachable", then the reload prompt, and neither reloads by itself; after choosing reload each shows a page from N+1, and the draft is still in the composer on the second.
- **Cleanup.** Stop the host, restore the N binary, remove the certificate from the trust store, delete the home.

<a id="dh-e-webapp-4"></a>
#### DH-E-WEBAPP-4 The installed web app opens in its own window and a hidden UI asks for attention

- **Proves.** DH-D-WEBAPP-4.
- **Given.** The built binary; a Chromium-family browser on each operating system and Safari on macOS, with fresh profiles; a session whose replayed turn stops at an approval.
- **When.** (1) Install the UI from the browser's own install action and open it. (2) Decode the maskable icon in the browser and measure its ink radius. (3) Turn notifications on in "this browser", grant permission, hide the window and run the turn. (4) Select the notification. (5) Stop the host and open the installed window.
- **Then.** (1) A window without browser chrome shows the same UI under the same cookie; no service worker is registered. (2) The artwork lies inside the platform's safe zone (kept from `apps/web/tests/pwa-manifest.e2e.ts`). (3) One notification names the session and the waiting approval and shows no transcript text. (4) The window comes forward on that session. (5) The window shows the browser's connection error, not stale content.
- **Cleanup.** Remove the installed app and the notification permission, stop the host, delete the home and profiles. Scope: proves the tested browsers and versions.

<a id="dh-e-webapp-5"></a>
#### DH-E-WEBAPP-5 The keyless browser lane runs on a clean runner and fails when it should

- **Proves.** DH-D-WEBAPP-6.
- **Given.** A clean Linux runner with Go and Chromium and no Node, no model key and no network beyond loopback; the built binary; the smoke scenario (one live turn with a tool call, a reload mid-stream, a cancel); three control builds: one that drops a stream event, one whose page logs a script error, one whose recording has an unused entry.
- **When.** Run the lane against the real build, then once against each control, then on a runner without a browser.
- **Then.** The real build passes with no request leaving the machine and no process or directory left behind. Each control fails, and its artifact directory holds a screenshot, the page's HTML, the console log and the host log. The run without a browser reports the scenarios as not run.
- **Cleanup.** Remove artifacts after review. If a control passed, keep its artifacts: the tripwire is broken.

<a id="dh-e-webapp-6"></a>
#### DH-E-WEBAPP-6 A long session stays inside the budgets on the reference runner

- **Proves.** DH-D-WEBAPP-7.
- **Given.** The reference runner named in the benchmark's source; the built binary; the synthetic 240-turn session; Chromium; no other load.
- **When.** Three times, each with a fresh host, home and browser: open the session, load every older page, open the trajectory view, return, send a message whose reply is 120 deltas at 16 ms, and type a draft while it streams.
- **Then.** Each median is under its constant in [DH-ADR-WEBAPP-7](#dh-adr-webapp-7); the typing overlapped the reply; the page ends after the 241st turn; the report lists every raw sample. The first run on this project's runner records the calibration; until then the scenario is open.
- **Cleanup.** Delete the homes. Keep the report as the calibration record.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Supported browsers: decided by the owner ([DH-ADR-39](../design.md#dh-adr-39)).** Chromium-family browsers are fully supported: every scenario of this document and the keyless browser lane of [DH-ADR-WEBAPP-6](#dh-adr-webapp-6) run in Chrome or Edge, and the budgets of [DH-ADR-WEBAPP-7](#dh-adr-webapp-7) are measured there. Current Firefox and Safari are supported with a smaller automated set: the scenarios marked engine-sensitive, which here are [DH-E-WEBAPP-4](#dh-e-webapp-4) (the installable window and notifications) and the first-run page. The floor is a browser release from 2024 or later; an older browser sees a banner that names the minimum (the page shell's, area C1). A manifest that a browser does not install is not a failure: the UI stays a tab.
2. **Recommended defaults, not confirmed by the owner.** (a) The installable web app manifest ([DH-ADR-WEBAPP-5](#dh-adr-webapp-5)) is specified, without a service worker. (b) The product installs no per-user service ([§6.5](#65-what-a-user-loses-without-a-desktop-application), item 1); it only documents how to run one.
3. **Clauses this document relies on, as their owners now write them.** An entry of the asset manifest carries source, version and licence, and the launch word in the connection notices comes from this area's contribution ([ui-core.md](ui-core.md) §6.1 and §6.3); the work summary of `host.status` is callable in process (A18); the shell's root route answers `303` to `/setup` while `setup.State` is neither ready nor deferred (C1).
4. **Not verified, because it needs a run.** The size of the embedded UI and of the binary; every number in [DH-ADR-WEBAPP-7](#dh-adr-webapp-7) for a server-rendered page; that a relative `start_url` in a manifest under `/assets/` installs correctly in each browser; that the driver can read the accessibility tree.
5. **A wrong key is found late.** Setup stores a key without calling the provider. A verify step would be a model request made before the user sent anything. A04's `llm.Runtime.Models` never waits for the network, and its only key probe is the user-started route check, which is a real request; setup offers that check as a button and never runs it by itself.
6. **Limits kept.** No offline page. No notification with every tab closed. No engine other than Chromium in the keyless lane. The budgets are borrowed until calibrated. Six of upstream's eight UI languages come later (C1).

## 11. Units dispositioned by this spec

6 assigned units, all with a row in `trace/fragments/A25.jsonl`: 5 `UP-PKG` (1 merged, 1 replaced, 3 dropped) and 1 `UP-DEBT` (not applicable). The same fragment defines and dispositions 13 found `UP-DEBT` units (11 not applicable, 1 resolved, 1 carried), holds final verdicts for the 24 `UP-DEC` notes (3 ported, 12 replaced, 1 merged, 5 dropped, 3 not applicable), and declares 8 decisions (`DH-ADR-WEBAPP-1` to `-8`), 9 deliverables (`DH-D-WEBAPP-1` to `-8` and `-10`), 15 local tests (`DH-T-WEBAPP-1` to `-15`) and 6 scenarios (`DH-E-WEBAPP-1` to `-6`).
