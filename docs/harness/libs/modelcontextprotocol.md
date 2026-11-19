# @modelcontextprotocol packages (MCP SDK): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Facts about the Go module were read from its source in the Go
> module cache on 2026-11-18 and are cited as paths inside that module.

| | |
|---|---|
| **Library ID** | `DH-LIB-MCP` |
| **Upstream units** | `UP-DEP-@modelcontextprotocol/client`, `UP-DEP-@modelcontextprotocol/core`, `UP-DEP-@modelcontextprotocol/server`, `UP-DEP-@modelcontextprotocol/sdk`, `UP-DEP-@modelcontextprotocol/node`, `UP-DEP-@modelcontextprotocol/server-everything`, `UP-DEP-@modelcontextprotocol/server-filesystem` |
| **Upstream version** | `client`, `core`, `server` 2.2.0; `node` 2.0.0; `sdk` 1.31.0; the two test servers 2026.8.31 (`pnpm-lock.yaml:22467` to `:22503`) |
| **Kind** | runtime (`client`, `core`, `server`, `sdk`); test-only (`node`, `server-everything`, `server-filesystem`) |
| **Used by** | `mcp`, `experimental` (Computer Use MCP server), `subagent` (declared only) |
| **Go choice** | named Go module `github.com/modelcontextprotocol/go-sdk` v1.8.0, in the client role and the server role |
| **Related areas** | MCP client bridge, manager and resources (`../areas/mcp-lsp-web-connector.md`); Computer Use MCP server (`../areas/x-cua-browser-voice.md`); wire decision [DH-ADR-WIRE-8](../upstream/wire-and-api.md#dh-adr-wire-8) |

## 1. What it is and why upstream needs it

The **Model Context Protocol (MCP)** is an open protocol, built on JSON-RPC 2.0, through
which a *server* offers tools, resources and prompts to a *client* that runs a model. A
**tool** is a function the model may call; a **resource** is a document the model may
read by address; **server instructions** are text a server asks the client to show the
model. The protocol has dated **revisions** (for example 2025-11-25 and 2026-07-28); the
two sides agree on one when they connect. A **transport** is how the bytes travel: a
child process's stdin and stdout (**stdio**), or HTTP (**Streamable HTTP**, and before it
an older HTTP-plus-server-sent-events transport called **SSE**).

The `@modelcontextprotocol/*` npm packages are the official TypeScript implementation of
that protocol (the "SDK"). They give upstream three things it does not want to write
itself: the wire protocol and revision negotiation, the transports, and the OAuth 2
client steps a protected HTTP server requires.

Upstream uses the SDK for three jobs:

1. **As a client.** `mcp-client` connects to each configured external server and turns
   its tools into ordinary harness tools named `mcp__<serverName>__<tool>`
   (`packages/mcp/mcp-client/README.md:12`). This is how third-party tools, the browser
   providers ([playwrightmcp.md](playwrightmcp.md),
   [chrome-devtools-mcp.md](chrome-devtools-mcp.md)) and connectors reach the model.
2. **As a server.** The experimental `cua-mcp` profile serves three Computer Use tools to
   another agent (Claude Code, Codex) over stdio
   (`packages/experimental/cua-mcp/src/index.ts:97`).
3. **As test fixtures.** Tests start small MCP servers, in process and as child
   processes, to exercise the client.

## 2. How upstream uses it

### 2.1 Which package each consumer uses, and why there is more than one

| Package | Consumer | What it is used for | Why it is a separate package |
|---|---|---|---|
| `client` 2.2.0 | `packages/mcp/mcp-client` (runtime); tests of `mcp-manager` and `cua-mcp` | The client session, both client transports, OAuth helper functions, error classes | Version 2 of the TypeScript SDK split the client and the server into separate packages. |
| `core` 2.2.0 | `packages/mcp/mcp-client` (runtime) | Only five OAuth validation schemas, to check stored grant and client records (`packages/mcp/mcp-client/src/oauth-credentials.ts:6`) | `client` does not re-export them, so upstream imports the shared inner package directly. |
| `server` 2.2.0 | `packages/experimental/cua-mcp` (runtime); `mcp-client` tests | Low-level `Server`, stdio server transport, protocol type schemas; `McpServer` for fixtures | The server half of the same split. |
| `node` 2.0.0 | `mcp-client` tests only | `toNodeHandler`: adapts the server package's web-standard HTTP handler to Node's `http` module for the HTTP and OAuth fixtures (`packages/mcp/mcp-client/tests/http-fixture.ts:6`) | Node-specific glue, published separately; it is one minor version behind the others. |
| `sdk` 1.31.0 | `packages/subagent/subagent-claude-code` (declared, never imported) | Nothing in upstream source. It is a required peer of the Claude Agent SDK (`pnpm-lock.yaml:20917`), and a test asserts the manifest lists it (`packages/subagent/subagent-claude-code/tests/subagent-claude-code.spec.ts:359`). | It is **version 1** of the same SDK, a single package. The Claude Agent SDK and both reference servers still depend on it (`pnpm-lock.yaml:30210`, `:30221`). |
| `server-everything`, `server-filesystem` 2026.8.31 | `mcp-client` end-to-end tests only | Reference servers started as real child processes (`packages/mcp/mcp-client/tests/mcp-client.e2e.ts:332`, `:397`) | Separate published programs, not libraries. |

So "more than one package" has two causes. One is harmless: the version 2 SDK is
published in parts. The other is tech debt: two generations of the SDK (1.31.0 and 2.x)
are installed side by side, and upstream reaches into the SDK's inner `core` package
(section 3).

### 2.2 Use sites and the API surface used

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/mcp/mcp-client/src/transport.ts:36` | Start a stdio server as a child process | `StdioClientTransport({command, args, env, cwd})`; the SDK performs the spawn |
| `packages/mcp/mcp-client/src/transport.ts:43` | Connect to an HTTP server | `StreamableHTTPClientTransport(url, {requestInit.headers, fetch, authProvider, onInsufficientScope: 'throw'})` |
| `packages/mcp/mcp-client/src/connection.ts:322` | One client per connection attempt | `new Client(info, {capabilities: {}, versionNegotiation: {mode: 'auto'}, listChanged.tools})`: no client capabilities, automatic revision choice, a callback when the tool list changes |
| `packages/mcp/mcp-client/src/generation.ts:41`, `:55`, `:105` | Connect with a deadline, observe closure, close | `client.connect(transport, {signal, timeout})`, `client.close()`; upstream replaces the transport's `start`, `close` and `onclose` to learn whether it really closed |
| `packages/mcp/mcp-client/src/tools.ts:155`, `:193` | List and call tools | `listTools(undefined, {cacheMode: 'refresh', signal})`, `callTool({name, arguments}, {signal, timeout, toolDefinition})` |
| `packages/mcp/mcp-client/src/tools.ts:156`, `:402`, `:182` | Validate results; detect unsupported tools | `AjvJsonSchemaValidator`, `specTypeSchemas.CallToolResult`, `tool.execution.taskSupport` |
| `packages/mcp/mcp-client/src/connection.ts:369`, `:422` to `:436` | Server instructions; resources | `getInstructions()`, `getServerCapabilities()`, `listResources`, `listResourceTemplates`, `readResource` |
| `packages/mcp/mcp-client/src/probe.ts:95`, `:103` | "Test this server" without registering anything | `getServerVersion()`, `client.request({method: 'tools/list'})` page by page |
| `packages/mcp/mcp-client/src/oauth.ts:246`, `:290`, `:218`, `:302` | Explicit browser authorization | `extractWWWAuthenticateParams`, `auth(provider, ...)` (discovery, client registration, PKCE, authorization URL), `validateAuthorizationResponseIssuer`, `exchangeAuthorization` |
| `packages/mcp/mcp-client/src/oauth-credentials.ts:187`, `:210` | Token use and refresh on a live connection | `refreshAuthorization`, the `AuthProvider` callbacks `token` and `onUnauthorized` |
| `packages/mcp/mcp-client/src/diagnostic.ts:6`, `:91`, `:140` | Turn SDK failures into harness error codes | `ProtocolError`, `SdkError` and `SdkErrorCode`, `SdkHttpError`, `UnauthorizedError`, `InsufficientScopeError`, `OAuthError` |
| `packages/experimental/cua-mcp/src/server.ts:164`, `:243`, `:247` | Serve a tool list computed on every request | `new Server(info, {capabilities: {tools}, instructions})`, `setRequestHandler('tools/list' / 'tools/call')` |
| `packages/experimental/cua-mcp/src/server.ts:172`, `:174` | Ask the connected client's user for consent | `getClientCapabilities().elicitation`, `elicitInput({mode: 'form', ...})` |
| `packages/experimental/cua-mcp/src/index.ts:97` | Serve on stdin and stdout | `server.connect(new StdioServerTransport())` |
| `packages/mcp/mcp-client/tests/*` | Fixtures | `InMemoryTransport`, `McpServer`, `serveStdio`, `ResourceTemplate`, `createMcpHandler`, `toNodeHandler` |

### 2.3 MCP features the harness really uses, checked against the Go SDK

"Go SDK" below is `github.com/modelcontextprotocol/go-sdk` v1.8.0.

| Feature | Used upstream? | Go SDK | Gap |
|---|---|---|---|
| stdio client (child process) | Yes (`transport.ts:36`) | `CommandTransport` over an `*exec.Cmd`, or `IOTransport` over any reader and writer (`mcp/cmd.go`, `mcp/transport.go`) | None. See DH-ADR-MCP-3. |
| Streamable HTTP client | Yes (`transport.ts:43`) | `StreamableClientTransport` with a caller-supplied `*http.Client`, retry limit and OAuth handler (`mcp/streamable.go:1966`) | None. |
| SSE client (the older HTTP transport) | No. Config accepts only `stdio` and `streamable-http` (`packages/mcp/mcp-client/README.md:57`) | `SSEClientTransport` exists (`mcp/sse.go:393`) | Not needed. Available if a later decision wants it. |
| stdio server | Yes (`cua-mcp/src/index.ts:97`) | `StdioTransport` (`mcp/transport.go:128`) | None. |
| HTTP server | Tests only | `NewStreamableHTTPHandler` returns a `net/http` handler (`mcp/streamable.go:231`) | None; no Node adapter is needed. |
| In-memory transport | Tests | `NewInMemoryTransports` (`mcp/transport.go`) | None. |
| Revision 2026-07-28 with fallback to older ones | Yes (`connection.ts:323`) | Latest is `2026-07-28`; also 2025-11-25, 2025-06-18, 2025-03-26, 2024-11-05 (`mcp/shared.go:50`). The client tries the new stateless `server/discover` first and falls back to `initialize` **on the same connection** (`mcp/client.go:308`). | None, and one improvement: no second process (section 3, debt 3). |
| List tools, with paging | Yes | `ListTools`, and an iterator `Tools` that follows cursors | The iterator has no page limit; the harness must cap pages itself. |
| Call a tool with cancellation and a deadline | Yes | `CallTool(ctx, ...)`; cancelling the context cancels the request | None. Deadline and cancel are told apart by the harness from its own context (section 6). |
| Tool-list change notice | Yes (`connection.ts:324`) | `ToolListChangedHandler`; the SDK opens the modern `subscriptions/listen` stream itself when needed (`mcp/client.go:356`) | None. |
| Structured results checked against the tool's output schema | Yes, by upstream's own code (`tools.ts:168`) | The client does not check | Same as upstream: the harness checks, with the schema module already chosen in [ajv.md](ajv.md). |
| Result content: text, image, audio, resource link, embedded resource | Yes (`tools.ts:607`) | Typed content values (`mcp/content.go`) | An **unknown** content type is a decode error for the whole result (`mcp/content.go:421`); upstream shows a placeholder for that one block (`tools.ts:629`). |
| Detect a tool that requires task-based execution | Yes, to refuse it (`tools.ts:182`) | The `Tool` type has no `execution` field, and the SDK has no task support (`ROADMAP.md` lists it as experimental future work) | **Gap.** The harness cannot refuse such a tool before calling it. |
| Server instructions | Yes (`connection.ts:369`) | `InitializeResult.Instructions` | None. |
| Resources: list, list templates, read | Yes (`connection.ts:426`) | `ListResources`, `ListResourceTemplates`, `ReadResource` | None. |
| Resource subscriptions | No (`packages/mcp/mcp-client/README.md:272`) | `Subscribe` exists | Not needed. |
| Prompts | No (`docs/subsystems/mcp.md:105`) | `ListPrompts`, `GetPrompt` exist | Not needed. |
| Sampling (a server asks the client's model to generate) | No; the client declares no capabilities (`connection.ts:323`) | Handler exists; deprecated in revision 2026-07-28 (`mcp/client.go`, `ClientOptions`) | Not needed. |
| Elicitation, as a client (a server asks the user a question) | No (`docs/subsystems/mcp.md:105`) | `ElicitationHandler` exists | Not needed. |
| Elicitation, as a server | Yes, form mode (`cua-mcp/src/server.ts:174`) | `ServerSession.Elicit` for older revisions; for 2026-07-28 a tool handler returns "input requests", and the SDK converts that to the older request for older clients (`mcp/mrtr.go:124`, `mcp/server.go:1619`) | None. This removes an upstream limit (section 3). |
| Roots (a client tells a server which folders it may use) | No | Exists. **By default the Go client advertises `roots`**; it must be given an empty capability set to advertise nothing (`docs/rough_edges.md:40`) | A pitfall, not a gap. Covered by `DH-LIB-MCP-R4`. |
| OAuth for HTTP servers | Yes (section 2.2) | `auth.OAuthHandler` interface, `auth.AuthorizationCodeHandler`, and building blocks in `oauthex` (challenge parsing, resource and server metadata, dynamic client registration); token exchange and refresh through `golang.org/x/oauth2` | Partly open. See section 7, items 4 and 5. |
| Tool list computed per request, as a server | Yes (`cua-mcp/src/server.ts:243`) | `AddTool` and `RemoveTools` at any time, and receiving middleware (`mcp/server.go:315`, `:613`, `:1860`) | None, and no deprecated class is involved. |

**Result.** The Go SDK lacks one thing upstream uses: the task-support marker on a tool.
Two behaviours differ (unknown content types; the default `roots` capability). The
OAuth pieces exist but two details need a spike before the area commits to them.

## 3. Tech debt upstream carries

No patch, pin comment or install-script exemption exists for these packages: `patches/`
has no file for them and `pnpm-workspace.yaml` does not mention them (searched for
`modelcontextprotocol`). The debt below was found in the calling code and READMEs. The
limits listed under each package README's "Known Limitations" are separate units owned
by the area specifications; this table covers what the *library* causes.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L3.1` | Two generations of the SDK are installed: 2.x split packages for the harness's own code, and 1.31.0 only because the Claude Agent SDK and the reference servers require it. `node` is also one minor behind. | `pnpm-lock.yaml:20917`, `:30210`; `packages/subagent/subagent-claude-code/package.json:55` | One Go module in one version serves every role (DH-ADR-MCP-1). No peer-dependency mechanism exists in Go. |
| `UP-DEBT-found.L3.2` | The bridge depends on SDK internals and on exact pins: it imports the inner `core` package, and the README warns that "the pinned MCP SDK is still evolving; a breaking upstream change requires updating the bridge". | `packages/mcp/mcp-client/src/oauth-credentials.ts:6`; `packages/mcp/mcp-client/README.md:292` | The SDK sits behind one harness-owned interface, and stored records are validated by harness types, not SDK types (DH-ADR-MCP-2). |
| `UP-DEBT-found.L3.3` | The SDK, not the host, starts the stdio child, so the host can only share its environment-scrub rule with it. Negotiation may also start a **temporary probe process** before the serving process, so a server command can run twice. | `packages/mcp/mcp-client/src/transport.ts:16`; `packages/mcp/mcp-client/README.md:28`, `:187` | The host starts the one child and gives the SDK only its pipes; the Go SDK negotiates on that same connection (DH-ADR-MCP-3). |
| `UP-DEBT-found.L3.4` | Workarounds for SDK behaviour inside the bridge: a fallback path that writes to stdout; an output check whose failure cannot be told from a server error, so the bridge repeats it; an abort reported as a timeout; closure that can only be observed by replacing transport methods; re-sync requests bound only by the SDK's 60-second default. | `packages/mcp/mcp-client/src/connection.ts:420`; `src/tools.ts:168`, `:199`; `src/generation.ts:41`; `README.md:273` | The harness interface returns a closed set of error kinds and an explicit close result; every request carries a harness deadline; stdout is tested (DH-ADR-MCP-2, `DH-LIB-MCP-R5`, `DH-LIB-MCP-R10`). |
| `UP-DEBT-found.L3.5` | The MCP server is built on a class and on elicitation accessors the SDK marks deprecated, behind lint suppressions, because the high-level class cannot compute a tool list per request. The visible result is that consent fails for clients on revision 2026-07-28. | `packages/experimental/cua-mcp/src/server.ts:32`, `:163`, `:171`; `packages/experimental/cua-mcp/README.md:172` | The Go SDK's ordinary server supports changing tools at run time, and its input-request mechanism serves both revision families (`DH-LIB-MCP-R9`). |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-MCP-R1` | A stdio server runs as exactly one child process per connection. The host starts it (scrubbed environment, working directory, sandbox, process group) and stops the whole process tree when the owning fiber unloads. | `transport.ts:36`; DH-ADR-WIRE-8; debt 3 |
| `DH-LIB-MCP-R2` | An HTTP server is reached by Streamable HTTP with configured headers, through an HTTP client the harness supplies (proxy, no redirects and HTTPS-or-loopback for OAuth entries). | `transport.ts:43`; `packages/mcp/mcp-client/README.md:125` |
| `DH-LIB-MCP-R3` | The newest revision both sides support is chosen without operator input, and an operator can pin a revision for one server. | `connection.ts:323`; section 7, item 1 |
| `DH-LIB-MCP-R4` | The client advertises no optional capability: no roots, sampling or elicitation. | `connection.ts:323` |
| `DH-LIB-MCP-R5` | Listing follows cursors up to a harness page cap. A call has a deadline and can be cancelled. Every failure is one of a closed set: tool error, connection closed (outcome unknown), timeout (outcome unknown), not sent, invalid result, request failed, authorization required. | `tools.ts:155`, `:193`; `diagnostic.ts:62` |
| `DH-LIB-MCP-R6` | A change of the server's tool list reaches one harness callback, whichever revision is in use. | `connection.ts:324` |
| `DH-LIB-MCP-R7` | Resources can be listed, templates listed, and one resource read. When the server did not advertise resources, lists are empty without a request. Server instructions are readable after connecting. | `connection.ts:369`, `:420` |
| `DH-LIB-MCP-R8` | OAuth: a connection uses only a saved grant and never opens a browser; an explicit flow does discovery, client registration or a configured client, PKCE, state and issuer checks, and code exchange; refresh is serialized by the credential store; no token, code or verifier appears in an error. | `oauth.ts:246` to `:322`; `oauth-credentials.ts:178` |
| `DH-LIB-MCP-R9` | As a server on stdio: a tool list computed per request, calls that honour cancellation, instructions, and a form question to the client's user that works on revision 2026-07-28 and on older ones. | `cua-mcp/src/server.ts:164` to `:254`; debt 5 |
| `DH-LIB-MCP-R10` | When the harness serves MCP on stdio, or serves any stdio protocol while it is also an MCP client, nothing but protocol frames is written to stdout. | `connection.ts:420`; DH-ADR-WIRE-8 |
| `DH-LIB-MCP-R11` | Tests need no Node: fixture servers are Go, in process over an in-memory transport or as a child built from Go source. | `packages/mcp/mcp-client/tests/fixture-server.ts:8`; DH-ADR-2 |
| `DH-LIB-MCP-R12` | Result content keeps block order; text, image, audio, resource link and embedded resource are decoded and the complete result can be re-encoded as JSON for programmatic callers. | `tools.ts:586` |
| `DH-LIB-MCP-R13` | The dependency builds with `CGO_ENABLED=0`. | release plan in the library brief |

## 5. Go choice

**Decision.** Use `github.com/modelcontextprotocol/go-sdk` v1.8.0 for the client role,
the server role and the test fixtures. This confirms the recommendation in
[DH-ADR-WIRE-8](../upstream/wire-and-api.md#dh-adr-wire-8) and adds three refinements,
recorded below as DH-ADR-MCP-1 to DH-ADR-MCP-3.

**Why.** It is the protocol project's own Go implementation, it already implements the
revision upstream selects (2026-07-28) and four older ones, and section 2.3 shows it
covers every transport and every feature upstream uses except one marker field.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **`github.com/modelcontextprotocol/go-sdk`** (chosen) | Official; newest revision plus fallback; client and server in one module; stdio, command, Streamable HTTP, SSE and in-memory transports; OAuth client pieces; ships a Go "everything" reference server | Brings `golang.org/x/oauth2`, `github.com/google/jsonschema-go`, `github.com/segmentio/encoding`, `github.com/yosida95/uritemplate/v3`, `golang.org/x/time`, `golang.org/x/sync` into the binary; a version 2 with breaking changes is planned (`docs/rough_edges.md`); no task support | v1.8.0, released 2026-09-04; `go 1.25.0` in `go.mod`; licence file: Apache-2.0 for new code, MIT for earlier contributions not yet relicensed; no `import "C"` in the module (text search); no stdout write outside `StdioTransport` in non-test files (text search); default logger discards (`mcp/logging.go:112`). Checked 2026-11-18 with `go list -m -json ...@latest`, `go mod download -json`, and reading the module cache. |
| `github.com/mark3labs/mcp-go` | Widely used community library; MIT | Not the protocol project's implementation, so revision support follows a third party; a second API to learn for no feature the official SDK lacks. Its revision and OAuth coverage were **not** examined. | v1.5.0, released 2026-10-30; MIT licence file; no `import "C"` (text search). Checked 2026-11-18 by the same method. |
| Own implementation on the harness JSON-RPC codec | No dependency; full control of process and errors | Re-implements two handshake styles, five revisions, Streamable HTTP resumption and the OAuth discovery chain, then tracks every future revision | Nothing to verify. |

**Tradeoff.** The harness takes six transitive modules and a library whose next major
version will break its API. The first is accepted because each is small and pure Go; the
second is contained by DH-ADR-MCP-2. The licences of the transitive modules were not
read in this check; the notices step of the release work must read them.

<a id="dh-adr-mcp-1"></a>
### DH-ADR-MCP-1 One Go module replaces every `@modelcontextprotocol` package

- **Decision.** One module, in one version, is the client, the server and the test
  fixtures. Verdict on upstream's package set: **change**.
- **Why.** Upstream needs five library packages in two generations (debt 1). Go has no
  peer dependencies, the Go SDK is not split, and its HTTP server is already a
  `net/http` handler, so the Node adapter has no counterpart.
- **Tradeoff.** The client role and the server role cannot be upgraded separately.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-13](../design.md#dh-o-13); consistent with DH-ADR-2 and DH-ADR-5.
- **Verified by.** `DH-T-MCP-10`: the harness module's dependency graph contains exactly
  one MCP module and builds with cgo disabled.

<a id="dh-adr-mcp-2"></a>
### DH-ADR-MCP-2 The SDK stays behind one harness-owned interface

- **Decision.** Exactly one harness package imports the SDK. It exposes a small
  interface (section 6) with harness types for tools, results, errors and close results.
  The tool registry, the manager, the resource tools, stored OAuth records and the UI
  never see an SDK type. Verdict on upstream's direct use of SDK classes: **change**.
- **Why.** Upstream's bridge is coupled to SDK error classes, transport methods and an
  inner package (debts 2 and 4). The Go SDK announces API changes for its version 2.
  Stored records validated by SDK types would make a library upgrade a data migration.
- **Tradeoff.** A thin mapping layer must be written and kept current; a new SDK feature
  is unavailable until the interface exposes it.
- **Serves.** [DH-O-10](../design.md#dh-o-10); consistent with DH-ADR-2.
- **Verified by.** `DH-T-MCP-9`: a build check that only the one package imports the
  module; `DH-T-MCP-3` for the error mapping.

<a id="dh-adr-mcp-3"></a>
### DH-ADR-MCP-3 The host starts the stdio child and gives the SDK only its pipes

- **Decision.** For a stdio server the harness process service starts the child and
  passes the child's stdout and stdin to the SDK as an `IOTransport`. The SDK's
  `CommandTransport` is not used. This refines DH-ADR-WIRE-8, which says the host
  "hands" the process to the SDK: what is handed over is the byte stream, not the
  process. Verdict on upstream letting the SDK spawn: **change**.
- **Why.** `CommandTransport.Close` closes stdin, waits, then signals and kills only the
  direct child (`mcp/cmd.go`). A server started through a wrapper such as `npx` leaves
  its real process behind. Inference: on Windows the terminate signal is not available,
  so that path goes straight to killing the one process. The harness already needs a
  process service that owns a process group or job object, applies the sandbox and
  scrubs the environment (DH-ADR-4); with pipes only, there is one owner and one
  shutdown path. It also guarantees one process per connection (debt 3).
- **Tradeoff.** The harness, not the SDK, implements the shutdown order the protocol
  recommends: close the child's stdin, wait, terminate the tree, kill the tree.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-11](../design.md#dh-o-11); consistent with DH-ADR-4.
- **Verified by.** `DH-T-MCP-1` and `DH-T-MCP-2`.

## 6. Design notes

**Owning package.** One package in the harness module, proposed name
`internal/mcpconn` (the owning area confirms the name). It is the only importer of the
SDK. Shape of what it offers, as a sketch and not final signatures:

```go
// Dial connects once. It never retries; reconnecting is the caller's policy.
func Dial(ctx context.Context, spec Spec) (Session, error)

type Spec struct {
    Stdio    *StdioSpec // pipes of a child the process service already started
    HTTP     *HTTPSpec  // endpoint, headers, *http.Client, optional TokenSource
    Revision string     // empty: newest both sides support
}

type Session interface {
    Server() ServerInfo // name, version, revision, instructions, capabilities
    ListTools(ctx context.Context, maxPages int) ([]Tool, error)
    CallTool(ctx context.Context, name string, args json.RawMessage) (Result, error)
    ListResources(ctx context.Context, cursor string) (ResourcePage, error)
    ListResourceTemplates(ctx context.Context, cursor string) (TemplatePage, error)
    ReadResource(ctx context.Context, uri string) (ResourceContents, error)
    OnToolsChanged(func())   // one callback for both revision families
    Done() <-chan struct{}   // closed when the transport ends for any reason
    Close(ctx context.Context) CloseResult // {Confirmed bool; CleanupErr error}
}
```

Errors carry a `Kind` from the closed set in `DH-LIB-MCP-R5`. The mapping rule replaces
upstream's inspection of SDK error classes:

| Observed | Kind | Outcome of the request |
|---|---|---|
| Result with `isError` | tool error | Known: the tool ran and failed. |
| Caller's context cancelled | (the caller's own cancellation, not an MCP error) | Unknown if already sent. |
| Harness deadline passed | timeout | Unknown. |
| `Done()` closed during the call, or the SDK reports a closed connection (`mcp.ErrConnectionClosed`) | connection closed | Unknown. |
| Session already closed before sending | not sent | Known: nothing was sent. |
| Result fails decoding or the output-schema check | invalid result | Known: a result arrived and was refused. |
| A JSON-RPC error object from the server | request failed, with the code | Known. |
| HTTP 401, or an insufficient-scope challenge | authorization required, with a reason | Not sent, or refused. |

Because the harness creates the deadline context and the caller's context itself, it
tells "deadline" from "cancelled" without asking the SDK, which removes one upstream
workaround (`tools.ts:199`).

**Ownership.** Ownership is who releases a resource; access is who may use it.

| Resource | Owner (releases it) | Who may use it |
|---|---|---|
| Child process of a stdio server | The process service, under the fiber of the connection that asked for it | Only that connection's `Session`, through the pipes |
| SDK client session, its goroutines, the subscription stream | The connection fiber, by `Close` | Tool calls and resource reads of agents that can see the server |
| HTTP client and its idle connections | The connection fiber | The session and its OAuth token source |
| OAuth callback route on the host web server | The authorization attempt (an effect of the manager's fiber) | The one browser redirect that carries the right state value |
| Saved grant and client registration | The credential store; removed only by sign-out | Connections of that server entry (read), the refresh path (serialized write) |
| stdin and stdout of the harness when it serves MCP | The serving command | The SDK's stdio transport only |

**Failure, retry and cleanup.**

- *Connect fails half-way.* `Dial` closes whatever it opened and returns the error. For
  stdio the process service still owns the child and stops it. Nothing is registered.
- *Connection drops during a call.* The call returns "connection closed"; its effect on
  the server is unknown and is reported as unknown, never retried by this package.
  Whether to reconnect, with what backoff and attempt budget, is the bridge's policy
  (DH-ADR-WIRE-8 keeps upstream's numbers).
- *Close.* `Close` cancels in-flight calls, closes the SDK session, then for stdio asks
  the process service to finish the child: close stdin, wait, terminate the tree, kill
  the tree. It returns `Confirmed=false` when the transport or the process could not be
  shown to have ended within the caller's deadline. Upstream blocks replacement of a
  connection whose cleanup is unconfirmed (`packages/mcp/mcp-client/README.md:116`); the
  close result gives the bridge what it needs to keep that rule.
- *Tool list changes while a list is in progress.* The callback only signals; the bridge
  serializes re-syncs, as upstream does (`connection.ts:212`).

**Capabilities.** The client is constructed with an explicit empty capability set and no
sampling or elicitation handler, because the SDK's default is to advertise `roots`.

**OAuth.** The connection path gets a harness implementation of the SDK's
`auth.OAuthHandler` whose token source reads the saved grant and refreshes through the
credential store, and whose `Authorize` never starts a flow: it returns "authorization
required" with a reason. The explicit flow, started by a person, uses the SDK's
discovery, registration and exchange code with a fetcher that publishes the
authorization URL to the UI and waits for the callback on the host's own web server.
Two details are unresolved (section 7, items 4 and 5).

**Serving.** The serving command builds an SDK server, adds or replaces tools when the
exposed tool set changes, and runs it on the stdio transport. A consent question is
returned from the tool handler as an input request, so it works for clients on either
revision family. All logging goes to stderr.

**Version policy.** The module is pinned in `go.mod`. An upgrade is a reviewed change
that re-runs the tests in section 8; no range is accepted automatically.

## 7. Risks and open questions

1. **Older stdio servers and the new handshake.** The Go client sends `server/discover`
   before `initialize` and falls back when the server refuses it (`mcp/client.go:320`).
   Inference: a strict older server could close the connection on an unknown first
   request instead of answering with an error. `DH-LIB-MCP-R3` therefore requires a
   per-server revision pin, which makes the client start with `initialize`. The real
   servers used in the area's scenarios should include one older-revision server.
2. **Task-required tools cannot be detected.** The Go `Tool` type drops the `execution`
   field. Upstream refuses such a tool locally (`tools.ts:394`); the Go bridge would
   call it and show the server's own error. Options: accept this (recommended; no
   server in upstream's fixtures needs tasks), or read the raw list result in a
   receiving middleware. The owning area decides.
3. **Unknown content types.** One unknown block fails the whole result as "invalid
   result", where upstream substitutes a placeholder. This is stricter and safe, but a
   server using a content type newer than the SDK loses its whole result until the
   module is upgraded.
4. **OAuth: saving a dynamic client registration.** Upstream stores the registration and
   reuses it for later authorizations (`oauth.ts:256` to `:272`). The Go handler accepts
   a previously registered client as configuration, but whether it exposes a *new*
   registration result so the harness can save it was not confirmed. If it does not,
   the explicit flow calls the lower-level `oauthex.RegisterClient` itself. Needs a
   spike in the owning area before the OAuth deliverable is sized.
5. **OAuth: HTTP 403.** The SDK's contract calls `Authorize` on 401 **and** 403
   (`auth/client.go`). Upstream states that an ordinary 403 does not change
   authorization state (`packages/mcp/mcp-client/README.md:123`). The harness handler
   must therefore inspect the challenge and treat only an insufficient-scope challenge
   as "authorization required".
6. **List cache under revision 2026-07-28.** The Go client caches list results when the
   server allows it (`mcp/client.go:1263`). Upstream forces a refresh on every sync
   (`tools.ts:155`). Whether a tool-list-changed notice always invalidates the Go cache
   was not confirmed; `DH-T-MCP-4` exists to find out.
7. **Tools with invalid names are dropped by the SDK** after a log line
   (`mcp/client.go:1274`). The bridge should surface the count so that a missing tool
   is explainable.
8. **Version 2 of the Go SDK** will break the API (`docs/rough_edges.md`). Contained by
   DH-ADR-MCP-2.
9. **Mixed licence.** The module is in transition from MIT to Apache-2.0; the notices
   file must carry both texts. Licences of transitive modules still need reading.
10. **Claude Code subagent.** `@modelcontextprotocol/sdk` 1.31.0 exists only as a peer of
    the Claude Agent SDK. Whether the Go design drives Claude Code through any SDK is a
    question for the external-agent library specification, not this one.
11. **For the owning areas.** The MCP area (`areas/mcp-lsp-web-connector.md`) should own
    a deliverable for the `internal/mcpconn` package and for the OAuth spike; the
    Computer Use area (`areas/x-cua-browser-voice.md`) should own the serving command.
    A real-environment scenario is theirs to declare; a suggested one is in section 8.

## 8. Verification

Local tests. Nothing here has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-MCP-1` | local | `DH-LIB-MCP-R1`, `DH-LIB-MCP-R3` | A Go fixture server built as a child records each start in a file. Connect, list, close. Assert one start, the negotiated revision, and no child left after `Close`. Repeat with a fixture limited to an older revision and with a pinned revision. |
| `DH-T-MCP-2` | local | `DH-LIB-MCP-R1` | The fixture ignores stdin close and spawns a grandchild. `Close` returns within its deadline, neither process survives, and a deadline too short to confirm returns `Confirmed=false`. |
| `DH-T-MCP-3` | local | `DH-LIB-MCP-R5` | Table test over an in-memory fixture: tool error, deadline, caller cancel, transport closed mid-call, call after close, malformed result, schema-violating result, JSON-RPC error. Each maps to its kind. A cursor loop stops at the page cap. |
| `DH-T-MCP-4` | local | `DH-LIB-MCP-R6` | The fixture changes its tool list. The callback fires once per change on a newest-revision session and on an older one, and the following list returns the new set (answers risk 6). |
| `DH-T-MCP-5` | local | `DH-LIB-MCP-R4`, `DH-LIB-MCP-R7` | The fixture records the client's declared capabilities: none. Instructions are read. A server without the resources capability yields empty lists and receives no resource request. |
| `DH-T-MCP-6` | local | `DH-LIB-MCP-R2`, `DH-LIB-MCP-R8` | `httptest` servers act as a protected MCP server and an authorization server. Without a grant: "authorization required", no browser URL produced. Explicit flow: wrong state and wrong issuer are refused; a good callback stores a grant. Expired token: one refresh under concurrent calls. Plain 403: state unchanged. Redirects refused. No secret in any error text. |
| `DH-T-MCP-7` | local | `DH-LIB-MCP-R9`, `DH-LIB-MCP-R10` | The serving command runs as a child with an SDK client on its pipes. Every stdout line parses as a JSON-RPC frame while logging is forced on. The tool list changes between two list requests. A consent question is answered through a client on the newest revision and through one on an older revision. |
| `DH-T-MCP-8` | local | `DH-LIB-MCP-R12` | Results with each content type keep order and re-encode to equal JSON; an unknown type yields "invalid result" naming the type. |
| `DH-T-MCP-9` | local | `DH-LIB-MCP-R11`, DH-ADR-MCP-2 | `go list -deps` shows one package importing the module; the test suite runs with no `node` on `PATH`. |
| `DH-T-MCP-10` | local | `DH-LIB-MCP-R13`, DH-ADR-MCP-1 | `CGO_ENABLED=0 go build` of the harness succeeds and `go list -m all` contains one MCP module. |

Suggested real-environment scenario for the owning area (not declared here, because
library specifications declare no scenarios):

- **Given** a built harness on a machine with Node installed, and the published
  reference servers `@modelcontextprotocol/server-everything` and
  `@modelcontextprotocol/server-filesystem` configured as two stdio servers on a
  temporary directory.
- **When** the harness starts, a session calls one tool on each server, the filesystem
  server's process is killed from outside, and a tool is called again after reconnect.
- **Then** tools appear under their `mcp__<serverName>__` names; the call during the
  outage fails as "connection closed" or "not sent"; reconnect follows the configured
  backoff; exactly one server process per entry exists at any time.
- **Cleanup** stop the harness, confirm no server process remains, remove the directory.

## 9. Units dispositioned by this spec

| Unit | Disposition |
|---|---|
| `UP-DEP-@modelcontextprotocol/client` | replaced by `DH-LIB-MCP` |
| `UP-DEP-@modelcontextprotocol/core` | replaced by `DH-LIB-MCP` and DH-ADR-MCP-2 |
| `UP-DEP-@modelcontextprotocol/server` | replaced by `DH-LIB-MCP` |
| `UP-DEP-@modelcontextprotocol/sdk` | not applicable (npm peer dependency, never imported) |
| `UP-DEP-@modelcontextprotocol/node` | not applicable (adapter to Node's `http` module) |
| `UP-DEP-@modelcontextprotocol/server-everything` | replaced by Go fixtures (`DH-T-MCP-9`) |
| `UP-DEP-@modelcontextprotocol/server-filesystem` | replaced by Go fixtures (`DH-T-MCP-9`) |
| `UP-DEBT-found.L3.1` to `UP-DEBT-found.L3.5` | resolved, as section 3 states |
