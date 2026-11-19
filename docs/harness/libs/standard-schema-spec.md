# @standard-schema/spec: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". The shared analysis and the Go mechanism are in [zod.md](zod.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-STANDARDSCHEMA` |
| **Upstream unit** | `UP-DEP-@standard-schema/spec` |
| **Upstream version** | 1.1.0 (`pnpm-lock.yaml`) |
| **Kind** | runtime dependency by declaration, but it contains TypeScript types only and no executable code |
| **Used by** | the vendored framework: `vendor/cordis` and `vendor/schemastery` |
| **Go choice** | own code that already exists: the `cordis.Schema` interface in the Go cordis port |
| **Related areas** | the cordis port and its loader (specifications named in [../design.md](../design.md)) |

## 1. What it is and why upstream needs it

Standard Schema is a small agreement between JavaScript schema libraries. A library that
follows it puts a property named `~standard` on every schema, holding a `validate`
function that returns either a value or a list of issues. Code that only needs "check
this value" can then accept a schema from any such library without importing it. The
package `@standard-schema/spec` holds the TypeScript types for that agreement.

Upstream's framework, cordis, uses it so that it does not depend on one schema library: a
plugin's `Config` is typed as "any Standard Schema" (`vendor/cordis/src/registry.ts:104`)
and the framework calls `Config['~standard'].validate(config)` before starting the plugin
(`vendor/cordis/src/fiber.ts:83`). schemastery implements the property
(`vendor/schemastery/src/index.ts:290`). The MCP SDK's schemas expose the same property
(`packages/mcp/mcp-client/src/tools.ts:402`), so harness code calls it on those too.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `vendor/cordis/src/registry.ts:104`, `:144` | the type of a plugin's `Config` export | `StandardSchemaV1` |
| `vendor/cordis/src/fiber.ts:27`, `:53`, `:83` | validate config; turn issues and their paths into an error | `StandardSchemaV1.Issue`, `~standard.validate` |
| `vendor/schemastery/src/index.ts:154`, `:290` | schemastery's implementation of the property | `StandardSchemaV1.Props` |
| `vendor/loader/src/config/diff.ts:8` | detect that a schema is schemastery | `~standard.vendor` |
| `packages/mcp/mcp-client/src/tools.ts:402`, `packages/experimental/cua-mcp/src/server.ts:125` | validate MCP results with the SDK's own schemas | `~standard.validate` |
| `packages/experimental/timeline/src/config.ts:167` | add a cross-field rule by replacing the property | `Object.defineProperty` on `~standard` |

## 3. Tech debt upstream carries

Search performed: every reference to `standard-schema`, `StandardSchemaV1` and
`~standard` in `packages/`, `apps/`, `scripts/` and `vendor/`; `pnpm-workspace.yaml` and
`patches/` (nothing for this package).

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.F3.5` | The neutral interface is not neutral in practice. The loader recognises schemastery by a vendor string and reads its private structure; the config-schema generator accepts only schemastery; a plugin that needs a cross-field rule overwrites the property on its schema object. The interface allows asynchronous validation, which the framework rejects at run time with a `TODO`. | `vendor/loader/src/config/diff.ts:7`; `packages/boot/app-boot/src/config-schema/native.ts:10`; `packages/experimental/timeline/src/config.ts:166`; `vendor/cordis/src/fiber.ts:82` | Resolved: metadata is read from the Go config type by reflection, not from the schema object, so nothing needs to know which schema implementation is in use ([DH-ADR-SCHEMA-1](zod.md#dh-adr-schema-1)). Cross-field rules are a `Validate()` method (`DH-LIB-ZOD-R7`). The Go interface is synchronous by its signature. |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-STANDARDSCHEMA-R1` | The framework checks config through one small interface and does not import the package that implements the check. | `vendor/cordis/src/registry.ts:104` |
| `DH-LIB-STANDARDSCHEMA-R2` | A failed check yields a list of issues, each with a message and the path of the field, in one error type that callers can inspect. | `vendor/cordis/src/fiber.ts:27` |
| `DH-LIB-STANDARDSCHEMA-R3` | The check is synchronous and has no side effects, so it can run on every load and every update. | `vendor/cordis/src/fiber.ts:82` |

## 5. Go choice

**Decision.** Nothing new. The Go cordis port already defines the equivalent:
`cordis.Schema`, an interface with one method, `Validate(config any) (any, error)`
(`src/go/cordis/component.go:24`), and `cordis.ConfigError` with a list of
`ConfigIssue{Message, Path}` (`src/go/cordis/errors.go:43`). The harness `schema` package
([zod.md](zod.md)) is one implementation of that interface.

**Why.** A Go interface is the language's built-in form of this agreement: any type with
the method satisfies it, with no shared package of type definitions. The port keeps its
standard-library-only rule (DH-ADR-5) because the implementation lives in the harness.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** the existing `cordis.Schema` interface | Already written; framework stays free of schema dependencies | The schema sees the decoded value only, so it cannot report unknown keys (open question in [zod.md](zod.md) section 7) | read in `src/go/cordis/component.go` and `errors.go`, 2026-11-18 |
| Make the port call the harness `schema` package directly | One less indirection | Breaks the port's standard-library-only rule and ties the framework to one implementation | not applicable |

**Tradeoff.** No asynchronous validation. Inference: none is needed, since upstream
rejects it too; a check that needs input or output belongs in the component's start-up,
where a fiber owns it and can cancel it.

## 6. Design notes

The harness does not use `cordis.Schema` to discover field metadata (descriptions, live
fields, defaults). Those come from `schema.Of[C]` on the config type. This is the fix for
`UP-DEBT-found.F3.5`: the interface carries only "check this value", and nothing reaches
through it.

The interface creates no resource. A schema value is owned by the component definition,
which is a package-level variable and lives for the whole process.

## 7. Risks and open questions

- Strict decoding of unknown keys: see [zod.md](zod.md) section 7.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-STANDARDSCHEMA-1` | local | `DH-LIB-STANDARDSCHEMA-R1`, `DH-LIB-STANDARDSCHEMA-R2`, `DH-LIB-STANDARDSCHEMA-R3` | A build check shows the cordis module imports nothing outside the standard library. A component using `schema.Config` and one using a hand-written `cordis.SchemaFunc` both load; both report bad config as a `ConfigError` whose issues carry paths; validating twice gives the same result and leaves the input unchanged. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-@standard-schema/spec`, `UP-DEBT-found.F3.5`.
