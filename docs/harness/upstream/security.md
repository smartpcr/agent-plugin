# Upstream safety and security model, and what the Go design should do about it

> **Status: research reference, nothing built.** Written by agent F8 (key `SECURITY`) from
> the upstream checkout at commit `2e39e70`. Upstream facts carry a `path:line` citation
> relative to the upstream root. A sentence that opens with "Inference" is reasoning, not
> something read from source. Section 9 holds the decisions `DH-ADR-SECURITY-1` to `-11`.

## 1. Purpose

deepseek-harness (`dsh`) lets a language model run shell commands, edit files, fetch
web pages and load plugins on the user's computer. This document tells the designers of
the Go security model what upstream protects, from whom and how; where that protection
stops; and which rules its authors learned from real defects. Upstream is frank about
the limit: it "has not undergone a security audit and must not be treated as secure"
(`SAFETY.md:7`), and its controls "do not guarantee isolation" (`SAFETY.md:13`).

The guarantee upstream offers is narrow and should be stated the same way in Go:
**confinement limits where a spawned command may write files; approvals gate a widening
of that limit; neither hides the user's readable files, the network or the user's
credentials from a command that runs as the user.**

### Vocabulary

| Term | Meaning | Why it exists |
|---|---|---|
| **Sandbox** | The rule that a spawned command runs with fewer file permissions than the harness process | The model writes the command; the harness must not lend it full authority (`packages/shell/bash-sandbox/README.md:12`) |
| **Sandbox mode** | `read-only`, `workspace-write` or `danger-full-access`; governs file effects only (`docs/subsystems/sandbox.md:11`) | One small vocabulary that every enforcing tool and every operating system can share |
| **Sandbox seam** | The service `ctx.sandbox` with one operation, `confine(argv, policy)`, that returns a wrapped argv (`docs/subsystems/sandbox.md:164`) | Tools ask for confinement without knowing which operating-system mechanism supplies it |
| **Confinement backend** (runner) | The operating-system mechanism behind the seam: bubblewrap, Landlock, Seatbelt or the Windows restricted token | Each operating system offers a different primitive |
| **Enforcement** | A reported fact, `full` or `partial`: whether the backend governs every file effect the mode promises (`docs/subsystems/sandbox.md:30`) | Some kernels and Windows cannot enforce everything; the caller must be told, not reassured |
| **Write root** | A directory a `workspace-write` command may write under: the session's working directory, configured extra roots, and a backend temp area (`docs/subsystems/sandbox.md:43`) | Defines "the workspace" precisely enough for a kernel to enforce it |
| **Policy** | The mode plus write roots resolved for **one** tool call (`docs/subsystems/sandbox.md:87`) | Two sessions, or one approved retry, may need different limits at the same instant |
| **Filesystem fence** | An in-process check that a file tool's target lies inside a write root (`packages/fs/fs-sandbox/README.md:66`) | The file tools run inside the harness process, where the process sandbox does not apply |
| **Approval** | A one-shot yes or no from a person or a machine for one specific action (`docs/subsystems/approval.md:21`) | A human decides the cases a static rule cannot |
| **Approval policy** | `ask` or `never` per session; `never` rejects without prompting (`docs/subsystems/approval.md:33`) | Unattended runs need a deterministic answer |
| **Answerer** | The component that answers an approval request: a UI channel or an automation bridge | The approval service itself never prompts (`packages/interaction/user-approval/README.md:167`) |
| **Escalation** | The model's request to retry one denied command under a wider mode, with a reason (`packages/sandbox/sandbox/src/escalation.ts:209`) | Keeps the default tight and makes widening explicit and audited |
| **Permission preset** | A named pair of sandbox mode and approval policy offered as one selector (`docs/subsystems/permission-presets.md:11`) | Two independent knobs are confusing in a UI |
| **Credential reference** | The *name* of a secret, an environment-variable-style identifier (`docs/subsystems/credentials.md:13`) | Configuration can name a secret without containing it |
| **Credential record** | A stored secret owned by one plugin, addressed `<owner>/<id>`, for example an OAuth grant (`docs/subsystems/credentials.md:137`) | A grant has no environment variable to fall back to and must be updated under a lock |
| **Peer** | The authenticated caller of one HTTP or WebSocket request (`packages/client/connection/README.md:51`) | Every remote call must speak for someone |
| **Principal** | Who a Peer is: the `owner` or a named non-owner kind (`packages/api/remote-access/README.md:30`) | Access rules are written against principals, not connections |
| **Trust boundary** | A place where data or control passes between parties that do not fully trust each other | Every check in this document sits on one |

## 2. What is protected and from whom

**Assets.** The user's files outside the workspace; the workspace itself; stored
credentials (model API keys, OAuth grants, the cookie signing secret); the session log
(the durable record of what happened); the host process and its control surface (HTTP
API, approvals, permission switches).

**Actors and boundaries.**

| Actor | Trusted for | Not trusted for | Boundary and upstream control | Residual gap |
|---|---|---|---|---|
| The user (owner) | Everything | - | Launch token, signed cookie (`packages/host/authentication-token/README.md:36`) | Anyone who obtains the launch URL is the owner |
| The model | Proposing tool calls | Choosing its own permissions | Sandbox mode per call; escalation needs approval (`packages/sandbox/sandbox/src/escalation.ts:221`) | Reads, network and anything inside the write roots are unrestricted |
| Tool output and fetched content (may carry injected instructions) | Being data | Being instructions | No separate control: it reaches the model as text. The sandbox and approvals limit what the model can then do | Injected text can still drive any allowed action; `web_fetch` needs no approval (`packages/web/tool-web/README.md:280`) |
| A spawned command | Writing inside write roots | Everything else on disk | Confinement backend (section 3); scrubbed environment (`packages/subprocess/subprocess/src/index.ts:54`) | Runs as the user: can read the credential file (`packages/credentials/credentials-local/README.md:115`) and open sockets |
| A third-party plugin | Nothing by mechanism | - | None. Plugins load into the host process (`SAFETY.md:9`). The dynamic-plugin `node:vm` realm "is not a security boundary" (`packages/extensions/cordis-host-runner/README.md:66`) | A plugin has the host's full authority |
| A remote client: a web page from another origin, an API client, a shared-session viewer | What its principal's roles grant | Naming its own identity | Host and Origin checks (`packages/client/connection/README.md:49`); authenticated Peer; role bindings (`packages/api/remote-access/README.md:30`) | Plain HTTP exposes the cookie (`packages/client/connection/README.md:83`) |
| A remote host: SSH target, model provider, MCP server | Executing what it is sent | Being who it claims without proof | OpenSSH host keys, helper digest, per-stream TLS (`packages/ssh/ssh/README.md:34`, `:64`); account token released only to the configured origin (`docs/subsystems/account.md:11`) | A malicious SSH host is not detected by the digest check (`packages/ssh/ssh/README.md:36`) |
| Other local users | Nothing | - | Credential file is owner-only and refused if others can read it, on POSIX only (`packages/credentials/credentials-local/README.md:115`); private temp dirs (`docs/defensive-patterns/general.md:245`) | Windows skips the mode check |

## 3. Process confinement

### The seam

A tool that spawns a command resolves a policy for the call and passes its exact argv to
`ctx.sandbox.confine()`. The provider returns a `ConfinedArgv`: the replacement argv, the
enforcement fact, and two lists used to read the outcome from stderr
(`docs/subsystems/sandbox.md:128`). The contract has four rules worth keeping:

- **Fail closed.** With no usable backend, `confine()` rejects with `SANDBOX_UNAVAILABLE`.
  "Silent unconfined passthrough is never legal for a confined policy"
  (`docs/subsystems/sandbox.md:164`).
- **Policy per call, not per provider.** The provider holds no mode state, so concurrent
  sessions and a one-shot escalated retry cannot disturb each other
  (`docs/subsystems/sandbox.md:87`).
- **`danger-full-access` never reaches the provider.** It is an explicit bypass, not a
  wide profile (`docs/subsystems/sandbox.md:23`).
- **Enforcement is reported.** A caller that needs an absolute boundary must treat
  `partial` as a refusal or show it (`docs/subsystems/sandbox.md:30`).

Policy precedence is: an approved explicit mode for this call, then the session's last
`sandbox/mode` log event, then the deployment default, which is `read-only` unless
configured (`packages/sandbox/sandbox-policy/README.md:36`, `:81`).

### Backends

Selection is by platform first. A platform with one candidate uses it without a probe; a
platform with several probes them once in order and caches the verdict for the
provider's lifetime (`packages/sandbox/sandbox-local/src/index.ts:178`, `:553`).

| Backend | OS | How it confines | Reports | Cannot enforce | Launcher failure is recognised by |
|---|---|---|---|---|---|
| **bubblewrap** (`bwrap`), first choice on Linux | Linux | Mount namespace: host root read-only, fresh `/dev`, private process-ID namespace with its own `/proc`; `workspace-write` adds a private `/tmp` and writable binds for each write root (`packages/sandbox/sandbox-local/src/profiles.ts:16`) | `full` | Reads, network. Needs the `bwrap` binary and unprivileged namespaces | A stderr line containing `bwrap: `; no reserved exit code (`packages/sandbox/sandbox-local/src/index.ts:239`) |
| **Landlock**, fallback on Linux | Linux | A small static launcher, `landlock-run`, sets `no_new_privs`, installs an allow-list ruleset on itself and `exec`s the command; descendants inherit it (`native/system/docs/cli-contract.md:34`). Read and execute on `/`, write on `/dev/null`, host `/tmp` and write roots (`packages/sandbox/sandbox-local/src/profiles.ts:30`) | `full`, or `partial` on a kernel with an older Landlock interface version | Reads, network, process visibility; `/tmp` is the shared host directory (`packages/shell/bash-sandbox/README.md:39`) | Exit code 125 **and** a `landlock-run: ` line that is not the partial-enforcement notice (`native/system/docs/cli-contract.md:22`) |
| **Seatbelt** (`sandbox-exec`) | macOS | A profile that allows everything, then denies all file writes, then allows writes to `/dev/null` and the canonical write roots (`packages/sandbox/sandbox-local/src/profiles.ts:53`) | `full` | Reads, network, processes. Apple marks the tool deprecated (`packages/sandbox/sandbox-local/README.md:136`) | A stderr line containing `sandbox-exec: ` |
| **Windows ACL restricted token** | Windows | The caller's token is copied as a write-restricted token at Low integrity. Each granted directory gets an allow entry for a capability identifier, a deny of delete-child for everyone, and a Low integrity label; the child runs in a job object that kills it with its parent (`packages/sandbox/sandbox-windows-acl/README.md:98`, `:110`) | Always `partial` | Reads, network; NTFS hard links alias a writable file outside the workspace; trees another AppContainer tool labelled become unreadable (`packages/sandbox/sandbox-local/src/index.ts:196`). Extra write roots are ignored for processes (`packages/sandbox/sandbox-local/README.md:133`) | Exit code 127 and a `windows-acl-run: ` line |
| **Custom runner** (`runnerCommand`) | any | Operator-supplied argv given bubblewrap-style arguments | `full`, asserted, never probed (`packages/sandbox/sandbox-local/README.md:45`) | Whatever the operator's runner omits | Operator-supplied signatures |
| **SSH** | remote POSIX | The remote host selects its own local backend (`packages/ssh/sandbox-ssh/README.md:12`) | That backend's | That backend's | That backend's |

Two side effects of the Windows backend outlive a session and must be understood as
changes to the *user's* directory, not to harness state. The workspace's access-control
entries and its Low integrity label are left in place on purpose as a reuse cache, and
the label lets any other Low-integrity process of the same user write there
(`packages/sandbox/sandbox-windows-acl/README.md:128`, `:129`). Per-session temp grants
are revoked at shutdown; after a crash they remain as inert residue
(`packages/sandbox/sandbox-local/src/index.ts:500`).

### When confinement is unavailable or partial

| Situation | Upstream behaviour |
|---|---|
| No backend for the platform, or every probe fails | `SANDBOX_UNAVAILABLE`; the command does not run (`packages/sandbox/sandbox-local/README.md:57`) |
| The launcher starts but refuses its profile | Classified as a sandbox infrastructure failure, checked before any denial (`docs/subsystems/sandbox.md:128`) |
| The backend denies a file effect | The command fails; the tool appends `[sandbox: file access denied under <mode> mode]` and, where possible, an escalation hint (`packages/sandbox/sandbox/src/escalation.ts:108`) |
| Enforcement is `partial` | The command runs; the fact travels on the result (`packages/shell/bash-sandbox/README.md:90`) |
| Windows preparation fails half-way | Partial grants are rolled back, the temp directory removed, and `SANDBOX_PREPARATION_FAILED` raised with the cause; cleanup failures are attached, not hidden (`packages/sandbox/sandbox-local/src/index.ts:478`) |

Both classifications read the command's stderr. Upstream accepts that a program can
print text that looks like a denial, and that a confined child can forge a launcher
failure line: "Stderr remains an in-band attribution channel"
(`docs/postmortem/0004-landlock-partial-notice-misclassified-child-failures.md:39`).

## 4. Approvals and permission presets

### Decision flow for one tool call

1. The call's arguments are already logged and shown; no later stage may rewrite them
   (`packages/core/tools/src/contracts.ts:372`).
2. The `tools/pre-execute` hook returns `allow` (the default when nothing listens),
   `deny`, `cancel` or `ask` (`packages/core/tools/src/pipeline.ts:200`).
3. On `ask`, the pipeline calls the approval service. A missing service or a call
   without an agent is a denial with code `APPROVAL_UNAVAILABLE`
   (`packages/core/tools/src/pipeline.ts:457`).
4. The approval service requires an open turn, appends `approval/asked`, and decides:
   an aborted request is `cancelled`; policy `never` is `rejected` before any answerer
   runs; otherwise the answerers are consulted, and no answer, a thrown error or an
   unknown return value all become `unavailable`. It then appends `approval/decided`
   (`packages/interaction/user-approval/src/index.ts:243`, `:294`).
5. Only `allowed-once` proceeds, and it grants that one call
   (`docs/subsystems/approval.md:21`). Execution guards then run
   (`packages/core/tools/src/pipeline.ts:214`).
6. A sandbox-enforcing tool resolves the per-call policy and runs. If the sandbox denies
   the action, the model may retry **that exact command once** with
   `sandbox_permissions` and a `justification`. The target must be strictly wider than
   the current mode; repeating the current mode needs no approval; anything else is
   rejected before execution (`packages/sandbox/sandbox/src/escalation.ts:209`).
7. `tools/post-execute` may accept or block the result
   (`packages/core/tools/src/pipeline.ts:507`).

Inference: because step 2 defaults to `allow`, a stock composition prompts the user only
for sandbox escalation; ordinary tool calls inside the current mode run unprompted. The
experimental Auto review layer adds a model-based check before each call and is shipped
switched off (`packages/experimental/auto-review/README.md:12`).

### Presets

The base composition ships three presets: `read-only` (read-only, ask),
`workspace-write` (workspace-write, ask) and `danger-full-access` (danger-full-access,
never) (`packages/bundle/base/cordis.patch.yml:273`). `custom` is a derived label for a
pair no preset matches; `auto` is reserved for Auto review
(`docs/subsystems/permission-presets.md:11`). Switching a preset is a remote call whose
session access is decided before any knob is written
(`docs/subsystems/permission-presets.md:75`).

### What is logged

| Record | Content | Visible to the model |
|---|---|---|
| `approval/asked` | Request ID, tool name, call ID, reason (`packages/interaction/user-approval/src/index.ts:253`) | No |
| `approval/decided` | Request ID, outcome (`packages/interaction/user-approval/src/index.ts:260`) | No; the model sees only the tool result |
| `approval/policy`, `sandbox/mode`, `permission/preset` | The new value; replay restores the session's setting (`docs/subsystems/approval.md:33`, `packages/sandbox/sandbox-policy/README.md:85`, `docs/subsystems/permission-presets.md:77`) | A fresh policy summary is appended to the conversation |

Gaps upstream names: only one-shot grants exist, pending approvals do not survive a
restart, and a request carries no tool arguments
(`packages/interaction/user-approval/README.md:165`). Inference: the audit pair does not
record *who* answered, because `approval/decided` holds only the ID and outcome.

## 5. Credentials and authentication

### Where secrets live and who may read them

| Secret | Stored | Readable by | Protection |
|---|---|---|---|
| API keys (references) | `<harness home>/.credentials.yaml`, section `refs`; fallback to launch environment, project `.env`, home `.env` in that order (`packages/credentials/credentials-local/README.md:73`) | The harness; any process of the same OS user | Owner-only file; load refused if others can read it (POSIX) |
| OAuth grants and other records | Same file, section `records` | Same | Written only by read-modify-write under a cross-process lock (`docs/subsystems/credentials.md:201`) |
| Cookie signing secret | Record `client-connection/browser-session` (`packages/host/authentication-token/README.md:40`) | Same | Same |
| Launch token | Process memory, 32 random bytes (`packages/host/authentication-token/src/browser-auth.ts:17`) | Whoever sees the printed URL | Lives for one process |

Rules that hold across the seam:

- A consumer resolves a reference **once per operation** and never caches it, so a
  rotated key reaches the next request (`docs/subsystems/credentials.md:23`).
- The describing call returns configured, source, writable and a masked preview, never
  the value; the remote API can set a value but no method returns one
  (`docs/subsystems/credentials.md:37`, `:226`).
- The store is never loaded into the process environment, and diagnostics carry
  positions and key names, never values
  (`packages/credentials/credentials-local/README.md:115`, `:163`).
- A spawned child starts from the parent environment minus every name matching
  `KEY|PASSWORD|SECRET|TOKEN` and minus all `DSH_*` names; a caller may add an entry
  back deliberately (`packages/subprocess/subprocess/src/index.ts:54`, `:73`).
- The account token is not handed to consumers. A consumer names a request's
  destination and receives headers, and only for the configured inference origin
  (`docs/subsystems/account.md:5`, `:11`).

Upstream states the limit plainly: the agent's tools run as the same user and can read
the file, "that is discretion, not a boundary"; an operating-system keychain provider is
deferred (`packages/credentials/credentials-local/README.md:115`, `:211`).

### How the web host authenticates a browser

1. `dsh web` prints a URL carrying the launch token. `GET /?token=...` with a valid
   token returns a redirect and sets a cookie; an invalid token returns 401
   (`packages/host/authentication-token/README.md:36`).
2. The cookie is signed with HMAC-SHA-256, compared in constant time, and set
   `HttpOnly; SameSite=Strict`, with `Secure` only when the listener uses TLS; it lasts
   30 days by default (`packages/host/authentication-token/src/browser-auth.ts:139`,
   `:144`, `:162`; `packages/host/authentication-token/README.md:32`). Its name and signed audience include the scheme and authority, so an
   HTTP cookie cannot authenticate to a TLS listener
   (`packages/client/connection/README.md:47`).
3. Before any credential is checked, the request source is checked: `Host` must be
   loopback, the bind address or a configured trusted host; an `Origin` must name the
   same authority; `sec-fetch-site: cross-site` is refused with 403. This defends
   against DNS rebinding and cross-site requests, not against identity theft
   (`packages/client/connection/README.md:49`).
4. The server refuses to bind a wildcard address (`packages/host/webserver/README.md:39`).

The desktop application uses a different credential: the Electron main process creates
a secret, hands it to the host over a private channel, and attaches it to requests
itself, so it never enters the page's cookie jar
(`packages/host/authentication-desktop/README.md:12`).

### How a remote client is authenticated and authorised

An API client exchanges the launch token at `/connect` for its own Peer and cookie. One
streaming socket may attach per API Peer; closing it revokes the credential
(`packages/client/connection/README.md:53`). Authorisation is separate. The owner is
never checked. A non-owner (for example a shared-session viewer,
`packages/experimental/liveshare/README.md:12`) is allowed a call only if, for every
resource the call touches, a role binding lists the action; the decision is taken before
arguments are decoded, and a failing policy denies
(`packages/api/remote-access/README.md:12`, `:30`, `:41`, `:45`).
The caller's identity is read only from the admitted connection, never from arguments
(`docs/defensive-patterns/remote-and-api.md:53`).

For SSH, identity comes from the operator's OpenSSH configuration with batch mode, strict
host-key checking and no agent forwarding. The remote helper's SHA-256 digest is
verified before use, and each stream is protected by TLS with a random 256-bit
pre-shared key (`packages/ssh/ssh/README.md:34`, `:64`). After a connection loss the
client "never reconnects to replay a possibly executed action"
(`docs/subsystems/ssh.md:23`).

## 6. Filesystem and network policy

**Filesystem.** The file tools run in the host process, so a second mechanism, the
filesystem fence, applies the same policy to them. It covers write, edit, patch, delete
and move; `read-only` rejects all of them; `workspace-write` allows a target only if it
canonicalises under a write root; a move checks both ends before either effect
(`packages/fs/fs-sandbox/README.md:28`, `:48`). Reads are never fenced. The fence and the
Seatbelt profile take their write roots from one function so they cannot drift
(`packages/fs/fs-sandbox/README.md:77`). A race between the check and the write remains
and is accepted, because closing it needs primitives upstream judged too costly to port
(`packages/fs/fs-sandbox/README.md:81`).

Write roots: the session's working directory is the root and moves when the session
enters a Git worktree. Extra roots come from configuration or from a plugin's
registration for one live session; registrations are not saved and must be repeated
after resume. Each call rejects a root that is missing, not a directory, or a symlink
(`packages/sandbox/sandbox-policy/README.md:51`, `:59`). Upstream warns that granting a
Git directory also lets the command edit hooks that a later, unconfined Git run will
execute (`packages/sandbox/sandbox-policy/README.md:51`).

A separate, optional policy requires a file to be read before it is edited. It guards
against stale edits, not against attackers, and is disabled in the shipped compositions
(`docs/subsystems/filesystem.md:7`).

**Network.** There is no network policy. The mode vocabulary excludes network and
process visibility (`docs/subsystems/sandbox.md:11`); no backend restricts sockets
(`packages/shell/bash-sandbox/README.md:170`). The `web_fetch` tool reaches loopback,
private and cloud-metadata addresses and never asks for approval
(`packages/web/web-fetch-http/README.md:136`, `packages/web/tool-web/README.md:280`).

## 7. Defensive patterns

Upstream keeps its review rules under `docs/defensive-patterns/`. Each is a defect that
shipped or nearly shipped. Sources in the four tables below are relative to that
directory. The "In Go" column says whether the rule is still needed: **applies**,
**applies, changed form**, or **unnecessary** with the reason.

### Security-critical rules

| Rule (source) | Failure it prevents | In Go |
|---|---|---|
| Never give untrusted output the ambient environment or predictable paths (`general.md:243`) | Harness secrets leaking into a child's output; symlink races on temp files | **Applies, and Go's default is the unsafe one**: `exec.Cmd` with a nil `Env` inherits the whole parent environment. `os.MkdirTemp` and `os.CreateTemp` already give private, random, exclusive files |
| Unlink link-shaped paths (`general.md:249`) | A recursive delete following a symlink or Windows junction into its target | **Applies.** Inference: junction handling by `os.RemoveAll` on Windows must be tested, not assumed |
| Caller identity and access on every remote method (`remote-and-api.md:53`) | A non-owner reaching a resource the method forgot to name; identity taken from wire data | **Applies.** Go can make the declaration mandatory at registration (DH-ADR-SECURITY-8) |
| Contain callback exceptions in the dispatcher (`general.md:229`) | One listener's failure rejecting the dispatch or starving later listeners | **Applies, stronger**: an unrecovered panic in a goroutine ends the whole process |
| Report orthogonal outcomes independently (`general.md:7`) | A timed-out run that exits 0 being read as success | **Applies**; a result struct with separate fields |
| Dispose must reach quiescence (`general.md:223`) | A "stopped" component whose child process still runs | **Applies**: wait for the process after killing it; a context cancel is only a request |

### Lifecycle, state and structure

| Rule (source) | Failure it prevents | In Go |
|---|---|---|
| Honour public contracts on both sides (`general.md:13`) | Callers guessing whether a failure was thrown or streamed | **Applies** |
| Async state is not synchronous state (`general.md:19`) | Treating "agent is idle" as "my message finished" | **Applies** |
| Timing does not carry correctness (`general.md:25`) | Work that is right only if a microtask ran first | **Applies, changed form**: Go has no microtasks, but `time.Sleep` and goroutine scheduling order are the same mistake |
| Optional inputs state both behaviours (`general.md:48`) | Chains of `?.` hiding whether a field should exist | **Mostly unnecessary**: Go has no optional chaining and a nil dereference fails loudly. The contract rule remains |
| One value states one fact (`general.md:71`) | One `null` meaning unknown, empty and absent | **Applies, stronger**: Go's zero values merge "unset" and "empty" unless a type separates them |
| No new active/current singleton; shared state has one owner (`general.md:87`, `:104`) | Instances overwriting each other; state surviving teardown | **Applies**: package-level variables are Go's version of `globalThis` |
| Plugins do not read the cordis root context (`general.md:124`) | Skipping dependency declaration, scope filtering and disposal | **Applies to built-in components**: the Go port exposes `Context.Root()` (this repository, `src/go/cordis/context.go:66`). **Unnecessary for plugin processes**: no context crosses the process boundary (DH-ADR-4) |
| A business capability owns itself (`general.md:153`); the API layer carries generic orchestration only (`remote-and-api.md:83`); a new remote capability gets its own service (`remote-and-api.md:7`) | A central layer that every feature must edit | **Applies** |
| Changes carry performance evidence (`general.md:180`) | A regression hidden by an aggregate benchmark | **Applies**, with Go benchmarks |
| Migrations preserve behaviour (`general.md:205`) | Behaviour changes buried in a code move | **Applies to later refactors.** The Go work itself is a redesign, not a migration (DH-ADR-2) |

### Session and conversation

| Rule (source) | Failure it prevents | In Go |
|---|---|---|
| Projection is the only read and compute path; cold reads are the last resort (`conversation-and-session.md:9`, `:50`) | Two answers to one question; every cache miss reading the whole log | **Applies** to the handlers that render HTML |
| Retention follows one lifecycle rule (`conversation-and-session.md:85`) | Sessions that are never reclaimed because a holder was invisible | **Applies** |
| Retained excerpts do not keep their source string alive (`conversation-and-session.md:128`) | A 200-character excerpt pinning a 16 MB file in memory | **Applies**: a Go substring shares its parent's storage; `strings.Clone` is the standard-library fix |
| Node computation is incremental and publication is batched (`conversation-and-session.md:148`) | A full rebuild per streamed chunk | **Applies** to server-sent event fan-out |
| The grouping engine, node list, Markdown pipeline and user-message model carry no business branch (`conversation-and-session.md:182`, `:217`, `:253`, `:299`) | Shared rendering code that every feature must edit | **Applies** to templates as a design rule; the React specifics are gone |

### Package surface and client components

| Rule (source) | Failure it prevents | In Go |
|---|---|---|
| A plugin package exports exactly what the Loader loads (`package-surface.md:7`) | Postmortem 0001: a default export discarded the dependency list | **Unnecessary**: Go has no default exports; built-in components are registered by typed constructors |
| Two package shapes; standard `exports` keys; dependency scan; internal dependency syntax (`package-surface.md:45`, `:75`, `:117`, `:150`) | Build output and manifest disagreeing; duplicate dependency instances | **Unnecessary**: one `go.mod`, `internal/` packages and `go.sum` replace them |
| A new module `resolve` hook is refused (`package-surface.md:190`) | Module resolution that no longer matches the installed tree | **Unnecessary**: Go has no resolution hooks |
| No `await` at module scope (`package-surface.md:228`) | Import-time work that blocks or fails every importer | **Applies, changed form**: `init()` functions must not perform I/O or block |
| The thirteen React component rules (`client-components.md:7` to `:450`, skimmed) | Stale props, effect ordering, store misuse | **Unnecessary by DH-ADR-3**, except "a key states what it identifies" (`client-components.md:417`), which carries over to stable element IDs for htmx and server-sent event targets |

## 8. Incidents

The last column says how the Go design removes the cause; the ledger rows for the four
postmortem units record the same links.

| Postmortem | What happened | Root cause | What upstream changed | Closed in Go by |
|---|---|---|---|---|
| **0001** ACP server crashed on connect (`docs/postmortem/0001-acp-default-export-drops-inject.md`) | The editor bridge failed on its first two requests despite 178 passing tests and full line coverage (`:13`) | (1) An extra `export default` made the Loader unwrap the module to a bare function and lose its dependency list (`:50`). (2) An undeclared optional service was read through a property proxy whose lookup walks ancestors only (`:77`). Every test mounted the plugin by hand, bypassing the Loader (`:93`) | Removed the export; read optional services with `ctx.get`; added an end-to-end test through the real Loader that needs no API key (`:102`) | Go has no module export unwrapping and no property proxy; components are compiled in and declare dependencies by type: DH-ADR-4, DH-ADR-SECURITY-11 (b) |
| **0002** Filesystem tools permanently disabled (`docs/postmortem/0002-js-expression-disabled-filesystem-tools.md`) | A `disabled: !!js ...` entry was meant to enable file tools conditionally; they were off in every mode, and refreshed snapshots recorded "unknown tool" as the expected output (`:19`) | The expression is evaluated only inside `config`; elsewhere the unevaluated object is truthy and no diagnostic is raised (`:32`). The snapshot check accepted any deterministic transcript (`:34`) | An explicit overlay file; a static check that rejects expressions in entry metadata; the snapshot tool rejects `UNKNOWN_TOOL` (`:38`). Lesson: a permission control must describe only what it governs (`:47`) | A non-boolean `disabled` is a load error; file and shell tools share one policy, so no preset governs half of them: DH-ADR-SECURITY-1, DH-ADR-SECURITY-11 (c)(d)(e) |
| **0003** Web agent validated the wrong server (`docs/postmortem/0003-web-agent-gui-feedback-loop.md`) | An agent editing the GUI started a bare development server, saw HTTP 200 on a blank page, then validated a second host on another port and left it running (`:27`) | The model was never told which URL, process and mode served its own session; HTTP 200 was taken for application readiness; a shell `&` bypassed managed job tracking (`:34`, `:38`) | The launcher publishes the canonical URL in the prompt and in `$DSH_WEB_URL`; the standalone development server refuses to start; acceptance tests observe the original page (`:42`) | One binary serves the UI, so no separate development server can answer 200; the canonical origin is published: DH-ADR-3, DH-ADR-SECURITY-7 (d) |
| **0004** Landlock notice misclassified child failures (`docs/postmortem/0004-landlock-partial-notice-misclassified-child-failures.md`) | On older kernels every non-zero exit, even `grep` finding nothing, surfaced as "sandbox unavailable" (`:15`). Confinement was not weakened (`:23`) | The result type could carry only a list of substrings, so a benign notice and a fatal error under one prefix were indistinguishable; real-kernel tests skipped themselves on hosts without the old interface (`:35`, `:37`) | Structured rules: exit code plus fatal line, minus exact informational lines; a deterministic fake at the native boundary; file search no longer runs through sandboxed bash (`:43`) | Launcher status is out of band, so a child's exit code and stderr cannot be read as launcher failure: DH-ADR-SECURITY-3, DH-ADR-SECURITY-11 (a) |

## 9. Recommendations for the Go design

Module checks below were made on 2026-11-18 with `go list -m -json <module>@latest`,
`go mod download -json` and by reading the licence file in the module cache.
Standard-library facts were read with `go doc` on Go 1.27.4.

| Module | Version | Licence | Used for | Verified facts |
|---|---|---|---|---|
| `golang.org/x/sys` | v0.50.0 (2026-11-06) | BSD-3-Clause | Landlock system-call numbers and types; Windows token, ACL and job-object calls | `unix` has `SYS_LANDLOCK_*` and `LandlockRulesetAttr` but no wrapper functions. `windows` has `DuplicateTokenEx`, `SetTokenInformation`, `SetNamedSecurityInfo`, `ACLFromEntries`, `CreateJobObject`, `AssignProcessToJobObject`; it does **not** wrap `CreateRestrictedToken` |
| `github.com/landlock-lsm/go-landlock` | v0.11.2 (2026-11-10) | MIT | Applying a Landlock ruleset from Go with graceful downgrade | Requires `kernel.org/pub/linux/libs/security/libcap/psx` v1.2.78 (BSD-3-Clause OR GPL-2.0-only); without cgo that package calls `syscall.AllThreadsSyscall`, with cgo it compiles C |
| `github.com/zalando/go-keyring` | v0.2.8 (2026-03-23) | MIT | Operating-system keychain backend | Requires `github.com/danieljoos/wincred` and `github.com/godbus/dbus/v5` |

<a id="dh-adr-security-1"></a>
### DH-ADR-SECURITY-1 Keep the sandbox seam; one policy for every enforcing capability

- **Decision.** Keep: three file-effect modes, a policy resolved per call, enforcement
  reported as `full` or `partial`, fail closed when no backend works, and
  `danger-full-access` as an explicit bypass. Change: the seam is a Go interface that
  **starts** the confined process and returns a handle, instead of returning an argv for
  the caller to spawn. Shell tools, file tools, terminals and plugin processes all
  obtain their policy from the one resolver and their write roots from one function.
- **Why.** The seam is upstream's best security idea and is independent of JavaScript.
  Returning an argv forced every consumer to repeat spawn and classification logic; the
  defect in postmortem 0004 lived in exactly that repeated consumer code.
- **Tradeoff.** The seam now owns process start, so it depends on the subprocess layer.
- **Evidence.** `docs/subsystems/sandbox.md:87`, `:164`;
  `packages/fs/fs-sandbox/README.md:126`.
- **Serves.** [DH-O-7](../design.md#dh-o-7).
- **Verified by.** Local tests with a fake backend for precedence and fail-closed
  behaviour; one real-OS scenario per backend (DH-ADR-SECURITY-11).

<a id="dh-adr-security-2"></a>
### DH-ADR-SECURITY-2 Backends per operating system

- **Decision.** Linux: bubblewrap first, Landlock second. macOS: `sandbox-exec`.
  Windows: write-restricted Low-integrity token with per-directory grants and a job
  object. Keep upstream's selection order and its reporting of Windows as always
  `partial`. Change: (a) every backend is probed once at start, including a sole
  candidate, so the UI can show the confinement status before the first command;
  (b) the first change to a workspace's Windows access-control list needs the user's
  one-time consent, and a cleanup command removes entries the harness created.
- **Why.** These are the only unprivileged mechanisms each system offers; upstream
  already rejected AppContainer on Windows
  (`packages/sandbox/sandbox-windows-acl/README.md:160`).
- **Tradeoff.** `sandbox-exec` is deprecated and has no supported successor. The Windows
  label is shared with other Low-integrity software, so cleanup may remove only the
  harness's own entries and must leave the label when another grant still uses it;
  that state is reported as uncertain, not as clean.
- **Evidence.** `packages/sandbox/sandbox-local/src/index.ts:178`;
  `packages/sandbox/sandbox-windows-acl/README.md:128`, `:184`.
- **Serves.** [DH-O-7](../design.md#dh-o-7).
- **Verified by.** Real-OS scenarios on Linux with and without `bwrap`, on a kernel with
  an old Landlock interface, on macOS and on Windows.

<a id="dh-adr-security-3"></a>
### DH-ADR-SECURITY-3 The launcher is the harness binary; launcher status travels out of band

- **Decision.** No Node addon and no separately built C launcher. The harness binary has
  a hidden launch stage, `dsh sandbox-launch --status-fd N -- <argv>`, that does nothing
  but confine and `exec`. Under Landlock the stage applies the ruleset and
  `no_new_privs` to itself with `go-landlock`, then calls `syscall.Exec`. bubblewrap and
  `sandbox-exec` run the same stage as their command, so it starts already confined.
  On POSIX the stage reports on an inherited descriptor that closes on `exec`: a record
  `confined` with backend and enforcement, or `exec-failed`. Area A10 makes this a
  two-phase exchange (the stage reports that it is confined, the host answers, and only
  then does the stage `exec`), so the host never treats a command as started before its
  confinement is known ([../areas/sandbox-ssh.md](../areas/sandbox-ssh.md)). End of
  file with no record means the launcher failed and the command never ran; the
  command's exit code and stderr are never used to decide this. Windows needs no stage
  and no runner process: the host builds the restricted token (`CreateRestrictedToken`
  through a lazily loaded `advapi32` procedure, since `x/sys/windows` does not wrap it),
  starts the child suspended with a direct `CreateProcessAsUser` call, assigns it to the
  job object and resumes it; a failure is an ordinary Go error. The first text used
  `syscall.SysProcAttr.Token`, which cannot resume a suspended child
  ([../libs/koffi.md](../libs/koffi.md) §7).
- **Why.** It removes a native build per platform and removes in-band attribution, the
  cause of postmortem 0004. A confined child cannot forge the record because the pipe
  is closed before its first instruction.
- **Tradeoff.** One extra `exec` per command; the binary must be readable inside the
  sandbox and the stage must start without loading configuration. Inference: with cgo
  enabled `psx` compiles C; the fallback is raw `x/sys/unix` system calls on a locked
  thread followed by `exec`, which needs its own real-kernel proof. File-effect denial
  is still inferred from stderr and stays a hint
  (`packages/shell/bash-sandbox/README.md:171`).
- **Evidence.** `native/system/docs/cli-contract.md:22`;
  `docs/postmortem/0004-landlock-partial-notice-misclassified-child-failures.md:39`.
- **Serves.** [DH-O-7](../design.md#dh-o-7).
- **Verified by.** A fake stage that emits each record and each truncation; real-kernel
  scenarios where the child exits 1, 2 and 125 and prints launcher-like text.

<a id="dh-adr-security-4"></a>
### DH-ADR-SECURITY-4 A plugin process is untrusted and holds only granted capabilities

- **Decision.** Refines DH-ADR-4. Trust has three levels.

  | Level | Example | Runs | May do |
  |---|---|---|---|
  | Built-in | Compiled-in component | In the host process | Anything; reviewed as host code |
  | Installed | Third-party plugin or MCP server the operator added | Separate process, through the sandbox seam | What its granted capabilities allow |
  | Model-authored | Plugin written during a session | Separate process, `read-only` plus its own state directory | Granted capabilities, and enabling it needs an approval |

  A plugin process starts with a scrubbed environment, a private state directory as its
  only write root, and no credential. Its manifest declares capabilities (offer tools,
  read session events); the operator grants a subset, and
  the host checks every inbound protocol call against the grants, denying by default.
  A tool offered by a plugin passes through the same pre-execute, approval and sandbox
  path as a built-in tool. A plugin never calls a host service directly: the first text
  let a manifest ask to "call a named host service", which would be an entry that skips
  the pipeline, and area A22 removed it ([../areas/extensions.md](../areas/extensions.md)).
  A plugin can never answer an approval, create a session grant, switch a preset, read
  a secret value or name its own principal; a secret reaches it only when the operator
  maps a credential reference to it explicitly. An installed plugin is a local
  directory the operator placed ([DH-ADR-13](../design.md#dh-adr-13)); the model may
  attach a plugin to its own session and cannot make one durable
  ([DH-ADR-30](../design.md#dh-adr-30)).
- **Why.** Upstream loads plugins into the host with full authority (`SAFETY.md:9`) and
  says its in-process realm "is not a security boundary"
  (`packages/extensions/cordis-host-runner/README.md:66`). The process boundary of
  DH-ADR-4 is the first real place to enforce least privilege.
- **Tradeoff.** Plugins need a manifest and cannot reach host internals. On Windows
  reads are unconfined (`packages/sandbox/sandbox-windows-acl/README.md:125`), so a
  plugin can read user files; the operator is told this at install time.
- **Serves.** [DH-O-11](../design.md#dh-o-11).
- **Verified by.** Protocol tests that call every host method without the grant; a
  real-OS scenario in which a plugin tries to write outside its state directory.

<a id="dh-adr-security-5"></a>
### DH-ADR-SECURITY-5 Approval flow: closed outcomes, fail closed, attributed audit

- **Decision.** Keep: the four outcomes with `allowed-once` as the only grant; policy
  `never` enforced inside the service before any answerer; the asked and decided pair
  written to the session log; escalation only to a strictly wider mode, for one call,
  with a reason; requests that reference the tool call by ID instead of copying its
  arguments. Change: (a) the decided record also names the answering principal and
  channel; (b) answerers are registered per channel and exactly one may own an agent,
  instead of an ordered chain; (c) the HTML prompt is a server-rendered fragment bound
  to the call ID and answered by a same-origin POST from a Peer allowed to answer.
- **Why.** The closed, fail-closed vocabulary is what makes a missing UI safe. An audit
  record without the answerer cannot settle who approved an action once shared sessions
  and automation bridges exist.
- **Tradeoff.** No remembered "always allow" rules in the first version, as upstream;
  users will see repeated prompts. Pending prompts are lost on restart and the action is
  denied.
- **Evidence.** `packages/interaction/user-approval/src/index.ts:260`, `:302`;
  `packages/interaction/user-approval/README.md:165`.
- **Serves.** [DH-O-7](../design.md#dh-o-7).
- **Verified by.** Local tests for each outcome, a panicking answerer and a late answer
  after cancel; a real-browser scenario that approves and rejects an escalation.

<a id="dh-adr-security-6"></a>
### DH-ADR-SECURITY-6 Secrets: references in configuration, values behind one store interface

- **Decision.** Keep: reference versus record, resolve per operation, a describing call
  that cannot return a value, one-way write from the UI, destination-bound release of
  account tokens. Change: (a) the store is an interface with an operating-system
  keychain backend as default where one exists and the owner-only file as fallback. The
  keychain holds one data key, not one item per secret: the secrets are kept in a file
  encrypted under that key, because keychain items have size limits that sign-in grants
  exceed ([../areas/interaction-credentials.md](../areas/interaction-credentials.md)).
  The store holds no owner credential for the web UI: there is none to hold
  ([DH-ADR-28](../design.md#dh-adr-28)); (b) secret values use a Go type whose `String`, JSON and log forms print
  `[redacted]`; (c) one spawn helper builds every child environment explicitly, and a
  repository check forbids `exec.Command` elsewhere; (d) the harness's secret directory
  is masked from confined processes where the backend can do it (bubblewrap, Seatbelt)
  and reported as unmasked where it cannot (Landlock allow-lists, Windows).
- **Why.** Upstream's largest admitted gap is that any tool can read the credential file
  (`packages/credentials/credentials-local/README.md:199`). Go's default of inheriting
  the parent environment would silently reintroduce the leak upstream scrubs.
- **Tradeoff.** A keychain raises the bar but is not a boundary against same-user
  processes on every system; headless Linux often has none, hence the fallback. The
  name-pattern scrub remains a heuristic and misses secrets with unusual names.
- **Evidence.** `packages/subprocess/subprocess/src/index.ts:54`;
  `docs/subsystems/credentials.md:226`.
- **Serves.** [DH-O-7](../design.md#dh-o-7).
- **Verified by.** A test that formats and serialises the secret type through every
  logger; a real-OS scenario in which a confined command tries to read the store.

<a id="dh-adr-security-7"></a>
### DH-ADR-SECURITY-7 Browser authentication for the HTML UI

- **Decision.** Keep: a random launch token exchanged for an
  `HttpOnly; SameSite=Strict` cookie bound to scheme and authority; loopback by default;
  no wildcard bind; source checks before credential checks. The cookie is not signed:
  its value is a random id of which the host stores only a hash, so there is no signing
  secret that a command running as the same user could read and use to make itself the
  owner ([DH-ADR-28](../design.md#dh-adr-28);
  [DH-ADR-HOST-12](../areas/host-telemetry.md#dh-adr-host-12)). Change: (a) the `Host`
  allow-list applies to every method, and the standard library's
  `http.CrossOriginProtection` rejects cross-origin state-changing requests; (b) a
  non-loopback listener without TLS is refused unless the operator sets an explicit
  insecure flag; (c) the token exchange response forbids caching and referrers; (d) the
  host publishes its one canonical origin to the model's context and to tool
  environments. Item (e) of the first text, a desktop shell serving the UI through a
  webview's in-process handler, is withdrawn: there is no desktop application
  ([DH-ADR-18](../design.md#dh-adr-18)), and the loopback listener with the token
  exchange is the only path to the UI. (f) The launch token is accepted once, and only
  the running host shows a sign-in link: it opens the browser or prints the link on its
  own terminal. No command and no endpoint returns one. (g) Every state-changing request
  and socket upgrade must be same-origin and carry the peer's form token
  ([DH-ADR-API-9](../areas/api.md#dh-adr-api-9)).
- **Why.** A server-rendered htmx UI changes state through form posts and streams data
  through `GET`, so both DNS rebinding (`Host` check) and cross-site posts (origin
  check) matter. Postmortem 0003 showed that an agent that does not know its own origin
  validates the wrong server.
- **Tradeoff.** Anyone who obtains an unused launch URL is the owner, and the browser
  is the only graphical interface. A one-time token narrows the first text of this
  decision, which kept the URL usable for the life of the process; the price is that a
  second browser needs a second `dsh web`, which asks the running host to show a new
  link itself ([../libs/go-ui-stack.md](../libs/go-ui-stack.md) `DH-LIB-GOUISTACK-R10`;
  [../areas/host-telemetry.md](../areas/host-telemetry.md), the `launch.open` method of
  the control endpoint). The gaps that remain after these controls are listed in
  [../design.md](../design.md) §6; the owner accepted them on condition that they are
  reported.
- **Evidence.** `packages/client/connection/README.md:49`, `:83`;
  `packages/host/webserver/README.md:39`;
  `docs/postmortem/0003-web-agent-gui-feedback-loop.md:42`.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12).
- **Verified by.** Handler tests for forged `Host`, `Origin` and cookie audience; a
  real-browser scenario from a second origin.

<a id="dh-adr-security-8"></a>
### DH-ADR-SECURITY-8 Remote clients: Peers, principals and mandatory access declarations

- **Decision.** Keep: every request is admitted as a Peer with a principal; the owner is
  unrestricted; a non-owner needs a role binding on every resource touched; identity
  comes only from the admitted connection; an API client's credential is revoked when
  its stream closes. Change: a remote handler cannot be registered without an access
  declaration that names its resources, so "forgot to name a resource" is a compile or
  start-up error; list handlers receive a filter they must apply; policy evaluation that
  panics or errors denies.
- **Why.** Upstream enforces the rule by review (`docs/defensive-patterns/remote-and-api.md:53`).
  A type signature is cheaper and cannot be skipped.
- **Tradeoff.** Slightly more ceremony per handler; a deliberately global handler must
  say so in code.
- **Evidence.** `packages/api/remote-access/README.md:30`, `:41`.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-13](../design.md#dh-o-13).
- **Verified by.** A test that enumerates every registered handler and calls it as a
  non-owner with no bindings, expecting a denial each time.

<a id="dh-adr-security-9"></a>
### DH-ADR-SECURITY-9 The filesystem fence uses directory-rooted handles

- **Decision.** Under `workspace-write`, file mutations go through an `os.Root` opened
  on each write root, not through a canonicalise-then-check step. Write roots still
  reject a missing path, a non-directory and a symlink on each call.
- **Why.** `os.Root` refuses any name that resolves outside its directory, which closes
  the check-to-write race upstream accepted for lack of a portable primitive
  (`packages/fs/fs-sandbox/README.md:81`).
- **Tradeoff.** `os.Root` follows symlinks that stay inside the root and does not stop at
  mount points; a write root that contains a bind mount of something else is the
  operator's responsibility. Inference: several needed methods (`Rename`, `RemoveAll`)
  were added after `os.Root` first appeared, so the harness module's minimum Go version
  must be checked against them.
- **Evidence.** `go doc os.Root` on Go 1.27.4.
- **Serves.** [DH-O-7](../design.md#dh-o-7).
- **Verified by.** A local test that swaps a directory for a symlink between resolve and
  write; a real-filesystem scenario on Linux, macOS and Windows.

<a id="dh-adr-security-10"></a>
### DH-ADR-SECURITY-10 Network is a separate, explicit policy dimension

- **Decision.** Add `network: allowed | denied` to the policy beside the file mode,
  default `allowed` for parity with upstream. `denied` is enforced by bubblewrap
  (network namespace) and Seatbelt (profile rule), reported `partial` under Landlock
  where the kernel governs only some socket operations, and **fails closed** on Windows,
  where the token mechanism cannot restrict sockets. The host's own fetch tool refuses
  loopback, link-local, private and metadata addresses at connection time unless the
  operator allows them. The vocabulary is fixed now; enforcement may ship later.
- **Why.** Upstream has no network control and documents the consequence: a model can
  reach internal services and send data anywhere (`packages/web/tool-web/README.md:280`).
  Adding a dimension later would change the policy type that every backend consumes.
- **Tradeoff.** Many developer commands need the network, so the default cannot be
  `denied`. The fetch guard can be bypassed by a shell command unless the process
  sandbox also denies network.
- **Scope boundary of this record.** The sandbox policy has exactly two dimensions, file
  effects (the file mode and write roots) and network; processes, system calls and
  devices are outside it, so a confined command can still see and signal other processes
  of the same user, make any system call the backend does not block for its own reasons,
  and open devices it can read. This is a product choice, not a platform limit: some
  backends could narrow more (bubblewrap can hide other processes on Linux), but one
  policy that every backend on every operating system can enforce and report the same
  way is kept small on purpose, and the extra dimensions would be enforceable on one
  platform only. The rows of register entry U-K1-13 rely on this sentence
  (`L:sandbox.sandbox.1`, `L:sandbox.sandbox-policy.2`, `L:shell.bash-sandbox.1`).
- **Evidence.** `docs/subsystems/sandbox.md:11`;
  `packages/web/web-fetch-http/README.md:136`.
- **Serves.** [DH-O-7](../design.md#dh-o-7).
- **Verified by.** Real-OS scenarios that attempt a connection under each backend; a
  local test of the fetch guard against a redirect to a private address.

<a id="dh-adr-security-11"></a>
### DH-ADR-SECURITY-11 Security controls are proven on the real entry path, and configuration is strict

- **Decision.** (a) Each security control has two kinds of evidence: a deterministic
  fake at the operating-system or protocol boundary, and at least one scenario on the
  real mechanism. A real scenario that is skipped is open, never a pass. (b) Components
  are tested through the same loader path the product uses. (c) Configuration decoding
  is strict: an unknown key, a wrong type or a value in a place that is not evaluated is
  a load error. (d) A check independent of recorded output rejects semantic
  impossibilities such as a call to an unregistered tool. (e) A permission control
  describes only what it governs.
- **Why.** Three of the four incidents passed full test suites because the tests did not
  exercise the path that ships (0001), accepted a recorded failure as the expected
  output (0002), or skipped on hosts without the triggering kernel (0004).
- **Tradeoff.** Real-OS scenarios need Linux, macOS and Windows runners and an old
  kernel image; they are slower and cannot all run on every change.
- **Evidence.** `docs/postmortem/0001-acp-default-export-drops-inject.md:93`;
  `docs/postmortem/0002-js-expression-disabled-filesystem-tools.md:46`;
  `docs/postmortem/0004-landlock-partial-notice-misclassified-child-failures.md:55`.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-15](../design.md#dh-o-15).
- **Verified by.** The traceability tool's rule that a deliverable needs proof, plus the
  scenario lists in each area specification.

<a id="dh-adr-security-12"></a>
### DH-ADR-SECURITY-12 The harness is stated as unaudited until an independent security review is done

This record answers `UP-DEBT-found.Q3.1`: upstream's top-level safety statement says the
software has had no security audit and that its sandbox, approval prompts and permission
controls reduce risk but do not guarantee isolation (`SAFETY.md:7`, `:13`, `:15`). The Go
design has not been audited either ([../design.md](../design.md) §6). The sandbox area
answers the same unit for the confinement itself, with the statement in its status
surfaces and a review before a stable release
([DH-ADR-SANDBOX-11](../areas/sandbox-ssh.md#dh-adr-sandbox-11)); this record covers the
harness as a whole and the documents an operator reads.

- **Decision.** (a) The Go project ships the same statement in plain words: the
  repository's safety page, the operator guide's first-start section and
  `dsh sandbox status` each say that the harness is unaudited, that confinement and
  approvals reduce risk and do not guarantee isolation, and that a command the model
  runs under full access acts as the user. (b) The statement is removed only by an
  independent security review of the released binary: a reviewer who did not write the
  code examines the four layers of the owner boundary, the sandbox backends, the tool
  command line and the plugin protocol, and the review's findings are each fixed or
  recorded as a limit with a decision before the statement changes. (c) No release
  note, page or product text calls the harness secure, safe or isolated without the
  qualification of (a).
- **Why.** A user deciding whether to point the harness at untrusted repositories or
  content needs the true strength of the boundary. Upstream states it; dropping the
  statement in the port would overstate what the design proves, since its own proof
  covers only the stated contracts on tested builds.
- **Tradeoff.** The review costs time and money the project has not committed, so the
  statement may stay for every release of the first version. Until then the residual
  risk is the user's to accept, as upstream leaves it.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-14](../design.md#dh-o-14).
- **Verified by.** `DH-T-SECURITY-1`: a check over the built documentation and the
  output of `dsh sandbox status` finds the statement of (a) in each of the three places,
  and finds none of the words "secure", "safe" or "isolated" applied to the harness
  without it. The review of (b) is an owner action recorded outside the test suite, not
  a test.

## Open questions

1. **Library rows.** Three modules are verified here but no `DH-LIB` rows are declared;
   the library agents own them. `go-landlock` versus raw system calls depends on whether
   the desktop build enables cgo (DH-ADR-SECURITY-3).
2. **Minimum Go version** of the harness module, against the `os.Root` methods and
   `http.CrossOriginProtection` that DH-ADR-SECURITY-7 and -9 rely on.
3. **Remembered approvals.** Upstream has none; whether Go adds stored "always allow"
   rules, and how they are revoked, is undecided.
4. **Other areas.** The capability names for DH-ADR-SECURITY-4 come from the
   plugin-protocol specification; the model-visible origin (DH-ADR-SECURITY-7 d) from
   the web application area.
5. **macOS after `sandbox-exec`.** No replacement is identified; if Apple removes it,
   the probe fails and confined modes fail closed.
6. **Outcome links.** Settled: each record now has a "Serves" line naming its outcomes,
   as mapped in [../design.md](../design.md) §7.2.
