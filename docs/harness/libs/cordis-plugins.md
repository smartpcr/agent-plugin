# cordis companion plugins (hmr, logger-console, timer): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Paths that start with `src/go/` are in this repository; every
> other `path:line` is relative to the upstream root.

| | |
|---|---|
| **Library ID** | `DH-LIB-CORDISPLUGINS` |
| **Upstream unit** | `UP-PKG-vendor.hmr`, `UP-PKG-vendor.logger-console`, `UP-PKG-vendor.timer` |
| **Upstream version** | hmr 1.0.15, logger-console 1.0.0, timer 1.1.4, vendored (`vendor/README.md:19-21`) |
| **Kind** | vendored, runtime |
| **Used by** | timer: 15 package manifests. logger-console: one test fixture. hmr: no package (see §2). |
| **Go choice** | own code: `src/go/timer` and the logger in `src/go/cordis` as they are, plus a reload notification in `src/go/loader` and a logging component in `src/harness` |
| **Related areas** | boot (the product's own reload package), extensions (plugin processes), and [cordis-loader.md](cordis-loader.md) |

## 1. What it is and why upstream needs it

These are three small plugins that sit beside the loader.

**hmr** stands for hot module replacement: changing a running program without restarting
it. It watches files and reacts in one of four ways (`vendor/hmr/src/index.ts:129-155`):

1. The file is a config file mounted by an include: re-read it, so the entry tree follows the edit (`:146-152`).
2. The file is a loaded source module: after a debounce, find the plugins whose import graph contains it, clear Node's module caches, import the new code and restart those plugins with their old config. If the import or a restart fails, restore the caches and the old plugins (`:252-401`).
3. The file belongs to the framework itself: call `loader.exit()` so the host can restart the process (`:136`).
4. Anything else: emit `hmr/change` for whoever cares (`:154`).

**logger-console** prints log messages to the terminal. It registers one exporter with the
cordis logger (`vendor/logger-console/src/shared.ts:57`) and formats each line with a
timestamp, a coloured logger name, an optional time-since-last-message and aligned labels
(`shared.ts:73-97`).

**timer** gives every plugin `timeout`, `interval`, `throttle` and `debounce` whose
lifetime belongs to the calling fiber: when the plugin unloads, its timers stop, so no
callback runs after its owner is gone (`vendor/timer/src/index.ts:29-150`).

## 2. How upstream uses it

| Use site | What it is used for | Finding |
|---|---|---|
| `packages/bundle/base/cordis.patch.yml:27-32` | The base bundle mounts `@deepseek-ai/dsh-hmr` with `root: []`, under the comment "Profile configuration reloads by default; module roots are opt-in" | The product's reload plugin is `packages/boot/hmr`, which replaces the vendored one (`packages/boot/hmr/README.md:36`); no package manifest depends on `@deepseek-ai/cordis-plugin-hmr`. The shipped default is behaviour 1 only, and code reload is a developer option. |
| `docs/architecture.md:29` | Headless, SDK and ACP profiles disable reload; `sdk-minimal` omits it | Reload is switched on and off from configuration. |
| `packages/session/session-telemetry-otel/tests/fixtures/telemetry.patch.yml:27` | The only mount of logger-console | No shipped bundle mounts it. |
| `packages/bundle/base/cordis.patch.yml:24-25`, `packages/bundle/sdk-minimal/cordis.patch.yml:98` | Bundles mount the timer | Outside tests the helpers are called twice, both `ctx.debounce`, both in reload code (`packages/boot/hmr/src/index.ts:444`, `vendor/hmr/src/index.ts:127`). |

Inference: of the three, only config reload and the debounce it needs carry weight in the
shipped product. The product's own reload package, `packages/boot/hmr`, has its own specification.

## 3. Tech debt upstream carries

Search: the assigned `UP-DEBT` units, `FIXME|TODO|XXX|@deprecated` in the three packages,
and the local-modification log in `vendor/README.md` (V1, V9, V24).

| Debt ID | What it is | Evidence | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-deprecated.vendor/timer/src/index.ts:18` | `ctx.setTimeout()` kept as a deprecated alias of `ctx.timeout()` | `vendor/timer/src/index.ts:18-21` | Resolved: the Go timer has one name per operation and never had the alias ([DH-ADR-LOADER-10](#dh-adr-loader-10)) |
| `UP-DEBT-deprecated.vendor/timer/src/index.ts:23` | `ctx.setInterval()` kept as a deprecated alias of `ctx.interval()` | `vendor/timer/src/index.ts:23-26` | Resolved, same decision |
| `UP-DEBT-found.F2.2` | Code reload reaches into Node's private module loader, through `--expose-internals` or a native addon, and tracks two incompatible internal API shapes | `vendor/loader/src/internal.ts:108-143`, `vendor/hmr/src/index.ts:79-81`, `:313-332` | Not applicable: it exists only because Node has no public module-cache API. Go does not reload code in process ([DH-ADR-LOADER-8](#dh-adr-loader-8)) |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-CORDISPLUGINS-R1` | An edit to a configuration layer applies to the running harness without a restart. A burst of saves is handled once. An invalid edit is rejected and the running configuration is unchanged. | `vendor/hmr/src/index.ts:146-152`, `packages/boot/app-boot/README.md:104` |
| `DH-LIB-CORDISPLUGINS-R2` | One entry can be restarted on request without changing its config or touching its siblings. | `vendor/hmr/src/index.ts:354-361` |
| `DH-LIB-CORDISPLUGINS-R3` | After a reload settles, observers receive one notification that says what changed and what failed. | `hmr/reload` at `vendor/hmr/src/index.ts:399` |
| `DH-LIB-CORDISPLUGINS-R4` | Reload can be turned off by configuration; changes then apply at the next start. | `docs/architecture.md:29` |
| `DH-LIB-CORDISPLUGINS-R5` | Log output is readable on a terminal, has a level threshold, and never goes to standard output when standard output carries a protocol. The output is released with the component that set it up. | `shared.ts:57`, `shared.ts:73-97` |
| `DH-LIB-CORDISPLUGINS-R6` | A timer belongs to the fiber that created it. No callback runs after that fiber unloads, and a pending wait is cancelled. | `vendor/timer/src/index.ts:35-52`, `scripts/cordis-timer.spec.ts:83` |

## 5. Go choice

**Decision.** Keep the Go timer and the Go logger as they are. Replace hmr: config reload
stays (the loader's layer source already watches), and code reload becomes a restart.

**Why.** [DH-ADR-4](../design.md#dh-adr-4) compiles first-party components into the binary
and runs external plugins as processes. There is no module graph to patch at run time.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Own code on the standard library (chosen)** | Already ported and tested for timer and logger; nothing to vet | Config files are polled, not watched by the operating system | `src/go/timer/timer_test.go:28-128`; polling by [AR-ADR-15](../../architecture.md#ar-adr-15) |
| Go `plugin` package for in-process code reload | Closest to upstream | A loaded plugin "cannot be closed", and plugins are supported only on Linux, FreeBSD and macOS; ruled out by DH-ADR-4 | Package comment in the Go 1.27.4 source, `src/plugin/plugin.go:14` and `:28`, read 2026-11-18 |
| A file-notification module for watching | Lower latency than polling | A dependency for a handful of small files | `github.com/fsnotify/fsnotify` v1.10.1, BSD 3-clause text in its LICENSE, released 2026-05-04 (`go list -m -json` and the module cache, 2026-11-18). The choice belongs to the spec for `UP-DEP-chokidar`. |

**Tradeoff.** A developer changing first-party Go code rebuilds and restarts. Up to one poll interval (100 ms by default) passes before a config edit is seen.

### 5.1 What each hmr behaviour becomes

| Upstream behaviour | Meaning once components are compiled in | Verdict |
|---|---|---|
| Re-read a config file on change | Unchanged | Keep. Layer source with poll watch ([DH-D-LOADER-7](cordis-loader.md#dh-d-loader-7)), existing watch at `src/go/loader/configfile.go:342-374` |
| Restart the plugins that import a changed module | First-party: none. External plugin: its process is the unit of code, so restart the entry whose fiber owns the process | Replace with `Restart(id)` ([DH-D-LOADER-11](#dh-d-loader-11)). What triggers it (a watch on the plugin's files, or a command) belongs to the extensions area. |
| Roll back to the old module when the new one fails | The old process is already stopped. The entry's fiber fails and stays failed until told to retry ([AR-ADR-9](../../architecture.md#ar-adr-9)) | Change. Whether a plugin host starts the new process before stopping the old one is an open question (§7). |
| `loader.exit()` when framework files change | Rebuild and restart the binary | Drop from the runtime; a build-tool concern ([DH-ADR-LOADER-6](cordis-loader.md#dh-adr-loader-6)) |
| `hmr/change` for other files | Template and asset edits during UI development ([DH-ADR-3](../design.md#dh-adr-3)) | Not a loader concern; left to the UI area |
| `hmr/reload` after plugins restart | One notification after a reload settles | Keep as `loader.Reloaded` (DH-D-LOADER-11) |
| Debounce (100 ms) and ignore globs (`vendor/hmr/src/index.ts:412-422`) | A burst of saves is one reload. Globs are needed only where a directory is watched | Keep the debounce with `timer.Debounce`; globs go with the plugin-directory watch |

### 5.2 Timer: upstream API to Go

| Upstream | Go (`src/go/timer/timer.go`) | Status |
|---|---|---|
| `ctx.timeout(cb, delay)` | `Timeout` (`:43`) | present |
| `ctx.timeout(delay)` as a promise that rejects on unload | `Sleep`, which returns `cordis.ErrInactive` on unload (`:98`) | present |
| `ctx.interval(cb, delay)` | `Interval` (`:67`) | present |
| `ctx.interval(delay)` as an async iterator | not ported (`src/go/README.md:280-281`); use a loop around `Sleep` | dropped |
| `ctx.throttle(cb, delay, noTrailing)` | `Throttle` (`:138`) | present |
| `ctx.debounce(cb, delay)` with call arguments | `Debounce` (`:122`); arguments are captured by the closure | present |
| `.dispose()` on a throttled or debounced function (`vendor/timer/src/index.ts:123`) | no early cancel; the schedule ends with its owner | gap, accepted (§7) |
| Helpers mixed into every context | `timer.Key.Get(ctx)`; `ForContext` binds the service to the caller (`:34-39`) | replaced |
| `setTimeout`, `setInterval` aliases | none | dropped |

### 5.3 logger-console: upstream feature to Go

| Upstream | Go (`src/go/cordis/logger.go`) | Status |
|---|---|---|
| An exporter registered by a plugin and removed with it | `AddExporter` returns a remove function (`:111`) | present |
| A default console exporter | Installed on every tree, info and debug to standard output (`:92`, `:230-234`) | present, and a hazard for R5; the harness creates its tree without it ([DH-D-CORDIS-9](cordis.md#dh-d-cordis-9)) |
| Per-logger-name `levels` | Threshold per subtree through an intercept (`LoggerConfig`, `:54`, `:197-205`) | replaced |
| Timestamp template, colours, label alignment, time since last message | Fixed `15:04:05 [L] name text` (`:237`) | dropped as cosmetic; `SlogExporter` (`:264`) hands formatting to a `log/slog` handler |
| `%o` through `util.inspect`; a browser build | Go format verbs; no browser runtime | not applicable |

### 5.4 Decisions

<a id="dh-adr-loader-8"></a>
#### DH-ADR-LOADER-8 Config reload stays; code reload becomes a restart
- **Decision.** The harness reloads configuration live. It never replaces code in process. A first-party code change needs a new binary. An external plugin's code change restarts that plugin's process through its entry.
- **Why.** DH-ADR-4, and `UP-DEBT-found.F2.2`: upstream code reload depends on Node internals that changed shape between Node 24.11 and 24.12 (`vendor/loader/src/internal.ts:131-143`). The shipped default is already config-only (§2).
- **Tradeoff.** The edit-and-see loop for first-party code is a rebuild. State held in a plugin process is lost on its restart. **Serves.** DH-O-2. **Verified by.** DH-T-LOADER-12, DH-E-LOADER-1.

<a id="dh-adr-loader-9"></a>
#### DH-ADR-LOADER-9 Console logging is the cordis logger plus `log/slog`
- **Decision.** No port of logger-console. The harness creates its tree with no console exporter ([DH-D-CORDIS-9](cordis.md#dh-d-cordis-9)), and a harness logging component installs one `SlogExporter` whose destination, level and format come from config.
- **Why.** R5. No shipped bundle mounts logger-console (§2), and the SDK and ACP profiles speak a protocol on standard output, where a stray log line corrupts the stream.
- **Tradeoff.** No colours or aligned labels unless a slog handler adds them. **Serves.** DH-O-2. **Verified by.** DH-T-LOADER-13.

<a id="dh-adr-loader-10"></a>
#### DH-ADR-LOADER-10 Keep `src/go/timer` unchanged
- **Decision.** Use the existing timer. Do not add the deprecated aliases, the async iterator or call arguments.
- **Why.** R6 is met and tested. Upstream itself calls the helpers at two sites outside tests (§2). A Go loop around `Sleep` is the idiom the iterator stands for.
- **Tradeoff.** A debounced or throttled function cannot be cancelled before its owner unloads. **Serves.** DH-O-2. **Verified by.** Existing tests, `src/go/timer/timer_test.go:28-128` (historical evidence, not re-run here).

## 6. Design notes

**Ownership.** A timer is an effect of the calling fiber. The logging component's fiber
owns the exporter and any log file it opens, and closes both on unload. A plugin process
belongs to the fiber of its entry; `Restart` asks that fiber to unload and load, so the
process is stopped by its owner and never by the loader directly.

**Failure and cleanup.** A reload that fails to parse leaves the running tree alone and is
reported in the notification. `Restart` of an entry whose load then fails leaves the entry
failed and reported; siblings keep running, and nothing is undone. If the logging
component fails to open its destination it fails to load. The exporter that boot passed
when it created the tree still collects warnings and errors for the start-up report
([../areas/boot.md](../areas/boot.md), `DH-D-BOOT-5`), and nothing is written to
standard output.

<a id="dh-d-loader-11"></a>
#### DH-D-LOADER-11 Restart one entry, and announce a settled reload
- **Result.** A plugin host can restart the entry that owns a plugin process, and anything that shows configuration can refresh after a reload (R2, R3).
- **How.** `Service.Restart(id)` disposes the entry's fiber and runs `ensure` again with the same options; it changes neither the tree nor any file. After each `Apply` settles, the loader emits a typed event `loader.Reloaded` with the ids created, updated, restarted and removed, and the issues found. The layer source debounces file changes with `timer.Debounce` when the timer service is present.
- **Components.** `src/go/loader` (`loader.go`, `layerset.go`). **Depends on.** [DH-D-LOADER-7](cordis-loader.md#dh-d-loader-7). **Priority.** P1. **Outcome.** DH-O-2. **Done when.** DH-T-LOADER-12 passes and scenario DH-E-LOADER-1 passes.

> **DC-3 override note (M1 story, 2026-11-22).** "The layer source debounces file changes
> with `timer.Debounce` when the timer service is present" is overridden by owner decision
> M1-D-FND-3 (a) in [the M1 decision log](../../stories/harness-DSH-M1/decisions.md)
> (gap M1-G-FND-5). The layer debounce owns its timer: `LayerSet` waits for
> `LayerSetOptions.Debounce` of quiet with its own `time.Timer` (`src/go/loader/layerset.go`,
> the watch loop) and does not take the timer service. The behaviour is the same: changes
> within the window become one reload. The deliverable text above is unchanged (DC-2).

<a id="dh-d-loader-12"></a>
#### DH-D-LOADER-12 Logging component for the harness
- **Result.** Operators choose where logs go, and protocol output on standard output stays clean (R5).
- **How.** A component in `src/harness` with config for destination (standard error or a file), level and format (text or JSON). On load it adds a `SlogExporter` as an effect of its fiber; on unload the effect removes the exporter and closes the file. It neither clears nor restores a default exporter: the tree has none, because boot creates it with the exporters it chooses.
- **Components.** `src/harness` logging package. **Depends on.** [DH-D-CORDIS-9](cordis.md#dh-d-cordis-9). **Priority.** P0. **Outcome.** DH-O-2. **Done when.** DH-T-LOADER-13 passes.

## 7. Risks and open questions

1. **Start-before-stop for plugin processes.** Upstream keeps the old code when new code fails. A plugin host could start the new process, wait for its handshake and only then stop the old one. That is a decision of area A22 ([../areas/extensions.md](../areas/extensions.md)), as is what triggers `Restart`.
2. **The scenario's inspection and configuration commands** are defined by area A24 ([../areas/app-cli.md](../areas/app-cli.md): `dsh config show` and the configuration commands); DH-E-LOADER-1 names them only by purpose.
3. **The product's own reload package** (`packages/boot/hmr`) serialises reloads with plugin-manager writes. Area A21 builds on `loader.Reloaded`, the layer source and its write guard ([DH-D-LOADER-13](cordis-loader.md#dh-d-loader-13)) instead of a second watcher ([../areas/boot.md](../areas/boot.md), `DH-D-BOOT-7`).
4. **Early cancel of a debounce** is missing in Go. No harness need is known; if one appears it is a small change to `src/go/timer`.
5. **Default exporter.** Settled: the tree option is [DH-D-CORDIS-9](cordis.md#dh-d-cordis-9).

## 8. Verification

Local tests and the real-environment scenario are separate kinds. Nothing here has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-LOADER-12` | local | DH-D-LOADER-11, R2, R3 | `Restart` reloads one entry while a sibling's start count is unchanged and no file is written; five saves inside the debounce window produce one `loader.Reloaded`, which lists the changed ids |
| `DH-T-LOADER-13` | local | DH-D-LOADER-12, R5 | With the component loaded and a pipe as standard output, an info message reaches the configured destination and the pipe stays empty; after unload the exporter is gone and the file is closed |

<a id="dh-e-loader-1"></a>
#### DH-E-LOADER-1 A running harness follows an edit to its profile patch

Proves DH-D-LOADER-2, DH-D-LOADER-7 and DH-D-LOADER-11 on a real file system and process.

- **Given.** The harness binary built from a recorded commit, run on a Windows 11 machine and on a Linux machine (no remote lab is involved; the test controller runs on the same machine). A temporary harness home holding one read-only bundle layer and one writable user patch with comments. Reload enabled. Two built-in probe entries, `probe-a` and `probe-b`, that report their config and start count through the CLI inspection command.
- **When.** (1) Start the binary against the temporary home. (2) Save the user patch the way an editor does, by writing a temporary file and renaming it, changing `probe-a`'s config. (3) Save invalid YAML. (4) Save a corrected file. (5) Disable `probe-b` through the harness's own configuration command. (6) Stop the harness.
- **Then.** After (2), within two seconds `probe-a` reports the new value, the process id is unchanged and `probe-b`'s start count is unchanged. After (3), a diagnostic names the file, line and column, and both probes report their previous state. After (4), the edit applies. After (5), the user patch contains an override for `probe-b`, still contains its comments, and the bundle layer's hash is unchanged. After (6), the exit code is 0 and no temporary file remains beside the patch.
- **Cleanup.** Delete the temporary home. If the process has not exited after ten seconds, kill it and keep the home directory and the log for diagnosis. Record the commit, operating system build, commands, diagnostic text and file hashes with the result.

## 9. Units dispositioned by this spec

`UP-PKG-vendor.hmr` (replaced), `UP-PKG-vendor.logger-console` (merged), `UP-PKG-vendor.timer` (ported),
`UP-DEBT-deprecated.vendor/timer/src/index.ts:18` and `:23` (resolved), `UP-DEBT-found.F2.2` (not applicable).
