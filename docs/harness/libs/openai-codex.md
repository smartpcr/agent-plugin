# @openai/codex: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-CODEX` |
| **Upstream unit** | `UP-DEP-@openai/codex` |
| **Upstream version** | 0.153.4, plus six per-platform executables published under the same package name with a platform suffix in the version (`pnpm-lock.yaml:30325` to `:30332`); Apache-2.0 (`THIRD_PARTY_NOTICES.md:59`) |
| **Kind** | runtime; a native executable delivered through npm. No JavaScript API of the package is imported |
| **Used by** | subagent (`packages/subagent/subagent-codex`) |
| **Go choice** | own code: a driver for the `codex app-server` of the Codex program the user has installed. No vendor binary in our release |
| **Related areas** | the subagent providers, the subprocess service, the JSON-RPC codec of DH-ADR-WIRE-5 |

## 1. What it is and why upstream needs it

Codex is OpenAI's coding agent, a native command-line program. The npm package
`@openai/codex` is a delivery vehicle: a small JavaScript wrapper that picks the
executable for the current platform from an optional dependency and runs it
(`.agents/notes/archived/feature/2026-08-04-claude-code-and-codex-subagent-backends.md:38`).

Upstream uses it for the `codex` **subagent provider**: a registered way to run a
delegated task (`docs/subsystems/subagent.md:341`). Each delegation runs one unattended
task in a fresh Codex conversation in the parent's workspace and returns the answer or a
safe failure line (`packages/subagent/subagent-codex/README.md:12`). Codex's own
configuration and login stay in charge.

The important fact for the Go design: upstream does not call any library from this
package. It locates the wrapper script inside the package and starts
`node <wrapper> app-server --stdio` (`packages/subagent/subagent-codex/src/run.ts:44` to
`:53`, `:135` to `:137`). The **app-server** is a mode of Codex that speaks JSON-RPC, one
message per line, on stdin and stdout. Upstream wrote its own client for it on top of
the line transport it already had for its SDK protocol
(`packages/subagent/subagent-codex/src/wire.ts:13`). So the dependency is only "a Codex
executable of a known version".

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/subagent/subagent-codex/src/run.ts:44` to `:53`, `:135` | Find the wrapper script in the installed package; never look at `PATH` | `require.resolve('@openai/codex/package.json')`, its `bin.codex` field |
| `packages/subagent/subagent-codex/src/run.ts:236` to `:242`, `:256` to `:271` | Start it through the shared subprocess service with all three streams as pipes; copy its stderr to the host's stderr | process only |
| `packages/subagent/subagent-codex/src/wire.ts:268` to `:282` | Handshake | request `initialize`, notification `initialized` |
| `packages/subagent/subagent-codex/src/wire.ts:289` to `:302`, `:32` to `:43` | Create a private conversation that is not saved, with the model and the approval and sandbox fields for the configured mode | request `thread/start` with `cwd`, `ephemeral: true`, `model`, `approvalPolicy`, `approvalsReviewer`, `sandbox` |
| `packages/subagent/subagent-codex/src/wire.ts:311` to `:375` | Send the task and wait for the end of the turn | request `turn/start`; notifications `turn/started`, `item/completed`, `turn/completed` |
| `packages/subagent/subagent-codex/src/wire.ts:574` to `:633` | Answer the server's own requests without a person | `item/commandExecution/requestApproval`, `item/fileChange/requestApproval`, `item/permissions/requestApproval`, `item/tool/requestUserInput`, `mcpServer/elicitation/request` |
| `packages/subagent/subagent-codex/src/wire.ts:381` to `:387` | Ask Codex to stop the turn | request `turn/interrupt` |
| `packages/subagent/subagent-codex/tests/real-product.spec.ts:222` to `:246` | Tests: read the version and have Codex write its own protocol schema, then check the fields the provider relies on | `codex --version`, `codex app-server generate-json-schema --out <dir>` |

**What the harness calls, what it starts, how results and permissions return.** One
process per task. The run is handed to its owner only after `thread/start` returned a
conversation id marked as not saved (`src/wire.ts:296` to `:301`). The answer is chosen
from completed assistant messages by their `phase`: the latest `final_answer`, else the
latest `partial_answer`, else the latest message with no phase; `commentary` is never
used and any other phase fails the run (`src/wire.ts:667` to `:682`, `:395` to `:400`).
Permissions are set by mode: `never` (the default) never asks; `approve-for-me` lets
Codex's own automatic reviewer decide inside a workspace-write sandbox; the third mode
turns approval and sandbox off and must be written out in full
(`packages/subagent/subagent-codex/README.md:48` to `:52`). If Codex still asks, the
provider answers `cancel` or `decline`, grants nothing, and fails the run on any request
it does not know (`src/wire.ts:59` to `:67`, `:625`).

## 3. Tech debt upstream carries

Search performed: every reference to the package; `patches/` (no patch for it);
`pnpm-workspace.yaml`; the provider README's limitations; the design note for the two
product providers.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-pin.minimumReleaseAgeExclude.@openai/codex@0.153.4__0.153.4-darwin-arm64__0.153.4-darwin-x64__0.153...` | One release-age exemption covering the package and its six platform executables. They share one entry because "all platform aliases resolve to the @openai/codex package name". The exemption exists because upstream installs a just-published vendor release through npm and its package manager refuses young packages. | `pnpm-workspace.yaml:83`, `:94` to `:96` | Resolved by `DH-ADR-CODEX-1`: the Go build downloads and ships no Codex executable, so no package-manager policy applies. Which Codex version runs becomes a run-time check (`DH-LIB-CODEX-R2`). |

Related debt with the same cause and no unit of its own:

- About 114 MB packed and 282 MB unpacked per platform
  (`packages/subagent/subagent-codex/README.md:185`).
- Node is needed only to run the wrapper script (`src/run.ts:136`).
- "Compatibility is pinned by development evidence": every upgrade means regenerating
  schema evidence and re-running credentialed tests
  (`packages/subagent/subagent-codex/README.md:171`). Yet the client already tolerates a
  version range: it accepts a `partial_answer` phase that its own comment dates to
  "app-server 0.162+", newer than the pinned 0.153.4 (`src/wire.ts:673` to `:677`).
  Inference: the pin protects the build, not the protocol handling, which is written to
  fail closed on what it does not recognise.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-CODEX-R1` | A delegated task starts one fresh `codex app-server` process in the child working directory, from the executable the operator configured or the one found on `PATH`, with the scrubbed host environment plus the provider's overlay. Its stderr goes to the host log and nowhere else. | `packages/subagent/subagent-codex/src/run.ts:236` to `:271`; changed by `DH-ADR-CODEX-1` |
| `DH-LIB-CODEX-R2` | Before the first run the provider reads the Codex version using the same environment as a run. A missing executable or a version below the stated minimum is a fixed failure category reported before any task text is sent; a version above the tested range is logged and allowed. | section 3; `patches/@botiverse__oar@0.67.0.patch:139` to `:160` |
| `DH-LIB-CODEX-R3` | The run is handed to its owner only after the handshake and after Codex returned a conversation that is marked as not saved. The model is sent only when configured. | `packages/subagent/subagent-codex/src/wire.ts:268` to `:302` |
| `DH-LIB-CODEX-R4` | Exactly one turn per run. Only messages for this conversation and this turn are used. The answer is chosen by phase in the order final, partial, none; commentary is never chosen; an unknown phase or a completed turn with no answer is an error. | `packages/subagent/subagent-codex/src/wire.ts:311` to `:375`, `:649` to `:683` |
| `DH-LIB-CODEX-R5` | Nothing waits for a person. The permission mode is fixed per provider instance, defaults to `never`, and maps to conversation fields by a closed table. Approval and input requests from Codex are declined; an unknown server request fails the run. | `packages/subagent/subagent-codex/src/wire.ts:32` to `:43`, `:574` to `:633` |
| `DH-LIB-CODEX-R6` | A failed turn is classified into `limit`, `access-policy`, `service`, `transport`, `product-error`, `invalid-result`, `process` or `unknown`; "context window exceeded" becomes the `max-tokens` stop reason with the partial answer kept. | `packages/subagent/subagent-codex/src/wire.ts:114` to `:148`, `:363` to `:365` |
| `DH-LIB-CODEX-R7` | The parent session receives only the chosen answer, or a failure line built from fixed words, the category, an HTTP status number, the exit code or signal, and at most one fixed permission line; at most 4,096 bytes. | `packages/subagent/subagent-codex/src/run.ts:87` to `:104`; `docs/subsystems/subagent.md:282` |
| `DH-LIB-CODEX-R8` | Cancellation asks Codex to interrupt the turn, does not wait for it, and yields `aborted`. Disposal returns only after the whole process tree has exited and may be called more than once. | `packages/subagent/subagent-codex/src/run.ts:306` to `:311`, `:190` to `:217` |
| `DH-LIB-CODEX-R9` | If the handshake or conversation start fails, including a reply without a conversation id, the provider stops the process and waits for it to exit before it reports the failure. | `packages/subagent/subagent-codex/src/run.ts:316` to `:354`; `patches/@botiverse__oar@0.67.0.patch:38` to `:48`, `:56` |

## 5. Go choice

<a id="dh-adr-codex-1"></a>
### DH-ADR-CODEX-1 Drive the user's installed `codex app-server`; bundle nothing

- **Decision.** The Go provider starts the `codex` executable that is already on the
  user's machine with the arguments `app-server --stdio` and speaks the same methods
  upstream uses (section 2) through the harness's own NDJSON JSON-RPC codec
  (DH-ADR-WIRE-5). The harness release contains no Codex executable. The first supported
  version is 0.153.4, the one upstream verified
  (`packages/subagent/subagent-codex/README.md:171`).
- **Why.** Upstream's use of the package is already "start an executable and speak a
  documented protocol to it"; the npm package adds only a way to obtain the executable
  and a Node wrapper to select it. A Go host has neither npm nor Node, and Codex is a
  native program users install by themselves. Upstream's own experimental provider
  already drives the installed program the same way
  (`packages/experimental/subagent-oar/README.md:12`).
- **Tradeoff.** The user must install Codex and log in. The harness no longer controls
  the Codex version, so the protocol can move under us. Mitigations: the version check
  (`DH-LIB-CODEX-R2`); parsing that ignores unknown notifications and fields but fails
  closed on an unknown server request, phase or terminal status (`-R4`, `-R5`); and a
  real-product scenario that also asks the installed Codex for its protocol schema and
  checks the fields we rely on, as upstream's test does (`DH-E-CODEX-1`).
- **Serves.** DH-ADR-1, DH-ADR-2, DH-ADR-4.
- **Verified by.** `DH-T-CODEX-1` to `-4` against a scripted peer; `DH-E-CODEX-1` against
  the real product.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** own client for the installed `codex app-server` | Standard library plus the codec the harness already has; same protocol upstream verified; nothing to redistribute | We own compatibility with the vendor's protocol | Methods and fields read from `packages/subagent/subagent-codex/src/wire.ts`; the schema command from `tests/real-product.spec.ts:228` |
| The one-shot command of Codex with JSON output | Inference: fewer messages to handle | Inference: no channel to answer an approval request, and it is not the interface upstream verified. Not examined further | none |
| The generic ACP provider with an ACP adapter for Codex | No product-specific code | No such adapter was verified in this research; if the user has one it works by configuration | See [agentclientprotocol-sdk.md](agentclientprotocol-sdk.md) |
| A Go SDK for Codex | Typed messages | None from the vendor was found: `github.com/openai/codex-go` reports "Repository not found" (`go list -m`, 2026-11-18) | none |
| Ship the vendor executable inside our release | Known version; works without user setup | Size; a release artefact per platform; the pin and its review process return | `packages/subagent/subagent-codex/README.md:185` |

**What the user must have installed.** Codex 0.153.4 or later, as an executable the
harness can start directly, and a working Codex login or an API key passed in the
provider's `env`. How it was installed does not matter to the harness. Inference: on
Windows an npm-installed `codex.cmd` shim should be replaced by the path of the real
executable in the provider configuration, for the reason given in
[anthropic-ai-claude-agent-sdk.md](anthropic-ai-claude-agent-sdk.md) section 5.

## 6. Design notes

**Shape.** No public API beyond the subagent provider contract. Inside the provider: a
locator and version probe; a client type with exactly five outgoing calls (`initialize`,
`initialized`, `thread/start`, `turn/start`, `turn/interrupt`), a closed table of server
requests it answers, and a notification handler; and the shared one-run lifecycle
described in [anthropic-ai-claude-agent-sdk.md](anthropic-ai-claude-agent-sdk.md)
section 6. Upstream's rule is kept: the client "exposes no generic request surface"
(`packages/subagent/subagent-codex/src/wire.ts:189`), so a new product method is a
reviewed change.

**Ordering detail worth keeping.** Codex can send notifications for a turn before the
reply to `turn/start` names that turn. Upstream buffers them and replays them once the
turn id is known, and fails if two different ids appear
(`packages/subagent/subagent-codex/src/wire.ts:449` to `:482`). Inference: in Go one
reader goroutine that owns all turn state gives the same result without locks.

**Ownership and access.** As for the Claude Code provider: the run owns the process
tree through the subprocess service; the provider component's fiber owns the runs; the
parent agent may read the result and cancel. Codex's own home directory belongs to the
user.

**Failure, retry, cleanup.**

| Moment | What happens | What is left uncertain |
|---|---|---|
| Executable missing or too old | Refused before any task text is sent (`-R2`) | Nothing |
| Handshake or conversation start fails | Stop, wait for exit, reject (`-R9`); a failed stop is reported beside the first failure | A process that would not die; its id is logged |
| Process exits or output ends before the end of the turn | `error`, category `process`, with the exit code; frames already received are processed first | Whether Codex changed files |
| Turn fails | Category from the table (`-R6`); the best answer seen so far is kept as partial output | Same |
| Cancellation | `turn/interrupt` is sent, the result settles as `aborted` at once, disposal stops the tree (`-R8`) | Files already changed stay changed |
| Retry | Never automatic | n/a |

**Upstream decisions judged.**

| Upstream decision | Verdict | Reason |
|---|---|---|
| Use the app-server protocol with a minimal hand-written client | Keep | It is already the Go-shaped design. |
| One process, one unsaved conversation, one turn per task | Keep | Simple ownership; nothing of the product's state to clean up. |
| Default mode `never`; the unsafe mode has a long explicit name | Keep | Fails closed; hard to choose by accident. |
| Resolve the executable from the bundled package, never `PATH` | Change | `DH-ADR-CODEX-1`. |
| Exact-version pin proven by development evidence | Change | Becomes a minimum version, a tested range and a schema check in the scenario. |

## 7. Risks and open questions

- **Protocol drift.** The app-server protocol is the vendor's and has no version number
  the client negotiates; upstream sends `experimentalApi: false`
  (`packages/subagent/subagent-codex/src/wire.ts:276`), which keeps the client on the
  stable subset. The supported range must be re-proven for each new Codex release.
- **For the subagent area:** it owns the provider package and the supported-version
  range, and decides whether the Codex process runs inside the harness sandbox as well
  as Codex's own (Codex needs the network and its home directory).
- **For the wire area:** the Codex client is a second user of the DH-ADR-WIRE-5 codec in
  the client direction; the codec must allow the peer to send requests to us.
- **For the plugin or bundle area:** as for Claude Code, enabling the provider checks
  for the executable instead of installing one.

## 8. Verification

Local tests use a scripted peer on in-memory pipes and, for process behaviour, a stand-in
executable built by the test. They prove our handling, not the vendor's behaviour.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-CODEX-1` | local | `DH-LIB-CODEX-R1`, `-R2`, `-R3`, `DH-LIB-OAR-R3` | Missing executable and old version are refused before any input is written; the version probe sees the provider's `env` overlay; the recorded `thread/start` holds `ephemeral: true`, the mode's fields from the table, and `model` only when configured; a reply that is not marked unsaved fails the start. |
| `DH-T-CODEX-2` | local | `DH-LIB-CODEX-R4`, `-R6`, `-R7` | Scripted turns: each phase combination selects the tabled answer; early notifications before the `turn/start` reply are replayed; another conversation's messages are ignored; each failure kind maps to its category and stop reason; a secret planted in stderr and in an error message never reaches the result. |
| `DH-T-CODEX-3` | local | `DH-LIB-CODEX-R8`, `-R9`, `DH-LIB-OAR-R2`, `DH-LIB-OAR-R4` | A stand-in that fails `initialize`, and one that answers `thread/start` without an id, are both gone before the start error returns. Cancelling while the handshake is pending stops the stand-in and rejects the start as cancelled. A stand-in with a grandchild that ignores a polite stop: cancel sends `turn/interrupt`, the result is `aborted`, dispose returns only when both processes are gone. |
| `DH-T-CODEX-4` | local | `DH-LIB-CODEX-R5`, `DH-LIB-OAR-R4` | Each known approval or input request receives the tabled refusal and leaves one fixed permission line; an unknown server request fails the run instead of leaving it waiting. |
| `DH-E-CODEX-1` | real environment | `DH-LIB-CODEX-R1` to `-R8` | See below. |

<a id="dh-e-codex-1"></a>
#### DH-E-CODEX-1 Delegation to the real installed Codex

- **Given** a test machine for each supported operating system with Codex installed at
  (a) 0.153.4 and (b) the newest release, a Codex login or API key supplied at run time
  through the provider's `env`, a scratch workspace, and the built harness with the
  provider enabled.
- **When**, on that machine: the installed Codex is asked to write its protocol schema to
  a temporary directory; then a harness session delegates a task that answers with a
  given word; a task that must write a file, under `never`; the same under
  `approve-for-me`; and a long task that is cancelled after its first command.
- **Then** the schema contains every method and field named in section 2; the first
  delegation returns exactly the word; the file exists only when the mode and Codex's
  own sandbox allow it, and a refusal carries the fixed permission line; the cancelled
  run returns `aborted` and no process started for it remains; no saved Codex
  conversation was created; nothing from stderr appears in the parent session.
- **Cleanup.** Delete the scratch workspace and the schema directory. The test does not
  install, log in or change Codex settings. If a process remains, record its id and
  command line and keep the workspace.

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-@openai/codex`,
`UP-DEBT-pin.minimumReleaseAgeExclude.@openai/codex@0.153.4__0.153.4-darwin-arm64__0.153.4-darwin-x64__0.153...`.
