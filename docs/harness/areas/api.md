# The host's remote surface for the user interface (routes, access, event stream, terminals, workspace files): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) (`2e39e70`) and are cited as `path:line` relative to the upstream root. Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `API`. Nothing here has been built, run or tested.

| | |
|---|---|
| **Area key** | `API` (agent A19) |
| **Upstream packages** | 9, all under `packages/api`: `gateway`, `remotes`, `remote-access`, `session-controller`, `job-controller`, `settings-controller`, `terminal-controller`, `workspace-controller`, `workspace-files` |
| **Serves outcomes** | [DH-O-12](../design.md#dh-o-12) first; also [DH-O-1](../design.md#dh-o-1), [DH-O-3](../design.md#dh-o-3), [DH-O-5](../design.md#dh-o-5), [DH-O-7](../design.md#dh-o-7), [DH-O-10](../design.md#dh-o-10) |
| **Depends on areas** | A18 (listener, admission, peers), A01 and A05 (session reads, `Follow`, leases), A02 (agents, inbox, stream frames), A06 (query, projections, titles, working directory), A07 (workspaces, settings forms, attachments), A08 (filesystem seam, search), A09 (terminals), A13 (jobs), A17 (interactions, credentials), A23 (`schema`, `watch`), C1 to C7 (views, page shell, stream script, islands), X2 (non-owner principals) |
| **Priority** | P0 for the route contract, the access declarations, the event stream and the session routes: without them no browser shows a conversation. P1 for workspaces, jobs, settings, terminals, workspace files and the policy engine for non-owners |

## 1. Purpose

A person uses the harness through a browser. This area is everything between that browser and the host's services: which URL does what, who may call it, what comes back, and how a page that is already open keeps showing what the host is doing. It owns one shared piece that seven user-interface areas build on, **the HTTP surface and the event stream for the HTML UI**, and it specifies the routes for six features: sessions and the conversation, jobs, settings, terminals, workspaces and workspace files.

Upstream solves the same problem for a different client. Its browser runs a JavaScript application that holds a copy of host state, so upstream built a typed remote-call gateway, a multiplexed socket and repair logic in the browser. The Go host renders HTML ([DH-ADR-3](../design.md#dh-adr-3)), so that machinery disappears; the *guarantees* it gave do not. Without this area each UI area would invent its own URLs, error pages and live-update channel, an access check could be forgotten on one route, and a reconnecting tab could show an event twice or not at all.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **Peer, principal** | A peer is one admitted caller (for a browser: one signed cookie). Its principal says who it is: the `owner`, or a non-owner kind such as a viewer of a shared session. Area A18 admits peers; this area decides what a principal may do. | Access is decided from the admitted connection, never from request data. |
| **Route, declaration** | A route is one method and URL pattern with a handler. Its declaration is the Go value that also states its name, its access rule, whether it may wake a session, and what it returns. | A route that states nothing cannot be registered, so nothing is left to review. |
| **Action, resource** | The action is the route's stable name (`session.prompt`). A resource is one thing the request touches: a session, a workspace, or `global` when it names none. | They are the two inputs of every access decision. |
| **Role, binding, policy** | A role is a set of actions on one resource kind. A binding gives a role on one resource. A policy answers "which bindings does this principal hold now". | Sharing one session must not share the others. |
| **Page, fragment, region** | A page is a whole HTML document. A fragment is the HTML of one view. A region is an element with a stable `id` that a later fragment replaces. | The same view serves a first load, a form answer and a live update ([libs/go-ui-stack.md](../libs/go-ui-stack.md) §1). |
| **Request id** | A random value the host puts in every state-changing form when it renders it. | A submit that is retried, or sent twice, takes effect once. |
| **Problem** | The one shape of an error answer: status, a `<domain>/<reason>` code, a user-safe message, field messages, a request id for the log. | Errors look and behave the same on every route. |
| **Event stream** | One long HTTP response per tab (server-sent events, SSE) on which the host writes named events that carry fragments. | A page updates without asking. |
| **Recovery class** | What a stream source promises after a disconnect: **journal** (every item once, in order, resumed from a cursor), **state** (a complete baseline, then whole-value replacements) or **notice** (may be lost). | Each kind of data has a different honest answer after a disconnect. |
| **Cursor, baseline, `reset`** | The cursor is the `seq` of the last durable session event a page holds. A baseline is the complete current value of a state region. `reset` tells a page to fetch its regions again. | Resume without a copy of the log in the browser. |
| **Tab, interest, topic** | A tab is one open page, named by a random id the host put in it. A topic is a named source of state fragments (`jobs`, `files`). An interest is "this tab shows topic T for key K" (the files of one directory). | The host sends a tab only what it shows, and stops when it no longer does. |
| **Park, attention stream** | Parking closes a hidden tab's stream so that the browser keeps free connections. The attention stream is the one stream per browser that reports "something is waiting". | A browser allows about six connections to one origin ([DH-ADR-GOUISTACK-5](../libs/go-ui-stack.md#dh-adr-gouistack-5)). |
| **Activation** | Creating or resuming the live agent of a stored session, which takes its write lease. | Looking at a session must not wake it; prompting it must. |
| **User terminal, hold** | A user terminal is a shell a person opened in the UI; it runs with the person's permissions, not the agent's. A hold is an open terminal socket from some window. | The person's manual work is not the model's work. A terminal nobody holds can be reclaimed. |
| **Form token; base policy, frame policy** | The form token is a value bound to one peer's cookie that the host puts in every page and that every state-changing request must return; it is different from the request id, which makes a repeat harmless. The base policy is the one `Content-Security-Policy` value of every HTML document; a frame policy is the wider value a framed island's own document may carry. | A cookie is sent by the browser whoever wrote the form, and the token is not; the browser enforces the policy even if markup was injected ([DH-ADR-API-9](#dh-adr-api-9)). |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.api.gateway` | `dsh-api-gateway` | definition and provider, host and client | `ctx.typertGateway` validates and dispatches typed calls and owns the multiplexed stream socket; the client half provides `ctx.remote` (`packages/api/gateway/README.md:12`, `:27`, `:41`). |
| `UP-PKG-packages.api.remotes` | `dsh-api-remotes` | assembly | Selects which generated remote namespaces the application mounts and holds the allowlist of host events forwarded to the browser (`packages/api/remotes/README.md:32`, `:43`). |
| `UP-PKG-packages.api.remote-access` | `dsh-api-remote-access` | definition and provider | `ctx.remoteAccess`: roles, bindings and policies that decide what a non-owner peer may call or receive (`packages/api/remote-access/README.md:12`, `:27`). |
| `UP-PKG-packages.api.session-controller` | `dsh-api-session-controller` | controller | `ctx.sessionController`: session create, list, history pages, follow, prompt, cancel, fork, model choice, attachments, skill and file-reference completion (`packages/api/session-controller/README.md:13`, `:40`). |
| `UP-PKG-packages.api.job-controller` | `dsh-api-job-controller` | controller | `ctx.jobController`: a per-session roster stream, one job's output from a byte offset, and a human kill (`packages/api/job-controller/README.md:11`). |
| `UP-PKG-packages.api.settings-controller` | `dsh-api-settings-controller` | controller | Redacted settings and credential metadata, writes that never return a secret, and "open the settings file" (`packages/api/settings-controller/README.md:11`, `:28-32`). |
| `UP-PKG-packages.api.terminal-controller` | `dsh-api-terminal-controller` | controller and provider | `ctx.terminalController`: session-owned interactive terminals with shell discovery, screen recovery, a duplex attach stream and idle reclamation; also opens terminals for agents (`packages/api/terminal-controller/README.md:12`, `:28`, `:51`). |
| `UP-PKG-packages.api.workspace-controller` | `dsh-api-workspace-controller` | controller | `ctx.workspaceController`: create, rename, remove, reorder, archive and pin, a follow stream of the whole projection, the first-use workspace and the directory picker (`packages/api/workspace-controller/README.md:11`, `:25`, `:34`). |
| `UP-PKG-packages.api.workspace-files` | `dsh-api-workspace-files` | provider, host and client | `ctx.workspaceFiles`: bounded text pages, byte windows, listing, search and a change feed for files a session can read (`packages/api/workspace-files/README.md:12`, `:32-37`). |

### 3.2 Behaviour

**Calls.** A service method marked `@Remote` becomes callable as `ctx.remote.<namespace>.<method>`; a build step (Typert) generates validators and a typed client from the TypeScript types (`docs/api-gateway.md:9`, `:108`). A call is `POST /api/<ns>/<method>` with named arguments. The gateway refuses a disposed peer, checks the fields, validates the values, asks access control for a non-owner, resolves ids to live objects (which may resume a stored session) and invokes the method with a cancellation signal (`packages/api/gateway/README.md:33`; `docs/api-gateway.md:132`). The client never throws for a transport fault: every call resolves to `{ok, value}` or `{ok:false, error}` (`packages/api/gateway/README.md:62`).

**Streams.** The client keeps one WebSocket, `/api/remote.mux`, open from start; many logical streams share it, each with a client-to-host uplink (`packages/api/gateway/README.md:41`, `:45`). The host pings every 2 s and ends a socket silent for two intervals (`packages/api/gateway/src/index.ts:148`; `packages/api/gateway/src/stream-server.ts:42`). Each stream has a 256 KiB inbox and the socket a 1 MiB outbox; overflow of an uplink fails that stream (`packages/api/gateway/src/index.ts:149-150`; `packages/api/gateway/README.md:98`). The socket route is registered only after the application is ready (`packages/api/gateway/README.md:43`).

**Forwarded events and generations.** A reserved stream `$events` carries allowlisted host events to the browser. Its first frame, `ready`, is sent only after all listeners are attached, so nothing falls between "baseline read" and "live" (`packages/api/gateway/README.md:49`; `packages/api/remotes/README.md:49`). Approvals travel on it as questions the browser answers by a second call; only an owner peer that opted in may answer (`packages/api/gateway/README.md:51`). When the stream ends the "generation" is invalid: every consumer re-reads its baseline. Notifications are not replayed; pending questions are (`packages/api/gateway/README.md:92`).

**Sessions.** Three kinds of data with three recovery rules: the session log is paged and followed by `seq`; control state arrives as whole snapshots; notifications are fire-and-forget (`.agents/notes/implemented/architecture/2026-08-18-session-history-and-event-transport.md`). The client's `RemoteJournalStream` opens follow before the first page, publishes only contiguous changes and repairs a gap with a tail page (`packages/api/session-controller/README.md:62`). An opening page holds at least 50 messages across two turns, at most 500 (`:77`). Each endpoint states whether it may wake a session: list, history, follow, skills and path opening do not; prompt, queue edits, model and rename may; cancel needs a live agent (`:40`). A prompt carries a request id and is admitted once, also when retried after it was claimed (`:32`). `session.create` reuses a named blank session only after the host verified it and holds its writer (`:52`). An empty prompt with source `retry` continues after one of six failure codes (`:119`; the list is `packages/api/session-controller/src/continuation.ts:9-10`). Five live events feed the session list (`packages/api/session-controller/src/types.ts:677-704`).

**Jobs, settings, workspaces.** The job roster is a whole-set frame per change; one job's output is read from an absolute byte offset; a human kill records "cancelled by the user" (`packages/api/job-controller/README.md:25-27`). Settings and credentials are described redacted; no method returns a secret value (`packages/api/settings-controller/README.md:28-30`). The workspace follow stream sends one complete baseline, then ordered increments; archiving a session with running work is refused with a list of that work unless the caller asks to stop it (`packages/api/workspace-controller/README.md:25`).

**Terminals.** A person's terminal is owned by a session, runs through the session's subprocess provider with the operating-system user's permissions and outside the agent sandbox, and produces no model input (`.agents/notes/implemented/architecture/2026-09-16-user-terminal-permissions.md`). The host keeps an emulated screen; a new attachment gets a snapshot, then numbered output; the latest attachment owns input and resize (`packages/api/terminal-controller/README.md:61`, `:65`). A terminal nobody holds is reclaimed after two hours of confirmed idleness; unknown activity resets the clock (`:47`). The same package opens agent terminals under the sandbox, which pass to the person at the person's first typed input (`:51-53`).

**Workspace files.** Every method takes a session id; the host resolves the workspace root without waking the agent (`packages/api/workspace-files/README.md:28`). Named-file reads follow the filesystem backend's read authority, so a path outside the workspace is readable when the backend allows it; listing, search and directory watches stay inside the workspace (`:41`, `:110`). Text is paged by lines (5,000, 2 MiB), bytes by windows (2 MiB) or whole files (32 MiB), listings are cut at 2,000 entries (`:74-77`). The change feed carries invalidations only (`:68`). A separate route `GET /api/file?path=` serves any absolute path to the owner (`packages/api/session-controller/README.md:97`).

**Access.** The owner is never checked. A non-owner call is allowed only if, on every resource it touches, a binding grants a role that lists the action; resources are found by decorators, lookup declarations or by matching wire field names (`packages/api/remote-access/README.md:38-40`). A page is rendered with an access snapshot so that the client can hide what would be refused (`:48`, `:62-68`).

### 3.3 Failure, retry, rollback and cleanup upstream

- **Transport loss.** Connection retries with jitter under growing caps; each logical stream reopens and applies its own resume rule (`packages/api/gateway/README.md:66`, `:92`). Follow reopens with a full snapshot; a failed follow resumption stays failed (`packages/api/session-controller/README.md:136`).
- **A slow reader.** All streams pause their sources when the socket's unwritten bytes pass the outbox limit; a slow terminal follower fails explicitly and re-attaches from a new snapshot (`packages/api/terminal-controller/README.md:61`).
- **A policy throws.** The decision is refused as `policy-failed` and the shared event source keeps running (`packages/api/remote-access/README.md:44`).
- **Host restart.** Job rosters and output rings, terminals and screens, and the client's accepted-prompt display memory are lost (`packages/api/job-controller/README.md:56`; `packages/api/terminal-controller/README.md:102`; `packages/api/session-controller/README.md:140`).
- **Cleanup.** Closing a terminal awaits process cleanup; a failure keeps the resource for a retry, and the browser stores an unfinished close request to retry after reload (`packages/api/terminal-controller/README.md:65`, `:77`). A named blank session that fails a check is left unchanged (`packages/api/session-controller/README.md:52`).

## 4. Upstream decisions dissected

The first 26 rows are the decision notes handed to this area, named by file under `.agents/notes/` (`impl/` is `implemented/`, `prop/` is `proposed/`); the rest are visible only in code and READMEs, and their paths omit the leading `packages/api/`. `API-n` is short for `DH-ADR-API-n` in [§7](#7-decisions). The ledger rows hold the full reasoning.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `impl/architecture/2026-08-02-typert-remote-method-calls` | Decorated methods plus generated typed client, carried by a gateway | Drop the mechanism, keep the allowlist | Types exist at run time and the caller is the host's own handler | [API-1](#dh-adr-api-1) |
| `impl/architecture/2026-08-18-session-history-and-event-transport` | Three recovery rules; gateway owns transport, controllers own wire types | Keep the rules, change the carrier | They are the honest answers after a disconnect; resume moves to the host | [API-4](#dh-adr-api-4) |
| `impl/architecture/2026-09-05-workspace-files-service` | A dedicated bounded file-read service keyed by session | Keep | The UI needs the same reads; the client provider half becomes rendering | [API-8](#dh-adr-api-8) |
| `impl/architecture/2026-09-09-workspace-file-read-authority` | Named reads follow the backend; listing and watching stay in the workspace | Keep | One read policy, owned by the filesystem seam | [API-8](#dh-adr-api-8) |
| `impl/architecture/2026-09-16-user-terminal-permissions` | A person's terminal is unconfined and asks no approval | Keep | Otherwise a person must widen the agent's sandbox to work by hand | [API-7](#dh-adr-api-7) |
| `impl/architecture/2026-09-17-process-local-blank-sessions` | Reuse a blank session only after acquiring its writer; no process id stored | Keep, simplify | Acquire-then-use closes the race; the host, not the browser, picks the candidate | [DH-D-API-5](#dh-d-api-5) |
| `impl/architecture/2026-09-17-workspace-file-binary-transfer` | Bytes as multipart attachments of a typed call | Replace | A download is an ordinary response with `Range` | [API-8](#dh-adr-api-8) |
| `impl/architecture/2026-09-19-remote-duplex-stream` | Every stream is duplex; an invocation context carries the caller | Replace | Updates flow down on SSE, input goes up as requests; only the terminal is duplex | [API-1](#dh-adr-api-1), [API-7](#dh-adr-api-7) |
| `impl/architecture/2026-09-29-explicit-session-history-preparation` | Background actions refuse to trigger a format migration | Drop | No per-session migration exists: payloads convert on read, the layout migrates once at open | PERSISTENCE-5, -6 |
| `impl/architecture/2026-10-09-operator-scoped-connect-handshake` | Scripted callers get their own peer and must opt in to answer | Replace | No scripted caller uses UI routes; the rule "answering is opt-in" stays | [API-6](#dh-adr-api-6) |
| `impl/architecture/2026-10-12-peer-principal-and-remote-access` | Peers carry principals; methods declare resources; one decision point | Keep, make declarations mandatory | It is the authorization model; a missing declaration becomes a load error | [API-3](#dh-adr-api-3), [API-5](#dh-adr-api-5) |
| `impl/architecture/2026-10-18-websocket-permessage-deflate` | Compress the multiplexed socket by hand above 1,024 characters | Drop | The socket is gone; SSE is never compressed, the terminal socket is uncompressed | `DH-ADR-COMPRESSION-1` |
| `impl/architecture/2026-10-21-session-history-stream-and-snapshot` | Six operations: snapshot and observation of history, projections and state | Keep | Subscribing must not force a read or wake an agent | [API-4](#dh-adr-api-4) |
| `impl/architecture/2026-10-21-when-access` | Client registrations follow access | Replace | The host renders per request with the same function that enforces | [API-5](#dh-adr-api-5) |
| `impl/architecture/2026-10-23-store-scoped-image-reads` | Any authorised caller may read any image in the store | Change | A reference row per session makes the membership check one indexed lookup | [API-8](#dh-adr-api-8) |
| `prop/architecture/2026-09-29-session-migration-follow-redirect` | Show migration progress by a typed redirect | Drop | No per-session migration; host operations report progress as a state region | PERSISTENCE-5 |
| `impl/architecture/2026-11-15-session-retain-facets-and-recent-views` | Client references keep recent sessions warm | Replace | No client objects; a page is one range read plus stored projections | [API-1](#dh-adr-api-1) |
| `impl/bug-fix/2026-09-15-client-session-blank-reconciliation` | The client remembers accepted prompts so a row does not turn blank again | Replace | One source: the host marks a session not blank at prompt acceptance | [DH-D-API-5](#dh-d-api-5) |
| `impl/bug-fix/2026-09-20-session-open-read-coordination` | Auxiliary calls wait for the first open | Replace | One handler makes the reads at one cut; completion routes do not activate | [API-2](#dh-adr-api-2) |
| `impl/bug-fix/2026-09-29-reused-blank-runs-requested-preset` | A reused blank runs the requested preset | Keep | A removed preset must not break every new conversation | [DH-D-API-5](#dh-d-api-5) |
| `impl/bug-fix/2026-09-30-session-list-host-fairness` | Time-sliced list assembly | Not applicable | It exists because Node runs host work on one thread | none |
| `impl/feature/2026-09-09-web-sidebar-terminal` | Host-owned terminals, reattach by snapshot, idempotent create | Keep | Fits an island; the browser stores nothing authoritative | [API-7](#dh-adr-api-7) |
| `impl/feature/2026-09-14-unattended-browser-terminal-reclamation` | Reclaim after two hours of confirmed idleness with no hold | Keep | Killing on disconnect loses work; never reclaiming leaks shells | [API-7](#dh-adr-api-7) |
| `impl/feature/2026-09-17-sidebar-file-and-directory-auto-refresh` | Watch only what is open; watches carry invalidation only | Keep | Changes come from editors and shells too; the cost is bounded by what is shown | [API-4](#dh-adr-api-4), [API-8](#dh-adr-api-8) |
| `impl/feature/2026-10-26-manual-generation-continuation` | "Continue" is an empty prompt with source `retry` | Keep | No special loop branch, no invented instruction in history | [DH-D-API-5](#dh-d-api-5) |
| `prop/feature/2026-11-06-chat-history-consumption-details` | Which client class owns history prefetch | Drop | The classes do not exist; paging is one route | [DH-ADR-3](../design.md#dh-adr-3) |
| One multiplexed socket with its own heartbeat and limits | `packages/api/gateway/README.md:41` | Change | SSE for host to browser; one socket per terminal | [API-1](#dh-adr-api-1) |
| The live route opens only after the application is ready | `gateway/README.md:43` | Keep | A restarting host must not accept streams while services start | [API-4](#dh-adr-api-4) |
| Listeners attach before the baseline is read | `remotes/README.md:49` | Keep | Removes the gap between snapshot and live | [API-4](#dh-adr-api-4) |
| The capability set is fixed by build-time imports | `remotes/README.md:77` | Change | A route is registered at run time by the component that owns the feature | [API-2](#dh-adr-api-2) |
| Resources resolved by decorators or by wire field name | `remote-access/README.md:40`, `:85` | Change | Resources come from the URL pattern and typed fields only | [API-3](#dh-adr-api-3) |
| Conditions may read call arguments | `remote-access/README.md:38` | Change | A condition that reads arguments makes the page and the check disagree | [API-5](#dh-adr-api-5) |
| An access snapshot injected into the page | `remote-access/README.md:48` | Drop | The host renders only what the principal may use | [API-5](#dh-adr-api-5) |
| Each endpoint states its activation policy, in prose | `session-controller/README.md:40` | Keep, make it a field | A reader can list which routes may wake a session | [API-2](#dh-adr-api-2) |
| Prompt accepted once per request id | `session-controller/README.md:32` | Keep, make it durable | A double submit must be one turn, also across a restart | [API-3](#dh-adr-api-3) |
| `GET /api/file?path=` serves any absolute path to the owner | `session-controller/README.md:97` | Change | The file route names a session, so access is checked like any other | [API-8](#dh-adr-api-8) |
| Agent terminals live in the controller package | `terminal-controller/README.md:51` | Change | Terminal sessions are area A09's service; this area adds the person's policy and the route | [API-7](#dh-adr-api-7) |
| "Open the settings file" starts a native editor | `settings-controller/README.md:32` | Drop | No desktop application; the page shows the path | [DH-ADR-18](../design.md#dh-adr-18) |

## 5. Tech debt and its resolution

All 50 units are grouped by cause; each unit's own reason is in its ledger row. `L:` stands for `UP-DEBT-limit.packages.api.`. "Carried" rows keep the limit on purpose; each is classed as a platform limit or a scope boundary and names stage `DH-S-59`, where the implementation plan documents the known limits. Totals in the ledger: 32 resolved, 6 not applicable, 12 carried; `workspace-files.8` is resolved since (U-K2-1) and its row is re-issued by the debt reviewers.

| Group (cause) | Units | How the Go design answers it |
|---|---|---|
| **A. Build-time typed RPC.** Source mode with weaker validation, codecs per field, capabilities fixed by imports | `L:gateway.2`, `.3`, `L:remotes.1`, `.2` (4) | Not applicable: they exist because host and client are separate TypeScript builds. A route is registered at run time ([DH-D-API-1](#dh-d-api-1)). |
| **B. One generic socket.** Replay left to each domain, one setting for ping and deadline, hand-made compression, late detection of a vanished client, uplink credits | `L:gateway.4`, `.7`, `.8`, `.9`, `.10`, `L:remotes.3` (6) | The hub owns the three recovery classes; heartbeat and write deadline are separate; nothing is compressed; the only uplink is the terminal socket with acknowledgements ([DH-D-API-4](#dh-d-api-4), [DH-D-API-8](#dh-d-api-8)). `.8` is not applicable. `.9` is carried in bounded form: SSE has no reply from the browser, so behind a buffering proxy a vanished tab is noticed late; a stream is rotated after `MaxAge`. |
| **C. Errors and lookups at the gateway.** Empty details on internal errors; no per-endpoint activation policy; unprojected event payloads | `L:gateway.1`, `.5`, `.6` (3) | One Problem shape with a request id; activation is a field of every route; no host event is forwarded, a fragment is rendered for its reader ([DH-D-API-2](#dh-d-api-2)). |
| **D. Access checked by convention.** Unnamed resources unchecked, field-name matching, routes outside the gateway, snapshot and check disagree, payloads and host paths shown whole, a revoked view noticed late, fencing by argument | `L:remote-access.1` to `.4`, `.6` to `.8`, `L:job-controller.3` (8) | Mandatory declarations, resources from the pattern, one kind of route, one decision function without arguments, per-principal rendering, re-baseline on an access change ([DH-D-API-3](#dh-d-api-3)). |
| **E. Owner-only answering** | `L:remote-access.5` (1) | Carried on purpose: answering an approval widens what the model may do; area A17 decides whether any non-owner role may. The route still declares an action. |
| **F. State kept in the browser.** Accepted-prompt memory lost on reload and not shared by tabs, title increments computed from the client list, deletion markers in the client model, a follow that gives up, a deprecated entry point, a batch bound | `L:session-controller.3`, `.5`, `.7`, `.8`, `L:workspace-controller.2`, `L:settings-controller.1`, `UP-DEBT-deprecated.packages/api/session-controller/src/index.ts:500` (7) | The host is the only source: every tab renders the same durable facts, the host computes fork titles, the stream script retries without end ([DH-D-API-5](#dh-d-api-5), [DH-D-API-4](#dh-d-api-4)). |
| **G. Session work at the wrong time or size.** A list that can exceed its time slice, pixel limits, completion that resumes a cold session, uploads that restart from zero | `L:session-controller.1`, `.2`, `.6`, `.4` (4) | `.1` not applicable (one thread). `.2` resolved by the image admission of area A07. `.6` resolved by `Activation: None`. `.4` carried: one streamed request, no resumable offsets; an upload on loopback is a local copy. |
| **H. Live state that dies with the host or cannot be exact.** Job output interleaving, rosters lost on restart, no terminal restoration, PTY availability known only on open, reclamation needs activity observation, bounded screen and one writer, whole-projection replacement | `L:job-controller.1`, `.2`, `L:terminal-controller.1` to `.4`, `L:workspace-controller.1` (7) | Carried on purpose, each narrowed: a job or terminal is a child process a fiber owns ([design.md §4.4](../design.md#44-process-model)), so it ends with the host and the page says so; output chunks are labelled by channel; exited terminals no longer count against the cap; small state restarts from a baseline by rule ([API-4](#dh-adr-api-4)). |
| **I. Workspace file reads.** Watch support, recreated parents, no line total, a giant line, non-transactional reads, an unbounded change queue, search needing a packaged binary, dead feeds | `L:workspace-files.1`, `.2`, `.4` to `.7`, `.9`, `.10` (8) | A polling fallback for a provider that cannot watch, the harness watch service, a line total when the file is under the whole-file cap, continuation segments, metadata from the open handle with a changed-during-read flag, coalesced invalidations, the pure-Go search of area A08, a stale marker when a feed ends ([DH-D-API-9](#dh-d-api-9)). |
| **J. Scope kept on purpose.** Listing and watching only inside the workspace; the entry cap bounds the answer, not the directory read | `L:workspace-files.3`, `.8` (2) | `.3` is carried: it is the read-authority decision. `.8` is resolved (U-K2-1): area A08's `ReadDir` takes a limit and reports truncation ([filesystem.md](filesystem.md)), the route asks for the cap plus one, and Go's directory read costs no `stat` per child. |

## 6. Go design

### 6.1 Shape

All packages are under `src/harness/internal/host`, the family [design.md §4.2](../design.md#42-go-module-layout) gives to A18, A19, A20 and A22. Area A18 owns the listener, the source check, the credential exchange and static assets. This area owns what a UI route *is* and everything a route does after admission.

| Package | Component and service key | Why it exists |
|---|---|---|
| `web` | Component `host.web`; `web.Key` (`Routes`) | The declaration every UI route is written against, the registry, form decoding and the one error shape. |
| `access` | Component `host.access`; `access.Key` (`Service`) | Who may do what: principals, resources, roles, policies, one decision function. |
| `stream` | Component `host.stream`; `stream.Key` (`Hub`) | The event stream: connections, the three recovery classes, cursor resume, backpressure, parking, tabs and interests, the attention stream. |
| `conversation` | Component `host.conversation`; `conversation.Key` (`Commands`) | The commands a carrier needs that no single spine service offers: create with blank reuse, prompt (a command line is offered to A17's registry before admission), fork, continue. It is carrier-neutral and mounted by the `core` bundle, and it is the `Activator` area A13 needs to deliver a due schedule in a profile with no stdio carrier ([jobs-schedule-goal-plan.md](jobs-schedule-goal-plan.md), DH-D-JOBS-5). |
| `routes/sessions`, `routes/workspaces`, `routes/jobs`, `routes/settings` | One component each, no service | Thin handlers: decode, call the owning service, render a view. A composition that does not mount one has no such routes. |
| `userterm`, `routes/terminals` | Component `host.userterm` (`userterm.Key`, `Service`); route component | The person's terminals on top of area A09's `terminal.Service`: caps, holds, reclamation; the routes and the socket. |
| `wsfiles`, `routes/files` | Component `host.wsfiles` (`wsfiles.Key`, `Service`); route component | Bounded reads, listing, search and watching of files a session can read. |

```go
// package web
type Route struct {
    Name       string        // stable name and access action: "session.prompt"
    Method     string        // GET | POST | PUT | DELETE
    Pattern    string        // "/sessions/{sid}/prompt"; every {param} must be named by Access
    Access     access.Rule   // required: a zero value fails registration
    Activation Activation    // None (default) | LiveOnly | MayResume
    Kind       Kind          // Page | Fragment | List | Action | Download | Upload | Stream | Socket | Frame
    Policy     FramePolicy   // Kind Frame only: additions from the closed list of DH-ADR-API-9
    Form       any           // nil, or a pointer to the struct a body decodes into, strictly
    Limits     Limits        // body bytes and handler time; zero means web.Config's defaults
    Handle     func(rq *Request) Response
}
type Routes interface {
    Handle(owner *cordis.Context, r Route) error // an effect of owner: unloading owner removes the route
    List() []Info                                // for the catalog and for the deny-all test
    AllowInlineData(owner *cordis.Context, scriptType, text string) error // an effect; only type "importmap"; web hashes the text
    AllowFeature(owner *cordis.Context, feature string) error             // an effect; only "microphone" (Permissions-Policy, self)
}
// Request gives a handler only what its Route declared.
func (rq *Request) Context() context.Context     // ends with the client, the peer, or the owner's fiber
func (rq *Request) Principal() access.Principal
func (rq *Request) Session(param string) session.ID // only for a parameter Access bound to a session
func (rq *Request) Param(name string) string        // only for a parameter Access declared Opaque
func (rq *Request) Filter() access.Filter           // Kind List must use it
func (rq *Request) Tab() stream.Tab; func (rq *Request) Env() ui.Req // the tab's interests; language, zone, theme and the form token, from C1's shell.Env
func (rq *Request) NewRequestID() string; func (rq *Request) Peer() Peer // the id a view renders into a state-changing form; A18's peer, for FormToken
type Response interface{ response() } // closed: View, NoContent, Go(url), Problem, Bytes, Hijack

// package access
type Principal struct{ Kind, ID string }; type Resource struct{ Kind, ID string } // Kind "owner" is built in; "global" has no ID
func Owner() Rule                        // owner only
func On(from ...From) Rule               // the route's Name on these resources; none means global
func Admitted() Rule; func Public(class, reason string) Rule // every admitted principal, owner or not (per-principal pages of C1, C2); no principal needed, class is A18's "anonymous" or "callback"
func Session(param string) From; func Workspace(param string) From; func Opaque(param string) From
type Service interface {
    RegisterKind(owner *cordis.Context, kind string, from func(param string) From) error
    RegisterPolicy(owner *cordis.Context, principalKind string, p Policy) error
    Decide(ctx context.Context, p Principal, action string, rs []Resource) Decision // allow | deny(reason)
    Allows(ctx context.Context, p Principal, action string, r Resource) bool       // same function: rendering, filters
    Invalidate(p *Principal)                                                       // raises access.Changed
}
type Policy interface{ Bindings(p Principal) []Binding } // Binding{Role, Resource, Until}; Role{Name, ResourceKind, Actions} is declared with the policy; a denial's message may differ by principal kind

// package stream
type Source struct {
    Topic  string
    Class  Class       // State | Notice (the session journal is the hub's own source)
    Access access.Rule // checked at subscribe and again on access.Changed, under the action "stream.<Topic>"
    Open   func(sub Subscription) (stop func(), err error) // attach listeners, then send the baseline
}
type Subscription interface {
    Context() context.Context; Principal() access.Principal; Env() ui.Req; Key() string
    Replace(region string, render func() (ui.SafeHTML, error)) // latest wins; rendered when written
    Notify(render func() (ui.SafeHTML, error))                 // may be dropped
}
type Hub interface {
    Register(owner *cordis.Context, s Source) error
    SetRenderer(owner *cordis.Context, r Renderer) error // from area C4: Events(env, []Event) SafeHTML, which draws its own fallback row for an item it cannot draw and returns no error for it; Live(env, frame) SafeHTML
}
type Tab interface{ Watch(topic, key string) error; Unwatch(topic, key string) }
```

**How a request runs.** (1) Area A18 admits it: source check, credential, peer. (2) `web` finds the route; a path that no loaded component registered is 404. A request whose method is not `GET` or `HEAD`, and every socket upgrade, must then pass the forgery check of [DH-ADR-API-9](#dh-adr-api-9) (same origin and, for a body, the form token), or it is answered 403 `web/forgery` before anything is decoded. (3) The rule is resolved: each bound path parameter becomes a resource, and `access.Decide` runs for a non-owner. A refusal is answered before any session is read, so it says nothing about whether the id exists. (4) A body is decoded into `Form` by the `schema` package (area A23): unknown fields, wrong types and oversize bodies are refused. A form field whose Go type is a resource id must carry an `access` tag, and is then checked like a path parameter. (5) `Activation` is applied: `None` never creates an agent; `LiveOnly` answers 409 when there is none; `MayResume` resumes the session once, however many requests arrive together. (6) The handler calls services and returns a `Response`; `web` renders it.

### 6.2 Contracts

**URL rules.** They are what lets access, logs and tests be derived from a pattern.

| Rule | Statement |
|---|---|
| Nouns and ids | A URL names what the user sees: `/sessions/{sid}`, `/workspaces/{wid}/title`. Lower case, hyphens, no trailing slash, no extension, no version prefix (UI routes carry no compatibility promise, [DH-ADR-WIRE-1](../upstream/wire-and-api.md#dh-adr-wire-1)). |
| Resource ids live in the path | The id of a session, workspace or any other access-controlled thing is a path segment, bound in the declaration. A query parameter is view state only: a paging cursor, a filter, a file path inside a session. |
| Methods | `GET` reads and never changes state. `POST` creates or performs a named action (`/sessions/{sid}/cancel`). `PUT` replaces one whole value (`/title`, `/order`). `DELETE` removes. |
| Reserved | `/events` and `/sessions/{sid}/live` (streams), `/events/attention`, `.../socket` (WebSocket). `/assets/`, `/auth/` and island frames belong to A18 and C1. |
| One owner | A second registration of a method and pattern, or of a name, fails the component that tried it. |

**What a route returns.**

| Kind | Without `HX-Request` | With `HX-Request` (htmx) |
|---|---|---|
| `Page`, `Fragment`, `List` | The fragment wrapped in the page layout (area C1) | The fragment alone. Both carry `Cache-Control: no-store` and `Vary: HX-Request`. |
| `Action` that changed something the stream draws | `303` to the resource's page | `204`, or the posted form's own region (a cleared composer). The change itself arrives on the stream. |
| `Action` that created something | `303` to the new resource | `200` with `HX-Location` naming it |
| `Action` on plain state (a setting, an order) | `303` | The updated region; other tabs get the same region as a state replacement |
| `Download` | Bytes with `Content-Type` from the harness table, `nosniff`, a sandboxing policy, `ETag`, `Range` support | same |
| `Upload` | A streamed `multipart/form-data` body under the attachment limit, never buffered whole | A fragment with the stored references |
| `Frame` | The document of a framed island, with its frame policy and `frame-ancestors 'self'`. Area C1 registers one such route per framed island (`GET /ui/frames/<tag>`, name `ui.frame.<tag>`); the URL has no query, and an instance gets its attributes in a `postMessage` `init` message | not applicable |

**Headers `web` writes.** No view and no handler writes a security header; `web` does, from the route's kind. Every HTML answer (`Page`, `Fragment`, `List`, an `Action` that returns a region, every HTML error answer, a `Frame`) carries `Content-Security-Policy` (the composed page policy, or the route's frame policy), `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`, `Cross-Origin-Resource-Policy: same-origin`, and a `Permissions-Policy` that denies every feature (a component may register the one allowance `microphone=(self)` with `AllowFeature`, which area X4's voice input does). The page policy is area C1's base value `ui.BasePolicy`, the directive list of [DH-ADR-GOUISTACK-3](../libs/go-ui-stack.md#dh-adr-gouistack-3), plus one hash source in `script-src` per import map registered through `AllowInlineData`; `connect-src 'self'` admits the terminal's same-origin WebSocket and `frame-src 'self'` stays. A download keeps `Content-Security-Policy: sandbox; default-src 'none'`. The policy text is composed when the route table is swapped, never per response, and carries no nonce.

Two rules keep "once and in order" true. **One writer per region:** a durable session event is drawn only by the stream, never by the response to the form that caused it and never by a page beyond its cursor. **Idempotent submits:** every state-changing form carries the request id the host rendered into it. A prompt uses it as the message id. Admission-once is area A02's rule and is used as written ([agent-loop.md §6.2](agent-loop.md)): a message id is unique over the session's whole history, and a repeated `Send` appends nothing and returns `agent/duplicate-message` with the first receipt, which `conversation.Prompt` answers as success. A repeat is therefore one message also after the first was claimed and after a host restart, and this area keeps no memory of ids. A create uses an id the host minted when it rendered the form.

**Errors.** One table for every route; the code comes from the harness's one `<domain>/<reason>` table.

| Status | When | Body |
|---|---|---|
| 400 `web/bad-request` | Malformed body, unknown field, wrong type, bad cursor | Problem |
| 401, 403 (source), 403 `auth/untrusted-peer` | No credential; failed source check; or a local peer that is a process the host itself started for the model, refused by the listener before any credential is read. All area A18's | A page that says how to start `dsh` |
| 403 `access/denied`, `web/forgery` | The rule refuses this principal; details name the action and a reason (`no-allow`, `policy-failed`, `resource-invalid`). Or the forgery check failed: the page says "reload this page", because a stale token after a new cookie is the common cause | Problem |
| 404 | No such route (also: its component is not loaded), or no such thing | Problem |
| 409 | A conflict the user can resolve: `session/writer-held`, `session/not-running`, `settings/conflict`, `workspace/session-active` (with the list of running work) | The region re-rendered with current state and the message |
| 413, 422 | Body too large; a field failed validation | 422 re-renders the posted form with a message beside each field |
| 500 `web/internal` | Anything unclassified, a view that failed | Problem with a request id; the detail goes to the log only. A failed view writes nothing ([`DH-LIB-GOUISTACK-R13`](../libs/go-ui-stack.md)) |
| 429, 503 | A tab reconnects its stream faster than `ReconnectBurst` allows (`stream/reconnecting`); not ready yet, or `store/busy` | Problem with `Retry-After` |

An error answer is a fragment for htmx and a page otherwise. The page shell's `htmx-config` ([DH-D-UICORE-1](ui-core.md#dh-d-uicore-1)) lets htmx swap an HTML error answer: 422 and 409 into the form's own target, others into the notice region named by `HX-Retarget`.

**The event stream.** Three routes: `GET /events` (a tab that shows no session), `GET /sessions/{sid}/live?after={seq}` (a tab that shows one; this refines the illustrative `?session=` of [DH-ADR-WIRE-2](../upstream/wire-and-api.md#dh-adr-wire-2) so that the session is a declared resource) and `GET /events/attention`. Every stream request also names its tab (`tab=`). The event names are a closed set owned by this area; the first seven are those of [libs/go-ui-stack.md §5.3](../libs/go-ui-stack.md).

| Event | Class | `id` | Payload and rule |
|---|---|---|---|
| `session` | journal | `<sid>:<seq>` | The fragment of one or more consecutive durable events, in commit order, plus the cursor element `#stream-cursor` holding the last `seq` covered. An event with nothing to draw still moves the cursor. A fragment may depend only on events at or before its own, so a replay draws what the live stream drew. |
| `live` | state | none | The in-flight assistant message: whole on connect, then finished blocks appended and the unfinished tail replaced. On connect with no attempt in flight it is an empty tail, which clears a stale partial after a host restart. Frames of one tab are at least `LiveMinInterval` apart, and the settled message and the clearing of the tail are written as one `session` event. |
| `interaction` | state | none | Pending approvals and questions of the sessions this principal may answer (area A17's registry); complete on connect. |
| `shell` | state | none | Workspace and session lists, running marks, counters; complete on connect. |
| `view` | state | none | The regions of the tab's interests (job roster, terminal roster, an open directory, a settings form); complete on connect and whenever an interest is added. |
| `notice` | notice | none | A transient message (an agent failed outside a turn). Never the only carrier of something that matters. |
| `reset` | n/a | none | "Fetch your regions again." The stream script raises one DOM event; every live region re-requests itself, which also re-registers its interests. |
| `park` | n/a | none | The host is about to close this stream to free a connection; the page shows "paused". |
| `attention` | notice | none | Session id, title and kind only; never transcript content. Only on the attention stream. |

- **How a view subscribes.** It does not open anything. A fragment route that renders a live region calls `rq.Tab().Watch(topic, key)`; the tab id travels in a header that the page layout puts on every htmx request. From then on the tab's stream carries `view` events for that region, each a whole replacement by element `id`. Collapsing or closing the region is a request too, which calls `Unwatch`. Interests are owned by the tab record: capped (`InterestsPerTab`), dropped `TabGrace` after the tab's last stream closed, and replaced by a "not live, reload" marker when evicted.
- **Connect order.** Check the rule; attach to every source (listeners first); send the state baselines; then the journal from the cursor; then live changes. A change that arrives while a baseline is being read is applied after it, never lost.
- **Resume.** The cursor is `Last-Event-ID` when present, else `after`. Same session and at most `ReplayLimit` events behind: the missed events are rendered and sent. Otherwise, or when the cursor is beyond the log's head: `reset`. The journal is read through `store.Follow(id, afterSeq)` (area A05), which serves a session whether this process, another process or nobody holds its lease, so resume needs no live agent and works after a host restart. The interrupted turn's closing events, appended by whoever resumes the session, arrive as ordinary events. A tab id the host does not know (expired, or a new host) gets `reset` before anything else.
- **Ordering.** One goroutine writes a connection. Journal events are written in `seq` order with no gap. A pending `live` frame is discarded before the durable message that settles it is written.
- **Backpressure.** The journal is *pulled*: the connection reads the next page from the store only when its last write completed, so a slow tab lags and holds one page of memory. State is a set of slots, one per region, where the latest value replaces a waiting one and is rendered only when written. Notices are dropped when their small queue is full. Every write has a deadline; a tab that accepts nothing for `WriteTimeout` is closed and resumes by cursor. No producer ever waits for a browser.
- **Parking.** The host counts session and shell streams per peer. A stream beyond `StreamsPerPeer` (4 on plain HTTP, unlimited under TLS) sends `park` to the longest-open one and closes it. The attention stream counts and is never parked; the terminal socket is outside the count. A second attention stream for one peer closes the first.
- **An item that cannot be drawn** never ends a stream and never stalls a cursor ([DH-ADR-API-10](#dh-adr-api-10)). Area C4's renderer draws its own fallback row for an event it cannot draw and returns no error for it ([ui-conversation.md §6.3](ui-conversation.md)); the row moves the cursor like any event and is the same on a replay and in a page's opening window. Should the renderer itself return an error or panic, the hub writes a constant placeholder built from the event's `seq` and type only, moves the cursor and logs once per session, `seq` and build. A failed state region is replaced by a "not current, reload" marker and rendered again at its next change. A failed `live` tail or notice is dropped.
- **Lifetime.** A stream ends when the client goes away, its principal loses the rule, the peer is revoked, the store cannot be read, `MaxAge` passes, or the hub's fiber unloads. Stream and socket routes answer 503 until boot reports ready. A tab that opens more than `ReconnectBurst` streams within `ReconnectWindow` is answered 429 with `Retry-After`, so that no fault on either side becomes a tight loop. The browser side is C1's ([ui-core.md §6.3](ui-core.md)): the stream script waits a growing delay between attempts, honours `Retry-After`, and after `ReconnectGiveUp` attempts in a row that delivered no event stops retrying and shows `stalled` ("not live, reload").

**Routes of the six features.** Names are final for this area; view names belong to the UI areas. `S` is `access.Session("sid")`; "owner" is `access.Owner()`. Activation is `None` unless stated.

| Feature | Method and pattern (name) | Returns and rules |
|---|---|---|
| Sessions | `GET /sessions` (`session.list`) | List, filtered by `rq.Filter()`, keyset paged, with an optional text filter `q=` answered by A06's `Search`; rows from stored projections, each `current`, `behind` or `pending` as area A06 reports. Access: global. |
| | `POST /sessions` (`session.create`) | Form: workspace, directory, agent preset, an optional initial model, request id. The host reuses the workspace's first eligible blank session only after taking its lease and re-checking it; held by another process, it creates a new one. The session runs the requested preset. `HX-Location` to it. |
| | `GET /sessions/{sid}` (`session.open`), `GET /sessions/{sid}/events?before={seq}` (`session.history`; `at=` and `around=` give the window that holds one `seq`) | Page: the tail window (at least 50 messages across two turns, at most 500) and the state regions, read at one cut, with `#stream-cursor` at that cut. Older pages are turn-aligned. Access: `S`. |
| | `GET /sessions/{sid}/live` (`session.follow`) | Stream. Access: `S`. |
| | `POST /sessions/{sid}/prompt` (`session.prompt`) | Multipart: text, attachment references, mode (follow-up or steer), request id. `MayResume`. Empty content is refused, except `continue=1`, which admits an empty message with source `retry` when the last turn failed with an eligible code, the agent is idle and nothing human is pending. |
| | `POST /sessions/{sid}/cancel` (`session.cancel`); `PUT`, `DELETE /sessions/{sid}/inbox/{mid}` (`session.queue`) | `LiveOnly`; `MayResume`. `{mid}` is opaque; `PUT` edits a queued message or moves it to steering. Prompt and cancel on a subagent's session are routed through `subagent.Runtime` (A12). |
| | `PUT /sessions/{sid}/title`, `PUT /sessions/{sid}/model` (`session.rename`, `session.model`); `POST /sessions/{sid}/fork` (`session.fork`), `DELETE /sessions/{sid}` (`session.delete`) | Title and model: `MayResume`; a model must be in the catalog area A04 offers, and nothing substitutes another. Fork at a `seq` (default: the last completed turn); the host assigns the title. A fork starts with no session grant, whatever its source held ([DH-ADR-16](../design.md#dh-adr-16)). Delete is owner only. |
| | `POST /sessions/{sid}/uploads` (`session.upload`), `GET /sessions/{sid}/attachments/{ref}` (`session.attachment`); `GET /sessions/{sid}/completions/{kind}?q=` (`session.complete`) | Upload stores through area A07, adds the reference to the caller's draft and returns chips. The download requires that the session's log, or the caller's draft, references `{ref}` (`blob.Store.ReferencedBy`); byte routes take a download disposition and offer a PNG variant of an image. Completions give candidates for the composer (`{kind}` is the trigger: `skill`, `file`, `session` or `command`; a session is resolved by A14 under the caller's principal) and never wake a session. |
| Workspaces | `GET`, `POST /workspaces`; `PUT /workspaces/{wid}/title`; `DELETE /workspaces/{wid}`; `PUT /workspaces/order`; `PUT /workspaces/{wid}/sessions/order` | Over `workspace.Registry` (area A07). Session ids in an order form are typed fields and are checked. `POST /workspaces/default` creates the first-use workspace when A07's rule allows. |
| | `POST`, `DELETE /sessions/{sid}/archive`; `PUT`, `DELETE /sessions/{sid}/pin`; `GET /directories?path=` (`directory.browse`) | Archive of a session with running work answers 409 with that work, or stops it first when `stop=1`. The directory route is the server-rendered directory picker that replaces a native dialog ([DH-ADR-18](../design.md#dh-adr-18)): it renders what A18's `dirbrowse.Key` returns and lists nothing itself. Owner. |
| Jobs | `GET /sessions/{sid}/jobs` (`job.list`); `GET /sessions/{sid}/jobs/{jid}?from={offset}` (`job.output`); `POST /sessions/{sid}/jobs/{jid}/kill` (`job.kill`) | Roster and output tail are interests (`jobs`, `job-output`), over the registry's `List`, `Subscribe`, `ReadAt` and `Kill`; a row of a background workflow run carries its job id. Output before the tail is paged by offset. The kill is recorded as the user's, so the agent still gets its notice. Access: `S`; `{jid}` must be a job that session can see. |
| Settings | `GET /settings`, `GET`, `PUT /settings/{entry}` (`settings.read`, `settings.write`) | Forms from `settings.Forms` (area A07). A write carries the revision it was rendered at and goes through `config.Service.Apply` ([boot.md §6.2](boot.md)); a stale one is 409. `GET /settings/credentials`; `PUT`, `DELETE /settings/credentials/{ref}`: presence and a masked preview only; no route returns a secret value. All owner. |
| Terminals | `GET`, `POST /sessions/{sid}/terminals`; `GET /sessions/{sid}/terminals/shells`; `PUT /sessions/{sid}/terminals/{tid}/title`; `DELETE /sessions/{sid}/terminals/{tid}` | Roster is an interest. Create is idempotent on a terminal id the host rendered into the form; a closed id cannot be created again. Close waits for process cleanup and reports a failure with a retry. `GET /sessions/{sid}/terminals/{tid}/socket` (`terminal.attach`) is the one WebSocket; "take control" in another window is a new attachment, which then owns input and resize. All owner. |
| Files | `GET /sessions/{sid}/files/tree?dir=`, `/text?path=&line=`, `/stat?path=`, `/search?q=` | Fragments over `wsfiles`. Tree and preview are interests. A tree entry carries a `hidden` flag, `tree` takes a paging cursor, a listing that reached the cap is in the operating system's directory order, so the page sorts what it received and says "more entries not shown", and `search` takes literal exclusions and the "include hidden" and "use ignore files" choices. `GET /sessions/{sid}/files/raw?path=` (`file.raw`) is a download with `Range` and replaces both `readBytes` and `GET /api/file`. Access: `S`. |
| Others | `POST /interactions/{iid}` (A17), the session export (A06), the model picker (`model.picker`, area C3), `grant.list` and `grant.revoke` (area C4), `POST /directories` (`directory.create`, owner; area C6 over A18's `dirbrowse`), every page of the UI areas | Registered by their owners under the same rules. The interactions route accepts A17's four kinds and the `plan-review` intent; "allow for this session" is offered on the `ui` and `cli` channels only. |

**The terminal socket.** Binary messages, one type byte then the payload. Down: snapshot `{seq, cols, rows, bytes}`, output `{seq, bytes}`, state `{writable, title, exited}`. Up: acknowledge `{seq}`, input `{typed, bytes}`, resize `{cols, rows}`. It is the stream of area A09's `Attach` ([execution.md §6.2](execution.md)), framed with [libs/ws.md](../libs/ws.md): a new socket always starts with a snapshot (a terminal is state, not a journal), a viewer that stops acknowledging is dropped and re-attaches, a wrong or oversized message closes the socket with a standard code, and the socket closes with code 1001 when the terminal, the peer or the fiber goes away. An open socket is a hold.

**Events.** No host event is forwarded to a browser. Each upstream event becomes a reason to replace a region.

| Upstream unit | Go replacement | Form |
|---|---|---|
| `UP-EVT-api-session/added`, `UP-EVT-api-session/removed`; `UP-EVT-api-session/status` | `shell` rows re-rendered on `agent.Created` and `agent.Disposed` (A02) and on `workspace.Changed` (A07); `shell` running mark on `agent.StatusChanged` (A02) | state |
| `UP-EVT-api-session/activity` | `shell` row order on `projection.Changed` of the list summary (A06) | state |
| `UP-EVT-api-session/error` | `notice` on `agent.Failed` (A02) | notice |
| (new) `access.Changed` | live, notify: principal or all; the hub re-checks its connections | n/a |

**Config.** Go structs with defaults as Go values; unknown keys fail the load ([DH-ADR-SCHEMA-3](../libs/zod.md#dh-adr-schema-3)). Numbers marked * are unmeasured starting points.

| Go struct | Fields and defaults | Replaces |
|---|---|---|
| `stream.Config`, `web.Config` | `Heartbeat` 15 s; `WriteTimeout` 10 s; `MaxAge` 30 min; `ReplayLimit` 2,000 events*; `PageEvents` 100*; `StreamsPerPeer` 4; `InterestsPerTab` 128; `TabGrace` 2 min; `ReconnectBurst` 10 in `ReconnectWindow` 10 s*; `ReconnectGiveUp` 8* (sent to the stream script); `LiveMinInterval` 50 ms* (the least time between two `live` frames of one tab). `MaxFormBytes` 1 MiB; `HandlerTimeout` 60 s | `UP-CFG-dsh-api-gateway` (`websocketHeartbeatIntervalMs`, `streamInboxBytes`, `streamOutboxBytes`, `websocketCompression` have no counterpart) |
| `conversation.Config` | `PageMinMessages` 50; `PageMinTurns` 2; `PageMaxMessages` 500; `JumpPageMessages` 200 | `UP-CFG-dsh-api-session-controller` (`nativeOpen` and `listWorkSliceMs` are gone) |
| `routes/jobs.Config`; none for settings | `OutputFlush` 100 ms; `MaxFrameBytes` 65,536. The settings routes have no setting; the batch bound is gone | `UP-CFG-dsh-api-job-controller`; `UP-CFG-dsh-api-settings-controller` (no counterpart) |
| `userterm.Config` | `Shell`; `ShellCandidates`; `MaxTerminals` 8; `ExitedKept` 4; `MaxCols` 500; `MaxRows` 200; `MaxInputBytes` 65,536; `MaxBufferedBytes` 2 MiB; `UnattendedTimeout` 2 h (0 disables); `ActivityPoll` 30 s; `CleanupRetry` 60 s | `UP-CFG-dsh-api-terminal-controller` (`scrollback`, `disposeGraceMs` and `maxAgentTerminals` belong to A09) |
| `routes/workspaces.Config` | `DocumentsDirectory`; `DocumentsLookupTimeout` 10 s | `UP-CFG-dsh-api-workspace-controller` |
| `wsfiles.Config` | `MaxBytes` 2 MiB; `MaxFileBytes` 32 MiB; `MaxLines` 5,000; `MaxEntries` 2,000; `Search{MaxResults 512, MaxEntries 100,000, Timeout 30 s}`; `PollInterval` 2 s | `UP-CFG-dsh-api-workspace-files` |

**Services.**

| Upstream unit | Go replacement |
|---|---|
| `UP-SVC-typertGateway` | None: dropped ([API-1](#dh-adr-api-1)). `web.Key` registers routes; it dispatches no typed calls. |
| `UP-SVC-remoteAccess` | `access.Key`, interface `access.Service`. |
| `UP-SVC-sessionController` | `conversation.Key`, interface `Commands` (`Create`, `Prompt`, `Continue`, `Fork`); reads go to `query.Key` (A06). |
| `UP-SVC-sessionFileReferences`, `UP-SVC-sessionSkillCatalog` | Merged into the route `session.complete`, which calls A08's search and A14's skill catalog. |
| `UP-SVC-jobController`; `UP-SVC-settingsController`, `UP-SVC-credentialsController` | Merged into `routes/jobs` over A13's job registry; merged into `routes/settings` over `settings.Key` (A07) and A17's credential store. |
| `UP-SVC-terminalController` | `userterm.Key` for the person's terminals; agent terminals are `terminal.Key` (A09). |
| `UP-SVC-workspaceController`, `UP-SVC-directoryPickerController` | Merged into `routes/workspaces` over `workspace.Key` (A07) and the filesystem seam. |
| `UP-SVC-workspaceFiles` | `wsfiles.Key`, interface `Service` (`Stat`, `Text`, `Open`, `List`, `Search`, `Watch`). |

**Persisted data.** One fold, `continuation` (whether "continue" is offered), registered with area A06's registry. Tab records, interests, streams, terminals and holds are memory only. **Tools exposed to the model:** none.

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership and access.** A route, a stream source, a resource kind and a policy are effects of the fiber that registered them. A connection's goroutine and a tab record are owned by the hub's fiber. A user terminal is owned by its `userterm` record, keyed by session, and released when it is closed, reclaimed, its session is deleted or the component unloads; the process itself is owned by area A09's provider. A browser has access to a route through its principal and never owns anything on the host. A hold keeps a terminal from being reclaimed; it does not own it.

| Situation | What happens | Undone, retried, uncertain |
|---|---|---|
| A component that owns routes unloads or fails to load | Its routes answer 404; its in-flight handlers are cancelled through `rq.Context()`; the hub replaces its regions with an "unavailable" fragment. Other routes keep serving | Nothing to undo. A reload registers them again |
| A handler panics or a view fails | 500 with a request id; nothing partial is written; the process continues | The action may have taken effect; the page says "reload to see the current state" |
| One event or region cannot be rendered on a stream or in an opening window | A placeholder for that `seq`, or a "not current" marker for that region; the cursor moves on; the stream stays open | Nothing is skipped or repeated. The event itself is intact in the log and is drawn properly by a build whose renderer is fixed |
| A form is posted twice, or retried after a timeout | The second gets the first receipt | Nothing happens twice |
| A browser disconnects, is parked, or reads slowly | It resumes from its cursor; state regions are re-sent whole | Notices sent meanwhile are lost |
| The host restarts | Tabs reconnect with their cursor, get `reset` for regions and the journal after the cursor; jobs, terminals, holds and session grants of the old host are gone and their regions say so. A grant is never restored by a resume, a fork or a subagent: it ended with the live session it was given to | **Uncertain:** whether a tool call that was running took effect; the log's closing events say which ([design.md §5](../design.md#5-failure-retry-rollback-and-cleanup)) |
| Access is revoked while a stream is open | `access.Changed` makes the hub re-check; the stream ends or its baselines are re-sent for what remains | Fragments already shown stay on the screen until replaced |
| A policy panics or errs | Deny, with reason `policy-failed`, logged once per registration | Nothing |
| `MayResume` meets a lease held by another process | 409 `session/writer-held`; the page stays read-only and keeps following through `Follow` | The user retries when the other process ends |
| A terminal's cleanup fails | The record is kept, marked failed, and retried every `CleanupRetry`; the roster shows it | **Uncertain:** whether every process of the tree ended; area A09 reports leftovers |
| A terminal socket breaks | The island reconnects and gets a new snapshot; input typed while disconnected is not sent | A keystroke the host received may already have reached the program |
| A file changes while it is read | The answer is marked changed-during-read with the version it ended at | The view re-requests |
| A watch cannot be set up, or ends | Polling `Stat` for that interest; if that fails too, a stale marker | Nothing silent |

## 7. Decisions

<a id="dh-adr-api-1"></a>
### DH-ADR-API-1 The gateway's machinery is dropped; its guarantees are restated as rules of the route and the stream
- **Decision.** Not built: the typed remote-call gateway, the Typert type graph and generated clients, the multiplexed socket with logical streams and uplinks, forwarded host events, connection generations, the client journal and its gap repair, the injected access snapshot. Still required, and where each now lives: only declared operations are callable (the route registry); input is validated before a handler runs (strict form decoding); every caller is identified and checked (admission, then the access rule); cancellation reaches the work (`rq.Context()`); every durable event is shown once and in order after a reconnect or a host restart (the journal class of the stream); nothing falls between a baseline and live updates (connect order); a slow or dead client costs bounded memory (pull and slots).
- **Why.** Each dropped part exists because the browser was a second program with its own state ([upstream/wire-and-api.md §10](../upstream/wire-and-api.md)). The guarantees are about users, not about JavaScript.
- **Tradeoff.** No typed browser API for third parties; a program uses the SDK protocol or ACP. Every interaction is a round trip.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-API-3, DH-T-API-12, DH-T-API-13, DH-E-API-1.

<a id="dh-adr-api-2"></a>
### DH-ADR-API-2 A route is a declared effect of the component that owns its feature
- **Decision.** A route exists only while the component that registered it is loaded. Its declaration is complete or it does not register: name, pattern, access rule, activation, kind. Stream sources, resource kinds and policies follow the same rule.
- **Why.** Upstream fixes the capability set at build time and states activation in prose. In cordis, a registration that is an effect cannot outlive its owner ([DH-O-1](../design.md#dh-o-1)), and a field can be listed, tested and shown in a catalog.
- **Tradeoff.** The route table changes at run time, so it is rebuilt and swapped on each change, and a page can hold a link to a route that has gone; that link answers 404 with a plain message.
- **Serves.** [DH-O-1](../design.md#dh-o-1), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-API-1, DH-T-API-2, DH-T-API-20, DH-E-API-5.

<a id="dh-adr-api-3"></a>
### DH-ADR-API-3 Resource ids live in the URL path, one writer draws each region, and every submit is idempotent
- **Decision.** The three rules of [§6.2](#62-contracts): an access-controlled id is a path segment bound by the declaration, and a handler can read only what was declared; a durable event is drawn only by the stream; a state-changing form carries a host-minted request id that makes a repeat harmless, durably for prompts. One boundary is recorded here because the route area owns the `continuation` fold: "Continue" is offered after a fixed list of six failure codes (upstream's, `packages/api/session-controller/src/continuation.ts:9-10`), independent of a provider's retry settings; a new code joins the list by a change to this specification, not by configuration (recommended default, not confirmed by the owner).
- **Why.** Upstream finds resources by field name and leaves unnamed ones unchecked. A browser-side model decides what to draw; here two writers for one region would draw an event twice. A person double-clicks, and htmx retries.
- **Tradeoff.** Longer URLs. A form answer shows less than the user expects until the stream delivers the change, which on a remote link is visible as a short wait.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-3](../design.md#dh-o-3), [DH-O-7](../design.md#dh-o-7). **Verified by.** DH-T-API-1, DH-T-API-4, DH-T-API-7, DH-T-API-19.

<a id="dh-adr-api-4"></a>
### DH-ADR-API-4 One hub, three recovery classes; the journal is pulled by cursor, state is replaced whole, a tab's interests select what it gets
- **Decision.** As [§6.2](#62-contracts) states: a source declares journal, state or notice; the session journal resumes by durable `seq` through the store, whoever holds the lease; state restarts from a baseline and coalesces per region; a view subscribes by registering an interest from an ordinary request; an unknown tab or cursor gets `reset`; the stream opens only after boot is ready. This applies [DH-ADR-WIRE-3](../upstream/wire-and-api.md#dh-adr-wire-3) and [DH-ADR-GOUISTACK-4](../libs/go-ui-stack.md#dh-adr-gouistack-4) and adds the tab, the interest and the `view` and `notice` names.
- **Why.** Seven UI areas need live regions; without one rule each would add a stream, and a browser has six connections. Pulling the journal means a slow tab cannot grow host memory, and reading it from the store means resume does not depend on anything the old host process knew.
- **Tradeoff.** The host keeps a small record per tab. A region that is not re-requested after `reset` goes stale, so every live region must be able to fetch itself. A long gap costs a page reload of regions, not a replay.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-API-11 to DH-T-API-17, DH-E-API-1.

<a id="dh-adr-api-5"></a>
### DH-ADR-API-5 Access: the owner is unrestricted, a non-owner needs a binding on every resource, and one argument-free function both renders and enforces
- **Decision.** Keep upstream's model (roles, bindings, one policy per principal kind, resources independent, fail closed on a faulty policy). Change: declarations are mandatory; a decision reads the principal, the action, the resource and the time, never request arguments; the host calls the same `Allows` to decide whether to draw a control and whether to run its route; a list route must apply the filter it is given; an access change is an event that re-checks open streams. No principal kind other than `owner` ships in the base product; area X2 registers the viewer of a live share, whose role holds read actions only, because the owner decided that viewers only watch ([DH-ADR-37](../design.md#dh-adr-37)).
- **Why.** Upstream's snapshot can show a call as allowed that a condition then refuses, and its event stream notices a revocation only on the next unrelated change. [DH-ADR-SECURITY-8](../upstream/security.md#dh-adr-security-8) asks for declarations that cannot be skipped.
- **Tradeoff.** A policy cannot depend on what a request says, only on what it is addressed to. The owner's session content still contains the owner's absolute paths; sharing a session shares them.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-API-3, DH-T-API-8, DH-T-API-9, DH-T-API-10.

<a id="dh-adr-api-6"></a>
### DH-ADR-API-6 A browser on another machine is admitted as the owner through the same token exchange; everything else is refused
- **Decision.** The UI listener serves browsers only. **Admitted:** a browser that presents a launch token to a listener the operator bound to one concrete address, over TLS (or with the operator's explicit insecure flag), under a `Host` name on the allow-list; it becomes the owner. A port forwarded to loopback (for example by SSH) needs none of this and is the recommended way. **Refused:** a wildcard bind and a non-loopback bind without TLS or the flag (start-up errors); a `Host` not on the list; a cross-origin or cross-site request; a request without a valid cookie; any non-owner principal without a policy; a script that wants to call UI routes (it uses the SDK protocol or ACP: there is no `/connect` endpoint and no bearer token). A caller that holds no event stream is never an answerer of approvals.
- **Why.** [DH-ADR-SECURITY-7](../upstream/security.md#dh-adr-security-7) and [DH-ADR-WIRE-10](../upstream/wire-and-api.md#dh-adr-wire-10) fix the checks; upstream's separate API peer exists for callers that the Go design moves to stdio protocols.
- **Tradeoff.** Whoever holds a launch URL is the owner. Over plain HTTP from another machine the browser grants no notifications ([DH-ADR-GOUISTACK-9](../libs/go-ui-stack.md#dh-adr-gouistack-9)). Area A18 implements the admission; this record states what the surface relies on.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-API-3, DH-E-API-2.

<a id="dh-adr-api-7"></a>
### DH-ADR-API-7 A person's terminal runs as the person, is owned by a session, and is the only WebSocket
- **Decision.** A user terminal is started through area A09's terminal service with a trusted (unconfined) launch and the scrubbed environment, in the session's current directory. It needs no approval, produces no session event, and keeps running when the agent's sandbox mode changes. It is reachable only by the owner. One socket per terminal carries a snapshot and then acknowledged output down and input up. A terminal with no hold is reclaimed after `UnattendedTimeout` of *confirmed* idleness; busy or unknown activity restarts the clock. Agent terminals stay in A09 and appear in the same roster.
- **Why.** The upstream notes give the reasons, and they do not depend on the language. A WebSocket is kept only where bytes flow up at keystroke rate ([DH-ADR-WIRE-2](../upstream/wire-and-api.md#dh-adr-wire-2)).
- **Tradeoff.** Access to the UI is command execution as the operating-system user, which is why the routes are owner only. A shell whose activity cannot be observed is never reclaimed automatically.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-API-23, DH-E-API-3.

<a id="dh-adr-api-8"></a>
### DH-ADR-API-8 File and attachment bytes are plain HTTP, addressed through a session, and bounded
- **Decision.** Every file route names a session. Reads of a named file follow that session's filesystem seam, including paths outside the workspace when the backend allows them; listing, search and watching stay inside the workspace. Bytes are an ordinary response with `Range`, a version `ETag`, `nosniff` and a sandboxing policy. An attachment is served only if the session's log references it, checked by one lookup of the reference rows area A07 keeps. Watches carry invalidation; the region is fetched again through the bounded readers.
- **Why.** One read policy, owned by the filesystem; no base64 and no codec; and a membership check that costs one row, which removes upstream's reason for making the whole image store readable.
- **Tradeoff.** A session's file views can show any file the user can read; this is the security model's stated limit ([design.md §6](../design.md#6-security-model)). A file opened directly in the browser relies on the sandboxing header.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-7](../design.md#dh-o-7). **Verified by.** DH-T-API-24, DH-E-API-4.

<a id="dh-adr-api-9"></a>
### DH-ADR-API-9 `web` writes one content security policy and refuses every state-changing request that is not same-origin with a form token
- **Decision.** (a) **The policy header.** `web` is the only writer of `Content-Security-Policy` and `Permissions-Policy`. The base policy is the directive list of [DH-ADR-GOUISTACK-3](../libs/go-ui-stack.md#dh-adr-gouistack-3), supplied by area C1 as the value `ui.BasePolicy`; `web` adds to `script-src` one `'sha256-…'` source for each block registered through `AllowInlineData`, computing the hash from the registered text, and the shell writes exactly the registered blocks into the page head. Only a block of type `importmap` (data that maps module names to same-origin files) can be registered; executable inline script, inline event handlers, `'unsafe-inline'`, `'unsafe-eval'`, `'unsafe-hashes'`, a nonce and a remote source are never in a page's policy, and a registration that needs one fails. (b) **Islands.** An island runs under the page policy. One whose code needs more is served as a `Frame` route whose `Policy` may add only entries of a closed list (`style-src 'unsafe-inline'`, `worker-src blob:`, `script-src 'wasm-unsafe-eval'`), always with `frame-ancestors 'self'`; every other document has `frame-ancestors 'none'`. (c) **Forgery.** A request that is not `GET` or `HEAD`, and every socket upgrade, must be same-origin: `Sec-Fetch-Site: same-origin` when the browser sends it, otherwise an `Origin` equal to one of the listener's own origins; with neither it is refused. A request with a body must also carry the peer's form token, which is A18's `Peer.FormToken()` (an HMAC of the peer's id under a random key kept with that browser session's row; the host has no signing secret, [host-telemetry.md](host-telemetry.md) `DH-ADR-HOST-12`), in the header `X-DSH-Token` (set for htmx by an `hx-headers` attribute on `<body>`) or in the hidden field `_token` that `web`'s form helper writes for a plain form post. `GET` never changes state ([§6.2](#62-contracts)).
- **Why.** [`DH-LIB-GOUISTACK-R4`](../libs/go-ui-stack.md) requires the header on every HTML answer and no specification wrote it; area C3 needs one import map for the composer island ([ui-chat.md](ui-chat.md), `DH-ADR-UICHAT-3`). A fixed hash of a data block needs no per-response value, so the reason the library gives against a nonce still holds. `web` sees every response and owns the registry of what may be inline, so it composes and writes the value; the shell supplies the base and makes every page fit it. For forgery, the cookie is `SameSite=Strict`, but "site" ignores the port: a page served by any other program on `localhost`, including a development server the agent itself started, is same-site, and its form post would carry the cookie. Same-origin plus a token that such a page cannot read closes that; either alone leaves a gap (an old browser without `Sec-Fetch-Site`, a proxy that drops `Origin`).
- **Tradeoff.** The policy is "no inline script except hashed import maps", which the UI-stack specification must adopt. A page open across a cookie change must be reloaded once. A script-less form works because the token is also a field. A policy change (a component with an import map is enabled) reaches a tab only at its next full load.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-API-25, DH-E-API-2.

<a id="dh-adr-api-10"></a>
### DH-ADR-API-10 A render failure is drawn as a placeholder, and reconnects are bounded on both sides
- **Decision.** The rule "an item that cannot be drawn" of [§6.2](#62-contracts): per event, the renderer's own fallback row (or, if the renderer itself fails, the hub's constant placeholder) takes the event's place, the cursor advances and the stream stays open; per region, a marker; the opening window of a page does the same, so one bad event cannot make a session unopenable. A stream is closed for a failure only when the store cannot be read. Independently, the host limits how fast one tab may reopen a stream, and C1's stream script backs off, then stops and shows `stalled` after `ReconnectGiveUp` attempts without an event.
- **Why.** The first text ended the stream on a render failure "so the error is seen once on resume". A renderer is a pure function of the log, so the resume fails at the same `seq`: the tab reconnects for ever and shows nothing after that event. [DH-O-12](../design.md#dh-o-12) asks for every event once and in order, which a placeholder satisfies and a loop does not.
- **Tradeoff.** A user may see "this event could not be shown" where a build has a renderer defect; the log and the export are unaffected. [`DH-LIB-GOUISTACK-R13`](../libs/go-ui-stack.md) (a failing view writes nothing) still holds for a whole response; the placeholder is a different, constant view.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-API-26, DH-E-API-1.

## 8. Deliverables

<a id="dh-d-api-1"></a>
### DH-D-API-1 Every UI route is a declared effect that leaves with its component
- **Result and why it matters.** A UI area adds a page by registering a declaration; disabling a feature in configuration removes its URLs at once; a catalog lists every route with its access rule.
- **How it is implemented.** `Routes.Handle` validates the declaration (rule present, every path parameter bound or opaque, typed id fields tagged, `List` kind uses the filter), then adds it to a copy of the table and swaps the table atomically; the effect's cleanup removes it and cancels its handlers. Dispatch is the standard library's pattern matching over the current table.
- **Components and files.** `internal/host/web`. **Depends on.** [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1), [DH-D-CORDIS-2](../libs/cordis.md#dh-d-cordis-2), [DH-D-HOST-1](host-telemetry.md#dh-d-host-1) (the listener calls this table through `listen.Mount` after admission and puts the peer in the request context). **Priority.** P0. **Outcome.** [DH-O-1](../design.md#dh-o-1), [DH-O-12](../design.md#dh-o-12).
- **Done when.** DH-T-API-1, DH-T-API-2 and DH-T-API-3 pass and [DH-E-API-5](#dh-e-api-5) has passed.

<a id="dh-d-api-2"></a>
### DH-D-API-2 One way to answer: pages, fragments, forms and errors
- **Result and why it matters.** A user sees the same behaviour on every screen: a link works with or without htmx, a bad field is marked beside the field, an error names a code and never shows internals, a double submit does nothing twice.
- **How it is implemented.** The closed `Response` set rendered by `web` according to [§6.2](#62-contracts); strict decoding through the `schema` package; the Problem type and the status table; request-id handling shared by all `Action` routes; the header writer, the policy composer with `AllowInlineData` and `AllowFeature`, and the forgery check with the form token of [DH-ADR-API-9](#dh-adr-api-9).
- **Components and files.** `internal/host/web`. **Depends on.** [DH-D-API-1](#dh-d-api-1), [DH-D-UTIL-1](util-test-support.md#dh-d-util-1) (`schema` decoding of form bodies), [DH-D-HOST-2](host-telemetry.md#dh-d-host-2) (the peer and `Peer.FormToken()`). The layout, the problem view, `ui.BasePolicy`, the token on `<body>` and `htmx-config` are [DH-D-UICORE-1](ui-core.md#dh-d-uicore-1), which builds on this deliverable. **Priority.** P0. **Outcome.** [DH-O-12](../design.md#dh-o-12).
- **Done when.** DH-T-API-4, DH-T-API-5, DH-T-API-6, DH-T-API-7 and DH-T-API-25 pass and [DH-E-API-2](#dh-e-api-2) has passed.

<a id="dh-d-api-3"></a>
### DH-D-API-3 Access is decided for every route and every stream, and only the owner path is open at first
- **Result and why it matters.** No route can be reached without a decision: a route with no rule cannot register, the owner is admitted, and any other principal is refused until a policy for its kind exists. A browser on another machine is admitted as the owner only by the stated path.
- **How it is implemented.** The `access` service of [§6.1](#61-shape) without registered policies: declarations are checked at registration, the owner path is a constant, `Decide` denies every non-owner principal (no policy, no binding), and a `List` route that does not apply `rq.Filter()` fails registration. `Filter` already wraps `Allows`, so a list is correct for the owner and empty for anyone else.
- **Components and files.** `internal/host/access`. **Depends on.** [DH-D-API-1](#dh-d-api-1), [DH-D-HOST-2](host-telemetry.md#dh-d-host-2) (peer and principal on the request). Its filter feeds the visibility of [DH-D-VIEW-5](session-views.md#dh-d-view-5). **Priority.** P0. **Outcome.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12).
- **Done when.** DH-T-API-3 and DH-T-API-9 pass and [DH-E-API-2](#dh-e-api-2) has passed.

<a id="dh-d-api-10"></a>
### DH-D-API-10 A non-owner principal sees only what a policy grants, and a revocation reaches open pages
- **Result and why it matters.** A shared session exposes that session and nothing else, and revoking access takes effect on pages already open. This is what area X2's viewer of a live share needs ([DH-ADR-37](../design.md#dh-adr-37)); it was the P1 part of [DH-D-API-3](#dh-d-api-3) and has its own priority here.
- **How it is implemented.** `RegisterKind` and `RegisterPolicy` as effects; the non-owner path evaluates bindings per decision, never cached across decisions, and denies on any error or panic (`policy-failed`); `Invalidate` raises `access.Changed`, on which the hub re-checks open streams and re-sends baselines without the revoked rows.
- **Components and files.** `internal/host/access`, the re-check in `internal/host/stream`. **Depends on.** [DH-D-API-3](#dh-d-api-3), [DH-D-API-4](#dh-d-api-4). The first policy is area X2's. **Priority.** P1. **Outcome.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12).
- **Done when.** DH-T-API-8, DH-T-API-9 and DH-T-API-10 pass.

<a id="dh-d-api-4"></a>
### DH-D-API-4 An open page shows every event once and in order, and stays live without blocking anything
- **Result and why it matters.** This is the visible half of [DH-O-12](../design.md#dh-o-12): after a network cut, a parked tab or a host restart the transcript has no gap and no duplicate, state regions are current, and a slow tab costs only itself.
- **How it is implemented.** The hub of [§6.2](#62-contracts): one goroutine per connection that selects between the store's follow signal, dirty state slots, the heartbeat and its context; the tab table; the per-peer counter; the attention stream fed by area A17's pending interactions and by turn ends.
- **Components and files.** `internal/host/stream`. **Depends on.** [DH-D-API-1](#dh-d-api-1), [DH-D-API-3](#dh-d-api-3), [DH-D-STORE-8](session-store.md#dh-d-store-8) (`Follow`), [DH-D-AGENT-1](agent-loop.md#dh-d-agent-1) (`agent.StreamFrame`, status), [DH-D-VIEW-1](session-views.md#dh-d-view-1) (`projection.Changed`), [DH-D-HUMAN-1](interaction-credentials.md#dh-d-human-1) (pending interactions). Its renderer is supplied by [DH-D-UICONV-1](ui-conversation.md#dh-d-uiconv-1) and its browser half is the stream script of [DH-D-UICORE-3](ui-core.md#dh-d-uicore-3). **Priority.** P0. **Outcome.** [DH-O-12](../design.md#dh-o-12).
- **Done when.** DH-T-API-11, DH-T-API-12, DH-T-API-13, DH-T-API-14, DH-T-API-15, DH-T-API-16, DH-T-API-17 and DH-T-API-26 pass and [DH-E-API-1](#dh-e-api-1) has passed.

<a id="dh-d-api-5"></a>
### DH-D-API-5 A person can start, read, prompt, steer, cancel, continue and fork a conversation
- **Result and why it matters.** The first working product: the session routes of [§6.2](#62-contracts), with the rule that looking never wakes a session and one accepted message is at most one turn.
- **How it is implemented.** `conversation.Commands` holds what needs ordering: create (pick a blank, take the lease, re-check, select the preset), prompt (offer a command line to A17, resolve attachment references, send with the request id as message id and answer a duplicate with its first receipt), continue (the `continuation` fold and the six-code list), fork (cut, host-assigned title, serialized per source). Reads use area A06's `Query` at one cut. The blank flag turns false at the first inbox insertion.
- **Components and files.** `internal/host/conversation`, `internal/host/routes/sessions`. **Depends on.** [DH-D-API-2](#dh-d-api-2), [DH-D-API-4](#dh-d-api-4), [DH-D-AGENT-2](agent-loop.md#dh-d-agent-2) (`Send` with a caller-chosen message id that is logged with the insertion), [DH-D-AGENT-6](agent-loop.md#dh-d-agent-6) (create, resume), [DH-D-AGENT-5](agent-loop.md#dh-d-agent-5) (cancel), [DH-D-STORE-3](session-store.md#dh-d-store-3) (lease `IfFree`), [DH-D-STORE-6](session-store.md#dh-d-store-6) (delete), [DH-D-VIEW-3](session-views.md#dh-d-view-3) and [DH-D-VIEW-5](session-views.md#dh-d-view-5) (list summary, query), [DH-D-VIEW-4](session-views.md#dh-d-view-4) (titles), [DH-D-STORAGE-3](storage.md#dh-d-storage-3) (attachments), [DH-D-LLM-4](llm.md#dh-d-llm-4) (model catalogue), [DH-D-CONTEXT-9](context-compaction-skill.md#dh-d-context-9) (skill catalogue). Its views are areas C3's and C4's. **Priority.** P0. **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-3](../design.md#dh-o-3), [DH-O-5](../design.md#dh-o-5).
- **Done when.** DH-T-API-7, DH-T-API-18, DH-T-API-19 and DH-T-API-20 pass and [DH-E-API-1](#dh-e-api-1) has passed.

<a id="dh-d-api-6"></a>
### DH-D-API-6 A person can organise projects and pick a directory in the browser
- **Result and why it matters.** The sidebar's workspaces, order, archive and pin work from any tab, and a directory can be chosen without a native dialog.
- **How it is implemented.** Handlers over `workspace.Registry`; the `shell` source re-renders on `workspace.Changed`; the picker lists directories through the host filesystem with the entry cap.
- **Components and files.** `internal/host/routes/workspaces`. **Depends on.** [DH-D-API-2](#dh-d-api-2), [DH-D-API-4](#dh-d-api-4), [DH-D-STORAGE-7](storage.md#dh-d-storage-7) (registry, archive rule), [DH-D-HOST-8](host-telemetry.md#dh-d-host-8) (the directory listing). Its views are area C6's. **Priority.** P1. **Outcome.** [DH-O-12](../design.md#dh-o-12).
- **Done when.** DH-T-API-21 passes.

<a id="dh-d-api-7"></a>
### DH-D-API-7 A person can watch and stop background jobs, and edit settings and credentials
- **Result and why it matters.** Long commands are visible and stoppable by the person; configuration is changed in a form and a secret never leaves the host.
- **How it is implemented.** Job routes and two interests over area A13's registry, with chunks labelled by channel and coalesced every `OutputFlush`. Settings routes over `settings.Forms` and the credential store; `settings.FormChanged` replaces an open form's region.
- **Components and files.** `internal/host/routes/jobs`, `internal/host/routes/settings`. **Depends on.** [DH-D-API-2](#dh-d-api-2), [DH-D-API-4](#dh-d-api-4), [DH-D-JOBS-1](jobs-schedule-goal-plan.md#dh-d-jobs-1) (visible set, change notice, read at offset, kill with a reason), [DH-D-STORAGE-8](storage.md#dh-d-storage-8) (forms, written through `config.Service.Apply`, [boot.md](boot.md) `DH-ADR-BOOT-14`), [DH-D-HUMAN-7](interaction-credentials.md#dh-d-human-7) (credentials). Its views are areas C5's and C7's. **Priority.** P1. **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7).
- **Done when.** DH-T-API-22 passes.

<a id="dh-d-api-8"></a>
### DH-D-API-8 A person has terminals in the browser that survive a reload and are cleaned up
- **Result and why it matters.** Manual work beside the agent, without widening the agent's sandbox, and without shells that live for ever.
- **How it is implemented.** `userterm.Service` keeps one record per terminal under a session: publish after the backend is set up, closed ids remembered, the reclamation clock driven by holds and by A09's activity report. The socket route frames `Attach` with the wrapper of [libs/ws.md](../libs/ws.md).
- **Components and files.** `internal/host/userterm`, `internal/host/routes/terminals`, and `internal/host/wsconn`, the socket wrapper and the only importer of `github.com/coder/websocket` ([libs/ws.md](../libs/ws.md)). **Depends on.** [DH-D-API-1](#dh-d-api-1), [DH-D-API-3](#dh-d-api-3), [DH-D-EXEC-7](execution.md#dh-d-exec-7) (owner-scoped terminal sessions: a person as owner, trusted launch, activity, hand-over of an agent terminal on typed input), [DH-D-VIEW-10](session-views.md#dh-d-view-10) (current directory). The island and its frame are [DH-D-UISIDE-7](ui-sidebar-workspace.md#dh-d-uiside-7). **Priority.** P1. **Outcome.** [DH-O-10](../design.md#dh-o-10), [DH-O-12](../design.md#dh-o-12).
- **Done when.** DH-T-API-23, `DH-T-WS-1`, `DH-T-WS-2`, `DH-T-WS-3`, `DH-T-WS-4` and `DH-T-WS-5` pass and [DH-E-API-3](#dh-e-api-3) has passed.

<a id="dh-d-api-9"></a>
### DH-D-API-9 A person can browse, preview, search and download the files a session can read
- **Result and why it matters.** The Files tree and previews stay current when an editor or a shell changes a file, on a local or a remote execution world, within fixed bounds.
- **How it is implemented.** `wsfiles.Service` over the filesystem seam of the session's world: regular-file and symlink checks, a line cutter that counts and segments, metadata from the open handle, a listing that asks the seam's `ReadDir` for `MaxEntries` plus one and never more (debt entry U-K2-1: a huge directory is no longer read whole), search through area A08's helper, `Watch` with a `Stat` polling fallback per interest. The attachment route asks area A07 whether the session references the object.
- **Components and files.** `internal/host/wsfiles`, `internal/host/routes/files`. **Depends on.** [DH-D-API-2](#dh-d-api-2), [DH-D-API-4](#dh-d-api-4), [DH-D-FS-4](filesystem.md#dh-d-fs-4) (the seam, with a listing limit), [DH-D-FS-3](filesystem.md#dh-d-fs-3) (search), [DH-D-UTIL-3](util-test-support.md#dh-d-util-3) (`watch`), [DH-D-STORAGE-3](storage.md#dh-d-storage-3) (reference lookup), [DH-D-VIEW-10](session-views.md#dh-d-view-10) (directory of a cold session). Its views are area C6's. **Priority.** P1. **Outcome.** [DH-O-10](../design.md#dh-o-10), [DH-O-12](../design.md#dh-o-12).
- **Done when.** DH-T-API-24 passes and [DH-E-API-4](#dh-e-api-4) has passed.

## 9. Acceptance

### 9.1 Local tests

Run with `net/http/httptest`, fakes of the services of other areas and virtual time. Nothing has been run.

| Test ID | Proves | What is asserted |
|---|---|---|
| DH-T-API-1 | D-1 | A declaration with no rule, an unbound path parameter, an untagged id field, a duplicate pattern or a duplicate name fails registration; unloading the owner gives 404 and cancels an in-flight handler |
| DH-T-API-2 | D-1 | Through the real loader: disabling one route component removes its routes and topics, other routes keep serving, open streams receive the "unavailable" fragment |
| DH-T-API-3 | D-1, D-3 | Every registered route is called with no peer and as a non-owner with no binding; each is refused before its handler runs |
| DH-T-API-4 | D-2 | One fragment has identical bytes in a page and alone; `no-store` and `Vary`; an action answers 303 without `HX-Request` and 204, a region or `HX-Location` with it |
| DH-T-API-5 | D-2 | An unknown field is 400, an oversize body 413, a bad field 422 with the form re-rendered and the message beside the field |
| DH-T-API-6 | D-2 | Each error code maps to its documented status; a Problem holds a code and a request id and no internal text; a failing view writes zero bytes |
| DH-T-API-7 | D-2, D-5 | One request id posted twice, concurrently, after the message was claimed, and after the store was reopened: one inbox insertion, two equal answers |
| DH-T-API-8 | D-10 | Role and binding table; resources are independent; an expired binding denies; a panicking policy denies with `policy-failed`; `Allows` equals `Decide` |
| DH-T-API-9 | D-3, D-10 | A non-owner's list holds only admitted rows and no link to an unseen parent; a `List` route that ignores the filter fails registration |
| DH-T-API-10 | D-10 | After `Invalidate`, a stream on a revoked session ends and a shell stream is re-baselined without the revoked rows |
| DH-T-API-11 | D-4 | Framing of each event name, id, multi-line data and heartbeat; journal events are never merged out of order; a newer state value replaces a waiting one; notices may drop |
| DH-T-API-12 | D-4 | Cursor table: header, `after`, both, another session, beyond the head, beyond `ReplayLimit`; events committed during catch-up arrive once and in order; a session held by another process is followed |
| DH-T-API-13 | D-4 | A change made while a baseline is read is delivered after it; pending interactions are in the baseline; with no attempt in flight the live tail is sent empty |
| DH-T-API-14 | D-4 | A reader that stops is closed at the write deadline; no producer blocks; memory per connection stays under one page plus the slots; a stream is rotated at `MaxAge` and resumes |
| DH-T-API-15 | D-4 | One stream over the cap parks the longest-open session stream; the attention stream is never parked; a second attention stream closes the first |
| DH-T-API-16 | D-4 | `Watch` adds a region to the next baseline; the cap evicts the oldest with a marker; an unknown tab gets `reset`; a tab record expires after `TabGrace` |
| DH-T-API-17 | D-4 | A stream handler returns when its owner unloads, the client leaves or the peer is revoked; stream routes answer 503 before ready |
| DH-T-API-18 | D-5 | Create reuses a blank only under its lease and creates a new session when another holder has it; the requested preset runs; continue is admitted only for the six codes when idle with nothing pending; concurrent forks get distinct titles |
| DH-T-API-19 | D-5 | The opening window meets the minima and the cap; events committed while the page renders appear exactly once between page and stream |
| DH-T-API-20 | D-5 | Routes declared `None` create no agent (open, list, history, completions, files, terminal roster); `LiveOnly` answers 409 when idle; concurrent `MayResume` posts resume once |
| DH-T-API-21 | D-6 | Archive of a busy session answers 409 with its work, or stops it with `stop=1`; orders persist; the picker returns directories only and refuses a non-owner |
| DH-T-API-22 | D-7 | A roster holds only jobs the session can see; a kill carries the user's reason; output is read from an offset; a stale settings revision is 409; no response contains a stored secret |
| DH-T-API-23 | D-8 | Caps; create is idempotent by id and a closed id is refused; the reclamation clock under hold, busy, unknown and idle; snapshot then ordered output; a bad message closes with its code; the socket closes when the terminal, peer or fiber ends |
| DH-T-API-25 | D-2, ADR-9 | Every HTML answer of every registered route, and every error answer, carries the composed policy and the fixed headers, and no handler can set them; the policy holds exactly one hash per registered import map, computed from its text, and loses it when the owner unloads; registering any other script type, a feature other than `microphone`, or a frame addition outside the closed list, fails; a `Frame` carries its own policy with `frame-ancestors 'self'`; a non-`GET` request is refused with `web/forgery` when cross-origin, when same-site from another port, when it has neither `Sec-Fetch-Site` nor `Origin`, and when its token is missing, wrong or another peer's; a plain form post with the `_token` field passes; a `GET` route that changes state fails a static check; a cross-origin socket upgrade is refused |
| DH-T-API-26 | D-4, ADR-10 | With a renderer that returns its fallback row for one `seq`, and again with one that returns an error or panics for it: the stream writes the row (or the hub's placeholder) for that `seq`, the cursor passes it, later events follow in order, the stream stays open, a resume from before it draws the same placeholder once, and the page's opening window does the same; a failing region shows the marker and recovers at its next change; a failing live tail is dropped; the failure is logged once. A tab that reopens its stream past `ReconnectBurst` gets 429 with `Retry-After` |
| DH-T-API-24 | D-9 | Line pages, a giant line in segments, a line total, `not-text`, a refused final symlink, ranges and `ETag`, a read outside the workspace when the backend allows, a listing cut at the cap for which a counting fake provider was asked for no more than the cap plus one entries and which is drawn sorted with "more entries not shown", a watch that replaces a region, the polling fallback, the changed-during-read flag, an attachment refused without a reference |

### 9.2 Real-environment scenarios

Each needs a real browser driving the built binary; the replay model provider stands in for a model API. A scenario that is skipped, blocked or failed stays open. They prove the stated contracts for the tested build, browser and operating system.

<a id="dh-e-api-1"></a>
#### DH-E-API-1 A browser shows every event once across a network cut, a parked tab and a host restart
- **Proves.** [DH-D-API-4](#dh-d-api-4), [DH-D-API-5](#dh-d-api-5).
- **Given.** The built `dsh` in its web profile on loopback with a temporary harness home; a real Chromium; a replayed session whose turn produces text, two tool calls and one approval.
- **When.** (1) Create a session in the UI and submit the prompt twice by double-click. (2) Cut and restore the connection during the answer. (3) Open tabs until the first is parked, then show it again. (4) Approve the tool call. (5) Kill the host during the second tool call, start it again, and reload nothing.
- **Then.** The transcript in the first tab equals the session export: every `seq` once, in order; one user message for the double-click; the partial answer continues after (2); the parked tab says so and has no gap after (3); after (5) the page shows the turn closed as interrupted with the tool outcome marked unknown, and no stale partial text.
- **Cleanup.** Close the session, stop the host, delete the home and the browser profile. On failure keep the export, the host log and the browser's network log.

<a id="dh-e-api-2"></a>
#### DH-E-API-2 A browser on another machine is admitted only by the stated path
- **Proves.** [DH-D-API-3](#dh-d-api-3), [DH-D-API-2](#dh-d-api-2) (the forgery check and the policy header in a real browser).
- **Given.** Two machines on one network (or one machine with a second network address), a test certificate, the built binary, a real browser on the second machine.
- **When.** (1) Start `dsh` bound to the wildcard address. (2) Start it bound to the network address without TLS. (3) Start it with TLS and open the launch URL from the second machine. (4) From that browser, load a page of another origin that posts to the host, load a page served from another port of the host's own name that posts the same form and opens the terminal socket, and request the host by a name not on the allow-list. (5) Open the UI without a token in a fresh profile.
- **Then.** (1) and (2) fail at start-up with a message naming the cause. (3) shows the UI with live updates, no token in the address bar, and no policy violation in the browser's console. (4) and (5) are refused and change nothing on the host; the same-site post is refused although the browser sent the cookie.
- **Cleanup.** Stop the host, delete the home, the certificate and the profiles.

<a id="dh-e-api-3"></a>
#### DH-E-API-3 A person's terminal survives a reload and is cleaned up
- **Proves.** [DH-D-API-8](#dh-d-api-8).
- **Given.** The built binary on Linux, macOS and Windows; a real browser; a session whose agent is in `read-only` mode.
- **When.** (1) Open a terminal, write a file outside the workspace, start a full-screen program. (2) Reload the page. (3) Switch the agent's sandbox mode. (4) Close the terminal. (5) Open another, close the tab, and advance the reclamation setting to a few seconds.
- **Then.** The write succeeds and no approval is asked; after (2) the screen is restored and input works; (3) leaves the terminal running; after (4) and (5) no process of either terminal remains and the roster is empty; the session log holds no terminal content.
- **Cleanup.** Delete the written file, stop the host, confirm no child process remains, delete the home.

<a id="dh-e-api-4"></a>
#### DH-E-API-4 A file preview follows changes made outside the harness
- **Proves.** [DH-D-API-9](#dh-d-api-9).
- **Given.** The built binary, a real browser, a session on a workspace with a text file, an image and a directory of 3,000 files; a second run against an SSH execution world (area A10) where watching is unavailable.
- **When.** Open the tree and a preview; change the file with an external editor; delete and recreate its parent directory; download the image with a range request; open the large directory; repeat the edit on the SSH world.
- **Then.** The preview and the tree refresh without a reload in both worlds (by polling in the second); the range answer is partial content with a matching version; the large listing is cut and says so; collapsing the directory ends its watch.
- **Cleanup.** Restore the files, stop the host, delete the home.

<a id="dh-e-api-5"></a>
#### DH-E-API-5 Disabling a feature removes its routes from a running product
- **Proves.** [DH-D-API-1](#dh-d-api-1).
- **Given.** The built binary with the jobs routes mounted, a real browser showing a session with a running job in the roster.
- **When.** Edit the writable configuration layer to disable the jobs route component; then enable it again.
- **Then.** Without a restart the roster region is replaced by "unavailable", a direct request to a jobs URL answers 404, the conversation keeps streaming; after re-enabling, the region is live again.
- **Cleanup.** Restore the layer, stop the job, stop the host, delete the home.

## 10. Open questions and limitations

1. **Whether a browser honours a hash for an inline import map** is known only from a run; if a supported browser does not, the composer island is framed ([ui-chat.md](ui-chat.md), `DH-ADR-UICHAT-3`) and `AllowInlineData` has no user.
2. **Unmeasured numbers.** `ReplayLimit`, `PageEvents`, the write deadline, the reconnect limits, `LiveMinInterval` and the cost of rendering a replay; a rendered-fragment cache for recently viewed sessions is not designed until a budget test shows a need.
3. **Limits kept.** Twelve upstream limits are carried on purpose ([§5](#5-tech-debt-and-its-resolution)). No resumable upload. Jobs, terminals, holds and session grants do not survive a host restart. A vanished tab behind a buffering proxy is noticed only at `MaxAge`. Sharing a session shares the absolute paths in it. This area's access model and its forgery defence have had no security review.

## 11. Units dispositioned by this spec

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 9 | 4 ported, 5 replaced |
| `UP-DEBT` | 50 | 32 resolved, 6 not applicable, 12 carried |
| `UP-EVT` | 5 | 5 replaced |
| `UP-CFG` | 7 | 6 ported, 1 replaced |
| `UP-SVC` | 12 | 4 ported, 7 merged, 1 dropped |
| `UP-DEC` (handed over) | 26 | 11 ported, 10 replaced, 4 dropped, 1 not applicable |

Ledger fragment: `trace/fragments/A19.jsonl`. Go items declared: `DH-ADR-API-1` to `-10`, `DH-D-API-1` to `-10`, `DH-T-API-1` to `-26`, `DH-E-API-1` to `-5`.
