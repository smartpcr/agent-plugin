# xlsx (SheetJS Community Edition): Go specification

> **Status: design, not built.** Short form. Upstream facts were read at the pinned commit
> recorded in [../README.md](../README.md). Statements that are reasoning and not read
> from source open with "Inference". Go module facts were checked on 2026-11-18 with
> `go list -m -json <module>@latest`, `go mod download -json` and the `LICENSE` file in
> the module cache. The viewer this belongs to is specified in [exceljs.md](exceljs.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-XLSX` |
| **Upstream units** | `UP-DEP-xlsx`, and the pin `UP-DEBT-found.L12.1` |
| **Upstream version** | 0.20.3, installed from a vendor download address, not from the npm registry |
| **Kind** | bundled into the client |
| **Used by** | client (`packages/client/ui-sidebar-documentpreview`) |
| **Go choice** | dropped: no reader for the legacy binary format. A `.xls` file is converted to `.xlsx` by the Office engine and then follows the `.xlsx` path |
| **Related areas** | C6 (document preview), A16 (Office engine) |

## 1. What it is and why upstream needs it

Excel files saved before 2007, and by some export tools since, use `.xls`: a binary
format called BIFF inside a container file, unrelated to the ZIP-and-XML `.xlsx`.
ExcelJS cannot read it, so upstream bundles a second parser, SheetJS, only for these
files. It gives the preview saved values, formulas, number formats, merged cells and
some row and column sizes; fonts, borders and frozen panes are not available
(`packages/client/ui-sidebar-documentpreview/README.md:75`).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/client/ui-sidebar-documentpreview/src/client/excel/xls.ts:19-23` | Refuse a file that is not really binary Excel (a renamed text or HTML file) by its first bytes, before parsing | none (own check) |
| `packages/client/ui-sidebar-documentpreview/src/client/excel/xls.ts:24-27` | Parse the workbook with number formats, styles, formulas and empty cells | `read(bytes, { type: 'array', cellNF, cellStyles, cellFormula, sheetStubs })` |
| `packages/client/ui-sidebar-documentpreview/src/client/excel/xls.ts:3`, `:8` | Load legacy code pages so that non-Latin text decodes | `set_cptable(codepages)` |
| `packages/client/ui-sidebar-documentpreview/src/client/excel/xls.ts:10-11`, `:36-41` | Decide whether a number format is a date; read sheet names and hidden flags | `SSF.is_date`, `workbook.SheetNames`, `workbook.Workbook.Sheets` |

**Tech debt.** `UP-DEBT-found.L12.1`: the dependency is declared as a download address,
`https://cdn.sheetjs.com/xlsx-0.20.3/xlsx-0.20.3.tgz`
(`packages/client/ui-sidebar-documentpreview/package.json:90`), because the vendor no
longer publishes current versions to the npm registry. The package therefore bypasses
the registry's integrity and availability, and a build fails if that one host is down.
No patch or other pin names it. Resolved by dropping the dependency
([DH-ADR-XLSX-1](#dh-adr-xlsx-1)).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-XLSX-R1` | A `.xls` file is accepted only when its first bytes identify a binary Excel file; anything else is refused as `invalid` before any conversion starts. | `xls.ts:19-23` |
| `DH-LIB-XLSX-R2` | When an Office engine is available, a `.xls` file previews in the same table viewer as `.xlsx`, with at least saved values, formulas, number formats and merged cells. | `README.md:75` |
| `DH-LIB-XLSX-R3` | When no engine is available, the preview shows the unsupported state with the reason and the install hint; it never shows partial or guessed content. | Inference from `packages/document/office-kit/README.md:43` |
| `DH-LIB-XLSX-R4` | The conversion uses the Office provider's queue at background priority, its limits and its cache; it adds no second conversion path. | `packages/document/office-kit/README.md:63-65` |

## 5. Go choice

<a id="dh-adr-xlsx-1"></a>
### DH-ADR-XLSX-1 Legacy `.xls` previews go through the Office engine; no BIFF parser is added

- **Choice.** The Go design contains no reader for the binary Excel format. The
  preview asks the Office provider for an `.xlsx` copy and renders that.
- **Why.** No suitable Go reader was found: the maintained candidate is under a
  copyleft licence and the permissive one has not been released since 2017 (table
  below). The Office engine already converts `.xls` for the model-facing tools, so the
  preview reuses a path that must exist anyway, and the result gains the formatting
  the old parser could not read.
- **Tradeoff.** Upstream previews `.xls` with nothing installed; the Go product needs
  LibreOffice for it. Conversion is slower than a direct parse; the provider's cache
  hides that after the first open.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-XLSX-1` and the
  Office engine scenarios in [deepseek-ai-libreoffice-kit.md](deepseek-ai-libreoffice-kit.md).

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** convert with the Office engine, then read as `.xlsx` | One viewer, one parser, better fidelity | Needs the engine; first open is slow | see [deepseek-ai-libreoffice-kit.md](deepseek-ai-libreoffice-kit.md) and [exceljs.md](exceljs.md) |
| `github.com/shakinm/xlsReader` | Pure Go BIFF reader | **GPL-3.0**, which the harness (MIT) cannot link; last release 2022 | v0.9.12, 2022-09-27; `LICENSE` read, 2026-11-18 |
| `github.com/extrame/xls` | Pure Go, permissive | One tagged release, from 2017; an unmaintained parser for untrusted binary input | v0.0.1, 2017-02-21, Apache-2.0; `LICENSE` read, 2026-11-18 |
| Keep SheetJS in a JavaScript island | Same behaviour as upstream | Keeps the vendor-address install and a parser in the browser, against [exceljs.md](exceljs.md) DH-ADR-EXCELJS-1 | n/a |

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-XLSX-1` | local | `DH-LIB-XLSX-R1`, `-R3`, `-R4` | With a fake Office provider that returns a committed `.xlsx`: a real `.xls` header reaches the provider once at background priority and the table renders; a text file named `.xls` never reaches it; with no provider the unsupported state and hint are shown. |

`DH-LIB-XLSX-R2` needs a real LibreOffice and is covered by area A16's scenarios. Local
tests and real-environment scenarios are separate kinds. Nothing here has been run.

Units dispositioned by this spec: `UP-DEP-xlsx`, `UP-DEBT-found.L12.1`.
