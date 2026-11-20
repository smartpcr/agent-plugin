# harness-gate

`harness-gate` is the single quality gate of the repository (QG-1,
DH-ADR-TOOLCHAIN-9). It runs one fixed, ordered list of gates, stops at the first
failure, prints that gate's name and exits non-zero. The same command decides
"this commit passes" on a laptop and in CI:

```sh
GOWORK=off go -C tools/harness-gate run . --root ../..
```

`--root` is the repository root (the gates then run on the product modules
`src/go`, `src/sdk`, `src/harness`, RM-1) or a directory holding a `go.mod` (the
gates then run on that one module; the seeded modules of `testdata/` are run this
way). Exit status: `0` every gate passed, `1` a gate failed, `2` usage or setup
error.

The module has its own `go.mod` and is not part of the root `go.work`; run it with
`GOWORK=off` (RM-2).

## Gates, in order

| # | gate | what fails it |
|---|------|---------------|
| 1 | `gofmt` | any `.go` file not `gofmt`-formatted (`testdata/`, `vendor/`, nested modules skipped) |
| 2 | `vet` | `go vet ./...`, then the own analyzers through `go vet -vettool=<harness-gate>` (QG-2) |
| 3 | `test` | `go test -tags= -race -shuffle=on -count=1 ./...` per module with `CGO_ENABLED=1` (QG-4); the `replay` and `scenario` layers are excluded |
| 4 | `coverage` | any package under 85% of statements, profiles merged across processes (QG-3) |
| 5 | `trace` | `tools/harness-trace verify` (QG-9); a root without the tool fails |
| 6 | `budget` | a `TestBudget*` test over its compiled-in budget (QG-7) |
| 7 | `cross-compile` | `GOWORK=off CGO_ENABLED=0 go build ./cmd/dsh` for linux, darwin, windows × amd64, arm64 (QG-8) |

There is no "skipped" state. Nothing reads a baseline, suppression or exemption
file: a finding is fixed in the code, not listed away (QG-2, QG-3, DH-ADR-14).

## Own analyzers (`analyzers/`)

| analyzer | rule |
|----------|------|
| `nosleep` | no reference to `time.Sleep` in a `_test.go` file |
| `onespawn` | no `os/exec` import outside `internal/world/subprocess` (SP-12); `_test.go` files are exempt |
| `cleanup` | a test that starts a goroutine, a listener (`net.Listen*`, `httptest.New*Server`) or a child process (`os.StartProcess`, `(*exec.Cmd).Start`) registers `t.Cleanup` (QG-5) |
| `fuzzcheck` | a package with a `//dsh:untrusted-parser` line in a non-test file has a `func FuzzXxx(f *testing.F)` in its own test files, a `seeds_test.go` (no `os` / `io/ioutil` import), every target seeds the `f` it receives through `f.Add` in `seeds_test.go` (directly or via the seeded parameter of a helper there), and every `f.Add` argument is compiled in -- literals, embed bytes or `seeds_test.go` data, never a call such as `readSeed()` (QG-6, DH-ADR-TOOLCHAIN-3; rules in `analyzers/fuzzseeds.go` and `src/harness/internal/testkit/README.md`) |

The same binary is the vet tool: started with `HARNESS_GATE_VETTOOL=1` it runs
`unitchecker.Main` over the analyzers. Their fixtures live in
`analyzers/testdata/src` and are checked with `analysistest`.

`fuzzcheck` decides over a whole package: `go vet` hands it a package together
with its in-package `_test.go` files. A loader that also yields the package without
its tests (`go/packages` with `Tests`, as `analysistest` uses) is skipped for that
variant when test files of the package exist on disk, and the test variant is
judged instead. `TestFuzzCheckRunsInVetGate` proves the rule through the real
`go vet -vettool` path.

## Test layers and build tags (DH-ADR-TOOLCHAIN-4)

The `replay` and `scenario` build tags (`LayerTags`) mark the layers the keyless
gate never runs: `*_replay_test.go` and `*_scenario_test.go` files carry
`//go:build replay` / `//go:build scenario`. The `test` and `coverage` gates (and
the `go list` the coverage gate plans with) pass `-tags=`, an explicit empty tag
list that overrides any `-tags` in `GOFLAGS`, so neither layer can run in, fail or
pass the default gate. `testdata/layer-tagged` holds an always-failing test of each
layer; the gate passes on it, and `go test -tags scenario` (or `replay`) fails.

## Race detector (QG-4)

`-race` needs cgo and so a C compiler (`go env CC` on `PATH`; on Windows e.g.
MSYS2 UCRT64 `gcc`, EN-6). The test gate checks for it first; a lane where `-race`
cannot run is a **failure** of the `test` gate, never a skip.

## Virtual time in tests (`testing/synctest`)

Tests never wait on the wall clock — the `nosleep` analyzer rejects `time.Sleep`
in test files. Code that waits on timers, tickers or timeouts is tested inside a
`testing/synctest` bubble, where time is virtual and advances only when every
goroutine of the bubble is durably blocked:

```go
func TestRetryBacksOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- retry(ctx, time.Second) }()
		synctest.Wait() // every goroutine of the bubble is blocked on virtual time
		if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
}
```

The convention:

- Wrap the test body in `synctest.Test(t, func(t *testing.T) { … })`.
- Advance time by blocking: let the code under test wait on its own timer, then
  call `synctest.Wait()` to block until every goroutine of the bubble is idle, and
  assert. A whole hour of timeouts runs in microseconds and deterministically.
- Never `time.Sleep` to "give a goroutine a chance"; wait on an explicit signal
  (channel, `sync.WaitGroup`, `synctest.Wait`).
- Everything a bubble starts must finish inside it; register `t.Cleanup` for what
  outlives a step (the `cleanup` analyzer).

## Coverage across processes (QG-3)

The coverage gate owns one `GOCOVERDIR` per module and, for every package with
tests, runs

```sh
go test -c -tags= -cover -covermode=set -coverpkg=./... -o <tmp>/bin/pkgN.test <pkg>
GOCOVERDIR=<tmp>/covdata <tmp>/bin/pkgN.test -test.count=1 -test.timeout=10m -test.skip='^TestBudget' -test.gocoverdir=<tmp>/covdata   # in the package directory
```

then merges everything in that directory with

```sh
go tool covdata textfmt -i=<tmp>/covdata -o=<tmp>/covdata.out
```

`covdata` joins the profiles of *different* binaries package by package — the test
binary, every re-executed copy of it and every separately built `-cover` helper.
(`go test -cover`'s own report keeps only the test binary's profiles, so a helper
binary's statements would be lost.) A package without test files gets its 0%
entries from `go test -cover -coverprofile`, so none escapes the floor. Every
package under 85% of statements fails the gate; there is no exemption list.

A test binary run directly has no default timeout (only `go test` applies one), so
the gate passes `-test.timeout` (`Config.TestTimeout`, default 10m, `go test`'s own
default) and backs it with a process deadline one minute later. A blocked test
fails the `coverage` gate naming the test that was running
(`test timed out: TestBlocked`, `testdata/coverage-blocked`) instead of hanging the
runner.

Children every test process starts inherit `GOCOVERDIR`. For their counters to be
collected:

- a test that re-executes its own binary runs the child through `m.Run` and passes
  `-test.gocoverdir=` + `os.Getenv("GOCOVERDIR")` (a test binary writes no counters
  on a bare `os.Exit`);
- a separately built helper binary is built with `go build -cover -covermode=set`
  (the mode must match for `covdata` to merge) and writes its counters on exit.

`testdata/coverage-child` exercises both: its package reaches 100% only with both
children merged, and 70% without either (`TestCoverageMergesChildProcesses`).

## Performance budgets (QG-7)

Budget tests are named `TestBudget*` and their budgets are constants compiled into
the test. The `test` gate runs them under `-race` with every other test, so a race
in a budget test fails there (`testdata/budget-race`); a budget test must therefore
also pass under the race detector (skip only the *measurement* assertion when
built with `-race`, e.g. through a `//go:build race` constant, never the code
path). The `coverage` gate leaves them out. The `budget` gate runs them alone,
uninstrumented, with every `DSH_*` and `GO*` variable removed from the child
environment (only the locations `GOROOT`, `GOPATH`, `GOMODCACHE`, `GOCACHE`,
`GOTMPDIR` are kept, and `GOWORK=off`, `GOTOOLCHAIN=local` are set), so no
override can relax a budget.

## Seeded-violation modules (`testdata/`)

| module | fails at |
|--------|----------|
| `race-seeded` | `test` (a data race) |
| `budget-race` | `test` (a data race inside `TestBudgetRacy`) |
| `unformatted-file` | `gofmt` (`add.go` is deliberately unformatted) |
| `coverage-40pct` | `coverage` (`example.com/cov40/p` at 40%) |
| `coverage-blocked` | `coverage` (`TestBlocked` never returns; the binary's `-test.timeout` fires) |
| `coverage-child` | passes `coverage` only with both child processes' profiles merged |
| `budget-exceeded` | `budget` (`TestBudgetX` honours `DSH_BUDGET_RELAX` unless cleared) |
| `cross-broken` | `cross-compile` (`windows/arm64` only) |
| `layer-tagged` | passes `test` and `coverage`: its always-failing `replay` and `scenario` tests are excluded |

`gates_test.go` copies each into `t.TempDir()`, runs a freshly built
`harness-gate` against it and checks the named gate, the non-zero exit and that no
later gate started.
