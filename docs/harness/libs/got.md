# got: Go specification

> **Status: design, not built.** Short form. Upstream facts were read at the pinned commit
> recorded in [../README.md](../README.md). Statements that are reasoning and not read
> from source open with "Inference". The shared client design is in [undici.md](undici.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-GOT` |
| **Upstream unit** | `UP-DEP-got` |
| **Upstream version** | 14.6.6 |
| **Kind** | runtime, one package |
| **Used by** | telemetry (`packages/telemetry/otel`) |
| **Go choice** | standard library `net/http`, through the `egress` client factory of [undici.md](undici.md) |
| **Related areas** | telemetry |

## 1. What it is and why upstream needs it

got is an HTTP client for Node built on `node:http`. Upstream uses it in exactly one
place: sending batches of usage events to an OpenTelemetry collector over OTLP (the
OpenTelemetry wire protocol) as JSON over HTTP.

It is there because the OpenTelemetry SDK's own exporter cannot be cancelled. When a
telemetry channel shuts down, the SDK's request and its retry timer would keep the
process alive. got gives one abort signal that stops the request, the wait between
retries and the response read (`packages/telemetry/otel/README.md:28`, `:37`). It runs on
`node:http` agents, which undici's dispatcher does not reach, so telemetry never goes
through the proxy (`packages/util/http-proxy/README.md:113`).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/telemetry/otel/src/event-transport.ts:27` | One `POST` per batch, body optionally gzip-compressed by the caller | `got.post(url, { body, headers, agent, signal, followRedirect: false, retry, responseType: 'buffer' })` |
| `packages/telemetry/otel/src/event-transport.ts:32` | Cancel on channel shutdown or per-attempt timeout | `AbortSignal.any([...])` |
| `packages/telemetry/otel/src/event-transport.ts:34` | Retry `POST` up to 5 times on 429, 502, 503 and 504, never on a network error code, with waits capped at 5 s | `retry` option |
| `packages/telemetry/otel/src/event-transport.ts:38` | Stop reading a response larger than 4 MiB | `downloadProgress` event and `cancel()` |
| `packages/telemetry/otel/src/event-transport.ts:52` | Destroy the keep-alive agent the channel owns on shutdown | `agent.destroy()` |

No patch, pin or exemption exists for got (`patches/`, `pnpm-workspace.yaml` searched).
The one limitation it causes, "telemetry is direct by design", is the inventoried unit
`UP-DEBT-limit.packages.util.http-proxy.4`, owned by the area agent for that package.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-GOT-R1` | Shutting down a telemetry channel cancels the request in flight, any wait before a retry and any response read, and returns only after its connections are released. | `packages/telemetry/otel/README.md:28` |
| `DH-LIB-GOT-R2` | An export is retried a bounded number of times, only for statuses that mean "try later" (429, 502, 503, 504), with a capped wait; a redirect is an error, never followed. | `packages/telemetry/otel/src/event-transport.ts:33` |
| `DH-LIB-GOT-R3` | A collector response is read under a fixed size limit. | `packages/telemetry/otel/src/event-transport.ts:38` |
| `DH-LIB-GOT-R4` | Telemetry export uses the same proxy policy and certificate authorities as every other outbound request. | Change from upstream; `DH-LIB-UNDICI-R1` |
| `DH-LIB-GOT-R5` | Headers are exactly the ones the channel supplies: no ambient authorization and no shared connection identity between channels. | `packages/telemetry/otel/README.md:32` |

## 5. Go choice

**Decision.** No retry or client library. A telemetry channel asks `egress` for its own
`http.Client` (redirects refused, a response-header timeout), and its exporter runs a
small loop: send with a context, on a retryable status wait with a timer that the same
context cancels, read the response through `egress.ReadCapped`.

**Why.** got exists to add cancellation to an API that lacks it. In Go every request
already takes a `context.Context` that stops the dial, the write, the wait and the body
read, so `DH-LIB-GOT-R1` needs no library. The retry rule is about fifteen lines.
`DH-LIB-GOT-R4` falls out of [DH-ADR-UNDICI-1](undici.md#dh-adr-undici-1): upstream's
reason for a direct connection was that `node:http` could not see the proxy, and that
reason does not exist here.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `net/http` with an own retry loop | No dependency; one outbound path; cancellation by construction | The loop is ours to test | Go 1.27.4 standard library |
| The OpenTelemetry Go SDK's OTLP HTTP exporter, if the telemetry library spec adopts that SDK | Retry and batching already written | The telemetry spec must confirm it accepts an `http.Client` and stops promptly on context cancel; not checked here | Not verified by this agent; belongs to the OpenTelemetry library spec |

**Tradeoff.** Behind a proxy that blocks the collector, exports now fail at the proxy
instead of silently connecting directly. Telemetry loss must stay invisible to the user,
so the channel treats every export failure as a dropped batch, as upstream does.

Failure and ownership: the channel's fiber owns the client and its release function.
A batch whose request was cut off mid-flight may or may not have reached the collector;
it is not re-sent after shutdown, so the outcome is "at most once after cancel".

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-GOT-1` | local | `DH-LIB-GOT-R1` | Against a test server that never answers and one that answers 503: cancelling the channel context returns within a bound, no goroutine or connection remains, and no retry fires afterwards. |
| `DH-T-GOT-2` | local | `DH-LIB-GOT-R2`, `DH-LIB-GOT-R3`, `DH-LIB-GOT-R5` | 429 then 200 succeeds on the second attempt; 400 is not retried; a 302 is an error; a 5 MiB response is cut at the limit; the request carries only the configured headers. |
| `DH-T-UNDICI-1` (defined in [undici.md](undici.md)) | local | `DH-LIB-GOT-R4` | A telemetry export arrives at the fake proxy. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

Units dispositioned by this spec: `UP-DEP-got`.
