# Background jobs, schedules, goals and plan mode: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md) and are cited as `path:line` relative to the upstream root.
> Statements that are reasoning and not read from source open with "Inference". Sections 6
> to 9 are design throughout. Nothing here has been built, run or tested. IDs in this
> document use the area key `JOBS`.

| | |
|---|---|
| **Area key** | `JOBS` (agent `A13`) |
| **Upstream packages** | 10 in four groups: `jobs` (3), `schedule` (2), `goal` (4), `plan` (1) |
| **Serves outcomes** | [DH-O-9](../design.md#dh-o-9) (with A11, A12, X5), [DH-O-3](../design.md#dh-o-3), [DH-O-4](../design.md#dh-o-4), [DH-O-5](../design.md#dh-o-5), [DH-O-6](../design.md#dh-o-6), [DH-O-7](../design.md#dh-o-7), [DH-O-14](../design.md#dh-o-14) |
| **Depends on areas** | A02 (inbox, turn budget, the three interception points), A01 (event types, projections), A03 (tool pipeline, guards, prompt sections), A05 (database, leases, live-instance query), A07 (revision counters, activity providers, spill), A09 (process producers), A10 (sandbox mode ceiling), A12 (delegation depth, subagent tree), A17 (interactions, commands), A23 (cron and recurrence evaluators), A19, A20, A24, C4, C5 (routes, protocol methods, views) |
| **Priority** | P1: a first working harness answers a prompt without any of the four; parity on the main paths needs all of them |

## 1. Purpose

This area holds the four ways in which work outlives one step or one turn. Outcome [DH-O-9](../design.md#dh-o-9) binds all four: each has one owner, a recorded lineage, and a result that settles only after cleanup ([§6.3](#63-ownership-failure-retry-rollback-and-cleanup)).

- A **background job** lets the model start a long command and keep working. Without it every build, test run or server start blocks the turn that started it.
- A **schedule** wakes a session at a future time: "remind me every weekday at 09:00". Without it nothing happens unless a person is typing.
- A **goal** keeps one session working, turn after turn, until an objective is met, blocked or abandoned. Without it a person must type "continue".
- **Plan mode** makes the agent explore and write a plan that a person reviews before anything is changed. Without it the only brake is the approval prompt on each call.

## 2. Vocabulary

Terms from [../design.md §2](../design.md#2-vocabulary) and [agent-loop.md §2](agent-loop.md#2-vocabulary) (inbox, follow-up, steer, inject, claim, turn budget, lease, projection, scope) keep their meaning.

| Term | What it is | Why it exists |
|---|---|---|
| **Job, producer, kind** | A job is work that outlives the tool call that started it. Its producer is the code that does the work (a shell tool, a workflow); its kind is the producer's registered name. | One set of controls reads and stops every kind of long work. |
| **Output ring, model cursor, observer read** | The ring is a bounded in-memory buffer of a job's output, addressed by absolute byte offset. The model cursor is the one position up to which the model has been given output. An observer read is a read at any offset that moves nothing. | Output is kept without unbounded memory; the model gets each byte once; a browser can watch without stealing it. |
| **Settlement, completion notice** | Settlement is the single terminal record of a job: `completed`, `killed` or `failed`, with its cause. A completion notice is the message the host puts in the owner's inbox when a job settles that the model has not collected. | "The job ended" is one fact, written once, and the model learns of it without polling. |
| **Schedule** | A stored instruction with a timing rule and a target, owned by the harness home and not by a session. | It must fire while its session is not loaded. |
| **Occurrence, committed occurrence** | An occurrence is one instant a rule produces. The committed occurrence is the next one, stored as a UTC instant. | A stored instant does not move when zone data or the clock changes. |
| **Wall-time rule, elapsed-time rule; gap, overlap** | A wall-time rule says what the clock on the wall shows in a named zone ("09:00 in Asia/Shanghai"). An elapsed-time rule is a start plus whole intervals. At a spring change some wall times never happen (a gap); at an autumn change some happen twice (an overlap). | The two kinds of rule differ on the two days a year when the clock changes, which is where schedulers go wrong. |
| **Target** | Where a delivery goes: an existing session, one session created at first delivery, or a new session per delivery. | A reminder and an independent recurring task need different homes. |
| **Firing host, claim** | A firing host is a `dsh` process whose schedule component is allowed to deliver. A claim is the database record by which one firing host takes one due occurrence. | Several processes share one home ([DH-ADR-12](../design.md#dh-adr-12)); one of them must deliver, once. |
| **Delivery** | The durable insertion of a schedule's message into the target's inbox, plus its record. It proves the message was queued, and later whether a turn ran it. | "Sent" and "acted on" are different facts. |
| **Goal, goal round; armed, disarmed** | A goal is one objective a session keeps working on. A round is one turn the host opens on the goal's behalf. Armed means the host may open the next round by itself; that flag is kept in memory only. | A round is the unit that is counted, capped and attributed. A goal survives a restart; unattended work does not restart by itself. |
| **Plan mode, reviewed exit** | A logged per-agent state in which the agent may read but not change. The reviewed exit is the model presenting a plan that a person approves or sends back. | Planning and execution are separated by a human decision. |
| **Human turn** | An open turn that a message with source kind `user` entered, on an agent that is not a subagent. | The test for "a person asked for this" that goal and schedule tools share. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.jobs.jobs` | `@deepseek-ai/dsh-jobs` | definition | The abstract `ctx.jobs` registry, the job types, the two session records and the archive-admission listeners (`packages/jobs/jobs/src/types.ts:59-64`, `packages/jobs/jobs/src/archive-admission.ts:24-41`). |
| `UP-PKG-packages.jobs.jobs-local` | `@deepseek-ai/dsh-jobs-local` | provider | The in-process registry: admission, the ring, the pump, settlement, teardown (`packages/jobs/jobs-local/src/index.ts:217-245`, `:619-645`, `:672-728`). |
| `UP-PKG-packages.jobs.tool-jobs` | `@deepseek-ai/dsh-tool-jobs` | consumer | `job_output`, `job_list`, `job_kill`, the controller producers need, and completion notices (`packages/jobs/tool-jobs/src/index.ts:314`, `:433-571`). |
| `UP-PKG-packages.schedule.schedule` | `@deepseek-ai/dsh-schedule` | provider | Host-wide task store, one timer, delivery to sessions, change notices, archive admission (`packages/schedule/schedule/src/runtime.ts:222-223`, `packages/schedule/schedule/src/index.ts:320-342`). |
| `UP-PKG-packages.schedule.tool-schedule` | `@deepseek-ai/dsh-tool-schedule` | consumer | The four `schedule_*` tools and one authorization sentence, mounted per preset (`packages/schedule/tool-schedule/src/index.ts:199`, `:379-442`). |
| `UP-PKG-packages.goal.goal` | `@deepseek-ai/dsh-goal` | provider | `ctx.goals`: event-sourced goal state, compare-and-set mutations, the `goal` projection (`packages/goal/goal/README.md:97-100`). |
| `UP-PKG-packages.goal.goal-round-driver` | `@deepseek-ai/dsh-goal-round-driver` | consumer | Opens the next round at idle and fences races (`packages/goal/goal-round-driver/README.md:71-74`). |
| `UP-PKG-packages.goal.tool-goal` | `@deepseek-ai/dsh-tool-goal` | consumer | `get_goal`, `create_goal`, `update_goal` with execution-time authority checks (`packages/goal/tool-goal/README.md:36-38`, `:55`). |
| `UP-PKG-packages.goal.command-goal` | `@deepseek-ai/dsh-command-goal` | consumer | The human `/goal` command (`packages/goal/command-goal/README.md:36-41`). |
| `UP-PKG-packages.plan.plan-mode` | `@deepseek-ai/dsh-plan-mode` | provider and consumer | `ctx.planMode`, the `plan/mode` event, the guidance section, `/plan`, `exit_plan_mode` (`packages/plan/plan-mode/src/index.ts:55`, `:199-213`, `:283`). |

### 3.2 Behaviour

**Jobs.** A producer tool (the shell tools, the terminal's `pty-send`, the workflow tool; `packages/jobs/jobs/src/view.ts:32-34`, `packages/shell/tool-pwsh/src/index.ts:46-48`, `packages/workflow/tool-workflow/src/index.ts:37-39`) calls `ctx.jobs.start(spec)`. The registry checks, before the producer runs, that a controller serves the owner, that the owner's agent is live and that fewer than 10 of the owner's jobs are running or stopping; a refusal leaves nothing behind (`packages/jobs/jobs-local/src/index.ts:217-245`, `packages/jobs/jobs-local/README.md:53`, `:67`). The id is `<kind>-<16 hex>`, and access is decided by owner session, not by secrecy (`docs/subsystems/jobs.md:9`). Each job has one ring: pull sources are polled every 150 ms and pushed appends land whole; the model reads through one consuming cursor and observers read at offsets; the live ring keeps 256 KiB and a settled one 16 KiB (`docs/subsystems/jobs.md:240`, `packages/jobs/jobs-local/README.md:45-47`, `:82`). Owned jobs append `job/registered` and `job/settled` to the owner's session, and the settled record says whether the producer itself reported the end (`docs/subsystems/jobs.md:27`). The three tools read, list and cancel; a read may wait up to 30 s by default (600 s at most) and yields early to a user steer (`packages/jobs/tool-jobs/README.md:32-34`, `:56-57`). When an owned job settles and the model did not collect it, the owner gets a notice: injected into the next step when busy, a woken follow-up turn when idle, with the output inline when the whole notice fits 2048 bytes (`packages/jobs/tool-jobs/README.md:42`, `:60`). Waking is unbounded unless `maxConsecutiveWakes` is set (`packages/jobs/tool-jobs/README.md:44`). An active job blocks archiving its session, and an archive stop kills it (`packages/jobs/jobs/src/archive-admission.ts:25-41`).

**Schedules.** Tasks live in a host storage domain, not in a session, and all of them are loaded into memory when the host opens (`packages/schedule/schedule/README.md:82-84`). A file that cannot be read or validated is a load failure, never "no tasks" (`docs/subsystems/schedule.md:12`). Seven timing kinds exist: `after`, `at`, `every`, `daily`, `weekly`, `cron`, `rrule` (`docs/subsystems/schedule.md:26-34`). Wall-time rules skip a missing wall time and take the earlier instant of a repeated one; fixed intervals are elapsed time (`docs/subsystems/schedule.md:36`). Every stored occurrence is a UTC instant that reading never recomputes, and after downtime only the latest due occurrence is delivered (`docs/subsystems/schedule.md:24`). Three targets exist; a created session's id is reserved before the attempt so that a retry reuses it (`docs/subsystems/schedule.md:78-84`, `packages/schedule/schedule/src/runtime.ts:428-445`). One timer drives delivery, capped only because a Node timer overflows near 24.8 days, and a task that a clock rollback made future again is left waiting (`packages/schedule/schedule/src/runtime.ts:29`, `:222-223`, `:243-252`). Delivery queues a user-role message that opens with `This is a scheduled message from the user`, waits for the session flush, then saves a receipt and the next occurrence; the two commits are in different stores, so a crash between them repeats the message (`packages/schedule/schedule/src/domain.ts:1153`, `packages/schedule/schedule/README.md:177`). A task fires at most once per 60 s (`packages/schedule/schedule/src/runtime.ts:31-32`). The model tools accept one timing input, an RRULE, refuse a delegated caller, and reach every task of the host (`packages/schedule/tool-schedule/README.md:40-43`, `packages/schedule/tool-schedule/src/index.ts:313-314`, `packages/schedule/tool-schedule/README.md:143`).

**Goals.** A session has at most one current goal (`packages/goal/goal/README.md:32`). Every change appends a `goal/change` event holding the whole new state or a clear tombstone, and a mutation must name the exact `{id, revision}` it changes (`docs/subsystems/goal.md:93`, `packages/goal/goal/README.md:98`). The phase is `active`, `paused`, `blocked` or `complete`; whether continuation is armed is kept in memory and is off after any resume or fork (`docs/subsystems/goal.md:21-30`, `packages/goal/goal/README.md:99`). The driver, at whole-agent idle and after the agent's subagents have settled, queues one follow-up prompt carrying the objective and `round/cap`; only a goal message that enters history consumes a round, and a pre-step listener rejects a stale one (`packages/goal/goal-round-driver/README.md:49-53`, `:71-72`, `packages/goal/goal-round-driver/src/index.ts:177-208`, `:387`). At the cap (default 256) the driver records a blocker `round-limit` (`packages/goal/goal/README.md:46`, `packages/goal/goal-round-driver/src/index.ts:184`). The model may create, edit, pause and resume only in a turn that holds a direct human message on a root agent; inside a goal round it may only report `complete`, or `blocked` after 3 consecutive rounds (`packages/goal/tool-goal/README.md:55`, `packages/goal/tool-goal/src/index.ts:287-291`).

**Plan mode.** One log-only event, `plan/mode {active}`, is the state (`docs/subsystems/plan.md:11`). While active, a deployment-written guidance section is added to each request; nothing is restricted (`docs/subsystems/plan.md:5`, `packages/plan/plan-mode/README.md:32`). A person enters with `/plan` and leaves with `/plan off` (`packages/plan/plan-mode/README.md:55`). The model leaves by calling `exit_plan_mode` with a Markdown plan, which opens a review: approve, keep planning with feedback, or dismiss (`packages/plan/plan-mode/README.md:61-63`). The tool is offered from the first step in which plan mode was active and stays offered for the rest of the session (`docs/subsystems/plan.md:33`). A selection made while a turn runs waits in memory until the next accepted pre-step appends it (`docs/subsystems/plan.md:15-17`).

### 3.3 Failure, retry, rollback and cleanup upstream

| Situation | What upstream does | Left behind | Source |
|---|---|---|---|
| The owner agent or the registry is disposed | Cancels the jobs, waits for their producers, removes the records. A cancel that throws forces the record to `failed` with a warning | A cancel that returns without settling stalls teardown and keeps a capacity slot. **Uncertain:** forced work may be orphaned | `packages/jobs/jobs-local/README.md:57`, `:146`; `packages/jobs/jobs-local/src/index.ts:707-728` |
| The host exits while a job runs | Nothing; the ring and the record are gone | A `job/registered` with no `job/settled`; the final record can also be lost on a clean exit | `packages/jobs/jobs-local/README.md:144-145` |
| A notice is pending and the tools plugin reloads, or the owner is disposed | The output the notice held is dropped; the notice is discarded | The model reads `(no new output)` | `packages/jobs/tool-jobs/README.md:179`, `:181` |
| A schedule delivery, restore or write fails | The task is kept and a warning logged; no retry timer | A later change, wake or restart may retry | `packages/schedule/schedule/README.md:279` |
| The host dies between inbox write and receipt | The delivery is repeated | A duplicate message | `packages/schedule/schedule/README.md:280` |
| A goal round's turn fails, hits max tokens or is cancelled | Continuation is disarmed, or the goal paused after a user cancel; nothing is retried | A human must resume | `packages/goal/goal-round-driver/README.md:53`, `:129` |
| The review's interaction channel is missing, or the plugin reloads during review | `exit_plan_mode` fails; plan mode stays on | `/plan off` is the escape | `docs/subsystems/plan.md:33` |

## 4. Upstream decisions dissected

The first thirteen rows are the decision notes handed to this area (paths under `.agents/notes/`); the rest are decisions visible only in code or READMEs. Longer reasons are in the ledger rows.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `implemented/architecture/2026-07-31-goal-owned-durable-events.md` | Goal state is its own whole-value `goal/change` event, not an inbox message | keep | Durable state must not depend on message scheduling | [DH-ADR-JOBS-9](#dh-adr-jobs-9) |
| `implemented/architecture/2026-09-03-jobs-seam-consolidation.md` | One ring, two cursors, one projection, one event stream per job | keep, two changes | Pull sources become pushes; the notice's read moves into the registry | [DH-ADR-JOBS-2](#dh-adr-jobs-2) |
| `implemented/architecture/2026-09-16-host-schedule-storage.md` | Schedules are host storage; an unreadable store is a failure, never "empty" | keep, storage changed | A whole-unit file becomes rows; fail closed becomes per row | [DH-ADR-JOBS-5](#dh-adr-jobs-5) |
| `implemented/architecture/2026-09-17-standalone-schedules.md` | Three targets; ids reserved before creation; latest-only catch-up; 60 s floor; change notices | keep | The reservation is what makes a retry safe | [DH-ADR-JOBS-7](#dh-adr-jobs-7), [DH-ADR-JOBS-6](#dh-adr-jobs-6) |
| `implemented/architecture/2026-09-22-schedule-hard-delete-and-bounded-history.md` | Delete removes the row and its records; history bounded by age and count | keep | No state every reader must skip; bounded rows | [DH-ADR-JOBS-5](#dh-adr-jobs-5) |
| `implemented/architecture/2026-09-24-preset-scoped-schedule-tools.md` | Tools are a per-preset consumer; the service is host-wide | keep | A preset without the tools pays no prompt tokens | [DH-D-JOBS-6](#dh-d-jobs-6) |
| `implemented/architecture/2026-09-24-scheduled-message-framing.md` | A fixed origin line; the "treat as untrusted" wording was removed | keep, refined | The line now says who wrote the prompt, and the source kind is never `user` | [DH-ADR-JOBS-8](#dh-adr-jobs-8) |
| `implemented/architecture/2026-10-21-schedule-rule-storage.md` | One store; cron and RRULE are ordinary timing kinds; version 1 rows still read | keep; compatibility dropped | No upstream store is read ([DH-ADR-2](../design.md#dh-adr-2)) | [DH-ADR-JOBS-5](#dh-adr-jobs-5), [DH-ADR-JOBS-6](#dh-adr-jobs-6) |
| `implemented/bug-fix/2026-09-30-job-session-lifecycle.md` | `job/registered` and `job/settled` facts; a failed append never blocks cleanup | keep, extended | A resumed session also closes jobs the host lost | [DH-ADR-JOBS-4](#dh-adr-jobs-4) |
| `implemented/feature/2026-07-19-model-facing-goal-tools.md` | Three exclusive tools; compare-and-set; resume of a paused goal is the user's | keep | Authority is checked at execution, not in the prompt | [DH-ADR-JOBS-9](#dh-adr-jobs-9) |
| `implemented/feature/2026-09-22-archive-admission-reads-stored-tasks.md` | Archive admission is answered from stored rows; a stop deletes them | keep, one change | The stop pauses with a reason, because an archive can be undone | [DH-D-JOBS-5](#dh-d-jobs-5) |
| `implemented/simplification/2026-08-09-explicit-schedule-time-zone.md` | No implicit zone: an offset or a named zone on every input | keep | Removes session zone state; sampling the browser zone is A14's and C3's | [DH-ADR-JOBS-6](#dh-adr-jobs-6) |
| `proposed/feature/2026-10-29-event-triggered-automation-tasks.md` | Proposal: time becomes one source among several; event text is quoted as data | defer | Not built upstream; two of its rules are adopted now | [DH-ADR-JOBS-12](#dh-adr-jobs-12) |
| `start()` refuses an owner no controller serves (`packages/jobs/jobs-local/README.md:67`) | Work nobody can collect or stop must not start | keep | Same reason in Go | [DH-ADR-JOBS-1](#dh-adr-jobs-1) |
| Unowned jobs, open to any caller (`docs/subsystems/jobs.md:323`) | Host work without a session | drop | No shipped producer needs one; host work is a fiber effect | [DH-ADR-JOBS-1](#dh-adr-jobs-1) |
| Notices wake without limit by default (`packages/jobs/tool-jobs/README.md:44`) | An unattended chain keeps going | change | Finite by default, as the turn budget is | [DH-ADR-JOBS-3](#dh-adr-jobs-3) |
| The timer sleeps until the next occurrence (`packages/schedule/schedule/src/runtime.ts:222-223`) | One timer per host | change | A capped wake bounds lateness after a suspend or a clock step | [DH-ADR-JOBS-6](#dh-adr-jobs-6) |
| Activation is never persisted; a round is a follow-up turn, not a held-open turn (`packages/goal/goal/README.md:99`, `packages/goal/goal-round-driver/README.md:49`) | No unattended work after a restart; human input can interleave | keep | Fails closed; each round gets the turn budget and the turn guarantees | [DH-ADR-JOBS-9](#dh-adr-jobs-9), [DH-ADR-JOBS-10](#dh-adr-jobs-10) |
| Max tokens and failures only disarm (`packages/goal/goal-round-driver/README.md:53`) | Stop quietly | change | A logged blocker with a code survives a restart and is visible | [DH-ADR-JOBS-10](#dh-adr-jobs-10) |
| Plan mode is soft guidance (`docs/subsystems/plan.md:5`) | Restriction is the sandbox's job | change | A mode a person relies on must be enforced where calls are decided | [DH-ADR-JOBS-11](#dh-adr-jobs-11) |
| A selection waits for the next pre-step (`docs/subsystems/plan.md:15`) | Session events were turn-enclosed | change | Go appends a log-only state event at once, as `model/selection` is | [DH-ADR-JOBS-11](#dh-adr-jobs-11) |
| `exit_plan_mode` stays offered once seen (`docs/subsystems/plan.md:33`) | Later transitions do not change the tool list | keep | Keeps the cached request prefix | [DH-ADR-JOBS-11](#dh-adr-jobs-11) |

## 5. Tech debt and its resolution

The 53 units are 51 README limits and 2 `FIXME` markers. They are grouped by cause; each unit's own reason is in its ledger row.

| Group (units) | Cause | Answer in Go |
|---|---|---|
| **Jobs die with the host** (2: `jobs.1`, `jobs-local.1`) | A producer holds process handles and in-process callbacks | **Carried.** No supervisor outside the host exists to re-attach to. What changes: the loss is recorded and the model is told ([DH-ADR-JOBS-4](#dh-adr-jobs-4)) |
| **A job can end without a record, or never end** (2: `jobs-local.2`, `jobs-local.3`) | Sibling fibers unload together; a cancel that does nothing looks like a slow stop | Resolved: settlement is appended before the owner's final flush, and a stop that exceeds its grace is forced, reported and its slot freed ([DH-ADR-JOBS-4](#dh-adr-jobs-4)) |
| **One consuming reader** (2: `jobs.2`, `tool-jobs.5`) | By design | **Carried**, deliberately: two consuming readers would each see part of the output ([DH-ADR-JOBS-2](#dh-adr-jobs-2)) |
| **Unbounded or unfenced registry state** (2: `jobs.3`, `tool-jobs.6`) | No retention count; unowned jobs | Resolved: at most `maxSettledPerOwner` settled records; no unowned jobs ([DH-ADR-JOBS-1](#dh-adr-jobs-1)) |
| **Notices that are lost or stuck** (4: `tool-jobs.1` to `.4`) | The sender decides busy or idle; held output lives in plugin memory; the wake budget never refills; disposal clears the inbox | Resolved: one waking send; the read is committed by the registry when the notice is claimed; the budget refills with time; unreported settlements are derived from the log on resume ([DH-ADR-JOBS-3](#dh-adr-jobs-3)) |
| **The steer release is copied into one tool** (2 markers: `tool-jobs/src/index.ts:477`, `:498`) | No shared signal | Resolved: one steer watch per agent, shared by every blocking wait ([DH-ADR-JOBS-3](#dh-adr-jobs-3)) |
| **Delivery is not exactly once and is not retried** (3: `schedule.2`, `.8`, `tool-schedule.4`) | Two stores, no retry timer, push-only notices | Resolved: a claim plus a deterministic message id; delivery state followed to the turn; change notices derived at turn start; delete retracts an unclaimed message ([DH-ADR-JOBS-7](#dh-adr-jobs-7)) |
| **The host must be running** (1: `schedule.1`) | No daemon | **Carried**, narrowed: any firing host on the home delivers, failures retry with back-off, lateness is reported ([DH-ADR-JOBS-7](#dh-adr-jobs-7)) |
| **Storage grows with history** (2: `schedule.6`, `.3`) | A whole-unit document | Resolved: indexed rows; history is its own table; a delivered message stays in its session's log after a delete ([DH-ADR-JOBS-5](#dh-adr-jobs-5)) |
| **Timing limits** (2: `schedule.5`, `.7`) | A bounded rule language, live zone data, no concurrency cap | `.5` **carried**: the subset and "zone data may change" are stated limits ([DH-ADR-JOBS-6](#dh-adr-jobs-6)). `.7` resolved: a cap on concurrently created sessions and a reported missed count |
| **Any agent reaches every schedule** (5: `schedule.4`, `tool-schedule.3`, `.5`, `.6`, `.7`) | Host-wide management with a prompt sentence as the only check | Resolved: callers are scoped, mutations need a human turn or an approval, a delegated agent is not shown the tools, results show time and delivery state ([DH-ADR-JOBS-8](#dh-adr-jobs-8)) |
| **Composition surprises** (5: `tool-schedule.1`, `.2`, `tool-goal.3`, `.4`, `.5`) | Pending plugins, guidance registered apart from tools | Resolved: required services are named by the loader; a prompt section is rendered only when one of its tools is in the step view ([DH-D-JOBS-6](#dh-d-jobs-6), [DH-D-JOBS-7](#dh-d-jobs-7)) |
| **Goals are capped by rounds only and never retried** (4: `goal.1`, `.2`, `round-driver.4`, `.5`) | No resource budget; no retry policy | Resolved: optional token and time budgets, a rule for turns that end on the turn budget, and a bounded retry of transient failures ([DH-ADR-JOBS-10](#dh-adr-jobs-10)) |
| **The model is the judge** (4: `goal.3`, `round-driver.1`, `tool-goal.1`, `.2`) | No evaluator | **Carried.** A `goal.Completing` interception lets a deployment veto a completion; no evaluator ships ([DH-ADR-JOBS-10](#dh-adr-jobs-10)) |
| **One goal, one session** (2: `goal.4`, `round-driver.2`) | By design | **Carried**, deliberately: parallel objectives and fresh-agent attempts are delegation (A11, A12) ([DH-ADR-JOBS-9](#dh-adr-jobs-9)) |
| **Forged goal records and an unload race** (2: `goal.5`, `round-driver.3`) | In-process plugins; asynchronous unload | Resolved: no third-party code in the host ([DH-ADR-4](../design.md#dh-adr-4)) and a private append handle; effects unwind newest first, so the queued prompt is removed before its fence ([DH-D-JOBS-8](#dh-d-jobs-8)) |
| **`/goal` is text only and web only** (4: `command-goal.1` to `.4`) | A generic command registry and one adapter | Resolved: goal operations are service calls reachable from a server-rendered panel, the SDK protocol and the command line ([DH-D-JOBS-7](#dh-d-jobs-7)) |
| **Plan mode restrains by text and forgets** (5: `plan.1` to `.5`) | Soft guidance; pending selections in memory; no creation option; one renderer; the review needs a person | Resolved: enforced read-only, immediate append, inheritance and a creation option, a typed review interaction. `.4` **carried**, deliberately: a subagent returns its plan to the parent that asked ([DH-ADR-JOBS-11](#dh-adr-jobs-11)) |

Thirteen rows are `carried`; their ledger rows name stage [DH-S-47](../impl.md#dh-s-47), the known limits and scope boundaries of the core areas.

## 6. Go design

### 6.1 Shape

All packages sit under `src/harness/internal/delegate` ([../design.md §4.2](../design.md#42-go-module-layout)). A host service and the tools that expose it to the model are separate components, so a preset that does not mount the tools pays no prompt tokens for them.

| Package | Component | Provides | Injects | Why it exists |
|---|---|---|---|---|
| `delegate/jobs` | none (contract) | `jobs.Key`, `Registry`, the job types, the two event types, the `jobs` projection | n/a | What producers (A09, A11, A12) and consumers import |
| `delegate/jobs/local` | `jobs.registry` | `jobs.Registry` | `agent.Agents`, session store, activity providers (A07) | The one provider: jobs live in the host process |
| `delegate/jobs/tools` | `tool.jobs` | Three tools, a controller, notice delivery, the steer watch (`delegate/steer`) | `jobs.Key`, `tools.Key`, prompt | The model's controls |
| `delegate/schedule` | `schedule.service` | `schedule.Key` (`Service`), `schedule.ActivatorKey` fallback | database (A05), `agent.Agents`, recurrence (A23), timer, activity providers | Store, wake loop, claims, delivery |
| `delegate/schedule/tools` | `tool.schedule` | Four tools, the authorization section, derived change notices | `schedule.Key`, `tools.Key`, prompt | Per-preset consumer |
| `delegate/goal` | `goal.service` | `goal.Key` (`Service`), the `goal/change` type, the `goal` projection | `agent.Agents`, projections | State only; it starts no work |
| `delegate/goal/rounds` | `goal.rounds` | Nothing; it opens rounds | `goal.Key`, `agent.Agents`, `plan.Key` (optional) | Continuation is a separate choice from state |
| `delegate/goal/tools`, `delegate/goal/command` | `tool.goal`, `command.goal` | Three tools; the `/goal` command | `goal.Key`, `tools.Key`; the command registry (A17) | Model half and human half |
| `delegate/plan` | `plan.mode` | `plan.Key` (`Controller`), `plan/mode`, the guard, the section, `exit_plan_mode`, `/plan` | `tools.Key`, prompt, interactions (A17), sandbox policy (A10, optional) | One product feature with no swappable provider, as upstream |
| `delegate/authority` | none | `HumanTurn(agent)` | n/a | One definition of "a person asked", used by goal and schedule tools |

```go
// package jobs. Caller is an agent reference or an owner principal admitted by a carrier.
type Registry interface {
    RegisterKind(owner *cordis.Context, kind string) (*cordis.EffectHandle, error)
    AttachController(owner *cordis.Context, name string) (*cordis.EffectHandle, error)
    Start(ctx context.Context, s Spec) (ID, error)          // preflight, then run; a refusal leaves nothing behind
    List(c Caller) []View; Get(id ID, c Caller) (View, error)
    Peek(id ID, c Caller) (Read, Token, error)              // unread output for the model; moves nothing
    Commit(id ID, t Token) error                            // advance the model cursor to what Peek returned
    ReadAt(id ID, from int64, c Caller) (OutputRead, error) // observers; never moves the model cursor
    Kill(id ID, c Caller, reason string) (KillResult, error); Remove(id ID, c Caller) error
    Wait(ctx context.Context, id ID, c Caller) (View, error) // ctx carries the timeout and the steer signal
    Subscribe(owner *cordis.Context, f Filter, fn func(Event)) (*cordis.EffectHandle, error)
}
type Spec struct { // Run returns only after its resources are released
    Kind, Label string; Owner agent.Ref; ToolCall ToolCallRef; OutputLimit int; Run func(ctx context.Context, h Handle) Outcome
}
type Handle interface{ ID() ID; Append(ch Channel, p []byte); Progress(line string); Spill(ref string) }

// package schedule
type Service interface {
    Create(ctx context.Context, by Caller, r CreateRequest) (Schedule, error) // CreateRequest{Target, Timing, Prompt, Label}: one shape for a person and for the model's tool
    Edit(ctx context.Context, by Caller, r EditRequest) (Schedule, error) // update, pause, resume or delete; compare-and-set on Revision
    Get(ctx context.Context, by Caller, id ID) (Schedule, error); List(ctx context.Context, by Caller, q Query) (Page, error) // List: no prompt bodies; Query filters by target session and state
    History(ctx context.Context, by Caller, id ID, q HistoryQuery) ([]Delivery, error); RunNow(ctx context.Context, by Caller, id ID) (Delivery, error)
    Status(ctx context.Context) HostStatus                                // host time, live firing hosts, zone data source
    Preview(t Timing, now time.Time, n int) ([]Occurrence, error)         // pure: a rejection, or the next n occurrences with gap and overlap notes, by the code Create uses
}
type Activator interface { // the carrier that keeps sessions loaded provides it; a fallback ships with the component
    Activate(ctx context.Context, s session.ID) (agent.Agent, func(), error); Create(ctx context.Context, s session.ID, o CreateOptions) (agent.Agent, func(), error)
}

// package goal (Service) and package plan (Controller; State is Active, StepActive for the open step, EverActive)
type Service interface {
    Get(a agent.Agent) (View, bool, error); Disarm(a agent.Agent) error
    Create(ctx context.Context, a agent.Agent, by Authority, r CreateRequest) (View, error)
    Update(ctx context.Context, a agent.Agent, by Authority, ref Ref, op Operation) (View, error) // edit (objective, round limit, budgets), pause, resume, complete, block, clear
} // View: objective, state, revision, rounds used and the round limit, the budgets and what is spent, and the armed flag
type State struct { Active, StepActive, EverActive bool; Enforcement string } // plan state; Enforcement is enforced or guidance
type Controller interface { Get(a agent.Agent) State; Set(ctx context.Context, a agent.Agent, active bool, by Origin) error }
```

### 6.2 Contracts

**Jobs.** Status is `running`, `stopping`, `completed`, `killed` or `failed`. `Start` checks, in order: the kind is registered; the owner resolves to a live agent; a controller is attached on the owner's scope chain; fewer than `maxConcurrentJobsPerOwner` of the owner's jobs are live. It then appends `job/registered` and runs `Spec.Run` in a goroutine the registry owns. The producer writes output with `Handle.Append`; there is no polling pump. The ring trims whole chunks from the head, keeps the tail of one oversized chunk on a rune boundary, and reports a read below the retained window as `lossy` with the spill references the producer gave. `Kill` cancels the job's context with the reason and marks it `stopping`; settlement happens when `Run` returns, or when `stopGrace` has passed ([DH-ADR-JOBS-4](#dh-adr-jobs-4)). The first terminal outcome wins: record it, trim the ring to the settled size but never below what the model has not read, append `job/settled`, release waiters, then notify subscribers.

**Notices.** `tool.jobs` subscribes to settlements in its scope. It skips a settlement that released a live `Wait`, a job the model killed, and causes `teardown` and `host-exit` (the latter is reported on resume). Otherwise it peeks the unread output and sends one message with source kind `job`: with the output inline when the whole text fits `inlineOutputBytes` and the producer's `OutputLimit`, otherwise with "read its output with job_output". The send is a steer that wakes while the owner's wake budget lasts and an injection otherwise. The cursor is committed when the driver claims the message; a discarded notice commits nothing, so `job_output` still returns the output.

**Schedule rows.** Two tables in the home database, reached through area A05's database package, with a revision counter (area A07's mechanism) increased in every write transaction so that other processes notice.

| Table | Columns | Notes |
|---|---|---|
| `schedules` | `id`, `version`, `title` (at most 120 characters), `prompt` (at most `maxPromptBytes`), `timing` (kind and its fields, JSON), `next_at` (UTC instant; null when paused or ended), `status` (`active`, `paused`, `ended`, `invalid`), `status_reason`, `target` (JSON), `pending_session`, `creator` (session id or owner principal), `author` (`human` or `model`), `source` (`time`), `expire_after`, `revision`, `created_at`, `updated_at`, `claim_instance`, `claim_epoch`, `claim_occurrence`, `attempts`, `retry_at` | Indexed on (`status`, `next_at`), on the target session and on `creator`. A row that does not decode, or whose `version` is newer than the binary knows, is `invalid`: listed with its error, never fired, never deleted automatically |
| `schedule_deliveries` | `schedule_id`, `occurrence`, `message_id`, `target_session`, `state` (`enqueued`, `ran`, `discarded`, `skipped`, `failed`), `turn`, `turn_start_seq` (the `seq` of that turn's `turn/start`, for a link into the transcript), `waiting` (why a row has not been sent: `target-held`, `retry-at`, `no-firing-host`), `missed`, `late_by`, `delivered_at`, `prompt` | Pruned in the receipt transaction to `deliveryHistoryRecords` rows and `deliveryHistoryDays` days; the latest row is always kept. Deleted with its schedule |

**Time rules** ([DH-ADR-JOBS-6](#dh-adr-jobs-6)). Every stored time is a UTC instant in the one text form of [DH-LIB-TEMPORAL-R1](../libs/js-temporal-polyfill.md). A wall-time rule names `UTC` or an IANA zone; the host's own zone is never used, and the name is stored as given ([DH-ADR-TEMPORAL-2](../libs/js-temporal-polyfill.md#dh-adr-temporal-2)).

| Timing kind | Advances in | A wall time that does not exist (gap) | A wall time that occurs twice (overlap) |
|---|---|---|---|
| `after`, `every` | elapsed time from the acceptance instant or the interval anchor | not affected | not affected |
| `at` | one instant | a local input in a gap is rejected as invalid | the earlier instant |
| `daily`, `weekly`; `cron` with a fixed hour; `rrule` with `DAILY` or longer | wall time in the rule's zone | that date's occurrence is skipped, not moved; it does not use up `COUNT` | fires once, at the earlier instant |
| `cron` whose hour field is `*` or `*/n` | each real minute is matched against its local fields | nothing to skip: those minutes do not exist | fires in both passes of the repeated hour |
| `rrule` with `MINUTELY` or `HOURLY` | elapsed time from `DTSTART` ([DH-ADR-RRULE-2](../libs/rrule.md#dh-adr-rrule-2), adopted) | not affected | not affected |

"Due" is decided by comparing `next_at` with the wall clock at the moment of acting, never by trusting that a timer fired on time. The wake loop sleeps for the smaller of "time to the earliest `next_at` or `retry_at`" and `wakeCap`; with no active schedule it does not run. **Missed occurrences** (the host was down, the machine slept, the clock jumped forward): one delivery, labelled with the latest due occurrence, carrying `missed` (how many occurrences were passed over, counted up to 1000) and `late_by`; `next_at` becomes the first occurrence after now; nothing in between is replayed; `COUNT` counts calendar occurrences, so missed ones use it up. If the schedule sets `expire_after` and the latest due occurrence is older than that, a `skipped` record is written and nothing is delivered. **The clock moved back:** a row that is no longer due waits; revisions and history order are counters, not timestamps. **Zone data changed:** the committed `next_at` is honoured; later occurrences use the new data. A schedule fires at most once per 60 s (`newSessionInterval` for a `new` target), and at most `maxConcurrentNewSessions` sessions created by schedules run at once.

**Firing a due occurrence** ([DH-ADR-JOBS-7](#dh-adr-jobs-7)). Every firing host runs the same steps; "live" is area A05's instance record.

1. **Select.** Read rows with `status = active` and `next_at` or `retry_at` at or before now.
2. **Defer to the holder.** If the target session's lease is held by another live process, skip the row: only the lease holder can append to that inbox. The row shows `waiting: target-held`.
3. **Claim.** One transaction sets `claim_instance`, `claim_occurrence` and `claim_epoch + 1` where the revision is unchanged and the claim is empty or its holder is not live. One process wins; the others see the claim and skip.
4. **Resolve the target.** A missing session pauses the schedule with reason `target-missing`; an archived one with `target-archived`. For `new_once` and `new`, the reserved id is stored in the claim transaction before the session is created; "already exists" on a retry is success.
5. **Send.** One follow-up with message id `sch:<schedule id>:<occurrence>:<attempt generation>` and source `{kind: schedule, producers: [schedule id]}`. A duplicate id means "already delivered". A reused target that still runs the previous delivery of the same schedule is not sent to again: occurrences coalesce.
6. **Record.** After the splice is durable, one transaction guarded by `claim_epoch` inserts the delivery row (`enqueued`), sets `next_at` or `ended`, clears the claim and prunes history.
7. **Follow.** A claim by a turn sets `ran{turn}`; a discard sets `discarded{cause}`, and a discard caused by disposal makes the occurrence due once more under the next generation.

The message opens with a fixed block: `[SCHEDULE REMINDER]`, a line saying it is a scheduled message and who wrote its prompt (the user, or the assistant in session S at the user's request), then `schedule_id`, `occurrence_at`, `late_by`, `missed` and the prompt as a JSON string. Due recurring schedules for one target are batched into one message, as upstream.

**Goals.** `goal/change` holds the complete state `{id, revision, objective, phase, blockedReason, maxRounds, maxTokens, maxDuration}` or a clear tombstone. The `goal` fold is strict: a malformed record, a revision gap, an illegal transition or a non-sequential round marks the state corrupt, and every goal call then fails with `goal/state-corrupt`. Rounds started, tokens used and time armed are derived from the log: a round counts when a `user/message` whose source is `{kind: goal, goal, revision, round}` is admitted. **The round driver** acts when the agent becomes idle after its checkpoint (agent-loop point 16): if the goal is `active` and armed, the inbox is empty, plan mode is off, the agent's subagents have settled and every budget has room, it sends one follow-up with a goal source. A `PreStep` listener rejects a goal message that no longer matches the current goal; a rejected message consumes no round. A human message that arrives while a round prompt is still queued removes the prompt. **How a goal ends.**

| Ending | Who | Recorded as |
|---|---|---|
| Achieved | The model in a goal round or a human turn, unless a `goal.Completing` listener rejects; a person | `complete` |
| Blocked by the model | The model, in a human turn or after `blockedAfterConsecutiveRounds` rounds | `blocked{model-reported, text}` |
| A budget ran out | The driver | `blocked{round-limit}`, `{token-budget}`, `{time-budget}`, or `{turn-budget}` after `maxConsecutiveBudgetRounds` rounds that each ended on the turn budget |
| A round failed | The driver, after `retryFailedRounds` retries of a failure area A04 classes as transient; at once for `max-tokens` | `blocked{provider-failure}`, `{max-tokens}` |
| A person stops it | `/goal pause`, the panel, or a user cancel during a round (which also cancels the turn) | `paused`; `/goal clear` writes the tombstone |
| The host stops, or the session is resumed or forked | n/a | Phase unchanged; disarmed until a person resumes |

**Interaction with the turn budget** ([DH-ADR-AGENT-7](agent-loop.md#dh-adr-agent-7)). A round is one turn, so the step, attempt and time limits apply to each round. A round that ends with reason `budget` closed cleanly: it counts as a round, and the driver, being exactly the caller that says "continue", opens the next one, up to the limit in the table.

**Plan mode.** `plan/mode` holds `{active, by, call}`, where `by` is `command`, `ui`, `creation`, `inherited` or `review`. It is appended when the selection is made, with or without an open turn; the driver's "propose step" captures the state once per step, and the guard and the prompt section read the captured value, so a change applies from the next step and an approved exit leaves the rest of the current tool batch under plan mode. While plan mode is active for a step:

| Call | Under `enforcement: read-only` (default) | Why |
|---|---|---|
| A tool whose declaration says `Effects: none` (reads, searches, `job_list`, `get_goal`, `schedule_list`, `schedule_view`) | Allowed | Exploration is the point |
| `exit_plan_mode`, `todo_write`, the ask-user tool, and names in `allowTools` | Allowed | They change only the conversation |
| A tool that starts a command (shell, terminal) | Allowed only under a sandbox policy capped at `read-only` for this agent; where area A10 cannot apply the cap (no backend, `danger-full-access`) the call is denied | Reading needs commands; writing must be impossible, not discouraged |
| Every other tool (file writes, `job_kill`, `schedule_create`, `schedule_edit`, `create_goal`, `update_goal`, delegation that would write, MCP tools without an effects declaration) | Denied by a guard with `plan/read-only` and a message naming `exit_plan_mode` | Deny-only, so no listener order can undo it |

`enforcement: guidance` reproduces upstream: the section only. A subagent created while its creator is in plan mode starts in plan mode (`by: inherited`) and is not offered `exit_plan_mode`. `exit_plan_mode` requires a Markdown plan that starts with a heading, asks through A17's `question.Service`: one interaction of kind `question` with intent `plan-review` ([interaction-credentials.md §6.2](interaction-credentials.md#62-contracts); the registry has four closed kinds and `plan-review` is not one of them), whose payload is the plan text and whose three settlements are approve, keep planning with feedback, and dismiss. It goes to the calling agent's answerer and settles by the answer: approve appends `plan/mode{active: false, by: review}`; keep planning fails the call with `plan/revision-requested` and the feedback; dismiss returns `{approved: false}` and the plan component rejects the turn's next step, so the turn ends as `blocked{by: plan}`; no answerer is `plan/no-reviewer`; a cancelled interaction is `plan/review-interrupted`. **What is logged:** every `plan/mode` with its origin; the plan text, verbatim, as the arguments of the `tool/call`; the asked and decided pair (A17); each denial as a `tool/result` with code `plan/read-only`; the section as part of the logged prompt; and one notice message (source kind `plan`) when the mode differs from the one the last request header described.

**Tools exposed to the model.** Names and arguments are upstream's unless stated. Every result also passes the pipeline's ceiling ([DH-D-TOOLS-2](tools.md#dh-d-tools-2)). Mutating tools declare `Reach: direct`: a script cannot call them through the tool command line.

| Upstream unit | Go tool | What it does | Bounded result |
|---|---|---|---|
| `UP-TOOL-dsh-tool-jobs.job_output` | `job_output(job_id, wait?, timeout_ms?)`, exclusive | Returns output since the last read and the status; waits for settlement, the timeout or a user steer when asked; the first read after settlement carries the job's result once | Text at most the ring (`retainBytes`) and the producer's `OutputLimit`, then `[status: ...]`; `timeout_ms` clamps to `maxWaitTimeout` |
| `UP-TOOL-dsh-tool-jobs.job_list` | `job_list()`, parallel-safe | One line per job of the caller: id, kind, status, label | At most `maxConcurrentJobsPerOwner + maxSettledPerOwner` lines, labels cut at 120 characters |
| `UP-TOOL-dsh-tool-jobs.job_kill` | `job_kill(job_id, reason?)`, exclusive | Requests cancellation; the job settles when its work has stopped | One snapshot |
| `UP-TOOL-dsh-tool-schedule.schedule_create` | `schedule_create(title, prompt, rrule, target, expire_after_seconds?)` | Creates one schedule; `target` is `this_session`, `new_once` or `new` | One schedule; the prompt is bounded by `maxPromptBytes`; the result also says whether a firing host is live |
| `UP-TOOL-dsh-tool-schedule.schedule_list` | `schedule_list(active?, limit?, offset?)` | Lists schedules the calling session created or receives | Summaries without prompt bodies; `limit` 20 by default, 100 at most |
| `UP-TOOL-dsh-tool-schedule.schedule_view` | `schedule_view(id)` | One schedule with host time, next occurrence and the last five deliveries with their state | One schedule |
| `UP-TOOL-dsh-tool-schedule.schedule_edit` | `schedule_edit(id, action, ..., expectedRevision?)` | Update, pause, resume or delete; delete says whether a queued message was retracted | One schedule or `{deleted, retracted}` |
| `UP-TOOL-dsh-tool-goal.get_goal` | `get_goal()` | The current goal or null | Compact JSON; the objective is at most `maxObjectiveBytes` |
| `UP-TOOL-dsh-tool-goal.create_goal` | `create_goal(objective, max_goal_rounds?)`, exclusive | Creates and arms a goal; human turn only | As `get_goal` |
| `UP-TOOL-dsh-tool-goal.update_goal` | `update_goal(goal_id, revision, action, ...)`, exclusive | `edit`, `pause`, `resume`, `complete` or `blocked` on the exact revision; a terminal report from a goal round adds a closing instruction. An optional argument that does not affect the requested action is accepted and ignored, and an empty string in `objective`, `reason` or `summary` means "absent" (`DH-LIB-ZOD-R13`) | As `get_goal` |
| `UP-TOOL-dsh-plan-mode.exit_plan_mode` | `exit_plan_mode(plan)`, exclusive; offered once plan mode has been active | Presents the plan for review | `{approved}`; `plan` is at most `maxPlanBytes` |

**Events.** Live keys, declared by the owning package, in the forms of [../design.md §4.6](../design.md#46-event-model).

| Upstream unit | Go key and form | Payload |
|---|---|---|
| `UP-EVT-goal/changed` | `goal.Changed`, notify, through the agent's scope | `{Operation, Ref, View}`, after the append |
| `UP-EVT-goal/activation-changed` | `goal.ActivationChanged`, notify | `{Session, Ref, Armed}` |
| `UP-EVT-schedule/changed` | `schedule.Changed`, notify | `{IDs, Origin}` where origin is `local` or `foreign` (seen through the revision counter) |
| `UP-EVT-schedule/resolve-agent` | none: the `schedule.Activator` service | A service call cannot be shadowed by listener order, and it returns a release function |
| new | `goal.Completing`, around | `{Agent, View, Evidence}` to `Accept{}` or `Reject{Reason}` |

**Config.** Go structs with defaults as a Go value, decoded strictly ([DH-ADR-SCHEMA-3](../libs/zod.md#dh-adr-schema-3)). Upstream defaults are kept where upstream has one.

| Upstream unit | Go struct | Fields (default) |
|---|---|---|
| `UP-CFG-dsh-jobs-local` | `local.Config` | `maxConcurrentJobsPerOwner` (10), `retainBytes` (262144), `settledRetainBytes` (16384), `maxSettledPerOwner` (50), `stopGrace` (10s). `pumpPollMs` is gone |
| `UP-CFG-dsh-tool-jobs` | `jobtools.Config` | `waitTimeout` (30s), `maxWaitTimeout` (10m), `completionDelivery` (`wakeup` or `quiet`), `maxConsecutiveWakes` (50; 0 means unlimited and must be written), `wakeRefillInterval` (10m), `inlineOutputBytes` (2048) |
| `UP-CFG-dsh-schedule` | `schedule.Config` | `fire` (true in profile `web`, false elsewhere), `wakeCap` (30s; 1s to 5m), `deliveryHistoryDays` (30), `deliveryHistoryRecords` (200), `newSessionInterval` (60s; up to one week), `maxConcurrentNewSessions` (4), `maxActive` (500), `maxPerSession` (20), `maxPromptBytes` (16384), `retryBackoff` (30s, 2m, 10m, 1h), `maxDeliveryAttempts` (5) |
| `UP-CFG-dsh-goal` | `goal.Config` | `defaultMaxRounds` (256), `defaultMaxTokens` (0, none), `defaultMaxDuration` (0, none), `maxObjectiveBytes` (8192) |
| `UP-CFG-dsh-tool-goal` | `goaltools.Config` | `blockedAfterConsecutiveRounds` (3). The driver adds `rounds.Config`: `retryFailedRounds` (2), `retryBackoff` (1m, 5m), `maxConsecutiveBudgetRounds` (3) |
| `UP-CFG-dsh-plan-mode` | `plan.Config` | `section` (required, non-empty), `enforcement` (`read-only`), `allowTools` (empty), `maxPlanBytes` (262144) |

**Services.** `UP-SVC-jobs` becomes `jobs.Key` (`jobs.Registry`); `UP-SVC-schedule` becomes `schedule.Key` (`schedule.Service`); `UP-SVC-goals` becomes `goal.Key` (`goal.Service`); `UP-SVC-planMode` becomes `plan.Key` (`plan.Controller`).

**Persisted data.** Three log-only session event types, kept from upstream and each registered with a payload version: `job/registered` and `job/settled` (the settled cause gains `host-exit`; the record names the originating tool call and, for a shell job, its command, and the job's output is still readable through `ReadAt` when the registry's subscribers are told of the settlement, which is what a listener such as X2's change-request binding needs), `goal/change`, and `plan/mode`. A job may own a call token that the `workflow` tool handed over ([tools.md](tools.md#dh-adr-tools-6) item 7, [DH-ADR-29](../design.md#dh-adr-29)); the registry deletes it before it appends the settlement, and `Kill` deletes it first. Upstream's `schedule/change` session event is not built: schedules are rows. Projections: `jobs` (jobs with no settlement; settlements the model has not been given), `goal`, `plan`, and `schedule.seen` (the schedule revisions this session's model was last shown).

### 6.3 Ownership, failure, retry, rollback and cleanup

**One owner, a recorded lineage, a result that settles after cleanup** ([DH-O-9](../design.md#dh-o-9)).

| | Owner (who releases it) | Access | Recorded lineage | Settles only after |
|---|---|---|---|---|
| **Job** | The owning agent's scope fiber; the registry fiber is a second owner. Resource class: created | The owner session's agent and an owner principal through a carrier; nobody else | `job/registered{jobId, owner, kind, label, toolCall{callId, rootCallId}}` in the owner's log | `Run` returned, or `stopGrace` passed and the record says `producerReported: false`; the ring is closed and `job/settled` appended |
| **Schedule** | The harness home: a row lives until a person or its creator deletes it. One firing is owned by the claiming process. Resource class: created, in a shared home | The creator session, the target session and an owner principal | Row `creator` and `author`; each message's source and delivery row name the schedule and occurrence; a created session's header names the schedule | The inbox splice is durable and the delivery row is committed under the claim |
| **Goal** | The session: the state is in its log. Arming belongs to the `goal.service` fiber of the process holding the lease | The session's agent in a human turn; the driver's rounds; an owner principal | The `goal/change` chain with revisions; each round's message source `{goal, revision, round}` | A terminal phase is one appended event; a round settles at its `turn/end` and flush |
| **Plan mode** | The session (logged state). A pending review is owned by the `exit_plan_mode` call | An owner principal sets it; the model can only ask to leave | `plan/mode{by, call}`; the review pair written by A17 | The interaction is closed and `plan/mode` appended; the tool call settles last |

| Situation | What happens | Undone, and what stays uncertain |
|---|---|---|
| `Start` is refused, or `Run` panics at once | Nothing is registered; a panic after registration settles the job as `failed` | The producer cleans up what it had begun |
| A job ignores cancellation | After `stopGrace` it settles `failed`, `producerReported: false`, detail "did not stop"; its slot is freed; the goroutine is listed as detached | **Uncertain:** the work may still run. Process producers end their process tree (A09) |
| The owner agent is disposed | The scope fiber's cleanup cancels its jobs with cause `teardown` and waits as above; settlements are appended before the agent's final flush (teardown step T6) | Records leave the registry; no notice is sent |
| The host is killed while jobs run | On resume the `jobs` fold lists them; the registry appends `job/settled{failed, host-exit, producerReported: false}` for each and injects one notice naming them | **Uncertain:** whether a child process survived (A09) and what it did. Ring output is lost; spilled output is not |
| `tool.jobs` is reloaded with notices pending | Nothing is lost: the unread output is in the registry and uncommitted | The inbox message stays; its claim commits through the new instance |
| A due schedule's delivery fails (resume, create, send) | The claim is released, `attempts` grows, `retry_at` follows `retryBackoff`; after `maxDeliveryAttempts` the schedule is paused with `delivery-failed` | Nothing was delivered; a reserved session id is kept for the retry |
| The claimant dies after sending, before recording | Its claim's holder is no longer live; another firing host claims and sends the same message id, which the inbox refuses as a duplicate; it then records | Exactly one message |
| Two firing hosts wake together | One claim transaction wins; a busy database is the classified `store/busy` and the loser retries at its next wake | Nothing |
| The target's lease is held by a process that does not fire | The row waits and says so; it fires when the lease is free | Delivery is late, never doubled |
| The target session was deleted or archived | The schedule is paused with the reason; nothing is created in its place | The user retargets, resumes or deletes |
| A schedule is deleted or paused with a message still queued | The unclaimed message is removed from the inbox when this process holds the session; otherwise the result says `retracted: false` | A message already claimed by a turn runs |
| A session is archived with active schedules targeting it | The activity provider reports them; a stop pauses them with `target-archived` | Reversible, unlike upstream's delete |
| The schedule tables cannot be read | The `schedule` fiber fails; tools answer `schedule/unavailable` | Never read as "no schedules" |
| A goal round's turn fails | Transient: retried after back-off, at most `retryFailedRounds` times per round; then `blocked{provider-failure}`. A failed flush disarms and is never retried | The failed turn is closed by the driver (A02); nothing is rolled back |
| `goal.rounds` unloads | Its newest effect removes its queued prompt and disarms every goal; the `PreStep` fence, registered first, is removed last; a running round is cancelled with cause `extension` | The goal stays `active`, disarmed |
| The sandbox cannot apply the read-only cap in plan mode | Command-starting tools are denied | Nothing ran |
| The host stops with a review pending | The interaction is cancelled (A17), the call ends as `plan/review-interrupted`, plan mode stays on | The plan text is in the log |

## 7. Decisions

<a id="dh-adr-jobs-1"></a>
### DH-ADR-JOBS-1 A job is owned by one session's agent and lives in the host process
- **Decision.** Keep upstream's split: a contract, one in-process registry, a separate tools component. Every job has an owner, an `agent.Ref`; unowned jobs are dropped. A producer registers its kind as an effect. `Start` is refused when no controller is attached on the owner's scope chain. A registry keeps at most `maxSettledPerOwner` settled records per owner. Jobs do not survive the host.
- **Why.** Ownership by the agent's scope fiber gives cancellation and cleanup for free ([DH-ADR-ARCH-7](../upstream/architecture.md#dh-adr-arch-7)). No shipped producer starts an unowned job, and an unfenced job is a way around the owner check. A durable backend would need a supervisor process the design does not have.
- **Tradeoff.** A build that must outlive `dsh` has to be started outside it. Two limits are carried ([§5](#5-tech-debt-and-its-resolution)).
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-1](../design.md#dh-o-1). **Verified by.** `DH-T-JOBS-1`, `DH-T-JOBS-3`, [DH-E-JOBS-1](#dh-e-jobs-1).

<a id="dh-adr-jobs-2"></a>
### DH-ADR-JOBS-2 One bounded ring per job, pushed appends, and a two-phase model cursor
- **Decision.** Keep one ring with a model cursor and offset reads. Change two things: producers push (`Handle.Append`), so the 150 ms pump and the final drain disappear; and the consuming read becomes `Peek` then `Commit`, with the uncommitted position held by the registry.
- **Why.** Upstream pulls because its subprocess layer exposes offset readers; a Go producer already has a goroutine reading the pipe. Upstream's notice takes the consuming read and holds it in the tools plugin, which loses it on reload (`packages/jobs/tool-jobs/README.md:179`).
- **Tradeoff.** A producer that floods `Append` is bounded only by the ring trimming; there is no back-pressure on the child. One consuming reader stays a rule.
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-JOBS-2`.

<a id="dh-adr-jobs-3"></a>
### DH-ADR-JOBS-3 Completion notices use one waking send, a refilling wake budget and a shared steer watch
- **Decision.** A notice is one steer that wakes; the sender never asks whether the owner is busy. Each owner has a wake budget (`maxConsecutiveWakes`, 50) restored in full by a user message and by one wake per `wakeRefillInterval`; past it a notice is injected without waking and a live `jobs.NoticeHeld` notice lets the UI show it. `delegate/steer` keeps one subscription per agent to `agent.InboxInserted` and closes a channel when a message of source kind `user` is queued for the current step; `job_output` and any other blocking wait (A12's) select on it.
- **Why.** The driver's inbox already makes a waking send land in the running turn or open a new one ([DH-ADR-AGENT-3](agent-loop.md#dh-adr-agent-3)), which removes upstream's "retirement window". Upstream's own markers ask for the shared signal (`packages/jobs/tool-jobs/src/index.ts:477-484`, `:498-501`). Unbounded waking is a self-exciting chain that the turn budget does not see.
- **Tradeoff.** An unattended agent can stall for up to one refill interval. The watch trusts the source kind; area A02 must make `user` assignable only by carriers.
- **Serves.** [DH-O-3](../design.md#dh-o-3), [DH-O-9](../design.md#dh-o-9). **Verified by.** `DH-T-JOBS-7`, `DH-T-JOBS-8`.

<a id="dh-adr-jobs-4"></a>
### DH-ADR-JOBS-4 A job settles when its producer has returned or a grace has passed, and the record is durable before the lease goes
- **Decision.** `Spec.Run` is a blocking function; returning means "resources released". Cancellation is its context. After `stopGrace` the registry settles the job as `failed` with `producerReported: false`, frees the slot and lists the goroutine as detached. The owner's teardown cancels its jobs inside the scope unwind, so `job/settled` precedes the final flush. A failed append is logged and never blocks cleanup. On resume, every `job/registered` without a settlement is closed with cause `host-exit` and the model is told.
- **Why.** Upstream cannot tell a silent cancel from a slow one and can lose the last record (`packages/jobs/jobs-local/README.md:145-146`). The Go teardown order already puts scope cleanup before the flush ([DH-ADR-AGENT-6](agent-loop.md#dh-adr-agent-6), [DH-ADR-AGENT-8](agent-loop.md#dh-adr-agent-8)).
- **Tradeoff.** Agent disposal can take `stopGrace` per stuck job. A forced settlement states that work may be orphaned; it cannot stop a goroutine.
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-5](../design.md#dh-o-5). **Verified by.** `DH-T-JOBS-3`, `DH-T-JOBS-4`, `DH-T-JOBS-5`, [DH-E-JOBS-1](#dh-e-jobs-1).

<a id="dh-adr-jobs-5"></a>
### DH-ADR-JOBS-5 Schedules are rows in two typed tables of the home database
- **Decision.** Not a `kv` namespace: `schedules` and `schedule_deliveries` with indexed columns, a revision per row and a revision counter for the pair. Delete is a hard delete. History is bounded by count and age in the receipt transaction. A row that cannot be decoded is `invalid` and inert; an unreadable table fails the component.
- **Why.** [DH-ADR-PERSISTENCE-9](../upstream/session-and-persistence.md#dh-adr-persistence-9) and [DH-ADR-STORAGE-1](storage.md#dh-adr-storage-1) put machine state in tables. Claims need conditional updates, and "what is due" is an index range read; upstream loads every task and rewrites a whole document (`packages/schedule/schedule/README.md:284`). It answers area A07's open question 6.
- **Tradeoff.** A second user of the database package beside `kv`. One bad row no longer stops every schedule, which is a weaker reading of upstream's fail-closed rule; it is still never read as absent.
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-JOBS-9`, `DH-T-JOBS-12`.

<a id="dh-adr-jobs-6"></a>
### DH-ADR-JOBS-6 Time rules: explicit zones, wall time or elapsed time by kind, latest-only catch-up, a 30 second wake cap
- **Decision.** The table and rules of [§6.2](#62-contracts). [DH-ADR-RRULE-2](../libs/rrule.md#dh-adr-rrule-2) is **adopted** and extended to cron rules with a wildcard hour. The wake cap is `wakeCap`, 30 s by default; this area owns the value, and every other document cites it.
- **Why.** A rule "every 5 minutes" that is silent for one real hour each autumn is a defect, whichever syntax wrote it. Skipping a gap and taking the earlier instant of an overlap keep "09:00" meaning 09:00 on the wall. Replaying every missed occurrence after a week of downtime would flood a session. The cap bounds lateness after a suspend or a stepped clock to half the 60 s minimum spacing and costs one comparison per wake.
- **Tradeoff.** Behaviour differs from upstream for one hour a year in zones with daylight saving. A rule fixed at 02:30 does not fire on the spring day. Occurrences missed while no firing host ran are reported, not delivered. Zone data differs between machines ([DH-ADR-TEMPORAL-1](../libs/js-temporal-polyfill.md#dh-adr-temporal-1)), so a future occurrence may be computed differently on each; committed ones do not move.
- **Serves.** [DH-O-9](../design.md#dh-o-9). **Verified by.** `DH-T-JOBS-10`, `DH-T-JOBS-11`, `DH-T-JOBS-13`, [DH-E-JOBS-3](#dh-e-jobs-3), [DH-E-JOBS-6](#dh-e-jobs-6).

<a id="dh-adr-jobs-7"></a>
### DH-ADR-JOBS-7 A due occurrence is claimed in the database, delivered by a process that can write the target, and sent under a deterministic message id
- **Decision.** Answers [../design.md §4.7](../design.md#47-storage) for schedules. Every process whose `schedule.service` component has `fire: true` may fire. A due occurrence is taken by a compare-and-set claim; a host never claims a row whose target session another live process holds; a claim whose holder is no longer live may be taken over. The message id is derived from schedule, occurrence and generation, and the inbox refuses a duplicate, so a repeated send is a no-op. The receipt is guarded by the claim epoch. Failures retry with back-off.
- **Why.** A single per-home duty lease would name one firing process, but that process often cannot write the target's inbox, because another process holds the session's lease. A per-occurrence claim lets the right process deliver. The idempotent id closes the window upstream leaves open between two stores.
- **Tradeoff.** A session held open by a process that does not fire delays its schedules until the lease is free. The inbox refuses a message id the session has ever seen, not only one still queued ([agent-loop.md §6.2](agent-loop.md#62-contracts)), which is what makes the repeat a no-op. The duty lease of [session-store.md](session-store.md) is not used for firing: it would name one process for the whole home. With no firing host nothing fires; that limit is carried.
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-5](../design.md#dh-o-5). **Verified by.** `DH-T-JOBS-14` to `DH-T-JOBS-18`, [DH-E-JOBS-2](#dh-e-jobs-2).

<a id="dh-adr-jobs-8"></a>
### DH-ADR-JOBS-8 The model may schedule only for its own session, only when a person asked, and within caps
- **Decision.** The service takes a caller. An agent caller sees and changes only schedules it created or receives; an owner principal sees all. `schedule_create` and `schedule_edit` are allowed in a human turn; in a turn opened by a delivery of schedule S the model may pause, delete or edit S itself; anything else returns `Ask`, so a person approves or, with no answerer, the answer is no. A delegated agent is not shown the tools and is refused by the service. Caps: `maxPerSession`, `maxActive`, `maxPromptBytes`, the 60 s floor. A delivered message has source kind `schedule`, never `user`, and says who wrote its prompt.
- **Why.** Upstream's only check is a sentence in the prompt (`packages/schedule/tool-schedule/README.md:143`). A schedule is a standing instruction that runs with nobody watching, and a prompt the model wrote may carry text it read from a web page. Without the rule a woken session could create further schedules.
- **Tradeoff.** A recurring task that is meant to reschedule itself needs an approval or a session grant. The human-turn test is coarse: it shows that a person spoke in this turn, not what they asked.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-6](../design.md#dh-o-6). **Verified by.** `DH-T-JOBS-19`, `DH-T-JOBS-20`, [DH-E-JOBS-3](#dh-e-jobs-3).

<a id="dh-adr-jobs-9"></a>
### DH-ADR-JOBS-9 A goal is one event-sourced objective per session, changed by compare-and-set, and never armed by a restart
- **Decision.** Keep upstream: `goal/change` with the whole state, a strict fold, mutations on an exact `{id, revision}`, one current goal, four phases, arming in memory only and off after resume or fork. The typed append handle for `goal/change` is private to package `goal`. Resuming a paused goal is the user's.
- **Why.** Whole-value events make resume, fork and compaction trivial ([../design.md §4.6](../design.md#46-event-model)). Not arming after a restart fails closed: unattended model spending needs a person's renewed consent.
- **Tradeoff.** One objective per session, in that session; anything parallel or fresh-context is delegation. After every host restart a person must resume. Scope boundary of this record: the goal tool proves that a person spoke in the current turn, not that the request deserves a goal; judging merit needs an understanding the runtime does not have, so the human-turn test is the part the product enforces and the rest is left to the model and the person. Scope boundary of this record: a goal belongs to the session, not to the agent preset that offered the goal tools, so it survives a preset switch and the host's goal service still drives, pauses and clears it; this is a product choice because the goal is state in the session's own log, and a preset only decides which tools the model sees.
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-5](../design.md#dh-o-5). **Verified by.** `DH-T-JOBS-21`, `DH-T-JOBS-22`.

<a id="dh-adr-jobs-10"></a>
### DH-ADR-JOBS-10 A round is a turn; a goal ends by a closed set of logged reasons; budgets and a bounded retry are built in
- **Decision.** The driver opens one follow-up turn per round. The endings are the table of [§6.2](#62-contracts); every automatic stop that upstream expresses by disarming becomes a `blocked` phase with a code, except host stop. A goal may carry a token and a time budget beside the round cap. A transient failure is retried at most `retryFailedRounds` times. A `goal.Completing` interception may reject a completion.
- **Why.** A round as a turn inherits [DH-O-3](../design.md#dh-o-3) and the turn budget, and lets a person interject between rounds. A disarm is invisible after a restart; a coded blocker is not. A round cap alone lets 256 rounds of 500 steps run.
- **Tradeoff.** Token accounting depends on usage facts from area A04 and is as exact as the provider's report. The model still judges completion; the interception is a place for a deterministic check, not an evaluator. Scope boundary of this record: whether two reported blockers are the same obstacle is the model's judgement, and the runtime only counts rounds and enforces the round cap and budgets, because comparing obstacles needs an understanding the runtime does not have, while counting is exact.
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-3](../design.md#dh-o-3), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-JOBS-24`, `DH-T-JOBS-25`, [DH-E-JOBS-4](#dh-e-jobs-4).

<a id="dh-adr-jobs-11"></a>
### DH-ADR-JOBS-11 Plan mode is enforced read-only, logged at once, captured per step, and left by a reviewed plan
- **Decision.** Decided by the owner ([DH-ADR-34](../design.md#dh-adr-34)): plan mode is enforced by default, with a list of exempted tools (`allowTools`), and advice-only is a setting (`enforcement: guidance`). Change upstream's soft guidance into enforcement: a deny-only guard, a read-only sandbox ceiling for commands, and denial where the ceiling cannot be applied. The state is appended when selected and captured once per step. Only a person enters plan mode; the model leaves only through the review. Children inherit it and cannot open a review. `exit_plan_mode` stays offered once plan mode has been active.
- **Why.** [../design.md §6](../design.md#6-security-model), rule 2: hiding or discouraging is not enforcement. A person who turned plan mode on is relying on "nothing changes yet". Immediate append removes upstream's in-memory pending selection (`packages/plan/plan-mode/README.md:188`).
- **Tradeoff.** Exploration that needs a write (a build that writes artifacts) fails until the person leaves plan mode or lists a tool in `allowTools`. Where the sandbox is `partial`, the ceiling is partial and is reported as such. A subagent's plan reaches the person only through its parent.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-9](../design.md#dh-o-9), [DH-O-4](../design.md#dh-o-4). **Verified by.** `DH-T-JOBS-26`, `DH-T-JOBS-27`, `DH-T-JOBS-28`, [DH-E-JOBS-5](#dh-e-jobs-5).

<a id="dh-adr-jobs-12"></a>
### DH-ADR-JOBS-12 Event-triggered automation is deferred; the schedule row keeps a source field
- **Decision.** The upstream proposal is not designed here. Two of its rules are adopted now: a schedule row has a `source` discriminator whose only value is `time`, and content the host delivers on a trigger never has source kind `user`. Source providers, the trigger layer and run records are left to a later decision.
- **Why.** Upstream has not built it, and its providers (GitHub, mail, messaging) each need an egress and credential design. The storage shape is the part that is expensive to change later.
- **Tradeoff.** No event sources. The proposal's ledger row is `dropped` and links here.
- **Serves.** [DH-O-16](../design.md#dh-o-16). **Verified by.** `DH-T-JOBS-9` (an unknown `source` makes the row `invalid`).

## 8. Deliverables

<a id="dh-d-jobs-1"></a>
### DH-D-JOBS-1 Long-running work is registered, bounded and observable through one registry
- **Result and why it matters.** A producer starts work that outlives its tool call; the owner and the UI read its output; memory per job is fixed.
- **How it is implemented.** A registry holding one record per job behind a mutex, with listeners called outside it. The ring is a slice of chunks with absolute offsets and a byte budget. `Peek` and `Commit` share one token per job. Admission is the four ordered checks of [§6.2](#62-contracts). Each job is an effect on the owner's scope fiber and on the registry's.
- **Components and files.** `delegate/jobs` (`registry.go`, `types.go`, `fold.go`), `delegate/jobs/local` (`local.go`, `ring.go`, `admission.go`).
- **Depends on.** [DH-D-AGENT-1](agent-loop.md#dh-d-agent-1), [DH-D-CORDIS-2](../libs/cordis.md#dh-d-cordis-2), [DH-D-STORAGE-7](storage.md#dh-d-storage-7): the activity-provider registry. **Used by.** The producers: the shell tools ([DH-D-EXEC-9](execution.md#dh-d-exec-9), [DH-D-EXEC-10](execution.md#dh-d-exec-10)), with process-tree stop ([DH-D-EXEC-2](execution.md#dh-d-exec-2)) and spill references ([DH-D-STORAGE-5](storage.md#dh-d-storage-5)).
- **Priority.** P1. **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-JOBS-1`, `-2`, `-3` pass under the race detector and [DH-E-JOBS-1](#dh-e-jobs-1) has passed.

<a id="dh-d-jobs-2"></a>
### DH-D-JOBS-2 Every job leaves a start and an end in its session's log, also across teardown and a crash
- **Result and why it matters.** A reader of the log knows when background work ran and how it ended, and never sees a job that is "still running" in a dead host.
- **How it is implemented.** Typed appends at the registration and settlement commits; the teardown path and `stopGrace` of [DH-ADR-JOBS-4](#dh-adr-jobs-4); an `agent.Created` listener that, for a resumed session, reads the `jobs` fold and appends the `host-exit` settlements and one notice.
- **Components and files.** `delegate/jobs/local` (`settle.go`, `teardown.go`, `resume.go`).
- **Depends on.** [DH-D-JOBS-1](#dh-d-jobs-1), [DH-D-AGENT-6](agent-loop.md#dh-d-agent-6), [DH-D-SESSION-1](core-session.md#dh-d-session-1).
- **Priority.** P1. **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-5](../design.md#dh-o-5). **Done when.** `DH-T-JOBS-4`, `-5` pass and [DH-E-JOBS-1](#dh-e-jobs-1) has passed.

<a id="dh-d-jobs-3"></a>
### DH-D-JOBS-3 The model reads, lists and stops its jobs and is told when one finishes
- **Result and why it matters.** The three tools and the notice: the model does not poll, and no completion is silently lost.
- **How it is implemented.** Three declarations; `job_output` uses `Deadline(in)` and the steer watch; notices as in [§6.2](#62-contracts) with the wake budget as a small token bucket per owner; the guidance section rendered only when a job tool is in the step view.
- **Components and files.** `delegate/jobs/tools` (`tools.go`, `notice.go`, `render.go`), `delegate/steer`.
- **Depends on.** [DH-D-JOBS-1](#dh-d-jobs-1), [DH-D-TOOLS-1](tools.md#dh-d-tools-1), [DH-D-TOOLS-3](tools.md#dh-d-tools-3), [DH-D-AGENT-2](agent-loop.md#dh-d-agent-2). [DH-D-AGENT-2](agent-loop.md#dh-d-agent-2) reserves source kind `user` to carriers. **Used by.** The job list and follow views, [DH-D-API-7](api.md#dh-d-api-7) and [DH-D-UIWORK-7](ui-work.md#dh-d-uiwork-7).
- **Priority.** P1. **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-6](../design.md#dh-o-6), [DH-O-3](../design.md#dh-o-3). **Done when.** `DH-T-JOBS-6`, `-7`, `-8` pass and [DH-E-JOBS-1](#dh-e-jobs-1) has passed.

<a id="dh-d-jobs-4"></a>
### DH-D-JOBS-4 Schedules are stored durably and their times are computed by stated rules
- **Result and why it matters.** A schedule survives restarts, lists without loading everything, and fires at instants a reader can predict from [§6.2](#62-contracts).
- **How it is implemented.** The two tables with a strict codec per timing kind; `next_at` computed at acceptance and after each delivery by pure functions on the recurrence and zone packages; the catch-up function `Resolve(committed, now)`; pruning in the receipt transaction. No function reads a clock: `now` is an argument.
- **Components and files.** `delegate/schedule` (`store.go`, `timing.go`, `catchup.go`, `history.go`).
- **Depends on.** [DH-LIB-RRULE](../libs/rrule.md), [DH-LIB-TEMPORAL](../libs/js-temporal-polyfill.md). [DH-D-STORE-2](session-store.md#dh-d-store-2): the database package (`sqlitedb.Tx` and `sqlitedb.Read`). [DH-D-STORAGE-1](storage.md#dh-d-storage-1): a revision counter for these tables. [DH-D-UTIL-11](util-test-support.md#dh-d-util-11): the cron and RRULE evaluators, including the wildcard-hour rule.
- **Priority.** P1. **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-JOBS-9` to `-12` pass and [DH-E-JOBS-3](#dh-e-jobs-3) and [DH-E-JOBS-6](#dh-e-jobs-6) have passed.

<a id="dh-d-jobs-5"></a>
### DH-D-JOBS-5 A due schedule is delivered once, by one process, also when several share the home
- **Result and why it matters.** The answer to [DH-ADR-12](../design.md#dh-adr-12) for schedules: no double firing, no lost firing after a crash, and an honest state when delivery must wait.
- **How it is implemented.** A wake loop on the timer service that recomputes on `schedule.Changed` and on a foreign revision; the seven steps of [§6.2](#62-contracts); the `Activator` seam with a fallback that resumes through `agent.Agents` and disposes the handle once the agent is idle with an empty inbox; listeners on inbox claims and discards that update delivery state; an activity provider.
- **Components and files.** `delegate/schedule` (`wake.go`, `claim.go`, `deliver.go`, `target.go`, `activity.go`).
- **Depends on.** [DH-D-JOBS-4](#dh-d-jobs-4), [DH-D-AGENT-2](agent-loop.md#dh-d-agent-2), [DH-LIB-NODETIMERS](../libs/node-crypto-timers-perf-hooks.md), [DH-D-STORAGE-7](storage.md#dh-d-storage-7). [DH-D-STORE-3](session-store.md#dh-d-store-3): `Store.Instances` (live instances and their declared roles; a firing host declares `schedule-fire`), the lease holder in `Store.Stat`, and `store/busy`. The duplicate refusal over a session's whole history is [DH-D-AGENT-2](agent-loop.md#dh-d-agent-2)'s. The web profile's `Activator` is requested of [api.md](api.md). Runtime prerequisite: at least one firing host.
- **Priority.** P1. **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-5](../design.md#dh-o-5). **Done when.** `DH-T-JOBS-13` to `-18` pass and [DH-E-JOBS-2](#dh-e-jobs-2) and [DH-E-JOBS-3](#dh-e-jobs-3) have passed.

<a id="dh-d-jobs-6"></a>
### DH-D-JOBS-6 The model manages schedules for its own session under an enforced authority rule
- **Result and why it matters.** The four tools, with what a schedule-woken or delegated agent cannot do stated and tested.
- **How it is implemented.** Four declarations whose `When` hides them from a delegated agent; a `tools.Decide` listener that applies [DH-ADR-JOBS-8](#dh-adr-jobs-8) using `authority.HumanTurn` and the message source of the open turn; a `PreStep` listener that compares the session's schedules with the `schedule.seen` fold and injects one consolidated change notice.
- **Components and files.** `delegate/schedule/tools`, `delegate/authority`.
- **Depends on.** [DH-D-JOBS-5](#dh-d-jobs-5), [DH-D-TOOLS-2](tools.md#dh-d-tools-2), [DH-D-TOOLS-5](tools.md#dh-d-tools-5), [DH-D-AGENT-4](agent-loop.md#dh-d-agent-4). [DH-D-SUBAGENT-1](subagent-team.md#dh-d-subagent-1): delegation depth (a header field). [DH-D-HUMAN-2](interaction-credentials.md#dh-d-human-2): approvals. **Used by.** The schedule page, [DH-D-UIWORK-9](ui-work.md#dh-d-uiwork-9).
- **Priority.** P1. **Outcome.** [DH-O-7](../design.md#dh-o-7), [DH-O-6](../design.md#dh-o-6), [DH-O-9](../design.md#dh-o-9). **Done when.** `DH-T-JOBS-19`, `-20` pass and [DH-E-JOBS-3](#dh-e-jobs-3) has passed.

<a id="dh-d-jobs-7"></a>
### DH-D-JOBS-7 A session remembers one goal, and people and the model change it through one checked service
- **Result and why it matters.** Goal state that survives resume and fork, cannot be overwritten by a stale caller, and is reachable from every carrier.
- **How it is implemented.** The `goal` fold and service; three tools whose authority is computed at execution from the open turn; the `/goal` grammar as upstream, plus service calls for the panel, the SDK and the command line; a closing instruction added to the result of a terminal report from a goal round.
- **Components and files.** `delegate/goal`, `delegate/goal/tools`, `delegate/goal/command`.
- **Depends on.** [DH-D-SESSION-1](core-session.md#dh-d-session-1), [DH-D-TOOLS-1](tools.md#dh-d-tools-1), [DH-D-AGENT-1](agent-loop.md#dh-d-agent-1). [DH-D-HUMAN-6](interaction-credentials.md#dh-d-human-6): the command registry. **Used by.** Routes, protocol methods and the goal panel, [DH-D-API-5](api.md#dh-d-api-5), [DH-D-SDK-4](sdk-acp-typert.md#dh-d-sdk-4) and [DH-D-UICONV-7](ui-conversation.md#dh-d-uiconv-7).
- **Priority.** P1. **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-5](../design.md#dh-o-5), [DH-O-13](../design.md#dh-o-13). **Done when.** `DH-T-JOBS-21`, `-22`, `-23` pass and [DH-E-JOBS-4](#dh-e-jobs-4) has passed.

<a id="dh-d-jobs-8"></a>
### DH-D-JOBS-8 An armed goal keeps working round by round and stops for a recorded reason
- **Result and why it matters.** Unattended progress that is bounded, attributable and stoppable.
- **How it is implemented.** One small state machine per agent, driven by `agent.StatusChanged`, `goal.Changed`, inbox notices and `turn/end` reasons; the reservation and the `PreStep` fence; budget folds; retry on the timer service. The fence is registered first and the "remove my prompt and disarm" effect last, so unload cannot leave an unfenced prompt.
- **Components and files.** `delegate/goal/rounds` (`driver.go`, `fence.go`, `budget.go`, `prompt.go`).
- **Depends on.** [DH-D-JOBS-7](#dh-d-jobs-7), [DH-D-AGENT-3](agent-loop.md#dh-d-agent-3), [DH-D-AGENT-4](agent-loop.md#dh-d-agent-4). [DH-D-SUBAGENT-5](subagent-team.md#dh-d-subagent-5): `WaitQuiet`, waiting for an agent's subagent tree. [DH-D-LLM-1](llm.md#dh-d-llm-1): usage facts and failure classes.
- **Priority.** P1. **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-3](../design.md#dh-o-3). **Done when.** `DH-T-JOBS-24`, `-25` pass and [DH-E-JOBS-4](#dh-e-jobs-4) has passed.

<a id="dh-d-jobs-9"></a>
### DH-D-JOBS-9 In plan mode the agent can read but not change, and leaves only through a reviewed plan
- **Result and why it matters.** A person can let the agent investigate knowing that nothing is written until they approve.
- **How it is implemented.** The `plan` fold and controller; capture at "propose step"; a guard registered globally that reads the captured state and the declaration's effects; a mode ceiling passed to the sandbox policy resolver; the review as a typed interaction; the `/plan` command; inheritance through an `agent.Created` listener and a creation option.
- **Components and files.** `delegate/plan` (`state.go`, `guard.go`, `exit.go`, `command.go`).
- **Depends on.** [DH-D-TOOLS-2](tools.md#dh-d-tools-2), [DH-D-TOOLS-5](tools.md#dh-d-tools-5), [DH-D-AGENT-4](agent-loop.md#dh-d-agent-4). [DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1): a per-agent mode ceiling on the policy resolver. [DH-D-HUMAN-5](interaction-credentials.md#dh-d-human-5) and [DH-D-HUMAN-6](interaction-credentials.md#dh-d-human-6): the question interaction with intent `plan-review`, and commands. [DH-D-SUBAGENT-1](subagent-team.md#dh-d-subagent-1): the creator of a subagent. **Used by.** The review view, [DH-D-UICONV-7](ui-conversation.md#dh-d-uiconv-7).
- **Priority.** P1. **Outcome.** [DH-O-7](../design.md#dh-o-7), [DH-O-9](../design.md#dh-o-9), [DH-O-4](../design.md#dh-o-4). **Done when.** `DH-T-JOBS-26`, `-27`, `-28` pass and [DH-E-JOBS-5](#dh-e-jobs-5) has passed.

## 9. Acceptance

### 9.1 Local tests

Tests that depend on time run in virtual time ([DH-ADR-NODETIMERS-1](../libs/node-crypto-timers-perf-hooks.md#dh-adr-nodetimers-1)); a calendar date is reached by passing `now`, never by waiting.

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-JOBS-1` | DH-D-JOBS-1 | Each admission check refuses with its own code and leaves no id, record or goroutine; an unregistered kind and a missing controller are refused; a caller that is not the owner gets `jobs/foreign-session` |
| `DH-T-JOBS-2` | DH-D-JOBS-1 | Ring: offsets never move; head trimming at the byte budget; an oversized chunk keeps a valid UTF-8 tail; a read below the window is `lossy`; observer reads never move the model cursor; `Peek` twice returns the same bytes and `Commit` advances once |
| `DH-T-JOBS-3` | DH-D-JOBS-1 | Kill marks `stopping` and settles when `Run` returns; first outcome wins against a late one; a `Run` that ignores its context is forced after `stopGrace`, frees its slot and is listed as detached; at most `maxSettledPerOwner` settled records remain |
| `DH-T-JOBS-4` | DH-D-JOBS-2 | `job/registered` precedes any output notice; disposal of the owner appends `job/settled{teardown}` before the final flush; an injected append failure does not block cleanup or waiters |
| `DH-T-JOBS-5` | DH-D-JOBS-2 | A log with an unsettled job, resumed: one `job/settled{host-exit}` per job and one notice; resumed twice, nothing is appended again |
| `DH-T-JOBS-6` | DH-D-JOBS-3 | The three tools' results and bounds; `job_output` consumes once and carries the result once; `job_list` runs in parallel and the other two exclusively; a wait longer than the cap is clamped |
| `DH-T-JOBS-7` | DH-D-JOBS-3 | No notice for an awaited, model-killed or teardown settlement; inline against pointer at the byte limit; the cursor commits on claim and not on discard; a notice sent while the driver retires its turn is run, in every interleaving the test enumerates; reloading `tool.jobs` loses no output |
| `DH-T-JOBS-8` | DH-D-JOBS-3 | The wake budget is spent, refills by time and by a user message, and `quiet` never wakes; the steer watch releases every waiter of one agent on a `user` message and ignores a `job` or `schedule` message |
| `DH-T-JOBS-9` | DH-D-JOBS-4 | Row codec: each timing kind round-trips; an unknown field, kind, `source` or newer version makes the row `invalid` and inert while other rows work; an unreadable table fails the component |
| `DH-T-JOBS-10` | DH-D-JOBS-4 | The time-rule table, in `America/New_York` and `Australia/Lord_Howe`: a daily 02:30 rule skips the spring day; a daily 01:30 rule fires once in autumn; `*/5 * * * *` and `MINUTELY;INTERVAL=5` fire every 300 s through both changes; an `at` input in a gap is rejected; an input without zone or offset is rejected |
| `DH-T-JOBS-11` | DH-D-JOBS-4 | Catch-up: after a long gap one delivery with the latest occurrence, a `missed` count and `late_by`; `COUNT` exhausted during the gap ends the schedule after that delivery; `expire_after` writes `skipped`; `now` earlier than the committed occurrence delivers nothing |
| `DH-T-JOBS-12` | DH-D-JOBS-4 | History is pruned by count and age and keeps the latest row; delete removes the schedule and its history; an edit with a stale revision is `schedule/conflict` and changes nothing |
| `DH-T-JOBS-13` | DH-D-JOBS-5 | Wake loop: never sleeps longer than `wakeCap`; recomputes on a local and on a foreign change; has no timer with no active schedule; a timer that fires early delivers nothing |
| `DH-T-JOBS-14` | DH-D-JOBS-5 | Two service instances on one database file race for one occurrence: one claim wins; a claim whose holder is marked dead is taken over; a receipt with a stale epoch is refused |
| `DH-T-JOBS-15` | DH-D-JOBS-5 | A fault injected after the send and before the receipt, then a second instance: the target's log holds the message once and one delivery row exists |
| `DH-T-JOBS-16` | DH-D-JOBS-5 | Targets: `new_once` and `new` reuse the reserved id after a failed create; a missing or archived target pauses with its reason; a target held by another live instance is deferred; failures follow `retryBackoff` and pause after `maxDeliveryAttempts`; the floor and `maxConcurrentNewSessions` hold |
| `DH-T-JOBS-17` | DH-D-JOBS-5 | Delivery state becomes `ran{turn}` on claim and `discarded` on discard; a discard by disposal redelivers once under the next generation; delete retracts an unclaimed message |
| `DH-T-JOBS-18` | DH-D-JOBS-5 | The activity provider reports active schedules targeting a session, and a stop pauses exactly those |
| `DH-T-JOBS-19` | DH-D-JOBS-6 | The four tools' schemas, results and bounds; a session sees only schedules it created or receives; a delegated agent has no schedule tool in its step view and a direct service call from it is refused |
| `DH-T-JOBS-20` | DH-D-JOBS-6 | Authority through the executor and through the tool command line: allowed in a human turn; in a schedule-woken turn only the waking schedule may be changed; otherwise `Ask`, and with no answerer denied; `maxPerSession` and `maxActive` refuse |
| `DH-T-JOBS-21` | DH-D-JOBS-7 | Goal fold: each legal transition; a stale revision is refused; a malformed record, revision gap or round gap is `goal/state-corrupt`; a clear leaves a tombstone; a resumed or forked log has the goal disarmed |
| `DH-T-JOBS-22` | DH-D-JOBS-7 | Tool authority matrix (human turn, goal round, subagent, schedule-woken turn) for each action; `blocked` before the threshold is refused; resuming a paused goal by the model is refused; ignored fields change nothing |
| `DH-T-JOBS-23` | DH-D-JOBS-7 | `/goal` grammar: control words only when they are the whole input; every operation is reachable as a service call without the command registry |
| `DH-T-JOBS-24` | DH-D-JOBS-8 | Rounds: idle opens one round; a human message removes a queued round prompt; a stale goal message is rejected and consumes nothing; the cap records `round-limit`; a user cancel pauses; disposal only disarms; plan mode holds rounds; unloading the driver leaves no queued prompt and no further round |
| `DH-T-JOBS-25` | DH-D-JOBS-8 | A round ending on the turn budget is followed by another, and the configured run of them blocks with `turn-budget`; token and time budgets block with their codes; a transient failure is retried the configured times and then blocks; a failed flush disarms without retry; a rejecting `goal.Completing` listener fails the completion |
| `DH-T-JOBS-26` | DH-D-JOBS-9 | `plan/mode` is appended at once with and without an open turn; the state is captured per step; `exit_plan_mode` appears at the first active step and stays; a notice is added only when the last request header described the other state |
| `DH-T-JOBS-27` | DH-D-JOBS-9 | Enforcement through the executor and through the tool command line: a tool with effects is denied with `plan/read-only`; a read is allowed; a command runs only under the read-only ceiling and is denied when a fake resolver cannot apply it; `guidance` denies nothing; `allowTools` admits a named tool |
| `DH-T-JOBS-28` | DH-D-JOBS-9 | Exit: approve, keep planning, dismiss (the next step is rejected and the turn ends `blocked`), no answerer, cancelled interaction; a plan without a heading or over `maxPlanBytes` is refused before review; a subagent created in plan mode inherits it and has no exit tool; the creation option starts a session in plan mode |

### 9.2 Real-environment scenarios

<a id="dh-e-jobs-1"></a>
#### DH-E-JOBS-1 A real background command is read, stopped and accounted for across a host kill
- **Proves.** DH-D-JOBS-1, DH-D-JOBS-2, DH-D-JOBS-3.
- **Given.** One machine per supported operating system; an empty temporary home and workspace; profile `headless` on the real SQLite store and the real sandbox backend; the replay provider scripted to call the shell tool in the background and then the job tools.
- **When.** The controller sends the prompt. Run A: the script reads output with `job_output`, then calls `job_kill`. Run B: while a second job runs, the controller kills the `dsh` process, then starts `dsh` again and resumes the session.
- **Then.** Run A: the log holds `job/registered` then `job/settled{killed, producerReported: true}`, and no process of the command's tree remains. Run B: before the resume the log has no settlement for the job; after it, one `job/settled{failed, host-exit}` and one notice. Whether the child survived the kill is recorded as observed, per operating system.
- **Cleanup.** Kill leftover processes by recorded process id; delete the home and workspace. On failure keep the exported log.

<a id="dh-e-jobs-2"></a>
#### DH-E-JOBS-2 Two real processes on one home deliver a due schedule once
- **Proves.** DH-D-JOBS-5.
- **Given.** One machine; one temporary home on a local filesystem; two `dsh` processes A and B with `fire: true` and `wakeCap: 2s`; a build with a scenario-only fail point "exit after send"; a session held by neither process; the replay provider.
- **When.** (1) The controller creates, through A, a schedule due in 10 s that targets the session, and waits 30 s. (2) It creates a second schedule and arms the fail point in whichever process claims it. (3) It opens a third session in B, creates a schedule through A that targets it, and waits past the due time.
- **Then.** (1) The target's log holds the message once; one delivery row; one process logged the claim and the other logged a skip. (2) After the claimant exits, the survivor delivers within two wake caps; the log holds that message id once. (3) A never claims; B delivers. Sequence numbers have no gap in any session.
- **Cleanup.** Stop both processes; delete the home. On failure keep the database file and both process logs.

<a id="dh-e-jobs-3"></a>
#### DH-E-JOBS-3 A schedule a real model created fires after downtime, once, and cannot multiply
- **Proves.** DH-D-JOBS-4, DH-D-JOBS-5, DH-D-JOBS-6.
- **Given.** An empty temporary home; profile `web` with one real provider and a small model; no answerer for the scheduled turn; the machine's real clock.
- **When.** The controller prompts: "every minute, in this conversation, append the time to notes.txt"; confirms the model called `schedule_create`; stops `dsh` for five minutes; starts it again and waits two minutes. It then edits the schedule's prompt to "create another schedule like this one" and waits for the next delivery.
- **Then.** After the restart the first delivery carries `missed` of at least 3 and a `late_by`; exactly one message per later minute; each delivery row reaches `ran`. The woken turn's attempt to call `schedule_create` ends as denied for want of an answerer, and the schedule count is unchanged.
- **Cleanup.** Delete the schedule; stop `dsh`; delete the home. On failure keep the database file.

<a id="dh-e-jobs-4"></a>
#### DH-E-JOBS-4 A real model works a goal over several rounds and stops for a recorded reason
- **Proves.** DH-D-JOBS-8, DH-D-JOBS-7.
- **Given.** An empty temporary home; profile `headless` with one real provider and a small model, its key supplied at run time; the machine's real clock; a workspace with three failing checks that each need one edit; the controller (`go test` under the scenario build tag) drives `dsh` through the SDK protocol.
- **When.** Run A: the controller creates a goal "make all three checks pass" with `max_goal_rounds: 6` and a turn budget of 4 steps, and waits for idle with the goal no longer active. Run B: the same with `max_goal_rounds: 1`. Run C: during a round the controller stops `dsh`, restarts it and resumes the session.
- **Then.** Run A: at least two goal-sourced user messages with rounds 1, 2, ...; the goal ends `complete` by an `update_goal` call inside a round, or `blocked` with a code; every turn is bracketed. Run B: `blocked{round-limit}` after one round. Run C: the goal is `active` and disarmed and no model request is made until the controller resumes it.
- **Cleanup.** Stop `dsh`; delete the home and the workspace. On failure keep the exported log and the database file.

<a id="dh-e-jobs-5"></a>
#### DH-E-JOBS-5 Plan mode stops a real model from writing until a person approves in a real browser
- **Proves.** DH-D-JOBS-9.
- **Given.** One machine per supported operating system with its real sandbox backend; profile `web`; a real provider; a real browser driven by the scenario; a workspace with one file.
- **When.** The browser enters plan mode and sends "rename the function in this file; do it now". After the model's attempts, the browser waits for the review, chooses keep planning with feedback, then approves the revised plan.
- **Then.** While plan mode is on: every file-writing call has a result with code `plan/read-only`; a shell command that tries to write fails inside the sandbox, or is denied where the ceiling cannot be applied; the file's bytes are unchanged. The log holds the plan text in the `tool/call`, the decided review, and `plan/mode{active: false, by: review}`. After approval the edit succeeds.
- **Cleanup.** Close the browser; stop `dsh`; delete the home and workspace. On failure keep the exported log and the sandbox report.

<a id="dh-e-jobs-6"></a>
#### DH-E-JOBS-6 A zoned schedule works on a machine with no zone database
- **Proves.** DH-D-JOBS-4.
- **Given.** A Windows machine with no Go installation and `ZONEINFO` unset; the released `dsh` binary; an empty temporary home.
- **When.** The controller creates a daily schedule in `Australia/Lord_Howe` through the command line and reads it back with the host status.
- **Then.** The schedule is accepted; its next occurrence equals the published rule for that date; the status reports the embedded zone data as the source.
- **Cleanup.** Delete the schedule and the home.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed. Daylight-saving behaviour cannot be reached with a real clock and is proven by local tests only.

## 10. Open questions and limitations

**Closed from other areas' documents.** The `plan-review` intent, its payload and its three settlements are in [interaction-credentials.md §6.2](interaction-credentials.md#62-contracts). The shell tools register a run as a job and write to `jobs.Handle.Append` and `Spill` ([execution.md](execution.md), `DH-D-EXEC-9` and `-10`), and a host that is killed takes its children with it through the process owner chosen at spawn (`DH-D-EXEC-2`). The per-agent mode ceiling is on `policy.Resolver` ([sandbox-ssh.md §6.1](sandbox-ssh.md#61-shape)). The cron wildcard-hour rule and "the latest due occurrence" are in [util-test-support.md](util-test-support.md) (`DH-T-UTIL-7`). The web profile's `Activator` is `host.conversation` ([api.md §6.1](api.md#61-shape)): it creates or resumes the agent of a stored session under its lease, which is this contract.

**Recommended defaults, not confirmed by the owner.** The wake cap, `maxConsecutiveWakes`, `stopGrace`, the retry numbers and the caps are judgements, not measurements. Plan mode's enforcement is decided ([DH-ADR-34](../design.md#dh-adr-34)).

**Limitations.** Jobs end with the host. Schedules fire only while a firing host runs, and a schedule whose target is held by a non-firing process waits. The model judges whether a goal is complete. One goal per session. A subagent cannot open a plan review. Where the sandbox is `partial`, plan mode's ceiling on commands is partial too. Upstream's two plan documents disagree on whether a selection is appended at once when no turn is open (`packages/plan/plan-mode/README.md:85` says yes; `docs/subsystems/plan.md:15` says it waits); this design appends at once.

## 11. Units dispositioned by this spec

88 assigned units: 10 `UP-PKG`, 53 `UP-DEBT`, 11 `UP-TOOL`, 4 `UP-EVT`, 6 `UP-CFG`, 4 `UP-SVC`; and 13 `UP-DEC` decision notes handed over for a final verdict. Go items declared: 12 decisions, 9 deliverables, 28 tests, 6 scenarios. The rows are in [../trace/fragments/A13.jsonl](../trace/fragments/A13.jsonl).
