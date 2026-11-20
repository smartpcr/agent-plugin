# DH-S-2 evidence

## Stage 2.1 -- Final Unload and Fiber Cancellation

### Manual check: the port's example output (TC-02b)

| Field | Value |
| --- | --- |
| Kind | `manual` (golden comparison) |
| Change | impl.md steps 2.1 (`DH-D-CORDIS-1`) and 2.2 (`DH-D-CORDIS-2`) |
| Toolchain | `go version go1.27.4 windows/amd64` |
| Date | 2026-11-19 |

From `src/go`, with the stage's changes applied and the committed golden untouched:

```text
$ go test ./cmd/cordis-sample/ -race -count=1 -run TestOutputMatchesGolden -v
=== RUN   TestOutputMatchesGolden
--- PASS: TestOutputMatchesGolden (0.16s)
PASS
```

`src/go/cmd/cordis-sample/testdata/output.golden` was not re-recorded: the sample's
output is unchanged, so there are no intended diffs to note. The sample never creates
an effect from a cleanup or the root's teardown, disposes a handle twice concurrently,
races an acquisition against an unload, disposes a fiber from a creation handler, or
reads a fiber lifetime, which are the only behaviours the changes alter. Re-checked
after the second iteration (root teardown, acquisition tracking, creation ordering):
still passes unchanged. Re-checked again after the third iteration (acquisition-safe
notification delivery, root `Unloading`/`Disposed` state) and the fourth (notification
dispatcher with a delivery barrier in `Wait`/`Dispose`) and the fifth (serialised
dispatcher, caller-delivering barrier) and the sixth (creation notification starts the
load, bounded barrier) and the seventh (barriers deliver on fresh goroutines, creation-order
announcement) and the eighth (strictly ordered dispatcher with parked deliveries, no
barrier stealing) and the ninth (validated goroutine identity for parked deliveries) and the tenth (an
ambiguous identity attributes no delivery):
still passes unchanged.

### Tests

```text
$ go test ./... -race -count=5          # from src/go
ok  github.com/smartpcr/agent-plugin/src/go/cmd/cordis-sample
ok  github.com/smartpcr/agent-plugin/src/go/cordis
ok  github.com/smartpcr/agent-plugin/src/go/loader
ok  github.com/smartpcr/agent-plugin/src/go/timer
```

| Test | In `src/go/cordis/unload_test.go` |
| --- | --- |
| `DH-T-CORDIS-1` | `TestCleanupCannotRegisterWhileItsFiberUnloads`, `TestRootDisposeIsFinalAndExclusive`, `TestRootLifecycleHandlersCannotBlockTeardown`, `TestHandlerWaitingForALoadThatWaitsForAChild`, `TestDisposeDeliversItsNotificationsInOrderBehindABlockedHandler`, `TestQueuedNotificationsKeepTheirOrderBehindABlockedHandler`, `TestBarrierIgnoresNotificationsPastItsTarget`, `TestParseGoroutineIDAcceptsOnlyAWellFormedHeader`, `TestGoroutineIDIsDistinctPerGoroutine`, `TestUnidentifiedDeliveryIsNeverParked`, `TestDuplicateGoroutineIdentityParksNeitherDelivery` (`tree_internal_test.go`) |
| TC-02a | `TestConcurrentDisposeWaitsForEveryCleanup` |
| `DH-T-CORDIS-2` | `TestRunningCleanupHoldsBackSecondDisposeAndProvider`, `TestDependentUnloadsBeforeItsProvider`, `TestUnloadWaitsForInFlightAcquisition`, `TestNotificationHandlerCanDisposeTheAcquiringFiber`, `TestAcquisitionBarrierDoesNotRunHandlersOnItsStack` |
| `DH-T-CORDIS-3` | `TestPendingFiberReleasesEffectsAndCreationHandlerCanDispose`, `TestCreationHandlerRunsBeforeLoadUnderConcurrentDelivery`, `TestBlockedCreationHandlerHoldsTheLoadAgainstOtherDelivery`, `TestCreationHandlerDisposalCanCreateASibling` |
| `DH-T-CORDIS-4` | `TestFiberLifetimeIsCancelledByUnload` |

## Stage 2.2 -- Context Values and Scoped Dispatch

Built by the previous stage (impl.md steps 2.3 to 2.5); its tests are re-run below with
this stage's changes applied.

| Test | In `src/go/cordis/scope_test.go` |
| --- | --- |
| `DH-T-CORDIS-5` | `TestContextValueIsVisibleToItsSubtreeOnly` |
| `DH-T-CORDIS-6` | `TestScopedDispatchReachesOneSubtree` |
| `DH-T-CORDIS-7` | `TestNotifyListenerCannotVetoTheFact` |
| `DH-T-CORDIS-12` | `TestAroundKeyWrapsInRegistrationOrder` |

## Stage 2.3 -- Config Views and Strict Decoding (closes DH-S-2)

### What changed

| Step | Deliverable | API |
| --- | --- | --- |
| 2.6 | `DH-D-CORDIS-5` | `Fiber.EffectiveConfig`, `Fiber.ResolveConfig`, `Fiber.RetainConfig` beside the raw `Fiber.Config`; `Context.OnConfig(ConfigHook)`, tree-wide and owned by the registering fiber, run before the schema on every load, `Update` and `ResolveConfig`. An `Update` accepted in place is published only if no later update, reload or disposal happened while the update hooks ran (config revision, epoch and load count compared). |
| 2.7 | `DH-D-CORDIS-8` | Tree option `WithStrictConfig()`: JSON config (`json.RawMessage`, `[]byte`, `map[string]any`) is decoded with `DisallowUnknownFields` and no data after the first value; component option `LenientConfig()` exempts a component. The `ConfigIssue.Path` names the unknown key, or the field of a type mismatch (in every tree). A `WithSchema` component is decoded the same way before its schema runs. |
| 2.8 | `DH-D-CORDIS-9` | `New(options ...TreeOption)`; `WithExporters(...)` starts the logger with exactly the given exporters (none with no argument). `New()` keeps the console exporter. |

The README's claim that a schema runs under the tree lock was corrected: config is
resolved outside it (`libs/cordis.md` §7, `libs/cordis-loader.md` question 4).

### Tests

```text
$ go test ./cordis/ -race -count=1 -v -run '<the DH-T-CORDIS-5..14 tests below>'   # from src/go
--- PASS: TestRejectedCandidateDoesNotLeakIntoEffectiveConfig (0.00s)
--- PASS: TestResolveConfigStoresNothing (0.00s)
--- PASS: TestInPlaceUpdatePublishesEffectiveConfig (0.00s)
--- PASS: TestSupersededInPlaceUpdateIsNotPublished (0.00s)
--- PASS: TestPendingFiberHasNoEffectiveConfig (0.01s)
--- PASS: TestRetainConfigStoresRawWithoutUpdate (0.00s)
--- PASS: TestResolutionCannotMutateStoredConfigViews (0.00s)
    --- PASS: TestResolutionCannotMutateStoredConfigViews/map (0.00s)
    --- PASS: TestResolutionCannotMutateStoredConfigViews/pointer (0.00s)
--- PASS: TestCloneConfigCopiesReferencesAndKeepsCycles (0.00s)
--- PASS: TestConfigHookTransformsEffectiveViewOnly (0.00s)
--- PASS: TestConfigHookCanRejectACandidate (0.00s)
--- PASS: TestConfigHookEndsWithItsOwner (0.00s)
--- PASS: TestStrictConfigRejectsUnknownKeysAndTrailingData (0.00s)
--- PASS: TestTreeLogsOnlyToTheExportersItsCreatorChose (0.00s)
--- PASS: TestContextValueIsVisibleToItsSubtreeOnly (0.00s)
--- PASS: TestScopedDispatchReachesOneSubtree (0.00s)
--- PASS: TestNotifyListenerCannotVetoTheFact (0.00s)
--- PASS: TestAroundKeyWrapsInRegistrationOrder (0.00s)
ok  github.com/smartpcr/agent-plugin/src/go/cordis

$ go test ./... -race -count=5          # from src/go: every existing test, including Stage 2.1's
ok  github.com/smartpcr/agent-plugin/src/go/cmd/cordis-sample
ok  github.com/smartpcr/agent-plugin/src/go/cordis
ok  github.com/smartpcr/agent-plugin/src/go/loader
ok  github.com/smartpcr/agent-plugin/src/go/timer

$ go test -tags e2e -race -count=1 ./test/...
ok  github.com/smartpcr/agent-plugin/src/go/test/e2e/harness-DSH
```

| Test | In `src/go/cordis/config_test.go` |
| --- | --- |
| `DH-T-CORDIS-8` (TC-02a) | `TestRejectedCandidateDoesNotLeakIntoEffectiveConfig`, `TestResolveConfigStoresNothing`, `TestInPlaceUpdatePublishesEffectiveConfig`, `TestSupersededInPlaceUpdateIsNotPublished` (later update, restart, disposal), `TestPendingFiberHasNoEffectiveConfig`, `TestRetainConfigStoresRawWithoutUpdate`, `TestResolutionCannotMutateStoredConfigViews` (a hook that edits a map or pointer candidate in place, then rejects it, leaves raw and effective unchanged; `TestCloneConfigCopiesReferencesAndKeepsCycles` in `clone_test.go` covers the copy itself) |
| `DH-T-CORDIS-9` | `TestConfigHookTransformsEffectiveViewOnly`, `TestConfigHookCanRejectACandidate` (error and panic), `TestConfigHookEndsWithItsOwner` |
| `DH-T-CORDIS-13` | `TestStrictConfigRejectsUnknownKeysAndTrailingData` |
| `DH-T-CORDIS-14` | `TestTreeLogsOnlyToTheExportersItsCreatorChose` (the process's standard streams replaced by pipes) |

### Manual check: the port's example output (TC-02b), all eight changes

| Field | Value |
| --- | --- |
| Kind | `manual` (golden comparison) |
| Change | impl.md steps 2.1 to 2.8 |
| Toolchain | `go version go1.27.4 windows/amd64` |
| Date | 2026-11-19 |

```text
$ go test ./cmd/cordis-sample/ -race -count=1 -run TestOutputMatchesGolden -v
=== RUN   TestOutputMatchesGolden
--- PASS: TestOutputMatchesGolden (0.02s)
PASS
```

`src/go/cmd/cordis-sample/testdata/output.golden` was not re-recorded: there are no
intended diffs. The sample creates its tree with `cordis.New()` (console exporter kept),
does not use `WithStrictConfig`, registers no config hook, and its only config error
(an out-of-range `intervalMs`) comes from its own `Validate`, whose message is unchanged.

### Not in DH-S-2

`DH-D-CORDIS-6`, `DH-D-CORDIS-10` and `DH-D-CORDIS-11` (P1/P2) stay for DH-S-38 and
DH-S-49.
