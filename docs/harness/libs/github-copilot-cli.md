# GitHub Copilot CLI as a model route: Go specification

> **Status: design, not built** (gap-closure plan step 16.5.2). Every vendor fact carries
> its source and date:
>
> - **Help 1.0.115**: `copilot --help`, `copilot --version`, `copilot login --help`,
>   `copilot version --help`, `copilot mcp --help`, `copilot sessions --help` and the help
>   topics `environment`, `input`, `sessions`, `timeline`, `permissions`, `mcp`,
>   `limits`, `acp`, `providers`, `logging`, `network`, `config`, run 2026-11-23 against
>   GitHub Copilot CLI 1.0.115-7 installed at
>   `C:\Program Files\Microsoft VS Code\bin\copilot-shim\copilot.exe` (Windows). No
>   prompt was sent and no configuration or credential file was read.
> - **Vendor doc**: `docs.github.com/en/copilot/reference/copilot-cli-reference/cli-programmatic-reference`,
>   read 2026-11-23; `github.com/microsoft/copilot-sdk` `go/README.md` and the
>   `pkg.go.dev` pages of `github.com/github/copilot-sdk/go` (v1.0.35) and
>   `github.com/microsoft/copilot-sdk/go/v2` (v2.0.1), read 2026-11-23.
> - **Secondary**: the JSONL parser of the third-party crate `ralph-adapters` 2.9.3
>   (`src/copilot_stream.rs` on docs.rs), read 2026-11-23. It is evidence of field names,
>   not a vendor contract.
>
> Statements that are reasoning and not read from these sources open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-COPILOTCLI` |
| **Upstream unit** | none: introduced by owner decision M1-D-PIPE-3 A ([decisions.md](../../stories/harness-DSH-M1/decisions.md) sec. 3) |
| **Vendor version read** | GitHub Copilot CLI 1.0.115-7 (help); vendor documentation as of 2026-11-23 |
| **Kind** | an external program the user installs and signs in to (`copilot login`); the harness ships none of it |
| **Used by** | `llm/provider/copilot` ([DH-D-LLM-12](../areas/llm.md#dh-d-llm-12)) |
| **Go choice** | own code, sharing the locator, version probe, strict line reader, renderer and listing-only MCP server of [claude-code-cli.md](claude-code-cli.md); no Copilot SDK module |
| **Related areas** | [../areas/llm.md](../areas/llm.md) (DH-ADR-LLM-10, DH-D-LLM-12); [../areas/execution.md](../areas/execution.md); [cross-spawn.md](cross-spawn.md) (a `.cmd` client on Windows) |

## 1. What it is and why the harness needs it

The GitHub Copilot CLI is GitHub's coding agent, a command-line program. With
`-p` it runs one prompt non-interactively and exits; with `--output-format json` it
prints one JSON object per line (JSONL) as it works (Help 1.0.115). The user signs in
once with `copilot login`, which stores a token "in the system credential store", or in
a plain file under `~/.copilot/` when there is none (Help 1.0.115, `copilot login
--help`).

Under OWNER-2 the M1 definition of done records Copilot "either way" (decisions.md sec.
1.1 item 4): Claude must pass, Copilot is run and recorded. The route has the same
shape as the Claude route (M1-D-PIPE-3 A, -4 A): one child process per attempt, the
user's own sign-in, a logged client invocation, the client's additions opaque, and no
tool run by the client.

The Copilot CLI differs from Claude Code in three ways that matter here: it has **no
switch that replaces its system prompt**, **no turn limit** outside autopilot, and **no
switch that stops it saving the session** on disk. Each is handled in section 5 and
listed as opaque in section 6.

## 2. What the route uses

| Need | Mechanism | Source |
|---|---|---|
| Find the program | As the Claude route; on Windows an npm install is a `copilot.cmd` shim, started with `Spec.Batch = AllowChecked` (plan 16.5.1). The VS Code shim on this machine is an `.exe` | Help 1.0.115 (install path); `world/subprocess` |
| Version | `copilot --version` prints `GitHub Copilot CLI 1.0.115-7.` and a second line `Run 'copilot update' to check for updates.` `copilot version` (no dashes) makes a network request and is not used | Help 1.0.115 |
| Keep the probed version the one that runs | `--no-auto-update` ("runs the version bundled in the executable you launched rather than a newer previously downloaded one") and `COPILOT_AUTO_UPDATE=false` | Help 1.0.115 (`--help`, `help environment`) |
| One request, non-interactive | `-p <text>`, or piped standard input. Inference: piped standard input alone selects prompt mode (`--fleet` "combine with -i, -p, or piped stdin"; the reference mentions "a piped prompt"); `DH-E-COPILOTCLI-1` confirms it | Help 1.0.115; Vendor doc programmatic reference |
| Streamed output | `--output-format json` ("JSONL, one JSON object per line") and `--stream on` | Help 1.0.115 |
| No built-in tool runs | `--deny-tool <kind>` for `shell`, `write`, `read`, `url`, `memory`: "Deny rules win over every allow, even --allow-all and --yolo"; "In autopilot and -p runs, a tool permission request that would prompt is denied" | Help 1.0.115 (`--help`, `help permissions`) |
| Restrict what the model sees | `--available-tools <tools>` ("Only these tools will be available to the model"), `--excluded-tools`, `--disable-builtin-mcps` (the built-in GitHub MCP server), `--no-ask-user`, `--no-custom-instructions` (no `AGENTS.md`) | Help 1.0.115 |
| Declare harness tools | `--additional-mcp-config @<file>` (a JSON file with `mcpServers`; "augments config from ~/.copilot/mcp-config.json for this session"); `--deny-tool dsh` keeps their calls denied | Help 1.0.115 (`--help`, `help mcp`) |
| Known session id | `--session-id <id>` ("set the UUID for a new session") | Help 1.0.115 |
| Keep the session off the account | `--sync-session=off` ("read-only account history sync for this run only") | Help 1.0.115 |
| Least data leaving the machine | `--disable-client-telemetry` | Help 1.0.115 |
| Usage | `--usage-output-file <file>`: input, output, cache and reasoning tokens per model, also for `-p` runs | Help 1.0.115 |

## 3. Tech debt

None dispositioned: no upstream unit maps to this route. Vendor-side limits are in
section 7.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-COPILOTCLI-R1` | One attempt starts one fresh client process with the argument list of section 5.1, in an empty private working directory, with the environment of section 5.3; the request is written to standard input and standard input is closed. | M1-D-PIPE-3 A; plan 16.5.4 |
| `DH-LIB-COPILOTCLI-R2` | The version probe (`--version --no-auto-update`, same environment) refuses a missing program, an unparsable line or a version below the floor with `llm/client-upgrade-required` before any request text is written. | plan 16.5.5 |
| `DH-LIB-COPILOTCLI-R3` | The client never runs a tool: every built-in kind and the harness's MCP server are denied by rule, and the adapter stops the process tree as soon as the first assistant message that requests a tool is complete. The calls are returned unexecuted. | M1-D-PIPE-4 A; DH-ADR-LLM-10 |
| `DH-LIB-COPILOTCLI-R4` | The JSONL lines are mapped to `vocab.Chunk`s by section 5.2; unknown types are ignored, and a stream the adapter cannot place fails closed. | DH-ADR-LLM-2 |
| `DH-LIB-COPILOTCLI-R5` | A signed-out client settles as one Permanent failure (section 5.4). | DH-ADR-LLM-4 |
| `DH-LIB-COPILOTCLI-R6` | No credential or provider override reaches the client from the harness (section 5.3). | SP-11; DH-ADR-LLM-3 |
| `DH-LIB-COPILOTCLI-R7` | The attempt settles the invocation record of [claude-code-cli.md](claude-code-cli.md) section 6, with the client session id. | M1-D-PIPE-4 A |
| `DH-LIB-COPILOTCLI-R8` | Cancelling stops the whole process tree (the shim, the CLI it runs and the MCP listing server) and ends the stream with `Finish{Aborted}`. | DH-D-EXEC-2 |
| `DH-LIB-COPILOTCLI-R9` | No stderr text, account name, token or profile path reaches a chunk, a failure message or the log. | R-11 |

## 5. Go choice

<a id="dh-adr-copilotcli-1"></a>
### DH-ADR-COPILOTCLI-1 One prompt-mode process per attempt; harness tools denied; stop at the first tool request

- **Decision.** Each attempt runs the installed CLI once in prompt mode with JSONL
  output, every tool kind denied, the harness tools listed by the same listing-only MCP
  server as the Claude route and denied by name, and the request (with the harness
  system prompt as its leading section) on standard input. The adapter stops the tree
  when the first assistant message carrying tool requests is complete, and returns them
  as `ToolCallDelta`s and `Finish{ToolCalls}`; a message with no tool request ends the
  attempt normally.
- **Why.** The CLI has no turn limit for a prompt-mode run and no system-prompt
  replacement (Help 1.0.115 lists neither; the programmatic reference documents no
  system-prompt option). Deny rules are the one control the help says no allow and no
  mode overrides, so no tool can run; stopping at the first tool request keeps the
  client from continuing its own loop on the denial.
- **Tradeoff.** Between the denial and the stop the client may already have sent its
  next model request; that request is the client's, opaque, and may be billed. The
  harness system prompt is the start of the user message, not the system slot, so the
  client's own system prompt also reaches the model. The JSONL event schema is not
  documented by the vendor; the field names below are confirmed only by a recorded
  fixture.
- **Serves.** R1, R3, R4. **Verified by.** `DH-T-COPILOTCLI-1` to `-4`;
  `DH-E-COPILOTCLI-1`.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** prompt mode, deny rules, stop at the first tool request | Documented flags only; the installed program; no new module | No system-prompt replacement; a possible extra client request; undocumented JSONL schema | Help 1.0.115 |
| The Copilot SDK for Go over a stdio connection | Host-defined tools run in a host callback; `SystemMessage` has a `replace` mode; `assistant.message_delta` streaming | v2 "defaults to loading the native runtime into the application process"; stdio needs `COPILOT_CLI_PATH`; a module of 39 imports; the protocol is defined only by the SDK; plan 16.5.4 allows it only "if M1-D-PIPE-3 picks it" | Vendor doc `go/README.md`; `pkg.go.dev` v2.0.1 (2026-11-18, MIT) |
| `copilot --acp` (Agent Client Protocol over stdio) | An open protocol with a Go module ([agentclientprotocol-sdk.md](agentclientprotocol-sdk.md)); permission requests reach the client | No system-prompt control; tools still come through MCP; a second protocol to keep | Help 1.0.115 (`help acp`) |

### 5.1 The argument list

As on the Claude route, variable text is in files or on standard input, never in an
argument, so a `copilot.cmd` shim passes the checked batch rules of plan 16.5.1.

| Argument | Purpose |
|---|---|
| `--no-auto-update` | The probed binary is the one that runs |
| `--output-format json --stream on` | JSONL, streamed |
| `--deny-tool shell --deny-tool write --deny-tool read --deny-tool url --deny-tool memory` | No built-in tool kind can run, whatever settings allow |
| `--disable-builtin-mcps --no-ask-user --no-custom-instructions --disallow-temp-dir` | No GitHub MCP server, no question form, no `AGENTS.md`, no temporary-directory access |
| `--additional-mcp-config @<attempt>/mcp.json --deny-tool dsh` | Only when the request declares tools: the listing server `dsh`, whose calls are denied |
| `--available-tools <the dsh tools>` | Only the harness tools are visible. Inference: an empty value hides every tool; confirmed by `DH-E-COPILOTCLI-1` |
| `--sync-session=off --disable-client-telemetry` | Nothing synced to the account; no client telemetry |
| `--session-id <uuid>` | The harness chooses the client session id |
| `--model <id>`, `--reasoning-effort <level>` | The route's model; effort only when the request sets it |

The working directory is the empty attempt directory (the folder is untrusted, so "A
-p run in an untrusted folder skips its workspace MCP servers and repository hooks",
Help 1.0.115 `help permissions`).

### 5.2 JSONL lines and chunks

Field names are from the secondary source and the SDK's event names; they are pinned
by the fixture `DH-E-COPILOTCLI-1` records and fail closed when absent.

| Line `type` | Chunks |
|---|---|
| `assistant.message_delta` (`data.messageId`, `data.deltaContent`) | `BlockStart` on a new message id, then `TextDelta` |
| `assistant.reasoning_delta` (`data.reasoningId`, `data.deltaContent`) | `BlockStart`, then `ReasoningDelta` |
| `assistant.reasoning` | `BlockEnd` of the reasoning block |
| `assistant.message` (`data.content`, `data.toolRequests[]` with `toolCallId`, `name`, `arguments`) | `BlockEnd` of the text; for each tool request, `BlockStart`, one `ToolCallDelta` with the whole arguments and `BlockEnd`. When `toolRequests` is non-empty the adapter stops the tree and ends with `Finish{ToolCalls}` |
| `assistant.turn_start`, `assistant.turn_end`, `assistant.intent`, `session.*` | No chunk; a second `assistant.turn_start` before the stop is counted as a client-internal request |
| `tool.execution_start` | No chunk: a tool ran in the client. The tree is stopped and the attempt fails (a contract breach) |
| usage (`--usage-output-file`, read after exit; or a usage line if one is recorded) | `UsageChunk` before `Finish` |
| `result` (`exitCode`) with no tool request | `Finish{Stop}` when `exitCode` is 0 and some text arrived; otherwise section 5.4 |

### 5.3 Environment

`BuildEnv` with the secret store, as on the Claude route, plus:

- **Removed by name**: `COPILOT_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN` ("an
  authentication token that takes precedence over previously stored credentials"),
  `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`; the BYOK provider variables
  `COPILOT_PROVIDER_BASE_URL`, `COPILOT_PROVIDER_TYPE`, `COPILOT_PROVIDER_API_KEY`,
  `COPILOT_PROVIDER_API_KEY_COMMAND`, `COPILOT_PROVIDER_BEARER_TOKEN`,
  `COPILOT_PROVIDER_HEADERS` and every other `COPILOT_PROVIDER_*`,
  `COPILOT_PROVIDERS_CONFIG` and `COPILOT_OFFLINE`, because each moves the request to
  another provider (DH-ADR-LLM-3); `COPILOT_MODEL` (the model is `--model`);
  `COPILOT_ALLOW_ALL` and `COPILOT_ASSISTED_APPROVAL` (they approve tools);
  `COPILOT_CLI_VERSION` and `COPILOT_CLI_DIST_DIR` (they change which build runs).
- **Set**: `COPILOT_AUTO_UPDATE=false`, `COPILOT_TELEMETRY_DISABLED=true`.
- **Kept**: `COPILOT_HOME` and `COPILOT_CACHE_HOME` (where the sign-in and the installed
  build live), `GH_HOST`, `COPILOT_GH_HOST`, proxy variables.

All names are from Help 1.0.115 `help environment`.

### 5.4 Signed out, and other failures

The CLI's help says only that a session is refused with no sign-in: over ACP, "With no
sign-in and no model provider of your own, it refuses to open a session" (Help 1.0.115
`help acp`); `-p` "exits ... with a nonzero status if the run is interrupted or the
model returns a completely empty final response" (Vendor doc programmatic reference).
No structured signed-out record is documented. The adapter therefore classifies only
from structured JSONL fields, pinned by the signed-out fixture of `DH-E-COPILOTCLI-1`:

| Signal | Failure |
|---|---|
| A structured error line whose type names authentication, before any assistant line | `llm/sign-in-required` (Permanent) |
| The same after a model request was made (a rejected token) | `llm/auth` (Permanent) |
| A rate-limit error line | `llm/rate-limit` (Transient) |
| A non-zero exit with no line the adapter can place | Permanent, with the exit code; never retried. Until the fixture pins the signed-out line this is how a signed-out run settles, so the check that it is `llm/sign-in-required` stays open |
| `tool.execution_start` | Permanent contract breach (section 5.2) |

Stderr is never parsed (R9). The CLI may retry inside a run; no documented switch turns
that off, so its retries are an opaque capability and are counted when visible.

### 5.5 Version floor and the probe

The floor is **1.0.115**, the version this record was read against: the JSONL schema is
undocumented, so no older version is assumed to match. A newer version is allowed and
logged "not checked at this version". The probe accepts the first line
`^GitHub Copilot CLI (\d+)\.(\d+)\.(\d+)(-\d+)?\.?$` and ignores the update hint line.

## 6. Design notes

**Invocation record** as on the Claude route, with the session id from `--session-id`.

**Opaque capabilities**: the client's whole system prompt (the harness's is a user-message
section), its context handling and compaction, its tool-name form for MCP tools, its
internal retries and any request made between a denial and the stop, and the session it
saves under `$COPILOT_HOME/session-state/<id>/` ("Sessions are kept until you delete
them", Help 1.0.115 `help sessions`).

**Ownership.** The attempt owns the process tree and the attempt directory. The saved
client session belongs to the user's client home: the harness knows its id (logged) but
does not delete it, because it does not own files under the client's home.

## 7. Risks and open questions

- **Saved sessions accumulate.** Each attempt leaves one session directory in the
  user's Copilot home. An owner may prefer a periodic `/session prune`; the harness does
  not do it.
- **Stdin as the prompt source** is an inference; if it fails, `-p` with the text is the
  fallback, limited to 32,767 UTF-16 units (8,191 through a `.cmd` shim) and refused by
  the shim's checks for a `"`, `%` or `!` in the text.
- **The signed-out case** may not be reachable by an empty `COPILOT_HOME` alone, since
  the token is in the system credential store; the runbook records how it was produced.
- **The Copilot SDK** is the option that restores system-prompt replacement and
  host-run tools; choosing it is the owner's (M1-D-PIPE-3 allows "the Copilot CLI or
  SDK").

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-COPILOTCLI-1` | local (re-executed stand-in, also as `copilot.cmd` on Windows) | R1, R2, R6, R7 | The argument list equals section 5.1; the removed names are absent though the host holds them; an old version is refused before input; the `.cmd` stand-in starts by bare name with checked arguments |
| `DH-T-COPILOTCLI-2` | local | R3, R4 | Recorded JSONL (text; reasoning; one tool request followed by more lines the stand-in would print; unknown types) gives the tabled chunks; the stand-in is stopped at the tool request and its later lines are never read; `tool.execution_start` fails the attempt |
| `DH-T-COPILOTCLI-3` | local | R5, R9 | The section 5.4 rows give their code and class; a planted token in stderr never reaches a message |
| `DH-T-COPILOTCLI-4` | local | R8 | Cancel stops the shim, its child and the listing server |
| `DH-E-COPILOTCLI-1` | manual, real signed-in client (plan 16.5.9 runbook) | R1 to R9 | As `DH-E-CLAUDECODECLI-1`, plus: the recorded JSONL of each case is kept (scrubbed) as the fixture that pins section 5.2 and 5.4, and stdin as the prompt source is confirmed |

The adapter-level IDs (`DH-T-LLM-n`) are assigned by the lead at plan 16.5.5. Nothing
here has been run.

## 9. Units dispositioned by this spec

None: no upstream unit maps to this route.
