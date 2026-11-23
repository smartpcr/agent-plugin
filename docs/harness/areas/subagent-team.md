# Subagents and agent teams: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md) and are cited as `path:line` relative to the upstream root.
> Statements that are reasoning and not read from source open with "Inference". Sections 6
> to 9 are design throughout. Nothing here has been built, run or tested. IDs in this
> document use the area key `SUBAGENT`. Short citations: in the table of §3.1 a path that begins with `src/` or `README.md` is inside that row's package; everywhere else it is inside `packages/subagent/subagent/`.

| | |
|---|---|
| **Area key** | `SUBAGENT` (agent `A12`) |
| **Upstream packages** | 11: 9 in group `subagent` (the seam, six providers, two tool packages) and 2 in group `team` |
| **Serves outcomes** | [DH-O-9](../design.md#dh-o-9) (this area owns it for delegation), [DH-O-1](../design.md#dh-o-1), [DH-O-4](../design.md#dh-o-4), [DH-O-5](../design.md#dh-o-5), [DH-O-6](../design.md#dh-o-6), [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10) |
| **Depends on areas** | A02 (agent contract, inbox, teardown, budget), A01 and A05 (session log, header, lease, flush, delete), A03 (tool pipeline, prompt context), A04 (model routes, message sources), A17 (interaction registry, session grants), A10 and A09 (policy resolver, the one spawn helper), A20 (SDK protocol client, ACP wrapper), A27 (presets), A07 (settings, spill, archive activity), A19 and C5 (routes and views), A11 (workflows), X5 (the OAR provider) |
| **Priority** | P1 for the seam, the in-process providers, the tools and teams: a first working harness runs without delegation, but every shipped upstream profile has it. P2 for the external providers |

## 1. Purpose

A model working on a large task runs out of context and of patience. **Delegation** lets it hand a self-contained piece of work to another agent, a **subagent**, that works in its own conversation and gives back only a result. A **team** is the same set of agents plus one shared task board, so that several agents can divide work without stepping on each other.

This area decides who that other agent is (a fresh agent in this process, a copy that inherits the conversation, another `dsh` process, or a product the operator installed such as Claude Code or Codex), who is responsible for stopping it, how many may run, how they talk, and how a child's answer or its failure gets back. Its promise is the delegation part of [DH-O-9](../design.md#dh-o-9): every subagent has **one owner**, a **recorded lineage**, and a result that **settles only after cleanup**. Without it a delegated process can outlive the conversation that started it, a result can be lost because its parent was not looking, and nobody can say afterwards what ran on whose behalf.

## 2. Vocabulary

Terms from [../design.md §2](../design.md#2-vocabulary) and [agent-loop.md §2](agent-loop.md#2-vocabulary) (agent, handle, scope, inbox, follow-up, steer, inject, turn, lease, session grant, answerer, seam) keep their meaning.

| Term | What it is | Why it exists |
|---|---|---|
| **Subagent, child** | An agent, or an external program acting as one, that was started to do a task for another agent. | The unit of delegated work. |
| **Creator, lineage** | The creator is the agent whose tool call (or the caller whose request) started the child. Lineage is the chain of creators up to the root. | Says on whose behalf work ran. It is data; it is not ownership. |
| **Tree, root** | The root is an ordinary top-level agent; it and every agent reachable through creators form one tree. An ordinary session fork is the root of a new tree. | The unit of ownership, capacity and messaging authority. |
| **Activation, run** | One period in which a child is live: a local child's agent is in memory, or an external program is running. Each has a run id. A local child may have many over its life; an external child has one. | The thing that is owned, counted and cleaned up. |
| **Resident, cold** | A resident child has a live activation. A cold child exists only as a stored session and can be resumed. | A long conversation should not hold memory or a slot while nothing happens. |
| **Provider** | A component that knows one way of running a child. A *local* provider only says how the child session is seeded; an *external* provider starts and owns one program. | The seam: the tool the model sees does not change when the provider does. |
| **Capability** | A declared fact about a provider: continuable, inherits the conversation, accepts a preset, accepts a model choice, can ask a person. | A request the provider cannot honour is refused, never silently reduced. |
| **Slot, depth, start budget** | A slot is one live activation of a tree. Depth is the number of creators above an agent. The start budget counts activations a tree has ever started. | Three independent bounds on fan-out, recursion and total work. |
| **Tree ledger** | The log-only records of every start, settlement and dismissal in a tree, kept in the root's session log. | One durable place answers "what ran, for whom, and how it ended". |
| **Settlement** | The moment an activation's result is final: cleanup has finished and the ledger holds the outcome. | A waiter must never learn of a result while a process still runs. |
| **Notice** | A bounded user-role message that tells a creator how one of its children ended. | How a result enters the creator's model-visible history. |
| **Delivery** | `parent`: the creator gets a notice. `caller`: only the code that started the activation gets the result. | A workflow or script wants a return value, not a message. |
| **Name, path** | A short name unique among one creator's children; the path joins names from the root (`reviewer/checker`). The root is `lead`. | Models copy 36-character ids badly; the session id stays the only identity. |
| **Delegation boundary** | The rules for what crosses from creator to child at start. | Authority must not leak into a child by accident. |
| **Dismissal** | Removing a settled child from listings and from messaging, without deleting its session. | Listings stay bounded over a long conversation. |
| **Task board, revision, write scope** | The shared set of tasks of one tree, kept in the root's log. Every change names the revision it read (compare-and-set); a stale one is refused. A write scope is a workspace-relative path prefix a task expects to modify; it is advisory. | The only thing a team adds to a tree; two agents cannot overwrite each other's update. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.subagent.subagent` | `dsh-subagent` | definition | `ctx.subagents`: the named-provider registry and the activation manager that owns start, capacity, messaging, cold resume, settlement and disposal (`src/index.ts:194-199`, `src/manager.ts:162`). |
| `UP-PKG-packages.subagent.subagent-spawn-in-process` | `dsh-subagent-spawn-in-process` | provider | Provider `spawn`: a fresh child agent in this process; supports an independent preset (`src/index.ts:39-58`). |
| `UP-PKG-packages.subagent.subagent-fork-in-process` | `dsh-subagent-fork-in-process` | provider | Provider `fork`: seeds the child with the parent's completed-turn prefix (`src/index.ts:46-82`). |
| `UP-PKG-packages.subagent.subagent-dsh-sdk` | `dsh-subagent-dsh-sdk` | provider | One fresh `dsh` runtime process per task, driven over the SDK protocol; needs an isolated home (`README.md:44`). |
| `UP-PKG-packages.subagent.subagent-acp` | `dsh-subagent-acp` | provider | One fresh process per task speaking the Agent Client Protocol (ACP); answers permission requests by a fixed policy (`src/run.ts:26-27`, `:430`). |
| `UP-PKG-packages.subagent.subagent-claude-code` | `dsh-subagent-claude-code` | provider | One Claude Code query per task through the vendor SDK and a bundled executable (`README.md:32`, `:98`). |
| `UP-PKG-packages.subagent.subagent-codex` | `dsh-subagent-codex` | provider | One Codex `app-server` process, one unsaved thread and one turn per task (`src/wire.ts:187`, `:292`). |
| `UP-PKG-packages.subagent.tool-subagent` | `dsh-tool-subagent` | consumer | The delegation tool (one instance per provider and tool name) and `list_subagent_models`; owns the model-route setting `ctx.subagentModelSelection` (`src/index.ts:328-340`). |
| `UP-PKG-packages.subagent.tool-subagent-control` | `dsh-tool-subagent-control` | consumer | `send_message`, `wait_agent`, `interrupt_agent`, `list_agents` and the identity context every agent receives (`src/index.ts:86-88`, `:124-160`). |
| `UP-PKG-packages.team.agent-team` | `dsh-agent-team` | definition | `ctx.agentTeams`: the task board and the per-session Team mode (`src/index.ts:47-51`). |
| `UP-PKG-packages.team.tool-agent-team` | `dsh-tool-agent-team` | consumer | Four task tools and the `team:policy` prompt section, shown only while Team mode is on (`README.md:96`, `:127`). |

### 3.2 Behaviour

- **Registry and capabilities.** A provider registers under a unique name as an effect; removing it blocks new starts and does not revoke running children (`src/index.ts:522-540`). It declares four start-time capabilities (`agentOptions`, `agentPreset`, `outputSchema`, `depthLimit`); a request that needs a missing one is rejected (`src/index.ts:584-596`). A provider replaced while a start is pending fails that start with `PROVIDER_CHANGED` (`src/index.ts:555`).
- **Two execution shapes.** A provider with `prepareContinuable()` contributes only data (an optional history seed) and the manager creates, resumes and disposes the child agent itself. A provider with `start()` returns one execution handle and never gets a second turn (`docs/subsystems/subagent.md:343-397`). `startActivation()` returns only after the child is accepted and its handle can cancel exactly that execution (`README.md:98`).
- **Capacity and depth.** `maxActiveSubagents` (default 8) bounds live and materialising children per tree; the root uses no slot; the slot is returned only after cleanup. At capacity a start or a cold resume is **rejected, not queued**, "because a parent waiting for descendants must not wait for its own occupied slot" (`README.md:53-57`, `src/activation.ts:15-35`). `maxDepth` (default 1) is an absolute cap checked at each start; the tool stays visible at the cap (`packages/subagent/tool-subagent/README.md:54`, `:64`).
- **Ownership and lineage.** Every live instance belongs to the tree root, while creation parent ids keep the ancestry (`README.md:144`). Hosts must call `drainDescendants([root])` before disposing a root (`README.md:82`, `src/manager.ts:289-298`).
- **Names and the catalog.** Each child gets a name unique among its creator's children (`^[a-z][a-z0-9-]{0,31}$`, never `lead`, default `agent-N`), reserved synchronously (`src/names.ts:13-16`, `:84-85`). The creator's log gets one `subagent/catalog` event per child with id, mode, label and name and **no execution status** (`src/catalog.ts:155-163`, `README.md:133`). Listing a tree reads one catalog per reachable session, one after another (`README.md:248`).
- **What a child inherits.** Working directory, origin, depth, model route, and the parent's explicit sandbox-mode override. Its approval policy is pinned to `never`: an operation that needs approval is rejected automatically, and the child is told so (`src/child-agent.ts:171-177`, `:254-260`). One-shot grants are never captured (`src/child-agent.ts:249`). Persona and tools come from the preset; only `spawn` may select one (`README.md:74`).
- **Messages, waiting, interruption.** One operation, `sendMessage(sender, target, content)`: the exact live sender, a target in the same tree, always delivered by steering; only a cold target is resumed, its creators stay offline (`src/manager.ts:568`, `README.md:78`). The result is inbox acceptance, never a reply. External children reject follow-ups (`src/activation.ts:113-121`). `wait_agent` ends when input arrives in the caller's own next-step inbox, or on a timeout of 10 seconds to 1 hour (`packages/subagent/tool-subagent-control/src/wait-agent.ts:61`, `:108-110`). An ancestor or a trusted user may interrupt: a local child's turn is cancelled and its queue kept; an external run stops for good (`src/manager.ts:194-233`).
- **Settlement.** A local child settles when it is idle and its inbox is empty; it does **not** wait for its own children (`src/manager.ts:1032-1041`). The order is: capture the result, dispose the handle, release the slot, notify the parent, emit `subagent/end`, resolve the result (`src/manager.ts:1044-1121`). The notice is a user-role message with the child's closing text, sent only while the direct creator is online (`src/manager.ts:1124-1146`, `README.md:191`). A cleanup failure never replaces a captured result (`README.md:82`).
- **Model-facing surface.** Ten tools ([§6.2](#62-contracts)). The delegation tool returns `started subagent <name>` at once and never the result (`packages/subagent/tool-subagent/README.md:60`). Its guidance tells the model to end its turn instead of sleeping while it waits (`packages/subagent/tool-subagent/README.md:175`). A child model route must be authorised by a host setting read at every call (`packages/subagent/tool-subagent/README.md:77`).
- **Teams.** Every root and its descendants are a team; there is no roster. The board is a set of versioned task snapshots in the root's log; every mutation is compare-and-set; only the root reassigns; write scopes only warn (`packages/team/agent-team/README.md:65`, `:134`; `packages/team/agent-team/src/task-board.ts:129-148`, `:190`). Team mode is a saved choice on the root session, default off, and gates the four tools and the policy text (`packages/team/agent-team/README.md:28`; `packages/team/agent-team/src/index.ts:113-117`). Task changes wake nobody.
- **Config, services, events, persisted data.** Eleven config schemas, three services (`ctx.subagents`, `ctx.subagentModelSelection`, `ctx.agentTeams`), four live events (`subagent/start`, `subagent/end`, `subagent/provider-added`, `subagent/provider-removed`; `docs/event-producer-consumer.md:81-84`), and the persisted types `subagent/catalog`, `subagent/descriptor`, `team/mode`, `team/task` plus four that are only still declared (`docs/persistence-catalog.md:64-72`).

### 3.3 Failure, retry, rollback and cleanup upstream

| Situation | What upstream does | Left behind | Source |
|---|---|---|---|
| A start fails before publication | Disposes any created handle, releases the slot and the name; no catalog event. An external provider stops its process and waits for it first | A child session may already exist on disk; a process that would not die | `src/manager.ts:300-303`, `:955-961`; `packages/subagent/subagent-acp/README.md:69` |
| A child fails or is cancelled | Settles through the result with `error` or `aborted`, partial output and a fixed-word diagnostic of at most 4,096 bytes | **Uncertain:** files the child changed | `src/out-of-process.ts:20`; `packages/subagent/subagent-codex/README.md:175` |
| The creator is offline at settlement | The notice is dropped | The result exists only in a local child's own session; an external result is gone | `src/manager.ts:1128-1130` |
| A child delegates and ends its turn | It settles and is released; its children's notices then find no creator | Results that nobody reads | `src/manager.ts:1032-1041` |
| Cleanup fails | `dispose()` rejects, `subagent/end` reports `error`, the captured result is kept | **Uncertain:** the resource | `README.md:82` |
| The host dies | Nothing runs; residency and slots were in memory | A catalog entry with no outcome; an accepted prompt may be lost | `README.md:254-255` |
| A task write cannot be flushed | `TEAM_TASK_PERSISTENCE_UNCONFIRMED` with id and revision; the task is visible, durability unknown | The caller must read before retrying | `packages/team/agent-team/src/journal.ts:64-71` |

## 4. Upstream decisions dissected

The first 25 rows are the decision notes handed to this area (paths under `.agents/notes/`); the reasoning is in the ledger rows. The rest are decisions visible only in code or READMEs.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `implemented/architecture/2026-08-10-fork-children-stay-one-shot.md` | A fork child has the parent's tool list and prompt head; its task comes after the inherited history | keep | Prefix reuse is the only payoff of fork | [ADR-9](#dh-adr-subagent-9) |
| `implemented/architecture/2026-08-27-adjacent-agent-steer-messaging.md` | One `send_message`, always a steer, one durable sender attribution | keep | One operation in both directions; no report tool | [ADR-8](#dh-adr-subagent-8) |
| `implemented/architecture/2026-09-01-parent-owned-subagent-catalog.md` | Each parent's log lists its direct children | change | Runtime ownership later moved to the root; one root ledger lists a tree in one read | [ADR-2](#dh-adr-subagent-2) |
| `implemented/architecture/2026-10-13-team-identity-runtime-context.md` | Identity is required runtime context derived from the live tree | keep | Survives compaction and is right after a fork | [ADR-8](#dh-adr-subagent-8) |
| `implemented/architecture/2026-10-15-agent-teams-as-subagents.md` | Teammates are ordinary subagents; no roster | keep | One identity model | [ADR-11](#dh-adr-subagent-11) |
| `implemented/architecture/2026-10-16-tree-wide-agent-communication.md` | Listing and messaging span the tree; the root owns runtime instances | keep | It is what lets a creator leave while its child lives | [ADR-1](#dh-adr-subagent-1), [ADR-8](#dh-adr-subagent-8) |
| `implemented/architecture/2026-10-19-team-task-board-inbox.md` | The team owns tasks only; waits and messages are ordinary | keep | No second wake channel | [ADR-11](#dh-adr-subagent-11) |
| `implemented/architecture/2026-10-20-session-team-mode.md` | Team mode is a saved per-session choice, default off | keep | A profile switch changed other sessions | [ADR-11](#dh-adr-subagent-11) |
| `implemented/architecture/2026-10-22-subagent-target-user-controls.md` | A user prompt or interrupt names only the child session, which is the access resource | keep, extended | The host resumes the root first, so "needs a live root" is no longer an error | [ADR-8](#dh-adr-subagent-8) |
| `implemented/architecture/2026-10-22-subagent-tool-owned-communication.md` | The tool component owns the "report to your parent" text and shows it only when its tool is visible | keep | Guidance travels with the tool that needs it | [ADR-8](#dh-adr-subagent-8) |
| `implemented/architecture/2026-11-02-subagent-catalog-names.md` | Sibling-unique names, paths from the root, reserved at admission | keep | A model-facing fix; the name now lives in the root ledger | [ADR-4](#dh-adr-subagent-4) |
| `implemented/bug-fix/2026-10-21-subagent-end-turn-guidance.md` | Keep the sentence "end your turn instead of sleeping" | keep | Measured upstream; made true at every depth by [ADR-3](#dh-adr-subagent-3) | [ADR-3](#dh-adr-subagent-3) |
| `implemented/bug-fix/2026-10-25-shared-preset-delegation-tools.md` | A tool instance registers once in the composition that owns it | keep, plus a load check | Scoped registration is how the Go port works; unknown preset ids are now found at composition | [ADR-10](#dh-adr-subagent-10) |
| `implemented/feature/2026-07-12-subagent-persona-tool-filter-and-depth.md` | Depth is an absolute cap; persona and tool filter are restored for old children | change | The cap is kept and applies to every provider; the restore paths read old logs, which Go does not ([DH-ADR-2](../design.md#dh-adr-2)) | [ADR-4](#dh-adr-subagent-4) |
| `implemented/feature/2026-07-28-continuable-subagent-conversations.md` | One durable session, at most one live activation; the agent's own inbox is the only queue | keep | Separates the conversation from its residency | [ADR-1](#dh-adr-subagent-1) |
| `implemented/feature/2026-08-05-agent-teams.md` | A compare-and-set task board with dependencies and advisory write scopes | keep, plus owner release | Needed for shared work in any implementation | [ADR-11](#dh-adr-subagent-11) |
| `implemented/feature/2026-08-15-product-subagent-noninteractive-permissions.md` | Product providers run unattended in a mode the operator fixed; the model cannot change it | keep, extended | Authority stays with the operator; a person may answer where the product's protocol has a channel | [ADR-7](#dh-adr-subagent-7) |
| `implemented/feature/2026-08-24-user-authorized-subagent-model-routes.md` | A host setting lists the exact routes a child may be given | keep | A new model never widens cost by itself | [ADR-10](#dh-adr-subagent-10) |
| `implemented/feature/2026-09-20-spawn-agent-preset-selection.md` | An explicit preset mounts the current definition; omission joins the parent's; an event order marks which | change | The semantics stay; the marker becomes the `Preset` field of the child's header | [ADR-10](#dh-adr-subagent-10) |
| `proposed/feature/2026-07-08-interactive-side-sessions.md` | Proposal, not built: a forked advisor session whose note is merged back | adopt as composition | Needs no mechanism beyond the fork provider, caller delivery and `Inject` | [DH-D-SUBAGENT-4](#dh-d-subagent-4) |
| `implemented/feature/2026-11-17-live-subagent-model-selection-setting.md` | Routes are read live at each call, not copied into sessions | keep | No retired event to declare in Go | [ADR-10](#dh-adr-subagent-10) |
| `implemented/simplification/2026-09-17-unified-subagent-activations.md` | One entry point; a handle with identity, a result after cleanup, and exact disposal | keep | It is [DH-O-9](../design.md#dh-o-9) in upstream's words | [ADR-1](#dh-adr-subagent-1), [ADR-3](#dh-adr-subagent-3) |
| `implemented/simplification/2026-10-09-preset-owned-subagent-composition.md` | Persona and tools come from the preset; removed keys fail at load | keep | Strict decoding already rejects unknown keys | [ADR-10](#dh-adr-subagent-10) |
| `implemented/simplification/2026-10-29-inbox-only-agent-wait.md` | `wait_agent` ends only on the caller's own inbox | keep | A wait that ends with nothing to read is useless | [ADR-8](#dh-adr-subagent-8) |
| `implemented/simplification/2026-10-29-team-task-only-projection.md` | The team projection holds tasks and mode only | keep | Go has no older records to ignore | [ADR-11](#dh-adr-subagent-11) |
| Reject at capacity, no queue (`README.md:57`) | A waiting parent must not wait for its own slot | keep, plus a start budget | This repository's earlier design queued ([../../usage.md](../../usage.md), AU-DEC-2); a queue needs a lead that only dispatches, which a general agent is not | [ADR-4](#dh-adr-subagent-4) |
| Hosts call `drainDescendants` before disposing a root (`README.md:82`) | The manager cannot see the root going away | change | The tree is a fiber under the root's scope, so teardown is structural | [ADR-1](#dh-adr-subagent-1) |
| A child settles when idle with an empty inbox (`src/manager.ts:1032-1041`) | Members are independent | change | It drops the results of that child's own children | [ADR-3](#dh-adr-subagent-3) |
| The notice is sent only to an online creator (`src/manager.ts:1128-1130`) | No offline mailbox | change | The outcome is always in the ledger, and an undelivered notice is derived on resume | [ADR-2](#dh-adr-subagent-2), [ADR-3](#dh-adr-subagent-3) |
| A child's approval policy is pinned to `never` (`src/child-agent.ts:254-260`) | A background child must not wait for a person | change | With one interaction registry a person can answer; with nobody to ask the answer is still no | [ADR-6](#dh-adr-subagent-6) |
| Structured output through a child-only tool that closes input (`README.md:121`) | Reliable capture | change | A child-only tool breaks the fork prefix and cannot exist in a foreign product | [ADR-9](#dh-adr-subagent-9) |
| `maxDepth: 'provider-managed'` (`packages/subagent/tool-subagent/README.md:64`) | An external provider cannot enforce depth inside its process | drop | The check on the child being started needs no provider | [ADR-4](#dh-adr-subagent-4) |
| Product providers bundle the vendor executable (`packages/subagent/subagent-claude-code/README.md:32`) | A known version without user setup | change | Decided in the library specifications | [DH-ADR-CLAUDEAGENTSDK-1](../libs/anthropic-ai-claude-agent-sdk.md#dh-adr-claudeagentsdk-1), [DH-ADR-CODEX-1](../libs/openai-codex.md#dh-adr-codex-1) |
| Fixed-word diagnostics capped at 4,096 bytes (`src/out-of-process.ts:20`) | No child text, path or secret reaches the parent as "diagnostic" | keep | Fails closed on leakage | [ADR-9](#dh-adr-subagent-9) |

## 5. Tech debt and its resolution

Sixty-three units: the 61 bullets under "Known Limitations and Deferred Work" in the eleven READMEs, one `TODO` marker, and one found here. They are grouped by cause; each unit's own reason, and each carried unit's justification and owning stage, is in its ledger row. Short forms: `core.N` is `UP-DEBT-limit.packages.subagent.subagent.N`; `acp`, `cc`, `codex`, `sdk`, `fork`, `spawn` are the provider packages; `tool` and `ctl` are `tool-subagent` and `tool-subagent-control`; `team` and `ttool` are `agent-team` and `tool-agent-team`.

| Cause | Units | How the Go design answers it | Result |
|---|---|---|---|
| Discovery is rebuilt from many logs | `core.1`, `ctl.3`, `ctl.5`, the `TODO` at `src/assistant-output.ts:68` | One ledger projection in the root's log lists the tree in one read; listing is paged and has dismissal; the final answer is read backwards from the log, stopping at the first hit ([ADR-2](#dh-adr-subagent-2)) | 4 resolved |
| Residency is in memory only, or needs a root that may be gone | `core.5`, `core.6`, `core.7`, `core.8`, `ctl.2`, `team.4`, `team.6`, `ttool.2`, and `UP-DEBT-found.A12.1` (a child that delegates and ends its turn loses its children's results) | The inbox is a logged queue and a send returns a durable receipt (area A02); the lease makes a tree belong to one process; the tree is a fiber under the root, and the host resumes the root first; a creator owed results stays resident ([ADR-1](#dh-adr-subagent-1), [ADR-3](#dh-adr-subagent-3)) | 9 resolved |
| A lifecycle point nobody can act on | `core.9`, `ctl.4`, `tool.1`, `team.7` | An `Admit` decision before a start; a wait keyed on the inbox's log position; a `wait` argument on the delegation tool; task release when the owner's run fails ([ADR-8](#dh-adr-subagent-8), [ADR-11](#dh-adr-subagent-11)) | 4 resolved |
| Composition fixed too early or per instance | `tool.2`, `tool.3`, `tool.5`, `cc.2`, `codex.2`, `cc.3`, `fork.2`, `sdk.2`, `sdk.3` | Preset ids are checked when layers are composed and described in the schema; depth is resolved per call; one tool may offer several operator-declared targets; Claude Code gets an explicit `settings` choice; the SDK child takes preset and depth at `initialize` and may share the home ([ADR-5](#dh-adr-subagent-5), [ADR-10](#dh-adr-subagent-10)) | 9 resolved |
| Vendor executables are bundled and pinned | `cc.5`, `codex.4`, `codex.5` | The operator's installed program is used; a minimum version and a tested range replace the pin | 3 resolved |
| A child's question never reaches a person | `acp.5`, `codex.6`; `cc.6` | ACP and Codex gain an `ask` policy through the interaction registry. Claude Code's print mode has no answer channel | 2 resolved, 1 carried |
| The board is tied to one client | `team.1`, `ttool.3`; `team.8` | One event per changed task; the service accepts a user as actor. Go reads no upstream mailbox records | 2 resolved, 1 not applicable |
| One process, and one execution, per external run | `acp.1`, `cc.1`, `codex.1`, `sdk.1`, `core.3` | Kept: one process per run is what makes ownership and cleanup exact. Reduced for the `dsh` runtime child (register entry U-K1-4): a follow-up is a second run that resumes the child's session ([ADR-5](#dh-adr-subagent-5)); ACP, Codex and Claude Code children stay single-run | 5 carried; `core.3` narrowed |
| The child runs on this machine | `acp.2`, `sdk.4` | Scope boundary of [ADR-5](#dh-adr-subagent-5): a child never runs on a remote machine, because no area designs the mapping of paths, credentials and process ownership to a second machine | 2 carried |
| A foreign product has no harness preset, and results are bounded | `acp.3`, `cc.8`, `codex.8`, `acp.4`, `cc.7`, `codex.7`, `ctl.1` | Reduced: output schemas and the depth check no longer depend on the provider, and usage is recorded. Kept: no preset or model choice inside a foreign product; only the bounded final answer enters the parent's log and the child's trace is withheld (scope boundary of [ADR-9](#dh-adr-subagent-9)); a send has a receipt, not a reply | 7 carried |
| The vendor owns the account; nothing is rolled back | `cc.4`, `codex.3`, `cc.9`, `codex.9` | Reduced: every external run has a time limit and an `auth` failure category where the product states one. Kept: no login management. A cancelled run's file changes stay on its own branch when its target sets `isolation: worktree` ([ADR-12](#dh-adr-subagent-12), U-K1-3); **by default the option is off**, so in the default configuration a cancelled run's edits stay in the user's tree, and changes to systems outside the checkout are never restored | `cc.4`, `codex.3` carried; `cc.9`, `codex.9` resolved when the option is on |
| What fork and spawn mean; the provider cache | `fork.1`, `spawn.1`, `fork.3`, `tool.4` | Kept: a session is an independent log, and a cache cannot be shared across models | 4 carried |
| The tree is the authority boundary; names are permanent | `core.2`, `core.4`, `team.5` | Kept on purpose: names appear in logged messages, and cross-tree messaging needs an authority this design does not have | 3 carried |
| One shared checkout | `team.2`, `team.3`, `ttool.1` | A member's target may set `isolation: worktree`: its own branch and checkout, Git repositories only, nothing merged automatically ([ADR-12](#dh-adr-subagent-12), [DH-D-SUBAGENT-10](#dh-d-subagent-10), U-K1-3). **Off by default**: in the default configuration members still share one checkout. Kept in a shared checkout: write scopes stay advisory and the team policy text coordinates without confining | `team.2` resolved when the option is on; `team.3`, `ttool.1` carried |

## 6. Go design

### 6.1 Shape

Packages under `src/harness/internal/delegate`, following [../design.md §4.2](../design.md#42-go-module-layout). An import-direction test fails if a tool package imports a provider package.

| Package | Component | Provides | Injects | Why it exists |
|---|---|---|---|---|
| `delegate/subagent` | `subagent.manager` | Key `subagent.Key` (`Runtime`); key `subagent.RoutesKey`; types, events, errors; the ledger event types and projection | `agent.Agents` (A02), session store and projections (A01, A05), interactions (A17), settings (A07) | The contract and the one activation manager |
| `delegate/subagent/local` | `subagent.spawn`, `subagent.fork` | Two `Preparer` providers | `subagent.Key` | The only difference between them is the seed |
| `delegate/subagent/external` | none (library) | The one-run lifecycle shared by every external provider: start, publish, settle, stop | subprocess service (A09) | Three library specifications asked for exactly one copy of it |
| `delegate/subagent/sdkchild`, `.../acp`, `.../claudecode`, `.../codex` | `subagent.sdk-child`, `subagent.acp`, `subagent.claude-code`, `subagent.codex` | One `Runner` provider per configured instance | `subagent.Key`; the Go client package of module `src/sdk` and A20's ACP client role | External providers; the OAR provider is area X5's and plugs into the same interface |
| `delegate/subagent/tools` | `tool.subagent` (one entry per instance, `tool.subagent@<instance>`), `subagent.tools` | The six subagent tools, the identity and depth context, the delegation guidance | `subagent.Key`, tools and prompt assembly (A03), LLM runtime (A04) | Consumers; a preset that omits them pays no prompt tokens |
| `delegate/team`, `delegate/team/tools` | `team.board`, `team.tools` | Key `team.Key` (`Board`); four task tools and the policy section | `subagent.Key`, session store | The task board |

```go
type Runtime interface {
    Register(owner *cordis.Context, p Provider) (*cordis.EffectHandle, error) // unique name; gone with its owner
    Start(ctx context.Context, s Spec) (Activation, error)   // returns once the child is published
    Send(ctx context.Context, from agent.Ref, to session.ID, c Content) (agent.Receipt, error) // model-authored
    Prompt(ctx context.Context, by Principal, to session.ID, m UserMessage, d Delivery) (agent.Receipt, error) // human
    Interrupt(ctx context.Context, by Authority, target session.ID) error // Authority: ancestor agent.Ref or owner Principal; a target that is not running is a success with no effect, as for the tool
    Dismiss(ctx context.Context, by Authority, target session.ID) error
    Tree(of session.ID, page Page) (TreeView, error)         // ledger snapshot plus live activity; per run: stop, usage, duration
    Resolve(caller agent.Ref, name string) (session.ID, error) // grants no authority
    RootOf(member session.ID) (session.ID, error)
    WaitQuiet(ctx context.Context, root agent.Ref) error     // for headless carriers: no member is working
}
type Spec struct {
    Provider, Label, Name, Cwd, Preset string
    Creator  agent.Ref
    Prompt   Content
    Model    *agent.ModelSelection // needs an authorised route
    Output   *schema.Schema        // any JSON root
    Delivery Delivery              // Parent | Caller
}
type Activation interface {
    Child() session.ID; Run() RunID; Name() string
    Result(ctx context.Context) (Result, error) // blocks until settled; never earlier
    Dispose(ctx context.Context) error          // stops and releases exactly this run
}
type Result struct {
    Stop StopReason // completed | aborted | error | max-tokens | refusal | start-failed | interrupted
    Output Content; Structured json.RawMessage; Diagnostic string; Usage *Usage; Cleanup CleanupState
}
type Provider interface{ Info() ProviderInfo } // Name, Capabilities, Requires []Prerequisite
type Preparer interface { Provider; Prepare(ctx context.Context, r PrepareRequest) (Seed, error) } // data only
type Runner interface { Provider; Run(ctx context.Context, r RunRequest) (ExternalRun, error) }
type ExternalRun interface {
    Wait(ctx context.Context) (Result, error)
    Stop(ctx context.Context, cause error) error // returns when the whole process tree has gone
}
```

`RunRequest` carries the resolved directory, the task, the time limit, and `Ask func(ctx, Question) (Answer, error)`: the only way an external provider reaches a person. `Capabilities` is `{Continuable, InheritsContext, Preset, ModelOptions, CanAsk}`.

**Who owns what.** Inference: the structure below takes "an activation is a fiber" from this repository's earlier design ([../../usage.md](../../usage.md) §7.2) and "the root owns runtime instances" from upstream.

```
root agent's scope fiber                    (area A02; disposing it disposes everything below)
└─ tree fiber                               created at the root's first delegation; holds slots, names, the start count
   ├─ activation "reviewer"                 effects, in order: slot, agent handle, cancel
   ├─ activation "reviewer/checker"         its creator is reviewer: lineage is a field, not nesting
   └─ activation "codex-1"                  effects: slot, external run and its process tree, cancel
```

### 6.2 Contracts

**Activation lifecycle.** One per-child lock serialises start, delivery, release and resume, so a send that meets a child being released waits and then resumes it. "Record" means one append to the root's log followed by a flush ([ADR-2](#dh-adr-subagent-2)).

| # | Point | What happens | Recorded | On failure |
|---|---|---|---|---|
| A1 | Admit | Bind the exact provider instance; check capabilities, depth, the route and the preset; run `subagent.Admit` | nothing | An error result; nothing was created |
| A2 | Reserve | Under the tree mutex: the name, one slot, one unit of the start budget | nothing | `subagent/at-capacity`, `subagent/start-budget`, `subagent/name-taken` |
| A3 | Record | The start is written and flushed **before** any session or process exists | `subagent/started` | The flush fails closed: nothing starts |
| A4 | Materialise | Create the activation fiber. Local: `Prepare`, then create or resume the agent with the delegation boundary applied. External: `Run` until the program has accepted the task | child header and policy events (local) | The fiber is disposed (a process is stopped and awaited; an unpublished child session is deleted), then `subagent/settled{start-failed}` |
| A5 | Accept | Local: the task is sent as a follow-up with the creator as source | the child's inbox splice | As A4 |
| A6 | Publish | `Started` is dispatched; `Start` returns | nothing | n/a |
| S1 | Close | Admission closes. A local child settles by itself when it is idle, has no waking input, has no pending interaction and is **owed no result** by a child of its own | nothing | n/a |
| S2 | Stop | Only when asked (dispose, interrupt of an external run, time limit, teardown): cancel with the cause and wait for quiescence | the child's own closers (A02) | Reported; continues to S3 |
| S3 | Capture | Local: the last non-empty assistant message after the run's start. External: `Wait`. Bound the text; validate the output schema | nothing | The result is `error` with a diagnostic |
| S4 | Release | Dispose the agent handle, or stop the process tree | the child's final flush | `Cleanup: failed`; the captured result is kept |
| S5 | Record | The outcome is written and flushed | `subagent/settled` | Retried by the next append; the result stays unreleased until it is durable |
| S6 | Free | The slot returns to the tree | nothing | n/a |
| S7 | Deliver | The notice goes to the creator; `Settled` is dispatched; `Result` unblocks | the creator's inbox splice | The notice stays derivable from the ledger |

**Providers.** Each row answers the four questions of the brief: what process exists, who owns it, how a child's approval or question reaches a person, and what must be installed.

| Provider | Process | Owner | Follow-up | Approvals and questions | Must be installed |
|---|---|---|---|---|---|
| `spawn` | None: an agent in the host | The activation fiber, through the agent handle | Yes; cold resume | Registered in the host's interaction registry for the child session and shown to the **root's answerer** with the child's path; no answerer means no | Nothing |
| `fork` | None | As `spawn` | Yes | As `spawn` | Nothing |
| `dsh-sdk` | One `dsh --profile sdk` child per run, by default the running executable | The run, through the spawn helper | No | The child host sends interaction requests over the SDK protocol; the provider forwards them through `Ask` | Nothing |
| `acp` | The configured ACP agent, one per run | The run | No | `permission: reject` (default), `allow`, or `ask` (forward `session/request_permission` through `Ask`) | The agent and its login |
| `claude-code` | The user's `claude` in print mode | The run | No | None: the print mode has no answer channel; the mode is fixed by the operator, default `dontAsk` | Claude Code 2.1.259 or later, signed in |
| `codex` | The user's `codex app-server` | The run | No | `never` (default), `approve-for-me`, the bypass mode, or `ask`: the approval requests upstream declines (`packages/subagent/subagent-codex/src/wire.ts:574-633`) go through `Ask` | Codex 0.153.4 or later, signed in |
| `oar` (area X5) | The operator's Node running the operator's OAR, one bridge child per run | The run | No | An approval or input request ends the run ([botiverse-oar.md](../libs/botiverse-oar.md), `DH-LIB-OAR-R4`) | Node 24, `@botiverse/oar`, each native agent |

A missing prerequisite never fails host start-up: the provider registers, `Info().Requires` says what is missing, and a delegation fails with `subagent/unavailable` before any task text is sent. The message names of the OAR bridge protocol are fixed as listed in [botiverse-oar.md §5](../libs/botiverse-oar.md#dh-adr-oar-1).

**The delegation boundary** ([ADR-6](#dh-adr-subagent-6)).

| Crosses to the child | Does not cross |
|---|---|
| Working directory, captured at A2 (an explicit `Cwd` resolves against it) | Session grants, in either direction and for every provider |
| Depth: the creator's plus one, in the child header | One-call approvals already given to the creator |
| The creator's effective sandbox mode, as a cap the child cannot exceed, logged in the child's log as set by delegation | The creator's agent-local tool restrictions |
| The model route, unless an authorised one is chosen | A call token ([DH-ADR-8](../design.md#dh-adr-8)) |
| The preset: the creator's live revision, or the one named (`spawn` only) | The conversation, except through `fork` |
| The right to **ask the root's answerer** | An answerer of its own |

**Tools exposed to the model.** All pass the guarded pipeline (area A03) and are shown only while their precondition holds. Results are bounded as stated.

| Upstream unit | Go tool | What the model can do | Bounded result |
|---|---|---|---|
| `UP-TOOL-dsh-tool-subagent.subagent` | `subagent` (the instance's name: `subagent_fork`, `subagent_claude_code`, ...) | Start a child with `description`, `prompt`, optional `name`; optional `target` (when the instance lists several), `provider`+`model`+`reasoning_effort` (authorised routes only), `agent_preset` (allow-list), and `wait` | Without `wait`: `{kind: activation, name}`, fixed size. With `wait: true`: stop reason, diagnostic and the answer cut to `noticeMaxBytes` with a spill locator; cancelling the call disposes the child |
| `UP-TOOL-dsh-tool-subagent.list_subagent_models` | `list_subagent_models` | See the providers, models and efforts it may route a child to | At most 100 rows and a cursor |
| `UP-TOOL-dsh-tool-subagent-control.send_message` | `send_message` | Steer `message` to `agent` (a path, `lead` or a session id) in its tree | `{messageId}`; the message is at most `maxMessageBytes` |
| `UP-TOOL-dsh-tool-subagent-control.wait_agent` | `wait_agent` | Block until its own next-step inbox changes, or `timeout_ms` (10,000 to 3,600,000, default 30,000) | `{timedOut}` |
| `UP-TOOL-dsh-tool-subagent-control.interrupt_agent` | `interrupt_agent` | Ask a descendant to stop its current work | One line; an absent target is an accepted no-op |
| `UP-TOOL-dsh-tool-subagent-control.list_agents` | `list_agents` | List the tree: `name`, `status` (`running`, `inactive`, `settled`, `missing`), `description`; optional `cursor`, `limit`, `include_dismissed` | At most `listLimit` rows (default 50, maximum 200) and a cursor |
| `UP-TOOL-dsh-tool-agent-team.team_task_create` | `team_task_create` | Add an unowned task with `subject`, `description`, `blocked_by`, `write_scopes` | One task view |
| `UP-TOOL-dsh-tool-agent-team.team_task_get` | `team_task_get` | Read one task | One task view |
| `UP-TOOL-dsh-tool-agent-team.team_task_list` | `team_task_list` | Filter by `status`, `owner` or `unowned`, `ready`; page with `cursor`, `limit` | At most 100 task views |
| `UP-TOOL-dsh-tool-agent-team.team_task_update` | `team_task_update` | `claim`, `release`, `edit`, `set_dependencies`, `complete`, `reopen`, `reassign` (root only), `delete`, each with `expected_revision` | One task view, or `team/stale-revision` with the current revision |

**Messages, waits and notices.** `Send` requires a live sender and a target in the same tree that is local and not dismissed; it steers, resumes a cold target (one slot, one unit of start budget), and returns the receipt of area A02, which is durable. A model-authored message is framed `Agent <name> sent a message:` with source `agent-message{sender}`. `wait_agent` remembers the inbox's last log position it reported per outer call, so an edited message ends the next wait. A notice is a user-role message with source `subagent-settled{run, child, stop}`: one line naming the child, its closing text cut to `noticeMaxBytes` (the rest behind a spill locator), the structured value, and the diagnostic. The closing text is omitted when the child's last action was a message to that creator. It is a follow-up for an idle creator, a steer for a running one, and an injection for one that is closing.

**Events.** Dispatch forms are those of [../design.md §4.6](../design.md#46-event-model); tree events are dispatched through the root's scope, so a listener on the root sees its whole tree.

| Upstream unit | Go key and form | Payload and timing |
|---|---|---|
| `UP-EVT-subagent/start` | `subagent.Started`, notify | `{Run, Child, Creator, Root, Name, Provider}`, at A6 |
| `UP-EVT-subagent/end` | `subagent.Settled`, notify | The same plus `{Stop, Cleanup, Seq}`, at S7, after the record |
| `UP-EVT-subagent/provider-added`, `UP-EVT-subagent/provider-removed` | `subagent.ProvidersChanged`, notify | `{Name, Added}`. Tools need no listener: visibility is resolved per step |
| new | `subagent.Admit`, around | `AdmitRequest{Spec, Depth, Tree}` to `Allow{}` or `Deny{Reason}` |
| new | `subagent.Progress`, notify | Unlogged activity of an external run, for a view |
| new | `team.TaskChanged`, notify | `{Root, Task, Revision}`: one task, not the board |

**Config.** Go structs decoded strictly, defaults as a Go value ([DH-ADR-SCHEMA-1](../libs/zod.md#dh-adr-schema-1)); every field is live unless it names a program.

| Upstream unit | Go struct | Fields (default) |
|---|---|---|
| `UP-CFG-dsh-subagent` | `subagent.Config` | `maxActive` (8), `maxDepth` (1), `maxStartsPerTree` (200; 0 means unlimited and must be written), `noticeMaxBytes` (32,768), `childApprovals` (`ask-root`; or `deny`) |
| `UP-CFG-dsh-subagent-spawn-in-process` | `local.SpawnConfig` | `name` (`spawn`) |
| `UP-CFG-dsh-subagent-fork-in-process` | `local.ForkConfig` | `name` (`fork`) |
| `UP-CFG-dsh-subagent-dsh-sdk` | `sdkchild.Config` | `name` (`dsh-sdk`), `binary` (the running executable), `profile` (`sdk`), `layers`, `home` (required: `same` or an absolute path), `model`, `env`, `maxDuration` (1h), `stopGrace` |
| `UP-CFG-dsh-subagent-acp` | `acp.Config` | `name` (`acp`), `command` (required), `args`, `permission` (`reject`), `env`, `maxDuration` (1h), `stopGrace` |
| `UP-CFG-dsh-subagent-claude-code` | `claudecode.Config` | `name` (`claude-code`), `executable` (from `PATH`), `model`, `permissionMode` (`dontAsk`), `settings` (`native`; or `bare`), `env`, `maxDuration` (1h), `stopGrace` |
| `UP-CFG-dsh-subagent-codex` | `codex.Config` | `name` (`codex`), `executable`, `model`, `permissionMode` (`never`), `env`, `maxDuration` (1h), `stopGrace` |
| `UP-CFG-dsh-tool-subagent` | `tools.DelegateConfig` | `tool` (`subagent`), `provider` or `targets[]` (`alias`, `provider`, `description`), `modelSelection` (false), `modelDiscovery` (false), `model`, `preset`, `allowedPresets`, `maxDepth` (the host value) |
| `UP-CFG-dsh-tool-subagent-control` | `tools.ControlConfig` | `interrupt` (true), `listLimit` (50), `maxMessageBytes` (65,536) |
| `UP-CFG-dsh-agent-team` | `team.Config` | `maxTasks` (256), `defaultEnabled` (false), `releaseOnOwnerFailure` (true) |
| `UP-CFG-dsh-tool-agent-team` | none | The component has no settings; a key in its entry is a load error |

**Services.**

| Upstream unit | Go replacement |
|---|---|
| `UP-SVC-subagents` | Key `subagent.Key`, interface `Runtime` ([§6.1](#61-shape)) |
| `UP-SVC-subagentModelSelection` | Key `subagent.RoutesKey`: `Allowed() (routes []Route, enabled bool, err error)`, read from the live setting `subagent-model-selection` at every call |
| `UP-SVC-agentTeams` | Key `team.Key`, interface `Board`: `Mode`, `SetMode`, `Create`, `Get`, `List`, `Update`, each taking an actor that is an agent or an owner principal. `SetMode(actor, on, confirmed)` returns `team/confirm-needed` when the root session has already recorded a model request and `confirmed` is false, because the switch changes the tools of a conversation in progress; the board decides this from the session's turn index, so a caller never has to ask "has this session made a request" itself |

**Persisted data.** All log-only, state-carrying, with a payload version, written to the **root's** log: `subagent/started` (run, child, creator, the creating tool call, name, label, provider, kind, depth, route), `subagent/settled` (run, stop, diagnostic, bounded output or its locator, structured value, usage, cleanup, how the run was confined), `subagent/dismissed`, `team/mode` and `team/task` (the whole task with its revision). Two folds are registered: `delegation` (members, runs, names, counts) and `teamBoard`; each session also folds `noticedRuns` from its own messages. Records below a fork's inherited count are ignored, so a fork starts an empty tree. Upstream's `subagent/catalog` and `subagent/descriptor` have no Go type: the first is replaced by `subagent/started`, the second by the child header fields of area A01 and the child's own log.

**Team rules.** A task is `pending`, `in_progress` or `completed`, and may be deleted (a tombstone). It is ready when every blocker is completed. Any member creates, reads and claims a ready unowned task; the owner or the root edits and moves it; only the root reassigns. Dependencies must form a directed acyclic graph. A mutation validates under the board's mutex, appends, and flushes; a failed flush returns `team/persistence-unconfirmed` with the id and revision. When `releaseOnOwnerFailure` is on and the ledger shows the owner's latest run settled as anything but `completed`, or the owner was dismissed, the board appends a release revision naming the cause. Views show the owner's name and whether the owner is resident.

**Model-facing text owned here.** The delegation guidance (with upstream's end-turn sentence unchanged), the depth notice, the identity context (`You are a subagent delegated by agent "lead". Your name is "reviewer". ...`), the delegation statement and the team policy. The delegation statement changes one fact: an operation that needs approval is shown to the person watching the root conversation, and is denied when nobody is there. Identity and the delegation statement are required runtime context (area A03), so they survive compaction and follow inherited history.

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership, access, lineage.** The root's scope fiber owns the tree fiber; the tree fiber owns every activation; an activation owns its slot, its agent handle or external run, and its cancel function, released in the reverse order. The creator owns nothing: it has *access* (it may message, interrupt and, with `Delivery: Caller`, dispose). A started `wait` call additionally ties the activation to that tool call's context. The operator's programs, their logins and the workspace files are shared or the user's; the harness never deletes them. Nothing is retried on the model's behalf: a delegation has side effects.

| Situation | What happens | Undone, and what stays uncertain |
|---|---|---|
| A start fails at A4 or A5 | The fiber is disposed, the slot freed, `start-failed` recorded; the name is free again | **Uncertain:** nothing, unless the process would not die; then its id is logged and the instance refuses new runs until it has gone |
| A provider is unloaded | New starts fail with `subagent/no-provider`. Its external runs are stopped with it: the provider component's fiber holds a second handle on the same memoised stop, so provider code never outlives its component. Local children continue | Results settle as `aborted` |
| A child fails, is refused or hits its token ceiling | S1 to S7 with that stop reason and partial output | **Uncertain:** files it changed |
| An external run exceeds `maxDuration` | Cancelled with cause `budget`; settles as `aborted` with the fixed deadline diagnostic | As above |
| Interrupt | Local: the turn is cancelled, the inbox kept, the child stays. External: the run stops and settles | Claimed input is not put back (A02) |
| A person does not answer a routed approval | The child waits; it holds its slot. The interaction ends as denied when the tree is torn down or the child is interrupted | Nothing |
| The creator is not resident at S7 | The ledger has the outcome. When that creator is next resumed, every settled run it created that is absent from its `noticedRuns` is injected | Nothing is lost; the notice is late |
| The root is disposed or its host shuts down | The tree fiber closes admission, cancels pending starts and disposes every activation with cause `parent`, in parallel; S5 lands in the root's log before the root's final flush (A02 teardown step T6) | A tool body or external program that ignores the stop delays shutdown; it is named in the log |
| The host is killed | No teardown. External children die with it (job object, or parent-death signal, from the spawn helper). When the root is resumed, each run with a start and no settlement gets `subagent/settled{interrupted}` and a notice | **Uncertain:** whether the child finished; the notice says so and tells the model to verify |
| Another process opens a member | The lease refuses it (`subagent/busy-elsewhere`); the tree is operated only by the holder of the root's lease | Nothing |
| A member's session is deleted | `Tree` shows `missing`; a send fails with `subagent/not-found` | The ledger row stays until dismissed |

## 7. Decisions

<a id="dh-adr-subagent-1"></a>
### DH-ADR-SUBAGENT-1 One manager; an activation is a fiber in a tree the root owns; lineage is data
- **Decision.** Every way of running a child goes through `Runtime.Start`. Each activation is a fiber under a per-tree fiber that hangs off the root agent's scope. Its slot, handle or external run, and cancel function are effects. The creator is recorded and owns nothing. A continuable child is one durable session with at most one live activation, and the agent's own inbox is its only queue.
- **Why.** Ownership (who stops it), lineage (for whom) and access (who may talk to it) are three questions upstream answers in three places. One owner per tree lets a creator leave while its child lives, and makes root teardown stop the whole tree without a host calling `drainDescendants`.
- **Tradeoff.** Activations are siblings, so teardown is not ordered leaves first; it does not need to be, because no member waits for another during a forced stop. A tree lives in the process that holds the root's lease.
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-1](../design.md#dh-o-1). **Verified by.** `DH-T-SUBAGENT-1`, `DH-T-SUBAGENT-2`, `DH-T-SUBAGENT-9`, [DH-E-SUBAGENT-2](#dh-e-subagent-2).

<a id="dh-adr-subagent-2"></a>
### DH-ADR-SUBAGENT-2 The tree ledger lives in the root's session log
- **Decision.** Starts, settlements and dismissals of a whole tree are log-only events in the root's log, folded by one projection. A start is recorded and flushed before anything is created.
- **Why.** The root is the one session that is always writable while its tree runs. One log answers listing, name lookup, the start count, crash recovery and, because the task board is in the same log, task release. Upstream's per-parent catalog needs one read per member and holds no outcome.
- **Tradeoff.** A child's own children are not listed in the child's log, so a member opened without its root shows no tree; the root is found by walking `Creator` headers. A failed start costs two small records. Deleting the root deletes the ledger with it.
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-5](../design.md#dh-o-5). **Verified by.** `DH-T-SUBAGENT-5`, `DH-T-SUBAGENT-6`, `DH-T-SUBAGENT-7`.

<a id="dh-adr-subagent-3"></a>
### DH-ADR-SUBAGENT-3 A result settles in one fixed order, is durable before anyone is told, and a creator owed results stays resident
- **Decision.** The order S1 to S7 of [§6.2](#62-contracts), run by one memoised function per activation. A local child does not settle while a child it created with parent delivery is unsettled. After a crash, open runs are closed as `interrupted`, and an undelivered notice is derived from the ledger and the creator's own log.
- **Why.** "Settles only after cleanup" must hold for every path: natural end, dispose, interrupt, time limit, teardown. Upstream tells a model to end its turn while waiting, and then releases a child that does so before its own children report.
- **Tradeoff.** A waiting creator keeps its slot, so a deep tree fills sooner; with the default depth of 1 only the root waits, and it uses no slot. Inference: giving the slot back while waiting, as this repository's earlier design does, was not chosen because a woken creator would then need a slot that may be gone.
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-5](../design.md#dh-o-5), [DH-O-4](../design.md#dh-o-4). **Verified by.** `DH-T-SUBAGENT-3`, `DH-T-SUBAGENT-4`, `DH-T-SUBAGENT-8`, [DH-E-SUBAGENT-1](#dh-e-subagent-1), [DH-E-SUBAGENT-2](#dh-e-subagent-2).

<a id="dh-adr-subagent-4"></a>
### DH-ADR-SUBAGENT-4 Three bounds, refused and never queued; names are held by the ledger
- **Decision.** Live activations per tree (8), depth (1) and starts per tree (200) are checked at A1 and A2 for every provider and for cold resumes. Over a bound the call fails at once with a message the model can act on. Names follow upstream's rule and are unique for ever among one creator's children, except that a start that never published frees its name.
- **Why.** A slot limit alone does not bound total work. A queue would make a parent wait for a slot its own child holds. Depth is a fact about the child being started, so no provider needs to enforce it.
- **Tradeoff.** The model must handle a refusal. Inference: 200 is a judgement. What a foreign product delegates inside itself is not counted.
- **Serves.** [DH-O-9](../design.md#dh-o-9). **Verified by.** `DH-T-SUBAGENT-10`, `DH-T-SUBAGENT-11`, `DH-T-SUBAGENT-12`.

<a id="dh-adr-subagent-5"></a>
### DH-ADR-SUBAGENT-5 Two provider shapes, declared capabilities, and one run lifecycle for every external provider
- **Decision.** A provider is a `Preparer` (data only) or a `Runner` (one owned run). Capabilities are declared and checked at A1. All external providers use the `external` package for start, publish, settle and stop, and the one spawn helper of area A09. One delegation tool may list several operator-declared `targets`. **A follow-up to an external child is a second run** (register entry U-K1-4; recommended default, not confirmed by the owner): the `dsh` runtime provider (`subagent.sdk-child`) declares `Continuable`, because its child has a durable session. A message to such a child after its run settled starts a new process that resumes the child's session and takes the message as its task; the ledger gets a new `subagent/started` with the same child id and `continues` naming the previous run. One process per run, its ownership and its cleanup are unchanged. The child's session is therefore kept after a run and is deleted with its root. ACP children become continuable later, where the agent says it can load a session; Codex and Claude Code stay single-run, because their library decisions turn saved sessions off. **A child never runs on a remote machine** (recommended default, not confirmed by the owner): every provider starts its child on the host's machine, because no area designs the mapping of paths, credentials and process ownership to a second machine; a remote workspace is reached through the execution world of the child's tools, not by moving the child.
- **Why.** A local provider that never sees the agent cannot leak it. One lifecycle is the request of three library specifications, and it is where "stop, wait, then report" is proven once. Targets give the model a choice among instances without letting it change one.
- **Tradeoff.** A provider that needs a third shape is a change to the contract. An external child still gets one process per run, so a follow-up pays a process start and a session resume. Scope boundary of this record: a foreign product (an ACP agent, Codex or Claude Code) runs with its own operator configuration and takes no harness preset and no per-call model option, because the harness cannot compose prompts, tools or models inside another product, and the model must not change a foreign agent's settings.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-9](../design.md#dh-o-9). **Verified by.** `DH-T-SUBAGENT-13`, `DH-T-SUBAGENT-14`, `DH-T-SUBAGENT-21`.

<a id="dh-adr-subagent-6"></a>
### DH-ADR-SUBAGENT-6 Session grants never cross the delegation boundary; a child asks the root's answerer
- **Decision.** The rule is the owner's and is decided ([DH-ADR-16](../design.md#dh-adr-16) as amended by [DH-ADR-33](../design.md#dh-adr-33)): a session grant is never copied. It lives and dies with the one live session it was given to; it does not survive a resume, does not pass to a fork and does not pass to a subagent. A subagent's request goes to whoever answers for the root session and is denied if nobody does. Applied here: a grant is not copied to, consulted for, or restored in any child (spawn, fork or external), and a grant given on a child's own prompt belongs to that child's session alone, never to its creator or siblings, and ends when the child's activation is released. A fork child's inherited log contains the creator's grant events; they are below its inherited count and are ignored. An approval or question raised by an in-process child is registered for the child's session, and the manager binds every child `Via` its creator in A17's registry ([interaction-credentials.md §6.2](interaction-credentials.md#62-contracts)), so the request is offered to the answerer bound to the tree's root, labelled with the child's path. When nobody answers for the root, the request settles `unavailable` and the call is denied. `childApprovals: deny` restores upstream's automatic refusal without asking.
- **Why.** A standing yes was given for one conversation the user was watching. Routing the question, instead of the grant, keeps a person in the decision.
- **Tradeoff.** The same prompt may appear once per child. A child waiting for a person holds a slot. A resumed tree starts with no grant anywhere in it.
- **Serves.** [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-SUBAGENT-15`, `DH-T-SUBAGENT-16`, [DH-E-SUBAGENT-3](#dh-e-subagent-3).

<a id="dh-adr-subagent-7"></a>
### DH-ADR-SUBAGENT-7 External products run as operator-installed programs in an operator-fixed mode
- **Decision.** Claude Code, Codex, ACP agents and OAR runtimes are started through the spawn helper with a scrubbed environment plus the instance's overlay. The permission mode is part of the instance configuration; no tool argument reaches it. A person is asked only where the product's protocol has an answer channel (ACP, Codex, the SDK child). Every instance has a time limit. By default the process is **not** inside the harness sandbox: the product applies its own mode, and the settled record says `confined: by-product`. An external child is an untrusted process tree for the peer rule of the control channel even when it runs unconfined ([host-telemetry.md](host-telemetry.md), `DH-ADR-HOST-12`): it is never an owner peer. Each external provider entry carries `disabled: !expr launch.safe`, so a host started in safe mode starts none of them ([boot.md](boot.md), `DH-ADR-BOOT-12`).
- **Why.** [DH-ADR-23](../design.md#dh-adr-23) and the three library decisions fix "installed, not bundled". A model must not be able to widen a foreign agent's authority.
- **Tradeoff.** Delegating to a product trusts that product, its settings in the workspace and the operator's choice of mode; this is a stated residual gap ([../design.md §6](../design.md#6-security-model)). Claude Code cannot ask a person.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-SUBAGENT-21` to `DH-T-SUBAGENT-23`, [DH-E-SUBAGENT-4](#dh-e-subagent-4), [DH-E-SUBAGENT-5](#dh-e-subagent-5).

<a id="dh-adr-subagent-8"></a>
### DH-ADR-SUBAGENT-8 One steer operation with tree-scoped authority; waits read only the caller's inbox
- **Decision.** Keep upstream: one `Send`, always a steer; the sender must be live and in the target's tree; interruption needs an ancestor or an owner principal; a user prompt names only the child session. `wait_agent` observes the caller's next-step inbox and nothing else. Identity and communication guidance are required runtime context contributed by the control-tool component.
- **Why.** Authority by tree membership needs no roster. A wait that ends without input gives the model nothing to act on.
- **Tradeoff.** No message crosses trees, and a send has a receipt, never a reply.
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-6](../design.md#dh-o-6). **Verified by.** `DH-T-SUBAGENT-17` to `DH-T-SUBAGENT-20`.

<a id="dh-adr-subagent-9"></a>
### DH-ADR-SUBAGENT-9 Results are bounded, diagnostics are fixed words, and output schemas are checked by the manager
- **Decision.** A notice and a `wait` result carry at most `noticeMaxBytes` of the answer and a locator for the rest. A diagnostic is built from fixed words and closed categories, at most 4,096 bytes. An output schema may have any JSON root; the child is asked for a final answer that is one JSON value, the manager validates it at S3, and a continuable child gets one repair message. There is no child-only tool. Scope boundary of this record: from an external child (ACP, Codex, Claude Code, OAR) only the final answer text, its structured value and usage are collected; the child's own trace of steps and tool calls stays with the product and is withheld from the parent's log.
- **Why.** A child-only tool changes the request head, which defeats the fork prefix, and cannot exist in a foreign product. Checking at S3 works for every provider.
- **Tradeoff.** Free-text JSON is less reliable than tool arguments on weaker models; area A04 may later supply a provider's native structured mode behind the same field.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-4](../design.md#dh-o-4). **Verified by.** `DH-T-SUBAGENT-8`, `DH-T-SUBAGENT-14`, `DH-T-SUBAGENT-24`.

<a id="dh-adr-subagent-10"></a>
### DH-ADR-SUBAGENT-10 Child routes come from a live host setting; persona and tools come from the preset
- **Decision.** Keep upstream: an explicit child route must be in the host's allow-list, read at each call; with the setting off only the creator's current route is allowed. Persona and tools are the preset's. `spawn` may name a preset; `fork` inherits and offers no route fields by default. Preset ids in a tool's configuration are checked when layers are composed. The cap on depth is resolved per call: the tool instance's value, else the host's.
- **Why.** Cost and capability must not grow because a model was registered. A composed tree can be checked before it runs ([DH-O-2](../design.md#dh-o-2)).
- **Tradeoff.** Replaying a session does not reproduce its authorisation, as upstream accepted. Preset descriptions in the schema change the request head when edited. Scope boundary of this record: a fork copies the creator's completed turns once, when it starts, and never follows the creator's later turns, and a spawn starts with an empty conversation; this is a product choice because a child is its own independent log, and a single copy keeps the provider's cached prefix.
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-SUBAGENT-25`, `DH-T-SUBAGENT-26`.

<a id="dh-adr-subagent-11"></a>
### DH-ADR-SUBAGENT-11 A team is the tree plus a task board in the root's log
- **Decision.** No roster, no mailbox, no agents of its own. A plain tree already has identity, names, messaging, waiting, interruption and capacity; Team mode adds four tools, one policy section and the board. Mode is a saved per-session choice, default off. Tasks are whole snapshots changed by compare-and-set. A task whose owner's run failed is released by a recorded revision.
- **Why.** Upstream reached this after removing a second roster and a second wake channel. Release on failure is possible because ledger and board share one log.
- **Tradeoff.** Write scopes stay advisory in a shared checkout: a shell command can still write anywhere the sandbox allows, and the views keep saying "not enforced". Members that must not collide are started with `isolation: worktree` ([DH-ADR-SUBAGENT-12](#dh-adr-subagent-12)). Task changes wake nobody; an agent that must act is sent a message. Two things are scope by this decision (register entry U-K2-6; recommended default, not confirmed by the owner): a person cannot start a member from the team view, because a start needs a creator agent and its bounds, and a member cannot be renamed, because the name is the ledger's key for messages. The message timeline a supervisor misses is a read model: [session-views.md](session-views.md) `Query.TreeMessages`.
- **Serves.** [DH-O-9](../design.md#dh-o-9). **Verified by.** `DH-T-SUBAGENT-27` to `DH-T-SUBAGENT-30`, [DH-E-SUBAGENT-6](#dh-e-subagent-6).

<a id="dh-adr-subagent-12"></a>
### DH-ADR-SUBAGENT-12 A delegation target may isolate its child in a Git worktree
- **Decision.** The owner's resolution of register entry U-K1-3 ([DH-ADR-40](../design.md#dh-adr-40)). A target of a delegation tool may be configured with `isolation: worktree`; the default is `none`, and the model cannot set it. It works only in a Git repository and only where the worktree entry of [x-forge-worktree.md](x-forge-worktree.md) is mounted ([DH-ADR-XFORGE-8](x-forge-worktree.md#dh-adr-xforge-8)); otherwise the start is refused at A1 with `subagent/isolation-unavailable` and nothing starts. At A4, before the child exists, the manager calls the worktree seam, `worktree.Service.ForChild(activation, parent, {Run, Name})`, which pins the creator's current commit, creates a branch named after the child and the run with its checkout, as an effect of the child's activation fiber, and returns path, branch and base commit. The child, local or external, is then created with that path as its working directory, so no live session is moved and the worktree is its only write root: write scopes then need no separate enforcement. The settled record names the branch and the path. **Nothing is merged automatically**: the creator, or the person, reads the branch and decides. **The default stays as before**: with the option off, which is the default, a child works in its creator's directory, members of a team share one checkout, write scopes are advisory, and a cancelled external run leaves its edits in the user's tree. The option is built as its own deliverable, [DH-D-SUBAGENT-10](#dh-d-subagent-10), after the worktree seam.
- **Why.** Five carried units had one cause, a child working in its creator's directory: members overwrote each other, a write scope was only a warning, and a cancelled Claude Code or Codex run left half its edits in the user's tree. Upstream rejected a worktree for every subagent, and that stays rejected; an option the operator turns on per target was never rejected.
- **Tradeoff.** The delegation area gains a dependency on an experimental area. A child does not see its creator's uncommitted changes, and the creator does not see the child's work until it reads the branch. Branches and checkouts accumulate until they are removed. Effects outside the checkout (a network call, a file elsewhere) are never undone.
- **Failure and cleanup.** Creating the worktree fails: the start fails as `start-failed` and the seam rolls its attempt back. The run fails or is cancelled: its changes stay on its own branch, which is the rollback; the user deletes the branch. On release of the activation the seam's effect removes a clean checkout and deletes the branch only if it still points at the base commit; a checkout with changes and a branch with commits are kept and named in the settled record; a host crash leaves a named remainder that the seam's journal reports ([DH-D-XFORGE-2](x-forge-worktree.md#dh-d-xforge-2)).
- **Serves.** [DH-O-9](../design.md#dh-o-9), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-SUBAGENT-32`, [DH-E-SUBAGENT-7](#dh-e-subagent-7).

## 8. Deliverables

<a id="dh-d-subagent-1"></a>
### DH-D-SUBAGENT-1 Any component can delegate through one seam, and every child has one owner
- **Result and why it matters.** `Runtime.Start` returns an activation whose result arrives only after cleanup; disposing a root stops its tree. No caller writes its own cleanup path.
- **How it is implemented.** The `subagent.manager` component provides `subagent.Key`. The manager keeps one tree record per root (mutex, resident map, pending starts) and creates the tree fiber as an effect of the root's scope. Lifecycle points A1 to A6 and S1 to S7 are one function each; settlement is memoised per activation and reached from every trigger.
- **Components and files.** `delegate/subagent`. **Depends on.** [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1) (a provider outlives its dependents' cleanup). [DH-D-AGENT-1](agent-loop.md#dh-d-agent-1), [DH-D-AGENT-5](agent-loop.md#dh-d-agent-5) and [DH-D-AGENT-6](agent-loop.md#dh-d-agent-6): create, resume, handle, maintenance task, typed cancel causes.
- **Priority.** P1. **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-1](../design.md#dh-o-1).
- **Done when.** `DH-T-SUBAGENT-1` to `-4`, `-9` and `-14` pass and [DH-E-SUBAGENT-2](#dh-e-subagent-2) has passed. Amended by the M1 story's decision M1-D-LOOP-3 (B): `DH-T-SUBAGENT-4` (the grandchild wait), `DH-T-SUBAGENT-14` (the capability check at A1 and the output-schema check at S3) and the interrupt and time-limit legs of `DH-T-SUBAGENT-3`, with its clause on the S5 record, move to [DH-S-29](../impl.md#dh-s-29), steps 29.1 to 29.4, which build the ledger, notices, providers and steering they need; they are deferred there with `//dsh:test ... later DH-S-29` in `delegate/subagent/doc.go`. Step 11.6 closes on the other tests of this list and the natural-end, dispose and owner-teardown legs of the settle-order golden.

<a id="dh-d-subagent-2"></a>
### DH-D-SUBAGENT-2 The root's log says what ran, for whom and how it ended, also after a crash
- **Result and why it matters.** One projection lists a tree; a killed host leaves an honest record; a creator that was away still learns of its children's results.
- **How it is implemented.** Three event types and the `delegation` fold registered with the session area; record-then-flush at A3 and S5; a resume hook that closes open runs and derives missing notices; bounded notice text with the spill store.
- **Components and files.** `delegate/subagent` (ledger). **Depends on.** [DH-D-SUBAGENT-1](#dh-d-subagent-1). [DH-D-SESSION-2](core-session.md#dh-d-session-2): event registration, the header's inherited count for the fold, a flush that honours its context, and `session.Created` with `How: resumed` as the on-resume hook. [DH-D-STORE-6](session-store.md#dh-d-store-6): deleting an unpublished child session. [DH-D-STORAGE-5](storage.md#dh-d-storage-5): spill.
- **Priority.** P1. **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-5](../design.md#dh-o-5).
- **Done when.** `DH-T-SUBAGENT-5` to `-8` pass and [DH-E-SUBAGENT-2](#dh-e-subagent-2) has passed.

<a id="dh-d-subagent-3"></a>
### DH-D-SUBAGENT-3 Fan-out, recursion and total work are bounded, and children have short names
- **Result and why it matters.** A runaway model cannot start unlimited agents; a refusal says which bound and what to do.
- **How it is implemented.** Counters in the tree record under its mutex, fed from the ledger fold at resume; name validation and reservation in the same critical section; the depth notice as runtime context.
- **Components and files.** `delegate/subagent`. **Depends on.** [DH-D-SUBAGENT-2](#dh-d-subagent-2).
- **Priority.** P1. **Outcome.** [DH-O-9](../design.md#dh-o-9).
- **Done when.** `DH-T-SUBAGENT-10` to `-12` pass.

<a id="dh-d-subagent-4"></a>
### DH-D-SUBAGENT-4 A child can run in this process, fresh or with the conversation, under a strict boundary
- **Result and why it matters.** The two providers every profile uses; a child cannot inherit authority by accident; a fork reuses the provider's cached prefix. A side session for a person is the fork provider with caller delivery and an injected note, with no further mechanism.
- **How it is implemented.** `spawn` returns an empty seed; `fork` returns the creator's completed-turn prefix through the session area's fork. The manager applies the boundary table before the child is published and bridges the child's interactions to the root's answerer.
- **Components and files.** `delegate/subagent/local`. **Depends on.** [DH-D-SUBAGENT-1](#dh-d-subagent-1). [DH-D-SESSION-7](core-session.md#dh-d-session-7): fork from a stored prefix. [DH-D-HUMAN-1](interaction-credentials.md#dh-d-human-1): a child bound `Via` its creator, so that its interactions reach the root's answerer. [DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1): a sandbox mode cap set by delegation. [DH-D-BUNDLE-4](bundle-preset.md#dh-d-bundle-4): composing the child from a preset.
- **Priority.** P1. **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-7](../design.md#dh-o-7), [DH-O-4](../design.md#dh-o-4).
- **Done when.** `DH-T-SUBAGENT-13`, `-15`, `-16` and `-24` pass and [DH-E-SUBAGENT-1](#dh-e-subagent-1) and [DH-E-SUBAGENT-3](#dh-e-subagent-3) have passed.

<a id="dh-d-subagent-5"></a>
### DH-D-SUBAGENT-5 Agents in a tree can message, wait for, interrupt and list each other, and a person can address a child
- **Result and why it matters.** Collaboration without a relay through parents; a user can open a child, prompt it and stop it.
- **How it is implemented.** `Send`, `Prompt`, `Interrupt`, `Dismiss`, `Tree`, `Resolve` on the manager; four tools and the identity context in `subagent.tools`; root-first resume for a cold target; an `ActivityProvider` (area A07) that reports and stops a tree when its session is archived.
- **Components and files.** `delegate/subagent`, `delegate/subagent/tools`. **Depends on.** [DH-D-SUBAGENT-2](#dh-d-subagent-2), [DH-D-SUBAGENT-3](#dh-d-subagent-3). [DH-D-AGENT-2](agent-loop.md#dh-d-agent-2) (durable receipt, wake during cancellation), [DH-D-TOOLS-5](tools.md#dh-d-tools-5) (required context). **Used by.** Routes that declare the child session as their resource ([DH-D-API-5](api.md#dh-d-api-5)) and by the tree view ([DH-D-UIWORK-4](ui-work.md#dh-d-uiwork-4)).
- **Priority.** P1. **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-6](../design.md#dh-o-6).
- **Done when.** `DH-T-SUBAGENT-17` to `-20` pass and [DH-E-SUBAGENT-1](#dh-e-subagent-1) has passed.

<a id="dh-d-subagent-6"></a>
### DH-D-SUBAGENT-6 The model, and a script, can delegate with one tool per target
- **Result and why it matters.** The tool the model sees; with `wait`, the primitive a workflow script calls through the tool command line ([DH-ADR-8](../design.md#dh-adr-8)), with the same owner and lineage as a direct call.
- **How it is implemented.** One component per configured instance registers its tool in the composition that owns it, visible while its provider (or any target) is registered. Routes are checked against `RoutesKey` and the LLM runtime before A1. In `wait` mode the creator is the initiator of the call and the activation is tied to the call's context. A target's `isolation` field is accepted here only as `none`; the value `worktree` is [DH-D-SUBAGENT-10](#dh-d-subagent-10)'s, and without that deliverable a target that sets it fails at configuration load. In the model fields (`provider`, `model`, `reasoning_effort`, `agent_preset`) an empty string means "absent", and an optional argument that does not affect the start is accepted and ignored (`DH-LIB-ZOD-R13`).
- **Components and files.** `delegate/subagent/tools`. **Depends on.** [DH-D-SUBAGENT-1](#dh-d-subagent-1), [DH-D-SUBAGENT-3](#dh-d-subagent-3). [DH-D-TOOLS-1](tools.md#dh-d-tools-1) (scoped registration, per-step visibility), [DH-D-LLM-1](llm.md#dh-d-llm-1) (route validation), [DH-D-STORAGE-8](storage.md#dh-d-storage-8) (the setting), [DH-D-BUNDLE-4](bundle-preset.md#dh-d-bundle-4) (preset ids and descriptions). **Used by.** Workflow members, which start through these tools ([DH-D-PTC-5](ptc-workflow.md#dh-d-ptc-5)).
- **Priority.** P1. **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-6](../design.md#dh-o-6).
- **Done when.** `DH-T-SUBAGENT-25`, `-26` and `-31` pass and [DH-E-SUBAGENT-1](#dh-e-subagent-1) has passed.

<a id="dh-d-subagent-7"></a>
### DH-D-SUBAGENT-7 A child can be another process: a `dsh` runtime or any ACP agent
- **Result and why it matters.** Isolation from the host process, and a path to any product with an ACP mode.
- **How it is implemented.** The `external` lifecycle; `sdkchild` drives the SDK protocol client and passes route, preset, depth and creator at `initialize`; `acp` drives the client role of `acpwire`. Both forward interaction requests through `Ask`. `sdkchild` is `Continuable` ([DH-ADR-SUBAGENT-5](#dh-adr-subagent-5), register entry U-K1-4): a follow-up is a new run whose process calls `session/resume` on the child's stored session and then prompts it. The catalogue names of the external providers are `subagent.sdk-child`, `subagent.acp`, `subagent.claude-code` and `subagent.codex` (one entry per configured instance, `@<instance>` for a further one), each with `disabled: !expr launch.safe`.
- **Components and files.** `delegate/subagent/external`, `sdkchild`, `acp`. **Depends on.** [DH-D-SUBAGENT-1](#dh-d-subagent-1); library decision [DH-ADR-ACPSDK-1](../libs/agentclientprotocol-sdk.md#dh-adr-acpsdk-1). [DH-D-SDK-8](sdk-acp-typert.md#dh-d-sdk-8): the Go client package of the module `src/sdk` (`sdk.Connect`), which is the only thing this provider imports to drive a child `dsh` (no protocol type or codec is declared in `src/harness`), with the `initialize` fields for preset, depth and creator. [DH-D-SDK-6](sdk-acp-typert.md#dh-d-sdk-6): interaction requests sent to the client. [DH-D-SDK-9](sdk-acp-typert.md#dh-d-sdk-9): the ACP client role. [DH-D-EXEC-1](execution.md#dh-d-exec-1) and [DH-D-EXEC-2](execution.md#dh-d-exec-2): the spawn helper and stop ladder.
- **Priority.** P2. **Outcome.** [DH-O-10](../design.md#dh-o-10), [DH-O-9](../design.md#dh-o-9).
- **Done when.** `DH-T-SUBAGENT-21`, `-22` and `-33` pass and [DH-E-SUBAGENT-4](#dh-e-subagent-4) has passed.

<a id="dh-d-subagent-8"></a>
### DH-D-SUBAGENT-8 A child can be Claude Code or Codex as the user installed them, and OAR plugs into the same interface
- **Result and why it matters.** Delegation to the two products upstream ships providers for, with nothing bundled; area X5's bridge needs no contract of its own.
- **How it is implemented.** As specified in the two library documents (locator, version probe, argument builder or five-call client, strict acceptance), on the `external` lifecycle. Codex's `ask` mode answers approval requests through `Ask`. Usage is copied to the settled record when the product reports it.
- **Components and files.** `delegate/subagent/claudecode`, `codex`. **Depends on.** [DH-D-SUBAGENT-7](#dh-d-subagent-7); [DH-ADR-CLAUDEAGENTSDK-1](../libs/anthropic-ai-claude-agent-sdk.md#dh-adr-claudeagentsdk-1), [DH-ADR-CODEX-1](../libs/openai-codex.md#dh-adr-codex-1), [DH-ADR-OAR-1](../libs/botiverse-oar.md#dh-adr-oar-1). **Used by.** [DH-D-XHOOKS-7](x-hooks-mods-terminal.md#dh-d-xhooks-7), which builds the `oar` provider on this interface. Runtime prerequisites: the products, installed and signed in.
- **Priority.** P2. **Outcome.** [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7).
- **Done when.** `DH-T-SUBAGENT-23` passes (it runs the library checks `DH-T-CLAUDEAGENTSDK-1` to `-5` and `DH-T-CODEX-1` to `-4` against these providers) and [DH-E-SUBAGENT-5](#dh-e-subagent-5) has passed.

<a id="dh-d-subagent-9"></a>
### DH-D-SUBAGENT-9 A tree can share a durable task board
- **Result and why it matters.** Several agents divide work with ownership, ordering and no lost update.
- **How it is implemented.** The `team.board` component provides `team.Key`; two event types and the `teamBoard` fold; a board mutex per root; tools and the policy section shown while the root's mode is on; the release rule as a derivation from the two folds, run on settlement and at resume.
- **Components and files.** `delegate/team`, `delegate/team/tools`. **Depends on.** [DH-D-SUBAGENT-2](#dh-d-subagent-2), [DH-D-SUBAGENT-5](#dh-d-subagent-5). **Used by.** The board view and its controls, [DH-D-UIWORK-5](ui-work.md#dh-d-uiwork-5).
- **Priority.** P1. **Outcome.** [DH-O-9](../design.md#dh-o-9).
- **Done when.** `DH-T-SUBAGENT-27` to `-30` pass and [DH-E-SUBAGENT-6](#dh-e-subagent-6) has passed.

<a id="dh-d-subagent-10"></a>
### DH-D-SUBAGENT-10 A delegation target can isolate its child in its own Git branch and checkout
- **Result and why it matters.** Members that must not collide each work on their own branch, and a cancelled external run leaves the user's tree untouched (register entry U-K1-3). It is off by default; with it off, delegation behaves as [DH-D-SUBAGENT-6](#dh-d-subagent-6) describes.
- **How it is implemented.** [DH-ADR-SUBAGENT-12](#dh-adr-subagent-12): the `isolation: worktree` value of a target, read from the instance's configuration and never from tool arguments; at A1 the check for a Git repository and a mounted worktree entry, refusing with `subagent/isolation-unavailable`; at A4 `worktree.Service.ForChild` before the child exists, and the child created with the returned path as its working directory (a local child's header `Cwd`, an external run's directory); branch and path in `subagent/settled`; release through the seam's effect.
- **Components and files.** `delegate/subagent` (the start path), `delegate/subagent/tools` (the target option). **Depends on.** [DH-D-SUBAGENT-6](#dh-d-subagent-6), [DH-D-SUBAGENT-8](#dh-d-subagent-8) (external targets, on which its scenario cancels a run), [DH-D-XFORGE-1](x-forge-worktree.md#dh-d-xforge-1) and [DH-D-XFORGE-2](x-forge-worktree.md#dh-d-xforge-2) (the worktree seam and its cleanup).
- **Priority.** P2: it needs the experimental worktree entry. **Outcome.** [DH-O-9](../design.md#dh-o-9), [DH-O-7](../design.md#dh-o-7).
- **Done when.** `DH-T-SUBAGENT-32` passes and [DH-E-SUBAGENT-7](#dh-e-subagent-7) has passed. A defect could leave or remove a checkout, so that scenario runs in this deliverable's build stage.

## 9. Acceptance

### 9.1 Local tests

Unit and composition tests with a scripted model, fake providers, a fake answerer and stand-in executables, under the race detector and virtual time. They prove this area's handling, not a vendor's behaviour.

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-SUBAGENT-1` | D-1 | `Start` returns only after publication; `Result` blocks until S7; a second `Dispose` is a no-op |
| `DH-T-SUBAGENT-2` | D-1 | Disposing the root disposes every activation, frees every slot and leaves no goroutine, with no call from the host |
| `DH-T-SUBAGENT-3` | D-1 | Golden order S1 to S7 for natural end, dispose, interrupt, time limit and teardown; `Settled` never precedes the record |
| `DH-T-SUBAGENT-4` | D-1 | A child that delegated and went idle is not released until its child settles; it then wakes with the notice |
| `DH-T-SUBAGENT-5` | D-2 | The start record is durable before any session or process exists; a failed flush starts nothing |
| `DH-T-SUBAGENT-6` | D-2 | A start that fails at A4 or A5 leaves a `start-failed` record, no session, no process, a free slot and a free name |
| `DH-T-SUBAGENT-7` | D-2 | Resuming a root whose ledger has open runs closes each as `interrupted` once; a fork ignores inherited records |
| `DH-T-SUBAGENT-8` | D-2 | A notice is cut at `noticeMaxBytes` with a locator; a creator resumed later receives each missing notice exactly once |
| `DH-T-SUBAGENT-9` | D-1 | Concurrent sends, a release and a resume of one child are serialised; no send is lost or answered with a transient error |
| `DH-T-SUBAGENT-10` | D-3 | At capacity, at the depth cap and over the start budget a start and a cold resume are refused with the matching code; a live change of the limit applies to the next start |
| `DH-T-SUBAGENT-11` | D-3 | Name rule, default `agent-N`, duplicate refused, two concurrent starts never share a name |
| `DH-T-SUBAGENT-12` | D-3 | The depth check applies to an external provider; the depth notice appears only at the cap |
| `DH-T-SUBAGENT-13` | D-4 | A `Preparer` never receives an agent or handle; the fork seed ends at the last completed turn |
| `DH-T-SUBAGENT-14` | D-1 | A request for a capability the provider lacks is refused at A1; an invalid structured answer gives `error` |
| `DH-T-SUBAGENT-15` | D-4 | A grant in the creator's session does not allow the same call in a spawn child, a fork child or after a cold resume; a grant in a child does not allow it in the creator; a child's prompt reaches the root's answerer and is denied when the root has none |
| `DH-T-SUBAGENT-16` | D-4 | A child's approval reaches the root's answerer with the child's path; with no answerer, and with `childApprovals: deny`, it is denied |
| `DH-T-SUBAGENT-17` | D-5 | A send from outside the tree, to self, to an external child or to a dismissed child is refused; a cold target is resumed and its creator is not |
| `DH-T-SUBAGENT-18` | D-5 | `wait_agent` ends on new next-step input and on an edit of reported input, and not on next-turn input, another inbox or a task change |
| `DH-T-SUBAGENT-19` | D-5 | Interrupt by a non-ancestor is refused; a local target keeps its queue; an external target settles as `aborted` |
| `DH-T-SUBAGENT-20` | D-5 | `list_agents` pages a tree of 500 members in stable order from one projection read; dismissed members are hidden |
| `DH-T-SUBAGENT-21` | D-7 | The external lifecycle against a stand-in that starts a grandchild and ignores a polite stop: every exit path ends with both processes gone before the result |
| `DH-T-SUBAGENT-22` | D-7 | A scripted SDK child and a scripted ACP agent: an interaction request reaches `Ask`; `permission: reject` and `allow` never call it |
| `DH-T-SUBAGENT-23` | D-8 | Codex `ask` forwards each known approval request and fails closed on an unknown one; no tool argument changes a permission mode |
| `DH-T-SUBAGENT-24` | D-4 | A fork child's first request has the same system prompt and tool schemas, byte for byte, as its creator's latest request |
| `DH-T-SUBAGENT-25` | D-6 | An unauthorised route is refused before A1; with the setting off only the creator's route passes; an edit applies to the next call |
| `DH-T-SUBAGENT-26` | D-6 | An unknown preset id in a tool's configuration is a composition diagnostic with file and line |
| `DH-T-SUBAGENT-27` | D-9 | A stale revision is refused with the current one; a cycle, an unready claim and a non-root reassign are refused |
| `DH-T-SUBAGENT-28` | D-9 | With Team mode off the four tools and the policy are absent for the whole tree, and a task call is refused |
| `DH-T-SUBAGENT-29` | D-9 | An owner whose run settles as `error` loses its task by one recorded revision, also when found at resume |
| `DH-T-SUBAGENT-30` | D-9 | A failed flush returns `team/persistence-unconfirmed`; a task change wakes no agent |
| `DH-T-SUBAGENT-31` | D-6 | `wait: true` through the tool endpoint returns the bounded result; cancelling the outer call disposes the child; the ledger names the calling agent as creator |
| `DH-T-SUBAGENT-32` | D-10 | With a fake worktree seam: a target with `isolation: worktree` calls `ForChild` before the child exists, creates the child in the returned path and records branch and path; the model cannot set the option; outside a Git repository, or with the seam unmounted, the start is refused with `subagent/isolation-unavailable` and nothing starts; a failed creation is `start-failed`; release removes a clean checkout and keeps a changed one; nothing is merged |
| `DH-T-SUBAGENT-33` | D-7 | A message to a settled `dsh` runtime child starts a second run in a new process that resumes the child's session; the ledger links it with `continues`; the first process is gone before the second starts; an ACP, Codex or Claude Code child refuses the follow-up with `subagent/not-continuable` |

### 9.2 Real-environment scenarios

<a id="dh-e-subagent-1"></a>
#### DH-E-SUBAGENT-1 A real model delegates, is told the results, and a fork reuses the prefix
- **Proves.** DH-D-SUBAGENT-4, DH-D-SUBAGENT-5, DH-D-SUBAGENT-6.
- **Given.** The built `dsh` binary, an empty temporary home, a model API key supplied at run time, a scratch workspace with two small files, depth 2.
- **When.** A controller on the SDK protocol asks the root to delegate one file to `subagent` and one to `subagent_fork`, and to have the first child delegate a check to a child of its own; it waits for the tree to be quiet.
- **Then.** The root's log holds three `subagent/started` and three `subagent/settled` records and two notices; the intermediate child's log holds its child's notice, later than its own first turn end; the root's final answer cites both results. The provider's usage for the fork child's first request reports cached input tokens (recorded, not asserted, where the provider gives none).
- **Cleanup.** Delete the home and the workspace; on failure keep the database.

<a id="dh-e-subagent-2"></a>
#### DH-E-SUBAGENT-2 Stopping and killing the host leave no child and an honest ledger
- **Proves.** DH-D-SUBAGENT-1, DH-D-SUBAGENT-2.
- **Given.** The built `dsh` binary, an empty temporary home on the real SQLite store, a model API key supplied at run time, a scratch workspace, the shell tool under the platform's real sandbox, and the in-process `subagent` tool (spawn provider only, so nothing beyond [DH-D-SUBAGENT-4](#dh-d-subagent-4) is needed); a task that makes the root start three children that each run a 60-second command.
- **When.** Run A: the host is asked to stop while they run. Run B: the host process is killed. A second `dsh` then resumes the root.
- **Then.** Run A: before any resume the root's log has a settlement for every start, and no process started by the host remains. Run B: no child process remains; resume appends three `interrupted` settlements and one notice each; nothing is continued.
- **Cleanup.** Kill leftovers by recorded process id; delete the home.

<a id="dh-e-subagent-3"></a>
#### DH-E-SUBAGENT-3 A child's approval reaches a person in a real browser, and a grant does not follow the child
- **Proves.** DH-D-SUBAGENT-4.
- **Given.** The web profile in a real browser, a real model, a real sandbox backend in `workspace-write`; the user has granted "write outside the workspace in directory D" for the root session.
- **When.** The root delegates a task that writes into D. The user answers the prompt once with yes. The scenario is repeated with no browser attached.
- **Then.** The child's write raises a prompt that names the child; the root's grant did not cover it. With no browser the write is denied and the child reports the limit.
- **Cleanup.** Delete D and the home.

<a id="dh-e-subagent-4"></a>
#### DH-E-SUBAGENT-4 A task runs in a second `dsh` process and in a real ACP agent
- **Proves.** DH-D-SUBAGENT-7.
- **Given.** One third-party ACP agent installed and signed in by the operator; providers `dsh-sdk` (`home: same`) and `acp` (`permission: ask`); a browser attached.
- **When.** One task goes to each; the ACP task needs a file write; a third is cancelled mid-run.
- **Then.** The SDK child's transcript is a session in the same home with the root as creator. The ACP agent's permission request appears in the browser and its answer decides the write. The cancelled run is `aborted` and leaves no process.
- **Cleanup.** Delete the workspace and home. Nothing was installed by the test.

<a id="dh-e-subagent-5"></a>
#### DH-E-SUBAGENT-5 Claude Code and Codex, as installed, through one tool with two targets
- **Proves.** DH-D-SUBAGENT-8.
- **Given.** Both products installed and signed in; one delegation tool with targets `claude` (`dontAsk`) and `codex` (`ask`). It extends `DH-E-CLAUDEAGENTSDK-1` and `DH-E-CODEX-1`.
- **When.** The same copy-a-file task goes to each target; then the Claude Code path is pointed at a missing file.
- **Then.** Each result is `completed` with a settled record that holds usage and `confined: by-product`; the Codex approval was answered in the browser; the last delegation fails with `subagent/unavailable` and the host keeps running.
- **Cleanup.** Delete the workspace. The products' own state is the user's and is left alone.

<a id="dh-e-subagent-6"></a>
#### DH-E-SUBAGENT-6 Two real agents share a task board
- **Proves.** DH-D-SUBAGENT-9.
- **Given.** A real model, Team mode on, two tasks of which the second is blocked by the first.
- **When.** The root starts two members; each is told to claim what is ready; the member owning the first task is interrupted and dismissed.
- **Then.** No task has two owners; the blocked task is claimed only after the first completes; the dismissed owner's task is released by a recorded revision and claimed again.
- **Cleanup.** Delete the home.

<a id="dh-e-subagent-7"></a>
#### DH-E-SUBAGENT-7 An isolated child works on its own branch, and a cancelled one leaves the user's tree untouched
- **Proves.** [DH-D-SUBAGENT-10](#dh-d-subagent-10) (the isolation option of [DH-ADR-SUBAGENT-12](#dh-adr-subagent-12)). It needs the worktree seam ([DH-D-XFORGE-1](x-forge-worktree.md#dh-d-xforge-1)) and an external child provider ([DH-D-SUBAGENT-8](#dh-d-subagent-8)) built.
- **Given.** A real Git repository with one committed file and one uncommitted change; the built `dsh` with the worktree entry mounted; a real model; two delegation targets with `isolation: worktree`, one local and one external (Codex, installed and signed in), and one target without it; a real sandbox backend in `workspace-write`.
- **When.** (1) The root delegates "edit the file and commit" to the local isolated target and waits. (2) It delegates a long edit to the external isolated target and the controller cancels it half-way. (3) It delegates to the target without isolation from a directory that is not a Git repository, and then to an isolated target from that directory.
- **Then.** (1) The committed change is on a branch named after the run and nowhere else; the settled record names branch and path; the user's working tree, with its uncommitted change, is byte-identical. (2) The half-made edits are in the run's checkout only; the user's tree is unchanged; the checkout is kept and named in the record. (3) The plain target works; the isolated one is refused with `subagent/isolation-unavailable` and no child exists. Nothing was merged in any case. A write by an isolated child outside its checkout is denied by the sandbox.
- **Cleanup.** Remove the run branches and checkouts with `git worktree remove` and `git branch -D`; delete the home. If a checkout cannot be removed, keep the seam's journal and the output of `git worktree list`.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **External agents and the sandbox.** The mode cap that delegation sets on a child is on `policy.Resolver` ([sandbox-ssh.md §6.1](sandbox-ssh.md#61-shape)). Still open: whether an external agent can also be confined by the harness sandbox (it needs the network and its own state directory), which would make `confined` selectable; it can only be learned by running each product. Until then an external run reports how it was confined and nothing claims more.
2. **Recommended defaults, not confirmed by the owner; unconfirmed facts.** 8 slots, 200 starts, 32,768 bytes and one hour are judgements. The OAR command line and the supported OAR release range are as [botiverse-oar.md §7](../libs/botiverse-oar.md) says (area X5 owns the provider).
3. **Limits kept** (carried units of [§5](#5-tech-debt-and-its-resolution)): one process and one execution per external run, with a follow-up only for the `dsh` runtime child; local machine only; no rollback of what a child did outside its checkout; a shared checkout with advisory write scopes unless the target is isolated; no message across trees; Claude Code cannot ask a person.
4. **Not designed.** Write scopes enforced as sandbox write roots inside a shared checkout; isolation for a repository that is not Git; a merge of a child's branch.
5. **How other areas use this contract** (settled, for the record). The child header fields `Origin`, `Creator`, `DelegationDepth` and `Preset` are A01's; the creating tool call is a field of `subagent/started`. The resume hook is `session.Created` with `How: resumed`. A child's file changes are logged in the child's session and `RootOf` gives the tree root; they cannot be attributed to one turn of the root, because a child outlives the turn that started it. A child starts in its creator's directory. `WaitQuiet` is "this agent's tree has settled", for headless carriers and for goals (A13). A workflow member (A11) is an ordinary `Start` whose creating call is the workflow's hosting call, so it is in the root's ledger and its usage is in `subagent/settled`. Delegation no longer seeds approval policy `never`: a child asks the root's answerer unless `childApprovals: deny` is set.

## 11. Units dispositioned by this spec

Rows are in `trace/fragments/A12.jsonl`.

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 11 | 10 ported, 1 replaced |
| `UP-DEBT` | 63 (62 assigned, 1 found) | 36 resolved, 26 carried, 1 not applicable (as the debt review re-issued them; carried rows name stage [DH-S-47](../impl.md#dh-s-47)) |
| `UP-TOOL` | 10 | 10 ported |
| `UP-EVT` | 4 | 2 ported, 2 merged |
| `UP-CFG` | 11 | 11 ported |
| `UP-SVC` | 3 | 3 ported |
| `UP-DEC` (handed over) | 25 | 21 ported, 3 replaced, 1 merged |
