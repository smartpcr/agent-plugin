# @tanstack/react-virtual: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". **No performance claim in this document is measured.** Each one
> is marked, and each becomes true only when the test named beside it passes.

| | |
|---|---|
| **Library ID** | `DH-LIB-REACTVIRTUAL` |
| **Upstream unit** | `UP-DEP-@tanstack/react-virtual` |
| **Upstream version** | 3.14.10 |
| **Kind** | bundled into the client; shared by every client plugin as a platform module (`packages/client/web/src/platform.ts:10`) |
| **Used by** | `client/ui-chat`, `client/ui-trajectory`, `experimental/session-inspector`; `client/web` and `apps/web` only re-export it |
| **Go choice** | **own code**: the host renders bounded windows addressed by sequence number; the browser's own layout skipping does the rest. No library |
| **Related areas** | Transcript, trajectory and session-inspector UI areas; [react.md](react.md) for how fragments are swapped; `DH-ADR-WIRE-1` and `-3` in [upstream/wire-and-api.md](../upstream/wire-and-api.md) |

## 1. What it is and why upstream needs it

**Virtualising** a list means keeping only the rows near the visible area in the page and
replacing the rest with empty space of the right height. A browser slows down with the
number of elements it must lay out, so a list of thousands of rows is kept fast by never
creating most of them. `@tanstack/react-virtual` computes which rows to mount as the user
scrolls.

A reader would expect the chat transcript to be the list that needs this. **It is not.**
Upstream virtualises three secondary lists and keeps the transcript fast by two other
means: it loads history a page at a time, and it folds finished work.

## 2. How upstream uses it

| Use site (`path:line`) | What is virtualised | API used |
|---|---|---|
| `packages/client/ui-chat/src/client/chat/TurnNavigator.tsx:6`, `:110-122` | The **turn rail**: one small mark per turn beside the transcript, at a fixed pitch, with a custom scroll function | `useVirtualizer`, `defaultRangeExtractor`, `elementScroll`, `observeElementOffset` |
| `packages/client/ui-trajectory/src/client/TrajectoryTable.tsx:7`; `packages/client/ui-trajectory/README.md:74` | The **Trajectory table**: starts from the last 50 nodes, mounts "only the visible row window plus a small overscan", keeps row keys across prepends, follows the bottom on append | `useVirtualizer` |
| `packages/experimental/session-inspector/src/client/views/InspectorTable.tsx:4` | The experimental session inspector's event table | `useVirtualizer`, `defaultRangeExtractor` |
| `packages/client/web/src/seed.ts:8`, `:44` | Shared with client plugins as a platform module | whole module |

**How the transcript itself is bounded.**

| Mechanism | Evidence (`path:line`) |
|---|---|
| The page opens on the tail of the session; older history arrives one page at a time through an explicit "Load earlier" control | `docs/subsystems/web-client.md:50`; `benchmarks/long-session-browser/README.md:15` |
| "The transcript reflects the loaded Session window"; the turn rail is wider than the window because it merges loaded turns with a host projection of all turns (`turnOutline`) | `packages/client/ui-chat/README.md:236` |
| Finished turns fold their work rows, leaving the replies | `packages/client/ui-chat/README.md:119` |
| A jump to a turn outside the window pages history in until it arrives | `TurnNavigator.tsx:14-15` |
| A long highlighted code block is **not** virtualised: "Final DOM cardinality therefore still follows the fence's token count" | `packages/client/ui-primitives/README.md:245` |

**What upstream measures.** These are the scenarios a Go design must not make worse.

| Benchmark | Scenario | Upstream expectation (limit with 1.25× headroom) | Evidence |
|---|---|---|---|
| `benchmarks/long-session-browser` | Chromium opens a synthetic 240-turn session with 20 code blocks and 40 tool results | open 900 ms (1125 ms) | `README.md:5`, `:15`, `:19` |
| same | Load every older page; the slowest page is judged | 700 ms (875 ms) | `README.md:15` |
| same | First visit to Trajectory | 520 ms (650 ms) | `README.md:15` |
| same | Type a draft while a reply of 120 text deltas streams at 16 ms pacing | the first keystroke must land while the reply is unfinished | `README.md:15`, `:19` |
| `benchmarks/conversation-fold` | Fold 200 replies whose 500,000 streamed deltas are compacted into 1,600 records, in the client | 16 ms | `conversation-fold.bench.client.ts:16-31`, `:40` |

## 3. Tech debt upstream carries

Search performed: `patches/`, `pnpm-workspace.yaml`, and the "Known Limitations" of the
three consuming packages. No patch or pin; no `UP-DEBT` unit is assigned here.

| Item | What it is | Evidence (`path:line`) | Effect of the Go design |
|---|---|---|---|
| A jump to an old turn pages through everything between | The client can only extend its one contiguous window | `TurnNavigator.tsx:14-15` | The host renders a window around any turn directly (`DH-LIB-REACTVIRTUAL-R3`) |
| Tests work around the library in a simulated DOM | jsdom fires no `scrollend`, so the virtualiser falls back to a timer | `apps/web/tests/trajectory-image-display.expected.e2e.ts:17` | No client virtualiser to simulate |
| Loaded pages are never released | The window only grows while a session is on screen | `packages/client/ui-trajectory/README.md:74` ("without evicting its prefix") | Carried as a stated limit (`DH-LIB-REACTVIRTUAL-R5`) |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-REACTVIRTUAL-R1` | Opening a session renders a bounded tail of the conversation, cut on a turn boundary, whatever the session's total length. The cost of opening depends on the page size, not on the session size | `web-client.md:50`; `long-session-browser/README.md:5` |
| `DH-LIB-REACTVIRTUAL-R2` | Older history is fetched one page at a time, addressed by sequence number. Pages never overlap and never leave a gap, including when events commit while a page is being fetched | `conversation.md:276`; `DH-ADR-WIRE-1` (`history?before={seq}`) |
| `DH-LIB-REACTVIRTUAL-R3` | The reader can jump to any turn of the session, loaded or not, in one request | `ui-chat/README.md:236`; `TurnNavigator.tsx:14-15` |
| `DH-LIB-REACTVIRTUAL-R4` | The body of a folded turn and of a collapsed tool card is not part of the page until the reader opens it | `ui-chat/README.md:119` |
| `DH-LIB-REACTVIRTUAL-R5` | The number of elements the browser must lay out is bounded by what is near the viewport, not by what has been loaded. Where that cannot be guaranteed the limit is stated to the user, not hidden | `ui-trajectory/README.md:74`; `ui-primitives/README.md:245` |
| `DH-LIB-REACTVIRTUAL-R6` | Tables of events (Trajectory, session inspector) page and filter on the host; a filter or search never needs the whole session in the browser | `ui-trajectory/README.md:74` |
| `DH-LIB-REACTVIRTUAL-R7` | The scenarios of section 2 have budgets enforced as tests against the built binary | `benchmarks/long-session-browser/README.md:15`; `DH-ADR-TOOLCHAIN-14` |

## 5. Go choice

**Decision.** No virtualisation library. Recorded as
[DH-ADR-REACTVIRTUAL-1](#dh-adr-reactvirtual-1).

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Host paging by sequence number, lazy fold bodies, CSS layout skipping** (chosen) | No script; the page size bounds the response; a jump is one request | The browser keeps every loaded page; very long reading sessions grow the page | No module. `content-visibility` support in each supported browser engine is not verified |
| A virtualising island (TanStack Virtual core, or a small own script) | A hard bound on mounted rows | Needs row heights that a server-rendered row does not know in advance; rows scrolled away would be removed and re-fetched, which breaks find-in-page and text selection across rows; more script than the rest of the UI together | Not a Go module; no build step is available to bundle it |
| Infinite scroll with eviction of far pages | Hard bound without measuring heights | Scroll position must be re-anchored on every eviction; find-in-page breaks | No module |

**Tradeoff.** Find-in-page and selection work over everything loaded, which virtualised
lists cannot offer; the price is that the page grows while the reader keeps loading.

<a id="dh-adr-reactvirtual-1"></a>
### DH-ADR-REACTVIRTUAL-1 Long lists are bounded by host paging, not by a client virtualiser

- **Decision.** Four mechanisms, all on the host or in CSS:
  1. **Tail page.** A session opens on its last *N* turns. *N* is a host setting.
  2. **Keyset paging.** "Load earlier" requests the page before a sequence number and
     inserts it above; the response is cut on a turn boundary.
  3. **Windows around a target.** A jump to turn *T* requests a window around *T*. If that
     window does not reach the tail, the transcript region shows controls at both ends
     and stops following live output until the reader returns to the tail.
  4. **Lazy bodies.** A folded turn or collapsed tool card is rendered as its summary; its
     body is fetched the first time it is opened.

  In addition each turn container is styled so that the browser may skip layout and paint
  for turns far from the viewport (the CSS `content-visibility` property with a
  remembered size).
- **Why.** Upstream's own transcript is bounded by paging and folding, not by
  virtualisation (section 2). A server that renders any window directly needs no client
  bookkeeping to do the same, and it removes the "page through everything" jump.
- **Verdict on upstream.** *Keep* tail-first loading, the explicit "Load earlier" control
  and folding. *Change* jumps to a direct window. *Drop* client virtualisation of the
  rail and the tables.
- **Tradeoff or constraint.** Inference, unmeasured: layout skipping bounds rendering
  cost but not memory, and an engine without `content-visibility` degrades to laying out
  everything loaded. A single very long code block still produces one element per token.
  The turn rail for a session of several thousand turns is several thousand small
  elements or one drawn strip; the transcript area decides after measuring.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14)
  ("anything that grows with history is bounded"); `DH-LIB-REACTVIRTUAL-R1` to `-R6`.
- **Verified by.** `DH-T-REACTVIRTUAL-1` to `-5`.

## 6. Design notes

**Page boundaries.** A page is a run of whole turns. The handler reads events backwards
from the cursor until it has *N* turn starts, then renders forwards. The response carries
the sequence number of its first event, which becomes the next cursor, so the browser
never computes a boundary. Because the cursor is a durable sequence number, a page
fetched while the session is running cannot overlap the live stream: live events have
higher numbers and arrive on the event stream.

**Nodes whose start is outside the window.** A tool call that started before the page
boundary and finished inside it is rendered from its folded state when its page loads
(`DH-LIB-REACT-R2` in [react.md](react.md)). The host has the whole log, so it does not
need upstream's "pending context" bookkeeping (`docs/subsystems/conversation.md:16`).

**Cost on the host.** Rendering a page is a read of a bounded number of events plus
Markdown rendering. Inference: a cache of rendered fragments keyed by session, sequence
number and renderer version would make "Load earlier" and reconnect replay cheap, because
a durable event's fragment is a pure function of the log up to it (`DH-ADR-WIRE-3`). The
cache must be bounded and owned by the UI component's fiber. Whether it is needed is a
measurement question for `DH-T-REACTVIRTUAL-5`.

**Failure.** A history request that fails leaves the "Load earlier" control in place with
an error state; nothing already on the page changes. A window request for a turn that no
longer exists (a deleted session) returns the not-found page.

## 7. Risks and open questions

- **Budgets are not transferable.** Upstream's numbers measure a React client. The same
  scenarios must be re-measured for server rendering before any number is promised.
  Until then the numbers in section 2 are targets, not results.
- **`content-visibility` and scroll anchoring** are unverified in the browser engines
  the product supports. There is no desktop webview
  ([DH-ADR-18](../design.md#dh-adr-18)); the real-browser scenarios of area C4 check
  them in Chromium, Firefox and Safari
  ([../areas/ui-conversation.md](../areas/ui-conversation.md)).
- **Page growth.** This design, like upstream, never releases loaded pages. If
  measurement shows a problem, eviction of far pages is the next step, at the cost
  described in section 5.
- **Typing while streaming.** Upstream measures that a keystroke lands during a streaming
  reply. Inference: with the composer outside the swapped region and updates limited to
  the tail, the browser has less work per frame than upstream; this is unmeasured.

## 8. Verification

Local tests exercise handlers with a fixture session. `DH-T-REACTVIRTUAL-5` is a
performance test in the `test/bench` layer and needs a real browser; it is listed here
because it is the gate for this decision. Nothing has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-REACTVIRTUAL-1` | local | `DH-LIB-REACTVIRTUAL-R1` | For fixture sessions of 10, 240 and 5,000 turns, the open response contains exactly the last *N* turns and its size does not grow with the session |
| `DH-T-REACTVIRTUAL-2` | local | `DH-LIB-REACTVIRTUAL-R2` | Walking "Load earlier" to the start yields every sequence number exactly once; events appended between two page requests appear in neither page twice nor are skipped |
| `DH-T-REACTVIRTUAL-3` | local | `DH-LIB-REACTVIRTUAL-R3` | A window request around a turn in the middle returns that turn, both end controls and no live-follow marker; around the last turn it returns the tail form |
| `DH-T-REACTVIRTUAL-4` | local, golden | `DH-LIB-REACTVIRTUAL-R4`, `-R6` | A folded turn's fragment contains its summary and a fetch address, and no body; the body request returns it; a table filter returns only matching rows with a correct next cursor |
| `DH-T-REACTVIRTUAL-5` | performance, built binary, real browser | `DH-LIB-REACTVIRTUAL-R5`, `-R7` | The four `long-session-browser` scenarios of section 2 on an equivalent synthetic session, with element counts and timings recorded; budgets are set from the first measured runs and then enforced |

## 9. Units dispositioned by this spec

- `UP-DEP-@tanstack/react-virtual`
