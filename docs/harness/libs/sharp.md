# sharp: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go module facts were checked on 2026-11-18 with Go 1.27.4
> (`go list -m -json <module>@latest`, `go mod download -json`, the `LICENSE` file and the
> source in the module cache). No module code was compiled or run.

| | |
|---|---|
| **Library ID** | `DH-LIB-SHARP` |
| **Upstream unit** | `UP-DEP-sharp` |
| **Upstream version** | 0.35.5 |
| **Kind** | runtime, native (a Node binding to the C library libvips, one prebuilt package per platform) |
| **Used by** | attachment (`packages/attachment/attachment-local`) at run time; build scripts and tests in apps, experimental and spill |
| **Go choice** | standard library (`image`, `image/jpeg`, `image/png`, `image/gif`) plus the module `golang.org/x/image`, and a small amount of own code for metadata; no cgo |
| **Related areas** | A07 (attachment store; owns the pipeline), A16 (Office page images enter the same store), A25 (the web app manifest's icons; there are no desktop application icons, [DH-ADR-18](../design.md#dh-adr-18); [../areas/app-web.md](../areas/app-web.md)) |

## 1. What it is and why upstream needs it

When a person pastes a screenshot, or a tool returns one, the harness must turn an
arbitrary image file into something safe and cheap to send to a model: a known format,
upright, without private metadata, small enough in pixels and bytes. sharp is the Node
image library that does this, by calling libvips.

Upstream uses it in one runtime package, the local **attachment store**: the component
that keeps every attached image as a content-addressed file under the harness home.
An image is checked and **normalized** once when it is admitted, and a smaller
**request version** is derived and cached for each model route that needs one
(`packages/attachment/attachment-local/README.md:59`, `:78`).

Image decoding is an attack surface: the bytes come from the user, from web pages and
from tools, and a decoder bug or a "decompression bomb" (a tiny file that declares an
enormous picture) is a classic way to crash or exhaust a process. The limits below are
therefore part of the requirement, not an optimisation.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/attachment/attachment-local/src/sharp.ts:7` | Load the native library only at the first raster operation | lazy `require('sharp')` |
| `packages/attachment/attachment-local/src/image.ts:63-82` | Read format, size, frame count, bit depth, colour space, alpha and whether any metadata is present (EXIF, XMP, IPTC, ICC profile, comments, orientation). Only PNG, JPEG, WebP and GIF are accepted (`:45-50`). EXIF orientations 5 to 8 swap width and height so that limits use the size a person sees (`:71-75`) | `sharp(data, { failOn: 'error', limitInputPixels: false }).metadata()` |
| `packages/attachment/attachment-local/src/image.ts:92-100` | Header-only probe on later reads of a stored object, whose digest was already verified | `.metadata()` |
| `packages/attachment/attachment-local/src/image.ts:116-133` | Admission: check pixel count and side length from the header first, then decode the whole raster to prove the file is complete | `.raw().toBuffer()` |
| `packages/attachment/attachment-local/src/normalization.ts:74-84` | Normalize: apply EXIF orientation, convert to sRGB, scale down inside a box without enlarging | `.rotate().toColourspace('srgb').resize({ fit: 'inside', withoutEnlargement: true })` |
| `packages/attachment/attachment-local/src/normalization.ts:36-49` | Pass a file through byte for byte when it is already clean: not GIF, one frame, no metadata, 8-bit, sRGB, inside every limit | none |
| `packages/attachment/attachment-local/src/encoding.ts:6-38` | Encode: WebP when the source has an alpha channel, JPEG otherwise, trying quality 85, then 75, then 60, and keeping the first that fits the byte target or else the smallest | `.webp({ quality, effort: 0 })`, `.jpeg({ quality })`, `.clone()`, `.toBuffer({ resolveWithObject: true })` |
| `packages/attachment/attachment-local/src/request-image.ts:89-120` | Request version: scale by the long edge only, to the size the model route chose, with a separate byte target and the same encoder ladder | `.resize({ width \| height, withoutEnlargement: true })` |
| `packages/attachment/attachment-local/src/normalization.ts:52-71`, `request-image.ts:146-160` | Decode every encoder output again and compare it with the recorded facts | `detectImage` |
| `apps/web/scripts/render-app-icons.ts:145-152`, `apps/desktop/scripts/render-tray-icon.ts:108`, `packages/experimental/cua-macos/native/generate_icon.mjs:20-21` | Build scripts: rasterise SVG icons to PNG | `sharp(svg, { density }).resize().png()` |
| Tests in `apps/web`, `apps/desktop`, `packages/experimental/cua*`, `packages/spill/spill-policy` (for example `apps/web/tests/menu-material.e2e.ts:180`, `packages/spill/spill-policy/tests/multimodal-recovery.spec.ts:85`) | Make test images and read pixel statistics from screenshots | `sharp({ create })`, `.stats()`, `.raw()` |

Limits upstream applies (`packages/attachment/attachment-local/README.md:41-49`): 50 MiB
per source file, 20 images and 200 MiB per message, 64,000,000 pixels and 65,535 pixels
per side at admission; the normalized image fits 2048 × 2048 pixels in total, 8192 on the
long edge and aims at 4 MiB; two transforms run at once. sharp's own pixel limit is
switched off (`limitInputPixels: false`) and replaced by these checks.

## 3. Tech debt upstream carries

| Debt | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| Native decoder in the host | libvips and its codec libraries parse untrusted bytes as native code in the host process | `packages/attachment/attachment-local/src/image.ts:119-127` | Removed: the Go decoders are memory-safe Go code. A malformed file can cost time and memory inside the stated limits, and can cause a panic, which is recovered and reported as an invalid image. |
| Output depends on the installed native build | "The installed Sharp/libvips build pins normalization and request bytes" | `packages/attachment/attachment-local/README.md:140` | Reduced: the encoders are Go code pinned by `go.mod` and the Go toolchain version, with no platform-specific native build. A toolchain upgrade can still change bytes, so the transform version stays in the cache identity (`request-image.ts:28`). |
| Lazy CommonJS loading and per-platform packages | The binding must be loaded lazily and shipped per platform | `packages/attachment/attachment-local/src/sharp.ts:7` | Not applicable: no native package. |
| Animated GIF becomes static | Only the first frame is kept | `packages/attachment/attachment-local/README.md:139` | Narrowed by area A07: the image the model and the transcript see is the first frame (`DH-LIB-SHARP-R7`), and the original bytes are kept beside it as a linked file, so the animation is not lost ([../areas/storage.md](../areas/storage.md)). This README bullet is an inventory unit of area A07, not of this spec. |

No patch, pin or install-script exemption names sharp (`patches/` and
`pnpm-workspace.yaml` searched).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-SHARP-R1` | Only PNG, JPEG, WebP and GIF are accepted, identified from the bytes, never from a file name or a declared type. | `image.ts:45-50`, `:63-68` |
| `DH-LIB-SHARP-R2` | **Limits before pixels.** The byte limit is checked before parsing; width, height and pixel count are read from the header and checked before any pixel buffer is allocated. | `image.ts:116-126`, `README.md:41-45` |
| `DH-LIB-SHARP-R3` | Admission decodes the whole image; a truncated or corrupt file is refused with the stable code `INVALID_IMAGE`. A decoder panic is the same refusal, never a host crash. | `image.ts:127-132` |
| `DH-LIB-SHARP-R4` | Decoded-pixel memory in use at one time is bounded across all sessions, by pixel count and not only by job count. | `README.md:49`; Inference: Go decoders hold the whole raster in memory, where libvips streams |
| `DH-LIB-SHARP-R5` | The stored image is upright (EXIF orientation applied), carries no metadata or colour profile, is 8 bits per channel in sRGB, and keeps transparency when the source had it. | `normalization.ts:74-84`, `README.md:59` |
| `DH-LIB-SHARP-R6` | Scaling never enlarges, keeps the aspect ratio, and follows upstream's two rules: a total-pixel budget then a long-edge cap for normalization; long edge only for request versions. | `normalization.ts:87-96`, `request-image.ts:88-93` |
| `DH-LIB-SHARP-R7` | A clean file inside every limit is stored byte for byte. GIF, more than one frame, metadata, 16-bit samples and any colour space other than 8-bit RGB force re-encoding. Only the first frame of an animation becomes the stored image; area A07 keeps the original bytes as a linked file ([../areas/storage.md](../areas/storage.md)). | `normalization.ts:36-49`, `README.md:88`, `:139` |
| `DH-LIB-SHARP-R8` | Encoding tries candidates in a fixed order, takes the first that meets the byte target and otherwise keeps the smallest; provider byte caps are enforced where the bytes are sent. | `encoding.ts:56-72`, `normalization.ts:100-103` |
| `DH-LIB-SHARP-R9` | Every encoder output is decoded again and must match the recorded format, size, depth and alpha fact, or the write fails. | `normalization.ts:52-71` |
| `DH-LIB-SHARP-R10` | The same source and policy give the same bytes on every platform for one harness version, so a cached request version is reused and the model's prompt cache is not disturbed. | `README.md:128` |

## 5. Go choice

**Decision.** Pure Go. Decode with `image/png`, `image/jpeg`, `image/gif` and
`golang.org/x/image/webp`; scale with `golang.org/x/image/draw` (Catmull-Rom kernel);
encode opaque images with `image/jpeg` and images with transparency with `image/png`.
EXIF orientation, metadata detection and colour-profile handling are own code.

<a id="dh-adr-sharp-1"></a>
### DH-ADR-SHARP-1 A pure-Go image pipeline; transparent images are stored as PNG

- **Choice.** No libvips and no cgo. Opaque images keep upstream's JPEG ladder (85, 75,
  60). An image with an alpha channel is encoded as PNG, not as lossy WebP.
- **Why.** `DH-LIB-SHARP-R2` to `-R4` are easiest to guarantee with memory-safe decoders
  in one process, and the release plan assumes `CGO_ENABLED=0`. The standard library and
  `golang.org/x/image` can decode all four formats and encode JPEG and PNG, but
  `golang.org/x/image/webp` only decodes (the package has `decode.go` and no encoder).
- **Tradeoff.** PNG is lossless, so a photograph with transparency is larger than
  upstream's WebP and may miss the byte target; the existing "keep the smallest, enforce
  the provider cap at the route" rule then applies (`DH-LIB-SHARP-R8`). Inference: most
  transparent attachments are screenshots and interface graphics, which PNG compresses
  well. Colour management is narrower than libvips (see §6). Scaling is slower than
  libvips.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-7](../design.md#dh-o-7),
  [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-SHARP-1` to `DH-T-SHARP-6`.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** standard library + `golang.org/x/image` | No cgo, no native install, memory-safe decoders, header-only `DecodeConfig` for limits, same bytes on every platform | No lossy WebP encoder; no ICC transform; whole raster in memory | `golang.org/x/image` v0.48.0, 2026-11-09, BSD-3-Clause; `webp/decode.go` (`Decode`, `DecodeConfig`; an animation flag is defined at `:164` but no frame decoding exists), `draw/scale.go:177-183` (Catmull-Rom); `go list -m`, `LICENSE` and source read, 2026-11-18 |
| Add a pure-Go lossy WebP encoder: `github.com/gen2brain/webp` | Restores upstream's WebP for transparent images, with quality and lossless options (`webp.go:46-51`), without cgo | The encoder is libwebp compiled to WebAssembly and transpiled to one generated Go file. By default it first tries to load a system libwebp through `purego`; the build tag `nodynamic` must be set to forbid that (`README.md:9-13`, `webp_dynamic.go:1`) | v0.6.4, 2026-07-01, MIT plus libwebp's BSD licence (`lib/LICENSE.libwebp`); `go list -m`, source read, 2026-11-18 |
| Lossless WebP: `github.com/HugoSmits86/nativewebp` | Small pure-Go encoder | Lossless only (`README.md:9`), so no size gain that justifies a second format | v1.3.0, 2026-05-10, MIT |
| cgo binding to libvips: `github.com/davidbyttow/govips/v2` | Parity with upstream, including ICC transforms and streaming | cgo, a libvips install or bundle on every platform (LGPL), native parsing of untrusted bytes in the host, different bytes per libvips build | v2.20.0, 2026-10-16, MIT (the binding); `go list -m`, `LICENSE`, 2026-11-18 |

**Tradeoff.** Stated in the decision. The second row is the upgrade path if measured
PNG sizes prove a problem; it changes one encoder call and the transform version.

**Upstream decisions judged** ([DH-ADR-2](../design.md#dh-adr-2)).

| Upstream decision | Verdict | Why |
|---|---|---|
| Normalize once at admission, derive request versions per route (`README.md:78`) | **Keep** | Independent of the library; it keeps durable history stable. |
| The library's own pixel limit is off and the store's limits are checked from the header (`image.ts:119-126`) | **Keep** | Go has no built-in limit at all, so the header check is the only one. |
| Full decode at admission, header probe on later reads (`image.ts:84-100`) | **Keep** | The digest proves the bytes are the ones already decoded once. |
| Quality ladder 85, 75, 60 and "smallest kept" (`encoding.ts:6`, `:56-72`) | **Keep** for JPEG | Same behaviour with `image/jpeg`. |
| Lossy WebP for images with transparency (`encoding.ts:33-38`) | **Change** to PNG | No encoder in the chosen packages; see DH-ADR-SHARP-1. |
| First frame of any animation is kept (`README.md:139`) | **Change** for animated WebP only | Refused until a first-frame extractor exists; GIF behaves as upstream. |
| Every encoder output is decoded again and verified (`normalization.ts:52-71`) | **Keep** | Cheap, and it catches an encoder or toolchain regression before a bad object becomes durable. |

## 6. Design notes

The pipeline is harness code in the attachment area (A07). No deliverable is declared
here; A07 owns it.

**Limits on hostile input, in the order they apply.**

1. Source bytes are capped before the file is read into memory (50 MiB by default).
2. The format is sniffed from the first bytes; anything else is refused.
3. `image.DecodeConfig` reads only the header and returns width, height and colour
   model. Pixel count and side length are checked here (64,000,000 and 65,535 by
   default). Nothing proportional to the picture has been allocated yet.
4. A weighted limiter admits the decode by `width × height` (`DH-LIB-SHARP-R4`).
   Inference: an 8-bit decode needs about 4 bytes per pixel and a 16-bit PNG 8 bytes,
   so the default pixel limit means up to about 512 MB for one image; A07 should size
   the limiter from that and may lower the default.
5. The decode runs inside a `recover`, and its result is discarded if the request was
   cancelled. A Go decoder cannot be interrupted part-way; the pixel limit is what
   bounds its time.
6. A GIF is decoded for its first frame only, never with `gif.DecodeAll`, so a file
   with thousands of frames costs one frame.
7. Metadata blocks are skipped by length while scanning, never copied.

**Own code needed** (small, bounded parsers, all in the attachment package):

- *Orientation.* Read EXIF tag 0x0112 from a JPEG `APP1` segment, a PNG `eXIf` chunk or
  a WebP `EXIF` chunk, and apply one of eight flips or rotations.
- *Metadata presence.* Walk JPEG segments, PNG chunks and the WebP header to decide
  whether a file is "clean" for pass-through (`DH-LIB-SHARP-R7`).
- *Colour.* 16-bit samples are reduced to 8 bits; grey and CMYK are converted with the
  standard formulas. An embedded ICC profile that is a simple RGB matrix profile
  (Display P3, Adobe RGB, ProPhoto) is converted to sRGB; any other profile is dropped
  without conversion and the stored reference records a colour-accuracy note. The
  module `github.com/mandykoh/prism` (v0.35.3, 2024-06-27, MIT, checked the same way)
  implements these conversions and can be used instead of own code; it has had no
  release since 2024, so it is recorded as an alternative, not a dependency.
- *Animated WebP.* Inference: `golang.org/x/image/webp` rejects it. Upstream keeps the
  first frame; the Go design refuses the file with `INVALID_IMAGE` and a message that
  says why, until a first-frame extractor is written. This is a behaviour change.

**Failure and cleanup.** The pipeline owns no long-lived resource: no process, no
handle, no watcher. A failed or cancelled transform leaves nothing behind except a
possible temporary file in the store's staging directory, which the store's own
publish-by-link step and start-up sweep handle (A07). The request-version cache is a
created resource owned by the attachment component; it can be deleted at any time and
is rebuilt on demand (`packages/attachment/attachment-local/README.md:90`).

**Build and test uses.** The three icon scripts rasterise SVG at build time. Inference:
the Go build has no SVG rasteriser and should not gain one; application icons are
committed PNG and ICO files, regenerated by a developer tool when the artwork changes
(a note for area A25, which owns the web app manifest's icons; there are no desktop
application icons, [DH-ADR-18](../design.md#dh-adr-18)). Tests build their images with
the `image` package.

## 7. Risks and open questions

1. **For A07.** Whether PNG for transparent images keeps typical attachments under the
   4 MiB target must be measured on real screenshots before the encoder choice is
   final. The fallback is the `gen2brain/webp` row above.
2. **For A07.** Wide-gamut screenshots (every recent macOS screenshot carries a display
   profile) depend on the own colour conversion; if it is deferred, colours shift
   slightly and the reference must say so.
3. **Decode memory** is higher than upstream's for very large images; see limit 4.
4. Scaling quality differs from libvips' default kernel, so upstream's pixel-exact test
   expectations cannot be reused ([DH-ADR-2](../design.md#dh-adr-2)).

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-SHARP-1` | local | `DH-LIB-SHARP-R1`, `-R2` | Hand-built headers that declare 100,000 × 100,000 pixels in each format are refused with the pixel or dimension code, and an allocation counter shows no raster was allocated. A renamed text file is refused. |
| `DH-T-SHARP-2` | local | `DH-LIB-SHARP-R3` | Truncated and bit-flipped files of each format give `INVALID_IMAGE`; a fuzz target over the admission function never panics out of it. |
| `DH-T-SHARP-3` | local | `DH-LIB-SHARP-R5`, `-R7` | The eight EXIF orientations produce the same upright picture; a file with a profile, a comment or 16-bit samples is re-encoded without them; a clean file comes back byte for byte; an animated GIF yields its first frame. |
| `DH-T-SHARP-4` | local | `DH-LIB-SHARP-R6`, `-R8`, `-R9` | Table of sizes and budgets: output dimensions, the chosen quality step, "smallest kept" when nothing fits, transparency preserved, and a deliberately wrong recorded fact fails verification. |
| `DH-T-SHARP-5` | local | `DH-LIB-SHARP-R10` | Golden digests of normalized and request outputs for a fixed set of sources, compared on every platform in CI; the golden file is keyed by the transform version. |
| `DH-T-SHARP-6` | local | `DH-LIB-SHARP-R4` | With the limiter sized for one large image, a second large decode waits and a cancelled waiter leaves the queue. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-sharp`.
