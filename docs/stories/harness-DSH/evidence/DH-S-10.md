# DH-S-10 evidence

DH-S-10 is the tool pipeline, approvals and human commands. Stage 10.1 (#72/#73)
built impl.md steps 10.1 and 10.2, and Stage 10.2 (#74/#75) built 10.3 and 10.4.
Stage 10.4 built steps 10.7 to 10.10 and authored `DH-E-HUMAN-6`.

**DH-S-10 is not closed yet.** Steps 10.5 (DH-D-TOOLS-3, deadlines) and 10.6
(DH-D-STORAGE-6, the result budget) belong to Stage 10.3, which has not landed on
this branch, so those two stay unticked in impl.md. The three manual checks below
are scripted by tests here; their interactive runs are for the operator.

Machine: Windows, go toolchain per `src/harness/go.mod` (floor `go 1.26.0`).

## What was built

| Step | Package | What it does |
| --- | --- | --- |
| 10.7 DH-D-AGENT-2 | `internal/agent` (`inbox.go`) | `Inbox` over the session log. `Send` dedups on the message id, appends `agent/inbox/spliced` and flushes before it returns, so the item is durable at once (SP-5). A second send with the same id is refused with `agent/duplicate-message` and the first receipt. Next-step items are taken only by `ClaimStep`, at a step boundary. Next-turn items are taken by `ClaimTurn`, at a turn boundary. The claim is one group: `turn/start`, the `claimed` splice and the `user/message`. `OpenInbox` folds the log, so a claimed item is never run again after a crash or resume. `Discard` removes an item with a cause. `SendWith` and `Hand` write an admission and the producer's own record (an answer's `question/answered`) as one group, so no crash separates them. `Hand` admits and discards in one group, for an answer a waiting call takes. A group is folded once appended. If its flush then fails, the inbox is marked not durable and a next-turn admission does not wake the agent yet. `Sync` (or any later successful flush) completes the durability and then pays the owed wake. It never appends, so a resend is still a duplicate. |
| 10.8 DH-D-HUMAN-5 | `internal/human/question`, `question/tool` | `Service.Ask` logs `question/asked` and asks the agent's bound answerer. With `wait`, it blocks until the answer or the deadline and returns `pending` at the deadline. Without `wait`, it returns `pending` at once. `Reply` settles the question in one durable group: the answer's inbox admission (a user message naming the call, source `question-answer`, id `ans-<question>`) and `question/answered`, attributed with principal and channel. If the call is still waiting, the group also discards the message with cause `returned-to-call`, the call is released at once, and the registry item is withdrawn. Otherwise the answer is queued: next-step while a turn is open, or next-turn when the agent is idle, which wakes the agent and starts a turn. A failed append admits nothing, is returned (to the waiting call too, as `pending` with the error), and leaves the question open for a retry. A failed flush after the append leaves the group folded, and that group is the settlement: the waiting call gets the answer (the executor's own flush makes the group durable before the tool result), and the replier gets the error. A retried `Reply` runs `Inbox.Sync` and returns the first receipt, which makes the answer durable and wakes the agent without admitting it twice. A background settlement that fails (a registry answer or close) is reported through `Config.OnError`, else the `Config.Owner` logger, else `slog`. `Service.Close` withdraws the running interactions and waits for them. A second reply is refused with `question/reply-queued`. `Restore` reopens unanswered questions after a restart. It treats an admitted `ans-<question>` as settled, and delivers again once (next-turn) an answer handed to a call that never showed it to the model: one with no tool result, or only a closer or an error result. The `ask_user_question` tool declares the call. |
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
| Question answered through the inbox (TC-10a) | `question` `TestQuestionAnsweredThroughTheInbox`, `TestTimedQuestionReturnsPendingAndTheLateAnswerIsOneUserMessage`, `TestNoBindingAndMovingOn`, `TestReplyReleasesAWaitingAsk`, `TestLateAnswerIntoAnOpenTurnIsNextStep`, `TestFailedSettlementLeavesTheQuestionOpen`, `TestFailedCloseIsReported`, `TestRestoreClosesAQuestionWhoseAnswerWasAdmitted`, `TestRestoreRedeliversAnAnswerTheCrashTookFromItsCall`, `TestFailedBackgroundSettlementIsLoggedByDefault`, `TestRetryAfterAFailedFlushCompletesTheAnswer`; `question/tool` `TestHandedAnswerUnderAFailedFlushReachesTheModelOnce`, `TestAnswerOfAFailedCallIsDeliveredAgainOnResume` (through the `tools/core` executor) | pass. With `wait`, the fake answerer's reply enters the inbox and the waiting call returns it. `question/asked` and `question/answered` are logged with attribution. A timed question returns `pending`. After a restart, a late answer is one user message naming the call. It wakes the idle agent and is claimed by `ClaimTurn`, with no turn opened by hand. A second answer is refused. An external `Reply` releases a waiting call at once and withdraws the registry item. Injected write failures admit nothing and leave the question open, and a retry succeeds. After a restart, an admitted answer is not reopened, and an answer the crash took from its call is redelivered once. With no `OnError`, a failed background settlement is logged on the owner's logger. After a failed flush, the idle agent is not woken. The retry returns the same receipt and wakes it, and nothing is admitted twice. Through the executor, an answer handed over under a failed flush reaches the model and is not redelivered on resume. A call that fails after the hand-over has an error result, so its answer is redelivered once. |
| Human commands (DH-T-HUMAN-9) | `command` `TestCommandRunsAttributedAndDeliversThroughTheInbox`, `TestCommandRegistryAndExecuteRules` | pass. Commands from the cli, ui and sdk clients each log run and done with attribution and deliver one command-sourced inbox item. No turn opens. Refused lines (unknown, not a command, invalid arguments, owner-only) log nothing. A scoped command shadows a global one, and a stuck handler settles aborted. |
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
| `DH-E-HUMAN-6` | `src/harness/internal/testkit/scenarios/human_question_scenario_test.go` | DH-S-22 (requires DH-S-11, DH-S-16, DH-S-21); indexed in `test/harness/acceptance-index.yaml`. It is compiled by `go vet -tags scenario ./...` and never run by the gate. |

## Known limits

- The guarded pipeline (`internal/tools/core`) does not pass an approval `Subject`
  yet, so a grant takes effect only for callers that set `approval.Request.Subject`.
  Wiring the subject from tool calls follows with the sandbox (DH-D-SANDBOX-1).
- The inbox lives in `internal/agent`, because `internal/agent/driver` does not exist
  yet. The driver (DH-S-11) calls `ClaimTurn` and `ClaimStep` at its boundaries.

## Closure

Remaining: Stage 10.3 (impl.md steps 10.5 and 10.6), then tick those and set
`built-through` once the review of this PR accepts the scenario.
