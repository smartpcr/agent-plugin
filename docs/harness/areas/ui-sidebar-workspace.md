# The frame around the conversation (session list, workspaces, side panel, files, document preview, terminal, session panel, directory picker): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) (`2e39e70`) and are cited as `path:line` relative to the upstream root. Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `UISIDE`. Nothing here has been built, run or tested, and no browser file was fetched.

| | |
|---|---|
| **Area key** | `UISIDE` (agent C6) |
| **Upstream packages** | 14, all under `packages/client`: `ui-sidebar`, `ui-workspace`, `ui-session`, `ui-session-status`, `ui-session-panel`, `ui-sidebar-right`, `ui-sidebar-files`, `ui-sidebar-documentpreview`, `ui-sidebar-terminal`, `ui-sidebar-browser`, `ui-directory-picker-browse`, `ui-directory-picker-native`, `ui-open-in-app`, `ui-entry-customization` |
| **Serves outcomes** | [DH-O-12](../design.md#dh-o-12) first; also [DH-O-5](../design.md#dh-o-5) (sessions can be found, archived and deleted), [DH-O-7](../design.md#dh-o-7) (untrusted file content stays confined), [DH-O-10](../design.md#dh-o-10) (a missing LibreOffice is reported plainly) |
| **Depends on areas** | A19 ([api.md](api.md): routes, access, the event stream, terminals, workspace files; used as written), C1 ([ui-core.md](ui-core.md): page shell, regions, islands, preferences, catalogue, shortcuts; used as written), A18 ([host-telemetry.md](host-telemetry.md): directory browser, opening a path on the host), A07 ([storage.md](storage.md): workspace registry, `kv`), A06 ([session-views.md](session-views.md): list summary, search, working directory), A16 ([document-browser-computer.md](document-browser-computer.md): `office.Service`), A09 ([execution.md](execution.md): terminals), A17 (pending interactions), A02 (agent status), A27 (presets), C2 (layout mechanics, menus, dialogs), C4 ([ui-conversation.md](ui-conversation.md): the safe-HTML renderer, the session page's main view) |
| **Priority** | P0 for the session list with "new session" and the directory picker: without them a first web harness cannot start or find a conversation. P1 for session actions, search, reading reminders, the side panel, files, previews and the terminal. P2 for the session panel, "open in" and entry customisation; the interactive HTML preview is P1 with the other previews. Each deliverable states its one priority in [§8](#8-deliverables) |

## 1. Purpose

A person does not only read one conversation. They choose a project, find yesterday's session, see which sessions are waiting for them, look at the file the agent just wrote, run a command beside the agent, and pick a folder for new work. This area is that frame: the navigation column on the left (workspaces and sessions), the panel on the right of the conversation (files, document previews, terminals), the small panel of facts about the current session, the dialog that picks a directory of the host machine, and the menus that open a project in another application.

Upstream builds the frame from 14 React plugins that keep their own copies of host state in the browser, with a docking engine, a spreadsheet grid and an embedded browser. The Go host renders HTML ([DH-ADR-3](../design.md#dh-adr-3)), has no desktop application and no embedded browser pane ([DH-ADR-18](../design.md#dh-adr-18)). So this specification keeps what the user could *do*, moves every list, tab and preference to the host, keeps two JavaScript islands (the terminal and the PDF viewer), and records what is dropped.

## 2. Vocabulary

Terms defined elsewhere are used as written: **route, region, tab, interest, topic, `reset`, park, request id, user terminal, hold** ([api.md](api.md) §2); **page shell, contribution, framed island, host preference, per-tab store, message catalogue, command** ([ui-core.md](ui-core.md) §2); **workspace, ledger** ([storage.md](storage.md) §2); **projection, list summary** ([session-views.md](session-views.md) §2). In this document "browser tab" always means a tab of the user's browser, and "side tab" a tab inside the side panel.

| Term | What it is | Why it exists |
|---|---|---|
| **Navigation column** | The left column of every page: brand, "new session", entry links, the workspace and session list, settings. | One place to move between projects and conversations. |
| **Session row, group** | A row is one session in the list. A group is one workspace with its rows; sessions with no workspace form the group "Ungrouped". | The list is read by project. |
| **Blank session** | A session that has no prompt yet. Only the selected one is listed. | "New session" returns to an unsent draft instead of creating another empty session. |
| **Row status** | The one mark a row shows: waiting for the person, running, unread (finished or failed), or none. | A person scans the list for what needs them. |
| **Reading record** | Per person and session: the last turn the person has seen, and an explicit "unread" mark. | Reminders agree across browsers and are not part of the conversation. |
| **Side panel** | The column to the right of the conversation. It shows side tabs in one or two panes. | Files, previews and terminals stay beside the conversation they belong to. |
| **Side tab, kind** | A side tab is one opened thing (a file preview, a terminal). Its kind is the registered type that draws it (`files`, `preview`, `terminal`, `start`). | Other areas add kinds without importing this one. |
| **Start page** | The side tab a pane shows when nothing else is open: one card per kind that can be opened by hand. | A way in that needs no link in the transcript. |
| **Pop-out** | A side tab opened as a page of its own in another browser window. | The web equivalent of upstream's floating panels. |
| **Renderer** | The registered function that turns one file type into a preview (text, Markdown, image, PDF, spreadsheet, office document, HTML). | A file type can gain a better preview without changing the tab. |
| **Session panel** | A small panel of facts about the current session (working folder, execution environment, running work), made of sections that areas contribute. | One home for session facts that takes no width from the transcript. |
| **Directory picker** | The dialog that lists directories of the *host* machine and returns one path. | A browser's own file dialog cannot name a path on the host ([host-telemetry.md](host-telemetry.md) §2). |
| **Entry** | A link in the navigation column or a card on the start page that a component contributed. | The user can hide entries they do not use. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.client.ui-sidebar` | `dsh-client-ui-sidebar` | definition (shell with seats) | The left column: brand and version badge, New Session, a 56 px collapsed rail, a drawer below 768 px, panel entries with a More menu, the seat for the workspace browser and for Settings (`packages/client/ui-sidebar/README.md:14`, `:32`, `:42`, `:46`). |
| `UP-PKG-packages.client.ui-workspace` | `dsh-client-ui-workspace` | consumer | The workspace and session browser: grouped, tree or flat lists, sort modes, search, rename, fork, pin, archive, workspace add and delete, row status dots, the reusable workspace picker (`packages/client/ui-workspace/README.md:14`, `:40`, `:48`, `:58`, `:68`). |
| `UP-PKG-packages.client.ui-session` | `dsh-client-ui-session` | provider | Adapts the session catalog to React hooks and session-scoped slots; holds the highest-precedence pending interaction per session (`packages/client/ui-session/README.md:11-13`). |
| `UP-PKG-packages.client.ui-session-status` | `dsh-client-ui-session-status` | provider, host and client | `ctx.sessionStatus` keeps reading records in host memory; the client half merges running state, pending interaction and unread marks into one status table (`packages/client/ui-session-status/README.md:12`, `:44-47`, `:59`). |
| `UP-PKG-packages.client.ui-session-panel` | `dsh-client-ui-session-panel` | definition and consumer | A header toggle and a floating panel of sections that plugins register; ships the Environment section with the working folder (`packages/client/ui-session-panel/README.md:12`, `:41-43`). |
| `UP-PKG-packages.client.ui-sidebar-right` | `dsh-client-ui-sidebar-right` | definition and provider | One docking surface per session over `ui-dockkit`: split panes, tabs, floating panels, a recorded operation sequence; the tab-type registry and the navigation service `ctx.sidebarRight` (`packages/client/ui-sidebar-right/README.md:12`, `:68`, `:85`, `:95`). |
| `UP-PKG-packages.client.ui-sidebar-files` | `dsh-client-ui-sidebar-files` | consumer | The `files` tab and a workspace tree beside every preview, listed one level at a time, with host-side name search and exclusion rules (`packages/client/ui-sidebar-files/README.md:12`, `:59`, `:80`, `:84`). |
| `UP-PKG-packages.client.ui-sidebar-documentpreview` | `dsh-client-ui-sidebar-documentpreview` | definition and consumer | The fallback tab for any file address, with a renderer registry: paged text, Markdown, code, images, PDF (pdf.js), HTML, spreadsheets parsed in the browser, Word and PowerPoint converted to PDF on the host (`packages/client/ui-sidebar-documentpreview/README.md:12`, `:37`, `:75`, `:98`). |
| `UP-PKG-packages.client.ui-sidebar-terminal` | `dsh-client-ui-sidebar-terminal` | consumer | The `terminal` tab: xterm.js over the terminal controller, a shell picker, rename, reconnect, take control, agent terminals with a badge (`packages/client/ui-sidebar-terminal/README.md:12`, `:32-40`). |
| `UP-PKG-packages.client.ui-sidebar-browser` | `dsh-client-ui-sidebar-browser` | consumer | The `browser` tab: a web page in a sandboxed iframe on the web (off by default there) and an Electron webview on the desktop (`packages/client/ui-sidebar-browser/README.md:12`, `:28`, `:63`). |
| `UP-PKG-packages.client.ui-directory-picker-browse` | `dsh-client-ui-directory-picker-browse` | consumer | An in-page dialog that lists, navigates and creates host directories (`packages/client/ui-directory-picker-browse/README.md:12`, `:32`). |
| `UP-PKG-packages.client.ui-directory-picker-native` | `dsh-client-ui-directory-picker-native` | consumer | A renderless occupant that opens the operating system's folder chooser on the host machine or in Electron (`packages/client/ui-directory-picker-native/README.md:12`, `:30`). |
| `UP-PKG-packages.client.ui-open-in-app` | `dsh-client-ui-open-in-app` | consumer | "Open in" menus for a session's directory, and "open with" and "show file location" controls for files (`packages/client/ui-open-in-app/README.md:12`, `:42`). |
| `UP-PKG-packages.client.ui-entry-customization` | `dsh-client-ui-entry-customization` | consumer | The content of three More menus: hidden entries, "Browse other plugins", "Create your plugin" (`packages/client/ui-entry-customization/README.md:14`, `:31-33`). |

### 3.2 Behaviour

**The session list.** The browser holds the whole session catalog and the workspace projection and derives the list itself. A workspace group shows its first five non-blank sessions and raises that count to cover any session that is running, waiting, unread or open (`packages/client/ui-workspace/README.md:30`). Pinned rows lead; the order is "last updated" or a manual order. The manual order of sessions is saved in the browser, the order of workspace groups on the host (`:40-44`). Workspaces are siblings or nest under their nearest registered ancestor (`:48`). Sessions that a subagent created are hidden (`:243`). A row shows one status with a fixed precedence: waiting for the person, then running, then unread; a failed turn's reminder is red, any other ended turn's green (`:78`). New Session targets the explicit workspace, else the current session's, else the most recently active one, and reuses that workspace's first blank session unless a named preset differs (`packages/client/ui-sidebar/README.md:14`; `packages/client/ui-workspace/README.md:98`). An empty installation gets a default workspace and a blank session (`:100`).

**Search and actions.** A query replaces the list with at most 20 flat results: title, workspace and id matches at once, then content matches with snippets from a host request sent after 250 ms (`:58`). Rename opens a dialog; confirming an unchanged title pins it. Fork copies up to the last completed turn. Archive needs no confirmation for a quiet session; when the host refuses because work is running, a dialog lists that work and offers "stop and archive", and a notice offers undo (`:66-68`). Sessions cannot be deleted (`:292`).

**Reading reminders.** The host keeps, per session and in memory only, the last completed turn, whether it failed, the last turn read and a manual-unread flag (`packages/client/ui-session-status/README.md:59`). A turn that ends makes the session unread. A visible, focused page that shows the session acknowledges it; an acknowledgement never clears a manual mark (`:30`, `:61`). All browsers of one host share the result; a host restart forgets everything (`:28`).

**The right sidebar.** Each session has a docking layout of one or two side-by-side panes plus floating panels. Every change is a recorded operation; the layout is saved in `localStorage` per session (`packages/client/ui-sidebar-right/README.md:68`, `:76`). A tab type registers address patterns and a priority band; opening an address picks the best type, focuses an existing tab or opens a new one, and expands the panel (`:85-95`). A pane is never empty: it shows the "guide", a page of entry cards, or directly the only registered page (`:72`, `:127`). Below 768 px the panel covers the viewport (`:39`).

**Files.** The tree's root is the session's working directory. A level is listed when first expanded, entries starting with `.` are hidden until a toggle shows them, a level cut by the host's cap ends with a marker (`packages/client/ui-sidebar-files/README.md:59`, `:67`). Tree state is kept per pane so that it survives tab switches, and a hidden tree holds no directory watch on the host (`:69`). Search walks the whole workspace on the host by name or relative path, with exclusion rules and a `.gitignore` choice saved in browser storage per workspace root (`:80-86`).

**Document preview.** Text, Markdown and code are read in pages of lines; PDF, HTML, images and spreadsheets are read whole, up to the host's file cap (`packages/client/ui-sidebar-documentpreview/README.md:58-59`). HTML is shown in an iframe with no sandbox permissions after sanitising, or, when a setting allows scripts, in a `sandbox="allow-scripts"` frame with its directly referenced local `.js` and `.css` packed in (`:51`, `:62`). Images are drawn through `<img>`, so SVG scripts cannot run (`:64`). Spreadsheets (`.xlsx`, `.xls`, `.csv`, `.tsv`) are parsed in a Worker and drawn by a grid widget with saved formula results and no recalculation (`:75`). Word and PowerPoint files are converted to PDF by the host's LibreOffice provider; a missing provider shows configuration guidance (`:98`). A file that changes is reloaded automatically (`:60`).

**Terminal.** A terminal tab starts the remembered shell in the session's directory. The process outlives a collapsed panel and a page reload; closing the tab ends it. A second page that attaches is read-only until it presses "Take control"; a lost connection keeps the screen and offers "Reconnect" (`packages/client/ui-sidebar-terminal/README.md:34-38`). A terminal the agent opened appears by itself with an "Agent" badge, and the person's first keystroke takes it over (`:40`).

**Browser, session panel, pickers, "open in", entries.** The browser tab accepts only `http` and `https` addresses, rejects the application's own origin and local files, and cannot see where a cross-origin frame navigated (`packages/client/ui-sidebar-browser/README.md:59`, `:65`). The session panel docks in the gutter beside the transcript when at least 348 px are free and otherwise opens as an overlay that Escape or an outside press closes (`packages/client/ui-session-panel/README.md:32-34`). The browse picker is a column view over the host's list and create calls; the native picker opens a chooser on the host's display and cannot be cancelled from the page (`packages/client/ui-directory-picker-browse/README.md:42`; `packages/client/ui-directory-picker-native/README.md:46`). "Open in" lists installed applications from the host; file menus list associated applications and "show file location" (`packages/client/ui-open-in-app/README.md:36`, `:42`). Each region keeps its hidden entries in memory, so a reload restores every entry (`packages/client/ui-entry-customization/README.md:68`).

### 3.3 Failure, retry, rollback and cleanup upstream

- **Search.** A failed content search leaves the metadata matches with no warning (`packages/client/ui-workspace/README.md:58`).
- **Archive with running work.** The archive is durable before the stops settle; a failed stop is reported later and the session stays archived (`:68`).
- **Reading.** A failed mark keeps the previous reminder and raises a toast; a broken reading stream disables the commands until the page reloads (`packages/client/ui-session-status/README.md:30`, `:99`).
- **Layout.** Invalid saved layout data clears only that session's key; a storage failure leaves the layout in memory (`packages/client/ui-sidebar-right/README.md:76`).
- **Preview.** A read failure before any content shows the file icon, the reason and a retry; a later failure keeps what was loaded (`packages/client/ui-sidebar-documentpreview/README.md:58`). A failed PDF or terminal chunk load needs a page reload (`:153`; `packages/client/ui-sidebar-terminal/README.md:82`).
- **Terminal.** Closing a tab removes it at once and ends the process in the background; a cleanup failure has no notice and no manual retry (`packages/client/ui-sidebar-terminal/README.md:36`). A saved terminal whose process is gone shows "unavailable" with "New terminal" (`:38`).
- **Left behind.** Reading records of unloaded sessions stay until the service stops (`packages/client/ui-session-status/README.md:100`); a native chooser stays open after its page is gone (`packages/client/ui-directory-picker-native/README.md:80`).

## 4. Upstream decisions dissected

The first 12 rows are the decision notes handed to this area, named by file under `.agents/notes/implemented/`. The rest are visible only in READMEs. `UISIDE-n` is short for `DH-ADR-UISIDE-n` in [§7](#7-decisions). The ledger rows hold the full reasoning.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `architecture/2026-09-08-document-preview-operations` | Watching a file and reading it are separate; every path is resolved through the session's filesystem | Replace | The host half is A19's file routes; watching becomes an interest of the browser tab | [UISIDE-5](#dh-adr-uiside-5) |
| `architecture/2026-09-15-client-session-references` | Counted references to client session objects; no global current session | Replace | No client objects exist; "no global current session" is kept because every view names its session in the URL | [UISIDE-1](#dh-adr-uiside-1) |
| `architecture/2026-09-20-sidebar-retained-tab-layout` | Keep DOM ancestors stable because Electron's webview resets when detached | Replace | The webview is gone; what must survive a switch (terminal screen, read position) lives on the host or in the per-tab store | [UISIDE-3](#dh-adr-uiside-3), [UISIDE-7](#dh-adr-uiside-7) |
| `architecture/2026-10-14-plugin-entry-customization` | Each region owns its hidden entries and its More menu | Replace | Kept as a stored preference per region; slot sharing is React-specific | [UISIDE-9](#dh-adr-uiside-9) |
| `architecture/2026-10-20-new-session-blank-reuse-by-preset` | New Session reuses the first blank; a named preset skips a blank of another preset | Keep | The rule moves into the host's create route (A19), which checks under the session's lease | [UISIDE-1](#dh-adr-uiside-1) |
| `architecture/2026-10-20-workspace-tree-state-per-pane` | Tree state per pane; hidden trees hold no watch; the mounting plugin decides where a file opens | Replace | State per session on the host; a watch is an interest that exists only while the tree is on a page | [UISIDE-6](#dh-adr-uiside-6) |
| `feature/2026-09-04-right-sidebar-docking-infrastructure` | A per-session docking surface with splits, floats and an undoable log | Change | A shared side surface stays; the dock engine, floats and the log do not | [UISIDE-3](#dh-adr-uiside-3) |
| `feature/2026-09-16-browser-excel-preview` | Parse spreadsheets in the browser; show saved formula results; a chart must not block the cells | Replace | Requirements kept; parsing and rendering move to the host ([DH-ADR-EXCELJS-1](../libs/exceljs.md#dh-adr-exceljs-1)) | [UISIDE-5](#dh-adr-uiside-5) |
| `feature/2026-09-16-sidebar-browser` | A sandboxed iframe browser tab with a strict address parser | Drop | The owner dropped the embedded browser pane; the first verdict ("ported") is overruled | [DH-ADR-18](../design.md#dh-adr-18), [UISIDE-4](#dh-adr-uiside-4) |
| `feature/2026-10-23-session-reading-memory` | Reading state on the host, in memory, outside the log | Keep, change storage | Host-owned and outside the log stays; memory-only is replaced, because it loses reminders on restart | [UISIDE-2](#dh-adr-uiside-2) |
| `feature/2026-11-05-session-panel` | One floating panel of session facts that docks only when the gutter has room | Replace | Same goal with the browser's popover and CSS, and no script | [UISIDE-9](#dh-adr-uiside-9) |
| `feature/2026-11-17-sidebar-scrollbar-fade-in-css` | The scrollbar's hold and fade are a CSS transition, not a timer | Keep | Pure CSS, independent of React; the style rules carry over | [DH-D-UISIDE-1](#dh-d-uiside-1) |
| The browser derives the list from the whole catalog | `packages/client/ui-workspace/README.md:241` | Change | The host renders rows from the list projection and the ledger | [UISIDE-1](#dh-adr-uiside-1) |
| Session order in browser storage, workspace order on the host | `packages/client/ui-workspace/README.md:44` | Change | Both orders are host state (A19's order routes), so two browsers agree | [UISIDE-1](#dh-adr-uiside-1) |
| Drag and drop reorders rows | `packages/client/ui-workspace/README.md:42` | Change | Reordering is a menu action and a form; drag needs script and is left to C2 | [UISIDE-1](#dh-adr-uiside-1) |
| Recent views keep up to 10 sessions' history loaded | `packages/client/ui-workspace/README.md:92` | Drop | A page is rendered from the store on request; nothing is retained in the browser | [UISIDE-1](#dh-adr-uiside-1) |
| Status precedence: waiting, running, unread | `packages/client/ui-workspace/README.md:78` | Keep | Language-neutral; composed on the host per row | [UISIDE-2](#dh-adr-uiside-2) |
| Tab types claim addresses by band, then pattern length | `packages/client/ui-sidebar-right/README.md:88` | Keep | Lets another area add a better viewer for one file type | [UISIDE-3](#dh-adr-uiside-3) |
| Closing the last tab collapses the panel; a pane is never empty | `packages/client/ui-sidebar-right/README.md:72-74` | Keep | Simple and predictable | [UISIDE-3](#dh-adr-uiside-3) |
| Renderers register by extension; plain text is the fallback | `packages/client/ui-sidebar-documentpreview/README.md:37` | Keep | Same registry on the host | [UISIDE-5](#dh-adr-uiside-5) |
| SVG only through `<img>`; static HTML with no sandbox permissions | `packages/client/ui-sidebar-documentpreview/README.md:51`, `:64` | Keep | The browser's isolation is the control ([DH-ADR-DOMPURIFY-2](../libs/micromark.md#dh-adr-dompurify-2)) | [UISIDE-5](#dh-adr-uiside-5) |
| Every changed file reloads its preview | `packages/client/ui-sidebar-documentpreview/README.md:60` | Change | Text and images refresh; PDF, office, spreadsheet and HTML previews show "changed, reload" | [UISIDE-5](#dh-adr-uiside-5) |
| Agent terminals open a tab and expand the panel | `packages/client/ui-sidebar-terminal/README.md:40` | Change | The roster shows them and the header toggle carries a mark; the host does not move a person's panel | [UISIDE-7](#dh-adr-uiside-7) |
| The latest attachment owns input; others are read-only | `packages/client/ui-sidebar-terminal/README.md:34` | Keep | It is A19's socket rule | [UISIDE-7](#dh-adr-uiside-7) |
| Two picker surfaces chosen by composition | `packages/client/ui-directory-picker-native/README.md:34` | Change | One server-rendered picker ([host-telemetry.md](host-telemetry.md), `DH-ADR-HOST-8`) | [UISIDE-8](#dh-adr-uiside-8) |
| File menus open a file with its associated application or reveal it | `packages/client/ui-open-in-app/README.md:42` | Drop | The host opens workspace directories only ([host-telemetry.md](host-telemetry.md), `DH-ADR-HOST-9`) | [UISIDE-8](#dh-adr-uiside-8) |

## 5. Tech debt and its resolution

All 76 units are grouped by cause; each unit's own reason is in its ledger row. `L:` stands for `UP-DEBT-limit.packages.client.`. Carried rows name stage [DH-S-59](../impl.md#dh-s-59), where the known limits and scope boundaries of this part of the product are stated and checked.

| Group (cause) | Units | How the Go design answers it |
|---|---|---|
| **A. State kept in the browser or in host memory** | `L:ui-session-status.1` to `.7`, `L:ui-session.1`, `L:ui-sidebar-right.1`, `.4`, `.6`, `L:ui-sidebar-files.6`, `L:ui-entry-customization.1`, `L:ui-open-in-app.2` (14) | Resolved. Reading records are stored per person and combined with a completion that is folded from the log, so they survive a restart and are shared by every process on the home; marks carry a revision. Side tabs, tree state, hidden entries and search preferences are host state rendered per request. Pending interactions come from A19's `interaction` baseline on every connect ([DH-D-UISIDE-3](#dh-d-uiside-3), [DH-D-UISIDE-4](#dh-d-uiside-4)). |
| **B. React, client stores and client composition** | `L:ui-sidebar.1`, `.2`, `L:ui-sidebar-documentpreview.7`, `L:ui-entry-customization.2` (4) | Not applicable. Each describes which React plugin owns a piece of rendering, a store write per scroll event, or values of an older client format. The host renders one list and keeps no client store ([DH-ADR-3](../design.md#dh-adr-3)). |
| **C. Lazy code chunks that cannot be retried** | `L:ui-sidebar-documentpreview.8`, `L:ui-sidebar-terminal.5` (2) | Resolved. An island module is imported again the next time its element appears ([DH-ADR-GOUISTACK-6](../libs/go-ui-stack.md#dh-adr-gouistack-6)). |
| **D. The embedded browser tab** | `L:ui-sidebar-browser.1` to `.7` (7) | Resolved by removal: the tab kind is not built ([DH-ADR-UISIDE-4](#dh-adr-uiside-4)), so no page is framed, no popup can reach the application and no address is saved. |
| **E. A dialog on the host's display, and desktop-only file gestures** | `L:ui-directory-picker-native.1` to `.3`, `L:ui-workspace.5`, `L:ui-open-in-app.3` to `.7` (9) | Resolved by removal or by one path. There is one directory picker, rendered by the host. Files are not opened with host applications, so there are no file associations to cache, no reveal label and no second opener ([DH-ADR-UISIDE-8](#dh-adr-uiside-8)). |
| **F. Things the list could not do** | `L:ui-workspace.1` to `.4`, `L:ui-sidebar.3`, `.4` (6) | `.2`, `.3` and both `ui-sidebar` units resolved: sessions can be deleted, a collapsed group shows a mark for waiting sessions inside it, the version badge is its own keyboard-reachable control, and the column has one scroller so no gesture needs splitting. `L:ui-workspace.1` carried in part: a search result now links to its event, but matching stays literal. `.4` carried in part: every row takes Tab focus; arrow-key movement needs script and is asked of C2 ([DH-D-UISIDE-1](#dh-d-uiside-1), [DH-D-UISIDE-2](#dh-d-uiside-2)). |
| **G. The side panel's layout engine** | `L:ui-sidebar-right.2`, `.3`, `.5`, `.7`, `L:ui-entry-customization.4` to `.6` (7) | `.3` and `.7` resolved: there is no floating layer, menus and dialogs use the browser's top layer, and the selected side tab is in the URL so Back and Forward step through visited content. Carried: the panel needs a session (`.2`, `entry.5`), there is no layout undo (`.5`), entries keep contribution order (`entry.4`) and the menus do not list plugins (`entry.6`). |
| **H. Files tree limits** | `L:ui-sidebar-files.1` to `.5`, `.7` (6) | `.2` to `.5` resolved on the host: the current file's ancestors are always drawn, one hidden rule serves tree and search, a path query is highlighted on the path, and an exclusion rule is a literal name or path. `.1` (listing only) and `.7` (one root) carried: the view is read-only and A19 keeps listing inside the workspace ([DH-D-UISIDE-5](#dh-d-uiside-5)). |
| **I. Preview limits** | `L:ui-sidebar-documentpreview.1` to `.6` (6) | `.3` resolved: text is read by line window from any line, and the PDF island reads by byte range. Carried: preview is not editing (`.1`); office fidelity and font diagnostics are the installed LibreOffice's (`.2`, [DH-ADR-20](../design.md#dh-adr-20)); the page bitmap cap (`.4`); scroll inside the sandboxed HTML frame (`.5`); only direct `.js` and `.css` are packed for an interactive HTML preview (`.6`). |
| **J. Terminal limits** | `L:ui-sidebar-terminal.1` to `.4` (4) | Carried, each by design or by another area: a shell that cannot start is reported and never replaced by another; completion is the shell's; colours a program set are not in the host snapshot (A09); history is bounded and terminals do not survive a host restart (A19). |
| **K. Session panel** | `L:ui-session-panel.1` to `.5` (5) | `.2`, `.4`, `.5` resolved: the host knows the execution world and sandbox mode and draws them; on another view the toggle is a link to the chat view; the panel exists once per page. `.1` (overlay on laptop widths) and `.3` (no section customisation) carried ([DH-D-UISIDE-9](#dh-d-uiside-9)). |
| **L. Picker, "open in", entries, other people** | `L:ui-directory-picker-browse.1`, `.2`, the marker `UP-DEBT-marker.packages/client/ui-directory-picker-browse/src/client/DirectoryBrowser.tsx:128`, `L:ui-open-in-app.1`, `L:ui-entry-customization.3`, `L:ui-session-status.8` (6) | Hidden entries are filtered on the host; the host writes every path and breadcrumb, so nothing infers a separator; an operator-defined application shows its configured name; "Create your plugin" is drawn only when its preset exists. Carried: the picker has no search, multi-select, rename or delete (`browse.1`), and reminders are each person's own with no receipts for others (`status.8`). |

Totals: 50 resolved, 4 not applicable, 22 carried.

## 6. Go design

### 6.1 Shape

Packages are under `src/harness/internal/ui`, the family [../design.md §4.2](../design.md#42-go-module-layout) gives to the UI areas. Every route below is registered through A19's `web.Routes` under A19's rules; every view is written against C1's view contract (`ui.Req`, `ui.View[T]`, `ui.SafeHTML`).

| Package | Component and service key | Why it exists |
|---|---|---|
| `ui/nav` | Component `ui.nav`; no service | The navigation column: contributes to `shell.nav`, renders groups, rows, search results and dialogs, registers the list's regions with the stream |
| `ui/reading` | Component `ui.reading`; `reading.Key` (`Service`) | Reading records and the row status; replaces `ctx.sessionStatus` |
| `ui/side` | Component `ui.side`; `side.Key` (`Surface`) | The side panel: the kind registry, the stored side tabs of each session, the strip, the start page |
| `ui/files` | Component `ui.files`; no service | The `files` kind and the tree fragment over A19's `wsfiles` |
| `ui/preview` | Component `ui.preview`; `preview.Key` (`Renderers`) | The `preview` kind, the renderer registry, the built-in renderers, the `<dsh-pdf>` island |
| `ui/preview/sheet` | No component | The spreadsheet reader and table view of [libs/exceljs.md](../libs/exceljs.md) §6 and [libs/papaparse.md](../libs/papaparse.md) |
| `ui/term` | Component `ui.term`; no service | The `terminal` kind, the roster view, the frame document of the `<dsh-terminal>` island |
| `ui/sessionpanel` | Component `ui.sessionpanel`; no service | The session panel, its regions and the Environment section |
| `ui/dirpick` | Component `ui.dirpick`; no service | The directory picker dialog and the "add workspace" flow |
| `ui/openin` | Component `ui.openin`; no service | "Open in" menu items; draws nothing where A18's route is not registered |

```go
// package reading
type Record struct{ ReadTurn int; ManualUnread bool; Rev uint64 }          // stored per (principal, session)
type Status struct{ Pending interact.Kind; Running, Unread, Failed bool }  // what one row shows; precedence: Pending, Running, Unread
type Service interface {
    Status(ctx context.Context, who access.Principal, s session.ID) (Status, error)
    Seen(ctx context.Context, who access.Principal, s session.ID, turn int) error   // automatic: only forward, never past the last completed turn, never clears a manual mark
    Mark(ctx context.Context, who access.Principal, s session.ID, unread bool, rev uint64) error // explicit: "reading/stale" when rev is not current
}

// package side
type Kind struct {
    ID       string                                   // "preview", "files", "terminal", "start"; unique
    Patterns []string; Band Band                      // which addresses it claims: Extension > Builtin > Fallback, then longest pattern
    Multiple bool                                     // several tabs of this kind may be open (terminals)
    Title    func(rq ui.Req, t Tab) string            // rendered per request, in the request's language
    Body     func(ctx context.Context, rq ui.Req, t Tab) (ui.SafeHTML, error)
    Tabs     func(ctx context.Context, s session.ID) ([]Tab, error) // optional: tabs that exist because host state does (the terminal roster)
    Card     *Card                                    // optional start-page entry: label key, glyph, order
}
type Tab struct{ ID, Kind, Address string; Session session.ID; Pane int; Line int }
type State struct{ Tabs []Tab; Selected [2]string; Split bool; Tree Tree; Rev uint64 } // Tree: Open bool, Expanded []string (capped)
type Surface interface {
    Register(owner *cordis.Context, k Kind) error     // an effect of owner
    Open(ctx context.Context, who access.Principal, s session.ID, address string, o OpenOptions) (Tab, error) // reveals an open tab, else adds one
    Close(ctx context.Context, who access.Principal, s session.ID, tab string) error
    State(ctx context.Context, who access.Principal, s session.ID) (State, error)
    Href(s session.ID, address string, line int) string // the link any area writes for "show this file beside the conversation"
}

// package preview
type Renderer struct {
    ID string; Extensions []string; Binary bool; Band side.Band; Title msg.Key
    Render func(ctx context.Context, rq ui.Req, src Source, o Options) (ui.SafeHTML, error) // Source: session, path, stat; Options: line, sheet, row
}
type Renderers interface {
    Register(owner *cordis.Context, r Renderer) error // an effect of owner
    For(name string) []Renderer                       // best first; the plain-text renderer is last unless the type is Binary
}
```

**Where each upstream package's view lives.** "Server" means a server-rendered fragment updated by htmx and the event stream.

| What the user sees | Placement | Host state it reads, and the owner |
|---|---|---|
| Navigation column, groups, rows, status marks, view options, search results | Server | List summary and search (A06 `query.Key`), ledger, order, archive and pin (A07 `workspace.Registry`), agent status (A02), pending interactions (A17), reading records (this area) |
| Rename, archive, delete, add-workspace dialogs; row and group menus | Server (`<dialog>`, C2's menu partial) | The same, through A19's session and workspace routes |
| Side panel strip, start page, panes | Server | `side.State` in A07's `kv` (this area) |
| Files tree and file search | Server | `wsfiles.Key` (A19) over the session's filesystem seam (A08) |
| Text, Markdown, code, image, spreadsheet previews | Server | `wsfiles.Key`; C4's renderer for Markdown and code |
| PDF, and office documents converted to PDF | **Island** `<dsh-pdf>`, vendored pdf.js ([DH-ADR-PDFJSDIST-1](../libs/pdfjs-dist.md#dh-adr-pdfjsdist-1)) | Bytes from A19's `file.raw` or this area's `preview.pdf`, which calls `office.Service.PDF` (A16) |
| HTML file preview | Server-filtered document in a sandboxed `<iframe>` (a browser feature, not an island) | `wsfiles.Key` |
| Terminal | **Framed island** `<dsh-terminal>`, vendored xterm.js ([libs/xtermxterm.md](../libs/xtermxterm.md)) | `userterm.Key` and the terminal socket (A19), over A09's terminals |
| Embedded web browser tab | **Dropped** ([DH-ADR-UISIDE-4](#dh-adr-uiside-4)) | none |
| Session panel | Server, with the browser's popover | Working directory (A06), execution world and sandbox mode (A10), sections other areas contribute |
| Directory picker | Server (`<dialog>`) | `dirbrowse.Key` (A18) |
| Native folder chooser | **Dropped** ([DH-ADR-UISIDE-8](#dh-adr-uiside-8)) | none |
| "Open in" for a workspace directory | Server | `openpath.Key` and its two routes (A18) |
| "Open with" and "show file location" for a file | **Dropped** ([DH-ADR-UISIDE-8](#dh-adr-uiside-8)) | none |
| More menus, hidden entries | Server | Host preferences (C1 `prefs.Key`) |

### 6.2 Contracts

**Routes this area registers.** `S` is `access.Session("sid")`; activation is `None` for all of them, so looking never wakes a session.

| Method and pattern (name) | Returns and rules |
|---|---|
| `PUT /sessions/{sid}/reading` (`session.reading`) | Form: either `seen=<turn>` (automatic) or `unread=0\|1` with the record's `rev`. A stale `rev` answers 409 with the row re-rendered. Access: `S` |
| `GET /sessions/{sid}/side?tab=` (`side.show`) | Fragment: the strip and the selected body of each pane. Registers the interests of what it draws. Access: `S` |
| `GET /sessions/{sid}/side/{tab}` (`side.page`) | Page: one side tab alone (the pop-out). `{tab}` is opaque. Access: `S` |
| `POST /sessions/{sid}/side/tabs` (`side.open`); `DELETE /sessions/{sid}/side/tabs/{tab}` (`side.close`) | Open by address or by kind; close. Closing a terminal's tab closes the terminal through A19's route. Answers the panel region. Access: `S` |
| `PUT /sessions/{sid}/side/layout` (`side.layout`) | The whole placement: order, pane of each tab, split. Unknown tab ids are refused. Access: `S` |
| `POST /sessions/{sid}/side/tree` (`files.toggle`) | Form: directory, open or closed. Records the expansion and answers that level's fragment. Access: `S` |
| `GET /sessions/{sid}/preview?path=&as=&line=` (`preview.show`) | Fragment: header, renderer choice and body for one file. `as` names a renderer. Access: `S` |
| `GET /sessions/{sid}/preview/sheet?path=&sheet=&row=` (`preview.sheet`) | Fragment: one worksheet page of `PageRows` rows. Access: `S` |
| `GET /sessions/{sid}/preview/pdf?path=` (`preview.pdf`) | Download: the PDF that `office.Service.PDF` made of an office file. `office/unavailable` answers 503 with the reason. Access: `S` |
| `GET /sessions/{sid}/preview/html?path=` (`preview.html`) | The filtered document for the sandboxed frame, with its own policy as a response header. Access: `S` |
| `GET /sessions/{sid}/panel` (`session.panel`) | Fragment: the session panel. Access: `S` |

**Routes of other areas this area's views use, as written.** A19: `session.list` (with a text filter for search), `session.create`, `session.open`, `session.rename`, `session.fork`, `session.delete`, the workspace routes, archive and pin, `directory.browse`, the terminal routes and `terminal.attach`, and the file routes `tree`, `text`, `stat`, `search`, `file.raw` ([api.md §6.2](api.md#62-contracts)). A18: the two `openpath` routes. C1: `ui.pref`, and the `Frame` route the shell registers for the terminal island.

**Stream.** No new event name. The list is the `shell` state of A19: this area registers its regions (`#nav-list`, one `#ws-{id}` per group, one `#sess-{id}` per row) and re-renders a row when `agent.StatusChanged`, `workspace.Changed`, the list summary, a pending interaction or `reading.Changed` says so. Because state is replaced whole and is complete on connect, **a second browser tab** shows a created, renamed, archived, deleted or newly unread session without a reload, and a parked tab is brought up to date when it is shown again. Interests, delivered as `view` events: `files` (one per drawn directory), `file` (one per previewed path, A19's invalidation), `terminals` (the roster of a session), and this area's own state sources `side` (the strip of a session) and `session-panel`.

**Regions this area declares** (C1's registry; other areas contribute).

| Region (kind) | Scope | Filled by |
|---|---|---|
| `nav.entries` (List) | page | Entry links: plugins and settings (C7), schedules and jobs (C5) |
| `nav.session.mark`, `nav.session.menu` (List) | one session row | A mark in an idle row's status cell (a schedule clock, C5); extra menu items |
| `side.actions` (List) | one side tab | Header controls of a tab: "open in" (this area), others |
| `session.panel.section`, `session.panel.indicator` (List) | one session | Sections (subagents, jobs: C5; change requests: X1) and marks on the toggle |
| `session.panel.environment` (List) | one session | Rows of the Environment section (checkout and local changes: X1) |

This area contributes to C1's `shell.nav` and `shell.aside` and to `shell.header` (the side panel toggle and the session panel toggle).

**The session list.** Groups follow the stored grouping preference: workspaces as siblings, workspaces nested under their nearest registered ancestor, or one flat list. Pinned rows lead; the order is "last updated" (the latest prompt or steer time from the list summary) or the stored manual order. A group draws `GroupRows` rows and always the rows that are running, waiting, unread or open; "Show more" fetches five more. Sessions created by a subagent and blank sessions other than the selected one are not listed. The archive filter has three values (hide, show, only). Row status is `reading.Status`. A collapsed group shows a count of waiting sessions inside it. **New session** is a form to A19's `session.create` with the workspace (explicit, else the current session's, else the most recently active), the preset if a flow names one, and a request id; the host applies the blank-reuse rule. With no workspace at all the page shows the directory picker's entry point; A19's `POST /workspaces/default` creates the first-use workspace when A07 allows it.

**Search.** The search field requests `session.list` with the query 250 ms after typing stops. The result fragment holds at most `SearchResults` rows: title, workspace and id matches first, then content matches from A06's `Search` with a snippet. A content result links to `/sessions/{sid}?at={seq}`, so it opens at the matching event. An archived result offers "Unarchive" and does not open. If the index is off (`query/search-disabled`) the fragment says so and still shows the metadata matches.

**Session and workspace actions.** Rename is a dialog over `PUT /sessions/{sid}/title`, prefilled; confirming an unchanged title pins it. Fork posts `session.fork`. Archive posts at once; a 409 `workspace/session-active` answer is rendered as a dialog that lists the running work by family and offers "stop and archive" (`stop=1`); a notice offers undo. Delete is new: a confirmation dialog over `session.delete`, owner only, which names what is removed (the log, its attachments, its side tabs and reading record) and that it cannot be undone. Reordering posts the complete order to A19's order routes from "move up", "move down" and "move to top" menu items. A control is drawn only where C1's `can` helper says its route would accept the person.

**Reading.** `completed turn` and `failed` are not stored by this area: they come from A06's list summary, which is folded from the log, so a turn that ended under a command-line run or before a restart is known. The record adds what the log must not hold: the last turn seen and the manual mark. A session is unread when the manual mark is set or the last completed turn is beyond `ReadTurn`. Acknowledgement needs no feature script: the fragment that ends a turn in the transcript carries a hidden form with `hx-put` to `session.reading` and `hx-trigger="intersect once"`, so the browser reports the turn when its end is drawn in a visible page. A session's history that exists when a person first lists it starts as read. Archived sessions read as read.

**The side panel.** A session page's URL names the selected side tab of each pane (`?side=<tab>&side2=<tab>`); without `side` the panel is collapsed. `Surface.Href` builds that URL, and C4's link helper uses it for local file links in settled messages. The stored `State` holds which tabs exist and where; a terminal's tab is not stored, it exists while A19's roster lists the terminal. Opening an address asks the registry which kind claims it, reveals a tab that already shows it, otherwise adds one to the active pane, at most `MaxTabs` per session. Closing the last tab of the only pane collapses the panel; a pane with no tab shows the start page, or directly the only card's kind. Below C2's narrow breakpoint the panel covers the viewport. Pane width and split ratio are host preferences per platform class, changed through C2's resize mechanic.

**Files.** The tree's root is the session's current directory from A06. A level is requested when its directory is opened (`files.toggle`), which also registers the `files` interest for that directory; closing it, or leaving the page, ends the interest, so a tree nobody looks at watches nothing. A level cut at A19's `MaxEntries` ends with a marker that says how many entries are shown and points to search. Search calls A19's `search` route with the query, the hidden toggle, the `.gitignore` choice and the exclusion list; these are host preferences keyed by workspace root, so every session and browser on that root shares them. Row actions: open in the side panel, open in a new window (pop-out), download (`file.raw`), copy path. There is no rename, delete or upload here.

**Preview.** `preview.show` picks a renderer by extension (band, then longest suffix) or by `as`. Built in:

| File type | How it is shown | Limits and failure |
|---|---|---|
| Plain text, code | Pages of lines from A19's `text` route, highlighted by C4's code renderer; "load more" appends; `line=` opens a window around a line | A19's line and byte bounds; `not-text` offers download |
| Markdown | Rendered whole by C4's renderer under the same rules as model output: sanitised, remote images click-to-load ([DH-ADR-22](../design.md#dh-adr-22)), local images as links to `file.raw`; a front-matter block is a key and value list | Above `MarkdownMaxBytes` the source is shown as text |
| PNG, JPEG, GIF, WebP, BMP, ICO, SVG | `<img src>` of `file.raw`; SVG never enters the page's DOM | The browser's own |
| PDF | The `<dsh-pdf>` island: pages drawn lazily in a Worker, selectable text, fit-width and fixed zoom steps, a page bitmap cap of 16,777,216 pixels | A load or parse failure shows the reason and "retry"; without script the fallback is a download link |
| `.docx`, `.doc`, `.pptx`, `.ppt` | Converted by `office.Service.PDF`, then the PDF island | See "LibreOffice" below |
| `.xlsx`, `.csv`, `.tsv` | A host-rendered table per worksheet: sheet tabs, saved formula results with the formula as a title, merged cells, frozen rows and columns as sticky cells, a notice naming charts, images and conditional formats that are not drawn | `SheetMaxBytes`, `SheetMaxCells`, `SheetTimeout`; over a bound the panel says "too large" before any allocation |
| `.xls` | `office.Service.Convert` to `.xlsx`, then the table ([DH-ADR-XLSX-1](../libs/xlsx.md#dh-adr-xlsx-1)) | See "LibreOffice" below |
| HTML | A separate document from `preview.html` in `<iframe sandbox>` with no permissions: links, refresh directives and scripts removed, policy sent as a header. With `HTML.Interactive` on: `sandbox="allow-scripts"` without same-origin access, and directly referenced relative `.js` and `.css` files packed into the document | 4 MiB per asset, 32 MiB in all, 64 assets |

**When LibreOffice is not installed.** The preview body is a plain panel: "This preview needs LibreOffice on the machine that runs `dsh`", the reason from `office.Service.Status()`, where the host looked, and a download link for the original file. Nothing is downloaded and no other converter is tried ([DH-ADR-20](../design.md#dh-adr-20)). The panel watches `office.StatusChanged`, so it becomes a preview once the program is installed, without a restart. A conversion that fails or times out shows its `office/...` code and "retry".

**A file that changes.** The `file` interest replaces a text, Markdown or image preview whole; the scroll position is kept by C1's per-tab store. For PDF, office, spreadsheet and HTML previews it replaces only a "this file changed, reload" control, so a reading position is not thrown away and a large file is not converted again unasked.

**Terminal.** The `terminal` kind's tabs are the roster of A19's `userterm` (the person's terminals and the agent's, the latter with an "Agent" mark). The start-page card opens the remembered shell; its menu lists A19's `shells` route. The body is an `<iframe>` of C1's frame document for the island, which carries the socket address, the size and input limits and the translated labels as attributes; the frame's status line and buttons are host-rendered, the island draws only the screen.

- **Socket.** One WebSocket per shown terminal, A19's `terminal.attach`: a snapshot first, then numbered output that the island acknowledges.
- **Resize.** A visible, writable island fits its pane, clamps to the host's limits and sends the size; a hidden or read-only island follows the host's size.
- **Reconnect.** When the socket closes unexpectedly the frame shows "disconnected", retries with C1's reconnect delays and draws the new snapshot. Keys pressed while disconnected are not sent.
- **A second viewer.** Every attachment sees the same screen. The latest attachment may type and resize; an earlier one becomes read-only, shows "Take control", and takes control by attaching again. No tab holds state the others lack.
- **Close.** Closing the tab asks first when the terminal is not idle, then calls A19's close route, which waits for process cleanup and reports a failure with "retry".

**Session panel.** One `<div popover>` per session page, opened by a header button with `popovertarget`; the browser closes it on Escape or an outside press and returns focus. A stylesheet container rule shows the same element docked in the transcript's end gutter when the gutter is wide enough and the stored "keep docked" preference is on. The Environment section shows the working folder, the execution world (this machine or an SSH host) and the sandbox mode.

**Directory picker and "open in".** The picker is a `<dialog>` over A19's `directory.browse`: a breadcrumb and an editable path, one level of directories, a prefix filter, a "show hidden" toggle (both query parameters, applied on the host), "New folder" and "Open". The host writes every path, so the page never joins or splits one. Picking a directory posts A19's `POST /workspaces`; `workspace/not-a-directory` and a path outside A18's configured `Roots` are shown beside the path. "Open in" is drawn in the session menu, the row menu and the files header only when A18's route exists for this request (a local owner, [host-telemetry.md](host-telemetry.md) `DH-ADR-HOST-9`); it lists A18's applications by category glyph and name and remembers the last choice as a preference. A success notice says "opened on the machine that runs `dsh`", because that machine may not be the one in front of the person.

**Config.** Go structs with defaults as Go values; unknown keys fail the load.

| Go struct | Fields and defaults | Replaces |
|---|---|---|
| `preview.Config` | `SheetMaxBytes` 16 MiB; `SheetMaxCells` 250,000; `SheetTimeout` 15 s; `PageRows` 200; `MarkdownMaxBytes` 2 MiB; `HTML{Interactive false (live), MaxAssetBytes 4 MiB, MaxTotalBytes 32 MiB, MaxAssets 64}` | `UP-CFG-dsh-client-ui-sidebar-documentpreview` (`excel.*`; the `office.*` cache fields have no counterpart here: the cache is A16's office service) |
| `nav.Config`, `side.Config` | `GroupRows` 5; `SearchResults` 20; `SearchDelay` 250 ms. `MaxTabs` 32; `MaxExpanded` 256 | new |

**Events, services, persisted data.** One live event, `reading.Changed` (notify: principal and session), raised after a record is committed or a foreign change is seen. `UP-SVC-sessionStatus` becomes `reading.Key` with `reading.Service`. Persisted: `kv` namespaces `reading` (key: principal and session) and `ui-side` (key: principal and session), both authoritative; host preferences `nav.view` (grouping, sort, archive filter, collapsed groups), `nav.hidden`, `side.hidden`, `side.size`, `files.search` (per workspace root), `sessionpanel.docked`, `openin.choice`, `term.shell`. In the browser: C1's per-tab store only (scroll positions, the PDF page and zoom). No session event. **Tools exposed to the model:** none. UI text is in catalogue namespaces `nav`, `side`, `files`, `preview`, `term`, `sessionpanel`, `dirpick`, `openin`, complete in English and Chinese. Shortcut commands (new session, search, add workspace, rename, fork, archive, toggle read, toggle side panel, new terminal, show files, close side tab, open locally) are registered with C1's `keys.Registry`; upstream's `browser.new` has no counterpart.

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership and access.** A kind, a renderer, a region and a route are effects of the fiber that registered them. A `reading` or `ui-side` record is *created* by this area and released when its session is deleted or by an explicit close; nothing else is this area's to release. A side tab has **access** to a file, a terminal or a converted PDF; it owns none of them: the file is the user's, the terminal belongs to A19's `userterm` record, a converted PDF to A16's cache, a watch to the browser tab's record in A19's hub. A directory made by "New folder" is the user's from the moment it exists; the harness never removes it. Registering a workspace gives the right to list a directory, not to delete it ([storage.md](storage.md) §6.3).

| Situation | What happens | Undone, retried, uncertain |
|---|---|---|
| A kind's or renderer's component unloads | Its tabs stay in the stored state and draw "not available"; they work again when it loads. Its routes answer 404 | Nothing is deleted |
| A side-state or reading write meets a busy store | 503 with `Retry-After`; the page keeps what it showed | The user repeats the action; the request id makes a repeat harmless |
| A mark is posted with an old revision | 409; the row is re-rendered with the current state | Nothing is overwritten |
| A session is deleted while its page or side tabs are open elsewhere | Its row leaves every list; its fragments answer 404; its records are deleted; A19 closes its terminals | **Uncertain:** nothing; deletion is A05's and is not undone |
| Archive with "stop" where a stop fails | The archive stands (A07); the notice names what could not be stopped | Undo unarchives; stopped work is not resumed |
| A directory level cannot be listed, or its watch ends | One line with the code (`not-found`, `outside-workspace`, `not-directory`); A19 falls back to polling, then to a "stale" marker | The user reloads the level |
| LibreOffice is missing, fails or times out | The panel of §6.2; no cache entry is written | **Uncertain:** what a failed conversion left in A16's run directory; A16 removes it |
| A spreadsheet exceeds a bound, or its package is invalid | "Too large" or "invalid" before cells are allocated; a deadline discards the result | Nothing partial is shown |
| An island's module fails to load | The fallback inside the element stays (a download link; "the terminal needs script"); the import is retried when the element next appears | Nothing |
| The terminal socket breaks, or the host restarts | The frame reconnects and draws a new snapshot. After a restart the roster is empty and the tab says the terminal ended | **Uncertain:** whether a key the host received reached the program |
| "New folder" fails | The typed error beside the field (`exists`, `invalid name`, `parent missing`) | Nothing was created |
| "Open in" cannot start the application | A notice with A18's reason | Not retried. **Uncertain:** whether a window opened on an unattended machine |
| Another process on the same home changes the list | A07's revision refresh raises the change within about a second and the regions are replaced | A session another process created appears when A06's summary for it is visible to this host ([§10](#10-open-questions-and-limitations), item 3) |

## 7. Decisions

<a id="dh-adr-uiside-1"></a>
### DH-ADR-UISIDE-1 The session list is rendered by the host from the list projection and the workspace ledger; every view option and order is host state
- **Decision.** The browser holds no catalog. Rows come from A06's list summary and A07's ledger, filtered by A19's access filter. Grouping, sort mode, archive filter and collapsed groups are host preferences; session order and workspace order are both stored by the host. Reordering is done with menu actions and forms. A page names its session in the URL, so there is no "current session" anywhere on the host. Blank reuse and the preset rule are applied by A19's create route. Recently viewed sessions are not kept loaded.
- **Why.** Upstream derives the list in each browser from a full copy, so two browsers can order it differently and a reload forgets how many rows a group showed. One host-rendered list is what [DH-O-12](../design.md#dh-o-12) asks for, and it lets a second browser tab or a second process see the same list.
- **Tradeoff.** Changing a view option is a request. Drag-and-drop reordering is not offered until C2 supplies a list primitive for it. Returning to a session renders its page again instead of showing retained history.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-5](../design.md#dh-o-5). **Verified by.** DH-T-UISIDE-1, DH-T-UISIDE-2, DH-T-UISIDE-3, [DH-E-UISIDE-1](#dh-e-uiside-1).

<a id="dh-adr-uiside-2"></a>
### DH-ADR-UISIDE-2 Reading state is a stored position per person combined with a completion folded from the log, and a turn is acknowledged when its end is drawn in a visible page
- **Decision.** As [§6.2](#62-contracts), "Reading". The record lives in A07's `kv`, keyed by principal and session, with a revision that an explicit mark must match. Completion and failure are read from A06's list summary. Acknowledgement is an htmx request triggered when the end-of-turn element intersects the viewport. Row status has one precedence: waiting, running, unread.
- **Why.** Upstream's in-memory records lose every reminder on a restart, belong to one process and let a delayed action clear a newer mark (`packages/client/ui-session-status/README.md:95-97`). Several processes share one home ([DH-ADR-12](../design.md#dh-adr-12)), so memory is the wrong place. The log already knows when a turn ended; only what a person has seen is new information, and it must stay out of the log because it is not something the model did.
- **Tradeoff.** "Seen" means the end of the turn was drawn in a visible page, not that the window had focus, which is slightly weaker than upstream's rule. Inference: a browser does not run intersection callbacks for a hidden page; [DH-E-UISIDE-1](#dh-e-uiside-1) must confirm it, and if it does not hold the trigger moves into a small island.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UISIDE-6, DH-T-UISIDE-7, [DH-E-UISIDE-1](#dh-e-uiside-1).

<a id="dh-adr-uiside-3"></a>
### DH-ADR-UISIDE-3 The side panel is a host-stored list of side tabs in at most two panes, selected by URL; there is no dock engine, no floating panel and no operation log
- **Decision.** As [§6.2](#62-contracts), "The side panel". Kinds register with `side.Surface` as effects and claim addresses by band and pattern. The tab set of a session is stored per person; the selection is in the URL. A side tab can be opened as a page of its own (pop-out), which replaces floating panels. Layout changes are whole-value forms.
- **Why.** Upstream's surface exists so that features share one place beside the conversation, and that need stays. Its engine (split tree, drag gestures, floats, a recorded sequence that no control exposes) is client JavaScript that [libs/go-ui-stack.md](../libs/go-ui-stack.md) §5.4 allows only as "small, or CSS only". Putting the selection in the URL gives linking, reload and Back for free and removes the DOM-retention rules that the Electron webview forced.
- **Tradeoff.** No free-form layout: two panes side by side, no drag between them, no undo. Switching side tabs is a request and redraws the body, so a body must restore itself from host state or the per-tab store. The panel still needs a session.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-1](../design.md#dh-o-1). **Verified by.** DH-T-UISIDE-8, DH-T-UISIDE-9, [DH-E-UISIDE-2](#dh-e-uiside-2).

<a id="dh-adr-uiside-4"></a>
### DH-ADR-UISIDE-4 The embedded web browser tab is not built, and nothing replaces it in the side panel
- **Decision.** No `browser` kind. A link in a message opens in the user's own browser as an ordinary new browser tab (C4's link helper). The side panel does not show the agent's browser either: a screenshot the agent takes is an image in its tool card, in the transcript.
- **Why.** The owner dropped the embedded browser pane ([DH-ADR-18](../design.md#dh-adr-18)). The web carrier upstream ships is an iframe that many sites refuse, that cannot report where it navigated, and whose popups can navigate the application (`packages/client/ui-sidebar-browser/README.md:104-106`). The page's policy allows frames of its own origin only ([DH-ADR-GOUISTACK-3](../libs/go-ui-stack.md#dh-adr-gouistack-3)), and the person already has a browser open. A live view of the agent's browser was considered and left out: A16's browser service offers screenshots to tools, not a stream to viewers, and every action is already logged with its image.
- **Tradeoff.** A page cannot sit beside the conversation inside the product; the user arranges two browser windows. A recorded exception to [DH-ADR-1](../design.md#dh-adr-1).
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-16](../design.md#dh-o-16). **Verified by.** DH-T-UISIDE-8 (no kind claims an `http` address); the ledger rows for the package and its seven limits link here.

<a id="dh-adr-uiside-5"></a>
### DH-ADR-UISIDE-5 Documents are previewed by host-side renderers; PDF is the one island; office files need the installed LibreOffice and say so when it is missing
- **Decision.** The renderer registry and the table of [§6.2](#62-contracts). Text, Markdown, images and spreadsheets are rendered by the host; PDF by the vendored pdf.js island; office documents are converted by A16's service and shown through the PDF island; HTML is a filtered document in a sandboxed frame, scripted only when configured. A changed file refreshes text and image previews and only flags the others.
- **Why.** It applies the library decisions ([DH-ADR-PDFJSDIST-1](../libs/pdfjs-dist.md#dh-adr-pdfjsdist-1), [DH-ADR-EXCELJS-1](../libs/exceljs.md#dh-adr-exceljs-1), [DH-ADR-FORTUNESHEET-1](../libs/fortune-sheet.md#dh-adr-fortunesheet-1), [DH-ADR-XLSX-1](../libs/xlsx.md#dh-adr-xlsx-1), [DH-ADR-DOMPURIFY-2](../libs/micromark.md#dh-adr-dompurify-2)) and [DH-ADR-20](../design.md#dh-adr-20), and adds the rules a viewer needs: one registry, one "unavailable" panel, one refresh rule. Rendering Markdown through C4's renderer means a previewed file gets exactly the protections of model output.
- **Tradeoff.** `.xls` previews now need LibreOffice. A table is not a grid: no cell selection model, approximate column widths. Markdown above a size bound is shown as source. A flagged preview needs a click to refresh. Scope boundary of this record: a preview never edits; changing a file is the agent's tools' job or the person's own editor's. Scope boundary of this record: a scripted HTML preview gets only the script and stylesheet files the page references directly, packed by the host, and has no bridge to the workspace, so the previewed page cannot read or list any other file, because a script in a previewed file is untrusted content and the sandboxed frame is the control that keeps it away from the person's files ([DH-ADR-DOMPURIFY-2](../libs/micromark.md#dh-adr-dompurify-2)). Scope boundary of this record, PDF sharpness: each page bitmap is capped at 16,777,216 pixels (`DH-LIB-PDFJSDIST-R4`), so a very large page, or a high zoom on a dense display, looks softer than the display could show; the cap is kept on purpose, not forced by the browser, because one such bitmap is already 64 MiB of memory in the tab and the cost grows with the square of the zoom, and drawing large pages in tiles would add a second drawing path to the one island for a side-panel preview whose full-quality route is "Download" into the person's own viewer.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10). **Verified by.** DH-T-UISIDE-12 to DH-T-UISIDE-15, [DH-E-UISIDE-2](#dh-e-uiside-2), [DH-E-UISIDE-5](#dh-e-uiside-5).

<a id="dh-adr-uiside-6"></a>
### DH-ADR-UISIDE-6 The files tree is drawn one level per request, its state belongs to the session on the host, and a directory is watched only while it is on a page
- **Decision.** Expansion is stored in `side.State` (capped); a level is fetched when opened; the `files` interest is registered by that request and ends with the browser tab's record. Search preferences are host preferences per workspace root. The hidden rule is the host's: a dot prefix or the Windows hidden attribute, for the tree and the search alike.
- **Why.** Upstream keeps tree state per pane because its dock unmounts unselected tabs, and it keeps the watch rule by hand. With state on the host the tree is the same in every browser tab and survives any redraw, and A19's interest lifetime gives "no watch for a hidden tree" without code here.
- **Tradeoff.** Opening a directory is a request. A directory larger than A19's entry cap is shown cut, with search as the way in. The view is read-only: writing files is the agent's job or the person's own tools'. Scope, recorded here (register section 5.2; recommended default, not confirmed by the owner): the files view has no search inside file contents (the agent's `grep` tool and the person's editor do that), no artifact filter, no drag-and-drop and no rename.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-10](../design.md#dh-o-10). **Verified by.** DH-T-UISIDE-10, DH-T-UISIDE-11, [DH-E-UISIDE-2](#dh-e-uiside-2).

<a id="dh-adr-uiside-7"></a>
### DH-ADR-UISIDE-7 Terminal tabs are the host's roster, drawn by a framed island; the newest viewer types; the host never opens a person's panel by itself
- **Decision.** As [§6.2](#62-contracts), "Terminal". The island is the `<dsh-terminal>` element of [libs/xtermxterm.md](../libs/xtermxterm.md) in C1's frame, because xterm.js injects styles. An agent's terminal appears in the roster and puts a mark on the side panel toggle; it does not expand the panel.
- **Why.** The host already keeps the screen (A09's emulator), so a terminal tab needs no browser state and a reload, a second window and a pop-out all work by attaching. Deriving tabs from the roster removes upstream's two failure cases: a terminal with no tab to reach it, and a tab whose process is gone. Expanding a panel from the host would change what every open browser tab shows.
- **Tradeoff.** A person must notice the mark to see an agent's terminal. Focus, shortcuts and resize cross a frame boundary. Colours a program set before a viewer attached are not recovered.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-10](../design.md#dh-o-10). **Verified by.** DH-T-UISIDE-16, [DH-E-UISIDE-3](#dh-e-uiside-3).

<a id="dh-adr-uiside-8"></a>
### DH-ADR-UISIDE-8 One server-rendered directory picker; the host opens workspace directories only; "open with" and "show file location" for files are not built
- **Decision.** The native chooser package is not built. The picker of [§6.2](#62-contracts) is the only way to choose a directory. "Open in" offers A18's applications for a session's directory when A18's route exists. A file is viewed in the side panel, popped out, downloaded or its path copied; it is never handed to a program on the host. "Reveal in file manager" has no provider: A18's `openpath` takes a workspace directory and never a file ([host-telemetry.md](host-telemetry.md), `DH-ADR-HOST-9`), and a browser cannot open a file manager. What the web UI offers instead, wherever another area links to a file: "Show in Files", which opens the Files side tab with the file's ancestors expanded and the row selected (every user); "Copy path"; "Download"; and, for a local owner only, "Open in" on the session's workspace directory with the file manager from A18's catalogue.
- **Why.** A chooser on the host's display assumes the person sits there, and it needs native code ([DH-ADR-18](../design.md#dh-adr-18), [DH-ADR-15](../design.md#dh-adr-15)). Opening an arbitrary file with its default handler runs it if it is a program, outside the tool pipeline; A18 refuses it for that reason, and this area follows A18.
- **Tradeoff.** A person at the host machine loses the system dialog with its bookmarks, and a file manager opened with the file selected: the nearest thing is the workspace directory opened in it. Two recorded exceptions to [DH-ADR-1](../design.md#dh-adr-1). Scope boundary of this record: the directory picker has a prefix filter and "New folder", and no search, multi-select, rename or delete; a directory is managed with the person's own tools.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7). **Verified by.** DH-T-UISIDE-17, DH-T-UISIDE-18, [DH-E-UISIDE-4](#dh-e-uiside-4).

<a id="dh-adr-uiside-9"></a>
### DH-ADR-UISIDE-9 The session panel and the More menus are regions with stored preferences, built from the browser's own popover and CSS
- **Decision.** The session panel is one popover element per session page that a stylesheet rule docks when there is room. "Room" means that the transcript keeps its minimum readable width (area C2's token `--frame-centre-min`, 400 px), not its default width, so the panel docks on laptop widths by narrowing the transcript and overlays only below that (register entry U-K2-7; recommended default, not confirmed by the owner; [DH-D-UIKIT-5](ui-kit.md#dh-d-uikit-5) owns the token and the proof); its sections, indicators and environment rows are regions. The navigation column and the start page each keep a stored list of hidden entries and draw a More menu with them, "Browse plugins" (when C7's page exists) and "Create your plugin" (when the creator preset exists).
- **Why.** Upstream needs a resize observer and Escape handling in script to tell docked from overlay; the popover attribute gives the overlay's behaviour and a container rule gives docking. Hidden entries kept in memory are lost on reload upstream; a preference is not.
- **Tradeoff.** On laptop widths the transcript narrows to its minimum readable width to make room for the docked panel; only below that width is the panel an overlay. Sections cannot be hidden or reordered. Scope boundary of this record: entries in the navigation column and the start page can be hidden but not reordered, and appear in the order the product declares, because a second stored order for a handful of links would have to be reconciled whenever a component that contributes an entry loads or unloads. Inference: overriding a closed popover's hidden state from a container rule works in each supported browser; DH-E-UISIDE-1 checks it, and the fallback is a docked panel that a preference alone controls.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UISIDE-18, [DH-E-UISIDE-1](#dh-e-uiside-1).

## 8. Deliverables

<a id="dh-d-uiside-1"></a>
### DH-D-UISIDE-1 A person sees every workspace and session, live, and can start a new session
- **Result and why it matters.** The navigation column lists projects and conversations with their status, in every browser tab at once, and "new session" works. Without it a second conversation cannot be reached.
- **How it is implemented.** `ui.nav` contributes to `shell.nav`: view functions for the column, a group and a row; the list and the search results are marked for area C2's `k-list` island, so Up and Down move between rows, Home and End jump, and Right and Left open and close a workspace group, while without script every row stays a native link reached by Tab (register entry U-K2-9); the grouping and sort rules of [§6.2](#62-contracts) as pure functions over the summary and ledger rows; regions registered with the `shell` state so that one changed session redraws one row. View options are declared preferences. The scrollbar hold and fade is the CSS transition upstream uses; the column has one scroller.
- **Components and files.** `internal/ui/nav`. **Depends on.** [DH-D-API-5](api.md#dh-d-api-5) (`session.list`, `session.create`), [DH-D-API-4](api.md#dh-d-api-4) (the `shell` state), [DH-D-VIEW-3](session-views.md#dh-d-view-3) (list summary), [DH-D-STORAGE-7](storage.md#dh-d-storage-7) (registry, order), [DH-D-UICORE-1](ui-core.md#dh-d-uicore-1), [DH-D-UICORE-5](ui-core.md#dh-d-uicore-5) and [DH-D-UICORE-7](ui-core.md#dh-d-uicore-7) (shell, preferences, catalogue), [DH-D-UIKIT-5](ui-kit.md#dh-d-uikit-5) (column layout, rail and drawer).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-5](../design.md#dh-o-5). **Priority.** P0. A list without the status marks of [DH-D-UISIDE-3](#dh-d-uiside-3) works before they exist. **Done when.** DH-T-UISIDE-1, DH-T-UISIDE-2, DH-T-UISIDE-3 and DH-T-UISIDE-19 pass and [DH-E-UISIDE-1](#dh-e-uiside-1) has passed.

<a id="dh-d-uiside-2"></a>
### DH-D-UISIDE-2 A person can find, rename, fork, pin, reorder, archive and delete sessions, and manage workspaces
- **Result and why it matters.** History stays usable as it grows, and a session can finally be removed.
- **How it is implemented.** Dialog and menu fragments over A19's routes; the 409 answer of an archive rendered as the stop dialog; the delete confirmation; the search result view with event links; move actions that post complete orders; every control gated by `can`.
- **Components and files.** `internal/ui/nav`. **Depends on.** [DH-D-UISIDE-1](#dh-d-uiside-1). [DH-D-API-5](api.md#dh-d-api-5) and [DH-D-API-6](api.md#dh-d-api-6) (rename, fork, delete, archive, pin, order and workspace routes, and a text filter on `session.list`), [DH-D-VIEW-6](session-views.md#dh-d-view-6) (`Search`), [DH-D-UIKIT-4](ui-kit.md#dh-d-uikit-4) (dialog and menu partials).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-5](../design.md#dh-o-5). **Priority.** P1. **Done when.** DH-T-UISIDE-4, DH-T-UISIDE-5 and DH-T-UISIDE-19 pass and [DH-E-UISIDE-1](#dh-e-uiside-1) has passed.

<a id="dh-d-uiside-3"></a>
### DH-D-UISIDE-3 Rows show who is waiting, what is running and what is unread, and reminders survive a restart
- **Result and why it matters.** A person can leave an agent working and see later, in any browser, that it finished or failed.
- **How it is implemented.** `reading.Service` over a `kv` table with a revision per record; `Status` composes A17's pending item, A02's status, A06's completion and the record; the `session.reading` route; the acknowledgement form in the end-of-turn fragment (placed by C4's turn-tail region); deletion of a record when its session is deleted; `reading.Changed`.
- **Components and files.** `internal/ui/reading`. **Depends on.** Nothing in this area. [DH-D-STORAGE-1](storage.md#dh-d-storage-1) (`kv`), [DH-D-VIEW-3](session-views.md#dh-d-view-3) (last completed turn and its outcome in the list summary), [DH-D-HUMAN-1](interaction-credentials.md#dh-d-human-1) (pending interactions per session), [DH-D-AGENT-1](agent-loop.md#dh-d-agent-1) (`agent.StatusChanged`), [DH-D-STORE-6](session-store.md#dh-d-store-6) (a session-deleted notice).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UISIDE-6, DH-T-UISIDE-7 and DH-T-UISIDE-19 pass and [DH-E-UISIDE-1](#dh-e-uiside-1) has passed.

<a id="dh-d-uiside-4"></a>
### DH-D-UISIDE-4 A side panel where features open tabs beside the conversation
- **Result and why it matters.** Files, previews and terminals have one place, reachable by a link, a card or a shortcut, the same in every browser.
- **How it is implemented.** `side.Surface`: the kind registry as effects, claim rules, the `ui-side` table, the strip and start-page views, the routes of [§6.2](#62-contracts), `Href`, the pop-out page, hidden start-page cards as a preference.
- **Components and files.** `internal/ui/side`. **Depends on.** Nothing in this area. [DH-D-UICORE-2](ui-core.md#dh-d-uicore-2) and [DH-D-UICORE-5](ui-core.md#dh-d-uicore-5) (`shell.aside`, preferences), [DH-D-UIKIT-6](ui-kit.md#dh-d-uikit-6) (the dock partial: pane resize, strip and narrow-viewport layout), [DH-D-STORAGE-1](storage.md#dh-d-storage-1) (`kv`), [DH-D-API-1](api.md#dh-d-api-1) and [DH-D-API-4](api.md#dh-d-api-4) (route registration, `view` events).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-1](../design.md#dh-o-1). **Priority.** P1. **Done when.** DH-T-UISIDE-8, DH-T-UISIDE-9 and DH-T-UISIDE-19 pass and [DH-E-UISIDE-2](#dh-e-uiside-2) has passed.

<a id="dh-d-uiside-5"></a>
### DH-D-UISIDE-5 A person can browse and search the files of a session's workspace
- **Result and why it matters.** The person sees what the agent sees, including on a remote execution world, and it stays current.
- **How it is implemented.** The `files` kind; level fragments from A19's `tree` route; `files.toggle`; the reveal of the current file's ancestors on the host; the search fragment with highlighted paths; preferences per root; row actions.
- **Components and files.** `internal/ui/files`. **Depends on.** [DH-D-UISIDE-4](#dh-d-uiside-4). [DH-D-API-9](api.md#dh-d-api-9) (the `tree` and `search` routes with a `hidden` flag per entry and literal exclusions, the `files` interest).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-10](../design.md#dh-o-10). **Priority.** P1. **Done when.** DH-T-UISIDE-10, DH-T-UISIDE-11 and DH-T-UISIDE-19 pass and [DH-E-UISIDE-2](#dh-e-uiside-2) has passed.

<a id="dh-d-uiside-6"></a>
### DH-D-UISIDE-6 A person can preview text, Markdown, images, PDF, spreadsheets, office documents and HTML files
- **Result and why it matters.** What the agent produced can be read without leaving the page or trusting the file.
- **How it is implemented.** `preview.Renderers` and the built-in renderers of [§6.2](#62-contracts); the spreadsheet reader with its bounds; the `<dsh-pdf>` island module beside the vendored pdf.js files, registered through `Shell.Island` with no extra policy; the office and HTML routes; the "unavailable" and "changed" panels.
- **Components and files.** `internal/ui/preview`, `internal/ui/preview/sheet`, island assets under C1's `ui/assets`. **Depends on.** [DH-D-UISIDE-4](#dh-d-uiside-4), `DH-LIB-PDFJSDIST`, `DH-LIB-EXCELJS`, `DH-LIB-PAPAPARSE`. [DH-D-API-9](api.md#dh-d-api-9) (`text`, `stat`, `file.raw` with ranges, the `file` interest), [DH-D-MEDIA-1](document-browser-computer.md#dh-d-media-1) (`office.Service`), [DH-D-UICONV-2](ui-conversation.md#dh-d-uiconv-2) (Markdown and code rendering). Runtime prerequisite: LibreOffice for office and `.xls` files only.
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10). **Priority.** P1, the interactive HTML mode included. **Done when.** DH-T-UISIDE-12, DH-T-UISIDE-13, DH-T-UISIDE-14, DH-T-UISIDE-15 and DH-T-UISIDE-19 pass and [DH-E-UISIDE-2](#dh-e-uiside-2) and [DH-E-UISIDE-5](#dh-e-uiside-5) have passed.

<a id="dh-d-uiside-7"></a>
### DH-D-UISIDE-7 A person has terminals beside the conversation that fit, reconnect and can be watched from two windows
- **Result and why it matters.** Manual work beside the agent, in the browser, without losing a running command to a reload.
- **How it is implemented.** The `terminal` kind over A19's roster; the frame document and status line; the island module around vendored xterm.js and its fit add-on; on every attach the island applies the `palette` section of A09's snapshot (the indexed, foreground, background and cursor colours a program set and has not reset) before it draws the screen, so a shell theme or an editor colour scheme survives a tab switch and a reload (register entry U-K2-8; [execution.md §6.2](execution.md)); the resize, reconnect and take-control rules of [§6.2](#62-contracts); the toggle mark for agent terminals.
- **Components and files.** `internal/ui/term`, island assets. **Depends on.** [DH-D-UISIDE-4](#dh-d-uiside-4), `DH-LIB-XTERMJS`. [DH-D-API-8](api.md#dh-d-api-8) (terminal routes, socket, `terminals` interest), [DH-D-EXEC-7](execution.md#dh-d-exec-7) (snapshot format), [DH-D-UICORE-4](ui-core.md#dh-d-uicore-4) (a framed island that receives the terminal's socket address in the `init` message, never in the frame URL).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-10](../design.md#dh-o-10). **Priority.** P1. **Done when.** DH-T-UISIDE-16 and DH-T-UISIDE-19 pass and [DH-E-UISIDE-3](#dh-e-uiside-3) has passed.

<a id="dh-d-uiside-8"></a>
### DH-D-UISIDE-8 A person can choose a directory of the host machine and add it as a workspace, from any browser
- **Result and why it matters.** The first thing a new user does, and it must work through a forwarded port or on a machine with no display.
- **How it is implemented.** The picker dialog over `directory.browse`, the create form, the add-workspace post and its errors, the reusable picker fragment that the new-session page and C3's composer can embed.
- **Components and files.** `internal/ui/dirpick`. **Depends on.** Nothing in this area. [DH-D-HOST-8](host-telemetry.md#dh-d-host-8) (`dirbrowse.Service`), [DH-D-API-6](api.md#dh-d-api-6) (`directory.browse`, a create route, `POST /workspaces`), [DH-D-STORAGE-7](storage.md#dh-d-storage-7) (workspace `Add`), [DH-D-UIKIT-4](ui-kit.md#dh-d-uikit-4) (dialog).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P0. **Done when.** DH-T-UISIDE-17 and DH-T-UISIDE-19 pass and [DH-E-UISIDE-4](#dh-e-uiside-4) has passed.

<a id="dh-d-uiside-9"></a>
### DH-D-UISIDE-9 Session facts, "open in" and entry choices have one place each
- **Result and why it matters.** A person sees where a session works and under which confinement, opens the project in an editor when they sit at the host, and hides entries they do not use.
- **How it is implemented.** The panel fragment, its regions, the Environment rows and the popover and docking rules; `ui.openin` contributing menu items that post to A18's route; the More menus over stored hidden lists; the "Create your plugin" action as a `session.create` with the creator preset and a draft text for C3.
- **Components and files.** `internal/ui/sessionpanel`, `internal/ui/openin`, `internal/ui/nav`, `internal/ui/side`. **Depends on.** [DH-D-UISIDE-1](#dh-d-uiside-1), [DH-D-UISIDE-4](#dh-d-uiside-4). [DH-D-HOST-9](host-telemetry.md#dh-d-host-9) (`openpath`), [DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1) (execution world and sandbox mode of a session), [DH-D-BUNDLE-4](bundle-preset.md#dh-d-bundle-4) (agent preset lookup), [DH-D-UICHAT-4](ui-chat.md#dh-d-uichat-4) (draft text on create). The link to area C7's plugins page is drawn only when that page is registered.
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7). **Priority.** P2. **Done when.** DH-T-UISIDE-18 and DH-T-UISIDE-19 pass and [DH-E-UISIDE-1](#dh-e-uiside-1) and [DH-E-UISIDE-4](#dh-e-uiside-4) have passed.

## 9. Acceptance

### 9.1 Local tests

Go tests with golden files, `net/http/httptest`, an HTML parser and fakes of other areas' services; no browser. `D-n` is short for `DH-D-UISIDE-n`. Nothing has been run.

| Test ID | Proves | What is asserted |
|---|---|---|
| DH-T-UISIDE-1 | D-1 | Golden lists for the three groupings, both sort modes, pinned rows first, the three archive filters; subagent sessions and unselected blanks absent; a group draws `GroupRows` rows plus every running, waiting, unread or open row; a row has identical bytes in a page and alone; the list and the search results carry the `k-list` marking and every row is a link |
| DH-T-UISIDE-2 | D-1 | A status change, a workspace change, a summary change and `reading.Changed` each replace only the affected regions; the baseline is complete; a non-owner's list holds only rows A19's filter admits |
| DH-T-UISIDE-3 | D-1 | The new-session form carries the workspace by the stated precedence, the preset and a request id; a stored view option is in the first response; a second principal has its own options |
| DH-T-UISIDE-4 | D-2 | Rename is prefilled; a 409 archive answer renders the stop dialog with the listed work, and `stop=1` is posted on confirm; delete is absent for a non-owner; a move action posts a complete order; controls follow `can` |
| DH-T-UISIDE-5 | D-2 | Search shows metadata then content matches, at most `SearchResults`; a content row links with `at`; an archived row offers unarchive; `query/search-disabled` still shows metadata; titles and snippets are escaped and masked |
| DH-T-UISIDE-6 | D-3 | Table of record transitions: a completion makes unread; `Seen` only moves forward and not past the completed turn; `Seen` keeps a manual mark; `Mark` with an old revision is `reading/stale`; a failed turn sets `Failed`; archived reads as read; first listing starts read; precedence waiting, running, unread |
| DH-T-UISIDE-7 | D-3 | A record survives closing and reopening the store; a write through a second connection is seen after the refresh; deleting the session deletes the record; two principals do not share a record |
| DH-T-UISIDE-8 | D-4 | A duplicate kind id fails; claims follow band, then pattern length, then registration order; no kind claims an `http` or `https` address; unloading a kind leaves its tabs as "not available"; `Open` reveals an open tab; the tab cap is enforced |
| DH-T-UISIDE-9 | D-4 | State is per principal and session; a layout with an unknown tab id or a third pane is refused; closing the last tab collapses; `?side=` selects; the pop-out page and the fragment have the same body bytes; a title follows the request's language |
| DH-T-UISIDE-10 | D-5 | A level is fetched only when opened; a cut level has the marker; opening registers the interest and closing removes it; the current file's ancestors are drawn open even when hidden entries are off; an exclusion rule with `[`, `*` or `!` is accepted as a literal |
| DH-T-UISIDE-11 | D-5 | Search preferences are shared by two sessions on one root and not by another root; a query containing `/` highlights the matched path; each failure code has its line; a session with no directory shows one line |
| DH-T-UISIDE-12 | D-6 | Renderer choice by band and longest suffix; a binary type with no renderer shows "unsupported" and reads nothing; text opens at `line`; Markdown output contains no remote `img` and links local images under the session's prefix |
| DH-T-UISIDE-13 | D-6 | A workbook fixture gives the golden table: merged cells, a hidden sheet absent, frozen rows, a formula's saved value with the formula as title, the notice for a chart; CSV keeps leading zeros; each bound gives "too large" with no cell read past it; a hostile sheet name and font name are escaped or dropped |
| DH-T-UISIDE-14 | D-6 | With a fake office service: available gives a PDF response and an island element pointing at it; `office/unavailable` gives the panel naming LibreOffice with a download link and no outbound call; `office.StatusChanged` replaces the panel; `.xls` calls `Convert`; a timeout shows its code |
| DH-T-UISIDE-15 | D-6 | `preview.html` sends the restrictive policy as a header and its document has no `href`, no refresh directive and no script; with `Interactive` on the policy allows scripts without same-origin and packs at most the configured assets; the main page's policy is unchanged |
| DH-T-UISIDE-16 | D-7 | The roster fragment lists user and agent terminals, an exited one with its code; the frame document carries the socket path, limits and labels and the frame policy; the element holds fallback text; closing a busy terminal renders the confirmation; the island's attach step applies the snapshot's `palette` section before the first draw (checked statically in the module and in [DH-E-UISIDE-3](#dh-e-uiside-3)) |
| DH-T-UISIDE-17 | D-8 | Breadcrumbs and paths come from the host for POSIX and Windows fixtures; the hidden toggle and the prefix filter act on the host; a cut level has the marker; create errors appear beside the field; a non-owner is refused; `workspace/not-a-directory` is shown |
| DH-T-UISIDE-18 | D-9 | The panel draws sections in order and omits an empty one; the Environment rows show folder, world and mode; "open in" is absent when A18's route is not registered; a hidden entry stays hidden in a new request and its page stays reachable; "Create your plugin" is absent without the preset |
| DH-T-UISIDE-19 | D-1 to D-9 | Every catalogue key of this area's namespaces exists in English and Chinese with the same placeholders; no template holds literal product text; this area's shortcut commands register without duplicates |

### 9.2 Real-environment scenarios

Each needs a real browser driving the built `dsh` binary; the replay model provider stands in for a model API. They prove the stated contracts for the tested build, browser and operating system.

<a id="dh-e-uiside-1"></a>
#### DH-E-UISIDE-1 Two browser tabs and a second process keep one session list
- **Proves.** [DH-D-UISIDE-1](#dh-d-uiside-1), [DH-D-UISIDE-2](#dh-d-uiside-2), [DH-D-UISIDE-3](#dh-d-uiside-3), [DH-D-UISIDE-9](#dh-d-uiside-9).
- **Given.** The built `dsh` in its web profile on loopback with a temporary harness home; a real Chromium with two tabs, A and B, admitted as the owner; two workspaces; a replayed session whose turn runs for some seconds and one whose turn fails.
- **When.** (1) In A, create a session and send the prompt; keep B on another session. (2) Wait for the turn to end with A hidden. (3) Show A. (4) In B, mark the session unread, rename it, pin it and move it down. (5) In A, archive a session that is running, cancel, then choose "stop and archive", then undo. (6) Run `dsh` from a shell on the same home to create a third session. (7) Stop and start the host. (8) Delete a session in B. (9) Open the session panel at a wide and a narrow window width.
- **Then.** With the keyboard alone, the arrow keys move through the rows of the list and of the search results, Home and End jump, and Right and Left open and close a group. After (1) B shows the new row as running without a reload. After (2) both tabs show it unread and no acknowledgement was sent; after (3) the mark clears in both. Each change of (4) appears in A; the failed session's mark is the failure mark. In (5) the dialog names the running turn, the plain archive changed nothing, and after undo the session is listed and its turn is recorded as cancelled by the user. After (6) the third session appears in both tabs. After (7) the unread and pinned states and the order are unchanged. After (8) the row is gone from A and its URL answers "not found". In (9) the panel is docked when wide and an overlay that Escape closes when narrow.
- **Cleanup.** Stop the host, delete the home and the browser profile. On failure keep the host log, both tabs' HTML and the database.

<a id="dh-e-uiside-2"></a>
#### DH-E-UISIDE-2 Files and previews in a real browser, with and without LibreOffice
- **Proves.** [DH-D-UISIDE-4](#dh-d-uiside-4), [DH-D-UISIDE-5](#dh-d-uiside-5), [DH-D-UISIDE-6](#dh-d-uiside-6).
- **Given.** The built binary; a real browser recording network requests and policy violations; a session on a workspace holding a directory of 3,000 files, a hidden file, a 20,000-line source file, a Markdown file with a remote image, a three-page PDF with text, an `.xlsx` with formulas and a chart, a `.docx` and an `.xls`. Run once on a machine with LibreOffice installed and once without.
- **When.** Open the files tree, the large directory and the hidden file by a link from a message; search by name and by a path with `/`; open each file; jump to line 15,000; select a word in the PDF and zoom to 400%; change the source file and the PDF with an external editor; pop a preview out into a second window; reload the page; use Back.
- **Then.** The large level is cut and says so; the hidden file's row is shown and marked. Line 15,000 opens without loading the lines before it. The PDF draws three pages, the word can be copied, no canvas exceeds the bitmap cap, and the Worker is gone after the tab closes. The spreadsheet shows saved values and names the chart as not drawn. With LibreOffice the `.docx` and `.xls` are shown; without it both show the panel naming LibreOffice, and no request leaves the host's origin in either run. The remote image is not requested before a click. The source preview refreshes by itself; the PDF shows "changed, reload". After the reload the same tabs are open at the same scroll position; Back returns to the previous side tab.
- **Cleanup.** Restore the files, stop the host, delete the home and the profile.

<a id="dh-e-uiside-3"></a>
#### DH-E-UISIDE-3 A terminal fits, reconnects and is shared by two windows
- **Proves.** [DH-D-UISIDE-7](#dh-d-uiside-7).
- **Given.** The built binary on Linux, macOS and Windows; a real browser with two windows on one session, recording policy violations; a session whose agent opens a terminal in one replayed turn.
- **When.** (1) Open a terminal in window 1 and start a full-screen program. (2) Resize the side panel. (3) Open the same terminal in window 2 and type. (4) Press "Take control" in window 1. (5) Cut and restore the network of window 1. (6) Run the turn in which the agent opens a terminal. (7) Close the busy terminal. (8) Restart the host.
- **Then.** No policy violation is recorded on the page or in the frame. A program that set its own colours before the tab switch and the reload shows the same colours after each. After (2) the program redraws at the new size. After (3) window 1 is read-only and shows the same screen; after (4) window 1 types and window 2 is read-only. After (5) window 1 shows "disconnected", then the current screen, and no key typed meanwhile reached the program. After (6) the roster shows the agent's terminal and the toggle carries a mark, and no panel opened by itself. (7) asks first and leaves no process. After (8) the tab says the terminal ended.
- **Cleanup.** Stop the host, confirm no child process remains, delete the home and the profile.

<a id="dh-e-uiside-4"></a>
#### DH-E-UISIDE-4 Choosing a directory and "open in", from the host machine and from another machine
- **Proves.** [DH-D-UISIDE-8](#dh-d-uiside-8), [DH-D-UISIDE-9](#dh-d-uiside-9).
- **Given.** The built binary on Linux, macOS and Windows with an editor from A18's catalog installed; a browser on the host machine; a second run reached from a browser on another machine by the admitted path of [api.md](api.md) `DH-ADR-API-6`; a fresh harness home.
- **When.** In each run: open the picker, go up to the roots level, show hidden directories, filter by a prefix, create a folder, pick it; type a path of a file; then look for "open in" in the session menu and, where present, choose the editor.
- **Then.** The folder exists on the host and becomes a workspace with a blank session. The file path is refused beside the field. On Windows the roots level lists drives and every path uses the host's separator. In the local run the editor opens the directory and the notice says so; in the remote run no "open in" item is drawn and its URL answers "not found".
- **Cleanup.** Remove the workspace, delete the created folder by hand, stop the host, delete the home.

<a id="dh-e-uiside-5"></a>
#### DH-E-UISIDE-5 A hostile file cannot leave its preview
- **Proves.** [DH-D-UISIDE-6](#dh-d-uiside-6).
- **Given.** The built binary; each supported browser recording policy violations and every request; a local recording server; a workspace with an HTML file that holds a script, a form, a refresh directive, a link to the recording server and an `hx-get` to a host route; an SVG with a script; a Markdown file and a spreadsheet whose text holds the same markup.
- **When.** Preview each file with the interactive mode off; turn the mode on and preview the HTML file again, with a relative script that tries to read a host route and the parent page.
- **Then.** With the mode off no script runs, no request reaches the recording server or any host route other than the preview's own, and the page outside the frame is unchanged. With the mode on the script runs inside the frame, cannot read the parent page, and its request to the host route is refused for lack of a credential. The markup in the Markdown file and the spreadsheet is shown as text.
- **Cleanup.** Stop the host and the recording server, delete the home and the profiles.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Assumed of [api.md](api.md) (requests sent).** (a) A text filter on `session.list` that calls A06's `Search`, and which component registers the `shell` regions: this document assumes the UI area does. Settled by api.md §6.2: creating one child directory is A19's owner-only route `POST /directories` (`directory.create`); tree entries carry `hidden`, and `search` takes literal exclusions and the hidden and ignore choices. (c) Still assumed: a paging cursor on `tree` for levels beyond the cap. (d) Whether "Take control" is a re-attach, as assumed here, or a message on the socket.
2. **What this area takes from C1 and C2, settled.** The terminal island's document gets its socket address in C1's `init` message, so the frame URL carries no id. Preferences per workspace root are C1's keyed preferences (`prefs.DeclareKeyed`). Copying a path is C2's `k-copy` island; layout, pane resize, menus, dialogs and arrow-key lists are C2's partials and kit islands. A file is never revealed in a file manager ([DH-ADR-UISIDE-8](#dh-adr-uiside-8) says what is offered instead).
3. **Settled, and still assumed.** Settled by their owners: the list summary holds the last completed turn and its reason ([session-views.md](session-views.md)); `Store.Changes()` signals a commit by another process, which is how a session that another `dsh` created or deleted appears in or leaves this host's list ([session-store.md](session-store.md)). Still assumed (requests sent): A10: the execution world's display name and the sandbox mode of a cold session. A17: the pending interaction kind per session without waking it.
4. **Inferences to confirm in a browser.** Intersection callbacks do not run in a hidden page ([DH-ADR-UISIDE-2](#dh-adr-uiside-2)). A container rule can dock a closed popover ([DH-ADR-UISIDE-9](#dh-adr-uiside-9)). pdf.js reads `file.raw` by byte range under the base policy; if its WebAssembly decoders are needed the island must be framed with one extra directive ([libs/go-ui-stack.md](../libs/go-ui-stack.md) §5.4), which is not designed here. A sandboxed frame's request to the host carries no cookie.
5. **Unmeasured.** The embedded size of pdf.js and xterm.js, the cost of rendering a 250,000-cell workbook as table pages, and the list's render cost with thousands of sessions.
6. **Limits kept.** Twenty-two upstream limits are carried on purpose ([§5](#5-tech-debt-and-its-resolution)). No drag-and-drop anywhere in this area. Code highlighting restarts at each page of lines, so a construct that spans a page boundary can be coloured wrongly. Terminals do not survive a host restart. The previews have had no security review.

## 11. Units dispositioned by this spec

Ledger fragment: `trace/fragments/C6.jsonl`.

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 14 | 2 ported, 9 replaced, 1 merged, 2 dropped |
| `UP-DEBT` | 76 | 50 resolved, 4 not applicable, 22 carried |
| `UP-CFG` | 1 | 1 ported |
| `UP-SVC` | 1 | 1 ported |
| `UP-DEC` (handed over) | 12 | 3 ported, 8 replaced, 1 dropped |

Go items declared: `DH-ADR-UISIDE-1` to `-9`, `DH-D-UISIDE-1` to `-9`, `DH-T-UISIDE-1` to `-19`, `DH-E-UISIDE-1` to `-5`.
