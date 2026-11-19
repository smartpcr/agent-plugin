# Experimental: sharing a live session, and the in-browser WebWorker runtime: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) (`2e39e70`) and are cited as `path:line` relative to the upstream root. Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `XSHARE`. Nothing here has been built, run or tested. Upstream marks all four packages as experimental, "without a product-support commitment" (`packages/experimental/README.md:12`), so this specification is shorter than a core area's.

| | |
|---|---|
| **Area key** | `XSHARE` (agent X2) |
| **Upstream packages** | 4, all under `packages/experimental`: `liveshare`, `liveshare-protocol`, `webworker-packer`, `webworker-runtime` |
| **Serves outcomes** | [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7); the drops serve [DH-O-16](../design.md#dh-o-16) |
| **Depends on areas** | A19 (routes, access service, event stream: [api.md](api.md)), A18 (listener, admission, peers: [host-telemetry.md](host-telemetry.md)), A17 (who may answer, secret masking: [interaction-credentials.md](interaction-credentials.md)), A07 (machine-state tables), C1 and C4 (page shell, places, transcript renderer, message catalogue) |
| **Priority** | P2. Nothing in a first working harness or on the main paths needs a second person. The WebWorker runtime has no priority: it is not built ([DH-ADR-17](../design.md#dh-adr-17)) |

## 1. Purpose

This area covers two unrelated experiments.

**Sharing a live session ("liveshare").** A session runs on its owner's machine. Sometimes a colleague needs to watch it while it runs: to follow a debugging session or to see how a long task is going. Sending the log afterwards is not the same thing. Upstream answers with a relay service on the network and a viewer who can also drive the session. The Go design keeps the need (another person watches a session live) and answers it with what the host already has: a browser is a client of the host, and a second kind of principal can be admitted to one session, read-only, for a bounded time. Without this area the only way to show someone a session is to hand them a launch URL, which makes them the owner, with terminals and settings included.

**The in-browser runtime.** Upstream can run its whole host inside a browser tab, because the host is JavaScript. The owner has dropped this ([DH-ADR-17](../design.md#dh-adr-17)). This document records what it was used for, so that the loss is visible ([§3.4](#34-what-the-browser-hosted-runtime-was-for-and-what-is-given-up)), dispositions its units, and designs no replacement.

## 2. Vocabulary

Terms defined elsewhere are used as written: **peer**, **principal**, **route**, **action**, **resource**, **role**, **binding**, **policy**, **event stream**, **journal**, **cursor**, **region**, **activation** ([api.md](api.md) §2); **launch token**, **class**, **mount**, **local mode**, **remote mode** ([host-telemetry.md](host-telemetry.md) §2); **answerer**, **actor**, **session grant** ([interaction-credentials.md](interaction-credentials.md) §2).

| Term | What it is | Why it exists |
|---|---|---|
| **Owner, viewer** | The owner is the person whose host runs the session. A viewer is another person the owner lets look at it. | The two have different rights, so they must be different principals. |
| **Share** (Go) | The owner's grant that lets one viewer browser read one session until a stated time. It lives in the memory of the host process that serves it. | The unit that is granted, shown, listed, expired and stopped. |
| **Invitation** | A random one-time value in a link. The first browser that opens the link becomes the share's viewer; the link is then dead. | A link that keeps working after it was forwarded is a standing credential. |
| **Relay, Liveshare server** (upstream) | A separately deployed network service that authenticates viewers and carries their requests to the owner's host. | Lets a viewer reach an owner whose machine accepts no inbound connection. Not built in Go ([DH-ADR-XSHARE-2](#dh-adr-xshare-2)). |
| **Tier** (upstream) | `view` or `write`: what the tools of a viewer-started turn may do. Both tiers let the viewer prompt. | Upstream viewers act; Go viewers do not ([DH-ADR-XSHARE-3](#dh-adr-xshare-3)). |
| **Preview, VFS image** (upstream) | The Preview is the web client with the host running in a Web Worker (a background browser thread). A VFS image is the packed in-memory file system that worker mounts. | A demonstration with no installation. Not built. |
| **Exposure statement** | The text on the create form that lists what the viewer will and will not see. | A grant is only explicit if the person granting knows what it covers. |

A naming note: area A18's `sharing` gate ([host-telemetry.md](host-telemetry.md) §6.1) decides what is *sent to vendors*. It is unrelated to a session share; the Go package here is therefore named `sessionshare`.

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.experimental.liveshare` | `dsh-experimental-liveshare` | provider, host and client; default-off bundle | Owner sign-in, the Share dialog, `ctx.liveshareShares`, the outgoing gateway process, the `share-viewer` access policy, the tool confinement for viewer-started turns, and the viewer surfaces of the owner's own web page (`packages/experimental/liveshare/README.md:12`). 267 files. |
| `UP-PKG-packages.experimental.liveshare-protocol` | `dsh-experimental-liveshare-protocol` | definition | What the owner bundle and the relay must agree on: owner-to-worker frames, gateway frames the relay forwards unread, the carrier script the relay puts in the served page, the identity contract and the viewer-state vocabulary (`packages/experimental/liveshare-protocol/README.md:12`). |
| `UP-PKG-packages.experimental.webworker-runtime` | `dsh-experimental-webworker-runtime` | other (a second host platform) | Runs the whole plugin tree in one Web Worker: an in-memory file system, a module loader, a `postMessage` tunnel that speaks HTTP, and re-implementations of Node's built-in modules (`packages/experimental/webworker-runtime/README.md:12`, `:32`). |
| `UP-PKG-packages.experimental.webworker-packer` | `dsh-experimental-webworker-packer` | other (build tool) | Packs one shipped profile's built packages into the compressed image the worker mounts, with lazily fetched chunks and fixture overlays (`packages/experimental/webworker-packer/README.md:12`, `:37`). |

### 3.2 Behaviour

**Liveshare, question by question.** In this table `README.md` and `src/` are under `packages/experimental/liveshare`, and "note" is `.agents/notes/implemented/feature/2026-09-13-liveshare-protocol-and-owner-surfaces.md`.

| Question | Upstream answer |
|---|---|
| **Who can watch?** | Anyone who signs in at the relay and whom the share admits. A share made in the dialog is an "organization link": anyone in the organization who has the link, narrowed by a deployment allow-list (`.agents/notes/implemented/feature/2026-09-13-liveshare-protocol-and-owner-surfaces.md:21`). The relay asserts who the viewer is; the owner's host creates one peer per share and reader with a `share-viewer` principal (`packages/experimental/liveshare/README.md:111`). |
| **Who can act?** | Every admitted viewer. At both tiers a viewer prompts the shared session and stops its running turn; `write` adds bounded file edits (`README.md:86-88`). The note says so plainly: "A `view` share is not watch-only" (note `:150`). Independent of tier a viewer uploads attachments, answers the session's pending questions, manages its goal, follows its jobs and, when the owner allows, forks the session and calls selected plugin methods (`README.md:90`, `:96`, `:99`; `src/host/viewer-access-policy.ts:160`). Approvals, terminals, workspace files, settings and credentials stay with the owner (`README.md:90`, `:164`; `docs/subsystems/liveshare.md:57`). |
| **How does a share start?** | The bundle is off and hidden until a profile enables it (`README.md:38`). The owner saves a relay address, signs in to it through a pop-up, then creates a share for one session with a tier, a fork switch and an optional lifetime; the host registers it at the relay and returns a link (`README.md:70-76`). The lifetime is optional: "absent means the share never expires" (`src/share-api-types.ts:140-141`). |
| **How does it end?** | The owner stops it, it expires, or the relay answers that it no longer exists. Forks are revoked, and a running turn is stopped only if a viewer started it (`docs/subsystems/liveshare.md:35`, `:43`). |
| **What travels?** | The host starts a supervised child process that keeps an outbound WebSocket to the relay (`src/owner-gateway/worker.ts:160`). The viewer's page *is the owner's web client*: the relay fetches the owner's index document, shell assets and client plugin bundles, and adds a carrier script that turns every call and stream of the page into `POST {base}/__share/rpc` (`README.md:111`; `packages/experimental/liveshare-protocol/src/viewer-carrier.ts:52`). So calls, the session journal, state streams, attachments and the client code itself all pass through the relay. Version 1; a mismatch is refused, not translated (`README.md:126`). |
| **Where does the relay run?** | Somewhere both sides can reach. It is a separate package, deployed and reviewed separately, and not in this checkout; its sign-in, database and routing "are not part of this package" (`README.md:12`; note `:13`). Inference: the relay serves the page and injects script into it, so it is fully trusted: it can read whatever a viewer can. |
| **What does a viewer see?** | The session as the owner's client renders it: messages, reasoning, tool calls with arguments and results, attachments, the job roster and output, and the workspace names around the session (`README.md:90`; `docs/subsystems/liveshare.md:55`). The `@` picker also lists the id, title and directory path of every session on the host (`README.md:169`). No redaction step exists: a search of `src/host` finds none. A secret a command printed into a tool result is shown. |

**How acting viewers are contained.** While a share is active the host installs confined replacements of the read tools (and bounded `edit` and `write` at the `write` tier) in the session's scope. An in-memory marker says whether the running turn was started by a viewer, and only such turns are confined (`docs/subsystems/liveshare.md:39`). The containment has stated holes: a `run_code` program "is not bounded by the share", path checks can be raced with a symlink, and a viewer's stop also ends the owner's turn (`README.md:94`, `:163`; `docs/subsystems/liveshare.md:47`).

**The WebWorker runtime.** The page starts a worker; the worker downloads and mounts the image, boots the same profile as the served web deployment, and answers the page's HTTP over a message tunnel (`packages/experimental/webworker-runtime/README.md:30`, `:34`). `child_process.spawn` starts another worker that interprets a small shell grammar over a table of core utilities (`:33`). The packer never compiles: it copies built packages, rewrites each reachable module to a wrapper form, and splits lazy chunks (`packages/experimental/webworker-packer/README.md:34-35`).

### 3.3 Failure, retry, rollback and cleanup upstream

- **Relay unreachable or refusing.** A 404 or 410 withdraws the share; a 401 or 403 ends admission but keeps the share; a timeout leaves held grants alone and refuses new viewer input until a read succeeds (`docs/subsystems/liveshare.md:43`). Missing authority fails closed (`:59`).
- **Retried fork.** A fork is reserved durably before the child exists, so a retry replays the same child; a host that dies between creating and recording the child forks twice (`docs/subsystems/liveshare.md:33`).
- **Two hosts on one home.** One host claims the owner state with a file; the other reads shares and is refused every change (`.agents/notes/implemented/feature/2026-11-11-liveshare-owner-state-host-claim.md:13`).
- **Worker restart.** Shares are registered again; viewer streams are not replayed (`packages/experimental/liveshare/README.md:174`). **Cleanup.** Stopping a share deletes the relay row "when reachable" and otherwise reports that a retry is needed (`docs/subsystems/liveshare.md:99-101`). Revoked forks keep their logs.
- **Preview.** Everything is in the tab's memory: configuration edits and sessions last for the worker's lifetime (`packages/experimental/webworker-runtime/README.md:30`). A failed lazy-chunk fetch is retried on the next read (`:31`).

### 3.4 What the browser-hosted runtime was for, and what is given up

`README.md` in this table is `packages/experimental/webworker-runtime/README.md`.

| Use upstream | Evidence | What the Go design has instead |
|---|---|---|
| **A demonstration with nothing to install.** A static site serves the page; the visitor picks a fixture and browses real workspaces, sessions, tool cards and history. It makes no model request, runs no real process and cannot convert office documents. | `README.md:12`, `:38`, `:64`, `:66`, `:75` (the site is GitHub Pages) | Nothing. Seeing the product means downloading one binary and running it. |
| **A preview per pull request**, with a link to a snapshot comparison page. | `README.md:34` | Nothing. A reviewer runs the built binary; the replay provider ([design.md §3.4](../design.md#34-how-outcomes-are-proven)) supplies sessions without a model key. |
| **A packaging regression test**: a headless browser boots the packed image, so a package that no longer loads is caught. | `README.md:12`, `:38`; `packages/experimental/webworker-packer/README.md:12` | Not needed: the Go build links every component into one binary, so "does the packed tree load" is answered by the compiler and the composition tests. |

Inference: a Go equivalent would be a WebAssembly build of the host: a second build target with its own storage, sandbox and network answers ([DH-ADR-17](../design.md#dh-adr-17)). This document designs none. Conditional requirements in library specifications that exist "only if a host without an operating system is built" (for example `DH-LIB-YARNPKGPARSERS-R2` in [libs/yarnpkgparsers.md](../libs/yarnpkgparsers.md)) therefore lapse.

## 4. Upstream decisions dissected

The first four rows are the decision notes handed to this area (paths under `.agents/notes/implemented/`); the rest are visible in code and READMEs (paths under `packages/experimental/`). `XSHARE-n` is short for `DH-ADR-XSHARE-n` in [§7](#7-decisions). The ledger rows hold the full reasoning.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `architecture/2026-10-14-webworker-preview-profile-parity` | The Preview boots the shipped profile and fetches lazy chunks on demand | Drop | There is no Preview to boot | [DH-ADR-17](../design.md#dh-adr-17) |
| `feature/2026-09-13-liveshare-protocol-and-owner-surfaces` | Share through a relay; the viewer gets the owner's own page; two driving tiers; forks; verified owner sign-in; hidden by default | Change | Kept: the owner's host renders the page and decides every request, one access decision both draws and enforces, off by default, an unknown state is never driven. Dropped: the relay, relay identity, tiers, forks | [XSHARE-1](#dh-adr-xshare-1), [-2](#dh-adr-xshare-2), [-3](#dh-adr-xshare-3) |
| `feature/2026-11-02-liveshare-plugin-method-catalog` | Plugin methods a share may expose are read from the live method registry | Drop | A viewer calls four read routes and nothing else; a Go plugin is a process that registers no UI route ([DH-ADR-4](../design.md#dh-adr-4)). The first verdict "ported" is overturned | [XSHARE-3](#dh-adr-xshare-3) |
| `feature/2026-11-11-liveshare-owner-state-host-claim` | One host per home claims the share state, so two hosts do not steal each other's relay registrations | Replace | No registration exists to steal: a share belongs to the process whose listener serves it and ends with it | [XSHARE-4](#dh-adr-xshare-4) |
| The viewer's page is the owner's client, served through the relay | `liveshare/README.md:111` | Keep the idea, drop the carrier | A server-rendered page needs no client code to be relayed; the viewer's browser talks to the owner's host | [XSHARE-1](#dh-adr-xshare-1) |
| A `view` share lets the viewer prompt and stop | note `:150` | Drop | Watching and driving are different grants; driving needs containment that upstream itself lists as incomplete | [XSHARE-3](#dh-adr-xshare-3) |
| A share may have no expiry | `liveshare/src/share-api-types.ts:140-141` | Change | Expiry is mandatory and capped | [XSHARE-4](#dh-adr-xshare-4) |
| The viewer is identified by an organization sign-in at the relay | note `:21`, `:64` | Drop | No identity provider exists without the relay; a one-time invitation replaces it, and the limit is stated | [XSHARE-2](#dh-adr-xshare-2), [XSHARE-4](#dh-adr-xshare-4) |
| Shares persist in an owner state file and are restored at start | `liveshare/README.md:67` | Change | A share lives in memory; a restart ends it. Only an audit row is stored | [XSHARE-4](#dh-adr-xshare-4) |
| The bundle ships hidden and off | `liveshare/README.md:38` | Keep | Exposing a session to another person must be a deliberate act | [XSHARE-4](#dh-adr-xshare-4) |
| Approvals stay on the owner's host | `docs/subsystems/liveshare.md:57` | Keep, extend to questions | Answering widens what the model may do ([interaction-credentials.md](interaction-credentials.md) §6.2, rule 6) | [XSHARE-3](#dh-adr-xshare-3) |

## 5. Tech debt and its resolution

All 47 assigned units, and two this area found, are grouped by cause; each unit's own reason is in its ledger row. `L:` stands for `UP-DEBT-limit.packages.experimental.`.

| Group (cause) | Units | How the Go design answers it |
|---|---|---|
| **A. A third party between owner and viewer.** Identity asserted by the relay, a gateway worker with registration tokens, authority snapshots, version matching between three deployed parts, state shared by two hosts, requests that bypass the carrier | `L:liveshare-protocol.1` to `.4`, `L:liveshare.1`, `.12`, `.14`, `.17` (8) | Resolved: there is no relay, worker, wire protocol or carrier. The viewer's browser is a client of the owner's host, and the host is the only authority ([XSHARE-2](#dh-adr-xshare-2)). |
| **B. Viewers that act.** Uploads the owner cannot refuse, tool ceilings with an unbounded `run_code` and a symlink race, plugin methods with no sandbox, no per-viewer levels, a stop that is not attributed | `L:liveshare.2`, `.3`, `.7`, `.8`, `.15`; found `UP-DEBT-found.X2.2` (6) | Resolved: a viewer starts no turn, uploads nothing and calls no method, so there is nothing to confine or attribute ([XSHARE-3](#dh-adr-xshare-3)). |
| **C. Reads wider or narrower than the share.** The `@` picker lists every session; file references are classified readable yet unreachable; a workspace leaves the feed late; several sources per share | `L:liveshare.5`, `.6`, `.9`, `.10`, `.11` (5) | Resolved: one share names one session; the role lists four actions; no list, picker or workspace feed is served to a viewer ([DH-D-XSHARE-3](#dh-d-xshare-3)). |
| **D. A lifetime with no end** | found `UP-DEBT-found.X2.1` (1) | Resolved: expiry is required and capped ([XSHARE-4](#dh-adr-xshare-4)). |
| **E. Verification conventions** | `L:liveshare.13`, `.16` (2) | `.13` resolved: authorization is one decision before the handler, proved by a sweep of the route table ([DH-T-XSHARE-3](#91-local-tests)). `.16` not applicable: a release gate for upstream's client artifact. |
| **F. Kept on purpose** | `L:liveshare.4` (1) | Carried: a viewer has no file preview and no terminal. File routes can read any file the session can; a terminal is command execution as the user ([api.md](api.md) `DH-ADR-API-7`, `-8`). |
| **G. A build-time image of JavaScript packages.** Rule tables, inferred reachability, excluded sources, stale build inputs, a tool tied to the checkout, fixture formats | `L:webworker-packer.1` to `.6` (6) | Not applicable: no image is packed ([DH-ADR-17](../design.md#dh-adr-17)). |
| **H. Node emulated in a browser.** Stub modules, partial built-ins, loader internals, lazy files, watchers | `L:webworker-runtime.2`, `.3`, `.4`, `.12` to `.15` (7) | Not applicable: the Go host runs on an operating system and uses the standard library. |
| **I. Features the Preview cannot run.** Compressed logs, telemetry, package commands, external agents, model requests, programs, office conversion, module reload | `L:webworker-runtime.1`, `.5` to `.11` (8) | Not applicable: each is a limit of a host that is not built. The same features on the real host belong to their own areas. |
| **J. An emulated shell and sandbox, and a browser-only test gap** | `L:webworker-runtime.16` to `.20` (5) | Not applicable: commands run in the real shell under a real sandbox backend (area A10); there is no worker transport to cover. |

Totals: 21 resolved (19 assigned, 2 found), 27 not applicable, 1 carried.

## 6. Go design

### 6.1 Shape

**What already exists without this area.** A second browser admitted with a fresh launch token is a second owner peer; it sees every session live through the event stream ([api.md](api.md) `DH-ADR-API-6`; [host-telemetry.md](host-telemetry.md) `DH-ADR-HOST-3`). That serves one person on two devices. It does not serve "show a colleague": an owner can open terminals, read files and change settings. The foundations for the missing piece are also there: the access service decides every route and stream for a non-owner from bindings and names this area as the source of the first policy ([api.md](api.md) `DH-ADR-API-5`, `DH-D-API-10`), and the listener lets another component answer authentication with a peer of its own principal kind ([host-telemetry.md](host-telemetry.md) `DH-ADR-HOST-4`).

**What this area adds.** One default-off component, in `src/harness/internal/x/sessionshare` (the experimental family of [design.md §4.2](../design.md#42-go-module-layout)), and nothing for the WebWorker runtime.

| Piece | Component, service key | Injects | Why it exists |
|---|---|---|---|
| `sessionshare` | Component `x.session-share`; `sessionshare.Key` (`Shares`) | `access.Key`, `web.Key`, `stream.Key` (A19); `listen.Key` (A18); `kv.Key` (A07); the shell's region registry (C1) | The registry of live shares, the invitation exchange, the viewer peer, the `share-viewer` policy, the owner's routes and the two indicators. |

```go
// package sessionshare
type Share struct {
    ID      ID
    Session session.ID
    Label   string            // the owner's note of who was invited; shown, never verified
    Created, Until time.Time  // Until is required
    State   State             // Invited | Joined | Ended
    Ended   Reason            // Stopped | Expired | InviteLapsed | HostStopped | Unloaded
}
type Shares interface {
    Create(ctx context.Context, by listen.Peer, rq CreateRequest) (Share, Invitation, error) // owner peers only
    List(session session.ID) []Share                 // the zero id lists every live share of this host
    Stop(ctx context.Context, by listen.Peer, id ID) error // idempotent
    StopAll(ctx context.Context, by listen.Peer) int
}
type Invitation struct{ /* unexported */ } // prints "[redacted]"; URL() is called once, by the create view
```

**How a share runs.** (1) The owner opens the share panel of a session and submits the create form. (2) The host mints the invitation and shows its link once. (3) The viewer opens the link; the exchange consumes the invitation and calls A18's `auth.Issue(principal, ttl)`, which creates the browser session and its cookie, so this component implements no cookie of its own; the browser is redirected to `/sessions/{sid}`. (4) Every later request passes A18's admission as a peer whose principal is `{Kind: "share-viewer", ID: <share id>}`; a browser that also holds an owner session is admitted as the owner. (5) A19's `access.Decide` asks the policy this component registered; it returns one binding: the view role on the shared session, `Until` the share's end. (6) The session page and its stream are A19's and C4's, rendered for that principal; nothing in them is specific to sharing. (7) At `Until`, on stop, or when the component unloads, the share ends ([§6.3](#63-ownership-failure-retry-rollback-and-cleanup)).

The host opens no outbound connection for a share, contacts no relay and opens no tunnel. The viewer's browser must be able to reach the owner's listener: on the same machine, through a port the operator forwarded, or on a listener the operator bound to a network address with TLS ([host-telemetry.md](host-telemetry.md) `DH-ADR-HOST-2`).

### 6.2 Contracts

**The view role.** One role exists, on resource kind `session`. Its actions are route names of [api.md](api.md) §6.2 plus this area's status region.

| A viewer may | Action | A viewer may not (refused before any handler runs) |
|---|---|---|
| Open the session page and page back through its history | `session.open`, `session.history` | Prompt, steer, cancel, edit the queue, rename, choose a model, fork, delete, archive, pin (`session.prompt`, `.cancel`, `.queue`, `.rename`, `.model`, `.fork`, `.delete`) |
| Follow it live: durable events by cursor, and the in-flight assistant message | `session.follow` | Upload or use the composer's completions (`session.upload`, `session.complete`) |
| Load attachments that this session's log references | `session.attachment` | List sessions or workspaces, open another session or a child session, browse or download files, see jobs, open a terminal, read settings or credentials |
| See whose session this is and when the share ends | `share.status` | Answer an approval or a question, create or revoke a session grant, load a remote image (the host would fetch an address the model chose) |

Every route is `Activation: None` for a viewer: watching never wakes a session and never takes its lease.

**Routes.** Registered through `web.Routes` (A19); they leave with the component.

| Method and pattern (name) | Access | Returns and rules |
|---|---|---|
| `GET /sessions/{sid}/share` (`share.panel`) | owner | Fragment: this session's live shares, the create form and the exposure statement |
| `POST /sessions/{sid}/shares` (`share.create`) | owner | Form: label, lifetime in minutes, acknowledgement, request id. Answers the invitation link once. Refusals: `share/not-acknowledged`, `share/invalid-lifetime`, `share/limit`, `share/insecure-listener` |
| `DELETE /sessions/{sid}/shares/{shid}` (`share.stop`); `DELETE /shares` (`share.stop-all`) | owner | `{shid}` is opaque. Idempotent |
| `GET /shares` (`share.list`) | owner | Page: every live share of this host and the recent audit rows |
| `GET /sessions/{sid}/share/status` (`share.status`) | `access.On(access.Session("sid"))` | Fragment and state region: the owner's badge, or the viewer's banner, by principal |
| `GET /share/join?invite=` (`share.join`) | `access.Public(...)`, A18 class `callback` | The exchange. Success: `303` to the session, the viewer cookie, `Cache-Control: no-store`, `Referrer-Policy: no-referrer`. Any failure: one page, the same for an unknown, used, lapsed or stopped invitation, and no cookie |

**The security rules of a share.** These are the contract; [DH-ADR-XSHARE-4](#dh-adr-xshare-4) gives the reasons.

| Rule | Statement |
|---|---|
| **Off by default** | The component is a default-off entry. With it off there is no share route, no viewer principal kind and no policy, so every non-owner is refused ([api.md](api.md) `DH-ADR-API-6`). |
| **Explicit grant** | Only an owner peer creates a share, through the form, for one named session, after ticking the acknowledgement under the exposure statement. The model, a plugin process, the tool endpoint and an SDK or ACP client have no path to `Create`. |
| **One share, one browser** | An invitation is 32 random bytes, valid once and for `InviteTTL`. The browser session it yields is A18's (`auth.Issue`): a random session id in an `HttpOnly`, `SameSite=Strict` cookie, `Secure` when the listener speaks TLS, with a row that lives until the share ends. The label is the owner's note, not an identity. |
| **Expiry** | `Until` is required: `DefaultLifetime` unless the owner chose another, never beyond `MaxLifetime`. It is enforced twice: the binding's `Until` denies by the clock at every decision, and a timer ends the share and its streams. A share is not extended; the owner creates a new one. |
| **Read-only** | The view role above is the only role. No setting adds an action to it. |
| **Visible indicator** | While a share is not ended, every owner page of that session shows a badge in the session header (label, state, end time, a stop control) and the session list marks the row. Creation, a viewer joining and an end are also notices. The viewer's page carries a banner: whose session, read-only, until when. |
| **Transport** | Creating a share is refused (`share/insecure-listener`) on a listener in remote mode without TLS, because the invitation and the transcript would cross the network in clear text. In local mode the form says that the link works only on this machine or through a forward the operator set up. |
| **What is redacted** | *Already masked in the log:* every secret value the credential store released, masked in tool results before they are logged ([interaction-credentials.md](interaction-credentials.md) §6.2). *Never served:* everything outside the one session's transcript (the right column above). *Not redacted:* the rest of the transcript, including history from before the share: absolute paths, command output, file contents the model read, and any secret the store never held, such as a key a command printed from a file. The exposure statement says exactly this. |
| **Audit** | Each share has a row: session, label, the creating actor, created, joined, ended, reason. The invitation and the cookie are never stored or logged. |

**Config.** `sessionshare.Config`, a Go struct, strictly decoded: `DefaultLifetime` 1 h; `MaxLifetime` 24 h (at most 7 days); `InviteTTL` 15 min; `MaxShares` 8 live shares per host; `AuditKept` 200 rows. No upstream `UP-CFG` unit is assigned to this area; upstream's settings (owner identity, allow-list, state and credential files, `packages/experimental/liveshare/README.md:62-68`) have no counterpart.

**Services, events, persisted data, tools.**

| Kind | Upstream | Go |
|---|---|---|
| Service | `UP-SVC-liveshareShares` (twelve remote methods, `docs/subsystems/liveshare.md:71-164`) | `sessionshare.Key`, interface `Shares`: `Create`, `List`, `Stop`, `StopAll`. The relay address, identity, capability, audit-fetch and relay-test methods have no counterpart |
| Live event | none assigned | `sessionshare.Changed`, notify form ([design.md §4.6](../design.md#46-event-model)): share id, session, state. It replaces the badge and list regions |
| Persisted | `owner-shares.json`, `owner-credential.json`, `owner-host.json` | Audit rows in A07's `kv`, namespace `session-share`. No session event: appending needs the session's lease, and a share must not wake a session. Live shares are memory only |
| Tools for the model | none | none |

**Client surfaces.** Upstream's browser face is React (`packages/experimental/liveshare/src/client`). UI text lives in the message catalogue under namespace `share`, in English and Chinese ([ui-core.md](ui-core.md) `DH-ADR-UICORE-7`). No island is needed. The viewer's page uses A19's `session.open` and `session.history` routes, the `session` and `live` events of the stream, and this area's `share.status` region; the owner's surfaces use `share.panel`, `share.list`, `share.status` and a row mark in the `shell` region. Area A19 owns the route table and the stream they all ride on.

| Upstream surface | What the user does with it | Go placement |
|---|---|---|
| Share action and Share dialog (`ShareAction.tsx`, `ShareDialog.tsx`) | Create or edit a share from the session header | A contribution to the session header's place (C1) that loads the `share.panel` fragment; a plain form |
| Manage Shares dialog (`ManageSharesDialog.tsx`) | List shares, read the audit, stop | The `share.list` page |
| Viewer brand row and status banner (`viewer-brand.tsx`, `viewer-status-banner.ts`) | See who shared and why a page cannot be read | The `share.status` banner; one refusal page for a dead invitation; the problem page for an ended share |
| Relay address field, owner sign-in pop-up, plugin-method picker, tier and fork fields, viewer session rows and fork lineage | Configure the relay and what viewers may do | Recorded drops ([XSHARE-2](#dh-adr-xshare-2), [XSHARE-3](#dh-adr-xshare-3)) |

### 6.3 Ownership, failure, retry, rollback and cleanup

**Ownership and access.** The component's fiber owns the registry, the timers, the routes, the policy and the principal kind; A18 owns each viewer's browser-session row, which the component asks it to create and to delete: unloading it releases all of them and so ends every share. The owner has access to create and stop shares; a viewer has access to four read actions on one session and owns nothing on the host. The session, its log and its lease are untouched by a share: a share is never an owner of anything in the session.

| Situation | What happens | Undone, retried, uncertain |
|---|---|---|
| The create form is posted twice | The request id makes the second answer the first receipt; the link is not shown again | One share exists |
| The invitation is never opened | At `InviteTTL` the share ends (`InviteLapsed`) | Nothing to undo |
| Somebody else opens the link first | They are the viewer. The owner's badge shows "joined" and a notice; the intended viewer's attempt fails | The owner stops the share. **Uncertain:** what the first browser already read |
| The owner stops a share, or it expires | The binding is gone, `access.Invalidate` makes the hub end the viewer's streams, the viewer's browser-session row is deleted through A18's `SignOut`, which cancels the peer's context, and the audit row closes. The cookie then names no row and admits nobody | Fragments already on the viewer's screen stay there; a share cannot recall what was shown |
| The host stops or crashes | Shares are gone with the process. On the next start, audit rows this listener left open are closed as `HostStopped`, and the component deletes every A18 browser-session row of principal kind `share-viewer` before the listener admits a request | A viewer's cookie names a deleted row and admits nobody; were a row left, its principal would have no binding and every route would refuse it |
| The component is disabled while shares are live | Unload ends them, removes the routes and the policy; viewers get 403, then 404 | Nothing persists |
| A second host process uses the same home | It shows the badge from the audit rows, read-only, naming the serving listener; it cannot stop a share it does not serve | Rows past `Until` are ignored, so a row left by a dead process misleads at most until then |
| The policy panics, or the registry cannot be read | A19 denies with `policy-failed` | Fails closed |
| The audit row cannot be written | Create fails and no share exists. A failed close is retried; the share has already ended in memory | Never a live share without a row |

## 7. Decisions

<a id="dh-adr-xshare-1"></a>
### DH-ADR-XSHARE-1 Liveshare is merged into the host's own multi-client surface: a share is a second kind of principal on the owner's listener

- **Decision.** Not ported as a subsystem. The need is met by one small default-off component that adds a `share-viewer` principal kind, a policy and an invitation exchange. Rendering, live updates, resume, backpressure and enforcement are the ordinary session page, event stream and access decision of areas A19, A18, C1 and C4.
- **Why.** Upstream needs 267 files because its viewer runs a client application that must be transported, version-matched and told what to hide. A server-rendered page for a principal with one binding is the same page with fewer controls. Of the three options (port at low priority, merge, drop), a port rebuilds a relay product this repository does not contain, and a drop leaves "hand over a launch URL" as the only way to show a session, which gives away the machine.
- **Tradeoff.** The viewer must be able to reach the owner's host, which upstream's relay existed to avoid. No viewer identity beyond a one-time link. One session per share and one viewer per share.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7). **Verified by.** DH-T-XSHARE-3, DH-T-XSHARE-8, [DH-E-XSHARE-1](#dh-e-xshare-1).

<a id="dh-adr-xshare-2"></a>
### DH-ADR-XSHARE-2 No relay: the relay protocol, the owner gateway process and relay sign-in are not built

- **Decision.** The harness speaks to no Liveshare server. `liveshare-protocol` is dropped whole; of `liveshare`, the outgoing gateway worker, registration, authority snapshots, the carrier, owner sign-in and the owner state files are dropped.
- **Why.** (1) The relay is not in the upstream checkout; a Go relay would be a new network service with accounts, storage and operations, which nothing in [design.md](../design.md) plans. (2) Upstream's relay could not be reused: it relays upstream's client application and typed calls, which the Go host does not serve ([DH-ADR-2](../design.md#dh-adr-2), [DH-ADR-3](../design.md#dh-adr-3)). (3) A relay sees everything a viewer sees. That is session content leaving the machine through a third party, which [DH-ADR-9](../design.md#dh-adr-9) allows only as an opt-in with a named destination; this design avoids the question by having no third party.
- **Tradeoff.** An owner behind a firewall cannot share without arranging reachability, and the harness does not help with that. If a relay is wanted later it is a new decision: it would be a sender in A18's sense, off by default, and under the one off-switch. A recorded exception to [DH-ADR-1](../design.md#dh-adr-1), decided by the owner: no relay ([DH-ADR-37](../design.md#dh-adr-37)).
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-16](../design.md#dh-o-16). **Verified by.** DH-T-XSHARE-1 (no outbound connection is made), the `dropped` ledger rows that link here.

<a id="dh-adr-xshare-3"></a>
### DH-ADR-XSHARE-3 A viewer watches and cannot act

- **Decision.** One role, four read actions. Not built: viewer prompts and stops, the `view` and `write` tiers, forks and their ceilings, viewer uploads, goal management, question answering, the plugin-method catalog, job and speech access.
- **Why.** A viewer's prompt is model input: the turn it starts runs with the owner's tools, sandbox policy and session grants. Upstream contains this with replaced tools and a per-turn marker and still lists an unbounded program tool, a symlink race and an unattributed stop ([§3.2](#32-behaviour)). The interaction rules bind an agent to the client that created it, with no automatic transfer ([interaction-credentials.md](interaction-credentials.md) `DH-ADR-HUMAN-2`), so a viewer-started turn would raise approvals only the owner can answer. Doing this safely is core security work, not an experiment.
- **Tradeoff.** Decided by the owner: viewers only watch ([DH-ADR-37](../design.md#dh-adr-37)). Upstream's headline use, a colleague who helps drive, is lost; the colleague can only tell the owner what to type. A driving role would need: the admitting principal recorded on the message, a tool ceiling applied at the pipeline's policy stage for turns that principal started, session grants that do not apply to such turns, and an attributed stop. It would be a new decision.
- **Serves.** [DH-O-7](../design.md#dh-o-7). **Verified by.** DH-T-XSHARE-3, DH-T-XSHARE-4, [DH-E-XSHARE-1](#dh-e-xshare-1).

<a id="dh-adr-xshare-4"></a>
### DH-ADR-XSHARE-4 A share is explicit, single-use, time-boxed, visible, memory-only and honest about what it shows

- **Decision.** The rules table of [§6.2](#62-contracts): off by default; created only by an owner for one session after an acknowledged exposure statement; one invitation, one browser; a required and capped expiry enforced by the clock and by a timer; a permanent badge for the owner and banner for the viewer; refused on an insecure network listener; live state in memory only, with an audit row per share; no redaction promised beyond the masking the log already has. The one off-switch does not touch sharing: it stops senders only, and a share the user created stays up while it is set ([DH-ADR-27](../design.md#dh-adr-27)). A share is not a kind of A18's sharing gate and holds no permit; what ends it is its expiry, the stop control, a restart, or disabling the entry.
- **Why.** A share exposes a session to another person. Each rule closes one way that can go wrong without the owner noticing: a share nobody remembers (expiry, badge, a restart ends it), a link that travels (single use, `InviteTTL`), a viewer taken for someone else (the label is stated to be unverified), a secret believed hidden (the exposure statement names what is not redacted). Memory-only state also replaces upstream's owner-state claim: there is nothing durable for two hosts to contend for.
- **Tradeoff.** Whoever opens the link first is the viewer: the same limit as the launch URL ([design.md §6](../design.md#6-security-model)). A restart ends every share. The whole transcript is shared, history included. Redaction by pattern is not attempted for a live view, because unsettled stream text cannot be masked reliably and a promise that mostly holds is worse than a plain statement. An owner peer can share, so the open finding that a command run for the model may be able to make itself an owner ([host-telemetry.md](host-telemetry.md) `DH-ADR-HOST-3`) extends to creating a share; the badge and notices are what the real owner would see.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Verified by.** DH-T-XSHARE-1, -2, -5, -6, -7, -9, [DH-E-XSHARE-1](#dh-e-xshare-1), [DH-E-XSHARE-2](#dh-e-xshare-2).

## 8. Deliverables

<a id="dh-d-xshare-1"></a>
### DH-D-XSHARE-1 An owner lets one invited browser watch one session, read-only, until a stated time

- **Result and why it matters.** A colleague follows a running session live in an ordinary browser, without becoming an owner and without any software or account.
- **How it is implemented.** A registry of shares guarded by one mutex. `Create` checks the peer's principal is the owner, the acknowledgement, the lifetime bounds, `MaxShares` and the listener's mode and TLS state, writes the audit row, then mints the invitation. The exchange route compares the invitation in constant time, consumes it, and calls A18's `auth.Issue(principal, ttl)` with the principal `{Kind: "share-viewer", ID: <share id>}` and the share's remaining lifetime; A18 creates the browser-session row and its cookie, and admits the browser as that peer from then on, so this component holds no key and implements no cookie ([host-telemetry.md](host-telemetry.md) §6.1; there is no signing secret, [DH-ADR-28](../design.md#dh-adr-28)). The share keeps the row's id; when the share ends the component deletes that row through A18's `SignOut`, which ends the peer's context. A browser that also holds an owner session stays the owner.
- **Components and files.** `internal/x/sessionshare` (registry, invitation, policy, routes).
- **Depends on.** [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1), [DH-D-CORDIS-2](../libs/cordis.md#dh-d-cordis-2). [DH-D-API-1](api.md#dh-d-api-1), [DH-D-API-3](api.md#dh-d-api-3), [DH-D-API-10](api.md#dh-d-api-10) and [DH-D-API-4](api.md#dh-d-api-4) (`web.Routes` and `access.Public`; the first non-owner policy; the session routes and the stream), [DH-D-HOST-1](host-telemetry.md#dh-d-host-1) and [DH-D-HOST-2](host-telemetry.md#dh-d-host-2) (`listen.Authenticate`, the `callback` class, `Listener.Secure()` and `Mode()`), [DH-D-STORAGE-1](storage.md#dh-d-storage-1) (a `kv` table). Runtime prerequisite: a listener the viewer's browser can reach.
- **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7). **Priority.** P2. **Done when.** DH-T-XSHARE-1, DH-T-XSHARE-2, DH-T-XSHARE-8 and DH-T-XSHARE-9 pass and [DH-E-XSHARE-1](#dh-e-xshare-1) has passed.

<a id="dh-d-xshare-2"></a>
### DH-D-XSHARE-2 A share is always visible to the owner, ends on time, and can be stopped at once

- **Result and why it matters.** The owner cannot forget a share: the badge is on the session, the list is one page, and expiry, stop, a restart or disabling the feature each end it completely.
- **How it is implemented.** A state source on the hub, keyed by session, renders the badge for an owner and the banner for a viewer from the registry and, in a process that does not serve the share, from the audit rows. A timer per share fires at `Until` (and at `InviteTTL` while unjoined). Ending a share removes it from the registry, calls `access.Invalidate` for its principal, cancels the peer's context, closes the audit row and raises `sessionshare.Changed`. At start the component closes rows left open under its own listener address and trims the table to `AuditKept`.
- **Components and files.** `internal/x/sessionshare` (indicator source, timers, audit table, list page).
- **Depends on.** [DH-D-XSHARE-1](#dh-d-xshare-1). [DH-D-API-10](api.md#dh-d-api-10) and [DH-D-API-4](api.md#dh-d-api-4) (`access.Invalidate`, `stream.Hub.Register`), [DH-D-UICORE-2](ui-core.md#dh-d-uicore-2) (the regions `shell.header` for the badge and `shell.notices`), [DH-D-UISIDE-3](ui-sidebar-workspace.md#dh-d-uiside-3) (the row region `nav.session.mark`).
- **Outcome.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Priority.** P2. **Done when.** DH-T-XSHARE-5, DH-T-XSHARE-6 and DH-T-XSHARE-7 pass and [DH-E-XSHARE-2](#dh-e-xshare-2) has passed.

<a id="dh-d-xshare-3"></a>
### DH-D-XSHARE-3 A viewer's page shows the transcript and nothing else, and the owner is told exactly what that is

- **Result and why it matters.** Sharing one session shares no other session, no file, no terminal and no control; the form states what a viewer will see before the owner agrees.
- **How it is implemented.** The policy registered with `access.RegisterPolicy` for kind `share-viewer` returns one binding per live share. The role's action list is one Go value; the exposure statement is rendered from the same value plus a fixed "not redacted" text, so the statement cannot drift from the enforcement. The viewer's page is the ordinary session page: because the host draws a control only when `access.Allows` its route ([api.md](api.md) `DH-ADR-API-5`), it has no composer, no sidebar and no links to other sessions.
- **Components and files.** `internal/x/sessionshare` (policy, role, exposure view).
- **Depends on.** [DH-D-XSHARE-1](#dh-d-xshare-1). [DH-D-API-10](api.md#dh-d-api-10) (`access.RegisterPolicy`, per-principal rendering), [DH-D-UICORE-1](ui-core.md#dh-d-uicore-1) (a page shell that renders for a principal with no global binding, the `can` helper that leaves out controls the principal cannot use, and `ui.state` open to every admitted principal), [DH-D-UICONV-1](ui-conversation.md#dh-d-uiconv-1) and [DH-D-UICONV-6](ui-conversation.md#dh-d-uiconv-6) (the transcript, and approval cards and the grants strip drawn without controls), [DH-D-HUMAN-7](interaction-credentials.md#dh-d-human-7) (secret masking at result shaping).
- **Outcome.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Priority.** P2. **Done when.** DH-T-XSHARE-3 and DH-T-XSHARE-4 pass and [DH-E-XSHARE-1](#dh-e-xshare-1) has passed.

## 9. Acceptance

### 9.1 Local tests

Run with `net/http/httptest`, fakes of other areas' services and virtual time, under the race detector. Nothing has been run. `D-n` is `DH-D-XSHARE-n`.

| Test ID | Proves | What is asserted |
|---|---|---|
| DH-T-XSHARE-1 | D-1 | `Create` is refused for a non-owner peer, without the acknowledgement, for a lifetime of zero or beyond `MaxLifetime`, beyond `MaxShares`, and on a remote listener without TLS; accepted otherwise. A recording transport sees no outbound request. The invitation appears once in the create answer and in no log, list or audit row |
| DH-T-XSHARE-2 | D-1 | The exchange works once and sets the cookie attributes, `no-store` and `no-referrer`; a second use, a lapsed, unknown or stopped invitation give one identical page and no cookie; the session is issued through `auth.Issue` with the viewer principal and the share's lifetime, and ending the share deletes its row; after a restart no `share-viewer` row remains; a browser with an owner session stays the owner |
| DH-T-XSHARE-3 | D-3 | With every route of a full composition registered, a viewer principal is allowed exactly the role's actions on the shared session and refused everything else, including the same actions on another session and on a child session, before any handler runs |
| DH-T-XSHARE-4 | D-3 | The session page rendered for a viewer contains no form, no composer, no control whose route is refused, no link to another session, and no load control on a remote-image placeholder; the exposure statement lists every action of the role and no other |
| DH-T-XSHARE-5 | D-2 | Under virtual time: at `Until` the stream ends, the row closes as `Expired` and `Changed` is raised; a decision one tick after `Until` denies even when the timer has not run; an unjoined share ends at `InviteTTL` |
| DH-T-XSHARE-6 | D-2 | `Stop` and `StopAll` are idempotent, end the viewer's streams and record the actor. Through the real loader: disabling the entry ends every share and removes the routes, the policy and the principal kind. With the off-switch set, `Create` still succeeds and a live share stays up ([DH-ADR-27](../design.md#dh-adr-27)); the component requests no permit from the sharing gate |
| DH-T-XSHARE-7 | D-2 | The badge and the list mark are in the owner's baseline on connect and change on create, join and end; a second process on the same home shows the badge read-only and ignores rows past `Until`; at start, rows left open under this listener close as `HostStopped` |
| DH-T-XSHARE-8 | D-1 | A viewer's stream carries the journal in order and resumes by cursor; it never carries `interaction`, `shell` or `view` events; no viewer request creates an agent or takes a lease |
| DH-T-XSHARE-9 | D-1 | The invitation and cookie types print `[redacted]` in every text, JSON and log form; a tool result that held a released secret value reaches the viewer masked |

### 9.2 Real-environment scenarios

Each needs real browsers driving the built binary; the replay model provider stands in for a model API. Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed. They prove the stated contracts for the tested build, browsers and operating system.

<a id="dh-e-xshare-1"></a>
#### DH-E-XSHARE-1 A second person watches a live session and can do nothing else

- **Proves.** [DH-D-XSHARE-1](#dh-d-xshare-1), [DH-D-XSHARE-3](#dh-d-xshare-3).
- **Given.** Two machines on one network (or one machine with a second network address). The built `dsh` in its web profile with the `x.session-share` entry enabled, bound to the network address with a test certificate, a temporary harness home. A real Chromium on each machine: the owner's, signed in by launch URL, and the viewer's, with a fresh profile. A replayed session whose turn produces text, a tool call whose output contains a secret the credential store released, an approval and a remote image.
- **When.** (1) The owner opens the share panel, reads the exposure statement, creates a share of 30 minutes and copies the link. (2) The viewer opens the link. (3) The owner prompts; the turn runs; the owner approves the tool call. (4) From the viewer's browser: open `/sessions`, another session's URL, the files and terminal URLs of the shared session, and post a prompt and an approval answer with the developer tools. (5) Open the link again in a third fresh profile. (6) The owner presses stop.
- **Then.** After (2) the viewer sees the banner and the transcript, with no composer and no sidebar, and the owner's badge says "joined". During (3) both browsers show every event once and in order; the viewer sees the approval card with no buttons, the tool output with the secret masked, and the image as a placeholder that cannot be loaded. Every request of (4) is refused and changes nothing on the host. (5) shows the refusal page and sets no cookie. After (6) the viewer's stream ends, a reload shows that the share ended, and the audit page lists the share with its creator, join time and reason. The host made no outbound connection for the share.
- **Cleanup.** Stop all shares, close the session, stop the host, delete the home, the certificate and the three profiles. On failure keep the host log, the audit rows and both browsers' network logs.

<a id="dh-e-xshare-2"></a>
#### DH-E-XSHARE-2 A share ends by itself: expiry, restart, and disabling the feature

- **Proves.** [DH-D-XSHARE-2](#dh-d-xshare-2).
- **Given.** Two machines on one network (or one machine with a second network address); the built `dsh` in its web profile with the `x.session-share` entry enabled, bound to the network address with a test certificate and a temporary harness home, with `DefaultLifetime` set to two minutes for the run; a real Chromium for the owner, signed in by launch URL, and one for the viewer with a fresh profile, watching a replayed session through an accepted invitation.
- **When.** (1) Wait for the expiry. (2) Create a second share and join it; kill the host process and start it again. (3) Create a third share and join it; disable the `x.session-share` entry in the writable configuration layer. (4) Start a second `dsh` process on the same home during a fourth share.
- **Then.** (1) The viewer's stream ends at the stated time and the badge disappears without a reload. (2) After the restart the viewer's cookie is refused, the owner's page shows no badge, and the audit row reads `HostStopped`. (3) Without a restart the viewer is refused, the share URLs answer 404, and the owner's conversation keeps streaming. (4) The second process shows the badge read-only and cannot stop the share.
- **Cleanup.** Restore the configuration layer and the lifetime, stop both hosts, delete the home and the profiles. If a host cannot be stopped, record its process id and the runtime record under `<home>/run/` before deleting anything.

## 10. Open questions and limitations

1. **Decided by the owner.** No relay and viewers only watch ([DH-ADR-37](../design.md#dh-adr-37)); the off-switch does not end or refuse a share ([DH-ADR-27](../design.md#dh-adr-27)).
2. **Settled with [api.md](api.md).** A role is `access.Role{Name, ResourceKind, Actions}` declared with the policy; `access.Public(class, reason)` names A18's `callback` class for the invitation exchange; a stream `Source` is checked under the action `stream.<Topic>`, which the view role lists; a denial's message may differ by principal kind, so an ended share reads "this share has ended". This area is the first non-owner policy that `DH-D-API-10` names.
3. **Settled with [host-telemetry.md](host-telemetry.md).** `auth.Issue(principal, ttl)` issues the viewer's browser session; a browser holding an owner session and a viewer session is admitted as the owner. The finding "a same-user process may create a share" is answered by `DH-ADR-HOST-12` and reported with the other takeover gaps ([DH-ADR-28](../design.md#dh-adr-28)). The kinds table has no `live-share` row.
4. **Assumed of [interaction-credentials.md](interaction-credentials.md) (request sent).** Confirm that no role offered to `share-viewer` may contain `interactions: answer`, and that masking at result shaping covers every tool result before it is logged.
5. **Commands.** [app-cli.md](app-cli.md) declines to own `dsh share list` and `dsh share stop`; they are this component's, contributed as `cmdline.Command` values over `Service.List` and `StopAll` and present only while the entry is enabled (P2, part of [DH-D-XSHARE-2](#dh-d-xshare-2)). Until they are built the emergency stop is the list page or stopping the host.
6. **Not designed.** A share that starts "from now on" (it needs a lower bound on the binding that the history route and the hub honour; A19's decision takes no cursor). Pattern redaction for viewers (it needs a per-principal transform in the hub and A06's baseline redactor outside its upload component). Sharing a session together with its subagent children. Several viewers on one link.
7. **Limitations.** The invitation is a bearer value; there is no viewer identity. The viewer must reach the owner's listener. The whole transcript is shown, with whatever it contains. A share cannot recall what was already shown. A second host on the same home cannot stop a share it does not serve. Nothing here has had a security review.
## 11. Units dispositioned by this spec

Ledger fragment: `trace/fragments/X2.jsonl`.

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 4 | 1 merged (`liveshare`), 3 dropped (`liveshare-protocol` by [DH-ADR-XSHARE-2](#dh-adr-xshare-2); `webworker-runtime` and `webworker-packer` by [DH-ADR-17](../design.md#dh-adr-17)) |
| `UP-DEBT` (assigned) | 47 | 19 resolved, 27 not applicable, 1 carried |
| `UP-DEBT` (found by this area) | 2 | 2 resolved |
| `UP-SVC` | 1 | 1 replaced |
| `UP-DEC` (handed over) | 4 | 2 replaced, 2 dropped |

Go items declared: `DH-ADR-XSHARE-1` to `-4`, `DH-D-XSHARE-1` to `-3`, `DH-T-XSHARE-1` to `-9`, `DH-E-XSHARE-1` and `-2`.
