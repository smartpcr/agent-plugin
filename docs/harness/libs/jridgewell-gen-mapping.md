# @jridgewell/gen-mapping: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". The shared analysis and the Go mechanism are in [zod.md](zod.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-GENMAPPING` |
| **Upstream unit** | `UP-DEP-@jridgewell/gen-mapping` |
| **Upstream version** | 0.3.13 (`pnpm-lock.yaml`) |
| **Kind** | build-only in effect: a declared runtime dependency of `packages/typert/generator`, which runs only at build time (`packages/typert/generator/README.md:12`) |
| **Used by** | typert (one file) |
| **Go choice** | dropped |
| **Related areas** | typert and remote calls |

## 1. What it is and why upstream needs it

A **source map** is a small file that records, for each position in a generated file,
which position in which original file it came from. Editors and debuggers use it to jump
from generated code back to what a person wrote. `@jridgewell/gen-mapping` is a library
for building one: the caller adds mappings one at a time and asks for the encoded result.

Upstream needs it in exactly one place. The typert generator
(see [typescript.md](typescript.md)) writes a TypeScript declaration file,
`typert.remote-client.d.ts`, that describes host methods to client code. That file is
generated, so "go to definition" on a remote method would land in machine-written text.
The generator therefore also writes `typert.remote-client.d.ts.map`, which sends the
editor to the method's real declaration in the host package's source.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/typert/generator/src/emitter.ts:9`, `:396`, `:440`, `:481` | build the declaration map for the generated remote-client declarations | `GenMapping`, `addMapping`, `toEncodedMap` |

It is listed as a dependency of that one package only
(`packages/typert/generator/package.json:38`).

## 3. Tech debt upstream carries

Search performed: every reference to `gen-mapping` in `packages/`, `apps/`, `scripts/`
and `vendor/`; `pnpm-workspace.yaml` and `patches/`.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| none found | No patch, pin, exemption or workaround concerns this package. The debt it is a symptom of, generating declarations because types are erased, is recorded as `UP-DEBT-found.F3.8` in [typescript.md](typescript.md). | search above | not applicable |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-GENMAPPING-R1` | A developer navigating from a call to a remote method or tool reaches hand-written source, not generated text. | `packages/typert/generator/src/emitter.ts:396` |

## 5. Go choice

**Decision.** Dropped. The Go design generates no declaration files
([DH-ADR-SCHEMA-4](zod.md#dh-adr-schema-4)), so there is nothing to map back from.

**Why.** `DH-LIB-GENMAPPING-R1` holds without any mechanism: a remote method is an
ordinary Go function registered by an ordinary call, and the Go tooling navigates to it
directly.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** no source maps | Nothing to build or keep in step | None for Go code | follows from DH-ADR-SCHEMA-4 |
| Generate Go stubs with `//line` directives (the Go compiler's own way to point generated code at its source) | Would preserve navigation if stubs were generated | Needs the generation step that DH-ADR-SCHEMA-4 removes | `//line` is part of the Go toolchain; no module involved |

**Tradeoff.** If typed clients for other languages are generated later from the exported
JSON Schema files, their navigation would end at the schema file. That is accepted; it is
a consequence of DH-ADR-SCHEMA-4, not of this choice.

## 6. Design notes

No Go API. No resource is created, so there is nothing for a fiber to own or release.

This library is a different thing from the source maps a JavaScript bundler writes for
browser code. Whether the small JavaScript "islands" allowed by DH-ADR-3 ship source maps
is a question for the UI specifications.

## 7. Risks and open questions

None for this library.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-TYPESCRIPT-1` (defined in [typescript.md](typescript.md)) | local | `DH-LIB-GENMAPPING-R1` | The repository check that no `go:generate` directive produces schema or call-descriptor code shows that remote methods exist only as hand-written source. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-@jridgewell/gen-mapping`.
