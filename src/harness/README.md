# dsh harness

`src/harness` is the harness product module
(`github.com/smartpcr/agent-plugin/src/harness`). It is one of exactly three product
modules (RM-1), all on the same `go 1.26.0` floor (RM-2):

| Module | Holds | Requires |
| --- | --- | --- |
| `src/go` | cordis, loader, timer | nothing |
| `src/sdk` | jsonrpc, protocol | nothing (`DH-T-SDK-17`) |
| `src/harness` | `cmd/dsh` and `internal/...` | `src/go`, `src/sdk` -- never through `replace` (RM-4) |

`cmd/dsh` holds `main` only; every implementation package lives under `internal/`
(RM-5).

## Development: the root `go.work`

The repository root has a `go.work` that uses `./src/go`, `./src/sdk` and
`./src/harness`, so local edits to one module are seen by the others without a
`replace` directive. There is no module at the root, so build with module-directory
patterns (a bare `./...` at the root matches nothing):

```bash
go build ./src/go/... ./src/sdk/... ./src/harness/...
go vet   ./src/go/... ./src/sdk/... ./src/harness/...
```

Modules outside the workspace (`tools/harness-gate`, `tools/harness-trace`,
`test/e2e/bindings/go`) are run with `GOWORK=off`.

## Release and rehearsal builds: `GOWORK=off`

The workspace is for development only. Release and rehearsal builds always run with
`GOWORK=off` (DH-ADR-26, DH-ADR-TOOLCHAIN-11), so `src/harness` resolves `src/go` and
`src/sdk` exactly as its own `go.mod` pins them:

```bash
GOWORK=off go -C src/harness build -o dsh ./cmd/dsh
GOWORK=off go -C src/harness build -ldflags "-X main.version=v0.1.0" -o dsh ./cmd/dsh
```

`dsh` prints `dsh <version>` and exits 0. The version is the one set with
`-X main.version`, else the module version the go command recorded, else `(devel)`.

## Pins

`src/harness/go.mod` pins `src/go` and `src/sdk` at pseudo-versions of a real commit
(today `a647d4984790`, the first commit containing `src/sdk`), recorded in `go.sum`.
Re-pin with:

```bash
GOWORK=off go -C src/harness get github.com/smartpcr/agent-plugin/src/go@<commit> github.com/smartpcr/agent-plugin/src/sdk@<commit>
```

Until `internal/` imports both modules, `go mod tidy` would drop the requirements, so
do not run it here; `DH-T-TOOLCHAIN-11` fails on a missing or placeholder pin.

`github.com/cucumber/godog` is required only by the `e2e`-tagged acceptance suite
under `test/e2e/harness-DSH` (`go -C src/harness test -tags e2e ./test/...`); no
package of `cmd/dsh` or `internal/` imports it.

## Module pin tests

`DH-T-TOOLCHAIN-11` (three modules, one `go` floor, no `replace`) and `DH-T-SDK-17`
(`src/sdk` requires nothing) live in `tools/harness-gate/modcheck`, together with the
workspace and release build scenarios:

```bash
GOWORK=off go -C tools/harness-gate test ./modcheck
```
