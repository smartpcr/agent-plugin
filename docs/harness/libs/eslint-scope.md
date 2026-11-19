# eslint-scope and eslint-visitor-keys: Go specification

> **Status: design, not built.** Short form. Upstream facts were read at the pinned commit
> recorded in [../README.md](../README.md). Statements that are reasoning and not read
> from source open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-ESLINTSCOPE` |
| **Upstream units** | `UP-DEP-eslint-scope`, `UP-DEP-eslint-visitor-keys` |
| **Upstream version** | `eslint-scope` 9.1.2, `eslint-visitor-keys` 5.0.1 |
| **Kind** | runtime dependency of one experimental package; in effect a packaging-time tool |
| **Used by** | `experimental` (`webworker-runtime`) |
| **Go choice** | dropped: no JavaScript modules are rewritten |
| **Related areas** | experimental liveshare and Web Worker runtime (`areas/x-liveshare-webworker.md`) |

## 1. What it is and why upstream needs it

`eslint-scope` works out, for a JavaScript syntax tree, which declaration each name
refers to. `eslint-visitor-keys` lists the child fields of each kind of tree node so the
analysis can walk the tree.

Upstream has an experimental mode in which the whole harness runs inside a browser Web
Worker, for preview deployments (`packages/experimental/webworker-runtime/README.md:12`).
The harness is written as ES modules, and the worker loads modules through a CommonJS
wrapper. An "image packer" therefore rewrites every module: `import` and `export`
statements become reads of a required module object. Scope analysis tells a read of an
imported name apart from a local variable with the same name. These libraries have
nothing to do with model-written programs.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/experimental/webworker-runtime/src/compile/transform.ts:27`, `:619` | Decide which identifiers that spell an import's name really read the import, only for a module that also declares or assigns that name (`:13-19`) | `analyze(program, { ecmaVersion: 2022, sourceType: 'module', childVisitorKeys })` |
| `transform.ts:28`, `:619` | The child-field table passed to the analysis | `KEYS` |

No patch, pin or exemption names either package (`patches/` and `pnpm-workspace.yaml`
searched).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-ESLINTSCOPE-R1` | No part of building, packaging or running the Go harness rewrites JavaScript module syntax. | Inference from [DH-ADR-3](../design.md#dh-adr-3) and [DH-ADR-4](../design.md#dh-adr-4) |

## 5. Go choice

**Decision.** Dropped. Nothing replaces them.

**Why.** The need is created by loading ES modules written for Node into a browser
worker. A Go program has no modules to rewrite. Inference: if the experimental-runtime
area keeps an in-browser host, the Go route is to compile the binary for WebAssembly
(`GOOS=js GOARCH=wasm`), which needs no source transform; if it drops the mode, there is
nothing to build. Either way this dependency has no counterpart. Which of the two
happens is that area's decision, not this document's.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Nothing** (chosen) | No dependency | None | Nothing to verify |
| A Go scope analyser for JavaScript | None: there is no JavaScript to analyse | Own code with no caller | Not examined |

**Tradeoff.** None.

## 8. Verification

No test is declared. `DH-LIB-ESLINTSCOPE-R1` is a property of the repository (no
JavaScript module transform exists), which the build area's rule "no JavaScript build
step" already covers. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-eslint-scope`, `UP-DEP-eslint-visitor-keys`.
