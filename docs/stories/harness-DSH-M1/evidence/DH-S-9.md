# DH-S-9 evidence

Decisions governing this record ([decisions.md](../decisions.md)): `models-signed-in-claude-copilot` with M1-D-PIPE-3 and M1-D-PIPE-4 (the manual check below moves to the signed-in Claude and Copilot routes, so `dh-s-9-closure-path` is superseded), `dh-s-9-scenario-review`, `inherited-vet-gate-failure`, `archtest-llm-edges-exception`, `built-through-may-skip-s8` (superseded), M1-D-CLOSE-1 and M1-D-CLOSE-4, and the open `retro-entry-order-s8-s11`. Logged by gap-closure-plan Stage 12.1; merge commit: recorded when the Stage 12.1 change merges. Added by plan Phase 16: M1-D-PIPE-2 (the driver waits before a retry), M1-D-PIPE-5 (now in force: DeepSeek kept, keyless and fake-tested), M1-D-PIPE-6, -7 and -8 (the client routes), and the lead settlements `dh-d-llm-12-numbering`, `llm-client-contract-code`, `copilot-usage-not-reported` and `rule-6-additions-phase-16`.

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit;
the stage itself is re-closed by plan step 16.6.3, which **has not closed it yet**: plan
Phase 16 built the signed-in client routes and the retry wait (sections "Stage 16.4" and
"Stage 16.5" below, all tests passing in both gate lanes at `f72f435`), but the owner's
signed-in manual check (plan 16.5.9) has not run and the re-authored scenarios await the
owner's review (see [Closure](#closure)). The records before "Stage 16.4" are history. The
fields of impl.md sec. 7.3 and the per-step manual-check register, re-targeted to the
signed-in Claude and Copilot routes, are at the end of this file, in "Evidence record
(impl.md sec. 7.3)"; how to fill them is in [the evidence runbook](README.md).

DH-S-9 is the DeepSeek provider, retry and usage. Stage 9.1 (the Messages provider,
#68/#69) built impl.md step 9.1. Stage 9.2 built steps 9.2 and 9.3 and authored
DH-S-9's three scenarios. **DH-S-9 is not closed yet**: its manual check (below)
has not been run and its scenarios await review in the PR, so impl.md steps 9.1 to
9.3 stay unticked and `built-through` in the acceptance index stays at its baseline
(DH-S-7). See [Closure](#closure) for what remains.

Machine: Windows 11 Enterprise, 13th Gen Intel Core i9-13900K, go1.27.4
windows/amd64; the module's floor is `go 1.26.0`.

## What was built

| Step | Package | What it does |
| --- | --- | --- |
| 9.2 DH-D-LLM-3 | `internal/llm` (`retry.go`) | `RetryPolicy` (`Mode` bounded or persistent, `MaxRetries`, `BaseDelay`, `MaxDelay`, `RetryAfterCap`, `MaxElapsed`, `Jitter`), `DefaultRetry`, `PersistentRetry`, `Validate`, `Normalised`, and the pure `Next(failure, attempt, elapsed, u)`: the wait before the next attempt, or the `Abandon` reason (`not-retryable`, `exhausted`, `elapsed`, `step-cap`, `cancelled`). Of the four failure classes (`Cancelled`, `Transient`, `Repairable`, `Permanent`) only `Transient` retries (`llm/rate-limit`, `llm/server` and the transport codes are Transient); a `Permanent` failure (`llm/auth` among them) or a `Repairable` one never does, and a `Cancelled` one is abandoned. A Retry-After is honoured exactly, up to `RetryAfterCap`; otherwise the delay is exponential with jitter, capped at `MaxDelay`. A route carries its policy (`Route.Retry`; the zero policy means no retry); the DeepSeek route defaults to `DefaultRetry`. |
| 9.2 buffered one-shot | `internal/llm` (`runtime.go`) | `Runtime.Complete` (auxiliary requests: titles, compaction) retries outside the adapter on the runtime's clock (`WithTimer`, `WithJitter`): each attempt is its own prepared call on the route, model and policy the first attempt captured (a `Registration.Replace` during the call reaches none of them, nor extends its bound), and settles once (`assistant/attempt` for a failed one, `assistant/message` for the last success), the wait is taken from `Next`, a cancel during the wait ends with `llm/aborted`. It never changes route: SP-2. |
| 9.2 streamed turns | `internal/llm/retry` | The `AttemptFailed` listener for streamed turns: decides from the settled failure and the route's policy on the session clock, logs `llm/retry` (scheduled, with the wait and the reason), `llm/retry-started` and `llm/retry-abandoned` (with `reason`), and binds every listener to the step's attempt cap. A `Permanent` failure is terminal: no inner listener is asked, and `agent.AskAttemptFailed` turns any listener's `Retry` of one into `GiveUp` (SP-2). Persistent elapsed time counts from the step's first attempt start (`AttemptFailure.StepStarted`); a persistent policy's point without it fails with `retry.ErrNoStepStart` instead of measuring from the first failure (a bounded policy needs no start). |
| 9.3 DH-D-LLM-6 | `internal/llm/meter` | Three registered folds and the `tokenMeter` service (`meter.Key`), as DH-ADR-LLM-9 describes. `tokenUsage` (`llm/token-usage`, `UsageView`): input, cache read, cache write and output totals, the attempts counted and the last reported sample, folded from `assistant/attempt` and `assistant/message` settlements. `contextBreakdown` (`llm/context-breakdown`, `BreakdownView`): one price per system prompt the surface retains and their total; every surface replacement removes the prices of the system prompts in its replaced range, whatever its own type, while a source it only cites outside that range (provenance) keeps its price; the range is found by an order key per retained node (an appended node's key is its seq, a replacement's node takes the key of the first node it replaced, kept as an anchor while it is retained), so a range that starts at a compaction's summary covers the earlier-seq nodes after it; a blank system prompt (which leaves no node) is not priced. The fold's state stays bounded: it keeps at most `MaxAnchors` (16) anchors; a rewrite that would need more marks it `Approximate` (sticky, views at version 3), after which it keeps no prices and no anchors at all (a guessed order could keep the price of a system prompt the surface removed), and the service reads the system prompts from the session's surface (their one owner) instead, so neither fold grows with the number of messages or rewrites. `contextPressure` (`llm/context-pressure`, `PressureView`): the last `assistant/message` sample (prompt, output, `requestAsOf`, digest), the heuristic price of nodes appended since and the system prompts' price change since, so a system-prompt replacement is repriced at once; `Projected` and `Fraction(window)`. The `Meter` service (`New(registry, Config)`, `Config.Calibrate` default true): `Measure(ctx, session, header)` prices the session's current surface, read from the session at the folds' cut, and calibrates it by the last sample's reported prompt over the heuristic's price of that same request envelope (the nodes up to its `requestAsOf` under the header then in force), and once a replacement or a header change rewrites that envelope it keeps scaling the new one by the last such ratio (token-meter.4). The runtime records on every `assistant/attempt` and `assistant/message` settlement (payload version 3, optional `estimate`, identity upgrade) the heuristic's price of the exact request it sent (`EstimateRequest`), so the `contextPressure` fold refreshes the ratio at every sample, after any number of compactions, as reported prompt over that same request's estimate; settlements without an estimate fall back to the fold's running price; `Pressure(ctx, session)` (the projection, or a measurement after a compaction or once the breakdown is approximate); `EstimateMessage`. The token estimate (`EstimateText`, `EstimateBytes`, `EstimateMessage`, `EstimateHeader`, `Calibration`, `Calibrated`) is what the DH-S-10 result budget reads. |

## Tests (TC-09a)

Run with `go -C src/harness test -count=1 -v ./internal/llm/ ./internal/llm/retry/ ./internal/llm/meter/`:

| Scenario (implementation-plan Stage 9.2) | Test | Result |
| --- | --- | --- |
| Overloaded retried within bound (DH-T-LLM-7) | `llm` `TestCompleteRetriesOverloadedWithinBound`; `retry` `TestBoundedRetryOnTheVirtualClock` | pass. The overloaded-with-retry-after fixture twice, then success, on the virtual clock: three settlements (`assistant/attempt`, `assistant/attempt`, `assistant/message`), two waits each equal to the fixture's `Retry-After: 2`, three requests and no fourth; with `MaxRetries: 2` and the fixture three times the call fails with the last overloaded failure after three settlements, three requests and two waits. |
| Bad key never retried (SP-2) | `llm` `TestCompleteNeverRetriesABadKey`; `retry` `TestPermanentFailureIsNeverRetried`; `agent` `TestAttemptFailed` | pass: exactly one `assistant/attempt` on `deepseek-official` with failure `llm/auth` (class `permanent`), one request, no wait and no other route; an inner listener that would retry it is never asked, and a listener's `Retry` of `llm/auth` becomes `GiveUp`. |
| Buffered one-shot calls (DH-T-LLM-8) | `llm` `TestCompleteSinkOncePerAttempt`, `TestCompleteHandsOneRecordToTheCaller` | pass: one record per attempt, one prepare per attempt, one base-delay wait; a cancel during the wait ends with `llm/aborted`. |
| Policy | `llm` `TestRetryPolicyNext`, `TestCompleteKeepsTheCaptureAcrossAReplace`; `retry` `TestRetryAfterCapAndPersistentElapsed`, `TestPersistentElapsedCountsTheFirstAttempt`, `TestPersistentPolicyRequiresTheStepStart`, `TestCancelDuringTheWaitIsAbandoned`, `TestStepCapBindsEveryListener`, `TestRecordFailureFailsClosed`, `TestListenAndPointValidation` | pass: a Retry-After of 7 s waits exactly 7 s, an over-long one waits the cap; persistent mode stops at `MaxElapsed`, counted from the first attempt's start, and a persistent point without that start fails explicitly with nothing recorded; a replacement during the wait leaves the captured route, model and bound; a failed record fails the decision closed. |
| Usage folded from settlements (DH-T-LLM-15) | `meter` `TestUsageFoldedFromSettlements`, `TestSystemPromptReplacementIsRepricedAtOnce`, `TestSystemPricesFollowTheSurface`, `TestMeterMeasuresTheSession`, `TestBreakdownStaysBoundedUnderRewrites`, `TestEstimateEveryBlockKind` | pass: three settlements with known counts fold to the expected totals; the projected context is 167 (15 + 140 + 12) from the sample at seq 4; the estimate rounds up at four characters (runes) a token; a system-prompt replacement moves the projection by its price change before the next sample, and a checkpoint between them folds on equal; an empty system replacement, a user compaction over a later system prompt and a system replacement over a range each leave exactly the prices of the system nodes the surface itself retains, as do a replacement and a compaction that cite a retained system prompt outside their range and ranges that start or end at a replacement's node; on a real session the service measures the surface, calibrates by 2.0 when the provider reported twice the heuristic's price of the request, keeps the factor 2.0 after a compaction and after a header change (a sample of the old envelope does not recalibrate), recalibrates to 3.0 on a fresh request whose settlement carries its estimate and keeps 3.0 after a second compaction (a checkpoint at every cut folds equal), stays at 1 with calibration off, and pressure then measures instead of projecting; `TestBreakdownStaysBoundedUnderRewrites`: `MaxAnchors` singleton replacements stay exact against the surface; 200, with and without system prompts, leave the fold approximate and empty, and the pressure state within a constant bound; five cycles of a range whose ends are rewritten nodes past the bound (a system replacement over a compacted span that removes a system prompt) keep no stale price and no growth, and on a real session the service then prices exactly the system prompts the surface retains. Coverage: `llm/retry` 92.6 %, `llm/meter` 85.7 %. |

The Stage 9.1 tests in the same packages (`TestDeepSeek*`, `TestStream*`,
`TestPrepare*`) were rerun with them and pass.

## Manual check (impl.md DH-S-9)

> **Re-targeted (plan 12.4.4; `models-signed-in-claude-copilot`, M1-D-CLOSE-3).** The
> DeepSeek procedure below is historical: it needs an API key, which M1 no longer uses,
> and its decision `dh-s-9-closure-path` is superseded. The check now runs on the
> signed-in Claude and Copilot routes, and "bad key" becomes "signed-out or expired
> session". The procedure of record is the register under "Evidence record (impl.md
> sec. 7.3)" at the end of this file.

**Not run on this machine:** no DeepSeek key was available to this run. It is
`manual` and never a gate criterion (EV-6). Procedure, for the operator with a
key supplied at run time (`DEEPSEEK_API_KEY`, the env name behind
`secret://deepseek/api-key`): send one streamed request on `deepseek-official`
with the good key and one with a bad key; expect one `assistant/message`
settlement for the first, and exactly one `assistant/attempt` with failure
`llm/auth` and no `llm/retry` event for the second. Record the request ids here.

The DeepSeek probe that scripted this check (`manual_deepseek_test.go`,
`TestManualDeepSeekGoodKey` and `TestManualDeepSeekBadKey`) was **removed by plan step
16.5.6** (`d67d483`). Its replacement is the signed-in probe
`src/harness/internal/llm/manual_signedin_test.go` (`TestManualSignedIn`, build tag
`manual`, skipped unless `DSH_MANUAL_SIGNEDIN=1`), and the procedure of record is
[the DH-S-9 manual-check runbook](DH-S-9-manual-runbook.md). Nothing in the DeepSeek
procedure is run any more.

Attempted from this machine on 2026-11-21: the network refuses the TLS handshake
to `api.deepseek.com` (`curl` exit 35; the probe saw `llm/transport: remote
error: tls: handshake failure` and stopped at the default policy's bound). No
request reached DeepSeek, so this is not an observation of either case and the
table stays unfilled.

| Request | Expected | Observed |
| --- | --- | --- |
| good key | one `assistant/message`, usage present | not run |
| bad key | one `assistant/attempt`, `llm/auth`, no retry, no other route | not run |

Owner (operator decision `dh-s-9-closure-path` = A, 2026-11-21, now superseded by
`models-signed-in-claude-copilot`): the operator was to run both requests from a network
that reaches `api.deepseek.com`, with the key supplied at run time. This table is
history and will not be filled; the signed-in table under "Stage 16.5" replaces it.

## Scenarios

`DH-E-LLM-1`, `DH-E-LLM-3` and `DH-E-VIEW-1` authored, for review in this PR, and
indexed to DH-S-17 with authoring stage DH-S-9 and `requires: [DH-S-11, DH-S-16]`;
**none executed.**

Re-authored (gap-closure-plan 16.5.8, M1-D-CLOSE-4 A): the three files no longer
use DeepSeek or any API key. They run on the user's signed-in client routes of
`llm.md` DH-D-LLM-12, one subtest per route (`claude-code` must pass, `copilot-cli`
is run and recorded either way, M1-D-LOOP-6 A), with "signed out" (the client's
configuration home pointed at an empty temporary directory) replacing "bad key".
The route helpers are `signedin_routes_test.go` and `signedin_routes_copilot_test.go`;
the Copilot leg names its route only by ID, and its signed-out recipe and code stay
open until `DH-E-COPILOTCLI-1` (plan 16.5.9) pins them (marked TODO in that file).
Authored and compiled only; **none executed.**

Review (operator decision `dh-s-9-scenario-review` = A): the re-authored files are
reviewed in a PR before the closure is ticked.

Review: reviewer: the owner, in the Phase 16 PR, pending.

| Scenario | File | Test |
| --- | --- | --- |
| DH-E-LLM-1 (a turn with reasoning and a tool call on a signed-in client route) | `src/harness/internal/testkit/scenarios/llm_turn_scenario_test.go` | `TestTurnWithReasoningAndToolCall` |
| DH-E-LLM-3 (a failure is a fact: no retry, no fallback when the client is signed out) | `src/harness/internal/testkit/scenarios/llm_signed_out_scenario_test.go` (was `llm_bad_key_scenario_test.go`) | `TestSignedOutIsAFact` (was `TestBadKeyIsAFact`) |
| DH-E-VIEW-1 (a real model titles a session; a signed-out title route leaves the fallback) | `src/harness/internal/testkit/scenarios/view_title_scenario_test.go` | `TestModelTitlesSession` |

Each starts the built `dsh` through the test kit's launcher (`dsh [--layer f] sdk`)
and speaks only `src/sdk/protocol` and `src/sdk/jsonrpc`; the shared helper is
`sdkhost_test.go`. Their imports are limited to the standard library, `src/sdk/...`
and `internal/testkit/launcher`: the gate's scenario-index check
(`acceptance.CheckBoundary`) refuses any other import in a file whose entry has a
`requires` list, and `TestAuthoredScenariosAreIndexedAndCompileButDoNotRun` holds
the helpers to the same rule. The DH-S-17 preflight (`acceptance.Preflight`) refuses
them while `built-through` is below DH-S-16: run against this checkout, the DH-S-17
preflight refuses each of the three as "requires DH-S-11, DH-S-16, not built (built
through DH-S-7)" (`TestPreflight*` in `tools/harness-gate/acceptance`).

Gate evidence: `go -C src/harness vet -tags scenario ./...` exits 0; the default
`go test -list . ./internal/testkit/scenarios/...` lists no test;
`GOWORK=off go -C tools/harness-gate test ./...` passes except for
`TestRepositoryPassesEveryGate`, which fails at its vet gate on files this stage does
not change:
- `src/go/cordis`;
- in `src/harness`: `session/scope`, `util/caltime`, `util/fsx`, `util/glob`,
  `util/recur`, `session/event/event_test.go`, `util/version` and
  `util/watch/watchtest`.

The findings are QG-4 (time.Sleep), QG-5 (goroutine without t.Cleanup) and QG-6
(missing fuzz seeds). This branch leaves the analyzers unchanged. The operator
decided (`inherited-vet-gate-failure` = A) to fix this in a follow-up workstream, not
here.

Superseded by M1 gate repair (Phase 13): the follow-up workstream is
[gap-closure-plan Phase 13](../gap-closure-plan.md#phase-13-gate-green), which fixes the
findings above and the agent-family debt added after this stage (M1-G-GATE-27; the
files are named in [DH-S-11.md](DH-S-11.md)), with its evidence in
[DH-S-1.md, "M1 gate repair"](DH-S-1.md#m1-gate-repair). This record is historical.

## Stage 16.4: retry decided by the policy, waited by the driver (P1 work closed early)

**P1 work closed early.** DH-D-LLM-3's retry wait is P1 (before DH-S-16) in the plan, and
was pulled forward to Phase 16 because "a signed-out client is never retried" means
nothing unless a retry listener that could retry is mounted. Commit `06032ed`; owner
decision M1-D-PIPE-2 A. Every test below passed in both gate runs at `f72f435`
([DH-S-1.md, "Phase 16 exit gate runs"](DH-S-1.md#phase-16-exit-gate-runs)).

What changed. The retry component decides and appends `llm/retry`, then returns
`agent.Retry{After}`; the driver waits on the session clock and the step context, then
sends the new observe-only notice `agent.RetryWaited` (`RetryWaitEnded{Agent, Turn, Step,
Attempt, Cancelled}`, event key `agent/retry-wait-ended`), from which the component
appends `llm/retry-started`, or `llm/retry-abandoned` "cancelled". A cancel during the wait
ends the step with the cancel cause, not the provider's failure. The route's own policy
reaches the component through `AttemptFailure.RoutePolicy` (from `Prepared.Retry()`), so its
`RetryAfterCap`, mode, jitter and elapsed cap apply. The F2 rig now mounts `retry.Listen`,
`Grants.Bind` and the spill policy.

| Test ID | Tests (package) | What they show | Result |
| --- | --- | --- | --- |
| `DH-T-LLM-7` (retagged and adapted) | `TestBoundedRetryOnTheVirtualClock`, `TestPermanentFailureIsNeverRetried`, `TestRetryAfterCapAndPersistentElapsed`, `TestCancelDuringTheWaitIsAbandoned`, `TestStepCapBindsEveryListener`, `TestPersistentElapsedCountsTheFirstAttempt`, `TestPersistentPolicyRequiresTheStepStart`, `TestRoutePolicyIsTheDefault`, `TestWaitOfAnotherRetryIsNotRecorded`, `TestDriverAttemptBudgetIsAStepCap` (`llm/retry`) | the policy under virtual time: bounds, classes, the Retry-After cap, persistent elapsed time from the step's first attempt, the step cap over every listener, the route's policy as the default, and a cancel during the wait abandoned | pass |
| `DH-T-LLM-7` (driver) | `TestRetryComponentDecidesAndTheDriverWaits`, `TestCancelDuringRetryComponentWaitEndsWithTheCause` (`agent/driver`, `retrywait_test.go`) | the component decides and the driver waits; a cancel during the wait ends the step with cause `user` | pass |
| `DH-T-LLM-7` (F2) | `TestFlowF2RetriedWithinTheRouteCap` (`agent`) | Scenario "a streamed turn is retried within the route's cap": overloaded with `Retry-After: 7` and a route cap of 5 s, then success, on the virtual clock; the log holds `assistant/attempt`, `llm/retry{delay_ms:5000}`, `llm/retry-started` and `assistant/message`, the driver waited exactly 5 s, and the bracketed log equals the new golden `f2_retry_log.golden` | pass |
| `DH-T-LLM-7` (F2) | `TestFlowF2BadCredentialIsNeverRetried` (`agent`) | Scenario "a bad credential on a streamed turn": with a scripted `llm/auth` failure, exactly one `assistant/attempt`, then `llm/retry-abandoned{not-retryable}`, no `llm/retry` and no other route | pass |

**Deviations from the plan's text** (recorded, not hidden):
- The retry golden `f2_retry_log.golden` is a reply-only variant of F2 (no tool call).
- The driver's own attempt budget became a step cap: when it binds, the component logs
  `llm/retry-abandoned{step-cap}`.
- A failed `llm/retry-started` write no longer stops the retry.

**The `llm.md` correction.** The DH-D-LLM-3 "How it is implemented" line of
[llm.md](../../../harness/areas/llm.md#dh-d-llm-3) (the plan's `llm.md:435`) now says the
policy returns `Retry{After}`, the driver waits and sends `agent.RetryWaited`, and the
component writes the wait's outcome from it. `agent.RetryWaited` is also added to the
DH-D-AGENT-4 event table of [agent-loop.md](../../../harness/areas/agent-loop.md#dh-d-agent-4).

## Stage 16.5: the signed-in client routes

Commits `4021aea` (16.5.1 batch opt-in, 16.5.2 route design), `7976094` (the Claude route
and the shared package), `593059d` (a staticcheck fix), `16590ae` (the Copilot route),
`42f2c38` (16.5.7 relabel, 16.5.8 scenarios), `d67d483` (16.5.6 manual probe). The design
is [llm.md](../../../harness/areas/llm.md#dh-d-llm-12) DH-D-LLM-12 and DH-ADR-LLM-10,
[libs/claude-code-cli.md](../../../harness/libs/claude-code-cli.md) (DH-ADR-CLAUDECODECLI-1,
`DH-T-CLAUDECODECLI-1` to `-4`, `DH-E-CLAUDECODECLI-1`) and
[libs/github-copilot-cli.md](../../../harness/libs/github-copilot-cli.md)
(DH-ADR-COPILOTCLI-1, `DH-T-COPILOTCLI-1` to `-4`, `DH-E-COPILOTCLI-1`).

**Every client fixture is synthetic.** The adapter tests replay lines written from the
vendors' documents and help output, through a re-executed stand-in client; none is a
recording of a real client. The 16.5.9 run replaces them with scrubbed recordings.

### Adapter tests

Every test below passed in both gate runs at `f72f435`.

| Test ID | Tests (package) | What they show | Result |
| --- | --- | --- | --- |
| `DH-T-NODEPROC-2`, `DH-T-CROSSSPAWN-1` (16.5.1) | `TestBatchToolByBareNameWithCheckedArguments` (`world/subprocess`, Windows), `TestBatchRules`, `TestBatchPolicyOffWindows` | Scenario ".cmd client started by bare name with checked arguments": with the opt-in, plain arguments arrive unchanged; with `%` the start is refused and a marker proves nothing ran. The proof needs the Windows lane, and it ran there | pass |
| `DH-T-LLM-18` | `TestProbe`, `TestProbeBatchShim`, `TestLocate`, `TestEnv`, `TestRender`, `TestInvocation`, `TestRun`, `TestRunCancelStopsTheTree`, `TestTapped` (`llm/provider/clientroute`) | the shared package: the version probe, the locator on the child's `PATH`, the scrubbed environment, the pure renderer, the runner and the invocation record | pass |
| `DH-T-LLM-19` | `TestListingServesToolsAndRefusesCalls`, `TestListingRefusesBadInvocations` (`llm/provider/clientroute/listing`) | the listing-only MCP server lists the tools and refuses every `tools/call` | pass |
| `DH-T-LLM-20` (`DH-T-CLAUDECODECLI-1`) | `TestClaudeCodeSettlesOnce`, `TestClaudeCodeNoAmbientCredential`, `TestClaudeCodeRefusedBeforeInput`, `TestClaudeCodeRoute`, `TestArgs` (`llm/provider/claudecode`) | Scenario "a client route settles once" (one `assistant/message` with usage; the logged invocation digest equals the one rebuilt from the log) and Scenario "no ambient credential reaches the client" (`ANTHROPIC_API_KEY`, `GH_TOKEN` and the other removed names are absent in the client); a too-old client is refused before input | pass |
| `DH-T-LLM-21` (`DH-T-CLAUDECODECLI-2`) | `TestClaudeCodeStreamMapping`, `TestMapperFinishes`, `TestMapperChunks` | the stream mapping, including a tool call returned unexecuted (M1-D-PIPE-6 deny and stop) | pass |
| `DH-T-LLM-22` (`DH-T-CLAUDECODECLI-3`) | `TestClaudeCodeFailureMapping`, `TestMapperInit`, `TestClassify`; `TestSignedInRedaction` (`llm`) | the failure mapping: signed out is `llm/sign-in-required`, Permanent, one `assistant/attempt`, never retried by the route's policy, and a second registered route is never prepared; a contract breach is `llm/client-contract`; a token planted in the client's output never reaches a failure, and the probe's mask catches the token shapes | pass |
| `DH-T-LLM-23` (`DH-T-CLAUDECODECLI-4`) | `TestClaudeCodeCancelStopsTheTree` | cancellation stops the client and the listing server and removes the attempt directory | pass |
| `DH-T-LLM-24` (`DH-T-COPILOTCLI-1`) | `TestCopilotSettlesOnce`, `TestCopilotNoAmbientCredential`, `TestCopilotRefusedBeforeInput`, `TestCopilotBatchShim`, `TestCopilotRoute`, `TestArgs` (`llm/provider/copilot`) | as for Claude, and the `.cmd` shim started through the batch opt-in | pass |
| `DH-T-LLM-25` (`DH-T-COPILOTCLI-2`) | `TestCopilotStreamMapping`, `TestMapperChunks`, `TestMapperToolRequests` | the stream mapping; the tree stops at the first tool request | pass |
| `DH-T-LLM-26` (`DH-T-COPILOTCLI-3`) | `TestCopilotFailureMapping`, `TestMapperFailsClosed`, `TestMapperFailures`; `TestSignedInRedaction` (`llm`) | signed out is Permanent, one `assistant/attempt`, never retried, no second route prepared; its code is `llm/client-contract` until the 16.5.9 recording pins the signed-out line | pass |
| `DH-T-LLM-27` (`DH-T-COPILOTCLI-4`) | `TestCopilotCancelStopsTheTree` | cancellation, as for Claude | pass |

**Scenario "a signed-out client is a fact, not a retry": proven in part.** The gate proves
the route side: signed out settles as one Permanent `assistant/attempt`, the route's policy
does not retry it, and the second route is never prepared (`DH-T-LLM-22`, `-26`). The full
form, with the retry component mounted and logging `llm/retry-abandoned{not-retryable}`,
is in `TestManualSignedIn` (`manual`, not run) and in `DH-E-LLM-3` (authored, not run).
The Copilot code is `llm/client-contract`, not yet `llm/sign-in-required`.

**Deviations from the plan's text.** `cmd.exe` comes from the system directory, not
`%ComSpec%` ([node-process-builtins.md](../../../harness/libs/node-process-builtins.md),
"As built"); the 8191-unit command-line limit is enforced (`subprocess/line-too-long`);
`LookupProgram` is new. The plan's `DH-D-LLM-17` is `DH-D-LLM-12`
(`dh-d-llm-12-numbering`). The client routes' `Prepare` probes the installed client's
version, so it does I/O; the `llm.Adapter` doc comment now says so. `Runtime.Prepare`'s
doc comment (`llm/runtime.go`) still says "it does no I/O" and is left for a later code
change.

### 16.5.7 and 16.5.8

- **16.5.7 (M1-D-PIPE-5 A).** `deepseek-official` is labelled "keyless, fake-tested; not
  proven against a real API (OWNER-2)" in `llm.md` DH-D-LLM-2 and the package doc. The
  `DEEPSEEK_API_KEY` instructions are removed from `agent/m1_manual_test.go`; its retarget
  is plan 17.4.1.
- **16.5.8.** DH-E-LLM-1, DH-E-LLM-3 (`TestSignedOutIsAFact`) and DH-E-VIEW-1 are
  re-authored per route (`claude-code` required, `copilot-cli` recorded), compiled, and
  not run (section "Scenarios" above). The trace ledger's title of DH-E-LLM-3 now reads
  "when the client is signed out".

**Scenario review line.** Reviewer: the owner, in the Phase 16 PR (its number is recorded
here when the PR is opened). Status: **pending**. Decision `dh-s-9-scenario-review` = A
requires this review before the closure is ticked.

### Recorded decisions

| Decision | Choice in force | Where it shows |
| --- | --- | --- |
| M1-D-PIPE-3 | A: dsh drives the user's installed `claude` and Copilot CLI as child processes; the client signs in, dsh never reads a token | DH-ADR-LLM-10 (a); the environment scrub (`DH-T-LLM-20`, `-24`) |
| M1-D-PIPE-4 | A: the guarantee holds at the client-invocation boundary; the client's own additions are opaque | DH-ADR-LLM-10 (b); the invocation digest rebuilt from the log (`DH-T-LLM-20`, `-24`) |
| M1-D-PIPE-5 | A: DeepSeek kept, keyless, fake-tested, "not proven against a real API" | 16.5.7 |
| M1-D-PIPE-6 | Deny and stop: a listing-only MCP server; tool calls return unexecuted to the guarded pipeline; the full MCP bridge is deferred (DH-ADR-LLM-10) | `DH-T-LLM-19`, `-21`, `-25` |
| M1-D-PIPE-7 | Copilot's saved sessions are left in the user's Copilot home, documented, with each attempt's session id logged | the runbook's "Clean up" step; `DH-T-LLM-24` |
| M1-D-PIPE-8 | The Copilot CLI, not the SDK | `llm/provider/copilot` |

Lead settlements of the same phase: `dh-d-llm-12-numbering`, `llm-client-contract-code`,
`copilot-usage-not-reported` (the Copilot route reports no usage at M1) and
`rule-6-additions-phase-16` ([decisions.md](../decisions.md) sec. 4).

### Manual check (16.5.9): not run yet

**Not run yet: the owner runs 16.5.9.** The procedure is
[the DH-S-9 manual-check runbook](DH-S-9-manual-runbook.md). This table is left empty until
the owner pastes the four printed lines; nothing here is evidence yet, and Stage 16.5
stays open until it is filled (`manual`).

| Case | Printed line | Pass? |
| --- | --- | --- |
| `DH-S-9 claude signed-in` | | |
| `DH-S-9 claude signed-out` | | |
| `DH-S-9 copilot signed-in` | | recorded either way |
| `DH-S-9 copilot signed-out` | | recorded either way; `llm/sign-in-required` check open |

The open inferences this run settles:
- **Claude:** the hidden flags `--max-turns` and `--system-prompt-file`; the
  `apiKeySource` value of a signed-in run; that the init tool list is only `mcp__dsh__*`
  (if Claude lists built-ins, every attempt fails `llm/client-contract`); the denial field
  names; the signed-out output; the `type: stdio` field of the MCP configuration.
- **Copilot:** the event schema; the signed-out line; standard input as the prompt
  source; what `--available-tools` does; whether an empty `COPILOT_HOME` signs the client
  out; and whether usage can be read (`copilot-usage-not-reported`).

## Closure

**Open: DH-S-9 does not close in Phase 16.** Plan step 16.6.3 closes DH-S-9 only when both
of these are done, and neither is:

1. The signed-in manual check (plan 16.5.9) is run by the owner and its four lines are
   recorded in the table above (`manual`).
2. The re-authored scenario files are reviewed by the owner in the Phase 16 PR, and the
   review line above is filled with the reviewer and the PR number.

Until then impl.md steps 9.1 to 9.3 stay unticked, the story plan's "Close DH-S-9" step
stays unticked, and `built-through` stays at DH-S-8: `CheckBuiltThrough` requires every
step of DH-S-1 to DH-S-n ticked for a marker of DH-S-n, so neither DH-S-9 nor DH-S-10 can
be the marker while 9.1 to 9.3 are open. What *is* built: every DH-S-9 build step's tagged
tests pass in both gate lanes at `f72f435` (`trace-tests --stage DH-S-9`: required 6,
tagged 6, missing 0), plus the client routes and the retry wait above.

Closing is then: fill the table, record the review, tick impl.md steps 9.1, 9.2 and 9.3
as built, tick "Close DH-S-9" in the implementation plan, and raise `built-through` to
DH-S-10, since DH-S-10 is already closed as built (`DH-S-10.md`, "Closure"); the
`built-through-may-skip-s8` decision stays superseded, so the marker moves only in order.
The archtest's narrow exception for the `llm/retry` to `agent` and `llm/meter` to `views`
edges is kept, by operator decision `archtest-llm-edges-exception`.

## Evidence record (impl.md sec. 7.3)

These are the eight fields of [impl.md sec. 7.3](../../../harness/impl.md#73-the-evidence-record),
in the format of [the evidence runbook](README.md#the-evidence-record-headings). Plan step
18.1.7 fills them at the frozen M1 closure commit; plan Stages 16.5 and 16.6 add the
signed-in route record and the re-closure before that. Until then each field points to
the history above, which stays exactly as it was recorded and is not closure evidence
(M1-D-CLOSE-1).

### Source and build identity

At closure: not yet recorded (plan 18.1.7), with the client name and version of each
signed-in route (`claude --version`, `copilot --version`). Historical: no commit was
recorded.

### Environment and fixture

At closure: both local lanes, from their run records; for the manual check, the
operator's own workstation (Windows native or WSL, recorded per line) with Claude Code
and the Copilot CLI installed and signed in. macOS: out of scope
(`platforms-windows-wsl`). Historical: the Windows machine named at the top of this file.

### Exact tests and commands

At closure: the lane scripts; `go -C src/harness test -race -count=1 ./internal/llm/...`;
and the probe of plan step 16.5.6, `go -C src/harness test -tags manual -count=1 -v -run
TestManualSignedIn ./internal/llm/` with `DSH_MANUAL_SIGNEDIN=1`. Historical: the
DeepSeek probe commands above.

### Run identifiers

At closure: the run-record ids of both lanes, in the runbook's template (plan 18.1.3),
and per signed-in case the settlement seq and the masked client request or session id.
WSL lane: not run at the M1 closure commit yet. Windows lane: not run at the M1 closure
commit yet. macOS: out of scope (`platforms-windows-wsl`). Phase 16 (not the M1 closure
commit): `wsl-f72f435-20261123T1041Z` and `win-f72f435-20261123T1041Z`, both `PASSED all 12
gates` ([DH-S-1.md, "Phase 16 exit gate runs"](DH-S-1.md#phase-16-exit-gate-runs)). The
signed-in manual check has no settlement seq or session id yet: not run (plan 16.5.9).
Historical: no DeepSeek request was made, so no request id exists.

### Results

At closure: not yet recorded. Phase 16: both lanes `PASSED all 12 gates` at `f72f435`;
per test ID, "Stage 16.4" and "Stage 16.5" above. Historical: "Tests (TC-09a)" above.

### Manual checks

impl.md DH-S-9 names one check: one streamed request with a good key and one with a bad
key; one attempt event each, and no retry on the bad key. Under
`models-signed-in-claude-copilot` and M1-D-CLOSE-3 (in force) it runs on the user's
signed-in Claude and Copilot clients, with no API key, and "bad key" becomes "signed-out
or expired session": exactly one `assistant/attempt` settlement with
`llm/sign-in-required` (or `llm/auth`), no retry and no fallback. It covers steps 9.1
and 9.2; step 9.3 takes the rule-3 default. The probe is built by plan step 16.5.6
(`d67d483`) and run by step 16.5.9 under
[the DH-S-9 manual-check runbook](DH-S-9-manual-runbook.md) (the procedure of
[plan Stage 16.5](../gap-closure-plan.md#stage-165-signed-in-client-routes-and-the-dh-s-9-manual-check),
made concrete); rows 9.1 and 9.2 are not run yet. Claude must pass; Copilot is run and recorded either way
(M1-D-LOOP-6, lead settlement). How to run and fill the register:
[the evidence runbook](README.md#the-signed-in-route-checks-dh-s-9-and-dh-s-11).

#### Manual checks register

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 9.1 | one streamed request per signed-in client: one settlement with usage | probe (plan 16.5.6): `claude --version` and `copilot --version`, then `TestManualSignedIn` with `DSH_MANUAL_SIGNEDIN=1`; copy the `DH-S-9 claude signed-in:` and `DH-S-9 copilot signed-in:` lines: one `assistant/message` with usage, the route id, the model id the client reported, the masked request id. Claude must pass; Copilot is recorded either way | the operator's workstation (Windows or WSL, recorded) | | | | `manual` |
| 9.2 | a signed-out or expired session: one settlement, no retry, no fallback | probe (plan 16.5.6): the signed-out cases of `TestManualSignedIn`, run against an empty temporary configuration home; copy the `DH-S-9 claude signed-out:` and `DH-S-9 copilot signed-out:` lines: exactly one `assistant/attempt` with `llm/sign-in-required` (or `llm/auth`), class permanent, 0 `llm/retry` events, 0 attempts on the second registered route | the operator's workstation (Windows or WSL, recorded) | | | | `manual` |
| 9.3 | rule-3 default: token usage and context occupancy are read from the log | composed test: `go -C src/harness test -count=1 -v -run 'TestUsageFoldedFromSettlements\|TestMeterMeasuresTheSession' ./internal/llm/meter/`; and the usage of the 9.1 settlement | one lane | | | | `manual` |

Every printed line passes the mask and the token-shape filter of plan step 16.5.6, and a
second person reviews the record before it is committed (R-11). A vendor that cannot be
reached is recorded `not run: <client>, <date>, <host>, <error line>`, and the row stays
open.

### Cleanup evidence

At closure: the empty temporary configuration home of the signed-out cases is removed,
and the user's own sign-in is unchanged (the client still reports signed in afterwards).
Historical: none recorded.

### Open items

At closure: plan 18.1.11 lists them. Built in Phase 16: the signed-in routes and the
probe (plan Stage 16.5); M1-D-PIPE-5 is now in force. Now open:
- the owner's signed-in run (plan 16.5.9) and its open inferences ("Stage 16.5", "Manual
  check (16.5.9): not run yet"), with the synthetic fixtures it replaces;
- the owner's review of the re-authored scenarios DH-E-LLM-1, DH-E-LLM-3 and DH-E-VIEW-1
  (plan 16.5.8, `dh-s-9-scenario-review`);
- the Copilot signed-out code (`llm/client-contract` until 16.5.9 pins it) and Copilot
  usage (`copilot-usage-not-reported`);
- `Runtime.Prepare`'s doc comment still says "no I/O" (the `llm.Adapter` comment is
  amended);
- production composition of the routes and the retry listener (DH-S-16);
- the closure items under "Closure" above.
