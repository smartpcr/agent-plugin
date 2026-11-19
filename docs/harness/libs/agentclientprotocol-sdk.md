# @agentclientprotocol/sdk: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Facts about a Go module were read from the module cache on
> 2026-11-18 and are cited as `<module>: <file>:<line>`.

| | |
|---|---|
| **Library ID** | `DH-LIB-ACPSDK` |
| **Upstream unit** | `UP-DEP-@agentclientprotocol/sdk` |
| **Upstream version** | 1.4.0 (`pnpm-lock.yaml:20784`), Apache-2.0 (`THIRD_PARTY_NOTICES.md:34`) |
| **Kind** | runtime in the ACP server and the ACP subagent provider; test support; a declared but unimported dependency of the OAR provider |
| **Used by** | acp, subagent, experimental, test-support; dev dependency of apps |
| **Go choice** | named Go module `github.com/coder/acp-go-sdk` v0.20.0, behind one harness package |
| **Related areas** | the ACP front end and the subagent providers; [../upstream/wire-and-api.md](../upstream/wire-and-api.md) section 6 |

## 1. What it is and why upstream needs it

The Agent Client Protocol (ACP) is an open protocol between two programs. The **client**
is the controller: an editor, a test runner or another agent. The **agent** does the
work. They exchange JSON-RPC 2.0 messages, one JSON object per line, over the agent
process's standard input and output. The client creates a session, sends a prompt and
receives `session/update` notifications while the agent works; the agent may ask the
client `session/request_permission` before it does something sensitive.

`@agentclientprotocol/sdk` is the TypeScript library for that protocol: the message
types, the line framing, request correlation and a small builder to register handlers.
Upstream needs it in **both roles**:

- **Agent role.** `dsh --profile acp` lets a trusted program drive persistent harness
  sessions (`packages/acp/acp/README.md:12`).
- **Client role.** The `acp` subagent provider starts any ACP agent as a child process,
  gives it one task and returns its final answer
  (`packages/subagent/subagent-acp/README.md:12`). A subagent provider is a registered
  way to run a delegated task (`docs/subsystems/subagent.md:341`). Upstream's ACP tests
  use the same client role to drive the built product (`packages/test-support/session-snapshot/src/launcher.ts:16`).

Inference: without the library upstream would hand-write several dozen message types (the
agent entry file alone imports twenty, `packages/acp/acp/src/index.ts:27` to `:46`) and
keep them in step with a protocol that still gains methods.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/acp/acp/src/index.ts:21`, `:374`, `:378` to `:392` | Agent role: bind stdin and stdout, register one handler per method, obtain the handle used to call the client | `agent()`, `ndJsonStream`, `methods.agent.*`, `.onRequest`, `.onNotification`, `.connect`, `connection.client` |
| `packages/acp/acp/src/index.ts:126`, `:158` to `:176` | Send `session/update`; turn a harness approval into `session/request_permission` with two one-shot options, and map `cancelled` or the chosen option back | `conn.notify`, `conn.request`, `methods.client.session.*` |
| `packages/acp/acp/src/index.ts:64` to `:71`; `packages/acp/acp/src/session.ts:69` | Keep detail in wire errors; a plain handler error becomes a generic internal error | `RequestError.invalidParams`, `RequestError.internalError` |
| `packages/acp/acp/src/index.ts:183` to `:192` | Answer `initialize` with protocol version 1 and only the capabilities that are mounted | `PROTOCOL_VERSION`, capability types |
| `packages/acp/acp/src/index.ts:426` to `:436` | Run the same teardown when the client disconnects as when the plugin is disposed | `connection.closed` |
| `packages/acp/acp/src/{codec,content,mcp,model-control,updates}.ts` | Types only: stop reason, content block, MCP server entry, config option, update | type imports |
| `packages/subagent/subagent-acp/src/run.ts:10`, `:420` to `:457` | Client role over the child's pipes; collect `agent_message_chunk` text; answer permission requests by a fixed policy | `client()`, `ndJsonStream`, `.onNotification`, `.onRequest`, `connection.agent` |
| `packages/subagent/subagent-acp/src/run.ts:485` to `:492`, `:565`, `:471` | `initialize` with no client capabilities, `session/new` with the working directory and no MCP servers, one `session/prompt`, best-effort `session/cancel` | `agent.request`, `agent.notify` |
| `packages/test-support/session-snapshot/src/launcher.ts:16`; `packages/subagent/subagent-acp/tests/mock-acp-server.ts:84`; `apps/cli/tests/profiles/acp/tests/acp.e2e.ts:6` | Tests: a real client against the built binary, a scripted fake agent | both roles |
| `packages/experimental/subagent-oar/package.json:46` | No import. Declared only because the embedded OAR library needs it at run time (`pnpm-lock.yaml:29039`) | none |

**What the harness calls, what it starts, how results and permissions return (client
role).** The provider starts `command` with `args` once per task through the shared
subprocess service, with stdin and stdout as pipes and stderr passed through to the
host's stderr (`packages/subagent/subagent-acp/src/run.ts:348`). The result is the text of
the `agent_message_chunk` updates plus the stop reason of the `session/prompt` response,
mapped to the harness vocabulary; any unknown stop reason is an error, never a success
(`:220` to `:242`). A permission request from the child is answered at once by
configuration: `reject` (the default) answers `cancelled`; `allow` selects the first
`allow_once` or `allow_always` option (`:430` to `:451`). No person is asked
(`packages/subagent/subagent-acp/README.md:162`). On disposal the provider closes the
child's stdin, waits 6 s, then terminates and waits for the whole process tree
(`packages/subagent/subagent-acp/src/run.ts:82`, `:192` to `:212`).

## 3. Tech debt upstream carries

Search performed: every import of the package in source, tests and scripts; `patches/`
(no patch); `pnpm-workspace.yaml` (no pin, override or exemption); the limitation
sections of both consuming READMEs. No `UP-DEBT` unit is assigned for this dependency.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| none assigned | A fresh process and handshake per delegated task; text-only results; no start-time options | `packages/subagent/subagent-acp/README.md:158` to `:161` | These are provider limits, not library debt. They are judged in the subagent area. Kept here as requirements `DH-LIB-ACPSDK-R3` and `-R4`. |
| none assigned | Stdout must stay free of log lines because any in-process plugin can print to it | `packages/acp/acp/README.md:36` | The host owns its stdout and gives the library a logger (`DH-LIB-ACPSDK-R1`). |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-ACPSDK-R1` | Both roles speak ACP protocol version 1 as one JSON object per line. In the agent role nothing except protocol frames is ever written to stdout; library diagnostics go to the harness logger. | `packages/acp/acp/src/index.ts:374`; `packages/acp/acp/README.md:36` |
| `DH-LIB-ACPSDK-R2` | Agent role: serve `initialize`, `authenticate`, `session/new`, `session/list`, `session/resume`, `session/close`, `session/set_config_option`, `session/prompt`, `session/cancel` and `$/cancel_request`; send `session/update`; ask `session/request_permission`. Every other method answers "method not found" and its capability is not advertised. | `packages/acp/acp/README.md:62` to `:82`, `:99` |
| `DH-LIB-ACPSDK-R3` | Client role: `initialize` advertising no file-system, terminal or elicitation capability; `session/new`; one text `session/prompt`; `session/cancel`. Requests the client does not offer answer "method not found". | `packages/subagent/subagent-acp/src/run.ts:485` to `:492` |
| `DH-LIB-ACPSDK-R4` | Every update that arrived before a prompt response is handled before the prompt call returns, so the collected answer is complete. | `packages/subagent/subagent-acp/src/run.ts:421` to `:429`, `:571` to `:577` |
| `DH-LIB-ACPSDK-R5` | Cancelling the Go context of an outgoing request tells the peer (`$/cancel_request`); an incoming cancel ends the context of the handler. `session/cancel` is separate and stops session work. | `packages/acp/acp/README.md:72` |
| `DH-LIB-ACPSDK-R6` | A handler can return "invalid params" or "internal error" with a detail string that reaches the peer; any other error reaches the peer as a generic internal error with no detail. | `packages/acp/acp/src/index.ts:63` to `:71` |
| `DH-LIB-ACPSDK-R7` | Loss of the connection is observable, fails every pending request, and starts the same teardown as an explicit close, exactly once. | `packages/acp/acp/src/index.ts:426` to `:436`; `packages/acp/acp/README.md:118` |
| `DH-LIB-ACPSDK-R8` | No type of the chosen library appears outside one harness package. Replacing or upgrading the library changes that package and nothing else. | [DH-ADR-WIRE-7](../upstream/wire-and-api.md#dh-adr-wire-7); section 5 |
| `DH-LIB-ACPSDK-R9` | Stop reasons map both ways through closed tables; an unknown value from a peer is an error, never "completed". | `packages/subagent/subagent-acp/src/run.ts:220` to `:242`; `packages/acp/acp/src/codec.ts:6` |
| `DH-LIB-ACPSDK-R10` | Both roles can be connected to each other in one test process over in-memory pipes, and to a child process over its pipes. | `packages/acp/acp/src/index.ts:82`; `packages/test-support/session-snapshot/src/launcher.ts:118` |

## 5. Go choice

<a id="dh-adr-acpsdk-1"></a>
### DH-ADR-ACPSDK-1 One ACP module, both roles, pinned exactly, behind one harness package

- **Decision.** Use `github.com/coder/acp-go-sdk` at exactly v0.20.0 for framing,
  correlation and message types in the agent role and in the client role. Only the
  harness package `acpwire` (name illustrative) imports it. This confirms and completes
  [DH-ADR-WIRE-7](../upstream/wire-and-api.md#dh-adr-wire-7), which named the module but
  left the library record to this specification.
- **Why.** The harness needs both ACP roles (section 1). The module has both
  (`github.com/coder/acp-go-sdk: agent.go:32`, `client.go:16`), generated from the
  protocol's own schema (`schema/version` holds 1.32.0; protocol version 1,
  `constants_gen.go:6`), has no dependencies and no cgo (`go.mod` holds only
  `go 1.21`), and its licence matches upstream's library. It covers every method in
  `DH-LIB-ACPSDK-R2`: `session/close`, `session/list` and `session/resume` are stable
  constants (`constants_gen.go:23`, `:26`, `:31`), and `$/cancel_request` is handled by
  the connection (`constants_gen.go:58`, `connection.go:627`).
- **Tradeoff.** The module is pre-1.0 and says it will stay so: "The SDK stays on v0.
  Raise the minor version for a schema bump or an API break" (`RELEASING.md`). v0.19.0
  and v0.20.0 were both published on 2026-11-18, and v0.20.0 ships a migration note
  whose changes include some "the compiler does not catch" (`MIGRATION_v0.20.md`). So an
  upgrade can change behaviour silently. The wrapper and the fixture test
  (`DH-T-ACPSDK-5`) are the price of that. Its interfaces are also wider than the
  harness's surface: `acp.Agent` requires `Logout`, `DeleteSession` and
  `SetSessionMode`, and `acp.Client` requires file, terminal and elicitation methods
  (`types_gen.go:15349`, `:15594`). The wrapper answers those with
  `acp.NewMethodNotFound` (`errors.go:58`) and never advertises them.
- **Serves.** DH-ADR-1 (ACP and the ACP provider are in scope), DH-ADR-4 (an external
  agent is a separate process the host owns).
- **Verified by.** `DH-T-ACPSDK-1` to `DH-T-ACPSDK-5` locally; `DH-E-ACPSDK-1` and
  `DH-E-ACPSDK-2` against real programs.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `github.com/coder/acp-go-sdk` | Both roles; generated from the ACP schema; no dependencies; ordering guarantee that gives `DH-LIB-ACPSDK-R4` (comment above `SendRequest`, `connection.go:740`: a request returns after the handlers of all earlier notifications) | v0, frequent breaking releases; wide interfaces; a fixed 10 MiB line limit (`connection.go:425`) | v0.20.0, published 2026-11-18T14:15:46Z, Apache-2.0, `go 1.21`, no `require` lines, no `import "C"`. Checked with `go list -m -json`, `go mod download -json`, and by reading `LICENSE`, `go.mod`, `README.md`, `RELEASING.md` and the source in the module cache, 2026-11-18 |
| `github.com/ironpark/acp-go` | Higher-level: router, session store, middleware, an HTTP transport | A framework that wants to own sessions, which the harness already owns; needs `go 1.27.0` and pulls `github.com/coder/websocket`; younger | v0.6.0, published 2026-11-16, MIT, no cgo. Same method, 2026-11-18 |
| Own code: the NDJSON JSON-RPC codec of DH-ADR-WIRE-5 plus hand-written types for the messages in `DH-LIB-ACPSDK-R2` and `-R3` | No third-party churn; exactly the surface used | Several dozen types to write and keep in step with the schema by hand; our own conformance burden | Not a module. This is the fallback named in DH-ADR-WIRE-7 |
| `github.com/agentclientprotocol/go-sdk` | Would be the protocol project's own | Does not exist: `go list -m` reports "Repository not found", 2026-11-18 | none |

**Roles the harness needs.** Both. Agent side: the `dsh acp` front end. Client side: the
ACP subagent provider, and the test client that drives the built binary.

## 6. Design notes

**Shape.** The wrapper exposes harness-owned types only. Illustrative:

```go
// Agent role: the harness implements Agent; Serve returns the handle used to reach the client.
type Agent interface {
    Initialize(ctx context.Context, p InitializeParams) (InitializeResult, error)
    NewSession(ctx context.Context, p NewSessionParams) (NewSessionResult, error)
    Prompt(ctx context.Context, p PromptParams) (StopReason, error)
    Cancel(sessionID string)
    // list, resume, close, set_config_option
}
type ClientPeer interface {
    Update(ctx context.Context, u SessionUpdate) error
    RequestPermission(ctx context.Context, r PermissionRequest) (PermissionOutcome, error)
}
func Serve(ctx context.Context, in io.Reader, out io.Writer, a Agent, log *slog.Logger) (ClientPeer, <-chan struct{})

// Client role: the provider implements Client; Connect returns the handle used to reach the agent.
type Client interface {
    OnUpdate(u SessionUpdate)
    OnPermission(ctx context.Context, r PermissionRequest) PermissionOutcome
}
func Connect(ctx context.Context, in io.Reader, out io.Writer, c Client, log *slog.Logger) (AgentPeer, <-chan struct{})
```

**Ownership.** The wrapper owns no process and no file. It is given a reader and a
writer. In the agent role the ACP front end's fiber owns the connection and stdin and
stdout. In the client role the fiber of the provider that started the child owns the
child process, through the harness subprocess service, and therefore its pipes. Ownership
(who stops the child) is the provider's; the parent agent has access only to the result
and to cancellation.

**Failure, retry, cleanup.**

- *Peer disappears.* The returned channel closes; pending calls fail with one error;
  the owner runs its teardown once (`DH-LIB-ACPSDK-R7`). Nothing is retried: a prompt
  that was cut off may already have changed files.
- *Oversized frame.* The module reads lines with a 10 MiB limit
  (`connection.go:425`); a longer line ends the connection. Inference: an inline image
  in `session/prompt` larger than about 7 MiB before base64 would hit it. The agent
  wrapper states this limit in its documentation and the ACP area decides whether to
  refuse large images earlier with a clear error.
- *Slow peer.* The module writes each frame directly under a lock
  (`connection.go:713`), so a peer that stops reading blocks the sender. The wrapper is
  handed the writer, so the agent role can give it the bounded queue and deadline that
  DH-ADR-WIRE-5 defines for the SDK protocol.
- *Module logging.* The module logs to `slog.Default()` unless told otherwise
  (`connection.go:183`). The wrapper always calls `SetLogger`, so a default logger
  that someone points at stdout cannot corrupt the protocol stream.
- *Half-finished session setup.* Not the library's concern; the ACP area keeps
  upstream's rule that a failed `session/new` leaves nothing published
  (`packages/acp/acp/README.md:82`).

**Upstream decisions judged.**

| Upstream decision | Verdict | Reason |
|---|---|---|
| Use a protocol library instead of hand-written messages | Keep | Same reasoning in Go; `DH-ADR-ACPSDK-1`. |
| Advertise no optional client capability to a child agent, so it uses its own files and terminal | Keep | The child is a separate product in its own process; `DH-LIB-ACPSDK-R3`. |
| Answer a child's permission request by fixed policy, default reject | Keep as the default | Fails closed with nobody watching. Inference: with the interaction registry of DH-ADR-WIRE-4 a third policy, "ask the person", becomes possible; that is a product change for the subagent area to decide (section 7). |
| Import library types throughout the ACP package | Change | A v0 library must be contained; `DH-LIB-ACPSDK-R8`. |
| A dedicated test launcher package built on the library's client | Change | The client wrapper is the test client; `DH-LIB-ACPSDK-R10`. |

## 7. Risks and open questions

- **Release age.** The pinned version was hours old when checked. Go's checksum
  database makes it immutable but gives no cooling-off period, which is the protection
  upstream's package manager enforces (`pnpm-workspace.yaml:57`). The release plan
  should say whether new module versions wait before adoption. For the toolchain or
  release area.
- **For the ACP area:** it owns the `acpwire` package and decides the image-size rule
  and the slow-client policy above.
- **For the subagent area:** whether to add an "ask the person" permission policy for
  external ACP agents, and whether a child agent's process runs inside the operating
  system sandbox.
- **Other external agents.** Any product that offers an ACP mode, or has an ACP adapter,
  is reachable through this provider by configuration alone. That is the fallback path
  for products without their own provider ([botiverse-oar.md](botiverse-oar.md)).
- **Unstable methods.** The module also generates the protocol's unstable methods. The
  wrapper must not expose them until a requirement asks for one.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-ACPSDK-1` | local | `DH-LIB-ACPSDK-R1`, `-R2`, `-R3`, `-R10` | The agent wrapper and the client wrapper are joined by in-memory pipes; a full sequence (initialize, new, set option, prompt with updates and a permission request, cancel, close, list, resume) completes, and every line captured on the agent's output is one JSON-RPC frame. |
| `DH-T-ACPSDK-2` | local | `DH-LIB-ACPSDK-R2`, `-R3`, `-R6` | Each method the harness does not offer (load, delete, set mode, logout; file, terminal and elicitation requests) answers code -32601 and is absent from the advertised capabilities; "invalid params" keeps its detail; a plain error arrives without detail. |
| `DH-T-ACPSDK-3` | local | `DH-LIB-ACPSDK-R4`, `-R9` | A scripted agent sends many chunks and then the prompt response in one burst; the collected text is complete every time. Each known stop reason maps as tabled; an invented one maps to error. |
| `DH-T-ACPSDK-4` | local | `DH-LIB-ACPSDK-R5`, `-R7` | Cancelling the caller's context produces `$/cancel_request` on the wire and ends the peer handler's context; closing one side closes the channel, fails pending calls and runs the owner's teardown exactly once. |
| `DH-T-ACPSDK-5` | local | `DH-LIB-ACPSDK-R8` | A test lists the importers of the module and fails if any package other than the wrapper imports it. Recorded wire lines for every message the harness uses decode and re-encode to the same JSON, so an upgrade that changes the wire fails here. |
| `DH-E-ACPSDK-1` | real environment | `DH-LIB-ACPSDK-R1`, `-R2`, `-R5`, `-R7` | See below. |
| `DH-E-ACPSDK-2` | real environment | `DH-LIB-ACPSDK-R3`, `-R4`, `-R5`, `-R9` | See below. |

<a id="dh-e-acpsdk-1"></a>
#### DH-E-ACPSDK-1 An independent ACP client drives the built binary

- **Given** the built harness binary, a temporary home directory, a real model API key
  supplied at run time, and an ACP client that is not built on our wrapper (an editor
  with ACP support, or the TypeScript reference client).
- **When** the client initializes, creates a session, prompts, answers one permission
  request, cancels a second prompt, closes the session, is killed during a third prompt,
  and a new client lists and resumes the session.
- **Then** each call returns what `DH-LIB-ACPSDK-R2` states; stdout held only protocol
  frames; after the kill no child process of the harness remains and the session is
  still listed.
- **Cleanup.** Remove the temporary home. If a process remains, record its command line
  and keep the home for inspection.

<a id="dh-e-acpsdk-2"></a>
#### DH-E-ACPSDK-2 The harness delegates a task to a real external ACP agent

- **Given** a host with one third-party ACP agent installed and authenticated by the
  operator, a scratch workspace, and a harness profile whose ACP provider names that
  agent's command with policy `reject`.
- **When** a session delegates "create `note.txt`" (needs permission) and then "answer
  with the word ready" (does not), and a third delegation is cancelled mid-run.
- **Then** the first returns a non-completed result with the fixed permission line and
  no file exists; the second returns exactly the agent's answer; the third returns
  `aborted`; after each, no process started for the run remains.
- **Cleanup.** Delete the scratch workspace. Nothing is installed or logged in by the
  test, so nothing else is undone.

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-@agentclientprotocol/sdk`.
