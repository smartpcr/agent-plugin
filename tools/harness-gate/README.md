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
| 3 | `test` | `go test -race -shuffle=on -count=1 ./...` per module with `CGO_ENABLED=1` (QG-4) |
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

The same binary is the vet tool: started with `HARNESS_GATE_VETTOOL=1` it runs
`unitchecker.Main` over the analyzers. Their fixtures live in
`analyzers/testdata/src` and are checked with `analysistest`.

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

The coverage gate runs, per module,

```sh
go test -cover -covermode=set -coverpkg=./... -count=1 -skip '^TestBudget' -coverprofile=<tmp> ./...
```

`go test -cover` gives every test process a `GOCOVERDIR`, inherited by every child
the test starts. The go command merges each such directory (the `go tool covdata`
format) into the `-coverprofile`; `harness-gate` then merges the per-package
sections block by block and applies the 85% floor to every package. With
`-coverpkg=./...` a package without tests appears at 0%, so none escapes the floor.

A test that re-executes its own binary as a child must run the child through
`m.Run` and pass `-test.gocoverdir=` + `os.Getenv("GOCOVERDIR")`, so the child's
counters land in the same directory; a separately built helper binary must be
built with `-cover`. Without that the child's statements count as uncovered.

## Performance budgets (QG-7)

Budget tests are named `TestBudget*` and their budgets are constants compiled into
the test. The test and coverage gates skip them; the `budget` gate runs them alone
with every `DSH_*` and `GO*` variable removed from the child environment (only the
locations `GOROOT`, `GOPATH`, `GOMODCACHE`, `GOCACHE`, `GOTMPDIR` are kept, and
`GOWORK=off`, `GOTOOLCHAIN=local` are set), so no override can relax a budget.

## Seeded-violation modules (`testdata/`)

| module | fails at |
|--------|----------|
| `race-seeded` | `test` (a data race) |
| `unformatted-file` | `gofmt` (`add.go` is deliberately unformatted) |
| `coverage-40pct` | `coverage` (`example.com/cov40/p` at 40%) |
| `budget-exceeded` | `budget` (`TestBudgetX` honours `DSH_BUDGET_RELAX` unless cleared) |
| `cross-broken` | `cross-compile` (`windows/arm64` only) |

`gates_test.go` copies each into `t.TempDir()`, runs a freshly built
`harness-gate` against it and checks the named gate, the non-zero exit and that no
later gate started.
