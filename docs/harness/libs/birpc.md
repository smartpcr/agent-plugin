# birpc: Go specification

> **Status: design, not built.** Short form. Upstream facts were read at the pinned commit
> recorded in [../README.md](../README.md). Statements that are reasoning and not read
> from source open with "Inference". The channel this serves is described in
> [ptc-guest-runtime.md](ptc-guest-runtime.md).
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The owner decided
> that no guest runtime is built, so the control pipe to a program child process no
> longer exists. This document now specifies the channel that carries a script's tool
> calls instead: the one between the `dsh tool` command and the host.

| | |
|---|---|
| **Library ID** | `DH-LIB-BIRPC` |
| **Upstream unit** | `UP-DEP-birpc` |
| **Upstream version** | 4.2.0 |
| **Kind** | runtime, also bundled into the child process |
| **Used by** | `ptc-runtime` (`ptc-runtime-node`) |
| **Go choice** | own code: the harness's newline-delimited JSON-RPC codec, used for one request and one reply per connection |
| **Related areas** | A03, which owns the tool endpoint, its request and reply fields and its limits ([../areas/tools.md](../areas/tools.md) §6.2, `DH-D-TOOLS-4`); A24, which owns the `dsh tool` command; A11, programmatic tool calling ([../areas/ptc-workflow.md](../areas/ptc-workflow.md)); the wire foundation ([../upstream/wire-and-api.md](../upstream/wire-and-api.md)) |

## 1. What it is and why upstream needs it

A remote procedure call (RPC) library lets one side call a named function on the other
side of a connection and await the reply. `birpc` is a small one in which **both** sides
can call each other over the same connection.

Upstream uses it for one connection: the control pipe between the host and the child
process that runs a model-written program. The host calls the child ("run this
program", "stop"), and the child calls the host ("invoke this tool", "here is some
output"). The child is untrusted, because the model's code can write anything to that
pipe, so most of what upstream wraps around `birpc` is defence.

In the Go design there is no such child. A model-written script is an ordinary command,
and each tool call it makes is one run of the `dsh tool` command, which connects to the
host's **tool endpoint**, sends one request and reads one reply. Only one side calls:
the host never calls the tool command. The host stops a script through the shell tool's
stop sequence, not through a message. What survives from upstream is the defence,
because the peer is still code the model wrote.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/ptc-runtime/ptc-runtime-node/src/rpc.ts:33-57` | One typed two-way connection over a stream, one JSON object per line | `createBirpc(functions, { timeout: -1, eventNames, on, off, post, serialize, deserialize, resolver })` |
| `rpc.ts:34` | No transport timeouts: deadlines belong to the execution, not to the channel | `timeout: -1` |
| `rpc.ts:35` | Some methods are one-way notifications with no reply | `eventNames` |
| `rpc.ts:39`, `:46-51` | An error crosses as its message only | `serialize`, `deserialize` |
| `rpc.ts:43`, `:62-64` | Bound each incoming line and the bytes queued for sending; breaking a bound closes the connection as a protocol failure | own code around `post` |
| `rpc.ts:54-56` | Only the object's own methods may be called by the peer, and none after the connection closes | `resolver` |
| `rpc.ts:81-86` | End, error and close of the stream reject every pending call once | `$close`, `$closed` |
| `packages/ptc-runtime/ptc-runtime-node/src/protocol.ts:6`, `:46-93` | The methods carried: `ready`, `initialized`, `invoke`, `log`, `image`, `outputLimit` to the host; `run`, `revoke`, `discardOutput` to the child; wire revision 5 | types only |

No patch, pin or exemption names the package (`patches/` and `pnpm-workspace.yaml`
searched). One limitation is admitted: a separately installed child must be upgraded by
hand to the host's wire revision
(`packages/ptc-runtime/ptc-runtime-node/README.md:260`).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-BIRPC-R1` | The tool command and the host exchange one request and one reply per connection, on a channel that is separate from the script's standard input, output and error. Only the tool command sends requests. | `rpc.ts:33-57`; README `:155`; changed by [DH-ADR-PTCGUEST-2](ptc-guest-runtime.md#dh-adr-ptcguest-2) |
| `DH-LIB-BIRPC-R2` | The size of one message is bounded in both directions; exceeding it ends that connection with a protocol failure and never blocks or exhausts the host. The number of open connections per call token is bounded. | `rpc.ts:43`, `:62-64` |
| `DH-LIB-BIRPC-R3` | A request names one operation from a closed list that area A03 defines: `call`, `list` and `describe` ([../areas/tools.md](../areas/tools.md) §6.2). Only `call` executes anything, and it does so by entering the guarded pipeline at its first stage; `list` and `describe` return what the prompt already says and run no tool. An unknown operation or an unknown field fails the request with an error reply; nothing is dispatched after the connection closes. No operation names a session, an agent, a preset or an interaction. | `rpc.ts:54-56`; [DH-ADR-8](../design.md#dh-adr-8) |
| `DH-LIB-BIRPC-R4` | When a connection ends before the reply, the pending call fails exactly once on each side: the host cancels the sub-call and logs it, and the tool command exits with a protocol or unavailable outcome. | `rpc.ts:26-31`, `:81-86` |
| `DH-LIB-BIRPC-R5` | The channel imposes no time limit of its own; cancellation and deadlines come from the outer call's context. | `rpc.ts:34` |
| `DH-LIB-BIRPC-R6` | The request states its wire revision; a mismatch is refused before the token is used or anything is dispatched, with an error that names both revisions. | `protocol.ts:6`, `:51` |

## 5. Go choice

**Decision.** Own code: the codec that
[DH-ADR-WIRE-5](../upstream/wire-and-api.md#dh-adr-wire-5) already defines for every
stdio surface (JSON-RPC 2.0, one message per line, on `bufio` and `encoding/json`), used
here on the connection between the `dsh tool` command and the host's tool endpoint. The
codec is one package, `sdk/jsonrpc` in the module `src/sdk`, standard library only
([../areas/sdk-acp-typert.md](../areas/sdk-acp-typert.md)); it learns an error's
`<domain>/<reason>` through a `Reason() string` method on the error, so it does not
import the harness. No RPC library is added. Verdict on upstream: **keep** every defensive rule; **drop** the
library and the two-way shape.

**Why.** The harness needs a line-delimited JSON channel for the SDK and for plugin
processes anyway. A third such channel should use the same codec, the same error table
and the same tests. Inference: the tool command is normally the same `dsh` binary as the
host, so `DH-LIB-BIRPC-R6` rarely fires; it exists because a script calls `dsh` by name
and could find another installation on its search path.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **The harness codec** (chosen) | One codec and one mental model for every pipe; bounds and allow-list are already its requirements | Own tests | Standard library only |
| `net/rpc` with `net/rpc/jsonrpc` (standard library) | No own code | JSON-RPC 1.0, no context cancellation; frozen | Standard library; `net/rpc` is frozen |
| Reuse the host's HTTP surface for the tool command | No second listener | The browser-facing surface has cookie and origin rules a command-line caller does not fit, and a confined script would need network access to it | Not examined |

**Tradeoff.** A connection per sub-call, where upstream keeps one pipe open for a whole
program. Large tool results are encoded as JSON text twice (tool to host, host to tool
command). Upstream has the same cost and bounds it at 128 MiB per message
(`packages/ptc-runtime/ptc-runtime-node/src/config.ts:49`).

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-BIRPC-1` | local | `DH-LIB-BIRPC-R1` to `-R6` | Against a test endpoint with a hostile peer: a valid request gets one reply and the connection closes; an oversized line, a line that is not a JSON object, an unknown method and a second request on the same connection each produce the stated outcome and leave other connections working; closing with a call pending cancels it once on the host; a wrong revision is refused before the token is looked up; more connections than the bound for one token are refused. Run under the race detector. |

Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-birpc`.
