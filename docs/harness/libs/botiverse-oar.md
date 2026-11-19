# @botiverse/oar: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference".
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The earlier revision
> dropped the OAR runtime bridge. **The repository owner keeps it**: the Go design must
> have a path to the external agent runtimes upstream reaches through `oar`. This
> revision specifies that path. Nothing about the OAR product beyond what the upstream
> checkout shows could be verified; section 7 lists what remains unconfirmed.

| | |
|---|---|
| **Library ID** | `DH-LIB-OAR` |
| **Upstream unit** | `UP-DEP-@botiverse/oar` |
| **Upstream version** | 0.67.0, Apache-2.0 (`THIRD_PARTY_NOTICES.md:38`), with a local patch |
| **Kind** | development dependency whose patched code is copied into the provider's bundle at build time (`apps/desktop/scripts/runtime-patch-policy.ts:11`; `packages/experimental/subagent-oar/package.json:58`) |
| **Used by** | experimental (`packages/experimental/subagent-oar`) |
| **Go choice** | **own code** that drives an external program. A Go `oar` subagent provider runs the OAR that the operator installed as a child process owned by the host, and talks to it over a line-delimited JSON channel. Nothing of OAR is linked, bundled, downloaded or patched by the harness ([DH-ADR-OAR-1](#dh-adr-oar-1)) |
| **Related areas** | the subagent providers (areas A12 and X5; this document's "subagent area"); [anthropic-ai-claude-agent-sdk.md](anthropic-ai-claude-agent-sdk.md), [openai-codex.md](openai-codex.md), [agentclientprotocol-sdk.md](agentclientprotocol-sdk.md) |

## 1. What it is and why upstream needs it

OAR is a JavaScript library that puts one programming interface in front of several
coding-agent command-line programs. Each supported program is a **runtime**. The library
finds the installed program, starts it, sends a prompt, reports its output as events and
stops it. Its default registry names seven runtimes: `antigravity`, `claude`, `codex`,
`grok`, `kimi`, `opencode` and `pi` (`packages/experimental/subagent-oar/README.md:93`).

Upstream uses it for one experimental **subagent provider** (a registered way to run a
delegated task) so that a single tool, `subagent_oar`, can choose a runtime per call.
This document calls that provider **the OAR runtime bridge**: it is the piece that
connects the harness's delegation service to OAR's runtimes. Unlike the two product
providers, it uses the programs the user installed, with their own login and settings
(`packages/experimental/subagent-oar/README.md:12`, `:30`). Only `codex` and `claude`
are tested; the other five "remain unverified" (`:93`).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/experimental/subagent-oar/src/index.ts:10`, `:60` | Look up the configured runtime by name when the plugin loads; an unknown name fails the load | `defaultRuntimes.require` |
| `packages/experimental/subagent-oar/src/run.ts:301` | Ask whether the program is installed, using the run's environment | `runtime.installation({ env })` |
| `packages/experimental/subagent-oar/src/run.ts:317` to `:323` | Start one native session with working directory, model, effort, environment and extra launch arguments | `runtime.session(installation, options)` |
| `packages/experimental/subagent-oar/src/run.ts:225` to `:231`, `:237`, `:245` | Send one prompt, collect root-level text, wait for the end of the turn | `session.events`, `session.prompt`, `awaitTurnEnd` |
| `packages/experimental/subagent-oar/src/run.ts:205`, `:158` | Interrupt and dispose | `session.abort`, `session.dispose` |

OAR starts and owns the native processes itself. They do not go through the harness
subprocess service, so the harness's sandbox and process-tree cleanup do not apply to
them (`packages/experimental/subagent-oar/README.md:163`). Permissions are whatever
launch arguments the profile passes; no approval reaches a person (`:105`, `:161`).

### 2.1 What the bridge does, step by step

This is the behaviour the Go design must keep. Every step is read from
`packages/experimental/subagent-oar/src/run.ts` unless another file is named.

1. **Accept only a text task.** An empty task, a blank task or a task with a non-text
   block is refused before anything starts (`:129` to `:144`).
2. **Probe the installation** with the run's environment, and stop with "runtime
   unavailable" when the program is missing or unsupported (`:299` to `:313`).
3. **Open one native session** in the resolved child directory with the instance's
   model, effort, environment and launch arguments (`:315` to `:323`).
4. **Collect the answer.** Only text events of this session at the root of its agent
   tree count. When the runtime labels messages, the answer is the last non-empty
   message; otherwise it is all root text joined (`:195` to `:201`, `:225` to `:231`;
   `README.md:107`). Reasoning, tool events and transcript history never reach the parent
   session (`README.md:107`).
5. **Send one prompt and wait for the turn to end.** A prompt the runtime rejects, a
   failed turn and an aborted turn each map to a fixed result (`:166` to `:181`, `:235`
   to `:247`).
6. **Cancel and time out.** Cancelling settles the run as aborted at once, asks the
   runtime to abort, and disposes the session; an optional deadline that starts after
   the session opened does the same with a fixed diagnostic (`:210` to `:223`, `:234`).
7. **Always dispose**, and report failures from a closed list of six codes whose
   messages hold only a stage and a category, because native error text "can contain
   secrets" (`:55` to `:113`).
8. **Build the environment** from the host's scrubbed environment plus explicit
   overrides, where `null` removes a variable
   (`packages/experimental/subagent-oar/src/environment.ts:10` to `:19`).

The provider creates no harness child session and offers no follow-up or resume
(`README.md:73`; `src/index.ts:82` to `:83`). Its configuration is one runtime id, a
provider name, and optional model, effort, launch arguments, environment and deadline
(`README.md:79` to `:87`). A second package holds the tool that maps a model-visible
alias to a provider instance; the model picks an alias and cannot change launch
arguments or permissions (`packages/experimental/tool-subagent-oar/README.md:12`).

### 2.2 What the upstream checkout shows about OAR itself

The package's files are not in the checkout (no `node_modules`). What can be read:

| Fact | Evidence |
|---|---|
| It needs Node 24 or later | `pnpm-lock.yaml:21156`; `.agents/notes/implemented/process/2026-10-15-node-24-runtime-floor.md:13` |
| It drives Claude Code "in print mode over stream-json", assigning the session id itself | `patches/@botiverse__oar@0.67.0.patch:7` to `:12`, `:24` |
| It starts Claude Code with `--dangerously-skip-permissions` by default; its own comment calls this "YOLO by default (repo policy, 2026-08-24)" | `patches/@botiverse__oar@0.67.0.patch:18` to `:21` |
| It drives Codex by starting its app-server and sending `initialize` and a thread start over a request and reply protocol, with approval policy `never` | `patches/@botiverse__oar@0.67.0.patch:33` to `:41`, `:53` to `:57`; `README.md:105` |
| It finds a program through a pinning environment variable, then `PATH` with fallbacks, then a readiness command and `--version` | `patches/@botiverse__oar@0.67.0.patch:138` to `:163` |
| It depends on the Agent Client Protocol SDK, on the `pi` coding agent as a library, and on `cross-spawn`, `undici` and `which` | `pnpm-lock.yaml:29038` to `:29044` |
| **A separate "OAR CLI" exists.** Upstream says only that it is not needed: "The provider embeds the OAR library, so no separate OAR CLI installation is required" | `packages/experimental/subagent-oar/README.md:12` |

Upstream never runs the OAR command line. Its commands, flags and output format appear
nowhere in the checkout. Inference: from the dependency list, some runtimes are reached
over the Agent Client Protocol and `pi` runs inside the OAR process as a library; neither
is shown by upstream's own use.

## 3. Tech debt upstream carries

Search performed: every import of the package; the patch file; `pnpm-workspace.yaml`;
the provider README's limitations.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-patch.@botiverse__oar@0.67.0` | A local patch with three fixes, described below | `patches/@botiverse__oar@0.67.0.patch`; `pnpm-workspace.yaml:113` | Resolved by [DH-ADR-OAR-1](#dh-adr-oar-1): the harness neither ships nor edits OAR, so there is no file to patch. Each fix is kept as a requirement (`DH-LIB-OAR-R1` to `-R3`) that the bridge meets from outside OAR, by owning the child process, and that the two product providers meet in their own code. Each has a test. |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@botiverse/oar@0.67.0` | Release-age exemption: "Native Codex and Claude tests qualify this exact release with its local patches" | `pnpm-workspace.yaml:58` to `:59` | Resolved by [DH-ADR-OAR-1](#dh-adr-oar-1): building the harness downloads no OAR package, so no package-age rule applies and nothing is exempted. Which OAR release runs becomes the operator's choice; the bridge reports the version it found and refuses one outside its supported range (`DH-LIB-OAR-R10`). |

**What upstream had to fix in OAR.**

1. **A requested permission mode was overridden by "skip all permissions".** OAR always
   started Claude Code with `--dangerously-skip-permissions` and listed
   `--permission-mode` as an argument callers may not pass, because the first overrides
   the second. The patch allows `--permission-mode` and adds the skip-all flag only when
   the caller did not choose a mode (`patches/@botiverse__oar@0.67.0.patch:9`, `:21` to
   `:23`). Without the patch the harness could not run Claude Code with any restriction.
   Even patched, a Claude instance configured without a mode still gets the bypass
   (`packages/experimental/subagent-oar/README.md:89`).
2. **A failed Codex start left the process running, or reported failure before it was
   gone.** The patch kills the app-server when `initialize` fails, and after every kill
   on a start failure it waits for the process to exit before the error is returned
   (`patches/@botiverse__oar@0.67.0.patch:38` to `:48`, `:56`, `:67`). Upstream's test
   states the goal: the process is released "before DSH releases its activation slot"
   (`packages/experimental/subagent-oar/tests/startup-cleanup.e2e.ts:1`).
3. **The "is it installed" check ignored the run's environment.** The probe ran the
   program with the host's environment, not the scrubbed and overlaid one the session
   would get. The patch passes the environment into the readiness and version commands
   (`patches/@botiverse__oar@0.67.0.patch:80` to `:82`, `:105` to `:108`, `:139` to
   `:160`).

Limits that remain after the patch: opening a session cannot be cancelled, so a cancel
waits for it (`packages/experimental/subagent-oar/README.md:162`); unexpected Codex
approval requests are recorded but never answered, so the run can hang until a timeout
(`:161`); and the native processes are outside the harness's sandbox and cleanup
(`:163`). Their units belong to the subagent area; the bridge design below removes the
first and the third and bounds the second.

## 4. Requirements for the Go implementation

The first four hold for **every** provider that starts an external agent, and are cited
by the two product specifications. The rest are specific to the bridge.

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-OAR-R1` | An external agent is never started with its permission checks turned off unless the operator's configuration says so in words. No default and no omission selects that state. | patch fix 1 |
| `DH-LIB-OAR-R2` | When starting an external agent fails at any step, the process is stopped and has exited before the failure is reported and before the delegation's capacity slot is freed. | patch fix 2 |
| `DH-LIB-OAR-R3` | Any check the provider runs on an external program before a task (found, version, ready) uses the same environment the task would use. | patch fix 3 |
| `DH-LIB-OAR-R4` | Starting an external agent can be cancelled at every step, and a request from the agent that the provider does not answer fails the run instead of waiting. | the two remaining limits above |
| `DH-LIB-OAR-R5` | The bridge is one provider type with any number of named instances. Each instance is bound to one runtime id and fixes model, effort, launch arguments, environment overlay and deadline. A selector tool maps a model-visible alias to an instance; the model cannot change an instance's launch arguments or permissions. | `src/index.ts:44` to `:52`, `:80` to `:86`; `packages/experimental/tool-subagent-oar/README.md:12` |
| `DH-LIB-OAR-R6` | One delegation is one fresh native session for one non-empty text task in the resolved child directory. The answer is the runtime's root-level text: the last non-empty message when messages are labelled, otherwise all root text joined. Reasoning, tool traffic and native history never enter the parent session. There is no harness child session, follow-up or resume. | section 2.1, steps 1, 4 and 5; `README.md:73` |
| `DH-LIB-OAR-R7` | The bridge child and every process below it, including the native agent, are started through the harness spawn helper and are owned by the run. Cancel, deadline, a failed start and disposal end the whole process tree, and disposal returns only when it is gone. | change of `README.md:163` |
| `DH-LIB-OAR-R8` | The bridge child's own process environment is the scrubbed host environment plus the instance's explicit overlay, in which `null` removes a variable. Everything the child starts, probes included, therefore inherits the run's environment. | `src/environment.ts:10` to `:19`; patch fix 3 |
| `DH-LIB-OAR-R9` | A run ends in one of a closed set of outcomes: completed, aborted, or a failure whose kind is one of invalid task, bridge unavailable, runtime unavailable, start failed, turn failed, deadline, protocol, cleanup failed. The parent session receives fixed words with the stage and category only. Native error text goes to the local log at debug level and never to the model. | `src/run.ts:55` to `:113`; `README.md:107` |
| `DH-LIB-OAR-R10` | A missing prerequisite (Node, OAR, the native agent, or a version outside the supported range) never fails host start-up. The provider registers; each delegation fails as "bridge unavailable" or "runtime unavailable", naming what is missing and where the bridge looked. The bridge reports the Node and OAR versions it found. | `README.md:30`, `:160` |
| `DH-LIB-OAR-R11` | Every byte from the bridge child is untrusted: a line over the size bound, a line that is not a known message, or a message out of order ends the run as a protocol failure and the child is stopped. The host never blocks on the child. | Inference from [DH-ADR-WIRE-5](../upstream/wire-and-api.md#dh-adr-wire-5); the child runs third-party code |

**How the bridge meets `-R1` to `-R4`.** The product providers meet them in their own
code: `-R1` by `DH-LIB-CLAUDEAGENTSDK-R4` and `DH-LIB-CODEX-R5`; `-R2` by
`DH-LIB-CLAUDEAGENTSDK-R9` and `DH-LIB-CODEX-R9`; `-R3` by `DH-LIB-CLAUDEAGENTSDK-R2` and
`DH-LIB-CODEX-R2`; `-R4` by `DH-LIB-CODEX-R5`, `DH-LIB-CODEX-R9` and
`DH-LIB-CLAUDEAGENTSDK-R9` (see
[anthropic-ai-claude-agent-sdk.md](anthropic-ai-claude-agent-sdk.md) and
[openai-codex.md](openai-codex.md)). The bridge cannot change OAR, so it meets them from
outside:

| Requirement | How the bridge meets it | What stays weaker than in the product providers |
|---|---|---|
| `-R1` | An instance loads only if its configuration states a permission posture: either launch arguments that select a native permission mode from a per-runtime table the bridge owns (for example `--permission-mode <mode>` for `claude`, a `sandbox_mode` setting for `codex`: `README.md:41` to `:53`), or the literal setting `allowPermissionBypass: true`. An instance with neither is refused at load, because OAR's own default is to skip permission checks (section 2.2). | OAR 0.67.0 without upstream's patch refuses `--permission-mode` for Claude Code (patch fix 1). With such a release a restricted `claude` instance cannot be honoured, and the bridge fails the delegation as "start failed" instead of running with the bypass. Whether later OAR releases accept the argument is unconfirmed. |
| `-R2` | `DH-LIB-OAR-R7`: on any start failure the host stops the bridge child's process tree and waits for it before reporting. It does not rely on OAR waiting for its own children. | Nothing. |
| `-R3` | `DH-LIB-OAR-R8`: the environment is set on the bridge child itself, so OAR's probes cannot see anything else, patched or not. | Nothing. |
| `-R4` | Cancelling during start stops the process tree; the host does not wait for OAR to finish opening. An approval or input request that OAR reports is answered by failing the run. | OAR may swallow a native request without reporting it (`README.md:161`). The bridge then cannot see it, so every instance has a turn deadline; "no deadline" is not offered, unlike upstream (`README.md:87`). |

## 5. Go choice

<a id="dh-adr-oar-1"></a>
### DH-ADR-OAR-1 The OAR runtime bridge is kept: a Go provider drives the operator-installed OAR as a host-owned child process

- **Decision.** Decided by the repository owner on 2026-11-18, overruling this
  document's earlier recommendation to drop the library, and recorded for the whole
  design as [DH-ADR-23](../design.md#dh-adr-23). That record reads "kept" as "a path to
  the runtimes OAR reaches", not "the library itself", and leaves the path to this
  document; areas X5 and A12 confirm it. The Go harness has an `oar`
  subagent provider that reaches the runtimes of OAR's registry. It does so by starting
  the OAR that the operator installed as a **bridge child**: a child process of the
  host, started through the one spawn helper, that speaks a small line-delimited JSON
  protocol on its standard input and output. The harness ships no OAR code, links no OAR
  module and applies no patch.

  ```
  dsh host (Go)                     bridge child                      native agent
  oar provider instance  --spawn--> operator's Node running    --->   claude, codex,
     owns the run and               the operator's OAR                opencode, ...
     the process tree    <--JSON--  (one per delegation)              (started by OAR)
       lines on stdio
  ```

- **How the bridge child is entered.** Two entries satisfy the same protocol, and the
  rest of the design does not depend on which is used.
  1. **A bridge script (specified now).** One small JavaScript file, embedded in the
     `dsh` binary as an asset, is run by the operator's Node. It imports the
     operator-installed `@botiverse/oar` and translates the protocol into exactly the
     library calls upstream uses: `defaultRuntimes.require`, `runtime.installation`,
     `runtime.session`, `session.events`, `session.prompt`, `awaitTurnEnd`,
     `session.abort`, `session.dispose` (section 2). This is the only interface to OAR
     for which the upstream checkout holds evidence, so it is the entry the plan builds.
  2. **The `oar` command line (preferred if confirmed).** If the OAR command line has a
     non-interactive mode that can open one session for a named runtime, take one prompt
     and stream events as lines, the bridge runs it directly and the harness ships no
     JavaScript for this feature. Upstream shows only that such a command line exists
     (section 2.2), so this entry is recorded as an open question, not designed.
- **The protocol between host and bridge child.** Messages are JSON objects, one per
  line, on the codec of [DH-ADR-WIRE-5](../upstream/wire-and-api.md#dh-adr-wire-5).
  Inference: the message set below is the smallest that carries section 2.1; names are
  illustrative and the subagent area fixes them.

  | Direction | Message | Meaning |
  |---|---|---|
  | child to host | `ready` | The bridge started. Carries the bridge revision, the OAR version and the runtime ids OAR registers |
  | host to child | `open` | Runtime id, child directory, model, effort and launch arguments. The environment is not in the message: it is the child's own (`DH-LIB-OAR-R8`) |
  | child to host | `installation` | `available` with a version, `not_found` or `unsupported` |
  | child to host | `opened` | The native session exists |
  | host to child | `prompt` | The task text and an input id |
  | child to host | `text` | Root-level text, with a message id when the runtime gives one |
  | child to host | `request` | The native agent asked for an approval or for input. The host answers by ending the run |
  | child to host | `turn` | `completed`, `aborted`, `failed` with a category, or `rejected` with a code |
  | host to child | `abort`, `close` | Interrupt the turn; dispose the session and exit |

  One bridge child serves one delegation and exits after `close` or when its input
  closes. Standard error is not part of the protocol; a bounded tail is kept for the
  local debug log only.
- **What the operator must have installed.** The harness installs none of these.

  | Prerequisite | Why | Evidence |
  |---|---|---|
  | Node.js 24 or later, found from configuration or on `PATH` | OAR is a JavaScript library and declares this floor | `pnpm-lock.yaml:21156` |
  | The `@botiverse/oar` package, in a directory named in configuration or resolvable by that Node | It is the adapter being driven | section 2 |
  | Each native agent the instances name (for example Claude Code, Codex), installed and signed in | OAR starts the installed program with the user's own login and settings; it installs and authenticates nothing | `README.md:30`, `:160` |

  An API credential a native agent needs from the environment must be given in the
  instance's `env`, because ambient credential-shaped variables are scrubbed
  (`README.md:91`).
- **Why.** The owner requires a path to these runtimes. A child process is the only path
  that needs no reimplementation of seven adapters and no unverified Go module. It also
  repairs what upstream lists as limits: the host owns the whole process tree, so a
  failed start, a cancel and a deadline are enforced by the host instead of by OAR, and
  the run's environment is the child's environment by construction (section 4). The
  three patch fixes become properties of the process boundary instead of edits to a
  distributed file.
- **Tradeoff.**
  - The bridge needs Node and an npm package on the operator's machine. The core `dsh`
    binary stays pure Go; the feature is simply unavailable where they are absent
    (`DH-LIB-OAR-R10`).
  - Under entry 1 the repository holds one JavaScript file that no Go tool checks. It is
    tested only by the real-environment scenario and by the stand-in tests of the
    protocol.
  - The operator chooses the OAR release. Upstream verified one release, with a patch;
    the repository's newest tag on 2026-11-18 is 53 minor versions later. The library
    calls the script uses may have changed, so the bridge states a supported range and
    refuses others.
  - OAR's default is to skip permission checks. The bridge refuses to start an instance
    that does not state its permission posture, and cannot restrict Claude Code through
    an OAR release that rejects the mode argument (section 4).
  - Five of the seven runtimes were never verified upstream and are not verified here.
  - For `claude` and `codex` the product providers remain the better path: no Node, a
    permission mode that is always explicit, and host-side parsing of the agent's own
    output. The bridge accepts those two ids because upstream's does and the owner kept
    the bridge; the selector tool can map an alias to either kind of provider.
- **Verdict on upstream.** *Keep* the provider, its one-shot contract, per-instance
  configuration, the selector tool and the closed failure list. *Change* process
  ownership (the host, not OAR), cancellation during start, and the optional deadline
  (now required). *Drop* the embedded copy, the patch and the release-age exemption.
- **Serves.** DH-ADR-1 (all of upstream is covered), DH-ADR-4 (the host owns every child
  process), [DH-O-9](../design.md#dh-o-9) (delegated work has one owner and settles only
  after cleanup).
- **Verified by.** `DH-T-OAR-1` to `-4` against a stand-in bridge child, and
  `DH-E-OAR-1` against a real OAR and a real native agent. `DH-LIB-OAR-R1` to `-R4` are
  also proved for the product providers by `DH-T-CLAUDEAGENTSDK-1`, `-2`, `-4` and
  `DH-T-CODEX-1`, `-3`, `-4`.

**Options considered and not taken.**

| Option | Why it was not taken | Verified facts |
|---|---|---|
| Drop the bridge; reach Claude Code and Codex through their own providers and other agents through the ACP provider (this document's earlier choice) | Overruled by the owner. It left five runtimes with no path unless the product has an ACP mode | Section 2: only two runtimes have upstream tests |
| Link OAR as a Go module | No Go module could be verified. `go list -m -json github.com/botiverse/oar@latest` resolves the repository tag v0.120.0 (2026-11-18), but `go mod download` fails because the checksum database has no record of it (`sum.golang.org` answers 404). That check was not bypassed. Inference: OAR is distributed as compiled JavaScript (the patch edits `dist/runtimes/claude/launch.js`), so the tag is unlikely to hold a Go package | Both commands run on 2026-11-18 |
| Rewrite OAR's runtime adapters in Go | Seven adapters, five of them for products nobody has tested here, each tracking a vendor's private command-line protocol. A single adapter can still be written later as an ordinary provider, as was done for Claude Code and Codex | none |
| Speak "OAR's protocol" directly from Go | OAR has no protocol of its own that upstream shows: it is called as a library in the same process. The protocols it speaks are each vendor's (section 2.2) | `src/run.ts:16` to `:17` |

## 6. Design notes

**Shape.** No public API beyond the subagent provider contract. Inside the provider: a
locator for Node and OAR (configured path, else `PATH`), an instance validator (runtime
id, permission posture, deadline), the protocol reader, the answer collector of
`DH-LIB-OAR-R6`, and the shared one-run lifecycle the other external providers use
(start, publish, settle, dispose). Inference: upstream checks the runtime id against
OAR's registry when the plugin loads (`src/index.ts:60`). The Go bridge cannot ask OAR
without starting Node, so it checks the id's syntax at load and its existence at the
first delegation, from the list in `ready`.

**Ownership and access.**

| Resource | Class | Owner | Released |
|---|---|---|---|
| Bridge child and every process below it | created | the run, through the spawn helper; the run is owned by the provider component's fiber | on settle, cancel, deadline or unload: stop request, grace period, kill of the process tree; disposal returns after it is gone |
| The bridge script file, written to a private directory under the harness home | created | the provider component | on unload; rewritten when its embedded hash differs |
| The operator's Node, OAR and native agents | shared | the operator | never installed, upgraded or modified; the harness has access, not ownership |
| The native agent's login, settings and any transcript it keeps | shared | the user | never read, copied or deleted by the harness |
| Files the native agent changed in the workspace | uncertain after a cancel | the user's workspace | not rolled back (`README.md:164`) |

The parent agent has access to the result and to cancellation only.

**Failure, retry, cleanup.**

| Moment | What happens | What is left uncertain |
|---|---|---|
| Node or OAR missing, or a version outside the range | "bridge unavailable" before any task text is sent (`DH-LIB-OAR-R10`) | Nothing |
| Native agent missing or unsupported | "runtime unavailable" after the `installation` message; the child is closed | Nothing |
| Failure or cancel while the session opens | The process tree is stopped and awaited, then the failure or the cancel is reported (`-R2`, `-R4`) | Whether the native agent created a session record of its own |
| Turn fails, is rejected, or the agent asks for approval or input | The run settles as "turn failed" with the category; the tree is stopped | Workspace changes made before the failure stay |
| Deadline | As cancel, with the fixed deadline diagnostic | Workspace changes stay |
| Malformed or oversized output from the child | "protocol"; the tree is stopped (`-R11`) | Workspace changes stay |
| The tree does not die | "cleanup failed" is reported with the process id; the instance refuses new runs until the process is gone | **Uncertain:** the process may still be changing files |
| Host dies mid-run | The child dies with the host (job object on Windows, parent-death signal elsewhere, per the spawn helper). On resume the delegation is closed as "outcome unknown" | Whether the native agent finished |

Nothing is retried automatically: a delegation has side effects in the workspace.

## 7. Risks and open questions

What remains unconfirmed, and who can settle it:

1. **The OAR command line.** Its existence is stated upstream; its commands and output
   are not. Until someone reads its documentation, entry 2 of
   [DH-ADR-OAR-1](#dh-adr-oar-1) cannot be designed. For the subagent area.
2. **Which OAR releases the bridge script works with.** The library calls are verified
   only as upstream uses them at 0.67.0, and upstream applied a patch. Whether an
   unpatched 0.67.0, or any later release, exposes the same calls is unknown. The
   supported range must be set by running `DH-E-OAR-1`.
3. **Whether current OAR lets a caller restrict Claude Code's permissions** without a
   patch (section 4, `-R1`).
4. **How Node resolves the operator's OAR package** from a script that lives in the
   harness home. Inference: a configured package directory is needed, because a global
   npm install is not on Node's default resolution path for a script elsewhere.
5. **The five unverified runtimes** (`antigravity`, `grok`, `kimi`, `opencode`, `pi`).
   The design gives them a path; nobody has shown that it works.
6. **No Go module** for OAR could be verified (section 5, options table).
7. **Sandbox.** The bridge child is started by the spawn helper, but which sandbox
   policy applies to a native agent that needs the network and the user's login files is
   for the security and subagent areas. Upstream applies none (`README.md:163`).
8. For the subagent area: the selector tool's name, and whether one selector serves
   bridge instances and product providers together.

## 8. Verification

Local tests use a **stand-in bridge child**: a test executable that speaks the protocol
of section 5 and can be scripted to misbehave. They prove the Go provider, not OAR.
Nothing here has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-OAR-1` | local | `DH-LIB-OAR-R5`, `-R6`, `-R9` | Two instances with different runtime ids and a selector: an alias reaches its instance and no call can alter launch arguments. An empty, blank or non-text task is refused before a child starts. Scripted streams give the tabled answer (last labelled message; joined text when unlabelled; nested-agent text ignored) and the tabled outcome for completed, aborted, failed and rejected. A secret planted in the child's stderr and in a failure message never appears in the result. |
| `DH-T-OAR-2` | local | `DH-LIB-OAR-R2`, `-R4`, `-R7` | A stand-in that starts a grandchild and ignores a polite stop. A failed open, a cancel while opening, a cancel during the turn, a deadline and an approval `request` each end with both processes gone before the result is reported; a second dispose is a no-op; unloading the provider component disposes a live run. |
| `DH-T-OAR-3` | local | `DH-LIB-OAR-R1`, `-R3`, `-R8`, `-R10` | An instance with no permission posture is refused at load; one with a tabled mode argument, and one with `allowPermissionBypass: true`, load. The stand-in records its own environment: it equals the scrubbed host environment plus the overlay, and a `null` entry is absent. With Node or the package path missing the provider still loads and a delegation fails naming what is missing. |
| `DH-T-OAR-4` | local | `DH-LIB-OAR-R11` | A stand-in that sends an oversized line, a line that is not JSON, an unknown message, `text` before `opened`, and output after `turn`: each ends the run as "protocol", the child is stopped, and the host's other runs continue. |
| `DH-E-OAR-1` | real environment | `DH-LIB-OAR-R1`, `-R3`, `-R6`, `-R7`, `-R10` | See below. |

<a id="dh-e-oar-1"></a>
### DH-E-OAR-1 Delegation through a real OAR to a real installed agent

- **Given.** A machine with Node 24 or later, a released `@botiverse/oar` in a known
  directory, and Codex and Claude Code installed and signed in. The built `dsh` binary
  with two bridge instances: `codex` with a workspace-write sandbox argument, and
  `claude` with a `dontAsk` permission mode. A temporary workspace holding `input.txt`
  with a random line. The identity is the tester's own agent login, supplied at run
  time and never written to the repository.
- **When.** Through the selector tool, each instance in turn is asked to copy
  `input.txt` to `output.txt` and reply with a fixed word (the task of upstream's
  `tests/native.e2e.ts:14` to `:50`). Then a third delegation is cancelled two seconds
  after it starts. Then the Node path is pointed at a missing file and a fourth
  delegation is made.
- **Then.** The first two results are `completed`, the reply contains the fixed word,
  `output.txt` equals `input.txt`, and `input.txt` is unchanged. The cancelled run is
  `aborted` and no process started by it remains. The fourth fails as "bridge
  unavailable", names Node, and the host keeps running. The session log holds one
  delegation start and one terminal record per run. If the `claude` instance cannot be
  restricted with the installed OAR release, that run must fail as "start failed", not
  complete: either result is recorded with the OAR version.
- **Cleanup.** Remove the workspace and the bridge script directory. If a process
  survived, record its id and command line before ending it, and keep the host log.

`DH-LIB-OAR-R1` to `-R4` are also proved for the product providers by the tests named in
[DH-ADR-OAR-1](#dh-adr-oar-1) and exercised against real programs by
`DH-E-CLAUDEAGENTSDK-1` and `DH-E-CODEX-1`. A skipped or blocked `DH-E-OAR-1` leaves the
bridge unproven against a real OAR; local tests do not stand in for it.

## 9. Units dispositioned by this spec

`UP-DEP-@botiverse/oar` (replaced), `UP-DEBT-patch.@botiverse__oar@0.67.0` (resolved),
`UP-DEBT-pin.minimumReleaseAgeExclude.@botiverse/oar@0.67.0` (resolved).

Go items declared here: `DH-LIB-OAR`, `DH-ADR-OAR-1`, `DH-T-OAR-1` to `-4`,
`DH-E-OAR-1`.
