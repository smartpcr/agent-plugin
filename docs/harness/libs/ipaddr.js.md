# ipaddr.js: Go specification

> **Status: design, not built.** Short form with the debt table, because the use is
> security-relevant. Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". No harness code exists.
>
> **Unverified research notes.** Where this document says "probe", it reports an
> observation of `net/netip` made during library research on 2026-11-18 by running a
> throwaway program on Go 1.27.4 outside this repository. That was against the project's
> rule that nothing is built or run in the design task, and the program was not kept, so
> the observation cannot be repeated from this repository. Every such statement is
> therefore **unverified and to be measured** by `DH-T-IPADDRJS-1` (section 8); if the
> test contradicts an observation, the choice is reopened.

| | |
|---|---|
| **Library ID** | `DH-LIB-IPADDRJS` |
| **Upstream unit** | `UP-DEP-ipaddr.js` |
| **Upstream version** | 2.5.0 |
| **Kind** | runtime, one package |
| **Used by** | host (`packages/host/webserver`) |
| **Go choice** | standard library `net/netip` |
| **Related areas** | web host and its authentication |

## 1. What it is and why upstream needs it

`ipaddr.js` parses IPv4 and IPv6 address text and classifies an address into a named
range such as loopback, private or unspecified.

Upstream uses it to decide what the web server's **bind address** means. The web UI can
run commands on the machine, so where it listens is a security decision
(`packages/host/webserver/src/index.ts:438`):

- The **unspecified address** (`0.0.0.0`, `::`, and IPv4-mapped spellings of the first)
  means "every network interface at once". It is refused when configuration loads.
- A **loopback address** (`127.0.0.0/8`, `::1`) keeps the server on this machine. Other
  code keys on that: a non-loopback listener over plain HTTP gets a warning
  (`packages/bundle/web-app/src/index.ts:207`).
- The address must also be turned into the host part of the URL that is printed and
  opened in the browser.

The hard part is that one address has many spellings, and the classification must agree
with what the operating system will actually bind.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/host/webserver/src/index.ts:243` | Parse a literal that Node's `net.isIP` accepted, after removing a `%zone` suffix; turn an IPv4-mapped IPv6 address into IPv4 | `ipaddr.parse`, `isIPv4MappedAddress`, `toIPv4Address` |
| `packages/host/webserver/src/index.ts:256` | "Is this the all-interfaces address?" by testing that every part is zero, because the library's own "unspecified" range is all of `0.0.0.0/8` | `octets`, `parts` |
| `packages/host/webserver/src/index.ts:276` | "Is this loopback?" | `range() === 'loopback'` |
| `packages/host/webserver/src/index.ts:290` | Canonical text of the address for a URL host | `toString()` |
| `packages/host/webserver/src/index.ts:305` | URL host form: brackets for IPv6; no URL form for a non-loopback address with a zone | none |

It is not used for the request-time `Host` header check; that code parses with the
platform `URL` class (`packages/client/connection/src/api-request-trust.ts:30`).

## 3. Tech debt upstream carries

Search performed: imports of `ipaddr.js`; `patches/` and `pnpm-workspace.yaml` (no
patch, pin or exemption); the web server source and README.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L7.4` | The library and the socket layer disagree about an IPv6 address written with a dotted tail: `::0.0.0.1` is the loopback address `::1` to `listen`, but version 2.5.0 reads it as an IPv4-mapped address. Upstream rewrites the text before parsing. The library's "unspecified" range is also wider than the one address that binds every interface, so upstream tests the parts itself. Two parsers (`net.isIP` and the library) must be kept in agreement by hand. | `packages/host/webserver/src/index.ts:217`, `:255` | Resolved by `DH-LIB-IPADDRJS`: `net/netip` is the same parser family the Go network stack uses to bind, so there is one reading. Probe results are in section 5. |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-IPADDRJS-R1` | The bind address must be one concrete IPv4 or IPv6 literal. A host name, an empty value, or a non-canonical spelling (`0x7f.0.0.1`, `127.1`) is refused at configuration load with a message that quotes the value. | `packages/host/webserver/src/index.ts:434` |
| `DH-LIB-IPADDRJS-R2` | The unspecified address in every spelling (`0.0.0.0`, `::`, `::ffff:0.0.0.0`, with or without a zone) is refused at load, with a message that says why. `0.0.0.1` is not the unspecified address. | `packages/host/webserver/src/index.ts:437` |
| `DH-LIB-IPADDRJS-R3` | Loopback is recognised by value: all of `127.0.0.0/8`, `::1`, IPv4-mapped forms, zoned forms, and `::0.0.0.1`; `::127.0.0.1` is not loopback. | `packages/host/webserver/README.md:39` |
| `DH-LIB-IPADDRJS-R4` | The classification is made on the same parsed value that is passed to the listener, so the two cannot disagree. | debt L7.4 |
| `DH-LIB-IPADDRJS-R5` | The address has a URL host form: canonical text, IPv4 for a mapped address, brackets for IPv6, zone dropped for loopback; a non-loopback zoned address has none and the operator must supply a public URL. | `packages/host/webserver/src/index.ts:305`; `packages/bundle/web-app/src/index.ts:201` |
| `DH-LIB-IPADDRJS-R6` | The request-time `Host` and `Origin` checks of [DH-ADR-WIRE-10](../upstream/wire-and-api.md#dh-adr-wire-10) use the same parser for IP literals and treat anything it cannot parse as a name to be matched exactly, never as an address. | `packages/client/connection/src/api-request-trust.ts:105`, `:138` |

## 5. Go choice

**Decision.** `net/netip` from the standard library. One function parses the configured
value with `netip.ParseAddr`, calls `Unmap()` and returns a small value type that holds
the address, its zone and the three answers (`IsUnspecified`, `IsLoopback`, URL host).
The listener is opened from that same value (`net.JoinHostPort` of its text), which is
`DH-LIB-IPADDRJS-R4`.

**Why.** Probe results reported on Go 1.27.4 (unverified, to be measured by
`DH-T-IPADDRJS-1`):

| Input | `netip` result | Matches upstream's intent |
|---|---|---|
| `::0.0.0.1` | `::1`, loopback | Yes, without the text rewrite |
| `::ffff:127.0.0.1` | unmaps to `127.0.0.1`, loopback | Yes |
| `::127.0.0.1` | `::7f00:1`, not loopback | Yes |
| `::ffff:0.0.0.0`, `0.0.0.0`, `::` | unspecified | Yes |
| `0.0.0.1` | not unspecified | Yes, without the "every part is zero" test |
| `::1%lo`, `fe80::1%eth0` | zone kept separately | Yes |
| `localhost`, `0x7f.0.0.1`, `127.1` | parse error | Yes: refused. Inference: a browser-style (WHATWG) URL parser rewrites the last two to `127.0.0.1` instead |

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `net/netip` | Value type, comparable, no allocation; strict parser; `Unmap`, `IsLoopback`, `IsUnspecified`, `IsPrivate`, `IsLinkLocalUnicast`, `Zone` | No named-range table beyond those methods; cloud metadata addresses and similar must be listed by the caller as `netip.Prefix` values | Go 1.27.4 standard library; probe 2026-11-18 (unverified, to be measured) |
| `net.ParseIP` (older API) | Also standard | No zone; returns a byte slice that cannot be compared with `==`; an IPv4 address and its IPv4-mapped form are indistinguishable after parsing | Go 1.27.4 standard library |

**Tradeoff.** Strictness: a spelling a browser would accept in a URL (`127.1`) is
refused in configuration. That is the safe direction for a bind address and for a
`Host` header, where a browser always sends the canonical form.

The same value type serves the destination guard of `DH-LIB-UNDICI-R9`
([undici.md](undici.md)), which needs "is this loopback, private, link-local or a
metadata address" for outbound connections.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-IPADDRJS-1` | local | `DH-LIB-IPADDRJS-R1`, `DH-LIB-IPADDRJS-R2`, `DH-LIB-IPADDRJS-R3` | Table test over the spellings in section 5 and upstream's README examples: each is accepted or refused, and classified, as listed. |
| `DH-T-IPADDRJS-2` | local | `DH-LIB-IPADDRJS-R4`, `DH-LIB-IPADDRJS-R5` | For each accepted loopback spelling, the listener opened from the parsed value reports a local address equal to the parsed address, and a request to the generated URL host reaches it. A zoned non-loopback address yields no URL and a clear error. |
| `DH-T-IPADDRJS-3` | local | `DH-LIB-IPADDRJS-R6` | `Host` values `127.0.0.1:port`, `[::1]:port`, `localhost:port` and the bind address pass; `0x7f.0.0.1`, `127.1`, a rebinding domain and an IPv6 literal without brackets are refused. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.
The real-browser rebinding scenario is owned by DH-ADR-WIRE-10.

Units dispositioned by this spec: `UP-DEP-ipaddr.js`, `UP-DEBT-found.L7.4`.
