# cordis for Go

> **Ported from [cordiverse/cordis](https://github.com/cordiverse/cordis).**
> This directory is a Go reimplementation of that TypeScript project; the design, the
> terminology (context, fiber, effect, isolate, intercept), and the semantics are theirs.
> It tracks the same upstream revision as the [.NET port](../net/README.md) and follows
> the same scope decisions; where Go needs a different mechanism, the
> [design notes](#design-notes) say why.

A dynamic DI container where **service availability is a value that changes over time**.
Components declare what they need, load themselves when it appears, and unload
themselves when it goes away. Requires Go 1.26 or later and has no third-party
dependencies.

Paths below are relative to `src/go/`, where `go.mod` lives
(module `github.com/smartpcr/agent-plugin/src/go`).

```
cordis              core: Context, Fiber, effects, services, registry, events, logger
timer               the "timer" service: timers owned by the calling fiber
loader              the "loader" service and ConfigFile: a declarative, hot-reloading entry tree
internal/testkit    fixtures and helpers shared by the test suites
cmd/cordis-sample   runnable end-to-end demo
```

```bash
cd src/go
go build ./...
go vet ./...
go test -race ./...           # 161 tests: cordis 92, timer 7, loader 62
go run ./cmd/cordis-sample
gofmt -l .                    # must print nothing
```

`-race` needs cgo (a C compiler on `PATH`); without one, run `go test ./...`.

## Concept mapping

| cordis (TypeScript) | Go | Notes |
| --- | --- | --- |
| `new Context()` | `cordis.New(options...)` | one tree: store, registry, events, logger; `WithExporters`, `WithStrictConfig` |
| `Context` + `Proxy` get-trap | `ctx.Get(name)`, `ServiceKey[T].Get(ctx)` | explicit resolution; errors wrap `ErrNotInjected` / `ErrUnavailable` |
| `locate()` | `store.locateLocked` | stops at a context that isolates the name again |
| `ctx.get(name)` (unchecked, optional) | `ctx.Lookup(name)`, `ServiceKey[T].Lookup(ctx)` | the active provider in the caller's realm, or `ok == false`; no inject check, never fails |
| `Object.create(parent)` chains | `chainMap[V]` | lookups fall through, writes stay local |
| `Symbol(name)` isolate keys | `*IsolateKey` (pointer identity) | |
| `Fiber` + epoch + inertia | `*Fiber`, same algorithm | transitions run on their own goroutine |
| `Impl` | `serviceImpl` | tracks its dependents; withdrawal waits for them |
| `ctx.effect(() => dispose)` | `ctx.Effect(label, func() (Disposer, error))` | |
| `ctx.effect(async () => …)` | `ctx.EffectAsync(label, …)` | keeps the fiber `Loading` until it settles |
| `ctx.effect(async function* () {…})` | `ctx.EffectIter(label, func(yield func(Disposer) bool) error)` | a Go push iterator; `yield` returns false once disposed |
| `ctx.use(component, config)` | `ctx.Use(component, config, extraInject...)` | returns `(*Fiber, error)`; `fiber.Wait()` replaces `await` |
| function component | `cordis.Define[C](name, func(*Context, C) error, options...)` | |
| class component | `cordis.Construct[C, T](name, func(*Context, C) (T, error), options...)` | instance `Init() error` and `io.Closer` are honoured |
| `static inject = [...]` | `cordis.WithInject(names...)`, `cordis.WithInjectConfig(name, cfg)` | |
| Standard Schema `Config` | `Validate() error` on the config type, or `cordis.WithSchema` | resolved on every load |
| `fiber.rawConfig`, `fiber.effectiveConfig`, `fiber.resolveConfig(candidate)` (patch V15) | `fiber.Config()`, `fiber.EffectiveConfig()`, `fiber.ResolveConfig(candidate)`, `fiber.RetainConfig(raw)` | three distinct views; resolving a candidate works on a deep copy and stores nothing |
| `ctx.on('internal/config', …)` | `ctx.OnConfig(hook)` | tree-wide, owned by the registering fiber; transforms or rejects a raw candidate before the schema |
| `ctx.inject(deps, fn)` | `ctx.Inject(names, body)` / `ctx.Apply(name, body, inject...)` | |
| `registry.replace(old, next)` | `ctx.Registry().Replace(old, next)` | later uses of `old` are forwarded |
| `Service.init` | `Initializer.Init()` | gates dependents |
| `Service.check` | `Checker.Available()` | |
| traceable proxy (caller attribution) | `Scoped.ForContext(ctx)` | opt-in; the timer uses it |
| `Service.resolveIntercept` | `ctx.InterceptChain(name)`, `cordis.GetIntercept[T]` | context intercepts first, inject config last |
| `ctx.isolate(name)` / `ctx.intercept(name, cfg)` | `ctx.Isolate(name)` / `ctx.Intercept(name, cfg)` | |
| `emit / parallel / bail / waterfall` | `EventKey[A].Emit / Parallel`, `BailKey[A,R].Bail`, `WaterfallKey[A].Waterfall` | one key type per mode, so listeners stay typed; `On` appends a listener and `Prepend` puts it first, on every key type |
| `ctx.on('internal/update', …)` | `ctx.OnUpdate(handler, prepend)`, `cordis.AcceptUpdate[C]` | per fiber; accept in place or call `next` |
| `internal/commit` | `ctx.Internal().OnCommit` | carries the original config, for write-back |
| other `internal/*` events | `ctx.Internal().OnPlugin / OnStatus / OnService / OnReplace` | delivered in order, outside the tree lock |
| `@cordisjs/loader` + `@cordisjs/include` | `loader.Component` + `loader.ConfigFileComponent` | JSON instead of YAML |

## Usage

### Service

```go
type Database struct{ ctx *cordis.Context /* ... */ }

var DatabaseKey = cordis.NewServiceKey[*Database]("database")

var DatabaseComponent = cordis.Construct("DatabaseService",
    func(ctx *cordis.Context, _ cordis.None) (*Database, error) {
        db := &Database{ctx: ctx}
        _, err := DatabaseKey.Provide(ctx, db)
        return db, err
    })

func (db *Database) Init() error  { return db.connect() } // dependents wait for this
func (db *Database) Close() error { return db.disconnect() } // runs after every dependent unloaded
```

### Component with dependencies and validated config

```go
type HeartbeatOptions struct {
    IntervalMs int `json:"intervalMs"`
}

func (o HeartbeatOptions) Validate() error { /* range check; an error fails the load */ }

var Heartbeat = cordis.Define("HeartbeatPlugin",
    func(ctx *cordis.Context, config HeartbeatOptions) error {
        db, err := DatabaseKey.Get(ctx) // ErrNotInjected unless declared below
        if err != nil {
            return err
        }
        timers, err := timer.Key.Get(ctx)
        if err != nil {
            return err
        }
        // stops when *this* component unloads
        _, err = timers.Interval(func() { ctx.Logger().Infof("%s", db.Read("greeting")) },
            time.Duration(config.IntervalMs)*time.Millisecond)
        return err
    },
    cordis.WithInject("database", "timer"),
    cordis.WithDefaults(func() HeartbeatOptions { return HeartbeatOptions{IntervalMs: 250} }))
```

### Composition

```go
root := cordis.New()
timerFiber, _ := root.Use(timer.Component, nil)
_ = timerFiber.Wait()

heartbeat, _ := root.Use(Heartbeat, HeartbeatOptions{IntervalMs: 200})
heartbeat.State()                    // Pending: no database yet

database, _ := root.Use(DatabaseComponent, nil)
_ = heartbeat.Wait()                 // Active, started on its own

_ = heartbeat.Update(HeartbeatOptions{IntervalMs: 100}, false) // unload + reload
database.Dispose()                   // dependents unload first

root.Isolate("database").Use(DatabaseComponent, nil)          // second, private instance
root.Intercept(cordis.LoggerServiceName, cordis.LoggerConfig{Level: cordis.LevelDebug}).Use(Noisy, nil)
root.Registry().Replace(Heartbeat, HeartbeatV2)               // swap in place
root.Dispose()                                                // whole tree, leaves first
```

### Config updates handled in place

```go
// Accept new options without a restart. Without a hook, Update restarts the fiber.
cordis.AcceptUpdate(ctx, func(next HeartbeatOptions) { interval.Store(next.IntervalMs) })
```

### Config views and config hooks

```go
fiber.Config()                   // raw: as handed in, kept for the next load
fiber.EffectiveConfig()          // what the running instance holds; ok == false until it loads
fiber.ResolveConfig(candidate)   // hooks, then schema; changes neither view
fiber.RetainConfig(raw)          // store a raw config without an update

// A hook sees every fiber's raw candidate before its schema, and ends with its owner.
ctx.OnConfig(func(fiber *cordis.Fiber, candidate any) (any, error) {
    return expand(candidate) // transform, or return an error to reject it
})
```

An `Update` accepted in place becomes the effective config only if no later update,
restart or disposal happened while the update hooks ran.

### Tree options

```go
root := cordis.New(
    cordis.WithExporters(bootExporter), // exactly these exporters; none with no argument
    cordis.WithStrictConfig(),          // an unknown JSON key or trailing data fails the load
)
```

`cordis.New()` with no option keeps the console exporter on the standard streams. The
tree never closes the exporters its creator passed. In a strict tree, JSON config
(`json.RawMessage`, `[]byte` or `map[string]any`) is decoded with unknown keys refused
and nothing allowed after the first value; the `ConfigError` names the key (or the
field of a type mismatch) in its issue's `Path`. A component declared with
`cordis.LenientConfig()` is exempt, and a typed Go config is unaffected.

### Inline bodies and events

```go
var Tick = cordis.NewEvent[int]("tick")

fiber, _ := root.Inject([]string{"database"}, func(ctx *cordis.Context) error {
    _, err := Tick.On(ctx, func(_ *cordis.Context, n int) error { // unregisters itself on unload
        fmt.Println(n)
        return nil
    })
    return err
})

_ = Tick.Emit(root, 1)
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

```go
func init() {
    loader.Register("TimerService", timer.Component) // like database/sql drivers
    loader.Register("DatabaseService", DatabaseComponent)
    loader.Register("HeartbeatPlugin", Heartbeat)
}

root.Use(loader.Component, nil) // or loader.Options{Resolver: loader.Components{...}}
root.Use(loader.ConfigFileComponent, loader.ConfigFileOptions{Path: "cordis.json", Watch: true})
```

Entry config is handed to the component as JSON and decoded onto its defaults by the
component's own schema. With `Watch`, edits to the file reconcile the live tree
(create / update / dispose per entry), and `fiber.Update(...)` writes config back.

- **Entries.** Ids must not contain `:` (the tree separator), and every non-group entry
  needs a `name`. An invalid, unresolvable or failing entry is logged on its own; its
  siblings still load.
- **Groups.** A group's children are created by the group's own body. They run only while
  the group is loaded: a disabled group, or one pending on its `inject`, holds them back,
  and a rebuilt group recreates them.
- **Isolation.** `isolate` maps a service name to a realm label. A label is shared per
  service by every entry that uses it, so two services can share one label. `"true"`
  makes a realm private to the entry, and `""` is an ordinary shared label. Siblings load
  concurrently, so when two of them provide the same service in one realm, which one
  wins is not specified; the other fails as already registered.
- **File sync.** All reads and writes run on one serial loop. File edits and runtime
  edits are merged per entry against the last content synced with the file; if both
  sides changed the same entry differently, the file wins and the conflict is logged. A
  runtime edit made while a reload is being applied is kept. Writes go through a
  temporary file that is renamed over the config, retried while it is locked, and never
  replace a file that changed since it was last read.
- **Missing or bad files.** A missing file is seeded from `Initial` only at startup. A
  file that disappears, or stops being a JSON array, later is logged and left alone, and
  the live tree is kept. Entries without an id keep their identity across reloads.

## Design notes

The semantics are the .NET port's (see its design notes for providers outliving their
dependents, epochs and revisions, config resolved on every load, and sequential effect
unwinding); these notes cover what Go changes.

**One lock per tree, no user code under it.** JavaScript is single-threaded and the .NET
port serialises transitions on a reentrant monitor. Go mutexes are not reentrant, so the
Go port never runs component code while it holds a tree's lock. Lifecycle notifications
(`internal/status`, `internal/service`, `internal/plugin`, `internal/replace`) are posted
to an ordered outbox while the lock is held. Whichever goroutine releases the lock next
delivers them, one deliverer at a time, so a handler may call back into the context.
Read-only calls (`State`, `Get`, `Has`, …) release the lock without delivering, so reading
state never runs handlers on the caller's goroutine. One callback does run under the
lock: `Checker.Available`. It must be quick and must not call into the context. Config
is resolved outside the lock, at the start of each load once the fiber's injections are
active, so config hooks (`ctx.OnConfig`) and a component's schema run unlocked; a schema
still must not call into the context.

**Goroutines instead of `Task.Yield`.** Each load or unload runs on its own goroutine,
started from inside the lock, so a component body never runs inside `ctx.Use()`. A body
may block (it is the Go form of an async body); the fiber stays `Loading` meanwhile.
`fiber.Wait()` blocks until no transition is in flight.

**Errors and panics.** Calls that can fail return an `error` instead of throwing:
`ErrInactive` for an effect on a disposed or unloading fiber, a `*ServiceError` for a failed lookup, a
`*ConfigError` for a rejected config. A panic in a body, `Init`, an effect or a cleanup
is recovered as a `*PanicError` and treated like any other load or cleanup failure, so one
faulty component cannot take the process down. Listener panics in `Emit`, `Bail` and
`Waterfall` propagate to the caller, as in any synchronous Go call; `Parallel` recovers
them into its `*AggregateError`.

**Typed keys instead of reflection.** .NET resolves services and components through
attributes and reflection. Here a service name carries its type in a `ServiceKey[T]`,
events in `EventKey[A]` / `BailKey[A,R]` / `WaterfallKey[A]`, and components are
built by generic constructors that know their config type `C`. That knowledge lets a
component decode JSON config itself, so the loader passes entry config through untouched.

**Instances by interface.** `Construct` checks the returned instance for `Initializer`
(`Init() error`, which gates dependents) and `io.Closer` (closed on unload), the Go
analogues of `IAsyncInit` and `IDisposable`. Config types opt into validation with
`Validate() error`. A service value opts into `Checker` and `Scoped`.

**Component identity.** Components are compared by pointer, like upstream compares
component objects, so define each one once, as a package-level variable. Each
`ctx.Apply` / `ctx.Inject` call defines a new inline component.

**Name resolution for the loader** uses a `Resolver`. By default it is the package-level
`loader.Register` table (the `database/sql` driver pattern); `loader.Options.Resolver`
overrides it per loader. There is no assembly scanning to fall back on.

**Polling file watch.** The standard library has no file-system notifications, and the
port avoids third-party dependencies, so `ConfigFile` polls the file's content
(`PollInterval`, 100ms by default). Polling sees editor-style rename saves and the
component's own writes alike. Each tick reads one small file.

**Layered config.** `loader.LayerSetComponent` composes the tree from ordered layers
(bundle, profile, user, project); each layer file is a JSON/JSONC array of
operations (`insert`, override by `id`, `merge` into config, `remove`), and
`loader.Compose` returns the tree with an `Index` naming the layer that supplied each
value. Runtime edits are written to the one `Writable` layer only, edited in place so
comments survive; the write is refused with `loader.ErrStaleLayer` when the file's
content hash changed since it was read, and with `loader.ErrMaskedEdit` when a later layer overrides the edited value (nothing is written either way); a layer file that disappears keeps its last operations. An optional `WriteGuard` (e.g. a
cross-process lock) wraps every write. `Debounce` makes a burst of watched saves one
reload.

**Editing, restarting and nesting.** `Create`, `Update`, `Set`, `Move` and `Remove`
validate an edit before applying it (an invalid one changes nothing) and report it for
persistence; `Lookup(path)` reads the desired tree and `EntryOf(fiber)` finds the entry
that loaded a fiber. `Lifecycle()` / `OnLifecycle` record `add`, `update`, `move`,
`remove` and `restart` per entry. `Restart(id)` reloads one entry and leaves its
siblings and every file alone. After each apply, edit or restart has settled, the
loader emits one `loader.Reloaded` notice listing the created, updated, restarted and
removed ids and the issues found. `loader.New(ctx, options, layers...)` runs a
read-only loader of a component's own: its entries are private to that component's
context and unload with it, and `ApplyLayers` with a new layer list restarts only the
entries that changed.

## Not ported

Everything the [.NET port leaves out](../net/README.md#not-ported) is left out here too:
HMR, YAML config and multi-file includes, `ctx.mixin` / accessors, long stack-trace
stitching, merged intercepts (`InterceptChain` exposes every layer instead), and the
fs, server, http, env, cli and package-manager packages. In addition:

- **`EffectHandle.Children`.** Go functions are not comparable, so the runtime cannot
  tell that a yielded `Disposer` belongs to another handle. Release order is unaffected:
  the inner effect is still released before the effect that yielded it.
- **The interval async iterator** (`ctx.interval(delay)` without a callback). Use
  `Interval` with a callback, or `Sleep` in a loop.
- **Debounce arguments.** `Debounce` and `Throttle` wrap a `func()`; capture arguments in
  the closure.

## Upstream sync

Ported against upstream `86f59e4`, the revision the .NET port tracks. Every behaviour in
the .NET port's [upstream sync table](../net/README.md#upstream-sync) is ported, and every
.NET test has a Go counterpart named after it; the upstream issue number is in a comment
after the test name.

Changes made for the harness, beyond upstream and the .NET port (harness requirement, not
upstream; tests in `cordis/unload_test.go`):

- **Final unload** (`DH-D-CORDIS-1`). A fiber that is unloading refuses new effects,
  services, listeners and child fibers with `ErrInactive`, so a cleanup cannot register
  something that outlives the unload. A second `EffectHandle.Dispose` or `Fiber.Dispose`
  caller waits until the first caller's cleanups have returned, and an unload waits for
  handles another goroutine is releasing before it releases its providers. Disposing a
  fiber that never loaded still releases effects created on its context. An unload also
  waits for an effect whose acquisition is still running on another goroutine, so the
  cleanup it collects runs before the providers are released. Internal
  notifications are delivered by the tree's dispatcher goroutine, each on a fresh
  goroutine, never as a side effect of the lifecycle work (an acquisition, an unload, the
  root's teardown), and strictly in the order they were posted: a notification starts
  only once every earlier one has returned or is *parked*, that is blocked in a cordis
  lifecycle wait (`Fiber.Wait`, `Fiber.Dispose`, `EffectHandle.Dispose`, an unload or
  acquisition it waits for). `Fiber.Wait` and `Fiber.Dispose` return only once the
  notifications raised during the call have been delivered (returned or parked). So a
  handler may dispose or wait for the fiber that raised it, wait for a load that creates
  and waits for children, or dispose a fiber whose cleanup creates another one: the
  notifications that work raises are delivered while the handler waits. A handler
  blocked outside cordis (on its own channel or lock) holds back every later
  notification, and the `Wait`/`Dispose` calls waiting for them, until it returns. A
  parked handler resumes concurrently with later ones, so handlers guard one-shot work
  with a flag rather than `sync.Once`. A barrier waits only for its own target
  notifications, never for a later one that is still running. A wait is attributed to
  its delivery by goroutine id, which Go exposes only in a stack trace header
  (`goroutine N [...]`); `goid.go` accepts only a well-formed, positive id, and an id
  claimed by two running deliveries is attributed to neither until both have returned. A delivery without a valid identity is never parked:
  delivery stays in order, a handler waiting in it holds back later notifications, and
  the condition is logged once as a warning.
  Disposing the root is
  final in the same way: the root refuses new effects and child fibers, its state is
  `Unloading` then `Disposed`, and an overlapping `root.Dispose()` or
  `root.Fiber().Wait()` returns only after the teardown. A fiber loads only after its
  `Internal.OnPlugin` creation handlers have returned (the creation notification starts
  the load itself after running them), so a creation handler that disposes it stops it
  before its body runs. `Fiber.Wait` on a new fiber waits for its creation handlers to
  return, so a creation handler must not wait for the fiber it announces.
  A cleanup or acquisition must not dispose its own handle or wait for its own fiber's
  unload.
- **Fiber lifetime** (`DH-D-CORDIS-2`). `Fiber.Lifetime()` / `Context.Lifetime()` is a
  `context.Context` derived from the parent fiber's, fresh for each load, and cancelled
  as soon as the fiber is asked to leave the load, before the unload waits for
  dependents or runs cleanups. `context.Cause` is `ErrInactive` for a disposed fiber and
  `ErrUnloading` otherwise; a fiber that is not loaded returns an already-cancelled
  context. The root's lifetime ends with `root.Dispose()`.
- **Context values and scoped dispatch** (`DH-D-CORDIS-3`; `cordis/scope_test.go`).
  `ctx.WithValue(key, v)` derives a context on the same fiber whose value `Value(key)`
  finds by walking the parent chain, so its derived contexts and the fibers loaded from
  them see it and its parent and siblings do not; `ValueKey[T]` is the typed form.
  `ctx.WithFilter(admit)` derives a context to dispatch from: every verb (`Emit`,
  `Notify`, `Parallel`, `Bail`, `Waterfall`, `Around`) skips a listener whose
  registering context `admit` rejects, unless it was registered with `cordis.Global()`.
  Nested filters must all admit; fibers loaded from a filtered context do not inherit
  it; a panicking filter rejects and is logged once per dispatch. `ctx.ScopeTo(fiber)`
  admits only listeners from that fiber's subtree (`Context.Within`), and
  `EmitScoped`/`NotifyScoped` dispatch through it. `On` and `Prepend` take a variadic
  `ListenOption`, so existing calls compile unchanged.
- **Contained notification** (`DH-D-CORDIS-4`). `EventKey.Notify` calls every admitted
  listener in order; an error or panic is logged on that listener's own logger and
  reaches neither the caller nor the later listeners. It returns nothing. `Emit` still
  stops on, and returns, the first error.
- **Around key** (`DH-D-CORDIS-7`). `AroundKey[Req, Res]` is middleware whose result type
  differs from its request's. Listeners wrap one another outermost first in
  registration order; one that does not call `next` short-circuits; an error reaches the
  caller unchanged; a panic in a listener or the terminal is returned as an error naming
  the fiber and wrapping a `*PanicError`. `WaterfallKey` is unchanged.

## Upstream

- Original project: <https://github.com/cordiverse/cordis>
- This port: <https://github.com/smartpcr/agent-plugin/tree/main/src/go>

Not affiliated with or endorsed by the cordis maintainers. Refer to the upstream
repository for its own license and terms before redistributing anything derived from it.
