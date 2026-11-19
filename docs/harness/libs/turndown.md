# turndown and @joplin/turndown-plugin-gfm: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go module facts were checked on 2026-11-18 with Go 1.27.4
> (`go list -m -json <module>@latest`, `go mod download -json`, the `LICENSE` file and the
> source in the module cache). No module code was compiled or run.

| | |
|---|---|
| **Library ID** | `DH-LIB-TURNDOWN` (one family, two packages) |
| **Upstream units** | `UP-DEP-turndown`, `UP-DEP-@joplin/turndown-plugin-gfm`, and the workaround `UP-DEBT-found.L12.2` |
| **Upstream version** | `turndown` 7.2.4; `@joplin/turndown-plugin-gfm` 1.0.67 |
| **Kind** | runtime, pure JavaScript |
| **Used by** | web (`packages/web/tool-web`) |
| **Go choice** | named Go module `github.com/JohannesKaufmann/html-to-markdown/v2`, on the HTML parser of `golang.org/x/net/html`, with three harness rules registered on it |
| **Related areas** | A15 (web search and fetch; owns the `web_fetch` tool) |

## 1. What it is and why upstream needs it

The `web_fetch` tool lets the model read a web page. A page arrives as HTML, which is
mostly markup the model does not need and pays tokens for. turndown converts HTML to
**Markdown**, the plain-text format models read well; the GFM plugin adds the two
GitHub-flavoured extensions that pages use most, tables and strikethrough.

| Package | What it adds |
|---|---|
| `turndown` | Parses HTML into a document tree (with its bundled parser, domino) and walks it, emitting Markdown through replaceable rules |
| `@joplin/turndown-plugin-gfm` | Rules for tables, strikethrough and task lists |

The page is untrusted in two ways. Its *content* may try to instruct the model; the
tool labels it as external data and that is outside this library. Its *shape* may try
to exhaust the converter: very deep nesting, or a table cell that claims to span a
million columns. Upstream guards against both by hand, and those guards are
requirements here.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/web/tool-web/src/fetch.ts:25-30` | One shared converter with fixed model-facing style: `#` headings, fenced code blocks, `-` list markers, GFM tables and strikethrough | `new TurndownService({ headingStyle: 'atx', codeBlockStyle: 'fenced', bulletListMarker: '-' })`, `.use(gfm)` |
| `packages/web/tool-web/src/fetch.ts:31-49` | Drop content a reader would not see: `script`, `style`, `noscript`, `template`, `iframe`, `object`, `embed`; anything with `hidden` or `aria-hidden="true"`; hidden form inputs; inline `display: none` and `visibility: hidden` or `collapse` | `.addRule(…, { filter, replacement: () => '' })` |
| `packages/web/tool-web/src/fetch.ts:51-95` | Replace the plugin's cell and row rules so that `colspan` is ignored: Markdown cannot show a spanning cell, and ignoring it keeps work and output proportional to the page instead of to a number in an attribute | `.addRule` for `th`, `td`, `tr` |
| `packages/web/tool-web/src/fetch.ts:112-223` | Before converting, scan the text and refuse HTML nested deeper than 512 elements, because the bundled parser and the walk become very slow on unclosed tags (2 seconds at depth 2,000, measured upstream) and conversion blocks the single thread | own code, not the library |
| `packages/web/tool-web/src/fetch.ts:242-256` | Cut the source to the output limit before converting; on any conversion error return a fixed marker instead of raw markup | `.turndown(html)` in `try` |
| `packages/web/tool-web/src/fetch.ts:302-318` | Convert once per result although two callers ask for it | own memo, not the library |

## 3. Tech debt upstream carries

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L12.2` | A hand-written 100-line lexical HTML scanner exists only to protect the converter from deep nesting, and it "over-counts rather than hiding nesting", so some valid pages are refused | `packages/web/tool-web/src/fetch.ts:112-223` | **Resolved** by `DH-LIB-TURNDOWN`: the Go HTML parser enforces the limit itself. `golang.org/x/net/html` stops with an error when its stack of open elements passes 512 (`html/parse.go:238-243`, documented at `:2277`). The scanner is not written. |
| Missing type declarations | The plugin ships no TypeScript types, so upstream writes an ambient declaration | `packages/web/tool-web/src/turndown-plugin-gfm.d.ts:2-8` | Not applicable: a Go module is typed. |
| Synchronous conversion blocks the event loop, so the tool's timeout cannot fire during it | `packages/web/tool-web/src/fetch.ts:113-116` | Not applicable in that form: conversion runs on its own goroutine and the caller can stop waiting. The work itself still cannot be interrupted, so the input bound stays (`DH-LIB-TURNDOWN-R5`). |

No patch, pin or exemption names either package (`patches/` and `pnpm-workspace.yaml`
searched). The `tool-web` README records one limitation that names turndown: inputs it
cannot represent safely are replaced by a fixed marker, and `colspan` is ignored
(`packages/web/tool-web/README.md:278`). That bullet is an inventory unit of area A15;
the behaviour is kept here as `DH-LIB-TURNDOWN-R3` and `-R4`.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-TURNDOWN-R1` | HTML becomes Markdown with `#` headings, fenced code blocks, `-` list markers, GFM tables and `~~strikethrough~~`. The style is fixed, not configurable, because it is what the model reads. | `fetch.ts:25-30` |
| `DH-LIB-TURNDOWN-R2` | Content a reader would not see is removed entirely, by the rules in §2 row 2. No script or style text reaches the model. | `fetch.ts:31-49` |
| `DH-LIB-TURNDOWN-R3` | A table cell's `colspan` and `rowspan` never multiply work or output: converting a page costs time and memory proportional to its size. | `fetch.ts:76-95` |
| `DH-LIB-TURNDOWN-R4` | HTML nested deeper than 512 elements is not converted; the result carries the fixed text `[HTML content omitted: unable to convert safely.]`. The same text replaces any conversion failure, including a panic. Raw markup is never returned as a fallback. | `fetch.ts:120`, `:247-255` |
| `DH-LIB-TURNDOWN-R5` | The source is cut to the configured character limit before parsing, and the result says it was truncated. | `fetch.ts:243-244`, `:328-336` |
| `DH-LIB-TURNDOWN-R6` | Plain-text bodies pass through unchanged. | `fetch.ts:256-257` |
| `DH-LIB-TURNDOWN-R7` | The converter holds no state between calls and may be used by concurrent tool calls. | `fetch.ts:22-23`; Inference for concurrency, since `web_fetch` declares itself safe to overlap (`fetch.ts:500`) |

## 5. Go choice

**Decision.** `github.com/JohannesKaufmann/html-to-markdown/v2` with its `base`,
`commonmark`, `table` and `strikethrough` plugins, configured once, plus three small
rules of harness code registered on it (§6).

**Why.** It has the same architecture as turndown (a parsed tree, replaceable rules,
plugins), exposes exactly the style options upstream sets (`WithHeadingStyle`,
`WithCodeBlockFence`, `WithBulletListMarker` in `plugin/commonmark/commonmark.go:50-73`),
removes tags by registration (`converter/register.go:192-196`), and parses with
`golang.org/x/net/html`, whose depth limit gives `DH-LIB-TURNDOWN-R4` for free.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `github.com/JohannesKaufmann/html-to-markdown/v2` | Rule and plugin model like turndown; GFM tables and strikethrough as plugins; accepts a `context.Context` (`converter/convert.go:20`); escaping of Markdown characters is handled and documented (`ESCAPING.md`) | **Its table plugin expands spans without a cap:** any positive `colspan` and `rowspan` is accepted (`plugin/table/utils.go:47-60`) and one entry is appended per spanned cell (`utils.go:78-97`), so a single hostile cell can demand billions of entries. The harness must remove both attributes before the plugin runs (§6). Its `go.mod` also lists command-line and test dependencies; what a build that imports only the converter links was not measured | v2.5.2, 2026-06-07, MIT; `go list -m`, `LICENSE`, source read, 2026-11-18 |
| Own converter on `golang.org/x/net/html` | No third-party converter; every character the model sees is under harness control; spans are simply never read | Inference: 400 to 600 lines plus the escaping rules, which are the hard part (a `*` or `_` in page text must not become emphasis) | `golang.org/x/net` v0.63.0, 2026-11-17, BSD-3-Clause; `html/parse.go:238-243` read |
| Send raw HTML, or strip tags to text | Nothing to maintain | Several times the tokens, or loss of headings, links, lists and tables that the model uses to navigate a page | n/a |

**Tradeoff.** The exact Markdown differs from turndown's in spacing and escaping, so
upstream's expected strings cannot be reused; the Go tests carry their own golden
files. A converter upgrade can change model-facing text; tool results are stored in
the session log, so only new results are affected.

## 6. Design notes

The converter is a package-level value inside the web tool's package (area A15),
built once at start-up. It creates no process, socket or file, so no fiber owns
anything for it.

Harness rules registered on the converter:

1. **Remove hidden content** (`DH-LIB-TURNDOWN-R2`). The seven tag names are registered
   with the "remove" tag type. One pre-render pass deletes every element that matches
   the attribute and inline-style rules of `fetch.ts:33-44`. Inference: parsing the
   `style` attribute stays as simple as upstream's (split on `;`, compare two
   properties); a stylesheet is never evaluated.
2. **Neutralise spans** (`DH-LIB-TURNDOWN-R3`). A pre-render pass, ordered before the
   table plugin, deletes the `colspan` and `rowspan` attributes from every `td` and
   `th`. This is the Go form of upstream's replacement rules and is mandatory, not
   cosmetic.
3. **Keep multi-line cells.** The table plugin skips a table whose cells contain line
   breaks unless told otherwise (`plugin/table/table.go:43-57`); the harness selects the
   "preserve" behaviour so that such tables still convert, as upstream does by writing
   `<br>` (`fetch.ts:54`).

**Failure.** `ConvertString` returns the parser's depth error as an ordinary error; the
tool maps every error, and a recovered panic, to the fixed omission text
(`DH-LIB-TURNDOWN-R4`). There is nothing to retry or clean up. Cancellation: the tool
call's context is passed to the converter; if the converter does not poll it during a
long walk, the tool returns on cancellation and the abandoned goroutine finishes on its
own, bounded by `DH-LIB-TURNDOWN-R5`.

**Not carried over.** Upstream's per-result memo (`fetch.ts:302-318`) exists because two
callers render the same frozen result. Inference: in Go the tool converts once in its
result-shaping step and stores the text; no memo is needed.

## 7. Risks and open questions

1. **For A15.** The span neutraliser must be covered by a test that fails if the pass
   is removed (`DH-T-TURNDOWN-2`); a future plugin version may add its own cap, but the
   harness does not rely on it.
2. **For A15.** Whether `ConvertString` honours cancellation inside the walk was not
   established from the source; the design does not depend on it.
3. The dependency closure of the module should be measured at first build; if it pulls
   more than the HTML parser and two small helpers, the "own converter" row is the
   alternative.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-TURNDOWN-1` | local | `DH-LIB-TURNDOWN-R1`, `-R6`, `-R7` | Golden files for headings, nested lists, code blocks, links, tables with alignment and strikethrough; a plain-text body is unchanged; the same conversions run from many goroutines under the race detector. |
| `DH-T-TURNDOWN-2` | local | `DH-LIB-TURNDOWN-R3` | A 200-byte table with `colspan="100000000" rowspan="100000000"` converts within a small time and memory budget and its output is shorter than a fixed bound. |
| `DH-T-TURNDOWN-3` | local | `DH-LIB-TURNDOWN-R2` | Each hidden-content rule, alone and nested, including `style="DISPLAY : none !important"` and script text that looks like Markdown; none of the hidden text appears in the output. |
| `DH-T-TURNDOWN-4` | local | `DH-LIB-TURNDOWN-R4`, `-R5` | 513 nested `div` elements, 100,000 unclosed tags and a rule that panics each yield exactly the omission text; a source longer than the limit is cut before parsing and flagged as truncated. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-turndown`, `UP-DEP-@joplin/turndown-plugin-gfm`, `UP-DEBT-found.L12.2`.
