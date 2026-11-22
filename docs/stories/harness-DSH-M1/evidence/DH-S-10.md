# DH-S-10 evidence

Decisions that govern M1 records, this one included, are logged in [decisions.md](../decisions.md).

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The "DH-S-10 is closed" statement below is the stage's own closure, kept as history:
under M1-D-CLOSE-1 every tick made while the gate was red is provisional and is
re-verified at that commit, and plan step 16.6.1 unticks impl.md 10.3, 10.4 and 10.6
(known not built) until plan Stages 16.1 to 16.3 land. The fields of impl.md sec. 7.3
and the per-step manual-check register are at the end of this file, in "Evidence record
(impl.md sec. 7.3)"; how to fill them is in [the evidence runbook](README.md).

DH-S-10 is the tool pipeline, approvals and human commands. Stage 10.1 (#72/#73)
built impl.md steps 10.1 and 10.2, Stage 10.2 (#74/#75) built 10.3 and 10.4, and
Stage 10.3 (#76, `69c5f17`, merged into this branch) built 10.5 and 10.6 and
authored `DH-E-TOOLS-3`. Stage 10.4 built steps 10.7 to 10.10 and authored
`DH-E-HUMAN-6`.

**DH-S-10 is closed:** impl.md steps 10.1 to 10.10 are ticked as built, and the
combined branch passes the tests below. The three manual checks below are
scripted by tests here; their interactive runs are for the operator.

Machine: Windows, go toolchain per `src/harness/go.mod` (floor `go 1.26.0`).

## What was built

| Step | Package | What it does |
| --- | --- | --- |
| 10.7 DH-D-AGENT-2 | `internal/agent` (`inbox.go`) | `Inbox` over the session log. `Send` dedups on the message id, appends `agent/inbox/spliced` and flushes before it returns, so the item is durable at once (SP-5). A second send with the same id is refused with `agent/duplicate-message` and the first receipt. Next-step items are taken only by `ClaimStep`, at a step boundary. Next-turn items are taken by `ClaimTurn`, at a turn boundary. The claim is one group: `turn/start`, the `claimed` splice and the `user/message`. `OpenInbox` folds the log, so a claimed item is never run again after a crash or resume. `Discard` removes an item with a cause. `SendWith` and `Hand` write an admission and the producer's own record (an answer's `question/answered`) as one group, so no crash separates them. `Hand` admits and discards in one group, for an answer a waiting call takes. A group is folded once appended. If its flush then fails, the inbox is marked not durable and a next-turn admission does not wake the agent yet. `Sync` (or any later successful flush) completes the durability and then pays the owed wake. It never appends, so a resend is still a duplicate. |
| 10.8 DH-D-HUMAN-5 | `internal/human/question`, `question/tool` | `Service.Ask` logs `question/asked` and asks the agent's bound answerer. With `wait`, it blocks until the answer or the deadline and returns `pending` at the deadline. Without `wait`, it returns `pending` at once. `Reply` settles the question in one durable group: the answer's inbox admission (a user message naming the call, source `question-answer`, id `ans-<question>`) and `question/answered`, attributed with principal and channel. If the call is still waiting, the group also discards the message with cause `returned-to-call`, the call is released at once, and the registry item is withdrawn. Otherwise the answer is queued: next-step while a turn is open, or next-turn when the agent is idle, which wakes the agent and starts a turn. A failed append admits nothing, is returned (to the waiting call too, as `pending` with the error), and leaves the question open for a retry. A failed flush after the append leaves the group folded, and that group is the settlement. A waiting call is given the answer only after `Reply` has run `Inbox.Sync` and the group is in storage. If that sync fails too, the call gets the error (an error tool result, which makes `Restore` deliver the answer again), and the answer is queued again (`ans-<question>-again`, next-step while the turn is open), so the running agent still sees it. If that requeue cannot be admitted either, it stays owed: a retried `Reply` admits it first, once by id, and only then syncs and reports recovery. A queued answer's replier gets the error. A retried `Reply` runs `Inbox.Sync` and returns the first receipt, which makes the answer durable and wakes the agent without admitting it twice. A background settlement that fails (a registry answer or close) is reported through `Config.OnError`, else the `Config.Owner` logger, else `slog`. `Service.Close` withdraws the running interactions and waits for them. A second reply is refused with `question/reply-queued`. `Restore` reopens unanswered questions after a restart. It treats an admitted `ans-<question>` as settled, and delivers again once (next-turn) an answer handed to a call that never showed it to the model: one with no tool result, or only a closer or an error result. The `ask_user_question` tool declares the call. |
| 10.9 DH-D-HUMAN-6 | `internal/human/command` | A scoped `Registry` (nearest scope wins; `List` merges scopes). `Parse` and `Execute` run a `/name args` line from any client against an agent, with no model turn. An unknown name, invalid typed arguments or an owner-only command from a non-owner is refused and logs nothing. Otherwise `command/run` and `command/done` (ok, failed or aborted) are paired and attributed. A handler that ignores its deadline settles `aborted`, and a panic settles `failed`. `Call.Send` delivers what the command hands the agent through the inbox, attributed to the person (source `command`). |
| 10.10 DH-D-HUMAN-3 | `internal/human/approval` (`grants.go`, `approval.go`) | Session grants of one directory (canonical path, symlinks resolved, separator-bounded prefix) or one application (provider and app id; a PID is refused). They are listable and revocable, and capped at 32 per session. They are logged as `grant/created` and `grant/ended`. They are held per agent incarnation, so a resume, fork or subagent starts with none (NG-8). `Grants.Bind` ties the table to the agent lifecycle. On `agent/created` it ends every grant the session log leaves open (`grant/ended{session-ended}`) and drops older incarnations. On `agent/disposed` it ends the agent's grants while the session is live, and forgets them either way. `EndAll` forgets every grant even when its `grant/ended` cannot be written (failing closed), and returns the joined errors, which the disposal hook reports. The unended `grant/created` is closed at the next resume. Only an owner channel (ui, cli or tui) can create one. `approval.Service.Request` approves a call whose `Subject` a grant covers (detail `allowed-by-grant:<id>`), and turns an `allowed-session` answer with a subject into a grant. A grant refused by rule approves once (detail `grant-refused:<why>`). A `grant/created` that cannot be written refuses the call (`grant-audit-failed`) and returns the error. |

## Tests (TC-10a, TC-10b)

Run with `go -C src/harness test -race -count=1 ./internal/agent/ ./internal/human/... ./internal/tools/...`:

| Scenario (implementation-plan Stage 10.4) | Test | Result |
| --- | --- | --- |
| Inbox item durable before Send returns (TC-10b) | `agent` `TestInboxSendIsDurableBeforeItReturns` | pass. Against the SQLite store in `t.TempDir()`, a second connection reads the inbox row as soon as `Send` returns. |
| One accepted message is at most one turn (`DH-T-AGENT-6`, TC-10a) | `agent` `TestClaimedItemRunsAtMostOnceAcrossACrash` | pass. After a claim, the stub crashes and the session is resumed. The item is not pending, the log has one `turn/start` for it, and a resend is refused with the first receipt. |
| Idle injection and follow-ups (`DH-T-AGENT-4`, `-5`) | `agent` `TestIdleInjectionOpensNoTurnAndIsClaimedAtTheNextStep`, `TestQueuedFollowUpsAreOneTurnEach`, `TestConcurrentSendsAreOrderedAndUnique`, `TestFailedFlushOwesTheWakeUntilSync` | pass. A send whose flush fails does not wake the agent, a resend is a duplicate, and `Sync` pays the wake once. A next-step item opens no turn. Two follow-ups make two turns, and a discarded item makes none. Concurrent sends are ordered and unique, and the fold equals the live list. |
| Grant does not survive resume (`DH-D-HUMAN-3`) | `approval` `TestGrantDoesNotSurviveResume`, `TestGrantEndsWhenItsAgentIsDisposed`, `TestGrantCreateFailures`, `TestEndAllForgetsGrantsWhoseEndCannotBeWritten`, `TestGrantMatching`, `TestGrantRevokeChannelAndCap` | pass. Granting `/w` approves a later call on `/w/x` without asking. The agent is disposed and its session is resumed through the real agent registry, with no manual cleanup. The grant list is then empty, the log holds one `grant/ended{session-ended}`, and the same call asks again. A failed `grant/created` refuses the call. A failed `grant/ended` still clears the table, at `EndAll` and at disposal, and the failure is logged. A sibling prefix or a link out of the root is not covered. Revoke works, an sdk channel cannot grant, and the cap holds. |
| Question answered through the inbox (TC-10a) | `question` `TestQuestionAnsweredThroughTheInbox`, `TestTimedQuestionReturnsPendingAndTheLateAnswerIsOneUserMessage`, `TestNoBindingAndMovingOn`, `TestReplyReleasesAWaitingAsk`, `TestLateAnswerIntoAnOpenTurnIsNextStep`, `TestFailedSettlementLeavesTheQuestionOpen`, `TestFailedCloseIsReported`, `TestRestoreClosesAQuestionWhoseAnswerWasAdmitted`, `TestRestoreRedeliversAnAnswerTheCrashTookFromItsCall`, `TestFailedBackgroundSettlementIsLoggedByDefault`, `TestRetryAfterAFailedFlushCompletesTheAnswer`; `question/tool` `TestHandedAnswerUnderAFailedFlushReachesTheModelOnce`, `TestAnswerOfAFailedCallIsDeliveredAgainOnResume` (through the `tools/core` executor) | pass. With `wait`, the fake answerer's reply enters the inbox and the waiting call returns it. `question/asked` and `question/answered` are logged with attribution. A timed question returns `pending`. After a restart, a late answer is one user message naming the call. It wakes the idle agent and is claimed by `ClaimTurn`, with no turn opened by hand. A second answer is refused. An external `Reply` releases a waiting call at once and withdraws the registry item. Injected write failures admit nothing and leave the question open, and a retry succeeds. After a restart, an admitted answer is not reopened, and an answer the crash took from its call is redelivered once. With no `OnError`, a failed background settlement is logged on the owner's logger. After a failed flush, the idle agent is not woken. The retry returns the same receipt and wakes it, and nothing is admitted twice. Through the executor, an answer handed over under one failed flush reaches the model only after the session's durable offset is past its `question/answered`, and it is not redelivered on resume. When the sync fails too, the call's result is an error and the answer is queued next-step once, with no third copy on resume (`TestHandOverThatCannotBeMadeDurableFailsTheCall`). When the flush, the sync and the requeue all fail, the retried `Reply` admits the owed redelivery, wakes the idle agent and makes it durable without a restart, and a later `Restore` adds no copy (`question` `TestRetryAdmitsARedeliveryWhoseRequeueFailed`). A call that fails after the hand-over has an error result, so its answer is redelivered once. |
| Human commands (DH-T-HUMAN-9) | `command` `TestCommandRunsAttributedAndDeliversThroughTheInbox`, `TestCommandRegistryAndExecuteRules` | pass. Commands from the cli, ui and sdk clients each log run and done with attribution and deliver one command-sourced inbox item. No turn opens. Refused lines (unknown, not a command, invalid arguments, owner-only) log nothing. A scoped command shadows a global one, and a stuck handler settles aborted. |
| Deadlines and result budget (Stage 10.3: `DH-T-TOOLS-8`, `-9`; TC-10a, TC-10b) | `tools/core` `TestDeadlineDoesNotHoldTheTurn`, `TestDeadlineCooperativeBodyTimesOut`, `TestDeadlineGraceEndsAtExpiryPlusGrace`, `TestDeadlineBudgetPrecedence`, `TestConformanceCancellation`, `TestResultCeiling`, `TestOversizedResultSpilledBeforeReachingTheModel`, `TestSpillSeatFailureIsReported`; `storage/spill/policy` `TestPolicyNeverOverBudget`, `TestPolicyFailedSaveStillBounds` | pass on the combined branch. A body that returns at or after its deadline is answered `tools/timeout` (cause deadline; `TestDeadlineCooperativeBodyTimesOut`, `TestDeadlineReturnAtExpiryIsTimeout`). A body that ignores cancellation is not waited for. At budget plus grace (12 s on the virtual clock) it is detached, and its call is answered `tools/outcome-unknown` with cause deadline ("timed out after 10s"), because its effects are unknown. The step goes on to its next call, a further call of the tool is busy, and the eventual return is logged as `tool/late-return` (`TestDeadlineDoesNotHoldTheTurn`, `TestDeadlineGraceEndsAtExpiryPlusGrace`). A 2 MB result reaches the log within the budget with a spill reference, and the spill file holds all of it. |
| Denial enforced through the executor (exit evidence) | `tools/core` `TestDenialEnforcedInTheExecutor`, `TestNoAnswererMeansNoThroughTheExecutor`, `TestSchemaBreakingArgumentsNeverReachTheBody` | pass. A denied tool stays declared in the step's tool list. The call is refused by the pipeline's approval stage, and its body never runs. With no answerer the answer is no. Schema-breaking arguments never reach the body. |

Coverage: `agent` 81.6 %, `human/approval` 88.1 %, `human/question` 79.4 %,
`human/command` 85.1 %.

## Manual checks (impl.md DH-S-10)

These are `manual` and never a gate criterion (EV-6). Each is scripted by the test named here:

1. **Schema-breaking arguments never run the body.** Scripted by
   `TestSchemaBreakingArgumentsNeverReachTheBody`: the call settles with a
   validation code and the body's counter stays 0.
2. **Approval with no answerer is denied.** Scripted by
   `TestNoAnswererMeansNoThroughTheExecutor`: the outcome is `no-answerer`, and the
   call is refused with an approval code.
3. **A grant is gone after end and resume.** Scripted by
   `TestGrantDoesNotSurviveResume`: after the resume, `List` is empty and the call
   asks again.

An interactive run through a launcher needs the agent loop (DH-S-11) and the web
UI (DH-S-21), so it is for the operator once those land.

## Scenarios

| Scenario | File | Runs in |
| --- | --- | --- |
| `DH-E-TOOLS-3` | `src/harness/internal/testkit/scenarios/tools_deadline_scenario_test.go` | DH-S-39 (requires DH-S-13, DH-S-16). Authored by Stage 10.3. On the combined branch it reads the session log through a `DSH_SCENARIO_TOOLS3_LOG` hook instead of importing `session/event`, `session/store` and `storage/home`, which the scenario-boundary gate refuses for a scenario authored ahead of its stages. The `require --stage DH-S-39` expectation in `tools/harness-gate/scenarios_test.go` lists it. |
| `DH-E-HUMAN-6` | `src/harness/internal/testkit/scenarios/human_question_scenario_test.go` | DH-S-22 (requires DH-S-11, DH-S-16, DH-S-21); indexed in `test/harness/acceptance-index.yaml`. It is compiled by `go vet -tags scenario ./...` and never run by the gate. |

## Known limits

- The guarded pipeline (`internal/tools/core`) does not pass an approval `Subject`
  yet, so a grant takes effect only for callers that set `approval.Request.Subject`.
  Wiring the subject from tool calls follows with the sandbox (DH-D-SANDBOX-1).
- The inbox lives in `internal/agent`, because `internal/agent/driver` does not exist
  yet. The driver (DH-S-11) calls `ClaimTurn` and `ClaimStep` at its boundaries.

## Closure

Closed on the branch that combines Stage 10.3 (#76) and Stage 10.4: impl.md steps
10.1 to 10.10 are ticked, and the Stage 10.4 "Close DH-S-10" step is ticked in
the implementation plan.

`built-through` in `test/harness/acceptance-index.yaml` stays at `DH-S-7`, by the
index's own rule rather than by a pending decision. The marker means that every
stage through it is built (preflight runs a scenario only once `built-through` has
reached every stage it requires), and a stage raises it when it closes. DH-S-8 is
not closed: impl.md steps 8.3 to 8.5 are open, and its closure waits for Stage 8.2
(operator decision `dsh-s8-closure-waits-stage-8-2`, `DH-S-8.md`). DH-S-9 is not
closed either: impl.md steps 9.1 to 9.3 are open, as `DH-S-9.md` says. Raising the
marker past DH-S-7 would claim those stages are built, and preflight would run
scenarios that require them. The stage that closes DH-S-8 raises the marker, and
each later closing stage, this one included, raises it in turn once everything
before it is closed.

## Evidence record (impl.md sec. 7.3)

These are the eight fields of [impl.md sec. 7.3](../../../harness/impl.md#73-the-evidence-record),
in the format of [the evidence runbook](README.md#the-evidence-record-headings). Plan step
18.1.7 fills them at the frozen M1 closure commit; plan step 16.6.3 rewrites the closure
before that. Until then each field points to the history above, which stays exactly as
it was recorded and is not closure evidence (M1-D-CLOSE-1).

### Source and build identity

At closure: not yet recorded (plan 18.1.7). Historical: Stage 10.3 names commit
`69c5f17`; the other stages name their PRs (#72 to #76).

### Environment and fixture

At closure: both local lanes, from their run records. macOS: out of scope
(`platforms-windows-wsl`). Historical: one Windows machine. Fixtures: SQLite stores in
`t.TempDir()`, the test kit's answerer, a 2 MB result.

### Exact tests and commands

At closure: the lane scripts, and `go -C src/harness test -race -count=1 ./internal/agent/
./internal/human/... ./internal/tools/... ./internal/storage/spill/...`. Historical: the
command under "Tests (TC-10a, TC-10b)".

### Run identifiers

At closure: the run-record ids of both lanes, in the runbook's template (plan 18.1.3).
WSL lane: not run. Windows lane: not run. macOS: out of scope (`platforms-windows-wsl`).
Historical: no run id was recorded. DH-E-TOOLS-3 and DH-E-HUMAN-6 run in later stages
and have no run id yet.

### Results

At closure: not yet recorded. Historical: the test table under "Tests (TC-10a, TC-10b)".

### Manual checks

impl.md DH-S-10 names three checks: call a test tool with arguments that break its
schema and record that the body never ran (step 10.4); request an approval with no
answerer and record the denial (step 10.3); give a grant, end the session, resume it and
record that the grant is gone (step 10.10). Under the default of M1-D-CLOSE-2 each is run
as the scripted test that composes the real pipeline, watched by a named person; an
interactive run through a launcher waits for DH-S-11 and DH-S-21. The other steps take
the rule-3 default. Rows 10.3, 10.4 and 10.6 run only after plan Stages 16.1 to 16.3 have
landed (plan 16.6.1). Historical record: "Manual checks (impl.md DH-S-10)". How to run and
fill the register: [the evidence runbook](README.md#manual-checks). Composed tests run as
`go -C src/harness test -count=1 -v -run '<names>' <package>`.

#### Manual checks register

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 10.1 | rule-3 default: a tool is declared once and registered in a scope | composed test: `./internal/tools/` (the declaration and registry tests) | one lane | | | | `manual` |
| 10.2 | rule-3 default: the system prompt and tool list are assembled deterministically | composed test: `./internal/tools/prompt/` | one lane | | | | `manual` |
| 10.3 | an approval with no answerer is denied, with an audit record | composed test: `TestNoAnswererMeansNoThroughTheExecutor` in `./internal/tools/core/`; record the outcome and the approval code | one lane | | | | `manual` |
| 10.4 | arguments that break the schema: the body never runs | composed test: `TestSchemaBreakingArgumentsNeverReachTheBody`, `TestDenialEnforcedInTheExecutor` in `./internal/tools/core/`; record the validation code and the body counter (0) | one lane | | | | `manual` |
| 10.5 | rule-3 default: enforced deadlines that never hold a turn | composed test: `TestDeadlineDoesNotHoldTheTurn`, `TestDeadlineGraceEndsAtExpiryPlusGrace` in `./internal/tools/core/` | one lane | | | | `manual` |
| 10.6 | rule-3 default: no result larger than the budget reaches a model | composed test: `TestOversizedResultSpilledBeforeReachingTheModel` in `./internal/tools/core/`, `TestPolicyNeverOverBudget` in `./internal/storage/spill/policy/` | one lane | | | | `manual` |
| 10.7 | rule-3 default: input is durable at once and runs at most once | composed test: `TestInboxSendIsDurableBeforeItReturns`, `TestClaimedItemRunsAtMostOnceAcrossACrash` in `./internal/agent/` | one lane | | | | `manual` |
| 10.8 | rule-3 default: the model asks the user a question, waits or moves on | composed test: `TestQuestionAnsweredThroughTheInbox`, `TestTimedQuestionReturnsPendingAndTheLateAnswerIsOneUserMessage` in `./internal/human/question/` | one lane | | | | `manual` |
| 10.9 | rule-3 default: a person runs a command against an agent from any client | composed test: `TestCommandRunsAttributedAndDeliversThroughTheInbox` in `./internal/human/command/` | one lane | | | | `manual` |
| 10.10 | give a grant, end the session, resume it: the grant is gone | composed test: `TestGrantDoesNotSurviveResume` in `./internal/human/approval/`; record the empty grant list, the `grant/ended{session-ended}` event and that the call asks again | one lane | | | | `manual` |

### Cleanup evidence

At closure: every temporary store is gone; `git status --porcelain` empty after the run.
Historical: none recorded.

### Open items

At closure: plan 18.1.11 lists them. Now open: the known-false ticks of impl.md 10.3, 10.4
and 10.6 (plan 16.6.1) and their gaps M1-G-PIPE-1 to -8 (plan Stages 16.1 to 16.3); the
items under "Known limits"; DH-E-TOOLS-3 (DH-S-39) and DH-E-HUMAN-6 (DH-S-22).
