# Execution (subprocesses, persistent terminals and shell tools): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". IDs in this document use the area key `EXEC`. Nothing here has
> been compiled, run or tested.

| | |
|---|---|
| **Area key** | `EXEC` (agent A09) |
| **Upstream packages** | 15: `packages/subprocess` (3), `packages/terminal` (2), `packages/shell` (10) |
| **Serves outcomes** | [DH-O-7](../design.md#dh-o-7) (confinement), [DH-O-10](../design.md#dh-o-10) (seams), [DH-O-6](../design.md#dh-o-6) (bounded results), [DH-O-1](../design.md#dh-o-1) (owned lifetimes), [DH-O-12](../design.md#dh-o-12) (terminal in the browser), [DH-O-14](../design.md#dh-o-14) (pure-Go binary, catalogs) |
| **Depends on areas** | A03 (tool pipeline, result shaping, call tokens), A06 (session working directory), A07 (spill store), A08 (filesystem seam of the same execution world), A10 (sandbox plan and policy, SSH provider), A13 (jobs), A17 (credential names), A19 (terminal WebSocket route), C6 (terminal island), X5 (the `terminal_*` tools), A23 (schema, test kit) |
| **Priority** | P0 for the process package, trees, output capture, confined launch, shell executors and one-shot tools: no agent can do coding work without them. P1 for pseudo-terminals, the screen, terminal sessions and the persistent tools |

## 1. Purpose

An agent harness spends most of its time running other programs. This area starts them,
stops them **with everything they started**, bounds what they print, decides what
environment they see, and gives the model two ways to use a shell: one command per fresh
process, or a shell that stays alive between tool calls. It also owns three pieces every
other area uses: the **one package allowed to start a process**, **pseudo-terminal
allocation** on Linux, macOS and Windows, and the **host-side terminal emulator** that
turns terminal output into text the model can read and a screen a browser can restore.
Without it the model could not build or test anything, and no other area could start a
language server, a plugin process or LibreOffice under one set of rules.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **Argument list (argv)** | The program name followed by its arguments as separate strings. | Starting from a list, never a string, keeps a model-chosen value from being interpreted by a shell by accident. |
| **Process tree, tree owner** | Everything a started program starts in turn; the owner is the operating-system object the harness terminates and waits on: a Windows **job object**, a Linux **control group** (cgroup, a kernel directory whose members are tracked even if they detach) or a POSIX **process group**. | "The command exited" and "nothing it started is left" are different facts. |
| **Stop ladder, grace, settle deadline** | The fixed order for stopping a child: revoke authority, ask, close input, terminate, kill. Grace is the wait between rungs; the settle deadline bounds the final wait. | One order for every consumer ([../design.md §4.4](../design.md#44-process-model)), and no wait without a bound. |
| **Collect, tail, spill** | Collect keeps the last N bytes of a stream in memory (the tail) and may write the whole stream to a bounded file (the spill). | Errors cluster at the end; the model must never receive unbounded text. |
| **Launch plan, launcher status, untrusted tree** | The plan is the `sandbox.Launch` value the sandbox seam (A10) builds for one command: how to wrap it, confirm it and release it. The status is the exchange on a private channel in which the launch stage says what it enforced and the host answers "go". A tree started under any plan but the host's own trusted one is an untrusted tree, and the host can say whether a given process is in one. | A command runs under the resolved policy or does not run; "the launcher failed" is never guessed from the command's output; and a process started for the model can be told from the owner ([DH-ADR-HOST-12](host-telemetry.md#dh-adr-host-12)). |
| **Pseudo-terminal (PTY), ConPTY, console host** | A PTY is a device pair that makes a program believe it talks to a terminal. ConPTY is the Windows equivalent, provided by a console host process. | Shells, debuggers and REPLs behave differently, or refuse to run, on a pipe. |
| **Session, foreground group** | The processes started from one terminal; the group that currently receives keyboard signals. | Ctrl-C must reach the running command, not the shell. |
| **Screen, scrollback, mark, snapshot** | The emulated grid of characters; the bounded history above it; a remembered line; a byte string that redraws the screen in a browser. | The model reads text a person would see; a browser tab restores at once. |
| **Terminal session, owner** | A PTY-backed program that outlives one tool call. Its owner is the one agent (or user) that may use it and whose end closes it. | Here ownership and access coincide: nobody else may touch it. |
| **Readiness, wait reason** | The judgement that a terminal is waiting for input again, and why a send returned. | A terminal never says "done"; the harness must infer it and say how sure it is. |
| **Dialect** | `bash` or `pwsh` (PowerShell). | The two shells differ in argv, quoting, exit status and encoding; nothing translates between them. |
| **Managed environment** | The `DSH_*` variables the harness sets for every shell call. | A command can learn its session and home without inheriting stale values. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit (`UP-PKG-packages.<group>.<name>`) | Package | Role | What it does |
|---|---|---|---|
| `subprocess.subprocess` | `@deepseek-ai/dsh-subprocess` | definition | `ctx.subprocess`: lookup, `spawn`, `spawnTerminal`, the scrubbed environment, the collected-output shape |
| `subprocess.subprocess-local` | `dsh-subprocess-local` | provider | Runs processes on the host: tree owners, collectors and spill files, node-pty terminals, process inspection |
| `subprocess.win32-process` | `dsh-win32-process` | other (library) | Win32 process, stdio and job-object calls through the `koffi` foreign-function binding |
| `terminal.terminal` | `dsh-terminal` | definition | `ctx.terminals`: owner-scoped session ids, backend registry, send, read, signal, awaited cleanup |
| `terminal.terminal-bash` | `dsh-terminal-bash` | provider | The `shell` backend: a persistent bash or pwsh on a PTY, readiness detection, bounded scrollback |
| `shell.shell` | `dsh-shell` | definition | `ctx.shell`: request and resolved spec, foreground result, background handle, exit-marker parser |
| `shell.shell-env` | `dsh-shell-env` | definition and provider | `ctx.shellEnv`: registry of managed `DSH_*` variables |
| `shell.bash-local` | `dsh-bash-local` | provider | Fresh `bash -c` per call, unconfined |
| `shell.bash-sandbox` | `dsh-bash-sandbox` | provider | The same, with the argv wrapped by `ctx.sandbox.confine()` |
| `shell.pwsh-local` | `dsh-pwsh-local` | provider | Fresh `pwsh -Command` per call, unconfined |
| `shell.pwsh-sandbox` | `dsh-pwsh-sandbox` | provider | The same, confined |
| `shell.tool-bash` | `dsh-tool-bash` | consumer | The model's `bash` tool: one-shot, background jobs, sandbox escalation |
| `shell.tool-pwsh` | `dsh-tool-pwsh` | consumer | The model's `pwsh` tool, a twin of the above |
| `shell.tool-bash-persistent` | `dsh-tool-bash-persistent` | consumer | A `bash` tool whose shell persists per agent, on a terminal session |
| `shell.tool-pwsh-persistent` | `dsh-tool-pwsh-persistent` | consumer | The PowerShell twin |

### 3.2 Behaviour

- **One execution world.** The filesystem service and the subprocess service together describe one path and process namespace; shells, terminals and language servers consume those two and never name the provider, so a remote world needs no second terminal package (`.agents/notes/implemented/architecture/2026-07-28-portable-execution-world-consumers.md:17-29`).
- **Starting a process.** The request is fully explicit, the seam applies no defaults and argv is never shell-interpreted (`docs/subsystems/subprocess.md:93`). A bare name is searched on the child's scrubbed `PATH`, with `PATHEXT` on Windows; a relative path with a separator is refused (`packages/subprocess/subprocess-local/src/index.ts:168-208`).
- **Environment.** Children start from the harness environment minus names matching `KEY|PASSWORD|SECRET|TOKEN` and minus every `DSH_*` name; the caller's entries merge after the scrub (`packages/subprocess/subprocess/src/index.ts:54`, `:73-76`).
- **Output.** Each stream is ignored, piped raw, inherited or collected. Collect keeps the tail and may spill the whole stream to an owner-only file in the OS temp directory; readers take byte offsets and never consume (`docs/subsystems/subprocess.md:41`, `:137`; `packages/subprocess/subprocess-local/README.md:47`).
- **Process trees.** One owner is chosen per spawn before the command can run: a user-systemd scope on Linux, a job object held by a helper Node process on Windows, otherwise a process group or a process-table scan with one warning. A command is never replayed through a weaker owner (`packages/subprocess/subprocess-local/README.md:86`; `src/index.ts:250-290`). `done` reports the direct command and `waitForExit()` the whole range (`subprocess.md:137`). The outcome carries exit code or signal only; the caller decides whether a kill was a timeout (`subprocess.md:225`).
- **Terminals.** `spawnTerminal` is the one non-pipe primitive: it allocates a PTY through node-pty and owns foreground-group inspection, signalling and one awaited teardown. Readiness, scrollback and session policy stay in the consumer (`subprocess.md:245`).
- **Persistent sessions.** `ctx.terminals` mints ids, publishes a session only after backend setup succeeds, fences every operation to the exact owning agent and allows one send at a time (`packages/terminal/terminal/src/index.ts:228`, `:372`; `docs/subsystems/terminal.md:9`, `:61`). The backend starts `bash --noprofile --norc -i` or `pwsh -NoLogo -NoProfile` (`packages/terminal/terminal-bash/src/config.ts:65-67`) with a controlled prompt that prints an `OSC 133;D` marker (`terminal-bash/src/index.ts:80-96`). A send settles as `stdin_read`, `inferred_idle`, `timeout` or `session_exit`. The model reads text from a hand-written byte filter; a headless xterm is fed only to answer terminal queries (`packages/terminal/terminal-bash/README.md:169`). Raw bytes are process-local and never persisted (`terminal.md:91`).
- **Shell executors.** `resolve()` turns a request into a spec with every default and cap explicit; `execute()` returns one handle whose foreground result and background reads are views of the same process (`docs/subsystems/shell.md:15`, `:251`). Bash runs `['bash','-c',command]` with `NO_COLOR=1 TERM=dumb PAGER=cat GIT_PAGER=cat` (`packages/shell/bash-local/src/index.ts:28-31`, `:188`). PowerShell runs `pwsh -NoLogo -NoProfile -NonInteractive -Command <UTF-8 preamble + command>` (`packages/shell/pwsh-local/src/index.ts:49`, `:194`), found at an explicit path, well-known install locations, `PATH`, then Windows PowerShell 5.1 (`pwsh-local/src/resolve.ts:22-35`). Timeout and cancellation are separate facts and the first cause wins (`bash-local/src/index.ts:211-226`). The sandbox executors subclass the local ones and wrap the argv.
- **Shell tools.** `bash` and `pwsh` take `description`, `command`, `timeoutMs`, `workdir` and, when available, `run_in_background`, `sandbox_permissions` and `justification` (`docs/tool-catalog.md:651`). Results end with text markers such as `[exit code: N]`. With a job registry every command is a job from its start, and a foreground command that outlives its timeout keeps running as that job (`packages/shell/tool-bash/README.md:64`).
- **Persistent shell tools.** One shell per agent, commands serialised. Each command is typed into the terminal as one line wrapped in printable start and end markers that carry the exit status (`packages/shell/tool-bash-persistent/src/index.ts:87`; `tool-pwsh-persistent/src/index.ts:99`), and the result is cut out of the scrollback. Any doubt (timeout, cancel, `exit`) closes the shell.
- **Events, persistence, config.** The area defines no event of its own and persists nothing; what the model sent and received is in `tool/call` and `tool/result`. The terminal backend intercepts `sandbox/mode` to refuse a mode change while the owner has a terminal (`terminal-bash/src/index.ts:52-55`). The ten config schemas are listed in §6.2.

### 3.3 Failure, retry, rollback and cleanup upstream

- Disposing the subprocess service terminates every range and terminal and awaits them (`subprocess-local/src/index.ts:135-166`), so a process survives a consumer reload but not its composition.
- A spawn that never starts rejects without output; nothing is retried and a command is never replayed. At a normal Node exit a synchronous pass kills what it can; a killed or crashed harness leaves children behind and needs "an external supervisor" (`subprocess-local/README.md:66`, `:155`).
- After `SIGKILL`, uninterruptible kernel I/O can keep disposal waiting with no bound (`README.md:148`). Completed spill files are never deleted (`README.md:157`). A terminal close that cannot confirm the tree gone rejects and may be retried; a failed backend setup is rolled back before anything is published (`docs/subsystems/terminal.md:27`).

## 4. Upstream decisions dissected

The first seven rows are the decision notes handed to this area (`UP-DEC-…`); the rest are visible only in code and READMEs.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `implemented.architecture.2026-07-28-portable-execution-world-consumers` | Filesystem plus subprocess is one execution world; the terminal primitive lives in the subprocess provider, readiness in the consumer | keep | It is what lets SSH replace the world without new shell or terminal code | [DH-ADR-EXEC-1](#dh-adr-exec-1) |
| `implemented.architecture.2026-09-11-subprocess-control-pipe` | A child may get a separate byte channel on descriptor 7 so a host protocol never mixes with program output | change | The fixed descriptor and the C-runtime table exist for a Node child; its consumer, the program guest, is not built ([DH-ADR-8](../design.md#dh-adr-8)). The need for a private inherited channel remains (launcher status) | Generic extra channels in the spawn spec, [DH-D-EXEC-1](#dh-d-exec-1) |
| `implemented.bug-fix.2026-09-27-pwsh-prompt-tail-grace` | `promptTailGraceMs` (default 0) extends the wait when the prompt marker arrived and its text has not | keep, narrowed | The two-chunk delivery comes from the console layer. In Go a bracketed command completes on its end line and does not wait for the prompt; the setting matters only for raw sends | Config field kept, [DH-D-EXEC-7](#dh-d-exec-7) |
| `implemented.bug-fix.2026-09-28-pty-bundled-console-host` | Windows terminals use the console host node-pty ships, because older system hosts repaint the prompt in another byte form | change | The cause is comparing bytes. Comparing the emulated screen makes both forms equal, so nothing is bundled | [DH-ADR-NODEPTY-2](../libs/node-pty.md#dh-adr-nodepty-2), [DH-ADR-EXEC-5](#dh-adr-exec-5) |
| `implemented.feature.2026-07-16-persistent-pty-sessions` | Optional owner-scoped, line-oriented PTY sessions with six tools; full-screen programs and restoration deferred | keep, extended | Terminal state is a real need. The emulator removes the line-only limit for reading; the six tools belong to area X5 | [DH-D-EXEC-7](#dh-d-exec-7) |
| `implemented.simplification.2026-09-11-remove-e2b-providers` | No E2B backend; the contracts stay asynchronous and provider-neutral | keep | The Go seam takes a context on every call and promises nothing a remote provider cannot give | [DH-ADR-EXEC-1](#dh-adr-exec-1) |
| `proposed.simplification.2026-09-19-shell-env-key-declarations` | Proposal: drop variable descriptions and `list()` because nothing reads them | drop | The Go design generates catalogs from running registries ([DH-O-14](../design.md#dh-o-14)), which gives the list a reader; completing it is cheaper than removing it | [DH-ADR-EXEC-8](#dh-adr-exec-8) |
| Fully explicit spec, argv never shell-interpreted | No hidden default in the seam | keep | The safety property of the whole area | [DH-ADR-EXEC-1](#dh-adr-exec-1) |
| About two dozen packages start processes outside the service | Each sets its own limits (`UP-DEBT-found.L5.5`) | change | One rule set needs one door | [DH-ADR-EXEC-1](#dh-adr-exec-1) |
| Outcome is exit facts only; the caller classifies | The seam never decides why it killed | change | The harness did the kill and knows why; on Windows there is no signal to read it from | [DH-ADR-EXEC-4](#dh-adr-exec-4) |
| Two waits: direct exit and whole range | A survivor must stay observable | keep | Unchanged | [DH-ADR-EXEC-2](#dh-adr-exec-2) |
| Owner chosen before the run, fallback disclosed, never replay | Honest containment | keep, mechanism changed | Go holds the owners itself: no helper process, no `systemd-run` per command | [DH-ADR-EXEC-2](#dh-adr-exec-2) |
| `terminate()` is the only verb; ladders are the consumer's | Children cooperate differently | change | Every consumer rebuilt the same ladder | [DH-ADR-EXEC-2](#dh-adr-exec-2) |
| Synchronous kill pass in Node's `exit` event | Best effort at host exit | change | Go runs no code at exit; owners held by the operating system and signal handling replace it | [DH-ADR-EXEC-2](#dh-adr-exec-2) |
| Tail kept in memory, spill in the OS temp directory | Bounded output with recovery | keep the shape, change the store | Spill files need an owner that deletes them | [DH-D-EXEC-3](#dh-d-exec-3) |
| Name-pattern credential scrub | One heuristic for every spawner | keep, extended | Deny and allow lists, exact credential names | [DH-ADR-EXEC-8](#dh-adr-exec-8) |
| Sandbox executors subclass the local ones; `danger-full-access` bypasses the sandbox service | Reuse process mechanics | change | Confinement must not depend on which provider was mounted | [DH-ADR-EXEC-3](#dh-adr-exec-3) |
| One `ctx.shell` per composition; `pwsh` is "the bash executor seam" | One provider slot | change | Both shells exist on all three systems | [DH-ADR-EXEC-7](#dh-adr-exec-7) |
| `resolve()` request-to-spec split; one execution, foreground is what the caller awaits | Explicit defaults, no second spawn path | keep | Unchanged | [DH-D-EXEC-8](#dh-d-exec-8) |
| Executor-owned timeout, opt-out of the tool timeout policy | A timeout fused with the caller's signal | change | One deadline, carried by the context | [DH-ADR-EXEC-4](#dh-adr-exec-4) |
| Exit status parsed back from result text | The UI pill reads the marker | change | A structured result field cannot be forged by output | [DH-D-EXEC-9](#dh-d-exec-9) |
| Every command is a job; promote on timeout | Long commands survive their wait | keep | Uses the job registry ([DH-D-JOBS-1](jobs-schedule-goal-plan.md#dh-d-jobs-1)) | [DH-D-EXEC-9](#dh-d-exec-9) |
| Commands typed into the terminal; reset on any doubt | Line editors are fragile; a half-known shell is unsafe | change | Typing is the cause of five limitations; an interrupt can be verified | [DH-ADR-EXEC-6](#dh-adr-exec-6) |
| Byte filter for the model, emulator only for replies; `TERM=dumb` | Line-oriented contract | change | Two answers to "what is on the screen" | [DH-ADR-EXEC-5](#dh-adr-exec-5) |
| Shell lifecycle hooks (`shellActivity`) installed into user shells | Idle or busy for the sidebar terminal | change | The foreground group read from the terminal answers it without touching the user's shell | [DH-D-EXEC-7](#dh-d-exec-7) |
| Owner is the exact agent; publish after setup; one send; sandbox-mode fence | Isolation and no wider terminal after a downgrade | keep | Unchanged | [DH-D-EXEC-7](#dh-d-exec-7) |

## 5. Tech debt and its resolution

`L:<package>.<n>` abbreviates `UP-DEBT-limit.packages.<group>.<package>.<n>`, the n-th bullet of that README's "Known Limitations"; `M:<path>:<line>` abbreviates `UP-DEBT-marker.packages/<path>:<line>`. The evidence is that bullet or source line. "Carried" rows are limits the Go design keeps; each says why, and the ledger row holds the justification. Their owning stage is a placeholder (§10).

| Debt ID | What it is | Resolution in Go |
|---|---|---|
| `L:subprocess.1` | Transports that own their spawn stay outside the service | Resolved: no package but `proc` can start a process; the MCP, SDK and plugin transports are harness code that calls it ([DH-D-EXEC-1](#dh-d-exec-1)) |
| `L:subprocess.2` | Teardown ladders are rebuilt by each consumer | Resolved: one stop ladder in the package, with a hook for the protocol-level "ask" ([DH-D-EXEC-2](#dh-d-exec-2)) |
| `L:subprocess.3` | What can be observed depends on the provider | Resolved: every handle and result reports its owner kind, so a consumer never needs to know the provider. The weak rungs themselves are carried below |
| `L:subprocess-local.1` | After `SIGKILL`, disposal can wait for ever | Resolved: a settle deadline; the survivor is reported and the unload continues ([DH-D-EXEC-2](#dh-d-exec-2)) |
| `L:subprocess-local.2` | Native ownership needs specific host facilities; macOS has none | Resolved on Linux (U-K1-1): cgroup v2 with either a writable own subtree or a scope the user service manager delegates once at start ([DH-ADR-EXEC-2](#dh-adr-exec-2)); a host with neither falls to process groups and says so. macOS still has only process groups, a platform limit |
| `L:subprocess-local.3` | Probes and helper processes on every spawn | Resolved: the owner rung is decided once when the provider starts; no child process is used to probe or to hold an owner |
| `L:subprocess-local.4`, `L:win32-process.5` | Job breakaway exclusions; a window between create and assign | Resolved: the process is created inside the job in one call and the job grants no breakaway. Residual: work a system service does on the tree's behalf (§10) |
| `L:subprocess-local.5`, `L:tool-pwsh-persistent.6` | Windows terminal signals are console-wide; stop and hang-up do not exist | **Carried**: Windows consoles have no per-group signals. Refused as unsupported, never ignored |
| `L:subprocess-local.6` | Windows terminals need node-pty's bundled console host | Resolved by [DH-ADR-NODEPTY-2](../libs/node-pty.md#dh-adr-nodepty-2) and screen-based readiness ([DH-ADR-EXEC-5](#dh-adr-exec-5)) |
| `L:subprocess-local.7` | Fallback terminal cleanup can miss a child that left the session | **Carried** for macOS and for Linux without a cgroup; elsewhere the cgroup or job owns it |
| `L:subprocess-local.8` | Cleanup needs a JavaScript-observable exit | Resolved: owners the operating system keeps, plus handlers for `SIGTERM`, `SIGINT` and `SIGHUP` that run normal disposal ([DH-ADR-NODEPROC-2](../libs/node-process-builtins.md#dh-adr-nodeproc-2)) |
| `L:subprocess-local.9` | The credential scrub is a four-word name heuristic | **Carried**, reduced: deny and allow lists are configuration and names the harness itself reads as credentials are removed exactly, but an unknown secret with an unusual name still passes |
| `L:subprocess-local.10`, `.11` | Spill files never deleted; a removed spill directory is not recreated | Resolved: spill goes to the storage area's spill store, which owns retention and its directory ([DH-D-EXEC-3](#dh-d-exec-3)) |
| `L:win32-process.1`, `.3`, `.6` | DLL loading through `koffi`; a null environment block because `koffi` cannot pass one; x64-only layout probe | Not applicable: there is no foreign-function binding. `golang.org/x/sys/windows` supplies typed calls per architecture and the harness passes an explicit UTF-16 block |
| `L:win32-process.2`, `.4` | Primitives without a service around them | Resolved: the primitives are private to `proc`; the service is the only API ([DH-D-EXEC-1](#dh-d-exec-1)) |
| `L:terminal.1`, `L:terminal-bash.5` | Sessions die with the harness process | **Carried**: surviving a restart needs an owner outside the host process, and the tree owners deliberately end children with the host |
| `L:terminal.2` | No sharing between agents | **Carried** (deliberate): owner scoping is the access rule; a person may attach, another agent may not |
| `L:terminal.3` | No declarative auto-start | **Carried** (deliberate): a session needs an owner, which exists only once an agent or user asks |
| `L:terminal-bash.1` | Line-oriented only; full-screen programs unreadable | Resolved: the model reads the emulated screen, including the alternate screen ([DH-D-EXEC-6](#dh-d-exec-6)), subject to the emulator gate |
| `L:terminal-bash.2`, `L:tool-bash-persistent.2` | Readiness is heuristic where a stdin wait cannot be proven | **Carried**, narrowed: a bracketed command completes exactly on every system, and "shell at prompt" is exact on Linux and macOS; "a foreign child waits for input" stays provable on Linux only |
| `L:terminal-bash.3`, `L:tool-pwsh-persistent.7` | The pwsh bootstrap fails in ConstrainedLanguage | Resolved: the bootstrap uses only core types and `chcp` ([DH-D-EXEC-7](#dh-d-exec-7)); inference, proven by [DH-E-EXEC-6](#dh-e-exec-6) |
| `L:terminal-bash.4` | Cleanup guarantees belong to the provider | Resolved: kept as the design; one `Close` owned by `proc` ([DH-D-EXEC-5](#dh-d-exec-5)) |
| `L:bash-local.1`, `L:pwsh-local.1`, `M:shell/tool-bash/src/index.ts:9` | An executor that confines nothing; policy "belongs elsewhere" | Resolved: there is no unconfined executor; every spawn for an agent carries a launch plan ([DH-ADR-EXEC-3](#dh-adr-exec-3)) and policy sits in the pipeline (area A03) |
| `L:bash-local.2`, `L:pwsh-local.2`, `L:tool-pwsh.2`, `M:shell/bash-local/src/index.ts:150` | No persistent shell | Resolved: the persistent tools ([DH-D-EXEC-10](#dh-d-exec-10)) |
| `L:bash-local.3` | Bash is POSIX-only | Resolved: bash is resolved on Windows too, with stated rules ([DH-ADR-EXEC-7](#dh-adr-exec-7)) |
| `L:bash-local.4`, `L:pwsh-local.4`, `M:shell/tool-bash/src/background.ts:55`, `M:shell/tool-pwsh/src/background.ts:57` | A start failure is disguised as stderr text or a signal-less kill | Resolved: the execution has an explicit `Failed` outcome with the error, apart from output ([DH-D-EXEC-8](#dh-d-exec-8)) |
| `L:bash-sandbox.1` | Confinement covers file effects only | **Carried**: the guarantee is deliberately narrow ([../design.md §6](../design.md#6-security-model)); area A10 owns it |
| `L:bash-sandbox.2` | A denial is inferred from stderr | **Carried** as a hint: launcher failure is now a fact ([DH-ADR-SECURITY-3](../upstream/security.md#dh-adr-security-3)). Whether a backend reports a denied file operation out of band is a fact to establish per backend, and on macOS the sandbox log is read for a failed command (sandbox-ssh.md, row `sandbox.3`, U-K1-15) |
| `L:bash-sandbox.3` | A background launcher failure surfaces only on a later read | Resolved: `Start` waits for the launcher status record, so "never ran" is a start error for background runs too ([DH-D-EXEC-4](#dh-d-exec-4)) |
| `L:bash-sandbox.4` | `danger-full-access` bypasses the sandbox service | Resolved: the sandbox seam is always asked and itself returns the unconfined plan, so one place decides and it is recorded |
| `L:pwsh-local.3`, `L:tool-pwsh.3` | The command is PowerShell text; no dialect translation | **Carried** (deliberate): a shell tool hands text to that shell |
| `L:pwsh-local.5`, `M:shell/tool-bash-persistent/src/index.ts:361`, `M:shell/tool-pwsh-persistent/src/index.ts:371` | Windows reports no signal; a timeout is reported as "timed out or OOM" | Resolved: the result carries the stop cause the harness recorded ([DH-ADR-EXEC-4](#dh-adr-exec-4)) |
| `L:pwsh-local.6` | The encoding preamble breaks a command that must start with `param`, `using` or `#requires` | Resolved: such a command is run from a private script file invoked after the preamble ([DH-D-EXEC-8](#dh-d-exec-8)) |
| `L:pwsh-local.7` | Non-ASCII stdin is mis-decoded by Windows PowerShell 5.1 | **Carried**: only the 5.1 fallback and only trusted callers that pass stdin; the edition is reported so a caller can refuse |
| `L:pwsh-sandbox.1`, `.3` | Windows reads are unconfined; read-only stays partial | **Carried**: stated residual gaps of the Windows backend ([../design.md §6](../design.md#6-security-model)); area A10 |
| `L:pwsh-sandbox.2` | Workspace-write gets a private temp directory | Resolved: kept as behaviour; the launch plan carries the `TMP` and `TEMP` edits ([DH-D-EXEC-4](#dh-d-exec-4)) |
| `L:shell-env.1`, `M:shell/shell-env/src/index.ts:203` | `list()` omits built-in variables | Resolved: built-ins are the first contributor, so the list is complete ([DH-ADR-EXEC-8](#dh-adr-exec-8)) |
| `L:shell.1` | No way to feed a running command | Resolved: interactive input is the terminal seam's contract ([DH-D-EXEC-7](#dh-d-exec-7)); the one-shot seam stays without it on purpose |
| `L:shell.2`, `L:tool-bash.2` | Timeouts are executor-owned and outside the timeout policy | Resolved: the caller's context is the only deadline ([DH-ADR-EXEC-4](#dh-adr-exec-4)) |
| `L:tool-bash.1` | The exit pill is parsed from text and can be fooled | Resolved: structured result fields ([DH-D-EXEC-9](#dh-d-exec-9)) |
| `L:tool-bash.3` | Background commands have no timeout | Resolved: `timeoutMs` applies to a background run when given, and a deployment cap exists; the owner's end always stops it |
| `L:tool-bash.4` | The job list cannot show which rows a tool call waits on | Resolved: the job record carries the waiting call id (depends on area A13) |
| `L:tool-bash-persistent.1`, `L:tool-pwsh-persistent.1` | Needs an owning agent and a real PTY | **Carried** (deliberate): the owner gives the shell its lifetime. Where no PTY can be allocated the tool reports `unavailable` |
| `L:tool-bash-persistent.3` | Timeout and cancel discard the shell even when it is fine | Resolved: interrupt, verify the prompt, reset only if that fails ([DH-ADR-EXEC-6](#dh-adr-exec-6)) |
| `L:tool-bash-persistent.4` | Environment facts belong in the configured description | Resolved: the config field is kept and resolved facts (shell edition, path) are added to it |
| `L:tool-bash-persistent.5`, `L:tool-pwsh-persistent.2`, `.3` | Control characters are eaten by the line editor; echoed input leaks; raw ESC unsupported | Resolved: commands are delivered by file, not typed ([DH-ADR-EXEC-6](#dh-adr-exec-6)) |
| `L:tool-pwsh-persistent.4`, `M:terminal/terminal-bash/src/session.ts:505` | Redefining `prompt` loses readiness; a delayed prompt can settle the wrong send | Resolved: completion is the end line with the command's own nonce, not the prompt |
| `L:tool-pwsh-persistent.5` | A child reading stdin can swallow the next command | Resolved: a command is refused while the previous one has not finished |
| `L:tool-pwsh-persistent.8` | The terminal bell is not reported to the model | **Carried** (deliberate): no consumer |
| `L:tool-pwsh.1` | ConstrainedLanguage and denied named pipes under the Windows sandbox | **Carried** for named pipes only: a property of the restricted token ([sandbox-ssh.md](sandbox-ssh.md), `sandbox-windows-acl.6`), stated in the tool description. The reduced language mode is not carried: `read-only` has a scratch, so PowerShell is expected to start in its full mode ([DH-ADR-SANDBOX-4](sandbox-ssh.md#dh-adr-sandbox-4)), and the tools work in either mode because nothing relies on it (§6.2, "Language mode") |
| `M:shell/tool-bash-persistent/src/index.ts:17`, `M:shell/tool-pwsh-persistent/src/index.ts:18` | The truncation notice advises a file search that does not apply | Resolved: the notice names what was cut and how to read the rest |
| `M:terminal/terminal-bash/src/index.ts:114`, `M:terminal/terminal-bash/src/session.ts:249` | Start-up race and per-send state spread over many fields | Resolved: one goroutine per session owns one send-state value; start-up runs under one context |

## 6. Go design

### 6.1 Shape

All packages sit under `src/harness/internal/world`.

| Package | Kind | Provides and injects | Why it exists |
|---|---|---|---|
| `proc` | library, no cordis | nothing | **The single starter.** The only code that may import `os/exec` or call a process-creation function: lookup, environment block, Windows command line, tree owners, collectors, PTY allocation |
| `subprocess` | contract | keys `subprocess.Key` and `subprocess.HostKey`, interface `Service` | The seam: a remote world replaces the provider, consumers do not change |
| `subprocess/local` | provider component | provides both keys; injects the spill store (A07) | Wraps `proc`; owns every live process and terminal of its fiber |
| `terminal` | contract and service component | key `terminal.Key`; a registry of backends by type | Owner-scoped sessions that outlive a tool call |
| `terminal/screen` | library | nothing | The `Screen` interface, its emulator adapter (the only importer of the emulator module), serializer, marks and the fallback line screen |
| `terminal/shellpty` | backend component | registers types `bash` and `pwsh`; injects `subprocess.Key` and the sandbox seam (A10) | The persistent shell: bootstrap, readiness, command brackets |
| `shellexec`, `shellexec/bash`, `shellexec/pwsh` | contract with registry; two provider components | key `shellexec.Key`; providers register by dialect | One-shot execution; both dialects may be mounted at once. The name `shell.Key` belongs to the page shell (C1) |
| `shellexec/env` | component | key `shellenv.Key` | The managed environment registry |
| `shellexec/tools` | four tool components | register `bash` and `pwsh`, one-shot or persistent, with the tool registry (A03) | What the model sees; a preset that omits them pays no tokens |

Catalogue names follow boot's rule ([boot.md](boot.md), `DH-ADR-BOOT-13`): `subprocess.local`, `terminal.service`, `terminal.shellpty`, `shellexec.bash`, `shellexec.pwsh`, `shellexec.env`, and `tool.bash`, `tool.pwsh`, `tool.bash-persistent`, `tool.pwsh-persistent`.

```go
// package subprocess: the seam. Every method takes a context; nothing has a default.
type Service interface {
    World() World                                                      // platform, default shell
    Lookup(ctx context.Context, name string, env Env) (Program, error) // Program{Path, Batch}
    Start(ctx context.Context, spec Spec) (Process, error)
    StartTerminal(ctx context.Context, spec TerminalSpec) (Terminal, error)
    PeerTree(c net.Conn) (Tree, error) // host key only: is the connecting local process in an Untrusted or Trusted tree, or Outside; Exact or best effort
}
type Spec struct {
    Argv           []string    // Argv[0] resolved by Lookup; never a shell string
    Dir            string
    Env            Env         // built by BuildEnv: scrubbed base, entries, removals
    Stdin          StdinMode   // Ignore | Pipe | Data
    Stdout, Stderr OutputMode  // Ignore | Pipe | Inherit | Collect{TailBytes, Spill}
    Extra          []Channel   // private inherited byte channels (launcher status)
    Launch         sandbox.Launch // A10's type: from sandbox.Service.Prepare, or sandbox.Trusted(reason); required
    Stop           StopPlan    // Ask hook, Grace, Settle
    Batch          BatchPolicy // Refuse (default) | AllowChecked (Windows)
    Detach         bool        // outlives the harness; nothing kept
    Limits         Limits      // optional memory and process-count caps, carried by the tree owner; the outcome says enforced, partial or unavailable
}
type Process interface {
    Stdin() io.WriteCloser; Stdout() io.Reader; Stderr() io.Reader
    Collected(s Stream) OutputReader             // ReadFrom(offset): non-consuming
    Exited() <-chan struct{}                     // the direct command
    Outcome() Outcome                            // ExitCode, Signal, Stop cause, Owner, Launch facts
    TreeGone(ctx context.Context) error          // the whole tree
    Stop(ctx context.Context, cause error) error // the ladder; idempotent
}
// terminal.Service: Open, Send, Read, Signal, Resize, Attach, Close, List, each taking the Owner.
// shellexec.Executor: Dialect(); Resolve(Request) (ExecSpec, error); Start(ctx, ExecSpec) (Execution, error).
```

`Terminal` is the interface of [../libs/node-pty.md](../libs/node-pty.md) §6 and `Screen` that of [../libs/xterm.md](../libs/xterm.md) §6; both are adopted unchanged. **Program lookup and the Windows command line.** Lookup is own code against the **child's** environment ([../libs/which.md](../libs/which.md)). These rules apply on Windows, where a naive port goes wrong:

1. The resolved absolute path is passed as the application name, so `CreateProcess` never searches the current or the application directory itself.
2. A bare name that resolves through `PATHEXT` to a `.bat` or `.cmd` file is refused unless the caller opts in; with opt-in it runs through `cmd.exe /d /s /c` and any argument holding `"`, `%`, `!`, CR, LF or NUL is refused ([DH-ADR-NODEPROC-1](../libs/node-process-builtins.md#dh-adr-nodeproc-1)). `npm`, `npx`, `pnpm` and `yarn` are such files.
3. An ordinary program gets `syscall.EscapeArg` quoting. A shell command string is always **one** argv element; the shell, not the harness, parses it.
4. A command line longer than 32,767 UTF-16 units is refused before anything starts, with an error that tells the caller to pass a script file.
5. The environment block is merged without regard to case (`Path` and `PATH` are one name, the later entry wins), sorted, and always keeps `SystemRoot`.
6. Pipe children are created with no window. Inference: Store "execution aliases" are reparse points that cannot be opened as files, so lookup tests existence by attributes and never opens a candidate.

**Process trees** ([DH-ADR-EXEC-2](#dh-adr-exec-2)). The rung is chosen once, when the provider starts, and is reported on every handle.

| System | Owner, strongest first | Polite stop | Forced stop | If the host is killed |
|---|---|---|---|---|
| Windows | A job object with kill-on-close whose only handle the host holds; the child is created inside it in the same `CreateProcess` call, terminals included. Fallback: create suspended, assign, resume | None for a pipe child: close its input and wait. A terminal gets the Ctrl-C byte | `TerminateJobObject` | The handle closes and the tree dies |
| Linux | A child control group per command, placed at creation (`UseCgroupFD`), inside a parent the host may write: its own cgroup v2 directory where that is writable, otherwise a scope the user service manager delegated to the host once at start. Fallback, only when no user manager answers: a process group, announced once | `SIGTERM` to every member | `cgroup.kill`, or `SIGKILL` to each member until empty | The direct child gets `SIGKILL` (`Pdeathsig`); other members survive and are reported at the next start |
| macOS | A process group; for a terminal, also the groups seen in its session | `SIGTERM` to the group | `SIGKILL` to the group | They survive |

- **Linux: one delegated scope, then limits** (U-K1-1, [DH-ADR-40](../design.md#dh-adr-40)). Inference: a host whose own cgroup is not writable (an ordinary login over SSH sits in a session scope the system owns) cannot create a child group there. When the provider starts, it therefore asks the user service manager once for a transient scope with delegation that contains the host process, by running `systemd-run --user --scope` through `proc` as a trusted launch with a deadline, and creates every per-command group inside that scope. If no user manager answers within the deadline (no systemd, no user session, a container), the rung is the process group, announced once and reported on every handle. Nothing is asked per command and no command is ever wrapped in `systemd-run`. Whether a small D-Bus client would be better than running the program is a later choice; the program needs no module to verify. `Spec.Limits` is carried out by the same owner object: the job object's memory and process-count limits on Windows, the control group's memory and process-count files on Linux where those controllers are delegated, nothing on macOS; a workflow run sets its default ([ptc-workflow.md](ptc-workflow.md)).
- **Stop ladder.** `Stop` runs five bounded rungs: revoke authority (the consumer's hook; a hosting tool calls `Run.RevokeScriptEnv()`), ask (the consumer's protocol request), close input, terminate, kill. Then it waits for the tree up to the settle deadline. If the tree is not confirmed gone it returns an error naming the survivors, and the handle stays registered for the next attempt. A tool that owns a process must finish this within the pipeline's `grace`, or the `Grace` its declaration sets (at most 60 s); past it the call settles as `tools/outcome-unknown` and the body is detached ([tools.md](tools.md), `DH-ADR-TOOLS-5`). A forced exit of the host (status 124) runs no ladder: what then happens to children on each system is the last column of the table above, and it is the same for an external agent runtime or any other child. The host handles `SIGTERM`, `SIGINT` and `SIGHUP` by unloading the tree, which runs the same ladder for every child.
- **Output capture.** A ring per collected stream keeps the tail and cuts on a character boundary; readers hold their own byte offsets. The spill writer comes from the storage area and is capped; a stream over the cap discards its spill and is marked truncated. Bytes are never decoded here.
- **Confined launch** ([DH-ADR-EXEC-3](#dh-adr-exec-3)). One contract, the same in [sandbox-ssh.md](sandbox-ssh.md) §6.1. A consumer resolves a `Policy` (`policy.Resolver`), asks the sandbox seam for a plan (`sandbox.Service.Prepare`) and puts the returned `sandbox.Launch` in `Spec.Launch`. `Start` calls `Launch.Wrap(spec)` (a wrapped argv and a status channel on POSIX; a token, a job and environment edits on Windows), creates the process, then calls `Launch.Confirm`, which reads the stage's `ready`, computes the `Confinement` and sends `go`. `Start` returns the handle with that `Confinement`. An error from `Confirm`, or end of file, is "the command never ran", and `Start` then calls `Launch.Release`. `proc` never decides confinement and imports only A10's contract package. A spawn whose context carries an initiating agent is refused when its `Launch` is empty or is `sandbox.Trusted(reason)`; `Trusted` is for the host's own children that read nothing the model can write (the `ssh` client, the browser opener, the keychain helper). A helper that does read such files (Git in a workspace, LibreOffice on a document) gets a `ForHelper` policy and is an untrusted tree. So does an operator hook (X5): it is confined unless the operator wrote `confine: none` for that hook source ([DH-ADR-31](../design.md#dh-adr-31)), and then it gets the reported unconfined plan of a registered helper kind, never `Trusted`, because an agent's event triggered it.
- **Which tree a process is in.** `proc` records for every tree whether its plan was trusted. `PeerTree` resolves the process behind a Unix-socket or loopback connection and answers from that record: by job membership on Windows and control-group membership on Linux, which is exact, and by process group, session and parent chain elsewhere, which is best effort. A detached launch belongs to no tree. A18 uses the answer to refuse a process started for the model at the control endpoint and the listener.
- **The screen** ([DH-ADR-EXEC-5](#dh-adr-exec-5)). Each terminal session has one pump goroutine: read the PTY, `Screen.Write`, send the returned replies to the PTY, number the chunk, offer it to attachments. The host always answers the program's terminal queries; a browser never does. The PTY and the screen are resized together under the pump's lock, within configured limits.
- **Readiness.** A send returns with one wait reason: `completed` (the end line of a bracketed command), `prompt` (the `OSC 133;D` marker, the controlled prompt text left of the cursor and, on POSIX, the shell holding the foreground group), `stdin_wait` (Linux only: the foreground group provably blocked reading the terminal), `inferred_idle` (silence), `timeout`, or `session_exit`.
- **Persistent shell commands** ([DH-ADR-EXEC-6](#dh-adr-exec-6)). At bootstrap the backend installs a function `__dsh_run` and the controlled prompt. For each command the tool writes the text to `<private dir>/<nonce>` and the per-call managed values, with the call's script environment when the pipeline issued one ([DH-ADR-EXEC-6](#dh-adr-exec-6)), to a sidecar file the function reads and deletes, takes a screen mark, and types only `__dsh_run <nonce>`. The function prints a start line, exports the per-call values, evaluates the file in the current shell, unsets them, and prints an end line with the nonce, the exit status and the working directory. The result is the screen text between the two lines, and its `cwd` field is the directory of the end line; the context area treats a changed `cwd` as a touch of that directory and loads its instruction files (U-K1-10). A `cd` that begins and ends inside one command is seen by nobody outside the shell, a platform limit.

### 6.2 Contracts

**Live events** (notify form, [../design.md §4.6](../design.md#46-event-model)); upstream has none here. `subprocess.ProcessStarted` and `subprocess.ProcessEnded` carry program base name, owner kind, initiator, duration and exit facts, never argv or environment. `terminal.SessionChanged` (opened, resized, exited, closed) is dispatched through the owner's scope. The area consumes the sandbox area's mode-change interception to deny a change while the owner has a terminal. **Persisted data:** none of its own. Text sent and returned is in the tool events (A03); a spill reference is a storage-area value stored before the event that names it.

**Config.** Each struct is a Go type with a default value; decoding is strict. The provider adds `subprocess/local.Config`: `ScrubDeny`, `ScrubAllow` (name patterns) and `Settle` (5 s).

| Upstream unit | Go config | Fields (default) |
|---|---|---|
| `UP-CFG-dsh-bash-local`, `UP-CFG-dsh-bash-sandbox` | `shellexec/bash.Config` | `Path` (resolved), `Timeout` (120 s), `MaxTimeout` (600 s), `TailBytes` (64,000), `SpillBytes` (64 MiB), `Grace` (3 s) |
| `UP-CFG-dsh-pwsh-local`, `UP-CFG-dsh-pwsh-sandbox` | `shellexec/pwsh.Config` | the same, plus `AllowWindowsPowerShell` (true) |
| `UP-CFG-dsh-shell-env` | none | `dshHome` is a boot fact, not a setting; the registry reads it from the boot area |
| `UP-CFG-dsh-terminal-bash` | `terminal/shellpty.Config` | `Dialect`, `Path`, `Args`, `Rows` (40), `Cols` (160), `ScrollbackRows` (10,000), `ReadBytes` (256 KiB), `SendTimeout` (30 s), `IdleSilence` (3 s), `HandoffGrace` (500 ms), `PromptTailGrace` (0), `CloseGrace` (3 s), `ConsoleHostDir` (empty). Upstream's poll and probe intervals have no counterpart (the pump reacts to output), and its byte cap on scrollback is replaced by rows times columns |
| `UP-CFG-dsh-tool-bash`, `UP-CFG-dsh-tool-pwsh` | `shellexec/tools.OneShotConfig` | `EnableBackground` (true), `PromoteOnTimeout` (true), `MaxBackgroundRuntime` (none) |
| `UP-CFG-dsh-tool-bash-persistent`, `UP-CFG-dsh-tool-pwsh-persistent` | `shellexec/tools.PersistentConfig` | `Backend`, `Timeout` (300 s), `MaxTimeout` (600 s), `OutputBytes` (16,000), `Description` |

**Services.** `UP-SVC-subprocess` becomes `subprocess.Key`; `UP-SVC-terminals` becomes `terminal.Key`; `UP-SVC-shell` becomes `shellexec.Key` (a registry of `Executor` by dialect); `UP-SVC-shellEnv` becomes `shellenv.Key`. **Tools.** Argument names are upstream's; area A03 may normalise them.

| Upstream unit | Go tool | Arguments | Change |
|---|---|---|---|
| `UP-TOOL-dsh-tool-bash.bash`, `UP-TOOL-dsh-tool-pwsh.pwsh` | `bash`, `pwsh` (one-shot) | `description`, `command`, `timeoutMs`, `workdir`; `run_in_background` with a job registry; `sandbox_permissions` and `justification` while a confining policy applies (contract owned by A10) | Structured result; the description states the resolved shell and edition |
| `UP-TOOL-dsh-tool-bash-persistent.bash`, `UP-TOOL-dsh-tool-pwsh-persistent.pwsh` | `bash`, `pwsh` (persistent) | `command`; new: `timeoutMs`, `restart` | A preset mounts at most one component per tool name |

Every result has structured fields beside its text: `command`, `exitCode`, `signal`, `stop` (`timeout`, `cancelled`, `user`, `owner-gone`, with the budget), `cwd`, per-stream `truncated` and spill reference, `owner` kind, `sandbox` (mode, enforcement, backend, `deniedHint`), `jobId`. The text keeps upstream's markers (`[exit code: N]`, `[output truncated; …]`, `[sandbox: file access denied under <mode> mode]`), rendered from the fields. **How the two shells differ, and what carries between calls:**

| Aspect | `bash` | `pwsh` |
|---|---|---|
| Finding the program | `Path`, else `bash` on the child's `PATH`. On Windows: `Path`, else Git for Windows under `%ProgramFiles%\Git`, else `PATH`, skipping `%SystemRoot%\System32\bash.exe` and `WindowsApps`. Inference: those two start WSL, a different path namespace | `Path`, else `%ProgramFiles%\PowerShell\7\pwsh.exe`, else `pwsh` on `PATH`, else Windows PowerShell 5.1 when allowed; the edition is reported |
| One-shot argv | `bash -c <command>`; `BASH_ENV` and `ENV` removed so no start-up file runs | `pwsh -NoLogo -NoProfile -NonInteractive -Command <preamble; command>`; a command starting with `param`, `using` or `#requires` is written to a private script file and invoked after the preamble |
| Defaults in the environment | `NO_COLOR=1 TERM=dumb PAGER=cat GIT_PAGER=cat` | `NO_COLOR=1 PAGER=cat GIT_PAGER=cat` |
| Exit status | `$?` | Native exit code, else 1 for a terminating error |
| Output encoding | Bytes as written | UTF-8 pinned by the preamble; in a terminal, `chcp 65001` |
| One-shot: what carries | Nothing. The directory is `workdir` resolved against the session's current directory (area A06); the environment is rebuilt each call | The same |
| Persistent: what carries | Directory, variables, functions and jobs live in the shell. It starts in the session directory and later session changes do not move it. Each result reports the shell's directory | The same; `$env:` variables and functions persist |
| Persistent delivery | `__dsh_run` sources the file | `__dsh_run` runs `Invoke-Expression` on the file's text, which no execution policy blocks |
| Interrupt | `SIGINT` to the foreground group | The Ctrl-C byte; console-wide |
| Language mode | n/a | **Nothing in the design relies on PowerShell's reduced (ConstrainedLanguage) mode**: confinement is the token's ([sandbox-ssh.md](sandbox-ssh.md)), and a language mode is never counted as a control, since a script leaves it by starting another program. Where the mode applies anyway (a machine policy, or a temp directory PowerShell cannot write), the bootstrap and `__dsh_run` still work because they use core types only, and a command that needs more fails with PowerShell's own error. Where it is absent (Linux, macOS, an unmanaged Windows) nothing changes. Each result reports the mode the shell started in |

**What a browser's terminal island receives.** `Attach` returns first a snapshot `{seq, cols, rows, bytes, palette}` that is exactly the state after chunk `seq` (`DH-LIB-XTERM-R1`, `R4`). The `palette` section (U-K2-8) holds what a program set through colour escape sequences and has not reset: indexed colours, foreground, background and cursor, at most 256 plus three entries; the island applies it before it draws, so a shell theme survives a tab switch and a reload. The pump records these entries from the byte stream itself, so the section does not depend on what the emulator exposes. After the snapshot come output chunks numbered from `seq+1`, then an exit notice. The island sends acknowledgements, input caused by a person, and resize requests within host limits. Unacknowledged bytes per attachment are bounded; a viewer over the bound is dropped and re-attaches with a new snapshot, so a slow browser never blocks the pump. The route, its authentication and who may attach belong to area A19; the island to C6.

### 6.3 Ownership, failure, retry, rollback and cleanup

Ownership is who releases; access is who may use. The provider's fiber **owns** every process and terminal it started; a consumer holding a handle has **access** only, so a consumer that unloads first leaks nothing. A terminal session is owned by its owner's scope: the agent's fiber (`DH-ADR-ARCH-7`) or the user terminal's record. A spill file is owned by the storage area once written. A detached launch is the one thing created and deliberately not owned. Nothing in this area is retried automatically: starting a program twice is never assumed safe.

| Situation | What happens | Undone, or left uncertain |
|---|---|---|
| Lookup fails, a batch file or an over-long line is refused, the launch plan is missing | Error before anything is created | Nothing exists |
| The launcher reports `exec-failed`, or its pipe closes with no record | `Start` fails with "never ran"; the owner object is released | Nothing ran |
| The owner object cannot be created | Decided at provider start, not per command: the weaker rung is announced once and reported on every handle | No command is replayed |
| The context is cancelled or its deadline passes | The ladder runs with that cause; the outcome records it | **Uncertain:** what the command already did |
| The tree is not gone at the settle deadline | Error naming survivors; the handle stays registered; the fiber's unload completes and reports the leak | **Uncertain:** the survivor |
| A grandchild keeps a pipe open | `Exited` completes after the grace period with what was read; `TreeGone` keeps waiting | The grandchild stays owned |
| The spill store refuses a write | Collection continues in memory; the result is truncated with no reference; one error is logged | The partial spill is released |
| Terminal allocation or shell bootstrap fails | Everything opened is closed in reverse order; nothing is published | Nothing |
| The emulator panics on some output | The screen is marked unavailable; the process and raw delivery to browsers continue; text reads fail with a stable error | The session stays usable by a person |
| A persistent command times out or is cancelled | Interrupt, then wait a short grace for the end line and prompt. If they arrive the shell is kept; if not it is closed and the next call starts fresh, and the result says which | **Uncertain:** the command's own effects |
| The owner (agent) ends | Its sessions are closed and awaited before the scope reports done | A close that cannot confirm the tree gone is reported, never swallowed |
| The host dies | Windows: every tree dies with the job handle. Linux: direct children are killed, other cgroup members survive. macOS: children survive | Survivors are listed at the next start of a host on the same home; nothing is killed automatically |

## 7. Decisions

<a id="dh-adr-exec-1"></a>
### DH-ADR-EXEC-1 One process package behind the subprocess seam, under two service keys

- **Decision.** `internal/world/proc` is the only package that may import `os/exec` or call `os.StartProcess`, `syscall.ForkExec`, `syscall.StartProcess` or `windows.CreateProcess*`. Every other package reaches it through `subprocess.Service`. The local provider offers the service under `subprocess.Key` (the execution world, replaceable by a remote provider) and `subprocess.HostKey` (always this machine).
- **Why.** Upstream's rules live in one service that about two dozen packages bypass (`UP-DEBT-found.L5.5`). Go's default of inheriting the parent environment, and its batch-file quoting, make a bypass a leak or an injection.
- **Tradeoff.** PTY code and both shells' needs live in one package tree. Inference: a child that must run on the host whatever the world is (a plugin process, a local MCP server, the browser opener) uses the host key; areas A10, A15 and A22 confirm which key each child takes.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10). **Verified by.** `DH-T-EXEC-1`, `DH-T-EXEC-2`, `DH-T-NODEPROC-10`.

<a id="dh-adr-exec-2"></a>
### DH-ADR-EXEC-2 Tree owners held by the host, one stop ladder, every wait bounded

- **Decision.** The owners in the §6.1 table, chosen once at provider start; Windows terminals are created inside the job; Linux uses a control group per command, not a `systemd-run` scope per command, and where the host's own group is not writable it asks the user service manager once, at host start, for a delegated scope and creates the per-command groups inside it, falling back to process groups only when no user manager answers (the owner's choice for U-K1-1, [DH-ADR-40](../design.md#dh-adr-40)); the ladder and the settle deadline are part of the package.
- **Why.** It applies [DH-ADR-NODEPROC-2](../libs/node-process-builtins.md#dh-adr-nodeproc-2) and settles what that record left to this area. A cgroup places the child atomically and needs no helper; upstream's scope launcher is 730 lines and probes per spawn (`packages/subprocess/subprocess-local/README.md:150`).
- **Tradeoff.** One start-time dependency on a running user manager and one more start-up path to test; the scope request can fail or time out, and then the weaker rung is used and said. With it the design covers what upstream's per-command scope covered, keeps "no probe per command", makes `PeerTree` exact and `Spec.Limits` available on an ordinary SSH login. macOS stays weak. A bounded wait means the harness can report a survivor instead of proving it gone. Jobs and terminals still end with the host, because the owners are held by the host process on purpose; a "restart keeps jobs" story would need a supervisor process and is not designed.
- **Serves.** [DH-O-1](../design.md#dh-o-1), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-EXEC-4`, `DH-T-EXEC-5`, [DH-E-EXEC-1](#dh-e-exec-1) to [DH-E-EXEC-3](#dh-e-exec-3), [DH-E-EXEC-10](#dh-e-exec-10).

<a id="dh-adr-exec-3"></a>
### DH-ADR-EXEC-3 Confinement is a required field of the spawn, not a choice of provider

- **Decision.** There is one executor per dialect and one terminal backend. Each asks the sandbox seam for a launch plan per call and cannot start a process for an agent without one. The four upstream sandbox and local executors merge into two.
- **Why.** Upstream confines only when the operator mounted the sandboxing package; the default packages run with the host's authority (`packages/shell/bash-local/README.md:139`). [DH-O-7](../design.md#dh-o-7) says a command runs under the resolved policy or does not run, which must not depend on composition.
- **Tradeoff.** The process package depends on the sandbox area's plan type, and an unconfined run is something the sandbox seam must return, not something a caller can assume.
- **Serves.** [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-EXEC-8`, [DH-E-EXEC-8](#dh-e-exec-8).

<a id="dh-adr-exec-4"></a>
### DH-ADR-EXEC-4 The caller's context is the only deadline, and the outcome records why a run was stopped

- **Decision.** The seam has no timer of its own. A deadline or cancellation arrives through `context.Context` with a typed cause (`DH-ADR-ARCH-3`). `Outcome.Stop` records the cause of the first stop; results show it as `stop`.
- **Why.** Upstream fuses an executor timer with the caller's signal and classifies afterwards; on Windows a killed process is "exit 1" with no signal (`packages/shell/pwsh-local/README.md:149`), and the persistent tools report "timed out or OOM".
- **Tradeoff.** A kill from outside the harness still looks like an ordinary exit on Windows.
- **Serves.** [DH-O-3](../design.md#dh-o-3), [DH-O-6](../design.md#dh-o-6). **Verified by.** `DH-T-EXEC-6`.

<a id="dh-adr-exec-5"></a>
### DH-ADR-EXEC-5 The model reads the emulated screen, the host always answers the program, and the emulator has a gate and a fallback

- **Decision.** One `Screen` per session serves replies, the model's text, readiness and the browser snapshot. The module choice of [DH-ADR-XTERM-1](../libs/xterm.md#dh-adr-xterm-1) stays **provisional** until the gate test `DH-T-XTERM-9` passes. This area fixes the gate. *Corpus:* recorded output of `bash`, `zsh`, `pwsh` through ConPTY on Windows 11 and Server 2022, `git log` in a pager, `less`, a progress bar, wide and combining characters (the "must" tier), and `vim` and `top` (the "should" tier). *Reference:* grids produced by xterm.js 6.0.0 and committed as golden files. *Threshold:* in the must tier, every recording equal in text, cursor, wrapped rows and showing screen; in the should tier, text and cursor equal, with attribute differences recorded. *Fallback ladder:* (1) `go-headless-term`; (2) `github.com/charmbracelet/x/vt` behind the same interface; (3) an own line screen with upstream's capability (no alternate screen, replies to status and attribute queries only) while the owner decides on the cgo emulator.
- **Why.** Upstream has two answers to "what is on the screen" and a byte comparison that forced it to ship a console host ([../libs/xterm.md](../libs/xterm.md) §3; `UP-DEBT-found.L5.2`).
- **Tradeoff.** The model's text depends on a young module; rung 3 keeps the product working without full-screen reads. Terminals now run with `TERM=xterm-256color`, so the shell tools keep `NO_COLOR` and the pager overrides.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-EXEC-9`, `DH-T-XTERM-1` to `DH-T-XTERM-9`, [DH-E-EXEC-6](#dh-e-exec-6), [DH-E-EXEC-9](#dh-e-exec-9).

<a id="dh-adr-exec-6"></a>
### DH-ADR-EXEC-6 Persistent commands are delivered by file, bracketed by a nonce, and a doubtful shell is interrupted before it is reset

- **Decision.** The mechanism of §6.1. A command is accepted only when the previous one has printed its end line. On timeout or cancellation the tool interrupts the foreground group and keeps the shell only if the end line and the prompt then appear (on POSIX also: the shell's group is the foreground group).
- **Why.** Typing the command is the cause of five upstream limitations: control characters eaten by the line editor, echoed input in results, raw ESC unsupported, a prompt redefinition breaking readiness, and a child swallowing the next command. The script environment of the tool command line is defined by [tools.md](tools.md) (`DH-ADR-TOOLS-6`: `DSH_TOOL_ENDPOINT` and `DSH_TOOL_TOKEN`, minted by the pipeline for a root call of a tool that declares `HostsScripts`, deleted when that call leaves execution). It cannot reach a shell that is already running through a spawn environment, so the shell tools declare `HostsScripts: call`, take the script environment from `Run.ScriptEnv()`, and the persistent ones deliver it in the sidecar file, which is never typed or shown; a one-shot command gets it in its process environment. The first rung of the stop ladder calls `Run.RevokeScriptEnv()`, and a command moved to the background keeps no token. That names the outer call tools.md asks for: a nested call from a persistent shell belongs to the persistent-tool call whose command is running. The terminal session itself holds no token: a raw send, a person typing and a job left behind in the shell are refused by the endpoint.
- **Tradeoff.** One or two small files per command, which a remote world must write through the filesystem seam. A model can redefine `__dsh_run`; that ends in a timeout and a reset.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-EXEC-11`, `DH-T-EXEC-16`, [DH-E-EXEC-4](#dh-e-exec-4) to [DH-E-EXEC-6](#dh-e-exec-6).

<a id="dh-adr-exec-7"></a>
### DH-ADR-EXEC-7 Shells are a registry by dialect, resolved by stated rules on each system, with no translation

- **Decision.** `shellexec.Key` holds executors by dialect; `bash` and `pwsh` may both be mounted on any system where the program is found by the rules of §6.2. A tool whose shell is not found reports `unavailable`.
- **Why.** Upstream has one slot and hard-codes `bash` as POSIX-only (`packages/shell/bash-local/README.md:141`), though Git Bash on Windows and PowerShell on Linux are common.
- **Tradeoff.** Two shell tools can be visible at once and the model must pick. Inference: Git Bash re-parses its command line with its own runtime, so the quoting table must be proven through it ([DH-E-EXEC-7](#dh-e-exec-7)).
- **Serves.** [DH-O-10](../design.md#dh-o-10). **Verified by.** `DH-T-EXEC-13`, [DH-E-EXEC-7](#dh-e-exec-7).

<a id="dh-adr-exec-8"></a>
### DH-ADR-EXEC-8 The child environment is built explicitly; managed variables are declared, described and listed completely

- **Decision.** `BuildEnv` starts from the host environment, removes names matching the deny patterns (upstream's four plus `PASSPHRASE` and `CREDENTIAL`, extendable), removes every name the configuration uses as a credential reference, keeps names on the allow list, removes every `DSH_*` name, then adds caller entries and finally the managed snapshot. Contributors declare keys with a one-line description; built-ins are the first contributor; `List` is complete and feeds the generated catalog. `DSH_NODE_EXECUTABLE` becomes `DSH_EXECUTABLE`. No child ever receives the control socket's path, a launch link or a browser session; `DSH_TOOL_ENDPOINT` and `DSH_TOOL_TOKEN` are set only from the script environment a hosting tool's body passes, and the token is masked in captured output.
- **Why.** [DH-ADR-SECURITY-6](../upstream/security.md#dh-adr-security-6) requires one helper to build every child environment. The proposed upstream note would delete the list; here it has a reader.
- **Tradeoff.** The scrub is still a name heuristic for secrets the harness does not know.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-EXEC-3`, `DH-T-EXEC-14`.

<a id="dh-adr-exec-9"></a>
### DH-ADR-EXEC-9 A terminal has one owner, starts only on request, needs a pseudo-terminal, and a person may take it over

- **Decision.** Recommended default, not confirmed by the owner; it records as scope what the deliverables already do. (a) **One owner.** A terminal session belongs to exactly one agent or one person; no other agent may read, send or signal, and there is no sharing between agents. (b) **Control state.** An agent's terminal has a control state, `agent` or `person`. The first typed input from an attached person sets it to `person`; while it is, the agent's `Send` and `Signal` are refused with `terminal/person-control`, and the person hands it back explicitly. A terminal taken over keeps its launch plan, stays an untrusted tree, and still counts for the sandbox-mode fence of §6.2. (c) **Only on request.** No terminal is declared in configuration or started at boot: it needs an owner, which exists only once an agent or a person asks. (d) **A real pseudo-terminal.** Where none can be allocated the persistent tools report `unavailable`; they never fall back to pipes. (e) **No bell.** The terminal bell is not reported to the model. (f) **Ends with the host.** A session does not survive a restart of `dsh`.
- **Why.** The owner is what gives a terminal a lifetime and an access rule in one fact. A shell on pipes behaves differently enough that a silent fallback would be a different tool under the same name. Nothing consumes a bell.
- **Tradeoff.** Two agents cannot cooperate in one shell; a person watching an agent's terminal must take it over to type. A restart for an update ends the user's shells and dev servers.
- **Serves.** [DH-O-1](../design.md#dh-o-1), [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-EXEC-10`, [DH-E-EXEC-9](#dh-e-exec-9).

## 8. Deliverables

<a id="dh-d-exec-1"></a>
### DH-D-EXEC-1 One package starts every program, from an argument list, behind the subprocess seam

- **Result and why it matters.** Any component can start a program and gets the same lookup, quoting, environment and ownership rules; nothing else in the binary can start one.
- **How it is implemented.** `proc` over `os/exec` on POSIX and its own `CreateProcess` call on Windows (needed for the job and pseudoconsole attributes); own lookup against the child's environment; the six Windows rules of §6.1; `BuildEnv`; generic extra channels (`Cmd.ExtraFiles`, inherited handles on Windows); `Detach`; a `Run` helper for run-and-capture ([../libs/execa.md](../libs/execa.md)); a source check in CI for the forbidden imports and calls. **Components and files.** `internal/world/proc`, `internal/world/subprocess`, `internal/world/subprocess/local`.
- **Depends on.** [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1) (complete unload); [DH-D-UTIL-6](util-test-support.md#dh-d-util-6) (test kit). Runtime: none.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10). **Priority.** P0.
- **Done when.** `DH-T-EXEC-1` to `DH-T-EXEC-3`, `DH-T-NODEPROC-1` to `-4`, `DH-T-NODEPROC-10`, `DH-T-WHICH-1`, `DH-T-EXECA-1` to `-3` and `DH-T-CROSSSPAWN-1` pass, and [DH-E-EXEC-7](#dh-e-exec-7) has passed.

<a id="dh-d-exec-2"></a>
### DH-D-EXEC-2 A whole process tree is stopped and awaited on every system, within a bound

- **Result and why it matters.** Cancelling a command leaves nothing it started, and a stuck child cannot hang the harness.
- **How it is implemented.** Job object with the job-list attribute; the parent group chosen once at start (the host's own, or a delegated scope obtained from the user service manager with a deadline); a cgroup directory per child with `UseCgroupFD`, `cgroup.kill` and the `populated` event; process groups; children started from one locked thread so `Pdeathsig` is tied to the provider's life; the five-rung ladder; the settle deadline; host signal handlers; a runtime file listing live owners so a later host can report leftovers; the trusted-or-untrusted mark on every tree and `PeerTree` over it (peer credentials of a Unix socket, the owner of a loopback connection, then job or control-group membership); the optional `Limits`, set on the job or control group and reported. **Components and files.** `proc` (owner files per system), `subprocess/local`.
- **Depends on.** [DH-D-EXEC-1](#dh-d-exec-1), [DH-D-CORDIS-2](../libs/cordis.md#dh-d-cordis-2) (lifetime context). Runtime: cgroup v2 for the strong rung on Linux.
- **Serves.** [DH-O-1](../design.md#dh-o-1), [DH-O-7](../design.md#dh-o-7). **Priority.** P0.
- **Done when.** `DH-T-EXEC-4` to `-6`, `DH-T-EXEC-17` and `DH-T-NODEPROC-5` to `-8` pass, and [DH-E-EXEC-1](#dh-e-exec-1), [DH-E-EXEC-2](#dh-e-exec-2), [DH-E-EXEC-3](#dh-e-exec-3) and [DH-E-EXEC-10](#dh-e-exec-10) have passed.

<a id="dh-d-exec-3"></a>
### DH-D-EXEC-3 Output is captured within fixed limits and the rest is recoverable

- **Result and why it matters.** No command can exhaust host memory or flood the model, and truncated output can still be read.
- **How it is implemented.** A ring per stream that keeps the tail and cuts on a character boundary; whole-stream byte offsets with non-consuming readers; an optional spill writer obtained from the storage area's spill store, capped, discarded when the cap is exceeded; `Run` with a hard limit that stops the tree. **Components and files.** `proc` (collector), `subprocess/local`.
- **Depends on.** [DH-D-EXEC-1](#dh-d-exec-1); [DH-D-STORAGE-5](storage.md#dh-d-storage-5) (a spill writer and reference with retention). The model-visible result is shaped afterwards by the tool pipeline, which uses this and is not a dependency of it.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-14](../design.md#dh-o-14). **Priority.** P0.
- **Done when.** `DH-T-EXEC-7` and `DH-T-NODEPROC-9` pass.

<a id="dh-d-exec-4"></a>
### DH-D-EXEC-4 A command for an agent starts under its launch plan or does not start

- **Result and why it matters.** Confinement cannot be skipped by composition, and "the sandbox failed" is never mistaken for "the command failed".
- **How it is implemented.** The required `Spec.Launch` field and its check against the context's initiator; `Wrap`, start, `Confirm`, and `Release` on failure, in that order and nowhere else; `Start` blocks until `Confirm` returns; the `Confinement` is copied to the outcome. A fake `sandbox.Launch` drives the local tests. **Components and files.** `proc`, `subprocess`, with the executors and the terminal backend as callers.
- **Depends on.** [DH-D-EXEC-1](#dh-d-exec-1); [DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1) (the contract package: `Policy`, `Launch`, `Confinement`). The seam, the stage and the backends of sandbox-ssh.md depend on this deliverable, not the reverse.
- **Serves.** [DH-O-7](../design.md#dh-o-7). **Priority.** P0.
- **Done when.** `DH-T-EXEC-8` passes and [DH-E-EXEC-8](#dh-e-exec-8) has passed on each backend sandbox-ssh.md ships.

<a id="dh-d-exec-5"></a>
### DH-D-EXEC-5 Pseudo-terminals on Linux, macOS and Windows from the product binary alone

- **Result and why it matters.** Interactive programs run with no native addon, helper file or compiler ([DH-ADR-15](../design.md#dh-adr-15)).
- **How it is implemented.** As [DH-ADR-NODEPTY-1](../libs/node-pty.md#dh-adr-nodepty-1) specifies, inside `proc`: `/dev/ptmx` and `ioctl` on POSIX with a new session; pipes, `CreatePseudoConsole` and an attribute list on Windows, in the same call that places the process in its job; one reader goroutine; ordered, bounded close that drains output until end of stream (`DH-LIB-NODEPTY-R10`); an operator-configured console host directory, never a bundled one. **Components and files.** `proc` (PTY files per system).
- **Depends on.** [DH-D-EXEC-1](#dh-d-exec-1), [DH-D-EXEC-2](#dh-d-exec-2). Runtime: Windows 10 version 1809 or later.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-14](../design.md#dh-o-14). **Priority.** P1.
- **Done when.** `DH-T-NODEPTY-1` to `-9` pass, and [DH-E-EXEC-4](#dh-e-exec-4), [DH-E-EXEC-5](#dh-e-exec-5) and [DH-E-EXEC-6](#dh-e-exec-6) have passed.

<a id="dh-d-exec-6"></a>
### DH-D-EXEC-6 A host-side screen that answers programs, yields text and restores a browser

- **Result and why it matters.** The model can read what a person would see, including a full-screen page, and a browser tab restores at once.
- **How it is implemented.** The `Screen` interface, the adapter, the serializer and the marks of [../libs/xterm.md](../libs/xterm.md); the golden-file gate of [DH-ADR-EXEC-5](#dh-adr-exec-5), run before anything depends on the adapter; the second adapter and the line screen written only if the gate demands them; one contract suite every implementation must pass. **Components and files.** `internal/world/terminal/screen`.
- **Depends on.** No deliverable. The golden grids are committed files here; regenerating them from xterm.js is a browser job of the terminal island in ui-sidebar-workspace.md, which uses this screen and is not a dependency of it.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-12](../design.md#dh-o-12). **Priority.** P1.
- **Done when.** `DH-T-EXEC-9` and `DH-T-XTERM-1` to `-9` pass, the gate's outcome (which rung) is recorded, and [DH-E-EXEC-9](#dh-e-exec-9) has passed.

<a id="dh-d-exec-7"></a>
### DH-D-EXEC-7 Owner-scoped terminal sessions with a persistent shell backend and a browser feed

- **Result and why it matters.** An agent or a person keeps a terminal across calls; only its owner can use it; it is closed with its owner.
- **How it is implemented.** A registry keyed by owner; publish after backend setup; one send at a time; a pump goroutine per session that owns the send state; the readiness tiers of §6.1; idle or busy from the foreground group; the mode-change fence; `Attach` with acknowledgement-bounded queues; a bootstrap per dialect that uses only core types on PowerShell. **Components and files.** `internal/world/terminal`, `terminal/shellpty`.
- **Depends on.** [DH-D-EXEC-4](#dh-d-exec-4), [DH-D-EXEC-5](#dh-d-exec-5), [DH-D-EXEC-6](#dh-d-exec-6), [DH-D-CORDIS-3](../libs/cordis.md#dh-d-cordis-3) (scoped dispatch); [DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1) (`sandbox.ModeChanging`). Its consumers, which depend on it, are the terminal route of api.md and the `terminal_*` tools of x-hooks-mods-terminal.md.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-1](../design.md#dh-o-1), [DH-O-12](../design.md#dh-o-12). **Priority.** P1.
- **Done when.** `DH-T-EXEC-10` to `-12` pass, and [DH-E-EXEC-4](#dh-e-exec-4), [DH-E-EXEC-5](#dh-e-exec-5), [DH-E-EXEC-6](#dh-e-exec-6) and [DH-E-EXEC-9](#dh-e-exec-9) have passed.

<a id="dh-d-exec-8"></a>
### DH-D-EXEC-8 Bash and PowerShell executors with a managed environment

- **Result and why it matters.** Any component can run a shell command with explicit budgets and get a classified, bounded result on every system.
- **How it is implemented.** `Resolve` applies defaults and caps; `Start` builds the dialect's argv, asks for the launch plan and calls the subprocess service; one execution serves the foreground result and background reads; an explicit `Failed` outcome; program resolution per §6.2; the script-file path for PowerShell first-statement commands; the environment registry with declared keys. **Components and files.** `internal/world/shellexec`, `shellexec/bash`, `shellexec/pwsh`, `shellexec/env`.
- **Depends on.** [DH-D-EXEC-1](#dh-d-exec-1), [DH-D-EXEC-2](#dh-d-exec-2), [DH-D-EXEC-3](#dh-d-exec-3), [DH-D-EXEC-4](#dh-d-exec-4); [DH-D-VIEW-10](session-views.md#dh-d-view-10) (the session's current directory); [DH-D-HUMAN-7](interaction-credentials.md#dh-d-human-7) (`Store.References()`, the names configuration uses as credential references).
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7). **Priority.** P0.
- **Done when.** `DH-T-EXEC-3`, `DH-T-EXEC-13` and `DH-T-EXEC-14` pass and [DH-E-EXEC-7](#dh-e-exec-7) has passed.

<a id="dh-d-exec-9"></a>
### DH-D-EXEC-9 One-shot `bash` and `pwsh` tools

- **Result and why it matters.** The model runs a command and learns how it ended, from fields that output cannot forge.
- **How it is implemented.** Tool components that build a request from named arguments only (the model cannot set stdin or environment), collect the managed environment, register the run as a job when a registry is present (output goes to `jobs.Handle.Append` and `Spill`) and promote it on timeout, and return the structured result of §6.2. **Components and files.** `internal/world/shellexec/tools`.
- **Depends on.** [DH-D-EXEC-8](#dh-d-exec-8); [DH-D-TOOLS-1](tools.md#dh-d-tools-1) and [DH-D-TOOLS-2](tools.md#dh-d-tools-2) (tool declaration, structured result); [DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1) (`Resolver.Escalate` and the two escalation arguments). When a job registry is mounted the tools use it; they are built and pass their proof without one, so the jobs area is not a dependency.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-7](../design.md#dh-o-7). **Priority.** P0.
- **Done when.** `DH-T-EXEC-15` passes and [DH-E-EXEC-8](#dh-e-exec-8) has passed.

<a id="dh-d-exec-10"></a>
### DH-D-EXEC-10 Persistent `bash` and `pwsh` tools

- **Result and why it matters.** Work that depends on shell state (an activated environment, a changed directory) continues across calls, per agent.
- **How it is implemented.** One session per owner, opened lazily; a per-owner queue; file delivery and brackets ([DH-ADR-EXEC-6](#dh-adr-exec-6)); the result from the screen between marks, bounded, with an honest notice when the start scrolled out of history; interrupt, verify, else reset; `restart`; the script environment in the sidecar. **Components and files.** `internal/world/shellexec/tools`, using `terminal`.
- **Depends on.** [DH-D-EXEC-7](#dh-d-exec-7); [DH-D-TOOLS-4](tools.md#dh-d-tools-4) (the script environment of a hosting call); [DH-D-FS-4](filesystem.md#dh-d-fs-4) (writing the command file in whichever world the session uses).
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-6](../design.md#dh-o-6). **Priority.** P1.
- **Done when.** `DH-T-EXEC-11` and `DH-T-EXEC-16` pass, and [DH-E-EXEC-4](#dh-e-exec-4), [DH-E-EXEC-5](#dh-e-exec-5) and [DH-E-EXEC-6](#dh-e-exec-6) have passed.

## 9. Acceptance

### 9.1 Local tests

The tests of the library specifications (`DH-T-NODEPROC-*`, `DH-T-NODEPTY-*`, `DH-T-XTERM-*`, `DH-T-WHICH-1`, `DH-T-EXECA-*`, `DH-T-CROSSSPAWN-1`) are part of this area's proof and are not repeated. A test with a fake is a different kind of evidence from a scenario in §9.2. Nothing has been run.

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-EXEC-1` | D-1 | A source check fails when any non-test file outside `internal/world/proc` imports `os/exec` or calls a process-creation function |
| `DH-T-EXEC-2` | D-1 | Windows lookup and line rules with a fake file system: absolute application name, batch refused, over-long line refused, a candidate is never opened; a relative path is refused on every system |
| `DH-T-EXEC-3` | D-1, D-8 | `BuildEnv`: deny, allow, exact credential names, `DSH_*` removed then managed values last; case-insensitive merge and sorted block on Windows |
| `DH-T-EXEC-4` | D-2 | With each owner made unavailable, the next rung is chosen once, announced once and reported on the handle; nothing is started twice. With a fake user manager: an unwritable own group leads to one scope request at start and groups inside the granted scope; a refusal or a timeout leads to the process-group rung; no request is made per command |
| `DH-T-EXEC-5` | D-2 | The ladder runs its rungs in order, each bounded; past the settle deadline `Stop` names survivors and the handle stays registered; a fiber unload stops every child |
| `DH-T-EXEC-6` | D-2, D-8 | Timeout, cancellation and a user stop each give their own first cause; with no signal available the cause is still present |
| `DH-T-EXEC-7` | D-3 | Tail and truncation flag; independent offset readers; the spill cap; a failing spill store degrades to memory; the cut never splits a character |
| `DH-T-EXEC-8` | D-4 | A spawn for an agent with no plan, or with `sandbox.Trusted`, is refused; with a fake `sandbox.Launch`, `Wrap`, start and `Confirm` run in that order; an error or end of file from `Confirm` gives "never ran" and calls `Release`; a background start waits for `Confirm` |
| `DH-T-EXEC-9` | D-6 | One contract suite passes for every `Screen` implementation; the line screen passes its reduced suite; the selected rung is reported |
| `DH-T-EXEC-10` | D-7 | With a fake backend: foreign owner refused, second send refused, publish only after setup, owner end closes and awaits, mode change denied while a session is open, also for a terminal a person took over; a person's first typed input sets `person` control, the agent's `Send` and `Signal` are then refused, and a hand-back restores them |
| `DH-T-EXEC-11` | D-7, D-10 | With a scripted PTY: the end line of another nonce does not settle a send; a prompt drawn in two chunks and in two byte forms is recognised; a busy shell refuses; interrupt then verify keeps the shell, otherwise resets |
| `DH-T-EXEC-12` | D-7 | An attachment gets a snapshot equal to the state at `seq`, then chunks in order; a recorded stream that sets and resets palette entries yields exactly the entries still set, within the bound; an unacknowledging viewer is dropped and the pump never blocks; only the host answers a cursor query |
| `DH-T-EXEC-13` | D-8 | Argv per dialect; the PowerShell first-statement rule; resolution order on a fake Windows and POSIX file system, including the skipped WSL launcher; the explicit `Failed` outcome |
| `DH-T-EXEC-14` | D-8 | Registry: duplicate or reserved key refused, undeclared output refused, the list includes built-ins, a contributor's entries leave with its fiber |
| `DH-T-EXEC-15` | D-9 | Through the real tool pipeline with a fake executor: schema per composition, structured fields, markers rendered from fields, promotion to a job on timeout |
| `DH-T-EXEC-16` | D-10 | With a fake terminal: one shell per owner, serialised commands, directory in the result, the script environment present only while its command runs and never on the screen, none for a raw send, `restart` |
| `DH-T-EXEC-17` | D-2 | With fake owners and a fake peer lookup: a process in a tree started under a confining or unconfined plan is `Untrusted`, the host's own helper is `Trusted`, a detached launch and an unrelated process are `Outside`; job and control-group owners answer `Exact`, the process-group rung answers best effort; no child environment contains the control socket's path; a `Limits` value is applied by the job and control-group owners and reported `unavailable` by the process-group rung |

### 9.2 Real-environment scenarios

Each runs the built `dsh` binary against real processes on the named system. Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

<a id="dh-e-exec-1"></a>
#### DH-E-EXEC-1 Linux: a whole process tree is killed (control group)

- **Proves.** [DH-D-EXEC-2](#dh-d-exec-2).
- **Given.** A Linux host with cgroup v2 where the test user's cgroup is writable; a fixture program that starts a child, which starts a grandchild that calls `setsid`, ignores `SIGTERM` and writes its pid to a file.
- **When.** The one-shot `bash` tool runs the fixture with a 2 s timeout. Then the same run is repeated and the `dsh` process is sent `SIGKILL` mid-run. Then it is repeated on a host with no writable cgroup and no user service manager (a container without systemd).
- **Then.** After the timeout no fixture pid exists, the result says `stop: timeout` and `owner: cgroup`, and the child cgroup directory is removed. After `SIGKILL` the direct child is gone, and the next `dsh` start on that home lists the surviving members. Without a cgroup the result says `owner: process-group` and one warning was logged.
- **Cleanup.** Kill listed pids and remove leftover cgroup directories; keep the runtime owner file if removal fails.

<a id="dh-e-exec-2"></a>
#### DH-E-EXEC-2 macOS: a process group is killed and the documented gap is observed

- **Proves.** [DH-D-EXEC-2](#dh-d-exec-2).
- **Given.** A macOS host and the fixture of [DH-E-EXEC-1](#dh-e-exec-1).
- **When.** The tool runs the fixture with a 2 s timeout, once with a grandchild that stays in the group and once with one that calls `setsid`.
- **Then.** The in-group tree is gone and the result says `owner: process-group`. The detached grandchild survives, exactly as documented; the one-time warning names the weaker owner.
- **Cleanup.** Kill the recorded pids.

<a id="dh-e-exec-3"></a>
#### DH-E-EXEC-3 Windows: a job object ends the tree, also when the host dies

- **Proves.** [DH-D-EXEC-2](#dh-d-exec-2).
- **Given.** Windows 11 and Windows Server 2022; a fixture that starts a grandchild with a new process group and asks to break away from its job.
- **When.** The one-shot `pwsh` tool runs the fixture with a 2 s timeout. Then again, and `dsh` is ended with `taskkill /F` mid-run.
- **Then.** In both runs no fixture process remains within the settle deadline; the breakaway request failed; the first result says `stop: timeout`, `owner: job`, with no signal; no console window appeared.
- **Cleanup.** `taskkill` any recorded pid.

<a id="dh-e-exec-4"></a>
#### DH-E-EXEC-4 Linux: a persistent shell keeps state, survives an interrupt and is closed with its owner

- **Proves.** [DH-D-EXEC-5](#dh-d-exec-5), [DH-D-EXEC-7](#dh-d-exec-7), [DH-D-EXEC-10](#dh-d-exec-10).
- **Given.** A Linux host with bash; a session with the persistent `bash` tool, replayed without a model key.
- **When.** Calls in order: `cd` to a temp directory and `export X=1`; print both; a heredoc holding TAB and ESC bytes written to a file; `sleep 600` with a 2 s timeout; print `X` again; `vim` on a file through a raw send, a read, then quit; the agent is disposed.
- **Then.** State persists; the file holds the exact bytes; the timed-out call says the command was interrupted and the shell kept, and `X` is still `1`; the read returns the editor's page; after disposal no shell or child process exists and the command files are gone.
- **Cleanup.** Remove the temp directory; kill recorded pids.

<a id="dh-e-exec-5"></a>
#### DH-E-EXEC-5 macOS: the same persistent shell without a control group

- **Proves.** [DH-D-EXEC-5](#dh-d-exec-5), [DH-D-EXEC-7](#dh-d-exec-7), [DH-D-EXEC-10](#dh-d-exec-10).
- **Given.** A macOS host with its system bash and zsh.
- **When.** The steps of [DH-E-EXEC-4](#dh-e-exec-4), with each shell as the backend program.
- **Then.** As in [DH-E-EXEC-4](#dh-e-exec-4); in addition, every command settles as `completed`, never `inferred_idle`.
- **Cleanup.** As in [DH-E-EXEC-4](#dh-e-exec-4).

<a id="dh-e-exec-6"></a>
#### DH-E-EXEC-6 Windows: a persistent PowerShell on the system console host

- **Proves.** [DH-D-EXEC-5](#dh-d-exec-5), [DH-D-EXEC-7](#dh-d-exec-7), [DH-D-EXEC-10](#dh-d-exec-10).
- **Given.** Windows 11 and Windows Server 2022 (or Windows 10 22H2), with no console host files beside the binary; pwsh 7 and, separately, Windows PowerShell 5.1; once more under the sandbox's read-only mode (area A10).
- **When.** Seven commands, one printing 500 lines so the prompt is repainted; a command holding a raw ESC; non-ASCII output from a native program; `Start-Sleep 600` with a 2 s timeout; a redefinition of `prompt` followed by another command; close.
- **Then.** Every command settles as `completed`, none on silence; the ESC and the non-ASCII text arrive intact; the interrupt keeps the shell; the redefined prompt does not break completion; in read-only mode the bootstrap succeeds; close returns within its deadline on both Windows versions and leaves no shell or console host process.
- **Cleanup.** `taskkill` recorded pids; keep the per-send readiness timeline if a step fails.

<a id="dh-e-exec-7"></a>
#### DH-E-EXEC-7 Windows: command resolution and quoting through the real shells

- **Proves.** [DH-D-EXEC-1](#dh-d-exec-1), [DH-D-EXEC-8](#dh-d-exec-8).
- **Given.** Windows with pwsh 7, Windows PowerShell 5.1, Git for Windows, WSL installed, and Node's `npm.cmd` on `PATH`.
- **When.** A table of command strings (spaces, double and single quotes, trailing backslashes, `%NAME%`, `&`, `^`, non-ASCII, 40,000 characters) is run through the `pwsh` and `bash` tools so that each prints its input back; `npm` is started by bare name through the process package with a plain argument and with a `%` argument; `bash` is resolved with and without Git for Windows; non-ASCII text is passed on standard input to Windows PowerShell 5.1 started with its console input code page set to UTF-8 beforehand (the one fact U-K1-12 asks of this area).
- **Then.** Every string within the length limit prints back exactly through all three shells; the over-long one is refused before any process starts; `npm` is refused by default, runs with opt-in and the plain argument, and is refused with the `%` argument; without Git for Windows the `bash` tool is `unavailable` and WSL was never started. Whether the 5.1 standard input arrived intact is recorded: if it did, the limit `L:pwsh-local.7` is resolved by setting the code page before start; if not, it is a platform limit of that edition.
- **Cleanup.** None beyond temp files.

<a id="dh-e-exec-8"></a>
#### DH-E-EXEC-8 A confined command is confined, and with no backend it does not run

- **Proves.** [DH-D-EXEC-4](#dh-d-exec-4), [DH-D-EXEC-9](#dh-d-exec-9).
- **Given.** One host per sandbox backend that area A10 ships; policy `read-only`; a marker path outside the write roots.
- **When.** The one-shot shell tool writes the marker; then the backend is made unusable and the call is repeated, in the foreground and as a background job; then a command that exits 125 and prints launcher-like text is run.
- **Then.** The marker is never created. The first result carries `sandbox.enforcement` and a denial hint. With no backend both calls fail with "never ran" at start and no shell process was created. The third is an ordinary failed command.
- **Cleanup.** Restore the backend; remove temp files.

<a id="dh-e-exec-9"></a>
#### DH-E-EXEC-9 A browser attaches to an agent's terminal and reattaches

- **Proves.** [DH-D-EXEC-6](#dh-d-exec-6), [DH-D-EXEC-7](#dh-d-exec-7).
- **Given.** A running host with the HTML UI (areas A19 and C6), a real browser, and an agent terminal running a program that asks for the cursor position and then draws a full-screen page.
- **When.** The tab opens the terminal, is reloaded, and is throttled until it stops acknowledging.
- **Then.** The page shown equals the host's screen after open and after reload; the program received exactly one reply to its query; the throttled tab is dropped and restored from a new snapshot while the program kept running.
- **Cleanup.** Close the terminal; no process remains.

<a id="dh-e-exec-10"></a>
#### DH-E-EXEC-10 Linux over a real SSH login: the host obtains a delegated scope and owns the tree

- **Proves.** [DH-D-EXEC-2](#dh-d-exec-2).
- **Given.** A Linux machine with systemd and cgroup v2, reached by a real SSH login as an ordinary user, so that the login's own control group is not writable by that user; the built binary; the fixture of [DH-E-EXEC-1](#dh-e-exec-1) (a grandchild that calls `setsid` and ignores `SIGTERM`); a second fixture that allocates memory without bound. A second machine of the same kind on which the user manager is stopped.
- **When.** Start `dsh` in the SSH session and read which owner it reports. Run the first fixture through the one-shot `bash` tool with a 2 s timeout. Run the second with `Spec.Limits` set to 256 MiB. From inside a command, connect to the host's control socket. Repeat the first two steps on the second machine.
- **Then.** On the first machine the host made exactly one scope request at start and none per command; the result says `owner: cgroup`; no fixture process survives the timeout and the child group is removed; the second fixture is ended by the limit and the result says `limits: enforced`; the control-socket connection is refused with `PeerTree` reporting `Exact`. On the second machine one warning names the process-group rung, the result says `owner: process-group` and `limits: unavailable`, and the detached grandchild is reported as a survivor.
- **Cleanup.** Kill listed pids; stop the host and confirm the scope is gone (`systemctl --user`); if a scope or a group remains, keep its name and the host log.

## 10. Open questions and limitations

- **Carried limits and their stage.** The carried debt rows of §5 are owned by stage `DH-S-47` of [../impl.md](../impl.md#dh-s-47), except `L:pwsh-local.7`, which waits for one measurement in [DH-E-EXEC-7](#dh-e-exec-7) and is owned by `DH-S-13`.
- **Linux without a writable cgroup: decided.** The host asks the user service manager once for a delegated scope ([DH-ADR-40](../design.md#dh-adr-40), U-K1-1; [DH-ADR-EXEC-2](#dh-adr-exec-2)). Where none answers, a detached grandchild survives a stop, `PeerTree` is best effort and `Spec.Limits` is unavailable, and each is reported. Leftover members of a dead host are listed at the next start and ended only by an operator command, never automatically.
- **Inferences that need a first measurement.** The job-list attribute places a process in a job atomically, and a job that grants no breakaway cannot be left. A cgroup with no controllers enabled may hold both the host and child groups, and `cgroup.kill` exists on the supported kernels. `chcp` and core-type prompt code are enough in ConstrainedLanguage. PowerShell splits its command line by the standard rules. Git Bash passes one `-c` element through unchanged. The process behind a loopback connection can be found without native code on each system (if not on one, `PeerTree` reports that it cannot answer there). **Residual on Windows:** work a system service performs on a tree's behalf (a scheduled task, a COM server) is outside every job. A kill from outside the harness looks like an ordinary exit.
- **The emulator is provisional.** Until the gate of [DH-ADR-EXEC-5](#dh-adr-exec-5) has run, full-screen reads and screen-based prompt matching are a plan. If the gate ends on rung 3, `L:terminal-bash.1` returns as a limit and the bundled-console-host question reopens for older Windows.
- **Which key a child takes.** The SSH world replaces `subprocess.Key` and never `subprocess.HostKey`. Plugin processes, MCP servers the fleet starts, LibreOffice and the browser opener use the host key; language servers and shells use the world key.
- **A person's terminal, and leftovers.** A terminal whose owner is a person is started with `sandbox.Trusted`, because no agent initiated it, and is a trusted tree. A terminal an agent opened keeps its plan and stays an untrusted tree after a person takes it over ([DH-ADR-EXEC-9](#dh-adr-exec-9); [api.md](api.md), `DH-D-API-8`). The idle-or-busy report may say `unknown` (always on Windows, where a console has no foreground group to read). Leftover children of a dead host are listed and ended by the operator command `dsh reap` ([app-cli.md](app-cli.md) owns its grammar).
- **Scope of the guarantee.** The evidence, once it exists, proves the stated contracts for the tested build and systems. It does not prove confinement (area A10) or that a command's own effects can be undone.

## 11. Units dispositioned by this spec

| Type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 15 | 12 ported, 2 merged, 1 replaced |
| `UP-DEBT` | 81 | 56 resolved, 22 carried, 3 not-applicable |
| `UP-TOOL` | 4 | 4 ported |
| `UP-CFG` | 10 | 7 ported, 3 merged |
| `UP-SVC` | 4 | 4 ported |
| `UP-DEC` (handed over) | 7 | 4 ported, 2 replaced, 1 dropped |

Go items declared: 9 decisions, 10 deliverables, 17 tests, 10 scenarios. Rows are in [../trace/fragments/A09.jsonl](../trace/fragments/A09.jsonl).
