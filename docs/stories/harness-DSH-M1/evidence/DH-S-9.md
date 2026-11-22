# DH-S-9 evidence

Decisions governing this record ([decisions.md](../decisions.md)): `models-signed-in-claude-copilot` with M1-D-PIPE-3 and M1-D-PIPE-4 (the manual check below moves to the signed-in Claude and Copilot routes, so `dh-s-9-closure-path` is superseded), `dh-s-9-scenario-review`, `inherited-vet-gate-failure`, `archtest-llm-edges-exception`, `built-through-may-skip-s8` (superseded), M1-D-CLOSE-1 and M1-D-CLOSE-4, and the open M1-D-PIPE-5 and `retro-entry-order-s8-s11`. Logged by gap-closure-plan Stage 12.1; merge commit: recorded when the Stage 12.1 change merges.

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit;
the stage itself is re-closed by plan step 16.6.3. The records below are history. The
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

The check is scripted, outside every gate, by the `manual`-tagged probe
`src/harness/internal/llm/manual_deepseek_test.go` (the default build and the gate
never compile it). With the key in the environment only:

```
$env:DEEPSEEK_API_KEY = '<key>'
go -C src/harness test -tags manual -count=1 -v -run TestManualDeepSeek ./internal/llm/
```

`TestManualDeepSeekGoodKey` streams one request and requires exactly one
`assistant/message` with usage. `TestManualDeepSeekBadKey` uses a key-shaped
non-key and requires exactly one `assistant/attempt` (`llm/auth`, permanent) on
`deepseek-official`, no `llm/retry` event and no attempt on a second registered
route. Each prints a `DH-S-9 good key:` or `DH-S-9 bad key:` line (seq, code,
status, masked request id) for the table below.

Attempted from this machine on 2026-11-21: the network refuses the TLS handshake
to `api.deepseek.com` (`curl` exit 35; the probe saw `llm/transport: remote
error: tls: handshake failure` and stopped at the default policy's bound). No
request reached DeepSeek, so this is not an observation of either case and the
table stays unfilled.

| Request | Expected | Observed |
| --- | --- | --- |
| good key | one `assistant/message`, usage present | not run |
| bad key | one `assistant/attempt`, `llm/auth`, no retry, no other route | not run |

Owner (operator decision `dh-s-9-closure-path` = A, 2026-11-21): the operator runs
both requests from a network that reaches `api.deepseek.com`, with the key supplied
at run time, and replaces each `not run` cell with the printed `DH-S-9 ... :` line,
which gives the settlement seq, code, status and masked request id. This stays a
manual check (EV-6) and is never a gate criterion.

## Scenarios

`DH-E-LLM-1`, `DH-E-LLM-3` and `DH-E-VIEW-1` authored, for review in this PR, and
indexed to DH-S-17 with authoring stage DH-S-9 and `requires: [DH-S-11, DH-S-16]`;
**none executed.**

Review (operator decision `dh-s-9-scenario-review` = A): a separate reviewer
approves the three files below in this branch's PR. The approval is recorded here
with the PR number and reviewer before the closure is ticked. Pending: no PR has
been opened for the branch yet.

| Scenario | File | Test |
| --- | --- | --- |
| DH-E-LLM-1 (a turn with reasoning and a tool call on the vendor's protocol) | `src/harness/internal/testkit/scenarios/llm_turn_scenario_test.go` | `TestTurnWithReasoningAndToolCall` |
| DH-E-LLM-3 (a failure is a fact: no retry, no fallback on a bad key) | `src/harness/internal/testkit/scenarios/llm_bad_key_scenario_test.go` | `TestBadKeyIsAFact` |
| DH-E-VIEW-1 (a real model titles a session; a failing provider leaves the fallback) | `src/harness/internal/testkit/scenarios/view_title_scenario_test.go` | `TestModelTitlesSession` |

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

## Closure

**Open.** DH-S-9 closes only when both of these are done; until then impl.md steps
9.1 to 9.3 stay unticked, the plan's "Close DH-S-9" step stays unticked and
`built-through` stays at DH-S-7:

1. The manual check above is run with a real key and its table filled in.
2. The three scenario files are reviewed in this stage's PR.

Closing is then: tick impl.md steps 9.1, 9.2 and 9.3 as built, tick "Close DH-S-9"
in the implementation plan, and raise `built-through` to DH-S-9. The operator had
approved raising it to DH-S-9 even if DH-S-8's own closure has not landed yet; that
decision (`built-through-may-skip-s8`) is superseded by the index rule of
`DH-S-10.md` "Closure", so the marker is raised only in order. Until DH-S-9 closes it
stays at DH-S-7. The archtest's narrow exception for the
`llm/retry` to `agent` and `llm/meter` to `views` edges is kept, by operator
decision `archtest-llm-edges-exception`.

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
WSL lane: not run. Windows lane: not run. macOS: out of scope (`platforms-windows-wsl`).
Historical: no DeepSeek request was made, so no request id exists.

### Results

At closure: not yet recorded. Historical: "Tests (TC-09a)" above.

### Manual checks

impl.md DH-S-9 names one check: one streamed request with a good key and one with a bad
key; one attempt event each, and no retry on the bad key. Under
`models-signed-in-claude-copilot` and M1-D-CLOSE-3 (in force) it runs on the user's
signed-in Claude and Copilot clients, with no API key, and "bad key" becomes "signed-out
or expired session": exactly one `assistant/attempt` settlement with
`llm/sign-in-required` (or `llm/auth`), no retry and no fallback. It covers steps 9.1
and 9.2; step 9.3 takes the rule-3 default. The probe is built by plan step 16.5.6 and
run by step 16.5.9 under the runbook of
[plan Stage 16.5](../gap-closure-plan.md#stage-165-signed-in-client-routes-and-the-dh-s-9-manual-check);
these rows cannot run before it. Claude must pass; Copilot is run and recorded either way
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

At closure: plan 18.1.11 lists them. Now open: the signed-in routes and the probe (plan
Stage 16.5); the re-authored scenarios DH-E-LLM-1, DH-E-LLM-3 and DH-E-VIEW-1 and their
review (plan 16.5.8, `dh-s-9-scenario-review`); M1-D-PIPE-5 (the DeepSeek provider,
`open`); the closure items under "Closure" above.
