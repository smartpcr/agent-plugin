# react, react-dom, zustand, immer, use-sync-external-store and clsx: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Nothing here was compiled, run or measured.

| | |
|---|---|
| **Library ID** | `DH-LIB-REACT` (one family: rendering and client state) |
| **Upstream units** | `UP-DEP-react`, `UP-DEP-react-dom`, `UP-DEP-zustand`, `UP-DEP-immer`, `UP-DEP-use-sync-external-store`, `UP-DEP-clsx` |
| **Upstream version** | react 18.3.1, react-dom 18.3.1, zustand 4.5.7, immer 10.2.0, use-sync-external-store 1.6.0, clsx 2.1.1 |
| **Kind** | bundled into the client (every one is a development dependency that the client build packs into browser bundles) |
| **Used by** | react: 83 packages; react-dom: 48; clsx: 24; zustand and immer: `packages/client/store` only; use-sync-external-store: `packages/client/ui-renderer` only |
| **Go choice** | **own code**: the host renders HTML and holds the state; no client framework and no client store library. The template engine, htmx and its extensions are chosen by the UI stack specification, not here |
| **Related areas** | The UI areas named in [design.md §4.2](../design.md#42-go-module-layout) (`internal/ui`); [upstream/wire-and-api.md](../upstream/wire-and-api.md) (`DH-ADR-WIRE-1` to `-3`); [micromark.md](micromark.md) for the text inside a message; [lexical.md](lexical.md) for the composer; [tanstack-react-virtual.md](tanstack-react-virtual.md) for long lists |

| Package | What it is | What happens to it |
|---|---|---|
| `react`, `react-dom` | The component framework and its browser renderer | Replaced by fragments rendered on the host ([DH-ADR-3](../design.md#dh-adr-3)) |
| `zustand` | A small store: a value, a `set`, and subscribers | Replaced: application state stays on the host; view state goes to four named places ([DH-ADR-REACT-1](#dh-adr-react-1)) |
| `immer` | Lets code "mutate" a draft and get a new immutable object | Not applicable in Go |
| `use-sync-external-store` | The React hook that reads such a store safely during rendering | Not applicable in Go |
| `clsx` | Joins CSS class names conditionally | Not applicable: a template writes the class attribute |

## 1. What it is and why upstream needs it

Upstream's web client is a program that runs in the browser. It keeps a **mirror** of the
host's state (sessions, the loaded part of a conversation, workspaces), turns that mirror
into a tree of React components, and lets React change only the parts of the page whose
data changed. The dependency direction is "Host state → Remote transport → Client model →
UI adapter → Conversation or presentation → Slots → React"
(`docs/subsystems/web-client.md:20`).

The libraries in this family are the machinery of that mirror. React draws and re-draws.
zustand holds each piece of client state as a snapshot with subscribers; immer produces
the next snapshot; `use-sync-external-store` connects a snapshot to a component; clsx
builds class strings. None of them is a product feature. The **jobs** they do for the
user are: the page shows new text while the model is still writing, a sent message
appears at once, a collapsed panel stays collapsed, and the browser remembers where the
user was.

[DH-ADR-3](../design.md#dh-adr-3) removes the mirror: the host renders HTML **fragments**
(pieces of a page) and htmx swaps them in. This specification lists each job and says
where it is done now: on the host, in the browser's own features, or in a small script.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/client/ui-renderer/src/client/renderer.ts:4`, `:31`, `:36` | Mount the one application tree; take over the boot DOM the host sent | `createRoot`, `hydrateRoot` |
| `docs/subsystems/web-client.md:26` | The first frame waits until the whole client plugin roster has loaded | `renderSlot('root')` |
| `docs/subsystems/conversation.md:11`, `:246`, `:254` | One assembler per session folds events into **nodes** with a stable key `(kind, id)`; React keeps a node's DOM when only its data changes | keyed elements |
| `docs/subsystems/conversation.md:72`, `:287` | Live model output arrives as client-only `assistant/live-chunk` events; a visible change is published at most once per animation frame | `publication: 'animation-frame'` |
| `docs/subsystems/conversation.md:276-283` | Three ways the loaded window changes: replace (open, resync), prepend an older page, append one live event | incremental update |
| `packages/api/session-controller/README.md:64` | **Submission echo**: after the host accepts a prompt, the client shows the message from its own memory until the durable event arrives; the prompt's request id is the correlation | client memory |
| `packages/client/ui-conversation/README.md:137` | While the host has not yet accepted, the composer is `sending`: read-only, with progress on the Send button | component state |
| `packages/client/store/src/index.ts:10-13`, `:104`, `:128-130` | The store engine: zustand vanilla store, selector subscriptions, immer `produce` for updates, publication either synchronous or once per animation frame | `createStore`, `subscribeWithSelector`, `shallow`, `produce`, `freeze` |
| `packages/client/store/src/index.ts:139-165` | Optional persistence of a whole store value as JSON in `localStorage`; failures only disable persistence | hand-written, not the zustand middleware |
| `packages/client/ui-renderer/src/client/bind.ts:9`, `:24` | Turn a `subscribe`/`getSnapshot` pair into a React hook with a selector | `useSyncExternalStoreWithSelector` |
| `docs/subsystems/slots.md:103`, `:131`, `:237` | Components never receive the cordis context; a declared store holds "shared viewing and interaction state only" | slot props |
| `docs/web-styling.md:17` | "Use CSS Modules and `clsx`; do not add a component library or Tailwind" | `clsx(...)` |

**What the persisted stores hold.** Reading every `persist` declaration shows what "local
UI state" means in practice.

| Store (`path:line`) | Content |
|---|---|
| `packages/client/ui-workspace/src/client/navigation.ts:176` | Which session is current (`dsh.sessions.current`) |
| `packages/client/ui-conversation/src/client/stores.ts:8`, `:22-34` | Per session: the composer draft and the selected view (Chat or Trajectory) |
| `packages/client/ui-workspace/src/client/stores.ts:83-91` | Sidebar grouping, ordering, which groups are expanded, the archived filter |
| `packages/client/ui-session-panel/src/client/visibility.ts:39` | Whether the session panel is pinned |
| `packages/client/ui-sidebar-browser/src/client/browser/store.ts:23`, `ui-sidebar-files/src/client/index.ts:76` | Sidebar browser tabs; file search preferences |
| `packages/client/ui-trajectory/src/client/duration-store.ts:11`, `string-wrapping-store.ts:11` | Two display toggles in the Trajectory view |
| `packages/client/ui-user-questions/src/client/draft-store.ts:46` | Unsent answers to a pending question |
| `packages/client/ui-open-in-app/src/client/controller.ts:30`, `ui-plugin-manager/src/client/manager-store.ts:694` | Last "open in" choice; plugin install registry |

**Routing.** A search of `packages/client`, `packages/api` and `apps/web` for
`pushState`, `replaceState`, `popstate` and `hashchange` outside tests finds nothing.
Upstream has no URL routing: the current session is a `localStorage` value.

## 3. Tech debt upstream carries

Search performed: `patches/` and `pnpm-workspace.yaml` (no patch, pin or override for any
of the six packages); the "Known Limitations" of `client/store` and `client/ui-renderer`.
No `UP-DEBT` unit is assigned to this family. The items below belong to the consuming
packages; they are listed because the Go choice removes their cause.

| Item | What it is | Evidence (`path:line`) | Effect of the Go design |
|---|---|---|---|
| View state is per browser profile | "persisted stores use JSON in `localStorage`"; no synchronisation; a second window or the desktop application starts from other values | `packages/client/store/README.md:31` | Preferences move to the host, navigation moves to the URL (`DH-LIB-REACT-R5`, `-R6`) |
| The first frame waits for every client plugin | No per-region readiness, no lazy entries | `packages/client/ui-renderer/README.md:96`, `:98` | A page is one HTTP response; a slow region can be a fragment loaded after the page (`DH-LIB-REACT-R1`) |
| The echo is client memory | "reload and reconnect rebuild the conversation from durable events alone", so a reload between acceptance and the durable event loses the bubble; "Reconnecting between claim and admission can briefly omit that bubble" | `packages/api/session-controller/README.md:64`, `:67` | The pending row is host state, the same in every tab ([DH-ADR-REACT-3](#dh-adr-react-3)) |
| Hand-written persistence beside the store library | The zustand `persist` middleware corrupts primitive values, so upstream wrote its own | `packages/client/store/src/index.ts:139-143` | No client store library |
| A client model per controller, with race resolution | Each controller keeps a browser mirror and merges late replies with new baselines | `docs/subsystems/web-client.md:40`, `:56` | Removed with the mirror; see [upstream/wire-and-api.md §10](../upstream/wire-and-api.md) |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-REACT-R1` | A page is complete in one response: the transcript tail, the session list and the composer are in the first HTML, readable before any script runs. Nothing waits for a plugin roster | `web-client.md:26`; `ui-renderer/README.md:96` |
| `DH-LIB-REACT-R2` | Every conversation node has a DOM id derived from its stable key. An event that changes a node replaces that node only. An update for a node that is not in the page is ignored, and the node appears with its current state when its page is loaded | `conversation.md:16`, `:70`, `:254`, `:282` |
| `DH-LIB-REACT-R3` | While the model writes, the reader sees text grow without the page re-sending what it already showed: bytes sent for one reply grow in proportion to the reply, not to its square. A slow or hidden tab receives fewer, larger updates, never a queue | `conversation.md:287`; `packages/client/ui-primitives/src/markdown/incremental.ts:4-14`; `DH-ADR-WIRE-3` |
| `DH-LIB-REACT-R4` | Sending a message gives feedback in three steps, each visible: the composer is busy while the request is in flight; an accepted message appears as a pending row; the durable message replaces the pending row exactly once. A refused or failed send leaves the draft and its attachments in the composer. A repeated send with the same request id is accepted once | `session-controller/README.md:64`; `ui-conversation/README.md:137`, `:141` |
| `DH-LIB-REACT-R5` | The address bar names what is on screen: session, view and open side panel. Back, forward, reload and a copied link restore it | Inference from the absence of routing upstream and from `navigation.ts:176` |
| `DH-LIB-REACT-R6` | A preference that changes what the host renders (grouping, ordering, expanded groups, filters, pinned panels, display toggles) is stored by the host and is the same in every tab and every browser the user opens | `ui-workspace/src/client/stores.ts:83-91`; `store/README.md:31` |
| `DH-LIB-REACT-R7` | A live update never undoes what the reader did to a node: a disclosure they opened stays open, a text selection, the focused control and the scroll position inside it are kept | `conversation.md:254` ("keep returning the same key"); `packages/client/ui-chat/README.md:161` |
| `DH-LIB-REACT-R8` | The transcript follows the newest content while the reader is at the bottom and stops following as soon as they scroll away; loading older history keeps the line they were reading in place | `ui-chat/README.md:159-161` |
| `DH-LIB-REACT-R9` | Styling keeps upstream's rules: semantic design tokens, no literal colours in feature styles, hover styles only where the device can hover, visible keyboard focus, reduced motion respected | `docs/web-styling.md:9`, `:18`, `:24` |
| `DH-LIB-REACT-R10` | The client script that remains holds no application data. Removing it leaves a page that still shows the session and still sends a prompt | [DH-ADR-3](../design.md#dh-adr-3) |

## 5. Go choice

**Decision.** No client framework and no client store. Recorded as
[DH-ADR-REACT-1](#dh-adr-react-1) (where state lives),
[DH-ADR-REACT-2](#dh-adr-react-2) (how streaming text reaches the page) and
[DH-ADR-REACT-3](#dh-adr-react-3) (submission feedback).

**Why.** Each library exists to keep a second copy of host state consistent inside the
browser. With the host rendering, there is no second copy, so there is nothing for a
store, an immutable-update helper or a subscription hook to do.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Host-rendered fragments, state on the host, a small own script** (chosen) | One place for state; every tab agrees; a page works before scripts load; no client build | Every interaction that changes host state costs a request; scroll following and drafts still need script; rendering cost moves to the host | No module. The template engine and htmx are for the UI stack specification |
| Keep React as one large island | All of upstream's interaction code could be studied line by line | Contradicts [DH-ADR-3](../design.md#dh-adr-3); needs a bundler, which the toolchain drops; brings back the mirror and its race resolution | Not a Go module |
| A smaller client framework (Preact, Alpine.js, a signals library) for view state | Familiar component model for menus and panels | Still a second state holder with its own lifecycle; the jobs in section 6 are covered by browser features and about a page of script | Not evaluated: no requirement in section 4 needs it |
| Web components for each widget | Encapsulation without a framework | Encapsulation is not a requirement here; shadow DOM complicates the shared design tokens (`web-styling.md:9`) | Not evaluated |

**Tradeoff.** Interactions upstream answers in a frame (switching Chat to Trajectory,
expanding a sidebar group) become a local HTTP request unless they are pure browser
state. On loopback this is expected to be imperceptible. Inference: that is not measured,
and for a browser that reaches the host over a network (remote access, or a viewer of
a shared session, who connects to the host directly because there is no relay:
[DH-ADR-XSHARE-2](../areas/x-liveshare-webworker.md#dh-adr-xshare-2)) it will be slower
than upstream.

<a id="dh-adr-react-1"></a>
### DH-ADR-REACT-1 Application state lives only on the host; view state lives in four named places

- **Decision.** The browser never holds session, workspace, job or interaction data except
  as rendered HTML. State that only affects the view goes to exactly one of:

  | Place | Holds | Why there |
  |---|---|---|
  | **The URL** | Current session, selected view, open side panel and its tab | It is navigation; the browser already saves, restores and shares it |
  | **Host preferences** (the settings store of area A07) | Grouping, ordering, expanded groups, filters, pinned panels, display toggles, theme | The host must know them to render the first response correctly, with no flash of the wrong layout |
  | **The DOM** | Open or closed disclosures (`<details>`), the open dialog or popover, focus, selection, scroll | The browser owns these natively; no code is needed to hold them |
  | **A small per-tab script store** | Unsent question answers, "am I following the tail", and a buffer of composer text the host has not saved yet | They change on every keystroke or scroll and must not cost a request each time |

  The composer **draft** itself (its text and its staged uploads) is host state, one
  record per session and principal, so a reload or a second tab keeps it; the tab's copy
  is only the unsaved buffer and is used when its revision matches the host's
  ([DH-ADR-UICHAT-4](../areas/ui-chat.md#dh-adr-uichat-4), which owns the composer).

- **Why.** Upstream declares stores with `defineStore` in 27 source files (a search of
  `packages/client`, `packages/experimental` and `apps`, tests excluded) and mixes all
  four kinds in one mechanism, which is why view state is lost between windows and
  navigation is not linkable.
- **Verdict on upstream.** *Keep* the rule that stores hold "shared viewing and
  interaction state only" (`docs/subsystems/slots.md:237`). *Change* where each kind
  lives. *Drop* the store engine.
- **Tradeoff or constraint.** A preference write is a request. The script store uses
  `sessionStorage` or `localStorage` and must tolerate both being unavailable, as
  upstream does (`store/src/index.ts:144-150`).
- **Serves.** [DH-O-12](../design.md#dh-o-12); `DH-LIB-REACT-R5`, `-R6`, `-R7`, `-R10`.
- **Verified by.** `DH-T-REACT-4`, `DH-T-REACT-5`, `DH-T-REACT-7`.

<a id="dh-adr-react-2"></a>
### DH-ADR-REACT-2 Streaming text is sent as block-level HTML deltas with a per-connection cursor

- **Decision.** For each assistant message in flight the host keeps one **live render
  state**: the text so far, the HTML of every top-level block that can no longer change
  (*frozen* blocks), and the HTML of the unstable tail. Each browser connection keeps one
  number: how many frozen blocks it has been sent. When a connection is ready to be
  written, the host sends the frozen blocks it has not sent (to be appended) and the
  current tail (to replace the previous tail). When the message settles, the durable
  event's fragment replaces the whole live element once.
- **Why.** Sending the whole message on every tick costs bytes in proportion to the
  square of the reply length. Upstream removed the same quadratic cost inside the
  browser by freezing all but the last two blocks
  (`packages/client/ui-primitives/src/markdown/incremental.ts:4-14`, `:38`). The cursor
  makes dropping a tick safe: the next write is simply larger. That is what lets
  `DH-ADR-WIRE-3` coalesce `live` frames.
- **Verdict on upstream.** *Keep* the freeze algorithm and the once-per-frame cadence.
  *Change* where it runs: on the host, once per message for all tabs.
- **Tradeoff or constraint.** The host holds render state for every message in flight and
  releases it when the message settles or its turn ends. A reference-style link whose
  definition arrives after its block froze stays literal until the message settles, as
  upstream (`incremental.ts:25-28`). The Markdown side is specified in
  [micromark.md](micromark.md) (`DH-LIB-MICROMARK-R8`).
- **Serves.** [DH-O-12](../design.md#dh-o-12); `DH-LIB-REACT-R3`.
- **Verified by.** `DH-T-REACT-2`, `DH-T-REACT-3`.

<a id="dh-adr-react-3"></a>
### DH-ADR-REACT-3 Submission feedback is a host-rendered pending row, not client memory

- **Decision.** The prompt form carries a request id. While the request is in flight the
  form is marked busy by htmx and its controls are disabled. The response to an accepted
  prompt is the emptied composer plus a **pending row** whose DOM id contains the request
  id. The pending row is rendered from the host's inbox projection, so every tab on the
  session shows it. The durable user message's fragment removes the pending row with the
  same id. A response with an error status swaps nothing into the composer, so the draft
  stays.
- **Why.** Upstream already waits for acceptance before showing the echo
  (`packages/api/session-controller/README.md:64`), so the echo is not a guess; it is a
  host fact that upstream happens to keep in the browser. Keeping it on the host removes
  the reload and reconnect gaps upstream documents (`:67`).
- **Verdict on upstream.** *Keep* acceptance-then-echo and request-id correlation.
  *Change* the holder from client memory to the inbox projection.
- **Tradeoff or constraint.** Feedback needs the response; on a slow link the busy state
  lasts longer than upstream's local transition would. No message is ever shown that the
  host did not accept.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-3](../design.md#dh-o-3);
  `DH-LIB-REACT-R4`.
- **Verified by.** `DH-T-REACT-6`.

## 6. Design notes

**The job map.** This is the heart of the specification: each thing the libraries did,
and where it is done now.

| Job upstream does in the browser | Go answer | Where it runs |
|---|---|---|
| Fold events into conversation nodes (`ui-conversation` assembler) | The same fold as host code: a registry of node renderers, each a pure function from the folded state of one `(kind, id)` to a fragment | Host |
| Re-render only what changed | The `session` server-sent event for one committed event carries the fragments that event changed: a new node to append, or a replacement addressed by DOM id | Host renders, htmx swaps |
| Publish at most once per frame | A per-connection writer coalesces; the cadence is a host setting | Host |
| Prepend an older page without disturbing loaded nodes | A history fragment inserted before the first node; the browser's scroll anchoring keeps the reading line | Host renders; browser anchors |
| Submission echo, `sending` state | [DH-ADR-REACT-3](#dh-adr-react-3) | Host; htmx request state |
| Client model reconnect and gap repair | `DH-ADR-WIRE-3`: resume by sequence number or `reset` | Host |
| Stores with `localStorage` | [DH-ADR-REACT-1](#dh-adr-react-1) | URL, host, DOM, small script |
| Collapsed panels and folded turns | `<details>` elements; a preference when the default must persist | Browser |
| Modal with focus trap and Escape handling; menus and popovers | `<dialog>` and the Popover API. A browser that lacks them gets the unsupported-browser banner of area C2 ([DH-ADR-UIKIT-3](../areas/ui-kit.md#dh-adr-uikit-3)); see section 7 | Browser |
| Class-name joining | Template conditionals | Host |
| Immutable snapshots for change detection | Not needed: a projection produces a new Go value and the feed says which event changed | Host |
| Slots: packages contribute components to named places | Named template slots filled by compiled-in components through a host registry. A UI area owns it; it is outside this specification | Host |

**The stable shell rule (`DH-LIB-REACT-R7`).** A node's fragment has two parts: a
**shell** that is sent once (the container, its disclosure element, its header) and
**live regions** inside it that later events replace (status, progress, output tail). An
update addresses a live region, never the shell. Because the disclosure element is never
re-sent, the reader's open or closed choice survives without any script. Inference: a
morphing swap (an htmx extension that patches the existing DOM instead of replacing it)
would give the same guarantee with less template discipline; whether to adopt one is for
the UI stack specification.

**The small script.** One own file, loaded as a static asset, with three duties: keep the
transcript pinned to the bottom while the reader is there (`DH-LIB-REACT-R8`); save and
restore the per-tab store; dispatch keyboard shortcuts. It holds no application data, so it
has nothing to resynchronise after a reconnect.

**Ownership and cleanup.** Live render state is owned by the component that owns the
session stream, keyed by message, and released when the message settles, the turn ends
or that component's fiber unloads. A connection's cursor dies with the connection. The
script store is the browser's; the host never reads it.

**Failure.** Two cases are kept apart, as area C4 specifies
([../areas/ui-conversation.md](../areas/ui-conversation.md) §6.3). If drawing one node
fails or panics, the renderer itself sends a generic "could not render this item"
fragment for that node, logs the event type and sequence number, and returns no error,
so the stream stays open: one bad node must not blank a transcript, and closing the
connection for a failure that repeats on every resume would loop. If the renderer cannot
read what it needs (a store error), it returns the error; the stream hub of area A19
then closes the connection and the tab resumes from its cursor. If a live update is
lost, the settled fragment corrects the page. If the page and the stream disagree after a
disconnect, `reset` re-fetches the transcript region.

## 7. Risks and open questions

- **Settled by the UI stack specification** ([go-ui-stack.md](go-ui-stack.md)): the
  template engine is `html/template` (`DH-ADR-GOUISTACK-1`) and htmx is one vendored
  file. Still to confirm in a real browser: that an out-of-band swap ignores a target id
  that is not in the page, which `DH-LIB-REACT-R2` relies on. No morphing swap is adopted.
- **Browser features.** The dialog, popover and scroll-anchoring features are stated
  from general knowledge. There is no desktop webview to pin
  ([DH-ADR-18](../design.md#dh-adr-18)): the engine is whatever browser the user has.
  Area C2 states the required feature set and shows a banner where it is missing
  ([DH-ADR-UIKIT-3](../areas/ui-kit.md#dh-adr-uikit-3)). The owner decided which browsers
  are supported: Chromium-family fully, current Firefox and Safari with a smaller
  automated set, releases since 2024 ([DH-ADR-39](../design.md#dh-adr-39)).
- **Latency of preference changes** for a browser that is not on loopback is unmeasured.
- **Scroll choreography.** Upstream animates folding rows and the scroll position on one
  frame clock (`packages/client/ui-chat/README.md:171-177`). This design keeps the
  outcome (the reader's line does not jump) and drops the choreography. If the plain
  version reads badly, the transcript area may add it to the small script.
- **New harness code.** The node-renderer registry, the live render state and the small
  script are own code in `internal/ui`; area C4 schedules the first two
  ([../areas/ui-conversation.md](../areas/ui-conversation.md)) and area C1 the script
  ([../areas/ui-core.md](../areas/ui-core.md)).

## 8. Verification

All tests are local. Tests marked "golden" compare rendered HTML with a committed file
and need no browser. Behaviour that only a real browser can show (scroll anchoring,
focus, the swap of a missing target) is a real-environment scenario owned by the UI
areas, and is not counted here. Nothing has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-REACT-1` | local, golden | `DH-LIB-REACT-R1`, `-R2` | A recorded session renders to a full page whose nodes carry the expected ids; replaying its events one at a time and applying each fragment to the previous HTML gives the same document as one full render |
| `DH-T-REACT-2` | local | `DH-LIB-REACT-R3` | For a reply fed in chunks of 1, 3, 7 and 16 bytes, frozen blocks plus tail at every step equal a fresh render of the same prefix; total bytes sent stay within a constant factor of the final HTML size |
| `DH-T-REACT-3` | local | `DH-LIB-REACT-R3` | Two connections on one message, one of which skips most ticks, end with identical HTML; a connection that joins mid-message receives all frozen blocks once |
| `DH-T-REACT-4` | local | `DH-LIB-REACT-R5` | Each of session, view and side panel is restored from the URL alone by a request with no cookies of state and no script |
| `DH-T-REACT-5` | local | `DH-LIB-REACT-R6` | A preference changed through one principal's request is in the next page rendered for the same principal on another connection |
| `DH-T-REACT-6` | local | `DH-LIB-REACT-R4` | Accepted prompt: response holds an empty composer and a pending row; the durable event's fragment removes that row; a second POST with the same request id creates no second row; a refused prompt returns an error status and no composer fragment |
| `DH-T-REACT-7` | local, golden | `DH-LIB-REACT-R7`, `-R10` | For every registered node renderer, an update fragment's target is a live region and never contains the node's disclosure element; the page with scripts removed still contains the transcript and a working prompt form |
| `DH-T-REACT-8` | local | `DH-LIB-REACT-R9` | A check over the style sheets rejects literal colours outside the token files and hover rules outside a hover media query |

`DH-LIB-REACT-R8` has no local test: it is browser behaviour and is proven only by a UI
area scenario.

## 9. Units dispositioned by this spec

- `UP-DEP-react`
- `UP-DEP-react-dom`
- `UP-DEP-zustand`
- `UP-DEP-immer`
- `UP-DEP-use-sync-external-store`
- `UP-DEP-clsx`
