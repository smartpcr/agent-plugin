# harness:DSH-M2 -- Architecture: first prompt from the command line and the SDK

> **Story.** "clone deepseek harness, given context in docs\harness of this repo, and
> design and impl doc, i want you to follow impl. This story is milestone M2 of dsh
> (impl.md stages DH-S-12 to DH-S-18), following harness:DSH-M1. Its tech spec is
> docs/stories/harness-DSH-M2/tech-spec.md."
>
> **Authority.** [`docs/harness/impl.md`](../../harness/impl.md) sec. 3.3 (milestone M2),
> [DH-S-12](../../harness/impl.md#dh-s-12) to [DH-S-18](../../harness/impl.md#dh-s-18),
> sec. 7 (real-environment acceptance), sec. 10.2 (cycle C-3) and sec. 10.3 (H-1 to H-4,
> H-10, H-21); [`docs/harness/design.md`](../../harness/design.md) sec. 4.2 (package layout)
> and sec. 4.4 (process model); the area specifications under
> [`docs/harness/areas/`](../../harness/areas/). Where this document and those disagree,
> impl.md and the area specification win and this document is corrected.
>
> **Sibling plan documents** (written in parallel; this document does not repeat them):
> [tech-spec.md](tech-spec.md) (requirements, decisions, environments),
> [implementation-plan.md](implementation-plan.md) (step order and commits),
> [e2e-scenarios.md](e2e-scenarios.md) (scenario texts and run schedule).
> The milestone before this one is described in
> [harness-DSH-M1/architecture.md](../harness-DSH-M1/architecture.md); component ids here
> continue its numbering (C01 to C30 are M1's; M2 adds C31 to C57).

---

## 0. Situation report

| Item | State at the start of M2 |
|---|---|
| What M1 left | A host process that boots a profile, runs one agent loop against a model route, records a session in SQLite, runs tools through one pipeline and asks a human through `approval` and `question` (DH-S-1 to DH-S-11). It has no filesystem tools, no shell, no sandbox, no listener, no SDK server, and the `headless` and `sdk` profile bundles are empty placeholders |
| What M2 adds | The first prompt a user can send without a browser: `dsh headless "<task>"` from a terminal and a Go program through `src/sdk/sdk` against `dsh --profile sdk`, with the agent reading, writing and running commands inside a sandbox it can report on |
| What M2 proves | Every M2 build stage (S12 to S16) closes on local tests; S17 and S18 close on real-environment scenarios (14 and 19 scenarios); the four guarded scenarios of step 16.8 pass on scratch homes before S17 and S18 start |
| What M2 enables | Nothing for users. "Team pre-release only, on scratch homes, on Linux" (impl.md sec. 3.3) |
| Entry prerequisite | M1 closed: every DH-S-1 to DH-S-11 evidence record says `closed`. Today `test/harness/acceptance-index.yaml` says `built-through: DH-S-8` and the eleven M1 records are `open` (see sec. 8, R-01) |
| Owner decisions in force | `platforms-windows-wsl` (gate lanes: Windows native and Linux under WSL; macOS out of scope), `models-signed-in-claude-copilot` (real-model checks on the signed-in Claude Code and Copilot CLI routes, no DeepSeek key), `go-floor-1-27-4`, M1-D-PIPE-3/-4/-5 ([M1 decisions](../harness-DSH-M1/decisions.md)) |

---

## 1. Scope

### 1.1 Stages and deliverables

| Stage | Theme | Deliverables built (P0 unless marked) | Closes on |
|---|---|---|---|
| [DH-S-12](../../harness/impl.md#dh-s-12) | Working directory, filesystem seam, policy, file tools | VIEW-10, FS-4, SANDBOX-1 (cycle C-3, one commit, contract first); HUMAN-4 (P1, pulled forward); FS-6 | Local tests DH-T-VIEW-23/-24, DH-T-FS-8..-13/-16..-20/-26/-27, DH-T-SANDBOX-1/-2/-5/-15, DH-T-HUMAN-7 |
| [DH-S-13](../../harness/impl.md#dh-s-13) | Sandbox launch, confinement, shell | SANDBOX-2, SANDBOX-3, SANDBOX-4, EXEC-3, EXEC-4, EXEC-8, EXEC-9, CONTEXT-2 | DH-T-SANDBOX-3/-4/-6/-7/-13/-14/-17, DH-T-EXEC-3/-7/-8/-13/-14/-15, DH-T-NODEPROC-9, DH-T-CONTEXT-4..-6 |
| [DH-S-14](../../harness/impl.md#dh-s-14) | Profiles, presets (parallel with S12 and S13: needs only S6 and S11) | BUNDLE-1, BUNDLE-2, BUNDLE-4, BUNDLE-5, BUNDLE-6 | DH-T-BUNDLE-1..-16 (less -17) |
| [DH-S-15](../../harness/impl.md#dh-s-15) | Listener, admission, control, routes, stdio, SDK server | HOST-1, HOST-2, HOST-3, API-1, SDK-3, SDK-4 (H-1, H-2, H-3 held open), SDK-5 (H-4 held open), SDK-6 | DH-T-HOST-1..-16/-31, DH-T-API-1..-3, DH-T-SDK-8..-15/-21/-22/-25 |
| [DH-S-16](../../harness/impl.md#dh-s-16) | Launchers, admin tree, stop, client, replay, scenario helper | BUNDLE-3, CLI-9, CLI-2, CLI-4, CLI-5, SDK-8 (P1), UTIL-8, UTIL-7 (P1, H-10 held open), VIEW-4 (P1); step 16.8 guarded batch; step 16.10 closes H-21 (BOOT-6 `dsh config show`/`validate`) | DH-T-BUNDLE-17, DH-T-CLI-4/-7..-9/-12..-14/-16, DH-T-SDK-16/-17, DH-T-UTIL-16..-20, DH-T-TOOLCHAIN-3/-5/-6, DH-T-VIEW-10..-12, DH-T-BOOT-14; guarded DH-E-FS-3, DH-E-STORE-1, -2, -6 |
| [DH-S-17](../../harness/impl.md#dh-s-17) | Acceptance on a real model (Linux) | none built | 14 scenarios: TOOLCHAIN-1 (first), SESSION-1/-2, AGENT-1..-4, LLM-1/-3, TOOLS-1, VIEW-1, LOADER-1, SDK-1, SANDBOX-7 (closes debt `UP-DEBT-found.Q3.2`) |
| [DH-S-18](../../harness/impl.md#dh-s-18) | Acceptance on real operating systems | none built | 19 scenarios: TOOLCHAIN-2 (first), HOST-4, EXEC-1/-10, NATIVESYSTEM-1..-4, SANDBOX-1, FS-1/-2, VIEW-5, RIPGREP-1/-2, CHOKIDAR-1/-2, CONTEXT-2, STORAGE-6, NODEFS-1 (last) |

M2 is reached when S17 and S18 are closed and the step 16.8 guarded scenarios have
passed. Held open at M2: H-1 to H-4 (SDK-4, SDK-5) and H-10 (UTIL-7); H-21 is closed by
step 16.10.

### 1.2 Out of scope

- The web UI, browsers and the `web` launcher's pages (S19 onward). HOST-1/-2 build the
  listener and admission; nothing in M2 serves a page a scenario must load.
- macOS backends (Seatbelt) and the Windows sandbox backend: the policy resolver renders
  the macOS and Windows policy *text* (SANDBOX-7 reads it) but SANDBOX-4 is Linux only.
  On Windows no backend passes the M2 start-up probe: `dsh sandbox status` reports
  `Unconfined` and names every missing effect, a confined policy is refused with
  `sandbox/unavailable`, and only a `danger-full-access` start runs, reported
  `Unconfined` (the `unconfined` plan kind, sec. 4.2).
- SSH execution (`ssh-helper` remains a reserved hidden mode), jobs, schedules, goals,
  subagent replay (the targets of H-1, H-4, H-10).
- The `acp` launcher (BUNDLE-8, DH-S-37) and the `sdk-minimal` profile.
- Scenarios authored in M2 whose run belongs to a later stage (SANDBOX-2, EXEC-7, EXEC-8,
  BUNDLE-1..-4, HOST-1/-2/-7/-8/-9, SDK-2, API-5, CLI-1/-2, UTIL-2): written and indexed,
  not executed (impl.md sec. 10.4, S-1).

---

## 2. Starting point in the repository

The plan builds on packages that exist today. Names in this table are taken from the
tree at the start of the story, not invented.

| Existing package or file | What M2 does with it |
|---|---|
| `src/harness/cmd/dsh/main.go` | Adds dispatch of the hidden `sandbox-launch` mode to `world/sandbox/launch` before boot (SANDBOX-2) |
| `src/harness/internal/cli` (`cli.go`, `tree.go`, `reference.go`, `cmdline`, `output`; golden `testdata/reference.golden`) | Gains the launcher commands `headless`, `sdk`, the admin tree (`status`, `stop`, `crash`, `secret set`, `config show`/`validate`, `sandbox status`) and `--safe`. The hidden modes `sandbox-launch` and `ssh-helper` are already reserved in `tree.go` |
| `src/harness/internal/boot/{launch,profile,config,reload,catalog}`; bundles `profile/bundles/{acp,core,headless,minimal,sdk,web}.yml`; golden `profile/testdata/composed.golden` | BUNDLE-1/-2/-3/-6 fill the profiles: `admin.yml`, `base.yml` and `optional.yml` are added; `headless.yml` and `sdk.yml` (today `[]`, "content is DH-D-BUNDLE-1's") get their layers |
| `src/harness/internal/world/subprocess` (the one spawn place: `Spec`, `Start`, `Process.StopTree`, tree owners for linux, bsd, windows) | EXEC-4 makes `Spec.Launch` required; EXEC-3 adds the bounded collector. No second spawn path is created |
| `src/harness/internal/util/{fsx,search,watch,glob,textdiff,herr,ident}` | `fsx` (`ReplaceIn`, `CreateIfAbsentIn` on `os.Root`) backs `world/fs/local`; `search` backs `grep`/`glob`; `watch` drives instruction reconciliation; `herr` carries `<domain>/<reason>` codes |
| `src/harness/internal/storage/{workspace,spill,settings,home}` | `workspace.Registry` gives the working directory its project; `spill` takes overflow from EXEC-3 and FS-6 |
| `src/harness/internal/session/{event,views,surface,store/sqlite}` | New folds (`workingDirectory`, `permissions`, `title`, `instructions.visible`, preset binding) are registered as views |
| `src/harness/internal/human/{approval,question,secret}` | `approval.SetPolicy` and `PolicySet` are driven by HUMAN-4; SDK-6 and the headless `cli` answerer answer `approval` and `question` |
| `src/harness/internal/agent` (`Registry.Create`/`Resume`, `Agent`, `Handle`, `Stop`) | Used unchanged by `sdkserver` and `cli/headless` |
| `src/harness/internal/tools` (`Declaration`, `Overlap`, `Service`), `tools/prompt` | File and shell tools register through it; the policy section registers a prompt section |
| `src/harness/internal/llm/provider/clientroute` (shared client-route plumbing) and its siblings `src/harness/internal/llm/provider/claudecode` and `src/harness/internal/llm/provider/copilot` (also sibling: `src/harness/internal/llm/provider/deepseek`, keyless and fake-tested only per M1-D-PIPE-5) | The model route of the real-model runs (S17, and DH-E-STORE-2 at step 16.8) under decision `models-signed-in-claude-copilot` |
| `src/sdk/jsonrpc`, `src/sdk/protocol` (`methods.go`, `schema.json`, `Version` 1) | The wire is fixed: `sdkserver` implements it, `src/sdk/sdk` consumes it. No method is added in M2 |
| `src/harness/internal/testkit/{launcher,temphome,model,answerer,procwait,redact,golden}` | Unit and binary-level tests; the scenario helper of UTIL-8 wraps `launcher` |
| `src/harness/internal/testkit/scenarios/*_scenario_test.go` (`//go:build scenario`), helpers `sdkhost_test.go`, `realenv_test.go`, `binary_test.go`, `signedin_routes_test.go` | Home of the S17/S18 scenario code; many S17/S18 scenarios already exist and are retargeted, the missing ones are authored (sec. 6.7) |
| `test/harness/acceptance-index.yaml`, `tools/harness-gate` (`acceptance/`, `lanes/win.ps1`, `lanes/wsl.sh`), `.github/workflows/harness-scenarios.yml` | The index gains the S12 to S18 entries; the workflow derives `DSH_REQUIRE_SCENARIOS` per stage |

---

## 3. Components

### 3.1 Module and package map

New packages are marked `+`; extended existing packages `~`. All under
`src/harness/internal/` unless the path says otherwise.

```
src/harness/cmd/dsh/main.go ~ ......... dispatch: sandbox-launch | boot.Process
|
+-- cli ~ (tree.go, cli.go) .............. C50 launchers + admin tree
|   +-- cli/headless + ................... C50 one-shot task runner
|   +-- cli/lifecycle + .................. C51 one bounded stop path
|   +-- cli/hostclient + ................. C52 runtime record, second launch, crash notice
|
+-- boot/profile ~ (+ admin/base/optional.yml) .. C42 five profiles, layers, coverage
+-- boot/agentpreset + (+ persona, presets/standard.yml) .. C43 preset registry
|
+-- host/listen + , host/assets + ........ C44 one listener, TLS, mounts
+-- host/auth + .......................... C45 admission: source, then cookie
+-- host/launch + ........................ C46 runtime record + control endpoint
+-- host/web + ........................... C47 route table: every route a declared effect
|
+-- wire/stdio + ......................... C48 sole owner of stdin/stdout
+-- wire/sdkserver + ..................... C49 SDK methods, forwarder, answerer
|
+-- session/workdir + .................... C31 working-directory fold      -+
+-- world/fs + , fs/local + , fs/ops + ... C32 filesystem seam              +-- cycle C-3
+-- world/sandbox/policy + ............... C33 policy resolver             -+
+-- human/permission + ................... C34 permissions choice
+-- world/fs/tools + ..................... C35 read write edit apply_patch glob grep
|
+-- world/sandbox + , sandbox/local + .... C37 probe, select, prepare, confirm, report
+-- world/sandbox/launch + ............... C36 the launch stage (hidden mode)
+-- world/subprocess ~ (+ proc collector)  C38 launch plan required, bounded capture
+-- world/shellexec{,/bash,/pwsh,/env} +   C39 executors
+-- world/shellexec/tools + .............. C40 bash and pwsh tools
+-- context/instructions + ............... C41 AGENTS.md and kin, through the seam
+-- session/title + ...................... C56 titles
|
+-- llm/replay + , testkit/fixture + , testkit/dshbin + .. C54 keyless replay
+-- testkit/scenario + ................... C55 scenario helper, evidence statement

src/sdk/sdk + ............................ C53 Go client
tools/harness-gate ~, test/harness/acceptance-index.yaml ~ .. C57 acceptance plumbing
```

### 3.2 Process picture of the two launchers

```
 terminal                                   Go program (src/sdk/sdk, C53)
    |                                          |  exec: dsh --home H --profile sdk
    | dsh headless "task"                      |  stdin/stdout = JSON-RPC frames
    v                                          v
+-------------- one dsh process (many may share a home; see "Who may coexist") --------------+
| C50 cli -> boot.Process(profile)   [only `dsh web` / bare `dsh` consult C52 for reuse] |
|                                                                                       |
|  headless profile                           sdk profile                               |
|  C50 cli/headless --+                       C48 wire/stdio --> C49 sdkserver          |
|        | cli answerer|                             | approval/request, question/ask  |
|        v             v                             v                                  |
|  agent.Registry --- Agent loop (M1) --- tools.Service ---+                            |
|                                                          |                             |
|        C35 fs tools --> C32 fs/ops --> C33 policy <-- C31 workdir <-- C34 permission |
|                              |                                                        |
|                              +--> C32 fs/local (os.Root, fsx)                         |
|        C40 shell tools --> C39 shellexec --> C37 sandbox.Prepare --> C38 subprocess  |
|                                                                          |            |
|  C44 listen + C45 auth + C47 routes (bound only by profiles that mount)   |            |
|  C46 launch: runtime record + control endpoint (web-host profiles only)    |            |
|  C51 lifecycle.Watch: Ctrl-C | signal | EOF | dsh stop  --> launch.App.Stop          |
+---------------------------------------------------------------------------|----------+
                                                                            v
             dsh sandbox-launch (C36, same binary) <== status fd pair ==> parent (C37/C38)
                  confine self, canary write -> "ready" -> wait -> "go" -> exec
                              | bwrap or Landlock (C37 local, Linux)
                              v
                         bash / pwsh command (C39), output -> C38 rings -> spill
```

#### Who may coexist on one home

Two different things are "one per" something, and M2 keeps them apart:

| Kind of ownership | Unit | Mechanism | Who participates | Consequence |
|---|---|---|---|---|
| Interactive host discovery (HOST-3, CLI-5) | One *listening web host* per home and address | Runtime record and its lock in `<home>/run/`, control endpoint (`host.status`, `host.stop`, `launch.open`) | Only processes that bind a listener (`dsh web`, and a bare `dsh` that would start one). `dsh headless` and `dsh sdk` bind no listener, write no runtime record and never hand a task to another process | A second `dsh web` on the same home reuses the running host via C52 instead of failing on a busy port |
| Session writer ownership (DH-D-STORE-2/-3, built in M1) | One *writer per session* | Per-process lock file `<home>/run/<pid>-*.lock`, the `hosts` row and the session lease epoch checked by every append (design.md sec. 4.5: "the append transaction checks the lease epoch and is refused") | Every process that opens the store: any number of `headless`, `sdk` and `web` processes may run on one home at once | A second process that resumes a session another live process owns is refused with `session/owned` naming the owner's pid; when the owner dies its kernel lock is released and the next resume takes over with epoch + 1 |

So DH-E-STORE-1 (two headless hosts on one home, refusal then takeover) and DH-E-FS-3
(two `dsh` processes editing one file) are legitimate M2 configurations, proved by the
session lease, not by host discovery. Task hand-off through the control endpoint is
never used to satisfy them.

### 3.3 Component table

| Id | Component (package) | Deliverable | Responsibility | Depends on (components / M1) | Stage |
|---|---|---|---|---|---|
| C31 | Working directory (`session/workdir`) | VIEW-10 | One working directory per session: the `workingDirectory` fold, per-session serialized changes, validation and recovery through the seam, `workdir.Access` for readers, the prompt context line and an inbox notice when it moves; `workdir/unavailable` when it is gone | C32 (contract), views, agent (M1) | S12 |
| C32 | Filesystem seam (`world/fs`, `fs/fstest`, `fs/local`, `fs/ops`) | FS-4 | The only way the product touches project files. `world/fs` defines contract, types, service keys, errors and the conformance suite; `fs/local` implements it on `os.Root` and `fsx`; `fs/ops` is the sole importer of the resolver and emits the file events. Roots and mode come from the policy, never from its own config | C33 (contract), C31 (contract), `util/fsx`, cordis | S12 |
| C33 | Policy resolver (`world/sandbox`, `world/sandbox/policy`) | SANDBOX-1 | Turns mode, working directory, workspace and grants into a `Policy` (unexported fields, no valid zero value). `Resolve`, `Escalate`, `ForHelper`, `GrantRoot`, `SetMode`; writes `sandbox/mode`; dispatches `sandbox.ModeChanging`; renders the model-visible policy section per platform text | C31, approval (M1), tools/prompt | S12 |
| C34 | Permissions (`human/permission`) | HUMAN-4 (P1) | The user's single choice (a named preset of mode plus approval policy). Folds into `permissions`; pins a fresh session at agent creation; switches through `SetMode` and `approval.SetPolicy`; registers `/permission`. The default preset must resolve or config load fails | C33, approval, command | S12 |
| C35 | File tools (`world/fs/tools`) | FS-6 | Six tools from Go argument structs: `read`, `write`, `edit`, `apply_patch`, `glob`, `grep`. Bounded results with spill; `read`, `glob`, `grep` declare overlapping reads, the writers declare writes on the path key | C32, tools (M1), `util/search`, `storage/spill` | S12 |
| C36 | Launch stage (`world/sandbox/launch`; `cmd/dsh` dispatch) | SANDBOX-2 | The hidden `dsh sandbox-launch` mode, run as the child under the selected backend. Two-phase per DH-ADR-SANDBOX-2: over a close-on-exec descriptor pair it (1) confines itself and (2) tries to open the canary for writing -- both only for a `confined` plan; an `unconfined` plan (`danger-full-access`, backend `none`) skips them and reports `canary: not-tried` -- (3) sends `ready{backend, governed effects, canary result}`, (4) waits; it replaces itself with the command only after the host sends `go`. Host rejection, or end of file in either direction (host died, stage died), ends the stage with no command run. The launcher is the dsh binary itself; no helper is installed | C33, cli (M1) | S13 |
| C37 | Sandbox service and Linux backends (`world/sandbox`, `world/sandbox/local`) | SANDBOX-3, SANDBOX-4 | Probe backends with a canary, select, `Prepare(ctx, Policy, argv) (Launch, error)` (fixes the plan `Kind` per sec. 4.2; a confined policy with no passing backend is refused `sandbox/unavailable`), then on the stage's `ready` compute enforcement (DH-ADR-SANDBOX-3: weaker of start-up probe and this start's `ready`; unknown counts as not governed; a custom runner is never `Full`), refuse with `sandbox/enforcement-insufficient` if `require: full` is not met, and only then authorize `go`; report `Confinement{State, Backend, Missing, Exposed, Scratch}`. Linux: `bwrap` found once by absolute path, then Landlock | C33, C36, C38 | S13 |
| C38 | Subprocess plan and capture (`world/subprocess` ~, `proc` collector) | EXEC-4, EXEC-3 | A command starts under its plan or not at all: `Spec.Launch` required; `Wrap`, start the stage, read `ready`, `Confirm` (C37's enforcement decision), send `go` or close, `Release`; the outcome carries the confirmed `Confinement`. One ring per stream keeps the tail; readers do not consume; overflow goes to bounded spill; `Run`'s hard limit stops the tree | subprocess (M1), `storage/spill`, C37 | S13 |
| C39 | Shell executors (`world/shellexec`, `/bash`, `/pwsh`, `/env`) | EXEC-8 | `Resolve` applies defaults and caps; `Start` builds argv, asks C37 for the launch plan and starts through C38; one execution supports foreground and background reads; explicit `Failed` outcome. `/env` builds the scrubbed environment (secret names never pass) | C37, C38, C31, secret (M1) | S13 |
| C40 | Shell tools (`world/shellexec/tools`) | EXEC-9 | One-shot `bash` and `pwsh` tools. The model supplies named arguments only (command, timeout, description); never stdin or environment. Structured outcome with exit code, tails, spill reference and confinement | C39, tools, C33 | S13 |
| C41 | Instruction files (`context/instructions`) | CONTEXT-2 | Discovers `AGENTS.md` and kin from the working directory upward to the project root, reads them bounded through C32 (so the fence applies), digests them, folds `instructions.visible`, renders a framed, budgeted section, and reconciles on watch, tool touches, directory change and resume | C32, C31, `util/watch`, C40 | S13 |
| C42 | Profiles and layers (`boot/profile` ~; `admin.yml`, `core.yml`, `base.yml`, `web.yml`, `headless.yml`, `sdk.yml`, `optional.yml`) | BUNDLE-1, -2, -3, -6 | Five profile definitions (`admin`, `core` kernel, `web`, `headless`, `sdk`), `profile.Spine`, each profile's `Required` list; layer operations `insert`, `merge`, `remove`; coverage check that every catalogue entry is in a profile or in `optional.yml`; defaults send nothing | boot (M1), loader | S14, S16 |
| C43 | Preset registry and persona (`boot/agentpreset`, `agentpreset/persona`, `presets/standard.yml`) | BUNDLE-4, -5 | Declarations, revision list with lease counts, `Extends` validation, `Select`, session binding and its fold. Persona registers a scoped prefix/suffix, `Complete`, `Suppress`; it refuses to load outside a preset | C42, views, agent | S14 |
| C44 | Listener (`host/listen`, `host/assets`) | HOST-1 | One `http.Server` on one parsed address, optional TLS that reloads when files change, mounts dispatched by longest prefix and only after admission; embedded assets | boot (M1) | S15 |
| C45 | Admission (`host/auth`) | HOST-2 | Admit by source first (loopback or allowed peer), then by cookie issued from a one-time launch token; hashed session table, `Issue`, sign-out, revocation, sign-in notice; provides `access.Principal` | C44, storage | S15 |
| C46 | Host launch and control (`host/launch`) | HOST-3 | Prints the address or hands off, writes the runtime record under `<home>/run/` (only in profiles that bind a listener; never in `headless` or `sdk`), serves the control endpoint (same-user local socket; `status`, `stop`, second launch; returns no credential) | C45, subprocess, `storage/home` | S15 |
| C47 | Routes (`host/web`) | API-1 | `Routes.Handle` validates declarations (each route is a declared effect) and swaps the route table atomically; cleanup removes routes and cancels in-flight handlers | C44, cordis | S15 |
| C48 | Stdio owner (`wire/stdio`) | SDK-3 | Claims stdin and stdout once, redirects stray stdout writes to the log, bounded writer; calls `launch.App.Stop` once per ending: `InputClosed` status 0, `ClientStalled` status 125; boot supplies forced status 124 | `src/sdk/jsonrpc`, boot | S15 |
| C49 | SDK server (`wire/sdkserver`: `server.go`, `forward.go`, `runs.go`, `answer.go`) | SDK-4, -5, -6 | Implements `src/sdk/protocol` methods: create, resume, prompt, cancel, close, history, list, wait, query, model and working-directory set. Connection table of owned sessions and children; `initialize` waits for readiness. Forwarder emits scoped, ordered `session.event` and `session.run.completed`. One answerer per connection sends `approval/request` and `question/ask`, validates answers, cancels on withdrawal | C48, agent, C31, approval, question, C43 | S15 |
| C50 | CLI launchers and admin tree (`cli` ~, `cli/headless`) | CLI-2, CLI-9, BOOT-6 (H-21) | `dsh headless` creates or resumes, sends a task or command, waits idle, flushes, prints its interval's last assistant text, stops; `--json` emits export lines. `dsh sdk` boots the `sdk` profile. Admin tree runs no carrier, agent, model adapter or plugin process; read-only commands get a non-writing view; `dsh secret set` reads stdin only; `dsh config show`/`validate` | C42, C43, C51, C52, agent | S16 |
| C51 | Lifecycle (`cli/lifecycle`) | CLI-4 | `lifecycle.Watch` is a state machine: Ctrl-C, SIGTERM/SIGHUP (console close on Windows), stdin end, `dsh stop` and stdio end share one path to `launch.App.Stop`; second Ctrl-C forces; bounded by `ShutdownTimeout` | boot, agent | S16 |
| C52 | Host client (`cli/hostclient`) | CLI-5 | Reads the runtime record and lock; `launch.open` returns `opened`, `printed` or `launch/no-channel`; a second `dsh web` reuses the running web host (headless and sdk processes coexist instead, governed by the session lease); unclean exit is explained at the next start with the crash report path and `--safe`; statuses 70 and 78; `dsh status`, `dsh crash` | C46, `host/diag` (M1) | S16 |
| C53 | Go client (`src/sdk/sdk`) | SDK-8 (P1) | `Start` takes `Options.Bin` or `DSH_BIN`, requires a home, runs `dsh --home <dir> --profile sdk`; shutdown ladder `shutdown`, wait, terminate, kill; `Run` returns on the matching `session.run.completed`; handler contexts cancel on withdrawal | `src/sdk/jsonrpc`, `src/sdk/protocol` | S16 |
| C54 | Keyless replay (`llm/replay`, `testkit/fixture`, `testkit/dshbin`, `test/replay`) | UTIL-7 (P1; H-10) | Replays a committed recording without a key and compares log, workspace and model requests after normalization with fixed typed tokens | llm (M1), store | S16 |
| C55 | Scenario helper (`testkit/scenario`; analyzers in `tools`) | UTIL-8 | Starts the built binary by `DSH_BIN`, scratch home per scenario, deadline, cleanup, one evidence record per scenario to `DSH_SCENARIO_REPORT`; analyzer rules forbid scenarios importing product internals | `testkit/launcher` | S16 |
| C56 | Titles (`session/title`) | VIEW-4 (P1) | Fallback title, normalization, `Rename`, `Unpin`, `Refresh`, revision, generator over the configured route; folds `title` and `titleInput`; events `session/title-request`, `session/title-attempt`, `session/title` | views, llm, C54 | S16 |
| C57 | Acceptance plumbing (`test/harness/acceptance-index.yaml`, `tools/harness-gate`, `.github/workflows/harness-scenarios.yml`) | step closure of S12 to S18 | Indexes each scenario under its stage, advances `built-through`, derives `DSH_REQUIRE_SCENARIOS`, and refuses a stage close while a listed scenario is missing, skipped or failed | M1 gate | S12 to S18 |

### 3.4 Boundaries that are rules, not habits

1. **One spawn place.** Every child process (shell command, `sandbox-launch`, signed-in
   client route) is started by `world/subprocess`. C39 never calls `os/exec`.
2. **One file place.** Every project file access by a tool, by instructions (C41) or by
   the working directory (C31) goes through `world/fs`. Only `fs/ops` imports
   `sandbox/policy`. The harness home is not a project file and keeps using `storage`.
3. **One owner of stdio.** In the `sdk` profile only C48 reads stdin and writes stdout;
   anything else that writes stdout is redirected to the log.
4. **One stop path.** C51 is the only caller of `launch.App.Stop` from the CLI side; C48
   is the only caller from the stdio side; both converge on the same `Stop`.
5. **Clients see only `src/sdk`.** C53 and every scenario import `src/sdk/...` and the
   test kit's launcher, never `src/harness/internal/...` (enforced by C55's analyzer and
   the existing scenario-index check).

---

## 4. Data model

Entities are grouped by where they live. "Fold" means a session view registered with
`session/views`, rebuilt from the event log on resume (M1 rule: the log is the truth,
views are derived). Field names are the contract names of the area specifications;
where an area does not fix a name, the field is described and the implementation plan
picks the Go identifier.

### 4.1 Session folds (in the event log, rebuilt on resume)

#### WorkingDirectory (fold `workingDirectory`, C31)

| Field | Meaning |
|---|---|
| `path` | Absolute, cleaned path of the session's working directory |
| `workspace` | Id of the `storage/workspace` entry the path belongs to |
| `root` | The project root used by the fence (repository root, or the linked worktree's own root) |
| `revision` | Increments on every accepted change; changes are serialized per session |
| `state` | `available` or `unavailable`; a missing directory yields `workdir/unavailable` and a recovery attempt to the nearest existing root |

#### Permissions (fold `permissions`, C34)

| Field | Meaning |
|---|---|
| `preset` | Name of the permission preset in force (for example the configured default) |
| `mode` | Sandbox mode it implies: `read-only`, `workspace-write` or `danger-full-access` |
| `approval` | Approval policy it implies: `ask` or `never` (`approval.Policy`, M1) |
| `pinnedAt` | Event seq at which a fresh session was pinned to the preset at agent creation |

#### SandboxMode (event `sandbox/mode`, C33)

Written only by `Resolver.SetMode`; carries the old and new mode and the actor
(`approval.By`). `sandbox.ModeChanging` is dispatched before the write so a running
command can be refused under the old policy rather than mid-change.

#### PresetBinding (fold of C43)

| Field | Meaning |
|---|---|
| `preset` | Agent preset name (from `presets/standard.yml` or user declarations) |
| `revision` | Revision id the session is leased to; the registry keeps the revision while any lease exists |
| `persona` | Optional persona prefix/suffix scope bound with the preset |

#### InstructionsVisible (fold `instructions.visible`, C41)

| Field | Meaning |
|---|---|
| `files[]` | `{path, digest, bytes, truncated}` for each instruction file read through C32 |
| `refused[]` | `{path, reason}` for files the fence refused (for example a link to an outside file); diagnosed, never rendered |
| `budget` | Byte budget applied to the rendered section |

#### Title (folds `title`, `titleInput`, C56)

`text`, `source` (`fallback`, `generated`, `user`), `pinned`, `revision`; events
`session/title-request`, `session/title-attempt`, `session/title`.

### 4.2 Runtime values (in memory; never persisted as such)

#### Policy (C33)

An opaque value with unexported fields and no valid zero value; obtained only from the
resolver. Readable through accessors:

| Accessor | Meaning |
|---|---|
| `Mode()` | `read-only`, `workspace-write`, `danger-full-access` |
| `WriteRoots()` | `[]{Path, Source}`; `Source` says why the root is writable (working directory, workspace, grant, scratch) |
| `Network()` | Whether outbound network is an allowed effect |
| `Require()` | `full` or `any`: whether a command may run when the backend cannot confine fully |
| `TextVersion()` | Version of the model-visible policy text, recorded by SANDBOX-7 |

#### Launch plan and confinement (C37, C38)

| Entity | Fields |
|---|---|
| `sandbox.Launch` | Wrapped argv (`dsh sandbox-launch ...` or `bwrap ... dsh sandbox-launch ...`), extra files (the close-on-exec status descriptor pair), environment additions, `Kind`, canary path (confined kind only), the `Policy` the start must meet |
| Launch plan `Kind` | Fixed by C37 at `Prepare`, before any process exists, and passed to the stage: **`confined`** -- policy `read-only` or `workspace-write` and a backend (`bwrap`, `landlock`) passed the start-up probe; the stage confines itself and must try the canary, whose expected result is `refused`. **`unconfined`** -- policy `danger-full-access` on any machine (the sandbox area: "nothing in this table applies and the handle says `unconfined`"); backend `none`, no self-confinement, no canary (the stage reports `canary: not-tried`). A confined policy on a machine where no backend passed the probe (Windows in M2, `service:linux-unconfined`) gets **no plan**: `Prepare` refuses with `sandbox/unavailable` and nothing starts |
| Stage `ready` message | Sent by C36 after the step its `Kind` requires, before any command exists: `backend`, `governed []Effect`, `canary` (`refused`, `written` or `not-tried`). The stage then blocks |
| Host decision | C37 `Confirm` computes `Confinement` from the start-up probe and this `ready` (weaker wins; unknown = not governed), then decides in this order: (1) `confined` kind and canary `written` or `not-tried` -> close the pair, `sandbox/launch-failed`, re-probe once (a failed confinement); (2) `unconfined` kind and canary anything but `not-tried`, or backend not `none` -> close, `sandbox/launch-failed` (plan and stage disagree); (3) `Require()==full` and `State!=Full` -> close, `sandbox/enforcement-insufficient` (applies to both kinds, so a `danger-full-access` start under `require: full` is refused); (4) otherwise send `go` -- `confined` yields `Full` or `Partial{Missing}`, `unconfined` yields `Unconfined` with every effect in `Missing`. Only after `go` does the stage `exec` the command. End of file in either direction before `go` ends the stage with no command run and the outcome `Failed` |
| `Confinement` | `State` (`Full`, `Partial`, `Unconfined`), `Backend` (`bwrap`, `landlock`, `none`), `Missing []Effect`, `Exposed []Effect` (for example `delegated-execution`), `Scratch` (scratch directory, if any); computed by the host at the decision above, never asserted by the stage |
| `sandbox.Status` | Per backend from `Describe`/`Reprobe`: name, available, reason, canary result |

#### Execution (C38, C39, C40)

| Entity | Fields |
|---|---|
| `shellexec.Request` | Shell (`bash` or `pwsh`), command text, working directory (from C31), timeout (capped), description |
| Stream capture | Per stream: ring of the last N bytes, total bytes seen, spill reference when overflowed |
| `shellexec.Outcome` | Kind (`Exited`, `TimedOut`, `Cancelled`, `Failed`), exit code, stdout and stderr tails, spill references, `Confinement`, duration |

#### Errors (all `<domain>/<reason>`, carried by `util/herr`)

| Code | Raised by | When |
|---|---|---|
| `fs/sandbox-denied` | C32 `fs/ops` | Path outside the policy's roots for the requested effect |
| `fs/stale-version` | C32 | The file changed since the version the caller read (edit, apply_patch) |
| `workdir/unavailable` | C31 | The working directory no longer exists and no recovery root is valid |
| `sandbox/enforcement-insufficient` | C37 | `Require()` is `full` and the selected backend cannot give `Full` |
| `launch/no-channel` | C52 | Neither a browser opener nor a terminal can show the address |
| `sdk/*` (existing) | C49 | `sdk/not-initialized`, `sdk/session-not-owned`, `sdk/session-live`, `sdk/invalid-content`, `sdk/shutting-down`, ... (already in `src/sdk/protocol`) |

### 4.3 Harness-home records (files under the home)

| Record | Owner | Fields and rules |
|---|---|---|
| Runtime record | C46 writes, C52 reads | Written only by a process that binds a listener (web-host profiles): pid, start time, build version, listen address, control endpoint path, profile; in `<home>/run/`, written atomically with `fsx`, guarded by its own lock; removed on clean stop. A record whose lock is free means an unclean exit. `headless` and `sdk` processes write none |
| Session-writer lock | Session store (M1, DH-D-STORE-3) | `<home>/run/<pid>-*.lock` plus a `hosts` row per process that opens the store; the session lease epoch is checked on every append. Independent of the runtime record |
| Crash report | `host/diag` (M1, HOST-7) writes, C52 reads | Owner-only file with cause and stacks; the ten newest are kept; its path is printed on stderr and at the next start with the `--safe` option |
| Auth session table | C45 | Hashed cookie values, principal, issued and expiry times; launch token single-use |
| Preset declarations and revisions | C43 | Declaration map, revision list, lease counts (leases are recorded against sessions, released on dispose) |
| Secrets | `human/secret` (M1) via `dsh secret set` | Value read from stdin only; name checked by the secret-name predicate; never echoed |

### 4.4 Wire entities (`src/sdk/protocol`, unchanged in M2)

`InitializeParams` (protocol version, capabilities `approvals`, `questions`, `stream`,
client tools, `timeZone`), `session/create` and `session/resume` results (session id,
`closedInterruptedTurn` on resume), `session/prompt` params (content parts; inline
images refused with `sdk/invalid-content` until H-3), `RunCompletedNotification`
(session id, turn id, last assistant text, stop reason; the goal stop signal waits for
H-4), `ApprovalRequest`/`ApprovalResult` (outcome `allowed-once`, `rejected` or
`cancelled`; never a standing grant), `question/ask`, `$/cancel`. `schema.json` is the
golden of this model.

### 4.5 Acceptance entities (C55, C57)

| Entity | Fields |
|---|---|
| Acceptance index entry | Scenario id, stage, `proves` deliverables, environment id (E1, E2), go test name, status |
| Evidence record (impl.md sec. 7.3) | Source and build identity (commit, Go version, binary checksum and version); environment and fixture (E-id, OS, sandbox backend and reported enforcement, route); exact commands (`go test` line, `DSH_REQUIRE_SCENARIOS`); run identifiers (CI run/job or local run-record id and SHA-256, session ids, provider request ids); results; manual checks; cleanup evidence; open items (H-n, built-not-done) |
| Scenario report line | One per scenario written by C55 to `DSH_SCENARIO_REPORT`: id, pass/fail/skip, duration, diagnostic |

---

## 5. Interfaces between components

### 5.1 Go contracts (in-process)

```go
// C33 world/sandbox/policy
type Resolver interface {
    Resolve(ctx context.Context, req Request) (Policy, error)          // per tool call / command
    Escalate(ctx context.Context, req EscalationRequest) (Policy, error) // asks approval; one-shot widening
    ForHelper(ctx context.Context, req HelperRequest) (Policy, error)
    GrantRoot(ctx context.Context, s session.ID, dir string) (release func(), error)
    SetMode(ctx context.Context, s session.ID, m Mode) error            // writes sandbox/mode
}

// C37 world/sandbox
type Service interface {
    Describe(ctx context.Context) []Status
    Reprobe(ctx context.Context) []Status
    Prepare(ctx context.Context, p policy.Policy, argv []string) (Launch, error)
}

// C38 world/subprocess (extended)
type Spec struct {
    // ... existing fields
    Launch Launch // required: a Spec without a plan is refused, never started bare
}
// Start -> Wrap(plan) -> start stage -> read "ready" -> Confirm (enforcement decision)
//       -> send "go" (stage execs the command) | close pair (rejected: no command ran) -> Release
// Outcome.Confinement = the host-computed value from Confirm

// C39 world/shellexec
func (e *Executor) Resolve(req Request) (Request, error) // defaults and caps
func (e *Executor) Start(ctx context.Context, req Request) (*Execution, error)

// C47 host/web
func (r *Routes) Handle(owner *cordis.Context, decls ...Route) (*cordis.EffectHandle, error)

// C48 wire/stdio -> boot
// Claim() once; on end calls launch.App.Stop(cause) with InputClosed(0) | ClientStalled(125)

// C51 cli/lifecycle
func Watch(ctx context.Context, app *launch.App, in Inputs) (status int)

// C53 src/sdk/sdk
func Start(ctx context.Context, o Options) (*Client, error) // o.Bin or DSH_BIN; o.Home required
func (c *Client) Run(ctx context.Context, s SessionID, content ...Part) (RunResult, error)
```

The exact Go identifiers not fixed by an area specification (`Executor`, `Inputs`,
`RunResult`) are the implementation plan's to choose; the method sets and their
semantics above are the contract.

### 5.2 Interface table

| From | To | Mechanism | Contract |
|---|---|---|---|
| C35 file tools | C32 `fs/ops` | Go call | Every path argument is resolved against C31's directory; result or `fs/sandbox-denied` / `fs/stale-version`; never a raw `os` call |
| C32 `fs/ops` | C33 | Go call `Resolve` | Policy per call; roots are the policy's, not the seam's config |
| C33 | C31 | `workdir.Access` read | Current directory and root; contract-only edge of cycle C-3 |
| C31 | C32 | Seam `Stat` | Validates and recovers the directory through the seam (fence applies) |
| C34 | C33, approval | `SetMode`, `approval.SetPolicy` | One user choice moves both knobs; folded in `permissions` |
| C40 | C39 -> C37 -> C38 | Go calls | Tool args -> `Request` -> `Prepare` -> `Spec.Launch` -> start; outcome carries confinement |
| C38 | C36 (child) | Re-exec of the dsh binary with hidden mode; close-on-exec descriptor pair | Two-phase (DH-ADR-SANDBOX-2): child sends `ready{backend, governed, canary}` and waits; parent `Confirm`s via C37 (the ordered decision of sec. 4.2) then sends `go`, or closes the pair; EOF either way means no command runs and the outcome is `Failed` |
| C41 | C32, `util/watch` | Seam reads, watch events | Bounded reads; refused links diagnosed in `instructions.visible.refused` |
| C49 | agent, C31, approval, question | Go calls | `Registry.Create`/`Resume`, `Agent.Send`/`Cancel`, `Handle.Dispose`, `workdir.Service` |
| C48 | C49 | `src/sdk/jsonrpc` conn over stdio | Line framing; bounded writer; stalled client -> status 125 |
| C53 / scenario | dsh (C48) | Child process, JSON-RPC over stdin/stdout | `src/sdk/protocol` Version 1; stdout carries frames only |
| C50 `headless` | agent, cli answerer | Go calls | Create or resume, send, wait idle, print last assistant text of the interval |
| C52 | C46 | Same-user local socket (control endpoint) | `status`, `stop`, second launch; no credential returned |
| C44 | C45, C47 | In-process | Mount served only after admission; routes declared with their effect |
| C42 | boot (M1) | YAML bundles via loader | Composed result is golden-checked; coverage check over the catalogue |
| C43 | agent | Session binding at create | Lease on revision; released at dispose |
| C55 | built dsh | `DSH_BIN`, `DSH_REQUIRE_SCENARIOS`, `DSH_SCENARIO_REPORT` | One record per scenario; a listed scenario that skips fails the job |
| C57 | workflow / lanes | `harness-gate require --stage`, `acceptance-index.yaml` | Stage close blocked while any indexed scenario is open |

### 5.3 Command-line surface added in M2

| Command | Owner | Exit statuses |
|---|---|---|
| `dsh headless [--json] [--session <id>] [--agent-preset <id>] [--permission-mode <preset>] "<task>"` (flags per app-cli area) | C50 | 0 done; non-zero on refused start, failed turn, forced stop |
| `dsh sdk` (alias of `--profile sdk`) | C50, C48 | 0 input closed; 124 forced; 125 client stalled |
| `dsh status`, `dsh stop` | C52 via C46 | 0 / non-zero when no host |
| `dsh crash` | C52 | Shows the newest crash report path |
| `dsh --safe <profile>` | C52 | Starts with optional bundles off after an unclean exit |
| `dsh secret set <name>` | C50 | Value from stdin only |
| `dsh config show`, `dsh config validate` | C50 (BOOT-6, H-21) | `validate` non-zero on an invalid layer; neither constructs a component |
| `dsh sandbox status` | C50 -> C37 `Describe` | Prints backends and the enforcement a command would get |
| `dsh sandbox-launch` (hidden) | C36 | Internal only |
| Exit 70 / 78 | C52 | Host-side internal error / configuration error (CLI-5) |

---

## 6. End-to-end sequence flows

### 6.1 F1 -- `dsh headless` runs a task that writes a file (S12, S16; proved by DH-E-TOOLS-1, DH-E-AGENT-1 in S17)

```
user        cli(C50)   store(M1)        boot+C42/C43   agent(M1)   fs tools(C35)  fs/ops(C32)  policy(C33)  approval+cli answerer
 | dsh headless "fix x"  |                  |              |            |              |            |               |
 |--------->|            |                  |              |            |              |            |               |
 |          |--open----->| hosts row + <pid> lock (no runtime record, no reuse probe)  |            |               |
 |          |<-ok--------|                  |              |            |              |            |               |
 |          |--boot headless profile------->| select preset, lease rev  |              |            |               |
 |          |                               |--Create(session, preset)->| pin permissions (C34)    |               |
 |          |--Send(task)---------------------------------------------->| model turn   |            |               |
 |          |                               |              |--call write(path,text)-->|            |               |
 |          |                               |              |            |--Write------>|--Resolve-->| (mode, roots)  |
 |          |                               |              |            |              |<--Policy---|               |
 |          |                               |              |            |              |  path in root, policy ask   |
 |          |                               |              |            |              |--------------request------->|
 |          |                               |              |            |              |<-------allowed-once---------|
 |          |                               |              |            |              | fs/local ReplaceIn (os.Root)|
 |          |                               |              |<--result---|<--version----|            |               |
 |          |<--idle--------------------------------------------------- | turn/end     |            |               |
 |<-last assistant text of this interval---|              |            |              |            |               |
 |          |--lifecycle.Stop(done)-------->| dispose, release lease, exit 0           |            |               |
```

Rules shown: the write is resolved against the working directory (C31) and the policy
(C33) on every call; the approval outcome is one-shot; headless prints only the last
assistant text of its own interval (`--json` prints export lines instead).

### 6.2 F2 -- A Go program drives `dsh --profile sdk` (S15, S16; proved by DH-E-SDK-1 in S17)

```
Go program (C53)            dsh: stdio(C48)   sdkserver(C49)      agent   shell tool(C40) -> C39 -> C37 -> C38
 | sdk.Start(Home=H)            |                 |                 |          |
 |== exec dsh --home H --profile sdk ==>| Claim stdin/stdout        |          |
 |--initialize{caps: approvals, stream, timeZone}-->| wait readiness |          |
 |<--result{version 1}--------------------------------|              |          |
 |--session/create--------------------------------->| Registry.Create          |
 |<--{sessionId}------------------------------------|                |          |
 |--session/prompt{"run tests"}-------------------->| Agent.Send ---->|          |
 |<==session.event (ordered, scoped to the session)=|<--- forwarder --|          |
 |                                                   |                |--bash--->| Resolve, Prepare(policy, argv)
 |<--approval/request{tool bash, reason}------------| answerer (one per connection)
 |<--result{allowed-once}--------------------------->|                |          | start sandbox-launch, read ready,
 |                                                   |                |          | Confirm, send go, capture rings
 |<==session.event (tool result, confinement)=======|                |<-outcome-|
 |<--session.run.completed{turn, lastAssistant}-----| runs.go matches turn/end
 | Run() returns RunResult                           |                |
 |--session/close, shutdown------------------------>| Dispose; stdio end -> launch.App.Stop(InputClosed) -> exit 0
 | ladder: wait -> terminate -> kill (only if exit is late)
```

Properties: stdout carries frames only (C48 redirects stray writes); the run result
equals the last assistant message of that turn in `session/history`; a withdrawn
approval (`$/cancel`) cancels the handler's context in C53.

### 6.3 F3 -- A command meets the fence, then an escalation (S12, S13; proved by DH-E-SANDBOX-1, -7, DH-E-FS-1)

```
model -> bash "echo x > /outside/f"
  C40 -> C39.Resolve -> C33.Resolve(session) = Policy{workspace-write, roots=[W]}
  C37.Prepare(policy, argv): pick backend from the start-up probe (bwrap -> Landlock -> none)
      policy confined + a backend passed   -> Kind=confined
      policy danger-full-access            -> Kind=unconfined (backend none)
      policy confined + no backend passed  -> refuse sandbox/unavailable, NOTHING starts
  C38.Start(Spec{Launch}) -> child: [bwrap ...] dsh sandbox-launch            (no command exists yet)
      stage, Kind=confined:   confine self -> open canary for writing -> send ready{backend, governed, canary} -> WAIT
      stage, Kind=unconfined: no confinement, no canary -> send ready{none, [], not-tried} -> WAIT
      host (C37 Confirm): Confinement = weaker(start-up probe, this ready); unknown = not governed
          confined and canary written/not-tried     -> close, sandbox/launch-failed, re-probe once
          unconfined and stage reports otherwise    -> close, sandbox/launch-failed
          Require()==full and State!=Full (either kind) -> close, sandbox/enforcement-insufficient
              (every close: stage sees EOF -> exits, NO command ran -> Outcome{Failed, code, Confinement})
          otherwise -> send go -> stage execs bash (Full | Partial{Missing} | Unconfined)
      host dies before go -> stage sees EOF -> exits, NO command ran
  bash write to /outside -> EACCES/EROFS -> exit 1 -> Outcome{Exited, code 1, Confinement}
model -> asks to escalate with a reason
  C33.Escalate(req) -> approval/request (cli answerer or SDK client) -> allowed-once
      -> Policy{roots=[W, /outside]} for this one call -> rerun -> success, logged
File tools take the same path: fs/ops refuses /outside with fs/sandbox-denied before any I/O.
```

### 6.4 F4 -- A second launch, and a crash explained at the next start (S15, S16; proved by DH-E-HOST-4 in S18)

```
dsh web #2 (same home; headless and sdk never take this path, see sec. 3.2 "Who may coexist")
  C52: read runtime record -> record lock held? -> connect control endpoint (C46)
       -> held: launch.open on the running web host = opened | printed | launch/no-channel -> exit 0
       -> lock free, record present: unclean exit
            -> find newest crash report (host/diag) -> print "dsh did not stop cleanly; report: <path>; start with --safe"
            -> take the lock, replace the stale record, boot normally (or with optional bundles off under --safe)
any dsh (web, headless, sdk) at start: a free-lock record or a new crash report also prints the unclean-exit line
host dies (panic, fault, failure at load)
  host/diag writes owner-only report (cause, stacks) -> stderr names it -> exit non-zero; at most ten kept
```

### 6.5 F5 -- One bounded stop (S15, S16; proved by DH-T-CLI-8/-9, DH-E-SDK-2 later)

```
inputs: Ctrl-C | SIGTERM/SIGHUP | console close | stdin EOF (headless) | dsh stop (control) | stdio end (sdk)
   -> C51 lifecycle.Watch state machine: Running -> Stopping (first signal)
         -> launch.App.Stop(cause): agents Stop(cause) -> turns closed as aborted in the log
            -> subprocess StopTree(grace) for every live command -> store flush -> remove runtime record (if this process wrote one) and the per-pid lock
         -> Stopping + second Ctrl-C, or ShutdownTimeout elapsed -> Forced: exit 124
   exit status: 0 clean | 124 forced | 125 client stalled (sdk) | 130 interrupted (headless, Ctrl-C)
```

The exit status 130 for an interrupted headless run is this document's proposal; CLI-3's
exit-code table (M1) is the authority and the implementation plan aligns to it.

### 6.6 F6 -- Instructions follow the working directory (S13; proved by DH-E-CONTEXT-2 in S18)

```
session start / resume / workdir change (C31 inbox notice) / watch event / tool touched AGENTS.md
  -> C41 discover from workdir up to root -> read each via C32 (bounded) -> digest
  -> changed digest? fold instructions.visible -> next prompt renders framed, budgeted section
  -> link to a file outside the roots -> C32 refuses -> refused[] + diagnostic; text never reaches the model
```

### 6.7 F7 -- The acceptance pipeline that closes M2 (S16.8, S17, S18)

```
build once (go-toolchain) -> dsh binary + checksum                       [DEC: build once, pass DSH_BIN]
  -> C57 harness-gate require --stage DH-S-16 -> DSH_REQUIRE_SCENARIOS=STORE-1,STORE-2,STORE-6,FS-3
  -> step 16.8 guarded batch on scratch homes: STORE-1, STORE-6, FS-3 on Windows native + Linux (WSL)
     lanes (replay provider); STORE-2 on the WSL lane only, signed-in claude-code + bwrap -> evidence records
  -> only then: S17 on Linux, E1 (signed-in claude-code must pass; copilot recorded either way)
        TOOLCHAIN-1 first (record, then keyless replay) -> 12 more -> SANDBOX-7 (closes UP-DEBT-found.Q3.2)
  -> S18 on Linux E2 variants (bwrap+userns, Landlock only, neither/old kernel) + Windows
        TOOLCHAIN-2 first -> ... -> NODEFS-1 last
  -> per scenario: C55 scratch home, deadline, cleanup, record -> DSH_SCENARIO_REPORT
  -> C57: every listed scenario passed -> stage closed -> built-through advances -> M2 reached
```

Scenario code lives in `src/harness/internal/testkit/scenarios/` (the location the
workflow and gate already use); impl.md sec. 7.2 names `./test/scenario/...` -- see
inconsistency I-2 in sec. 8.

---

## 7. Invariants

| Id | Invariant | Enforced by | Proved by |
|---|---|---|---|
| INV-1 | No tool, instruction read or working-directory check touches a project file except through `world/fs` | C32 conformance suite; import rule (only `fs/ops` imports `sandbox/policy`; tools import no `os` file APIs) | DH-T-FS-8..-13, arch test |
| INV-2 | A command starts under its plan or does not start | C38 refuses `Spec` without `Launch`; the stage execs only after `go` (INV-14) | DH-T-EXEC-8, DH-T-SANDBOX-4 |
| INV-3 | Reported enforcement is never stronger than real enforcement | C37: unknown evidence or custom runner never `Full`; weaker of probe and stage | DH-T-SANDBOX-3/-7/-13, DH-E-SANDBOX-1 |
| INV-4 | `require: full` never runs a partially confined command | C37 `sandbox/enforcement-insufficient` | DH-T-SANDBOX-14, DH-E-SANDBOX-1 |
| INV-5 | An approval is one-shot: allowed-once, rejected or cancelled; never a standing grant | protocol type; C49 answerer validation | DH-T-SDK-13/-14 |
| INV-6 | Output memory is bounded regardless of command output size | C38 rings + spill | DH-T-EXEC-7, DH-T-NODEPROC-9 |
| INV-7 | In the `sdk` profile stdout carries frames only | C48 claim and redirect | DH-T-SDK-21/-22, DH-E-SDK-1 |
| INV-8 | One listening web host per home and address (a second `dsh web` reuses it); one writer per session (any number of headless/sdk/web processes may share a home, a resume of a live-owned session is refused with `session/owned`) | Runtime record + its lock (C46, C52); session lease epoch + per-pid lock (M1 store) | DH-T-HOST-15/-16, DH-T-CLI-7; DH-E-STORE-1, DH-E-FS-3 |
| INV-14 | No command runs before the host has accepted the stage's `ready`; EOF or rejection before `go` means no command ran; a canary that is written or not tried is a failed confinement only for a `confined` plan, never for an `unconfined` one | C36 waits for `go`; C38/C37 send `go` only after `Confirm`'s ordered decision (sec. 4.2) | DH-T-SANDBOX-4, DH-E-SANDBOX-1 |
| INV-9 | Every stop is bounded and leaves no child process | C51 single path; `StopTree` | DH-T-CLI-8/-9, DH-E-EXEC-1 |
| INV-10 | The admin tree constructs no carrier, agent, model adapter or plugin process | C42 `admin` bundle; C50 | DH-T-CLI-4/-16, DH-T-BOOT-14 |
| INV-11 | A secret value never appears in argv, stdout or the log | `dsh secret set` reads stdin; `shellexec/env` drops secret names | DH-T-CLI-16, DH-T-EXEC-13 |
| INV-12 | Default profiles send nothing off the machine until a route is configured | C42 defaults | DH-T-BUNDLE-3/-6 |
| INV-13 | A scenario that cannot run fails; it never skips | C55 `need`, C57 required list | DH-T-UTIL-19/-20 |

The "Proved by" column names tests from each deliverable's done-list in its area
specification; which single test carries which invariant is pinned by the
implementation plan when the test is written.

---

## Test & Environment Contract

The gate host (the `harness-gate` lanes `win.ps1` on Windows native and `wsl.sh` on
Linux under WSL, and the hosted `ubuntu-latest` / `windows-latest` CI jobs) has **no
docker and no pre-provisioned services**.

**Two separate axes.** Every row below states (1) its **proof strategy** -- what kind of
evidence it is -- and (2) its **venue** -- where and when it runs. They are not mixed:

- Proof strategy is one of the four the brief defines. `in-process`: logic, zero
  external I/O. `golden`: a committed snapshot is the equality oracle. `service:<name>`:
  needs a live backing service, named, with its provider stated. `lab`: physical or
  hardware lab only. **M2 has no `lab` row**: impl.md sec. 7 states "This product has
  no lab", and every real environment M2 needs (a signed-in model client, a Linux
  kernel with bubblewrap or Landlock, a systemd/cgroup host, a Windows machine, git and
  an editor) is a software service or an OS host, so it is a `service:<name>` row.
- Venue is one of: `gate` (in the required gate run on both lanes and hosted CI; only
  gate-local services below); `guarded` (a build-stage scenario of step 16.8, run by
  the lane script on scratch homes before S16 closes, `-tags scenario`, with its named
  provided services preflighted); `acceptance` (S17 or S18, run by the credentialed
  workflow or a tester's shell per impl.md sec. 7.2, closing through an evidence
  record). Credentialed scheduling (never on a fork PR, schedule or dispatch) is a
  property of the `acceptance` venue, not of the proof strategy.
- A `service:<name>` row is never `inline` in a build step's closure: it closes only by
  its venue's run and record.

### Gate-local services (created by the test itself; no external provisioning)

| Service | How the gate provides it | Used by |
|---|---|---|
| `service:filesystem` | `t.TempDir()` per test; symlinks created by the test (on Windows, the lane runs with Developer Mode or the test uses a junction where a symlink is refused, and records which) | C31, C32, C35, C41, C43, C46 |
| `service:os-process` | The test binary re-executes itself (helper-process pattern) or the test starts the built `dsh` from `service:go-toolchain`; `testkit/procwait` reaps; no daemon | C36, C38, C39, C46, C48, C51, C52, C53 |
| `service:go-toolchain` | The Go 1.27.4 toolchain already on the lane builds `dsh` once into a temp directory (`testkit/dshbin`) | binary-level tests, C53, C55 |
| `service:loopback-http` | `httptest` / the real listener bound to `127.0.0.1:0`; TLS certificates generated per test | C44, C45, C47 |
| `service:sqlite` | Embedded `modernc.org/sqlite` (pure Go, no extension, no DSN) through `session/store/sqlite` | C43 lease fold, C49 history, resume |
| `service:local-socket` | A Unix-domain socket in `t.TempDir()` on Linux; on Windows an AF_UNIX socket in the temp directory (supported on the Windows lane's build) | C46 control endpoint, C52 |
| Shells | `bash` on the Linux lane; `pwsh` on the Windows lane (`windows-latest` ships it; the local Windows lane's preflight checks it). A shell row runs only on the lane that has the shell and **fails** (never skips) if preflight did not find it | C39, C40 |

### Provided services (not on a bare gate host; each has a named provider and a preflight)

| Service | What it is | Provider and how it is reached | Preflight (fails the run, never skips) | Venues |
|---|---|---|---|---|
| `service:model-route-claude-code` | The signed-in Claude Code CLI, started by dsh as a child through `llm/provider/claudecode` (decision `models-signed-in-claude-copilot`; M1-D-PIPE-3) | The tester's own sign-in on the Linux machine running the job (the WSL lane machine for `guarded`; the S17 machine for `acceptance`). No key is stored or passed | The existing `signedin_routes_test.go` helper: binary found by the product's discovery and a probe turn succeeds | guarded (DH-E-STORE-2), acceptance (S17, FS-2, VIEW-5) |
| `service:model-route-copilot` | The signed-in Copilot CLI via `llm/provider/copilot` | As above | As above; its result is recorded either way (M1-D-LOOP-6) | acceptance (S17) |
| `service:linux-sandbox-bwrap` | A Linux kernel with unprivileged user namespaces and `bwrap` on an absolute path | The WSL lane machine, with `bwrap` added to the lane's provisioning by this story (today `tools/harness-gate/lanes/wsl.sh` does not install or check it) for `guarded`; an E2 machine for `acceptance` | `dsh sandbox status` must report backend `bwrap`, state `Full`; anything else fails preflight | guarded (DH-E-STORE-2), acceptance (S18) |
| `service:linux-landlock-only` | Linux with a Landlock ABI and no `bwrap` | Team-kept Linux VM reached over SSH (impl.md sec. 7.1 E2; Q `m2-e2-fleet`) | `dsh sandbox status` reports backend `landlock` | acceptance (S18) |
| `service:linux-unconfined` | Linux with neither `bwrap` nor Landlock (older kernel) | Team-kept Linux VM over SSH | `dsh sandbox status` reports `Unconfined` with the missing effects named | acceptance (S18) |
| `service:linux-systemd-cgroup` | Linux with a user service manager and writable cgroup v2, plus one machine whose user manager is stopped | Team-kept Linux VMs reached over SSH with a scratch account (impl.md E2/E3) | Host address, user and key inputs present; `systemctl --user` state recorded | acceptance (EXEC-1, EXEC-10) |
| `service:windows-host` | A Windows machine running the built binary | Hosted `windows-latest` or the Windows native lane machine | Built binary checksum matches | guarded, acceptance (S18 Windows rows) |
| `service:git-and-editor` | `git` and an editor save script (write temporary file, rename) | Installed on the test machine before the run (impl.md E4 pattern) | Found by the product's own discovery, version recorded | guarded (DH-E-FS-3 save script), acceptance (CONTEXT-2, VIEW-5, FS-2) |
| `service:vm-power-cut` | A virtual-machine controller that can hard power-off and restart a guest and create/delete disposable disks: a team-kept Linux KVM/libvirt host (Linux guest) and a team-kept Hyper-V host (Windows guest). Virtual machines only, so this is a service, not `lab` | Team-kept hypervisor hosts reached over SSH (libvirt) and PowerShell remoting (Hyper-V) with a scratch account and a base image per guest OS (impl.md E2; Q `m2-e2-fleet`) | Controller reachable; a throw-away guest is created from an overlay, powered off hard, restarted and deleted; base image checksum recorded | acceptance (TC-21s5 only) |
| `service:ack-recorder` | The "second machine" of DH-E-NODEFS-1: a small listener that appends each acknowledged version (guest, iteration, version, monotonic sequence) to an append-only log on its own disk | Runs on the hypervisor host (a different machine from every guest, whose power is never cut), started by the scenario and stopped in Cleanup; reached from guests over the hypervisor's private network | A test acknowledgment from a throw-away guest is recorded and read back before iteration 1 | acceptance (TC-21s5 only) |

### Contract rows

Row ids: `a` = logic, `g` = golden oracle, `b` = gate integration, `s` = scenario on
provided services. "Venue" is the second axis defined above.

| TC | Acceptance area (deliverables) | Proof strategy | Dependency and how it is provided | Venue |
|---|---|---|---|---|
| TC-01a | Cycle C-3 contracts: `workingDirectory` fold, resolver precedence, `Policy` has no valid zero value, `SetMode` writes `sandbox/mode`, policy text per platform (VIEW-10, SANDBOX-1) | `in-process` | None: fake seam (`world/fs/fstest`), fake clock, in-memory views | gate |
| TC-01b | Seam conformance, fence on symlink swap, `fs/sandbox-denied`, `fs/stale-version`, directory recovery (FS-4, VIEW-10) | `service:filesystem` | `t.TempDir`; both lanes | gate |
| TC-01g | Model-visible policy section text for Linux, macOS and Windows | `golden` | Committed golden beside the resolver (pattern of `human/approval/testdata/policy_section.golden`); its version is the one SANDBOX-7 records | gate |
| TC-02a | Permissions: pin at creation, switch moves mode and approval policy, default must resolve (HUMAN-4) | `in-process` | None | gate |
| TC-03b | Six file tools, bounded results, overlap declarations, grep without external `rg` (FS-6) | `service:filesystem` | `t.TempDir`; `PATH` scrubbed in the test to prove no `rg` child | gate |
| TC-04a | Backend selection, plan `Kind` at `Prepare` (confined / unconfined / refused `sandbox/unavailable`), the four-step host decision of sec. 4.2 (canary `written` or `not-tried` on a confined plan rejects; `not-tried` on an unconfined plan is accepted; `require: full` rejects either kind), enforcement from probe and `ready` (weaker wins), bwrap argv and Landlock rule sets (SANDBOX-3, SANDBOX-4 logic) | `in-process` | Fake backends and fake kernel evidence | gate |
| TC-04b | Two-phase launch: stage sends `ready` and blocks; no command before `go`; host rejection or EOF from either side leaves no command run; an `unconfined` plan (`danger-full-access`, backend `none`) reaches `go` and runs with `Unconfined`; the same plan under `require: full` is refused; a stage forced to report canary `written` on a `confined` plan is refused; `Confirm`/`Release` (SANDBOX-2, EXEC-4; DH-T-SANDBOX-4, DH-T-EXEC-8) | `service:os-process` | Test binary re-exec as `sandbox-launch` with the `none` backend on both lanes (the confined-plan rejection uses a test hook that makes the stage report `written`); the "command" is a marker-writing helper whose presence or absence proves exec | gate |
| TC-04s | Real bubblewrap and Landlock confinement; status per machine; writes inside/outside roots; `require: full` on a weaker machine (DH-E-SANDBOX-1, DH-E-NATIVESYSTEM-1..-4) | `service:linux-sandbox-bwrap` + `service:linux-landlock-only` + `service:linux-unconfined` | Provided per the table above; one subtest per machine | acceptance (S18.4) |
| TC-05b | Bounded capture, non-consuming readers, spill, hard limit stops the tree (EXEC-3, EXEC-4) | `service:os-process` | Re-exec helper that floods stdout and spawns a grandchild | gate |
| TC-06b | bash and pwsh executors and tools: argv, caps, timeouts, `Failed`, scrubbed env (EXEC-8, EXEC-9) | `service:os-process` | bash rows on the Linux lane, pwsh rows on the Windows lane (see Shells); Windows rows use a `danger-full-access` policy (the `unconfined` plan kind, R-05) and one row asserts a `workspace-write` start there is refused `sandbox/unavailable` | gate |
| TC-07a | Instruction discovery, budget, frame escape, refused outside link (CONTEXT-2) | `in-process` (fake seam) and `service:filesystem` (watch reconciliation) | Fake seam; `t.TempDir` | gate |
| TC-07g | Rendered instruction section | `golden` | Committed golden under `context/instructions/testdata` | gate |
| TC-07s | Real editor save and `git` branch switch reach the model; outside link refused (DH-E-CONTEXT-2) | `service:git-and-editor` + `service:os-process` | Provided per the table above | acceptance (S18.9) |
| TC-08g | Five profiles composed; layer operations; `headless.yml`, `sdk.yml` content (BUNDLE-1, -2, -3) | `golden` | Existing `boot/profile/testdata/composed.golden`, regenerated in the step that changes it and reviewed as a diff | gate |
| TC-08a | Coverage: every catalogue entry is in a profile or `optional.yml`; defaults send nothing (BUNDLE-6, -2) | `in-process` | None | gate |
| TC-09b | Preset revisions, leases, `Extends`, persona refusal outside a preset (BUNDLE-4, -5) | `service:sqlite` | Embedded modernc SQLite in `t.TempDir` | gate |
| TC-10b | Listener, TLS reload, longest-prefix mounts, admission by source then cookie, route table swap (HOST-1, HOST-2, API-1) | `service:loopback-http` | `127.0.0.1:0`; generated certificates | gate |
| TC-11b | Runtime record written only by a listening host; control endpoint; a second `dsh web` reuses it; a headless process beside it writes no record; unclean-exit notice, `--safe`, statuses 70/78 (HOST-3, CLI-5) | `service:os-process` + `service:local-socket` + `service:filesystem` | Built `dsh` (go-toolchain) started twice on one temp home; a killed first host for the crash path | gate |
| TC-12a | SDK server: ownership, readiness, ordered scoped notifications, run result equals last assistant message, answerer validation and withdrawal (SDK-4, -5, -6) | `in-process` | `src/sdk/jsonrpc/jsonrpctest` peer, `testkit/model` fake model, memstore | gate |
| TC-12b | Stdio ownership and bounded endings 0 / 124 / 125 (SDK-3) | `service:os-process` | Built `dsh --profile sdk` driven over pipes with the keyless fake model route configured by a test layer | gate |
| TC-12g | Wire schema unchanged by M2 | `golden` | `src/sdk/protocol/schema.json` regenerated by `internal/genschema` must be byte-identical | gate |
| TC-13g | Command reference with launchers and admin tree | `golden` | `cli/testdata/reference.golden` | gate |
| TC-13b | `dsh headless` prints the interval's last assistant text; `--json`; admin tree builds no agent; `secret set` from stdin; `config show`/`validate` (CLI-2, CLI-9, BOOT-6 / H-21) | `service:os-process` | Built `dsh`, temp home, keyless fake model route | gate |
| TC-14b | One bounded stop from Ctrl-C, SIGTERM, console close, EOF, `dsh stop` (CLI-4) | `service:os-process` | Signals on the Linux lane; console control events on the Windows lane (`terminate_windows_test.go` pattern) | gate |
| TC-15b | Go client start, run, handlers, shutdown ladder (SDK-8) | `service:os-process` + `service:go-toolchain` | `DSH_BIN` set to the binary built once by the test | gate |
| TC-16g | Keyless replay oracle: the committed recording, the expected normalized log, the expected workspace tree and the expected model requests (UTIL-7) | `golden` | Committed under `test/replay`; normalization by fixed typed tokens; no key | gate |
| TC-16b | Executing that replay: the built binary replays the recording into a scratch home and workspace; the persisted log is read back from the store and the workspace and requests are compared with TC-16g (UTIL-7; DH-T-UTIL-16..-18, DH-T-TOOLCHAIN-5) | `service:go-toolchain` + `service:os-process` + `service:filesystem` + `service:sqlite` | `testkit/dshbin` builds `dsh` once; `testkit/fixture` copies the workspace into `t.TempDir`; the run's embedded SQLite store in the scratch home is the persisted log | gate |
| TC-17a | Scenario helper: missing input fails, deadline, one record per scenario; analyzer forbids internal imports (UTIL-8) | `in-process` + `service:go-toolchain` | `analysistest` in the tools module | gate |
| TC-18a | Titles: fallback, normalization, generator via replay (VIEW-4) | `in-process` | Replay provider (C54) | gate |
| TC-19s1 | Step 16.8 guarded, no model: DH-E-STORE-1 (two headless processes on one home, `session/owned`, takeover with epoch + 1 -- legitimate per sec. 3.2 "Who may coexist"), DH-E-STORE-6 (deletion and interrupted deletion), DH-E-FS-3 (two processes and an editor save script, `fs/stale-version`) | `service:os-process` + `service:filesystem` + `service:sqlite` + `service:git-and-editor` (FS-3's save script only) | Built `dsh` on both lanes (Windows native, Linux under WSL); STORE-1 and STORE-6 use the keyless replay provider (C54) as their Given states; FS-3 drives `edit` through the replay provider; required list from `harness-gate require --stage DH-S-16`. macOS row: Q `m2-macos-rows` | guarded (16.8) |
| TC-19s2 | Step 16.8 guarded, real model and real sandboxed shell: DH-E-STORE-2 (host killed mid-turn while a sandboxed `sleep 60; write` runs; resume shows the turn interrupted and the tool call "outcome unknown"; the first model request after the next prompt contains that result) | `service:model-route-claude-code` + `service:linux-sandbox-bwrap` + `service:os-process` + `service:filesystem` + `service:sqlite` | WSL lane machine with the tester's Claude Code sign-in and `bwrap` (provided services table). The scenario text is kept; the one permitted amendment is E1 "real model API key" -> signed-in route (I-1): the existing `DSH_SCENARIO_STORE2_KEY` input of `store_crash_recovery_scenario_test.go` is replaced by the route input. The model request is read from the session log (`DSH_SCENARIO_STORE2_REQUEST`), not from the provider. **Platform scope (settled, not open):** impl.md step 16.8 itself binds this scenario to Linux -- "DH-E-STORE-2 (a host killed mid-turn under the Linux sandbox ...)" -- and the M2 milestone row says "Team pre-release only, on scratch homes, on Linux ... no sandbox backend exists on macOS or Windows before DH-S-27". So the 16.8 obligation for STORE-2 is the Linux row only; there is no Windows STORE-2 row in M2 and none is left open. Closure consequence: TC-19s2 passing on the WSL lane is sufficient for "the guarded scenarios of DH-S-16 have passed" (sec. 1.1); a Windows run of STORE-2 belongs to DH-S-27 (Windows backend), outside this story | guarded (16.8, Linux only, per impl.md) |
| TC-20s | S17, 14 scenarios on a real model (TOOLCHAIN-1 first, SESSION-1/-2, AGENT-1..-4, LLM-1/-3, TOOLS-1, VIEW-1, LOADER-1, SDK-1, SANDBOX-7) | `service:model-route-claude-code` (must pass) + `service:model-route-copilot` (recorded) + `service:os-process` + `service:filesystem` + `service:sqlite` | Tester's Linux machine per `models-signed-in-claude-copilot`; SANDBOX-7 reads the three platform policy texts from the resolver without running a backend | acceptance (S17) |
| TC-21s1 | S18 OS-variant scenarios: SANDBOX-1, NATIVESYSTEM-1..-4 | as TC-04s | as TC-04s | acceptance (S18.4) |
| TC-21s2 | EXEC-1 (cgroup v2 tree), EXEC-10 (SSH, systemd, memory limit) | `service:linux-systemd-cgroup` + `service:os-process` | Team-kept VMs over SSH | acceptance (S18.3) |
| TC-21s3 | HOST-4, FS-1, RIPGREP-1/-2, CHOKIDAR-1/-2, STORAGE-6, TOOLCHAIN-2 (first) | `service:os-process` + `service:filesystem` on `service:windows-host` and Linux (`service:linux-sandbox-bwrap` machine) | Hosted runners where the variant exists, else the lane machines | acceptance (S18) |
| TC-21s4 | FS-2, VIEW-5, CONTEXT-2 (real repository, model or editor) | `service:model-route-claude-code` (FS-2, VIEW-5) + `service:git-and-editor` + `service:linux-sandbox-bwrap` (VIEW-5 `workspace-write`) | Tester's Linux machine | acceptance (S18.5, 18.6, 18.9) |
| TC-21s5 | DH-E-NODEFS-1, durable replace under power loss (step 18.11, run last in S18): per guest OS, a loop replaces a file durably through `fsx.Replace` and sends each acknowledged version to a second machine; the guest is powered off (not shut down) at a random moment and restarted, 100 times; after each restart the file is complete and never older than the last acknowledged version (`DH-LIB-NODEFS-R1`) | `service:vm-power-cut` + `service:ack-recorder` (no `service:os-process`/`service:filesystem` substitute: a hosted runner or lane machine cannot cut its own power) | **Provisioning:** the controller clones one disposable disk per guest per run (copy-on-write overlay of a base image holding the built `dsh` test binary and the loop), boots the guest, and points the loop at the recorder's address. **Driver:** the scenario process on the controller picks a random delay, issues a hard power-off (`virsh destroy` on the Linux hypervisor; `Stop-VM -TurnOff` on Hyper-V), restarts the guest, reads the file through the guest agent, compares it with the recorder's last acknowledged version, and repeats to 100. **Cleanup:** delete every overlay disk and the guest definitions; keep the recorder's version log and the overlay of any failing iteration (the specification's Cleanup). Guests: Linux and Windows; macOS per Q `m2-macos-rows`; fleet ownership per Q `m2-e2-fleet` | acceptance (S18.11) |

### Rules for downstream documents

1. The implementation plan gives every step a TC row from this table; a step whose
   proof is not a row here adds a row in the next iteration of this document first.
2. No **gate-local** row (the "Gate-local services" table: filesystem, os-process,
   go-toolchain, loopback-http, sqlite, local-socket, shells) may be satisfied by
   docker, a remote database or a pre-started daemon; a gate-local test that needs one
   is wrong for the gate. **Provided services** (the "Provided services" table) are
   exempt by definition: they are named, pre-existing infrastructure -- for example the
   user service manager of `service:linux-systemd-cgroup` (TC-21s2), the hypervisor of
   `service:vm-power-cut` (TC-21s5) -- reached only from `guarded` or `acceptance`
   rows, each behind its preflight, and never from a `gate` row.
3. Rows whose venue is `acceptance` (TC-04s, TC-07s, TC-20s, TC-21s1..s5) are never in
   a build gate's required list and never `inline` in a build step's closure: they
   close S17 or S18 only, through an evidence record. Rows whose venue is `guarded`
   (TC-19s1, TC-19s2) are required for S16 to close, run on the named lanes with their
   provided services preflighted, and are also never `inline`. There is no `lab` row in
   M2; a downstream document that introduces one must name the physical lab.
4. A row that cannot run on a lane fails there; it never skips (INV-13). Shell rows are
   placed on the lane that has the shell rather than skipped on the other.
5. Golden rows change only in the step that changes the behavior, and the diff is
   reviewed; `schema.json` (TC-12g) must not change in M2 at all.
6. E2E scenario texts (e2e-scenarios.md) keep the area specification's Given, When,
   Then and Cleanup and add only the environment binding from this table.
7. Run order: TC-19s1 and TC-19s2 before any TC-20s/TC-21s row; TOOLCHAIN-1 first in
   S17, TOOLCHAIN-2 first and NODEFS-1 last in S18; S17 and S18 are dispatched as
   separate runs.
8. Real confinement is allowed as a dependency only through a named provided service
   (`service:linux-sandbox-*`) whose preflight checks the reported backend; a gate row
   (venue `gate`) never depends on it and proves the launch protocol with the `none`
   backend (TC-04b).

---

## 8. Risks, open items and inconsistencies

### 8.1 Risks

| Id | Risk | Effect | Mitigation |
|---|---|---|---|
| R-01 | M1 is not closed (`built-through: DH-S-8`; the eleven records are `open`) | M2's entry prerequisite fails; S17 scenarios that prove M1 deliverables (AGENT, LLM, TOOLS) fail for M1 reasons | Q `m2-entry-m1-closure`; S12 to S14 build on M1 contracts that exist and do not need M1's acceptance |
| R-02 | Ubuntu 24.04 hosted runners restrict unprivileged user namespaces through AppArmor; WSL kernels vary in Landlock ABI | bwrap probes fail on hosted runners; "full" is not reachable where the scenario expects it | C37 reports the truth (INV-3); the E2 variants are named machines, not "whatever the runner is" (Q `m2-e2-fleet`) |
| R-03 | SANDBOX-7 is 108 model runs per policy text (12 tasks x 3 runs x 3 texts) on signed-in client routes | Quota and time on the tester's account; flaky counts | Run it last in S17; record counts beside the text version; the scenario's own threshold (never less than 10 of 18) is the pass rule |
| R-04 | pwsh is absent on the Linux lane and bash on the Windows lane | Shell rows cannot run everywhere | Rows are placed per lane (TC-06b); preflight fails loudly |
| R-05 | Windows has no sandbox backend in M2 | Confined starts on Windows are refused (`sandbox/unavailable`); only `danger-full-access` commands run, as `Unconfined` plans | Reported, never hidden; `require: full` refuses even those; Windows-lane rows that run a shell set `danger-full-access` in their scratch home's test layer and assert `Unconfined` on the result; nothing is enabled for users in M2 |
| R-06 | Cycle C-3 built at one commit is a large change | Review burden; partial landing breaks the build | Contract-first commit (interfaces and fakes), then the three implementations, ticked together (impl.md sec. 10.2) |

### 8.2 Held open at M2

H-1 (schedule activation hook, SDK-4), H-2 (multi-route validation, SDK-4), H-3 (inline
images, SDK-4), H-4 (goal stop signal, SDK-5), H-10 (replay of sessions with children,
UTIL-7). H-21 closes at step 16.10. The parts that wait are not enabled; SDK-4 and
SDK-5 are built, not done.

### 8.3 Inconsistencies for the sibling documents and the next iteration

| Id | Where | Inconsistency | This document's position |
|---|---|---|---|
| I-1 | impl.md sec. 7.1 E1, DH-E-SDK-1 Given, DH-E-VIEW-5, DH-E-SANDBOX-7, DH-E-STORE-2 Given (and its `DSH_SCENARIO_STORE2_KEY` input) | Name "a real model API, key supplied at run time"; owner decision `models-signed-in-claude-copilot` removes the key | E1 = `service:model-route-claude-code` / `service:model-route-copilot` (TC-19s2, TC-20s, TC-21s4); tech-spec records the amendment |
| I-2 | impl.md sec. 7.2 | Scenarios under `./test/scenario/...`; the repository and workflow use `src/harness/internal/testkit/scenarios` | Keep the existing location; UTIL-8's `test/scenario` holds the helper's own tests |
| I-3 | app-cli area | Names a package `internal/cli/tree`; the tree today is `tree.go` in package `cli` | Keep it in package `cli` unless the implementation plan moves it in one refactor step |
| I-4 | DH-E-HOST-4 Then | "a start in safe mode reaches the UI"; M2 has no UI | Q `m2-host4-ui-clause` |
| I-5 | 16.8, S18 scenario Givens | Name macOS; decision `platforms-windows-wsl` excludes it | Q `m2-macos-rows` |
| I-6 | Sec. 6.5 | Exit status 130 for an interrupted headless run is a proposal | CLI-3's table (M1) is the authority |
| I-7 | impl.md step 16.8 lead-in ("on scratch homes on Linux, macOS and Windows") vs its own STORE-2 clause ("under the Linux sandbox") and the M2 row ("on Linux") | The lead-in reads as if every 16.8 scenario runs on all platforms | Resolved by the step's more specific clause: STORE-2 is Linux only (TC-19s2); FS-3, STORE-1, STORE-6 run on Windows and Linux (TC-19s1); macOS per Q `m2-macos-rows`. No open Windows STORE-2 row |
| I-8 | `tools/harness-gate/lanes/wsl.sh` | Does not install or check `bwrap`, which TC-19s2 needs | The implementation plan adds `bwrap` provisioning and a `dsh sandbox status` preflight to the WSL lane |
