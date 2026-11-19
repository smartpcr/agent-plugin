# OpenTelemetry (`@opentelemetry/*`): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Paths are relative to the upstream root. No harness code
> exists.
>
> **Unverified research notes.** The binary sizes and module counts marked "size probe"
> in section 5 report an observation made during library research on 2026-11-18 by
> building throwaway programs outside this repository. That was against the project's
> rule that nothing is built or run in the design task, and the programs were not kept,
> so the figures cannot be repeated from this repository. They are **unverified and to
> be measured** when the exporter is first built; the choice rests on the cited source
> lines and on option A adding no module, not on the exact sizes.
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The defaults that
> [DH-ADR-OTEL-3](#dh-adr-otel-3) left open are now decided: everything that leaves the
> machine is off by default and opt-in.

| | |
|---|---|
| **Library ID** | `DH-LIB-OTEL` |
| **Upstream units** | `UP-DEP-@opentelemetry/api`, `UP-DEP-@opentelemetry/api-logs`, `UP-DEP-@opentelemetry/core`, `UP-DEP-@opentelemetry/otlp-exporter-base`, `UP-DEP-@opentelemetry/otlp-transformer`, `UP-DEP-@opentelemetry/resources`, `UP-DEP-@opentelemetry/sdk-logs` |
| **Upstream version** | api 1.9.1; core and resources 2.11.0; api-logs, sdk-logs, otlp-exporter-base and otlp-transformer 0.222.0 (`pnpm-lock.yaml:22665-22707`) |
| **Kind** | runtime |
| **Used by** | `packages/telemetry/otel` (all seven), `packages/session/session-telemetry-otel` (api-logs), `packages/host/product-telemetry-otel` (api-logs, otlp-exporter-base, sdk-logs) |
| **Go choice** | own code on the standard library: a small exporter that sends OpenTelemetry log records as JSON over HTTP. No OpenTelemetry Go module is linked. |
| **Related areas** | A18, host services and telemetry ([../areas/host-telemetry.md](../areas/host-telemetry.md)); A06, session telemetry ([../areas/session-views.md](../areas/session-views.md)); A17, identity and feedback ([../areas/interaction-credentials.md](../areas/interaction-credentials.md)) |

## 1. What it is and why upstream needs it

**Telemetry** here means data about the product's use that is sent from the user's machine
to a server run by the product's publisher. OpenTelemetry is a vendor-neutral standard for
such data. It defines three kinds of data, called **signals**: logs (discrete records),
traces (timed operations) and metrics (numbers over time). It also defines **OTLP**, the
OpenTelemetry Protocol, which says how records are encoded and posted to a **collector**,
the receiving server.

Upstream uses **only the logs signal**, and only as a transport. It never creates a tracer
or a meter, and installs no global provider (`packages/telemetry/otel/README.md:37`; the
only `@opentelemetry` imports in source are listed in section 2). The metrics and trace
SDK packages are in the lockfile only because the encoder package depends on them
(`pnpm-lock.yaml:30381-30382`). Two product features put records on this transport:

1. **Session upload on feedback.** When the user submits feedback, the whole session so
   far is uploaded, one log record per session event
   (`packages/session/session-telemetry-otel/README.md:14`).
2. **Desktop product analytics.** The desktop app reports selected UI events with
   environment and identity fields (`packages/client/product-analytics/README.md:12`).

The library family provides the record type, batching in memory, JSON encoding, the HTTP
POST with retry, and shutdown. Everything about *what* is sent and *when* lives in the
consuming packages. This spec therefore owns the transport, and records the privacy facts
(section 1.1) on which the owner decided the Go defaults
([DH-ADR-OTEL-3](#dh-adr-otel-3)).

### 1.1 What leaves the machine in upstream's shipped configuration

All OpenTelemetry traffic is HTTP `POST` of OTLP logs in JSON
(`packages/telemetry/otel/src/transport.ts:53`, `:28`) to one default collector,
`https://dsh-otel-collector.deepseeksvc.com/v1/logs`.

| | Flow A: session upload on feedback | Flow B: desktop product analytics |
|---|---|---|
| Mounted in | every profile, through the base bundle (`packages/bundle/base/cordis.patch.yml:203-233`) | the desktop profile only (`packages/bundle/web-app/cordis.patch.yml:37-54`) |
| Default | on, mode `FEEDBACK_ONLY` (`cordis.patch.yml:222`) | on, `enabled: true`, "without a user-facing control" (`packages/client/product-analytics/README.md:12`, `:24`) |
| What triggers a send | only a new feedback action in that session: text feedback, a rating created or edited, a note edit, a withdrawal (`session-telemetry-otel/README.md:36`). Ordinary activity, restore and startup never trigger it (`:68`) | each listed UI event: launch, sign-in page, onboarding, send button, model switch, plugin install and so on (`packages/client/product-analytics/src/events.ts:6-57`) |
| Content | **the complete session log from its first event up to the feedback event**: every event's envelope and data (`session-telemetry-otel/README.md:72`, `.agents/notes/implemented/architecture/2026-09-05-nonofficial-feedback-otel.md:15`). That includes prompts, model output, tool arguments and results. Attributes add session id, format version, event type and sequence, and when present the working directory, parent session id and inherited event count (`packages/session/session-telemetry/src/coordinator.ts:291-305`) | event name plus chosen fields: session ids, model names, plugin names, a redacted install input; OS name, version and build, CPU model and core counts, total memory; `app_version` (`product-analytics/README.md:28-42`). No prompts, responses, keys, host names or machine UUIDs (`:28`) |
| Identity attached | `user.id` = the anonymous installation id; `service.name`, `service.version` (`packages/session/session-telemetry-otel/src/index.ts:195-199`) | **`user_id` and `device_id` from the login record when the user is signed in** (`packages/client/product-analytics/src/index.ts:121-122`). This flow is not anonymous for a signed-in user |
| Redaction | none built in. A deployment may mount rules; without them records leave "exactly as captured, including any credentials embedded in file contents or command output" (`packages/session/session-telemetry/README.md:118`) | none; the caller chooses the fields (`packages/host/product-telemetry-otel/README.md:104`) |
| Extra header | none (`session-telemetry-otel/README.md:72`) | `x-channel: dsh_otel_report` (`packages/host/product-telemetry-otel/src/index.ts:48`, `:87`) |
| Endpoint override | `DSH_TELEMETRY_OTLP_URL` (`cordis.patch.yml:226`) | `DSH_PRODUCT_ANALYTICS_OTLP_URL` (`web-app/cordis.patch.yml:47`) |
| How the user turns it off | `DSH_TELEMETRY_MODE=DISABLED`, or `DSH_TELEMETRY_DISABLED` set to any non-empty value, even `0` (`packages/boot/app-boot/src/profile-context.ts:42-55`) | only by setting `enabled: false` in the `product-analytics` config namespace; there is no UI for it (`product-analytics/README.md:24`) |

Three facts a reader should not miss:

- **Feedback uploads the session, not just the feedback.** The acknowledgement the user
  sees confirms that feedback was recorded and does not consult the sharing policy
  (`session-telemetry-otel/README.md:39`). Whether the feedback dialog tells the user
  what will be uploaded was not audited here; see section 7.
- **`DSH_TELEMETRY_DISABLED` does not cover analytics.** Inference: the switch patches only
  the row named `session-telemetry-otel` (`profile-context.ts:39`, `:52-55`), and no other
  source file reads the variable, so desktop analytics keeps running when it is set.
- **Turning OpenTelemetry off does not stop all uploads.** Two flows outside this family
  also send data by default and are owned by other areas: the session log is attached to
  official DeepSeek model requests (`packages/session/session-log-deepseek/src/index.ts:60`;
  "disabling OTel does not disable this contribution",
  `.agents/notes/implemented/architecture/2026-09-14-session-log-upload-default.md:28`),
  and every DeepSeek request carries the anonymous id in the header
  `x-deepseek-harness-user-id` (`packages/llm/llm-deepseek/src/adapter.ts:158`).

**Where the anonymous id comes from.** It is a random version-4 UUID, never derived from
host name, network address or account (`packages/identity/anonymous-user-id/src/index.ts:4-9`).
It is stored as one line in `$DSH_HOME/.anonymous-user-id` (default `~/.dsh`), so it is
per harness home, not per machine or person. Deleting the file gives a new id at the next
start (`:8-9`, `:29`). Inference: the file is created at startup, not at first upload, because
the session backend calls `getOrCreateAnonymousUserId()` in its constructor whenever the
mode is not `DISABLED` (`session-telemetry-otel/src/index.ts:198`).

## 2. How upstream uses it

| Package | Use sites (`path:line`) | What it is used for | API used |
|---|---|---|---|
| `@opentelemetry/api` | `packages/telemetry/otel/src/event-log.ts:2`, `session-log.ts:3` | a type for attribute maps. No runtime call | `Attributes` (type only) |
| `@opentelemetry/api-logs` | `event-log.ts:4`, `session-log.ts:4`, `packages/session/session-telemetry-otel/src/index.ts:28`, `:132-136` | severity levels on a record | `SeverityNumber`, `Logger` (type) |
| `@opentelemetry/core` | `event-log.ts:5`, `session-log.ts:5` | tell success from failure in an export callback | `ExportResultCode` |
| `@opentelemetry/resources` | `event-log.ts:6`, `session-log.ts:7` | attach `service.name`, `service.version`, `user.id` to every record of a channel | `resourceFromAttributes` |
| `@opentelemetry/sdk-logs` | `event-log.ts:7-8`, `:50-66`; `session-log.ts:8`, `:198-203`; `transport.ts:5` | the record pipeline: a provider per channel, count-based batching for ordinary events, and the processor interface upstream implements itself for session logs | `LoggerProvider`, `BatchLogRecordProcessor`, `LogRecordProcessor`, `LogRecordExporter`, `ReadableLogRecord` |
| `@opentelemetry/otlp-exporter-base` | `transport.ts:2-3`, `:26-61`; `event-transport.ts:5`, `:19`; `packages/host/product-telemetry-otel/src/index.ts:5` | the HTTP exporter shell, its timeout and compression defaults, the Node HTTP agent | `OTLPExporterBase`, `createOtlpHttpExportDelegate`, `createOtlpNetworkExportDelegate`, `getSharedConfigurationFromEnvironment`, `httpAgentFactoryFromOptions`, `CompressionAlgorithm` |
| `@opentelemetry/otlp-transformer` | `transport.ts:4`, `event-transport.ts:6`, `session-log.ts:6`, `:95` | encode records as OTLP JSON, and measure one record's encoded size | `JsonLogsSerializer`, `LogsExporterMetricsHelper` |

How the pieces fit (`packages/telemetry/otel/src/index.ts:13-31`): one cordis service,
`ctx.otel`, is a factory. Mounting it creates nothing. A consumer asks it for a
**channel**: an independent queue with its own endpoint, headers, resource and scope. There
are two channel kinds.

- **Ordinary-event channel** (`event-log.ts`): batches by record count (by default 512 per
  request and a queue of 2,048, `packages/host/product-telemetry-otel/src/index.ts:52-53`)
  and sends through a second HTTP client, `got`, written so that shutdown can cancel a
  request and its retry waits (`event-transport.ts:16-44`). `got` belongs to
  the HTTP library family (agent L7, unit `UP-DEP-got`) and is not specified here.
- **Session-log channel** (`session-log.ts`): batches by count **and by bytes**. One
  request may not exceed 4,000,000 uncompressed bytes (`:12`). A single record over the
  limit is rejected whole and never truncated (`:102-105`). One request is in flight at a
  time (`:126-145`).

Both are memory-only and best-effort: a full queue, a failed request or a process exit
loses records (`packages/telemetry/otel/README.md:63-67`).

## 3. Tech debt upstream carries

Search performed: `patches/` (no OpenTelemetry patch); `pnpm-workspace.yaml` (no
OpenTelemetry pin, override or exemption); "Known Limitations and Deferred Work" in the
four consuming READMEs; comments in the five source files of `packages/telemetry/otel/src`.
No `UP-DEBT` unit was assigned to this family. Four were found and are defined by this spec.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L8.1` | Seven packages must be pinned to exact versions from one coordinated release, four of them `0.x` from the SDK's experimental tree, whose API churn upstream expects to absorb. One consumer declares a package it never imports. | `packages/telemetry/otel/README.md:39`; `packages/telemetry/otel/package.json:35-41`; `packages/session/session-telemetry-otel/README.md:140`; `packages/host/product-telemetry-otel/package.json:38` (declares `sdk-logs`; no import under its `src` or `tests`) | Resolved by [DH-ADR-OTEL-1](#dh-adr-otel-1): no SDK is linked, so there is no release train to follow. |
| `UP-DEBT-found.L8.2` | Two HTTP transports for one protocol. The SDK's own transport cannot be cancelled, so ordinary events got a second one built on `got` that re-implements retry, gzip and the response-size limit. | `packages/telemetry/otel/src/event-transport.ts:1`, `:20-44`; `transport.ts:26-29` | Resolved: one transport on `net/http`, where a request context cancels the request and the retry wait (`DH-LIB-OTEL-R7`, `-R8`). |
| `UP-DEBT-found.L8.3` | The SDK batches by count only, so byte-bounded batching was written around it. Each record is encoded once only to measure it and again to send it, and the per-record measure over-counts the shared envelope. A watchdog can warn but cannot free a stuck request slot. | `packages/telemetry/otel/src/session-log.ts:71`, `:93-97`, `:136-137`, `:147-152` | Resolved: the exporter owns the encoder, so each record is encoded once, its bytes are kept and counted exactly, and a per-request deadline really ends the request (`DH-LIB-OTEL-R6`, `-R7`). |
| `UP-DEBT-found.L8.4` | Unused machinery has to be switched off by hand: the encoder pulls in the metrics and trace SDKs, the exporter wants a self-observability meter that is passed as `undefined`, and the SDK reads `OTEL_*` environment variables for compression and timeout while upstream rejects them for headers and TLS. | `pnpm-lock.yaml:30381-30382`; `transport.ts:13-15`, `:36-38`, `:48`; `packages/host/product-telemetry-otel/src/index.ts:27` | Resolved by [DH-ADR-OTEL-2](#dh-adr-otel-2): logs only, and no setting is read from the environment. |

Debt units owned by other agents whose cause is this family. This spec gives them the
mechanism; the owning agent writes the ledger row.

| Unit (owner) | What it is | What `DH-LIB-OTEL` offers |
|---|---|---|
| `UP-DEBT-limit.packages.session.session-telemetry-otel.1`, `.2` (A06) | experimental SDK tree; live-collector behaviour belongs to the SDK | no SDK; behaviour is the harness's own and tested (section 8) |
| `UP-DEBT-limit.packages.util.http-proxy.4` (A23) | telemetry bypasses the configured proxy because the SDK posts through `node:http` (`packages/util/http-proxy/README.md:113`) | the channel takes its `*http.Client` from the harness, so proxy and CA policy apply (`DH-LIB-OTEL-R12`) |
| `UP-DEBT-limit.packages.telemetry.otel.3`, `UP-DEBT-limit.packages.host.product-telemetry-otel.3` (A18) | ordinary events batch by count, so the caller must keep requests under the collector's 4 MB limit | every channel is bounded by count and by bytes (`DH-LIB-OTEL-R6`) |
| `UP-DEBT-limit.packages.telemetry.otel.2` and the other "no durable outbox" units (A18, A06) | records are lost on overflow, failure or exit | not changed by this library: the channel stays memory-only. A durable outbox is an area decision. |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-OTEL-R1` | The only signal is logs. A batch is one HTTP `POST` to a full `http(s)` URL with body = OTLP `ExportLogsServiceRequest` in JSON, `Content-Type: application/json`, optionally `Content-Encoding: gzip`. No traces, no metrics, no process-global provider. | `transport.ts:28`, `:53`; `packages/telemetry/otel/README.md:37` |
| `DH-LIB-OTEL-R2` | Nothing happens until a consumer creates a channel. The service alone, or a consumer that is disabled or has no endpoint, opens no socket, resolves no name, starts no goroutine and reads or creates no identity file. Resource attributes are evaluated at the first accepted record, not at creation. | `packages/telemetry/otel/src/index.ts:12`; `session-telemetry-otel/README.md:37`; change of `session-telemetry-otel/src/index.ts:198` |
| `DH-LIB-OTEL-R3` | Channels are independent: own queue, endpoint, headers, resource and scope. Records of two channels never share a request. | `packages/telemetry/otel/README.md:30`; note `2026-09-25-session-log-otel-byte-limits.md:29` |
| `DH-LIB-OTEL-R4` | A request carries exactly the configured headers plus the protocol headers. No `OTEL_*` environment variable changes any behaviour. Redirects are not followed. A client certificate is sent only if configured. | `packages/telemetry/otel/README.md:32`; `event-transport.ts:33`; widened by [DH-ADR-OTEL-2](#dh-adr-otel-2) |
| `DH-LIB-OTEL-R5` | Handing over a record never waits for the network. The queue is bounded; when it is full the new record is dropped and counted. | `packages/session/session-telemetry/README.md:36`; `session-log.ts:89-92` |
| `DH-LIB-OTEL-R6` | A request is bounded by record count and by uncompressed body bytes. The byte ceiling is set per channel and may not exceed a hard maximum fixed in the harness (upstream: 4,000,000, which a deployment may only lower). A single record over the ceiling is rejected whole, with one diagnostic, never truncated, and later records still go. One request is in flight per channel and order is kept. | `session-log.ts:12`, `:54-58`, `:102-105`, `:126-145` |
| `DH-LIB-OTEL-R7` | Each request has a deadline that really ends it. Network errors and HTTP 429, 502, 503 and 504 are retried with backoff inside that deadline, honouring `Retry-After`; any other status fails the batch. At most 4 MiB of response is read. A response that reports partial rejection is surfaced as a diagnostic. | `event-transport.ts:32-40`; change of `session-log.ts:147-152` |
| `DH-LIB-OTEL-R8` | Shutdown drains the queue until the caller's deadline. At the deadline it cancels the request in flight and any retry wait, discards what is queued, reports how many records were lost, and returns only after its goroutine has ended and idle connections are closed. A channel accepts nothing after shutdown. | `event-log.ts:83-93`; `session-telemetry-otel/src/index.ts:259-275`; `packages/host/product-telemetry-otel/src/index.ts:99-110` |
| `DH-LIB-OTEL-R9` | Diagnostics go to the local log and contain counts, sizes, status codes and error kinds only: never record content, attribute values or header values. | `session-log.ts:45` ("without recording their content") |
| `DH-LIB-OTEL-R10` | A record carries an event name, a string body, occurrence time, observed time, a severity, and attributes that are strings, integers, floats, booleans or one level of those. The exporter adds no attribute of its own: no host name, process id or SDK name. | `event-log.ts:13-28`, `:75`; `session-log.ts:212-217` |
| `DH-LIB-OTEL-R11` | Channel options are checked when the channel is created: URL scheme, positive limits, batch size not above queue size, byte ceiling in range. A bad option fails the creating component only. | `session-log.ts:54-69`; `packages/host/product-telemetry-otel/src/index.ts:67-83` |
| `DH-LIB-OTEL-R12` | Requests go through an HTTP client supplied by the harness, so the outbound proxy and certificate policy that apply to every other request apply to telemetry. | change of `packages/util/http-proxy/README.md:113` |
| `DH-LIB-OTEL-R13` | A channel can be closed without sending: when the user turns a flow's setting off, or sets the off-switch that covers every flow, the consumer closes its channel with no drain. The request in flight is cancelled, every queued record is discarded and counted, and no further connection is made. Withdrawn consent is never followed by a "last flush". | [DH-ADR-OTEL-3](#dh-adr-otel-3); change of `session-telemetry-otel/src/index.ts:259-275`, which drains on disposal |

## 5. Go choice

**Decision.** Own code in the harness: an OTLP/HTTP JSON logs exporter built on `net/http`,
`encoding/json`, `compress/gzip`, `context` and `log/slog`. Recorded as
[DH-ADR-OTEL-1](#dh-adr-otel-1).

**Why.** Upstream uses the SDK as a queue, a JSON encoder and an HTTP POST, and already
had to write its own batching and its own second transport around it (section 3). The
official Go SDK would not remove that work: its batch processor is count-only, and its
HTTP exporter sends protobuf, not the JSON upstream's collector is fed today.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **A. Own exporter, standard library only** (chosen) | No new module. Byte-exact batching because the exporter owns the encoder. Cancellation through `context`. No ambient configuration to neutralise. Nothing unused in the binary. | The harness owns about ten JSON message shapes and a retry loop, and must track the OTLP logs schema itself. No traces or metrics if they are wanted later. | No third-party module by construction. Unverified, to be measured: a size probe on 2026-11-18 (Go 1.27.4, `CGO_ENABLED=0`, `-trimpath -ldflags "-s -w"`) reported 6.84 MB for linux/amd64 and 7.05 MB for windows/amd64 for a throwaway program that posts one gzip JSON log record. |
| **B. OpenTelemetry Go SDK**: `go.opentelemetry.io/otel/log`, `.../otel/sdk/log`, `.../otel/sdk` (all v1.47.0) and `.../otel/exporters/otlp/otlplog/otlploghttp` v0.23.0 | Maintained by the OpenTelemetry project. Logs API and SDK are stable. Adds traces and metrics later without a second library. Standard `OTEL_*` configuration for operators. | The HTTP log exporter is still `v0.x`. It sends protobuf only. Batching is count-only and `WithMaxRequestSize` rejects an oversized batch instead of splitting it, so the byte-bounded processor is still own code. It reads `OTEL_*` variables for endpoint, headers, TLS files, timeout, compression, queue sizes and attribute limits unless every option is passed, and falls back to a default resource. Reportedly about 7 MB more binary (unverified). Requires Go 1.26. | All Apache-2.0 (LICENSE files in the module cache), released 2026-10-02 (`go list -m -json`, `go mod download -json`, 2026-11-18). `README.md:17-21` lists Logs "Stable"; `versions.yaml` puts `otel/log` and `otel/sdk/log` in `stable-v1` and both OTLP log exporters in `experimental-logs` v0.23.0. Protobuf only: `otlploghttp/doc.go:5-6`, `client.go:104`. Size limit: `client.go:172`. Environment: `config.go:109-152`, `sdk/log/batch.go:26-29`, `provider.go:28-29`, `:55`. `go 1.26.0` in each `go.mod`. Unverified, to be measured: the same size probe built on the SDK reported 14.18 MB linux/amd64 and 14.62 MB windows/amd64, 22 modules and 173 third-party packages compiled in, 65 of them gRPC, pulled by `go.opentelemetry.io/proto/otlp/collector/logs/v1`, and no package with cgo files. |
| **C. Own batching, official message types**: `go.opentelemetry.io/proto/otlp` v1.11.1 with `google.golang.org/protobuf` v1.36.12 | The schema comes from generated code. Can send protobuf or JSON. | Links protobuf, and gRPC through the collector package, to encode a handful of messages. Size not measured. | Apache-2.0 and BSD-3-Clause (LICENSE files), released 2026-09-30 and 2026-08-10, checked the same way. The request message lives in `collector/logs/v1`, which imports gRPC. |
| **D. `log/slog` bridge into the SDK**: `go.opentelemetry.io/contrib/bridges/otelslog` v0.21.0 | Any `slog` call becomes an exported record. | Needs option B, and makes the diagnostic log an export source, which [DH-ADR-OTEL-2](#dh-adr-otel-2) forbids. | Apache-2.0, released 2026-10-02, checked the same way. |

Inference: the 7.3 MB (linux) and 7.6 MB (windows) differences are the cost the SDK would
add to the one harness binary, because the baseline is almost entirely the Go runtime and
`net/http`, which the harness links anyway. The cost would be lower if another chosen
module already linked protobuf or gRPC; that was not checked.

**Tradeoff.** The harness maintains its own encoder and cannot claim SDK conformance. The
decision should be revisited if the owner wants traces or metrics, or wants operators to
configure export with the standard `OTEL_*` variables. The channel interface in section 6
hides the transport, so moving to option B later changes one package.

<a id="dh-adr-otel-1"></a>
### DH-ADR-OTEL-1 An own OTLP/HTTP JSON logs exporter; the OpenTelemetry SDK is not linked

- **Decision.** The seven `@opentelemetry/*` packages are replaced by one harness package
  on the standard library. Upstream's ordinary-event and session-log channel kinds become
  one channel type with options.
- **Why.** The needed behaviour is small and specific (section 4), and the parts that are
  hard (byte-bounded batching, cancellable shutdown, no ambient configuration) are the
  parts neither SDK provides.
- **Tradeoff.** Own encoder to maintain; logs only. See the table above.
- **Upstream verdicts.** Keep: logs only, JSON over HTTP, a factory service with
  caller-owned channels, "mounting creates nothing", explicit headers, the 4,000,000-byte
  ceiling and reject-not-truncate, best-effort memory-only delivery. Change: byte bounds
  on every channel, not only session logs; one encode per record; a deadline that ends
  the request; telemetry goes through the harness HTTP client and its proxy policy.
  Drop: the second transport on `got`, exact SDK pins, the unused meter plumbing.
- **Serves.** `DH-LIB-OTEL-R1`, `-R3`, `-R5` to `-R8`, `-R10` to `-R12`.
- **Verified by.** `DH-T-OTEL-1`, `-3`, `-5` to `-8`, `-10` to `-12`.

<a id="dh-adr-otel-2"></a>
### DH-ADR-OTEL-2 Records are explicit; the diagnostic log is never exported; no ambient configuration

- **Decision.** A record reaches a channel only through a call that names the channel.
  The exporter does not implement `slog.Handler`, so the harness's diagnostic log cannot
  be routed to a collector by installing a handler. `log/slog` is used for two things
  only: its `Attr`, `Value` and `Level` types are the record vocabulary, and the
  exporter's own diagnostics are written to the local logger. No `OTEL_*` environment
  variable is read. Nothing is constructed for a disabled consumer.
- **Why.** Upstream's privacy position is that the consumer selects every field and
  mounting collects nothing (`packages/host/product-telemetry-otel/README.md:57`). A
  diagnostic log contains paths, commands and error text that nobody selected. Ambient
  variables can add credentials or redirect data to a collector the user did not choose;
  upstream already refuses them for headers and TLS
  (`packages/telemetry/otel/README.md:32`), and the Go design refuses them for the rest
  as well, so that there is one place to look for where data goes.
- **Tradeoff.** Operators used to `OTEL_EXPORTER_OTLP_*` must use harness configuration
  instead. Exporting diagnostics for support would need a new, explicit consumer.
- **Serves.** `DH-LIB-OTEL-R2`, `-R4`, `-R9`.
- **Verified by.** `DH-T-OTEL-2`, `-4`, `-9`.

<a id="dh-adr-otel-3"></a>
### DH-ADR-OTEL-3 Nothing leaves the machine by default: each flow is a visible opt-in setting, one off-switch covers all, and feedback shows what will be sent

**Status: decided by the repository owner on 2026-11-18** (open decision OD-3 and the
decision "Data leaving the machine", recorded for the whole design as
[DH-ADR-9](../design.md#dh-adr-9)). This replaces the open question this record held;
it states what that decision means for the flows on this transport.
Verdict on upstream's defaults, which are "on" for both flows of section 1.1: **change**.

- **Decision.** Four rules, in the owner's terms.
  1. **Off by default, opt-in.** Every flow that sends data from the user's machine to a
     server is off in a fresh installation and stays off until the user turns it on. No
     profile, bundle or environment default turns one on.
  2. **Each flow is its own visible setting.** Session-log upload, product analytics and
     telemetry are three separate settings that a person can see and change in the
     settings page of the web UI and in configuration. A flow with no visible setting is
     not allowed to exist (upstream's analytics has none: section 1.1).
  3. **One off-switch covers all of them.** A single switch stops every flow whatever the
     three settings say. It wins over them and over any configured endpoint.
  4. **Feedback shows what will be sent before sending.** Before a feedback submission
     leaves the machine the user is shown what it contains, including whether the session
     is attached and how much of it, and confirms. Declining sends nothing.
- **How the rules map to upstream's flows.**

  | Flow | Upstream default | Go default | Turned on by |
  |---|---|---|---|
  | A. Session upload on feedback (section 1.1) | on, `FEEDBACK_ONLY` | **off** | the session-log upload setting, and then per submission by the confirmation of rule 4 |
  | B. Product analytics (section 1.1) | on, no user control | **off** | the analytics setting |
  | Any other record a component puts on a channel | none upstream | **off** | the telemetry setting |
  | Session log attached to official model requests, and the anonymous id header on model requests (not this family; section 1.1, third fact) | on | **off**: the owner's rule is "everything that leaves the machine", so both are covered. Their mechanism is specified by the areas that own them, not here | the session-log upload setting (Inference for the header: it identifies an installation and so falls under the same rule; the model-provider area confirms which setting governs it) |

- **What this library must do for the decision to hold.** `DH-LIB-OTEL-R2` already makes a
  consumer that is disabled inert: no socket, no name lookup, no goroutine, no identity
  file. Two things are added. `DH-LIB-OTEL-R13`: when consent is withdrawn while the host
  runs, the channel closes without draining. And the consumer, not the library, reads the
  settings; the library exposes no way to send that bypasses a consumer.
- **Consequences for the consuming areas.** Each opens with "Inference" because the owner
  decided the rules, not these details; the owning area agent confirms them.
  - Inference: a consumer creates its channel only when its own setting is on **and** the
    off-switch is not set, and closes it (no drain) when either changes. Both are read
    from live configuration, so no restart is needed to stop sending.
  - Inference: the off-switch is one configuration key plus one environment variable, so
    that an operator can set it for a whole fleet without editing each home. Upstream's
    rule "any non-empty value disables, even `0`"
    (`packages/boot/app-boot/src/profile-context.ts:42-55`) is kept for the variable,
    because a switch that can be mistyped into "on" is not a safe off-switch. Unlike
    upstream's `DSH_TELEMETRY_DISABLED`, it covers analytics too.
  - Inference: the anonymous installation id is created at the first authorised export,
    never at start-up as upstream does (section 1.1), so an installation that sends
    nothing has no id file. `DH-LIB-OTEL-R2` already evaluates resource attributes
    lazily, so nothing else is needed here.
  - Inference: the settings page states, for each flow, what is sent, to which address
    and under which identity, using the facts of section 1.1. A flow whose endpoint is
    not configured shows as unavailable instead of silently doing nothing.
  - Inference: the feedback confirmation is rendered by the host from the same records
    that would be exported (event count, byte size, first and last event time, and
    whether redaction rules are mounted), so what is shown cannot drift from what is
    sent. It belongs to the feedback area (A17).
- **Why.** Flow A uploads the complete session, with prompts, model output and tool
  results, and with no built-in redaction; flow B is not anonymous for a signed-in user
  and has no control (section 1.1). A default that sends either is a decision made for
  the user. Opt-in makes the user the one who decides, and one off-switch gives an
  operator a single fact to check.
- **Tradeoff.** The publisher receives far less data: most installations will send
  nothing, so problems are diagnosed from what users choose to submit. A feedback
  submission without the session attached is harder to act on. Three settings and a
  switch are more surface than upstream's one variable.
- **Serves.** [DH-O-7](../design.md#dh-o-7) (nothing the user did not approve leaves the
  machine), [DH-O-14](../design.md#dh-o-14) (the product says what it sends).
- **Verified by.** `DH-T-OTEL-2` (a disabled consumer is inert) and `DH-T-OTEL-13` (a
  withdrawn consent sends nothing more) in this document. The settings, the off-switch
  and the feedback confirmation are behaviour of the consuming areas; the scenario at
  the end of section 8 states what they must show against a real collector.

**Still open after the owner's decision.** These were not asked and are not settled by
the four rules. Each belongs to the area named.

| Question | Options | Facts that bear on it | For |
|---|---|---|---|
| Destination once a user opts in | (a) none compiled in: a flow stays unavailable until an operator configures a URL. (b) a collector run by the Go harness's publisher. (c) upstream's collector. | Upstream's default is a DeepSeek-operated host (`packages/host/product-telemetry-otel/src/index.ts:47`). Option (c) sends users' data to a party other than the Go harness's publisher. Inference: (a) is the only option that needs no further decision. | A18 |
| Redaction of an uploaded session | (a) no rules shipped, as upstream. (b) ship a default rule set that masks recognised secrets. (c) refuse to enable upload until at least one rule is mounted. | Without rules, records leave "exactly as captured, including any credentials embedded in file contents or command output" (`packages/session/session-telemetry/README.md:118`). Rule 4 makes the absence of rules visible to the user but does not add any. | A06 |
| Fields in product analytics | Whether account id, device id and hardware fields are included once the user opts in | Section 1.1, flow B | A18 |

## 6. Design notes

Inference: the exporter is one package in `src/harness`, for example `telemetry/otlplog`,
owned by the host-telemetry area. A sketch of the surface, to fix responsibilities and not
names:

```go
// Service is provided on the cordis context. Providing it allocates nothing.
type Service interface {
    NewChannel(ctx *cordis.Context, opts Options) (*Channel, error) // validates opts (R11)
}

type Options struct {
    Endpoint        string                 // full http(s) URL, required
    Headers         map[string]string      // sent as given; nothing is added from the environment
    Client          *http.Client           // from the harness HTTP client factory (R12)
    Gzip            bool
    Scope           Scope                  // name and version chosen by the consumer
    Resource        func() []slog.Attr     // called once, at the first accepted record (R2)
    MaxQueue        int                    // records
    MaxBatchRecords int
    MaxRequestBytes int                    // uncompressed body
    FlushInterval   time.Duration
    RequestTimeout  time.Duration
    Logger          *slog.Logger           // local diagnostics only (R9)
}

type Record struct {
    EventName string
    Body      string
    Time      time.Time
    Level     slog.Level
    Attrs     []slog.Attr
}

func (c *Channel) Emit(r Record) bool            // never blocks; false when dropped (R5)
func (c *Channel) Shutdown(ctx context.Context) error // drains until ctx ends (R8)
```

Area A18, which owns the exporter component, extends this sketch
([../areas/host-telemetry.md](../areas/host-telemetry.md), package `otlplog`): `Emit` returns a sequence number, and an `OnBatch` callback
reports each batch as delivered, failed, dropped or cancelled. A consumer with a
durable source, such as the session-log upload, needs that outcome to know what to
send again; the channel itself re-queues nothing. A channel also needs a permit from
the sharing gate before it sends anything ([DH-ADR-27](../design.md#dh-adr-27)).

- **Encoding.** `Emit` resolves the attributes and encodes the record to its JSON bytes
  immediately, so the caller's values may change afterwards and the size is known exactly.
  Field names follow the OTLP JSON mapping: `resourceLogs`, `scopeLogs`, `logRecords`,
  `timeUnixNano` and `observedTimeUnixNano` as decimal strings, `severityNumber`,
  `severityText`, `eventName`, `body`, `attributes` with `stringValue`, `intValue` (a
  decimal string), `doubleValue`, `boolValue` and `kvlistValue`. These names were checked
  against the generated types in `go.opentelemetry.io/proto/otlp` v1.11.1 on 2026-11-18.
  `slog.Level` maps to severity 5 (debug), 9 (info), 13 (warn) and 17 (error). A
  `slog.Value` of kind `Any` is refused, so that a struct cannot be serialised by accident.
- **Batching.** One goroutine per channel takes records from a bounded queue and packs
  them greedily until the count or byte bound is reached, adding the envelope size once.
  It sends when a bound is reached or the flush interval passes.
- **Ownership.** The consumer's fiber owns the channel. `NewChannel` registers an effect
  on that fiber whose disposer calls `Shutdown` with the consumer's deadline, so the
  queue, the goroutine and the idle connections are released when the consumer unloads or
  is disabled. The service owns nothing. The `*http.Client` is owned by whoever supplied
  it; the channel closes only connections it left idle. The collector may be *used* by
  any channel given its URL; no channel owns anything on it.
- **Failure and retry.** A failed batch is dropped after its deadline and counted; it is
  not re-queued, so a dead collector cannot grow memory. There is no rollback: a record
  that was posted cannot be recalled, which is why authorisation and redaction must
  happen before `Emit`. After a crash the queue is lost; whether a record arrived is
  unknown, and receivers must tolerate both loss and duplicates
  (`packages/session/session-telemetry/README.md:117`).
- **Withdrawn consent.** Inference: closing without sending (`DH-LIB-OTEL-R13`) needs no
  second method. `Shutdown` called with a context that is already cancelled has a drain
  deadline of zero, so by `DH-LIB-OTEL-R8` it cancels the request in flight, discards the
  queue, reports the count and returns. A consumer uses that form when its setting is
  turned off or the off-switch is set, and the ordinary form when the host stops.
- **Not in this library.** The three settings, the off-switch, the feedback confirmation,
  redaction rules, field selection and the anonymous id belong to the consuming areas
  ([DH-ADR-OTEL-3](#dh-adr-otel-3)). The library's part is that nothing is sent unless a
  consumer creates a channel, and that a channel can be closed without sending.

## 7. Risks and open questions

- **Owner decision made:** [DH-ADR-OTEL-3](#dh-adr-otel-3). The consuming areas design
  for "off unless the user turned this flow on, and off whenever the off-switch is set".
  Three questions remain open and are listed under that record: the destination, the
  redaction of an uploaded session, and the analytics fields.
- **The rule is only as wide as the list of flows.** "Everything that leaves the machine"
  must be enumerated to be checked. Section 1.1 names four flows (two on this transport,
  two outside it). [DH-ADR-9](../design.md#dh-adr-9) adds the reading for the rest: a
  model request to a provider the user configured is the product's function and is not
  switched off; an update check, and the client name and version reported to a vendor's
  model catalog, are treated as opt-in until the owner says otherwise. The areas that
  own each sender must list it under one of the settings, so that the off-switch's scope
  is not argued case by case later.
- **Collector compatibility is unproven.** Upstream's collector is not in the repository.
  If the Go harness must feed it, a scenario against that collector is required; local
  tests cannot show it.
- **Schema drift.** The OTLP logs schema is stable but can gain fields. Mitigation:
  `DH-T-OTEL-1` compares output with fixtures, and the area should add a scenario against
  a real OpenTelemetry Collector.
- **For A18 (host and telemetry):** this spec asks for a new harness package (section 6).
  A18 owns the deliverable, and decides whether ordinary events keep upstream's defaults
  (512 per request, 30 s interval, 21 s drain) or get shorter ones.
- **For A06 (session telemetry):** resource attributes are now lazy (`DH-LIB-OTEL-R2`), so
  the anonymous id need not exist before the first authorised upload.
- **For A17 (identity and feedback):** the feedback form must show what will be sent
  before sending (rule 4 of [DH-ADR-OTEL-3](#dh-adr-otel-3)). Whether upstream's dialog
  tells the user that the whole session is uploaded was not audited; its acknowledgement
  confirms recording only (`packages/feedback/command-feedback/src/index.ts:81`), so the
  confirmation step is new behaviour, not a port.
- **For L7 (HTTP libraries):** this spec needs a harness HTTP client factory that applies
  proxy and certificate policy and allows redirects to be turned off. `UP-DEP-got` is
  dispositioned there.
- **Go version.** If option B is ever taken, the harness module must declare Go 1.26 or
  later; the cordis module declares 1.24 (`src/go/go.mod`).

## 8. Verification

All tests are local: they run the exporter against an in-process HTTP test server. They
prove the exporter's behaviour, not delivery to a real collector. Nothing here has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-OTEL-1` | local | `DH-LIB-OTEL-R1`, `-R10` | One record of each attribute kind is emitted; the received body is compared field by field with an OTLP JSON fixture, and the method, path and content headers are checked, with and without gzip. No attribute appears that the test did not supply. |
| `DH-T-OTEL-2` | local | `DH-LIB-OTEL-R2` | The service is provided and a disabled consumer is loaded, with a dialer that counts connections and a resource function that counts calls. Both counts stay zero and no goroutine is added. After the first `Emit` on an enabled channel the resource function has run once. |
| `DH-T-OTEL-3` | local | `DH-LIB-OTEL-R3` | Two channels with different headers post to two servers; each server sees only its own records and headers. |
| `DH-T-OTEL-4` | local | `DH-LIB-OTEL-R4` | `OTEL_EXPORTER_OTLP_ENDPOINT`, `_HEADERS`, `_COMPRESSION` and `_CERTIFICATE` are set to hostile values; the request is unchanged. A 307 response is not followed. |
| `DH-T-OTEL-5` | local | `DH-LIB-OTEL-R5` | The server stalls; `Emit` returns within a millisecond bound each time; records beyond the queue size return false and the drop counter matches. |
| `DH-T-OTEL-6` | local | `DH-LIB-OTEL-R6` | Records are sized so that a count batch would exceed the byte ceiling; every received body is at or under the ceiling, requests do not overlap, order is kept, and one oversized record is rejected while its neighbours arrive. A ceiling above the hard maximum is refused. |
| `DH-T-OTEL-7` | local | `DH-LIB-OTEL-R7` | The server answers 503 with `Retry-After`, then 200: one retry after the stated wait. It answers 400: no retry. It never answers: the request ends at its deadline. A partial-rejection response produces a diagnostic. A response over 4 MiB is cut off. |
| `DH-T-OTEL-8` | local | `DH-LIB-OTEL-R8` | With a hung server, `Shutdown` returns at its deadline, the server sees the request cancelled, the lost count is reported, no goroutine remains, and a later `Emit` returns false. Unloading the consumer's fiber does the same. |
| `DH-T-OTEL-9` | local | `DH-LIB-OTEL-R9` | Every failure path above is run with marker strings in body, attributes and headers; the captured local log contains none of them. |
| `DH-T-OTEL-10` | local | `DH-LIB-OTEL-R11` | Each invalid option (bad scheme, zero limit, batch above queue, ceiling out of range) fails `NewChannel` with a field-named error and the sibling components keep running. |
| `DH-T-OTEL-11` | local | `DH-LIB-OTEL-R12` | The channel is given a client whose transport points at a test proxy; the proxy sees the request and the collector address is never dialled directly. |
| `DH-T-OTEL-12` | local | `DH-LIB-OTEL-R1` | The fixtures of `DH-T-OTEL-1` are decoded with the official OTLP message types in a separate test-only Go module, so the harness module gains no dependency. |
| `DH-T-OTEL-13` | local | `DH-LIB-OTEL-R13`, `DH-ADR-OTEL-3` | A channel has records queued and one request held open by the server. The consumer closes it with an already-cancelled context: the server sees that request cancelled and receives no later request, the discarded count equals the queue length plus the batch in flight, the dialer counts no new connection, and a later `Emit` returns false. Repeated with the consumer's setting switched off in live configuration, and with the off-switch set, through a fake consumer. |

A real-environment scenario belongs to the consuming areas and is not declared here.
Suggested shape, covering the four rules of [DH-ADR-OTEL-3](#dh-adr-otel-3): **Given** a
stock OpenTelemetry Collector with an OTLP/HTTP receiver and a file exporter, a harness
configured with that URL for every flow, and a fresh harness home; **When** (1) a session
is used and feedback is submitted with default settings, (2) the session-log upload
setting is turned on and feedback is submitted, first declining and then accepting the
confirmation, (3) the off-switch is set and feedback is submitted again; **Then** in (1)
the collector receives no connection at all and no identity file exists, in (2) the
confirmation states the event count that the collector's file then holds, nothing
arrives for the declined submission, and one record per session event up to the feedback
arrives for the accepted one, in (3) the collector receives no connection although the
setting is still on; **Cleanup** stop the collector, delete its output file and the
harness home; if a record arrived in (1) or (3), keep the collector's file and the
harness configuration as evidence.

## 9. Units dispositioned by this spec

- `UP-DEP-@opentelemetry/api`, `UP-DEP-@opentelemetry/api-logs`, `UP-DEP-@opentelemetry/core`,
  `UP-DEP-@opentelemetry/otlp-exporter-base`, `UP-DEP-@opentelemetry/otlp-transformer`,
  `UP-DEP-@opentelemetry/resources`, `UP-DEP-@opentelemetry/sdk-logs`: replaced by `DH-LIB-OTEL`.
- `UP-DEBT-found.L8.1` to `UP-DEBT-found.L8.4`: defined here and resolved.
