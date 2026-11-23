# Model providers and streaming: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". IDs in this document use the area key `LLM`. Nothing here has
> been built, run or tested.

| | |
|---|---|
| **Area key** | `LLM` (cluster `A04`) |
| **Upstream packages** | 9, all under `packages/llm`: the contract, two adapters for one vendor, two vendor route plugins, the retry executor, two request-extension packages, the token meter |
| **Serves outcomes** | [DH-O-8](../design.md#dh-o-8) (owned here); also [DH-O-4](../design.md#dh-o-4), [DH-O-3](../design.md#dh-o-3), [DH-O-14](../design.md#dh-o-14) |
| **Depends on areas** | A01 and A02 (session events, the driver), A05 (session-log sender), A06 (projection registry), A07 (machine-state tables, attachments), A14 (compaction), A17 (credentials, account, sign-in), A18 (the one off-switch), A21 (config layers), A23 (`egress`, `schema`, the replay provider), C3 (the model picker, [DH-D-UICHAT-7](ui-chat.md#dh-d-uichat-7)) |
| **Priority** | P0: without one provider, one stream contract and bounded retry there is no agent. Other protocols, the catalogue and the meter are P1. Cloud sign-ins are P2 by [DH-ADR-25](../design.md#dh-adr-25) |

## 1. Purpose

This area is how the harness talks to a language model. It turns one logged model request into one HTTP call to the provider the user chose, turns the streamed answer back into a small vocabulary every other part understands (text, reasoning, tool calls, token counts, why it stopped), and reports a failure as a fact with a code. It also decides when a failed call is tried again, lists the models a provider offers, and estimates how full the model's context is. Without it nothing can ask a model anything; with it done badly, a provider's quirks leak into the session log, a failure silently bills a different account, or a retry loop spends money without a record.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **Provider**, **vendor** | A provider is a company or server that runs models (Anthropic, a company gateway, a local server). The vendor is DeepSeek, the provider upstream is built around. | The provider is what is billed and trusted with the conversation. |
| **Wire protocol** | The HTTP request and streamed response format a provider speaks. Four are in scope: Anthropic Messages, OpenAI Chat Completions, OpenAI Responses, Gemini API. | Many providers share one protocol, so code is written per protocol, not per provider. |
| **Route** | One configured provider: an exact id, one protocol, an endpoint, one credential source and a model set. | A request names a route; the route decides who is billed. |
| **Driver** | Pure code for one protocol: an encoder from the harness request to HTTP bytes and a decoder from response events to chunks. | Tested from recorded bytes, with no network. |
| **Adapter** | The Go interface a provider component implements to serve routes: describe, list models, prepare a call. | The seam that makes providers swappable. |
| **Attempt** | One call to a provider: one prepared request, one response stream, one terminal fact. | The unit that is logged, retried and billed. |
| **Chunk**, **finish** | A chunk is one element of the response stream; the set is closed (section 6.2). The finish is the last chunk of every attempt: stop, tool calls, output cap reached, aborted, or error with a failure. | One vocabulary for the driver loop, the log and the UI; success, failure and cancellation are read the same way. |
| **Failure** | A record of what went wrong: code, class, message, HTTP status, the provider's requested delay, its request id. It holds facts and no policy. | Policy decides from facts, never from message text. |
| **Replay state** | Provider-private data (response ids, signatures on reasoning) that must be sent back unchanged to the same route. | The provider rejects the next request without it. |
| **Server-sent events (SSE)** | The text framing providers use to stream a response over one HTTP connection. | Defined in [libs/eventsource-parser.md](../libs/eventsource-parser.md). |
| **Model catalogue**, **listing** | The catalogue is the table of facts per model: context window, output cap, input types, reasoning levels, update modes. A listing is the model list a provider's own endpoint returns. | Facts are needed before a request is sent and are owned as data ([DH-ADR-PIAI-2](../libs/earendil-works-pi.md#dh-adr-piai-2)); a listing lets new models appear without a release. |
| **Update mode** | Whether a model accepts a changed system prompt or tool list in the middle of a conversation (`in-history`) or only at its start. | Decides whether a change can be appended, keeping the provider's cached prefix. |
| **Request contribution** | A named extra field or header added to a provider request outside the model's input (for example a session-log suffix). | Vendor diagnostics; in Go each one is opt-in. |
| **Token meter** | A service that estimates request size and context occupancy from the session log, with no model call. | Compaction and the UI share one number. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.llm.llm` | `dsh-llm` | definition | Message, content-block and stream vocabulary; the adapter registry `ctx.llm`; call preparation; failure normalisation; the block assembler and the compact stream record (`packages/llm/llm/README.md:117-128`) |
| `UP-PKG-packages.llm.llm-deepseek` | `dsh-llm-deepseek` | provider (library) | The vendor's Messages transport: serialise, send, frame SSE, translate; image upload through a Files API; listing and version gate (`packages/llm/llm-deepseek/README.md:147-151`) |
| `UP-PKG-packages.llm.llm-deepseek-api-key` | `dsh-llm-deepseek-api-key` | provider | Registers route `deepseek-official` with an API-key reference and the official listing (`packages/llm/llm-deepseek-api-key/README.md:12-14`) |
| `UP-PKG-packages.llm.llm-deepseek-account` | `dsh-llm-deepseek-account` | provider | Registers route `deepseek-account` with the signed-in account token, the per-account listing and sign-out cancellation (`packages/llm/llm-deepseek-account/README.md:12-14`, `:59`) |
| `UP-PKG-packages.llm.llm-pi-ai` | `dsh-llm-pi-ai` | provider | Every other provider through the `pi-ai` library; mounted with zero routes until settings add one (`packages/llm/llm-pi-ai/README.md:12`; `packages/bundle/base/cordis.patch.yml:132-140`) |
| `UP-PKG-packages.llm.llm-retry` | `dsh-llm-retry` | consumer (policy) | Listens on the loop's failed-request extension point and re-runs the step under the route's retry policy (`packages/llm/llm-retry/README.md:12`, `:72`) |
| `UP-PKG-packages.llm.deepseek-llm-api-extensions` | `dsh-deepseek-llm-api-extensions` | definition | Registry `ctx.deepseekLlmApiExtensions` of extra top-level request fields for the official route (`packages/llm/deepseek-llm-api-extensions/README.md:12`) |
| `UP-PKG-packages.llm.plugin-package-inventory-deepseek` | `dsh-plugin-package-inventory-deepseek` | consumer | Sends the list of active npm plugin packages as field `dsh_plugin_packages`, on by default (`packages/llm/plugin-package-inventory-deepseek/README.md:12`, `:30`) |
| `UP-PKG-packages.llm.token-meter` | `dsh-token-meter` | provider (core) | `ctx.tokenMeter` and three session projections: `tokenUsage`, `contextPressure`, `contextBreakdown` (`packages/llm/token-meter/README.md:12`) |

### 3.2 Behaviour

| Step | What upstream does | Evidence |
|---|---|---|
| Prepare | The loop calls `prepareCall()`, which binds the model's facts, config defaults and retry policy to the adapter registration that will serve the call, so a settings change cannot mix two generations | `packages/llm/llm/src/index.ts:942`; `packages/llm/llm/README.md:132` |
| Dispatch | The assembled request (`GenerateOptions`: provider, model, messages, tools, sampling, cancellation signal) passes the `llm/stream` waterfall, where a listener may short-circuit it (replay) or reroute a one-shot request, and reaches the adapter registered for that provider id | `docs/subsystems/llm-streaming.md:661-703`, `:871` |
| Stream | The adapter yields `block-start`, `text-delta`, `reasoning-delta`, `tool-call-delta`, `block-end`, `usage`, `finish`. Contract: usage before finish, nothing after it, tool arguments stay raw JSON text, one call is one provider attempt, five idle minutes is a timeout, an empty completion is an error | `docs/subsystems/llm-streaming.md:203-215`, `:298-306` |
| Settle | The runtime turns whatever the adapter throws into one terminal `finish` with a failure. The loop logs the chunks in compact timed form inside `assistant/message` on success, or `assistant/attempt` otherwise | `.agents/notes/implemented/architecture/2026-07-29-terminal-llm-stream-failures.md:17`; `docs/subsystems/llm-streaming.md:222-224` |
| Providers | The vendor is served twice. `llm-deepseek` owns the wire and sends an API key or an account token, never falling back between them. `llm-pi-ai` wraps a multi-provider library, built on different internals on purpose so the vocabulary cannot take one implementation's shape | `packages/llm/llm-deepseek/README.md:93-95`; `.agents/notes/implemented/architecture/2026-06-13-twin-llm-adapters.md:13-18` |
| Listing and gate | Both vendor routes follow the vendor's `GET /models`, refreshed on a ten-minute timer. The client hides a model whose `client_requirements["deepseek-harness"].min_version` is above its own version | `packages/llm/llm-deepseek-account/README.md:30-34`; `packages/llm/llm-deepseek/src/listing-cache.ts:172-177`; `packages/llm/llm-deepseek/src/listing.ts:104-113` |
| Extra data sent | Every request carries `User-Agent: deepseek-harness/<version> (+url) dsh-profile/<profile>`. Vendor model requests add a stable anonymous id for the harness home, the session id and a compaction marker. Two body fields are on by default: the active npm package list and a suffix of the whole session log (prompts, tool results, working directory). A gateway behind `baseURL` receives the same | `packages/llm/llm-deepseek/src/adapter.ts:152-161`; `docs/deepseek-llm-api-wire-extensions.md:24-31`, `:43`, `:76`, `:160` |
| Retry | `llm-retry` has no config; each route owns a `retryPolicy`. Normal mode: five retries for `EMPTY_RESPONSE`, `RATE_LIMIT`, `SERVER`, `TIMEOUT`, `TRANSPORT`, backoff 500 ms to 10 s. Always mode: every failure, no limit. `llm/retry` is logged before the wait and `llm/retry-started` before the step re-runs | `packages/llm/llm-retry/README.md:50`; `packages/llm/llm/src/retry-policy.ts:15-18`; `packages/llm/llm-retry/src/index.ts:185-187` |
| Token meter | `measure(session)` replays the log and prices the request with a character heuristic, reusing provider usage only for an identical request envelope. `tokenUsage` folds billed tokens from every attempt, failed ones included | `packages/llm/token-meter/README.md:36-45`; `.agents/notes/implemented/architecture/2026-07-29-projected-token-usage-and-request-context.md:17` |

Services: `ctx.llm`, `ctx.deepseekLlmApiExtensions`, `ctx.tokenMeter`. Live events: `llm/stream` and `llm-pi-ai/request-headers` (waterfall), `llm/adapters-updated` and `llm/models-updated` (emit) (`docs/subsystems/llm-streaming.md:1194-1286`). Durable events written here: `llm/retry`, `llm/retry-started`. No tool is exposed to the model.

### 3.3 Failure, retry, rollback and cleanup upstream

- A failed attempt commits no assistant message and no tool side effect; the partial stream is kept in `assistant/attempt`, outside model history (`docs/subsystems/llm-streaming.md:300`).
- A cancelled wait or a disposed retry plugin aborts the backoff and fails closed (`packages/llm/llm-retry/README.md:54`).
- One "attempt" of the vendor adapter may send more than one HTTP request: image uploads retry, a stale file id permits one replacement model request, and a failed upload rebuilds the request with inline images (`packages/llm/llm-deepseek/src/adapter.ts:113-125`, `:170`).
- Request-extension acceptance runs after HTTP 2xx; a crash before the watermark is stored resends a range and never skips one (`docs/deepseek-llm-api-wire-extensions.md:39`, `:156`).
- A process that dies mid-stream leaves no record of the unfinished attempt. **Uncertain upstream and in Go:** a request that was sent and then cancelled may still be billed.

## 4. Upstream decisions dissected

Notes are under `.agents/notes/`; the row label is the note's file name without date.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `twin-llm-adapters` | Two unlike adapters for one vendor prove the vocabulary is neutral | change | The second twin was the npm library the Go design removes; the check is kept as a rule on four drivers | [DH-ADR-LLM-5](#dh-adr-llm-5) |
| `bounded-llm-request-recovery` | Failures are serialisable facts; a listener decides retry within bounds | keep | It is the shape DH-O-8 asks for | [DH-ADR-LLM-4](#dh-adr-llm-4) |
| `provider-routed-llm-adapters` | The provider id, not the model id, selects the adapter; one owner per provider | keep, change the backend | Exact routing is the base of "no silent fallback"; the generic backend becomes own drivers | [DH-ADR-LLM-3](#dh-adr-llm-3), [DH-ADR-PIAI-1](../libs/earendil-works-pi.md#dh-adr-piai-1) |
| `identified-immutable-message-values` | Every message gets an id at creation and is frozen | keep | Go enforces it by constructors and unexported fields, not a run-time freeze | [DH-ADR-LLM-1](#dh-adr-llm-1) |
| `projected-token-usage-and-request-context` | Billing and occupancy are projections of the whole log | keep | A UI window cannot be the source of billing | [DH-ADR-LLM-9](#dh-adr-llm-9) |
| `terminal-llm-stream-failures` | The runtime is the one place that turns an adapter failure into a terminal chunk | keep | One failure shape for every consumer; a consumer's own error is never a model failure | [DH-ADR-LLM-2](#dh-adr-llm-2) |
| `deepseek-llm-api-request-extensions` | A registry of extra body fields for the official route, on by default | change | Kept as a mechanism, but each field is off until the user opts in, per route | [DH-ADR-LLM-7](#dh-adr-llm-7) |
| `producer-owned-message-sources` | A message's source kind names its producer | keep | The old-format conversion table is not needed ([DH-ADR-2](../design.md#dh-adr-2)) | [DH-D-LLM-1](#dh-d-llm-1) |
| `account-provider-signout` | Two routes, two credentials, no fallback; sign-out cancels work that uses the account | keep | Falling back would change who is billed | [DH-ADR-LLM-3](#dh-adr-llm-3) |
| `deepseek-account-remote-model-catalog` | Follow the account listing; gate by client version; cache in memory | keep, change two parts | The last good listing is stored; a refused model is shown as unavailable, not hidden | [DH-ADR-LLM-6](#dh-adr-llm-6) |
| `context-breakdown-system-prompt-state` | Projection state keeps system prompts and a running total only | keep | Bounded projection state is a rule for every Go projection | [DH-ADR-LLM-9](#dh-adr-llm-9) |
| `pi-ai-system-reminder-fallback` | On a model without mid-conversation system support, later system text becomes a user-role reminder | keep | The rule (the driver adapts, the log stays truthful) holds for any driver | [DH-D-LLM-4](#dh-d-llm-4) |
| `deepseek-official-model-listing` | Follow the listing only while the catalogue is uncustomised | change | "Customised" was judged by comparing with the built-in list; a keyed overlay removes the comparison | [DH-ADR-LLM-6](#dh-adr-llm-6) |
| `context-form-vocabulary` | A message source may declare a semantic form (instructions, catalog, notice, ...) | keep | A tag on the value; presentation belongs to the UI areas | [DH-D-LLM-1](#dh-d-llm-1) |
| `deepseek-messages-adapter` | The vendor is reached through the Anthropic Messages protocol with lossless replay | keep | The protocol mapping is provider behaviour | [DH-D-LLM-2](#dh-d-llm-2) |
| `listed-model-badges` | A short badge beside a model name comes from the listing, validated on the host | keep | Display data only; never in a request or the log | [DH-D-LLM-5](#dh-d-llm-5) |
| `deepseek-deferred-tool-failures` | No placeholder tool; the provider's rejection is surfaced | keep | A placeholder changes the cached prefix and advertises a tool nothing runs | [DH-ADR-LLM-2](#dh-adr-llm-2) |
| `prune-stream-visibility-helpers` (proposed) | Remove five unused stream queries | keep, by omission | The Go package has the first-token reader and no unused query | [DH-D-LLM-1](#dh-d-llm-1) |
| `builtin-timer-promises-for-hand-rolled-sleeps` (rejected) | Whether to use a Node timer helper | not applicable | A cancellable wait in Go is a `select` on a timer and the context | none |

Decisions visible only in code or READMEs:

| Upstream decision | Verdict | Reason | Go decision |
|---|---|---|---|
| Content blocks, finish reasons and message sources are merge-extensible by any plugin (`docs/subsystems/llm-streaming.md:23-31`, `:713-719`) | change | Plugins are processes and cannot add a block type; a closed set is checked by the compiler | [DH-ADR-LLM-1](#dh-adr-llm-1) |
| An adapter may throw or end with an error chunk (`docs/subsystems/llm-streaming.md:300`) | keep | Go: return an error or yield a terminal chunk; the runtime treats both alike | [DH-ADR-LLM-2](#dh-adr-llm-2) |
| A `llm/stream` listener may reroute a one-shot request (`docs/subsystems/llm-streaming.md:871`) | drop the reroute | A reroute is a fallback nobody logged | [DH-ADR-LLM-3](#dh-adr-llm-3) |
| Replay state passes only inside one adapter instance (`docs/subsystems/llm-streaming.md:306`) | change to "one route" | One Go component serves many routes, so "same instance" would be too wide | [DH-ADR-LLM-3](#dh-adr-llm-3) |
| Always mode retries every failure without limit (`packages/llm/llm-retry/README.md:50`) | change | Unbounded retry of an authentication or quota failure spends money for nothing | [DH-ADR-LLM-4](#dh-adr-llm-4) |
| A model catalogue advises and never restricts which id may be requested (`docs/subsystems/llm-streaming.md:871`) | keep | A new model must be usable before any catalogue knows it | [DH-ADR-LLM-6](#dh-adr-llm-6) |
| Session log, package list and three identifying headers are sent by default (`docs/deepseek-llm-api-wire-extensions.md:26-29`, `:43`, `:76`) | change | Overruled by [DH-ADR-9](../design.md#dh-adr-9) | [DH-ADR-LLM-7](#dh-adr-llm-7) |
| The listing refreshes on a timer while idle (`packages/llm/llm-deepseek/src/listing-cache.ts:172-177`) | change | An idle timer is a periodic signal to the vendor; refresh on access is the default | [DH-ADR-LLM-7](#dh-adr-llm-7) |
| Provider usage is disjoint: uncached input, cache read, cache write, output (`docs/subsystems/llm-streaming.md:338`) | keep | One accounting rule for four protocols | [DH-D-LLM-1](#dh-d-llm-1) |
| Images reach a provider only as harness-held bytes, never as a URL (`packages/llm/llm-deepseek/README.md:217`) | keep | A URL the provider fetches is model-visible content that is not in the log | [DH-ADR-LLM-3](#dh-adr-llm-3) |

## 5. Tech debt and its resolution

IDs omit the prefix `UP-DEBT-limit.packages.llm.`; evidence is the package's `README.md` at the line given. "Carried" rows are limits the Go design keeps; the ledger marks them `carried` with a justification.

| Debt ID | What it is | Evidence | Resolution in Go |
|---|---|---|---|
| `deepseek-llm-api-extensions.1` | Extra fields exist for the official route only | `:49` | Contributions are named and listed per route; any route may opt in ([DH-ADR-LLM-7](#dh-adr-llm-7)) |
| `deepseek-llm-api-extensions.2` | No field-order contract | `:50` | Contributed fields are appended in name order, so request bytes are reproducible ([DH-D-LLM-2](#dh-d-llm-2)) |
| `llm-deepseek-account.1` | Listing cache is memory only | `:96` | The last good listing is stored in a machine-state table and served, marked stale, on an offline start ([DH-ADR-LLM-6](#dh-adr-llm-6)) |
| `llm-deepseek-account.2` | A listing change is not announced | `:97` | `llm.ModelsChanged` is notified on every change ([DH-D-LLM-5](#dh-d-llm-5)). The bullet is stale upstream: the README body already says changes emit `llm/models-updated` (`:32`) |
| `llm-deepseek-account.3` | The version gate runs in the client and a modified client can skip it | `:98` | **Carried.** No client can enforce a rule on a modified copy of itself. The gate is documented as a compatibility guard, not a security control |
| `llm-deepseek-account.4` | The listing's effort levels are ignored | `:99` | **Carried.** The field that carries them is not read anywhere in the pinned source, so its shape is unknown. Levels come from the catalogue and can be corrected per model in config |
| `llm-deepseek-account.5` | With no servable model the picker shows a generic empty state | `:100` | A refused model is returned with `Unavailable{reason, minVersion}`, so the picker can say "needs a newer build" ([DH-ADR-LLM-6](#dh-adr-llm-6)) |
| `llm-deepseek-account.6` | An override cannot remove an update mode | `:101` | Overlay fields take an explicit `none` value ([DH-ADR-LLM-6](#dh-adr-llm-6)) |
| `llm-deepseek-account.7` | With the plugin disabled, running work is not reported at sign-out and a tool keeps running | `:102` | The logged request header records the credential reference name; sign-out cancels every running agent whose latest header names it, whichever components are loaded ([DH-ADR-LLM-3](#dh-adr-llm-3)) |
| `llm-deepseek-api-key.1` | A configured catalogue is not validated remotely | `:86` | An on-demand route check sends one small real request per chosen model and reports the classified result ([DH-D-LLM-4](#dh-d-llm-4)) |
| `llm-deepseek-api-key.2` | The Models page and the listing disagree; editing drops listed models | `:87` | One resolved model set serves the page, the picker and the request ([DH-ADR-LLM-6](#dh-adr-llm-6)) |
| `llm-deepseek-api-key.3` | A customised catalogue gets no new listed models | `:88` | Config is an overlay keyed by model id; the listing still contributes ids ([DH-ADR-LLM-6](#dh-adr-llm-6)) |
| `llm-deepseek-api-key.4` | "Customised" is judged against the running build's built-in list | `:89` | No comparison exists; an overlay entry is explicit ([DH-ADR-LLM-6](#dh-adr-llm-6)) |
| `llm-deepseek-api-key.5` | Listing cache is memory only | `:90` | As `llm-deepseek-account.1` |
| `llm-deepseek-api-key.6` | A listing change is not announced | `:91` | As `llm-deepseek-account.2` |
| `llm-deepseek-api-key.7` | With no servable model the route offers nothing | `:92` | As `llm-deepseek-account.5` |
| `llm-deepseek.1` | Replacing `models` replaces the whole list | `:213` | Keyed overlay, edited per model ([DH-ADR-LLM-6](#dh-adr-llm-6)) |
| `llm-deepseek.2` | `tool_choice` is not mapped | `:214` | `Controls.ToolChoice` exists and every driver encodes it or refuses before I/O ([DH-ADR-LLM-1](#dh-adr-llm-1)) |
| `llm-deepseek.3` | Raw `fetch`, no shared proxy | `:215` | Every client comes from `egress` ([DH-ADR-UNDICI-1](../libs/undici.md#dh-adr-undici-1)) |
| `llm-deepseek.4` | An in-history system update with no retained user turn fails serialisation | `:216` | The driver consolidates an update it cannot place into the leading system slot for that request and logs a diagnostic; placement never fails a request ([DH-D-LLM-2](#dh-d-llm-2)) |
| `llm-deepseek.5` | Images are input only, never URLs; upload recovery waits for a count of skipped requests | `:217` | **Carried in part.** The skip count becomes a time-based cool-down ([DH-D-LLM-5](#dh-d-llm-5)). No URL images and no assistant image output are kept on purpose ([DH-ADR-LLM-3](#dh-adr-llm-3)) |
| `llm-deepseek.6` | The default catalogue advertises a model without probing | `:218` | The route check above; a refused model fails with a classified code ([DH-D-LLM-4](#dh-d-llm-4)) |
| `llm-deepseek.7` | Real API checks need a key and several environment flags | `:219` | Scenarios take the key at run time in a separate credentialed workflow, and a skipped scenario is open, never passed ([DH-ADR-TOOLCHAIN-12](../upstream/toolchain.md#dh-adr-toolchain-12), [DH-E-LLM-1](#dh-e-llm-1)) |
| `llm-pi-ai.1` | The image byte bound counts only base64 image payload | `:257` | The harness encodes the body itself, so the bound is the encoded request size ([DH-D-LLM-5](#dh-d-llm-5)) |
| `llm-pi-ai.2` | The byte bound has no removal quantum | `:258` | One image policy for all drivers, with byte and count quanta ([DH-D-LLM-5](#dh-d-llm-5)) |
| `llm-pi-ai.3` | A sign-in is lost on page reload; sign-out does not tell the issuer | `:259` | A pending sign-in is an interaction in the host registry, so a reload re-attaches; sign-out calls the issuer's revocation endpoint where one exists ([DH-D-LLM-10](#dh-d-llm-10)). A host restart still abandons it |
| `llm-pi-ai.4` | No ChatGPT sign-in, because it needs an installation id | `:260` | OAuth routes are a later component with an opt-in installation id ([DH-D-LLM-10](#dh-d-llm-10)); whether to offer a third party's consumer sign-in is an owner question (section 10) |
| `llm-pi-ai.5` | The library reads environment and credential files itself | `:261` | Drivers get credentials only from the credential service ([DH-ADR-PIAI-1](../libs/earendil-works-pi.md#dh-adr-piai-1)). A cloud component's vendor module reads its own chain; that is stated in [DH-D-LLM-7](#dh-d-llm-7) |
| `llm-pi-ai.6` | Reset restores an inherited route | `:262` | Layer behaviour made visible: the ownership index shows which layer supplies a route, and the writable layer can `remove` it ([DH-ADR-LOADER-4](../libs/cordis-loader.md#dh-adr-loader-4)) |
| `llm-pi-ai.7` | Whole-config replacement can drop inherited entries | `:263` | Routes and models merge by key; removal is an explicit operation ([DH-ADR-LOADER-4](../libs/cordis-loader.md#dh-adr-loader-4), [DH-ADR-LLM-6](#dh-adr-llm-6)) |
| `llm-pi-ai.8` | `headers` can hold a credential the redactor never sees | `:264` | A header value is plain text or a credential reference; a plain value under a credential-bearing name is refused at load ([DH-D-LLM-4](#dh-d-llm-4)) |
| `llm-pi-ai.9` | Discovery never changes configured models | `:265` | A route chooses `listing: offer` or `follow` ([DH-ADR-LLM-6](#dh-adr-llm-6)) |
| `llm-pi-ai.10` | Discovery reads one page of 1,000 models | `:266` | Discovery follows pagination to a bounded page count and reports truncation ([DH-D-LLM-4](#dh-d-llm-4)) |
| `llm-pi-ai.11` | An uncatalogued model on a mixed-protocol route is ambiguous | `:267` | A route has exactly one protocol; a vendor with two is two routes ([DH-ADR-LLM-6](#dh-adr-llm-6)) |
| `llm-pi-ai.12` | A declared input type is not verified | `:268` | The route check probes declared input types on request ([DH-D-LLM-4](#dh-d-llm-4)) |
| `llm-pi-ai.13` | A keyless route needs a placeholder key | `:269` | A route that names no credential sends none (`DH-LIB-PIAI-R10`, [DH-ADR-PIAI-1](../libs/earendil-works-pi.md#dh-adr-piai-1)) |
| `llm-pi-ai.14` | Gemini accepts four level spellings | `:270` | Level spellings and "cannot disable reasoning" are catalogue data per model ([DH-D-LLM-4](#dh-d-llm-4)) |
| `llm-pi-ai.15` | Stop sequences unsupported | `:271` | Sent where the protocol has them (`DH-LIB-PIAI-R11`, [DH-D-LLM-4](#dh-d-llm-4)) |
| `llm-pi-ai.16` | Update modes come only from the installed catalogue | `:272` | Update modes are overlay fields any route can set ([DH-ADR-LLM-6](#dh-adr-llm-6)) |
| `llm-pi-ai.17` | An image in a system or assistant message fails the request | `:273` | Each driver states which roles carry images; elsewhere the image is sent as the same placeholder text a text-only route gets ([DH-D-LLM-5](#dh-d-llm-5)) |
| `llm-pi-ai.18` | HTTP status unavailable; failures classified from text | `:274` | The transport keeps status, headers and cause (`DH-LIB-PIAI-R7`, [DH-ADR-LLM-2](#dh-adr-llm-2)) |
| `llm-pi-ai.19` | Library retries must be switched off | `:275` | No library is in the request path ([DH-ADR-PIAI-1](../libs/earendil-works-pi.md#dh-adr-piai-1)); one-shot callers get buffered retry ([DH-ADR-LLM-4](#dh-adr-llm-4)) |
| `llm-pi-ai.20` | A patch removes quadratic re-parsing of tool arguments | `:276` | Fragments are forwarded as text (`DH-LIB-PIAI-R3`, [DH-D-LLM-2](#dh-d-llm-2)) |
| `llm-pi-ai.21` | Signed reasoning is dropped on any prefix change; the digest is not over wire bytes | `:277` | The digest is over the bytes sent; a route may set `signedReasoning: keep` where the endpoint does not check ([DH-D-LLM-4](#dh-d-llm-4)) |
| `llm-pi-ai.22` | A tool round that spans a prefix change can be rejected | `:278` | `ResolvedModel.PrefixBoundReasoning` tells the driver loop to hold a prefix change until the round ends ([DH-D-LLM-4](#dh-d-llm-4); depends on area A02) |
| `llm-pi-ai.23` | A patch carries Gemini tool-call signatures | `:279` | Unknown response members are kept as raw JSON and replayed (`DH-LIB-PIAI-R5`, [DH-D-LLM-4](#dh-d-llm-4)) |
| `llm-retry.1` | Direct callers are single-attempt | `:133` | `Runtime.Complete` retries a buffered one-shot call; no chunk of a failed attempt is exposed ([DH-ADR-LLM-4](#dh-adr-llm-4)) |
| `llm-retry.2` | Always mode retries permanent failures | `:134` | A permanent-class failure is never retried in any mode ([DH-ADR-LLM-4](#dh-adr-llm-4)) |
| `llm-retry.3` | Budgets of several policies add up | `:135` | One cap on attempts per step, counted from the log, binds every recovery listener ([DH-ADR-LLM-4](#dh-adr-llm-4)) |
| `llm-retry.4` | Recovery depends on listener order; always mode waits for downstream first | `:136` | The decision is by failure class, so the retry component never waits on another listener before acting ([DH-ADR-LLM-4](#dh-adr-llm-4)). A listener that never returns is area A02's dispatch deadline |
| `llm-retry.5` | `llm/retry` records scheduling, not the outcome | `:137` | `llm/retry-abandoned` records why recovery stopped ([DH-D-LLM-3](#dh-d-llm-3)) |
| `llm.1` | No retry, cache or rate limit in the service | `:181` | Retry is a separate component by decision; a per-route concurrency cap is added; a response cache is deliberately absent because it would answer without an attempt ([DH-ADR-LLM-4](#dh-adr-llm-4)) |
| `llm.2` | Only model adapters send the profile `User-Agent` suffix | `:182` | One identity value is the default of every `egress` client ([DH-ADR-LLM-7](#dh-adr-llm-7); depends on area A23) |
| `llm.3` | Sampling is temperature, output cap and stop only | `:183` | `Controls` adds `TopP` and `ToolChoice`; other gateway parameters go in a route's `extraBody` ([DH-ADR-LLM-1](#dh-adr-llm-1)) |
| `llm.4` | Variants without a producer were pruned | `:184` | The rule is kept. Cache breakpoints are placed by the Anthropic driver itself and need no vocabulary ([DH-ADR-LLM-1](#dh-adr-llm-1)) |
| `llm.5` | The assembler throws on a plugin-added block type | `:185` | The block set is closed; an unknown kind cannot be constructed ([DH-ADR-LLM-1](#dh-adr-llm-1)) |
| `llm.6` | `sessionId` is a locally declared brand to avoid an import cycle | `:186` | `Meta.Session` is an opaque label in the leaf package `llm/vocab`; `session` imports that leaf and `llm` imports `session`, so there is no cycle and no duplicate declaration ([DH-D-LLM-11](#dh-d-llm-11)) |
| `llm.7` | Tool updates fall back when session history is missing | `:187` | A loop request is always built from the log ([DH-ADR-ARCH-10](../upstream/architecture.md#dh-adr-arch-10)), so the projection always has its history ([DH-D-LLM-1](#dh-d-llm-1)) |
| `plugin-package-inventory-deepseek.1` | Programmatic and in-memory plugins have no npm identity | `:65` | Not applicable: there are no npm packages. The inventory is the binary's build version, enabled entry ids and plugin manifests |
| `plugin-package-inventory-deepseek.2` | The inventory can be partial | `:66` | A compiled-in component always has the binary's identity and a plugin cannot load without a manifest ([DH-D-LLM-5](#dh-d-llm-5)) |
| `plugin-package-inventory-deepseek.3` | In-place package replacement needs a restart | `:67` | Not applicable: code is never replaced in a running Go process ([DH-ADR-LOADER-8](../libs/cordis-plugins.md#dh-adr-loader-8)) |
| `token-meter.1` | The heuristic is approximate | `:143` | **Carried.** An exact count needs a provider tokenizer or a network call per measurement. The error is reduced by a per-session calibration factor ([DH-ADR-LLM-9](#dh-adr-llm-9)) |
| `token-meter.2` | A cold cache can scan the whole log | `:144` | Meter state is checkpointed in the transaction of its events ([DH-ADR-PERSISTENCE-7](../upstream/session-and-persistence.md#dh-adr-persistence-7)); a cold read prices the current surface only ([DH-D-LLM-6](#dh-d-llm-6)) |
| `token-meter.3` | Every measurement clones the surface | `:145` | Values are immutable by ownership, so a read shares them; pressure is a stored total ([DH-ADR-ARCH-11](../upstream/architecture.md#dh-adr-arch-11), [DH-D-LLM-6](#dh-d-llm-6)) |
| `token-meter.4` | Provider usage is reused only for an identical envelope | `:146` | Otherwise the estimate is scaled by the calibration factor instead of falling back to the raw heuristic ([DH-ADR-LLM-9](#dh-adr-llm-9)) |
| `token-meter.5` | A system-prompt rewrite is priced at zero until the next usage sample | `:147` | The fold keeps each system node's price and reprices a replacement in the same step ([DH-D-LLM-6](#dh-d-llm-6)) |
| `token-meter.6` | Checkpoints keep the whole surface | `:148` | State is totals and system-node prices; the surface has one owner, the session ([DH-ADR-LLM-9](#dh-adr-llm-9)) |
| `UP-DEBT-marker.packages/llm/llm-pi-ai/src/stream.ts:41` | The library flattens an error to its message | `packages/llm/llm-pi-ai/src/stream.ts:41-48` | The transport keeps the original error as the cause ([DH-ADR-PIAI-1](../libs/earendil-works-pi.md#dh-adr-piai-1)) |
| `UP-DEBT-marker.packages/llm/llm/src/call-config.ts:22` | Open question: which fields are per-conversation, and where provider options belong | `packages/llm/llm/src/call-config.ts:22-23` | `Controls` is logged per request; provider-specific options are route config ([DH-ADR-LLM-1](#dh-adr-llm-1)) |
| `UP-DEBT-marker.packages/llm/plugin-package-inventory-deepseek/src/index.ts:101` | Cached manifest identities are never invalidated | `packages/llm/plugin-package-inventory-deepseek/src/index.ts:101` | Not applicable: no in-process package replacement exists in Go |

## 6. Go design

### 6.1 Shape

All packages are under `src/harness/internal/llm`. Neither `llm/vocab` nor `llm` imports a provider or does I/O, and `llm/vocab` imports no other harness package except `schema`; an import-direction test enforces both ([DH-ADR-ARCH-9](../upstream/architecture.md#dh-adr-arch-9)).

| Package | Component (cordis) | Provides / injects | Why it exists |
|---|---|---|---|
| `llm/vocab` | none (types, a leaf) | Message, block, source, tool schema, `RouteID`, `Controls`, `CallConfig`, `Meta`, chunk, `Failure` and its codes, the canonical JSON encoding | The vocabulary a model request is made of. A leaf so that the session package (A01), which constructs every request, can import it without a cycle |
| `llm` | none (the seam) | `Adapter`, `Call`, `Runtime`, `Prepared`, the assembler, the stream record, the event keys, and every `llm/vocab` type again as a Go alias (`llm.Message` is `vocab.Message`) | The adapter seam. It imports `session` for the one request type, `session.Request`; other areas import only `llm` |
| `llm/runtime` | `llm.runtime` (`Construct`) | provides `llm.RuntimeKey`; injects nothing | The registry seam ([DH-ADR-ARCH-6](../upstream/architecture.md#dh-adr-arch-6)): routes, prepared calls, the one normalisation point |
| `llm/transport` | none (library) | used by providers; takes an `egress` client | HTTP call, SSE reader, idle timer, failure facts, request contributions, client identity |
| `llm/wire/anthropic`, `openaichat`, `openairesp`, `gemini` | none (pure) | `Encode`, `Decode`, `ProjectTools` | One driver per protocol ([DH-ADR-PIAI-1](../libs/earendil-works-pi.md#dh-adr-piai-1)) |
| `llm/catalog` | none (library) | embedded data, overlay resolution | Model facts as data |
| `llm/provider/deepseek` | `llm.deepseek.api-key`, `llm.deepseek.account` | inject `llm`, `egress`, credentials (A17), attachments and tables (A07) | The vendor's two routes |
| `llm/provider/keyed` | `llm.routes` | inject `llm`, `egress`, credentials | Any key-authenticated route on any of the four protocols |
| `llm/provider/bedrock`, `vertex`, `azureentra`, `oauth` | one each, default off | inject `llm`, `egress`, credentials | Cloud sign-ins, later ([DH-ADR-25](../design.md#dh-adr-25)) |
| `llm/retry` | `llm.retry` | injects `agent.Agents`, projections | The retry policy |
| `llm/meter` | `llm.meter` | provides `meter.Key`; injects projections, optional `llm` | Token measurement |

The adapter seam and the runtime, as Go interfaces (illustrative, not final):

```go
package llm

type RouteID = vocab.RouteID // matched exactly; there is no default route and no alias
// There is no request type here. The seam receives session.Request (core-session.md, DH-ADR-SESSION-7), which only the
// session can construct, and reads it through accessors: Route, Model, Controls, Messages, Tools, ToolHistory, Meta.

type Adapter interface {
    Describe(r RouteID) RouteInfo
    Models(ctx context.Context, r RouteID) ([]ModelInfo, error)
    Prepare(ctx context.Context, r RouteID, model string) (Call, error)
}

type Call interface { // endpoint, model facts and retry policy captured once
    Model() ResolvedModel
    Retry() RetryPolicy
    Attempt(ctx context.Context, req session.Request) iter.Seq2[Chunk, error] // one provider attempt
}
type Chunk interface{ chunk() } // sealed: the seven types in section 6.2
type Failure struct {
    Code       Code          // "llm/rate-limit"; a closed table plus "llm/http-<status>"
    Class      Class         // Transient, Permanent, Repairable or Cancelled; fixed per code
    Message    string
    Status     int           // HTTP status, 0 when none
    RetryAfter time.Duration // the provider's request, validated; not a decision
    RequestID  string
}
type Runtime interface {
    Register(owner *cordis.Context, a Adapter, routes ...RouteID) (Registration, error) // any adapter, a test stub included
    Routes() []RouteInfo // id, protocol, kind (api-key, account, cloud), base address, credential reference name
    Models(ctx context.Context, r RouteID) ([]ModelInfo, error) // cached or embedded listing, with image support per model; never waits for the network
    Prepare(ctx context.Context, r RouteID, model string, c Controls) (Prepared, error)
    Complete(ctx context.Context, req session.Request, sink AttemptSink) (Message, error) // buffered auxiliary call
}
type Prepared interface { // what callers hold; the raw Attempt is not reachable from it
    Config() CallConfig // effective controls, and which were adapter defaults
    Model() ResolvedModel
    Retry() RetryPolicy
    Stream(ctx context.Context, req session.Request) iter.Seq[Chunk] // once; ends with one Finish
}
```

- **Request.** `session.Request` is the only request type, and area A01 owns how one is constructed: `NextRequest` for a loop step, `RequestFrom(seq)` for an auxiliary call (a title, a summary, a review) described by one committed event. There is no field for a one-shot system text: the event's builder puts every message into the request, and its `Meta` carries the session and the purpose. A caller with no session (the route check) uses an ephemeral in-memory session. An adapter is a pure function of the request and its captured call: no listener or adapter can change a request ([DH-ADR-ARCH-11](../upstream/architecture.md#dh-adr-arch-11)), so the digest the session stores covers what was encoded.
- **The one open member of the vocabulary.** A message's `Source{Kind, Producers, Data}` has an open string kind, a list of producers (so a merged message names every contributor) and a typed payload its producer owns; a reader that does not know a kind treats the message as plain content. Everything else is closed ([DH-ADR-LLM-1](#dh-adr-llm-1)).
- **Stream.** A Go iterator. When the consumer stops ranging, the adapter's function returns and its deferred cleanup closes the response body, so no path leaks a connection.
- **Cancellation.** `ctx` is the turn's context ([DH-ADR-ARCH-3](../upstream/architecture.md#dh-adr-arch-3)). Cancelling it aborts the HTTP request and the stream ends with `Finish{Aborted}` and the recorded cause. The idle timer is a second, internal cause that ends as `llm/timeout`; a caller cancel that came first stays a cancel.
- **Error classification.** An adapter returns a `*Failure`, or any error, which the runtime classifies through `egress.Classify` and otherwise as `llm/transport`. Class is a property of the code, so policy never reads message text.

| Class | Codes | Who may act |
|---|---|---|
| Cancelled | `llm/aborted` | Nobody; the turn ends with its cause |
| Transient | `llm/rate-limit`, `llm/server`, `llm/timeout`, `llm/transport`, `llm/stream-closed`, `llm/malformed-response`, `llm/empty-response` | The retry policy |
| Repairable | `llm/context-window-exceeded`, `llm/image-offload-required`, `llm/attachment-unreadable` (a stored image the request needs cannot be read; it names the reference) | A repair listener (compaction, area A14; quarantine, area A07), which then asks for one retry |
| Permanent | `llm/auth`, `llm/quota`, `llm/account-quota`, `llm/invalid-request`, `llm/content-refused`, `llm/missing-credential`, `llm/invalid-credential`, `llm/sign-in-required`, `llm/token-invalid`, `llm/no-route`, `llm/unknown-model`, `llm/model-unsupported`, `llm/client-upgrade-required`, `llm/client-contract` (a signed-in client broke its route's contract, [DH-ADR-LLM-10](#dh-adr-llm-10): a tool ran inside the client, the listing-only MCP server was asked to run one, or the client printed a line the adapter cannot place; added at plan 16.5.3), `llm/unsupported-option`, `llm/unsupported-content`, `llm/unsupported-reasoning-effort`, `llm/request-extension`, `llm/request-headers`, `llm/http-<4xx>` | Nobody; the turn ends and the user is told |

### 6.2 Contracts

**Chunks** (replace upstream `StreamChunk`): `BlockStart{Index, Kind}`, `TextDelta{Index, Text}`, `ReasoningDelta{Index, Text}`, `ToolCallDelta{Index, ID, Name, ArgsDelta}`, `BlockEnd{Index, Block}`, `UsageChunk{Usage}`, `Finish{Reason, Failure, Replay}`. Finish reasons are a closed enum: `Stop`, `ToolCalls`, `MaxTokens`, `Aborted`, `Error`. Rules, all kept from upstream:

- Blocks are numbered in first-seen order; usage comes before `Finish`; nothing follows `Finish`.
- `ArgsDelta` is raw text exactly as received and is checked as JSON once, at `BlockEnd`.
- A response that ends without the protocol's terminal event is `llm/stream-closed`; a `stop` with no block is `llm/empty-response`; an output-cap finish drops every tool call and its replay entry.
- Usage is disjoint: uncached input, cache read, cache write, output (reasoning is inside output).

**Text is never damaged in transit.** The SSE reader finds frames on bytes and a driver decodes only whole frames, so a multi-byte character split across two network reads cannot be cut ([DH-ADR-NODEPLATFORM-1](../libs/node-platform.md#dh-adr-nodeplatform-1)). Inference: one case remains inside JSON. A provider may end one delta with the escape for the first half of a surrogate pair and start the next with the second half, and Go's JSON decoder would turn each half into a replacement character. The driver holds back a trailing unpaired half and joins it with the next delta of the same block.

**Events.** Dispatch forms are those of [design.md §4.6](../design.md#46-event-model).

| Go key | Form | Replaces | Payload and rule |
|---|---|---|---|
| `llm.RoutesChanged` | notify | `UP-EVT-llm/adapters-updated` | No payload; listeners re-read `Routes()`. A failing observer cannot veto a registry change |
| `llm.ModelsChanged` | notify | `UP-EVT-llm/models-updated` | `{Route}`; the model picker fragment reloads (area C3, [DH-D-UICHAT-7](ui-chat.md#dh-d-uichat-7)) |
| `llm.StreamAround` | around ([DH-D-CORDIS-7](../libs/cordis.md#dh-d-cordis-7)) | `UP-EVT-llm/stream` | Request `{Config, session.Request}`; decision `Proceed` or `Serve(stream, servedBy)`. A listener may substitute the stream (replay, area A23), which may end in a recorded failure like any stream, and the attempt record names who served it. It cannot change the route or the request |
| `llm.PrepareHeaders` | ordered | `UP-EVT-llm-pi-ai/request-headers` | `{Route, Model, BaseURL, Meta}` and a header set, for every route. Compiled-in listeners only; credential headers and the client identity are applied afterwards and cannot be replaced; an error fails the call with `llm/request-headers` before any I/O |

**Durable session events written by this area** (registered with payload structs, log-only): `llm/retry` (scheduled: attempt number, delay, failure, policy), `llm/retry-started`, and new `llm/retry-abandoned` (reason: not retryable, exhausted, elapsed, step cap, cancelled). The attempt events `assistant/message` and `assistant/attempt` are declared by area A01 and appended by the driver (A02); this area supplies their payload, the **attempt record**: the compact timed stream, usage, the failure if any, the number of HTTP requests, and `servedBy`.

**Config** (Go structs; defaults are Go values; decoding is strict, [DH-ADR-SCHEMA-3](../libs/zod.md#dh-adr-schema-3)).

| Go struct | Replaces | Main fields and defaults |
|---|---|---|
| `deepseek.APIKeyConfig` | `UP-CFG-dsh-llm-deepseek-api-key` | `BaseURL`, `APIKey` (credential reference, default `DEEPSEEK_API_KEY`), `Reasoning`, `MaxTokens`, `IdleTimeout` 5 min, `Models` (overlay), `Listing` (`follow` on the official origin, else `off`; `Refresh: on-access`), image and Files limits, `Retry`, `Send` |
| `deepseek.AccountConfig` | `UP-CFG-dsh-llm-deepseek-account` | As above without `APIKey`; the credential is the account sign-in only |
| `keyed.Config` | `UP-CFG-dsh-llm-pi-ai` | `Routes map[RouteID]Route`: `Protocol`, `BaseURL`, `Credential`, `Headers` (text or reference), `Models` (overlay), `Listing` (`offer`), `Switches`, `ExtraBody`, `MaxConcurrent`, `Retry`, `Send`. Empty by default: no route until one is configured |
| `retry.Config` | `UP-CFG-dsh-llm-retry` | `StepAttemptCap` (default 12). The policy itself stays on each route: `Mode` `bounded` (5 retries, 500 ms to 10 s, jitter 0.1) or `persistent` (`MaxElapsed`, default 30 min) |
| `transport.Send` (per route) | `UP-CFG-dsh-plugin-package-inventory-deepseek` | `Identity` (`product`, `version`, `full`), `Installation`, `Session`, `Contributions []string`. Everything defaults to the least: section 7 |
| `meter.Config` | `UP-CFG-dsh-token-meter` | `Calibrate` (default true) |

**Services.**

| Go service key | Replaces | Interface |
|---|---|---|
| `llm.RuntimeKey` (`"llm"`) | `UP-SVC-llm` | `Runtime` above |
| `transport.ContributionsKey` (`"llm.contributions"`) | `UP-SVC-deepseekLlmApiExtensions` | `Register(name, Contributor) (release, error)`; a contributor has `Prepare(ctx, body, meta) (json.RawMessage, settle func(accepted bool) error, error)` |
| `meter.Key` (`"tokenMeter"`) | `UP-SVC-tokenMeter` | `Measure(session, header) Measurement`, `Pressure(session) Pressure`, `EstimateMessage(Message) int` |

**Tool schemas sent to the model.** The tool registry holds one canonical document per tool, inside the model-facing subset that `schema` enforces at registration (`DH-LIB-ZOD-R3`, [DH-ADR-SCHEMA-2](../libs/zod.md#dh-adr-schema-2)). The canonical subset is upstream's (`packages/core/tools/src/json-schema.ts:1-11`): an object root, one scalar `type`, `properties`, `required`, boolean `additionalProperties`, `items`, scalar `enum` and `const`, `oneOf`, the annotations `description`, `title`, `default` and `examples`, plus the constraint keywords the `dsh` tag derives (`minimum`, `maximum`, `pattern`). No `$ref`, `allOf`, `not`, conditional or `format` keyword is admitted. Each driver sends a projection of that document. Arguments are always validated against the canonical document, so a projection never weakens a rule. Inference: the per-protocol columns below come from vendor documentation, not from a run; [DH-E-LLM-4](#dh-e-llm-4) is what confirms them.

| Keyword | Anthropic Messages | OpenAI Chat Completions | OpenAI Responses | Gemini API |
|---|---|---|---|---|
| `type`, `properties`, `required`, `items`, `enum`, `description` | sent | sent | sent | sent |
| `additionalProperties: false` | sent | sent | sent | removed |
| `const` | sent | sent | sent | sent as a one-value `enum` |
| `oneOf` | sent | sent | sent | sent as `anyOf` |
| `minimum`, `maximum` | sent | sent | sent | sent |
| `pattern`, `title`, `default`, `examples` | sent | sent | sent | removed; `pattern` and `default` are restated in `description` |
| vendor "strict" mode | n/a | not requested | set to `false` | n/a |

**Persisted data.** Machine-state tables (area A07): the last good listing per endpoint and credential fingerprint (a keyed hash, never the key), and the vendor Files upload index. Session projections (area A06's registry): `tokenUsage`, `contextPressure`, `contextBreakdown`, `llmRetry`. Embedded in the binary: the catalogue file. **Tools exposed to the model:** none.

### 6.3 Ownership, failure, retry, rollback and cleanup

| Resource | Owner (releases it) | Access (may use it) |
|---|---|---|
| A route registration | The fiber of the provider component; unloading removes its routes as a set and notifies `RoutesChanged` | Any caller of `Prepare` |
| An `egress` client and its idle connections | The provider component's fiber, through the release function `egress` returns | That component's calls |
| One response body and its idle timer | The attempt; closed on finish, failure, cancel and when the consumer stops reading | The driver's decoder |
| A credential value | The credential service (A17); the transport holds it only while building one request | The route that names its reference |
| A retry wait | The retry component's fiber and the turn's context, whichever ends first | n/a |
| A listing refresh | The provider fiber; unload cancels it and waits for it | n/a |
| An uploaded image file at the vendor | Shared: the vendor stores it. The harness deletes only ids in its own upload index | The route and credential that uploaded it |

- **A provider component fails to load or is disabled.** Its routes are gone and a request for one fails with `llm/no-route`. A call already prepared keeps its captured endpoint, model facts and policy and finishes.
- **An attempt fails before the first byte or mid-stream.** Same `Failure` value. Chunks already delivered stay in the attempt record; no message and no tool call is assembled from them. Nothing is rolled back at the provider. **Uncertain:** whether a cancelled or cut request was billed.
- **The adapter panics, returns without a finish or yields after one.** The runtime recovers, emits one `Finish{Error}`, drops anything later and logs a defect.
- **The consumer fails while ranging.** That is the consumer's error. It never becomes an `llm/` failure and never reaches the retry policy.
- **The host dies during a retry wait.** The log holds `llm/retry` without `llm/retry-started`; on resume the lease holder closes the turn as interrupted ([design.md §5](../design.md#5-failure-retry-rollback-and-cleanup)). The retry is not resumed.
- **An image upload fails.** The whole request is rebuilt with inline images inside the inline budget; one request never mixes both forms. A stale file id permits one replacement request inside the same attempt, on the same route and credential, and the attempt record counts two HTTP requests.
- **A request contribution fails.** A preparation failure omits that field and logs once; a name collision with a base field fails the call (`llm/request-extension`); `settle(true)` runs only after HTTP 2xx and its failure never fails the model request. When a provider rejects a request that carried contributions as invalid (`llm/invalid-request`), the transport sends one replacement request without any contribution inside the same attempt, on the same route and credential, calls `settle(false)` on each contributor so that it can stop contributing, and the attempt record counts two HTTP requests. **Uncertain:** after a crash between the 2xx and the stored watermark, a range is sent again.
- **The listing cannot be loaded.** The last good listing is served, then the embedded catalogue. This is not a fallback across a boundary: no request goes anywhere else.

## 7. Decisions

<a id="dh-adr-llm-1"></a>
### DH-ADR-LLM-1 One closed vocabulary, and an adapter is a prepared one-attempt call
- **Decision.** Messages, blocks, chunks, finish reasons and failure codes are closed Go sets in the leaf package `llm/vocab`; the seam (`Adapter`, `Call`, `Runtime`, `Prepared`) is package `llm` and receives `session.Request`. A message is built only by constructors that mint its id and copy its content. The adapter seam is `Adapter.Prepare` returning a `Call` whose `Attempt` is one provider attempt. `Controls` holds reasoning effort, temperature, `TopP`, output cap, stop sequences and `ToolChoice`; a driver encodes each or fails with `llm/unsupported-option` before I/O. No request field exists without a producer.
- **Why.** Upstream's sets are open to any plugin, which is why its assembler can throw on an unknown block. In Go a plugin is a process and cannot add a type, so a closed set costs nothing and lets a missed case be a test failure.
- **Tradeoff.** A new modality is a change to this package and to every driver, the UI and compaction, as upstream already requires. Audio and video never reach a model: the vocabulary has no block for them, by this decision and not by omission; such data from an MCP server or a file is stored as a generic file and named to the model as text (recommended default, not confirmed by the owner). Provider-specific reasons and fields live in replay state or route config, not in the vocabulary.
- **Serves.** [DH-O-8](../design.md#dh-o-8), [DH-O-4](../design.md#dh-o-4). **Verified by.** DH-T-LLM-1, DH-T-LLM-3, DH-T-LLM-6.

<a id="dh-adr-llm-2"></a>
### DH-ADR-LLM-2 Every attempt ends in exactly one terminal fact, made by the runtime
- **Decision.** `Prepared.Stream` always ends with exactly one `Finish`. The runtime turns a returned error, a panic, a missing finish and a cancellation into that one chunk and produces one attempt record, which the caller logs as one event. A provider's rejection is surfaced as it is; no placeholder or repair is invented inside an adapter.
- **Why.** DH-O-8 says one attempt settles as exactly one logged event. Upstream reached the same boundary after first tagging thrown errors in a side table.
- **Tradeoff.** The original Go error is kept only as a logged cause, not passed to policy. A failure inside an adapter and a failure inside a consumer are told apart by where they occur, so the consumer must not hide its own errors in a chunk.
- **Serves.** [DH-O-8](../design.md#dh-o-8), [DH-O-3](../design.md#dh-o-3). **Verified by.** DH-T-LLM-2, [DH-E-LLM-1](#dh-e-llm-1), [DH-E-LLM-3](#dh-e-llm-3).

<a id="dh-adr-llm-3"></a>
### DH-ADR-LLM-3 No failure falls back across a trust or billing boundary
- **Decision.** (a) A route is matched by exact id; there is no default route and `StreamAround` cannot reroute. (b) A route has exactly one credential source and never tries another. (c) A retry repeats the same route, model and credential reference. (d) Redirects are refused, so a credential stays on its configured origin. (e) Replay state is sent only to the route that produced it. (f) Images are sent only as bytes the harness holds; a provider is never given a URL to fetch, and assistant image output is not in the vocabulary. (g) The request header records the credential reference name, and sign-out cancels every running agent whose latest header names it.
- **Why.** Each of these is a place where "it kept working" would mean a different account was billed or a different party saw the conversation.
- **Tradeoff.** Less convenience: a missing key fails instead of using another route; switching routes mid-session loses signed reasoning; an image the user only has as a link must be attached first.
- **Serves.** [DH-O-8](../design.md#dh-o-8), [DH-O-4](../design.md#dh-o-4). **Verified by.** DH-T-LLM-4, DH-T-LLM-14, [DH-E-LLM-3](#dh-e-llm-3), [DH-E-LLM-6](#dh-e-llm-6).

<a id="dh-adr-llm-4"></a>
### DH-ADR-LLM-4 Retry is a bounded policy, decided by failure class, outside the adapter
- **Decision.** No adapter or transport retries a model request. The `llm.retry` component listens on the driver's failed-request point and retries only Transient-class failures: `bounded` mode up to a count, `persistent` mode up to a total elapsed time. A Permanent failure is never retried. One cap on attempts per step, counted from the log, binds every recovery listener. A retry is logged before its wait and its end is logged with a reason. `Runtime.Complete` gives one-shot callers the same policy on a buffered call and reports each attempt once to the caller's sink. A per-route concurrency cap is the only rate limit; there is no response cache.
- **Why.** Upstream's always mode retries authentication and quota failures for ever, its budgets add up across listeners, and its outcome depends on listener order.
- **Tradeoff.** No unlimited mode: an unattended run on a flaky provider ends after `MaxElapsed`. A buffered one-shot call shows nothing until it succeeds.
- **Serves.** [DH-O-8](../design.md#dh-o-8). **Verified by.** DH-T-LLM-7, DH-T-LLM-8, [DH-E-LLM-3](#dh-e-llm-3).

<a id="dh-adr-llm-5"></a>
### DH-ADR-LLM-5 One adapter per vendor; the twin rule becomes a conformance rule
- **Decision.** The vendor has one adapter, built on the Anthropic Messages driver. Upstream's second adapter for the same vendor is not built. Its purpose is kept as three rules. (1) Every driver passes one conformance table of logical cases. (2) A chunk type, finish reason or failure field may exist only if drivers of at least two different protocol families produce it from recorded real bytes; anything only one protocol can express belongs in replay state. (3) Each driver's fixtures include a stream recorded from a real provider during a scenario run.
- **Why.** The second twin was the npm library that [DH-ADR-PIAI-1](../libs/earendil-works-pi.md#dh-adr-piai-1) removes. Inference: four protocols differ in the ways that matter (indexed blocks, per-choice deltas, whole function-call objects), so they check neutrality at least as well as two adapters for one endpoint; upstream itself names a conformance suite as what could retire a twin (`.agents/notes/implemented/architecture/2026-06-13-twin-llm-adapters.md:27`).
- **Tradeoff.** The drivers share one transport, so a transport defect is not caught by disagreement. The vendor's own Chat Completions endpoint stays reachable as a keyed route, which [DH-E-LLM-2](#dh-e-llm-2) uses to compare two protocols against one vendor.
- **Serves.** [DH-O-8](../design.md#dh-o-8). **Verified by.** DH-T-LLM-6, [DH-E-LLM-2](#dh-e-llm-2).

<a id="dh-adr-llm-6"></a>
### DH-ADR-LLM-6 A route's models are a keyed overlay; one protocol per route; the catalogue is refreshed from vendor listings
- **Decision.** A model's facts resolve in layers, later winning field by field: the embedded catalogue, the route's listing when the route follows one, the route's `Models` overlay keyed by model id (with `hidden` and explicit `none` values). There is no replacing list. Each vendor entry of the embedded catalogue carries a display name and optional search aliases, which the provider picker shows and filters on, with the route identifier as secondary text (register entry U-K2-11); a catalogue test fails when an entry has none. Every field of a keyed route is live. A route speaks exactly one protocol. A listed model the build cannot drive is returned as unavailable with its reason. The last good listing is stored. The embedded file is refreshed by a maintainer tool that reads each vendor's own listing endpoint and a hand-kept file in the repository for facts no listing discloses; a third-party catalogue is at most a cross-check. The running product never contacts a catalogue service.
- **Why.** Upstream's "is the catalogue customised" comparison is the cause of four limitations, and a mixed-protocol route of one more.
- **Tradeoff.** A maintainer needs a key per vendor to refresh. Facts kept by hand can be wrong until a scenario or a user corrects them. Inference: the vendor gate compares against upstream's version numbers under the key `deepseek-harness`, which the Go build does not share; the build therefore has a configurable compatibility version for that comparison, and it is unset in a shipped build until the vendor lists this client (recommended default, not confirmed by the owner). With it unset, a listed model that carries a minimum version is shown as unavailable with that reason, and the embedded catalogue still serves a key. The build never claims upstream's product name or version.
- **Serves.** [DH-O-8](../design.md#dh-o-8), [DH-O-2](../design.md#dh-o-2). **Verified by.** DH-T-LLM-9, DH-T-LLM-12, [DH-E-LLM-5](#dh-e-llm-5).

<a id="dh-adr-llm-7"></a>
### DH-ADR-LLM-7 Everything beyond the model request is opt-in, per route
- **Decision.** By default a provider request carries the model request, the credential and `User-Agent: dsh`. Each of these is a separate setting on the route, off by default: the stable installation id, the session id and purpose marker, and each named request contribution (the session-log suffix of area A05, the installation inventory). The listing is refreshed when it is used, not by an idle timer. The one off-switch of area A18 forces all of them off.
- **Client name and version: decided by the owner** ([DH-ADR-32](../design.md#dh-adr-32)). The client version counts as data leaving the machine and is a setting that is off until turned on. The default is `Identity: product`: the vendor sees the bare word `dsh`. `version` (`dsh/<version>`) and `full` (with the profile name) are settings the user turns on per route, and the one off-switch forces `product`. The vendor's version gate is evaluated locally and works at every level.
- **Why.** [DH-ADR-9](../design.md#dh-adr-9). Upstream sends the session log, the package list and three identifying headers by default, also to a gateway behind `baseURL`.
- **Tradeoff.** The vendor receives no diagnostics from a user who changes nothing, and a vendor that serves different listings by client version sees a generic client.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-8](../design.md#dh-o-8). **Verified by.** DH-T-LLM-11, DH-T-LLM-16, [DH-E-LLM-5](#dh-e-llm-5).

<a id="dh-adr-llm-8"></a>
### DH-ADR-LLM-8 Tool schemas are projected per protocol from one canonical subset
- **Decision.** The canonical subset and the projection table of section 6.2. A keyword outside the canonical subset fails tool registration. A driver's projection is a pure function of the document; the request header logs the canonical document.
- **Why.** [libs/zod.md](../libs/zod.md) left the provider-safe subset to this area. Sending the intersection only would lose rules three protocols accept; sending everything would be rejected by the fourth.
- **Tradeoff.** The model on one protocol may see a rule as prose where another sees a keyword. The table is unverified until [DH-E-LLM-4](#dh-e-llm-4) runs.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-8](../design.md#dh-o-8). **Verified by.** DH-T-LLM-10, [DH-E-LLM-4](#dh-e-llm-4).

<a id="dh-adr-llm-9"></a>
### DH-ADR-LLM-9 The token meter keeps bounded state and decides nothing
- **Decision.** Kept from upstream: measurement is a deterministic fold of the log with no model call; billed usage counts every attempt; occupancy is a reference figure, not an atomic observation. Changed: projection state is totals plus one price per retained system prompt, never one entry per message; the surface is read from the session, its one owner; an estimate is scaled by a calibration factor, the ratio of the last provider-reported prompt size to the heuristic's estimate of that same request in the same session.
- **Why.** Upstream measured megabytes of per-message projection state before reducing it (`.agents/notes/implemented/architecture/2026-10-13-context-breakdown-system-prompt-state.md:9`).
- **Tradeoff.** The number stays approximate; a consumer that needs exact tokens must ask the provider.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-5](../design.md#dh-o-5). **Verified by.** DH-T-LLM-15.

<a id="dh-adr-llm-10"></a>
### DH-ADR-LLM-10 A signed-in vendor client is a route: the guarantee holds at the client invocation, and the client never runs a tool
- **Context.** Owner rule OWNER-2 (2026-11-22) removes API keys from M1: real-model checks run only on Claude and GitHub Copilot through the user's own signed-in session. This record states owner decisions M1-D-PIPE-3 A and M1-D-PIPE-4 A ([decisions.md](../../stories/harness-DSH-M1/decisions.md) sec. 3; [gap-analysis.md](../../stories/harness-DSH-M1/gap-analysis.md) sec. 7.1) in this area's terms.
- **Decision.** (a) *Reach (M1-D-PIPE-3 A).* The harness reaches Claude and Copilot by starting the user's installed official command-line client (`claude`, the GitHub Copilot CLI) as a child process through `world/subprocess`, one process per attempt. The client signs in; the harness never reads, copies, stores or forwards a token, and removes every credential and provider-override variable from the child's environment, so the sign-in is what is used. (b) *Guarantee (M1-D-PIPE-4 A).* On a client route, SP-1 ("the bytes sent equal the request rebuilt from the log") holds at the **client-invocation boundary**: the logged and rebuildable unit is the argument list, the standard input and the files the adapter wrote for the attempt, the environment names without values and the client version, all as digests in the attempt record. What the client then sends to its vendor is the client's: its system text, its context handling, its loop and its retries are declared **opaque** in the route's capabilities. Keyed routes keep full SP-1. (c) *Tools.* Tool calls stay in the harness's guarded pipeline. A client route never runs a tool: built-in tools are off, harness tools are declared to the client by a harness-hosted **listing-only** MCP server, every call to them is denied by the client's own rules, and the adapter ends the attempt at the client's first turn. The call comes back **unexecuted** as `ToolCallDelta` and `Finish{ToolCalls}`, and the driver runs it as any model tool call (origin `model`, the call being a block of an assistant message). The conversation so far is rendered into the client's standard input by a pure function of `session.Request`, because neither client can be given earlier assistant turns.
- **Why.** (a) The alternatives either extract the stored token and call the vendor's API (option B of M1-D-PIPE-3, a possible terms breach) or need a Console account and work for Claude only (option C). (b) An official client adds its own system text and loop, so a byte-level guarantee past the client cannot be kept; stating the boundary keeps the guarantee true instead of quietly false. (c) Neither client's print or prompt mode can be told "return tool calls", and both expose harness tools only through MCP (Claude Code: `--tools ""` "doesn't affect MCP tools", tools arrive by `--mcp-config`; Copilot CLI: `--additional-mcp-config`). Denial plus a stop after one turn is the documented way to see the call without running it ([../libs/claude-code-cli.md](../libs/claude-code-cli.md) DH-ADR-CLAUDECODECLI-1, [../libs/github-copilot-cli.md](../libs/github-copilot-cli.md) DH-ADR-COPILOTCLI-1). It keeps the driver, the guarded pipeline, approval and the M1 oracle (one `tool/result`, then a final `assistant/message`) unchanged.
- **Alternative considered: an MCP bridge.** The client would call a harness MCP server that admits the call into the guarded pipeline with a new origin `client` and returns the result to the client, whose loop then continues. Its size: (1) a stdio MCP server that forwards `tools/call`; (2) an authenticated channel from that server back to the host (an inherited pipe needs `Spec.Extra`, plan 19.3.2, not built; a loopback listener needs a per-attempt secret and `PeerTree`, area A18); (3) a host endpoint that admits a call with origin `client` (a tools vocabulary change and an approval attribution); (4) driver changes, because one attempt would then span tool executions and `tool/result` events would be appended inside it, and the final message must not re-run calls the client already executed; (5) the client's follow-up model requests are invisible, so SP-1 would cover even less. Inference: on the order of 1,500 to 2,500 lines with tests, across `llm`, `tools`, `agent/driver`, session events (a format change) and `world/subprocess`. It is not built for M1 and is recorded here for a later stage that wants the client's own multi-step behaviour.
- **Tradeoff.** The model sees harness tools under the client's MCP naming and the history as rendered text; the Copilot CLI cannot replace its system prompt, so the harness system prompt is the leading section of the user message there; a Copilot run may send one more request between the denial and the stop. Each is listed as opaque. Two Claude Code flags the route needs (`--max-turns`, `--system-prompt-file`) are documented but hidden from the 2.1.312 help, and the Copilot JSONL schema is undocumented, so the first operator run (plan 16.5.9) confirms them and records fixtures.
- **Serves.** [DH-O-8](../design.md#dh-o-8), [DH-O-4](../design.md#dh-o-4) (on this route, at the stated boundary). **Verified by.** The tests of [DH-D-LLM-12](#dh-d-llm-12); `DH-E-CLAUDECODECLI-1`, `DH-E-COPILOTCLI-1`.

## 8. Deliverables

<a id="dh-d-llm-1"></a>
### DH-D-LLM-1 One vocabulary and one runtime for every model call
- **Result and why it matters.** Any component can prepare and stream a model call and always gets one terminal fact. This is the spine service "LLM runtime".
- **How it is implemented.** The `llm` package declares the seam interfaces, the assembler (with the output-cap and interruption rules), the compact stream record with its validating expansion and first-token reader, tool-update projection as a pure function of the folded tool history, and the event keys. `llm/runtime` holds the route registry (atomic replace of a registration's route set), `Prepare` (materialise defaults, validate effort, capture the call), the normalising `Stream`, and the route concurrency cap.
- **Components and files.** `internal/llm`, `internal/llm/runtime`. **Depends on.** [DH-D-LLM-11](#dh-d-llm-11), [DH-D-SESSION-4](core-session.md#dh-d-session-4) (the request type), [DH-D-CORDIS-2](../libs/cordis.md#dh-d-cordis-2), [DH-D-CORDIS-4](../libs/cordis.md#dh-d-cordis-4), [DH-D-CORDIS-7](../libs/cordis.md#dh-d-cordis-7) (the around key of `llm.StreamAround`). **Used by.** The driver, which appends the attempt record and owns the failed-request point ([DH-D-AGENT-3](agent-loop.md#dh-d-agent-3), [DH-D-AGENT-4](agent-loop.md#dh-d-agent-4)).
- **Priority.** P0. **Outcome.** [DH-O-8](../design.md#dh-o-8), [DH-O-4](../design.md#dh-o-4), [DH-O-3](../design.md#dh-o-3). **Done when.** DH-T-LLM-1 to DH-T-LLM-4 pass and [DH-E-LLM-1](#dh-e-llm-1) has passed.

<a id="dh-d-llm-2"></a>
### DH-D-LLM-2 The first working provider: shared transport, the Messages driver and the vendor's API-key route
- **Result and why it matters.** With one key, the harness holds a conversation with the vendor's models, with reasoning and tool calls.
- **Status of `deepseek-official`.** Keyless, fake-tested; not proven against a real API (OWNER-2). Under decision M1-D-PIPE-5 A (`docs/stories/harness-DSH-M1/decisions.md`) the route is kept and is tested only against fakes with a fake key; M1 holds no DeepSeek key, so no request has reached the vendor. The Messages decoder is also exercised by the signed-in Claude route of [DH-D-LLM-12](#dh-d-llm-12), whose `stream_event` lines it reads; the HTTP transport is not, because a client route makes no request of its own. The M1 real-model scenarios ([DH-E-LLM-1](#dh-e-llm-1), [DH-E-LLM-3](#dh-e-llm-3)) run on those routes, not on this one.
- **How it is implemented.** `transport` takes its client from `egress` (redirects refused), wraps the body for the idle timer, reads SSE, builds `Failure` from status, headers and cause, applies `PrepareHeaders`, `Send` and request contributions in name order. `wire/anthropic` encodes and decodes Messages, including replay signatures, native or consolidated system updates, tool-change blocks, and cache breakpoints for endpoints that need them. `provider/deepseek` registers `deepseek-official` with text input and the embedded catalogue.
- **Components and files.** `internal/llm/transport`, `internal/llm/wire/anthropic`, `internal/llm/provider/deepseek`. **Depends on.** [DH-D-LLM-1](#dh-d-llm-1); [DH-D-UTIL-2](util-test-support.md#dh-d-util-2) for `egress`; [DH-D-HUMAN-7](interaction-credentials.md#dh-d-human-7) for resolving a credential reference per request, with its revocation signal. Runtime prerequisite: a vendor API key.
- **Priority.** P0. **Outcome.** [DH-O-8](../design.md#dh-o-8), [DH-O-14](../design.md#dh-o-14). **Done when.** DH-T-LLM-5, DH-T-LLM-11 and DH-T-LLM-16 pass and [DH-E-LLM-1](#dh-e-llm-1), [DH-E-LLM-3](#dh-e-llm-3) and [DH-E-LLM-5](#dh-e-llm-5) have passed.

<a id="dh-d-llm-3"></a>
### DH-D-LLM-3 Bounded retry, and buffered one-shot calls
- **Result and why it matters.** A rate limit or a dropped connection does not end a turn, and no failure is retried without a bound or a record.
- **How it is implemented.** The `llm.retry` component is a listener on the driver's failed-request point. It reads the failure class and the call's captured route policy (`AttemptFailure.RoutePolicy`, from `Prepared.Retry()`), checks the step cap from the `llmRetry` projection, appends `llm/retry` and returns `Retry{After}`; the driver waits on the session clock and the step context and then sends the observe-only notice `agent.RetryWaited` (`RetryWaitEnded{Cancelled}`), from which the component appends `llm/retry-started`, or `llm/retry-abandoned` "cancelled" when a cancel ended the wait (the step then ends with the cancel cause, not the provider's failure); otherwise it appends `llm/retry-abandoned` and passes the failure on. `Runtime.Complete` loops the same policy over a buffered stream.
- **Components and files.** `internal/llm/retry`, `Complete` in `internal/llm/runtime`. **Depends on.** [DH-D-LLM-1](#dh-d-llm-1); [DH-D-AGENT-4](agent-loop.md#dh-d-agent-4) for the `agent.AttemptFailed` around key, on which the policy returns `Retry{After}` and the driver does the waiting; [DH-D-VIEW-1](session-views.md#dh-d-view-1) for the projection registry.
- **Priority.** P0. **Outcome.** [DH-O-8](../design.md#dh-o-8), [DH-O-3](../design.md#dh-o-3). **Done when.** DH-T-LLM-7 and DH-T-LLM-8 pass and [DH-E-LLM-3](#dh-e-llm-3) has passed.

<a id="dh-d-llm-4"></a>
### DH-D-LLM-4 Any key-authenticated provider: three more drivers, keyed routes, the catalogue and the route check
- **Result and why it matters.** A user adds a provider, a gateway or a local server in configuration and it works without a restart, on any of four protocols.
- **How it is implemented.** `wire/openaichat`, `wire/openairesp` and `wire/gemini` follow the Messages driver's layout and its conformance table, keep unknown response members as raw JSON for replay, and carry named deviation switches (`DH-LIB-PIAI-R12`) and `extraBody`. `provider/keyed` turns its route map into one registration and replaces the route set atomically on a config update. `catalog` loads the embedded file and resolves the overlay. Discovery follows pagination to a bound. The route check sends one minimal real request per chosen model on demand. `ProjectTools` implements [DH-ADR-LLM-8](#dh-adr-llm-8). A refresh tool under `tools/` rewrites the embedded file for review.
- **Components and files.** `internal/llm/wire/...`, `internal/llm/provider/keyed`, `internal/llm/catalog`, `tools/catalog-refresh`. **Depends on.** [DH-D-LLM-2](#dh-d-llm-2); [DH-D-UTIL-1](util-test-support.md#dh-d-util-1) for `schema`; [DH-D-BOOT-3](boot.md#dh-d-boot-3) for layered config and its ownership index. **Used by.** The driver, which holds a prefix change during a tool round on a model that declares `PrefixBoundReasoning` ([DH-D-AGENT-3](agent-loop.md#dh-d-agent-3)).
- **Priority.** P1. **Outcome.** [DH-O-8](../design.md#dh-o-8), [DH-O-6](../design.md#dh-o-6), [DH-O-2](../design.md#dh-o-2). **Done when.** DH-T-LLM-6 (which runs the conformance table of [DH-T-PIAI-8](../libs/earendil-works-pi.md#8-verification) over the four drivers), DH-T-LLM-9 and DH-T-LLM-10 pass and [DH-E-LLM-2](#dh-e-llm-2) and [DH-E-LLM-4](#dh-e-llm-4) have passed.

<a id="dh-d-llm-5"></a>
### DH-D-LLM-5 The vendor provider at parity: listing and gate, images, the account route, opt-in diagnostics
- **Result and why it matters.** New vendor models appear without a release, images work, a signed-in account can be used instead of a key, and vendor diagnostics can be switched on knowingly.
- **How it is implemented.** The listing client decodes row by row (one bad row never rejects the listing), applies the version gate and badge validation, stores the last good listing, and notifies `ModelsChanged`. One image policy sizes each request image, enforces the budget on encoded bytes with byte and count quanta, prefers Files references with an upload index scoped by endpoint and credential fingerprint, falls back to inline for the whole request, and uses a time-based cool-down after upload timeouts. `deepseek-account` resolves only the account credential, maps 401 to `llm/token-invalid`, and ends in-flight requests when the credential is revoked. The installation inventory is a contributor that reports the build version, enabled entry ids and plugin manifests.
- **Components and files.** `internal/llm/provider/deepseek`. **Depends on.** [DH-D-LLM-2](#dh-d-llm-2); [DH-D-HUMAN-9](interaction-credentials.md#dh-d-human-9) for the account service and its sign-in in the system browser; [DH-D-STORAGE-1](storage.md#dh-d-storage-1) and [DH-D-STORAGE-3](storage.md#dh-d-storage-3) for machine-state tables and attachments; [DH-D-HOST-4](host-telemetry.md#dh-d-host-4) for the off-switch; [DH-D-EXT-1](extensions.md#dh-d-ext-1) for plugin manifests. **Used by.** The session-log contributor, [DH-D-STORE-9](session-store.md#dh-d-store-9), which depends on this area and not the reverse.
- **Priority.** P1. **Outcome.** [DH-O-8](../design.md#dh-o-8), [DH-O-14](../design.md#dh-o-14). **Done when.** DH-T-LLM-12, DH-T-LLM-13 and DH-T-LLM-14 pass and [DH-E-LLM-5](#dh-e-llm-5) and [DH-E-LLM-6](#dh-e-llm-6) have passed.

<a id="dh-d-llm-6"></a>
### DH-D-LLM-6 Token usage and context occupancy from the log
- **Result and why it matters.** Compaction, the occupancy ring and the usage figure agree, survive paging and compaction, and cost no model call.
- **How it is implemented.** Three registered folds and the `tokenMeter` service as [DH-ADR-LLM-9](#dh-adr-llm-9) describes; image prices come from the route's declared pricing, files are priced as their handle text.
- **Components and files.** `internal/llm/meter`. **Depends on.** [DH-D-LLM-1](#dh-d-llm-1); [DH-D-VIEW-1](session-views.md#dh-d-view-1) and [DH-D-VIEW-2](session-views.md#dh-d-view-2) for the projection registry and checkpoints; [DH-D-SESSION-3](core-session.md#dh-d-session-3) for derived history.
- **Priority.** P1. **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-5](../design.md#dh-o-5). **Done when.** DH-T-LLM-15 passes and [DH-E-LLM-1](#dh-e-llm-1), including its usage assertions, has passed.

<a id="dh-d-llm-11"></a>
### DH-D-LLM-11 The model vocabulary is a leaf package
- **Result and why it matters.** The session package can construct every model request, and every area can name a message, a block or a failure, without importing the runtime. Without it the rule "only the session constructs a request" would be an import cycle. It is built first: [DH-D-SESSION-1](core-session.md#dh-d-session-1) and [DH-D-LLM-1](#dh-d-llm-1) both depend on it.
- **How it is implemented.** `llm/vocab` holds the closed types of [DH-ADR-LLM-1](#dh-adr-llm-1) with their constructors, the open message source, and one canonical JSON encoding with a fixed field order, which is what the session's request digest is computed over. Package `llm` re-exports each type as an alias.
- **Components and files.** `internal/llm/vocab`. **Depends on.** [DH-D-UTIL-1](util-test-support.md#dh-d-util-1) for `schema`.
- **Priority.** P0. **Outcome.** [DH-O-8](../design.md#dh-o-8), [DH-O-4](../design.md#dh-o-4). **Done when.** DH-T-LLM-1 passes, its vocabulary clauses included.

> **DC-3 override note (M1 story, 2026-11-22).** "Package `llm` re-exports each type as an
> alias" is overridden by owner decision M1-D-FND-3 (a) in
> [the M1 decision log](../../stories/harness-DSH-M1/decisions.md) (gap M1-G-FND-19).
> Package `llm` declares no `llm` aliases (no `type X = vocab.X`): a consumer that names a
> message, block or failure imports `internal/llm/vocab` directly, as the code already
> does. One name per type keeps the vocabulary a leaf without a second spelling to keep in
> step. The deliverable text above is unchanged (DC-2).

<a id="dh-d-llm-12"></a>
### DH-D-LLM-12 Signed-in client routes
- **Result and why it matters.** With no API key, the harness streams one model request to Claude and one to GitHub Copilot through the user's own signed-in client, started as a child process ([DH-ADR-LLM-10](#dh-adr-llm-10)). Each attempt settles as exactly one logged event. A signed-out client settles as one `assistant/attempt` with `llm/sign-in-required` (or `llm/auth` for a sign-in the vendor rejected), Permanent, so there is no retry and no other route. A tool call comes back unexecuted and runs in the harness's guarded pipeline, so the M1 run (one tool call, one approval) works on this route. Without it neither the DH-S-9 manual check nor M1 can run under OWNER-2 (gaps M1-G-PIPE-12, M1-G-LOOP-12).
- **How it is implemented.** One shared package and two providers.
  - *Shared (`llm/provider/clientroute`).* The locator (configured absolute path, else `subprocess.LookupProgram` on the child's `PATH`; a `.cmd` result is started with `Spec.Batch = AllowChecked`), the version probe (once per prepared call, in the run's environment, bounded by a deadline and the strict line reader of [DH-ADR-CLAUDEAGENTSDK-1](../libs/anthropic-ai-claude-agent-sdk.md#dh-adr-claudeagentsdk-1); a version below the floor fails `Prepare` with `llm/client-upgrade-required` before any request text is written), the environment builder (`BuildEnv` with the secret store, plus each route's removal and set lists), the request renderer (a pure function of `session.Request` that writes the system prompt file, the tool schema file of [DH-ADR-LLM-8](#dh-adr-llm-8) and the transcript for standard input), the attempt directory (private, removed when the attempt ends), the runner (one `subprocess.Start` per attempt, standard input written then closed, `StopTree` with a short grace when the context ends), and the invocation record (argument list with the attempt directory as `{attempt}`, digests of the argument list, standard input and each attempt file, environment names, probed version, client session id, client-reported model, client-internal retry and denial counts).
  - *The listing-only MCP server.* The harness executable in a hidden mode, started by the client from the attempt's MCP configuration. It answers `initialize`, `ping` and `tools/list` from the tool schema file and refuses every `tools/call` with an error, recording that it was asked (a contract breach the adapter turns into a failed attempt). It holds no channel to the host and can run nothing. Inference: about 200 to 300 lines of own JSON-RPC code, or the MCP module of [../libs/modelcontextprotocol.md](../libs/modelcontextprotocol.md) once it is in the module graph.
  - *`llm/provider/claudecode`.* Route `claude-code`: `claude -p` with stream-json output and partial messages, `--system-prompt-file`, `--tools ""`, `--strict-mcp-config --mcp-config`, `--permission-mode dontAsk --permission-prompts none`, `--max-turns 1`, `--no-session-persistence`, `--session-id`, `--setting-sources local`, `--model`; `stream_event` lines decoded by the Messages decoder of [DH-D-LLM-2](#dh-d-llm-2); the failure table, environment and version floor (2.1.259) of [../libs/claude-code-cli.md](../libs/claude-code-cli.md).
  - *`llm/provider/copilot`.* Route `copilot-cli`: prompt mode with JSONL output, `--no-auto-update`, deny rules for every tool kind and the harness server, `--additional-mcp-config`, `--session-id`, `--sync-session=off`, `--model`; the adapter stops the tree at the first assistant message that requests a tool; floor 1.0.115; everything else in [../libs/github-copilot-cli.md](../libs/github-copilot-cli.md).
  - *Capabilities.* Each route's `RouteInfo` names its kind (`client`), the client and its opaque parts (system text additions, context handling, loop scaffolding, internal retries, tool-name form, saved sessions for Copilot), and the controls it supports (model and reasoning effort; any other control set on the request fails with `llm/unsupported-option` before I/O, by [DH-ADR-LLM-1](#dh-adr-llm-1)). Input is text only; an image becomes the placeholder text a text-only route sends.
- **Components and files.** `internal/llm/provider/clientroute`, `internal/llm/provider/claudecode`, `internal/llm/provider/copilot`, the hidden listing mode in `cmd/dsh` (and in test binaries, which re-execute themselves as the server), the attempt record's `client` fields (a persisted-format change with a version bump, Rule 6). **Depends on.** [DH-D-LLM-1](#dh-d-llm-1) (runtime and attempt record), [DH-D-LLM-2](#dh-d-llm-2) (the Messages decoder), [DH-D-LLM-3](#dh-d-llm-3) (so "no retry" is a decision of the policy), [DH-D-EXEC-1](execution.md#dh-d-exec-1) with the batch opt-in (plan 16.5.1) and `BuildEnv` with secret names (plan 14.6.4), [DH-D-EXEC-2](execution.md#dh-d-exec-2) (tree stop). Runtime prerequisites: the client installed at or above its floor, and signed in by the user. These are operator facts, not build dependencies: the deliverable builds and its local tests pass without either.
- **Priority.** P0 (OWNER-2 leaves no other real-model route for M1). The Claude route is required by the M1 definition of done; the Copilot route is built and recorded either way (decisions.md sec. 1.1 item 4). The MCP bridge of DH-ADR-LLM-10 is deferred and is not part of this deliverable. **Outcome.** [DH-O-8](../design.md#dh-o-8), [DH-O-4](../design.md#dh-o-4) at the client boundary. **Done when.** The adapter tests the lead assigns at plan 16.5.5 pass (the shared package and the Claude route: DH-T-LLM-18 to DH-T-LLM-23; the Copilot route: DH-T-LLM-24 to DH-T-LLM-27): they cover `DH-T-CLAUDECODECLI-1` to `-4` and `DH-T-COPILOTCLI-1` to `-4` (a re-executed stand-in client replaying recorded lines: one settlement per attempt, the failure mapping, a too-old version refused before the request, the environment scrub, the invocation digest equal to one rebuilt from the log, a tool call returned unexecuted, cancellation stopping the process tree); and the operator's manual records `DH-E-CLAUDECODECLI-1` and `DH-E-COPILOTCLI-1` (plan 16.5.9) are filed, the Claude one passing.

The four cloud and sign-in deliverables below are later, lower-priority work by [DH-ADR-25](../design.md#dh-adr-25); priority does not weaken their completion criteria. Each is its own provider component that obtains a token or signs a request and then uses the shared transport and an existing driver ([DH-ADR-PIAI-3](../libs/earendil-works-pi.md#dh-adr-piai-3)). Each package is compiled only under a build tag, so the default binary links no vendor module (recommended default, not confirmed by the owner). A vendor module is allowed for authentication only, verified and recorded as a library item when the component is specified; none is named here. All four depend on [DH-D-LLM-4](#dh-d-llm-4), [DH-D-HUMAN-8](interaction-credentials.md#dh-d-human-8) (the credential store and its lock) and [DH-D-HUMAN-1](interaction-credentials.md#dh-d-human-1) (the interaction registry, for a pending sign-in), and need an account on the platform at run time.

<a id="dh-d-llm-7"></a>
### DH-D-LLM-7 Bedrock models through the AWS credential chain
- **Result and why it matters.** A user whose models are reachable only through AWS can use them. **How it is implemented.** The AWS credential chain and request signing over the Messages driver, inside the platform's framing.
- **Components and files.** `internal/llm/provider/bedrock`, component `llm.bedrock`. **Depends on.** [DH-D-LLM-4](#dh-d-llm-4), [DH-D-HUMAN-8](interaction-credentials.md#dh-d-human-8), [DH-D-HUMAN-1](interaction-credentials.md#dh-d-human-1).
- **Priority.** P2. **Outcome.** [DH-O-8](../design.md#dh-o-8). **Done when.** DH-T-LLM-17 passes for this component and [DH-E-LLM-7](#dh-e-llm-7) has passed.

<a id="dh-d-llm-8"></a>
### DH-D-LLM-8 Vertex models through Google application-default credentials
- **Result and why it matters.** A user whose models are reachable only through Google Cloud can use them. **How it is implemented.** Application-default credentials, project and location over the Gemini and Messages drivers.
- **Components and files.** `internal/llm/provider/vertex`, component `llm.vertex`. **Depends on.** [DH-D-LLM-4](#dh-d-llm-4), [DH-D-HUMAN-8](interaction-credentials.md#dh-d-human-8), [DH-D-HUMAN-1](interaction-credentials.md#dh-d-human-1).
- **Priority.** P2. **Outcome.** [DH-O-8](../design.md#dh-o-8). **Done when.** DH-T-LLM-17 passes for this component and [DH-E-LLM-8](#dh-e-llm-8) has passed.

<a id="dh-d-llm-9"></a>
### DH-D-LLM-9 Azure OpenAI deployments through Microsoft Entra ID
- **Result and why it matters.** A user whose models are reachable only through an Azure deployment can use them. **How it is implemented.** Entra ID tokens, refreshed under a cross-process lock, deployment names and the API version over the Chat Completions and Responses drivers.
- **Components and files.** `internal/llm/provider/azureentra`, component `llm.azure-entra`. **Depends on.** [DH-D-LLM-4](#dh-d-llm-4), [DH-D-HUMAN-8](interaction-credentials.md#dh-d-human-8), [DH-D-HUMAN-1](interaction-credentials.md#dh-d-human-1).
- **Priority.** P2. **Outcome.** [DH-O-8](../design.md#dh-o-8). **Done when.** DH-T-LLM-17 passes for this component and [DH-E-LLM-9](#dh-e-llm-9) has passed.

<a id="dh-d-llm-10"></a>
### DH-D-LLM-10 A model vendor's OAuth sign-in
- **Result and why it matters.** A user who signs in with an issuer instead of a key can use its models. It is built for a vendor only after that vendor's terms are found to permit a third-party client (recommended default, not confirmed by the owner). **How it is implemented.** An OAuth sign-in in the system browser as a pending interaction, a stored refreshable grant under a cross-process lock, revocation on sign-out, over whichever driver the issuer's API speaks.
- **Components and files.** `internal/llm/provider/oauth`, component `llm.oauth`. **Depends on.** [DH-D-LLM-4](#dh-d-llm-4), [DH-D-HUMAN-8](interaction-credentials.md#dh-d-human-8), [DH-D-HUMAN-1](interaction-credentials.md#dh-d-human-1).
- **Priority.** P2. **Outcome.** [DH-O-8](../design.md#dh-o-8). **Done when.** DH-T-LLM-17 passes for this component and [DH-E-LLM-10](#dh-e-llm-10) has passed.

## 9. Acceptance

### 9.1 Local tests

Local tests use fakes, recorded bytes and an in-process HTTP server. They are a different kind of evidence from section 9.2. They extend, and do not repeat, `DH-T-PIAI-1` to `-10` and `DH-T-EVENTSOURCEPARSER-1` to `-3`.

| Test ID | Proves | What is asserted |
|---|---|---|
| DH-T-LLM-1 | DH-D-LLM-1, DH-D-LLM-11 | Vocabulary: every type re-encodes to the same canonical bytes; `llm/vocab` imports no harness package but `schema`, and `session` imports no `llm` package but `llm/vocab`; no function of the seam takes a request other than `session.Request`. Assembler rules (output cap drops tool calls and their replay entries; interrupted text is kept, tool calls are not; a delta after `BlockEnd` is ignored). Fuzz: expanding a recorded stream gives back the same chunks and times. A table of every chunk type is handled by every consumer switch |
| DH-T-LLM-2 | DH-D-LLM-1 | A fake adapter that errors, panics, ends without a finish, yields after a finish or is cancelled always produces exactly one `Finish` and one attempt record; a consumer error is not an `llm/` failure. Run with the race detector |
| DH-T-LLM-3 | DH-D-LLM-1 | A prepared call keeps its endpoint, facts and policy when the route's config changes mid-stream; reuse and a config mismatch fail; an unsupported effort or option fails before I/O |
| DH-T-LLM-4 | DH-D-LLM-1 | Unknown route fails with `llm/no-route` and no other route receives a request; a duplicate route fails registration and leaves the old set serving; `StreamAround` cannot change the route; replay state is withheld from a different route |
| DH-T-LLM-5 | DH-D-LLM-2 | Golden request bytes and recorded streams for Messages; a system update that cannot be placed is consolidated; a surrogate pair split across two deltas and a multi-byte character delivered one byte at a time both arrive intact |
| DH-T-LLM-6 | DH-D-LLM-4 | The conformance table gives the same chunks from all four drivers; a coverage matrix fails if a chunk type or finish reason is produced by fewer than two protocol families |
| DH-T-LLM-7 | DH-D-LLM-3 | Under virtual time: bounded count; persistent elapsed cap; a Permanent failure is never retried; `Retry-After` is honoured inside the bounds; the retry event is appended before the timer starts; a cancel during the wait appends `llm/retry-abandoned` and makes no attempt; the step cap binds two listeners |
| DH-T-LLM-8 | DH-D-LLM-3 | `Complete` calls its sink once per attempt and exposes no chunk of a failed attempt |
| DH-T-LLM-9 | DH-D-LLM-4 | The embedded catalogue validates, and every vendor entry has a display name; overlay order, `hidden` and explicit `none`; a followed listing adds ids; an unlisted id is accepted; discovery stops at its page bound and says so; a plain header value under a credential-bearing name is refused |
| DH-T-LLM-10 | DH-D-LLM-4 | Golden projected schema per protocol for every registered tool; arguments are validated against the canonical document |
| DH-T-LLM-11 | DH-D-LLM-2 | A fresh home sends a fake server only the model request, the credential and `User-Agent: dsh`; each `Send` switch adds exactly its own header or field; the off-switch removes all; a header listener cannot replace the credential or identity |
| DH-T-LLM-12 | DH-D-LLM-5 | Version gate including pre-releases; a model with no declaration and an unknown id is unavailable; a refused model fails before I/O; one bad row is skipped; the stored listing is served offline; `ModelsChanged` fires on change; badge text rules |
| DH-T-LLM-13 | DH-D-LLM-5 | Budget on encoded bytes; quanta; an image in a role the protocol cannot carry becomes placeholder text; a Files failure rebuilds the whole request inline; cool-down by time; one replacement after a stale file id |
| DH-T-LLM-14 | DH-D-LLM-5 | The account route never uses an API key; a 401 invalidates only the token it was sent with; sign-out cancels agents whose latest header names the account credential, also with the provider component disabled, and keeps their inboxes |
| DH-T-LLM-15 | DH-D-LLM-6 | Folding from a checkpoint equals folding from zero; disjoint usage buckets; a failed attempt and a retry are each counted once; state size does not grow with message count; a system-prompt replacement is repriced at once; calibration is deterministic |
| DH-T-LLM-16 | DH-D-LLM-2 | Contributions are prepared after serialisation, merged in name order, accepted only after 2xx; a collision fails the call; a failing contributor is omitted; cancellation stops the wait |
| DH-T-LLM-17 | DH-D-LLM-7, DH-D-LLM-8, DH-D-LLM-9, DH-D-LLM-10 | With a fake token source or signer, each component streams through the shared transport and an existing driver unchanged; a refresh is serialised under the store lock; no vendor module is imported outside its component |
| DH-T-LLM-18 | DH-D-LLM-12 | The shared client-route package (`llm/provider/clientroute`), against a re-executed test binary: the version probe refuses a missing, unrecognised, too-old, failing or hung client with `llm/client-upgrade-required` (a cancelled caller is `llm/aborted`) and ignores the lines after the first; the locator searches the child's `PATH`, never the host's; the child environment holds no removed name, removal pattern, default-deny name or secret-store name, and an extra entry that would bring one back or override a route setting is refused; the renderer is a pure function (one user message as its text, a longer conversation as a transcript with tool calls and results, no reasoning, images as placeholder text, tools as MCP entries, the system text at the head of the prompt for a client that cannot replace its own); the invocation record writes the attempt directory as `{attempt}`, keeps environment names only, and its digests equal those recomputed; the attempt directory is private and removed; the runner reads bounded lines, keeps a bounded window on standard error, and a cancel stops the client and its grandchild |
| DH-T-LLM-19 | DH-D-LLM-12 | The listing-only MCP server answers `initialize` (the client's protocol revision when known), `ping` and `tools/list` from the tool schema file, refuses every `tools/call` with `mcp-listing/call-refused` and records it by tool name only, never its arguments, and ends with its input; a bad invocation or schema file is status 2 |
| DH-T-LLM-20 | DH-D-LLM-12 | The Claude route's invocation (`DH-T-CLAUDECODECLI-1`), with a re-executed stand-in `claude`: Scenario "A client route settles once" (one `assistant/message` with the result's usage; the logged argument list equals the one rebuilt from the log with `{attempt}` and its digest; the request rebuilt from the log at `requestAsOf` renders to the logged standard-input and file digests; both equal what the stand-in received; the client ran in an empty private directory, removed afterwards, after one probe); Scenario "No ambient credential reaches the client" (credentials and provider overrides in the host environment are absent in the client, the route's settings present); the argument list of [../libs/claude-code-cli.md](../libs/claude-code-cli.md) sec. 5.1; a client below the floor, with an unrecognised version line or missing fails `Prepare` with `llm/client-upgrade-required` and never receives the request; a control the client cannot take is refused before the probe |
| DH-T-LLM-21 | DH-D-LLM-12 | The Claude route's stream mapping (`DH-T-CLAUDECODECLI-2`): recorded text, reasoning and text, one tool call and two parallel tool calls give the chunks and finish of claude-code-cli.md sec. 5.2; a tool call comes back unexecuted (M1-D-PIPE-6 A: listed by the listing server, denied by the client, `ToolCallDelta` under the harness's name and `Finish{ToolCalls}`, the listing server stopped with the tree); unknown line types are ignored; every line the route cannot place (no init, a second model request or result, a tool the harness did not declare, a tool that ran, a result inside an unfinished message) fails with `llm/client-contract` |
| DH-T-LLM-22 | DH-D-LLM-12 | The Claude route's failure mapping (`DH-T-CLAUDECODECLI-3`): each row of claude-code-cli.md sec. 5.4 gives its code, class and status; signed out is `llm/sign-in-required` and a 401 `llm/auth`, Permanent, as exactly one `assistant/attempt` with its invocation, never retried by the route's policy, with a second registered route never prepared; a key source other than the sign-in is `llm/invalid-credential`; a tool run inside the client or a `tools/call` reaching the listing server is `llm/client-contract`; a token planted in the client's text and standard error never reaches a failure |
| DH-T-LLM-23 | DH-D-LLM-12 | The Claude route's cancellation (`DH-T-CLAUDECODECLI-4`): cancelling a stand-in that started the listing server and stalls mid-message ends the stream with `Finish{Aborted}` and one `assistant/attempt` carrying the invocation; the client and the listing server are both gone and the attempt directory is removed |
| DH-T-LLM-24 | DH-D-LLM-12 | The Copilot route's invocation (`DH-T-COPILOTCLI-1`), with a re-executed stand-in `copilot` replaying synthetic JSONL (pending the plan 16.5.9 recording): Scenario "A client route settles once" (one `assistant/message`; the logged argument list equals the one rebuilt from the log with `{attempt}` and its digest; the request rebuilt from the log at `requestAsOf` renders, with the system text as the leading `<system>` section of standard input, to the logged standard-input and file digests; both equal what the stand-in received; the harness-chosen client session id is logged and is the one passed, M1-D-PIPE-7 A; the client ran in an empty private directory, removed afterwards, after one probe); Scenario "No ambient credential reaches the client" (`GH_TOKEN`, `GITHUB_TOKEN`, `COPILOT_GITHUB_TOKEN` and the provider, model, approval and build overrides set in the host are absent in the client; `COPILOT_AUTO_UPDATE=false` and `COPILOT_TELEMETRY_DISABLED=true` present; the client home passed on unread); the argument list of [../libs/github-copilot-cli.md](../libs/github-copilot-cli.md) sec. 5.1; a client below 1.0.115, with an unrecognised `--version` line or missing fails `Prepare` with `llm/client-upgrade-required` and is never started with the request; a control the client cannot take is refused before the probe; on Windows Scenario ".cmd client started by bare name" at the adapter: a `copilot.cmd` stand-in on the child's `PATH` is probed and run through the checked batch opt-in and receives the logged argument list unchanged |
| DH-T-LLM-25 | DH-D-LLM-12 | The Copilot route's stream mapping (`DH-T-COPILOTCLI-2`): synthetic text, reasoning and text, one tool request and two parallel ones give the chunks and finish of github-copilot-cli.md sec. 5.2; a tool call comes back unexecuted (M1-D-PIPE-6 A: listed by the listing server, `ToolCallDelta` under the harness's name and `Finish{ToolCalls}`), the tree stopped at the requesting message before its chunks are yielded, so a stalled stand-in and its listing server are gone and the lines after the message (a `tool.execution_start`, a second turn) are never read; unknown line types are ignored; a placed line without its fields, a delta after its block ended, a line after the result and a result inside an unfinished message fail with `llm/client-contract` |
| DH-T-LLM-26 | DH-D-LLM-12 | The Copilot route's failure mapping (`DH-T-COPILOTCLI-3`): signed out (a non-zero exit with no line the route can place) is Permanent, exactly one `assistant/attempt` with its invocation, never retried by the route's policy, with a second registered route never prepared; its code is `llm/client-contract` until DH-E-COPILOTCLI-1 pins the signed-out line (the `llm/sign-in-required` check stays open); a structured error line (inference: `session.error`, `data.errorType`) naming authentication is `llm/sign-in-required` before any assistant line and `llm/auth` after one, a rate limit `llm/rate-limit` (Transient); a `tool.execution_start` line, a `tools/call` reaching the listing server, unparseable output and output without a result are `llm/client-contract`; a token planted in the client's text and standard error never reaches a failure |
| DH-T-LLM-27 | DH-D-LLM-12 | The Copilot route's cancellation (`DH-T-COPILOTCLI-4`): cancelling a stand-in that started the listing server and stalls mid-message ends the stream with `Finish{Aborted}` and one `assistant/attempt` carrying the invocation; the client and the listing server are both gone and the attempt directory is removed |

### 9.2 Real-environment scenarios

The key is supplied at run time through the environment of a credentialed workflow ([DH-ADR-TOOLCHAIN-12](../upstream/toolchain.md#dh-adr-toolchain-12)); it is never written to a file or a report. Each scenario records the build identity, route, model id, provider request ids and the exported session log.

[DH-E-LLM-1](#dh-e-llm-1) and [DH-E-LLM-3](#dh-e-llm-3) take no key. Under OWNER-2 (no DeepSeek and no API key; real-model checks only through the user's own signed-in Claude and Copilot) they run on the signed-in client routes of [DH-D-LLM-12](#dh-d-llm-12): the user's installed official client, signed in by the user beforehand, which dsh starts as a child process and never reads a token of (M1-D-PIPE-3 A). They were re-authored onto these routes by `docs/stories/harness-DSH-M1/gap-closure-plan.md` step 16.5.8 (M1-D-CLOSE-4 A). Each runs once per route, `claude-code` and `copilot-cli`; the `claude-code` leg must pass and the `copilot-cli` leg is run and its result recorded either way (M1-D-LOOP-6 A). Each records the build identity, route, model id, the client invocations (client, version, client session id) and the exported session log. A client route has no provider request id.

<a id="dh-e-llm-1"></a>
#### DH-E-LLM-1 A turn with reasoning and a tool call on the vendor's own protocol
- **Proves.** DH-D-LLM-1, DH-D-LLM-2, DH-D-LLM-6, on the signed-in client route of [DH-D-LLM-12](#dh-d-llm-12) (for DH-D-LLM-2, its Messages decoder, which reads the Claude client's `stream_event` lines).
- **Given.** The built `dsh` binary and a fresh temporary harness home; no API key anywhere (the test kit's launcher removes every credential-shaped name from dsh's environment); the user's own installed official client on the `PATH`, at or above its version floor and signed in by the user beforehand; its route (`claude-code`, then `copilot-cli`) registered by a configuration layer; a model and a reasoning effort at which it reasons; one harmless test tool, declared and answered by the scenario's SDK client. File: `src/harness/internal/testkit/scenarios/llm_turn_scenario_test.go`, test `TestTurnWithReasoningAndToolCall`, one subtest per route.
- **When.** Per route, through the SDK of the headless host: (1) send a prompt that requires the tool; (2) let the turn finish; (3) send a second prompt and cancel it after its first text delta.
- **Then.** Turn 1 has two steps; each attempt is exactly one `assistant/message` that records its client invocation (the route's client, its version, the argument and standard-input digests and a client session id of its own, a new one per attempt). The first message has reasoning and a tool call whose arguments are valid JSON; the client returned the call unexecuted and the harness ran it once, through its guarded pipeline, as one `tool/result`. Every message reports usage, and nothing of an attempt streams after its end. The second step is accepted. A client route renders no reasoning back to the client (DH-T-LLM-18), so the second request is not checked for it. Turn 2 ends aborted with its cause and one `assistant/attempt` of class `cancelled` that records its invocation; stopping the client's process tree is proved locally by DH-T-LLM-23. `tokenUsage` equals the sum of the client-reported usage of all three attempts. The `claude-code` leg must pass; the `copilot-cli` leg is run and recorded either way.
- **Cleanup.** The launcher deletes the temporary home. Nothing of the user's sign-in or client settings is read or changed, so nothing else is undone. On failure keep the exported log and the logged client invocations.

<a id="dh-e-llm-2"></a>
#### DH-E-LLM-2 The same turn on a second protocol
- **Proves.** DH-D-LLM-4.
- **Given.** The built `dsh` binary, a fresh temporary harness home, `DEEPSEEK_API_KEY` set at run time, route `deepseek-official` on the vendor's Messages endpoint, a model that supports reasoning, one harmless test tool, and a keyed route with protocol OpenAI Chat Completions on the vendor's Chat Completions endpoint with the same key. Where keys are supplied, the scenario repeats for an OpenAI Responses route and a Gemini route.
- **When.** On each route, headless: send a prompt that requires the test tool and let the turn finish; then send a prompt that makes the model call two tools in one step.
- **Then.** Each route yields the same block kinds in the same order as the Messages route, raw argument text, disjoint usage and one event per attempt; parallel tool calls have distinct indexes; a follow-up step on the same route is accepted with its replay state. A raw stream from each route is captured and scrubbed as a conformance fixture.
- **Cleanup.** Delete the temporary home. On failure keep the exported log and the provider request ids. A route whose key was not supplied is reported as not run, never as passed.

<a id="dh-e-llm-3"></a>
#### DH-E-LLM-3 A failure is a fact: no retry and no fallback when the client is signed out
- **Proves.** DH-D-LLM-1, DH-D-LLM-2, DH-D-LLM-3, on the signed-in client routes of [DH-D-LLM-12](#dh-d-llm-12). "Signed out" replaces the earlier "bad key" (M1-D-CLOSE-4 A): a client route holds no key to get wrong.
- **Given.** The built `dsh` binary and fresh temporary homes; no API key anywhere; the user's own installed client of the route under test; a configuration layer that registers that route (`claude-code`, then `copilot-cli`) and the other client route as a second route, with retry mode `persistent`. For the first turn the client is signed out: its configuration home (`CLAUDE_CONFIG_DIR` for Claude Code, `COPILOT_HOME` for the Copilot CLI) points at an empty temporary directory in dsh's environment, so the user's own sign-in is not seen. For the later turns the client is signed in as the user. File: `src/harness/internal/testkit/scenarios/llm_signed_out_scenario_test.go`, test `TestSignedOutIsAFact`, one subtest per route.
- **When.** Per route: (1) with the client signed out, send one prompt on the route; then, on a second dsh with the client signed in, (2) send one prompt with `MaxTokens` set and (3) send the same prompt without it.
- **Then.** (1) The turn ends with `llm/sign-in-required`; it holds exactly one `assistant/attempt` (class Permanent) that records its client invocation, `llm/retry-abandoned` with reason "not retryable", and no `llm/retry`; no settlement of the session names the second route's client, so nothing fell back. (2) A client route takes no output cap ([../libs/claude-code-cli.md](../libs/claude-code-cli.md), [../libs/github-copilot-cli.md](../libs/github-copilot-cli.md) sec. 5.1), so the cap is refused before the client starts: the turn ends `error` with `llm/unsupported-option`, no settlement records a client invocation, and nothing is retried. (3) The turn completes with one `assistant/message` of the route's client and no executed tool call, which shows the failure of (1) was the sign-in and not the route. The `claude-code` leg must pass; the `copilot-cli` leg is run and recorded either way. Until the Copilot CLI's signed-out line is pinned by `DH-E-COPILOTCLI-1`, its signed-out run may settle as a Permanent failure carrying the exit code rather than `llm/sign-in-required` ([../libs/github-copilot-cli.md](../libs/github-copilot-cli.md) sec. 5.4), and an empty `COPILOT_HOME` may not sign it out (sec. 7); that leg's result is recorded as it falls.
- **Cleanup.** Delete the temporary homes and the empty configuration home. Nothing of the user's sign-in is read or changed. On failure keep the exported logs and the logged client invocations.

<a id="dh-e-llm-4"></a>
#### DH-E-LLM-4 Every provider accepts the projected tool schemas
- **Proves.** DH-D-LLM-4.
- **Given.** The built `dsh` binary and a fresh temporary home; one route per protocol for which a real key is supplied at run time (at least Messages and Chat Completions); the full registered tool set of the default composition.
- **When.** For each route, send one request that declares every tool and asks for a call to a tool whose schema uses `oneOf`, `const`, `additionalProperties: false` and a `pattern`.
- **Then.** No provider rejects the request for its schema; the returned arguments pass the canonical validator. A rejection names the keyword and reopens the table of [DH-ADR-LLM-8](#dh-adr-llm-8).
- **Cleanup.** Delete the temporary home. On failure keep the rejected request body and the provider request id.

<a id="dh-e-llm-5"></a>
#### DH-E-LLM-5 By default only the model request leaves the machine
- **Proves.** DH-D-LLM-2, DH-D-LLM-5.
- **Given.** A fresh home with default settings, a real vendor key, and a local recording proxy that terminates TLS with a test authority configured as an extra certificate authority.
- **When.** (1) Open the model picker, run one turn, leave the host idle for longer than the listing age. (2) Turn on `Installation`, `Session` and the inventory contribution and run one turn. (3) Turn on the one off-switch and run one turn.
- **Then.** In (1) the proxy sees one listing request made on use, none while idle, `User-Agent: dsh`, and no identifying header or `dsh_` body field. In (2) exactly the three enabled items appear. In (3) they are gone again. Models listed with a minimum version above the build's compatibility version appear as unavailable.
- **Cleanup.** Stop the proxy, delete the home and the test authority.

<a id="dh-e-llm-6"></a>
#### DH-E-LLM-6 The account route, images and sign-out
- **Proves.** DH-D-LLM-5.
- **Given.** A real vendor account signed in through the system browser by a person, a valid API key also configured, an image-capable model, one image attached.
- **When.** Run a turn with the image on `deepseek-account`. Start a second turn that runs a long tool, and sign out while the tool runs.
- **Then.** The image is uploaded once and referenced by file id; a second turn reuses the id. On sign-out the running turn is cancelled with the signed-out cause, its inbox is kept, no request is made with the API key, and the next request on the route fails with `llm/sign-in-required`.
- **Cleanup.** Delete the files the run uploaded (the ids in the upload index), sign out, delete the home. If deletion fails, keep the id list.

<a id="dh-e-llm-7"></a><a id="dh-e-llm-8"></a><a id="dh-e-llm-9"></a><a id="dh-e-llm-10"></a>
#### DH-E-LLM-7 to DH-E-LLM-10 One scenario per cloud sign-in component
| ID | Proves | Given | When | Then | Cleanup |
|---|---|---|---|---|---|
| **DH-E-LLM-7** | DH-D-LLM-7 | A real AWS account with model access; credentials from the standard chain at run time | Run the turn of DH-E-LLM-1 through `llm.bedrock`; then run it with the credentials removed | Signed requests are accepted and streamed; without credentials the call fails with `llm/missing-credential` before I/O and no other route is used | Delete the home; nothing is created in the account |
| **DH-E-LLM-8** | DH-D-LLM-8 | A real Google Cloud project with application-default credentials at run time | Run the turn of DH-E-LLM-1 through `llm.vertex`; then run it with the credentials removed | Authenticated requests are accepted and streamed; without credentials the call fails with `llm/missing-credential` before I/O and no other route is used | Delete the home; nothing is created in the project |
| **DH-E-LLM-9** | DH-D-LLM-9 | A real Azure OpenAI deployment and an Entra identity at run time | Run the turn of DH-E-LLM-1 through `llm.azure-entra`; let the token expire once; then run it with the identity removed | Requests are accepted and streamed; the expired token is refreshed once under the lock; without the identity the call fails with `llm/missing-credential` before I/O and no other route is used | Delete the home; nothing is created in the subscription |
| **DH-E-LLM-10** | DH-D-LLM-10 | A real issuer account; a person completes the sign-in in the system browser | Sign in, reload the page mid-sign-in, run a turn, sign out | The reload re-attaches to the pending sign-in; the turn streams; sign-out revokes the grant at the issuer and the next call fails with `llm/sign-in-required` | Revoke the grant if the run failed before sign-out; delete the home |

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

**Recommended defaults, not confirmed by the owner.**
1. The compatibility version a Go build declares to the vendor's version gate is unset until the vendor lists this client ([DH-ADR-LLM-6](#dh-adr-llm-6)).
2. Sign-in to another company's consumer subscription ([DH-D-LLM-10](#dh-d-llm-10), `llm-pi-ai.4`) stays a later, default-off component, built per vendor only after that vendor's terms are checked and found to permit a third-party client.
3. The cloud sign-in components ([DH-D-LLM-7](#dh-d-llm-7) to `-10`) are behind a build tag and are not in the default binary, because they link vendor modules and [DH-ADR-PIAI-3](../libs/earendil-works-pi.md#dh-adr-piai-3) assumes an unused platform is not linked. A user who needs one uses the second build.

The client name and version is decided ([DH-ADR-32](../design.md#dh-adr-32), [DH-ADR-LLM-7](#dh-adr-llm-7)).

**Unverified facts.** The schema projection table; the field that carries Gemini tool-call signatures; the Responses default for strict mode. Each is settled only by a scenario. The shape of the listing's effort-level field (register entry U-K1-12) is obtained by [DH-E-LLM-5](#dh-e-llm-5), which keeps one real listing; a tolerant read is then added to the catalogue refresh, and until then the embedded catalogue supplies effort levels. `egress` supplies the `User-Agent: dsh` hook and the idle-timer body wrapper `IdleBody` ([DH-D-UTIL-2](util-test-support.md#dh-d-util-2)).

**Limits kept.** The version gate is not a security control. Token counts are estimates. Images are input only and never URLs. A request that was sent and cancelled may be billed. Until a cloud component is built, its platform is reachable only through a key-authenticated endpoint. A key is checked only by a real request: the route check of [DH-D-LLM-4](#dh-d-llm-4) is the user-started "test this key" call, and nothing verifies a key without sending one. Upstream contradicts itself on whether a listing change is announced (`packages/llm/llm-deepseek-account/README.md:32` against `:97`); this design announces it (`llm.ModelsChanged`).

## 11. Units dispositioned by this spec

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 9 | 6 ported, 3 replaced |
| `UP-DEBT` | 70 | 63 resolved, 3 not-applicable, 4 carried |
| `UP-EVT` | 4 | 4 replaced by typed keys |
| `UP-CFG` | 6 | 6 replaced by Go config structs |
| `UP-SVC` | 3 | 3 replaced by Go service keys |
| `UP-DEC` (handed over) | 19 | 15 ported, 3 replaced, 1 not-applicable |

The four `carried` rows name their stage in the ledger: three name [DH-S-47](../impl.md#dh-s-47) (known limits and scope boundaries of the core areas) and one names [DH-S-35](../impl.md#dh-s-35) (keychain, vendor account and vendor services).

Rows are in [../trace/fragments/A04.jsonl](../trace/fragments/A04.jsonl). Go items declared: `DH-ADR-LLM-1` to `-9`, `DH-D-LLM-1` to `-11`, `DH-T-LLM-1` to `-17`, `DH-E-LLM-1` to `-10`.
