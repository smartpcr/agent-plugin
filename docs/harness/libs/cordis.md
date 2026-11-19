# cordis (framework core): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Nothing in this document was built, run or tested.

| | |
|---|---|
| **Library ID** | `DH-LIB-CORDIS` |
| **Upstream unit** | `UP-PKG-vendor.cordis` |
| **Upstream version** | cordis `4.0.0-rc.12`, upstream commit `01b3962` (`vendor/README.md:15`), plus logged local modifications. The manifest's own `4.1.0` (`vendor/cordis/package.json:4`) is the harness release number, not an upstream version (`docs/rescope.md:25`). |
| **Kind** | vendored, runtime |
| **Used by** | every package group: 765 import lines in `packages/` and `apps/`, host and browser client alike |
| **Go choice** | own code: the existing port in [src/go](../../../src/go/README.md) (`github.com/smartpcr/agent-plugin/src/go`, package `cordis`), extended by six deliverables |
| **Related areas** | the loader, include, group, timer and HMR packages (`libs/cordis-loader.md`, `libs/cordis-plugins.md`), config schemas (`libs/schemastery.md`), and [cosmokit](cosmokit.md) |

## 1. What it is and why upstream needs it

cordis is the plugin framework under the whole harness. It answers one question: when a
piece of the program goes away (a plugin is disabled, its config changes, a service it
needs disappears), what exactly has to be undone, and in which order? Five terms carry
the answer (`docs/cordis-primer.md:9-13`):

- A **plugin** (a **component** in the Go port) is a unit that can be loaded and unloaded.
- A **fiber** is one running instance of a plugin. It moves through the states
  pending, loading, active, failed, unloading, disposed (`vendor/cordis/src/fiber.ts:177-184`).
- A **context** (`ctx`) is the handle a plugin works through. Each fiber has its own.
- A **service** is a named object one plugin provides and others declare with `inject`.
  A plugin loads only while every declared service has a provider, and reloads when a
  provider is replaced.
- An **effect** is a registration paired with its undo. Listeners, service
  registrations and child plugins are all effects, so unloading a fiber undoes them.

Ownership and access are separate here. The fiber that created an effect **owns** it and
is the only one that releases it. **Access** to a service is granted by declaring it;
using a service never makes the user its owner.

The harness is composed almost entirely from these parts: 303 dependency declarations,
about 620 effects and 330 listeners in non-test code. Upstream vendors the framework "so
that the harness fully owns its framework layer" (`vendor/README.md:3`) and patches it.

## 2. How upstream uses it

**How the counts were taken.** Lines matching a pattern in TypeScript under `packages/`
and `apps/` at commit `2e39e70`, excluding `tests/` folders and `*.spec.*` / `*.test.*`
files, with `git grep` on 2026-11-18. "Client" is the share under `packages/client/`, a
`src/client/` folder, `apps/web` or `apps/desktop`: browser code that
[DH-ADR-3](../design.md#dh-adr-3) replaces. The counts are rough (a receiver not named
`ctx`, `context` or `this.ctx` is missed) but they tell a pervasive API from a rare one.

### 2.1 cordis API used by the harness, and the Go port's status

Go symbols are in `src/go/cordis`. **Have** means the port provides it today, **gap**
means a deliverable in section 6 adds it, **replaced** means Go reaches the same result
another way, **not needed** means the API exists only because of JavaScript.

| Upstream API | Lines (client) | Go port status |
|---|---|---|
| `ctx.effect(fn, label)` returning a cleanup | 623 (391) | Have: `Context.Effect` (`context.go:245`) |
| `ctx.effect` with a generator or async generator | 53 (4) | Have: `Context.EffectIter` (`context.go:262`) |
| `ctx.effect` with an async body | 6 (1) | Have: `Context.EffectAsync` (`context.go:253`) |
| Effect creation refused while the fiber unloads (local patch V6) | relied on by all of the above | **Gap**: [DH-D-CORDIS-1](#dh-d-cordis-1) |
| `ctx.on(name, listener)` | 332 (63) | Have: `EventKey.On`, `BailKey.On`, `WaterfallKey.On` (`events.go:97`) |
| `ctx.on(..., { global: true })` | 43 (3) | **Gap**: listener option, [DH-D-CORDIS-3](#dh-d-cordis-3) |
| `ctx.on(..., { prepend: true })` | 1 | Have: `Prepend` on every key type (`events.go:102`) |
| `ctx.once` | 0 | Have, unused: `EventKey.Once` |
| `ctx.emit` | 57 (9) | Have: `EventKey.Emit` (`events.go:123`); the first failing listener stops the dispatch, as upstream |
| `ctx.events.dispatch('emit', args)` with a hand-written loop | 15 (1) | **Gap**: contained notification, [DH-D-CORDIS-4](#dh-d-cordis-4) |
| `ctx.parallel` | 10 (1) | Have: `EventKey.Parallel` (`events.go:137`) |
| `ctx.serial` | 1 | Replaced: `BailKey.Bail`. A Go listener may block, so "awaited in order" and "in order" are the same thing |
| `ctx.bail` | 2 | Have: `BailKey.Bail` (`events.go:192`) |
| `ctx.waterfall` | 30 (1) | Have: `WaterfallKey.Waterfall` (`events.go:232`); several arguments become one struct |
| Dispatch through a scope carrier (`Context.filter`); helpers `scopeTarget`, `scopeOf` | 4; 15 and 38 | **Gap**: [DH-D-CORDIS-3](#dh-d-cordis-3) |
| `ctx.extend({ ... })` (attach inherited values to a context) | 3 | **Gap**: [DH-D-CORDIS-3](#dh-d-cordis-3) |
| `ctx.provide` / `ctx.reflect.provide` | 39 (15) / 13 (13) | Have: `Context.Provide` (`context.go:164`), `ServiceKey.Provide` (`keys.go:59`) |
| `class X extends Service`, `super(ctx, 'name')` | 97 (24), 135 | Have: `Construct` plus `ServiceKey.Provide` (`component.go:105`) |
| `[Service.init]` (dependents wait for it) | 38 (1) | Have: `Initializer.Init` (`fiber.go:19`) |
| `Service.check` | 0 | Have, unused: `Checker` (`store.go:12`) |
| `Service.invoke`, `Service.extend`, `resolveIntercept`, `mergeIntercept` | 0 | Not ported, not needed |
| Reading `ctx.<service>` (196 type augmentations of `Context`) | pervasive | Replaced: `ServiceKey[T].Get(ctx)` (`keys.go:30`); an undeclared service is `ErrNotInjected` |
| `ctx.get(name)` (optional, never throws) | 420 (73) | Have: `Context.Lookup`, `ServiceKey.Lookup` (`context.go:188`) |
| `ctx.set` | 0 | Have, unused: `Context.Set` |
| `static inject` / `export const inject` | 303 (112) | Have: `WithInject` (`component.go:51`) |
| `inject` with per-dependency config | 0 | Have, unused: `WithInjectConfig` |
| `ctx.inject([...], body)` | 99 (48) | Have: `Context.Inject` (`context.go:215`) |
| `@Inject` decorator | 2 (one file) | Not needed: `WithInject` |
| `ctx.plugin(plugin, config)` | 77 (26) | Have: `Context.Use` (`context.go:203`) |
| `Config` schema on a plugin | 196 (18) | Have: `Validator`, `WithSchema`, `WithDefaults` (`component.go:17-75`); the schema library itself is specified in `libs/schemastery.md` |
| `ctx.isolate(name)` | 2, plus 18 `isolate:` lines in 11 profile files | Have: `Isolate`, `IsolateWith` (`context.go:97-107`) |
| `ctx.intercept`, `intercept:` in profiles | 0, 0 | Have, unused: `Intercept`, `InterceptChain` |
| `ctx.mixin` | 0 | Not ported ([AR-ADR-17](../../architecture.md#ar-adr-17)), unused |
| `ctx.accessor` | 1 (`packages/typert/protocol/src/index.ts:274`) | Not ported; that one use makes a missing value read as "absent", which `Lookup` already does |
| Caller attribution: a service method sees the caller's context (`docs/cordis-tutorial/02-lifecycle-and-effects.md:90`) | implicit in registries | Replaced: `Scoped.ForContext` (`store.go:19`), or pass the owner context explicitly |
| `getTraceable`, `symbols.original` | 2, 4 | Not needed: Go contexts are not proxies |
| `ctx.fiber.state`, `FiberState.*` | 21, 44 (9) | Have: `Fiber.State` (`fiber.go:181`), same six states (`state.go`) |
| `fiber.dispose()`, `fiber.await()`, `fiber.inertia` loops | 12, 4, 3 | Have: `Fiber.Dispose` blocks until unloaded, `Fiber.Wait` (`fiber.go:232`, `fiber.go:303`) |
| `fiber.assertActive()`, `fiber.uid === null` | 10, 13 | Have: `Fiber.State() == Disposed`; with [DH-D-CORDIS-2](#dh-d-cordis-2) also `Lifetime().Err()` |
| `fiber.update(config, noSave)`, `fiber.restart()` | 3, 6 | Have: `Fiber.Update`, `Fiber.Restart` (`fiber.go:240-296`) |
| `fiber.rawConfig` (read) | 2 | Have: `Fiber.Config` (`fiber.go:196`) |
| `fiber.rawConfig = x`, `fiber.effectiveConfig`, `fiber.resolveConfig(candidate)` (local patch V15) | 1 in the vendored loader, 5, 3 | **Gap**: [DH-D-CORDIS-5](#dh-d-cordis-5) |
| `ctx.on('internal/config', ...)` | 3 | **Gap**: [DH-D-CORDIS-5](#dh-d-cordis-5) |
| `ctx.on('internal/update', ...)`, `'internal/commit'` | 0, 0 | Have: `Context.OnUpdate`, `AcceptUpdate`, `Internal.OnCommit` |
| `ctx.on('internal/plugin', ...)` | 9 | Have: `Internal.OnPlugin` (`tree.go:130`), but not owned by a fiber; most uses disappear with [DH-D-CORDIS-2](#dh-d-cordis-2), the rest get [DH-D-CORDIS-6](#dh-d-cordis-6) |
| `ctx.on('internal/status', ...)`, `'internal/service'` | 6, 2 | Have: `Internal.OnStatus`, `OnService`; owned form in [DH-D-CORDIS-6](#dh-d-cordis-6) |
| `ctx.on('internal/dispatch', ...)` | 1 | Not provided; see section 7 |
| `'internal/get'`, `'internal/set'`, `'internal/listener'` | 0 | Not ported, not needed |
| `ctx.registry.get / delete / resolve` | 10 lines in 2 files (HMR, client modules) | Have: `Registry.Get`, `Remove`; and `Registry.Replace` (`registry.go:78`), which the vendored revision does not have |
| `ctx[Context.root]` | 18 | Have: `Context.Root` |
| `Context.is(value)` | 2 | Not needed: Go types |
| `ctx.logger.warn / error / info / debug` | 258 / 35 / 15 / 7 | Have: `Context.Logger`, `Warnf`, `Error`, `Errorf`, `Infof`, `Debugf` (`logger.go:162-193`) |
| `ctx.logger.exporter(...)` | 2 | Have: `LoggerService.AddExporter` (`logger.go:111`) |
| `ctx.baseUrl` (base for resolving module names) | 19 (1) | Not needed in core: Go components are resolved by a registered name ([AR-ADR-16](../../architecture.md#ar-adr-16)) |

Of the names imported from the package, `Context` (709 lines) and `Service` (112) dominate.
`Volatile` (39) is a config reference type re-exported from cosmokit; see [cosmokit.md](cosmokit.md).

### 2.2 Workarounds the harness applies around cordis

| # | Workaround | Evidence | Why it exists |
|---|---|---|---|
| W1 | A hand-written loop over `ctx.events.dispatch('emit', args)` with a try/catch per listener, at 15 sites | `packages/core/agent/src/dispatch.ts:120-137` | `emit` lets one throwing listener starve the rest and drops returned promises (`vendor/cordis/src/events.ts:207-210`). Upstream marks `dispatch()` deprecated; the fork removes the mark to keep using it (`vendor/README.md:37`). |
| W2 | Listening to `internal/plugin` for "my own fiber has `uid === null`" and firing an `AbortController` | `packages/lsp/lsp-stdio/src/index.ts:146-153`, `packages/mcp/mcp-client/src/index.ts:198-204`, `packages/mcp/mcp-manager/src/index.ts:200-202` | Unload waits for a plugin body that is still running, and the body has no other way to learn that it should stop. |
| W3 | Re-attaching a service to its real owner with `getTraceable` and `symbols.original` | `packages/core/agent/src/index.ts:391-429` | The context is a JavaScript proxy that silently swaps `this.ctx` for the caller's context; code that needs the true owner has to undo that. |
| W4 | An opaque "carrier" object with a `Context.filter` method, plus a no-op plugin used only to get an owned sub-lifetime | `packages/core/scope/src/index.ts:137-147`, `packages/core/scope/src/index.ts:170-185` | Agent-scoped listeners: a listener registered in one agent's scope must not see another agent's events. |
| W5 | `await fiber.dispose()` followed by `while (fiber.inertia) await fiber.inertia` | `packages/core/scope/src/index.ts:115-118` | A disposer that was already claimed resolves early, so callers poll until the fiber is quiet. |
| W6 | Throwing from an `internal/dispatch` observer to veto an event | `packages/terminal/terminal-bash/src/index.ts:49-58` | The event being vetoed has no veto contract of its own. |

## 3. Tech debt upstream carries

The search covered `vendor/cordis` and `vendor/cosmokit` source for `FIXME`, `TODO`, `XXX`
and `@deprecated`, the modification log in `vendor/README.md`, the overrides in
`pnpm-workspace.yaml`, and calling code under `packages/` for the workarounds above.
`patches/` holds nothing for either package.

| Debt ID | What it is | Evidence | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-marker.vendor/cordis/src/fiber.ts:613` | `FIXME internal/fiber-info`: the status event carries only the previous state and is meant to be replaced | `vendor/cordis/src/fiber.ts:613` | Resolved. The port's status notification is a typed callback with the fiber, the old state and the new state (`Internal.OnStatus`, `tree.go:133`); [DH-D-CORDIS-6](#dh-d-cordis-6) gives it an owned form. |
| `UP-DEBT-marker.vendor/cordis/src/fiber.ts:82` | `TODO: async validation`: a schema that returns a promise is rejected with a `TypeError` | `vendor/cordis/src/fiber.ts:82-86` | Not applicable. The split between synchronous and promise-returning validators is a JavaScript property. A Go schema is an ordinary function that may block; it runs outside the tree lock (`fiber.go:762-773`). |
| `UP-DEBT-marker.vendor/cordis/src/reflect.ts:409` | `TODO enhance error message` in the mixin forwarder: reading a mixed-in member of a missing service gives a poor error | `vendor/cordis/src/reflect.ts:408-411` | Resolved by [DH-ADR-CORDIS-2](#dh-adr-cordis-2): there are no mixins. A failed lookup is a `ServiceError` that names the service and says whether it was undeclared or unavailable (`errors.go:28`). |
| `UP-DEBT-found.F1.1` | Lifecycle hardening exists only as local patch V6 on the vendored copy | `vendor/README.md:36` | Resolved by [DH-D-CORDIS-1](#dh-d-cordis-1): the same guarantees become port behaviour with tests. |
| `UP-DEBT-found.F1.2` | Raw, effective and candidate config access exists only as local patch V15 | `vendor/README.md:42` | Resolved by [DH-D-CORDIS-5](#dh-d-cordis-5). |
| `UP-DEBT-found.F1.3` | W1: 15 copies of a containment loop built on an API upstream deprecated | `packages/core/agent/src/dispatch.ts:120-137` | Resolved by [DH-D-CORDIS-4](#dh-d-cordis-4): one dispatch verb with that contract. |
| `UP-DEBT-found.F1.4` | W2: self-disposal detected through a tree-wide lifecycle event | `packages/lsp/lsp-stdio/src/index.ts:146-153` | Resolved by [DH-D-CORDIS-2](#dh-d-cordis-2): each fiber has a cancellation signal. |
| `UP-DEBT-found.F1.5` | W3: proxy-based caller attribution has to be undone by hand | `packages/core/agent/src/index.ts:391-429` | Resolved by [DH-ADR-CORDIS-2](#dh-adr-cordis-2): no proxy; the owner is always explicit. |
| `UP-DEBT-found.F1.6` | The fork carries 21 logged local modifications that must be re-applied by hand at every upstream sync | `vendor/README.md:29`, `vendor/README.md:62-68` | Resolved by [DH-ADR-CORDIS-1](#dh-adr-cordis-1): the Go port is first-party code with one recorded upstream revision and no patch log. |

The fourth assigned unit, the `pnpm` override for cosmokit, is in [cosmokit.md](cosmokit.md).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from | Status in the port |
|---|---|---|---|
| `DH-LIB-CORDIS-R1` | Everything a component registers is released exactly once when its fiber unloads, in reverse order of registration, and the fiber is not reported unloaded until every cleanup has returned. | 623 effects; V6 | Partly: [DH-D-CORDIS-1](#dh-d-cordis-1) |
| `DH-LIB-CORDIS-R2` | Once a fiber has started to unload, new registrations on it are refused, so cleanup code cannot create something that outlives the unload. | V6 (`vendor/cordis/src/fiber.ts:448-450`) | Gap: [DH-D-CORDIS-1](#dh-d-cordis-1) |
| `DH-LIB-CORDIS-R3` | A running component body learns promptly that its fiber is leaving, without subscribing to tree-wide events. | W2 | Gap: [DH-D-CORDIS-2](#dh-d-cordis-2) |
| `DH-LIB-CORDIS-R4` | A component loads only while every declared service has an active provider, reaches only the services it declared, and can probe for an optional service without failing. | 303 declarations, 420 optional lookups | Have |
| `DH-LIB-CORDIS-R5` | A provider's resources outlive the cleanup of every dependent that used them. | `scripts/cordis-core-lifecycle.spec.ts:74` | Have, strengthened by [DH-D-CORDIS-1](#dh-d-cordis-1) |
| `DH-LIB-CORDIS-R6` | Typed events offer four contracts: contained notification (every listener runs, failures are logged), await-all, first answer, and middleware. | section 2.1; W1 | Three have; notification is [DH-D-CORDIS-4](#dh-d-cordis-4) |
| `DH-LIB-CORDIS-R7` | A dispatch can be limited to listeners registered in an admitted scope, a listener can opt out of that limit, and a context carries values its descendants inherit. | W4 | Gap: [DH-D-CORDIS-3](#dh-d-cordis-3) |
| `DH-LIB-CORDIS-R8` | Config is validated before a body runs and before an update is applied. The raw config, the config the running instance holds, and a candidate being checked are three distinct things, and a component can hook the check of its own candidate. | V15; `scripts/cordis-config-access.spec.ts:14-91` | Partly: [DH-D-CORDIS-5](#dh-d-cordis-5) |
| `DH-LIB-CORDIS-R9` | A subtree can run a private instance of a service that the rest of the tree cannot see. | 18 `isolate:` lines | Have |
| `DH-LIB-CORDIS-R10` | Fiber creation, disposal and state changes can be observed by a subscription that is removed with its owner; a failing observer does not affect the others or the lifecycle. | 17 `internal/*` listeners | Partly: [DH-D-CORDIS-6](#dh-d-cordis-6) |
| `DH-LIB-CORDIS-R11` | A failed fiber stays failed until it is told to retry, and a panic in component code becomes an ordinary failure of that component. | `src/go/README.md` design notes | Have |
| `DH-LIB-CORDIS-R12` | The framework uses the standard library only and passes the race detector. | [AR-ADR-15](../../architecture.md#ar-adr-15) | Have; every deliverable keeps it |
| `DH-LIB-CORDIS-R13` | The middleware contract of R6 can take a request of one type and return a result of another, so that an interception returns a decision and not a rewritten request. | [DH-ADR-ARCH-5](../upstream/architecture.md#dh-adr-arch-5) | Gap: [DH-D-CORDIS-7](#dh-d-cordis-7) |
| `DH-LIB-CORDIS-R14` | Config that arrives as JSON can be decoded strictly: an unknown key or trailing data is a config error that names the key, not a silently dropped value. | [DH-ADR-SECURITY-11](../upstream/security.md#dh-adr-security-11) (c); [zod.md](zod.md) §7 | Gap: [DH-D-CORDIS-8](#dh-d-cordis-8) |
| `DH-LIB-CORDIS-R15` | A tree can be created with the log exporters its creator chooses, including none, so nothing is written to standard output before the application installs its own exporter. | [cordis-plugins.md](cordis-plugins.md) §7; a protocol on standard output must stay clean | Gap: [DH-D-CORDIS-9](#dh-d-cordis-9) |
| `DH-LIB-CORDIS-R16` | Code outside the tree can observe the start and the end of every effect cleanup, with the fiber and the effect's label, without being able to change it. | [../areas/boot.md](../areas/boot.md) (`DH-D-BOOT-8`) | Gap: [DH-D-CORDIS-10](#dh-d-cordis-10) |
| `DH-LIB-CORDIS-R17` | A fiber's child fibers and the names of the services it provides can be read, as copies. | [../areas/x-inspector-timeline.md](../areas/x-inspector-timeline.md) (`DH-D-XINSPECT-1`) | Gap: [DH-D-CORDIS-11](#dh-d-cordis-11) |

## 5. Go choice

**Decision.** Keep the existing Go port and extend it ([DH-ADR-5](../design.md#dh-adr-5)).
The owner decided on 2026-11-18 that the port is changed, not wrapped
([DH-ADR-7](../design.md#dh-adr-7)). No third-party module is involved.

**Why.** Of the 54 API rows in section 2.1, seven are gaps, one is partly covered and one
is deliberately not provided. The port already has or replaces the rest, and the gaps
are additions to one package.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| Extend the port in `src/go/cordis` (chosen) | Lifecycle model already built and tested; one place for the semantics | The port moves away from a pure upstream mirror | Module `github.com/smartpcr/agent-plugin/src/go`, `go 1.24`, no requirements (`src/go/go.mod`); tag `src/go/v0.1.4`; read 2026-11-18 |
| Re-port from the harness's vendored fork | Exact parity with what the harness runs | Older base; brings the proxy, mixins and patch log back | See drift table below |
| A static dependency-injection library (`fx`, `wire`) | Widely known | No notion of a service going away; rejected in [architecture.md](../../architecture.md) section 4 | Not evaluated further |

**Tradeoff.** The port has to state, per behaviour, whether it follows upstream, the
harness fork, or neither.

### 5.1 Version drift

| | Harness vendored copy | Go port |
|---|---|---|
| Upstream revision | `01b3962`, tag `v4.0.0-rc.12`, 2026-10-21 (`vendor/README.md:15`) | `86f59e4`, 2026-11-16 (`src/go/README.md`, "Upstream sync") |
| Relation | base | 51 commits ahead, 0 behind; its `packages/core/package.json` says `4.0.0-rc.13` |
| `registry.replace()` | absent (`vendor/cordis/src/registry.ts`); HMR re-plugs fibers by hand (`packages/boot/hmr/src/index.ts:691`) | present: `Registry.Replace` |
| Naming | `plugin` | `component`, `Use` |
| V6 lifecycle hardening | present (local) | absent, and absent at `86f59e4` too |
| V15 config access | present (local) | absent, and absent at `86f59e4` too |

The relation was checked on 2026-11-18 with the GitHub compare API
(`gh api repos/cordiverse/cordis/compare/01b3962...86f59e4`) and by reading
`packages/core/src` at `86f59e4`. The port is newer than the harness's base but lacks the
harness's own patches. Local modifications that touch the core (`vendor/README.md:31-51`):

| ID | What it changes | Go relevance |
|---|---|---|
| V2, V3, V4, V10, V16, V17 | Manifests, `tsconfig`, import specifiers, published file lists, the `@deepseek-ai` scope | None: packaging for npm and Node |
| V6 | Reentrant disposal: effect registered before its body runs; joined cleanup; no effects while unloading; child fiber owned before it is announced; pending effects drained; observer failures contained | [DH-D-CORDIS-1](#dh-d-cordis-1) |
| V7 | JSDoc on the public surface | None: Go doc comments already exist |
| V15 | `rawConfig`, `effectiveConfig`, `resolveConfig(candidate)`, `internal/config` | [DH-D-CORDIS-5](#dh-d-cordis-5) |
| V22 | Re-exports the `Volatile` types | [cosmokit.md](cosmokit.md) |

### 5.2 Decisions

<a id="dh-adr-cordis-1"></a>
#### DH-ADR-CORDIS-1 The port follows upstream `86f59e4` and adds the harness's guarantees as its own features

- **Decision.** Do not rebase the port onto the vendored fork. Keep `86f59e4` as the
  recorded upstream revision and implement V6 and V15 as port behaviour, each listed in
  the "Upstream sync" section of `src/go/README.md` as "harness requirement, not upstream".
- **Why.** The port is the newer base, and the harness's patches are requirements, not
  JavaScript accidents.
- **Tradeoff.** A third lineage exists next to upstream and the fork. The sync table is
  the only thing that keeps it honest.
- **Serves.** `DH-O-1`. **Verified by.** `DH-T-CORDIS-1` to `DH-T-CORDIS-3`, `DH-T-CORDIS-8`, `DH-T-CORDIS-11`.

<a id="dh-adr-cordis-2"></a>
#### DH-ADR-CORDIS-2 No proxy context: typed keys and an explicit owner

- **Decision.** Mixins, accessors, decorators, `Context.is` and implicit caller
  attribution stay unported ([AR-ADR-17](../../architecture.md#ar-adr-17)). A service is read
  through a `ServiceKey[T]`. A service method that registers something on behalf of a
  caller takes the caller's `*Context` as a parameter, or the service implements `Scoped`.
- **Why.** The harness uses mixins 0 times and accessors once, yet pays for the proxy with
  W3 and with one debt marker.
- **Tradeoff.** Call sites are longer (`Tools.Get(ctx)` instead of `ctx.tools`), and every
  registry API must name its owner parameter.
- **Serves.** `DH-O-1`. **Verified by.** Existing port test `TestUndeclaredServiceAccessIsRejected`; `DH-T-CORDIS-11`.

<a id="dh-adr-cordis-3"></a>
#### DH-ADR-CORDIS-3 Leaving is signalled by a cancellation context, not by an event

- **Decision.** Each loaded fiber exposes a standard `context.Context` that is cancelled
  the moment the fiber is asked to unload.
- **Why.** In Go, cancellation is how blocking calls (network, subprocess, file) are
  interrupted. Upstream creates an `AbortController` in 229 non-test files and needs W2
  to connect one to a fiber.
- **Tradeoff.** A body that ignores the signal still blocks its unload; the framework
  does not kill goroutines.
- **Serves.** `DH-O-1`. **Verified by.** `DH-T-CORDIS-4`.

<a id="dh-adr-cordis-4"></a>
#### DH-ADR-CORDIS-4 Two verbs for two contracts: `Notify` contains failures, `Emit` stops on the first

- **Decision.** Add `Notify` for "tell everyone, nobody may veto". Keep `Emit` for the
  case where the caller wants the first error back.
- **Why.** Upstream has one verb and 15 hand-written loops around it (W1).
- **Tradeoff.** Authors must choose. The event's declaration names the verb, as upstream's
  `@mode` tag does (`docs/cordis-primer.md:27`).
- **Serves.** `DH-O-1`. **Verified by.** `DH-T-CORDIS-7`.

<a id="dh-adr-cordis-5"></a>
#### DH-ADR-CORDIS-5 Scoped routing is a dispatcher feature; scope policy stays in the harness

- **Decision.** The core gains context values, a filter on the dispatching context and a
  "global" listener option. What a scope is (a key, a parent chain, which listeners an
  agent admits) stays in the harness.
- **Why.** One filter in the dispatcher serves all dispatch modes. Without it every
  listener would need a wrapper that knows how to "skip" in each mode.
- **Tradeoff.** The core learns a concept upstream reached through a symbol-keyed
  property; the filter runs once per listener per dispatch.
- **Serves.** `DH-O-1`. **Verified by.** `DH-T-CORDIS-5`, `DH-T-CORDIS-6`.

## 6. Design notes: the deliverables

### 6.1 The complete list of changes to `src/go`

The owner decided that the existing port is changed ([DH-ADR-7](../design.md#dh-adr-7)).
This table is the one list of those changes: the 19 rows of conflict CF-1 in
[../design.md](../design.md) §8, plus the three additions the area specifications asked
for afterwards and one row that was split; 22 changes are built and one is withdrawn.
All three Go modules state one minimum Go version, `go 1.26.0`
([../design.md](../design.md) §4.2), so the `go` line of `src/go/go.mod` rises from
1.24 with the first release the three modules share; no change below needs it. Every change is a deliverable with an ID. Package `cordis` changes are
specified below in this document; package `loader` changes are specified in
[cordis-loader.md](cordis-loader.md) and [cordis-plugins.md](cordis-plugins.md). Every
change keeps the standard-library rule of the module
([AR-ADR-15](../../architecture.md#ar-adr-15)); none adds a requirement to
`src/go/go.mod`. The deliverables `DH-D-LOADER-2`, `-5` and `-12` are not in this table
because they are harness code in `src/harness`, not changes to `src/go`.

| Deliverable | Package | What changes | Why | Needed by | Depends on | Priority | Proof |
|---|---|---|---|---|---|---|---|
| [DH-D-CORDIS-1](#dh-d-cordis-1) | `cordis` | Unload is complete, exclusive and final (four defects) | Owned lifetime must hold under concurrency | every area; first of all | none | P0 | `DH-T-CORDIS-1` to `-3` |
| [DH-D-CORDIS-2](#dh-d-cordis-2) | `cordis` | A cancellation context per loaded fiber (`Lifetime`) | Blocking calls are interrupted by cancellation ([DH-ADR-ARCH-3](../upstream/architecture.md#dh-adr-arch-3)) | every area with a blocking body; A21 (`DH-D-BOOT-5`) | CORDIS-1 | P0 | `DH-T-CORDIS-4` |
| [DH-D-CORDIS-3](#dh-d-cordis-3) | `cordis` | Context values, filtered dispatch, global listeners | Agent scope ([DH-ADR-ARCH-7](../upstream/architecture.md#dh-adr-arch-7)) | A02, A03, A08, A17 | none | P0 | `DH-T-CORDIS-5`, `-6` |
| [DH-D-CORDIS-4](#dh-d-cordis-4) | `cordis` | Contained notification (`Notify`) | A listener must not veto a fact | A02, A03, A08, A17 | CORDIS-3 | P0 | `DH-T-CORDIS-7` |
| [DH-D-CORDIS-5](#dh-d-cordis-5) | `cordis` | Raw, effective and candidate config; the `OnConfig` hook | The loader's expressions and the settings editor | `DH-D-LOADER-4`; A21 (`DH-D-BOOT-2`) | none | P0 | `DH-T-CORDIS-8`, `-9` |
| [DH-D-CORDIS-6](#dh-d-cordis-6) | `cordis` | Lifecycle subscriptions owned by a fiber | A subscription a component can forget is a leak | A21 (audit), X3 | CORDIS-1 | P2 | `DH-T-CORDIS-10` |
| [DH-D-CORDIS-7](#dh-d-cordis-7) | `cordis` | An around key with separate request and result types | Interception returns a closed decision ([DH-ADR-ARCH-5](../upstream/architecture.md#dh-adr-arch-5)) | A02 (`DH-D-AGENT-4`), A03 (`DH-D-TOOLS-2`), A04 (`llm.StreamAround`), A08 (`DH-D-FS-4`) | CORDIS-3 | P0 | `DH-T-CORDIS-12` |
| [DH-D-CORDIS-8](#dh-d-cordis-8) | `cordis` | Strict config decoding | An unknown key is a load error ([DH-ADR-SECURITY-11](../upstream/security.md#dh-adr-security-11)) | A23 (`DH-D-UTIL-1`), A21 | none | P0 | `DH-T-CORDIS-13` |
| [DH-D-CORDIS-9](#dh-d-cordis-9) | `cordis` | A tree is created with the exporters its creator chooses | No write to standard output before the logging component loads | A21 (`DH-D-BOOT-5`), `DH-D-LOADER-12` | none | P0 | `DH-T-CORDIS-14` |
| [DH-D-CORDIS-10](#dh-d-cordis-10) | `cordis` | A cleanup observer on a tree | The watchdog must name the effect whose cleanup does not return | A21 (`DH-D-BOOT-8`) | CORDIS-1 | P1 | `DH-T-CORDIS-15` |
| [DH-D-CORDIS-11](#dh-d-cordis-11) | `cordis` | Read-only accessors for a fiber's children and provided services | The tree view of the inspector | X3 (`DH-D-XINSPECT-1`) | none | P2 | `DH-T-CORDIS-16` |
| withdrawn (was `DH-D-COSMOKIT-1`) | new `volatile` | A volatile reference type. **Not built** | A config value that changes without a restart is delivered through the update hook instead | no area asked for it ([DH-ADR-COSMOKIT-2](cosmokit.md#dh-adr-cosmokit-2), a recommended default) | n/a | n/a | n/a |
| [DH-D-LOADER-1](cordis-loader.md#dh-d-loader-1) | `loader` | Codec seam and source positions | Errors name file, line and column | `DH-D-LOADER-2`, A21 | none | P0 | `DH-T-LOADER-1` |
| [DH-D-LOADER-3](cordis-loader.md#dh-d-loader-3) | `loader` | Entry schema, `intercept`, full validation | A bad entry reports all its problems | A21, A27 | LOADER-1 | P0 | `DH-T-LOADER-3` |
| [DH-D-LOADER-4](cordis-loader.md#dh-d-loader-4) | `loader` | Expression seam | `!expr` replaces arbitrary JavaScript in config | `DH-D-LOADER-5`, A21 (`DH-D-BOOT-2`) | LOADER-3, **CORDIS-5** | P0 | `DH-T-LOADER-4`, `-5` |
| [DH-D-LOADER-6](cordis-loader.md#dh-d-loader-6) | `loader` | Layer composition and ownership index | Preview and the running tree use one function | A21, A27 | LOADER-3 | P0 | `DH-T-LOADER-7` |
| [DH-D-LOADER-7](cordis-loader.md#dh-d-loader-7) | `loader` | Layered source with write-back to one layer | A runtime edit has one home | A21 (`DH-D-BOOT-5`, `-7`) | LOADER-1, LOADER-6 | P0 | `DH-T-LOADER-8`, `-14`, `DH-E-LOADER-1` |
| [DH-D-LOADER-8](cordis-loader.md#dh-d-loader-8) | `loader` | Mutation, lookup and lifecycle log API | The config editor and the audit | A21 (`DH-D-BOOT-5`, `-7`, `-8`) | LOADER-3 | P1 | `DH-T-LOADER-9` |
| [DH-D-LOADER-9](cordis-loader.md#dh-d-loader-9) | `loader` | Per-field three-way merge | Edits to different fields of one entry both survive | A21 | LOADER-3 | P2 | `DH-T-LOADER-10` |
| [DH-D-LOADER-10](cordis-loader.md#dh-d-loader-10) | `loader` | A component can run a loader of its own under its context, with no writable layer | Agent presets run a tree of their own | A27 (`DH-D-BUNDLE-4`) | LOADER-6 | P0 | `DH-T-LOADER-11` |
| [DH-D-LOADER-14](cordis-loader.md#dh-d-loader-14) | `loader` | A group entry can mount another file as its children (split from LOADER-10) | A large layer can be split across files | none yet | LOADER-7 | P2 | `DH-T-LOADER-16` |
| [DH-D-LOADER-11](cordis-plugins.md#dh-d-loader-11) | `loader` | Restart one entry; announce a settled reload | A plugin process is restarted by its owner | A22, A21 (`DH-D-BOOT-7`) | LOADER-7 | P1 | `DH-T-LOADER-12`, `DH-E-LOADER-1` |
| [DH-D-LOADER-13](cordis-loader.md#dh-d-loader-13) | `loader` | A write guard on the layer source | Two hosts on one home must not both replace the profile layer | A21 (`DH-D-BOOT-7`) | LOADER-7 | P1 | `DH-T-LOADER-15` |

Four things follow from the table. `DH-D-CORDIS-5` is P0, not P1 as first written,
because the P0 expression seam depends on it. `DH-D-LOADER-10` is P0 and was split,
because the P0 agent presets depend on its nested-loader half. CF-1 rows 7, 8 and 9, which had no
deliverable, are `DH-D-CORDIS-7`, `-8` and `-9`. Whether the .NET port follows these
changes was not asked of the owner and stays open; until it is answered each change is
listed in the "Upstream sync" section of `src/go/README.md` as "harness requirement,
not upstream" ([DH-ADR-CORDIS-1](#dh-adr-cordis-1)).

### 6.2 API and delivery order

The eleven `DH-D-CORDIS` deliverables change package `cordis` in `src/go` and nothing
else. Proposed API:

```go
func (c *Context) Lifetime() context.Context                      // DH-D-CORDIS-2
func (c *Context) WithValue(key, value any) *Context              // DH-D-CORDIS-3
func (c *Context) Value(key any) any
func (c *Context) WithFilter(admit func(listener *Context) bool) *Context
func Global() ListenOption                                        // k.On(ctx, fn, cordis.Global())
func (k *EventKey[A]) Notify(ctx *Context, args A)                // DH-D-CORDIS-4
func (f *Fiber) EffectiveConfig() any                             // DH-D-CORDIS-5
func (f *Fiber) ResolveConfig(candidate any) (any, error)
func (f *Fiber) RetainConfig(raw any) error
func (c *Context) OnConfig(hook func(f *Fiber, raw any) (any, error)) (*EffectHandle, error)
func (c *Context) OnFiber(fn func(f *Fiber)) (*EffectHandle, error) // DH-D-CORDIS-6
func (c *Context) OnStatus(fn func(f *Fiber, old, next FiberState)) (*EffectHandle, error)

type AroundKey[Req, Res any] struct{ /* ... */ }                   // DH-D-CORDIS-7
func NewAround[Req, Res any](name string) *AroundKey[Req, Res]
func (k *AroundKey[Req, Res]) On(ctx *Context,
    fn func(ctx *Context, req Req, next func(Req) (Res, error)) (Res, error),
    opts ...ListenOption) (*EffectHandle, error)
func (k *AroundKey[Req, Res]) Around(ctx *Context, req Req,
    terminal func(Req) (Res, error)) (Res, error)

func New(options ...TreeOption) *Context                           // existing calls compile unchanged
func WithStrictConfig() TreeOption                                 // DH-D-CORDIS-8
func LenientConfig() Option                                        // per component
func WithExporters(exporters ...Exporter) TreeOption               // DH-D-CORDIS-9
func WithCleanupObserver(observe func(CleanupEvent)) TreeOption    // DH-D-CORDIS-10
type CleanupEvent struct{ Fiber *Fiber; Label string; Done bool; Err error }
func (f *Fiber) Children() []*Fiber                                // DH-D-CORDIS-11
func (f *Fiber) Provides() []string
```

Delivery order: 1, then 2, 6 and 10 (all rely on the unload sequence that 1 fixes); 3,
then 4 and 7; 5, 8, 9 and 11 are independent. No deliverable sits behind a flag: each is
usable once its tests pass.

<a id="dh-d-cordis-1"></a>
### DH-D-CORDIS-1 Unload is complete, exclusive and final

- **Result.** When `Fiber.Dispose` or `Fiber.Wait` returns after an unload, every cleanup
  the fiber owned has finished, nothing new was registered during the unload, and a
  fiber that never loaded leaves nothing behind. This is what makes "owned lifetime" true
  under concurrency, and it is what the harness's V6 patch provides upstream.
- **Gaps found in the port** (inference from reading the code; no test was run):
  1. `Fiber.run` refuses a new effect only when the fiber is disposed (`fiber.go:337-340`).
     During a restart, an effect created from a cleanup lands in the list that was
     already snapshotted (`fiber.go:723-727`) and survives into the next load.
  2. `EffectHandle.Dispose` returns at once for a second caller (`effect.go:51-54`) and
     the handle is detached before its cleanups run (`effect.go:57`). An unload that
     starts meanwhile does not see the handle, finishes early and releases providers
     (`fiber.go:736-747`) while that cleanup is still running.
  3. Disposing a fiber that is still pending skips the unload, because the epoch does not
     change (`fiber.go:144`, `fiber.go:554-556`). Effects attached to a pending fiber are
     never released. Upstream's fix is `vendor/cordis/src/fiber.ts:304-313`.
  4. The creation notification is delivered by the unlock at `fiber.go:109`, before the
     fiber's anchor effect exists (`fiber.go:111-129`). A handler that disposes the fiber
     there does nothing (`fiber.go:305-311`) and the fiber loads anyway.
- **How.** (1) `run` returns `ErrInactive` while the state is `Unloading`. (2) Each
  handle gets a "released" channel closed after its last cleanup; a later `Dispose`
  caller waits on it; the fiber keeps a set of handles in flight and `unload` waits for
  them before it releases providers. (3) `disposeAnchor` runs the unload path whenever
  the effect list is not empty. (4) The anchor effect is registered first and the
  creation notification is posted from inside it.
- **Failure and cleanup.** A cleanup that returns an error or panics is logged and the
  rest still run (unchanged). A cleanup that never returns blocks the unload; that is
  reported, not hidden (section 7).
- **Components.** `fiber.go`, `effect.go`. **Dependencies.** None. **Priority.** P0:
  every harness component relies on it.
- **Outcome.** `DH-O-1`. **Done when.** `DH-T-CORDIS-1`, `DH-T-CORDIS-2` and
  `DH-T-CORDIS-3` pass under `go test -race -count=5`, and the existing tests still pass.

<a id="dh-d-cordis-2"></a>
### DH-D-CORDIS-2 A cancellation context for every loaded fiber

- **Result.** `ctx.Lifetime()` returns a `context.Context` that is live while the fiber
  is loading or active and is cancelled as soon as the fiber is asked to leave, before
  the unload waits for the body. A component passes it to every blocking call. This
  replaces W2 and most `internal/plugin` listeners.
- **How.** The fiber creates a context with `context.WithCancelCause` when a load starts
  and cancels it in `setEpochLocked` when the target epoch moves away from the loaded
  one (dispose, restart, a provider withdrawn, a parent unloading). The cause says which:
  a new `ErrUnloading`, or `ErrInactive` for a disposed fiber. A fiber that is not loaded
  returns an already-cancelled context. Contexts derived by `Isolate`, `Intercept`,
  `WithValue` and `WithFilter` share their fiber's lifetime. The root's is cancelled by
  the root's `Dispose`.
- **Ownership.** The fiber owns the cancel function and calls it on every way out, so
  the context cannot leak. Cancelling does not release anything; cleanups still do that.
- **Components.** `fiber.go`, `context.go`, `errors.go`. **Dependencies.**
  [DH-D-CORDIS-1](#dh-d-cordis-1). **Priority.** P0.
- **Outcome.** `DH-O-1`. **Done when.** `DH-T-CORDIS-4` passes.

<a id="dh-d-cordis-3"></a>
### DH-D-CORDIS-3 Context values and scoped dispatch

- **Result.** A context can carry values that every context and fiber created under it
  can read, and a dispatch can be restricted to listeners whose registering context is
  admitted. The harness builds agent scopes on this instead of W4.
- **How.** `WithValue` derives a context on the same fiber, as `Isolate` does; `Value`
  walks the parent chain. `WithFilter` derives a context used only to dispatch: every
  dispatch verb skips a listener whose context the filter rejects, unless the listener
  was registered with `Global()`. Nested filters must all admit. `On` and `Prepend` gain
  a variadic option list, so existing calls compile unchanged. Upstream's equivalent is
  `vendor/cordis/src/events.ts:168-171`.
- **Failure.** A filter that panics is treated as "not admitted" and logged once per dispatch.
- **Components.** `context.go`, `events.go`. **Dependencies.** None. **Priority.** P0:
  the agent and session event model depends on it.
- **Outcome.** `DH-O-1`. **Done when.** `DH-T-CORDIS-5` and `DH-T-CORDIS-6` pass.

<a id="dh-d-cordis-4"></a>
### DH-D-CORDIS-4 Contained notification

- **Result.** `Notify` calls every admitted listener in registration order. An error or
  panic from one is logged on that listener's own logger and never reaches the caller
  or the later listeners. It returns nothing, so it cannot be used to veto.
- **How.** The loop in `Emit` with `protect` around each call, honouring removal during
  dispatch and the filter from [DH-D-CORDIS-3](#dh-d-cordis-3).
- **Components.** `events.go`. **Dependencies.** [DH-D-CORDIS-3](#dh-d-cordis-3).
  **Priority.** P0.
- **Outcome.** `DH-O-1`. **Done when.** `DH-T-CORDIS-7` passes.

<a id="dh-d-cordis-5"></a>
### DH-D-CORDIS-5 Raw, effective and candidate config

- **Result.** Three things become readable and stay distinct: the raw config kept for the
  next load (`Config`, exists), the resolved config the running instance holds
  (`EffectiveConfig`), and the result of checking a candidate without applying it
  (`ResolveConfig`). `RetainConfig` stores a raw config without triggering an update.
  A component can register a hook that sees, transforms or rejects raw candidates before
  the schema runs. The settings editor and the loader need all of these
  (`packages/boot/config-editor/src/index.ts:102`, `vendor/loader/src/config/entry.ts:163-191`).
- **How.** The fiber stores the value passed to the body. An update accepted in place
  replaces it only if no restart, later update or disposal happened in between: compare
  a config revision, the epoch and the committed providers, as
  `vendor/cordis/src/fiber.ts:821-826` does. `ResolveConfig` runs the hooks and then the
  schema and stores nothing. Hooks are tree-wide, owned by the registering fiber, and
  receive the fiber whose candidate is being checked.
- **Failure.** A rejected candidate returns a `ConfigError` and changes nothing. A hook
  that panics rejects the candidate.
- **Components.** `fiber.go`, `context.go`, `tree.go`. **Dependencies.** None.
  **Priority.** P0. It was P1 when first written ("needed by the loader and settings,
  not by the first components"). The loader's expression seam
  ([DH-D-LOADER-4](cordis-loader.md#dh-d-loader-4)) is P0 and depends on the `OnConfig`
  hook, and a deliverable cannot rank above one it depends on.
- **Outcome.** `DH-O-1`. **Done when.** `DH-T-CORDIS-8` and `DH-T-CORDIS-9` pass.

<a id="dh-d-cordis-6"></a>
### DH-D-CORDIS-6 Lifecycle subscriptions owned by a fiber

- **Result.** `ctx.OnFiber` and `ctx.OnStatus` subscribe to fiber creation, disposal and
  state changes as effects, so they end with the subscriber. Today `Internal.On...`
  returns a bare remove function (`tree.go:130-150`) that a component can forget.
- **How.** Thin wrappers that register the existing subscription inside `ctx.Effect`.
- **Components.** `context.go`. **Dependencies.** [DH-D-CORDIS-1](#dh-d-cordis-1).
  **Priority.** P2.
- **Outcome.** `DH-O-1`. **Done when.** `DH-T-CORDIS-10` passes.

<a id="dh-d-cordis-7"></a>
### DH-D-CORDIS-7 An around key with separate request and result types

- **Result.** An event can be dispatched as middleware in which each listener receives
  a request and a `next` function and returns a result of a different type. This is the
  "around" form of [../design.md](../design.md) §4.6. It matters because an interception
  in the harness returns one of a closed set of decisions (`allow`, `deny`, `ask` and so
  on), not a rewritten copy of its request
  ([DH-ADR-ARCH-5](../upstream/architecture.md#dh-adr-arch-5)). The port's
  `WaterfallKey[A]` threads one type through the chain
  (`src/go/cordis/events.go:210-232`), so it cannot express that.
- **How.** A new key type `AroundKey[Req, Res]` beside `WaterfallKey`, which stays as it
  is. `On` and `Prepend` register a listener as an effect of the calling fiber; `Around`
  builds the chain from the listeners the dispatching context admits
  ([DH-D-CORDIS-3](#dh-d-cordis-3)), outermost first in registration order, and calls
  the caller's `terminal` when the last listener calls `next`. A listener that does not
  call `next` short-circuits, which is how a denial is returned. A listener removed
  while a dispatch runs is skipped if it has not started. The key's type does not say
  what a decision is: the package that declares the event defines the result type.
- **Failure.** A listener's error is returned to the caller of `Around` unchanged, and
  the listeners outside it see it as the result of `next`. Inference: a panic in a
  listener or in `terminal` is recovered at the dispatch and returned as an error that
  names the listener's fiber, so that the caller fails closed (a policy check that
  panics means "no", [../design.md](../design.md) §6, rule 1); `Waterfall` keeps its
  present behaviour. Nothing is retried, and the dispatch owns no resource, so there is
  nothing to clean up.
- **Components.** `events.go`. **Dependencies.** [DH-D-CORDIS-3](#dh-d-cordis-3) (the
  filter and `Global`). **Priority.** P0: the tool pipeline, the driver's steering
  points, the model stream and the filesystem seam are declared on it
  ([../areas/tools.md](../areas/tools.md) `DH-D-TOOLS-2`,
  [../areas/agent-loop.md](../areas/agent-loop.md) `DH-D-AGENT-4`,
  [../areas/llm.md](../areas/llm.md) `llm.StreamAround`,
  [../areas/filesystem.md](../areas/filesystem.md) `DH-D-FS-4`).
- **Outcome.** `DH-O-1`. **Done when.** `DH-T-CORDIS-12` passes.

<a id="dh-d-cordis-8"></a>
### DH-D-CORDIS-8 Strict config decoding

- **Result.** In a tree created with strict decoding, a component whose config arrives
  as JSON and contains a key its config type does not have fails to load with a config
  error that names the key. A misspelt setting is reported, not dropped. This is what
  "decoding is strict: an unknown key or a wrong type is a load error" in
  [../design.md](../design.md) §4.9 needs from the port.
- **Why here and not in the harness.** `decodeConfig` (`src/go/cordis/component.go:182`)
  decodes with `json.Unmarshal`, which ignores unknown keys, and a `cordis.Schema`
  receives only the decoded value, so nothing above the port can see the dropped key
  ([zod.md](zod.md) §7).
- **How.** A tree option `WithStrictConfig()` makes `decodeConfig` use a `json.Decoder`
  with `DisallowUnknownFields` and reject data after the first value. A component that
  must accept unknown keys says so with the component option `LenientConfig()`. A tree
  created without the option behaves as today, so the port's existing tests and users
  are unaffected; the harness always creates its tree with it. The issue carries the
  key name. The file, line and column are added by the loader, which holds the
  position of every value by path ([DH-D-LOADER-1](cordis-loader.md#dh-d-loader-1)).
  The standard library reports only the first unknown key; listing every problem of a
  config at once is the job of the harness `schema` package (area A23,
  `DH-D-UTIL-1`), which checks the raw document before it is handed to the component.
- **Failure.** A rejected config is a `ConfigError`: the fiber fails and stays failed
  until told to retry, as for any config error. Nothing was applied, so nothing is
  undone.
- **Components.** `component.go`, `tree.go`. **Dependencies.** None. **Priority.** P0:
  every harness component's config is decoded through it.
- **Outcome.** `DH-O-1`, `DH-O-2`. **Done when.** `DH-T-CORDIS-13` passes.

<a id="dh-d-cordis-9"></a>
### DH-D-CORDIS-9 A tree is created with the exporters its creator chooses

- **Result.** `cordis.New(cordis.WithExporters(...))` creates a tree whose logger
  starts with exactly the given exporters, and with none when none is given. The
  application can therefore guarantee that nothing is written to standard output
  between the creation of the tree and the moment its logging component loads. That
  matters for the profiles that speak a protocol on standard output (SDK, ACP, MCP):
  one stray log line there is a protocol error.
- **How.** `New` gains a variadic option list; existing calls compile unchanged and
  keep the default console exporter (`src/go/cordis/logger.go:92`). With the option the
  logger service is created with the given list in place of the default. Boot passes an
  exporter of its own that outlives the root's unload and collects warnings and errors
  for its report ([../areas/boot.md](../areas/boot.md), `DH-D-BOOT-5`); the logging
  component ([DH-D-LOADER-12](cordis-plugins.md#dh-d-loader-12)) then adds and removes
  its exporter as an effect and no longer needs to restore a default.
- **Ownership.** The creator of the tree owns the exporters it passed; the tree never
  closes them. **Failure.** An exporter that panics is contained, as today.
- **Components.** `context.go`, `logger.go`. **Dependencies.** None. **Priority.** P0.
  It is a few lines, and the P0 boot sequence depends on it. CF-1 proposed P1 before
  boot stated the dependency.
- **Outcome.** `DH-O-1`, `DH-O-14`. **Done when.** `DH-T-CORDIS-14` passes.

<a id="dh-d-cordis-10"></a>
### DH-D-CORDIS-10 A cleanup observer on a tree

- **Result.** Code that created a tree is told when each effect's cleanup starts and
  when it ends, with the fiber and the effect's label. The harness watchdog uses it to
  name the effect whose cleanup does not return; without it the watchdog can name only
  the fiber ([../areas/boot.md](../areas/boot.md), `DH-D-BOOT-8`).
- **How.** A tree option `WithCleanupObserver(func(CleanupEvent))`. The unload sequence
  of [DH-D-CORDIS-1](#dh-d-cordis-1) calls the observer immediately before and
  immediately after each cleanup, outside the tree lock, on the goroutine that runs the
  cleanup. The event carries the fiber, the label, whether the cleanup is done and, when
  done, its error. It is an option of the tree and not an effect because the watchdog
  lives outside the tree and must still be informed while the root itself unloads.
- **Access, not ownership.** The observer can read; it cannot cancel, skip or reorder a
  cleanup, and it receives no handle. **Failure.** The observer must return quickly. A
  panic in it is recovered and logged once, and the cleanup proceeds. An observer that
  blocks would block the unload, which is the fault the watchdog exists to report; the
  watchdog's own observer only records a time in a table.
- **Components.** `fiber.go`, `effect.go`, `tree.go`. **Dependencies.**
  [DH-D-CORDIS-1](#dh-d-cordis-1). **Priority.** P1, with the watchdog.
- **Outcome.** `DH-O-1`, `DH-O-14`. **Done when.** `DH-T-CORDIS-15` passes.

<a id="dh-d-cordis-11"></a>
### DH-D-CORDIS-11 Read-only accessors for a fiber's children and provided services

- **Result.** `Fiber.Children()` returns the fibers created under a fiber, and
  `Fiber.Provides()` the names of the services it currently provides. The inspector's
  tree page draws them ([../areas/x-inspector-timeline.md](../areas/x-inspector-timeline.md),
  `DH-D-XINSPECT-1`). Name, state, error, injections and effect labels are readable
  already (`src/go/cordis/fiber.go:160-219`).
- **How.** Both take the tree lock, copy and return, like the existing read-only
  accessors, and deliver no notification. The slices are copies, so a caller cannot
  change the tree through them, and the result is a snapshot that may be out of date as
  soon as it is returned.
- **Access, not ownership.** A `*Fiber` carries `Dispose`, as every fiber handle in the
  port does, so the accessor adds no power that the existing creation subscription does
  not already give. The fiber's owner is still the context that created it; a reader
  such as the inspector must only read.
- **Components.** `fiber.go`. **Dependencies.** None. **Priority.** P2: its one user is
  an experimental, default-off page.
- **Outcome.** `DH-O-1`, `DH-O-14`. **Done when.** `DH-T-CORDIS-16` passes.

## 7. Risks and open questions

- **A cleanup that never returns still blocks its fiber's unload.** DH-D-CORDIS-1 makes
  the wait complete, not bounded. Settled: the harness adds a watchdog, owned by area
  A21, that reports the fiber and the label of an effect whose cleanup exceeds a limit
  ([../areas/boot.md](../areas/boot.md), `DH-D-BOOT-8`), fed by
  [DH-D-CORDIS-10](#dh-d-cordis-10). The fiber still stays unloading until the process
  restarts; the framework does not kill goroutines.
- **`internal/dispatch` is not provided.** Its one user vetoes an event by throwing from
  an observer (W6). An event that needs a veto is declared as an around key
  ([DH-D-CORDIS-7](#dh-d-cordis-7)) by the area that owns it.
- **Volatile config.** Settled for the loader: a live config value is delivered through
  the component's update hook, and schema volatility metadata is not ported
  ([DH-ADR-LOADER-7](cordis-loader.md#dh-adr-loader-7); the harness `live` tag of
  [DH-ADR-SCHEMA-5](schemastery.md#dh-adr-schema-5) is a helper a component calls
  inside that hook). The volatile reference type of [cosmokit.md](cosmokit.md) is not
  built ([DH-ADR-COSMOKIT-2](cosmokit.md#dh-adr-cosmokit-2)).
- **Schema under the lock.** `src/go/README.md` (lines 229 to 231) says a component's
  schema runs under the tree lock. The code calls it after unlocking, in `Update`
  (`fiber.go:270-279`) and in `execute` (`fiber.go:779-784`). This design relies on the
  code: `DH-D-CORDIS-5` and `DH-D-LOADER-4` read injected services while config is
  resolved, which would deadlock under the lock. The README is to be corrected with the
  first change to `src/go`; this specification cannot edit it.
- **The .NET port.** Whether it follows these changes was not asked of the owner. Until
  it is, the rule that every .NET test has a Go counterpart
  ([AR-ADR-2](../../architecture.md#ar-adr-2)) holds in one direction only: the Go
  tests `DH-T-CORDIS-1` to `-16` have no .NET counterpart.
- **Cleanup concurrency differs from upstream on purpose.** Upstream runs a fiber's
  asynchronous cleanups concurrently (`vendor/cordis/src/fiber.ts:742`); the port runs
  them one at a time in reverse order (`fiber.go:729-733`). Keep the port's behaviour: it
  removes an ordering caveat upstream documents (`docs/cordis-tutorial/02-lifecycle-and-effects.md:94`).

## 8. Verification

All tests are local Go tests in `src/go/cordis`, run with `go test -race`. The framework is
in-process and has no real-environment scenario of its own; the areas that use it define theirs.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-CORDIS-1` | local | [DH-D-CORDIS-1](#dh-d-cordis-1), `DH-LIB-CORDIS-R2` | A cleanup calls `Effect`, `Provide`, `On` and `Use` during a restart and during a dispose; each returns `ErrInactive`; the next load starts with an empty effect list. |
| `DH-T-CORDIS-2` | local | [DH-D-CORDIS-1](#dh-d-cordis-1), `DH-LIB-CORDIS-R1`, `DH-LIB-CORDIS-R5` | Goroutine A disposes a handle whose cleanup blocks on a channel; goroutine B disposes the same handle and the fiber. B does not return, and the provider's cleanup does not run, until the channel is released. |
| `DH-T-CORDIS-3` | local | [DH-D-CORDIS-1](#dh-d-cordis-1) | An effect attached to a pending fiber is released when the fiber is disposed. A creation handler that disposes the new fiber leaves it `Disposed` with its body never run. A panicking handler does not stop the others. |
| `DH-T-CORDIS-4` | local | [DH-D-CORDIS-2](#dh-d-cordis-2), `DH-LIB-CORDIS-R3` | A body blocks on `Lifetime().Done()`; dispose, restart and provider withdrawal each unblock it with the expected cause; each load sees a fresh context; a pending fiber's context is already cancelled. |
| `DH-T-CORDIS-5` | local | [DH-D-CORDIS-3](#dh-d-cordis-3) | A value set on a context is read from a derived context and from a child fiber, and is absent from the parent and from siblings. |
| `DH-T-CORDIS-6` | local | [DH-D-CORDIS-3](#dh-d-cordis-3), `DH-LIB-CORDIS-R7` | With two tagged listeners, one untagged and one global, a filtered dispatch reaches exactly the admitted set in each of `Emit`, `Notify`, `Parallel`, `Bail` and `Waterfall`; nested filters combine. |
| `DH-T-CORDIS-7` | local | [DH-D-CORDIS-4](#dh-d-cordis-4), `DH-LIB-CORDIS-R6` | Of three listeners the first returns an error and the second panics; the third still runs; two entries appear on the owners' loggers; a listener removed mid-dispatch is skipped. |
| `DH-T-CORDIS-8` | local | [DH-D-CORDIS-5](#dh-d-cordis-5), `DH-LIB-CORDIS-R8` | The cases of `scripts/cordis-config-access.spec.ts:14-91` and `scripts/cordis-core-lifecycle.spec.ts:122-176`: resolving a valid or invalid candidate changes neither raw nor effective config; an in-place update publishes; a stale, rejected or superseded one does not; a pending fiber has no effective config until it loads. |
| `DH-T-CORDIS-9` | local | [DH-D-CORDIS-5](#dh-d-cordis-5) | A config hook sees its own fiber's candidate, can transform it and can reject it; it is gone after its owner unloads. |
| `DH-T-CORDIS-10` | local | [DH-D-CORDIS-6](#dh-d-cordis-6), `DH-LIB-CORDIS-R10` | A component subscribes with `OnFiber` and `OnStatus`, then unloads; later lifecycle changes do not call it. |
| `DH-T-CORDIS-11` | local | `DH-LIB-CORDIS-R4`, `DH-LIB-CORDIS-R9`, `DH-LIB-CORDIS-R11`, `DH-LIB-CORDIS-R12` | Regression: the port's existing tests (`src/go/README.md` counts 37 in `cordis`) stay green with the race detector after all eleven deliverables, and each case in `scripts/cordis-core-rc12.spec.ts:36-143` that is not about JavaScript typing is mapped to a named Go test in the README's sync table. |
| `DH-T-CORDIS-12` | local | [DH-D-CORDIS-7](#dh-d-cordis-7), `DH-LIB-CORDIS-R13` | An around key whose request and result types differ: three listeners run outermost first and the terminal last; a listener that does not call `next` returns its result and the inner ones never run; a listener's error reaches the caller and the outer listeners; a panicking listener and a panicking terminal each give the caller an error that names the fiber; a filtered dispatch admits exactly the admitted and global listeners; a listener removed mid-dispatch is skipped; an unloaded fiber's listener is gone. |
| `DH-T-CORDIS-13` | local | [DH-D-CORDIS-8](#dh-d-cordis-8), `DH-LIB-CORDIS-R14` | In a tree with `WithStrictConfig`: JSON config with an unknown key, and JSON followed by trailing data, each fail the load with a `ConfigError` naming the key or the trailing data, and the body never runs; the same input loads for a component declared `LenientConfig`; a typed Go value is unaffected. In a tree without the option the unknown key is ignored as before. |
| `DH-T-CORDIS-14` | local | [DH-D-CORDIS-9](#dh-d-cordis-9), `DH-LIB-CORDIS-R15` | A tree created with `WithExporters()` and given writers in place of the process's standard streams writes nothing to them while a component logs at every level; one created with a memory exporter delivers every message to it, including those logged during the root's unload; `New()` with no option keeps the console exporter. |
| `DH-T-CORDIS-15` | local | [DH-D-CORDIS-10](#dh-d-cordis-10), `DH-LIB-CORDIS-R16` | With an observer on the tree: disposing a fiber with three labelled effects yields a start and an end per effect, in reverse order of registration, with the fiber and label; a cleanup that blocks has a start and no end until it is released; a cleanup's error is on its end event; a panicking observer is logged once and the unload completes; the root's own unload is observed. |
| `DH-T-CORDIS-16` | local | [DH-D-CORDIS-11](#dh-d-cordis-11), `DH-LIB-CORDIS-R17` | `Children` lists the fibers created under a fiber and drops one after it is disposed; `Provides` lists a service after `Provide` and drops it after the provider unloads; changing a returned slice changes nothing in the tree; both are safe under the race detector while the tree changes. |

## 9. Units dispositioned by this spec

- `UP-PKG-vendor.cordis`
- `UP-DEBT-marker.vendor/cordis/src/fiber.ts:613`, `UP-DEBT-marker.vendor/cordis/src/fiber.ts:82`,
  `UP-DEBT-marker.vendor/cordis/src/reflect.ts:409`
- `UP-DEBT-found.F1.1` to `UP-DEBT-found.F1.6` (defined by this spec)
