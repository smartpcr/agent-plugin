# ajv: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". The shared analysis and the Go mechanism are in [zod.md](zod.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-AJV` |
| **Upstream unit** | `UP-DEP-ajv` |
| **Upstream version** | 8.20.0 as a direct dependency; 6.15.0 also present in `pnpm-lock.yaml` through other packages |
| **Kind** | runtime in one package (`packages/boot/app-boot`), test-only in two (`apps/cli`, `packages/experimental/cua-macos`) |
| **Used by** | boot; tests in apps and experimental |
| **Go choice** | named Go module `github.com/google/jsonschema-go`, the module already chosen in [zod.md](zod.md) |
| **Related areas** | boot and config loading |

## 1. What it is and why upstream needs it

ajv ("Another JSON Schema Validator") takes a JSON Schema document and a value and says
whether the value conforms. It is the standard JavaScript library for that.

Upstream uses it very little, and not for what its name suggests. Plugin config is
validated by schemastery and tool arguments by an in-house validator, so ajv validates
neither. Its one runtime job is inside the **config-schema projector**: the code that
converts every plugin's schemastery schema into one JSON Schema document so that an
editor can offer completion and warnings while someone edits `cordis.yml`. While
converting, the projector must decide whether a field may be left out, which depends on
whether the field's default itself satisfies the field's rules; ajv answers that question
(`packages/boot/app-boot/src/config-schema/projector.ts:144`, `:395`). Its other job is in
tests, which use it to prove that documents the harness generates are valid JSON Schema
and accept and reject the right examples.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/boot/app-boot/src/config-schema/projector.ts:150` | check a literal default against the schema generated for its field | `Ajv2020` from `ajv/dist/2020.js` (draft 2020-12), `validate(schema, value)`, with `strict`, formats and logging turned off |
| `packages/boot/app-boot/tests/config-schema.spec.ts:10`, `packages/boot/app-boot/tests/schemastery-json-schema.spec.ts:4` | test the projector's output | `Ajv2020`, `compile` |
| `apps/cli/tests/dump-config-schema.expected.e2e.ts:5` | test the document the CLI writes for a real profile | `Ajv2020`, `compile` |
| `packages/experimental/cua-macos/tests/protocol-generation.spec.ts:16` | test a generated protocol document against sample messages | `Ajv2020`, `compile` |

## 3. Tech debt upstream carries

Search performed: every import of `ajv` in source, tests and scripts; `patches/` and
`pnpm-workspace.yaml` (no patch, pin or exemption for ajv); the projector sources.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.F3.7` | The JSON Schema document for config is a lossy translation of a different schema system, so it ships with a list of "limitations" where the two disagree: fields whose optionality is "unknown", conversions that are not executed, and regular expressions that mean different things to JavaScript and to JSON Schema (a second library, a regular-expression parser, exists only to detect that). A JSON Schema validator is loaded at run time only to patch one gap in the translation. | `packages/boot/app-boot/src/config-schema/projector.ts:13`, `:144`, `:199`; `packages/boot/app-boot/src/config-schema/pattern.ts:1` | Resolved by [DH-ADR-SCHEMA-2](zod.md#dh-adr-schema-2): the JSON Schema document is derived from the same Go type that config is decoded into, so there is no translation and no list of disagreements. Defaults are checked when the schema is derived (`DH-LIB-AJV-R1`). |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-AJV-R1` | A default value that does not satisfy the constraints of its own field is found when the schema is derived, at startup and in tests, not when a user first relies on it. | `packages/boot/app-boot/src/config-schema/projector.ts:395` |
| `DH-LIB-AJV-R2` | Any JSON Schema document the harness writes out (config, tools, plugin protocol) can be checked in a test to be a valid draft 2020-12 document and to accept and reject given examples. | the four test use sites |
| `DH-LIB-AJV-R3` | A JSON Schema document received from outside (an external plugin or MCP server) can be used to validate values, or is refused with a reason; it is never trusted unchecked. | Inference from DH-ADR-4: external plugins describe their tools in JSON Schema |

## 5. Go choice

**Decision.** No separate validator library. The module chosen in [zod.md](zod.md),
`github.com/google/jsonschema-go`, validates values against a schema
(`Resolved.Validate`) and validates the defaults inside a schema
(`ResolveOptions.ValidateDefaults`).

**Why.** Its two functions are exactly ajv's two upstream jobs, and using one schema model
for deriving and validating is the point of [DH-ADR-SCHEMA-2](zod.md#dh-adr-schema-2).

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `github.com/google/jsonschema-go` | Already needed; default checking built in | Validates drafts 2020-12 and 07 only; patterns use Go regular-expression syntax | v0.4.3, 2026-04-17, MIT; `go list -m -json` and `LICENSE` in the module cache, 2026-11-18 |
| `github.com/santhosh-tekuri/jsonschema/v6` | Covers every draft; detailed error output | A second schema model beside the one used for derivation | v6.0.3, 2026-06-28, Apache-2.0; same method, 2026-11-18 |

**Tradeoff.** A document written for an older draft, or using a JavaScript-only regular
expression, cannot be validated locally. The harness refuses to validate against it and
says why (`DH-LIB-AJV-R3`); what a tool does next is decided in [zod.md](zod.md) section 6.

## 6. Design notes

The harness never calls the module directly; it goes through the `schema` package
described in [zod.md](zod.md). `schema.Of[T]` resolves the derived document with default
checking on, so `DH-LIB-AJV-R1` needs no extra step from component authors.
`schema.FromJSON(doc)` resolves an external document and returns an error naming the
unsupported draft, keyword or pattern.

Verdict on the upstream design: **drop** the projector and its pattern checker (1,094
lines under `packages/boot/app-boot/src/config-schema/`). **Keep** the result it
produces, a JSON Schema document for a composed profile that an editor can use
(`DH-LIB-ZOD-R11`), now assembled from derived schemas.

Failure and ownership: resolving a document allocates memory only. A resolved external
document is owned by the registration of the plugin that supplied it and is dropped when
the fiber that owns that plugin process unloads.

## 7. Risks and open questions

- The risks of the module (0.x API, error completeness, regular-expression dialect) are
  recorded once, in [zod.md](zod.md) section 7.
- Which command writes the profile schema, and whether it needs to import component code
  the way upstream's does (`packages/boot/app-boot/src/config-schema/index.ts:11`), is for
  the boot specification.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-ZOD-6` (defined in [zod.md](zod.md)) | local | `DH-LIB-AJV-R1` | A default outside its bounds fails derivation. |
| `DH-T-ZOD-10` (defined in [zod.md](zod.md)) | local | `DH-LIB-AJV-R2` | Exported documents resolve as valid draft 2020-12. |
| `DH-T-AJV-1` | local | `DH-LIB-AJV-R3` | `schema.FromJSON` accepts a draft 2020-12 document and validates examples for and against it; it returns a descriptive error for an unsupported draft, an unknown `$ref` and a pattern Go cannot compile. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-ajv`, `UP-DEBT-found.F3.7`.
