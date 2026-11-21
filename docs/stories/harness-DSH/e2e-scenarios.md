---
title: "dsh -- end-to-end scenarios"
storyId: "harness:DSH"
---

## About this document

Story `harness:DSH` (13 points): "clone deepseek harness, given context in docs\harness
of this repo, and design and impl doc, i want you to follow impl". The clone is `dsh`,
the Go re-implementation of deepseek-harness specified in
[docs/harness/design.md](../../harness/design.md) and scheduled in
[docs/harness/impl.md](../../harness/impl.md). Following impl.md literally, this story
builds milestone **M1** -- stages **DH-S-1 .. DH-S-11** -- exactly as scoped by
[architecture.md sec. 1](architecture.md#1-scope) and ordered by
[implementation-plan.md](implementation-plan.md).

This file holds the Gherkin feature scenarios QA runs to accept each phase. Phases
here map one-to-one to the eleven phases of implementation-plan.md (same names, same
stage numbers). Field-level contracts (error codes, exit codes, settings) live in
[tech-spec.md](tech-spec.md); components (C01..C30), flows (F1..F4) and the
`TC-xx` proof rows live in architecture.md's
[Test & Environment Contract](architecture.md#test--environment-contract). This file
does not restate them; it names them.

### Two kinds of scenario -- never mixed

| Kind | What it is | Run in this story? |
|---|---|---|
| **Gate scenario** (every `- **Scenario:**` below unless tagged `@manual`; this includes the index and `-tags scenario` vet checks that prove each authored `DH-E-*` file exists) | A Go test (unit, composition, golden, two-process, loopback) proving one `TC-xx` row | **Yes.** Part of `tools/harness-gate` on Linux, macOS and Windows; may not skip |
| **Manual check** (`@manual`) | The two real-DeepSeek-API checks impl.md requires at DH-S-9 and DH-S-11 | Yes, by hand, recorded in `docs/stories/harness-DSH/evidence/DH-S-<n>.md`; never a gate criterion (EV-6) |
| **Real-environment scenario** (`DH-E-*`; listed only in the schedule table below, never as a `- **Scenario:**` bullet, so no gate check is excluded by its tag) | impl.md's acceptance scenarios, written as `//go:build scenario` files | **Authored here; executed at the stage impl.md assigns; none executed in this story** (contract rule 2) |

### Conventions

- **Tags.** `@TC-xx` names the architecture proof row; `@proof:in-process`,
  `@proof:golden`, `@proof:service:<name>` name the strategy; `@DH-T-*` / `@DH-D-*`
  name the impl.md test or deliverable; `@edge` marks an edge or failure case.
- **Services.** `service:filesystem` = `t.TempDir()`; `service:os-process` = the test
  binary re-executing itself (helper env var owned by `subprocesstest`, Stage 6.1);
  `service:go-toolchain` = the pinned Go toolchain the gate runs with;
  `service:loopback-http` = `net/http/httptest` on `127.0.0.1:0`; `service:sqlite` =
  embedded `modernc.org/sqlite` compiled into the test binary with its file under a
  temp home. **No docker, no database server, no external network, no DSN.**
- **Environment variables, never literals.** Connection-like inputs are named by
  env-var only: `DSH_HOME` (home resolution, tech-spec sec. 5), `DSH_DEEPSEEK_API_KEY`
  (manual checks and the credentialed workflow only), `DSH_REQUIRE_SCENARIOS`
  (preflight), plus Go's own `GOWORK`, `CGO_ENABLED`, `GOCOVERDIR`. No hostname,
  port, path to a real home, or credential appears in a scenario.
- **Home isolation.** The test kit (C20, Stage 6.7) always passes an explicit home
  path under `t.TempDir()` and clears `DSH_HOME` from the child environment, so a
  developer who has `DSH_HOME` set never has that home touched by a test.
- **Virtual time.** Every timing scenario runs on `testing/synctest` or the test
  kit's virtual clock; no `time.Sleep` in a `_test.go` (analyzer `nosleep`).
- **Every Then is checkable** by a Go assertion: a count, an equality, a golden
  byte-compare, an exit code, or a row read from a second connection.

### Gate provisioning in one table

Setup types: Phases 1-6 and 9 are **`inline`** (pure in-process, filesystem,
re-exec or loopback proofs; no persisted service). Phases 7, 8, 10 and 11 are
**`compose`** because their required scenarios prove persisted state in the
`service:sqlite` store (implementation-plan.md marks them `proof: service:sqlite`).
No phase is `lab-*` (architecture TC-X2: physical/hardware proof is none in this
story).

`compose` here keeps two paths, and the gate never needs the second:

- **Gate path (env-var unset, the default).** `DSH_E2E_STORE_HOME` is unset, so the
  test kit self-provisions the store in-process: embedded `modernc.org/sqlite` in the
  test binary, `dsh.db` migrated inside a `t.TempDir()` home. No docker, no server,
  no DSN; every required scenario runs and none may skip.
- **Compose path (env-var set, CI stack).** `tests/e2e/<phase-slug>/docker-compose.yml`
  runs the same Go tests in a container with `DSH_E2E_STORE_HOME` pointing at a
  named-volume home, so the store's on-disk state is exercised on a Linux container
  filesystem. Every restart/reopen scenario performs its write half, `Close`, and
  reopen half **inside one Go test** against that test's own sub-directory; no
  scenario depends on the container itself being restarted. This lane is the
  non-gating `harness-compose` job; it never replaces the gate path.
- **Compose run sequence (same for every compose phase).** `harness-store-tests` has
  no `depends_on` on `dsh-home-init`, and `--abort-on-container-exit` is never used,
  so the one-shot init exiting cannot stop the test container:
  1. `docker compose -f <file> run --rm dsh-home-init` -- must exit 0; a non-zero
     exit fails the job before any test starts.
  2. `docker compose -f <file> run --rm harness-store-tests` -- the job's result is
     this command's exit code, which is the `go test` exit code.
  3. `docker compose -f <file> down -v` -- always runs (`if: always()`), removing the
     `dsh-home` volume.

| Dependency | Env-var that would point elsewhere | Path when unset (the gate path) | Path when set |
|---|---|---|---|
| Scratch home / store file | `DSH_HOME` | Test kit creates `t.TempDir()` home; `dsh.db` created by migrations in it | **Ignored by tests** -- cleared from the child env; only `dsh` run by hand reads it |
| SQLite engine | none (embedded, no DSN exists) | `modernc.org/sqlite` linked into the test binary | n/a |
| Model endpoint | `DSH_DEEPSEEK_API_KEY` | Fake transport (in-memory) or `httptest` loopback serving committed SSE fixtures | Used only by the `@manual` checks of Phases 9 and 11, run by hand; never by the gate |
| Child processes | none | Test binary re-exec | n/a |
| Store home for `compose` phases (7, 8, 10, 11) | `DSH_E2E_STORE_HOME` | Test kit creates a `t.TempDir()` home and migrates an embedded `dsh.db` in it (the gate path) | Set only inside the compose `harness-store-tests` container to the mounted `dsh-home` volume; the test kit still creates a fresh per-test sub-directory under it |
| Required-scenario list | `DSH_REQUIRE_SCENARIOS` | Empty for this story; preflight passes | Only the credentialed `harness-scenarios.yml` workflow sets it, at later acceptance stages |

### Authored real-environment scenarios (schedule kept from impl.md)

Each `DH-E-*` below is written in this story as a reviewed
`src/harness/internal/testkit/scenarios/*_scenario_test.go` file with
`//go:build scenario`, and listed under its executing stage in
`test/harness/acceptance-index.yaml`. **None is executed in this story.** The phase
that authors each one has a gate scenario proving the file compiles, is indexed
exactly once, and is excluded from the default gate run.

| Authored in phase | Scenarios | Executed at (impl.md) |
|---|---|---|
| 1 | DH-E-TOOLCHAIN-2 | DH-S-18 (18.1) |
| 1 (indexed), 11 (file) | DH-E-TOOLCHAIN-1 | DH-S-17 (17.1) |
| 4 | file, search and watcher scenarios impl.md sec. 7.5 indexes to DH-S-4 | DH-S-18 |
| 6 (Stage 6.5) | DH-E-LOADER-1; DH-E-BOOT-2 | DH-S-17 (17.7); guarded step 21.13 |
| 6 (Stage 6.7) | DH-E-EXEC-1, DH-E-HOST-4; DH-E-BOOT-1; DH-E-UTIL-1 | DH-S-18; DH-S-22 (22.3); DH-S-39 (39.15) |
| 7 | DH-E-STORE-1, DH-E-STORE-2, DH-E-STORE-6; DH-E-STORE-5 | guarded step 16.8 (scratch homes); step 35.7 |
| 8 | DH-E-STORAGE-1; DH-E-STORAGE-2; DH-E-STORAGE-3 | DH-S-19 (19.7); DH-S-23 (23.14); DH-S-39 (39.8) |
| 9 | DH-E-LLM-1, DH-E-LLM-3, DH-E-VIEW-1 | DH-S-17 (17.4, 17.6), `requires: [DH-S-11, DH-S-16]` |
| 10 | DH-E-TOOLS-1; DH-E-TOOLS-3; DH-E-HUMAN-6 | DH-S-17 (17.5); DH-S-39 (39.7); DH-S-22 (22.10) |
| 11 | DH-E-SESSION-1, DH-E-SESSION-2, DH-E-AGENT-1..4 | DH-S-17 (17.2, 17.3) |

Phase 2 (cordis) has no `DH-E-*` of its own (impl.md DH-S-2: "None of its own").

### Phase to proof-row map

| Phase | Stage | TC rows exercised |
|---|---|---|
| 1 | DH-S-1 | TC-01a, TC-01b, TC-02b (baseline), TC-X1 |
| 2 | DH-S-2 | TC-02a, TC-02b |
| 3 | DH-S-3 | TC-03a, TC-03b |
| 4 | DH-S-4 | TC-04a, TC-04b |
| 5 | DH-S-5 | TC-05 |
| 6 | DH-S-6 | TC-06a, TC-06b, TC-06c, TC-06d, TC-X2 (fallback reported) |
| 7 | DH-S-7 | TC-07 |
| 8 | DH-S-8 | TC-08 |
| 9 | DH-S-9 | TC-09a, TC-09b |
| 10 | DH-S-10 | TC-10a, TC-10b |
| 11 | DH-S-11 | TC-11a, TC-11b |

---

# Phase 1: Repository Modules and Quality Gates

Stage DH-S-1, deliverables DH-D-TOOLCHAIN-1..5. Components C01 (`tools/harness-gate`)
and the three-module layout (`src/go`, `src/sdk`, `src/harness`, root `go.work`).

### Setup
- **Type**: inline
- **Local**: from the repository root with the pinned Go toolchain (`go` line `1.26.0`)
  and a C compiler on `PATH` for `-race`:
  `go build ./src/go/... ./src/sdk/... ./src/harness/...`, then
  `GOWORK=off go -C tools/harness-gate test -race -shuffle=on ./...`, then the full gate
  `GOWORK=off go -C tools/harness-gate run . --root ../..`. Golden baseline:
  `go -C src/go test -count=20 ./cmd/cordis-sample`.
- **CI runner**: GitHub-hosted `ubuntu-latest`, `macos-latest`, `windows-latest` --
  the keyless `harness` job of `.github/workflows/ci.yml` with `CGO_ENABLED=1`; the
  `rehearse` subcommand runs on the Linux lane only. No ADO pool, no self-hosted runner.
- **Secrets**: none. (`.github/workflows/harness-scenarios.yml` is added here and its
  preflight names `DSH_DEEPSEEK_API_KEY`, but that workflow is `workflow_dispatch` /
  schedule only and is not a gate of this story; its required list is empty.)
- **Pre-test bootstrap**: none beyond the toolchain. The C compiler shipped on the
  hosted images satisfies `-race`; a lane where `-race` cannot run fails the gate.
- **Gate provisioning**: no connection env-var exists for this phase. Seeded-violation
  modules (data race, unformatted file, under-floor package) are copied from
  `tools/harness-gate/testdata/` into `t.TempDir()` and the gate spawns `go vet` /
  `go test -race` on them as child processes of the pinned toolchain
  (`service:go-toolchain` + `service:os-process` + `service:filesystem`). Analyzer
  predicates run on in-memory ASTs (`go/parser.ParseFile` with a `src` string).
  Nothing is downloaded at test time; no scenario skips.

### Scenarios

#### Feature: Three-module workspace builds and stays pinned

- **Scenario:** Workspace builds all three product modules from the root
  ```gherkin
  @TC-01b @proof:service:go-toolchain @DH-D-TOOLCHAIN-1
  Given a clean checkout with the root go.work using ./src/go ./src/sdk ./src/harness
  When "go build ./src/go/... ./src/sdk/... ./src/harness/..." runs from the root
  And "go vet" runs over the same three module-directory patterns
  And "GOWORK=off go -C <module> build ./..." runs in each of the three module directories
  Then every command exits 0
  And "go list -m" reports exactly the three module paths of constraint RM-1
  ```
- **Scenario:** Release build of dsh ignores the workspace
  ```gherkin
  @TC-01b @proof:service:go-toolchain
  Given GOWORK=off
  When "go build -o <tmp>/dsh ./cmd/dsh" runs in src/harness with <tmp> = t.TempDir()
  Then the build succeeds with no replace directive in src/harness/go.mod
  And running <tmp>/dsh prints its embedded version and exits 0
  ```
- **Scenario:** A replace directive is rejected by the module pin test
  ```gherkin
  @TC-01a @proof:in-process @DH-T-TOOLCHAIN-11 @edge
  Given an in-memory copy of src/harness/go.mod with "replace github.com/smartpcr/agent-plugin/src/go => ../go" appended
  When DH-T-TOOLCHAIN-11 checks the parsed bytes
  Then it fails naming the replace line
  And with the committed go.mod bytes it passes
  ```
- **Scenario:** src/sdk requires nothing
  ```gherkin
  @TC-01a @proof:in-process @DH-T-SDK-17 @edge
  Given an in-memory copy of src/sdk/go.mod with one require line added
  When DH-T-SDK-17 checks it
  Then it fails naming the require line
  And the committed src/sdk/go.mod (no require block) passes
  ```
- **Scenario:** The three product modules share one go floor
  ```gherkin
  @TC-01a @proof:in-process @DH-T-TOOLCHAIN-11 @edge
  Given in-memory go.mod files where src/sdk declares "go 1.25" and the others "go 1.26.0"
  When the module pin test runs
  Then it fails naming src/sdk and both go lines
  ```

#### Feature: cordis-sample golden baseline (committed before any cordis edit)

- **Scenario:** Normalized sample output is byte-identical across runs and OSes
  ```gherkin
  @TC-02b @proof:golden
  Given src/go/cmd/cordis-sample/testdata/output.golden committed before the first DH-S-2 edit
  And run(env) executes inside a testing/synctest bubble with Settle = synctest.Wait and the barrier Pause
  And the test exporter writes "[<level>] <name> <text>" with no timestamp
  When "go -C src/go test -count=20 ./cmd/cordis-sample" runs without -update on Linux, macOS and Windows
  Then every run's output, with the temp dir rewritten to <TMP> and "\" to "/", equals the golden byte for byte
  And the number and position of "beat:" lines in each window equal the golden
  ```
- **Scenario:** A same-instant race becomes a deterministic failure
  ```gherkin
  @TC-02b @proof:in-process @edge
  Given a test-only Pause that resumes main exactly on a heartbeat tick instant
  When the sample runs in the bubble
  Then the test fails with "coincident events"
  And it never produces a flaky golden mismatch instead
  ```
- **Scenario:** main still wires the real environment
  ```gherkin
  @TC-02b @proof:in-process
  Given the refactored main.go
  When its call to run is inspected by the test (AST of main.go embedded in the test)
  Then run receives os.Stdout, os.TempDir(), the console exporter, time.Sleep and a no-op Settle
  ```

#### Feature: Gate runner runs a fixed sequence and names the first failure

- **Scenario:** Clean tree passes every gate in the fixed order
  ```gherkin
  @TC-01b @proof:service:go-toolchain @DH-T-TOOLCHAIN-9
  Given the story branch with no violations
  When "GOWORK=off go -C tools/harness-gate run . --root ../.." runs
  Then the output lists, in order: gofmt, vet + own analyzers, race/shuffle tests, coverage, harness-trace verify, performance budgets, cross-compile
  And each is reported passed and the exit code is 0
  ```
- **Scenario:** The first failing gate stops the run
  ```gherkin
  @TC-01a @proof:in-process @DH-T-TOOLCHAIN-9 @edge
  Given an in-memory gate list whose third gate returns an error
  When the runner executes
  Then gates four to seven are never invoked
  And stdout names the third gate and the exit code is non-zero
  ```
- **Scenario Outline:** A seeded violation fails the named gate
  ```gherkin
  @TC-01b @proof:service:go-toolchain @edge
  Given scratch module <module> copied from tools/harness-gate/testdata into t.TempDir()
  When harness-gate runs against it
  Then the run stops at gate <gate>, prints its name and exits non-zero
  Examples:
    | module            | gate     |
    | race-seeded       | test     |
    | unformatted-file  | gofmt    |
    | coverage-40pct    | coverage |
  ```
- **Scenario:** Coverage is merged across re-executed child processes
  ```gherkin
  @TC-01a @proof:in-process @DH-T-TOOLCHAIN-7
  Given in-memory coverage profiles for package p: parent covering 70%, re-exec'd child raising the merge to 88%
  When the floor check runs at 85%
  Then p passes
  And with the child profile dropped p fails, reported as "70% < 85%"
  ```
- **Scenario:** No baseline, suppression or exemption file is honoured
  ```gherkin
  @TC-01b @proof:service:go-toolchain @DH-T-TOOLCHAIN-8 @edge
  Given the coverage-40pct scratch module plus a file that lists p as exempt in any name the gate could look for
  When the gate runs
  Then it still fails at the coverage gate naming p
  ```
- **Scenario:** Budget tests cannot be relaxed from the environment
  ```gherkin
  @TC-01b @proof:service:go-toolchain @DH-T-TOOLCHAIN-12 @edge
  Given a scratch module whose TestBudgetX exceeds its compiled-in budget
  And the parent environment sets arbitrary DSH_* and GO* override variables
  When the performance-budget gate runs
  Then the child environment holds none of those overrides
  And the gate fails naming TestBudgetX
  ```
- **Scenario:** A lane without -race fails rather than skips
  ```gherkin
  @TC-01a @proof:in-process @edge
  Given the race/shuffle gate's preflight reporting that -race is unsupported on the lane
  When the gate runs
  Then the result is "failed" (not "skipped") and the gate is named
  ```
- **Scenario Outline:** Own analyzers flag forbidden patterns
  ```gherkin
  @TC-01a @proof:in-process @DH-T-TOOLCHAIN-8
  Given in-memory source <source> in file <file> parsed with go/parser.ParseFile
  When analyzer <analyzer> runs its predicate
  Then exactly <n> diagnostic is reported at the offending call or import
  Examples:
    | analyzer  | file                               | source                                             | n |
    | nosleep   | x_test.go                          | func TestX(t *testing.T){ time.Sleep(1) }          | 1 |
    | onespawn  | internal/tools/a.go                | import "os/exec"                                   | 1 |
    | onespawn  | internal/world/subprocess/a.go     | import "os/exec"                                   | 0 |
    | cleanup   | y_test.go                          | go func(){}() with no t.Cleanup registration       | 1 |
  ```
- **Scenario:** Analyzers against committed fixture packages
  ```gherkin
  @TC-01b @proof:service:go-toolchain
  Given tools/harness-gate/analyzers/testdata/src/... with "// want" comments
  When analysistest.Run loads them
  Then every expected diagnostic is reported and none extra
  ```
- **Scenario:** Cross-compile gate covers six targets
  ```gherkin
  @TC-01b @proof:service:go-toolchain
  Given GOWORK=off and CGO_ENABLED=0
  When the cross-compile gate runs
  Then cmd/dsh builds for linux, darwin and windows on amd64 and arm64 into a temp directory
  And a build failure on any one target fails the gate naming GOOS/GOARCH
  ```

#### Feature: Test layers, golden helper and scenario build tag

- **Scenario:** Golden helper detects drift and rewrites only on -update
  ```gherkin
  @TC-01b @proof:service:filesystem @DH-T-TOOLCHAIN-4
  Given testdata/x.golden in t.TempDir() containing "a"
  When golden.Assert(t, "x", "b") runs without -update
  Then the test fails with a diff and the file still holds "a"
  And when it runs with -update the file holds "b" and the test passes
  ```
- **Scenario:** A parser marked untrusted without a fuzz target is reported
  ```gherkin
  @TC-01a @proof:in-process @edge
  Given in-memory package source carrying "//dsh:untrusted-parser" and no Fuzz* function
  When the fuzzcheck predicate runs
  Then one diagnostic is reported
  And adding "func FuzzDecode(f *testing.F)" with f.Add seeds clears it
  ```
- **Scenario:** Scenario-tagged tests never run in the default gate
  ```gherkin
  @TC-01b @proof:service:go-toolchain @edge
  Given a scratch module with x_scenario_test.go under "//go:build scenario" that always fails
  When the test gate runs
  Then the gate passes
  And "go test -tags scenario" on the same module reports the failure
  ```

#### Feature: CI lanes, preflight and release rehearsal

- **Scenario:** Rehearsal produces six pure-Go binaries and publishes nothing
  ```gherkin
  @TC-01b @proof:service:go-toolchain @DH-D-TOOLCHAIN-5
  Given GOWORK=off
  When "harness-gate rehearse --out <tmp>" runs
  Then six binaries exist in <tmp>, each built with CGO_ENABLED=0 and -trimpath
  And the output lists each binary's SHA-256 and embedded version
  And no tag, release or network upload is made
  ```
- **Scenario Outline:** Preflight decides from the environment it is given
  ```gherkin
  @TC-01a @proof:in-process @DH-D-TOOLCHAIN-4
  Given an injected environment map <env> and an in-memory acceptance index holding DH-E-TOOLCHAIN-2
  When the preflight decision function runs
  Then the result is <result>
  Examples:
    | env                                                                  | result                                    |
    | DSH_DEEPSEEK_API_KEY absent                                          | fail, names DSH_DEEPSEEK_API_KEY          |
    | DSH_DEEPSEEK_API_KEY set, DSH_REQUIRE_SCENARIOS=DH-E-FAKE-9          | fail, names DH-E-FAKE-9                   |
    | DSH_DEEPSEEK_API_KEY set, DSH_REQUIRE_SCENARIOS empty                | pass, zero scenarios started              |
  ```
- **Scenario:** Acceptance index and scenario files agree
  ```gherkin
  @TC-01b @proof:service:go-toolchain
  Given toolchain_rehearsal_scenario_test.go and test/harness/acceptance-index.yaml
  When the gate's index check and "go -C src/harness vet -tags scenario ./..." run
  Then DH-E-TOOLCHAIN-2 appears in exactly one entry (DH-S-18) and has exactly one file
  And DH-E-TOOLCHAIN-1 is indexed under DH-S-17 with authoring stage DH-S-11 and no file yet
  And vet exits 0 and the default gate run lists zero scenario tests
  ```
- **Scenario:** An authored file missing from the index fails the index check
  ```gherkin
  @TC-01a @proof:in-process @edge
  Given an in-memory file list holding DH-E-X-1 and an index that does not list it
  When the index check runs
  Then it fails naming DH-E-X-1
  ```
- **Scenario:** Gate is green on all three CI operating systems
  ```gherkin
  @TC-X1 @proof:service:go-toolchain
  Given the stage-closing commit of DH-S-1
  When the harness job runs on ubuntu-latest, macos-latest and windows-latest
  Then each lane ends with every gate passed and uploads its output as the evidence artifact
  And the run ids are recorded in docs/stories/harness-DSH/evidence/DH-S-1.md
  ```

---

# Phase 2: Cordis Port Guarantees

Stage DH-S-2: DH-D-CORDIS-1,2,3,4,5,7,8,9 in `src/go/cordis` (C02). The existing
`src/go/cordis` tests and the TC-02b golden are the regression net.

### Setup
- **Type**: inline
- **Local**: `go -C src/go test -race -shuffle=on ./cordis/... ./cmd/cordis-sample`
  (add `-count=20` for the golden check); or the full gate from Phase 1.
- **CI runner**: GitHub-hosted `ubuntu-latest`, `macos-latest`, `windows-latest`
  (`harness` job of `.github/workflows/ci.yml`).
- **Secrets**: none.
- **Pre-test bootstrap**: none; the golden `src/go/cmd/cordis-sample/testdata/output.golden`
  is already committed by Phase 1.
- **Gate provisioning**: zero external I/O -- in-memory cordis contexts, fibers and
  exporters on the `testing/synctest` clock. No connection env-var exists; nothing to
  stand up, nothing to skip.

### Scenarios

#### Feature: Unload is complete, exclusive and final

- **Scenario:** Two concurrent unloads both wait for every disposer
  ```gherkin
  @TC-02a @proof:in-process @DH-T-CORDIS-1
  Given a fiber with three effects whose disposers block until released
  When two goroutines call Dispose at the same time under -race
  And the disposers are released
  Then both Dispose calls return only after all three disposers ran exactly once
  ```
- **Scenario:** A disposed fiber refuses new effects
  ```gherkin
  @TC-02a @proof:in-process @DH-T-CORDIS-1 @edge
  Given a fiber that has been disposed and whose Wait has returned
  When Effect, EffectAsync or EffectIter is called on it
  Then each returns the disposed error and no effect function is invoked
  ```
- **Scenario:** Dependents unload before their provider
  ```gherkin
  @TC-02a @proof:in-process @DH-T-CORDIS-2
  Given fiber B injecting a service provided by fiber A
  When A is unloaded
  Then B's disposer observes A's service still live
  And A's disposer runs after B's
  ```
- **Scenario:** Unload during an in-flight async effect waits for it
  ```gherkin
  @TC-02a @proof:in-process @edge
  Given an EffectAsync blocked on its fiber context
  When the fiber is unloaded
  Then Wait returns only after the async effect has returned
  ```

#### Feature: Every fiber carries a cancellable context

- **Scenario:** Unload cancels the fiber context with its cause
  ```gherkin
  @TC-02a @proof:in-process @DH-T-CORDIS-4
  Given a loaded fiber whose effect blocks on <-fiber.Ctx().Done()
  When the fiber is unloaded with cause "reload"
  Then the effect returns, context.Cause(ctx) is the unload cause
  And the context was cancelled before the effect's disposer ran
  ```
- **Scenario:** A child fiber context derives from its parent
  ```gherkin
  @TC-02a @proof:in-process @edge
  Given parent fiber P and child fiber C
  When P is unloaded
  Then C.Ctx() is done and its cause names P's unload
  ```

#### Feature: Context values, scoped dispatch, Notify and AroundKey

- **Scenario:** A context value is visible to its subtree only
  ```gherkin
  @TC-02a @proof:in-process @DH-T-CORDIS-5
  Given sibling contexts A and B under one root
  When a value is set on A
  Then A and its descendants read it and B reads nothing
  ```
- **Scenario:** Scoped dispatch reaches one agent subtree
  ```gherkin
  @TC-02a @proof:in-process @DH-T-CORDIS-6
  Given two agent subtrees listening on the same EventKey
  When the event is emitted scoped to subtree A
  Then A's listener runs once and B's call count stays 0
  ```
- **Scenario:** A notified fact cannot be vetoed by a failing listener
  ```gherkin
  @TC-02a @proof:in-process @DH-T-CORDIS-7 @edge
  Given three Notify listeners where the second panics and a fourth returns an error
  When the fact is notified
  Then listeners one, three and four run
  And the panic and the error are each recorded and logged once
  And Notify returns normally
  ```
- **Scenario:** AroundKey wraps in registration order with distinct types
  ```gherkin
  @TC-02a @proof:in-process @DH-T-CORDIS-12
  Given two around listeners that append "a" then "b" to the request and "B" then "A" to the result
  When the key is called with an inner handler
  Then the handler sees request "ab" and the caller receives the result suffixed "BA"
  ```

#### Feature: Config views, strict decoding and per-tree exporters

- **Scenario:** A rejected candidate does not leak into effective config
  ```gherkin
  @TC-02a @proof:in-process @DH-T-CORDIS-8
  Given a component with effective config {n: 1}
  When candidate {n: 2} is validated and rejected
  Then raw and effective both read {n: 1}
  And the config hook was never called with {n: 2}
  ```
- **Scenario:** The config hook transforms the effective view only
  ```gherkin
  @TC-02a @proof:in-process @DH-T-CORDIS-9
  Given a hook that doubles n and raw config {n: 2}
  When the component is constructed
  Then raw reads {n: 2} and effective reads {n: 4}
  ```
- **Scenario Outline:** Strict decoding fails with the field path
  ```gherkin
  @TC-02a @proof:in-process @DH-T-CORDIS-13 @edge
  Given a schema with field "retries" of type int
  When input <input> is decoded strictly
  Then decoding fails with an error naming <path> and the component is not constructed
  Examples:
    | input                     | path       |
    | {retrys: 3}               | retrys     |
    | {retries: "three"}        | retries    |
  ```
- **Scenario:** A tree logs only to the exporters its creator chose
  ```gherkin
  @TC-02a @proof:in-process @DH-T-CORDIS-14
  Given tree T1 created with New(WithExporters(mem)) and tree T2 created with none
  When each logs one line
  Then mem holds exactly one record and no package-global sink received anything
  ```

#### Feature: Existing behaviour preserved

- **Scenario:** Existing cordis tests and the sample golden still pass
  ```gherkin
  @TC-02b @proof:golden
  Given the committed cordis-sample golden and the existing cordis_test.go and race_test.go
  When "go -C src/go test -race ./cordis/... ./cmd/cordis-sample" runs at the DH-S-2 closing commit
  Then the existing tests pass
  And the golden compares equal, or differs only on lines listed as intended in evidence/DH-S-2.md
  ```

---

# Phase 3: Loader Layers and Write-Back

Stage DH-S-3: DH-D-LOADER-1,3,4,6,7,8,10,11,13 in `src/go/loader` (C03).

### Setup
- **Type**: inline
- **Local**: `go -C src/go test -race -shuffle=on ./loader/...`; re-record goldens only
  with `-update` and review the diff.
- **CI runner**: GitHub-hosted `ubuntu-latest`, `macos-latest`, `windows-latest`
  (`harness` job).
- **Secrets**: none.
- **Pre-test bootstrap**: none.
- **Gate provisioning**: TC-03a parses layer documents from in-memory bytes. TC-03b
  writes layer files into `t.TempDir()` (`service:filesystem`) created and removed per
  test. No connection env-var; `src/go` ships no expression evaluator, so tests inject
  a fake `Evaluator`. Nothing skips.

### Scenarios

#### Feature: Positioned diagnostics through the codec seam

- **Scenario:** A diagnostic carries file, line and column
  ```gherkin
  @TC-03a @proof:golden @DH-T-LOADER-1
  Given in-memory layer bytes named base.json with a type error on line 4 column 9
  When the loader validates
  Then the diagnostic text equals src/go/loader/testdata/diag_position.golden ("base.json:4:9: ...")
  ```
- **Scenario:** Every entry error is reported in one pass
  ```gherkin
  @TC-03a @proof:in-process @DH-T-LOADER-3 @edge
  Given an in-memory document with three invalid entries on lines 3, 7 and 12
  When validation runs
  Then three diagnostics are returned in document order with their positions
  ```
- **Scenario:** The existing JSON path is unchanged
  ```gherkin
  @TC-03a @proof:in-process
  Given the existing loader_test.go
  When it runs after the codec seam lands
  Then it passes unchanged
  ```

#### Feature: Expression seam

- **Scenario:** The injected evaluator fills the effective config only
  ```gherkin
  @TC-03a @proof:in-process @DH-T-LOADER-4
  Given an entry value "${{ env.X }}" and a fake evaluator returning 7
  When the entry is loaded
  Then effective config holds 7 and raw config still holds the expression text
  ```
- **Scenario:** An evaluator failure is a positioned diagnostic
  ```gherkin
  @TC-03a @proof:in-process @DH-D-LOADER-4 @edge
  Given a fake evaluator that fails on "${{ env.MISSING }}" at line 2 column 5
  When the entry is loaded
  Then the load fails with a diagnostic at that position and the entry is not constructed
  ```

#### Feature: Layer composition, ownership and write-back

- **Scenario:** The ownership index names the supplying layer
  ```gherkin
  @TC-03a @proof:in-process @DH-T-LOADER-7
  Given in-memory layers base {a: 1, b: 1} and user {b: 2}
  When composed
  Then the effective tree is {a: 1, b: 2}
  And the index reports "a" from base and "b" from user
  ```
- **Scenario:** Write-back touches exactly one layer file
  ```gherkin
  @TC-03b @proof:golden @DH-T-LOADER-8 @DH-T-LOADER-14
  Given base and user layer files with comments in t.TempDir()
  When b is set to 3 through the layer set targeting "user"
  Then the user file equals src/go/loader/testdata/writeback_user.golden with its comments kept
  And the base file is byte-identical to before
  ```
- **Scenario:** The write guard refuses a stale write
  ```gherkin
  @TC-03b @proof:service:filesystem @DH-T-LOADER-15 @edge
  Given a layer file read by the layer set and then rewritten on disk by another writer
  When the layer set writes back
  Then the write fails with a stale-source error
  And the file holds the other writer's content
  ```

#### Feature: Nested loaders, mutation API and single-entry restart

- **Scenario:** A nested loader unloads with its owner
  ```gherkin
  @TC-03a @proof:in-process @DH-D-LOADER-10
  Given component C running a nested loader with two entries
  When C is unloaded
  Then both nested entries' disposers have run before C's Wait returns
  ```
- **Scenario:** Restarting one entry leaves its siblings untouched
  ```gherkin
  @TC-03a @proof:in-process @DH-D-LOADER-11
  Given three loaded entries
  When entry two's config changes and it is restarted
  Then entry two's construct count is 2 and entries one and three stay at 1
  And exactly one settled-reload notice is emitted
  ```
- **Scenario:** One edit touching two entries yields one settled notice
  ```gherkin
  @TC-03a @proof:in-process @DH-D-LOADER-11 @edge
  Given three loaded entries
  When one edit changes entries one and three
  Then both restart and exactly one settled-reload notice is emitted, after both restarts finish
  ```
- **Scenario:** The lifecycle log records mutations in order
  ```gherkin
  @TC-03a @proof:in-process @DH-T-LOADER-9
  Given a loader
  When an entry is added, set and removed through the mutation API
  Then the lifecycle log holds add, update, remove for that path in that order
  And Lookup(path) returns not-found after the remove
  ```

---

# Phase 4: SDK Codec Schema and Leaf Packages

Stage DH-S-4: C04 `src/sdk/jsonrpc`, C05 `src/sdk/protocol`, C06 `internal/schema`,
C07 `internal/llm/vocab`, C08 leaf helpers (`internal/util/{fsx,textdiff,search,watch}`,
`internal/boot/{config,yamlx,expr,catalog}`), plus the `internal/archtest`
import-direction test.

### Setup
- **Type**: inline
- **Local**: `go -C src/sdk test -race -shuffle=on ./...` and
  `go -C src/harness test -race -shuffle=on ./internal/schema/... ./internal/llm/vocab/... ./internal/util/... ./internal/boot/... ./internal/archtest/...`.
  Fuzz exploration (not a gate): `go -C src/sdk test -fuzz FuzzDecode -fuzztime 60s ./jsonrpc`.
- **CI runner**: GitHub-hosted `ubuntu-latest`, `macos-latest`, `windows-latest`
  (`harness` job).
- **Secrets**: none.
- **Pre-test bootstrap**: none. `src/sdk/protocol/schema.json` is regenerated only by
  `go generate ./...` in `src/sdk` and committed with the code that changes it.
- **Gate provisioning**: TC-04a is zero-I/O (in-memory buffers, embedded seeds,
  committed goldens). TC-04b uses a real OS watcher on a `t.TempDir()` tree
  (`service:filesystem`) with a bounded deadline on the virtual clock where possible.
  The archtest runs `go list -deps -json` through the pinned toolchain
  (`service:go-toolchain`). No connection env-var; nothing skips.

### Scenarios

#### Feature: Bounded line-delimited JSON-RPC codec (src/sdk/jsonrpc)

- **Scenario:** A well-formed request round-trips
  ```gherkin
  @TC-04a @proof:in-process @DH-D-SDK-1
  Given an in-memory pipe and a request {id: 1, method: "session/list"}
  When it is encoded and decoded
  Then the decoded request equals the original and exactly one newline frames it
  ```
- **Scenario:** An oversized line is refused without over-allocation
  ```gherkin
  @TC-04a @proof:in-process @DH-D-SDK-1 @edge
  Given an in-memory reader with one line one byte over the configured line bound
  When the codec reads
  Then it returns jsonrpc/line-too-long
  And the allocation observed by the test stays within the bound
  ```
- **Scenario:** Pending-request bound is enforced
  ```gherkin
  @TC-04a @proof:in-process @DH-D-SDK-1 @edge
  Given a client at its maximum pending-request count
  When one more request is sent before any response arrives
  Then it fails at once with an error from the one <domain>/<reason> table
  ```
- **Scenario:** Embedded fuzz seeds never panic
  ```gherkin
  @TC-04a @proof:in-process
  Given the seed slice compiled into src/sdk/jsonrpc/seeds_test.go
  When TestDecodeSeeds feeds each seed to the decoder
  Then every seed decodes or fails with a table error, and none panics
  ```
- **Scenario:** The on-disk fuzz corpus replays as a unit test
  ```gherkin
  @TC-04a @proof:service:filesystem
  Given the committed files under src/sdk/jsonrpc/testdata/fuzz/FuzzDecode/
  When "go -C src/sdk test -run FuzzDecode ./jsonrpc" runs without -fuzz
  Then each corpus input decodes or fails with a table error, and none panics
  ```

#### Feature: One shape description, protocol schema and import direction

- **Scenario:** Derived protocol schema equals the committed file
  ```gherkin
  @TC-04a @proof:golden @DH-D-SDK-2
  Given the Go types of src/sdk/protocol
  When the schema is derived
  Then it is byte-identical to src/sdk/protocol/schema.json
  ```
- **Scenario:** Schema validates and fingerprints a value
  ```gherkin
  @TC-04a @proof:in-process @DH-D-UTIL-1
  Given a shape {name: string, n: int>=0}
  When {name: "x", n: -1} is validated
  Then validation fails naming "n"
  And the shape's fingerprint is identical across two derivations
  ```
- **Scenario:** Module import direction holds
  ```gherkin
  @TC-04a @proof:service:go-toolchain
  Given the three modules
  When internal/archtest lists dependencies with "go list -deps -json"
  Then src/sdk imports nothing under src/go or src/harness
  And src/go imports nothing under src/sdk or src/harness
  And every harness implementation package lives under internal/
  ```

#### Feature: Model vocabulary is a leaf with stable encoding

- **Scenario:** Canonical encoding is independent of insertion order
  ```gherkin
  @TC-04a @proof:in-process @DH-T-LLM-1
  Given two Message values built with map fields inserted in different orders
  When each is canonically encoded
  Then the bytes and SHA-256 digests are identical
  ```
- **Scenario:** Persisted vocabulary fingerprints are unchanged
  ```gherkin
  @TC-04a @proof:golden
  Given internal/llm/vocab/testdata/fingerprints.golden
  When every vocabulary type that will be persisted is fingerprinted
  Then each line equals its golden line
  ```
- **Scenario:** vocab holds no request type and imports no area
  ```gherkin
  @TC-05 @proof:in-process @edge
  Given the ASTs of every file in internal/llm/vocab (embedded via embed.FS)
  When the archtest predicate searches for a type named Request or a struct holding []Message plus Controls
  Then none is found
  And vocab's imports contain no internal area package
  ```
- **Scenario Outline:** One meaning per value kind across OSes
  ```gherkin
  @TC-04a @proof:in-process @DH-D-UTIL-4
  Given the util <kind> package
  When <input> is evaluated against <subject>
  Then the result is <result> on Linux, macOS and Windows alike
  Examples:
    | kind    | input        | subject    | result   |
    | glob    | **/*.go      | a/b/c.go   | match    |
    | glob    | *.go         | a/b.go     | no match |
    | version | >=1.2.0      | 1.10.0     | match    |
  ```

#### Feature: File primitives, diff, search and watcher

- **Scenario:** Atomic replace never exposes a partial file
  ```gherkin
  @TC-04b @proof:service:filesystem @DH-D-FS-1
  Given a file in t.TempDir() and a reader goroutine reading it in a loop
  When fsx.Replace writes new content 200 times
  Then every read returns either a whole old or a whole new content
  ```
- **Scenario:** Atomic replace retries while Windows holds the target open
  ```gherkin
  @TC-04b @proof:service:filesystem @DH-D-FS-1 @edge
  Given a target file opened for read by a second handle (Windows lane)
  When fsx.Replace runs and the handle closes within the retry window
  Then the replace succeeds and no temp file is left in the directory
  ```
- **Scenario:** Exclusive create and advisory lock refuse a second holder
  ```gherkin
  @TC-04b @proof:service:filesystem @DH-D-FS-1 @edge
  Given a lock file in t.TempDir() held through fsx
  When a second fsx lock on the same file is attempted without waiting
  Then it fails as held, and after release it succeeds
  ```
- **Scenario:** Model-format patch round-trips
  ```gherkin
  @TC-04a @proof:in-process @DH-D-FS-2
  Given in-memory texts a and b including a missing final newline
  When a model-format patch from a to b is produced and applied to a
  Then the result equals b byte for byte
  ```
- **Scenario:** Search honours ignore files
  ```gherkin
  @TC-04b @proof:golden @DH-D-FS-3
  Given a temp tree with .gitignore excluding build/
  When search.List runs
  Then no path under build/ is returned
  And the listing equals internal/util/search/testdata/list.golden
  ```
- **Scenario:** Watcher sends one notice per settled change
  ```gherkin
  @TC-04b @proof:service:filesystem @DH-D-UTIL-3
  Given a watch on a temp directory
  When a file is replaced atomically
  Then exactly one change notice for that path arrives within the bounded deadline
  ```
- **Scenario:** A burst of writes is debounced into one notice
  ```gherkin
  @TC-04b @proof:service:filesystem @DH-D-UTIL-3 @edge
  Given a watch on a temp directory
  When one file is written 50 times in quick succession
  Then exactly one notice for that path arrives after the debounce settles
  ```

#### Feature: YAML dialect, expressions, logging and catalog

- **Scenario:** JSON and YAML codecs give the same tree
  ```gherkin
  @TC-04a @proof:in-process @DH-T-LOADER-2
  Given equivalent in-memory JSON and YAML documents
  When each is loaded through its codec
  Then the effective trees are equal and each position points into its own source
  ```
- **Scenario:** yamlx edit keeps comments
  ```gherkin
  @TC-04a @proof:golden @DH-D-BOOT-4
  Given an in-memory YAML document with comments
  When yamlx sets one scalar
  Then the output equals internal/boot/yamlx/testdata/edit_keep_comments.golden
  ```
- **Scenario Outline:** yamlx rejects constructs outside the dialect
  ```gherkin
  @TC-04a @proof:in-process @DH-D-BOOT-4 @edge
  Given an in-memory YAML document containing <construct>
  When yamlx decodes it
  Then decoding fails with a positioned diagnostic naming the construct
  Examples:
    | construct          |
    | a custom tag !code |
    | an anchor to code  |
  ```
- **Scenario:** Expression engine is side-effect free
  ```gherkin
  @TC-04a @proof:in-process @DH-T-LOADER-6
  Given expressions referencing env, launch and an undefined name
  When they are evaluated against an in-memory scope
  Then defined names resolve, the undefined one fails naming it
  And the engine's function table holds no function that reaches the filesystem or network
  ```
- **Scenario:** Logging component honours the tree's exporters
  ```gherkin
  @TC-04a @proof:in-process @DH-D-LOADER-12
  Given a tree created with an in-memory exporter and the slog logging component loaded
  When a component logs one structured record
  Then the in-memory exporter holds it with its attributes and no other sink received it
  ```
- **Scenario:** Catalog lists compiled-in components by name
  ```gherkin
  @TC-04a @proof:golden @DH-D-BOOT-1
  Given the compiled-in catalog
  When it is printed
  Then the output equals internal/boot/catalog/testdata/catalog.golden
  ```
- **Scenario:** DH-S-4 scenarios are authored and indexed, not executed
  ```gherkin
  @TC-01b @proof:service:go-toolchain
  Given the file, search and watcher scenario files impl.md sec. 7.5 assigns to DH-S-4
  When the index check and "go -C src/harness vet -tags scenario ./..." run
  Then each has one file and one DH-S-18 entry, vet exits 0, and the default gate runs none
  ```

---

# Phase 5: Session Core Contracts

Stage DH-S-5, in memory: C09 session core, C10 store contract + `memstore`, C11 fold
registry, C12 LLM runtime, C13 agent contract, C14 interaction registry.

### Setup
- **Type**: inline
- **Local**: `go -C src/harness test -race -shuffle=on ./internal/session/... ./internal/llm/... ./internal/agent/... ./internal/human/...`.
- **CI runner**: GitHub-hosted `ubuntu-latest`, `macos-latest`, `windows-latest`
  (`harness` job).
- **Secrets**: none.
- **Pre-test bootstrap**: none.
- **Gate provisioning**: TC-05 is zero external I/O by contract -- the in-memory
  reference store `memstore`, in-memory fake routes and the virtual clock. The SQLite
  store does not exist yet; the same `storetest.Run` suite is re-run on SQLite in
  Phase 7. No connection env-var; nothing skips.

### Scenarios

#### Feature: Typed, versioned events and the store contract

- **Scenario:** An unknown event version surfaces instead of being guessed
  ```gherkin
  @TC-05 @proof:in-process @DH-T-SESSION-2 @edge
  Given an encoded event tool/result version 3 while the registry knows version 2
  When it is decoded
  Then the decoder returns a typed unknown-version error and no value is fabricated
  ```
- **Scenario:** Conformance suite passes on memstore
  ```gherkin
  @TC-05 @proof:in-process @DH-T-STORE-1
  Given storetest.Run with the memstore factory
  When the suite runs
  Then every case passes: append order, flush barrier semantics, paged reads, listing
  ```
- **Scenario:** A persisted type cannot change unnoticed
  ```gherkin
  @TC-05 @proof:golden @DH-T-STORE-10 @edge
  Given a test-only change adding a field to a persisted event without a version bump
  When the inventory test compares with internal/session/store/testdata/persisted.golden
  Then it fails naming that type
  And with a version bump plus an upgrade note it passes after -update
  ```
- **Scenario:** A registration has exactly one owner
  ```gherkin
  @TC-05 @proof:in-process @DH-T-SESSION-17 @DH-T-SESSION-18 @edge
  Given agent A registered an event type in its scope
  When agent B tries to register or append using A's registration
  Then B is refused and A's registration is unchanged
  ```

#### Feature: Live session, folds, requests and brackets

- **Scenario:** Ordered once-only delivery under concurrency
  ```gherkin
  @TC-05 @proof:in-process @DH-T-SESSION-3
  Given a live session on memstore with three subscribers
  When 1,000 events are appended from 8 goroutines under -race
  Then each subscriber receives every event exactly once in sequence order
  And the session itself holds no history (a fresh fold over the store equals the subscribers' view)
  ```
- **Scenario:** A rebuilt loop request equals its digest
  ```gherkin
  @TC-05 @proof:in-process @DH-T-SESSION-12
  Given a log: user message, assistant tool call, tool result
  When NextRequest is built and later surface.Rebuild runs over the prefix ending at its requestAsOf
  Then the canonical bytes and Digest() are identical
  ```
- **Scenario:** An auxiliary request names its builder event
  ```gherkin
  @TC-05 @proof:in-process @DH-T-SESSION-12
  Given one committed builder event at sequence s
  When RequestFrom(s) is called
  Then the request's Meta() names that event and its digest rebuilds from the log
  ```
- **Scenario:** No package outside session can construct a request
  ```gherkin
  @TC-05 @proof:in-process @edge
  Given the ASTs of every harness package (embedded)
  When the archtest predicate looks for composite literals or constructors of session.Request outside internal/session
  Then none is found
  ```
- **Scenario:** An unbalanced bracket is refused on append
  ```gherkin
  @TC-05 @proof:in-process @DH-T-SESSION-13 @edge
  Given an open step/start inside turn/start
  When turn/end is appended before step/end
  Then the append is refused with a bracket error and the log is unchanged
  ```
- **Scenario:** Failure paths close brackets in order
  ```gherkin
  @TC-05 @proof:in-process @DH-T-SESSION-13
  Given random sequences of opens and injected failures (property test)
  When each failure path runs its closers
  Then step/end precedes turn/end every time and every bracket balances
  ```
- **Scenario:** Only the session core writes interrupted
  ```gherkin
  @TC-05 @proof:in-process @edge
  Given a caller outside the session core
  When it appends a closer with outcome interrupted
  Then the append is refused
  ```

#### Feature: Model runtime settles exactly once per attempt

- **Scenario:** One attempt, one caller-logged settlement
  ```gherkin
  @TC-05 @proof:in-process @DH-T-LLM-2
  Given the runtime with a fake route that fails once then succeeds and a recording AttemptSink
  When the caller runs one Prepared.Stream attempt and, separately, one Complete call
  Then each stream ends with exactly one Finish chunk
  And each attempt yields exactly one record carrying requestAsOf and requestDigest
  And the store holds one settlement per attempt, all appended by the caller
  And the runtime made zero session appends
  ```
- **Scenario Outline:** Abnormal endings still produce one Finish
  ```gherkin
  @TC-05 @proof:in-process @DH-D-LLM-1 @edge
  Given a fake route that <misbehaves>
  When one attempt runs
  Then the stream ends with exactly one Finish and one attempt record of class <class>
  Examples:
    | misbehaves                   | class      |
    | panics mid-stream            | error      |
    | ends with no finish chunk    | error      |
    | is cancelled by the caller   | cancelled  |
  ```

#### Feature: Agent contract, steering points and registries

- **Scenario:** A rejected PreStep ends the turn blocked; observers cannot veto
  ```gherkin
  @TC-05 @proof:in-process @DH-T-AGENT-10
  Given a PreStep listener returning Reject{Reason: "quota"} and a Notify observer that panics
  When the driver reaches the pre-step point
  Then no step/start is appended and turn/end records outcome blocked with reason "quota"
  And the observer panic is recorded without changing the outcome
  ```
- **Scenario:** A failing steering listener ends the turn error
  ```gherkin
  @TC-05 @proof:in-process @DH-D-AGENT-4 @edge
  Given a PreStep listener that panics
  When the pre-step point is reached
  Then turn/end records outcome error with extension/failed
  ```
- **Scenario:** AttemptFailed and OnTurnStopping return closed decisions
  ```gherkin
  @TC-05 @proof:in-process @DH-D-AGENT-4
  Given AttemptFailed returning GiveUp{} and OnTurnStopping returning KeepOpen{}
  When an attempt fails and later the turn reaches its stop point
  Then no retry is made, and the turn stays open for the next inbox item
  ```
- **Scenario:** Each agent has exactly one owner
  ```gherkin
  @TC-05 @proof:in-process @DH-T-AGENT-1 @DH-T-AGENT-2
  Given owner O created agent A
  When a second owner tries to claim A
  Then the claim is refused and A is addressable only through O
  ```
- **Scenario:** Folds read at one cut
  ```gherkin
  @TC-05 @proof:in-process @DH-D-VIEW-1
  Given two registered folds and a concurrent appender
  When a reader requests both folds
  Then both results report the same sequence number
  ```
- **Scenario:** Pending interactions are addressed by agent
  ```gherkin
  @TC-05 @proof:in-process @DH-D-HUMAN-1
  Given agents A and B each with one pending approval
  When A's interactions are listed and the first is answered
  Then only A's approval is returned and exactly that one resolves
  ```

---

# Phase 6: Boot Launch Egress Secrets and Test Kit

Stage DH-S-6 (17 deliverables, dependency circle C-1, closes at ONE commit): C08 leaf
helpers, C15 boot and host, C16 CLI, C17 subprocess, C18 secret store, C19 egress,
C20 test kit.

### Setup
- **Type**: inline
- **Local**: `go -C src/harness test -race -shuffle=on ./internal/util/... ./internal/world/... ./internal/boot/... ./internal/cli/... ./internal/host/... ./internal/human/... ./internal/testkit/...`.
  Manual checks of impl.md DH-S-6 (broken entry, child ignoring termination, bad layer
  edit) are run on a scratch home created with `t.TempDir()` from a Go test, never the
  developer's home.
- **CI runner**: GitHub-hosted `ubuntu-latest`, `macos-latest`, `windows-latest`
  (`harness` job). Each OS runs its own process-tree mechanism (process group /
  Job Object / Linux control group or reported fallback).
- **Secrets**: none. The secret-store scenarios use a fake secret source returning a
  test value; the child-environment scenario sets `DSH_DEEPSEEK_API_KEY` to a dummy
  in the parent test process only.
- **Pre-test bootstrap**: none.
- **Gate provisioning**: homes are `t.TempDir()` (`service:filesystem`); child and
  grandchild processes are the test binary re-executed by `subprocesstest`
  (`service:os-process`); egress targets are `httptest` servers on `127.0.0.1:0`
  (`service:loopback-http`). `DSH_HOME` is cleared from every child environment and
  the test kit passes an explicit home, so a set `DSH_HOME` on the developer machine is
  never read. No connection env-var gates anything; nothing skips. TC-X2: on a Linux
  runner without a writable control group the process-group fallback runs and is
  reported, not skipped.

### Scenarios

#### Feature: Leaf helpers and the one place a process starts

- **Scenario:** Error kinds resolve to one table and never leak secrets
  ```gherkin
  @TC-06a @proof:in-process @DH-D-UTIL-5
  Given an error wrapped three levels deep around a value marked secret
  When herr.Kind is read and the error is formatted
  Then Kind returns the innermost <domain>/<reason>
  And the formatted text does not contain the secret value
  ```
- **Scenario:** Arguments are passed literally, never through a shell
  ```gherkin
  @TC-06c @proof:service:os-process @DH-D-EXEC-1 @edge
  Given the re-exec'd test binary as the program and an argument "a; echo pwned"
  When it is started through internal/world/subprocess
  Then the child reports exactly one argument equal to "a; echo pwned"
  ```
- **Scenario:** The child environment never inherits a secret
  ```gherkin
  @TC-06c @proof:service:os-process @DH-D-EXEC-1
  Given the parent test process has DSH_DEEPSEEK_API_KEY set to a dummy value
  When a child is started through subprocess with an explicit env list
  Then the child reports DSH_DEEPSEEK_API_KEY as absent
  ```
- **Scenario:** StopTree stops and awaits the whole tree
  ```gherkin
  @TC-06c @proof:service:os-process @DH-D-EXEC-2
  Given a re-exec'd child that spawns a grandchild ignoring termination
  When StopTree runs with a 2 s grace
  Then both processes are gone and StopTree returned only after both exited
  ```
- **Scenario:** Linux ownership fallback is reported
  ```gherkin
  @TC-X2 @proof:service:os-process @edge
  Given a Linux lane where the control group is not writable
  When StopTree is used
  Then the process-group fallback stops the tree
  And the fallback is reported in the boot audit and the DH-S-6 evidence record
  ```

#### Feature: Launch facts, private home and secret references

- **Scenario:** Home is created private
  ```gherkin
  @TC-06b @proof:service:filesystem @DH-D-STORAGE-2
  Given an empty t.TempDir() as home
  When the layout check runs
  Then dsh.db's directory, run/, spill/, blobs/, config/ and reports/ exist private to the owner
  ```
- **Scenario Outline:** A loosened home directory fails the check
  ```gherkin
  @TC-06b @proof:service:filesystem @DH-D-STORAGE-2 @edge
  Given a home that passed the layout check
  When spill/ is loosened by <how> on <os>
  Then the next check fails naming spill/ and boot does not proceed
  Examples:
    | os      | how                                                       |
    | unix    | os.Chmod(spill, 0o777)                                    |
    | windows | an added DACL entry granting Everyone (S-1-1-0) read      |
  ```
- **Scenario:** A secret is resolved per use and redacted everywhere
  ```gherkin
  @TC-06a @proof:in-process @DH-D-HUMAN-7
  Given config naming secret://deepseek/api-key and a fake source returning the test value
  When the reference is resolved for one failing use
  Then the value is used once
  And it appears in no captured config dump, log line or error text
  ```
- **Scenario:** Launch facts feed the expression scope
  ```gherkin
  @TC-06a @proof:in-process @DH-D-BOOT-2
  Given in-memory launch facts with OS "linux"
  When "${{ launch.os }}" is evaluated
  Then it yields "linux"
  ```

#### Feature: Profiles, command tree and the exit contract

- **Scenario:** Layer stack precedence is bundle < profile < user < project
  ```gherkin
  @TC-06a @proof:in-process @DH-D-BOOT-3
  Given in-memory bundle {model: a}, profile {model: b}, user {} and project {}
  When the stack composes
  Then model is b and the ownership index names the profile layer
  ```
- **Scenario Outline:** Commands map to the fixed exit-code table
  ```gherkin
  @TC-06a @proof:in-process @DH-D-CLI-3
  Given the command tree dispatched in process with captured streams
  When <argv> is dispatched
  Then the exit code is <code> and <stream> holds <text>
  Examples:
    | argv               | code | stream | text                              |
    | dsh --version      | 0    | stdout | the embedded version              |
    | dsh --no-such-flag | 2    | stderr | one usage line plus a help hint   |
  ```
- **Scenario:** Generated CLI reference is current
  ```gherkin
  @TC-06a @proof:golden @DH-D-CLI-1
  Given the command tree
  When the reference is generated
  Then it equals internal/cli/testdata/reference.golden
  ```

#### Feature: Boot sequence, fatal path and bounded shutdown

- **Scenario:** Boot runs its steps in order and audits permits
  ```gherkin
  @TC-06a @proof:in-process @DH-D-BOOT-5
  Given in-memory fakes for every boot step and no outbound permits configured
  When boot runs
  Then launch facts, home check, layers, compose, audit, readiness are called in that order
  And the audit lists every outbound kind as not permitted
  ```
- **Scenario:** A broken required entry fails boot once, loudly
  ```gherkin
  @TC-06b @proof:service:filesystem @DH-D-HOST-7
  Given a temp home and a layer with a broken required entry
  When boot runs
  Then exactly one fatal message is printed
  And exactly one file exists in <home>/reports/ naming the entry and its file:line:col
  ```
- **Scenario:** Shutdown overrun stops the tree and exits 124
  ```gherkin
  @TC-06c @proof:service:os-process @DH-D-BOOT-5 @edge
  Given a composed tree whose re-exec'd child ignores termination and a shutdown budget shortened on the test clock
  When shutdown runs
  Then the tree is killed, the host exits 124 and no child remains
  ```

#### Feature: Preview, validated reload and settings edits

- **Scenario:** Preview composes without starting anything
  ```gherkin
  @TC-06a @proof:in-process @DH-D-BOOT-6
  Given layers in memory
  When preview runs through the Go API
  Then it returns the composed entry list with owning layers
  And no component's construct function was called
  ```
- **Scenario:** A bad layer edit leaves the running tree unchanged
  ```gherkin
  @TC-06b @proof:service:filesystem @DH-D-BOOT-7 @edge
  Given a tree booted from layer files in a temp home
  When the user layer is rewritten with an invalid value and reload fires
  Then reload is rejected with a positioned diagnostic
  And every component's effective config is unchanged
  ```
- **Scenario:** Concurrent settings edits never lose a write
  ```gherkin
  @TC-06b @proof:service:filesystem @DH-D-STORAGE-8 @edge
  Given two edit-path clients on one temp home that read the same version
  When both edit different keys
  Then one succeeds and the other gets a stale-source error
  And after the loser retries both values are on disk
  ```

#### Feature: Nothing leaves the machine by default

- **Scenario:** No permit, no dial
  ```gherkin
  @TC-06d @proof:service:loopback-http @DH-D-UTIL-2
  Given an httptest server on 127.0.0.1:0 counting connections
  When the egress client POSTs without a permit
  Then it returns egress/not-permitted and the server's connection count stays 0
  And with a model-request permit for that host the request arrives once
  ```
- **Scenario:** The off-switch overrides every permit
  ```gherkin
  @TC-06a @proof:in-process @DH-D-HOST-4 @edge
  Given permits for every data kind and the off-switch on
  When permit decisions are evaluated
  Then every kind is denied and the boot audit lists each kind as off
  ```

#### Feature: Harness test kit and DH-S-6 closure

- **Scenario:** Virtual clock drives deadlines without sleeping
  ```gherkin
  @TC-06a @proof:in-process @DH-D-UTIL-6
  Given a test-kit tree with a component waiting 30 s on the virtual clock
  When the clock is advanced 30 s
  Then the wait completes in under 100 ms of wall time
  ```
- **Scenario:** The fake model scripts one turn
  ```gherkin
  @TC-06a @proof:in-process @DH-D-UTIL-6
  Given the fake model scripted with one text reply
  When the runtime makes one attempt through the fake transport
  Then one assistant/message is settled with the scripted text
  ```
- **Scenario:** Temp home and children are torn down by cleanup
  ```gherkin
  @TC-06b @TC-06c @proof:service:filesystem @DH-D-UTIL-6
  Given a test that creates a test-kit temp home and a child process
  When the test ends
  Then the home directory no longer exists and no child process remains
  ```
- **Scenario:** DH-S-6 scenarios are authored and indexed, not executed
  ```gherkin
  @TC-01b @proof:service:go-toolchain
  Given the scenario files under src/harness/internal/testkit/scenarios/ and acceptance-index.yaml
  When the index check and "go -C src/harness vet -tags scenario ./..." run
  Then DH-E-EXEC-1 and DH-E-HOST-4 sit under DH-S-18, DH-E-BOOT-1 under DH-S-22,
    DH-E-UTIL-1 under DH-S-39, DH-E-LOADER-1 under DH-S-17 and DH-E-BOOT-2 under step 21.13
  And each has exactly one file, vet exits 0, and the default gate runs none of them
  ```
- **Scenario:** The circle closes at one commit
  ```gherkin
  @TC-X1 @proof:service:go-toolchain
  Given the DH-S-6 closing commit
  When the gate runs on the three OS lanes
  Then all seventeen deliverables' tests pass at that commit, including those that waited on circle C-1
  And H-21 is listed open in evidence/DH-S-6.md
  ```

---

# Phase 7: SQLite Store Leases and Recovery

Stage DH-S-7: C21 `internal/session/store/sqlite` (DH-D-STORE-2,3,4,6,7,8). Fact F-2
(the driver) is measured before anything is built on it. Until guarded step 16.8
passes, this store is used on **scratch homes only**.

### Setup
- **Type**: compose
- **Compose file**: `tests/e2e/sqlite-store-leases-and-recovery/docker-compose.yml`, starting
  `dsh-home-init` (one-shot container that creates and owns the `dsh-home` named
  volume) and `harness-store-tests` (the pinned Go toolchain image from
  `src/harness/go.mod` running `go -C src/harness test -race ./internal/session/store/...` with
  `DSH_E2E_STORE_HOME` set to the mounted volume). Run with the three-step
  compose run sequence above (init, then tests, then `down -v`).
- **Local**: `go -C src/harness test -race -shuffle=on ./internal/session/store/...`;
  driver measurement: `go -C src/harness test -run TestBudgetStoreAppend -v ./internal/session/store/sqlite`.
- **CI runner**: gate path on GitHub-hosted `ubuntu-latest`, `macos-latest`,
  `windows-latest` (`harness` job, required); compose path on GitHub-hosted
  `ubuntu-latest` (`harness-compose` job, non-gating, docker preinstalled on the image).
- **Secrets**: none.
- **Pre-test bootstrap**: none -- `modernc.org/sqlite` (pinned with `modernc.org/libc`
  in `src/harness/go.mod`) is fetched by the normal module download of the gate's
  build; no system SQLite, no server.
- **Gate provisioning**: `service:sqlite` is **embedded and ephemeral**: the pure-Go
  engine is compiled into the test binary and `dsh.db` is created by the numbered
  migrations inside a test-kit temp home under `t.TempDir()`. There is no DSN and no
  connection env-var in the gate (`DSH_E2E_STORE_HOME` unset selects this embedded path); `DSH_HOME` is cleared from children so the developer's real
  home is never opened. Two-process lease, follow and crash-injection scenarios
  re-execute the test binary against the same temp home (`service:os-process`). No
  scenario skips.

### Scenarios

#### Feature: Driver measurement and atomic appends

- **Scenario:** Conformance suite passes on SQLite
  ```gherkin
  @TC-07 @proof:service:sqlite @DH-T-STORE-1
  Given storetest.Run with a factory opening dsh.db in a test-kit temp home
  When the suite runs
  Then every case that passes on memstore passes on SQLite
  ```
- **Scenario:** A multi-event append is all or nothing
  ```gherkin
  @TC-07 @proof:service:sqlite @DH-D-STORE-2 @edge
  Given an append of three events where the third violates a constraint
  When the append runs
  Then a fresh connection reads none of the three rows
  ```
- **Scenario:** Flush returns only after the commit is durable
  ```gherkin
  @TC-07 @proof:service:sqlite @DH-D-STORE-2
  Given an append followed by Flush(ctx)
  When Flush returns and the re-exec'd child is killed immediately after
  Then reopening the store shows the appended rows
  ```
- **Scenario:** Driver budget is measured and recorded
  ```gherkin
  @TC-07 @proof:service:sqlite @DH-T-STORE-18
  Given the pinned driver
  When TestBudgetStoreAppend runs 1,000 single-event appends with flush
  Then p95 is under the compiled-in budget and the measured figure is printed for evidence/DH-S-7.md
  ```

#### Feature: One writer per session, safe takeover

- **Scenario:** A second writer is refused across processes
  ```gherkin
  @TC-07 @proof:service:sqlite @DH-T-STORE-6
  Given re-exec'd process A holding the lease on session S in a temp home
  When re-exec'd process B opens S for writing
  Then B is refused with session/owned naming A's process and S's log holds only A's events
  ```
- **Scenario:** A dead holder is taken over and its stale epoch rejected
  ```gherkin
  @TC-07 @proof:service:sqlite @DH-T-STORE-7 @edge
  Given process A holding the lease is killed
  When process B opens S
  Then B acquires A's liveness lock inside the write transaction and bumps the epoch
  And an append carrying A's epoch is rejected
  ```
- **Scenario:** A lease never expires by time alone
  ```gherkin
  @TC-07 @proof:service:sqlite @DH-D-STORE-3 @edge
  Given process A alive and idle holding the lease
  When process B's clock is advanced 24 hours and B opens S
  Then B is still refused with session/owned
  And only an explicit Force takeover succeeds, bumping the epoch
  ```

#### Feature: Checkpoints and honest crash recovery

- **Scenario:** A crash between append and flush recovers honestly
  ```gherkin
  @TC-07 @proof:service:sqlite @DH-T-STORE-8
  Given a re-exec'd child that appends turn/start, step/start and a tool call, then exits before flush
  When the store is reopened for write
  Then the brackets are balanced with closers marked interrupted, appended by the session core
  And the tool call has an interrupted result
  ```
- **Scenario:** A failed flush blocks the tool body
  ```gherkin
  @TC-07 @proof:service:sqlite @DH-T-STORE-9 @edge
  Given a store whose Flush returns an injected error
  When a checkpoint precedes a tool body
  Then the body's call counter stays 0 and the step ends with a checkpoint failure
  ```
- **Scenario:** Recovery is idempotent
  ```gherkin
  @TC-07 @proof:service:sqlite @DH-D-STORE-4 @edge
  Given a store already recovered once
  When it is reopened for write again
  Then no further closer or interrupted result is appended
  ```

#### Feature: Export, import, follow and repair

- **Scenario:** Export then import round-trips
  ```gherkin
  @TC-07 @proof:golden @DH-T-STORE-13
  Given a session with 50 events in home H1
  When it is exported and imported into empty home H2
  Then H2's rows equal H1's (type, version, payload, sequence)
  And the export of the fixed fixture equals store/sqlite/testdata/export.golden
  ```
- **Scenario:** Import refuses a session id already present
  ```gherkin
  @TC-07 @proof:service:sqlite @DH-T-STORE-13 @edge
  Given H2 already holding the imported session
  When the same export is imported again
  Then it is refused naming the session id and H2 is unchanged
  ```
- **Scenario:** A follower sees another process's commits
  ```gherkin
  @TC-07 @proof:service:sqlite @DH-T-STORE-14
  Given re-exec'd process A appending 10 events
  When process B follows the session
  Then B observes all 10 in order within the bounded latency
  ```
- **Scenario:** Once-per-home duties have one runner
  ```gherkin
  @TC-07 @proof:service:sqlite @DH-D-STORE-8 @edge
  Given two re-exec'd processes on one home both scheduling the same duty
  When the duty fires
  Then it runs exactly once
  ```
- **Scenario:** An interrupted deletion finishes on next open
  ```gherkin
  @TC-07 @proof:service:sqlite @DH-T-STORE-11 @DH-T-STORE-12
  Given a deletion killed after removing half the rows
  When the store is reopened
  Then the deletion completes, no row or spill file of that session remains
  And check reports clean
  ```
- **Scenario:** DH-S-7 scenarios are authored and indexed, not executed
  ```gherkin
  @TC-01b @proof:service:go-toolchain
  Given the scenario files for DH-E-STORE-1, DH-E-STORE-2, DH-E-STORE-6 and DH-E-STORE-5
  When the index check and the scenario-tag vet run
  Then the first three sit under guarded step 16.8 and DH-E-STORE-5 under step 35.7
  And the default gate runs none of them
  ```

---

# Phase 8: Machine State Views and Spill

Stage DH-S-8: C22 `internal/storage` (DH-D-STORAGE-1,3,5,7) and C11 views
(DH-D-VIEW-2,3,5), in the same `dsh.db` and the home's `spill/` and `blobs/`.

### Setup
- **Type**: compose
- **Compose file**: `tests/e2e/machine-state-views-and-spill/docker-compose.yml`, starting
  `dsh-home-init` (one-shot container that creates and owns the `dsh-home` named
  volume) and `harness-store-tests` (the pinned Go toolchain image from
  `src/harness/go.mod` running `go -C src/harness test -race ./internal/storage/... ./internal/session/views/...` with
  `DSH_E2E_STORE_HOME` set to the mounted volume). Run with the three-step
  compose run sequence above (init, then tests, then `down -v`).
- **Local**: `go -C src/harness test -race -shuffle=on ./internal/storage/... ./internal/session/views/...`.
  Manual checks of impl.md DH-S-8 (fold version change, the 5 MB result) run from a Go
  test on a `t.TempDir()` scratch home.
- **CI runner**: gate path on GitHub-hosted `ubuntu-latest`, `macos-latest`,
  `windows-latest` (`harness` job, required); compose path on GitHub-hosted
  `ubuntu-latest` (`harness-compose` job, non-gating, docker preinstalled on the image).
- **Secrets**: none.
- **Pre-test bootstrap**: none.
- **Gate provisioning**: same embedded provision as Phase 7 -- `modernc.org/sqlite`
  in the test binary, `dsh.db`, `spill/` and `blobs/` under a test-kit temp home
  (`service:sqlite` + `service:filesystem`) whenever `DSH_E2E_STORE_HOME` is unset. No DSN;
  `DSH_HOME` cleared. The hidden-session scenario is zero-I/O on `memstore` with a fake
  agent. Nothing skips.

### Scenarios

#### Feature: Typed machine state and stored folds

- **Scenario:** Machine state survives a restart
  ```gherkin
  @TC-08 @proof:service:sqlite @DH-D-STORAGE-1
  Given a workspace registered in a temp home
  When the store is closed and reopened
  Then the workspace row reads back with identical fields
  ```
- **Scenario:** A changed fold version rebuilds stored state
  ```gherkin
  @TC-08 @proof:service:sqlite @DH-D-VIEW-2
  Given a stored fold at version 1 over 20 events
  When the fold is re-registered at version 2 and the store opens
  Then the stored state is rebuilt, records version 2, and its sequence equals the log head
  ```
- **Scenario:** Stored fold state is never ahead of the log
  ```gherkin
  @TC-08 @proof:service:sqlite @DH-D-VIEW-2 @edge
  Given a re-exec'd child killed after appending events but before updating a stored fold
  When the store is reopened
  Then the stored fold's recorded sequence is at or below the log head
  And reading the fold returns state equal to a refold up to the log head
  ```

#### Feature: Built-in views, projects and listing

- **Scenario:** Refold equals stored for every built-in view
  ```gherkin
  @TC-08 @proof:service:sqlite @DH-D-VIEW-3
  Given a fixed 200-event session in a temp home
  When list summary, turn index, statistics and event index are refolded from the log
  Then each equals its stored state
  ```
- **Scenario:** Sessions list grouped by project
  ```gherkin
  @TC-08 @proof:golden @DH-D-STORAGE-7
  Given three sessions in two projects
  When sessions are listed by project
  Then the result equals session/views/testdata/list_by_project.golden
  ```
- **Scenario:** A hidden session stops working
  ```gherkin
  @TC-08 @proof:in-process @DH-D-STORAGE-7 @edge
  Given a session with an agent registered through the agent contract fake on memstore
  When the session is hidden
  Then the agent receives a stop with cause hidden and accepts no new inbox item
  ```
- **Scenario:** Follow and trace across a fork
  ```gherkin
  @TC-08 @proof:service:sqlite @DH-D-VIEW-5
  Given session P forked into child C at sequence 10
  When C is traced and followed
  Then the trace shows P's events up to 10 then C's own events
  And a new append to C reaches the follower in order
  ```

#### Feature: Spill store and attachments

- **Scenario:** A large result is spilled and fully recoverable
  ```gherkin
  @TC-08 @proof:service:filesystem @DH-D-STORAGE-5
  Given a 5 MB tool result
  When it is stored through the spill store
  Then the log holds a head no larger than the configured bound plus a spill reference
  And reading the reference returns all 5 MB byte-identical
  ```
- **Scenario:** A corrupted attachment is detected
  ```gherkin
  @TC-08 @proof:service:filesystem @DH-D-STORAGE-3 @edge
  Given an attachment stored in blobs/
  When one byte of the file is flipped on disk and it is read back
  Then the read fails with a digest-mismatch error
  ```
- **Scenario Outline:** Spill files are private to the owner
  ```gherkin
  @TC-08 @proof:service:filesystem @DH-D-STORAGE-5
  Given a spilled result on <os>
  When its permissions are read back
  Then <expectation>
  Examples:
    | os      | expectation                                                                       |
    | unix    | the file is mode 0600 inside a 0700 spill/                                        |
    | windows | the DACL is protected with ACEs only for the owner SID, SYSTEM and Administrators |
  ```
- **Scenario:** DH-S-8 scenarios are authored and indexed, not executed
  ```gherkin
  @TC-01b @proof:service:go-toolchain
  Given the scenario files for DH-E-STORAGE-1, DH-E-STORAGE-2 and DH-E-STORAGE-3
  When the index check and the scenario-tag vet run
  Then they sit under DH-S-19 (19.7), DH-S-23 (23.14) and DH-S-39 (39.8) respectively
  And the default gate runs none of them
  ```

---

# Phase 9: DeepSeek Provider Retry and Usage

Stage DH-S-9: C23 DeepSeek Messages provider + bounded retry, C24 usage fold
(DH-D-LLM-2,3,6). The `deepseek-official` route speaks the Anthropic Messages wire
format; the key reaches it only through C18 (secret store) and C19 (egress).

### Setup
- **Type**: inline
- **Local**: `go -C src/harness test -race -shuffle=on ./internal/llm/...`. Fuzz
  exploration (not a gate): `go -C src/harness test -fuzz FuzzSSE -fuzztime 60s ./internal/llm/...`.
  Manual check (`@manual`, impl.md DH-S-9): run the documented Go test with
  `DSH_DEEPSEEK_API_KEY` exported in the developer's own shell for the good-key run and
  a deliberately wrong value for the bad-key run; record both in
  `docs/stories/harness-DSH/evidence/DH-S-9.md`.
- **CI runner**: GitHub-hosted `ubuntu-latest`, `macos-latest`, `windows-latest`
  (`harness` job). The manual check never runs in CI.
- **Secrets**: none for the gate. The manual check reads `DSH_DEEPSEEK_API_KEY` from
  the developer's environment at run time; it is never committed, never put in a
  fixture, and never configured as a CI secret by this story.
- **Pre-test bootstrap**: none. SSE fixtures under `internal/llm/testdata/sse/` are
  hand-written from the vendor's published format and contain no real key.
- **Gate provisioning**: when `DSH_DEEPSEEK_API_KEY` is unset (always, in the gate)
  the provider is driven by the test kit's in-memory fake transport (TC-09a) or by
  an `httptest` server on `127.0.0.1:0` serving the same fixtures behind a
  `model-request` permit for that loopback host (TC-09b). The secret store resolves a
  fake test value. Retry waits run on the virtual clock. The real API is never a gate
  dependency, so no gate scenario can skip for its absence.

### Scenarios

#### Feature: The request that reaches the wire is the logged request

- **Scenario:** Bytes at the transport equal the rebuilt request
  ```gherkin
  @TC-09a @proof:in-process @DH-D-LLM-2
  Given the fake transport capturing request bytes
  When one attempt is made for a session
  Then the captured body equals surface.Rebuild at the settlement's requestAsOf
  And the settlement's requestDigest matches the digest of those bytes
  ```
- **Scenario:** Provider over loopback HTTP
  ```gherkin
  @TC-09b @proof:service:loopback-http @DH-D-LLM-2
  Given the success fixture served by httptest on 127.0.0.1:0 and a model-request permit for that host
  When the provider streams a reply
  Then one assistant/message is settled
  And the server saw the x-api-key header carrying the secret store's test value
  And the request body at the server equals surface.Rebuild at requestAsOf
  ```
- **Scenario:** Without a permit the provider never dials
  ```gherkin
  @TC-09b @proof:service:loopback-http @DH-D-UTIL-2 @edge
  Given the loopback server and no model-request permit
  When the provider attempts a call
  Then one settlement records egress/not-permitted and the server's connection count is 0
  ```

#### Feature: Decoding vendor streams into exactly one settlement

- **Scenario Outline:** Each fixture settles once with the expected kind
  ```gherkin
  @TC-09a @proof:golden @DH-T-LLM-5 @DH-T-LLM-11 @DH-T-LLM-16
  Given fixture <fixture> under internal/llm/testdata/sse/
  When it is fed through the driver in memory
  Then exactly one settlement of kind <kind> with failure class <class> is produced
  And the decoded message equals the committed golden for <fixture>
  Examples:
    | fixture                    | kind               | class       |
    | success                    | assistant/message  | none        |
    | overloaded-retry-after     | assistant/attempt  | overloaded  |
    | bad-key                    | assistant/attempt  | auth        |
    | mid-stream-drop            | assistant/attempt  | transport   |
  ```
- **Scenario:** An unknown vendor business code stays unknown
  ```gherkin
  @TC-09a @proof:in-process @DH-D-LLM-2 @edge
  Given an error body with a business code absent from the mapping table
  When it is decoded
  Then the failure class is unknown and the raw code is kept on the settlement
  ```
- **Scenario:** SSE fuzz seeds never panic
  ```gherkin
  @TC-09a @proof:in-process
  Given the FuzzSSE seeds registered with f.Add
  When the seeds run as a unit test
  Then every seed decodes or fails with a classified error and none panics
  ```
- **Scenario:** The key never appears in a log or settlement
  ```gherkin
  @TC-09a @proof:in-process @DH-D-HUMAN-7 @edge
  Given the bad-key fixture and a captured log exporter
  When the call fails
  Then the secret store's test value appears in no log record, settlement payload or error text
  ```

#### Feature: Bounded retry outside the adapter

- **Scenario:** Overloaded is retried within the bound
  ```gherkin
  @TC-09a @proof:in-process @DH-T-LLM-7
  Given overloaded-retry-after twice then success, on the virtual clock
  When the call runs
  Then three settlements are logged (two assistant/attempt, one assistant/message)
  And the waits equal the retry-after values and no fourth attempt occurs
  ```
- **Scenario:** A huge retry-after is capped
  ```gherkin
  @TC-09a @proof:in-process @DH-T-LLM-8 @edge
  Given overloaded with retry-after far above the cap
  When AttemptFailed decides
  Then the wait equals the cap, not the vendor value
  ```
- **Scenario:** A bad key is never retried and never falls back
  ```gherkin
  @TC-09a @proof:in-process @DH-D-LLM-3 @edge
  Given the bad-key fixture and a second route configured
  When the call runs
  Then exactly one settlement with class auth is logged
  And no other route is tried
  ```
- **Scenario:** Retry budget exhausted ends with the last failure
  ```gherkin
  @TC-09a @proof:in-process @DH-D-LLM-3 @edge
  Given overloaded on every attempt
  When the call runs to its retry bound
  Then the number of settlements equals the bound and the call returns the last overloaded failure
  ```
- **Scenario:** Buffered one-shot auxiliary call
  ```gherkin
  @TC-09a @proof:in-process @DH-D-LLM-3
  Given an auxiliary request built with RequestFrom(seq)
  When Complete runs against the success fixture
  Then the caller receives one buffered message and its AttemptSink one record naming seq
  ```

#### Feature: Usage folded from settlements

- **Scenario:** Totals and occupancy from settlements
  ```gherkin
  @TC-09a @proof:in-process @DH-T-LLM-15
  Given three settlements with known input and output token counts
  When the usage fold runs
  Then totals and context occupancy equal the expected values
  ```

#### Feature: Real-API check and authored scenarios

- **Scenario:** Real DeepSeek API, good key then bad key
  ```gherkin
  @manual @DH-D-LLM-2
  Given DSH_DEEPSEEK_API_KEY exported in the developer's shell and a model-request permit for the vendor host
  When one streamed request is made with the key, and one with a wrong value
  Then the first logs one assistant/message and the second one assistant/attempt with class auth
  And the bad-key run made no retry
  And both results are recorded in evidence/DH-S-9.md (never a gate criterion)
  ```
- **Scenario:** DH-S-9 scenarios are authored before DH-S-9 closes
  ```gherkin
  @TC-01b @proof:service:go-toolchain
  Given the files for DH-E-LLM-1, DH-E-LLM-3 and DH-E-VIEW-1 at the DH-S-9 closing commit
  When the index check and "go -C src/harness vet -tags scenario ./..." run
  Then each has one file and one DH-S-17 entry with authoring stage DH-S-9 and requires [DH-S-11, DH-S-16]
  And their imports are limited to src/sdk/..., the test kit's scenario launcher and the standard library
  And vet exits 0 and the default gate runs none of them
  ```

---

# Phase 10: Tool Pipeline Approvals and Human Commands

Stage DH-S-10: C25 tools, C26 approvals and grants, C28 inbox (DH-D-TOOLS-1,2,3,5;
DH-D-STORAGE-6; DH-D-AGENT-2; DH-D-HUMAN-2,3,5,6).

### Setup
- **Type**: compose
- **Compose file**: `tests/e2e/tool-pipeline-approvals-and-human-commands/docker-compose.yml`, starting
  `dsh-home-init` (one-shot container that creates and owns the `dsh-home` named
  volume) and `harness-store-tests` (the pinned Go toolchain image from
  `src/harness/go.mod` running `go -C src/harness test -race ./internal/tools/... ./internal/human/... ./internal/agent/...` with
  `DSH_E2E_STORE_HOME` set to the mounted volume). Run with the three-step
  compose run sequence above (init, then tests, then `down -v`).
- **Local**: `go -C src/harness test -race -shuffle=on ./internal/tools/... ./internal/human/... ./internal/agent/...`.
- **CI runner**: gate path on GitHub-hosted `ubuntu-latest`, `macos-latest`,
  `windows-latest` (`harness` job, required); compose path on GitHub-hosted
  `ubuntu-latest` (`harness-compose` job, non-gating, docker preinstalled on the image).
- **Secrets**: none.
- **Pre-test bootstrap**: none.
- **Gate provisioning**: TC-10a runs on the test kit with `memstore`, the virtual
  clock and a fake answerer (zero I/O). TC-10b is **unconditional**: the embedded
  SQLite store is always composed in a test-kit temp home, results over
  `maxResultBytes` spill into that home, and durability is checked from a second
  connection. `DSH_E2E_STORE_HOME` unset in the gate selects this embedded path;
  `DSH_HOME` cleared; nothing skips.

### Scenarios

#### Feature: Tool registry and deterministic prompt assembly

- **Scenario:** A duplicate tool name is refused in one scope
  ```gherkin
  @TC-10a @proof:in-process @DH-T-TOOLS-2
  Given a scope with tool echo
  When a second echo is registered in the same scope
  Then registration fails and the first echo remains
  And registering echo in a child scope shadows it in that subtree only
  ```
- **Scenario:** Prompt and tool list are byte-stable
  ```gherkin
  @TC-10a @proof:golden @DH-T-TOOLS-14 @DH-T-TOOLS-15
  Given five tools registered in a random order in each of 100 runs
  When the step prompt and tool list are assembled
  Then every run's bytes equal internal/tools/testdata/prompt.golden
  ```

#### Feature: The 8-stage guarded pipeline

- **Scenario:** Stages run in the fixed order
  ```gherkin
  @TC-10a @proof:in-process @DH-T-TOOLS-4
  Given recording stubs at every stage
  When one approved call runs
  Then the recorded order is resolve, validate, policy, guards, approval, flush, body, shape
  ```
- **Scenario Outline:** A call stopped early never reaches the body
  ```gherkin
  @TC-10a @proof:in-process @DH-T-TOOLS-3 @edge
  Given a step whose fixed ToolSet admits only echo and add(a:int,b:int)
  When the model calls <call>
  Then the call ends at stage <stage> with <result>
  And every later stage's stub records zero invocations
  And exactly one tool/result carries the refusal to the model
  Examples:
    | call                 | stage    | result              |
    | rm (not registered)  | resolve  | tools/not-started   |
    | grep (not admitted)  | resolve  | tools/not-started   |
    | add {a: "x"}         | validate | a validation error  |
  ```
- **Scenario:** Denial is enforced in the executor, not by hiding the tool
  ```gherkin
  @TC-10a @proof:in-process @DH-D-HUMAN-2
  Given a tool requiring approval, present in the prompt's tool list, and no answerer
  When the model calls it
  Then the approval outcome is no-answerer and an attributed audit event is logged
  And the body counter is 0 and the tool was still listed in the prompt
  ```
- **Scenario Outline:** Approval yields one closed outcome
  ```gherkin
  @TC-10a @proof:in-process @DH-D-HUMAN-2
  Given a tool requiring approval and a fake answerer that <behaves>
  When the model calls it
  Then the outcome is <outcome>, one audit event names the answerer, and the body ran <n> times
  Examples:
    | behaves                         | outcome      | n |
    | approves                        | approved     | 1 |
    | denies                          | denied       | 0 |
    | never answers (virtual timeout) | timeout      | 0 |
  ```
- **Scenario:** Logged arguments are never rewritten
  ```gherkin
  @TC-10a @proof:in-process @DH-D-TOOLS-2 @edge
  Given a policy listener that tries to change the call's arguments
  When the call runs
  Then the logged tool call equals the model's original arguments byte for byte
  ```
- **Scenario:** A deny-only guard can refuse but never allow
  ```gherkin
  @TC-10a @proof:in-process @DH-D-TOOLS-2 @edge
  Given a policy listener that denies and a guard that returns allow
  When the call runs
  Then the call is denied and the body counter is 0
  ```
- **Scenario:** The approval audit is durable before the body runs
  ```gherkin
  @TC-10b @proof:service:sqlite @DH-D-TOOLS-2
  Given the SQLite store in a temp home
  When an approved tool runs
  Then a second connection reads the approval audit row before the body's first instruction executes
  ```

#### Feature: Deadlines and the result budget

- **Scenario:** A body that ignores cancellation does not hold the turn
  ```gherkin
  @TC-10a @proof:in-process @DH-T-TOOLS-8 @DH-T-TOOLS-9
  Given a body ignoring cancellation, a 10 s deadline and 2 s grace on the virtual clock
  When the clock advances 12 s
  Then the call is answered timeout and the step continues
  ```
- **Scenario:** An oversized result is spilled before reaching the model
  ```gherkin
  @TC-10b @proof:service:sqlite @DH-D-STORAGE-6
  Given a tool returning 2 MB and maxResultBytes of 64 KB on the SQLite store
  When the result is shaped
  Then the logged result is within 64 KB with a spill reference
  And the spill file in the temp home holds all 2 MB
  ```
- **Scenario:** The token estimate also bounds a result
  ```gherkin
  @TC-10a @proof:in-process @DH-D-STORAGE-6 @edge
  Given a result under maxResultBytes but over the token budget from the usage estimate
  When it is shaped
  Then it is truncated to the token budget with a spill reference
  ```

#### Feature: Durable inbox, questions, human commands and grants

- **Scenario:** An inbox item is durable before Send returns
  ```gherkin
  @TC-10b @proof:service:sqlite @DH-D-AGENT-2
  Given the SQLite store in a temp home
  When Send returns for a message
  Then a second connection reads the inbox row at once
  ```
- **Scenario:** One accepted message runs at most once
  ```gherkin
  @TC-10a @proof:in-process @DH-T-AGENT-6 @edge
  Given an inbox item and a driver stub that crashes after taking it
  When the agent resumes
  Then the item is not run again and the log shows one turn for it
  ```
- **Scenario:** Input is taken only at boundaries
  ```gherkin
  @TC-10a @proof:in-process @DH-T-AGENT-4 @DH-T-AGENT-5
  Given an agent mid-step
  When a message is sent
  Then it is accepted at once but enters history only at the next step or turn boundary
  ```
- **Scenario:** A question answered through the inbox
  ```gherkin
  @TC-10a @proof:in-process @DH-D-HUMAN-5
  Given the model asks a question with wait
  When the fake answerer replies
  Then the reply enters the inbox and the waiting tool call returns the answer
  And both events are logged with attribution
  ```
- **Scenario:** A question without wait lets the turn move on
  ```gherkin
  @TC-10a @proof:in-process @DH-D-HUMAN-5 @edge
  Given the model asks a question without wait
  When the step continues and the answer arrives later
  Then the tool call returned immediately and the later answer arrives as a new inbox item
  ```
- **Scenario:** A human command is attributed and delivered through the inbox
  ```gherkin
  @TC-10a @proof:in-process @DH-D-HUMAN-6
  Given two clients addressing the same agent
  When client 2 sends a stop command
  Then it is delivered through the inbox and the logged command names client 2
  ```
- **Scenario Outline:** Grants are never copied
  ```gherkin
  @TC-10a @proof:in-process @DH-D-HUMAN-3 @edge
  Given a session grant of one directory, listed in the session's grants
  When the session is <action>
  Then the new session's grant list is empty and a call needing that directory asks again
  Examples:
    | action                    |
    | resumed                   |
    | forked                    |
    | used to start a subagent  |
  ```
- **Scenario:** A revoked grant is gone at once
  ```gherkin
  @TC-10a @proof:in-process @DH-D-HUMAN-3
  Given a listed session grant
  When it is revoked
  Then the list is empty and the next call needing it asks for approval
  ```
- **Scenario:** DH-S-10 scenarios are authored and indexed, not executed
  ```gherkin
  @TC-01b @proof:service:go-toolchain
  Given the files for DH-E-TOOLS-1, DH-E-TOOLS-3 and DH-E-HUMAN-6
  When the index check and the scenario-tag vet run
  Then they sit under DH-S-17 (17.5), DH-S-39 (39.7) and DH-S-22 (22.10) respectively
  And the default gate runs none of them
  ```

---

# Phase 11: Agent Loop Milestone M1

Stage DH-S-11: C27 driver, C29 delegation seam, C30 context pipeline
(DH-D-AGENT-3,5,6,7,10; DH-D-SUBAGENT-1; DH-D-CONTEXT-1). M1 is reached here: "an agent
runs turns against a real model with one tool, every step logged".

### Setup
- **Type**: compose
- **Compose file**: `tests/e2e/agent-loop-milestone-m1/docker-compose.yml`, starting
  `dsh-home-init` (one-shot container that creates and owns the `dsh-home` named
  volume) and `harness-store-tests` (the pinned Go toolchain image from
  `src/harness/go.mod` running `go -C src/harness test -race ./internal/agent/... ./internal/delegate/... ./internal/context/...` with
  `DSH_E2E_STORE_HOME` set to the mounted volume). Run with the three-step
  compose run sequence above (init, then tests, then `down -v`).
- **Local**: `go -C src/harness test -race -shuffle=on ./internal/agent/... ./internal/delegate/... ./internal/context/...`;
  the full gate from Phase 1 for story closure. M1 manual check (`@manual`): run the
  documented Go test with `DSH_DEEPSEEK_API_KEY` exported in the developer's shell.
- **CI runner**: gate path on GitHub-hosted `ubuntu-latest`, `macos-latest`,
  `windows-latest` (`harness` job, required); compose path on GitHub-hosted
  `ubuntu-latest` (`harness-compose` job, non-gating, docker preinstalled on the image). The M1 manual check never runs in CI.
- **Secrets**: none for the gate; the manual check reads `DSH_DEEPSEEK_API_KEY` from
  the developer's environment only.
- **Pre-test bootstrap**: none.
- **Gate provisioning**: TC-11a uses the test kit's scripted fake model, `memstore`
  and the virtual clock (zero I/O). TC-11b composes the embedded SQLite store in a
  temp home and reads checkpoint order from database rows. With
  `DSH_DEEPSEEK_API_KEY` unset (always, in the gate) the model is scripted; the real
  model is a manual check only. `DSH_E2E_STORE_HOME` unset in the gate selects the
  embedded store; nothing skips.

### Scenarios

#### Feature: Model selection and the bounded step batch

- **Scenario:** Each step logs the model it uses before the attempt
  ```gherkin
  @TC-11a @proof:in-process @DH-T-AGENT-23
  Given an agent whose default model changes between steps
  When two steps run
  Then each step/start is followed by a selection event naming the model
  And each settlement's route matches its step's selection
  ```
- **Scenario:** Conflicting calls serialize and results keep model order
  ```gherkin
  @TC-11a @proof:in-process @DH-T-AGENT-26
  Given three calls where 1 and 3 share a resource key and call 2 finishes first
  When the batch runs with maxParallel = 3
  Then calls 1 and 3 never overlap and results commit in order 1, 2, 3
  ```
- **Scenario:** maxParallel caps concurrency
  ```gherkin
  @TC-11a @proof:in-process @DH-T-AGENT-26 @edge
  Given five non-conflicting calls and maxParallel = 2
  When the batch runs
  Then at most two bodies run at any instant and all five results commit in model order
  ```
- **Scenario:** The tool set is fixed at step start
  ```gherkin
  @TC-11a @proof:in-process @DH-D-AGENT-10 @edge
  Given a step that has started with ToolSet {echo}
  When a tool add is registered mid-step and the model calls it
  Then the call ends tools/not-started; add is available from the next step
  ```

#### Feature: Driver brackets, budgets and cancellation

- **Scenario:** No tool call is left unanswered on any exit path
  ```gherkin
  @TC-11a @proof:in-process @DH-D-AGENT-3
  Given random scripts of model replies with tool calls and a random exit (cancel, budget, provider error, lease loss)
  When 500 property cases run
  Then every history fold has a result for every tool call and all brackets balance
  ```
- **Scenario:** A step budget closes the turn
  ```gherkin
  @TC-11a @proof:in-process @DH-T-AGENT-8 @edge
  Given a step budget of 3 and a model that always calls a tool
  When the turn runs
  Then exactly 3 steps run and turn/end records cause budget
  ```
- **Scenario:** Cancel a step and keep the inbox
  ```gherkin
  @TC-11a @proof:in-process @DH-T-AGENT-16
  Given a running turn in step 2, one user message parked in the inbox, and a hook at the continue-or-stop point
  When Cancel(CauseUser, Scope: Step, Inbox: Keep) is called
  Then step 2's attempt settles cancelled and step/end records cause user
  And no turn/end is written by the cancel and the hook fires once
  And the driver opens step 3 of the same turn with the parked message
  ```
- **Scenario:** Cancel a step with an empty inbox ends the turn normally
  ```gherkin
  @TC-11a @proof:in-process @DH-T-AGENT-16 @edge
  Given the same setup with an empty inbox and a scripted final answer
  When the step is cancelled
  Then the continue-or-stop point is reached and the turn ends completed without a step 3
  ```
- **Scenario:** Cancel a turn and discard the inbox
  ```gherkin
  @TC-11a @proof:in-process @DH-T-AGENT-17 @DH-T-AGENT-18
  Given a running turn and two parked inbox messages
  When Cancel(CauseUser, Scope: Turn, Inbox: Discard) is called
  Then step/end and turn/end record cause user, both messages are marked discarded
  And no further turn starts
  ```
- **Scenario:** Lease loss mid-turn closes honestly
  ```gherkin
  @TC-11b @proof:service:sqlite @DH-D-AGENT-3 @edge
  Given an agent mid-step on the SQLite store and a second re-exec'd process forcing takeover
  When the driver's next append is rejected for a stale epoch
  Then the driver stops without further appends and the new holder's recovery closes the brackets as interrupted
  ```

#### Feature: Agent lifecycle and the delegation seam

- **Scenario:** A failed create leaves nothing
  ```gherkin
  @TC-11a @proof:in-process @DH-T-AGENT-19
  Given agent creation whose third registration fails
  When creation runs
  Then no agent is addressable, no partial event is visible and the first two registrations were rolled back
  ```
- **Scenario:** A child is torn down with its owner
  ```gherkin
  @TC-11a @proof:in-process @DH-D-SUBAGENT-1
  Given component C delegating to child agent K through the seam
  When C is unloaded
  Then K's driver goroutine has exited before C's Wait returns and K had no other owner
  ```
- **Scenario:** Shutdown keeps the last events
  ```gherkin
  @TC-11b @proof:service:sqlite @DH-T-AGENT-21
  Given an agent mid-step on the SQLite store
  When the host shuts down
  Then on reopen the log ends with closed brackets and the last settlement is present
  ```

#### Feature: Context pipeline and flow F2 (M1)

- **Scenario:** Context reaches the model through one logged, ordered pipeline
  ```gherkin
  @TC-11a @proof:in-process @DH-D-CONTEXT-1
  Given three context contributors registered in a known order
  When NextRequest is built for a step
  Then the contributions appear in that order, each backed by a logged event
  And Digest() equals the digest of surface.Rebuild at that request's requestAsOf
  ```
- **Scenario:** Flow F2 bracketed log matches the keyless golden
  ```gherkin
  @TC-11a @proof:golden @DH-D-AGENT-3
  Given the scripted model returning one tool call then a final reply, and the fake answerer approving
  When one turn runs on memstore
  Then the serialized log equals internal/agent/testdata/f2_log.golden
  ```
- **Scenario:** Flow F2 on SQLite checkpoints before every I/O
  ```gherkin
  @TC-11b @proof:service:sqlite @DH-D-STORE-4
  Given flow F2 against the SQLite store in a temp home
  When the turn completes
  Then the database rows show a durable flush before the provider attempt, before the tool body and at step end
  And after the store is closed and reopened, the serialized log read back equals internal/agent/testdata/f2_log.golden byte for byte
  ```
- **Scenario:** M1 against the real DeepSeek API
  ```gherkin
  @manual
  Given DSH_DEEPSEEK_API_KEY exported in the developer's shell and a Go test composing the real components with one test tool
  When one prompt runs to completion, and a second run is cancelled mid-stream
  Then the first run's bracketed log and rebuilt request are recorded in evidence/DH-S-11.md
  And the second run's turn/end records the cancel cause
  And neither run is a gate criterion
  ```
- **Scenario:** Every DH-E scenario of DH-S-1..11 has a file and an index entry
  ```gherkin
  @TC-01b @proof:service:go-toolchain
  Given DH-E-SESSION-1, DH-E-SESSION-2, DH-E-AGENT-1..4 and DH-E-TOOLCHAIN-1 authored in this phase
  When the index check and "go -C src/harness vet -tags scenario ./..." run
  Then each sits under DH-S-17 with exactly one file
  And every DH-E scenario listed in this document's schedule table has a file and one index entry
  And vet exits 0 and the default gate runs none of them
  ```
- **Scenario:** Story closure at one commit
  ```gherkin
  @TC-X1 @proof:service:go-toolchain
  Given the DH-S-11 closing commit
  When the gate runs on Linux, macOS and Windows
  Then every gate passes on all three lanes
  And eleven evidence records exist, M1 is recorded, and H-21 is listed open
  ```
