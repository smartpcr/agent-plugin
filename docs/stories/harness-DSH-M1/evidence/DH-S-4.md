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
- The catalog's families are names only; their owning areas are not yet recorded.
- The built-in list lives in `internal/boot/builtin` (not beside `cmd/dsh`) so the
  catalog golden test can import it.
- The harness gate's vet step currently fails on files from earlier stages
  (`src/go/cordis` tests, the `recur`, `glob`, `caltime`, `version` fuzz seeds and
  `watchtest/manual_test.go`); none of the files of this stage are reported.

## Evidence record (impl.md sec. 7.3)

These are the eight fields of [impl.md sec. 7.3](../../../harness/impl.md#73-the-evidence-record),
in the format of [the evidence runbook](README.md#the-evidence-record-headings). Plan step
18.1.7 fills them at the frozen M1 closure commit; plan Stage 14.5 adds the test mapping
for steps 4.1 to 4.9 and the two manual-check records before that. Until then each field
points to the history above, which stays exactly as it was recorded and is not closure
evidence (M1-D-CLOSE-1).

### Source and build identity

At closure: not yet recorded (plan 18.1.7). Historical: no commit was recorded; the module
pins are under "What changed" of Stage 4.4.

### Environment and fixture

At closure: both local lanes, from their run records, and for the manual checks the
distribution, kernel and editor of each save (plan 14.5.5). macOS: out of scope
(`platforms-windows-wsl`). Historical: one local machine. Fixtures: the committed goldens
`internal/boot/yamlx/testdata/edit_keep_comments.golden` and
`internal/boot/catalog/testdata/catalog.golden`.

### Exact tests and commands

At closure: the lane scripts, and `go -C src/harness test -race -count=1 -cover ./...` and
`go -C src/sdk test -race -count=1 ./...`. Historical: the `go test` lines under "Tests"
of Stage 4.4 and "DH-S-4 exit: import direction".

### Run identifiers

At closure: the run-record ids of both lanes, in the runbook's template (plan 18.1.3).
WSL lane: not run. Windows lane: not run. macOS: out of scope (`platforms-windows-wsl`).
Historical: no run id was recorded. The scenarios authored here run in DH-S-18 and have
no run id yet.

### Results

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

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 4.1 | rule-3 default: a bounded JSON-RPC codec with one error table | composed test: `go -C src/sdk test -count=1 -v ./jsonrpc/`; record an over-limit frame refused with its table code | one lane | | | | `manual` |
| 4.2 | rule-3 default: one package describes, checks and fingerprints shapes | composed test: `./internal/schema/...` | one lane | | | | `manual` |
| 4.3 | rule-3 default: the SDK protocol types, method table and derived schema | composed test: `go -C src/sdk test -count=1 -v ./protocol/`; record that the derived schema equals the committed file | one lane | | | | `manual` |
| 4.4 | rule-3 default: globs, versions, instants and recurrence rules | composed test: `./internal/util/glob/ ./internal/util/version/ ./internal/util/caltime/ ./internal/util/recur/` | one lane | | | | `manual` |
| 4.5 | rule-3 default: the model vocabulary is a leaf | composed test: `./internal/llm/vocab/`, and `TestImportDirection` in `./internal/archtest/` | one lane | | | | `manual` |
| 4.6 | rule-3 default: `fsx` replaces, creates, locks and identifies files | composed test: `./internal/util/fsx/` | Windows, WSL | | | | `manual` |
| 4.7 | rule-3 default: `textdiff` line diffs and patch format | composed test: `./internal/util/textdiff/` | one lane | | | | `manual` |
| 4.8 | search on a real repository: the ignored set equals Git's (re-targeted, 12.4.5) | by hand: a throwaway program or test (removed afterwards) prints `search.List` for the repository root; compare it, sorted, with `git ls-files --cached --others --exclude-standard`. WSL: in the ext4 clone with `core.autocrlf=false`, and again on `/mnt/e` ignoring `core.filemode` noise. Windows: in the host checkout. Record both counts and every difference. Run by plan 14.5.5 | Windows, WSL | | | | `manual` |
| 4.9 | watcher with a real editor: one notice per save (re-targeted, 12.4.5) | by hand: watch a file and save it three times from each editor. Windows: a real editor (VS Code) on a local NTFS path. WSL: vim on an ext4 path (inotify). WSL: a Windows editor saving a file under `/mnt/e` (9p is probed as remote by `fsx/remote_linux.go`, so the watcher polls). Record the notice count per save and the mode (event or poll). Run by plan 14.5.5 | Windows, WSL | | | | `manual` |
| 4.10 | rule-3 default: the YAML codec in the harness module | composed test: `TestSameEntriesThroughJSONAndYAML`, `TestEncodeLayerKeepsComments` in `./internal/boot/config/` | one lane | | | | `manual` |
| 4.11 | rule-3 default: one YAML dialect, edits keep comments | composed test: `TestEditKeepsComments`, `TestEditRefusesUnverifiedText` in `./internal/boot/yamlx/` | one lane | | | | `manual` |
| 4.12 | rule-3 default: the expression engine has no side effects | composed test: `TestNamesResolve`, `TestRejected` in `./internal/boot/expr/` | one lane | | | | `manual` |
| 4.13 | rule-3 default: logging writes to the configured destination only | composed test: `TestFileDestinationKeepsStdoutClean`, `TestDebugLevelReachesTheDestination` in `./internal/boot/logging/` | one lane | | | | `manual` |
| 4.14 | rule-3 default: a build knows its components by name | composed test: `TestCatalogListsComponentsByName`, `TestLookupAndPlatforms` in `./internal/boot/catalog/` | one lane | | | | `manual` |

### Cleanup evidence

At closure: the throwaway search program is removed and the watched files are deleted;
`git status --porcelain` empty after the run. Historical: none recorded.

### Open items

At closure: plan 18.1.11 lists them. Now open: the test mapping for steps 4.1 to 4.9 and
both manual checks (plan Stage 14.5); the scenarios authored here, which run in DH-S-18;
the vet findings under "Known limitations" (plan Phase 13).
