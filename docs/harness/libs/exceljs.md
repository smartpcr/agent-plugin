# exceljs: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go module facts were checked on 2026-11-18 with Go 1.27.4
> (`go list -m -json <module>@latest`, `go mod download -json`, the `LICENSE` file and the
> source in the module cache). No module code was compiled or run.

| | |
|---|---|
| **Library ID** | `DH-LIB-EXCELJS` |
| **Upstream units** | `UP-DEP-exceljs`, `UP-DEBT-patch.exceljs@4.4.0` |
| **Upstream version** | 4.4.0, patched |
| **Kind** | bundled into the client (a lazily loaded chunk of the React application) |
| **Used by** | client (`packages/client/ui-sidebar-documentpreview`) |
| **Go choice** | named Go module `github.com/xuri/excelize/v2`, used in the host to read workbooks; the browser gets HTML |
| **Related areas** | C6 (document preview; designs the viewer), A16 (Office engine, used for legacy `.xls`); [fortune-sheet.md](fortune-sheet.md), [xlsx.md](xlsx.md), [papaparse.md](papaparse.md), [fast-xml-parser.md](fast-xml-parser.md) |

This is the main specification for the **spreadsheet preview**. The four linked
specifications are short and refer back to it.

## 1. What it is and why upstream needs it

The right sidebar previews files the agent is working on. For spreadsheets it shows a
read-only grid with worksheet tabs, so that a person can check what the agent produced
without opening Excel (`packages/client/ui-sidebar-documentpreview/README.md:75`).

Upstream does all of this in the browser. The host only sends the file's bytes. A
parser running in a **Web Worker** (a background browser thread that can be killed)
turns them into cells, and a grid widget draws them. Four parsers feed one grid:

| Format | Parser | Specification |
|---|---|---|
| `.xlsx` | ExcelJS | this file |
| `.xls` (the pre-2007 binary format) | SheetJS | [xlsx.md](xlsx.md) |
| `.csv`, `.tsv` | PapaParse | [papaparse.md](papaparse.md) |
| small XML parts of an `.xlsx` | fast-xml-parser | [fast-xml-parser.md](fast-xml-parser.md) |
| the grid itself | FortuneSheet | [fortune-sheet.md](fortune-sheet.md) |

An `.xlsx` file is a ZIP archive of XML "parts" linked by "relationship" files. ExcelJS
unpacks it and builds a workbook object.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/client/ui-sidebar-documentpreview/src/client/excel/xlsx.ts:23-36` | Load a workbook from bytes, after a copy of the archive had its drawing parts removed, and ignore drawing references | `new ExcelJS.Workbook()`, `workbook.xlsx.load(buffer, { ignoreNodes: ['drawing'] })` |
| `packages/client/ui-sidebar-documentpreview/src/client/excel/xlsx.ts:36-60` | Walk worksheets: visibility, merged ranges, cells, and a running count of the rectangular area against the cell limit | `workbook.worksheets`, `worksheet.model.merges`, `worksheet.getCell` |
| `packages/client/ui-sidebar-documentpreview/src/client/excel/xlsx.ts:9-14`, `:2` | Map fonts, colours (including theme colours), fills and thirteen border styles to the grid's cell style | types `Font`, `Color`, `BorderStyle`, `Cell` |
| `packages/client/ui-sidebar-documentpreview/README.md:75` | What is kept: fonts, solid fills, borders, number formats, rich text, merged cells, row and column sizes, hidden rows, columns and sheets, frozen headings. Formulas are shown with their **saved** results and never recalculated | (behaviour) |
| `packages/client/ui-sidebar-documentpreview/README.md:77` | Charts, images, shapes and conditional formatting are not drawn; a notice lists what was detected | (behaviour) |
| `packages/client/ui-sidebar-documentpreview/README.md:83-89` | Limits: 16 MiB of source, 250,000 cells of combined rectangular area, 15 seconds, after which the Worker is terminated | Worker lifetime, not the library |

## 3. Tech debt upstream carries

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-patch.exceljs@4.4.0` | A 35 KB patch to both the Node sources and the browser bundle. It fixes six things ExcelJS gets wrong on files written by tools other than Excel: (1) parts are found by fixed path (`xl/workbook.xml`, `xl/worksheets/sheet1.xml`) instead of through relationships, so the patch adds a relationship resolver with relative and absolute targets, case-insensitive part names, and hard failures for missing, ambiguous, external or escaping targets (`patches/exceljs@4.4.0.patch:537-655`); (2) XML names are matched by prefix text instead of namespace, so the patch makes the parser namespace-aware and maps eight namespace addresses, including the Strict OOXML ones, to the names the code expects (`:402-441`); (3) XML parts in UTF-16 were misread (`:541-549`); (4) CDATA text was dropped (`:438`); (5) a plain-text comment and the order of comment relationships were mishandled (`:468-492`, `:497-523`); (6) a worksheet with a missing relationship was silently skipped instead of failing (`:447-460`). It also points the browser entry at the patched, unminified bundle (`:798-803`) | `patches/exceljs@4.4.0.patch`, `pnpm-workspace.yaml:120`, `packages/client/ui-sidebar-documentpreview/README.md:93` | **Resolved** by `DH-LIB-EXCELJS` and proven by `DH-T-EXCELJS-2`. The causes are properties of ExcelJS, not of the format. Go's `encoding/xml` matches elements by namespace address, so prefixes do not matter. `excelize` finds the workbook through the package relationship (`workbook.go:196-208`), resolves worksheet targets that are relative or absolute (`sheet.go:113-120`), rewrites Strict namespaces (`lib.go:590-602`) and decodes other XML encodings through a character-set reader (`excelize.go:166`). What was **not** established from source: case-insensitive names in every part, failure on ambiguous names, refusal of external targets, plain-text comments. Those are requirement `DH-LIB-EXCELJS-R4`; a case the module fails is fixed in the module or makes the preview fail closed. The Go design carries no patched copy of a dependency. |
| Double entity decoding | ExcelJS decodes entities in cached string results twice, so a saved `&lt;` shows as `<` | `packages/client/ui-sidebar-documentpreview/README.md:89` | Not carried: a different parser. Covered by `DH-T-EXCELJS-3`. |
| Pre-pass to strip drawings | The archive is unpacked, drawing parts removed and re-zipped before ExcelJS sees it, because ExcelJS would parse them | `packages/client/ui-sidebar-documentpreview/src/client/excel/xlsx-archive.ts:28-41` | Not needed: the Go reader opens only the parts it asks for. Detection of unsupported content stays (`DH-LIB-EXCELJS-R3`). |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-EXCELJS-R1` | An `.xlsx` workbook is shown read-only with worksheet tabs, formatted values, fonts, solid fills, borders, merged cells, row and column sizes, hidden rows, columns and sheets, and frozen headings. | `README.md:75` |
| `DH-LIB-EXCELJS-R2` | Formulas are never evaluated. A cell shows its saved result; a formula with no saved result shows blank and the view says results may be incomplete. The formula text can be read. | `README.md:75` |
| `DH-LIB-EXCELJS-R3` | Charts, images, shapes, pivot tables and conditional formatting are not rendered, and a notice names the kinds that were detected. Hyperlinks are shown as text and never fetched. | `README.md:77`, `:89` |
| `DH-LIB-EXCELJS-R4` | Parts are found through relationships and names through namespaces: a workbook written by another tool with different part paths, prefixes, Strict OOXML namespaces or UTF-16 XML opens the same way. A package with a missing, ambiguous or external part fails the preview with `invalid`; it is never partly shown. | `README.md:93`, the patch |
| `DH-LIB-EXCELJS-R5` | **Limits before allocation.** Source bytes, total uncompressed bytes, combined cell area and wall-clock time are bounded; exceeding one gives `tooLarge` or a timeout message, never an out-of-memory host. | `README.md:83-87` |
| `DH-LIB-EXCELJS-R6` | Every string from the file (cell text, formulas, sheet names, font names, number formats) is treated as data: escaped for the HTML context it is written into, and never placed in a style sheet or attribute unvalidated. | `.agents/notes/implemented/feature/2026-09-16-browser-excel-preview.md:25`; Inference for style values |
| `DH-LIB-EXCELJS-R7` | The source file is never modified, and workbook bytes and parsed cells are transient: not written to the session log. | `README.md:59`, `:77` |
| `DH-LIB-EXCELJS-R8` | A large sheet reaches the browser in bounded pieces, so the page never holds all 250,000 cells at once. | Inference from DH-ADR-3; the grid upstream virtualises drawing on a canvas |

## 5. Go choice

**Decision.** The host reads the workbook with `github.com/xuri/excelize/v2` and
renders HTML table fragments. No spreadsheet parser runs in the browser.

<a id="dh-adr-exceljs-1"></a>
### DH-ADR-EXCELJS-1 Spreadsheets are parsed in the host and shown as server-rendered tables

- **Choice.** For `.xlsx`, `.csv` and `.tsv` the host parses the file into a small
  view model and renders it with the same template engine as the rest of the interface.
  The browser receives a table, not bytes and not a parser. Legacy `.xls` is converted
  first ([xlsx.md](xlsx.md)).
- **Why.** [DH-ADR-3](../design.md#dh-adr-3) keeps JavaScript for widgets that cannot be
  avoided. A read-only table is ordinary HTML: sticky rows and columns give frozen
  headings, the browser gives selection and copy. Parsing in Go also removes the patch,
  the Worker protocol and four bundled libraries.
- **Tradeoff.** Parsing costs host CPU and memory instead of the browser's, and a
  goroutine cannot be killed the way a Worker can; the limits in
  `DH-LIB-EXCELJS-R5` are what bound it. Canvas-level niceties of the grid (pixel-exact
  column widths, diagonal trackpad panning) become plain browser scrolling.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-EXCELJS-1` to
  `DH-T-EXCELJS-5`, and C6's browser scenario.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `github.com/xuri/excelize/v2` in the host | Reads everything `DH-LIB-EXCELJS-R1` lists through a public API: `Rows` (streaming rows, `rows.go:339`), `GetCellFormula`, `GetCellRichText`, `GetCellStyle` and `GetStyle`, `GetMergeCells`, `GetColWidth`, `GetRowHeight`, `GetColVisible`, `GetRowVisible`, `GetSheetVisible`, `GetPanes`, `GetConditionalFormats`, `GetPictures`. Applies number formats by default (`RawCellValue` option). Has open-time limits `UnzipSizeLimit` and `UnzipXMLSizeLimit` (`excelize.go:87-96`). No cgo | Larger than the need: it is a full read/write library with a formula engine (`calc.go`) the preview must not call. Its `go.mod` requires `golang.org/x/crypto`, `x/image`, `x/net`, `x/text` and four small modules. Worksheets over the XML limit are unpacked to temporary files, which `Close` removes (`excelize.go:91-98`, `file.go:89-90`) | v2.11.1, 2026-11-02, BSD-3-Clause; `go list -m`, `LICENSE`, source read, 2026-11-18 |
| Own reader on `archive/zip` and `encoding/xml` | Smallest surface; only the parts the preview needs | Number formats and style resolution (themes, tints, indexed colours) are most of the work and easy to get subtly wrong | n/a (standard library) |
| Keep ExcelJS and a grid in a JavaScript island | Same pixels as upstream | Keeps the 35 KB patch and the two grid patches, a bundler step and a Worker protocol; contradicts DH-ADR-3 where HTML suffices | n/a |
| Convert every workbook through the Office engine to HTML or PDF | One path for all formats | Needs LibreOffice for a plain preview; slow; loses cell selection | see [deepseek-ai-libreoffice-kit.md](deepseek-ai-libreoffice-kit.md) |

**Tradeoff.** Fidelity now depends on excelize's reading of styles; the preview states
that it shows saved values with approximate formatting, as upstream warns that its
column-width and theme-tint approximation "can affect fidelity" (`README.md:89`).

## 6. Design notes

The reader is harness code in the document-preview package (area C6); no deliverable is
declared here.

- **View model.** `Workbook{Sheets, Notices}`, `Sheet{Name, Hidden, Rows, Cols, Merges,
  FrozenRows, FrozenCols}`, `Cell{Text, Formula, StyleID}` and a table of distinct
  styles. Styles become generated CSS classes. Inference: colours are parsed to a fixed
  hexadecimal form and font names are matched against a character allow-list before
  they are written into a style rule; a value that does not pass is dropped, not
  escaped (`DH-LIB-EXCELJS-R6`).
- **Limits.** The file size is checked from metadata before reading. The workbook is
  opened with `UnzipSizeLimit` set from configuration and a private temporary
  directory. The cell area is accumulated sheet by sheet from the row iterator and the
  walk stops at the limit. A deadline discards the result; the goroutine ends when the
  bounded walk ends.
- **Ownership.** An opened workbook holds an archive handle and possibly temporary
  files: the function that opens it closes it before returning. A bounded cache of view
  models, keyed by file identity and version, is owned by the preview component's
  fiber and dropped on unload. Nothing is persisted.
- **Concurrency.** A small limiter bounds simultaneous parses across browser tabs.
- **Failure.** A parse error is `invalid`; a limit is `tooLarge`; neither is retried.
  A changed file version invalidates the cached model and the next request parses
  again.

## 7. Risks and open questions

1. **For C6.** Whether excelize meets every part of `DH-LIB-EXCELJS-R4` is not
   established. Upstream keeps "independent-writer regressions and fuzz diagnostics"
   for exactly these cases (`README.md:93`); equivalent fixtures must be written for Go,
   since upstream's cannot be reused as they are.
2. **For C6.** How the table is paged and how the formula of a selected cell is shown
   are viewer design; this spec only requires bounded pieces (`-R8`).
3. Memory per parse is unmeasured. If it is too high for the default 16 MiB file limit,
   lower the limit or use the own-reader option for the hot path.
4. `excelize` can open password-protected workbooks; the preview passes no password, so
   such a file fails as `invalid` with a specific message.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-EXCELJS-1` | local | `DH-LIB-EXCELJS-R1`, `-R2`, `-R3` | Golden view models and rendered HTML for committed workbooks covering each retained feature, a formula without a saved result, and a workbook with a chart, an image and conditional formatting (notice present, nothing drawn). |
| `DH-T-EXCELJS-2` | local | `DH-LIB-EXCELJS-R4` | Workbooks built by the test itself with non-default part paths, absolute and relative targets, renamed prefixes, Strict namespaces, UTF-16 parts, CDATA and plain-text comments open identically; missing, ambiguous-case and external-target packages give `invalid`. |
| `DH-T-EXCELJS-3` | local | `DH-LIB-EXCELJS-R6` | Cells, formulas, sheet names and font names containing markup, `</style>`, quotes and entity text (`&lt;`) render as literal text; the output contains no element or style the template did not write. |
| `DH-T-EXCELJS-4` | local | `DH-LIB-EXCELJS-R5` | An archive that expands far beyond the limit, a sheet that declares a huge area with few cells, and a slow reader are each refused within a memory and time budget. |
| `DH-T-EXCELJS-5` | local | `DH-LIB-EXCELJS-R7`, `-R8` | The source file's bytes are unchanged after a preview, temporary files are gone, and a 250,000-cell sheet is served in fragments no larger than the configured window. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-exceljs`, `UP-DEBT-patch.exceljs@4.4.0`.
