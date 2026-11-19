# Node built-ins for code loading and isolation (`node:vm`, `node:worker_threads`, `node:module`, `node:inspector`): Go specification

> **Status: design, not built.** Short form for a group of four Node built-in modules.
> Upstream facts were read at the pinned commit recorded in [../README.md](../README.md).
> Statements that are reasoning and not read from source open with "Inference". File
> counts come from a search of the upstream checkout that excluded tests, fixtures and
> snapshots; the inventory's counts differ slightly because its filter differs.
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The owner decided
> that the harness has no embedded interpreter, so the `node:vm` rows no longer point at
> an open engine choice.

| Module | Library ID | Upstream unit | Go choice |
|---|---|---|---|
| `node:vm` | none of its own: [`DH-LIB-PTCGUEST`](ptc-guest-runtime.md) | `UP-DEP-node:vm` | nothing evaluates code inside the harness: model-written programs become ordinary scripts that call tools through the `dsh tool` command (own code), and model-written plugins become plugin processes |
| `node:worker_threads` | `DH-LIB-NODEWORKER` | `UP-DEP-node:worker_threads` | standard library: goroutines and `context`; `os/exec` where a hard stop is needed |
| `node:module` | `DH-LIB-NODEMODULE` | `UP-DEP-node:module` | standard library for the three needs that survive: `embed`, `runtime/debug`, `os/exec` |
| `node:inspector` | `DH-LIB-NODEINSPECTOR` | `UP-DEP-node:inspector` | standard library: `net/http/pprof`, `runtime/trace` |

| | |
|---|---|
| **Kind** | Node built-in |
| **Related areas** | A11, programmatic tool calling ([../areas/ptc-workflow.md](../areas/ptc-workflow.md)); A22, runtime self-modification ([../areas/extensions.md](../areas/extensions.md)); A21, application boot ([../areas/boot.md](../areas/boot.md)); X3, experimental inspector ([../areas/x-inspector-timeline.md](../areas/x-inspector-timeline.md)) |

## 1. What they are and why upstream needs them

These four modules are how a Node program loads code and separates one piece of running
code from another. `node:vm` evaluates JavaScript text in a fresh set of globals (a
"realm") inside the same process. `node:worker_threads` runs JavaScript on another
thread with its own memory, joined to the parent by message ports. `node:module` is the
module loader's own interface: resolve a package name to a file, load a CommonJS file,
intercept resolution, erase TypeScript types. `node:inspector` connects to the V8
debugger of the running process.

A compiled Go binary has no realms, no module loader and no V8. So for each use the
question is not "which Go library replaces the module" but "does the need survive, and
where does it go". Most uses do not survive. The ones that do are: letting
model-written programs call tools (without evaluating them in the harness), doing work
off the main path with a way to stop it, finding a bundled asset or the product version,
and looking inside a running host.

## 2. How upstream uses them

### 2.1 `node:vm` (10 source files)

| Use site (`path:line`) | What it is used for | Go answer |
|---|---|---|
| `packages/ptc-runtime/ptc-runtime-node/src/evaluators/async-function.ts:3`, `vm-script.ts:3`, `scoped-repl.ts:4`, `scoped-repl-evaluator.ts:4`, `src/imports/vm-modules.ts:2` | Evaluate a model-written program in the child process, in four forms, and link its dynamic imports | Not evaluated by the harness. By the owner's decision there is no embedded interpreter: the program is an ordinary script run by the shell tool as a sandboxed command, and it calls tools through the `dsh tool` command ([DH-ADR-PTCGUEST-1](ptc-guest-runtime.md#dh-adr-ptcguest-1), [-2](ptc-guest-runtime.md#dh-adr-ptcguest-2)) |
| `packages/workflow/workflow-ptc/src/runtime.ts:8`, `:67-99` | Evaluate a workflow script in a second context with five helper globals and a 5 s limit on the first synchronous slice | No second context and no helper globals: what the helpers did is reached as ordinary tools through the same command, or done in the script's own language (`DH-LIB-PTCGUEST-R15`). The JavaScript script form of the workflow tool does not survive; the workflow area decides its replacement |
| `packages/workflow/workflow-ptc/src/index.ts:9`, `:63` | Compile a workflow script on the host, without running it, to reject a syntax error before a run exists | Not carried over: the harness does not parse script text ([DH-ADR-PTCGUEST-4](ptc-guest-runtime.md#dh-adr-ptcguest-4)). A syntax error is reported by the interpreter that runs the script |
| `packages/extensions/cordis-host-runner/src/sandbox.ts:14`, `:206`, `:228` | Evaluate a model-written **plugin** inside the host, with Node APIs replaced by traps. Upstream states it "is not containment" (`sandbox.ts:6-7`). | Not the guest runtime. [DH-ADR-4](../design.md#dh-adr-4): a model-written plugin is a separate, sandboxed plugin process; nothing is evaluated in the host |
| `scripts/verify-cordis-config.ts:15`, `:559` | Compile a `disabled` expression to check its syntax | Not applicable: config expressions are checked by the expression engine the loader specification chooses |
| `packages/experimental/webworker-runtime/src/node/builtin_modules/mock/vm.ts:11-26` | A stub that fails when called | Not applicable |

About forty test files also import `runInNewContext`, to make a value that belongs to
another realm and check that code handles it (for example
`packages/core/session/tests/json.spec.ts:1`). Go has one set of types per process, so
this class of defect and its tests do not exist.

### 2.2 `node:worker_threads` (23 source files)

A worker thread is used upstream for four different reasons. The Go mapping depends on
the reason, not on the API.

| Reason | Use sites (`path:line`) | Go mapping |
|---|---|---|
| **Keep slow work off the event loop.** Node runs all host code on one thread, so a long computation freezes the host. | Parse a large plugin index: `packages/boot/plugin-market/src/parse-thread.ts:1`, `:40` | A goroutine. Go has no single event loop to block. The work takes a `context.Context` and its input is size-bounded. |
| **Bound memory and force a stop.** A worker has its own heap and can be terminated. | Verify a migrated session file, at most two at once: `packages/session/session-persistence-jsonl/src/migration-verifier.ts:22`, `:142`. Collect an auxiliary crash report under one total time budget: `apps/desktop/src/operation-report-job.ts:1`, `:48` | A goroutine with a semaphore and a deadline when the work is our own Go code that checks its context. A **child process** when the work must be stoppable even if it does not co-operate, or must not share the host's memory: a goroutine cannot be killed. The session-file migration itself is not carried over ([DH-ADR-2](../design.md#dh-adr-2)). |
| **Isolate third-party code and its ambient environment.** | Run the Stagehand browser SDK with an empty environment so only explicit credentials reach it: `packages/experimental/browser-use-stagehand-native/src/worker-client.ts:1`, `:26`. Keep all DevTools protocol state away from the host: `packages/experimental/inspector/src/host/bridge/controller.ts:299`, `src/worker/entry.ts:3` | A child process with an explicitly built environment ([DH-ADR-SECURITY-6](../upstream/security.md#dh-adr-security-6)). A JavaScript SDK cannot run in a Go process at all, so it is a plugin or MCP process under [DH-ADR-4](../design.md#dh-adr-4). The inspector's worker goes with the inspector (§2.4). |
| **Carry module-loader state into workers, and parallelise the build.** | Pass the profile's package routing to every worker: `packages/boot/app-boot/src/profile-resolution/resolver.ts:1069-1070`, `worker-bootstrap.ts:3-9`. Generate type artefacts and scan sources in parallel: `packages/typert/generator/src/tsdown-plugin.ts:205`, `scripts/verify-package-dependencies.ts:736` | Not applicable: there is no module loader, and the Go toolchain parallelises builds and tests itself. |

The remaining files are the two ends of the same workers and one stub
(`packages/experimental/webworker-runtime/src/node/builtin_modules/mock/worker_threads.ts:12`).

### 2.3 `node:module` (50 source files)

| Category | Files | What it does | Go answer |
|---|---|---|---|
| Build, release and packaging scripts | 21 (`apps/desktop/scripts/*`, `scripts/*`, three `tsdown` configs, `website/.vitepress/config.ts`) | `createRequire` to find a package's files; `isBuiltin` to classify imports | Not applicable: replaced by the Go toolchain ([build-test-toolchain.md](build-test-toolchain.md)) |
| Plugin and module loading | 14 (`packages/boot/app-boot/src/profile.ts:632`, `compatibility-preflight.ts:38`, `profile-resolution/resolver.ts:4`, `service.ts:143`; `packages/boot/hmr/src/index.ts:1073`, `package-manifest.ts:4`; `vendor/hmr`, `vendor/loader/src/internal.ts:109`; `packages/typert/loader/src/index.ts:339`; `packages/util/lazy-require/src/index.ts:14`; `packages/client/modules/src/index.ts:969`; `packages/llm/plugin-package-inventory-deepseek/src/index.ts:79`; `packages/experimental/liveshare/src/host/shareable-catalog.ts:51`; the Web Worker runtime's emulation) | Search `node_modules` for a plugin by package name, intercept resolution so a profile can route packages, reach the loader's cache to reload a module, load a module lazily | Removed by [DH-ADR-4](../design.md#dh-adr-4) and [DH-ADR-LOADER-8](cordis-plugins.md#dh-adr-loader-8): components are compiled in and found in a registry; plugin processes are found in an install directory; code is never reloaded |
| Locate a dependency's files or a native add-on | 8 (`packages/bundle/web-app/src/index.ts:124-126`, `packages/subagent/subagent-codex/src/run.ts:44`, `packages/document/office-kit/src/locate.ts:56`, `packages/experimental/speech-to-text-sensevoice/src/inference.ts:41`, `native/system/packages/entry/src/index.ts:70`, `flock.ts:28`, `packages/ssh/ssh-helper-runtime/src/index.ts:21`, `python/sdk-runtime/runtime-bootstrap.mjs:64`) | Find the built web client, the Codex program, the Office engine, a native library | **Survives.** Assets that ship with the harness are embedded (`embed`). External programs are found by a configured path or on `PATH` (`os/exec.LookPath`) and reported clearly when missing. Native add-ons have no counterpart (see [node-addon-system.md](node-addon-system.md)). |
| Read the product's own version | 3 (`packages/llm/llm/src/attribution.ts:16`, `packages/session/session-telemetry-otel/src/index.ts:189`, `packages/experimental/cua-mcp/src/server.ts:154`) | `require('../package.json').version` | **Survives.** One version value from the build (`runtime/debug.ReadBuildInfo`, or a value set at link time) |
| Guest runtime internals | 4 (`packages/ptc-runtime/ptc-runtime-node/src/execution.ts:132`, `environment.ts:57`, `imports/module-hooks.ts:35`, `imports/vm-modules.ts:215`) | Erase TypeScript types; refresh `node:process` exports after emptying the environment; intercept a program's imports | Not applicable: no guest runtime is built ([DH-ADR-PTCGUEST-1](ptc-guest-runtime.md#dh-adr-ptcguest-1)). The harness erases no types and intercepts no imports; the one surviving idea, a child environment built explicitly instead of inherited, is the spawn helper's rule for every command |

### 2.4 `node:inspector` (1 source file)

| Use site (`path:line`) | What it is used for | Go answer |
|---|---|---|
| `packages/experimental/inspector/src/worker/realms/host/bridge.ts:3`, `:22-24` | Connect a worker to the host's own V8 debugger so Chrome DevTools can show the host's console, sources and breakpoints | A Go process has no V8 debugger. The DevTools interface is already dropped by [DH-ADR-CHROMEDEVTOOLSFRONTEND-1](chrome-devtools-frontend.md#dh-adr-chromedevtoolsfrontend-1). Looking inside a running host uses Go's profiler and execution tracer. |

## 3. Tech debt upstream carries

No `UP-DEBT` unit is assigned for these modules, and built-ins have no patches or pins.
Search: the consuming packages' "Known Limitations" sections. Two admitted limits are
caused by these modules and disappear with them: values crossing realms fail
`instanceof` (`packages/ptc-runtime/ptc-runtime-node/README.md:254`,
`packages/workflow/workflow-ptc/README.md:159`), and the `vm` realm for plugins is not a
security boundary (`packages/extensions/cordis-host-runner/README.md:66`). Their units
belong to the owning areas.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-NODEWORKER-R1` | Background work started by a component runs concurrently with the host, takes a `context.Context`, stops when its owning fiber unloads, and leaves no goroutine behind. | §2.2 rows 1 and 2; [DH-O-1](../design.md#dh-o-1) |
| `DH-LIB-NODEWORKER-R2` | Work that must be stoppable without its co-operation, must not share the host's memory, or runs code the harness did not write, runs in a child process started by the one spawn helper with an explicit environment. | §2.2 rows 2 and 3 |
| `DH-LIB-NODEWORKER-R3` | Concurrent background jobs of one kind are bounded (a fixed number at once), so a burst cannot exhaust memory. | `migration-verifier.ts:22` |
| `DH-LIB-NODEMODULE-R1` | Every asset the harness ships is inside the binary and is found without searching the disk. | §2.3 row 3 |
| `DH-LIB-NODEMODULE-R2` | An external program the harness needs is found from configuration or `PATH`; when it is missing, the feature reports which program and where it looked, and the host still starts. | `packages/subagent/subagent-codex/src/run.ts:44` |
| `DH-LIB-NODEMODULE-R3` | The product reports one version, fixed at build time, wherever a version is sent or shown. | §2.3 row 4 |
| `DH-LIB-NODEINSPECTOR-R1` | A developer can take a CPU profile, a heap profile and an execution trace from a running host. The endpoints are off by default and, when enabled, bound to loopback and limited to the owner. | `bridge.ts:22-24`; [DH-O-14](../design.md#dh-o-14) |

## 5. Go choice

**Decision.** The standard library for the three items declared here. What replaces the
guest runtime (`node:vm`) is decided in [ptc-guest-runtime.md](ptc-guest-runtime.md): no
embedded interpreter, and a `dsh tool` command for scripts. Verdicts: `node:vm`
evaluation **drop**; worker threads **change** (to goroutines or
processes by reason); the module-loader interface **drop**, keeping three small needs;
the V8 inspector **drop**, keeping profiling.

**Why.** Goroutines with `context` give concurrency and co-operative cancellation with
no dependency. They do not give termination or memory isolation, which is why the
second and third reasons in §2.2 map to processes. `embed` and build information replace
every "find my own files" use because a Go binary carries its assets.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Goroutines, `context`, `os/exec`, `embed`, `runtime/debug`, `net/http/pprof`, `runtime/trace`** (chosen) | No dependency; lifetimes are owned by fibers | A goroutine cannot be killed, so each background job must be classified | Standard library; nothing to verify as a module |
| A worker-pool module | Ready-made queueing | The need is one semaphore per job kind; a dependency for a few lines | Not examined |
| A Go plugin mechanism (`plugin` package) to keep run-time loading | Closest to Node's loader | Not available on Windows, requires cgo, cannot unload; already ruled out by DH-ADR-4 | Standard library documentation |
| Implement the Chrome DevTools protocol in Go | Familiar interface | Rejected with reasons in [chrome-devtools-frontend.md](chrome-devtools-frontend.md) | See that document |

**Tradeoff.** Each background job needs an explicit decision between a goroutine and a
process, where upstream uses one mechanism. Profiling endpoints show Go's view of the
host (goroutines, heap), not a JavaScript console.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-NODEWORKER-1` | local | `DH-LIB-NODEWORKER-R1`, `-R3` | A component starts more background jobs than its bound; at most the bound run at once; unloading the fiber cancels them and a goroutine-leak check passes. Run under the race detector. |
| `DH-T-NODEWORKER-2` | local | `DH-LIB-NODEWORKER-R2` | A helper process that ignores its stop request is killed within the grace period by the stop sequence, and its environment holds only the variables the spawn helper set. |
| `DH-T-NODEMODULE-1` | local | `DH-LIB-NODEMODULE-R1`, `-R2`, `-R3` | The built binary, run from an empty directory, serves an embedded asset; with an external program absent the feature's error names it and the host starts; the version in the telemetry resource, the model-request attribution and the command line are equal. |
| `DH-T-NODEINSPECTOR-1` | local | `DH-LIB-NODEINSPECTOR-R1` | With default configuration the profiling routes return not found; when enabled they answer only on loopback and only to an owner principal. |

Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-node:vm`, `UP-DEP-node:worker_threads`, `UP-DEP-node:module`,
`UP-DEP-node:inspector`.
