# DH-S-11 evidence

DH-S-11 is the agent loop, milestone M1. Stage 11.1 built impl.md steps 11.1 and
11.2, Stage 11.2 built 11.3 and 11.4 with the no-unanswered-call property test, and
Stage 11.3 built 11.5 and 11.6. Stage 11.4, this one, builds step 11.7, adds the
flow F2 golden and its SQLite twin (TC-11a, TC-11b), authors the remaining
`DH-E-*` scenarios of DH-S-1 to DH-S-11, and scripts the M1 manual check.

**DH-S-11 is not closed yet**: the M1 manual check below has not run with a real key,
so it is not recorded. Until it is, impl.md steps 11.3, 11.4 and 11.7 stay unticked
(11.1, 11.2, 11.5 and 11.6 were ticked by their stages), the plan's "Close DH-S-11
and the story" step stays unticked, and `built-through` stays at DH-S-7. See
"Closure".

Machine: Windows, go toolchain per `src/harness/go.mod` (floor `go 1.26.0`).

## What was built (Stage 11.4)

| Step | Package | What it does |
| --- | --- | --- |
| 11.7 DH-D-CONTEXT-1 | `internal/context/request` | `Pipeline` is the context area's one `agent.PreStep` listener. It calls downstream first; when the step is entered, it runs its contributors against a read-only view of the agent's surface in the fixed order of one table (`Order()`), whatever order they registered in, and appends their messages to `Enter.Messages` after the claimed batch. The driver logs every entered message as `user/message` before it builds the request, so each contribution is a logged event whose source kind is the contributor's name, and the request rebuilt from the log at its `requestAsOf` equals the one sent. A contributor never appends. An unchanged observation adds nothing. A failing or panicking optional contributor is skipped for the step and recorded as the log-only `context/skipped`; a failing required one rejects the step (the turn ends blocked, naming it). A downstream reject enters nothing and runs no contributor. Contributors are scope-layered (`scope.Layers`): `Register` mounts one in its owner's scope (`scope.Of`), so a contributor mounted in a preset serves only that preset's agents (context-compaction-skill.md §6.1) and the global scope's serve every agent; for each step the pipeline resolves the target agent's scope (`Options.Scopes`, `LiveScopes` over the agent registry), and the nearest mount of each kind wins. `Register` keeps the table: an unknown name, or a second mount of one kind in the same scope, is refused; the same kind in two sibling scopes is allowed. The package is named `request` as the plan names it; context-compaction-skill.md §6.1 calls it `context/inject`. |
| RM-8 | `internal/archtest` | Two rules: the directory `internal/context` holds no Go package (nothing shadows the standard `context`), and no package under `internal/context` imports the agent driver or a model provider, wire format or transport. |

The F2 harness (`internal/agent/f2_test.go`) composes the real components: a
`cordis` root with the scope table, the guarded tool pipeline (`tools/core`) with
one echo tool `probe` behind an `Ask` policy, the real approval service and human
registry with the test kit's answerer approving once, a session on the given store,
the inbox, the model selection and the driver. Only the model differs between the
golden (the scripted model) and M1 (the real DeepSeek route).

## Tests (TC-11a, TC-11b)

Run with `go -C src/harness test -race -count=1 ./internal/context/... ./internal/agent/ ./internal/archtest/`:

| Scenario (implementation-plan Stage 11.4) | Test | Result |
| --- | --- | --- |
| Context pipeline is logged and ordered (TC-11a) | `request` `TestContributionsAreLoggedInTableOrderAndRebuildTheRequest`, `TestUnchangedObservationAddsNothing`, `TestFailuresAreContainedPerContributor`, `TestRegisterKeepsTheTable`, `TestContributorServesOnlyItsScopesAgents` | pass. Three contributors registered out of order contribute in table order, each backed by one logged `user/message` with its source. The request's `Digest()` equals that of `surface.Rebuild` over the log prefix ending at its `requestAsOf`. An optional failure or panic is skipped and logged as `context/skipped`; a required failure blocks the turn. A contributor mounted in one preset reaches only that preset's agent, not its sibling's; a preset mount shadows the global one of its kind for that preset only; closing the preset unmounts it. |
| Flow F2 bracketed log matches golden (TC-11a) | `agent` `TestFlowF2LogMatchesGolden` | pass. One turn on memstore, with one `probe` call and one approval, serializes to `internal/agent/testdata/f2_log.golden` (19 events: turn and step brackets, the request header, the two settlements, the call, `approval/asked` and `approval/decided`, the result, `turn/end` completed). Generated ids, digests and the transport's wall-clock timing are numbered or masked, so runs are byte-identical (`-count=5`). Both sent requests rebuild from the log to the same digest. |
| Flow F2 persisted with checkpoints before I/O (TC-11b) | `agent` `TestFlowF2CheckpointsBeforeIOOnSQLite`, `TestFlowF2CheckpointOracleCatchesAMissingFlush` | pass. The SQLite store is wrapped so every successful `Writer.Flush` (the durability barrier) is recorded with the highest sequence it covered; because SQLite commits each append, rows alone cannot prove a barrier, so the test requires a recorded flush covering the cut before each provider attempt, before the tool body and at step end, in that order. The negative twin hides the recorded flushes and asserts the oracle then fails at every cut. This found a driver defect, fixed here: `driver/step.go` `call()` built and sent the request without a flush (architecture.md §6.2 point 9, "checkpoint, then build at the committed cut"); it now flushes after the request header and before `NextRequest`, and a failed flush means the attempt does not run. In addition, a second connection sees, before each provider attempt, every row the request reflects (and before attempt 2, step 1's `step/end`); before the tool body, the call's `assistant/message`, `tool/call` and `approval/decided`; after the turn, the `step/end` and `turn/end` as the last two rows. The log reopened from the database serializes to the same golden. |
| Import direction (RM-8) | `archtest` `TestImportDirection`, `TestRulesCatchViolations` | pass. A package at `internal/context` and an import of the driver from `internal/context/...` are both caught. |

The property test that no tool call is left unanswered in history on any exit path
is Stage 11.2's (`internal/agent/driver` `property_test.go`), and passes on this
branch.

## Manual check: M1 (impl.md DH-S-11)

`manual`, never a gate criterion (EV-6), and not a scenario. Scripted by
`src/harness/internal/agent/m1_manual_test.go` (`//go:build manual`), which reuses
the F2 composition with the real `deepseek-official` route (`deepseek-chat`, egress
transport under a model-request permit for `https://api.deepseek.com`, default
bounded retry, the driver on the wall clock). Run with

```
$env:DEEPSEEK_API_KEY = '<key>'   # never committed, never logged
go -C src/harness test -tags manual -count=1 -v -run TestManualM1 ./internal/agent/
```

- `TestManualM1OneTurn` sends one prompt asking for one `probe` call, prints the
  bracketed log (`M1 bracketed log`), and for each sent request the digest rebuilt
  by `surface.Rebuild` at its `requestAsOf` and its messages (`M1 request N:`). It
  fails unless the brackets close, the answerer was asked once, every rebuilt digest
  equals the sent one, and the shared oracle `f2Completed` holds: exactly one
  `turn/end`, completed; no failed attempt; the one `tool/result` a success (no
  `is_error` on the payload or its `tool_result` block) whose `tool_result.parts`
  text carries `probed: ping`; the last settlement an `assistant/message` with
  text and no tool call. `TestM1CompletionOracleOnTheF2Turn` (default build, no
  key) runs the same oracle on the scripted F2 turn, so it is proven against the real
  successful payload shape, and shows it refuses a turn cut after the tool result,
  an error result and a result without the probe's text.
- `TestManualM1CancelMidStream` sends a prompt with a long reply, cancels the turn
  (`CauseUser`, `ScopeTurn`) only after a nonempty answer `TextDelta` has
  streamed (a block start, usage chunk or error finish does not trigger it), and
  prints the `turn/end` reason (`M1 cancel:`). It fails unless the brackets close,
  `turn/end` is `aborted` with cause `user`, the one `step/end` carries cause
  `user`, and the one settlement is either an interrupted `assistant/message`
  keeping the streamed text with no tool call or an `assistant/attempt` whose
  failure class is `cancelled`; a provider or authentication failure fails it.

Without the key both skip. Attempted from this machine: `curl` to
`https://api.deepseek.com/` times out (exit 28), as the DH-S-9 attempt found, so no
request reached DeepSeek and the table stays unfilled.

| Run | Expected | Observed |
| --- | --- | --- |
| one prompt, one tool | brackets closed, one approval, every rebuilt digest equals the sent one, one completed turn, probe succeeded, final text reply | not run |
| cancel mid-stream | brackets closed, `turn/end` aborted with cause user, cancelled settlement | not run |

Owner: the operator runs both from a network that reaches `api.deepseek.com`, with
the key supplied at run time, and replaces each `not run` cell with the printed
lines.

## Scenarios

Authored for review in this PR, indexed under DH-S-17 in
`test/harness/acceptance-index.yaml` with authoring stage DH-S-11; **none
executed.** Each starts the built `dsh` through the test kit's launcher and
speaks only `src/sdk/protocol`, so it passes `acceptance.CheckBoundary`. Inputs
the SDK does not yet carry (record and replay commands, hooks that stop or read
a live session) come from `DSH_SCENARIO_*` environment variables that the run
stage supplies.

| Scenario | File | Test | Requires |
| --- | --- | --- | --- |
| DH-E-SESSION-1 | `session_rebuild_scenario_test.go` | `TestRequestsReceivedEqualRequestsRebuilt` | DH-S-11, DH-S-16 |
| DH-E-SESSION-2 | `session_kill_resume_scenario_test.go` | `TestKilledDuringToolCallResumesIntoAcceptedHistory` | DH-S-11, DH-S-13, DH-S-16 |
| DH-E-AGENT-1 | `agent_turn_scenario_test.go` | `TestRealMultiStepTurnIsBracketed` | DH-S-11, DH-S-16 |
| DH-E-AGENT-2 | `agent_cancel_scenario_test.go` | `TestCancelledStreamKeepsWhatWasShown` | DH-S-11, DH-S-16 |
| DH-E-AGENT-3 | `agent_stop_kill_scenario_test.go` | `TestStopAndKillLeaveAnHonestLog` | DH-S-11, DH-S-13, DH-S-16 |
| DH-E-AGENT-4 | `agent_budget_scenario_test.go` | `TestBudgetStopsARealToolLoop` | DH-S-11, DH-S-16 |
| DH-E-TOOLCHAIN-1 | `toolchain_record_replay_scenario_test.go` | `TestRecordThenReplayWithoutAKey` | none (as DH-S-1 indexed it) |

SESSION-2 and AGENT-3 also require DH-S-13 because they kill the host during a
shell tool call. With these, every `DH-E-*` scenario of DH-S-1 to DH-S-11 named in
impl.md §7.5 has a file and an index entry. `tools/harness-gate/scenarios_test.go`
expects each file and test (TOOLCHAIN-1 requires record to write a new `session[.vN].jsonl` into a copy that held no session log, upstream/toolchain.md:556, with every line a nonempty JSON object; `null`, `{}`, arrays, scalars and torn lines are refused, which the scenario's keyless `selfCheckSessionLog` proves before reading its inputs. AGENT-2 asserts the persisted shape the driver writes for a cancelled stream: one `assistant/message` whose text begins with the streamed text, no tool call, `turn/end` aborted{user}, and the recording proxy's `ClientClosed` showing dsh closed the provider connection before the response ended, areas/agent-loop.md:556; `ClientClosed` is read through `settled`, which waits, with a bound, on a per-request signal that the proxy handler closes after it records the outcome, so the check cannot race the handler. The scenario's keyless `selfCheckProxyClose` holds the handler before it records, shows the snapshot is still unrecorded and `settled` has not returned, then releases it and requires `ClientClosed` for an abandoned stream and not for a fully read response), checks the new helper files against the import
boundary, and lists the six new ids in the `require --stage DH-S-17` output.

Gate evidence: `go -C src/harness vet -tags scenario ./...` exits 0; the default
`go test -list . ./internal/testkit/scenarios/...` lists no test;
`GOWORK=off go -C tools/harness-gate test -run 'TestAuthoredScenarios|TestRequireSubcommand|TestPreflightSubcommand|TestScenarioIndex' . ./acceptance/` passes.

## Definition of done (tech-spec §2.3)

Not confirmed at this commit:

| Item | Status |
| --- | --- |
| All 106 steps built | no: DH-S-8 (8.3 to 8.5), DH-S-9 (9.1 to 9.3) and DH-S-11 (11.3, 11.4, 11.7) are unticked |
| Gate green on three OSes | not observable from this machine (Windows only); the inherited vet-gate failure of DH-S-9's evidence still applies |
| Eleven evidence records | ten closed or open records plus this one; DH-S-8, DH-S-9 and DH-S-11 are open |
| M1 recorded | no: the table above is unfilled |
| H-21 open | unchanged by this stage |

## Closure

**Open.** DH-S-11 closes when the M1 table above is filled from a real run and the
seven scenario files are reviewed in this stage's PR. Closing is then: tick impl.md
steps 11.3, 11.4 and 11.7 as built, tick "Close DH-S-11 and the story" in the
implementation plan once the definition of done above holds, and raise
`built-through` when DH-S-8 and DH-S-9 have closed (the index's own rule, as
`DH-S-10.md` explains).
