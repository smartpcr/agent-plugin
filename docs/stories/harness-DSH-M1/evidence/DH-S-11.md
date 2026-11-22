# DH-S-11 evidence

Decisions governing this record ([decisions.md](../decisions.md)): `m1-operator-run`, `context-package-name`, `agent-28-staged-acceptance`, `models-signed-in-claude-copilot` with M1-D-PIPE-3, M1-D-PIPE-4 and M1-D-CLOSE-3 (the M1 run moves to the signed-in route, so `dh-s-11-closure-path` is superseded), `platforms-windows-wsl`, M1-D-CLOSE-1, M1-D-CLOSE-4 and M1-D-LOOP-6, and the open `turn-budget-defaults-s11`, M1-D-LOOP-1 to M1-D-LOOP-5 and `retro-entry-order-s8-s11`. The restated definition of done is decisions.md sec. 1.1. Logged by gap-closure-plan Stage 12.1; merge commit: recorded when the Stage 12.1 change merges.

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The records below are history. The fields of impl.md sec. 7.3 and the per-step
manual-check register, with the M1 check re-targeted to the signed-in Claude and Copilot
routes, are at the end of this file, in "Evidence record (impl.md sec. 7.3)"; how to fill
them is in [the evidence runbook](README.md).

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

> **Re-targeted (plan 12.4.4; `models-signed-in-claude-copilot`, M1-D-CLOSE-3).** The
> DeepSeek procedure below is historical: it needs an API key, which M1 no longer uses,
> and its decision `dh-s-11-closure-path` is superseded. The M1 check now runs on the
> signed-in Claude and Copilot routes, at the client-invocation boundary. The procedure
> of record is the register under "Evidence record (impl.md sec. 7.3)" at the end of this
> file.

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
request reached DeepSeek and the table stays unfilled. Re-attempted 2026-11-22:
no key in the environment and `curl --max-time 15` still exits 28.

Operator runbook (from `src/harness`, a network that reaches `api.deepseek.com`):

```
$env:DEEPSEEK_API_KEY = '<supplied at run time, never committed>'
git rev-parse HEAD
go test -tags manual -count=1 -v -run 'TestManualM1OneTurn|TestManualM1CancelMidStream' ./internal/agent/
```

Paste the commit and, per run, the `M1 bracketed log`, `M1 request N` (sent and
rebuilt digests), `M1 turn/end`, `M1 final reply`, `M1 cancelled log` and
`M1 cancel:` lines into the Observed cells; the lines carry ids, digests and
text only, no credential. Both tests must report `PASS`; a failure is recorded
as observed and DH-S-11 stays open.

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
expects each file and test (TOOLCHAIN-1 requires record to write a new `session[.vN].jsonl` into a copy that held no session log, upstream/toolchain.md:556, with every line a nonempty JSON object; `null`, `{}`, arrays, scalars and torn lines are refused, which the scenario's keyless `selfCheckSessionLog` proves before reading its inputs. AGENT-2 asserts the persisted shape the driver writes for a cancelled stream: one `assistant/message` whose text begins with the streamed text, no tool call, `turn/end` aborted{user}, and the recording proxy's `ClientClosed` showing dsh closed the provider connection before the response ended, areas/agent-loop.md:556; `ClientClosed` is read through `settled`, which waits, with a bound, on a per-request signal that the proxy handler closes after it records the outcome, so the check cannot race the handler. The scenario's keyless `selfCheckProxyClose` holds the handler before it records, shows the snapshot is still unrecorded and `settled` has not returned, then releases it and requires `ClientClosed` for an abandoned stream and not for a fully read response; its wait runs through `awaitSettled`, which reports the bound as an error to the test goroutine, and the hold is released and the request cancelled on every exit path, so a failing self-check reports instead of hanging), checks the new helper files against the import
boundary, and lists the six new ids in the `require --stage DH-S-17` output.

Gate evidence: `go -C src/harness vet -tags scenario ./...` exits 0; the default
`go test -list . ./internal/testkit/scenarios/...` lists no test;
`GOWORK=off go -C tools/harness-gate test -run 'TestAuthoredScenarios|TestRequireSubcommand|TestPreflightSubcommand|TestScenarioIndex' . ./acceptance/` passes.

## Definition of done (tech-spec §2.3)

Not confirmed at this commit:

| Item | Status |
| --- | --- |
| All 103 steps (99 deliverables) built | no: DH-S-8 (8.3 to 8.5), DH-S-9 (9.1 to 9.3) and DH-S-11 (11.3, 11.4, 11.7) are unticked |
| Gate green on Windows (native) and Linux (WSL) (decision `platforms-windows-wsl`; historically "three OSes") | not observable from this machine (Windows only); the inherited vet-gate failure of DH-S-9's evidence still applies |
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

### Operator decisions (pinned 2026-11-22)

- `m1-operator-run`: **merge this stage with DH-S-11 Open; record M1 and close
  the story in a follow-up PR.** That PR's operator runs the runbook above with a
  real key, replaces both `not run` cells with the printed lines and the commit,
  ticks impl.md 11.3, 11.4 and 11.7, and ticks the plan's M1 and Close steps only
  if the definition of done above then holds at that one commit.
- `dh-s-11-closure-path`: the operator runs M1 with a real key, then ticks and
  closes, as above; this stage ticks nothing it has not built.
- `context-package-name`: **`request`**, as the plan and architecture.md (C30)
  name it; context-compaction-skill.md §6.1's `context/inject` is the area's
  older name for the same component.

Under these decisions, this stage's open M1 and closure rows are handed to the
follow-up PR, not left undecided.

## Evidence record (impl.md sec. 7.3)

These are the eight fields of [impl.md sec. 7.3](../../../harness/impl.md#73-the-evidence-record),
in the format of [the evidence runbook](README.md#the-evidence-record-headings). Plan step
18.1.7 fills them at the frozen M1 closure commit; plan Stage 17.4 adds the M1 record per
route before that. Until then each field points to the history above, which stays exactly
as it was recorded and is not closure evidence (M1-D-CLOSE-1).

### Source and build identity

At closure: not yet recorded (plan 18.1.7), with the client name and version of each
signed-in route. Historical: no commit was recorded.

### Environment and fixture

At closure: both local lanes, from their run records; for the M1 check, the operator's
own workstation (Windows, and WSL if run there) with the clients signed in. macOS: out of
scope (`platforms-windows-wsl`). Historical: one Windows machine. Fixtures: the F2
composition with one `probe` tool; the golden `internal/agent/testdata/f2_log.golden`.

### Exact tests and commands

At closure: the lane scripts; `go -C src/harness test -race -count=1 ./internal/context/...
./internal/agent/... ./internal/archtest/`; and the probe of plan steps 17.4.1 to 17.4.3,
`go -C src/harness test -tags manual -count=1 -v -run
'TestManualM1OneTurn|TestManualM1CancelMidStream' ./internal/agent/` with `DSH_M1_ROUTE`
set to `claude` and then `copilot`. Historical: the DeepSeek runbook above.

### Run identifiers

At closure: the run-record ids of both lanes, in the runbook's template (plan 18.1.3),
and per route the masked client session ids. WSL lane: not run. Windows lane: not run.
macOS: out of scope (`platforms-windows-wsl`). Historical: no request reached a provider,
so no request id exists.

### Results

At closure: not yet recorded. Historical: "Tests (TC-11a, TC-11b)" above.

### Manual checks

impl.md DH-S-11 names two checks: from a Go test that composes the real components, send
one prompt to the real model with one test tool and record the bracketed log and the
rebuilt request (steps 11.1, 11.2, 11.3, 11.7); cancel a second run mid-stream and record
the cause on `turn/end` (step 11.4). This is milestone M1, and it is not a scenario.
Under `models-signed-in-claude-copilot` and M1-D-CLOSE-3 (in force, a boundary route) it
runs on the user's signed-in Claude and Copilot clients, with no API key; the rebuilt
request is compared at the logged client-invocation boundary. Steps 11.5 and 11.6 take
the rule-3 default. The probe is retargeted by plan steps 17.4.1 (route selector) and
17.4.2 (redaction) and run by step 17.4.3
([plan Stage 17.4](../gap-closure-plan.md#stage-174-m1-on-the-signed-in-route-and-dh-s-11-closure));
the routes it uses come from plan Stage 16.5
([16.5](../gap-closure-plan.md#stage-165-signed-in-client-routes-and-the-dh-s-9-manual-check)).
These rows cannot run before them. Claude must pass; Copilot is run and recorded either
way, and a Copilot failure is recorded as seen and stays open (M1-D-LOOP-6, lead
settlement). How to run and fill the register:
[the evidence runbook](README.md#the-signed-in-route-checks-dh-s-9-and-dh-s-11).

#### Manual checks register

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 11.1 | M1, one prompt with one test tool: each step uses one logged model selection | probe (plan 17.4.3): `TestManualM1OneTurn` per route (`DSH_M1_ROUTE=claude`, then `copilot`); from the redacted `M1 bracketed log`, record the one model selection logged per step and the route and model id | the operator's workstation (Windows; WSL if run there) | | | | `manual` |
| 11.2 | M1, one prompt with one test tool: the step's tools are fixed once, its calls run in an ordered batch | probe: the same run; record the one `probe` call, its approval and its `tool/result` (`probed: ping`) | the operator's workstation | | | | `manual` |
| 11.3 | M1, one prompt with one test tool: every turn and step is bracketed and closed | probe: the same run and the cancel run; record that the brackets close in both, one `turn/end` completed and one aborted | the operator's workstation | | | | `manual` |
| 11.4 | M1, a second run cancelled mid-stream: the cause on `turn/end` | probe: `TestManualM1CancelMidStream` per route; copy the redacted `M1 cancelled log` and `M1 cancel:` lines: `turn/end` aborted with cause `user`, `step/end` cause `user`, one cancelled settlement | the operator's workstation | | | | `manual` |
| 11.5 | rule-3 default: agents are created, resumed and torn down as one transaction | composed test: `go -C src/harness test -count=1 -v ./internal/agent/` (the lifecycle tests of DH-D-AGENT-6); record create, resume and teardown | one lane | | | | `manual` |
| 11.6 | rule-3 default: one delegation seam; every child has one owner | composed test: `go -C src/harness test -count=1 -v ./internal/delegate/...`; runs only after plan Stage 17.2 (impl.md 11.6 is unticked by plan 17.4.6 until then) | one lane | | | | `manual` |
| 11.7 | M1, one prompt with one test tool: the request rebuilt from the log equals the one sent | probe: the `TestManualM1OneTurn` run; copy the `M1 request N` lines (sent and rebuilt digests at the client-invocation boundary, M1-D-CLOSE-3), `M1 turn/end` and `M1 final reply` | the operator's workstation | | | | `manual` |

Every printed line passes the `m1Redact` filter of plan step 17.4.2 (account name and
email, `Authorization` and cookie values, token prefixes, token file paths); a second
person reviews the record before it is committed (R-11). A vendor that cannot be reached
is recorded `not run: <client>, <date>, <host>, <error line>`, and the row stays open.

### Cleanup evidence

At closure: the cancelled run leaves no client process alive (a process listing after
row 11.4), and the user's own sign-in is unchanged. Historical: none recorded.

### Open items

At closure: plan 18.1.11 lists them, including H-21 with its closing step impl.md 16.10.
Now open: the M1 record per route (plan Stage 17.4); the seven retargeted scenario files
and their review (plan 17.4.4, 17.4.5); the definition-of-done table above; the decisions
M1-D-LOOP-1 to -5 and `turn-budget-defaults-s11`; macOS, out of scope
(`platforms-windows-wsl`).
