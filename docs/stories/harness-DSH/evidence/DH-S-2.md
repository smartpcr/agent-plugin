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
load, bounded barrier): still passes unchanged.

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
| `DH-T-CORDIS-1` | `TestCleanupCannotRegisterWhileItsFiberUnloads`, `TestRootDisposeIsFinalAndExclusive`, `TestRootLifecycleHandlersCannotBlockTeardown`, `TestHandlerWaitingForALoadThatWaitsForAChild`, `TestDisposeDeliversItsNotificationsPastABlockedHandler`, `TestBarrierIgnoresNotificationsPastItsTarget` (`tree_internal_test.go`) |
| TC-02a | `TestConcurrentDisposeWaitsForEveryCleanup` |
| `DH-T-CORDIS-2` | `TestRunningCleanupHoldsBackSecondDisposeAndProvider`, `TestDependentUnloadsBeforeItsProvider`, `TestUnloadWaitsForInFlightAcquisition`, `TestNotificationHandlerCanDisposeTheAcquiringFiber` |
| `DH-T-CORDIS-3` | `TestPendingFiberReleasesEffectsAndCreationHandlerCanDispose`, `TestCreationHandlerRunsBeforeLoadUnderConcurrentDelivery`, `TestBlockedCreationHandlerHoldsTheLoadAgainstOtherDelivery` |
| `DH-T-CORDIS-4` | `TestFiberLifetimeIsCancelledByUnload` |
