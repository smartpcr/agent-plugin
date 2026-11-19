# pi-ai family (`@earendil-works/pi-*`): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go modules named here were checked on 2026-11-18 with
> `go list -m -json <module>@latest` and `go mod download -json`, reading `LICENSE` and
> `go.mod` in the module cache (go1.27.4, windows/amd64). Nothing was built or run.

| | |
|---|---|
| **Library ID** | `DH-LIB-PIAI` |
| **Upstream units** | `UP-DEP-@earendil-works/pi-ai`, `UP-DEP-@earendil-works/pi-coding-agent` |
| **Upstream version** | 1.3.0 for both (`pnpm-lock.yaml:29186`, `:29240`); `pi-ai` carries a local patch (`pnpm-workspace.yaml:114`) |
| **Kind** | runtime |
| **Used by** | llm (`packages/llm/llm-pi-ai`), experimental (`packages/experimental/subagent-oar`) |
| **Go choice** | own code in the harness on the standard library (`net/http`, `encoding/json`); no multi-provider module, no vendor SDK in the request path |
| **Related areas** | model provider area (LLM runtime and adapters), credentials and authorization area, experimental subagent area. |

Packages in the family, one row each:

| Package | Imported by upstream source? | What it does for upstream | Go answer |
|---|---|---|---|
| `@earendil-works/pi-ai` in `packages/llm/llm-pi-ai` | Yes, 13 source files | Talks to every model provider except the first-party DeepSeek route; supplies the model catalogue and sign-in flows | Own code: `DH-LIB-PIAI`, [DH-ADR-PIAI-1](#dh-adr-piai-1), [DH-ADR-PIAI-2](#dh-adr-piai-2) |
| `@earendil-works/pi-ai` in `packages/experimental/subagent-oar` | No | Declared only because the package embeds the OAR library, which depends on it (`pnpm-lock.yaml:29040`) | Nothing to build |
| `@earendil-works/pi-coding-agent` | No | Same reason (`pnpm-lock.yaml:29041`); it is the agent that OAR's `pi` runtime runs inside the host process | Dropped: [DH-ADR-PIAI-4](#dh-adr-piai-4) |
| `@earendil-works/pi-telemetry` | No | A dependency of `pi-ai` (`pnpm-lock.yaml:29190`); has no unit of its own, only a pin | Gone with the library |

In the tables below, a path that starts with `src/` is short for
`packages/llm/llm-pi-ai/src/`.

## 1. What it is and why upstream needs it

Some terms first. A **provider** is a company or server that runs models (DeepSeek,
Anthropic, OpenAI, a company gateway, a local server). A **wire protocol** is the HTTP
request and streamed response format a provider speaks; four cover almost everything:
OpenAI Chat Completions, OpenAI Responses, Anthropic Messages and the Gemini API. A
**route** is one configured provider: a name, a protocol, an endpoint, a credential
reference and a list of models. An **adapter** is the harness code that turns the
harness's own request and stream vocabulary into one protocol and back. A **model
catalogue** is the table of facts about each model (context window, output cap, whether
it accepts images, which reasoning levels it has) that the harness needs before it sends
anything. **Replay metadata** is provider-private data (response ids, signatures on
reasoning blocks) that must be sent back unchanged on the next request or the provider
rejects it.

Upstream has two adapters. `llm-deepseek` is first-party code: it builds the HTTP request
itself and translates the stream itself. `llm-pi-ai` wraps `pi-ai`, a multi-provider
library with its own event vocabulary, and is what gives upstream every other provider
(`packages/llm/llm-pi-ai/README.md:12`). The library brings three things at once: the
protocol implementations, an installed catalogue of providers and models
(`packages/llm/llm-pi-ai/README.md:28`), and provider sign-in flows
(`packages/llm/llm-pi-ai/README.md:99`). It is itself built on the vendors' JavaScript
SDKs (`pnpm-lock.yaml:29188-29195`).

Upstream shipped two structurally different adapters on purpose, so that the shared
stream vocabulary could not silently take the shape of one implementation
(`.agents/notes/implemented/architecture/2026-06-13-twin-llm-adapters.md:13-18`). The
library adapter ships dormant: it registers no route until settings add one
(`packages/bundle/base/cordis.patch.yml:132-140`).

`pi-coding-agent` is a different thing with the same publisher: a complete coding agent
as a library. No upstream source imports it. It is declared because
`packages/experimental/subagent-oar` embeds the third-party OAR library into its
published file (`packages/experimental/subagent-oar/tsdown.config.ts:14`), and OAR's
registry includes a `pi` runtime
(`packages/experimental/subagent-oar/README.md:93`). Only the Codex and Claude runtimes
are tested by upstream (`packages/experimental/subagent-oar/README.md:159`).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/llm/llm-pi-ai/src/provider.ts:23-26`, `:48-53` | The four protocols a hand-declared route may name | `anthropicMessagesApi`, `googleGenerativeAIApi`, `openAICompletionsApi`, `openAIResponsesApi` |
| `packages/llm/llm-pi-ai/src/adapter.ts:475-482` | One streamed model call: model, context, key, reasoning level, temperature, output cap, session id, cancellation signal, headers | `Models.streamSimple` |
| `packages/llm/llm-pi-ai/src/adapter.ts:163-164` | Turning the library's own retries off | `maxRetries: 0` |
| `packages/llm/llm-pi-ai/src/stream.ts:153-254` | Translating the library's events into harness stream chunks; re-encoding parsed tool arguments as raw JSON text (`:219-221`) | `AssistantMessageEvent` union |
| `packages/llm/llm-pi-ai/src/stream.ts:49-76` | Guessing a failure code from the error message text, because the library discards the HTTP status and the original error (`:41-48`) | `errorMessage` string |
| `packages/llm/llm-pi-ai/src/context.ts:18` | Converting harness messages, tools and images to the library's context | `Context`, `Message`, `Tool` types |
| `packages/llm/llm-pi-ai/src/replay.ts:13`, `src/prefix-binding.ts:23` | Storing and restoring replay metadata; dropping signed Anthropic reasoning when the request prefix changed | `AssistantMessage`, `ToolCall` types |
| `packages/llm/llm-pi-ai/src/catalog.ts:15`, `src/models.ts:3`, `:32-36` | The installed provider and model catalogue, as defaults that a profile overrides field by field | `builtinProviders`, `getBuiltinModels`, `builtinModels` |
| `packages/llm/llm-pi-ai/src/models.ts:60-92` | Reasoning levels a model supports; whether it accepts system and tool changes mid-conversation | `thinkingLevelMap`, `compat` flags |
| `packages/llm/llm-pi-ai/src/catalog.ts:75-139` | Compile-time lists that must match the library's types exactly ("drift gates") | `ModelThinkingLevel`, `OpenAICompletionsCompat` and other types |
| `packages/llm/llm-pi-ai/src/login.ts:10`, `src/auth.ts:14` | Sign-in (OAuth or a typed key) and the credential store the library reads and refreshes | `AuthEvent`, `AuthPrompt`, `CredentialStore`, `AuthContext` |
| `packages/llm/llm-pi-ai/scripts/gen-provider-data.ts` (described at `packages/llm/llm-pi-ai/README.md:179-193`) | Probing the library to learn which environment variables each provider reads, then checking in the result | public resolvers with synthetic answers |
| `packages/experimental/subagent-oar/package.json:51-52` | Nothing in source (`src/index.ts:10` and `src/run.ts:17` import only OAR) | none |

What upstream does **not** take from the library: model listing over the wire is
upstream's own `fetch` code for all four protocols
(`packages/llm/llm-pi-ai/README.md:119`), cost data is never read
(`packages/llm/llm-pi-ai/src/catalog.ts:33-38`), and stop sequences are refused because
the library's common options cannot carry them
(`packages/llm/llm-pi-ai/src/adapter.ts:411-413`).

## 3. Tech debt upstream carries

Search performed: every import of the three packages under `packages/`, `apps/`,
`python/` and `scripts/`; `patches/`; every comment and entry for them in
`pnpm-workspace.yaml`; the "Known Limitations and Deferred Work" and "Dev Note" sections
of `packages/llm/llm-pi-ai/README.md`, `packages/llm/llm/README.md` and
`packages/experimental/subagent-oar/README.md`.

**What the patch fixes.** `patches/@earendil-works__pi-ai@1.3.0.patch` edits the library's
compiled output in six protocol files. It makes two changes.

1. *Quadratic work on streamed tool arguments.* On every fragment of a tool call's
   arguments the library re-parsed the whole text received so far. The patch deletes that
   line in all six stream implementations (`patches/@earendil-works__pi-ai@1.3.0.patch:9`,
   `:21`, `:33`, `:54`, `:84`, `:96-97`). Unpatched, "a multi-megabyte argument stream
   costs O(n²) CPU on the event loop and stalls every session in the process"
   (`packages/llm/llm-pi-ai/README.md:276`). The harness never wanted the parsed value: its
   contract is raw JSON text end to end (`docs/subsystems/llm-streaming.md:299`).
2. *A provider field the library dropped.* Gemini, reached through an OpenAI-compatible
   endpoint, attaches a signature to each tool call and requires it back on the next
   request. The patch reads it from the response
   (`patches/@earendil-works__pi-ai@1.3.0.patch:45-49`) and writes it into the next
   request (`:62-64`, `:72`). Unpatched, Gemini rejects the next step of a tool-calling
   turn with HTTP 400 (`packages/llm/llm-pi-ai/README.md:279`).

Both are the same kind of problem: the harness needs the wire as it is, and a library
that sits between the harness and the wire had decided otherwise.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-patch.@earendil-works__pi-ai@1.3.0` | The two fixes above, applied to a dependency's compiled files and re-applied or retired by hand on every upgrade | `patches/@earendil-works__pi-ai@1.3.0.patch:1-100`; `packages/llm/llm-pi-ai/README.md:276`, `:279` | Resolved by [DH-ADR-PIAI-1](#dh-adr-piai-1): the harness owns the stream decoder, so fragments are forwarded without parsing (`DH-LIB-PIAI-R3`) and provider-private fields are kept (`DH-LIB-PIAI-R5`). There is no third-party file to patch. |
| `UP-DEBT-pin.allowBuilds.@google/genai` | pnpm refuses to install a package that ships an install script until it is listed; this one is listed as denied | `pnpm-workspace.yaml:36-39` | Not applicable: install scripts are an npm mechanism. Go never runs module code when it downloads a module. The package is also absent from the Go design, because no multi-provider library pulls it in. |
| `UP-DEBT-pin.allowBuilds.protobufjs` | Same, for a dependency of `@google/genai` (`pnpm-lock.yaml:29807`) | `pnpm-workspace.yaml:36-40` | Not applicable, same reason. |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@earendil-works/pi-ai@1.3.0` | The workspace's supply-chain rule makes a new release wait before it may be installed. This library is exempted, because "fresh pi-ai releases carry the model catalog updates that are the whole point of bumping it" | `pnpm-workspace.yaml:67-69` | Resolved by [DH-ADR-PIAI-2](#dh-adr-piai-2): the catalogue is a data file owned by the harness. Learning about a new model changes data, not code, so no code dependency has to be taken the day it is published. |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@earendil-works/pi-telemetry@1.3.0` | The same exemption for a package that only `pi-ai` depends on | `pnpm-workspace.yaml:70`; `pnpm-lock.yaml:29190` | Resolved by [DH-ADR-PIAI-1](#dh-adr-piai-1): without the library its dependencies are not in the build. |
| `UP-DEBT-found.L2.1` | The adapter is welded to one exact library version by machinery that exists only to notice the library changing: compile-time "drift gates", a generated provider-metadata file with its own pre-commit and CI check, and a digest seed that must be changed by hand when the library changes how it serializes Anthropic messages | `packages/llm/llm-pi-ai/src/catalog.ts:44-51`, `:70-83`, `:92-112`; `packages/llm/llm-pi-ai/README.md:179-193`, `:277`, `:290` | Resolved by [DH-ADR-PIAI-1](#dh-adr-piai-1) and [DH-ADR-PIAI-2](#dh-adr-piai-2): the option lists, the provider metadata and the serialized request are the harness's own, so there is nothing external to drift from. The request-prefix digest is taken over the bytes actually sent (`DH-LIB-PIAI-R5`). |
| `UP-DEBT-found.L2.2` | Three dependencies are declared by packages whose source never imports them: `pi-ai` and `pi-coding-agent` in `subagent-oar` (dependencies of an embedded library) and `@anthropic-ai/sdk` in `subagent-claude-code` (a peer dependency of another package, see [anthropic-aisdk.md](anthropic-aisdk.md)) | `packages/experimental/subagent-oar/package.json:51-52`, `tsdown.config.ts:14`; `packages/subagent/subagent-claude-code/package.json:52` | Not applicable: both causes are npm rules (a bundle must re-declare what it leaves outside; a consumer must supply peer packages). A Go module's requirements are the packages its code imports, and `go mod tidy` removes the rest. |

Limits that the consuming package admits and that the library causes. Each bullet is a
tech-debt unit owned by the model provider area; they are listed here because the library
choice decides whether they survive.

| Limit (`packages/llm/llm-pi-ai/README.md` line) | Cause in the library | Removed by |
|---|---|---|
| HTTP status is unavailable; failures are classified from message text (`:274`) | Errors are flattened to a string (`src/stream.ts:41-48`) | `DH-LIB-PIAI-R7` |
| Stop sequences are unsupported (`:271`) | The common stream options cannot carry them | `DH-LIB-PIAI-R11` |
| A local server with no key needs a placeholder credential (`:269`) | The OpenAI-compatible implementation insists on a key | `DH-LIB-PIAI-R10` |
| Mid-conversation system and tool updates work only for models the installed catalogue flags (`:272`) | The flags live in the library's catalogue and profiles cannot set them | `DH-LIB-PIAI-R12` |
| Gemini accepts only four reasoning level spellings (`:270`) | The library's Gemini implementation | `DH-LIB-PIAI-R12` |
| Later system messages had to be rewritten by the adapter before the library saw them (`.agents/notes/implemented/architecture/2026-10-19-pi-ai-system-reminder-fallback.md:9-13`) | The library merges them into the first prompt, which breaks prompt-cache reuse | The fallback rule is kept ([../upstream/decisions-2.md](../upstream/decisions-2.md), row 89); the driver places messages itself |

## 4. Requirements for the Go implementation

These are requirements on the *library layer*: what must be true of whatever code speaks
to providers. The adapter contract itself (chunk types, failure codes, retry policy) is
the model provider area's.

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-PIAI-R1` | Several provider routes are served from configuration. A changed route takes effect on the next request without a restart; a request already running finishes with the endpoint, model facts and credential it started with. | `packages/llm/llm-pi-ai/README.md:12`; `src/adapter.ts:4-14` |
| `DH-LIB-PIAI-R2` | A route authenticated by a key and headers can speak any of four wire protocols: OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, Gemini API. | `src/provider.ts:48-53`; `docs/user/guide/providers.md:29` |
| `DH-LIB-PIAI-R3` | Streamed tool-call arguments are forwarded as text fragments exactly as received. Nothing parses the accumulated text per fragment; it is checked once, when the call is complete. Work is linear in the size of the arguments. | the patch; `docs/subsystems/llm-streaming.md:299` |
| `DH-LIB-PIAI-R4` | Stream order holds for every protocol: blocks are numbered in first-seen order, usage comes before the finish, nothing follows the finish, and a response that ends without the protocol's terminal event is a failure, never a success. | `docs/cookbook/adding-an-llm-adapter.md:27-29`; `src/stream.ts:253` |
| `DH-LIB-PIAI-R5` | Provider-private replay metadata is kept without loss and returned on the next request to the same model: response ids, signed and redacted reasoning, signatures on tool calls (including the Gemini signature that arrives through an OpenAI-compatible endpoint), encrypted reasoning details. An unknown field in a response is never a reason to lose a known one. The digest that decides whether signed Anthropic reasoning is still valid is computed from the request bytes actually sent. | the patch `:45-72`; `packages/llm/llm-pi-ai/README.md:197`, `:199`, `:277`, `:279` |
| `DH-LIB-PIAI-R6` | One call is exactly one HTTP request attempt. No layer below the harness's retry listener retries. | `docs/subsystems/llm-streaming.md:301`; `src/adapter.ts:163-164` |
| `DH-LIB-PIAI-R7` | A failure keeps its facts: HTTP status, the provider's error type and code, the `Retry-After` delay, the provider's request id, and the original transport error as the cause. Classification uses those fields; message text is the last resort. | `packages/llm/llm-deepseek/src/transport.ts:22-55`; `src/stream.ts:41-48` |
| `DH-LIB-PIAI-R8` | The caller's cancellation ends the HTTP request and closes the connection. A provider that sends no bytes for a configured time (five minutes by default) fails as a timeout; a keep-alive comment counts as activity; a caller cancel that came first stays a cancel. | `docs/subsystems/llm-streaming.md:302`; `packages/llm/llm-deepseek/src/sse.ts:14`; `src/adapter.ts:503-509` |
| `DH-LIB-PIAI-R9` | Every provider request carries the application's `User-Agent`, the route's configured headers and any header a plugin contributes, with the application identity applied last. Redirects are refused, so a credential never leaves the configured origin. The harness's outbound proxy policy applies. | `docs/subsystems/llm-streaming.md:305`; `src/adapter.ts:249-275`; `packages/llm/llm-deepseek/src/adapter.ts:151`; `packages/util/http-proxy/README.md:32` |
| `DH-LIB-PIAI-R10` | A credential is resolved for each request through the harness credential service and is never stored by the driver. A named reference that resolves to nothing fails before any network I/O. A route that names no credential sends none; no placeholder is needed. | `packages/llm/llm-pi-ai/README.md:38`, `:269` |
| `DH-LIB-PIAI-R11` | A request option the protocol supports is sent (stop sequences where the protocol has them). An option it cannot honour fails the call before I/O; it is never dropped silently. | `docs/cookbook/adding-an-llm-adapter.md:32`; `src/adapter.ts:411-413` |
| `DH-LIB-PIAI-R12` | Endpoints that deviate from the protocol's reference are handled by named switches that configuration can set per route and per model: which role carries the system prompt, the output-cap field name, how a reasoning level travels, the reasoning-budget field, whether system and tool changes are accepted mid-conversation. | `src/catalog.ts:92-139`; `packages/llm/llm-pi-ai/README.md:107-111`, `:272`; `docs/user/guide/providers.md:184` |
| `DH-LIB-PIAI-R13` | Known providers come with defaults: endpoint, protocol, credential variable names, and for each model the context window, output cap, input types and reasoning levels. A profile can replace the model list or correct single models. The catalogue advises; it does not restrict which model id may be requested. | `packages/llm/llm-pi-ai/README.md:77-87`, `:103`; `docs/subsystems/llm-streaming.md:871` |
| `DH-LIB-PIAI-R14` | Token usage is reported with input, cache-read and cache-write counted separately, reasoning inside output, and the provider's own total kept when it reports one. | `src/stream.ts:24-32`; `docs/subsystems/llm-streaming.md:338` |
| `DH-LIB-PIAI-R15` | A provider whose authentication is not a key (a cloud credential chain or an OAuth sign-in) can be added as its own component without changing the four protocol drivers or the stream vocabulary. | `src/provider.ts:36-46`; `packages/llm/llm-pi-ai/README.md:99`, `:261`, `:289` |

## 5. Go choice

The question to settle: what gives the Go harness more than one model provider?

**Decision.** The harness speaks each wire protocol itself, on `net/http`, with one small
server-sent-events reader ([eventsource-parser.md](eventsource-parser.md)). There is no
multi-provider module and no vendor SDK between the harness and the wire. The model
catalogue is data the harness owns. Three decision records follow the comparison.

### Options compared

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **A. Chosen: own protocol drivers on the standard library** | The harness sees every byte, status and header, which is what requirements R3, R5, R7 and R8 ask for. No dependency to patch, pin or wait for. Pure Go. | The harness maintains four protocol encoders and decoders and their deviation switches, and must follow wire changes itself. | Standard library only. Upstream's own direct adapter is the size reference: request, stream, framing, error and wire-type code is 473 lines in five files (`packages/llm/llm-deepseek/src/serialize.ts`, `translate.ts`, `sse.ts`, `transport.ts`, `wire-types.ts`). |
| **B. Official Go SDK per vendor** | Typed request and response structures kept current by the vendor. Vendor authentication for cloud platforms is included. | Three unrelated APIs to adapt. The types describe the vendor's own service, while most routes are *compatible* gateways whose extra fields carry the facts R5 and R12 need. Heavy module graphs. Frequent releases; the OpenAI module is on its third major import path. Retries are on unless turned off. | `github.com/anthropics/anthropic-sdk-go` v1.84.0 (2026-10-20), MIT, `go 1.24`; requires AWS, Google API, OpenTelemetry and MCP modules. `github.com/openai/openai-go/v3` v3.103.0 (2026-11-18), Apache-2.0, `go 1.26.0`; requires Azure SDK and AWS SDK modules; `/v2` last released 2025-09-30 and v1 2025-07-30. `google.golang.org/genai` v1.79.1 (2026-11-18), Apache-2.0, `go 1.25.0`; requires Google Cloud auth, gRPC and protobuf modules. Seen in source: both `option.WithMaxRetries` and `option.WithHTTPClient` exist in the first two; the Anthropic error type has `StatusCode` and `RequestID`; its tool-argument delta is a raw string (`PartialJSON`). No `import "C"` in any of the three modules' own source. |
| **C. A multi-provider Go module** | One API for many providers, closest to what `pi-ai` is. | Recreates the position that produced the patch: a third party's stream vocabulary and option set between the harness and the wire. The one examined is pre-1.0 and depends on a fork. | `charm.land/fantasy` v0.48.1 (2026-11-13), Apache-2.0, `go 1.27.4`. Its `go.mod` requires both SDKs from option B, a fork of the OpenAI SDK at a pseudo-version (`github.com/charmbracelet/openai-go v0.0.0-20260921175203-216db9e71b83`), and one more provider module that was not examined (`github.com/ardanlabs/kronk`). It streams tool arguments as raw deltas (`model.go`), has its own error type (`errors.go`), and retries in its own agent layer (`retry.go`, `agent.go`) while its Anthropic provider sets the SDK's retries to zero (`providers/anthropic/anthropic.go:277`). Also confirmed to exist, not examined further: `github.com/tmc/langchaingo` v0.1.15 (MIT), `github.com/cloudwego/eino` v0.9.23 (Apache-2.0), `github.com/mozilla-ai/any-llm-go` v0.9.0, `github.com/sashabaranov/go-openai` v1.43.0. |

### The options against the requirements

| Requirement | A. Own drivers | B. Vendor SDKs | C. Multi-provider module |
|---|---|---|---|
| R3 raw tool-argument fragments | By construction | Available (raw delta strings) | Available in the one examined |
| R5 lossless replay metadata, including fields on compatible gateways | By construction: the decoder keeps unknown fields as raw JSON | Only through each SDK's escape hatch for unmodelled fields, per SDK | Only what the module's metadata type carries; anything else needs a fork or patch, as upstream found |
| R6 one attempt | By construction | Must be configured on every client | Met in the provider that was read, as long as the module's agent layer is not used; not checked for its other providers |
| R7 failure facts | By construction | Available on the SDK error types that were inspected | Module's own error type; completeness not verified |
| R8 idle timeout that counts keep-alive bytes | Measured on the response body | Possible only by wrapping the HTTP client's body, outside the SDK | Same as B, two layers down |
| R9 headers, no redirects, proxy | One `http.Client` owned by the harness | Possible through a custom HTTP client per SDK | Possible through options, per provider package |
| R11 stop sequences | Sent where the protocol has them | Available | Depends on the module's common options, the limit upstream hit |
| R12 deviation switches | The driver's own table | Mostly outside the typed API | Fixed by the module |
| R13 catalogue | Harness data | Not provided | Separate module (see [DH-ADR-PIAI-2](#dh-adr-piai-2)) |
| R15 cloud credential chains | Not provided; needs a vendor module for signing | Provided | Provided for the platforms it wraps |
| Minimum Go version forced | none | `go 1.26.0` | `go 1.27.4` |

Inference. Options B and C satisfy most rows, but each "possible through" is adapter code
written against someone else's abstraction, and that is the code upstream had to patch,
gate and regenerate (section 3). Option A's cost is real and bounded, and it is the same
kind of code upstream already writes for its first-party route.

<a id="dh-adr-piai-1"></a>
### DH-ADR-PIAI-1 The harness speaks provider wire protocols itself

- **Decision.** Each of the four wire protocols in `DH-LIB-PIAI-R2` is a harness-owned
  driver: an encoder from the harness request to one HTTP request, and a decoder from the
  response stream to harness chunks. All drivers share one transport layer (HTTP client,
  server-sent-events reader, idle timer, failure facts). The first-party DeepSeek adapter
  uses the same Anthropic Messages driver and adds its own files, extension fields and
  authentication around it. No multi-provider module and no vendor SDK is in the request
  path.
- **Why.** Every piece of debt in section 3 comes from a library standing between the
  harness and the wire: it parsed what should have stayed text, dropped a field it did not
  know, flattened errors to strings, and tied the adapter to its release schedule. The
  harness contract is written in wire terms (raw argument text, opaque replay metadata,
  status and `Retry-After`, one attempt), so owning the wire meets it directly.
- **Tradeoff.** The harness maintains the drivers and their deviation switches and must
  notice protocol changes itself. Cloud platforms that need request signing are not
  covered by this decision ([DH-ADR-PIAI-3](#dh-adr-piai-3)). Upstream's "two unlike
  adapters" check loses its second, library-built implementation; see section 7.
- **Upstream verdict.** *Keep* the first-party direct adapter and its layout (wire types,
  serialization, framing, translation, adapter as separate responsibilities,
  `docs/cookbook/adding-an-llm-adapter.md:39`). *Change* the library-backed adapter into
  three more drivers of the same kind. *Drop* the library, its patch, its drift gates and
  the provider-metadata generator.
- **Serves.** `DH-LIB-PIAI-R1` to `R12`, `R14`.
- **Verified by.** `DH-T-PIAI-1` to `DH-T-PIAI-8`, and the real-provider scenarios the
  model provider area defines.

<a id="dh-adr-piai-2"></a>
### DH-ADR-PIAI-2 The model catalogue is data owned by the harness

- **Decision.** Provider and model defaults live in one versioned data file embedded in the
  binary, with a schema the harness defines (`DH-LIB-PIAI-R13`, plus the per-model switches
  of `R12`). Three layers resolve a model's facts, later ones winning: the embedded file,
  the route's configuration, and for endpoints that list their models, live discovery
  offered for adoption. A maintainer tool refreshes the embedded file from outside sources
  at development time; that tool is not part of the shipped program.
- **Why.** Upstream gets its catalogue from inside a code library, so learning about a new
  model means installing new third-party code at once, and the supply-chain waiting period
  had to be switched off for it (`pnpm-workspace.yaml:67-69`). Data and code have different
  risk; separating them removes the exemption instead of copying it. Upstream already
  generates and checks in part of this data (`packages/llm/llm-pi-ai/README.md:179-193`);
  this decision makes the whole catalogue work that way.
- **Options considered.**

  | Option | Verdict | Verified facts |
  |---|---|---|
  | Embedded data file with the harness's own schema | Chosen | none needed |
  | Import a catalogue module: `charm.land/catwalk/pkg/embedded` | Not chosen as a dependency. It would tie new models to a dependency release again, and its model record has no field for per-model protocol, mid-conversation update support or reasoning format. It is a candidate *source* for the refresh tool. | `charm.land/catwalk` v0.52.86 (2026-10-28), MIT, `go 1.26.10`; 42 embedded provider files; the model record holds id, name, costs, context window, default output cap, a reasoning flag and levels, and an image flag (`pkg/catwalk/provider.go`); the module's `go.mod` requires a Prometheus client |
  | Ask a catalogue service at run time | Not chosen: a model request must not depend on a third service being reachable | not applicable |

- **Tradeoff.** Someone must run the refresh and review the diff. The embedded file is as
  old as the build; configuration and discovery cover the gap, and an unlisted model can
  always be declared by hand, as upstream allows.
- **Serves.** `DH-LIB-PIAI-R13`.
- **Verified by.** `DH-T-PIAI-9`.

<a id="dh-adr-piai-3"></a>
### DH-ADR-PIAI-3 Vendor modules are allowed for vendor authentication only

- **Decision.** A provider whose authentication cannot be expressed as a key, an endpoint
  and headers is a separate provider component. It may depend on the vendor's own Go
  module for obtaining and refreshing credentials and for signing or framing that only that
  vendor uses. It still produces the harness stream through the shared transport and a
  harness driver wherever the wire is one of the four protocols. Each such module gets its
  own library item when the model provider area schedules the component.
- **Why.** Upstream's own table of protocols excludes exactly these cases: "Bedrock signs
  with SigV4 over AWS credentials and a region, Vertex needs a project, a location, and
  application-default credentials, Azure needs provider environment plus an api-version,
  and Codex authenticates through OAuth" (`packages/llm/llm-pi-ai/src/provider.ts:38-41`).
  Credential chains and request signing are security code that should come from the vendor.
  Keeping them in separate components means a deployment that does not use a platform does
  not link its module.
- **Tradeoff.** Upstream reaches these platforms as soon as the library is installed, with
  ambient credentials (`packages/llm/llm-pi-ai/README.md:261`). In Go each one is a
  component that must be designed, scheduled and tested. Until then that platform is not
  reachable, except through a key-authenticated endpoint of the same vendor (upstream's own
  real-provider test reaches Azure OpenAI that way,
  `.github/workflows/pi-ai-provider-e2e.yml:49`, `:67`).
- **Candidate modules, existence checked only.** `github.com/aws/aws-sdk-go-v2/service/bedrockruntime`
  v1.63.13 (2026-11-10) and `golang.org/x/oauth2` v0.38.0 (2026-11-06, BSD-3-Clause,
  licence read). The Bedrock module's licence and module graph were not read; it stays
  open until its component is specified.
- **Serves.** `DH-LIB-PIAI-R15`.
- **Verified by.** `DH-T-PIAI-10`.

<a id="dh-adr-piai-4"></a>
### DH-ADR-PIAI-4 No coding agent is linked into the host as a library

- **Decision.** The Go harness does not embed another coding agent in its own process.
  `@earendil-works/pi-coding-agent` has no Go counterpart. An outside coding agent is
  reached as a separate process, which is already the rule for third-party code
  ([DH-ADR-4](../design.md#dh-adr-4)).
- **Why.** Upstream never imports the package; it arrives because an embedded library
  lists it. What it would enable, OAR's `pi` runtime, is not tested by upstream
  (`packages/experimental/subagent-oar/README.md:159`), and upstream's own web-worker
  runtime replaces every one of its functions with a refusal
  (`packages/experimental/webworker-runtime/src/node/external_packages/pi-coding-agent.ts:1`,
  `:12-15`). A JavaScript agent cannot be called inside a Go process in any case.
- **Tradeoff.** If the experimental subagent area keeps a Pi runtime, it must start the Pi
  program as a child process and speak a protocol to it, as it does for Codex and Claude
  Code. That is that area's decision; this record only rules out the in-process form.
- **Serves.** [DH-O-11](../design.md#dh-o-11) (third-party code runs as a separate
  process) and [DH-O-14](../design.md#dh-o-14) (one pure-Go binary).
- **Verified by.** The import-direction test of
  [DH-ADR-ARCH-9](../upstream/architecture.md#dh-adr-arch-9), and a check that the harness
  module's requirements contain no agent library.

**Tradeoff of the whole choice.** The harness gives up a ready-made list of several dozen
providers that work the moment a key is entered. What it ships first is four protocols,
which covers every key-authenticated provider and gateway, plus a catalogue that is only
as good as its refresh. Cloud platforms and third-party sign-ins arrive later, one
component at a time.

## 6. Design notes

**Package shape.** Inference: three packages under the harness module are enough, and the
model provider area may rename them.

- A transport package: builds the one `http.Client` used for provider calls (proxy policy,
  redirects refused), wraps a response body so that every successful read resets the idle
  timer, reads server-sent events, and turns a non-2xx response or a transport error into
  one failure value with the fields of `DH-LIB-PIAI-R7`.
- One package per wire protocol with two pure halves: `Encode(call, route) (*http.Request, error)`
  and `Decode(events) iter.Seq2[Chunk, error]`. Neither half does I/O, so both are tested
  from recorded bytes.
- A catalogue package: loads the embedded file, applies configuration, answers "facts for
  this provider and model".

**What a driver keeps as raw JSON.** Tool arguments, and every response member the decoder
does not model, are held as `json.RawMessage`. Replay metadata is built from those raw
values, so a field added by a provider tomorrow survives a round trip today. This is the
structural fix for the second half of the patch.

**Ownership.** The HTTP client and its connection pool belong to the provider component's
fiber; unloading it closes idle connections. One response body belongs to one call. The
call's `context.Context` derives from the turn
([DH-ADR-ARCH-3](../upstream/architecture.md#dh-adr-arch-3)); cancelling it aborts the
request, and the stream closes the body on every exit path: finish, failure, cancel, and a
consumer that stops reading. The driver holds no credential after the request is built.

**Failure and retry.** A failure before the first byte and a failure mid-stream produce
the same failure value. Chunks already delivered are not taken back: the loop records them
as a failed attempt, as upstream does (`docs/subsystems/llm-streaming.md:300`). The driver
never retries; replay metadata is produced only with a successful finish. Nothing needs
rollback on the provider side, with one uncertainty that cannot be removed: a request that
was sent and then cancelled may still have been billed.

**Idle timeout.** The timer runs only while the driver is waiting for bytes, so a slow
consumer does not trip it, which is upstream's rule (`docs/subsystems/llm-streaming.md:302`).
Measuring at the body, not at decoded events, makes keep-alive comments count without the
event reader having to report them.

## 7. Risks and open questions

1. **Scope change to confirm with the product owner.** Under this choice, cloud-credential
   platforms (Bedrock, Vertex, Azure with Entra ID) and sign-ins to third-party
   subscriptions (upstream offers OAuth where the library ships a login,
   `packages/llm/llm-pi-ai/README.md:99`) are separate later components, not something a
   dependency provides on day one. Which of them are built, and in what order, is for the
   model provider and credentials areas. Whether the harness should carry sign-ins to
   another company's consumer subscription at all is a product and legal question this
   specification does not answer.
2. **The twin-adapter rule.** Upstream verifies its vocabulary with two implementations
   built on different internals. With four drivers on one transport the difference is in
   the protocols, not the internals. Inference: that is still a real check, because the
   four protocols disagree in the ways that matter (Gemini returns tool arguments as
   objects, Responses streams items, Messages streams indexed blocks), and upstream itself
   names a conformance suite as the thing that could retire one twin
   (`.agents/notes/implemented/architecture/2026-06-13-twin-llm-adapters.md:27`). The model
   provider area must state the replacement rule; `DH-T-PIAI-8` is the library-level part.
3. **Protocol drift.** Vendors add event types and fields. Decoders must ignore unknown
   event types and keep unknown fields (upstream does the first already,
   `packages/llm/llm-deepseek/src/translate.ts:124-126`). Real-provider scenarios, run on
   demand because they cost money, are the only detection; they belong to the model
   provider area.
4. **Catalogue source.** Which outside source the refresh tool reads is open. One checked
   candidate is in [DH-ADR-PIAI-2](#dh-adr-piai-2).
5. **Needed from other areas.** A credential service that resolves a reference per request
   and stores refreshable grants under a cross-process lock; the outbound proxy policy as
   an `http.RoundTripper` or proxy function; the application identity for `User-Agent`.

## 8. Verification

All tests below are local: they run a driver against bytes recorded or written by hand,
served by an in-process HTTP server where a connection is needed. They prove the stated
requirement for those inputs. They do not prove behaviour against a real provider; that
needs real-environment scenarios with real credentials, which the model provider area
owns.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-PIAI-1` | local | `DH-LIB-PIAI-R1`, `DH-LIB-PIAI-R2`, `DH-LIB-PIAI-R10` | Four routes, one per protocol, are configured against a fake server. Each request reaches its own endpoint in its own format with its own credential. A route is changed while a stream is open: the open stream finishes on the old endpoint, the next call uses the new one. A named credential that resolves to nothing fails with no connection made; a route with no credential sends no authentication header. |
| `DH-T-PIAI-2` | local | `DH-LIB-PIAI-R3` | For each protocol, a tool call whose arguments arrive in many thousands of fragments is decoded. The emitted fragments equal the received ones byte for byte, and a benchmark shows time growing linearly with argument size. Invalid final JSON fails once, at the end. |
| `DH-T-PIAI-3` | local | `DH-LIB-PIAI-R4`, `DH-LIB-PIAI-R14` | Recorded streams for each protocol, including a trailing usage-only event and a stream cut before its terminal event. Asserts block numbering, usage before finish, nothing after finish, failure on the cut stream, and the disjoint token counts. |
| `DH-T-PIAI-4` | local | `DH-LIB-PIAI-R5` | A response carrying signed reasoning, a tool-call signature in a gateway-specific member, and a member the decoder has never seen is decoded and its replay metadata encoded into the next request. All three appear unchanged. A changed request prefix removes signed Anthropic reasoning and keeps text and tool calls. |
| `DH-T-PIAI-5` | local | `DH-LIB-PIAI-R6`, `DH-LIB-PIAI-R7` | The fake server answers 429 with `Retry-After` and a request id, then 500, then drops the connection mid-stream. Each call makes exactly one request and returns a failure holding the status, delay, request id and cause. |
| `DH-T-PIAI-6` | local | `DH-LIB-PIAI-R8` | A server that stalls trips the idle timeout; a server that sends only keep-alive comments does not; a caller cancel during a stall reports a cancel, and the server observes the connection closed. Run with the race detector. |
| `DH-T-PIAI-7` | local | `DH-LIB-PIAI-R9` | The request seen by the fake server has the application `User-Agent` even when a contributed header tries to replace it. A 302 answer fails the call and no request reaches the redirect target. With a proxy configured the request arrives through it. |
| `DH-T-PIAI-8` | local | `DH-LIB-PIAI-R11`, `DH-LIB-PIAI-R12` | One conformance table runs every driver through the same logical cases (text, reasoning, parallel tool calls, output cap, stop sequence, each deviation switch) and compares the request bytes with a stored expectation. An option a protocol lacks fails before any request. |
| `DH-T-PIAI-9` | local | `DH-LIB-PIAI-R13` | The embedded catalogue file validates against its schema. Resolution order is asserted: embedded default, then a route's model list, then a single-model override. An unlisted model id is still accepted for a request. |
| `DH-T-PIAI-10` | local | `DH-LIB-PIAI-R15` | A test-only provider component with its own authentication step registers a route and streams through the shared transport and an existing driver, with no change to either. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-@earendil-works/pi-ai`, `UP-DEP-@earendil-works/pi-coding-agent`,
`UP-DEBT-patch.@earendil-works__pi-ai@1.3.0`, `UP-DEBT-pin.allowBuilds.@google/genai`,
`UP-DEBT-pin.allowBuilds.protobufjs`,
`UP-DEBT-pin.minimumReleaseAgeExclude.@earendil-works/pi-ai@1.3.0`,
`UP-DEBT-pin.minimumReleaseAgeExclude.@earendil-works/pi-telemetry@1.3.0`,
`UP-DEBT-found.L2.1`, `UP-DEBT-found.L2.2`.
