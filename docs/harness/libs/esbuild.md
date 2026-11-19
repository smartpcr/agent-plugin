# esbuild: Go specification

> **Status: design, not built.** Short form. Upstream facts were read at the pinned commit
> recorded in [../README.md](../README.md). Statements that are reasoning and not read
> from source open with "Inference".
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The earlier
> revision kept open a possible new use of esbuild as a run-time library; the owner's
> decision removes it (section 5).

| | |
|---|---|
| **Library ID** | `DH-LIB-ESBUILD` |
| **Upstream units** | `UP-DEP-esbuild`, `UP-DEBT-pin.allowBuilds.esbuild` |
| **Upstream version** | 0.28.2 (the lockfile also holds 0.21.5, 0.25.12 and 0.28.1 for other tools) |
| **Kind** | development and build only; never part of a packaged product |
| **Used by** | `boot` (`hmr`) and `vendor` (`vendor/hmr`) directly; the script runner and the front-end bundler indirectly |
| **Go choice** | the Go toolchain: nothing is transpiled or bundled |
| **Related areas** | build and test toolchain ([build-test-toolchain.md](build-test-toolchain.md)) |

## 1. What it is and why upstream needs it

esbuild is a fast compiler for TypeScript and JavaScript: it removes types, converts
module formats and bundles files. It is written in Go and distributed to npm as a
prebuilt native program per platform.

The question asked of this document was what esbuild bundles at run time and what at
build time. The answer is: **nothing at run time in a packaged product, and almost
nothing directly at build time either.** Upstream source imports esbuild once, for a
type. It is present because two development tools depend on it: the script runner
(`tsx`), which uses it to compile each TypeScript file as Node imports it when the
harness runs from source, and the front-end bundler (Vite). Packaged builds are produced
by a different bundler (`tsdown`), not by esbuild.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | When |
|---|---|---|
| `packages/boot/hmr/src/error.ts:2`, `:6-10`; `vendor/hmr/src/error.ts:2` | A type import (`BuildFailure`). The hot-reload error handler recognises a compile failure by its shape and prints its location; it never calls esbuild. | Development only: such a failure can only arise when source is compiled on import |
| `packages/boot/hmr/package.json:53`, `vendor/hmr/package.json:57` | Declared as a development dependency for that type | Development |
| `pnpm-lock.yaml:35963-35965` (`tsx`), `:293-299` (Vite and the test runner built on it) | A dependency of the script runner, the front-end bundler and the test runner | Development, tests, front-end build |
| `apps/desktop/tsdown.config.ts:76`, `apps/web/tests/focus-rings.e2e.ts:261` | A Vite option (`esbuild: { jsx: 'automatic' }`) selecting how JSX is compiled | Front-end build and one test |

## 3. Tech debt upstream carries

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-pin.allowBuilds.esbuild` | The package manager refuses to run any dependency's install script unless it is listed. esbuild is listed because its install script selects and checks the native program for the platform. Installing the repository therefore runs third-party code. | `pnpm-workspace.yaml:26-32` | Resolved. Go has no install scripts: `go mod download` fetches source and runs nothing, and every dependency is compiled from source by `go build`. There is nothing to allow-list. |

No patch names esbuild (`patches/` searched). Four versions are installed side by side
because different tools pin different ranges; that is npm resolution and has no Go
counterpart.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-ESBUILD-R1` | Building and installing the harness executes no code from a dependency and downloads no prebuilt program. | `pnpm-workspace.yaml:26-32` |
| `DH-LIB-ESBUILD-R2` | The harness does not compile its own source at run time; what runs is what was built. | `packages/boot/hmr/src/error.ts:12-16`; [DH-ADR-LOADER-8](cordis-plugins.md#dh-adr-loader-8) |

## 5. Go choice

**Decision.** The Go toolchain. `go build` compiles the harness; `go run` and
`go generate` replace the script runner; static assets are embedded. No transpiler or
bundler takes esbuild's upstream role. Verdict: **drop**.

**Why.** Each upstream use exists because TypeScript must be turned into JavaScript
before Node can run it. Go source is compiled once, ahead of time.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Go toolchain** (chosen) | Nothing to install or allow-list | None | Standard toolchain |
| Keep esbuild as a build tool for the JavaScript islands of the UI | Could bundle island scripts | A JavaScript build step, which [DH-ADR-3](../design.md#dh-adr-3) avoids; the UI stack specification decides how islands are shipped | Not examined here |

**A possible new use, considered and not taken.** Because esbuild is itself a Go
program, its transform is importable as a Go library
(`github.com/evanw/esbuild` v0.28.2, 2026-08-08, MIT, no cgo, one dependency
`golang.org/x/sys`, `pkg/api.Transform` with a TypeScript loader; checked on 2026-11-18
with `go list -m -json` and by reading the module cache). It was the candidate for
removing type annotations from model-written TypeScript programs, had the harness
embedded a JavaScript engine. The owner decided that there is no embedded interpreter:
scripts are ordinary programs that call tools through the `dsh tool` command, and the
harness never transforms their text
([DH-ADR-PTCGUEST-1](ptc-guest-runtime.md#dh-adr-ptcguest-1),
[DH-ADR-PTCGUEST-4](ptc-guest-runtime.md#dh-adr-ptcguest-4)). So esbuild is not a
dependency of the Go design in any role.

**Tradeoff.** None for the upstream role.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-ESBUILD-1` | local | `DH-LIB-ESBUILD-R1`, `DH-LIB-ESBUILD-R2` | With a populated module cache, the release build succeeds with network access to the module proxy disabled (`GOPROXY=off`, `GOFLAGS=-mod=readonly`) and with cgo off; the produced binary starts with no source tree present. |

Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-esbuild`, `UP-DEBT-pin.allowBuilds.esbuild`.
