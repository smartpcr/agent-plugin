# DH-S-3 evidence

Decisions that govern M1 records, this one included, are logged in [decisions.md](../decisions.md).

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The stage records below are history: under M1-D-CLOSE-1 every tick made while the gate
was red is provisional and is re-verified at that commit. The fields of impl.md sec. 7.3
and the per-step manual-check register are at the end of this file, in "Evidence record
(impl.md sec. 7.3)"; how to fill them is in [the evidence runbook](README.md).

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
--- PASS: TestAReloadListenerCanSaveTheConfigFile (0.01s)
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
| 3.5 | `DH-T-LOADER-14` | `TestLayerSetEditingALayerRestartsOnlyTheChangedEntry`, `TestLayerSetAnAcceptUpdateEntryAndAnAnonymousNeighbourAreNotRestarted` (`layers_test.go`; plan 14.1.3), `TestConfigFileAnonymousEntriesSurviveAnEditToANeighbour` (`loader_test.go`) |
| 3.6 | `DH-T-LOADER-11` | `TestNestedLoaderUnloadsWithItsOwner`, `TestNestedLoaderIsPrivateToItsOwnerAndReadOnly`, `TestNestedLoaderEvaluatesItsOwnExpressions`, `TestNestedLoaderANewLayerListRestartsOnlyWhatChanged` (`nested_test.go`) |
| 3.7 | `DH-T-LOADER-9` | `TestLifecycleLogRecordsMutationsInOrder`, `TestMutationsValidateAndSetUpserts`, `TestMutationAndRestartErrorsChangeNothing`, `TestMutationsReportTheChangeAndASelfDisposedEntryIsNotDisabled`, `TestConcurrentSetsOfOneAbsentPathAllSucceed` (`Set` is one atomic upsert), `TestPublishWritesADebugLinePerApplyReloadRestartAndUnload` (the `loader` debug lines; plan 14.1.4), and `EntryOf` of a nested fiber in `TestNestedLoaderIsPrivateToItsOwnerAndReadOnly` (`nested_test.go`) |
| 3.8 | `DH-T-LOADER-12` | `TestRestartOneEntryLeavesItsSiblingsAlone`, `TestRestartWritesNoFileAndANoticeListsTheChangedIDs`, `TestWatchedLayersDebounceABurstOfSavesIntoOneReload`, `TestAConfigEditRestartsOnlyTheChangedEntryWithOneNotice`, `TestAReloadListenerCanCallBackIntoTheLayerSet` (the notice is published after the layer source commits and unlocks), `TestARejectedConfigUpdateIsAnIssueNotAnUpdate` (refused again, and reported again, when re-applied), `TestAReloadListenerCanSaveTheConfigFile` (a listener calls `ConfigFile.Save` synchronously), `TestALayerFilesCompositionIssuesReachTheNotice` (`nested_test.go`) |
| 3.9 | `DH-T-LOADER-15` | `TestLayerSetWriteGuardMergesAFileChangedWhileItWaits` (a file changed while the guard waits is merged inside it; one `Flush`, no `Reload`), `TestLayerSetAWriterOutsideTheLockIsRefusedThenRetried` (`ErrStaleLayer` while a writer outside the lock keeps changing the file, its bytes kept; then `Reload` and `Flush` save both edits), `TestLayerSetWriteGuardWrapsEveryWrite`, `TestLayerSetAGuardThatPanicsOrSkipsTheWriteFailsIt` (`layers_test.go`; plan 14.1.1, 14.1.2) |

Plan stage 14.1 (M1-G-FND-1 to -4) replaced the 3.9 row: the write guard no longer
refuses a file changed while it waited, it merges it. The story plan's wording of step
3.9 (`implementation-plan.md`) was corrected to "re-read and merged three ways inside
the guard; refused only for a writer outside the lock" (M1-G-FND-2). Local runs at the
working tree, before the stage commit: `go test -race -shuffle=on -count=5 ./loader/`
passes on Windows (go1.27.4) and once on the WSL lane (Ubuntu-24.04); loader coverage
89.2%. The commit and the gate run ids of both lanes are recorded at the stage commit.

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

## Evidence record (impl.md sec. 7.3)

These are the eight fields of [impl.md sec. 7.3](../../../harness/impl.md#73-the-evidence-record),
in the format of [the evidence runbook](README.md#the-evidence-record-headings). Plan step
18.1.7 fills them at the frozen M1 closure commit. Until then each field points to the
history above, which stays exactly as it was recorded and is not closure evidence
(M1-D-CLOSE-1).

### Source and build identity

At closure: not yet recorded (plan 18.1.7). Historical: the manual check above names its
toolchain (`go1.27.4 windows/amd64`) and date but no commit.

### Environment and fixture

At closure: both local lanes, from their run records. macOS: out of scope
(`platforms-windows-wsl`). Historical: one local Windows machine. Fixture: the two
hand-written layer files shown under "Manual check: two hand-composed layers, one
broken".

### Exact tests and commands

At closure: the lane scripts, and `go -C src/go test -race -count=1 ./...`. Historical:
the `go test` lines under "Tests" of Stage 3.3.

### Run identifiers

At closure: the run-record ids of both lanes, in the runbook's template (plan 18.1.3).
WSL lane: not run. Windows lane: not run. macOS: out of scope (`platforms-windows-wsl`).
Historical: no run id was recorded.

### Results

At closure: not yet recorded. Historical: "DH-S-3 exit: the `DH-T-LOADER-` tests of the
nine deliverables" above.

### Manual checks

impl.md DH-S-3 names one check: compose two small layer files by hand, break one, and
record the diagnostic's file, line and column. It covers steps 3.1, 3.4, 3.5 and 3.8 (the
"Change" line of the historical record); the other steps take the rule-3 default.
Historical record: "Manual check: two hand-composed layers, one broken". How to run and
fill the register: [the evidence runbook](README.md#manual-checks). The hand-composed
check writes `base.json` and a writable `user.json` with one misspelled key into a
temporary directory and loads them through `loader.LayerSetComponent` from a throwaway
test that is removed afterwards. Composed tests run as `go -C src/go test -count=1 -v
-run '<names>' ./loader/`.

#### Manual checks register

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 3.1 | two hand-composed layers, one broken: the diagnostic names file, line and column | by hand: the two layer files, as above; record the `<file>:<line>:<col>` of the `unknown key` diagnostic | one lane | | | | `manual` |
| 3.2 | rule-3 default: every invalid entry is reported | composed test: `TestValidateReportsEveryInvalidEntryInDocumentOrder`, `TestAnEntryWithThreeFaultsReportsThemAllAndIsDisposed` | one lane | | | | `manual` |
| 3.3 | rule-3 default: the expression seam calls the injected evaluator | composed test: `TestTheExpressionSeamCallsTheInjectedEvaluator`, `TestAnExpressionTheEvaluatorRejectsIsReportedAtItsPosition` | one lane | | | | `manual` |
| 3.4 | two hand-composed layers, one broken: the other layer's entry still loads | by hand: the same run; record that the entry of `base.json` loads (`echo: +good`) | one lane | | | | `manual` |
| 3.5 | two hand-composed layers, one broken: the layered source with one writable layer | by hand: the same run, `user.json` writable; record that only `user.json` is named as writable and neither file changed on disk | one lane | | | | `manual` |
| 3.6 | rule-3 default: a nested loader runs under its owner's context | composed test: `TestNestedLoaderUnloadsWithItsOwner`, `TestNestedLoaderIsPrivateToItsOwnerAndReadOnly` | one lane | | | | `manual` |
| 3.7 | rule-3 default: mutations are validated and logged in order | composed test: `TestLifecycleLogRecordsMutationsInOrder`, `TestMutationAndRestartErrorsChangeNothing` | one lane | | | | `manual` |
| 3.8 | two hand-composed layers, one broken: one settled-reload notice | by hand: the same run; record the one `notice:` line and the issue it carries | one lane | | | | `manual` |
| 3.9 | rule-3 default: the write guard merges a file changed while it waits, and refuses only a writer outside the lock | composed test: `TestLayerSetWriteGuardMergesAFileChangedWhileItWaits`, `TestLayerSetAWriterOutsideTheLockIsRefusedThenRetried`, `TestLayerSetWriteGuardWrapsEveryWrite` | one lane | | | | `manual` |

### Cleanup evidence

At closure: the throwaway test is removed and the temporary directory is gone;
`git status --porcelain` empty after the run. Historical: "removed afterwards", in the
manual check above.

### Open items

At closure: plan 18.1.11 lists them. Now open: the loader write-back merge and proof of
plan Stage 14.1; `DH-D-LOADER-14` ("Not in DH-S-3" above).
