# ws: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go module facts were checked on 2026-11-18.

| | |
|---|---|
| **Library ID** | `DH-LIB-WS` |
| **Upstream unit** | `UP-DEP-ws` |
| **Upstream version** | 8.21.3 |
| **Kind** | runtime (4 packages), also used as a test client (3 apps and many suites) |
| **Used by** | api (`gateway`), apps (`desktop`), experimental (`inspector`, `liveshare`) |
| **Go choice** | named Go module `github.com/coder/websocket` |
| **Related areas** | A19, which owns the wrapper package and the terminal socket route ([../areas/api.md](../areas/api.md), `DH-D-API-8`); A09, which owns terminal sessions ([../areas/execution.md](../areas/execution.md)); C6, which owns the terminal island |

## 1. What it is and why upstream needs it

A WebSocket is one long-lived, two-way, message-framed connection that starts as an HTTP
request and is then "upgraded". `ws` is the standard Node library for both ends of it.

Upstream needs WebSockets because its browser client holds state and must receive a
continuous flow from the host. All of that flow, for every feature, travels over one
socket at `/api/remote.mux`, with many logical streams multiplexed inside it
(`packages/api/gateway/src/stream-server.ts:56`). The same library also serves the
Chrome DevTools protocol for the inspector and is the client for two outbound
connections.

The Go design removes most of this. [DH-ADR-WIRE-2](../upstream/wire-and-api.md#dh-adr-wire-2)
moves host-to-browser updates to one server-sent-events stream per tab and keeps a
WebSocket only where bytes flow upward at keystroke rate: the terminal, one socket per
terminal. So this spec chooses a WebSocket module for a much smaller job than `ws` does
upstream.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/api/gateway/src/stream-server.ts:81` | Accept the multiplexed stream socket after the web server has admitted the upgrade | `WebSocketServer({ noServer: true, perMessageDeflate })`, `handleUpgrade` |
| `packages/api/gateway/src/stream-server.ts:135` | Heartbeat: ping every interval, terminate after two missed; any received frame or pong counts as life | `ping()`, `'pong'`, `terminate()` |
| `packages/api/gateway/src/stream-server.ts:200` | Text frames only; binary closes with 1003, a bad frame with 1008, an undeliverable error with 1011, a departed Peer with 1001 | `'message'`, `close(code, reason)` |
| `packages/api/gateway/src/stream-server.ts:358` | One ordered write queue per connection; sources pause when unwritten bytes pass a limit | `send(text, { compress }, callback)`, `bufferedAmount` |
| `packages/experimental/inspector/src/worker/bridge/endpoint.ts:44` | Two servers on a private port: DevTools protocol sessions and client event ingest, each with a frame-size limit | `WebSocketServer({ noServer: true, maxPayload })` |
| `packages/experimental/liveshare/src/owner-gateway/worker.ts:160` | Outbound registration socket to the relay, authenticated by headers, with ping and missed-pong counting | `new WebSocket(url, { headers })`, `'unexpected-response'` |
| `apps/desktop/src/account-backend.ts:103` | Electron main process watches account state on the host's own stream socket, with a 64 KiB frame limit and a 1 s reconnect | `new WebSocket(url, { headers, maxPayload })` |
| Tests, for example `apps/web/tests/smoke-real.e2e.ts:29` | A scripted client for the stream socket | `new WebSocket(...)` |

## 3. Tech debt upstream carries

Search performed: every import of `ws` in `apps`, `packages` and `scripts`; `patches/`
and `pnpm-workspace.yaml` (no patch, pin or exemption); the gateway, inspector and
LiveShare READMEs.

This spec defines no new debt unit. Three inventoried limitations of the gateway are
caused by `ws` and belong to the area agent that owns `packages/api/gateway`:

| Inventoried unit | What it is | Go situation |
|---|---|---|
| `UP-DEBT-limit.packages.api.gateway.8` | `ws` ignores its own compression threshold, so the mux decides per frame whether to compress | Not applicable: there is no multiplexed socket, and the chosen module applies its own size threshold |
| `UP-DEBT-limit.packages.api.gateway.9` | A completed compressed write proves only that the local pipeline drained, so a vanished client is detected late | Does not arise with compression off (`DH-LIB-WS-R6`); the heartbeat is the liveness signal |
| `UP-DEBT-limit.packages.api.gateway.7` | One setting is both ping cadence and activity deadline | The terminal area sets interval and deadline separately (`DH-LIB-WS-R4`) |

The proxy bypass of outbound WebSocket clients is recorded as `UP-DEBT-found.L7.1` in
[undici.md](undici.md).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-WS-R1` | A socket is accepted only for a request the web host has already admitted: source check, credential, access to the terminal named in the path. The WebSocket library's own origin check is a second line, not the gate. | `packages/host/webserver/src/index.ts:719`; [DH-ADR-WIRE-10](../upstream/wire-and-api.md#dh-adr-wire-10) |
| `DH-LIB-WS-R2` | Binary messages in both directions: keystrokes and resize commands up, terminal output down. A message of the wrong type or over the size limit closes the socket with a standard close code. | `packages/api/gateway/src/stream-server.ts:203`; `packages/api/terminal-controller/README.md:41` |
| `DH-LIB-WS-R3` | Output to a slow browser is bounded: writes carry a deadline, the per-socket buffer has a byte limit, and overflow closes the socket so the browser reconnects and re-reads the screen. A slow browser never blocks the terminal or grows host memory. | `packages/api/gateway/src/stream-server.ts:343` |
| `DH-LIB-WS-R4` | The host pings on an interval and closes a socket that shows no life within a separate deadline; any received frame counts as life. | `packages/api/gateway/src/stream-server.ts:135` |
| `DH-LIB-WS-R5` | A socket is owned by the terminal session it serves: when that owner, the authenticated principal or the web host goes away, the socket is closed and the handler returns. HTTP server shutdown alone does not do this. | `packages/api/gateway/src/stream-server.ts:516`; `packages/host/webserver/src/index.ts:755` |
| `DH-LIB-WS-R6` | Compression is off for the terminal socket by default and can be turned on by configuration. | `packages/api/gateway/README.md:96` (upstream advises `'none'` on loopback) |
| `DH-LIB-WS-R7` | An outbound WebSocket, if a component ever needs one, is dialled with a client from the `egress` factory, so proxy policy and certificate authorities apply. No component in the current design dials one: the LiveShare relay is not built ([DH-ADR-XSHARE-2](../areas/x-liveshare-webworker.md#dh-adr-xshare-2)). The requirement stays so that the first such client cannot bypass `egress`. | `DH-LIB-UNDICI-R1` |
| `DH-LIB-WS-R8` | Tests can drive a socket from Go without a browser. | the test use sites |

## 5. Go choice

<a id="dh-adr-ws-1"></a>
### DH-ADR-WS-1 `github.com/coder/websocket` for the terminal socket and any outbound WebSocket client

- **Decision.** Use `github.com/coder/websocket`, wrapped in one small harness package so
  that no other package imports it.
- **Why.** Its API is built on `context.Context`: `Read`, `Write` and `Ping` all take one,
  which gives write deadlines (R3), heartbeat (R4) and owner-driven cancellation (R5)
  without extra goroutine bookkeeping. `Accept` works on a standard `http.ResponseWriter`
  after the harness's own admission handlers have run (R1). `Dial` takes an `http.Client`
  (R7). It has a per-message read limit (32,768 bytes by default, adjustable) and
  concurrent-safe writes. It has no dependencies and no cgo.
- **Tradeoff.** A third-party module in the request path of an authenticated,
  code-executing feature. The wrapper keeps the replacement cost to one package.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-10](../design.md#dh-o-10) (terminals); [DH-ADR-3](../design.md#dh-adr-3), [DH-ADR-WIRE-2](../upstream/wire-and-api.md#dh-adr-wire-2).
- **Verified by.** `DH-T-WS-1` to `DH-T-WS-5`.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `github.com/coder/websocket` | Context on every call; `Accept` checks `Origin` against `Host` by default; `SetReadLimit`; `CloseNow`; `Dial` accepts an `http.Client`; `permessage-deflate` optional with built-in size thresholds; `go.mod` lists no requirements | Smaller user base than gorilla; one reader at a time (`conn.go:30`) | v1.8.15, 2026-06-15, ISC-style permission notice (`LICENSE.txt`, "Copyright (c) 2025 Coder"); `go list -m -json`, `go mod download -json`, licence, `go.mod` and `accept.go`, `dial.go`, `read.go` read in the module cache, 2026-11-18; no `import "C"` |
| `github.com/gorilla/websocket` | The most widely used; mature | No `context` in its connection API (deadlines are set by hand); one concurrent reader and one concurrent writer, enforced by the caller (`doc.go:124`); last tag is over two years old | v1.5.3, 2024-06-14, BSD 2-Clause; same method, 2026-11-18 |
| `golang.org/x/net/websocket` | Already in the module graph | Its own package comment says it lacks features found in the two modules above and that they are more actively maintained (`websocket/websocket.go:8`) | `golang.org/x/net` v0.63.0, 2026-11-17, BSD-3-Clause |
| `github.com/gobwas/ws` | Low-level, low-allocation | Frame-level API; more code to write for one socket per terminal | v1.5.0, 2026-11-03 by `go list -m -json`; licence not read, not evaluated further |
| No WebSocket: keystrokes by `POST`, output on the event stream | No module at all | One HTTP request per keystroke batch and no ordering guarantee between requests; rejected by DH-ADR-WIRE-2 | n/a |

**What the standard library does not do.** `net/http` has no WebSocket implementation.
It supplies the pieces the module builds on: `http.ResponseController.Hijack`, and
`httputil.ReverseProxy`, which passes an upgrade through unchanged.

## 6. Design notes

**Package.** One harness package, `internal/host/wsconn`, is the only importer of the
module. Area A19 owns it as part of its terminal deliverable
([DH-D-API-8](../areas/api.md#dh-d-api-8)), whose socket route frames a terminal's
`Attach` stream with it. Its surface is roughly:

```go
type Limits struct {
    MaxMessageBytes int64         // inbound; over it, close 1009
    OutboxBytes     int           // queued output; over it, close 1013
    WriteTimeout    time.Duration
    PingEvery       time.Duration
    DeadAfter       time.Duration // no frame and no pong for this long: close
}
// Accept upgrades a request that the caller has already authorised.
func Accept(w http.ResponseWriter, r *http.Request, l Limits) (*Conn, error)
// Run pumps both directions until ctx ends, the peer closes, or a limit trips.
func (c *Conn) Run(ctx context.Context, up func([]byte) error, down <-chan []byte) error
```

**Ownership and cleanup.** The terminal's fiber owns each socket. `Run` takes the fiber's
context; when the fiber unloads, the context ends, `Run` sends close code 1001 and
returns, and the handler returns. This matters because Go's `http.Server.Shutdown` does
not close or wait for hijacked connections; upstream hit the same gap in Node and tracks
upgraded sockets by hand (`packages/host/webserver/src/index.ts:755`). The web host
therefore also keeps a registry of open sockets and closes them on its own unload.

**Failure.** A read error, a write that misses its deadline, an outbox overflow or a
missed heartbeat all end `Run` with a distinct error. Nothing is retried on the host:
the browser island reconnects and asks for the current screen. Keystrokes sent while
disconnected are lost, which the island must show (it disables input until reconnected).
A keystroke the host received before the socket broke may already have reached the
program in the terminal; that is unavoidable and matches upstream.

**Compression and the event stream.** Not applied here by default (R6): terminal output
on loopback gains nothing, and deflate with a shared window keeps memory per socket.
Whether HTTP response compression applies to the server-sent-events stream is decided in
[compression.md](compression.md): it does not.

**What happens to the other upstream uses.**

| Upstream use | Go design |
|---|---|
| Multiplexed stream socket and its heartbeat, queue and limits | Dropped with the gateway by [DH-ADR-WIRE-2](../upstream/wire-and-api.md#dh-adr-wire-2); the ideas (bounded queue, heartbeat, close on owner loss) are kept as R3 to R5 |
| Desktop account watcher | Not built: there is no desktop application ([DH-ADR-18](../design.md#dh-adr-18)). Account state reaches the browser as fragments on the tab's event stream, like any other host state |
| Inspector DevTools protocol sockets | Not built: the Go harness has no DevTools endpoint ([DH-ADR-XINSPECT-1](../areas/x-inspector-timeline.md#dh-adr-xinspect-1)) |
| LiveShare registration socket | Not built: sharing has no relay ([DH-ADR-XSHARE-2](../areas/x-liveshare-webworker.md#dh-adr-xshare-2)); a viewer is a browser on the host's own HTTP surface |

## 7. Risks and open questions

- **Browsers.** The user's own browser is the only client
  ([DH-ADR-18](../design.md#dh-adr-18)). The page policy must allow a
  WebSocket to the page's own origin and to nothing else (its `connect-src`); the
  policy's value is area C1's and area A19 writes the header. The real-browser terminal
  scenario of area A19 confirms it in each supported engine.
- **Reverse proxies** in front of a remote deployment must pass the upgrade. This is a
  documentation item for the deployment guide.
- **Limits.** Area A19 chooses the numbers with its terminal deliverable (upstream:
  64 KiB input, 2 MiB buffered output,
  `packages/api/terminal-controller/README.md:40`). This spec defines no deliverable of
  its own: `DH-T-WS-1` to `DH-T-WS-5` are completion conditions of
  [DH-D-API-8](../areas/api.md#dh-d-api-8).

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-WS-1` | local | `DH-LIB-WS-R1` | Without a credential, with a foreign `Origin`, and with a foreign `Host`, the upgrade is refused with an HTTP status and no socket is created. |
| `DH-T-WS-2` | local | `DH-LIB-WS-R2`, `DH-LIB-WS-R8` | A Go client sends binary input and receives output in order; a text frame and an oversized frame each close the socket with the documented code. |
| `DH-T-WS-3` | local | `DH-LIB-WS-R3` | A client that stops reading causes the host to close the socket once the outbox limit or write deadline is hit; the producer is never blocked and memory stays under the limit. |
| `DH-T-WS-4` | local | `DH-LIB-WS-R4`, `DH-LIB-WS-R5` | A client that stops answering pings is closed after the deadline; ending the owner context closes the socket with 1001 and the handler returns; unloading the web host leaves no open connection. |
| `DH-T-WS-5` | local | `DH-LIB-WS-R6`, `DH-LIB-WS-R7` | The handshake response carries no compression extension by default; an outbound dial reaches a fake proxy. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.
The real-browser terminal scenario (type, resize, disconnect, reconnect) is
[DH-E-API-3](../areas/api.md#dh-e-api-3) of area A19.

## 9. Units dispositioned by this spec

`UP-DEP-ws`.
