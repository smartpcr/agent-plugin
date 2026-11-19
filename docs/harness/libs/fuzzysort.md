# fuzzysort: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-FUZZYSORT` (matching and ranking), `DH-LIB-FUZZYSORTNORM` (Unicode normalisation it needs) |
| **Upstream unit** | `UP-DEP-fuzzysort` |
| **Upstream version** | 4.0.2 |
| **Kind** | runtime, in one package (`packages/util/text-search`) that ten others depend on |
| **Used by** | util; through it nine client UI packages and `packages/context/session-reference` |
| **Go choice** | own code, with `golang.org/x/text/unicode/norm` for normalisation |
| **Related areas** | conversation UI (commands, models, skills, shortcuts, schedules, workspaces, plugin manager), session references |

## 1. What it is and why upstream needs it

**Fuzzy matching** finds an item from a few typed letters that appear in it in order but
not necessarily next to each other: `cpt` finds `compact`. fuzzysort is a small
JavaScript library that scores such a match.

Upstream uses it for every short pick-list a person filters by typing: slash commands,
models, skills, keyboard shortcuts, schedules, workspaces, installed plugins, and
sessions that can be referenced. The lists hold tens to a few thousand items and live in
memory. Almost all of the behaviour is upstream's own code in
`packages/util/text-search/src/index.ts` (302 lines); the library supplies one thing, the
score of a letters-in-order match.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/util/text-search/src/index.ts:201` | Prepare the tail of a name that starts at a word start | `fuzzysort.prepare` |
| `packages/util/text-search/src/index.ts:204-207` | Score one query term against that tail; keep the match only if its first matched letter is at a word start | `fuzzysort.single`, `result.score`, `result.indexes` |

The surrounding own code defines the product behaviour (`index.ts:1-19`, `:36-40`,
`:213-230`, `:282-300`): each item offers *fuzzy keys* (names, titles) and *substring
keys* (descriptions, paths); every word of the query must match some key of the item; a
term scores by its best tier, which is prefix of a name, then substring of a name, then
letters-in-order starting at a word start, then substring of a description; ties keep the
list's own order. Before comparing, text is folded: case, full-width and half-width forms
(Unicode NFKC), accents in the range U+0300 to U+036F, and letters with a built-in stroke
such as `ł` and `ø` (`:83-102`). A word starts after a separator, at a capital following
a lower-case letter, and at every character of scripts written without spaces (`:74-79`).

## 3. Tech debt upstream carries

Search performed: every import of `fuzzysort`; `patches/` and `pnpm-workspace.yaml`
(nothing); `packages/util/text-search/README.md:77-83`, whose three limitation bullets
are ledger units owned by the util area.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L9.4` | The library does not fit the rule it is used for, so the caller works around it twice: it reports one alignment per target, so each word start is prepared and tried as a separate target; and its built-in cache grows for the life of the process, so the caller avoids it and keeps its own. | `packages/util/text-search/src/index.ts:179-182`, `:199-201` | Resolved by `DH-LIB-FUZZYSORT`: the Go scorer searches word-start alignments directly and keeps no global state. |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-FUZZYSORT-R1` | Filter and rank a list by a typed query with the four tiers, the every-term-must-match rule and the keep-list-order tie rule described in section 2. A blank query returns the list unchanged. | `text-search/src/index.ts:213-230`, `:282-300` |
| `DH-LIB-FUZZYSORT-R2` | Matching ignores case, width, the listed accents and strokes; word starts follow the three rules in section 2, including unspaced scripts. | `text-search/src/index.ts:74-102` |
| `DH-LIB-FUZZYSORT-R3` | Preparing a list is done once and reused across queries; replacing the list reuses the preparation of unchanged strings; nothing is retained after the index is dropped. | `text-search/src/index.ts:126-165`, `:266-273` |
| `DH-LIB-FUZZYSORT-R4` | The same list and query always give the same order. | Inference: the host renders the list ([DH-ADR-3](../design.md#dh-adr-3)), so tests compare rendered output |

## 5. Go choice

**Decision.** Own code: a `textsearch` package of about 300 lines that ports upstream's
own module and replaces the one library call with a scorer of about 60 lines.
`golang.org/x/text/unicode/norm` supplies NFKC and NFKD, which the standard library does
not have.

**Why.** The library is one function wrapped in two workarounds
(`UP-DEBT-found.L9.4`). A scorer written for the actual rule (letters in order, first
letter at a word start, bonus for consecutive letters and for later word starts) is
smaller than the adapter around a general one, and its ranking cannot change under an
upgrade.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** own scorer | Exactly the rule; no global state; ranking fixed by golden tests | Scores differ from fuzzysort's, so the order inside the letters-in-order tier can differ from upstream | not applicable |
| **Chosen for normalisation:** `golang.org/x/text` (`unicode/norm`) | Maintained by the Go team | A large module, of which one package is linked | v0.44.0, 2026-11-09, BSD 3-Clause. Three files import `C`, all behind the `icu` build tag or in a comparison tool, none in `unicode/norm`. Checked with `go list -m -json`, `go mod download -json`, `LICENSE` and the build tag in `cases/icu.go`, 2026-11-18. |
| `github.com/sahilm/fuzzy` | Ready-made scorer with matched positions | Same shape as fuzzysort: one alignment per target, so the word-start workaround would return | v0.1.3, 2026-06-11, MIT, no cgo; same method |
| `github.com/junegunn/fzf` (`src/algo`) | The best-known scoring rules | A package inside a terminal application's module; importing it pulls that module's requirements (terminal and shell libraries) into the build list | v0.75.0, 2026-11-07, MIT; same method |

**Tradeoff.** Where two items both match only by letters in order, their relative order
may differ from upstream. This is allowed by [DH-ADR-2](../design.md#dh-adr-2) and pinned
by tests.

Design notes in brief: `textsearch.New(items, keys)` returns an immutable index;
`Index.Update` returns a new one; `Index.Search(query)` returns positions in rank order.
The package holds memory only, cannot fail, and owns no resource, so no fiber owns
anything. The UI filters on the host: each keystroke is a request that re-renders the
list.

Open questions: whether typo tolerance, which upstream lists as missing
(`packages/util/text-search/README.md:81`), should be added is for the UI area. Another
library agent may also declare `golang.org/x/text`; one declaration should remain.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-FUZZYSORT-1` | local | `DH-LIB-FUZZYSORT-R1`, `R4` | Table ported from upstream's examples: `cpt` finds `compact`; `fab` does not match inside `Refactor build`; tier order; multi-term queries; ties keep list order; repeated runs are identical. |
| `DH-T-FUZZYSORT-2` | local | `DH-LIB-FUZZYSORT-R2` | Full-width and half-width forms, accented and stroked letters, camel-case humps, and Chinese, Japanese and Korean names match as specified. |
| `DH-T-FUZZYSORT-3` | local | `DH-LIB-FUZZYSORT-R3` | `Update` reuses prepared strings (allocation count) and a dropped index is collectable; a benchmark over 5,000 items records the time per query. |

Local tests only. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-fuzzysort`, `UP-DEBT-found.L9.4`.
