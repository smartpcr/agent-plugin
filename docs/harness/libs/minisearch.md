# minisearch: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference".
>
> Revised on 2026-11-19. The owner decided that plugins are installed from local
> directories and that the harness downloads and discovers none
> ([DH-ADR-13](../design.md#dh-adr-13)), and area A21 recorded that the plugin market is
> not built ([DH-ADR-BOOT-11](../areas/boot.md#dh-adr-boot-11)). The market's search
> index was this library's only use, so the Go choice is now **dropped**. Sections 4 and
> 5 are kept as the starting point for a later registry design; nothing in them is
> built or tested in this design.

| | |
|---|---|
| **Library ID** | `DH-LIB-MINISEARCH` |
| **Upstream unit** | `UP-DEP-minisearch` |
| **Upstream version** | 7.2.0 |
| **Kind** | runtime, one package |
| **Used by** | boot (`packages/boot/plugin-market`) |
| **Go choice** | dropped: there is no plugin market to search ([DH-ADR-BOOT-11](../areas/boot.md#dh-adr-boot-11)) |
| **Related areas** | A21, which records the drop of the plugin market ([../areas/boot.md](../areas/boot.md)) |

## 1. What it is and why upstream needs it

A **full-text index** answers "which documents contain these words" without scanning
every document: it stores, for each word, the list of documents that contain it (an
**inverted index**). MiniSearch is such an index for JavaScript, held in memory, with
prefix matching, typo tolerance and per-field weights.

Upstream uses it in one place: searching a **plugin market index**, a JSON document
downloaded from a configured source that lists the plugins available to install. The
host builds the text index the first time someone types a query and keeps it while the
market document is cached. This is separate from the small pick-lists handled in
[fuzzysort.md](fuzzysort.md) because a market can list tens of thousands of plugins
(`packages/util/text-search/README.md:83`).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/boot/plugin-market/src/search.ts:96-109` | Configure the index: six fields (name, title, keywords, description, author, category and label titles); name and title weigh 3, keywords 2; a term matches as a prefix; terms of four or more characters also match within an edit distance of one fifth of their length, at most 2; every term must match | `new MiniSearch({ fields, tokenize, processTerm, searchOptions })` |
| `packages/boot/plugin-market/src/search.ts:24-31` | Split text into terms: normalise to NFKC, take runs of letters and digits, and split a run of Chinese, Japanese or Korean characters into overlapping pairs plus its last character | custom `tokenize` |
| `packages/boot/plugin-market/src/search.ts:110-128` | Add every plugin, 500 per turn of the event loop so the host stays responsive | `addAllAsync(…, { chunkSize })` |
| `packages/boot/plugin-market/src/search.ts:89-92`, `:69-87` | Search, then filter by category and labels and order by index position, relevance, publication time or a custom key | `search(text)`, `result.id`, `result.score` |

## 3. Tech debt upstream carries

Search performed: every import of `minisearch`; `patches/` and `pnpm-workspace.yaml`
(nothing); `packages/boot/plugin-market/README.md:127-142`.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| (owned by the boot area) | The whole index is held in memory: 20,000 plugins took about 0.8 s to index and 130 MiB of heap, and handing the parsed document to the host blocks its event loop | `packages/boot/plugin-market/README.md:136` | Does not arise: no market index is downloaded or indexed ([DH-ADR-BOOT-11](../areas/boot.md#dh-adr-boot-11)). The unit's ledger row belongs to area A21. |

No debt of its own was found for this library.

## 4. Requirements a later registry search would have to meet

Not requirements of this design: no component implements them and no test proves them.

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-MINISEARCH-R1` | A text query returns the plugins in which every query term matches some field, as a whole term, as a prefix, or (for terms of four or more characters) within the stated edit distance. Results carry a relevance score in which name and title count three times and keywords twice as much as other fields. | `search.ts:96-109` |
| `DH-LIB-MINISEARCH-R2` | Terms are produced by the tokenisation in section 2, identically for indexed text and for queries, so text without spaces between words is searchable. | `search.ts:9-31` |
| `DH-LIB-MINISEARCH-R3` | The index is built on first use without delaying other requests, is built once per market document, and is released with that document. | `search.ts:58-59`, `:89-92`, `:110-128` |
| `DH-LIB-MINISEARCH-R4` | Results combine with category and label filters and with the four orderings; equal scores keep index order. | `search.ts:69-87`, `:132-154` |

## 5. Go choice

**Decision.** Dropped. The harness has no plugin market, no index reader and no
`plugin_market` tool, so nothing searches a market
([DH-ADR-BOOT-11](../areas/boot.md#dh-adr-boot-11), by
[DH-ADR-13](../design.md#dh-adr-13)). **Why.** The index existed for one document, the
downloaded market index, and the harness downloads none. **Tradeoff.** No discovery:
an operator finds a plugin outside the harness and places its directory by hand.
Area A21's record for a later registry says queries should be answered by the registry
server, which would make a local index unnecessary even then.

**What was first chosen, kept for a later registry design.** Own code: an inverted index in memory. A sorted slice of distinct terms
gives prefix lookup by binary search; typo tolerance is a bounded edit-distance scan over
that slice; postings are document numbers with a field mask.

**Why.** The need is narrow (one document set, six fields, three match kinds, one
scoring rule) and the tokeniser is upstream's own in any case. Inference: about 300
lines.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| Own inverted index (first choice, not built) | No dependency; memory layout under our control; exact match rules | Scoring is ours to define and will not equal MiniSearch's numbers | not applicable |
| `github.com/blevesearch/bleve/v2` | Complete search engine with prefix and fuzzy queries and field boosts | Its `go.mod` requires more than a dozen modules, among them `github.com/blevesearch/go-faiss` (Inference from its name: bindings to the FAISS vector library, which is not Go), which enter the build list whether or not that code is compiled; far more than one search box needs | v2.6.3, 2026-11-04, Apache 2.0; no file in the module itself imports `C`; requirements read from `go.mod`. Checked with `go list -m -json`, `go mod download -json`, `LICENSE` and `go.mod` in the module cache, 2026-11-18. |
| SQLite FTS5 in the session database ([DH-ADR-PERSISTENCE-8](../upstream/session-and-persistence.md#dh-adr-persistence-8)) | Already in the product; ranking with column weights; prefix queries | No typo tolerance; puts a transient download cache into the durable store and its write path | see that document |
| `github.com/agnivade/levenshtein` for the edit distance | Small | A 30-line bounded edit distance is simpler to own than a module with two requirements | v1.2.2, 2026-10-17; existence, date and `go.mod` only, licence file (`License.txt`) not read |

**Tradeoff.** Relevance order is close to, not equal to, upstream's. A much larger
market would need a query service on the market's side, as upstream also says
(`packages/boot/plugin-market/README.md:136`).

Design notes in brief: `marketsearch.Build(ctx, docs) (*Index, error)` runs on a
goroutine started by the first query; concurrent queries wait on the same build
(`sync.Once` with a context). The index is owned by the cache entry of its market
document, which the market service's fiber owns; when the document is replaced or the
fiber unloads, the index becomes unreachable and is collected. Building cannot leave
partial state: a cancelled build is discarded and the next query starts again.

The question this document first left to the extensions area, whether the Go design
keeps a plugin market at all, is answered: it does not
([DH-ADR-BOOT-11](../areas/boot.md#dh-adr-boot-11)).

## 8. Verification

None. A dropped library has nothing to prove; the three tests this document first
declared (`DH-T-MINISEARCH-1` to `-3`) were withdrawn with the choice, and their ledger
rows were removed.

## 9. Units dispositioned by this spec

`UP-DEP-minisearch`.
