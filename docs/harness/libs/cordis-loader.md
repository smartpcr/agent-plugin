# cordis loader, include and group: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Paths that start with `src/go/` are in this repository; every
> other `path:line` is relative to the upstream root.

| | |
|---|---|
| **Library ID** | `DH-LIB-LOADER` (own code), `DH-LIB-LOADEREXPR` (expression engine, a Go module) |
| **Upstream unit** | `UP-PKG-vendor.loader`, `UP-PKG-vendor.include`, `UP-PKG-vendor.group` |
| **Upstream version** | loader 1.0.0-rc.9, include 1.2.0, group 1.0.0, vendored with local modifications (`vendor/README.md:16-18`) |
| **Kind** | vendored, runtime |
| **Used by** | every package group: 99 other package manifests depend on the loader and 65 on include (`grep` over `package.json`) |
| **Go choice** | own code: extend `src/go/loader`; the YAML codec and the expression engine live in `src/harness` |
| **Related areas** | boot (profiles, bundles, config editor, plugin manager), extensions (plugin processes), and the companion [cordis-plugins.md](cordis-plugins.md) |

## 1. What it is and why upstream needs it

deepseek-harness is not started by code that wires objects together. It is started from a
list of **entries** in a YAML file. An entry names one plugin, gives its configuration and
says when it runs. The **loader** is the cordis service that turns the list into running
plugins (each one a *fiber*, the cordis unit of lifecycle) and keeps the two in step: when
the list changes, only the entries that changed restart. Every part of the product is a
plugin (`docs/architecture.md:9`), so the entry list is the product's composition.

Three vendored packages share the work. `loader` owns the in-memory **entry tree** and
the calls that change it. `include` binds a tree to a file: it reads YAML or JSON, applies
**patches** (overlays that insert entries or override fields of an entry found by id) and
writes runtime changes back. `group` is a three-line re-export
(`vendor/group/src/index.ts:1-3`) of the loader's `Group` plugin, an entry whose config is
a list of child entries.

The harness needs this for layered configuration. A **profile** (a named composition such
as `web` or `headless`) starts from an empty list and applies, in order, the patch files of
its **bundles** (installable sets of entries), the profile's own `cordis.patch.yml`, the
home patch and `--patch` overlays (`docs/architecture.md:19-27`). A later layer can replace
any row, which is how a user changes the product without editing shipped files.

### 1.1 The entry format

| Field | Meaning | Evidence |
|---|---|---|
| `id` | Stable name inside its tree. Generated (8 hex digits) when absent. A nested entry is addressed as `group:child`. | `vendor/loader/src/config/entry.ts:12`, `tree.ts:132-153` |
| `name` | Module specifier to import, or `cordis:<builtin>` | `entry.ts:14`, `tree.ts:226-229` |
| `config` | Value handed to the plugin. For a group it is the list of child entries. | `entry.ts:16`, `utils.ts:136` |
| `group` | Marks the entry as a group | `entry.ts:18` |
| `disabled` | Boolean or expression. A disabled entry, and everything below it, does not run. | `entry.ts:93-112`, `utils.ts:134` |
| `inject` | Extra services the entry waits for (a list), or a mapping that also carries per-service config | `entry.ts:22`, `utils.ts:138-144` |
| `isolate` | Service name to `true` (a realm private to this entry) or a label (a realm shared by entries using that label). A *realm* is a separate namespace for one service, so two providers of it do not collide. | `isolate.ts:8`, `isolate.ts:75-89` |
| `intercept` | Service name to config layered for this entry and everything below it | `isolate.ts:7`, `isolate.ts:127` |

Options are validated before the tree changes, and every problem is listed with the
`file:line:column` of the offending value (`utils.ts:119-162`, `vendor/include/src/index.ts:588-608`).
An invalid entry is logged, a running entry with the same id is disposed, and its siblings
still start (`group.ts:50-66`, `group.ts:97-99`). A shipped patch file looks like this
(`packages/bundle/base/cordis.patch.yml:15-32`, shortened):

```yaml
- insert:
    - id: plugin-manager
      name: '@deepseek-ai/dsh-plugin-manager'
      disabled: !!js "!ctx.get('profileContext')"
    - id: hmr
      name: '@deepseek-ai/dsh-hmr'
      config:
        root: []
```

### 1.2 `!!js` expressions

`!!js` is a YAML tag. Include parses the tagged scalar into a node that keeps the source
text (`vendor/include/src/index.ts:23-32`), so the expression is written back in its
original form (`entry.ts:105-106`).

- **Where.** Anywhere inside `config` (`utils.ts:12-22`), and in `disabled`, the only entry option that accepts one (`utils.ts:107`, `utils.ts:134`).
- **What it evaluates.** Arbitrary JavaScript, through `eval` inside `with (ctx)` (`utils.ts:5-9`). Names resolve first to properties of the cordis context (services such as `webStartup`, helpers such as `dshHomePath`), then to globals such as `process`.
- **When.** `config` is evaluated each time the fiber's config is resolved, after the entry's declared injections are active and against the plugin's own context (`vendor/loader/src/index.ts:124-131`, `docs/cordis-primer.md:39`). `disabled` is evaluated at each mount decision against the loader's context (`entry.ts:108-112`). Groups and includes leave their children's expressions alone; each child evaluates its own (`index.ts:129`).
- **Failure.** A `disabled` expression that throws is an error. It is not treated as "disabled" (`packages/boot/app-boot/README.md:108`).

The shipped bundle patch files contain 67 expressions (`grep '!!js' packages/bundle`), in six shapes:

| Shape | Example (`path:line`) |
|---|---|
| Environment variable with a default | `mode: !!js process.env.DSH_PERMISSION_MODE ?? 'workspace-write'` (`packages/bundle/base/cordis.patch.yml:252`) |
| Operating system switch | `disabled: !!js process.platform === 'win32'` (`base/cordis.patch.yml:257`) |
| Working directory or harness-home path | `process.cwd()` (`base/cordis.patch.yml:253`), `dshHomePath('sessions')` (`base/cordis.patch.yml:145`) |
| Launch context test | `disabled: !!js "!ctx.get('profileContext')"` (`base/cordis.patch.yml:22`) |
| Field of an injected startup service | `host: !!js ctx.webStartup.host ?? '127.0.0.1'` (`packages/bundle/web-app/cordis.patch.yml:192`) |
| Node module resolution | a `createRequire(baseUrl).resolve(...)` chain that locates a package's `skills` folder (`packages/bundle/web-app/presets/cordis.patch.yml:130`) |

### 1.3 Groups, includes and patches

A **group** holds child entries and can carry `inject` and `isolate`, which then apply to
all children. The presets use this to give a provider and its consumers one private realm
(`packages/bundle/web-app/presets/cordis.patch.yml:47-55`). An **include** is an entry
whose plugin reads another file and mounts it as a subtree; the application root is one
include over an empty list (`packages/boot/app-boot/README.md:56`, `:140`).

A **patch list** is include's overlay format (`vendor/include/src/patch.ts:105-182`):

- `insert: [...]` appends entries to the root, or to the group named by `id`.
- `{id, <key>: <value>}` overrides keys of an existing entry. Each named key is replaced whole; `config` is not deep-merged (`patch.ts:167-170`, `packages/boot/app-boot/README.md:210`). An optional `name` is an assertion, and a mismatch skips the patch (`patch.ts:156-158`). A patch that matches nothing warns.
- The result carries an **ownership index**: for every entry and every key, whether the file or a particular patch supplies it (`patch.ts:72-93`).

### 1.4 Commit, journal and write-back

Two parties change the tree: a person editing the file, and the running program (a plugin
saving its own config, or a call such as `loader.create`). Each runtime mutation is
reported synchronously as an **entry commit** (`tree.ts:59-65`, `index.ts:133-141`).
Include records commits in a **journal**: one pending record per entry id, holding a
per-key delta (`vendor/include/src/journal.ts:5-30`). One loop does the writing
(`vendor/include/src/index.ts:375-402`):

1. Re-read the file. If it changed, reconcile the journal against it key by key. When both sides changed the same key the file wins and the conflict is logged (`journal.ts:195-242`).
2. Route each record to its owner using the ownership index: file-owned changes go to the file, patch-owned changes to the patch list held in the parent entry's config (`patch.ts:189-274`).
3. Write through a temporary file and a rename, retried while the file is locked, and only while the file still holds the content last read (`index.ts:556-580`).

A missing, read-only or unparsable file keeps the running tree and leaves the changes in
memory; `flush()` rejects while changes remain unsaved (`index.ts:337-350`, `:478-533`).
A plugin that disposes its own fiber is recorded as `disabled: true`; eight special cases
separate that from every other disposal (`index.ts:149-195`).

Inference: for a shipped profile the root file is empty and every row comes from a patch,
so include routes all runtime changes to its own `patches` config. The root include has no
parent entry to persist them and logs that they "cannot be persisted" (`index.ts:504-516`).
Durable edits are therefore written by the boot packages into patch files, not by include.

**Volatile updates.** A config field marked *volatile* in the plugin's schema can change
without restarting the plugin. When an edit touches only volatile fields of an active
fiber, the loader writes the new values into references the plugin already holds and emits
`loader/volatile-update` to that fiber alone (`entry.ts:156-166`, `entry.ts:178-211`,
`diff.ts:43-45`). An invalid new value is logged and the running values stay.

## 2. How upstream uses it

Counts are call sites outside tests in `packages/` and `apps/`.

| Use | What it is used for | Count |
|---|---|---|
| `loader.entries()` | Inspect the running composition (inventory, diagnostics, reload) | 31 |
| `applyEntryPatches` | Compose layers, also for `--dump-config` (`vendor/README.md:40`) | 19 |
| `parseEntryYaml`, `stringifyEntryYaml`, `entryYamlErrorPosition` | Every reader and writer of patch files shares include's dialect | 16, 8, 2 |
| `loader.await()` | Wait until the tree has settled | 13 |
| `loader.resolve(id)` | Find one entry | 10 |
| `loader.create`, `loader.remove` | Mount and unmount entries at runtime | 5, 4 |
| `loader.import`, `loader.builtins` | Import a module; register `cordis:include` and `cordis:group` | 5, 2 |
| `loader.update` | Change an entry in place | 0 (tests only) |
| `cordis:group` rows with `isolate` | Private realms for plan mode, compaction and delegation | `web-app/presets/cordis.patch.yml:47-90` |

Inference: the product mostly inspects the tree, composes patches and parses YAML. Runtime
mutation is rare and goes through a few boot and host packages.

## 3. Tech debt upstream carries

Search: the assigned `UP-DEBT` units, `FIXME|TODO|XXX|@deprecated` in the three packages,
the local-modification log in `vendor/README.md`, and a read of each source file.

| Debt ID | What it is | Evidence | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-marker.vendor/loader/src/index.ts:153` | `// FIXME merge config`: an entry's `inject` mapping is copied into the fiber's injections without merging with what the plugin declared | `vendor/loader/src/index.ts:153-154` | Resolved. Entry `inject` is a list of names only; per-service config moves to `intercept`, and layers are read in a fixed order, never merged ([DH-ADR-LOADER-2](#dh-adr-loader-2), [DH-D-LOADER-3](#dh-d-loader-3)) |
| `UP-DEBT-found.F2.1` | Config files run arbitrary JavaScript with the full authority of the host process | `vendor/loader/src/config/utils.ts:5-9` | Resolved by a side-effect-free expression language ([DH-ADR-LOADER-3](#dh-adr-loader-3), [DH-D-LOADER-5](#dh-d-loader-5)) |
| `UP-DEBT-found.F2.3` | The loader writes `.cordis/resolve.mjs` and a `.gitignore` into the user's project to resolve modules there | `vendor/loader/src/resolve.ts:12-19`, `:78-91` | Not applicable: it exists only because of Node module resolution. Go resolves names in a compiled-in table ([AR-ADR-16](../../architecture.md#ar-adr-16), [DH-ADR-LOADER-6](#dh-adr-loader-6)) |
| `UP-DEBT-found.F2.4` | A runtime write serialises the parsed data, not the parsed document. Inference: comments and layout of a hand-edited file are lost. | `vendor/include/src/index.ts:498`, `:525`, `:582-586` | Resolved: the YAML codec edits the parsed document in place ([DH-D-LOADER-2](#dh-d-loader-2)) |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-LOADER-R1` | The running tree is composed from an ordered list of layers. A layer inserts entries, overrides or merges fields of an entry found by id, or removes one. Composition is a pure function, so it can be previewed without starting anything. | `docs/architecture.md:27`, `patch.ts:105-182` |
| `DH-LIB-LOADER-R2` | An entry has an id, a component name, config, children, a disabled switch, extra injections, isolation and intercepts. All problems of an entry are reported together. | §1.1 |
| `DH-LIB-LOADER-R3` | Every parse, validation, evaluation and unknown-name error names the file, line and column of the value that caused it. | `utils.ts:34-61`, `scripts/cordis-loader-rc9.spec.ts:70-112` |
| `DH-LIB-LOADER-R4` | Hand-written config supports comments and multi-line text, and a runtime write keeps the comments. Machine-written config has a strict, dependency-free form. | `web-app/presets/cordis.patch.yml:56-69`, `UP-DEBT-found.F2.4` |
| `DH-LIB-LOADER-R5` | A config value and the disabled switch may be computed from the launch environment; a config value may also read data of a service the entry injects. Evaluation has no side effects, the source form survives a write-back, and a failing switch is an error, never "disabled". | §1.2 |
| `DH-LIB-LOADER-R6` | On a change only the affected entries restart. An invalid, unresolvable or failing entry does not stop its siblings. An entry without an id keeps its identity when a neighbour is edited. | `group.ts:86-104`, `scripts/cordis-include-journal.spec.ts:136` |
| `DH-LIB-LOADER-R7` | File edits and runtime edits are merged. The file wins a true conflict and the conflict is reported. Writes are atomic and never replace a file that changed since it was read. | `journal.ts:195-242`, `index.ts:556-580` |
| `DH-LIB-LOADER-R8` | A runtime change is written only to the one layer the harness owns for writing. Shipped layers are never modified. | §1.4, `patch.ts:189-274` |
| `DH-LIB-LOADER-R9` | A missing, unreadable, read-only or invalid file keeps the running tree and the unsaved edits, and the caller can find out that edits are unsaved. | `index.ts:337-350`, `scripts/cordis-include-journal.spec.ts:60` |
| `DH-LIB-LOADER-R10` | Code can create, update, move and remove one entry, validated before anything changes; find the entry that owns a fiber; and wait for the tree to settle. | `tree.ts:163-220`, `index.ts:216-223` |
| `DH-LIB-LOADER-R11` | A component may accept a config change without restarting. | §1.4, volatile updates |
| `DH-LIB-LOADER-R12` | A file can be mounted as a subtree, and a component can own a subtree of its own. An expression belongs to the entry it is written in. | `index.ts:129`, `vendor/include/src/index.ts:238-291` |
| `DH-LIB-LOADER-R13` | Config can only name components the host registered. | [AR-ADR-16](../../architecture.md#ar-adr-16) |

## 5. Go choice

**Decision.** Keep and extend the existing Go loader (`src/go/loader`). Add three seams to
it that need no third-party code: a **codec** (text to entries and back, with positions),
an **evaluator** (expression source to value) and **layer composition**. The harness
module supplies a YAML codec and an expression engine behind those seams.

**Why.** The Go loader already reconciles a tree, merges file and runtime edits and writes
atomically, with tests (`src/go/loader/loader_test.go:106-264`). It lacks the format people
write by hand (R4), computed values (R5), layers (R1, R8) and located diagnostics (R3). The
cordis module must stay on the standard library ([AR-ADR-15](../../architecture.md#ar-adr-15)),
so a YAML parser and an expression engine cannot live there.

### 5.1 Options for the config format

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **YAML for hand-written layers, JSON for machine-written files, selected by file extension (chosen)** | Comments and block text for people; a strict stdlib format for tools; the core stays dependency-free | Two codecs to keep equivalent | Candidate YAML module `go.yaml.in/yaml/v3` v3.0.6, MIT and Apache-2.0, released 2026-10-13, no requirements in `go.mod`. Its `Node` carries `Line`, `Column`, `Tag` and three comment fields (`yaml.go:394-421`). Checked 2026-11-18 with `go list -m -json` and the LICENSE in the module cache. |
| JSON only (today, `src/go/README.md:68`) | Standard library only | No comments and no block text; the plan-mode prompt alone is 13 lines inside config (`web-app/presets/cordis.patch.yml:56-69`) | |
| YAML only, or another YAML module | One format | Machine writers and the stdlib-only core would need the parser | `go.yaml.in/yaml/v4` v4.0.0, LICENSE opens with Apache-2.0, 2026-11-04 (two weeks old at the check); `github.com/goccy/go-yaml` v1.19.2, MIT, 2026-01-08; `gopkg.in/yaml.v3` v3.0.1, 2022-05-27, licence not read, which the fork's README says was labelled unmaintained in April 2025. Same check. |

The final YAML module is recorded by the specification that owns `UP-DEP-yaml`; this document states what the loader requires of it ([DH-D-LOADER-2](#dh-d-loader-2)).

### 5.2 Options for replacing `!!js`

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **A restricted expression language, `github.com/expr-lang/expr` (chosen, `DH-LIB-LOADEREXPR`)** | Typed results (bool, number, list, object); checked at parse time against a fixed set of names; `??`, `?.` and `?:` map directly onto five of the six upstream shapes | A third-party module in the harness; a second small language to document | v1.17.8, MIT, released 2026-02-14, no requirements in `go.mod`. `Compile` with `Env`, `AsBool`, `MaxNodes`, `DisableBuiltin`, `Function` (`expr.go:29-229`); operators in `docs/language-definition.md:138-144`. Checked 2026-11-18 with `go list -m -json`, `go mod download -json` and the LICENSE file. |
| Common Expression Language, `github.com/google/cel-go` | Formal, widely used | Requires antlr4, protobuf and genproto; the downloaded `go.mod` declares `cel.dev/cel-go`, so the import path needs a re-check | v0.32.0, Apache-2.0, 2026-08-19. Same check. |
| Declarative substitutions only (`${env:NAME:-default}`, `when: {os: windows}`) | No dependency; trivially auditable | Cannot return lists or objects (`tls`, `trustedHosts`) or express the conditionals upstream uses (`base/cordis.patch.yml:268`) | |
| Go `text/template` | Standard library | Produces text; typed values must be re-parsed | |
| An embedded JavaScript engine | Upstream files run unchanged | Keeps `UP-DEBT-found.F2.1`; compatibility is not required ([DH-ADR-2](../design.md#dh-adr-2)) | |

**Tradeoff.** Upstream `cordis.yml` and patch files are not loadable as they are: `!!js` becomes `!expr`, a group's children move to `children`, and module specifiers become registered component names.

### 5.3 Upstream loader feature to Go loader status

"Present" means the behaviour exists today in the named file. D-numbers are the deliverables in §6.

| Upstream feature | Evidence | Go loader today | Status |
|---|---|---|---|
| Reconcile an entry list into fibers, parents first | `group.ts:86-104` | `Apply`, `src/go/loader/loader.go:142` | present |
| `id`, `name`, `config`, `group`, `disabled`, and `inject` as a list of names | `entry.ts:10-23`, `utils.ts:138-144` | `src/go/loader/entry.go:17-37`, `loader.go:356`; children in `children` | present |
| `inject` as a mapping with config | `entry.ts:22`, `index.ts:153-154` | none | replaced by `intercept` (D3) |
| `intercept` | `isolate.ts:127` | none in the loader; cordis has `ctx.Intercept` | gap, D3 |
| `isolate` with a label or a private realm | `isolate.ts:75-89` | `loader.go:337-339`, `:445-458`; the value is a string and `"true"` means private | present; D3 accepts boolean `true` |
| Change `isolate` on a live entry by moving the service between realms | `isolate.ts:96-156` | a shape change rebuilds the fiber (`entry.go:53-59`) | replaced |
| Unused shared realms are released | `isolate.ts:158-175` | realm keys are never removed (`loader.go:445-458`) | gap, D3 |
| `disabled`; a disabled group holds its children | `entry.ts:93-102` | `loader.go:295-305` | present |
| `disabled` as an expression | `entry.ts:108-112` | none | gap, D4 |
| Expressions inside `config` | `index.ts:124-131` | none | gap, D4 and D5 |
| Generated ids; an anonymous entry keeps its identity; nested ids `a:b` | `tree.ts:132-153`, `vendor/include/src/index.ts:622-662` | `src/go/loader/merge.go:48-76`, `entry.go:14`, `:99-104` | present |
| Explicit ids that contain `:` (local change V27) | `vendor/README.md:50` | rejected, `loader.go:262-263` | dropped ([DH-ADR-LOADER-2](#dh-adr-loader-2)) |
| Every option checked, every issue listed | `utils.ts:119-162` | id and name only (`loader.go:254-281`); a wrong JSON type fails the whole file (`merge.go:161`) | partial, D3 |
| An invalid entry does not stop its siblings | `group.ts:50-66`, `:97-99` | `loader.go:252-253` | present |
| Source positions on errors | `tree.ts:285-287`, `vendor/include/src/index.ts:588-608` | none | gap, D1 |
| Create, update, move, remove one entry, each with a change report | `tree.ts:59-65`, `tree.ts:163-220` | whole-tree `Apply`; `SetDisabled` (`loader.go:230`); `OnChange(func())` carries no detail (`loader.go:84`) | partial, D8 |
| A component saving its config is written back | `index.ts:133-141` | `onCommit`, `loader.go:460-487` | present |
| Self-disposal is persisted as `disabled: true` | `index.ts:149-195` | none | dropped ([DH-ADR-LOADER-6](#dh-adr-loader-6)) |
| `locate(fiber)` returns the owning entry id | `index.ts:216-223` | private `byFiber` map (`loader.go:50`) | gap, D8 |
| Wait for the tree to settle | `tree.ts:117-123`, `index.ts:204-208` | `Apply` blocks; `WaitAll` (`loader.go:220`) | present |
| Import by module specifier, project-scoped resolution, located not-found error | `tree.ts:226-273`, `resolve.ts:78-105` | `Resolver` and `Register` (`src/go/loader/resolver.go:11-42`); unknown name at `loader.go:350` | replaced ([AR-ADR-16](../../architecture.md#ar-adr-16)); position added by D1 |
| `cordis:` builtins | `tree.ts:227-229` | groups are intrinsic | replaced |
| Volatile update without a remount | `entry.ts:156-211` | `cordis.AcceptUpdate` reached through `fiber.Update` (`loader.go:411-419`) | replaced ([DH-ADR-LOADER-7](#dh-adr-loader-7)) |
| Apply, reload and unload log lines | `index.ts:210-213` | none | gap, D8 |
| `exit()` full-reload hook; `envData` shared across restarts | `index.ts:80-82`, `:225-227` | none | dropped ([DH-ADR-LOADER-6](#dh-adr-loader-6)) |
| YAML or JSON chosen by extension | `vendor/include/src/index.ts:87-91` | JSON only (`merge.go:155-177`) | gap, D1 and D2 |
| Unknown tags and dangling aliases rejected with a position | `vendor/include/src/index.ts:112-130` | not applicable to JSON | gap, D2 |
| `initial` seeds a missing file | `vendor/include/src/index.ts:297-311` | `src/go/loader/configfile.go:108-112` | present |
| Patches (insert, override by id) and the ownership index | `patch.ts:72-182` | none | gap, D6 |
| Runtime changes routed to the owning file or patch | `patch.ts:189-274` | none | replaced by one writable layer, D7 |
| Journal with per-key three-way reconcile | `journal.ts:195-242` | snapshot merge per whole entry (`merge.go:82-152`) | replaced, coarser; D9 |
| Atomic write, bounded retry, never over a changed file | `vendor/include/src/index.ts:556-580` | `configfile.go:307-335` | present |
| A missing, invalid or read-only file keeps the tree and the edits | `vendor/include/src/index.ts:375-402`, `:478-533` | `configfile.go:221-238`, `:315` | present |
| `flush()` rejects while edits are unsaved | `vendor/include/src/index.ts:337-350` | `Save` and `Close` report nothing (`configfile.go:134-143`) | partial, D7 |
| `refresh()` on demand; include has no watcher of its own (V14) | `vendor/include/src/index.ts:353-357`, `vendor/README.md:41` | `Reload` plus a polling watch (`configfile.go:130`, `:342-374`) | present |
| A file mounted as a subtree | `vendor/include/src/index.ts:238-291` | one file drives the one root tree (`configfile.go:115`) | gap, D10 |
| Composition usable as a pure function | `vendor/include/src/index.ts:16` | none | gap, D6 |
| Group as a plugin with its own `inject` and `isolate` | `group.ts:114-127` | intrinsic group fiber (`loader.go:343-346`, `:372-409`) | merged into the loader |

### 5.4 Decisions

<a id="dh-adr-loader-1"></a>
#### DH-ADR-LOADER-1 YAML for people, JSON for machines, behind a codec seam

- **Decision.** `src/go/loader` gains a `Codec` interface and keeps a JSON codec. `src/harness` registers a YAML codec. The file extension selects the codec, as upstream does (`vendor/include/src/index.ts:87-91`). The YAML dialect has no anchors, aliases or custom tags other than `!expr`.
- **Why.** R4, and the stdlib-only rule of the cordis module. Verdict on upstream: keep YAML for hand-written layers and JSON as the second format.
- **Tradeoff.** Two codecs must produce the same entries; a shared test table checks it. **Serves.** DH-O-2. **Verified by.** DH-T-LOADER-1, DH-T-LOADER-2.

<a id="dh-adr-loader-2"></a>
#### DH-ADR-LOADER-2 A Go-native entry model

- **Decision.** Keep `children` for a group's entries, so `config` has one meaning. A group is a kind of entry, not a plugin with a name. `inject` is a list of names; per-service config uses `intercept`. Ids may not contain `:`. `disabled` is a boolean or an expression.
- **Why.** Upstream overloads `config` and `inject`, and its FIXME at `index.ts:153` is the unfinished half of that overload. Upstream allowed `:` in ids only because generated bundle rows were prefixed with a package name (`vendor/README.md:50`), and such rows cannot be addressed by path.
- **Tradeoff.** Upstream files need converting, which [DH-ADR-2](../design.md#dh-adr-2) allows. **Serves.** DH-O-2. **Verified by.** DH-T-LOADER-3.

<a id="dh-adr-loader-3"></a>
#### DH-ADR-LOADER-3 Expressions are data reads in a restricted language

- **Decision.** `!!js` is replaced by `!expr <source>` in YAML and `{"$expr": "<source>"}` in JSON. The loader keeps the source form in the tree and calls an `Evaluator`. An expression may use only the names below. `disabled` sees every name except `use`. A service is visible only if it offers a data view (`ExprView() any`); an expression cannot call a service method and has no file, network or process access. Example: `disabled: !expr launch.profile != "web"` mounts an entry only in the `web` profile (`launch.profile` is the profile's name; the launch facts are area A21's, [../areas/boot.md](../areas/boot.md) §6.2).
- **Why.** R5 and `UP-DEBT-found.F2.1`. An entry that depends on a service being present should say so with `inject`, which cordis tracks over time, not with an expression evaluated at one instant.
- **Tradeoff.** The module-resolution shape (§1.2, last row) has no equivalent; see §7. **Serves.** DH-O-2. **Verified by.** DH-T-LOADER-4, DH-T-LOADER-5, DH-T-LOADER-6.

| Name | Value | Replaces |
|---|---|---|
| `env.NAME` | Environment variable, or nil when unset, so `??` applies | `process.env.NAME` |
| `os` | `windows`, `linux` or `darwin` | `process.platform` |
| `cwd` | Working directory | `process.cwd()` |
| `home(part, ...)` | Path under the harness home | `dshHomePath(...)` |
| `launch.<key>` | Launch data the host passes to the loader. Area A21 fixes the keys: `launch.profile`, `launch.home`, `launch.cwd`, `launch.version`, `launch.tty` and `launch.safe` (safe mode, which has no upstream counterpart) | `ctx.get('profileContext')` |
| `use.<service>.<field>` | Data view of a service the entry injects; `config` only | `ctx.webStartup.host` |

<a id="dh-adr-loader-4"></a>
#### DH-ADR-LOADER-4 Layers are first-class, with exactly one writable layer

- **Decision.** The loader package composes an ordered list of layers into a tree and an ownership index. A layer source keeps the tree in step with the layer files. Runtime changes are written as operations in the single layer marked writable (the user's profile patch). A layer operation is `insert`, an override by `id`, `merge` (deep-merge into `config`, where null deletes a key) or `remove`.
- **Why.** R1 and R8. Upstream routes each change to the patch that owns it, and for a shipped profile that ends at a root that cannot persist (§1.4). One writable layer is the rule the boot packages already follow by hand. `merge` removes the upstream limit that an override must restate the whole config (`packages/boot/app-boot/README.md:210`).
- **Tradeoff.** A value set at runtime lands in the user layer even when a bundle supplied it, so a later bundle update to that key is shadowed. The index makes the shadowing visible in a config dump. **Serves.** DH-O-2. **Verified by.** DH-T-LOADER-7, DH-T-LOADER-8.

<a id="dh-adr-loader-5"></a>
#### DH-ADR-LOADER-5 Keep the snapshot three-way merge and refine it to fields

- **Decision.** Keep the Go design of [AR-ADR-14](../../architecture.md#ar-adr-14): merge the file's version and the runtime's version against the last synced version. Do not port the journal. Compare per field of an entry instead of per whole entry.
- **Why.** R7. A snapshot merge has no pending-record state to keep consistent across failed writes. Whole-entry comparison loses a runtime edit when the file changed another field of the same entry; upstream reconciles per key (`journal.ts:224-231`).
- **Tradeoff.** The merge compares whole trees on every sync. A tree is a few hundred entries. **Serves.** DH-O-2. **Verified by.** DH-T-LOADER-10.

<a id="dh-adr-loader-6"></a>
#### DH-ADR-LOADER-6 Loader behaviours that are not carried over

- **Decision.** Drop (a) inferring `disabled: true` from a fiber disposing itself, (b) the `exit()` hook and `envData`, and (c) module import and resolution.
- **Why.** (a) needs eight special cases to tell self-disposal from every other disposal (`index.ts:157-190`); a Go component calls `SetDisabled` when it means it. (b) Restarting the process is the host's job. (c) First-party components are compiled in and external plugins are processes ([DH-ADR-4](../design.md#dh-adr-4)).
- **Tradeoff.** A component that only disposes itself comes back on the next reload. **Serves.** DH-O-2. **Verified by.** DH-T-LOADER-9.

<a id="dh-adr-loader-7"></a>
#### DH-ADR-LOADER-7 In-place updates use the component's update hook

- **Decision.** A component that can take new config without restarting registers `cordis.AcceptUpdate`. The loader passes every config change through `fiber.Update`, as it does today. Schema volatility metadata is not ported.
- **Why.** R11. Upstream needs a schema-driven diff and shared references because a plugin cannot otherwise say which fields are live (`diff.ts:17-45`). In Go the handler receives the new typed config and decides.
- **Tradeoff.** Accepting is all-or-nothing per update; a handler that cannot apply a field declines, and the fiber restarts. Each component that upstream marks volatile needs a handler: 38 source files declare volatile fields and 10 listen for the event (`grep` for `.volatile()` and `loader/volatile-update` under `packages/` and `apps/`, outside tests). **Serves.** DH-O-2. **Verified by.** DH-T-LOADER-14.

## 6. Design notes

The owner decided that `src/go` is changed ([DH-ADR-7](../design.md#dh-adr-7)). The one
list of every change to it, with the area that needs each and its dependencies, is in
[cordis.md](cordis.md) §6.1; the `loader` rows of that list are the deliverables below
and `DH-D-LOADER-11` in [cordis-plugins.md](cordis-plugins.md). `DH-D-LOADER-2` and
`DH-D-LOADER-5` are harness code in `src/harness` and are not changes to `src/go`.

Shape of the additions to `src/go/loader` (standard library only):

```go
type Codec interface {
    Parse(file string, text []byte) (*Document, error)   // *ParseError: issues with positions
    Render(previous *Document, ops []Op) ([]byte, error) // edits previous in place when given
}
type Evaluator interface {
    Check(source string, kind ScopeKind) error    // parse and type-check only
    Eval(source string, scope Scope) (any, error) // no side effects
}
type Layer struct{ Label, Path string; Writable bool; Ops []Op }
func Compose(layers []Layer) (tree []EntryOptions, index *Index, issues []Issue)
```

**Ownership.** The loader's fiber owns every entry fiber, so unloading the loader unloads
the tree (`src/go/loader/loader.go:33-35`). The layer source's fiber owns its poll timers
and its change subscription. A temporary file is removed by the write that made it. A
shared realm key belongs to the loader and is released when its last entry goes. The
loader may *use* any registered component, but it only *releases* the fibers it created.

**Failure, retry and cleanup.** A layer that fails to parse or validate is rejected as a
whole: the previous composition keeps running and the error is reported once per distinct
content. An entry that fails to evaluate, resolve or load is reported on its own and tried
again at the next reconciliation that changes it; siblings are untouched. A failed write
leaves the edits in memory and `Flush` returns the error until a later write succeeds, so
whether the user layer holds an edit is never left unknown. Nothing is rolled back across
entries: a reload is not a transaction, as upstream (`packages/boot/app-boot/README.md:117`).

<a id="dh-d-loader-1"></a>
#### DH-D-LOADER-1 Codec seam and source positions
- **Result.** Every loader error names `file:line:column` (R3), and the file format can change without touching the reconcile logic.
- **How.** Put `parse` and `serialize` (`src/go/loader/merge.go:155-177`) behind `Codec`. The JSON codec walks `encoding/json` tokens and records the byte offset of each value by path, converted to line and column on demand. `Document` keeps that map.
- **Components.** `src/go/loader` (new `codec.go`; `merge.go`, `configfile.go`). **Depends on.** Nothing. **Priority.** P0. **Outcome.** DH-O-2. **Done when.** DH-T-LOADER-1 passes and the existing loader tests pass unchanged.

<a id="dh-d-loader-2"></a>
#### DH-D-LOADER-2 YAML codec in the harness module
- **Result.** Layers can be written in YAML with comments and block text, and a runtime write keeps the comments (R4).
- **How.** Parse to the module's node tree; reject anchors, aliases, merge keys and unknown tags with their position; map `!expr` scalars to expression nodes; build `EntryOptions` from the nodes. `Render` applies operations to the kept node tree and re-encodes it, so comments attached to untouched nodes survive. Indentation is normalised.
- **Components.** `src/harness` config package and the YAML module chosen by the `UP-DEP-yaml` spec. **Depends on.** DH-D-LOADER-1. **Priority.** P0. **Outcome.** DH-O-2. **Done when.** DH-T-LOADER-2 passes, including the table that feeds the same entries through both codecs, and scenario [DH-E-LOADER-1](cordis-plugins.md#dh-e-loader-1) passes.

<a id="dh-d-loader-3"></a>
#### DH-D-LOADER-3 Entry schema and full validation
- **Result.** `intercept` works, `isolate: {name: true}` is accepted, and a bad entry reports all of its problems instead of failing the file (R2).
- **How.** Add `Intercept` to `EntryOptions` and apply it with `ctx.Intercept` beside the isolate step in `ensure`. Make `Disabled` a type holding a boolean or an expression. Decode entries leniently, then validate each into an `EntryError` with issues and positions. Count the users of each shared realm key and delete it at zero.
- **Components.** `src/go/loader` (`entry.go`, `loader.go`). **Depends on.** DH-D-LOADER-1. **Priority.** P0. **Outcome.** DH-O-2. **Done when.** DH-T-LOADER-3 passes.

<a id="dh-d-loader-4"></a>
#### DH-D-LOADER-4 Expression seam in the loader
- **Result.** `disabled` and config values can be computed, are evaluated again at the right moments, and keep their source form on write-back (R5).
- **How.** `disabled` is evaluated in `ensure`, outside every lock. For `config`, the loader registers one config hook with the cordis core: the `OnConfig` hook of [DH-D-CORDIS-5](cordis.md#dh-d-cordis-5), the Go form of upstream's `internal/config` listener (`index.ts:124-131`). For a fiber that is the root of an entry, the hook replaces each expression node in the raw config with its value before the schema runs. The core resolves config at the start of every load, after the fiber's injections are active and outside the tree lock (`src/go/cordis/fiber.go:776-784`), so reading `use.<service>` there is safe, and a replaced provider reloads the fiber and evaluates again. The tree keeps the source form. An evaluation error fails that entry only.
- **Components.** `src/go/loader` (`loader.go`, new `expr.go`). **Depends on.** DH-D-LOADER-3 and [DH-D-CORDIS-5](cordis.md#dh-d-cordis-5), which is P0 for this reason. **Priority.** P0. **Outcome.** DH-O-2. **Done when.** DH-T-LOADER-4 and DH-T-LOADER-5 pass with a stub evaluator.

<a id="dh-d-loader-5"></a>
#### DH-D-LOADER-5 Expression engine in the harness module
- **Result.** The names of [DH-ADR-LOADER-3](#dh-adr-loader-3) exist, and nothing else does.
- **How.** Implement `Evaluator` with `DH-LIB-LOADEREXPR`: compile against a typed environment so an unknown name fails at parse time with a position, cap the node count, and disable builtins that are not pure. Service views are collected from injected services that implement `ExprView`.
- **Components.** `src/harness` config package. **Depends on.** DH-D-LOADER-4. **Priority.** P0. **Outcome.** DH-O-2. **Done when.** DH-T-LOADER-6 passes.

<a id="dh-d-loader-6"></a>
#### DH-D-LOADER-6 Layer composition and ownership index
- **Result.** A profile's layers compose into one tree, and a preview uses the same function as the running system (R1).
- **How.** Port the algorithm of `applyPatches` (`patch.ts:105-182`) as a pure function over `[]Layer`, extended with `merge` and `remove`. The index records, per entry and per field, the label of the layer that supplied it. An operation that matches nothing becomes an issue with the layer label and position.
- **Components.** `src/go/loader` (new `layers.go`). **Depends on.** DH-D-LOADER-3. **Priority.** P0. **Outcome.** DH-O-2. **Done when.** DH-T-LOADER-7 passes.

<a id="dh-d-loader-7"></a>
#### DH-D-LOADER-7 Layered source with write-back to one layer
- **Result.** Editing any layer file changes the running tree, and a runtime change is saved to the user layer and nowhere else (R7, R8, R9).
- **How.** Generalise `ConfigFile` (`src/go/loader/configfile.go`) to a set of layer files on the same serial loop. A read composes again and applies. A write diffs the runtime tree against the composition without the writable layer, turns the difference into operations, merges them three ways with the writable file and writes atomically. `Flush() error` reports unsaved edits. `ConfigFile` remains as the one-layer case.
- **Components.** `src/go/loader` (`configfile.go`, new `layerset.go`). **Depends on.** DH-D-LOADER-1, DH-D-LOADER-6. **Priority.** P0. **Outcome.** DH-O-2. **Done when.** DH-T-LOADER-8 and DH-T-LOADER-14 pass, and scenario [DH-E-LOADER-1](cordis-plugins.md#dh-e-loader-1) passes.

<a id="dh-d-loader-8"></a>
#### DH-D-LOADER-8 Mutation, lookup and lifecycle log API
- **Result.** The config editor, plugin manager and diagnostics can change one entry and find the entry behind a fiber (R10).
- **How.** `Create`, `Update`, `Move` and `Remove` validate first, then edit a snapshot and call `Apply`. `OnChange` listeners receive the id and the options before and after. `EntryOf(fiber)` walks parents through `byFiber`. Apply, reload and unload are logged under the `loader` logger when enabled.
- **Components.** `src/go/loader` (`loader.go`). **Depends on.** DH-D-LOADER-3. **Priority.** P1. **Outcome.** DH-O-2. **Done when.** DH-T-LOADER-9 passes.

<a id="dh-d-loader-9"></a>
#### DH-D-LOADER-9 Per-field three-way merge
- **Result.** A file edit and a runtime edit to different fields of one entry both survive.
- **How.** Replace the whole-entry `shape` comparison (`src/go/loader/merge.go:16-22`, `:96-107`) with a comparison per field. A conflict is reported per field and the file's value wins.
- **Components.** `src/go/loader` (`merge.go`). **Depends on.** DH-D-LOADER-3. **Priority.** P2. **Outcome.** DH-O-2. **Done when.** DH-T-LOADER-10 passes.

<a id="dh-d-loader-10"></a>
#### DH-D-LOADER-10 A component can run a loader of its own under its context
- **Result.** A component such as an agent preset can run a tree of its own: a second loader instance mounted under that component's context, for example under an agent's scope, whose entries are visible only there and unload with it (R12). Area A27 builds agent presets on it ([../areas/bundle-preset.md](../areas/bundle-preset.md), `DH-D-BUNDLE-4`).
- **How.** `loader.New` can be called more than once in a tree. A second instance is provided as an isolated `loader` service under the caller's context, so the rest of the tree never sees it, and it owns the entry fibers it creates as children of the caller's fiber. It is given its layers directly as values, with **no layer marked writable**: it has no layer source, watches no file and never writes back. Its mutation methods (`Create`, `Update`, `Move`, `Remove`) return a read-only error; a change is made by the component handing it a new layer list, which is reconciled like any `Apply`. It resolves component names in the tree's one registry, and expressions are evaluated by the instance that owns the entry. A saved setting that belongs to a preset is written through the application's one edit path into the profile layer of the root loader, never by the nested instance.
- **Ownership and failure.** The component that created the instance owns it; unloading that component unloads the instance and every entry under it, leaves first. An entry that fails to load is reported on its own, as in the root loader; nothing is rolled back.
- **Components.** `src/go/loader` (`loader.go`). **Depends on.** DH-D-LOADER-6. **Priority.** P0. It was P2 while it also covered a group's `source` file; the P0 agent presets of area A27 depend on this half, so the halves were split and the other is [DH-D-LOADER-14](#dh-d-loader-14). **Outcome.** DH-O-2. **Done when.** DH-T-LOADER-11 passes.

<a id="dh-d-loader-14"></a>
#### DH-D-LOADER-14 A group entry can mount another file as its children
- **Result.** An entry can name a file whose entries become its children, so a large layer can be split across files.
- **How.** Let a group entry name a `source` file; its children come from a layer source scoped to the group's context. This was the first half of DH-D-LOADER-10 as first written.
- **Components.** `src/go/loader` (`loader.go`, `layerset.go`). **Depends on.** DH-D-LOADER-7. **Priority.** P2: no area needs it for a first working harness. **Outcome.** DH-O-2. **Done when.** DH-T-LOADER-16 passes.

<a id="dh-d-loader-13"></a>
#### DH-D-LOADER-13 A write guard on the layer source
- **Result.** The application can bracket every write of the writable layer with a function of its own, so that two `dsh` processes that share one harness home ([DH-ADR-12](../design.md#dh-adr-12)) never both replace the same profile layer. Without it the layer source's own check, "never replace a file that changed since it was read", leaves a window between the comparison and the rename in which two processes can both pass ([../areas/boot.md](../areas/boot.md), `DH-ADR-BOOT-8`).
- **How.** The layer source takes an option `WithWriteGuard(func(path string, write func() error) error)`. When the option is set, the source hands the guard the whole read-merge-replace of [DH-D-LOADER-7](#dh-d-loader-7) as one function: re-read the writable file, merge the pending operations three ways, write a temporary file and rename it. The guard decides when to call it; the harness's guard takes an exclusive cross-process file lock beside the layer with a bounded wait (area A08's lock, [../areas/filesystem.md](../areas/filesystem.md) `DH-D-FS-1`) and releases it when `write` returns. The loader package knows nothing about locks, so the cordis module stays standard-library only. With no guard the source behaves as in DH-D-LOADER-7.
- **Ownership and access.** The guard is supplied by the component that creates the layer source and is owned by that component's fiber; the source only calls it. Reads are not guarded: another host learns of a change from its own poll of the file.
- **Failure.** A guard that returns an error without calling `write` (for example the lock wait ran out) leaves the file untouched; the edits stay in memory and `Flush` keeps returning the error, exactly as for a failed write. A guard that calls `write` and then fails to release its lock returns `write`'s result; the lock is the guard's to clean up. A guard that panics is treated as a failed write.
- **Components.** `src/go/loader` (`layerset.go`). **Depends on.** DH-D-LOADER-7. **Priority.** P1, with the edit path of area A21 (`DH-D-BOOT-7`). **Outcome.** DH-O-2. **Done when.** DH-T-LOADER-15 passes, and area A21's scenario [DH-E-BOOT-2](../areas/boot.md#dh-e-boot-2), which proves the two-process behaviour, passes.

## 7. Risks and open questions

1. **YAML module.** Settled by [yaml.md](yaml.md): `go.yaml.in/yaml/v4` (`DH-LIB-YAML`), which gives node positions, comment round-trip and tag access.
2. **Launch data and service views.** Settled by area A21: the `launch.*` names and the services that offer a view are listed in [../areas/boot.md](../areas/boot.md) §6.2 (`DH-D-BOOT-2`).
3. **Module-resolution expressions.** The preset `skills` path (`web-app/presets/cordis.patch.yml:130`) has no Go form. Preset assets are embedded in the binary and addressed by name, as area A27 specifies ([../areas/bundle-preset.md](../areas/bundle-preset.md)).
4. **Dependency on the cordis core.** Settled: DH-D-LOADER-4 depends on [DH-D-CORDIS-5](cordis.md#dh-d-cordis-5), its ledger row lists the dependency, and DH-D-CORDIS-5 is P0. One fact to keep in mind: `src/go/README.md:229-231` says a schema runs under the tree lock, while the code resolves config after unlocking (`src/go/cordis/fiber.go:779-784`); this design relies on the code, and the README is to be corrected ([cordis.md](cordis.md) §7).
5. **Required entries.** Which entries must load for start-up to succeed (`packages/boot/app-boot/README.md:45`) is boot policy, built on `EntryOf` and fiber state: area A21, `DH-D-BOOT-5`.
6. **A runtime save over an expression.** Settled by area A21 ([../areas/boot.md](../areas/boot.md) §6.2, "Computed fields"): an edit of a field whose lower-layer value is an `!expr` is refused with `config/computed` unless the caller asks to overwrite, and a component that saves its own state skips a field whose value still equals the evaluated expression. The loader's write-back is called only from that edit path.

## 8. Verification

All are local Go tests against temporary directories. Nothing here has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-LOADER-1` | local | DH-D-LOADER-1, R3 | A JSON file with a wrong-typed field and an unknown component name yields issues whose line and column match the offending values |
| `DH-T-LOADER-2` | local | DH-D-LOADER-2, R4 | A YAML layer with comments, block text and `!expr` parses to the same entries as its JSON twin; an alias and an unknown tag are rejected with positions; a rendered edit keeps every comment |
| `DH-T-LOADER-3` | local | DH-D-LOADER-3, R2 | An entry with three faults reports three issues and is disposed while its sibling stays active; an intercept reaches the service; a shared realm key is gone after its last entry |
| `DH-T-LOADER-4` | local | DH-D-LOADER-4, R5 | A `disabled` expression is evaluated at each `Apply`; one that fails leaves the entry unloaded and reported, not disabled; the saved file still holds the expression |
| `DH-T-LOADER-5` | local | DH-D-LOADER-4, R5 | A config expression reading `use.x` is evaluated only after `x` is active, and again after `x` is replaced |
| `DH-T-LOADER-6` | local | DH-D-LOADER-5, R5 | Each name in the scope table returns its value; an unknown name fails at parse time; `use` is rejected in `disabled`; no builtin reads a file or the clock |
| `DH-T-LOADER-7` | local | DH-D-LOADER-6, R1 | Insert, override, merge and remove across three layers give the expected tree and index; the last layer wins; an unmatched operation is an issue; inputs are not mutated |
| `DH-T-LOADER-8` | local | DH-D-LOADER-7, R7 to R9 | A runtime change appears only in the writable layer and the other files are byte-identical; a concurrent file edit wins a conflict; with the file read-only `Flush` fails, and succeeds once it is writable |
| `DH-T-LOADER-9` | local | DH-D-LOADER-8, R10 | An invalid `Create` changes nothing; create, move and remove report the change; `EntryOf` finds the entry of a nested fiber; a self-disposed entry is not written as disabled |
| `DH-T-LOADER-10` | local | DH-D-LOADER-9, R7 | The file edits `config` while the runtime edits `disabled` of one entry: both are kept and no conflict is logged |
| `DH-T-LOADER-11` | local | DH-D-LOADER-10, R12 | A second loader instance mounted under a component's context with no writable layer: its entries load, are invisible to a sibling context and unload with the component; it evaluates its own expressions; `Create`, `Update`, `Move` and `Remove` return the read-only error and no file is written; a new layer list restarts only the changed entry |
| `DH-T-LOADER-16` | local | DH-D-LOADER-14 | A group with a `source` file mounts its children, and an edit to that file reloads only the changed child |
| `DH-T-LOADER-14` | local | DH-D-LOADER-7, R6, R11 | Editing a layer restarts only the changed entry; a component with `AcceptUpdate` is not restarted; an anonymous neighbour keeps its fiber |

| `DH-T-LOADER-15` | local | DH-D-LOADER-13 | With a recording guard: every write of the writable layer happens inside one guard call and no file changes outside one; a guard that returns an error without calling `write` leaves the file byte-identical and `Flush` reports the error until a later write succeeds; a file changed by another writer while the guard waits is re-read inside the guard, so both edits are in the result; a guard that panics is a failed write; with no guard the behaviour of `DH-T-LOADER-8` is unchanged |

`DH-T-LOADER-12`, `DH-T-LOADER-13` and scenario `DH-E-LOADER-1` are in [cordis-plugins.md](cordis-plugins.md).

## 9. Units dispositioned by this spec

`UP-PKG-vendor.loader` (ported), `UP-PKG-vendor.include` (replaced), `UP-PKG-vendor.group`
(merged), `UP-DEBT-marker.vendor/loader/src/index.ts:153` (resolved), `UP-DEBT-found.F2.1`
(resolved), `UP-DEBT-found.F2.3` (not applicable), `UP-DEBT-found.F2.4` (resolved).
