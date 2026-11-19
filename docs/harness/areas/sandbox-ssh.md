# Process confinement (the sandbox) and SSH remote execution: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". IDs in this document use the area key `SANDBOX`. Module and
> standard-library facts were checked on 2026-11-18 with a Go 1.27.4 toolchain (`go doc`, `go list -m -json`, `go mod download -json`, source in the module cache). The module floor is `go 1.26.0`; the one standard-library fact this design rests on, that `crypto/tls` offers no pre-shared-key mode for a caller's key, is an absence that holds at the floor as well.

| | |
|---|---|
| **Area key** | `SANDBOX` (agent A10) |
| **Upstream packages** | 10: `packages/sandbox` (4), `packages/ssh` (5), `scripts/ssh-helper` (1) |
| **Serves outcomes** | [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10), [DH-O-11](../design.md#dh-o-11), [DH-O-14](../design.md#dh-o-14), [DH-O-5](../design.md#dh-o-5) |
| **Depends on areas** | A09 (the one process starter, terminals), A08 (filesystem contract, the fence), A06 (session working directory), A03 (tool endpoint, prompt assembly), A17 (approvals, session grants, consent), A22 (plugin processes), A07 (machine-state tables), A20 (JSON-RPC codec), A23 (`watch`), A24 (command grammar) |
| **Priority** | P0 for the policy, the launch stage, the seam and Linux; P1 for macOS, Windows and SSH. Until a platform's backend exists, confined modes there fail closed |

## 1. Purpose

The model writes shell commands, and the harness runs them as the user. This area makes sure a
spawned command can write only where the resolved **policy** says, and that the harness says
truthfully how much of that the operating system enforced. If confinement cannot be applied, the
command does not run ([DH-O-7](../design.md#dh-o-7)). The same mechanism confines plugin processes ([DH-O-11](../design.md#dh-o-11)).

The second half moves the whole execution world (files, processes, terminals, sandbox) to another
machine over SSH, so the model works on a remote checkout while the harness, the model credentials
and the session log stay local ([DH-O-10](../design.md#dh-o-10)). Without this area the choices are "run everything unconfined" or "run nothing".

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **Mode** | `read-only`, `workspace-write` or `danger-full-access` | One file-effect vocabulary for every operating system (`docs/subsystems/sandbox.md:11`) |
| **Write root** | A directory a `workspace-write` command may write under, with its source (workspace, configuration, registration, session grant, plugin state) | Says exactly what "the workspace" is and who answers for each extra directory |
| **Scratch** | One private temporary directory per session and world, owned by the harness | Commands need temporary files even when they may not touch the user's files |
| **Policy** | Mode, write roots, scratch, network setting and required enforcement, resolved for **one** start | Two sessions, or one approved retry, need different limits at the same moment |
| **Backend** | The operating-system mechanism: bubblewrap, Landlock, Seatbelt, or the Windows restricted token | Each system offers a different primitive |
| **Governed effect** | One thing a backend demonstrably stops, such as "write outside the roots", "connect to the network" or `delegated-execution` (asking a same-user service, such as a service manager or a task scheduler, to run a program outside the confinement) | Enforcement is computed from these, not claimed |
| **Confinement, exposure** | The reported fact for one started process: `full`, `partial` (with the effects not governed) or `unconfined` (only under `danger-full-access` or an explicit helper request). Its exposure list names what of the harness itself the process can still reach: `secret-read`, `control-connect` | The caller is told, not reassured |
| **Launch stage** | The `dsh` binary re-executed as `dsh sandbox-launch`: it confines itself, proves it, then becomes the command | The command is born confined while the host is not |
| **Status channel, canary** | A private descriptor pair between host and stage that closes when the command starts; the canary is a host-owned file outside every write root that the stage tries to open for writing | Launcher status never travels through the command's output, and each start carries direct evidence that writes are blocked |
| **Capability identifier (SID), grant journal** | A Windows security identifier that means "may write here": the token must carry it and the directory must list it. The journal is a table of every access-control change the harness made | The unit of the Windows allow-list; cleanup may undo only what the harness created |
| **World** | One filesystem and one process table: the local machine, or one SSH host whose providers are mounted in place of the local ones | File tools, shells and the sandbox must agree on where a path lives |
| **Helper** | The `dsh` binary on the remote host, started as `dsh ssh-helper` | It does file and process work where the files are |
| **Multiplexer (mux)** | A library that carries many independent byte streams, each with its own flow-control window, over one connection | One SSH channel carries control messages and every program stream |
| **Lease, receipt** | The helper cleans up if it hears no heartbeat for the lease time, and writes a receipt file when cleanup ends | A lost connection must not leave remote processes running, and the host can later learn what happened |
| **Pre-shared key (PSK)** | A secret both ends know before a TLS handshake | Upstream uses one per stream; the Go standard library cannot (`DH-ADR-SANDBOX-7`) |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.sandbox.sandbox` | `dsh-sandbox` | definition | `ctx.sandbox.confine(argv, policy)` returns a wrapped argv, an enforcement value and stderr classification rules; owns the escalation ladder and denial markers (`packages/sandbox/sandbox/README.md:12`, `src/escalation.ts:64`) |
| `UP-PKG-packages.sandbox.sandbox-local` | `dsh-sandbox-local` | provider | Chooses a runner per platform and builds its profile (`src/index.ts:178-185`, `src/profiles.ts:16-60`); on Windows also owns directory grants (`src/index.ts:426-497`) |
| `UP-PKG-packages.sandbox.sandbox-policy` | `dsh-sandbox-policy` | provider | `ctx.sandboxPolicy.resolve()`: precedence, workspace root, extra roots, the `sandbox/mode` event, the model-visible policy text (`src/index.ts:212-232`, `:184-192`) |
| `UP-PKG-packages.sandbox.sandbox-windows-acl` | `dsh-sandbox-windows-acl` | provider (library) | Restricted token, access-control grants, a runner process, and a diagnosis skill (`README.md:98`, `:110`, `:78`) |
| `UP-PKG-packages.ssh.ssh` | `dsh-ssh` | provider | `ctx.ssh`: starts OpenSSH, verifies the helper, carries requests and forwarded streams; also contains the helper program (`src/index.ts:299-318`, `src/helper.ts:56`) |
| `UP-PKG-packages.ssh.fs-ssh` | `dsh-fs-ssh` | provider | `ctx.fs` on the remote host (`README.md:12`) |
| `UP-PKG-packages.ssh.subprocess-ssh` | `dsh-subprocess-ssh` | provider | `ctx.subprocess` and terminals on the remote host (`README.md:12`) |
| `UP-PKG-packages.ssh.sandbox-ssh` | `dsh-sandbox-ssh` | provider | `ctx.sandbox` that asks the helper to wrap the argv with the remote host's backend (`src/index.ts:13-32`) |
| `UP-PKG-packages.ssh.ssh-helper-runtime` | (private) | other | Packs Node, the helper and worker entry points into one executable per platform (`README.md:12`, `src/index.ts:28-38`) |
| `UP-PKG-scripts.ssh-helper` | (scripts) | other | Builds, inspects and releases that executable: archive manifest, native payload inspection, handshake probe, four-target release gate (`scripts/ssh-helper/artifact.ts:8`, `probe.ts:13`, `release.ts:1`) |

### 3.2 Behaviour

**Policy.** A tool that spawns a command calls `resolve({session, mode})`. An approved explicit
mode outranks the session's last `sandbox/mode` event, which outranks the deployment default,
`read-only` (`packages/sandbox/sandbox-policy/src/index.ts:219`, `README.md:36`). The workspace root
is the session's working directory (`src/index.ts:232`). Extra roots come from configuration and
from `registerWritableRoot(session, dir)`, an effect owned by the registering plugin for one live
session (`src/index.ts:252-266`). Every call rejects an extra root that is missing, not a directory
or a symlink (`packages/sandbox/sandbox/src/roots.ts:23-47`). The model is told the mode, workspace and extra
roots, and that "some platform temporary areas may also be writable" (`packages/sandbox/sandbox-policy/README.md:131`).

**Seam.** `confine()` returns an argv to spawn, `full` or `partial`, and two lists of stderr
signatures: "the sandbox denied a file effect" and "the runner failed before the command ran"
(`docs/subsystems/sandbox.md:128`). With no usable backend it rejects with `SANDBOX_UNAVAILABLE`
(`:164`); `danger-full-access` never reaches the provider (`:23`). A platform with one candidate is selected without a probe;
Linux probes its two once and caches the verdict for the provider's lifetime (`packages/sandbox/sandbox-local/src/index.ts:546-564`).

| Backend | Profile | Reports | Evidence |
|---|---|---|---|
| bubblewrap (Linux, first) | Read-only root, fresh `/dev`, private process namespace; `workspace-write` adds a private `/tmp` and writable binds | `full` | `packages/sandbox/sandbox-local/src/profiles.ts:16-23` |
| Landlock (Linux, second) | A separate C launcher; read and execute on `/`, write on `/dev/null`, and under `workspace-write` on the **shared** host `/tmp` and the roots | `full` or `partial` by kernel version | `src/profiles.ts:30-36` |
| Seatbelt (macOS) | Allow everything, deny file writes, allow `/dev/null`, the roots, `/tmp` and the per-user temp directory | `full` | `src/profiles.ts:53-60`; `packages/sandbox/sandbox/src/roots.ts:103-105` |
| Windows restricted token | A runner process builds a write-restricted, Low-integrity token; each granted directory gets an allow entry, a deny of delete-child and a Low label | always `partial` | `packages/sandbox/sandbox-windows-acl/README.md:98`; `packages/sandbox/sandbox-local/src/index.ts:206` |
| Custom runner | Operator argv given bubblewrap arguments | `full`, asserted, not probed | `packages/sandbox/sandbox-local/src/index.ts:346-352` |

**SSH.**

| Aspect | Upstream | Evidence |
|---|---|---|
| Connection | `ssh -T -M -S <control path>` with `BatchMode=yes`, `StrictHostKeyChecking=yes`, `ForwardAgent=no`, `ClearAllForwardings=yes`; the helper is the remote command | `packages/ssh/ssh/src/index.ts:299-303` |
| Requests | Length-prefixed JSON on that process's standard streams | `packages/ssh/ssh/src/protocol.ts:136-141` |
| Helper identity | The first reply must match the protocol version, the launch kind and the configured SHA-256, which the helper computes over its own file | `src/index.ts:312-318`; `src/helper.ts:115` |
| Program streams | One Unix socket per stream, listened on by the helper and reached with `ssh -O forward -L` | `src/index.ts:171` |
| Stream authentication | A socket in a writable temporary directory can be replaced or connected to by any process of the same user, so each stream runs TLS 1.2 with the single suite `PSK-AES256-GCM-SHA384` and a random 256-bit key sent only over the request channel | `src/stream-security.ts:7`; `src/helper-processes.ts:423-426` |
| Remote work | The helper runs the *local* filesystem, subprocess and sandbox providers on the remote machine | `packages/ssh/fs-ssh/README.md:42`; `packages/ssh/subprocess-ssh/README.md:48` |
| Lifetime | The client heartbeats; the helper cleans up on end of input, a signal or lease expiry. Both ends must be Linux or macOS | `src/helper.ts:87`, `:57`; `packages/ssh/ssh/README.md:66` |

### 3.3 Failure, retry, rollback and cleanup upstream

| Situation | Upstream behaviour |
|---|---|
| No backend, or every probe fails | `SANDBOX_UNAVAILABLE`; nothing runs (`packages/sandbox/sandbox-local/README.md:57`) |
| The runner fails before the command | Recognised from exit code plus a stderr line; a confined child can forge it (`packages/sandbox/sandbox/README.md:169`) |
| Windows preparation fails half-way | Partial temp grant revoked, directory removed, `SANDBOX_PREPARATION_FAILED`; a workspace grant already applied stays (`packages/sandbox/sandbox-local/src/index.ts:449-493`) |
| Shutdown or crash on Windows | Clean: temp grants revoked, workspace entries and the Low label left on purpose as a reuse cache. Crash: temp directories and entries remain as inert litter (`src/index.ts:508-531`; `packages/sandbox/sandbox-windows-acl/README.md:128-129`, `:183`) |
| SSH connection lost | Every pending operation fails; "remote outcomes and cleanup are unknown"; no reconnect, no replay (`packages/ssh/ssh/src/index.ts:308`, `docs/subsystems/ssh.md:23`) |
| A cancelled SSH request | The caller is released; a completed remote change is not rolled back (`packages/ssh/ssh/src/protocol.ts:103`) |

**The incident.** Postmortem 0004: on older kernels the Landlock launcher prints a harmless notice,
and the consumer read that notice plus any non-zero exit as "the sandbox failed", so `grep` finding
nothing surfaced as `SANDBOX_UNAVAILABLE`. Confinement was not weakened
(`docs/postmortem/0004-landlock-partial-notice-misclassified-child-failures.md:15`, `:23`); the cause was a
result type that could only carry substrings and an in-band channel (`:35`, `:39`). Inference: the opposite
error, a partial sandbox reported as full, is not what happened, but nothing in upstream's types prevents it:
enforcement is a constant per backend (`packages/sandbox/sandbox-local/src/index.ts:196-207`) and a custom runner is `full` by assertion. The Go design closes both directions (§7).

## 4. Upstream decisions dissected

The first seven rows are the handed-over notes. Each lies under `.agents/notes/` in the
directory its unit ID names; `...` abbreviates that directory.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `UP-DEC-implemented.architecture.2026-09-11-posix-ssh-runtime` | An OpenSSH alias reaches an installed helper; filesystem, subprocess and sandbox share it; requests and each stream use separate SSH channels; streams use TLS-PSK; no reconnect or replay (`.agents/notes/implemented/architecture/2026-09-11-posix-ssh-runtime.md:15`, `:19`, `:21`, `:29`) | keep the model, change the transport | The helper-on-the-remote model and "never replay" are right. Forwarded sockets exist because the alternative needed hand-written flow control (`:41`); a multiplexer library supplies that, and without sockets there is nothing to authenticate | `DH-ADR-SANDBOX-7`, `-8`, `-9`, `-10` |
| `UP-DEC-implemented.architecture.2026-10-26-session-writable-directory-effects` | Extra write roots are effects keyed by live session; not restored from history; running processes keep launch-time grants; an invalid path stops the call (`...session-writable-directory-effects.md:13`, `:15`, `:17`) | keep | It is cordis ownership applied to authority. Replaying a grant could outlive the component that asked for it (`:25`) | `DH-D-SANDBOX-1` |
| `UP-DEC-implemented.architecture.2026-10-29-windows-file-tool-directory-grants` | On Windows only the file tools honour extra roots; processes ignore them, because one identifier per directory set was judged too much access-control machinery (`...windows-file-tool-directory-grants.md:13`, `:19`) | change | The split makes one policy mean two things on one platform. In Go the host builds the token per start, so a token can carry one identifier per root, and the journal carries the bookkeeping upstream wanted to avoid | `DH-ADR-SANDBOX-6` |
| `UP-DEC-implemented.feature.2026-08-08-windows-acl-restricted-token-sandbox` | A `WRITE_RESTRICTED` token with capability identifiers, chosen over AppContainer and mxc because those need read grants on every path (`...windows-acl-restricted-token-sandbox.md:13`, `:31`, `:35`) | keep | The reasoning is about Windows, not JavaScript, and every call is reachable from Go without cgo ([koffi.md](../libs/koffi.md), row 5) | `DH-ADR-SANDBOX-6`, [DH-ADR-SECURITY-2](../upstream/security.md#dh-adr-security-2) |
| `UP-DEC-implemented.feature.2026-09-19-windows-acl-mandatory-integrity-confinement` | Each grant applies an allow entry, a deny of delete-child on containers only, and a Low label, in one call; the token is Low (`...mandatory-integrity-confinement.md:13-17`, `:19`) | keep exactly | It closes a real escape (deleting outside the workspace); each piece is load-bearing and measured | `DH-D-SANDBOX-6` |
| `UP-DEC-proposed.simplification.2026-10-18-parse-ssh-policy-once` | Decode the policy once when the request arrives (`...parse-ssh-policy-once.md:13`) | adopt | The ordinary Go shape: decode at the wire into a typed value | `DH-ADR-SANDBOX-9` |
| `UP-DEC-proposed.simplification.2026-10-18-single-workspace-canonicalization` | Resolve a workspace's real path once per confinement (`...single-workspace-canonicalization.md:13`) | adopt | Two resolutions can disagree if the path changes between them; extra roots are still re-checked every call (`:25`) | `DH-ADR-SANDBOX-9` |
| Seam returns an argv; the consumer spawns and classifies | `docs/subsystems/sandbox.md:128` | change | Every consumer repeated spawn and classification; the incident lived there ([DH-ADR-SECURITY-1](../upstream/security.md#dh-adr-security-1)) | `DH-ADR-SANDBOX-1` |
| Launcher status in exit code and stderr | `packages/sandbox/sandbox-local/src/index.ts:251-260` | drop | In-band, forgeable, cause of postmortem 0004 | `DH-ADR-SANDBOX-2` |
| Enforcement is a per-backend constant; a sole candidate is not probed | `src/index.ts:196-207`, `:558` | change | A constant is an assertion; an unprobed backend is found broken at the first command | `DH-ADR-SANDBOX-3` |
| Fail closed; policy per call; `danger-full-access` bypasses the provider | `docs/subsystems/sandbox.md:87`, `:164`, `:23` | keep | The core of the seam | `DH-ADR-SANDBOX-1` |
| `read-only` grants no temporary area; temp areas differ per backend and are summarised to the model | `packages/sandbox/sandbox-policy/README.md:163` | change | It puts PowerShell into a reduced language mode and makes the model-visible text vague | `DH-ADR-SANDBOX-4` |
| Windows workspace entries and the Low label stand for ever; no cleanup command | `packages/sandbox/sandbox-windows-acl/README.md:128-129`, `:204` | change | A change to the user's directory needs consent, a record and a way back ([DH-ADR-SECURITY-2](../upstream/security.md#dh-adr-security-2)) | `DH-ADR-SANDBOX-6` |
| A bundled PowerShell diagnosis skill the model runs after escalating | `packages/sandbox/sandbox-windows-acl/README.md:78`, `:191` | change | Repairing the user's access-control lists is an owner action, not a model action | `DH-ADR-SANDBOX-6` |
| Trust from the operator's OpenSSH configuration: batch mode, strict host keys, no agent forwarding | `packages/ssh/ssh/src/index.ts:299-303` | keep | Host identity and credentials stay with the tool the operator already manages | `DH-ADR-SANDBOX-7` |
| Helper is a Node single-file executable plus loose native files, installed by hand, pinned by a configured digest that the helper reports itself | `packages/ssh/ssh-helper-runtime/README.md:45-47`; `packages/ssh/ssh/src/helper.ts:115` | change the artefact, keep the pin | Go has one static binary and installing it is one command. The digest detects a wrong or stale file, not a hostile host (`packages/ssh/ssh/README.md:36`) | `DH-ADR-SANDBOX-8` |
| Escalation: strictly wider, one call, with a reason | `packages/sandbox/sandbox/src/escalation.ts:64` | keep | Unchanged by [DH-ADR-SECURITY-5](../upstream/security.md#dh-adr-security-5); the ladder is data in the policy package, the approval belongs to A17 | `DH-D-SANDBOX-1` |

## 5. Tech debt and its resolution

Unit IDs are `UP-DEBT-limit.packages.sandbox.<row>` and `UP-DEBT-limit.packages.ssh.<row>`. "Carried" means
the limit is still true in the Go design; each carried row says why no design removes it and what the user
is told. Carried rows are owned by stage `DH-S-47` of [../implementation-plan.md](../implementation-plan.md#dh-s-47).

| Debt ID | What it is | Evidence | Resolution in Go |
|---|---|---|---|
| `sandbox.1` | The policy expresses file effects only | `packages/sandbox/sandbox/README.md:166` | **Carried.** Network becomes a dimension and the secret directory is masked (`DH-D-SANDBOX-7`). Process visibility, system calls and devices stay outside: no unprivileged mechanism covers them on all three systems, and a dimension one backend enforces would read `partial` everywhere else |
| `sandbox.2` | Containers and remote hosts need whole capabilities replaced | `:167` | **Resolved**: a world is one component that provides filesystem, processes and sandbox together (`DH-ADR-SANDBOX-9`); a container would be another world, and a mismatched trio cannot be mounted |
| `sandbox.3` | Denial is inferred from the command's stderr | `:168` | **Carried; a fact to establish, not yet a platform limit (U-K1-15).** No source in this design states, per backend, that no unprivileged channel reports a denial, so the claim is held as an assumption and settled by three named scenarios: [DH-E-SANDBOX-1](#dh-e-sandbox-1) for bubblewrap and Landlock (Inference: a denied write returns `EACCES` or `EPERM` to the command, and the kernel reports Landlock denials only through the audit subsystem, which needs privileges), [DH-E-SANDBOX-4](#dh-e-sandbox-4) for the Windows token (Inference: `ERROR_ACCESS_DENIED` to the command; object-access auditing needs an administrator) and [DH-E-SANDBOX-3](#dh-e-sandbox-3) for Seatbelt, which writes violations to the unified system log that the user can read. **Decision for macOS:** when a confined command exits with a failure, the macOS backend reads that log once, for the command's process ids and the start's time window, through `/usr/bin/log show` run by the host starter with a deadline and bounded output, and attaches each denied path to the result as `sandbox.denied` (a hint, never a change of exit code or output). The step is kept only if [DH-E-SANDBOX-3](#dh-e-sandbox-3) shows the records name the process; otherwise it is removed and this row becomes a platform limit with that scenario as its source. The seam owns the one stderr classifier and labels its output a hint (`DH-D-SANDBOX-3`); file tools, which run in the host, return typed denials |
| `sandbox.4` | Runner status is in-band | `:169` | **Resolved** by the status channel (`DH-ADR-SANDBOX-2`) |
| `sandbox.5` | One provider per context | `:170` | **Resolved**: the ordered backend chain is inside the seam (`DH-D-SANDBOX-3`), and a world mounted in one agent's scope serves that agent only, so a local and a remote sandbox coexist in one host (`DH-ADR-SANDBOX-9`) |
| `sandbox-local.1` | Windows processes ignore extra roots | `packages/sandbox/sandbox-local/README.md:133` | **Resolved**: one capability identifier per write root (`DH-ADR-SANDBOX-6`) |
| `sandbox-local.2` | Windows is partial: hard links, unconfined reads, trees stamped by another sandbox are unreadable | `:134` | **Carried.** Properties of the token mechanism upstream chose over AppContainer for good reason. Reported on every start as `partial` with the named effects; `require: full` refuses |
| `sandbox-local.3` | Landlock may be partial on older kernels | `:135` | **Carried.** A kernel property. Computed per start from the kernel's version, never assumed (`DH-ADR-SANDBOX-3`) |
| `sandbox-local.4` | Seatbelt depends on deprecated `sandbox-exec` | `:136` | **Carried.** No unprivileged successor is known. Probed at every start-up, so removal by Apple shows as `unavailable` before the first command |
| `sandbox-local.5` | Selection is cached until the plugin reloads | `:137` | **Resolved**: `Reprobe` on command, from the UI, and automatically after a launch failure (`DH-D-SANDBOX-3`) |
| `sandbox-local.6` | A custom runner is trusted and reported `full` | `:138` | **Resolved**: it must run the launch stage, passes the same probe and canary, and can never be `full` (`DH-ADR-SANDBOX-3`) |
| `sandbox-policy.1` | Runtime grants must be renewed by hand after resume or fork | `packages/sandbox/sandbox-policy/README.md:161` | **Resolved**: a grant is an effect in the agent's scope, so building the agent for a resumed or forked session registers it again; authority is still never replayed from the log (`DH-D-SANDBOX-1`) |
| `sandbox-policy.2` | No knob for network or process policy | `:162` | **Carried** for process policy, as `sandbox.1`. The network knob exists |
| `sandbox-policy.3` | Temporary areas cannot be listed truthfully | `:163` | **Resolved**: backends are probed at start, so the resolver lists the real scratch and temp paths (`DH-ADR-SANDBOX-4`) |
| `sandbox-windows-acl.1` | Diagnosis keeps labels, cannot repair a missing right, uses extracted scripts that can be left behind | `packages/sandbox/sandbox-windows-acl/README.md:175` | **Resolved**: `dsh sandbox doctor` and `cleanup` are compiled in; nothing is extracted; cleanup removes journalled labels. A missing `WRITE_DAC` is reported as a precondition only the directory's owner can fix |
| `sandbox-windows-acl.2` | One allow-list per workspace instance | `:182` | **Resolved**: there is no instance; each start's token carries exactly that start's root identifiers |
| `sandbox-windows-acl.3` | Cleanup can leave a temp directory and its entry | `:183` | **Resolved**: the journal records intent before each change; a start-up sweep and `cleanup` remove leftovers |
| `sandbox-windows-acl.4` | Entries on a renamed workspace are invisible | `:184` | **Resolved**: the journal stores the volume and file identity, so `cleanup` finds a renamed directory |
| `sandbox-windows-acl.5` | A directory with no access list is not restored exactly | `:185` | **Resolved**: the journal stores the original descriptor; revoke restores it |
| `sandbox-windows-acl.6` | A confined program cannot give its children named-pipe stdio | `:186` | **Carried.** Windows gives a new named pipe a default descriptor no restricting identifier satisfies; adding the user's own identifier would remove the write restriction. Stated in the model-visible policy text on Windows; inherited and anonymous pipes work |
| `sandbox-windows-acl.7` | The first grant walks the whole tree | `:187` | **Carried.** Windows propagates inheritable entries eagerly. It happens once per directory, at the consent step, with progress and cancellation, not inside a command's time limit |
| `sandbox-windows-acl.8` | No read confinement or network policy on Windows | `:188` | **Carried.** The token intersects writes only. `network: denied` fails closed on Windows ([DH-ADR-SECURITY-10](../upstream/security.md#dh-adr-security-10)) |
| `sandbox-windows-acl.9` | Trees stamped by another sandbox are unreadable | `:189` | **Carried.** A kernel rule. `dsh sandbox doctor` names the objects and offers the repair to the owner |
| `sandbox-windows-acl.10` | No warning for very wide directories or FAT volumes | `:190` | **Resolved**: the preflight before consent refuses a volume without access-control lists and warns on a drive or profile root (`DH-D-SANDBOX-6`, `DH-E-SANDBOX-4`) |
| `sandbox-windows-acl.11` | The diagnosis skill needs the model to escalate | `:191` | **Resolved**: the host classifies a preparation failure itself, read-only; repairs are an owner command |
| `sandbox-windows-acl.12` | PowerShell language mode differs under `read-only` | `:192` | **Resolved**: `read-only` also gets the private scratch, so PowerShell's probe files can be created (`DH-ADR-SANDBOX-4`); checked in `DH-E-SANDBOX-4` |
| `fs-ssh.1` | Reads capped at 8 MiB by the frame size | `packages/ssh/fs-ssh/README.md:69` | **Resolved**: file content travels on its own stream, not in a JSON message |
| `fs-ssh.2` | No remote file watching | `:70` | **Resolved**: the helper runs the shared `watch` package and streams events ([DH-D-SANDBOX-10](#dh-d-sandbox-10), P2) |
| `fs-ssh.3` | Remote file addresses are not download links | `:71` | **Resolved**: the UI never dereferences a file address; a host route reads through the filesystem seam of the session's world (needs A19) |
| `sandbox-ssh.1` | No network or process restriction; remote partial stays partial | `packages/ssh/sandbox-ssh/README.md:67` | **Carried** for process visibility and for partial backends, as `sandbox.1`. The network dimension is enforced by the remote backend |
| `sandbox-ssh.2` | The SSH host and helper are trusted | `:68` | **Carried.** The remote machine executes everything it is sent; no design makes a hostile host safe. Stated at connect and in the documentation |
| `ssh-helper-runtime.1` | No Windows or musl build | `packages/ssh/ssh-helper-runtime/README.md:95` | **Resolved**: the helper is the static `dsh` binary for every released target ([DH-ADR-NATIVESYSTEM-3](../libs/node-addon-system.md#dh-adr-nativesystem-3)), which covers the musl case. The Windows half is not resolved: a Windows remote host is a scope boundary of [DH-ADR-SANDBOX-8](#dh-adr-sandbox-8) |
| `ssh-helper-runtime.2` | No deployment or connection UI | `:96` | **Resolved**: `dsh ssh install` uploads, verifies and pins (`DH-ADR-SANDBOX-8`); status is a seam fact the UI shows |
| `ssh-helper-runtime.3` | The executable is not a general Node | `:97` | **Not applicable**: it exists because the helper embeds Node. The Go helper embeds no interpreter |
| `ssh.1` | No Windows endpoint, provisioning, reconnect or replay | `packages/ssh/ssh/README.md:99` | **Resolved in part.** A Windows *local* end works (no forwarding or control socket is needed); provisioning as above; reconnect for new work. Replay stays forbidden by decision (`DH-ADR-SANDBOX-10`). A Windows *remote* host is not supported: a scope boundary of [DH-ADR-SANDBOX-8](#dh-adr-sandbox-8) (Q3-2) |
| `ssh.2` | Web workspace views assume host files | `:100` | **Resolved** by the same rule as `fs-ssh.3` (needs A19 and the UI areas) |
| `ssh.3` | Stream keys do not stop memory inspection on the remote | `:101` | **Resolved**: there are no stream keys; the helper marks itself non-dumpable and non-attachable (`DH-D-SANDBOX-8`). The remaining trust is `sandbox-ssh.2` |
| `subprocess-ssh.1` | Spill files outlive the connection | `packages/ssh/subprocess-ssh/README.md:75` | **Resolved**: they live in the helper's lock-held runtime directory, removed at close, at lease expiry, or by the next helper's sweep |
| `subprocess-ssh.2` | Callers must drain or close raw streams | `:76` | **Resolved**: a stream belongs to its process handle and closes with it or with its context; an unread stream stalls only its own sender |
| `subprocess-ssh.3` | Remote termination is unconfirmed after a loss | `:77` | **Resolved**: the helper writes a cleanup receipt; after a reconnect the host reads it and logs the outcome. Until then the log says `outcome unknown` (`DH-ADR-SANDBOX-10`) |

## 6. Go design

### 6.1 Shape

Packages under `src/harness/internal/world` ([design.md §4.2](../design.md)).

| Package | Component | Provides | Injects (owner) | Why it exists |
|---|---|---|---|---|
| `sandbox` | none (contract) | Types `Policy`, `Confinement`, `Effect`, `Status`; interface `Service`; the error table; the one function that computes enforcement | nothing | Consumers and backends share one vocabulary |
| `sandbox/policy` | `sandbox.policy` | `policy.Resolver` | session projections (A05), working directory (A06); optional: prompt assembly (A03), approvals and session grants (A17) | One resolver for shells, file tools, terminals and plugins |
| `sandbox/launch` | none (stage) | `dsh sandbox-launch` | nothing; imports only the standard library, `golang.org/x/sys` and go-landlock | The shared launch stage |
| `sandbox/local` | `sandbox.local` | `sandbox.Service` for world `local` | the starter package (A09); optional: interactions (A17), tables (A07) | Probe, choose, prepare and confirm; files per system |
| `sandbox/winacl` | part of `sandbox.local` | Grants, journal, preflight, cleanup, doctor (Windows files only) | tables (A07) | The Windows changes to the user's directories |
| `ssh` | `ssh.world`, one fiber per configured host | `ssh.Connection`; and, in place of the local providers, the filesystem, process and sandbox keys | the host-side starter (A09), codec (A20), contracts (A08, A09) | One remote world, selected as a unit |
| `ssh/helper` | none | `dsh ssh-helper` | the local providers of A08 and A09, and `sandbox/local` | The remote end |

| Interface | Methods | Notes |
|---|---|---|
| `sandbox.Service` | `Describe(ctx) []Status`; `Reprobe(ctx) []Status`; `Prepare(ctx, Policy, argv) (Launch, error)` | `Describe` answers from the last probe. `Prepare` is the only maker of a plan for a `Policy`; it fails closed with an error from the table below, and returns the unconfined plan for `danger-full-access` |
| `sandbox.Launch` | `Wrap(spec)`; `Confirm(ctx) (Confinement, error)`; `Release(ctx) error` | The plan the starter executes: `Wrap` adds the argv prefix, descriptors, token, job and environment. An error from `Confirm` means the command never ran. `sandbox.Trusted(reason)` builds the one other plan, for the host's own children; the starter refuses it when an agent initiated the start, and its tree is the only kind that is not an untrusted tree |
| `policy.Resolver` | `Resolve(ctx, Request) (Policy, error)`; `Escalate(ctx, EscalationRequest) (Policy, error)`; `ForHelper(ctx, HelperRequest) (Policy, error)`; `GrantRoot(ctx, session, dir) (release, error)`; `SetMode(ctx, session, mode) error` | `Policy` has unexported fields and no valid zero value. `Escalate` validates the two escalation fields against the strictly-wider ladder and asks for the approval (A17). `GrantRoot` is an effect on the caller's fiber; the headless runner and the ACP carrier register each `--add-dir` through it as a per-session root. `Resolve` works for a stored session no agent is running, from its log and configuration alone, and a `Policy` exposes its write roots with their sources (`WriteRoots()`). The common Git directory of a worktree that lies inside a workspace root is writable under `workspace-write` like the rest of that root; one that lies elsewhere is a root the worktree component registers with `GrantRoot`. Two ceilings are honoured by `Resolve` and never raised by an escalation: the mode cap delegation sets on a child (A12) and the per-agent ceiling plan mode sets (A13), whose result says where enforcement of it is `partial`. `ForHelper` serves a child that is not an agent's command (an MCP server, LibreOffice, a speech program): read-only, the named write roots, a network setting, a required level; `ForPlugin(ctx, PluginRequest)` is the same call with the plugin's state directory as its one write root. Its `Unconfined` flag is honoured only for a helper kind a compiled-in component registered with a reason (the session's browser), and the result is reported `unconfined` |
| `sandbox.Confinement` | Fields `State` (`Full`, `Partial`, `Unconfined`; zero is invalid), `Backend`, `Missing []Effect`, `Exposed []Effect`, `Scratch` | Built only by the seam |

**How a command starts.** One contract, the same in [execution.md](execution.md) §6.1. A consumer resolves a `Policy`, asks the seam for a `Launch` (`Prepare`) and puts it in `subprocess.Spec.Launch`. The one starter (A09) refuses a start made for an agent without one, and executes the plan before it returns a handle (`DH-ADR-SANDBOX-1`):

1. `Prepare` (the consumer's call): validate roots, prepare host state, create the status channel and the canary.
2. The starter applies `Wrap` and starts the backend with `dsh sandbox-launch` as its command (on Windows: the suspended child itself).
3. The stage confines itself, tries to open the canary for writing, and sends `ready` with backend, governed effects and canary result.
4. The starter calls `Confirm`, which computes the `Confinement` and sends `go`, or closes the channel and returns an error; the starter then calls `Release`.
5. On `go` the stage replaces itself with the command; the channel closes; the handle carries the `Confinement`.

**Backends in Go.** "Network denied" is the setting of [DH-ADR-SECURITY-10](../upstream/security.md#dh-adr-security-10).

| Backend | How | Governs | Cannot govern | Network denied | Harness home: reading `secrets/`, connecting to `run/` | How `dsh tool call` reaches the tool endpoint |
|---|---|---|---|---|---|---|
| bubblewrap | External `bwrap` with upstream's profile, the scratch bound writable, the `dsh` binary bound read-only last, the stage as its command | Writes, deletes, renames, truncation outside the roots; process visibility as a side effect | Reads elsewhere | `--unshare-net`: governed | Both replaced by empty mounts, and so is the user's runtime directory (session bus, keychain service), which also governs `delegated-execution` when the stage finds the bus unreachable | The endpoint's directory is bound read-only at the same path. Inference: connecting to a socket needs no writable mount, and a path socket is not cut off by `--unshare-net` |
| Landlock | The stage restricts itself ([DH-ADR-NATIVESYSTEM-1](../libs/node-addon-system.md#dh-adr-nativesystem-1), [-2](../libs/node-addon-system.md#dh-adr-nativesystem-2)) | The access kinds of `min(kernel, reference)` | Reads; kinds newer than the kernel; a carve-out inside a root; connecting to a path socket; `delegated-execution` where a session bus exists | Partial: the kernel governs TCP only, on newer versions | Neither (allow-list): both are in `Exposed` | No rule is needed, because the kernel's rules do not cover connecting to a path socket (Inference) |
| Seatbelt | External `sandbox-exec -p <profile>`, the stage as its command | Writes outside the roots | Reads elsewhere, processes; `delegated-execution` (a job handed to `launchd`, an Apple Event) unless a profile rule is proven | A profile rule; exact form proven by `DH-E-SANDBOX-5` | Read of `secrets/` and connection to `run/` denied by profile rules | Allowed by upstream's profile; under `network: denied` one rule for its path |
| Windows token | The host builds the token and grants; the starter creates the process suspended in a job with `CreateProcessAsUser` and resumes it ([koffi.md](../libs/koffi.md) §7, question 2). No stage | Writes and deletes outside granted roots | Reads elsewhere; hard-link aliases; trees stamped by another sandbox; `delegated-execution` (task scheduler, out-of-process COM) is unknown | Refused | The harness gives the two directories, which it created, a mandatory label that forbids reading from a lower integrity level; the Low token can then neither read the store nor open the socket (Inference) | A journalled grant on the endpoint's directory for the endpoint identifier every confined token carries. Whether that token can connect to a Unix socket is open; A03's fallback is a loopback port |
| Custom runner (P2) | Operator argv, which must end by running the stage | Whatever the probe and canary show | Unknown, so never `full` | As probed | As probed | As probed |

Every confined policy promises, besides its file and network effects, that the whole harness home is not writable and that `delegated-execution` is governed; a backend that cannot show the second is `partial` and says so. Reading the secret directory and reaching the control socket are not promises, because reads are unconfined on every backend: they are reported per start in `Exposed`, and the design does not rest on them ([DH-ADR-HOST-12](host-telemetry.md#dh-adr-host-12)). Under `danger-full-access` nothing in this table applies and the handle says `unconfined`. On a remote world the table applies to the remote machine and to the helper's runtime directory.

**When confinement is unavailable or partial.**

| Situation | What happens | Error or fact |
|---|---|---|
| No backend passes the start-up probe | Confined starts are refused; the UI shows it before the first command; `danger-full-access` still runs and is reported `unconfined` | `sandbox/unavailable` |
| The first backend fails its probe | The next in the chain is used and named on every result | `Confinement.Backend` |
| The backend or stage dies before `ready`, or the canary could be opened for writing | The command never ran; the seam re-probes once | `sandbox/launch-failed` |
| Governed effects do not cover the policy, and the policy requires `full` | The host closes the channel; the stage exits | `sandbox/enforcement-insufficient` |
| Governed effects do not cover the policy, and partial is accepted | The command runs; the missing effects travel on the handle, the result and the log | `Confinement{Partial, Missing}` |
| `network: denied` on a backend that cannot restrict sockets at all (Windows) | Refused | `sandbox/network-unenforceable` |
| The `dsh` binary or the canary would be writable under the policy and the backend cannot carve it out | Refused | `sandbox/launcher-writable` |
| A write root contains the harness home, or lies inside it (plugin state and scratch excepted) | bubblewrap and Seatbelt keep the home read-only inside the root; Landlock and Windows refuse the start | `sandbox/home-writable` |
| The stage can reach a same-user service that runs programs on request | The command runs unless the policy requires `full`; the effect is named on the handle, the result and `dsh sandbox status` | `Confinement{Partial, Missing: delegated-execution}` |
| An extra root is missing, not a directory or a symlink | Refused, naming the root | `sandbox/invalid-root` |
| Windows preparation fails or consent is refused | Journalled changes of this attempt are rolled back; nothing runs | `sandbox/preparation-failed`, `sandbox/consent-denied` |
| Confined, but the program cannot be started | Reported as the command's own failure to start | `sandbox/exec-failed` |

**SSH in Go.** `ssh.world` starts the operator's `ssh` once, with upstream's options minus connection
sharing, and runs `dsh ssh-helper` as the remote command. A multiplexer session runs over that process's
standard input and output. Stream 1 carries JSON-RPC requests (A20's codec); each program stream, file body,
watch feed and terminal is its own stream with its own window. There is no forwarded socket, no listener on
either machine and no stream key (`DH-ADR-SANDBOX-7`). The helper serves the remote world with the same local providers and seam,
so a remote start goes through the same launch steps on the remote machine and its `Confinement` comes back as data in the reply.

### 6.2 Contracts

| Event | Domain and form ([design.md §4.6](../design.md)) | Payload | Replaces |
|---|---|---|---|
| `sandbox/mode` | Session event, durable, log-only | The whole new value: mode and network setting | Upstream `sandbox/mode` (`packages/sandbox/sandbox-policy/src/session-mode.ts:33`) |
| `sandbox.ModeChanging` | Capability event, ordered: the first error refuses the change, before anything is appended | Session, current and requested mode | The terminal backend's interception of the framework's own dispatch (`packages/terminal/terminal-bash/src/index.ts:49-59`) |
| `sandbox.StatusChanged`, `sandbox.RootsChanged` | Capability events, notify | Per backend: usable, governed effects, exposure, reason. Per session: a registered write root or a directory grant came or went (A11 ends a workflow run on it) | Nothing: upstream has no status before the first command and no notice of a root that goes away |
| `ssh.StateChanged` | Capability event, notify | World name; `connecting`, `ready`, `lost`, `closed`; reason | Nothing |

The confinement of one start is a field of the process handle and of the tool result that A09 and A03 log; this area supplies the type, not a separate event. Config decoding is strict ([DH-ADR-SECURITY-11](../upstream/security.md#dh-adr-security-11)).

| Config struct | Fields and defaults | Validation | Replaces |
|---|---|---|---|
| `policy.Config` | `DefaultMode` (`read-only`), `FallbackRoot` (the start directory), `ExtraWriteRoots` (none), `Network` (`allowed`), `Require` (`partial-accepted`) | Absolute paths; a known mode; roots re-checked on every call | `UP-CFG-dsh-sandbox-policy` |
| `local.Config` | `ProbeTimeout` (5 s), `LaunchTimeout` (5 s), `Backends` (the platform chain; may only remove entries), `CustomRunner` (none) | Positive durations; a custom runner is an absolute path | `UP-CFG-dsh-sandbox-local`. `runnerFailureSignatures` has no successor: status is out of band |
| `ssh.Config` | `Name`, `Host` (OpenSSH alias), `HelperPath`, `HelperDigest`, `Workspace`, `RequestTimeout` (30 s), `MaxMessageBytes` (1 MiB), `MaxPending` (128), `Lease` (30 s; 3 s to 10 min), `Reconnect` (3 attempts) | Absolute remote paths without control characters; the digest is 64 hex digits | `UP-CFG-dsh-ssh`. `launch` has no successor: there is one helper kind |

| Contract | Go form | Replaces |
|---|---|---|
| Services | `sandbox.Key` with `sandbox.Service`; `policy.Key` with `policy.Resolver`; `ssh.Key` with `ssh.Connection` (state, `Describe`, and the three providers of its world) | `UP-SVC-sandbox`, `UP-SVC-sandboxPolicy`, `UP-SVC-ssh` |
| Persisted data | The `sandbox/mode` event. On Windows a grant journal table (path, volume and file identity, identifier, the three edits, original descriptor, state `intent` or `applied`, owner kind) behind A07's typed tables. The pinned helper digest in the writable config layer. On the remote host: the helper's runtime directory and a bounded set of receipt files | Upstream persists only `sandbox/mode` |
| Model-visible | No tool. One prompt-context section: mode, workspace, extra roots with their source, the real scratch and temp paths, the network setting, and on Windows the named-pipe limit. The denial marker and escalation hint keep upstream's wording | The `sandbox:policy` contribution and `packages/sandbox/sandbox/src/escalation.ts:108` |
| Wire | JSON-RPC 2.0 on stream 1: `hello`, `heartbeat`, `close`, `receipt`, the filesystem and process methods of A08's and A09's contracts, `watch`. A protocol number is checked at `hello`; both ends must match exactly | Upstream's private frame protocol, version 3 (`packages/ssh/ssh/src/protocol.ts:10`) |

**What other areas get.** A09: the `Launch` plan type, the mode-change event, the escalation call, and a remote provider under the
same process key (its host key stays local). A22 (plugins): `Resolver.ForPlugin`, which gives read-only plus the plugin's state directory
as its one write root, the manifest's network setting and an optional `require: full`; and the `Confinement` to show at install. A15, A16 and X4: `ForHelper`. A08: the
policy value, the write roots from one function for the fence, the escalation call and marker text, and a remote provider that passes its
conformance suite. A03: per backend, the rule that lets a confined command reach the endpoint socket, whose directory must lie outside `<home>/run`, `<home>/secrets` and every write root. A18 and A17: the home is never writable, and `run/` and `secrets/` are denied or reported as the table says. A06: the write boundary follows the session's current working directory, as upstream.

### 6.3 Ownership, failure, retry, rollback and cleanup

| Resource | Class | Owner | Released when |
|---|---|---|---|
| Probe results | Created | `sandbox.local` fiber | Fiber unload; replaced on re-probe |
| A status channel, a `Launch` | Created | The starter's call | `Confirm` fails, or the process is handed over |
| The scratch directory (and on Windows its grant) | Created | The session's agent fiber, through the seam | Session end; a start-up sweep after a crash |
| A registered write root | Access | The registering component's fiber | That fiber unloads; running processes keep what they started with |
| Windows entries on a write root | Adopted | The user owns the directory; the journal owns the entries | Only by `dsh sandbox cleanup`; never silently |
| The Low label where another grant remains | Shared | Nobody exclusively | Left in place and reported as uncertain |
| The `ssh` child process and the mux session | Created | `ssh.world` fiber | Unload: close request, close input, terminate, kill, each bounded |
| The SSH host, its `known_hosts` entry, the operator's keys | Shared | The operator | Never touched |
| Remote processes, the helper's runtime directory | Created by the helper | The helper | Close, end of input, signal or lease expiry |
| The installed remote `dsh` | Created by `dsh ssh install` | The operator after install | `dsh ssh uninstall`; never automatically |

- **A load fails.** `sandbox.local` with no usable backend still loads and reports `unavailable`: a failed fiber would hide the status. `ssh.world` with no `ssh` program loads and reports `ssh/unavailable`; a failed `hello` leaves it `Failed` until retried.
- **A start fails half-way.** Before `go` nothing outside the stage has changed. If the host dies before `go`, the stage reads end of file and exits; no command runs unowned.
- **Windows preparation fails half-way.** Each change is journalled as `intent` first. On failure the attempt's changes are reverted in reverse order and the rows removed; a row that cannot be reverted stays `intent` for the sweep and is reported.
- **Retry.** A launch failure triggers one re-probe, never an automatic second start of the same command. SSH reconnects for new work under a budget.
- **SSH connection lost.** Pending reads fail with `ssh/lost`. Pending changes and starts fail with `ssh/lost` and `outcome unknown`, which the tool pipeline logs ([design.md §5](../design.md)). Nothing is replayed.
- **Uncertain.** Whether a lost remote change happened, until a receipt is read; what a Low-integrity process of another product did inside a labelled workspace.

## 7. Decisions

<a id="dh-adr-sandbox-1"></a>
### DH-ADR-SANDBOX-1 The starter applies confinement; a policy is mandatory

- **Decision.** Refines [DH-ADR-SECURITY-1](../upstream/security.md#dh-adr-security-1). The one process starter (A09) requires a `sandbox.Launch` in every start request made for an agent and executes it; it never decides confinement itself. Only the seam can build a `Launch`, and only from a `Policy`, which has unexported fields and no valid zero value and which only `policy.Resolver` can build. `danger-full-access` yields an unconfined `Launch` from the same call, so the seam is always asked and the bypass is recorded.
- **Why.** "Runs under the policy or does not run" should be a property of the types. A caller that forgets the sandbox must fail to compile or fail the start, not run free.
- **Tradeoff.** The starter depends on this area's plan type, and probes need the starter's host-only path that only the seam may use; an import test guards it.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-11](../design.md#dh-o-11).
- **Verified by.** `DH-T-SANDBOX-2`, `DH-T-SANDBOX-14`, `DH-E-SANDBOX-1`.

<a id="dh-adr-sandbox-2"></a>
### DH-ADR-SANDBOX-2 Two-phase launch: the stage proves, the host accepts, then the command starts

- **Decision.** Refines [DH-ADR-SECURITY-3](../upstream/security.md#dh-adr-security-3). The status channel is a descriptor pair marked close-on-exec. The stage confines itself, tries to open the canary for writing, sends `ready` with the backend, the governed effects and the canary result, and waits. It replaces itself with the command only after `go`. End of file in either direction ends the stage with no command run.
- **Why.** A one-way record cannot stop a start the host would refuse (a `partial` under `require: full`), and leaves a command running if the host died first. The canary turns "the profile was accepted" into "a write was refused".
- **Tradeoff.** One more round trip per start. It assumes bubblewrap and `sandbox-exec` pass an inherited descriptor to their command: unverified, proven by `DH-E-SANDBOX-1` and `-3`. If the roots cover every place a canary could live, the start is refused.
- **Serves.** [DH-O-7](../design.md#dh-o-7).
- **Verified by.** `DH-T-SANDBOX-4`, `DH-E-SANDBOX-1`, `DH-E-SANDBOX-2`.

<a id="dh-adr-sandbox-3"></a>
### DH-ADR-SANDBOX-3 Enforcement is computed from governed effects, never asserted

- **Decision.** No backend has an "enforcement" field. One function compares the effects the policy promises with the effects the evidence shows are governed. Evidence is the start-up probe (every backend, also a sole candidate) and the stage's `ready` for this start; where they disagree the weaker wins. `full` needs every promised effect governed; anything unknown counts as not governed. A custom runner is never `full`. A policy may `require: full`; then partial is a refusal, enforced by the seam.
- **Why.** This is how the design makes "a partial sandbox reported as full" impossible: there is no place to write the claim. It also removes the constant table and the unprobed sole candidate of `packages/sandbox/sandbox-local/src/index.ts:196-207`.
- **Tradeoff.** The effect list is maintained by hand and must be reviewed when a backend or kernel changes. Probing every backend costs start-up time, bounded by a time limit and run off the critical path. Scope boundary of this record (`sandbox.1`, `sandbox-policy.2`, `bash-sandbox.1`): a policy promises file effects, the network setting, the protection of the harness home and `delegated-execution`, and nothing about which processes a command may see or signal, which system calls it may make, or which devices it may open. This is a product choice, not a platform limit: bubblewrap could hide other processes on Linux, but a policy dimension that only one backend enforces would read `partial` on every other backend for every start, and the effects the policy does promise are what stops a command from changing the user's files or taking over the harness.
- **Serves.** [DH-O-7](../design.md#dh-o-7).
- **Verified by.** `DH-T-SANDBOX-3`, `DH-T-SANDBOX-7`, `DH-E-SANDBOX-1`, `DH-E-SANDBOX-4`.

<a id="dh-adr-sandbox-4"></a>
### DH-ADR-SANDBOX-4 One private scratch directory in both confined modes; real temp areas are listed

- **Decision.** Every confined start gets the session's scratch directory as a write root and as `TMPDIR`, `TMP` and `TEMP`, also under `read-only`. `read-only` now means "no write to anything the user owns". Under `workspace-write`, Landlock and Seatbelt keep upstream's shared temp areas for programs that ignore `TMPDIR`. The resolver lists the actual paths in the model-visible section.
- **Why.** Many tools need temporary files to read anything; upstream's Windows `read-only` pushes PowerShell into a reduced language mode for that reason (`packages/sandbox/sandbox-windows-acl/README.md:192`).
- **Tradeoff.** `read-only` allows a write the user did not name, into a directory the harness owns and removes. The shared `/tmp` under Landlock stays writable. The scratch is expected to keep PowerShell in its full language mode; the design relies on neither mode, because confinement is the token's and a language mode is never counted as a control ([execution.md](execution.md) §6.2).
- **Serves.** [DH-O-7](../design.md#dh-o-7).
- **Verified by.** `DH-T-SANDBOX-1`, `DH-T-SANDBOX-6`, `DH-E-SANDBOX-4`.

<a id="dh-adr-sandbox-5"></a>
### DH-ADR-SANDBOX-5 The launcher and the harness home are never writable by what they confine; the tool endpoint is the one thing granted

- **Decision.** (a) `Prepare` refuses a policy under which the running `dsh` binary, the remote helper or any part of the harness home would be writable, unless the backend can keep it read-only inside the root (bubblewrap, Seatbelt). A plugin's state directory and the session scratch are the only parts of the home that are ever a write root. (b) Each backend denies what it can of reading `<home>/secrets` and connecting to `<home>/run`, as the table in §6.1 says, and reports the rest in `Exposed`. (c) Each backend adds exactly one rule that lets the command connect to the host's tool endpoint, a Unix socket in a private directory outside `run/` (area A03; [design.md §4.4](../design.md), item 5). Where a backend cannot, `dsh tool call` is `unavailable` there and the policy is not loosened. (d) The start-up probe tries all three from inside each backend (write a canary in the home, read a canary in `secrets/`, connect to a probe socket in `run/`) and tries to reach the user's service manager, so that every cell of the table is evidence and not a claim.
- **Why.** A command that can replace the launcher runs unconfined on the next start; upstream states this as an installation rule (`packages/ssh/ssh/README.md:36`). A command that can write the home can add a browser session, a plugin entry or a looser default, which is why owner authority is made to need such a write ([DH-ADR-HOST-12](host-telemetry.md#dh-adr-host-12)). The control socket and the secret store are denied as a second line, not as the boundary: on Landlock neither can be.
- **Tradeoff.** A developer who builds `dsh` inside the workspace it edits, or keeps the home inside a workspace, cannot use Landlock or the Windows backend there. Hiding the user's runtime directory under bubblewrap breaks a command that needs the session bus (a desktop notification, a keychain-backed credential helper); such a command needs an escalation.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-6](../design.md#dh-o-6).
- **Verified by.** `DH-T-SANDBOX-13`, `DH-E-SANDBOX-1`, `DH-E-SANDBOX-5`.

<a id="dh-adr-sandbox-6"></a>
### DH-ADR-SANDBOX-6 Windows: one identifier per write root, a journal, consent, and owner commands

- **Decision.** Keep the restricted-token mechanism and the three-edit grant. Change: (a) every write root and the scratch has its own identifier, derived from the canonical path with 160 bits of SHA-256 (upstream keeps 60, `packages/sandbox/sandbox-windows-acl/src/workspace-sid.ts:30`), and a start's token carries exactly its roots; (b) every change to an access-control list is journalled before it is made, with the original descriptor; (c) the first change to a directory needs the owner's consent, after a preflight (filesystem type, width, needed rights); with nobody to ask the start is refused, and `dsh sandbox prepare` lets an operator consent ahead; (d) `dsh sandbox cleanup` removes journalled entries, leaves a label another grant still needs and reports it; (e) `dsh sandbox doctor` replaces the model-run skill.
- **Why.** It removes the split between file tools and processes, the invisible residue and the unconditional standing change. Consent to prepare a directory is the owner changing their own machine; it is not an approval rule for the model, so [DH-ADR-16](../design.md#dh-adr-16) is not touched.
- **Tradeoff.** More entries on more directories; an extra root pays the tree walk once. Headless Windows runs need `prepare` first. Whether Windows accepts the wider identifier form in a token is unverified (`DH-E-SANDBOX-4`).
- **Serves.** [DH-O-7](../design.md#dh-o-7).
- **Verified by.** `DH-T-SANDBOX-8`, `DH-T-SANDBOX-9`, `DH-E-SANDBOX-4`.

<a id="dh-adr-sandbox-7"></a>
### DH-ADR-SANDBOX-7 SSH: the operator's OpenSSH, one channel, a multiplexer, no stream keys

- **What was checked.** Go's own documentation, Go 1.27.4, 2026-11-18. `go doc -all crypto/tls` mentions PSK in three lines, all about session-ticket resumption. `go doc crypto/tls.Config` lists no field or callback that accepts a caller's key or identity. `crypto/tls/cipher_suites.go` contains no suite whose name includes `PSK`. The only PSK code is unexported and requires a stored session (`crypto/tls/handshake_client_tls13.go:450`). **Finding: the standard library has no pre-shared-key mode for a caller-supplied key**, so upstream's stream authentication cannot be ported.
- **Decision.** Do not replace the key; remove what it protected. The host runs the operator's `ssh` program once, with `BatchMode=yes`, `StrictHostKeyChecking=yes`, `ForwardAgent=no`, `ClearAllForwardings=yes` and no connection sharing. A multiplexer runs over its standard streams. Every byte is inside the one channel OpenSSH authenticated; no socket exists for another process to connect to or replace. Host-key trust is OpenSSH's: the harness never adds, accepts or prompts for a key.
- **Why.** The key answered a threat created by forwarded sockets in writable directories (`.agents/notes/implemented/architecture/2026-09-11-posix-ssh-runtime.md:21`). Upstream chose sockets to avoid writing per-stream flow control (`:41`); a library provides it. Inference, not verified here: the Windows OpenSSH client supports neither connection sharing nor Unix-socket forwarding, so this transport is also what lets a Windows machine be the local end.
- **Library.** `DH-LIB-SANDBOXMUX`: `github.com/hashicorp/yamux` v0.1.2 (2024-09-06), MPL-2.0, no dependencies; verified with `go list -m -json`, `go mod download -json` and by reading `LICENSE`, `mux.go` and `stream.go` in the module cache: it runs over any `io.ReadWriteCloser`, each stream has its own 256 KiB window, and `Close` half-closes. Alternative verified the same way: `github.com/xtaci/smux` v1.5.60 (2026-11-12), MIT, one dependency.
- **Alternatives rejected.** Pinned one-time certificates ([DH-ADR-NODENET-2](../libs/node-net-builtins.md#dh-adr-nodenet-2)): sound, but keeps the sockets and a listener per stream. An in-process SSH client (`golang.org/x/crypto/ssh` v0.59.0, BSD-3-Clause, verified): it would not read the operator's OpenSSH configuration, jump hosts or hardware keys.
- **Tradeoff.** All streams share one SSH channel window, so fairness depends on the multiplexer. The licence is file-level copyleft; choosing it is a recommended default the owner has not confirmed (§10).
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7).
- **Verified by.** `DH-T-SANDBOX-10`, `DH-E-SANDBOX-6`.

<a id="dh-adr-sandbox-8"></a>
### DH-ADR-SANDBOX-8 The remote helper is the same `dsh` binary, installed by a command and pinned by digest

- **Decision.** `dsh ssh-helper` is a hidden mode of the one binary ([DH-ADR-NATIVESYSTEM-4](../libs/node-addon-system.md#dh-adr-nativesystem-4)). `dsh ssh install <name>` copies a `dsh` binary for the remote platform over the same `ssh` program, verifies it by reading it back, and writes path and SHA-256 to the writable config layer. The binary is the local one when platforms match; otherwise the operator passes a release asset. `hello` must report the same protocol number and digest. The harness downloads nothing ([DH-ADR-13](../design.md#dh-adr-13)).
- **Why.** Upstream ships a Node executable, loose native files and a manifest, built on four runners (`packages/ssh/ssh-helper-runtime/README.md:36-45`). None of that exists for a static Go binary.
- **Tradeoff.** The digest is self-reported: it catches a stale or wrong file, not a hostile host. Host and helper must be upgraded together. **Scope boundary of this record: the remote host is Linux or macOS** (Recommended default, not confirmed by the owner; units `ssh.1` and `ssh-helper-runtime.1`, audit finding Q3-2). A Windows remote would need its own process-tree owner, restricted-token preparation and journal on the remote machine, a pseudoconsole through the helper and a Windows quoting of the remote command line; none of that is designed, and `dsh ssh install` refuses a remote whose `uname` reports Windows (or which has no `uname`) with `ssh/unsupported-remote`. A Windows *local* end is supported.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-14](../design.md#dh-o-14).
- **Verified by.** `DH-T-SANDBOX-11`, `DH-E-SANDBOX-6`.

<a id="dh-adr-sandbox-9"></a>
### DH-ADR-SANDBOX-9 A world is one component; a policy is decoded once and resolved where the files are

- **Decision.** `ssh.world` provides the filesystem, process and sandbox keys together, in place of the local providers, for the agents in whose scope it is mounted; the host-side process key of A09 stays local, for children that must run beside the host. A policy crosses the wire as one typed message decoded once. The world that owns the files resolves each root's real path once per start and re-checks extra roots every time.
- **Why.** Upstream mounts three packages by hand and documents that they must match (`packages/sandbox/sandbox/README.md:84`). Adopts both proposed simplifications (§4).
- **Tradeoff.** A capability cannot be mixed across worlds, for example local files with a remote shell. Inference: whether two agents of one host may use different worlds rests on per-agent scope ([DH-ADR-ARCH-7](../upstream/architecture.md#dh-adr-arch-7)); the composition areas confirm.
- **Serves.** [DH-O-10](../design.md#dh-o-10).
- **Verified by.** `DH-T-SANDBOX-5`, `DH-T-SANDBOX-12`, `DH-E-SANDBOX-6`.

<a id="dh-adr-sandbox-10"></a>
### DH-ADR-SANDBOX-10 A lost connection means "outcome unknown"; reconnect serves new work; receipts close the gap

- **Decision.** Keep: no replay, a heartbeat lease, cleanup by the helper. Change: the fiber reconnects under a budget and starts a fresh helper for new operations; each helper writes a receipt when its cleanup ends, and the next connection reads the receipts of instances it lost and logs them.
- **Why.** Replaying a change that may have happened is how data is lost. A receipt lets "unknown" become a fact when one is available.
- **Tradeoff.** If the host never reconnects, the outcome stays unknown. Receipts are written by the trusted remote host and are bounded in number.
- **Serves.** [DH-O-5](../design.md#dh-o-5), [DH-O-10](../design.md#dh-o-10).
- **Verified by.** `DH-T-SANDBOX-11`, `DH-E-SANDBOX-6`.

<a id="dh-adr-sandbox-11"></a>
### DH-ADR-SANDBOX-11 The confinement is not audited, says so wherever it is shown, and is reviewed before a stable release

- **Decision.** Recommended default, not confirmed by the owner (answers `UP-DEBT-found.Q3.1`, with [DH-ADR-SECURITY-12](../upstream/security.md#dh-adr-security-12), which states the product-wide security review; this record states what the sandbox area shows and checks; upstream: `SAFETY.md:7`, `:13`, `:15`). (a) The sandbox, approvals and permission controls reduce risk and are not a guarantee of isolation; this design has had no security review, as upstream had none. (b) The statement is shown, in those words, by `dsh sandbox status`, on the permissions page, in the first-run notice and in the documentation website's security page, beside the per-machine gaps of [DH-ADR-HOST-12](host-telemetry.md#dh-adr-host-12). (c) An independent review of the confinement, the owner-authority path and the tool endpoint is a release criterion: the first release the owner declares stable (the one that ends `v0` of the modules, [DH-ADR-26](../design.md#dh-adr-26)) needs a review report that names the build it read, and every finding marked fixed or accepted by the owner. Until then each release note repeats (a).
- **Why.** The limit is true of the Go design and cannot be designed away: confinement is built on operating-system mechanisms whose combination nobody has attacked. Stating it once in a document would let the product imply more than it has.
- **Tradeoff.** One more gate before a stable release, which needs an outside reviewer and time nobody has scheduled. The statement may read as weak to a user; it is the true one.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-14](../design.md#dh-o-14).
- **Verified by.** `DH-T-SANDBOX-17` (the statement is present, verbatim, in each of the four places), and the review report at the stable release, which no test can supply.

## 8. Deliverables

<a id="dh-d-sandbox-1"></a>
### DH-D-SANDBOX-1 One policy resolver for every enforcing capability

- **Result and why it matters.** Shells, file tools, terminals and plugins get their mode, write roots, scratch and network setting from one place, so they cannot drift.
- **How it is implemented.** A component that folds `sandbox/mode` with a registered projection, applies the precedence of §3.2, collects roots from the session's current working directory, configuration, registrations made through agent scope, directory-scoped session grants and (for plugins) the state directory, and re-validates every root per call. `SetMode` dispatches `sandbox.ModeChanging` and appends only if no listener refuses. `Escalate` owns the strictly-wider ladder as data, validates the two fields and asks for a one-call approval. The component contributes the prompt-context section.
- **Components and files.** `internal/world/sandbox`, `internal/world/sandbox/policy`.
- **Depends on.** [DH-D-VIEW-1](session-views.md#dh-d-view-1) (fold registration); [DH-D-VIEW-10](session-views.md#dh-d-view-10) (the session working directory); [DH-D-HUMAN-2](interaction-credentials.md#dh-d-human-2) (the approval for an escalation); [DH-D-TOOLS-5](tools.md#dh-d-tools-5) (registering the prompt-context section). Directory grants are read through `Grants.Roots` when the grants component is mounted; that component depends on this resolver, not the reverse. Runtime: none.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-11](../design.md#dh-o-11). **Priority.** P0.
- **Done when.** `DH-T-SANDBOX-1`, `DH-T-SANDBOX-2`, `DH-T-SANDBOX-5` and `DH-T-SANDBOX-15` pass and [DH-E-SANDBOX-7](#dh-e-sandbox-7) has passed.

<a id="dh-d-sandbox-2"></a>
### DH-D-SANDBOX-2 The sandbox launch stage (shared piece)

- **Result and why it matters.** `dsh sandbox-launch` confines itself, proves it and becomes the command. It is the only code that runs between "start" and "confined".
- **How it is implemented.** Recognised in `main` before anything else. The sequence of [DH-ADR-NATIVESYSTEM-1](../libs/node-addon-system.md#dh-adr-nativesystem-1) for Landlock; no confinement step of its own under bubblewrap, Seatbelt and a custom runner; then the canary and the two-phase exchange of `DH-ADR-SANDBOX-2`. The host half (create the pair, read with a time limit, decide, send `go`) lives in `sandbox/local`.
- **Components and files.** `internal/world/sandbox/launch`, `cmd/dsh` dispatch.
- **Depends on.** [DH-LIB-NATIVESYSTEM](../libs/node-addon-system.md), [DH-LIB-GOLANDLOCK](../libs/node-addon-system.md); `DH-D-SANDBOX-1` (the contract types); [DH-D-CLI-1](app-cli.md#dh-d-cli-1) (the command grammar, which reserves the hidden word). Runtime: a Linux kernel with Landlock for that backend.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-14](../design.md#dh-o-14). **Priority.** P0.
- **Done when.** `DH-T-SANDBOX-4` passes and [DH-E-SANDBOX-1](#dh-e-sandbox-1) and [DH-E-SANDBOX-2](#dh-e-sandbox-2) have passed.

<a id="dh-d-sandbox-3"></a>
### DH-D-SANDBOX-3 The sandbox seam: probe, select, prepare, confirm, report

- **Result and why it matters.** A start either returns a handle with a truthful `Confinement` or one error from the table in §6.1. Operators see backend status before the first command.
- **How it is implemented.** `sandbox.local` probes every backend of the platform chain at load with the same canary test, publishes `Describe`, and re-probes on request and after a launch failure. `Prepare` validates roots, refuses a writable launcher, and builds the backend's `Launch`. The enforcement function and the stderr denial hint live here and nowhere else.
- **Components and files.** `internal/world/sandbox`, `internal/world/sandbox/local`.
- **Depends on.** `DH-D-SANDBOX-1`, `DH-D-SANDBOX-2`, [DH-D-CORDIS-4](../libs/cordis.md#dh-d-cordis-4) (contained notify, for the status event); [DH-D-EXEC-4](execution.md#dh-d-exec-4) (the starter requires a `Launch` and calls `Wrap`, `Confirm` and `Release`); [DH-D-EXEC-1](execution.md#dh-d-exec-1) (the host-only start path for probes).
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-11](../design.md#dh-o-11). **Priority.** P0.
- **Done when.** `DH-T-SANDBOX-2`, `-3`, `-5`, `-7`, `-13`, `-14` and `-17` pass and [DH-E-SANDBOX-2](#dh-e-sandbox-2) has passed.

<a id="dh-d-sandbox-4"></a>
### DH-D-SANDBOX-4 Linux backends: bubblewrap, then Landlock

- **Result and why it matters.** Confined commands on Linux, with or without `bwrap`.
- **How it is implemented.** Pure functions turn a policy into bubblewrap arguments and into Landlock grants (§6.1); `bwrap` is found once by absolute path.
- **Components and files.** `internal/world/sandbox/local` (Linux files).
- **Depends on.** `DH-D-SANDBOX-2`, `DH-D-SANDBOX-3`. Runtime: `bwrap` with unprivileged namespaces, or a kernel with Landlock.
- **Serves.** [DH-O-7](../design.md#dh-o-7). **Priority.** P0.
- **Done when.** `DH-T-SANDBOX-6` passes and [DH-E-SANDBOX-1](#dh-e-sandbox-1) has passed.

<a id="dh-d-sandbox-5"></a>
### DH-D-SANDBOX-5 macOS backend: Seatbelt

- **Result and why it matters.** Confined commands on macOS, and an honest `unavailable` if Apple removes `sandbox-exec`.
- **How it is implemented.** A pure function builds the profile text with quoted, canonical roots; `/usr/bin/sandbox-exec` runs the stage.
- **Components and files.** `internal/world/sandbox/local` (macOS files).
- **Depends on.** `DH-D-SANDBOX-2`, `DH-D-SANDBOX-3`. Runtime: `sandbox-exec`.
- **Serves.** [DH-O-7](../design.md#dh-o-7). **Priority.** P1.
- **Done when.** `DH-T-SANDBOX-6` passes and [DH-E-SANDBOX-3](#dh-e-sandbox-3) has passed.

<a id="dh-d-sandbox-6"></a>
### DH-D-SANDBOX-6 Windows backend: restricted token, journalled grants, consent, cleanup and doctor

- **Result and why it matters.** Confined commands on Windows that honour every write root, and no change to a user's directory without a record and a way back.
- **How it is implemented.** `DH-ADR-SANDBOX-6`. Typed calls from `golang.org/x/sys` with `CreateRestrictedToken` bound once ([DH-ADR-KOFFI-1](../libs/koffi.md#dh-adr-koffi-1)); grants under a per-directory lock; the `Launch` hands token, job and environment to the starter.
- **Components and files.** `internal/world/sandbox/winacl`, `internal/world/sandbox/local` (Windows files), `cmd/dsh` commands `sandbox prepare`, `cleanup`, `doctor`.
- **Depends on.** `DH-D-SANDBOX-3`, [DH-LIB-KOFFI](../libs/koffi.md); [DH-D-EXEC-2](execution.md#dh-d-exec-2) (suspended start in a job); [DH-D-STORAGE-1](storage.md#dh-d-storage-1) (a journal table); [DH-D-HUMAN-2](interaction-credentials.md#dh-d-human-2) (the `consent` kind); [DH-D-CLI-1](app-cli.md#dh-d-cli-1) (the command tree the three words join; the admin command set of app-cli.md lists them and depends on this). Runtime: NTFS or ReFS.
- **Serves.** [DH-O-7](../design.md#dh-o-7). **Priority.** P1.
- **Done when.** `DH-T-SANDBOX-8` and `DH-T-SANDBOX-9` pass and [DH-E-SANDBOX-4](#dh-e-sandbox-4) has passed. That scenario changes access-control lists of the user's directories and runs their cleanup, so it runs in this deliverable's build stage, not in a later acceptance batch.

<a id="dh-d-sandbox-7"></a>
### DH-D-SANDBOX-7 Network denial, home protection and the tool endpoint inside each backend

- **Result and why it matters.** `network: denied` is enforced, partial or refused as the table in §6.1 says. A confined command cannot write the harness home on any backend, and cannot read the secret directory, open the control socket or hand work to a same-user service where the backend can stop it; what it can still do is reported. A script can still call tools.
- **How it is implemented.** `DH-ADR-SANDBOX-5`: additions to each profile builder (the home write refusal, the two masks, the hidden runtime directory, the endpoint rule, the Windows directory label), each expressed as an effect and checked by the start-up probe, so that enforcement and exposure are computed, not claimed.
- **Components and files.** `internal/world/sandbox/local`.
- **Depends on.** `DH-D-SANDBOX-4`, `DH-D-SANDBOX-5`, `DH-D-SANDBOX-6`; [DH-D-STORAGE-2](storage.md#dh-d-storage-2) (the home layout: `secrets/`, `run/`). The one directory a backend admits is `tools.Service.EndpointDir()`, which arrives as a field of the request, so the tool endpoint of tools.md depends on this deliverable and not the reverse.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-6](../design.md#dh-o-6). **Priority.** P1.
- **Done when.** `DH-T-SANDBOX-6`, `DH-T-SANDBOX-9` and `DH-T-SANDBOX-15` pass and [DH-E-SANDBOX-5](#dh-e-sandbox-5) has passed.

<a id="dh-d-sandbox-8"></a>
### DH-D-SANDBOX-8 The SSH connection, the helper mode and `dsh ssh install`

- **Result and why it matters.** One owned connection to a remote host with verified helper identity, bounded requests, a lease, and honest loss handling.
- **How it is implemented.** `DH-ADR-SANDBOX-7`, `-8` and `-10`. The helper marks itself non-dumpable (Linux) and refuses debugger attachment (macOS), keeps its runtime directory under a held lock, and sweeps stale ones at start.
- **Components and files.** `internal/world/ssh`, `internal/world/ssh/helper`, `cmd/dsh` commands `ssh install`, `ssh check`, `ssh uninstall`.
- **Depends on.** `DH-LIB-SANDBOXMUX`; [DH-D-EXEC-1](execution.md#dh-d-exec-1) and [DH-D-EXEC-2](execution.md#dh-d-exec-2) (starting and stopping the `ssh` child as a trusted launch); [DH-D-SDK-1](sdk-acp-typert.md#dh-d-sdk-1) (the `sdk/jsonrpc` codec of module `src/sdk`); [DH-D-CLI-1](app-cli.md#dh-d-cli-1) (the command tree the three words join). Runtime: an OpenSSH client; a configured alias with a known host key.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-5](../design.md#dh-o-5), [DH-O-14](../design.md#dh-o-14). **Priority.** P1.
- **Done when.** `DH-T-SANDBOX-10` and `DH-T-SANDBOX-11` pass and [DH-E-SANDBOX-6](#dh-e-sandbox-6) has passed.

<a id="dh-d-sandbox-9"></a>
### DH-D-SANDBOX-9 The remote world: filesystem, processes, terminals and sandbox over the helper

- **Result and why it matters.** Every file tool, shell, terminal and language server works unchanged against a remote checkout, under the remote machine's sandbox.
- **How it is implemented.** Thin providers that forward each contract method to the helper, which calls the local providers there, file search included. Content, output and terminal bytes use streams. P2 addition: remote file watching. No relay to the local tool endpoint is designed: `dsh tool call` is `unavailable` on an SSH host.
- **Components and files.** `internal/world/ssh`.
- **Depends on.** `DH-D-SANDBOX-8`, `DH-D-SANDBOX-3`; [DH-D-FS-4](filesystem.md#dh-d-fs-4), [DH-D-EXEC-1](execution.md#dh-d-exec-1) and [DH-D-EXEC-5](execution.md#dh-d-exec-5) (the provider contracts and their contract test suites).
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7). **Priority.** P1.
- **Done when.** `DH-T-SANDBOX-12` passes and [DH-E-SANDBOX-6](#dh-e-sandbox-6) has passed.

<a id="dh-d-sandbox-10"></a>
### DH-D-SANDBOX-10 Remote file watching over the helper

- **Result and why it matters.** A component that watches files (instruction files, skills, the files view) sees changes on the remote checkout as it does locally, instead of only at its next read. Split out of [DH-D-SANDBOX-9](#dh-d-sandbox-9) so that its lower priority has its own entry.
- **How it is implemented.** The helper runs the shared `watch` package on the remote machine with the same polling fallback, and streams events on their own multiplexer stream; the remote provider's `Watch` returns them, with a resync event after a reconnect, because events during a lost connection are not replayed.
- **Components and files.** `internal/world/ssh` (watch method), `internal/world/ssh/helper`.
- **Depends on.** [DH-D-SANDBOX-9](#dh-d-sandbox-9); [DH-D-UTIL-3](util-test-support.md#dh-d-util-3) (`watch`).
- **Serves.** [DH-O-10](../design.md#dh-o-10). **Priority.** P2.
- **Done when.** `DH-T-SANDBOX-16` passes.

## 9. Acceptance

### 9.1 Local tests

`DH-T-NATIVESYSTEM-1` to `-7` in [node-addon-system.md](../libs/node-addon-system.md) cover the stage's own logic and are not repeated here.

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-SANDBOX-1` | `DH-D-SANDBOX-1` | Precedence (approved, session event, default); a root that is missing, a file, a symlink, or swapped for a symlink between two calls is refused; a registration is visible to its session only and ends with its fiber; scratch is present in both confined modes; a listener can refuse a mode change and nothing is appended; an escalation that is not strictly wider, has no reason or has no answerer is refused |
| `DH-T-SANDBOX-2` | `DH-D-SANDBOX-1`, `DH-D-SANDBOX-3` | A zero `Policy` is refused by the starter; no package outside the resolver can construct one (import and reflection test); config with an unknown key or mode is a load error |
| `DH-T-SANDBOX-3` | `DH-D-SANDBOX-3` | Table over backend, kernel version, policy and requirement: the computed state and missing effects; unknown evidence is never `full`; a custom runner is never `full`; probe and stage disagreement takes the weaker |
| `DH-T-SANDBOX-4` | `DH-D-SANDBOX-2` | With a fake stage: no `ready`, a truncated or oversized `ready`, canary writable, host declines, host closes early, exec fails. In each case the fake command's marker is absent and the error is the expected one. Fuzzing of the record decoder |
| `DH-T-SANDBOX-5` | `DH-D-SANDBOX-1`, `DH-D-SANDBOX-3` | One function gives the same write roots to the file fence and to every profile builder; with a counting fake filesystem, each root's real path is resolved once per start and extra roots are re-checked on every start |
| `DH-T-SANDBOX-6` | `DH-D-SANDBOX-4`, `DH-D-SANDBOX-5`, `DH-D-SANDBOX-7` | Golden files for bubblewrap arguments, Landlock grants and Seatbelt profiles per policy, including network denial, the masks of `secrets/` and `run/`, the hidden runtime directory, the endpoint rule, read-only overlay of the binary and of a home inside a root, and paths with quotes and spaces |
| `DH-T-SANDBOX-7` | `DH-D-SANDBOX-3` | Chain order; all probes fail gives `unavailable` while the fiber stays loaded; `Reprobe` after a fake repair changes the selection; a launch failure triggers exactly one re-probe |
| `DH-T-SANDBOX-8` | `DH-D-SANDBOX-6` | With a fake access-control API: intent is journalled before each change; a failure at every step rolls back that attempt only; cleanup removes only journalled entries; a shared label is left and reported; a directory with no access list is restored; refused consent changes nothing |
| `DH-T-SANDBOX-9` | `DH-D-SANDBOX-6`, `DH-D-SANDBOX-7` | The token's identifier list equals the start's roots plus scratch; `read-only` carries scratch only; `network: denied` is refused; identifiers of two paths differ and are stable |
| `DH-T-SANDBOX-10` | `DH-D-SANDBOX-8` | Over an in-memory pipe: a stalled stream does not stop the request stream; message and pending limits; half-close; cancellation; no socket or listener is created on either side |
| `DH-T-SANDBOX-11` | `DH-D-SANDBOX-8` | With a fake `ssh` process: wrong protocol or digest refuses; loss fails pending changes as `outcome unknown` and replays nothing; the reconnect budget; lease expiry runs cleanup and writes a receipt that the next connection reads |
| `DH-T-SANDBOX-12` | `DH-D-SANDBOX-9` | The A08 and A09 provider contract suites pass against the helper over an in-memory transport; a policy is decoded once; `Confinement` returns as data |
| `DH-T-SANDBOX-13` | `DH-D-SANDBOX-3` | A policy that makes the binary writable is refused under Landlock and overlaid under bubblewrap and Seatbelt; the denial hint is marked a hint and never changes exit code or output |
| `DH-T-SANDBOX-14` | `DH-D-SANDBOX-3` | Import-direction test: only the starter package calls the operating system's process creation; consumers do not import a backend; the stage imports only its three allowed sources |
| `DH-T-SANDBOX-17` | `DH-D-SANDBOX-3` | The not-audited statement of [DH-ADR-SANDBOX-11](#dh-adr-sandbox-11) appears verbatim in `dsh sandbox status` output, in the permissions fragment and the first-run notice rendered by the host, and in the website's security page source; a build whose release metadata says `stable` without a recorded review report fails the release check |
| `DH-T-SANDBOX-16` | `DH-D-SANDBOX-10` | Over an in-memory transport to a helper with a fake watch source: a created, changed and removed file arrive as events in order; a dropped connection ends the stream with a resync event and no replayed events; the provider's `Watch` stops when its context ends |
| `DH-T-SANDBOX-15` | `DH-D-SANDBOX-7`, `DH-D-SANDBOX-1` | A root that contains the home, or lies in it, is refused under Landlock and Windows and overlaid elsewhere; plugin state and scratch are accepted. Table over backend and probe result: `delegated-execution` unreachable is governed, reachable or unknown is `partial`; `secret-read` and `control-connect` appear in `Exposed` and never change `State`. `ForHelper` honours `Unconfined` only for a registered helper kind |

### 9.2 Real-environment scenarios

<a id="dh-e-sandbox-1"></a>
#### DH-E-SANDBOX-1 Linux: bubblewrap confines, Landlock is the fallback, an old kernel is reported partial

- **Proves.** `DH-D-SANDBOX-2`, `DH-D-SANDBOX-3`, `DH-D-SANDBOX-4`.
- **Given.** Three Linux machines with the release `dsh`: (a) `bwrap` and unprivileged namespaces; (b) no `bwrap`, a kernel at or above the reference Landlock version; (c) no `bwrap`, a kernel below it. On each: a temporary workspace `W`, an extra root `X`, a path `D` outside both. Builds on [DH-E-NATIVESYSTEM-1](../libs/node-addon-system.md#dh-e-nativesystem-1) and [-2](../libs/node-addon-system.md#dh-e-nativesystem-2).
- **When.** `dsh sandbox status`. Then, through the starter under `workspace-write`: write `W/ok` and `X/ok`; write `D`; delete a file beside `W`; start a child that writes `D`; run commands that exit 0, 1, 2 and 125 and print `bwrap: fatal` and `dsh sandbox-launch: failed`. On (c) repeat one start with `require: full`. On (b) run one start from a copy of the binary placed in `W`.
- **Then.** Status names the backend each machine uses. The two inside writes succeed; the others fail and `D` is absent. (a) reports `bwrap`, `full`; (b) `landlock`, `full` when the machine has no session bus and `partial` naming `delegated-execution` when it has one; (c) `landlock`, `partial` with the missing kinds, still blocks the outside write, and refuses the `require: full` start with `sandbox/enforcement-insufficient` and no command run. Each exit code and output reaches the caller unchanged as the command's own; the status descriptor is not open in the command. The start from `W` is refused with `sandbox/launcher-writable`. Whether a denied write left any report other than the command's own error is recorded per backend (debt row `sandbox.3`).
- **Cleanup.** Remove `W` and `X`; on failure keep the listings, kernel versions and records.

<a id="dh-e-sandbox-2"></a>
#### DH-E-SANDBOX-2 With no usable backend, a confined command does not run (Linux)

- **Proves.** `DH-D-SANDBOX-2`, `DH-D-SANDBOX-3`. It needs only the Linux backends of the same stage; the same case on macOS and Windows is a step of [DH-E-SANDBOX-3](#dh-e-sandbox-3) and [DH-E-SANDBOX-4](#dh-e-sandbox-4), which need those backends.
- **Given.** A Linux machine with no `bwrap` and Landlock disabled at boot; the built binary; a marker path `M`.
- **When.** Start `dsh`; read the status; ask for a command that creates `M` under `read-only` and under `workspace-write`; then under `danger-full-access`.
- **Then.** Status shows `unavailable` before any command. Both confined starts fail with the table's error and `M` is absent. The third creates `M` and is reported `unconfined`.
- **Cleanup.** Remove `M`; restore the machine image.

<a id="dh-e-sandbox-3"></a>
#### DH-E-SANDBOX-3 macOS Seatbelt confines

- **Proves.** `DH-D-SANDBOX-5`, `DH-D-SANDBOX-2`.
- **Given.** A macOS runner; a workspace `W` under a path with a symlinked prefix (`/tmp`); a path `D` in the user's home.
- **When.** The writes and exit-code cases of scenario 1, under both confined modes. Then `sandbox-exec` is replaced by a failing stub on the path the harness uses, and the steps of [DH-E-SANDBOX-2](#dh-e-sandbox-2) are repeated.
- **Then.** As scenario 1 with backend `seatbelt`. The state is `full` only if the probe shows that a job cannot be handed to `launchd` from inside the profile; otherwise it is `partial` naming `delegated-execution`, and which of the two was observed is recorded. Under `read-only` only the scratch and `/dev/null` are writable. The unified log holds a sandbox violation record for the denied write that names the command's process id and the path, and the failed command's result carries that path in `sandbox.denied`; if either is not so, the observation is recorded, the log step is removed as the `sandbox.3` row says, and this clause is not counted as a failure. With the stub, the result is that of [DH-E-SANDBOX-2](#dh-e-sandbox-2).
- **Cleanup.** Remove `W` and `M`; restore `sandbox-exec`; on failure keep the profile text and records.

<a id="dh-e-sandbox-4"></a>
#### DH-E-SANDBOX-4 Windows restricted token: roots, consent, journal, cleanup

- **Proves.** `DH-D-SANDBOX-6`.
- **Given.** A Windows machine with an NTFS and a FAT-class volume; a fresh home; workspace `W`, extra root `X`, outside file `D`, a hard link in `W` to a file outside.
- **When.** (1) Start a command with nobody to answer. (2) Run `dsh sandbox prepare` for `W` and `X`. (3) Under `workspace-write`: write in `W`, in `X`, in the scratch; write and delete `D` with `cmd`, PowerShell and a .NET call; print PowerShell's language mode under `read-only`. (4) Kill the host mid-start and restart it. (5) Try `prepare` on the FAT-class volume and on the drive root. (6) Run `dsh sandbox cleanup`. (7) As a test account that lacks the right to change the workspace's access-control list, repeat the steps of [DH-E-SANDBOX-2](#dh-e-sandbox-2).
- **Then.** (1) is refused and nothing changed. (7) Status shows the preparation failure before any command, both confined starts fail and the marker is absent, `danger-full-access` creates it and is reported `unconfined`, and the journal holds no row. (3) Inside writes succeed, `D` survives, the state is `partial` naming the hard-link effect, the language mode is full; whether the denial left any report other than the command's own error is recorded. (4) The sweep removes the dead start's scratch and entry. (5) The first is refused, the second warns. (6) `W` and `X` carry no entry the journal listed; a label another grant needs is left and reported.
- **Cleanup.** `dsh sandbox cleanup`, then compare descriptors with those saved before the run; keep both on a mismatch.

<a id="dh-e-sandbox-5"></a>
#### DH-E-SANDBOX-5 Network denial, home protection and `dsh tool call` on every backend

- **Proves.** `DH-D-SANDBOX-7`.
- **Given.** The machines of scenarios 1, 3 and 4; a listener on loopback; a harness home with a secret store file and a running host's control socket; a session whose agent has one tool that needs approval and no answerer.
- **When.** Under `network: denied`, a command opens a TCP and a UDP connection, reads the secret file, connects to the control socket, creates a file in the home, and asks the user's service manager to create a marker file outside every root; a script calls `dsh tool call` for the tool. Then once more with a workspace that contains the home.
- **Then.** No file is created in the home on any backend. bubblewrap: no connection, the secret directory and the control socket's directory are empty, the service manager is unreachable, `full`. Seatbelt: no connection, read and socket refused; the state and the marker's fate are recorded as scenario 3 says. Landlock: TCP refused where the kernel can, `partial` naming the rest, with `secret-read` and `control-connect` in `Exposed`; the control socket answers and returns no credential. Windows: the start is refused with `sandbox/network-unenforceable`; with the network allowed it runs, the secret file and the socket cannot be opened, and the marker's fate is recorded. On every backend that started, the tool call reaches the host, is denied for lack of an answerer, and is in the session log ([DH-ADR-8](../design.md#dh-adr-8)); where it cannot reach the host the result says `unavailable` and that backend's run stays open. With the home inside the workspace, Landlock and Windows refuse with `sandbox/home-writable` and the other two keep it read-only.
- **Cleanup.** Stop the listener and the host; remove the home and any marker. [DH-E-HOST-7](host-telemetry.md#dh-e-host-7) to [-9](host-telemetry.md#dh-e-host-9) run the full takeover attempt on the same machines.

<a id="dh-e-sandbox-6"></a>
#### DH-E-SANDBOX-6 A real SSH host: trust, install, remote world, loss and receipts

- **Proves.** `DH-D-SANDBOX-8`, `DH-D-SANDBOX-9`.
- **Given.** A disposable Linux host running OpenSSH with no `dsh`; a local OpenSSH alias with a key; the host key first absent from `known_hosts`; a remote workspace. Repeated with a Windows local machine.
- **When.** (1) Connect with the host key unknown, then with a changed key. (2) Add the key by hand; run `dsh ssh install`. (3) Replace the remote binary with another build and connect. (4) Restore; through the file and shell tools: edit a file through a symlink, run a confined command that writes inside and outside the workspace, read a 20 MiB file, run a command with paused output while a terminal stays responsive. (5) Start a long command, then kill the local `ssh` process. (6) Wait for the lease; let the fiber reconnect.
- **Then.** (1) Both fail before any helper runs; `known_hosts` is unchanged. (3) Refused with `ssh/helper-mismatch`. (4) Results equal the local world's; the remote backend and state are reported; no socket file appears on either machine. (5) The tool result says `outcome unknown` and nothing is replayed. (6) No remote process of the lost instance remains; the receipt is read and logged; new commands work.
- **Cleanup.** `dsh ssh uninstall`; destroy the host; keep receipts and logs on failure.

<a id="dh-e-sandbox-7"></a>
#### DH-E-SANDBOX-7 The policy text does not stop a real model from trying, and escalating, a denied task

- **Proves.** `DH-D-SANDBOX-1` (its model-visible policy section). Answers `UP-DEBT-found.Q3.2`: upstream found that one wording made a model give up before its first tool call in five of twelve runs (`packages/sandbox/sandbox-policy/README.md:165`).
- **Given.** A real model API, key supplied at run time; the built binary with the `headless` profile and the `cli` answerer scripted to allow an escalation; a workspace `W`; a fixed set of twelve tasks, six of which need a write outside `W` (denied first, then possible after an escalation) and six of which need only `W`; the policy text of the build under test, with its version recorded.
- **When.** Each task is run three times on each of the platform texts (Linux, macOS, Windows, read from the resolver for that platform without running the backend), twelve tasks times three runs per text.
- **Then.** For every task the model makes at least one tool call before giving up; for the six tasks that need escalation, the model asks for escalation with a reason in at least the share of runs recorded for the previous policy text, and never less than ten of eighteen; no run claims success without a tool call. The counts are recorded beside the policy text version. A change to the policy text is not merged until this scenario has passed on the new text.
- **Cleanup.** Remove `W` and the home; keep the transcripts of every failed run.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Stage for carried debt.** The carried rows are owned by stage `DH-S-47` of [../implementation-plan.md](../implementation-plan.md#dh-s-47).
2. **Recommended defaults, not confirmed by the owner.** (a) The multiplexer is `yamux`, MPL-2.0, a file-level copyleft licence; if the owner declines it, `smux` (MIT) is the verified substitute and the design does not change. (b) Under `danger-full-access` a command may also change the harness's own home; the preset's label says that commands can take over the harness and read stored keys, and no "full access except the harness home" mode is built. (c) A backend that cannot stop a command from asking a same-user service to run a program is reported `partial`, naming `delegated-execution`; upstream reports the same backends as `full`.
3. **To be learned by running, each tied to a scenario.** Descriptor passing through `bwrap` and `sandbox-exec`; the Seatbelt rules for the network, for a Unix-socket connection and against handing a job to `launchd`; whether connecting to a path socket works on a read-only bind and is outside Landlock's rules on the reference kernel; whether a Windows mandatory label stops a Low token from reading a directory and opening a socket in it, and whether that token can connect to a Unix socket at all; whether a confined Windows process can use the task scheduler or start an out-of-process COM server; tool-endpoint reachability under each backend; the 160-bit identifier form; binary-clean standard streams through the Windows OpenSSH client; PowerShell's language mode with a scratch. Until its scenario has run, each cell marked "Inference" in §6.1 is a plan, and the start-up probe, not this document, decides what a machine reports.
4. **Limits kept.** Reads are unconfined everywhere; Windows is always `partial`; Landlock on a desktop and Seatbelt are `partial` while `delegated-execution` is not governed; macOS depends on a deprecated tool; the remote host is trusted; the shared `/tmp` is writable under Landlock; a remote world has no `dsh tool call`. If Go's standard library gains a Landlock field for child processes ([node-addon-system.md](../libs/node-addon-system.md) §7), the stage is still needed for the canary and the two-phase exchange.

## 11. Units dispositioned by this spec

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 10 | 6 ported, 3 replaced, 1 merged |
| `UP-DEBT` | 40 | 27 resolved, 12 carried, 1 not-applicable |
| `UP-CFG`, `UP-SVC` | 3 and 3 | all ported |
| `UP-DEC` (handed over) | 7 | 6 ported, 1 replaced |

Go-side items declared: `DH-ADR-SANDBOX-1` to `-11`, `DH-D-SANDBOX-1` to `-10`, `DH-T-SANDBOX-1` to `-17`,
`DH-E-SANDBOX-1` to `-7`, and `DH-LIB-SANDBOXMUX`. The rows are in `trace/fragments/A10.jsonl`.
