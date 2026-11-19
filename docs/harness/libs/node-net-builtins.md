# Node network built-ins (`node:http`, `node:https`, `node:net`, `node:tls`, `node:dns`, `node:url`): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go facts were checked on 2026-11-18 by reading the Go 1.27.4
> source; no harness code exists.
>
> **Unverified research note.** The one statement marked "probe" (section 5, the cipher
> suite count) reports an observation made during library research by running a
> throwaway program outside this repository. That was against the project's rule that
> nothing is built or run in the design task, and the program was not kept. It is
> **unverified and to be measured** by `DH-T-NODENET-4`; if the test contradicts it,
> the decision is reopened.

| | |
|---|---|
| **Library ID** | `DH-LIB-NODENET` |
| **Upstream unit** | `UP-DEP-node:http`, `UP-DEP-node:https`, `UP-DEP-node:net`, `UP-DEP-node:tls`, `UP-DEP-node:dns`, `UP-DEP-node:url` |
| **Upstream version** | the Node runtime's own modules |
| **Kind** | Node built-in |
| **Used by** | almost every group; see section 2 |
| **Go choice** | standard library: `net/http`, `crypto/tls`, `net`, `net/netip`, `net/url`, `os`; small own helpers where the standard library stops (section 5) |
| **Related areas** | web host, SSH, subprocess, computer use, language servers, webhooks |

## 1. What it is and why upstream needs it

These six modules are Node's networking layer: an HTTP server and client (`node:http`,
`node:https`), raw stream sockets (`node:net`), TLS (`node:tls`), name resolution
(`node:dns`) and URL helpers (`node:url`). Upstream product code uses them for five
jobs:

1. **Listening.** The web server that carries the UI, plus three small private
   listeners (inspector, speech recognition, webhook).
2. **Local streams.** Talking to helper processes over Unix sockets, Windows named
   pipes and an inherited file descriptor.
3. **Securing a forwarded stream.** The SSH feature authenticates each forwarded stream
   with TLS using a pre-shared key.
4. **Validating header text** that came from configuration, before it is sent.
5. **Turning file paths into `file:` URLs and back.** Mostly because JavaScript modules
   identify themselves by URL, and in a few places because a protocol wants a URL.

Outbound HTTP requests are not made with these modules (they use `fetch`); that side is
[undici.md](undici.md). The one exception is telemetry, [got.md](got.md).

## 2. How upstream uses it

Product source only; the many test files that start a throwaway server are not listed.

| Module | Use site (`path:line`) | What it is used for | API used |
|---|---|---|---|
| `node:http`, `node:https` | `packages/host/webserver/src/index.ts:672`, `:681` | The UI listener, plain or TLS, on one address; upgrade handling; forced close on unload | `createServer`, `'upgrade'`, `listen`, `close`, `closeAllConnections` |
| `node:http` | `packages/experimental/inspector/src/worker/bridge/endpoint.ts:260` | A private listener for DevTools sockets | `createServer`, `'upgrade'` |
| `node:http` | `packages/experimental/speech-to-text-sensevoice/src/process-server.ts:17`, `:51` | A loopback listener on an OS-assigned port, guarded by a per-process bearer token | `createServer`, `listen(0, '127.0.0.1')` |
| `node:http` | `packages/experimental/webhook-github/src/handler.ts:4`, `packages/host/open-in-app/src/index.ts:19`, `packages/client/connection/src/http-bridge.ts:6` | Request and response types for route handlers | types only |
| `node:http` | `packages/acp/acp/src/mcp.ts:89`, `packages/host/product-telemetry-otel/src/index.ts:74` | Reject a configured header name or value that is not legal HTTP | `validateHeaderName`, `validateHeaderValue` |
| `node:https` | `packages/telemetry/otel/src/event-transport.ts:2` | Agent type for the telemetry client | type only |
| `node:net` | `packages/host/webserver/src/index.ts:244`, `:310` | "Is this text an IP literal, and which family" | `isIP` |
| `node:net` | `packages/ssh/ssh/src/index.ts:174` | Connect to a local Unix socket that OpenSSH forwards, keeping each direction open independently | `createConnection({ path, allowHalfOpen })` |
| `node:net` | `packages/experimental/cua-windows/src/native-transport.ts:30`, `packages/experimental/cua-macos/src/shared-native.ts:192` | Connect to a desktop helper over a Windows named pipe or a Unix socket | `new Socket().connect(path)` |
| `node:net` | `packages/subprocess/subprocess/src/control.ts:23` | In a child process, open the control channel inherited as descriptor 7 | `new Socket({ fd: 7 })` |
| `node:net` | `packages/experimental/inspector/src/host/plugin.ts:149` | Tunnel an admitted upgrade to the private inspector listener by writing the HTTP upgrade request by hand | `connect(port, host)`, `pipe` |
| `node:tls` | `packages/ssh/ssh/src/stream-security.ts:20`, `packages/ssh/ssh/src/helper-processes.ts:424` | Both ends of a forwarded stream prove they hold a random 256-bit key that travelled only over the administrative channel; TLS 1.2, one cipher, no certificates | `connect({ pskCallback })`, `createServer({ pskCallback })`, cipher `PSK-AES256-GCM-SHA384` |
| `node:dns` | `packages/experimental/webworker-runtime/src/node/builtin_modules/mock/dns/promises.ts:11` | A stub that fails, for the browser-worker preview | none |
| `node:url` | 118 files; outside tests and scripts, 116 lines call it, of which 40 pass `import.meta.url` directly and most of the rest serve module resolution (for example `packages/boot/app-boot/src/profile-resolution/resolver.ts:555`, `packages/boot/hmr/src/index.ts:344`) | Find a module's own directory, or build a module specifier | `fileURLToPath`, `pathToFileURL` |
| `node:url` | `packages/lsp/tool-lsp/src/render.ts:171` | Turn a `file:` URL from a language server into a path **for a stated target system** (Windows or POSIX), refusing malformed input | `fileURLToPath(url, { windows })` |
| `node:url` | `packages/fs/fs-local/src/index.ts:164` | The `file:` URL of a path | `pathToFileURL` |

## 3. Tech debt upstream carries

Search performed: every `node:http`, `node:https`, `node:net`, `node:tls`, `node:dns`
and `node:url` import in `apps`, `packages`, `scripts`, `vendor` and `website`; the web
server, SSH and subprocess READMEs. No tech-debt unit is assigned to this spec and it
defines none. Two workarounds for Node behaviour are visible and are answered in
section 5: the server must track upgraded sockets and unfinished TLS handshakes itself
because `closeAllConnections()` misses them (`packages/host/webserver/src/index.ts:755`),
and an empty certificate file is accepted silently by `node:tls`
(`packages/host/webserver/src/index.ts:345`). Inventoried limitations of the web server
(`UP-DEBT-limit.packages.host.webserver.1` to `.3`) belong to the area agent.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-NODENET-R1` | A listener binds one concrete address and port (0 means "assign one", and the assigned port can be read back). A bind failure or unusable TLS material fails start-up with the cause; there is never a fall back from HTTPS to HTTP. | `packages/host/webserver/README.md:45`, `:81` |
| `DH-LIB-NODENET-R2` | Every listener limits how long a client may take to send its headers and how large a request body may be. A streaming response is exempt from a write time limit but ends when its owner or client goes away. | Inference: Go sets no such limits by default (section 5); `packages/client/connection/src/http-bridge.ts:15` for the body limit |
| `DH-LIB-NODENET-R3` | Unloading a listener closes the listening socket, idle connections, connections with a request in progress, upgraded sockets and open streams, and returns only after all are closed. | `packages/host/webserver/src/index.ts:758` |
| `DH-LIB-NODENET-R4` | A failure while handling one request ends that request or connection and is logged; it never ends the process. | `packages/host/webserver/src/index.ts:648` |
| `DH-LIB-NODENET-R5` | The host can connect to a local helper over a Unix domain socket on every system and over a named pipe on Windows, with a connect time limit, cancellation, and closing of one direction at a time. | `packages/ssh/ssh/src/index.ts:174`; `packages/experimental/cua-windows/src/native-transport.ts:30` |
| `DH-LIB-NODENET-R6` | A child process can be handed one extra byte channel, beyond standard input and output, that only it and the host hold, on Unix and on Windows. | `packages/subprocess/subprocess/src/control.ts:23` |
| `DH-LIB-NODENET-R7` | Each end of a forwarded SSH stream proves it holds a secret delivered only over the administrative channel, and all stream bytes are encrypted and integrity-protected, so that replacing the socket path cannot impersonate an end or read the stream. | `packages/ssh/ssh/README.md:64` |
| `DH-LIB-NODENET-R8` | A header name or value taken from configuration is checked to be legal HTTP when the configuration loads, not when the first request fails. | `packages/acp/acp/src/mcp.ts:89` |
| `DH-LIB-NODENET-R9` | A file path can be converted to a `file:` URL and back **for a named target system**, independent of the system the host runs on, and malformed URLs (bad escapes, a host part, an encoded separator, a NUL) are refused. | `packages/lsp/tool-lsp/src/render.ts:168` |
| `DH-LIB-NODENET-R10` | An admitted upgrade request can be passed through to another local listener without the host parsing the upgraded protocol. | `packages/experimental/inspector/src/host/plugin.ts:149` |

## 5. Go choice

**Decision.** The standard library for all six modules.

| Node module | Go package | Behaviour relied on |
|---|---|---|
| `node:http` server | `net/http` (`http.Server`, `http.ServeMux`, `http.ResponseController`, `http.MaxBytesReader`) | Listening, routing, flush for streams, hijack for upgrades, body limits |
| `node:https` server | `crypto/tls` with `net/http` | `tls.LoadX509KeyPair` refuses an empty file and a key that does not match its certificate (`crypto/tls/tls.go:278`, `:325`), which gives the R1 start-up failure without upstream's extra empty-file check |
| `node:net` | `net` (`net.Listen`, `net.Dialer.DialContext`, `*net.UnixConn`), `net/netip`, `os` | Unix sockets (also on Windows), half-close with `CloseWrite`, deadlines, context cancellation, `netip.ParseAddr` for "is this an IP literal" ([ipaddr.js.md](ipaddr.js.md)) |
| `node:tls` | `crypto/tls` | TLS 1.2 and 1.3 with certificates; **not** pre-shared keys (below) |
| `node:dns` | `net` (the resolver used implicitly by every dial) | Nothing more: upstream resolves no names itself |
| `node:url` | `net/url`, `path/filepath`, `os.Executable`, `embed` | URL parsing; a Go program finds its files through `embed` or its executable path, so the module-location calls have no equivalent need |
| inspector tunnel | `net/http/httputil.ReverseProxy` | Passes an upgrade through (`httputil/reverseproxy.go:822`), replacing the hand-written request in upstream |

**What the standard library does not do.** Each row is a place where a line-for-line
port would be wrong or impossible.

| Gap | Fact checked | Consequence for the design |
|---|---|---|
| No time limits on a server by default | `http.Server` fields such as `ReadHeaderTimeout` are zero unless set (`net/http/server.go:3096`) | [DH-ADR-NODENET-1](#dh-adr-nodenet-1) |
| `Shutdown` neither closes nor waits for hijacked connections, and does not interrupt a handler that is still streaming | documented on `http.Server.Shutdown` (`net/http/server.go:3268`) | DH-ADR-NODENET-1: the listener's owner tracks them. `Server.Close` does close connections still in the TLS handshake, because a connection is tracked from accept (`net/http/server.go:1893`, `:3240`), so upstream's separate handshake set is not needed |
| A panic in a goroutine a handler started ends the process; only the handler's own goroutine is protected | Go language rule; `net/http` recovers only in its connection goroutine | R4: handlers start goroutines only through the owning fiber's helper, which recovers and reports |
| No TLS cipher suites with pre-shared keys The suites are listed in `crypto/tls/cipher_suites.go`; a probe reported that 0 of the 25 contain `PSK` (unverified, to be measured by `DH-T-NODENET-4`) | [DH-ADR-NODENET-2](#dh-adr-nodenet-2) |
| No Windows named pipes in `net` | `net` has no pipe network type | Module `github.com/Microsoft/go-winio` (`DialPipeContext`), recommended to the computer-use area: v0.6.4, 2026-10-13, MIT; `go list -m -json`, `go mod download -json`, `LICENSE` and `pipe.go` read in the module cache, 2026-11-18; no cgo. Opening the pipe path with `os.OpenFile` also works for a client but gives no deadline or cancellation |
| Extra inherited files for a child are Unix only | "ExtraFiles is not supported on Windows" (`os/exec/exec.go:242`) | R6 on Windows needs `SysProcAttr.AdditionalInheritedHandles` (`syscall/exec_windows.go:300`) and the handle value passed to the child; the subprocess area owns this |
| No exported check for legal header text | `net/http` only fails when the request is sent | R8 uses `httpguts.ValidHeaderFieldName` and `ValidHeaderFieldValue` from `golang.org/x/net/http/httpguts` (the module verified in [undici.md](undici.md)) |
| No path-to-`file:`-URL function, and `path/filepath` only knows the host's own rules | none exists in `net/url` or `path/filepath` | R9 is about 60 lines of own code with explicit Windows and POSIX modes (drive letters, UNC hosts, percent-encoding) |
| `net/url` follows RFC 3986, not the browser (WHATWG) rules | it does not lower-case or canonicalise hosts (`0x7f.0.0.1` stays as written) and does no IDNA conversion | Code that compares hosts for trust (the `Host` check of DH-ADR-WIRE-10) must normalise explicitly and refuse what it cannot parse; see `DH-LIB-IPADDRJS-R6` |

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** standard library plus the helpers above | One HTTP stack for server and client; no web framework; `CGO_ENABLED=0` | The gaps above are ours to close and test | Go 1.27.4 source, 2026-11-18 |
| A third-party HTTP router or framework | Middleware ready-made | Nothing upstream's route model needs that `http.ServeMux` patterns lack; adds a dependency in the authenticated request path | none evaluated, none named |

**Tradeoff.** More explicit set-up code than Node needs for a server (limits, connection
tracking), in exchange for no hidden defaults.

<a id="dh-adr-nodenet-1"></a>
### DH-ADR-NODENET-1 Every listener has explicit limits and an owner that closes all of its connections

- **Decision.** A harness helper builds each `http.Server`: `ReadHeaderTimeout` and
  `IdleTimeout` always set; request bodies wrapped in `http.MaxBytesReader` with a
  per-route limit; no global `WriteTimeout`, because it would cut the event stream, and
  instead a per-response write deadline through `http.ResponseController` on ordinary
  routes. The helper records every hijacked connection and every streaming handler. On
  unload it stops accepting, cancels the base context that every request context derives
  from, closes hijacked connections, waits for handlers up to a bound, then calls
  `Server.Close` for whatever is left.
- **Why.** Inference: Node's HTTP server applies default header and request time limits,
  so upstream never sets any. Go gives none, so an unconfigured Go listener can be held
  open by a client that sends one byte a minute.
  And both runtimes leave upgraded sockets out of "close all connections", which is why
  upstream tracks them (`packages/host/webserver/src/index.ts:755`).
- **Tradeoff.** A handler that ignores its context delays unload until the bound, then
  loses its connection. The bound is a setting of the web-host area.
- **Serves.** [DH-O-1](../design.md#dh-o-1) (everything a component acquired is released once), [DH-O-12](../design.md#dh-o-12); DH-ADR-3, [DH-ADR-WIRE-2](../upstream/wire-and-api.md#dh-adr-wire-2).
- **Verified by.** `DH-T-NODENET-1`, `DH-T-NODENET-2`.

<a id="dh-adr-nodenet-2"></a>
### DH-ADR-NODENET-2 Forwarded SSH streams are authenticated by pinned one-time certificates, not by TLS pre-shared keys

- **Decision (recommendation to the SSH area, which owns the final design).** Keep the
  property of `DH-LIB-NODENET-R7` and change the mechanism. For each stream, each end
  creates a one-time key pair and self-signed certificate. The two public-key
  fingerprints travel over the administrative channel, where upstream sends its 256-bit
  key today. The stream then runs TLS 1.3 with client certificates required, and each
  end accepts exactly the fingerprint it was told (`tls.Config.VerifyConnection`),
  ignoring names and authorities.
- **Why.** Upstream's mechanism is TLS 1.2 with the single suite `PSK-AES256-GCM-SHA384`
  (`packages/ssh/ssh/src/stream-security.ts:7`). Go's `crypto/tls` implements no
  pre-shared-key suite, so it cannot be ported. Pinning a one-time certificate whose
  fingerprint came over the trusted channel gives the same guarantee: only the two
  processes that took part in the administrative exchange can complete the handshake.
- **Alternatives.** (a) Keep a single shared secret: run TLS 1.3 with a one-time server
  certificate and have each end send an HMAC of the connection's exported keying
  material (`ConnectionState.ExportKeyingMaterial`) under that secret. Also standard
  library only, but it is a hand-built protocol step. (b) Use an in-process SSH client
  (`golang.org/x/crypto/ssh`, v0.59.0, 2026-11-11, BSD-3-Clause, checked with
  `go list -m -json` and the module cache on 2026-11-18), whose
  `direct-streamlocal@openssh.com` channels are carried inside the authenticated SSH
  connection, so there is no local forwarding socket to replace. That changes how the
  harness uses the operator's OpenSSH configuration and is the SSH area's decision.
- **Tradeoff.** Both ends must be harness code that follows the new scheme, which holds
  because the remote helper is shipped by the harness. Certificate generation costs a
  key generation per stream.
- **Serves.** [DH-O-10](../design.md#dh-o-10) (remote hosts), [DH-O-7](../design.md#dh-o-7); DH-ADR-1, DH-ADR-2.
- **Verified by.** `DH-T-NODENET-4`.

## 6. Design notes

- **Listener ownership.** A listener, its TLS configuration and its connection registry
  are created by one effect on the fiber of the component that asked for them (the web
  host, the webhook adapter, a helper bridge). The effect's disposer is the unload
  sequence of DH-ADR-NODENET-1. If the fiber is unloaded while the listener is still
  being created (certificate files being read), the disposer waits for creation to end
  and then closes it, as upstream does (`packages/host/webserver/README.md:97`).
- **Local streams.** A dialled socket or pipe is owned by the fiber that dialled it and
  is closed by that fiber's disposer. A connect attempt takes the fiber's context, so
  unloading during connect cancels it. A stream that fails mid-message leaves the
  remote operation in an unknown state; the protocol that runs over the stream, not this
  layer, must say whether it may be retried.
- **`file:` URLs.** One package (`fileuri` here) with `FromPath(path, sys)` and
  `ToPath(uri, sys)`, where `sys` is `Windows` or `POSIX`. It is pure functions. The
  language-server and file-system areas use it; nothing else should build such URLs by
  string concatenation.
- **Verdict on upstream.** Keep: one concrete bind address, fail closed on TLS problems,
  per-request failure containment, half-open local streams. Change: the stream
  authentication mechanism. Drop: every `fileURLToPath(import.meta.url)` call, the
  browser-worker stubs of Node modules, and the hand-written upgrade tunnel.

## 7. Risks and open questions

- **SSH.** DH-ADR-NODENET-2 is a recommendation. The SSH area must choose between it and
  an in-process SSH client, and write the real-host scenario.
- **Named pipes.** `go-winio` is named for the computer-use area to adopt or reject; no
  library item is declared for it here.
- **Windows control channel.** `DH-LIB-NODENET-R6` on Windows is new work for the
  subprocess area; upstream gets it from Node's process spawning.
- **HTTP/2 and HTTP/3 on the listener.** Upstream serves HTTP/1.1 only. `net/http` offers
  HTTP/2 over TLS automatically; the web-host area should decide whether to leave it on,
  since WebSocket upgrades and some reverse proxies behave differently under it.
- **Unix sockets on Windows.** Go supports them on current Windows; older builds do not.
  The areas that use them need a stated minimum Windows version.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-NODENET-1` | local | `DH-LIB-NODENET-R1`, `DH-LIB-NODENET-R2` | Port 0 yields a readable port; a port in use, a missing certificate file, an empty one and a mismatched key each fail start-up with the cause and leave nothing listening; a client that sends headers one byte at a time is disconnected at the limit; a body over the route limit gets 413. |
| `DH-T-NODENET-2` | local | `DH-LIB-NODENET-R3`, `DH-LIB-NODENET-R4` | With one idle connection, one request in progress, one open event stream, one upgraded socket and one connection that never finishes its TLS handshake, unload returns within the bound and all five are closed; a handler that panics, and a helper goroutine that panics, each produce a log entry and leave the process running. |
| `DH-T-NODENET-3` | local | `DH-LIB-NODENET-R5`, `DH-LIB-NODENET-R6`, `DH-LIB-NODENET-R10` | A Unix-socket echo with half-close works on every tested system; a child started with a control channel reads and writes it while its standard streams stay separate; an upgrade request is passed through to a second local listener and bytes flow both ways. The named-pipe and Windows-handle cases run only on Windows. |
| `DH-T-NODENET-4` | local | `DH-LIB-NODENET-R7` | Over an in-memory pipe: the two real ends complete a handshake; an end with a different certificate, an end that presents none, and a plain-text client are all refused before any stream byte is delivered; TLS versions below 1.3 are refused. |
| `DH-T-NODENET-5` | local | `DH-LIB-NODENET-R8`, `DH-LIB-NODENET-R9` | Header names and values with control characters, spaces in names or line breaks are refused at configuration load; a table of Windows and POSIX paths (drive letters, UNC, spaces, `%`, non-ASCII) converts to a URL and back unchanged on any host system, and malformed URLs are refused. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.
Real-environment evidence (a real remote host for the SSH stream, a real Windows desktop
helper for the pipe) belongs to the SSH and computer-use areas.

## 9. Units dispositioned by this spec

`UP-DEP-node:http`, `UP-DEP-node:https`, `UP-DEP-node:net`, `UP-DEP-node:tls`,
`UP-DEP-node:dns`, `UP-DEP-node:url`.
