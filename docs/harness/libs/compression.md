# compression: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go module facts were checked on 2026-11-18.

| | |
|---|---|
| **Library ID** | `DH-LIB-COMPRESSION` |
| **Upstream unit** | `UP-DEP-compression` (and `UP-DEP-negotiator`, see [negotiator.md](negotiator.md)) |
| **Upstream version** | 1.8.2 |
| **Kind** | runtime, one package |
| **Used by** | host (`packages/host/webserver`) |
| **Go choice** | named Go module `github.com/klauspost/compress`, package `gzhttp` |
| **Related areas** | web host |

## 1. What it is and why upstream needs it

`compression` is the Express middleware that gzip-compresses HTTP responses when the
browser says it accepts gzip. Upstream's web server is not Express, but the middleware
only touches the plain Node request and response, so the server calls it directly
(`packages/host/webserver/src/index.ts:314`).

It exists because upstream's browser client is several megabytes of JavaScript and its
unary API returns large JSON documents. Compression is **off by default** and the shipped
web profile turns it on at the fastest level with a 1,024-byte threshold
(`packages/host/webserver/README.md:41`).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/host/webserver/src/index.ts:316` | Build one middleware from config: level 0 to 9 (default 1), threshold in bytes (default 1,024) | `compression({ level, threshold, filter })` |
| `packages/host/webserver/src/index.ts:319` | Decide per response: never a range response (`Content-Range`), never `text/event-stream`, always `multipart/form-data`, otherwise the library's "is this type compressible" table | `filter`, `compression.filter` |
| `packages/host/webserver/src/index.ts:334` | Reduce the request's `Accept-Encoding` to `gzip` or `identity` before the middleware sees it, so no other encoding is ever chosen | [negotiator.md](negotiator.md) |
| `packages/host/webserver/src/index.ts:330` | Skip responses that have no socket (the in-browser worker tunnel) | none |
| `packages/host/webserver/src/index.ts:399` | Work around the middleware forwarding `on('drain')` to its compressor but not `off` | one listener pair per response |

Behaviour the README promises (`packages/host/webserver/README.md:41`): the client must
accept gzip; a known length under the threshold is not compressed; a stream of unknown
length is; an existing `Content-Encoding`, `Cache-Control: no-transform`, range
responses, the event stream, ZIP files and the pre-compressed worker image are left
alone.

## 3. Tech debt upstream carries

Search performed: imports of `compression`; `patches/` and `pnpm-workspace.yaml` (no
patch, pin or exemption); the web server source and README. No debt unit is assigned or
defined. Two workarounds are visible and neither survives: the request-header rewrite
(the Go handler is configured for gzip only) and the drain-listener note (`:399`; Go
writers block instead of emitting events).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-COMPRESSION-R1` | Response compression is off unless configured. When on, it is gzip only, with a configurable level and minimum size. | `packages/host/webserver/src/index.ts:448` |
| `DH-LIB-COMPRESSION-R2` | A response is compressed only if the request accepts gzip (honouring `q=0`), and the response then says so (`Content-Encoding`, `Vary: Accept-Encoding`). | `packages/host/webserver/src/index.ts:334` |
| `DH-LIB-COMPRESSION-R3` | Never compressed: the server-sent-events stream, range responses, responses that already have an encoding, responses marked `Cache-Control: no-transform`, already-compressed media and archives, and upgraded connections. | `packages/host/webserver/src/index.ts:319`; `packages/host/webserver/README.md:41` |
| `DH-LIB-COMPRESSION-R4` | A handler that flushes still delivers its bytes promptly through the compressor, and a handler that hijacks the connection still can. | `packages/host/webserver/src/index.ts:391` |
| `DH-LIB-COMPRESSION-R5` | Static assets shipped inside the binary can be served from a copy compressed at build time, with no work per request. | Inference from DH-ADR-3: assets are embedded files |

## 5. Go choice

<a id="dh-adr-compression-1"></a>
### DH-ADR-COMPRESSION-1 Opt-in gzip by `gzhttp`; the event stream is never compressed

- **Decision.** Keep upstream's contract: off by default, gzip only, level and threshold
  configurable. Implement it by wrapping the host's handler with
  `github.com/klauspost/compress/gzhttp`, configured with zstd disabled, a content-type
  filter that excludes `text/event-stream`, and a small harness handler in front that
  marks `no-transform` responses as not to be compressed. The event stream
  (`GET /events`) and the terminal WebSocket are mounted outside the wrapper as well, so
  the exclusion does not depend on a header being set in time.
- **Why the event stream is excluded.** (1) A compressor holds bytes until it is
  flushed, so every event would need a flush, and many small flushed blocks compress
  poorly. (2) The stream carries secrets-adjacent content (model output, file contents)
  mixed with text an attacker can influence (a fetched page); compressing the two
  together over TLS is the condition for the BREACH class of length side channels. (3)
  The default listener is loopback, where compression costs CPU and saves nothing.
  Upstream excludes it too (`packages/host/webserver/src/index.ts:322`).
- **Why a module.** The standard library has the gzip encoder (`compress/gzip`) but no
  response-compression handler and no `Accept-Encoding` negotiation. Writing one
  correctly means handling quality values, buffering up to the threshold before
  deciding, removing `Content-Length`, setting `Vary`, leaving `HEAD` and empty bodies
  alone, flush and hijack. `gzhttp` handles these cases (read in `gzhttp/compress.go`;
  an empty body stays under the threshold and is never encoded). The module is also the
  one the persistence research named for Zstandard, so it is likely in the build already.
- **Tradeoff.** `gzhttp` does not know `Cache-Control: no-transform` and does not exclude
  event streams by default (its default filter excludes only audio, video and archive
  types, `gzhttp/compress.go:1246`); the harness adds both. A deployment behind a
  reverse proxy should compress there and leave this off.
- **Serves.** [DH-O-12](../design.md#dh-o-12); DH-ADR-3, [DH-ADR-WIRE-2](../upstream/wire-and-api.md#dh-adr-wire-2).
- **Verified by.** `DH-T-COMPRESSION-1` to `DH-T-COMPRESSION-3`.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `github.com/klauspost/compress/gzhttp` | Negotiation with quality values, minimum size (default 1,024), level, content-type filter, skips existing `Content-Encoding` and `Content-Range`, implements `Flush` and `Hijack`; no dependencies outside its own module | Must be told about `no-transform` and event streams; enables zstd unless disabled | v1.20.4, 2026-11-09; `LICENSE` in the module cache: BSD 3-Clause for the module, Apache-2.0 for `gzhttp/*`; `go list -m -json`, `go mod download -json`, `go.mod` (no requirements) and `gzhttp/compress.go` read, 2026-11-18; no cgo |
| Own handler on `compress/gzip` | No module | About 200 lines of response-writer wrapping with well-known pitfalls, for a feature that is off by default | Go 1.27.4 standard library |
| No runtime compression; only build-time compressed assets | Nothing in the request path | Dynamic HTML on a remote, directly exposed listener is sent uncompressed | n/a |

## 6. Design notes

The web host builds its handler chain once at start: source check, authentication,
`no-transform` marker, `gzhttp` wrapper, router. With `compression: none` the wrapper is
not installed at all. Changing the setting rebuilds the listener's handler, which the
web host does on reload like any other config change.

Build-time assets (R5): the asset build step writes a `.gz` beside each compressible
embedded file, and the static handler serves it with `Content-Encoding: gzip` when the
request accepts gzip. This needs no module and works with compression "off", because
nothing is compressed at run time. It belongs to the UI area and is listed in section 7.

Failure and ownership: the wrapper holds a pooled gzip writer for the life of one
response and returns it when the handler returns. A client that disconnects mid-response
ends the write with an error like any other response. There is nothing to clean up on
fiber unload.

Verdict on upstream: **keep** the contract, **drop** the Express middleware, the
header-rewriting request proxy and the worker-tunnel special case (there is no
in-browser host in the Go design).

## 7. Risks and open questions

- If the persistence spec does not adopt `klauspost/compress`, this would be the only
  user; the fallback is the own handler in the options table.
- Build-time compressed assets need an owner in the UI area.
- BREACH also applies to compressed HTML pages that echo request input beside a secret.
  The UI area should keep secrets (tokens) out of response bodies; the cookie is already
  `HttpOnly`.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-COMPRESSION-1` | local | `DH-LIB-COMPRESSION-R1`, `DH-LIB-COMPRESSION-R2` | With compression off no response is encoded. With it on: `Accept-Encoding: gzip` gets gzip and `Vary`; `gzip;q=0`, `identity`, and `br` alone get the plain body; a body under the threshold is plain. |
| `DH-T-COMPRESSION-2` | local | `DH-LIB-COMPRESSION-R3` | Table test: event stream, range response, pre-encoded response, `no-transform`, a ZIP download and a WebSocket upgrade all pass through unchanged with compression on. |
| `DH-T-COMPRESSION-3` | local | `DH-LIB-COMPRESSION-R4`, `DH-LIB-COMPRESSION-R5` | A handler that writes and flushes twice is received as two readable chunks before it returns; an embedded asset with a `.gz` sibling is served encoded to a gzip client and plain otherwise. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-compression`. (`UP-DEP-negotiator` is dispositioned by [negotiator.md](negotiator.md).)
