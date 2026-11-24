# DH-S-10 evidence

Decisions that govern M1 records, this one included, are logged in [decisions.md](../decisions.md).

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The first closure of the stage (the "DH-S-10 was closed" paragraph below) is kept as
history. It was **reopened by M1-G-PIPE-1..8**: impl.md 10.3, 10.4 and 10.6 had been
ticked without the approval policy, the resume closer, the guarded-pipeline cases, grant
subjects, the spill reader and images in the budget. Plan Phase 16 built those (Stages
16.1 to 16.3) and **re-closed the stage at `f72f435`**, where both local lanes pass all
12 gates: see [Gap closure (M1-G-PIPE-1 to -8)](#gap-closure-m1-g-pipe-1-to-8) and
[Closure](#closure). Under M1-D-CLOSE-1 every tick is still re-verified at the M1
closure commit. The fields of impl.md sec. 7.3 and the per-step manual-check register
are at the end of this file, in "Evidence record (impl.md sec. 7.3)"; how to fill them
is in [the evidence runbook](README.md).

DH-S-10 is the tool pipeline, approvals and human commands. Stage 10.1 (#72/#73)
built impl.md steps 10.1 and 10.2, Stage 10.2 (#74/#75) built 10.3 and 10.4, and
Stage 10.3 (#76, `69c5f17`, merged into this branch) built 10.5 and 10.6 and
authored `DH-E-TOOLS-3`. Stage 10.4 built steps 10.7 to 10.10 and authored
`DH-E-HUMAN-6`.

**DH-S-10 was closed (history; reopened by M1-G-PIPE-1..8, re-closed at `f72f435`):**
impl.md steps 10.1 to 10.10 were ticked as built, and the combined branch passed the
tests below. The three manual checks below are scripted by tests here; their
interactive runs are for the operator. The tables in "What was built" and "Tests" are
that first closure's record: some outcome names in them (`no-answerer`) are the
pre-M1-D-PIPE-1 vocabulary, which version 1 records still decode to (see the outcome
table under Stage 16.1 below).

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

- The inbox lives in `internal/agent`, because `internal/agent/driver` did not exist
  when it was built. The driver (DH-S-11) calls `ClaimTurn` and `ClaimStep` at its
  boundaries.

The earlier limit "the guarded pipeline does not pass an approval `Subject`" is removed:
plan step 16.2.2 adds `tools.Subject{Kind, Name, Label}` and `AskFor`, and the pipeline
maps it to `approval.Subject` (see the Stage 16.2 table below).

<a id="gap-closure-m1-g-pipe-1-to-8"></a>
## Gap closure (M1-G-PIPE-1 to -8)

Plan Stages 16.1 to 16.3 of [the gap-closure plan](../gap-closure-plan.md#phase-16-tools-retry-and-signed-in-routes)
build what the first closure ticked without building: the approval service to its spec
(16.1), the guarded pipeline's missing cases and grant subjects (16.2), and the spill
reader and images in the result budget (16.3). Commits: `f4e1b3d` and `cbfed95` (16.1),
`9ddb313` with the `Subject.Label` part of `7976094` (16.2), `959eef5` (16.3). Owner
decision M1-D-PIPE-1 A governs 16.1; the lead settlements
`approval-subject-label-optional`, `approval-audit-failure-is-unavailable`,
`consent-never-uses-grants` and `rule-6-additions-phase-16` are in
[decisions.md](../decisions.md) sec. 4.

**Every test named below passed in both gate runs at `f72f435`** (the record of those runs
is in [DH-S-1.md, "Phase 16 exit gate runs"](DH-S-1.md#phase-16-exit-gate-runs)). This is
local evidence: in-process and SQLite tests on both lanes. It proves the stated contracts
for that build; it is not a lab or real-model run, and none of the stage's `manual` checks
or authored scenarios ran.

### Stage 16.1: the approval service to its spec

**M1-D-PIPE-1 A outcome table.** An approval returns exactly one of five outcomes. Version 2
of `approval/asked` and `approval/decided` carries them; a version 1 record is read into
the same vocabulary (`TestVersion1RecordsUpgrade`).

| Outcome (v2) | When | Tool result code | Version 1 records read as this |
| --- | --- | --- | --- |
| `allowed-once` | a person allowed this call; also "allow for the session" when no grant can be made (detail `grant-refused:<why>`) | none: the call runs | `approved` without a grant |
| `allowed-by-grant` | a session grant in force covers the call's subject; nobody is asked (detail `allowed-by-grant:<id>`, field `grant`) | none: the call runs | `approved` with detail `allowed-by-grant:<id>` |
| `rejected` | a person refused; or the session's policy is `never` (detail `policy-never`, principal `harness`, nobody asked) | `approval/rejected` | `denied`, other than the cases below |
| `cancelled` | the caller's context ended before the decision | `approval/cancelled` | `denied` with detail `cancelled: ...` |
| `unavailable` | nobody could answer: no binding, no answerer, a failed answerer, an unknown answer, a set timeout (detail `timeout`), a host that stopped while the request was open (detail `host-stopped`), or an audit record that could not be written (`audit-failed`, `grant-audit-failed`) | `approval/unavailable` | `timeout` (detail `timeout`), `no-answerer`, and `denied` with `audit-failed`, `grant-audit-failed` or an unexpected answer |

The default timeout is 0: a request waits for its answer (`TestDefaultWaits`).
`approval/decided` v2 names the principal, the channel and the peer the answer came from,
and the grant id when a grant decided it; `approval/asked` v2 names the kind (approval or
consent), the subject and the mode. `internal/agent/testdata/f2_log.golden` was
regenerated for these events (its approval lines 9 to 11 only; Rule 6).

**Tests by `DH-T` ID.**

| Test ID | Plan step | Tests (package) | What they show | Result |
| --- | --- | --- | --- | --- |
| `DH-T-HUMAN-1` | 16.1.1 | `TestActorRoundTrip` (`human`) | `human.Actor` carries `Peer`, the carrier or connection an answer arrived from, and round-trips it | pass |
| `DH-T-HUMAN-3` | 16.1.2, 16.1.3 | `TestAllowedOnce`, `TestAllowSessionIsAllowedOnce`, `TestRejected`, `TestTimeout`, `TestDefaultWaits`, `TestUnavailableMeansNo`, `TestFailedAnswererMeansNo`, `TestUnknownAnswerIsUnavailable`, `TestCancelled`, `TestAuditFailureRefuses`, `TestVersion1RecordsUpgrade` (`human/approval`) | each row of the outcome table above, and the v1 upgrade | pass |
| `DH-T-HUMAN-3` (policy) | 16.1.3 | `TestNeverRefusesWithoutAskingThroughTheExecutor`, `TestNeverRejectsWithoutAsking`, `TestSetPolicyRefusals`, `TestBindRestoresThePolicy` (`human/approval`) | Scenario "never refuses without asking": under policy `never`, a tool that asks, called through the executor, ends `rejected` with code `approval/rejected`; the answerer received 0 items, the grant table was asked 0 times, the body counter is 0, and the audit pair names principal `harness`. Switching the policy ends every grant (`policy-changed`); a resumed session gets its policy back from the `approval/policy` events | pass |
| `DH-T-HUMAN-4` | 16.1.1, 16.1.2, 16.1.4 | `TestAllowedOnce`, `TestRejected`, `TestDecisionNamesItsPeer`, `TestRequestOutsideATurn` (`human/approval`) | Scenario "the decision names its peer": an answerer whose actor has peer `p1` allows a call once, and `approval/decided` v2 names principal, channel and peer `p1`, with no grant id | pass |
| `DH-T-HUMAN-4` (resume closer) | 16.1.4 | `TestOpenPairClosedOnResumeBeforeAnyAppend`, `TestResumeClosersDirectly` (`human/approval`); `TestApprovalHoldsACallOutOfOutcomeUnknown` (`session/surface`, `approval_hold_test.go`) | Scenario "crash during an open approval", on SQLite through the real agent registry: a log that ends with `tool/call` and `approval/asked` and no decision is resumed, and before any other append it gains, in order, `tool/result` coded `approval/unavailable` ("did not run"), `step/end`, `turn/end`, `session/end-seed`, `approval/decided{unavailable, host-stopped}` and `grant/ended{host-stopped}`. The control case, an approval allowed before the crash, still closes as `tools/outcome-unknown`, because the body may have started. The surface checkpoint carries the hold (`CheckpointVersion` 2) | pass |
| `DH-T-HUMAN-5` | 16.1.5 | `TestGrantMatching` (`human/approval`, `grants_test.go`) | tag checked: the grant-matching test keeps `-5` | pass |
| `DH-T-HUMAN-6` | 16.1.5 | `TestGrantDoesNotSurviveResume`, `TestGrantEndsWhenItsAgentIsDisposed`, `TestGrantRevokeChannelAndCap`, `TestGrantCreateFailures`, `TestEndAllForgetsGrantsWhoseEndCannotBeWritten` (`human/approval`) | tag checked: the other five grant tests keep `-6` | pass |
| `DH-T-TOOLS-14` (policy section) | 16.1.3 | `TestPolicySectionGolden` (`human/approval`) | the prompt-context section that tells the model the session's approval policy equals its golden | pass |

**Still open after 16.1.** `approval.Service.Bind`, `Grants.Bind` and the policy's
`RegisterPromptSection` are wired only in tests; the product composition that mounts them
lands at DH-S-16.

### Stage 16.2: the guarded pipeline and grant subjects

`tools.Settled` (a notify key carrying the encoded durable result, sent after `tool/result`
is appended) and `tools.Subject{Kind, Name, Label}` with `AskFor` are added. The contract
package imports no `human/approval` (`DH-T-TOOLS-18`).

**Test table for Stages 10.1 to 10.3, by `DH-T` ID** (impl.md steps 10.1, 10.2, 10.4 to
10.6; the approval rows of step 10.3 are under 16.1 above):

| Test ID | impl.md step | Tests (package) | What they show | Result |
| --- | --- | --- | --- | --- |
| `DH-T-TOOLS-1` | 10.1 | `TestDeclarationSchema` (`tools`), `TestCatalogueAndRefusals` (`tools/core`) | a tool's schema is derived once and is byte-stable; the catalogue lists each tool once and refuses a declaration with an error | pass |
| `DH-T-TOOLS-2` | 10.1 | `TestDuplicateRefusedInScope`, `TestScopedViews` (`tools/core`) | a duplicate name in one scope is refused and the first stays; a child scope's tool shadows only that subtree | pass |
| `DH-T-TOOLS-3` | 10.4 | `TestStageOrderFixed`, `TestUnknownOrUnadmittedToolRefusedBeforeExecution` (`tools/core`) | the stages run resolve, validate, policy, guards, approval, flush, body, shape; an unknown or unadmitted tool ends `tools/not-started` at resolve (retagged by 16.2.3) | pass |
| `DH-T-TOOLS-4` | 10.4 | `TestSchemaBreakingArgumentsNeverReachTheBody`, `TestLoggedArgumentsNeverRewritten`, `TestFinalShapersHaveTheLastWord` (`tools/core`) | arguments that break the schema never reach the body; logged arguments are never rewritten (retagged by 16.2.3) | pass |
| `DH-T-TOOLS-5` | 10.4 | `TestGuardDenialHoldsUnderEveryListenerOrder`, `TestPanickingListenerRefusesTheCallAndTheTurnGoesOn`, `TestAskWithNoAgentIsUnavailable`, `TestNoAnswererMeansNoThroughTheExecutor`, `TestDenialEnforcedInTheExecutor` (`tools/core`) | a guard denial holds under all 24 orders of two policies and two guards, and the answerer is never asked; a panicking policy, guard or answerer refuses the call and the turn goes on; an `Ask` for a call with no agent is `approval/unavailable` with no request made; denial is enforced in the executor, not by hiding the tool | pass |
| `DH-T-TOOLS-6` | 10.4 | `TestConflictingCallsSerializeAndResultsKeepModelOrder`, `TestBatchNeverExceedsMaxParallel`, `TestExclusiveCallsRunAlone`, `TestOversizedArgumentsAreNotClassified`, `TestLogFailureStopsAdmissionWhileEarlierCallsRun`, `TestDetachedBodyKeepsItsResourceKeys` (`tools/core`, `schedule_test.go`) | the batch scheduler (tagged by 16.2.3) | pass |
| `DH-T-TOOLS-7` | 10.4, 10.6 | `TestOutputSchemaViolationIsInvalidOutput`, `TestBlockTurnsSuccessIntoFailureAndNeverTheReverse`, `TestSettledObserverCannotChangeAResult` (`tools/core`, `guarded_test.go`), `TestResultCeiling` (`deadline_test.go`) | an output that breaks its schema is `tools/invalid-output`; a post-run `Block` turns success into failure and leaves a failure a failure; Scenario "a Settled observer cannot change a result" (observers that panic, fail and record, over an approved and a denied call: both `tool/result` events unchanged, the recorder saw two settlements in commit order, the panic logged with the observer's name); the result ceiling (retagged by 16.2.3) | pass |
| `DH-T-TOOLS-8`, `-9` | 10.5 | `TestDeadlineDoesNotHoldTheTurn`, `TestDeadlineCooperativeBodyTimesOut`, `TestDeadlineBudgetPrecedence`, `TestDeadlineDeclaredGraceIsLonger`, `TestConformanceCancellation`, `TestDeadlineGraceEndsAtExpiryPlusGrace`, `TestDeadlineReturnAtExpiryIsTimeout`, `TestDeadlineCancelledBeforeExpiryKeepsCancelCause`, `TestDeadlineReturnBeforeExpiryWithDeadlineSeen` (`tools/core`) | unchanged by Phase 16; listed so the 10.5 row is complete | pass |
| `DH-T-TOOLS-14` | 10.2 | `TestPromptAssemblyIsDeterministic` (`tools`, golden `prompt`), `TestScopedChangesAreLocal` (`tools/prompt`), `TestPolicySectionGolden` (`human/approval`) | five tools in random order over 100 runs assemble to the committed golden; a scoped change is local | pass |
| `DH-T-TOOLS-15` | 10.2 | `TestInterpolation`, `TestCompleteSections`, `TestToolOrder` (`tools/prompt`) | variable interpolation and escapes, complete sections, and `toolOrder` validation | pass |
| `DH-T-TOOLS-18` | 10.1 | `TestContractImportsNoProviderNorApproval` (`tools`, `imports_test.go`) | the tools contract package imports no provider package and no `human/approval` | pass |
| `DH-T-HUMAN-5` (subjects) | 10.10 | `TestSessionGrantMatchesALaterCall`, `TestAskSubjectReachesThePromptOnlyWhenNamed` (`tools/core`, `subject_test.go`) | Scenario "a session grant matches a later call": with directory subject `/w` and an answerer that allows for the session once, a call on `/w/a` creates `grant/created`, a call on `/w/b` is `allowed-by-grant:<id>` with no delivery, and a call on `/wx/c` asks again; also a call with no subject, an application subject, invalid kinds, and the label reaching the answerer | pass |
| `DH-T-STORAGE-11` (pipeline) | 10.6 | `TestImagesReachTheSpillSeatAndAreKeptOrOmittedWhole` (`tools/core`, `images_test.go`) | a result's text and image parts pass the shapers, the spill seat and the ceiling through the real pipeline | pass |

`harness-gate trace-tests --stage DH-S-10` reports DH-S-10 complete: required 25, tagged
25, missing 0 (see "Closure" for what the command reports as a whole).

### Stage 16.3: reading a spilled result, and images in the budget

| Test ID | Plan step | Tests (package) | What they show | Result |
| --- | --- | --- | --- | --- |
| `DH-T-STORAGE-13` | 16.3.1 | `TestReadByOffsetAndLimit`, `TestReadByPattern`, `TestReadRefused`, `TestMountedOnlyWhereTheSpillDirCannotBeMapped` (`storage/spill/tool`), `TestNoticeNamesTheReadTool` (`storage/spill/policy`), `TestModelReadsTheRestOfASpilledResult` (`storage/spill/tool`, `scenario_test.go`) | `spill_read{ref, offset, limit, pattern}` reads a spilled result a slice at a time through the store's session-lineage check, bounds its own output, and is mounted only when no filesystem provider can map the spill directory (a fake provider covers "not mounted"). Scenario "the model reads the rest of a spilled result": a 2 MB result spilled on SQLite, read at offset 1 MiB with limit 32 KiB and then by a pattern; each result is the exact slice, within budget, with no `spill` field | pass |
| `DH-T-STORAGE-11` | 16.3.2 | `TestImagesNeverSplitTheBudget`, `TestImagesPricedByTheRoute`, `TestImagesWithAFailingStore` (`storage/spill/policy`, `images_test.go`) | Scenario "an image never splits the budget": text plus two images priced over the budget, shaped with no route calculator, is within budget, each image is kept or omitted whole, and the saved text has an image line at each image's position; a route calculator prices the images, and one that gives no price, a negative one or panics falls back to the fixed price; with a failing store the result is still within budget, has no reference, says it was not saved and how many images were omitted, and leaves no file | pass |
| `DH-T-STORAGE-12` | 16.3.2 | `TestImageFallbackRefused`, `TestImagesNeverOverBudget` (`storage/spill/policy`) | a negative image fallback price is refused at load; no mix of texts and images, priced by any route, gives a result over the budget, and the saved file holds every text and an image line for every image | pass |

**Still open after 16.3.** `spill_read` and the spill policy are mounted only in tests;
the product composition mounts them at DH-S-16. The "unmappable image path" case of
`DH-T-STORAGE-11` needs an image path mapper, which does not exist yet.

## Closure

**Re-closed by plan step 16.6.3 at `f72f435`** (both local lanes pass all 12 gates;
[DH-S-1.md, "Phase 16 exit gate runs"](DH-S-1.md#phase-16-exit-gate-runs)). This is the
stage's closure as *built*; "done" is claimed only at M1 closure (plan 18.1.12), and the
`Closure status` line at the top stays `open` until plan 18.1.14.

- **Ticks.** Plan step 16.6.1 unticked impl.md 10.3, 10.4 and 10.6 and the story plan's
  "Close DH-S-10" (known-false ticks: gaps M1-G-PIPE-1 to -8) and re-ticked them in the
  same change, because Stages 16.1 to 16.3 pass at `f72f435`. The net result is all ten
  impl.md steps 10.1 to 10.10 ticked, with a pointer line under impl.md DH-S-10 naming the
  reopening and the evidence.
- **Tests.** Every `DH-T` ID that impl.md steps 10.1 to 10.10 name tags at least one
  test (`trace-tests --stage DH-S-10`: required 25, tagged 25, missing 0), and every
  tagged test passed in both gate runs.
- **Manual checks.** Not yet run at this commit: the register below is filled at the M1
  closure commit (plan 18.1.5). Rows 10.3, 10.4 and 10.6 can now run, because Stages 16.1
  to 16.3 have landed.
- **Scenarios.** `DH-E-TOOLS-3` (DH-S-39) and `DH-E-HUMAN-6` (DH-S-22) are authored and
  not run.

**The tool defaults of tech-spec sec. 5.5.** Plan step 16.6.3 asks for "the four tool
defaults"; the sec. 5.5 "Tool registry" row lists six values, so all six are recorded.
The built values equal the row (`tools/core`: `DefaultMaxParallel`, `DefaultTimeout`,
`DefaultGrace`, `DefaultMaxArgumentBytes`, `DefaultMaxResultBytes`, `Config.ForceExclusive`),
and each is a `core.Config` field with a default, not a persisted value (DC-4, R-09).

| Setting | Value | Status |
| --- | --- | --- |
| `maxParallel` (the one overlap cap) | 10 | open: unconfirmed by the owner |
| `defaultTimeout` | 5 min | open: unconfirmed by the owner |
| `grace` | 5 s | open: unconfirmed by the owner |
| `maxArgumentBytes` | 1 MiB | open: unconfirmed by the owner |
| `maxResultBytes` | 256 KiB | open: unconfirmed by the owner |
| `forceExclusive` | empty | open: unconfirmed by the owner |

No row of [decisions.md](../decisions.md) confirms them. Tech-spec DC-4 asked for them to
be confirmed before DH-S-10 started; that did not happen, and it stays an open item for
the owner (listed under "Open items").

**`built-through` stays at `DH-S-8`.** `CheckBuiltThrough` requires the marker to equal
the largest n for which every impl.md step of DH-S-1 to DH-S-n is ticked. DH-S-9's steps
9.1 to 9.3 stay unticked until its manual check (plan 16.5.9) is recorded and its
re-authored scenarios are reviewed ([DH-S-9.md, "Closure"](DH-S-9.md#closure)), so the
largest such n is 8. Raising the marker to DH-S-10 would fail the check
(`TestAuthoredScenariosAreIndexedAndCompileButDoNotRun` runs it). The marker reaches
DH-S-10 when DH-S-9 closes; the same change then raises it past both stages.

## Evidence record (impl.md sec. 7.3)

These are the eight fields of [impl.md sec. 7.3](../../../harness/impl.md#73-the-evidence-record),
in the format of [the evidence runbook](README.md#the-evidence-record-headings). Plan step
18.1.7 fills them at the frozen M1 closure commit; plan step 16.6.3 rewrites the closure
before that. Until then each field points to the history above, which stays exactly as
it was recorded and is not closure evidence (M1-D-CLOSE-1).

### Source and build identity

**At the closure commit (plan 18.1.7).** Commit `593ceaa3efd55d41bf0eeaced5ba7e26acc71966`: one commit for the three modules, on branch `m1-phase-18` over `main` 896c760. Go `go1.27.4` (the go floor). The WSL rehearsal built six `dsh` binaries, each with `vcs.revision=593ceaa…` and `vcs.modified=false`; their SHA-256 values are in [wsl-593ceaa-20261123T2122Z-rehearse.txt](runs/wsl-593ceaa-20261123T2122Z-rehearse.txt).

At closure: not yet recorded (plan 18.1.7). Phase 16 re-closure: commit
`f72f43585c043c9ce0bc8dfe9f4fc05e64d11477`, clean tree, both lanes. Historical: Stage 10.3
names commit `69c5f17`; the other stages name their PRs (#72 to #76).

### Environment and fixture

**At the closure commit (plan 18.1.7).** Windows 11 Enterprise 10.0.28000 native, with `CC` MSYS2 gcc. WSL Ubuntu 24.04.5 LTS, kernel 6.18.58.1-microsoft-standard-WSL2, ext4 clone, gcc 13.3.0. macOS is out of this story (`macos-return-stage`). The manual checks ran on the same workstation (decision `manual-checks-run-by-lead`). Signed-in clients: Claude Code 2.1.312 and GitHub Copilot CLI 1.0.115-7, Windows only. Fixtures: those named by this stage's register rows.

At closure: both local lanes, from their run records. macOS: out of scope
(`platforms-windows-wsl`). Historical: one Windows machine. Fixtures: SQLite stores in
`t.TempDir()`, the test kit's answerer, a 2 MB result.

### Exact tests and commands

**At the closure commit (plan 18.1.7).** `tools/harness-gate/lanes/win.ps1` and `tools/harness-gate/lanes/wsl.sh` (the gate with `--evidence`; `rehearse` in WSL). `DSH_REQUIRE_SCENARIOS` is unset. For the register: `evidence/runs/manual-593ceaa/run-register.sh` (Windows, and `--os wsl`), with the by-hand steps of `by-hand-checklist.md`; the per-row commands are in this record's register.

At closure: the lane scripts, and `go -C src/harness test -race -count=1 ./internal/agent/
./internal/human/... ./internal/tools/... ./internal/storage/spill/...`. Historical: the
command under "Tests (TC-10a, TC-10b)".

### Run identifiers

**At the closure commit (plan 18.1.7).** Windows lane `win-593ceaa-20261123T2122Z` ([record](runs/win-593ceaa-20261123T2122Z.txt)) and WSL lane `wsl-593ceaa-20261123T2122Z` ([record](runs/wsl-593ceaa-20261123T2122Z.txt), [rehearse](runs/wsl-593ceaa-20261123T2122Z-rehearse.txt)). Register logs: `register-windows-593ceaa-20261123T2213Z` and `register-wsl-593ceaa-20261123T2237Z` in [runs/manual-593ceaa/](runs/manual-593ceaa/byhand-results.md). The WSL rehearse record was rebuilt from the lane's tee'd output after the original file was deleted by mistake; the gate record's own `sha256:` line verifies it byte for byte.

At closure: the run-record ids of both lanes, in the runbook's template (plan 18.1.3).
WSL lane: not run at the M1 closure commit yet. Windows lane: not run at the M1 closure
commit yet. macOS: out of scope (`platforms-windows-wsl`). Phase 16 re-closure (not the M1
closure commit): `wsl-f72f435-20261123T1041Z` and `win-f72f435-20261123T1041Z`, both
`PASSED all 12 gates`; the full template is in
[DH-S-1.md, "Phase 16 exit gate runs"](DH-S-1.md#phase-16-exit-gate-runs). Historical: no
run id was recorded before Phase 16. DH-E-TOOLS-3 and DH-E-HUMAN-6 run in later stages
and have no run id yet.

### Results

**At the closure commit (plan 18.1.7).** Both lanes `PASSED all 12 gates`, with `trace-tests` in the `trace` gate. `trace-tests` for DH-S-1 to DH-S-11 reports required 310, tagged 288, deferred 22, missing 0. This stage's register: see "Manual checks register" (every row PASS or recorded as seen; not run and pending rows are open, never a pass).

At closure: not yet recorded. Phase 16: both lanes `PASSED all 12 gates` at `f72f435`; per
test ID, the tables of "Gap closure (M1-G-PIPE-1 to -8)". Historical: the test table under
"Tests (TC-10a, TC-10b)".

### Manual checks

impl.md DH-S-10 names three checks: call a test tool with arguments that break its
schema and record that the body never ran (step 10.4); request an approval with no
answerer and record the denial (step 10.3); give a grant, end the session, resume it and
record that the grant is gone (step 10.10). Under the default of M1-D-CLOSE-2 each is run
as the scripted test that composes the real pipeline, watched by a named person; an
interactive run through a launcher waits for DH-S-11 and DH-S-21. The other steps take
the rule-3 default. Rows 10.3, 10.4 and 10.6 could run only after plan Stages 16.1 to 16.3
had landed (plan 16.6.1); they have landed at `f72f435`, and no row has been run yet (the
register is filled at the M1 closure commit, plan 18.1.5). Historical record: "Manual checks (impl.md DH-S-10)". How to run and
fill the register: [the evidence runbook](README.md#manual-checks). Composed tests run as
`go -C src/harness test -count=1 -v -run '<names>' <package>`.

#### Manual checks register

**Run at the M1 closure commit (plan 18.1.5, 2026-11-23).** The lead Claude Code session ran every row below at `593ceaa3efd55d41bf0eeaced5ba7e26acc71966`, at the owner's instruction of 2026-11-23 ("you should do that"); the owner did not watch these runs. This departs from M1-D-CLOSE-2 A (a named person runs it at the recorded commit, watches it and records it `manual`) and from the runbook rule that signed-in checks are never run from an agent session, and it needs to be recorded as an owner decision. Logs: `register-windows-593ceaa-20261123T2213Z.log`, `register-wsl-593ceaa-20261123T2237Z.log`, `byhand-windows-593ceaa.log`, `byhand-windows-593ceaa-part2.log`, `byhand-wsl-593ceaa.log`, `signedin-dhs9-593ceaa.log`, `signedin-m1-claude-593ceaa.log` and `signedin-m1-copilot-593ceaa.log`. The WSL rehearse record (`wsl-593ceaa-20261123T2122Z-rehearse.txt`) was rebuilt from the lane's tee'd output after the original file was deleted by mistake.

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 10.1 | rule-3 default: a tool is declared once and registered in a scope | composed test: `./internal/tools/` (the declaration and registry tests) | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): 10 tests `--- PASS`, 0 FAIL, among them `TestDeclarationSchema`, `TestDynamicDeclaration`, `TestOverlap`, `TestSettlementIsImmutable`; `internal/tools` ok. | `manual` |
| 10.2 | rule-3 default: the system prompt and tool list are assembled deterministically | composed test: `./internal/tools/prompt/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestInterpolation`, `TestCompleteSections`, `TestScopedChangesAreLocal`, `TestToolOrder`, `TestLivePersona`, `TestToolOrderIsCopied`, `TestAssemblyUsesOneConfigSnapshot`. | `manual` |
| 10.3 | an approval with no answerer is denied, with an audit record | composed test: `TestNoAnswererMeansNoThroughTheExecutor` in `./internal/tools/core/`; record the outcome and the approval code | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestNoAnswererMeansNoThroughTheExecutor`; no outcome or approval code was printed under `-v`; the denial and audit assertions passed. | `manual` |
| 10.4 | arguments that break the schema: the body never runs | composed test: `TestSchemaBreakingArgumentsNeverReachTheBody`, `TestDenialEnforcedInTheExecutor` in `./internal/tools/core/`; record the validation code and the body counter (0) | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestSchemaBreakingArgumentsNeverReachTheBody`, `TestDenialEnforcedInTheExecutor`; no validation code or body counter was printed under `-v`; the body-never-runs assertions passed. | `manual` |
| 10.5 | rule-3 default: enforced deadlines that never hold a turn | composed test: `TestDeadlineDoesNotHoldTheTurn`, `TestDeadlineGraceEndsAtExpiryPlusGrace` in `./internal/tools/core/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestDeadlineDoesNotHoldTheTurn`, `TestDeadlineGraceEndsAtExpiryPlusGrace`. | `manual` |
| 10.6 | rule-3 default: no result larger than the budget reaches a model | composed test: `TestOversizedResultSpilledBeforeReachingTheModel` in `./internal/tools/core/`, `TestPolicyNeverOverBudget` in `./internal/storage/spill/policy/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestOversizedResultSpilledBeforeReachingTheModel`, `TestPolicyNeverOverBudget`. | `manual` |
| 10.7 | rule-3 default: input is durable at once and runs at most once | composed test: `TestInboxSendIsDurableBeforeItReturns`, `TestClaimedItemRunsAtMostOnceAcrossACrash` in `./internal/agent/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestInboxSendIsDurableBeforeItReturns`, `TestClaimedItemRunsAtMostOnceAcrossACrash`. | `manual` |
| 10.8 | rule-3 default: the model asks the user a question, waits or moves on | composed test: `TestQuestionAnsweredThroughTheInbox`, `TestTimedQuestionReturnsPendingAndTheLateAnswerIsOneUserMessage` in `./internal/human/question/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestQuestionAnsweredThroughTheInbox`, `TestTimedQuestionReturnsPendingAndTheLateAnswerIsOneUserMessage`. | `manual` |
| 10.9 | rule-3 default: a person runs a command against an agent from any client | composed test: `TestCommandRunsAttributedAndDeliversThroughTheInbox` in `./internal/human/command/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestCommandRunsAttributedAndDeliversThroughTheInbox`. | `manual` |
| 10.10 | give a grant, end the session, resume it: the grant is gone | composed test: `TestGrantDoesNotSurviveResume` in `./internal/human/approval/`; record the empty grant list, the `grant/ended{session-ended}` event and that the call asks again | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestGrantDoesNotSurviveResume`; no grant list or event was printed under `-v`; the empty-grants, `grant/ended{session-ended}` and asks-again assertions passed. | `manual` |

### Cleanup evidence

**At the closure commit (plan 18.1.7).** Every throwaway worktree of the manual checks was removed, and `git status --porcelain` of the main tree was empty before and after. The signed-in runs left no `claude` or `copilot` process (PID sets equal before and after). The WSL clone's tags (7, all created 2026-11-17) were unchanged, and nothing was published.

At closure: every temporary store is gone; `git status --porcelain` empty after the run.
Historical: none recorded.

### Open items

**At the closure commit (plan 18.1.7).** For this stage: none of its own. The M1-wide list is [DH-S-11 "Open items at M1"](DH-S-11.md#open-items-at-m1-plan-18111).

At closure: plan 18.1.11 lists them. Closed by Phase 16: the known-false ticks of impl.md
10.3, 10.4 and 10.6 and their gaps M1-G-PIPE-1 to -8 (re-ticked at `f72f435`, plan
16.6.1). Now open:
- the six tool defaults of tech-spec sec. 5.5, unconfirmed by the owner ("Closure");
- production composition: `approval.Service.Bind`, `Grants.Bind`, the policy's
  `RegisterPromptSection`, `spill_read` and the spill policy are mounted only in tests,
  until DH-S-16;
- the "unmappable image path" case of `DH-T-STORAGE-11`, which needs an image path mapper;
- the manual-check register (plan 18.1.5);
- `built-through`, held at DH-S-8 until DH-S-9 closes;
- the items under "Known limits"; DH-E-TOOLS-3 (DH-S-39) and DH-E-HUMAN-6 (DH-S-22).
