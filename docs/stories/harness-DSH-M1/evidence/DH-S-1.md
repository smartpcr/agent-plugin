# DH-S-1 evidence

Decisions governing this record: [decisions.md](../decisions.md), chiefly `platforms-windows-wsl` (the gate lanes are Windows native and Linux under WSL; the macOS rows below are historical) and `models-signed-in-claude-copilot` (the preflight no longer asks for a model key).

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The stage records below are history: under M1-D-CLOSE-1 every tick made while the gate
was red is provisional and is re-verified at that commit. The fields of impl.md sec. 7.3
and the per-step manual-check register are at the end of this file, in "Evidence record
(impl.md sec. 7.3)"; how to fill them is in [the evidence runbook](README.md).

**Scope change (2026-11-22, decision `platforms-windows-wsl`).** impl.md DH-S-1 asks for
the gate to pass "on Linux, macOS and Windows". For M1 the gate of record runs on two
systems: Windows (native) and Linux under WSL on the development host. macOS has left M1
scope: a macOS row reads "out of scope (`platforms-windows-wsl`)" and is never a pass or a
skip. No stage brings macOS back yet (`macos-return-stage`, open). Cross-compiling
`cmd/dsh` for macOS stays a gate (QG-8). The upstream rules this overrides carry DC-3
notes in [toolchain.md](../../../harness/upstream/toolchain.md#dh-d-toolchain-4).

## Toolchain scope decisions

Where the toolchain built in DH-S-1 differs from
[toolchain.md](../../../harness/upstream/toolchain.md), this table says what was built,
what the spec asks for, which decision in [decisions.md](../decisions.md) covers the
difference, and where the difference ends (gap-closure plan steps 12.2.1 and 12.2.4). A
row whose decision is `open` follows the plan's default until the owner answers; the
default is not in force.

| Item | What was built (DH-S-1 closing commit `e13abf3`) | What the spec asks for | Decision (status) | Return stage |
| --- | --- | --- | --- | --- |
| Gate list | 8 gates: `gofmt`, `vet` (with the own analyzers), `test`, `coverage`, `trace`, `scenario-index`, `budget`, `cross-compile` | DH-ADR-TOOLCHAIN-8 and -9, DH-D-TOOLCHAIN-2: also staticcheck, module tidy and verify, the vulnerability check and replay | M1-D-GATE-23, M1-D-TOOL-1 (both open; default: build the missing gates now, growing the list to 12 with `modules`, `staticcheck`, `vulncheck`, `self`) | plan 13.4, 13.5; replay by `replay-gate-step-dh-s-16` below |
| Module hygiene | `modcheck` tests: three modules, one `go` floor, no `replace`, pins resolve, the release build ignores `go.work`. No `go mod tidy -diff` or `go mod verify` gate | DH-ADR-TOOLCHAIN-11: tidy and verify as gates; tools pinned in a separate tools module | M1-D-GATE-23 (open); M1-D-TOOL-2 (open; default: `tools/harness-gate/go.mod` is the tools module) | plan 13.4 |
| Site exceptions | None: the own analyzers have no exception form at all (and no baseline file) | DH-ADR-TOOLCHAIN-8: an exception written at the site with a reason; a test fails on one without a reason and prints the total | M1-D-GATE-23 (open; the QG-2 "no exemption list" tension gets a DC-3 note in plan 13.5.8) | plan 13.5 |
| Release rehearsal | Built in part: `harness-gate rehearse` builds six pure-Go binaries with `go build`, prints each SHA-256 and embedded version, publishes nothing; `release.yml` and `.goreleaser.yaml` unchanged | DH-D-TOOLCHAIN-5: a GoReleaser snapshot with archives and a checksum file, and each archive that matches a runner starts and answers a keyless replay | M1-D-TOOL-3 (open; default: a GoReleaser snapshot at plan 19.1, before DH-S-18) | plan 19.1.1; run by DH-E-TOOLCHAIN-2 at DH-S-18 |
| Coverage floor | Stricter than the spec: 85% for every package of the three product modules, `main` packages included | DH-ADR-TOOLCHAIN-7: 85% for every package under `src/harness/internal`; `main` packages excluded by rule | `coverage-floor-all-packages-kept` (in force, lead settlement; DC-3 record, rule kept) | none: the stricter rule stays |
| Import rules | Go tests: `src/harness/internal/archtest/rules.go`, run by `TestImportDirection` in the `test` gate | DH-ADR-TOOLCHAIN-8 lists them as an own analyzer; DH-ADR-TOOLCHAIN-9 allows structural rules as Go tests | `import-rules-by-archtest` (in force, lead settlement) | none: met. The other three missing own analyzers are built by plan 13.5.4 |
| HTTP-client rule | Not in DH-S-1 | DH-ADR-TOOLCHAIN-8: an analyzer owned by the area that states the rule | `http-client-rule-owner` (in force, lead settlement): util's `DH-T-UTIL-22` for DH-D-UTIL-10 | impl.md step 23.16 (DH-S-23) |
| Golden files | Checked inside the `test` gate (`golden.Assert` runs in plain `go test`); no separate gate | DH-D-TOOLCHAIN-2 lists golden files as one of the runner's checks | `goldens-in-test-gate` (in force, lead settlement); the local lanes set `CI=1` so `-update` is refused there | plan 12.3.5 (`CI=1`) |
| Replay gate and `harness-replay` job | Neither exists, and no impl.md stage schedules them | DH-D-TOOLCHAIN-4, DH-ADR-TOOLCHAIN-12, `DH-T-TOOLCHAIN-5` | `replay-gate-step-dh-s-16` (open: a proposed impl.md step 16.11, which the owner signs under DC-2) | DH-S-16; built by plan 19.1.2 |
| Platform lanes | Hosted `ubuntu-latest`, `macos-latest`, `windows-latest` (historical) | DH-D-TOOLCHAIN-4, DH-ADR-TOOLCHAIN-6 and -12: Linux, macOS and Windows; no lane switched off without a failing job | `platforms-windows-wsl` (in force); hosted lanes kept by M1-D-GATE-21 / M1-D-TOOL-5 (open) | plan 12.3; macOS: `macos-return-stage` (open, no stage) |

## Stage 1.1 -- Three Module Workspace (manual check)

| Field | Value |
| --- | --- |
| Kind | `manual` |
| Commit | `a647d4984790e200c6ccae7067abf8e7c905cef5` (stage-1.1 implementation; the tree was extracted with `git archive a647d4984790` and checked in isolation) |
| Toolchain | `go version go1.27.4 windows/amd64`, `GOPROXY=off` |
| Date | 2026-11-19 |

From the repository root of that tree (no root module; `go.work` selects the three
modules):

```text
$ go build ./src/go/... ./src/sdk/... ./src/harness/...
(exit 0)
$ go list -m
github.com/smartpcr/agent-plugin/src/go
github.com/smartpcr/agent-plugin/src/sdk
github.com/smartpcr/agent-plugin/src/harness
$ GOWORK=off go -C src/harness build ./cmd/dsh
(exit 0)
$ ./src/harness/dsh
dsh (devel)
(exit 0)
```

### Follow-up: real module pins

At `a647d49`, `src/harness/go.mod` still named the zero placeholder
`v0.0.0-00010101000000-000000000000`. The next commit re-pins both requirements to
pseudo-versions of `a647d49` itself -- the first commit containing `src/sdk` and the
`go 1.26.0` `src/go` -- with `GOWORK=off go get <module>@a647d4984790e200c6ccae7067abf8e7c905cef5`,
which wrote `src/harness/go.sum`:

```text
github.com/smartpcr/agent-plugin/src/go  v0.1.5-0.20261119220503-a647d4984790
github.com/smartpcr/agent-plugin/src/sdk v0.0.0-20261119220503-a647d4984790
```

The same two commands were re-run on that tree with the same results. The
automated counterparts, run with `GOWORK=off go -C tools/harness-gate test ./modcheck`,
are `TestWorkspaceBuildsThreeModules`, `TestReleaseBuildIgnoresWorkspace`,
`TestPinsResolveToCommittedModules` (each pin's revision, timestamp, module path and
go.sum hash, checked offline against git history), and the `DH-T-TOOLCHAIN-11` /
`DH-T-SDK-17` rule tests. The TC-02b golden
(`src/go/cmd/cordis-sample/testdata/output.golden`) is in `a647d49`, before any DH-S-2
edit, and passes `go -C src/go test -count=20 ./cmd/cordis-sample`.

### Stage 1.1 addendum: tools module and module hygiene (plan stage 13.4)

`manual`, **not closure evidence**. Plan Stage 13.4 (M1-G-TOOL-2, -3, -5, -10) under
M1-D-TOOL-2 (a), M1-D-TOOL-4 (A) and M1-D-GATE-23 (A). Recorded on the Windows
development host on the uncommitted tree over `035bd97`, branch `m1-phase-13`; the
lead's stage commit is the build identity.

| Field | Value |
| --- | --- |
| Kind | `manual` |
| Base commit | `035bd97` (plus the stage 13.4 working-tree changes) |
| Toolchain | `go1.26.0` (`GOTOOLCHAIN=go1.26.0`; host default go1.27.4) |
| Date | 2026-11-22 |

**What changed.**

- `tools/harness-gate/go.mod` is the tools module. It pins staticcheck
  (`honnef.co/go/tools` v0.8.2, release 2026.2.2) and govulncheck
  (`golang.org/x/vuln` v1.10.0) as `tool` lines. Both are the newest releases
  (`go list -m -versions`), and both build and run under go1.26.0. `golang.org/x/tools`
  stays at v0.53.0, which govulncheck requires; staticcheck needs v0.51.0. The DC-3
  note under DH-D-TOOLCHAIN-1 in `toolchain.md` names the file.
- `src/go/test/e2e` is a nested test-only module. It requires godog and `src/go`
  through a reasoned `replace => ../..`. `src/go/go.mod` now requires nothing, and its
  empty `go.sum` is removed. The module is outside `go.work`, so it runs with
  `GOWORK=off`. No `e2e-*.yml` workflow, docker-compose file or azure-pipeline file ran
  `src/go`'s suites, so no path in them changed; only the `ci.yml` `cache:` comment
  was corrected.
- `modcheck` gains the test-module and tools-module classes,
  `CheckCordisStdlib` (also enforced by `CheckToolchain11`), `CheckReasons` and
  `CheckRepoReasons`.
- New gates `modules` (after `vet`) and `self` (last). The list has 10 gates:
  `gofmt, vet, modules, test, coverage, trace, scenario-index, budget,
  cross-compile, self`. `gofmt` also checks the test module's files.

**New reason comments** (in full in each file):

| File | Line | Reason, in short |
| --- | --- | --- |
| `tools/harness-gate/go.mod` | `github.com/cucumber/godog v0.16.0` | Gherkin runner of the gate's own e2e suites; one version across the repository; test code only |
| `tools/harness-gate/go.mod` | `golang.org/x/tools v0.53.0` | analysis framework of the own analyzers (QG-2); also the floor the pinned tools require |
| `tools/harness-gate/go.mod` | `golang.org/x/vuln v1.10.0`, `honnef.co/go/tools v0.8.2` | the version pins of the two `tool` lines |
| `tools/harness-gate/go.mod` | `tool golang.org/x/vuln/cmd/govulncheck` | vulnerability check of the `vulncheck` gate (M1-D-TOOL-1 (a)); newest release, verified on the floor |
| `tools/harness-gate/go.mod` | `tool honnef.co/go/tools/cmd/staticcheck` | linter of the `staticcheck` gate (DH-ADR-TOOLCHAIN-8); newest release, verified on the floor; pinned, not installed per machine |
| `src/harness/go.mod` | `github.com/cucumber/godog v0.16.0` | Gherkin runner of `test/e2e/harness-DSH`; never linked into `dsh` |
| `src/harness/go.mod` | `.../src/go`, `.../src/sdk` pins | product modules pinned to committed revisions, never replaced (DH-ADR-26, RM-4) |
| `src/go/test/e2e/go.mod` | godog, `src/go`, `replace => ../..` | the suites' runner; the module under test; the replace tests the working tree and cannot leak, since the module is never released |

**Commands and output.**

```text
$ GOWORK=off GOTOOLCHAIN=go1.26.0 go list -m -versions honnef.co/go/tools   # last: v0.8.2
$ GOWORK=off GOTOOLCHAIN=go1.26.0 go list -m -versions golang.org/x/vuln    # last: v1.10.0
$ GOWORK=off GOPROXY=off GOTOOLCHAIN=go1.26.0 go -C tools/harness-gate tool staticcheck -version
staticcheck.exe 2026.2.2 (0.8.2)
$ GOWORK=off GOTOOLCHAIN=go1.26.0 go -C tools/harness-gate tool govulncheck -version
Go: go1.26.0
Scanner: govulncheck@v1.10.0
$ GOWORK=off go -C src/go mod tidy -diff          # (no output, exit 0)
$ GOWORK=off go -C src/go/test/e2e vet -tags e2e ./...   # (exit 0)
$ GOWORK=off go -C src/go/test/e2e test -tags e2e -count=1 ./...
ok  	github.com/smartpcr/agent-plugin/src/go/test/e2e/harness-DSH	38.090s
$ GOWORK=off go -C tools/harness-gate vet ./...   # (exit 0)
$ GOWORK=off go -C tools/harness-gate test -count=1 -timeout 25m -skip TestRepositoryPassesEveryGate ./...
ok  	github.com/smartpcr/agent-plugin/tools/harness-gate	393.548s
ok  	github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance	0.830s
ok  	github.com/smartpcr/agent-plugin/tools/harness-gate/analyzers	35.274s
ok  	github.com/smartpcr/agent-plugin/tools/harness-gate/modcheck	30.684s
```

The stage's five scenarios, run with `go test -count=1 -v -run ...`, all `PASS`:

| Scenario | Test |
| --- | --- |
| Untidy module fails the modules gate | `TestSeededViolationFailsNamedGate/untidy-module` (`testdata/untidy-module`: the built gate stops at `modules`, prints the `-require golang.org/x/mod v0.43.0` diff, exits 1) |
| Pin without a reason is named | `TestReasonsNamePinWithoutReason`, `TestReasonsCoverEveryPinVerb`, `TestModulesGateNamesUnreasonedPin` (`go.mod:5: require golang.org/x/mod v0.43.0: no reason comment ...`) |
| src/go is standard-library only | `TestCordisModuleIsStandardLibraryOnly`, `TestToolchain11TestModuleClass`, and the e2e vet above |
| A broken module rule fails the main gate | `TestModulesGateNamesRM4` (a scratch copy of the three product modules plus `go.work`: it passes unchanged, then fails naming `replace ...: replace directive is not allowed (RM-4)` with no go-command step failing), and `TestSelfGateRunsTheGateTests` (passes; skips `TestRepositoryPassesEveryGate`; then fails naming `TestBrokenRule`) |
| Tools resolve from the tools module | `TestToolPinsResolveOffline` (`GOPROXY=off`: staticcheck prints `(0.8.2)`, govulncheck builds, `go list -m` gives v1.10.0), `TestToolsModulePinsLinters` |

`TestModulesGatePassesOnRepository` checks the `modules` gate on the checkout and
prints `modules: 3 product, 1 test and 2 tools modules checked`.
`TestReasonsCommittedFilesPass` checks every committed pin.

The full gate, run once after the stage (`GOWORK=off go -C tools/harness-gate run .
--root ../..`), shows the new list and stops at `vet`, as expected while stages
13.2 and 13.3 are open. `gofmt` passed; `src/go`, `src/sdk` and the plain
`src/harness` vet passed. The own analyzers in `src/harness` report 30 unique
findings over the two tag sets:

```text
harness-gate: gate 1/10 gofmt -- every Go file is gofmt-formatted
harness-gate: PASS gofmt (306ms)
harness-gate: gate 2/10 vet -- go vet and the project's own analyzers (nosleep, onespawn, cleanup, fuzzcheck)
FAIL src/harness: go vet -tags= -vettool=<harness-gate> ./...
FAIL src/harness: go vet -tags=replay,scenario -vettool=<harness-gate> ./...
harness-gate: FAIL vet (1m9.824s)
harness-gate: FAILED at gate vet
```

So the `modules` and `self` gates have not yet run inside the full gate. Their
behaviour is proved by the gate tests above. The full-gate `PASS modules` and
`PASS self` lines are still open until vet is green (stage 13.3's Windows gate run).

## Stage 1.2 -- Gate Runner Core (manual check, recorded after the fact)

This record was missing (gap M1-G-TOOL-18) and is added by gap-closure plan step 12.2.4.
It holds no new run: every result below is copied from the Stage 1.4 rows of this file,
and the commits are read from git history.

| Field | Value |
| --- | --- |
| Kind | `manual` |
| Commit that built the stage | `609c1d0f9aaa12080c078f8b66aaed7bc22a45e9` ("[impl] Gate Runner Core", PR #8, 2026-11-19): `runner.go`, `gates.go`, `coverage.go`, the own analyzers `nosleep`, `onespawn` and `cleanup`, and the seeded-violation modules under `tools/harness-gate/testdata/` (`race-seeded`, `unformatted-file`, `coverage-40pct` and others). Its godog suite followed in `c2b0c62` (PR #9). |
| Commit at which the seeded checks were seen | `e13abf3234ea9adeab5d72d329c873b52bb7c901` (the Stage 1.4 commit, 2026-11-19), local Windows, `go1.27.4 windows/amd64`, by the stage author (Copilot agent). No seeded run at `609c1d0` itself is recorded. |
| Gate list at `609c1d0` | 7 gates: `gofmt`, `vet`, `test`, `coverage`, `trace`, `budget`, `cross-compile`. `scenario-index` was added at `e13abf3`, making the 8 of the Stage 1.4 record. |
| Date written | 2026-11-22 |

The seeded checks of impl.md DH-S-1 "Manual checks" ("seed one violation per gate and
record that the runner names it"). Each `tools/harness-gate/testdata` module was copied
to a temp directory and run with a built `harness-gate --root <copy>`; the rows are those
of "Manual checks (seeded violations)" under Stage 1.4:

| Seeded violation | Gate named | What was seen | Exit |
| --- | --- | --- | --- |
| data race (`race-seeded`) | `test` | `harness-gate: FAILED at gate test` -- `failing tests: TestRace; data race detected` | 1 |
| unformatted file (`unformatted-file`) | `gofmt` | `harness-gate: FAILED at gate gofmt` -- `not gofmt-formatted:` `add.go` | 1 |
| package under the floor (`coverage-40pct`) | `coverage` | `harness-gate: FAILED at gate coverage` -- `example.com/cov40/p: 40% < 85% of statements (2 of 5 covered)` | 1 |

Automated counterparts in `609c1d0`: `TestRunStopsAtFirstFailingGate`,
`TestGateListIsFixed` (`runner_test.go`) and `TestSeededViolationFailsNamedGate`
(`gates_test.go`). They passed in the full `GOWORK=off go -C tools/harness-gate test
-count=1 ./...` run at `e13abf3` recorded under Stage 1.4; no run at `609c1d0` is
recorded.

## Stage 1.3 -- Test Layers and Golden Helper

| Field | Value |
| --- | --- |
| Kind | `automated` (unit, composition with the go toolchain) |
| Commit | `38969de44615ffbba94bab6dcda0d11e0816572e` (stage-1.3 PR head at the time of the run; clean tracked tree) |
| Toolchain | `go version go1.27.4 windows/amd64` |
| Date | 2026-11-20 |

### Ledger status of the DH-S-1 toolchain tests

impl.md DH-S-1 "Scenarios": the tests below need helpers that later stories add,
so at this stage they are **listed open, not failed**. No test for them is
written, skipped or marked failed here.

| Test | Status | Waits for |
| --- | --- | --- |
| `DH-T-TOOLCHAIN-1` | open | replay/scenario helpers of DH-S-16 |
| `DH-T-TOOLCHAIN-3` | open | replay/scenario helpers of DH-S-16 |
| `DH-T-TOOLCHAIN-5` | open | replay/scenario helpers of DH-S-16 |
| `DH-T-TOOLCHAIN-6` | open | replay/scenario helpers of DH-S-16 |
| `DH-T-TOOLCHAIN-10` | open | DH-S-46 |
| `DH-T-TOOLCHAIN-13` | open | DH-S-46 |

### Proven at this stage

| Test / step | Proof |
| --- | --- |
| `DH-T-TOOLCHAIN-4` (golden helper diffs, updates only on `-update`, refuses under CI) | `src/harness/internal/testkit/golden`: `TestAssertDetectsDriftAndUpdatesOnlyOnSwitch`, `TestAssertRefusesUpdateUnderCI`, `TestAssertMissingGoldenFailsWithoutCreatingIt` and the rest of `golden_test.go` (100% statement coverage) |
| step 1.6 part 2 (fuzz-target convention) | `tools/harness-gate/analyzers`: `TestFuzzCheckMissingTargetIsReported`, `TestFuzzCheckRules`, the `fuzzcheck/...` analysistest fixtures; `tools/harness-gate`: `TestFuzzCheckRunsInVetGate` (real `go vet -vettool`) |
| step 1.6 part 3 (`replay`/`scenario` excluded from the default gate) | `tools/harness-gate`: `TestLayerTaggedTestsAreExcludedFromDefaultGate` over `testdata/layer-tagged` (gate passes; `go test -tags scenario` / `-tags replay` fails) |

Both commands were run from the repository root of commit `38969de` and exited 0:

```text
$ go -C src/harness test -race -shuffle=on -count=1 -cover ./internal/testkit/golden
ok  	github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden	2.868s	coverage: 100.0% of statements
$ GOWORK=off go -C tools/harness-gate test -count=1 ./...
ok  	github.com/smartpcr/agent-plugin/tools/harness-gate	338.966s
ok  	github.com/smartpcr/agent-plugin/tools/harness-gate/analyzers	20.794s
ok  	github.com/smartpcr/agent-plugin/tools/harness-gate/modcheck	17.238s
```

## Stage 1.4 -- CI and Release Rehearsal (DH-S-1 closing record, impl.md sec. 7.3)

| Field | Value |
| --- | --- |
| Kind | `automated` (CI lanes, gate unit/composition tests) and `manual` (seeded violations) |
| Source and build identity | Commit `e13abf3234ea9adeab5d72d329c873b52bb7c901` (one commit for `src/go`, `src/sdk`, `src/harness`; no module tags cut -- this story publishes nothing, so the embedded version is `(devel)`) |
| Go version | CI: `go1.26.0` linux/amd64, darwin/arm64 (historical), windows/amd64 (`go-version-file: src/harness/go.mod`); local: `go1.27.4 windows/amd64` |
| Environment | GitHub-hosted `ubuntu-latest`, `macos-latest` (historical), `windows-latest`; `CGO_ENABLED=1`, C compiler verified per lane (`go env CC`; Windows: MSYS2 UCRT64 `gcc`) |
| Run identifiers | `ci` run [44670956416](https://github.com/smartpcr/agent-plugin/actions/runs/44670956416) (`workflow_dispatch` at `e13abf3`): job `harness (ubuntu-latest)` 136071526284, `harness (macos-latest)` 136071526164 (historical), `harness (windows-latest)` 136071526084 -- all success; artifacts `harness-gate-<os>` |
| `DSH_REQUIRE_SCENARIOS` | empty -- no scenario executes in DH-S-1 (tech-spec sec. 3.2); `harness-scenarios.yml` was not run with a stage |
| Date | 2026-11-21 |

> **Historical (decision `platforms-windows-wsl`, 2026-11-22).** The macOS entries in this
> record (the `macos-latest` lane, `darwin/arm64` Go, job 136071526164) were made before
> macOS left M1 scope. They are kept as history only. For M1 closure they are neither a
> pass nor a skip: a macOS row now reads "out of scope (`platforms-windows-wsl`)", and
> the gate of record runs on Windows (native) and Linux (WSL). The darwin binaries of the
> rehearsal below are cross-compile output, which stays in scope (QG-8).

### Gate output, three operating systems (historical lanes)

Every lane ends identically (timings stripped; full logs in the run's artifacts):

```text
harness-gate: PASS gofmt
harness-gate: PASS vet
harness-gate: PASS test
harness-gate: PASS coverage
harness-gate: PASS trace
scenario-index: 1 scenario tests authored, 2 scenarios indexed in 4 stages, built through DH-S-1
harness-gate: PASS scenario-index
harness-gate: PASS budget
harness-gate: PASS cross-compile
harness-gate: PASSED all 8 gates
```

| Lane | Result |
| --- | --- |
| `harness (ubuntu-latest)` | pass, all 8 gates |
| `harness (macos-latest)` | historical: pass, all 8 gates (macOS out of M1 scope, `platforms-windows-wsl`) |
| `harness (windows-latest)` | pass, all 8 gates |

### Release rehearsal (Linux lane, DH-D-TOOLCHAIN-5)

`GOWORK=off go -C tools/harness-gate run . rehearse --root ../..`, nothing published
(no tag, no upload; `release.yml` and `.goreleaser.yaml` unchanged):

```text
689a66e409ad0b6945973a225b2a945919d575c85ac99a9e9cd58251a6be66bb  dsh-linux-amd64  version=(devel) vcs.revision=e13abf3234ea9adeab5d72d329c873b52bb7c901 vcs.modified=false
5f70a8b01308917670cd6ffedaebc04daef9d4fdd5041a9cc65cecc34c37ad1e  dsh-linux-arm64  version=(devel) vcs.revision=e13abf3234ea9adeab5d72d329c873b52bb7c901 vcs.modified=false
b02b80652f9b458f52f8932ba61b40ff996cfe77929c1c5b7c889b1738549d3e  dsh-darwin-amd64  version=(devel) vcs.revision=e13abf3234ea9adeab5d72d329c873b52bb7c901 vcs.modified=false
eec8a5d45f3818c1f7453fdb3413fc220d25ca1370989c33b8e2db3ef52f175c  dsh-darwin-arm64  version=(devel) vcs.revision=e13abf3234ea9adeab5d72d329c873b52bb7c901 vcs.modified=false
4a7500e45b6178ee128e8aaa6dff0b1818db821bc0a2e7b8f16468238848ac46  dsh-windows-amd64.exe  version=(devel) vcs.revision=e13abf3234ea9adeab5d72d329c873b52bb7c901 vcs.modified=false
2d8b402720b6b6fe3f01cf9d76f0fdbc34b4d359777319a6686804d1832c2470  dsh-windows-arm64.exe  version=(devel) vcs.revision=e13abf3234ea9adeab5d72d329c873b52bb7c901 vcs.modified=false
rehearse: 6 binaries, nothing published
```

### Tests of this stage

`GOWORK=off go -C tools/harness-gate test -count=1 ./...` at `e13abf3` (local), exit 0:

```text
ok  	github.com/smartpcr/agent-plugin/tools/harness-gate	416.728s
ok  	github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance	1.591s
ok  	github.com/smartpcr/agent-plugin/tools/harness-gate/analyzers	31.506s
ok  	github.com/smartpcr/agent-plugin/tools/harness-gate/modcheck	24.437s
```

| Scenario of the stage | Proof |
| --- | --- |
| Preflight fails on a missing secret | `acceptance.TestPreflight` (environment as a map; empty plan on failure), `TestPreflightSubcommand/missing_secret` (no `$GITHUB_OUTPUT` written) |
| Preflight fails on an unknown required scenario; empty list passes | `acceptance.TestPreflight` (`DH-E-FAKE-9`), `TestPreflightSubcommand` |
| Rehearsal produces six pure-Go binaries, publishes nothing | `TestRehearseBuildsSixPureGoBinaries` (`GOPROXY=off`, parent `CGO_ENABLED=1`/`GOFLAGS=-race` overridden, build info `CGO_ENABLED=0 -trimpath=true`, tags unchanged), `TestRehearseNamesFailingTarget` |
| Gate passes on all three CI operating systems (historical; restated by `platforms-windows-wsl` as Windows native plus Linux under WSL, still open) | run 44670956416 above |
| Authored scenarios are indexed, compile, do not run | `TestAuthoredScenariosAreIndexedAndCompileButDoNotRun` (`scenario-index` passes on the repo; `go -C src/harness vet -tags scenario ./...` exit 0; `go test -tags= -list` lists no test, `-tags scenario` lists `TestToolchainRehearsal`), `TestScenarioIndexGateNamesUnindexedScenario`, `acceptance.TestCheck` |

### Manual checks (seeded violations)

`manual`, by the stage author (Copilot agent), at `e13abf3`, local Windows: each
`tools/harness-gate/testdata` module copied to a temp directory and run with a built
`harness-gate --root <copy>`:

| Seeded violation | What was seen | Exit |
| --- | --- | --- |
| data race (`race-seeded`) | `harness-gate: FAILED at gate test` -- `failing tests: TestRace; data race detected` | 1 |
| unformatted file (`unformatted-file`) | `harness-gate: FAILED at gate gofmt` -- `not gofmt-formatted:` `add.go` | 1 |
| package under the floor (`coverage-40pct`) | `harness-gate: FAILED at gate coverage` -- `example.com/cov40/p: 40% < 85% of statements (2 of 5 covered)` | 1 |

### Scenarios

- `DH-E-TOOLCHAIN-2` -- authored (`src/harness/internal/testkit/scenarios/toolchain_rehearsal_scenario_test.go`, `//go:build scenario`, `//dsh:scenario DH-E-TOOLCHAIN-2` on `TestToolchainRehearsal`), reviewed in this stage's PR, indexed to `DH-S-18` in `test/harness/acceptance-index.yaml`. **Not run** (executes at DH-S-18).
- `DH-E-TOOLCHAIN-1` -- indexed to `DH-S-17` with authoring stage `DH-S-11`; no file yet (its subjects, the model runtime and replay helpers, arrive in Phase 11). **Not run.**

### Cleanup evidence

The rehearsal and gate build into temp directories they remove; the scenario and
`TestRehearseBuildsSixPureGoBinaries` compare `git for-each-ref refs/tags` before
and after (unchanged). No tag, release or upload was made.

### Open items

- `DH-E-TOOLCHAIN-2`: open until DH-S-18 runs it (its keyless replay subtest needs
  the DH-S-16 recording `testkit/scenarios/testdata/rehearsal-replay`).
- `DH-E-TOOLCHAIN-1`: open; authored at DH-S-11, run at DH-S-17.
- `DH-T-TOOLCHAIN-1`, `-3`, `-5`, `-6` (DH-S-16) and `-10`, `-13` (DH-S-46): open, as recorded under Stage 1.3.
- `harness-scenarios.yml` has not been run: the `DSH_DEEPSEEK_API_KEY` secret is consumed first at the stage that accepts a scenario. (Historical: under decision `models-signed-in-claude-copilot` the secret is removed by gap-closure-plan step 12.3.8.)

## Local lanes

This section is the exit evidence of plan Stage 12.3 (gaps M1-G-GATE-20, -21,
M1-G-TOOL-12 to -15). Under decision `platforms-windows-wsl` the gate of record runs on
two local lanes of the Windows development host: **Windows native** and **Linux under
WSL**. macOS is out of scope (`platforms-windows-wsl`). The hosted CI lanes stay as
regression signals only.

**Decisions.** All in [decisions.md](../decisions.md):
- `platforms-windows-wsl` (OWNER-1, in force): the two lanes; macOS out of M1 scope.
- M1-D-GATE-21 and M1-D-TOOL-5 (one question, both `open`): what hosted CI keeps. The
  plan's default, which plan step 12.3.7 applies: hosted `ubuntu-latest` and
  `windows-latest` `harness` lanes as regression signals, macOS removed.
- `lane-toolchain-floor` (in force, lead settlement): go1.26.0 on both lanes.
- `local-run-record-format` (in force, lead settlement): the run-record id and path.

**Where the procedure is.** Provisioning, the clone rule and the preflight checks:
[tools/harness-gate/README.md sec. Lanes](../../../../tools/harness-gate/README.md#lanes).
The lane scripts: [`lanes/wsl.sh`](../../../../tools/harness-gate/lanes/wsl.sh) and
[`lanes/win.ps1`](../../../../tools/harness-gate/lanes/win.ps1). How to run them and
record a run: [the evidence runbook](README.md#lanes).

**Changed workflows (plan 12.3.7 and 12.3.8).**
- [`.github/workflows/ci.yml`](../../../../.github/workflows/ci.yml): the `harness` matrix
  is `[ubuntu-latest, windows-latest]`, and the header comment says the hosted lanes are
  regression signals and the evidence of record is the local lanes.
- [`.github/workflows/harness-scenarios.yml`](../../../../.github/workflows/harness-scenarios.yml):
  the `scenarios` matrix is `[ubuntu-latest, windows-latest]`, and the DeepSeek key is no
  longer read (`models-signed-in-claude-copilot`).
- [`.github/workflows/harness-manual-checks.yml`](../../../../.github/workflows/harness-manual-checks.yml):
  macOS is dropped from its matrix.

### Provisioning facts

`manual`, measured on 2026-11-22 at the provisioning of plan steps 12.3.1 and 12.3.2.
Operator: to be named by the lead. These are facts of the host, not a gate run.

| Fact | WSL lane (12.3.1) | Windows native lane (12.3.2) |
| --- | --- | --- |
| Host | Windows 10.0.28000.3501; WSL2, default version 2 | Windows 10.0.28000.3501 |
| Install | `wsl --install -d Ubuntu-24.04 --no-launch` | -- |
| Distribution | Ubuntu 24.04.5 LTS | -- |
| Kernel | 6.18.58.1-microsoft-standard-WSL2 | -- |
| User | default user `dsh`, uid 1000 (not root) | not recorded at provisioning; `win.ps1` refuses an elevated shell at each run |
| `/etc/wsl.conf` | `[user] default=dsh`, `[interop] appendWindowsPath=false`, `[boot] systemd=true` | -- |
| systemd | on: systemd is PID 1 and `systemctl --user` is available, so the Linux user-scope path is usable (EN-5) | -- |
| Go | go1.26.0 linux/amd64 in `/usr/local/go`, from the go.dev tarball (SHA-256 `aac1b08a0fb0c4e0a7c1555beb7b59180b05dfc5a3d62e40e9de90cd42f88235`, matched against the go.dev JSON); `PATH` set in `~/.profile` (login shells) | host default go1.27.4; `GOTOOLCHAIN=go1.26.0` selects go1.26.0 windows/amd64 (downloaded by Go on first use) |
| C compiler | gcc (Ubuntu 13.3.0-6ubuntu2~24.04.1) 13.3.0, from `build-essential` | `CC=gcc`, resolving to `C:\msys64\ucrt64\bin\gcc.exe`: gcc (Rev5, Built by MSYS2 project) 16.1.0 |
| git | 2.43.0 | not recorded |
| Caches and temp | `GOCACHE=/home/dsh/.cache/go-build`, `GOMODCACHE=/home/dsh/go/pkg/mod`, `TMPDIR` unset (so `/tmp`), all on ext4 | not recorded |
| File systems | the clone and the caches on ext4 (`/dev/sdd`; `stat -f -c %T` prints `ext2/ext3`); `/mnt/e` reports `v9fs`, which the lane preflight refuses | not recorded at provisioning; `win.ps1` requires a local fixed NTFS or ReFS volume at each run |
| Checkout | `git clone /mnt/e/work/github/ai/agent-plugin ~/src/agent-plugin`, `core.autocrlf=false`, on ext4, clean at `cf64524` | the host checkout |
| macOS | out of scope (`platforms-windows-wsl`) | |

### First lane runs (plan 12.3.9)

`manual`, **not closure evidence.** Both scripts run once at the Stage 12.3 commit to
prove the lanes work end to end; each is expected to stop at the `vet` gate, which Phase
13 repairs. The record is the first triage input for plan step 13.7.

| Field | WSL lane (`lanes/wsl.sh`) | Windows lane (`lanes/win.ps1`) |
| --- | --- | --- |
| Run-record id | `wsl-a30ddd9-20261122T1535Z` | `win-4a96224-20261122T1534Z` |
| Record file and SHA-256 | [runs/wsl-a30ddd9-20261122T1535Z.txt](runs/wsl-a30ddd9-20261122T1535Z.txt), file sha256 `8d7cbdbc30c6197eb6f0a5abb0d983f86a9dc1b19139e1ec87ded9463bc444a1` (the `sha256:` line inside covers the output above it) | [runs/win-4a96224-20261122T1534Z.txt](runs/win-4a96224-20261122T1534Z.txt), file sha256 `9828d6d0af39a4c2e9b9c719d309004ad37ff3583d071e3408ba9616b199dc49` |
| Rehearsal file and SHA-256 | [runs/wsl-a30ddd9-20261122T1535Z-rehearse.txt](runs/wsl-a30ddd9-20261122T1535Z-rehearse.txt), file sha256 `8eed8c858326bb029428906893a96d7095246082c4c22220337b1a24d4e46770`; exit 0, six binaries, each `vcs.modified=false` | -- (WSL lane only) |
| Operator | Claude Code (lead agent), for the owner | Claude Code (lead agent), for the owner |
| Commit | `a30ddd9` (`4a96224` plus the executable bit on `lanes/wsl.sh`) | `4a96224` (identical tree except that file mode) |
| Preflight | passed: ext4 checkout, uid 1000, go1.26.0, `CC=gcc`, clean tree, `TMPDIR=/tmp` on ext4 | passed: NTFS, not elevated, go1.26.0 (`GOTOOLCHAIN=go1.26.0`), MSYS2 gcc, clean tree |
| Gate result | `harness-gate: FAILED at gate vet`, exit 1 (analyzer findings in `src/harness` test files; Phase 13) | `harness-gate: FAILED at gate vet`, exit 1 (same findings) |
| Label | `manual`, not closure evidence | `manual`, not closure evidence |

Lane fault found by the first run: `lanes/wsl.sh` had been committed without its
executable bit (exit 126 at `4a96224`); fixed in `a30ddd9`.

### Runbook dry run (plan 12.4.7)

`manual`, **not closure evidence.** On the fresh distribution at `a30ddd9`, every
verification line of [README.md sec. Lanes](README.md) printed its stated value:
`go env GOOS GOARCH` = `linux amd64`; `df -T .` = `ext4`; `git status --porcelain` empty
apart from the new run records; `go env CC` = `gcc`, gcc 13.3.0; `systemctl --user`
present (the Linux user-scope path is usable, EN-5); `id -u` = 1000; `TMPDIR` = `/tmp`
on ext4; `CGO_ENABLED=1`; Ubuntu 24.04.5 LTS, kernel 6.18.58.1-microsoft-standard-WSL2;
`wsl.exe --version`: WSL 3.0.5.0, kernel 6.18.58.1-1. A gate log was produced (the WSL
row above).

## M1 gate repair

Plan Stage 13.1 (Wait Idiom, Analyzer Fix, cordis and Fuzz Seeds; M1-G-GATE-2 to -5,
M1-G-GATE-24). `manual`, **not closure evidence**: the `vet` gate as a whole still
fails on the `src/harness` findings that Stages 13.2 and 13.3 repair.

**The decided idiom (M1-D-GATE-24, option A).** `nosleep` stays strict (no
`time.Sleep` in any `_test.go` file, `analyzers/nosleep.go` unchanged); waits use
`testing/synctest` virtual time or an explicit signal; no `<-time.After` settle
outside a bubble; a cleanup must unblock, then join; one named poll helper,
`src/harness/internal/testkit/procwait` (`Until(ctx, t, fact)`, `Poll(ctx, fact)`),
for a real child process or another process's log. Written down in
[tools/harness-gate/README.md, "Waiting and releasing in tests"](../../../../tools/harness-gate/README.md#waiting-and-releasing-in-tests-m1-d-gate-24),
with the fix patterns P-C, P-V, P-S and P-F.

**The analyzer fix (M1-G-GATE-5).** `cleanup.go` `assignedIdents` records a
selector left-hand side (`cs.Server = httptest.NewServer(...)`) under its root
identifier, so `t.Cleanup(cs.Close)` releases it. Fixtures, tagged DH-T-TOOLCHAIN-8:

| fixture or test | proves |
| --- | --- |
| `tools/harness-gate/analyzers/testdata/src/cleanup/field_test.go` (run by `TestAnalyzersOnFixtures`, `analysistest`) | `TestFieldServerReleased` and `TestNestedFieldServerReleased` have no diagnostic; `TestFieldServerLeaks` and `TestFieldServerOtherCleanupDoesNotCount` each have exactly one `listener started ... (QG-5)` (`// want`) |
| `TestCleanupFieldAssignedListener` (`analyzers_test.go`) | the stage scenario in memory: released, no finding; cleanup removed, exactly one `listener started ... (QG-5)` |
| `src/harness/internal/testkit/procwait/procwait_test.go` | the poll helper, on virtual time: immediate success, one check per `Interval`, the context deadline, the `DefaultTimeout` bound, the cancel cause, and `Until` failing its test |

The fixture is named `field_test.go`, not the plan's `field.go`: the analyzer judges
`_test.go` files only, so a `field.go` fixture could not carry a diagnostic.
Without the `cleanup.go` change the fixture fails with two unexpected diagnostics
(lines 19 and 25), checked once by reverting the file.

**`src/go` vet output.** Run on the working tree of `c080f53` plus the Stage 13.1
edits (the lead records the stage commit), Windows host, go1.27.4 with
`GOTOOLCHAIN=go1.26.0`, vet tool built from `tools/harness-gate` (`HARNESS_GATE_VETTOOL=1`
must be set for `go vet -vettool`):

```
go -C src/go vet -vettool=$TEMP/hg/harness-gate.exe ./...
go -C src/go vet -vettool=$TEMP/hg/harness-gate.exe -tags=replay,scenario ./...
```

Before: exit 1, the same 23 findings with both tag sets -- `cordis/tree_internal_test.go:25,68`,
`cordis/config_test.go:137,151,164,455,456`, `cordis/unload_test.go:96,195,200,207,392,395,396,449,454,492,659,663,734,814,824`
(QG-5) and `cordis/unload_test.go:500` (QG-4). After: exit 0, no output, with both tag sets.

`src/harness` (default tags): 53 findings before, 48 after; the five cleared are the
four QG-6 findings of `util/{caltime,glob,recur,version}` and the field false positive
at `util/egress/egress_test.go:32`. Total cleared by the stage: 28 of 81.

**Tests run** (`manual` record of this stage's scenarios):
`GOWORK=off go -C tools/harness-gate test -count=1 ./analyzers/...` ok;
`CGO_ENABLED=1 go -C src/go test -race -shuffle=on -count=3 ./cordis/` ok;
`go -C src/harness test -run Fuzz ./internal/util/...` ok (every seed runs as
`FuzzXxx/seed#n`, from `seeds_test.go`); `go -C src/harness test ./internal/testkit/procwait/` ok.
The cordis cleanups were also checked on the failure path: with a `t.FailNow()`
injected right after the goroutines start in five `unload_test.go` tests (four in a
synctest bubble), each test failed with no bubble deadlock and no join timeout, so
every cleanup released and joined its goroutines (injection reverted).

### Stage 13.2 (session, storage, util, boot, llm and human tests)

Plan 13.2.1-13.2.5 (M1-G-GATE-6 to -10). `manual`, **not closure evidence**. The 26
vet findings of `src/harness/internal/{session,storage,util,boot,llm,human}` are
cleared:
- **P-C** in `session/{scope,event,views}`, `session/session_test.go`,
  `util/fsx/replace_test.go`, `util/watch/watchtest/manual_test.go`,
  `boot/{audit,boot}_test.go`, `storage/workspace/archive_test.go`,
  `llm/runtime_test.go` and `human/human_test.go`.
- **P-V** in `session/query/visibility_test.go` (the SQLite store in a `synctest`
  bubble) and `storage/blob/gclock_test.go`.
- **P-S** through the `appended` channel (query), `sqlite.WriteHeldSignal` and
  `sqlite.BusyWaitSignal` (shutdown), `blob.SetAfterLockShared` (gclock) and
  `clock.Manual.BlockUntil` before each `Advance` (boot).

Two unexported test seams in non-test code are nil outside tests: `ctxMutex.held` in
`session/store/sqlite/open.go` and `afterLockShared` in `storage/blob/blob.go`.

Checks:
- vet with the gate tool, both tag sets: exit 0, no diagnostic.
- `-race -shuffle=on -count=3`: 55 packages ok.
- The "close during busy wait" scenario, `-race -count=20`: ok.
- **Open, not a 13.2 finding:** `session/store/sqlite`
  `TestDutyHasOneHolderAndMovesAfterAKill` (DH-T-STORE-14) fails intermittently with
  `store/busy` (SQLITE_BUSY) when two child processes open one fresh database. The
  unchanged code fails it as well: 3 of 20 runs side by side, against 1 of 20 with the
  13.2 edits. It is recorded for plan step 13.7.1 as the first race and order triage
  item.

Windows host, go1.26.0, MSYS2 gcc.

### Stage 13.3 (agent, delegate, tools and scenario-layer tests)

Plan 13.3.1-13.3.5 (M1-G-GATE-11 to -14, -27). `manual`, **not closure evidence**.
The scripted model `internal/testkit/model` gained:
- `(*Transport).WaitRequests(ctx, n)`, which is woken by each request, does not
  poll, and returns `context.Cause(ctx)`;
- `(*Transport).PrefixServed(n)`.

Unit tests: `TestWaitRequestsBlocksUntilTheNthRequest`,
`TestWaitRequestsReturnsTheContextError` (the "WaitRequests blocks without polling"
scenario, in `synctest`) and `TestPrefixServedSignalsTheEndOfTheScriptedText`.

The 27 agent-family findings are cleared in `agent/agent_test.go`,
`agent/driver/{driver,lifecycle,retry}_test.go`, `delegate/subagent/subagent_test.go`,
`tools/core/schedule_test.go` and, under `-tags scenario`,
`testkit/scenarios/store_{two_process,crash_recovery}_scenario_test.go`. The patterns
used:
- **P-C** through `goJoined` helpers.
- **P-V** through `synctest`. A follow-up (`a199951`) also moved the three 100 ms
  "did not happen" waits (owner unload, `WaitQuiet`, manager unload) into bubbles.
- **P-S** through `WaitRequests`, `PrefixServed`, `life.reached` and
  `subagent.Refusing`.
- `procwait.Poll` for the two cross-process polls.

Vet with the gate tool (`GOTOOLCHAIN=go1.26.0`), both tag sets: exit 0.
`-race -shuffle=on -count=3` over the packages: all ok. The scenario files are vetted,
not run (EV-4).

### trace-tests report (13.6, not yet a gate)

Plan 13.6.2 and 13.6.3 (M1-G-CLOSE-9). This is a **report, not yet a gate**: `trace-tests` joins
the gate list at step 18.1.4. It is not closure evidence.

- Command: `GOWORK=off go -C tools/harness-gate run . trace-tests --root ../.. --stage DH-S-11`.
- Tree: `d25a556` plus the uncommitted 13.6 part B changes. Those changes add
  `//dsh:test` directives to the DH-S-2 and DH-S-5 tests, and nothing else in test
  files. The run exits 1 because IDs are missing.
- No ID is deferred: no `doc.go` deferral exists yet. DH-S-1's `DH-T-TOOLCHAIN-1`,
  `-3` and `-5` are therefore counted as missing, not deferred. impl.md's DH-S-16
  section names them, so each needs a `doc.go` deferral line.

| Stage | Required | Tagged | Deferred | Missing |
| --- | --- | --- | --- | --- |
| DH-S-1 | 12 | 5 | 0 | 7 |
| DH-S-2 | 12 | 12 | 0 | 0 |
| DH-S-3 | 11 | 5 | 0 | 6 |
| DH-S-4 | 70 | 17 | 0 | 53 |
| DH-S-5 | 37 | 34 | 0 | 3 |
| DH-S-6 | 81 | 6 | 0 | 75 |
| DH-S-7 | 13 | 9 | 0 | 4 |
| DH-S-8 | 19 | 4 | 0 | 15 |
| DH-S-9 | 6 | 6 | 0 | 0 |
| DH-S-10 | 25 | 19 | 0 | 6 |
| DH-S-11 | 24 | 17 | 0 | 7 |
| **Total** | **310** | **134** | **0** | **176** |

The tags were found in three forms: 46 `//dsh:test` directives, 81 legacy doc
comments and 7 legacy body comments.

DH-S-5's three missing IDs (`DH-T-AGENT-14`, `DH-T-VIEW-1`, `DH-T-VIEW-4`) are
sub-gaps M1-G-CLOSE-9.1 to 9.3. Two tagged IDs have an unasserted clause:
`DH-T-VIEW-3` (9.4) and `DH-T-HUMAN-2` (9.5).

## Evidence record (impl.md sec. 7.3)

These are the eight fields of [impl.md sec. 7.3](../../../harness/impl.md#73-the-evidence-record),
in the format of [the evidence runbook](README.md#the-evidence-record-headings). Plan step
18.1.7 fills them at the frozen M1 closure commit. Until then each field points to the
history above, which stays exactly as it was recorded and is not closure evidence
(M1-D-CLOSE-1).

### Source and build identity

At closure: not yet recorded (plan 18.1.7). Historical: commits `a647d49` (Stage 1.1),
`609c1d0` and `e13abf3` (Stage 1.2), `38969de` (Stage 1.3) and `e13abf3` (Stage 1.4, the
DH-S-1 closing record), each with its toolchain, in the stage sections above. The
rehearsal's binary checksums and embedded versions are under "Release rehearsal (Linux
lane, DH-D-TOOLCHAIN-5)".

### Environment and fixture

At closure: both local lanes, as provisioned in "Local lanes" above; the run-record
header gives the rest. macOS: out of scope (`platforms-windows-wsl`). Historical: the
hosted CI lanes and the local Windows runs named in the stage sections above; the macOS
lane there is historical.

### Exact tests and commands

At closure: `lanes/wsl.sh` and `lanes/win.ps1` (each runs `GOWORK=off go -C
tools/harness-gate run . --root ../.. --evidence <file>`; the WSL script also runs
`rehearse`), and `GOWORK=off go -C tools/harness-gate test -count=1 ./...`.
`DSH_REQUIRE_SCENARIOS`: empty (no scenario executes in DH-S-1). Historical: the commands
in the stage sections above.

### Run identifiers

At closure: the run-record ids of both lanes, in the runbook's template (plan 18.1.3).
WSL lane: not run. Windows lane: not run. macOS: out of scope (`platforms-windows-wsl`).
Not closure evidence: the first lane runs of 12.3.9 ("Local lanes" above). Historical:
CI run 44670956416 (Stage 1.4), whose macOS job is historical.

### Results

At closure: not yet recorded. Historical: "Gate output, three operating systems
(historical lanes)" and "Tests of this stage" under Stage 1.4.

### Manual checks

impl.md DH-S-1 names one check: seed one violation per gate and record that the runner
names it. Historical records: "Stage 1.2 -- Gate Runner Core" and "Manual checks (seeded
violations)" above. How to run and fill the register: [the evidence runbook](README.md#manual-checks).
Commands run from the repository root; a "seeded gate" row copies the named module of
`tools/harness-gate/testdata/` to a temporary directory and runs `GOWORK=off go -C
tools/harness-gate run . --root <copy>`.

#### Manual checks register

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1.1 | rule-3 default: three modules build together, the release build ignores `go.work` | by hand: from a clean clone, `go build ./src/go/... ./src/sdk/... ./src/harness/...`, `go list -m` (three modules), `GOWORK=off go -C src/harness build ./cmd/dsh` and run `./src/harness/dsh` (prints `dsh (devel)`) | one lane | | | | `manual` |
| 1.2 | seeded violation per gate: the runner names the gate | seeded gate: run each module of `testdata/` and record `harness-gate: FAILED at gate <gate>`, exit 1, and no later gate started; with `testdata/fake-go` first on `PATH`, setup fails with exit 2 naming both versions | one lane | | | | `manual` |
| 1.3 | seeded violation: a data race | seeded gate: `race-seeded` and `budget-race` each fail at `test` (`data race detected`) | one lane | | | | `manual` |
| 1.4 | seeded violation: an own-analyzer finding | seeded gate: add a `time.Sleep` call to a `_test.go` file of a copied module and record `FAILED at gate vet` naming the `nosleep` finding | one lane | | | | `manual` |
| 1.5 | seeded violation: a package under the floor | seeded gate: `coverage-40pct` fails at `coverage` (`40% < 85%`); `coverage-blocked` fails naming `TestBlocked`; `coverage-child` passes | one lane | | | | `manual` |
| 1.6 | seeded violation: a golden drift; layers excluded | composed test: `go -C src/harness test -count=1 -v ./internal/testkit/golden` (drift reported; `-update` refused with `CI=1`); seeded gate: `layer-tagged` passes `test` and `coverage` | one lane | | | | `manual` |
| 1.7 | seeded violation: a budget over its limit | seeded gate: `budget-exceeded` with `DSH_BUDGET_RELAX=1` set still fails at `budget` | one lane | | | | `manual` |
| 1.8 | rule-3 default: keyless CI lanes and a credentialed preflight | by hand: read the `harness` matrix of `ci.yml` and the `scenarios` matrix of `harness-scenarios.yml` (equal to the decided list, M1-D-GATE-21 / M1-D-TOOL-5); run `GOWORK=off go -C tools/harness-gate run . preflight --root ../..` with no model variable (passes) and with `DSH_REQUIRE_SCENARIOS=DH-E-FAKE-9` (fails naming the ID) | one lane | | | | `manual` |
| 1.9 | seeded violation: a target that does not cross-compile; the rehearsal | seeded gate: `cross-broken` fails at `cross-compile` naming `windows/arm64`; by hand: the `-rehearse.txt` of a WSL lane run lists six binaries with `vcs.modified=false` and publishes nothing | WSL (rehearsal) | | | | `manual` |

### Cleanup evidence

At closure: not yet recorded; the comparison is `git for-each-ref refs/tags` before and
after the rehearsal (unchanged) and `git status --porcelain` empty after the run.
Historical: "Cleanup evidence" under Stage 1.4.

### Open items

At closure: plan 18.1.11 lists them. Now open: the `DH-E-TOOLCHAIN-1` and `-2` scenarios
and the `DH-T-TOOLCHAIN` tests listed under "Open items" of Stage 1.4; the toolchain
scope rows whose decisions are `open` ("Toolchain scope decisions" above); macOS, out of
scope (`platforms-windows-wsl`), with no return stage (`macos-return-stage`, open).
