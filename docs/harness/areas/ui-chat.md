# Writing to the agent and reading the chat (composer, completion, attachments, references, commands, model choice, feedback, secret masking, chat reading rules): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) (`2e39e70`) and are cited as `path:line` relative to the upstream root. Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `UICHAT`. Nothing here has been built, run or tested, and no browser file was fetched.

| | |
|---|---|
| **Area key** | `UICHAT` (agent C3) |
| **Upstream packages** | 10, all under `packages/client`: `ui-chat`, `ui-input-trigger`, `ui-attachment`, `ui-file-drag`, `ui-file-actions`, `ui-reference`, `ui-commands`, `ui-message-feedback`, `ui-secret-mask`, `ui-model-selection` |
| **Serves outcomes** | [DH-O-12](../design.md#dh-o-12) first; also [DH-O-3](../design.md#dh-o-3) (one accepted message is at most one turn), [DH-O-7](../design.md#dh-o-7) (secrets are not printed), [DH-O-8](../design.md#dh-o-8) (the chosen model is the one used), [DH-O-14](../design.md#dh-o-14) (feedback sends nothing by default) |
| **Depends on areas** | A19 ([api.md](api.md): routes, event stream, the prompt, upload and completion routes), C1 ([ui-core.md](ui-core.md): page shell, island loading, regions, upload control, catalogue, preferences), C4 ([ui-conversation.md](ui-conversation.md): the composer's seat, the docks region, the transcript renderer), A02 (inbox, receipts, cancel), A07 (attachments, the `draft` records), A17 (commands, feedback, the secret store's mask), A04 (model catalogue), A14 (mention grammar, file and session references, skills), A06 (turn outline, statistics), C2 (menu, dialog and chip partials) |
| **Priority** | P0: a plain form that sends, the Send or Stop seat and the reading plan with the turn rail (without them there is no usable conversation). P1: the rich-text island, completion, attachment chips, masking, model choice, feedback. P2: file actions and drag. Each deliverable states its one priority in [§8](#8-deliverables) |

## 1. Purpose

This area is the place where a person talks to the agent. It covers the box the message is written in (the **composer**), what happens between a key press and the message appearing in the conversation, the menus that complete `/` commands and `@` references, attaching files, choosing a model, rating an answer, and hiding credentials that were typed into a message. It also owns the reading rules of the chat: which part of a finished turn folds away, and the strip of marks for jumping between turns.

Upstream builds all of it as React packages that keep their own copy of state in the browser. The Go host renders HTML ([DH-ADR-3](../design.md#dh-adr-3)), so almost everything here becomes a server-rendered fragment. The one exception was chosen by the owner: the composer is a rich-text JavaScript island, so that a reference appears as a chip inside the text ([DH-ADR-21](../design.md#dh-adr-21)). This document specifies that island tightly: what it owns, what stays on the host, and how the page still sends a message when the island does not load. Without this area a user could read a session but not write to it.

## 2. Vocabulary

Terms defined elsewhere are used as written: **island**, **fragment**, **swap**, **Content-Security-Policy (CSP)** ([libs/go-ui-stack.md](../libs/go-ui-stack.md) §1); **route**, **region**, **request id**, **`reset`** ([api.md](api.md) §2); **inbox**, **follow-up**, **steer** ([agent-loop.md](agent-loop.md) §2); **seat**, **dock**, **turn container** ([ui-conversation.md](ui-conversation.md) §2); **staged upload**, **message catalogue** ([ui-core.md](ui-core.md) §2).

| Term | What it is | Why it exists |
|---|---|---|
| **Composer, draft** | The composer is the message box with its controls. The draft is its unsent content: text plus staged uploads. | The draft must survive a reload, so the host holds it. |
| **Mention, canonical token** | A mention is text that points at something: `@path` for a file or folder, a Markdown-style link with a `dsh-session:` address (the "session link") for a session ([context-compaction-skill.md](context-compaction-skill.md) §2). The canonical token is that exact text. | The token is what is stored, copied and sent; it is the reference's identity. |
| **Chip** | A mention, a claimed command or a quote drawn as one unit inside the text: inserted, selected and deleted whole. | A reference cannot be broken by deleting one character ([DH-ADR-21](../design.md#dh-adr-21)). |
| **Trigger, completion source, menu** | A trigger is `/` or `@` typed at the start of a word. A completion source is host code that lists candidates for a trigger. The menu is the list the host renders. | The host knows commands, files and sessions; the browser does not. |
| **Command palette** | The same command menu opened from the composer's `+` button instead of by typing `/`. | Commands are discoverable without knowing their names. |
| **Attachment chip** | A staged upload drawn as one atomic chip inside the text, like a mention: a thumbnail or file icon, the name, and while it uploads its progress, a failure with retry, and a remove control. (The **turn rail** is something else: a strip of marks, one per turn, for jumping.) | The owner decided that mentions and attachments appear as chips inside the text ([DH-ADR-21](../design.md#dh-adr-21)); the user sees what will be sent, where, and can remove it. |
| **Delivery mode** | How a message sent during a running turn is delivered: **queue** (a follow-up) or **steer**. | The user chooses whether to interrupt. |
| **Import map** | A small JSON block in the page head that tells the browser which file a module name such as `lexical` means. | The editor library's files import one another by name; a browser needs the map to load them without a bundler. |
| **IME, composition** | An input method editor lets a user type Chinese, Japanese or Korean by composing candidates; composition is the state before a candidate is confirmed. | Enter confirms a candidate; it must not send the message. |
| **Process, reply, process group** | Within a turn, replies are the assistant's text messages and the process is everything else it did (reasoning, tool calls). A process group is a run of consecutive process rows. | Readers want answers first and work on demand. |
| **Masking** | Drawing a recognised credential with most characters replaced by `*`. | A key pasted into a message should not be readable on a shared screen. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.client.ui-chat` | `dsh-client-ui-chat` | consumer (view) | Renders the chat from recorded session nodes: folding, grouping, scroll state, turn rail, footer actions, usage rows (`packages/client/ui-chat/README.md:13`). |
| `UP-PKG-packages.client.ui-input-trigger` | `dsh-client-ui-input-trigger` | definition | Detects `/` and `@` under the caret, shows the grouped menu and routes a pick to the source that owns it (`packages/client/ui-input-trigger/README.md:12`). |
| `UP-PKG-packages.client.ui-commands` | `dsh-client-ui-commands` | provider | The `/` source, the per-session command directory and three command UI kinds: popup, action, input or direct execution (`packages/client/ui-commands/README.md:12`). |
| `UP-PKG-packages.client.ui-reference` | `dsh-client-ui-reference` | provider | The `@` source for files, folders and sessions, and the rendering of mentions in sent text (`packages/client/ui-reference/README.md:12`, `:40`). |
| `UP-PKG-packages.client.ui-attachment` | `dsh-client-ui-attachment` | consumer (view) | The draft rail, the drop overlay, message image galleries and the lightbox (`packages/client/ui-attachment/README.md:12`). |
| `UP-PKG-packages.client.ui-file-drag` | `dsh-client-ui-file-drag` | provider | Keeps an original file alive during a drag and shows drag failures (`packages/client/ui-file-drag/README.md:12`, `:27`). |
| `UP-PKG-packages.client.ui-file-actions` | `dsh-client-ui-file-actions` | provider | Download, save-as (desktop only) and copy-image-as-PNG (`packages/client/ui-file-actions/README.md:12`, `:34`). |
| `UP-PKG-packages.client.ui-model-selection` | `dsh-client-ui-model-selection` | consumer (view) | The composer's model control and the `/model` popup over one per-session directory (`packages/client/ui-model-selection/README.md:14`, `:78`). |
| `UP-PKG-packages.client.ui-message-feedback` | `dsh-client-ui-message-feedback` | consumer (view) | Like and Dislike on a finished assistant message and the feedback dialog (`packages/client/ui-message-feedback/README.md:12`). |
| `UP-PKG-packages.client.ui-secret-mask` | `dsh-client-ui-secret-mask` | provider | Masks recognised credentials in sent user text; display only (`packages/client/ui-secret-mask/README.md:12`). |

The composer's own code is in `packages/client/ui-conversation`, a unit of area C4. Area C4 hands its design to this area ([ui-conversation.md](ui-conversation.md), `DH-ADR-UICONV-8`), so its behaviour is described here and cited from that package's README.

### 3.2 Behaviour

**Writing and sending.** The composer is a Lexical editor registered as plain text; a reference is an atomic node and a claimed command is one chip at the start of the draft (`packages/client/ui-conversation/README.md:107`, `:127`). Enter sends with the configured delivery mode, Ctrl or Cmd+Enter with the other one, Shift+Enter breaks the line (`:131`). Enter never sends during composition, including Safari's late closing key and engines that only signal key code 229 (`packages/client/ui-conversation/src/client/input/editor/keymap.ts:52-55`). A send waits for the host: the editor and rail go read-only until the host accepts the prompt, and only then do draft, attachments and undo history clear; a refusal leaves everything in place (`README.md:137`). While a turn runs, the same seat shows Stop when the draft is empty and Send when it is not; the "busy Enter" setting chooses queue or steer and defaults to queue (`README.md:143`; `packages/client/ui-conversation/src/submission-settings.ts:18`). Queued messages sit in a dock with Edit, Remove and Steer (`README.md:127`, `:143`). Stopping keeps queued input (`packages/api/session-controller/src/commands.ts:540`). Drafts keep text and chips across session switches and reloads, in browser storage; draft attachments live in browser memory and a reload discards them (`README.md:105`, `:236`).

**Completion.** Trigger detection, menu reduction and exact matching are pure code; each hit fetches candidates from every registered source, a newer keystroke supersedes an older fetch, and a failed source is dropped with a console record (`packages/client/ui-input-trigger/README.md:46`). Focus stays in the composer: arrows move the highlight, Enter picks, Tab picks or drills into a folder, Escape and Shift+Tab leave without changing the draft, and a dismissed menu stays closed for the same token (`:32`). The `@` source lists files first, then sessions; a folder row can be picked or drilled into; a file pick serialises as `@path`, a session pick as the canonical session link returned by the host (an `@` followed by a bracketed label and a `dsh-session:` address), never rebuilt from the visible title (`packages/client/ui-reference/README.md:28-34`, `:68`). A file's context menu offers "Add to chat" (`:36`). In sent messages an `@path` token is drawn as a reference only after the session's filesystem confirms the path, an answer that lasts for the page load (`:40`, `:106`).

**Commands.** A typed `/` and the composer's `+` button open the same menu: an Add section (File, Goal, Plan, Feedback) and a Commands section (Compact, Permission, Model, Export) (`packages/client/ui-commands/README.md:28`). Space and Enter resolve the line against the session's directory: a host command with input becomes a chip followed by arguments, a registered popup (`/model`, `/permission`) opens, an action runs, anything else executes; a command line is never sent as a plain prompt (`:12`). English and Chinese spellings resolve to the same host name (`:42`). A command that does not declare attachments refuses a submission that carries them and the draft stays (`:46`).

**Attachments, drag and file actions.** Images and files keep pick order in one rail of 64 px items; an upload shows byte progress, failure shows retry, an image the host refused explains why (`packages/client/ui-attachment/README.md:34`). Selected files enter a background upload queue as soon as they are added and Send waits for every receipt (`packages/client/ui-conversation/README.md:137`). A paste that carries files while focus is outside any editor goes to the composer that last held focus (`:139`); a file drag over the page shows a full-viewport overlay with a limits line (`packages/client/ui-attachment/README.md:48`). In the browser, dropped folders are refused; on the desktop they become `@path` chips (`packages/client/ui-conversation/README.md:137`). A loaded conversation image opens a lightbox and can be dragged into the composer or, on the desktop, into another application; the browser path keeps one original `File`, addressed by an application-local token and a Blob `DownloadURL` (`packages/client/ui-attachment/README.md:38-42`; `packages/client/ui-file-drag/README.md:27-31`). Files and images offer Download; Save as exists only on the desktop; copying an image decodes it to PNG for the clipboard (`packages/client/ui-file-actions/README.md:34`, `:40`).

**Model choice, feedback, masking.** The composer control and `/model` show the same provider-grouped list with search; a selection is provider, model and effort ids, applies to the next request, and a running step keeps what it started with (`packages/client/ui-model-selection/README.md:14`, `:40`). A saved selection missing from the catalogue shows "Select model" and does not block sending (`:32`, `:58`). Like and Dislike both open a dialog with seven category chips and an optional note; clicking a recorded rating retracts it; "the conversation log travels with every feedback event" (`packages/client/ui-message-feedback/README.md:28`). Sent user text draws each recognised credential masked, keeping the first eight characters, with an eye control to reveal one value; the message text, the copy action and the model input keep the original (`packages/client/ui-secret-mask/README.md:12`, `:30`, `:35-45`).

**Reading the chat.** Four work-details modes decide how much of a finished turn's process shows; by default every assistant reply stays visible and only the process folds, with a setting to show the final answer only (`packages/client/ui-chat/README.md:119-121`; `.agents/notes/implemented/feature/2026-10-15-chat-keep-assistant-replies.md:22`). Folding happens at turn end or, by preference, when the next message arrives; in the second case a turn started by a notice (a finished subagent, a schedule) does not fold the previous turn, a rule expressed as "exchanges" (`README.md:132`, `:171-173`). System-prompt rows, `permission` command rows and `/team` receipts are hidden (`:62`). A tool call is one row through three stages, shown as "preparing" as soon as the model streams its name (`:130`). A finished turn has a footer of actions and, when accounting is complete, a usage row (`:89`, `:106`). The page follows the tail only while the reader is at the bottom, restores the reading position when older history is prepended, and prefetches a page of history within two viewport heights of the top (`:161`, `:201`). A turn rail marks every started turn and hides when the transcript is at most 900 px wide (`:163`, `:236`). Five preferences are stored on the host: transcript view, collapse timing, folded replies, performance and usage, link opening (`packages/client/ui-chat/src/index.ts:18-29`).

### 3.3 Failure, retry, rollback and cleanup upstream

- **A refused or failed send** publishes no echo and leaves the draft and attachments in the composer with a notice (`packages/client/ui-conversation/README.md:141`). An accepted message is shown from client memory, so a reload between acceptance and the durable event loses the bubble ([libs/react.md](../libs/react.md) §3).
- **An upload fails:** the rail item offers retry; removing a draft item skips its queued transfer or aborts the active one (`packages/client/ui-conversation/README.md:137`).
- **A completion source fails:** its rows are missing with no message (`packages/client/ui-reference/README.md:44`).
- **A session mention cannot be prepared:** the failure comes after the prompt was accepted and ends that turn (`packages/client/ui-reference/README.md:104`).
- **A feedback mutation conflicts:** the reply carries the authoritative item and the view reconciles; a failed submit keeps the dialog open (`packages/client/ui-message-feedback/README.md:48`).
- **A drag fails:** the image stays visible and a localized retry message survives the closing of its panel; drag end, drop or plugin disposal clears the retained file (`packages/client/ui-file-drag/README.md:27`, `:31`).
- **Cleanup.** Disposing a session scope releases the attachments of a message the host has not accepted (`packages/client/ui-conversation/README.md:137`).

## 4. Upstream decisions dissected

The first 11 rows are the decision notes handed to this area, named by file under `.agents/notes/` (`impl/` is `implemented/`, `prop/` is `proposed/`). The rest are visible only in code and READMEs; their paths omit `packages/client/`. `UICHAT-n` is short for `DH-ADR-UICHAT-n` in [§7](#7-decisions). The ledger rows hold the full reasoning.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `impl/architecture/2026-09-22-tool-call-three-phases` | One tool row owns preparing, start and result by call id | Keep | Without it the page shows nothing while a long argument streams; the plan gives the three stages one row, and area C4 draws it | [UICHAT-8](#dh-adr-uichat-8) |
| `impl/architecture/2026-10-09-message-node-factories-and-delegates` | Four image slots filled by one gallery | Replace | One gallery partial called from four views; slots are React's extension mechanism | [DH-D-UICHAT-9](#dh-d-uichat-9) |
| `impl/architecture/2026-10-09-reasoning-content-slot-factory` | Reasoning body is a replaceable slot plus a reusable renderer | Replace | A region plus a shared partial gives the same split on the host; the reasoning node is area C4's | [DH-ADR-3](../design.md#dh-adr-3) |
| `impl/architecture/2026-10-11-chat-group-tree-exchanges` | Folding follows exchanges, typed by positions in a group tree | Replace | The rule protects a reader when folding waits for the next message; here a group folds once, at its own turn's end, so no later turn can fold it | [UICHAT-8](#dh-adr-uichat-8) |
| `impl/architecture/2026-10-13-chat-single-fold-motion` | One frame clock moves closing rows and scroll together | Replace | A fold is one replacement at a durable event; no frame-by-frame script exists | [UICHAT-8](#dh-adr-uichat-8) |
| `impl/architecture/2026-10-16-steering-batch-start-in-definition-state` | Precompute where a steering batch starts | Replace | It keeps a reserved slot stable in React; the host knows the inbox order when it draws the queue dock | [UICHAT-5](#dh-adr-uichat-5) |
| `impl/architecture/2026-10-19-file-drag-plugin-ownership` | One plugin retains original files during a drag | Replace | A drag carries a reference to a host object, so nothing must be kept alive in the browser | [UICHAT-9](#dh-adr-uichat-9) |
| `impl/architecture/2026-11-05-markdown-fence-hoc-slot` | Fences are dispatched by language through a keyed slot | Replace | The Markdown renderer runs on the host; its handling of fences by language is area C4's | [DH-ADR-3](../design.md#dh-adr-3) |
| `impl/feature/2026-10-15-chat-keep-assistant-replies` | Every reply stays visible when a turn folds; a setting restores "final only" | Keep | Position is not content: the last message is often an addendum | [UICHAT-8](#dh-adr-uichat-8) |
| `impl/testing/2026-10-11-user-text-credential-matching` | A hand-built linear scanner with performance gates | Replace the scanner, keep the format table and the gate | Go's `regexp` is linear by construction | [UICHAT-7](#dh-adr-uichat-7) |
| `prop/feature/2026-11-06-chat-history-consumption` | Fetch history ahead; hold one page until it is safe to insert | Keep the behaviour, simplify | The "load earlier" control requests itself when it scrolls into view; a fragment inserted above the viewport under scroll anchoring does not move the reader | [UICHAT-8](#dh-adr-uichat-8) |
| The draft clears only after the host accepts | `ui-conversation/README.md:137` | Keep | No message is shown that the host did not accept | [UICHAT-5](#dh-adr-uichat-5) |
| Draft text in browser storage; draft attachments in browser memory | `ui-conversation/README.md:105`, `:236` | Change | Both are host state; a reload loses nothing | [UICHAT-4](#dh-adr-uichat-4) |
| A reference's identity is its canonical text | `ui-reference/README.md:68` | Keep, and make it the only identity | The same text is stored, copied, sent and parsed by the host | [UICHAT-2](#dh-adr-uichat-2) |
| A failed completion source is silent | `ui-input-trigger/README.md:46` | Change | The host knows which group failed and can say so | [UICHAT-6](#dh-adr-uichat-6) |
| Sources register globally in the client | `ui-input-trigger/README.md:80` | Change | A source registered through an agent's scope exists for that session only | [UICHAT-6](#dh-adr-uichat-6) |
| Three client command kinds plus decorations | `ui-commands/README.md:12`, `:32` | Change | One catalogue from area A17; a picker or form is a host fragment | [UICHAT-6](#dh-adr-uichat-6) |
| A sent mention is confirmed by a `stat` per page load | `ui-reference/README.md:40` | Change | A fragment may depend only on the log; the fact is fixed at admission | [UICHAT-2](#dh-adr-uichat-2) |
| Masking changes only what is drawn; the original stays in the page | `ui-secret-mask/README.md:12`, `:57` | Change | The host sends the masked text; the original leaves the host only on reveal or copy | [UICHAT-7](#dh-adr-uichat-7) |
| The session log travels with every feedback event | `ui-message-feedback/README.md:28` | Drop | Nothing leaves the machine by default | [DH-ADR-9](../design.md#dh-adr-9) |
| Model selection is by ids and applies at the next request | `ui-model-selection/README.md:14` | Keep | A running step must keep the model it was built for | [DH-D-UICHAT-7](#dh-d-uichat-7) |
| Folding may wait for the next message, with an animated fold | `ui-chat/README.md:121`, `:171` | Drop the option | One fold time removes the exchange rule and the motion code; see the two notes above | [UICHAT-8](#dh-adr-uichat-8) |
| Save as, native drag-out and the in-app link browser exist on the desktop | `ui-file-actions/README.md:34`; `ui-file-drag/README.md:12`; `ui-chat/README.md:57` | Drop | No desktop application and no embedded browser pane; links open in a browser tab | [DH-ADR-18](../design.md#dh-adr-18) |
| A send reports a product analytics event on the desktop | `ui-conversation/README.md:135` | Drop the default | Any such event is opt-in and is area C1's usage catalogue | [DH-ADR-9](../design.md#dh-adr-9) |
| Client plugins replace labels, docks and rows through slots | `ui-model-selection/README.md:52`; `ui-chat/README.md:93` | Replace | No client plugin runtime; a host component contributes to a declared region of area C1's registry | [DH-ADR-3](../design.md#dh-adr-3) |
| The transcript stays fully mounted and is paged by window | `ui-chat/README.md:186`, `:236` | Keep | Paging by `seq` bounds the page (`DH-ADR-REACTVIRTUAL-1` in [libs/tanstack-react-virtual.md](../libs/tanstack-react-virtual.md)) | [UICHAT-8](#dh-adr-uichat-8) |

## 5. Tech debt and its resolution

All 30 units are bullets from the ten READMEs' "Known Limitations and Deferred Work". They are grouped by cause; each unit's own reason is in its ledger row. `L:` stands for `UP-DEBT-limit.packages.client.`. Carried rows name stage [DH-S-59](../implementation-plan.md#dh-s-59), where the known limits and scope boundaries of this part of the product are stated and checked.

| Group (cause) | Units | How the Go design answers it |
|---|---|---|
| **A. React slots, client composition and the desktop adapter** | `L:ui-input-trigger.2`, `L:ui-reference.2`, `L:ui-file-actions.2` (3) | Not applicable. The slot-merge split exists because of a TypeScript dependency direction; "no browser-side file scan" describes a client that could run without its host provider; the download deadline and credential-free clipboard reader belong to the Electron adapter ([DH-ADR-18](../design.md#dh-adr-18)). |
| **B. State and order guessed in the browser** | `L:ui-chat.4`, `.5`, `.6`, `L:ui-message-feedback.2`, `L:ui-commands.1` (5) | Resolved. Waiting messages are drawn from the host's inbox in queue order; the turn rail and its previews are rendered from the host's turn outline for loaded and unloaded turns alike; a rating is a log event that every tab's stream draws; a command's result is the logged `command/done`, so there is no detached notice to lose ([DH-D-UICHAT-1](#dh-d-uichat-1), [DH-D-UICHAT-6](#dh-d-uichat-6), [DH-D-UICHAT-8](#dh-d-uichat-8)). |
| **C. Scroll and motion driven by script** | `L:ui-chat.1`, `.2` (2) | Resolved. No persistent script anchor exists to fight a touch gesture: an older page is inserted under browser scroll anchoring. The running indicator is vector and animated by CSS, so there is no raster to soften ([DH-D-UICHAT-6](#dh-d-uichat-6)). |
| **D. A surface that leaves something out** | `L:ui-chat.3`, `L:ui-attachment.1`, `.2`, `L:ui-message-feedback.1`, `.3`, `L:ui-model-selection.1` to `.4`, `L:ui-input-trigger.1`, `L:ui-reference.1`, `.4`, `L:ui-secret-mask.2`, `.3` (14) | Resolved. Tool changes are a row kind of their own in the plan; the lightbox is a modal dialog (the page behind is inert) with zoom and download; the feedback form shows its limits and a refusal beside the field, and the rating control is a partial any view can include; one picker fragment serves the control and `/model`, with badges, a retry for a failed provider, effort levels correctable in the route's overlay, and a model field on session creation; a completion source can be scoped to one session; a failed completion group is named and a bad session mention is refused before admission; sessions are found by body text; every host view masks through one function, including a mention's label ([DH-D-UICHAT-3](#dh-d-uichat-3), [-5](#dh-d-uichat-5), [-6](#dh-d-uichat-6), [-7](#dh-d-uichat-7), [-8](#dh-d-uichat-8), [-9](#dh-d-uichat-9)). |
| **E. Limits of the web platform** | `L:ui-file-drag.1`, `.2`, `L:ui-file-actions.1`, `.3` (4) | `.2` of file-drag is resolved: a drag names a durable host URL, so there is no Blob URL to be revoked. The other three are carried, each narrowed: dragging a file out to another application works only where the browser implements `DownloadURL`; the browser owns the save location and the clipboard permission; a page is never told where a download was saved. The Download action is the path that always works ([UICHAT-9](#dh-adr-uichat-9)). |
| **F. Rules kept on purpose** | `L:ui-secret-mask.1`, `L:ui-reference.3` (2) | Carried. Masking by published format only: masking by name would hide ordinary values; values the host itself released are masked whatever their shape. A sent mention shows what was true at admission: rendering must be a function of the log ([UICHAT-2](#dh-adr-uichat-2), [UICHAT-7](#dh-adr-uichat-7)). |

Totals: 22 resolved, 3 not applicable, 5 carried.

## 6. Go design

### 6.1 Shape

Everything is under `src/harness/internal/ui`. Routes are registered through area A19's `web.Routes` and live regions through its `stream.Hub` ([api.md](api.md) §6.1). Views, islands, regions, preferences and UI text follow area C1's contract as written ([ui-core.md](ui-core.md) §6.1): `Shell.Island`, `Shell.Declare` and `Contribute`, `prefs.Declare`, `msg.Catalog.Register`, and the shared template helpers. English and Chinese hold every key of this area's catalogue namespaces; an island gets its labels as attributes.

| Package | Component and service key | Why it exists |
|---|---|---|
| `composer` | `ui.composer`; `composer.Key` (`Drafts`). Declares the region `composer.controls` (List) | The composer fragment, the attachment chips, the Send or Stop seat and the draft records. `composer.controls` is the one seat for every per-session control beside Send; its scope is the session. A contribution draws one button or link and opens its own fragment in a popover or in `#dialogs`; it never draws into the editor. Order: model and effort (this area), agent preset chip ([ui-work.md](ui-work.md)), permission preset control ([ui-settings-plugins.md](ui-settings-plugins.md)), then the optional worktree and voice controls ([x-forge-worktree.md](x-forge-worktree.md), [x-cua-browser-voice.md](x-cua-browser-voice.md)). |
| `complete`, `complete/commands`, `complete/files`, `complete/sessions` | `ui.complete` with `complete.Key` (`Registry`); one source component per kind | Turns a trigger and a query into a menu. A composition without a source has no such group. |
| `usertext`, `mask` | `ui.usertext`; `mask` is a pure package | Draws sent user text: mention chips and masked credentials. The component installs `mask.Text` as area C1's `utext` template helper (`ui.SetUserText`), so every view prints user-written text through it. |
| `chat` | `ui.chat`; `chat.Key` (`Planner`) | The reading plan that area C4's renderer places nodes by; the turn rail, usage row and statistics views; the reading preferences. |
| `modelpicker`, `feedbackui`, `fileactions` | one component each, no service | Thin routes and views over areas A04, A17 and A07. `modelpicker` contributes its control to `composer.controls`. This area is the one owner of the model picker and of the feedback form: A04 and A17 supply data and services and register no view or route for either. |
| `assets/islands` | embedded files | Two first-party ES modules, each registered with `Shell.Island`: `composer` (with vendored Lexical) and `filedrag`. |

```go
// package composer
type Draft struct{ Text string; Attachments []attachments.Ref; RequestID string; Rev uint64 }
type Drafts interface { // one record per session and principal, in A07's kv namespace "draft"
    Get(ctx context.Context, p access.Principal, s session.ID) (Draft, error)
    Save(ctx context.Context, p access.Principal, s session.ID, text string, ifRev uint64) (Draft, error) // 409 on a stale rev
    Prefill(ctx context.Context, p access.Principal, s session.ID, text string, m Merge) (Draft, error)   // keep | replace | append; never sends
    Attach(ctx context.Context, p access.Principal, s session.ID, r attachments.Ref) (Draft, error)
    Detach(ctx context.Context, p access.Principal, s session.ID, r attachments.Ref) (Draft, error)
    Clear(ctx context.Context, p access.Principal, s session.ID, sent string) error // after admission; mints the next request id
}
// package complete
type Query struct{ Principal access.Principal; Session session.ID; Text, Drill string; Limit int }
type Candidate struct{ Kind Kind; Token, Label, Detail, Group, Icon string; Drill bool; Open string } // Open: a fragment URL or a named client action
type Source interface{ Trigger() rune; Candidates(ctx context.Context, q Query) ([]Candidate, error) }
type Registry interface {
    Register(owner *cordis.Context, s Source) error // through an agent's scope: that session only
    Menu(ctx context.Context, trigger rune, q Query) (Menu, error) // groups in order; a failed source is a named empty group
    Spans(ctx context.Context, q Query) ([]Span, error)            // mentions, quotes and a leading command in Text
}
// package mask
func Spans(text string) []Span // recognised credentials: start, end, kept prefix
func Text(text string) string  // the masked form, for titles, previews and excerpts
// package chat
type Row struct{ Seq uint64; Turn int; Pos Position; Kind string; Hidden bool; Group string } // Pos: input | process | reply | turn-level
type Planner interface {
    Plan(events []session.Event, p Prefs) []Row // pure: the same events and preferences give the same rows
    Step(prev Cursor, ev session.Event, p Prefs) (Row, Cursor) // the same rows one event at a time, for the live stream; Cursor is the small fold state of the open turn, kept by area C4 with its index
}
```

**The composer island and the host: who owns what.** This split is [DH-ADR-UICHAT-1](#dh-adr-uichat-1).

| The island owns (browser, between requests) | The host owns |
|---|---|
| The editor's document: text and chip nodes; caret and selection | What a chip means: the mention grammar and every lookup |
| Undo and redo (a pick or a paste is one step; history ends at a send) | The list of candidates and the menu's HTML |
| The composition guard: no send, no menu key and no draft save while an input method composes | Command names, their arguments and their dispatch |
| Paste of text (plain text only; a paste or drop that carries files is handed to area C1's upload control) and the attachment chip it then draws: inserted at the caret when the file is added, with its progress, failure, retry and remove, until the host's answer fixes the chip's token | Upload validation, storage, limits and previews; which staged file each attachment token names |
| Key map: Enter, Shift+Enter, Ctrl or Cmd+Enter, and the menu keys while a menu is open | The send action, the request id, the delivery mode's default |
| Trigger detection, the menu's highlight and position | The stored draft, its revision, and whether it was sent |
| An unsaved buffer of the text | Access: every request is checked whatever the island shows |

**The island's interface to the page.** The view places `<dsh-composer>` with area C1's `island` helper inside area C4's seat, which carries `hx-preserve` so that no region swap replaces a draft. Its child elements are a plain form, which is also the fallback; area C1's script imports the module when the element appears.

| Direction | Item | Content |
|---|---|---|
| In (children) | The form | `<textarea name="text">` holding the draft's canonical text, the hidden request id (`reqid`), one submit button per delivery mode, area C1's `<dsh-upload>` control with its `for` attribute naming the session's main column, a status strip, and the `composer.controls` region. With no island the staged uploads are listed under the textarea, each with a remove button; the textarea itself holds their tokens as text. |
| In (DOM event) | `dsh:upload-queued`, `dsh:upload-progress`, `dsh:upload-done`, `dsh:upload-failed` from `<dsh-upload>` (area C1) | The island inserts a pending attachment chip at the caret when a file is queued, updates its progress, turns it into the canonical attachment token with the stored reference on `done`, and on `failed` shows the host's reason with Retry and Remove inside the chip. Removing a chip whose upload runs cancels it; removing a stored one posts `composer.detach`. |
| In (attributes) | `data-spans` | The mentions, quotes and leading command the host found in the draft text (start, end, kind, label), so that the island draws chips without knowing the grammar. |
| In (attributes) | `data-rev`, `data-busy-mode`, URLs, labels | Draft revision; the default delivery mode; the completion and draft addresses; translated strings. |
| Out (request) | `GET` completion with `q` and `drill` | Answer: the menu fragment. Each option carries `data-kind`, `data-token`, `data-label`, and optionally `data-drill` or `data-open`. The fragment names the query it answers; an answer for a superseded query is discarded. |
| Out (request) | `PUT` draft with `text` and `rev` | After `DraftSaveDelay` without typing and when the page is hidden. Answer: the status strip (saved, a credential notice, the new revision). |
| Out (form submit) | The form itself | The island copies the canonical text into the textarea and calls the form's own submit, so the island and the fallback share one path. |
| In (DOM event) | `dsh:insert-reference`, `dsh:insert-quote` | Inserts a mention or a quote at the caret in one undo step: "Add to chat" from a file menu, a file tree, a selection in the transcript. A quote's canonical text is Markdown quote lines (area A06's `Quote`). |
| In (DOM event) | `dsh:reset` on `<body>` (area C1) | The island saves its buffer and asks for the composer fragment again; it replaces itself only if the host's draft is newer and nothing is unsaved. |

All requests use the fetch helper that area C1's script exports, which adds the tab header. The island declares no extra policy, so it is not framed; what it needs from the page policy is in [DH-ADR-UICHAT-3](#dh-adr-uichat-3). The element accepts one DOM event from other controls, `dsh:insert-text` with plain text in its detail: the island inserts the text at the caret as one undo step, and with the island not loaded the sender appends through `Drafts.Prefill` (a request) instead. This is how voice input proposes text ([x-cua-browser-voice.md](x-cua-browser-voice.md)); nothing else may write into the editor.

**How the draft survives.** The host's record is the draft. The island also keeps the text in `sessionStorage`, tagged with the revision it was based on and the request id (area C1's `keep` module stores plain `data-draft` fields only, so the fallback textarea is marked `data-draft` and the island keeps its own entry). On start the island uses its buffer only if the revision equals the host's (unsaved typing survived a reload); otherwise the host's draft wins. If the fragment says the buffered request id was accepted, the buffer is dropped: the message was sent. While the host is unreachable the strip says "not saved, kept in this tab" and the save is retried with a growing delay. Two tabs editing one draft: the second save answers 409 and that tab offers "use the other tab's draft" or "keep mine".

**How it degrades.** The island hides the textarea only after its editor has started. If the module, the import map or any vendored file fails to load, the form remains, and area C1 retries the import when the element next appears. The textarea and buttons post the same fields. Mentions can be typed as tokens and resolve to the same targets, because the host parses the text either way; there are no chips and no menu at the caret. The command palette's `+` button, the model control and the upload form are plain htmx and HTML and keep working. With no script at all the form posts and the host answers `303` to the session page.

**From key press to message.**

1. Enter, with no composition and no highlighted menu row. Send is unavailable while the upload control reports a transfer in flight. The island fills the textarea and submits the form with the delivery-mode button that Enter maps to.
2. htmx posts area A19's `session.prompt`: text, upload references, mode, request id. The form is busy and the editor read-only until the answer.
3. The host checks access and decodes the form. A line whose first token is a command goes to area A17's dispatch and never to the model. Otherwise `complete.Registry.Spans` parses the mentions. A session mention the principal may not read, or a staged upload that is gone, refuses the submit with 422: the composer form is re-rendered with the posted text, the failing chips marked and the reason beside them. Then `conversation.Commands.Prompt` admits the message with the request id as message id (a logged inbox insertion and a receipt, [agent-loop.md](agent-loop.md) `DH-ADR-AGENT-3`), and `Drafts.Clear` empties the draft.
4. The answer is the composer form: empty, with the next request id. The message appears in the queue dock in every tab: this area's `#pending` region, a contribution to area C4's `conversation.docks`, drawn from the inbox projection and listing queued follow-ups and steering input alike ([DH-ADR-REACT-3](../libs/react.md#dh-adr-react-3)).
5. **Idle session:** the driver claims the message at once; the durable `user/message` arrives on the stream, is drawn in the transcript and the dock row goes. **Running, queue:** the row stays in the dock with Edit, Remove and "Steer now" until its turn. **Running, steer:** the row is marked as steering until the next step claims it.
6. **Stop** posts `session.cancel` for the turn and keeps the queue, as upstream does. **Escape twice** is the same action.

A repeat of step 2 with the same request id (a double click, an htmx retry, a resend after a reconnect) is answered with the first receipt ([api.md](api.md) `DH-ADR-API-3`). If the connection drops before the answer, the island cannot know the outcome: it shows "not confirmed" and asks for the composer fragment until the host answers. An accepted message shows an empty draft and a dock row or a durable message; an unreceived one shows the draft with the same request id, ready to send again.

### 6.2 Contracts

**Routes.** The first block is registered by this area under area A19's rules (`S` is `access.Session("sid")`; activation is `None` unless stated). The second block is area A19's, used as written.

| Method and pattern (name) | Kind and rule | Purpose |
|---|---|---|
| `GET /sessions/{sid}/composer` (`composer.view`) | Fragment, `S` | The composer form; also how the island compares its buffer with the host. |
| `PUT /sessions/{sid}/draft` (`composer.draft`) | Action, `S` | Save text at a revision. 200 with the status strip, or 409. |
| `DELETE /sessions/{sid}/draft/attachments/{ref}` (`composer.detach`) | Action, `S`, `{ref}` opaque | Remove a staged upload and its token from the draft. Answer: the status strip (and, without the island, the list of staged uploads). |
| `POST /sessions/{sid}/draft/attachments` (`composer.attach`) | Action, `S` | Stage an object the session can already read, by reference: a dragged conversation image or workspace file. Answer: the attachment token, which the island inserts as a chip at the drop point. |
| `GET /sessions/{sid}/model` (`model.picker`) | Fragment, `S` | The model and effort picker, with `q` for search. |
| `GET /sessions/{sid}/feedback` (`feedback.form`); `PUT`, `DELETE /sessions/{sid}/messages/{mid}/rating` (`feedback.rate`); `POST /sessions/{sid}/feedback` (`feedback.record`) | Fragment and Actions, `S`, `{mid}` opaque, `MayResume` for writes | The form, a rating, a retraction, a session remark; over area A17's `feedback.Service`. |
| `GET /sessions/{sid}/turns` (`chat.rail`); `GET /sessions/{sid}/turns/{n}/preview` (`chat.preview`) | Fragments, `S` | The turn rail and one turn's preview card, from area A06's outline. |
| `GET /sessions/{sid}/messages/{mid}/secrets/{i}` (`usertext.reveal`) | Fragment, owner | One credential unmasked, `Cache-Control: no-store`. |
| `session.prompt`, `session.cancel`, `session.queue` | A19 | Send, stop, and edit or remove a queued message. |
| `session.upload`, `session.attachment` | A19 | Upload one file (driven by area C1's control); serve a stored upload. |
| `session.complete` (`GET /sessions/{sid}/completions/{kind}?q=`) | A19 | `{kind}` is the trigger name, `command` or `mention`; this area supplies the registry and the menu view. |
| `session.model`, `session.history`, `file.raw` | A19 | Apply a model choice; older pages; file bytes. |

**Live regions.** Each is a state region of the session stream placed with area C1's `live` helper: replaced whole by element id, with a baseline on connect and a refetch on `dsh:reset` ([api.md](api.md) `DH-ADR-API-4`). The composer's editor is not one of them and is never replaced by the stream.

| Region | Redrawn when | Source |
|---|---|---|
| `#run-<sid>`: the Send or Stop seat | `agent.StatusChanged` | A02 |
| `#model-<sid>`: the model control | `model/selection` is appended, or `llm.ModelsChanged` | A02, A04 |
| `#turns-<sid>`, `#stats-<sid>`: the turn rail and the statistics strip | The outline or statistics projection changes | A06 |
| A message's rating control | A feedback event is committed: an out-of-band part of that `session` event's fragment | A17 |

The queue of waiting messages is area C4's dock ([ui-conversation.md](ui-conversation.md) `DH-D-UICONV-7`); the dialogs of this area (picker, feedback form, lightbox) are fragments swapped into area C1's `#dialogs`.

**Placement of each upstream package.**

| Package | What the user sees and does | Placement |
|---|---|---|
| `ui-chat` | Reads turns, sees work fold and replies stay, jumps by the rail | A pure plan that area C4's renderer applies; server-rendered rail, usage and statistics fragments. No script of its own: following the tail is area C1's script |
| `ui-input-trigger` | Types `/` or `@`, drives a menu from the keyboard | Detection, highlight and keys in the composer island; the menu is a fragment |
| `ui-commands` | Picks or types a command; opens the palette with `+` | Fragment (menu and pickers); the leading command chip is part of the island |
| `ui-reference` | Picks a file, folder or session; sees mentions as chips | Fragment (menu, sent chips); draft chips are part of the island |
| `ui-attachment` | Sees attachments, drops files, opens an image | Attachment chips in the composer island (upstream's rail becomes chips, [DH-ADR-21](../design.md#dh-adr-21)); fragments for the gallery and a modal dialog; picking, drop, paste and the transfer are area C1's `<dsh-upload>` |
| `ui-file-drag` | Drags an image into the composer or out of the page | The `filedrag` island: a few lines that set drag data on marked elements |
| `ui-file-actions` | Downloads a file, copies an image | Links to area A19's byte routes; a copy button in the `filedrag` island. Save as is a recorded drop |
| `ui-model-selection` | Opens the picker, searches, chooses model and effort | Fragment |
| `ui-message-feedback` | Rates an answer, fills the form, chooses what happens to it | Fragment |
| `ui-secret-mask` | Sees credentials masked, reveals one | Host rendering through `mask`; reveal is a request |

**Attachments in detail.** Picking, dropping, pasting, the first-in-first-out queue, progress, cancel and retry are area C1's upload control ([ui-core.md](ui-core.md) `DH-ADR-UICORE-8`); this area decides where it sits and how its answer joins the draft. The control sits in the composer form, and its drop target is the session's main column, so a drop anywhere on the conversation goes to that page's one main composer and to no other. Each file is one `session.upload` request. Area A07 validates and stores it and the reference is added to the draft record, which keeps the object alive ([storage.md](storage.md) §6.2). **An attachment is a chip inside the text** ([DH-ADR-21](../design.md#dh-adr-21), confirmed by the owner on 2026-11-19 after the audit): a fourth atomic node kind of the island beside mention, command and quote. When a file is added the island inserts a pending chip at the caret (a thumbnail for an image, an icon and the name for any other file) and the chip shows the transfer's progress. The answer of `session.upload` carries the stored reference, and the chip's canonical text becomes the attachment token: a Markdown-style link whose label is the file name and whose target is `dsh-attachment:` followed by the stored reference, written like the session link so that the draft stays one string and a chip is copied, cut and undone as one unit. A refusal is shown inside the chip with the host's reason (`attachment/too-large`, `attachment/too-many`, `attachment/invalid-image`, `attachment/unsupported-type`) and two actions, Retry (a transfer that failed) and Remove; Send is unavailable while any chip uploads or has failed. Limits come from area A07's `attachments.Config` (20 files per message, 512 MiB per file, the image limits) and reach the control as attributes. A dropped folder is refused with a message that suggests an `@` mention. Removing a chip cancels its transfer or posts `composer.detach`, which deletes the reference from the draft; area A07's retention collects an object nothing cites. **At admission** the host parses attachment tokens with the mention grammar, in text order; a token whose reference is not staged in the caller's draft is refused with 422 `attachment/not-staged`, and a staged reference that no token names is detached and not sent. The message's attachments are the staged references in token order; each token is replaced in the model-visible text by a placeholder `[attachment <n>: <name>]` so that the model knows where the user placed it (Inference: this is new, upstream sent attachments apart from the text), and the token's span is recorded with the message's display facts, so the sent bubble shows the chip where it was typed. Without the island, `session.upload` appends the token to the draft text and the textarea shows it as text, which admits the same way.

**Commands, model choice and feedback.** The command menu lists area A17's catalogue for the session ([interaction-credentials.md](interaction-credentials.md) `DH-ADR-HUMAN-8`) plus area A14's skills, matched by name or by translated title in either language. A pick does one of three things: a command with arguments becomes a leading chip and the user types on; a command with a picker opens that picker's fragment (`/model` opens `model.picker`, the same view as the composer's control); an action opens its fragment or the file input. The model picker groups models by route, marks the current choice, lists a model the build cannot drive as unavailable with area A04's reason, and offers only the effort levels the catalogue advertises ([llm.md](llm.md) `DH-ADR-LLM-6`). Choosing posts `session.model`; the choice applies to the next request. Like and Dislike open `feedback.form`; submitting records the rating locally and the form then offers area A17's three exits (keep local, export, or send after a preview of exactly what would be sent; `DH-ADR-HUMAN-13`). Clicking a recorded rating retracts it.

**Secret masking: where it happens, what is stored, what is logged.**

| Place | What holds |
|---|---|
| The session log | The message as the user sent it. The user may have pasted a key for the model to use; changing the text would break [DH-O-4](../design.md#dh-o-4). |
| The model request | The same original text. |
| Any HTML the host renders (bubbles, dock rows, rail previews, titles, excerpts) | The masked text only. The fragment contains no hidden original. |
| Reveal and copy | An owner-only request returns one value, uncached. |
| The unsent draft | Shown as typed while editing, stored in the draft record as typed. On save the host scans it and the strip warns that the message appears to contain a credential which will be stored and sent to the model. |
| Diagnostic logs and telemetry | Never contain draft or message text: only ids, sizes and codes. |
| Export, upload and feedback payloads | Redacted by area A06's records before they leave ([interaction-credentials.md](interaction-credentials.md) §6.2). |

**Config and preferences.** Config is Go structs with defaults as Go values; unknown keys fail the load. Preferences are declared with area C1's `prefs.Declare`, per principal. No `UP-CFG` unit is assigned to this area.

| Go value | Fields and defaults | Upstream source |
|---|---|---|
| `composer.Config` | `DraftSaveDelay` 1 s; `MaxDraftBytes` 1 MiB | Inference |
| Preference `composer.busy-mode` | `queue` or `steer`, default `queue` | `ui-conversation/src/submission-settings.ts:18` |
| `complete.Config` | `MaxRows` 20 per group; `Timeout` 2 s per source | Inference |
| Preferences `chat.work-details`, `chat.folded-replies`, `chat.usage` | `folded` or `verbose` (never fold); `all` or `final`; `detailed` or `compact` | `ui-chat/src/index.ts:18-29`; `collapseTiming` and `linkOpening` are dropped |
| `chat.Config` | `RailMinWidth` 900 px | `ui-chat/README.md:163` |

**Persisted data.** The `draft` record per session and principal (text, upload references, request id, revision), in area A07's `kv`. Nothing else: waiting messages, ratings and model choices are other areas' log events. **Events.** No new session event type. This area listens to `agent.StatusChanged` (A02), `llm.ModelsChanged` (A04), `command.Changed` (A17) and `projection.Changed` (A06) to redraw regions; no host event is forwarded to the browser. **Shortcuts.** Send, new line, the other delivery mode, the command menu and the reference menu are registered with area C1's `keys.Registry` as fixed commands, so that the shortcut page lists them; the island handles them itself while it has focus. **Tools exposed to the model:** none.

### 6.3 Ownership, failure, retry, rollback and cleanup

| Thing | Owner (who releases it) | Access |
|---|---|---|
| The editor, its listeners and timers | The `<dsh-composer>` element; released in `disconnectedCallback` | The page |
| The `sessionStorage` buffer | The tab; dropped when its request id is accepted | That tab |
| The draft record | The `ui.composer` component's records; removed on an accepted send and when the session is deleted | The session's principal, through the draft routes |
| A staged upload's bytes | Area A07's collector, once no draft and no event cites them | The session, through `session.attachment` |
| A route, a region, an island entry, a completion source, a preference | The fiber that registered it; a session-scoped source leaves with its agent | Declared per route |
| The vendored editor files | The repository; embedded in the binary | Served from the asset route |

| Situation | What happens | Undone, retried, uncertain |
|---|---|---|
| The island or a vendored file fails to load | The plain form stays and sends | The import is retried the next time the element appears |
| A completion request fails, or one source errs or times out | The menu closes, or shows that group as "could not load" with a retry | Typing continues; nothing is inserted |
| A draft save fails | The strip says so; the text stays in the tab's buffer and is retried | Text typed in a tab that is then closed while unsaved is lost |
| An upload fails half-way | Nothing is stored for that file; area C1's control shows retry | A partial staging file is swept by area A07 |
| A send is refused with 422 or 409 (`session/writer-held`) | The composer form is re-rendered with the posted text and the reason; the island restarts on it | Text, chips and uploads are kept; undo history is lost |
| A send fails otherwise (413, `access/denied`, a refusal at admission, 5xx) | The message goes to `#notices`; the composer is untouched | Nothing changes |
| The connection drops during a send | "Not confirmed"; the composer fragment is requested until the host answers | **Uncertain** until then whether the host admitted it; the request id makes a resend safe |
| The host stops between admission and `Drafts.Clear` | On the next render the draft's request id is found in area A19's `submissions` fold and the draft is cleared | Never a second message |
| The host restarts | Drafts and staged uploads are still there; an upload in flight is lost and shows retry | Queued messages are in the log and reappear ([agent-loop.md](agent-loop.md) `DH-D-AGENT-3`) |
| `ui.composer` unloads | Its routes answer 404 and its regions show "unavailable"; draft records stay | A reload registers them again |
| A session is deleted | Its draft records are removed; staged objects become collectable | Nothing else |

## 7. Decisions

<a id="dh-adr-uichat-1"></a>
### DH-ADR-UICHAT-1 The composer island loads Lexical and owns only what happens between two requests; one text field is the whole draft
- **Decision.** Confirm [DH-ADR-LEXICAL-1](../libs/lexical.md#dh-adr-lexical-1): the island is `<dsh-composer>`, it loads vendored, unpatched Lexical ([DH-ADR-LEXICAL-2](../libs/lexical.md#dh-adr-lexical-2)) as a plain-text editor with history, and has four atomic node kinds: mention, claimed command, quote and attachment. The split is the table in [§6.1](#61-shape). The draft is exactly one string, the canonical text, in which an attachment is a token like a mention; the island sends no separate list of parts. The plain form inside the element is kept as the fallback. An upload is a chip inside the text, with its progress, failure, retry and remove inside the chip ([§6.2](#62-contracts), "Attachments in detail"), as the owner decided ([DH-ADR-21](../design.md#dh-adr-21)); upstream instead shows uploads in a rail under the text and only mentions as chips.
- **Why.** [DH-ADR-21](../design.md#dh-adr-21) is the owner's choice of an island with chips. Lexical is what upstream proved for input methods and selection. One string removes a failure class from the library proposal, in which the text and a parts list had to describe the same draft.
- **Tradeoff.** The largest hand-written script in the product, with no type checker and no JavaScript test runner; its pure parts (trigger detection, the canonical-text reading) are exercised by a self-test page in the real-browser scenario. [DH-ADR-21](../design.md#dh-adr-21) lists "no composer with scripts off" as a cost; this decision keeps a fallback anyway, because it costs one form. An attachment chip carries a transfer's state, so it is the most stateful chip; a long file name is cut to fit and shown whole on focus, and a failed chip keeps Send unavailable until it is retried or removed.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICHAT-1, DH-T-UICHAT-2, [DH-E-UICHAT-1](#dh-e-uichat-1).

<a id="dh-adr-uichat-2"></a>
### DH-ADR-UICHAT-2 A chip's identity is its canonical mention text; the host parses and validates at admission; what a sent mention shows is fixed there
- **Decision.** A chip stores a kind, a label and the canonical token the host put in the menu. The stored draft, the clipboard and the submitted form carry the token as text. At admission the host parses the text with area A14's grammar, validates each session mention under the caller's principal and checks each file mention once; the confirmed spans are recorded with the message as display facts, and the `usertext` view draws from them. There is no table of opaque handles.
- **Why.** [libs/lexical.md](../libs/lexical.md) proposed host-issued handles looked up again at submit. A file mention is a pointer the model reads through its own guarded tools, and area A14's resolver already takes the principal, so a handle adds no protection; it adds expiry, re-issue after a host restart and a second code path for the no-script form. Recording the spans makes a sent message's chips a function of the log, which the event stream requires ([api.md](api.md) §6.2).
- **Tradeoff.** A label is display only and may be stale; the host never reads it. A typed or pasted token becomes a chip only when the host next renders the draft. A sent mention does not follow a later rename or deletion.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7). **Verified by.** DH-T-UICHAT-3, DH-T-UICHAT-4.

<a id="dh-adr-uichat-3"></a>
### DH-ADR-UICHAT-3 The editor's import map is allowed by one fixed hash in `script-src`; a framed composer is the fallback
- **Decision.** The editor library's files import one another by bare name, so the browser needs an import map, and a browser treats an import map as an inline script. `ui.composer` generates the map's text from the vendor manifest once at start-up and registers it with area A19's `web.Routes.AllowInlineData(owner, "importmap", text)`, an effect of the component. It passes text, not a hash: `web` computes `'sha256-…'` from the registered text and composes it into `script-src` ([api.md](api.md), `DH-ADR-API-9`), and area C1's layout writes the registered block, unchanged, ahead of the first module script ([ui-core.md §6.1](ui-core.md#61-shape), "Living under a policy with no inline script"). The island's `Policy` stays empty, so it is not framed. A test fails if the map does not name every vendored module once or names a file outside the asset route; A19's and C1's tests cover the hash and the absence of any other inline script. If a supported browser does not honour a hash for an import map, the island declares the policy it needs and is mounted in a frame through the `Frame` route that area C1's shell registers for a framed island, as [DH-ADR-GOUISTACK-3](../libs/go-ui-stack.md#dh-adr-gouistack-3) prescribes.
- **Why.** [libs/go-ui-stack.md](../libs/go-ui-stack.md) forbids every inline script and states its reason: a per-response nonce would not survive a fragment rendered for a stream event. A fixed hash needs no nonce and is in no fragment, so that reason is not touched. An import map is data: it can only map a module name to a same-origin file that `script-src 'self'` would allow anyway, and an injected second map does not match the hash.
- **Tradeoff.** The page policy is "no inline script except hashed import maps registered with `web`". The alternative, rewriting the vendored files to relative import paths, would keep the policy literal but make the files differ from the published release they are verified against ([libs/lexical.md](../libs/lexical.md)). Framing the main text input would cost focus, shortcut, paste and resize handling across a frame, so it is the fallback, not the plan.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7). **Verified by.** DH-T-UICHAT-2, [DH-E-UICHAT-1](#dh-e-uichat-1).

<a id="dh-adr-uichat-4"></a>
### DH-ADR-UICHAT-4 Drafts and staged uploads are host state; the tab keeps only an unsaved buffer
- **Decision.** One draft record per session and principal holds the text and the staged upload references. A file is uploaded when it is added ([ui-core.md](ui-core.md) `DH-ADR-UICORE-8`). The island's `sessionStorage` copy is a buffer for text not yet saved and is used only when its revision matches the host's. A prefill for a new session writes the record and never sends.
- **Why.** Upstream loses draft attachments on reload because their bytes live in browser memory. Area A07 stores the record (`kv` namespace `draft`, [storage.md](storage.md)) and this area is its only writer; area C1's per-tab store keeps unsent text of plain form fields only ([DH-ADR-UICORE-5](ui-core.md#dh-adr-uicore-5)). Because the record is keyed by session and principal, a second browser of the same owner shows the same draft.
- **Tradeoff.** A request per pause in typing. A file is stored even if the message is never sent, until retention collects it. A draft is per user, so a second user of a shared session has their own.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICHAT-5, DH-T-UICHAT-6, [DH-E-UICHAT-3](#dh-e-uichat-3).

<a id="dh-adr-uichat-5"></a>
### DH-ADR-UICHAT-5 One send path: accepted, then shown; the queue is drawn from the inbox; Stop keeps it
- **Decision.** Island and fallback submit the same form to `session.prompt`. Nothing is drawn until the host has admitted the message; then the queue dock (`#pending`, this area's) shows it from the inbox projection, in the host's order, in every tab. While a turn runs, Enter uses the user's `composer.busy-mode` and Ctrl or Cmd+Enter the other mode. A queued row offers Edit, Remove and "Steer now". Stop cancels the turn with the cause `user` and keeps the inbox.
- **Why.** [DH-ADR-REACT-3](../libs/react.md#dh-adr-react-3) and [api.md](api.md) (`DH-ADR-API-3`) fix acceptance-then-echo and idempotent submits. Upstream's local echoes predict order and are lost on reload. Keeping the queue on Stop is upstream's choice; discarding typed messages on a stop would lose the user's words.
- **Tradeoff.** On a slow link the busy state is visible. A kept follow-up may start the next turn right after a stop (open question 5).
- **Serves.** [DH-O-3](../design.md#dh-o-3), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICHAT-7, DH-T-UICHAT-8, [DH-E-UICHAT-2](#dh-e-uichat-2).

<a id="dh-adr-uichat-6"></a>
### DH-ADR-UICHAT-6 Completion sources are host components; the menu and every picker are fragments
- **Decision.** A source registers per trigger with `complete.Registry`, globally or through one agent's scope. The registry asks the sources of a trigger under one deadline and renders one menu: files, then the session's own subagents, then other sessions for `@`; an Add section and a Commands section for `/`. A source that fails becomes a named, empty group with a retry. A command's picker or form is a fragment opened from the menu. Completion never wakes a session.
- **Why.** The browser has no commands, files or sessions of its own. Upstream's three client command kinds and its client-side directory cache exist to give a JavaScript client what the host already has.
- **Tradeoff.** A round trip per keystroke, cancelled by the next one; on a remote link the menu lags the typing. Third parties cannot add a client-side source; a plugin's commands reach the menu through area A17's catalogue.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICHAT-9, DH-T-UICHAT-10.

<a id="dh-adr-uichat-7"></a>
### DH-ADR-UICHAT-7 Masking is done by the host when it renders; the log keeps what the user sent
- **Decision.** As the table in [§6.2](#62-contracts) states. `mask.Spans` recognises upstream's published formats (`packages/client/ui-secret-mask/README.md:35-45`) with Go's `regexp`, and additionally masks every value the host's secret store has released (area A17's `Store.Mask`). Every view that prints user-written text does so through `mask`. A masked fragment holds the kept prefix and marks, never the original.
- **Why.** Upstream masks in one React renderer and leaves the original in the page for copying, so titles, previews and excerpts draw credentials as written. On the host, one function serves every view. Go's `regexp` guarantees time linear in the input, which is what upstream's hand-built scanner was written to obtain.
- **Tradeoff.** Masking is for shoulder-surfing and screenshots, not confidentiality: the log and the model receive the original, and the composer shows what is being typed. Reveal and copy cost a request. A credential in no published format, and not held by the store, is drawn as written.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICHAT-11, DH-T-UICHAT-12, [DH-E-UICHAT-4](#dh-e-uichat-4).

<a id="dh-adr-uichat-8"></a>
### DH-ADR-UICHAT-8 The chat's reading rules are one pure host plan; a process group folds once, at its turn's end; this area adds no reading script
- **Decision.** `chat.Planner` maps a turn's events and the user's preferences to rows: which turn, whether a row is input, process, reply or turn-level, whether it is hidden, which process group it joins. Area C4's renderer places every node by that plan and owns the nodes themselves ([ui-conversation.md](ui-conversation.md) `DH-ADR-UICONV-2`). Kept from upstream: every reply stays visible when a turn folds unless `chat.folded-replies` is `final`; system-prompt, `permission` and `/team` receipt rows are hidden; an empty retry message is a compact receipt; a tool call is one row through preparing, start and result; tool changes are a row kind of their own; inherited history ends at a divider. A process group folds once, when its turn ends; `verbose` never folds. Dropped: folding on the next message, and with it the exchange rule and the fold animation. Following the tail is area C1's script. Older history loads when the "load earlier" control scrolls into view (htmx's `intersect` trigger) and is inserted under the browser's scroll anchoring.
- **Why.** The rules are about what a reader must not lose and do not depend on React, and one owner keeps the page and the stream in agreement. One fold time makes a fold a single replacement at a durable event, which the stream's "page and stream agree" rule needs; upstream's frame clock, persistent anchors and held page exist because its script owns every pixel of scroll.
- **Tradeoff.** No coordinated fold animation, and one preference fewer than upstream. Where a browser lacks scroll anchoring an inserted page may shift the view. Changing a preference re-renders the transcript.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICHAT-13, DH-T-UICHAT-14, [DH-E-UICHAT-5](#dh-e-uichat-5).

<a id="dh-adr-uichat-9"></a>
### DH-ADR-UICHAT-9 File actions and drag use host URLs; save-as and native drag-out are dropped
- **Decision.** Download is a link to area A19's byte routes with a download disposition. Copying an image fetches a PNG the host made and writes it to the clipboard. A draggable image carries a reference to its host object; dropped on a composer it is staged by reference (`composer.attach`) with no bytes re-uploaded, and where the browser supports `DownloadURL` the same URL lets it be dropped into another application. Save as, reveal-in-folder after a download and native drag are not built.
- **Why.** [DH-ADR-18](../design.md#dh-adr-18) removes the desktop application that provided them. A reference to a host object needs no file kept alive in the page, which removes upstream's reason for a transfer service.
- **Tradeoff.** Drag-out works in Chromium-family browsers only and is unverified. The clipboard needs the browser's permission. A recorded exception to [DH-ADR-1](../design.md#dh-adr-1) for the desktop-only actions.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-UICHAT-17, [DH-E-UICHAT-6](#dh-e-uichat-6).

## 8. Deliverables

<a id="dh-d-uichat-1"></a>
### DH-D-UICHAT-1 A person can write a message in a plain form, send it, see it waiting, and stop a turn
- **Result and why it matters.** The first usable conversation: a composer that works with no island, the Send or Stop seat, queue and steer.
- **How it is implemented.** The composer view renders the form of [§6.1](#61-shape) from the draft record; the seat is a `live` region drawn from the agent's status; the buttons map to delivery modes by the user's preference. Step 3's draft clearing and the crash check against the `submissions` fold live in the component.
- **Components and files.** `internal/ui/composer`. **Depends on.** [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1). [DH-D-API-5](api.md#dh-d-api-5) (`session.prompt`, `session.cancel`, `session.queue`, and the prompt route passing a command line to area A17), [DH-D-API-4](api.md#dh-d-api-4) (the stream hub), [DH-D-AGENT-2](agent-loop.md#dh-d-agent-2) (status and the `inbox` fold), [DH-D-UICORE-1](ui-core.md#dh-d-uicore-1) and [DH-D-UICORE-5](ui-core.md#dh-d-uicore-5) (shell, helpers, `prefs`), [DH-D-UIKIT-1](ui-kit.md#dh-d-uikit-1) (form and button partials). Area C4's session page seats the composer in `conversation.composer` and offers `conversation.docks`, into which this deliverable contributes the queue dock; without that page the composer renders on its own route.
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-3](../design.md#dh-o-3). **Priority.** P0. **Done when.** DH-T-UICHAT-1, DH-T-UICHAT-7 and DH-T-UICHAT-8 pass and [DH-E-UICHAT-2](#dh-e-uichat-2) has passed.

<a id="dh-d-uichat-2"></a>
### DH-D-UICHAT-2 The composer is a rich-text island: chips in the text, undo, input methods, paste
- **Result and why it matters.** What the owner chose in [DH-ADR-21](../design.md#dh-adr-21): a reference is one unit in the text, and typing Chinese, Japanese or Korean never sends by accident.
- **How it is implemented.** One ES module defining `<dsh-composer>`: it creates a Lexical plain-text editor with history, the four chip node types (mention, command, quote, attachment with its upload states), the key map with the composition guard, trigger detection, the canonical-text reading and the buffer. The vendor manifest and the import map generator are added to the asset package, and the component registers the map's text with `web.Routes.AllowInlineData` ([DH-ADR-UICHAT-3](#dh-adr-uichat-3)). The module never assigns markup from a string, and its size is capped by a test.
- **Components and files.** `internal/ui/assets/islands/composer`, `internal/ui/composer`. **Depends on.** [DH-D-UICHAT-1](#dh-d-uichat-1). [DH-D-UICORE-4](ui-core.md#dh-d-uicore-4) (`Shell.Island`, the layout that writes registered import maps, the fetch helper), [DH-D-API-2](api.md#dh-d-api-2) (`AllowInlineData` and the composed policy). Runtime prerequisite: a browser that supports import maps and custom elements; otherwise the fallback applies.
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UICHAT-2 passes and [DH-E-UICHAT-1](#dh-e-uichat-1) has passed in every supported browser of [DH-ADR-39](../design.md#dh-adr-39).

<a id="dh-d-uichat-3"></a>
### DH-D-UICHAT-3 Typing `/` or `@` completes commands, files and sessions, and the `+` button opens the same commands
- **Result and why it matters.** A user reaches every command and can point at a file or another session without knowing exact names.
- **How it is implemented.** `complete.Registry` with three source components: commands over area A17's catalogue and area A14's skills; files over `fileref`; sessions over `sessionref` with the caller's principal. Ranking is the source's; the registry orders groups, applies the row limit and the deadline, and renders the menu view. `Spans` uses area A14's grammar for admission and for `data-spans`.
- **Components and files.** `internal/ui/complete` and its three sub-packages. **Depends on.** [DH-D-UICHAT-1](#dh-d-uichat-1). [DH-D-API-5](api.md#dh-d-api-5) (`session.complete` with the trigger as `{kind}`), [DH-D-HUMAN-6](interaction-credentials.md#dh-d-human-6) (the command catalogue with typed arguments and translated titles), [DH-D-CONTEXT-4](context-compaction-skill.md#dh-d-context-4) (`fileref.Key`, `sessionref.Key`, the grammar package), [DH-D-CONTEXT-9](context-compaction-skill.md#dh-d-context-9) (the skill catalogue), [DH-D-UIKIT-4](ui-kit.md#dh-d-uikit-4) (the menu partial).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UICHAT-3, DH-T-UICHAT-4, DH-T-UICHAT-9 and DH-T-UICHAT-10 pass and [DH-E-UICHAT-1](#dh-e-uichat-1) has passed.

<a id="dh-d-uichat-4"></a>
### DH-D-UICHAT-4 A draft and its attachments survive a reload and a host restart, and an attachment can be removed before sending
- **Result and why it matters.** A user never retypes a message or re-attaches a file because a page reloaded or the host restarted.
- **How it is implemented.** `composer.Drafts` over area A07's `kv`; the upload answer that carries the stored reference and the attachment token; the attachment chip node of the island and its four upload states (queued, uploading, stored, failed) driven by `<dsh-upload>`'s events; the admission rule for attachment tokens of [§6.2](#62-contracts); the fallback list of staged uploads for a page without the island; the detach and attach-by-reference routes; the placement of area C1's upload control in the form with the session's main column as its drop target.
- **Components and files.** `internal/ui/composer`. **Depends on.** [DH-D-UICHAT-1](#dh-d-uichat-1). [DH-D-UICORE-8](ui-core.md#dh-d-uicore-8) (the `<dsh-upload>` control, its `busy` attribute and the `dsh:upload-busy` event), [DH-D-STORAGE-3](storage.md#dh-d-storage-3) (`attachments.Store`, the `draft` namespace read by the collector), [DH-D-API-5](api.md#dh-d-api-5) (`session.upload` adds the reference to the draft, and `session.attachment` also serves a reference the caller's draft holds), [DH-D-UICHAT-2](#dh-d-uichat-2) (the island in which the chip is drawn), [DH-D-CONTEXT-4](context-compaction-skill.md#dh-d-context-4) (the grammar package that parses the token).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UICHAT-5 and DH-T-UICHAT-6 pass and [DH-E-UICHAT-3](#dh-e-uichat-3) has passed.

<a id="dh-d-uichat-5"></a>
### DH-D-UICHAT-5 Sent user text shows mentions as chips and credentials masked
- **Result and why it matters.** A pasted key is not readable in the transcript, a title or a preview, and a mention looks the same before and after sending.
- **How it is implemented.** The pure `mask` package (format table, one compiled expression, the store's released values); the `usertext` view, which merges credential spans with the message's recorded mention spans, the earlier span winning and a mention's label passing through `mask.Text`; the reveal route; a budget test on text with and without credentials.
- **Components and files.** `internal/ui/usertext`, `internal/ui/mask`. **Depends on.** [DH-D-UICHAT-3](#dh-d-uichat-3) for spans. [DH-D-HUMAN-7](interaction-credentials.md#dh-d-human-7) (`secret.Store.Mask`), [DH-D-AGENT-2](agent-loop.md#dh-d-agent-2) (a display annotation for mention spans on the user message), [DH-D-UICORE-1](ui-core.md#dh-d-uicore-1) (the `utext` helper). Every view that prints user-written text (bubbles, dock rows, titles, excerpts, notifications) uses the `usertext` view or `utext`.
- **Outcome.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UICHAT-11 and DH-T-UICHAT-12 pass and [DH-E-UICHAT-4](#dh-e-uichat-4) has passed.

<a id="dh-d-uichat-6"></a>
### DH-D-UICHAT-6 The chat reads well: answers stay visible, work folds, and a long session can be navigated by turn
- **Result and why it matters.** A long session stays readable: the plan decides what a finished turn shows, and the rail reaches any turn.
- **How it is implemented.** `chat.Planner` as a pure function with a golden corpus; the reading preferences; the turn rail and preview routes over area A06's outline, with the rail hidden by a container query under `RailMinWidth`; the usage row and the statistics strip as contributions to regions.
- **Components and files.** `internal/ui/chat`. **Depends on.** [DH-D-UICHAT-1](#dh-d-uichat-1). [DH-D-UICORE-3](ui-core.md#dh-d-uicore-3) and [DH-D-UICORE-5](ui-core.md#dh-d-uicore-5) (following the tail, `prefs`), [DH-D-API-5](api.md#dh-d-api-5) (`session.history` and a window around a turn for a jump), [DH-D-VIEW-3](session-views.md#dh-d-view-3) (the turn outline and statistics projections). Area C4's renderer places nodes by the plan, folds a group at turn end, marks its "load earlier" control to request itself on `intersect`, and declares the region `conversation.turn-footer`.
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P0, the plan, the preferences, the turn rail, the usage row and the statistics strip together. **Done when.** DH-T-UICHAT-13 and DH-T-UICHAT-14 pass and [DH-E-UICHAT-5](#dh-e-uichat-5) has passed.

<a id="dh-d-uichat-7"></a>
### DH-D-UICHAT-7 A person chooses the model and reasoning effort from the composer or with `/model`
- **Result and why it matters.** The model shown is the model the next request uses, and an unusable model says why.
- **How it is implemented.** One picker view over `llm.Runtime.Routes` and `Models`, filtered on the host by `q`; the control is a contribution to `composer.controls`, redrawn from the `modelSelection` fold and on `llm.ModelsChanged`; the form posts `session.model`. A stored selection missing from the catalogue shows "Select model" and does not disable Send. On a blank session the same form sets the model before the first message.
- **Components and files.** `internal/ui/modelpicker`. **Depends on.** [DH-D-UICHAT-1](#dh-d-uichat-1). [DH-D-LLM-4](llm.md#dh-d-llm-4) (`Runtime.Routes`, `Models`, badges, the unavailable reason), [DH-D-AGENT-7](agent-loop.md#dh-d-agent-7) (the `modelSelection` fold), [DH-D-API-5](api.md#dh-d-api-5) (`session.model`, and a model field on `session.create`), [DH-D-UIKIT-4](ui-kit.md#dh-d-uikit-4) (the menu partial).
- **Outcome.** [DH-O-8](../design.md#dh-o-8), [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UICHAT-15 passes and [DH-E-UICHAT-2](#dh-e-uichat-2) has passed.

<a id="dh-d-uichat-8"></a>
### DH-D-UICHAT-8 A person rates an answer or leaves a remark, and decides what happens to it
- **Result and why it matters.** Feedback is one click, appears in every tab, and never sends anything the user did not see ([DH-ADR-9](../design.md#dh-adr-9)).
- **How it is implemented.** The rating partial keyed by message id, contributed to the turn footer and drawn only when the principal may rate (`can`); the form view with categories from area A17, length limits as field attributes and a 422 beside the field; routes over `feedback.Service` carrying the item's version, a conflict re-rendering the control; after a record the form shows area A17's three exits.
- **Components and files.** `internal/ui/feedbackui`. **Depends on.** [DH-D-UICHAT-6](#dh-d-uichat-6). [DH-D-HUMAN-10](interaction-credentials.md#dh-d-human-10) (`feedback.Service`, the `feedback` projection, the preview and export), [DH-D-UICONV-1](ui-conversation.md#dh-d-uiconv-1) (the region `conversation.turn-footer`).
- **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-12](../design.md#dh-o-12). **Priority.** P1. **Done when.** DH-T-UICHAT-16 passes and [DH-E-UICHAT-6](#dh-e-uichat-6) has passed.

<a id="dh-d-uichat-9"></a>
### DH-D-UICHAT-9 Images open in a viewer, and files can be downloaded, copied and dragged
- **Result and why it matters.** A user can look closely at an image the model produced and take a file out of the conversation.
- **How it is implemented.** One gallery partial used by user messages, assistant output, tool results and trajectory rows; a modal dialog with fit and actual-size views and a download link; the `filedrag` island, which sets drag data on elements the host marked and implements the copy button.
- **Components and files.** `internal/ui/fileactions`, `internal/ui/assets/islands/filedrag`. **Depends on.** [DH-D-UICHAT-4](#dh-d-uichat-4). [DH-D-API-5](api.md#dh-d-api-5) and [DH-D-API-9](api.md#dh-d-api-9) (a download disposition and a PNG variant on `session.attachment` and `file.raw`), [DH-D-UICORE-6](ui-core.md#dh-d-uicore-6) (`#dialogs` and modal opening). Area C4's node views call the gallery partial.
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P2. **Done when.** DH-T-UICHAT-17 passes and [DH-E-UICHAT-6](#dh-e-uichat-6) has passed.

## 9. Acceptance

### 9.1 Local tests

All are Go tests with fakes at the service boundary; none drives a browser.

| Test ID | Proves | What is asserted |
|---|---|---|
| DH-T-UICHAT-1 | D-1, ADR-1 | Golden: the composer fragment is a working plain form (text field, request id, a button per mode); it contains no inline script or style; a POST with only the text field admits a prompt |
| DH-T-UICHAT-2 | D-2, ADR-3 | The import map generated from the manifest names every vendored module once and only files under the asset route, and is registered through `AllowInlineData` as type `importmap`; with the composer loaded the page's one inline block is that map, byte-equal to the registered text (A19's DH-T-API-25 checks its hash in the header, C1's DH-T-UICORE-7 the rest); the island module is under its size cap and contains no `innerHTML`, `eval` or `Function(` |
| DH-T-UICHAT-3 | D-3, ADR-2 | For a corpus of drafts, `Spans` finds the same mentions whether typed or picked; a session mention of a session the principal cannot read, and a malformed token, refuse admission with the span listed; a label sent by the browser is never read |
| DH-T-UICHAT-4 | D-3, ADR-2 | The spans recorded at admission are what the sent-text view draws; deleting the file afterwards changes no fragment; a replay of the session draws the same chips |
| DH-T-UICHAT-5 | D-4, ADR-4 | Draft save, prefill and restore per session and principal; a stale revision is 409; another session's and another principal's drafts are untouched; an oversized draft is refused; a malformed record renders an empty composer with a diagnostic |
| DH-T-UICHAT-6 | D-4 | An upload adds its reference to the draft and answers with the attachment token; without the island the token is appended to the draft text; each refusal code renders its message inside the chip view and stores nothing; detach removes the reference and its token; at admission the message's attachments are the staged references in token order, each token becomes `[attachment <n>: <name>]` in the model-visible text and a display span, a token naming an unstaged reference is 422 `attachment/not-staged`, and a staged reference with no token is detached and not sent; a staged object survives a collection run and is collectable after the draft is cleared |
| DH-T-UICHAT-7 | D-1, ADR-5 | An accepted prompt clears the draft and mints a new request id; the same request id twice admits one message; a host stop between admission and clear leaves no second message and an empty composer on the next render; a 422 re-renders the form with the posted text |
| DH-T-UICHAT-8 | D-1, ADR-5 | With a running agent, the default button maps to the busy-mode preference and the other button to its complement; "Steer now" removes the queued item and steers; Stop cancels with cause `user` and keeps the inbox; the seat shows Stop only while running with an empty draft |
| DH-T-UICHAT-9 | D-3, ADR-6 | Menu order for `@` and `/`; a source that errs or exceeds the deadline is a named empty group and the others are listed; a source registered through an agent's scope appears only for that session and leaves with it; completion creates no agent |
| DH-T-UICHAT-10 | D-3 | A command typed by its Chinese or English title resolves to one host name; a command without attachment support refuses a submit that carries attachments and the draft is unchanged; an unknown command is never sent to the model |
| DH-T-UICHAT-11 | D-5, ADR-7 | Table: every published format is masked with its kept prefix; named assignments, e-mail addresses and ordinary prose are not; a value released by the secret store is masked in any shape; overlapping candidates join the earliest; fuzzed input never panics |
| DH-T-UICHAT-12 | D-5, ADR-7 | A rendered bubble, dock row, rail preview and title contain no original credential; a credential inside a mention is masked in the chip label; the reveal route refuses a non-owner and is uncached; a budget test bounds time on text with and without credentials |
| DH-T-UICHAT-13 | D-6, ADR-8 | Golden corpus for the planner: replies stay outside a folded group under `all` and join it under `final`; `verbose` folds nothing; hidden rows; the retry receipt; the tool-change row; the inherited-history divider; preparing, start and result share one row; the same events and preferences always give the same rows |
| DH-T-UICHAT-14 | D-6 | The rail lists every started turn from the outline, loaded or not; a preview for an open turn carries its latest committed text, masked; a jump link names a window around the turn; English and Chinese hold every key of this area |
| DH-T-UICHAT-15 | D-7 | The picker groups by route, marks the current choice, lists an unavailable model disabled with its reason, shows badges in both entries and filters by `q`; a selection missing from the catalogue leaves Send enabled; a failed listing renders a retry row |
| DH-T-UICHAT-16 | D-8 | The rating control is absent without access; a rating with a stale version re-renders the control; an oversized note is 422 beside the field; recording calls no preview and no upload; a committed feedback event redraws the control in a second connection |
| DH-T-UICHAT-17 | D-9, ADR-9 | The gallery partial renders the same markup from four callers; a download link carries the disposition; attach-by-reference refuses an object the session cannot read; the dialog fragment has no inline handler |

### 9.2 Real-environment scenarios

Each uses the built binary, a temporary harness home and real browsers through the driver of [libs/playwright.md](../libs/playwright.md); the model is the replay provider unless stated. They are a different kind of evidence from the tests above.

<a id="dh-e-uichat-1"></a>
#### DH-E-UICHAT-1 The composer island in real browsers under the real policy, and its fallback
- **Proves.** DH-D-UICHAT-2, DH-D-UICHAT-3.
- **Given.** Chromium, Firefox and WebKit, each recording policy violations; a workspace with known files and a second session; an input method that composes (Pinyin); a touch device or emulation with an on-screen keyboard.
- **When.** In each browser: (1) open a session and focus the composer; (2) type `@` and part of a file name, pick with the keyboard, type on; (3) Backspace at the chip's edge twice, undo, redo; (4) paste text that contains markup, then undo; (5) compose a word and confirm it with Enter; (6) type `/`, pick a command with arguments; (7) reload; (8) send; (9) on the touch device edit the end of a long draft; (10) block the island's module and reload, type a message with an `@path` token, send; (11) with the module back, paste an image file into the middle of a sentence, then press Backspace at the chip's edge twice and undo.
- **Then.** (1) every module loads from the host and the violation count is zero; (2) one chip with its label, caret after it; (3) the chip is selected, then removed whole, restored, removed; (4) the paste is plain text and one undo step; (5) the candidate is confirmed and nothing is sent; (6) one leading command chip; (7) text and chips return; (8) one message is admitted whose text holds the canonical tokens, and the draft clears; (9) the caret stays above the keyboard and the page does not zoom; (10) the plain form sends and the sent message shows the same chip; (11) an attachment chip appears at the caret inside the text, shows the upload's progress and then the image, is selected and then removed whole, and undo restores it with its stored reference. The island's self-test table passes in the page.
- **Cleanup.** Stop the host; delete the home and workspace. On failure keep the session export, violation reports and a screenshot per browser.

<a id="dh-e-uichat-2"></a>
#### DH-E-UICHAT-2 Sending while a turn runs, stopping, and resending after a lost connection
- **Proves.** DH-D-UICHAT-1, DH-D-UICHAT-7.
- **Given.** The built `dsh` in its web profile with a temporary home; a real Chromium-family browser with two tabs on one session; a replay that keeps a turn running for a known time; a proxy that can cut the connection. Step (5), the model picker, is engine-sensitive: it also runs in current Firefox and Safari ([DH-ADR-39](../design.md#dh-adr-39)), because upstream had to skip its model menu test on WebKit, whose engine crashed on it (`UP-DEBT-found.Q3.3`). A crash in any engine is recorded as a failed step, never as a skip, and keeps the scenario open.
- **When.** (1) Send a message. (2) While it runs, send a second with Enter and a third with Ctrl+Enter. (3) Edit the queued row, then "Steer now". (4) Press Stop. (5) Open the model picker by its control and with `/model`, search, and choose another model. (6) Cut the connection, press Send, restore it, press Send again. (7) Kill and restart the host with a message queued and a draft typed.
- **Then.** (1) the message appears once in both tabs. (2) one row is queued and one is steering, in both tabs, in the host's order. (3) the edit is shown and the row moves to steering. (4) the turn ends as cancelled and the queued row is still there. (5) the control shows the choice in both tabs and the next request header in the log names it. (6) exactly one message is admitted. (7) the queued row reappears and the draft is intact.
- **Cleanup.** Delete the session, stop the host, delete the home and the browser profiles; keep the session export on failure.

<a id="dh-e-uichat-3"></a>
#### DH-E-UICHAT-3 Attachments: pick, drop, paste, limits, failure, removal, reload
- **Proves.** DH-D-UICHAT-4.
- **Given.** The built `dsh` in its web profile with a temporary home and a real model API key; a real Chromium-family browser signed in as the owner on a session; a small image, a text file, a file over the size limit, a corrupt image, a folder.
- **When.** Type a sentence, put the caret in its middle and pick the image; drop the text file on the transcript; paste an image; drop the oversized file, the corrupt image and the folder; cut the connection during an upload and press Retry in its chip; remove one chip with Backspace and one with its Remove control; reload; restart the host; send.
- **Then.** Each file appears at once as a chip at the caret, inside the text, with its progress; each refusal names its reason inside its chip and stores nothing; Retry completes the cut transfer; Send is unavailable while a chip uploads or has failed; after reload and restart the remaining chips are where they were; the sent message carries exactly those files in text order and its bubble shows the chips in the same places; the removed objects are collectable.
- **Cleanup.** Delete the session, stop the host, delete the home and the browser profile. On failure keep the home's `blobs` listing and the session export.

<a id="dh-e-uichat-4"></a>
#### DH-E-UICHAT-4 A pasted credential is masked wherever the page shows it
- **Proves.** DH-D-UICHAT-5.
- **Given.** The built `dsh` in its web profile with a temporary home and the replay model provider; a real Chromium-family browser signed in as the owner; a host log at debug level; a planted test key in a published format.
- **When.** Type a message containing the key; wait for the draft save; send; open the session list and the turn rail; reveal the value; reload.
- **Then.** The strip warns before sending; the page source of the transcript, list and rail contains no full key; reveal shows it and a reload masks it again; the session export contains the original; the host log contains neither the key nor the message text.
- **Cleanup.** Delete the session and the planted key from the store, stop the host, delete the home and the browser profile.

<a id="dh-e-uichat-5"></a>
#### DH-E-UICHAT-5 Reading a long conversation: fold, page back, jump
- **Proves.** DH-D-UICHAT-6.
- **Given.** Chromium, Firefox and WebKit; a recorded session of several hundred messages with tool calls, intermediate replies and a notice-started turn; a replay that streams a long answer with a tool call.
- **When.** Open the session; let the answer stream and its turn end; scroll to the top repeatedly; jump by the rail to an unloaded turn; switch the preference to `final`, then to `verbose`.
- **Then.** The tool call is one row from "preparing" to its result; at turn end the process folds once and every reply stays visible; the notice-started turn folds nothing but its own process; each older page appears with the line being read at the same screen position, within a stated tolerance, or the browser is recorded as lacking scroll anchoring; the jump lands on the turn; under `final` only the last answer stays outside the group, and under `verbose` nothing is folded.
- **Cleanup.** Stop the host, delete the home and the browser profiles; keep a screen recording per browser on failure.

<a id="dh-e-uichat-6"></a>
#### DH-E-UICHAT-6 Feedback from two tabs, and taking a file out of the page
- **Proves.** DH-D-UICHAT-8, DH-D-UICHAT-9.
- **Given.** A real browser with two tabs; a recording endpoint configured as the upload destination but with upload off; a session with an assistant image.
- **When.** Rate an answer in tab one with a category and a note; retract it; open the image, switch to actual size, download it, copy it, drag it onto the composer, and in Chromium drag it to the desktop.
- **Then.** Tab two shows the rating and its retraction without a reload; the endpoint received nothing; focus cannot leave the dialog while it is open; the downloaded bytes equal the stored object; the clipboard holds a PNG; the composer gains an attachment chip for the image with no upload request; the desktop file equals the original, or the step is recorded as unsupported for that browser.
- **Cleanup.** Delete the session and the downloaded and desktop files, stop the host and the recording endpoint, delete the home and the browser profile.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Attachments are chips inside the text,** as the owner decided ([DH-ADR-21](../design.md#dh-adr-21), confirmed on 2026-11-19 after the audit). Upstream shows uploads in a rail; this design does not.
2. **Ownership, settled.** This area owns the model picker ([DH-D-UICHAT-7](#dh-d-uichat-7)), the feedback form ([DH-D-UICHAT-8](#dh-d-uichat-8)), the composer draft record ([DH-ADR-UICHAT-4](#dh-adr-uichat-4)) and the seat `composer.controls`. Following the tail is state of area C1's `keep` module. The library texts that still say otherwise (a per-tab draft in [libs/react.md](../libs/react.md), opaque handles and a component-written import map in [libs/lexical.md](../libs/lexical.md)) are requested to follow.
3. **Unverified, only a browser or a download can settle.** Lexical's published files (names, hashes, whether each imports its siblings by bare name, the licence of `@preact/signals-core`); whether a later release fixes the on-screen-keyboard defect upstream patched; whether Lexical writes inline styles, which the policy would block; that every supported browser accepts a hash source for an import map and serves modules imported later from a map in the head; scroll anchoring in each engine; `DownloadURL` outside Chromium.
4. **Assumed of [api.md](api.md) (requests sent to its owner).** The prompt route passes a command line to area A17 before admission, because a plain form has one action; `session.complete` takes the trigger name as `{kind}`; `session.upload` adds the stored reference to the caller's draft and `session.attachment` also serves a reference that draft holds; `session.queue` can move a queued message to steering; `session.create` accepts an initial model; a download disposition and a PNG variant on the byte routes.
5. **Settled with [agent-loop.md](agent-loop.md).** A `UserMessage` may carry a display annotation, stored beside the message and never sent to the model: the confirmed mention spans go there. Input kept across a cancel stays parked until the next waking send, so Stop never starts a turn by itself. A message id is unique over a session's whole history and a duplicate returns the first receipt, which is what resending with the same request id relies on.
6. **Assumed of [interaction-credentials.md](interaction-credentials.md), [context-compaction-skill.md](context-compaction-skill.md) and [session-views.md](session-views.md) (requests sent).** A17: the grammar of a command line (is `/usr/bin` a command name?), categories and limits for the form, the catalogue's translated titles. A14: the grammar package and principal-aware session candidates. **Open, owner [session-views.md](session-views.md) (not answered in this pass):** whether stored titles, excerpts and previews are masked when written, and whether the turn outline has an entry for an open turn. Designed against what it defines today: the views of this and other UI areas print such text through the `utext` helper at render time, and the turn rail lists closed turns only, with the open turn added from the live stream.
7. **Limits kept.** Five upstream limits are carried ([§5](#5-tech-debt-and-its-resolution)). The composer needs JavaScript for chips and menus at the caret. Masking is not confidentiality. Completion costs a request per keystroke. A 422 on send loses the editor's undo history. No fold animation, and no folding on the next message. The numbers marked "Inference" in [§6.2](#62-contracts) are unmeasured.
8. **Who reviews the island.** It is JavaScript in a Go repository with no type checker. Inference: every change to it needs a recorded run of [DH-E-UICHAT-1](#dh-e-uichat-1); the implementation plan must name that duty.

## 11. Units dispositioned by this spec

Ledger fragment: `trace/fragments/C3.jsonl`.

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 10 | 3 ported, 7 replaced |
| `UP-DEBT` | 30 | 22 resolved, 3 not applicable, 5 carried |
| `UP-DEC` (handed over) | 11 | 2 ported, 9 replaced |

Go items declared: `DH-ADR-UICHAT-1` to `-9`, `DH-D-UICHAT-1` to `-9`, `DH-T-UICHAT-1` to `-17`, `DH-E-UICHAT-1` to `-6`.
