# schemastery: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Shared analysis and Go mechanism: [zod.md](zod.md). Here: plugin config.

| | |
|---|---|
| **Library ID** | `DH-LIB-SCHEMASTERY` |
| **Upstream unit** | `UP-PKG-vendor.schemastery` |
| **Upstream version** | schemastery 3.18.0 at commit `cf0b7e5` (`vendor/README.md:14`), released by upstream as `@deepseek-ai/schemastery` 3.19.0 (`vendor/schemastery/package.json:4`) |
| **Kind** | vendored (one 962-line source file, locally modified) |
| **Used by** | 202 source files in 184 packages: almost every plugin, plus the vendored loader, hot-reload and console logger |
| **Go choice** | own code: the harness `schema` package on top of the config support already in the Go cordis port |
| **Related areas** | boot and config loading, settings UI |

## 1. What it is and why upstream needs it

Every upstream plugin can be given a `config:` block in the `cordis.yml` file that
composes the product. A plugin uses schemastery to say what that block may contain: it
exports a value named `Config`, and before the plugin starts the framework runs the user's
block through it, filling in defaults and rejecting bad values (`vendor/cordis/src/fiber.ts:80`).
The same value carries display hints that the settings screens use to draw a form, strips
values equal to the defaults before config is saved, and marks fields **volatile**: a
volatile field may change while the plugin keeps running; any other change restarts it.
Upstream copies the library's source into its repository ("vendoring") so it can patch
it, and added volatile fields that way (`vendor/README.md:45`).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| 112 files that `export const Config`, for example `packages/todo/tool-todo/src/index.ts:41` | declare a plugin's config | `object`, `string`, `number`, `boolean`, `array`, `dict`, `union`, `const`; `.default` (716 calls), `.volatile` (120), `.required` (93), `.description` (22), `.role` (19), `transform` (6) |
| `vendor/cordis/src/fiber.ts:80` | validate and fill defaults before a plugin starts | the `~standard` property (see [standard-schema-spec.md](standard-schema-spec.md)) |
| `vendor/loader/src/config/diff.ts:18`; `vendor/schemastery/src/index.ts:422` | the loader decides whether an edit touched only volatile fields, and drops values equal to defaults before saving | `meta.volatile`, `dict`, `meta.default`, `simplify` |
| `packages/settings/settings/src/schema.ts`, `packages/client/modules/src/config-projection.ts`, `packages/client/ui-plugin-manager/src/client/presentation.ts` | draw settings forms | `meta.role`, `meta.description`, `meta.hidden` |
| `packages/boot/app-boot/src/config-schema/projector.ts:149`; `scripts/gen-config-catalog.ts:1` | convert to JSON Schema for editors (see [ajv.md](ajv.md)); check that every schema key exists on the declared TypeScript type | the raw schema graph; static reading of the schema source |

## 3. Tech debt upstream carries

Search performed: `pnpm-workspace.yaml`, `patches/` (none), `vendor/README.md`, the library source.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-pin.overrides.@deepseek-ai/schemastery` | A package-manager override forces every consumer onto the vendored directory. No reason is stated. Inference: the vendored loader and cordis also depend on the package and must not resolve a second copy. | `pnpm-workspace.yaml:20` | Resolved: no schema library is vendored or forked. Config is described by Go types ([DH-ADR-SCHEMA-1](zod.md#dh-adr-schema-1)) through `DH-LIB-SCHEMASTERY`. |
| `UP-DEBT-found.F3.6` | The library is a locally modified fork that must be re-patched by hand on every sync (entries V5, V7, V10, V17, V22). It keeps process-global counters, revives serialised callbacks with `new Function`, and writes "adapted" values back into the caller's input while validating. | `vendor/README.md:35`, `:39`, `:45`, `:66`; `vendor/schemastery/src/index.ts:209`, `:273`, `:764` | Resolved: nothing is forked; a schema is immutable data with no callbacks (`DH-LIB-ZOD-R12`); decoding never changes its input. |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-SCHEMASTERY-R1` | A component's config is checked before its body runs. A rejected config fails that component only, with one issue per problem and the field path; sibling components still load. A rejected update leaves the running component on its previous config. Invalid config is never repaired silently. | `vendor/cordis/src/fiber.ts:80`; `src/go/cordis/component.go:14`; change of `autofix` and `loose` (`vendor/schemastery/src/index.ts:551`, `:767`) |
| `DH-LIB-SCHEMASTERY-R2` | A field can be marked live. An edit that changes only live fields is delivered to the running component; any other edit restarts it. A live mark is allowed only on a fixed field path, not inside a list or map. | `vendor/README.md:45`; `vendor/schemastery/src/index.ts:493` |
| `DH-LIB-SCHEMASTERY-R3` | When config is written back to disk, values equal to the defaults are left out, so the file holds only what the operator chose. | `vendor/schemastery/src/index.ts:422` |
| `DH-LIB-SCHEMASTERY-R4` | For every config field the settings UI can obtain its description, bounds, allowed values, a display hint, and whether it is secret, without code specific to the component. | UI use sites in section 2 |

## 5. Go choice

**Decision.** Own code. Config is a Go struct; defaults come from `cordis.WithDefaults`,
checking from a `cordis.Schema` built by the harness `schema` package ([zod.md](zod.md)),
and metadata from struct tags. **Why.** The cordis port already decodes config onto
defaults and calls a validator (`src/go/cordis/component.go:132`); the rest can be read
from the Go type.

<a id="dh-adr-schema-5"></a>
### DH-ADR-SCHEMA-5 A live config field is a tag and an update callback, not a wrapper type

- **Decision.** A field that may change without a restart is tagged `dsh:"live"`. The
  component reads new values in an update callback (`cordis.AcceptUpdate`); the loader
  asks `schema` whether an edit touched only live fields.
- **Why.** Upstream's volatile fields turn the field's type into a reference with a
  `.get()` method, which leaks into every reader (`vendor/schemastery/src/index.ts:245`).
- **Tradeoff.** Nothing stops a component tagging a field live and ignoring updates.
- **Serves.** `DH-LIB-SCHEMASTERY-R2`. **Verified by.** `DH-T-SCHEMASTERY-2`.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** Go struct, tags, `schema` package | One declaration; compiler-checked defaults; no fork | Descriptions live in tags | builds on `src/go/cordis/component.go`, read 2026-11-18 |
| Port schemastery's builder API to Go | Familiar to upstream authors | Recreates the second declaration and its cross-check (`UP-DEBT-found.F3.2`) | not applicable |

**Tradeoff.** No `transform` (6 uses) or schema `i18n` (1 use): Go types convert, the UI translates.

## 6. Design notes

A component opts in with `cordis.WithSchema(schema.Config[C]())`. On every load and
update the port decodes the entry's JSON onto the defaults; the schema then checks tag
constraints and calls `C.Validate()` if present, returning a `cordis.ConfigError` with
field paths. `schema` also provides `LiveOnly(old, new C)` for the loader, `Minimal(defaults,
value C)` for write-back, and the tag words `secret` (masked in the UI, redacted in logs,
default left out of exports) and `ui=` (a display hint). **Failure and ownership.** A failed
check stops the load before the component body runs, so nothing exists to clean up.
Schemas are owned by no fiber ([zod.md](zod.md) section 6).

| Upstream decision | Verdict | Reason |
|---|---|---|
| Validate config before the plugin starts; strip defaults on save | keep | `DH-LIB-SCHEMASTERY-R1`, `DH-LIB-SCHEMASTERY-R3` |
| Volatile fields | keep the behaviour, change the mechanism | DH-ADR-SCHEMA-5 |
| `autofix` deletes invalid properties; `loose` substitutes the default | drop | A typo would silently become a default (`DH-LIB-SCHEMASTERY-R1`). |
| Unknown config keys pass through (`vendor/schemastery/src/index.ts:821`) | change: reported | See the open question in [zod.md](zod.md) section 7. |

## 7. Risks and open questions

- Whether config keeps load-time `!!js` expressions (`packages/boot/app-boot/src/config-schema/projector.ts:7`)
  is for the loader specification. Rejecting unknown keys needs a change in the port or loader.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-SCHEMASTERY-1` | local | `DH-LIB-SCHEMASTERY-R1` | Load a component with out-of-range and missing-required config: the body does not run, issues carry paths, a sibling loads. Update a running component with bad config: it keeps the old config. No value is replaced by a default. |
| `DH-T-SCHEMASTERY-2` | local | `DH-LIB-SCHEMASTERY-R2` | `LiveOnly` is true for an edit to a live field and false when any other field differs; a live tag inside a list fails derivation. With a fake component, a live-only edit calls the update callback and does not restart. |
| `DH-T-SCHEMASTERY-3` | local | `DH-LIB-SCHEMASTERY-R3` | `Minimal` of the defaults is empty; of one changed field, only that field; decoding the result onto defaults gives back the value. |
| `DH-T-SCHEMASTERY-4` | local | `DH-LIB-SCHEMASTERY-R4` | For a struct using every tag word, the derived document carries description, bounds, enum and the `x-dsh-*` keywords; a secret field's default is absent from the exported document. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-PKG-vendor.schemastery`, `UP-DEBT-pin.overrides.@deepseek-ai/schemastery`, `UP-DEBT-found.F3.6`.
