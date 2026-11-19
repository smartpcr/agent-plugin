# yaml: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". No harness code exists.
>
> **Unverified research notes.** Where this document says "probe", it reports an
> observation made during library research on 2026-11-18 by running a throwaway program
> against the candidate modules outside this repository. That was against the project's
> rule that nothing is built or run in the design task, and the program was not kept, so
> the observation cannot be repeated from this repository. Every such statement is
> therefore **unverified and to be measured**: the test named beside it
> (`DH-T-YAML-2` to `-5` in section 8) re-establishes it before the choice is final, and a
> choice whose observation the test contradicts is reopened.

| | |
|---|---|
| **Library ID** | `DH-LIB-YAML` |
| **Upstream unit** | `UP-DEP-yaml` |
| **Upstream version** | 2.9.0 (`pnpm-lock.yaml`) |
| **Kind** | runtime; also bundled into the client by one package (`packages/client/ui-sidebar-documentpreview`) |
| **Used by** | apps, boot, connector, credentials, experimental, sandbox, settings, skill, vendor |
| **Go choice** | named Go module `go.yaml.in/yaml/v4`, used only inside one harness package |
| **Related areas** | boot (profiles, layer files, config editor), skills, credentials; the loader specification [cordis-loader.md](cordis-loader.md); the sibling [js-yaml.md](js-yaml.md) |

## 1. What it is and why upstream needs it

YAML is the text format people write harness configuration in. It allows comments and
multi-line text, which JSON does not, and the shipped configuration uses both (a 13-line
prompt sits inside one config value, `packages/bundle/web-app/presets/cordis.patch.yml:56-69`).
`yaml` is the JavaScript library that reads and writes it.

Upstream needs three different things from it, and they have different costs:

1. **Read a file into data, with positions.** Every parse error and every invalid value is
   reported with file, line and column. The library keeps the position of every node.
2. **Edit a file a person wrote without destroying it.** The library offers a *document*:
   the parsed file as a tree of nodes that still carries comments and layout. A program
   changes one node and prints the document; everything else comes back as written. This
   is what "comment-preserving write-back" means in this specification.
3. **A custom tag.** A *tag* is a YAML marker in front of a value that changes its type.
   Upstream's `!!js` tag marks a value as a JavaScript expression to be evaluated later
   (the Go design replaces it with `!expr`, [DH-ADR-LOADER-3](cordis-loader.md#dh-adr-loader-3)).

Upstream also uses a second YAML library, `js-yaml`. Section 3 explains why there are two
and treats the duplication as tech debt.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used | In Go |
|---|---|---|---|
| `vendor/include/src/index.ts:8-12`, `:23-37`, `:53-63`, `:72-74` | **The one reader of `cordis.yml` and every patch (layer) file.** YAML 1.2 under the *core schema* (the standard rule set that decides which plain words are numbers, booleans or null) plus the `!!js` tag. `parseEntryYaml` (16 call sites) and `stringifyEntryYaml` (8) are the dialect every other package shares ([cordis-loader.md](cordis-loader.md) section 2). | `parseDocument`, `LineCounter`, `stringify`, custom `ScalarTag`, `YAMLError.linePos` | the loader's YAML codec, [DH-D-LOADER-2](cordis-loader.md#dh-d-loader-2) |
| `vendor/include/src/index.ts:101-105` | JSON files are parsed as YAML too, only to get node positions for error messages | `parseDocument`, `Range` | the JSON codec records positions itself ([DH-D-LOADER-1](cordis-loader.md#dh-d-loader-1)) |
| `vendor/include/src/index.ts:112-130` | Find an alias that has no anchor, which the library accepts and only fails on later without a position | `visit`, `isAlias` | anchors and aliases are rejected outright (R3) |
| `packages/boot/config-editor/src/index.ts:109-131` | **Comment-preserving write-back** of the user's profile patch when a setting changes: set or delete one entry's `config`, re-tag expressions, print the document | `parseDocument` with a custom tag, `getIn`, `setIn`, `createNode`, `visit`, `Scalar` | one writer, the loader ([DH-D-LOADER-7](cordis-loader.md#dh-d-loader-7)) |
| `packages/boot/plugin-manager/src/patch.ts:34-70` | Comment-preserving toggle of an entry's `disabled`. When the override row is removed its comment is moved to the next row, because it may be the file header (`:60-66`) | `parseDocument`, `deleteIn`, `setIn`, `commentBefore` | the same loader writer |
| `packages/boot/plugin-manager/src/build-approval.ts:35-94` | Edit `allowBuilds` in pnpm's `pnpm-workspace.yaml` | `parseDocument`, `setIn` | not applicable: pnpm only ([DH-ADR-4](../design.md#dh-adr-4)) |
| `packages/credentials/credentials-local/src/index.ts:198`, `:246`, `:432-493` | The credentials file `$DSH_HOME/.credentials.yaml`: strict read (duplicate keys are errors), and writes that patch one key so that "comments and the formatting of every untouched entry survive" (`:22-24`) | `parseDocument` with `uniqueKeys` and `intAsBigInt`, `Document`, `setIn`, `deleteIn`, `toString` | the credentials area chooses the store; a file store uses the document edit of section 6 |
| `packages/skill/skill-filesystem/src/index.ts:941`, `packages/skill/skill-office/src/index.ts:53`, `packages/connector/upstream-skills/src/index.ts:199`, `packages/sandbox/sandbox-windows-acl/src/acl-skill.ts:29` | Read *frontmatter*: the YAML block between `---` lines at the top of a skill's Markdown file | `parse` | `DecodeFrontmatter` (section 6) |
| `packages/experimental/creation-recommendations/src/generated-bundle.ts:24-28` | Check that a patch file written by the model is plain YAML: no tags, no aliases | `parseDocument`, `toJS({ maxAliasCount: 0 })` | the strict dialect is the only dialect (R3) |
| `packages/settings/settings/src/index.ts:247` | One-time import of a removed `settings.yaml` | `parse` | not carried over: no upstream profile is loaded ([DH-ADR-2](../design.md#dh-adr-2)) |
| `apps/plugin-market-generator/src/packages.ts:129` | Read a source repository's `pnpm-workspace.yaml` | `parse` | not applicable: pnpm only |
| `packages/client/ui-sidebar-documentpreview/src/client/markdown/frontmatter-fields.tsx:31-48` | Show the frontmatter fields of a previewed Markdown document, in file order | `parseDocument`, `range` | rendered by the host ([DH-ADR-3](../design.md#dh-adr-3)) with `DecodeFrontmatter` |

So `yaml` parses `cordis.yml`, handles the `!!js` tag, and is the library that preserves
comments on write-back. `js-yaml` does none of these today.

Inference: one detail upstream gets wrong is worth knowing. The loader's own write path
does not use the document; it serialises the parsed data, so a write by the loader itself
loses comments (`UP-DEBT-found.F2.4` in [cordis-loader.md](cordis-loader.md)). The
comment-preserving writes are done by two boot packages that parse the same file a second
time with their own copy of the tag definition.

## 3. Tech debt upstream carries

Search performed: every import of `yaml` and `js-yaml` under `packages/`, `apps/`,
`vendor/` and `scripts/`; `patches/` and `pnpm-workspace.yaml` (no patch, pin or override
for either); the decision notes that mention either library; the "Known Limitations"
sections of the consuming packages.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L1.1` | **Two YAML libraries and one dialect defined three times.** The loader used `js-yaml` until release 0.2.2-rc.3 and then moved to `yaml` (`docs/upgrade-guide/v0.2.2-rc.3/cordis-loader-entry-validation/guide.md:14`, `:18`). `js-yaml` stayed in build scripts, release tooling and test support, because it cannot edit a document and so cannot replace `yaml`, and nobody migrated its remaining call sites. An upstream audit recorded the duplication and deferred it (`.agents/notes/rejected/simplification/2026-07-26-dependency-swaps-rejected-by-nih-audit.md:72`). The `!!js` tag is defined in three files that must agree exactly, two of them with a different resolver from the loader's. `apps/cli` still declares `js-yaml` as a runtime dependency although no source file there imports it. | `vendor/include/src/index.ts:23-32`; `packages/boot/config-editor/src/index.ts:110`; `packages/boot/plugin-manager/src/patch.ts:35`; `apps/cli/package.json:101` | Resolved by [DH-ADR-YAML-1](#dh-adr-yaml-1): one module, imported by one package, with the dialect and the `!expr` tag defined once. `DH-T-JSYAML-1` ([js-yaml.md](js-yaml.md)) fails the build if a second YAML module is imported. |
| `UP-DEBT-found.F2.4` (owned by [cordis-loader.md](cordis-loader.md)) | The loader's own write loses comments | `vendor/include/src/index.ts:498`, `:525` | Resolved there; this specification supplies the mechanism (R6, R7) |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-YAML-R1` | There is one YAML module and one definition of the harness YAML dialect. Every component that reads or writes YAML goes through one harness package. | `UP-DEBT-found.L1.1` |
| `DH-LIB-YAML-R2` | Parsing yields a node tree in which every node has a line and column. A syntax error is returned as a value carrying line and column, never as text that must be searched for a position. | `vendor/include/src/index.ts:53-63`, `:81-85` |
| `DH-LIB-YAML-R3` | The dialect is strict. Anchors, aliases, merge keys, more than one document in a file, duplicate keys in a mapping, and any tag other than the ones the caller registers are errors with a position. | `vendor/include/src/index.ts:112-130`; `packages/credentials/credentials-local/src/index.ts:198`; `packages/experimental/creation-recommendations/src/generated-bundle.ts:24-28`; [DH-ADR-LOADER-1](cordis-loader.md#dh-adr-loader-1) |
| `DH-LIB-YAML-R4` | Unquoted words are typed by the YAML 1.2 core rules and nothing else: `true` and `false` are booleans; `null`, `~` and an empty value are null; integers are decimal, `0o` octal or `0x` hexadecimal. A word that older YAML types differently (`yes`, `on`, `017`, `0b101`, `1:30`) is never silently converted: it is a string, or an error where a number was clearly meant (a leading zero). The same text gives the same value on every platform and through the JSON codec. | `vendor/include/src/index.ts:34-37`, `:46`; `docs/upgrade-guide/v0.2.2-rc.3/cordis-loader-entry-validation/guide.md:14` |
| `DH-LIB-YAML-R5` | A registered tag on a scalar (for the loader, `!expr`) is read with its source text and written back in the same form. | `vendor/include/src/index.ts:23-32`; [DH-ADR-LOADER-3](cordis-loader.md#dh-adr-loader-3) |
| `DH-LIB-YAML-R6` | A runtime write edits the parsed document. The operations are: insert an entry, set a field, delete a field, delete an entry. No comment is lost: a comment stays with the entry or key it was written above or beside, and the comment of a deleted entry moves to the next entry (or the end of the file). Untouched values keep their quoting, their block or inline style and their spelling. | `packages/boot/config-editor/src/index.ts:109-131`; `packages/boot/plugin-manager/src/patch.ts:56-66`; `packages/credentials/credentials-local/src/index.ts:22-24` |
| `DH-LIB-YAML-R7` | The written layout is defined and stable: two-space indentation, no re-wrapping of long lines, and a blank line between top-level entries wherever the file had one. Writing a file that the harness wrote changes nothing. | Inference from R6: the chosen module does not keep blank lines (section 5) |
| `DH-LIB-YAML-R8` | Before rendered text replaces a file it is parsed again. It must give exactly the intended data and contain every comment of the input. If not, the write is refused and reported, and the file is left as it was. | Inference: a writer that edits hand-written files must not be trusted blindly; supports `DH-LIB-LOADER-R9` |
| `DH-LIB-YAML-R9` | YAML from a place the user does not control (a skill file in a project, a plugin's manifest) is read under the same strict dialect with bounded size and nesting depth. A failure inside the module, including a panic, becomes an ordinary error. | the four frontmatter use sites; [DH-ADR-TOOLCHAIN-3](../upstream/toolchain.md#dh-adr-toolchain-3) |
| `DH-LIB-YAML-R10` | Writing data that has no source document (a config dump) is deterministic: fixed key order, fixed indentation, no anchors or aliases. | `vendor/include/src/index.ts:66-74` |

## 5. Go choice

**Decision.** `go.yaml.in/yaml/v4` at v4.0.0, imported by one package only.

**Why.** It meets R2, R5 and the comment half of R6 directly, is maintained by the YAML
organisation with a commit as recent as 2026-11-06, has no dependencies, and enforces
nesting and alias limits by default (R9). Its node tree is a plain Go struct, so the four
edit operations of R6 are a few lines each and cannot leave the tree inconsistent.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **`go.yaml.in/yaml/v4` (chosen)** | Node tree with tag, style, line, column and three comment fields per node. Errors are `*yaml.LoadError` with a line and column mark. Depth and alias limits on by default. Active. | Does not keep blank lines or comment alignment (probe below). The major version is two weeks old. Does not detect duplicate keys when loading into a node tree (probe), so the harness checks them. Types `017` as octal and `0b101` as binary (probe), so the harness types scalars itself (R4). | v4.0.0, released 2026-11-04; default branch last commit 2026-11-06; licence Apache-2.0, with eight files ported from libyaml under MIT (`LICENSE`, `NOTICE`); `go.mod` has no requirements; pure Go. Node fields at `internal/libyaml/node.go:159-203`, error type at `internal/libyaml/errors.go:39-55`. Checked 2026-11-18 with `go list -m -json`, `go mod download -json` and the files in the module cache; the behaviour marked "probe" is unverified. |
| `github.com/goccy/go-yaml` | The only candidate that kept blank lines through every edit in the probe. Detects duplicate keys and dangling aliases at parse time, with a source excerpt. | A sequence keeps three parallel slices (`Values`, `ValueHeadComments`, `Entries`, `ast/ast.go:1547-1556`) that the caller must edit together to delete or insert an entry; the path API only replaces and merges (`path.go:289-357`). After a merge in the probe the slices had different lengths (4, 4, 3). Maintenance has slowed. No documented input limits were found. | v1.19.2, released 2026-01-08; default branch last commit 2026-04-07; MIT; no requirements; pure Go. Same check. |
| `go.yaml.in/yaml/v3` | The same node tree as v4, in use for years. A fallback that needs almost no code change. | API frozen, "security fixes only" (v4 `README.md:19-26`). Errors are text such as `yaml: line 3: ...` with no column and no structured type (`yaml.go:310-316`), which fails R2. | v3.0.6, released 2026-10-13; MIT and Apache-2.0; no requirements. Same check. |
| `gopkg.in/yaml.v3` | None over the fork | The original, which its author labelled unmaintained in April 2025 (`README.md:24-28` of the v3 fork) | v3.0.1, released 2022-05-27; MIT and Apache-2.0. Same check. |
| Standard library | No dependency | There is no YAML package in the standard library | n/a |

**What the probe was reported to show (unverified, to be measured by `DH-T-YAML-2` to
`-5`).** The same file (a file header, a comment above an entry, a
comment at the end of a line, a blank line inside a mapping, a block of text, an inline
list, a tagged scalar, a quoted string) was parsed, changed and written by each candidate.
The changes were: replace a scalar, add a key, delete a key, append an entry, delete an entry.

| Property | yaml v3 | yaml v4 | goccy |
|---|---|---|---|
| Every comment survives every edit | yes | yes | yes |
| Comment stays with its own entry or key | yes | yes | yes |
| Quoting, block text, inline list kept | yes | yes | yes |
| Blank lines inside the document kept | no | no | yes |
| Spaces before an end-of-line comment kept | no | no | no |
| Indentation | re-emitted | re-emitted; nested lists are not indented unless `WithCompactSeqIndent(false)` | kept |
| Tag and source text of `!expr env.PORT ?? 3080`, with line 7 column 11 | yes | yes | yes |
| Duplicate key rejected in a node tree | no | no, also with `WithUniqueKeys` | yes |
| `017` read as | 15 | 15 | 15 |
| `yes` read as (untyped) | string | string | string |

**Tradeoff.** The harness gives up exact layout. After the first runtime write, blank
lines inside an entry and the alignment of end-of-line comments are gone; comments, order,
quoting and the blank lines between top-level entries remain (R7). Upstream keeps layout
exactly, because its library does. The module's major version is new; the version is
pinned, and both fallbacks are cheap because only one package imports it.

<a id="dh-adr-yaml-1"></a>
### DH-ADR-YAML-1 One YAML module, one dialect, defined once

- **Decision.** The harness has one YAML module. One package, `internal/boot/yamlx`
  (name proposed; the boot area owns it), imports it and exposes parse, document edit,
  frontmatter decode and dump. No other package imports a YAML module. Machine-written
  files are JSON ([DH-ADR-LOADER-1](cordis-loader.md#dh-adr-loader-1)).
- **Why.** `UP-DEBT-found.L1.1`: upstream has two parsers and three copies of the tag,
  and two packages re-parse a file the loader already parsed. Verdict on upstream: keep
  YAML for people, drop the second library and the per-package tag copies.
- **Tradeoff.** Every YAML need, however small, depends on the boot package's helper.
- **Serves.** DH-O-2. **Verified by.** DH-T-YAML-1 and `DH-T-JSYAML-1`.

<a id="dh-adr-yaml-2"></a>
### DH-ADR-YAML-2 Comments are preserved; layout is canonical

- **Decision.** Use `go.yaml.in/yaml/v4`. A runtime write keeps every comment and writes
  one defined layout (R6, R7), and is checked by parsing the result before it replaces the
  file (R8).
- **Why.** Losing a comment loses information the user wrote; losing a blank line does
  not. The candidate that keeps blank lines needs fragile tree surgery for two of the four
  operations and is the less actively maintained one, in a parser that also reads
  untrusted files.
- **Tradeoff.** Layout fidelity is lower than upstream's. If exact layout becomes a
  requirement, `goccy/go-yaml` is the recorded alternative, and R8 already bounds the risk
  of its edit API. The change touches one package.
- **Serves.** DH-O-2. **Verified by.** DH-T-YAML-4, DH-T-YAML-5, DH-T-YAML-6.

<a id="dh-adr-yaml-3"></a>
### DH-ADR-YAML-3 The harness, not the module, fixes the dialect

- **Decision.** The module is used to produce a node tree. The harness package walks the
  tree itself: it rejects what R3 lists, types unquoted scalars by R4, and builds plain Go
  values (`map[string]any`, `[]any`, string, int64, float64, bool, nil) that carry
  positions. Typed decoding is then done by the schema package from those values
  ([DH-ADR-SCHEMA-3](zod.md#dh-adr-schema-3)), never by the YAML module's reflection.
- **Why.** The module keeps YAML 1.1 behaviour for compatibility: it reads `017` as 15,
  and reads `yes` as `true` when the target field is a boolean (v3 `README.md:47-54`; the
  unverified probe observation reports the octal case for all three candidates, to be
  measured by `DH-T-YAML-3`). Inference from the YAML 1.2
  core rules that upstream selects (`vendor/include/src/index.ts:35`): upstream reads
  `017` as 17 and `yes` as a string. A dialect that depends on the module and on the
  target type cannot be matched by the JSON codec and changes if the module is swapped.
- **Tradeoff.** About a hundred lines of scalar typing and tree walking that the module
  could do. A leading-zero number such as `0644` is an error asking for `0o644` or quotes.
- **Serves.** DH-O-2. **Verified by.** DH-T-YAML-2, DH-T-YAML-3.

## 6. Design notes

The package is new harness code, so it is listed in section 7 for the boot area.

```go
// Parse reads one document under the strict dialect.
func Parse(file string, text []byte, opts ...Option) (*Document, error) // *SyntaxError{File, Line, Column, Message}
func WithTag(name string) Option                                         // for example "!expr"
func WithLimits(maxBytes, maxDepth int) Option

func (d *Document) Value() Value            // plain values; Value.Pos() gives line and column
func (d *Document) Edit(ops ...Op) ([]byte, error) // R6, R7, R8; the document itself is not changed
func Dump(v any) ([]byte, error)            // R10
func DecodeFrontmatter(markdown []byte) (fields Value, body []byte, err error)
```

- **Edit.** `Edit` copies the node tree, applies the operations, encodes with indent 2,
  unlimited line width and indented nested lists, restores the blank line above each
  top-level entry that had one (the entry's first line, less its comment lines, is known
  from the node positions), then parses its own output and compares data and comments
  (R8). New entries are separated by a blank line.
- **Comments.** A comment separated from the first entry by a blank line is the file
  header and belongs to the document. A comment directly above an entry or key belongs to
  it. Deleting an entry moves its comment to the next entry, as upstream does
  (`packages/boot/plugin-manager/src/patch.ts:60-66`): the harness never deletes text a
  person wrote.
- **Who writes.** Only the loader's layer source writes a layer file
  ([DH-D-LOADER-7](cordis-loader.md#dh-d-loader-7)). The config editor and the plugin
  manager call the loader's mutation API ([DH-D-LOADER-8](cordis-loader.md#dh-d-loader-8))
  and do not open the file. This removes upstream's second and third parse of one file.
- **Failure, retry and cleanup.** `Parse` and `Edit` allocate memory only; nothing is
  owned by a fiber and nothing needs releasing. `Edit` returns text; the caller writes it
  atomically. A refused edit (R8) leaves the file untouched and the caller keeps the
  change in memory, where the loader's `Flush` reports it
  (`DH-LIB-LOADER-R9`). A panic inside the module is recovered at the package boundary
  and returned as an error naming the file (R9).
- **Ownership and access.** A `Document` is owned by whoever parsed it. Any component may
  *use* the package; only the owner of a file (the loader for layers, the credentials
  provider for its store) may *write* that file.

## 7. Risks and open questions

1. **New package for the boot area.** `internal/boot/yamlx` (about 500 lines: tree walk,
   scalar typing, edit operations, blank-line restore, verification) has no deliverable
   yet. It is the mechanism behind [DH-D-LOADER-2](cordis-loader.md#dh-d-loader-2); the
   boot area should schedule it there or as its own deliverable.
2. **v4.0.0 is two weeks old.** Mitigation: exact pin, the fuzz target required by
   [DH-ADR-TOOLCHAIN-3](../upstream/toolchain.md#dh-adr-toolchain-3), and the R8 check.
   Fallback: v3.0.6 has the same node tree but needs positions scraped from error text.
3. **Layout.** Is losing blank lines inside an entry acceptable to the owner? If not,
   switch to `goccy/go-yaml` under [DH-ADR-YAML-2](#dh-adr-yaml-2).
4. **Credentials store.** Whether credentials live in a YAML file, the operating system
   keychain or both is for the credentials area. A file store uses `Document.Edit`.
5. **Frontmatter of untrusted skills.** The limits of R9 (bytes, depth) need values from
   the skills area.
6. **`DH-O-2`** is the outcome these decisions serve; no deliverable is declared here.

## 8. Verification

All are local Go tests on in-memory text. Nothing here has been run. The probe
observations of section 5 are unverified research notes; `DH-T-YAML-2` to `-5` are what
establish the behaviour the choice relies on.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-YAML-1` | local | `DH-LIB-YAML-R1` | A test over the import graph of the harness module finds the YAML module imported by exactly one package |
| `DH-T-YAML-2` | local | `DH-LIB-YAML-R2`, `DH-LIB-YAML-R3` | A table of invalid files (bad indentation, anchor, alias, merge key, second document, duplicate key, unknown tag) each yields an error whose line and column match the offending token |
| `DH-T-YAML-3` | local | `DH-LIB-YAML-R4` | A table of unquoted words gives the listed type for each; `yes`, `on`, `1:30` are strings; `017` and `0b101` are located errors; the YAML and JSON forms of one entry list give equal values |
| `DH-T-YAML-4` | local | `DH-LIB-YAML-R5`, `DH-LIB-YAML-R6` | For each of the four operations on a commented file: every input comment is in the output next to the same entry or key; untouched lines are byte-identical apart from the layout rules; a deleted entry's comment is above the next entry; `!expr` scalars are unchanged |
| `DH-T-YAML-5` | local | `DH-LIB-YAML-R7` | Editing the output of an edit with no operations returns the same bytes; blank lines between top-level entries are kept; a 300-character line is not wrapped |
| `DH-T-YAML-6` | local | `DH-LIB-YAML-R8` | With an encoder stub that drops a comment or changes a value, `Edit` returns an error and no text |
| `DH-T-YAML-7` | local | `DH-LIB-YAML-R9`, `DH-LIB-YAML-R10` | Input over the size or depth limit is an error; a fuzz target finds no panic; `Dump` of one value is byte-identical across runs and contains no `&` or `*` |

## 9. Units dispositioned by this spec

`UP-DEP-yaml` (replaced), `UP-DEBT-found.L1.1` (resolved).
