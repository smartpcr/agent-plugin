# Tools, the guarded pipeline, prompt assembly, guards and todo: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md) and are cited as `path:line` under the upstream root. Statements that are
> reasoning and not read from source open with "Inference"; sections 6 to 9 are design and are
> inference throughout. IDs in this document use the area key `TOOLS`. Nothing here was built, run or tested.

| | |
|---|---|
| **Area key** | `TOOLS` (agent A03) |
| **Upstream packages** | 7: `core/tools`, `core/system-prompt`, `core/tool-executor-native`, `core/tool-executor-ptc`, `guard/repeat-tool-reminder`, `guard/timeout-policy`, `todo/tool-todo` |
| **Serves outcomes** | [DH-O-6](../design.md#dh-o-6) (owner), [DH-O-3](../design.md#dh-o-3), [DH-O-4](../design.md#dh-o-4), [DH-O-7](../design.md#dh-o-7), [DH-O-9](../design.md#dh-o-9), [DH-O-14](../design.md#dh-o-14); in part [DH-O-1](../design.md#dh-o-1), [DH-O-2](../design.md#dh-o-2), [DH-O-12](../design.md#dh-o-12) |
| **Depends on areas** | A01, A05, A06 (session log, flush, projections); A02 (driver); A04 (provider adapters); A09 (spawn helper, shell tool); A10 (sandbox admits the endpoint); A11 (script-calling mode, workflows); A14 (compaction event, runtime context); A17 (approvals, session grants); A20 (the codec `src/sdk/jsonrpc`); A23 (`schema`, glob helper); A24 (`dsh tool` command) |
| **Priority** | P0 for the registry, pipeline, deadlines and prompt assembly: no agent can call a tool without them. P1 for the tool endpoint, the reminder and todo. P2 for deferred disclosure |

## 1. Purpose

A model can only read and write text. A **tool** is how it does anything else: read a file, run a command, start a subagent. This area owns the list of tools an agent may use, what the model is told about them, and the one path every tool call takes from "the model asked" to "the result is in the log". That path is where argument checking, policy, approval, time limits and size limits live, so it is the product's main safety surface: without it a tool author would have to re-implement each check, and one forgotten check would be a bypass.

The area also owns three smaller things that sit on that path: the assembly of the system prompt and of the tool list for each model request, two loop guards (a reminder when the model repeats itself, and the deadline on a tool call), and the `todo_write` tool. Since the owner's decision [DH-ADR-8](../design.md#dh-adr-8) it also owns a new entry into the path: a script that the model ran in a shell can call tools through the `dsh tool` command line.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **Declaration** | The one Go value that describes a tool: name, description, input and output types, body, and scheduling, deadline, reach and disclosure facts. | The model-facing schema, the argument check and the catalogue are all derived from it, so they cannot disagree. |
| **Step view** | The immutable set of tools one agent may use in one step, resolved once per step. | The prompt, the pipeline and nested calls must all see the same set. |
| **Call, outcome** | A call is one request to run one tool: id, name, raw JSON arguments, origin, attribution. An outcome is its single result: success with a value, or failure with a code. | One identity and one result per call; history stays paired. |
| **Origin** | Where a call entered: `model` (a call block in an assistant message), `script` (the tool command line), `direct` (host code such as a hook). | Logged for audit. It never selects a different path. |
| **Stage** | One fixed step of the pipeline. Each stage has a closed list of things it may change. | A reviewer can say what any extension can and cannot do. |
| **Policy listener** | A component that answers "allow, deny, ask a person, or cancel" for a call. | Reorderable policy: permission modes, hooks, plan mode. |
| **Guard** | A check that can only deny. | A denial that no listener order can undo. |
| **Wrapper** | Code that runs around a tool body, for example to measure or retry. | A place for cross-cutting behaviour that needs one lexical scope. |
| **Reach, disclosure** | Reach says how a tool may be called: `direct`, `script` or `both`. Disclosure says how much the model is told before it searches: `full`, `summary`, `name` or `none`. | Hiding a tool is not enforcement; these two facts are separate from permission. |
| **Outer call, nested call** | The outer call is the model's call that started a script. A nested call is one tool call the script makes through `dsh tool call`. | Nested calls are logged inside the outer call's bracket. |
| **Tool endpoint, call token** | The endpoint is the local socket on which the host accepts nested calls. The token is a random value minted for one outer call; it tells the host which session, agent, turn and outer call a nested call belongs to. | A token attributes; it carries no permission. |
| **Detached call** | A tool body that did not return within the grace period after its deadline or cancellation. | Go cannot stop a goroutine; the design reports this state instead of hanging the turn. |
| **Section, variable, runtime context** | A section is a named, ordered piece of system-prompt text. A variable is a `{{name}}` value. A runtime context is a changing fact delivered as a user-role snapshot. | Every prompt fact has one owner; changing facts stay out of the cached prefix. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.core.tools` | `@deepseek-ai/dsh-tools` | definition | Scoped tool registry, restrictions, step-time activation, the shared invocation pipeline, the batch scheduler, an in-house schema language and a JSON Schema subset validator, presentation types. Provides `ctx.tools`. |
| `UP-PKG-packages.core.system-prompt` | `@deepseek-ai/dsh-system-prompt` | definition | Registry of prompt sections, variables, runtime contexts and tool-schema providers; `assemble()` and `renderPrompt()`. Provides `ctx.systemPrompt`. |
| `UP-PKG-packages.core.tool-executor-native` | `@deepseek-ai/dsh-tool-executor-native` | provider | 35 lines: selects "native" execution for a scope, that is, one schema per tool and the shared batch scheduler. |
| `UP-PKG-packages.core.tool-executor-ptc` | `@deepseek-ai/dsh-tool-executor-ptc` | provider | Programmatic tool calling: exposes one `run_code` tool and a generated TypeScript SDK; runs the model's program in a guest runtime and routes each binding call through the pipeline. |
| `UP-PKG-packages.guard.repeat-tool-reminder` | `@deepseek-ai/dsh-repeat-tool-reminder` | consumer | Adds an advisory message when an agent makes the same call several times in a row. |
| `UP-PKG-packages.guard.timeout-policy` | `@deepseek-ai/dsh-tool-call-timeout-policy` | consumer | Wraps tool dispatch with a deadline for tools that declare `timeoutMs`. |
| `UP-PKG-packages.todo.tool-todo` | `@deepseek-ai/dsh-tool-todo` | consumer | The `todo_write` tool, the `todo/write` session event and the `todos` projection. |

### 3.2 Behaviour

**Registry.** A tool is registered globally or in one agent's scope; a scoped tool shadows a global one of the same name, and a duplicate in one layer fails (`packages/core/tools/src/index.ts:128-153`). A restriction is an allow or deny list over the tools a scope inherits; restrictions intersect and never hide the scope's own tools (`index.ts:162-189`, `:199-227`). A registration may carry a `when` condition; the loop calls `resolve()` once per step and the returned set decides that step's declarations and every call in its batch (`index.ts:264-297`, `docs/subsystems/tools.md:182`). Registration and restriction changes emit `tools/change` (`index.ts:106`).

**Declaring a tool.** `defineTool` takes parameters and an output schema written in an in-house schema language, plus a TypeScript type inferred from it. Argument validation happens inside the wrapper `defineTool` puts around the body (`docs/subsystems/tools.md:106`, `:159`), so a raw definition validates its own input and validation runs after policy and approval. Schemas are limited to an enforced subset; an unsupported keyword is rejected, not ignored (`docs/subsystems/tools.md:576-578`).

**Executors.** `ctx.tools.execute()` hands the model's whole batch to the executor selected for the agent's scope (`index.ts:357-377`). A custom executor implements only `describe` and `execute`; "the shared pipeline is optional" (`packages/core/tools/README.md:84`). The native executor uses the shared scheduler with a limit of 10 (`packages/core/tool-executor-native/src/index.ts:8-22`).

**The batch scheduler.** Raw argument text is parsed as JSON; text that does not parse is passed on as a string (`packages/core/tools/src/batch.ts:98-106`). A call that classifies as parallel-safe may overlap with neighbours in a rolling pool; any other call is an exclusive barrier. Only bodies overlap: policy, results and result context stay in the model's order (`batch.ts:2-6`, `:186-201`). Each started call appends `tool/call` before policy and `tool/result` when committed (`batch.ts:243-266`). On abort, started calls drain and every skipped call gets a synthetic `ABORTED_BEFORE_DISPATCH` result (`batch.ts:225-229`, `:271-280`).

**The invocation pipeline**, per call (`packages/core/tools/src/pipeline.ts`, `docs/tool-execution-pipeline.md:6`):

1. Snapshot and freeze the arguments and assign an opaque token (`pipeline.ts:117-183`).
2. `tools/pre-execute` waterfall: `allow`, `deny`, `cancel` or `ask` (`pipeline.ts:199-203`; `docs/subsystems/tools.md:553-558`). It cannot rewrite arguments, because history, audit, UI and execution must agree (`docs/subsystems/tools.md:572`).
3. `ask` goes to the approval service; only `allowed-once` proceeds, and a missing service, a missing agent or no answerer denies (`pipeline.ts:453-494`).
4. Guards run on an allowed call and can only deny (`pipeline.ts:214`; `index.ts:393-401`). Upstream therefore asks a person before the guards run.
5. `tools/execute` waterfall around the body; a wrapper may replace the cancellation signal, and the registry fuses the caller's signal back in (`pipeline.ts:263-336`).
6. The body's value is validated against the output schema and rendered to model-facing content (`pipeline.ts:558-588`).
7. `tools/post-execute` waterfall: accept, replace content or value, add context, or block (`pipeline.ts:507-546`).
8. The tool's `finalizeContent`, then the `tools/result` notification, whose listener failures are contained (`pipeline.ts:394-439`).

**Programmatic tool calling.** With the PTC executor selected the model sees one tool, `run_code`, and a generated TypeScript declaration of the others; a model-direct call to any other name is `UNKNOWN_TOOL` (`packages/core/tool-executor-ptc/README.md:67`). Each binding call passes the same pipeline with the outer token as parent and is logged as a log-only pair, `tool/ptc-dispatch-start` and `tool/ptc-dispatch` (`docs/tool-execution-pipeline.md:64`). A program has an allowance of 1024 calls per minute of its timeout and 10 overlapping calls; exceeding the allowance ends the whole program (`README.md:37-39`). The executor is off in the base bundle and selected by one preset ([../libs/ptc-guest-runtime.md](../libs/ptc-guest-runtime.md) §1).

**Deadlines.** `timeoutMs` on a definition is "declarative only" (`packages/core/tools/README.md:199`). The timeout-policy plugin wraps `tools/execute`, derives a deadline signal for tools that declare one, and replaces the result with `TOOL_TIMEOUT` only when its own timer fired (`packages/guard/timeout-policy/src/index.ts:56-80`). It never races the body.

**Repeat reminder.** On `tools/post-execute` it counts consecutive identical model-direct calls per agent, keyed by tool name and key-sorted arguments, and at counts 3, 5 and 8 adds a reminder as context for the next request (`packages/guard/repeat-tool-reminder/src/index.ts:52-57`, `:196-223`). A user message resets the chain (`:228-231`). State is an in-memory map (`:180`).

**Prompt assembly.** Sections have a name and an order and are joined in order; variables are `{{name}}` references resolved strictly, so an unknown reference fails assembly (`packages/core/system-prompt/README.md:56`, `:112`). Config supplies the fixed identity line, persona prefix and suffix and `toolOrder` (`README.md:44-52`). Tool-schema providers contribute the visible schemas; the tools service registers itself as one (`packages/core/tools/src/index.ts:113-117`). A `system-prompt/assemble` waterfall may replace sections, contexts and variables but not tools (`README.md:112`). The loop assembles once per proposed step and commits the rendered text as a `system/message` event and the tool schemas in `request/header` (`README.md:70`, `:142`). A schema may carry `deferLoading: true`, which asks the provider to keep the definition out of context until a later `tool-addition` records it (`packages/llm/llm/src/types.ts:443-451`, `:517-527`).

**Todo.** `todo_write` replaces the whole list; each call appends `todo/write`, and the `todos` projection is the latest list, cleared at the next `turn/start` (`packages/todo/tool-todo/src/index.ts:124-135`, `:193-209`).

### 3.3 Failure, retry, rollback and cleanup upstream

A tool that throws, an unknown tool and a denial all become an error result; the turn continues (`docs/subsystems/tools.md:574`). A listener that throws in pre-execute, around or post-execute also becomes an error result (`pipeline.ts:231-233`, `:333-335`, `:381-383`). Cancellation is cooperative: before the body runs the result is `ABORTED_BEFORE_DISPATCH`, after it `ABORTED`, and a started body is always awaited, so a body that ignores its signal holds the call open without bound (`.agents/notes/implemented/architecture/2026-07-19-cooperative-tool-cancellation.md:43`). Nothing is retried or rolled back; a tool's own side effects stay. Every registration is an effect and disappears with its plugin or agent. An executor that unloads leaves its scope with no executor: new batches reject, and nothing falls back to another executor (`index.ts:345-348`).

## 4. Upstream decisions dissected

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `UP-DEC-implemented.architecture.2026-07-02-tool-render-intent-union` | A tool describes its card through a closed tagged union, so an invalid combination cannot be written | keep | The Go host renders the HTML itself, so one switch over a closed set is exactly what a template needs | [DH-ADR-TOOLS-1](#dh-adr-tools-1): `View` is a closed interface on the declaration; templates belong to the UI areas |
| `UP-DEC-implemented.architecture.2026-07-05-prompt-variables-and-tool-guidance-ownership` | Every prompt fact has one owner; strict interpolation; persona is a section | keep | It prevents drift between what the harness knows and what the prompt says | [DH-ADR-TOOLS-8](#dh-adr-tools-8) |
| `UP-DEC-implemented.architecture.2026-07-07-tool-call-timeout-policy` | Deadlines are an optional plugin over an around hook; no deadline unless a tool declares one | change | An optional safety check is a bypass waiting for a composition mistake, and the note itself lists "declares a budget but ignores the signal" as a risk | [DH-ADR-TOOLS-5](#dh-adr-tools-5): the pipeline enforces a deadline on every call |
| `UP-DEC-implemented.architecture.2026-07-19-cooperative-tool-cancellation` | Every call carries the caller's signal; codes record whether the body started; a started body is always awaited | keep the first two, change the third | `context.Context` gives the first for free ([DH-ADR-ARCH-3](../upstream/architecture.md#dh-adr-arch-3)). Waiting without bound lets one faulty body hold a turn that cannot even be cancelled | [DH-ADR-TOOLS-4](#dh-adr-tools-4), [DH-ADR-TOOLS-5](#dh-adr-tools-5) |
| `UP-DEC-implemented.architecture.2026-10-09-ptc-owned-policy-cleanup` | The PTC executor cancels retained programs when the permission mode changes | replace | There is no retained program process. Each nested call resolves policy when it arrives, so it never runs under stale authority | [DH-ADR-TOOLS-6](#dh-adr-tools-6); whether a running command is stopped on a mode change is A09's |
| `UP-DEC-implemented.architecture.2026-11-09-explicit-ptc-output` | A program chooses what the model sees with `inspect()` | replace | A script's standard output is the selection; images are selected with `--attach` | [DH-ADR-TOOLS-7](#dh-adr-tools-7) |
| `UP-DEC-implemented.architecture.2026-11-13-ptc-output-truncation` | Too much output is truncated; the program is not cancelled, because cancelling hides which actions ran | keep the rule | The same reasoning applies to a script: a bound refuses one nested call or shortens one result and never kills the script | [DH-ADR-TOOLS-4](#dh-adr-tools-4), [DH-ADR-TOOLS-7](#dh-adr-tools-7) |
| `UP-DEC-implemented.feature.2026-06-15-ptc` | PTC is a presentation mode of the registry with a guest runtime behind a seam | replace | Overruled by [DH-ADR-8](../design.md#dh-adr-8). What survives: nested calls pass the full pipeline and are logged before the outer result | [DH-ADR-TOOLS-6](#dh-adr-tools-6) |
| `UP-DEC-implemented.feature.2026-07-20-ptc-typed-tool-returns` | A binding returns the tool's canonical JSON value; failure is a real error | keep | A Go tool returns a typed value naturally; `dsh tool call` prints it as JSON and reports failure by exit code | [DH-ADR-TOOLS-1](#dh-adr-tools-1), [DH-ADR-TOOLS-6](#dh-adr-tools-6) |
| `UP-DEC-proposed.architecture.2026-11-18-tool-reachability-and-initial-disclosure` | Proposal: two per-tool facts, reachability and initial disclosure, replace the one `deferLoading` flag | adopt | It also removes upstream's limit that presentation is per agent, and gives "not callable from scripts" a home | [DH-ADR-TOOLS-1](#dh-adr-tools-1), [DH-D-TOOLS-6](#dh-d-tools-6) |
| `UP-DEC-proposed.feature.2026-06-30-pre-tool-input-rewrite` | Proposal: let a hook rewrite arguments as a transaction before the call's identity exists | drop the feature, keep the constraint | Its own open question (does a provider accept a rewritten assistant message on replay) is unanswered, and [DH-O-6](../design.md#dh-o-6) requires logged arguments to stay as written | [DH-ADR-TOOLS-3](#dh-adr-tools-3) |
| `UP-DEC-implemented.simplification.2026-11-12-typescript-only-ptc` | Programs are TypeScript only | drop | The harness runs no program language; the script's language is whatever the machine has | [DH-ADR-8](../design.md#dh-adr-8) |
| `UP-DEC-proposed.simplification.2026-09-19-retire-prompt-registry-change-event` | Proposal: stop emitting `system-prompt/change`; nothing listens | adopt | The prompt is rebuilt each step; an advertised event is a promise with tests to keep | [DH-ADR-TOOLS-8](#dh-adr-tools-8) |
| Code: the executor is a swappable seam and may skip the shared pipeline (`packages/core/tools/README.md:84`) | So that native and PTC presentation can coexist | drop | With PTC gone the seam has one provider, and "optional pipeline" contradicts [DH-O-6](../design.md#dh-o-6) | [DH-ADR-TOOLS-2](#dh-adr-tools-2) |
| Code: arguments are validated inside the body wrapper, after policy and approval (`docs/subsystems/tools.md:106`) | A raw definition owns its input | change | A person should not be asked to approve a call that cannot run, and policy should not have to handle malformed input | [DH-ADR-TOOLS-3](#dh-adr-tools-3): validation is the first stage |
| Code: approval runs before guards (`pipeline.ts:204-214`) | Guards are the last word | change the order | A guard's denial does not depend on the answer, so asking first wastes a prompt; the guard is still final | [DH-ADR-TOOLS-3](#dh-adr-tools-3) |
| Code: unparseable argument text is passed to the tool as a string (`batch.ts:98-106`) | Tool-owned diagnostics | change | The pipeline reports the parse error with its position; no tool sees a non-object | [DH-ADR-TOOLS-3](#dh-adr-tools-3) |
| Code: parallel only when the tool's classifier returns exactly `true`; everything else exclusive (`pipeline.ts:106-115`) | Fail closed | keep | Same rule, with a config list that can force a tool exclusive | [DH-ADR-TOOLS-2](#dh-adr-tools-2) |
| Code: the whole assembly can be replaced by a waterfall listener (`packages/core/system-prompt/README.md:112`) | A general escape valve | change | A listener with the power to rewrite everything makes "who supplied this text" unanswerable | [DH-ADR-TOOLS-8](#dh-adr-tools-8): three closed operations |
| Code: reminder state is an in-memory map (`repeat-tool-reminder/src/index.ts:180`) | A heuristic nudge needs no log | change | The reminder is model-visible; deriving it from the log makes it identical after resume and replay | [DH-ADR-TOOLS-9](#dh-adr-tools-9) |
| Code: `todo_write` replaces the whole list; minimal item shape (`tool-todo/src/index.ts:136-158`) | Last write wins on replay; nothing to merge | keep | It matches the rule that a state-carrying event holds the whole value ([../design.md §4.6](../design.md#46-event-model)) | [DH-ADR-TOOLS-10](#dh-adr-tools-10) |

## 5. Tech debt and its resolution

Six rows are **carried**: limits the Go design keeps, each by a recorded decision and with the reason stated. Their ledger rows name stage [DH-S-47](../implementation-plan.md#dh-s-47), which records the known limits and scope boundaries of the core areas. They are listed again in [§10](#10-open-questions-and-limitations).

| Debt ID | What it is | Evidence | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-limit.packages.core.system-prompt.1` | No end-user prompt-editing API; persona text is config only | `packages/core/system-prompt/README.md:179` | Resolved. Persona text is a field of the prompt component's config struct, so the generic settings form and the writable config layer ([DH-O-2](../design.md#dh-o-2)) edit it at run time; the field is live and the next step uses it ([DH-D-TOOLS-5](#dh-d-tools-5)). No separate API is needed |
| `UP-DEBT-limit.packages.core.system-prompt.2` | No inline escape in interpolated text | `README.md:180` | Resolved. `{{{{` renders a literal `{{`; a section may still be marked literal as a whole ([DH-ADR-TOOLS-8](#dh-adr-tools-8)) |
| `UP-DEBT-limit.packages.core.system-prompt.3` | A bad `toolOrder` name fails at the first turn, not at boot | `README.md:181`; `src/index.ts:240-243` | Resolved. Every compiled-in tool name is known to the binary, so config load rejects a name that is neither compiled in nor matches a declared dynamic prefix, with file and line. A known name that is not visible is skipped ([DH-D-TOOLS-5](#dh-d-tools-5)) |
| `UP-DEBT-limit.packages.core.tool-executor-native.1` | The executor cannot stop a same-process tool that ignores cancellation | `packages/core/tool-executor-native/README.md:84` | Carried: Go cannot stop a goroutine. The harm is bounded. Third-party tool code no longer runs in the host ([DH-ADR-4](../design.md#dh-adr-4)), so only first-party bodies are in-process and each must pass a cancellation conformance test. The pipeline waits a bounded grace period and then settles the call as "outcome unknown" and tracks the body as detached ([DH-ADR-TOOLS-5](#dh-adr-tools-5), [DH-D-TOOLS-3](#dh-d-tools-3)) |
| `UP-DEBT-limit.packages.core.tool-executor-ptc.1` | Program images need a value reference; only some tools yield `Image`s | `packages/core/tool-executor-ptc/README.md:161` | Resolved. `--attach` on a nested call adds that result's image blocks, by attachment reference, to the outer result; it works for any image-producing tool ([DH-ADR-TOOLS-7](#dh-adr-tools-7)) |
| `UP-DEBT-limit.packages.core.tool-executor-ptc.2` | Program images need attachment paths mapped into the program's filesystem; `fs-ssh` has none | `README.md:162` | Resolved. The script never reads the image file: the host attaches the stored image itself, so no path mapping exists ([DH-ADR-TOOLS-7](#dh-adr-tools-7)) |
| `UP-DEBT-limit.packages.core.tool-executor-ptc.3` | No built-in control to reset retained program state | `README.md:163` | Not applicable. It exists only because a JavaScript process retains variables between programs; the Go design has no interpreter and keeps no program state ([DH-ADR-8](../design.md#dh-adr-8)) |
| `UP-DEBT-limit.packages.core.tool-executor-ptc.4` | TypeScript only, and presentation is per agent, not per tool | `README.md:164` | Resolved. No language is imposed, and reach is declared per tool, so one agent can have direct-only and script-only tools ([DH-ADR-TOOLS-1](#dh-adr-tools-1)) |
| `UP-DEBT-limit.packages.core.tool-executor-ptc.5` | Intermediate values are unbounded and cannot be rebuilt from the log | `README.md:165` | Resolved. Each nested result is bounded before it is returned and its shaped content is logged as `tool/subresult` ([DH-ADR-TOOLS-7](#dh-adr-tools-7)) |
| `UP-DEBT-limit.packages.core.tool-executor-ptc.6` | Program state depends on the runtime mode | `README.md:166` | Not applicable. It describes Node `vm` evaluation modes; a script keeps state only in files it writes |
| `UP-DEBT-limit.packages.core.tools.1` | Concurrency policy is not an event gate; only the owning definition can classify | `packages/core/tools/README.md:196` | Resolved. The classifier stays on the declaration, and operator config can force named tools exclusive. Nothing can force a tool parallel, the same one-way rule as guards ([DH-ADR-TOOLS-2](#dh-adr-tools-2)) |
| `UP-DEBT-limit.packages.core.tools.2` | Pre-execute cannot rewrite arguments | `README.md:197` | Carried on purpose: logged arguments are never rewritten ([DH-ADR-TOOLS-3](#dh-adr-tools-3)), which [DH-O-6](../design.md#dh-o-6) requires. The Go types give a listener no way to try |
| `UP-DEBT-limit.packages.core.tools.3` | Subagent and workflow structured outputs must be object-rooted | `README.md:198` | Resolved in this area's contract: a declaration's output type may be any JSON root and `schema` has no root restriction ([DH-D-TOOLS-1](#dh-d-tools-1)). Whether A11 and A12 keep a consumer-side guard is theirs to state |
| `UP-DEBT-limit.packages.core.tools.4` | `timeoutMs` is declarative only | `README.md:199` | Resolved. The pipeline enforces it ([DH-D-TOOLS-3](#dh-d-tools-3)) |
| `UP-DEBT-limit.packages.guard.repeat-tool-reminder.1` | Exact-match detection only | `packages/guard/repeat-tool-reminder/README.md:168` | Resolved. A declaration may supply `RepeatKey(args)`, so the tool that knows what "the same call" means says so; the default stays exact canonical JSON ([DH-D-TOOLS-7](#dh-d-tools-7)) |
| `UP-DEBT-limit.packages.guard.repeat-tool-reminder.2` | Compaction does not reset chains | `README.md:169` | Resolved. The chain is a fold over the log and resets on the compaction event, because the model no longer sees the earlier calls ([DH-ADR-TOOLS-9](#dh-adr-tools-9)) |
| `UP-DEBT-limit.packages.guard.repeat-tool-reminder.3` | Advisory only; no blocking form | `README.md:170` | Resolved. Optional `blockAt` registers a guard that denies the call at that count; it is off by default ([DH-D-TOOLS-7](#dh-d-tools-7)) |
| `UP-DEBT-limit.packages.guard.repeat-tool-reminder.4` | Chains are not shared between a parent and its subagent | `README.md:171` | Carried on purpose: a chain describes one model's own context, and a subagent has a different one ([DH-ADR-TOOLS-9](#dh-adr-tools-9)) |
| `UP-DEBT-limit.packages.guard.repeat-tool-reminder.5` | Legitimate polling still draws reminders | `README.md:172` | Resolved. A polling tool declares itself exempt, so the exemption ships with the tool and not with each deployment's config ([DH-D-TOOLS-7](#dh-d-tools-7)) |
| `UP-DEBT-limit.packages.guard.repeat-tool-reminder.6` | Silent past the highest threshold | `README.md:173` | Resolved. `repeatEvery` reminds again every N calls after the last threshold ([DH-D-TOOLS-7](#dh-d-tools-7)) |
| `UP-DEBT-limit.packages.guard.timeout-policy.1` | Cooperative, never a hard kill; no timeout result until the body settles | `packages/guard/timeout-policy/README.md:121` | Resolved for what the user and model see: the timeout result arrives after a bounded grace period whether or not the body returned. A body that owns a process is stopped by the process stop sequence ([DH-ADR-TOOLS-5](#dh-adr-tools-5)) |
| `UP-DEBT-limit.packages.guard.timeout-policy.2` | No blanket budget | `README.md:122` | Resolved. Every call has a budget: the tool's, an operator override, or the default. Opting out is explicit and listed by a golden test ([DH-ADR-TOOLS-5](#dh-adr-tools-5)) |
| `UP-DEBT-limit.packages.todo.tool-todo.1` | The list belongs to one agent session only | `packages/todo/tool-todo/README.md:155` | Carried on purpose ([DH-ADR-TOOLS-10](#dh-adr-tools-10)): one owner keeps the event a plain snapshot. A nested call from a script is attributed to its agent and so writes that agent's list |
| `UP-DEBT-limit.packages.todo.tool-todo.2` | Minimal item shape | `README.md:156` | Carried on purpose ([DH-ADR-TOOLS-10](#dh-adr-tools-10)): whole-list replacement needs no item identity |
| `UP-DEBT-limit.packages.todo.tool-todo.3` | Whole-list replacement is the only operation | `README.md:157` | Carried on purpose ([DH-ADR-TOOLS-10](#dh-adr-tools-10)): a whole-list event needs no merge and replays as last write wins |
| `UP-DEBT-marker.packages/core/tool-executor-ptc/src/ptc.ts:465` | FIXME: let image tools declare their program result adaptation | the marker | Not applicable. It concerns adapting results for a JavaScript program; no such adaptation exists without the guest runtime |
| `UP-DEBT-marker.packages/core/tool-executor-ptc/src/ts-types.ts:315` | FIXME: derive program return types from those adaptations | the marker | Not applicable. It concerns the generated TypeScript SDK, which is not built |
| `UP-DEBT-marker.packages/guard/timeout-policy/src/index.ts:6` | FIXME: settle an npm package rename | the marker | Not applicable. It is an npm package-naming question; in Go the deadline is a pipeline stage with no package of its own |

## 6. Go design

### 6.1 Shape

All packages sit under `src/harness/internal/tools` ([../design.md §4.2](../design.md#42-go-module-layout)).

| Package | Component (config row) | Provides | Injects | Why it exists |
|---|---|---|---|---|
| `tools` | none (contract) | Types: `Spec`, `Declaration`, `Call`, `Outcome`, `Run`, `View`, decisions, error codes, event keys, `tools.Key` | n/a | The only package other areas import to declare or call a tool |
| `tools/core` | `tools.registry` | `tools.Service` under `tools.Key`: registry, step view, pipeline, batch scheduler, deadlines | session store, `schema`, interactions (optional) | Registry and pipeline are one component so that no composition can have one without the other |
| `tools/endpoint` | `tools.endpoint` | Nothing to other components; it accepts nested calls and feeds them to `tools.Service` | `tools.Key` | Separate so that a composition without script calling has no listener at all |
| `tools/prompt` | `tools.prompt` | `prompt.Service` under `prompt.Key`: sections, variables, runtime contexts, `Assemble`, `Render` | `tools.Key` | What the model is told is one coherent thing |
| `tools/guard/repeat` | `tools.repeat-guard` | Nothing; registers a projection, a shaping listener and an optional guard | `tools.Key`, projections | Policy attaches; it does not edit the pipeline |
| `tools/todo` | `tool.todo` | Nothing; registers `todo_write`, the `todo/write` event type and the `todos` projection | `tools.Key`, projections | Tools and the service that hosts them are separate components |
| `tools/discover` | `tool.search` | Nothing; registers `tool_search` | `tools.Key` | Only compositions that defer disclosure mount it |

A declaration is one generic value. The sketch is illustrative, not final.

```go
var Read = tools.Define(tools.Spec[ReadIn, ReadOut]{
    Name: "read", Description: "Read a file.",
    Run:      func(ctx context.Context, in ReadIn, run tools.Run) (ReadOut, error) { ... },
    Render:   func(in ReadIn, out ReadOut) []llm.Block { ... },   // model-facing content
    Overlap:  func(in ReadIn) tools.Overlap { return tools.Reads(in.Path) }, // nil means exclusive
    Timeout:  30 * time.Second,                                   // 0 means the default budget
    Reach:    tools.ReachBoth, Disclosure: tools.Full,
})
```

`Define` derives the input and output JSON Schema from `ReadIn` and `ReadOut` through `schema` ([DH-ADR-SCHEMA-1](../libs/zod.md#dh-adr-schema-1)) and fails registration when the input schema leaves the model-facing subset. The input root is an object; an output schema may have any JSON root. `Overlap` classifies one call from its validated arguments: `tools.Exclusive` (also the answer when the field is nil or the function fails), `tools.Free`, or read and write resource keys (`tools.Reads(k...)`, `tools.Writes(k...)`); this is the classification with resource keys that [DH-ADR-AGENT-11](agent-loop.md#dh-adr-agent-11) asks for. Optional fields: `When` (a step-time condition that receives the agent, the step's model route and the carrier's capabilities), `Deadline(in)` (a per-call budget, for a tool whose arguments carry a timeout), `Grace` (a longer wait after cancellation, at most 60 s), `HostsScripts` (`no`, `call` or `job`; [DH-ADR-TOOLS-6](#dh-adr-tools-6)), `RepeatKey` and `RepeatExempt`, `Finalize` (a last content-only transform), `BoundsOwnOutput` (the spill policy leaves the result alone), `PresentCall` and `PresentResult` (pure functions of the logged call and result that need no step view and return a `View` holding data, never markup), and `Effects` (a closed set a guard can read: `none` for a pure read, `files`, `process`, `network`, `session`, `config`). A tool whose schema arrives at run time (MCP, a plugin) uses `tools.Dynamic`, which takes a JSON Schema document through `schema.FromJSON`, a body receiving raw JSON and the same optional fields; its origin is recorded in the catalogue and under the reserved result key `meta.external`.

`tools.Service` has four groups of methods.

| Group | Methods | Notes |
|---|---|---|
| Registration (effects on the caller's fiber and scope) | `Register(decl)`, `RegisterSet(decls)`, `Restrict(filter)`, `Guard(fn)`, `SetSpill(fn)`, `AllowScriptCalls(tools...)` | A scoped registration shadows a global one. `RegisterSet` adds or replaces a set of tools atomically (an MCP server's or a plugin's). `Restrict` needs a scoped context. `SetSpill` is a seat for one shaping listener that runs after all others (A07's spill policy). `AllowScriptCalls` is how A11's mode component turns script calling on for one scope, for the hosting tools it names |
| Step view | `Resolve(ctx, agent, model) (StepView, error)`; `StepView.Tools()`, `.Lookup(name)`, `.Release()` | Called once per proposed step by the driver. A `When` that fails is `tools/activation-failed` and fails step preparation |
| Entries into the pipeline | `ExecuteBatch(ctx, Batch) (BatchResult, error)` for the model's calls; `Call(ctx, DirectCall) Outcome` for host code; the endpoint's nested entry (unexported, same function underneath) | All three build an `entry` value and call one unexported `run(entry)`; nothing else can reach a tool body |
| Read-only | `Catalogue()`, `Detached()`, `ScriptLimits()`, `EndpointDir()` | For the generated tool catalogue and diagnostics; the endpoint's bounds as values (A11); the one directory a sandbox backend must admit (A10) |

`prompt.Service`: `Section(s)`, `Variable(name, fn)`, `Context(c)`, `Suppress(name)` (all effects), `Assemble(ctx, in) (Assembly, error)` and `Render(Assembly) (string, error)`. `Assemble` is a pure function of the registries, the step view and its input (agent, scope, model selection).

### 6.2 Contracts

**The pipeline.** Eight stages in a fixed order. Stages 0 to 4 run one call at a time in the model's order, so approvals are asked in order; stage 5 may overlap; stages 6 and 7 commit in the model's order.

| # | Stage | What it does | What it may change | Ends the call with | Logged |
|---|---|---|---|---|---|
| 0 | Admit | Assigns identity (id, origin, parent, attributed agent, turn, step), copies the raw argument bytes, appends the call record | Nothing | `tools/aborted-before-start` if already cancelled | `tool/call` (origin `model`) or `tool/subcall` (origins `script`, `direct`), with the argument bytes exactly as received |
| 1 | Resolve and validate | Looks the name up in the step view of the attributed agent, checks reach against the origin, bounds the argument size, strictly decodes into the input type and runs its `Validate()` | Nothing | `tools/unknown-tool`, `tools/not-reachable`, `tools/arguments-too-large`, `tools/invalid-arguments` (every violation, each with its path) | Only the result |
| 2 | Policy listeners | Dispatches the around event `tools.Decide` through the agent's scope | Nothing: the call is read-only. Returns `Allow`, `Deny{reason, code}`, `Ask{reason, display, subject}` or `Cancel`. The optional `subject` (a kind, a name and a label, for example directory `/repo/build`) is what a session grant can match; an `Ask` without one is asked every time | `tools/denied` with the listener's code, or `tools/aborted-before-start` | The deciding component's id in the result |
| 3 | Guards | Runs every guard on the scope chain for a call that is `Allow` or `Ask` | Nothing. A guard returns a denial or nothing | `tools/denied` | The guard's id in the result |
| 4 | Approval | Only for `Ask`: one call to `approval.Service.Request` (A17) with the call id, its parent and origin, the reason, the display and the subject. That service, not the pipeline, checks the approval policy, matches the session's grants and asks the agent's bound answerer; the pipeline holds no grant state and writes no approval event | Nothing. `allowed-once` covers this one call; `allowed-by-grant` means a grant of this session covered the subject and nobody was asked | `approval/rejected`, `approval/cancelled`, `approval/unavailable` | A17's events only: the `approval/asked` and `approval/decided` pair, and `grant/created` when the answer created a grant. The tool result names the outcome |
| 5 | Execute | For a root call, calls the checkpoint policy ([DH-D-STORE-4](session-store.md#dh-d-store-4)), which makes the call record durable; a nested call rides on its outer call's checkpoint. Then starts the deadline, dispatches the around event `tools.Execute`, runs the body | A wrapper may narrow the context it passes on; it cannot change the call or widen the context. The body returns a typed value | `tools/timeout`, `tools/aborted`, `tools/outcome-unknown`, `tools/not-started` (flush failed), or the body's own `<domain>/<reason>` | Events the tool itself owns, such as `todo/write` |
| 6 | Shape and bound | Validates the value against the output schema, renders content, dispatches the around event `tools.Shape`, runs the tool's `Finalize`, applies the size ceiling, encodes the durable form once | Content, value and added context only. `Block` turns a success into a failure; nothing turns a failure into a success | `tools/invalid-output`, `tools/blocked` | `truncated` and any spill reference in the result |
| 7 | Settle | Appends the result, then notifies `tools.Settled` | Nothing; observer failures are contained | n/a | `tool/result` (a surface event) or `tool/subresult` (log-only) |

A failure inside a listener, guard, wrapper or the approval service ends the call with a failure outcome naming the component and still passes stages 6 and 7 (rule 1 of [../design.md §6](../design.md#6-security-model): a check that panics means "no"). Every call that reached stage 0 reaches stage 7 exactly once, a denied call included, so `tools.Shape` listeners see it too. **Session grants at stage 4.** The grant table and its events are A17's; this pipeline only passes the subject. The rule is the owner's ([DH-ADR-16](../design.md#dh-adr-16), decided 2026-11-19): a session grant is never copied. It lives and dies with the one live session it was given to; it does not survive a resume, does not pass to a fork and does not pass to a subagent. A subagent's request goes to whoever answers for the root session and is denied if nobody does.

**Events.** All are typed keys in package `tools`, dispatched through the attributed agent's scope ([DH-D-CORDIS-3](../libs/cordis.md#dh-d-cordis-3)).

| Go key | Form | Replaces | Payload and result |
|---|---|---|---|
| `tools.Decide` | around | `UP-EVT-tools/pre-execute` | `Call` (read-only accessors) to a `PreDecision` (closed: `Allow`, `Deny`, `Ask`, `Cancel`) |
| `tools.Execute` | around | `UP-EVT-tools/execute` | `(ctx, Call)` to `Outcome`; `next(ctx)` accepts only a context derived from the one received |
| `tools.Shape` | around | `UP-EVT-tools/post-execute`, `UP-EVT-tools/ptc-dispatch-log` | `(Call, Outcome)` to a `PostDecision` (closed: `Accept`, `ReplaceContent`, `ReplaceValue`, `Block`; each may add context). It runs for nested calls too, so the logged copy of a nested result is shaped by the same listeners and no second hook is needed |
| `tools.Settled` | notify | `UP-EVT-tools/result` | `(Call, Outcome)`, both immutable ([DH-D-CORDIS-4](../libs/cordis.md#dh-d-cordis-4)). Sent for every call, nested ones included; the outcome carries the typed value and the `seq` of the result event |
| `tools.Changed` | notify | `UP-EVT-tools/change` | `{Scope}`; sent after a registration or restriction change commits |
| none | n/a | `UP-EVT-system-prompt/assemble` | Replaced by scoped shadowing, `Suppress` and a `Complete` section ([DH-ADR-TOOLS-8](#dh-adr-tools-8)) |
| none | n/a | `UP-EVT-system-prompt/change` | Not built ([DH-ADR-TOOLS-8](#dh-adr-tools-8)) |

**Config.** Each is a Go struct with defaults as a Go value ([DH-ADR-SCHEMA-3](../libs/zod.md#dh-adr-schema-3)); decoding is strict. Numbers are proposals; where upstream has a default it is kept.

| Component | Replaces | Fields (default) |
|---|---|---|
| `tools.registry` | upstream's removed `mode` field and the loop's `maxParallelToolCalls` | `maxParallel` (10, at least 1, live: the one cap on overlapping calls of a batch; the driver has no such setting), `forceExclusive` (names, empty), `defaultTimeout` (5m), `timeouts` (per-tool overrides, empty), `grace` (5s), `maxArgumentBytes` (1 MiB), `maxResultBytes` (256 KiB of model-facing content) |
| `tools.endpoint` | `UP-CFG-dsh-tool-executor-ptc` | `nestedCallsPerMinute` (1024), `maxNestedCalls` (8192), `maxPending` (128), `maxParallelNested` (10), `maxNestedValueBytes` (4 MiB), `maxConnections` (256), `requestTimeout` (10s) |
| `tools.prompt` | `UP-CFG-dsh-system-prompt` | `includeHarnessIdentity` (true), `includeRuntimeContext` (true), `personaPrefix` (empty, live), `personaSuffix` (empty, live), `toolOrder` (unset; exactly one `<unlisted-tools>` entry, no duplicates, names checked at load) |
| `tools.repeat-guard` | `UP-CFG-dsh-repeat-tool-reminder` | `thresholds` ([3, 5, 8]; integers of at least 2, no duplicates), `repeatEvery` (5; 0 turns it off), `blockAt` (0, off), `include`, `exclude` (name globs), `argumentsPreviewChars` (500) |
| `tool.todo` | `UP-CFG-dsh-tool-todo` | `allowParallelInProgress` (required, no default, as upstream: `packages/todo/tool-todo/src/index.ts:41-43`) |

**Persisted data.** Event types are registered with the session store at start-up (A01).

| Event | Surface | Data |
|---|---|---|
| `tool/call` | no (audit; the call block is in the assistant message) | turn, step, call id, name, raw arguments, origin |
| `tool/result` | yes | turn, step, the tool-result message (content, error flag), `error{code, message, stage, by}`, `truncated`, `meta`. `meta` is a small JSON object with reserved keys, each optional and each set by the pipeline or the tool, never by a listener: `external` (the origin of a dynamic tool), `spill` (the spill reference), `process` (`exit`, `signal`, `cwd`, `enforcement`, for a tool that ran a process), `file` (`path`, and `changed` for a write), `web` (`url`, `status`), `job` (a job id), `approval` (the outcome and, for `allowed-by-grant`, the grant id). A view reads only the result and these keys and ignores any other |
| `tool/subcall`, `tool/subresult` | no | The same fields plus `parent` (the outer call id, absent for origin `direct`). Ids are `<outer>:sub:<n>` |
| `tool/late-return` | no | The call id of a detached body that finally returned, and whether it reported success |
| `tool/disclosed` | no | Names of tools whose full schema the model has now been given ([DH-D-TOOLS-6](#dh-d-tools-6)) |
| `todo/write` | no | The whole list |

Projections: `todos` (latest list, cleared at `turn/start`), `tools.repeat` (per session: hash of the last tracked call and its run length), `tools.disclosed` (set of names).

**Tools exposed to the model.** `todo_write` (`UP-TOOL-dsh-tool-todo.todo_write`): same name, arguments and result text as upstream. `tool_search` (new, P2). `run_code` (`UP-TOOL-dsh-tool-executor-ptc.run_code`) is not exposed: the model runs a script with the shell tool instead ([DH-ADR-8](../design.md#dh-adr-8)).

**Outcome codes owned by this area.** `tools/unknown-tool`, `tools/not-reachable`, `tools/arguments-too-large`, `tools/invalid-arguments`, `tools/invalid-output`, `tools/denied`, `tools/blocked`, `tools/timeout`, `tools/aborted-before-start` (the body never ran; safe to retry), `tools/aborted` (the body ran and returned after cancellation), `tools/outcome-unknown` (the body may still be running, or the host died with the call recorded), `tools/not-started` (a call block with no call record, or a failed flush), `tools/busy` (a detached body of this tool is still running), `tools/activation-failed`, and for the endpoint `tools/token-rejected`, `tools/limit` and `tools/endpoint-unavailable`. The model-facing text of every failure is `Error: <message>`, as upstream; the code travels in the structured field.

**Wire: the tool endpoint.** One connection carries one JSON-RPC 2.0 request and its one response, encoded by `src/sdk/jsonrpc` in its one-shot mode ([DH-D-SDK-1](sdk-acp-typert.md#dh-d-sdk-1)); the harness imports that package and no codec lives in `src/harness`. The method is the operation, and the fields below are its params and result; their Go types are private to `internal/tools/endpoint` and `cmd/dsh`, because no outside program speaks this wire. A reply's `outcome` is one of a closed list that A24 maps to exit codes: `ok`; `tool-failed` (the body ran and failed, timed out or its outcome is unknown); `refused` (the call ended before its body: unknown tool, not reachable, invalid arguments, denied, approval rejected or unavailable); `limit`; `cancelled`; `token-rejected`; `bad-request`; `unavailable`. A new endpoint error code must be given a status class in [app-cli.md §6.2](app-cli.md#62-contracts) before it ships.

| Field | Request | Reply |
|---|---|---|
| Always | `v` (wire revision), `token`, `op` (`call`, `list` or `describe`) | `outcome` (the closed kind that A24 maps to an exit code), `error{code, message, tool, limit, call}` on failure |
| `call` | `name`, `args` (a JSON object), `attach` (boolean) | `value`, `text` (the rendered content), `truncated`, `images` (attachment references), `call` (the nested call id) |
| `list`, `describe` | `name` for `describe` | Names and one-line descriptions, or one input schema, for the script-reachable tools of the attributed agent's step view |

`call` is the only operation that executes anything, and it does so by entering stage 0. `list` and `describe` return what the prompt already says, run no tool and are not logged as calls. There is no field for a session, agent, turn, preset or interaction, and unknown fields fail the request.

### 6.3 Ownership, failure, retry, rollback and cleanup

| Resource | Owner (who releases it) | Who merely has access |
|---|---|---|
| A registration, restriction, guard, section, variable | The fiber that registered it; gone when that component or agent unloads | Every agent whose scope chain includes it |
| A step view | The driver, for one step; released at step end | The pipeline and the token table, which hold a reference until the calls using it settle |
| A call and its deadline timer | The pipeline, from stage 0 to stage 7 | Listeners, guards and wrappers, read-only |
| A tool body's own resources (a process, a file handle) | The body, or the seam it called; released before it returns | n/a |
| The endpoint listener, its socket file and runtime directory | The `tools.endpoint` fiber; removed on unload. A stale directory from a crashed host is removed at start when its process id is dead | A script, through the environment of its command |
| A call token | The outer call, which deletes it on leaving stage 5; or, after the `workflow` tool handed it over, the registered job, which deletes it when it settles or is stopped ([DH-ADR-TOOLS-6](#dh-adr-tools-6) item 7) | Any process that was handed the script environment; it can do only what the model can propose |
| A detached body | The `tools.registry` fiber, which lists it until it returns | n/a |

| Situation | What happens | Undone, and what stays uncertain |
|---|---|---|
| The tool body returns an error or panics | A failure outcome with the body's code; the turn continues | **Uncertain:** the tool's own side effects. Nothing is retried by the pipeline |
| The turn is cancelled during stages 0 to 4 | `tools/aborted-before-start`; calls not yet admitted get a call record and the same result, so history stays paired | Nothing ran |
| The turn is cancelled during stage 5 | The body's context is cancelled with the cause. If it returns within `grace`, a successful value is replaced by `tools/aborted` and a specific failure is kept | **Uncertain:** how far the body got |
| The body does not return within `grace` after a deadline or cancellation | The call settles as `tools/outcome-unknown`; the body is detached and listed; further calls to that tool by that agent are `tools/busy` until it returns; its return is logged as `tool/late-return` | **Uncertain:** everything the body did or will still do. The model is told to verify before retrying |
| A flush before stage 5 fails | The body does not run: `tools/not-started` | Nothing ran; the next flush retries the buffered events |
| The host dies during a call | On resume the lease holder appends closers using this area's codes: `tools/outcome-unknown` for a recorded call with no result (nested calls included), `tools/not-started` for a call block with no record | **Uncertain:** whether a recorded call took effect. Nothing is replayed |
| A component that registered a tool unloads during a step | Calls not yet at stage 1 fail as `tools/unknown-tool`; a running body's context is cancelled because it derives from that fiber | As for cancellation |
| The outer call ends while nested calls run | The token is deleted first, running nested calls are cancelled and settled, and each `tool/subresult` is appended before the outer `tool/result` | **Uncertain:** the nested tools' side effects |
| A nested call arrives with a missing, unknown or deleted token | Refused before stage 0 with `tools/token-rejected`; a diagnostic log line, no session event | Nothing ran |
| The endpoint cannot be created at start | The `tools.endpoint` fiber fails; no token is ever minted and the script-calling prompt text is absent (A11) | Direct tool calls are unaffected |
| Prompt assembly fails (unknown variable, two complete sections) | The step is not proposed; the turn ends with the error | Nothing was sent to the model |

## 7. Decisions

<a id="dh-adr-tools-1"></a>
### DH-ADR-TOOLS-1 One declaration per tool, with reach and disclosure as per-tool facts

- **Decision.** A tool is one `tools.Spec` value. Its input and output Go types yield the JSON Schema sent to the model, the validator used at stage 1, the output check at stage 6 and the generated catalogue entry. The declaration also states `Reach` (`direct`, `script`, `both`; default `both`) and `Disclosure` (default `full`). The input root is closed by default. Presentation is a closed `View` interface (generic, terminal, diff, search, read, web).
- **Why.** Upstream writes every shape twice and joins five schema systems ([../libs/zod.md](../libs/zod.md) §3). Reach and disclosure replace both the per-agent native-or-PTC switch and the provider-specific `deferLoading` flag with facts that both entries and the prompt read from one place.
- **Tradeoff.** Descriptions live in struct tags, which the compiler does not check; a test parses them ([DH-T-ZOD-1](../libs/zod.md)). A tool whose schema depends on config edits the derived document before registering, and that edited document is the one validated against.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-TOOLS-1`, `DH-T-TOOLS-2`.

<a id="dh-adr-tools-2"></a>
### DH-ADR-TOOLS-2 One pipeline and one scheduler; the replaceable executor is dropped

- **Decision.** The pipeline and the batch scheduler are fixed parts of the `tools.registry` component. There is no executor registry. Every entry (the model's batch, a direct caller, a nested call) builds an `entry` and calls one unexported function. The scheduler lets a call overlap running calls only when its declaration's `Overlap(args)` is not exclusive, the tool is not in `forceExclusive`, the call does not host scripts, and none of its resource keys conflicts with a running call's (a write key against any equal key, a read key against an equal write key). A call that may not overlap waits as a barrier, so calls start in the model's order. At most `maxParallel` bodies run at once; that setting is the only cap on overlapping calls and this component is its only owner, read live for each batch.
- **Why.** Upstream's seam existed to choose between native and PTC presentation, and it let a custom executor skip the pipeline. With one presentation the seam has one provider, and an optional pipeline contradicts "no entry skips the pipeline" ([../design.md §6](../design.md#6-security-model), rule 6).
- **Tradeoff.** How calls are scheduled cannot be swapped by configuration; changing it is a change to this specification. This narrows the "tool executor" row of [../design.md §2](../design.md#2-vocabulary) and "executor selection" in §4.3, which the lead should reword.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-3](../design.md#dh-o-3). **Verified by.** `DH-T-TOOLS-3`, `DH-T-TOOLS-6`.

<a id="dh-adr-tools-3"></a>
### DH-ADR-TOOLS-3 Fixed stage order; validation first, guards before approval; logged arguments are never rewritten

- **Decision.** The stage order of [§6.2](#62-contracts) is fixed. Arguments are recorded at stage 0 exactly as received and are never changed afterwards: `Call` exposes them through read-only accessors and no decision type carries replacement arguments. Validation runs before any policy. Guards run before a person is asked. There is no rewrite stage.
- **Why.** History, audit, the UI and execution must show the same arguments. Validating first means policy and people only ever see a call that can run. A guard's denial does not depend on a person's answer.
- **Tradeoff.** A hook that wants to correct a call must deny it with a message and let the model retry, costing one step. If a rewrite is ever needed it must happen before the assistant message is committed, which makes it the driver's (A02) feature, not a pipeline stage.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-4](../design.md#dh-o-4), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-TOOLS-3`, `DH-T-TOOLS-4`, `DH-T-TOOLS-5`.

<a id="dh-adr-tools-4"></a>
### DH-ADR-TOOLS-4 Every call ends in exactly one bounded result

- **Decision.** Every call that reached stage 0 gets exactly one result, also when it is denied, cancelled, timed out or never started. Results are bounded by the pipeline: after the shaping listeners and the tool's `Finalize`, content above `maxResultBytes` is cut at a block boundary with a marker and `truncated` is set. The spill policy (A07) is a shaping listener that normally replaces large content with a preview and a locator before the ceiling applies. A bound shortens or refuses; it never cancels work that has started.
- **Why.** An unanswered call makes the next model request invalid ([DH-O-3](../design.md#dh-o-3)). A ceiling that depends on an optional listener is not a bound. Cancelling on overflow hides which actions ran (`.agents/notes/implemented/architecture/2026-11-13-ptc-output-truncation.md:9`).
- **Tradeoff.** A tool that returns more than the ceiling loses the tail unless a spill provider is mounted. The canonical value is not written to the log, as upstream, so replay reproduces presentation and not intermediate values.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-3](../design.md#dh-o-3), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-TOOLS-6`, `DH-T-TOOLS-7`.

<a id="dh-adr-tools-5"></a>
### DH-ADR-TOOLS-5 The pipeline enforces a deadline on every call, waits a bounded time, then detaches

- **Decision.** Stage 5 gives every call a budget: `Deadline(args)` if declared, else an operator override, else the declaration's `Timeout`, else `defaultTimeout`. A tool opts out only by declaring `tools.NoDeadline`, and a golden test lists the tools that do. The budget counts time in stage 5 only and pauses while a nested call of that outer call waits for approval. When the budget ends, or the turn is cancelled, the body's context is cancelled with a typed cause and the pipeline waits `grace`, or the tool's declared `Grace` when that is longer (a workflow that must stop a process tree). If the body has not returned, the call settles as `tools/outcome-unknown` and the body is detached ([§6.3](#63-ownership-failure-retry-rollback-and-cleanup)).
- **Why.** Upstream's deadline is an optional plugin, applies only to tools that ask, and cannot produce a result until the body returns. In Go only first-party bodies run in the host, and a body that owns a process is stopped by the process stop sequence, so a hang is a defect; a defect should cost one call, not the agent. The same rule answers the time limit that upstream's sandboxed program runtime left open (unit `UP-DEBT-found.Q3.7`): a model-written script runs inside the hosting tool call, so it has that call's budget, a nested call has its own budget inside it, and the script's process tree is stopped when the budget ends ([DH-ADR-PTC-5](ptc-workflow.md#dh-adr-ptc-5) adds its memory and process-count limits); `DH-T-TOOLS-8` asserts it.
- **Tradeoff.** It reverses upstream's "never abandon a started body", and it changes the sentence "started work must settle before the executor returns" in [../design.md §5](../design.md#5-failure-retry-rollback-and-cleanup). A detached body may still act after its call was reported; the result says so and the tool is refused until it returns. Go cannot kill a goroutine, so a body that never returns leaks until the host exits.
- **Serves.** [DH-O-3](../design.md#dh-o-3), [DH-O-6](../design.md#dh-o-6), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-TOOLS-8`, `DH-T-TOOLS-9`, `DH-E-TOOLS-3`.

<a id="dh-adr-tools-6"></a>
### DH-ADR-TOOLS-6 The tool endpoint is a new entry, not a new authority

- **Decision.** This confirms the outline of [../design.md §4.4](../design.md#44-process-model) with these details.
  This is the one definition of the call token; areas A09, A10, A11 and A24 use it as written.
  1. **Reaching the host.** The host listens on a Unix domain socket, using the standard library on every platform, in `EndpointDir()`: a directory of mode 0700 owned by the user, `$XDG_RUNTIME_DIR/dsh-<instance>` where that variable is set and otherwise a per-user, per-instance directory under the system temporary directory, short enough for the platform's socket path limit. It is never inside `<home>/run` or `<home>/secrets` and never inside a write root, so a sandbox backend can admit this one directory and still deny the host's control socket. The peer rule of the control channel ([host-telemetry.md](host-telemetry.md), `DH-ADR-HOST-12`) does not apply here: this endpoint's callers are the untrusted process trees, which is why it has no method that reaches an interaction, a grant, a preset or a secret. The script environment is exactly two variables, `DSH_TOOL_ENDPOINT` (the socket path) and `DSH_TOOL_TOKEN`; the spawn helper never puts the control socket's path, a launch link or a browser session into a child's environment (`DH-ADR-EXEC-8`).
  2. **What a token is and who owns it.** 32 random bytes from the system generator, minted when the body of a hosting tool calls `Run.ScriptEnv()`, at most one per outer call. It is minted only for a root call (depth 0) of a tool whose declaration says `HostsScripts`, in a scope where `AllowScriptCalls` names that tool. Its owner is that outer call. A token exists in host memory only: it is never written to the session log, a file the host keeps, or a log line, and the spawn helper masks its value in captured output.
  3. **Attribution.** An in-memory table maps the token to session, agent, turn, step, outer call id, the outer call's step view and its context. A nested call is resolved against that step view, runs with that agent as initiator and is logged with the outer call as parent.
  4. **No authority.** A nested call enters stage 0 like any call. Policy is resolved for the attributed agent when the nested call arrives. The outer call's approval or sandbox escalation covers the spawn only; a nested `Ask` goes through the same approval service to the agent's answerer, and with none the answer is no. A grant of the attributed agent's own session applies to a nested call exactly as to a direct one, by subject and never by entry.
  5. **Delivery.** The hosting body hands the two variables to the spawn helper (A09), which sets them only from that hand-over. A one-shot command receives them in the environment of the process the outer call starts. A persistent shell is already running, so the command of one persistent-shell tool call receives them through A09's private per-command file, which exports them for that command only; the token is still owned by that tool call. Nothing else receives a token: not a command started by a nested call, not a raw send to a terminal, not a terminal a person types into.
  6. **Revocation.** The token is deleted when the outer call leaves stage 5, before the outer result is appended; a hosting body may delete it earlier with `Run.RevokeScriptEnv()`, and A09's stop ladder does so as its first rung. A process left behind, a background process in a persistent shell included, can therefore not call later.
  7. **A token that outlives its call: only for a registered job** (decided by the owner, [DH-ADR-29](../design.md#dh-adr-29)). A11's background workflow ([DH-D-PTC-6](ptc-workflow.md#dh-d-ptc-6)) keeps calling tools after the call that started it has returned. Only a tool that declares `HostsScripts: job` may do this, and only the `workflow` tool declares it; a test over the catalogue fails if any other tool does. Such a tool hands its token over to a job it registers with A13's registry ([DH-D-JOBS-1](jobs-schedule-goal-plan.md#dh-d-jobs-1)), which the user can see and stop in the jobs list. The job then owns the token: nested calls are attributed to the job's agent with the job id as parent and no turn or step, every one still passes policy, guards and approval, each `Ask` goes to the agent's answerer, and the token is deleted when the job settles or is stopped, before the job's settlement is appended. If the hand-over fails (the registry refuses the job), the token stays with the call and dies with it. Shell tools declare `call`: a shell command moved to the background never keeps a token.
- **Why.** The owner decided that scripts call tools through a command line and that every such call is checked and logged ([DH-ADR-8](../design.md#dh-adr-8)). The script is untrusted and can read its environment, so the token must be worth no more than what the model can already propose.
- **Tradeoff.** A script waits while a nested call waits for a person. A background script cannot call tools after its outer call ends, except a workflow job under item 7, whose approval prompts can therefore arrive while the user looks at something else. Reachability of the socket from inside each sandbox backend, and Unix sockets on Windows, are unverified (open questions 1 and 2); where it fails the result is `unavailable`, and the policy is never loosened. A script on a remote host has neither `dsh` nor the socket: there is no relay in this design.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-4](../design.md#dh-o-4), [DH-O-7](../design.md#dh-o-7), [DH-O-9](../design.md#dh-o-9). **Verified by.** `DH-T-TOOLS-10` to `DH-T-TOOLS-13`, `DH-E-TOOLS-2`.

<a id="dh-adr-tools-7"></a>
### DH-ADR-TOOLS-7 Nested calls: scheduled under their outer call, bounded per call, visible only through the script's output

- **Decision.** A call that hosts scripts is exclusive in its batch. Its nested calls are scheduled by a scheduler that belongs to the outer call and applies the same rule: parallel-safe calls overlap up to `maxParallelNested`, an exclusive call runs alone, admission is in arrival order. Bounds per outer call are in [§6.2](#62-contracts). A bound refuses that one nested call with `tools/limit` naming the bound; the script continues. A nested result reaches the model only as part of the script's output, which is the outer call's logged result; with `attach`, the nested result's image blocks are appended to the outer result at settle. Context that a nested call adds is carried on the outer result. A nested call cannot conclude the turn.
- **Why.** Making the outer call exclusive means nested calls never compete with another root call, so one small scheduler is enough and there is no deadlock with the batch. Refusing one call follows the truncation rule of [DH-ADR-TOOLS-4](#dh-adr-tools-4); upstream ends the whole program instead (`packages/core/tool-executor-ptc/README.md:39`).
- **Tradeoff.** Two shell calls in one step run one after the other whenever script calling is on. A script that ignores `tools/limit` can keep failing until the outer deadline. Each nested call costs one process start.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-4](../design.md#dh-o-4), [DH-O-9](../design.md#dh-o-9). **Verified by.** `DH-T-TOOLS-11`, `DH-T-TOOLS-12`.

<a id="dh-adr-tools-8"></a>
### DH-ADR-TOOLS-8 Prompt assembly is a pure function with closed ways to change it

- **Decision.** `Assemble` computes, for one step, the ordered sections, the resolved variables, the runtime contexts and the ordered tool declarations from the registries and the step view. A component changes the result in three ways only: a scoped section or variable shadows the global one of the same name; `Suppress` removes optional runtime context for a scope; one `Complete` section replaces all sections. There is no around event over the assembly and no change event. Interpolation is strict (an unknown or valueless reference fails assembly), `{{{{` is a literal `{{`, and substituted values are not rescanned. Tools are ordered by `toolOrder` or by name. The assembly carries a fingerprint and a manifest of section names, orders and owning components.
- **Why.** The prompt is rebuilt every step, so nothing needs a push notification, and closed operations let the host answer "which component supplied this text". A deterministic order keeps the provider's prefix cache valid.
- **Tradeoff.** A component that upstream implemented as an assembly listener must become a shadowing or suppressing registration (open question 6). The driver (A02), not this area, logs the result: the rendered text as `system/message` when the fingerprint changes, the tool declarations in `request/header`, the contexts as user-role snapshots.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-2](../design.md#dh-o-2). **Verified by.** `DH-T-TOOLS-14`, `DH-T-TOOLS-15`, `DH-E-TOOLS-1`.

<a id="dh-adr-tools-9"></a>
### DH-ADR-TOOLS-9 The repeat reminder is derived from the log

- **Decision.** The chain (hash of the last tracked call, run length) is a session projection folded from `tool/call` events of origin `model`. It resets on a human user message and on the compaction event. The reminder is added as context by a `tools.Shape` listener at the configured counts, so it follows the batch's results as a logged event. It stays advisory unless `blockAt` is set, in which case a guard denies the call at that count. Chains are per session and are not shared with subagents.
- **Why.** The reminder is model-visible. Computed from memory it differs after a resume; computed from the log it is the same in replay ([DH-O-4](../design.md#dh-o-4)). A deny-only guard is the right shape for the blocking form.
- **Tradeoff.** One more projection to fold on every `tool/call`. Nested calls are not counted, so a looping script is bounded by `tools/limit` and the outer deadline, not by this guard.
- **Serves.** [DH-O-3](../design.md#dh-o-3), [DH-O-4](../design.md#dh-o-4). **Verified by.** `DH-T-TOOLS-16`.

<a id="dh-adr-tools-10"></a>
### DH-ADR-TOOLS-10 `todo_write` stays a whole-list tool owned by one session

- **Decision.** Keep upstream's contract: one tool, one operation (replace the whole list), items of `content` and a three-state `status`, one list per agent session, a required deployment choice on parallel `in_progress` items. No read-back tool, item ids, partial update or shared list.
- **Why.** A whole-list event needs no merge and replays as last-write-wins, which is the rule for state-carrying events ([../design.md §4.6](../design.md#46-event-model)). The model already holds the list in its context, so a read-back tool would cost tokens for nothing.
- **Tradeoff.** The model resends the list on every change. A team or parent cannot see a child's list except through the UI, which reads each session's projection.
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-TOOLS-17`.

## 8. Deliverables

<a id="dh-d-tools-1"></a>
### DH-D-TOOLS-1 A tool is declared once and registered in a scope

- **Result and why it matters.** A tool author writes one Go value and gets the model-facing schema, argument checking and a catalogue entry. An agent sees exactly the tools its scope allows. Without this every other area has nowhere to put a tool.
- **How it is implemented.** `tools.Define` derives schemas through `schema` at registration and fails with every offending path when the input schema leaves the model-facing subset. The registry files entries under a scope key; a view is computed from the global layer, the ancestors' layers under intersected restrictions, and the scope's own entries. `Resolve` evaluates `When` conditions once and returns an immutable step view carrying each tool's reach and disclosure. `Catalogue()` feeds the generated tool catalogue ([DH-ADR-ARCH-12](../upstream/architecture.md#dh-adr-arch-12)). `tools.Dynamic` covers MCP and plugin tools.
- **Components and files.** `internal/tools` (contract), `internal/tools/core` (registry part of the `tools.registry` component).
- **Depends on.** [DH-D-CORDIS-3](../libs/cordis.md#dh-d-cordis-3) (scoped values and dispatch); [DH-D-SESSION-8](core-session.md#dh-d-session-8) for scope keys and `Layers`; [DH-D-UTIL-1](util-test-support.md#dh-d-util-1) for the `schema` package with a closed-root option and the subset check. Runtime prerequisite: none.
- **Priority.** P0. **Outcome.** [DH-O-6](../design.md#dh-o-6), [DH-O-14](../design.md#dh-o-14), [DH-O-1](../design.md#dh-o-1).
- **Done when.** `DH-T-TOOLS-1`, `DH-T-TOOLS-2` and `DH-T-TOOLS-18` pass.

<a id="dh-d-tools-2"></a>
### DH-D-TOOLS-2 Every tool call passes one guarded pipeline

- **Result and why it matters.** A call from the model, from host code or from a script goes through the same eight stages and always leaves one paired, bounded result. This is [DH-O-6](../design.md#dh-o-6).
- **How it is implemented.** One unexported `run(entry)` implements stages 0 to 7. The batch scheduler admits calls in order, runs stages 0 to 4 serially, overlaps bodies in a pool of `maxParallel` with exclusive barriers, and commits in order. Decisions are sealed Go interfaces, so a listener cannot return anything outside the closed set. `Call` exposes copies. The durable form of a result is encoded once, before `tools.Settled`.
- **Components and files.** `internal/tools/core`.
- **Depends on.** [DH-D-TOOLS-1](#dh-d-tools-1), [DH-D-CORDIS-4](../libs/cordis.md#dh-d-cordis-4). [DH-D-CORDIS-7](../libs/cordis.md#dh-d-cordis-7): the around key with separate request and result types (listeners register with `On`, the pipeline dispatches with `Around`, and a listener's error or recovered panic reaches the pipeline as an error, which ends the call as a failure). [DH-D-SESSION-2](core-session.md#dh-d-session-2) for event registration and append; [DH-D-STORE-4](session-store.md#dh-d-store-4) for the checkpoint policy; [DH-D-HUMAN-2](interaction-credentials.md#dh-d-human-2) for `approval.Service.Request`, which also matches session grants. **Used by.** The driver, which calls `ExecuteBatch` ([DH-D-AGENT-3](agent-loop.md#dh-d-agent-3)). Runtime prerequisite: with no approval service mounted every `Ask` is denied.
- **Priority.** P0. **Outcome.** [DH-O-6](../design.md#dh-o-6), [DH-O-3](../design.md#dh-o-3), [DH-O-7](../design.md#dh-o-7).
- **Done when.** `DH-T-TOOLS-3` to `DH-T-TOOLS-7` pass and `DH-E-TOOLS-1` has passed.

<a id="dh-d-tools-3"></a>
### DH-D-TOOLS-3 Tool calls have enforced deadlines and never hold a turn

- **Result and why it matters.** A stuck tool costs one call. The user can always cancel a turn, and the operator can list what is stuck.
- **How it is implemented.** A pausable budget timer per call in stage 5; cancellation with a typed cause; a bounded wait; the detached list on the `tools.registry` fiber, a `tools/busy` refusal while a detached body of the same tool lives, and `tool/late-return` when it ends. A conformance helper in `internal/testkit` runs any declaration's body with a context that is cancelled and fails the test if the body does not return in time.
- **Components and files.** `internal/tools/core`; a helper in `internal/testkit` (A23).
- **Depends on.** [DH-D-TOOLS-2](#dh-d-tools-2); [DH-D-UTIL-6](util-test-support.md#dh-d-util-6) for the test kit. Runtime prerequisite: none. **Related, not a dependency.** A process-owning tool stops its own tree within `grace`, by [DH-D-EXEC-2](execution.md#dh-d-exec-2).
- **Priority.** P0. **Outcome.** [DH-O-3](../design.md#dh-o-3), [DH-O-6](../design.md#dh-o-6), [DH-O-14](../design.md#dh-o-14).
- **Done when.** `DH-T-TOOLS-8` and `DH-T-TOOLS-9` pass and `DH-E-TOOLS-3` and `DH-E-TOOLS-4` have passed.

<a id="dh-d-tools-4"></a>
### DH-D-TOOLS-4 A script calls tools through the tool endpoint, checked and logged like any call

- **Result and why it matters.** The model can write a loop over tool calls in any language on the machine, and the log still shows every tool that ran and who approved it. This replaces `run_code` and the guest runtime.
- **How it is implemented.** The `tools.endpoint` component owns the listener, the token table and one nested scheduler per outer call. A handler reads one bounded line, checks the token before anything else, and for `call` builds a nested `entry` and calls the same `run`. Bounds are counters on the token's record. Every byte from the socket is untrusted: an oversized, malformed or unknown request fails that connection only.
- **Components and files.** `internal/tools/endpoint`; the client is `cmd/dsh` (A24).
- **Depends on.** [DH-D-TOOLS-2](#dh-d-tools-2), [DH-D-TOOLS-3](#dh-d-tools-3), [DH-LIB-BIRPC](../libs/birpc.md), [DH-D-SDK-1](sdk-acp-typert.md#dh-d-sdk-1) for the codec `src/sdk/jsonrpc`, and [DH-D-JOBS-1](jobs-schedule-goal-plan.md#dh-d-jobs-1) for the job that may own a token ([DH-ADR-29](../design.md#dh-adr-29)). **Used by.** The command surface ([DH-D-CLI-6](app-cli.md#dh-d-cli-6)); the spawn helper, which sets the two variables only from the script environment a body hands it, puts the running binary's directory first on the search path and masks the token value in captured output ([DH-D-EXEC-4](execution.md#dh-d-exec-4), [DH-D-EXEC-10](execution.md#dh-d-exec-10)); each sandbox backend admitting `EndpointDir()` and nothing else new ([DH-D-SANDBOX-7](sandbox-ssh.md#dh-d-sandbox-7)); the mode that registers `AllowScriptCalls` and teaches the model ([DH-D-PTC-1](ptc-workflow.md#dh-d-ptc-1)); and an approval prompt that shows the nested call and its outer call ([DH-D-HUMAN-2](interaction-credentials.md#dh-d-human-2)). Runtime prerequisite: a private runtime directory on a local filesystem.
- **What A24 must provide.** `dsh tool call <name>`, `dsh tool list`, `dsh tool describe <name>`, reading only the two variables. Argument input by `--args`, `--args-file`, `--args -` and repeatable `--arg field=value`; output by default (the JSON value), `--text`, `--envelope`, `--pick <json-pointer>` with `--lines`; `--attach`. A malformed argument document is refused locally with line and column before connecting. One exit code per reply `outcome`, with a failed tool distinct from every other failure. No flag for a session, agent or turn. With no variables the command contacts nothing and reports `unavailable`; whether a person may ever use it from their own shell is A24's decision.
- **What A11 must provide.** The mode switch per preset; the prompt section and worked example, generated from `list` and `describe` output and held by a golden file; workflows restated on this entry; the values of the bounds if the defaults do not suit.
- **Priority.** P1. **Outcome.** [DH-O-6](../design.md#dh-o-6), [DH-O-4](../design.md#dh-o-4), [DH-O-7](../design.md#dh-o-7), [DH-O-9](../design.md#dh-o-9).
- **Done when.** `DH-T-TOOLS-10` to `DH-T-TOOLS-13` pass and `DH-E-TOOLS-2` and `DH-E-TOOLS-4` have passed on every supported sandbox backend.

<a id="dh-d-tools-5"></a>
### DH-D-TOOLS-5 The system prompt and tool list for each step are assembled deterministically

- **Result and why it matters.** Each model request states who the agent is, what it may call and how, built from contributions that each have one owner. The same inputs give the same bytes, so the provider cache holds and the log can reproduce the request.
- **How it is implemented.** Scoped registries for sections, variables and contexts; a table of named first-party orders in one file; `Assemble` and `Render` as in [DH-ADR-TOOLS-8](#dh-adr-tools-8). The prompt component contributes the identity line and the persona prefix and suffix from its config, which are live fields. `toolOrder` names are checked at config load against the compiled-in catalogue and declared dynamic prefixes.
- **Components and files.** `internal/tools/prompt`.
- **Depends on.** [DH-D-TOOLS-1](#dh-d-tools-1). **Used by.** The driver, which calls `Resolve` then `Assemble` once per proposed step and logs the result with its manifest ([DH-D-AGENT-3](agent-loop.md#dh-d-agent-3)); by the runtime-context contributors ([DH-D-CONTEXT-1](context-compaction-skill.md#dh-d-context-1)); and by the settings form that edits persona text through `config.Service.Apply` ([DH-D-STORAGE-8](storage.md#dh-d-storage-8)).
- **Priority.** P0. **Outcome.** [DH-O-4](../design.md#dh-o-4), [DH-O-2](../design.md#dh-o-2), [DH-O-6](../design.md#dh-o-6).
- **Done when.** `DH-T-TOOLS-14` and `DH-T-TOOLS-15` pass and `DH-E-TOOLS-1` has passed.

<a id="dh-d-tools-6"></a>
### DH-D-TOOLS-6 Tools can be disclosed on demand

- **Result and why it matters.** A deployment with hundreds of tools does not pay for every schema in every request: the model is told a name or a summary, searches, and then receives the schema.
- **How it is implemented.** The step view marks each tool `full` or deferred from its declared disclosure and the `tools.disclosed` projection. `tool_search` searches names and descriptions of reachable tools that are not yet full, returns their schemas and appends `tool/disclosed`; from the next step those tools are `full` for the session. Reach, not disclosure, decides whether a call is accepted, so a direct call to an undisclosed but reachable tool runs.
- **Components and files.** `internal/tools/discover`, the step-view code in `internal/tools/core`.
- **Depends on.** [DH-D-TOOLS-1](#dh-d-tools-1), [DH-D-TOOLS-5](#dh-d-tools-5); [DH-D-LLM-1](llm.md#dh-d-llm-1): a deferred tool is absent from the request header until disclosed, and a disclosure is a `developer/message` tool addition that each driver projects in its protocol's tool-change form; [DH-D-VIEW-1](session-views.md#dh-d-view-1) for the projection registry.
- **Priority.** P2. **Outcome.** [DH-O-6](../design.md#dh-o-6), [DH-O-4](../design.md#dh-o-4).
- **Done when.** `DH-T-TOOLS-19` passes.

<a id="dh-d-tools-7"></a>
### DH-D-TOOLS-7 A repeating agent is reminded, and can be stopped

- **Result and why it matters.** An agent stuck on one call is nudged to change approach before it burns the budget, with the same reminder on replay as live.
- **How it is implemented.** As [DH-ADR-TOOLS-9](#dh-adr-tools-9). The key is the tool name plus the declaration's `RepeatKey(args)` or canonical JSON with sorted keys; `RepeatExempt` tools and names excluded by config neither count nor reset. The first threshold uses the short text, later ones name the tool, the count and a bounded preview of the arguments, as upstream.
- **Components and files.** `internal/tools/guard/repeat`.
- **Depends on.** [DH-D-TOOLS-2](#dh-d-tools-2); [DH-D-VIEW-1](session-views.md#dh-d-view-1) for projections; [DH-D-UTIL-4](util-test-support.md#dh-d-util-4) for the glob helper. **Used by.** The chain resets on the compaction event of [DH-D-CONTEXT-5](context-compaction-skill.md#dh-d-context-5).
- **Priority.** P1. **Outcome.** [DH-O-3](../design.md#dh-o-3), [DH-O-4](../design.md#dh-o-4).
- **Done when.** `DH-T-TOOLS-16` passes.

<a id="dh-d-tools-8"></a>
### DH-D-TOOLS-8 The model keeps a visible task list

- **Result and why it matters.** The model plans multi-step work in a list the user can watch; the list survives a restart because it is in the log.
- **How it is implemented.** `todo_write` validates the list (non-empty unique content, at most one `in_progress` unless the config allows several), appends `todo/write` with the whole list and returns counts. The `todos` projection folds it and clears at `turn/start`. A call with no attributed agent fails, since there is no session to write to.
- **Components and files.** `internal/tools/todo`.
- **Depends on.** [DH-D-TOOLS-1](#dh-d-tools-1), [DH-D-TOOLS-2](#dh-d-tools-2); [DH-D-VIEW-1](session-views.md#dh-d-view-1) for projections. **Used by.** The list is shown by [DH-D-UICONV-7](ui-conversation.md#dh-d-uiconv-7).
- **Priority.** P1. **Outcome.** [DH-O-4](../design.md#dh-o-4), [DH-O-12](../design.md#dh-o-12).
- **Done when.** `DH-T-TOOLS-17` passes.

## 9. Acceptance

### 9.1 Local tests

Unit and composition tests with fakes for the model, the session store, the answerer and tool bodies. They run under the race detector ([DH-ADR-TOOLCHAIN-2](../upstream/toolchain.md#dh-adr-toolchain-2)).

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-TOOLS-1` | `DH-D-TOOLS-1` | For a set of declarations, the derived schema is byte-stable, a keyword outside the subset fails registration with every path, a duplicate name in one layer fails, and the catalogue lists each tool once |
| `DH-T-TOOLS-2` | `DH-D-TOOLS-1` | A scoped tool shadows the global one; restrictions intersect and spare the scope's own tools; a tool registered through one agent's context is invisible to another and gone after disposal; a failing `When` fails `Resolve` and publishes no partial view |
| `DH-T-TOOLS-3` | `DH-D-TOOLS-2` | A recording listener at every stage sees the fixed order for all three origins; a table of entries shows no input field that skips a stage |
| `DH-T-TOOLS-4` | `DH-D-TOOLS-2` | Arguments that are not JSON, not an object, too large or that break the schema never reach a policy listener, an answerer or the body; the result lists every violation with its path; the logged argument bytes equal the bytes received |
| `DH-T-TOOLS-5` | `DH-D-TOOLS-2` | A guard's denial holds under every listener order and is evaluated before the answerer is asked; `Ask` with no interactions service, no agent or no answerer is denied with the matching code; a panicking listener, guard or answerer yields a failure result and the turn continues |
| `DH-T-TOOLS-6` | `DH-D-TOOLS-2` | In a mixed batch, calls whose `Overlap` allows it overlap up to `maxParallel` (changed live between two batches), a call whose read or write key conflicts with a running call waits as a barrier, an exclusive call and a forced-exclusive tool run alone, results commit in the model's order, and cancellation mid-batch leaves one result per call with the right code for started and unstarted calls |
| `DH-T-TOOLS-7` | `DH-D-TOOLS-2` | A value that breaks the output schema becomes `tools/invalid-output`; `Block` turns success into failure and nothing turns failure into success; content above the ceiling is cut at a block boundary and marked; a failing `tools.Settled` observer changes nothing |
| `DH-T-TOOLS-8` | `DH-D-TOOLS-3` | With virtual time: the budget is chosen in the stated precedence; a body that returns after cancellation within `grace` gives `tools/timeout`; the budget does not run during stages 0 to 4 and pauses during a nested approval; a hosting call whose script keeps calling tools ends at its own budget, its token is deleted and its nested calls settle; the golden list of `NoDeadline` tools matches |
| `DH-T-TOOLS-9` | `DH-D-TOOLS-3` | A body that ignores its context is detached after `grace`: the result is `tools/outcome-unknown`, the turn can end, the next call to that tool is `tools/busy`, and its eventual return appends `tool/late-return`; the conformance helper fails such a body |
| `DH-T-TOOLS-10` | `DH-D-TOOLS-4` | A token is minted only for a depth-0 call of a script-hosting tool in a scope that allows script calls for that tool; a persistent-shell command gets it only through the per-command hand-over and a raw send gets none; only the `workflow` tool declares `job`, a token handed to a registered job serves nested calls after the outer call returned and is refused once the job settles or is stopped, and a shell command moved to the background has none; a missing, forged or deleted token is refused before stage 0 and appends no event; the token value appears in no session event and no log line; with agent A's token a tool only agent B sees is `tools/unknown-tool` and no request field changes the attribution |
| `DH-T-TOOLS-11` | `DH-D-TOOLS-4` | A nested call passes every stage: a tool with reach `direct` is refused, an `Ask` reaches the agent's answerer although the outer call was approved, with no answerer it is denied, a grant of the same session covers a nested call by its subject, and the pipeline itself appends no approval or grant event. Each nested call leaves `tool/subcall` and `tool/subresult` with the outer id as parent, all before the outer `tool/result` |
| `DH-T-TOOLS-12` | `DH-D-TOOLS-4` | Each bound refuses one nested call with `tools/limit` naming the bound and the next call is still served; nested scheduling follows the parallel and exclusive rule; when the outer call is cancelled or its deadline passes, the token is deleted first, running nested calls settle, and a later request with that token is refused |
| `DH-T-TOOLS-13` | `DH-D-TOOLS-4` | On a raw connection an oversized line, malformed JSON, an unknown `op`, an unknown field and a slow sender each fail that connection only while a valid concurrent call completes; fuzzing the request decoder finds no panic; the socket directory is removed on unload and a stale one is replaced at start |
| `DH-T-TOOLS-14` | `DH-D-TOOLS-5` | Golden: a fixed set of sections, variables, contexts and tools renders to fixed bytes in fixed order; a scoped shadow, `Suppress` and `Complete` each change exactly their part; the manifest names each section's owner |
| `DH-T-TOOLS-15` | `DH-D-TOOLS-5` | An unknown or valueless variable and two complete sections fail assembly; `{{{{` renders `{{` and a substituted value is not rescanned; a `toolOrder` naming an unknown tool fails config load with file and line and one naming a hidden tool does not; a persona edit through the config layer changes the next assembly |
| `DH-T-TOOLS-16` | `DH-D-TOOLS-7` | The reminder appears at each threshold and every `repeatEvery` after the last; a user message and a compaction event reset the chain; exempt and excluded tools neither count nor reset; nested calls are ignored; replaying the session log yields the same reminders; `blockAt` denies at that count through a guard |
| `DH-T-TOOLS-17` | `DH-D-TOOLS-8` | A valid list appends one `todo/write` and updates the projection; duplicates, empty content, an unknown item key and a second `in_progress` under the single policy are refused before the body; `turn/start` clears the projection; a call with no agent fails |
| `DH-T-TOOLS-18` | `DH-D-TOOLS-1` | Import direction: no package outside `internal/tools` imports `internal/tools/core` or `endpoint`, and the contract package imports no provider |
| `DH-T-TOOLS-19` | `DH-D-TOOLS-6` | A deferred tool is absent from the full declarations, is found by `tool_search`, is `full` in the next step's view and after a replay of the log; a direct call to an undisclosed reachable tool runs and one to an unreachable tool does not |

### 9.2 Real-environment scenarios

<a id="dh-e-tools-1"></a>
#### DH-E-TOOLS-1 A real model's tool calls pass the pipeline and the request can be rebuilt from the log

- **Proves.** `DH-D-TOOLS-2`, `DH-D-TOOLS-5`.
- **Given.** A real model API with a key supplied at run time; the built `dsh` binary with the `headless` profile on an empty harness home; a temporary workspace with three known files; one read tool and one policy listener that returns `Ask` for a write tool; an answerer that rejects.
- **When.** 1. Send one message that needs two reads and one write. 2. Let the turn finish. 3. Rebuild every model request of the session from the log with the replay command.
- **Then.** The log holds one `tool/call` and one `tool/result` per call block, in the model's order. The write has an `approval/rejected` result and the file is unchanged. Each rebuilt request equals the request that was sent, including the system text and tool declarations. A second run with the same inputs sends the same system text and declarations byte for byte.
- **Cleanup.** Delete the home and the workspace. If a rebuilt request differs, keep the session export and both request bodies.

<a id="dh-e-tools-2"></a>
#### DH-E-TOOLS-2 A sandboxed script calls a tool that needs approval, and cannot skip it

- **Proves.** `DH-D-TOOLS-4` (and the verification named by [DH-ADR-8](../design.md#dh-adr-8)).
- **Given.** One run per supported sandbox backend (Linux, macOS, Windows) on a real machine; policy `workspace-write` with no network; the built binary; script calling enabled for the agent; a replay provider that issues one shell call running a fixed script; a write tool that a policy listener answers with `Ask`. The script calls `dsh tool call` for a read tool, then for the write tool, then tries to open an outside address and to reuse its token after exit.
- **When.** 1. Run with an answerer that approves the outer shell call and nothing else. 2. Run again with no answerer. 3. Run again with an answerer that approves the nested write.
- **Then.** In all runs the read succeeds and the log holds a `tool/subcall` and `tool/subresult` pair parented to the shell call, before its result. In run 1 the nested write is `approval/rejected` although the outer call was approved; in run 2 it is `approval/unavailable`; in both the file is unchanged and the command exits with the "denied" code. In run 3 the write happens and the decided record names the answerer. The outside address is unreachable in every run. The token reused after the outer call ended is refused and appends nothing. The token value is in no event.
- **Cleanup.** Remove the workspace and home. On Windows run the sandbox cleanup command (A10). If the endpoint was unreachable under a backend, keep the sandbox profile and host log; that backend's run is open, not passed.

<a id="dh-e-tools-3"></a>
#### DH-E-TOOLS-3 A command that ignores termination is stopped at its deadline and leaves no process

- **Proves.** `DH-D-TOOLS-3`.
- **Given.** A real machine per operating system; the built binary; a shell tool call (A09) with a 5 second budget running a program that ignores the polite stop request and starts one child.
- **When.** 1. Start the call. 2. Wait for the result. 3. List processes started by the run.
- **Then.** The result is `tools/timeout` within the budget plus `grace` plus a small margin. No process of the run remains. The turn continues and the next tool call is served. Cancelling the turn during an identical call gives `tools/aborted` within the same bound.
- **Cleanup.** None if no process remains. Otherwise record each remaining process id and command line, then kill it.

<a id="dh-e-tools-4"></a>
#### DH-E-TOOLS-4 A looping script is bounded and the host stays responsive

- **Proves.** `DH-D-TOOLS-4`, `DH-D-TOOLS-3`.
- **Given.** A real sandbox as in `DH-E-TOOLS-2`; `maxNestedCalls` set to 50; a script that calls a read tool in an endless loop and ignores failures; a 20 second budget on the shell call.
- **When.** 1. Start the call. 2. While it runs, send a second session a message. 3. Wait for the result.
- **Then.** Exactly 50 nested pairs are logged; later attempts are refused with `tools/limit` and append nothing. The second session is answered while the loop runs. At the deadline the outer result is `tools/timeout`, no process remains, and the log has no `tool/subcall` without a `tool/subresult`.
- **Cleanup.** Remove the workspace and home; record and kill any remaining process.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed. These scenarios show the stated contracts for the tested build, inputs and machines; they do not show that a model writes correct scripts, and they say nothing about what a script does directly under the sandbox.

## 10. Open questions and limitations

**Open questions.**

1. **Can a confined command open the endpoint socket?** [sandbox-ssh.md §6.1](sandbox-ssh.md#61-shape) states, per backend, how `EndpointDir()` is admitted; that is as far as a document can go. It is unverified until `DH-E-TOOLS-2` passes on a backend, and until then script calling is `unavailable` there.
2. **Unix domain sockets on Windows.** This design uses the standard library's Unix sockets on Windows to avoid a new module. Whether a restricted-token process can connect needs a run. The fallback is a loopback TCP port, where the token is the only gate and "only the same user can open it" no longer holds; choosing it would be a new decision record here.

**Carried limits (six ledger rows, stage [DH-S-47](../implementation-plan.md#dh-s-47)).** An in-process body cannot be killed ([DH-ADR-TOOLS-5](#dh-adr-tools-5)). Logged arguments cannot be rewritten ([DH-ADR-TOOLS-3](#dh-adr-tools-3)). Repeat chains are per session ([DH-ADR-TOOLS-9](#dh-adr-tools-9)). The todo list is single-owner, minimal and whole-list ([DH-ADR-TOOLS-10](#dh-adr-tools-10)).

**Limitations.** A goroutine cannot be killed, so a first-party body that ignores cancellation leaks until the host exits; the design bounds the harm and reports it. A detached body may act after its call was reported as unknown. What a script does directly, without `dsh tool call`, is recorded only as the outer command's output. Canonical tool values are not in the log, so replay reproduces what the model saw, not what a script received. All defaults in [§6.2](#62-contracts) are recommended defaults, not confirmed by the owner, and no measurement backs them; the cost of one process start per nested call is unmeasured.

## 11. Units dispositioned by this spec

Rows are in `trace/fragments/A03.jsonl`.

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 7 | 4 ported, 2 merged, 1 replaced |
| `UP-DEBT` | 28 | 17 resolved, 6 carried (each kept by a recorded decision), 5 not-applicable |
| `UP-TOOL` | 2 | 1 ported, 1 dropped |
| `UP-EVT` | 8 | 5 ported, 2 replaced, 1 dropped |
| `UP-CFG` | 4 | 3 ported, 1 replaced |
| `UP-SVC` | 2 | 2 ported |
| `UP-DEC` (handed over) | 13 | 7 ported, 4 replaced, 2 dropped |

This document declares `DH-ADR-TOOLS-1` to `-10`, `DH-D-TOOLS-1` to `-8`, `DH-T-TOOLS-1` to `-19` and `DH-E-TOOLS-1` to `-4`.
