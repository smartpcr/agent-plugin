# papaparse: Go specification

> **Status: design, not built.** Short form. Upstream facts were read at the pinned commit
> recorded in [../README.md](../README.md). Statements that are reasoning and not read
> from source open with "Inference". Standard-library behaviour is stated from the Go
> 1.27.4 documentation of `encoding/csv`. The viewer this belongs to is specified in
> [exceljs.md](exceljs.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-PAPAPARSE` |
| **Upstream unit** | `UP-DEP-papaparse` |
| **Upstream version** | 5.5.3 and 5.7.0 |
| **Kind** | bundled into the client; also used by one build script |
| **Used by** | client (`packages/client/ui-sidebar-documentpreview`), root (`scripts/primary-runtime`) |
| **Go choice** | standard library: `encoding/csv`, `unicode/utf8`, `unicode/utf16` |
| **Related areas** | C6 (document preview) |

## 1. What it is and why upstream needs it

CSV and TSV are text files with one record per line and fields separated by commas or
tabs; a field may be quoted, and a quoted field may contain separators, quotes and line
breaks. PapaParse is the JavaScript parser upstream uses to show such a file as a
one-sheet table in the spreadsheet preview. The preview shows what the file says: every
field stays a literal string, so leading zeros, dates and text that looks like a formula
are not reinterpreted (`packages/client/ui-sidebar-documentpreview/README.md:79`).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/client/ui-sidebar-documentpreview/src/client/excel/delimited.ts:15-19` | Decode UTF-8, or UTF-16 when a byte-order mark says so; invalid bytes are an `encoding` failure | `TextDecoder` (not the library) |
| `packages/client/ui-sidebar-documentpreview/src/client/excel/delimited.ts:24-35` | Parse row by row with a fixed delimiter, no header row, no type guessing and empty lines kept; stop at the first parser error or when rows × widest row passes the cell limit | `Papa.parse(text, { delimiter, header: false, dynamicTyping: false, skipEmptyLines: false, step })`, `parser.abort()` |
| `scripts/primary-runtime/prune-python-tests.ts:31`, `:39` | Build script: rewrite the file list inside a Python package | `Papa.parse`, `Papa.unparse` |

No patch, pin or exemption names it (`patches/` and `pnpm-workspace.yaml` searched).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-PAPAPARSE-R1` | `.csv` splits on commas and `.tsv` on tabs. Quoted separators, doubled quotes, line breaks inside quotes, empty fields and rows of unequal length are accepted. The first row is data. | `README.md:79`, `delimited.ts:25` |
| `DH-LIB-PAPAPARSE-R2` | Every field is shown as the literal text in the file. Nothing is converted to a number, date, boolean or formula. | `README.md:79` |
| `DH-LIB-PAPAPARSE-R3` | A malformed quoted field fails the table preview as `invalid`; rows are never dropped silently. An empty line is shown as an empty row, so row numbers match the file. | `README.md:79`, `delimited.ts:25`, `:29-32` |
| `DH-LIB-PAPAPARSE-R4` | Input is UTF-8, or UTF-16 with a byte-order mark. Invalid byte sequences, including unpaired surrogates, fail with `encoding`. | `delimited.ts:15-19` |
| `DH-LIB-PAPAPARSE-R5` | Parsing stops as soon as rows × widest row exceeds the cell limit, with `tooLarge`; the rest of the file is not read. | `delimited.ts:27-32` |

## 5. Go choice

**Decision.** `encoding/csv` with `Comma` set per format, `FieldsPerRecord = -1` (rows
may differ in length), `LazyQuotes = false` (a bad quote is an error) and
`ReuseRecord = true`, reading one record at a time so that the cell limit stops the
read early. Decoding and validation use `unicode/utf8` and `unicode/utf16`.

**Why.** The requirement is the RFC 4180 grammar with a strict error policy, which is
what the standard reader implements. No type guessing exists to switch off.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `encoding/csv` | Standard library; streaming; strict quotes by default; reports the line and column of an error | It skips blank lines, and `utf16.Decode` replaces a bad surrogate instead of failing; both need a few lines of harness code (below) | Go 1.27.4 standard library |
| Own reader | Exact control of blank lines | A second CSV grammar to test, for one difference | n/a |

**Tradeoff.** Two small pieces of own code. *Blank lines:* the reader's `FieldPos`
gives the line on which each record starts; the gap between that and the line after
the previous record is emitted as empty rows (`DH-LIB-PAPAPARSE-R3`). *UTF-16:* code
units are checked for unpaired surrogates before `utf16.Decode` is called
(`DH-LIB-PAPAPARSE-R4`).

The build-script use has no Go counterpart in the product. Inference: if the Python
SDK packaging still needs to rewrite a package file list, a Go tool does it with the
same package's `csv.Writer`.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-PAPAPARSE-1` | local | `DH-LIB-PAPAPARSE-R1` to `-R5` | Table test: quoted commas and tabs, doubled quotes, a line break inside quotes, ragged rows, blank lines in the middle and at the end, `0012` and `=SUM(A1)` kept as text, a stray quote (error with line number), UTF-16 in both byte orders, an unpaired surrogate, invalid UTF-8, and a file whose second row crosses the cell limit (the reader is shown not to have consumed the rest). |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

Units dispositioned by this spec: `UP-DEP-papaparse`.
