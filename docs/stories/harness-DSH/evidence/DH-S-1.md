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

```text
$ go -C src/harness test -race -shuffle=on -count=1 -cover ./internal/testkit/golden
ok  github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden  coverage: 100.0% of statements
$ GOWORK=off go -C tools/harness-gate test -count=1 ./...
ok  (see the command log of the stage-1.3 PR)
```