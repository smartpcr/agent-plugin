# pdfjs-dist: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". The package itself is not present in the upstream checkout (no
> `node_modules`), so file names inside it are taken from upstream's import paths and
> its size was not measured.

| | |
|---|---|
| **Library ID** | `DH-LIB-PDFJSDIST` |
| **Upstream unit** | `UP-DEP-pdfjs-dist` |
| **Upstream version** | 6.3.289 (`pnpm-lock.yaml:27012`) |
| **Kind** | bundled into the client (a lazily loaded chunk) |
| **Used by** | client (`packages/client/ui-sidebar-documentpreview`) |
| **Go choice** | own code: a small JavaScript island that loads the prebuilt pdf.js files, which are kept as a vendored browser asset embedded in the binary. No Go module |
| **Related areas** | C6, which owns the `<dsh-pdf>` island and the preview registry ([../areas/ui-sidebar-workspace.md](../areas/ui-sidebar-workspace.md)); A16 (converted Office documents arrive as PDF); C1 (static assets and the page policy, [../areas/ui-core.md](../areas/ui-core.md)) |

## 1. What it is and why upstream needs it

The sidebar previews PDF files, and Word and PowerPoint files after the Office engine
has converted them to PDF (`packages/client/ui-sidebar-documentpreview/README.md:98`).
A PDF is a program-like description of pages; drawing one needs a full interpreter
with fonts and image decoders. PDF.js is Mozilla's interpreter written in JavaScript:
it parses the file in a **Web Worker** (a background browser thread), draws each page
on a canvas and lays an invisible text layer over it so that text can be selected and
copied. `pdfjs-dist` is its prebuilt distribution.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/client/ui-sidebar-documentpreview/src/client/pdf/runtime.ts:70-91` | Start one dedicated Worker per open document and wait for it to report ready; a start-up failure never falls back to parsing on the page's own thread (`:10-11`) | `new Worker(url, { type: 'module' })`, `PDFWorker.create({ port })` |
| `packages/client/ui-sidebar-documentpreview/src/client/pdf/runtime.ts:92-104` | Open the document from a copy of the bytes with hardened options: no network fetch from the Worker, no XFA forms, stop at errors, no code evaluation | `getDocument({ data, worker, BinaryDataFactory, cMapPacked: true, useWorkerFetch: false, enableXfa: false, stopAtErrors: true, isEvalSupported: false })` |
| `packages/client/ui-sidebar-documentpreview/src/client/pdf/assets.ts:7`, `:28-38` | Supply character maps, the standard fonts and the image decoders from files built into the application; a request for a file that is not bundled is an error, not a download | `BinaryDataFactory` |
| `packages/client/ui-sidebar-documentpreview/src/client/pdf/text.ts:3-4` | The selectable text layer | `TextLayerBuilder` from `pdfjs-dist/web/pdf_viewer.mjs` |
| `packages/client/ui-sidebar-documentpreview/src/client/pdf/runtime.ts:49-67` | Close: destroy the loading task, terminate the Worker, release its address | `loadingTask.destroy()`, `worker.terminate()` |
| `packages/client/ui-sidebar-documentpreview/README.md:66-68` | Viewer behaviour: pages in a vertical sequence drawn only near the viewport, "fit width" and 25% to 400% zoom, redraw at device resolution after zoom settles | (behaviour) |
| `packages/client/ui-sidebar-documentpreview/README.md:148` | Each page bitmap is capped at 16,777,216 pixels | (behaviour) |

**Tech debt.** No patch, pin or exemption names the package (`patches/` and
`pnpm-workspace.yaml` searched). Two workarounds exist only because of the client
bundler: the Worker's source is inlined as text and started from a temporary address
(`assets.ts:2`, `runtime.ts:72-76`), and every binary asset is inlined as base64 text
(`assets.ts:9-15`). Neither applies when a server can serve files; see §6. The two
README limitations that mention PDF (`README.md:148`, `:152`) are inventory units of
area C6.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-PDFJSDIST-R1` | A PDF, and an Office document converted to PDF, is shown as a vertical sequence of pages with selectable, copyable text, drawn lazily near the viewport, with "fit width" and fixed zoom steps. | `README.md:66-68`, `:98` |
| `DH-LIB-PDFJSDIST-R2` | The file is parsed in a Worker, never on the page's main thread. If the Worker cannot start or fails, the preview shows an error and offers retry. Closing the preview terminates the Worker. | `runtime.ts:10-11`, `:49-67` |
| `DH-LIB-PDFJSDIST-R3` | Nothing is fetched from outside the host: the viewer script, the Worker, character maps, fonts and decoders are served by the `dsh` binary, all from one pdf.js version. PDF scripting, XFA forms and code evaluation stay off. | `runtime.ts:92-104`, `assets.ts:28-38` |
| `DH-LIB-PDFJSDIST-R4` | A page bitmap never exceeds 16,777,216 pixels, and the file size is bounded by the host's file-read limit. | `README.md:59`, `:148` |
| `DH-LIB-PDFJSDIST-R5` | The island needs no build step and no package manager: it is a plain script module beside the vendored files, whose version and checksums are recorded in the repository. | Inference from DH-ADR-3 and the toolchain rule of one Go build |

## 5. Go choice

<a id="dh-adr-pdfjsdist-1"></a>
### DH-ADR-PDFJSDIST-1 The PDF preview stays a JavaScript island on vendored pdf.js

- **Choice.** Keep pdf.js. Its prebuilt files are copied into the repository at a
  pinned version, embedded in the binary and served as static files. A small island
  script creates the Worker, draws pages and the text layer, and is the only viewer
  code the harness writes.
- **Why.** [DH-ADR-3](../design.md#dh-adr-3) allows an island where a browser widget
  cannot be avoided, and a PDF viewer with selectable text is one. The alternatives
  either lose text selection or behave differently in each browser (table below).
  Parsing stays in the browser's sandbox, so the host never interprets an untrusted
  PDF for the preview.
- **Tradeoff.** One JavaScript dependency that is updated by hand (a developer tool
  downloads a release, checks its checksum and replaces the directory), several
  megabytes of embedded assets, and island code to maintain. Upstream's PDF viewer is
  about 900 lines in `src/client/pdf/`, styles and translations included; the island
  is of that order.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-PDFJSDIST-1`,
  `DH-T-PDFJSDIST-2`, and C6's browser scenario.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** island on vendored `pdfjs-dist` | Keeps `DH-LIB-PDFJSDIST-R1` in full; no host CPU or memory per document; same behaviour in every browser | A vendored script dependency; island code | Apache-2.0 (`THIRD_PARTY_NOTICES.md:135`); upstream imports `pdfjs-dist`, `pdfjs-dist/build/pdf.worker.min.mjs` and `pdfjs-dist/web/pdf_viewer.mjs` (`runtime.ts:2`, `assets.ts:2`, `text.ts:4`). Sizes not measured |
| The browser's built-in PDF viewer in a frame | No script, no assets | Inference: not uniform. Desktop browsers have one, but each differs in controls and in how it behaves inside a frame, mobile browsers may offer a download instead, and the page policy forbids foreign frames. No shared zoom control, theme, lazy-page behaviour or Office font warning | not verified in any browser |
| The host rasterises pages with `github.com/klippa-app/go-pdfium` and sends images | No script at all; the module is already proposed for Office page images | Loses text selection and copy; host CPU and memory for every open document and zoom step; the host parses untrusted PDFs (inside a WebAssembly sandbox) | v1.21.5, MIT; see [deepseek-ai-libreoffice-kit.md](deepseek-ai-libreoffice-kit.md) |

**Tradeoff.** Stated in the decision. The third row is the fallback if the owner wants
no vendored script; it would change `DH-LIB-PDFJSDIST-R1`.

## 6. Design notes

- **Layout.** Inference: `internal/ui/static/pdfjs/<version>/` holds the files copied
  from the distribution (the display module, the Worker module, the viewer module and
  its style sheet, and the `cmaps`, `standard_fonts` and `wasm` directories that
  correspond to the three asset kinds in `assets.ts:7`), with a manifest of SHA-256
  checksums. They are embedded with `embed.FS` and served under a path that contains
  the version, so they can be cached for ever.
- **Simpler than upstream.** Because the host serves files, the Worker is started from
  its own address and assets are fetched from the same origin; the inlining and
  base64 decoding are not reproduced. The content security policy allows scripts and
  workers from the host's own origin only.
- **Bytes.** The island fetches the document from the authenticated file route (or the
  Office conversion route) as one bounded response. Nothing is stored in the browser.
- **Ownership.** The island element owns the Worker and the loading task and releases
  both when it is removed from the page or the document changes; the host owns nothing
  per open PDF. A converted Office PDF is owned by the Office provider's cache.
- **Failure.** A Worker error or a parse error shows the error state with retry; a
  retry creates a new Worker. There is no partial render to roll back.

## 7. Risks and open questions

1. **For C6 and A25.** The embedded size of the pdf.js files is unmeasured; it should
   be recorded when they are vendored. Rarely used character maps could be left out,
   at the cost of some East Asian documents.
2. **For C6.** Module Workers under the page policy must be checked in each supported
   browser engine. There is no desktop shell and no system webview
   ([DH-ADR-18](../design.md#dh-adr-18)); the user's own browser is the only client.
   Area C6 lists what it confirms in a browser
   ([../areas/ui-sidebar-workspace.md](../areas/ui-sidebar-workspace.md) §10, item 4).
3. **For the toolchain area.** The vendoring tool and its checksum manifest are a new
   developer command; a check must fail when the directory differs from the manifest.
4. `office_render` for PDF input (model-facing page images) is a host-side operation
   and does not use this island; see [deepseek-ai-libreoffice-kit.md](deepseek-ai-libreoffice-kit.md).

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-PDFJSDIST-1` | local | `DH-LIB-PDFJSDIST-R3`, `-R5` | Every file under the vendored directory matches the checksum manifest; the static route serves each with the right content type and a long cache lifetime; the preview fragment references only same-origin addresses, and the response's content security policy names no other origin. |
| `DH-T-PDFJSDIST-2` | local | `DH-LIB-PDFJSDIST-R4` | A PDF larger than the host's file-read limit is refused by the document route with the too-large reason and the preview fragment shows that state; the fragment carries the page-bitmap cap (16,777,216) as data for the island. The island's use of the cap is checked in the browser scenario below. |

`DH-LIB-PDFJSDIST-R1` and `-R2` are browser behaviour and need a real-environment
scenario, owned by C6: Given the built binary and a committed three-page PDF with text;
When the preview opens in a real browser; Then three pages are drawn, a word can be
selected and copied, no canvas exceeds the bitmap cap at 400% zoom, no request leaves
the host's origin, and closing the preview leaves no Worker; Cleanup closes the browser. Local tests and real-environment
scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-pdfjs-dist`.
