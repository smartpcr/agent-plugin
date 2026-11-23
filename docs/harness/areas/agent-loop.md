# Agent interface, registry and agent loop: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md) and are cited as `path:line` relative to the upstream root.
> Statements that are reasoning and not read from source open with "Inference". Sections 6
> to 9 are design throughout. Nothing here has been built, run or tested. IDs in this
> document use the area key `AGENT`.

| | |
|---|---|
| **Area key** | `AGENT` (agent `A02`) |
| **Upstream packages** | 4, all in group `core`: `agent`, `agent-loop`, `agent-default-model`, `agent-tool-presentation` |
| **Serves outcomes** | [DH-O-3](../design.md#dh-o-3) (this area owns it), [DH-O-4](../design.md#dh-o-4), [DH-O-1](../design.md#dh-o-1), [DH-O-5](../design.md#dh-o-5), [DH-O-8](../design.md#dh-o-8), [DH-O-2](../design.md#dh-o-2) |
| **Depends on areas** | A01 (session log, event types, request construction), A05 (lease, flush), A03 (tool set, pipeline, prompt assembly), A21 (`config.Service.Apply`), A04 (model call, failure facts, retry policy), A14 (compaction listeners), A27 (presets), A07 (workspace archive admission) |
| **Priority** | P0: no turn can run without it |

## 1. Purpose

This area is the part of the harness that turns "a message arrived" into "the model
answered". It gives every other component one object to talk to, the **agent**; keeps the
list of agents that are alive; and runs the loop that asks the model, runs the tools the
model asks for, and repeats. It also decides which model a step uses.

Without it there is a session log, a tool registry and a model client, and nothing that
connects them. Its promise to the rest of the product is outcome
[DH-O-3](../design.md#dh-o-3): one accepted message is at most one turn; turns and steps
are bracketed in the log; cancellation reaches every operation and its cause is recorded;
a failing extension ends the turn and never the loop; and no tool call is left unanswered
in history.

## 2. Vocabulary

Terms from [../design.md §2](../design.md#2-vocabulary) (session, session event, surface,
scope, lease, projection, fiber, effect) keep their meaning.

| Term | What it is | Why it exists |
|---|---|---|
| **Agent** | The live object that drives one session. It has the same identifier as its session. | The one contract a browser, a script, a hook and a parent agent all program against. |
| **Agent handle** | The agent plus the right to dispose it. Only the creator holds it. | Ownership (who releases the agent) is separate from access (who may send to it). |
| **Registry** | The service that lists live agents and offers create, resume and lookup. | Callers find agents without importing the code that drives them. |
| **Factory seat** | The one place in the registry where a driver registers how agents are built. | The driver can be replaced without changing any caller. |
| **Driver** | The loop that runs turns and steps for one agent. | It is the only writer of turn and step state. |
| **Inbox** | The agent's pending input: a `next-turn` list (prompts, one turn each) and a `next-step` list (input for the nearest model request). It is stored in the session log. | Input arrives at any time; the driver takes it only at defined points. |
| **Follow-up, steer, inject** | Three ways to send. A follow-up queues a prompt for its own turn and wakes the driver. Steering goes to the nearest step and wakes the driver. Injected context goes to the nearest step and does not wake it. | Callers choose when the model sees input and whether it costs a model request now. |
| **Claim** | The driver removing input from the inbox for the turn it has just opened. | From that point the message belongs to exactly one turn. |
| **Turn** | Zero or more steps between `turn/start` and `turn/end`. | The unit a user sees as "the agent answered". |
| **Step** | One model request and the tool calls it causes, between `step/start` and `step/end`. | The smallest unit that is admitted, retried or closed as a whole. |
| **Attempt** | One call to a model provider. A step has one or more. | A failed attempt is a logged fact and may be retried inside the step. |
| **Admission** | The single batch of log entries that fixes what one model request contains: prompt changes, the entered user messages and the request header. | A request can be rebuilt from the log because nothing else feeds it. |
| **Request series** | A run of requests whose earlier messages are byte-identical, so a provider's prefix cache can be reused. A compaction or a prompt rewrite starts a new one. | Decides whether a changed system prompt is appended or rewritten in place. |
| **Cancel cause** | A typed reason attached to a cancellation: `user`, `parent`, `extension`, `disposed` or `budget`. | The log says why a turn stopped, not only that it stopped. |
| **Maintenance task** | Work that needs the agent to itself while no turn runs, such as a manual compaction. | Mutual exclusion with turns without a second lock. |
| **Initiator** | The agent on whose behalf code is running, carried in `context.Context`. It proves neither liveness nor permission. | Shared services attribute work without an extra parameter on every call. |
| **Agent reference** | A small value (session id plus an incarnation number) that names one live agent instance and can cross a process boundary. | A replaced agent with the same id cannot be mistaken for the old one. |
| **Turn budget** | Limits on steps per turn, attempts per step and, optionally, time per turn. | A runaway turn stops by itself. |
| **Closers** | Log entries that close what is open: an error result for each unanswered tool call, then `step/end`, then `turn/end`. | History always pairs a call with a result. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.core.agent` | `@deepseek-ai/dsh-agent` | definition | Declares the `Agent` contract, the live registry `ctx.agents`, the factory seat, the initiator scope and the `agent/*` events (`packages/core/agent/src/index.ts:257`, `packages/core/agent/src/runtime-types.ts:190-273`). It builds no model request (`packages/core/agent/README.md:12`). |
| `UP-PKG-packages.core.agent-loop` | `@deepseek-ai/dsh-agent-loop` | provider | The one shipped driver. It registers itself in the factory seat, creates and resumes agents, starts agents declared in config, and runs turns and steps (`packages/core/agent-loop/src/index.ts:290-370`, `packages/core/agent-loop/src/agent.ts:112`). |
| `UP-PKG-packages.core.agent-default-model` | `@deepseek-ai/dsh-agent-default-model` | provider | Answers "which model does a fresh agent use" from live config and saves a new default through the profile configuration editor (`packages/core/agent-default-model/src/index.ts:49-96`). |
| `UP-PKG-packages.core.agent-tool-presentation` | `@deepseek-ai/dsh-agent-tool-presentation` | other (deprecated) | A selector with one field, `mode: native | ptc`, that mounts one of two tool executors for a preset (`packages/core/agent-tool-presentation/src/index.ts:52-54`). The whole package is marked deprecated (`packages/core/agent-tool-presentation/src/index.ts:4`). |

### 3.2 Behaviour

[../upstream/architecture.md §4](../upstream/architecture.md#4-turn-flow) lists the sixteen
points of a turn. This section adds what the Go design of this area must answer.

- **Registry and factory seat.** `ctx.agents` keeps one entry per live agent, keyed by session id; a second entry for the same id is refused (`packages/core/agent/src/index.ts:493`). `create()` and `resume()` delegate to the registered factory and pass the caller's context as the owner (`packages/core/agent/src/index.ts:421-446`). They return an `AgentHandle`; `get(id)` returns the bare agent, so only the creator can tear it down (`packages/core/agent/src/index.ts:157-174`). The loop is a second, structural owner: unloading it drains every agent it made (`packages/core/agent/README.md:136`).
- **Creation is one transaction.** In order: build the agent and its scoped context; store the history; enter the session store; run the creator's `setup` callback and its optional commit; enter the registry; dispatch `agent/created` to listeners one at a time; release held input (`packages/core/agent-loop/src/index.ts:622-651`). It runs as a maintenance task, so no turn starts inside it. Resume first takes write ownership of the stored session, reads it, and appends closers for a turn a crash left open (`packages/core/agent-loop/src/index.ts:796-810`).
- **Inbox.** Every change to pending input is one `agent/inbox/spliced` session event: list name, position, number removed, messages inserted (`packages/core/agent/src/types.ts:110-116`). A projection folds the events into the two lists and rejects a bad position or a duplicate message id (`packages/core/agent-loop/src/inbox.ts:36-48`). A removal by a caller is marked `outcome: 'canceled'`; a claim by the driver is a removal with no outcome (`packages/core/agent-loop/src/inbox.ts:214`). Insertions, claims and discards are also announced live, one event per message (`packages/core/agent-loop/src/inbox.ts:111`, `:224-229`). `send()` returns nothing: a follow-up only queues (`packages/core/agent/src/runtime-types.ts:245-252`).
- **Driver phases.** `idle`, `maintenance`, and `running` with the turn number, the step number and one abort controller per turn (`packages/core/agent-loop/src/agent.ts:53-61`). Public status has two values, `idle` and `running`; maintenance reports `idle` (`packages/core/agent-loop/src/agent.ts:157-159`).
- **Opening.** A turn appends `turn/start`, then claims all next-step input plus one queued prompt (`packages/core/agent-loop/src/agent.ts:342`, `packages/core/agent-loop/src/inbox.ts:108-113`). Before a step opens, the driver captures the model selection, resolves the step's tool set once, assembles the prompt and runs the `agent/pre-step` waterfall (`packages/core/agent-loop/src/agent.ts:293-312`). A rejection ends the turn as `blocked`; an empty first batch ends it as `completed`; neither opens a step, and the claimed messages stay removed (`packages/core/agent-loop/src/agent.ts:354-365`).
- **Admission.** After `step/start`, the deprecated `agent/request` waterfall and `prepareCall()` run; only then are prompt changes, user messages and the request header appended in one synchronous stretch (`packages/core/agent-loop/src/agent.ts:367`, `:451-485`).
- **Attempts and retry.** Each attempt ends in exactly one durable record: `assistant/message` on success, or `assistant/attempt` when it failed or was cancelled with nothing to keep (`packages/core/agent-loop/src/agent.ts:508-536`, `:549-553`, `:580-589`). A failed attempt goes to the `agent/request-error` waterfall; `retry` repeats the attempt inside the open step without repeating assembly, pre-step or user admission (`packages/core/agent-loop/src/agent.ts:554-569`). The subsystem page says this runs "after a failed model step closes" (`docs/subsystems/core.md:399`), but the code runs it before `step/end` (`packages/core/agent-loop/src/agent.ts:394`). Inference: the page is stale; the code is the behaviour.
- **Tools and stopping.** The whole batch of tool calls goes to `ctx.tools.execute()` with the bound `maxParallelToolCalls`, default 10 (`packages/core/agent-loop/src/agent.ts:594-599`, `packages/core/agent-loop/src/constants.ts:6`). When nothing is owed, `agent/turn-stopping` runs; a listener that objects steers, and fresh steering causes another step (`packages/core/agent-loop/src/agent.ts:401-405`).
- **Closing.** `turn/end` is appended in a `finally` block with `completed`, `max-tokens`, `blocked`, `aborted` with its cause, or `error` (`packages/core/agent-loop/src/agent.ts:408-431`). A `max-tokens` ending is sticky across later steps (`packages/core/agent-loop/src/agent.ts:374-379`).
- **Model selection.** `agent.model.current()` returns the pending recorded choice, else the choice of the latest request, else the agent's creation options (`packages/core/agent/src/model-selection.ts:106-121`). `select()` appends a log-only `model/selection` event (`packages/core/agent/src/model-selection.ts:113-116`). The loop captures the selection once per step and keeps it for that step's retries (`packages/core/agent-loop/README.md:93`). The default-model service is consulted by entry points, not by the loop (`packages/core/agent-default-model/README.md:28`).
- **Tool presentation.** The step's tool set is resolved once and fixes both the schemas the model is shown and the tools that may run (`packages/core/agent-loop/src/agent.ts:298`). Whether the model sees every schema (`native`) or one `run_code` tool with a generated SDK (`ptc`) is chosen per preset by which executor is mounted; offering both is rejected (`.agents/notes/implemented/simplification/2026-10-05-native-or-ptc-presentation.md:13`).
- **Config, services, persisted data.** Three config schemas: the loop (`maxParallelToolCalls`, `agents[]`; `docs/config-catalog.md:95`), the default model (`provider`, `model`, `reasoningEffort`; `docs/config-catalog.md:39`) and the presentation selector (`mode`; `docs/config-catalog.md:181`). Three services: `ctx.agents`, `ctx.agentLoop`, `ctx.agentDefaultModel`. Two persisted event types are declared here, `agent/inbox/spliced` and `model/selection`, and three projections, `inbox`, `modelSelection` and `turnBoundary` (`packages/core/agent/src/index.ts:303`, `packages/core/agent-loop/src/index.ts:333-334`). No tool is exposed to the model.

### 3.3 Failure, retry, rollback and cleanup upstream

| Situation | What upstream does | Left behind | Source |
|---|---|---|---|
| Setup, commit or an `agent/created` listener fails; or the owner is disposed during creation | Rolls back: scope unwound, write handle closed, registry entries removed. A creation that was announced gets a matching `agent/disposed` | Events already stored and restored inbox input are kept | `packages/core/agent-loop/README.md:112`; `packages/core/agent/src/index.ts:529-534` |
| A model attempt fails | One `assistant/attempt`; then the request-error waterfall; without `retry` the turn ends as `error` | Nothing for that attempt | `packages/core/agent-loop/src/agent.ts:549-569` |
| A step fails with tool calls unanswered | Before `step/end`, an error result per unanswered call: `TOOL_OUTCOME_UNKNOWN` if the call was recorded, `TOOL_NOT_STARTED` if not | **Uncertain:** whether a recorded call took effect. Unanswered calls in turns that were already closed are not repaired | `packages/core/agent-loop/src/agent.ts:380-394`; `packages/core/agent-loop/README.md:134`, `:235` |
| A listener or tool middleware throws | The turn ends as `error`; the driver survives | The next turn starts clean | `packages/core/agent-loop/README.md:132` |
| `cancel(cause)` | Aborts the turn's signal; clears the inbox unless `keepInbox`; the first cause wins. A cancelled stream keeps delivered text as an `interrupted` message. Calls never dispatched get paired `ABORTED_BEFORE_DISPATCH` results | No way to stop one step and keep the turn | `packages/core/agent-loop/src/agent.ts:192-202`, `:508-525`; `packages/core/agent/README.md:210` |
| Teardown | Cancel with cause `disposed`, wait for the driver, unwind the scope, close the write handle, leave the agent registry, leave the session store | Two other documents give a different order (section 4) | `packages/core/agent-loop/src/index.ts:494-546` |
| Root shutdown with an active agent | The final `turn/end` can be in memory and not in the stored log | **Uncertain:** whether the last turn is closed on disk | `packages/core/agent-loop/README.md:234` |
| A runaway turn | Nothing: there is no budget | The turn runs until tools and steering stop | `packages/core/agent-loop/README.md:238` |

## 4. Upstream decisions dissected

The first fourteen rows are the decision notes handed to this area; paths are under
`.agents/notes/`. The rest are decisions visible only in code or READMEs.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `implemented/architecture/2026-07-05-reconstructable-requests.md` | Anything the model sees can be rebuilt from the log; the open step is the reconstruction boundary | keep | It is what makes resume, fork, replay and prefix caching work | [DH-ADR-AGENT-2](#dh-adr-agent-2), [DH-ADR-ARCH-10](../upstream/architecture.md#dh-adr-arch-10) |
| `implemented/architecture/2026-07-15-agent-initiator-scope.md` | The initiating agent travels through `AsyncLocalStorage`, with drain bookkeeping at teardown | change | Go has no such storage. A context value needs no drain and no `disable()` call | [DH-ADR-AGENT-9](#dh-adr-agent-9) |
| `implemented/architecture/2026-07-24-separate-context-injection-from-turn-execution.md` | `inject()` is the only way to add context; a turn means one run of the loop; idle injection opens no turn | keep | One path, one event type; turn counts mean model work | [DH-ADR-AGENT-3](#dh-adr-agent-3) |
| `implemented/architecture/2026-07-30-followup-enqueue-and-owned-runs.md` | A follow-up only queues; no message owns the output that follows | keep | Steering and injection make a per-message result unsound. Go adds a receipt, which proves admission and nothing else | [DH-ADR-AGENT-3](#dh-adr-agent-3) |
| `implemented/architecture/2026-08-10-cancelled-stream-prefix-finalize.md` | A cancelled stream commits the text the user saw as an `interrupted` message; tool calls in it are left out | keep | History must contain what the user read | [DH-ADR-AGENT-5](#dh-adr-agent-5) |
| `implemented/architecture/2026-08-31-explicit-agent-runtime-identity.md` | Identity is passed where it is needed; a context owns registrations and has no reverse "my agent" property | keep | Lifetime ownership and domain identity are different facts | [DH-ADR-AGENT-9](#dh-adr-agent-9) |
| `implemented/architecture/2026-09-09-awaited-agent-creation.md` | `agent/created` listeners run one at a time and are awaited; a failure fails creation | keep, with one change | Tools installed asynchronously must exist before the first request. The self-wait deadlock becomes an immediate error | [DH-ADR-AGENT-6](#dh-adr-agent-6) |
| `implemented/bug-fix/2026-10-22-series-start-unchanged-prompt-prefix.md` | At a series start the prompt node is rewritten only when its text must move | keep | The note records a real 0.16% cache hit caused by the rewrite | [DH-ADR-AGENT-2](#dh-adr-agent-2) |
| `implemented/feature/2026-07-10-parallel-tool-call-execution.md` | A tool may classify one call as safe to overlap; anything else is exclusive; results are committed in model order | keep, extended | The fail-to-exclusive rule is right. The unary limit is removed with resource keys | [DH-ADR-AGENT-11](#dh-adr-agent-11) |
| `implemented/feature/2026-09-02-in-history-system-prompt-replacement.md` | On a route that declares it, a changed prompt is appended in history instead of rewriting the first message | keep | A provider fact; saves the whole cached prefix | [DH-ADR-AGENT-2](#dh-adr-agent-2) |
| `implemented/simplification/2026-06-20-unify-agent-and-session-id.md` | An agent's id is its session's id | keep | One identity for routing, log and storage | [DH-ADR-AGENT-1](#dh-adr-agent-1) |
| `implemented/simplification/2026-07-17-one-send-one-turn.md` | Each send is its own queue item and, if it runs, its own turn; the next turn starts after the previous one's durability checkpoint | keep | Grouping by timing would depend on the Go scheduler | [DH-ADR-AGENT-3](#dh-adr-agent-3) |
| `implemented/simplification/2026-10-05-native-or-ptc-presentation.md` | One invocation form per agent: native schemas or `run_code` | change | [DH-ADR-8](../design.md#dh-adr-8) removes the `run_code` form, so one form remains and there is nothing to select | [DH-ADR-AGENT-11](#dh-adr-agent-11) |
| `proposed/testing/2026-06-11-deterministic-and-stress-testing.md` | Proposal: no sleeps in tests; replay every test's log; repeat the loop tests to find races | keep (adopt) | Cheap in Go: virtual time, the race detector and `-count` already exist | `DH-T-AGENT-8`, [DH-ADR-TOOLCHAIN-2](../upstream/toolchain.md#dh-adr-toolchain-2) |
| Consumers depend on the contract package, never on the loop (`packages/core/agent/README.md:96`) | The driver stays swappable | keep | Recommended by [DH-ADR-ARCH-1](../upstream/architecture.md#dh-adr-arch-1) | [DH-ADR-AGENT-1](#dh-adr-agent-1) |
| Two owners, one memoised teardown (`packages/core/agent-loop/src/index.ts:494`) | The creator and the loop both reach the same teardown | keep | Either may unload first | [DH-ADR-AGENT-6](#dh-adr-agent-6) |
| Three documented teardown orders (`packages/core/agent-loop/src/index.ts:494-546`; `packages/core/agent/README.md:126`; `docs/subsystems/core.md:35-36`) | The code closes the write path after the scope; the README omits the write path; the handle comment unwinds the scope last | change | One order, stated once, with a test | [DH-ADR-AGENT-6](#dh-adr-agent-6) |
| Status has two values; maintenance reports `idle` (`packages/core/agent/src/runtime-types.ts:130-136`) | Disposal is not a status | keep, plus a separate lifecycle state | Callers also need to know that an agent refuses input | [DH-ADR-AGENT-1](#dh-adr-agent-1) |
| A wake while idle always opens a turn, even when its message was removed first (`packages/core/agent-loop/src/agent.ts:227-231`) | The turn boundary belongs to the wake | change | A turn means model work; a wake that finds nothing opens nothing | [DH-ADR-AGENT-3](#dh-adr-agent-3) |
| A rejected step drops its claimed messages without a record of their own (`packages/core/agent/src/runtime-types.ts:319-323`) | A claimed message "ends here" | change | The Go log names who rejected and why on `turn/end` | [DH-ADR-AGENT-4](#dh-adr-agent-4) |
| `agent/turn-stopping` listeners object by steering (`packages/core/agent/src/runtime-types.ts:401-411`) | "Data decides", so listener order cannot matter | keep the property, change the form | A returned decision is checkable; a merge rule keeps order irrelevant | [DH-ADR-AGENT-4](#dh-adr-agent-4) |
| The retry listener sleeps inside the waterfall (`packages/llm/llm-retry/src/index.ts:84-92`, `:186`) | Back-off is the listener's business | change | The driver waits, so the wait is cancellable in one place and testable under virtual time | [DH-ADR-AGENT-4](#dh-adr-agent-4) |
| Requests and messages are deep-frozen (`packages/core/agent-loop/src/agent.ts:733-740`) | Listeners share object references | drop | Go copies at the boundary ([DH-ADR-ARCH-11](../upstream/architecture.md#dh-adr-arch-11)) | [DH-ADR-AGENT-2](#dh-adr-agent-2) |
| The cancel cause is copied because Node's fetch writes a stack onto it (`packages/core/agent-loop/src/agent.ts:86-109`) | Keeps a transport trace out of the log | drop | A Go cause is an immutable typed error | [DH-ADR-AGENT-5](#dh-adr-agent-5) |
| A launcher fixes configured agents' session ids through a context key set before the loader runs (`packages/core/agent-loop/src/index.ts:204-249`) | An overlay that changes the model must not drop the identity | change | A command-line overlay is an ordinary layer in Go ([DH-ADR-LOADER-4](../libs/cordis-loader.md#dh-adr-loader-4)) | [DH-ADR-AGENT-12](#dh-adr-agent-12) |
| A missing adapter at `prepareCall` is tolerated for routes served by middleware (`packages/core/agent-loop/src/agent.ts:652-656`) | Replay serves a route without registering it | drop | In Go the replay provider is a registered provider (area A04) | [DH-ADR-AGENT-10](#dh-adr-agent-10) |

## 5. Tech debt and its resolution

| Debt ID | What it is | Evidence | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-deprecated.packages/core/agent/src/runtime-types.ts:363` | The `agent/request` waterfall is deprecated but still dispatched on every attempt | `packages/core/agent/src/runtime-types.ts:363`; `packages/core/agent-loop/src/agent.ts:636` | Resolved: the hook does not exist. Its four uses each have a replacement ([DH-ADR-AGENT-4](#dh-adr-agent-4)) |
| `UP-DEBT-deprecated.packages/core/agent-tool-presentation/src/index.ts:4`, `...:23`, `...:38`, `...:49` (four units) | The selector package, its config type, its schema and its entry function are all deprecated | `packages/core/agent-tool-presentation/src/index.ts:4`, `:23`, `:38`, `:49` | Resolved: no selector is built ([DH-ADR-AGENT-11](#dh-adr-agent-11)) |
| `UP-DEBT-limit.packages.core.agent-tool-presentation.1` | A preset can select PTC mode but cannot supply the runtime; selection stays pending | `packages/core/agent-tool-presentation/README.md:111` | Resolved: there is no PTC runtime and no pending state ([DH-ADR-8](../design.md#dh-adr-8), [DH-ADR-AGENT-11](#dh-adr-agent-11)) |
| `UP-DEBT-limit.packages.core.agent-default-model.1` | One default for the whole process | `packages/core/agent-default-model/README.md:115` | Resolved: the default is read with the agent's scope key, and a preset registers its own as a scoped registration ([DH-D-AGENT-8](#dh-d-agent-8)) |
| `UP-DEBT-limit.packages.core.agent-default-model.2` | Saving does nothing when no configuration editor is mounted | `packages/core/agent-default-model/README.md:116`; `packages/core/agent-default-model/src/index.ts:86-87` | Resolved: the save goes through boot's `config.Service.Apply` to the one writable layer, which every profile has, and a failed write is an error, never a silent no-op ([DH-D-AGENT-8](#dh-d-agent-8)) |
| `UP-DEBT-limit.packages.core.agent-loop.1` | The concurrency classifier looks at one call only; safety that depends on a sibling forces exclusive | `packages/core/agent-loop/README.md:233` | Resolved: a call may declare resource keys, and the scheduler compares them ([DH-ADR-AGENT-11](#dh-adr-agent-11), [DH-D-AGENT-10](#dh-d-agent-10)) |
| `UP-DEBT-limit.packages.core.agent-loop.2` | Root shutdown can lose the final `turn/end` | `packages/core/agent-loop/README.md:234` | Resolved: teardown ends with a bounded flush before the lease is released, and the driver keeps the store alive until then ([DH-ADR-AGENT-8](#dh-adr-agent-8)) |
| `UP-DEBT-limit.packages.core.agent-loop.3` | Unanswered calls in already closed turns are never repaired | `packages/core/agent-loop/README.md:235` | Resolved by construction: a step cannot be closed without a result for every call, because the results and `step/end` are one batch ([DH-ADR-AGENT-2](#dh-adr-agent-2)). No older logs exist: the Go store reads no upstream session ([DH-ADR-11](../design.md#dh-adr-11)) |
| `UP-DEBT-limit.packages.core.agent-loop.4` | A configured agent without `sessionId` gets a new session on every start | `packages/core/agent-loop/README.md:236` | Resolved: every configured agent states how it starts; the field is required and has no default ([DH-ADR-AGENT-12](#dh-adr-agent-12)) |
| `UP-DEBT-limit.packages.core.agent-loop.5` | Configured agents cannot choose a persona or a tool set | `packages/core/agent-loop/README.md:237` | Resolved: a configured agent names a preset ([DH-ADR-AGENT-12](#dh-adr-agent-12)) |
| `UP-DEBT-limit.packages.core.agent-loop.6` | No turn budget | `packages/core/agent-loop/README.md:238` | Resolved: [DH-ADR-AGENT-7](#dh-adr-agent-7) |
| `UP-DEBT-limit.packages.core.agent.1` | The initiator does not cross a worker, process, HTTP or queue boundary | `packages/core/agent/README.md:207` | Resolved: the agent reference is a serialisable value, and each boundary carries it and resolves it on arrival; child processes use the call token of [DH-ADR-8](../design.md#dh-adr-8) ([DH-ADR-AGENT-9](#dh-adr-agent-9)) |
| `UP-DEBT-limit.packages.core.agent.2` | An ambient agent object may be read after the agent has gone | `packages/core/agent/README.md:208` | Resolved: the context carries a reference, not the object; resolving it fails for a disposed or replaced agent, and the same context is cancelled when the agent leaves ([DH-ADR-AGENT-9](#dh-adr-agent-9)) |
| `UP-DEBT-limit.packages.core.agent.3` | An `agent/created` listener that waits for the agent's idle state or its owner's disposal deadlocks | `packages/core/agent/README.md:209` | Resolved: both calls return an error at once when made inside that agent's creation ([DH-ADR-AGENT-6](#dh-adr-agent-6)) |
| `UP-DEBT-limit.packages.core.agent.4` | `cancel()` clears the inbox unless told otherwise, and cannot stop one step only | `packages/core/agent/README.md:210` | Resolved: the inbox choice is a required argument, and a step scope exists ([DH-ADR-AGENT-5](#dh-adr-agent-5)) |
| `UP-DEBT-limit.packages.core.agent.5` | A user message names one source, so merged contributions lose their producers | `packages/core/agent/README.md:211` | Resolved: the driver never merges messages, and a message source holds a kind and a list of producers, so a producer that merges (the runtime-context reminder) names all of them ([DH-ADR-AGENT-4](#dh-adr-agent-4)). The message type is area A04's |

## 6. Go design

### 6.1 Shape

Three Go packages under `src/harness/internal/agent`, following
[../design.md §4.2](../design.md#42-go-module-layout).

| Package | Component | Provides | Injects | Why it exists |
|---|---|---|---|---|
| `internal/agent` | `agent.registry` | Service key `agent.Agents` (`Registry`); the types and event keys below | projections (A01) | The contract everyone imports. It contains no loop |
| `internal/agent/driver` | `agent.driver` | Fills the factory seat; starts configured agents | `agent.Agents`, session store and persistence (A01, A05), tools and prompt assembly (A03), LLM runtime (A04) | The one shipped driver. An import-direction test fails if anything outside this package imports it |
| `internal/agent/defaultmodel` | `agent.default-model` | Service key `defaultmodel.Key` | `config.Key` (boot's `config.Service`, area A21), `scope.TableKey` | Answers "which model when nothing says" |

```go
type Ref struct{ Session session.ID; Incarnation uint64 } // names one live instance
type Agent interface {
    Ref() Ref
    Session() session.ReadSession // read access; only the driver appends turn events
    Scope() *cordis.Context     // the registration context of the agent's scope (core-session.md); reaches no service
    ScopeKey() scope.Key        // visibility only; the driver alone holds the *scope.Scope
    Status() Status             // idle | running
    State() State               // initializing | live | closing | disposed
    Model() Model               // Current() (ModelSelection, bool); Select(ctx, ModelSelection) error
    Inbox() Inbox               // read both lists; Replace and Remove by message id
    Send(ctx context.Context, m UserMessage, to Target, wake bool) (Receipt, error)
    Cancel(cause Cause, o CancelOptions) error // o.Scope: Turn|Step; o.Inbox: Keep|Discard, required
    WaitIdle(ctx context.Context) error
    RunMaintenance(ctx context.Context, name string, task func(context.Context) error) error
}
type Handle interface{ Agent() Agent; Dispose(ctx context.Context) error }
type Registry interface {
    Create(owner *cordis.Context, o CreateOptions) (Handle, error)
    Resume(owner *cordis.Context, o ResumeOptions) (Handle, error)
    Get(id session.ID) (Agent, bool)
    Resolve(r Ref) (Agent, error) // fails for a disposed or replaced instance
    List() []Agent
    SetFactory(owner *cordis.Context, f Factory) (*cordis.EffectHandle, error)
    SetComposer(owner *cordis.Context, c Composer) (*cordis.EffectHandle, error); Recompose(ctx context.Context, r Ref, preset string) error
}
func WithInitiator(ctx context.Context, r Ref) context.Context
func Initiator(ctx context.Context) (Ref, bool)
```

`FollowUp`, `Steer` and `Inject` are fixed forms of `Send`. `CreateOptions` carries the session id, session metadata, an optional seed and fork parent, the initial `ModelSelection`, an optional budget, a preset id, and `Setup func(scope *cordis.Context, a Agent) (commit func() error, err error)`. The owner is an explicit parameter because the Go port has no caller tracking ([DH-ADR-CORDIS-2](../libs/cordis.md#dh-adr-cordis-2)). **The composer seat.** `Registry.SetComposer` is a second seat beside the factory seat, filled by the preset registry ([DH-D-BUNDLE-4](bundle-preset.md#dh-d-bundle-4)). At creation and resume the driver asks the composer for a lease on the preset (by id, or from the creator's scope key), creates the agent's scope with the lease's key as its enclosing scope, runs the composer's commit with `Setup`'s, and releases the lease after the scope has closed. Only the driver holds the `*scope.Scope`, so a change of preset is `Registry.Recompose(ref, preset)`: it runs as a maintenance task, is refused unless the session is blank, and calls `SetEnclosing`. When the seat empties, the driver disposes the agents composed through it.

### 6.2 Contracts

**Turn and step state machine.** The driver goroutine is the only code that moves between
these points. "Batch" means one atomic append under the session's append lock
([DH-ADR-ARCH-2](../upstream/architecture.md#dh-adr-arch-2)).

| # | Point | What happens | Appended to the log | Live |
|---|---|---|---|---|
| 1 | Wake | A waking send, or a wake held during creation or maintenance, reaches the idle driver. If both lists are empty, nothing opens | nothing (the send already logged its splice) | status `running` |
| 2 | Open turn | If the log shows an open turn (a failed close), append its closers first. Then open the turn and claim all `next-step` input plus one `next-turn` item | one batch: `turn/start`, the claim splice (outcome `claimed`, with the turn) | one `InboxClaimed` per message |
| 3 | Budget check | Before every proposed step: steps used against the limit | nothing | |
| 4 | Propose step | Capture the model selection; resolve the step tool set; assemble the prompt; run `PreStep` | nothing | |
| 5 | No step | `Reject`, or an empty first batch | `turn/end` as `blocked` (with who and why) or `completed` | |
| 6 | Open step | | `step/start` | |
| 7 | Bind the call | Ask the LLM runtime to prepare the call for the captured selection. Cancelled here: nothing of the step's input is committed | nothing | |
| 8 | Admit | Prompt changes for this route and series (a change that would rewrite the prefix waits for the tool round to close when the bound model declares `PrefixBoundReasoning`, area A04); the entered user messages (first attempt only); request header (with the credential reference name) and context when they changed; a tool-change notice | one batch: `system/message`, `user/message`, `request/header`, `request/context`, `developer/message` as needed | |
| 9 | Attempt | The checkpoint policy makes the admitted batch durable ([DH-D-STORE-4](session-store.md#dh-d-store-4)); then `Session.NextRequest` builds the `session.Request` from the log alone and `Prepared.Stream` sends it | nothing until the attempt settles | frames `Start`, `Chunk` |
| 10 | Settle | Exactly one record per attempt, then the last frame | `assistant/message` (also for an interrupted prefix) or `assistant/attempt` | frame `End` with the record's `seq` |
| 11 | Recover | A failed attempt goes to `AttemptFailed`. On `Retry` the driver waits and returns to point 7 in the same step; assembly, `PreStep` and user admission are not repeated | nothing | |
| 12 | Run tools | The whole batch goes to the tool pipeline's `ExecuteBatch` (area A03) with the step tool set; how many calls overlap is A03's `maxParallel` | per call `tool/call` then `tool/result`, results in the model's order (A03) | |
| 13 | Close step | Always, on every path out of points 7 to 12 | one batch: an error result for each unanswered call, then `step/end` | |
| 14 | Continue or stop | If `next-step` input is pending or the tools owe a request, claim and go to 3. Otherwise run `TurnStopping`; `KeepOpen` inserts its messages and goes to 3 | the claim or insert splice | |
| 15 | Close turn | Always, on every path out of points 2 to 14 | `turn/end` with one reason | |
| 16 | Checkpoint | Flush the session. The next turn does not open until the flush has settled; a failed flush is reported through `Failed` | nothing | status `idle` when no work is pending |

`turn/end` reasons form a closed set: `completed`, `max-tokens` (sticky across later steps), `blocked{by, reason}`, `aborted{cause}`, `error{failure}`, `refusal{failure}` (the attempt settled with `llm/content-refused`) and `budget{limit, value}`; a resumed log may also hold `interrupted`, written by the session core when it closes a turn at open. The synthetic tool results use the three outcome codes area A03 owns ([tools.md §6.2](tools.md#62-contracts)): `tools/not-started` (no `tool/call` was recorded), `tools/outcome-unknown` (recorded, no result) and `tools/aborted-before-start` (cancelled before it started). Nothing is retried on the model's behalf.

**Inbox and claims.** A send computes its splice and appends it while holding the inbox mutex, so splice order is commit order. `Send` returns `Receipt{MessageID, Seq}` once the splice is committed; the receipt proves admission, not an answer. A message leaves the inbox in one of two ways, both logged: `claimed{turn}` or `discarded{cause}`. A message id is unique over the session's whole history, inherited events included, not only among pending messages: the `consumedWork` fold keeps every id the session has admitted, whatever became of the message. A `Send` whose id was seen before appends nothing and returns `agent/duplicate-message` together with the first receipt, so a retry after a lost reply, also after the message was claimed and answered, cannot start a second turn. A caller may choose the id (a form's request id, an SDK `messageId`, a schedule's occurrence id); otherwise the constructor mints one. Source kind `user` can be set only by a carrier's entry point, never by a tool or a listener. A carrier may keep a presentation layer on top (A19's `conversation.Prompt` answers a repeated form submit with the first receipt), but the guarantee "one message id, at most one turn" is this inbox's and does not depend on it. An injection while idle logs its splice and opens no turn. A waking send that arrives while the turn is being cancelled is queued as a new turn. A resumed agent starts idle: restored input stays parked until a new wake arrives, and so does input kept across a cancel. A `UserMessage` may carry a display annotation (for example confirmed mention spans): it is stored beside the message in the `user/message` payload and is never part of a request.

**What may run at the same time, and what never does.**

| May overlap | Never overlaps |
|---|---|
| Drivers of different agents | Two turns, two steps or two model attempts of one agent |
| `Send`, `Cancel`, `Select` and reads on one agent from any goroutine, while its driver runs | A turn and a maintenance task of one agent (both run on the driver goroutine) |
| Tool calls of one step that the pipeline's scheduler classifies as compatible, up to A03's `maxParallel` | Two appends to one session (the append lock, area A01) |
| Delivery of a live notice and the driver's next action | `PreStep`, `AttemptFailed` and `TurnStopping` listeners of one agent |
| A model stream and a caller's `Cancel` | A listener call and any lock: no lock is held while user code runs |

Each agent has one driver goroutine, parked on a one-slot wake channel, and one deliverer goroutine that sends live notices in the order they were queued. A notice is queued under the lock that ordered it and delivered outside every lock. Stream frames are bounded by back-pressure: the driver stops reading the model stream while too many frames are undelivered. A notice listener must return promptly and must not wait for its own agent; one that has not returned within `drainWarnAfter` is reported by name. Lock order is inbox mutex, then session append lock, never the reverse.

**Events, grouped by what a listener can change.** Keys are declared in package `agent` unless stated, and every event about one agent is dispatched through that agent's scope filter ([DH-D-CORDIS-3](../libs/cordis.md#dh-d-cordis-3)). Dispatch forms are those of [../design.md §4.6](../design.md#46-event-model).

| Group | Upstream unit | Go key and form | Request and result |
|---|---|---|---|
| **Can change the turn** (closed decisions) | `UP-EVT-agent/pre-step` | `agent.PreStep`, around | `PreStepRequest{Agent, Turn, Step, Messages, Selection, Previous}` to `Enter{Messages, StartsSeries}` or `Reject{Reason}` |
| | `UP-EVT-agent/request` | none: removed | see [DH-ADR-AGENT-4](#dh-adr-agent-4) |
| | `UP-EVT-agent/request-error` | `agent.AttemptFailed`, around | `AttemptFailure{Agent, Turn, Step, Attempt, Failure, Policy, AttemptsLeft}` to `GiveUp{}` or `Retry{After, Reason}` |
| | `UP-EVT-agent/turn-stopping` | `agent.TurnStopping`, around behind a merging helper | `StopRequest{Agent, Turn, Steps, Reason}` to `AllowStop{}` or `KeepOpen{Messages, Reason}` |
| **Can fail creation** | `UP-EVT-agent/created` | `agent.Created`, ordered | `Created{Agent, Source}`; the first error fails creation and rolls it back |
| **Can change nothing** | `UP-EVT-agent/disposed` | `agent.Disposed`, notify | `{Ref}`; sent after the registry entry is removed |
| | `UP-EVT-agent/status` | `agent.StatusChanged`, notify | `{Agent, Status}` |
| | `UP-EVT-agent/inbox/inserted` | `agent.InboxInserted`, notify | `{Agent, Message, Seq}` |
| | `UP-EVT-agent/inbox/claimed` | `agent.InboxClaimed`, notify | `{Agent, Message, Turn}` |
| | `UP-EVT-agent/inbox/discarded` | `agent.InboxDiscarded`, notify | `{Agent, Message, Cause}` |
| | `UP-EVT-agent/assistant-stream` | `agent.StreamFrame`, notify | `Start`, `Chunk` with a dense index, one `End` holding the settled record's `seq` or `abandoned` |
| | `UP-EVT-agent/error` | `agent.Failed`, notify | `{Agent, Turn, Step, Err}`; also sent when the failure has no place in the log |
| | none: new (owner decision M1-D-PIPE-2 A, plan 16.4.1 of the M1 gap-closure plan) | `agent.RetryWaited`, notify (event key `agent/retry-wait-ended`) | `RetryWaitEnded{Agent, Turn, Step, Attempt, Cancelled}`; sent when the driver's wait on a `Retry` ends, before the next attempt is prepared or the step closes. `Cancelled` is true when a cancel of the step or turn ended the wait first, and then no further attempt runs. A retry listener writes the wait's outcome from it (`llm/retry-started`, or `llm/retry-abandoned` "cancelled"); an observer's error or panic never changes the step |
| | `UP-EVT-agent-loop/config-start-failed` | `driver.StartFailed`, notify | `{Label, Session, Err}` |
| **Not this area's** (assigned by default) | `UP-EVT-internal/config`, `internal/plugin`, `internal/service`, `internal/status`, `internal/dispatch`, `loader/volatile-update` | framework: no agent event | [DH-ADR-AGENT-13](#dh-adr-agent-13) |
| | `UP-EVT-connection/reset`, `locale/change`, `slots/changed`, `theme/change` | browser runtime: no host event | [DH-ADR-AGENT-13](#dh-adr-agent-13) |

A listener of the first group receives copies of the messages ([DH-ADR-ARCH-11](../upstream/architecture.md#dh-adr-arch-11)) and the turn's `context.Context`. An error or panic from one ends the turn as `error` with code `extension/failed` and the listener's name; the driver then waits for the next wake. `TurnStopping` listeners register through `agent.OnTurnStopping`, which never hands them `next`: the helper always calls downstream and keeps the turn open if either side says `KeepOpen`, joining the messages in registration order. A listener can therefore keep a turn open and can never force it closed. A `PreStep` listener may append to the session before `step/start`; when a retry follows a surface replacement made by an `AttemptFailed` listener, admission logs a new `request/header`, which starts a new request series. `RunMaintenance` returns `agent/busy` at once while a turn runs, and the caller chooses whether to `WaitIdle` first. `turn/end` and its reason reach observers as a committed session event (`session.Committed`). The `request/header` carries the prompt assembly's fingerprint and manifest ([tools.md](tools.md#dh-adr-tools-8)), and context that a tool call added is appended after the batch's results, before `step/end`.

**Config.** Go structs with defaults as a Go value, decoded strictly ([DH-ADR-SCHEMA-1](../libs/zod.md#dh-adr-schema-1), [DH-ADR-SCHEMA-3](../libs/zod.md#dh-adr-schema-3)).

| Upstream unit | Go struct | Fields (default) |
|---|---|---|
| `UP-CFG-dsh-agent-loop` | `driver.Config` | Upstream's `maxParallelToolCalls` is not here: the cap on overlapping tool calls has one owner, the `tools.registry` component's `maxParallel` ([tools.md §6.2](tools.md#62-contracts)). `budget.maxSteps` (500; 0 means unlimited and must be written), `budget.maxAttemptsPerStep` (8), `budget.maxDuration` (none); `finalFlushTimeout` (10s); `drainWarnAfter` (10s); `agents[]`: `label` (required), `start` (`fresh`, `resume-or-create` or `resume`; required), `session` (required for `resume`), `preset`, `dir`, `model`, `budget`. Two entries with one session id are a load error |
| `UP-CFG-dsh-agent-default-model` | `defaultmodel.Config` | `provider`, `model` (both required), `reasoningEffort`; all live |
| `UP-CFG-dsh-agent-tool-presentation` | none | The selector is not built |

**Services.**

| Upstream unit | Go replacement |
|---|---|
| `UP-SVC-agents` | Key `agent.Agents`, interface `Registry` |
| `UP-SVC-agentLoop` | No service key. The driver fills the factory seat; its limits are read from its config |
| `UP-SVC-agentDefaultModel` | Key `defaultmodel.Key`: `Current(k scope.Key) (ModelSelection, bool)`, `Register(ctx *cordis.Context, ModelSelection)` (a default for one scope, `Layered`), `Save(ctx, ModelSelection) error` |
| `UP-SVC-inspector` | Not this area's ([DH-ADR-AGENT-13](#dh-adr-agent-13)) |

**Persisted data.** This area registers two log-only event types, each a state-carrying record with a payload version: `agent/inbox/spliced` (list, position, removed count, inserted messages, outcome `claimed{turn}` or `discarded{cause}`) and `model/selection` (the complete selection). It registers four pure folds: `inbox`, `modelSelection`, `turnBoundary` and `consumedWork` (which closed turn accounts for which input). The turn and step event types are the session area's.

**Model selection.** `Model().Current()` reads, in order: a recorded selection not yet used; the selection of the latest request header; the agent's creation options; the default registered nearest on the agent's scope chain, read with `defaultmodel.Current(agent.ScopeKey())`. The driver captures it at point 4; it is fixed for that step and its retries, and a `Select` during the step applies from the next step, including a tool continuation in the same turn. With no source the turn ends as `error` with code `agent/no-model`. `ModelSelection` holds provider, model, optional reasoning effort and optional request controls (output cap, and sampling settings whose type area A04 owns); the request header logs what was used.

**Tool presentation.** The model is shown what the step tool set holds. The driver asks the tool service once per proposed step for the set visible to this agent and selection (global tools, the preset's, the agent's own, with shadowing and restrictions). The set supplies the schemas to prompt assembly and is the admission list for execution; it is kept for the step's retries and released at point 13. A change in schema names between two request headers is logged as a `developer/message`. There is one form, native function schemas ([DH-ADR-AGENT-11](#dh-adr-agent-11)). No tool and no wire message is defined here.

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership.** The creator's fiber owns the handle. The driver's fiber is a second owner of every agent it built. Each agent has one scope, minted by `scope.Table.New` with the driver's context as owner. The driver is the scope's owner and not its parent: the scope's fiber hangs under the scope component, which injects nothing, so nothing is resolved "through" an agent's scope ([DH-ADR-SESSION-10](core-session.md#dh-adr-session-10), which refines [DH-ADR-ARCH-7](../upstream/architecture.md#dh-adr-arch-7)). The agent's cancellation context derives from the scope's `Lifetime()`. The session's `Handle` carries the write lease; the creation transaction owns the handle until it hands it to the agent. Access is wider: anyone may look an agent up and send to it.

**Creation and resume.** (1) Validate options and check the owner is active. (2) Acquire the session: `session.Service.Prepare` for a new one, `Fork` for a fork, `Open` for a resume. `Open` itself closes an interrupted turn and returns an `OpenReport` saying what it closed, which the driver passes on to the carrier. (3) Obtain the preset lease, create the scope and the agent in state `initializing`; publish the session. (4) Run `Setup`, then its commit. (5) Enter the registry; a second live agent with the same id is refused here. (6) Dispatch `Created`. (7) State `live`; release held wakes. Sends are accepted from step 3 and are durable, but no turn starts before step 7. A failure or a cancelled owner at any step runs the teardown below from the point reached; `Disposed` is sent only if `Created` had begun. Not undone: events already stored and input already queued. The error is returned after cleanup has finished.

**Teardown, one order** ([DH-ADR-AGENT-6](#dh-adr-agent-6)). It is one memoised function attached as an effect to both owners; whoever unloads first runs it and the other waits.

| Step | Action | Observable |
|---|---|---|
| T1 | Stop admitting: state `closing`; `Send` returns `agent/closing` | |
| T2 | Wait for a creation dispatch still running | |
| T3 | Cancel the active turn or maintenance task with cause `disposed`; discard pending input of a published agent | `InboxDiscarded`; discard splice |
| T4 | Wait for the driver goroutine to exit; it writes the closers | tool results, `step/end`, `turn/end{aborted, disposed}`; status `idle` |
| T5 | Close the scope (`Scope.Close`): scoped tools, prompt sections, listeners; then release the preset lease | |
| T6 | Flush the session within `finalFlushTimeout` | flush error returned from `Dispose` |
| T7 | Leave the registry | `Disposed` |
| T8 | Close the session handle, which releases the lease; state `disposed` | `session.Closed` |

**Failure by kind.**

| Situation | What happens | Undone, and what stays uncertain |
|---|---|---|
| A model attempt fails | Point 10 logs it; point 11 may retry, at most `maxAttemptsPerStep` attempts per step. The driver, not the listener, waits `Retry.After`, and a cancel ends the wait | No message and no tool effect is committed for the failed attempt |
| A step fails after tool calls were proposed | Started calls settle first. Point 13 pairs every unanswered call | **Uncertain:** whether a call marked `tools/outcome-unknown` took effect; the result text tells the model to verify |
| An interception listener fails | The turn ends as `error`; the loop continues | Nothing in the log is rolled back |
| A notice listener fails or panics | Logged and contained (`Notify`) | Nothing |
| Cancel, turn scope | The turn context is cancelled with the cause. Stream: the delivered text and reasoning become an `interrupted` message, tool calls in it are dropped. Tools: no new call starts, started calls settle, the rest get `tools/aborted-before-start` pairs. With no active turn, only the inbox choice applies | Input claimed but not yet admitted is not put back; `consumedWork` shows it as claimed and unrun |
| Cancel, step scope | Only the step context is cancelled. The step closes as above; the turn continues if `next-step` input is pending and otherwise ends as `aborted` | As above |
| The budget is reached | Step limit: the turn ends as `budget` after the current step has closed. Time limit: the turn context is cancelled with cause `budget` | Pending `next-step` input stays parked |
| An append fails (lease lost, disk) | The driver stops the run and sends `Failed`. On `session/lease-lost` the session has closed itself (`session.Closed` with that cause) and the driver disposes the agent. The turn may stay open in the log | The next wake, or the session core at the next write-open, appends the closers first |
| The final flush fails or times out | `Dispose` returns `session/flush-failed` with the last durable `seq`; the lease is still released | **Uncertain:** events after that `seq`. Resume closes the turn |
| The host is killed | No teardown runs | Resume appends closers; the unsettled attempt's stream is lost |

## 7. Decisions

<a id="dh-adr-agent-1"></a>
### DH-ADR-AGENT-1 A contract package, a live registry and one replaceable driver
- **Decision.** Keep upstream's split: package `agent` holds the contract, the registry and the events; the driver registers in a factory seat. An agent's id is its session's id. Status stays two-valued (`idle`, `running`); a separate lifecycle state says whether the agent accepts input.
- **Why.** Every carrier and policy component depends on the contract only, so the loop can change or be replaced without touching them.
- **Tradeoff.** One more indirection when reading the code. A second factory is refused, so two drivers cannot share one registry.
- **Serves.** [DH-O-1](../design.md#dh-o-1), [DH-O-3](../design.md#dh-o-3). **Verified by.** `DH-T-AGENT-1`, `DH-T-AGENT-27`.

<a id="dh-adr-agent-2"></a>
### DH-ADR-AGENT-2 A turn and a step each have one close path, and admission is one batch
- **Decision.** The driver closes a step in one place and a turn in one place, on every exit. The step close appends the missing tool results and `step/end` as one batch. Admission is one batch after the model call is bound. The prompt rules of the two upstream notes are kept: append in history where the route allows it, rewrite the first node only when the text must move.
- **Why.** Upstream reaches the same result with nested `finally` blocks and single-threaded "synchronous stretches". Go needs the atomicity stated, and a batch makes a closed step with an unanswered call impossible to write.
- **Tradeoff.** The driver depends on a batch append from the session area. A request cannot be adjusted between admission and dispatch.
- **Serves.** [DH-O-3](../design.md#dh-o-3), [DH-O-4](../design.md#dh-o-4). **Verified by.** `DH-T-AGENT-7`, `DH-T-AGENT-8`, `DH-T-AGENT-9`, [DH-E-AGENT-1](#dh-e-agent-1).

<a id="dh-adr-agent-3"></a>
### DH-ADR-AGENT-3 The inbox is a logged queue; a send returns a receipt; one follow-up is at most one turn
- **Decision.** Every inbox change is one state-carrying log event. A turn claims at most one `next-turn` item, in the same batch as `turn/start`. A send returns a receipt and never a result. The next turn opens only after the previous turn's flush has settled. A wake that finds nothing pending opens no turn.
- **Why.** Grouping sends by arrival time would make behaviour depend on the scheduler. A logged claim is what lets a reader prove that a message ran once or not at all.
- **Tradeoff.** Callers that want "the answer to my prompt" must own an interval from receipt to idle (the SDK and command-line areas do); the agent does not pretend to know it.
- **Serves.** [DH-O-3](../design.md#dh-o-3), [DH-O-5](../design.md#dh-o-5). **Verified by.** `DH-T-AGENT-4`, `DH-T-AGENT-5`, `DH-T-AGENT-6`.

<a id="dh-adr-agent-4"></a>
### DH-ADR-AGENT-4 Three interception points with closed decisions; the request hook is removed
- **Decision.** `PreStep`, `AttemptFailed` and `TurnStopping` are around events returning closed decision types. `TurnStopping` is monotonic: a listener can only keep the turn open. The driver never merges messages; a producer that merges lists every contributor in the message source. The deprecated `agent/request` hook is not built. Its uses are replaced: model and effort by `Model().Select` before the step; the output cap and sampling by the controls in `ModelSelection`; detecting a model switch (upstream compaction, `packages/compaction/compaction-basic/src/index.ts:198`) by `PreStepRequest.Previous`; counting requests and reading the cancel signal (upstream timeline, `packages/experimental/timeline/src/reminders.ts:92`) by the `step/start` event, the `Start` frame and the context every listener receives.
- **Why.** A hook that runs between admission and dispatch is the one place a request could differ from the log. Upstream already forbids it from changing the model and says its other uses have "no replacement API" (`packages/core/agent/src/runtime-types.ts:363-366`); this gives them one.
- **Tradeoff.** A control change takes effect at the next step, never mid-step. Adding a decision outcome is a change to package `agent`.
- **Serves.** [DH-O-3](../design.md#dh-o-3), [DH-O-4](../design.md#dh-o-4), [DH-O-8](../design.md#dh-o-8). **Verified by.** `DH-T-AGENT-10` to `DH-T-AGENT-13`.

<a id="dh-adr-agent-5"></a>
### DH-ADR-AGENT-5 Cancellation carries a typed cause, an explicit inbox choice and a scope
- **Decision.** Contexts nest: fiber lifetime, agent, turn, step, attempt or tool call. Causes are a closed set of Go error types: `user`, `parent`, `extension`, `disposed`, `budget`. The first cause wins and is copied onto `turn/end`. `Cancel` requires the caller to say `Keep` or `Discard` for the inbox, and takes a scope: `Turn` or `Step`.
- **Why.** [DH-ADR-ARCH-3](../upstream/architecture.md#dh-adr-arch-3) fixes the mechanism. Upstream's default of clearing the inbox surprises callers, and it cannot stop one step. `budget` is added to the causes that decision lists.
- **Tradeoff.** No zero-argument cancel. Cancellation stays cooperative for listeners; a tool body that ignores its context delays the close only by the pipeline's grace period, after which it is detached and its call settles as `tools/outcome-unknown` ([DH-ADR-TOOLS-5](tools.md#dh-adr-tools-5)). `Cancel` never waits, so a listener may call it for the turn it runs in.
- **Serves.** [DH-O-3](../design.md#dh-o-3). **Verified by.** `DH-T-AGENT-15` to `DH-T-AGENT-18`, [DH-E-AGENT-2](#dh-e-agent-2).

<a id="dh-adr-agent-6"></a>
### DH-ADR-AGENT-6 One teardown order, shared by both owners
- **Decision.** The order T1 to T8 of section 6.3: stop admitting, join creation, cancel, wait for the driver, close the scope, flush, leave the registry, close the session handle (which releases the lease). `WaitIdle` and `Dispose` called from inside that agent's own `Created` dispatch return an error at once.
- **Why.** Upstream's code, package README and handle comment give three orders. The scope goes before the flush so that events written by scope cleanups are flushed too; the registry goes after the flush so that `Disposed` means "nothing more will be written".
- **Tradeoff.** `Disposed` listeners registered in the agent's own scope are gone before it is sent; only global listeners see it. Teardown waits for the driver without a limit (see [DH-ADR-AGENT-8](#dh-adr-agent-8)).
- **Serves.** [DH-O-1](../design.md#dh-o-1), [DH-O-3](../design.md#dh-o-3). **Verified by.** `DH-T-AGENT-19`, `DH-T-AGENT-20`.

<a id="dh-adr-agent-7"></a>
### DH-ADR-AGENT-7 A built-in turn budget, finite by default
- **Decision.** The driver enforces steps per turn (default 500), attempts per step (default 8) and an optional time limit, with per-agent overrides. A step limit ends the turn between steps with reason `budget`, never inside a step. Unlimited must be written explicitly. The numbers are a recommended default, not confirmed by the owner.
- **Why.** Upstream leaves this to a policy plugin that nobody ships, so a tool loop or a listener that always keeps the turn open runs until someone notices the bill.
- **Tradeoff.** A long legitimate turn can hit the limit; the user sends "continue" and loses nothing, because the turn closed cleanly. Inference: 500 is a judgement, not a measurement.
- **Serves.** [DH-O-3](../design.md#dh-o-3), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-AGENT-22`, [DH-E-AGENT-4](#dh-e-agent-4).

<a id="dh-adr-agent-8"></a>
### DH-ADR-AGENT-8 Shutdown flushes the final events within a bound, and reports when it cannot
- **Decision.** Teardown step T6 flushes the session before the lease is released, with a context that is not the cancelled agent context and a timeout of `finalFlushTimeout`. The driver component injects the session store and persistence, so the tree keeps them alive until the driver's cleanup has returned ([DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1)), also when the root is disposed. A flush failure is returned from `Dispose` and logged with the last durable `seq`. Waiting for the driver (T4) has no timeout; after `drainWarnAfter` the stuck turn, step and tool call are named in the log.
- **Why.** Answers the open row of [../design.md §5](../design.md#5-failure-retry-rollback-and-cleanup). Upstream loses the final `turn/end` because sibling fibers unload concurrently at root shutdown.
- **Tradeoff.** Shutdown can take as long as the pipeline's grace period for a running tool body plus the flush bound. The process is stopped by one call of `launch.App.Stop`, and the only exit bound is boot's `ShutdownTimeout` (20 s, [boot.md §6.2](boot.md#62-contracts)); the final flush runs in the owner's cleanup during that unload, so `finalFlushTimeout` must stay inside it. When the bound is exceeded the process exits; that is the crash path, and resume closes the turn.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-3](../design.md#dh-o-3). **Verified by.** `DH-T-AGENT-21`, [DH-E-AGENT-3](#dh-e-agent-3).

<a id="dh-adr-agent-9"></a>
### DH-ADR-AGENT-9 Identity is an explicit, serialisable reference; the initiator is a context value
- **Decision.** `agent.Ref` names one live instance. The driver puts it in the `context.Context` it passes down; `Initiator(ctx)` reads it; `Registry.Resolve` turns it into a live agent or an error. Lifecycle, log, authority, process and wire interfaces take the reference or the agent as a parameter. A scope context has no "my agent" accessor.
- **Why.** [DH-ADR-ARCH-8](../upstream/architecture.md#dh-adr-arch-8). A reference instead of the object removes "ambient identity may outlive liveness", and a value type can be written into a call token or a plugin request.
- **Tradeoff.** A goroutine started without the context has no initiator, which is what shared timers and pumps want.
- **Serves.** [DH-O-3](../design.md#dh-o-3), [DH-O-9](../design.md#dh-o-9). **Verified by.** `DH-T-AGENT-2`, `DH-T-AGENT-3`.

<a id="dh-adr-agent-10"></a>
### DH-ADR-AGENT-10 Model selection is logged, captured once per step, and falls back to a scoped default
- **Decision.** The precedence and capture rule of section 6.2. The driver itself consults the default-model service with the agent's scope key. The service is one global component and a `Layered` registry ([DH-ADR-SESSION-10](core-session.md#dh-adr-session-10)): a preset registers its own default through its scope's registration context, and the nearest registration on the agent's scope chain wins. A route with no registered provider fails at "bind the call".
- **Why.** Upstream makes every entry point consult the default, and its default is one per process. A scoped registration lets a preset carry its own without any service being resolved through a scope.
- **Tradeoff.** The default in force when a session first runs is fixed in that session's request header; changing the default later does not move existing sessions.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-8](../design.md#dh-o-8), [DH-O-2](../design.md#dh-o-2). **Verified by.** `DH-T-AGENT-23`, `DH-T-AGENT-24`.

<a id="dh-adr-agent-11"></a>
### DH-ADR-AGENT-11 No presentation selector; one tool form; a batch contract with resource keys
- **Decision.** The selector package is dropped: the model always sees native function schemas, and one fixed pipeline runs every batch: there is no replaceable executor ([DH-ADR-TOOLS-2](tools.md#dh-adr-tools-2)). The driver hands the pipeline the whole batch and the step tool set, and receives results committed in the model's order; the cap on overlapping calls is the pipeline's own setting and has no second owner here. A tool classifies one call as `exclusive` (the default, and the answer for anything unknown or invalid), `parallel`, or `parallel` with resource keys marked read or write. A call that conflicts with a running call's key waits as a barrier, so start order stays the model's order.
- **Why.** [DH-ADR-8](../design.md#dh-adr-8) removes the `run_code` form. Upstream's note keeps a tagged mode so that "resource-aware variants" stay possible (`.agents/notes/implemented/feature/2026-07-10-parallel-tool-call-execution.md:25`); keys are that variant and remove the unary limit.
- **Tradeoff.** A tool that declares wrong keys can race; the fail-closed default limits the damage to tools that opted in. The scheduler is area A03's.
- **Serves.** [DH-O-3](../design.md#dh-o-3), [DH-O-6](../design.md#dh-o-6). **Verified by.** `DH-T-AGENT-26`.

<a id="dh-adr-agent-12"></a>
### DH-ADR-AGENT-12 A configured agent states how it starts and which preset it uses
- **Decision.** Each `agents[]` entry has a required `start` field: `fresh`, `resume-or-create` (a stable id derived from the label when none is given) or `resume`. It may name a preset. A launcher fixes an identity with an ordinary command-line overlay layer. A start that fails sends `driver.StartFailed` and does not stop its siblings.
- **Why.** Upstream's default silently creates a new session at every start, and configured agents cannot have their own persona or tools.
- **Tradeoff.** One more required field. Presets are area A27's.
- **Serves.** [DH-O-2](../design.md#dh-o-2), [DH-O-5](../design.md#dh-o-5). **Verified by.** `DH-T-AGENT-25`.

<a id="dh-adr-agent-13"></a>
### DH-ADR-AGENT-13 Units assigned here by default belong to the framework and the browser
- **Decision.** Eleven units reached this area through the inventory's type fallback and have no agent content. Six are cordis or loader internals: `internal/status` and `internal/service` are the owned subscriptions of [DH-D-CORDIS-6](../libs/cordis.md#dh-d-cordis-6); `internal/plugin` is replaced by the fiber's cancellation context ([DH-D-CORDIS-2](../libs/cordis.md#dh-d-cordis-2)); `internal/config` by the config hook ([DH-D-CORDIS-5](../libs/cordis.md#dh-d-cordis-5)); `loader/volatile-update` by the update hook ([DH-ADR-LOADER-7](../libs/cordis-loader.md#dh-adr-loader-7)); `internal/dispatch` is dropped, because its one user vetoes an event by throwing from an observer and a closed decision replaces that ([DH-ADR-ARCH-5](../upstream/architecture.md#dh-adr-arch-5)). Four are events inside upstream's browser application (`connection/reset`, `locale/change`, `slots/changed`, `theme/change`; for example `packages/api/gateway/src/client/index.ts:180`): the Go UI is rendered by the host ([DH-ADR-3](../design.md#dh-adr-3)) and a reconnect is the stream's resume rule ([DH-ADR-WIRE-3](../upstream/wire-and-api.md#dh-adr-wire-3)). The service `ctx.inspector` hosts a DevTools debugging target and a tree query interface (`docs/capability-seams.md:811`): the target is not built ([DH-ADR-CHROMEDEVTOOLSFRONTEND-1](../libs/chrome-devtools-frontend.md#dh-adr-chromedevtoolsfrontend-1)) tree queries are the loader's lookup interface ([DH-D-LOADER-8](../libs/cordis-loader.md#dh-d-loader-8)), and process inspection uses the Go profiler endpoints (`DH-LIB-NODEINSPECTOR`).
- **Why.** Each unit needs one disposition, and the reason must be findable.
- **Tradeoff.** The UI and inspector areas (C1, C2, X3) may refine these rows.
- **Serves.** [DH-O-16](../design.md#dh-o-16). **Verified by.** The traceability check.

## 8. Deliverables

Order: 1, then 2, then 3 with 4, 5 and 7; 6 alongside 3; 10 with area A03; 8 and 9 last.
A feature may be enabled when its "Done when" tests pass; none sits behind a flag.

<a id="dh-d-agent-1"></a>
### DH-D-AGENT-1 Any component can find, address and own agents through one contract
- **Result and why it matters.** Carriers, policy components and parent agents use `agent.Agents` and never import the driver; only a creator can dispose.
- **How it is implemented.** A registry service holding a map guarded by a mutex, with an incarnation counter per id for `Ref`. The factory seat is an effect, so it empties when the driver unloads. The initiator is a context key private to the package. Events about one agent are dispatched through a filter built from the agent's scope chain.
- **Components and files.** `internal/agent`: `agent.go`, `registry.go`, `ref.go`, `events.go`.
- **Depends on.** [DH-D-CORDIS-3](../libs/cordis.md#dh-d-cordis-3), [DH-D-CORDIS-4](../libs/cordis.md#dh-d-cordis-4); [DH-D-SESSION-2](core-session.md#dh-d-session-2) and [DH-D-SESSION-8](core-session.md#dh-d-session-8) for the session reader type and scopes.
- **Priority.** P0. **Outcome.** [DH-O-1](../design.md#dh-o-1), [DH-O-3](../design.md#dh-o-3). **Done when.** `DH-T-AGENT-1`, `-2`, `-3`, `-27` pass.

<a id="dh-d-agent-2"></a>
### DH-D-AGENT-2 Input can be sent at any time, is durable at once, and runs at most once
- **Result and why it matters.** A caller gets a receipt as soon as its message is in the log; a queued prompt survives a restart; no message is run twice.
- **How it is implemented.** The inbox computes a normalised splice under its mutex, appends it, queues the notice and signals the wake channel. State is read from the `inbox` projection, never from a second copy. The claim is part of the turn-opening batch.
- **Components and files.** `internal/agent/driver`: `inbox.go`; `internal/agent`: the `inbox` and `consumedWork` folds.
- **Depends on.** [DH-D-AGENT-1](#dh-d-agent-1); [DH-D-SESSION-2](core-session.md#dh-d-session-2) for event registration and the group append; [DH-D-VIEW-1](session-views.md#dh-d-view-1) for the fold registry.
- **Priority.** P0. **Outcome.** [DH-O-3](../design.md#dh-o-3), [DH-O-5](../design.md#dh-o-5). **Done when.** `DH-T-AGENT-4`, `-5`, `-6` pass under the race detector.

<a id="dh-d-agent-3"></a>
### DH-D-AGENT-3 Every turn and step is bracketed, bounded and closed on every path
- **Result and why it matters.** The log of any turn, however it ended, has matching brackets, one record per attempt and a result for every tool call. This is [DH-O-3](../design.md#dh-o-3).
- **How it is implemented.** One goroutine per agent runs the sixteen points of section 6.2 as plain sequential Go, with `closeStep` and `closeTurn` deferred. Admission reconciles the prompt against the surviving prompt nodes for the bound route and series, then appends one batch. The budget is checked before each proposed step.
- **Components and files.** `internal/agent/driver`: `driver.go`, `turn.go`, `step.go`, `admission.go`, `budget.go`.
- **Depends on.** [DH-D-AGENT-2](#dh-d-agent-2), [DH-D-AGENT-4](#dh-d-agent-4), [DH-D-AGENT-7](#dh-d-agent-7), [DH-D-AGENT-10](#dh-d-agent-10), [DH-D-CORDIS-2](../libs/cordis.md#dh-d-cordis-2). Also [DH-D-SESSION-4](core-session.md#dh-d-session-4) (the request built from the log) and [DH-D-SESSION-5](core-session.md#dh-d-session-5) (brackets and closers), [DH-D-TOOLS-5](tools.md#dh-d-tools-5) (prompt assembly), [DH-D-LLM-1](llm.md#dh-d-llm-1) (prepare, stream, failure facts) and [DH-D-STORE-4](session-store.md#dh-d-store-4) (the checkpoint policy called before each attempt and at each step end). Runtime prerequisites: a session store and at least one registered model provider.
- **Priority.** P0. **Outcome.** [DH-O-3](../design.md#dh-o-3), [DH-O-4](../design.md#dh-o-4). **Done when.** `DH-T-AGENT-5`, `-7`, `-8`, `-9`, `-10`, `-22` pass, `DH-T-AGENT-28` passes inside its budget, and [DH-E-AGENT-1](#dh-e-agent-1) and [DH-E-AGENT-4](#dh-e-agent-4) have passed.

<a id="dh-d-agent-4"></a>
### DH-D-AGENT-4 Extensions can steer a turn through three typed points and observe the rest
- **Result and why it matters.** Compaction, hooks, plan mode and retry attach without editing the loop, and none of them can take the loop down or reorder another's veto.
- **How it is implemented.** Typed keys in package `agent`; around keys with separate request and result types; the merging helper for `TurnStopping`; notices through the per-agent deliverer. Decision types are interfaces with an unexported marker method.
- **Components and files.** `internal/agent`: `events.go`, `decisions.go`; `internal/agent/driver`: `deliver.go`, `stream.go`.
- **Depends on.** [DH-D-AGENT-1](#dh-d-agent-1), [DH-D-CORDIS-4](../libs/cordis.md#dh-d-cordis-4), and [DH-D-CORDIS-7](../libs/cordis.md#dh-d-cordis-7), the around key with separate request and result types: listeners register with `On`, the driver dispatches with `Around`, and a listener's error or recovered panic reaches the driver as an error, which ends the turn as `extension/failed`.
- **Priority.** P0. **Outcome.** [DH-O-3](../design.md#dh-o-3), [DH-O-8](../design.md#dh-o-8). **Done when.** `DH-T-AGENT-3` and `DH-T-AGENT-10` to `-14` pass.

<a id="dh-d-agent-5"></a>
### DH-D-AGENT-5 A turn or a single step can be stopped, and the log says why
- **Result and why it matters.** Stop buttons, parent agents, policies and shutdown all use one call; what the user saw stays in history; nothing is left unpaired.
- **How it is implemented.** `context.WithCancelCause` at each level; the stream reader turns a cancelled stream into an interrupted prefix; the executor contract returns which calls never started.
- **Components and files.** `internal/agent`: `cause.go`; `internal/agent/driver`: `cancel.go`, `step.go`.
- **Depends on.** [DH-D-AGENT-3](#dh-d-agent-3); [DH-D-TOOLS-2](tools.md#dh-d-tools-2) and [DH-D-TOOLS-3](tools.md#dh-d-tools-3): the pipeline returns with every call settled, a body that ignores cancellation being detached after its grace period.
- **Priority.** P0. **Outcome.** [DH-O-3](../design.md#dh-o-3). **Done when.** `DH-T-AGENT-15` to `-18` pass and [DH-E-AGENT-2](#dh-e-agent-2) has passed.

<a id="dh-d-agent-6"></a>
### DH-D-AGENT-6 Agents are created, resumed and torn down as one transaction, and shutdown keeps the last events
- **Result and why it matters.** A half-created agent leaves nothing live behind; a stopped host leaves closed turns on disk.
- **How it is implemented.** A transaction object records how far creation got; its rollback is the teardown function entered at that point. Teardown is a `sync.Once` with a done channel, registered as an effect on the owner's fiber and on the driver's.
- **Components and files.** `internal/agent/driver`: `lifecycle.go`, `teardown.go`.
- **Depends on.** [DH-D-AGENT-1](#dh-d-agent-1), [DH-D-AGENT-3](#dh-d-agent-3), [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1). Also [DH-D-SESSION-5](core-session.md#dh-d-session-5) (the open that closes an interrupted tail), [DH-D-SESSION-8](core-session.md#dh-d-session-8) (scopes) and [DH-D-STORE-3](session-store.md#dh-d-store-3) (the lease; every store call takes a context, so the final flush honours its deadline).
- **Priority.** P0. **Outcome.** [DH-O-1](../design.md#dh-o-1), [DH-O-3](../design.md#dh-o-3), [DH-O-5](../design.md#dh-o-5). **Done when.** `DH-T-AGENT-19`, `-20`, `-21` pass and [DH-E-AGENT-3](#dh-e-agent-3) has passed.

<a id="dh-d-agent-7"></a>
### DH-D-AGENT-7 Each step uses one logged model selection
- **Result and why it matters.** A user or a policy can change model mid-session; the log shows which model answered each step; a retry never changes model.
- **How it is implemented.** `Model` reads the `modelSelection` fold and appends `model/selection`. An optional component, `agent.model-switch-notice`, adds a short user message through `PreStep` when the route differs from the last header.
- **Components and files.** `internal/agent`: `model.go`, the `modelSelection` fold, `notice.go`.
- **Depends on.** [DH-D-AGENT-1](#dh-d-agent-1); [DH-D-LLM-11](llm.md#dh-d-llm-11) for the request-controls type; route validation is `Runtime.Prepare` ([DH-D-LLM-1](llm.md#dh-d-llm-1)) at "bind the call".
- **Priority.** P0. **Outcome.** [DH-O-4](../design.md#dh-o-4), [DH-O-8](../design.md#dh-o-8). **Done when.** `DH-T-AGENT-23` passes.

<a id="dh-d-agent-8"></a>
### DH-D-AGENT-8 A fresh agent has a default model, and saving a new default survives a restart
- **Result and why it matters.** No entry point has to choose a model, and "make this my default" is never silently lost.
- **How it is implemented.** A component with live config. `Save` changes configuration only through boot's one edit path: it calls `config.Service.Apply` with one `Merge` operation on its own entry and the revision it read ([boot.md §6.2](boot.md#62-contracts), "The edit seam", [DH-ADR-BOOT-14](boot.md#dh-adr-boot-14)), so the cross-process lock, the static check and the shadow check apply, and it returns `Apply`'s result: `overridden` when a higher layer shadows the value, `unsaved` with the write error. It never writes a layer itself. Overlapping saves are applied in call order.
- **Components and files.** `internal/agent/defaultmodel`.
- **Depends on.** [DH-D-AGENT-7](#dh-d-agent-7), [DH-D-BOOT-7](boot.md#dh-d-boot-7) (`config.Service.Apply`), [DH-D-SESSION-8](core-session.md#dh-d-session-8). Runtime prerequisite: a profile with a writable layer.
- **Priority.** P1. **Outcome.** [DH-O-2](../design.md#dh-o-2), [DH-O-8](../design.md#dh-o-8). **Done when.** `DH-T-AGENT-24` passes.

<a id="dh-d-agent-9"></a>
### DH-D-AGENT-9 An operator declares standing agents in configuration and chooses how each starts
- **Result and why it matters.** A headless or SDK profile has its agent at start-up, with a known identity and its own preset.
- **How it is implemented.** The driver starts each entry in its own goroutine under its fiber's lifetime, through the same `Create` and `Resume` as any caller, and reports a failure as a notice.
- **Components and files.** `internal/agent/driver`: `configured.go`.
- **Depends on.** [DH-D-AGENT-6](#dh-d-agent-6); [DH-D-BUNDLE-4](bundle-preset.md#dh-d-bundle-4) for presets, at run time.
- **Priority.** P1. **Outcome.** [DH-O-2](../design.md#dh-o-2), [DH-O-5](../design.md#dh-o-5). **Done when.** `DH-T-AGENT-25` passes.

<a id="dh-d-agent-10"></a>
### DH-D-AGENT-10 A step's tools are fixed once, and its calls run in a bounded, ordered batch
- **Result and why it matters.** What the model was shown is what can run, also across retries; independent calls overlap; results arrive in the model's order.
- **How it is implemented.** The driver's side only: resolve at "propose step", pass to assembly and to `ExecuteBatch`, release at "close step". The classification type with resource keys is part of the tool contract ([tools.md §6.1](tools.md#61-shape)), and the cap on overlapping calls is the pipeline's.
- **Components and files.** `internal/agent/driver`: `toolset.go`.
- **Depends on.** [DH-D-AGENT-1](#dh-d-agent-1); [DH-D-TOOLS-1](tools.md#dh-d-tools-1) and [DH-D-TOOLS-2](tools.md#dh-d-tools-2): the tool service and the scheduler that honours keys. **Used by.** [DH-D-AGENT-3](#dh-d-agent-3).
- **Priority.** P0. **Outcome.** [DH-O-3](../design.md#dh-o-3), [DH-O-6](../design.md#dh-o-6). **Done when.** `DH-T-AGENT-26` passes.

## 9. Acceptance

### 9.1 Local tests

All use the replay provider and an in-memory or temporary store; none proves behaviour
against a real model. Time is virtual; no test sleeps. After each driver test the helper
of `DH-T-AGENT-8` runs.

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-AGENT-1` | DH-D-AGENT-1 | Create, get, list; a second live agent with one id is refused; `Get` gives no way to dispose |
| `DH-T-AGENT-2` | DH-D-AGENT-1 | A tool body reads its initiator; a shared worker reads none; `Resolve` refuses a disposed or replaced incarnation |
| `DH-T-AGENT-3` | DH-D-AGENT-1, DH-D-AGENT-4 | A listener in agent A's scope sees none of agent B's events; a global listener sees both |
| `DH-T-AGENT-4` | DH-D-AGENT-2 | Many goroutines send while another cancels: receipts match splice order, the fold equals the lists after each append, an id seen before, pending or long since claimed, is refused with the first receipt, also after a resume and in a fork, no race |
| `DH-T-AGENT-5` | DH-D-AGENT-2, DH-D-AGENT-3 | Two queued follow-ups give two turns; the second `turn/start` follows the first turn's flush; a discarded item opens no turn |
| `DH-T-AGENT-6` | DH-D-AGENT-2 | An injection while idle logs a splice and opens no turn; it is claimed at the next proposed step |
| `DH-T-AGENT-7` | DH-D-AGENT-3 | Golden file: the exact event sequence of a three-step turn with tool calls |
| `DH-T-AGENT-8` | DH-D-AGENT-3 | Replay helper: the log replayed into a fresh session gives the same history, and every request rebuilt from the log equals the one sent |
| `DH-T-AGENT-9` | DH-D-AGENT-3 | An executor failure mid-batch: `tools/not-started` and `tools/outcome-unknown` results precede `step/end`; the next request is paired |
| `DH-T-AGENT-10` | DH-D-AGENT-4, DH-D-AGENT-3 | Two failures then success: two `assistant/attempt`, one `assistant/message`, one step, users admitted once; the attempt cap ends the turn; the wait is cancellable |
| `DH-T-AGENT-11` | DH-D-AGENT-4 | A `PreStep` listener that panics ends the turn as `error` and the next turn runs; a panicking notice listener changes nothing |
| `DH-T-AGENT-12` | DH-D-AGENT-4 | `Reject` gives `blocked` with name and reason and no step; `Enter` replaces the batch; `StartsSeries` reaches the header |
| `DH-T-AGENT-13` | DH-D-AGENT-4 | `KeepOpen` from any one listener keeps the turn open in every registration order; it is not asked after abort, error or budget |
| `DH-T-AGENT-14` | DH-D-AGENT-4 | Frames: one `Start`, dense indexes, one `End` after the record is committed; `abandoned` when the append fails |
| `DH-T-AGENT-15` | DH-D-AGENT-5 | Each cause reaches `turn/end`; of two cancels the first wins |
| `DH-T-AGENT-16` | DH-D-AGENT-5 | Cancel mid-stream: an `interrupted` message with text and reasoning, no tool call; an empty prefix gives `assistant/attempt` |
| `DH-T-AGENT-17` | DH-D-AGENT-5 | Cancel mid-batch: started calls settle, the rest get aborted pairs in model order |
| `DH-T-AGENT-18` | DH-D-AGENT-5 | Step scope: with steering pending the turn continues, without it ends `aborted`; a cancel without an inbox choice is rejected |
| `DH-T-AGENT-19` | DH-D-AGENT-6 | Setup error, listener error and owner disposal each roll back: no live entry, lease released, `Disposed` only after `Created`, stored events kept; a self-wait returns an error |
| `DH-T-AGENT-20` | DH-D-AGENT-6 | The observable points of teardown occur in the order T1 to T8; three concurrent disposers share one run |
| `DH-T-AGENT-21` | DH-D-AGENT-6 | Root disposal during a turn: `turn/end{aborted, disposed}` is flushed before the lease is released; a failing or slow flush is returned with the last durable `seq` |
| `DH-T-AGENT-22` | DH-D-AGENT-3 | The step limit ends the turn as `budget` after the step closed; parked input is kept; the time limit and a per-agent override work |
| `DH-T-AGENT-23` | DH-D-AGENT-7 | Precedence of the four sources; one capture per step; a select mid-step applies next step; `agent/no-model`; the switch notice |
| `DH-T-AGENT-24` | DH-D-AGENT-8 | A default registered in a preset's scope wins for that preset's agents; `Save` calls `config.Service.Apply` and writes no layer itself; a shadowed value and a failed write are reported |
| `DH-T-AGENT-25` | DH-D-AGENT-9 | The three start modes through the real loader; a duplicate identity is a load error; one failed start notifies and the others run |
| `DH-T-AGENT-26` | DH-D-AGENT-10 | The tool set is unchanged across a retry while the registry changes; conflicting keys form a barrier; unknown is exclusive (the overlap cap itself is proven by `DH-T-TOOLS-6`) |
| `DH-T-AGENT-27` | DH-D-AGENT-1 | Composition: a stub factory replaces the driver and registry consumers run unchanged; nothing outside `internal/agent/driver` imports it |
| `DH-T-AGENT-28` | DH-D-AGENT-3 | Budget test ([DH-ADR-TOOLCHAIN-14](../upstream/toolchain.md#dh-adr-toolchain-14)): continuing a session, from a waking send on a resumed long session to the first model request leaving the driver, stays inside its budget on the heavy-tailed session corpus, and again in the memory-limited rerun. A benchmark behind the `bench` build tag with the replay provider, not a unit test |

### 9.2 Real-environment scenarios

Each needs the built `dsh` binary with a temporary harness home and a real model. Under
OWNER-2 (no API key; real-model checks only through the user's own signed-in Claude and
Copilot) the model is reached through the signed-in client routes of
[llm.md DH-D-LLM-12](llm.md#dh-d-llm-12): the user's installed official client (Claude
Code or the GitHub Copilot CLI), signed in by the user beforehand, which `dsh` starts as a
child process and never reads a token of (M1-D-PIPE-3 A). No key is supplied anywhere; the
test kit's launcher removes every credential-shaped name from `dsh`'s environment, and the
route removes them again from the client's. The four scenarios were retargeted onto these
routes by `docs/stories/harness-DSH-M1/gap-closure-plan.md` step 17.4.4 (M1-D-CLOSE-4 A).
Each runs once per route, `claude-code` and `copilot-cli`, as a subtest; the `claude-code`
leg must pass and the `copilot-cli` leg is run and its result recorded either way
(M1-D-LOOP-6 A). The controller is `go test` under the scenario build tag; it runs on the
operator's workstation, where the client is signed in as the operator (a hosted runner
cannot hold a signed-in session until gap-closure-plan 19.2 says how).

**Why the recording proxy is gone.** On a keyed route a loopback proxy between `dsh` and the
provider recorded each request body. A client route starts the vendor's own client, which
talks to its vendor itself, so no proxy can sit between them. The guarantee that what was
sent equals what the log rebuilds (SP-1) holds at the **client-invocation boundary**
(M1-D-PIPE-4 A, [llm.md DH-ADR-LLM-10](llm.md#dh-adr-llm-10)): every settlement records the
argument list `dsh` started the client with (the attempt directory written as `{attempt}`),
its digest, and the digest of the standard input `dsh` wrote. A scenario asks the host to
rebuild those from the log and compares them. That the client received exactly those bytes
is proved locally against a stand-in client (`DH-T-LLM-20`, `DH-T-LLM-24`); what the client
then sends its vendor is declared opaque.

<a id="dh-e-agent-1"></a>
#### DH-E-AGENT-1 A real multi-step turn is bracketed and reconstructable
- **Proves.** DH-D-AGENT-3, on the signed-in client route.
- **Given.** An empty temporary home; profile `headless`; no API key anywhere; the user's own installed client on the `PATH`, signed in by the user, its route (`claude-code`, then `copilot-cli`) registered by a configuration layer, and a small model; a temporary workspace holding one text file; one SDK client tool that reads it. File: `src/harness/internal/testkit/scenarios/agent_turn_scenario_test.go`, test `TestRealMultiStepTurnIsBracketed`, one subtest per route.
- **When.** Per route, the controller starts `dsh`, sends one prompt that requires reading the file, waits for idle, then asks the host twice to rebuild from the log the client invocation of every settlement.
- **Then.** The exported log holds one `turn/start` and one `turn/end{completed}`, at least two bracketed steps, one settled record per attempt and a result for every call. The two rebuilds are byte-identical; each settlement records the invocation of the route's client with a client session id of its own; the rebuilt argument list, its digest and the standard-input digest equal the recorded ones, one for one, and the rebuilt standard input digests (SHA-256, recomputed by the scenario) to the recorded digest. The `claude-code` leg must pass; the `copilot-cli` leg is recorded either way.
- **Cleanup.** Stop `dsh`; delete the home and workspace. Nothing of the user's sign-in or client settings is read or changed. On failure keep the export and the logged client invocations.

<a id="dh-e-agent-2"></a>
#### DH-E-AGENT-2 Cancelling a real stream keeps what was shown and records the cause
- **Proves.** DH-D-AGENT-5, on the signed-in client route.
- **Given.** An empty temporary home; profile `headless`; no API key anywhere; the user's own installed client, signed in, its route registered by a layer, and a small model; a prompt that produces a long answer. File: `agent_cancel_scenario_test.go`, test `TestCancelledStreamKeepsWhatWasShown`, one subtest per route.
- **When.** Per route, after the first text chunk arrives the controller cancels with cause `user` and `Keep`, waits for idle, then sends "repeat your last sentence", and asks the host twice to rebuild the client invocations from the log.
- **Then.** The first turn ends `aborted` with cause `user`; its one settlement is an `interrupted` assistant message that holds the delivered text, has no tool call and records the invocation of the client that was stopped. No chunk of the first turn is streamed after it ended (that the cancel stops the client's whole process tree is proved locally by `DH-T-LLM-23` and `DH-T-LLM-27`). Every settlement's invocation equals the one rebuilt from the log, as in DH-E-AGENT-1, and the standard input of the second turn's first attempt, rebuilt from the log and matching its recorded digest, contains the delivered text. This replaces the keyed route's proxy checks (the provider connection closed; the second request body read from the proxy). The `claude-code` leg must pass; the `copilot-cli` leg is recorded either way.
- **Cleanup.** Stop `dsh`; delete the home. Nothing of the user's sign-in is touched. On failure keep the export and the logged client invocations.

<a id="dh-e-agent-3"></a>
#### DH-E-AGENT-3 Stopping and killing the host leave an honest log
- **Proves.** DH-D-AGENT-6.
- **Given.** An empty temporary home on the real SQLite store; profile `headless`; no API key anywhere; the user's own installed client, signed in, its route registered by a layer, and a small model; a temporary workspace; the shell tool under the platform's real sandbox; a prompt that starts a shell command sleeping 60 seconds. File: `agent_stop_kill_scenario_test.go`, test `TestStopAndKillLeaveAnHonestLog`, one subtest per route with runs `A stop` and `B kill`.
- **When.** Per route. Run A: while the command runs, the controller asks the host to stop (interrupt signal; console control event on Windows). Run B: the same, but the process is killed. In both a second `dsh` then opens the home and resumes the session.
- **Then.** Run A: before any resume the database already holds a result for the call, `step/end` and `turn/end{aborted, disposed}`; no child process remains. Run B: resume appends an `outcome unknown` result and the closers; the turn is not continued. In both, sequence numbers have no gap and every settlement records the invocation of the route's client. The client runs only during an attempt and has returned the shell call unexecuted before the command starts (M1-D-PIPE-6 A), so the stop and the kill meet the shell tool's child, not the client.
- **Cleanup.** Kill leftover processes by the recorded process ids; delete the home. Nothing of the user's sign-in is touched. On failure keep the database file.

<a id="dh-e-agent-4"></a>
#### DH-E-AGENT-4 The budget stops a real tool loop
- **Proves.** DH-D-AGENT-3.
- **Given.** An empty temporary home; profile `headless` with `budget.maxSteps: 2`; no API key anywhere; the user's own installed client, signed in, its route registered by a layer, and a small model; a temporary workspace holding four small files; a prompt that needs at least four tool calls in sequence (read each file in turn). File: `agent_budget_scenario_test.go`, test `TestBudgetStopsARealToolLoop`, one subtest per route.
- **When.** Per route, the controller sends the prompt, waits for idle, then sends "continue".
- **Then.** The first turn ends as `budget{limit: steps, value: 2}` with every call paired; each call came back from the client unexecuted and was run once by the harness (one SDK tool request per `tool/result`); every settlement records the invocation of the route's client. The second turn starts from that history and makes progress.
- **Cleanup.** Stop `dsh`; delete the home and workspace. Nothing of the user's sign-in is touched. On failure keep the export.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Recommended default, not confirmed by the owner.** The budget numbers (500 steps, 8 attempts per step, no time limit) and the 10 second bounds are judgements, not measurements; unlimited is never the default.
2. **Unmeasured.** The continuation budget of `DH-T-AGENT-28` has no number yet; the first measured run on the reference machine sets it ([DH-ADR-TOOLCHAIN-14](../upstream/toolchain.md#dh-adr-toolchain-14)).
3. **How other areas use this contract** (settled, for the record). Compaction (A14) listens on `PreStep`, where it may append before `step/start`, and on `AttemptFailed`. Archive admission (A07) is a `PreStep` listener that returns `Reject`. A carrier subscribes to `agent.StreamFrame` globally and filters by `Agent`. The only exit bound is boot's `ShutdownTimeout`, reached through `launch.App.Stop` ([DH-D-BOOT-5](boot.md#dh-d-boot-5)); when it expires the process exits and resume closes the turn.
4. **Limit kept.** A notice listener that waits for its own agent, or an interception listener that never returns, stalls that agent. It is reported by name, not solved; the watchdog of [DH-D-BOOT-8](boot.md#dh-d-boot-8) names a stuck cleanup. A tool body that ignores cancellation no longer holds teardown: the pipeline detaches it ([DH-ADR-TOOLS-5](tools.md#dh-adr-tools-5)).
5. **Limit kept.** A hard process loss loses the unsettled attempt's stream, as upstream.

## 11. Units dispositioned by this spec

Rows are in `trace/fragments/A02.jsonl`.

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 4 | 3 ported, 1 dropped |
| `UP-DEBT` | 19 | 19 resolved |
| `UP-EVT` | 23 | 14 ported, 7 replaced, 2 dropped |
| `UP-CFG` | 3 | 2 ported, 1 dropped |
| `UP-SVC` | 4 | 2 ported, 2 replaced |
| `UP-DEC` (handed over) | 14 | 12 ported, 2 replaced |
