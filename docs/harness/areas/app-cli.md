# The `dsh` command line: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". IDs in this document use the area key `CLI`.

| | |
|---|---|
| **Area key** | `CLI` |
| **Upstream packages** | 1 (`apps/cli`, group `apps`), and the 7 commands of upstream's generated help (`docs/cli-help.md`) |
| **Serves outcomes** | [DH-O-14](../design.md#dh-o-14) (one binary, one launch command, the command catalog), [DH-O-13](../design.md#dh-o-13) (the command line as a driver); the client side of [DH-O-6](../design.md#dh-o-6); in part [DH-O-2](../design.md#dh-o-2), [DH-O-3](../design.md#dh-o-3), [DH-O-5](../design.md#dh-o-5), [DH-O-7](../design.md#dh-o-7), [DH-O-11](../design.md#dh-o-11), [DH-O-12](../design.md#dh-o-12) |
| **Depends on areas** | A21 (profiles, layers, launch data, readiness, unload, safe mode), A27 (bundles, presets), A18 ([host-telemetry.md](host-telemetry.md): runtime record, control endpoint, fatal diagnostic, sharing gate), A03 ([tools.md](tools.md): tool endpoint), A02 ([agent-loop.md](agent-loop.md): agents, cancel, teardown), A05 and A06 ([session-store.md](session-store.md), [session-views.md](session-views.md)), A17 ([interaction-credentials.md](interaction-credentials.md): the `cli` channel, human commands, secrets), A20 ([sdk-acp-typert.md](sdk-acp-typert.md): stdio carriers, codec), A22, A10, A15, A07, A16 (commands they own), A09 (ending child processes), A11 ([ptc-workflow.md](ptc-workflow.md): what scripts are taught), A23 (`egress`), A25 and C1 (the update notice) |
| **Priority** | P0 for the grammar, the profile launchers, the output contract and the stop rule: no other area can be started or scripted without them. P1 for `dsh tool` and the admin commands. P2 for update, completion and the deferred import |

## 1. Purpose

`dsh` is the one program a person or a script starts. This area fixes everything typed after that name: which words exist, which flags they take, what each prints on standard output and on standard error, and which exit status a script may rely on. With no desktop application ([DH-ADR-18](../design.md#dh-adr-18)) it also owns what a desktop shell used to do: what a second launch does, what Ctrl-C does while a turn runs, how the process stops within a bound, and what the user sees after a crash. Without it every area would invent its own flags and exit codes. Two pieces are shared with other areas and owned here: the **command-line grammar** and the **optional update check**.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **Launcher** | The first parsing stage of `dsh`: it reads its own options, then one word that names a fixed command or a profile | One binary is many products; something must choose before any component runs |
| **Launcher option** | An option written before the command or profile name (`--home`, `--layer`) | It applies to whatever is started, so its place must not depend on what is started |
| **Fixed command** | A word compiled into the command tree (`tool`, `session`, `update`) | Work that needs no long-running composition, or that must behave the same in every profile |
| **Profile launcher** | `dsh <profile>`: boots the named composition (`web`, `headless`, `sdk`, `sdk-minimal`, `acp`, or one the operator made) | Keeps upstream's rule that every application process starts through one command and a named profile |
| **App words, app command line** | The words after the profile name. The component that the profile mounts to own them declares its options as a `cmdline.Spec` and parses them ([libs/commander.md](../libs/commander.md)) | A custom profile brings options the launcher never heard of |
| **Command class** | `pure` (touches nothing), `admin` (opens the harness home, starts no carrier or agent), `host client` (talks to a running host), `hidden mode` (the binary started by itself) | Says what a command may open and therefore what can go wrong |
| **Admin tree** | The small fixed composition an `admin` command runs in: home layout, store, secrets, config layers | An offline command gets the same locks, leases and redaction as a host, not private file access |
| **Runtime record, control endpoint** | Defined by A18 ([host-telemetry.md §6.1](host-telemetry.md)): a file in the home that says "a host is listening here", and a same-user socket with three methods, `host.status`, `host.stop` and `launch.open`, none of which returns a credential ([host-telemetry.md](host-telemetry.md), `DH-ADR-HOST-12`). A `dsh` that runs inside a process tree the host started for the model is refused by the endpoint | How a second `dsh` finds a running host |
| **Stop function** | The one function every stop request reaches, whatever its source: `launch.App.Stop` of area A21 ([boot.md §6.2](boot.md)). This area turns signals, key presses and `dsh stop` into calls of it and maps each reason to an exit status | Five sources (signal, end of input, a finished task, `dsh stop`, the UI) must not have five shutdown paths |
| **Exit bound** | The longest time between a stop request and the end of the process: boot's `ShutdownTimeout`. No other timer ends the process | A cleanup that never returns must not leave a process behind |
| **Safe mode** | `--safe`: a launch with plugin processes and optional bundles switched off, defined by [boot.md](boot.md), `DH-ADR-BOOT-12` | The start that still works after a plugin or an experimental feature broke the last one |
| **Result stream, diagnostic stream** | Standard output and standard error, with fixed roles | A script can pipe the result without filtering logs out of it |
| **Machine-readable mode** | `--json`: the result stream carries JSON and nothing else | Output a program parses must not change with wording |
| **Unclean exit** | A host that ended without removing its runtime record | Lets the next start say so and name the report |
| **Manifest, channel, swap** | Defined in [libs/electron-updater.md §1](../libs/electron-updater.md): the signed description of a release, the release stream, the replacement of the binary file | The vocabulary of `dsh update` |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-apps.cli` | `@deepseek-ai/dsh` | other (application) | The only supported launcher. Parses the launcher flags, prepares a profile directory, composes its layers, boots the tree, and owns process shutdown and the start-up failure report (`apps/cli/README.md:5`; `apps/cli/src/bin.ts:26-74`). It also forwards `dsh plugin` to the pnpm package manager and prints three configuration dumps |

### 3.2 Behaviour

**Two-stage parsing.** The launcher parses only `--profile`, `--from-default-profile`, repeatable `--patch`, three `--dump-*` switches and `-V`, and hands every later word to the booted tree unchanged; the first word it does not know starts the app's arguments (`apps/cli/src/args.ts:4-11`, `:152-173`). `dsh <name>` is rewritten to `dsh --profile <name>` unless the first word is `plugin` (`:206-208`). The profile mounts a component that parses the leftover words and prints its own help (`apps/cli/reference/README.md:32`).

**Seven documented commands** (`docs/cli-help.md:12-18`). `dsh` is the launcher. `dsh web` serves the browser UI with seven options for address, port, TLS, advertised URL, trusted authorities and `--no-open` (`:184-199`); the browser hand-off waits for the whole tree, is skipped under SSH, and its failure is not fatal (`apps/cli/reference/README.md:127`). `dsh headless` answers one task and exits: the final text goes to standard output, reasoning and diagnostics to standard error, the status is 0 only when the turn ended `completed`, `--json` switches standard output to one event per line, and `--session-id` resumes (`docs/cli-help.md:108-125`; `apps/cli/reference/README.md:46`). `dsh sdk`, `dsh sdk-minimal` and `dsh acp` take no options and reserve standard output for protocol frames (`docs/cli-help.md:88-169`). `dsh plugin --profile <name> <pnpm-args...>` forwards its arguments to pnpm in the profile directory (`apps/cli/src/args.ts:187-202`; `apps/cli/src/plugin.ts:89-107`).

**Dumps.** `--dump-config` and `--dump-default-config` print the composed tree with comments naming the file that supplied each row; `--dump-config-schema` prints one JSON Schema document and exits 1 when any part of it is approximate. All three refuse app arguments because they never run the app's parser (`apps/cli/src/args.ts:115-135`; `apps/cli/src/dump-config-schema.ts:42-47`). A dump creates missing profile files (`apps/cli/reference/README.md:54`). The command reference `docs/cli-help.md` is generated from real help output and checked for staleness (`docs/cli-help.md:1-8`).

### 3.3 Failure, retry, rollback and cleanup upstream

- **Usage errors** exit non-zero through commander; a headless usage error under `--json` is emitted as a JSON event (`packages/bundle/headless/src/startup.ts:102-116`).
- **Start-up failure.** A required plugin that fails prints a summary, writes a report file under `$DSH_HOME/logs/` with modes `0700` and `0600`, and exits 1; if the file cannot be written the full report goes to standard error (`apps/cli/src/startup-diagnostics.ts:57-67`). The report opens with a warning that it may contain credential values (`:40`). Reports are never pruned (`apps/cli/reference/README.md:77`).
- **Shutdown.** The first `SIGINT` or `SIGTERM` starts disposal of the tree with a five-second limit, then the process is forced to exit. `SIGTERM` reports 0 and `SIGINT` 130; a second signal exits at once (`apps/cli/src/process-shutdown.ts:4`, `:52-75`; `apps/cli/src/profile-boot.ts:314-318`). Nothing is undone: a turn in flight is cut off.
- **Schema dump.** It imports plugin modules, which "can block or retain process handles"; the dump neither forces an exit nor cleans those up, and callers are told to use an external timeout (`apps/cli/reference/README.md:70`).

## 4. Upstream decisions dissected

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `UP-DEC-…2026-08-22-single-dsh-application-launcher` | Every application process starts through `dsh` and one named profile, so composition, shutdown and customisation have one owner | keep | One binary makes it the natural shape; the SDK and ACP stay profiles, not separate programs | [DH-ADR-CLI-2](#dh-adr-cli-2) |
| `UP-DEC-…2026-07-29-dsh-source-launch-tsx-esm` | Run the launcher from TypeScript source through the tsx hook | not applicable | A Go program is compiled; there is no source launch | none |
| `UP-DEC-…2026-09-20-config-schema-dump-validation-policy` | The schema dump exits 1 when approximate, inspects disabled rows, and widens where native validation would mutate input | change | Keep: a schema command that never mounts anything and covers disabled entries. Drop: "partial" and widening, because a Go config type yields an exact schema ([DH-ADR-SCHEMA-1](../libs/zod.md#dh-adr-schema-1)) and no plugin code is imported | [DH-ADR-CLI-2](#dh-adr-cli-2), [DH-D-CLI-7](#dh-d-cli-7) |
| `UP-DEC-…2026-09-14-generated-cli-help-reference` | The command reference is generated from real help and checked for staleness | keep | Same drift, same cure; it becomes the `commands` catalog ([DH-ADR-TOOLCHAIN-10](../upstream/toolchain.md#dh-adr-toolchain-10)) | [DH-D-CLI-1](#dh-d-cli-1) |
| Code: split at "the first word the launcher does not know" (`apps/cli/src/args.ts:159-166`) | Lets `dsh tui --patch x` work | change | A new launcher option can swallow a word that used to reach an app. Positional split, as [DH-ADR-COMMANDER-2](../libs/commander.md#dh-adr-commander-2) proposes | [DH-ADR-CLI-1](#dh-adr-cli-1) |
| Code: `dsh <name>` and `dsh --profile <name>` both exist; `plugin` wins the short form (`:187`, `:206`) | A shorthand plus an escape for a profile named like a command | keep | The explicit form is what programs should use; it survives a new fixed command | [DH-ADR-CLI-1](#dh-adr-cli-1) |
| Code: dumps are launcher switches that exclude each other by hand (`:115-121`) | Inspect without booting | change | They are commands, not modifiers of a launch; as `dsh config …` they need no exclusion rule | [DH-ADR-CLI-2](#dh-adr-cli-2) |
| Code: a dump initialises missing profile files (`apps/cli/reference/README.md:54`) | Reuses profile preparation | drop | A preview that writes is a surprise; `admin` commands that read never create ([DH-O-2](../design.md#dh-o-2)) | [DH-D-CLI-7](#dh-d-cli-7) |
| Code: `dsh plugin` forwards to pnpm (`apps/cli/src/plugin.ts:89`) | Plugins are npm packages | drop | Plugins are local directories ([DH-ADR-13](../design.md#dh-adr-13)); there is nothing to forward to | [DH-ADR-CLI-2](#dh-adr-cli-2) |
| Code: `--from-default-profile` on the launcher (`apps/cli/src/args.ts:169`) | Recreate a profile from a shipped template, then boot | change | Creating and booting are two actions; `dsh profile new` and `dsh profile reset` (semantics: A21) | [DH-D-CLI-7](#dh-d-cli-7) |
| Code: the reserved `desktop` profile (`apps/cli/README.md:20`) | The Electron application owns it | drop | No desktop application ([DH-ADR-18](../design.md#dh-adr-18)) | none |
| Code: `sdk-minimal` is a fifth shipped profile (`docs/architecture.md:43`) | A standalone minimal tree for the SDK example | keep | It stays a built-in profile, defined by A27 as the bundles `core`, `minimal` and `sdk`; the `minimal` agent preset is also selectable under `dsh sdk`; see [§6.2.5](#625-what-the-seven-upstream-commands-become) | [DH-ADR-CLI-2](#dh-adr-cli-2) |
| Code: headless prints the answer on standard output, everything else on standard error, and exits 0 only for `completed` (`apps/cli/reference/README.md:46`) | A pipe gets the answer and nothing else | keep, and generalise to every command | It is the rule scripts need everywhere | [DH-ADR-CLI-3](#dh-adr-cli-3) |
| Code: `SIGTERM` exits 0, `SIGINT` 130, a second signal forces, five seconds of grace (`apps/cli/src/process-shutdown.ts:4`) | A supervisor's stop is ordinary; a stuck disposal must not hang | keep the shape, change the bound and the first Ctrl-C | Five seconds cuts the final flush that A02 requires ([DH-ADR-AGENT-8](agent-loop.md#dh-adr-agent-8)); Ctrl-C should end the turn in the log first | [DH-ADR-CLI-5](#dh-adr-cli-5) |
| Code: one start-up summary plus a private report file (`apps/cli/src/startup-diagnostics.ts:57-67`) | Keep the terminal short and the evidence complete | keep, extend | A18's fatal diagnostic covers every profile and every fatal error, with redaction and pruning | [DH-D-CLI-5](#dh-d-cli-5) |
| Code: no browser under SSH; hand-off failure is not fatal (`apps/cli/reference/README.md:127`) | A remote terminal has no browser | keep | Owned by A18 and [libs/open.md](../libs/open.md); this area supplies `--no-open` | [DH-D-CLI-2](#dh-d-cli-2) |

## 5. Tech debt and its resolution

No `UP-DEBT` unit was assigned: `apps/cli/README.md` has no "Known Limitations" section. Reading the code found three, defined in the ledger as `UP-DEBT-found.A24.1` to `.3`.

| Group | Units | Cause | Answer in Go |
|---|---|---|---|
| **Inspection runs plugin code** | `found.A24.1` | The schema dump imports every plugin module to read its declaration, so it can hang and leave handles (`apps/cli/reference/README.md:70`) | Resolved: schemas are derived from compiled-in Go types; `dsh config schema` starts no component and no plugin process ([DH-D-CLI-7](#dh-d-cli-7)) |
| **The failure report may leak secrets** | `found.A24.2` | The report dumps raw error objects and warns the reader instead (`apps/cli/src/startup-diagnostics.ts:40`) | Resolved: a secret is a redacting type in every text and JSON form ([DH-ADR-SECURITY-6](../upstream/security.md#dh-adr-security-6)); the report is A18's and is bounded and pruned ([DH-D-CLI-5](#dh-d-cli-5)) |
| **A fixed five-second exit cuts the last events** | `found.A24.3` | One constant bounds every shutdown, whatever is still flushing (`apps/cli/src/process-shutdown.ts:4`) | Resolved: the stop function cancels turns with a cause, waits for the agent teardown's bounded flush, and only then applies a configurable exit deadline whose expiry is reported as a forced exit ([DH-ADR-CLI-5](#dh-adr-cli-5)) |

## 6. Go design

### 6.1 Shape

`cmd/dsh` is three steps: install A18's fatal diagnostic, dispatch a hidden mode if the first word names one, otherwise call `cli.Main` and exit with the status it returns. Everything testable lives under `internal/cli`, because the coverage floor ([DH-ADR-14](../design.md#dh-adr-14)) applies to `internal` packages and a `main` package cannot be driven with buffers. `internal/cli` and `internal/update` are this area's package families beside `cmd/dsh`.

| Package (under `src/harness`) | Holds | Why it exists |
|---|---|---|
| `cmd/dsh` | `main` only | The one binary |
| `internal/cli/cmdline` | `Spec`, `Parse`, `Help`, `UsageError`, exactly as [libs/commander.md §6](../libs/commander.md) writes them, plus the `Command` contribution type. Imports only `pflag` | The leaf every area imports to declare options or contribute a command; no area sees `cobra` |
| `internal/cli/tree` | The `cobra` root, the launcher stage, the reserved-word list, help, completion, the `commands` catalog | The grammar in one place |
| `internal/cli/output` | The result writer (text, JSON, JSON Lines), the one error line, the exit-status table | One output contract for forty commands |
| `internal/cli/lifecycle` | Signal handling, the Ctrl-C confirmation, and the mapping from stop reason to exit status; each ends in one call of boot's `launch.App.Stop`. It owns no tree and no timer | One shutdown path |
| `internal/cli/hostclient` | Reads runtime records and speaks A18's control endpoint (`host.status`, `host.stop`, `launch.open`) | Second launch, `dsh stop`, `dsh status` |
| `internal/cli/headless` | Component `cli.headless`: the one-shot runner and the `cli` answerer | The command line as a carrier ([../design.md §2](../design.md#2-vocabulary)) |
| `internal/cli/toolcmd` | The client of the tool endpoint and the JSON-pointer selector | `dsh tool` |
| `internal/update` | Manifest, verifier, source, the `update.check` component, the swap ([libs/electron-updater.md §6](../libs/electron-updater.md)) | `dsh update` and the optional check |

**Components.** `cli.headless` injects the agent registry (A02), the session store (A05), the interaction registry (A17) and the app words (A21's launch data); it provides nothing. `update.check` is mounted by the `web` profile only; it injects A18's sharing gate, A23's `egress` and A07's key-value table, and provides `update.Status` for the UI notice: the last verified result, and `RestartPending`, which is true when `dsh update` replaced the binary this host was started from. The swap records the replaced file's identity in the home, so the fact does not depend on the running file's path (on Windows the old file is renamed) and `dsh status` reads the same record. `lifecycle` is not a component: it runs beside `boot.Run`, which alone creates and disposes the root tree, and reaches it only through `launch.App`.

```go
// cmdline: how any area contributes a fixed command.
type Command struct {
    Path  []string // {"session", "export"}
    Spec  Spec     // options, rules, arguments, examples
    Class Class    // Pure | Admin | HostClient
    Run   func(ctx context.Context, in Invocation) error
}
type Invocation struct {
    Values any       // the struct Spec was parsed into
    Home   string    // resolved harness home
    Out    Result    // Value(v), Line(v), Text(s) on the result stream; Diag(format, ...) on the other
    Admin  AdminTree // nil unless Class == Admin: typed access to the admin tree's services
}
// lifecycle: turns a signal, a key press or a control-endpoint call into one stop request on A21's launch.App.
// launch.Reason is the closed set Completed, Interrupted, Terminated, InputClosed, ClientStalled, Requested, Fatal.
func Watch(ctx context.Context, app launch.App, summary func(context.Context) Work, cfg Config) // signals and Ctrl-C
func Status(r launch.Reason, result int) int // the exit status of a reason; result is used only for Completed
```

`Run` returns `nil`, a `*cmdline.UsageError`, or an error carrying a `<domain>/<reason>` code from the one error table ([../design.md §4.8](../design.md#48-wire-surfaces)). `output` turns the code into the exit status; a command never calls `os.Exit`. `tree` imports each area's command list and areas import only `cmdline`, so the import-direction test of [DH-ADR-ARCH-9](../upstream/architecture.md#dh-adr-arch-9) holds.

### 6.2 Contracts

#### 6.2.1 Grammar

```text
dsh [launcher options] <fixed command> [subcommand] [flags] [arguments]
dsh [launcher options] <profile> [app words...]
dsh [launcher options] --profile <profile> [app words...]
```

1. Launcher options come first. The first word that is not an option ends them.
2. That word is a fixed command if the tree has it, otherwise a profile name. Everything after a profile name belongs to the app, including `--help`.
3. `--profile <name>` names the profile explicitly; then every remaining word is an app word. Programs that start `dsh` use this form.
4. Inside a fixed command, flags may follow arguments (`pflag` interspersed parsing); `--` ends flags.
5. A profile cannot be created with a reserved word as its name. Reserved: every fixed command, `help`, `version`, and the hidden modes.

| Launcher option | Meaning | Default |
|---|---|---|
| `--home <dir>` | The harness home | `DSH_HOME`, else `homedir.Default` of A23 ([util-test-support.md](util-test-support.md), DH-D-UTIL-5); the layout inside it is A07's |
| `--profile <name>` | Explicit profile selector (rule 3) | none |
| `--layer <file>` (repeatable) | A command-line overlay layer, applied last in the order given (upstream `--patch`) | none |
| `--safe` | Safe mode: the launch runs with every plugin process and every optional (experimental) bundle switched off, through boot's launcher layer. It reads the same configuration, deletes nothing and widens nothing ([boot.md](boot.md), `DH-ADR-BOOT-12`). It exists so that the host can be started after a plugin or an experimental feature made it crash or hang. Meaningful for profile launchers; a fixed command accepts and ignores it | off |
| `-h`, `--help`; `-V`, `--version` | Launcher help; version, commit and platform from the Go build information | |

Every fixed command also accepts `--json` and `-h`. Environment the command line itself reads: `DSH_HOME`, `DSH_TOOL_ENDPOINT` and `DSH_TOOL_TOKEN` (only `dsh tool`), `NO_COLOR`.

#### 6.2.2 The command tree

**Profile launchers.** Class: host. All take the launcher options.

| Command | App flags and arguments | Result stream | Diagnostic stream | Statuses beyond [§6.2.4](#624-exit-statuses) |
|---|---|---|---|---|
| `dsh web` | `--bind <address>` (one concrete address. The flag is `--bind` in every document; upstream's `--host` is not accepted, and typing it is a usage error that names `--bind`), `--port <n>` (0: the system picks), `--no-open`, `--public-url <url>`, `--trusted-host <authority>` (repeatable), `--tls-cert <file>` with `--tls-key <file>` (together), `--insecure-remote`. Meaning and validation are A18's ([host-telemetry.md §10](host-telemetry.md)) | A host that starts: one line, the launch URL. A second launch that reuses a host: one line that says where to look, the host's browser or the host's own terminal; the link itself never appears here ([DH-ADR-28](../design.md#dh-adr-28)) | Start-up notices, the unclean-exit line, logs | 0 after a clean stop or reuse; 4 `launch/no-channel`; 6 address in use or conflicting flags ([DH-ADR-CLI-4](#dh-adr-cli-4)) |
| `dsh headless [task...]` | `--json`, `--session <id>` (resume; upstream `--session-id`), `--agent-preset <id>` (the agent preset of a fresh run, A27), `--permission-mode <permission preset>` (A17), `--reasoning auto\|show\|hide` (default `auto`: reasoning is written to the diagnostic stream only when that stream is a terminal), `--approval-policy ask\|never`, `--add-dir <path>` (repeatable), `--command "/name input"` (run one human command of A17 instead of a task). The task is the joined words, or standard input when it is `-` or absent and piped | The last assistant text of the run. With `--json`: one committed session event per line in A05's export line format, then one `run/result` line (`session`, `turn`, `reason`, `text`) | One progress line when the run starts and one at each step start, before any token, naming the step and the model route; tool progress; the approval prompt; reasoning as `--reasoning` decides (debt entry U-K2-4) | 0 turn ended `completed`; 1 any other ending; 5 unknown session or agent preset; 6 `session/owned` |
| `dsh sdk`, `dsh sdk-minimal`, `dsh acp` | none | Protocol frames only (A20 claims standard output) | Logs | 0 end of input, `shutdown` or `SIGTERM`; 125 stalled client; 124 forced |
| `dsh <custom>` | Whatever the profile's app declares | The app's | The app's | The app's, from the same table |

**Fixed commands specified here.**

| Command | Class | Flags and arguments | Result stream | Statuses beyond the table |
|---|---|---|---|---|
| `dsh tool call <name>` | host client | Arguments: `--args <json>`, `--args-file <path>`, `--args -` (standard input), and repeatable `--arg field=value` (a string) or `--arg field:=<json>` (a typed value), merged over the document. Output: the JSON value (default), `--text` (what the model would read), `--envelope` (the whole reply), `--pick <json-pointer>` with `--lines` (an array, one element per line; a picked string is printed raw). `--attach` adds the result's images to the outer call | As selected | See the mapping below |
| `dsh tool list`, `dsh tool describe <name>` | host client | `--json` | Names with one-line descriptions; one input schema | 4 without the two variables |
| `dsh tool pick <json-pointer>` | pure | `--file <path>` (default standard input), `--lines` | The selected value; A11 asked for this to read `DSH_WORKFLOW_ARGS` ([ptc-workflow.md §10](ptc-workflow.md)) | 5 no such field or not JSON |
| `dsh stop` | host client | `--yes` (do not ask), `--bind`, `--port` (which host, when the home has several) | nothing | 0 stopped; 1 declined; 4 no host on this home |
| `dsh status` | host client | `--json` | One row per runtime record: address, process id, version, start time, alive or stale, work that a stop would interrupt, "restart pending" after an update | 0 also when no host runs |
| `dsh update` | pure | `--check` (report only), `--channel stable\|nightly`, `--yes` | Running and available version | 0 up to date or updated; 1 verification or swap failed and the old binary is in place; 4 release host unreachable; 6 binary not owned ([libs/electron-updater.md](../libs/electron-updater.md), R9) |
| `dsh catalog commands` | pure | `--format md\|json` | The reference of every fixed command and every shipped profile's app, generated from the tree | |
| `dsh completion <shell>` | pure | `bash`, `zsh`, `fish`, `powershell` | The completion script | |
| `dsh help [command]`, `dsh version` | pure | `--json` on `version` | Help; version, commit, Go version, platform and build tags | |

`dsh tool` reads only `DSH_TOOL_ENDPOINT` and `DSH_TOOL_TOKEN` and has no flag for a session, agent or turn ([tools.md](tools.md), [DH-D-TOOLS-4](tools.md#dh-d-tools-4)). A malformed argument document is refused before connecting, with line and column, status 5. The reply carries one of A03's closed `outcome` kinds and, on failure, an error code. The code chooses the status by the table below; a code with no row falls back to its outcome's class (`ok` 0, `tool-failed` 1, `refused` 3, `unavailable` and `token-rejected` 4, `bad-request` 5, `limit` 7, `cancelled` 8), so a new code can never yield an undefined status. A11's prompt section teaches a script these numbers.

| Status | Reply | A script should |
|---|---|---|
| 0 | success | use the result |
| 1 | the tool body ran and failed (its own code, or `tools/invalid-output`) | read the message |
| 3 | `tools/denied`, `tools/blocked` | not retry |
| 4 | no variables, `tools/endpoint-unavailable`, `tools/token-rejected`, `tools/not-reachable`, `tools/activation-failed` | stop calling tools |
| 5 | `tools/unknown-tool`, `tools/invalid-arguments`, `tools/arguments-too-large` | fix the call |
| 6 | `tools/busy` | retry later |
| 7 | `tools/limit`, `tools/timeout` | batch or stop |
| 8 | `tools/aborted-before-start`, `tools/aborted`, `tools/outcome-unknown`, `tools/not-started` | verify before repeating |

**Fixed commands whose meaning another area owns.** This area fixes the words, the common flags, the streams and the statuses; the owner supplies a `cmdline.Command` and its `Spec`. All are class `admin` unless marked.

| Command | Owner | What it is for | Priority |
|---|---|---|---|
| `dsh config show [profile]` (`--defaults`, `--format yaml\|json`), `dsh config schema`, `dsh config validate [file...]`, `dsh config path` | A21 | Preview the composed tree with the layer that supplied each value; the exact JSON Schema; check a layer and report file, line and column; print the layer paths. None starts a component or writes a file. They replace the three `--dump-*` switches | P0 |
| `dsh profile list`, `dsh profile new <name> --from <shipped>`, `dsh profile reset <name>` | A21, A27 | Replaces `--from-default-profile`. `reset` sets the profile layer aside under a unique name and keeps its bytes; it is the remedy when that layer stops start-up | P1 |
| `dsh plugin list`, `inspect <dir\|name>`, `add <dir>`, `remove <name>` (`--purge`), `grant <name> <capability...>`, `revoke <name> <capability...>`, `enable <name>`, `disable <name>`, `restart <name>` | A21 for the entry, A22 for inspection, the approved copy and grants | Local plugin directories: inspect before running, grant, install ([DH-ADR-13](../design.md#dh-adr-13)). `add` copies the inspected bytes into the home (A22) and nothing from a network. These commands are the owner's way to install durably; the model's tool cannot ([boot.md](boot.md), `DH-ADR-BOOT-10`) | P1 |
| `dsh session list`, `show <id>`, `export <id> --out <file\|->`, `import <file>`, `delete <id>` | A05, A06 | Portable copies and maintenance ([session-store.md](session-store.md) DH-D-STORE-6, -7; [session-views.md](session-views.md)) | P1 |
| `dsh session import-upstream <path>` (`--dry-run`) | A05 | The deferred one-way import of upstream sessions ([DH-ADR-11](../design.md#dh-adr-11)); refuses a whole file it cannot map | P2 |
| `dsh store check`, `dsh store rebuild` (`--session <id>`, `--verify`) | A05 | `check` is the integrity check and writes nothing. `rebuild` is the one name for A05's rebuild operation ([session-store.md](session-store.md), DH-D-STORE-6): it discards and refolds derived data (checkpoints, view rows and the search index), for the whole home or one session, with a verify option. There is no `dsh session reindex` | P1 |
| `dsh state list`, `dsh state show <table>` | A07 | Read machine state that is no longer a JSON file ([storage.md](storage.md)) | P1 |
| `dsh secret set <name>` (value on standard input), `unset <name>`, `list`, `reset`; `dsh identity reset` | A17 | The secret store and the anonymous installation id. A value is never an argument and never printed | P0 for `secret set` |
| `dsh sandbox doctor`, `status`, `prepare <dir>`, `cleanup`; `dsh ssh install <name>` (`--binary <file>`), `ssh uninstall <name>` | A10 | Probe and repair the sandbox; consent ahead; place the remote helper ([sandbox-ssh.md](sandbox-ssh.md)). `status` shows, per backend, the state, the missing effects, the exposure list and the listener's `PeerRule()` | P1 |
| `dsh mcp list`, `probe <name>`, `login <name>`, `logout <name>`, `restart <name>` (host client); `dsh connector skills install <name> --from <path>` (it fetches nothing; recommended default, not confirmed by the owner), `verify` | A15 | External tool servers and connector skills ([mcp-lsp-web-connector.md](mcp-lsp-web-connector.md)) | P1 |
| `dsh crash list`, `show [id]`, `prune` | A18 | The fatal reports under `<home>/crash/` | P1 |
| `dsh reap` (`--yes`) | A09 | Lists and ends child processes a dead host left behind ([execution.md §10](execution.md)); a new host never does this by itself | P1 |
| `dsh debug http`, `dsh debug profile`, `dsh debug tree`; `dsh session import-chat <zip-or-directory>` | X3 | The diagnostics and chat-import commands of [x-inspector-timeline.md §6.2](x-inspector-timeline.md), present only where its optional bundles are compiled in | P2 |
| `dsh doctor` | each area registers a check | One table: sandbox backend, keychain, WAL support of the home's filesystem, LibreOffice, the computer-use provider (A16's three status values) | P1 |
| `dsh catalog tools\|events\|config\|services\|persisted\|errors` | each registry's owner | The generated catalogs of [DH-O-14](../design.md#dh-o-14) | P1 |
| `dsh protocol schema` | A20 | The SDK protocol's schema document | P1 |
| Hidden: `dsh sandbox-launch`, `dsh ssh-helper` | A10 | Private modes of the binary. Dispatched on the exact first word before any parsing, absent from help and completion, reserved as profile names. The selector is removed before any further child starts ([libs/yao-pkg-pkg.md](../libs/yao-pkg-pkg.md), R2) | P0 for the reservation |

#### 6.2.3 Output contract

1. **The result stream carries only the result**: the answer, a table, a JSON document, a URL or protocol frames. Progress, prompts, warnings, logs and errors go to the diagnostic stream. Nothing reaches the result stream through the logger ([DH-ADR-LOADER-9](../libs/cordis-plugins.md#dh-adr-loader-9)).
2. **`--json`** makes the result one JSON value, or JSON Lines for a command that streams. Field names are part of the contract and are held by golden files. Without `--json` the text is for people and may change.
3. **A failure** is one line on the diagnostic stream, in the form `dsh <command>: <domain>/<reason>: <message>`, and a status from the table below. Under `--json` the result stream also ends with `{"error":{"code","message"}}`, so a program that reads only one stream still learns why. A usage error adds one line that names the help command.
4. **A person is asked only on a terminal.** A prompt (`dsh stop`, `dsh update`, the `cli` answerer) is written to the diagnostic stream and read from the controlling terminal, never from a piped standard input. With no terminal the answer is the safe one: no.
5. **Colour and width** are used only when the stream is a terminal and `NO_COLOR` is unset. Secrets print as `[redacted]`.

#### 6.2.4 Exit statuses

One closed table for every command. Statuses 1 to 8 are result classes that mean the same in every command; the others are process-level.

| Status | Meaning | Examples |
|---|---|---|
| 0 | Success; also help, version, a host stopped by `SIGTERM` or `dsh stop` inside the deadline | |
| 1 | The thing asked for ran and failed | A turn that did not end `completed`; a failed tool body; `store check` found damage |
| 2 | Reserved. The Go runtime exits with 2 after an unrecovered panic or a fatal runtime error (`runtime/panic.go:1449`, Go 1.27.4); A18's crash file holds the output. No command returns 2 | |
| 3 | Denied by policy, a guard, a person, or because nobody could be asked | |
| 4 | Unavailable: no host, no endpoint, a capability whose software is missing, a server that cannot be reached | |
| 5 | Invalid input or not found: arguments that fail a schema, an unknown session, tool or profile | |
| 6 | Busy or conflict: `store/busy`, `session/owned`, an address in use, a host already running with other settings | |
| 7 | A limit or a timeout ended the work | |
| 8 | Cancelled, or the outcome is unknown | |
| 64 | Usage error: unknown command or flag, a broken rule between flags, no task | |
| 70 | Fatal error with a report written (`diag.Fatal`, A18) | |
| 78 | The configuration cannot be composed, or a required entry failed to load | |
| 124 | Forced exit: the exit deadline passed during shutdown | |
| 125 | A stdio client stopped reading (A20's stall limit) | |
| 130 | Interrupted by Ctrl-C | |

Statuses 0, 125 and 124 are the "clean, stalled client, forced" that A20 asks for ([sdk-acp-typert.md](sdk-acp-typert.md), DH-D-SDK-3). Inference: 64, 70 and 78 follow the BSD `sysexits` convention so that 2 can stay with the Go runtime.

#### 6.2.5 What the seven upstream commands become

| Upstream unit | Becomes | Changes |
|---|---|---|
| `UP-CLI-dsh` | The launcher | Options before the name; `--patch` is `--layer`; the dumps are `dsh config`; `--from-default-profile` is `dsh profile`; `--home` and `--safe` are new |
| `UP-CLI-dsh.web` | `dsh web` | `--host` is `--bind`; `--insecure-remote` is new; a second launch reuses the host |
| `UP-CLI-dsh.headless` | `dsh headless` | `--session-id` is `--session`; `--command` is new; approvals are asked on a terminal or refused |
| `UP-CLI-dsh.sdk` | `dsh sdk` | Programs launch it as `dsh --home <dir> --profile sdk` |
| `UP-CLI-dsh.sdk-minimal` | `dsh sdk-minimal` | A built-in profile, launched by rule 2 of the grammar like the others. Its content is A27's: the bundles `core`, `minimal` and `sdk` ([bundle-preset.md §6.2](bundle-preset.md)); no bundle is called `sdk-minimal`. The `minimal` agent preset is also selectable under `dsh sdk` |
| `UP-CLI-dsh.acp` | `dsh acp` | None in the command line |
| `UP-CLI-dsh.plugin` | `dsh plugin <verb>` | Nested commands for local directories; nothing is forwarded to a package manager |

#### 6.2.6 Config

| Struct | Fields (default) | Replaces |
|---|---|---|
| `lifecycle.Config` | `confirmWindow` (5 s). The exit bound is not here: it is the profile's `ShutdownTimeout` (20 s, [boot.md §6.2](boot.md)), which replaces the constant at `apps/cli/src/process-shutdown.ts:4` | none |
| `update.Config` | `check` (false), `channel` (`stable`), `source` (the release address), `interval` (24 h), `silence` (60 s) | Upstream's desktop update settings ([libs/electron-updater.md](../libs/electron-updater.md)) |

This area defines no session event, no tool for the model and no wire message. It uses A03's endpoint request ([tools.md §6.2](tools.md)), A18's control endpoint and A05's export line format as written.

### 6.3 Ownership, failure, retry, rollback and cleanup

| Resource | Owner (who releases it) | Who merely has access |
|---|---|---|
| The argument list | The launcher; read-only launch data | The one component per profile that parses app words |
| The root tree | `boot.Run` (A21), which created it and disposes it once, on the first stop request | Every component in it; `lifecycle`, which may only ask for a stop |
| Signal handlers, the terminal mode during a prompt | `lifecycle`; the `cli` answerer, which restores the mode on every path it can run | |
| The agent of a headless run and its session lease | The runner, through the agent handle ([../design.md §2](../design.md#2-vocabulary)) | The `cli` answerer, bound to that agent |
| The listener, runtime record and control endpoint | The host (A18) | `hostclient`, which only reads the record and calls the endpoint |
| A browser tab | The user | The host never closes or focuses it |
| The downloaded archive and the kept previous binary | `dsh update`; removed when the command ends, and after the new version first starts | A binary a package manager installed is never touched |

| Situation | What happens | What is undone; what stays uncertain |
|---|---|---|
| A usage error, an unknown profile, a reserved name | One line, status 64 or 5. Nothing was opened | Nothing |
| The configuration does not compose, or a required entry fails | A18's fatal diagnostic names the first error and the report; status 78. The effects of the partial load are released by the tree | Nothing remains mounted |
| A second `dsh web` on a home with a host | [DH-ADR-CLI-4](#dh-adr-cli-4) | Nothing changes in the running host |
| The control endpoint does not answer although the record's lock is held | `host/unreachable`, status 4, and the record's process id is printed; nothing is replaced | **Uncertain:** whether that host is hung; the user decides to kill it |
| Ctrl-C during a headless turn | The turn is cancelled with cause `user-interrupt`; the runner waits for `turn/end` and the flush, prints what text exists, exits 130 | Started tool work settles or is recorded as unknown by A03's rules |
| Ctrl-C or `dsh stop` on a host with running work | [DH-ADR-CLI-5](#dh-adr-cli-5) | Pending approvals are denied; session grants end ([../design.md §5](../design.md#5-failure-retry-rollback-and-cleanup)) |
| The exit bound (`ShutdownTimeout`) passes | Boot prints the cleanups still running; status 124. This is the crash path: the next writer closes the open turn | **Uncertain:** unflushed events and child processes A09 could not end |
| The result stream closes early (`dsh headless \| head`) | The command cancels its work as for Ctrl-C and exits 1 with `cli/output-closed` on the diagnostic stream | As for cancellation |
| The host died (unclean exit) | The next start on that home and address prints one line with the report path and `--safe`, replaces the stale record and continues. A browser tab shows C1's "host not reachable" state with the restart command | **Uncertain:** work in flight; the session log reports it on resume |
| `dsh update` fails at any step | [libs/electron-updater.md §6](../libs/electron-updater.md): the previous file is restored or left in place | **Uncertain:** only if the command is killed between its two renames |

Nothing in this area retries by itself. A script decides whether to retry from the status.

## 7. Decisions

<a id="dh-adr-cli-1"></a>
### DH-ADR-CLI-1 Launcher options first, then one word; `--profile` is the explicit form

- **Decision.** The grammar of [§6.2.1](#621-grammar). This confirms [DH-ADR-COMMANDER-2](../libs/commander.md#dh-adr-commander-2) and adds two things: the explicit `--profile <name>` selector, and a closed reserved-word list that a test compares with the tree.
- **Why.** Upstream's "first unknown word" split makes a line's meaning depend on the launcher's whole option list. A positional split is stable. The explicit selector means a program that starts `dsh` is never affected when a later release adds a fixed command with the name of somebody's profile.
- **Tradeoff.** `dsh tui --patch x` becomes `dsh --layer x tui`; the old order is reported by the app as an unknown option, not silently accepted. Two spellings select a profile. A profile created before a release that reserves its name stays reachable only through `--profile`.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-13](../design.md#dh-o-13). **Verified by.** `DH-T-CLI-1`, `DH-T-CLI-2`, `DH-T-COMMANDER-1`.

<a id="dh-adr-cli-2"></a>
### DH-ADR-CLI-2 Long-running compositions are profiles; everything else is a fixed command in one of three classes

- **Decision.** A profile launcher boots a composition and runs until stopped. Any other action is a fixed command of class `pure`, `admin` or `host client`. An `admin` command runs in the admin tree, a fixed composition without carriers, agents, model adapters or plugin processes, loaded by the same loader. The dump switches, profile creation and plugin management become fixed commands; the five built-in profiles, `sdk-minimal` among them, are launchers ([§6.2.5](#625-what-the-seven-upstream-commands-become)). Areas contribute commands as data (`cmdline.Command`); `tree` owns the words.
- **Why.** Upstream bolts inspection onto the launcher as mutually exclusive switches and reaches files directly. Several processes may share a home ([DH-ADR-12](../design.md#dh-adr-12)), so an offline command must take the same leases and locks as a host: `dsh session delete` on a session a host holds gets `session/owned`, not a corrupted log.
- **Tradeoff.** An admin command pays the cost of loading a small tree. The tree has about forty commands, more than upstream's seven, because duties that lived in the desktop application and in hand-edited files moved here.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-2](../design.md#dh-o-2), [DH-O-5](../design.md#dh-o-5), [DH-O-11](../design.md#dh-o-11). **Verified by.** `DH-T-CLI-3`, `DH-T-CLI-4`, `DH-E-CLI-4`.

<a id="dh-adr-cli-3"></a>
### DH-ADR-CLI-3 One output contract and one closed table of exit statuses

- **Decision.** [§6.2.3](#623-output-contract) and [§6.2.4](#624-exit-statuses) bind every command and every profile's app. A status is derived from the `<domain>/<reason>` code by one table in `output`; a new code must be placed in a class or the build's table test fails. Scope boundary of this record (unit `bundle.headless.8`): the result stream carries only the result, so `dsh headless` without `--json` prints the final answer and nothing else there; progress, reasoning and tool output never go to the result stream, and a program that wants the whole run reads `--json` lines.
- **Why.** Scripts, the SDK clients and A11's model-written scripts all branch on the status. A03 needs "failed tool" to differ from every other failure, and A20 needs three distinct endings.
- **Headless diagnostics (U-K2-4).** Upstream prints nothing before the first token and writes reasoning to standard error always. Here a headless run writes a progress line at the start and at each step start, so that a slow first token is not mistaken for a hang, and it writes reasoning only to a terminal unless `--reasoning show` asks for it: model output must not land in a log collector by default, in the spirit of [DH-ADR-9](../design.md#dh-adr-9). The progress lines hold no model output. Recommended default, not confirmed by the owner.
- **Tradeoff.** Usage errors are 64, not the 2 many tools use, because the Go runtime owns 2. Text output carries no compatibility promise. A job that wants reasoning in its log must pass `--reasoning show`.
- **Serves.** [DH-O-13](../design.md#dh-o-13), [DH-O-6](../design.md#dh-o-6). **Verified by.** `DH-T-CLI-5`, `DH-T-CLI-6`, `DH-E-CLI-3`.

<a id="dh-adr-cli-4"></a>
### DH-ADR-CLI-4 A second `dsh web` reuses the host on the same address and may start another on a different one

- **Decision.** Two parts. **Decided by the owner ([DH-ADR-28](../design.md#dh-adr-28)):** a second `dsh` or `dsh web` never receives the sign-in link; the running host shows it itself. **Recommended default, not confirmed by the owner:** when a second launch reuses the host and when it may start another, clauses (a) to (e). The rule never changes a running host and never starts a second one unless the command names a different address. For a home that already has a live host: (a) the same address, which includes the default, reuses it: `dsh` calls `launch.open` on the control endpoint and exits 0 without binding anything. The running host opens its own browser, or with `--no-open` prints a fresh one-time link on its own terminal. The second command prints only where to look: "opened in the browser of the host (process N)" for `opened`, "the link is on the terminal of the host (process N)" for `printed`. When that host has neither a display nor a terminal the answer is `launch/no-channel`, status 4, with the advice to restart the host from a terminal. This is also how a second browser gets a link. (b) The same address with a flag that would change the running listener (`--tls-cert`, `--tls-key`, `--public-url`, `--trusted-host`, `--insecure-remote`) is refused with `host/already-running`, status 6, naming the process id. (c) A different address starts a second host, which says at start that the home is shared; per-session leases arbitrate. (d) A stale record is replaced. (e) An address held by another program is status 6; no other port is chosen silently. `headless`, `sdk` and `acp` never look for a host: they start, and a session another process holds is `session/owned`.
- **Why.** The owner allowed several processes per home ([DH-ADR-12](../design.md#dh-adr-12)) and asked what a second launch does. Reuse is what a person who types `dsh web` twice wants, and A18 already specifies it. A silently ignored security flag would be worse than a refusal.
- **Tradeoff.** Two hosts on one home show each other's sessions only through A05's follow mechanism. `dsh web` on a shared home is not a way to restart a host; `dsh stop` is.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-CLI-7`, `DH-T-ELECTRON-1`, `DH-E-CLI-1`.

<a id="dh-adr-cli-5"></a>
### DH-ADR-CLI-5 One stop function; Ctrl-C ends the turn first; a signal never waits for an answer; a deadline bounds the exit

- **Decision.**
  1. Every stop request is one call of `launch.App.Stop(reason, status)`, the stop function, which area A21 owns ([boot.md](boot.md), `DH-ADR-BOOT-6`). Boot then closes `App.Stopping` (carriers stop admitting work and cancel running turns with a typed cause in their own cleanups), unloads the tree from the leaves, and enforces `ShutdownTimeout`. When the unload returns the process exits with the status the request gave; when the bound passes first it exits 124. This area supplies the callers and the reason-to-status mapping; it has no stop function, tree or timer of its own, and neither has A20's stdio component.
  2. **One-shot (`headless`).** The first Ctrl-C cancels the turn with cause `user-interrupt` and lets it close in the log; status 130. A second Ctrl-C exits at once.
  3. **Host (`web`) on a terminal.** If nothing would be interrupted, the first Ctrl-C stops. Otherwise it prints what is running (agents, queued input, jobs, due schedules; asked of host services with A18's two-second limit, and treated as running on timeout) and stops only if Ctrl-C is pressed again within `confirmWindow`. A press during shutdown exits at once.
  4. **`dsh stop` and the UI's stop action** ask a person once, where the request came from, unless `--yes`. Without a terminal and without `--yes`, `dsh stop` declines when work is running.
  5. **`SIGTERM`, a closed console, an operating-system shutdown and end of input on a stdio carrier never ask.** They stop at once; a clean result is status 0.
- **Why.** Requirement `DH-LIB-ELECTRON-R3` of [libs/electron.md](../libs/electron.md) and [DH-ADR-AGENT-8](agent-loop.md#dh-adr-agent-8): the final events must reach the log, and a supervisor must never hang on a question. A second key press is used instead of a typed answer because the terminal's input may belong to something else.
- **Tradeoff.** An exit can take up to `ShutdownTimeout`. A forced exit skips cleanups and is handled as a crash. Inference: 20 s is a proposal, not a measurement.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-3](../design.md#dh-o-3), [DH-O-5](../design.md#dh-o-5). **Verified by.** `DH-T-CLI-8`, `DH-T-CLI-9`, `DH-T-ELECTRON-2`, `DH-E-CLI-1`, `DH-E-CLI-2`.

<a id="dh-adr-cli-6"></a>
### DH-ADR-CLI-6 `dsh tool` is for scripts the harness started, not for a person's shell

- **Decision.** `call`, `list` and `describe` work only with the two variables the host put into a script's environment. Without them the command contacts nothing and exits 4 with `tools/endpoint-unavailable`. There is no flag that names a session or selects a host. `dsh tool pick` is a pure helper and works anywhere. A person inspects tools with `dsh catalog tools` and runs them through a session.
- **Why.** A03 left this to this area. A tool call needs an agent, a turn and an answerer to be checked, approved and logged ([DH-ADR-8](../design.md#dh-adr-8)); a call from a bare shell has none, so allowing it would need a second, weaker entry.
- **Tradeoff.** No quick "try this tool" from a terminal.
- **Serves.** [DH-O-6](../design.md#dh-o-6), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-CLI-10`, `DH-T-CLI-11`, `DH-E-CLI-3`.

<a id="dh-adr-cli-7"></a>
### DH-ADR-CLI-7 The headless command answers approvals only from a terminal

- **Decision.** `cli.headless` registers the one `cli` answerer ([interaction-credentials.md §6.2](interaction-credentials.md)) when a controlling terminal can be opened and `--json` is not set. The prompt shows the call, and offers allow once, deny, or allow with a session grant for one of the scopes A17 offers. A grant belongs to the one live session that was asked: it ends when the run ends, a later `--session` resume starts with none, and it is never copied to a fork or a subagent ([DH-ADR-16](../design.md#dh-adr-16)). A subagent's request is asked on the same terminal, because the runner answers for the root session, and is denied when there is no terminal. In every other case no answerer is registered, so an approval settles `unavailable` and the call is denied. This is the rule area A17 states for every interactive profile ([interaction-credentials.md §6.2](interaction-credentials.md): each profile mounts the answerer of its carrier, and `dsh headless` has the `cli` answerer only with a controlling terminal and without `--json`), so a test or scenario that expects "no answerer" in a headless run must run with `--json` or with no controlling terminal. `--approval-policy never` rejects before anyone is asked. A fresh run uses the agent preset registry's default unless `--agent-preset` names one; a resumed run binds the session's recorded agent preset; a recorded one that no longer exists is refused with status 5 before any model call; the runner never falls back to another. The run owns the interval from accepting the task to the agent and its descendants being idle and flushed (A02 leaves that to the caller).
- **Why.** "With nobody to ask, the answer is no" ([DH-O-7](../design.md#dh-o-7)). A piped standard input cannot be trusted to be a person.
- **Tradeoff.** An unattended run that needs an escalation fails that call; the operator chooses a wider permission mode beforehand. A prompt on an abandoned terminal waits until A17's timeout.
- **Serves.** [DH-O-13](../design.md#dh-o-13), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-CLI-12`, `DH-T-CLI-13`, `DH-E-CLI-2`.

<a id="dh-adr-cli-8"></a>
### DH-ADR-CLI-8 The update check is off unless enabled, and only `dsh update` replaces the binary

- **Decision.** Decided by the owner: the automatic update check counts as data leaving the machine and is a setting that is off until turned on ([DH-ADR-32](../design.md#dh-adr-32)); the one off-switch stops senders only, so an explicit `dsh update` always runs and names the address first ([DH-ADR-27](../design.md#dh-adr-27)). This applies both and confirms [DH-ADR-ELECTRONUPDATER-3](../libs/electron-updater.md#dh-adr-electronupdater-3).
  1. The automatic check is the setting `update.check`, off by default. It exists only in a `web` host, registers as sender kind `update-check` at A18's sharing gate, and stops when the off-switch is set.
  2. A check requests the channel's manifest and its signature and nothing else. The request carries no installation id, no version and no session data; the version comparison and the rollout draw are local.
  3. A found release is a notice in the UI and one line at the next `dsh web` start. Nothing is downloaded.
  4. `dsh update` and `dsh update --check` are the user's explicit request and run whatever `update.check` says; each names the address before the first request. They run also while the off-switch is set: the switch stops what the harness would send by itself, and this is something the user asked for.
- **Why.** [DH-ADR-9](../design.md#dh-adr-9) makes everything that leaves the machine opt-in, and the design reads an update check as covered. A command the user types is the product doing what was asked, like a model request.
- **Tradeoff.** By default nobody learns of a release without asking. The off-switch is not a network lock: a user who types `dsh update` reaches the release address whatever the switch says, and is told the address first.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-CLI-15`, `DH-T-ELECTRONUPDATER-2`, `DH-E-ELECTRONUPDATER-1`.

## 8. Deliverables

<a id="dh-d-cli-1"></a>
### DH-D-CLI-1 One command tree: grammar, help, usage errors and a generated reference

- **Result and why it matters.** Every word after `dsh` is declared in one place, and the published reference cannot drift from the binary.
- **How it is implemented.** `cmdline` implements `Spec` and `Parse` on a private `pflag.FlagSet` and never prints or exits. `tree` builds the `cobra` root with interspersed parsing off for the launcher stage, adds each contributed `Command`, resolves a profile when no command matches, and refuses reserved names. Help is rendered from the same `Spec` for fixed commands and apps. `dsh catalog commands` walks the tree and each shipped profile's app `Spec`; a golden test compares it with the committed file.
- **Components and files.** `internal/cli/cmdline`, `internal/cli/tree`, `cmd/dsh`.
- **Depends on.** `DH-LIB-COMMANDER`, `DH-LIB-PFLAG`, [DH-D-BOOT-2](boot.md#dh-d-boot-2) (launch data that carries the app words and the safe-mode flag), [DH-D-BOOT-3](boot.md#dh-d-boot-3) (the list of profiles). Each shipped app's `Spec` is declared by the component that owns its words.
- **Priority.** P0. **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-13](../design.md#dh-o-13). **Done when.** `DH-T-CLI-1`, `DH-T-CLI-2`, `DH-T-CLI-3`, `DH-T-COMMANDER-1`, `DH-T-COMMANDER-2`, `DH-T-COMMANDER-3` and `DH-T-COMMANDER-4` pass.

<a id="dh-d-cli-2"></a>
### DH-D-CLI-2 The profile launchers start a host, a one-shot run or a stdio server

- **Result and why it matters.** `dsh web` is the one launch command; `dsh headless` lets a script create, prompt, answer, cancel and resume a session; `dsh sdk` and `dsh acp` are how programs start the host.
- **How it is implemented.** The launcher resolves the home, hands boot the profile, layers, `--safe` and app words, and waits on boot's readiness signal. For `web`, A18's `launch` prints and opens the URL. The `cli.headless` component creates or resumes the agent with its agent preset, registers each `--add-dir` as a per-session write root through A10's policy resolver (as the ACP carrier does), binds the `cli` channel, sends the task with `Agent.Send` (or dispatches `--command` to A17's registry), waits for idle, flushes, prints the last assistant text of its own interval and calls `launch.App.Stop(Completed, status)`. Under `--json` it follows the session's committed feed and writes export lines.
- **Components and files.** `internal/cli/tree`, `internal/cli/headless`.
- **Depends on.** [DH-D-CLI-1](#dh-d-cli-1), [DH-D-CLI-3](#dh-d-cli-3), [DH-D-CLI-4](#dh-d-cli-4), `DH-LIB-OPEN`, [DH-D-BOOT-5](boot.md#dh-d-boot-5) (boot and readiness), [DH-D-BUNDLE-3](bundle-preset.md#dh-d-bundle-3) (the `web`, `headless` and `sdk` profiles), [DH-D-BUNDLE-4](bundle-preset.md#dh-d-bundle-4) (agent presets), [DH-D-CLI-9](#dh-d-cli-9) (`dsh secret set`, which DH-E-CLI-2 uses), [DH-D-AGENT-1](agent-loop.md#dh-d-agent-1) and [DH-D-AGENT-6](agent-loop.md#dh-d-agent-6) (the agent registry, create, resume, the idle signal), [DH-D-AGENT-2](agent-loop.md#dh-d-agent-2) (`Send`), [DH-D-STORE-7](session-store.md#dh-d-store-7) (the export line format), [DH-D-HUMAN-1](interaction-credentials.md#dh-d-human-1), [DH-D-HUMAN-2](interaction-credentials.md#dh-d-human-2) and [DH-D-HUMAN-3](interaction-credentials.md#dh-d-human-3) (the `cli` channel, approvals, grants), [DH-D-HUMAN-6](interaction-credentials.md#dh-d-human-6) (human commands), [DH-D-SANDBOX-1](sandbox-ssh.md#dh-d-sandbox-1) (per-session roots), [DH-D-HOST-3](host-telemetry.md#dh-d-host-3) (the web launch), [DH-D-SDK-4](sdk-acp-typert.md#dh-d-sdk-4) (the SDK carrier). `dsh acp` and `dsh sdk-minimal` use the same launcher and become usable when [DH-D-BUNDLE-8](bundle-preset.md#dh-d-bundle-8) and [DH-D-SDK-9](sdk-acp-typert.md#dh-d-sdk-9), which are later work, are built; they consume this deliverable. Runtime prerequisite: a configured model route.
- **Priority.** P0. **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-13](../design.md#dh-o-13), [DH-O-7](../design.md#dh-o-7). **Done when.** `DH-T-CLI-12`, `DH-T-CLI-13` and `DH-T-CLI-14` pass and [DH-E-CLI-1](#dh-e-cli-1) and [DH-E-CLI-2](#dh-e-cli-2) have passed. Both are guarded scenarios of [DH-D-CLI-4](#dh-d-cli-4) and run in that build stage.

<a id="dh-d-cli-3"></a>
### DH-D-CLI-3 Every command prints and exits by one contract

- **Result and why it matters.** A script can pipe any result and branch on any status without reading the source.
- **How it is implemented.** `output.Result` is the only writer a command gets; it refuses a second document in JSON mode and detects a closed stream. `output.Status(err)` maps a code's class to a status from a table in which every code of the shared error table must appear.
- **Components and files.** `internal/cli/output`.
- **Depends on.** [DH-D-CLI-1](#dh-d-cli-1), [DH-D-UTIL-5](util-test-support.md#dh-d-util-5) (`herr`, the shared error table). Every area that owns error codes gives each code a class. The real-script check of the statuses needs `dsh tool` and is a proof of [DH-D-CLI-6](#dh-d-cli-6), not of this deliverable.
- **Priority.** P0. **Outcome.** [DH-O-13](../design.md#dh-o-13), [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-CLI-5` and `DH-T-CLI-6` pass.

<a id="dh-d-cli-4"></a>
### DH-D-CLI-4 Stopping is one path with a bound, from Ctrl-C, a signal, end of input, `dsh stop` or the UI

- **Result and why it matters.** A stop always ends the open turn in the log when it can, never hangs, and says so when it had to force.
- **How it is implemented.** `lifecycle.Watch` as [DH-ADR-CLI-5](#dh-adr-cli-5): a signal goroutine (`os/signal`; console control events on Windows) and a state machine (running, confirming, stopping) driven under virtual time in tests, whose only output is a call of `launch.App.Stop`. A18's `host.stop` and A20's stdio component call the same function. The bound and the forced exit are boot's. `dsh stop` uses `hostclient`.
- **Components and files.** `internal/cli/lifecycle`, `internal/cli/hostclient`.
- **Depends on.** [DH-D-CLI-3](#dh-d-cli-3), `DH-LIB-ELECTRON`, [DH-D-BOOT-2](boot.md#dh-d-boot-2) (`launch.App`), [DH-D-BOOT-5](boot.md#dh-d-boot-5) (the bounded shutdown), [DH-D-AGENT-5](agent-loop.md#dh-d-agent-5) and [DH-D-AGENT-6](agent-loop.md#dh-d-agent-6) (cancellation with a cause, the bounded final flush), [DH-D-HOST-3](host-telemetry.md#dh-d-host-3) (`host.stop` on the control endpoint, with a mode that returns the work summary without stopping).
- **Priority.** P0. **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-3](../design.md#dh-o-3), [DH-O-5](../design.md#dh-o-5). **Done when.** `DH-T-CLI-8`, `DH-T-CLI-9` and `DH-T-ELECTRON-2` pass and [DH-E-CLI-1](#dh-e-cli-1) and [DH-E-CLI-2](#dh-e-cli-2) have passed. **Guarded scenarios:** a stop can lose work and a crash leaves a session to be taken over, so DH-E-CLI-1 and DH-E-CLI-2 run inside this deliverable's build stage, before the stage closes, and not in a later acceptance batch; a failure there keeps the stage open.

<a id="dh-d-cli-5"></a>
### DH-D-CLI-5 A second launch finds the host, and a crash is explained at the next start

- **Result and why it matters.** Typing `dsh web` twice gives one host and two tabs; after a crash the user is told what happened and how to start without plugins.
- **How it is implemented.** The client half of A18's host reuse: `hostclient` lists runtime records, tests each lock, and calls `launch.open`, which returns `opened`, `printed` or `launch/no-channel` and never a link. The rules of [DH-ADR-CLI-4](#dh-adr-cli-4) are a pure function from (records, flags) to an action. The unclean-exit line and the report are A18's `diag`; this area supplies `--safe`, the statuses 70 and 78, and the `dsh status` and `dsh crash` words.
- **Components and files.** `internal/cli/hostclient`, `internal/cli/tree`.
- **Depends on.** [DH-D-CLI-1](#dh-d-cli-1), [DH-D-CLI-3](#dh-d-cli-3), `DH-LIB-ELECTRON`, [DH-D-HOST-3](host-telemetry.md#dh-d-host-3) (the runtime record and the control endpoint), [DH-D-HOST-7](host-telemetry.md#dh-d-host-7) (`diag`), [DH-D-BOOT-3](boot.md#dh-d-boot-3) (safe mode).
- **Priority.** P0. **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-12](../design.md#dh-o-12). **Done when.** `DH-T-CLI-7`, `DH-T-ELECTRON-1` and `DH-T-ELECTRON-4` pass and [DH-E-CLI-1](#dh-e-cli-1) has passed.

<a id="dh-d-cli-6"></a>
### DH-D-CLI-6 A script calls, lists and describes tools with `dsh tool`

- **Result and why it matters.** The client half of the tool command line ([DH-ADR-8](../design.md#dh-adr-8)): a loop in any shell can call tools, read one field of a result, and tell "denied" from "failed".
- **How it is implemented.** `toolcmd` builds the argument object locally (document, then `--arg` entries in order), validates that it is one JSON object, opens the endpoint, sends one request line in A03's format and reads one reply line. Output selection is local: the JSON-pointer selector (RFC 6901) is shared with `dsh tool pick`. The token is read from the environment and never printed or logged.
- **Components and files.** `internal/cli/toolcmd`.
- **Depends on.** [DH-D-CLI-3](#dh-d-cli-3), [DH-D-TOOLS-4](tools.md#dh-d-tools-4) (the endpoint, its wire format and its error codes), [DH-D-SDK-1](sdk-acp-typert.md#dh-d-sdk-1) (the line codec, from module `src/sdk`). Runtime prerequisite: a script started by a tool that hosts scripts.
- **Priority.** P1. **Outcome.** [DH-O-6](../design.md#dh-o-6), [DH-O-7](../design.md#dh-o-7). **Done when.** `DH-T-CLI-10` and `DH-T-CLI-11` pass and [DH-E-CLI-3](#dh-e-cli-3) has passed.

<a id="dh-d-cli-7"></a>
### DH-D-CLI-7 Admin commands work on a home with or without a running host

- **Result and why it matters.** Configuration preview, session export and import, plugin directories, the store and the sandbox are operable from a terminal, safely beside a live host, and the published command surface equals what the binary contains.
- **How it is implemented.** The command surface of [§6.2.2](#622-the-command-tree) is a table in `tree`; each row names the deliverable that contributes the command, and a test compares the rows of the deliverables in the build with their contributions, so a missing or extra command fails the build. The admin tree the commands run in is [DH-D-CLI-9](#dh-d-cli-9). `dsh session import-upstream` is contributed by [DH-D-STORE-10](session-store.md#dh-d-store-10) at that deliverable's priority (P2, [DH-ADR-11](../design.md#dh-adr-11)) and is not part of this one.
- **Components and files.** `internal/cli/tree` (the command table); each owner's `command` package.
- **Depends on.** [DH-D-CLI-9](#dh-d-cli-9), [DH-D-BOOT-6](boot.md#dh-d-boot-6) (`dsh config`). The owners whose commands the table lists, each from the deliverable that implements the operation: [DH-D-BOOT-3](boot.md#dh-d-boot-3) and [DH-D-BOOT-9](boot.md#dh-d-boot-9), [DH-D-STORE-6](session-store.md#dh-d-store-6), [DH-D-STORE-7](session-store.md#dh-d-store-7), [DH-D-STORAGE-1](storage.md#dh-d-storage-1), [DH-D-SANDBOX-3](sandbox-ssh.md#dh-d-sandbox-3) and [DH-D-SANDBOX-8](sandbox-ssh.md#dh-d-sandbox-8), [DH-D-CONNECT-2](mcp-lsp-web-connector.md#dh-d-connect-2), [DH-D-HOST-7](host-telemetry.md#dh-d-host-7), [DH-D-SDK-2](sdk-acp-typert.md#dh-d-sdk-2), [DH-D-EXT-1](extensions.md#dh-d-ext-1).
- **Priority.** P1. **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-2](../design.md#dh-o-2), [DH-O-5](../design.md#dh-o-5), [DH-O-11](../design.md#dh-o-11). **Done when.** `DH-T-CLI-3` passes and [DH-E-CLI-4](#dh-e-cli-4) has passed. Deletion and import write to a home, so DH-E-CLI-4 is a guarded scenario: it runs in this deliverable's build stage, not in a later acceptance batch.

<a id="dh-d-cli-9"></a>
### DH-D-CLI-9 An offline command runs in the admin tree beside a live host, and a secret can be stored from the start

- **Result and why it matters.** An `admin` command gets the same locks, leases and redaction as a host and no private file access; `dsh secret set` stores a model key before the first launch, which every P0 real-model scenario needs. It was the P0 part of [DH-D-CLI-7](#dh-d-cli-7).
- **How it is implemented.** `tree` loads the admin tree (the embedded `admin` bundle, with no carrier, agent, model adapter or plugin process) for a class `admin` command and passes typed service access to the owner's `Run`. A command declared read-only gets a view that cannot create files. The first word wired is `dsh secret set`, whose meaning is A17's: the value is read from standard input, never an argument, and never printed.
- **Components and files.** `internal/cli/tree` (the admin tree); `dsh secret` from A17's command package.
- **Depends on.** [DH-D-CLI-1](#dh-d-cli-1), [DH-D-CLI-3](#dh-d-cli-3), [DH-D-BOOT-3](boot.md#dh-d-boot-3) (profiles and embedded bundles), [DH-D-BUNDLE-2](bundle-preset.md#dh-d-bundle-2) (the embedded `admin` bundle), [DH-D-HUMAN-7](interaction-credentials.md#dh-d-human-7) (the secret store and `dsh secret`).
- **Priority.** P0. **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-5](../design.md#dh-o-5). **Done when.** `DH-T-CLI-4` and `DH-T-CLI-16` pass.

<a id="dh-d-cli-8"></a>
### DH-D-CLI-8 `dsh update` replaces the binary, and an optional check only notifies

- **Result and why it matters.** An installation can move to a newer release with one command, and learns of one only if the operator asked for that.
- **How it is implemented.** The `update` package of [libs/electron-updater.md §6](../libs/electron-updater.md), requirements R1 to R10: verifier, source through A23's `egress`, the single-file swap, and the `update.check` component with its schedule. The command asks on the terminal unless `--yes`.
- **Components and files.** `internal/update`, `internal/cli/tree`.
- **Depends on.** [DH-D-CLI-3](#dh-d-cli-3), `DH-LIB-ELECTRONUPDATER`, `DH-LIB-ELECTRONBUILDER` (the signed manifest a release produces), `DH-LIB-NODESEA` (the location of the running binary), [DH-D-HOST-4](host-telemetry.md#dh-d-host-4) (the sharing gate), [DH-D-UTIL-2](util-test-support.md#dh-d-util-2) (`egress`). The notice is drawn by [DH-D-WEBAPP-10](app-web.md#dh-d-webapp-10), which reads `update.Status` and consumes this deliverable. Runtime prerequisite: a published release with a manifest.
- **Priority.** P2. **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Done when.** `DH-T-CLI-15`, `DH-T-ELECTRONUPDATER-1`, `DH-T-ELECTRONUPDATER-2`, `DH-T-ELECTRONUPDATER-3` and `DH-T-ELECTRONUPDATER-4` pass and [DH-E-ELECTRONUPDATER-1](../libs/electron-updater.md#dh-e-electronupdater-1) and [DH-E-ELECTRONUPDATER-2](../libs/electron-updater.md#dh-e-electronupdater-2) have passed. The swap replaces a file, so the second scenario is guarded: it runs in this deliverable's build stage.

## 9. Acceptance

### 9.1 Local tests

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-CLI-1` | DH-D-CLI-1 | Grammar table: options before the word; a fixed command wins over a profile of that name; `--profile` makes every later word an app word; `--help` after a profile reaches the app; an unknown launcher option is status 64 |
| `DH-T-CLI-2` | DH-D-CLI-1 | The reserved-word list equals the tree's commands plus the hidden modes; creating a profile with a reserved name is refused; a hidden mode is dispatched only on the exact first word and is absent from help and completion |
| `DH-T-CLI-3` | DH-D-CLI-1, DH-D-CLI-7 | The committed command table, for the rows whose contributing deliverable is in the build, equals the contributed commands; the generated reference equals the golden file; every command has a description and an example |
| `DH-T-CLI-4` | DH-D-CLI-9 | With a fixture home: a read-only admin command creates no file; an admin command against a session leased by a fixture process gets `session/owned` and status 6; the admin tree mounts no carrier, agent or plugin process |
| `DH-T-CLI-5` | DH-D-CLI-3 | With buffers as streams: logs never reach the result stream; `--json` yields exactly one document or valid JSON Lines; a failure prints one line and, under `--json`, the error object; a closed result stream cancels the work |
| `DH-T-CLI-6` | DH-D-CLI-3 | Every code of the shared error table maps to exactly one status; no command path returns 2; the status table in this document equals the one in source |
| `DH-T-CLI-7` | DH-D-CLI-5 | The second-launch function over fixture records: reuse, refusal for each listener flag, a second host on another address, a stale record replaced, an unreachable endpoint reported with the process id |
| `DH-T-CLI-8` | DH-D-CLI-4 | The stop state machine under virtual time: each reason gives its status; a second request joins; the second Ctrl-C window; a press during shutdown forces; a signal never enters the confirming state |
| `DH-T-CLI-9` | DH-D-CLI-4 | Through the real boot sequence, with a fixture tree whose cleanup never returns: exit at `ShutdownTimeout` with status 124 and boot's line naming the stuck cleanup; with a slow flush inside the bound the last event is durable before exit; over the source, `lifecycle` starts no exit timer, disposes no tree and calls no `os.Exit` |
| `DH-T-CLI-10` | DH-D-CLI-6 | Against a fake endpoint: argument merging order, `field=value` and `field:=json`, a malformed document refused with line and column before any connection, each output mode, `--pick` with and without `--lines`, the token absent from every stream |
| `DH-T-CLI-11` | DH-D-CLI-6 | Each endpoint error code gives the status of [§6.2.2](#622-the-command-tree); without the two variables nothing is dialled and the status is 4; no flag names a session; `tool pick` reads a file and standard input |
| `DH-T-CLI-12` | DH-D-CLI-2 | Headless with a scripted model: text on the result stream only; status 0 only for `completed`; `--json` lines parse as export lines and end with `run/result`; `--session` resumes and an unknown id is status 5; `--command` appends `command/run` and no model message; a progress line naming the step and the model route is on the diagnostic stream before the first token of every step and holds no model output; with a buffer as the diagnostic stream reasoning is absent under `auto` and `hide` and present under `show`, and with a pseudo-terminal it is present under `auto` (U-K2-4) |
| `DH-T-CLI-13` | DH-D-CLI-2 | The `cli` answerer under a pseudo-terminal fixture: allow, deny and grant are recorded with channel `cli`; without a terminal or with `--json` no answerer is registered and the call is denied; policy `never` asks nobody; Ctrl-C at the prompt cancels the turn |
| `DH-T-CLI-14` | DH-D-CLI-2 | The launcher passes home, layers, `--safe` and app words to a fake boot unchanged; a configuration error is status 78 and a required-entry failure names the report |
| `DH-T-CLI-15` | DH-D-CLI-8 | With a recording transport: a fresh home makes no request in any profile; with `update.check` on only the `web` composition requests, and only the manifest and signature, with no identifier or version in the request; the off-switch stops the automatic check and does not stop `dsh update` or `dsh update --check`; `dsh update --check` names the address first and changes nothing |
| `DH-T-CLI-16` | DH-D-CLI-9 | `dsh secret set <name>` with the value on a pipe and on a pseudo-terminal (no echo) stores it through a fake store; a value given as an argument is a usage error (64); the value appears on no stream, in no log line and in no error; the command needs no running host |

### 9.2 Real-environment scenarios

<a id="dh-e-cli-1"></a>
#### DH-E-CLI-1 One launch command, a second launch, Ctrl-C and a crash, on three operating systems

- **Proves.** DH-D-CLI-2, DH-D-CLI-4, DH-D-CLI-5.
- **Given.** Hosted Linux, macOS and Windows machines with a real default browser and a real terminal (a pseudo-terminal the controller drives); the built `dsh`; an empty temporary home; the replay model provider, so no key is needed; a fixture session whose turn runs a shell command that sleeps for two minutes.
- **When.** (1) Run `dsh web`. (2) Run `dsh web` again; then `dsh web --tls-cert a --tls-key b`; then `dsh web --port 0`. (3) Start the sleeping turn in the first host and press Ctrl-C once; wait past the window; press it twice. (4) Start the host and the turn again and send the termination signal (close the console on Windows). (5) Start again, kill the process outright, then run `dsh web`, `dsh status` and `dsh --safe web`.
- **Then.** (1) The browser shows the UI and the result stream holds exactly one URL line. (2) The second run makes the running host open a second tab, prints where to look and no link, binds nothing and exits 0; the third exits 6 naming the process id; the fourth is a second host and both list the fixture session. (3) The first press prints the running work and stops nothing; the double press ends the host with status 130, and the session log holds `turn/end` with the interrupt cause. (4) Exit 0 inside the deadline with the turn closed. (5) The next start prints the unclean-exit line with a report path and the safe-mode hint, the open tab shows "host not reachable" until then, the resumed session shows the interrupted turn closed with unknown tool outcomes, and safe mode reaches the UI. No `dsh` process and no child remains after any step.
- **Cleanup.** Stop every host with `dsh stop --yes`; remove the temporary home. On failure keep the diagnostic streams, the crash directory and a process listing.

<a id="dh-e-cli-2"></a>
#### DH-E-CLI-2 A headless run against a real model: prompt, approval on a terminal, refusal without one, cancel, resume

- **Proves.** DH-D-CLI-2, DH-D-CLI-4.
- **Given.** A Linux and a Windows machine with the platform's real sandbox backend; the built `dsh`; a temporary home; a real model API key supplied at run time through `dsh secret set`; a workspace with one file; permission mode `workspace-write`.
- **When.** (1) On a pseudo-terminal, run `dsh headless` with a task that must write outside the workspace, and answer "allow once". (2) Run the same task with standard streams piped and no terminal. (3) Run it with `--json`. (4) Run a long task and press Ctrl-C mid-turn. (5) Run `dsh headless --session <id> "continue"`.
- **Then.** (1) The prompt appears on the diagnostic stream, the write happens, the answer is the only text on the result stream, status 0, and the log names channel `cli`. (2) No prompt; the call is recorded as denied with `unavailable`; the file is unchanged; the captured diagnostic stream holds a progress line before the first token and no reasoning text. (3) Every result line parses as an export line and the last is `run/result`. (4) Status 130 and a closed turn in the log. (5) The resumed run sees the earlier turns and exits 0.
- **Cleanup.** Remove the home, the workspace and the outside file. On failure keep both streams and the exported session.

<a id="dh-e-cli-3"></a>
#### DH-E-CLI-3 Real shell scripts see the documented statuses from `dsh tool`

- **Proves.** [DH-D-CLI-6](#dh-d-cli-6); it also exercises the status table of [DH-D-CLI-3](#dh-d-cli-3) from a real script.
- **Given.** The machines and model of DH-E-CLI-2; script calling enabled for the agent (A11); `bash` on Linux and macOS, PowerShell on Windows. This scenario complements A03's `DH-E-TOOLS-2`, which proves the endpoint under each sandbox backend.
- **When.** The model is prompted to run a script that calls a file-reading tool with `--arg`, picks one field with `--pick`, calls a tool that policy denies, calls an unknown tool, and prints each status. Separately a person runs `dsh tool call` and `dsh tool list` in an ordinary shell.
- **Then.** The script's output shows statuses 0, 3 and 5 in that order and the picked field as raw text; the session log holds one nested call per `call`, the denied one with its denial; the person's shell gets status 4 and no connection is attempted.
- **Cleanup.** As DH-E-CLI-2.

<a id="dh-e-cli-4"></a>
#### DH-E-CLI-4 Admin commands beside a live host on the same home

- **Proves.** DH-D-CLI-7.
- **Given.** One machine per operating system; the built `dsh`; a temporary home on a local disk; a web host running with one session mid-turn (the replay provider) and one idle session; a local plugin directory fixture.
- **When.** From a second terminal: `dsh config show web`, `dsh config validate` on a layer with a deliberate error, `dsh session list --json`, `dsh session export` of both sessions, `dsh session delete` of each, `dsh store check`, `dsh plugin inspect` and `dsh plugin add` of the fixture.
- **Then.** `config show` attributes each value to a layer and creates no file; `validate` exits 5 with file, line and column, and the host's composition is unchanged; both exports succeed and re-import into a second empty home with equal event counts; deleting the mid-turn session exits 6 with `session/owned` and deleting the idle one succeeds and disappears from the host's list; `store check` exits 0; `plugin inspect` prints the manifest and requested capabilities without starting the plugin.
- **Cleanup.** `dsh stop --yes`; remove both homes. On failure keep both homes as archives.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

**Recommended defaults, not confirmed by the owner.**
1. [DH-ADR-CLI-4](#dh-adr-cli-4), clauses (a) to (e): a second `dsh web` on the same address reuses the host, and another address may start a second one. (That the second command is never given the link is decided, [DH-ADR-28](../design.md#dh-adr-28).)
2. `dsh` started by a double-click on Windows opens a console that closes at once. The binary does nothing special, and the notice [libs/commander.md §6](../libs/commander.md) describes stays off; the documentation says to run `dsh web` from a terminal.
3. `dsh connector skills install` takes a local path and fetches nothing, and the cloud sign-in components are behind a build tag and absent from the default binary; `dsh version` lists the build tags, so a build that has them says so.

**Limitations.**
- There is no `dsh share` command. Live sharing is kept ([DH-ADR-37](../design.md#dh-adr-37): no relay, viewers only watch), and [x-liveshare-webworker.md](x-liveshare-webworker.md) creates, lists and stops a share in the UI and asks for no command; if it later wants one it contributes it as a `cmdline.Command`.
- A second launch cannot hand a link to its caller, so a script that wants a link for another machine must read it from the running host's terminal.
- The statuses and the `--json` field names are a compatibility surface from the first release; the text output is not.
- Running `dsh web` as a per-user service is documentation, not a command: nothing here installs a service, and a service has no terminal, so its stop path is the signal path.
- A terminal UI is not designed; upstream's examples mention a `tui` profile that it does not ship.
- All defaults in [§6.2.6](#626-config) are proposals without measurement.

## 11. Units dispositioned by this spec

1 `UP-PKG` (ported), 7 `UP-CLI` (6 ported, 1 replaced), 4 `UP-DEC` (2 ported, 1 replaced, 1 not applicable), and 3 `UP-DEBT` defined here (`UP-DEBT-found.A24.1` to `.3`, all resolved). Go items: 8 decisions, 9 deliverables, 16 tests, 4 scenarios. Rows are in [../trace/fragments/A24.jsonl](../trace/fragments/A24.jsonl).
