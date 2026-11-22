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
| 10.7 DH-D-AGENT-2 | `internal/agent` (`inbox.go`) | `Inbox` over the session log. `Send` dedups on the message id, appends `agent/inbox/spliced` and flushes before it returns, so the item is durable at once (SP-5). A second send with the same id is refused with `agent/duplicate-message` and the first receipt. Next-step items are taken only by `ClaimStep`, at a step boundary. Next-turn items are taken by `ClaimTurn`, at a turn boundary. The claim is one group: `turn/start`, the `claimed` splice and the `user/message`. `OpenInbox` folds the log, so a claimed item is never run again after a crash or resume. `Discard` removes an item with a cause. |
| 10.8 DH-D-HUMAN-5 | `internal/human/question`, `question/tool` | `Service.Ask` logs `question/asked` and asks the agent's bound answerer. With `wait`, it blocks until the answer or the deadline and returns `pending` at the deadline. Without `wait`, it returns `pending` at once. `Reply` sends the answer through the inbox as a next-step user message naming the call (source `question-answer`). If the call is still waiting, the message is discarded with cause `returned-to-call` and the call returns the answer. It then logs `question/answered`, attributed with principal and channel. A second reply is refused with `question/reply-queued`. `Restore` reopens unanswered questions after a restart. The `ask_user_question` tool declares the call. |
| 10.9 DH-D-HUMAN-6 | `internal/human/command` | A scoped `Registry` (nearest scope wins; `List` merges scopes). `Parse` and `Execute` run a `/name args` line from any client against an agent, with no model turn. An unknown name, invalid typed arguments or an owner-only command from a non-owner is refused and logs nothing. Otherwise `command/run` and `command/done` (ok, failed or aborted) are paired and attributed. A handler that ignores its deadline settles `aborted`, and a panic settles `failed`. `Call.Send` delivers what the command hands the agent through the inbox, attributed to the person (source `command`). |
| 10.10 DH-D-HUMAN-3 | `internal/human/approval` (`grants.go`, `approval.go`) | Session grants of one directory (canonical path, symlinks resolved, separator-bounded prefix) or one application (provider and app id; a PID is refused). They are listable and revocable, and capped at 32 per session. They are logged as `grant/created` and `grant/ended`. They are held per agent incarnation, so a resume, fork or subagent starts with none (NG-8). `CloseGrantsOnResume` ends grants that were left open. Only an owner channel (ui, cli or tui) can create one. `approval.Service.Request` approves a call whose `Subject` a grant covers (detail `allowed-by-grant:<id>`), and turns an `allowed-session` answer with a subject into a grant. |

## Tests (TC-10a, TC-10b)

Run with `go -C src/harness test -race -count=1 ./internal/agent/ ./internal/human/... ./internal/tools/...`:

| Scenario (implementation-plan Stage 10.4) | Test | Result |
| --- | --- | --- |
| Inbox item durable before Send returns (TC-10b) | `agent` `TestInboxSendIsDurableBeforeItReturns` | pass. Against the SQLite store in `t.TempDir()`, a second connection reads the inbox row as soon as `Send` returns. |
| One accepted message is at most one turn (`DH-T-AGENT-6`, TC-10a) | `agent` `TestClaimedItemRunsAtMostOnceAcrossACrash` | pass. After a claim, the stub crashes and the session is resumed. The item is not pending, the log has one `turn/start` for it, and a resend is refused with the first receipt. |
| Idle injection and follow-ups (`DH-T-AGENT-4`, `-5`) | `agent` `TestIdleInjectionOpensNoTurnAndIsClaimedAtTheNextStep`, `TestQueuedFollowUpsAreOneTurnEach`, `TestConcurrentSendsAreOrderedAndUnique` | pass. A next-step item opens no turn. Two follow-ups make two turns, and a discarded item makes none. Concurrent sends are ordered and unique, and the fold equals the live list. |
| Grant does not survive resume (`DH-D-HUMAN-3`) | `approval` `TestGrantDoesNotSurviveResume`, `TestGrantMatching`, `TestGrantRevokeChannelAndCap` | pass. Granting `/w` approves a later call on `/w/x` without asking. After the session ends and is resumed, the grant list is empty and the same call asks again. A sibling prefix or a link out of the root is not covered. Revoke works, an sdk channel cannot grant, and the cap holds. |
| Question answered through the inbox (TC-10a) | `question` `TestQuestionAnsweredThroughTheInbox`, `TestTimedQuestionReturnsPendingAndTheLateAnswerIsOneUserMessage`, `TestNoBindingAndMovingOn` | pass. With `wait`, the fake answerer's reply enters the inbox and the waiting call returns it. `question/asked` and `question/answered` are logged with attribution. A timed question returns `pending`. After a restart, a late answer is one user message naming the call, and a second answer is refused. |
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
