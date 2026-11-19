# @anthropic-ai/claude-agent-sdk: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Statements marked "Vendor doc" were read on 2026-11-18 from the
> vendor's public documentation (`code.claude.com/docs/en/headless` and
> `code.claude.com/docs/en/cli-reference`); they describe the vendor's product, not
> upstream, and must be re-checked against the installed version (`DH-E-CLAUDEAGENTSDK-1`).

| | |
|---|---|
| **Library ID** | `DH-LIB-CLAUDEAGENTSDK` |
| **Upstream unit** | `UP-DEP-@anthropic-ai/claude-agent-sdk` |
| **Upstream version** | 0.3.263 (`packages/subagent/subagent-claude-code/package.json:51`), which selects the Claude Code 2.1.263 executable from one of eight per-platform packages |
| **Kind** | runtime; carries a native executable per platform; licence is the vendor's own terms, not an open-source licence (`THIRD_PARTY_NOTICES.md:35`, `:193`) |
| **Used by** | subagent (`packages/subagent/subagent-claude-code`) |
| **Go choice** | own code: a small driver for the `claude` executable the user has installed. No SDK, and no vendor binary in our release |
| **Related areas** | the subagent providers and the subprocess service |

## 1. What it is and why upstream needs it

Claude Code is Anthropic's coding agent, a command-line program. The Claude Agent SDK is
a TypeScript package that starts that program as a child process and talks to it over
its standard input and output, so that other software can give it a task and read the
result. The SDK package does not contain the agent; each platform's executable arrives
as a separate npm package that the SDK lists as an optional dependency.

Upstream uses the SDK for one feature: the `claude-code` **subagent provider**. A
subagent provider is a registered way to run a delegated task
(`docs/subsystems/subagent.md:341`). When the model calls `subagent_claude_code`, the
provider runs one fresh, unattended Claude Code session in the parent's workspace and
returns its final answer or a safe failure line
(`packages/subagent/subagent-claude-code/README.md:12`). Claude Code's own settings and
login stay in charge; the harness chooses only the model, the environment and the
permission mode.

Upstream ships the executable with the product and "never falls back to the host
`claude` executable" (`packages/subagent/subagent-claude-code/README.md:12`). That choice
is the source of every tech-debt unit in section 3.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/subagent/subagent-claude-code/src/run.ts:441` | Start one session for one text task | `query({ prompt, options })` |
| `packages/subagent/subagent-claude-code/src/run.ts:325` to `:376` | Per-run options: working directory, optional model, scrubbed environment plus overlay, no saved session, `AskUserQuestion` disabled, the permission mode, and callbacks that refuse every interactive request | `Options`: `cwd`, `model`, `env`, `persistSession: false`, `disallowedTools`, `permissionMode`, `allowDangerouslySkipPermissions`, `canUseTool`, `onElicitation`, `onUserDialog`, `supportedDialogKinds`, `abortController` |
| `packages/subagent/subagent-claude-code/src/run.ts:370`; `src/process.ts:46`, `:67` | Make the harness, not the SDK, start and own the process, so that environment scrubbing and process-tree cleanup apply | `spawnClaudeCodeProcess`, `SpawnOptions`, `SpawnedProcess` |
| `packages/subagent/subagent-claude-code/src/run.ts:236` to `:261`, `:206` to `:226` | Read the message stream; accept only a `result` message with `subtype: "success"`, `is_error: false` and non-blank text, followed by a normal end of stream | `SDKMessage`, `SDKResultMessage` |
| `packages/subagent/subagent-claude-code/src/run.ts:111` to `:124` | Classify failure subtypes into `limit`, `product-error` or `unknown` | result `subtype` values |
| `packages/subagent/subagent-claude-code/src/run.ts:270` to `:306` | Stop: close the query, terminate the process tree, wait for it | `Query.close()` |

**What the harness calls, what it starts, how results and permissions return.** One call
to `query()` per task. The SDK asks the provider to start the per-platform `claude` (or
`claude.exe`) executable; the provider starts it through the shared subprocess service
with stdin and stdout as pipes and stderr passed through
(`packages/subagent/subagent-claude-code/src/process.ts:53` to `:60`). The result is the
text of the final `result` message. Permissions never reach a person: the configured
mode (`dontAsk` by default, or `acceptEdits`, `auto`, `plan`, `bypassPermissions`) is
given to Claude Code, and anything that would still prompt is denied by the callback
(`packages/subagent/subagent-claude-code/README.md:48` to `:54`,
`src/run.ts:338` to `:349`). A denial is remembered as one fixed line that is attached to
a failed result (`src/run.ts:138` to `:145`).

## 3. Tech debt upstream carries

Search performed: every import of the package; `patches/` (no patch for it);
`pnpm-workspace.yaml`; the provider README's limitations; the design note for the two
product providers.

All nine units below are one fact seen nine times: upstream downloads a just-published
vendor release (the SDK and eight platform packages) through npm, and its package
manager refuses packages younger than a set age unless each exact name and version is
exempted. The stated reason: "The active pnpm supply-chain policy blocks this reviewed
runtime closure until its release-age window expires unless every exact package is
exempt" (`pnpm-workspace.yaml:83`).

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-pin.minimumReleaseAgeExclude.@anthropic-ai/claude-agent-sdk@0.3.263` | Release-age exemption for the SDK package | `pnpm-workspace.yaml:93` | Resolved by `DH-ADR-CLAUDEAGENTSDK-1`: the Go build downloads no SDK, so there is nothing to exempt. |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@anthropic-ai/claude-agent-sdk-darwin-arm64@0.3.263` | Exemption for the macOS arm64 executable package | `pnpm-workspace.yaml:85` | Resolved by `DH-ADR-CLAUDEAGENTSDK-1`: no vendor executable is downloaded, bundled or shipped; the user installs Claude Code. |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@anthropic-ai/claude-agent-sdk-darwin-x64@0.3.263` | Same, macOS x64 | `pnpm-workspace.yaml:86` | Same. |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@anthropic-ai/claude-agent-sdk-linux-arm64-musl@0.3.263` | Same, Linux arm64 musl | `pnpm-workspace.yaml:87` | Same. |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@anthropic-ai/claude-agent-sdk-linux-arm64@0.3.263` | Same, Linux arm64 | `pnpm-workspace.yaml:88` | Same. |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@anthropic-ai/claude-agent-sdk-linux-x64-musl@0.3.263` | Same, Linux x64 musl | `pnpm-workspace.yaml:89` | Same. |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@anthropic-ai/claude-agent-sdk-linux-x64@0.3.263` | Same, Linux x64 | `pnpm-workspace.yaml:90` | Same. |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@anthropic-ai/claude-agent-sdk-win32-arm64@0.3.263` | Same, Windows arm64 | `pnpm-workspace.yaml:91` | Same. |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@anthropic-ai/claude-agent-sdk-win32-x64@0.3.263` | Same, Windows x64 | `pnpm-workspace.yaml:92` | Same. |

Related debt that has no unit of its own but has the same cause, recorded so the
resolution is not overstated:

- The executable is large: about 92 MB packed and 325 MB unpacked for one platform
  (`packages/subagent/subagent-claude-code/README.md:187`).
- Its licence is the vendor's terms. Upstream's right to redistribute rests on an
  authorization given to the upstream project owner
  (`.agents/notes/archived/feature/2026-08-04-claude-code-and-codex-subagent-backends.md:74`,
  `THIRD_PARTY_NOTICES.md:187`). Inference: that authorization does not transfer to this
  project.
- An install without optional dependencies leaves the provider present but unusable
  until the first delegation fails (`packages/subagent/subagent-claude-code/README.md:83`).
- Upstream's evidence "proves the pinned official SDK/CLI integration rather than
  compatibility with independently installed Claude versions" (the same note, line 70).
  The Go design takes on exactly that compatibility work; see `DH-LIB-CLAUDEAGENTSDK-R2`.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-CLAUDEAGENTSDK-R1` | A delegated task starts one fresh Claude Code process in the child working directory, from the executable the operator configured or the one found on `PATH`. The environment is the scrubbed host environment plus the provider's explicit overlay. | `packages/subagent/subagent-claude-code/src/run.ts:329`; changed by `DH-ADR-CLAUDEAGENTSDK-1` |
| `DH-LIB-CLAUDEAGENTSDK-R2` | Before the first run, the provider reads the executable's version using the same environment as a run. A missing executable, a version below the stated minimum, or an executable that cannot be started directly is reported as a fixed failure category before any task text is sent. A version above the tested range is logged and allowed. | section 3, last bullet; `patches/@botiverse__oar@0.67.0.patch:105` to `:108` |
| `DH-LIB-CLAUDEAGENTSDK-R3` | Only a non-empty, text-only task is accepted; anything else is refused before a process starts. | `packages/subagent/subagent-claude-code/src/run.ts:184` to `:199` |
| `DH-LIB-CLAUDEAGENTSDK-R4` | The run never waits for a person. The permission mode is fixed per provider instance, defaults to `dontAsk`, and is always passed explicitly; skipping all permission checks happens only when the operator wrote that mode. Questions to the user are unavailable to the child. | `packages/subagent/subagent-claude-code/src/run.ts:44` to `:56`, `:331` to `:336`; `patches/@botiverse__oar@0.67.0.patch:21` to `:23` |
| `DH-LIB-CLAUDEAGENTSDK-R5` | A run is `completed` only when the stream contains a `result` message with `subtype` `success`, `is_error` false and non-blank text, the stream then ends, and the process exits with status 0. Every other outcome is `error` with a category from a closed list (`limit`, `product-error`, `invalid-result`, `process`, `unsupported-version`, `unknown`). | `packages/subagent/subagent-claude-code/src/run.ts:206` to `:261`, `:62` to `:73` |
| `DH-LIB-CLAUDEAGENTSDK-R6` | The parent session receives only the final answer, or a failure line built from fixed words, the category, the exit code or signal, and at most one fixed permission line; never stderr, task text, paths, environment values or raw messages; at most 4,096 bytes. | `packages/subagent/subagent-claude-code/README.md:152`; `docs/subsystems/subagent.md:282` |
| `DH-LIB-CLAUDEAGENTSDK-R7` | Cancellation yields `aborted` with no failure line. Disposal returns only after the whole process tree has exited, and may be called more than once. Files the child changed before cancellation are not restored. | `packages/subagent/subagent-claude-code/src/run.ts:270` to `:306`; `README.md:177` |
| `DH-LIB-CLAUDEAGENTSDK-R8` | Claude Code keeps no saved session for the run, and reads its normal user, project and local settings and login; the harness neither copies, filters nor writes them. | `packages/subagent/subagent-claude-code/src/run.ts:330`; `README.md:56` |
| `DH-LIB-CLAUDEAGENTSDK-R9` | If anything fails after the process started and before the run is handed to its owner, the provider stops the process, waits for it, and only then reports the failure. | `packages/subagent/subagent-claude-code/src/run.ts:458` to `:516`; `docs/subsystems/subagent.md:376` |

## 5. Go choice

<a id="dh-adr-claudeagentsdk-1"></a>
### DH-ADR-CLAUDEAGENTSDK-1 Drive the user's installed `claude` executable in print mode; bundle nothing

- **Decision.** The Go provider starts the `claude` executable that is already on the
  user's machine, in its non-interactive "print" mode with streaming JSON output, and
  reads the result from its stdout. The harness release contains no Anthropic software.
  The proposed command, each flag from the vendor's CLI reference (Vendor doc):

  | Argument | Purpose | Upstream option it replaces |
  |---|---|---|
  | `-p` with the task on stdin | Run once and exit; stdin avoids argument-length limits and keeps the task out of process listings (Vendor doc: non-interactive mode reads stdin, capped at 10 MB) | `query({ prompt })` |
  | `--output-format stream-json --verbose` | One JSON message per line; the last line is the `result` message (Vendor doc) | the SDK message iterator |
  | `--permission-mode <mode>` | `dontAsk`, `acceptEdits`, `auto`, `plan` or `bypassPermissions` (Vendor doc lists these values) | `permissionMode`, `allowDangerouslySkipPermissions` |
  | `--permission-prompts none` | Deny anything that would prompt, remove tools that need a person, cancel MCP elicitation; denials appear as `permission_denied` messages and in the result's `permission_denials`. Needs Claude Code 2.1.259 or later (Vendor doc) | the `canUseTool`, `onElicitation` and `onUserDialog` callbacks |
  | `--disallowedTools AskUserQuestion` (plus `ExitPlanMode` in `plan` mode) | Keep upstream's explicit removal on versions where the flag above might not remove them | `disallowedTools` |
  | `--no-session-persistence` | No saved session (Vendor doc: print mode only) | `persistSession: false` |
  | `--model <name>` when configured | Fixed model for the instance | `model` |

  The minimum supported version is 2.1.259, because of `--permission-prompts`. The
  version upstream tested, 2.1.263, is above it.
- **Why.** Upstream needed the SDK for three things: to find and start the bundled
  executable, to parse its messages, and to refuse interactive requests through
  in-process callbacks. In Go the first is not wanted (below), the second is a line
  reader and a few structs, and the third is now a command-line flag. Not bundling
  removes the nine exemptions, a download of several hundred megabytes per platform, and
  a licence this project has no authorization to redistribute under (section 3).
  Upstream already has a provider that uses the installed program: the experimental OAR
  provider "use[s] their installed native CLIs, authentication, and settings"
  (`packages/experimental/subagent-oar/README.md:12`).
- **Tradeoff.** The user must install Claude Code and log in; the feature is absent
  until they do. The harness no longer controls which Claude Code version runs, so a
  vendor change can break delegation without any change on our side. The mitigations are
  the version check (`DH-LIB-CLAUDEAGENTSDK-R2`), strict acceptance that fails closed on
  anything unrecognised (`-R5`), and a real-product scenario per supported version
  (`DH-E-CLAUDEAGENTSDK-1`). In-process permission callbacks are given up; upstream used
  them only to say no.
- **Serves.** DH-ADR-1 (the provider stays in scope), DH-ADR-2 (a choice that exists
  because of npm is not carried over), DH-ADR-4 (the host owns the child process).
- **Verified by.** `DH-T-CLAUDEAGENTSDK-1` to `-5` against a scripted stand-in
  executable; `DH-E-CLAUDEAGENTSDK-1` against the real product.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** own driver over the installed executable's print mode | Standard library only; uses documented flags; nothing to redistribute | We own compatibility with the vendor's output; no permission callback. That follows from this choice, not from the vendor (the vendor's SDK has `canUseTool`, `onElicitation` and `onUserDialog`, section 2), so "the external agent has no path to ask the user" is a **scope boundary of this record**, not a platform limit (`UP-DEBT-limit.packages.subagent.subagent-claude-code.6`). A lead to check before the first build: the vendor's command line may offer a flag that hands permission prompts to an MCP tool in print mode; if `DH-E-CLAUDEAGENTSDK-1` shows it works, the driver uses it and the boundary narrows | Flags and message facts: vendor documentation read 2026-11-18. The pattern is also what upstream's OAR dependency does: it "drives claude in print mode over stream-json" (`patches/@botiverse__oar@0.67.0.patch:10`) |
| The generic ACP provider with an ACP adapter for Claude Code | No product-specific code; permission requests arrive as protocol messages | The adapter the Go ACP module's example uses is an npm package started with `npx` (`github.com/coder/acp-go-sdk: example/claude-code/README.md`), so the user needs Node; available today by configuration, so it needs no decision here | See [agentclientprotocol-sdk.md](agentclientprotocol-sdk.md) |
| `github.com/severity1/claude-agent-sdk-go` | A Go port of the SDK, including its control messages | Community project, not the vendor's; it re-implements an undocumented control protocol, so it carries the same drift risk plus a dependency | v0.10.0, published 2026-10-05, MIT, `go 1.18`, no `require` lines, no cgo; `go list -m -json`, `go mod download -json`, `LICENSE` and `go.mod` in the module cache, 2026-11-18. The vendor publishes no Go SDK: `github.com/anthropics/claude-agent-sdk-go` reports "Repository not found" |
| Ship the vendor executable inside our release | Known version; works without user setup | Licence; size; a release artefact per platform; the debt of section 3 returns | `packages/subagent/subagent-claude-code/README.md:187`; `THIRD_PARTY_NOTICES.md:187` |

**What the user must have installed.** Claude Code 2.1.259 or later as a native
executable that can be started directly, and a working login or an API key passed in the
provider's `env` (ambient credential-shaped variables are scrubbed, as upstream:
`packages/subagent/subagent-claude-code/README.md:56`). Node and npm are not needed by the
harness. Inference: on Windows an npm-installed `claude.cmd` shim is not acceptable,
because starting a batch file with arbitrary arguments is unsafe; upstream likewise
forwards "`claude.exe` without a batch shim" (the design note, line 70).

## 6. Design notes

**Shape.** No public API beyond the subagent provider contract. Inside the provider:
a locator (configured path, else `PATH`), a version probe, an argument builder that is a
pure function of the instance configuration, a line reader that decodes each message
into a struct with only the fields used (`type`, `subtype`, `is_error`, `result`,
`permission_denials`) and ignores unknown fields and unknown message types, and the
shared one-run lifecycle used by all external providers. Inference: that lifecycle
(start, publish, settle, dispose) is common to this provider, the Codex provider and the
ACP provider, and belongs in one package of the subagent area.

**Ownership and access.** The process, its pipes and its process group (a job object on
Windows) are created through the harness subprocess service and are owned by the run;
the run is owned by the fiber of the provider component, so unloading the component
disposes every live run. The parent agent has access to the result and to cancellation
only. The product's own files (login, settings, any transcripts it writes despite the
flag) belong to the user and are never deleted by the harness.

**Failure, retry, cleanup.**

| Moment | What happens | What is left uncertain |
|---|---|---|
| Executable missing, too old, or a shim | Refused before any task text is sent (`-R2`) | Nothing |
| Failure after the process started, before the run is published | Stop, wait for exit, then reject (`-R9`); if stopping also fails, both facts are reported and no claim of a clean stop is made | A process that would not die; its id is logged |
| Process exits or stream ends without an accepted result | `error` with category `process` or `invalid-result` and the exit code (`-R5`) | Whether the child changed files |
| Cancellation | Close stdin, then the service's terminate ladder; result `aborted` (`-R7`). Vendor doc: an interrupt signal ends the turn, a terminate signal exits with 143 and stops running shell commands | Files already changed stay changed |
| Retry | Never automatic: a task is not idempotent | n/a |

**Upstream decisions judged.**

| Upstream decision | Verdict | Reason |
|---|---|---|
| One fresh process per task; no continuation | Keep | Simple ownership; continuation is a product question for the subagent area. |
| The product's own settings and login are authoritative | Keep | The harness must not manage another vendor's account state. It does mean the workspace's Claude hooks and MCP servers run (Vendor doc: a print-mode run executes project hooks without a trust prompt); the security area should record that delegating to Claude Code trusts the workspace's Claude configuration. |
| Unattended by design; default `dontAsk` | Keep | Fails closed. |
| Bundle a pinned executable; never use the host's | Change | `DH-ADR-CLAUDEAGENTSDK-1`. |
| Use the vendor SDK | Change | No Go SDK from the vendor; its three jobs are small or unwanted. |
| Fixed-word diagnostics | Keep | `-R6`. |

## 7. Risks and open questions

- **Bare mode may become the default.** Vendor doc: `--bare` "will become the default
  for `-p` in a future release"; bare mode skips settings, hooks and stored logins. That
  would silently break `DH-LIB-CLAUDEAGENTSDK-R8`. The scenario below checks that a
  project setting is honoured; the subagent area should add an explicit setting for
  which behaviour is wanted once the vendor offers a way to opt out.
- **Two command details are not confirmed.** Whether `--permission-mode
  bypassPermissions` alone is enough in print mode or a second flag is needed; and
  whether a task given only on stdin, with no prompt argument, is accepted in every
  supported version (a vendor example pipes input to `claude -p` with no prompt). The
  scenario covers both; the fallback for the second is the prompt as an argument.
- **Output shape is the vendor's.** The message format is documented but versioned by
  the product, not by a protocol number. Unknown message types are ignored; an
  unrecognised result fails closed.
- **For the subagent area:** it owns the provider package, the shared run lifecycle and
  the supported-version range; and it decides whether the child runs inside the
  operating-system sandbox (Claude Code needs the network and its own state directory).
- **For the plugin or bundle area:** upstream delivers this provider as an optional
  bundle that installs the executable (`packages/subagent/subagent-claude-code/README.md:32`).
  In Go the provider is compiled in and enabling it only checks for the executable.

## 8. Verification

Local tests use a stand-in executable built by the test (a small Go program that prints
scripted lines); they prove our handling, not the vendor's behaviour.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-CLAUDEAGENTSDK-1` | local | `DH-LIB-CLAUDEAGENTSDK-R1`, `-R2`, `-R3`, `DH-LIB-OAR-R3` | Missing executable, old version, a `.cmd` shim, and a non-text task are each refused before the stand-in receives input; the version probe sees the provider's `env` overlay and not a scrubbed ambient secret. |
| `DH-T-CLAUDEAGENTSDK-2` | local | `DH-LIB-CLAUDEAGENTSDK-R4`, `-R8`, `DH-LIB-OAR-R1` | For each mode the stand-in records its arguments: the mode is always present, a skip-all-checks argument appears only for `bypassPermissions`, the prompt-refusal and no-persistence arguments are always present, and no settings-selection argument is ever added. |
| `DH-T-CLAUDEAGENTSDK-3` | local | `DH-LIB-CLAUDEAGENTSDK-R5`, `-R6` | A table of scripted streams (success; each failure subtype; `is_error` true; blank text; no result; result then non-zero exit; unknown message types) gives the tabled stop reason and category; a secret planted in stderr and in the task never appears in the result. |
| `DH-T-CLAUDEAGENTSDK-4` | local | `DH-LIB-CLAUDEAGENTSDK-R7`, `-R9`, `DH-LIB-OAR-R2`, `DH-LIB-OAR-R4` | A stand-in that starts a grandchild and ignores a polite stop: cancel gives `aborted`, dispose returns only when both processes are gone, a second dispose is a no-op; a stand-in that fails at start, and one whose start is cancelled, are gone before the start error is returned. |
| `DH-T-CLAUDEAGENTSDK-5` | local | `DH-LIB-CLAUDEAGENTSDK-R1` | Two runs started together have separate processes and independent outcomes; unloading the provider component disposes both. |
| `DH-E-CLAUDEAGENTSDK-1` | real environment | `DH-LIB-CLAUDEAGENTSDK-R1` to `-R8` | See below. |

<a id="dh-e-claudeagentsdk-1"></a>
#### DH-E-CLAUDEAGENTSDK-1 Delegation to the real installed Claude Code

- **Given** a test machine for each supported operating system with Claude Code
  installed at (a) the minimum supported version and (b) the newest release, an account
  or API key supplied at run time through the provider's `env`, a scratch workspace
  containing a project settings file with one recognisable setting, and the built
  harness with the provider enabled.
- **When**, on that machine, a harness session delegates in turn: a task that answers
  with a given word; a task that must write a file, under `dontAsk`; the same under
  `acceptEdits`; the same under `bypassPermissions`; a long task that is cancelled after
  its first tool call.
- **Then** the first returns exactly the word; under `dontAsk` no file exists and the
  result carries the fixed permission line; under `acceptEdits` and `bypassPermissions`
  the file exists; the cancelled run returns `aborted` and no process started for it
  remains; the project setting was honoured; nothing from stderr appears in the parent
  session; no saved Claude session was created for any run.
- **Cleanup.** Delete the scratch workspace. The test does not install, log in or change
  Claude settings, so nothing else is undone. If a process remains, record its id and
  command line and keep the workspace.

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-@anthropic-ai/claude-agent-sdk` and the nine
`UP-DEBT-pin.minimumReleaseAgeExclude.@anthropic-ai/claude-agent-sdk*@0.3.263` units listed
in section 3.
