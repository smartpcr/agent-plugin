# electron (desktop shell runtime): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Nothing here was built, run or tested.
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The owner decided
> that **there is no desktop application**: the product is the `dsh` binary plus the web
> UI in the user's own browser ("only use web ui, port desktop to web"). The embedded
> browser pane and the embedded sign-in pages are dropped. This document was first
> written for a Go shell around the system webview; it now records, feature by feature,
> what replaces upstream's desktop shell.

| | |
|---|---|
| **Library ID** | `DH-LIB-ELECTRON` |
| **Upstream unit** | `UP-DEP-electron` |
| **Upstream version** | 44.7.0 (`apps/desktop/package.json:76`) |
| **Kind** | Build-time dependency that becomes the shipped runtime: the packaged application *is* Electron |
| **Used by** | `apps/desktop` only |
| **Go choice** | **Own code, no shell.** No desktop shell is built and no webview module is linked. The `dsh` command starts the host and opens the web UI in the system browser. Every feature of upstream's shell has a web equivalent, a command-line equivalent or a recorded drop in [section 2](#2-how-upstream-uses-it), under [DH-ADR-ELECTRON-1](#dh-adr-electron-1) and [DH-ADR-ELECTRON-2](#dh-adr-electron-2) |
| **Related areas** | [go-ui-stack.md](go-ui-stack.md) (how the browser reaches the UI, the installable web app, browser notifications; the shell that was considered and not used), [open.md](open.md) (handing a URL to the system browser), [electron-builder.md](electron-builder.md) (signing and release of the binary), [electron-updater.md](electron-updater.md) (update), [../upstream/security.md](../upstream/security.md) (browser authentication). Owning areas: web application (A25), command line (A24), web host (A18) |

## 1. What it is and why upstream needs it

Electron is a runtime that packages a complete Chromium browser engine and a Node.js
runtime into one desktop application. An Electron application has a **main process**
(Node code with access to native windows, menus and dialogs) and one **renderer** per
window (a Chromium page). Upstream uses it to turn the `dsh` web application into an
installable desktop product.

Upstream's shell is deliberately thin: "an Electron shell around the complete dsh Web
application" (`apps/desktop/README.md:40`). The same web client and the same host run
inside it. Electron is nevertheless used for three separate jobs:

1. **A window with native trimmings.** Menus, dialogs, a tray icon, single-instance
   behaviour, a deep link, notifications, a close-to-background rule.
2. **A JavaScript runtime for the host.** The host runs as a child process of Electron
   in "run as Node" mode, so the application needs no system Node
   (`apps/desktop/README.md:125`). Electron also carries pnpm and a Python distribution
   in its signed resources (`apps/desktop/README.md:50`, `:110`).
3. **A second browser inside the window.** A browser pane for the agent's web work and
   embedded account pages are separate Chromium guests with their own storage
   (`apps/desktop/README.md:156`, `:52`).

In the Go design, job 2 disappears because the host is a compiled binary. Job 3 is
dropped by the owner. Job 1 is the subject of this document: the "window" is now a tab
(or an installed web app window) of the user's own browser, and each native trimming
either moves into the page, moves to the `dsh` command line, or is dropped.

Two terms used below. The **host** is the running `dsh` process. The **launch token** is
the one-time secret in the URL that `dsh` opens; the host exchanges it for a signed
cookie ([DH-ADR-WIRE-10](../upstream/wire-and-api.md#dh-adr-wire-10)).

## 2. How upstream uses it

Imports of `electron` are in 35 source files under `apps/desktop/src` and six scripts
under `apps/desktop/scripts` (found by searching for `from 'electron'`,
`require('electron')` and dynamic imports; tests and fixtures excluded). The table groups
them by feature and gives each one its answer. `apps/desktop-host` has no README at the
pinned commit; its role is taken from `docs/architecture.md:53`.

The answer column uses four words. **Web**: the browser or the host-rendered page does
it. **Command line**: the `dsh` command does it. **Not applicable**: the need existed
only because of Electron or Node. **Dropped**: deliberately not built, by the decision
named. Command names in this table (`dsh stop`, `--safe`, `--no-open`) are proposals;
the command-line area (A24) fixes them.

| # | Feature | Upstream evidence | Electron API | Answer in the Go design |
|---|---|---|---|---|
| 1 | Main window loads the web UI from a private scheme and forwards requests to the host with a credential the page never sees | `apps/desktop/README.md:40`, `:42`; `apps/desktop/src/main.ts:189`, `:797` | `protocol.registerSchemesAsPrivileged`, `protocol.handle`, `BrowserWindow` | **Web.** `dsh` opens the host's loopback URL in the system browser, which authenticates with the launch token ([go-ui-stack.md](go-ui-stack.md#dh-adr-gouistack-9), [open.md](open.md)). No private scheme, no forwarding |
| 2 | Host as a child process, private IPC for readiness, credential, shutdown | `apps/desktop/README.md:42`; `docs/architecture.md:53` | `ELECTRON_RUN_AS_NODE`, Node IPC, `ipcMain` | **Not applicable.** One process: the host is the `dsh` binary |
| 3 | Runtime carried in signed resources: host packages, pnpm, Python, Node | `docs/architecture.md:51`; `apps/desktop/README.md:50`, `:110`, `:126` | `process.resourcesPath`, ASAR archive | **Dropped.** The binary is the host and nothing is bundled beside it. Agent work uses the programs already on the machine |
| 4 | Single instance: a second launch focuses the first; the lock is taken before any profile access | `apps/desktop/src/single-instance.ts:20`; `apps/desktop/src/main.ts:1604`; `apps/desktop/README.md:127` | `app.requestSingleInstanceLock`, `second-instance` | **Command line.** A second `dsh` for the same harness home and listener finds the running host, opens the browser at it and exits (`R1`) |
| 5 | Deep link `dsh://open` shows the window; carries no credential | `apps/desktop/README.md:396`; `apps/desktop/src/main.ts:1488`, `:1489`; `apps/desktop/scripts/electron-builder-config.mjs:111` | `app.setAsDefaultProtocolClient`, `open-url` | **Dropped.** A custom URL scheme must be registered with the operating system by an installer, and there is none. Its one job, "show the window", is done by running `dsh` again (row 4), by a browser bookmark, or by the installed web app |
| 6 | File associations | None. No `fileAssociations` in the builder configuration and no `open-file` handler (searched `apps/desktop/src` and `apps/desktop/scripts`) | n/a | **Not required** |
| 7 | Application menu, About panel, context menus, localised in eight languages; on Windows a custom 40-pixel caption with native window buttons and popup menus | `apps/desktop/README.md:46`, `:164`, `:168`, `:170`; `apps/desktop/src/main.ts:1160`, `:1235`, `:274` | `Menu`, `app.setAboutPanelOptions`, `titleBarOverlay` | **Web.** Menus are host-rendered HTML in the page, in the UI language. "About" is a page, and `dsh --version` on the command line. The custom caption is dropped: the browser draws the window frame |
| 8 | Native dialogs: folder picker, message boxes (quit, fatal recovery, first-close notice), save dialog | `apps/desktop/README.md:48`, `:80`; `apps/desktop/src/directory-picker.ts:3` | `dialog` | **Web.** A message box is an HTML `<dialog>`. Saving is a browser download. A folder is chosen in a host-rendered directory picker or typed as a path (`R8`), because a web page cannot learn an absolute path from the browser's own picker |
| 9 | Tray icon on Windows for the whole run: click shows the window; menu has Open and Quit | `apps/desktop/README.md:78`; `apps/desktop/src/tray.ts:23` | `Tray`, `nativeImage` | **Dropped.** "Open" is row 4. "Quit" is `Ctrl-C` in the terminal that runs `dsh`, a stop action in the UI, or `dsh stop` (`R3`) |
| 10 | One notification (a mandatory update is ready), taskbar flash, Dock bounce | `apps/desktop/README.md:359`; `apps/desktop/src/update-attention.ts:38`, `:43` | `Notification`, `flashFrame`, `app.dock` | **Web.** Browser notifications for "an approval is waiting" and "a turn finished" ([go-ui-stack.md](go-ui-stack.md#dh-adr-gouistack-9)); a marker in the tab title; optionally the terminal bell. The mandatory-update notification is dropped with the service ([electron-updater.md](electron-updater.md#dh-adr-electronupdater-4)) |
| 11 | Closing the window hides it; work continues; quitting asks what would be interrupted | `apps/desktop/README.md:76`, `:80` | `close` event, `before-quit`, message box | **Web and command line.** Closing a tab never stops anything: the host is a separate process. Stopping the host asks first when work would be interrupted (`R3`) |
| 12 | Operating-system shutdown or log-off skips the quit question; resume from sleep triggers an update check | `apps/desktop/README.md:84`; `apps/desktop/src/main.ts:1151`, `:1342`, `:1500` | `session-end`, `powerMonitor` | **Command line.** A termination signal or console-close event never waits for an answer: the host unloads with a bounded grace period (`R3`). The check on resume is dropped; the optional update check runs on its own timer |
| 13 | **Browser pane**: navigable guests with per-workspace storage, no access to the host, permissions denied, downloads blocked | `apps/desktop/README.md:156`; `apps/desktop/src/browser-guests.ts:36`, `:72`, `:142`, `:147`; `apps/desktop/src/main.ts:294` | `<webview>`, `session.fromPartition`, `webRequest` | **Dropped** by the owner ([DH-ADR-ELECTRON-2](#dh-adr-electron-2)). A link opens in a new tab of the system browser |
| 14 | Embedded vendor account pages in an in-window view with per-account storage and a token injected before page scripts | `apps/desktop/README.md:52`; `apps/desktop/src/platform-view.ts:4` | `WebContentsView`, preload | **Dropped** by the owner ([DH-ADR-ELECTRON-2](#dh-adr-electron-2)). Sign-in opens in the system browser |
| 15 | Update dialogs and the mandatory-update overlay as isolated views served without the host | `apps/desktop/README.md:158`, `:282`, `:353` | `WebContentsView`, private scheme | **Web.** One notice in the UI when the optional check finds a release. No dialog flow and no overlay ([electron-updater.md](electron-updater.md)) |
| 16 | Page-to-native bridge: boot readiness, fatal report, folder selection, paths of dropped files, device description | `apps/desktop/README.md:152`; `apps/desktop/src/preload-app.ts:6`, `:111` | `contextBridge`, `ipcRenderer`, `webUtils.getPathForFile` | **Dropped.** There is no bridge (`R8`). Readiness is the page load; a fatal report is row 23; folder selection is row 8; a dropped file is uploaded as content, because a browser gives a page the file's bytes and not its path |
| 17 | Dragging a file out of the window to the desktop | `apps/desktop/README.md:154`; `apps/desktop/src/file-drag.ts:136` | `webContents.startDrag`, `app.getFileIcon` | **Dropped.** The UI offers a download link |
| 18 | Downloads through the browser engine with a native save dialog; image copy to clipboard | `apps/desktop/README.md:375`; `apps/desktop/src/file-actions.ts:46` | `will-download`, `clipboard` | **Web.** An ordinary browser download, and the browser's clipboard interface after a user gesture |
| 19 | Microphone only for the main frame; macOS usage text and entitlement | `apps/desktop/README.md:58`; `apps/desktop/src/microphone-permissions.ts:17`; `apps/desktop/scripts/electron-builder-config.mjs:172` | `setPermissionRequestHandler`, `systemPreferences` | **Web.** The browser asks the user for the origin. A `Permissions-Policy` response header allows the microphone to the main document only. No usage text or entitlement, because there is no application bundle |
| 20 | Shortcuts intercepted before the page, including inside guests; user-editable bindings file | `apps/desktop/README.md:94`, `:96`; `apps/desktop/src/keyboard.ts:2` | `before-input-event`, menu accelerators | **Web, reduced.** Shortcuts are dispatched in the page from a server-rendered table ([go-ui-stack.md](go-ui-stack.md#dh-adr-gouistack-8)); bindings are a setting. Shortcuts the browser reserves for itself (new tab, close tab) cannot be taken |
| 21 | Developer tools toggle in packaged builds | `apps/desktop/README.md:60` | `webContents.openDevTools` | **Not applicable.** The browser has its own |
| 22 | Theme source, macOS vibrancy, Windows acrylic, caption colours | `apps/desktop/README.md:34`, `:232`; `apps/desktop/src/main.ts:317`, `:333` | `nativeTheme`, `setVibrancy` | **Web** for the theme (CSS, with "follow the system" as a media query). Vibrancy, acrylic and caption colours are dropped |
| 23 | Fatal recovery: one native dialog independent of the page, a crash-report file, pruning; renderer-crash detection | `apps/desktop/README.md:184`; `apps/desktop/src/crash-report.ts:1`; `apps/desktop/src/main.ts:1362`, `:383` | message box, `render-process-gone` | **Command line.** A start-up or host failure prints one diagnostic that names a crash-report file and exits non-zero; the next start offers a safe mode (`R7`). A crashed tab is the browser's concern: reloading it restores the page from host state |
| 24 | Importing the login-shell environment for a GUI launch on macOS and Linux | `apps/desktop/README.md:188` | none (child process) | **Not applicable.** `dsh` is started from a shell and inherits its environment. An operator who starts it from a service manager sets the environment there |
| 25 | Opening links in the system browser; revealing a file in the file manager; popups denied | `apps/desktop/src/main.ts:298`, `:117` | `shell.openExternal`, `shell.showItemInFolder`, `setWindowOpenHandler` | **Web** for links; **dropped** for reveal. A link opens in a new tab with no reference back to the UI (`R6`). "Reveal in file manager" is not built: the host opens workspace directories only and refuses any file target ([../areas/host-telemetry.md](../areas/host-telemetry.md), `DH-ADR-HOST-9`), and a download link takes its place ([../areas/app-web.md](../areas/app-web.md)) |
| 26 | Installing a `dsh` terminal command from the application | `apps/desktop/README.md:66`, `:68` | dialogs only | **Not applicable.** The product is the command. Installing it is putting the binary on `PATH` ([electron-builder.md](electron-builder.md)) |

Installers, signing and update were also the desktop application's work. They are
answered in [electron-builder.md](electron-builder.md) (one signed `dsh` binary per
platform in a release archive; no installer) and [electron-updater.md](electron-updater.md)
(`dsh update`, and an optional update check that only notifies).

**Summary of the mapping.**

| Answer | Rows |
|---|---|
| Web | 1, 7, 8, 10, 15, 18, 19, 20, 22, 25 |
| Command line (11 is both) | 4, 11, 12, 23 |
| Not applicable or not required | 2, 6, 21, 24, 26 |
| Dropped by a recorded decision | 3, 5, 9, 16, 17 ([DH-ADR-ELECTRON-1](#dh-adr-electron-1)); 13, 14 ([DH-ADR-ELECTRON-2](#dh-adr-electron-2)) |

## 3. Tech debt upstream carries

Search: `patches/` for `electron`, the comments beside `electron` in
`pnpm-workspace.yaml`, the "Known limitations" section of `apps/desktop/README.md`, and
the importing code.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-pin.minimumReleaseAgeExclude.electron@44.7.0` | Electron 44.7.0 is exempt from pnpm's release-age waiting period because a native addon recognises only this Electron's Node and V8 fingerprint | `pnpm-workspace.yaml:108`, `:109` | **Not applicable.** No Electron, no pnpm release-age policy, and no addon keyed to a runtime fingerprint |
| (context, owned by the desktop area) | A whole browser engine and a Node runtime ship in every release; a host upgrade is a desktop release | `apps/desktop/README.md:124`, `:130` | Removed: no engine and no shell ship. The browser is the user's and is updated by its vendor |
| (context) | Host and plugins run inside one Electron Node-mode process with the application's authority | `apps/desktop/README.md:150` | Plugins are separate processes ([DH-ADR-4](../design.md#dh-adr-4)) |
| (context) | Old cookies must be purged at every start because cookies ignore ports | `apps/desktop/README.md:40` | Still relevant, and sharper in a general-purpose browser: see `DH-LIB-ELECTRON-R6` and risk 2 |

The remaining limitations in `apps/desktop/README.md:387` to `:392` are inventory units of
the desktop area.

## 4. Requirements for the Go implementation

The numbering of the first version is kept so that references stay stable. Three
requirements existed only for a native shell and are retired, not renumbered.

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-ELECTRON-R1` | Running `dsh` a second time for the same harness home and listener address does not start a second listener. It finds the running host, opens the browser at it with a fresh launch token, and exits. A record left by a host that no longer runs is ignored and replaced | rows 4, 5, 9 |
| `DH-LIB-ELECTRON-R2` | The browser shows the host-rendered UI with live updates and holds no credential that page script can read. The path is the loopback listener with the launch-token exchange | row 1; [DH-ADR-WIRE-10](../upstream/wire-and-api.md#dh-adr-wire-10) |
| `DH-LIB-ELECTRON-R3` | Closing every tab stops nothing. Stopping the host from a terminal or from the UI asks first when agents, queued input, jobs or schedules would be interrupted; if the question about running work is not answered by the host's own services in two seconds, work is treated as running. A termination signal, a closed console or an operating-system shutdown never waits on a question: the host unloads within a bounded grace period | rows 9, 11, 12 |
| `DH-LIB-ELECTRON-R4` | *Retired.* It required a tray icon on Windows; the tray is dropped by [DH-ADR-ELECTRON-1](#dh-adr-electron-1) | row 9 |
| `DH-LIB-ELECTRON-R5` | *Retired.* It required native menus and dialogs to follow the UI language. Menus and dialogs are now part of the page and follow `DH-LIB-GOUISTACK-R11` in [go-ui-stack.md](go-ui-stack.md) | row 7 |
| `DH-LIB-ELECTRON-R6` | The UI never navigates its own tab to external content and never frames it. An external link opens in a new browsing context that has no reference to the UI (`rel="noopener noreferrer"`). The owner's cookie is `SameSite=Strict` and `HttpOnly`, and cookies left by an earlier host on another port are expired at the token exchange | rows 13, 25; `apps/desktop/src/browser-guests.ts:163`; `apps/desktop/README.md:40` |
| `DH-LIB-ELECTRON-R7` | A start-up or host failure produces one diagnostic on standard error that names a crash-report file, and a non-zero exit. The next start after an unclean exit says so and offers a start with plugin processes disabled. A browser tab that loses the host shows a "host not reachable" state from markup already in the page, with the command that restarts it | row 23 |
| `DH-LIB-ELECTRON-R8` | No page-to-native bridge exists. A folder is chosen in a host-rendered directory picker or typed as a path, and the host validates it; the picker is served only to the owner. A dropped or pasted file is uploaded as content | rows 8, 16; `apps/desktop/README.md:152` |
| `DH-LIB-ELECTRON-R9` | *Retired.* It required reading the login-shell environment for a GUI launch; `dsh` is started from a shell (row 24) | row 24 |
| `DH-LIB-ELECTRON-R10` | Every Electron feature in section 2 has a recorded answer: web, command line, not applicable or dropped | [DH-ADR-1](../design.md#dh-adr-1) |

## 5. Go choice

**Decision.** No desktop shell. The replacement for Electron is the user's browser,
reached through the `dsh` command, plus a small amount of own code for the things a
shell used to do: reuse of a running host, the stop rule, the failure diagnostic and the
directory picker.

**Why.** The owner decided it: "only use web ui, port desktop to web". Inference from
the table in section 2: of twenty-six features, ten move into the page, four move to
the command line, five were only needed because of Electron or were never used, and
seven are dropped, two of them by the owner's decision on the browser pane.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **No shell: `dsh` opens the system browser** (chosen by the owner) | No module, no cgo, no installer, no second artifact. The core binary stays pure Go (owner decision OD-9). One client to test | No tray, deep link, native caption or embedded pane. The window is a browser tab unless the user installs the web app. The browser engine is whichever the user has | Opening the browser is own code on `os/exec` ([open.md](open.md)) |
| A Go shell on the system webview (Wails) | Native window, tray, dialogs, single-instance lock | A second artifact with cgo on macOS and Linux, installers, three webview engines, and a streaming limit on Windows. Rejected by the owner | Facts read from the module source are kept in [go-ui-stack.md](go-ui-stack.md#55-considered-and-not-used-a-desktop-shell) |
| Keep Electron around the Go host | Solves rows 13 and 14 | Brings back Node, a bundled engine and the whole packaging toolchain | Not evaluated further |

<a id="dh-adr-electron-1"></a>
#### DH-ADR-ELECTRON-1 There is no desktop shell; the feature table is the contract for the web UI and the command line

- **Decision.** Decided by the owner on 2026-11-18: the product is the `dsh` binary plus the web UI in the user's browser. This specification turns that into a checkable contract: section 2 gives each of upstream's twenty-six shell features one answer, and requirements R1 to R3, R6 to R8 and R10 state what the web UI and the command line must provide instead. Five features are dropped here with their reasons: the bundled runtime (row 3), the deep link (row 5), the tray icon (row 9), the page-to-native bridge (row 16) and drag-out of files (row 17).
- **Why.** "Port desktop to web" is only checkable as a feature list. Each dropped feature either needs an installer that no longer exists (deep link), a native window that no longer exists (tray, drag-out), or served a boundary that no longer exists (bridge, bundled runtime).
- **Tradeoff.** The user loses an always-visible tray entry and a one-click way back to the application; a bookmark, the installed web app or running `dsh` again replace it. The host is a terminal process, or a service the operator sets up, where upstream had an application that stayed in the background. This amends [DH-ADR-3](../design.md#dh-adr-3) and is a recorded exception to [DH-ADR-1](../design.md#dh-adr-1) for the five dropped rows.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-16](../design.md#dh-o-16). **Verified by.** `DH-T-ELECTRON-1` to `DH-T-ELECTRON-5`, and the real-browser scenario [DH-E-GOUISTACK-4](go-ui-stack.md#dh-e-gouistack-4).

<a id="dh-adr-electron-2"></a>
#### DH-ADR-ELECTRON-2 The embedded browser pane and the embedded sign-in pages are dropped

- **Decision.** Decided by the owner on 2026-11-18: there is no browser embedded in the UI and no embedded sign-in page. A link in the UI opens in a new tab of the system browser. A sign-in flow opens in the system browser and returns to the host through its ordinary callback route. The agent's own web work is the browser-use capability ([browserbasehqstagehand.md](browserbasehqstagehand.md)), which drives a separate browser process; the UI shows its results in the transcript and does not embed it.
- **Why it could not simply be ported.** Upstream's pane depends on four Electron facilities: a guest surface inside the window (`apps/desktop/src/main.ts:294`), a storage partition per workspace (`apps/desktop/src/browser-guests.ts:36`), a request filter that stops guests reaching the host (`:147`), and input capture inside guests (`apps/desktop/README.md:96`). A web page has none of the four. Inference: the only embedding a page can do is an `<iframe>`, which most sites forbid and which would share the browser's storage with the tab that holds the owner's cookie; `R6` forbids it.
- **Tradeoff.** The user cannot browse inside the application or watch the agent's browser live in a pane. This is a recorded exception to [DH-ADR-1](../design.md#dh-adr-1).
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-ELECTRON-3` for the navigation rule.

**Tradeoff of the whole choice.** The UI must work in every browser the project chooses
to support, not in one pinned engine; which browsers those are is an open question in
[go-ui-stack.md](go-ui-stack.md#7-risks-and-open-questions). Nothing native remains, so
anything a page cannot do (learn an absolute path, show itself on a link from another
program) is done by the host or not at all.

## 6. Design notes

- **Where the code lives.** No `internal/desktop` package. The launch, reuse and stop behaviour belongs to the command-line area (`cmd/dsh`, A24) and the web host (`internal/host`, A18); the directory picker belongs to area C6 (`ui.dirpick`) and the "host not reachable" state to area C1 ([../areas/ui-core.md](../areas/ui-core.md)); area A25 owns the table that gives each desktop duty its disposition ([../areas/app-web.md](../areas/app-web.md)).
- **Reusing a running host (`R1`).** The host writes a runtime record into the harness home when its listener is ready: listener address, process id and start time, in a file only the owner can read. A second `dsh` reads the record, checks that the host answers at that address, asks it for a launch token and opens the browser. A record whose host does not answer is stale: it is replaced by the new host. The owner decided that several processes may share one harness home under per-session leases ([DH-ADR-12](../design.md#dh-adr-12)), so this is a convenience that avoids a second listener by accident; it is not what protects the session log. Starting a second host on purpose, on another address, stays possible.
- **Ownership.** The host owns its listener and its runtime record and removes the record when it unloads. It does not own the browser or its tabs: closing them is the user's business, and the host never closes or focuses a browser window. It does not own the user's `PATH` or anything else outside the harness home.
- **Stop (`R3`).** Every stop request reaches one function, `launch.App.Stop` of area A21 ([../areas/boot.md](../areas/boot.md)), called by area A24's `lifecycle` component. An interactive stop asks host services what would be interrupted, with a two-second deadline, then asks the person once: in the terminal when the request came from the terminal, in the page when it came from the UI. A second request while the question is open joins it. A signal skips the question. Then the tree unloads from the leaves.
- **Failure and recovery (`R7`).** The host writes a "running" marker at start and removes it on clean exit. A panic is caught at the process boundary and written to a crash-report file; the process exits non-zero. Finding the marker at the next start prints one line saying the last run ended uncleanly and names the report and the safe-mode option. What stays uncertain is work in flight when the process died, which the session log reports on resume ([../design.md](../design.md) section 5).
- **Failure of a convenience.** If the browser cannot be opened, `dsh` prints the address and keeps running ([open.md](open.md), `DH-LIB-OPEN-R4`). If a notification cannot be shown, nothing is lost: the state is in the page.

## 7. Risks and open questions

1. **A second `dsh` never obtains a launch token** (`R1`). Decided by the owner ([DH-ADR-28](../design.md#dh-adr-28)): the host listens on a control endpoint, a Unix domain socket in an owner-only directory, whose `launch.open` method makes the running host open the browser or print a new one-time link on its own terminal. No method returns a link or any other credential ([../areas/host-telemetry.md](../areas/host-telemetry.md), `DH-D-HOST-3`).
2. **Cookies ignore ports.** Inference: in a general-purpose browser, a cookie set by `127.0.0.1:<port>` is also sent to any other local web server on `127.0.0.1`, which upstream's dedicated Electron profile never met. A signed, `HttpOnly`, `SameSite=Strict` cookie cannot be read by another site's script, but another local server would receive it. Whether to serve the UI under its own host name (for example a name under `.localhost`), which would scope the cookie, is for the security owner ([DH-ADR-SECURITY-7](../upstream/security.md#dh-adr-security-7)); it was not checked in any browser.
3. **A long-running host.** Upstream's application stayed in the background after its window closed. Whether the Go project documents a way to run `dsh` as a per-user service (and who then answers the stop question) is for the command-line area; nothing here installs one.
4. **The directory picker** lists directory names of the host machine to the owner. Whether it is offered to a non-loopback peer at all is for the security owner.
5. **Reserved shortcuts** (row 20) differ per browser; the shortcut table must avoid them.
6. **Owners.** The runtime record and host reuse are area A18's; the stop command and question, the unclean-exit marker and safe mode are areas A24's and A21's; the directory picker is area C6's and the "host not reachable" state area C1's. This specification declares no deliverable.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-ELECTRON-1` | local | `DH-LIB-ELECTRON-R1` | With a running fixture host and its runtime record: a second start opens the browser launcher with the first host's address and exits without binding a listener; with a record whose host does not answer, the second start replaces the record and becomes the host |
| `DH-T-ELECTRON-2` | local | `DH-LIB-ELECTRON-R3` | Table test of the stop decision: nothing running, work running, inspection error, inspection timeout, a second request while the question is open, a termination signal. Closing every stream connection stops nothing |
| `DH-T-ELECTRON-3` | local | `DH-LIB-ELECTRON-R6`, `DH-LIB-ELECTRON-R8` | Every rendered external link carries `rel="noopener noreferrer"` and a new-tab target; no view frames an external address; no route or script exposes a generic native call; the directory picker refuses a non-owner and a path the host cannot resolve |
| `DH-T-ELECTRON-4` | local | `DH-LIB-ELECTRON-R7` | A host made to fail at start prints one diagnostic naming a crash-report file and exits non-zero; a leftover "running" marker makes the next start print the unclean-exit line and accept the safe-mode option |
| `DH-T-ELECTRON-5` | local | `DH-LIB-ELECTRON-R10` | A table in the test lists every row of section 2 with its answer; the test fails when a row has no answer or a "dropped" row names no decision |

`DH-LIB-ELECTRON-R2` and the visible behaviour of rows 1, 4, 10 and 11 need a real
browser on each operating system. That is the real-environment scenario
[DH-E-GOUISTACK-4](go-ui-stack.md#dh-e-gouistack-4); a fake launcher does not prove it.
Nothing here has been run.

## 9. Units dispositioned by this spec

- `UP-DEP-electron`: replaced (`DH-LIB-ELECTRON`, DH-ADR-3, `DH-ADR-ELECTRON-1`, `DH-ADR-ELECTRON-2`).
- `UP-DEBT-pin.minimumReleaseAgeExclude.electron@44.7.0`: not applicable.
