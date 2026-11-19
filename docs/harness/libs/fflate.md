# fflate and extract-zip (ZIP archives): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-FFLATE` |
| **Upstream unit** | `UP-DEP-fflate`, `UP-DEP-extract-zip` |
| **Upstream version** | fflate 0.8.3; extract-zip 2.0.1 |
| **Kind** | fflate: runtime in two host packages, bundled into the client in one, test-only elsewhere. extract-zip: build-only |
| **Used by** | fflate: session-query, experimental, client, apps. extract-zip: root scripts and `apps/desktop` |
| **Go choice** | standard library (`archive/zip`, `compress/flate`) |
| **Related areas** | session export, chat import (experimental), document preview, release |

## 1. What it is and why upstream needs it

A **ZIP archive** is one file that holds many files, each usually compressed with the
DEFLATE method, followed by a **central directory** that lists them. fflate is a
JavaScript library that reads and writes ZIP archives and DEFLATE streams without native
code. extract-zip is a small tool that unpacks a ZIP file to a directory.

The harness writes one kind of ZIP and reads two. It *writes* a session export: the log
of a session, the logs of its subagents and every attachment they reference, streamed to
the browser as a download. It *reads* a chat export uploaded by a user who wants to
import conversations from the DeepSeek chat product, and it reads spreadsheet files
(`.xlsx` is a ZIP) to preview them. extract-zip is used only while building the product,
to unpack the Electron and Node runtimes that upstream bundles.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/session-query/session-log-export/src/archive.ts:659-673` | Stream a ZIP to an HTTP response one entry at a time, at a configurable compression level, never holding the whole archive, pausing when the browser reads slowly and stopping when the request is cancelled (`:16-20`) | `Zip`, `ZipDeflate`, `push`, `terminate` |
| `packages/experimental/deepseek-chat-import/src/archive.ts:140`, `:211-223`, `:297-305` | Open an uploaded ZIP held in memory (up to 512 MiB, `src/index.ts:60`), read its directory, inflate only the entries asked for, one at a time, with a per-entry size limit (256 MiB, `:62`) | `unzip` with a filter, `Unzip`, `UnzipInflate` |
| `packages/client/ui-sidebar-documentpreview/src/client/excel/xlsx-archive.ts:19`, `:64` | In the browser: open a spreadsheet's ZIP and rebuild it without some entries | `unzipSync`, `zipSync` |
| about 25 test files, for example `apps/web/tests/office-fixture.ts:3` | Build and inspect ZIP fixtures | `zipSync`, `unzipSync`, `strToU8`, `strFromU8` |
| `scripts/primary-runtime/prepare.ts:97`, `:141`; `apps/desktop/scripts/prepare-runtime.ts:29` | Build time: unpack the Windows Node archive, a Python wheel and the Electron archive | `extractZip(archive, { dir, onEntry })` |

## 3. Tech debt upstream carries

Search performed: every import of both libraries; `patches/` (nothing);
`pnpm-workspace.yaml:18`, which pins extract-zip's own dependency `yauzl` to 3.4.0 with
no stated reason (that pin is the ledger unit `UP-DEBT-pin.overrides.extract-zip_yauzl`,
owned by the toolchain agent); the "Known Limitations" sections of the consumers. The
bullets below are ledger units owned by the experimental area.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| (owned by the experimental area) | Inflation is pushed, not pulled: an entry that declares a small size keeps inflating to its end, and the limit only stops the bytes being kept | `packages/experimental/deepseek-chat-import/README.md:155` | `DH-LIB-FFLATE-R2`: Go's reader is pulled by the caller, so a limit stops the work. |
| (owned by the experimental area) | A second local header with an existing name is not detected and can deliver extra entries | `packages/experimental/deepseek-chat-import/README.md:156` | `archive/zip` reads entries from the central directory only; data not listed there is never returned. |
| (owned by the experimental area) | A by-name read truncates an over-long entry instead of failing, and re-parses the directory on each call | `packages/experimental/deepseek-chat-import/README.md:158` | `DH-LIB-FFLATE-R2`: the directory is parsed once; an over-long entry is an error. |

No debt of their own was found for these libraries beyond the pin.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-FFLATE-R1` | Write a ZIP as a stream to a writer that cannot seek, entry by entry, at a chosen compression level, with memory use independent of archive size. A slow reader slows the producer; cancellation stops it and releases the compressor. | `session-log-export/src/archive.ts:16-20`, `:659-673` |
| `DH-LIB-FFLATE-R2` | Read a ZIP: list the directory once, reject duplicate names, read chosen entries one at a time, and fail an entry as soon as it exceeds its declared size or the configured limit, without inflating the rest. A damaged entry fails alone. | `deepseek-chat-import/src/archive.ts:76-107`; the three bullets in section 3 |
| `DH-LIB-FFLATE-R3` | Unpack to disk only through names that stay inside the destination. | Inference: any Go release or import step that unpacks a ZIP must not write outside its target |

## 5. Go choice

**Decision.** The standard library: `archive/zip` for the container and
`compress/flate` for the compression level.

**Why.** It covers every use. `zip.Writer` writes to a plain `io.Writer` and marks each
entry's size after its data, which is what streaming to an HTTP response needs.
`zip.Reader` works over any `io.ReaderAt`, so an upload can be read from memory or from
a temporary file without a second code path.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `archive/zip`, `compress/flate` | No dependency; pull-based reading; checks each entry's checksum and length at its end | Names are not checked for safety unless the caller does it (see below) | Go 1.27.4 source. `archive/zip/reader.go:72-76` and `:101-102`: `ErrInsecurePath` is returned only when `GODEBUG=zipinsecurepath=0` is set. |
| `github.com/klauspost/compress` (`zip`, `flate`) | Faster DEFLATE | A module for speed nobody has asked for; export size is dominated by attachments that are already compressed | v1.20.4, see [node-fs-path-os-zlib.md](node-fs-path-os-zlib.md) |

**Tradeoff.** None found for these uses.

Design notes in brief:

- **Export.** The handler creates a `zip.Writer` over the response and copies each
  source with `io.Copy`. Back-pressure is the blocking write; cancellation is the request
  context, checked between entries and by the copy failing. The handler's goroutine owns
  the writer and closes it; the HTTP server owns the connection. A failure after the
  first byte cannot change the status code, so the archive is simply cut short, which a
  ZIP reader detects because the central directory is missing. This is the same limit
  upstream documents (`packages/session-query/session-log-export/README.md:158`).
- **Import.** `zip.NewReader`, then for each wanted entry
  `io.LimitReader(f.Open(), limit+1)`; reading one byte past the limit is the "too large"
  error. Duplicate names are rejected while indexing the directory.
- **Names.** Every entry name is checked with `filepath.IsLocal` before use, and files
  are created through an `os.Root` opened on the destination, so a crafted name cannot
  leave it.
- **Spreadsheet preview** moves to the host with the rest of the UI
  ([DH-ADR-3](../design.md#dh-adr-3)) and uses the same package.
- **extract-zip** has no counterpart. The Go release bundles no Node, Python or Electron
  runtime ([DH-ADR-TOOLCHAIN-13](../upstream/toolchain.md#dh-adr-toolchain-13),
  [DH-ADR-3](../design.md#dh-adr-3)), so the scripts that call it do not exist. Any Go
  build step that needs to unpack a ZIP uses `archive/zip` under `DH-LIB-FFLATE-R3`.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-FFLATE-1` | local | `DH-LIB-FFLATE-R1` | Stream an archive of many entries through a pipe whose reader is slow: peak heap stays flat as the archive grows; cancelling mid-entry returns promptly; the completed archive opens with `zip.NewReader` and with an external `unzip` listing recorded as a golden file. |
| `DH-T-FFLATE-2` | local | `DH-LIB-FFLATE-R2` | Crafted archives: duplicate names, an entry that inflates past its declared size, an entry over the limit, a damaged entry between two good ones, data present only in a local header. Each gives the specified error or omission, and the over-limit case stops reading within one buffer of the limit. |
| `DH-T-FFLATE-3` | local | `DH-LIB-FFLATE-R3` | Entries named `../x`, an absolute path, a Windows drive path and a reserved Windows name are refused; a symbolic-link entry is not followed. |

Local tests only. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-fflate`, `UP-DEP-extract-zip`.
