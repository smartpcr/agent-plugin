# cosmokit: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Reasoning that was not read from source opens with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-COSMOKIT` |
| **Upstream unit** | `UP-PKG-vendor.cosmokit` |
| **Upstream version** | cosmokit `1.8.1`, upstream commit `02e691c` (`vendor/README.md:13`), plus local modification V22. The manifest's `1.9.0` is the harness release number (`vendor/cosmokit/package.json:4`). |
| **Kind** | vendored, runtime; forced to the vendored copy by an override (`pnpm-workspace.yaml:19`) |
| **Used by** | the vendored framework (cordis, loader, include, schemastery, hmr, logger-console) and four harness source files |
| **Go choice** | standard library. The volatile reference type is not built ([DH-ADR-COSMOKIT-2](#dh-adr-cosmokit-2)) |
| **Related areas** | [cordis](cordis.md); config schemas (`libs/schemastery.md`); the loader (`libs/cordis-loader.md`) |

## 1. What it is and why upstream needs it

cosmokit is a grab bag of small JavaScript helpers by the author of cordis: dictionary
and array helpers, string case conversion, time formatting, binary encoding, deep clone
and deep equality (`vendor/cosmokit/src/index.ts:1-12`). The framework packages lean on it
for things JavaScript lacks in its standard library.

The harness added one thing of substance to its copy (local modification V22,
`vendor/README.md:45`): the **volatile reference**. A config field marked volatile is
handed to a plugin as a small object with a `get()` method instead of a plain value. When
the config file changes only in such fields, the loader swaps the value behind the
reference and the plugin is not restarted. The plugin calls `get()` each time it needs
the value (`vendor/cosmokit/src/volatile.ts:8-12`); an example is `maxParallelToolCalls`
(`packages/core/agent-loop/src/index.ts:257`). Only the framework may write a reference:
the write function is hidden behind a symbol (`vendor/cosmokit/src/volatile.ts:3`,
`:84-86`). The loader owns the value; the plugin only has access to read it.

## 2. How upstream uses it

Counted with `git grep` on 2026-11-18 over `packages/`, `apps/`, `vendor/` and `scripts/`:
37 import lines, 22 distinct names. Only the names below are imported anywhere.

| Use site (`path:line`) | What it is used for | API used | Go equivalent |
|---|---|---|---|
| `vendor/cordis/src/*.ts` (12 lines), `vendor/loader/src/*` | Types for "a string-keyed map" and "a value or a promise of it" | `Dict`, `Awaitable`, `Promisify` | `map[string]T`; the promise types have no Go counterpart because Go calls block |
| `vendor/cordis/src/fiber.ts:1`, `reflect.ts:1`, `service.ts:1`, `utils.ts:1` and 4 more | Hidden properties on objects; null checks | `defineProperty`, `isNullable`, `isNonNullable` | Struct fields; `== nil` |
| `vendor/cordis/src/logger.ts:248` | Logger name from a plugin name | `hyphenate` | Not needed: the port names a logger after its fiber as is |
| `vendor/logger-console/src/shared.ts:79`, `:93` | Timestamps and elapsed time in console logs | `Time.template`, `Time.format` | `time.Time.Format`, `time.Duration.String` |
| `vendor/loader/src/config/diff.ts:3`, `config/entry.ts:2`, `vendor/include/src/index.ts:3`, `journal.ts:2`, `vendor/schemastery/src/index.ts:1` | Did this config entry change? | `deepEqual` | `reflect.DeepEqual` on decoded values; see `DH-LIB-COSMOKIT-R2` |
| `vendor/schemastery/src/index.ts:1`, `vendor/loader/src/config/utils.ts:20`, `vendor/include/src/journal.ts:80` | Map over, filter, pick and omit keys of an object; copy a value | `valueMap`, `filterKeys`, `pick`, `omit`, `clone`, `isPlainObject` | A loop, or `maps.Clone`, `maps.DeleteFunc`; a type switch on `map[string]any` |
| `vendor/schemastery/src/index.ts:1` | Base64 and hex for binary schema values | `Binary` | `encoding/base64`, `encoding/hex` |
| `vendor/schemastery/src/index.ts:2`, `vendor/loader/src/config/entry.ts:2` | Create, find and commit volatile references | `createVolatile`, `isVolatile`, `volatileEntries`, `updateVolatile`, `Volatile` | None: a live setting reaches a component through its update hook ([DH-ADR-COSMOKIT-2](#dh-adr-cosmokit-2)) |
| `packages/llm/llm-deepseek/src/config.ts:72`, `packages/llm/llm-deepseek-account/src/config.ts:4`, `packages/settings/settings/src/schema.ts:11` | Flatten a config that may hold references into plain values | `isVolatile` | Not needed: a Go config holds plain values only |
| `packages/core/agent-loop/src/index.ts:7` and 39 lines importing it through cordis | Type of a volatile config field | `Volatile<T>` | A plain field with the `live` tag, read inside the component's update hook ([DH-ADR-SCHEMA-5](schemastery.md#dh-adr-schema-5)) |

The array helpers, the other string helpers, `noop` and `is` are exported but imported by nothing.

## 3. Tech debt upstream carries

Searched: `vendor/cosmokit/src` for `FIXME`, `TODO`, `XXX` and `@deprecated` (none),
`patches/` (nothing for cosmokit), `pnpm-workspace.yaml` overrides, and `vendor/README.md`.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-pin.overrides.@deepseek-ai/cosmokit` | Every request for the package is forced to the vendored folder, with no stated reason | `pnpm-workspace.yaml:19` | Not applicable. Inference: the override keeps one copy in the install tree, because two copies would break identity checks; the reference even brands itself with a global symbol to survive that (`vendor/cosmokit/src/volatile.ts:3`, `:47-54`). Duplicate package copies are an npm property. A Go build has exactly one copy of a package, and these helpers come from the standard library. |
| `UP-DEBT-found.F1.7` | The vendored copy is no longer upstream cosmokit: V22 adds the reference type and changes what `deepEqual` means (references always equal, cycles unequal, URLs by href) | `vendor/README.md:45`, `vendor/cosmokit/src/types.ts:127-168` | Resolved by [DH-ADR-COSMOKIT-1](#dh-adr-cosmokit-1) and [DH-ADR-COSMOKIT-2](#dh-adr-cosmokit-2): there is no reference type and no vendored helper package, so there is no fork to drift, and equality is not overloaded with a hidden special case. |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-COSMOKIT-R1` | Map, slice, string, time and binary-encoding helpers are available to the framework without a third-party module. | section 2; [AR-ADR-15](../../architecture.md#ar-adr-15) |
| `DH-LIB-COSMOKIT-R2` | Two config values can be compared structurally to decide whether an entry changed. A missing field and an explicit null are told apart when the caller asks for a strict comparison. | `vendor/cosmokit/src/types.ts:127-132` |
| `DH-LIB-COSMOKIT-R3` | A config value can be replaced while its component runs, and the component sees one complete new value, never a mix of old and new. Met without a reference type: the component's update hook receives the whole new typed config ([DH-ADR-LOADER-7](cordis-loader.md#dh-adr-loader-7)). | `vendor/cosmokit/src/volatile.ts:39-45`, `:84-86` |
| `DH-LIB-COSMOKIT-R4` | **Withdrawn.** It asked that the references inside a config value be listable with their paths. With no reference type there is nothing to list; the component decides in its update hook whether it can take the change. | `vendor/cosmokit/src/volatile.ts:62-76`; `vendor/loader/src/config/entry.ts:180-191` |

## 5. Go choice

**Decision.** Use the standard library, and add nothing to `src/go`.
**Why.** Sixteen of the 22 imported names are a built-in, a short loop or a standard
library call in Go. The other six are the volatile reference, which the Go design does
not need ([DH-ADR-COSMOKIT-2](#dh-adr-cosmokit-2)).

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| Standard library only (chosen) | Nothing to vet; keeps `src/go` dependency-free | Call sites differ from upstream's names | `go doc` on 2026-11-18 with Go 1.27.4 confirmed `sync/atomic.Pointer`, `reflect.DeepEqual`, `maps.Keys`, `slices.Compact`, `encoding/hex`, `encoding/base64`, `time.Duration.String`; `src/go/go.mod` requires Go 1.24 |
| Port cosmokit as a Go utility package | Familiar names for readers of upstream | A package of wrappers around the standard library; most of it would be unused, as it is upstream | Not applicable |

No third-party module was considered: it would break AR-ADR-15 for no gain.
**Tradeoff.** No cosmokit name survives; a reader porting upstream code needs section 2.

<a id="dh-adr-cosmokit-1"></a>
### DH-ADR-COSMOKIT-1 No utility package; equality stays plain

- **Decision.** There is no Go counterpart of cosmokit as a package. Config equality is
  `reflect.DeepEqual` over decoded values, with no special case of any kind.
- **Why.** Upstream's `deepEqual` quietly answers "equal" for any two references
  (`vendor/cosmokit/src/types.ts:131`), a rule every caller has to know about.
- **Tradeoff.** None beyond the lost names: a reader porting upstream code needs section 2.
- **Serves.** `DH-O-1`. **Verified by.** `DH-T-COSMOKIT-2`.

<a id="dh-adr-cosmokit-2"></a>
### DH-ADR-COSMOKIT-2 The volatile reference type is not built

- **Status.** Recommended default, not confirmed by the owner
  ([../design.md](../design.md) §9.2). It applies the resolution of conflict CF-3.
- **Decision.** `src/go` gains no `volatile` package. A component that can take a new
  setting without restarting registers the port's update hook and receives the new
  typed config ([DH-ADR-LOADER-7](cordis-loader.md#dh-adr-loader-7)); a field is marked
  live with a tag that the harness `schema` package turns into a helper called inside
  that hook ([DH-ADR-SCHEMA-5](schemastery.md#dh-adr-schema-5)). The deliverable
  `DH-D-COSMOKIT-1` and the test `DH-T-COSMOKIT-1` are withdrawn.
- **Why.** Upstream needs a shared reference because a plugin cannot otherwise say which
  of its fields are live. In Go the handler receives the new value and decides. No area
  specification named a component whose in-flight work cannot use the hook, which was
  the one condition under which CF-3 kept the type. A type with no user is code to
  test and maintain in a module that is meant to stay small.
- **Tradeoff.** Accepting an update is all-or-nothing per component: a handler that
  cannot apply one field declines, and the fiber restarts. A component that reads a
  setting in the middle of long work sees the new value only when its hook has run. If
  a component later needs a value it can read at any instant, the design below is the
  starting point.
- **Serves.** `DH-O-1`, `DH-O-2`. **Verified by.** `DH-T-LOADER-14`: a component
  with an update hook takes a config change without restarting.

## 6. Design notes

<a id="dh-d-cosmokit-1"></a>
### The volatile reference as first designed (withdrawn; was DH-D-COSMOKIT-1)

Kept for the record. Nothing in this section is built or tested.

- **Result it aimed at.** A component can hold a config value that the loader replaces
  without restarting the component.
- **How.** A new package `volatile` in `src/go`, standard library only:

```go
type Ref[T any] struct{ /* atomic.Pointer[T] */ }
func New[T any](value T) *Ref[T]
func (r *Ref[T]) Get() T                                 // the current complete value
func (r *Ref[T]) Commit(_ commit.Token, value T)         // framework only
func Entries(config any) []Entry                         // Entry{Path []string, Ref AnyRef}
```

  `Get` loads one pointer, so a reader sees the old or the new value, never a mix.
  `Commit` stores a pointer to a fresh value. Its first parameter has a struct type
  declared in `src/go/internal/commit`; Go only lets packages inside the `src/go` module
  import an `internal` package, so the loader can commit and harness or plugin code cannot.
  `Entries` walks structs, string-keyed maps and slices by reflection.
- **Ownership.** The fiber whose config holds the reference keeps it for one load; the
  loader is the only writer. Nothing needs releasing: a dropped reference is collected.
- **Failure.** A candidate is validated before `Commit`; an invalid one is not committed
  and the running value stays. Go cannot freeze a value, so a `T` holding a map or slice
  must be treated as read-only by readers; each commit decodes a fresh one.
- **Components it would have added.** A package `src/go/volatile` and
  `src/go/internal/commit`. It was P1 when first written, for a loader that would gain
  volatile-only updates; the loader does not.

## 7. Risks and open questions

- **The decision not to build the reference type is a recommended default**, not an
  owner decision ([DH-ADR-COSMOKIT-2](#dh-adr-cosmokit-2)). Overturning it restores the
  deliverable above and adds a row to the list of changes to `src/go`
  ([cordis.md](cordis.md) §6.1).
- A component that reads a live setting during long work sees a change only after its
  update hook has run. No area specification reports that as a problem.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-COSMOKIT-2` | local | `DH-LIB-COSMOKIT-R2`, [DH-ADR-COSMOKIT-1](#dh-adr-cosmokit-1) | Two decoded configs that differ in one nested field, in a map entry and in a slice element each compare unequal; equal values compare equal; an explicit null against a missing field compares unequal under the strict comparison; no value type gets a special case. |
| none | real environment | not applicable | cosmokit has no behaviour that depends on a real environment. Nothing here has been run. |

## 9. Units dispositioned by this spec

`UP-PKG-vendor.cosmokit`, `UP-DEBT-pin.overrides.@deepseek-ai/cosmokit`, `UP-DEBT-found.F1.7` (defined here).
