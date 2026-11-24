# DH-S-11 evidence

Decisions governing this record ([decisions.md](../decisions.md)): `m1-operator-run`, `context-package-name`, `agent-28-staged-acceptance`, `models-signed-in-claude-copilot` with M1-D-PIPE-3, M1-D-PIPE-4 and M1-D-CLOSE-3 (the M1 run moves to the signed-in route, so `dh-s-11-closure-path` is superseded), `platforms-windows-wsl`, M1-D-CLOSE-1, M1-D-CLOSE-4 and M1-D-LOOP-6, and the open `retro-entry-order-s8-s11`. The restated definition of done is decisions.md sec. 1.1. Logged by gap-closure-plan Stage 12.1; merge commit: recorded when the Stage 12.1 change merges. Added by plan Phase 17, all in force: the owner's answers M1-D-LOOP-1 to M1-D-LOOP-5 (2026-11-23) and, with M1-D-LOOP-5, `turn-budget-defaults-s11`.

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The stage itself closes at plan step 17.4.9, which **has not closed it yet**: plan Phase 17
built the prompt assembly, the delegation seam proofs, the context pipeline and the M1
probe on the signed-in routes (section ["Gap closure (plan Phase 17)"](#gap-closure-plan-phase-17),
every test passing in both gate lanes at `fda4b98`), but the owner has not run the M1
manual check (plan 17.4.3) and has not reviewed the seven retargeted scenarios (see
[Closure](#closure)). The records before "Gap closure (plan Phase 17)" are history. The
fields of impl.md sec. 7.3 and the per-step manual-check register, with the M1 check
re-targeted to the signed-in Claude and Copilot routes, are at the end of this file, in
"Evidence record (impl.md sec. 7.3)"; how to fill them is in
[the evidence runbook](README.md).

DH-S-11 is the agent loop, milestone M1. Stage 11.1 built impl.md steps 11.1 and
11.2, Stage 11.2 built 11.3 and 11.4 with the no-unanswered-call property test, and
Stage 11.3 built 11.5 and 11.6. Stage 11.4 built step 11.7, added the flow F2 golden
and its SQLite twin (TC-11a, TC-11b), authored the remaining `DH-E-*` scenarios of
DH-S-1 to DH-S-11, and scripted the M1 manual check. Plan Phase 17 closed the gaps
M1-G-LOOP-1 to -16 that the review found in this work.

**DH-S-11 is not closed yet.** The ticks after plan step 17.4.6 (Phase 17), in impl.md and
the story plan alike: steps 11.1, 11.2, 11.5 and 11.6 are ticked as built (11.6 was
unticked as a known-false tick and re-ticked in the same change, because every test
M1-D-LOOP-3 B keeps in M1 now passes); steps 11.3, 11.4 and 11.7 stay unticked, because
the M1 manual check that covers them (rows 11.3, 11.4 and 11.7 of the register below) has
not run. Built is not done: no step's `DH-E` scenario has run. The plan's "Close DH-S-11
and the story" step stays unticked, and `built-through` stays at DH-S-8
(`CheckBuiltThrough` allows no more while DH-S-9 and DH-S-11 are open). See
[Closure](#closure).

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
> file, and the operator's steps, commands and record table are
> [DH-S-11-manual-runbook.md](DH-S-11-manual-runbook.md) (probe retargeted by plan steps
> 17.4.1 and 17.4.2; `m1_manual_test.go` no longer names DeepSeek).

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

**Retargeted (plan 17.4.4, M1-D-CLOSE-4 A).** The seven files below no longer take
`DSH_DEEPSEEK_API_KEY`, a provider base URL or a recording proxy. Under OWNER-2 each runs on
the user's own signed-in client routes (`claude-code`, then `copilot-cli`, one subtest per
route through `forEachRoute` of `signedin_routes_test.go`; Claude must pass, Copilot is
recorded either way, M1-D-LOOP-6 A), with no API key anywhere and the route selected by its
id in the layer template (`{route}`) and `DSH_SCENARIO_<route>_MODEL`. The proxy assertions
of DH-E-SESSION-1, DH-E-AGENT-1 and DH-E-AGENT-2 are re-expressed at the boundary
16.5.2 says is observable, the settled client invocation (M1-D-PIPE-4 A): the host rebuilds
every settlement's argument list, its digest and the standard input it wrote from the log,
and the scenario compares them with the recorded invocation, recomputing the
standard-input digest itself; a keyless `selfCheckInvocationOracle` proves the comparison
refuses a differing argument list, digest or input before any input is read. DH-E-AGENT-2's
"connection closed" becomes "no chunk of the cancelled turn after it ended" (the process
tree stop is DH-T-LLM-23 and -27), and "the second request carries the shown text" becomes
"turn 2's rebuilt standard input, matching its logged digest, carries it". DH-E-TOOLCHAIN-1
records on the route and replays with empty client homes, client stand-ins first on the
`PATH` that must never run, and refusing proxies. The rows are in `areas/agent-loop.md`,
`areas/core-session.md`, `upstream/toolchain.md` and `e2e-scenarios.md` Phase 11.

**Scenario review line (plan 17.4.5).** Reviewer: the owner. PR:
[#93](https://github.com/smartpcr/agent-plugin/pull/93) (branch `m1-phase-17`). Status:
**pending**. Until the owner has reviewed the seven files in #93, step 17.4.5 stays open and
DH-S-11 cannot close
(M1-D-CLOSE-4 A).

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

The recording-proxy details of the paragraph above (AGENT-2's `ClientClosed`,
`selfCheckProxyClose`) are the Stage 11.4 form, kept as history; the retarget at the top of
this section replaced them. Retarget gate evidence (plan 17.4.4, local Windows lane, not a
lab or scenario run): `go -C src/harness vet -tags scenario ./...` (run by the gate test
below) exits 0, and the analyzers through `-vettool` with `-tags=replay,scenario,bench`
report nothing for `internal/testkit/scenarios`;
`GOWORK=off go -C tools/harness-gate test -count=1 -run '^TestAuthoredScenariosAreIndexedAndCompileButDoNotRun$' .`
passes; `GOWORK=off go -C tools/harness-trace run . verify` passes. File and test names are
unchanged, so the gate's pinned names stand.

M1 gate repair (plan 13.3, M1-G-GATE-11 to -14 and -27; [DH-S-1.md, "M1 gate repair"](DH-S-1.md#m1-gate-repair)): the vet debt added after DH-S-9 was 27 findings (QG-4 `time.Sleep`, QG-5 goroutine without `t.Cleanup`) in `src/harness/internal/agent/agent_test.go`, `agent/driver/driver_test.go`, `lifecycle_test.go`, `retry_test.go`, `delegate/subagent/subagent_test.go`, `tools/core/schedule_test.go` and, under the `scenario` tag, `testkit/scenarios/store_two_process_scenario_test.go` and `store_crash_recovery_scenario_test.go`. They now wait on the scripted model's `WaitRequests` and `PrefixServed` signals (`testkit/model`), on `synctest` bubbles or on explicit signals, and every goroutine they start is released and joined (P-C); the scenario polls of another process go through `testkit/procwait`. The DH-T-AGENT tags are unchanged; the two scenario files are vetted, not run (EV-4). `manual`, uncommitted at the time of writing: `harness-gate` vet with and without `-tags=replay,scenario` reports nothing for these packages.

## Gap closure (plan Phase 17)

Plan Phase 17 ("Agent Loop") closes the review's gaps M1-G-LOOP-1 to -16 in this stage.
**Why it was needed.** Before it, the model received no system prompt at all: the driver
never logged a `system/message`, so every request went out with only the history, the tool
schemas and the route. Recording M1 then would have shown a model with no instructions, not
flow F2 as designed. Phase 17 assembles the prompt (17.1), proves the delegation seam
(17.2), completes the context pipeline (17.3), and retargets the M1 probe and the
scenarios to the signed-in routes (17.4).

| Commit | What it holds |
| --- | --- |
| `066a4bf` | the owner's answers M1-D-LOOP-1 to -5 in [decisions.md](../decisions.md) |
| `df8ed28` | Stage 17.3, the context pipeline (17.3.1 to 17.3.4) |
| `72704a7` | Stage 17.2, the delegation seam proofs and the DH-S-29 deferrals (17.2.1 to 17.2.6) |
| `8aef1c3` | 17.4.1, 17.4.2 and 17.4.4: M1 on the signed-in routes, `m1Redact`, the seven scenarios retargeted |
| `ad21287` | DH-E-LLM-1 (a DH-S-9 scenario) accepts the driver's cancel settlement |
| `788dae6` | 17.1.1 to 17.1.8: prompt assembly and one-batch admission |
| `1cb9026` | 17.1.9 to 17.1.12: the F2 rig composes the prompt, the Rule 6 golden regeneration, the three-step golden, the bench re-run |
| `fda4b98` | the staticcheck fix (U1000 in `scenarios/sdkhost_test.go`), the closing commit for Phase 17's gate evidence |
| `0c284c8` | the run records |

**Where the results come from.** Unless a row says otherwise, every test below passed in
both gate runs at `fda4b98` ([DH-S-1.md, "Phase 17 exit gate runs"](DH-S-1.md#phase-17-exit-gate-runs)).
The `test` gate runs `go test -tags= -race -shuffle=on -count=1 ./...` per module, so the
`-race -count=1` results the plan asks for are the gate's. Two kinds of result are
not gate results, and are labelled where they appear: the continuation benchmark
(`bench` tag) and the three M1 `e2e` features, which the gate compiles and vets but does not
run.

### Stage 17.1: prompt assembly and one-batch admission

**What changed.** Each proposed step now asks the prompt component for its system prompt
and its tool list, through a new `Prompter` in `driver.Options` and `FactoryOptions`; a
composition without one is refused. The assembly is made once per proposed step and
reused by its retries; if it fails, the turn ends `error{by: "driver/prompt"}` and no step
opens. **Admission** (the point where the step's inputs are written to the log before the
request is built) is now one batch, one `AppendGroup`: the system prompt (only when its
fingerprint changed), the user messages the step entered, the runtime-context snapshots,
the `request/header` and, when the tool names changed, a `developer/message` naming the
added and removed tools. The header's tools come from the assembly in `toolOrder`. An
unchanged prompt adds nothing; a retry adds only a changed header. After this stage the
model receives the identity line (`prompt.IdentityLine`), and the log shows which section
supplied each part of the prompt and who owns it (the header's manifest).

**Request header v2 (Rule 6).** `request/header` gains two optional fields, as payload
version 2: `fingerprint` (the digest of the assembly the step's system prompt and tools
came from) and `manifest` (the sections, in order, with their owners). Version 1 is read by
an identity upgrade: a v1 header decodes with an empty fingerprint and manifest, and the
request rebuilt from an old log has the same digest as before (`TestOldHeadersStillReplay`,
on SQLite). The persisted-format inventory records the change (`persisted.golden`,
`persisted.notes`, `rows.jsonl`); this is the phase's one Rule 6 regeneration step, 17.1.10.

**Goldens, new and changed.**

| Golden | Change | Steps |
| --- | --- | --- |
| `internal/agent/testdata/f2_log.golden` | Regenerated; still 19 events. Step 1 now admits, after `model/selection`, the `system/message` carrying `prompt.IdentityLine` (seq 5), the claimed `user/message` (seq 6; it was seq 3, before `step/start`) and a v2 header with `fingerprint` and `manifest` `[{identity, 0, tools.prompt}]` (seq 7). The second header, an unchanged copy logged at step 2 (old seq 15, gap M1-G-LOOP-4), is gone. Every sent request's digest equals the one rebuilt from the log | 17.1.4, 17.1.7, 17.1.8, 17.1.10 |
| `internal/agent/testdata/f2_retry_log.golden` | Regenerated the same way; still 14 events. The header the retry used to repeat (old seq 10) is gone | 17.1.10 |
| `internal/agent/driver/testdata/three_step.golden` | New. A call, then a parallel pair of calls, then an answer; a persona edit through `prompt.Service.Configure` before step 3 forces exactly one node-0 replace (seq 22, `replace[5,5]`); `turn/end` is completed | 17.1.11 |
| `internal/session/store/testdata/persisted.golden`, `persisted.notes`, `rows.jsonl`; `internal/session/store/allevents/testdata/persisted.golden` | `request/header` 1 to 2, its compatibility note, and one v2 row | 17.1.3 |

**Tests.**

| Step | Test ID | Tests (package) | What they show | Result |
| --- | --- | --- | --- | --- |
| 17.1.1 | `DH-T-AGENT-27` | `TestCompositionWithoutAPrompterIsRefused` (`agent/driver`); the "no prompt" case of `TestNewFactoryRefusesBadOptions` | `Start` and `NewFactory` refuse a composition with no `Prompter` | pass |
| 17.1.2 | -- | `TestAssemblyFailureOpensNoStep` | a failed assembly ends the turn as `error{by: "driver/prompt"}` with no step opened | pass |
| 17.1.3 | `DH-T-SESSION-11` | `TestRequestHeaderV2RoundTrip` (`session/event`), `TestHeaderAndToolHistory` (`session/surface`), `TestOldHeadersStillReplay` (`session`, SQLite) | v2 round trip; the header fold; a v1 log replays with empty fields and the same digest | pass |
| 17.1.4 to 17.1.6 | `DH-T-AGENT-12`, `-26`, `DH-T-TOOLS-14` | `TestAdmissionIsOneBatch`, `TestEnteredMessagesAreAdmittedInTheirOrder` (`-12`), `TestChangedPromptReplacesNodeZero`, `TestRuntimeContextIsASnapshotWhenItChanges`, `TestToolChangeNoticeNamesTheAddedTool` (`-26`), `TestPromptServiceReachesTheHeader` (`DH-T-TOOLS-14`) (`agent/driver`) | one `AppendGroup` per admission; entered messages in order; a changed prompt replaces node 0; a runtime-context snapshot when its text changed; one notice naming the added tool, listed in `toolOrder`; the prompt service's tools reach the header | pass |
| 17.1.7 | -- | `TestUnchangedHeaderIsNotRepeated` | written first to reproduce the duplicate header of the old `f2_log.golden`; an unchanged prompt now adds neither a `system/message` nor a `request/header` | pass |
| 17.1.8 | `DH-T-AGENT-4`, `-5`, `-6` (re-run) | `TestConcurrentSendsAreOrderedAndUnique`, `TestQueuedFollowUpsAreOneTurnEach`, `TestClaimedItemRunsAtMostOnceAcrossACrash`, `TestIdleInjectionOpensNoTurnAndIsClaimedAtTheNextStep` (`agent`), `TestTurnRunsStepsUntilTheModelAnswers` (`agent/driver`); `TestNoToolCallLeftUnansweredOnAnyExit` with the new exit `crash-between-claim-and-admit`; `TestCrashBetweenClaimAndAdmit` (SQLite) | the claim writes only the inbox splice; the driver admits the claimed messages after the system prompt; a crash between claim and admission closes the turn `interrupted`, the item never runs twice, and `Inbox.Discarded()` reports it (cause `agent.CauseUnadmitted`) | pass |
| (fix) | -- | `TestInterruptedPrefixKeepsTheClientInvocation` (`agent/driver`) | settling an interrupted `assistant/message` keeps its client invocation | pass |
| 17.1.9, 17.1.10 | `DH-T-AGENT-8` | `TestFlowF2LogMatchesGolden`, `TestF2StepPromptReachesTheModelAndTheLog` (`agent`) | the F2 rig mounts `prompt.Component` with `prompt.Defaults()` and a contributor-free `request.New`, and passes the prompt service; the Phase 16 retry, grant and spill mounts are kept. Scenario "the step prompt reaches the model and the log" | pass |
| 17.1.11 | `DH-T-AGENT-7` | `TestThreeStepTurnLogMatchesGolden` (`agent/driver`) | Scenario "three-step turn golden"; the old `-7` tag is removed from `driver_test.go` | pass |
| 17.1.12 | `DH-T-AGENT-28` | `BenchmarkContinuation` (`agent/driver`, `bench` tag) | the table below | pass (see below for where it ran) |

**Continuation latency, DH-T-AGENT-28 (17.1.12).** Re-measured with the one-batch admission
in place, on the Windows workstation (13th Gen Intel Core i9-13900K), with the `bench` tag,
at `1cb9026`; `continuation_bench_test.go` is unchanged from there to `fda4b98`. This is the
lead's local run, not a gate result: the gate compiles and vets the bench file in both lanes
and does not run it. The time is from a waking send on a resumed session to the first model
request leaving the driver.

| Rung | Turns | Records | p50 | p99 |
| --- | --- | --- | --- | --- |
| p50 | 4 | 70 | 0.51 ms | 1.51 ms |
| p90 | 42 | 716 | 3.08 ms | 4.32 ms |
| p99 | 426 | 7,244 | 28.7 ms | **33.8 ms** |
| max (measured, not budgeted) | 1,024 | 17,410 | 69.8 ms | 83.4 ms |

Every budgeted rung is under the 100 ms budget M1-D-LOOP-2 pins; the max rung is too,
though it is not budgeted. One tool-heavy turn now logs 17 records, not 19
(`recordsPerTurn`), because an unchanged prompt adds no `system/message` or header.

**E2E features (not a gate result).** The three M1 `e2e` features (brackets 3/3, lifecycle
3/3, context pipeline 3/3) pass with a prompter, run by the lead with the `e2e` tag at
`1cb9026`; the gate vets them in both lanes. Two ordering checks were retargeted to
M1-D-LOOP-1 A: the claimed messages are admitted inside the step, after `step/start`.

**Deviations from the plan's text** (recorded, not hidden):
- 17.1.7: the comparison uses `vocab.Canonical`, the digest encoding of the session's tool
  fold, not `internal/schema`, because `schema.Fingerprint` strips descriptions, and a
  changed description must count as a changed header.
- 17.1.8: "reported discarded" is computed from the log when the inbox opens
  (`Inbox.Discarded()`); there is no durable discard record and no `InboxDiscarded` notice.
- 17.1.4: a node-0 replace happens whenever node 0 is a `system/message`; the tool notice
  is written only when the new header continues the series; a runtime-context snapshot is
  written only when its text changed.
- 17.1.11: the scenario's wording (three steps: a call, a pair, an answer) was followed; the
  step's text said "two tool calls, then a pair".

**M1-D-LOOP-1 outcome.** Option A, in force: the claim writes only the splice, and the
driver admits the claimed messages at point 8, after the system prompt, in the one
admission batch, so the first system prompt is node 0 of the surface (seq 5 of
`f2_log.golden`, before the user message at seq 6). A crash between the claim and the
admission closes the turn `interrupted` and the item is reported discarded, never run twice
(`TestCrashBetweenClaimAndAdmit`, the property test's new exit).

### Stage 17.2: delegation seam proofs

**What changed.** The seam's promise (one owner per child, a second `Dispose` does nothing,
no goroutine left after the root goes, concurrent operations on one child serialised, and
the settle points S1 to S7 in a fixed order) is proven by tests tagged with their IDs. The
manager gains `Spec.Resume` and a per-activation reservation, so a send, a release and a
resume on one child run one at a time.

| Test ID | Tests (`delegate/subagent`) | What they show | Result |
| --- | --- | --- | --- |
| `DH-T-SUBAGENT-1` | `TestStartPublishesResultBlocksDisposeIdempotent` | `Start` returns only after A6, `Result` blocks until S7, a second `Dispose` (also two at once) is a no-op | pass |
| `DH-T-SUBAGENT-2` | `TestRootDisposalLeavesNoGoroutine` | three children under two owners; after a root `Dispose` no child is registered and the goroutine count returns to its baseline | pass |
| `DH-T-SUBAGENT-3` (M1 legs) | `TestSettleOrderGolden`, golden `delegate/subagent/testdata/settle_order.golden` | the recorded S-points of natural end, dispose and owner teardown equal the golden | pass |
| `DH-T-SUBAGENT-9` | `TestConcurrentSendReleaseResumeSerialised` (under `-race`) | concurrent sends, release and resume on one child are serialised | pass |
| `DH-T-SUBAGENT-4`, `-14`; the time-limit and interrupt legs of `-3` | -- | deferred: `//dsh:test DH-T-SUBAGENT-3/-4/-14 later DH-S-29` in the package's `doc.go` | deferred to DH-S-29 |

All four tests also passed 30 consecutive `-race` runs (the lead, locally). The plan's
`trace-tests --stage DH-S-11` row 11.6: required `-1`, `-2`, `-3`, `-4`, `-9`, `-14`; tagged
`-1`, `-2`, `-3`, `-9`; deferred `-4`, `-14`; missing none. The tool accepts a deferral only
when the impl.md section of the stage it names lists the ID, and it reported no unbacked
deferral: impl.md's DH-S-29 pointer line maps `-4` to steps 29.1 and 29.3, `-14` to 29.3,
and the `-3` legs to 29.1 and 29.4. `subagent-team.md:434` is amended to match.

**M1-D-LOOP-3 outcome.** Option B, in force: `DH-T-SUBAGENT-4` (a child that delegated is
held until its own child settles), `-14` (the capability check at A1) and the time-limit
and interrupt legs of `-3` move to DH-S-29, which builds those features; every test the
decision keeps in M1 passes. impl.md step 11.6 is therefore built, and plan step 17.4.6
re-ticks it. DH-D-SUBAGENT-1 is built, not done: its scenario `DH-E-SUBAGENT-2` has not run.

### Stage 17.3: context pipeline completion

**What changed.** The context pipeline has a defined place where compaction runs: an
optional `Options.Compact`, a `Considerer` (a subset of the spec's `compact.Engine`), called
after an `Enter` and before the contributors with the `pressure` or `model-switch`
trigger. It is nil at M1. A fake compactor proves that a contributor re-adds, in the same
step, a baseline that compaction shadowed. A recorded multi-step log now replays with every
observation forbidden. The contributor output gains `After` and `Reword` for the DH-S-25
contributors.

The 11.7 rows (impl.md step 11.7, DH-D-CONTEXT-1):

| Test ID | Tests | What they show | Result |
| --- | --- | --- | --- |
| `DH-T-CONTEXT-1` | `TestRecordedLogRebuildsEveryRequest` (`context/request`) | three turns with contributors are rebuilt while every contributor, file, clock, shell and session read fails; every request's digest equals its record | pass |
| `DH-T-CONTEXT-2` | `TestCompactionIsCheckedBeforeTheContributors`, `TestBaselineShadowedByCompactionIsReentered`, `TestAfterAndRewordMergeInTableOrder`, and the Stage 11.4 tests (`TestContributionsAreLoggedInTableOrderAndRebuildTheRequest`, `TestUnchangedObservationAddsNothing`) | the compaction hook runs before the contributors; a shadowed baseline is re-entered in the same step and the request rebuilds to the sent digest (with the new admission); `After` and `Reword` merge in table order | pass |
| `DH-T-CONTEXT-3` | `TestCompactionFailuresFollowTheTrigger`, `TestAfterAndRewordAreChecked`, and Stage 11.4's `TestFailuresAreContainedPerContributor` | a compaction failure follows its trigger; bad `After` and `Reword` outputs are refused; contributor failures are contained | pass |
| `DH-T-CONTEXT-25` | `TestImportDirection`, `TestRulesCatchViolations` (`archtest`) | "no consumer package imports a provider package": the import rule "no package under `internal/context` imports the agent driver or an llm provider, wire or transport package", which `rules.go` already names `DH-T-CONTEXT-25`, holds on the real modules, and the checker catches a seeded violation | pass at `fda4b98`; **tagged after it** (see below) |

**DH-T-CONTEXT-25, untagged at `fda4b98`.** The test that proves it existed and passed in both
lanes, but carried no tag, so `trace-tests --stage DH-S-11` reported it missing at row 11.7.
The closure records' change adds `DH-T-CONTEXT-25` to the `//dsh:test` directives of
`TestImportDirection` and `TestRulesCatchViolations` (`internal/archtest/archtest_test.go`),
a comment-only change. The tag is therefore not in the gate runs' commit; it is re-proved
at the next lane run (at the latest the closure commit, plan 18.1.3). Scope of the guarantee:
at M1 the only package under `internal/context` is `context/request`, and the rule bars the
providers it could reach, the agent driver and the llm provider, wire and transport packages.
When DH-S-25 adds consumer and provider packages inside `internal/context` (for example
`context/skill/tool` and `context/skill/fsprovider`), the rule must widen to cover them.

**Deviations from the plan's text.** `Trigger` is a struct, not a bare kind. The merge rules of
`After` and `Reword` were not stated by the spec; the agent that built them wrote them into
[context-compaction-skill.md](../../../harness/areas/context-compaction-skill.md) sec. 6.1.

**M1-D-LOOP-4 outcome.** Option A, in force: only the compaction hook is built, nil at M1, and
it is proven with a fake compactor (`TestCompactionIsCheckedBeforeTheContributors`,
`TestBaselineShadowedByCompactionIsReentered`). The engine itself arrives with DH-S-25.

### Stage 17.4: M1 on the signed-in route

**What was built.**
- **17.4.1.** `agent/m1_manual_test.go` (build tag `manual`) runs on the `claude-code` or
  `copilot-cli` route, selected by `DSH_M1_ROUTE=claude|copilot`. The skip check runs only the
  client's version probe and never sends a prompt. The shared oracle `f2Completed` is kept:
  `TestM1CompletionOracleOnTheF2Turn` (default build, keyless) passes. The probes are
  `TestManualM1OneTurn` and `TestManualM1CancelMidStream`.
- **17.4.2.** `testkit/redact` and `m1Redact` mask every printed line.
- **17.4.4.** The seven DH-S-11 scenario files are retargeted per route (section "Scenarios"
  above).

**Redaction note (17.4.2).** Every line the probe prints passes `m1Redact` first: it masks the
signed-in account name and email (by value from `DSH_M1_ACCOUNT` and the machine's user name,
and by shape), `Authorization` and cookie values, the `sk-ant-`, `gho_`, `ghu_`, `ghp_`,
`ghs_`, `ghr_` and `github_pat_` token shapes, bearer strings and token file paths, each
replaced by `[redacted]`. If a pattern survives the masks, the test fails and the line is
not printed. `TestM1RedactionCatchesIdentity` (`agent`, keyless) proves it, and passes in
both lanes: a fake email, a bearer header and a `gho_` token, with the other shapes, come
out masked, and a surviving pattern fails the test. It carries no `DH-T` tag: no test ID in
[agent-loop.md](../../../harness/areas/agent-loop.md) names the probe's redaction, so it is
left untagged (its DH-S-9 counterpart `TestSignedInRedaction` is tagged with the route
failure-mapping IDs `DH-T-LLM-22` and `-26`). A second person still reviews the record
before it is committed (R-11).

**Retargeted scenarios (17.4.4).** The seven files no longer name DeepSeek or take a key; the
recording-proxy assertions of DH-E-SESSION-1 and DH-E-AGENT-2 moved to the settled client
invocation (M1-D-PIPE-4 A). In both lanes at `fda4b98`: `vet` with `-tags=replay,scenario,bench`
(and the project's analyzers) passes, the `scenario-index` gate passes, and the `self` gate
runs `TestAuthoredScenariosAreIndexedAndCompileButDoNotRun`. The retarget left two helpers
of the recording proxy unused, which failed `staticcheck` on WSL at `1cb9026`; `fda4b98`
removes them. DH-E-LLM-1 (DH-S-9) was aligned with the driver's cancel settlement in
`ad21287`. **None of these scenarios has run**; they run at DH-S-17.

**Scenario review line (17.4.5).** See "Scenarios" above: reviewer the owner, in the Phase 17
PR, **pending**.

#### M1 record per route (17.4.3, run at the frozen closure commit)

Run on 2026-11-23 at `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` (plan 18.1.6) by the lead session, at the owner's instruction (decision `manual-checks-run-by-lead`), on the owner's Windows workstation with the owner's own signed-in clients and no key (`manual`, never a scenario). Neither client is installed in WSL, so the run is Windows native only, as the runbook allows. Logs: [signedin-m1-claude-593ceaa.log](runs/manual-593ceaa/signedin-m1-claude-593ceaa.log) and [signedin-m1-copilot-593ceaa.log](runs/manual-593ceaa/signedin-m1-copilot-593ceaa.log). Under M1-D-LOOP-6 A, Claude must pass, and it does; Copilot is recorded as seen and stays open.

| Route | Run | Commit | Client and version | Model | Go, OS and host | Printed lines (`M1 ` lines, redacted) | Result |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `claude-code` | one turn | `593ceaa` | Claude Code 2.1.312 (`claude.exe`) | `sonnet` | go1.27.4, windows/amd64, Windows 11 10.0.28000 | A 19-event bracketed log, one `model/selection` per step, one `probe` call (`allowed-once`) giving `probed: ping`. `M1 request 1: asOf=7` sent = rebuilt `sha256:f249e334…`, and `M1 request 2: asOf=15` sent = rebuilt `sha256:bbb98262…`, both `equal=true` and `invocation-equal=true`. `M1 turn/end`: completed. `M1 final reply`: "The probe tool returned: `probed: ping`" | **PASS** |
| `claude-code` | cancel mid-stream | `593ceaa` | Claude Code 2.1.312 | `sonnet` | as above | An 11-event cancelled log: `step/end` cause `user`, `M1 cancel: turn/end reason={Kind:aborted … Cause:user …}`, `interrupted prefix kept (2 bytes)`, session `c7a904a2…`. No client process was left (the PID sets before and after are equal) | **PASS** |
| `copilot-cli` | one turn | `593ceaa` | GitHub Copilot CLI 1.0.115-7 (`copilot.exe`) | `claude-sonnet-4.5` | as above | `M1 turn/end: … Kind:error … llm/client-contract: copilot-cli: the client exited with status 1 and printed nothing this route can place`; the answerer was asked 0 times | **FAIL, open** (M1-D-LOOP-6) |
| `copilot-cli` | cancel mid-stream | `593ceaa` | GitHub Copilot CLI 1.0.115-7 | `claude-sonnet-4.5` | as above | `no answer text streamed within 2m`; no client process was left | **FAIL, open** (M1-D-LOOP-6) |

No token text appears: the log search with the token-shape patterns finds only the `first_token` timing field names.

### Decision outcomes (M1-D-LOOP-1 to -6)

All six are in force ([decisions.md](../decisions.md) sec. 5).

| Decision | Choice in force | Outcome in this phase |
| --- | --- | --- |
| M1-D-LOOP-1 | A: the claim writes only the splice; the driver admits at point 8 | Built (17.1.8): the system prompt is node 0; a crash between claim and admit closes the item as discarded, never run twice. See "Stage 17.1" |
| M1-D-LOOP-2 | A: pin 100 ms as the M1 `DH-T-AGENT-28` budget; a new held-open entry pins the final figure | Pinned: the bench's compiled budget is `provisionalBudget = 100 * time.Millisecond` (`continuation_bench_test.go`), unchanged; the p99 rung measures 33.8 ms. The final figure is held open as **H-37** ("Held open" below), closing at impl.md 16.7 on the replay corpus; it is not H-10 (`budget-held-open-not-h10`) |
| M1-D-LOOP-3 | B: `DH-T-SUBAGENT-4`, `-14` and the time-limit and interrupt legs of `-3` move to DH-S-29 | Deferred, backed by impl.md's DH-S-29 pointer line; every test kept in M1 passes; impl.md 11.6 re-ticked. See "Stage 17.2" |
| M1-D-LOOP-4 | A: the compaction hook only, with a fake compactor | Built (17.3.1, 17.3.2). See "Stage 17.3" |
| M1-D-LOOP-5 | Confirmed: 500 steps per turn, 8 attempts per step, no time limit | Recorded (17.4.8) in impl.md sec. 9.1 (pointer line, DC-2), [tech-spec.md](../tech-spec.md) sec. 5.5, [design.md](../../../harness/design.md#92-recommended-defaults-applied-not-confirmed-by-the-owner) sec. 9.2 and the comment in `agent/driver/budget.go`; `turn-budget-defaults-s11` is now `in force`. Because the defaults are confirmed, they stay constants; the plan's fallback (a `driver.Config` default with unexported constants, DC-4) is not needed |
| M1-D-LOOP-6 | A: Claude must pass; Copilot is recorded either way (lead settlement) | Applied to the probe, the runbook, the seven scenarios (`forEachRoute`) and the empty M1 table above; nothing is recorded yet |

### Held open

A held-open entry is a part of a deliverable that is ticked as built (or will be) but is
not done: the part that waits is not final, and its step is not done, until the closing
step lands. H-37 is the M1 story's entry for DH-S-11, added by plan step 17.4.7 under
decision M1-D-LOOP-2 A. A *provisional budget* is a limit taken from a measured run: the
benchmark fails when it is missed, but the number is not final until it is pinned at its
closing step. The entry is not H-10, which is the replay of a session with children (lead
settlement `budget-held-open-not-h10`). The other M1 entries are H-24 to H-32
([DH-S-6.md](DH-S-6.md#held-open)), H-34 ([DH-S-7.md](DH-S-7.md#held-open)) and H-33, H-35
and H-36 ([DH-S-8.md](DH-S-8.md#held-open)). impl.md carries a pointer line to this list in
its sec. 10.3 (DC-2); its table is unchanged. The closing stage records the entry it closes.

| ID | Step | Deliverable | The part held open | Gap | Closing step | Status |
| --- | --- | --- | --- | --- | --- | --- |
| H-37 | 11.3 | DH-D-AGENT-3 | The final budget of `DH-T-AGENT-28`: the p99 continuation latency (a waking send on a resumed long session to the first model request leaving the driver), on the heavy-tailed session corpus with the replay provider, also in the memory-limited rerun. Until then the budget is the 100 ms M1-D-LOOP-2 pins, met on the synthetic ladder with the scripted model (33.8 ms at the p99 rung, Windows) | M1-G-LOOP-6 | impl.md 16.7, on the replay corpus (M1-D-LOOP-2 A) | open |

## Definition of done (tech-spec §2.3)

Filled by plan step 17.4.9 at the Phase 17 closing commit `fda4b98`, against the restated
definition of [decisions.md](../decisions.md) sec. 1.1. This is the story's definition; the
story closes only at plan 18.1, and DH-S-11 as a stage closes only if every row holds at
one commit. **Rows 1, 3 and 4 do not hold, so DH-S-11 does not close in Phase 17.**

| Item (decisions.md sec. 1.1) | Holds at `fda4b98`? | Why |
| --- | --- | --- |
| 1. Every impl.md step 1.1 to 11.7 (103 steps, 99 deliverables) ticked *built* under rule 2, and the story plan agrees | **no** | 97 of 103 are ticked. Unticked: 9.1 to 9.3 (they wait for the DH-S-9 check, plan 16.5.9, and the DH-S-9 scenario review) and 11.3, 11.4 and 11.7 (they wait for the M1 check, plan 17.4.3). The story plan agrees for DH-S-11 after plan 17.4.6. Ticks made while the gate was red are provisional and are re-verified at the closure commit (M1-D-CLOSE-1, plan 18.1.8) |
| 2. The gate green on Windows native and Linux under WSL; macOS a scope change, not a pass (OWNER-1, `platforms-windows-wsl`) | **yes**, for this commit | Both lanes `PASSED all 12 gates` at `fda4b98` ([DH-S-1.md, "Phase 17 exit gate runs"](DH-S-1.md#phase-17-exit-gate-runs)). macOS: out of scope (`platforms-windows-wsl`), a scope change, not a pass and not a skip. The closure needs the same at the frozen closure commit (plan 18.1.3), with `trace-tests` wired into the `trace` gate (plan 18.1.4) |
| 3. Every stage DH-S-1 to DH-S-11 has a complete evidence record (impl.md sec. 7.3) | **no** | All eleven read `Closure status: open`; their sec. 7.3 fields are filled at the frozen commit (plan 18.1.7). As stages, DH-S-9 and DH-S-11 are open |
| 4. The M1 manual check recorded on the signed-in Claude route, with no API key; Copilot run and recorded either way | **no** | Not run: the owner runs plan 17.4.3 per [DH-S-11-manual-runbook.md](DH-S-11-manual-runbook.md). The table "M1 record per route" above is empty |
| 5. H-21 listed open with its closing step impl.md 16.10, with every other held-open item a decision creates | holds as listed | H-21 is open in impl.md sec. 10.3 with closing step 16.10, unchanged. This phase adds H-37 ("Held open" above). The final list is written at plan 18.1.11 |

## M1 closure (plan Stage 18.1)

**Closure status: built at the frozen commit `593ceaa`; not yet closed.** Closing waits for
the owner, at plan 18.1.13 and 18.1.14:
- the scenario review in the M1 closure PR (`m1-scenario-review-closure-pr`);
- the signature on `m1-done-claims`;
- the review of `decisions.md`;
- row 4.9 cases (a) and (c), the VS Code saves.

The lanes rerun at the reconciliation commit, the commit that ticks the steps and raises
`built-through`.

### Definition of done at the closure commit (plan 18.1.9)

| Item (decisions.md sec. 1.1) | Holds? | Why |
| --- | --- | --- |
| 1. Every impl.md step 1.1 to 11.7 ticked *built* under rule 2, and the story plan agrees | **yes, except row 4.9** | All 103 impl.md steps are ticked. 9.1 to 9.3, 11.3, 11.4 and 11.7 were ticked after the DH-S-9 and M1 checks passed on the Claude route at `593ceaa`. Step 4.9 stays ticked provisionally (M1-D-CLOSE-1 B) until register row 4.9 cases (a) and (c) are run; if they are not, 4.9 is unticked and `built-through` falls to DH-S-3. The story plan is reconciled by plan 18.1.8 (the table below) |
| 2. The gate green on Windows native and Linux under WSL, with `trace-tests` wired in; macOS a scope change, not a pass | **yes** | Both lanes `PASSED all 12 gates` at `593ceaa`: [win-593ceaa-20261123T2122Z](runs/win-593ceaa-20261123T2122Z.txt) and [wsl-593ceaa-20261123T2122Z](runs/wsl-593ceaa-20261123T2122Z.txt). The `trace` gate ran `trace-tests` (DH-S-1 to DH-S-8 at that marker). With the marker at DH-S-11: required 310, tagged 288, deferred 22, missing 0 ([trace-tests-DH-S-11-593ceaa.txt](runs/manual-593ceaa/trace-tests-DH-S-11-593ceaa.txt)). macOS is out of this story (`macos-return-stage`) |
| 3. Every stage DH-S-1 to DH-S-11 has a complete evidence record (impl.md sec. 7.3) | **yes, except row 4.9** | The eleven sec. 7.3 sections are filled at `593ceaa` (plan 18.1.7), and the 103 register rows are filled; 4.9 (a) and (c) are pending |
| 4. The M1 manual check recorded on the signed-in Claude route, with no key; Copilot run and recorded either way | **yes** | "M1 record per route" above: Claude PASS on both runs; Copilot FAIL, recorded and open (M1-D-LOOP-6) |
| 5. H-21 listed open with its closing step impl.md 16.10, with every other held-open item a decision creates | **yes** | "Open items at M1" below |

### Per-stage reconciliation (plan 18.1.9)

One row per stage at the reconciliation commit. "Story plan" is the state of that stage's boxes in
[implementation-plan.md](../implementation-plan.md) after plan 18.1.8: 141 boxes ticked on
the evidence at `593ceaa` (each verdict and its proof is in the reconciliation files of
`runs/manual-593ceaa/`), 1 marked superseded, and 7 left open.

| Stage | impl.md steps ticked | Story plan | Evidence (lanes at `593ceaa`; register) | State |
| --- | --- | --- | --- | --- |
| DH-S-1 | 9 of 9 | all ticked but "Close DH-S-1" (18.1.14); the key-preflight scenario is marked superseded (`models-signed-in-claude-copilot`) | both lanes pass; register 9 of 9 rows (four seeded cases amended, `register-seeded-amendments`) | built; done claims per "Done at M1" |
| DH-S-2 | 8 of 8 | all ticked but "Close DH-S-2" (18.1.14) | both lanes pass; register 8 of 8 | built |
| DH-S-3 | 9 of 9 | all ticked but "Close DH-S-3" (18.1.14) | both lanes pass; register 9 of 9 | built |
| DH-S-4 | 14 of 14 | all ticked but step 4.9 (row 4.9 (a)/(c) pending) and "Close DH-S-4" (18.1.14) | both lanes pass; register 13 of 14 complete, 4.9 (a) and (c) pending; 4.8 has the known difference M1-G-FND-23.1 | built; 4.9 provisional |
| DH-S-5 | 13 of 13 | all ticked | both lanes pass; register 13 of 13 | built |
| DH-S-6 | 17 of 17 | all ticked | both lanes pass; register 17 of 17 | built; H-24 to H-32 held open |
| DH-S-7 | 6 of 6 | all ticked | both lanes pass; register 6 of 6 | built; H-34 held open |
| DH-S-8 | 7 of 7 | all ticked | both lanes pass; register 7 of 7 | built; H-33, H-35, H-36 held open |
| DH-S-9 | 3 of 3 | all ticked but "Close DH-S-9" (18.1.14, after the scenario review) | both lanes pass; register 3 of 3 (Claude PASS, Copilot open) | built; scenario review pending |
| DH-S-10 | 10 of 10 | all ticked | both lanes pass; register 10 of 10 | built |
| DH-S-11 | 7 of 7 | all ticked but "Close DH-S-11 and the story" (18.1.14) | both lanes pass; register 7 of 7 (Claude PASS, Copilot open) | built; H-37 held open; scenario review pending |

### Done at M1

Plan 18.1.12. The deliverables of impl.md steps 1.1 to 11.7 that M1 claims **done**: each
Done-when line names no `DH-E-*` scenario and no deferred `DH-T` ID, the deliverable is
not held open, and its tagged tests pass on both lanes at `593ceaa` (Windows
`win-593ceaa-20261123T2122Z`, WSL `wsl-593ceaa-20261123T2122Z`). The list is derived by
`evidence/runs/manual-593ceaa/done_at_m1.py` from impl.md, the area specs' Done-when
lines, the trace-tests deferrals and the H-n tables. **Proposed; the owner signs it as
`m1-done-claims` in [decisions.md](../decisions.md).**

| Step | Deliverable | Done-when tests |
| --- | --- | --- |
| 1.1 | DH-D-TOOLCHAIN-1 | DH-T-TOOLCHAIN-11 |
| 1.2 | DH-D-TOOLCHAIN-2 | DH-T-TOOLCHAIN-12, DH-T-TOOLCHAIN-2, DH-T-TOOLCHAIN-4, DH-T-TOOLCHAIN-7, DH-T-TOOLCHAIN-8, DH-T-TOOLCHAIN-9 |
| 2.1 | DH-D-CORDIS-1 | DH-T-CORDIS-1, DH-T-CORDIS-2, DH-T-CORDIS-3 |
| 2.2 | DH-D-CORDIS-2 | DH-T-CORDIS-4 |
| 2.3 | DH-D-CORDIS-3 | DH-T-CORDIS-5, DH-T-CORDIS-6 |
| 2.4 | DH-D-CORDIS-4 | DH-T-CORDIS-7 |
| 2.5 | DH-D-CORDIS-7 | DH-T-CORDIS-12 |
| 2.6 | DH-D-CORDIS-5 | DH-T-CORDIS-8, DH-T-CORDIS-9 |
| 2.7 | DH-D-CORDIS-8 | DH-T-CORDIS-13 |
| 2.8 | DH-D-CORDIS-9 | DH-T-CORDIS-14 |
| 3.1 | DH-D-LOADER-1 | DH-T-LOADER-1 |
| 3.2 | DH-D-LOADER-3 | DH-T-LOADER-3 |
| 3.3 | DH-D-LOADER-4 | DH-T-LOADER-4, DH-T-LOADER-5 |
| 3.4 | DH-D-LOADER-6 | DH-T-LOADER-7 |
| 3.6 | DH-D-LOADER-10 | DH-T-LOADER-11 |
| 3.7 | DH-D-LOADER-8 | DH-T-LOADER-9 |
| 4.1 | DH-D-SDK-1 | DH-T-SDK-1, DH-T-SDK-2, DH-T-SDK-3, DH-T-SDK-4 |
| 4.3 | DH-D-SDK-2 | DH-T-SDK-5, DH-T-SDK-6, DH-T-SDK-7 |
| 4.4 | DH-D-UTIL-4 | DH-T-PICOMATCH-1, DH-T-PICOMATCH-2, DH-T-PICOMATCH-3, DH-T-SEMVER-1, DH-T-SEMVER-2, DH-T-SEMVER-3 |
| 4.5 | DH-D-LLM-11 | DH-T-LLM-1 |
| 4.7 | DH-D-FS-2 | DH-T-DIFF-1, DH-T-FS-4, DH-T-FS-5, DH-T-FS-6 |
| 4.11 | DH-D-BOOT-4 | DH-T-BOOT-9, DH-T-YAML-1, DH-T-YAML-2, DH-T-YAML-3, DH-T-YAML-4, DH-T-YAML-5, DH-T-YAML-6, DH-T-YAML-7 |
| 4.12 | DH-D-LOADER-5 | DH-T-LOADER-6 |
| 4.13 | DH-D-LOADER-12 | DH-T-LOADER-13 |
| 4.14 | DH-D-BOOT-1 | DH-T-BOOT-1, DH-T-BOOT-2, DH-T-BOOT-22, DH-T-BOOT-3 |
| 5.2 | DH-D-SESSION-8 | DH-T-SESSION-17, DH-T-SESSION-18, DH-T-SESSION-6 |
| 5.3 | DH-D-STORE-1 | DH-T-STORE-1 |
| 5.4 | DH-D-STORE-5 | DH-T-STORE-10 |
| 5.10 | DH-D-AGENT-1 | DH-T-AGENT-1 |
| 6.1 | DH-D-UTIL-5 | DH-T-CODEFRAME-1, DH-T-MIME-1, DH-T-MIME-2, DH-T-NODECRYPTO-3, DH-T-UTIL-11, DH-T-UTIL-8, DH-T-UTIL-9 |
| 6.4 | DH-D-BOOT-2 | DH-T-BOOT-4, DH-T-BOOT-5, DH-T-BOOT-6 |
| 6.8 | DH-D-CLI-1 | DH-T-CLI-1, DH-T-CLI-2, DH-T-CLI-3, DH-T-COMMANDER-1, DH-T-COMMANDER-2, DH-T-COMMANDER-3, DH-T-COMMANDER-4 |
| 6.9 | DH-D-CLI-3 | DH-T-CLI-5, DH-T-CLI-6 |
| 7.4 | DH-D-STORE-7 | DH-T-STORE-13 |
| 10.1 | DH-D-TOOLS-1 | DH-T-TOOLS-1, DH-T-TOOLS-18, DH-T-TOOLS-2 |
| 10.7 | DH-D-AGENT-2 | DH-T-AGENT-4 |
| 10.9 | DH-D-HUMAN-6 | DH-T-HUMAN-9 |
| 11.1 | DH-D-AGENT-7 | DH-T-AGENT-23 |
| 11.2 | DH-D-AGENT-10 | DH-T-AGENT-26 |

39 of 99 deliverables.

### Built, not done

Plan 18.1.11. Every other deliverable of steps 1.1 to 11.7 is **built, not done**: it is
recorded "built, done when <the item named> passes in <stage>". A `DH-E` scenario runs
in the stage the acceptance index keys it under; a deferred test passes in the stage its
`later` directive names; a held-open part closes at its H entry's closing step.

| Step | Deliverable | Waits for | Runs in |
| --- | --- | --- | --- |
| 1.6 | DH-D-TOOLCHAIN-3 | names DH-E-TOOLCHAIN-1; deferred DH-T-TOOLCHAIN-1, DH-T-TOOLCHAIN-3, DH-T-TOOLCHAIN-5 | DH-S-17, the deferral's stage |
| 1.8 | DH-D-TOOLCHAIN-4 | names DH-E-TOOLCHAIN-1, DH-E-TOOLCHAIN-2 | DH-S-17, DH-S-18 |
| 1.9 | DH-D-TOOLCHAIN-5 | names DH-E-TOOLCHAIN-2 | DH-S-18 |
| 3.5 | DH-D-LOADER-7 | names DH-E-LOADER-1 | DH-S-17 |
| 3.8 | DH-D-LOADER-11 | names DH-E-LOADER-1 | DH-S-17 |
| 3.9 | DH-D-LOADER-13 | names DH-E-BOOT-2 | DH-S-21 |
| 4.2 | DH-D-UTIL-1 | deferred DH-T-ZOD-1; held open H-38 | the deferral's stage, the H entry's closing step |
| 4.6 | DH-D-FS-1 | names DH-E-NODEFS-1 | DH-S-18 |
| 4.8 | DH-D-FS-3 | names DH-E-RIPGREP-1, DH-E-RIPGREP-2 | DH-S-18 |
| 4.9 | DH-D-UTIL-3 | names DH-E-CHOKIDAR-1, DH-E-CHOKIDAR-2 | DH-S-18 |
| 4.10 | DH-D-LOADER-2 | names DH-E-LOADER-1 | DH-S-17 |
| 5.1 | DH-D-SESSION-1 | names DH-E-SESSION-1 | DH-S-17 |
| 5.5 | DH-D-SESSION-2 | names DH-E-SESSION-2 | DH-S-17 |
| 5.6 | DH-D-SESSION-3 | names DH-E-SESSION-1 | DH-S-17 |
| 5.7 | DH-D-SESSION-4 | names DH-E-SESSION-1 | DH-S-17 |
| 5.8 | DH-D-SESSION-5 | names DH-E-SESSION-2 | DH-S-17 |
| 5.9 | DH-D-LLM-1 | names DH-E-LLM-1 | DH-S-17 |
| 5.11 | DH-D-AGENT-4 | held open H-39 | the H entry's closing step |
| 5.12 | DH-D-HUMAN-1 | names DH-E-HUMAN-2 | not authored yet (impl.md sec. 7.5 places it) |
| 5.13 | DH-D-VIEW-1 | names DH-E-VIEW-2; held open H-40 | not authored yet (impl.md sec. 7.5 places it), the H entry's closing step |
| 6.2 | DH-D-EXEC-1 | names DH-E-EXEC-7; deferred DH-T-EXECA-1; held open H-24 | not authored yet (impl.md sec. 7.5 places it), the deferral's stage, the H entry's closing step |
| 6.3 | DH-D-EXEC-2 | names DH-E-EXEC-1, DH-E-EXEC-10, DH-E-EXEC-2, DH-E-EXEC-3; deferred DH-T-EXEC-17; held open H-25 | DH-S-18, not authored yet (impl.md sec. 7.5 places it), the deferral's stage, the H entry's closing step |
| 6.5 | DH-D-STORAGE-2 | names DH-E-STORAGE-6 | DH-S-18 |
| 6.6 | DH-D-HUMAN-7 | held open H-27 | the H entry's closing step |
| 6.7 | DH-D-BOOT-3 | deferred DH-T-BOOT-23, DH-T-BOOT-8; held open H-28 | the deferral's stage, the H entry's closing step |
| 6.10 | DH-D-HOST-7 | names DH-E-HOST-4 | DH-S-18 |
| 6.11 | DH-D-BOOT-5 | names DH-E-BOOT-1 | DH-S-22 |
| 6.12 | DH-D-BOOT-6 | held open H-21 | the H entry's closing step |
| 6.13 | DH-D-BOOT-7 | names DH-E-BOOT-2, DH-E-LOADER-1 | DH-S-17, DH-S-21 |
| 6.14 | DH-D-STORAGE-8 | names DH-E-STORAGE-5 | not authored yet (impl.md sec. 7.5 places it) |
| 6.15 | DH-D-HOST-4 | names DH-E-HOST-3; deferred DH-T-HOST-17; held open H-29 | not authored yet (impl.md sec. 7.5 places it), the deferral's stage, the H entry's closing step |
| 6.16 | DH-D-UTIL-2 | names DH-E-UTIL-1; deferred DH-T-UNDICI-7, DH-T-UNDICI-9; held open H-30 | DH-S-39, the deferral's stage, the H entry's closing step |
| 6.17 | DH-D-UTIL-6 | deferred DH-T-UTIL-12, DH-T-UTIL-13, DH-T-UTIL-15; held open H-31 | the deferral's stage, the H entry's closing step |
| 7.1 | DH-D-STORE-2 | names DH-E-STORE-1, DH-E-STORE-2; held open H-34 | DH-S-16, the H entry's closing step |
| 7.2 | DH-D-STORE-3 | names DH-E-STORE-1 | DH-S-16 |
| 7.3 | DH-D-STORE-4 | names DH-E-STORE-2 | DH-S-16 |
| 7.5 | DH-D-STORE-8 | names DH-E-STORE-5 | DH-S-35 |
| 7.6 | DH-D-STORE-6 | names DH-E-STORE-6 | DH-S-16 |
| 8.1 | DH-D-STORAGE-1 | names DH-E-STORAGE-1 | DH-S-19 |
| 8.2 | DH-D-VIEW-2 | names DH-E-VIEW-2, DH-E-VIEW-3; held open H-36 | not authored yet (impl.md sec. 7.5 places it), the H entry's closing step |
| 8.3 | DH-D-VIEW-3 | names DH-E-VIEW-2 | not authored yet (impl.md sec. 7.5 places it) |
| 8.4 | DH-D-STORAGE-7 | names DH-E-STORAGE-1 | DH-S-19 |
| 8.5 | DH-D-VIEW-5 | names DH-E-VIEW-3; held open H-35 | not authored yet (impl.md sec. 7.5 places it), the H entry's closing step |
| 8.6 | DH-D-STORAGE-5 | names DH-E-STORAGE-3 | DH-S-39 |
| 8.7 | DH-D-STORAGE-3 | names DH-E-STORAGE-2; held open H-33 | DH-S-23, the H entry's closing step |
| 9.1 | DH-D-LLM-2 | names DH-E-LLM-1, DH-E-LLM-3, DH-E-LLM-5 | DH-S-17, not authored yet (impl.md sec. 7.5 places it) |
| 9.2 | DH-D-LLM-3 | names DH-E-LLM-3 | DH-S-17 |
| 9.3 | DH-D-LLM-6 | names DH-E-LLM-1 | DH-S-17 |
| 10.2 | DH-D-TOOLS-5 | names DH-E-TOOLS-1 | DH-S-17 |
| 10.3 | DH-D-HUMAN-2 | names DH-E-HUMAN-1 | not authored yet (impl.md sec. 7.5 places it) |
| 10.4 | DH-D-TOOLS-2 | names DH-E-TOOLS-1 | DH-S-17 |
| 10.5 | DH-D-TOOLS-3 | names DH-E-TOOLS-3, DH-E-TOOLS-4 | DH-S-39, not authored yet (impl.md sec. 7.5 places it) |
| 10.6 | DH-D-STORAGE-6 | names DH-E-STORAGE-3, DH-E-STORAGE-4 | DH-S-39, not authored yet (impl.md sec. 7.5 places it) |
| 10.8 | DH-D-HUMAN-5 | names DH-E-HUMAN-6 | DH-S-22 |
| 10.10 | DH-D-HUMAN-3 | names DH-E-HUMAN-7 | not authored yet (impl.md sec. 7.5 places it) |
| 11.3 | DH-D-AGENT-3 | names DH-E-AGENT-1, DH-E-AGENT-4; held open H-37 | DH-S-17, the H entry's closing step |
| 11.4 | DH-D-AGENT-5 | names DH-E-AGENT-2 | DH-S-17 |
| 11.5 | DH-D-AGENT-6 | names DH-E-AGENT-3 | DH-S-17 |
| 11.6 | DH-D-SUBAGENT-1 | names DH-E-SUBAGENT-2; deferred DH-T-SUBAGENT-14, DH-T-SUBAGENT-4 | not authored yet (impl.md sec. 7.5 places it), the deferral's stage |
| 11.7 | DH-D-CONTEXT-1 | names DH-E-CONTEXT-1 | not authored yet (impl.md sec. 7.5 places it) |

60 of 99 deliverables.

### Open items at M1 (plan 18.1.11)

M1 closes with these open. None is counted done; each closes at the step named.

- **H-21**: the `DH-D-BOOT-6` part held open in impl.md sec. 10.3, closing at impl.md step 16.10.
- **Built, not done:** the 60 deliverables in "Built, not done" above, each with the `DH-E` scenario or deferred test it waits for and the stage that runs it.
- **Held open, by decision:**
  - H-24 to H-32 ([DH-S-6](DH-S-6.md#held-open); M1-D-BOOT-1 (b), M1-D-BOOT-2 (b); plan 14.6.11), closing in plan Phase 19 and at the deferral stages of M1-D-CLOSE-5;
  - H-34 ([DH-S-7](DH-S-7.md#held-open); plan 15.3.2) and H-33, H-35, H-36 ([DH-S-8](DH-S-8.md#held-open); plans 15.3.9 and 15.3.2);
  - H-37 ("Held open" above; plan 17.4.7), closing at impl.md 16.7;
  - H-38 ([DH-S-4](DH-S-4.md#held-open)) and H-39, H-40 ([DH-S-5](DH-S-5.md#held-open)), from M1-D-CLOSE-5.
- **Deferred tests:** 22 IDs, each backed by an impl.md pointer line. They are DH-T-TOOLCHAIN-1, -3 and -5 (DH-S-16); DH-T-SUBAGENT-4, -14 and legs of -3 (DH-S-29, M1-D-LOOP-3 B); and the 17 IDs of M1-D-CLOSE-5 (DH-S-12, -13, -15 and -16).
- **The Copilot route** (`copilot-cli`), which fails at `593ceaa`: DH-S-9 signed-in settles `llm/client-contract`, M1 exits with status 1, and DH-E-COPILOTCLI-1 is open. It is recorded under M1-D-LOOP-6 A and fixed after M1.
- **Register row 4.9**, cases (a) VS Code on NTFS and (c) a Windows editor saving a file that WSL watches on `/mnt/e`. Both are pending the owner's GUI saves; (a) has been carried since plan 14.5.5.
- **M1-G-FND-23.1**: search lists the files of nested `.worktree/*` Git worktrees, which Git shows as gitlinks (register row 4.8).
- **Partial tags**, recorded as deviations:
  - DH-T-SDK-17 covers only the step 1.1 module-boundary clause; its other clauses are re-checked at 16.6.
  - DH-T-BOOT-18 is proven on `config.Service`; boot builds `State.Config` only when reload is on.
  - DH-T-TOOLCHAIN-7 is proven on the coverage-child fixture; the real replay runner comes at DH-S-16.
- **The scenario review** of the 13 re-authored files, in the M1 closure PR (`m1-scenario-review-closure-pr`), and **the owner's signature** on `m1-done-claims` (plan 18.1.12, 18.1.13).
- **macOS**: out of this story (`macos-return-stage`); never counted as covered.
- **Production composition** of the prompt component and the context pipeline, at DH-S-16.
- **Carried over:** errdiscard (plan 13.5.3, M1-D-TOOL-1c) and the control-group re-record of 14.6.12.

### Closure (Phase 17 record, kept as history)

**Open: DH-S-11 does not close in Phase 17.** Plan step 17.4.9 closes DH-S-11 as a stage only
when every row of its definition holds at one commit. Two of the stage's own conditions are
not met:

1. The M1 manual check (plan 17.4.3) is run by the owner on the signed-in routes and its
   printed lines are recorded in "M1 record per route" above and in the register below
   (`manual`). Claude must pass.
2. The seven retargeted scenario files are reviewed by the owner in the Phase 17 PR, and the
   review line under "Scenarios" names the reviewer and the PR number (plan 17.4.5).

Until then impl.md steps 11.3, 11.4 and 11.7 stay unticked, the story plan's "Close DH-S-11
and the story" step stays unticked, and `built-through` stays at DH-S-8: `CheckBuiltThrough`
requires every step of DH-S-1 to DH-S-n ticked for a marker of DH-S-n. What *is* built: every
DH-S-11 build step's tagged tests pass in both gate lanes at `fda4b98`
(`trace-tests --stage DH-S-11` at `fda4b98`: required 24, tagged 21, deferred 2, missing 1,
`DH-T-CONTEXT-25`; with the directive this change adds, tagged 22 and missing 0), and the
Phase 17 work above.

Closing is then: fill the M1 table and rows 11.1 to 11.4 and 11.7 of the register, record
the review, tick impl.md steps 11.3, 11.4 and 11.7 as built and their story-plan steps
(plan 17.4.6's rule), and raise `built-through` only as `CheckBuiltThrough` allows, which
needs DH-S-9 closed first (plan 16.6.3).

### Operator decisions (pinned 2026-11-22)

History: these were pinned before the M1 check moved to the signed-in routes; the "real
key" below is superseded by `models-signed-in-claude-copilot` (no key), and the follow-up
PR they name is the work of plan Stage 17.4.

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

**At the closure commit (plan 18.1.7).** Commit `593ceaa3efd55d41bf0eeaced5ba7e26acc71966`: one commit for the three modules, on branch `m1-phase-18` over `main` 896c760. Go `go1.27.4` (the go floor). The WSL rehearsal built six `dsh` binaries, each with `vcs.revision=593ceaa…` and `vcs.modified=false`; their SHA-256 values are in [wsl-593ceaa-20261123T2122Z-rehearse.txt](runs/wsl-593ceaa-20261123T2122Z-rehearse.txt).

At closure: not yet recorded (plan 18.1.7), with the client name and version of each
signed-in route. Historical: no commit was recorded.

### Environment and fixture

**At the closure commit (plan 18.1.7).** Windows 11 Enterprise 10.0.28000 native, with `CC` MSYS2 gcc. WSL Ubuntu 24.04.5 LTS, kernel 6.18.58.1-microsoft-standard-WSL2, ext4 clone, gcc 13.3.0. macOS is out of this story (`macos-return-stage`). The manual checks ran on the same workstation (decision `manual-checks-run-by-lead`). Signed-in clients: Claude Code 2.1.312 and GitHub Copilot CLI 1.0.115-7, Windows only. Fixtures: those named by this stage's register rows.

At closure: both local lanes, from their run records; for the M1 check, the operator's
own workstation (Windows, and WSL if run there) with the clients signed in. macOS: out of
scope (`platforms-windows-wsl`). Historical: one Windows machine. Fixtures: the F2
composition with one `probe` tool; the golden `internal/agent/testdata/f2_log.golden`.

### Exact tests and commands

**At the closure commit (plan 18.1.7).** `tools/harness-gate/lanes/win.ps1` and `tools/harness-gate/lanes/wsl.sh` (the gate with `--evidence`; `rehearse` in WSL). `DSH_REQUIRE_SCENARIOS` is unset. For the register: `evidence/runs/manual-593ceaa/run-register.sh` (Windows, and `--os wsl`), with the by-hand steps of `by-hand-checklist.md`; the per-row commands are in this record's register.

At closure: the lane scripts; `go -C src/harness test -race -count=1 ./internal/context/...
./internal/agent/... ./internal/archtest/`; and the probe of plan steps 17.4.1 to 17.4.3,
`go -C src/harness test -tags manual -count=1 -v -run
'TestManualM1OneTurn|TestManualM1CancelMidStream' ./internal/agent/` with `DSH_M1_ROUTE`
set to `claude` and then `copilot`, per [DH-S-11-manual-runbook.md](DH-S-11-manual-runbook.md).
Historical: the DeepSeek runbook above.

### Run identifiers

**At the closure commit (plan 18.1.7).** Windows lane `win-593ceaa-20261123T2122Z` ([record](runs/win-593ceaa-20261123T2122Z.txt)) and WSL lane `wsl-593ceaa-20261123T2122Z` ([record](runs/wsl-593ceaa-20261123T2122Z.txt), [rehearse](runs/wsl-593ceaa-20261123T2122Z-rehearse.txt)). Register logs: `register-windows-593ceaa-20261123T2213Z` and `register-wsl-593ceaa-20261123T2237Z` in [runs/manual-593ceaa/](runs/manual-593ceaa/byhand-results.md). The WSL rehearse record was rebuilt from the lane's tee'd output after the original file was deleted by mistake; the gate record's own `sha256:` line verifies it byte for byte.

At closure: the run-record ids of both lanes, in the runbook's template (plan 18.1.3),
and per route the masked client session ids. WSL lane: not run at the M1 closure commit
yet. Windows lane: not run at the M1 closure commit yet. macOS: out of scope
(`platforms-windows-wsl`). Phase 17 (not the M1 closure commit): `wsl-fda4b98-20261123T1244Z`
and `win-fda4b98-20261123T1244Z`, both `PASSED all 12 gates`
([DH-S-1.md, "Phase 17 exit gate runs"](DH-S-1.md#phase-17-exit-gate-runs)), and the failed
`wsl-1cb9026-20261123T1242Z`, kept as history. The M1 check has no client session id yet:
not run (plan 17.4.3). Historical: no request reached a provider, so no request id exists.

### Results

**At the closure commit (plan 18.1.7).** Both lanes `PASSED all 12 gates`, with `trace-tests` in the `trace` gate. `trace-tests` for DH-S-1 to DH-S-11 reports required 310, tagged 288, deferred 22, missing 0. This stage's register: see "Manual checks register" (every row PASS or recorded as seen; not run and pending rows are open, never a pass).

At closure: not yet recorded. Phase 17: both lanes `PASSED all 12 gates` at `fda4b98`; per
test ID, "Gap closure (plan Phase 17)" above. Historical: "Tests (TC-11a, TC-11b)" above.

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

**Run at the M1 closure commit (plan 18.1.5, 2026-11-23).** The lead Claude Code session ran every row below at `593ceaa3efd55d41bf0eeaced5ba7e26acc71966`, at the owner's instruction of 2026-11-23 ("you should do that"); the owner did not watch these runs. This departs from M1-D-CLOSE-2 A (a named person runs it at the recorded commit, watches it and records it `manual`) and from the runbook rule that signed-in checks are never run from an agent session, and it needs to be recorded as an owner decision. Logs: `register-windows-593ceaa-20261123T2213Z.log`, `register-wsl-593ceaa-20261123T2237Z.log`, `byhand-windows-593ceaa.log`, `byhand-windows-593ceaa-part2.log`, `byhand-wsl-593ceaa.log`, `signedin-dhs9-593ceaa.log`, `signedin-m1-claude-593ceaa.log` and `signedin-m1-copilot-593ceaa.log`. The WSL rehearse record (`wsl-593ceaa-20261123T2122Z-rehearse.txt`) was rebuilt from the lane's tee'd output after the original file was deleted by mistake.

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 11.1 | M1, one prompt with one test tool: each step uses one logged model selection | probe (plan 17.4.3): `TestManualM1OneTurn` per route (`DSH_M1_ROUTE=claude`, then `copilot`); from the redacted `M1 bracketed log`, record the one model selection logged per step and the route and model id | the operator's workstation (Windows; WSL if run there) | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | Claude: PASS (Windows, `signedin-m1-claude-593ceaa.log`; `TestManualM1OneTurn` `--- PASS`): the 19-event bracketed log has one `model/selection` per step, step 1 `{"model":"sonnet","route":"claude-code"}` from `options`, step 2 the same from `header`. Copilot: FAIL (`signedin-m1-copilot-593ceaa.log`): step 1 logged one selection (`claude-sonnet-4.5`, `copilot-cli`), then `turn/end` error `llm/client-contract: copilot-cli: the client exited with status 1 and printed nothing this route can place`; open (M1-D-LOOP-6). | `manual` |
| 11.2 | M1, one prompt with one test tool: the step's tools are fixed once, its calls run in an ordered batch | probe: the same run; record the one `probe` call, its approval and its `tool/result` (`probed: ping`) | the operator's workstation | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | Claude: PASS (`signedin-m1-claude-593ceaa.log`): one `tool/call` `probe` with `{"text": "ping"}`, `approval/asked` then `approval/decided` `allowed-once`, `tool/result` `probed: ping`. Copilot: FAIL (`signedin-m1-copilot-593ceaa.log`): no tool call; the answerer was asked 0 times; open (M1-D-LOOP-6). | `manual` |
| 11.3 | M1, one prompt with one test tool: every turn and step is bracketed and closed | probe: the same run and the cancel run; record that the brackets close in both, one `turn/end` completed and one aborted | the operator's workstation | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | Claude: PASS (`signedin-m1-claude-593ceaa.log`): one-turn log of 19 events, steps 1 and 2 each closed by `step/end`, `turn/end` completed; cancel log of 11 events, `step/end` cause `user`, `turn/end` aborted cause `user`. Copilot: FAIL (`signedin-m1-copilot-593ceaa.log`): the one-turn log (12 events) closed with `step/end` and `turn/end` error `llm/client-contract`; the cancel run failed `no answer text streamed within 2m`; open (M1-D-LOOP-6). | `manual` |
| 11.4 | M1, a second run cancelled mid-stream: the cause on `turn/end` | probe: `TestManualM1CancelMidStream` per route; copy the redacted `M1 cancelled log` and `M1 cancel:` lines: `turn/end` aborted with cause `user`, `step/end` cause `user`, one cancelled settlement | the operator's workstation | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | Claude: PASS (`signedin-m1-claude-593ceaa.log`; `TestManualM1CancelMidStream` `--- PASS`): 11-event cancelled log, one `assistant/message` settlement (seq 8), `step/end` cause `user`, `M1 cancel: turn/end reason={Kind:aborted ... Cause:user ...}`, `interrupted prefix kept (2 bytes)`, session `c7a904a2...`. Copilot: FAIL (`signedin-m1-copilot-593ceaa.log`): `no answer text streamed within 2m`; open (M1-D-LOOP-6). | `manual` |
| 11.5 | rule-3 default: agents are created, resumed and torn down as one transaction | composed test: `go -C src/harness test -count=1 -v ./internal/agent/` (the lifecycle tests of DH-D-AGENT-6); record create, resume and teardown | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): 29 tests `--- PASS`, 0 FAIL, among them `TestRegistryCreateGetList`, `TestCompositionWithAStubFactory`, `TestOwnerUnloadDuringGuardAcquisition`, `TestM1CompletionOracleOnTheF2Turn`, `TestClaimedItemRunsAtMostOnceAcrossACrash`; `internal/agent` ok; no create, resume or teardown lines were printed under `-v`; the lifecycle assertions passed. | `manual` |
| 11.6 | rule-3 default: one delegation seam; every child has one owner | composed test: `go -C src/harness test -count=1 -v ./internal/delegate/...`; plan Stage 17.2 is built (`72704a7`) and plan 17.4.6 re-ticked impl.md 11.6, so the row can run; record the M1 legs of `DH-T-SUBAGENT-1`, `-2`, `-3` and `-9` (the deferred ones are DH-S-29's) | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): 16 tests `--- PASS`, 0 FAIL, among them `TestStartPublishesResultBlocksDisposeIdempotent`, `TestRootDisposalLeavesNoGoroutine`, `TestSettleOrderGolden`, `TestConcurrentSendReleaseResumeSerialised`; the M1 legs of `DH-T-SUBAGENT-1`, `-2`, `-3` and `-9` respectively; `12 sends receipted, 0 refused with agent/closing`; `delegate/subagent` ok. | `manual` |
| 11.7 | M1, one prompt with one test tool: the request rebuilt from the log equals the one sent | probe: the `TestManualM1OneTurn` run; copy the `M1 request N` lines (sent and rebuilt digests at the client-invocation boundary, M1-D-CLOSE-3), `M1 turn/end` and `M1 final reply` | the operator's workstation | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | Claude: PASS (`signedin-m1-claude-593ceaa.log`): `M1 request 1: asOf=7` sent = rebuilt `sha256:f249e334...`, `equal=true`; `M1 request 2: asOf=15` sent = rebuilt `sha256:bbb98262...`, `equal=true`; `invocation-equal=true` for both (args and stdin digests equal); `M1 turn/end` completed; `M1 final reply`: "The probe tool returned: `probed: ping`". No claude or copilot process was left (PID sets equal before and after); the log has no token text (the only token-pattern hits are `first_token` field names). Copilot: FAIL (`signedin-m1-copilot-593ceaa.log`): request 1 sent = rebuilt (`equal=true`), but `turn/end` error `llm/client-contract`; open (M1-D-LOOP-6). | `manual` |

Every printed line passes the `m1Redact` filter of plan step 17.4.2 (account name and
email, `Authorization` and cookie values, token prefixes, token file paths); a second
person reviews the record before it is committed (R-11). A vendor that cannot be reached
is recorded `not run: <client>, <date>, <host>, <error line>`, and the row stays open.

### Cleanup evidence

**At the closure commit (plan 18.1.7).** Every throwaway worktree of the manual checks was removed, and `git status --porcelain` of the main tree was empty before and after. The signed-in runs left no `claude` or `copilot` process (PID sets equal before and after). The WSL clone's tags (7, all created 2026-11-17) were unchanged, and nothing was published.

At closure: the cancelled run leaves no client process alive (a process listing after
row 11.4), and the user's own sign-in is unchanged. Historical: none recorded.

### Open items

**At the closure commit (plan 18.1.7).** For this stage: H-37, the Copilot route (M1-D-LOOP-6), the DH-S-29 deferrals (M1-D-LOOP-3 B), and the scenario review in the M1 closure PR. The M1-wide list is [DH-S-11 "Open items at M1"](#open-items-at-m1-plan-18111).

At closure: plan 18.1.11 lists them, including H-21 with its closing step impl.md 16.10.
Built in Phase 17: Stages 17.1 to 17.3, the probe and the retargeted scenarios of 17.4;
M1-D-LOOP-1 to -5 and `turn-budget-defaults-s11` are now in force. Now open:
- the owner's M1 run (plan 17.4.3) and the table "M1 record per route", with rows 11.1 to
  11.4 and 11.7 of the register;
- the owner's review of the seven retargeted scenario files in the Phase 17 PR, and its PR
  number (plan 17.4.5);
- impl.md steps 11.3, 11.4 and 11.7 and their story-plan steps, unticked until the M1 run;
- the definition-of-done rows 1, 3 and 4 above (plan 17.4.9);
- H-37, the final `DH-T-AGENT-28` budget, closing at impl.md 16.7 ("Held open" above);
- the deferred `DH-T-SUBAGENT-4`, `-14` and legs of `-3`, at DH-S-29 (M1-D-LOOP-3 B);
- the `DH-T-CONTEXT-25` tag, added after `fda4b98`, re-proved at the next lane run;
- the doc comment of `TestTurnBudgetClosesTheTurn` (`agent/driver/driver_test.go`) still
  names `DH-T-AGENT-8`, the replay test's ID; the budget clause it proves is
  `DH-T-AGENT-22`'s, and the comment is corrected in a later code change;
- the DH-S-9 items this stage waits on: the DH-S-9 check (plan 16.5.9) and the DH-S-9
  scenario review in PR #92;
- production composition of the prompt component and the context pipeline (DH-S-16);
- macOS, out of scope (`platforms-windows-wsl`).
