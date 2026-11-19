# @xterm/xterm and @xterm/addon-fit (the browser terminal widget): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Package facts were read on 2026-11-18 from the npm registry
> manifests of the exact versions and from the xterm.js source at tag 6.0.0. Nothing was
> downloaded into the repository, built or run in a browser.

| | |
|---|---|
| **Library ID** | `DH-LIB-XTERMJS` (one family, two packages) |
| **Upstream units** | `UP-DEP-@xterm/xterm`, `UP-DEP-@xterm/addon-fit` |
| **Upstream version** | `@xterm/xterm` 6.0.0; `@xterm/addon-fit` 0.11.0 (both still the `latest` tag) |
| **Kind** | bundled into the client |
| **Used by** | client (`packages/client/ui-sidebar-terminal`) |
| **Go choice** | Kept: the same two packages, vendored as their published ES module files and wrapped in one island. There is no Go replacement for a terminal drawn in a browser |
| **Related areas** | C6 (terminal panel), A09 (terminals); [go-ui-stack.md](go-ui-stack.md) for the island rules; [xterm.md](xterm.md) for the host-side emulator; [ws.md](ws.md) for the socket |

| Package | What it is | In the Go design |
|---|---|---|
| `@xterm/xterm` | xterm.js: draws a terminal in a web page, turns keys and pastes into the bytes a program expects, and applies the program's output to the screen | Vendored `lib/xterm.mjs` and `css/xterm.css`, version 6.0.0, MIT, no runtime dependencies |
| `@xterm/addon-fit` | Works out how many columns and rows fit the element | Vendored `lib/addon-fit.mjs`, version 0.11.0, MIT. It reads xterm.js internals, so it is always upgraded together with xterm.js |

## 1. What it is and why upstream needs it

A terminal in the sidebar lets a person, or an agent, run a shell on the host. The
program runs on the host, behind a pseudo-terminal; the browser only draws its screen
and sends keystrokes. Drawing that screen correctly (colours, cursor movement,
full-screen programs, input methods, selection, clipboard) is the job of a terminal
emulator, and xterm.js is the one browsers use. Under
[DH-ADR-3](../design.md#dh-adr-3) it stays, as a JavaScript island, because no amount of
server-rendered HTML can follow output at that rate or capture raw keys.

The host keeps its own emulator ([xterm.md](xterm.md)) so that a terminal lives on while
no tab shows it. This document covers only what runs in the browser.

## 2. How upstream uses it

All in `packages/client/ui-sidebar-terminal/src/client/`. The package loads xterm.js in a
separate chunk, only when a terminal view mounts (`README.md`, "Implementation
internals").

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `terminal.tsx:102-108` | Create the widget with the host's scrollback size, the theme's terminal font and a 4.5:1 minimum contrast, and mount it | `new Terminal({...})`, `loadAddon`, `open` |
| `terminal.tsx:157-167` | Draw: a **snapshot** frame resets the widget, resizes it to the host's grid and writes the whole screen; a **data** frame writes one chunk. Each applied frame is acknowledged to the host | `reset`, `resize`, `write(data, callback)` |
| `terminal.tsx:116-121`, `:72-80` | Send input, flagged "typed" only if a key, paste, drop, click or input-method commit happened in the last 250 ms. xterm.js reports a person's typing and its own replies to the program through the same event, and this is how upstream tells them apart | `onData` |
| `terminal.tsx:122-127`, `:191-200` | Fit: ask how many cells fit, clamp to the host's maximum, resize the widget and tell the host. Only while the view is visible, writable and has a size | `FitAddon.proposeDimensions()`, `resize` |
| `terminal.tsx:169-175` | A read-only view takes no input and follows the host's size instead of fitting | `options.disableStdin`, `resize` |
| `terminal-theme.ts:19-49`, `:72-79` | Colours follow the application theme, while colours the program set itself (escape codes 4, 10 to 12 and their resets) are kept apart and survive a theme change | `parser.registerOscHandler`, `options.theme` |
| `terminal-cursor.ts:13-37` | Keep the cursor at 3:1 contrast against the cell under it, by reading xterm.js's cursor element after each render | `onRender`, the `.xterm-cursor` class |
| `terminal.tsx:112`, `:176-179` | Accessible name for the input, focus when shown | `textarea`, `focus` |
| `terminal.tsx:128-137` | Release everything | `dispose` |

## 3. Tech debt upstream carries

No `UP-DEBT` unit is assigned to this family; no patch, pin or exemption exists for it
(`patches/`, `pnpm-workspace.yaml` searched). Three weaknesses in the calling code
matter for the Go design. They belong to area C6, which owns that package's units.

| Item | Evidence | In the Go design |
|---|---|---|
| Typing is told from terminal replies by a 250 ms timer | `terminal.tsx:72-80` | [xterm.md](xterm.md) section 7 asks who answers the program. Recommended here: the host emulator always answers, and the island sends input only for a person's action, so no reply is ever sent twice. The timer then decides only "did a person take over an agent's terminal" |
| The cursor fix depends on a private class name and on the DOM renderer | `terminal-cursor.ts:15-25` | Keep the requirement (`R5`), not the mechanism; it is re-tested on every xterm.js upgrade |
| A failed chunk load needs a page reload | `packages/client/ui-sidebar-terminal/README.md`, last limitation | Gone: an island module is imported again the next time its element appears ([DH-ADR-GOUISTACK-6](go-ui-stack.md#dh-adr-gouistack-6)) |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-XTERMJS-R1` | The island draws exactly what the host sends: a snapshot replaces the screen at the host's size, later chunks are applied in order, and each applied frame is acknowledged so the host can bound what it sends | `terminal.tsx:157-167`; [ws.md](ws.md) `DH-LIB-WS-R3` |
| `DH-LIB-XTERMJS-R2` | Input reaches the host in order and within the host's input limit; a read-only or disconnected view sends none and shows that state | `terminal.tsx:116-121`, `:169-175`; `packages/api/terminal-controller/README.md:41` |
| `DH-LIB-XTERMJS-R3` | The program receives each reply to a terminal query once, with a tab attached or not | [xterm.md](xterm.md) `DH-LIB-XTERM-R2` |
| `DH-LIB-XTERMJS-R4` | A visible, writable view sizes the host terminal to the cells that fit, within the host's limits; a hidden or zero-size view never resizes it | `terminal.tsx:122-127`, `:191-200` |
| `DH-LIB-XTERMJS-R5` | Background, text, selection and font follow the application theme and settings without restarting the shell; colours set by the program survive a theme change; the cursor stays at 3:1 contrast | `terminal-theme.ts`, `terminal-cursor.ts` |
| `DH-LIB-XTERMJS-R6` | The island is loaded only when a terminal is shown, has a translated accessible name, and on removal closes its socket and frees the widget. It runs under the frame policy of [DH-ADR-GOUISTACK-3](go-ui-stack.md#dh-adr-gouistack-3) | `terminal.tsx:112`, `:128-137` |

## 5. Go choice

**Decision.** Keep xterm.js 6.0.0 and its fit add-on 0.11.0. Vendor the three published
files (`lib/xterm.mjs`, `css/xterm.css`, `lib/addon-fit.mjs`) with version, source, licence
and SHA-256 in the asset manifest. One first-party ES module defines the `<dsh-terminal>`
custom element around them. The element lives in a same-origin frame, because xterm.js
creates `<style>` elements at run time with no nonce
(`src/browser/renderer/dom/DomRenderer.ts` at tag 6.0.0), which the main document's
policy forbids. The frame document, its status line and its buttons are rendered by the
host; the island draws only the screen.

**Why.** It is the same widget the host-side snapshot is written for
([DH-ADR-XTERM-2](xterm.md#dh-adr-xterm-2)), it publishes an ES module that a browser
loads without a bundler, and it has no dependencies.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **xterm.js 6.0.0 + fit 0.11.0, vendored** (chosen) | Known behaviour; host snapshot format already targets it | About 6 MB unpacked in the npm package, of which three files are vendored; needs the frame | npm manifests of 6.0.0 and 0.11.0: MIT, no `dependencies`, `module` entries `lib/xterm.mjs` and `lib/addon-fit.mjs`, `style` `css/xterm.css`; both are the `latest` tag (pre-releases of 6.1.0 and 0.12.0 exist under `beta`); MIT also in upstream `THIRD_PARTY_NOTICES.md:74`, `:77` |
| Own fit code instead of the add-on | One file fewer | Would read the same xterm.js internals the add-on reads; no gain | n/a |
| Host renders the screen as HTML | No island | Cannot follow full-screen programs at output rate or capture raw keys | Rejected by [DH-ADR-WIRE-2](../upstream/wire-and-api.md#dh-adr-wire-2) |

**Tradeoff.** The project carries a vendored third-party script and a frame boundary
(focus, shortcuts and resize cross it). Upstream's limitation that a newly opened view
cannot recover program-set colours from the host snapshot remains unless the host
snapshot learns to carry them, which is for the terminal area.

## 8. Verification

The local test checks what the host serves. Behaviour inside the widget needs a real
browser and is part of [DH-E-GOUISTACK-3](go-ui-stack.md#dh-e-gouistack-3) and of
`DH-T-XTERM-4` in [xterm.md](xterm.md). Nothing has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-XTERMJS-1` | local | `DH-LIB-XTERMJS-R6` | The three vendored files match their manifest hashes; the terminal frame route answers with the frame policy and the main document does not; the frame's HTML names the island module, the socket address, the limits and the translated label as attributes |
| `DH-T-XTERMJS-2` | local, Go client on the socket | The host half of `DH-LIB-XTERMJS-R1`, `R2`, `R4` | A Go test plays the island's side of the protocol: it receives a snapshot then chunks, acknowledges, sends input and a resize, and is refused beyond the input limit and the size limits. It proves the contract the island is written against, not the island |
| [DH-E-GOUISTACK-3](go-ui-stack.md#dh-e-gouistack-3) | real browser | The island half of `R1`, `R2`, `R4`; `DH-LIB-XTERMJS-R3`, `R5`, `R6` | The terminal draws and takes input in its frame with no policy violation; it fits its pane and the host follows; a cursor-position query gets one reply; a theme change keeps the shell and the program's colours; removing the element closes the socket |

## 9. Units dispositioned by this spec

- `UP-DEP-@xterm/xterm`
- `UP-DEP-@xterm/addon-fit`
