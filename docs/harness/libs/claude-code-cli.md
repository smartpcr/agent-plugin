# Claude Code CLI as a model route: Go specification

> **Status: design, not built** (gap-closure plan step 16.5.2). This record covers the
> installed `claude` program used as a **model route** (one request, one streamed
> answer), which is a different use from the subagent delegation of
> [anthropic-ai-claude-agent-sdk.md](anthropic-ai-claude-agent-sdk.md). Every vendor fact
> carries its source and date:
>
> - **Help 2.1.312**: `claude --help`, `claude auth --help`, `claude auth status --help`,
>   `claude mcp --help` and `claude --version`, run 2026-11-23 against Claude Code
>   2.1.312 installed at `%USERPROFILE%\.local\bin\claude.exe` (Windows). No prompt was
>   sent and no configuration or credential file was read.
> - **Vendor doc**: pages of `code.claude.com/docs/en/` read 2026-11-23: `cli-reference`,
>   `headless`, `env-vars`, `errors`, `hooks`, `agent-sdk/typescript`,
>   `agent-sdk/hooks`.
>
> Statements that are reasoning and not read from either source open with
> "Inference". A flag the vendor documents but 2.1.312's help does not list is marked
> "(doc only)"; it is confirmed by the operator's manual check (plan 16.5.9), not here.

| | |
|---|---|
| **Library ID** | `DH-LIB-CLAUDECODECLI` |
| **Upstream unit** | none: the route is introduced by owner decision M1-D-PIPE-3 A ([decisions.md](../../stories/harness-DSH-M1/decisions.md) sec. 3), not by an upstream dependency |
| **Vendor version read** | Claude Code 2.1.312 (help); vendor documentation as of 2026-11-23 |
| **Kind** | an external program the user installs and signs in to; the harness ships none of it |
| **Used by** | `llm/provider/claudecode` ([DH-D-LLM-12](../areas/llm.md#dh-d-llm-12)) |
| **Go choice** | own code: the locator, version probe and strict line reader of [DH-ADR-CLAUDEAGENTSDK-1](anthropic-ai-claude-agent-sdk.md#dh-adr-claudeagentsdk-1), an argument builder, a request renderer and a stream mapper; started through `world/subprocess` |
| **Related areas** | [../areas/llm.md](../areas/llm.md) (DH-ADR-LLM-10, DH-D-LLM-12); [../areas/execution.md](../areas/execution.md) (the process package); [github-copilot-cli.md](github-copilot-cli.md) (the second client route) |

## 1. What it is and why the harness needs it

Claude Code is Anthropic's coding agent, a command-line program. In "print mode" (`-p`)
it takes one request, answers it, prints the answer and exits; with
`--output-format stream-json` it prints one JSON object per line as the answer streams.
The user signs in to it once with their own Claude account (`claude auth login`).

Under the owner's rule OWNER-2 the harness has no API key, and the real-model checks
must run on the user's own signed-in Claude session. Owner decision M1-D-PIPE-3 A says
how: the harness starts the installed `claude` as a child process, gives it one request,
and reads the streamed answer. The client signs in; the harness never sees, reads or
stores a token. M1-D-PIPE-4 A says what the harness guarantees on that route: the unit
that is logged and rebuildable is the **client invocation** (argument list, standard
input, environment names, client version), and whatever the client adds on its own is
declared opaque (section 6).

Two words used below. A **turn** of the client is one model reply inside the client's
own loop; `--max-turns` counts them. A **tool call** is the model asking for a tool to
run; on this route the client must never run one, because harness tools run only in the
harness's guarded pipeline (M1-D-PIPE-4 A).

## 2. What the route uses

| Need | Mechanism | Source |
|---|---|---|
| Find the program | Configured absolute path, else bare `claude` on the child's `PATH` through `subprocess.LookupProgram`; a `.cmd` shim is started only with `Spec.Batch = AllowChecked` (plan 16.5.1) | DH-ADR-CLAUDEAGENTSDK-1 (locator); `world/subprocess` |
| Version | `claude --version` prints one line, `2.1.312 (Claude Code)` | Help 2.1.312 |
| One request, streamed | `-p`; request on standard input ("Non-interactive mode reads stdin"; "Piped stdin is capped at 10MB"); `--output-format stream-json` with `--verbose`; `--include-partial-messages` adds the raw token stream | Help 2.1.312; Vendor doc `headless` |
| Replace the system prompt | `--system-prompt <text>` (help) or `--system-prompt-file <path>` (doc only): "Replace the entire system prompt" | Help 2.1.312; Vendor doc `cli-reference` |
| No built-in tools | `--tools ""`: "Use \"\" to disable all tools". It "doesn't affect MCP tools" | Help 2.1.312; Vendor doc `cli-reference` |
| Declare harness tools | `--strict-mcp-config --mcp-config <file>`: only the MCP servers passed on the command line load | Help 2.1.312; Vendor doc `cli-reference` |
| Never run a tool | `--permission-mode dontAsk` ("denies every call that would otherwise prompt") with `--permission-prompts none` (needs 2.1.259 or later); denials appear as `system/permission_denied` lines and in the result's `permission_denials` | Help 2.1.312; Vendor doc `headless` |
| One turn | `--max-turns 1` (doc only): "Limit the number of agentic turns (print mode only)"; the result subtype is then `error_max_turns` | Vendor doc `cli-reference`, `agent-sdk/typescript` |
| No saved session | `--no-session-persistence` ("only works with --print"); also `CLAUDE_CODE_SKIP_PROMPT_HISTORY=1` | Help 2.1.312; Vendor doc `env-vars` |
| Known session id | `--session-id <uuid>`; every stream line carries `session_id` | Help 2.1.312; Vendor doc `agent-sdk/typescript` |
| No client retries | `CLAUDE_CODE_MAX_RETRIES` (default 10, capped at 15); a retry is announced as a `system/api_retry` line | Vendor doc `agent-sdk/typescript`, `headless` |
| Fixed binary between probe and run | `DISABLE_AUTOUPDATER=1` ("disable automatic background updates") | Vendor doc `env-vars` |
| Least data leaving the machine | `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` (auto-updates, telemetry, error reporting) | Vendor doc `env-vars` |

**Not usable on this route.** `--bare` (and `CLAUDE_CODE_SIMPLE=1`) "never reads OAuth
credentials or the system keychain"; it would make a signed-in route impossible (Vendor
doc `headless`). The vendor says bare mode "will become the default for `-p` in a future
release"; see section 7.

## 3. Tech debt

No upstream unit is dispositioned here. The vendor-side limits the route must live with
are in section 7.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-CLAUDECODECLI-R1` | One attempt starts one fresh `claude -p` process with the argument list of section 5.1, in an empty private working directory, with the environment of section 5.3. The request is written to standard input and standard input is closed. | M1-D-PIPE-3 A; plan 16.5.3 |
| `DH-LIB-CLAUDECODECLI-R2` | Before the first attempt of a prepared call, the version probe reads `claude --version` in the same environment; a missing program, an unparsable line or a version below the floor (section 5.5) fails the call with `llm/client-upgrade-required` (Permanent) before any request text is written. | DH-LIB-CLAUDEAGENTSDK-R2; plan 16.5.5 |
| `DH-LIB-CLAUDECODECLI-R3` | The client never runs a tool. Built-in tools are off, harness tools are declared through a listing-only MCP server, every call to them is denied, and the attempt ends after the client's first turn. A tool call is returned to the harness unexecuted. | M1-D-PIPE-4 A; DH-ADR-LLM-10 |
| `DH-LIB-CLAUDECODECLI-R4` | The stream is mapped to `vocab.Chunk`s by section 5.2. Unknown line types are ignored; a stream that cannot be placed (no `result` line, a `result` that contradicts the chunks, a tool that ran) fails closed. | DH-ADR-LLM-2 |
| `DH-LIB-CLAUDECODECLI-R5` | A signed-out or rejected sign-in settles as one Permanent failure (section 5.4): no retry by the client and none by the harness. | DH-ADR-LLM-4; plan Stage 16.5 Result |
| `DH-LIB-CLAUDECODECLI-R6` | No credential reaches the client from the harness: the variables of section 5.3 are absent from the child environment, and a settings or key source other than the sign-in is detected and fails the attempt. | SP-11; DH-ADR-LLM-3 |
| `DH-LIB-CLAUDECODECLI-R7` | The attempt settles the invocation record of section 6 (argument list and standard-input digests, client version, client session id). | M1-D-PIPE-4 A |
| `DH-LIB-CLAUDECODECLI-R8` | Cancelling the attempt's context stops the client's whole process tree, including the MCP server it started, and the stream ends with `Finish{Aborted}` and the cause. | DH-ADR-LLM-2; DH-D-EXEC-2 |
| `DH-LIB-CLAUDECODECLI-R9` | No stderr text, account name, email, token or path under the user's profile appears in a chunk, a failure message or the log; stderr is read only to a bounded buffer that is discarded. | DH-LIB-CLAUDEAGENTSDK-R6; R-11 |

## 5. Go choice

<a id="dh-adr-claudecodecli-1"></a>
### DH-ADR-CLAUDECODECLI-1 One `claude -p` process per attempt; tools are declared, denied and returned

- **Decision.** Each model attempt on the route runs the installed client once, in
  print mode, with the harness's system prompt replacing the client's, no built-in
  tools, the harness tools listed by a harness-hosted MCP server that only lists them,
  every tool call denied, one client turn, and no saved session. The adapter reads the
  stream and stops at the first turn: a tool call comes back as a `ToolCallDelta` and
  `Finish{ToolCalls}`, and the harness driver runs it through the guarded pipeline as
  any model tool call (origin `model`). The conversation so far is rendered into
  standard input, because print mode cannot be given earlier assistant turns.
- **Why.** Print mode offers no way to hand a tool's schema to the model other than a
  built-in tool or an MCP server, and no switch that says "return tool calls". Denial
  plus a one-turn limit gives exactly that: the client sees the call, asks to run it,
  is refused, and stops before its next model request. The vendor's `defer` decision
  (a `PreToolUse` hook) would also return the call unexecuted, with
  `stop_reason: "tool_deferred"` and `deferred_tool_use`, but it is honoured "only when
  Claude makes a single tool call in the turn" and is built for resuming a saved
  session (Vendor doc `hooks`); denial works for parallel calls too and needs no hook.
- **Tradeoff.** The model sees the harness tools under the client's MCP names
  (`mcp__dsh__<tool>`) and the history as rendered text, not as structured blocks;
  both are client additions declared opaque. `--max-turns` and `--system-prompt-file`
  are documented but hidden from 2.1.312's help, so the first manual check confirms
  them. The client's own loop scaffolding around the one turn is not seen.
- **Serves.** DH-LIB-CLAUDECODECLI-R1, R3, R4. **Verified by.** `DH-T-CLAUDECODECLI-1`
  to `-4`; `DH-E-CLAUDECODECLI-1`.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** deny and stop after one turn, tools listed by a listing-only MCP server | Works with documented flags; a tool can never run in the client; parallel calls work; no hook, no saved session | Hidden flags to confirm; MCP-prefixed tool names; rendered history | Help 2.1.312; Vendor doc `cli-reference`, `headless` |
| `PreToolUse` hook returning `defer` | The vendor's own "pause at a tool call" | Single tool call per turn only; designed around `--resume` of a saved session; needs a hook command per run | Vendor doc `hooks` ("Defer a tool call for later") |
| A full MCP bridge: the client calls the tool and the bridge runs it in the harness pipeline (origin `client`) | Native multi-step behaviour of the client | The client's loop issues model requests the log cannot rebuild; needs a channel back to the host, a new origin and mid-attempt tool results ([DH-ADR-LLM-10](../areas/llm.md#dh-adr-llm-10) sizes it) | n/a |
| The Agent SDK control protocol over `--input-format stream-json` (in-process SDK tools) | Tool calls reach the host as messages | The control messages are documented only through the vendor's TypeScript and Python SDKs; a Go port re-implements them ([anthropic-ai-claude-agent-sdk.md](anthropic-ai-claude-agent-sdk.md), section 5) | n/a |

### 5.1 The argument list

Variable text never goes in the argument list: the system prompt, the tool list and the
MCP configuration are files in the attempt's private directory, so a `.cmd` shim can be
started with checked arguments (none holds `"`, `%` or `!`) and no argument is long.

| Argument | Purpose | In 2.1.312 help? |
|---|---|---|
| `-p` | Print mode; the request is read from standard input | yes |
| `--output-format stream-json --verbose` | One JSON object per line | yes |
| `--include-partial-messages` | Raw token deltas as `stream_event` lines | yes |
| `--system-prompt-file <attempt>/system.txt` | The harness system prompt replaces the client's | doc only (help lists `--system-prompt <prompt>`) |
| `--tools ""` | No built-in tool | yes |
| `--strict-mcp-config --mcp-config <attempt>/mcp.json` | Only the harness's listing server; present only when the request declares tools | yes |
| `--permission-mode dontAsk --permission-prompts none` | Every tool call is denied; no host is asked | yes (both) |
| `--max-turns 1` | Stop after the first client turn | doc only |
| `--no-session-persistence` | Nothing saved for resume | yes |
| `--session-id <uuid>` | The harness chooses the client session id (derived from the attempt id) | yes |
| `--setting-sources local` | With an empty working directory, no user or project settings file is loaded, so no settings `env` block, hook or `apiKeyHelper` applies (Inference: the sign-in is not a setting source; confirmed by `DH-E-CLAUDECODECLI-1`) | yes |
| `--model <id>` | The route's model | yes |
| `--effort <level>` | Only when the request sets a reasoning effort (low, medium, high, xhigh, max) | yes |

The working directory is the empty attempt directory, so no `CLAUDE.md`, `.mcp.json` or
project hook is found. `mcp.json` names one stdio server, `dsh`, whose command is the
harness executable with a listing-only mode and the path of `tools.json` (the canonical
tool schemas of [DH-ADR-LLM-8](../areas/llm.md#dh-adr-llm-8)).

### 5.2 Stream lines and chunks

The line reader is the strict reader of DH-ADR-CLAUDEAGENTSDK-1 (bounded line length,
unknown fields and types ignored). Types are from Vendor doc `agent-sdk/typescript`
(`SDKMessage`).

| Line | Chunks and checks |
|---|---|
| `system`/`init` | No chunk. Records `session_id` (must equal `--session-id`), `claude_code_version` (must equal the probe), `model`, `apiKeySource`, `tools` (must be only `mcp__dsh__*`) and `mcp_servers` (`dsh` must be connected when tools were declared). A key source other than the sign-in fails the attempt (section 5.4) |
| `stream_event` | `event` is a raw Anthropic Messages stream event; the Messages decoder of [DH-D-LLM-2](../areas/llm.md#dh-d-llm-2) maps it: `content_block_start` to `BlockStart`; `text_delta`, `thinking_delta` and `input_json_delta` to `TextDelta`, `ReasoningDelta` and `ToolCallDelta` (name without the `mcp__dsh__` prefix); `content_block_stop` to `BlockEnd`; `message_delta` usage to `UsageChunk`. `ping` events are liveness only |
| `assistant` | No chunk when partial messages are on; the whole message cross-checks the assembled blocks. Its `error` field (`SDKAssistantMessageError`) feeds section 5.4 |
| `user` | No chunk. A tool result here must be a denial of a call listed in `permission_denials`; anything else means a tool ran, and the attempt fails |
| `system`/`permission_denied` | No chunk. Counted; each must name a call of the turn |
| `system`/`api_retry` | No chunk. Counted in the attempt record as a client-internal retry; with `CLAUDE_CODE_MAX_RETRIES=0` none is expected |
| `result` `success` | `Finish{Stop}` (or `MaxTokens` when `stop_reason` is `max_tokens`); `usage` is the attempt's usage |
| `result` `error_max_turns` whose turn's every `tool_use` is in `permission_denials` | `Finish{ToolCalls}` |
| `result` with `is_error: true` otherwise | `Finish{Error}` with the failure of section 5.4 |
| end of stream without `result`, or a non-zero exit without one | `llm/stream-closed` |

### 5.3 Environment

The child environment is `subprocess.BuildEnv(host, ...)` with the secret store as
`Secrets` (plan 14.6.4). `DefaultDeny` already removes every name holding `KEY`,
`TOKEN`, `SECRET`, `PASSWORD`, `PASSWD`, `PASSPHRASE` or `CREDENTIAL`. The route adds:

- **Removed by name** (the plan's four, then the vendor's): `ANTHROPIC_API_KEY`,
  `ANTHROPIC_AUTH_TOKEN`, `GITHUB_TOKEN`, `GH_TOKEN`; `CLAUDE_CODE_OAUTH_TOKEN` and
  `CLAUDE_CODE_OAUTH_REFRESH_TOKEN` ("Takes precedence over keychain-stored
  credentials"); `ANTHROPIC_AWS_API_KEY`, `AWS_BEARER_TOKEN_BEDROCK`,
  `ANTHROPIC_FOUNDRY_API_KEY`, `ANTHROPIC_FOUNDRY_AUTH_TOKEN`, `ANTHROPIC_PROFILE`,
  `CLAUDE_CODE_CLIENT_KEY_PASSPHRASE` (Vendor doc `env-vars`).
- **Removed because they move the request to another endpoint, provider or account**
  (DH-ADR-LLM-3; no deny pattern catches them): `ANTHROPIC_BASE_URL` and every
  `ANTHROPIC_*_BASE_URL`, `ANTHROPIC_FOUNDRY_RESOURCE`, `CLAUDE_CODE_USE_BEDROCK`,
  `CLAUDE_CODE_USE_VERTEX`, `CLAUDE_CODE_USE_FOUNDRY`, `CLAUDE_CODE_USE_MANTLE`,
  `CLAUDE_CODE_USE_ANTHROPIC_AWS`, `CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD`; and the
  model overrides `ANTHROPIC_MODEL`, `ANTHROPIC_DEFAULT_*_MODEL`, because the model is
  the route's `--model` and is logged.
- **Removed because they weaken safety or change the mode**: `CLAUDE_CODE_SIMPLE`
  (bare mode), `CLAUDE_CODE_EVEN_MORE_DANGEROUSLY_SKIP_PERMISSIONS`.
- **Set**: `CLAUDE_CODE_MAX_RETRIES=0`, `CLAUDE_CODE_SKIP_PROMPT_HISTORY=1`,
  `DISABLE_AUTOUPDATER=1`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`.
- **Kept**: `CLAUDE_CONFIG_DIR` (where the sign-in lives), proxy variables
  (`HTTPS_PROXY`, `NO_PROXY`), `PATH`, `SystemRoot`, the home variables.

### 5.4 Signed out, and other failures

Vendor doc `headless`: "Claude Code exits with code 0 on success and a non-zero code
when the run fails ... When a failure happens inside the run, such as missing
authentication, Claude Code prints the failure as the result on stdout." The messages
include `Not logged in · Please run /login`, `Login expired · Please run /login`,
`OAuth token revoked` / `OAuth token has expired` and
`API Error: 401 Invalid authentication credentials` (Vendor doc `errors`). The adapter
classifies from structured fields only, never from message text:

| Structured signal | Failure |
|---|---|
| `assistant.error` is `authentication_failed` or `oauth_org_not_allowed`, and the `result` has no `api_error_status` (no request reached the API: not signed in, expired login) | `llm/sign-in-required` (Permanent) |
| The same with `api_error_status` 401 or 403 (a sign-in the API rejected) | `llm/auth` (Permanent) |
| `system/init` reports a key source other than the sign-in (`apiKeySource` naming an environment key or a helper) | `llm/invalid-credential` (Permanent); the process is stopped. Inference: the exact `apiKeySource` value of a signed-in run is recorded by `DH-E-CLAUDECODECLI-1` and pinned in the mapping test |
| `rate_limit` | `llm/rate-limit` (Transient) |
| `overloaded`, `server_error` | `llm/server` (Transient) |
| `billing_error`; `account_on_hold` | `llm/quota`; `llm/account-quota` (Permanent) |
| `invalid_request`; `model_not_found` | `llm/invalid-request`; `llm/unknown-model` (Permanent) |
| `max_output_tokens` | `Finish{MaxTokens}`, no failure |
| `cloud_credential_error` (impossible after section 5.3) | `llm/auth` (Permanent) |
| `unknown` with a status | `llm/http-<status>`; without one, `llm/server` |
| An argument rejected at start ("unknown option" on stderr with no stream) | `llm/client-upgrade-required` (Permanent); stderr itself is not kept |

A Permanent class means the retry policy appends `llm/retry-abandoned{not-retryable}`
and no other route is tried (DH-ADR-LLM-3, -4). The client's own retries are switched
off (`CLAUDE_CODE_MAX_RETRIES=0`), so "one attempt, one logged settlement" holds at the
client boundary too.

### 5.5 Version floor and the probe

The floor is **2.1.259**, the version the vendor names for `--permission-prompts`, the
newest flag in section 5.1 with a documented minimum. The route was read against
2.1.312 only; a version above the floor and below the newest recorded manual check is
allowed and logged with "not checked at this version". The probe runs
`<claude> --version` with the run's environment, a 10-second deadline and a 4 KiB
output bound through the strict line reader, and accepts exactly
`^(\d+)\.(\d+)\.(\d+) \(Claude Code\)$`. It runs once per prepared call, before any
request text is written; a refusal is `llm/client-upgrade-required` with the found and
required versions. `DISABLE_AUTOUPDATER=1` keeps the binary that was probed the one
that runs, and the `system/init` line's `claude_code_version` is compared with it.

## 6. Design notes

**The invocation record** (settled in the attempt record, M1-D-PIPE-4 A): client name,
probed version, the program's base name, the argument list with the attempt directory
written as `{attempt}`, the SHA-256 digests of the argument list, of standard input and
of each attempt file (`system.txt`, `tools.json`, `mcp.json`), the environment names
(never values), the client session id, the model the client reported, the client's
internal retry and permission-denial counts. Adding these fields is a persisted-format
change of the attempt record (Rule 6), made in plan 16.5.3 with a version bump.

**Opaque capabilities** declared in the route's `RouteInfo`: the client's additions to
the system text and messages (system reminders, tool descriptions of its MCP layer), its
context handling and compaction, its tool-name prefix, its own transcript handling,
and its usage accounting.

**Ownership.** The attempt owns the process tree (the client and the MCP listing server
it starts, both inside the job object or control group of `world/subprocess`), its pipes
and the attempt directory, which is removed when the attempt ends. The client's sign-in,
settings and any file it writes under its own configuration directory belong to the
user and are never read, changed or deleted by the harness.

**Failure and cleanup.**

| Moment | What happens | What is left |
|---|---|---|
| Probe refuses | `llm/client-upgrade-required` before a request is written | Nothing |
| Start refused by the process package (a path holding `%`, `!` or `"` under a `.cmd` shim) | `llm/invalid-request` before anything starts | The attempt directory, removed |
| Cancel | `StopTree` with a 2-second grace; `Finish{Aborted}` with the cause | Nothing; a sent request may be billed |
| A tool ran in the client (a non-denial tool result, or the listing server received `tools/call`) | The tree is stopped; `Finish{Error}`; a defect is logged | Whatever that call did; the listing server performs nothing, so nothing |

## 7. Risks and open questions

- **Hidden flags.** `--max-turns` and `--system-prompt-file` are in the vendor's CLI
  reference but not in 2.1.312's help. If either is refused, the start fails with
  `llm/client-upgrade-required`; the fallback for the system prompt is
  `--system-prompt` with the text, which a `.cmd` shim may refuse.
- **What `--max-turns 1` stops.** Inference: the denied call ends turn 1 and the client
  stops before a second model request. `DH-E-CLAUDECODECLI-1` checks it from `num_turns`
  and from the absence of a second `message_start`.
- **Bare mode as default.** If a later version makes `--bare` the `-p` default, the
  sign-in is not read and every attempt fails `llm/sign-in-required`; the fix is an
  explicit opt-out flag once the vendor offers one.
- **`--setting-sources local`.** Whether managed (organisation) settings still apply is
  the vendor's policy; they are opaque.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-CLAUDECODECLI-1` | local (re-executed test binary as the client) | R1, R2, R6, R7 | The stand-in records its argument list, standard input and environment names: the list equals section 5.1, the removed names are absent though the host holds them, a version below the floor and an unparsable version are refused before the stand-in receives input, and the record's digests equal those rebuilt from the logged request |
| `DH-T-CLAUDECODECLI-2` | local | R3, R4 | Recorded stream lines (text; reasoning and text; one tool call; two parallel tool calls; unknown line types) give the tabled chunks and finish; a tool result that is not a denial fails the attempt |
| `DH-T-CLAUDECODECLI-3` | local | R5, R9 | Each row of section 5.4 gives its code and class; a planted token in stderr and in the result text never reaches a failure message |
| `DH-T-CLAUDECODECLI-4` | local | R8 | A stand-in that starts a grandchild (the MCP listing server's place) and streams slowly: cancel gives `Finish{Aborted}` and both processes are gone |
| `DH-E-CLAUDECODECLI-1` | manual, real signed-in client (plan 16.5.9 runbook) | R1 to R9 | See below |

The adapter-level IDs these become (`DH-T-LLM-n`) are assigned by the lead at plan 16.5.5.

<a id="dh-e-claudecodecli-1"></a>
#### DH-E-CLAUDECODECLI-1 The real signed-in client on the route

- **Given** the operator's workstation with Claude Code signed in as themselves, its
  version recorded; the harness at a recorded commit; no API key in the environment.
- **When** the manual test streams (1) a text-only request, (2) a request that must call
  the one test tool, and (3) the same text request with `CLAUDE_CONFIG_DIR` set to an
  empty temporary directory.
- **Then** (1) settles as one `assistant/message` with usage and `num_turns` 1; (2) ends
  with `Finish{ToolCalls}`, the call's arguments are valid JSON, `permission_denials`
  names it, the listing server received no `tools/call`, and no second `message_start`
  was seen; (3) settles as one `assistant/attempt` with `llm/sign-in-required`, no
  `llm/retry`, and no other route prepared. The hidden flags were accepted, and the
  `apiKeySource` of (1) is recorded.
- **Cleanup.** Delete the temporary directories. Nothing of the user's sign-in or
  settings is touched, so nothing else is undone.

Nothing here has been run.

## 9. Units dispositioned by this spec

None: no upstream unit maps to this route.
