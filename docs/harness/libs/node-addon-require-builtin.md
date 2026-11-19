# node-addon-require-builtin and node-addon-native-custom-loader: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Short form: sections 1, 2, 3, 4, 5, 8 and 9 of the template.

| | |
|---|---|
| **Library ID** | `DH-LIB-REQUIREBUILTIN` |
| **Upstream unit** | `UP-DEP-node-addon-require-builtin` (the family also contains `node-addon-native-custom-loader` and nine platform packages, which appear in the inventory only as pin units) |
| **Upstream version** | 0.1.9 for every member (`pnpm-lock.yaml:26688-26752`) |
| **Kind** | runtime, native (a prebuilt Node addon per operating system, processor and C library) |
| **Used by** | apps, boot, vendor |
| **Go choice** | dropped: nothing replaces it, because the thing it reaches does not exist in a Go program |
| **Related areas** | boot and plugin loading; [cordis-plugins.md](cordis-plugins.md) (`DH-ADR-LOADER-8`); [../design.md](../design.md) (`DH-ADR-4`) |

## 1. What it is and why upstream needs it

Node keeps the code that loads JavaScript modules in private modules such as
`internal/modules/esm/loader`. A program cannot `require` them unless Node is started
with the flag `--expose-internals`. `node-addon-require-builtin` is a native addon with
one function, `requireBuiltin(id)`, that returns such a private module without the flag.

A correction to this agent's brief, which described the two packages as existing "to
load native code inside a single-executable Node build": the source shows a different
purpose. They do not load native code for upstream. They give upstream's JavaScript
access to **Node's own module loader**, so that it can replace loaded code and redirect
where packages are found. Their link to the single-executable and desktop builds is
only that those builds must carry the addon's binary
(`apps/desktop/tests/fixtures/runtime-payload-smoke.mjs:136-137`).

`node-addon-native-custom-loader` is never imported by upstream code. It is a dependency
of the addon and of each platform package (`pnpm-lock.yaml:34639-34698`). Its source was
not read, because the upstream checkout has no installed `node_modules`. Inference from
its name and position: it is the shim that picks and loads the right prebuilt binary.

Upstream needs this access for three things, all consequences of loading plugins as npm
packages into one Node process: replacing a plugin's code while the process runs (hot
reload), making an `import` inside a plugin resolve to the package version a *profile*
selected, and making Node forget a cached `package.json` after a plugin is installed.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `vendor/loader/src/internal.ts:108-118`, `:131-143` | The cordis loader fetches Node's ESM loader object to find, drop and re-create module jobs for hot reload. It first tries `--expose-internals`, then the addon. It must tell two incompatible shapes of that object apart, because the shape changed inside the Node 24 line | `requireBuiltin('internal/modules/esm/loader')` |
| `packages/boot/hmr/src/package-manifest.ts:69-98` | Reads five private modules (`internal/bootstrap/realm`, `internal/modules/package_json_reader`, `internal/modules/esm/module_map`, `internal/modules/cjs/loader`, `internal/modules/esm/loader`) and temporarily replaces methods on them to clear Node's cached package manifests | `requireBuiltin(id)` |
| `packages/boot/app-boot/src/profile-resolution/resolver.ts:733-773` | Wraps Node's ESM and CommonJS resolve functions so a package request from inside a profile is routed to that profile's copy; refuses to start on an unknown loader shape (`.agents/notes/implemented/architecture/2026-09-09-profile-resolution-generations.md:82`) | `requireBuiltin` for five ids |
| `apps/cli/package.json:102` | Declared so the published command-line package installs it | dependency only |
| `packages/experimental/webworker-runtime/src/node/external_packages/node-addon-require-builtin.ts:13-32` | A stand-in for the browser preview that answers the same five ids with objects imitating Node v24.18.0 | none |

## 3. Tech debt upstream carries

Search performed: every import and `require` of both package names in `vendor/`,
`packages/`, `apps/` and `scripts/`; `patches/` (none); every entry of
`pnpm-workspace.yaml` naming them. The underlying debt, reaching into Node's private
loader, is already recorded as `UP-DEBT-found.F2.2` in [cordis-plugins.md](cordis-plugins.md).

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-pin.minimumReleaseAgeExclude.node-addon-require-builtin@0.1.9`, `UP-DEBT-pin.minimumReleaseAgeExclude.node-addon-native-custom-loader@0.1.9` | The addon works only with Node and V8 builds whose "fingerprint" it recognises. Desktop moved to Electron 44.7.0, so an addon release that recognises it had to be installed at once, bypassing the rule that blocks packages younger than a waiting period. Electron is pinned to match (`pnpm-workspace.yaml:108-109`) | `pnpm-workspace.yaml:71-82` | **Resolved** by [DH-ADR-REQUIREBUILTIN-1](#dh-adr-requirebuiltin-1): no harness code depends on private interfaces of its runtime, so no component has to be re-released for each runtime build |
| The nine platform units: `...node-addon-require-builtin-darwin-arm64@0.1.9`, `-darwin-x64`, `-linux-arm64-gnu`, `-linux-arm64-musl`, `-linux-x64-gnu`, `-linux-x64-musl`, `-win32-arm64-msvc`, `-win32-ia32-msvc`, `-win32-x64-msvc` (each prefixed `UP-DEBT-pin.minimumReleaseAgeExclude.`) | The same exemption, repeated for each prebuilt binary, because the policy matches exact package names | `pnpm-workspace.yaml:73-81` | **Resolved** by the same decision: with no addon there is no per-platform binary to exempt |
| `UP-DEBT-pin.allowBuilds.node-addon-require-builtin` | pnpm refuses any package with an install script until it is listed; this one is listed as denied because its script is not needed | `pnpm-workspace.yaml:36-41` | **Not applicable.** Install scripts are an npm mechanism; downloading a Go module runs no code |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-REQUIREBUILTIN-R1` | No harness behaviour depends on a private interface of the Go runtime or standard library. A new Go release may change the harness's speed, never whether it starts. | the fingerprint coupling, `pnpm-workspace.yaml:71`; the shape test at `vendor/loader/src/internal.ts:122-129` |
| `DH-LIB-REQUIREBUILTIN-R2` | The three needs behind the addon are met without touching a module loader: changed plugin code takes effect by restarting that plugin's process; a profile selects components and plugins by name from explicit registries; installing a plugin needs no cache to be cleared in the host. | section 2, rows 1 to 3 |

## 5. Go choice

**Decision.** Dropped. There is no Go module, no own code and nothing to build.

**Why the need disappears in Go.** A Go binary has no run-time module loader. All
first-party code is linked into the binary when it is built, and third-party code runs
as a separate process ([DH-ADR-4](../design.md#dh-adr-4)). So there is no loader state
to reach into, no resolver to wrap and no manifest cache to clear. Hot reload of code
becomes a process restart ([DH-ADR-LOADER-8](cordis-plugins.md#dh-adr-loader-8)).

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Drop (chosen)** | Removes a native addon, nine platform binaries, a version coupling to the runtime and twelve policy exemptions | In-process code reload is gone (already decided by DH-ADR-LOADER-8) | not applicable |
| Load code in process with Go's `plugin` package | Would keep in-process plugins | Supported only on Linux, FreeBSD and macOS, so not on Windows; requires the host and every plugin to be compiled with exactly the same toolchain version, build tags and flags: the same version coupling in a new form. Contradicts DH-ADR-4 | `go doc plugin` on Go 1.27.4, section "Warnings"; not evaluated further |

**Tradeoff.** None beyond DH-ADR-LOADER-8's: a first-party code change needs a new binary.

<a id="dh-adr-requirebuiltin-1"></a>
### DH-ADR-REQUIREBUILTIN-1 Harness code never reaches into runtime internals

- **Decision.** Harness packages contain no `//go:linkname` directive and no assembly or
  `unsafe` access to unexported symbols of the Go runtime or standard library. A
  dependency that does so is named in its library specification with the Go versions it
  supports.
- **Why.** `//go:linkname` is Go's equivalent of `requireBuiltin`: it binds to a private
  symbol and breaks when that symbol changes. Upstream's use of the Node equivalent
  produced a runtime-shape test, a refusal to start on unknown versions and a standing
  need to install unaged releases. The Go linker already rejects most such references by
  default (`go tool link` lists `-checklinkname`, default true, on Go 1.27.4); this
  decision forbids the rest and forbids switching the check off.
- **Tradeoff.** A few optimisations that depend on runtime internals are unavailable.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-15](../design.md#dh-o-15).
- **Verified by.** `DH-T-REQUIREBUILTIN-1`.

## 8. Verification

Nothing here has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-REQUIREBUILTIN-1` | local | `DH-LIB-REQUIREBUILTIN-R1`, `DH-ADR-REQUIREBUILTIN-1` | A Go test parses every file of the harness module and fails on a `//go:linkname` directive or an assembly file, and fails if the build configuration passes `-checklinkname=0` to the linker |

`DH-LIB-REQUIREBUILTIN-R2` is proven by the tests and scenarios of DH-ADR-LOADER-8 and of
the plugin-process specification, not here.

## 9. Units dispositioned by this spec

- `UP-DEP-node-addon-require-builtin`: not applicable.
- `UP-DEBT-pin.allowBuilds.node-addon-require-builtin`: not applicable.
- The twelve `UP-DEBT-pin.minimumReleaseAgeExclude.*` units for
  `node-addon-native-custom-loader@0.1.9`, `node-addon-require-builtin@0.1.9` and the nine
  platform packages: resolved.

Go-side items declared: `DH-LIB-REQUIREBUILTIN`, `DH-ADR-REQUIREBUILTIN-1`,
`DH-T-REQUIREBUILTIN-1`.
