# DH-S-1 evidence

## Stage 1.1 -- Three Module Workspace (manual check)

| Field | Value |
| --- | --- |
| Kind | `manual` |
| Commit | Working tree of branch `ws/harness-DSH/phase-repository-modules-and-quality-gates-stage-three-module-workspace` on top of `332b0731135d2b8a3bc02714abf4d61b45876aa6`; the stage-1.1 commit SHA is the one that adds this file |
| Toolchain | `go version go1.27.4 windows/amd64` |
| Date | 2026-11-19 |

From the repository root (no root module; `go.work` selects the three modules):

```text
$ go build ./src/go/... ./src/sdk/... ./src/harness/...
(exit 0)
$ go list -m
github.com/smartpcr/agent-plugin/src/go
github.com/smartpcr/agent-plugin/src/sdk
github.com/smartpcr/agent-plugin/src/harness
$ GOWORK=off go -C src/harness build ./cmd/dsh
(exit 0)
```

Automated counterparts, run with `GOWORK=off go -C tools/harness-gate test ./modcheck`:
`TestWorkspaceBuildsThreeModules`, `TestReleaseBuildIgnoresWorkspace`, and the
`DH-T-TOOLCHAIN-11` / `DH-T-SDK-17` rule tests. The TC-02b golden
(`src/go/cmd/cordis-sample/testdata/output.golden`) is committed in the same stage,
before any DH-S-2 edit, and passes `go -C src/go test -count=20 ./cmd/cordis-sample`.
