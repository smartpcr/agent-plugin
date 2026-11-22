# DH-S-2 evidence

Decisions that govern M1 records, this one included, are logged in [decisions.md](../decisions.md).

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The stage records below are history: under M1-D-CLOSE-1 every tick made while the gate
was red is provisional and is re-verified at that commit. The fields of impl.md sec. 7.3
and the per-step manual-check register are at the end of this file, in "Evidence record
(impl.md sec. 7.3)"; how to fill them is in [the evidence runbook](README.md).

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
    --- PASS: TestResolutionCannotMutateStoredConfigViews/defined_pointer (0.00s)
--- PASS: TestCloneConfigCopiesReferencesAndKeepsCycles (0.00s)
--- PASS: TestCloneConfigKeepsDefinedPointerTypes (0.00s)
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
| `DH-T-CORDIS-8` (TC-02a) | `TestRejectedCandidateDoesNotLeakIntoEffectiveConfig`, `TestResolveConfigStoresNothing`, `TestInPlaceUpdatePublishesEffectiveConfig`, `TestSupersededInPlaceUpdateIsNotPublished` (later update, restart, disposal), `TestPendingFiberHasNoEffectiveConfig`, `TestRetainConfigStoresRawWithoutUpdate`, `TestResolutionCannotMutateStoredConfigViews` (a hook that edits a map or pointer candidate in place, then rejects it, leaves raw and effective unchanged, and a defined pointer config type (`type sizedPtr *sized`) still loads with its type kept; `TestCloneConfigCopiesReferencesAndKeepsCycles` and `TestCloneConfigKeepsDefinedPointerTypes` in `clone_test.go` cover the copy itself) |
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

M1 gate repair (plan 13.1.3, 13.1.4; [DH-S-1.md, "M1 gate repair"](DH-S-1.md#m1-gate-repair)): `src/go/cordis/config_test.go`, `tree_internal_test.go` and `unload_test.go` now release and join every goroutine they start (P-C, helper `join_test.go`), and the `unload_test.go` settle waits run in `synctest` bubbles; DH-T-CORDIS tags unchanged.

## Evidence record (impl.md sec. 7.3)

These are the eight fields of [impl.md sec. 7.3](../../../harness/impl.md#73-the-evidence-record),
in the format of [the evidence runbook](README.md#the-evidence-record-headings). Plan step
18.1.7 fills them at the frozen M1 closure commit. Until then each field points to the
history above, which stays exactly as it was recorded and is not closure evidence
(M1-D-CLOSE-1).

### Source and build identity

At closure: not yet recorded (plan 18.1.7). Historical: the Stage 2.1 and Stage 2.3
records above name the toolchain (`go1.27.4 windows/amd64`) and date but no commit.

### Environment and fixture

At closure: both local lanes, from their run records. macOS: out of scope
(`platforms-windows-wsl`). Historical: one local Windows machine. Fixture: the committed
golden `src/go/cmd/cordis-sample/testdata/output.golden`.

### Exact tests and commands

At closure: the lane scripts, and `go -C src/go test -race -count=1 ./...`. Historical:
the `go test` lines under "Tests" of Stage 2.1 and Stage 2.3.

### Run identifiers

At closure: the run-record ids of both lanes, in the runbook's template (plan 18.1.3).
WSL lane: not run. Windows lane: not run. macOS: out of scope (`platforms-windows-wsl`).
Historical: no run id was recorded.

### Results

At closure: not yet recorded. Historical: the test tables of Stage 2.1, 2.2 and 2.3.

### Manual checks

impl.md DH-S-2 names one check for every step: run the port's existing example with
each change and record that its output is unchanged except where the change says so.
Historical records: "Manual check: the port's example output (TC-02b)" under Stage 2.1
and Stage 2.3. How to run and fill the register: [the evidence runbook](README.md#manual-checks).
Each row runs `go -C src/go test -race -count=1 -v -run TestOutputMatchesGolden
./cmd/cordis-sample/` and records `PASS` and that `git diff --exit-code
src/go/cmd/cordis-sample/testdata/output.golden` is clean (the golden was not
re-recorded).

#### Manual checks register

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 2.1 | the example's output is unchanged (unload is complete and final) | composed test: the sample golden, as above; expected difference: none | one lane | | | | `manual` |
| 2.2 | the example's output is unchanged (a cancellation context per fiber) | composed test: the sample golden; expected difference: none | one lane | | | | `manual` |
| 2.3 | the example's output is unchanged (context values, scoped dispatch) | composed test: the sample golden; expected difference: none | one lane | | | | `manual` |
| 2.4 | the example's output is unchanged (contained `Notify`) | composed test: the sample golden; expected difference: none | one lane | | | | `manual` |
| 2.5 | the example's output is unchanged (around key) | composed test: the sample golden; expected difference: none | one lane | | | | `manual` |
| 2.6 | the example's output is unchanged (raw, effective and candidate config) | composed test: the sample golden; expected difference: none (the sample registers no config hook) | one lane | | | | `manual` |
| 2.7 | the example's output is unchanged (strict config decoding) | composed test: the sample golden; expected difference: none (the sample does not use `WithStrictConfig`) | one lane | | | | `manual` |
| 2.8 | the example's output is unchanged (exporters chosen by the creator) | composed test: the sample golden; expected difference: none (`cordis.New()` keeps the console exporter) | one lane | | | | `manual` |

### Cleanup evidence

At closure: `git status --porcelain` empty after the run (no golden re-recorded).
Historical: none recorded.

### Open items

At closure: plan 18.1.11 lists them. Now open: the `src/go/cordis` test findings of the
`vet` gate, repaired by plan Stage 13.1; `DH-D-CORDIS-6`, `-10` and `-11` stay for DH-S-38
and DH-S-49 ("Not in DH-S-2" above).
