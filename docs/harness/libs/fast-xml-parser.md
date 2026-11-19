# fast-xml-parser: Go specification

> **Status: design, not built.** Short form. Upstream facts were read at the pinned commit
> recorded in [../README.md](../README.md). Statements that are reasoning and not read
> from source open with "Inference". The viewer this belongs to is specified in
> [exceljs.md](exceljs.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-FASTXMLPARSER` |
| **Upstream unit** | `UP-DEP-fast-xml-parser` |
| **Upstream version** | 5.11.1 |
| **Kind** | bundled into the client |
| **Used by** | client (`packages/client/ui-sidebar-documentpreview`) |
| **Go choice** | standard library: `encoding/xml` (with `archive/zip`), inside the host's workbook reader |
| **Related areas** | C6 (document preview) |

## 1. What it is and why upstream needs it

An `.xlsx` workbook is a ZIP archive of XML files. ExcelJS reads most of them, but the
preview needs three things ExcelJS does not hand over, and reads them itself with
fast-xml-parser, a small XML parser for JavaScript: the workbook's theme colours, the
relationship files that say which part belongs to which sheet, and a quick scan of each
sheet for content the preview cannot draw.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/client/ui-sidebar-documentpreview/src/client/excel/xlsx.ts:222-232` | Read the twelve theme colours from the theme part, so that a cell coloured "accent 1" gets a real colour | `new XMLParser({ ignoreAttributes: false, removeNSPrefix: true, processEntities: false }).parse(xml)` |
| `packages/client/ui-sidebar-documentpreview/src/client/excel/xlsx-archive.ts:41-60` | Walk every relationship file, resolve worksheet and drawing targets inside the package, fail on a missing target, and mark drawing parts for removal | `XMLParser` with `removeNSPrefix` and an `updateTag` callback |
| `packages/client/ui-sidebar-documentpreview/src/client/excel/xlsx-archive.ts:67-80` | Scan a part for charts, pictures, shapes and conditional formatting, skipping the cell data, to build the "not displayed" notice | `XMLParser` with `stopNodes: ['*.sheetData']` |
| `packages/client/ui-sidebar-documentpreview/src/client/excel/xlsx-archive.ts:88-110` | Around the parser: compare part names without regard to ASCII case, decode UTF-8 or UTF-16, and refuse a target that climbs above the package root | own code, not the library |

Entity processing is switched off in two of the three uses (`processEntities: false`).
No patch, pin or exemption names the package (`patches/` and `pnpm-workspace.yaml`
searched).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-FASTXMLPARSER-R1` | Theme colours are resolved, including the tint adjustment, so themed cells show a real colour. | `xlsx.ts:215-232` |
| `DH-LIB-FASTXMLPARSER-R2` | The scan for undisplayed content (charts, pictures, shapes, conditional formatting) follows relationships, never reads cell data to do it, and costs time proportional to the part's size. | `xlsx-archive.ts:41-80` |
| `DH-LIB-FASTXMLPARSER-R3` | XML from a workbook is parsed as inert data: no external entity or document type definition is resolved, no network or file access happens, and deeply nested or entity-heavy input cannot grow memory beyond the limits of [exceljs.md](exceljs.md) `DH-LIB-EXCELJS-R5`. | `xlsx.ts:226`, `xlsx-archive.ts:69`; Inference |
| `DH-LIB-FASTXMLPARSER-R4` | Element names are matched by namespace, not by prefix; a relationship target that leaves the package is refused. | `xlsx-archive.ts:42`, `:100-110` |

## 5. Go choice

**Decision.** `encoding/xml` from the standard library, used as a streaming token
reader (`Decoder.Token`) inside the workbook reader; no separate XML library.

**Why.** The need is three small read-only walks. `encoding/xml` reports each element
with its namespace address, so prefix handling (`removeNSPrefix`) is unnecessary; it
has no support for resolving external entities or document type definitions, which
gives `DH-LIB-FASTXMLPARSER-R3` by construction; and a token loop can skip the cell
data element with `Decoder.Skip`, which is what `stopNodes` does upstream.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `encoding/xml` | Standard library; namespace-aware; streaming; no entity expansion beyond the five predefined entities | Slower than specialised parsers; fine for parts bounded by the workbook limits | Go 1.27.4 standard library |
| Use only what `excelize` exposes | No XML code in the harness | Theme colours are available (`GetBaseColor`, `styles.go:1442`), but a notice for charts and shapes needs detection the module does not offer as one call | `github.com/xuri/excelize/v2` v2.11.1, declared in [exceljs.md](exceljs.md) |

**Tradeoff.** A little own code for the detection scan. Inference: theme colours come
from `excelize`, so only the scan and the relationship check are written by hand.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-FASTXMLPARSER-1` | local | `DH-LIB-FASTXMLPARSER-R1` to `-R4` | Workbooks built by the test: themed and tinted cells give the expected colours; a sheet with a chart, a picture, a shape and conditional formatting yields exactly those four notices; a part with a document type definition and nested entity declarations is parsed without expansion; a relationship target of `../../outside.xml` is refused; the scan of a sheet with a million cells does not visit them. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

Units dispositioned by this spec: `UP-DEP-fast-xml-parser`.
