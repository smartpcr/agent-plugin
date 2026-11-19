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
