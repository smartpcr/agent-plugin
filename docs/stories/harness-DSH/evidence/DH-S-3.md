# DH-S-3 evidence

Stages 3.1 and 3.2 (impl.md steps 3.1 to 3.5 and 3.9) were built by the previous
workstreams of this phase; Stage 3.3 (steps 3.6 to 3.8) closes DH-S-3. Every test
named below was re-run with all nine deliverables applied.

## Stage 3.3 -- Nested Loaders and Entry Restart (closes DH-S-3)

### What changed

| Step | Deliverable | API (`src/go/loader`) |
| --- | --- | --- |
| 3.6 | `DH-D-LOADER-10` | `loader.New(ctx, Options, layers...)` runs a loader of a component's own, provided as an isolated `loader` service under `ctx` (invisible to sibling contexts); its entry fibers are children of the owner's fiber and unload with it. It refuses writable or file-backed layers, its `Create`/`Update`/`Set`/`Move`/`Remove`/`SetDisabled` return `ErrReadOnly`, and `ApplyLayers(layers)` changes it, keeping the ids of anonymous entries so only changed entries restart. It evaluates expressions with its own `Options.Evaluator`. A loader whose owner has unloaded returns `ErrClosed`. |
| 3.7 | `DH-D-LOADER-8` | `Create(parentID, index, options)`, `Update(id, options)`, `Set(path, options)` (upsert), `Move(id, parentID, index)`, `Remove(id)`: each validates first (an invalid edit changes nothing), applies atomically under the apply lock, and raises `OnChange`. `Lookup(path)` reads the desired tree; `EntryOf(fiber)` walks up from any fiber to the entry that loaded it. `Lifecycle()` (the last 512 events, oldest first, numbered by `Seq`) and `OnLifecycle(fn)` record `add`, `update`, `move`, `remove` and `restart`, with the entry's options before and after. |
| 3.8 | `DH-D-LOADER-11` | `Restart(id)` disposes and reloads one entry (a group with its children); siblings, the tree and every file are untouched. `loader.Reloaded` (a `Notify`, so no listener can veto it) carries a `ReloadNotice{Loader, Created, Updated, Restarted, Removed, Issues}` and is emitted once per `Apply`/edit/`ApplyLayers` and once per `Restart`, after every fiber it touched has settled; a nested loader's notice reaches only listeners at or below its owner. `LayerSetOptions.Debounce` waits until watched layer files are quiet before one reload. |

### Tests

```text
$ go test ./... -race -count=1          # from src/go
ok  github.com/smartpcr/agent-plugin/src/go/cmd/cordis-sample
ok  github.com/smartpcr/agent-plugin/src/go/cordis
ok  github.com/smartpcr/agent-plugin/src/go/loader
ok  github.com/smartpcr/agent-plugin/src/go/timer

$ go test -tags e2e -race -count=1 ./test/...
ok  github.com/smartpcr/agent-plugin/src/go/test/e2e/harness-DSH

$ go test ./loader/ -race -count=1 -v -run 'Nested|Restart|ConfigEdit|Debounce|Lifecycle|Mutation'
--- PASS: TestNestedLoaderUnloadsWithItsOwner (0.00s)
--- PASS: TestNestedLoaderIsPrivateToItsOwnerAndReadOnly (0.00s)
--- PASS: TestNestedLoaderANewLayerListRestartsOnlyWhatChanged (0.00s)
--- PASS: TestRestartOneEntryLeavesItsSiblingsAlone (0.00s)
--- PASS: TestAConfigEditRestartsOnlyTheChangedEntryWithOneNotice (0.00s)
--- PASS: TestWatchedLayersDebounceABurstOfSavesIntoOneReload (0.11s)
--- PASS: TestLifecycleLogRecordsMutationsInOrder (0.00s)
--- PASS: TestMutationsValidateAndSetUpserts (0.00s)
--- PASS: TestMutationAndRestartErrorsChangeNothing (0.00s)
--- PASS: TestMutationsReportTheChangeAndASelfDisposedEntryIsNotDisabled (0.00s)
--- PASS: TestNestedLoaderEvaluatesItsOwnExpressions (0.00s)
--- PASS: TestRestartWritesNoFileAndANoticeListsTheChangedIDs (0.01s)
--- PASS: TestAReloadListenerCanCallBackIntoTheLayerSet (0.01s)
--- PASS: TestARejectedConfigUpdateIsAnIssueNotAnUpdate (0.00s)
--- PASS: TestALayerFilesCompositionIssuesReachTheNotice (0.01s)
--- PASS: TestConcurrentSetsOfOneAbsentPathAllSucceed (0.01s)
ok  github.com/smartpcr/agent-plugin/src/go/loader
```

The new tests were also run with `-count=30` without a failure. The debounce test
runs on virtual time (`testing/synctest`), so it does not sleep.

| Scenario (implementation-plan.md Stage 3.3) | Test in `src/go/loader/nested_test.go` |
| --- | --- |
| Nested loader unloads with its owner (TC-03a) | `TestNestedLoaderUnloadsWithItsOwner`: both nested disposers have run before the owner's `Wait` returns, and before the effect the owner acquired first |
| Restart one entry, siblings untouched | `TestRestartOneEntryLeavesItsSiblingsAlone` (construct counts 1/2/1, one notice) and `TestAConfigEditRestartsOnlyTheChangedEntryWithOneNotice` (the config of entry two changes: 1/2/1, exactly one notice) |
| Lifecycle log records mutations in order | `TestLifecycleLogRecordsMutationsInOrder`: `add`, `update`, `remove` for `g:x`, in order, in the log and to an observer |

## DH-S-3 exit: the `DH-T-LOADER-` tests of the nine deliverables

| Step | Test | In `src/go/loader/` |
| --- | --- | --- |
| 3.1 | `DH-T-LOADER-1` | `TestDiagnosticsCarryFileLineAndColumn`, `TestConfigFileReportsAnInvalidEntryAtItsPosition`, `TestJSONCodecReportsMalformedInputWithAPosition`, `TestJSONCodecRoundTripsTheEntrySchema` (`codec_test.go`) |
| 3.2 | `DH-T-LOADER-3` | `TestValidateReportsEveryInvalidEntryInDocumentOrder`, `TestAnEntryWithThreeFaultsReportsThemAllAndIsDisposed`, `TestAnInterceptReachesTheEntrysServices`, `TestDescendantsOfAnInvalidGroupAreStillReportedWhenLoading`, `TestChildrenOfDistinctAnonymousGroupsDoNotCollide` (`codec_test.go`), `TestASharedRealmKeyIsReleasedWithItsLastEntry` (`realm_internal_test.go`) |
| 3.3 | `DH-T-LOADER-4` | `TestTheExpressionSeamCallsTheInjectedEvaluator`, `TestASavedFileKeepsTheExpressionSource`, `TestADisabledExpressionIsEvaluatedAtEachApply`, `TestAnExpressionWithoutAnEvaluatorIsReportedNotLoaded`, `TestAnExpressionTheEvaluatorRejectsIsReportedAtItsPosition` (`expr_test.go`) |
| 3.3 | `DH-T-LOADER-5` | `TestAConfigExpressionReadingUseRunsOnlyOnceItsServiceIsActive`, `TestAConfigEvaluationFailureNamesTheValuesPosition`, `TestAnEmptyExpressionIsRejectedNotTreatedAsAbsent` (`expr_test.go`) |
| 3.4 | `DH-T-LOADER-7` | `TestComposeOwnershipIndexNamesTheSupplyingLayer`, `TestComposeLaterLayersWinAndUnmatchedOpsAreReported`, `TestParseLayerReadsOpsWithComments`, `TestJSONCRejectsAnUnterminatedBlockComment`, `TestEncodeLayerKeepsACommentTrailingTheLastKeptOp` (`layers_test.go`) |
| 3.5 | `DH-T-LOADER-8` | `TestLayerSetWriteBackTouchesOneLayerFileOnly`, `TestLayerSetARuntimeEditOfALowerValueBecomesAWritableOp`, `TestLayerSetAnEditMaskedByALaterLayerIsRefused`, `TestLayerSetAMaskedEditTheWritableFileAlreadyHoldsIsRefused`, `TestLayerSetAVanishedLayerKeepsTheRunningTree`, `TestLayerSetAReadOnlyLayerFailsTheWriteUntilItIsWritable`, `TestLayerSetOptionsAllowOneWritableLayer` (`layers_test.go`) |
| 3.5 | `DH-T-LOADER-14` | `TestLayerSetEditingALayerRestartsOnlyTheChangedEntry` (`layers_test.go`), `TestConfigFileAnonymousEntriesSurviveAnEditToANeighbour` (`loader_test.go`) |
| 3.6 | `DH-T-LOADER-11` | `TestNestedLoaderUnloadsWithItsOwner`, `TestNestedLoaderIsPrivateToItsOwnerAndReadOnly`, `TestNestedLoaderEvaluatesItsOwnExpressions`, `TestNestedLoaderANewLayerListRestartsOnlyWhatChanged` (`nested_test.go`) |
| 3.7 | `DH-T-LOADER-9` | `TestLifecycleLogRecordsMutationsInOrder`, `TestMutationsValidateAndSetUpserts`, `TestMutationAndRestartErrorsChangeNothing`, `TestMutationsReportTheChangeAndASelfDisposedEntryIsNotDisabled`, `TestConcurrentSetsOfOneAbsentPathAllSucceed` (`Set` is one atomic upsert), and `EntryOf` of a nested fiber in `TestNestedLoaderIsPrivateToItsOwnerAndReadOnly` (`nested_test.go`) |
| 3.8 | `DH-T-LOADER-12` | `TestRestartOneEntryLeavesItsSiblingsAlone`, `TestRestartWritesNoFileAndANoticeListsTheChangedIDs`, `TestWatchedLayersDebounceABurstOfSavesIntoOneReload`, `TestAConfigEditRestartsOnlyTheChangedEntryWithOneNotice`, `TestAReloadListenerCanCallBackIntoTheLayerSet` (the notice is published after the layer source commits and unlocks), `TestARejectedConfigUpdateIsAnIssueNotAnUpdate`, `TestALayerFilesCompositionIssuesReachTheNotice` (`nested_test.go`) |
| 3.9 | `DH-T-LOADER-15` | `TestLayerSetWriteGuardRefusesAStaleWrite`, `TestLayerSetWriteGuardWrapsEveryWrite`, `TestLayerSetAGuardThatPanicsOrSkipsTheWriteFailsIt` (`layers_test.go`) |

`DH-T-LOADER-2`, `-6`, `-10`, `-13` and `-16` verify deliverables outside DH-S-3
(`src/harness` config and logging, DH-D-LOADER-9 and -14).

### Manual check: two hand-composed layers, one broken

| Field | Value |
| --- | --- |
| Kind | `manual` |
| Change | impl.md steps 3.1 (positions), 3.4 (composition), 3.5 (layered source), 3.8 (notice) |
| Toolchain | `go version go1.27.4 windows/amd64` |
| Date | 2026-11-20 |

Two layer files written by hand; the user layer misspells `disabled`:

```text
base.json:
[
  { "insert": [
    { "id": "good", "name": "EchoPlugin", "config": { "text": "good" } }
  ] }
]

user.json:
[
  { "insert": [
    { "id": "broken", "name": "EchoPlugin",
      "disabeld": true }
  ] }
]
```

Loaded through `loader.LayerSetComponent` with `base` then `user` (writable), from a
throwaway test (removed afterwards); the temporary directory is shown as `<TMP>`:

```text
error: loader: <TMP>/user.json:4:7: [0].insert[0].disabeld: unknown key
notice: created=[good] issues=[<TMP>/user.json:4:7: [0].insert[0].disabeld: unknown key]
echo: +good
```

The diagnostic names the broken entry's file, line 4 and column 7 (the misspelled
key), the sibling from the other layer still loads, and the one settled-reload notice
carries the same issue.

### Not in DH-S-3

`DH-D-LOADER-14` (a group's `source` file, P2) stays with DH-D-LOADER-10's other half.
