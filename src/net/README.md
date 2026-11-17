# Cordis.NET

> **Ported from [cordiverse/cordis](https://github.com/cordiverse/cordis).**
> This directory is a C# / .NET reimplementation of that TypeScript project; the design,
> the terminology (context, fiber, effect, isolate, intercept), and the semantics are theirs.
> See [Concept mapping](#concept-mapping) for how each upstream construct is expressed here,
> [Not ported](#not-ported) for what was deliberately left out, and
> [Upstream sync](#upstream-sync) for the upstream revision this port tracks.

Targets .NET 10. A dynamic DI container where **service availability is a value that
changes over time**. Components declare what they need, load themselves when it appears, and
unload themselves when it goes away. (Upstream renamed "plugin" to "component"; so does this
port. `ctx.Plugin()` remains as an `[Obsolete]` alias of `ctx.Use()`.)

Paths below are relative to `src/net/`, where `Cordis.slnx` lives.

```
Cordis                core: Context, Fiber, effects, services, registry, events, logger
Cordis.Timer          TimerService — timers owned by the calling fiber
Cordis.Loader         LoaderService + JsonConfigFile — declarative, hot-reloading entry tree
tests/Cordis.Tests    48 behavioural tests (no external test framework)
samples/Cordis.Sample runnable end-to-end demo
```

```bash
cd src/net
dotnet build Cordis.slnx
dotnet run --project tests/Cordis.Tests    # 48 tests, custom runner
dotnet run --project samples/Cordis.Sample
dotnet format Cordis.slnx                  # apply the enforced code style
```

## Build setup

| File | Role |
| --- | --- |
| `global.json` | Pins the .NET 10 SDK (`10.0.100`, rolling forward to later 10.0 feature bands only). |
| `Cordis.slnx` | XML solution file. |
| `Directory.Build.props` | Shared settings: `net10.0`, nullable, implicit usings, warnings as errors, XML documentation, code style and StyleCop enforced at build. |
| `Directory.Packages.props` | Central package management. The only package is `StyleCop.Analyzers` (build-time only); add versions here, never in a `.csproj`. |
| `.editorconfig`, `stylecop.json`, `CodeAnalysis/*.ruleset` | The ASZ-UpdateService coding style and analysis rules (StyleCop SA rules and selected CA/CS rules as errors). |

### Code style

The style is taken from the ASZ-UpdateService repository, adapted only to keep LF line
endings. Every rule below is an error, so a violation fails `dotnet build`:

- every file starts with the Microsoft copyright header (with its file name);
- `using` directives go inside the namespace; one type per file, named after the type
  (`ChainMap{TValue}.cs` for generics);
- instance members are always qualified with `this.`; private fields are camelCase without an
  underscore, and private static readonly fields PascalCase;
- members are ordered by kind, then access, then static-ness, as StyleCop requires;
- public and protected members carry complete XML documentation (`<summary>`, every
  `<param>`, `<typeparam>` and `<returns>`); internal and private members are exempt;
- C# files are UTF-8 with a byte order mark.

Most violations are fixed by `dotnet format Cordis.slnx`; documentation is written by hand.

The build treats `NU1507` (several machine-level NuGet feeds without source mapping) as a
plain warning. A repository `nuget.config` with `<clear />` and package source mapping
would remove it.

## Concept mapping

| cordis (TypeScript) | Cordis.NET | Notes |
| --- | --- | --- |
| `Context` + `Proxy` get-trap | `Context` + `ServiceStore.Resolve` | explicit resolution; no proxies |
| `locate()` (fiber walk to the declaring fiber) | `ServiceStore.Locate` | stops at a context that isolates the name again |
| `Object.create(parent)` chain | `ChainMap<T>` + parent links | lookups fall through, writes stay local |
| `Symbol(name)` isolate keys | `IsolateKey` (reference identity) | |
| `Fiber` + epoch + inertia | `Fiber`, same algorithm | epoch = reload revision + binding uids |
| `Impl` (one binding per `provide`) | `ServiceImpl` | tracks its dependents; withdrawal waits for them |
| `ctx.effect(() => dispose)` | `ctx.Effect(Func<Action?>)` | |
| `ctx.effect(async () => …)` | `ctx.EffectAsync(Func<ValueTask<Disposer?>>)` | keeps the fiber `Loading` until it settles |
| `ctx.effect(async function* () { yield … })` | `ctx.EffectMany(Func<IAsyncEnumerable<Disposer>>)` | not advanced after disposal; the iterator is closed |
| `ctx.use(component, config)` | `ctx.Use<T>(config)` / `ctx.Use(IComponent)` | `ctx.Plugin()` is obsolete |
| `Component` object `{ inject, apply }` | `IComponent` / `IComponent<TConfig>` | |
| `registry.replace(old, next)` | `Registry.Replace(old, next)` | in-place swap; later uses of `old` are forwarded |
| `Service.init` | `IAsyncInit.InitAsync` | gates dependents |
| `Service.check` | `Service.CheckAvailable` | |
| traceable proxy (caller attribution) | `IContextScoped.ForContext` | opt-in via `ScopePerCaller` |
| Standard Schema `Config` | `IConfigSchema` (+ `DataAnnotationsSchema<T>`) | sync-only, resolved on every load |
| `static inject = ['x']` | `[Inject("x")]` / `[Inject(typeof(T))]` | |
| `Service.resolveIntercept` | `ctx.InterceptChain<T>(name)` | context intercepts first, `[Inject]` metadata last |
| `ctx.isolate(name)` | `ctx.Isolate(name)` | |
| `ctx.intercept(name, cfg)` | `ctx.Intercept(name, cfg)` | |
| `emit/parallel/serial/bail/waterfall` | `EmitAsync` / `ParallelAsync` / `BailAsync` / `WaterfallAsync` | one key type per mode, so handlers stay typed |
| `ctx.on('internal/update', …)` (per fiber) | `ctx.OnUpdate(...)` | accept a config in place, or call `next` to restart |
| `internal/commit` | `Context.Internal.Committing` | carries the original config, for write-back |
| other `internal/*` events | `Context.Internal` (C# events) | synchronous |
| `@cordisjs/loader` + `@cordisjs/include` | `LoaderService` + `JsonConfigFile` | JSON instead of YAML |

## Usage

### Service

```csharp
[ServiceName("database")]
public sealed class DatabaseService(Context ctx) : Service(ctx, "database"), IAsyncInit
{
    public async ValueTask InitAsync() => await Connect();   // dependents wait for this
    public string? Read(string key) => …;
}
```

### Component with dependencies and validated config

```csharp
[Inject("database")]
[Inject("timer")]
public sealed class HeartbeatPlugin
{
    public sealed class Options
    {
        [Range(10, 60_000)] public int IntervalMs { get; set; } = 250;
    }

    public static readonly IConfigSchema Schema = DataAnnotationsSchema<Options>.Instance;

    public HeartbeatPlugin(Context ctx, Options config)
    {
        var db = ctx.Get<DatabaseService>();                 // throws unless declared above
        ctx.Get<TimerService>().Interval(
            () => ctx.Logger.Info(db.Read("greeting")),      // stops when *this* component unloads
            TimeSpan.FromMilliseconds(config.IntervalMs));

        ctx.Effect(() =>
        {
            var listener = Subscribe();
            return () => listener.Dispose();                 // released on unload
        }, "subscription");
    }
}
```

### Composition

```csharp
var root = new Context();
await root.Use<TimerService>();

var heartbeat = root.Use<HeartbeatPlugin>(new HeartbeatPlugin.Options { IntervalMs = 200 });
Console.WriteLine(heartbeat.State);            // Pending — no database yet

await root.Use<DatabaseService>();
await heartbeat;                               // Active, started on its own

await heartbeat.UpdateAsync(new HeartbeatPlugin.Options { IntervalMs = 100 }); // unload + reload
await root.Registry.RemoveAsync(typeof(DatabaseService));                      // dependents unload first

root.Isolate("database").Use<DatabaseService>();          // second, private instance
root.Intercept("logger", new LoggerConfig { Level = LogLevel.Debug }).Use<Noisy>();
root.Registry.Replace(typeof(HeartbeatPlugin), typeof(HeartbeatV2)); // swap in place
await root.DisposeAsync();                                // whole tree, leaves first
```

### Config updates handled in place

```csharp
public HeartbeatPlugin(Context ctx, Options config)
{
    // Accept new options without a restart. Without a hook, UpdateAsync restarts the fiber.
    ctx.OnUpdate<Options>(next => _interval = next.IntervalMs);
}
```

### Inline bodies and events

```csharp
static readonly EventKey<int> Tick = new("tick");

var fiber = root.Inject(["database"], ctx =>
{
    ctx.On(Tick, (c, n) => Console.WriteLine(n));   // unregisters itself on unload
});

await root.EmitAsync(Tick, 1);
```

### Declarative tree

`cordis.json`:

```json
[
  { "id": "timer", "name": "TimerService" },
  {
    "id": "prod", "name": "group", "group": true,
    "children": [
      { "id": "db",   "name": "DatabaseService", "isolate": { "database": "true" } },
      { "id": "beat", "name": "HeartbeatPlugin", "config": { "intervalMs": 150 } }
    ]
  }
]
```

```csharp
await root.Use<LoaderService>();
await root.Use<JsonConfigFile>(new JsonConfigFile.Options { Path = "cordis.json" });
```

The file is watched; edits reconcile the live tree (create / update / dispose per entry) and
`fiber.UpdateAsync(...)` writes config back to disk.

- **Entries.** Ids must not contain `:` (the tree separator), and every non-group entry
  needs a `name`. An invalid, unresolvable or failing entry is logged on its own; its
  siblings still load.
- **Groups.** A group's children are created by the group's own body. They run only while
  the group is loaded: a disabled group, or one pending on its `inject`, holds them back, and
  a rebuilt group recreates them.
- **Isolation.** `isolate` maps a service name to a realm label. A label is shared per
  service by every entry that uses it, so two services can share one label. `"true"` makes a
  realm private to the entry, and `""` is an ordinary shared label.
- **File sync.** All reads and writes run on one serial loop. File edits and runtime edits
  are merged per entry against the last content synced with the file; if both sides changed
  the same entry differently, the file wins and the conflict is logged. Writes go through a
  temporary file that is renamed over the config, retried while it is locked, and never
  replace a file that changed since it was last read.
- **Missing or bad files.** A missing file is seeded from `Initial` only at startup. A file
  that disappears, or stops being a JSON array, later is logged and left alone, and the live
  tree is kept. Entries without an id keep their identity across reloads.

## Design notes

**Why explicit resolution instead of `DispatchProxy`.** cordis leans on `Proxy` for three jobs:
dependency enforcement, caller attribution, and mixins. A faithful reproduction would need a
`DispatchProxy` per service access — reflection-heavy, interface-only, and AOT-hostile.
`ctx.Get<T>()` does the enforcement, `IContextScoped` does the attribution, and mixins are
dropped (extension methods cover that ground in C#).

**What a component can reach.** Resolution walks from the calling fiber up to the root,
like upstream's `locate()`. A service is reachable when a fiber on the walk declares it in
`Inject`, or provides it itself in the same realm. Services provided directly on the root
are reachable from every fiber. The walk stops at a context that isolates the name again,
so `ctx.Isolate("db").Get("db")` never returns the outer `db`.

**Providers outlive their dependents.** Every fiber that loaded against a binding is recorded
as its dependent. A provider that unloads first notifies its dependents, then waits for
each to finish its own cleanup before disposing its effects. A dependent can therefore still
use the service from its disposers. Withdrawing a single `Provide` waits the same way.

**Epochs and revisions.** A fiber's epoch is its reload revision followed by the uid of every
binding it injects. `RestartAsync` and `UpdateAsync` bump the revision, so a restart requested
while a load is still running is never lost. A failed fiber stays failed until `UpdateAsync`
or `Replace` clears the error; provider changes alone do not re-run it.

**Config is resolved on every load.** `Fiber.Config` holds the config as given. The schema
validates it each time the component loads, so an invalid config fails that load. On a loaded
fiber, `UpdateAsync` validates up front and throws without changing anything.

**Per-caller cloning is opt-in.** `Service.ForContext` returns `this` unless the service sets
`ScopePerCaller`. The clone is shallow, so a service that mutates state after cloning — an event
subscription, a cache — would silently diverge per caller. In JavaScript the same hazard exists
(own-property writes shadow the prototype); making it opt-in turns a silent bug into a decision.
`TimerService` opts in; `LoaderService` does not.

**Threading.** JavaScript is single-threaded; .NET is not. All fiber state transitions are
serialised on one lock per context tree, and no `await` ever runs while it is held — every
transition starts with `await Task.Yield()` so its synchronous prefix ends inside the caller's
lock. (Getting this wrong is a real deadlock; there is a regression test for it.)

**Effects unwind sequentially.** A fiber disposes its effects one at a time, in reverse order
of registration, so a tree always unloads leaves first. Upstream starts all disposers in
reverse order and lets them run concurrently. This port keeps the deterministic order.

**Async effects during load.** An effect started by the component body keeps the fiber
`Loading` until it settles, and its failure fails the load. Effects started later — from a
timer callback, say — cannot retroactively fail the fiber; they just log and dispose themselves.

**Timers.** `Throttle` runs the first call at once and collapses later calls in the window
into one trailing call (`noTrailing: true` drops them). Windows are measured on a monotonic
clock, and no scheduled call runs after its owner unloads.

## Not ported

- **HMR.** The .NET analogue is `AssemblyLoadContext` with collectible assemblies plus a
  `FileSystemWatcher` on build output. `Registry.Replace` provides the in-place swap it would
  need, but unload semantics differ enough from Node's module registry that a straight port
  would be misleading.
- **YAML config** (`js-yaml`, anchors and aliases, the `!!js` expression tag), source
  locations for config errors, and multi-file includes — JSON only, no extra dependency.
- **`ctx.mixin` / accessors** — prototype ergonomics with no C# analogue worth the machinery.
- **Long stack-trace stitching** (`composeError`). .NET's `ExceptionDispatchInfo` preserves async
  stacks well enough that the JS trick isn't needed.
- **Merged intercepts.** Upstream merges every intercept layer with `Object.assign`. Here the
  highest-priority config wins (`GetInterceptConfig`); `InterceptChain` exposes all layers for
  a service that wants to merge.
- **`[Inject]` per-dependency config** is limited to attribute constants; use
  `ctx.Intercept(name, config).Use<T>()` for rich objects.
- **The fs, server, http, env, cli and package-manager packages** that upstream added later.

## Upstream sync

The port was first written against upstream `8cc9e33` (2026-08-13). It was brought up to
`86f59e4` (2026-11-17) by reviewing every change to `core`, `timer`, `loader` and `include`
in that range.

| Area | Ported |
| --- | --- |
| core | failed fibers do not re-enter the lifecycle (#98); config resolved on every load, commit separated from update (#134); `internal/update` hooks per fiber, disposed with it, may accept a config in place (#56); reload revisions in epochs (#189); per-binding uids and dependents, providers disposed after their dependents (#191, #196, #277); lookup stops at isolation boundaries (#209); `[Inject]` metadata ranked below context intercepts (#206); disposed fibers settle to `Disposed` (#133); async-generator effects closed on disposal (#268); `registry.replace()` (#266); listener removal honoured mid-dispatch (#242); waterfall `next()` guard (#44); the failing event named in parallel errors (#205); empty aggregate errors logged as themselves (#222); plugin renamed to component (#307) |
| timer | throttle disposal guard (#161), monotonic windows (#299), trailing handle kept on re-entry (#224) |
| loader | failures isolated per entry (#109); entry ids and names validated (#172, #239); isolate labels per service, `""` shared (#267, #329); applies serialised (#166) |
| include | file and runtime edits reconciled (#121); only a missing file treated as missing (#171); stable ids for anonymous entries (#216) |

TypeScript-only changes (typings, `.ts` import extensions, `ctx.root` → `ctx[Context.root]`,
access-error stack capture, symbol event names) have no .NET counterpart.

## Upstream

- Original project: <https://github.com/cordiverse/cordis>
- This port: <https://github.com/smartpcr/agent-plugin/tree/main/src/net>

Not affiliated with or endorsed by the cordis maintainers. Refer to the upstream repository
for its own license and terms before redistributing anything derived from it.
