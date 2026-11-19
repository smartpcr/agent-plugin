# picomatch: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". No harness code exists.
>
> **Unverified research notes.** Where this document says "probe", it reports an
> observation made during library research on 2026-11-18 by running a throwaway program
> against the candidate module outside this repository. That was against the project's
> rule that nothing is built or run in the design task, and the program was not kept, so
> the observation cannot be repeated from this repository. Every such statement is
> therefore **unverified and to be measured** by `DH-T-PICOMATCH-1` to `-3` (section 8);
> if a test contradicts an observation, the choice is reopened.

| | |
|---|---|
| **Library ID** | `DH-LIB-PICOMATCH` |
| **Upstream unit** | `UP-DEP-picomatch` |
| **Upstream version** | 4.0.4 and 4.0.7, both installed (`pnpm-lock.yaml`) |
| **Kind** | runtime; also bundled into the client by one package (`packages/client/ui-sidebar-right`) |
| **Used by** | boot, experimental, vendor; client (development dependency) |
| **Go choice** | named Go module `github.com/bmatcuk/doublestar/v4`, behind one harness helper |
| **Related areas** | UI (sidebar routing), the experimental code-review feature, boot (any file watcher) |

## 1. What it is and why upstream needs it

A **glob** is a short pattern for names and paths, such as `*.md` or `**/node_modules`.
`picomatch` compiles a glob into a function that answers "does this string match?". It
does not read the file system.

Globs look alike but are not one language. Each library has its own **dialect**: whether
`*` matches a name that starts with a dot, whether `**` exists, whether `{a,b}` works,
what an invalid pattern does. A pattern written for one dialect can silently match more,
less or nothing under another. Upstream calls picomatch from six places with four
different option sets, so the same pattern already means different things in different
places. That is the main thing to fix.

## 2. How upstream uses it

| Use site (`path:line`) | What is matched | Options, and what they change | In Go |
|---|---|---|---|
| `packages/boot/hmr/src/index.ts:218-223`, `:425`, `:439` | Paths the hot-reload file watcher ignores, relative to the watch base. Defaults: `**/node_modules`, `**/.*`, `cache`, `data`. | none: `*` does not match a name that starts with a dot, which is why `**/.*` is listed | Code hot reload is not a goal ([DH-ADR-LOADER-8](cordis-plugins.md#dh-adr-loader-8)); layer files are watched by exact path. If the boot area adds a watcher for plugin source directories, its ignore list uses the helper of section 6. |
| `vendor/hmr/src/index.ts:107` | The same, in the vendored original | none | as above |
| `packages/experimental/webworker-packer/src/pack.ts:45-51` | Files left out of, or served beside, the browser Preview image | `dot: true` | not built: the Preview is dropped ([DH-ADR-17](../design.md#dh-adr-17)) |
| `packages/experimental/webworker-packer/src/pack.ts:278-287` | npm's `files` allowlist: each pattern also admits everything under it, and a leading `!` subtracts | `dot: true`, own code for `!` | not applicable: npm packaging |
| `packages/experimental/webworker-runtime/src/shell/expand.ts:111` | One path segment of a shell word such as `*.md`, in the Preview's emulated shell | `dot` only when the segment itself starts with `.` (the shell rule) | goes with the Preview; a real shell expands its own globs |
| `packages/experimental/webworker-runtime/src/shell/programs/files.ts:69` | `find -name`, against the base name | `dot: true` | as above |
| `packages/experimental/client-ui-forge/src/layers.ts:77` | Sort the changed files of a code review into test, interface and implementation, using built-in lists such as `**/*.d.ts` and `**/openapi.{json,yaml,yml}` (`:55-59`). `.gitattributes` patterns use the package's own matcher, not picomatch (`:11`, `:114`). | `dot: true` | the helper, on the host |
| `packages/client/ui-sidebar-right/src/client/tab-registry.ts:34`, `:211-221` | Choose which sidebar tab type opens an address. A pattern with `:` is matched against the whole address (`dsh-resource://plan/**`); one without is matched against the address's path. | the `posix` build, `nocase: true`, `dot: true`, and `basename: true` for path patterns so that `*.md` matches at any depth | the helper, on the host: the UI is rendered by the host ([DH-ADR-3](../design.md#dh-adr-3)) |

Patterns that components declare for the sidebar: `dsh-resource://plan/**`
(`packages/client/ui-plan/src/client/index.ts:66`), `dsh-resource://changes-review/**`
(`packages/client/ui-deliverables/src/client/review-definition.ts:42`), and
`dsh-resource://file/[\s\S]+` (`packages/client/ui-sidebar-documentpreview/src/client/definition.ts:50`).

## 3. Tech debt upstream carries

Search performed: every import of `picomatch` (including the `picomatch/posix` entry);
`patches/` and `pnpm-workspace.yaml` (no patch or pin); the pattern lists and declared
patterns named above.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L1.2` | **Patterns depend on one library's quirks and on per-call options.** One shipped pattern, `dsh-resource://file/[\s\S]+`, is a regular expression fragment that only works because picomatch passes such text through. Whether `*` matches a dot name differs between call sites (off in the watcher, on in four places, conditional in one), so a pattern cannot be read without knowing its call site. Two versions of the library are installed. | `packages/client/ui-sidebar-documentpreview/src/client/definition.ts:50`; `packages/boot/hmr/src/index.ts:425`; `packages/experimental/webworker-runtime/src/shell/expand.ts:111` | Resolved by [DH-ADR-PICOMATCH-1](#dh-adr-picomatch-1): one dialect with no regular-expression pass-through, written down once; the two behaviours that do vary (case folding, base-name matching) are named options of one helper. |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-PICOMATCH-R1` | Every pattern a person writes in configuration or a component declares uses one dialect: `*` (any run of characters except `/`), `?`, `[...]` classes, `{a,b}` alternatives, and `**` as a whole path segment meaning any depth, including none. `/` is the separator on every platform. | the six use sites; `packages/experimental/client-ui-forge/src/layers.ts:55-59` |
| `DH-LIB-PICOMATCH-R2` | `*` and `**` match names that start with a dot, everywhere. A caller that wants to skip dot names lists a pattern for them. | Inference: four of six upstream sites set `dot: true`; one uniform rule replaces the option |
| `DH-LIB-PICOMATCH-R3` | A pattern is checked when it is loaded or registered. A malformed pattern is an error that names the pattern and where it was written; it is never a pattern that silently matches nothing. | Inference: upstream compiles patterns at first use and has no error path |
| `DH-LIB-PICOMATCH-R4` | Two variations are available as named options and nowhere else: fold case (ASCII and Unicode simple folding), and base-name matching (a pattern without `/` is tested against the last path segment). | `packages/client/ui-sidebar-right/src/client/tab-registry.ts:211-221` |
| `DH-LIB-PICOMATCH-R5` | A set of patterns can be compiled once into "matches any" and "include minus exclude" predicates. | `packages/experimental/webworker-packer/src/pack.ts:278-287`; `packages/boot/hmr/src/index.ts:425` |
| `DH-LIB-PICOMATCH-R6` | Matching is a pure function of two strings. It does not touch the file system, cannot run a regular expression supplied by a pattern, and pattern and input length are bounded. | Inference: patterns can come from plugin manifests, which are not trusted |

## 5. Go choice

**Decision.** `github.com/bmatcuk/doublestar/v4` at v4.10.4, called only through one
harness helper.

**Why.** Its dialect is exactly R1, it validates patterns (R3), and `Match` is a pure
string function (R6). The standard library has no `**` and no `{a,b}`, which the built-in
lists and the watcher defaults need.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **`github.com/bmatcuk/doublestar/v4` (chosen)** | `*`, `?`, classes with `!` or `^`, `{a,b}` with nesting, `**` as a whole segment; `Match` always uses `/`; `ValidatePattern`; no dependencies | `Match` has no fold-case option (only its file-walking functions do), so the helper folds both strings. No base-name mode, so the helper applies `path.Base`. | v4.10.4, released 2026-10-24, MIT, `go.mod` has no requirements, pure Go. Pattern table at `README.md:387-416`. Checked 2026-11-18 with `go list -m -json`, `go mod download -json`, the `LICENSE` in the module cache, and the probe. |
| Standard library `path.Match` | No dependency | No `**` (it reads `**` as `*`), no `{a,b}`, class negation only with `^` (`go doc path.Match`). `**/*.d.ts` and `openapi.{json,yaml,yml}` cannot be written. | Go 1.27.4 |
| `github.com/gobwas/glob` | Patterns compile once to a fast matcher; configurable separators | By default `**` is "a `*` that crosses separators", not a path segment: `a/**/b` does not match `a/b` unless the `Globstar` compiler flag is set (`readme.md:119-136`). Two matching modes are a second dialect risk. | v1.1.0, released 2026-10-26 (v1.0.0 was 2026-08-28), MIT, no requirements. Same check, no probe. |
| Translate globs to `regexp` in own code | No dependency | A translation layer to write, fuzz and document | n/a |

**Dialect differences that change behaviour.** The doublestar column is from the probe
(unverified, to be measured by `DH-T-PICOMATCH-1`);
the picomatch column is from the upstream call sites where cited, otherwise from the
library's documentation and marked "doc".

| Case | picomatch as upstream calls it | doublestar | Consequence for the Go design |
|---|---|---|---|
| `*` against `.env` | no match in the watcher; match where `dot: true` | match | R2: one rule. The watcher default `**/.*` stays meaningful. |
| `**/node_modules` against `node_modules` | match (doc) | match | none |
| `tests/**` against `tests` | match (doc) | match | none |
| `*.md` against `a/b/readme.md` | match only with `basename: true` | no match | R4: base-name option, or write `**/*.md` |
| `*.MD` against `readme.md` | match only with `nocase: true` | no match | R4: fold-case option |
| `dsh-resource://file/[\s\S]+` | matches any file address (regular-expression pass-through) | no match: a class of two letters, then a literal `+` | rewrite as `dsh-resource://file/**`; nothing in R1 can express "one or more of anything" except `**` and `*` |
| `@(a\|b)`, `!(a)` (extended globs) | supported (doc) | literal text, no match | not in the dialect; use `{a,b}` |
| leading `!pattern` | negation (doc) | literal `!` | R5: an explicit exclude list |
| `[[:alpha:]]` | supported (doc) | no match | not in the dialect |
| `a/**b` | `**` not alone in a segment acts as `*` (doc) | the same | none |
| malformed pattern such as `[a` | no error path at the call sites | `ErrBadPattern` | R3 |

**Tradeoff.** Patterns written for upstream that use extended globs, a leading `!`, or
regular-expression text do not carry over. No shipped pattern except the one above does.

<a id="dh-adr-picomatch-1"></a>
### DH-ADR-PICOMATCH-1 One glob dialect, one helper, options by name

- **Decision.** All harness globs go through one helper package. The dialect is R1 and
  R2. The only options are fold-case and base-name. Patterns are validated when loaded.
  The dialect is documented once, for operators and plugin authors.
- **Why.** `UP-DEBT-found.L1.2`. Verdict on upstream: keep globs as the way to name sets
  of paths and addresses; drop per-call option sets and the regular-expression escape.
- **Tradeoff.** The emulated shell's rule (a dot name matches only a pattern that starts
  with a dot) is not offered; it is needed only by the Preview shell.
- **Serves.** DH-O-2, DH-O-12. **Verified by.** DH-T-PICOMATCH-1, DH-T-PICOMATCH-2, DH-T-PICOMATCH-3.

## 6. Design notes

```go
// package glob (proposed: internal/util/glob)
func Compile(pattern string, opts ...Option) (Matcher, error) // *PatternError{Pattern, Reason}
func CompileSet(include, exclude []string, opts ...Option) (Matcher, error)
func FoldCase() Option
func BaseName() Option // a pattern without "/" is tested against path.Base(name)
type Matcher interface{ Match(name string) bool }
```

`Compile` validates with the module, rejects patterns over a length limit, and stores the
pattern (folded if asked). `Match` folds the input if asked and calls the module. An
address such as `dsh-resource://plan/a/b` is matched as a `/`-separated string; the empty
segment after the scheme is ordinary. Paths from the file system are converted to `/`
before matching.

Failure and ownership: a matcher is an immutable value with no resource behind it. A bad
pattern in a config layer is a validation issue of that entry, with the file position
supplied by the config codec; the entry does not start and its siblings do
(`DH-LIB-LOADER-R6`).

This specification covers only picomatch's call sites. Upstream has other matchers (its
own `.gitattributes` matcher, tool-level file search). Inference: they should use this
helper too unless they must follow another program's rules, as `.gitignore` handling must.

## 7. Risks and open questions

1. **New helper package.** `internal/util/glob` has no deliverable; the shared-utilities
   area should own it.
2. **Other glob users.** Permission rules, file-search tools and ignore files belong to
   other specifications. A second dialect there would recreate the debt; the owner of
   each should either use the helper or say why not.
3. **Watcher.** Whether any directory watcher with an ignore list survives without code
   hot reload is for the boot area.
4. **Worst-case matching time** of the module on adversarial patterns was not measured.
   The length bound of R6 limits it; `DH-T-PICOMATCH-3` includes a timing case.

## 8. Verification

Local Go tests on strings. Nothing here has been run; the unverified probe compared the module with
the upstream patterns only.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-PICOMATCH-1` | local | `DH-LIB-PICOMATCH-R1`, `DH-LIB-PICOMATCH-R2` | A table with every upstream built-in list entry and declared address pattern (the regular-expression one rewritten), each with matching and non-matching inputs, including dot names, zero-depth `**`, nested `{}` and Windows paths converted to `/` |
| `DH-T-PICOMATCH-2` | local | `DH-LIB-PICOMATCH-R4`, `DH-LIB-PICOMATCH-R5` | `*.md` with base-name matches `/a/b/README.MD` only with fold-case; an include and exclude set admits and subtracts as listed |
| `DH-T-PICOMATCH-3` | local | `DH-LIB-PICOMATCH-R3`, `DH-LIB-PICOMATCH-R6` | Malformed and over-long patterns return a `*PatternError`; a pattern of repeated `*` against a long non-matching input finishes within a fixed bound; a fuzz target finds no panic |

## 9. Units dispositioned by this spec

`UP-DEP-picomatch` (replaced), `UP-DEBT-found.L1.2` (resolved).
