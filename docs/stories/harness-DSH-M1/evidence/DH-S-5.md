# DH-S-5 evidence

Stages 5.1 and 5.2 (impl.md steps 5.1 to 5.8: the typed event registry, the scope
layers, the store contract with memstore and its conformance suite, event
versioning, the live session, the core folds, the rebuilt request and the
brackets) were built by the earlier workstreams of this phase. Stage 5.3 (steps 5.9
to 5.13) closes DH-S-5. Every result below was re-run with all thirteen
deliverables applied.

## Stage 5.3 -- Model Runtime, Agent Contract and Registries (closes DH-S-5)

### What changed

| Step | Deliverable | Package (`src/harness/internal/`) |
| --- | --- | --- |
| 5.9 | `DH-D-LLM-1` | `llm`: the runtime. `Register`/`Replace` routes (one id each, refused while served), `Prepare` (route, model, effort and option validation with the closed `llm/*` codes, defaults materialised), `Prepared.Stream` and `Complete`. The runtime holds no session writer. Every attempt ends with exactly one `Finish` chunk and one `Attempt` record (route, model, purpose, `RequestAsOf`, `RequestDigest`, message, finish, usage, timing). An adapter error, panic, missing finish or cancellation becomes that Finish and that record. The record goes to the caller's `AttemptSink` exactly once, and `Attempt.Draft()` is the caller's one settlement event: `assistant/message` on success, `assistant/attempt` otherwise. Streams are assembled by block (`BlockEnd` is authoritative); a route's `MaxConcurrent` cap is honoured. |
| 5.10 | `DH-D-AGENT-1` | `agent`: `Ref{Session, Incarnation}`, the `Agent` view, `Registry` (`Create`, `Get`, `Resolve`, `List`, `SetFactory`) under `agent.Agents`. `Create` returns the one owning `Handle`: its disposal, or its owner fiber's unload, disposes the agent. Readers get a view with no `Close`. One factory seat is held as an effect. `Resolve` tells a stale incarnation from an unknown one. `WithInitiator`/`Initiator` address the calling agent. Lifecycle notices (`Created`, `Disposed`, `StatusChanged`, `Failed`, `StepDecided`) are scope events dispatched through the agent's scope key. |
| 5.11 | `DH-D-AGENT-4` | `agent`: three `AroundKey` steering points with sealed decisions. `PreStep` gives `Enter{Messages, StartsSeries}` or `Reject{By, Reason}`; `AttemptFailed` gives `Retry{After, Reason}` or `GiveUp{}`; `OnTurnStopping` gives `AllowStop{}` or `KeepOpen{Messages, Reason}`, where any KeepOpen wins in every registration order. `ProposeStep` is point 4. Enter appends `step/start`. Reject appends `turn/end` `blocked` with the reason and no `step/start`. A listener's error, panic or nil/unknown decision appends `turn/end` `error` with `extension/failed`. Observers (`Notify`) are contained and logged and never change an outcome. |
| 5.12 | `DH-D-HUMAN-1` | `human`: one interaction registry. There is one answerer per channel and one binding per agent (a child binds `Via` its creator), both cordis effects. `Ask` delivers to the root's channel and blocks; a single compare-and-swap settles each item exactly once. A second answer gets `interaction/settled`; an unknown id gets `interaction/unknown`; another channel gets `interaction/not-yours`. If nothing is bound, or no answerer is registered, the asker gets `unavailable` at once without delivery. Cancellation settles `cancelled`. Unbinding, unloading the answerer or `Close` settles items `unavailable`. `Pending(agent)` lists by agent. |
| 5.13 | `DH-D-VIEW-1` | `session/views`: the fold registry, implementing `session.Folder`. `Register[S]` takes a typed `Definition` once: a duplicate key is refused, and registering after first use is `view/sealed`. Dispatch is by declared event type. A batch's fold stays pending until the session's position shows the batch committed, so a failed append is never folded. `Read` returns every requested fold at the session's one `AsOf` cut. A session the registry has not followed is folded from its log at that cut. A panicking fold is `view/failed` for that session only. Checkpoint rows are DH-D-VIEW-2 and are not written yet. |

Supporting edits:
- `session/scope` exports `Filter(key)`, the listener filter the agent scopes use.
- `archtest` gains three rules:
  - only the driver package imports the driver (DH-T-AGENT-27);
  - `session` imports none of `llm`, `agent` or `human`;
  - `llm` imports none of `agent`, `human` or `session/views`.

### Tests

```text
$ go test -race -count=1 -cover <packages>           # from src/harness
ok  .../internal/llm             coverage: 85.4% of statements
ok  .../internal/agent           coverage: 88.1% of statements
ok  .../internal/human           coverage: 87.1% of statements
ok  .../internal/session/views   coverage: 89.9% of statements
ok  .../internal/session/scope   coverage: 84.8% of statements
ok  .../internal/archtest        coverage: 96.5% of statements
```

`go test -race -count=1 ./...` passes for every package of `src/harness`; `gofmt -l`
and `go vet ./...` are clean.

| Scenario (implementation-plan.md Stage 5.3) | Test |
| --- | --- |
| One attempt, one caller-logged settlement (`DH-T-LLM-2`, TC-05) | `TestStreamOneAttemptOneSettlement` (`llm/runtime_test.go`). A fake route fails once, then succeeds, against a memstore session. Each attempt gives exactly one Finish and one record carrying `RequestAsOf`/`RequestDigest`. The store holds one settlement event per attempt, every append made by the caller, and the runtime made zero appends. `TestCompleteHandsOneRecordToTheCaller` does the same for `Complete` with an auxiliary request. Also covered: `TestStreamNormalisesEveryEnding` (error, panic, missing finish, cancel, early stop, unknown reason, empty response), `TestStreamAssemblesBlocks` (an interrupted attempt keeps text and drops every tool call, ended or not), `TestPrepareRoutesAndValidates`, `TestRouteConcurrencyCap`, `TestPreparedCallIsSingleUse` (a second Stream, sequential or 8 concurrent, is refused `llm/invalid-request` with one record and no provider attempt), `TestStreamRefusesAConfigMismatch` (a request whose controls, defaults materialised, differ from the prepared config is refused before the adapter), `TestSlotHeldUntilTheProviderExits` (a cancelled attempt settles at once, but its route slot is held until the provider iterator exits). Interception (`llm.StreamAround`, `llm/intercept_test.go`): `TestStreamAroundServesASubstitutedStream` (`Serve(stream, "replay")` bypasses the provider; the record and its `assistant/message` carry `served_by`), `TestStreamAroundServedStreamIsNormalised` (a served failure, error, missing finish, panic or unknown reason gets the same single Finish and `assistant/attempt`), `TestStreamAroundCannotReroute` (`DH-T-LLM-4`: `next` takes no request and each listener gets its own copy; an outer listener that edits route, model, controls and request and then calls `next` leaves an inner listener that serves without calling `next` seeing the original, and with two editing listeners the provider still gets the original on r/a), `TestStreamAroundServedStreamEndsWithTheAttempt` (a served iterator blocked on `StreamRequest.Ctx` exits when a background-context consumer stops after one chunk; exactly one aborted record settles), `TestStreamAroundListenerFailures`, `TestStreamAroundServedTakesNoSlot`. |
| Rejected PreStep closes the turn blocked; observers cannot veto (`DH-T-AGENT-10`) | `TestPreStepRejectAndPanic` (`agent/steering_test.go`). `Reject{Reason: "quota"}` appends no `step/start`, and the `turn/end` is `blocked` with the reason. A panicking `StepDecided` observer is logged and changes nothing. A `PreStep` listener that panics, or that returns an error, ends the turn `error` with `extension/failed`. Also: `TestPreStepEnterReplacesTheBatch`, `TestTurnStoppingKeepOpenWinsInEveryOrder` (`DH-T-AGENT-13`), `TestAttemptFailed`. |
| Agent contract (`DH-T-AGENT-1`, `-2`, `-3`, `-27`) | `agent/agent_test.go`: `TestRegistryCreateGetList`, `TestInitiatorAndResolve`, `TestEventsAreScopedPerAgent`, `TestCompositionWithAStubFactory`, plus the archtest rule. |
| Folds read at one cut (`DH-D-VIEW-1`, TC-05) | `TestFoldsReadAtOneCut` (`session/views/views_test.go`). Two folds (event count and next seq) are read while four goroutines append. Every read's values equal its `AsOf` across at least 20 distinct cuts, under `-race`. A read-only open folds from the log to the same values. Also: `TestRegistrationIsOnceAndBeforeUse`, `TestDispatchByTypeAndContainedPanic`, `TestFailedAppendIsNotFolded`. |
| Pending interaction is addressable by agent (`DH-D-HUMAN-1`) | `TestPendingIsAddressableByAgent` (`human/human_test.go`). Agents A and B each have a pending approval. `Pending(A)` returns only A's. Answering it resolves A's ask, and B's stays pending until `Close`. Also: `TestConcurrentAnswersOneWins` (`DH-T-HUMAN-1`: 8 racing answers, 1 wins, 7 get `interaction/settled`, ×50), `TestCancelUnbindUnloadAndRefusals` (`DH-T-HUMAN-2` subset), `TestUnloadingTheCarrierSettlesItsItems`, `TestFailingAnswererIsUnavailable`, `TestMintedIDsSkipCallerSuppliedOnes` (a minted id never reuses a pending or settled caller-supplied id), `TestDeliveryContextEndsWhenTheItemSettles` (the context given to `Deliver` is cancelled on answer, unbind, unload and `Close`). |

## DH-S-5 exit checks

### Conformance suite on memstore

```text
$ go test -count=1 ./internal/session/store/memstore/ -run 'TestConformance$' -v
--- PASS: TestConformance (0.00s)
ok  github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore
```

### Persisted-type test

```text
$ go test -count=1 ./internal/session/store/ -run 'TestInventory|TestPersistedRowsDecode' -v
--- PASS: TestInventory (0.00s)
--- PASS: TestInventoryUpdatePath (0.00s)
--- PASS: TestInventoryCatchesUnbumpedChange (0.00s)
--- PASS: TestInventoryRefusesRemovalAndDowngrade (0.00s)
--- PASS: TestPersistedRowsDecode (0.00s)
ok  github.com/smartpcr/agent-plugin/src/harness/internal/session/store
```

`assistant/message` and `assistant/attempt` went to version 2 for the optional `served_by` (`llm.StreamAround`). Their payloads are decoded strictly, so an older binary could not read a row that sets the new field, and the bump cannot be a same-version "compatible" note. Instead:
- Upgrade 1 is the identity: a v1 row has no `served_by`, which means the provider served it.
- `persisted.notes` records the `1->2` lines, and `persisted.golden` was re-recorded with `-update`.
- `testdata/rows.jsonl` gained one v2 row of each type.
- An older binary refuses v2 rows with the typed newer-payload error.

### Manual rebuilt-request check

`TestRequestRebuild` (`session/request_test.go`) takes a live session's
`NextRequest` and rebuilds it with `surface.Rebuild` from the stored log at the
request's `AsOf`. It checks that the rebuilt request has the same digest, then
rebuilds again after more appends and checks that the digest and `AsOf` are
unchanged. This is the record that `requestAsOf`/`requestDigest` on every
settlement event point at.

```text
$ go test -count=1 ./internal/session/ -run TestRequestRebuild -v
=== RUN   TestRequestRebuild
--- PASS: TestRequestRebuild (0.00s)
ok  github.com/smartpcr/agent-plugin/src/harness/internal/session
```

## Deviations, recorded

- `Prepared.Stream(ctx, req, sink)` takes the caller's `AttemptSink` beside the request, so that the single record has one delivery path for both `Stream` and `Complete`.
- `Complete` uses the route's first (default) model, because `session.Request` carries a route and no model.
- The interaction registry lives at `internal/human`, as the plan names it, not at the area doc's `internal/human/interact`.
- `agent.StepDecided` is an added observe-only notice that makes the point-4 outcome observable.
- `ProposeStep` lives in `agent` until the driver (C27) lands.
- These are deferred to the driver stage:
  - `Resume`;
  - composer swap;
  - stream frames (`DH-T-AGENT-14`).
