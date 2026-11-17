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
