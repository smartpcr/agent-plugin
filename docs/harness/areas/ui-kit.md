# The UI kit (partials, design tokens, themes, layout, dock, context menu, shortcut help, icons and brand): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) (`2e39e70`) and are cited as `path:line` relative to the upstream root; in §3 to §5 a path that starts with `ui-` omits the leading `packages/client/`. Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `UIKIT`. Nothing here has been built, run, opened in a browser or tested.

| | |
|---|---|
| **Area key** | `UIKIT` (agent C2) |
| **Upstream packages** | 8, all under `packages/client`: `ui-primitives`, `ui-theme`, `ui-layout`, `ui-dockkit`, `ui-renderer`, `ui-brand-official`, `ui-context-menu`, `ui-shortcuts` |
| **Serves outcomes** | [DH-O-12](../design.md#dh-o-12) (the HTML UI), [DH-O-15](../design.md#dh-o-15) (quality is verifiable), [DH-O-14](../design.md#dh-o-14) (what the product ships), [DH-O-16](../design.md#dh-o-16) |
| **Depends on areas** | C1 ([ui-core.md](ui-core.md): view contract, page shell and landmarks, island table, preferences, shortcut commands, message catalogue; used as written), A19 ([api.md](api.md): routes, access), A18 (assets mount), A07 (`kv` behind C1's preferences), C4 (renderers of Markdown, code, diff and terminal output), C6 (the right panel that embeds the dock), A25 (web manifest and icons) |
| **Priority** | P0: partials, stylesheet, native widgets and kit islands, frame layout with resizing, icons and technology marks, the gallery. P1: appearance preferences with registered themes, the dock with tab dragging, context menu, shortcut help with recording. Each deliverable states its one priority in [§8](#8-deliverables) |

## 1. Purpose

Every page of the harness is built from the same small parts: a button, a field, a dialog, a menu, a card, a tab strip. This area defines those parts once, so that seven UI areas produce pages that look and behave alike, and so that a keyboard or screen-reader user meets the same behaviour everywhere. It also owns what is around the parts: the colours and sizes they draw with (design tokens), light and dark themes, how the page's regions are laid out and resized, the dockable side panel, right-click menus, the keyboard-shortcut help, icons, and the product's name and mark.

Upstream does this with React components. In a server-rendered UI ([DH-ADR-3](../design.md#dh-adr-3)) a component is an HTML fragment produced by a template, plus CSS, plus (rarely) a few lines of browser script. Without this area each feature would write its own dialog and its own focus handling, which is what a hand-rolled widget set gets wrong.

## 2. Vocabulary

Terms of [ui-core.md §2](ui-core.md#2-vocabulary) and [libs/go-ui-stack.md §1](../libs/go-ui-stack.md) (view, fragment, swap, island, region, page shell, host preference, command, chord) are used as written.

| Term | What it is | Why it exists |
|---|---|---|
| **Partial** | A named template that draws one reusable part from one typed Go value (its **parameter struct**), with its own stylesheet. | The Go form of a React component: typed input, HTML output, no state. |
| **Variant, state** | A variant is a fixed visual choice (`primary`, `ghost`). A state is a condition that changes over time (disabled, busy, pressed, selected, open). | Variants are attributes the stylesheet selects on; states are native or ARIA attributes, so assistive technology reads the same fact CSS draws. |
| **Design token** | A CSS custom property that names a design value. Three tiers: a **static** step (one raw colour), a **semantic alias** (what a colour is for, with a light and a dark value), a **component property** (one partial's own knob). | Features name a purpose, never a colour, so a theme changes everything at once. |
| **Scheme, theme** | The scheme is `light`, `dark` or `system` (follow the operating system). A theme is a named set of alias values and icon replacements laid over the built-in ones. | Appearance is a choice of the person, not of a feature. |
| **Cascade layer** | A CSS feature (`@layer`) that fixes which group of rules wins, whatever their order or specificity. | Feature CSS cannot override a partial by accident, with no build tool to enforce it. |
| **Top layer, modal dialog, popover** | The browser draws a `<dialog>` opened as modal, and an element with the `popover` attribute, above everything else (the top layer). A modal dialog also makes the rest of the page inert. | Stacking, focus trapping and Escape are done by the browser, not by our script. |
| **Roving focus** | In a composite widget (menu, tab strip) one item is in the Tab order and arrow keys move focus between items. | The keyboard pattern users of desktop software expect; it needs script. |
| **Kit island** | A small unframed island (a custom element) that *enhances* markup the host already rendered: its children work without it. | The line between "HTML is enough" and "script is needed" is one declared list. |
| **Frame, pane, divider** | The frame is the grid that places the shell's landmarks (navigation, main, side panel). A pane is one resizable column. A divider is the draggable, focusable line between two panes. | One layout for every page; widths survive a reload. |
| **Dock, strip, body cell, arrangement, intent** | The dock is the tabbed side panel. A strip is its row of tabs. A body cell is the stable element that holds one tab's content. The arrangement is `single` or `split` (two panes side by side). An intent is one settled user action (open, close, move, split, resize). | Content (a terminal, a preview) must not be re-created when the layout changes. |
| **Target, contribution, item** | A target is the thing a context menu is about (a session, a file). A contribution is one component's function that returns items for a kind of target. An item is one menu row and the request it makes. | Features add actions to menus they do not own. |
| **Sprite, symbol** | A sprite is one SVG file that holds many icons as `<symbol>` elements; a page draws one with `<use>`. | Icons are cached once, follow text colour and can be replaced by a theme. |
| **Gallery, golden file** | The gallery is a page that shows every partial in every variant and state. A golden file is the committed expected HTML of one rendering. | Consistency is checked by a tool, not by memory. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.client.ui-primitives` | `dsh-client-ui-primitives` | definition | The shared React components: controls, overlays, icons, Markdown and the output cards for tool results. No cordis, no slots; styled only through tokens (`ui-primitives/README.md:12`, `:173`). |
| `UP-PKG-packages.client.ui-theme` | `dsh-client-ui-theme` | provider | The token stylesheets, the light, dark or system preference, font sizes and font lists, the icon sprite, and `ctx.theme` for third-party themes (`ui-theme/README.md:12`). |
| `UP-PKG-packages.client.ui-layout` | `dsh-client-ui-layout` | provider | The three-column frame with drag handles, `ctx.layout`, and the presenter that writes the theme onto the document (`ui-layout/README.md:12`). |
| `UP-PKG-packages.client.ui-dockkit` | `dsh-client-ui-dockkit` | definition | A docking engine (a split tree of tabbed panes with invertible operations) and the React components that draw and drive it (`ui-dockkit/README.md:12`). |
| `UP-PKG-packages.client.ui-renderer` | `dsh-client-ui-renderer` | provider | Mounts the React application after every client plugin has loaded, binds slots to React and installs each plugin's stylesheets (`ui-renderer/README.md:12`, `:50-52`). |
| `UP-PKG-packages.client.ui-brand-official` | `dsh-client-ui-brand-official` | consumer | The official sidebar mark and name for an `official` build, and a click-triggered animated whale on the empty home page (`ui-brand-official/README.md:12`). |
| `UP-PKG-packages.client.ui-context-menu` | `dsh-client-ui-context-menu` | provider | `ctx.contextMenus`: typed targets, registered action providers, one shared menu (`ui-context-menu/README.md:12`). |
| `UP-PKG-packages.client.ui-shortcuts` | `dsh-client-ui-shortcuts` | consumer | The keyboard-shortcut reference dialog with search, and recording, clearing and restoring of bindings (`ui-shortcuts/README.md:12`). |

### 3.2 Behaviour

**Primitives.** One catalogue of about forty kinds of export is "the only place a control can be shared", and a deliberate visual difference becomes a prop, not a second copy (`ui-primitives/README.md:43-90`, `:99`). The components cannot read the locale, so every label is a required prop (`:139`). Overlays are hand-written: `Modal` is a portal with `role="dialog"` and its own Escape, Tab and focus-return stack (`ui-primitives/src/Modal.tsx:59-68`, `src/useModalLayer.ts:37-78`, `README.md:30-34`); `Menu` has data rows, submenus opened by hover with a pointer grace period, an arrow-key walk, and CSS anchor positioning (`README.md:54`, `:156-158`, `src/Menu.module.css:54-81`); `Tooltip` fits its bubble by script (`:107`); a document-wide tracker hides focus rings after pointer use (`:194`). The package also holds the Markdown renderer and the terminal, read, diff, search and web output cards (`:78`, `:84`, `:198-202`). A product icon is an `<svg>` with one `<use>` of a sprite symbol (`:105`).

**Theme and tokens.** Eight stylesheets define a static palette, semantic aliases for light (`body`) and dark (`body[data-ds-dark-theme]`), typography, shadows, focus and scrollbars (`ui-theme/README.md:70`; `ui-theme/src/styles/design-platform.css:4`, `:90`). `base.css` holds the radius scale (4, 8, 12, 16, 20, 28 px) and three motion durations (`ui-theme/src/styles/base.css:14-23`); the radius standard assigns a radius by role and size (`docs/ui-radius.md:30-37`). There is no spacing scale: a search of `ui-theme/src/styles` for `--dsw-space`, `--dsw-spacing` and `--dsw-gap` finds nothing. The user chooses light, dark or system, three font sizes (text 10 to 22 px, code 10 to 16, terminal 10 to 20) and three font lists (`src/theme-settings.ts:6`, `:57-61`; `README.md:34`, `:40`); the values are host settings (`src/index.ts:38-46`), and the host injects a synchronous script into the page so that the first paint uses them (`README.md:48`; `src/boot-theme.ts:39-42`). A composition may register a theme that overrides alias tokens and replaces icons (`README.md:44`). Rules are enforced by stylesheet tests: no literal colours in feature styles, hover only where the device hovers, elevated surfaces without borders, hairline borders (`docs/web-styling.md:18`, `:24`, `:26`, `:32`).

**Layout.** A script solves three column widths: the sidebar is 264 to 420 px (default 280), a 56 px rail when collapsed, a modal drawer below 768 px; the right panel opens at 45% of the frame, is at most 70% and at least 300 px, and closes before the centre falls under 400 px (`ui-layout/README.md:30`; `ui-layout/src/client/columns.ts:11-31`). Layout state resets on reload (`README.md:12`). Seats for the macOS and Windows window chrome exist only in the desktop application (`:43-47`).

**Renderer.** The first frame waits for the whole client plugin roster; then one call mounts React and installs stylesheets per module (`ui-renderer/README.md:12`, `:32`, `:50-52`; `ui-renderer/src/client/renderer.ts:31-36`).

**Dock kit.** The engine is a recursive split tree with floating panes, operations that return their own inverse, replay, and a linear history (`ui-dockkit/README.md:31-38`). The components report one settled intent per gesture, never a drag frame; the tab strip follows the WAI-ARIA tabs pattern with manual activation; a dragged tab can be reordered, moved to another pane, dropped on a pane's edge to split it, or released clear of the surface to float (`:40-44`, `:63`). Floating panels spring back inside the viewport (`:73`). **What the product actually uses is narrower.** The only embedder is the right sidebar, through `DockLayout`: one docked pane or two side by side with a minimum share of 0.2, plus floating panels (`ui-sidebar-right/src/client/shell/SidebarRight.tsx:356-360`; `ui-dockkit/README.md:69`; `ui-sidebar-right/README.md:41`). Each tab stays in a stable DOM cell across selection, moves and floating (`ui-dockkit/README.md:69`). The layout is saved per session in the browser's `localStorage`, and undo history is not offered to the user (`ui-sidebar-right/README.md:76`; `docs/subsystems/sidebar-right.md:161`). The recursive renderer `DockSurface` has no embedder outside the kit and its tests (search of `packages/client`).

**Context menu.** A feature registers a provider for a target kind (`session`, `file`, `directory`, `url`, `image`, `tab`; `ui-context-menu/src/client/types.ts:29-40`). On right-click, the ContextMenu key or Shift+F10 the target is captured, every provider resolves its actions under a timeout (1,000 ms, a live config field), and the menu appears once all have settled; a provider that timed out is left out with a visible notice (`ui-context-menu/README.md:28-36`, `:48`; `src/index.ts:10-12`). An action is plain data plus a callback that runs in the browser; a `commandId` only shows a shortcut hint (`:36-38`). An ellipsis button opens the same menu (`:32`).

**Shortcut help.** One dialog, opened from settings or by a chord, lists commands in a fixed order with search over label, English alias or keys; a row records a new binding from physical key positions, clears it or restores the default; overrides live in `localStorage` (`ui-shortcuts/README.md:30-38`).

**Brand.** An `official` build shows the DeepSeek mark and name in the sidebar; the home page plays a whale animation built from a frame atlas derived from a vendor video (`ui-brand-official/README.md:32-38`, `:54`). The mark is the fish path in `ui-primitives/src/FishLogo.tsx`, and brand text uses a bundled Montserrat font (`ui-theme/README.md:88`). `BRAND_GUIDELINES.md:7-10` allows a truthful description of a project's relationship, recommends "DSH" in names, and asks others not to use the full name as a project name or the official brand materials in a way that suggests endorsement.

### 3.3 Failure, retry, rollback and cleanup upstream

A stylesheet that fails to install is left out and reported once; the rest are installed (`ui-renderer/README.md:52`). A rejected theme write reloads the stored values (`ui-theme/README.md:34`). A saved dock layout that fails validation clears only that session's key, and a storage failure leaves the layout usable in memory (`ui-sidebar-right/README.md:76`). A menu provider that fails or times out is omitted with a toast; unloading a plugin withdraws its actions and cancels pending work (`ui-context-menu/README.md:28`, `:42-44`). A failed shortcut write keeps the draft for a retry (`ui-shortcuts/README.md:38`). A patched grammar that no longer matches makes its language render as plain text (`ui-primitives/README.md:250`). Nothing in this area persists to the session log.

## 4. Upstream decisions dissected

The first four rows are the decision notes handed to this area (files under `.agents/notes/implemented/`); the rest are visible only in code, READMEs and `docs/`. `UIKIT-n` is short for `DH-ADR-UIKIT-n` in [§7](#7-decisions). The ledger rows hold the full reasoning.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `architecture/2026-10-21-context-menu-contributions` | Actions are registered against typed targets and described by plain data, so a menu needs no contributor component | Keep the model, change the carrier | Plain data is exactly what a host can render; the browser callback becomes a request to a declared route | [UIKIT-5](#dh-adr-uikit-5) |
| `architecture/2026-10-21-replaceable-icon-sprite` | One sprite symbol per icon, drawn with `<use>`, replaceable by a theme, following text colour | Keep, extend | Plain SVG and CSS; the sprite becomes a cached file any document can reference, for every icon family | [UIKIT-6](#dh-adr-uikit-6) |
| `bug-fix/2026-10-10-background-shell-command-display` | A background shell call shows its command; after the launch is acknowledged the card is static, with no state dot or exit code | Keep | An acknowledgement says nothing about the job; the terminal partial takes the same two facts | [DH-D-UIKIT-1](#dh-d-uikit-1) |
| `feature/2026-10-21-floating-window-edge-recovery` | Floating dock panels resist at the viewport edge and spring back | Drop | There are no floating panels; a tab opens in a browser window instead | [UIKIT-4](#dh-adr-uikit-4) |
| Components know nothing of cordis or slots; one catalogue is the only way to share a control (`ui-primitives/README.md:43`, `:173`) | Presentation stays testable and uniform | Keep | A partial is a pure function of its parameter struct | [UIKIT-1](#dh-adr-uikit-1) |
| Every label is a prop supplied by the feature (`ui-primitives/README.md:139`) | The atoms cannot reach the locale | Change | A template can call the catalogue; a parameter says whether a text is a message key or data | [UIKIT-1](#dh-adr-uikit-1) |
| CSS Modules and `clsx`; no component library, no Tailwind (`docs/web-styling.md:17`) | Scoped class names from the bundler | Change | No bundler: plain CSS in cascade layers with a checked class prefix; still no component library | [UIKIT-2](#dh-adr-uikit-2) |
| Three token tiers; features use aliases only; tests reject literal colours (`docs/web-styling.md:9`, `:18`) | A theme must be able to change everything | Keep | The rule is independent of React | [UIKIT-2](#dh-adr-uikit-2) |
| Dark palette selected by an attribute that a boot script sets (`ui-theme/src/boot-theme.ts:42`) | No flash before plugins load | Change | The host renders the attribute; CSS `light-dark()` holds both values; no script | [UIKIT-2](#dh-adr-uikit-2), [UIKIT-8](#dh-adr-uikit-8) |
| Radius by role and size, six steps, smoothed corners where supported (`docs/ui-radius.md:30-37`, `:52`) | Consistent silhouettes | Keep | Pure CSS and already a written standard | [UIKIT-2](#dh-adr-uikit-2) |
| A committed list of radius exceptions (`docs/ui-radius.md:50`) | Some packages keep their own geometry | Drop | [DH-O-15](../design.md#dh-o-15) allows no baseline of exceptions; an exception is a component property with a stated inset | [UIKIT-2](#dh-adr-uikit-2) |
| Elevated surfaces have no border; one menu material; hairline borders; hover only where the device hovers (`docs/web-styling.md:24-32`) | Rules learned in design review | Keep | They are CSS rules a test can check | [UIKIT-2](#dh-adr-uikit-2) |
| macOS vibrancy backing behind menus, window-chrome seats, caption clearance (`docs/web-styling.md:31`; `ui-layout/README.md:43-47`) | Desktop window materials | Drop | No desktop application ([DH-ADR-18](../design.md#dh-adr-18)) | [UIKIT-2](#dh-adr-uikit-2) |
| Hand-written modal layer, menu walk and tooltip fitting (`ui-primitives/src/useModalLayer.ts:37-78`; `README.md:54`, `:107`) | No native element did the job when written | Change | `<dialog>` and `popover` trap focus, stack and dismiss natively; only roving focus needs script | [UIKIT-3](#dh-adr-uikit-3) |
| Submenus open on hover with a grace period (`ui-primitives/README.md:156`) | Desktop menu feel | Drop | It is the largest part of the menu script; submenus open on click and Right Arrow | [UIKIT-3](#dh-adr-uikit-3) |
| A script tracks input modality to hide focus rings (`ui-primitives/README.md:194`) | Rings after a click look wrong | Drop | `:focus-visible` is the browser's own answer | [UIKIT-3](#dh-adr-uikit-3) |
| Appearance is stored by the host, the same in every signed-in browser (`ui-theme/README.md:12`, `:104`) | One user, one choice | Keep, refine | Stored per principal as a host preference, with operator defaults in config | [UIKIT-8](#dh-adr-uikit-8) |
| Third-party themes override aliases and icons through `ctx.theme` (`ui-theme/README.md:44`) | Skins without forks | Keep, narrow | A compiled-in component registers a theme; it is validated; no browser plugin registers one | [UIKIT-8](#dh-adr-uikit-8) |
| Three columns solved by script; the right column concedes before the centre (`ui-layout/src/client/columns.ts:11-31`) | The conversation must stay readable | Keep the rule, change the mechanism | CSS grid with `minmax` and `clamp` states the same rule with no measuring | [DH-D-UIKIT-5](#dh-d-uikit-5) |
| A general docking engine with invertible operations and history (`ui-dockkit/README.md:31-38`) | Reusable beyond the first embedder | Drop the generality | The product uses two panes and no user-facing undo; a small pure planner covers that | [UIKIT-4](#dh-adr-uikit-4) |
| One settled intent per gesture; tab strip with manual activation; stable body cells (`ui-dockkit/README.md:40`, `:69`) | Each fixes a defect found in a browser | Keep | They are what makes a dock usable; all three survive server rendering | [UIKIT-4](#dh-adr-uikit-4) |
| Dock layout saved per session in the browser (`ui-sidebar-right/README.md:76`) | No host API for it | Change | Host state, the same in every tab ([DH-ADR-UICORE-5](ui-core.md#dh-adr-uicore-5)) | [UIKIT-4](#dh-adr-uikit-4) |
| Shortcut reference: one dialog, fixed order, subsequence search, inline recording (`ui-shortcuts/README.md:30-38`) | One place to learn and change keys | Keep | Rendered by the host; the device's overrides are merged by an island | [DH-D-UIKIT-8](#dh-d-uikit-8) |
| Brand is a package chosen by a build profile; the title is a build variable (`ui-brand-official/README.md:32`, `:89`) | Official and local builds differ | Change, and drop the assets | Identity is configuration; no upstream mark is carried | [UIKIT-7](#dh-adr-uikit-7) |
| The whole roster loads, then React mounts; styles are installed per module (`ui-renderer/README.md:12`, `:50`) | A client plugin runtime | Drop | No client runtime ([DH-ADR-UICORE-1](ui-core.md#dh-adr-uicore-1)); stylesheets are static files | [UIKIT-2](#dh-adr-uikit-2) |
| Known-site marks are a fixed list (`ui-primitives/README.md:243`) | Fetching a site's icon leaks the reader's activity | Keep | Already adopted by [libs/simple-icons.md](../libs/simple-icons.md) | `DH-LIB-SIMPLEICONS` |

## 5. Tech debt and its resolution

Forty units: 39 bullets from the packages' "Known Limitations" and one source marker. They are grouped by cause; each unit's own reasoning is in its ledger row (`trace/fragments/C2.jsonl`). `P:` is `UP-DEBT-limit.packages.client.ui-primitives.`, and the other prefixes are formed the same way.

| Group (cause) | Units | How the design answers it |
|---|---|---|
| **A. The React and plugin assembly** | `renderer.1`, `.2`, `.3`; `P:10`, `P:11` (5) | `renderer.1` resolved: a page is one complete response ([`DH-LIB-REACT-R1`](../libs/react.md)). The others are not applicable: per-module style installation, React Suspense and in-memory patches to TextMate grammars exist only because of the client runtime and the `@shikijs/langs` dependency. |
| **B. Limits of rendering text in the browser** | `P:2`, `P:4`, `P:5`, `P:6`, `P:7`, `P:8`, `P:9`, `P:17` (8) | Rendering moves to the host ([libs/micromark.md](../libs/micromark.md), [libs/diff.md](../libs/diff.md)). Resolved: `P:5` and `P:7` (a fence is coloured once, when it closes, and is plain above a size limit), `P:6` (one copy handler for the whole transcript), `P:9` (the URL rule is configuration with a test, not two patches). Carried, each by a library decision: `P:2` (a diff is bounded, now by input size), `P:4` (a late reference stays literal while streaming), `P:8` (very long lines stay uncoloured), `P:17` (command output is a stream of lines, not a screen). |
| **C. Copy, icons and artwork** | `P:1`, `P:3`, `P:12`, `P:13`, `P:14`, `P:15`, `P:16`, `theme.3`, the marker `ui-primitives/src/ZoomControls.module.css:15` (9) | Resolved: `P:16` (templates reach the catalogue), `P:14` and `theme.3` (every icon family is a sprite file any same-origin document can reference and a theme can replace), `P:12` (the mark is not carried), `P:13` (the gallery is the design source), `P:1` (a download route replaces the Chromium-only transfer), the marker (the style check rejects a literal shadow). Carried: `P:3` (a fixed list of site marks, on purpose) and `P:15` (five status marks, no sixth). |
| **D. Theme governance** | `theme.1`, `theme.2` (2) | Resolved: a registered theme is validated against the token table, and "tokens are the only colour authority" is kept as a rule with a check. |
| **E. Layout state and geometry** | `layout.1`, `.2`, `.3`, `.4` (4) | Resolved: widths are host preferences and the side panel is in the URL (`.1`); below the breakpoints a panel replaces the centre instead of squeezing it (`.2`); one CSS transition on the grid moves track and panel together (`.3`). Carried: `.4`, because keeping the reading line during a reflow is the browser's scroll anchoring, which this design cannot add where a browser lacks it. |
| **F. Dock kit** | `dockkit.1`, `.2`, `.3`, `.4` (4) | Resolved: one divider has nothing to cascade and each pane has a CSS minimum (`.1`); every dock action has a button, a menu item and a key, so nothing needs a drag (`.2`, `.3`); the divider is a focusable `separator`; class names, attributes and tokens are the published contract, shown in the gallery (`.4`). |
| **G. Context menu** | `context-menu.1`, `.2`, `.3` (3) | `.1` not applicable (there is no desktop window to give a native menu). `.2` resolved: only data items exist. `.3` carried: an open menu is page state by decision ([DH-ADR-REACT-1](../libs/react.md#dh-adr-react-1)). |
| **H. Shortcut help** | `shortcuts.1`, `.2` (2) | `.1` resolved: a recording draft lives in the island and is checked when saved, so a change of the command list does not discard it. `.2` carried: bindings are physical key positions shown with US key names, and the browser keeps its own chords ([DH-ADR-UICORE-6](ui-core.md#dh-adr-uicore-6)). |
| **I. Brand** | `brand-official.1`, `.2`, `.3` (3) | Resolved: identity is one configuration value, the canvas decoration is not built, and the page title comes from the same value at run time. |

Totals: 26 resolved, 5 not applicable, 9 carried. Every carried unit keeps a limit on purpose or because a browser or a library decision sets it; its justification is in its ledger row.

## 6. Go design

### 6.1 Shape

Packages are under `src/harness/internal/ui`, beside C1's ([ui-core.md §6.1](ui-core.md#61-shape)). C1 owns which elements a page has; this area owns how they look and move, and every reusable part inside them.

| Package | Component and service key | Why it exists |
|---|---|---|
| `ui/kit` | No component. Parameter structs, their `Validate` methods, the registered views, and the templates and stylesheets embedded beside them | The partial library every UI area imports |
| `ui/theme` | Component `ui.theme`; `theme.Key` (`Themes`) | Tokens, registered themes, the appearance preference, the stylesheet and head contributions |
| `ui/frame` | Component `ui.frame`, no service | The grid around C1's landmarks, pane widths, the divider |
| `ui/dock` | No component. A pure layout model and planner, the strip and cell partials | What area C6's right panel embeds; no clock, no I/O |
| `ui/menu` | Component `ui.menu`; `menu.Key` (`Registry`) | Context-menu contributions, resolution and the menu routes |
| `ui/keyhelp` | Component `ui.keyhelp`, no service | The shortcut reference and editing page over C1's `keys.Registry` |
| `ui/brand` | Component `ui.brand`; `brand.Key` (`Identity`) | The product's displayed name and mark |
| `ui/gallery` | Component `ui.gallery`, off by default | The gallery page, mounted in a development composition |
| `ui/assets/kit` | No component | Embedded sprites, kit island modules and their manifest entries, served by A18's assets mount |

```go
// package kit: a partial is a struct, a named template and a stylesheet.
type Text struct{ /* a message key with arguments, or data shown verbatim */ }
func Msg(k msg.Key, args ...any) Text // product text: looked up in the request's language when rendered
func Data(s string) Text              // a file name, a title, model text: never translated, always escaped
type Action struct {                  // what a control does; exactly one field is set
    Href   string   // an ordinary link
    Do     *Request // an htmx request: Route name, Params, Method, Target, Swap, Confirm (id of a confirm dialog)
    Submit bool     // submits the enclosing form
    Opens  string   // id of a popover (popovertarget); a dialog is opened by Do with Target "#dialogs"
}
type Button struct {
    ID      string        // required inside a swap target, so focus survives a swap
    Label   Text          // visible text; an icon-only button sets Name instead
    Name    Text          // accessible name when there is no visible label
    Icon    Icon          // a sprite symbol; the zero value draws none
    Variant ButtonVariant // Primary | Ghost | Outline | Toolbar | Danger
    Size    Size          // SM | MD
    Action  Action
    State   State         // Disabled (with Reason Text), Busy, Pressed *bool, Current
}
func (b Button) Validate() error // no name, an unknown variant, two actions: an error, and the view writes nothing
var Views = struct{ Button ui.View[Button]; Dialog ui.View[Dialog]; Menu ui.View[Menu] /* one per partial */ }{}

// package theme
type Appearance struct {             // one host preference per principal, declared with prefs.Declare
    Scheme   string                  // "system" | "light" | "dark"
    Theme    string                  // "" is the built-in theme
    TextPx, CodePx, TerminalPx int   // 10..22 (14), 10..16 (11), 10..20 (13)
    TextFonts, CodeFonts, TerminalFonts []string // family names placed before the built-in stack
}
type Theme struct{ ID string; Label msg.Key; Light, Dark map[Token]string; Icons fs.FS } // overrides only
type Themes interface {
    Register(owner *cordis.Context, t Theme) error      // an effect of owner; invalid input fails the caller
    Scheme(ctx context.Context, p access.Principal) string
    Stylesheet(ctx context.Context, p access.Principal) (css []byte, etag string)
}
// package menu
type Target struct{ Kind string; Session session.ID; Workspace, Ref string } // Ref: a path, an address, a tab id
type Item struct {
    ID string; Label kit.Text; Icon kit.Icon; Command string // Command: only shows that command's chord
    Danger bool; Disabled kit.Text                           // a non-empty Disabled is the reason shown
    Action kit.Action; Children []Item                       // one level of submenu
}
type Contribution struct {
    ID, Kind string; Order int
    Resolve func(ctx context.Context, p access.Principal, t Target) ([]Item, error)
}
type Registry interface {
    Register(owner *cordis.Context, c Contribution) error // an effect of owner; a duplicate ID for one Kind fails
    Kinds() []string
    Resolve(ctx context.Context, p access.Principal, t Target) (items []Item, omitted []string)
}
// package dock: pure functions over a value
type Layout struct{ Arrangement string; Ratio float64; Panes [2]Pane; Rev uint64 } // "single" | "split"
type Pane struct{ Tabs []Tab; Active string }
type Tab struct{ ID, Kind, Address string; Title kit.Text }
type Intent struct{ Op string; Tab Tab; Pane, Index int; Ratio float64; Rev uint64 } // open|close|focus|move|split|merge|resize
func Apply(l Layout, in Intent, lim Limits) (Layout, error) // an invalid or stale intent: an error, l unchanged
```

**How a partial takes parameters, variants and states.** A feature builds the parameter struct in Go (in its handler or view model) and its template calls `{{template "kit/button" .Save}}`; no template helper is added to C1's set. The struct's fields are the whole input. A variant is a typed constant rendered as `data-variant`; a size as `data-size`. A state is rendered as the attribute that means it: `disabled`, `aria-disabled` with a described reason, `aria-busy`, `aria-pressed`, `aria-current`, `aria-expanded`, `open`. CSS selects on those attributes, so what is drawn and what a screen reader hears cannot differ. `Validate` runs before rendering; a failed validation fails the view, which writes nothing ([`DH-LIB-GOUISTACK-R13`](../libs/go-ui-stack.md)). Standard labels (Close, Copy, Copied, More actions, Show more) come from the kit's own catalogue namespace `kit`, in English and Chinese; a feature's labels are `kit.Msg` keys and its data is `kit.Data`.

**The partial catalogue.** "Native" means HTML and CSS only; "htmx" means it makes a request; "island" names the kit island that enhances it.

| Family | Partials | Replaces (`ui-primitives`) | Behaviour comes from |
|---|---|---|---|
| Controls | `button`, `iconbutton`, `switch`, `checkbox`, `segmented`, `input`, `textarea`, `select`, `field` (label, help, error wiring), `pill`, `tag` | `Button`, `Switch`, `Checkbox`, `SegmentedControl`, `Input`, `InlineEditor`, `Pill`, `Tag`, `SettingsForm` fields | Native. `switch` is a checkbox with `role="switch"`; `segmented` is a radio group, which gives arrow keys and single choice for free. `textarea` with Enter-to-save uses `k-submit` |
| Feedback | `statedot`, `spinner`, `progress`, `toast`, `banner`, `skeleton`, `empty`, `shimmer` | `StateDot`, `Toast`, `ConnectionIndicator`, `TextShimmer` | Native. A toast is rendered into C1's `#notices` with `role="status"` (or `role="alert"` for an error); it fades by a CSS animation that pauses on hover and focus; an error toast stays until closed |
| Overlays | `dialog`, `confirm`, `popover`, `menu`, `tooltip`, `hovercard`, `lightbox` | `Modal`, `RiskConfirmation`, `Menu`, `MenuGroup`, `Tooltip`, `HoverCard`, `ImageLightbox` | See the widget table below |
| Structure | `tabs` (links), `tablist`, `disclosure`, `list`, `row`, `card`, `pathlabel`, `keys` (keycaps), `chip`, `filecard` | `SegmentedTabs`, `DisclosureRow`, `PathLabel`, `ShortcutKeys`, `ReferenceChip` | `tabs` and `disclosure` (`<details>`) native; `tablist` with `k-tabs` |
| Content frames | `codecard` (header, copy, wrap, fold), `terminal`, `diff`, `read`, `searchresults`, `webresult`, `jsontree`, `prose` | `CodeBlock`, `TerminalBlock`, `DiffBlock`, `ReadBlock`, `SearchBlock`, `WebBlock`, `JsonTree`, `MarkdownText` styles | Native: wrap is a checkbox and `:has()`; the head-and-tail fold and the JSON tree are `<details>`. Copy uses `k-copy`. The inner HTML comes from C4's renderers as `ui.SafeHTML` |
| Layout | `frame` (stylesheet only), `divider`, `dockstrip`, `dockcell` | `AppFrame`, the dock components | `k-split`, `k-dock` |
| Icons | `icon`, `fileicon`, `sitemark`, `artwork`, `brandmark` | `icons/*`, `FileTypeIcon`, `LinkIcon`, plugin and guide artwork | Native (`<use>` of a sprite file) |

The `terminal` partial takes `Background` and `Running` as upstream's card does: background with running means "the launch is in progress"; background without running draws the command only, with no state dot, exit code, empty-output line or accessible status.

**Interactive widgets: native element first.** This table is the contract for keyboard and screen-reader behaviour. C1's `focus` module opens a swapped-in `<dialog data-modal>` as a modal and returns focus after swaps ([ui-core.md §6.1](ui-core.md#61-shape)); everything else here is this area's.

| Widget | Built from | Script | Keyboard | What a screen reader is told |
|---|---|---|---|---|
| Navigable list (C6's session list and search results; register entry U-K2-9) | A list of native links, each focusable, with workspace groups as `<details>` | `k-list`, a further kit island: roving focus only | One row is in the Tab order; Up and Down move, Home and End jump, Right opens a group and Left closes it. Without the module every row is still reached by Tab | A list and its size; the focused row; a group's open state |
| Modal dialog, confirm, lightbox | `<dialog data-modal>` swapped into `#dialogs`; close buttons in a `<form method="dialog">` | None of ours | Focus moves to the element marked `autofocus` (a confirm marks its least destructive button), Tab stays inside, Escape closes the top-most dialog only, focus returns to the opener | Role `dialog` (`alertdialog` for a confirm), named by its title through `aria-labelledby`, described by its first paragraph; the page behind is inert |
| Popover (non-menu) | `popover` attribute and a button with `popovertarget`; CSS anchor positioning, centred where a browser lacks it | None | Enter or Space toggles; Escape or a click outside closes; focus stays on the button until the user tabs in | The button's expanded state; the popover is a `group` with a name |
| Menu, submenu | A popover holding `<ul role="menu">` of `<button role="menuitem">` | `k-menu` | Opening by keyboard focuses the first item. Up and Down move and wrap, Home and End jump, a letter moves to the next item starting with it, Right opens a submenu, Left or Escape returns, Enter or Space activates, Tab closes. Closing returns focus to the opener | `menu` with a name, `menuitem`, `aria-haspopup` on a parent row, `aria-disabled` plus a described reason on a blocked row, separators as `separator` |
| Context menu | The same menu, fetched from the host and shown at the pointer or below the focused element | `k-context` | Right-click, the ContextMenu key and Shift+F10 open it; text fields and elements with no target keep the browser's menu. Every target also has a visible "more actions" button | As a menu; the "more actions" button has a name that includes the target |
| Tabs that change the page | `<nav>` of links with `aria-current="page"` | None | Tab and Enter, as links | A navigation landmark with a name |
| Tab list (panels without their own address) | `role="tablist"`, `tab`, `tabpanel` | `k-tabs` | Only the selected tab is in the Tab order; Left and Right move and wrap, Home and End jump; Enter or Space selects (manual activation, as upstream) | `aria-selected`, `aria-controls`, the panel named by its tab |
| Tooltip | An element with `role="tooltip"` and `popover="manual"`, referenced by `aria-describedby` | `k-tip` | Shown on keyboard focus and on hover where the device hovers; Escape hides it without moving focus; it stays while the pointer is over it | The description is read with the control. A tooltip never carries the only name of a control |
| Pick list with filtering | A text input with `role="combobox"` and a `role="listbox"` the host re-renders | `k-combobox` plus htmx | Down opens, Up and Down move the active option, Enter chooses, Escape closes then clears | `aria-expanded`, `aria-activedescendant`, the number of results announced through `#announce` |
| Disclosure, fold, JSON tree | `<details>` and `<summary>` | None | Enter or Space toggles | Expanded or collapsed |
| Divider between panes | `<k-split>` holding an element with `role="separator"` and `tabindex="0"` | `k-split` | Left and Right move it by a step, Shift makes the step larger, Home and End go to the limits, Enter collapses or restores | `separator` with orientation, `aria-valuenow`, `aria-valuemin`, `aria-valuemax` and a name |
| Copy button | `<k-copy>` around a button | `k-copy` | Enter or Space | "Copied" is announced once through `#announce`; the button's name does not change |

**Kit islands.** Each is a custom element in its own small ES module, declared through C1's `Shell.Island`, unframed and with no policy beyond the base. Its children are complete server-rendered markup, so with the module missing the page still works: a menu is a list of buttons reached by Tab, a divider does not move, a context menu is reached through its "more actions" button. The list is closed: `k-menu`, `k-context`, `k-tabs`, `k-tip`, `k-combobox`, `k-copy`, `k-submit` (Enter saves, Shift+Enter breaks the line, nothing happens during input-method composition), `k-split`, `k-dock`, `k-keys` (shortcut help). An island holds no data, makes requests only through htmx or one same-origin `fetch`, positions an element only by setting custom properties through the style object, and tells the page something by a DOM event. Anything that needs keystroke-rate editing, a canvas, a device or third-party code is a feature island of its owning area (terminal, composer, PDF), not a kit island.

**Stylesheets.** Plain CSS files embedded beside their partials, concatenated in a fixed order at start-up into one content-hashed file that C1's head links first. The first line is `@layer reset, tokens, base, kit, feature, user;`.

| Layer | Holds | Rules a test enforces |
|---|---|---|
| `tokens` | Static steps (`--ui-static-*`), semantic aliases (`--ui-bg-*`, `--ui-label-*`, `--ui-border-*`, `--ui-state-*`, `--ui-link`), each alias one `light-dark(light, dark)` value; the scales | The only place a literal colour or a static step may appear; every alias has both values; declared text and surface pairs meet their contrast ratio |
| `base` | Reset, typography roles (size paired with line height), `:focus-visible` ring, scrollbars, reduced motion | No `:hover` outside `@media (hover: hover)`; motion only inside `@media (prefers-reduced-motion: no-preference)` |
| `kit` | One file per partial; classes start with `k-`; component properties start with `--k-` | A radius is a scale token, or a `calc()` from one; a full-round radius is paired with `corner-shape: round`; an elevated surface has `border: 0` and an elevation token; a neutral border is `0.5px` |
| `feature` | Each UI area's files, under its own registered class prefix | Aliases only; no selector on another prefix or on `[data-theme]`; no `!important` |
| `user` | The per-principal stylesheet: font sizes and lists, pane widths, a registered theme's overrides | Generated, never hand-written |

Scales. Radius is upstream's: 4, 8, 12, 16, 20 and 28 px, chosen by role (`docs/ui-radius.md:30-37`), with menus at 16 outside and 12 inside a 4 px inset (`:71`), and the superellipse curve inside `@supports`. Motion is upstream's three durations and one curve (`ui-theme/src/styles/base.css:14-17`). Inference: spacing is new, because upstream has no scale: 2, 4, 8, 12, 16, 20, 24, 32 and 48 px as `--ui-space-1` to `-9`, with the review rule that a gap inside a group is smaller than the gap between groups (`docs/ui-design-review-cases.md:57`). The scheme is the `data-theme` attribute C1 renders on `<html>` from `ui.Req.Theme`: `:root` declares `color-scheme: light dark`, and `[data-theme="light"]` or `[data-theme="dark"]` narrows it, so `light-dark()` picks a value with no script and no flash, and "system" is the browser's own behaviour.

**Frame.** C1's landmarks are placed by one grid on `<body>`: `#nav`, `#header` over `#main`, and `#aside`. The widths are the custom properties `--ui-nav-w` and `--ui-aside-w` from the `user` layer, clamped in CSS to upstream's limits (264 to 420 px; 300 px to 70%), and the centre is `minmax(400px, 1fr)`. Whether the navigation is collapsed to its 56 px rail is a host preference rendered as a checked checkbox that CSS reads with `:has()`, and that posts its change with `hx-put` to C1's `ui.pref`. From 768 to 1,023 px the navigation is the rail; below 768 px it is not a drawer but a page: the header's menu button is a link to the session list. When the frame is too narrow for a 400 px centre beside the side panel, the panel covers the centre (it is named in the URL, so its close control is a link). A `k-split` divider sits on each inner edge; it writes the property while dragged and saves once on release.

**Dock.** The side panel shows one pane of tabs, or two side by side. `dock.Apply` is the only code that computes a layout: at most two panes, a ratio between 0.2 and 0.8, a bounded number of tabs, no empty pane (a pane that loses its last tab merges away), a tab identified by kind and address so that opening the same thing focuses it. The embedder (C6) stores the `Layout` per session on the host, renders it, and sends every intent with the revision it was rendered at. Markup: each pane has a `dockstrip` (a tab list with manual activation, a close button per tab, an add control, a split control and a "more actions" menu with Close, Move to the other pane, Move left, Move right and Open in a new window), and all bodies are sibling `dockcell` elements that say which pane they belong to and whether they are hidden. `k-dock` applies a new layout to the existing cells, so a terminal or a preview is never re-created by a move; it also gives the strip its arrow keys, Delete to close and middle-click to close. Without the module every intent is a form that answers with a redirect.

**Context menus.** A view marks an element as a target by giving it the address of its menu (rendered with C1's `href`), so the target is captured when the page is rendered and cannot change under the pointer. `k-context` asks for that address; the route resolves the target, calls every contribution for its kind concurrently under `ResolveTimeout`, drops any item whose route `access.Allows` would refuse, and answers with one menu fragment. A contribution that fails, panics or times out is left out and named in a last, disabled row. An item's action is a request to a declared route, so the check that matters happens when the item is used, against current state. Hints for chords come from C1's `kbd` helper and are hidden for a coarse pointer by CSS.

**Shortcut help.** `GET /ui/shortcuts` answers a modal dialog that lists every command of C1's `keys.Registry` for the request's platform in registration group order, with its default chord, and marks fixed commands. The `k-keys` island calls the first-party script's exported `list`, `set` and `reset` ([DH-D-UICORE-6](ui-core.md#dh-d-uicore-6)): it shows the device's effective chords and an override count, filters rows by an ordered subsequence of label, alias or keys, and turns a row into a recorder that captures one chord from physical key codes, shows a refusal beside the row, and saves. Without the module the dialog is a read-only list of defaults.

**Brand.** `brand.Identity` gives every view the product's displayed name and the symbol of its mark; the page title, the navigation header, the "about" page and the web manifest (A25) all read it.

### 6.2 Contracts

**Routes this area registers** (through A19's `web.Routes`). The dock's routes are area C6's.

| Method and pattern (name) | Returns and rules |
|---|---|
| `GET /ui/appearance.css` (`ui.appearance`) | The `user` layer stylesheet of the calling principal, as bytes with an `ETag` and `Cache-Control: no-cache`. Access: global. Never wakes a session |
| `PUT /ui/prefs/{name}` (C1's `ui.pref`) | Used as written for the preferences `appearance` and `frame`. An appearance change answers with `HX-Refresh`, because the scheme attribute and the stylesheet link belong to the document |
| `GET /ui/shortcuts` (`ui.shortcuts`) | Fragment: the shortcut help dialog. Access: global |
| `GET /sessions/{sid}/menus/{kind}?ref=` (`menu.session`); `GET /workspaces/{wid}/menu` (`menu.workspace`) | Fragment: the resolved menu. `{kind}` is opaque and must be a registered kind (else 404); `ref` is a path, address or tab id inside that session. Access: the session or workspace. Activation `None` |
| `GET /dev/kit`, `GET /dev/kit/{partial}` (`ui.gallery`) | Page: the gallery. Exists only while `ui.gallery` is loaded. Owner |

**Event stream.** No new event name and no stream source. A toast is a fragment in A19's `notice` event or in a route's answer; the dock's strips are a `view` region of C6.

| Contract | Statement | Replaces |
|---|---|---|
| Config `theme.Config` | `Default theme.Appearance` (live): what a principal with no stored preference gets. Defaults `system`, 14, 11, 13, empty font lists | `UP-CFG-dsh-client-ui-theme` (its seven fields become the preference type `theme.Appearance`, validated by the `schema` package with the same ranges) |
| Config `menu.Config` | `ResolveTimeout` 1 s (live, at least 1 ms); `MaxItems` 64 per menu | `UP-CFG-dsh-client-ui-context-menu` (`resolutionTimeoutMs`) |
| Config `brand.Config` | `Name` (default `dsh`); `Mark` (default `mark-default`, a symbol in the artwork sprite) | The build variables `DSH_CLIENT_BUILD_PROFILE` and `DSH_CLIENT_TITLE` |
| Preference `appearance` | `theme.Appearance`, per principal, through C1's `prefs.Declare` | The `ui-theme` settings namespace |
| Preference `frame` | `{NavWidth, AsideWidth int; NavCollapsed bool}`, per principal. One value, not one per platform class: CSS applies a stored width only from 1,024 px upward and clamps it | Upstream's transient widths |
| Service `theme.Key` | `Themes`: replaces `ctx.theme` | |
| Service `menu.Key` | `Registry`: replaces `ctx.contextMenus`. `open` and `notify` have no counterpart: a gesture opens a menu, and a notice is a toast fragment | |
| Service `brand.Key` | `Identity{Name, Mark}` | The brand slots |
| none | `ctx.layout` (navigation is the URL, widths are preferences) and `ctx.uiRenderer` (C1's `shell.Key`) | |

**Persisted data.** The two preferences, in C1's `ui-prefs` namespace. Nothing in the browser of this area's own: a dialog, an open popover and an open menu are DOM state. No session event. **Tools exposed to the model:** none.

**The font lists** are the one place a user's text enters CSS. Each name is kept only if it consists of letters, digits, spaces, hyphens and underscores, at most 64 characters; at most eight names per role; every name except a CSS generic family is written double-quoted. Anything else is refused with 422; nothing is silently rewritten.

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership and access.** A registered theme, a menu contribution, a route, a head contribution and an island entry are effects of the fiber that registered them and leave with it. A parameter struct is a value; a partial owns nothing. The host owns the two preferences; a browser has access to them through its principal. The browser owns its open dialog, popover and menu, its focus and its clipboard; the host can read none of them. A menu item gives access to nothing: the route it calls decides.

| Situation | What happens | Undone, retried, uncertain |
|---|---|---|
| A parameter struct fails `Validate`, or a template errs | The view fails and writes nothing; the route answers 500 with a request id ([api.md §6.2](api.md#62-contracts)); inside a region C1 shows its "could not show this" placeholder | Nothing partial is drawn |
| A kit island's module fails to load | The server-rendered children stay and work at their fallback level; C1 retries the import when the element next appears | Nothing |
| A browser lacks a required feature (`popover`, `<dialog>`, `:has()`, `@layer`, `light-dark()`) | A banner that is always in the page is revealed by an `@supports` rule and names the minimum; the page is still a document | Nothing |
| A theme's registration is invalid (unknown token, a value that is not a colour, a sprite with a script, a style or a foreign id) | The registering component fails to load; the built-in theme is unaffected | Every effect of that load is released |
| The component that registered the active theme unloads | The built-in theme is drawn; the stored preference keeps the theme's id and applies again if it returns | Nothing is rewritten |
| An appearance or frame write fails or is refused | 422 beside the field, or a notice; the page keeps what it showed | The user retries; the previous value stands |
| A menu contribution fails, panics or exceeds the timeout | It is omitted and named in the menu; its context is cancelled; the failure is logged once per registration | Never retried inside one opening |
| The menu's target no longer exists, or the principal lost access | The route answers 404 or 403 and `k-context` shows the notice; an already open menu's item fails at its own route the same way | Nothing ran |
| A menu contributor unloads while its menu is open | The open menu is stale; its items answer 404 | The next opening is correct |
| A dock intent carries an old revision or is invalid | `Apply` returns an error; the embedder answers 409 with the current layout, and `k-dock` applies that | The user's gesture is dropped, never half applied |
| The clipboard is unavailable (not a secure context, permission denied) | `k-copy` selects the text and announces that it could not copy | Nothing |
| A divider drag is interrupted (pointer cancelled, tab hidden) | The property returns to the last saved width | Nothing is saved |

## 7. Decisions

<a id="dh-adr-uikit-1"></a>
### DH-ADR-UIKIT-1 A component is a typed partial: a parameter struct, a named template and a stylesheet
- **Decision.** Every reusable part is one partial in `ui/kit`. Its input is one Go struct with a `Validate` method; variants are typed constants; states are native or ARIA attributes; text is either a message key or data, by type. A feature composes partials by building structs and calling `{{template}}`; it never copies a partial's markup. A second area needing a part moves it into the kit. Scope boundary of this record: the status mark has exactly five states (done, warning, ongoing, error, idle) and no "active" state, and a sixth state is added only when a consumer needs one, because each variant is a typed constant that every view, its stylesheet and its accessible name must handle, and no view in the specification needs another.
- **Why.** `html/template` passes one value to a nested template and checks nothing at compile time ([DH-ADR-GOUISTACK-1](../libs/go-ui-stack.md#dh-adr-gouistack-1)); a struct gives call sites the compiler's help that React props gave upstream. Keeping upstream's "one catalogue" rule is what keeps seven areas consistent.
- **Tradeoff.** More Go per call site than a template call with loose arguments. A partial cannot fetch anything, so the feature gathers all data first.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-15](../design.md#dh-o-15). **Verified by.** DH-T-UIKIT-1, DH-T-UIKIT-2, DH-T-UIKIT-3.

<a id="dh-adr-uikit-2"></a>
### DH-ADR-UIKIT-2 Plain CSS in cascade layers, three tiers of tokens, both schemes in one value, rules enforced by a check
- **Decision.** The stylesheet architecture of [§6.1](#61-shape): six layers, a class prefix per owner, aliases as `light-dark()` pairs, upstream's radius and motion scales, a new spacing scale, and a stylesheet check that runs with the unit tests. There is no exception list: a deviation is a named component property. Desktop-only materials are not carried.
- **Why.** CSS Modules and their hashed names need a bundler, which the build does not have ([`DH-LIB-GOUISTACK-R3`](../libs/go-ui-stack.md)). Layers and a checked prefix give the same isolation. Upstream's rules are the residue of real design review and cost nothing to keep.
- **Tradeoff.** One stylesheet for every page, so a page downloads rules it does not use (once, then cached by its hash). A prefix is a convention until the check runs. `light-dark()` and `@layer` set a browser floor ([DH-ADR-UIKIT-3](#dh-adr-uikit-3)).
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-15](../design.md#dh-o-15). **Verified by.** DH-T-UIKIT-5, DH-T-UIKIT-6, DH-T-UIKIT-7, [DH-E-UIKIT-2](#dh-e-uikit-2).

<a id="dh-adr-uikit-3"></a>
### DH-ADR-UIKIT-3 Native elements first, a closed list of kit islands second, and a stated browser floor
- **Decision.** A widget is built from the native element that already has its behaviour (`<dialog>`, `popover`, `<details>`, radio and checkbox inputs, links). Script is added only for what no element does: roving focus, opening at the pointer, dragging a divider, the clipboard, recording a key. That script is a kit island from the closed list of [§6.1](#61-shape), and the widget table there is its contract. Submenus on hover, the input-modality tracker and scripted tooltip fitting are not built. The UI requires `<dialog>`, `popover`, `:has()`, `@layer` and `light-dark()`, and uses CSS anchor positioning, `corner-shape` and view transitions only as enhancements. This answers open question 7 of [libs/go-ui-stack.md](../libs/go-ui-stack.md) as a recommendation.
- **Why.** Focus trapping, stacking and dismissal are where hand-written widgets fail, and the browser now does them. C1 fixes the first-party script's jobs ([DH-ADR-UICORE-4](ui-core.md#dh-adr-uicore-4)), so new behaviour must be an island; making each one an enhancer keeps every page usable when a module fails.
- **Tradeoff.** Inference from general knowledge of browser releases, not checked here: the required set is in the Chromium, Firefox and Safari versions released since 2024, and older browsers get the banner. Where anchor positioning is missing a popover is centred, not attached. Ten small modules must be proven in a real browser, since nothing can run JavaScript locally ([ui-core.md §10](ui-core.md#10-open-questions-and-limitations), item 4). **Supported browsers, decided by the owner ([DH-ADR-39](../design.md#dh-adr-39)).** Chromium-family browsers are fully supported: every real-browser scenario of the UI areas runs there. Current Firefox and Safari are supported with a smaller automated set, the engine-sensitive scenarios ([DH-E-UIKIT-5](#dh-e-uikit-5): widgets, schemes, anchoring, sprites). The floor is releases since 2024; an older browser sees a banner that names the minimum. **Devices (register entry U-K2-10; recommended default, not confirmed by the owner).** Desktop browsers are the supported target. Narrow layouts are for narrow desktop windows; a phone may work and is not tested, so behaviour under an on-screen keyboard is outside the guarantee. **Command output (register section 5.2).** The frame that shows a command's output is not a terminal emulator: colours and styles are drawn, cursor addressing and screen switches are stripped, and an interactive program belongs in the terminal island.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UIKIT-4, DH-T-UIKIT-8, [DH-E-UIKIT-1](#dh-e-uikit-1), [DH-E-UIKIT-5](#dh-e-uikit-5).

<a id="dh-adr-uikit-4"></a>
### DH-ADR-UIKIT-4 The dock is one or two panes with a divider, planned on the host; no floating panels, no nested splits, no drag-to-dock
- **Decision.** Of the three candidates (a fixed set of layouts, a simple resizable split, a full dock island), the design takes the first two together: the arrangements `single` and `split`, a resizable divider, tabs moved by commands, and a small island that applies the host's layout to stable cells. The general engine, its history, floating panels and their edge recovery, and drop zones are not built. Dragging a tab within and between the two strips is a P2 enhancement of the same island and sends the same `move` intent.
- **Why.** Upstream's product already limits the kit to two side-by-side panes and offers no undo ([§3.2](#32-behaviour)). The kit's 4,500 lines mostly serve what the product does not use or what only a pointer can do. A dock that works from buttons, menus and keys is also the one that is accessible and works on touch, which upstream lists as unfinished.
- **Tradeoff. What the user loses.** (1) Floating a tab over the conversation; the substitute is "Open in a new window", a real browser window that counts against the stream limit of [DH-ADR-GOUISTACK-5](../libs/go-ui-stack.md#dh-adr-gouistack-5). (2) Dragging a tab onto a pane edge to split, with a preview; the split control and the move command do it in two steps. (3) Dragging to reorder, until P2. (4) Per-browser layouts: a session's layout is now the same in every tab, so two windows on one session change each other. Nested and top-and-bottom splits and layout undo were never offered by the product.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UIKIT-13, DH-T-UIKIT-14, [DH-E-UIKIT-3](#dh-e-uikit-3).

<a id="dh-adr-uikit-5"></a>
### DH-ADR-UIKIT-5 A context menu is resolved by the host from registered contributions, and an item is a request to a declared route
- **Decision.** The registry, target and item of [§6.1](#61-shape). Kinds are an open set of strings checked at registration. A menu is resolved once per opening, complete or timed out; it is not re-resolved while open. Items the principal could not use are not shown, and the route still decides when one is used. Component contributions do not exist. Scope boundary of this record: an open menu is view state of one browser tab, gone when the tab closes, and is never written to the session log or any store, because a menu changes nothing by being open and what an item does is recorded by the route it calls.
- **Why.** Upstream's note already separated a plain-data action from its presentation; the only part that cannot cross to a server-rendered page is the browser callback. A route is the Go form of "an action must recheck mutable resources before acting": it has an access rule, strict input and an idempotent request id ([DH-ADR-API-3](api.md#dh-adr-api-3)).
- **Tradeoff.** Opening a menu is a round trip, bounded by the timeout. Upstream's live re-resolution of an open menu is lost: a row that became invalid fails when used and says so. A plugin process can contribute only if area A22 offers a host service for it.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-1](../design.md#dh-o-1). **Verified by.** DH-T-UIKIT-15, DH-T-UIKIT-16, [DH-E-UIKIT-4](#dh-e-uikit-4).

<a id="dh-adr-uikit-6"></a>
### DH-ADR-UIKIT-6 Every icon is a symbol in an embedded sprite file, referenced by address
- **Decision.** Product glyphs, file-type glyphs, site marks, artwork and the brand mark are symbols in sprite files served from the assets mount. A page draws one with `<use href="<sprite address>#<symbol>">`. Names keep upstream's glyph and weight form. A theme may replace any symbol. Every sprite has a manifest entry with source, licence and hash. Scope boundary of this record: site marks are a fixed list of known sites shipped in a sprite; a site not on the list gets a generic mark, because fetching a site's own icon would tell that site what the reader is looking at ([DH-ADR-9](../design.md#dh-adr-9)).
- **Why.** Upstream's decision holds: a symbol follows `currentColor` and tokens and can be multi-colour. An address instead of an inline sprite removes upstream's two limits (an icon in a frame renders nothing; only product icons are replaceable) and keeps icons out of every page's bytes. Inference: C1's shell sets a `<base>` element, under which a bare `#symbol` reference would resolve against the base address and not the document, so a full address is also the safe form.
- **Tradeoff.** Inference, to be confirmed in a browser: a same-origin `<use>` reference is allowed by the base policy in each supported engine. If one refuses it, the fallback is the sprite inlined by the shell.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UIKIT-18, [DH-E-UIKIT-2](#dh-e-uikit-2).

<a id="dh-adr-uikit-7"></a>
### DH-ADR-UIKIT-7 No upstream brand asset is carried; the product's identity is configuration with a neutral default
- **Decision.** The Go project is not upstream's product, and its UI must not look as if DeepSeek made or endorsed it. Not carried: the fish mark (`FishLogo` and its path data), the `BrandWordmark` name artwork, the animated hero fish, the home-page whale, its frame atlas and code slogans, the Montserrat brand font, the onboarding brand gradients, upstream's favicons and application icons, the `official` build profile, and "DeepSeek Harness" as a displayed product name. Carried under upstream's MIT licence with a recorded notice: the generic product glyphs. Technology logos for file types are third parties' marks; they are P2 and come from the recorded simple-icons copy ([libs/simple-icons.md](../libs/simple-icons.md)), not from upstream's redraws. The default name is `dsh`, the default mark is a neutral shape drawn for this project, and an operator sets both in `brand.Config`. The "about" page may state the relationship truthfully in words.
- **Why.** `BRAND_GUIDELINES.md:7-10` permits a truthful description and the "DSH" abbreviation, and asks that the full name not be a project name and that official materials not suggest endorsement. A click-to-play decoration is also not a function of the product.
- **Tradeoff.** The UI has no recognisable identity until the project draws one. A recorded exception to [DH-ADR-1](../design.md#dh-adr-1) for the whale decoration. Recommended default, not confirmed by the owner: the displayed name is `dsh` with a neutral mark ([§10](#10-open-questions-and-limitations), item 3).
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-16](../design.md#dh-o-16). **Verified by.** DH-T-UIKIT-18, DH-T-UIKIT-19.

<a id="dh-adr-uikit-8"></a>
### DH-ADR-UIKIT-8 Appearance is a host preference per principal, drawn by an attribute and one generated stylesheet
- **Decision.** Scheme, theme, font sizes and font lists are one preference of the principal, with operator defaults in `theme.Config`. The scheme reaches the page as the `data-theme` attribute; everything else as the `user` layer stylesheet, which also carries pane widths. A theme is registered by a compiled-in component and validated: token names must exist, values must parse as colours, icon replacements must be well-formed symbols with prefixed ids and no script or style. Changing appearance reloads the tab that changed it; other open tabs follow at their next page load.
- **Why.** The first response must already be right, with no script ([DH-ADR-GOUISTACK-8](../libs/go-ui-stack.md#dh-adr-gouistack-8)) and no inline style ([DH-ADR-GOUISTACK-3](../libs/go-ui-stack.md#dh-adr-gouistack-3)). Per principal, not host-wide as upstream, because A19's settings routes are owner-only and a non-owner should still be able to read in the dark.
- **Tradeoff.** Upstream updates every open browser live; here another tab changes on its next load. A second device of the same owner shares the explicit choice (with `system`, each device still follows its own operating system).
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-2](../design.md#dh-o-2). **Verified by.** DH-T-UIKIT-9, DH-T-UIKIT-10, DH-T-UIKIT-11, [DH-E-UIKIT-2](#dh-e-uikit-2).

## 8. Deliverables

<a id="dh-d-uikit-1"></a>
### DH-D-UIKIT-1 Every page is built from one library of typed partials
- **Result and why it matters.** A feature author gets a button, a field, a dialog, a card or a code frame by filling a struct, and cannot produce one without a name, with an unknown variant or with unescaped text.
- **How it is implemented.** One file trio per partial (struct with `Validate`, template, stylesheet) registered with C1's `ui.Register`. `kit.Text` separates message keys from data. Content frames take C4's `ui.SafeHTML` and add the header, copy, wrap and fold. The catalogue of [§6.1](#61-shape) is built family by family: controls, feedback, structure and content frames first.
- **Components and files.** `internal/ui/kit`. **Depends on.** `DH-LIB-GOUITEMPLATES`. [DH-D-UICORE-1](ui-core.md#dh-d-uicore-1) (`ui.View`, `ui.Req`, the helper set), [DH-D-UICORE-7](ui-core.md#dh-d-uicore-7) (the `kit` catalogue namespace). The `kit` layer holds one rule per class of the fixed vocabulary for rendered code, diff rows and terminal colours that area C4's renderer emits.
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-15](../design.md#dh-o-15). **Priority.** P0. **Done when.** DH-T-UIKIT-1, DH-T-UIKIT-2, DH-T-UIKIT-3, DH-T-UIKIT-4, DH-T-UIKIT-20 and DH-T-UIKIT-21 pass and [DH-E-UIKIT-1](#dh-e-uikit-1) has passed.

<a id="dh-d-uikit-2"></a>
### DH-D-UIKIT-2 One stylesheet with tokens, layers and scales, and a check that keeps it honest
- **Result and why it matters.** Every surface draws from the same colours, radii, spacing and motion in light and dark, and a rule broken in one feature fails a test instead of reaching a user.
- **How it is implemented.** Embedded CSS files are concatenated in layer order at start-up and published under a content hash; the `theme` component contributes the link to C1's `shell.head`. The check parses every stylesheet (a small own tokenizer for declarations and selectors; no CSS library is named) and applies the rules of the layer table; contrast is computed from the token values.
- **Components and files.** `internal/ui/theme` (tokens, assembly), `internal/ui/kit` (kit layer), the check in `internal/testkit`. **Depends on.** `DH-LIB-SERVESTATIC` (content-hashed, long-cached files), [DH-D-HOST-1](host-telemetry.md#dh-d-host-1) (assets), [DH-D-UICORE-1](ui-core.md#dh-d-uicore-1) (the `shell.head` region, which takes `<link>` elements only).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-15](../design.md#dh-o-15). **Priority.** P0. **Done when.** DH-T-UIKIT-5, DH-T-UIKIT-6 and DH-T-UIKIT-7 pass and [DH-E-UIKIT-2](#dh-e-uikit-2) and [DH-E-UIKIT-5](#dh-e-uikit-5) have passed.

<a id="dh-d-uikit-3"></a>
### DH-D-UIKIT-3 A person chooses light or dark, text sizes and fonts, and the first paint already shows the choice
- **Result and why it matters.** No flash of the wrong theme, no script needed, and the choice follows the person.
- **How it is implemented.** `theme` declares the `appearance` preference, answers `Scheme` for C1's `Shell.Env`, generates the `user` layer from the preference and the active theme, and contributes an "Appearance" section (scheme choice, three steppers, three font fields) to the settings page. Registered themes are validated and merged over the built-in values.
- **Components and files.** `internal/ui/theme`. **Depends on.** [DH-D-UIKIT-2](#dh-d-uikit-2), [DH-D-API-1](api.md#dh-d-api-1). [DH-D-UICORE-5](ui-core.md#dh-d-uicore-5) (`prefs.Declare`, `ui.pref`; `Shell.Env` reads the scheme from `theme.Key`). [DH-D-UISET-2](ui-settings-plugins.md#dh-d-uiset-2) (the region `settings.general.rows`, where the Appearance rows go).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-2](../design.md#dh-o-2). **Priority.** P1: the preference, fonts and registered themes together. Until it is built a page follows the operating system's scheme through `light-dark()` in the stylesheet of [DH-D-UIKIT-2](#dh-d-uikit-2). **Done when.** DH-T-UIKIT-9, DH-T-UIKIT-10 and DH-T-UIKIT-11 pass and [DH-E-UIKIT-2](#dh-e-uikit-2) has passed.

<a id="dh-d-uikit-4"></a>
### DH-D-UIKIT-4 Dialogs, menus, tabs, tooltips and pick lists work from the keyboard and are announced correctly
- **Result and why it matters.** The behaviour in the widget table of [§6.1](#61-shape) holds on every page, because it is written once.
- **How it is implemented.** The overlay partials emit the native elements and ARIA attributes of the table. The seven widget islands (`k-menu`, `k-context`, `k-tabs`, `k-tip`, `k-combobox`, `k-copy`, `k-submit`) and the helper module they import are declared through `Shell.Island`; each is held under a byte limit by a test.
- **Components and files.** `internal/ui/kit` (partials), `internal/ui/assets/kit` (modules). **Depends on.** [DH-D-UIKIT-1](#dh-d-uikit-1), `DH-LIB-GOUIHTMX`. [DH-D-UICORE-4](ui-core.md#dh-d-uicore-4) (`Shell.Island`), [DH-D-UICORE-6](ui-core.md#dh-d-uicore-6) (the `focus` module's handling of `<dialog data-modal>`, `#dialogs` and `#announce`).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P0, the native widgets and the kit islands together. **Done when.** DH-T-UIKIT-4 and DH-T-UIKIT-8 pass and [DH-E-UIKIT-1](#dh-e-uikit-1) and [DH-E-UIKIT-5](#dh-e-uikit-5) have passed.

<a id="dh-d-uikit-5"></a>
### DH-D-UIKIT-5 The page's regions keep a readable centre at every width, and a pane width survives a reload
- **Result and why it matters.** The conversation is never squeezed below 400 px, and a person sets a width once.
- **How it is implemented.** The frame stylesheet of [§6.1](#61-shape) on C1's landmarks; the `frame` preference; the `k-split` island (pointer capture, keyboard steps, one save on release). The width tokens decide when C6's session panel docks (register entry U-K2-7; recommended default, not confirmed by the owner): "room" is measured against the transcript's minimum readable width, the token `--frame-centre-min` (400 px), not against its default width. The container rule docks the panel whenever the centre would keep that minimum, narrowing the transcript, and leaves it an overlay only below that, so on laptop widths the panel sits beside the conversation.
- **Components and files.** `internal/ui/frame`, `internal/ui/assets/kit` (`split`). **Depends on.** [DH-D-UIKIT-2](#dh-d-uikit-2), [DH-D-UIKIT-3](#dh-d-uikit-3) (the `user` layer). [DH-D-UICORE-1](ui-core.md#dh-d-uicore-1) (the landmark ids), [DH-D-UICORE-5](ui-core.md#dh-d-uicore-5) (`ui.pref`; a stored size is clamped to the viewport and ignored by the narrow layout).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P0, the grid, the breakpoints and resizing together. **Done when.** DH-T-UIKIT-12 passes and [DH-E-UIKIT-3](#dh-e-uikit-3) has passed.

<a id="dh-d-uikit-6"></a>
### DH-D-UIKIT-6 The side panel holds tabs in one or two panes, and moving a tab never restarts its content
- **Result and why it matters.** A file preview and a terminal can sit side by side, and a running terminal keeps its connection when the layout changes.
- **How it is implemented.** The pure `dock` package (`Apply`, limits, settle rules), the `dockstrip` and `dockcell` partials, and the `k-dock` island that applies a layout fragment to existing cells and sends intents with their revision. Tab dragging is added later inside the same island.
- **Components and files.** `internal/ui/dock`, `internal/ui/assets/kit` (`dock`). **Depends on.** [DH-D-UIKIT-4](#dh-d-uikit-4), [DH-D-UIKIT-5](#dh-d-uikit-5). Area C6 supplies the routes, the stored layout per session, the tab kinds and their bodies.
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P1, dragging a tab included. **Done when.** DH-T-UIKIT-13 and DH-T-UIKIT-14 pass and [DH-E-UIKIT-3](#dh-e-uikit-3) has passed.

<a id="dh-d-uikit-7"></a>
### DH-D-UIKIT-7 A right-click or the menu key shows the actions every loaded feature offers for that thing
- **Result and why it matters.** Features add actions for sessions, files and links without knowing each other, and an action a person may not use is not offered.
- **How it is implemented.** `menu.Registry` with contributions as effects; concurrent resolution under one deadline with panics contained; the two menu routes; the `k-context` island and the "more actions" button partial.
- **Components and files.** `internal/ui/menu`. **Depends on.** [DH-D-UIKIT-4](#dh-d-uikit-4), [DH-D-API-1](api.md#dh-d-api-1), [DH-D-API-3](api.md#dh-d-api-3). Areas C3, C4 and C6 register contributions and mark their target elements; a plugin process registers none.
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-1](../design.md#dh-o-1). **Priority.** P1. **Done when.** DH-T-UIKIT-15 and DH-T-UIKIT-16 pass and [DH-E-UIKIT-4](#dh-e-uikit-4) has passed.

<a id="dh-d-uikit-8"></a>
### DH-D-UIKIT-8 A person can look up every shortcut and change one on their device
- **Result and why it matters.** Keyboard use is discoverable, and a binding that clashes is refused before it is saved.
- **How it is implemented.** The `ui.shortcuts` route and dialog; the `keys` keycap partial; the `k-keys` island over the first-party script's `list`, `set` and `reset`.
- **Components and files.** `internal/ui/keyhelp`, `internal/ui/assets/kit` (`keys`). **Depends on.** [DH-D-UIKIT-4](#dh-d-uikit-4), [DH-D-API-2](api.md#dh-d-api-2). [DH-D-UICORE-6](ui-core.md#dh-d-uicore-6) (`keys.Registry.Commands` for the list, and the script's `list`, `set` and `reset`, where `set` reports a clash).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P1, the reference and recording a new chord together. **Done when.** DH-T-UIKIT-17 passes and [DH-E-UIKIT-4](#dh-e-uikit-4) has passed.

<a id="dh-d-uikit-9"></a>
### DH-D-UIKIT-9 Icons come from recorded sprite files, and the product shows its own name and mark
- **Result and why it matters.** Icons are cached once and recolour with the theme; nothing in the binary imitates upstream's brand.
- **How it is implemented.** Sprite files with a manifest entry each (source, licence, hash); the `icon` family of partials; `brand.Identity` from `brand.Config`; a deny-list test over the embedded assets and the catalogue.
- **Components and files.** `internal/ui/assets/kit`, `internal/ui/brand`. **Depends on.** [DH-D-UIKIT-1](#dh-d-uikit-1), `DH-LIB-SIMPLEICONS`, [DH-D-HOST-1](host-telemetry.md#dh-d-host-1). Area A25's manifest and application icons are named from the same `brand.Identity`.
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Priority.** P0, the technology marks for file types included. **Done when.** DH-T-UIKIT-18 and DH-T-UIKIT-19 pass.

<a id="dh-d-uikit-10"></a>
### DH-D-UIKIT-10 A gallery page and golden files show and pin every partial
- **Result and why it matters.** A reviewer sees every variant and state on one page in both schemes and languages, and an unintended change to shared markup fails a test.
- **How it is implemented.** Each partial registers its examples (a name and a struct) next to its code; the gallery renders them, and the same examples are the golden-file fixtures and the input of the accessibility lint. A partial without an example fails a test.
- **Components and files.** `internal/ui/gallery`, `internal/ui/kit` (examples), `testdata`. **Depends on.** [DH-D-UIKIT-1](#dh-d-uikit-1), [DH-D-UIKIT-2](#dh-d-uikit-2), `DH-LIB-GOLDEN`, `DH-LIB-DOMTEST`.
- **Outcome.** [DH-O-15](../design.md#dh-o-15), [DH-O-12](../design.md#dh-o-12). **Priority.** P0. **Done when.** DH-T-UIKIT-1, DH-T-UIKIT-4 and DH-T-UIKIT-20 pass.

## 9. Acceptance

### 9.1 Local tests

Run with the Go test runner, an HTML parser (`DH-LIB-DOMTEST`) and golden files (`DH-LIB-GOLDEN`). They cannot run JavaScript or lay out a page: what an island does, and what a browser draws, is proven only in [§9.2](#92-real-environment-scenarios). Nothing has been run.

| Test ID | Proves | What is asserted |
|---|---|---|
| DH-T-UIKIT-1 | D-1, D-10 | Every registered example of every partial renders to its golden file, in English and Chinese; the bytes are the same inside a page and alone |
| DH-T-UIKIT-2 | D-1 | `Validate` refuses a control with no accessible name, an unknown variant or size, two actions, a dialog without a title, a disabled state without a reason where one is required; a refused struct writes zero bytes |
| DH-T-UIKIT-3 | D-1 | A table of hostile strings through every `Text`, id and address field yields no new element, attribute, `hx-*` or `javascript:` address (extends `DH-T-GOUISTACK-2`) |
| DH-T-UIKIT-4 | D-1, D-4, D-10 | Accessibility lint over all goldens: every interactive element is a native control with a name; every id an ARIA attribute names exists once; `menu` holds only `menuitem` and `separator`; each `tab` controls an existing `tabpanel`; a `dialog` is labelled; a `separator` that is focusable has its three values; no positive `tabindex`; every focusable element in a swap target has an id; images have `alt` |
| DH-T-UIKIT-5 | D-2 | The stylesheet check: literal colours and static steps only in `tokens`; hover and motion only under their media queries; off-scale radius, a border beside an elevation shadow, a wrong class prefix, `!important` and a selector on `[data-theme]` outside `tokens` are each rejected, with a fixture per rule |
| DH-T-UIKIT-6 | D-2 | Every token a stylesheet reads is defined; every alias has a light and a dark value; each declared text-on-surface pair reaches 4.5:1 and each control boundary 3:1 in both schemes, computed from the values |
| DH-T-UIKIT-7 | D-2 | The assembled file starts with the layer statement, is byte-identical across runs, names no other origin and has no `@import`; its address changes when any source changes |
| DH-T-UIKIT-8 | D-4 | Every kit island in the island table has a module in the asset manifest under its byte limit, declares no extra policy, and contains no `eval`, `Function(`, `innerHTML` or `setAttribute('style'`; every custom element a partial emits is in the table, and every one has complete children in its golden |
| DH-T-UIKIT-9 | D-3 | Appearance validation: each size at and beyond its range; a font list with quotes, braces, semicolons, `url(` or a comment is refused; the generated stylesheet parses to declarations of known custom properties only; the `ETag` changes with the preference |
| DH-T-UIKIT-10 | D-3 | For `system`, `light` and `dark` the page carries the attribute and the head contributions, with no inline script or style; a principal with no preference gets `theme.Config.Default`; another principal's preference is not used |
| DH-T-UIKIT-11 | D-3 | A theme with an unknown token, a non-colour value, or a sprite with a script, a style or an unprefixed id fails registration; unloading its owner restores the built-in values and leaves the stored preference unchanged |
| DH-T-UIKIT-12 | D-5 | The `frame` preference clamps each width; the generated properties match it; the frame's golden has each landmark once, a divider per inner edge with separator attributes, and the collapse checkbox reflecting the preference; the session-panel dock rule is written against `--frame-centre-min`, and a stylesheet check fails if it names the default centre width |
| DH-T-UIKIT-13 | D-6 | Property test of `dock.Apply`: never more than two panes, never an empty pane, unique tab ids, a ratio inside its bounds; a stale revision or an invalid intent returns an error and the same layout; opening an open address focuses it; a layout survives a JSON round trip |
| DH-T-UIKIT-14 | D-6 | Dock goldens: strip roles and selection, one cell per tab with its pane, a blocked split control with its reason, every intent also present as a plain form |
| DH-T-UIKIT-15 | D-7 | Registry: order then id; a duplicate id fails; a contribution that errs, panics or outlasts the timeout is omitted and named, and its context is cancelled; unloading the owner removes it; an item whose route the principal may not call is absent |
| DH-T-UIKIT-16 | D-7 | Menu routes: an unregistered kind is 404; a session the principal cannot see is refused before any contribution runs; every item's action names a route in `web.Routes.List()`; the golden has the roles of the widget table |
| DH-T-UIKIT-17 | D-8 | The help dialog lists every registered command once, grouped, with the platform's default chord; a fixed command has no edit control; a command leaves the list with its owner |
| DH-T-UIKIT-18 | D-9 | Every icon name any example uses exists in a sprite; no symbol id repeats; each sprite's hash, source and licence are in the manifest; no embedded file or catalogue text matches the deny-list (the fish path data, the atlas and font file names, "DeepSeek Harness" outside the "about" text) |
| DH-T-UIKIT-19 | D-9 | The title, the navigation header and `brand.Identity` follow `brand.Config`, and the defaults are `dsh` and the neutral mark |
| DH-T-UIKIT-20 | D-1, D-10 | Every registered kit view has at least one example and appears in the gallery; the gallery routes answer 404 unless `ui.gallery` is loaded; the `kit` namespace is complete in English and Chinese |
| DH-T-UIKIT-21 | D-1 | The `terminal` partial over the four combinations of `Background` and `Running`: a background call after its acknowledgement has no state dot, exit code, empty-output line or status text |

### 9.2 Real-environment scenarios

Each drives the built binary with a real browser through the driver of [libs/playwright.md](../libs/playwright.md) (Chromium), with the replay model provider and a temporary harness home. A scenario that is skipped, blocked or failed stays open. Each proves the stated contracts for the tested build, browser and operating system only.

<a id="dh-e-uikit-1"></a>
#### DH-E-UIKIT-1 The widgets are operable by keyboard alone and expose the right roles
- **Proves.** [DH-D-UIKIT-1](#dh-d-uikit-1), [DH-D-UIKIT-4](#dh-d-uikit-4).
- **Given.** The gallery composition on loopback; Chromium with script enabled, and a second profile with the kit modules blocked; a Chinese input method.
- **When.** Using only the keyboard: open a dialog, open a menu inside it, open its submenu, press Escape three times; walk a menu with arrows, Home, End and a letter; walk a tab list; focus a control with a tooltip and press Escape; filter a pick list and choose; press Enter in a `k-submit` field during composition and after it; copy a code block. Read the accessibility tree after each step. Repeat the dialog and menu steps in the second profile.
- **Then.** Focus never leaves an open dialog and returns to each opener in turn; each Escape closes exactly one layer; the tree shows the roles, names and states of the widget table; composition never submits; "Copied" appears once in the live region; no policy violation is recorded. In the second profile the dialog behaves the same and every menu item is reachable with Tab.
- **Cleanup.** Stop the host, delete the home and profiles. On failure keep the accessibility tree dumps and the console log.

<a id="dh-e-uikit-2"></a>
#### DH-E-UIKIT-2 The chosen appearance is in the first paint, in both schemes, with no violation
- **Proves.** [DH-D-UIKIT-2](#dh-d-uikit-2), [DH-D-UIKIT-3](#dh-d-uikit-3).
- **Given.** The gallery composition on loopback with a temporary home; Chromium with script enabled and a second profile with scripts blocked; the browser's colour-scheme, forced-colours and reduced-motion emulation.
- **When.** Load a session page with the preference `system` under an emulated light and then dark system; set `dark`, a larger text size and a font list in settings; reload; open a second profile of the same owner; switch on forced colours and reduced motion.
- **Then.** A screenshot taken at first paint already has the expected background in every case, also with scripts blocked; computed styles of sampled elements equal the token values; icons take the text colour and are drawn in the terminal frame too; the second profile shows the same choice after a load; forced colours leave every control visible and focus rings present; nothing animates under reduced motion; no request leaves the origin.
- **Cleanup.** Reset the preference, stop the host, delete the home and profiles.

<a id="dh-e-uikit-3"></a>
#### DH-E-UIKIT-3 Panes resize and tabs move without restarting content
- **Proves.** [DH-D-UIKIT-5](#dh-d-uikit-5), [DH-D-UIKIT-6](#dh-d-uikit-6).
- **Given.** A session with a real terminal running a counter and a file preview in the side panel (needs areas C6 and A09); viewport widths of 1,600, 1,100, 900 and 600 px; touch emulation for one pass.
- **When.** Drag each divider with the pointer, then move it with the keyboard; reload; split the panel, move the terminal's tab to the other pane and back by menu and by key; open the same session in a second tab and move a tab there; step through the widths; block the dock module and repeat one move.
- **Then.** The centre is never narrower than 400 px; widths are the same after the reload; the terminal's socket is never closed and its counter never restarts; the second tab's change appears in the first; at 1,600 and 1,100 px the session panel is docked beside a transcript at least 400 px wide, and at 900 px and below it is an overlay; at 600 px the navigation is a page and the side panel covers the centre; with the module blocked the move still happens through a page load.
- **Cleanup.** Close the terminal, delete the session, stop the host, confirm no process remains, delete the home.

<a id="dh-e-uikit-4"></a>
#### DH-E-UIKIT-4 A context menu shows what is allowed, and a shortcut can be looked up and changed
- **Proves.** [DH-D-UIKIT-7](#dh-d-uikit-7), [DH-D-UIKIT-8](#dh-d-uikit-8).
- **Given.** A composition with two test contributions for sessions, one of which sleeps past the timeout; a session row and a file link.
- **When.** Right-click the row; press Shift+F10 on it; right-click inside a text field; delete the session from another tab and use an item of a menu left open; open the shortcut help, search by a key, record a chord that clashes and then a free one, reload, restore the default.
- **Then.** The menu appears within the timeout with the fast contribution's items and a row naming the omitted one; the keyboard opening is placed at the row; the text field shows the browser's own menu; the stale item reports that the session no longer exists and changes nothing; the clash is refused beside its row, the free chord works after the reload and in a second tab, and the default works after the restore.
- **Cleanup.** Clear the browser's stored bindings, stop the host, delete the home.

<a id="dh-e-uikit-5"></a>
#### DH-E-UIKIT-5 The required browser features and the widget contract hold in Firefox and Safari
- **Proves.** [DH-D-UIKIT-4](#dh-d-uikit-4), [DH-D-UIKIT-2](#dh-d-uikit-2).
- **Given.** Current Firefox on Windows, macOS and Linux and Safari on macOS, each with a fresh profile; the gallery composition. This is the smaller automated set of [DH-ADR-39](../design.md#dh-adr-39). The Chromium driver does not reach these engines ([DH-ADR-PLAYWRIGHT-1](../libs/playwright.md)); they are driven through W3C WebDriver ([DH-ADR-PLAYWRIGHT-2](../libs/playwright.md#dh-adr-playwright-2)). Until that back end is built a person follows the written checklist, the run is recorded as `manual`, and the scenario stays open.
- **When.** Repeat the keyboard steps of [DH-E-UIKIT-1](#dh-e-uikit-1) and the scheme steps of [DH-E-UIKIT-2](#dh-e-uikit-2); open an anchored popover and menu near each viewport edge; draw icons from the sprite file.
- **Then.** The unsupported-browser banner is hidden; behaviour matches the widget table; a popover is attached to its opener or, where anchoring is missing, centred and fully visible; icons draw. Each difference is recorded as a defect or as a stated fallback.
- **Cleanup.** Delete the profiles and the home.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Who owns what, settled.** Appearance (scheme, theme, text sizes, fonts) is this area's and is stored once, as a host preference per principal in C1's `prefs` store ([DH-ADR-UIKIT-8](#dh-adr-uikit-8)); the language is C1's preference in the same store ([ui-core.md §6.2](ui-core.md#62-contracts)). Neither is a cookie. Copying to the clipboard is this area's `k-copy` island for every other area. A plugin process contributes no menu item: it has no UI capability ([ui-core.md §10](ui-core.md#10-open-questions-and-limitations), item 2).
2. **Assumed of [api.md](api.md) and [app-web.md](app-web.md) (requests sent to their owner).** A route kind for a generated stylesheet (`Download` is used here); `ui.appearance` and the shortcut page usable by every admitted principal; the manifest, favicons and application icons named from `brand.Identity`; the "second device" table of app-web.md corrected to say that theme and language follow the principal.
3. **Decided, and recommended defaults.** Decided by the owner ([DH-ADR-39](../design.md#dh-adr-39)): Chromium-family fully, current Firefox and Safari with the smaller engine-sensitive set, releases since 2024, a banner below that. Recommended defaults, not confirmed by the owner: the displayed product name `dsh` with a neutral mark ([DH-ADR-UIKIT-7](#dh-adr-uikit-7)); phones are best effort and untested (U-K2-10, [DH-ADR-UIKIT-3](#dh-adr-uikit-3)); the session panel docks down to the transcript's minimum readable width (U-K2-7, [DH-D-UIKIT-5](#dh-d-uikit-5)).
4. **Not verified, because it needs a run.** Which browser versions have each required and optional feature; that setting a custom property through the style object and a same-origin `<use>` reference are allowed under the base policy in each engine; that a `<dialog>` returns focus to its opener when it was swapped in by htmx; that a toast hidden by an ended animation leaves the accessibility tree; the byte limits of the islands; the palette's contrast (the values are not chosen yet).
5. **Limits kept, each with its reason in the ledger.** No floating panels, drag-to-dock or nested splits. A menu is not re-resolved while open. Appearance changes reach other tabs on their next load. Shortcut keys are shown with US key names. Site marks are a fixed list. Five status marks. Image pan and zoom inside the lightbox is not built: the image fits the viewport and links to the original. List reordering animates only where the browser has view transitions.
6. **Artwork.** The palette values and the neutral mark must be drawn; reusing upstream's MIT-licensed colour values is permitted by the licence and is a design choice, not decided here. The licence file of the simple-icons copy must be read when it is made ([libs/simple-icons.md](../libs/simple-icons.md)).

## 11. Units dispositioned by this spec

Ledger fragment: `trace/fragments/C2.jsonl`.

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 8 | 4 ported (`ui-primitives`, `ui-theme`, `ui-context-menu`, `ui-shortcuts`), 3 replaced (`ui-layout`, `ui-dockkit`, `ui-renderer`), 1 dropped (`ui-brand-official`) |
| `UP-DEBT` | 40 | 26 resolved, 5 not applicable, 9 carried |
| `UP-CFG` | 2 | 2 replaced |
| `UP-DEC` (handed over) | 4 | 2 ported, 1 replaced, 1 dropped |

Go items declared: `DH-ADR-UIKIT-1` to `-8`, `DH-D-UIKIT-1` to `-10`, `DH-T-UIKIT-1` to `-21`, `DH-E-UIKIT-1` to `-5`.
