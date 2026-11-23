---
title: "dsh"
storyId: "harness:DSH"
---

> Decisions that change steps of this plan (owner, operator and lead) are logged in [decisions.md](decisions.md); a step that follows one cites it by name. The work that closes M1's remaining gaps continues as Phases 12 to 19 in [gap-closure-plan.md](gap-closure-plan.md).

# Phase 1: Repository Modules and Quality Gates

**Toolchain scope decisions (added 2026-11-22 by gap-closure plan step 12.2.1).** Phase 1 built less than [toolchain.md](../../harness/upstream/toolchain.md) asks for in some places, and more in one. Each difference is a row of [decisions.md](decisions.md); an `open` row follows the plan's default until the owner answers. The full table, with what was built and what the spec asks for, is in [evidence/DH-S-1.md](evidence/DH-S-1.md#toolchain-scope-decisions).

| Item | Built in Phase 1 | Spec | Decision (status) | Return stage |
| --- | --- | --- | --- | --- |
| Gate list | 8 gates | DH-ADR-TOOLCHAIN-8, -9: also staticcheck, module tidy and verify, vulnerability check, replay | M1-D-GATE-23, M1-D-TOOL-1 (open) | gap-closure plan 13.4, 13.5 |
| Module hygiene | `modcheck` tests; no tidy or verify gate | DH-ADR-TOOLCHAIN-11 | M1-D-GATE-23, M1-D-TOOL-2 (open) | 13.4 |
| Site exceptions | none | DH-ADR-TOOLCHAIN-8: reasoned exception at the site | M1-D-GATE-23 (open) | 13.5 |
| Release rehearsal | built in part: `go build` of six binaries, no GoReleaser | DH-D-TOOLCHAIN-5 | M1-D-TOOL-3 (open) | 19.1.1; DH-S-18 |
| Coverage floor | stricter: every package, `main` included | DH-ADR-TOOLCHAIN-7 | `coverage-floor-all-packages-kept` (in force, lead settlement; DC-3 record, rule kept) | none |
| Import rules, HTTP-client rule, goldens | archtest Go tests; HTTP rule not in DH-S-1; goldens inside the `test` gate | DH-ADR-TOOLCHAIN-8, DH-D-TOOLCHAIN-2 | `import-rules-by-archtest`, `http-client-rule-owner`, `goldens-in-test-gate` (in force, lead settlements) | HTTP rule: impl.md 23.16 |
| Replay gate and `harness-replay` job | not built, not scheduled | DH-D-TOOLCHAIN-4, `DH-T-TOOLCHAIN-5` | `replay-gate-step-dh-s-16` (open) | DH-S-16; 19.1.2 |
| Platform lanes | three hosted lanes (historical) | Linux, macOS, Windows | `platforms-windows-wsl` (in force) | 12.3; macOS: `macos-return-stage` (open) |

## Dependencies
- _none -- start phase_

## Stage 1.1: Three Module Workspace

### Implementation Steps
- [ ] impl.md step 1.1 / DH-D-TOOLCHAIN-1 (part 1): raise the `go` line of `src/go/go.mod` from `1.24` to `1.26.0` (constraint RM-2, fact F-1); keep `src/go` standard-library only and confirm `src/go/cordis`, `src/go/loader`, `src/go/timer` and `src/go/cmd/cordis-sample` still build and their existing tests pass unchanged.
- [ ] impl.md step 1.1 / DH-D-TOOLCHAIN-1 (part 2): create module `src/sdk` (`module github.com/smartpcr/agent-plugin/src/sdk`, `go 1.26.0`, no `require`) with empty `jsonrpc/doc.go` and `protocol/doc.go` packages, so DH-S-4 has a home (constraint RM-1, RM-3).
- [ ] impl.md step 1.1 / DH-D-TOOLCHAIN-1 (part 3): create module `src/harness` (`go 1.26.0`) requiring `src/go` and `src/sdk` at pseudo-versions WITHOUT any `replace` directive (RM-4), with `cmd/dsh/main.go` holding `main` only (prints the embedded version and exits 0) and an empty `internal/` tree (RM-5).
- [ ] impl.md step 1.1 / DH-D-TOOLCHAIN-1 (part 4): add the root `go.work` (`use ./src/go ./src/sdk ./src/harness`) for development only; document in `src/harness/README.md` that release and rehearsal builds run with `GOWORK=off` (DH-ADR-26, DH-ADR-TOOLCHAIN-11); write `DH-T-TOOLCHAIN-11` (three modules, one `go` floor, no `replace`) and `DH-T-SDK-17` (`src/sdk` requires nothing) as tests under `tools/harness-gate/modcheck`.
- [ ] Commit the TC-02b golden BEFORE any DH-S-2 edit. The sample today sleeps for real (50/500/300/300/500 ms), logs a timer-driven `beat:` line, stamps console log lines with wall-clock `15:04:05`, and prints `filepath.Join(os.TempDir(), "cordis-sample.json")`; none of that is stable across runs or OSes. Refactor `src/go/cmd/cordis-sample/main.go` to `run(env sampleEnv) error`, where `sampleEnv` carries `Out io.Writer` (every `fmt.Println` becomes `fmt.Fprintln(env.Out, ...)`), `TempDir string`, and `Exporter cordis.Exporter`; `main` calls it with `os.Stdout`, `os.TempDir()` and the default console exporter, so the program's behaviour and sleep/interval values are unchanged. Add `main_test.go` that (a) runs `run` inside a `testing/synctest` bubble with explicit synchronization, because virtual time alone orders distinct instants but not goroutines runnable at the same instant: `sampleEnv` also carries `Pause func(time.Duration)` (replacing each of the four `time.Sleep` calls in `run`; `main` passes `time.Sleep`) and `Settle func()` (called by `run` after every lifecycle call -- `Use`, `Wait`, `Update`, `Dispose` -- and before every `Fprintln`; `main` passes a no-op). The test's `Settle` is `synctest.Wait()`, so every unload/reload log line of a lifecycle call is written before `run` prints its next line; the test's `Pause(d)` is `synctest.Wait(); <-time.After(d - time.Millisecond); synctest.Wait()` (no `time.Sleep` in a `_test.go`, per the `nosleep` analyzer of Stage 1.2), so main resumes 1 ms before each window boundary and every heartbeat tick strictly inside the window has run its callback and logged before main acts, while no tick (200/100/150 ms periods, and the loader's 100 ms poll, all started at whole-millisecond offsets) can share an instant with main. The test exporter records each message's `Time` (the bubble clock) and the test fails with "coincident events" if a `beat:` line and a main-goroutine line ever carry the same instant, turning any future same-instant race into a deterministic failure rather than a flaky golden; (b) `run` replaces the root's exporters with `env.Exporter` (`ClearExporters` then `AddExporter`; `main` passes `cordis.NewConsoleExporter(os.Stdout, os.Stderr)`, identical to today's default), and the test passes an exporter that writes `[<level>] <name> <text>` with no timestamp to the same buffer; (c) passes `t.TempDir()` and rewrites that prefix to `<TMP>` and `\` to `/` before comparing with `src/go/cmd/cordis-sample/testdata/output.golden`, re-recorded only with `-update` (contract rule 3). `src/go/go.mod` moves its `go` line to the single floor of step 1.1 (>= 1.25, where `testing/synctest` is stable).
- [ ] Record the stage-1.1 manual check (`manual`, commit SHA) in `docs/stories/harness-DSH-M1/evidence/DH-S-1.md`: from the repository root (no root module; `go.work` selects the three modules) `go build ./src/go/... ./src/sdk/... ./src/harness/...` succeeds, and `GOWORK=off go -C src/harness build ./cmd/dsh` succeeds.

### Dependencies
- _none -- start stage_

### Test Scenarios
- [ ] Scenario: Workspace builds all three modules -- Given a clean checkout with the root `go.work`, When `go build ./src/go/... ./src/sdk/... ./src/harness/...` and `go vet ./src/go/... ./src/sdk/... ./src/harness/...` run from the repository root (module-directory patterns; a bare `./...` at the root matches no module and is not used), and `GOWORK=off go -C <dir> build ./...` runs in each of the three module directories, Then every command exits 0 and `go list -m` reports exactly the three module paths of RM-1 [proof: service:go-toolchain; deps: go-toolchain -- the pinned Go toolchain the gate host already runs]
- [ ] Scenario: Release build ignores the workspace -- Given `GOWORK=off`, When `go build -o <tmp>/dsh ./cmd/dsh` runs in `src/harness`, Then the build succeeds without any `replace` directive and the binary prints its version and exits 0 [proof: service:go-toolchain; deps: go-toolchain for the build, filesystem (`t.TempDir()`) for the output binary, os-process to run it]
- [ ] Scenario: Module pin test rejects a replace directive -- Given an in-memory copy of `src/harness/go.mod` with a `replace github.com/smartpcr/agent-plugin/src/go => ../go` line added, When `DH-T-TOOLCHAIN-11` checks the parsed bytes, Then it fails naming the `replace` line; with the committed bytes it passes [proof: in-process; deps: none -- go.mod bytes are embedded string fixtures]
- [ ] Scenario: cordis-sample golden matches pre-change behaviour -- Given `testdata/output.golden` committed before the first DH-S-2 edit, When `go -C src/go test -count=20 ./cmd/cordis-sample` runs without `-update` on Linux, macOS and Windows, Then every run's normalized output (bubble clock with `Settle`/`Pause` barriers, timestamp-free exporter, `<TMP>` path, `/` separators) is byte-identical to the golden -- including the exact number and position of `beat:` lines in each window -- no "coincident events" failure is raised, and `main` still calls `run` with `os.Stdout`, `os.TempDir()`, the console exporter, `time.Sleep` and a no-op `Settle` [proof: golden; deps: committed golden `src/go/cmd/cordis-sample/testdata/output.golden`]

## Stage 1.2: Gate Runner Core

### Implementation Steps
- [ ] impl.md step 1.2 / DH-D-TOOLCHAIN-2: create `tools/harness-gate` (own `go.mod`, may keep its own `go` line per RM-2) with `main.go`, `gates.go` (the fixed ordered list: gofmt, vet + own analyzers, race/shuffle tests, coverage merge + floor, `tools/harness-trace verify`, performance budgets, cross-compile) and `runner.go` that runs them sequentially, stops at the first failure, prints that gate's name and exits non-zero (QG-1, DH-ADR-TOOLCHAIN-9; `DH-T-TOOLCHAIN-9`).
- [ ] impl.md step 1.3 / DH-D-TOOLCHAIN-2: the test gate runs `go test -race -shuffle=on -count=1 ./...` per module with `CGO_ENABLED=1` (QG-4); a lane where `-race` cannot run is a gate failure, not a skip; add the virtual-time convention (`testing/synctest`) to `tools/harness-gate/README.md` (`DH-T-TOOLCHAIN-2`).
- [ ] impl.md step 1.4 / DH-D-TOOLCHAIN-2: add `tools/harness-gate/analyzers` with the project's own analyzers -- `nosleep` (no `time.Sleep` in `_test.go`), `onespawn` (no `os/exec` import outside `internal/world/subprocess`, SP-12), `cleanup` (goroutine/listener/child started in a test registers `t.Cleanup`, QG-5) -- run through `go vet -vettool`; no baseline or suppression file is read (QG-2; `DH-T-TOOLCHAIN-8`).
- [ ] impl.md step 1.5 / DH-D-TOOLCHAIN-2: coverage gate builds tests with `-cover`, collects `GOCOVERDIR` profiles from the test process and every re-exec'd child, merges them with `go tool covdata`, and fails any package under 85% of statements; there is no exemption list (QG-3, DH-ADR-14; `DH-T-TOOLCHAIN-7`).
- [ ] impl.md step 1.7 / DH-D-TOOLCHAIN-2: performance-budget gate runs tests named `TestBudget*` with fixed budgets compiled into the test; the gate clears `DSH_*` and `GO*` override variables from the child environment so budgets cannot be relaxed (QG-7; `DH-T-TOOLCHAIN-12`).
- [ ] Cross-compile gate: `GOWORK=off CGO_ENABLED=0 go build ./cmd/dsh` for linux, darwin and windows on amd64 and arm64 into a temp directory (QG-8).
- [ ] Wire `tools/harness-trace verify` as a gate (QG-9) and add `tools/harness-gate/testdata/` seeded-violation modules (a data race, an unformatted file, a package under the floor) used by the end-to-end gate tests.

### Dependencies
- phase-repository-modules-and-quality-gates/stage-three-module-workspace

### Test Scenarios
- [ ] Scenario: First failing gate is named -- Given an in-memory gate list whose third gate returns an error, When the runner executes, Then gates four onward never run, stdout names the third gate and the exit code is non-zero (TC-01a) [proof: in-process; deps: none -- fake gate funcs]
- [ ] Scenario: Coverage floor arithmetic over merged profiles -- Given two in-memory coverage profiles for package `p` (parent 70%, re-exec'd child covering the rest to 88% merged), When the floor check runs, Then `p` passes; with the child profile dropped it fails at 70% < 85% (TC-01a) [proof: in-process; deps: none]
- [ ] Scenario: Analyzer flags sleep in a test file -- Given source `func TestX(t *testing.T){ time.Sleep(1) }` parsed with `go/parser.ParseFile` from a string, When the `nosleep` predicate runs, Then exactly one diagnostic is returned at the call position (TC-01a) [proof: in-process; deps: none]
- [ ] Scenario: Seeded data race fails the race gate -- Given a scratch module in `t.TempDir()` with a racing test, When `harness-gate` runs against it, Then the run stops at the test gate, names it and exits non-zero (TC-01b) [proof: service:go-toolchain; deps: go-toolchain runs `go test -race`; os-process for the child; filesystem via `t.TempDir()`]
- [ ] Scenario: Seeded unformatted file and low-coverage package -- Given two further scratch modules (one unformatted file; one package at 40% coverage), When the gate runs on each, Then it names `gofmt` and `coverage` respectively and exits non-zero (TC-01b) [proof: service:go-toolchain; deps: go-toolchain; filesystem via `t.TempDir()`]
- [ ] Scenario: Own analyzers against fixture packages -- Given committed `tools/harness-gate/analyzers/testdata/src/...` fixtures with `// want` comments, When `analysistest.Run` loads them, Then every expected diagnostic is reported and none extra (TC-01b) [proof: service:go-toolchain; deps: go-toolchain package loader over committed testdata]

## Stage 1.3: Test Layers and Golden Helper

### Implementation Steps
- [ ] impl.md step 1.6 / DH-D-TOOLCHAIN-3 (part 1): add the golden helper `src/harness/internal/testkit/golden` (`golden.Assert(t, name, got)`), reading `testdata/<name>.golden` and rewriting it only when the single switch `-update` is set (QG-6, DH-ADR-TOOLCHAIN-1).
- [ ] impl.md step 1.6 / DH-D-TOOLCHAIN-3 (part 2): fuzz-target convention -- a `fuzzcheck` analyzer in `tools/harness-gate/analyzers` requires every package that declares a `//dsh:untrusted-parser` marker to have a `Fuzz*` function whose seeds are registered in code with `f.Add` from a Go slice or `//go:embed` bytes in `seeds_test.go` (so a plain `go test` exercises them with no runtime file read), plus an optional on-disk corpus under `testdata/fuzz/<FuzzName>/` that `go test` loads from the package directory (DH-ADR-TOOLCHAIN-3).
- [ ] impl.md step 1.6 / DH-D-TOOLCHAIN-3 (part 3): define the `replay` and `scenario` build tags; the gate's default `go test` excludes both; document in `src/harness/internal/testkit/README.md` that scenario files are authored as `*_scenario_test.go` with `//go:build scenario` (DH-ADR-TOOLCHAIN-4; `DH-T-TOOLCHAIN-4`).
- [ ] Record in `docs/stories/harness-DSH-M1/evidence/DH-S-1.md` that `DH-T-TOOLCHAIN-1`, `-3`, `-5`, `-6` wait for the replay/scenario helpers of DH-S-16 and `-10`, `-13` for DH-S-46 (impl.md DH-S-1 "Scenarios"), so they are listed open, not failed.

### Dependencies
- phase-repository-modules-and-quality-gates/stage-gate-runner-core

### Test Scenarios
- [ ] Scenario: Golden helper detects drift and updates only on the switch -- Given a golden file in `t.TempDir()` with content `a`, When `golden.Assert` is called with `b` without `-update`, Then the test fails with a diff and the file still holds `a`; with `-update` the file holds `b` (`DH-T-TOOLCHAIN-4`) [proof: service:filesystem; deps: filesystem via `t.TempDir()`]
- [ ] Scenario: Missing fuzz target is reported -- Given in-memory source of a package with the `//dsh:untrusted-parser` marker and no `Fuzz*` function, When the `fuzzcheck` predicate runs on the parsed AST, Then it reports one diagnostic; adding `func FuzzDecode(f *testing.F)` clears it [proof: in-process; deps: none]
- [ ] Scenario: Scenario-tagged tests are excluded from the default gate -- Given a scratch module containing `x_scenario_test.go` with `//go:build scenario` that always fails, When the test gate runs, Then it passes; with `-tags scenario` the failure appears [proof: service:go-toolchain; deps: go-toolchain; filesystem via `t.TempDir()`]

## Stage 1.4: CI and Release Rehearsal

### Implementation Steps
- [ ] impl.md step 1.8 / DH-D-TOOLCHAIN-4 (part 1): add a keyless `harness` job to `.github/workflows/ci.yml` with a matrix of `ubuntu-latest`, `macos-latest`, `windows-latest`, `CGO_ENABLED=1` and a C compiler provisioned for `-race` (QG-4), that only runs the gate module-locally with the workspace off -- `GOWORK=off go -C tools/harness-gate run . --root ../..` (`tools/harness-gate` is its own module and deliberately not in `go.work`, so RM-1's three product modules stay three; the gate itself sets `GOWORK=off` and `-C <module>` for each per-module command it spawns) -- and uploads its output as the evidence artifact.
  - Amended 2026-11-22 by decision `platforms-windows-wsl`: macOS leaves M1 scope, and the gate of record runs on two local lanes, Windows (native) and Linux under WSL on the development host. The hosted matrix keeps the lanes M1-D-GATE-21 / M1-D-TOOL-5 decide, as regression signals; plan step 12.3.7 of [gap-closure-plan.md](gap-closure-plan.md) changes `ci.yml`. The `macos-latest` entry above is historical.
- [ ] impl.md step 1.8 / DH-D-TOOLCHAIN-4 (part 2): add `.github/workflows/harness-scenarios.yml` (credentialed, `workflow_dispatch` and schedule only, never `pull_request_target`, QG-10) whose first job runs `GOWORK=off go -C tools/harness-gate run . preflight --root ../..`: fail when `DSH_DEEPSEEK_API_KEY` is absent or when any ID in `DSH_REQUIRE_SCENARIOS` has no matching scenario test (DH-ADR-TOOLCHAIN-6, -12). The required list for this story is empty because no scenario executes here (tech-spec sec. 3.2). In the same PR add the acceptance index `test/harness/acceptance-index.yaml`, keyed by executing stage (`DH-S-16`, `DH-S-17`, `DH-S-18`, `DH-S-22`, ...), each entry listing the authored `DH-E-*` IDs with their authoring stage; the workflow derives `DSH_REQUIRE_SCENARIOS` for a run from the entry of the stage being accepted, so an authored scenario that is missing from the index (or vice versa) fails preflight when that stage is accepted.
- [ ] impl.md step 1.9 / DH-D-TOOLCHAIN-5: add the `rehearse` subcommand (invoked as `GOWORK=off go -C tools/harness-gate run . rehearse --root ../..`), which builds `cmd/dsh` with `GOWORK=off CGO_ENABLED=0 -trimpath` for the six targets into a temp directory, prints each binary's SHA-256 and embedded version, and publishes nothing (DH-ADR-TOOLCHAIN-13, DH-ADR-15); call it from the `harness` CI job on Linux only. `release.yml` and `.goreleaser.yaml` are not changed.
- [ ] Author DH-S-1's scenarios (impl.md DH-S-1 "Scenarios", authored when their subjects exist; tech-spec sec. 2.2, EV-4): write `DH-E-TOOLCHAIN-2` ("a release rehearsal yields a runnable binary on every platform", subject = the `rehearse` subcommand above) as `src/harness/internal/testkit/scenarios/toolchain_rehearsal_scenario_test.go` with `//go:build scenario`, get it reviewed in the PR, and add it to the `DH-S-18` entry of `test/harness/acceptance-index.yaml`; add `DH-E-TOOLCHAIN-1` ("record against the real model, then replay without a key") to the `DH-S-17` entry with authoring stage DH-S-11, because its subjects (the model runtime and the replay helpers) do not exist until Phase 11 -- its file is written by the Phase 11 authoring step. Neither is executed in this story.
- [ ] Close DH-S-1: fill `docs/stories/harness-DSH-M1/evidence/DH-S-1.md` per impl.md sec. 7.3 (commit, Go version, gate output from each gate lane [Windows native and Linux under WSL, decision `platforms-windows-wsl`; three OS lanes in the historical record], CI run ids, the seeded-violation manual checks of impl.md DH-S-1, and the scenario line: `DH-E-TOOLCHAIN-2` authored, reviewed and indexed to DH-S-18; `DH-E-TOOLCHAIN-1` indexed to DH-S-17 with authoring at DH-S-11) and tick impl.md steps 1.1-1.9 as built.

### Dependencies
- phase-repository-modules-and-quality-gates/stage-gate-runner-core
- phase-repository-modules-and-quality-gates/stage-test-layers-and-golden-helper

### Test Scenarios
- [ ] Scenario: Preflight fails on a missing secret -- Given an injected environment map without `DSH_DEEPSEEK_API_KEY`, When the preflight decision function runs, Then it returns a failure naming the missing secret and no scenario is started [proof: in-process; deps: none -- environment passed as a map]
- [ ] Scenario: Preflight fails on an unknown required scenario -- Given `DSH_REQUIRE_SCENARIOS=DH-E-FAKE-9` and an in-memory scenario index without it, When preflight runs, Then it fails naming `DH-E-FAKE-9`; an empty required list passes [proof: in-process; deps: none]
- [ ] Scenario: Rehearsal produces six pure-Go binaries and publishes nothing -- Given `GOWORK=off`, When `harness-gate rehearse --out <tmp>` runs, Then six binaries (linux/darwin/windows x amd64/arm64) exist in the temp dir, each built with `CGO_ENABLED=0`, and no network call or tag is made (TC-01b) [proof: service:go-toolchain; deps: go-toolchain cross-compile; filesystem via `t.TempDir()`]
- [ ] Scenario: Gate passes on Windows (native) and Linux (WSL) -- Given the story branch at the stage-closing commit, When the gate runs on the Windows (native) lane and on the Linux lane under WSL (the lane scripts of gap-closure-plan step 12.3.6), Then each lane's gate output ends with every gate passed, recorded with its run-record id in the local-lane evidence format in the DH-S-1 evidence record [proof: service:go-toolchain; deps: go-toolchain on the two local lanes]. Amended 2026-11-22 by decision `platforms-windows-wsl`; the historical title was "Gate passes on all three CI operating systems", run by the hosted `harness` job on Linux, macOS and Windows.
- [ ] Scenario: Authored scenarios are indexed and compile but do not run -- Given `toolchain_rehearsal_scenario_test.go` and `test/harness/acceptance-index.yaml`, When the gate's index check runs and `go -C src/harness vet -tags scenario ./...` runs, Then every `*_scenario_test.go` ID appears in exactly one index entry (`DH-E-TOOLCHAIN-2` under `DH-S-18`), every index ID authored at or before DH-S-1 has a file, the vet exits 0, and the default gate `go test` run lists zero scenario tests [proof: service:go-toolchain; deps: go-toolchain package loader over the checkout; index read from the checkout by the gate]

### Cross-Stage Dependencies
- phase-repository-modules-and-quality-gates/stage-three-module-workspace

# Phase 2: Cordis Port Guarantees

## Dependencies
- phase-repository-modules-and-quality-gates

## Stage 2.1: Final Unload and Fiber Cancellation

### Implementation Steps
- [ ] impl.md step 2.1 / DH-D-CORDIS-1: make unload complete, exclusive and final in `src/go/cordis/fiber.go` and `effect.go` -- dependents unload first, a second concurrent unload waits for the first, `Wait` returns only after every effect's disposer has returned, and a disposed fiber refuses new effects (SP-10; `DH-T-CORDIS-1`, `DH-T-CORDIS-2` and the third test named by libs/cordis.md).
- [ ] impl.md step 2.2 / DH-D-CORDIS-2: give every loaded fiber a `context.Context` (`Fiber.Ctx()`), derived from its parent's, cancelled by unload before disposers run, with the unload cause available through `context.Cause` (`DH-T-CORDIS-4`).
- [ ] Re-run the TC-02b golden; if the sample's output changes, re-record only the lines the change explains, and note each intended diff in `docs/stories/harness-DSH-M1/evidence/DH-S-2.md` (impl.md DH-S-2 manual check).

### Dependencies
- phase-repository-modules-and-quality-gates/stage-three-module-workspace

### Test Scenarios
- [ ] Scenario: Unload is final under concurrency -- Given a fiber with three effects whose disposers block until released, When two goroutines call `Dispose` at once under `-race`, Then both return only after all three disposers ran exactly once and a later `Effect` call returns the disposed error (`DH-T-CORDIS-1`, TC-02a) [proof: in-process; deps: none]
- [ ] Scenario: Dependents unload before their provider -- Given fiber B injecting a service provided by fiber A, When A is unloaded, Then B's disposer observes A's service still live and A's disposer runs after B's (`DH-T-CORDIS-2`) [proof: in-process; deps: none]
- [ ] Scenario: Fiber context is cancelled by unload with a cause -- Given a loaded fiber whose effect blocks on `<-fiber.Ctx().Done()`, When the fiber is unloaded, Then the effect returns, `context.Cause(ctx)` is the unload cause and `Wait` returns after it (`DH-T-CORDIS-4`) [proof: in-process; deps: none]
- [ ] Scenario: Existing cordis behaviour preserved -- Given the committed cordis-sample golden and existing `cordis_test.go` / `race_test.go`, When the stage's changes land, Then the existing tests pass and the golden comparison passes (or differs only on lines recorded as intended) (TC-02b) [proof: golden; deps: committed golden `src/go/cmd/cordis-sample/testdata/output.golden`]

## Stage 2.2: Context Values and Scoped Dispatch

### Implementation Steps
- [ ] impl.md step 2.3 / DH-D-CORDIS-3: add context values and scoped dispatch in `src/go/cordis/context.go` and `events.go` -- a value set on a child `Context` is visible to its subtree only, and an `EventKey` emit can be scoped to one subtree (one agent) (`DH-T-CORDIS-5`, `DH-T-CORDIS-6`).
- [ ] impl.md step 2.4 / DH-D-CORDIS-4: add contained notification `Notify` in `events.go`: a listener's panic or error is recorded and logged but cannot veto or abort the emitted fact; other listeners still run (`DH-T-CORDIS-7`).
- [ ] impl.md step 2.5 / DH-D-CORDIS-7: add `AroundKey[Req, Res]` in `keys.go` -- middleware listeners wrap a call with distinct request and result types, ordered by registration, dispatched through context values (`DH-T-CORDIS-12`).

### Dependencies
- phase-cordis-port-guarantees/stage-final-unload-and-fiber-cancellation

### Test Scenarios
- [ ] Scenario: Scoped dispatch reaches one subtree -- Given two agent subtrees each listening on the same `EventKey`, When an event is emitted scoped to subtree A, Then only A's listener runs and B's call count stays 0 (`DH-T-CORDIS-6`, TC-02a) [proof: in-process; deps: none]
- [ ] Scenario: A listener cannot veto a notified fact -- Given three `Notify` listeners where the second panics, When the fact is notified, Then listeners one and three run, the panic is recorded once and `Notify` returns normally (`DH-T-CORDIS-7`) [proof: in-process; deps: none]
- [ ] Scenario: Around key wraps in registration order -- Given two around listeners that append `a` and `b` to the request and `B` and `A` to the result, When the key is called with an inner handler, Then the handler sees `ab` and the caller receives the result suffixed `BA` (`DH-T-CORDIS-12`) [proof: in-process; deps: none]

## Stage 2.3: Config Views and Strict Decoding

### Implementation Steps
- [ ] impl.md step 2.6 / DH-D-CORDIS-5: add raw, effective and candidate config views to `src/go/cordis/component.go` and `state.go`, plus a config hook that may transform the effective config and that the expression seam of DH-S-3 plugs into (`DH-T-CORDIS-8`, `DH-T-CORDIS-9`).
- [ ] impl.md step 2.7 / DH-D-CORDIS-8: strict config decoding -- unknown fields and type mismatches fail with the field path; `WithSchema` decoding no longer drops unknown keys silently (`DH-T-CORDIS-13`).
- [ ] impl.md step 2.8 / DH-D-CORDIS-9: `tree.go` / `logger.go` accept the log exporters chosen by the tree's creator (`New(WithExporters(...))`); no global exporter is installed (`DH-T-CORDIS-14`).
- [ ] Close DH-S-2: write `docs/stories/harness-DSH-M1/evidence/DH-S-2.md` (the eight `DH-T-CORDIS-*` tests under `-race`, existing `src/go` tests, TC-02b result, manual check) and tick impl.md steps 2.1-2.8 as built. DH-D-CORDIS-6, -10, -11 stay for DH-S-38/DH-S-49.

### Dependencies
- phase-cordis-port-guarantees/stage-context-values-and-scoped-dispatch

### Test Scenarios
- [ ] Scenario: Candidate config does not leak into effective config -- Given a component with effective config `{n:1}`, When a candidate `{n:2}` is validated and rejected, Then raw and effective both still read `{n:1}` and the hook was not called with `{n:2}` (`DH-T-CORDIS-8`, TC-02a) [proof: in-process; deps: none]
- [ ] Scenario: Unknown config field fails with its path -- Given a schema with field `retries` and input `{retrys: 3}`, When the config is decoded strictly, Then decoding fails with an error naming `retrys` and the component is not constructed (`DH-T-CORDIS-13`) [proof: in-process; deps: none]
- [ ] Scenario: Tree logs only to the chosen exporter -- Given two trees, one created with an in-memory exporter and one with none, When each logs a line, Then only the first exporter holds exactly one record and no package-global sink received anything (`DH-T-CORDIS-14`) [proof: in-process; deps: none]
- [ ] Scenario: Sample output after all eight changes -- Given the TC-02b golden, When `go test ./cmd/cordis-sample` runs at the stage-closing commit, Then it passes with only the intended re-recordings listed in the DH-S-2 evidence record [proof: golden; deps: committed golden `src/go/cmd/cordis-sample/testdata/output.golden`]

# Phase 3: Loader Layers and Write-Back

## Dependencies
- phase-cordis-port-guarantees

## Stage 3.1: Codec Seam and Entry Schema

### Implementation Steps
- [ ] impl.md step 3.1 / DH-D-LOADER-1: add `src/go/loader/codec.go` (a `Codec` seam: decode bytes into nodes carrying file, line and column) and thread positions through `merge.go` and `configfile.go`; the existing JSON path keeps working (`DH-T-LOADER-1`; existing `loader_test.go` passes unchanged).
- [ ] impl.md step 3.2 / DH-D-LOADER-3: entry schema and full validation in `entry.go` / `loader.go` -- every entry is validated in one pass and all errors are reported with positions, not only the first (`DH-T-LOADER-3`).
- [ ] impl.md step 3.3 / DH-D-LOADER-4: expression seam in new `src/go/loader/expr.go` -- the loader calls an injected `Evaluator` through the DH-D-CORDIS-5 config hook; `src/go` ships no evaluator (NG-5; `DH-T-LOADER-4`, `DH-T-LOADER-5`).

### Dependencies
- phase-cordis-port-guarantees/stage-config-views-and-strict-decoding

### Test Scenarios
- [ ] Scenario: Diagnostic carries file, line and column -- Given in-memory layer bytes named `base.json` with a type error on line 4 column 9, When the loader validates, Then the diagnostic text equals the committed golden `base.json:4:9: ...` (`DH-T-LOADER-1`, TC-03a) [proof: golden; deps: committed golden `src/go/loader/testdata/diag_position.golden`]
- [ ] Scenario: All entry errors reported in one pass -- Given an in-memory document with three invalid entries, When validation runs, Then three diagnostics are returned in document order (`DH-T-LOADER-3`, TC-03a) [proof: in-process; deps: none]
- [ ] Scenario: Expression seam calls the injected evaluator -- Given an entry value `${{ env.X }}` and a fake evaluator returning `7`, When the entry is loaded, Then the effective config holds `7` and the raw config still holds the expression text (`DH-T-LOADER-4`) [proof: in-process; deps: none]

## Stage 3.2: Layer Composition and Write-Back

### Implementation Steps
- [ ] impl.md step 3.4 / DH-D-LOADER-6: add `src/go/loader/layers.go` -- ordered layers (bundle, profile, user, project) compose into one tree and an ownership index answers "which layer supplied this value" for every path (`DH-T-LOADER-7`).
- [ ] impl.md step 3.5 / DH-D-LOADER-7: add `src/go/loader/layerset.go` -- a layered source that writes an edit back to exactly one named layer through the codec, preserving the other layers byte-for-byte (`DH-T-LOADER-8`, `DH-T-LOADER-14`). Scenario DH-E-LOADER-1 is authored in DH-S-6 and runs in DH-S-17 (17.7).
- [ ] impl.md step 3.9 / DH-D-LOADER-13 (P1, pulled forward): a write guard on the layer source -- a write is refused when the file changed since it was read (content hash compare under a lock), so a concurrent edit is never lost (`DH-T-LOADER-15`).

### Dependencies
- phase-loader-layers-and-write-back/stage-codec-seam-and-entry-schema

### Test Scenarios
- [ ] Scenario: Ownership index names the supplying layer -- Given in-memory layers base `{a:1,b:1}` and user `{b:2}`, When composed, Then the effective tree is `{a:1,b:2}` and the index reports `a` from base and `b` from user (`DH-T-LOADER-7`, TC-03a) [proof: in-process; deps: none]
- [ ] Scenario: Write-back touches one layer file only -- Given base and user layer files in `t.TempDir()`, When `b` is set to 3 through the layer set targeting `user`, Then `user` equals the committed golden (comments kept) and `base` is byte-identical to before (`DH-T-LOADER-8`, TC-03b) [proof: golden; deps: filesystem via `t.TempDir()`; committed golden `src/go/loader/testdata/writeback_user.golden`]
- [ ] Scenario: Write guard refuses a stale write -- Given a layer file read by the layer set and then modified on disk by another writer, When the layer set writes back, Then the write fails with a stale-source error and the file holds the other writer's content (`DH-T-LOADER-15`, TC-03b) [proof: service:filesystem; deps: filesystem via `t.TempDir()`]

## Stage 3.3: Nested Loaders and Entry Restart

### Implementation Steps
- [ ] impl.md step 3.6 / DH-D-LOADER-10: a component can run a loader of its own under its fiber context (nested loader), whose entries unload with the component (needed later by agent presets, A27).
- [ ] impl.md step 3.7 / DH-D-LOADER-8 (P1, pulled forward): mutation, lookup and lifecycle-log API on `loader.go` (`Set`, `Lookup(path)`, entry lifecycle events) (`DH-T-LOADER-9`).
- [ ] impl.md step 3.8 / DH-D-LOADER-11 (P1, pulled forward): restart one entry without touching its siblings, and emit one settled-reload notice after all restarts of an edit have finished (libs/cordis-plugins.md).
- [ ] Close DH-S-3: write `docs/stories/harness-DSH-M1/evidence/DH-S-3.md` (nine `DH-T-LOADER-*` tests, the hand-composed two-layer manual check with the broken entry's file:line:col) and tick impl.md steps 3.1-3.9 as built.

### Dependencies
- phase-loader-layers-and-write-back/stage-layer-composition-and-write-back

### Test Scenarios
- [ ] Scenario: Nested loader unloads with its owner -- Given component C running a nested loader with two entries, When C is unloaded, Then both nested entries' disposers have run before C's `Wait` returns (TC-03a) [proof: in-process; deps: none]
- [ ] Scenario: Restart one entry, siblings untouched -- Given three loaded entries, When entry two's config changes and it is restarted, Then entry two's construct count is 2, entries one and three stay at 1, and exactly one settled-reload notice is emitted (`DH-D-LOADER-11`) [proof: in-process; deps: none]
- [ ] Scenario: Lifecycle log records mutations in order -- Given a loader, When an entry is added, set and removed through the mutation API, Then the lifecycle log holds `add`, `update`, `remove` for that path in order (`DH-T-LOADER-9`) [proof: in-process; deps: none]

# Phase 4: SDK Codec Schema and Leaf Packages

## Dependencies
- phase-cordis-port-guarantees
- phase-loader-layers-and-write-back

## Stage 4.1: JSONRPC Codec and Shape Schema

### Implementation Steps
- [ ] impl.md step 4.1 / DH-D-SDK-1: implement `src/sdk/jsonrpc` -- bounded line-delimited framing (max line bytes, max pending requests), request/response/notification types, and one error table keyed `<domain>/<reason>`; mark it `//dsh:untrusted-parser` and add `FuzzDecode` with seeds registered via `f.Add` from an embedded slice in `seeds_test.go` and an on-disk corpus under `testdata/fuzz/FuzzDecode/` (QG-6). Standard library only (RM-3).
- [ ] impl.md step 4.2 / DH-D-UTIL-1: implement `src/harness/internal/schema` -- one description of a data shape that derives JSON Schema, validates a value against it, and computes a stable fingerprint; fuzz target on validation input (libs/zod.md).
- [ ] impl.md step 4.3 / DH-D-SDK-2: implement `src/sdk/protocol` -- SDK Go types, the method table, and a derived schema file `src/sdk/protocol/schema.json` generated by `go generate` and committed; `src/sdk` re-implements the derivation it needs (it may not import `src/harness`).
- [ ] Add the import-direction test `src/harness/internal/archtest` (via `go list -deps -json`) asserting: `src/sdk` imports neither `src/go` nor `src/harness`; `src/go` imports neither; harness implementation packages are under `internal/` (RM-3, RM-5). Later stages extend its rule table.

### Dependencies
- phase-loader-layers-and-write-back/stage-nested-loaders-and-entry-restart

### Test Scenarios
- [ ] Scenario: Codec rejects an oversized line -- Given an in-memory reader with one line one byte over the configured bound, When the codec reads, Then it returns `jsonrpc/line-too-long` and does not allocate past the bound (TC-04a) [proof: in-process; deps: none]
- [ ] Scenario: Embedded fuzz seeds decode without panicking -- Given the seed slice compiled into `src/sdk/jsonrpc/seeds_test.go` (Go literals or `//go:embed` bytes, resolved at build time), When `TestDecodeSeeds` feeds each seed to the decoder in memory, Then every seed decodes or fails with a table error and none panics (TC-04a) [proof: in-process; deps: none -- seeds are Go values in the test binary; no file is opened at run time]
- [ ] Scenario: On-disk fuzz corpus replays as a unit test -- Given the committed files in `src/sdk/jsonrpc/testdata/fuzz/FuzzDecode/`, When `go -C src/sdk test -run FuzzDecode ./jsonrpc` runs without `-fuzz`, Then the Go test runner reads every corpus file and each input decodes or fails with a table error, none panicking (filesystem-backed complement of TC-04a; architecture.md's TC-04a row covers only the in-memory seeds) [proof: service:filesystem; deps: filesystem -- `go test` reads the committed corpus from the package directory of the checkout the gate runs in]
- [ ] Scenario: Derived protocol schema matches golden -- Given the protocol Go types, When the schema is derived, Then it is byte-identical to committed `src/sdk/protocol/schema.json` (TC-04a) [proof: golden; deps: committed golden `src/sdk/protocol/schema.json`]
- [ ] Scenario: src/sdk imports nothing of the product -- Given the three modules, When the archtest lists `src/sdk` dependencies, Then none has prefix `github.com/smartpcr/agent-plugin/src/go` or `/src/harness` (DH-S-4 exit evidence) [proof: service:go-toolchain; deps: go-toolchain `go list -deps`]

## Stage 4.2: Model Vocabulary and Value Meanings

### Implementation Steps
- [ ] impl.md step 4.4 / DH-D-UTIL-4: one meaning each for globs, versions, instants and recurrence rules in `src/harness/internal/util/` sub-packages (names indicative per RM-9), each with table tests and a fuzz target for the parsers.
- [ ] impl.md step 4.5 / DH-D-LLM-11: implement the leaf `src/harness/internal/llm/vocab` -- `Message`, `Block` and its sources, tool schema, `Controls`, route id, failure classification and the canonical encoding used for request digests. It holds NO request type (RM-6; C07); add the archtest rule that `vocab` imports no area package (`DH-T-LLM-1`, vocabulary clauses).
- [ ] Commit golden fingerprints of every vocabulary type that will be persisted (`internal/llm/vocab/testdata/fingerprints.golden`) so DH-D-STORE-5 can detect a silent change.

### Dependencies
- phase-sdk-codec-schema-and-leaf-packages/stage-jsonrpc-codec-and-shape-schema

### Test Scenarios
- [ ] Scenario: Canonical encoding is stable -- Given two `Message` values built with map fields inserted in different orders, When each is canonically encoded, Then the bytes and SHA-256 digests are identical (`DH-T-LLM-1`, TC-04a) [proof: in-process; deps: none]
- [ ] Scenario: Vocabulary fingerprints unchanged -- Given the committed fingerprint golden, When the schema package fingerprints every vocab type, Then each equals its golden line (TC-04a) [proof: golden; deps: committed golden `internal/llm/vocab/testdata/fingerprints.golden`]
- [ ] Scenario: vocab holds no request type -- Given the parsed AST of every file in `internal/llm/vocab` (in-memory via `go/parser`), When the archtest predicate looks for a type named `Request` or a struct holding `[]Message` plus `Controls`, Then none is found (TC-05 rule) [proof: in-process; deps: none -- sources embedded via `embed.FS` in the test]
- [ ] Scenario: Glob meaning is single -- Given the table `**/*.go` vs `a/b/c.go` (match), `*.go` vs `a/b.go` (no match), When matched with the util glob package, Then results equal the table on all three OS lanes (`DH-D-UTIL-4`) [proof: in-process; deps: none]

## Stage 4.3: File Primitives Diff Search and Watcher

### Implementation Steps
- [ ] impl.md step 4.6 / DH-D-FS-1: implement `src/harness/internal/util/fsx` -- atomic replace (temp file + rename, Windows retry), exclusive create, advisory file locks and file identity (device+inode / volume+index) (areas/filesystem.md).
- [ ] impl.md step 4.7 / DH-D-FS-2: implement `src/harness/internal/util/textdiff` -- line diffs for display and the model's patch format, with apply and round-trip.
- [ ] impl.md step 4.8 / DH-D-FS-3: implement `src/harness/internal/util/search` -- in-process content search and file listing honouring ignore files.
- [ ] impl.md step 4.9 / DH-D-UTIL-3 (P1, pulled forward): implement `src/harness/internal/util/watch` -- a component is told when a file or directory changed, debounced, one notice per settled change (needed by three P0 deliverables).

### Dependencies
- phase-sdk-codec-schema-and-leaf-packages/stage-jsonrpc-codec-and-shape-schema

### Test Scenarios
- [ ] Scenario: Atomic replace never exposes a partial file -- Given a file in `t.TempDir()` and a reader goroutine reading it in a loop, When `fsx.Replace` writes new content 200 times, Then every read returns either the old or the new full content (TC-04b) [proof: service:filesystem; deps: filesystem via `t.TempDir()`]
- [ ] Scenario: Patch round-trip -- Given in-memory texts `a` and `b`, When a model-format patch from `a` to `b` is produced and applied to `a`, Then the result equals `b` byte-for-byte (`DH-D-FS-2`, TC-04a) [proof: in-process; deps: none]
- [ ] Scenario: Search honours ignore files -- Given a temp tree with `.gitignore` excluding `build/`, When `search.List` runs, Then no path under `build/` is returned and the listing equals the committed golden (TC-04b) [proof: golden; deps: filesystem via `t.TempDir()`; committed golden `internal/util/search/testdata/list.golden`]
- [ ] Scenario: Watcher reports one notice per save -- Given a watch on a temp directory, When a file is replaced atomically, Then exactly one change notice for that path arrives within a bounded deadline (TC-04b) [proof: service:filesystem; deps: filesystem via `t.TempDir()` + OS file-change notification]

## Stage 4.4: YAML Expressions Logging and Catalog

### Implementation Steps
- [ ] impl.md step 4.10 / DH-D-LOADER-2: YAML codec in the harness module (`src/harness/internal/boot/config`, plugging the DH-D-LOADER-1 seam) using the YAML module chosen by libs/yaml.md, pinned with a reason in `src/harness/go.mod` (RM-10) (`DH-T-LOADER-2`).
- [ ] impl.md step 4.11 / DH-D-BOOT-4: implement `src/harness/internal/boot/yamlx` -- one YAML dialect (no anchors-to-code, no custom tags) and edits that keep comments; fuzz target (QG-6).
- [ ] impl.md step 4.12 / DH-D-LOADER-5: implement `src/harness/internal/boot/expr` -- the expression engine behind the DH-D-LOADER-4 seam, side-effect free (NG-5) (`DH-T-LOADER-6`).
- [ ] impl.md step 4.13 / DH-D-LOADER-12: logging component for the harness (structured `log/slog` handler as a cordis component; exporters chosen per DH-D-CORDIS-9).
- [ ] impl.md step 4.14 / DH-D-BOOT-1: implement `src/harness/internal/boot/catalog` -- the build knows its compiled-in components by name and prints them (used by `dsh` preview later).
- [ ] Close DH-S-4: write `docs/stories/harness-DSH-M1/evidence/DH-S-4.md` (import-direction result, manual watcher/search checks of impl.md DH-S-4), author the file/search/watcher scenarios indexed to DH-S-18 as `*_scenario_test.go` (`//go:build scenario`, not executed), and tick impl.md steps 4.1-4.14 as built.

### Dependencies
- phase-sdk-codec-schema-and-leaf-packages/stage-jsonrpc-codec-and-shape-schema
- phase-sdk-codec-schema-and-leaf-packages/stage-file-primitives-diff-search-and-watcher

### Test Scenarios
- [ ] Scenario: Same entries through JSON and YAML codecs -- Given equivalent in-memory JSON and YAML documents, When loaded through each codec, Then the effective trees are equal and positions point into the respective source (`DH-T-LOADER-2`, TC-04a) [proof: in-process; deps: none]
- [ ] Scenario: yamlx edit keeps comments -- Given an in-memory YAML document with comments, When `yamlx` sets one scalar, Then the output equals the committed golden with all comments retained (TC-04a) [proof: golden; deps: committed golden `internal/boot/yamlx/testdata/edit_keep_comments.golden`]
- [ ] Scenario: Expression engine has no side effects -- Given expressions referencing `env`, `launch` and an undefined name, When evaluated against an in-memory scope, Then defined names resolve, the undefined one fails with its name, and no function can reach the filesystem or network (`DH-T-LOADER-6`) [proof: in-process; deps: none]
- [ ] Scenario: Catalog lists components by name -- Given the compiled-in catalog, When it is printed, Then the output equals the committed golden list (TC-04a) [proof: golden; deps: committed golden `internal/boot/catalog/testdata/catalog.golden`]

# Phase 5: Session Core Contracts

## Dependencies
- phase-sdk-codec-schema-and-leaf-packages

## Stage 5.1: Event Vocabulary and Store Contract

### Implementation Steps
- [x] impl.md step 5.1 / DH-D-SESSION-1: implement the typed, versioned event registry in `src/harness/internal/session` (event type name, version, schema from `internal/schema`, codec); unknown or newer versions are surfaced, never guessed (`DH-T-SESSION-1`, `-2`).
- [x] impl.md step 5.2 / DH-D-SESSION-8: per-agent and per-preset registrations with one owner and no borrowed authority (`DH-T-SESSION-17`, `-18`).
- [x] impl.md step 5.3 / DH-D-STORE-1: define the `Store` interface in `src/harness/internal/session/store`, the reusable conformance suite `storetest.Run(t, factory)`, and the in-memory reference `memstore` (`DH-T-STORE-1` on `memstore`) (C10).
- [x] impl.md step 5.4 / DH-D-STORE-5: each event type evolves by itself; a persisted-type inventory file `internal/session/store/testdata/persisted.golden` lists type, version and schema fingerprint, and a test fails when a fingerprint changes without a version bump and an upgrade note (`DH-T-STORE-10`).

### Dependencies
- phase-sdk-codec-schema-and-leaf-packages/stage-model-vocabulary-and-value-meanings
- phase-sdk-codec-schema-and-leaf-packages/stage-yaml-expressions-logging-and-catalog

### Test Scenarios
- [ ] Scenario: Unknown event version surfaces -- Given an in-memory encoded event `tool/result` version 3 while the registry knows version 2, When decoded, Then the decoder returns a typed unknown-version error and no value is fabricated (`DH-T-SESSION-2`, TC-05) [proof: in-process; deps: none]
- [ ] Scenario: Conformance suite passes on memstore -- Given `storetest.Run` with the `memstore` factory, When the suite runs, Then every case (append order, flush barrier semantics, paged reads, listing) passes (`DH-T-STORE-1`, TC-05) [proof: in-process; deps: none]
- [ ] Scenario: Persisted type cannot change unnoticed -- Given a test-only change adding a field to a persisted event without a version bump, When the inventory test runs against the committed golden, Then it fails naming that type (`DH-T-STORE-10`) [proof: golden; deps: committed golden `internal/session/store/testdata/persisted.golden`]

## Stage 5.2: Live Session History and Brackets

### Implementation Steps
- [x] impl.md step 5.5 / DH-D-SESSION-2: a live session over a `Store` that accepts events in order, assigns sequence numbers, delivers each event once to subscribers and holds no history of its own (`DH-T-SESSION-3`..`-8`, `-19` under `-race`).
- [x] impl.md step 5.6 / DH-D-SESSION-3: folds that compute model history, the request header and tool history from the log (`DH-T-SESSION-9`, `-10`, `-11`, `-14`).
- [x] impl.md step 5.7 / DH-D-SESSION-4: the opaque `session.Request` built only by `NextRequest` (loop) and `RequestFrom(seq)` (auxiliary, from one committed builder event), with `Digest()`, and `surface.Rebuild` recomputing a request from the log prefix ending at `requestAsOf`; no other package can construct one (SP-1; `DH-T-SESSION-12`).
- [x] impl.md step 5.8 / DH-D-SESSION-5: bracket checking on append (`turn/start`..`turn/end`, `step/start`..`step/end`) and closers on failure; `interrupted` is written only by the session core (SP-3; `DH-T-SESSION-13`).

### Dependencies
- phase-session-core-contracts/stage-event-vocabulary-and-store-contract

### Test Scenarios
- [ ] Scenario: Ordered once-only delivery under race -- Given a live session on `memstore` and three subscribers, When 1,000 events are appended from 8 goroutines under `-race`, Then each subscriber receives every event exactly once in sequence order (`DH-T-SESSION-3`, TC-05) [proof: in-process; deps: none]
- [ ] Scenario: Rebuilt request equals digest -- Given a hand-written event sequence (user message, assistant tool call, tool result), When `NextRequest` is built and later `surface.Rebuild` is called over the log prefix ending at the request's `requestAsOf`, Then the canonical bytes and `Digest()` are identical; separately, `RequestFrom(seq)` on one committed builder event yields an auxiliary request whose `Meta()` names that event (`DH-T-SESSION-12`, TC-05) [proof: in-process; deps: none]
- [ ] Scenario: Unbalanced bracket refused on append -- Given an open `step/start` inside `turn/start`, When `turn/end` is appended before `step/end`, Then the append is refused with a bracket error and the log is unchanged; a failure path appends `step/end` then `turn/end` (property test, `DH-T-SESSION-13`) [proof: in-process; deps: none]

## Stage 5.3: Model Runtime Agent Contract and Registries

### Implementation Steps
- [x] impl.md step 5.9 / DH-D-LLM-1: implement the runtime in `src/harness/internal/llm` -- route, attempt and settle. The runtime holds no session writer: `Prepared.Stream` always ends with exactly one Finish chunk and yields exactly one attempt record (an error, panic, missing finish or cancellation becomes that Finish and record), and `Complete` hands its one record to the caller's `AttemptSink`; the CALLER (driver C27, or the auxiliary caller via its `AttemptSink`) logs that record as exactly one settlement event (`assistant/message` or `assistant/attempt`) carrying `requestAsOf` and `requestDigest` (architecture.md sec. 5.4; SP-1, SP-2; `DH-T-LLM-1`..`-4`).
- [x] impl.md step 5.10 / DH-D-AGENT-1: implement the agent contract in `src/harness/internal/agent` -- find, address and own agents; one owner per agent (`DH-T-AGENT-1`, `-2`, `-3`, `-27`).
- [x] impl.md step 5.11 / DH-D-AGENT-4: three typed steering points on cordis `AroundKey`s with closed decision types -- `agent.PreStep` returns `Enter{Messages, StartsSeries}` or `Reject{Reason}` (a reject ends the turn `blocked` and no `step/start` is appended), `AttemptFailed` returns `Retry{After, Reason}` or `GiveUp{}`, `agent.OnTurnStopping` returns `AllowStop{}` or `KeepOpen{}`; an error or panic from a listener of any of the three ends the turn `error` (`extension/failed`); every other lifecycle fact is observe-only via `Notify`, whose observer failures never change an outcome (architecture.md sec. 5.6; `DH-T-AGENT-3`, `-10`..`-14`).
- [x] impl.md step 5.12 / DH-D-HUMAN-1: implement the interaction registry in `src/harness/internal/human` -- one registry of pending approvals and questions, addressed by agent.
- [x] impl.md step 5.13 / DH-D-VIEW-1: implement the fold registry in `src/harness/internal/session/views` -- folds registered once, read at one cut (one sequence number for all folds of a read).
- [x] Close DH-S-5: write `docs/stories/harness-DSH-M1/evidence/DH-S-5.md` (conformance suite on memstore, persisted-type test, the manual rebuilt-request check) and tick impl.md steps 5.1-5.13 as built.

### Dependencies
- phase-session-core-contracts/stage-live-session-history-and-brackets

### Test Scenarios
- [ ] Scenario: One attempt, one caller-logged settlement -- Given the runtime with an in-memory fake route that fails once then succeeds, an in-memory session store that records every append, and a caller-side recording `AttemptSink`, When the caller runs one `Prepared.Stream` attempt (and, separately, one `Complete` call) and logs the record it receives, Then the stream ends with exactly one Finish chunk, the runtime delivers exactly one attempt record per attempt carrying `requestAsOf` and `requestDigest`, the store holds exactly one settlement event per attempt and every append is attributed to the caller, and the runtime itself made zero session appends (`DH-T-LLM-2`, TC-05) [proof: in-process; deps: none]
- [ ] Scenario: Rejected PreStep closes the turn blocked; observers cannot veto -- Given a `agent.PreStep` listener returning `Reject{Reason: "quota"}` and a `Notify` observer that panics, When the driver reaches point 4 of a step, Then no `step/start` event is appended, `turn/end` is appended with outcome `blocked` carrying the reason, and the observer panic is recorded without changing that outcome; a second run where the `PreStep` listener itself panics ends the turn `error` with `extension/failed` (`DH-T-AGENT-10`) [proof: in-process; deps: none]
- [ ] Scenario: Folds read at one cut -- Given two registered folds and a concurrent appender, When a reader requests both folds, Then both results are computed at the same sequence number (`DH-D-VIEW-1`, TC-05) [proof: in-process; deps: none]
- [ ] Scenario: Pending interaction is addressable by agent -- Given two agents each with a pending approval, When agent A's interactions are listed, Then only A's approval is returned and answering it resolves exactly that one (`DH-D-HUMAN-1`) [proof: in-process; deps: none]

# Phase 6: Boot Launch Egress Secrets and Test Kit

## Dependencies
- phase-loader-layers-and-write-back
- phase-sdk-codec-schema-and-leaf-packages
- phase-session-core-contracts

## Stage 6.1: Leaf Helpers and Process Trees

### Implementation Steps
- [ ] impl.md step 6.1 / DH-D-UTIL-5: leaf packages `internal/util/herr` (error kinds `<domain>/<reason>`), `ident` (ids), `homedir` (home location) and `mediatype`; `herr` and `ident` import the standard library only (RM-6). Built contract-first inside dependency circle C-1.
- [ ] impl.md step 6.2 / DH-D-EXEC-1: implement `src/harness/internal/world/subprocess` -- the one place a program starts, from an argument list only (no shell string), with an explicit environment that never inherits secrets (SP-11, SP-12); the `onespawn` analyzer of stage 1.2 now enforces it.
- [ ] impl.md step 6.3 / DH-D-EXEC-2: `StopTree` stops and awaits a whole process tree on every OS (process group on Unix; Job Object on Windows; Linux control group when writable, otherwise process-group fallback that is reported, register U-K1-1).
- [ ] Add the re-exec helper `subprocesstest` (test binary re-executes itself as child/grandchild via a helper env var) used by TC-06c and TC-07.

### Dependencies
- phase-session-core-contracts/stage-model-runtime-agent-contract-and-registries

### Test Scenarios
- [ ] Scenario: Error kinds map to one table -- Given in-memory errors wrapped three levels deep, When `herr.Kind` is read, Then it returns the innermost `<domain>/<reason>` and formatting never includes a wrapped secret value (TC-06a) [proof: in-process; deps: none]
- [ ] Scenario: Process tree stopped and awaited -- Given the test binary re-exec'd as a child that spawns a grandchild ignoring SIGTERM, When `StopTree` runs with a 2 s grace, Then both processes are gone and `StopTree` returns only after both exited (TC-06c) [proof: service:os-process; deps: os-process -- the test binary re-executes itself; no external program]
- [ ] Scenario: Child environment holds no secret -- Given a parent environment containing `DSH_DEEPSEEK_API_KEY`, When a child is started through `subprocess` with a given env list, Then the child (re-exec'd test binary) reports the variable as absent (TC-06c, SP-11) [proof: service:os-process; deps: os-process via test-binary re-exec]

## Stage 6.2: Launch Facts Home Layout and Secrets

### Implementation Steps
- [ ] impl.md step 6.4 / DH-D-BOOT-2: launch facts (argv, cwd, OS, user, build identity), the launch environment and the expression scope fed to `boot/expr`, in `src/harness/internal/boot/launch`.
- [ ] impl.md step 6.5 / DH-D-STORAGE-2: home layout (`dsh.db`, `run/`, `spill/`, `blobs/`, `config/`, `reports/`) created private (0700 / owner-only ACL) and checked at start; a world-readable home fails boot (architecture.md sec. 4.1).
- [ ] impl.md step 6.6 / DH-D-HUMAN-7: secret-reference store in `internal/human` -- configuration names a secret (`secret://deepseek/api-key`); the value is resolved per use from the environment or a private file under the home and never enters config, log or error text (SP-11). OS keychain stays for DH-S-35.

### Dependencies
- phase-boot-launch-egress-secrets-and-test-kit/stage-leaf-helpers-and-process-trees

### Test Scenarios
- [ ] Scenario: Home created private -- Given an empty `t.TempDir()` as home, When the layout check runs, Then every directory exists private to its owner; then the test loosens `spill/` per OS -- on Unix `os.Chmod(spill, 0o777)`, on Windows an added DACL entry granting `Everyone` (S-1-1-0) read via `golang.org/x/sys/windows` `SetNamedSecurityInfo` -- and the next check fails naming `spill/`. The check itself is per OS: Unix requires mode bits `0700` with no group/other bits; Windows requires a protected DACL whose only ACEs are for the owner SID, `SYSTEM` and `Administrators` (TC-06b) [proof: service:filesystem; deps: filesystem via `t.TempDir()`]
- [ ] Scenario: Secret resolved per use and redacted -- Given a fake secret source returning `sk-test-123`, When the reference is resolved and the resulting error and log lines of a failed use are captured, Then the value is used once and `sk-test-123` appears in no captured string (TC-06a) [proof: in-process; deps: none]
- [ ] Scenario: Launch facts feed the expression scope -- Given in-memory launch facts with cwd `/w` and OS `linux`, When `${{ launch.os }}` is evaluated, Then it yields `linux` (TC-06a) [proof: in-process; deps: none]

## Stage 6.3: Profiles Command Tree and Output Contract

### Implementation Steps
- [ ] impl.md step 6.7 / DH-D-BOOT-3: profiles, embedded bundles (`embed.FS`) and the layer stack (bundle < profile < user < project) built on `loader` layers, in `internal/boot/profile`.
- [ ] impl.md step 6.8 / DH-D-CLI-1: one command tree in `src/harness/internal/cli` (grammar, help, usage errors) and a generated command reference checked as a golden; `cmd/dsh/main.go` only calls it.
- [ ] impl.md step 6.9 / DH-D-CLI-3: one print/exit contract -- every command returns a result mapped to stdout/stderr and a fixed exit code table (0 ok, 2 usage, 124 shutdown overrun, ...; tech-spec sec. 5.5).

### Dependencies
- phase-boot-launch-egress-secrets-and-test-kit/stage-launch-facts-home-layout-and-secrets

### Test Scenarios
- [ ] Scenario: Layer stack precedence -- Given in-memory bundle `{model: a}`, profile `{model: b}` and user `{}`, When the stack composes, Then `model` is `b` and the ownership index names the profile layer (TC-06a) [proof: in-process; deps: none]
- [ ] Scenario: Usage error exits 2 -- Given the command tree, When `dsh --no-such-flag` is dispatched in process with captured streams, Then stderr holds one usage line plus help hint and the mapped exit code is 2 (TC-06a) [proof: in-process; deps: none]
- [ ] Scenario: Generated CLI reference is current -- Given the command tree, When the reference is generated, Then it equals the committed golden `internal/cli/testdata/reference.golden` [proof: golden; deps: committed golden `internal/cli/testdata/reference.golden`]

## Stage 6.4: Fatal Path Boot Sequence and Shutdown

### Implementation Steps
- [ ] impl.md step 6.10 / DH-D-HOST-7: implement `src/harness/internal/host` fatal path -- a host that cannot continue prints one message and writes one report file under `reports/`.
- [ ] impl.md step 6.11 / DH-D-BOOT-5: the boot sequence (launch facts, home check, layers, compose, audit, readiness), the boot audit listing every outbound permit, and bounded shutdown (20 s budget, process trees stopped via `StopTree`, exit 124 on overrun).

### Dependencies
- phase-boot-launch-egress-secrets-and-test-kit/stage-profiles-command-tree-and-output-contract

### Test Scenarios
- [ ] Scenario: Broken required entry fails boot once -- Given a temp home and a layer with a broken required entry, When boot runs, Then exactly one fatal message is printed and exactly one file exists in `<home>/reports/` naming the entry (TC-06b) [proof: service:filesystem; deps: filesystem via `t.TempDir()` home]
- [ ] Scenario: Boot order and audit on fakes -- Given in-memory fakes for every boot step, When boot runs, Then steps are called in the documented order and the audit lists zero permits when none are configured (TC-06a) [proof: in-process; deps: none]
- [ ] Scenario: Shutdown overrun exits 124 -- Given a composed tree whose child process (re-exec'd test binary) ignores termination and a shutdown budget shortened by the test kit's clock, When shutdown runs, Then the tree is killed, the host exits 124 and no child remains (TC-06c) [proof: service:os-process; deps: os-process via test-binary re-exec]

## Stage 6.5: Preview Validated Reload and Settings Edits

### Implementation Steps
- [ ] impl.md step 6.12 / DH-D-BOOT-6 (P1, pulled forward; held open H-21): preview, explain and check the composed tree without starting it, as a Go API only; the `dsh config` commands wait for the DH-S-16 admin tree and H-21 is listed open in the evidence record.
- [ ] impl.md step 6.13 / DH-D-BOOT-7 (P1, pulled forward): validated reload and one edit path safe across processes -- a candidate is validated through DH-D-CORDIS-5 candidate config before it becomes effective; writes go through the DH-D-LOADER-13 guard.
- [ ] impl.md step 6.14 / DH-D-STORAGE-8 (P1, pulled forward): live settings edited through one path; a bad edit changes nothing (owner path only, NG-9).
- [ ] Author `DH-E-LOADER-1` (runs in DH-S-17, step 17.7) and `DH-E-BOOT-2` (guarded, runs in step 21.13) as `//go:build scenario` files; not executed here.

### Dependencies
- phase-boot-launch-egress-secrets-and-test-kit/stage-fatal-path-boot-sequence-and-shutdown

### Test Scenarios
- [ ] Scenario: Bad layer edit leaves running tree unchanged -- Given a booted tree from layer files in a temp home, When the user layer is rewritten with an invalid value and reload fires, Then reload is rejected with a positioned diagnostic and every component's effective config is unchanged (TC-06b) [proof: service:filesystem; deps: filesystem via `t.TempDir()` home]
- [ ] Scenario: Preview without start -- Given layers in memory, When preview runs, Then it returns the composed entry list with owning layers and no component's construct function was called (TC-06a) [proof: in-process; deps: none]
- [ ] Scenario: Concurrent settings edits do not lose a write -- Given two edit-path clients on one temp home, When both edit different keys from the same read, Then one succeeds, the other gets a stale-source error, and after retry both values are on disk (TC-06b) [proof: service:filesystem; deps: filesystem via `t.TempDir()` home]

## Stage 6.6: Egress Policy and Telemetry Off Switch

### Implementation Steps
- [ ] impl.md step 6.15 / DH-D-HOST-4: nothing leaves the machine by default; each data kind has a visible setting, the boot audit lists it, and one off-switch disables all outbound kinds (NG-7).
- [ ] impl.md step 6.16 / DH-D-UTIL-2: implement `src/harness/internal/util/egress` -- every outbound connection is made through one client that requires a permit per data kind; without one it fails with `egress/not-permitted` before dialing.

### Dependencies
- phase-boot-launch-egress-secrets-and-test-kit/stage-fatal-path-boot-sequence-and-shutdown

### Test Scenarios
- [ ] Scenario: No permit, no dial -- Given an `httptest` server on `127.0.0.1:0` counting connections, When the egress client is asked to POST without a permit, Then it returns `egress/not-permitted` and the server's connection count stays 0; with a `model-request` permit the request arrives (TC-06d) [proof: service:loopback-http; deps: loopback-http -- `net/http/httptest` on 127.0.0.1:0 started and closed in the test]
- [ ] Scenario: Off-switch overrides permits -- Given permits for every kind and the off-switch on, When permit decisions are evaluated in memory, Then every kind is denied and the boot audit lists each kind as `off` (TC-06a) [proof: in-process; deps: none]

## Stage 6.7: Harness Test Kit and Circle Closure

### Implementation Steps
- [x] impl.md step 6.17 / DH-D-UTIL-6: implement `src/harness/internal/testkit` -- compose real components against scripted boundaries: fake model (scripted SSE turns), fake transport, fake answerer, virtual clock, temp homes, and a scenario launcher that builds and starts a program through `internal/world/subprocess` with a temp home and returns an `src/sdk/jsonrpc` client on its stdio (used by `//go:build scenario` files; it compiles now and first runs at DH-S-17); register every release with `t.Cleanup` (QG-5).
- [x] Replace the hand-written fakes used by steps 6.1-6.16 with the test kit and re-run the tests that waited on later steps of circle C-1.
- [x] Author DH-S-6's scenarios (impl.md DH-S-6 "Scenarios: authored here"; tech-spec sec. 2.2, EV-4) as `//go:build scenario` files under `src/harness/internal/testkit/scenarios/`, each reviewed in the PR and added to its executing stage's entry in `test/harness/acceptance-index.yaml`: `DH-E-EXEC-1` (Linux: a whole process tree is killed) and `DH-E-HOST-4` (a host that dies leaves a readable report on each OS) under `DH-S-18`; `DH-E-BOOT-1` (requests the UI's first page) under `DH-S-22` (step 22.3); `DH-E-UTIL-1` (egress scenario that fetches a page and starts an MCP server) under `DH-S-39`. `DH-E-LOADER-1` and `DH-E-BOOT-2` are authored in Stage 6.5. None is executed in this story.
- [x] Close DH-S-6 at ONE commit: write `docs/stories/harness-DSH-M1/evidence/DH-S-6.md` with all seventeen deliverables built at that commit, the list of circle-waiting tests shown passing, the manual checks of impl.md DH-S-6 (broken entry, child ignoring termination on each OS, bad layer edit), the scenario line (`DH-E-EXEC-1`, `DH-E-HOST-4`, `DH-E-BOOT-1`, `DH-E-UTIL-1`, `DH-E-LOADER-1`, `DH-E-BOOT-2` authored, reviewed, indexed; none executed), H-21 open, and the Linux ownership fallback reported (TC-X2); tick impl.md steps 6.1-6.17 as built. _Closed: the manual checks are recorded on Windows, Linux and macOS from `harness-manual-checks` run 44860891149 at `42d0d7d` (see `evidence/DH-S-6.md`)._

### Dependencies
- phase-boot-launch-egress-secrets-and-test-kit/stage-preview-validated-reload-and-settings-edits
- phase-boot-launch-egress-secrets-and-test-kit/stage-egress-policy-and-telemetry-off-switch

### Test Scenarios
- [ ] Scenario: Virtual clock drives deadlines without sleeping -- Given a test-kit tree with a component waiting 30 s on the virtual clock, When the clock is advanced 30 s, Then the wait completes in under 100 ms of wall time (TC-06a) [proof: in-process; deps: none]
- [ ] Scenario: Fake model scripts a turn -- Given the fake model scripted with one text reply, When the runtime makes one attempt through the fake transport, Then one `assistant/message` is settled with the scripted text (TC-06a) [proof: in-process; deps: none]
- [ ] Scenario: Temp home torn down by cleanup -- Given a test that creates a test-kit temp home and a child process, When the test ends, Then the home directory no longer exists and no child process remains (TC-06b/TC-06c) [proof: service:filesystem; deps: filesystem via `t.TempDir()`; os-process via test-binary re-exec]
- [ ] Scenario: DH-S-6 scenarios authored and indexed -- Given the four new scenario files and `test/harness/acceptance-index.yaml`, When the gate's index check and `go -C src/harness vet -tags scenario ./...` run, Then `DH-E-EXEC-1` and `DH-E-HOST-4` sit under `DH-S-18`, `DH-E-BOOT-1` under `DH-S-22`, `DH-E-UTIL-1` under `DH-S-39`, each has exactly one file, vet exits 0, and the default gate run executes none of them [proof: service:go-toolchain; deps: go-toolchain package loader over the checkout; index read from the checkout by the gate]

### Cross-Stage Dependencies
- phase-boot-launch-egress-secrets-and-test-kit/stage-leaf-helpers-and-process-trees
- phase-boot-launch-egress-secrets-and-test-kit/stage-launch-facts-home-layout-and-secrets
- phase-boot-launch-egress-secrets-and-test-kit/stage-profiles-command-tree-and-output-contract

# Phase 7: SQLite Store Leases and Recovery

## Dependencies
- phase-session-core-contracts
- phase-boot-launch-egress-secrets-and-test-kit

## Stage 7.1: SQLite Driver Measurement and Atomic Appends

### Implementation Steps
- [ ] Fact F-2 first (impl.md DH-S-7 entry): add `modernc.org/sqlite` v1.62.0 pinned with `modernc.org/libc` v1.80.1 to `src/harness/go.mod` with a reason comment (RM-10, NG-4), and a `TestBudget*` measurement of open, WAL append and flush latency recorded in `docs/stories/harness-DSH-M1/evidence/DH-S-7.md` BEFORE anything else is built on the driver.
- [ ] impl.md step 7.1 / DH-D-STORE-2 (part 1): implement `src/harness/internal/session/store/sqlite` -- one database per home (`<home>/dsh.db`), WAL mode, the schema of architecture.md sec. 4.3 created by numbered migrations, atomic multi-event appends in one transaction.
- [ ] impl.md step 7.1 / DH-D-STORE-2 (part 2): the flush barrier (`Flush(ctx)` returns after the WAL commit is durable), paged reads and listing; run `storetest.Run` from stage 5.1 against the SQLite factory (`DH-T-STORE-1`..`-5`, `DH-T-STORE-18` inside its budget).

### Dependencies
- phase-session-core-contracts/stage-event-vocabulary-and-store-contract
- phase-boot-launch-egress-secrets-and-test-kit/stage-harness-test-kit-and-circle-closure

### Test Scenarios
- [ ] Scenario: Conformance suite passes on SQLite -- Given `storetest.Run` with a factory opening `dsh.db` in a test-kit temp home, When the suite runs, Then every case that passes on `memstore` passes on SQLite (`DH-T-STORE-1`, TC-07) [proof: service:sqlite; deps: sqlite -- embedded `modernc.org/sqlite` compiled into the test binary, DB file under `t.TempDir()`; no DSN]
- [ ] Scenario: Multi-event append is atomic -- Given an append of three events where the third violates a constraint, When the append runs, Then a fresh read of the database holds none of the three rows (TC-07) [proof: service:sqlite; deps: sqlite embedded; filesystem via `t.TempDir()`]
- [ ] Scenario: Driver budget measured -- Given the pinned driver, When `TestBudgetStoreAppend` runs 1,000 single-event appends with flush, Then the p95 is under the budget compiled into the test and the measured figure is printed for the evidence record (`DH-T-STORE-18`, fact F-2) [proof: service:sqlite; deps: sqlite embedded; filesystem via `t.TempDir()`]

## Stage 7.2: Session Leases and Crash Recovery

### Implementation Steps
- [ ] impl.md step 7.2 / DH-D-STORE-3: one writer per session across processes -- lease row with epoch, per-instance liveness lock file under `run/`, epoch checked on every append; nothing expires by time; takeover only by `Force` or when the holder's liveness lock is acquired inside the write transaction (SP-9; `DH-T-STORE-6`, `-7`).
- [ ] impl.md step 7.3 / DH-D-STORE-4 (part 1): durability checkpoints -- flush before a provider attempt, before a tool body and at step end; a failed flush means the work does not run (SP-8).
- [ ] impl.md step 7.3 / DH-D-STORE-4 (part 2): honest crash recovery on write-open -- open brackets are closed with `interrupted` closers appended by the session core, pending tool calls answered as interrupted, and bounded shutdown flushes the last events (`DH-T-STORE-5`, `-8`, `-9`).
- [ ] Add crash-injection hooks (test-only build of the re-exec child that exits between append and flush) used by the tests below.
- [ ] Author `DH-E-STORE-1` and `DH-E-STORE-2` (guarded; run at step 16.8 on scratch homes) as `//go:build scenario` files; not executed in this story.

### Dependencies
- phase-sqlite-store-leases-and-recovery/stage-sqlite-driver-measurement-and-atomic-appends

### Test Scenarios
- [ ] Scenario: Second writer refused across processes -- Given process A (re-exec'd test binary) holding the lease on session S in a temp home, When process B opens S for writing, Then B is refused with `session/owned` naming A's process and S's log contains only A's events (`DH-T-STORE-6`, TC-07; the refusal code is the store contract's, [session-store.md §6.2](../../harness/areas/session-store.md) lease rule 3 -- an earlier draft named it `store/lease-held`, a code no contract defines) [proof: service:sqlite; deps: sqlite embedded; os-process via test-binary re-exec; filesystem via shared `t.TempDir()` home]
- [ ] Scenario: Dead holder taken over safely -- Given process A holding the lease is killed, When process B opens S, Then B acquires A's liveness lock inside the write transaction, bumps the epoch, and a stale append carrying A's epoch is rejected (`DH-T-STORE-7`, TC-07) [proof: service:sqlite; deps: sqlite embedded; os-process via re-exec; filesystem via `t.TempDir()`]
- [ ] Scenario: Crash between append and flush recovers honestly -- Given a child that appends `turn/start`, `step/start` and a tool call then exits before flush, When the store is reopened for write, Then the persisted rows contain balanced brackets closed as `interrupted` and the tool call has an interrupted result (`DH-T-STORE-8`, TC-07) [proof: service:sqlite; deps: sqlite embedded; os-process via re-exec; filesystem via `t.TempDir()`]
- [ ] Scenario: Failed flush blocks the tool body -- Given a store whose flush returns an injected error, When a checkpoint precedes a tool body, Then the body's call counter stays 0 and the step ends with a checkpoint failure (SP-8, `DH-T-STORE-9`) [proof: service:sqlite; deps: sqlite embedded with fault-injecting wrapper; filesystem via `t.TempDir()`]

## Stage 7.3: Export Import Follow and Store Repair

### Implementation Steps
- [x] impl.md step 7.4 / DH-D-STORE-7: export a session to one readable file (JSON lines with a header) and import it into another home; import refuses a session id already present (`DH-T-STORE-13`).
- [x] impl.md step 7.5 / DH-D-STORE-8 (P1, pulled forward): follow a session another process writes (poll on WAL change with bounded latency) and once-per-home duties with one runner (`DH-T-STORE-14`).
- [x] impl.md step 7.6 / DH-D-STORE-6 (P1, pulled forward): delete sessions, retire old ones, check and repair the store; an interrupted deletion finishes on next open (`DH-T-STORE-11`, `-12`).
- [x] Author `DH-E-STORE-6` (guarded, step 16.8) and `DH-E-STORE-5` (step 35.7) as scenario files; record that the store is used on scratch homes only until 16.8 passes.
- [x] Close DH-S-7: write `docs/stories/harness-DSH-M1/evidence/DH-S-7.md` (conformance on SQLite, driver measurement, crash-injection results, manual checks of impl.md DH-S-7 on a scratch home) and tick impl.md steps 7.1-7.6 as built.

### Dependencies
- phase-sqlite-store-leases-and-recovery/stage-session-leases-and-crash-recovery

### Test Scenarios
- [x] Scenario: Export then import round-trips -- Given a session with 50 events in home H1, When it is exported and imported into empty home H2, Then H2's event rows are equal to H1's (type, version, payload, sequence) and the export file equals the committed golden for a fixed fixture (`DH-T-STORE-13`, TC-07) [proof: golden; deps: sqlite embedded; filesystem via two `t.TempDir()` homes; committed golden `store/sqlite/testdata/export.golden`]
- [x] Scenario: Follower sees another process's commits -- Given process A (re-exec'd) appending 10 events, When process B follows the session, Then B observes all 10 in order within the bounded latency (`DH-T-STORE-14`, TC-07) [proof: service:sqlite; deps: sqlite embedded; os-process via re-exec; filesystem via `t.TempDir()`]
- [x] Scenario: Interrupted deletion finishes -- Given a deletion killed after removing half the rows, When the store is reopened, Then the deletion completes, no row or spill file of that session remains, and `check` reports clean (`DH-T-STORE-11`) [proof: service:sqlite; deps: sqlite embedded; os-process via re-exec; filesystem via `t.TempDir()`]

# Phase 8: Machine State Views and Spill

## Dependencies
- phase-sqlite-store-leases-and-recovery

## Stage 8.1: Typed Machine State and Stored Folds

### Implementation Steps
- [ ] impl.md step 8.1 / DH-D-STORAGE-1: implement `src/harness/internal/storage` typed machine-state tables (workspace registry, settings) in the same `dsh.db`, with typed accessors and migrations.
- [ ] impl.md step 8.2 / DH-D-VIEW-2: stored fold state is never ahead of the log; each stored fold records the sequence it reflects and its fold version; a changed fold version rebuilds the stored state on open.

> Operator decision `step-8-1-settings-meaning`: "settings" in step 8.1 means machine-written settings, which live in typed `kv` namespaces in `dsh.db` (`storage/kv`). User settings stay with the one writable config layer (`storage/settings`, [architecture.md](architecture.md), [storage.md](../../harness/areas/storage.md)), and there is no dedicated settings table. Pruning checkpoint rows of superseded fold versions is left to a later stage.

### Dependencies
- phase-sqlite-store-leases-and-recovery/stage-export-import-follow-and-store-repair

### Test Scenarios
- [ ] Scenario: Machine state survives restart -- Given a workspace registered in a temp home, When the store is closed and reopened, Then the workspace row is read back with identical fields (TC-08) [proof: service:sqlite; deps: sqlite embedded; filesystem via `t.TempDir()`]
- [ ] Scenario: Changed fold version rebuilds -- Given a stored fold at version 1 over 20 events, When the fold is re-registered at version 2 and the store opens, Then the stored state is rebuilt, records version 2 and its sequence equals the log head (TC-08) [proof: service:sqlite; deps: sqlite embedded; filesystem via `t.TempDir()`]

## Stage 8.2: Builtin Views Projects and Session Listing

### Implementation Steps
- [ ] impl.md step 8.3 / DH-D-VIEW-3 (P1, pulled forward): built-in views in `internal/session/views` -- list summary, turn index, statistics, event index.
- [ ] impl.md step 8.4 / DH-D-STORAGE-7 (P1, pulled forward): projects group sessions; a hidden session never keeps working (hiding stops its agents).
- [ ] impl.md step 8.5 / DH-D-VIEW-5: list, observe, trace and follow sessions across forks.
- [ ] Author `DH-E-STORAGE-1` (runs DH-S-19) and `DH-E-STORAGE-2` (runs DH-S-23) as scenario files.
- [ ] Close DH-S-8 (moved here from Stage 8.3 by operator decision `dsh-s8-closure-waits-stage-8-2`): add the refold-equals-stored evidence for the four built-in views to `docs/stories/harness-DSH-M1/evidence/DH-S-8.md`, tick impl.md steps 8.3-8.5 as built, and raise `built-through` in `test/harness/acceptance-index.yaml` to DH-S-8.

### Dependencies
- phase-machine-state-views-and-spill/stage-typed-machine-state-and-stored-folds

### Test Scenarios
- [x] Scenario: Refold equals stored for every built-in view -- Given a fixed 200-event session in a temp home, When each built-in view is refolded from the log and compared with its stored state, Then all four are equal (DH-S-8 exit evidence, TC-08) [proof: service:sqlite; deps: sqlite embedded; filesystem via `t.TempDir()`]
- [x] Scenario: Listing groups by project -- Given three sessions in two projects, When sessions are listed by project, Then the result equals the committed golden listing (TC-08) [proof: golden; deps: sqlite embedded; filesystem via `t.TempDir()`; committed golden `session/views/testdata/list_by_project.golden`]
- [x] Scenario: Hidden session stops working -- Given a session with an agent registered through the agent contract fake, When the session is hidden, Then the agent receives a stop with cause `hidden` (TC-08 logic) [proof: in-process; deps: none -- memstore and fake agent]

## Stage 8.3: Spill Store and Attachments

### Implementation Steps
- [x] impl.md step 8.6 / DH-D-STORAGE-5: spill store under `<home>/spill/` -- the full text of a large result saved privately, addressed by a fixed-length id reserved before writing (owner-directory hash, 96 random bits, a safe name, as storage.md DH-D-STORAGE-5 specifies), readable back; only a bounded head enters the log.
- [x] impl.md step 8.7 / DH-D-STORAGE-3 (P1, pulled forward): attachments under `<home>/blobs/` stored durably and read back verified by digest.
- [x] Author `DH-E-STORAGE-3` (runs DH-S-39). Write `docs/stories/harness-DSH-M1/evidence/DH-S-8.md` with this stage's evidence (manual checks: fold version change and the 5 MB result) and tick impl.md steps 8.1, 8.2, 8.6 and 8.7 as built. Closing DH-S-8 (refold-equals-stored, impl.md 8.3-8.5) is Stage 8.2's step.
- [x] Built with DH-D-STORAGE-3 (operator decision `defer-gc-lock-blob-refs`: build now): savers hold `<home>/blobs/gc.lock` shared and the collector's `LockCollection` takes it exclusively; cited references are `blob_refs` rows (layout 3 of the session store) written in the append, import and fork transactions and removed with their session; `blob.Store.ReferencedBy` reads them.

> Status of Stage 8.3:
> - Spill addressing follows storage.md DH-D-STORAGE-5 (operator decision `spill-addressing`: storage.md's random id governs; this plan's wording was amended from "addressed by digest"). The DH-S-39 scenario DH-E-STORAGE-3 is authored and compile-checked with the `scenario` build tag but has not been run: it needs the real sandbox and the spill policy, which later stages build.
> - DH-S-8 closure belongs to Stage 8.2 (operator decision `dsh-s8-closure-waits-stage-8-2`). `built-through` stays at DH-S-7 until that step lands.

### Dependencies
- phase-machine-state-views-and-spill/stage-typed-machine-state-and-stored-folds

### Test Scenarios
- [x] Scenario: Large result spilled and recoverable -- Given a 5 MB tool result, When it is stored through the spill store, Then the log holds a head no larger than the configured bound plus a spill reference, and reading the reference returns all 5 MB byte-identical (TC-08) [proof: service:filesystem; deps: filesystem via `t.TempDir()` home; sqlite embedded for the log row]
- [x] Scenario: Corrupted attachment detected -- Given an attachment stored in `blobs/`, When one byte of the file is flipped on disk and it is read back, Then the read fails with a digest-mismatch error (TC-08) [proof: service:filesystem; deps: filesystem via `t.TempDir()` home]
- [x] Scenario: Spill files are private -- Given a spilled result, When the file's permissions are inspected, Then it is private to its owner -- on Unix mode `0600` inside a `0700` `spill/`; on Windows a protected DACL whose only ACEs are for the owner SID, `SYSTEM` and `Administrators`, read back with `GetNamedSecurityInfo` (TC-08) [proof: service:filesystem; deps: filesystem via `t.TempDir()` home]

# Phase 9: DeepSeek Provider Retry and Usage

## Dependencies
- phase-boot-launch-egress-secrets-and-test-kit
- phase-machine-state-views-and-spill

## Stage 9.1: DeepSeek Messages Provider

### Implementation Steps
- [ ] impl.md step 9.1 / DH-D-LLM-2 (part 1): shared HTTP transport in `internal/llm` built on the egress client (permit kind `model-request`) and the Anthropic Messages wire driver: request encoding from `session.Request`, SSE decoding marked `//dsh:untrusted-parser` with `FuzzSSE`.
- [ ] impl.md step 9.1 / DH-D-LLM-2 (part 2): the `deepseek-official` route (`api.deepseek.com`, architecture.md sec. 3.2) with key authentication through the secret-reference store; vendor error mapping into `vocab` failure classes (unknown business codes stay `unknown`, register item `protocol.ts:245`).
- [ ] Record SSE fixtures (success, overloaded with retry-after, bad key, mid-stream drop) under `internal/llm/testdata/sse/` -- written by hand from the vendor's published format, containing no real key (`DH-T-LLM-5`, `-11`, `-16`).

### Dependencies
- phase-boot-launch-egress-secrets-and-test-kit/stage-egress-policy-and-telemetry-off-switch
- phase-machine-state-views-and-spill/stage-spill-store-and-attachments

### Test Scenarios
- [ ] Scenario: Request at the transport equals rebuilt request -- Given the fake transport of the test kit capturing bytes, When one attempt is made for a session, Then the captured body equals `surface.Rebuild` at the settlement's `requestAsOf`, and the settlement's `requestDigest` matches (DH-S-9 exit evidence, TC-09a) [proof: in-process; deps: none -- fake transport]
- [ ] Scenario: Decoded fixtures settle once -- Given each SSE fixture, When fed through the driver in memory, Then each yields exactly one settlement of the expected kind and the decoded message equals the committed golden (TC-09a) [proof: golden; deps: committed fixtures and goldens under `internal/llm/testdata/sse/`]
- [ ] Scenario: Provider over loopback HTTP -- Given the success fixture served by `httptest` on 127.0.0.1:0 and a `model-request` permit for that host, When the provider streams a reply, Then one `assistant/message` is settled and the server saw the `x-api-key` header from the secret store (TC-09b) [proof: service:loopback-http; deps: loopback-http -- `httptest` started and closed in the test]

## Stage 9.2: Bounded Retry and Token Usage

### Implementation Steps
- [x] impl.md step 9.2 / DH-D-LLM-3: bounded retry policy outside the adapter, driven by `AttemptFailed` decisions on the clock; retry-after honoured up to a cap; auth failures never retried and never fall back to another route (SP-2); buffered one-shot calls for auxiliary requests (`DH-T-LLM-7`, `-8`).
- [x] impl.md step 9.3 / DH-D-LLM-6 (P1, pulled forward): usage fold in `internal/llm` -- token usage and context occupancy read from settlements, with a token estimate used by the DH-S-10 result budget (`DH-T-LLM-15`).
- [ ] Manual check (impl.md DH-S-9): one streamed request to the real DeepSeek API with a good key and one with a bad key, key supplied at run time; record one attempt event each and no retry on the bad key (`manual`; never a gate criterion, EV-6). Record the result in `docs/stories/harness-DSH-M1/evidence/DH-S-9.md`.
- [x] Author DH-S-9's scenarios BEFORE DH-S-9 closes (impl.md DH-S-9 "Scenarios: authored here, run in DH-S-17"; tech-spec sec. 2.2, EV-4): write `DH-E-LLM-1` (a turn with reasoning and a tool call on the vendor's own protocol), `DH-E-LLM-3` (a failure is a fact: no retry and no fallback on a bad key) and `DH-E-VIEW-1` (a real model titles a session; a failing provider leaves the fallback) as `//go:build scenario` files under `src/harness/internal/testkit/scenarios/`. Authorship is separated from execution: each file is written against the PRODUCT boundary its scenario specification names, not against internal packages -- it starts the built `dsh` program through the test kit's scenario launcher (Stage 6.7, on `internal/world/subprocess`), speaks to it only through the client-facing types of `src/sdk/protocol` and `src/sdk/jsonrpc` (Phase 4), and asserts on what that boundary returns (the bracketed log export, the settlement records, and for `DH-E-VIEW-1` the session's title and its fallback). No scenario re-implements product behaviour from `llm.Runtime`, views or the store, and this stage implements no title behaviour: titles (DH-D-VIEW-4) and the headless launcher stay deferred to DH-S-16 (architecture.md sec. 1.3; tech-spec sec. 3), the agent loop to Phase 11, and the scenarios execute only at DH-S-17, after both exist. Get them reviewed in the PR and list them under `DH-S-17` in `test/harness/acceptance-index.yaml` with authoring stage DH-S-9 and a `requires: [DH-S-11, DH-S-16]` field the DH-S-17 preflight checks. Not executed.
- [ ] Close DH-S-9: complete `docs/stories/harness-DSH-M1/evidence/DH-S-9.md` (fixture results, the manual check above, and the scenario line: `DH-E-LLM-1`, `DH-E-LLM-3`, `DH-E-VIEW-1` authored, reviewed and indexed to DH-S-17; none executed) and tick impl.md steps 9.1-9.3 as built.

### Dependencies
- phase-deepseek-provider-retry-and-usage/stage-deepseek-messages-provider

### Test Scenarios
- [x] Scenario: Overloaded retried within bound -- Given the overloaded-with-retry-after fixture twice then success, on the virtual clock, When the call runs, Then three `assistant/attempt`/`assistant/message` settlements are logged, waits equal the retry-after values, and no fourth attempt occurs (`DH-T-LLM-7`, TC-09a) [proof: in-process; deps: none -- fake transport + virtual clock]
- [x] Scenario: Bad key never retried -- Given the bad-key fixture, When the call runs, Then exactly one settlement with failure class `auth` is logged and no other route is tried (SP-2, TC-09a) [proof: in-process; deps: none]
- [x] Scenario: DH-S-9 scenarios authored before closure -- Given the three DH-S-9 scenario files and `test/harness/acceptance-index.yaml` at the DH-S-9 closing commit, When the gate's index check and `go -C src/harness vet -tags scenario ./...` run on that commit, Then `DH-E-LLM-1`, `DH-E-LLM-3` and `DH-E-VIEW-1` each have exactly one file and one `DH-S-17` index entry with authoring stage DH-S-9 and `requires: [DH-S-11, DH-S-16]`, the files' imports are limited to `src/sdk/...`, the test kit's scenario launcher and the standard library (no `internal/llm`, `internal/agent`, views or store import, so no title or loop substitute exists in them), vet exits 0, and the default gate run executes none of them; the `go vet` result therefore depends only on code present at DH-S-9, while the title and loop behaviour they assert is first exercised at DH-S-17 [proof: service:go-toolchain; deps: go-toolchain package loader over the checkout; index read from the checkout by the gate]
- [x] Scenario: Usage folded from settlements -- Given three settlements with known token counts, When the usage fold runs, Then totals and context occupancy equal the expected values (`DH-T-LLM-15`) [proof: in-process; deps: none]

# Phase 10: Tool Pipeline Approvals and Human Commands

## Dependencies
- phase-boot-launch-egress-secrets-and-test-kit
- phase-sqlite-store-leases-and-recovery
- phase-machine-state-views-and-spill
- phase-deepseek-provider-retry-and-usage

## Stage 10.1: Tool Registry and Prompt Assembly

### Implementation Steps
- [x] impl.md step 10.1 / DH-D-TOOLS-1: implement `src/harness/internal/tools` declaration and registry -- a tool is declared once (name, schema via `internal/schema`, resource keys, body) and registered in a cordis scope; duplicates in a scope are refused (`DH-T-TOOLS-1`, `-2`, `-18`).
- [x] impl.md step 10.2 / DH-D-TOOLS-5: deterministic assembly of each step's system prompt and tool list (stable ordering, canonical schema encoding) (`DH-T-TOOLS-14`, `-15`).

### Dependencies
- phase-deepseek-provider-retry-and-usage/stage-bounded-retry-and-token-usage

### Test Scenarios
- [ ] Scenario: Duplicate tool name refused in a scope -- Given a scope with tool `echo`, When a second `echo` is registered in the same scope, Then registration fails and the first remains; registering it in a child scope shadows only that subtree (`DH-T-TOOLS-2`, TC-10a) [proof: in-process; deps: none]
- [ ] Scenario: Prompt assembly is deterministic -- Given five tools registered in random order across 100 runs, When the step prompt and tool list are assembled, Then every run produces bytes equal to the committed golden (`DH-T-TOOLS-14`) [proof: golden; deps: committed golden `internal/tools/testdata/prompt.golden`]

## Stage 10.2: Approvals and Guarded Pipeline

### Implementation Steps
- [x] impl.md step 10.3 / DH-D-HUMAN-2: approvals in `internal/human` return one closed outcome (`approved`, `denied`, `timeout`, `no-answerer`) and append an attributed audit event; no answerer means denial (SP-7).
- [x] impl.md step 10.4 / DH-D-TOOLS-2: the guarded pipeline in fixed order -- (1) resolve the call by name in the step's fixed `ToolSet` (an unknown or unadmitted tool is refused with `tools/not-started` and nothing later runs), (2) bound, decode and validate arguments against schema, (3) policy listeners, (4) deny-only guards, (5) approval, (6) checkpoint flush, (7) body, (8) shape the result and append `tool/result`; logged arguments are never rewritten; denial is enforced in the executor, not by hiding the tool (architecture.md sec. 5.5; SP-6, SP-7; `DH-T-TOOLS-3`..`-7`).
- [x] Author `DH-E-TOOLS-1` (runs DH-S-17, 17.5) as a scenario file.

### Dependencies
- phase-tool-pipeline-approvals-and-human-commands/stage-tool-registry-and-prompt-assembly

### Test Scenarios
- [x] Scenario: Schema-breaking arguments never reach the body -- Given tool `add(a:int,b:int)` and arguments `{a:"x"}`, When the call goes through the pipeline, Then the body counter is 0 and a validation-error result is returned to the model (`DH-T-TOOLS-3`, TC-10a) [proof: in-process; deps: none]
- [x] Scenario: Nobody to ask means no, through the executor -- Given a tool requiring approval, registered and visible in the tool list, and no answerer, When the model calls it, Then the approval outcome is `no-answerer`, an audit event is logged, the body counter is 0, and the tool was still present in the prompt (DH-S-10 exit evidence, SP-7) [proof: in-process; deps: none]
- [x] Scenario: Pipeline stage order fixed -- Given recording stubs at every stage, When one approved call runs, Then the recorded order is resolve, validate, policy, guards, approval, flush, body, shape (`DH-T-TOOLS-4`) [proof: in-process; deps: none]
- [x] Scenario: Unknown or unadmitted tool refused before execution -- Given a step whose fixed `ToolSet` admits only `echo`, and recording stubs at every later stage, When the model calls `rm` (not registered) and then `grep` (registered but not admitted to this step), Then each call ends `tools/not-started` at the resolve stage, the validate/policy/guards/approval/flush/body stubs record zero invocations, and one `tool/result` per call carries the refusal (`DH-T-TOOLS-3`) [proof: in-process; deps: none]
- [x] Scenario: Flush before body observed in the database -- Given the SQLite store in a temp home, When an approved tool runs, Then the approval audit row is durable (visible from a second connection) before the body's first instruction executes (TC-10b) [proof: service:sqlite; deps: sqlite embedded; filesystem via `t.TempDir()`]

## Stage 10.3: Deadlines and Result Budget

### Implementation Steps
- [x] impl.md step 10.5 / DH-D-TOOLS-3: enforced deadlines with grace -- a body exceeding its deadline is cancelled via context; after grace the call is answered as timed out and the turn proceeds (`DH-T-TOOLS-8`, `-9`).
- [x] impl.md step 10.6 / DH-D-STORAGE-6: result budget -- no result larger than `maxResultBytes` (and the token estimate from DH-D-LLM-6) reaches a model; the excess is spilled through the DH-S-8 spill store with a reference.
- [x] Author `DH-E-TOOLS-3` (runs DH-S-39) as a scenario file.

### Dependencies
- phase-tool-pipeline-approvals-and-human-commands/stage-approvals-and-guarded-pipeline

### Test Scenarios
- [ ] Scenario: Deadline does not hold the turn -- Given a tool body that ignores cancellation, a 10 s deadline and 2 s grace on the virtual clock, When the clock advances 12 s, Then the call is answered `timeout` and the step continues (`DH-T-TOOLS-8`, TC-10a) [proof: in-process; deps: none -- virtual clock]
- [ ] Scenario: Oversized result spilled before reaching the model -- Given a tool returning 2 MB and `maxResultBytes` of 64 KB on the SQLite store, When the result is shaped, Then the logged result is within 64 KB with a spill reference and the spill file in the temp home holds all 2 MB (TC-10b) [proof: service:sqlite; deps: sqlite embedded; filesystem via `t.TempDir()` home]

## Stage 10.4: Durable Inbox Questions Commands and Grants

### Implementation Steps
- [x] impl.md step 10.7 / DH-D-AGENT-2: inbox in `internal/agent` -- input accepted any time, durable before `Send` returns, taken only at turn/step boundaries, runs at most once (SP-5; `DH-T-AGENT-4`, `-5`, `-6`).
- [x] impl.md step 10.8 / DH-D-HUMAN-5 (P1, pulled forward): the model can ask the user a question through a tool, then wait or move on; answers arrive through the inbox.
- [x] impl.md step 10.9 / DH-D-HUMAN-6 (P1, pulled forward): human commands against an agent from any client, delivered through the inbox and logged with attribution. Author `DH-E-HUMAN-6` (runs DH-S-22).
- [x] impl.md step 10.10 / DH-D-HUMAN-3 (P1, pulled forward): session grants of one directory or one application, listable and revocable, stored with the session and never copied to resume, fork or subagent (NG-8).
- [x] Close DH-S-10: write `docs/stories/harness-DSH-M1/evidence/DH-S-10.md` (denial-through-executor test, manual checks of impl.md DH-S-10) and tick impl.md steps 10.1-10.10 as built.

### Dependencies
- phase-tool-pipeline-approvals-and-human-commands/stage-approvals-and-guarded-pipeline

### Test Scenarios
- [x] Scenario: Inbox item durable before Send returns -- Given the SQLite store in a temp home, When `Send` returns for a message, Then a second connection reads the inbox row immediately (TC-10b) [proof: service:sqlite; deps: sqlite embedded; filesystem via `t.TempDir()`]
- [x] Scenario: One accepted message is at most one turn -- Given an inbox item and a driver stub that crashes after taking it, When the agent resumes, Then the item is not run a second time and the log shows one turn for it (`DH-T-AGENT-6`, TC-10a) [proof: in-process; deps: none]
- [x] Scenario: Grant does not survive resume -- Given a session grant of directory `/w` and an ended session, When the session is resumed, Then the grant list is empty and a call needing `/w` asks again (`DH-D-HUMAN-3`) [proof: in-process; deps: none]
- [x] Scenario: Question answered through the inbox -- Given the model asks a question with wait, When the fake answerer replies, Then the reply enters the inbox, the waiting tool call returns the answer, and both events are logged with attribution (TC-10a) [proof: in-process; deps: none]

# Phase 11: Agent Loop Milestone M1

## Dependencies
- phase-deepseek-provider-retry-and-usage
- phase-tool-pipeline-approvals-and-human-commands

## Stage 11.1: Model Selection and Step Tool Batch

### Implementation Steps
- [x] impl.md step 11.1 / DH-D-AGENT-7: each step uses one model selection, logged before the attempt (`DH-T-AGENT-23`).
- [x] impl.md step 11.2 / DH-D-AGENT-10: a step's tool set is fixed once at step start; its calls run in a bounded batch (cap `maxParallel`), overlapping only when resource keys do not conflict, and results commit in the model's order (`DH-T-AGENT-26`).

### Dependencies
- phase-tool-pipeline-approvals-and-human-commands/stage-durable-inbox-questions-commands-and-grants

### Test Scenarios
- [x] Scenario: Model selection logged per step -- Given an agent whose default model changes between steps, When two steps run, Then each `step/start` is followed by a selection event naming the model used and the settlement's route matches it (`DH-T-AGENT-23`, TC-11a) [proof: in-process; deps: none]
- [x] Scenario: Conflicting calls serialize, results keep model order -- Given three calls where calls 1 and 3 share a resource key and call 2 finishes first, When the batch runs with `maxParallel=3`, Then calls 1 and 3 never overlap and results are committed in order 1, 2, 3 (`DH-T-AGENT-26`) [proof: in-process; deps: none]

## Stage 11.2: Agent Driver Brackets and Cancellation

### Implementation Steps
- [x] impl.md step 11.3 / DH-D-AGENT-3: the driver in `internal/agent` -- one goroutine per agent; turns and steps bracketed, bounded by the configurable turn budget defaults of tech-spec sec. 5.5 (owner-confirmed for this stage: 500 steps, 8 attempts per step, no time limit), and closed on every path (`DH-T-AGENT-5`, `-7`..`-10`, `-22`; `DH-T-AGENT-28` inside budget).
  - Status: the driver, its brackets and `DH-T-AGENT-5`, `-7`..`-10`, `-22` are implemented and tested (`internal/agent/driver`). The owner confirmed the turn budget defaults for this stage: 500 steps, 8 attempts per step, no time limit (`budget.go`); tech-spec sec. 5.5 still calls them unconfirmed, so decision `turn-budget-defaults-s11` stays open until M1-D-LOOP-5 is answered. `DH-T-AGENT-28` is accepted in stages, as the owner decided (decision `agent-28-staged-acceptance`). `continuation_bench_test.go` (bench tag) fails if the 99th-percentile (nearest-rank) continuation latency at any rung up to the p99 rung of the synthetic heavy-tailed ladder exceeds a provisional p99 budget of 100 ms; `latency_test.go` proves the gate fails on a mostly fast sample with a slow tail. It applies this in the plain run and in the GOMEMLIMIT reruns; the max rung is measured but not budgeted. The final budget stays unpinned until the replay provider and session corpus land (impl.md step 16.7, H-10), and the check is then re-run against them.
- [x] impl.md step 11.4 / DH-D-AGENT-5: cancellation of a turn or a single step with a recorded cause on `turn/end`/`step/end`; scope Turn/Step, inbox Keep/Discard (architecture.md flow F4) (`DH-T-AGENT-15`..`-18`).
- [x] Add the property test "no tool call is left unanswered in history on any exit path" (cancel, budget, error, lease loss) using the test kit's scripted model (SP-4).

### Dependencies
- phase-agent-loop-milestone-m1/stage-model-selection-and-step-tool-batch

### Test Scenarios
- [ ] Scenario: No tool call left unanswered on any exit -- Given random scripts of model replies with tool calls and a random exit injected (cancel, budget, provider error, lease loss), When 500 property cases run, Then every history fold has a result for every tool call and brackets balance (DH-S-11 exit evidence, TC-11a) [proof: in-process; deps: none -- test kit, memstore, virtual clock]
- [ ] Scenario: Cancel step resumes at the continue-or-stop decision -- Given a running turn in step 2 on the scripted fake model, a test hook that records each time the driver reaches point 14 (continue-or-stop), and one user message parked in the inbox, When `Cancel(CauseUser, CancelOptions{Scope: Step, Inbox: Keep})` is called, Then step 2's attempt settles `cancelled`, `step/end` for step 2 records cause `user` (the closed cause set of architecture.md sec. 5.6: user, parent, extension, disposed, budget), no `turn/end` is written by the cancel, the point-14 hook fires once for the same turn, and because the parked message is pending input the driver opens step 3 of that turn with it; a second run with an empty inbox and a scripted final answer reaches point 14 and ends the turn `completed` without a step 3 (architecture.md sec. 6.4 F4; `DH-T-AGENT-16`) [proof: in-process; deps: none]
- [ ] Scenario: Turn budget closes the turn -- Given a step budget of 3 and a model that always calls a tool, When the turn runs, Then exactly 3 steps run and `turn/end` records cause `budget` (`DH-T-AGENT-8`) [proof: in-process; deps: none]

## Stage 11.3: Agent Lifecycle and Delegation Seam

### Implementation Steps
- [ ] impl.md step 11.5 / DH-D-AGENT-6: agents are created, resumed and torn down as one transaction; teardown stops what the driver started and shutdown keeps the last events (`DH-T-AGENT-19`, `-20`, `-21`).
  - Status: built when local tests pass; done when DH-E-AGENT-3 passes. What exists: the registry runs creation as one transaction (`internal/agent/registry.go`: reserve, build, Created, own, live) and rolls back on a Setup, listener or owner failure; the owner's effect is registered before the build, so an owner unloading during a blocked Setup or Created listener joins the rollback, and rollback errors (a failed final flush) are joined to the cause. `driver.NewFactory` builds and resumes agents (`driver/lifecycle.go`); `agent.OpenReport` hands a resumed agent's recovery report to the carrier; `driver/teardown.go` is the one memoised T1 to T8 order. Proved by `driver/lifecycle_test.go` (TC-11a failed create, veto and self-wait, owner disposal, owner unload during a blocked creation, rollback with a failing flush, resume report/idle start/queued input, resume Setup rollback, T1-T8 with three concurrent disposers, TC-11b SQLite shutdown, failing and slow final flush). This is local evidence. The step is ticked *built* when these local tests pass under the gate (impl.md sec. 1.3); DH-D-AGENT-6 is *done* only when remote scenario [DH-E-AGENT-3](../../harness/areas/agent-loop.md#dh-e-agent-3) passes at DH-S-17 (it is authored in Stage 11.4 and has not run). The scenario does not hold the built tick back.
- [ ] impl.md step 11.6 / DH-D-SUBAGENT-1 (P1, pulled forward): implement `src/harness/internal/delegate` -- any component delegates through one seam; every child agent has exactly one owner and is torn down with it.
  - Status: built when local tests pass; done when DH-E-SUBAGENT-2 passes. What exists: `internal/delegate/subagent` is the in-process seam (`Runtime.Start`, `OwnerOf`, `Children`, `WaitQuiet`), provided under `subagent.Key` by the `subagent.manager` component (`component.go`, injects `agent.Agents`); `WaitQuiet` waits for starts still in Setup, and the manager's unload refuses new starts under the reservation lock and releases every admitted run, pending starts included. Each run is a fiber under its owner holding the child's handle, so the owner's unload stops, tears down and settles the child. The ledger, slots, providers and subagent tools stay with DH-D-SUBAGENT-2 onward. Local evidence so far (`delegate/subagent/subagent_test.go`): a child is torn down with its owner, a completed child closes, dispose releases the run, start refusals, `WaitQuiet` waiting for a pending start, and the manager providing the seam and releasing its runs on unload. The step is not built yet: DH-D-SUBAGENT-1's Done-when (subagent-team.md sec. 8) needs local tests `DH-T-SUBAGENT-1` (including a second `Dispose` being a no-op), `-2` (no goroutine left after root disposal), `-3` (golden S1..S7 order across natural end, dispose, interrupt, time limit and teardown), `-4` (a child that delegated is held until its own child settles), `-9` (concurrent sends/release/resume serialised) and `-14` (capability refusal at A1), each written as a test tagged with its ID (M1 scope of -3, -4 and -14: M1-D-LOOP-3, open; work in gap-closure-plan Stage 17.2). DH-D-SUBAGENT-1 is *done* only when remote scenario `DH-E-SUBAGENT-2` passes; it has not run and also needs DH-D-SUBAGENT-2's ledger.

### Dependencies
- phase-agent-loop-milestone-m1/stage-agent-driver-brackets-and-cancellation

### Test Scenarios
- [ ] Scenario: Failed create leaves nothing -- Given agent creation whose third registration fails, When creation runs, Then no agent is addressable, no partial events are visible, and the first two registrations were rolled back (`DH-T-AGENT-19`, TC-11a) [proof: in-process; deps: none]
- [ ] Scenario: Child torn down with its owner -- Given component C delegating to child agent K through the seam, When C is unloaded, Then K's driver goroutine has exited before C's `Wait` returns and K has no other owner (TC-11a) [proof: in-process; deps: none]
- [ ] Scenario: Shutdown keeps the last events -- Given an agent mid-step on the SQLite store, When the host shuts down, Then the persisted log ends with closed brackets and the last settlement is present when reopened (TC-11b) [proof: service:sqlite; deps: sqlite embedded; filesystem via `t.TempDir()`]

## Stage 11.4: Context Pipeline and M1 Closure

### Implementation Steps
- [x] impl.md step 11.7 / DH-D-CONTEXT-1: implement `src/harness/internal/context/request` -- context reaches the model through one logged, ordered pipeline; `internal/context` itself has no Go package (RM-8).
- [x] Add the keyless golden of flow F2 (architecture.md sec. 6.2): one turn with one test tool call and one approval driven by the scripted model, bracketed log serialized to `internal/agent/testdata/f2_log.golden`.
- [x] Add the TC-11b test running flow F2 against the SQLite store and asserting checkpoint-before-I/O order from database rows.
- [x] Author the remaining scenarios indexed to DH-S-17, each as a reviewed `//go:build scenario` file under `src/harness/internal/testkit/scenarios/` and listed under `DH-S-17` in `test/harness/acceptance-index.yaml`, not executed: from DH-S-5 `DH-E-SESSION-1`, `DH-E-SESSION-2` (impl.md DH-S-5: "authored in DH-S-11", when a real model can be called); from DH-S-11 `DH-E-AGENT-1`..`DH-E-AGENT-4`; and `DH-E-TOOLCHAIN-1` (DH-S-1 indexes it with authoring stage DH-S-11 because impl.md authors it "when its subjects exist" -- the runtime and replay helpers). With this step every `DH-E-*` scenario of DH-S-1..11 named in impl.md sec. 7.5 has a file and an index entry (DH-S-2 has none of its own; DH-S-9's three are authored in Stage 9.2 before DH-S-9 closes; DH-S-3's `DH-E-LOADER-1` is in Stage 6.5; DH-S-4's in Stage 4.4; DH-S-6's in Stages 6.5/6.7; DH-S-7's in Stages 7.2/7.3; DH-S-8's in Stages 8.2/8.3; DH-S-10's in Stages 10.2-10.4).
- [ ] M1 manual check (impl.md DH-S-11, `manual`): from a Go test composing the real components, send one prompt to the real DeepSeek API with one test tool; record the bracketed log and the rebuilt request; cancel a second run mid-stream and record the cause on `turn/end`. Not a scenario and not a gate criterion (EV-6). *Operator decision (2026-11-22): tests authored (`internal/agent/m1_manual_test.go`, runbook in `evidence/DH-S-11.md`); the real run is recorded in a follow-up PR.* *Re-targeted 2026-11-22 by decision `models-signed-in-claude-copilot`: the prompt goes to the signed-in Claude route (Copilot also run and recorded, M1-D-LOOP-6) through the installed official client, with no API key (M1-D-PIPE-3, M1-D-CLOSE-3); gap-closure-plan Stage 17.4 does the run.*
- [ ] Close DH-S-11 and the story: write `docs/stories/harness-DSH-M1/evidence/DH-S-11.md`, tick impl.md steps 11.1-11.7 as built, and confirm the tech-spec sec. 2.3 definition of done at one commit (all 103 steps (99 deliverables) built, gate green on Windows (native) and Linux (WSL) by decision `platforms-windows-wsl`, eleven evidence records, M1 recorded on the signed-in route by decision `models-signed-in-claude-copilot`, H-21 open with its closing step 16.10). The restated definition is [decisions.md](decisions.md) sec. 1.1. *Operator decision (2026-11-22): evidence written with closure Open; ticking and closure happen in the follow-up PR that records M1.*

### Dependencies
- phase-agent-loop-milestone-m1/stage-agent-lifecycle-and-delegation-seam

### Test Scenarios
- [x] Scenario: Context pipeline is logged and ordered -- Given three context contributors registered in a known order, When the loop request for a step is built with `NextRequest`, Then the contributions appear in that order, each is backed by a logged event, and the request's `Digest()` equals the digest of `surface.Rebuild` over the log prefix ending at that request's `requestAsOf` (architecture.md sec. 4.2, 5.3; TC-11a) [proof: in-process; deps: none]
- [x] Scenario: Flow F2 bracketed log matches golden -- Given the scripted model returning one tool call then a final reply, and the fake answerer approving, When one turn runs on memstore, Then the serialized log equals `internal/agent/testdata/f2_log.golden` (TC-11a) [proof: golden; deps: committed golden `internal/agent/testdata/f2_log.golden`]
- [x] Scenario: Flow F2 persisted with checkpoints before I/O -- Given the same script on the SQLite store in a temp home, When the turn runs, Then database rows show a flush before the provider attempt, before the tool body and at step end, and the reopened log equals the in-memory golden (TC-11b) [proof: service:sqlite; deps: sqlite embedded; filesystem via `t.TempDir()`]

### Cross-Stage Dependencies
- phase-deepseek-provider-retry-and-usage/stage-deepseek-messages-provider
- phase-tool-pipeline-approvals-and-human-commands/stage-approvals-and-guarded-pipeline
- phase-sqlite-store-leases-and-recovery/stage-session-leases-and-crash-recovery

