# agent-plugin

Plugin runtimes for agent hosts: a dynamic dependency-injection model where plugins declare
the services they need, load when those services appear, and unload when they go away.

## Implementations

| Path | Language | Description |
| --- | --- | --- |
| [src/net](src/net/README.md) | C# / .NET 10 | Cordis.NET, a port of [cordiverse/cordis](https://github.com/cordiverse/cordis), with samples and tests |
| [src/go](src/go/README.md) | Go 1.24+ | cordis for Go, a port of the same upstream revision, with a sample and tests |

## Design docs

| Document | What it covers |
|---|---|
| [docs/architecture.md](docs/architecture.md) | Why this framework, how it is implemented, how to use it, benefits, and decision records with pros and cons |
| [docs/usage.md](docs/usage.md) | Using the framework for agentic coding: a lead agent that orchestrates, spawns and manages subagents, with the kmap workflow as the worked example (design, not built) |

## Quick start (.NET)

```bash
cd src/net
dotnet build Cordis.sln
dotnet run --project tests/Cordis.Tests
dotnet run --project samples/Cordis.Sample
```

## Quick start (Go)

```bash
cd src/go
go test -race ./...
go run ./cmd/cordis-sample
```

## Releasing

Push one tag, `vX.Y.Z` (or `vX.Y.Z-suffix` for a prerelease), and
[release.yml](.github/workflows/release.yml) releases both ports at that version once CI passes
on the tagged commit:

| Port | Published as |
| --- | --- |
| Go | the module tag `src/go/vX.Y.Z` (`go get github.com/smartpcr/agent-plugin/src/go@vX.Y.Z`), and a GitHub release made by [GoReleaser](.goreleaser.yaml) with the sample program for each platform |
| .NET | `Smartpcr.AgentPlugin`, `Smartpcr.AgentPlugin.Timer` and `Smartpcr.AgentPlugin.Loader` on nuget.org, also attached to the GitHub release |

The workflow needs the repository secret `NUGET_API_KEY`. Go versions must keep increasing past
the existing `src/go/v0.1.x` tags. The e2e Go binding pins a released `src/go`; bump its
`go.mod` after a release to test the new version.

## License

[MIT](LICENSE). Both ports are derived from [cordiverse/cordis](https://github.com/cordiverse/cordis),
also MIT, whose copyright notice is kept in [LICENSE](LICENSE).
