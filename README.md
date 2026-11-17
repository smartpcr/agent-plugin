# agent-plugin

Plugin runtimes for agent hosts: a dynamic dependency-injection model where plugins declare
the services they need, load when those services appear, and unload when they go away.

## Implementations

| Path | Language | Description |
| --- | --- | --- |
| [src/net](src/net/README.md) | C# / .NET 10 | Cordis.NET, a port of [cordiverse/cordis](https://github.com/cordiverse/cordis), with samples and tests |

## Quick start (.NET)

```bash
cd src/net
dotnet build Cordis.sln
dotnet run --project tests/Cordis.Tests
dotnet run --project samples/Cordis.Sample
```
