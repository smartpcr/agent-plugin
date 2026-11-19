# typescript: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". The shared analysis and the Go mechanism are in [zod.md](zod.md).
> The typert packages belong to another specification; this one covers only what they and
> the repository scripts use the TypeScript compiler for.

| | |
|---|---|
| **Library ID** | `DH-LIB-TYPESCRIPT` |
| **Upstream unit** | `UP-DEP-typescript` |
| **Upstream version** | 6.0.3 (`pnpm-lock.yaml`); peers are allowed `>=5 <7` (`pnpm-workspace.yaml:24`) |
| **Kind** | build-only in effect: declared as a runtime dependency of `packages/typert/generator`, which runs only at build time (`packages/typert/generator/README.md:12`); development dependency of eight other packages |
| **Used by** | typert; the repository root and its `scripts/`; apps, client, native, lsp and test-support as the compiler |
| **Go choice** | the Go toolchain and standard library (`reflect`, `go/parser`, `go/ast`, `go/types`, `go/doc`) |
| **Related areas** | typert and remote calls, session persistence, documentation tooling |

## 1. What it is and why upstream needs it

The `typescript` package is the TypeScript compiler: it checks types and turns TypeScript
into JavaScript. It also offers the **compiler API**, a library that parses source into a
syntax tree and answers questions such as "what are the fields of this type". Upstream
leans on that API for one underlying reason: TypeScript types exist only while compiling.
Anything that needs a type's shape at run time must ask the compiler beforehand and write
the answer into a file.

Three things need that answer. **typert** reads each package's public types and generates
runtime schemas and call descriptors, so the client can call host methods with checked
arguments. The **persistence fingerprint** tool hashes the shape of every saved session
event type, so a change to a saved format is noticed in review. The **catalog generators**
read declarations and comments to write reference pages. Beyond those, 18 `verify-*`
scripts use the syntax tree to enforce house rules.

## 2. How upstream uses it

65 files import the compiler API: 3 in the typert generator, 49 under `scripts/`, 13 others.

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/typert/generator/src/analyzer.ts:384`, `:625` (3,471 lines) | build the type graph of the host and client programs | `createProgram`, the type checker, `createSourceFile` |
| `packages/typert/generator/src/tsdown-plugin.ts:74` | rewrite decorators during bundling | `transpileModule` |
| `packages/typert/generator/src/build-output-guard.ts:13` | reject a build that reads stale generated files | module resolution |
| `scripts/persistence-schema.ts:5`, `:49` (512 lines) | fingerprint persisted types; it hands the compiler an in-memory synthetic source file naming every event type | `readConfigFile`, `createProgram`, the type checker |
| `scripts/gen-config-catalog.ts:12`, `scripts/gen-client-catalog.ts`, `scripts/gen-doc-graphs.ts`, `scripts/doc-typecheck.ts` | generate and check reference documents | syntax tree, JSDoc access |
| `scripts/verify-*.ts` (18 files, for example `scripts/verify-no-unknown-casts.ts`) | repository rules checked on the syntax tree | `createSourceFile`, tree walking |
| every package | type checking and compilation | the `tsc` command |

## 3. Tech debt upstream carries

Search performed: every import of `typescript`; `pnpm-workspace.yaml` and `patches/` (one
peer-version allowance, no patch); the typert READMEs; the persistence cookbook. The
generator's "Known Limitations" bullets are inventory units owned by the typert specification.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.F3.8` | Because types are erased at run time, upstream maintains a 9,544-line build-time generator and a separate 512-line fingerprint extractor that each run a full compiler program. Generated files can be stale or missing, which needs its own guard and failure modes; the generator supports only a subset of the type language; and the workspace must widen the allowed compiler version for peers. | `packages/typert/generator/README.md:79`, `:159`; `packages/typert/generator/src/analyzer.ts:384`; `scripts/persistence-schema.ts:49`; `pnpm-workspace.yaml:22` | Resolved by [DH-ADR-SCHEMA-4](zod.md#dh-adr-schema-4): Go types are available at run time through `reflect`, so schemas and fingerprints are computed in the running program and no generated file exists to go stale. |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-TYPESCRIPT-R1` | A clean checkout of the harness compiles and type-checks with the Go toolchain alone. No file has to be generated first. | `packages/typert/generator/README.md:79` (the stale-output guard) |
| `DH-LIB-TYPESCRIPT-R2` | The shape of any registered type (remote method input and output, tool input, event) is available in the running program without a build-time step. | typert, job 7 in [zod.md](zod.md) |
| `DH-LIB-TYPESCRIPT-R3` | Persisted types have fingerprints (this is `DH-LIB-ZOD-R10`). | `scripts/persistence-schema.ts:45` |
| `DH-LIB-TYPESCRIPT-R4` | Tools that must read source (a rule check, or a reference page that quotes doc comments) use the Go parser and type checker from the standard library. | the catalog and `verify-*` scripts |

## 5. Go choice

**Decision.** No library. Compilation and type checking are `go build` and `go vet`.
Run-time type shapes come from `reflect`, through the harness `schema` package
([zod.md](zod.md)). Source-reading tools use `go/parser`, `go/ast`, `go/types`, `go/doc`.

**Why.** The need behind most of upstream's compiler-API code, recovering erased types,
does not exist in Go. Reading source for rules and documents is covered by the toolchain.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** reflection at startup, standard-library source tools | No generated files; no generator to maintain; nothing can be stale | Reflection cannot see doc comments, so descriptions are struct tags ([DH-ADR-SCHEMA-1](zod.md#dh-adr-schema-1)); derivation errors appear at startup, not at compile time, so a test must derive every type | Go 1.27.4 toolchain present, `go version`, 2026-11-18; the cordis port declares `go 1.24` (`src/go/go.mod`) |
| Generate schema files with `go generate` | Errors at build time; schemas reviewable as files | Brings back a generator, a generation order and stale outputs: the debt in `UP-DEBT-found.F3.8` | not applicable |

**Tradeoff.** Clients in other languages get JSON Schema files (`DH-LIB-ZOD-R11`), not
typed code. Inference: acceptable, because the host renders the UI (DH-ADR-3) and the main
consumer of upstream's generated client code no longer exists.

## 6. Design notes

| Upstream use | Verdict | Go replacement |
|---|---|---|
| Build-time type graph and schema emission (typert generator) | drop | Register a method or tool with its Go types; `schema.Of` derives the document then. The registration API is owned by the typert specification. |
| Separate host and client programs ("faces") | drop | There is no client program (DH-ADR-3). |
| Decorator rewriting | not applicable | Go has no decorators; registration is an explicit call. |
| Persistence fingerprint extractor | change | `Fingerprint()` on the derived schema and a golden-file test ([zod.md](zod.md) section 6). |
| Config, tool and service catalogs from source | change | A Go command prints catalogs from the registered components' derived schemas; only pages that quote doc comments parse source. |
| Syntax-tree rule checks | judged one by one by the specification that owns `scripts/` | `go vet` analyzers or tests where a rule still applies; many are TypeScript-specific and disappear. |

Failure and ownership: reflection creates nothing that needs releasing. A registration is
owned by the registering component's fiber and removed when it unloads; the registry that
holds it enforces that, not this library.

## 7. Risks and open questions

- Start-up cost of reflection is assumed small, not measured; it can be made lazy per type.
- Open, typert area: the shape of remote-call registration and how a registered method is
  exposed over the plugin protocol. Open, documentation tooling: which catalogs are kept.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-TYPESCRIPT-1` | local | `DH-LIB-TYPESCRIPT-R1`, `DH-LIB-TYPESCRIPT-R2`, `DH-LIB-GENMAPPING-R1` | On a clean checkout, `go build ./...` and `go vet ./...` succeed with no prior command. A repository check finds no `go:generate` directive that produces schema or call-descriptor code. A registry test registers a method and reads back its input and output documents. |
| `DH-T-ZOD-9` (defined in [zod.md](zod.md)) | local | `DH-LIB-TYPESCRIPT-R3` | Fingerprint stability and the golden-file comparison. |
| `DH-T-TYPESCRIPT-2` | local | `DH-LIB-TYPESCRIPT-R4` | A sample rule check built on `go/parser` and `go/types` reports a seeded violation in a fixture package and passes a clean one; the tool's module has no third-party parser dependency. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-typescript`, `UP-DEBT-found.F3.8`.
