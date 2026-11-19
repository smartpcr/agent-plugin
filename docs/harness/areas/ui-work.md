# Work views (trajectory, subagents, workflow runs, jobs, teams, deliverables, schedules, agent presets): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) (`2e39e70`) and are cited as `path:line` relative to the upstream root; a bare `ui-x/...` path is under `packages/client/`. Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `UIWORK`. Nothing here has been built, run, measured or tested.

| | |
|---|---|
| **Area key** | `UIWORK` (agent C5) |
| **Upstream packages** | 8, all under `packages/client`: `ui-trajectory`, `ui-subagent`, `ui-workflow-run`, `ui-jobs`, `ui-agent-team`, `ui-deliverables`, `ui-schedule`, `ui-agent-preset` |
| **Serves outcomes** | [DH-O-12](../design.md#dh-o-12) first; also [DH-O-9](../design.md#dh-o-9) (delegated and scheduled work is visible and stoppable), [DH-O-14](../design.md#dh-o-14) (what grows with history is bounded), [DH-O-5](../design.md#dh-o-5) (honest state after a crash), [DH-O-7](../design.md#dh-o-7) (file bytes served only by a declared route) |
| **Depends on areas** | A19 ([api.md](api.md): routes, access, event stream, job routes), C1 ([ui-core.md](ui-core.md): page shell, regions, view contract, catalogue, preferences), C4 ([ui-conversation.md](ui-conversation.md): node registry, `render`, tool cards), A06 ([session-views.md](session-views.md): projections, query), A02 and A01 ([agent-loop.md](agent-loop.md), [core-session.md](core-session.md): turns, steps, event types), A03 ([tools.md](tools.md): `tools.Settled`, nested calls), A04 (`tokenUsage`), A12 ([subagent-team.md](subagent-team.md)), A11 ([ptc-workflow.md](ptc-workflow.md)), A13 ([jobs-schedule-goal-plan.md](jobs-schedule-goal-plan.md)), A08 ([filesystem.md](filesystem.md): `deliver.Changes`), A27 ([bundle-preset.md](bundle-preset.md): `agentpreset.Service`), A23 (recurrence, zone data), C3 ([ui-chat.md](ui-chat.md): completion sources), C6 and C2 (session panel, primitives; not written) |
| **Priority** | P1 as a whole: a first working harness shows the conversation without these views, but none of the work an agent delegates, schedules or produces is visible or stoppable from the browser without them. P2 for the timing overview and live tool values. Each deliverable states its one priority in [§8](#8-deliverables) |

## 1. Purpose

An agent does more than answer. It takes steps, calls tools, starts other agents, runs scripts and background commands, changes files and leaves reminders for later. This area is the set of views in which a person **sees that work and its results, and stops it**: a step-by-step record of what the agent did (the trajectory), the tree of subagents, workflow runs, background jobs, a team's task board, the files a turn produced, the schedules of the harness home, and the agent preset a session runs.

Each view is a **read model**: it owns no state. Another area owns the facts (a session log, a registry, a table) and the operations (interrupt, kill, pause); this area turns them into HTML, registers the routes a browser needs under area A19's rules, and says plainly when what it shows is behind or not available. Without it the work would still happen, recorded and owned, and the user could see it only by reading an exported log.

## 2. Vocabulary

Terms defined elsewhere are used as written: **turn, step, attempt** ([../design.md §2](../design.md#2-vocabulary)); **route, region, tab, interest, topic, cursor, `reset`, request id, activation** ([api.md §2](api.md#2-vocabulary)); **contribution, host preference, per-tab store** ([ui-core.md §2](ui-core.md#2-vocabulary)); **node** ([ui-conversation.md](ui-conversation.md)); **current, behind, pending, cut, head** ([session-views.md §2](session-views.md#2-vocabulary)); **tree, root, creator, activation (of a child), tree ledger, settlement, name, path, dismissal, task board** ([subagent-team.md §2](subagent-team.md#2-vocabulary)); **run, member, run record, narration** ([ptc-workflow.md §2](ptc-workflow.md#2-vocabulary)); **job, output ring, schedule, occurrence, committed occurrence, gap, overlap, target, delivery, firing host** ([jobs-schedule-goal-plan.md §2](jobs-schedule-goal-plan.md#2-vocabulary)); **deliverable** ([filesystem.md §2](filesystem.md)); **preset, declaration, revision, digest, blank session** ([bundle-preset.md §2](bundle-preset.md#2-vocabulary)).

| Term | What it is | Why it exists |
|---|---|---|
| **Work view** | One of the eight views of this area. | The unit that is composed in or out: each is one component. |
| **Read model** | A view computed from state another area owns; it can be thrown away and drawn again. | Two tabs, a reload and a restart must agree, so nothing may live only in the view. |
| **Owner (of a view's state)** | The area and service whose facts a view draws and whose operation a control calls. | Every view names one, so a reader knows where a wrong number comes from. |
| **Trajectory, record** | The trajectory is the ordered list of everything that entered or left the model in one session, with timing and token use. A record is one row of it: a prompt, a context change, a model answer, a tool call with its result, a nested call, a compaction. | The conversation view shows what a person should read; the trajectory shows what happened, for debugging and cost. |
| **Closed step, open step** | A closed step has its `step/end` in the log; its records can no longer change. The open step is the one in flight. | They update differently: closed steps are facts fetched once; the open step is state that is replaced. |
| **Inspector** | The detail of one record: input, output, reasoning, timing, token use, the tool schema as the model saw it. | A row is one line; the detail may be megabytes and is fetched only when opened. |
| **Overview** | A strip above the records that draws each record as a mark on a time axis. | Shows where a turn's time went at a glance. |
| **Live value** | The typed value a tool returned, before it was shaped into the text the model saw. Held in memory only, for a live session. | Debugging a tool needs the value; the log holds only the model-visible form ([tools.md](tools.md), `DH-ADR-TOOLS-4`). |
| **Panel section** | A block in the session side panel (subagents, shared tasks, background jobs). The panel is area C6's. | Work in progress is shown beside the conversation, not in it. |
| **Freshness marker** | A short line on a region saying it is `behind` (valid at an older cut) or `pending` (being rebuilt), or that its service is `unavailable`. | A stale or missing value must never look like a current or empty one. |
| **Base revision** | The revision of a schedule or task that a form was rendered from. | A save names it, so an edit made elsewhere in between is detected, not overwritten. |
| **Next-run preview** | The next occurrences of a timing rule, computed by the host without saving anything. | The user sees what a rule means, including on clock-change days, before creating it. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.client.ui-trajectory` | `dsh-client-ui-trajectory` | consumer, plus a small host provider (`ctx.sessionToolResults`) | The Trajectory tab: records grouped by turn and step, a timing overview, an inspector, live raw tool values (`ui-trajectory/README.md:12`) |
| `UP-PKG-packages.client.ui-subagent` | `dsh-client-ui-subagent` | consumer | Subagents section of the session panel, breadcrumb switcher, child conversation tab, the `@` source (`ui-subagent/README.md:12`) |
| `UP-PKG-packages.client.ui-workflow-run` | `dsh-client-ui-workflow-run` | consumer | One chat node per top-level workflow run, with phases and members (`ui-workflow-run/README.md:12`) |
| `UP-PKG-packages.client.ui-jobs` | `dsh-client-ui-jobs` | consumer | Background jobs section: live and settled rows, stop, an output panel (`ui-jobs/README.md:12`) |
| `UP-PKG-packages.client.ui-agent-team` | `dsh-client-ui-agent-team` | consumer | Shared tasks section and the "Agent teamwork" switch in the model menu (`ui-agent-team/README.md:12`, `:28`) |
| `UP-PKG-packages.client.ui-deliverables` | `dsh-client-ui-deliverables` | consumer, plus host routes for native file opening | Changed-files card, review tab with comparisons, delivery cards, file links in the closing text (`ui-deliverables/README.md:12`) |
| `UP-PKG-packages.client.ui-schedule` | `dsh-client-ui-schedule` | consumer | "Automation tasks" page and side tab: list, editor, delivery records; clock marks on sessions (`ui-schedule/README.md:12`) |
| `UP-PKG-packages.client.ui-agent-preset` | `dsh-client-ui-agent-preset` | consumer | Preset roster and default in Settings, the per-session picker, a read-only configuration viewer (`ui-agent-preset/README.md:12`, `:30`) |

### 3.2 Behaviour

**Trajectory.** The view registers one entry (`trajectory`, order 10) in the conversation's view ring (`ui-trajectory/src/client/index.ts:80-83`). It is a pure projection of the shared session window and neither reads nor changes the chat snapshot (`README.md:58`). Records have seven kinds: `system`, `user`, `context`, `compacted`, `message`, `tool`, `subtool` (`src/client/trajectory-record.ts:9-16`); nested calls fold under their parent tool, and turns, calls and nested calls fold independently from a toolbar whose search also covers folded rows (`README.md:32`). Selecting a record opens an inspector with token use, duration, input, output, timing and images; a `run_code` call shows numbered, highlighted source (`README.md:34`, `:36`). A request stays pending while it waits, streams or retries, and retry counts and the last attempt error stay visible after recovery (`README.md:40`). An overview projects each record's real start and duration on a time axis, dividing an assistant span into time to first token and decoding; dragging selects an interval, the wheel zooms, a right-button drag pans (`README.md:46-48`). Long histories open on 50 nodes at the tail, load older pages on demand without ever releasing loaded ones, and mount only visible rows through a virtualiser (`README.md:74`; `src/client/TrajectoryView.tsx:36`; `src/client/TrajectoryTable.tsx:52`). The package's host half keeps each successful tool's final value in memory per live session, at most `maxResultsPerSession` (default 1000), evicting the oldest, and serves it by result sequence over a stream; nothing is persisted and a cold session has none (`src/index.ts:34`, `:45-56`, `:68-74`, `:105-126`; `README.md:70`).

**Subagents.** The section lists the session's direct children from the `subagentCatalog` projection held in the client's session cache; showing it reads nothing, and a level is read only when a branch is expanded or a switcher is opened, one level at a time (`ui-subagent/README.md:28`, `:42`, `:64`). Rows show a status glyph (running, completed normally, other), the creation description, the agent name, token total and duration, with a hover card after 500 ms; each visible level runs its own one-second clock (`README.md:42`, `:74`). A continuable child keeps a composer and a Stop that route through `subagents/prompt` and `subagents/interruptByUser`; one-shot and external children are read-only (`README.md:46`, `:78`). The `@` source inserts a running child's label as literal text with no meaning to the host (`README.md:50`).

**Workflow runs.** One chat node per run is replayed from four durable events (`tool-workflow/run-start`, `agent-start`, `agent-end`, `run-end`; `ui-workflow-run/src/client/workflow-definition.ts:158-161`). Running, failed, cancelled and interrupted levels open by default and completed ones stay closed (`README.md:12`, `:52`). It shows identities and statuses only; a member can be opened only while it runs (`README.md:32`, `:93`).

**Jobs.** The section mirrors one roster stream; live rows come first, settled rows fold under "Finished" with a client-only "Clear" (`ui-jobs/README.md:30`, `:36`). Expanding a row opens that job's output stream and collapsing closes it (`README.md:42`, `:52`). Stop is two presses within three seconds and calls the kill, which leaves the model's completion notice intact (`README.md:38`; `src/client/JobsSection.tsx:65`).

**Teams.** The switch calls `agentTeams.setEnabled` and asks for confirmation once the session has recorded a model request (`ui-agent-team/README.md:28-30`; `src/client/mount.ts:45`). The board reads the `agentTeam` projection, orders tasks in progress, pending, completed, and is read-only (`README.md:28`, `:40`; `src/client/TeamBoard.tsx:137`).

**Deliverables.** A finished turn ends with a changed-files card read from a summary the host keeps in memory for the turn's `workspace/changes` announcement; after a host restart there is no card (`ui-deliverables/README.md:28`, `:45`). A row opens a review tab with unified or side-by-side comparison, drawn up to 5,000 lines (`README.md:49`; `src/client/FileDiff.tsx:14`). Files declared with `present` get cards after the turn ends, with a control that opens the file in a desktop application of the host (`README.md:37-39`). Inline-code tokens in the closing text become links only when they match a recorded path exactly, by a recorded argument or by a unique basename; nothing is guessed from the text (`README.md:57`).

**Schedules.** One editor serves the page and the side tab (`ui-schedule/README.md:12`). The list filters by Enabled, Paused, Ended and All and searches title, prompt and session id (`README.md:34`). The repeat editor offers once, minute, hour, day, week, month and custom; guided presets produce an RRULE, and Custom accepts an RRULE or a five-field cron expression with an explicit zone (`README.md:38`). The browser runs its own copy of the recurrence evaluator to validate and to find the first future occurrence across daylight-saving gaps (`src/client/editor-timing.ts:3`; `README.md:64`), and takes zone names from the browser's `Intl` data (`README.md:50`). A save sends changed fields with `expectedUpdatedAt`; on a conflict the draft is kept and the user reviews it against the latest values or discards it (`README.md:34`, `:62`; `src/client/TaskDetail.tsx:279`). "Next run" is the later of the scheduled occurrence and the host's delivery floor (`README.md:42`). Records page 20 saved deliveries, newest first, and acknowledge delivery to the inbox, not that the model succeeded (`README.md:44`; `src/client/DeliveryHistory.tsx:93`).

**Agent presets.** `agentPresets/list` supplies the roster and the default; `agentPresets/read` returns one declaration as read-only YAML (`ui-agent-preset/README.md:52`; `src/client/section-store.ts:48`, `:62`). The picker changes the preset of a blank session only; the page edits nothing and sends authoring to a "Creator" session (`README.md:30`, `:40`). With the "Coding Tools" setting off, two presets are hidden and a saved default is reset (`README.md:32-34`).

### 3.3 Failure, retry, rollback and cleanup upstream

| Situation | What upstream does | What is left |
|---|---|---|
| A catalog, summary, comparison or record read fails | The view keeps what it has and offers Retry (`ui-subagent/README.md:42`; `ui-deliverables/README.md:49`; `ui-schedule/README.md:44`) | A partly loaded tree or list; a rejected level read stops later siblings and is only logged (`ui-subagent/README.md:128`) |
| The connection is replaced | Client caches of summaries, comparisons and desktop metadata are cleared and read again (`ui-deliverables/README.md:67`) | Unsaved schedule drafts survive; they are lost on reload (`ui-schedule/README.md:62`) |
| The session unloads, or the trajectory plugin unloads | Every live tool value is cleared and open inspectors are told (`ui-trajectory/src/index.ts:68-79`) | Rendered text stays; raw values are gone for good |
| The host restarts | Change summaries are gone, so earlier turns lose their card (`ui-deliverables/README.md:28`) | A workflow run with no terminal event stays "running" (`ui-workflow-run/README.md:91`) |
| A schedule was edited elsewhere while a draft is open | Save is disabled; the user reapplies or discards (`ui-schedule/README.md:34`) | Nothing is overwritten |
| A stop or kill is rejected | "Stop failed" for four seconds (`ui-jobs/src/client/JobsSection.tsx:68`) | The job keeps running |

## 4. Upstream decisions dissected

No decision note was handed to this area. The rows are decisions visible in READMEs and code.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| Trajectory is a pure projection of session events, separate from the chat fold (`ui-trajectory/README.md:58`) | Two readers of one log cannot corrupt each other | keep | It is the rule that makes page and live update agree; the fold moves to the host | [UIWORK-2](#dh-adr-uiwork-2) |
| Tail first, 50 nodes, older pages on demand, nothing released (`README.md:74`) | Opening cost independent of history | keep, change the unit | Pages are step-aligned so a call and its result never straddle a page; loaded pages are still not released, and the page says so | [UIWORK-2](#dh-adr-uiwork-2) |
| Rows are virtualised in the browser (`src/client/TrajectoryTable.tsx:52`) | A long ledger must not mount every row | drop | Bounded by host paging and lazy bodies ([DH-ADR-REACTVIRTUAL-1](../libs/tanstack-react-virtual.md#dh-adr-reactvirtual-1)) | [UIWORK-2](#dh-adr-uiwork-2) |
| The overview is driven by drag, wheel and right-button gestures (`README.md:48`) | Fast exploration of a time axis | change | Pointer tracking would need an island; turns and steps are the intervals people select, and they are links | [UIWORK-3](#dh-adr-uiwork-3) |
| An in-flight record shows a start marker and no invented duration (`README.md:114`) | A view must not fabricate a fact | keep | Elapsed time since a recorded start is shown as such, never as a duration | [UIWORK-2](#dh-adr-uiwork-2) |
| Record selection is local, with no links (`README.md:114`) | Selection was view state of one client | change | The URL is where navigation state lives ([DH-ADR-REACT-1](../libs/react.md#dh-adr-react-1)) | [UIWORK-2](#dh-adr-uiwork-2) |
| Raw tool values live in memory per live session, bounded by count, never persisted (`README.md:70`) | Debugging without growing the log | keep, add a byte bound | A count bound alone lets 1000 large values pin memory | [UIWORK-4](#dh-adr-uiwork-4) |
| The subagent tree is discovered level by level from a client cache; showing reads nothing (`ui-subagent/README.md:42`, `:64`) | Avoids requests for a hidden panel | drop | The host has the whole ledger; one bounded read draws the tree and removes three documented gaps | [UIWORK-5](#dh-adr-uiwork-5) |
| Outcomes other than a normal completion share one glyph; Stop exists only in a running child's composer (`README.md:126`) | The catalog carried no outcome | change | The ledger records the stop reason of every run, and the manager lets an owner interrupt any member | [UIWORK-5](#dh-adr-uiwork-5) |
| An `@` reference is the display label as plain text (`README.md:50`, `:130`) | Labels can repeat, so no meaning was attached | change | A path is unique in a tree and is what `send_message` accepts | [UIWORK-5](#dh-adr-uiwork-5) |
| The workflow node replays durable events and opens abnormal levels by default (`ui-workflow-run/README.md:48`, `:52`) | Same state from replay, prepend and live append | keep | It is the node contract of area C4 | [DH-D-UIWORK-6](#dh-d-uiwork-6) |
| The workflow node shows identity and status only (`README.md:93`) | Scope of a first version | change | The run record holds limits, sandbox facts, stop reason and usage; a detail fragment shows them | [DH-D-UIWORK-6](#dh-d-uiwork-6) |
| Job output is observed only while its row is expanded (`ui-jobs/README.md:52`) | No stream for what nobody watches | keep | It is exactly an interest of area A19's stream | [DH-D-UIWORK-7](#dh-d-uiwork-7) |
| "Clear" hides settled jobs in one client; Stop is armed by a three-second timer (`README.md:30`, `:38`) | Tidy list; guard against a slip | drop the first, change the second | The registry already bounds settled records; a hidden-in-one-browser list is a second copy of state. A confirm form needs no timer | [UIWORK-1](#dh-adr-uiwork-1) |
| The task board is read-only; turning teamwork off asks first (`ui-agent-team/README.md:30`, `:40`) | Agents own tasks; the switch changes the model's tools | keep the confirmation, change the first | Area A12's board accepts an owner principal as actor, so a person can reassign or remove a stuck task | [DH-D-UIWORK-5](#dh-d-uiwork-5) |
| Linked paths come from recorded facts, never from the text (`ui-deliverables/README.md:12`, `:57`) | A model's prose must not mint a file link | keep | It is a security property; the matcher gains one safe case | [UIWORK-7](#dh-adr-uiwork-7) |
| The changed-files card is read from host memory and delivery cards wait for the turn's end (`README.md:28`, `:37`) | The summary was not in the log | change | Area A08 puts the summary in the event and the hunks in the blob store; a card can be drawn at any time, for any session | [UIWORK-7](#dh-adr-uiwork-7) |
| Files open in a desktop application of the host (`README.md:39`) | The desktop product had one | drop | No desktop application ([DH-ADR-18](../design.md#dh-adr-18)); the browser previews and downloads | [UIWORK-7](#dh-adr-uiwork-7) |
| The schedule editor validates with a browser copy of the evaluator and lists zones from the browser (`ui-schedule/src/client/editor-timing.ts:3`; `README.md:50`) | Instant feedback without a request | drop | Two evaluators can disagree; [../libs/rrule.md](../libs/rrule.md) already drops the copy. The host answers as the user types | [UIWORK-6](#dh-adr-uiwork-6) |
| A save is compare-and-set and a conflict keeps the draft (`README.md:34`, `:62`) | No lost update, no lost typing | keep | Uses the schedule row's revision | [UIWORK-6](#dh-adr-uiwork-6) |
| The preset page is read-only; presets are hidden by the "Coding Tools" setting (`ui-agent-preset/README.md:30`, `:32`) | Authoring is a composition task; the setting simplified the menu | keep the first, drop the second | The roster is the set of declarations; an operator removes one with a layer operation, not a hidden coupling between two settings | [UIWORK-8](#dh-adr-uiwork-8) |
| Hover cards after 500 ms and a clock per visible list (`ui-subagent/README.md:42`, `:74`; `ui-jobs/README.md:30`) | Dense rows with detail on demand | change | Hover needs timers and fails on touch; a row is a disclosure, and elapsed time is one shared behaviour | [UIWORK-1](#dh-adr-uiwork-1) |

## 5. Tech debt and its resolution

All 27 units are bullets from the eight READMEs' "Known Limitations". They are grouped by cause; each unit's own reason is in its ledger row. `L:` stands for `UP-DEBT-limit.packages.client.`. "Carried" rows keep a limit on purpose and name stage [DH-S-59](../impl.md#dh-s-59).

| Group (cause) | Units | How the Go design answers it |
|---|---|---|
| **A. The view shows less than the host records.** Grouped outcomes and no stop, a workflow node without detail, unlabelled output channels, blank in-flight time and no links, terse tool-change rows, records that say only "delivered" | `L:ui-subagent.1`, `L:ui-workflow-run.4`, `L:ui-jobs.1`, `L:ui-trajectory.1`, `.2`, `L:ui-schedule.1` (6) | Resolved. Each view draws the owner's full vocabulary: seven stop reasons and a run id per activation, the run record's limits and endings, chunks tinted by channel, a start time with elapsed time and a URL per record, one line per added or removed tool, and the delivery states `enqueued`, `ran`, `discarded`, `skipped`, `failed` with a link to the turn ([DH-D-UIWORK-1](#dh-d-uiwork-1), [-4](#dh-d-uiwork-4), [-6](#dh-d-uiwork-6), [-7](#dh-d-uiwork-7), [-9](#dh-d-uiwork-9)) |
| **B. A tree discovered through a client cache.** Uncached ancestor levels, discovery that stops on a failed read, a section that cannot expand | `L:ui-subagent.2`, `.3`, `.4` (3) | Resolved. The host renders the tree from the ledger in one bounded read; there is no level to prepare ([UIWORK-5](#dh-adr-uiwork-5)) |
| **C. Facts that were not in the log.** Runs without a record, a lost run that stays "running", members that cannot be opened later, declarations without content, shell-created files that cannot be linked, comparisons that carry whole files, links that open a session and not a message, history bounded by count but not bytes | `L:ui-workflow-run.1`, `.2`, `.3`, `L:ui-deliverables.3`, `.4`, `.7`, `L:ui-schedule.2`, `.3` (8) | Resolved by the owning areas' records, which these views draw: every run has a record and a closer after a crash (A11); a member row links to the child's stored session; the changes event lists shell edits and stores bounded hunks, and `present` keeps a digest and a bounded copy (A08); a delivery row names its message and turn, and a delete reports whether a queued message was retracted (A13). A records page is bounded by `RecordsPage` times `maxPromptBytes` |
| **D. A desktop shell.** Local images that need an HTTP page, native opening that needs a host desktop | `L:ui-deliverables.1`, `.5` (2) | Resolved by [DH-ADR-18](../design.md#dh-adr-18): every page is HTTP, and the action that always exists is the browser's own (preview, download) |
| **E. Matching and authority rules.** Mentions matched by exact path or unique basename only, `@` labels without meaning, no authorization of the model's schedule tools in the UI | `L:ui-deliverables.2`, `L:ui-subagent.5`, `L:ui-schedule.4` (3) | Resolved. A unique suffix on a path-segment boundary links; an `@` reference is the member's path; the host enforces who may schedule (A13, `DH-ADR-JOBS-8`) and each row shows whether a person or the model wrote it |
| **F. Limits kept on purpose.** No starting or renaming a teammate and advisory write scopes, highlighting per hunk, files outside the workspace found only by their recorded path, no preset editor | `L:ui-agent-team.2`, `L:ui-deliverables.6`, `.8`, `L:ui-agent-preset.1` (4) | Carried, each narrowed. A person can interrupt and dismiss a member; starting and naming stay the model's. The 5,000-line cap is gone (hunks are paged); colour is still computed per hunk because only hunks are stored. A presented file has a kept copy and a comparison its stored hunks, so only "open the current file" needs the old path. A preset child is edited as a layer entry, never by replacing the list ([UIWORK-8](#dh-adr-uiwork-8)) |
| **G. A supervisor reads every member to see what agents told each other.** No tree-wide message timeline | `L:ui-agent-team.1` (1) | Resolved by the read-only timeline of [DH-D-UIWORK-5](#dh-d-uiwork-5) over `Query.TreeMessages` (register entry U-K2-6); messages are also shown in each member's conversation with the sender's path |

Totals: 23 resolved, 0 not applicable, 4 carried.

## 6. Go design

### 6.1 Shape

Packages are under `src/harness/internal/ui/work`, in the family [../design.md §4.2](../design.md#42-go-module-layout) gives to the UI areas. Each view is one component that injects its owner's service, so a composition without the owner has neither the view nor its routes ([DH-D-API-1](api.md#dh-d-api-1)). No component here provides a service except `values`.

| Package | Component | Injects (owner) | What it registers |
|---|---|---|---|
| `work` | none | n/a | Shared partials: the freshness marker, the panel-section frame with its fold, the elapsed-time element, the confirm form |
| `work/trajectory` | `ui.trajectory` | `query.Key`, `projection.Key` (A06), `render.Key`, `toolcard.Key` (C4), `values.Key` (optional) | The trajectory page, its routes, the state source `trajectory`, a link in `shell.header` |
| `work/trajectory/values` | `ui.trajectory.values`; provides `values.Key` (`Store`) | `tools.Key` (A03), `agent.Agents` (A02) | A `tools.Settled` listener and the bounded store |
| `work/tree` | `ui.subagents` | `subagent.Key` (A12), `query.Key` | The Subagents section, member routes, the breadcrumb, the `@` completion source (C3's `complete.Registry`) |
| `work/board` | `ui.team` | `team.Key` (A12) | The Shared tasks section, the mode switch, task forms |
| `work/run` | `ui.workflow-run` | `workflow.Key` (A11), `transcript.Key` (C4) | Node kind `workflow-run`, the run detail route, the state source `work.narration` |
| `work/jobs` | `ui.jobs` | `jobs.Key` (A13) | The views that area A19's job routes and its `jobs` and `job-output` sources render; the Background jobs section |
| `work/files` | `ui.deliverables` | `deliver.ChangesKey` (A08), `transcript.Key`, `render.Key`, A07's blob store | Node kinds `changes` and `presented`, the review and download routes, the mention resolver |
| `work/schedules` | `ui.schedule` | `schedule.Key` (A13), `workspace.Key` (A07), zone data (A23) | The schedules page and editor, routes, the state source `work.schedules`, session marks, the `schedule_create` card body |
| `work/presets` | `ui.agent-preset` | `agentpreset.Key` (A27), `settings.Key` (A07) | The picker, the roster and viewer routes, one usage event |

```go
// package work
type Fresh struct{ State projection.ValueState; AsOf, Head session.Seq } // current | behind | pending (A06)
func Marker(rq ui.Req, f Fresh) ui.SafeHTML                              // empty when current

// package trajectory. ctx is context.Context; v is query.Visibility.
type Record struct {
    ID     string            // "<kind>:<seq>" or the call id: stable across pages, the fragment of its URL
    Kind   Kind              // system | user | context | compaction | assistant | tool | subtool
    Seq    session.Seq       // the event that opened it
    Turn, Step, Request int  // Request counts model requests of the session, attempts included
    Parent string            // the outer call of a subtool
    State  State             // running | done | failed | interrupted | unknown
    Start  time.Time; Dur time.Duration; HasDur bool // HasDur is false while running: nothing is invented
    Usage  *Usage; Timing *Timing                    // assistant only: tokens by bucket; first token, decode rate
    Child  session.ID        // set when the call started a subagent: a link, never an inlined trajectory
}
type Builder interface {
    Page(ctx, v, s session.ID, w Window, at session.Seq) (Page, error) // closed steps only; step-aligned; Window: Tail | Before | After | Around
    Open(ctx, v, s session.ID) (OpenStep, work.Fresh, error)           // the step in flight, or none
    Detail(ctx, v, s session.ID, id, part string, from int) (Detail, error)
    Overview(ctx, v, s session.ID, from, to time.Time) (Overview, error)
}
// package values
type Store interface { Get(s session.ID, result session.Seq) (json.RawMessage, Availability) } // live | evicted | not-live
```

**One rule for all eight views** ([UIWORK-1](#dh-adr-uiwork-1)). A handler reads the owner, renders a typed view (area C1's `ui.View`), and returns. Something that changes while the page is open is drawn in exactly one of three ways. A **node** is a durable fact drawn inside the transcript by area C4's registry, on the `session` stream event. A **state region** is the current value of something, replaced whole on the `view` event through a source this area registers with area A19's hub. A **fetched fragment** is requested when a person opens a disclosure. Detail that upstream showed in a hover card is a `<details>` disclosure whose body is fetched on first open; running durations are one `<time>` element that area C1's `tick` module advances from a server-written start; a destructive control is a small form that is revealed, then confirmed. All text comes from the message catalogue, English and Chinese complete ([DH-ADR-UICORE-7](ui-core.md#dh-adr-uicore-7)); labels, paths, prompts and model text are data and are never translated.

**How the trajectory is built** ([UIWORK-2](#dh-adr-uiwork-2)). `Builder.Page` reads a run of whole steps through `query.Events` and folds them into records: `user/message`, `developer/message`, `system/message` and a changed `request/header` become input records of the step they precede; `assistant/attempt` and `assistant/message` become one assistant record per request, with failed attempts counted on it; `tool/call` with its `tool/result` is one tool record; `tool/subcall` with `tool/subresult` is a subtool record under its `parent`; `compaction/*` is a compaction record, inside its turn or in a "between turns" group. A record's title comes from the same `toolcard` presentation the conversation uses, so a call has one name in both views. Turn headers and "jump to turn" read area A06's `turn_index` rows. A step is the page unit because the pipeline commits a step's results in that step, so a call and its result are never on two pages; a `tool/late-return` is a short record of its own that links to its call. A tool record with more than `MaxSubRows` nested calls shows a count and pages them on demand.

**How the trajectory stays live.** A closed step never changes, so it is **pulled by cursor**: the element `#traj-more` at the end of the list holds the `seq` of the last closed step on the page and requests `rows?after={seq}`; the answer replaces that element with the new rows and a new `#traj-more`, and one request at a time is allowed (`hx-sync`), so a step can be neither skipped nor drawn twice, whatever the stream did. The open step is a **state region**, `#traj-open`, on source `trajectory` keyed by session: it is replaced whole whenever `projection.Changed` fires for the session, and each replacement makes `#traj-more` pull. A `reset` re-sends the region and triggers the same pull. The totals strip (`#traj-totals`: requests, tokens by bucket from A04's `tokenUsage`, model and tool time from A06's `stats`) is a second region of the same source.

**How a nested tree is shown.** The Subagents section is one nested list built from `subagent.Runtime.Tree(root, page)`: a member sits under its creator, to any depth the ledger holds, with its name, label, provider, the state of its latest run and a link to its conversation. Workflow members are the same sessions; a run's node lists them by phase and links to the same pages. A tool record that started a child links to that child's own trajectory. Nothing is inlined across sessions, so every page stays bounded by its own log.

### 6.2 Contracts

**The eight views.** "Owner" is the area whose state is drawn and the call that reads it.

| View | What the user sees and does | Owner and read | Live update | While behind, pending or unavailable |
|---|---|---|---|---|
| Trajectory | A page per session: records by turn and step, folding, search and kind filter, an inspector per record, the overview, totals; "Load earlier"; a link to any record | A06: `query.Events`, `turn_index`, `stats`; A04: `tokenUsage`; this area's `values.Store` | Closed steps pulled by cursor; `#traj-open` and `#traj-totals` state regions | Records come from the log, which is never behind. Totals show the marker with their `AsOfSeq`, or "being rebuilt" in place of numbers. A raw value says `evicted` or `only for a live session` |
| Subagents | A tree in the session panel and a breadcrumb on a child's page; open a member; Stop a running member; Dismiss a settled one | A12: `Runtime.Tree`, the `delegation` fold; live activity from `subagent.Progress` | State region `#tree-{root}` on source `work.tree`, re-drawn on A06's `projection.Changed` for the `delegation` fold (which covers the logged `subagent/started`, `subagent/settled` and `subagent/dismissed`) and on coalesced `subagent.Progress`, the one unlogged signal | `behind`: the tree as of its cut, with the marker. `pending`: "being rebuilt", never "no subagents". Root held by another process: ledger facts only, with "running in another `dsh` process" and no controls |
| Team | The Shared tasks section; the teamwork switch; for the owner: create, reassign, release, complete, reopen, delete | A12: `team.Board`, the `teamBoard` fold | State region `#board-{root}` on source `work.board`, re-drawn on `projection.Changed` for the `teamBoard` fold, so that a mode change (`team/mode`) redraws it as a task change (`team/task`) does; `team.TaskChanged` names one task and is not enough alone | As above; a fold failure is shown above the tasks it still has |
| Workflow runs | A node in the transcript: run, phases, members; a detail with script, limits, endings, narration | A11: the four `workflow/*` events, the `workflow.runs` projection, `workflow.Service.Lookup` | Node updates on `session`; narration is state region `#run-{id}-log` on source `work.narration` | The node is drawn from events. Detail from the projection carries the marker. No live engine: narration says "not available after the run's host stopped" |
| Jobs | The Background jobs section: live rows, a folded "Finished" list, an output panel, Stop | A13: `jobs.Registry` through A19's `job.list`, `job.output`, `job.kill` | A19's interests `jobs` and `job-output` | Jobs live in the host process: after a restart the region shows the `host-exit` settlements from the log and no output. Output below the ring shows the `lossy` notice and the spill link |
| Deliverables | A changed-files card and delivery cards in the turn tail; a review of one turn's changes; download; file links in the closing text | A08: `workspace/changes`, `deliverables/presented`, `deliver.Changes` | Nodes on `session` | Drawn from events, never behind. A missing blob: the card stays, the comparison says "no longer stored". A changed or missing source says so and offers the kept copy |
| Schedules | A page: list with filters and search, the editor with next-run preview, records; pause, resume, run now, delete; clock marks on target sessions | A13: `schedule.Service` (`List`, `Get`, `History`, `Status`) | State region `#schedules` on source `work.schedules`, on `schedule.Changed` (local or foreign) | `schedule/unavailable` is an error panel, never an empty list. No live firing host: a banner. A row that waits says why (`target-held`, retry time). An `invalid` row is listed with its error and has no form |
| Agent presets | A picker beside the composer of a blank session, a read-only chip afterwards; a roster with the default; a viewer of what a preset contains | A27: `agentpreset.Service` (`List`, `Of`, `Select`, `Inventory`), the `agentPreset` fold | The chip is a contribution to area C3's seat `composer.controls` ([ui-chat.md §6.1](ui-chat.md#61-shape)); `agentpreset.Selected` re-draws it | A cold session's chip comes from the fold and carries the marker. A broken declaration is listed, disabled, with its diagnostic |

**Routes.** Registered through `web.Routes` under the rules of [api.md §6.2](api.md#62-contracts), which area A19 owns: resource ids in the path, a declared access rule, a host-minted request id in every state-changing form. `S` is `access.Session("sid")`; `C` is `access.Session("child")`; "owner" is `access.Owner()`. Activation is `None` unless stated. `{rid}`, `{run}`, `{tid}`, `{pid}`, `{id}` are opaque.

| Method and pattern (name) | Returns and rules |
|---|---|
| `GET /sessions/{sid}/trajectory?at=&turn=&record=` (`trajectory.open`) | Page: the tail window, or a window around a turn or record, rendered at one cut, with both cursors. Access `S` |
| `GET /sessions/{sid}/trajectory/rows?before=\|after=&q=&kind=` (`trajectory.rows`) | Fragment: one step-aligned page and the next cursor. `q` is a literal search through `query.Search` scoped to the session; a filter never needs the session in the browser. Access `S` |
| `GET /sessions/{sid}/trajectory/records/{rid}?part=&from=` (`trajectory.record`) | Fragment: the inspector; `part` is `summary`, `input`, `output`, `thinking`, `timing`, `schema`, `raw` or `value`; bodies above `InspectorPageBytes` are paged by `from`. Access `S` |
| `GET /sessions/{sid}/trajectory/overview?from=&to=` (`trajectory.overview`) | Fragment: the strip for a time window. Access `S` |
| `GET /sessions/{sid}/tree?page=` (`tree.read`); `GET /sessions/{sid}/tree/members/{child}` (`tree.member`) | Fragment: the tree of `RootOf(sid)`; one member's runs with stop reason, diagnostic, usage, duration, cleanup. Access `S`, and `S` with `C` |
| `POST /sessions/{sid}/tree/members/{child}/interrupt` (`tree.interrupt`); `.../dismiss` (`tree.dismiss`) | `Runtime.Interrupt` and `Runtime.Dismiss` with the admitted owner as authority. `LiveOnly`; `MayResume`. Owner. A child outside the tree of `{sid}` is 404 |
| `GET /sessions/{sid}/board` (`board.read`); `PUT /sessions/{sid}/team-mode` (`board.mode`) | The section. The switch calls `Board.SetMode`; turning it off after the session recorded a model request needs `confirm=1`, else 409 `team/confirm-needed` with the explanation. Owner; `MayResume` |
| `POST /sessions/{sid}/board/tasks`; `PUT /sessions/{sid}/board/tasks/{tid}` (`board.create`, `board.update`) | `Board.Create` and `Board.Update` with the form's base revision; 409 `team/stale-revision` re-renders the task as it is now. Owner; `MayResume` |
| `GET /sessions/{sid}/runs/{run}` (`run.read`) | Fragment: the run detail. Access `S`. There is no stop route: a foreground run ends with A19's `session.cancel`, a background run with `job.kill` of its job, and the detail shows that one button |
| `GET /sessions/{sid}/changes/{seq}?file=&from=&view=` (`changes.review`) | Page or fragment: the files of one `workspace/changes` event and a page of one file's comparison. Access `S`; `{seq}` must be such an event of that session |
| `GET /sessions/{sid}/deliverables/{seq}/{index}?copy=` (`deliverable.open`) | Download of one presented file: the current file, or the kept copy. Access `S`; the event at `{seq}` must name the file |
| `GET /schedules?status=&q=&target=` (`schedule.list`); `GET /schedules/new`, `GET /schedules/{id}?tab=` (`schedule.new`, `schedule.read`) | Page; editor and records. Owner |
| `GET /schedules/preview` (`schedule.preview`) | Fragment: field messages and the next occurrences for the timing fields in the query. Changes nothing. Owner |
| `POST /schedules`, `PUT /schedules/{id}` (`schedule.create`, `schedule.update`) | `schedule.Service.Create` and `Edit` with the base revision. 422 re-renders the form with a message beside each field; 409 `schedule/stale-revision` as described below. Owner |
| `POST /schedules/{id}/pause`, `/resume`, `/run`; `DELETE /schedules/{id}`; `POST /schedules/clear-ended` | `Edit` and `RunNow`. Delete answers with whether a queued message was retracted. Owner |
| `GET /agent-presets`, `GET /agent-presets/{pid}` (`agent-preset.list`, `agent-preset.read`) | Fragment: the roster; one declaration's children with component, state and the layer that supplied each. Owner |
| `PUT /sessions/{sid}/agent-preset` (`agent-preset.select`); `PUT /agent-presets/default` (`agent-preset.default`) | Names and paths say "agent preset" because area A17's permission presets are a different thing with their own routes ([ui-settings-plugins.md](ui-settings-plugins.md), `permission.set`). `agentpreset.Service.Select` (409 `agentpreset/locked` once the session has a turn; `MayResume`); the live setting `SelectedDefault` through A07. Owner |

**Stream.** This area defines no event name. It registers five `State` sources with `stream.Hub`: `trajectory` (key: session), `work.tree` and `work.board` (key: root session), `work.narration` (key: run), `work.schedules` (no key). Each checks the access rule of its read route at subscribe. Nodes ride the `session` event through C4's renderer.

**The schedule form** ([UIWORK-6](#dh-adr-uiwork-6)). One form serves create and edit. Its timing part is a fieldset: a kind selector (once, every N minutes, every N hours, daily, weekly, monthly, custom), the controls of that kind (date, time, interval, weekdays, or a text box for an RRULE or a cron expression), and a zone. Changing the kind requests the fieldset again (`hx-get`), so only the relevant controls exist. Any change in the fieldset requests `schedule.preview` after `PreviewDebounce`, replacing any earlier preview request, and the answer replaces `#schedule-preview`:

1. **Validity.** The host maps the fields to area A13's timing kinds (`at`, `every`, `daily`, `weekly`, `rrule`, `cron`) with one pure function and asks A13 to check it. A rejection is shown beside the field it names (`syntax`, `time_zone`, `density`, a local time that does not exist for `at`).
2. **Next occurrences.** The next `PreviewOccurrences` instants after the host's clock, each shown in the rule's zone with its UTC offset and, when the viewer's zone differs, in the viewer's zone too. The first line is the later of the first occurrence and the delivery floor, as upstream.
3. **Clock-change notes**, from A13's time rules ([jobs-schedule-goal-plan.md §6.2](jobs-schedule-goal-plan.md#62-contracts)), for the next transition of the zone that the rule meets: for a wall-time rule, "02:30 does not exist on that date: that occurrence is skipped" or "01:30 occurs twice on that date: it fires once, at the earlier time"; for an elapsed-time rule, "this rule counts real time; its local time shifts by one hour after that date". The change in offset between two listed occurrences is visible in the offsets themselves.

Zones are listed by the host from its zone data ([DH-ADR-TEMPORAL-1](../libs/js-temporal-polyfill.md#dh-adr-temporal-1)) as IANA names with the current offset, searchable through a `<datalist>`; the default is the viewer's zone from C1's time-zone cookie, and a stored name is kept as given ([DH-ADR-TEMPORAL-2](../libs/js-temporal-polyfill.md#dh-adr-temporal-2)). The target part offers "this conversation" (when opened from a session), "one new conversation, reused" and "a new conversation each time", with a workspace selector from A07's registry. Typed fields carry `data-draft`, so an unsaved form survives navigation in the same tab (C1's per-tab store). Save posts the whole form with the base revision. On 409 the form comes back holding the posted values, with each field whose stored value changed since the base revision marked and the new revision as its base, and two buttons: save again, or discard. While a delivery to a reused conversation is queued or running, the form is read-only and links to that conversation.

**Deliverable and comparison bytes.** A comparison is drawn from the hunks area A08 stored with the event: rows laid out by A08's `textdiff`, coloured per hunk side by `render.Code`, `DiffPageRows` at a time, unified or split by a host preference. A file A08 lists without content (binary, over the bound, outside `compareScope`) shows that reason. `deliverable.open` opens the path through the filesystem seam once, checks on that handle that it is a regular file, and streams from it with `nosniff` and an attachment disposition; when the digest differs from the recorded one the card says "changed since presented", and when the file is gone it says "source missing"; both offer the kept copy when there is one. Workspace files open in area C6's preview through A19's file routes.

**File links in the closing text.** `work/files` gives C4's renderer a resolver for settled assistant messages: an inline-code token links when it equals a recorded path of the same turn (from `workspace/changes`, whatever changed the file, or from `deliverables/presented`), or is a suffix of exactly one recorded path that starts on a path-segment boundary (a basename is the shortest such suffix). Two matches, or none, leave the token as text.

**Events, config, services, persisted data.**

| Upstream unit | Go replacement |
|---|---|
| `UP-CFG-dsh-client-ui-trajectory` (`maxResultsPerSession`) | `values.Config`: `MaxResultsPerSession` 1000 (at least 1), `MaxValueBytes` 1 MiB* (a larger value is not kept and the inspector says so), `MaxBytesPerSession` 64 MiB* |
| (new) `trajectory.Config` | `PageRecords` 200*; `MaxSubRows` 20; `InspectorPageBytes` 256 KiB*; `OverviewMaxMarks` 2,000*; `SearchMaxHits` 500 |
| (new) `work.Config` | `FoldRows` 5 (rows of a panel section before its fold, as upstream); `TreePage` 50; `ProgressMinInterval` 1 s; `DiffPageRows` 500*; `CardFoldRows` 4; `PreviewOccurrences` 5; `PreviewDebounce` 300 ms; `RecordsPage` 20 |

Numbers marked * are unmeasured starting points. The eight packages have no `UP-EVT`, `UP-SVC` or `UP-TOOL` unit; upstream's `ctx.sessionToolResults` is not in the service inventory and becomes `values.Key`. This area defines no host event and exposes no tool to the model. It registers no projection and appends no session event. Host preferences (C1's `prefs`): `trajectory.fold`, `trajectory.wrap`, `changes.view`, `changes.wrap`, `schedule.zones` (recently used), `panel.fold`. Per-tab: unsaved schedule and task forms, "following the tail". Memory only: live values. One usage event, `agent-preset.switch` with agent preset categories only, declared in C1's closed catalogue and sent nowhere until the user opts in ([DH-ADR-9](../design.md#dh-adr-9)).

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership and access.** A route, a stream source, a node kind, a contribution, a completion source and a preference declaration are effects of the fiber that registered them; unloading a view removes them, and open pages lose the region at the next composition epoch ([ui-core.md §6.1](ui-core.md#61-shape)). The live-value store is owned by the `ui.trajectory.values` fiber and each session's entries also by that agent's lifetime. Everything else shown here is owned elsewhere: a child activation by its tree fiber, a job by its agent's scope, a run by the workflow fiber, a schedule by the harness home, a file by the user. A browser has **access** through its principal: reading needs the session rule, and every control that stops, removes or changes something is owner-only. A view never releases anything; it asks the owner to.

| Situation | What happens | Undone, retried, uncertain |
|---|---|---|
| A view's owner component is not loaded, or unloads | The view component does not load; its routes are 404 and its section and links are absent. A node kind that is gone is drawn by C4's fallback | Nothing to undo; a reload brings both back |
| A read fails (store busy, fold failed, schedule tables unreadable) | The region shows the error with a retry; a list is never drawn empty in its place. 503 `store/busy` carries `Retry-After` | The region re-requests on `reset` |
| Stop, dismiss, kill, pause or delete is posted twice, or retried after a timeout | The request id makes the second a repeat of the first receipt | Nothing happens twice |
| Interrupt reaches a member that already settled | Nothing is running to stop; the answer re-draws the row in its settled state (area A12 documents the same no-op for the model's `interrupt_agent`) | Nothing |
| The root's lease is held by another process | Controls answer 409 `session/writer-held`; the view stays read-only and says which facts are from the log | The user acts in the process that holds it |
| The host dies while work runs | After restart the views draw what the owners append on resume: `subagent/settled{interrupted}`, `job/settled{host-exit}`, `workflow/run-end{interrupted}`, the closed step. Live values and narration are gone | **Uncertain:** whether the work finished; each row says "outcome unknown", as the log does |
| A schedule save meets a newer revision | 409 with the posted values kept ([§6.2](#62-contracts)) | Nothing is written until the user saves again |
| "Run now" is refused (minimum interval, archived target, a delivery pending) | The row shows the reason and, for the interval, when it becomes possible | Nothing was delivered |
| A presented file changed, vanished or became a link | The card says which; the route serves only a regular file through one handle, or the kept copy | Nothing is guessed from a same-named file |
| The stored hunks of an old turn were removed by retention | The card and counts remain; the comparison says "no longer stored" | Not recoverable |
| A record body or comparison is too large | It is paged; a value above `MaxValueBytes` was never kept | Nothing partial is written ([`DH-LIB-GOUISTACK-R13`](../libs/go-ui-stack.md)) |
| The values store reaches a bound | The oldest entries go first; an open inspector shows `evicted` at its next request | By design |

## 7. Decisions

<a id="dh-adr-uiwork-1"></a>
### DH-ADR-UIWORK-1 Every work view is a read model of a named owner: it keeps nothing, draws the owner's own words, and says when it is behind, pending or unavailable

- **Decision.** A view holds no data between requests. It names the service it reads and the operation each control calls ([§6.2](#62-contracts)). A value from a projection is drawn with A06's state: `current` plainly, `behind` with a marker and its cut, `pending` as "being rebuilt". A missing owner means the view is absent; a failed read is an error panel. Hover cards, per-list clocks, timers that arm a button and lists hidden in one browser are replaced by disclosures, one elapsed-time behaviour, confirm forms and lists the owner bounds.
- **Why.** Upstream's views each keep a cache and repair it; five of its documented limits are caches that were not filled or not cleared. A view that cannot hold state cannot disagree with another tab.
- **Tradeoff.** Every disclosure is a request; a panel shows a little less at a glance than a hover card did. No "clear finished" exists: the list is as long as the registry keeps it.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-9](../design.md#dh-o-9). **Verified by.** `DH-T-UIWORK-22`, `DH-T-UIWORK-23`.

<a id="dh-adr-uiwork-2"></a>
### DH-ADR-UIWORK-2 The trajectory is a page of its own, built from the log by a pure fold: closed steps are pulled by cursor, the open step is a state region, pages are step-aligned

- **Decision.** As [§6.1](#61-shape). The trajectory has its own URL beside the conversation ([ui-conversation.md §4](ui-conversation.md)). Records are a pure function of the events up to a cut. Filters, search and folding defaults are computed on the host; the selected record, turn and filter are in the URL. A call that started a subagent links to the child's trajectory and is never expanded in place; the creating tool call is read from the root's `subagent/started` event, which carries it, not from the child's header.
- **Why.** A closed step is immutable, so fetching it by cursor gives "once, in order" with an ordinary request and no second journal renderer; only the open step needs pushing, and it is small. Step alignment removes upstream's "pending context" for calls whose start is on another page.
- **Tradeoff.** A newly closed step appears one request after the region changes. Loaded pages are not released, so a tab that pages through a very long session grows; the page states how many records it holds ([`DH-LIB-REACTVIRTUAL-R5`](../libs/tanstack-react-virtual.md)). One step with very many top-level calls is one large page.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-UIWORK-1` to `-5`, [DH-E-UIWORK-1](#dh-e-uiwork-1).

<a id="dh-adr-uiwork-3"></a>
### DH-ADR-UIWORK-3 The timing overview is server-rendered SVG with links; there is no island and no pointer gesture

- **Decision.** The strip is an inline SVG written by a trusted template: one mark per record placed by SVG geometry attributes, an assistant mark split at its first token, a running record as a start tick. A mark links to its record; turn and step bands link to a window of that turn or step; zoom and pan are buttons that request another window. Above `OverviewMaxMarks` the marks of a step, then of a turn, are merged.
- **Why.** The island rule of [DH-ADR-GOUISTACK-6](../libs/go-ui-stack.md#dh-adr-gouistack-6) allows an island only where a round trip would be wrong; choosing an interval is not such a case, and turns and steps are the intervals a person means.
- **Tradeoff.** No free drag selection, no wheel zoom. SVG geometry is not styling, so the policy that forbids inline style is kept; colours come from classes.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-UIWORK-6`.

<a id="dh-adr-uiwork-4"></a>
### DH-ADR-UIWORK-4 Raw tool values are kept in memory for live sessions only, bounded by count and bytes, and never written anywhere

- **Decision.** Keep upstream's rule and add byte bounds. The store listens to `tools.Settled`, keeps the value of a successful call under its result `seq`, evicts oldest first, and drops a session's entries when its agent is disposed or the component unloads. It is a separate default-on component of the `web` profile; without it the inspector has no "value" part.
- **Why.** The value is useful while debugging and worthless as history; writing it would double the log and bypass the pipeline's bound on what is stored.
- **Tradeoff.** A value is gone after a restart, a fork or an eviction, and the inspector says which. A failed call keeps nothing: its text is in the log.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-4](../design.md#dh-o-4). **Verified by.** `DH-T-UIWORK-8`.

<a id="dh-adr-uiwork-5"></a>
### DH-ADR-UIWORK-5 Nested work is one host-rendered tree from the ledger, with the owner's full outcome words, and a person stops work only through the owner's operation

- **Decision.** The tree is drawn in one bounded read of A12's `Tree`. Each member shows its latest run's state from the closed set A12 records (`completed`, `aborted`, `error`, `max-tokens`, `refusal`, `start-failed`, `interrupted`), or `running`, `inactive`, `missing`. Stop is `Runtime.Interrupt` for a member, `jobs.Registry.Kill` (A19's route) for a job, a turn cancel or a job kill for a workflow run. An `@` reference inserts the member's path, which is unique in the tree and is what the model's `send_message` accepts.
- **Why.** One tree answers "what is running under this conversation" without the reader knowing which mechanism started it. Calling the owner's operation keeps its guarantees: a result settles after cleanup, and the model is told a person stopped its work.
- **Tradeoff.** A stop is a request: the row shows `stopping` until the owner settles. **Team view, register entry U-K2-6 (recommended default, not confirmed by the owner).** A read-only message timeline is built ([DH-D-UIWORK-5](#dh-d-uiwork-5)): what the agents of one tree told each other, in time order, without opening each member's conversation. Starting and renaming a member from the view are scope, not offered: a start needs a creator agent and the delegation bounds of area A12, and its ledger has no rename, so both stay the model's. A member's write scope is labelled "advisory, not enforced"; a member that must not touch its siblings' files is started with `isolation: worktree` ([DH-ADR-40](../design.md#dh-adr-40), [x-forge-worktree.md](x-forge-worktree.md)), and its row shows its branch.
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-UIWORK-9` to `-11`, `-13`, `-14`, [DH-E-UIWORK-2](#dh-e-uiwork-2).

<a id="dh-adr-uiwork-6"></a>
### DH-ADR-UIWORK-6 A schedule is edited in a plain form that the host validates and previews with the evaluator that will fire it

- **Decision.** As "The schedule form" in [§6.2](#62-contracts). No recurrence or time-zone code runs in the browser. The preview and the stored committed occurrence come from one code path in area A13. A save is compare-and-set on the row's revision.
- **Why.** The day a schedule goes wrong is the day the clock changes; a preview from a second evaluator would be wrong on exactly that day. A form with a fragment answer per change is what htmx is for.
- **Tradeoff.** Feedback takes a request (debounced), which is felt over a remote link. Zones are shown by IANA name, not by a localised display name, because the host has no such table. Recently used zones are a host preference.
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-UIWORK-18` to `-20`, [DH-E-UIWORK-3](#dh-e-uiwork-3).

<a id="dh-adr-uiwork-7"></a>
### DH-ADR-UIWORK-7 Deliverables are drawn from logged facts and stored hunks; files are previewed and downloaded, not opened on the host; a link is made only for a recorded path

- **Decision.** Cards are transcript nodes of `workspace/changes` and `deliverables/presented`, drawn when the event commits. The review reads `deliver.Changes`. Bytes leave through one download route that requires the logged event to name the file. The mention resolver adds the unique segment-boundary suffix to upstream's exact and basename rules. Opening a directory in a host application stays area A18's optional `host.openpath`.
- **Why.** Cards survive a restart and work for a cold session because nothing is read from memory. A route that serves "whatever path the page names" would be a file-read primitive; naming the event instead makes the log the allow-list.
- **Tradeoff.** No "open with" list. Colour is computed per hunk, so a line inside a long comment or string that starts above the hunk can be coloured wrongly ([§5](#5-tech-debt-and-its-resolution), group F).
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7), [DH-O-5](../design.md#dh-o-5). **Verified by.** `DH-T-UIWORK-15` to `-17`, [DH-E-UIWORK-4](#dh-e-uiwork-4).

<a id="dh-adr-uiwork-8"></a>
### DH-ADR-UIWORK-8 An agent preset is chosen for a blank session and inspected, not edited, in this view

- **Decision.** The picker lists `agentpreset.Service.List()` in roster order and calls `Select`; once the session has a turn the chip is read-only and shows the recorded id and digest. The viewer lists a declaration's children with component, state and the layer that supplied each field. There is no editor here: a child is an ordinary layer entry, changed through the settings forms of area A07 or a layer file, by nested operations ([bundle-preset.md](bundle-preset.md), `DH-ADR-BUNDLE-4`). The roster is not filtered by any other setting.
- **Why.** Authoring a preset is editing composition, which already has one owner and one validation path; a second editor would be a second way to write layers. Showing provenance answers "why does this session have this tool".
- **Tradeoff.** Creating a preset needs a layer file or the `creator` preset. `UP-DEBT-limit.packages.client.ui-agent-preset.1` is carried in that form.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-2](../design.md#dh-o-2). **Verified by.** `DH-T-UIWORK-21`, [DH-E-UIWORK-5](#dh-e-uiwork-5).

## 8. Deliverables

<a id="dh-d-uiwork-1"></a>
### DH-D-UIWORK-1 A person can read, page, search and link into the step-by-step record of a session, live

- **Result and why it matters.** For any session, live or cold, a page shows every prompt, context change, model request, tool call and nested call with its state, timing and token use, and follows a running turn. It is how a user finds why a turn was slow, costly or wrong.
- **How it is implemented.** `trajectory.Builder` as [§6.1](#61-shape): a fold over step-aligned event ranges from `query.Events`, golden-tested against fixture logs; the page, rows and record routes; `#traj-more` and the `trajectory` source; inspector bodies through C4's `render` (Markdown, code, terminal output) with paging; a tool-change record that lists each added and removed tool with its schema as of that request; folding as `<details>` with defaults from a host preference; a link in `shell.header` between conversation and trajectory. A record of an assistant message carries area C3's rating control, the same partial the conversation's turn footer shows, so a person can rate or retract from the trajectory too; each bar of the timing overview links to its record, where the control is (upstream's trajectory and waterfall views had none, `UP-DEBT-limit.packages.client.ui-message-feedback.3`).
- **Components and files.** `internal/ui/work/trajectory`, `internal/ui/work`.
- **Depends on.** [DH-ADR-REACTVIRTUAL-1](../libs/tanstack-react-virtual.md#dh-adr-reactvirtual-1), [DH-ADR-GOUISTACK-4](../libs/go-ui-stack.md#dh-adr-gouistack-4). [DH-D-API-1](api.md#dh-d-api-1), [DH-D-API-3](api.md#dh-d-api-3) and [DH-D-API-4](api.md#dh-d-api-4) (routes, access, the hub), [DH-D-UICORE-1](ui-core.md#dh-d-uicore-1) and [DH-D-UICORE-3](ui-core.md#dh-d-uicore-3) (the view contract, the `live` helper, the `tick` module for elapsed time, the `dsh:swapped` event that triggers `#traj-more`, following the tail), [DH-D-UICONV-2](ui-conversation.md#dh-d-uiconv-2) and [DH-D-UICONV-4](ui-conversation.md#dh-d-uiconv-4) (`render`, `toolcard` presentation), [DH-D-VIEW-3](session-views.md#dh-d-view-3), [DH-D-VIEW-5](session-views.md#dh-d-view-5) and [DH-D-VIEW-6](session-views.md#dh-d-view-6) (`turn_index`, `query.Events`, session-scoped literal search), [DH-D-AGENT-3](agent-loop.md#dh-d-agent-3) and [DH-D-LLM-6](llm.md#dh-d-llm-6) (start, first-token and end times and usage as plain fields), [DH-D-UICHAT-8](ui-chat.md#dh-d-uichat-8) (the rating control).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Priority.** P1. **Done when.** DH-T-UIWORK-1, DH-T-UIWORK-2, DH-T-UIWORK-3, DH-T-UIWORK-4, DH-T-UIWORK-5, DH-T-UIWORK-22 and DH-T-UIWORK-23 pass and [DH-E-UIWORK-1](#dh-e-uiwork-1) has passed.

<a id="dh-d-uiwork-2"></a>
### DH-D-UIWORK-2 The time and token cost of a session is visible at a glance

- **Result and why it matters.** A strip shows where each turn's time went and a totals line shows requests and tokens, so cost questions need no arithmetic.
- **How it is implemented.** `Builder.Overview` and the SVG template of [UIWORK-3](#dh-adr-uiwork-3); the `#traj-totals` region from `tokenUsage` and `stats` snapshots read `Stored`, with the freshness marker.
- **Components and files.** `internal/ui/work/trajectory`. **Depends on.** [DH-D-UIWORK-1](#dh-d-uiwork-1). [DH-D-LLM-6](llm.md#dh-d-llm-6) (the `tokenUsage` view), [DH-D-VIEW-3](session-views.md#dh-d-view-3) (the `stats` view).
- **Outcome.** [DH-O-12](../design.md#dh-o-12). **Priority.** P2. **Done when.** DH-T-UIWORK-6 and DH-T-UIWORK-7 pass.

<a id="dh-d-uiwork-3"></a>
### DH-D-UIWORK-3 The typed value a tool returned can be inspected while its session is live

- **Result and why it matters.** A tool author sees the value before shaping, without the log growing.
- **How it is implemented.** The `values` component of [UIWORK-4](#dh-adr-uiwork-4): a `tools.Settled` listener, a per-session ordered map with three bounds, cleanup effects; the `value` part of the record route renders the JSON as a tree through `render.Code`, or one of `evicted`, `not kept (too large)`, `only for a live session`.
- **Components and files.** `internal/ui/work/trajectory/values`. **Depends on.** [DH-D-UIWORK-1](#dh-d-uiwork-1). [DH-D-TOOLS-2](tools.md#dh-d-tools-2) (`tools.Settled` carrying the outcome's value and the result `seq`), [DH-D-AGENT-6](agent-loop.md#dh-d-agent-6) (a disposal notice per agent).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Priority.** P2. **Done when.** DH-T-UIWORK-8 passes.

<a id="dh-d-uiwork-4"></a>
### DH-D-UIWORK-4 A person sees the tree of subagents with how each run ended, opens any member, and stops or dismisses one

- **Result and why it matters.** Delegated work is visible as one tree and is stoppable without typing a command; a failed child no longer looks like an idle one.
- **How it is implemented.** The `tree` view over `Runtime.Tree`; the `work.tree` source; the member fragment; the two owner routes; a breadcrumb contribution to `shell.header` built from `query.Lineage` and names; the `@` source registered with C3's `complete.Registry`, listing resident members by path; a count of running members for the panel indicator.
- **Components and files.** `internal/ui/work/tree`.
- **Depends on.** [DH-D-SUBAGENT-2](subagent-team.md#dh-d-subagent-2) (the `delegation` fold and the three logged events it reads), [DH-D-SUBAGENT-5](subagent-team.md#dh-d-subagent-5) (`Tree` with stop reason, usage and duration per run; `Interrupt` and `Dismiss` with an owner principal; `subagent.Progress`), [DH-D-VIEW-2](session-views.md#dh-d-view-2) (`projection.Changed`), [DH-D-API-5](api.md#dh-d-api-5) (`session.prompt` and `session.cancel` on a child session route through `subagent.Runtime.Prompt` and `Interrupt`), [DH-D-UISIDE-9](ui-sidebar-workspace.md#dh-d-uiside-9) (the regions `session.panel.section` and `session.panel.indicator`), [DH-D-UICHAT-3](ui-chat.md#dh-d-uichat-3) (the completion registry), [DH-D-UICONV-1](ui-conversation.md#dh-d-uiconv-1) (a session page that seats a read-only composer for a child that cannot be continued).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-9](../design.md#dh-o-9). **Priority.** P1. **Done when.** DH-T-UIWORK-9, DH-T-UIWORK-10, DH-T-UIWORK-11, DH-T-UIWORK-22 and DH-T-UIWORK-23 pass and [DH-E-UIWORK-2](#dh-e-uiwork-2) has passed.

<a id="dh-d-uiwork-5"></a>
### DH-D-UIWORK-5 A team's task board is visible beside the conversation, and its owner can switch teamwork and unblock a task

- **Result and why it matters.** A person follows how agents divided the work, and can reassign or remove a task that a failed member left behind.
- **How it is implemented.** The board view ordered as upstream (in progress, pending, completed), with the blocked marker, the owner's path and write scopes labelled "advisory"; the `work.board` source; the mode switch with its confirmation; create and update forms carrying the base revision. The message timeline (`GET /sessions/{sid}/team/messages?before=`, name `team.messages`, a fragment bound to the root session, paged newest first) lists every message with source `agent-message` in the sessions of the tree: time, sender, recipient, the text printed through `utext`, and a link to the message in the recipient's conversation. It reads area A06's bounded query `Query.TreeMessages` ([session-views.md §6.2](session-views.md#62-contracts)), which returns the messages between agents of one tree in time order within the caller's visibility. It holds nothing between requests and is not live: a "newer" control fetches again. A member whose log cannot be read is named in a line of its own, never dropped silently.
- **Components and files.** `internal/ui/work/board`. **Depends on.** [DH-D-UIWORK-4](#dh-d-uiwork-4). [DH-D-SUBAGENT-9](subagent-team.md#dh-d-subagent-9) (`team.Board` with an owner principal as actor, the `teamBoard` fold, and "has this session recorded a model request"), [DH-D-UISIDE-9](ui-sidebar-workspace.md#dh-d-uiside-9) (the panel region), [DH-D-SUBAGENT-5](subagent-team.md#dh-d-subagent-5) (messages between agents, logged with source `agent-message`), [DH-D-VIEW-5](session-views.md#dh-d-view-5) (`Query.TreeMessages`).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-9](../design.md#dh-o-9). **Priority.** P1: the board, the switch, the task forms and the message timeline together. **Done when.** DH-T-UIWORK-12, DH-T-UIWORK-22, DH-T-UIWORK-23 and DH-T-UIWORK-24 pass.

<a id="dh-d-uiwork-6"></a>
### DH-D-UIWORK-6 A workflow run is a node in the conversation with its members, its ending and its detail

- **Result and why it matters.** One model step that drives many children is readable as one unit, during and after the run, and after a crash it says `interrupted` and not "running".
- **How it is implemented.** A `transcript.NodeDef` of kind `workflow-run` over the four `workflow/*` events (the start is `workflow/run-start`); disclosure defaults computed from logged facts (open while running, failed, cancelled, timed out or interrupted); member rows linking to the child session; the detail fragment from the `workflow.runs` projection, with the script taken from the hosting call's logged arguments and shown through `render.Code`; the narration region from `workflow.Narrated`, bounded by the engine's ring.
- **Components and files.** `internal/ui/work/run`. **Depends on.** [DH-D-UIWORK-4](#dh-d-uiwork-4). [DH-D-UICONV-1](ui-conversation.md#dh-d-uiconv-1) (node registration by another area, placed in `conversation.turn-tail`), [DH-D-PTC-3](ptc-workflow.md#dh-d-ptc-3) (the projection, `Lookup`, the narration ring readable by an observer), [DH-D-PTC-6](ptc-workflow.md#dh-d-ptc-6) (the job id of a background run).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-9](../design.md#dh-o-9), [DH-O-5](../design.md#dh-o-5). **Priority.** P1. **Done when.** DH-T-UIWORK-13, DH-T-UIWORK-22 and DH-T-UIWORK-23 pass and [DH-E-UIWORK-2](#dh-e-uiwork-2) has passed.

<a id="dh-d-uiwork-7"></a>
### DH-D-UIWORK-7 Background jobs are listed with their output, and a person can stop one

- **Result and why it matters.** A long command is watched and stopped from the panel, and the model still learns that a person stopped it.
- **How it is implemented.** The views area A19's job routes and sources render: live rows first, a folded "Finished" group, the output panel with chunks tinted by channel and the `lossy` and spill notices, the confirm form posting `job.kill`. The section is a contribution to the session panel and is absent while the session can see no job.
- **Components and files.** `internal/ui/work/jobs`. **Depends on.** [DH-D-API-7](api.md#dh-d-api-7) (`job.list`, `job.output`, `job.kill` and their two interests), [DH-D-JOBS-1](jobs-schedule-goal-plan.md#dh-d-jobs-1) (the registry's visible set and channel per chunk), [DH-D-UISIDE-9](ui-sidebar-workspace.md#dh-d-uiside-9) (the panel region).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-9](../design.md#dh-o-9). **Priority.** P1. **Done when.** DH-T-UIWORK-14 and DH-T-UIWORK-22 pass and [DH-E-UIWORK-2](#dh-e-uiwork-2) has passed.

<a id="dh-d-uiwork-8"></a>
### DH-D-UIWORK-8 The files a turn changed or presented are listed, compared, previewed and downloadable, also after a restart

- **Result and why it matters.** The result of a turn is a list of files a person can review and take, not a paragraph to trust.
- **How it is implemented.** Node kinds `changes` and `presented` in the turn tail; the review route over `deliver.Changes` with paged hunks; the download route of [§6.2](#62-contracts); the mention resolver handed to C4's renderer; cards labelled with A08's `tool` or `other` source and its coverage note.
- **Components and files.** `internal/ui/work/files`. **Depends on.** [DH-ADR-DIFF-1](../libs/diff.md#dh-adr-diff-1). [DH-D-FS-9](filesystem.md#dh-d-fs-9) and [DH-D-FS-10](filesystem.md#dh-d-fs-10) (the two events, `deliver.Changes`, the digest and copy of `present`), [DH-D-STORAGE-3](storage.md#dh-d-storage-3) (reading a blob by reference), [DH-D-UICONV-1](ui-conversation.md#dh-d-uiconv-1) and [DH-D-UICONV-2](ui-conversation.md#dh-d-uiconv-2) (node registration, `render.Code`, a link resolver for settled assistant messages), [DH-D-UISIDE-6](ui-sidebar-workspace.md#dh-d-uiside-6) (the file preview for workspace paths).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7), [DH-O-5](../design.md#dh-o-5). **Priority.** P1. **Done when.** DH-T-UIWORK-15, DH-T-UIWORK-16, DH-T-UIWORK-17 and DH-T-UIWORK-22 pass and [DH-E-UIWORK-4](#dh-e-uiwork-4) has passed.

<a id="dh-d-uiwork-9"></a>
### DH-D-UIWORK-9 A person creates, edits, pauses, runs and deletes schedules, and sees when each will next fire and what each delivery did

- **Result and why it matters.** Recurring work is managed without the model, and the form says what a rule means on the days the clock changes.
- **How it is implemented.** The page, list and editor of [UIWORK-6](#dh-adr-uiwork-6); the preview route; records paged by keyset with each delivery's state and a link to the turn that ran it; the `work.schedules` source; a clock mark on a target session's header and row; the `schedule_create` card body, fetched lazily so it shows the schedule as it is now; a `shell.nav` entry. Recommended starting tasks are catalogue data and open a new conversation with a draft.
- **Components and files.** `internal/ui/work/schedules`.
- **Depends on.** [DH-ADR-RRULE-1](../libs/rrule.md#dh-adr-rrule-1), [DH-ADR-TEMPORAL-2](../libs/js-temporal-polyfill.md#dh-adr-temporal-2). [DH-D-JOBS-4](jobs-schedule-goal-plan.md#dh-d-jobs-4) (`schedule.Service` and a preview call, [§10](#10-open-questions-and-limitations)), [DH-D-JOBS-5](jobs-schedule-goal-plan.md#dh-d-jobs-5) (the delivery's turn), [DH-D-UTIL-11](util-test-support.md#dh-d-util-11) (the cron and RRULE evaluators and the zone data), [DH-D-STORAGE-7](storage.md#dh-d-storage-7) (the workspace registry), [DH-D-API-5](api.md#dh-d-api-5) and [DH-D-UICONV-8](ui-conversation.md#dh-d-uiconv-8) (a window around a `seq`), [DH-D-UISIDE-3](ui-sidebar-workspace.md#dh-d-uiside-3) (the row region `nav.session.mark`).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-9](../design.md#dh-o-9). **Priority.** P1. **Done when.** DH-T-UIWORK-18, DH-T-UIWORK-19, DH-T-UIWORK-20 and DH-T-UIWORK-22 pass and [DH-E-UIWORK-3](#dh-e-uiwork-3) has passed.

<a id="dh-d-uiwork-10"></a>
### DH-D-UIWORK-10 A person chooses the agent preset of a new session and can see what any agent preset contains

- **Result and why it matters.** One host runs agents with different tools; the user picks which, and can check what a session was composed from.
- **How it is implemented.** As [UIWORK-8](#dh-adr-uiwork-8): the picker and chip, the roster with the default, the viewer from `Inventory()`, the default as a settings write, a "Create with the agent" link that starts a session with the `creator` preset where it is declared.
- **Components and files.** `internal/ui/work/presets`. **Depends on.** [DH-D-BUNDLE-4](bundle-preset.md#dh-d-bundle-4) (`agentpreset.Service`, the `agentPreset` fold, provenance per child in `Inventory()`), [DH-D-STORAGE-8](storage.md#dh-d-storage-8) (the settings write), [DH-D-UICHAT-1](ui-chat.md#dh-d-uichat-1) (the seat `composer.controls`).
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-2](../design.md#dh-o-2). **Priority.** P1. **Done when.** DH-T-UIWORK-21 and DH-T-UIWORK-22 pass and [DH-E-UIWORK-5](#dh-e-uiwork-5) has passed.

## 9. Acceptance

### 9.1 Local tests

Fakes at the service boundary and fixture logs; none proves a real browser, model or process.

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-UIWORK-1` | D-1 | Golden records for fixture logs: every kind, step grouping, request numbers, failed attempts on their request; an assistant-message record holds C3's rating control with the message's current rating, and a rating posted from it is the same `feedback` event the turn footer posts; a page at any cut plus the rows after it equals the page at the later cut |
| `DH-T-UIWORK-2` | D-1 | Walking `before` to the start and `after` to the head yields every closed step exactly once, with events committed between two requests; two concurrent pulls from one cursor add each step once |
| `DH-T-UIWORK-3` | D-1 | The open-step region shows a start and no duration for a running request and call; it is empty with no step open; after resume closers its rows are `interrupted` or `unknown` |
| `DH-T-UIWORK-4` | D-1 | Inspector bodies are escaped and pass `render`; a large body is paged without gap or overlap; a tool-change record lists each tool; `?record=` returns a window that contains the record |
| `DH-T-UIWORK-5` | D-1 | Search and kind filters return only matching records, folded nested calls included, with a correct next cursor |
| `DH-T-UIWORK-6` | D-2 | Marks follow recorded start and duration; a running record is a tick; merging above the bound keeps total time; the SVG has no `style` attribute and no script |
| `DH-T-UIWORK-7` | D-2 | Totals equal the fold values; `behind` and `pending` snapshots draw the marker and the placeholder |
| `DH-T-UIWORK-8` | D-3 | Count and byte bounds evict oldest first; a failed call keeps nothing; entries go when the agent is disposed or the component unloads; a cold session answers `not-live`; no event is appended |
| `DH-T-UIWORK-9` | D-4 | From a fixture ledger: nesting by creator, order, a distinct label for each stop reason, external leaves, a `missing` child, dismissed members hidden, the page bound |
| `DH-T-UIWORK-10` | D-4 | Interrupt and dismiss: refused for a non-owner; a child outside the tree is 404; 409 with no live root or a lease held elsewhere; a repeated request id acts once |
| `DH-T-UIWORK-11` | D-4 | The `@` source lists resident members; two members with one label get different paths; the inserted path resolves to the child |
| `DH-T-UIWORK-12` | D-5 | Board order, blocked marker, owner fallback; turning teamwork off after a model request needs the confirmation; a stale revision returns 409 with the current task |
| `DH-T-UIWORK-13` | D-6 | The run node at every cut of a fixture; disclosure defaults; a run with no end shows `interrupted` after closers; the script is shown escaped; narration is bounded |
| `DH-T-UIWORK-14` | D-7 | Live rows first; chunks carry their channel class; the `lossy` notice; the confirm form posts the kill with a request id; no section when no job is visible |
| `DH-T-UIWORK-15` | D-8 | Cards from events: counts, binary and too-large rows, `tool` and `other` labels, the coverage note; hunk pages cover every row once in both layouts; a counts-only file shows no lines |
| `DH-T-UIWORK-16` | D-8 | The download serves only a file the event at that `seq` names; a directory, a symbolic link and a missing file are refused; a digest mismatch is reported; another session's principal is refused |
| `DH-T-UIWORK-17` | D-8 | Exact path, basename and unique segment suffix link; an ambiguous or unrecorded token stays text; a suffix that splits a path segment does not match |
| `DH-T-UIWORK-18` | D-9 | Each guided kind maps to the stated timing kind; invalid input returns 422 with a message per field and the values kept; a stale revision returns 409 with the posted values and the changed fields marked |
| `DH-T-UIWORK-19` | D-9 | With a fixed clock: for a wall-time rule across a gap and an overlap in `America/New_York` the preview lists the instants and notes of A13's table; an elapsed-time rule shows the shifted local time; the first preview line equals the `next_at` that `Create` stores |
| `DH-T-UIWORK-20` | D-9 | Filters, search and record pages; the five delivery states drawn distinctly; `schedule/unavailable` draws an error; no firing host draws the banner; an `invalid` row has no form |
| `DH-T-UIWORK-21` | D-10 | Roster order; a broken declaration disabled with its diagnostic; `Select` on a blank session; 409 once it has a turn; the viewer shows the supplying layer and never a secret value |
| `DH-T-UIWORK-22` | D-1, D-4 to D-10 | Every route of [§6.2](#62-contracts) is registered with an access rule and refuses a principal without a binding; every view renders a golden file in English and Chinese; a composition without the owner has no route and no section |
| `DH-T-UIWORK-24` | D-5 | With fixture logs of a root and three members: the timeline lists exactly the messages of source `agent-message`, across sessions, in time order, each with sender, recipient and a link by `seq`; text passes through `utext`; a page boundary loses and repeats nothing; an unreadable member is named; the view offers no start and no rename control, and a write scope carries the "not enforced" label |
| `DH-T-UIWORK-23` | D-1, D-4, D-5, D-6 | A `behind` value is drawn with its cut; a `pending` value is a placeholder, never a zero or an empty list; another process holding the lease removes the controls |

### 9.2 Real-environment scenarios

Each uses the built `dsh` binary in profile `web`, a fresh harness home and a real browser driven by the scenario. Local tests above are separate evidence.

<a id="dh-e-uiwork-1"></a>
#### DH-E-UIWORK-1 The trajectory of a real turn is complete, live and linkable

- **Proves.** DH-D-UIWORK-1.
- **Given.** A real model provider with a key supplied at run time; a workspace with a few files; one browser tab on the session's trajectory page and a second on its conversation.
- **When.** Send a prompt that makes the model read two files and run one command. While it runs, cut the first tab's network for ten seconds and restore it. After the turn, copy a record's link into a new tab, then load earlier pages to the start.
- **Then.** During the turn the open step shows running calls with a start and no duration. After the cut the page holds every step once, in order. At the end the records match the exported log one to one (requests, calls, results, token counts). The copied link opens on that record. Paging to the start shows every step once.
- **Cleanup.** Close the tabs, stop `dsh`, delete the home and workspace. On failure keep the exported log and the page HTML.

<a id="dh-e-uiwork-2"></a>
#### DH-E-UIWORK-2 A person watches and stops a subagent, a background job and a workflow run

- **Proves.** DH-D-UIWORK-4, DH-D-UIWORK-6, DH-D-UIWORK-7.
- **Given.** A real provider; the `standard` preset; `bash` or PowerShell on the machine; a session open in two tabs.
- **When.** Ask the model to (a) start a subagent on a long task, (b) run a long command in the background, (c) start a background workflow with two members. From the first tab stop the subagent, then the job, then the workflow run. Then kill `dsh` during a fourth background job and start it again.
- **Then.** Each item appears in both tabs without a reload. After each stop the row leaves `running` and then shows the state the owner recorded (the subagent's turn cancelled, the job `killed`, the run `cancelled`), the model's next turn mentions the stop, and no child process of the stopped work remains. After the restart the fourth job shows `failed` with `host-exit` and "outcome unknown", and nothing shows "running".
- **Cleanup.** Stop `dsh`; check for and end leftover processes by the ids in the log; delete the home. On failure keep the log and the process list.

<a id="dh-e-uiwork-3"></a>
#### DH-E-UIWORK-3 The schedule form previews, saves, fires and records

- **Proves.** DH-D-UIWORK-9.
- **Given.** `dsh web` as the only firing host; a real provider; the machine's real zone data; a second browser profile on the same host.
- **When.** Open "new schedule"; type an invalid cron expression, then "daily 02:30" in a zone whose next clock change is a gap, then a rule two minutes ahead with target "one new conversation". Save. In the second browser pause and resume it, then save a stale edit from the first. Wait for it to fire; use "Run now" twice in a row.
- **Then.** The invalid expression shows a message at its field and no save happens. The preview for the 02:30 rule names the skipped date, which matches the zone's published transition. The stale save returns the form with the typed values and the changed field marked. The schedule fires once; its record shows `ran` and links to the turn; the second "Run now" is refused with the time it becomes possible.
- **Cleanup.** Delete the schedule and the created session; stop `dsh`; delete the home. On failure keep the `schedules` and `schedule_deliveries` rows.

<a id="dh-e-uiwork-4"></a>
#### DH-E-UIWORK-4 Deliverables survive a restart and say when a file changed

- **Proves.** DH-D-UIWORK-8.
- **Given.** A real Git repository as workspace; a real provider; `git` installed.
- **When.** Ask the model to edit two files, create one with a shell command and present one. Open the review, switch layouts, download the presented file. Edit the presented file outside the harness, then delete it. Restart `dsh` and reopen the session.
- **Then.** The card lists three files with the right counts and labels the shell-created one `other`; the closing text links the mentioned files. The download equals the file on disk. After the edit the card says "changed since presented" and offers the copy, whose bytes equal the presented version; after the delete it says "source missing". After the restart the cards and comparisons are unchanged. The repository's own index and objects are unmodified.
- **Cleanup.** Stop `dsh`; delete the workspace and home. On failure keep the two events and the blob references.

<a id="dh-e-uiwork-5"></a>
#### DH-E-UIWORK-5 A preset is chosen for a blank session and locked by its first turn

- **Proves.** DH-D-UIWORK-10.
- **Given.** A real provider; the shipped presets `standard` and `minimal`.
- **When.** Create a session, pick `minimal`, open its viewer, send a prompt, then try to pick `standard` (also by posting the form directly).
- **Then.** The viewer lists `minimal`'s children with their layer. The first request's logged tool schemas are those of `minimal`. The chip is read-only afterwards and the direct post answers 409 `agentpreset/locked`; the log holds one `agent-preset/selected` with cause `select`.
- **Cleanup.** Stop `dsh`; delete the home.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Where this area's UI sits, settled.** Panel sections and their running marks go to C6's `session.panel.section` and `session.panel.indicator`; the schedule mark to `nav.session.mark`; the agent preset chip to C3's `composer.controls`; end-of-turn cards are nodes with `Place: TurnTail` in C4's registry. Elapsed time is C1's `tick` module and `#traj-more` is triggered by C1's `dsh:swapped` event. "Preset" in this document always means the agent preset of [bundle-preset.md](bundle-preset.md); the permission preset of area A17 is drawn by [ui-settings-plugins.md](ui-settings-plugins.md).
2. **Session events used here, checked against [subagent-team.md](subagent-team.md).** Logged: `subagent/started`, `subagent/settled`, `subagent/dismissed`, `team/task`, `team/mode`, all registered there and read through its two folds. Live: `subagent.Progress` only. Settled there: `Tree` rows carry stop, usage and duration, and the creating tool call is in `subagent/started`. **Open, owner [subagent-team.md](subagent-team.md):** the code `team/confirm-needed` (or a `confirmed` argument on `Board.SetMode`), a way to ask "has this session recorded a model request", and the absent-target no-op stated for `Runtime.Interrupt` as it is for the tool. Until then the route decides the confirmation itself from A06's `stats` view (a session with a `step/end` has made a request) and treats an interrupt of an absent target as success.
3. **[jobs-schedule-goal-plan.md](jobs-schedule-goal-plan.md): `Preview` settled, the rest open (not answered in this pass).** Open, owner that document: whether a person's `CreateRequest` may carry any of the seven timing kinds (assumed yes; the model tool takes an `rrule` only); the delivery row's turn start `seq` (until then the link opens the target session at its tail); `List` filtered by target (until then this area filters the full list); the reason a row waits (until then the row says "waiting" with no reason). Settled: `schedule.Service.Preview(timing, now, n)`: a pure call that returns a rejection or the next `n` occurrences with gap, overlap and elapsed-time annotations, through the code that `Create` uses. Without it this area would call the evaluator of [../libs/rrule.md](../libs/rrule.md) itself, which is the second evaluator [UIWORK-6](#dh-adr-uiwork-6) forbids. Also: the delivery row's turn start `seq`; `List` filtered by target; the reason a row waits.
4. **Assumed of [api.md](api.md) (request sent).** Prompt and cancel on a child session routed through `subagent.Runtime`; `around={seq}` on the history route (also asked by C4); the job id of a background workflow run on `job.list` rows. If A19 later offers named journal renderers per view, the trajectory could use one; this design does not need it.
5. **Assumed of other areas (requests sent).** A11: observer access to the narration ring and the `workflow.runs` projection; the hosting call id on the run. A08: `Diff` returning hunks in pages; the kept copy readable by reference. A27: the supplying layer per child in `Inventory()`. **Open, owner [session-views.md](session-views.md) (not answered in this pass):** literal search scoped to one session returning `seq`s; until then trajectory search filters the records of the loaded pages on the host. Settled by [tools.md](tools.md): `tools.Settled` carries the typed value and the result `seq`.
6. **Unmeasured or unverified.** `hx-sync` semantics in a real browser. Every number marked * in [§6.2](#62-contracts); the cost of re-rendering the open step on each change; the size of an overview SVG; the first-view budget of 650 ms that [app-web.md](app-web.md) carries over for the trajectory.
7. **Limits kept.** [api.md](api.md) gives a session one journal renderer, so the trajectory cannot have journal fragments of its own and pulls closed steps by cursor. The five carried units of [§5](#5-tech-debt-and-its-resolution). Loaded trajectory pages are never released. No pointer gestures on the overview. No localised zone names. Jobs, narration and live values do not survive a host restart. A work view of a session held by another process is read-only. The evidence planned here proves the stated contracts for the tested build and environments, not every browser.

## 11. Units dispositioned by this spec

Ledger fragment: `trace/fragments/C5.jsonl`.

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 8 | 8 replaced |
| `UP-DEBT` | 27 | 22 resolved, 5 carried |
| `UP-CFG` | 1 | 1 ported |

Go items declared: `DH-ADR-UIWORK-1` to `-8`, `DH-D-UIWORK-1` to `-10`, `DH-T-UIWORK-1` to `-24`, `DH-E-UIWORK-1` to `-5`.
