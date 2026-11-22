# DH-S-1 evidence

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
| Go version | CI: `go1.26.0` linux/amd64, darwin/arm64, windows/amd64 (`go-version-file: src/harness/go.mod`); local: `go1.27.4 windows/amd64` |
| Environment | GitHub-hosted `ubuntu-latest`, `macos-latest`, `windows-latest`; `CGO_ENABLED=1`, C compiler verified per lane (`go env CC`; Windows: MSYS2 UCRT64 `gcc`) |
| Run identifiers | `ci` run [44670956416](https://github.com/smartpcr/agent-plugin/actions/runs/44670956416) (`workflow_dispatch` at `e13abf3`): job `harness (ubuntu-latest)` 136071526284, `harness (macos-latest)` 136071526164, `harness (windows-latest)` 136071526084 -- all success; artifacts `harness-gate-<os>` |
| `DSH_REQUIRE_SCENARIOS` | empty -- no scenario executes in DH-S-1 (tech-spec sec. 3.2); `harness-scenarios.yml` was not run with a stage |
| Date | 2026-11-21 |

### Gate output, three operating systems

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
| `harness (macos-latest)` | pass, all 8 gates |
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
| Gate passes on all three CI operating systems | run 44670956416 above |
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
- `harness-scenarios.yml` has not been run: the `DSH_DEEPSEEK_API_KEY` secret is consumed first at the stage that accepts a scenario.