# DH-S-9 evidence

DH-S-9 is the DeepSeek provider, retry and usage. Stage 9.1 (the Messages provider,
#68/#69) built impl.md step 9.1. Stage 9.2 built steps 9.2 and 9.3, authored DH-S-9's
three scenarios and closes DH-S-9: this stage ticks impl.md steps 9.1 to 9.3 as
built and raises `built-through` in the acceptance index to DH-S-9.

Machine: Windows 11 Enterprise, 13th Gen Intel Core i9-13900K, go1.27.4
windows/amd64; the module's floor is `go 1.26.0`.

## What was built

| Step | Package | What it does |
| --- | --- | --- |
| 9.2 DH-D-LLM-3 | `internal/llm` (`retry.go`) | `RetryPolicy` (`Mode` bounded or persistent, `MaxAttempts`, `BaseDelay`, `MaxDelay`, `RetryAfterCap`, `MaxElapsed`, `Jitter`), `DefaultRetry`, `PersistentRetry`, `Validate`, `Normalised`, and the pure `Next(failure, attempt, elapsed, u)`: the wait before the next attempt, or the `Abandon` reason (`not-retryable`, `exhausted`, `elapsed`, `step-cap`, `cancelled`). Only `Transient` and `RateLimited` failures retry; a `Permanent` failure (`llm/auth` among them) or a `Repairable` one never does. A Retry-After is honoured exactly, up to `RetryAfterCap`; otherwise the delay is exponential with jitter, capped at `MaxDelay`. A route carries its policy (`Route.Retry`; the zero policy means no retry); the DeepSeek route defaults to `DefaultRetry`. |
| 9.2 buffered one-shot | `internal/llm` (`runtime.go`) | `Prepared.Complete` (auxiliary requests: titles, compaction) retries outside the adapter on the runtime's clock (`WithTimer`, `WithJitter`): each attempt is prepared afresh and settles once (`assistant/attempt` for a failed one, `assistant/message` for the last success), the wait is taken from `Next`, a cancel during the wait ends with `llm/aborted`. It never changes route: SP-2. |
| 9.2 streamed turns | `internal/llm/retry` | The `AttemptFailed` listener for streamed turns: decides from the settled failure and the route's policy on the session clock, logs `llm/retry` (scheduled, with the wait and the reason), `llm/retry-started` and `llm/retry-abandoned` (with `reason`), and binds every listener to the step's attempt cap. |
| 9.3 DH-D-LLM-6 | `internal/llm/meter` | The usage fold under `llm/token-usage`: input, cache read, cache write and output totals and the attempts counted, folded from `assistant/attempt` and `assistant/message` settlements; `Occupancy` (the last settled request's input plus cache tokens plus output: what the context holds now) and `Pressure` against a window; the token estimate (`EstimateText`, `EstimateMessage`, `Calibration` from settled counts) the DH-S-10 result budget reads. |

## Tests (TC-09a)

Run with `go -C src/harness test -count=1 -v ./internal/llm/ ./internal/llm/retry/ ./internal/llm/meter/`:

| Scenario (implementation-plan Stage 9.2) | Test | Result |
| --- | --- | --- |
| Overloaded retried within bound (DH-T-LLM-7) | `llm` `TestCompleteRetriesOverloadedWithinBound`; `retry` `TestBoundedRetryOnTheVirtualClock` | pass. The overloaded-with-retry-after fixture twice, then success, on the virtual clock: three settlements (`assistant/attempt`, `assistant/attempt`, `assistant/message`), two waits each equal to the fixture's `Retry-After: 2`, three requests and no fourth; with a two-attempt bound the call fails with the last overloaded failure after two settlements. |
| Bad key never retried (SP-2) | `llm` `TestCompleteNeverRetriesABadKey`; `retry` `TestPermanentFailureIsNeverRetried` | pass: exactly one `assistant/attempt` on `deepseek-official` with failure `llm/auth` (class `permanent`), one request, no wait and no other route. |
| Buffered one-shot calls (DH-T-LLM-8) | `llm` `TestCompleteSinkOncePerAttempt`, `TestCompleteHandsOneRecordToTheCaller` | pass: one record per attempt, one prepare per attempt, one base-delay wait; a cancel during the wait ends with `llm/aborted`. |
| Policy | `llm` `TestRetryPolicyNext`; `retry` `TestRetryAfterCapAndPersistentElapsed`, `TestCancelDuringTheWaitIsAbandoned`, `TestStepCapBindsEveryListener` | pass: a Retry-After of 7 s waits exactly 7 s, an over-long one waits the cap; persistent mode stops at `MaxElapsed`. |
| Usage folded from settlements (DH-T-LLM-15) | `meter` `TestUsageFoldedFromSettlements` | pass: three settlements with known counts fold to the expected totals; occupancy is 167 (15 + 140 + 12) at seq 4; the estimate rounds up at four bytes a token. |

The Stage 9.1 tests in the same packages (`TestDeepSeek*`, `TestStream*`,
`TestPrepare*`) were rerun with them and pass.

## Manual check (impl.md DH-S-9)

**Not run on this machine:** no DeepSeek key was available to this run. It is
`manual` and never a gate criterion (EV-6). Procedure, for the operator with a
key supplied at run time (`DEEPSEEK_API_KEY`, the env name behind
`secret://deepseek/api-key`): send one streamed request on `deepseek-official`
with the good key and one with a bad key; expect one `assistant/message`
settlement for the first, and exactly one `assistant/attempt` with failure
`llm/auth` and no `llm/retry` event for the second. Record the request ids here.

| Request | Expected | Observed |
| --- | --- | --- |
| good key | one `assistant/message`, usage present | not run |
| bad key | one `assistant/attempt`, `llm/auth`, no retry, no other route | not run |

## Scenarios

`DH-E-LLM-1`, `DH-E-LLM-3` and `DH-E-VIEW-1` authored, for review in this PR, and
indexed to DH-S-17 with authoring stage DH-S-9 and `requires: [DH-S-11, DH-S-16]`;
**none executed.**

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
through DH-S-9)" (`TestPreflight*` in `tools/harness-gate/acceptance`).

Gate evidence: `go -C src/harness vet -tags scenario ./...` exits 0; the default
`go test -list . ./internal/testkit/scenarios/...` lists no test;
`GOWORK=off go -C tools/harness-gate test ./...` passes.

## Closure

impl.md steps 9.1, 9.2 and 9.3 are ticked as built; `built-through` is DH-S-9.
