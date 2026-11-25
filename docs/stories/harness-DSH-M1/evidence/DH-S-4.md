# DH-S-4 evidence

Decisions that govern M1 records, this one included, are logged in [decisions.md](../decisions.md).

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The stage records below are history: under M1-D-CLOSE-1 every tick made while the gate
was red is provisional and is re-verified at that commit. The fields of impl.md sec. 7.3
and the per-step manual-check register are at the end of this file, in "Evidence record
(impl.md sec. 7.3)"; how to fill them is in [the evidence runbook](README.md).

Stages 4.1 to 4.3 (impl.md steps 4.1 to 4.9: the JSON-RPC codec, the shape schema,
the SDK protocol types, the util leaves, the model vocabulary, `fsx`, `textdiff`,
`search` and the watcher) were built by the earlier workstreams of this phase.
Stage 4.4 (steps 4.10 to 4.14) closes DH-S-4. Every result below was re-run with all
fourteen deliverables applied.

## Stage 4.4 -- YAML, Expressions, Logging and Catalog (closes DH-S-4)

### What changed

| Step | Deliverable | Package (`src/harness/internal/boot/`) |
| --- | --- | --- |
| 4.10 | `DH-D-LOADER-2` | `config`: `YAML` is a `loader.Codec` (plugs the DH-D-LOADER-1 seam). `Decode` builds the entry tree through `yamlx`, so every diagnostic carries the file, line and column of the YAML source; `Encode` writes the canonical document; `EncodeLayer` writes a layer by editing the text last read, keeping its comments; if that text no longer parses or the edit cannot be verified to hold exactly the intended operations and every comment, the write is refused (`ErrWriteRefused`, no text) and the file is left as it was (DH-LIB-YAML-R8). Only a layer with no previous text is rendered afresh. `Register` adds it beside the JSON codec for `.yaml`/`.yml`. |
| 4.11 | `DH-D-BOOT-4` | `yamlx`: the one YAML dialect. `Parse` refuses anchors, aliases, merge keys, tags not registered by the caller (`WithTag`, used for `!expr`), duplicate keys and documents past the size/depth/node limits, each with its position; scalars are typed by YAML 1.2 core rules. `Edit` sets, inserts or removes one value and keeps every comment and the layout of every untouched line; it re-parses its output and refuses an edit whose result is not the requested tree. `DecodeFrontmatter` reads a Markdown front matter block. The package is `//dsh:untrusted-parser` with `FuzzParse` and seeds in `seeds_test.go` (QG-6). |
| 4.12 | `DH-D-LOADER-5` | `expr`: `Evaluator` implements the DH-D-LOADER-4 seam on `github.com/expr-lang/expr`. Names are `env.<NAME>`, `os`, `cwd`, `launch.*` and, in config scope only, `use.<service>` (a declared, plain-data view: no pointer, function, interface, channel or `dsh:"secret"` field at any depth; a self-containing type is checked once). The only function is `home(...)`, a pure path join that refuses absolute paths and `..` escapes; only pure builtins are enabled (no `now`, `date`, `duration`, `timezone`); `$env`, a computed `env[...]`, and secret env names are refused at compile time. Nothing in the engine can reach the filesystem, network, clock or a process (NG-5). |
| 4.13 | `DH-D-LOADER-12` | `logging`: the `boot.logging` cordis component. Its config (`destination` `stderr`/`file`, `path`, `level`, `format` `text`/`json`) builds a `log/slog` handler and adds it to the tree with `ctx.Loggers().AddExporter(cordis.SlogExporter(...))` (DH-D-CORDIS-9); a level more verbose than the tree's threshold (debug under the default info) raises that threshold while loaded and never lowers it; the loaded components of one tree share the threshold (the most verbose any still needs), so unloading one in any order keeps another's level, and unloading the last restores the threshold unless it was changed since; unloading removes the exporter and closes the file. stdout is never written. |
| 4.14 | `DH-D-BOOT-1` | `catalog`: `Build` freezes a named list of compiled-in components (name, config type, provided service, declared view, platforms), refusing malformed or unregistered family names, duplicates, a component whose name differs from its entry, and an invalid view. `Lookup` tells unknown from unavailable-on-this-OS; `Resolve` is the loader's resolver; `Print` writes the table `dsh` preview will show. `builtin.Catalog()` is the list this build ships. |

Module pins (RM-10), in `src/harness/go.mod`, each with its reason comment:
`go.yaml.in/yaml/v4 v4.0.0` (the module chosen by libs/yaml.md) and
`github.com/expr-lang/expr v1.17.8`. Neither has dependencies of its own.
`go mod verify` passes. Two new archtest rules keep each behind its one package:
only `internal/boot/yamlx` imports the YAML module, only `internal/boot/expr`
imports expr-lang.

### Tests

```text
$ go test -race -count=1 -cover ./...          # from src/harness (excerpt)
ok  .../internal/archtest          coverage: 96.9% of statements
ok  .../internal/boot/builtin      coverage: 100.0% of statements
ok  .../internal/boot/catalog      coverage: 100.0% of statements
ok  .../internal/boot/config       coverage: 96.1% of statements
ok  .../internal/boot/expr         coverage: 99.4% of statements
ok  .../internal/boot/logging      coverage: 98.6% of statements
ok  .../internal/boot/yamlx        coverage: 96.1% of statements
```

Every other package of `src/harness`, `src/sdk` and `src/go` also passes.
`gofmt -l` is empty, and `go vet -tags scenario ./internal/testkit/scenarios/`
compiles the scenario files without running them.

| Scenario (implementation-plan.md Stage 4.4) | Test |
| --- | --- |
| Same entries through JSON and YAML codecs (`DH-T-LOADER-2`, TC-04a) | `TestSameEntriesThroughJSONAndYAML` (`config/yaml_test.go`): equal effective trees, and each diagnostic's position points into its own source; also `TestDecodeRejectsWithPosition`, `TestEncodeRoundTrips`, `TestEncodeLayerKeepsComments`, `TestEncodeLayerRefusesAnUnverifiedWrite` (DH-LIB-YAML-R8: unparsable previous text, a dropped comment, a changed value, undecodable output) |
| yamlx edit keeps comments (TC-04a) | `TestEditKeepsComments` (`yamlx/yamlx_test.go`) against the committed golden `internal/boot/yamlx/testdata/edit_keep_comments.golden`; also `TestEditIsStable`, `TestEditRefusesUnverifiedText`, `TestDeleteNestedMovesCommentsForward`, `FuzzParse` |
| Expression engine has no side effects (`DH-T-LOADER-6`) | `TestNamesResolve` (env, launch, os, cwd resolve; an undefined name fails naming it), `TestRejected` (`readFile`, `$env`, computed env names, secrets), `TestOnlyPureBuiltins`, `TestHome`, `TestBehindLoaderSeam`, `TestCheckView` (secret fields direct, nested and inside a self-containing type are refused; recursive plain types, including named self-containing slices and maps, are accepted) (`expr/expr_test.go`) |
| Catalog lists components by name (TC-04a) | `TestCatalogListsComponentsByName` (`catalog/catalog_test.go`) against the committed golden `internal/boot/catalog/testdata/catalog.golden`; also `TestBuildRejects` (DH-T-BOOT-1), `TestLookupAndPlatforms` |
| Logging writes to the configured destination only (`DH-T-LOADER-13`) | `TestFileDestinationKeepsStdoutClean` (stdout stays empty, the file gets one JSON line, unloading removes the exporter and closes the file), `TestDebugLevelReachesTheDestination` (a debug record reaches the file through an exporter-free tree; the threshold is restored, never lowered, and not overwritten if changed since), `TestOverlappingDebugLoggers` (two debug loggers unloaded in either order: the remaining one still gets debug records, the threshold returns to info after both), `TestStderrDestination`, `TestBadConfigFailsLoad` (`logging/logging_test.go`) |

## DH-S-4 exit: import direction

```text
$ go test ./internal/archtest/ -run TestImportDirection -count=1 -v   # from src/harness
--- PASS: TestImportDirection
ok  github.com/smartpcr/agent-plugin/src/harness/internal/archtest
```

`TestImportDirection` runs every rule against the real modules: `src/sdk` imports
neither cordis (`src/go`) nor the harness, `src/go` imports neither `src/sdk` nor the
harness, and the YAML and expression modules are reached only through `yamlx` and
`expr`. The rule tests (`TestRulesCatchViolations`) show each rule failing on a seeded violation.

## Manual checks (impl.md DH-S-4)

These need a person at real machines and are recorded `manual`; they were not run by
this workstream.

| Check | Procedure | Result |
| --- | --- | --- |
| Watcher, real editor | On each of darwin, linux and windows, save a watched file from a real editor (VS Code, vim, a JetBrains IDE) and count the notices (historical procedure; darwin is out of scope (`platforms-windows-wsl`); re-targeted by plan 12.4.5 in the register below) | `manual` -- pending; automated form is `DH-E-CHOKIDAR-1` |
| Search ignored set | Run `search.List` on a real repository and compare the listed and ignored sets with `git ls-files --cached --others --exclude-standard` | `manual` -- pending; automated form is `DH-E-RIPGREP-2` |

## Scenarios authored here, run in DH-S-18

Indexed in `test/harness/acceptance-index.yaml` under `DH-S-18` with
`authored: DH-S-4`, in `src/harness/internal/testkit/scenarios/` behind
`//go:build scenario` (compiled by vet, not executed):

| ID | Test | File |
| --- | --- | --- |
| `DH-E-CHOKIDAR-1` | `TestWatchRealEditorSaves` | `watch_scenario_test.go` |
| `DH-E-CHOKIDAR-2` | `TestWatchRemoteFilesystemsPoll` | `watch_scenario_test.go` |
| `DH-E-RIPGREP-1` | `TestSearchMeetsBudget` | `search_scenario_test.go` |
| `DH-E-RIPGREP-2` | `TestFileListingMatchesGit` | `search_scenario_test.go` |
| `DH-E-NODEFS-1` | `TestDurableReplaceSurvivesPowerLoss` | `durable_replace_scenario_test.go` |

The watcher scenarios time a save or remote change at the moment this machine first sees the file change (polled with `os.Stat` while the command runs) and each report at its callback, so a command that lingers cannot hide a late report; the search scenario fails a comparison whose rg run exits other than 0 with files or 1 without, or reports problems. Each reads its real environment (editors, remote mounts, repositories, VMs) from
`DSH_SCENARIO_*` variables and fails, naming the variable, when one is absent or lists no item (a value such as `;`): a scenario that cannot run never skips (DH-O-15). The scenario-index check
(`acceptance.ReadIndex` + `ScanScenarios` + `Check`) passes with six scenarios.
`DH-E-FS-1` and `DH-E-FS-2` are authored by DH-S-12 (impl.md), not here.

## Known limitations

- `config.YAML.Encode` of a whole document writes the canonical form without comments;
  only `EncodeLayer` (the write-back path) keeps them. A replaced scalar takes the
  plain or quoted style its new value needs, not its old style.
- `expr` takes the secret-name predicate and the launch data through `Options`; the
  real sources (DH-D-HUMAN-7 and DH-D-BOOT-2) are later stages.
- The catalog's families are names only; their owning areas are not yet recorded. (Plan 14.5.6 records them as `[]Family{Name, Area}`; register row 4.14.)
- The built-in list lives in `internal/boot/builtin` (not beside `cmd/dsh`) so the
  catalog golden test can import it.
- The harness gate's vet step currently fails on files from earlier stages
  (`src/go/cordis` tests, the `recur`, `glob`, `caltime`, `version` fuzz seeds and
  `watchtest/manual_test.go`); none of the files of this stage are reported.
- M1 gate repair (plan 13.1.5; [DH-S-1.md, "M1 gate repair"](DH-S-1.md#m1-gate-repair)): the fuzz seeds of `internal/util/{caltime,glob,recur,version}` moved to a new `seeds_test.go` in each package (`caltime_test.go`, `glob_test.go`, `rrule_test.go`, `version_test.go` call the helper); `watchtest/manual_test.go` is Stage 13.2.
- M1 gate triage (plan 13.7.1, [M1-G-GATE-15.2](../gap-analysis.md)): on Linux a watch whose parent directory is deleted and recreated within one settle window is re-registered -- ext4 reuses the inode number, so the identity check alone kept a dead inotify registration; an event naming the watched directory itself, or an overflow, now forces a re-add (`util/watch/watch.go` `subscribe`, `dispatch`; new `TestFakeRearmOnDirectoryEvent`). `TestRecreatedParentWithinSettle` went from 5 failures in 5 to a passing `-race -shuffle=on -count=20` of `./internal/util/watch/...` on WSL ext4, also passing on Windows native; package coverage 91.2% to 93.5% (WSL), 91.4% to 92.7% (Windows) (go1.27.4, uncommitted on `105859e`; `manual`, not gate evidence).

## Evidence record (impl.md sec. 7.3)

These are the eight fields of [impl.md sec. 7.3](../../../harness/impl.md#73-the-evidence-record),
in the format of [the evidence runbook](README.md#the-evidence-record-headings). Plan step
18.1.7 fills them at the frozen M1 closure commit; plan Stage 14.5 adds the test mapping
for steps 4.1 to 4.9 and the two manual-check records before that. Until then each field
points to the history above, which stays exactly as it was recorded and is not closure
evidence (M1-D-CLOSE-1).

### Source and build identity

**At the closure commit (plan 18.1.7).** Commit `593ceaa3efd55d41bf0eeaced5ba7e26acc71966`: one commit for the three modules, on branch `m1-phase-18` over `main` 896c760. Go `go1.27.4` (the go floor). The WSL rehearsal built six `dsh` binaries, each with `vcs.revision=593ceaa…` and `vcs.modified=false`; their SHA-256 values are in [wsl-593ceaa-20261123T2122Z-rehearse.txt](runs/wsl-593ceaa-20261123T2122Z-rehearse.txt).

At closure: not yet recorded (plan 18.1.7). Historical: no commit was recorded; the module
pins are under "What changed" of Stage 4.4.

### Environment and fixture

**At the closure commit (plan 18.1.7).** Windows 11 Enterprise 10.0.28000 native, with `CC` MSYS2 gcc. WSL Ubuntu 24.04.5 LTS, kernel 6.18.58.1-microsoft-standard-WSL2, ext4 clone, gcc 13.3.0. macOS is out of this story (`macos-return-stage`). The manual checks ran on the same workstation (decision `manual-checks-run-by-lead`). Signed-in clients: Claude Code 2.1.312 and GitHub Copilot CLI 1.0.115-7, Windows only. Fixtures: those named by this stage's register rows.

At closure: both local lanes, from their run records, and for the manual checks the
distribution, kernel and editor of each save (plan 14.5.5). macOS: out of scope
(`platforms-windows-wsl`). Historical: one local machine. Fixtures: the committed goldens
`internal/boot/yamlx/testdata/edit_keep_comments.golden` and
`internal/boot/catalog/testdata/catalog.golden`.

### Exact tests and commands

**At the closure commit (plan 18.1.7).** `tools/harness-gate/lanes/win.ps1` and `tools/harness-gate/lanes/wsl.sh` (the gate with `--evidence`; `rehearse` in WSL). `DSH_REQUIRE_SCENARIOS` is unset. For the register: `evidence/runs/manual-593ceaa/run-register.sh` (Windows, and `--os wsl`), with the by-hand steps of `by-hand-checklist.md`; the per-row commands are in this record's register.

At closure: the lane scripts, and `go -C src/harness test -race -count=1 -cover ./...` and
`go -C src/sdk test -race -count=1 ./...`. Historical: the `go test` lines under "Tests"
of Stage 4.4 and "DH-S-4 exit: import direction".

### Run identifiers

**At the closure commit (plan 18.1.7).** Windows lane `win-593ceaa-20261123T2122Z` ([record](runs/win-593ceaa-20261123T2122Z.txt)) and WSL lane `wsl-593ceaa-20261123T2122Z` ([record](runs/wsl-593ceaa-20261123T2122Z.txt), [rehearse](runs/wsl-593ceaa-20261123T2122Z-rehearse.txt)). Register logs: `register-windows-593ceaa-20261123T2213Z` and `register-wsl-593ceaa-20261123T2237Z` in [runs/manual-593ceaa/](runs/manual-593ceaa/byhand-results.md). The WSL rehearse record was rebuilt from the lane's tee'd output after the original file was deleted by mistake; the gate record's own `sha256:` line verifies it byte for byte.

At closure: the run-record ids of both lanes, in the runbook's template (plan 18.1.3).
WSL lane: not run. Windows lane: not run. macOS: out of scope (`platforms-windows-wsl`).
Historical: no run id was recorded. The scenarios authored here run in DH-S-18 and have
no run id yet.
Stage 14.5 (uncommitted on `b59ea4a`): Windows lane `<run id: lead>`, WSL lane
`<run id: lead>`.

### Results

**At the closure commit (plan 18.1.7).** Both lanes `PASSED all 12 gates`, with `trace-tests` in the `trace` gate. `trace-tests` for DH-S-1 to DH-S-11 reports required 310, tagged 288, deferred 22, missing 0. This stage's register: see "Manual checks register" (every row PASS or recorded as seen; not run and pending rows are open, never a pass).

At closure: not yet recorded. Historical: the test table of Stage 4.4 and the import
direction test.

### Manual checks

impl.md DH-S-4 names two checks: for the watcher (step 4.9), save a file from a real
editor on each operating system and record one notice per save; for search (step 4.8),
run it on a real repository and compare the ignored set with what Git reports. Plan step
12.4.5 re-targets both to the in-scope systems, and plan step 14.5.5 runs them
([plan Stage 14.5](../gap-closure-plan.md#stage-145-leaf-test-tags-evidence-and-manual-checks)).
Darwin is out of scope (`platforms-windows-wsl`). The other steps take the rule-3
default. How to run and fill the register: [the evidence runbook](README.md#manual-checks).
Composed tests run as `go -C src/harness test -count=1 -v -run '<names>' <packages>`
unless the row names `src/sdk`.

#### Manual checks register

**Run at the M1 closure commit (plan 18.1.5, 2026-11-23).** The lead Claude Code session ran every row below at `593ceaa3efd55d41bf0eeaced5ba7e26acc71966`, at the owner's instruction of 2026-11-23 ("you should do that"); the owner did not watch these runs. This departs from M1-D-CLOSE-2 A (a named person runs it at the recorded commit, watches it and records it `manual`) and from the runbook rule that signed-in checks are never run from an agent session, and it needs to be recorded as an owner decision. Logs: `register-windows-593ceaa-20261123T2213Z.log`, `register-wsl-593ceaa-20261123T2237Z.log`, `byhand-windows-593ceaa.log`, `byhand-windows-593ceaa-part2.log`, `byhand-wsl-593ceaa.log`, `signedin-dhs9-593ceaa.log`, `signedin-m1-claude-593ceaa.log` and `signedin-m1-copilot-593ceaa.log`. The WSL rehearse record (`wsl-593ceaa-20261123T2122Z-rehearse.txt`) was rebuilt from the lane's tee'd output after the original file was deleted by mistake.

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 4.1 | rule-3 default: a bounded JSON-RPC codec with one error table | composed test: `go -C src/sdk test -count=1 -v ./jsonrpc/...`; record an over-limit frame refused with its table code. Test mapping (plan Stage 14.2, `//dsh:test` directives): DH-T-SDK-1 `TestServeAnswersRequests`, `TestServeOneServesOneCall`, `TestServeOneRefusesASecondFrame`, `TestServeOneFirstFrame`, `TestServeOneFailures` (`jsonrpc/`), `TestPeerDrivesAConnection`, `TestPeerExpectationsFail` (`jsonrpctest/`); DH-T-SDK-2 `TestServeAnswersRequests`, `TestServeClosesOnOversizedLine`, `TestServeOneFirstFrame`; DH-T-SDK-3 `TestServeIncomingCancel`, `TestCallCancelSendsCancel` (ported onto `jsonrpctest`, `kit_test.go`), `TestPeerDrivesAConnection`, `TestPeerReleasedWhileStalled`; DH-T-SDK-4 `TestWriteStallEndsTheConnection` (`testing/synctest`: the connection ends at exactly 5 s with `jsonrpc/write-stalled` and each of three pending calls fails once with it), `TestWritesWithinTheStallKeepTheConnection`, `TestNotifyAndBarrier`, `TestPeerStall`. New table row `jsonrpc/write-stalled` (regenerated `protocol/schema.json`); `Options.MaxLineBytes` renamed `MaxFrameBytes` (no consumer outside `src/sdk`), `WriteStall` (60 s) and `Log` added. Steps 14.2.2 (`ServeOne`) and 14.2.3 (`jsonrpc/jsonrpctest`) are built ahead of DH-S-26 and DH-S-15 | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): 44 tests `--- PASS`, 0 FAIL, among them `TestServeClosesOnOversizedLine`, `TestReaderRejectsOversizedLine`, `TestDecodeRefusals`, `TestTable`, `TestPeerDrivesAConnection`; `jsonrpc` and `jsonrpctest` ok; the over-limit refusal is asserted by `TestServeClosesOnOversizedLine`, and no frame or table code was printed under `-v`. | `manual` |
| 4.2 | rule-3 default: one package describes, checks and fingerprints shapes | composed test: `./internal/schema/...`. Test mapping (plan Stage 14.4, `//dsh:test` on the new tests; the older ones are tagged by 14.5): DH-T-ZOD-1 partly (`TestTags`, `TestTagErrors` parse every tag form; no walk of every registered type for descriptions, M1-G-FND-21); DH-T-ZOD-2 `TestDocument` (field order), `TestShapeScenario`, `TestUnion` (derived twice, same fingerprint; two processes not run); DH-T-ZOD-3 `TestClosedOpenAndSubset` (`CheckSubset` lists every path), `TestToolSchemaSubset` (`llm/vocab`, now through `schema.(*Type).CheckSubset`); DH-T-ZOD-4 `TestValidateReportsEveryIssue`, `TestDecode`, `TestLocate`; DH-T-ZOD-5 `TestTolerant`, `TestDecode`; DH-T-ZOD-6 partly (`TestDurationRoundTrip` decodes onto a default; no `default` annotations are derived); DH-T-ZOD-7 partly (`TestDecode`: a `Validate()` issue keeps its path); DH-T-ZOD-8 `TestUnion`; DH-T-ZOD-9 `TestFingerprint`, `TestFingerprints` (`llm/vocab`), `TestInventory` (`session/store`); DH-T-ZOD-10 partly (`TestFromJSON`: a function-like description is data; no export command yet); DH-T-UTIL-1 `TestClosedOpenAndSubset` (the import clause has no archtest rule, M1-G-FND-14.1); DH-T-UTIL-2 `TestDurationRoundTrip`, `TestDurationsAtEveryDepth`, `TestFieldsAndChanged`, `TestDecodeForm`, `TestProject` (fake profile), and the wire tests `TestFailureRetryAfterIsText`, `TestRetryPolicyDurationsAreText`, `TestBudgetDurationIsText`. M1-D-FND-2 outcome (a): `DecodeForm` and `Project` are built; `Project` takes a caller-supplied `Profile{Drop}` table. Fingerprint change (Rule 6, the durations step 14.4.1): a `time.Duration` derives to a string with `DurationPattern` and is written as text ("2s") by `schema.Marshal`, which `vocab.Failure`, `llm.RetryPolicy` and `driver.Budget` now encode through. Regenerated in this one change: `llm/vocab/testdata/fingerprints.golden` (only `vocab.Failure` changed), `session/store/testdata/persisted.golden` (only `assistant/attempt` and `turn/end`, the two inventoried types that hold a `Failure`), `llm/testdata/sse/overloaded-retry-after.golden` (`"retry_after":"2s"`). `DH-T-STORE-10` (`TestInventory`) demanded a version bump: an older binary cannot read the text, so no "compatible" note was true. `assistant/attempt` 3 to 4 and `turn/end` 1 to 2 (`session/event/core.go`), each with an upgrade that rewrites an integer `retry_after` to its text, two lines in `persisted.notes`, two new rows in `rows.jsonl`, and `TestRetryAfterBecomesText` (`session/event`). The non-core `llm/retry` and `llm/retry-abandoned` carry durations too: both are bumped 1 to 2 with the same upgrade (`event.DurationsAsText`), and the new `session/store/allevents` inventories every durable type the harness declares (golden, notes, v1/v2 rows; a source scan fails on a declaring package it does not link), closing M1-G-FND-12.1. Against the HEAD inventory only `assistant/attempt`, `turn/end`, `llm/retry` and `llm/retry-abandoned` changed. Step 14.4.7 `Locate` is P1 (DH-S-34); impl.md names no ID for it there, so its test is tagged `DH-T-ZOD-4` (M1-G-FND-18.1) | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): 29 tests `--- PASS`, 0 FAIL, among them `TestValidateReportsEveryIssue`, `TestFingerprint`, `TestDecode`, `TestShapeScenario`, `FuzzValidate`; `internal/schema` ok. | `manual` |
| 4.3 | rule-3 default: the SDK protocol types, method table and derived schema | composed test: `go -C src/sdk test -count=1 -v ./protocol/`; record that the derived schema equals the committed file. Test mapping (plan Stage 14.3): DH-T-SDK-5 `TestSchemaMatchesGolden` (`protocol/`), and `TestEveryProtocolTypeDerives`, `TestCheckReportsEveryFailure` in `src/harness/internal/wire/protoschema` (M1-D-FND-1 outcome (a): the SDK deriver and the committed `schema.json` stay; the harness test derives every type of `protocol.Methods` with `schema.ForType` and fails on any error; the spec names the file at DH-S-15); DH-T-SDK-6 `TestRegisterMatchesTheTableBothWays`, `TestRegisterRefusals`, `TestHandleDecodesStrictly`, `TestRequests`, `TestEveryReasonIsDeclared`, `TestSDKErrorPanicsOutsideTable`; DH-T-SDK-7 `TestNegotiate`, `TestGate` (over the `jsonrpctest` peer), `TestGateInitializeRunsOnce`, `TestDecodeParams`. The bijection here is proven on fake handler sets and the full `Methods` table; the real-server bijection is re-run at DH-S-15 step 15.6 | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): 15 tests `--- PASS`, 0 FAIL, among them `TestSchemaMatchesGolden`, `TestRegisterMatchesTheTableBothWays`, `TestNegotiate`; `protocol` ok; the derived schema equals the committed file (`TestSchemaMatchesGolden`). | `manual` |
| 4.4 | rule-3 default: globs, versions, instants and recurrence rules | composed test: `./internal/util/glob/ ./internal/util/version/ ./internal/util/caltime/ ./internal/util/recur/`. Test mapping (plan Stage 14.5; tags placed by earlier stages): DH-T-PICOMATCH-1 `TestDialect`, -2 `TestOptions`, -3 `TestRefused` (`util/glob`); DH-T-SEMVER-1 `TestParse`, -2 `TestOrder`, -3 `TestRange` (`util/version`) | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): 28 tests `--- PASS`, 0 FAIL, among them `TestDialect`, `TestParse`, `TestOrder`, `TestRange`, `TestOccurrences`; `glob` 8, `version` 6, `caltime` 6 and `recur` 8 tests, every package ok. | `manual` |
| 4.5 | rule-3 default: the model vocabulary is a leaf | composed test: `./internal/llm/vocab/`, and `TestImportDirection` in `./internal/archtest/`. Test mapping (plan Stage 14.5): DH-T-LLM-1 `TestCanonicalStable`, `TestReencode`, `TestChunksSealed`, `TestRequestTypes` (`llm/vocab`), `TestImportDirection` (`archtest`). M1-D-FND-3 (a): package `llm` declares no aliases of the vocabulary types; `llm.md` DH-D-LLM-11 carries the DC-3 note (M1-G-FND-19) | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): 14 tests `--- PASS`, 0 FAIL, among them `TestCanonicalStable`, `TestReencode`, `TestChunksSealed`, `TestImportDirection`; `llm/vocab` (13) and `archtest` (1) ok. | `manual` |
| 4.6 | rule-3 default: `fsx` replaces, creates, locks and identifies files | composed test: `./internal/util/fsx/`. Test mapping (plan 14.5.1, `//dsh:test` directives): DH-T-FS-1 `TestFailureAtEachStep`, `TestRootedCreateOneWinner`, `TestCreateIfAbsentAcrossProcesses`, `TestCreateIfAbsentOneWinner`, `TestRootedReplaceAndCreate`, `TestRootedCreateWithoutLinks`, `TestReplaceWriterFailureLeavesTarget`, `TestSweepStaged`; DH-T-FS-2 `TestReaderAndEditorVerdict`, `TestIsText`, `TestEOL`; DH-T-FS-3 `TestVersionStaleness`, `TestVersion`; DH-T-FS-27 (encoding clause only) `TestUTF16RoundTrip`, `TestIsText`, `TestEncoding`; DH-T-NODEFS-1 `TestReplaceNeverExposesPartialFile` (now 1,000 replacements), `TestFailureAtEachStep`, `TestReplaceWriterFailureLeavesTarget`, `TestReplaceRetriesWhileHeldOpen`, `TestReplaceGivesUpWithErrInUse` (Windows); DH-T-NODEFS-2 `TestCreateIfAbsentOneWinner`, `TestRootedCreateOneWinner`, `TestCreateIfAbsentAcrossProcesses`; DH-T-NODEFS-3 `TestIdentity`, `TestVersion`, `TestVersionTokenChanges`, `TestIdentityShortNameAndCase` (Windows: long name, 8.3 name, upper and lower case give one identity and one canonical path); DH-T-NODEFS-4 `TestInside`, `TestContainment`. New: a test hook `faultAt` in `replace.go` and `rooted.go` fails the stage, mode, sync, close and publish steps (the write step fails through the writer), for plain and rooted replace and create and the no-link reservation path; the two-process case re-executes the test binary (`os.Executable`, `FSX_TEST_RACE_*` variables): 2 processes x 25 goroutines, plain, rooted and rooted without links, released together through a file gate polled with `testkit/procwait`; exactly one winner of 50, its content in the file, no staged file. The symbolic-link rows of `TestIdentity` and `TestContainment` run on WSL only (this Windows account may not create links; the rows log and do not run there). The tool clauses of DH-T-FS-27 are not proven and impl.md names no deferral (M1-G-FND-21.1) | Windows, WSL | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS on both lanes. Windows (`register-windows-593ceaa-20261123T2213Z.log`): 33 tests `--- PASS`, 0 FAIL, among them `TestReplaceNeverExposesPartialFile`, `TestCreateIfAbsentAcrossProcesses`, `TestIdentityShortNameAndCase`, `TestReplaceRetriesWhileHeldOpen`; the symbolic-link rows of `TestContainment` and `TestIdentity` logged `no symbolic links here` (no link privilege). WSL (`register-wsl-593ceaa-20261123T2237Z.log`): 26 tests `--- PASS`, 0 FAIL, the same tests without the Windows-only ones; `fsx` ok on both. | `manual` |
| 4.7 | rule-3 default: `textdiff` line diffs and patch format | composed test: `./internal/util/textdiff/`. Test mapping (plan 14.5.2): DH-T-FS-4 `TestParseHunks`, `TestParseChunks`, `TestParseLenient`, `TestParseErrors`, `TestFeedEqualsParse`, `TestFormatParsesBack`, `FuzzParse` (`textdiff/patch`); DH-T-FS-5 `TestApplyPasses`, `TestApplyFailures`, `TestApplyAddAndDelete`, `TestRoundTrip`, `FuzzApply`, `FuzzMake`; DH-T-FS-6 `TestLimits`, `TestLimitEdges`, `TestCoarseAboveHunkLimit`, `TestMakeLimits`; DH-T-DIFF-1 `TestGoldenCases` (new committed golden `internal/util/textdiff/testdata/hunks.golden`: insertion, deletion, replacement, two separate changes, first line, last line, identical texts), `TestLinesHunks`, `TestBeforeAfter`; DH-T-DIFF-2 `TestLimitEdges` (at and one over `MaxBytes`, `MaxLines` and `HunkMaxBytes`), `TestParallelIdentical` (100 parallel diffs equal), `FuzzLines` (new: applying the hunks to before gives after), `TestLimits`, `TestApplyInvertsLines`, `TestEqualAboveLimits`; DH-T-DIFF-3 `TestFinalNewlineAndEmpty`, `TestLinesHunks` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): 12 tests `--- PASS`, 0 FAIL, among them `TestGoldenCases`, `TestLimitEdges`, `TestLinesHunks`, `TestApplyInvertsLines`, `FuzzLines`; `textdiff` ok. | `manual` |
| 4.8 | search on a real repository: the ignored set equals Git's (re-targeted, 12.4.5) | by hand: a throwaway program or test (removed afterwards) prints `search.List` for the repository root; compare it, sorted, with `git ls-files --cached --others --exclude-standard`. WSL: in the ext4 clone with `core.autocrlf=false`, and again on `/mnt/e` ignoring `core.filemode` noise. Windows: in the host checkout. Record both counts and every difference. Run by plan 14.5.5. Test mapping (plan 14.5.2): DH-T-FS-7 `TestPatternDirectedWalk`, `TestOffsetPages`, `TestGrepOutputs`, `TestSampleSpreads`; DH-T-RIPGREP-1 `TestGrep`, `TestGrepLines`, `TestGrepNoPhantomEOFLine`, `TestGrepLongLine`; DH-T-RIPGREP-2 `TestListGolden`, `TestListSwitches`, `TestListByTime`; DH-T-RIPGREP-3 `TestGrepLimits`, `TestGrepErrors`, `TestGrepCancelPromptly`, `TestGrepCancelAfterWalk`, `TestUnreadableIsIncomplete`, `TestGrepBytesCountsConsumed`, `TestGrepBytesDeterministic`, `TestListSwitches`, `TestGrepLongLine`; DH-T-RIPGREP-4 `TestNoProcessNoAmbientConfig` (new: `RIPGREP_CONFIG_PATH` set and a recording `rg` first on `PATH`; the same results, no process run, and no file of the package imports `os/exec` or `syscall` or reads the environment), `TestConfinedSource`; DH-T-RIPGREP-5 `TestGrepLines` (placeholder), `TestNonUTF8Name` (new, Linux: a Latin-1 name is listed, matched and opened by the returned name), `TestRepeatedRunsSameOrder` (new: 20 runs, 1 to 8 workers), `TestGrepBytesDeterministic`; DH-T-RIPGREP-6 `TestPatternTable` (new: valid and invalid patterns, and the Rust-only forms with what Go does: ASCII-only `\d`, `\w` and `\b`; `[a-z&&[^aeiou]]` is a different class; `(?x)` and `\h` refused), `TestGrepErrors`; DH-T-RIPGREP-7 `TestIgnoreRules`, `TestIgnoreAboveRoot`, `TestIgnoreFileFailures`, `TestListGolden` | Windows, WSL | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | seen: equal except the known difference M1-G-FND-23.1 (open) (`byhand-windows-593ceaa-part2.log`, `byhand-wsl-593ceaa.log`). Windows (git 2.56.0.windows.4, `core.autocrlf=false`): git 1625, search 2775; only in git, the two gitlinks `.worktree/plan-harness-DSH-e2e-scenarios` and `.worktree/plan-harness-DSH-implementation-plan`; only in search, 576 files inside each of those two nested worktrees. WSL ext4 clone (git 2.43.0): git 1625, search 1623; only in git, the same two gitlinks (empty directories in the clone); only in search, nothing. WSL `/mnt/e`: git 1625, search 2775, the same difference as Windows. A pass relies on M1-G-FND-23.1 being accepted as a known difference. | `manual` |
| 4.9 | watcher with a real editor: one notice per save (re-targeted, 12.4.5) | by hand: watch a file and save it three times from each editor. Windows: a real editor (VS Code) on a local NTFS path. WSL: vim on an ext4 path (inotify). WSL: a Windows editor saving a file under `/mnt/e` (9p is probed as remote by `fsx/remote_linux.go`, so the watcher polls). Record the notice count per save and the mode (event or poll). Run by plan 14.5.5. Test mapping (plan 14.5.3): DH-T-CHOKIDAR-1 `TestFileTransitions`, `TestAtomicReplaceOneNotice` (Notify and Poll); DH-T-CHOKIDAR-2 `TestDirectoryDepth`, `TestDirectoryAtomicReplaceSameMeta`; DH-T-CHOKIDAR-3 `TestMissingParents`, `TestRecreatedParentWithinSettle`, `TestFakeRearmOnDirectoryEvent`, `TestRearmRefusedDegrades`; DH-T-CHOKIDAR-4 `TestReconcileOnReady`, `TestCloseStopsCallbacks`; DH-T-CHOKIDAR-5 `TestDegradedNotifications`, `TestRearmRefusedDegrades`, `TestBadTargets` (overflow and silence stay in Notify, as the design says and the test text does not: M1-G-FND-21.2); DH-T-CHOKIDAR-6 `TestSymlinkSameTarget` (WSL; no link privilege on this Windows account), `TestShortNameAndCaseOneWatch` (new, Windows: the long name, upper case and the 8.3 name are one registered directory and each watch gets one notice); DH-T-UTIL-6 `TestKeyContract` (the real component and `watchtest.Manual` pass one table), `TestOwnedRefusals`, `TestUnloadClosesWatch`, `TestOnlyOwnerCloses`, `TestServiceUnloadClosesAll` (`watchfiber`), `TestManualCovers`, `TestManualDegradeAndClose` (`watchtest`) | Windows, WSL | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | (a) Windows VS Code on NTFS: PENDING: owner GUI saves. (b) WSL ext4, vim 9.1 (`byhand-wsl-593ceaa.log`): three saves with `vim -es -c 'normal Ax' -c wq`, 3 s apart: `watching ... mode=notify`, notices 1, 2 and 3 at 3.22 s, 6.23 s and 9.29 s, `total notices 3 mode=notify` (one notice per save): PASS. (c) WSL `/mnt/e` with a Windows editor: PENDING: owner GUI saves. | `manual` |
| 4.10 | rule-3 default: the YAML codec in the harness module | composed test: `TestSameEntriesThroughJSONAndYAML`, `TestEncodeLayerKeepsComments` in `./internal/boot/config/`. Test mapping (plan Stage 14.5): DH-T-LOADER-2 `TestSameEntriesThroughJSONAndYAML` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestSameEntriesThroughJSONAndYAML`, `TestEncodeLayerKeepsComments`. | `manual` |
| 4.11 | rule-3 default: one YAML dialect, edits keep comments | composed test: `TestEditKeepsComments`, `TestEditRefusesUnverifiedText` in `./internal/boot/yamlx/`. Test mapping (plan Stage 14.5): DH-T-YAML-2 `TestParseRejectsWithPosition`, -3 `TestScalarTyping`, -4 `TestEditOperations`, -5 `TestEditIsStable`, -6 `TestEditRefusesUnverifiedText`, -7 `TestLimits`; DH-T-BOOT-9 and DH-T-YAML-1 are named by no test (M1-G-FND-21.3) | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestEditKeepsComments`, `TestEditRefusesUnverifiedText`. | `manual` |
| 4.12 | rule-3 default: the expression engine has no side effects | composed test: `TestNamesResolve`, `TestRejected` in `./internal/boot/expr/`. Test mapping (plan Stage 14.5): DH-T-LOADER-6 `TestNamesResolve`, `TestOnlyPureBuiltins` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestNamesResolve`, `TestRejected`. | `manual` |
| 4.13 | rule-3 default: logging writes to the configured destination only | composed test: `TestFileDestinationKeepsStdoutClean`, `TestDebugLevelReachesTheDestination` in `./internal/boot/logging/`. Test mapping (plan Stage 14.5): DH-T-LOADER-13 `TestFileDestinationKeepsStdoutClean` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestFileDestinationKeepsStdoutClean`, `TestDebugLevelReachesTheDestination`. | `manual` |
| 4.14 | rule-3 default: a build knows its components by name | composed test: `TestCatalogListsComponentsByName`, `TestLookupAndPlatforms` in `./internal/boot/catalog/`. Test mapping (plan Stage 14.5): DH-T-BOOT-1 `TestBuildRejects`, `TestLookupAndPlatforms`; DH-T-BOOT-2, -3 and -22 are named by no test (M1-G-FND-21.3). Plan 14.5.6 (M1-G-FND-20): `catalog.Families` is `[]Family{Name, Area}` with each family's owning area key (`families.go`), `Print` writes an `AREA` column, the golden `internal/boot/catalog/testdata/catalog.golden` is regenerated, and `TestFamiliesHaveAreas` checks that every family is registered once with one area key; the built-in list still lives in `internal/boot/builtin` (stated deviation) | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestCatalogListsComponentsByName`, `TestLookupAndPlatforms`. | `manual` |

#### Superseded register values (historical)

The register held these Operator, Commit and Seen values before plan 18.1.5. They are kept as history, labelled `manual` and not closure evidence, and they do not fill a row (the runbook: nothing is carried over from an older commit).

| Step | Operator (then) | Commit (then) | Seen (then) |
| --- | --- | --- | --- |
| 4.1 | -- | uncommitted on `e77d26b` (plan 14.2) | Windows, go1.27.4: `go -C src/sdk test -race -shuffle=on -count=5 ./...` ok; coverage `jsonrpc` 98.1%, `jsonrpctest` 92.7%; agent run, not an operator check |
| 4.2 | -- | uncommitted on `0e94ba9` (plan 14.4) | Windows, go1.27.4: `CGO_ENABLED=1 go -C src/harness test -race -shuffle=on -count=3` ok on the 30 packages that touch or import `internal/schema` or `llm/vocab`; the 32 packages that touch or import `session/event`, `session/store` or `llm/retry` also pass it after the M1-G-FND-12.1 bump; coverage `schema` 93.0%, `llm/vocab` 88.3%, `llm` 88.7%, `agent/driver` 88.1%, `storage/settings` 91.1%, `session/event` 88.2% and `session/store` 94.3% (both merged over `./internal/session/...`); gofmt, harness-gate vet (`-tags=`, `replay,scenario`, `e2e`) and staticcheck clean on the touched packages; agent run, not an operator check |
| 4.3 | -- | uncommitted on `e77d26b` (plan 14.3) | Windows, go1.27.4: `go -C src/sdk test -race -shuffle=on -count=5 ./...` ok, coverage `protocol` 97.9%; `go -C src/harness test -race ./internal/wire/protoschema/` ok, coverage 100.0% (against the in-progress 14.4 `internal/schema`); agent run, not an operator check |
| 4.4 | -- | uncommitted on `b59ea4a` (plan 14.5) | Windows, go1.27.4: `go -C src/harness test -race -count=1 -cover` ok; coverage `glob` 94.0%, `version` 100.0%, `caltime` 94.6%, `recur` 94.3%; agent run, not an operator check |
| 4.5 | -- | uncommitted on `b59ea4a` (plan 14.5) | Windows, go1.27.4: `-race -count=1 -cover` ok; coverage `llm/vocab` 88.3%, `archtest` 97.8%; agent run, not an operator check |
| 4.6 | -- | uncommitted on `b59ea4a` (plan 14.5) | Windows 11 (10.0.28000), go1.27.4: `CGO_ENABLED=1 go -C src/harness test -race -shuffle=on -count=3` ok on `fsx`; coverage `fsx` 92.6%; WSL Ubuntu 24.04.5, go1.27.4, ext4 clone: the same run ok; coverage `fsx` 90.2%; gofmt, harness-gate vet (built from `git archive HEAD tools/harness-gate`; `-tags=`, `replay,scenario`, `e2e`) and staticcheck (`-tags=`, `replay,scenario`; the Linux files also with `GOOS=linux`) clean on the touched packages; `harness-gate trace-tests --stage DH-S-4`: steps 4.6-4.9 required 29, tagged 29, deferred 0, missing 0; gate run ids: Windows lane `<run id: lead>`, WSL lane `<run id: lead>`; agent run, not an operator check |
| 4.7 | -- | uncommitted on `b59ea4a` (plan 14.5) | Windows: `-race -shuffle=on -count=3` ok on `textdiff/...`; coverage `textdiff` 99.2%, `textdiff/patch` 98.4%; WSL: the same run ok; coverage `textdiff` 99.2%, `textdiff/patch` 98.4%; `FuzzLines` fuzzed 15 s on Windows (12.9 million executions, no failure); agent run, not an operator check |
| 4.8 | none (agent run, 2026-11-22) | uncommitted on `b59ea4a` (plan 14.5); the WSL clone was at `b59ea4a` with the 14.5 packages copied in | `manual` check record: a throwaway program (`search.List`, `Hidden: true`, no limits; built in the WSL clone for Linux and Windows and removed) against `git ls-files --cached --others --exclude-standard`, both sorted. **WSL** Ubuntu 24.04.5 LTS, kernel 6.18.58.1-microsoft-standard-WSL2, git 2.43.0. The ext4 clone, `core.autocrlf=false`: Git 1,440, search 1,438; only in Git, the two gitlinks `.worktree/plan-harness-DSH-e2e-scenarios` and `.worktree/plan-harness-DSH-implementation-plan` (empty directories in the clone); only in search, none. `/mnt/e` (v9fs), `-c core.filemode=false`: Git 1,443, search 2,593; only in Git, the same two gitlinks; only in search, the 1,152 files inside those two Git worktrees (576 each). **Windows** 11 (10.0.28000), NTFS, git 2.56.0.windows.4, `core.autocrlf=false`, the host checkout: Git 1,443, search 2,593, the same difference. Every other path agrees (the shared tree has 3 untracked files more than the clone). The difference, search entering nested Git repositories, is M1-G-FND-23.1; Windows: `-race -shuffle=on -count=3` ok on `search`; coverage 96.4%; WSL: `-race -shuffle=on -count=3` ok on `search` (with `TestNonUTF8Name`); coverage 96.7%; agent run, not an operator check |
| 4.9 | none (agent run, 2026-11-22) | uncommitted on `b59ea4a` (plan 14.5) | `manual` check record: a throwaway program (`watch.New` with default options, one file target in `Auto` mode, printing the mode and every notice; removed afterwards) while the file was saved 3 times, 2 s apart, with `vim -es -c 'normal Ax' -c wq <file>`. (1) **WSL** Ubuntu 24.04.5 LTS, kernel 6.18.58.1-microsoft-standard-WSL2, vim 9.1, ext4: mode `notify`, notices 1/1/1 (3 for 3 saves), content read back `linexxx`. (2) WSL vim on `/mnt/e` (v9fs, a directory outside the repository): mode `poll`, 1/1/1. (3) a Windows editor on `/mnt/e`: vim 9.2 of Git for Windows, run on Windows, saving the `E:` file that the WSL watcher watched through `/mnt/e`: mode `poll`, 1/1/1. (4) **Windows** 11 (10.0.28000) native, NTFS, vim 9.2 of Git for Windows: mode `notify`, 1/1/1. **Not run, needs an operator:** a VS Code save on Windows. Procedure: build the program above (`go build` from `src/harness`, its source under `internal/` removed afterwards), run `dsh-manual-watch.exe %TEMP%\dsh-watch\settings.json 60` on a local NTFS path, open that file in VS Code, type a character and press Ctrl+S three times a few seconds apart, and record the mode line and the notice count per save (want `notify`, one each); then delete the file and the program. No Windows GUI editor (VS Code, Notepad) can be driven to save non-interactively from this session; Git for Windows vim was the only Windows editor driven, and the VS Code row is not a pass; Windows: `-race -shuffle=on -count=3` ok on `watch/...` (483 s); coverage `watch` 92.7%, `watchfiber` 100.0%, `watchtest` 94.1%; WSL: `-race -shuffle=on -count=3` ok on `watch/...` (468 s); coverage `watch` 93.5%, `watchfiber` 100.0%, `watchtest` 93.4%; agent run, not an operator check |
| 4.10 | -- | uncommitted on `b59ea4a` (plan 14.5) | Windows, go1.27.4: `-race -count=1 -cover` ok; coverage `config` 87.4%; agent run, not an operator check |
| 4.11 | -- | uncommitted on `b59ea4a` (plan 14.5) | Windows, go1.27.4: `-race -count=1 -cover` ok; coverage `yamlx` 96.1%; agent run, not an operator check |
| 4.12 | -- | uncommitted on `b59ea4a` (plan 14.5) | Windows, go1.27.4: `-race -count=1 -cover` ok; coverage `expr` 99.4%; agent run, not an operator check |
| 4.13 | -- | uncommitted on `b59ea4a` (plan 14.5) | Windows, go1.27.4: `-race -count=1 -cover` ok; coverage `logging` 98.6%; agent run, not an operator check |
| 4.14 | -- | uncommitted on `b59ea4a` (plan 14.5) | Windows, go1.27.4: `-race -count=1 -cover` ok; coverage `catalog` 96.2% (`overlap`, untouched, 33.3%), `builtin` 100.0%; agent run, not an operator check |

### Held open

A held-open entry is a part of a deliverable that is ticked as built but is not done:
the part that waits is not enabled until its closing step lands. H-38 is the M1
story's entry for this stage: decision M1-D-CLOSE-5 defers `DH-T-ZOD-1`, which no M1
code proves, to the DH-S stage that first needs it, by a
`//dsh:test DH-T-ZOD-1 later DH-S-12` directive in `internal/schema/doc.go`. impl.md
carries the pointer line in its DH-S-12 section (DC-2). The deferred ID stays open and
is never counted done; the closing stage records the entry in its own evidence record.

| ID | Step | Deliverable | The part held open | Gap | Closing step | Status |
| --- | --- | --- | --- | --- | --- | --- |
| H-38 | 4.2 | DH-D-UTIL-1 | `DH-T-ZOD-1`: a walk of every registered config, tool, message and event type finding a description on each exported field and a parsable `dsh` tag, and the repository check for hand-written JSON Schema literals. No Go-declared shape of the product carries a `jsonschema:"..."` description yet, so the clause fails for every registered type | M1-G-FND-21.4 | DH-S-12 (impl.md DH-S-12, "Deferred tests (pointer, M1)"), with the file tools of 12.5; test deferred `later DH-S-12` (M1-D-CLOSE-5) | open |

### Cleanup evidence

**At the closure commit (plan 18.1.7).** Every throwaway worktree of the manual checks was removed, and `git status --porcelain` of the main tree was empty before and after. The signed-in runs left no `claude` or `copilot` process (PID sets equal before and after). The WSL clone's tags (7, all created 2026-11-17) were unchanged, and nothing was published.

At closure: the throwaway search program is removed and the watched files are deleted;
`git status --porcelain` empty after the run. Historical: none recorded.

### Open items

**At the closure commit (plan 18.1.7).** For this stage: H-38 (`DH-T-ZOD-1`, deferred to DH-S-12 by M1-D-CLOSE-5) and register row 4.9 cases (a) and (c), pending the owner's GUI saves; M1-G-FND-23.1 (row 4.8). The M1-wide list is [DH-S-11 "Open items at M1"](DH-S-11.md#open-items-at-m1-plan-18111).

At closure: plan 18.1.11 lists them. Now open: the 11 IDs of steps 4.2, 4.11 and 4.14 that
no test names (M1-G-FND-21.3); the tool clauses of DH-T-FS-27 (M1-G-FND-21.1); the
DH-T-CHOKIDAR-5 text against the design (M1-G-FND-21.2); search entering nested Git
repositories (M1-G-FND-23.1); the VS Code save on Windows, which needs an operator
(register row 4.9); the gate run ids of Stage 14.5 (filled by the lead); the scenarios
authored here, which run in DH-S-18; the vet findings under "Known limitations" (plan
Phase 13); `DH-T-ZOD-1`, held open as H-38 and deferred to DH-S-12 (["Held
open"](#held-open), M1-D-CLOSE-5).

**Phase 14 gate runs (Rule 2).** Both lanes pass all 12 gates at `08233be`: run records `wsl-08233be-20261123T0215Z` and `win-08233be-20261123T0223Z` (`manual`; details in [DH-S-1.md](DH-S-1.md), "Phase 14 exit gate runs").
