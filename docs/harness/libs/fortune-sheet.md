# @fortune-sheet/core and @fortune-sheet/react: Go specification

> **Status: design, not built.** Short form with the tech-debt section, because both
> packages are patched. Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". The viewer this belongs to is specified in
> [exceljs.md](exceljs.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-FORTUNESHEET` (one family, two packages) |
| **Upstream units** | `UP-DEP-@fortune-sheet/core`, `UP-DEP-@fortune-sheet/react`, `UP-DEBT-patch.@fortune-sheet__core@1.0.4`, `UP-DEBT-patch.@fortune-sheet__react@1.0.4` |
| **Upstream version** | 1.0.4 for both, both patched |
| **Kind** | bundled into the client |
| **Used by** | client (`packages/client/ui-sidebar-documentpreview`) |
| **Go choice** | dropped: no spreadsheet grid widget. The host renders an HTML table |
| **Related areas** | C6 (document preview; designs the viewer) |

## 1. What it is and why upstream needs it

FortuneSheet is an open-source spreadsheet editor for the React framework: it draws a
grid on a canvas and handles selection, scrolling, a formula bar and worksheet tabs.
Upstream uses it as the display surface of the spreadsheet preview, with editing
switched off. The parsers ([exceljs.md](exceljs.md), [xlsx.md](xlsx.md),
[papaparse.md](papaparse.md)) produce FortuneSheet's cell format and the widget draws it.

| Package | What it adds |
|---|---|
| `@fortune-sheet/core` | The data model (`Sheet`, `Cell`, `CellStyle`, `SheetConfig`), number formatting and the canvas drawing code |
| `@fortune-sheet/react` | The `Workbook` React component and its style sheet |

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/client/ui-sidebar-documentpreview/src/client/excel/excel.tsx:3`, `:63-65` | The read-only grid: no toolbar, formula bar and sheet tabs shown, no recalculation, a context menu with only "copy" | `<Workbook data allowEdit={false} showToolbar={false} showFormulaBar showSheetTabs forceCalculation={false} cellContextMenu={['copy']} …>` |
| `packages/client/ui-sidebar-documentpreview/src/client/excel/excel.tsx:5`, `:13` | The widget's style sheet, inlined and confined to the preview with a CSS scope | `@fortune-sheet/react/dist/index.css` |
| `packages/client/ui-sidebar-documentpreview/src/client/excel/excel.tsx:38-45` | Workaround: the widget only re-measures on a window resize, so a resize event is faked when the pane changes size | `ResizeObserver`, not the library |
| `packages/client/ui-sidebar-documentpreview/src/client/excel/xlsx.ts:3`, `xls.ts:4`, `delimited.ts:3` | The cell and sheet types every parser adapter produces | types from `@fortune-sheet/core` |
| `packages/client/ui-sidebar-documentpreview/src/client/excel/model.ts:2` | Apply a number format to a value | `update` from `@fortune-sheet/core` |

## 3. Tech debt upstream carries

Both patches are applied to the published ESM and CommonJS bundles
(`pnpm-workspace.yaml:116-117`). The reasons are recorded in
`.agents/notes/implemented/feature/2026-09-16-browser-excel-preview.md:23-27`.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-patch.@fortune-sheet__core@1.0.4` | Three fixes. (1) **Injection through copy:** when cells are copied as an HTML table, the widget's own escaping helper lets a string that starts with `<span` through unchanged, so file content could become markup on the clipboard; the patch escapes every value. (2) Freeze divider lines, drawn on the canvas and not removable by CSS, are skipped in read-only mode. (3) Read-only trackpad scrolling follows the gesture on both axes at once | `patches/@fortune-sheet__core@1.0.4.patch:5-11`, `:14-19`, `:22-35` | **Resolved** by [DH-ADR-FORTUNESHEET-1](#dh-adr-fortunesheet-1): there is no widget. (1) Cell text is written by the host's template engine, which escapes by context, and a copy takes the already-escaped text from the page (`DH-LIB-FORTUNESHEET-R2`). (2) and (3) are properties of the canvas grid; a table has sticky header rows and native scrolling. |
| `UP-DEBT-patch.@fortune-sheet__react@1.0.4` | Four fixes. (1) An input-box effect that runs on every render in read-only mode caused React's "maximum update depth" failure when switching worksheets; it now runs only when formula highlighting is on. (2) **Injection through the formula bar:** the bar wrote formula text with `innerHTML`; the patch shows the raw formula and writes it as text. (3) Switching sheets cleared the selection instead of restoring the sheet's own. (4) The tab scroll buttons miscounted the available width | `patches/@fortune-sheet__react@1.0.4.patch:5-13`, `:14-32`, `:33-42` and `:60-69`, `:43-56` | **Resolved** by [DH-ADR-FORTUNESHEET-1](#dh-adr-fortunesheet-1). (1) and (3): a worksheet tab is a request for a server-rendered fragment, so there is no client state loop and no client selection model to restore. (2): `DH-LIB-FORTUNESHEET-R2`. (4): tabs are plain elements in a scrolling row. |

Upgrading the widget upstream means re-checking five behaviours by hand
(`…2026-09-16-browser-excel-preview.md:27`); that duty disappears with the widget.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-FORTUNESHEET-R1` | The spreadsheet preview needs no JavaScript grid: worksheet tabs, frozen headings, scrolling, text selection and copy work with HTML, CSS and the shared htmx runtime. | [DH-ADR-3](../design.md#dh-adr-3); `README.md:75` |
| `DH-LIB-FORTUNESHEET-R2` | Cell text, formula text and sheet names reach the page only as escaped text. Copying cells yields their text; no markup from the file can reach the page or the clipboard as markup. | the two injection fixes above |
| `DH-LIB-FORTUNESHEET-R3` | The view is read-only by construction: it contains no editing control to hide. | `excel.tsx:63-65` |
| `DH-LIB-FORTUNESHEET-R4` | Switching worksheets keeps the pane's size and does not reload other sheets' data into the page. | `README.md:75` |

## 5. Go choice

<a id="dh-adr-fortunesheet-1"></a>
### DH-ADR-FORTUNESHEET-1 No spreadsheet grid widget; the preview is a server-rendered table

- **Choice.** The Go design ships no spreadsheet component. The view model defined in
  [exceljs.md](exceljs.md) is rendered as an HTML `<table>` in bounded fragments.
- **Why.** FortuneSheet is an editor used here with editing off; two security fixes and
  five behaviour fixes were needed to make it a safe viewer. It requires the React
  runtime, which the Go interface does not have ([DH-ADR-3](../design.md#dh-adr-3)).
  A read-only table needs none of it.
- **Tradeoff.** No canvas virtualisation, so a large sheet is paged instead of scrolled
  as one surface; selection is the browser's text selection, not a spreadsheet range
  with a formula bar that follows it; column widths are approximate. Number formats
  are applied by the host parser, not by this library's `update` function.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-FORTUNESHEET-1`,
  `DH-T-EXCELJS-3` in [exceljs.md](exceljs.md), and C6's browser scenario.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** HTML table rendered by the host | No dependency, no patches, escaping by the template engine, works without a client build | Loses grid-style range selection and smooth scrolling of very large sheets | n/a (no library) |
| Keep FortuneSheet as a JavaScript island | Same look as upstream | Needs React and a bundler inside an island, both patches, and the resize workaround | MIT (`THIRD_PARTY_NOTICES.md:44-45`) |
| Another grid library without React | A real grid without React | None was evaluated; every candidate would still be a large script for a read-only view | none named |

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-FORTUNESHEET-1` | local | `DH-LIB-FORTUNESHEET-R2`, `-R3`, `-R4` | Render a workbook whose cells, formulas and sheet names contain `<span onmouseover=…>`, `<script>` and quotes: the fragment parses to a DOM whose only elements are the template's own, with the hostile strings as text nodes. The fragment contains no form control, and a tab request returns only that sheet's rows. |

`DH-LIB-FORTUNESHEET-R1` (the page works in a real browser with no viewer script) is a
real-environment scenario owned by C6. Local tests and real-environment scenarios are
separate kinds. Nothing here has been run.

Units dispositioned by this spec: `UP-DEP-@fortune-sheet/core`,
`UP-DEP-@fortune-sheet/react`, `UP-DEBT-patch.@fortune-sheet__core@1.0.4`,
`UP-DEBT-patch.@fortune-sheet__react@1.0.4`.
