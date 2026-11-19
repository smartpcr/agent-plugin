# The conversation view (transcript, tool cards, questions, approvals, plan and goal): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) (`2e39e70`) and are cited as `path:line` relative to the upstream root. Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `UICONV`. Nothing here has been built, run, measured or tested.

| | |
|---|---|
| **Area key** | `UICONV` (agent C4) |
| **Upstream packages** | 6, all under `packages/client`: `ui-conversation`, `ui-tool`, `ui-user-questions`, `ui-approval`, `ui-plan`, `ui-goal` |
| **Serves outcomes** | [DH-O-12](../design.md#dh-o-12) first; also [DH-O-7](../design.md#dh-o-7) (safe rendering, approvals, grants), [DH-O-3](../design.md#dh-o-3), [DH-O-5](../design.md#dh-o-5), [DH-O-9](../design.md#dh-o-9), [DH-O-14](../design.md#dh-o-14) |
| **Depends on areas** | A19 ([api.md](api.md): routes, event stream, resume), A01 and A02 ([core-session.md](core-session.md), [agent-loop.md](agent-loop.md): events, stream frames), A06 ([session-views.md](session-views.md): projections, quotes), A03 ([tools.md](tools.md): calls, results, views), A17 ([interaction-credentials.md](interaction-credentials.md): interactions, grants), A13 ([jobs-schedule-goal-plan.md](jobs-schedule-goal-plan.md): plan mode, goals), A15 (address guard), A08 (`textdiff`), A07 (spill), A23 (`egress`, `jsonscan`), C1 ([ui-core.md](ui-core.md): page shell, view contract, `ui.SafeHTML`, script, shortcuts, catalogue), C3 ([ui-chat.md](ui-chat.md): composer, reading plan, sent user text, tail following), C2 (primitives) |
| **Priority** | P0 for the transcript, the safe-HTML renderer with math, the live stream, the tool cards, and approvals with questions and grants: without them a browser shows nothing of a conversation, or shows it unsafely. P1 for remote images, plan, goal and long-session behaviour. Each deliverable states its one priority in [§8](#8-deliverables) |

## 1. Purpose

This area is what a person looks at while an agent works: the conversation. It turns a session's event log into a readable transcript, shows the reply while the model is still writing it, draws every tool call as a card, puts the agent's questions and approval requests in front of the person, and shows the plan and the goal the agent is working to. It owns one shared piece that every other UI area uses: **rendering model and tool output as safe HTML**. That piece is a security control, not formatting. The text comes from a model, from files it read and from programs it ran, and the product asks a person to approve actions on the same page; text that became markup could run script, make requests with the user's session, or imitate an approval prompt.

Upstream does this in the browser: a React application keeps a copy of the log and folds it into components. The Go host renders HTML ([DH-ADR-3](../design.md#dh-adr-3)), so the fold moves to the host and the browser keeps nothing. Without this area the event stream of [api.md](api.md) would have nothing to carry.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **Transcript** | The ordered view of one session's log that a person reads. | The log holds bookkeeping a person should not have to read. |
| **Node, node key** | One thing in the transcript (a message, a tool call, a notice). Its key is `(kind, id)`, where `id` comes from the event's own data, never from position. | An event that changes a node must find it without guessing "the latest unfinished one". |
| **Node definition** | A registered rule: which events belong to a node, how its state is folded from them, how it is drawn. | The component that owns a kind of event owns how it is shown. |
| **Evidence** | The events that belong to one node, in `seq` order. | A node's state is a pure fold of its evidence. |
| **Shell, live region** | The shell is the part of a node sent once (container, disclosure, header). A live region is a part with its own `id` that later events replace. | A disclosure the reader opened stays open ([libs/react.md](../libs/react.md), `DH-LIB-REACT-R7`). |
| **Row, process group** | A row is a node placed in a turn by area C3's reading plan (`chat.Planner`): input, process, reply or turn-level. A process group is the plan's unit of folding: consecutive work rows (reasoning, tool cards, retries). | Finished work folds away and replies stay; the rule has one owner ([ui-chat.md](ui-chat.md), `DH-ADR-UICHAT-8`). |
| **Live element, frame, frozen block, tail** | The live element shows the model attempt in flight. A frame is one update of it: blocks that can no longer change (frozen) are appended, and the unfinished tail is replaced. | Text grows without re-sending what is already shown. |
| **Settle** | The durable event for an attempt is committed and its node replaces the live element. | Only committed events are replayable ([design.md §4.6](../design.md#46-event-model)). |
| **Part** | A typed piece of rendered content: gated HTML, a code block, an image slot. | Controls (copy, load image) are drawn by trusted templates, never by the content renderer. |
| **Gate, allow-list** | The gate is the last step of rendering: a filter that keeps only listed elements, attributes and address schemes. | A defect in a renderer or a library must not reach the page. |
| **Card, frame, kind, body** | A card shows one tool call. The frame (title row, state, disclosure, children) is this area's. A kind is one of six closed layouts. A body is a template registered by the tool's owner. | Every call looks and behaves alike; only its content differs. |
| **Interaction region** | The region above the composer that lists pending approvals and questions. | A waiting agent must be visible without hiding the draft. |
| **Dock** | A small state region beside the composer: tasks, goal, plan control, "Continue". Queued input is area C3's `#pending` region. | State that is not part of the transcript but belongs to the session. |
| **`at`** | The `seq` a fragment is rendered as of: the requesting tab's stream cursor. | A page and the stream then agree, so each event is applied once. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.client.ui-conversation` | `dsh-client-ui-conversation` | definition and provider | The assembly engine (`ctx.uiConversation`): registries of event definitions, view builders and group definitions, one binding per session, turn and step locations; the shell (header, view tabs, widths); the composer (one Lexical editor per session, drafts, send state, queue and task docks); recognition of references in sent user text (`packages/client/ui-conversation/README.md:14`, `:31`, `:58`, `:103`, `:111`). |
| `UP-PKG-packages.client.ui-tool` | `dsh-client-ui-tool` | consumer and definition | Renders every tool call: the call tree with nested sub-calls, a keyed slot `tool.call.toolview` per tool name, the generic card and the built-in cards (`packages/client/ui-tool/README.md:12`, `:58`). |
| `UP-PKG-packages.client.ui-user-questions` | `dsh-client-ui-user-questions` | consumer | The question card in the composer seat, the timed wait, browser drafts, late replies, the answer bubble and the plan-review card (`packages/client/ui-user-questions/README.md:12`, `:44`). |
| `UP-PKG-packages.client.ui-approval` | `dsh-client-ui-approval` | consumer | The approval card: takes over the composer, shows the correlated tool detail, returns the decision (`packages/client/ui-approval/README.md:11`). |
| `UP-PKG-packages.client.ui-plan` | `dsh-client-ui-plan` | consumer | The plan-mode chip, plan cards at the end of a turn and plan documents in the sidebar (`packages/client/ui-plan/README.md:12`). |
| `UP-PKG-packages.client.ui-goal` | `dsh-client-ui-goal` | consumer | The goal strip above the composer (edit, pause, resume, clear) and the bubble that shows a `/goal` command (`packages/client/ui-goal/README.md:12`, `:28`). |

### 3.2 Behaviour

- **Assembly.** The browser holds a contiguous window of the log. One assembler per session offers each entry to every registered definition. `match(event)` returns an id and a role (`start` or `update`) from that event alone; the engine finds the context by `(kind, id)`, calls `start` once and `update` for each later match, and asks `buildViewNode` for the final node (`docs/subsystems/conversation.md:11`, `:15-20`, `:246`). With `D` definitions an event costs `D` matches and one keyed lookup; scanning the window is forbidden on the append path (`:285`). An update whose start is outside the loaded window stays pending until an older page supplies it (`:70`). A definition may claim an event exclusively for its view; two claims throw (`:250`).
- **Live output.** Model deltas arrive as client-only `assistant/live-chunk` events and fold through the same interface as durable events; the durable `assistant/message` or `assistant/attempt` embeds the whole stream for replay (`docs/subsystems/conversation.md:72`). Every chunk is folded at once, but a visible change is published after three browser animation frames; a structural change or a settlement publishes immediately (`packages/client/ui-conversation/src/client/contract/conversation.ts:199-200`; `src/client/conversation/assembly.ts:222-227`). An opt-in stress test streams 100,000 reasoning chunks (`.agents/notes/implemented/testing/2026-08-03-opt-in-reasoning-chunk-browser-stress.md:21`).
- **Window and grouping.** The window changes in three ways: replace, prepend an older page, append one event (`docs/subsystems/conversation.md:276-283`). A node declares its place in a turn (`exchange`, `input`, `turn`, `process`, or ordinary work), and a group definition turns consecutive work into expandable groups shared by three display densities (`:256-264`; `.agents/notes/implemented/architecture/2026-09-21-conversation-build-groups.md:9`). The transcript is not virtualised: it is bounded by paging and by folding finished work ([libs/tanstack-react-virtual.md](../libs/tanstack-react-virtual.md) §2).
- **Model text.** Markdown, code, math and terminal colour are rendered in the browser; source HTML is shown as text and React builds the elements, so nothing is sanitised ([libs/micromark.md](../libs/micromark.md) §1, §2). A remote image is shown as written, so the browser fetches it.
- **Tool cards.** A tool row has three phases, `preparing`, `start` and `result`, under one call id (`packages/client/ui-tool/src/client/contract/slots.ts:75-77`). Before dispatch a named call is one row whose summary grows as arguments stream (`packages/client/ui-tool/README.md:32`). Cards are derived in the browser from the raw `tool/call` and `tool/result` events and the persisted `result.meta`; the host's `presentCall` and `presentResult` are not used by the web client (`docs/cookbook/adding-a-tool.md:96`; `packages/client/ui-tool/README.md:58`). Presenters must be pure because they run on replay, and malformed input falls back to the generic card (`docs/cookbook/adding-a-tool.md:88-90`). Sub-calls render as children (`src/client/tool/ToolCallTree.tsx:84-86`); a diff shows nine rows before folding (`README.md:94`).
- **Questions.** The card takes the composer's seat; one request owns it at a time (`packages/client/ui-user-questions/README.md:28`, `:122`). A timed card counts down in the browser, and focus, editing or "Take time" hold it; at zero the agent continues and the question stays answerable, and a late answer becomes a steering message (`:52-54`). Unsent answers live in browser storage (`src/client/draft-store.ts:46`). A `plan-review` intent draws a compact card with "Request changes" and "Approve" (`README.md:44`).
- **Approvals.** A pending request takes over the composer. Enter in the detail region allows, Escape rejects (`packages/client/ui-approval/src/client/ApprovalCard.tsx:44-51`). When "Always allow" is offered it is the highlighted primary button (`packages/client/ui-approval/README.md:23`; `ApprovalCard.tsx:83-86`).
- **Plan and goal.** The chip appears only while plan mode is on and runs `/plan off` (`packages/client/ui-plan/src/client/index.ts:116-124`); entering is `/plan`. Submitted plans appear as cards at the end of the turn, derived from the `exit_plan_mode` call (`packages/client/ui-plan/README.md:36`, `:54`). The goal strip reads the `goal` projection and a process-local activation flag fetched by a call plus a live event, and each mutation carries the goal's id and revision (`packages/client/ui-goal/README.md:46`).
- **Composer and docks.** A default send waits for the host: the composer is read-only until the prompt is accepted, then the draft clears and an echo appears (`packages/client/ui-conversation/README.md:137`). The queue dock reads `next-turn` input from the `inbox` projection (`:127`). After one of six failure codes an idle composer offers "Continue generating" (`:85`).
- **Services, events, config, tools.** `ctx.uiConversation` is a browser-side service. None of the six packages registers a host event, a host config schema or a model-facing tool, and none persists anything on the host.

### 3.3 Failure, retry, rollback and cleanup upstream

- **A definition throws during an incremental update.** The assembler replays the whole loaded window and replaces every view; a definition that always fails replays the window on every publication (`docs/subsystems/conversation.md:283`; `packages/client/ui-conversation/README.md:238`).
- **A view is no longer shown.** The binding drops its fold when the last reference goes and folds the window again on return (`packages/client/ui-conversation/README.md:35`).
- **Settlement during streaming.** One unseen partial may be skipped; the final content is complete (`.agents/notes/implemented/testing/2026-08-03-opt-in-reasoning-chunk-browser-stress.md:17`).
- **Two browsers answer one question.** The gateway drops the late outcome silently; the card keeps its draft until the projection closes it (`packages/client/ui-user-questions/README.md:54`).
- **An approval is withdrawn or replaced.** One lock per request: the old card accepts no further answer (`packages/client/ui-approval/README.md:21`).
- **Reload.** Draft text and question drafts survive; draft attachments do not (`packages/client/ui-conversation/README.md:236`). A plan review that was not logged keeps its text only in the tab and loses it (`packages/client/ui-plan/README.md:38`).
- **A goal or plan change is refused.** The error is shown inline and the control keeps the host's value (`packages/client/ui-goal/README.md:36`; `packages/client/ui-plan/README.md:42`).

## 4. Upstream decisions dissected

The first 11 rows are the decision notes handed to this area, named by file under `.agents/notes/` (`impl/` is `implemented/`, `prop/` is `proposed/`). The rest are visible only in code and READMEs. `UICONV-n` is short for `DH-ADR-UICONV-n` in [§7](#7-decisions). The ledger rows hold the full reasoning.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `impl/architecture/2026-08-09-client-conversation-node-assembly` | An engine folds events into nodes by `(kind, id)`; businesses register definitions | Keep the model, move it | Identity from event data and a pure fold are what make replay equal live; the React key machinery is not needed | [UICONV-1](#dh-adr-uiconv-1) |
| `impl/architecture/2026-08-23-client-derived-tool-presentation` | Cards are derived from raw events where they are drawn; history reads run no presenter | Keep the rule, move the place | The store still never runs a presenter; the host's UI layer derives the card at render time | [UICONV-6](#dh-adr-uiconv-6) |
| `prop/architecture/2026-09-14-composer-model-and-draft-editor` | Split a session-owned draft model from per-view editors | Keep the split | It is the island rule: the island owns caret and DOM, nothing authoritative | [UICONV-8](#dh-adr-uiconv-8) |
| `impl/architecture/2026-09-21-conversation-build-groups` | Grouping is a registered business rule apart from the generic assembler; all densities share one result | Keep the split | The rule is area C3's pure plan and this area's renderer places nodes by it; the React identity constraints do not exist | [UICONV-2](#dh-adr-uiconv-2) |
| `impl/architecture/2026-09-30-structured-draft-initialization` | A new session may be prefilled; keep, replace or append to an existing draft; prefill never sends | Keep the behaviour | The host renders the initial draft into the page; the mechanism is C3's | [UICONV-8](#dh-adr-uiconv-8) |
| `impl/architecture/2026-10-09-composer-command-chip` | A recognised command is one atomic chip that carries its claim | Keep the behaviour | It replaced a text transform that looped in the editor library; the island's node model is C3's | [UICONV-8](#dh-adr-uiconv-8) |
| `impl/architecture/2026-10-10-user-text-json-nodes` | Registered rules find references in sent user text again each time it is shown; cached queries refresh when facts change | Replace | Area C3 records mention spans when a message is sent and draws them, with masked credentials, in its `usertext` view; nothing is recognised twice and there is no cache to refresh | [UICONV-8](#dh-adr-uiconv-8) |
| `impl/architecture/2026-10-12-exclusive-conversation-matches` | A definition may claim an event; two claims are an error naming both | Keep | The same double-rendering arises in any registry with a generic and a specific rule | [UICONV-1](#dh-adr-uiconv-1) |
| `impl/bug-fix/2026-11-13-draft-attachments-across-session-switches` | A draft with attachments keeps its session scope alive; a reload discards them with a notice | Replace | Attachments are stored on the host when added, so a draft holds references that survive a switch and a reload | [UICONV-8](#dh-adr-uiconv-8) |
| `impl/feature/2026-08-26-web-rich-text-quotes` | A transcript selection becomes an atomic quote in the editor; the sent message carries plain Markdown | Keep the behaviour | The sent form is plain Markdown quote lines (area A06's `Quote` and `RecognizeQuotes`); the editor node is C3's | [UICONV-8](#dh-adr-uiconv-8) |
| `impl/testing/2026-08-03-opt-in-reasoning-chunk-browser-stress` | Fold every chunk, publish once per frame, prove it with a stress run | Keep the goal, move the coalescing | The host bounds frames per connection; the stress run becomes a budget scenario | [UICONV-3](#dh-adr-uiconv-3) |
| An update whose start is not loaded stays pending | `docs/subsystems/conversation.md:70` | Drop | The host has the whole log and an index of node starts | [UICONV-1](#dh-adr-uiconv-1) |
| A failing definition replays the whole window | `docs/subsystems/conversation.md:283` | Change | One node shows "could not render"; nothing else is redrawn | [UICONV-1](#dh-adr-uiconv-1) |
| View targets, activation and the retain facet | `packages/client/ui-conversation/README.md:35`, `:47` | Drop | They manage a browser-side fold. A page is one view at one URL; Trajectory is another page (area C5) | [DH-ADR-3](../design.md#dh-adr-3) |
| Remote images are shown as written | [libs/micromark.md](../libs/micromark.md) §2 | Change | Owner decision | [UICONV-5](#dh-adr-uiconv-5) |
| A pending interaction replaces the composer; the first matching one wins | `packages/client/ui-conversation/README.md:160-203` | Change | All pending items are listed; the composer and its draft stay | [UICONV-7](#dh-adr-uiconv-7) |
| "Always allow" is the primary button when offered | `packages/client/ui-approval/README.md:23` | Change | Standing consent must be a deliberate act ([DH-ADR-16](../design.md#dh-adr-16)); nothing is stored across sessions | [UICONV-7](#dh-adr-uiconv-7) |
| Question drafts in browser storage | `packages/client/ui-user-questions/src/client/draft-store.ts:46` | Keep | One of the four places for view state ([DH-ADR-REACT-1](../libs/react.md#dh-adr-react-1)) | [UICONV-7](#dh-adr-uiconv-7) |
| The plan chip shows `pending ? !active : active` | `packages/client/ui-plan/src/client/PlanModeControl.tsx:33` | Drop | Area A13 appends `plan/mode` at once, so there is no pending selection | [UICONV-8](#dh-adr-uiconv-8) |
| `/goal` runs are shown as "Command input" bubbles | `packages/client/ui-goal/README.md:32` | Keep, generalise | Every `command/run` event gets the same row | [UICONV-1](#dh-adr-uiconv-1) |
| Malformed card input falls back to the generic card; nine diff rows before folding; disclosures reset when a turn folds | `packages/client/ui-tool/README.md:82`, `:90`, `:94` | Keep | Display must never break a replay | [UICONV-6](#dh-adr-uiconv-6), [UICONV-2](#dh-adr-uiconv-2) |

## 5. Tech debt and its resolution

All 21 units are bullets from the six READMEs' "Known Limitations". They are grouped by cause; each unit's own reason is in its ledger row. `L:` stands for `UP-DEBT-limit.packages.client.`. "Carried" rows keep the limit on purpose and name stage [DH-S-59](../impl.md#dh-s-59).

| Group (cause) | Units | How the Go design answers it |
|---|---|---|
| **A. A client runtime of targets, slots and dictionaries.** No fallback view target, a failing definition replays the window, first-party cards colocated in one package, card copy in another package's dictionary | `L:ui-conversation.1`, `.7`, `L:ui-tool.3`, `.5` (4) | `.1` is not applicable: there is no view-target registry in a server-rendered page. The rest are resolved: a failing node costs one fallback fragment, a card body is registered by the tool's owner with its own message keys ([DH-D-UICONV-1](#dh-d-uiconv-1), [DH-D-UICONV-4](#dh-d-uiconv-4)). |
| **B. A conversation addressed by where it is rendered** | `L:ui-conversation.2` (1) | Resolved: every transcript fragment is addressed by session id in its URL, so any panel can show any session ([DH-D-UICONV-1](#dh-d-uiconv-1)). |
| **C. Composer state that lives in one browser document.** Blur verified in one engine, attachments lost on reload, a file drop taken by two composers | `L:ui-conversation.4`, `.5`, `.6` (3) | `.4` is not applicable: it describes upstream's composer style sheet. `.5` and `.6` are resolved by area C3's composer, which this area's page seats once per shown session: the draft and its staged uploads are host records, and a drop outside a composer goes to the page's one main composer ([UICONV-8](#dh-adr-uiconv-8); [ui-chat.md §6.2](ui-chat.md#62-contracts)). |
| **D. Product rules kept on purpose.** The fixed list of failure codes for "Continue"; a goal that outlives the agent preset that offered its tools | `L:ui-conversation.3`, `L:ui-goal.1` (2) | Carried. The list is area A19's admission rule and must not move with retry policy. A goal is session state; the strip says when the current agent preset has no goal tools and stays the way to pause or clear it. |
| **E. Nested and historical tool calls.** One level of nesting in production; old nested calls without location facts | `L:ui-tool.1`, `.2` (2) | `.1` resolved: the frame nests by the logged `parent` to whatever depth area A03's bound allows. `.2` not applicable: no upstream session is read ([DH-ADR-11](../design.md#dh-adr-11)). |
| **F. Links in cards ignore the link setting** | `L:ui-tool.4` (1) | Resolved: every link is written by one helper of the render package, and with no embedded browser pane ([DH-ADR-18](../design.md#dh-adr-18)) there is one behaviour. |
| **G. One interaction, in one place, in one browser.** Caller-owned grants, one question card at a time, questions only in the open session, the plan chip hidden during a review, drafts local to a browser | `L:ui-approval.1`, `L:ui-user-questions.1`, `.2`, `.3`, `L:ui-plan.2` (5) | Four resolved: grants are host state with closed outcomes, the region lists every pending item and names those of other sessions, and it does not cover the plan control ([DH-D-UICONV-6](#dh-d-uiconv-6), [DH-D-UICONV-7](#dh-d-uiconv-7)). `user-questions.1` is carried: unsent answers stay in the tab ([DH-ADR-REACT-1](../libs/react.md#dh-adr-react-1)); the question itself is host state. |
| **H. Plan and goal controls that are advisory or missing.** Plan mode is guidance, no control to enter it, no control for the round limit | `L:ui-plan.1`, `.3`, `L:ui-goal.2` (3) | Resolved: area A13 enforces plan mode (`DH-ADR-JOBS-11`) and the control states the enforcement; the control is shown in both states; the goal form edits the round limit ([DH-D-UICONV-7](#dh-d-uiconv-7)). |

Totals: 15 resolved, 3 not applicable, 3 carried.

## 6. Go design

### 6.1 Shape

All packages are under `src/harness/internal/ui`, the family [design.md §4.2](../design.md#42-go-module-layout) gives to the UI areas. Views follow area C1's contract as written ([ui-core.md §6.1](ui-core.md#61-shape)): a registered `ui.View[T]` gets one typed value and `ui.Req`, never a service; templates use only the shared helpers (`t`, `region`, `live`, `domid`, `reqid`, `can`, `time`). UI text comes from the catalogue (`ui.Req.T`), with English and Chinese complete; model and tool text is never translated.

| Package | Component and service key | Why it exists |
|---|---|---|
| `render` | Component `ui.render`; `render.Key` (`Service`) | The only code that turns untrusted text into `ui.SafeHTML`: Markdown, code, terminal colour, math, the gate. One place to review, fuzz and replace a library. |
| `transcript` | Component `ui.transcript`; `transcript.Key` (`Service`). Registers the `transcript` projection with area A06 and the renderer with area A19's hub | The node registry, the fold, page and event fragments. It also owns the session page's main view and declares its places with `Shell.Declare`: `conversation.docks` (List; C3's queue dock `#pending` is one contribution), `conversation.composer` (One, filled by C3), `conversation.turn-tail` (List: plan cards, and other areas' end-of-turn cards), `conversation.turn-footer` (List, scope: one settled turn; C3's rating, copy and usage controls) and `conversation.reasoning` (Wrap, around one reasoning body, its data the block's text and event `seq`; the experimental translation control of [x-recommendations-misc.md](x-recommendations-misc.md) is its one contributor). |
| `transcript/live` | Part of `ui.transcript` | The live render state of each attempt in flight, fed by `agent.StreamFrame`. |
| `toolcard` | Component `ui.toolcard`; `toolcard.Key` (`Registry`) | The card frame, the six closed kinds, and the registry of bodies by tool name. |
| `interaction` | Component `ui.interaction`, no service | Views of pending approvals, consents, questions and plan reviews; the grants strip. |
| `docks` | Component `ui.docks`, no service | Task list, goal strip, plan control and the "Continue" action, contributed to `conversation.docks`; the goal and plan routes. Queued and steering input is C3's `#pending` region. |
| `remoteimage` | Component `ui.remote-images`, no service | The click-to-load routes and their bounded cache. A composition without it shows placeholders that cannot load. |

```go
// package render. ctx is context.Context; Options{Session, Seq, Settled bool, LocalLinks bool}.
type Service interface {
    Markdown(src string, o Options) Rendered           // never fails: the worst case is escaped text
    Stream(o Options) *Stream                          // Append(text); Frozen(from int) []Part; Tail() []Part
    PlainText(src string) string                       // titles, summaries, notifications
    Code(src, langOrPath string) ui.SafeHTML           // class-based colour, bounded
    Terminal(out []byte) ui.SafeHTML                   // ANSI colour and line replay
    Math(tex string, display bool) (ui.SafeHTML, bool) // false: show the source
}
type Rendered struct{ Parts []Part; Images []ImageRef } // ImageRef{N int; Address, Alt string}
type Part interface{ part() }                           // closed: HTML | CodeBlock | ImageSlot | HostImage
// Every SafeHTML above is made by C1's ui.FromSanitizer, which adds the hx-disable container itself.

// package transcript
type NodeDef struct {
    Kind  string                                       // unique; the namespace of its ids
    Types []string                                     // event types offered to Match; required, no catch-all
    Place Place                                        // InFlow (default: the row C3's plan gives its first event; an event type the plan does not know is a process row) | TurnTail (the turn's conversation.turn-tail)
    Match func(ev event.Event) (id string, role Role, claim, ok bool) // reads this event only
    Fold  func(prev State, ev event.Event) State       // pure; evidence in seq order, the start first
    View  func(s State, env ui.Req) Node               // Shell, Regions, Summary; no I/O. Placement is C3's plan
    Body  func(s State, part string, env ui.Req) (ui.SafeHTML, error) // optional lazy body
}
type Service interface {
    Register(owner *cordis.Context, d NodeDef) error   // an effect of owner
    Page(ctx, env ui.Req, v query.Visibility, s session.ID, w Window, at event.Seq) (Page, error)
    Body(ctx, env ui.Req, v query.Visibility, s session.ID, k NodeKey, part string, at event.Seq) (ui.SafeHTML, error)
}
// What this area passes to stream.Hub.SetRenderer; api.md names the type and not its methods.
type Renderer interface {
    Events(ctx, env ui.Req, s session.ID, batch []event.Event) (ui.SafeHTML, error) // a node failure is not an error
    Live(env ui.Req, s session.ID, sent LiveCursor) (frame ui.SafeHTML, next LiveCursor)
}
// package toolcard
type Facts struct{ Call, Parent tools.CallID; Name string; Args json.RawMessage; State State; Result *Result; Cwd, Home string }
type Card struct{ Call, Result func(Facts) tools.View; Body func(Facts, ui.Req) (ui.SafeHTML, error) } // each optional
type Registry interface{ Register(owner *cordis.Context, tool string, c Card) error }
```

**How events become nodes.** For each committed event the registry asks the definitions registered for its type. A match gives the node key and a role. A `start` creates the node; an `update` adds to its evidence. A node's state is `Fold` over its evidence up to the event being drawn, and `View` turns state into a shell and live regions. The renderer never scans the log to find a node: the `transcript` projection (a row projection in area A06's registry) writes one small row per multi-event node, `(session, node key) -> kind, start seq, evidence seqs, turn`, in the same transaction as the event, so an update finds its start with one indexed read wherever it is in the log. Evidence is bounded per node (`MaxEvidence`); because a state-carrying event holds the whole value ([design.md §4.6](../design.md#46-event-model)), the start plus the newest entries are enough.

**Where a node goes.** This area does not decide which rows fold or what a reader must not lose: area C3's `chat.Planner` does ([ui-chat.md](ui-chat.md), `DH-ADR-UICHAT-8`). The renderer gives the plan the turn's events and draws each node into the container the plan's row names (turn, exchange, process group), using C3's turn container, process group, history control and footer views; a row the plan hides is not drawn. The `transcript` projection keeps, per open turn, what a per-event call needs (the turn's start `seq` and the plan's last row), so that drawing one event does not read the whole turn again; this needs an incremental form of `Plan` ([§10](#10-open-questions-and-limitations), item 10).

**Page and stream agree.** A page request (first load, "Load earlier", a jump, a lazy body) is rendered as of `at`, the tab's cursor: evidence after `at` is ignored. An event fragment for `seq` *n* uses evidence up to *n*. So applying the stream's fragments in order to a page rendered at any earlier cut gives the page rendered at the later cut; `DH-T-UICONV-1` asserts this for every cut of the fixture sessions. A fragment appends a new node to its turn or process group (an out-of-band `beforeend` on the container's id) or replaces one live region by id. A target that is not on the page is ignored by htmx ([libs/go-ui-stack.md](../libs/go-ui-stack.md) §7.1), and the node appears in its current state when its page is loaded. An event with nothing to draw (a request header, a step bracket) yields no fragment; the cursor still moves ([api.md §6.2](api.md#62-contracts)).

**Built-in node kinds.** Other areas register kinds for their own events (jobs, subagents, compaction) through `Register`.

| Kind | Evidence | Shows |
|---|---|---|
| `turn` | `turn/start`, `turn/end` | C3's turn container; at the end its footer, given the reason (`completed`, `max-tokens`, `blocked`, `aborted`, `error`, `budget`, `interrupted`). |
| `user` | `user/message` | The text through C3's `usertext` view (mention chips, masked credentials), attachments through C3's gallery; a notice form for sources `retry`, `schedule`, `job`, `goal`, `plan`. |
| `assistant` | `assistant/message`, `assistant/attempt` | Reasoning (collapsed), text, tool-call slots; an interrupted prefix is marked; a failed attempt is a retry row with its failure. |
| `tool` | `tool/call`, `approval/asked`, `approval/decided`, `tool/result`, `tool/late-return`, `tool/subcall`, `tool/subresult` | The card ([§6.2](#62-contracts)). |
| `question-reply` | a `user/message` that answers a question (claims the event) | The answers beside their questions. |
| `command` | `command/run`, `command/done` | "Command input" and its result. |
| `notice` | `plan/mode`, `permission/preset`, `approval/policy`, `grant/created`, `grant/ended`, `model/selection`, a tool-change `developer/message` | One line: what changed and who changed it. |
| `plan` | the `exit_plan_mode` call and its result | A plan card with "Open", contributed to `conversation.turn-tail`. |

**The safe-HTML pipeline** ([DH-ADR-UICONV-4](#dh-adr-uiconv-4)) applies the four controls of [DH-ADR-DOMPURIFY-1](../libs/micromark.md#dh-adr-dompurify-1) with the modules [libs/micromark.md](../libs/micromark.md) verified. (1) Bound the input (`MaxBytes`); over the bound the text is shown escaped with a cut marker. (2) Parse with goldmark and the own extensions of that specification. (3) Render through an escaping writer into **parts**. A top-level block becomes an `HTML` part. A fenced block becomes a `CodeBlock` part: the body is tokenised by chroma and written as class-named spans, and the card header with its language label and copy control is drawn by a trusted template. A remote image becomes a numbered marker in the text and an `ImageSlot` after its block. (4) Every `HTML` part and code body passes the bluemonday gate as the last statement of the function. (5) The gated bytes become a value only through C1's `ui.FromSanitizer`, which wraps them in a container carrying `hx-disable` ([ui-core.md](ui-core.md), `DH-ADR-UICORE-3`); a view places controls between such containers, outside every disabled subtree.

**The live message** ([DH-ADR-UICONV-3](#dh-adr-uiconv-3)). `transcript/live` listens to `agent.StreamFrame`. `Start` creates the render state for the attempt: per content block a `render.Stream` (text and reasoning) or a preparing row (a tool call, shown once its name is known, its growing arguments read through area A23's `jsonscan`). Each `Chunk` appends. The hub asks `Live(env, s, sent)` when a connection can be written; the answer is the frozen parts after `sent`, to be appended, and the tail, to replace the previous tail. The live element is the last child of the open turn's container. `End` with a `seq` releases the state; the `session` fragment for that `assistant/message` carries the settled node and an empty live element in one fragment, so the browser makes both changes in one swap. `End` with `abandoned`, a turn end, a disposed agent or an unload also release it.

**Tool cards** ([DH-ADR-UICONV-6](#dh-adr-uiconv-6)). The `tool` node builds `Facts` from logged events only and looks the tool name up in `toolcard.Registry`. A component that registers a tool also registers its card, at global scope whatever the scope of the tool registration, so a cold session draws the same cards without an agent. `toolcard.FromSpec` adapts a declaration's `PresentCall` and `PresentResult` ([tools.md §6.1](tools.md#61-shape)): it decodes the logged arguments leniently and returns a `tools.View` of one closed kind. A `Body` is for a tool no kind fits (the question row, the task list, a subagent receipt). No entry, a decoding failure or a panic gives the generic card.

**Interactions, docks and grants** ([DH-ADR-UICONV-7](#dh-adr-uiconv-7), [-8](#dh-adr-uiconv-8)). The interaction region is drawn from area A17's `Registry.Pending(v)` and re-drawn on `interact.Changed`, on the `interaction` stream event. The docks and the grants strip are state regions on the `view` event; each is written with C1's `live` helper, which registers the tab's interest and lets the region fetch itself again after `reset`.

### 6.2 Contracts

**Routes.** Registered through `web.Routes` under the rules of [api.md §6.2](api.md#62-contracts), which area A19 owns: resource ids in the path, a declared access rule, a host-minted request id on every state-changing form. `S` is `access.Session("sid")`. Each handler decodes, calls the owning service and renders a view.

| Method and pattern (name) | Returns and rules |
|---|---|
| `GET /sessions/{sid}` and `GET /sessions/{sid}/events?before={seq}` (area A19's `session.open`, `session.history`) | This area supplies `transcript.Page` for the window A19 cuts. Requested of A19: an `at={seq}` parameter and an `around={seq}` window for jumps. |
| `GET /sessions/{sid}/nodes/{key}/body?part=&from=&at=` (`conversation.body`) | Fragment: the rows of a closed process group, the body of a collapsed card, or the next page of long output. `{key}` is opaque. Access `S`. Activation `None`. |
| `POST /sessions/{sid}/remote-images` (`conversation.image-load`) | Action. Form: `seq`, `n`, request id. The host reads the address from the committed event; the form cannot carry one. Returns the slot region. Access `S`. |
| `GET /sessions/{sid}/remote-images/{key}` (`conversation.image`) | Download of cached raster bytes: `nosniff`, a sandboxing policy, `Cache-Control: private`. Access `S`. |
| `GET /sessions/{sid}/docks`, `GET /sessions/{sid}/interactions` (`conversation.docks`, `conversation.interactions`) | Fragments: how the two regions fetch themselves after `reset`. Access `S`. Activation `None`. |
| `POST /interactions/{iid}` and `POST /interactions/{iid}/hold` (area A17 registers them) | This area supplies the forms and the settled view. |
| `GET`, `POST /sessions/{sid}/questions/{call}` (`question.read`, `question.reply`) | Read-only review of a settled question; a late answer through `question.Service.Reply`. `{call}` is opaque. Access `S`; `MayResume` for the reply. |
| `GET /sessions/{sid}/grants`, `DELETE /sessions/{sid}/grants/{gid}` (`grant.list`, `grant.revoke`) | The grants strip; revoke calls `Grants.Revoke` with the admitted actor. Owner. |
| `PUT /sessions/{sid}/plan-mode` (`plan.set`) | `plan.Controller.Set(ctx, agent, active, by: ui)`. Owner; `MayResume`. |
| `GET /sessions/{sid}/plans/{call}` (`plan.document`) | Fragment: the plan Markdown from the logged call, through `render`. Access `S`. |
| `PUT`, `DELETE /sessions/{sid}/goal`; `POST /sessions/{sid}/goal/pause`, `/resume` (`goal.edit`, `goal.clear`, `goal.pause`, `goal.resume`) | `goal.Service.Update` with the `{id, revision}` the form was rendered with; a stale revision is 409 with the current strip. `MayResume`. |

**Stream fragments** (events and recovery classes are area A19's).

| Event | What this area supplies |
|---|---|
| `session` (journal) | `Renderer.Events`: appended nodes and replaced live regions for the batch, in `seq` order. |
| `live` (state) | `Renderer.Live`: the frame for one connection. Whole partial message on connect; empty when no attempt is in flight. |
| `interaction` (state) | The region: every pending item of the shown session in ask order, and one line per other session with something waiting. |
| `view` (state), topics `conversation.docks` and `conversation.grants`, key: session | Whole regions, re-rendered on `projection.Changed` (`todos`, `goal`, `plan`, `continuation`), `goal.ActivationChanged`, `grant.Changed`. |

**Tool card states**, derived from logged events only.

| State | Evidence | Shown as |
|---|---|---|
| preparing | a tool-call block in the live stream, name known | A row in the live element with the growing argument summary. Not a node. |
| waiting for approval | `tool/call`, then `approval/asked` without a decision | "Waiting for approval"; the request itself is in the interaction region. |
| running | `tool/call`, and any approval decided `allowed-once` or `allowed-by-grant` | The pending view from the arguments. Inference: the log does not say whether a call is queued behind another, so the label is "running". |
| finished | `tool/result` without an error | The result view; collapsed, body fetched on open. |
| failed, denied, cancelled, not started | `tool/result` with a code: the body's own, `tools/denied` or `approval/rejected`, `tools/aborted*`, `tools/not-started` | The code's message and what decided it; never styled as success. |
| detached, outcome unknown | `tools/outcome-unknown` | A warning: the tool may still be running or may have taken effect; verify before repeating. A later `tool/late-return` adds "returned later" with success or failure. |

A nested call (`tool/subcall`, `tool/subresult`) is a child row under its `parent`. **Long output** is bounded by the pipeline before it is logged ([DH-ADR-TOOLS-4](tools.md#dh-adr-tools-4)); the body shows the first `OutputHeadLines` lines and pages the rest by `from`; a spilled result shows its preview and a link to the file view of its locator; a truncated result says so. **Diffs** come from the hunks in `result.meta` (before and after text per hunk), are laid out by area A08's `textdiff`, show `DiffFoldRows` rows before folding and the added and removed counts in the header, and say "replaced as a whole" for a coarse result. A pending write shows the diff its arguments imply.

**What the gate lets through.** Everything else is dropped. The vocabulary is the renderers' own, so a change the gate makes to renderer output is a defect, counted and logged with the event's `seq`.

| Class | Allowed |
|---|---|
| Text structure | `p`, `h1` to `h6`, `ul`, `ol`, `li`, `blockquote`, `pre`, `code`, `em`, `strong`, `del`, `hr`, `br`, `sup`, `span`, `table`, `thead`, `tbody`, `tr`, `th`, `td` |
| Links | `a` with `href` (absolute `http`, `https`, `mailto`, or a host path under `/sessions/{sid}/` that the renderer built for a local file in a settled message), fixed `rel="noopener noreferrer nofollow"` and `target="_blank"` |
| Images | `img` with `src` under `/sessions/{sid}/attachments/` or `/sessions/{sid}/remote-images/`, `alt`, `loading` |
| Math | Presentational MathML (`math`, `mrow`, `mi`, `mn`, `mo`, `mtext`, `mspace`, `msup`, `msub`, `msubsup`, `mfrac`, `msqrt`, `mroot`, `munder`, `mover`, `munderover`, `mtable`, `mtr`, `mtd`, `semantics`, `annotation`) with `display`, `mathvariant`, `encoding` |
| Attributes | `class` only with values from the fixed sets the renderers write (token, alignment, terminal colour, task and marker classes); `start` on `ol`; `colspan`, `rowspan` |
| Never | `id`, `name`, `style`, `hx-*`, `data-*`, `on*`, `form` and its controls, `script`, `style`, `iframe`, `object`, `svg`, `base`, `meta`, `link`, any other address scheme |

C1's script and C3's islands find the elements they act on by `data-*` attributes and stable ids (`data-draft`, `data-focus`, `data-scroll-key`), none of which the gate admits. Requested of C1 and C3 as a standing rule: no behaviour is ever keyed on a class name or an element type alone.

**Config.** Go structs with defaults as Go values; unknown keys fail the load. Numbers marked * are unmeasured starting points.

| Go struct | Fields and defaults |
|---|---|
| `transcript.Config` | `LiveMinInterval` 100 ms*; `MaxEvidence` 32; `OutputHeadLines` 200*; `DiffFoldRows` 9 |
| `render.Config` | `MaxBytes` 1 MiB*; `MaxCodeBytes` 256 KiB*; `MaxLineChars` 1,000 (longer lines are not coloured, as upstream); `Math` `mathml` or `source` (ships as `source` until `DH-T-KATEX-1` and `DH-T-KATEX-3` of [libs/micromark.md](../libs/micromark.md) have passed); `MathTimeout` 200 ms*; `MaxFormulaBytes` 16 KiB* |
| `remoteimage.Config` | `Enabled` true; `MaxBytes` 10 MiB*; `Timeout` 15 s; `MaxRedirects` 3; `Types` `png`, `jpeg`, `gif`, `webp`; `CacheBytes` 64 MiB*; `CacheTTL` 1 h; `AllowHosts` empty (an operator may list intranet hosts the address guard would refuse) |

**Events, services, tools, persisted data.** The six packages have no `UP-EVT`, `UP-CFG`, `UP-SVC` or `UP-TOOL` unit. This area defines no host event and exposes no tool to the model. It persists one derived thing: the `transcript` projection (table `transcript_node` and a small value per open turn), version-stamped, deleted with its session and rebuilt by area A06 when its version changes. Live render state, the image cache and tab state are memory only.

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership and access.** A node definition, a card, a declared place, a contribution, a shortcut command and a route are effects of the fiber that registered them. Live render state is owned by the `ui.transcript` fiber, keyed by session and attempt. The image cache is owned by the `ui.remote-images` fiber. A pending interaction is owned by area A17's registry and a grant by the live agent of its session; this area's views have access to list them and, for an owner principal, to answer or revoke, and own neither. The browser owns its scroll position, open disclosures and unsent question answers; the host never reads them. Every view is derived, so there is nothing to roll back.

| Situation | What happens | Undone, retried, uncertain |
|---|---|---|
| A node definition or card fails or panics, or the plan returns no row for a node | That node is drawn as "could not render this item" with the event type and `seq`; the failure is logged and counted per kind. `Renderer.Events` returns no error, so the stream is not closed | Nothing else is redrawn. A fixed binary draws it correctly on the next page load |
| The store cannot be read while rendering | `Events` returns the error; the hub closes the connection and the tab resumes from its cursor ([api.md §6.2](api.md#62-contracts)). A render failure never does this: the hub draws a placeholder for the `seq` ([api.md](api.md), `DH-ADR-API-10`). If streams keep ending with no event, area C1's script stops retrying after A19's `ReconnectGiveUp` and shows "not live, reload" ([ui-core.md §6.3](ui-core.md#63-ownership-failure-retry-rollback-and-cleanup)) | Nothing was written for that batch |
| A Markdown, code, math or terminal renderer panics or exceeds a bound | The block is shown as escaped plain text; a formula is shown as its source; a code block is uncoloured | Always safe; nothing to retry |
| The `transcript` projection is `pending` or `behind` (a rebuild, another process) | The read uses `Exact` freshness within area A06's catch-up bound; beyond it a node whose start is unknown is drawn as "earlier item" until the page is reloaded | Self-heals when the fold is current |
| A dock's projection is `pending` | The dock shows "not available yet"; it never shows an empty goal or an empty task list as fact | Replaced when the value is `current` |
| A `live` frame is dropped or a tab is slow | The next frame is larger; the settled fragment corrects the page | Nothing is queued |
| The host dies during an attempt | On connect the live element is sent empty; the closers appended at resume arrive as events: an interrupted message and cards marked "outcome unknown" | **Uncertain:** whether a recorded tool call took effect; the card says so |
| A component that registered a node kind or a card unloads | Its events draw nothing, or its calls draw the generic card, from the next render | Nodes already on a page stay until that page is reloaded |
| An image fetch is refused, fails, is too large or is not a raster image | The slot shows the reason and "try again"; nothing is cached | **Uncertain:** the address may have received the request |
| An answer arrives for a settled or unknown interaction | The region is re-drawn with the settlement and who gave it | Nothing changes |
| A goal form carries a stale revision; plan mode is set while another process holds the lease | 409 with the current region and a message | The person repeats the change |
| A lazy body cannot be fetched | The disclosure shows an error with a retry; the page is unchanged | Nothing |
| `reset` | The transcript region requests itself at the head; docks and the interaction region get new baselines | Scroll position returns to the tail |

## 7. Decisions

<a id="dh-adr-uiconv-1"></a>
### DH-ADR-UICONV-1 The transcript is folded on the host by a node registry, and a node is found through an index committed with its events
- **Decision.** Keep upstream's model: a node has a key `(kind, id)` taken from event data; a definition matches one event at a time, folds state purely and may claim an event (two claims on one event fail and name both kinds). Change where it runs and how a node is found: the host renders, and the `transcript` row projection records each multi-event node's start and evidence in the transaction of the event. A fragment is a pure function of the log up to its own event, and a page is rendered as of the tab's cursor.
- **Why.** [api.md](api.md) renders each connection's journal at that connection's pace, from the store, in any process, also after a restart. A fold held in the memory of the process that runs the agent cannot serve that; an index stored with the events can, and area A06 already commits derived rows with their events ([DH-ADR-VIEW-2](session-views.md#dh-adr-view-2)).
- **Tradeoff.** One small row per tool call, question and similar node, for the life of the session. A definition must be pure and cannot read another node's state except through evidence. Rendering cost per event is one indexed read plus a few event reads; this is unmeasured.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-5](../design.md#dh-o-5). **Verified by.** DH-T-UICONV-1 to -5, [DH-E-UICONV-1](#dh-e-uiconv-1).

<a id="dh-adr-uiconv-2"></a>
### DH-ADR-UICONV-2 Placement and folding are area C3's plan; this area draws into it, sends closed work as a summary with a lazy body, and virtualises nothing
- **Decision.** One owner decides which rows fold, when, and what stays visible: `chat.Planner`, with C3's turn and process-group views ([ui-chat.md](ui-chat.md), `DH-ADR-UICHAT-8`); following the tail is state of area C1's `keep` module, and no reading script exists. This area's part is mechanical. The renderer draws each node into the container the plan names. When the plan closes a process group (once, at its turn's end), the group's shell is replaced once by its closed form: C3's summary and an address from which this area's `conversation.body` route returns the rows when the group is opened. A collapsed tool card is sent the same way. Windows are area A19's (a tail page, turn-aligned older pages, a window around a target), and a turn container may let the browser skip layout while far from the viewport ([DH-ADR-REACTVIRTUAL-1](../libs/tanstack-react-virtual.md#dh-adr-reactvirtual-1)).
- **Why.** Upstream keeps grouping apart from the assembler for the same reason: the assembler stays generic. Two areas with fold rules would disagree; C3's specification says one must yield, and the reading rules are its subject. A lazy body keeps a turn of hundreds of tool calls out of the page until someone wants it (`DH-LIB-REACTVIRTUAL-R4`).
- **Tradeoff.** The close is the one place a shell is replaced, so disclosures the reader opened inside that group close (upstream resets them too, `packages/client/ui-tool/README.md:82`). A turn that is still running keeps all its rows in the page. Loaded pages are never released. Find-in-page does not see closed bodies. The renderer depends on the plan for every event it draws.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Verified by.** DH-T-UICONV-6, DH-T-UICONV-23, [DH-E-UICONV-7](#dh-e-uiconv-7).

<a id="dh-adr-uiconv-3"></a>
### DH-ADR-UICONV-3 A streaming message is one live element, updated in block-level frames and replaced by its settled node in the same stream event
- **Decision.** The unit of update is a top-level Markdown block ([DH-ADR-REACT-2](../libs/react.md#dh-adr-react-2)): frozen blocks are appended once, the tail is replaced. While streaming, the streaming grammar is used (no math, an open code fence uncoloured, [DH-ADR-MICROMARK-1](../libs/micromark.md#dh-adr-micromark-1)). Reasoning streams the same way inside a collapsed disclosure. A connection gets at most one frame per `LiveMinInterval`. The settled node and the emptied live element travel in one `session` fragment. The live element is not announced frame by frame; C1's `#announce` region gets one sentence when an answer starts and one when it ends.
- **Why.** Partial Markdown re-rendered whole on every tick flickers and costs bytes in proportion to the square of the length. Frozen blocks never change, so only the last one or two blocks can move. One fragment for "final in, partial out" means no frame in which both or neither is shown.
- **Tradeoff.** A text selection inside the tail is lost on each frame, and any selection in the live element is lost at settlement. Formulas and code colours appear when the block or message completes, so heights can shift slightly at settlement. A reference link defined after its block froze stays literal until settlement. The frame rate and bytes per frame are unmeasured.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICONV-12 to -14, [DH-E-UICONV-1](#dh-e-uiconv-1), [DH-E-UICONV-7](#dh-e-uiconv-7).

<a id="dh-adr-uiconv-4"></a>
### DH-ADR-UICONV-4 Untrusted text becomes HTML only in the `render` package: typed parts, a fixed vocabulary, an allow-list gate, and htmx switched off around the result
- **Decision.** This is a security control and is tested as one. `render` is the only caller of C1's `ui.FromSanitizer`, the one constructor of `ui.SafeHTML` from untrusted text; a source check fails the build on any other call or conversion. Its output is parts, so that every control is drawn by a trusted template outside the content. The gate of [§6.2](#62-contracts) admits no `id`, `style`, `hx-*`, `data-*`, form element or free class name. The constructor adds the `hx-disable` container, so no view can place gated content without it. Math is typeset only when settled, through the adapter of [DH-ADR-KATEX-1](../libs/micromark.md#dh-adr-katex-1); the fallback, per formula and as a setting, is the TeX source in a code style. A KaTeX script island is not adopted: it needs the owner's decision.
- **Why.** Upstream's guarantee was React's structure. A host that writes strings has none, and server-rendered pages add an attack upstream did not have: an injected element with an htmx attribute makes an authenticated request with no script at all, which a script-only Content-Security-Policy does not stop. No `id` means content cannot name a region that a later fragment targets; no free class and no `data-*` means it cannot trigger a script behaviour.
- **Tradeoff.** Footnotes have no jump links, task lists show marks and not checkboxes, table alignment and terminal colours use classes (24-bit colour is mapped to the 256-colour palette), and an image stands after its block, not inside a sentence. The gate parses every fragment again. Math quality is unproven, and the first version may show formulas as source. Scope boundary of this record, code highlighting: a source line longer than the cut-off of `DH-LIB-SHIKI-R3` (upstream's 1,000 UTF-16 units), and a block above its size limit, is shown as one plain run, and a comment or string that opens on such a line can mis-colour the lines after it. This is kept on purpose, not forced by a platform: highlighting runs on the host for every viewer, so its cost must be bounded per render, and the chosen tokeniser ([libs/micromark.md §5.3](../libs/micromark.md#53-code-highlighting), chroma) backtracks and needs its per-rule time limit, while a linear-time engine in C is ruled out by the pure-Go rule ([DH-ADR-15](../design.md#dh-adr-15)). Colour carries no meaning: the text is escaped and copied exactly, so the loss is cosmetic.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICONV-7 to -11, [DH-E-UICONV-2](#dh-e-uiconv-2).

<a id="dh-adr-uiconv-5"></a>
### DH-ADR-UICONV-5 A remote image is fetched by the host after a click, from the address in the log, through the guarded client
- **Decision.** This settles the mechanism [DH-ADR-22](../design.md#dh-adr-22) left to this area. A remote image is a slot that shows its full address and a "Load image" button. The form names the event `seq` and the image's ordinal; the host reads the address from the committed event, so the route cannot be used to fetch an arbitrary address. The fetch uses area A23's `egress` client with area A15's address guard: loopback, private, link-local and metadata destinations are refused at connect time, each redirect is checked again, and no cookie or referrer is sent. Only raster types under `MaxBytes` are accepted; SVG is refused. Bytes are served from the host's origin from a bounded cache, so the page's policy `img-src 'self'` stays as it is.
- **Why.** Nothing is fetched without a user action, a renderer defect cannot make the browser fetch, and the browser never contacts the address, so it learns nothing about the user's browser.
- **Tradeoff.** One click per image and per tab. The click still tells the address's server that this host exists; the slot shows the address so the person can judge. The cache is per host process: after a restart the slots return. `AllowHosts` is a way for an operator to widen the guard; it is empty by default.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7). **Verified by.** DH-T-UICONV-18, [DH-E-UICONV-3](#dh-e-uiconv-3).

<a id="dh-adr-uiconv-6"></a>
### DH-ADR-UICONV-6 A tool card is a frame owned here plus a closed kind or a body registered by the tool's owner, derived from logged facts at render time
- **Decision.** This area owns the frame and the templates of the six closed kinds of [DH-ADR-TOOLS-1](tools.md#dh-adr-tools-1) (generic, terminal, diff, search, read, web). The component that owns a tool registers its card by tool name, globally. A card function is pure: logged arguments, result content, error, `meta` and the session's path facts in; no I/O, no clock, no service. Reading the log runs no presenter; the UI layer derives the card when it draws. Anything missing or malformed is the generic card.
- **Why.** Upstream decided that history reads stay free of the tool registry and that display must survive replay; both hold. Keying cards by name and not by an agent's step view is what lets a cold session, or a session whose agent used a scoped tool, be drawn without waking it.
- **Tradeoff.** A tool offered by an MCP server or a plugin process gets the generic card: the host renders only what it knows how to render ([libs/go-ui-stack.md](../libs/go-ui-stack.md) §7.2). Two tools with one name share a card. A card shows what was logged, so it cannot show facts the tool did not put in `meta`.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-6](../design.md#dh-o-6). **Verified by.** DH-T-UICONV-15 to -17, [DH-E-UICONV-5](#dh-e-uiconv-5).

<a id="dh-adr-uiconv-7"></a>
### DH-ADR-UICONV-7 Pending approvals and questions are one state region above the composer, listed in full; a session grant is an explicit choice and never the default
- **Decision.** The region lists every pending item of the shown session, oldest first, and names other sessions with something waiting. The composer stays in the page beneath it. An approval card shows what [interaction-credentials.md §6.2](interaction-credentials.md#62-contracts) lists: agent and lineage, the call drawn by its card, origin, reason, the sandbox mode asked for, the subject. Its default action (Enter) is "Allow once"; reject is a command in C1's shortcut registry (Escape by default) whose target is the reject button of the card that holds focus. "Allow for this session" appears only for an owner and a grantable subject, states the exact scope and the one session it covers, and is a separate, non-primary button. Grantable subjects are the ones area A17 names: one directory, one application, and, in browser use only, one website, shown as its origin ([DH-ADR-33](../design.md#dh-adr-33)). A web fetch to a private address is never grantable: its card has "Allow once" only and asks every time. A session grant is never copied ([DH-ADR-16](../design.md#dh-adr-16), owner decision of 2026-11-19; [DH-ADR-HUMAN-5](interaction-credentials.md#dh-adr-human-5)): it ends when its session's agent is disposed or its host stops, a resumed session and a fork start with none, and a subagent has none of its parent's. The UI says so where a person could expect otherwise. The button's help line reads "until this session ends; not kept after a resume, not shared with forks or subagents". A subagent's request is listed in the root session's region with its lineage, because whoever answers for the root answers for the tree; a grant given on that card belongs to the child's session, the label names the child, and it ends with the child. With nobody bound to answer for the root, the request is denied and the card is drawn settled as `unavailable`. After a resume the strip is empty and the transcript shows the `grant/ended` row with its reason, so the next prompt is explained. Every tab shows the same item; the first valid answer wins and the others are re-drawn with the settlement and who gave it. Grants in force are a strip with a "Revoke" button each, and `grant/created` and `grant/ended` are rows in the transcript.
- **Why.** Upstream shows one request at a time in place of the composer and makes "Always allow" primary. With session grants ([DH-ADR-16](../design.md#dh-adr-16)) a standing yes must be deliberate, visible and revocable in one click.
- **Tradeoff.** The region can grow tall when many items wait; items after the first are collapsed. Unsent question answers are fields marked `data-draft`, kept by C1's per-tab store, so another browser sees the question and not the draft. A timed question shows its deadline as a clock time: C1's script has no countdown, and none is added.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICONV-19 to -21, [DH-E-UICONV-4](#dh-e-uiconv-4), [DH-E-UICONV-6](#dh-e-uiconv-6).

<a id="dh-adr-uiconv-8"></a>
### DH-ADR-UICONV-8 The composer, sent user text and queued input are area C3's design; this area owns the session page, the composer's seat and the regions around it
- **Decision.** Upstream's composer, its drafts and its recognition of references in sent text live in `ui-conversation`, a unit of this area. In the Go design they are specified once, in [ui-chat.md](ui-chat.md): the rich-text island ([DH-ADR-21](../design.md#dh-adr-21)), the host-stored draft with its staged uploads, the command chip, quotes, prefill, the `usertext` view and the `#pending` region. The six composer decisions and the user-text decision of [§4](#4-upstream-decisions-dissected) get their verdict here and their design there. This area fixes what its page guarantees around the composer: one `conversation.composer` place per shown session; no fragment of this area ever targets the composer's region; the interaction region and the docks (tasks, goal, plan control, "Continue") are separate regions, so a swap of one cannot disturb another or the draft. The plan control is shown whenever the plan component is loaded: "off" enters plan mode and "on" leaves it, and it states whether the mode is enforced.
- **Why.** Two specifications of one editor, or of one way to draw a user message, would diverge. The page rules are what the transcript, the stream and the interaction region need from the composer, and together with C3's host-stored draft they remove three upstream limits ([§5](#5-tech-debt-and-its-resolution), group C).
- **Tradeoff.** The composer's and the sent text's behaviour is proven by C3's tests and scenarios, not here. Ledger rows of this area's composer units link to this record and name C3's deliverables in their reasons.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-9](../design.md#dh-o-9). **Verified by.** DH-T-UICONV-22, [DH-E-UICONV-6](#dh-e-uiconv-6).

## 8. Deliverables

<a id="dh-d-uiconv-1"></a>
### DH-D-UICONV-1 A session's log is shown as a transcript of nodes, the same from a page load and from the stream
- **Result and why it matters.** Opening, reloading, reconnecting and restarting all give the same transcript: every event once, in order. This is the rendering half of [DH-O-12](../design.md#dh-o-12).
- **How it is implemented.** The node registry with claims; the `transcript` projection registered through area A06's `projection.Register`; `Page` and `Renderer.Events` over the same `Fold` and `View`; the `at` rule; the built-in kinds of [§6.1](#61-shape); placement by C3's plan; the session page view with its declared places; per-node failure containment.
- **Components and files.** `internal/ui/transcript` (registry, index fold, page, events, built-in definitions, session page view). **Depends on.** [DH-D-UICONV-2](#dh-d-uiconv-2). [DH-D-API-4](api.md#dh-d-api-4) (`Hub.SetRenderer`), [DH-D-API-5](api.md#dh-d-api-5) (the history window with `at`), [DH-D-VIEW-1](session-views.md#dh-d-view-1) (a row projection with a derived table, `Exact` reads), [DH-D-SESSION-1](core-session.md#dh-d-session-1), [DH-D-AGENT-3](agent-loop.md#dh-d-agent-3), [DH-D-TOOLS-2](tools.md#dh-d-tools-2) and [DH-D-HUMAN-2](interaction-credentials.md#dh-d-human-2) (the event payloads named in [§6.1](#61-shape)), [DH-D-UICORE-1](ui-core.md#dh-d-uicore-1) and [DH-D-UICORE-2](ui-core.md#dh-d-uicore-2) (`ui.View`, `ui.Req`, the helpers, `Shell.Declare`), [DH-D-UICHAT-6](ui-chat.md#dh-d-uichat-6) (`chat.Planner` with `Plan` and `Step`, the turn and process-group views), [DH-D-UICHAT-5](ui-chat.md#dh-d-uichat-5) (the `usertext` view). The footer controls and the gallery partial are C3's contributions and are not needed to draw a transcript.
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-5](../design.md#dh-o-5). **Priority.** P0. **Done when.** DH-T-UICONV-1, DH-T-UICONV-2, DH-T-UICONV-3, DH-T-UICONV-4, DH-T-UICONV-5 and DH-T-UICONV-6 pass and [DH-E-UICONV-1](#dh-e-uiconv-1) has passed.

<a id="dh-d-uiconv-2"></a>
### DH-D-UICONV-2 Model and tool text is rendered as safe HTML, and injected markup, script and htmx attributes are inert
- **Result and why it matters.** Markdown, code, terminal colour and math look right and cannot act. It is the shared piece every UI area calls, and a security control for [DH-O-7](../design.md#dh-o-7).
- **How it is implemented.** The pipeline of [§6.1](#61-shape) on the modules of [libs/micromark.md](../libs/micromark.md) (`DH-LIB-MICROMARK`, `-MICROMARKMATH`, `-SHIKI`, `-KATEX`, `-DOMPURIFY`, and own code for `-ANSER`), built once at component start; the parts model; the gate policies as Go values with the table of [§6.2](#62-contracts) as their golden file; the static check on `ui.SafeHTML`; the math adapter with its switch.
- **Components and files.** `internal/ui/render`. **Depends on.** `DH-LIB-MICROMARK`, `DH-LIB-MICROMARKMATH`, `DH-LIB-SHIKI`, `DH-LIB-ANSER`, `DH-LIB-KATEX`, `DH-LIB-DOMPURIFY` (the renderer's libraries and their gates), [DH-D-UICORE-1](ui-core.md#dh-d-uicore-1) (`ui.FromSanitizer`, and the base policy `ui.BasePolicy`), [DH-D-API-2](api.md#dh-d-api-2) (A19's `web` writes the `Content-Security-Policy` header on every HTML answer). Runtime prerequisite: none.
- **Outcome.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Priority.** P0, math included (until its library gates pass, `Math: source` shows a formula as its source, which DH-T-UICONV-10 covers). **Done when.** DH-T-UICONV-7, DH-T-UICONV-8, DH-T-UICONV-9, DH-T-UICONV-10 and DH-T-UICONV-11 pass and [DH-E-UICONV-2](#dh-e-uiconv-2) has passed. The libraries' own tests in [libs/micromark.md](../libs/micromark.md) prove the library choices this deliverable depends on.

<a id="dh-d-uiconv-3"></a>
### DH-D-UICONV-3 A reply is visible while it is written and settles without a flicker, a duplicate or a stale partial
- **Result and why it matters.** The reader sees text grow, in every tab, and the finished message replaces the growing one exactly once, also for a tab that joined late or dropped frames.
- **How it is implemented.** `transcript/live` as in [§6.1](#61-shape): one state per attempt, `render.Stream` per block, a per-connection cursor answered by `Renderer.Live`, the minimum interval, and the combined settlement fragment.
- **Components and files.** `internal/ui/transcript/live`. **Depends on.** [DH-D-UICONV-1](#dh-d-uiconv-1), [DH-D-UICONV-2](#dh-d-uiconv-2). [DH-D-AGENT-4](agent-loop.md#dh-d-agent-4) (`agent.StreamFrame` with a dense chunk index and one `End`), [DH-D-API-4](api.md#dh-d-api-4) (the `live` state slot rendered when written, and the discard of a pending frame before its settlement).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P0. **Done when.** DH-T-UICONV-12, DH-T-UICONV-13 and DH-T-UICONV-14 pass and [DH-E-UICONV-1](#dh-e-uiconv-1) and [DH-E-UICONV-7](#dh-e-uiconv-7) have passed.

<a id="dh-d-uiconv-4"></a>
### DH-D-UICONV-4 Every tool call is a card with an honest state, bounded output and a readable diff
- **Result and why it matters.** A person sees what the agent did, whether it worked, and when the harness does not know. "Outcome unknown" is never drawn as success or as failure.
- **How it is implemented.** The frame and state table of [§6.2](#62-contracts); the registry and `FromSpec`; the six kind templates on C2's primitives; lazy and paged bodies; diffs through `textdiff`; children by `parent`.
- **Components and files.** `internal/ui/toolcard`, the `tool` definition in `internal/ui/transcript`. **Depends on.** [DH-D-UICONV-1](#dh-d-uiconv-1), [DH-D-UICONV-2](#dh-d-uiconv-2). [DH-D-TOOLS-1](tools.md#dh-d-tools-1) and [DH-D-TOOLS-2](tools.md#dh-d-tools-2) (`tools.View`, `PresentCall` and `PresentResult` callable without a step view, `meta` on `tool/result`), [DH-D-FS-2](filesystem.md#dh-d-fs-2) (`textdiff`), [DH-D-STORAGE-5](storage.md#dh-d-storage-5) (the spill locator), [DH-D-UIKIT-1](ui-kit.md#dh-d-uikit-1) (disclosure, code, diff and terminal partials; copying is the `k-copy` island of [DH-D-UIKIT-4](ui-kit.md#dh-d-uikit-4)).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-6](../design.md#dh-o-6). **Priority.** P0, the frame and all six built-in kinds together. **Done when.** DH-T-UICONV-15, DH-T-UICONV-16 and DH-T-UICONV-17 pass and [DH-E-UICONV-5](#dh-e-uiconv-5) has passed.

<a id="dh-d-uiconv-5"></a>
### DH-D-UICONV-5 A remote image in model output loads only after a click
- **Result and why it matters.** A conversation cannot leak data through an image address without the person acting ([DH-ADR-22](../design.md#dh-adr-22)).
- **How it is implemented.** [DH-ADR-UICONV-5](#dh-adr-uiconv-5): the slot part, the two routes, the guarded fetch with a content sniff limited to the listed types, the bounded cache keyed by session and address.
- **Components and files.** `internal/ui/remoteimage`. **Depends on.** [DH-D-UICONV-2](#dh-d-uiconv-2). [DH-D-CONNECT-7](mcp-lsp-web-connector.md#dh-d-connect-7) (the address guard), [DH-D-UTIL-2](util-test-support.md#dh-d-util-2) (`egress`), [DH-D-API-1](api.md#dh-d-api-1) (route registration).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7). **Priority.** P1. **Done when.** DH-T-UICONV-18 passes and [DH-E-UICONV-3](#dh-e-uiconv-3) has passed.

<a id="dh-d-uiconv-6"></a>
### DH-D-UICONV-6 A person answers approvals and questions inline from any tab, and grants or revokes a session grant
- **Result and why it matters.** A waiting agent is never invisible, an answer is given once, and standing consent is offered narrowly, listed and revocable.
- **How it is implemented.** The views of [DH-ADR-UICONV-7](#dh-adr-uiconv-7) per interaction kind (approval, consent, question, question with the `plan-review` intent, authorization prompts as plain forms); forms that post to area A17's route with a request id; the timed question showing the host's deadline, with a hold on first edit or "Take time"; answer fields marked `data-draft`; the question row's late-answer and review routes; the grants strip; two `keys` commands for allow and reject.
- **Components and files.** `internal/ui/interaction`. **Depends on.** [DH-D-UICONV-4](#dh-d-uiconv-4). [DH-D-HUMAN-1](interaction-credentials.md#dh-d-human-1) (`Registry.Pending`, `interact.Changed`, `POST /interactions/{id}` and the hold route), [DH-D-HUMAN-2](interaction-credentials.md#dh-d-human-2) (outcomes and actors), [DH-D-HUMAN-3](interaction-credentials.md#dh-d-human-3) (`Grants.List` and `Revoke`, `grant.Changed`, the never-copied rule), [DH-D-HUMAN-5](interaction-credentials.md#dh-d-human-5) (`question.Service`), [DH-D-API-4](api.md#dh-d-api-4) (the `interaction` and `view` stream events), [DH-D-UICORE-6](ui-core.md#dh-d-uicore-6) (`keys.Registry` with a command scoped to the focused card, the `focus` module), [DH-D-UICORE-5](ui-core.md#dh-d-uicore-5) (the `keep` module), [DH-D-UICORE-1](ui-core.md#dh-d-uicore-1) (the `live` and `reqid` helpers). This area registers `grant.list` and `grant.revoke` itself through `web.Routes`.
- **Outcome.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Priority.** P0, approvals, questions and grants together. **Done when.** DH-T-UICONV-19, DH-T-UICONV-20 and DH-T-UICONV-21 pass and [DH-E-UICONV-4](#dh-e-uiconv-4) and [DH-E-UICONV-6](#dh-e-uiconv-6) have passed.

<a id="dh-d-uiconv-7"></a>
### DH-D-UICONV-7 Plan mode, plan review, the goal and the task list are visible and controllable beside the composer
- **Result and why it matters.** A person can see and change what steers the agent besides their messages: enter and leave plan mode, read and judge a plan, edit, pause, resume or clear a goal and its round limit.
- **How it is implemented.** The docks as state regions; the plan control and document route; plan cards from the logged call; the review card with "Approve", "Request changes" (with feedback) and "Dismiss", matching area A13's three settlements; the goal strip with phase, blocker, rounds and budgets, whose forms carry `{id, revision}`; values drawn by their state (`current`, `behind`, `pending`).
- **Components and files.** `internal/ui/docks`, the `plan` definition. **Depends on.** [DH-D-UICONV-1](#dh-d-uiconv-1), [DH-D-UICONV-6](#dh-d-uiconv-6). [DH-D-JOBS-9](jobs-schedule-goal-plan.md#dh-d-jobs-9) (`plan.Controller`), [DH-D-JOBS-7](jobs-schedule-goal-plan.md#dh-d-jobs-7) and [DH-D-JOBS-8](jobs-schedule-goal-plan.md#dh-d-jobs-8) (`goal.Service` with an `edit` that takes the round limit, `goal.ActivationChanged`), [DH-D-TOOLS-8](tools.md#dh-d-tools-8) (the `todos` projection), [DH-D-API-5](api.md#dh-d-api-5) (the `continuation` fold and `session.prompt` with `continue=1`), [DH-D-UICORE-2](ui-core.md#dh-d-uicore-2) (`Shell.Contribute`, the `live` helper).
- **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UICONV-22 passes and [DH-E-UICONV-6](#dh-e-uiconv-6) has passed.

<a id="dh-d-uiconv-8"></a>
### DH-D-UICONV-8 Long sessions stay usable: closed work is fetched on demand, any window is drawn directly, and budgets are measured
- **Result and why it matters.** Opening a session costs a page, not the session; a jump to an old turn is one request; and the numbers are measured, not assumed.
- **How it is implemented.** [DH-ADR-UICONV-2](#dh-adr-uiconv-2): closed groups and cards with lazy bodies through `conversation.body`, any window rendered as of `at`, and the budget scenario that sets the limits from first measurements ([libs/tanstack-react-virtual.md](../libs/tanstack-react-virtual.md), `DH-T-REACTVIRTUAL-5`). Following the tail and inserting older pages are C3's island.
- **Components and files.** `internal/ui/transcript`; `test/bench`. **Depends on.** [DH-D-UICONV-1](#dh-d-uiconv-1), [DH-D-UICONV-3](#dh-d-uiconv-3). [DH-D-API-5](api.md#dh-d-api-5) (the windows), [DH-D-UICHAT-6](ui-chat.md#dh-d-uichat-6) (the closed-group summary view), [DH-D-UICORE-5](ui-core.md#dh-d-uicore-5) (following the tail, in the `keep` module; there is no transcript island).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Priority.** P1. **Done when.** DH-T-UICONV-6 and DH-T-UICONV-23 pass and [DH-E-UICONV-7](#dh-e-uiconv-7) has passed within its budgets.

## 9. Acceptance

### 9.1 Local tests

Go tests with golden files, the Go fuzzer and an HTML parser; fakes for the services of other areas; no browser. `D-n` is short for `DH-D-UICONV-n`. Nothing has been run.

| Test ID | Proves | What is asserted |
|---|---|---|
| DH-T-UICONV-1 | D-1 | For each fixture session and every cut: the page at the cut plus each later event fragment applied in order equals the page at the head, node for node |
| DH-T-UICONV-2 | D-1 | A duplicate kind fails; a claim withholds the event from other definitions in either registration order; two claims fail and name both; unloading an owner removes its kind and nothing else |
| DH-T-UICONV-3 | D-1 | An update whose start is outside the window finds it through the index; a fragment for `seq` *n* reads no later event; a rebuilt index equals the incremental one; a `pending` index gives the "earlier item" form |
| DH-T-UICONV-4 | D-1 | A definition, card or rule that fails or panics yields one fallback fragment, no error to the hub, unchanged siblings and one count |
| DH-T-UICONV-5 | D-1 | With a fake plan: every node is drawn into the container its row names and a hidden row draws nothing; a per-event call reads no event before the turn's start; user text appears only through the `usertext` view (a spy), never raw; the session page declares its three places |
| DH-T-UICONV-6 | D-1, D-8 | When the plan closes a group its shell is replaced once; the closed form holds the summary and an address and no rows; the body route returns the rows; no other update fragment contains a shell |
| DH-T-UICONV-7 | D-2 | Over the corpora of [libs/micromark.md](../libs/micromark.md) and by fuzzing Markdown, code, terminal bytes and TeX: the parsed output holds only the elements, attributes, classes and address forms of [§6.2](#62-contracts), and the gate changes nothing |
| DH-T-UICONV-8 | D-2 | Hostile inputs (script, `on*`, mixed-case `javascript:`, `data:`, `hx-get`, `hx-trigger="load"`, `hx-swap-oob`, an `id` equal to a real region's, in raw HTML, link titles, fence info strings, image text, coloured tool output and TeX `\class` and `\href`; lines that look like stream framing) come out as text or are dropped; a source check finds no call of `ui.FromSanitizer` outside `render`; in every registered view of this area each gated value sits inside an `hx-disable` container and no control sits inside one |
| DH-T-UICONV-9 | D-2 | A remote image becomes a numbered slot and no `img`; a code fence becomes a code part; a local path is a link only in a settled render and only under the session's own prefix; relative links and other schemes are text |
| DH-T-UICONV-10 | D-2 | Math is typeset only when settled; an adapter error, timeout, oversize formula or forced panic gives the source form; with `Math: source` the module is never called |
| DH-T-UICONV-11 | D-2 | Deep nesting, a very long line, a huge table and oversize input finish within a bound proportional to size or are cut with a marker; a forced renderer panic gives escaped text |
| DH-T-UICONV-12 | D-3 | For replies fed in chunks of 1, 3, 7 and 16 bytes, frames applied in order equal a fresh streaming render of each prefix; a connection that skips most frames and one that joins late end identical; a tool call appears only once named |
| DH-T-UICONV-13 | D-3 | The fragment for a settling `assistant/message` holds the node and the emptied live element; `abandoned`, a turn end and an unload release the state; an interrupted prefix is drawn as interrupted. Run under the race detector |
| DH-T-UICONV-14 | D-3 | With virtual time and 100,000 reasoning chunks: at most one frame per interval per connection, and total bytes within a constant factor of the final HTML |
| DH-T-UICONV-15 | D-4 | Each row of the state table from its event sequence, including a late return and host-death closers; a nested call is a child of its parent |
| DH-T-UICONV-16 | D-4 | Unknown tool, malformed arguments or `meta`, and a panicking card give the generic card; a card function gives the same bytes twice; drawing a cold session resolves no step view and creates no agent; golden files for the six kinds |
| DH-T-UICONV-17 | D-4 | A collapsed card has no body; long output pages by `from`; a spilled result links its locator; a truncated result says so; a diff folds at nine rows and marks a coarse result |
| DH-T-UICONV-18 | D-5 | The form has no address field; an unknown `seq` or ordinal is refused; loopback, private, link-local and metadata addresses and a redirect to one are refused; SVG, a wrong type and an oversize body are refused; a second load makes no second request; the outbound request has no cookie or referrer |
| DH-T-UICONV-19 | D-6 | The approval card shows lineage, origin, reason, mode, subject and the exact scope label; the grant button is absent without a subject and for a non-owner, present with the origin as label for a browser-use call on a website, and absent for a web fetch to a private address; the default button is "Allow once"; an answer for a settled item draws the settlement and its actor; all pending items are listed |
| DH-T-UICONV-20 | D-6 | Answer encoding for single, multiple, custom and skipped questions; the hold; a late answer and a second one (`question/reply-queued`); the read-only review; answer fields carry `data-draft` and a stable `id`; a timed question renders its deadline with the `time` helper |
| DH-T-UICONV-21 | D-6 | The strip lists exactly `Grants.List`; revoke passes the admitted actor; `grant.Changed` replaces the strip; `grant/created` and `grant/ended` are transcript rows; the page of a resumed session and of a fork draws an empty strip although the log holds `grant/created`; a subagent's card is listed in the root's region, its grant label names the child, and the root's strip does not list that grant; the grant button carries the "not kept after a resume" help line |
| DH-T-UICONV-22 | D-7 | A `pending` value draws the placeholder and never an empty goal or task list; goal forms carry id and revision and a stale one is 409; the plan control in both states and its enforcement label; the plan document from the logged call; the three review settlements; each dock fetches itself after `reset`; English and Chinese have every key of this area |
| DH-T-UICONV-23 | D-8 | A page rendered with `at` ignores later evidence; a jump window has controls at both ends and no follow marker; the tail window has the live element |

### 9.2 Real-environment scenarios

Each needs a real browser driving the built binary. The replay model provider stands in for a model API unless stated. They prove the stated contracts for the tested build, browser and operating system. A scenario that is skipped, blocked or failed stays open.

<a id="dh-e-uiconv-1"></a>
#### DH-E-UICONV-1 A streamed answer reads the same in two tabs across a network cut and a host restart
- **Proves.** [DH-D-UICONV-1](#dh-d-uiconv-1), [DH-D-UICONV-3](#dh-d-uiconv-3).
- **Given.** The built `dsh` in its web profile on loopback with a temporary harness home; Chromium, Firefox and Safari in turn; a replayed turn that streams a long answer with a table, a code fence, a formula and two tool calls.
- **When.** (1) Open the session in two tabs and prompt. (2) In tab one, open a tool card and select text in a finished paragraph while the answer streams. (3) Cut and restore tab two's connection. (4) Kill the host during the second tool call and start it again, reloading nothing.
- **Then.** Both tabs end with identical transcript text equal to the session export, every `seq` once and in order. The opened card stays open and the selection in the finished paragraph survives until settlement. After (4) no partial text remains, the turn is shown as interrupted and the tool card says "outcome unknown". The formula is source text while streaming and typeset, or source by setting, once settled.
- **Cleanup.** Close the session, stop the host, delete the home and the browser profiles. On failure keep the export, the host log and both tabs' HTML.

<a id="dh-e-uiconv-2"></a>
#### DH-E-UICONV-2 Hostile model and tool output is inert in a real browser
- **Proves.** [DH-D-UICONV-2](#dh-d-uiconv-2).
- **Given.** The built binary; each supported browser recording every `securitypolicyviolation` event and every network request; a local recording server; a replayed session whose assistant text, reasoning, tool output, tool arguments, question text and plan contain script elements, handler attributes, `hx-get` and `hx-post` to host routes with `hx-trigger="load"`, `hx-swap-oob` naming the interaction region, an element whose `id` equals the stream cursor's, a style element, a remote image, `javascript:` links, TeX using `\class` and `\href`, and lines shaped like stream events.
- **When.** Open the session while it streams and after it settled; open every card, the plan document and the question review; load an older page.
- **Then.** The hostile text is visible as text. The recording server and the host's request log show no request the content asked for. No region the content named changed, the stream cursor is intact, and no script ran. This is the browser-side proof that controls 1 to 4 of [DH-ADR-DOMPURIFY-1](../libs/micromark.md#dh-adr-dompurify-1) hold together; it does not prove the absence of every bypass.
- **Cleanup.** Stop the host and the recording server, delete the home and the browser profiles; keep the request logs and violation records on failure.

<a id="dh-e-uiconv-3"></a>
#### DH-E-UICONV-3 A remote image is fetched only after the click, and never from a private address by default
- **Proves.** [DH-D-UICONV-5](#dh-d-uiconv-5).
- **Given.** The built binary; a real browser; a recording image server on a second address of the machine, listed in `AllowHosts`; a replayed answer with one image on that server, one on a loopback address and one whose server redirects to a metadata address.
- **When.** (1) Open the session and wait. (2) Click "Load image" on the first slot, twice, then in a second tab. (3) Click the other two.
- **Then.** After (1) the server has seen no request and the page shows three slots with their addresses. After (2) the server has seen exactly one request, without cookie or referrer, the image is served from the host's origin, and the browser itself contacted only the host. After (3) both slots show a refusal and neither address was contacted.
- **Cleanup.** Stop the servers and the host, delete the home.

<a id="dh-e-uiconv-4"></a>
#### DH-E-UICONV-4 Two browsers see one approval; the first answer wins; a session grant is offered, listed and revoked
- **Proves.** [DH-D-UICONV-6](#dh-d-uiconv-6).
- **Given.** The built binary on Linux, macOS and Windows with a real sandbox backend; two browser profiles admitted as the owner; a replayed turn that writes three files outside the write roots in one directory.
- **When.** (1) Both browsers open the session; the first approval appears. (2) Browser A chooses "Allow once"; browser B answers the same card a moment later. (3) On the second approval A chooses "Allow for this session". (4) The third write runs. (5) B revokes the grant; a fourth write is prompted. (6) Restart the host and prompt a fifth write. (7) Grant the directory again, fork the session and prompt a write in the fork. (8) In the original, have the agent delegate a write in that directory to a subagent.
- **Then.** (2) one write happens; B's card shows it was answered by the owner from another tab. (3) The label names the exact directory; the strip lists the grant in both browsers. (4) No prompt; the card says "allowed by session grant". (5) The strip is empty in both; a prompt appears. (6) No grant exists; a prompt appears, and the transcript shows the grant ended with the host. (7) The fork's strip is empty and a prompt appears. (8) The subagent's request appears in the original session's region with its lineage and is not covered by the original's grant. The log holds one asked and decided pair per call and `grant/created` and `grant/ended`.
- **Cleanup.** Delete the written files, stop the host, delete the home and profiles; confirm no sandbox access entries remain (area A10's cleanup).

<a id="dh-e-uiconv-5"></a>
#### DH-E-UICONV-5 Tool cards tell the truth for a real command, a real edit and a hung tool
- **Proves.** [DH-D-UICONV-4](#dh-d-uiconv-4).
- **Given.** The built binary with the real shell and file tools under the operating system's sandbox; a real browser; a replayed turn that runs a command printing colour and a progress bar and exiting non-zero, edits a file, runs a script that calls a tool through the tool command line, and calls a test tool that ignores cancellation.
- **When.** Watch the turn; open each card; cancel during the hung tool; wait for the grace period; let the test tool return.
- **Then.** The command card shows the final progress line, colours and the exit code. The edit card shows the applied diff with counts. The script's nested call is a child row. The hung tool's card says "outcome unknown" and later "returned later". Reloading the page draws the same cards.
- **Cleanup.** Restore the file, stop the host, confirm no child process remains, delete the home.

<a id="dh-e-uiconv-6"></a>
#### DH-E-UICONV-6 A timed question, a plan review and a goal are handled from the page
- **Proves.** [DH-D-UICONV-6](#dh-d-uiconv-6), [DH-D-UICONV-7](#dh-d-uiconv-7).
- **Given.** The built binary; a real browser; a real model API (a replayed run is recorded separately and does not count as this scenario); a workspace with one file.
- **When.** (1) Turn plan mode on from the control and ask for a change. (2) When the review appears, open the full plan, choose "Request changes" with feedback, then approve the next plan. (3) Ask a question with a ten-second timeout; type half an answer, reload, finish after the timeout. (4) Create a goal, pause it, raise its round limit, resume, clear.
- **Then.** (1) No file changes while the control says plan mode is on and enforced. (2) The feedback reaches the model; after approval the control is off and the plan card remains. (3) The draft survives the reload; the late answer appears once as a user message and a second answer is refused. (4) Each change is in the strip at once in a second tab, and a stale form is refused with the current state.
- **Cleanup.** Clear the goal, restore the file, stop the host, delete the home.

<a id="dh-e-uiconv-7"></a>
#### DH-E-UICONV-7 Long sessions and long streams stay within measured budgets
- **Proves.** [DH-D-UICONV-8](#dh-d-uiconv-8), [DH-D-UICONV-3](#dh-d-uiconv-3).
- **Given.** The built binary; a real Chromium on a recorded machine; synthetic sessions of 240 and 5,000 turns with code blocks and tool results (the shape of upstream's `benchmarks/long-session-browser`); a test provider that emits 100,000 reasoning chunks.
- **When.** Open each session; load every older page; jump to a turn in the middle; type in the composer while a reply streams; run the reasoning stream.
- **Then.** Timings, element counts, bytes per frame and host CPU are recorded. The first recorded run sets the budgets; later runs fail when a budget is exceeded. A keystroke lands while the reply is unfinished. The open response does not grow with session length.
- **Cleanup.** Delete the homes. Keep the measurements with the build identity.

## 10. Open questions and limitations

1. **Unmeasured.** Rendering cost per event and per page, `LiveMinInterval`, bytes per live frame, the size of the `transcript_node` table, a running turn with hundreds of cards in the page, `content-visibility` in each browser, the height shift at settlement, binary size from the highlighter. If a running turn proves too heavy, C3's plan must close finished groups while the turn runs. A rendered-fragment cache is not designed until [DH-E-UICONV-7](#dh-e-uiconv-7) shows a need.
2. **Math is provisional.** The safety and coverage gates of [libs/micromark.md](../libs/micromark.md) have not run; the first version may ship with `Math: source`. A KaTeX island is not designed; it would be a further island and needs its own decision.
3. **Settled with their owners.** A plan review is an interaction of kind `question` with the intent `plan-review` ([jobs-schedule-goal-plan.md](jobs-schedule-goal-plan.md), [interaction-credentials.md](interaction-credentials.md)), settled as approve, keep planning with feedback, or dismiss. `PresentCall` and `PresentResult` are pure and need no step view, and a `tools.View` holds data, never markup ([tools.md](tools.md)). A render failure never ends a stream ([api.md](api.md), `DH-ADR-API-10`): this area's renderer draws its own fallback row for a node that fails, and if the renderer itself fails the hub draws a placeholder for that `seq`. The renderer is a pure function of events at or before its own.
4. **Settled with [api.md](api.md).** `stream.Renderer` is `Events(env, []Event) SafeHTML`, which draws its own fallback row for an item it cannot draw and returns no error, and `Live(env, frame) SafeHTML`; if the renderer does fail or panic the hub writes a constant placeholder and moves the cursor, and a stream is closed for a failure only when the store cannot be read. `live` frames of one tab are at least `LiveMinInterval` (50 ms) apart, and the settled message and the clearing of the tail are one `session` event. Still assumed: `at` and `around` on the history route.
5. **Open, owner [tools.md](tools.md) (not answered in this pass).** What `meta` on `tool/result` holds for each built-in kind. Designed against what it defines today: a card reads `meta` leniently and falls back to the generic card when a field is missing. Answered: a nested call's record does not carry a working directory, so a nested card shows none.
6. **Settled with [interaction-credentials.md](interaction-credentials.md), and one point open.** A child agent's prompt reaches the root's answerer and the card shows the child's path; a website origin is grantable in browser use only ([DH-ADR-33](../design.md#dh-adr-33)). This area registers the two grant routes through `web.Routes`. **Open, owner that document:** the payload of each interaction kind as a Go type and the actor of a settlement for "answered by" (until then a card reads the fields its §6.2 lists and shows "answered" without a name when no actor is given); a non-owner with the answer role sees the pending items and not the grants strip. **Why two kinds of unsent text are stored differently (register section 5.3).** A half-typed message is a host record ([DH-ADR-UICHAT-4](ui-chat.md#dh-adr-uichat-4)) because its attachments are host objects and it is the main thing a person writes. A half-typed answer to a question is kept by C1's per-tab store: it is short-lived, the question may be answered from another tab or expire, and a host record per pending interaction and principal would need cleanup on every settlement. Recommended default, not confirmed by the owner; the cost is that an answer in progress does not follow the person to another browser.
7. **Open, owner [jobs-schedule-goal-plan.md](jobs-schedule-goal-plan.md) (not answered in this pass; until it is, the goal strip omits the round-limit field and draws only the fields that document's goal view lists, and the plan control states the enforcement mode only where the plan state carries it).** That `goal.Service.Update` with `edit` accepts the round limit; that the goal view carries rounds used, budgets and the armed flag; the enforcement mode on the plan state.
8. **Open, owner [session-views.md](session-views.md) (not answered in this pass; until it is, this area keeps its own `transcript_node` index and does not rely on `event_link`).** A row projection whose derived table can be read by key at `Exact` freshness; whether `event_link` already answers "which events belong to this call", which would shrink this area's index.
9. **Limits kept.** A timed question shows its deadline as a clock time; there is no countdown. Plugin and MCP tools get the generic card. Closed bodies are invisible to find-in-page. The turn rail and transcript search are C3's and area A06's, not designed here. Loaded pages are never released. Unsent question answers are per tab. The fixed "Continue" list and the goal that outlives its agent preset are carried ([§5](#5-tech-debt-and-its-resolution)). The renderer has had no security review.

## 11. Units dispositioned by this spec

Ledger fragment: `trace/fragments/C4.jsonl`.

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 6 | 6 replaced |
| `UP-DEBT` | 21 | 15 resolved, 3 not applicable, 3 carried |
| `UP-DEC` (handed over) | 11 | 1 ported, 10 replaced |

Go items declared: `DH-ADR-UICONV-1` to `-8`, `DH-D-UICONV-1` to `-8`, `DH-T-UICONV-1` to `-23`, `DH-E-UICONV-1` to `-7`.
