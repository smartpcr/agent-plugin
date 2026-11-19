# negotiator: Go specification

> **Status: design, not built.** Short form. Upstream facts were read at the pinned commit
> recorded in [../README.md](../README.md). Statements that are reasoning and not read
> from source open with "Inference". The design it belongs to is in
> [compression.md](compression.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-NEGOTIATOR` |
| **Upstream unit** | `UP-DEP-negotiator` |
| **Upstream version** | 1.1.0 |
| **Kind** | runtime, one package |
| **Used by** | host (`packages/host/webserver`) |
| **Go choice** | no separate library: the negotiation inside `github.com/klauspost/compress/gzhttp`, the module chosen in [compression.md](compression.md) |
| **Related areas** | web host |

## 1. What it is and why upstream needs it

Content negotiation is how an HTTP server picks one representation from what the client
says it accepts, using headers such as `Accept`, `Accept-Language` and `Accept-Encoding`,
each a list with optional quality values (`gzip;q=0.5, br`). `negotiator` parses those
headers and ranks the choices.

Upstream uses it for one header and one question: "between `gzip` and `identity` (no
encoding), which does this client prefer?" The answer is written back into a copy of
the request so that the compression middleware can only ever pick gzip, never another
encoding the client also listed (`packages/host/webserver/src/index.ts:334`). It is not
used for media types or languages, and it is not part of any access check.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/host/webserver/src/index.ts:334` | Choose `gzip` or `identity` from `Accept-Encoding`, honouring quality values | `new Negotiator(req).encoding(['gzip', 'identity'])` |
| `packages/host/webserver/src/index.ts:335` | Hand the middleware a request whose `Accept-Encoding` is exactly that one value | none |

No patch, pin or exemption exists (`patches/`, `pnpm-workspace.yaml` searched), and no
README limitation names it.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-NEGOTIATOR-R1` | The host compresses a response with gzip only when the request's `Accept-Encoding` ranks gzip above "no encoding"; `gzip;q=0` and a missing header both mean no compression. | `packages/host/webserver/src/index.ts:334` |
| `DH-LIB-NEGOTIATOR-R2` | No encoding other than gzip is ever selected for a response the host compresses itself, whatever the client lists. | `packages/host/webserver/src/index.ts:337` |
| `DH-LIB-NEGOTIATOR-R3` | Whether a request wants a whole page or a fragment is decided by the `HX-Request` header that htmx sends, not by `Accept` negotiation. | Inference from DH-ADR-3 and [DH-ADR-WIRE-1](../upstream/wire-and-api.md#dh-adr-wire-1) |

## 5. Go choice

**Decision.** No negotiation library and no own parser. `gzhttp` parses
`Accept-Encoding` with quality values itself; configuring it with zstd disabled gives
`DH-LIB-NEGOTIATOR-R2` without rewriting the request.

**Why.** The only question upstream asks of `negotiator` is answered inside the handler
that needs the answer. The standard library has no `Accept-Encoding` parser
(`mime.ParseMediaType` parses a single media type, not a ranked list), so the choice is
between the chosen module's built-in parser and writing one.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** negotiation inside `gzhttp` | Nothing extra to import or write | Tied to the compression choice | `github.com/klauspost/compress` v1.20.4, 2026-11-09, BSD 3-Clause with Apache-2.0 for `gzhttp/*`; `gzhttp/compress.go` read in the module cache, 2026-11-18 (quality values at `:73`, encoding selection at `:640`) |
| Own parser for `Accept-Encoding` | No dependency on the compression module | About 40 lines plus tests, needed only if the own-handler fallback in [compression.md](compression.md) is taken | n/a |

**Tradeoff.** If a later feature needs real media-type or language negotiation, it will
need its own parser; nothing in upstream needs one today.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-COMPRESSION-1` (defined in [compression.md](compression.md)) | local | `DH-LIB-NEGOTIATOR-R1`, `DH-LIB-NEGOTIATOR-R2` | The `Accept-Encoding` table: `gzip`, `gzip;q=0`, `identity`, `br` alone, `br, gzip`, `zstd, gzip`, and no header. Only gzip is ever produced. |
| `DH-T-NEGOTIATOR-1` | local | `DH-LIB-NEGOTIATOR-R3` | The same route returns a full document without `HX-Request` and only the fragment with it, and sets `Vary: HX-Request` so a cache cannot mix them. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

Units dispositioned by this spec: `UP-DEP-negotiator`.
