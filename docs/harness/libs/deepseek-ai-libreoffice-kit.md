# @deepseek-ai/libreoffice-kit: Go specification

> **Status: design, not built. The engine is decided: an operator-installed LibreOffice
> run headless as a child process ([DH-ADR-LIBREOFFICEKIT-3](#dh-adr-libreofficekit-3)).**
> Revised on 2026-11-18 after the owner's decisions; see `design.md`.
> Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go module facts were checked on 2026-11-18 with Go 1.27.4
> (`go list -m -json <module>@latest`, `go mod download -json`, the `LICENSE` file and the
> source in the module cache). No module code and no LibreOffice build was run, so every
> statement about what LibreOffice does on a command line is an inference to be proven by
> a real-environment scenario. The kit packages are not present in the upstream checkout
> (no `node_modules`), so their size and internals could not be read.

| | |
|---|---|
| **Library ID** | `DH-LIB-LIBREOFFICEKIT` |
| **Upstream unit** | `UP-DEP-@deepseek-ai/libreoffice-kit` |
| **Upstream version** | 0.2.3, with five engine packages at the same version (`pnpm-lock.yaml:21225-21256`) |
| **Kind** | runtime, native: a Node API plus one precompiled LibreOffice engine per platform, and a WebAssembly build |
| **Used by** | document (`packages/document/office-kit`); bundled by `packages/bundle/web-app` |
| **Go choice** | **own code** that drives an external program: the LibreOffice the operator installed, run headless as a child process. Nothing of LibreOffice is linked, bundled or downloaded, and no cgo is used. The office features work only where LibreOffice is installed ([§5.1](#51-what-does-not-work-without-libreoffice)) |
| **Related areas** | A16 (office documents; owns the seam, queue, tools and scenarios), C6 (document preview), A07 (attachment store for rendered images); [sharp.md](sharp.md), [exceljs.md](exceljs.md), [pdfjs-dist.md](pdfjs-dist.md) |

## 1. What it is and why upstream needs it

An agent that edits Word, Excel and PowerPoint files needs three things it cannot do by
reading bytes: **see** a page the way a person would (to check layout), **convert** a
document to another format (most often PDF), and **recalculate** a workbook so that the
stored formula results match the formulas. A person previewing a `.docx` in the sidebar
needs the same conversion to PDF.

All three need a real office suite. Upstream uses LibreOffice through
**LibreOfficeKit** (LOK), the C interface LibreOffice offers for loading its engine as a
library inside another program. `@deepseek-ai/libreoffice-kit` is a first-party Node
wrapper, kept in its own repository with its own release cycle
(`.agents/notes/implemented/architecture/2026-09-14-independent-libreoffice-kit.md:13-15`).
It ships a precompiled engine for macOS and Windows (arm64 and x64) and a WebAssembly
build that every other platform, including Linux, falls back to
(`packages/document/office-kit/README.md:12`, `pnpm-lock.yaml:21225-21251`).

The harness package `office-kit` is the adapter: it owns the queue, limits, cache and
output store, and the kit owns the engine
(`docs/subsystems/office-kit.md:5`, `:43`). This specification covers only what replaces
the kit. The queue, cache, output store and tools belong to area A16.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/document/office-kit/src/index.ts:346-356` | Locate the kit from the provider's own installation and import it at first use; absent or broken means `unavailable`, never a start-up failure | dynamic `import()`, `createConverter` |
| `packages/document/office-kit/src/index.ts:378-385` | One long-lived converter per concurrency slot (default 2), created lazily and reused | `createConverter(options)` |
| `packages/document/office-kit/src/index.ts:151-157` | Pass limits and fonts to the engine: time, input bytes, output bytes, PDF image resolution, ZIP entry count and uncompressed size, font directories, fallback groups, font file limits | `ConverterOptions` |
| `packages/document/office-kit/src/index.ts:416-420` | Office file to PDF for previews; returns the fonts the document asked for and the engine did not have | `converter.render({inputPath, outputPath})` |
| `packages/document/office-kit/src/index.ts:421-428` | Convert to `pdf`, `docx`, `odt`, `txt`, `xlsx`, `ods`, `csv`, `pptx` or `odp`; CSV takes a worksheet name; returns the PDF page count | `converter.convert` |
| `packages/document/office-kit/src/index.ts:425` | Recalculate every formula of an `xls`, `xlsx` or `ods` workbook and save as `xlsx` or `ods` | `converter.recalculate` |
| `packages/document/office-kit/src/index.ts:444-481` | Render chosen pages, slides or a worksheet range to PNG files, bounded by image count, pixels per image and side length; the adapter then bounds total bytes and refuses a path outside its scratch directory | `converter.renderImages` |
| `packages/document/office-kit/src/index.ts:387-406` | Every operation works on a private scratch directory that is removed before the result returns; an abort signal cancels it | file paths in, file paths out |
| `packages/document/office-kit/src/locate.ts:55-93` | Resolve each engine package next to the kit, and map a path inside the desktop archive to its unpacked copy | `resolveEnginePackage` |
| `python/sdk-runtime/README.md:59` | The Python SDK ships the kit as a downloaded "Office sidecar" with its own Node runtime | kit command line (`lib/cli.js`) |

Which inputs each operation accepts is in `packages/document/tool-office/src/formats.ts:15-33`:
Word (`doc`, `docx`, `odt`), spreadsheet (`xls`, `xlsx`, `ods`), presentation (`ppt`,
`pptx`, `odp`), and PDF for rendering only.

**How the engine is found, loaded, isolated and stopped upstream.** Found: by Node
module resolution anchored at the provider's own copy of the kit; no search of the
machine and no download (`packages/document/office-kit/README.md:115-117`). Loaded:
inside the host process; the native engine also starts helper processes, worker threads
and font caches (`.agents/notes/implemented/architecture/2026-10-31-native-office-tool.md:9`,
`:13`). Isolated: it is not; the tool README lists "native engine in the Host process"
as a limitation, with the untrusted document parsed under the host user's permissions
outside the shell sandbox (`packages/document/tool-office/README.md:111`). Stopped: the
provider's disposal reports `unavailable` to waiting readers and joins conversions and
converter teardown (`packages/document/office-kit/README.md:85`).

## 3. Tech debt upstream carries

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-pin.minimumReleaseAgeExclude.@deepseek-ai/libreoffice-kit@0.2.3` and the five engine entries (`-darwin-arm64`, `-darwin-x64`, `-win32-arm64`, `-win32-x64`, `-wasm`) | pnpm is configured to refuse packages younger than a set age, as a defence against a hijacked release. The kit and its engines are exempted because they are qualified and released together by the kit repository. (The inventory's `statedReason` also carries the comment of the neighbouring `@botiverse/oar` entry; the kit's own comment is line 60.) | `pnpm-workspace.yaml:60-66` | **Resolved** by [DH-ADR-LIBREOFFICEKIT-1](#dh-adr-libreofficekit-1): the engine is not a build dependency of the Go harness. No package is installed, so there is no age rule to exempt. What vouches for the engine becomes an operator question: it is the LibreOffice the operator installed, and the harness reports its path and version. |
| Engine inside the host (not an inventory unit of this agent; owned by A16) | An untrusted document is parsed by native code in the host process | `packages/document/tool-office/README.md:111` | Removed by [DH-ADR-LIBREOFFICEKIT-1](#dh-adr-libreofficekit-1): the engine is always a child process. |
| Packaging rules per deployment | Desktop must unpack native files from its archive, sign them on macOS and select one engine per target; the Python SDK ships a second Node runtime only for the kit | `packages/document/office-kit/src/locate.ts:86-93`, `python/sdk-runtime/README.md:59` | Not applicable: nothing is packed into the binary. |
| MPL-2.0 redistribution duty | Shipping the engine obliges upstream to ship licence texts and corresponding source | `THIRD_PARTY_NOTICES.md:205-207` | Not applicable: the harness does not redistribute LibreOffice ([DH-ADR-LIBREOFFICEKIT-3](#dh-adr-libreofficekit-3)). |

Search performed: `patches/` has no kit patch; `pnpm-workspace.yaml` has only the six
exemptions; the two document READMEs were read in full.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-LIBREOFFICEKIT-R1` | Convert `doc`, `docx`, `odt`, `xls`, `xlsx`, `ods`, `ppt`, `pptx`, `odp` to one complete PDF. | `index.ts:416-420`, `formats.ts:15-20` |
| `DH-LIB-LIBREOFFICEKIT-R2` | Convert within a family: Word to `pdf`, `docx`, `odt`, `txt`; spreadsheet to `pdf`, `xlsx`, `ods`, `csv` (one named worksheet); presentation to `pdf`, `pptx`, `odp`. Report the PDF page count. | `formats.ts:23-27`, `index.ts:421-428` |
| `DH-LIB-LIBREOFFICEKIT-R3` | Recalculate every formula of a workbook and save formulas with refreshed results as `xlsx` or `ods`. No validation of formulas or data is implied. | `docs/subsystems/office-kit.md:93-97` |
| `DH-LIB-LIBREOFFICEKIT-R4` | Render chosen pages or slides, or one worksheet range, as PNG at a requested resolution. A selection that would exceed the image count, pixels per image, side length or total bytes fails before pixels are allocated; nothing is truncated. PDF input is accepted. | `index.ts:444-481`, `packages/document/office-kit/README.md:39` |
| `DH-LIB-LIBREOFFICEKIT-R5` | Report the font families a document requests that the machine cannot supply. | `docs/subsystems/office-kit.md:25` |
| `DH-LIB-LIBREOFFICEKIT-R6` | The engine never runs inside the host process. It is a child process owned by the provider's fiber, confined by the harness sandbox to read its scratch and profile directories and write only there, with no network. | Inference from `tool-office/README.md:111`; [DH-O-7](../design.md#dh-o-7) |
| `DH-LIB-LIBREOFFICEKIT-R7` | Every operation is bounded: input bytes, ZIP entry count and declared uncompressed size are checked before the engine sees the file; a deadline kills the engine's whole process tree; output bytes are checked while reading. | `index.ts:98-103`, `:151-157` |
| `DH-LIB-LIBREOFFICEKIT-R8` | Cancellation of the last waiting reader stops the engine process promptly; the scratch directory is removed before the call returns in every outcome. | `index.ts:387-406`, `office-kit/README.md:85` |
| `DH-LIB-LIBREOFFICEKIT-R9` | An absent or unusable engine never fails start-up. The provider registers, and each call fails with the classified reason `unavailable` and an instruction that names what to install. The model is told not to substitute another converter. | `office-kit/README.md:43`, `tool-office/README.md:32` |
| `DH-LIB-LIBREOFFICEKIT-R10` | Failures are classified with the upstream reasons: `input-too-large`, `output-too-large`, `invalid-document`, `unsupported-format`, `invalid-request`, `invalid-output`, `timeout`, `unavailable`, `failed`. Engine diagnostics go to the log, not to the browser. | `index.ts:394-404`, `docs/subsystems/office-kit.md:35` |
| `DH-LIB-LIBREOFFICEKIT-R11` | The engine never modifies the source file, the workspace or the user's own LibreOffice profile, and two concurrent conversions never share engine state. | `tool-office/README.md:12`; Inference for the profile |
| `DH-LIB-LIBREOFFICEKIT-R12` | The engine's identity (which provider, path, version) is part of the provider generation, so a cached PDF made by one engine is never reused after the engine changes. | `office-kit/README.md:63`, `docs/subsystems/office-kit.md:27` |

## 5. Go choice

**Decision.** The harness drives the LibreOffice that the operator installed, as a
headless child process, with its own code. The repository owner chose this on 2026-11-18
("an operator-installed LibreOffice run headless as a child process; no cgo, nothing
bundled; the office tools work only where LibreOffice is installed"; recorded for the
whole design as [DH-ADR-20](../design.md#dh-adr-20)). Three decision records carry it
here: where the engine runs (`-1`), which engine it is (`-3`) and what the
harness enforces itself (`-2`).

<a id="dh-adr-libreofficekit-1"></a>
### DH-ADR-LIBREOFFICEKIT-1 The office engine is a located child process, never a linked library

- **Choice.** The engine runs as a child process started through the
  harness spawn helper, is located on the machine (configuration, then a fixed list of
  standard install paths, then `PATH`), and is never compiled into, loaded into or
  downloaded by the `dsh` binary. Absence means `unavailable`.
- **Why.** LibreOffice parses hostile documents with a large native code base. In the
  host process a crash ends every session and a memory-safety bug has the host's full
  authority; upstream records this as a limitation. A process boundary gives a kill
  switch for the deadline, a place to apply the sandbox, and keeps the release pure Go
  (`CGO_ENABLED=0`). It also matches foundation decision 96 in
  [../upstream/decisions-1.md](../upstream/decisions-1.md).
- **Tradeoff.** A process start per conversion, or a helper to keep warm. Upstream
  refuses to search the machine for LibreOffice; this decision reverses that, so two
  machines may give different fidelity. The engine version is therefore reported.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10),
  [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-LIBREOFFICEKIT-1`,
  `DH-T-LIBREOFFICEKIT-3`, and A16's sandbox scenario.

<a id="dh-adr-libreofficekit-2"></a>
### DH-ADR-LIBREOFFICEKIT-2 Limits and font reporting are harness code, independent of the engine

- **Choice.** The harness, not the engine, enforces the archive limits (entry count and
  declared uncompressed bytes, with `archive/zip`), the page-image limits (computed from
  PDF page sizes and the requested resolution before rasterising) and reports missing
  fonts (font names read from the OOXML parts with `encoding/xml`, compared with the
  family names of the font files found in the configured font directories, read with
  `golang.org/x/image/font/sfnt`).
- **Why.** Upstream gets these from the kit's own code (its dependencies are a ZIP
  reader, an XML parser and a font parser: `pnpm-lock.yaml:29128-29132`). A stock
  LibreOffice offers none of them on its command line. Owning them keeps
  `DH-LIB-LIBREOFFICEKIT-R4`, `-R5` and `-R7` true whatever LibreOffice version the
  operator installed.
- **Tradeoff.** The font answer is a prediction from file contents, not the engine's own
  substitution record; it can differ when LibreOffice substitutes through its
  replacement table. Binary `doc`, `xls` and `ppt` report an empty list, as upstream does
  (`office-kit/README.md:12`).
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-10](../design.md#dh-o-10).
  **Verified by.** `DH-T-LIBREOFFICEKIT-2`, `DH-T-LIBREOFFICEKIT-4`.

<a id="dh-adr-libreofficekit-3"></a>
### DH-ADR-LIBREOFFICEKIT-3 The engine is the LibreOffice the operator installed, run headless, one process per job

- **Choice.** The office engine is the stock `soffice` program of a LibreOffice that
  the operator installed from the vendor or from the operating system's packages. The
  harness starts it headless, once per job, with a private profile directory per
  concurrency slot, and reads the files it writes. The harness ships no LibreOffice, no
  engine package and no helper binary, and uses no cgo for this. Decided by the
  repository owner on 2026-11-18.
- **Why.** It is the only option with no cgo, no second language runtime and no
  redistribution duty. LibreOffice is one package command away on Linux, where upstream
  has no native engine at all (`packages/document/office-kit/README.md:12`), and the
  process boundary gives the isolation upstream lacks
  ([DH-ADR-LIBREOFFICEKIT-1](#dh-adr-libreofficekit-1)). With the desktop application
  removed from the design (owner decision of the same day), there is no packaged product
  that could carry an engine in any case.
- **Tradeoff.** The office features are absent on a machine without LibreOffice
  ([§5.1](#51-what-does-not-work-without-libreoffice)). No warm converter: every job pays
  a process start (Inference: about one to three seconds; to be measured), so the
  content-addressed cache matters more. Fidelity varies with the installed version, so
  the version is reported and is part of the cache generation
  (`DH-LIB-LIBREOFFICEKIT-R12`). A worksheet *range* has no command-line switch (§6).
  Rendering goes through PDF, so a rendered "page" is a PDF page, which is what
  upstream's preview shows anyway.
- **What the operator must do.** Install LibreOffice, or set its path in configuration
  when it is not in a standard place. Nothing else: no "prepare office" command, no
  download by the harness.
- **Serves.** [DH-O-10](../design.md#dh-o-10) (the engine is behind a seam),
  [DH-O-14](../design.md#dh-o-14) (one pure-Go binary), [DH-O-7](../design.md#dh-o-7)
  (the engine is a confined child process).
- **Verified by.** `DH-T-LIBREOFFICEKIT-1`, `-3` and `-6` with a fake engine executable;
  the real-environment scenarios of section 8, owned by A16, with a real LibreOffice.

**Page images, and the words "nothing bundled".** Recommended default, not confirmed by
the owner ([../design.md](../design.md) §9.2): **no PDF rasteriser is embedded in the
binary.** Page images are made by LibreOffice itself, one export per page, behind the
`office.Rasterizer` interface of area A16
([../areas/document-browser-computer.md](../areas/document-browser-computer.md),
`DH-D-MEDIA-3`); a PDF given as input therefore also needs LibreOffice. The first text
of this document rasterised the chosen PDF pages in the host with
`github.com/klippa-app/go-pdfium` in its WebAssembly mode (pure Go, no cgo). That module
embeds a 6.0 MB PDFium WebAssembly file (`internal/pdfium_wasm/pdfium.wasm`, measured in
the module cache) and needs the wazero runtime: a third-party engine inside the binary,
one more parser of hostile input, and PDFium's licence to add to the notices. It is
kept in the options below as the alternative the owner may choose; the interface does
not change if it is.

### 5.1 What does not work without LibreOffice

Stated plainly, for operators. On a machine where no usable LibreOffice is found:

| Feature | Without LibreOffice |
|---|---|
| `office_convert` (to PDF or within a format family) | Fails with `unavailable` and a message that names what to install |
| `office_recalculate` (refresh workbook formula results) | Fails with `unavailable` |
| `office_render` for Word, spreadsheet and presentation files | Fails with `unavailable` |
| Sidebar preview of `doc`, `docx`, `odt`, `ppt`, `pptx`, `odp` (shown as PDF) | Not available; the preview shows the same install guidance |
| Preview of legacy `xls` workbooks, which are converted to `xlsx` first ([xlsx.md](xlsx.md)) | Not available |
| Missing-font report for a document | Still works: it is harness code that reads the file ([DH-ADR-LIBREOFFICEKIT-2](#dh-adr-libreofficekit-2)) |
| `office_render` of a PDF, and PDF preview | Still works: no office engine is involved (unless the page-image fallback above is taken) |
| Preview of `xlsx` workbooks | Still works: read by the host ([exceljs.md](exceljs.md)) |

The host starts normally in every case, the provider registers, and the model is told
not to substitute another converter (`DH-LIB-LIBREOFFICEKIT-R9`). The harness never
installs, downloads or upgrades LibreOffice. A LibreOffice older than the supported
minimum counts as unusable; the minimum is not yet established (section 7).

### 5.2 Options considered and not taken

| Option | Needs cgo | Download / install | Licence exposure | What the user must do | Why it was not taken |
|---|---|---|---|---|---|
| **B. LibreOfficeKit through cgo, in a separate helper binary** (`dsh-office`), loading `libsofficeapp`/`libmergedlo` from the installed LibreOffice | Yes, in the helper only; the host stays pure Go | A second binary per platform, built with a C toolchain | Helper is own code; LibreOffice not redistributed | Install LibreOffice, as in the chosen option | The owner ruled out cgo for this. It is closest to upstream: warm engine, tile painting for any page, range or slide, `.uno:` commands for recalculation. Cost: a cgo build matrix, and LOK's API is marked unstable. The only Go binding found, `github.com/dveselov/go-libreofficekit` (MIT, untagged, last commit 2024-11-27), links with `-ldl`, so it is POSIX only, and exposes no command dispatch; the helper would carry its own binding |
| **C. Keep upstream's kit as a sidecar** (a Node runtime plus the kit's command line), as upstream's Python SDK does | No | A Node runtime and one engine package per platform, downloaded on request | MPL-2.0 redistribution duties return (`THIRD_PARTY_NOTICES.md:205-207`) | Run one "prepare office" command | It bundles or downloads an engine, which the owner ruled out. It would give exact parity, including Linux through WebAssembly, at the price of a second language runtime, a dependency on a private package's command line, and no native engine for Linux |
| **D. Run the kit's WebAssembly engine in a pure-Go runtime** (`github.com/tetratelabs/wazero`) | No | The engine file and its data file, embedded or downloaded | MPL-2.0 as in C | Nothing | **Rejected as infeasible.** The build targets Node 22.19 or later (`pnpm-lock.yaml:21237-21239`) and ships a separate data asset (`apps/desktop/tests/runtime-file-policy.spec.ts:32`). Inference: it is an Emscripten build that needs Emscripten's JavaScript runtime, a virtual filesystem and threads; wazero has an `imports/emscripten` package but its threads feature is experimental (`experimental/features.go:15`), and nobody has qualified LibreOffice on it |
| **E. Pure-Go document libraries for a subset** (`github.com/xuri/excelize/v2` for workbooks) | No | None | BSD-3-Clause | Nothing | Cannot lay out a page, so no PDF and no images; formula coverage is partial. Not a replacement. Useful only as a degraded path: `xlsx` to `csv`, and reading sheet names |

The engine stays behind the seam of section 6 ([DH-O-10](../design.md#dh-o-10)), so a
second provider could be added later without changing a tool schema. None is planned.

| Module named in this section | Version | Licence | Checked |
|---|---|---|---|
| `github.com/klippa-app/go-pdfium` | v1.21.5 (2026-11-17) | MIT | `go list -m`, `LICENSE`, `webassembly/` package and the embedded `pdfium.wasm` in the module cache, 2026-11-18. `NATIVE_VS_WEBASSEMBLY.md` in the module reports the WebAssembly backend at about twice the native time. Its `go.mod` also requires `hashicorp/go-plugin` for its multi-process mode; whether the WebAssembly package links it was not checked |
| `github.com/tetratelabs/wazero` | v1.12.0 (2026-05-28) | Apache-2.0 | `go list -m`, `LICENSE`, `config.go` (`WithMemoryLimitPages`) |
| `golang.org/x/image` (`font/sfnt`) | v0.48.0 (2026-11-09) | BSD-3-Clause | declared in [sharp.md](sharp.md) |
| `github.com/dveselov/go-libreofficekit` | v0.0.0-20241127125619-96cbcdcf00f4 | MIT | `go list -m`, `LICENSE`, `libreofficekit.go:4-6` (cgo, `-ldl`) |
| `github.com/xuri/excelize/v2` | v2.11.1 (2026-11-02) | BSD-3-Clause | declared in [exceljs.md](exceljs.md) |

`x/image` is used by the chosen design. `go-pdfium` and `wazero` belong to the embedded
rasteriser, which is not built by default; `go-libreofficekit` belongs to option B and
`excelize` to option E and to [exceljs.md](exceljs.md).

**Upstream decisions judged** ([DH-ADR-2](../design.md#dh-adr-2)).

| Upstream decision | Verdict | Why |
|---|---|---|
| The engine is built and released outside the harness (`2026-09-14-independent-libreoffice-kit.md:13-15`) | **Keep**, more strongly | The Go harness does not even consume a packaged engine. |
| The engine runs inside the host through the provider (`2026-10-31-native-office-tool.md:13`) | **Change** | Upstream moved the engine into the host because its shell sandbox restricted the engine's helper processes. The Go design starts the engine itself, under a sandbox profile written for it, so the original reason does not force an in-process engine. |
| No search for a system LibreOffice and no download (`office-kit/README.md:115`) | **Change** | Upstream could ship a qualified engine in its desktop package; a single Go binary cannot. Reproducibility is replaced by reporting the engine version. |
| An absent engine degrades to `unavailable` instead of failing start-up (`office-kit/README.md:43`) | **Keep** | It is the rule for every optional external tool. |
| Missing native engine fails packaging, never a silent switch to WebAssembly (`office-kit/README.md:41`) | **Drop** | There is no packaging step and no second engine to switch to. |
| Scratch directory per job, outputs in a private store, never in the workspace (`office-kit/README.md:39`, `:85`) | **Keep** | Independent of the engine; owned by A16. |

## 6. Design notes

**Seam.** Area A16 owns a contract package with one interface and a registry of
providers. Inference: the shape below is the minimum the four operations need.

```go
type Engine interface {
    Info() EngineInfo                                   // provider id, path, version: part of the generation
    ToPDF(ctx context.Context, in, out string) error
    Convert(ctx context.Context, in, out string, f Format, sheet string) error
    Recalculate(ctx context.Context, in, out string, f Format) error
}
```

Page images are not an engine method: the provider converts to PDF and a
separate `Rasterizer` turns chosen PDF pages into PNG. Its one implementation runs a
LibreOffice export per page.

**How LibreOffice is driven (all Inference until a scenario proves them).**

- *Run.* `soffice --headless --norestore --nolockcheck -env:UserInstallation=file:///<slot profile> --convert-to <format>[:<filter>] --outdir <scratch> <scratch>/source.<ext>`.
  One profile directory per concurrency slot, under the harness home, created by the
  provider. The user's own profile is never opened, and a LibreOffice window the user
  has open is not disturbed (`DH-LIB-LIBREOFFICEKIT-R11`).
- *Recalculation.* The slot profile is written with the Calc load settings "recalculate
  on load: always" for both Excel and OpenDocument files, so a plain load and save
  refreshes every result. If a version ignores the setting, recalculation reports
  `unsupported-format` instead of returning stale results.
- *CSV of one sheet.* The CSV export filter takes a sheet selector in its option string;
  the sheet's position is read from the workbook first.
- *Worksheet range.* The provider makes a scratch copy of the workbook, sets the print
  area to the requested range and "fit to one page" on that sheet (with excelize for
  `xlsx`; other workbook formats are converted to `xlsx` first), exports that sheet to
  PDF and rasterises it. If this cannot be made reliable, a range request fails with
  `invalid-request` and the reason, and the model renders whole pages instead. This is
  the weakest point of the chosen design.

**Ownership, failure and cleanup.**

| Resource | Class | Owner | Released |
|---|---|---|---|
| Engine child process | created | the provider's fiber, through the job that started it | on completion; on deadline or cancellation the whole process tree is stopped as in [../design.md](../design.md) §4.4 |
| Scratch directory per job | created | the job | removed before the result returns, on every path |
| Slot profile directory | created | the provider's fiber | on unload; deleted and rebuilt after a killed run, because a killed LibreOffice can leave a locked profile |
| Output store files | created | the provider; a session only has access | by retention, by size pressure, when the owning root session is disposed, on unload (kept from `office-kit/README.md:39`) |
| The installed LibreOffice | shared | the operator | never modified; the harness has access, not ownership |

- **Engine fails or crashes.** The job fails with `failed`; nothing is retried
  automatically, because a document that crashes the engine will crash it again.
- **Deadline.** The process tree is killed; the result is `timeout`; the slot profile is
  rebuilt. **Uncertain:** nothing outside the scratch directory, since the sandbox
  allows no other write.
- **Host dies mid-job.** The child dies with the host (job object on Windows, parent
  death signal elsewhere, per the spawn helper). Scratch and profile directories with
  the provider's own name pattern are swept at the next start; other entries are left.
- **Engine changes while running** (upgrade, path edit). The provider generation
  changes; cached PDFs are dropped (`DH-LIB-LIBREOFFICEKIT-R12`).

## 7. Risks and open questions

1. **The embedded PDF rasteriser is not built: a recommended default, not confirmed by
   the owner.** The engine decision is made
   ([DH-ADR-LIBREOFFICEKIT-3](#dh-adr-libreofficekit-3)). Under it, page images come
   from LibreOffice's own per-page export: one process run per page, and a PDF input
   needs LibreOffice too. If the owner prefers page images of a PDF without LibreOffice,
   go-pdfium is added behind the same interface, at the cost of a 6.0 MB PDFium
   WebAssembly file in the binary and one more parser of hostile input.
2. **For A16.** Minimum supported LibreOffice version, and whether the recalculation and
   CSV sheet settings hold on it, must be established by scenarios on Windows, macOS and
   Linux before the Office tools are enabled by default.
3. **For A16.** Start-up latency per job is unmeasured. If it is unacceptable for
   previews, the remedies inside the decision are the cache and a larger slot count;
   a warm engine would need a different engine option, which the owner did not take.
4. **For A16 and the security area.** The sandbox profile for the engine child (read
   scratch, profile and the LibreOffice install; write scratch and profile; no network)
   needs the same backend support as a shell command. On Windows enforcement is
   `partial`, as everywhere else in the design.
5. **Cost of one process per page.** Rendering many pages starts LibreOffice once per
   page; the latency is unmeasured and belongs to area A16's scenarios. Should
   go-pdfium be added later, its memory ceiling, cancellation behaviour and dependency
   closure must be checked when the module is first built.
6. **Font prediction** can disagree with the engine (see DH-ADR-LIBREOFFICEKIT-2).

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-LIBREOFFICEKIT-1` | local | `DH-LIB-LIBREOFFICEKIT-R6`, `-R8`, `-R10` | A fake engine executable (a test binary that sleeps, exits non-zero, writes an oversized file or writes outside its directory) is driven through the provider: each case yields the expected reason, the scratch directory is gone, and no process survives a cancel or a deadline. |
| `DH-T-LIBREOFFICEKIT-2` | local | `DH-LIB-LIBREOFFICEKIT-R7` | A ZIP with too many entries, and one that declares too many uncompressed bytes, are refused before the fake engine is started. |
| `DH-T-LIBREOFFICEKIT-3` | local | `DH-LIB-LIBREOFFICEKIT-R9`, `-R12` | With no engine configured the provider loads, every call returns `unavailable` with the install hint, and pointing the configuration at an engine makes the next call succeed with a new generation. |
| `DH-T-LIBREOFFICEKIT-4` | local | `DH-LIB-LIBREOFFICEKIT-R5` | Golden OOXML files that name an installed and a missing font family give exactly the missing family; a binary `doc` gives an empty list. |
| `DH-T-LIBREOFFICEKIT-5` | local | `DH-LIB-LIBREOFFICEKIT-R4` | With a fake engine executable as the per-page exporter and a committed multi-page PDF: page selection and order, one engine run per chosen page, and refusal before any engine run when count, pixels, side or total bytes computed from the page sizes exceed the limit. PNG dimensions for a resolution are checked against a real LibreOffice in area A16's scenario. |
| `DH-T-LIBREOFFICEKIT-6` | local | `DH-LIB-LIBREOFFICEKIT-R11` | Two concurrent jobs use different profile and scratch directories and the source file's bytes and modification time are unchanged. |

Requirements `-R1`, `-R2` and `-R3` are behaviour of a real LibreOffice and cannot be
proven with a fake. They need real-environment scenarios, owned by area A16: Given a
machine with LibreOffice installed and the committed `docx`, `xlsx` and `pptx`
fixtures; When each tool operation runs through the built binary; Then the PDF opens
and has the expected page count, the converted file opens in the target format, and a
workbook whose stored result was deliberately made stale comes back with the correct
value; Cleanup removes the output store and the slot profiles. A skipped scenario
leaves the requirement open. Local tests and real-environment scenarios are separate
kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-@deepseek-ai/libreoffice-kit`;
`UP-DEBT-pin.minimumReleaseAgeExclude.@deepseek-ai/libreoffice-kit@0.2.3`,
`…-darwin-arm64@0.2.3`, `…-darwin-x64@0.2.3`, `…-win32-arm64@0.2.3`,
`…-win32-x64@0.2.3`, `…-wasm@0.2.3`.
