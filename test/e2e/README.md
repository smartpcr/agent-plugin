# Behaviour suites: cordis as its production users rely on it

These BDD suites describe what two production systems built on cordis expect
from the framework, written so that **one suite runs against both ports** in
this repository (the .NET port in `src/net`, binding `csharp`; the Go port in
`src/go`, binding `go`). They follow the
[bdd-code-extension](https://github.com/smartpcr/bdd-code-extension) suite
format: YAML cases of Given/When/Then steps, each step naming an action
declared in [schema/actions.catalog.yaml](schema/actions.catalog.yaml).

| Source system | What it is | How it uses cordis |
|---|---|---|
| [deepseek-ai/deepseek-harness](https://github.com/deepseek-ai/deepseek-harness) | An agent harness where "everything is a plugin" (vendored cordis 4.0.0-rc.12) | About 300 components; services such as `agents`, `tools`, `llm`, `mcpManager`; agent scopes as child fibers; waterfall events that veto tool calls; a loader with layered patch files; MCP clients mounted per server |
| [deepseek-ai/dsh-superagent-prototype](https://github.com/deepseek-ai/dsh-superagent-prototype) | A proactive coding agent shipped as a harness bundle | A main plugin injecting eleven host services and providing `orchestrator`; source plugins that await their watch loops on dispose; preset groups with isolate realms; a fiber that restarts itself after a settings save |

Both were read at their default branch on 2026-11-17 (deepseek-harness
`39163dd3`, dsh-superagent `8c32a9d`). Each case's `description` cites the
file that motivates it; tags name the source (`deepseek-harness`,
`dsh-superagent`) and the outcome it proves (`AR-O-n`, from
[docs/architecture.md](../../docs/architecture.md#32-outcomes)).

## Status

| Part | Status |
|---|---|
| Scenarios and catalog | **Authored.** 49 cases (52 report entries, counting the four rows of the timer outline) in 8 suites; `lint --strict` and `lint --binding-check` are clean. |
| C# binding (`csharp`) | **Built.** `bindings/dotnet`, on the .NET port in `src/net` (project references) and `Smartpcr.BddSuite` 0.8.1. |
| Go binding (`go`) | **Built.** `bindings/go`, on the Go port in `src/go` (a `replace` directive) and the bdd-suite Go module v0.8.1. |
| Runs (2026-11-17, local, Windows) | **52 of 52 entries pass in both bindings**, in each of three consecutive runs per binding. The Go result was obtained against the working-tree `src/go` (a temporary `-modfile` with a `replace`), because `Lookup` is not yet in the published `src/go/v0.1.0` that `bindings/go/go.mod` requires. |

These runs are local, in-process evidence: the suites drive the framework
itself, and nothing here needs a lab or a network.

## Layout

| Path | What it is |
|---|---|
| `schema/actions.catalog.yaml` | The actions: a probe host (one cordis tree per case) and configurable probe components that record an ordered trace. The probe's keys and every trace marker are documented at the top of the file. |
| `schema/suite.schema.json`, `suite.schema.md` | Generated from the catalog and the bundled core; do not edit. |
| `suites/*.yml` | The suites (below). |
| `bindings/dotnet/` | The C# binding, one type per file under `Suite.Cli/`: `Actions.cs` (the registrations) and `CordisExtension.cs`; `ProbeSpec.cs`, `ProbeSchema.cs`, `ProbeBody.cs`, `ProbeImplementation.cs`, `ProbeComponent.cs`, `ProbeHandle.cs` and `ProbeResolver.cs` (the probe component); `ProbeHost.cs` and `HostTable.cs` (one tree per case, keyed by suite and case); `TraceLog.cs`, `Args.cs`. It follows src/net's code style: `Directory.Build.props` reuses src/net's `stylecop.json` and test ruleset, `.editorconfig` is a copy of src/net's, and a violation fails the build. `Get-BddSuitePackage.ps1` downloads `Smartpcr.BddSuite` from the GitHub release into `feed/`, because it is not on nuget.org. |
| `bindings/go/` | The Go binding: `actions/actions.go` (the registrations), `probe/` (the probe and the host; one host per run, since Go actions carry no case identity and cases run one at a time), `cmd/suiterun`. |
| `Invoke-E2E.ps1` | Builds both bindings and runs every suite with each; results in `artifacts/e2e/*.xml`. |

The layout is the extension's default (`test/e2e/schema`, `test/e2e/**/*.yml`,
`bindings/dotnet/Suite.Cli/Actions.cs`, `bindings/go/actions/actions.go`), so
the extension activates, navigates and runs with no settings.

## Run

```powershell
pwsh -File test/e2e/Invoke-E2E.ps1                       # both bindings
pwsh -File test/e2e/Invoke-E2E.ps1 -Binding go -Name 'a-policy*'
```


## The suites

| Suite | Outcomes | What it pins down |
|---|---|---|
| [dependency-gating.yml](suites/dependency-gating.yml) | AR-O-1, AR-O-4 | Pending until every required service exists; reload when a provider returns; optional scopes that follow their service; optional lookups that never fail; undeclared access rejected |
| [async-activation.yml](suites/async-activation.yml) | AR-O-1, AR-O-2 | Dependents wait for async init; failed init releases what it acquired; disposal mid-activation closes what was opened; failures stay local; a failed start can be retried |
| [teardown-order.yml](suites/teardown-order.yml) | AR-O-2, AR-O-3 | A dependent's cleanup sees its provider open; registrations undone and redone across a restart; reverse-order effects; children before their manager; cancelling one subagent; leaves-first, exactly-once host shutdown |
| [realms.yml](suites/realms.yml) | AR-O-4 | Isolated groups each with their own `shell`; a duplicate provider fails while the first keeps serving; the same name in separate realms; no leak to the root |
| [reconfiguration.yml](suites/reconfiguration.yml) | AR-O-5 | In-place (volatile-style) updates; remount on ordinary updates; schema rejection keeps the instance; self-restart ordering; the latest of concurrent updates wins; sticky failure and explicit retry; implementation replacement |
| [events.yml](suites/events.yml) | AR-O-7 | Waterfall veto; prepend ordering; `next` twice is an error; serial async listeners awaited in order; listeners die with their owner; parallel failures contained and named; bail answers |
| [timers.yml](suites/timers.yml) | AR-O-2 | Timers fire for live owners; timeout, interval, debounce and the trailing throttle call never fire after their owner unloads |
| [loader.yml](suites/loader.yml) | AR-O-6 | Only the edited entry restarts; disabled entries; preset groups with realms; failure isolation; entry and group `inject`; malformed file keeps the last good tree; file and runtime edits merged; watching across rename saves |

## Port changes made for the suites

The Go port had `Prepend` on `EventKey` only. `BailKey.Prepend` and
`WaterfallKey.Prepend` were added to `src/go/cordis/events.go` (each one call
to the existing internal registration with the prepend flag), with the
regression test `TestPrependedBailAndWaterfallListenersRunFirst`. The .NET port
already took `prepend` on every `On` overload.

Both production systems read services they never declared with upstream's
unchecked `ctx.get(name)` (deepseek-harness 376 times), which neither port had.
Both now have it as `Lookup`: .NET `Context.Lookup(name)` / `Lookup<T>()`
(`ServiceStore.Lookup`), Go `Context.Lookup(name)` / `ServiceKey[T].Lookup(ctx)`
(`store.lookup`). It returns the active provider in the caller's realm, or
nothing, with no inject check and no failure. Regression tests: .NET "optional
lookup is unchecked and never fails", Go `TestOptionalLookupIsUncheckedAndNeverFails`.
The bindings use it for `service.lookup` with `optional: true`.

## Not covered, and why

These harness patterns are outside what the ports implement
([AR-ADR-17](../../docs/architecture.md#ar-adr-17)) and have no case here:
YAML patch layers (`insert`, targeting rows by id across bundle, profile and
home layers), `!!js` expressions in `config` and `disabled`, includes, HMR of
module code with rollback on a failed import, and the harness's own scope
event filter (`[Context.filter]` tagging), which is harness code built on top
of cordis rather than framework behaviour.

## Lint and regenerate

```powershell
# regenerate the schema after a catalog edit (PowerShell 7 + powershell-yaml)
pwsh -File <bdd-code-extension>/spec/Build-Schema.ps1 -Catalog test/e2e/schema/actions.catalog.yaml

# lint every suite, as CI would
node <bdd-code-extension>/runtimes/typescript/dist/cli.js lint --strict --path "test/e2e/suites/*.yml"
node <bdd-code-extension>/runtimes/typescript/dist/cli.js lint --binding-check --binding csharp --path "test/e2e/suites/*.yml"
```

Run both from the repository root.
