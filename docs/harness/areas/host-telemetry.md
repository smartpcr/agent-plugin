# Web host services and telemetry: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md) (`2e39e70`) and are cited as `path:line` relative to the upstream root. Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `HOST`. Nothing here has been built, run or tested.

| | |
|---|---|
| **Area key** | `HOST` (agent A18) |
| **Upstream packages** | 12: `packages/telemetry/otel`, and eleven under `packages/host` (`webserver`, `frontend-static`, `authentication-token`, `authentication-desktop`, `directory-picker`, `directory-picker-auto`, `directory-picker-browse`, `directory-picker-native`, `open-in-app`, `plugin-inventory`, `product-telemetry-otel`) |
| **Serves outcomes** | [DH-O-7](../design.md#dh-o-7), [DH-O-11](../design.md#dh-o-11), [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14) |
| **Depends on areas** | A19 (routes, event stream, access policy for non-owners), A17 (who may answer, anonymous installation id, feedback), A07 (home layout, machine-state tables, settings forms, workspace registry), A09 (the one process starter, which tree a local peer belongs to), A10 (what a confined command is denied), A08 (`fsx` lock), A20 (the `sdk/jsonrpc` codec), A23 (`egress`, `schema`, `mediatype`), A21 (loader view, logging component, boot readiness, safe mode), A22 (plugin process descriptors), A24 (commands and flags), A27 (presets), C1 and C2 (page shell, embedded asset files) |
| **Priority** | P0 for the listener, admission, launch, the sharing gate, the fatal diagnostic and the directory browser: a first web harness cannot be reached, trusted or diagnosed without them, and no sender may ship before the gate. P1 for the exporter and the plugin inventory. P2 for analytics and telemetry records and for opening a workspace in a host application |

## 1. Purpose

This area is the part of the host that faces outward. It does four jobs. It **lets a browser in**: it opens the one network listener, decides which requests may enter, turns the launch URL into a signed cookie, serves the files the pages need, and tells a second `dsh` how to find a host that is already running. It **decides what may leave**: every component that sends data to a vendor or collector reports through one gate with one off-switch, and one exporter carries the records ([DH-ADR-9](../design.md#dh-adr-9)). It **says what went wrong**: one fatal diagnostic and a crash-report file when the host cannot continue. And it gives the browser three things a web page cannot do for itself: browse directories of the host machine, open a workspace in an application on that machine, and list what is composed in the host.

Without it the web UI would be an unauthenticated port that any page in the user's browser could post to, data would leave on each component's own judgement, and a crash would leave nothing to read.

## 2. Vocabulary

Framework terms, **peer**, **principal**, **carrier**, **fragment** and **harness home** are defined in [../design.md §2](../design.md#2-vocabulary); **OTLP**, **collector**, **signal** and **channel** in [../libs/opentelemetry.md §1](../libs/opentelemetry.md).

| Term | What it is | Why it exists |
|---|---|---|
| **Listener, bind address** | The one TCP socket the host accepts browsers on, and the address it is opened on. **Loopback** means an address only this machine can reach (`127.0.0.0/8`, `::1`). The **unspecified address** (`0.0.0.0`, `::`) means "every interface". | The bind address decides who can reach a program that runs commands as the user. |
| **Local mode, remote mode** | Local: loopback bind, no non-loopback trusted host, no public URL. Anything else is remote. | Some actions only make sense when the person at the browser sits at the host machine. |
| **Source check** | Refusing a request by where it claims to come from, before any credential is read: the `Host` header must be on an allow-list, and a browser's `Origin` and `Sec-Fetch-Site` headers must say "same site". | A local server is reachable from every web page the user visits. The `Host` check stops **DNS rebinding** (a hostile domain made to resolve to loopback); the origin check stops cross-site form posts. |
| **Launch token** | A random secret in the URL that `dsh` prints or opens. It is used once and expires. | Proves the browser was started by the person who started `dsh`; a `Host` header proves nothing. |
| **Signed cookie, audience** | The credential the browser keeps after the exchange: a payload signed with a secret only the host knows. The audience is the scheme and authority it was issued for. | The browser authenticates every later request without the token; a cookie for one address is useless at another. |
| **Class** | What a request needs before it is served: `required` (a live peer), `anonymous` (no credential, still source-checked), `callback` (cross-site allowed, the handler verifies its own proof) or `deny`. | The default is closed, and the exceptions are few and listed. |
| **Mount** | A handler attached to the listener under a path prefix, with a function that states the class of each request. Area A19's route table is one mount; the assets and the sign-out routes are two more. | The listener admits; what a UI route is, and what a principal may do, belong to A19 ([api.md](api.md) §6.1). |
| **Runtime record, control endpoint** | A file in the home that says "a web host runs here", and a socket only the same operating-system user can open. The socket hands out no credential. | A second `dsh` reuses the running host instead of failing on a busy port. |
| **Owner authority, untrusted tree, peer rule** | Owner authority is the right to answer approvals, give session grants, change settings and sign a browser in. An untrusted tree is every process the host started under a sandbox launch plan: a command run for an agent and whatever it starts, an MCP server, a plugin. The peer rule refuses a local connection whose process is inside such a tree. | Those processes run as the same operating-system user as the host, so "only this user can open it" does not keep them out ([DH-ADR-HOST-12](#dh-adr-host-12)). |
| **Sender, kind, permit, off-switch** | A sender is a component that sends data to a vendor or collector. Its kind names what it sends. A permit is the gate's "yes, for now" for one kind. The off-switch refuses every permit. | [DH-ADR-9](../design.md#dh-adr-9) can only be checked if every sender is listed and asks in one place. |
| **Fatal diagnostic, crash report** | One message on standard error when the host cannot continue, and the file that holds the full detail. | A failure must be diagnosable from what is left behind, not from a screenshot. |
| **Directory browser** | A host service that lists one directory level and creates one child directory. | A web page cannot learn an absolute path of the host machine from the browser's own file dialog. |

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role | What it does |
|---|---|---|---|
| `UP-PKG-packages.host.webserver` | `dsh-host-webserver` | definition and provider | `ctx.webServer`: a `node:http` or `node:https` listener, named routes, upgrade routes, one fallback, optional gzip, index rendering (`packages/host/webserver/README.md:12`). |
| `UP-PKG-packages.host.frontend-static` | `dsh-host-frontend-static` | consumer | Claims the fallback and serves the built single-page application from a directory (`packages/host/frontend-static/README.md:12`). |
| `UP-PKG-packages.host.authentication-token` | `dsh-host-authentication-token` | provider | Launch token, signed cookies, the `/connect` exchange for API clients (`packages/host/authentication-token/README.md:12`). |
| `UP-PKG-packages.host.authentication-desktop` | `dsh-host-authentication-desktop` | provider | Accepts a private credential that only the Electron main process holds (`packages/host/authentication-desktop/README.md:12`). |
| `UP-PKG-packages.host.directory-picker` | `dsh-host-directory-picker` | definition | `ctx.directoryPicker`: a capability that is either `native` or `browse` (`packages/host/directory-picker/README.md:36`). |
| `UP-PKG-packages.host.directory-picker-native` | `dsh-host-directory-picker-native` | provider | Opens an operating-system folder chooser on the host's display (`packages/host/directory-picker-native/README.md:12`). |
| `UP-PKG-packages.host.directory-picker-browse` | `dsh-host-directory-picker-browse` | provider | Lists one directory level and creates a child directory (`packages/host/directory-picker-browse/README.md:12`). |
| `UP-PKG-packages.host.directory-picker-auto` | `dsh-host-directory-picker-auto` | other | Guesses once at boot whether the operator sits at the host and mounts one of the two (`packages/host/directory-picker-auto/README.md:12`). |
| `UP-PKG-packages.host.open-in-app` | `dsh-host-open-in-app` | consumer | Three routes: installed applications, their icons, and "open this workspace directory in that application" (`packages/host/open-in-app/README.md:12`, `:82`). |
| `UP-PKG-packages.host.plugin-inventory` | `dsh-host-plugin-inventory` | provider | A read-only remote method that lists loader entries and preset compositions (`packages/host/plugin-inventory/README.md:12`). |
| `UP-PKG-packages.telemetry.otel` | `dsh-otel` | definition and provider | `ctx.otel`: a factory for export channels; mounting it creates nothing (`packages/telemetry/otel/README.md:12`). |
| `UP-PKG-packages.host.product-telemetry-otel` | `dsh-host-product-telemetry-otel` | provider | `ctx.productTelemetry.emit()`: product usage events on one channel (`packages/host/product-telemetry-otel/README.md:12`). |

### 3.2 Behaviour

**Listener and routes.** The server listens as soon as it activates, on one concrete address; an unspecified address is a config error because "binding every interface would expose remote code execution to the network" (`packages/host/webserver/src/index.ts:437-438`). TLS is optional: both files are read before the socket binds, and bad material fails start-up with no fallback to HTTP (`packages/host/webserver/README.md:45`). Other plugins register routes; a duplicate path throws; matching is exact, then longest prefix, then the single fallback (`README.md:63`). Gzip is opt-in and never applied to the event stream (`README.md:41`; `src/index.ts:322`). The shipped command warns, and continues, when a non-loopback bind serves plain HTTP (`packages/bundle/web-app/src/index.ts:208`).

**Admission is done with events.** The server knows route classes but checks nothing itself. It dispatches `webserver/authorize` (first answer wins) and another package, Connection, answers with the source check and a credential lookup (`README.md:67`; `docs/subsystems/web-server.md:53`). With no listener mounted, a `required` route answers 503 and an `anonymous` route is reachable with no source check (`src/index.ts:787-788`; `README.md:140`). Two more events are middleware chains that run before routing: `webserver/request` and `webserver/upgrade` (`src/index.ts:68`, `:78`).

**Token and cookie.** A 32-byte random launch token is created once per process and never stored (`packages/host/authentication-token/src/browser-auth.ts:62-68`). `GET /?token=` answers 303 to `./` with a cookie, `cache-control: no-store` and `referrer-policy: no-referrer` (`:280-287`). The cookie is `v1.<payload>.<HMAC-SHA-256>`, named after a hash of the authority, `HttpOnly; SameSite=Strict`, `Secure` only when the listener itself speaks TLS (`:117-150`). The signing secret is a durable credential record, so cookies survive a restart for 30 days; deleting the record and restarting is the only revocation, and there is no sign-out (note `2026-08-24-browser-token-authentication.md:19-21`, `:41`). Every browser cookie maps to one shared owner peer (`src/index.ts:33`). A second exchange, `/connect`, gives an API client its own peer and one WebSocket (`src/index.ts:62-151`).

**Static shell.** The fallback serves files under one directory with a traversal check, a nine-entry type table, a weak SHA-1 ETag and `no-cache`; the index requires a peer and other files do not (`packages/host/frontend-static/src/index.ts:45-59`, `:95-113`, `:134-137`). The index gets `<base href="./">` so the application works under a proxy path prefix (`:148`).

**Directory picking.** `native` runs `osascript`, Zenity or KDialog, or a Windows COM dialog through a native binding in a child process (`packages/host/directory-picker-native/README.md:56`). `browse` lists directories only, name-sorted, at most 1,000 per level, follows links to directories, and creates one child directory non-recursively (`packages/host/directory-picker-browse/README.md:32-36`). `auto` picks `native` only for a loopback bind, no remote trusted host, no SSH launch and a display (`packages/host/directory-picker-auto/src/resolve.ts:55-61`).

**Open in app.** A fixed catalog of about 35 editors, Git clients, terminals and file managers is resolved to verified launchers once per process; the launch route accepts an owner peer, a catalog id and an absolute path of an existing directory, and starts the launcher detached with a scrubbed environment and no shell (`packages/host/open-in-app/README.md:54-64`, `:82`; `src/index.ts:175-176`, `:269-275`). Under an SSH launch the list is empty (`README.md:62`).

**Plugin inventory.** Each call reads the loader: entry id, module name, enablement, fiber phase, the current start-up diagnostic, and per-preset rows when a roster exists (`packages/host/plugin-inventory/README.md:32-44`; `src/types.ts:17-28`).

**Telemetry.** A consumer asks `ctx.otel` for a channel and owns its shutdown (`packages/telemetry/otel/README.md:26-28`). The product adapter creates one count-batched channel to a compiled-in default collector with a 21-second drain on unload (`packages/host/product-telemetry-otel/src/index.ts:46-58`, `:99-110`). What is sent, the defaults and the identity attached are set out in [../libs/opentelemetry.md §1.1](../libs/opentelemetry.md). None of the twelve packages exposes a tool to the model or persists session events.

### 3.3 Failure, retry, rollback and cleanup upstream

A bind failure or bad TLS material rejects the server's initialisation (`packages/host/webserver/README.md:81`). A handler that throws is answered 400, or its socket is destroyed, and the process continues (`README.md:81`). Disposal closes the server, force-closes connections, and destroys upgraded and half-handshaken TLS sockets it tracks itself, because an open event stream never ends on its own (`README.md:97`). If the token plugin fails to activate, protected routes answer 503 and are not opened (`packages/host/authentication-token/README.md:40`). Unloading the token plugin ends every browser's connections together (`README.md:80`). Telemetry is best effort: a full queue, a failed request or process exit loses records, and an unreachable collector can hold unload for 21 seconds (`packages/host/product-telemetry-otel/README.md:55`, `:102`). A wrong `native` choice opens a dialog on an unattended machine and ends in a retryable error (`packages/host/directory-picker-auto/README.md:109`). Nothing in this area is rolled back; nothing needs to be.

## 4. Upstream decisions dissected

The first five rows are the decision notes handed to this area; their paths are under `.agents/notes/implemented/architecture/`. The rest are visible only in code or READMEs.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `2026-08-24-browser-token-authentication` | Authenticate every call with a cookie obtained from a launch token; `Host` is not authority | Keep, change | The reasoning holds for any local server. Changed: tokens are one-time, cookies are per browser, sign-out exists | [DH-ADR-HOST-3](#dh-adr-host-3) |
| `2026-09-14-web-document-relative-app-routes` | One `<base>`; browser references are relative to the document; no forwarded-prefix header is read | Keep | Server-rendered pages behind a prefix-stripping proxy need the same rule | [DH-ADR-HOST-2](#dh-adr-host-2) |
| `2026-09-19-concrete-web-bind-address` | One concrete address; the unspecified address is refused; the bind literal is an accepted `Host` | Keep | Configured trust and actual reachability stay the same thing | [DH-ADR-HOST-2](#dh-adr-host-2) |
| `2026-09-23-native-https-web-listener` | Optional TLS on the listener; bad material fails start-up; the listener's own protocol decides `Secure` | Keep, change | Changed: a network listener without TLS is refused unless flagged, and a replaced certificate is picked up | [DH-ADR-HOST-2](#dh-adr-host-2) |
| `2026-09-25-session-log-otel-byte-limits` | Byte-bounded requests, one event per record, reject an oversized record whole | Keep, change | Byte bounds on every channel, one encode per record, a deadline that ends the request | [DH-ADR-HOST-7](#dh-adr-host-7) |
| Admission by events (`authorize`, `request`, `upgrade`) | The carrier knows no credential scheme (`README.md:67`) | Change | An unmounted listener leaves anonymous routes unchecked; middleware before admission is a way around it | [DH-ADR-HOST-1](#dh-adr-host-1) |
| Exact, longest prefix, fallback; duplicate route throws | Collisions are composition errors (`README.md:63`) | Keep | One owner per pattern is a rule of A19's route table ([api.md](api.md) §6.2); here an overlapping mount prefix is the same kind of error | [DH-D-HOST-1](#dh-d-host-1) |
| One fallback seat, index injection, in-process `readResource` | An SPA needs one index and start-up data (`README.md:75-77`) | Drop | Pages are rendered by the host; a miss is 404 ([DH-ADR-3](../design.md#dh-adr-3)) | [DH-ADR-HOST-1](#dh-adr-host-1) |
| A handler error is answered 400 | Never exit on a request (`README.md:81`) | Change | A server fault is 500; the containment is kept | [DH-D-HOST-1](#dh-d-host-1) |
| `/connect`, API peers, one socket per API peer | CLI and MCP clients share the HTTP gateway (`authentication-token/README.md:38`) | Drop | There is no RPC gateway; programs use stdio protocols ([DH-ADR-WIRE-1](../upstream/wire-and-api.md#dh-adr-wire-1)) | [DH-ADR-HOST-4](#dh-adr-host-4) |
| Desktop private credential | The renderer must never hold the credential (`authentication-desktop/README.md:12`) | Drop | No desktop application ([DH-ADR-18](../design.md#dh-adr-18)) | [DH-ADR-HOST-4](#dh-adr-host-4) |
| Durable signing secret; delete and restart to revoke; no sign-out | Cookies survive a restart (note `2026-08-24`, `:39-41`) | Change | A secret that can be read makes owners. Durable hashed session rows keep the restart behaviour; sign-out and "revoke all" delete rows | [DH-ADR-HOST-3](#dh-adr-host-3), [DH-ADR-HOST-12](#dh-adr-host-12) |
| Two picker interactions and a boot-time guess | A native dialog is nicer at the host's display (`directory-picker/README.md:32`) | Change | The guess cannot be proven and the dialog needs a native binding | [DH-ADR-HOST-8](#dh-adr-host-8) |
| Fixed application catalog, verified launchers, owner only, hidden under SSH | An unrestricted application list cannot say which programs accept a directory (`open-in-app/README.md:112`) | Keep, tighten | Opening things on the host at a browser's request needs a narrower rule | [DH-ADR-HOST-9](#dh-adr-host-9) |
| Inventory is a direct projection with no cache | A cache would be a second lifecycle truth (`plugin-inventory/README.md:56`) | Keep, extend | Add layer attribution, plugin processes and a short history | [DH-ADR-HOST-11](#dh-adr-host-11) |
| Mounting telemetry creates nothing; channels are caller-owned | The service has no collection policy (`telemetry/otel/README.md:26`) | Keep, change | Consent moves into the factory: no permit, no channel | [DH-ADR-HOST-5](#dh-adr-host-5) |
| A default collector is compiled in; analytics is on with no control | Product events reach the vendor (`product-telemetry-otel/src/index.ts:47`; `packages/client/product-analytics/src/index.ts:36`) | Drop | [DH-ADR-9](../design.md#dh-adr-9) | [DH-ADR-HOST-6](#dh-adr-host-6) |
| Opt-in gzip with fixed exclusions; weak ETag with `no-cache` | Compression must not break streams; files change between builds (`frontend-static/src/index.ts:111`) | Keep; change | Kept as [DH-ADR-COMPRESSION-1](../libs/compression.md#dh-adr-compression-1); embedded files are immutable per build | [DH-D-HOST-1](#dh-d-host-1) |

## 5. Tech debt and its resolution

All 35 units are bullets under "Known Limitations and Deferred Work" in the package READMEs. They fall into nine causes. Per-unit reasons are in the ledger rows.

| Group (count) | Units (`UP-DEBT-limit.packages.` prefix omitted) | Cause | How the Go design answers it |
|---|---|---|---|
| Desktop-only component (1) | `host.authentication-desktop.1` | A credential path for the Electron shell | Not applicable: the component is not built ([DH-ADR-HOST-4](#dh-adr-host-4)) |
| Browser credentials (3) | `host.authentication-token.1` to `.3` | One shared peer; cookies in clear text; a token query on other paths left to route owners | Resolved: a peer per browser; a network listener without TLS is refused by default; a `token` parameter anywhere but the exchange is rejected before routing ([DH-D-HOST-2](#dh-d-host-2)) |
| A minimal listener (3) | `host.webserver.1` to `.3` | No certificate reload; checks only exist if a listener is mounted; fixed socket settings | Resolved: certificates are re-read when the files change; the source check is part of the listener; timeouts and limits are config ([DH-D-HOST-1](#dh-d-host-1)) |
| Guessing where the operator sits (8) | `host.directory-picker-auto.1` to `.4`, `host.directory-picker-native.1` to `.4` | A dialog on the host's display, chosen from launch-time signals that cannot prove who is looking | Resolved by removal: one interaction that works wherever the browser is ([DH-ADR-HOST-8](#dh-adr-host-8)) |
| Directory browser limits (4) | `host.directory-picker-browse.1` to `.3`, `host.directory-picker.1` | Node hides the Windows hidden attribute; no level above a drive; no root restriction; one ancestry chain | Resolved: Go reads the attribute; a roots level lists drives or configured roots; roots are enforced with `os.Root` ([DH-D-HOST-8](#dh-d-host-8)) |
| Serving a single-page application (2) | `host.frontend-static.1`, `.2` | A starter type table; no pathname routes without an explicit rule | Resolved: one harness type table ([../libs/mime.md](../libs/mime.md)); every page is an explicit route and there is no fallback ([DH-D-HOST-1](#dh-d-host-1)) |
| Application catalog (4) | `host.open-in-app.1` to `.4` | Build-time catalog; macOS known paths only; icons extracted by platform tools; resolved once per process | `.1` and `.4` resolved: operator-defined entries in config, and re-resolution on demand. `.2` resolved since the audit: a moved macOS application is found by a Spotlight query run as a child process. `.3` **carried** as a scope boundary: a pure-Go binary has no icon extractor ([DH-ADR-HOST-9](#dh-adr-host-9)) |
| Inventory snapshot (3) | `host.plugin-inventory.1` to `.3` | No history or subscription; no layer attribution; presets absent, not empty | Resolved: a change event and a bounded history; the supplying layer per entry; presets always present with an explicit availability ([DH-D-HOST-10](#dh-d-host-10)) |
| Telemetry left to callers (7) | `telemetry.otel.1` to `.3`, `host.product-telemetry-otel.1` to `.4` | Consent, field selection and redaction are the caller's; count-only batching; memory-only queue | Six resolved: a channel needs a permit; records come from a closed typed catalog; every channel is byte-bounded; each batch reports its outcome so a consumer with a durable source can resume. `product-telemetry-otel.2` **carried**: analytics stays best effort on purpose ([DH-ADR-HOST-7](#dh-adr-host-7)) |

Totals: 31 resolved, 1 not applicable, 3 carried, each a scope boundary owned by stage `DH-S-47` of [../implementation-plan.md](../implementation-plan.md#dh-s-47). Since the audit, `open-in-app.2` is resolved by the Spotlight query of [DH-ADR-HOST-9](#dh-adr-host-9) (finding Q3-11); its ledger row is the debt reviewers' to re-issue.

## 6. Go design

### 6.1 Shape

Packages are under `src/harness/internal/host` ([../design.md §4.2](../design.md#42-go-module-layout)). A contract package holds interfaces, keys and types; a provider is a component.

| Package | Kind | Provides | Injects | Why it exists |
|---|---|---|---|---|
| `listen` | contract and component `host.listen` | `listen.Key` (`Listener`), `Mount`, `Class`, `Peer`; bail key `listen.Authenticate` | config, logger, boot readiness (A21) | Owns the socket, TLS, the admission pipeline, compression and shutdown. Area A19's `web` (component `host.web`, key `web.Key`) mounts its route table here and is not redefined |
| `auth` | component `host.auth` | `auth.Key` (`Mint`, `Issue`, `SignOut`, `RevokeAll`); answers `listen.Authenticate`; the `/auth/` mount | `listen.Key`; a machine-state table (A07) | Launch tokens, browser sessions and their peers |
| `assets` | component `host.assets` | the `/assets/` mount | `listen.Key`; the embedded file system built by C1 and C2 | Serves what pages reference, and nothing else |
| `launch` | component `host.launch` | the runtime record, the control endpoint, the printed URL, the browser hand-off | `listen.Key`, `auth.Key`, `subprocess.HostKey` (A09) | How a person, or a second `dsh`, reaches the host |
| `sharing` | component `host.sharing` | `sharing.Key` (`Gate`); notify key `sharing.Changed` | config | The one place that says whether data may leave |
| `telemetry/otlplog` | component `host.otlplog` | `otlplog.Key` (`Service`) | `sharing.Key`, `egress` (A23) | The exporter of [../libs/opentelemetry.md](../libs/opentelemetry.md) |
| `telemetry/report` | component `host.report` | `report.Key` (`Reporter`) | `otlplog.Key`, `sharing.Key`, installation id (A17) | The channels other areas report product and operational events through |
| `diag` | library and component `host.diag` | `diag.Install`, `diag.Fatal`, `diag.Key` | nothing: it must work before the tree exists | The fatal diagnostic and crash report |
| `dirbrowse`, `dirbrowse/local` | contract and provider | `dirbrowse.Key` (`Service`) | config | A seam, so that a remote world (A10) can browse its own machine |
| `openpath` | component `host.openpath` | `openpath.Key`; two owner-only routes through `web.Key` (A19) | `subprocess.HostKey`, workspace registry (A07), `listen.Key` | Opens a workspace directory in a host application |
| `inventory` | component `host.inventory` | `inventory.Key`; notify key `inventory.Changed` | loader view (A21); plugin host (A22) and presets (A27), both optional | What is composed in this host |

```go
package listen
type Class uint8 // Required (zero value), Anonymous, Callback, Deny
type Mount struct {
    Prefix   string                         // "/" for A19's route table, "/assets/", "/auth/"
    Classify func(r *http.Request) Class    // asked before any credential is read; a panic counts as Deny
    Stream   func(r *http.Request) bool     // long-lived: own write deadlines, never compressed, ends when Done closes
    Serve    http.Handler                   // called only after admission; the Peer is in the request context
}
type Listener interface {
    Mount(owner *cordis.Context, m Mount) error // an effect of owner's fiber; an overlapping prefix is an error
    Origin() string; Secure() bool; Mode() Mode // Mode: Local or Remote
    PeerRule() PeerRule                         // Exact, BestEffort or NotApplied on this machine (DH-ADR-HOST-12)
    Ready() <-chan struct{}                     // closed when boot reports the required entries loaded
    Done() <-chan struct{}
}
type Peer interface { ID() string; Principal() access.Principal; Context() context.Context; FormToken() string } // one per browser; the context ends on revocation
func PeerFrom(ctx context.Context) (Peer, bool)
// One cordis bail key, declared here: Authenticate, named "listen/authenticate": *Credentials in, Peer out.
// access.Principal, access.Rule and web.Route are area A19's (api.md §6.1) and are used as written.

package sharing
type Sender struct { Kind Kind; About Description; Enabled func() bool; Endpoint func() string }
type Gate interface {
    Register(ctx *cordis.Context, s Sender) error // one sender per kind
    Permit(k Kind) (Permit, bool)                 // false: off-switch set, kind off, no endpoint, or not registered
    Off() (off, lockedByEnvironment bool)
    Senders() []Status                            // for the settings page and the generated catalog
}
type Permit interface { Kind() Kind; Context() context.Context } // the context ends when consent is withdrawn
package otlplog // Options and Record as in libs/opentelemetry.md §6, plus OnBatch
type Service interface { NewChannel(ctx *cordis.Context, p sharing.Permit, o Options) (*Channel, error) }
func (c *Channel) Emit(r Record) (seq uint64, ok bool)
type Outcome struct { First, Last uint64; Records, Bytes, Rejected int; Status Status } // Delivered, Failed, Dropped, Cancelled
package report
type Reporter interface { Analytics(e Event); Telemetry(e Event) } // e's type must be in the catalog
func Declare[T Event](kind sharing.Kind, name string)               // called by the package that owns the event
package dirbrowse // "" lists the roots level
type Service interface { List(ctx context.Context, path string) (Listing, error); CreateDirectory(ctx context.Context, parent, name string) (Entry, error) }
```

**The admission pipeline** is one fixed chain inside `listen`; no component can insert a step or remove one. What happens after it (finding the route, the access rule for a non-owner, decoding the form) is A19's `web` and `access` ([api.md](api.md) §6.1, "How a request runs").

```
request -> limits -> Host check -> token parameter rule -> mount by longest prefix -> class
        -> origin check (not for callback) -> [required] listen.Authenticate -> Peer in context -> Mount.Serve
```

1. **Limits.** Header size, header read timeout and idle timeout from config. There is no server-wide write timeout; a `Stream` request sets its own deadlines.
2. **`Host` check and peer rule, on every request.** `Host` must be loopback, the bind literal (any port), or a configured trusted host, parsed with `net/netip` ([../libs/ipaddr.js.md](../libs/ipaddr.js.md), `DH-LIB-IPADDRJS-R6`). Failure is 403. On a loopback connection the listener also asks A09, once per accepted connection, which process tree the connecting process belongs to ([DH-D-EXEC-2](execution.md#dh-d-exec-2)); a peer inside an untrusted tree is answered 403 `auth/untrusted-peer`, whatever cookie it carries ([DH-ADR-HOST-12](#dh-adr-host-12)).
3. **Token parameter rule.** `GET` on the document root with exactly one valid `token` is the exchange. A `token` parameter on any other request is 400 and is never passed on or logged.
4. **Mount and class.** No mount is 404. The mount's `Classify` gives the class; `deny` is 403.
5. **Origin check, for every class except `callback`.** `Sec-Fetch-Site: cross-site` and `Origin: null` are refused, and an `Origin` must match the `Host`. Every method other than `GET` and `HEAD`, and every socket upgrade, must be same **origin**, not merely same site: `Sec-Fetch-Site: same-origin`, or else an `Origin` equal to one of the listener's own origins, or else it is refused; another port on `localhost`, such as a development server the agent started, is same-site and its post would carry the cookie. Those requests also pass `http.CrossOriginProtection` (`net/http/csrf.go:44`; the type is in the standard library since Go 1.25, `api/go1.25.txt:66`, so it exists at the module floor `go 1.26.0`). Failure is 403. The form token A19's `web` adds on top ([api.md](api.md), `DH-ADR-API-9`) is `Peer.FormToken()` ([DH-D-HOST-2](#dh-d-host-2)).
6. **Credential.** `anonymous` and `callback` skip it and get no peer. `required` dispatches `listen.Authenticate`; no answer is 401, and no answerer mounted is 503. The peer goes into the request context.

**Authentication.** `auth.Mint` returns a URL with a fresh 32-byte token that is valid once and for `LaunchTokenTTL`. The exchange answers 303 to `./` with `Cache-Control: no-store`, `Referrer-Policy: no-referrer` and a cookie `HttpOnly; SameSite=Strict; Path=/`, with `Secure` exactly when the listener speaks TLS. The cookie's value is a random 256-bit **session id** and nothing else. The host keeps one row per browser session in a machine-state table (A07): the SHA-256 of the id, the audience, the principal, a browser label, issue and expiry times, and a random 256-bit **form key** made with the row. `Peer.FormToken()` is HMAC-SHA-256 of the peer's id under that row's form key; A19's `web` puts it in every page and checks it on every state-changing request. It is not the signing secret that [DH-ADR-28](../design.md#dh-adr-28) rules out: it is one key per browser session, it authenticates nothing (the cookie does), it only proves that a form came from a page this host rendered for that same session, and knowing it gives no way to make a cookie or to act as any session. It ends with its row. A request is admitted when the hash of its cookie equals a live row of this audience, compared in constant time. There is no signing secret, so nothing that can be read from the home, the secret store or the keychain makes a cookie; making one without a launch token takes a write to the home's database ([DH-ADR-HOST-12](#dh-adr-host-12)). The cookie's name contains a hash of the audience; cookies with the harness prefix and another audience that arrive with the exchange are expired in the same response (`DH-LIB-ELECTRON-R6`). Each row is one peer, created by the exchange with the principal `owner`; its `ID` is the stable id A19 counts streams by. `auth.Issue(principal, ttl)` creates a row and a cookie of its own name for another principal kind, so that a component such as session sharing (X2) implements no cookie of its own; a browser holding both is admitted as the owner. `POST /auth/sign-out` (`SignOut`) deletes one browser's row; `POST /auth/revoke-all` (`RevokeAll`) deletes every row of the audience. Both end the affected peers' contexts. Each new sign-in is announced on the host's terminal and in every open tab, beside the revoke-all action.

**Reaching the host.** When boot reports the required entries loaded (`launch.App.Ready`), `launch` registers the managed variable `DSH_WEB_URL` (the origin, never a link) with A09's environment registry, mints a URL, writes it to the terminal (never through the logger) and, unless disabled, started through SSH or without a display, hands it to the system browser through A09's starter as a detached, trusted launch with a scrubbed environment ([../libs/open.md](../libs/open.md)). It then writes the runtime record `<home>/run/web-<hash of address>.json` (origin, process id, start time, socket path) while holding a kernel lock on a sibling lock file (A08's `fsx` lock), and listens on a Unix domain socket in the same owner-only directory. That **control endpoint** speaks newline-delimited JSON-RPC through the `sdk/jsonrpc` codec of module `src/sdk` ([DH-D-SDK-1](sdk-acp-typert.md#dh-d-sdk-1)). It has a closed set of three methods, and none returns a credential: `host.status` (origin, uptime and the summary of running work that `dsh stop` shows before it stops anything), `host.stop` (it calls boot's `launch.App.Stop(Requested, 0)`; with `ask` set it returns the same work summary and stops nothing, and that summary is also callable in process) and `launch.open`, which makes the host itself mint a link and print it on its own terminal or hand it to the browser, exactly as at start. The caller learns only which of the two happened, or `launch/no-channel` when the host has neither a terminal nor a display. No component can add a method, and every connection passes the peer rule before a request is read. A second `dsh` for the same home and address finds the record, sees the lock held, calls `launch.open` and exits. A record whose lock is free is stale and is replaced. A second `dsh` is never handed the link itself: decided by the owner ([DH-ADR-28](../design.md#dh-adr-28)). `launch.open` takes one flag, `print`, which a second command started with `--no-open` sets: the running host then prints the link on its own terminal and opens no browser.

**The sharing gate.** A sender registers its kind with a description (what is sent, to whom, under which identity) and two readers: its own enabling setting and its endpoint. `Permit` answers yes only when the off-switch is clear, the setting is on and an endpoint exists. The permit's context ends the moment any of the three changes, so a sender that derives its requests from it stops without a "last flush". The off-switch is the live config field `sharing.Off` or the environment variable `DSH_SHARING_OFF` set to any non-empty value, even `0`; the variable wins and the settings page shows the switch as locked. Each entry of `Senders()` also carries the settings address (entry and field) of the kind's switch and destination, so the sharing page (C7) keeps no table of its own. This table is the complete list: every kind is off until its own setting is turned on, and the one off-switch stops every one of them. A refused `Permit` carries one code: `sharing/off` (the switch is set), `sharing/disabled` (the kind's own setting is off), `sharing/no-endpoint` or `sharing/unregistered`.

| Kind | What is sent, and by whom | Default | Off-switch |
|---|---|---|---|
| `session-upload` | Session events on a confirmed feedback submission (A06) | off | stops it |
| `session-log-contribution` | Session-log lines attached to a vendor's model request (A05) | off | stops it |
| `model-request-extras` | Installation id, session id and purpose, inventory, and the client name and version (A04). The vendor sees the bare word `dsh` unless the user turns the version on ([DH-ADR-32](../design.md#dh-adr-32)) | off | stops it |
| `feedback` | Feedback text and rating (A17); it also previews | off | stops it |
| `analytics`, `telemetry` | Product usage events; operational events (this area) | off | stops it |
| `update-check` | The automatic version query (A24); it counts as data leaving the machine ([DH-ADR-32](../design.md#dh-adr-32)) | off | stops it |
| `account-notices` | Polling the vendor account for notices, and the displayed, clicked and dismissed reports (A17) | off | stops it |
| `translation` | One paragraph of model reasoning and two language codes to a public translation endpoint (X6). The provider is specified and not in the shipped bundle; an operator adds it in a layer ([DH-ADR-36](../design.md#dh-adr-36)) | off | stops it |
| `speech-audio` | One recording to a remote speech provider the user configured (X4) | off | stops it |
| Not a sender | A model request to a provider the user configured; web fetch and search tools; MCP, SSH and plugin traffic; a clicked remote image; sign-in in the system browser; an explicit `dsh update`, which names the address before it connects (A24); a live share the user created, whose invited browser connects to this host (X2); a code-host status read the user configured, through the user's own tool (X1) | n/a | No: the switch stops senders only, that is, what the harness would send on its own. Each of these is the product doing what the user asked ([DH-ADR-27](../design.md#dh-adr-27)) |

**Reporting channels.** An event is a Go struct declared in a catalog by the package that owns it, under kind `analytics` or `telemetry`. Declaration fails at start-up if a field is anything but a boolean, an integer, a duration bucket or a string with a closed set of values; there is no free text. `Reporter` drops an event when its kind has no permit, and creates its channel at the first permitted event. The resource is `service.name`, `service.version`, operating system family, architecture and the anonymous installation id, read lazily so that no id exists before the first accepted record. The built-in `telemetry` catalog is `process.start`, `process.fatal` and `component.failed`, each with codes only.

**Fatal diagnostic.** `diag.Install(home)` is the first call in `main` for every profile. Layer one: it opens a file under `<home>/crash/` and passes it to `debug.SetCrashOutput` (`runtime/debug/stack.go:49`; in the standard library since Go 1.23, `api/go1.23.txt:87`, so at the floor `go 1.26.0`), so that what the Go runtime prints for an unrecovered panic or a runtime fault also lands in a file. Layer two: `diag.Fatal(source, err)` is called by boot when a required entry fails and by the harness's goroutine helper when it recovers a panic. It writes the report, prints one diagnostic naming the source, the first error line and the report path, runs the tree's release under a timeout, and exits with status 70, or 78 when a required entry failed ([app-cli.md](app-cli.md) §6.2). A report holds time, version, operating system, profile, source, the error chain, all goroutine stacks and the last 64 KiB of the log ring. An empty crash file is removed on a clean exit; the ten newest are kept.

**Directory browser.** `List` returns one level: directories only, name-sorted, at most `MaxEntries` with a `truncated` flag, each with an absolute path and a `hidden` flag (dot prefix, and on Windows `FILE_ATTRIBUTE_HIDDEN`, `syscall/types_windows.go:124`), plus the home directory and the ancestor chain. Links to directories are followed; broken and cyclic links are skipped. A path that is not fully qualified is refused. The level above the top is a **roots level**: the configured `Roots`, or on Windows the drive letters that answer a bounded probe. With `Roots` set, every operation goes through an `os.Root` handle for its root. `CreateDirectory` makes one child of an existing parent from a single path segment.

**Open in a host application.** The catalog is embedded data plus operator entries from config; each entry resolves to a verified launcher. Its two routes (list, open) are declared through A19's `web.Routes` with `access.Owner()`, and are registered only in local mode, outside an SSH launch and with a display. The open route accepts a catalog id and a path that must be an existing directory inside a workspace A07 has registered, and starts the launcher through `subprocess.HostKey` as an argument list with a scrubbed environment, detached, watched for `LaunchWatch`.

**Plugin inventory.** `Snapshot` reads, on each call: loader entries (id, component name, enabled, phase, current diagnostic, supplying layer); plugin processes (directory, manifest name and version, trust level, capabilities requested and granted, process state, restart count, last exit); and presets with an explicit `Available`. A ring of the last 200 phase changes answers "what failed before it was removed". It has no method that changes anything.

### 6.2 Contracts

| Event (dispatch form) | Replaces | Payload and rule |
|---|---|---|
| `listen.Authenticate` (bail) | `UP-EVT-webserver/authorize` | Cookie and audience in, a peer out. The source check and the class are not part of it; what a non-owner may do is `access.Decide` (A19) |
| none | `UP-EVT-webserver/request`, `UP-EVT-webserver/upgrade` | No middleware before admission. A WebSocket is an ordinary `Stream` request of a mount |
| none | `UP-EVT-webserver/entry-url` | `auth.Mint` is called by `launch`; there is one credential scheme |
| none | `UP-EVT-webserver/index-inject` | Start-up data is rendered into the page by the page shell (C1) |
| `sharing.Changed`, `inventory.Changed` (notify) | new | Kind and new state; entry or plugin id and new phase |

| Config struct | Fields and defaults (`live` = applied without restart) | Replaces |
|---|---|---|
| `listen.Config` | `Bind` `127.0.0.1`; `Port` 3080; `TLS{CertFile, KeyFile}` none; `AllowInsecureRemote` false; `TrustedHosts` none; `PublicURL` none; `Compression{Mode none, Level 1, MinBytes 1024}`; `ReadHeaderTimeout` 10 s; `IdleTimeout` 120 s; `MaxHeaderBytes` 64 KiB; `ShutdownGrace` 5 s | `UP-CFG-dsh-host-webserver` |
| `auth.Config` | `CookieMaxAge` 30 days (minimum 1); `LaunchTokenTTL` 10 minutes | `UP-CFG-dsh-host-authentication-token` |
| none | The desktop provider is not built | `UP-CFG-dsh-host-authentication-desktop` |
| none | Assets are embedded; there is no directory to point at | `UP-CFG-dsh-host-frontend-static` |
| `dirbrowse.Config` | `MaxEntries` 1000; `Roots` none | `UP-CFG-dsh-host-directory-picker-browse` |
| `openpath.Config` | `Enabled` true (live); `ProbeTimeout` 10 s; `LaunchWatch` 1 s; `RefreshAfter` 60 s; `Apps` none | `UP-CFG-dsh-host-open-in-app` |
| `report.Config` | `Analytics{Enabled false, Endpoint ""}` and `Telemetry{Enabled false, Endpoint ""}` (live); `Headers` as credential references; `Gzip` true; `MaxQueue` 2048; `MaxBatchRecords` 512; `MaxRequestBytes` 1,000,000; `FlushInterval` 30 s; `RequestTimeout` 15 s; `DrainTimeout` 3 s | `UP-CFG-dsh-host-product-telemetry-otel` |
| `sharing.Config`, `launch.Config` | `Off` false (live); `Open` true, `Print` true | new |

Validation is strict decoding plus: `Bind` is one address literal and not unspecified; `TLS` files come as a pair; a non-loopback `Bind` needs `TLS` or `AllowInsecureRemote`; an endpoint is `https`, or `http` only to a loopback address; `MaxRequestBytes` is at most 4,000,000. Each live `Enabled` field appears in A07's settings form with the sender's description beside it.

| Service key | Interface | Replaces |
|---|---|---|
| `listen.Key` | `listen.Listener` (socket facts and mounts); route registration is `web.Key` (A19) | `UP-SVC-webServer` |
| `dirbrowse.Key` | `dirbrowse.Service` | `UP-SVC-directoryPicker` |
| `otlplog.Key` | `otlplog.Service` | `UP-SVC-otel` |
| `report.Key` | `report.Reporter` | `UP-SVC-productTelemetry` |

**Persisted data.** Browser sessions, as hashes only, in an A07 machine-state table until they expire or are deleted; the runtime record and its lock (`<home>/run/`); crash reports (`<home>/crash/`, owner-only). Each session row's form key is stored with that row and deleted with it. No signing secret and no session event. **Tools exposed to the model:** none, as upstream. `Listener.Origin()` is the one canonical origin that [DH-ADR-SECURITY-7](../upstream/security.md#dh-adr-security-7) item (d) asks to be published to the model's context; publishing it is A03's.

### 6.3 Ownership, failure, retry, rollback and cleanup

| Resource | Owner (who releases it) | Access (who may use it) | Released when |
|---|---|---|---|
| The listening socket, accepted connections, tracked WebSocket connections | The `host.listen` fiber | Any admitted peer | Unload: stop accepting, close `Done` so `Stream` handlers return, `Shutdown` within `ShutdownGrace`, then close what remains |
| A mount | The fiber that called `Mount` | Requests under its prefix | That fiber unloads; the prefix then answers 404 |
| A browser peer | The `host.auth` fiber | Requests carrying that browser's cookie, from a process outside every untrusted tree | Sign-out, revocation, expiry, or unload; its streams end with it |
| The browser, its tabs and its cookie jar | The user | The host can ask the operating system to open one URL | Never by the host |
| The runtime record, its lock, the control socket | The `host.launch` fiber | Any process of the same operating-system user outside every untrusted tree; access gives it no owner authority | Unload; a crash leaves a record whose lock is free, which the next start replaces |
| A permit, a channel, its queue and goroutine | The sender's fiber | Only that sender | Consent withdrawn (no drain), or unload (drain for `DrainTimeout`) |
| The collector | Its operator | Any channel given its URL | n/a: nothing on it is the harness's to undo |
| A launched application | The user, once the watch window has passed | n/a | Never by the host: it is detached on purpose |
| Crash reports | `diag` | The owner, on disk | Pruned to the ten newest at start |

| Failure | What happens | Undone, or left uncertain |
|---|---|---|
| The port is taken | By a live host of this home: reuse it. Otherwise start-up fails with a message naming the port; the host never moves to another port | Nothing was opened |
| TLS material is unreadable or mismatched | At start: the component fails and nothing listens. After a replacement at run time: the last good pair keeps serving and one error is logged | No fallback to HTTP in either case |
| The session table is unavailable | `host.auth` fails to load; `required` routes answer 503; assets still serve | Nothing is admitted |
| The connecting process cannot be identified | On the control endpoint the connection is refused. On the listener the request is served, `PeerRule()` reports `NotApplied`, and the status page says so | **Uncertain:** on that machine the peer rule protects nothing |
| A handler panics | Recovered; 500 if nothing was written, otherwise the connection is closed; logged with the route pattern | The handler's side effects stay; the process continues |
| The browser cannot be opened | One warning without the URL; the printed URL remains ([../libs/open.md](../libs/open.md), `DH-LIB-OPEN-R4`) | The host keeps running |
| The off-switch is set or a setting turned off mid-send | The permit's context ends: the request in flight is cancelled, queued records are discarded and counted | **Uncertain:** whether the cancelled request arrived. Records already posted cannot be recalled |
| The collector is down or slow | Retries inside the request deadline, then the batch is dropped and reported through `OnBatch` | Nothing is re-queued by the channel; a consumer with a durable source resends |
| The host dies | Queued records are lost. The crash file holds what the runtime printed. The next start says the last run ended uncleanly and names the report | **Uncertain:** work in flight, which the session log reports ([../design.md §5](../design.md#5-failure-retry-rollback-and-cleanup)) |
| The report cannot be written | The diagnostic is still printed, with the write error | The exit code is unchanged |
| A listing is cancelled or a directory is unreadable | The scan stops with the request; `directory-unreadable` with the path | Nothing was created |
| A launcher is missing or exits early | That entry is re-resolved once and dropped if nothing proves it; the route answers a typed error | No retry of the launch |

## 7. Decisions

<a id="dh-adr-host-1"></a>
### DH-ADR-HOST-1 Admission is a fixed pipeline inside the listener; the credential lookup is its only extension point

- **Decision.** The six steps of §6.1 run in that order in the listener. `listen.Authenticate` is the only place another component can answer. There is no middleware event, no fallback seat and no in-process read that skips the checks. A handler reaches the listener only as a mount that states the class of every request, and a `required` request is never served without a peer in its context. What a principal may then do is decided once, by A19's `access.Decide`, from the rule every `web.Route` must carry.
- **Why.** Upstream's source check exists only while another package's listener is mounted, and its two waterfall events run before admission (`packages/host/webserver/README.md:67`, `:140`). A check that can be unmounted, or stepped around, is not a control.
- **Tradeoff.** A new credential scheme must fit "cookie in, peer out". A component that wants to observe every request cannot; it reads the access log. Two areas share one request path, so the seam between them is the `Mount` type and nothing else.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-HOST-4`, `DH-T-HOST-9`, `DH-T-HOST-13`, [DH-E-HOST-1](#dh-e-host-1).

<a id="dh-adr-host-2"></a>
### DH-ADR-HOST-2 A network listener needs TLS or an explicit flag, and remote mode switches host-local actions off

- **Decision.** (a) The bind address is one literal, loopback by default, never unspecified. (b) A non-loopback bind is refused at start-up unless TLS is configured or `AllowInsecureRemote` is set; with the flag, a warning is printed at every start and shown in the UI. (c) A host name other than the bind literal is accepted only from `TrustedHosts`. (d) `PublicURL` changes what is printed, never what is trusted; no forwarded header is read. (e) Every page carries one `<base>` and every reference and redirect is document-relative, so one binary works at the origin root and under a prefix-stripping proxy. (f) Certificate files are re-read when they change. (g) `Listener.Mode()` says local or remote, and a component whose action only makes sense at the host machine does not register it in remote mode.
- **Why.** The host runs commands as the user. [DH-ADR-SECURITY-7](../upstream/security.md#dh-adr-security-7) item (b) asks for the refusal; upstream only warns (`packages/bundle/web-app/src/index.ts:208`), and a warning in a terminal nobody reads sends the launch token and cookie in clear text.
- **Tradeoff.** An operator behind a TLS-terminating proxy on another machine must set the flag knowingly. The harness obtains and renews no certificate: the operator supplies the pair, because issuing one needs a name, a challenge path and a trust decision that belong to whoever runs the machine. A second listener for another purpose is a further mount of the same component (`host.listen@ingress`, for the webhook receiver of X5) with its own address and TLS and only `callback` mounts; it shares nothing with the UI listener. Inference: over plain HTTP from another machine the browser gives the UI no notifications and no HTTP/2, so the stream cap of [DH-ADR-GOUISTACK-5](../libs/go-ui-stack.md#dh-adr-gouistack-5) applies.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-HOST-1`, `DH-T-HOST-2`, `DH-T-HOST-3`, [DH-E-HOST-2](#dh-e-host-2).

<a id="dh-adr-host-3"></a>
### DH-ADR-HOST-3 Launch tokens are one-time, sessions are per browser, and a second `dsh` asks the running host to open a new link

- **Decision.** A launch token works once and expires after `LaunchTokenTTL`. Each exchange creates one browser session, so each browser is its own peer and can be signed out alone. Sessions are durable rows, so a restart does not sign browsers out. A second `dsh` asks the running host, over the control endpoint, to open or print a fresh link itself.
- **Why.** Upstream's token is valid for the whole process lifetime (`browser-auth.ts:62-68`), so a URL left in a shell history or a screenshot stays a credential. One shared peer means one browser cannot be revoked (`authentication-token/README.md:80`). The control endpoint answers the question [../libs/electron.md §7](../libs/electron.md) left for this area.
- **Tradeoff.** A printed URL that is not opened within ten minutes is dead; `dsh` run again makes the host print another, in the host's terminal and not in the caller's. A host with neither a terminal nor a display must be restarted from a terminal to sign a browser in. While it lives the link is a bearer value: whoever opens it first is the owner ([../design.md §6](../design.md#6-security-model)). What keeps a process started for the model from being that someone is [DH-ADR-HOST-12](#dh-adr-host-12).
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-HOST-10`, `DH-T-HOST-11`, `DH-T-HOST-12`, `DH-T-HOST-16`, `DH-T-HOST-31`, [DH-E-HOST-1](#dh-e-host-1).

<a id="dh-adr-host-4"></a>
### DH-ADR-HOST-4 No desktop credential provider and no API-client handshake on the listener

- **Decision.** `authentication-desktop` is not built. The `/connect` exchange, API peers and the one-socket rule are not built.
- **Why.** The first existed to keep a credential out of an Electron renderer; there is no desktop application ([DH-ADR-18](../design.md#dh-adr-18)). The second existed so that programs could use the browser's RPC gateway; there is no gateway, and a program drives the harness over stdio ([DH-ADR-WIRE-1](../upstream/wire-and-api.md#dh-adr-wire-1)).
- **Tradeoff.** A program cannot attach to a running web host over HTTP. If remote sharing later needs a second credential kind, it answers `listen.Authenticate` with a peer of its own principal kind; nothing here changes.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-16](../design.md#dh-o-16). **Verified by.** `DH-T-HOST-14`.

<a id="dh-adr-host-5"></a>
### DH-ADR-HOST-5 One sharing gate: every sender is registered, has its own setting, and needs a permit; one off-switch wins

- **Decision.** As §6.1. A component may make a request whose purpose is to send data to a vendor or collector only while it holds a permit. The exporter takes a permit as an argument. The table of kinds is the scope of the off-switch, and a new sender is a new row.
- **Why.** [DH-ADR-9](../design.md#dh-adr-9) fixes the rule; this makes it checkable. Upstream's disable variable patches one configuration row by name and misses analytics ([../libs/opentelemetry.md §1.1](../libs/opentelemetry.md)).
- **Tradeoff.** Three areas already keep their own enabling field (A04, A05, A06); the gate reads them and does not own them, so the settings page gathers fields from several forms. The rule is only as strong as the list: a sender that builds its own HTTP client escapes it, which is why `DH-T-HOST-19` asks A23 for a source check and `DH-T-HOST-17` records every outbound request.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-HOST-17`, `DH-T-HOST-18`, `DH-T-HOST-19`, [DH-E-HOST-3](#dh-e-host-3).

<a id="dh-adr-host-6"></a>
### DH-ADR-HOST-6 No collector is compiled in; analytics and telemetry are closed catalogs with no account or device identity

- **Decision.** Every endpoint defaults to empty, and a kind without an endpoint is shown as unavailable. Analytics and telemetry records are typed events with enumerated fields. The only identity is the anonymous installation id. Account id, device id, processor model, core counts and memory size are never attached.
- **Why.** These answer the two questions [../libs/opentelemetry.md](../libs/opentelemetry.md) left to this area. Upstream's default endpoint is the upstream vendor's (`product-telemetry-otel/src/index.ts:47`); sending a Go user's data there, or anywhere chosen in source, is a decision nobody made. Upstream's analytics attaches the signed-in account (`packages/client/product-analytics/src/index.ts:121-122`) and leaves string fields to callers (`product-telemetry-otel/README.md:104`).
- **Tradeoff.** Until a publisher names an endpoint, opting in does nothing but say so. A custom model name or plugin name cannot be reported except as "custom". Which events exist is for the UI areas; upstream's list is in `packages/client/product-analytics/src/events.ts:6-57`.
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-HOST-21`, `DH-T-HOST-22`, [DH-E-HOST-3](#dh-e-host-3).

<a id="dh-adr-host-7"></a>
### DH-ADR-HOST-7 Channels stay memory-only and report every batch's outcome

- **Decision.** The exporter is the one of [DH-ADR-OTEL-1](../libs/opentelemetry.md#dh-adr-otel-1), unchanged in its requirements. This area adds: a channel needs a permit; `Emit` returns a sequence number and `OnBatch` reports each batch as delivered, failed, dropped or cancelled; the drain on unload is 3 seconds, not 21; an ordinary channel's request is at most 1,000,000 bytes.
- **Why.** A06's session upload keeps its progress in the session log and needs to know which records arrived ([session-views.md](session-views.md) §10, item 4). Its durable source is the log, so the channel needs no second one. A 21-second wait on Ctrl-C is not acceptable for an interactive program, as upstream's README itself says (`product-telemetry-otel/README.md:55`).
- **Tradeoff.** Analytics and telemetry records are lost on overflow, failure and exit; the loss is counted and shown, not prevented. A durable outbox would keep usage data on disk and would have to be purged when consent is withdrawn.
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-HOST-20`, and `DH-T-OTEL-1` to `DH-T-OTEL-13` in the library specification.

<a id="dh-adr-host-8"></a>
### DH-ADR-HOST-8 One server-side directory browser; no native chooser and no boot-time guess

- **Decision.** `directory-picker-native` and `directory-picker-auto` are not built. The seam keeps one shape, the browser of §6.1, served to the owner only. Scope boundary of this record: the browser lists one level and creates one child directory; it does not search, rename or delete, because it exists to pick a place for a workspace, and file changes belong to the file tools and the files view.
- **Why.** [DH-ADR-18](../design.md#dh-adr-18) already turns native folder dialogs into a host-rendered picker. A dialog on the host's display assumes the person is looking at it, and upstream lists four ways the assumption fails (`directory-picker-auto/README.md:109-112`). The Windows dialog needs a native binding, which a pure-Go binary cannot carry ([DH-ADR-15](../design.md#dh-adr-15)).
- **Tradeoff.** A person at the host machine loses the familiar system dialog and its bookmarks. `Roots` scopes what is listed; it is a security boundary only if workspace creation (A07) honours the same roots, because an owner can name any path there.
- **Serves.** [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-HOST-25`, `DH-T-HOST-26`, [DH-E-HOST-5](#dh-e-host-5).

<a id="dh-adr-host-9"></a>
### DH-ADR-HOST-9 The host opens only workspace directories, only in catalog or operator-defined applications, only for a local owner

- **Decision.** Four conditions, all required. (1) The peer is the owner. (2) The host is in local mode, was not started through SSH and has a display. (3) The path is an existing directory inside a registered workspace. (4) The application is a catalog entry or an operator entry from a config layer; a request can never name a program. The host never "opens" a file with its default handler. Application icons are not extracted; the UI shows a category glyph and the name. On macOS a catalog entry that names a bundle identifier is found first in the known directories and then by one Spotlight query, `/usr/bin/mdfind "kMDItemCFBundleIdentifier == '<id>'"`, run through A09's host starter as a trusted launch with a deadline and bounded output, at resolution time only; the first result that is an application bundle whose `Info.plist` names the same identifier is the launcher (answers Q3-11 for `open-in-app.2`). Inference: Spotlight may be off or still indexing; then the query returns nothing and the entry is not offered, as before.
- **Why.** Asking the operating system to open an arbitrary file runs it if it is a script or a program, outside the tool pipeline, the sandbox and approvals. A link the model wrote could lead a person to that click. A directory handed to a known application has no such path. Locality cannot be proven from a request (a forwarded port arrives from loopback), so the rule also holds when the guess is wrong: the worst case is an editor window on an unattended machine.
- **Tradeoff.** No "open this file in its application". Upstream's "reveal in file manager" is **dropped** as a file action, a recorded exception to [DH-ADR-1](../design.md#dh-adr-1): neither the filesystem area nor the execution area provides it, and no route takes a file path. Its replacement is condition (3) with the file manager as the application: the workspace directory opens, and no file is selected in it. A file is downloaded or viewed in the page. macOS detection finds a moved application by the Spotlight query, not by Launch Services, which needs native code; an application that Spotlight does not index is added by an operator entry. Icons are generic, because icon extraction needs native code or helper programs (a scope boundary of this record).
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-HOST-27`, `DH-T-HOST-28`, [DH-E-HOST-5](#dh-e-host-5).

<a id="dh-adr-host-10"></a>
### DH-ADR-HOST-10 A fatal failure prints one diagnostic and leaves one local, bounded report

- **Decision.** The two layers of §6.1. Reports stay on the machine: nothing uploads them, and the `telemetry` kind may send only the failure's code. The next start reports an unclean exit and names the report and the safe-mode option.
- **Why.** Upstream decided that an uncaught error is fatal and must leave a complete diagnostic, after two field failures were diagnosed from screenshots (note `2026-09-22-fatal-diagnostics-and-crash-reports.md:9-21`); its report file exists only in the desktop shell. In Go a panic in any goroutine ends the process, and a runtime fault cannot be recovered, so a handler alone would miss cases; the runtime's crash output covers them.
- **Tradeoff.** A report contains paths, error text and log lines. Secret values print as `[redacted]` by their type ([../design.md §6](../design.md#6-security-model), rule 4), but a path or a message can still be private, which is why reports are owner-only and never sent. The runtime's own output is not formatted by the harness.
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-HOST-23`, `DH-T-HOST-24`, [DH-E-HOST-4](#dh-e-host-4).

<a id="dh-adr-host-11"></a>
### DH-ADR-HOST-11 The plugin inventory is a read-only projection that names layers and plugin processes

- **Decision.** As §6.1. The inventory reads the loader, the plugin host and the preset registry at each call and keeps only a bounded history of phase changes. Changing the composition is done through settings forms (A07) and plugin installation (A21, A22), never here.
- **Why.** With plugins as local directories ([DH-ADR-13](../design.md#dh-adr-13)), what an operator needs to see is which directory runs, what it asked for and what it was granted. Upstream's snapshot cannot say which layer supplied an entry or why an entry has no fiber (`plugin-inventory/README.md:104-105`).
- **Tradeoff.** The history is in memory and ends with the process; the log file is the durable record.
- **Serves.** [DH-O-11](../design.md#dh-o-11), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-HOST-29`, `DH-T-HOST-30`, [DH-E-HOST-6](#dh-e-host-6).

<a id="dh-adr-host-12"></a>
### DH-ADR-HOST-12 Owner authority is never handed to a caller, never readable from disk, and refused to a process the host started for the model

- **Problem.** Everything the host starts for the model (a shell command and what it starts, a script, an MCP server, a plugin) runs as the same operating-system user as the host. File modes and "only this user can open the socket" do not tell the two apart. A process that became an owner peer could answer its own approval, give itself a session grant, switch the preset and create a share.
- **Decision.** Four layers, each owned by one area. (1) **No interface hands authority to whoever asks.** Owner authority exists in two places: a browser session made by exchanging a launch token, and the stdio client that started the host (A20). A launch link leaves the host only on its own terminal or in its own hand-off to the browser. The control endpoint has three methods and returns no credential; the tool endpoint ([tools.md](tools.md)) and the plugin protocol ([extensions.md](extensions.md)) have no method that reaches an interaction, a grant, a preset or a secret ([interaction-credentials.md](interaction-credentials.md) §6.2, "Who may answer"). (2) **Nothing readable makes an owner.** A session is a random id of which the host stores only a hash; there is no signing secret. Forging a session takes a write to the home, and every confined mode denies writes to the whole home ([DH-ADR-SANDBOX-5](sandbox-ssh.md#dh-adr-sandbox-5)). (3) **The peer rule.** The control endpoint and the listener ask A09 which tree a local peer's process belongs to ([DH-D-EXEC-2](execution.md#dh-d-exec-2)) and refuse a peer inside an untrusted tree before any credential is read. The answer is exact where the tree owner is a Windows job object or a Linux control group, and best effort elsewhere; `Listener.PeerRule()` says which. It is not applied to a peer from another machine: remote mode is the operator's choice. (4) **Sandbox denials and self-protection.** Each backend denies a confined command what it can of: reading `<home>/secrets`, connecting to `<home>/run`, and asking a same-user service to run a program outside the confinement, and reports the rest ([sandbox-ssh.md](sandbox-ssh.md) §6.1). The host marks itself non-dumpable on Linux and refuses debugger attachment on macOS in the first lines of `main`, so its memory and descriptors cannot be read through the process table.
- **Why.** Layers 3 and 4 depend on the platform and are unproven until run, so the design must not need them: with layers 1 and 2 a process that reaches every socket and reads every file of the user still holds nothing that makes it an owner. Inference from two upstream facts: its cookie signing secret is a durable credential record (note `2026-08-24-browser-token-authentication.md:39-41`), and its credential file can be read by the model's tools because they run as the same user (`packages/credentials/credentials-local/README.md:115`); together they let a command sign its own owner cookie. A control method that returns a link would open the same door.
- **Residual risk, stated plainly.** Under `danger-full-access`, or where no backend works and the owner chose to run unconfined, a command has the user's whole authority: it can rewrite the home's database and configuration, read stored keys, drive the user's browser or desktop, and on some systems attach to the host. Layers 1 and 2 still hold; nothing else is a boundary there. In confined modes three gaps remain. Where the peer rule is best effort or not applied (macOS; a Linux host on which neither its own control group is writable nor a user service manager answers, [DH-ADR-EXEC-2](execution.md#dh-adr-exec-2)), a command that learns a live link or cookie can use it. Asking the user service manager once for a delegated scope ([DH-ADR-40](../design.md#dh-adr-40)) makes the rule exact on an ordinary SSH login as well. Where a backend cannot hide a same-user service that runs programs on request (a session message bus, a service manager, a task scheduler), a command can act from outside every tree, and A10 reports the effect `delegated-execution`. A command that can synthesise input in the user's desktop session can click an approval in the user's own browser. `dsh sandbox status`, the permissions page and the preset's label show these facts for the running machine. The owner accepted these gaps as reported ones, with no pairing step at sign-in, and confirmed this record ([DH-ADR-28](../design.md#dh-adr-28)): each command result, `dsh sandbox status` and the permissions page say which gaps exist on the machine, and every new browser sign-in is announced with a "sign out everything" action.
- **Tradeoff.** A session lookup is one indexed read per request, cached per connection. A tool the model starts can never drive the harness's own UI on loopback, even for a legitimate test. Revoking is deleting rows, and a row copied before deletion is useless because it is a hash.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Verified by.** `DH-T-HOST-11`, `DH-T-HOST-16`, `DH-T-HOST-31`, [DH-E-HOST-7](#dh-e-host-7), [DH-E-HOST-8](#dh-e-host-8), [DH-E-HOST-9](#dh-e-host-9).

## 8. Deliverables

<a id="dh-d-host-1"></a>
### DH-D-HOST-1 The listener: one address, optional TLS, admission-gated mounts, embedded assets, a shutdown that ends streams

- **Result and why it matters.** A browser can load pages and files from the host, and no handler can be reached except through the admission pipeline.
- **How it is implemented.** `host.listen` parses the bind address once with `net/netip` and listens on that same value. One `http.Server` runs one handler: the pipeline of §6.1, which picks a mount by longest prefix and calls it last. `Mount` registers an effect that removes the mount. TLS uses `tls.Config.GetCertificate` over a pair that is re-read when the files' modification times change. `Stream` requests and `/assets/` sit outside the `gzhttp` wrapper ([../libs/compression.md](../libs/compression.md)). `host.assets` serves an `embed.FS` with content types from the harness table, `nosniff`, 404 for a directory or a miss, and `GET` and `HEAD` only ([../libs/serve-static.md](../libs/serve-static.md)); a file whose name carries its content hash is served as immutable with a long cache lifetime, any other with a build-hash ETag and `no-cache`. The listener writes no `Content-Security-Policy`: A19's `web` writes it, because only it knows the kind of answer. **Components and files.** `internal/host/listen`, `internal/host/assets`.
- **Depends on.** `DH-LIB-IPADDRJS`, `DH-LIB-SERVESTATIC`, `DH-LIB-COMPRESSION`, `DH-LIB-MIME`, `DH-LIB-SELFSIGNED` (tests); [DH-D-BOOT-5](boot.md#dh-d-boot-5) (the readiness signal); [DH-D-UTIL-5](util-test-support.md#dh-d-util-5) (the `mediatype` table). Built on this, not the reverse: A19's `web` mounts its route table at `/` ([DH-D-API-1](api.md#dh-d-api-1)), and the embedded files come from [DH-D-WEBAPP-2](app-web.md#dh-d-webapp-2). **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Priority.** P0. **Done when.** `DH-T-HOST-1` to `DH-T-HOST-8` pass and [DH-E-HOST-2](#dh-e-host-2) has passed.

<a id="dh-d-host-2"></a>
### DH-D-HOST-2 A browser is admitted by source, then by a cookie from a launch token, and arrives as a peer

- **Result and why it matters.** Only the person who started `dsh` controls it from a browser; another site, another machine, another local user and a process the host started for the model cannot.
- **How it is implemented.** Steps 2 to 6 of §6.1 in `listen`, with the peer rule resolved once per accepted connection; `host.auth` for tokens, the hashed session table, peers, `Issue`, sign-out, revocation and the sign-in notice; the self-protection calls of [DH-ADR-HOST-12](#dh-adr-host-12) in `cmd/dsh`. **Components and files.** `internal/host/listen`, `internal/host/auth`.
- **Depends on.** [DH-D-HOST-1](#dh-d-host-1); [DH-D-STORAGE-1](storage.md#dh-d-storage-1) (a machine-state table); [DH-D-EXEC-2](execution.md#dh-d-exec-2) (which tree a local peer belongs to). `access.Principal` is a type of A19's contract package; what a non-owner peer may do is decided by [DH-D-API-3](api.md#dh-d-api-3), which depends on this deliverable. **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12). **Priority.** P0. **Done when.** `DH-T-HOST-9` to `DH-T-HOST-14` and `DH-T-HOST-31` pass, and [DH-E-HOST-1](#dh-e-host-1), [DH-E-HOST-2](#dh-e-host-2), [DH-E-HOST-7](#dh-e-host-7), [DH-E-HOST-8](#dh-e-host-8) and [DH-E-HOST-9](#dh-e-host-9) have passed. The three takeover scenarios try to take ownership of the host, so they run in this deliverable's build stage.

<a id="dh-d-host-3"></a>
### DH-D-HOST-3 Running `dsh` shows the UI, and running it again reuses the host

- **Result and why it matters.** One command gets a person to a signed-in page; a second run opens another tab instead of failing on a busy port.
- **How it is implemented.** `host.launch` as §6.1: print, hand-off, runtime record, control endpoint. The client side of reuse is a few lines in `cmd/dsh` (A24). **Components and files.** `internal/host/launch`.
- **Depends on.** [DH-D-HOST-2](#dh-d-host-2), `DH-LIB-OPEN`, `DH-LIB-ELECTRON`; [DH-D-EXEC-1](execution.md#dh-d-exec-1) (`subprocess.HostKey`, a detached trusted launch) and [DH-D-EXEC-2](execution.md#dh-d-exec-2) (the peer of a socket connection); [DH-D-FS-1](filesystem.md#dh-d-fs-1) (the `fsx` lock); [DH-D-SDK-1](sdk-acp-typert.md#dh-d-sdk-1) (the codec); [DH-D-BOOT-5](boot.md#dh-d-boot-5) (the readiness signal). The stop function is a value A24 supplies at composition ([DH-D-CLI-4](app-cli.md#dh-d-cli-4)); the flags and the client half are [DH-D-CLI-5](app-cli.md#dh-d-cli-5); both depend on this deliverable. **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Priority.** P0. **Done when.** `DH-T-HOST-15`, `DH-T-HOST-16` and `DH-T-HOST-31` pass and [DH-E-HOST-1](#dh-e-host-1) has passed.

<a id="dh-d-host-4"></a>
### DH-D-HOST-4 Nothing leaves the machine by default, each kind has a visible setting, and one switch stops all of them

- **Result and why it matters.** A user who changes nothing sends nothing, and an operator has one fact to check for a whole fleet.
- **How it is implemented.** `host.sharing` as §6.1. Each sender registers at load. The settings page lists `Senders()` with each description, its setting and its state (`off`, `on`, `unavailable`, `stopped by the switch`). **Components and files.** `internal/host/sharing`.
- **Depends on.** `DH-LIB-UNDICI`; [DH-D-STORAGE-8](storage.md#dh-d-storage-8) (settings forms). `egress` depends on this deliverable for the permit type, not the reverse ([DH-D-UTIL-2](util-test-support.md#dh-d-util-2): it hands a client for a sharing purpose only to a permit holder, and its test kit records every outbound request and name lookup). Each sender registers itself and so depends on this deliverable, not the reverse: [DH-D-LLM-5](llm.md#dh-d-llm-5), [DH-D-STORE-9](session-store.md#dh-d-store-9), [DH-D-VIEW-9](session-views.md#dh-d-view-9), [DH-D-HUMAN-9](interaction-credentials.md#dh-d-human-9), [DH-D-HUMAN-10](interaction-credentials.md#dh-d-human-10), [DH-D-CLI-8](app-cli.md#dh-d-cli-8), [DH-D-XMISC-2](x-recommendations-misc.md#dh-d-xmisc-2) and [DH-D-XCUA-4](x-cua-browser-voice.md#dh-d-xcua-4); the page is [DH-D-UISET-3](ui-settings-plugins.md#dh-d-uiset-3). **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Priority.** P0. **Done when.** `DH-T-HOST-17` to `DH-T-HOST-19` pass and [DH-E-HOST-3](#dh-e-host-3) has passed.

<a id="dh-d-host-5"></a>
### DH-D-HOST-5 One exporter sends OpenTelemetry log records, bounded by bytes, for whoever holds a permit

- **Result and why it matters.** Every flow that reaches a collector uses one transport with one set of limits, proxy rules and shutdown behaviour.
- **How it is implemented.** The package of [../libs/opentelemetry.md §6](../libs/opentelemetry.md), with the three additions of [DH-ADR-HOST-7](#dh-adr-host-7). The channel's lifetime context is the permit's. **The exporter acknowledges every batch.** `Emit` gives each accepted record a sequence number, and for every batch the channel calls `OnBatch` exactly once with an `Outcome`: the first and last sequence number, the counts, and one status. `Delivered` means the collector answered success for the whole request; a partial success reports `Rejected` with the collector's count; `Failed` means the deadline passed after retries; `Dropped` means the queue overflowed before sending; `Cancelled` means consent was withdrawn. A consumer with a durable source, such as the session-log upload of A05 and A06, advances its recorded position only on `Delivered` and resends from the first unacknowledged sequence number; it never needs to end as `unavailable` for want of an answer. A record larger than the request limit is refused by `Emit` itself, so the consumer can send an elision record in its place (U-K1-11). **Components and files.** `internal/host/telemetry/otlplog`.
- **Depends on.** [DH-D-HOST-4](#dh-d-host-4), `DH-LIB-OTEL`, `DH-LIB-UNDICI`; [DH-D-UTIL-2](util-test-support.md#dh-d-util-2) (an `egress` client that follows no redirects). **Serves.** [DH-O-14](../design.md#dh-o-14). **Priority.** P1. **Done when.** `DH-T-HOST-20` and `DH-T-OTEL-1` to `DH-T-OTEL-13` pass and [DH-E-HOST-11](#dh-e-host-11) has passed.

<a id="dh-d-host-6"></a>
### DH-D-HOST-6 Other areas report product and operational events through two typed channels

- **Result and why it matters.** An area that wants to count something declares an event; it cannot send free text, an identity or anything the user did not switch on.
- **How it is implemented.** `host.report` as §6.1: the catalog, the two kinds, lazy channels, the fixed resource. The catalog is one of the generated catalogs of [DH-O-14](../design.md#dh-o-14). **Components and files.** `internal/host/telemetry/report`.
- **Depends on.** [DH-D-HOST-4](#dh-d-host-4), [DH-D-HOST-5](#dh-d-host-5); [DH-D-UTIL-1](util-test-support.md#dh-d-util-1) (`schema` field kinds); [DH-D-HUMAN-9](interaction-credentials.md#dh-d-human-9) (`identity.Ensure` with the kind as its purpose, so the id is created at the first permitted record). The events are declared by their owners, first [DH-D-UICORE-9](ui-core.md#dh-d-uicore-9). **Serves.** [DH-O-14](../design.md#dh-o-14). **Priority.** P2. **Done when.** `DH-T-HOST-21` and `DH-T-HOST-22` pass and [DH-E-HOST-12](#dh-e-host-12) has passed.

<a id="dh-d-host-7"></a>
### DH-D-HOST-7 A host that cannot continue says why once and leaves a report

- **Result and why it matters.** A failure in the field can be diagnosed from one file, on every profile, with no desktop dialog.
- **How it is implemented.** `diag` as §6.1 and [DH-ADR-HOST-10](#dh-adr-host-10). The unclean-exit notice reads the runtime record and the crash directory at start. **Components and files.** `internal/host/diag`; one call in `cmd/dsh`.
- **Depends on.** `DH-LIB-ELECTRON` (`DH-LIB-ELECTRON-R7`); [DH-D-BOOT-5](boot.md#dh-d-boot-5) (a bounded log ring from the logging component, the release function, and the `--safe` start the notice names: `launch.Data.Safe`, defined by [DH-ADR-BOOT-12](boot.md#dh-adr-boot-12)); [DH-D-CLI-3](app-cli.md#dh-d-cli-3) (exit codes); [DH-D-STORAGE-2](storage.md#dh-d-storage-2) (`home.Layout`). The commands that list and show reports are [DH-D-CLI-5](app-cli.md#dh-d-cli-5). **Serves.** [DH-O-14](../design.md#dh-o-14). **Priority.** P0. **Done when.** `DH-T-HOST-23`, `DH-T-HOST-24` and `DH-T-ELECTRON-4` pass and [DH-E-HOST-4](#dh-e-host-4) has passed.

<a id="dh-d-host-8"></a>
### DH-D-HOST-8 The owner chooses a directory of the host machine from the browser

- **Result and why it matters.** A workspace can be created from a remote browser, a forwarded port or a machine with no display.
- **How it is implemented.** `dirbrowse/local` as §6.1, reading one level through a bounded sorted window so that memory does not grow with the directory. The route (`directory.browse`, owner only) is A19's and the picker page is the UI areas'; they call `dirbrowse.Key` with the request's context. **Components and files.** `internal/host/dirbrowse`, `dirbrowse/local`.
- **Depends on.** [DH-D-HOST-2](#dh-d-host-2), `DH-LIB-ELECTRON` (`DH-LIB-ELECTRON-R8`). Its callers are A19's `directory.browse` route ([DH-D-API-6](api.md#dh-d-api-6)), which calls `dirbrowse.Key` and lists nothing itself, and workspace creation ([DH-D-STORAGE-7](storage.md#dh-d-storage-7)), which must honour `Roots`. **Serves.** [DH-O-12](../design.md#dh-o-12). **Priority.** P0. **Done when.** `DH-T-HOST-25`, `DH-T-HOST-26` and `DH-T-ELECTRON-3` pass and [DH-E-HOST-5](#dh-e-host-5) has passed.

<a id="dh-d-host-9"></a>
### DH-D-HOST-9 A local owner opens a workspace directory in an editor, terminal or file manager on the host

- **Result and why it matters.** One click from the session header to the tools beside it, without a way to start an arbitrary program.
- **How it is implemented.** `host.openpath` as §6.1 and [DH-ADR-HOST-9](#dh-adr-host-9): the embedded catalog with per-platform locators (path lookup, known directories, the Windows registry through its command-line reader, desktop entries), resolution on first use and again after `RefreshAfter`. **Components and files.** `internal/host/openpath`.
- **Depends on.** [DH-D-HOST-1](#dh-d-host-1), [DH-D-HOST-2](#dh-d-host-2); [DH-D-EXEC-1](execution.md#dh-d-exec-1) (`subprocess.HostKey` with a detached start); [DH-D-STORAGE-7](storage.md#dh-d-storage-7) (the workspace registry); [DH-D-API-1](api.md#dh-d-api-1) (`web.Routes`). **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-7](../design.md#dh-o-7). **Priority.** P2. **Done when.** `DH-T-HOST-27` and `DH-T-HOST-28` pass and [DH-E-HOST-10](#dh-e-host-10) has passed.

<a id="dh-d-host-10"></a>
### DH-D-HOST-10 The host reports what is composed in it, including plugin directories and their grants

- **Result and why it matters.** An operator sees which components and plugins run, which failed and why, and which layer or directory each came from.
- **How it is implemented.** `host.inventory` as §6.1. It listens to the loader's and the plugin host's state changes to feed the history ring and `inventory.Changed`. **Components and files.** `internal/host/inventory`.
- **Depends on.** `DH-D-CORDIS-4`; [DH-D-BOOT-6](boot.md#dh-d-boot-6) (`config.Service.Tree`: a loader view with entry id, phase, diagnostic and supplying layer); [DH-D-EXT-4](extensions.md#dh-d-ext-4) (plugin `Descriptor` values and `plugin.StateChanged`); [DH-D-BUNDLE-4](bundle-preset.md#dh-d-bundle-4) (the preset registry). **Serves.** [DH-O-11](../design.md#dh-o-11), [DH-O-14](../design.md#dh-o-14). **Priority.** P1. **Done when.** `DH-T-HOST-29` and `DH-T-HOST-30` pass and [DH-E-HOST-6](#dh-e-host-6) has passed.

## 9. Acceptance

### 9.1 Local tests

These run in the Go test process against fakes, an in-process listener or a temporary home. They are not evidence of browser or operating-system behaviour.

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-HOST-1` | DH-D-HOST-1 | Bind table: loopback, mapped and zoned spellings accepted; every unspecified spelling and every host name refused with the value quoted; the listener's address equals the parsed value |
| `DH-T-HOST-2` | DH-D-HOST-1 | TLS: an unreadable, empty, non-PEM or mismatched pair fails the component and nothing listens; a replaced valid pair is served to the next handshake; a replaced bad pair keeps the old one |
| `DH-T-HOST-3` | DH-D-HOST-1 | A non-loopback bind without TLS fails start-up; with `AllowInsecureRemote` it starts and warns; `Mode()` is remote for a non-loopback bind, a remote trusted host or a public URL |
| `DH-T-HOST-4` | DH-D-HOST-1 | `Mount` refuses an overlapping prefix; an unloaded owner's prefix answers 404; `deny`, and a `Classify` that panics, answer 403 without calling the handler; stream requests answer 503 until `Ready` closes |
| `DH-T-HOST-5` | DH-D-HOST-1 | Unloading `host.listen` returns within the grace period with an open `Stream` handler and an open WebSocket; both see `Done`; no goroutine remains |
| `DH-T-HOST-6` | DH-D-HOST-1 | Assets: `..`, encoded separators, a directory, a miss and a `POST` never return file bytes; type from the harness table with `nosniff`; a matching `If-None-Match` is 304; `anonymous` still fails a bad `Host` |
| `DH-T-HOST-7` | DH-D-HOST-1 | Compression is off by default; when on, an HTML response is gzip and a `Stream` route, an asset and a range response are not |
| `DH-T-HOST-8` | DH-D-HOST-1 | A slow header, an oversized header and an idle connection are closed at the configured limits; a panicking handler yields 500 and the next request succeeds |
| `DH-T-HOST-9` | DH-D-HOST-2 | Source table: loopback names, the bind literal on any port and a trusted host pass; a rebinding name, `127.1`, `Origin: null`, a mismatched `Origin` and `Sec-Fetch-Site: cross-site` are 403 on `GET` and `POST`, before any credential is read |
| `DH-T-HOST-10` | DH-D-HOST-2 | The exchange sets the cookie with the exact attributes and headers and redirects to `./`; the same token fails the second time and after the TTL; a `token` parameter on another path is 400 and absent from the captured log |
| `DH-T-HOST-11` | DH-D-HOST-2 | A cookie whose value matches no live row, a row of another audience or an expired row is refused; no value stored in the table works as a cookie; `Secure` follows the listener's protocol and not a forwarded header; a stale harness cookie for another port is expired by the exchange; `Issue` yields a peer of the named principal and an owner cookie in the same browser wins |
| `DH-T-HOST-12` | DH-D-HOST-2 | Two browsers are two peers; `SignOut` ends one browser's streams and refuses its cookie; `RevokeAll` refuses both; a cookie issued before a restart still works; `FormToken()` differs between the two peers, is stable for one peer across requests, and stops verifying when its row is deleted |
| `DH-T-HOST-13` | DH-D-HOST-2 | Class table through a fake mount: `required` never reaches `Serve` without a peer in the context; `anonymous` reads no cookie and still fails a bad `Host` or origin; `callback` accepts a cross-site request, fails a bad `Host`, and gets no peer; a peer's context ends on sign-out |
| `DH-T-HOST-14` | DH-D-HOST-2 | With `host.auth` unloaded a `required` request is 503 and an asset is served; `/connect` is 404; no desktop cookie name is accepted |
| `DH-T-HOST-15` | DH-D-HOST-3 | Decision table for print and open: default, `Open` off, SSH environment, no display; the launcher gets the URL as one argument and no harness variable; a failed launcher warns without the URL |
| `DH-T-HOST-16` | DH-D-HOST-3 | With a live fixture host, a second start calls `launch.open`, receives no link and binds nothing; no reply of any method contains a token or a cookie; an unknown method is refused and none can be registered; with a record whose lock is free it replaces the record; the socket directory is owner-only; unload removes record and socket |
| `DH-T-HOST-17` | DH-D-HOST-4 | For each shipped profile, through the real loader, with a fresh home, a recording transport and resolver and a replay model on loopback: start, one turn with a tool call, a feedback submission, the settings page, two simulated days of timers, stop. The recorder sees no request and no name lookup except to the replay model, and no identity file exists |
| `DH-T-HOST-18` | DH-D-HOST-4 | With every kind on and pointed at the recorder, setting `sharing.Off`, and separately `DSH_SHARING_OFF=0`, yields no further request, cancels the one in flight, and shows every sender as stopped; clearing the config field while the variable is set changes nothing |
| `DH-T-HOST-19` | DH-D-HOST-4 | A second sender for one kind and a permit for an unregistered kind are refused; every sharing-purpose client `egress` handed out belongs to a listed sender; a source check finds no HTTP client built outside `egress` |
| `DH-T-HOST-20` | DH-D-HOST-5 | No permit, no channel; ending the permit closes the channel without a drain; `OnBatch` reports delivered, failed, dropped and cancelled with exact sequence ranges; an `http` endpoint off loopback is refused; unload returns within `DrainTimeout` against a hung server |
| `DH-T-HOST-21` | DH-D-HOST-6 | Declaring an event with a free string field fails start-up; the sent resource has exactly the five attributes; the installation id is not created until the first accepted record |
| `DH-T-HOST-22` | DH-D-HOST-6 | Analytics on and telemetry off sends only analytics events, and the reverse; an event of an undeclared type is refused |
| `DH-T-HOST-23` | DH-D-HOST-7 | A failing required entry and a recovered panic each print one diagnostic naming the report, write a report with the listed fields, run the release, and exit non-zero; a secret value placed in the error and the log appears as `[redacted]` |
| `DH-T-HOST-24` | DH-D-HOST-7 | The crash file is set before any component loads and removed on a clean exit; twelve reports are pruned to ten; a leftover record with a free lock makes the next start print the unclean-exit line; an unwritable crash directory still prints the diagnostic |
| `DH-T-HOST-25` | DH-D-HOST-8 | Listing: directories only, sorted, `truncated` at the bound with bounded memory, links followed and loops skipped, hidden flags, ancestor chain; relative and drive-less paths refused; with `Roots`, a path and a link outside them are refused; cancellation stops the scan |
| `DH-T-HOST-26` | DH-D-HOST-8 | `CreateDirectory` refuses separators, `.`, `..`, a missing parent and an existing child with typed errors; the roots level lists configured roots, or drive letters on Windows |
| `DH-T-HOST-27` | DH-D-HOST-9 | Locality table: in remote mode, under SSH and with no display the routes are not registered (404); a non-owner is refused by the rule; no process starts in any of the four |
| `DH-T-HOST-28` | DH-D-HOST-9 | A file path, a directory outside every workspace and an unknown application id start nothing; the launcher gets an argument list and a scrubbed environment; an operator entry from config is offered; a missing launcher drops its entry; with a fake starter on macOS, an entry not in the known directories is found by the Spotlight query, a result whose bundle identifier differs is ignored, and a query that times out leaves the entry unoffered |
| `DH-T-HOST-29` | DH-D-HOST-10 | A snapshot shows each entry's layer, phase and diagnostic; an entry that failed and was removed is in the history; presets carry `Available` with and without a registry; the service type has no mutating method |
| `DH-T-HOST-30` | DH-D-HOST-10 | With a fake plugin host: directory, manifest, requested and granted capabilities and process state appear; a state change raises `inventory.Changed` |
| `DH-T-HOST-31` | DH-D-HOST-2, DH-D-HOST-3 | With a fake tree lookup: a peer inside an untrusted tree is refused on the control endpoint, on the token exchange and on a `required` request carrying a valid cookie, before any credential is read; a peer outside every tree and a trusted helper pass; a peer that cannot be identified is refused on the control endpoint and makes `PeerRule()` report `NotApplied` on the listener; a new sign-in raises the notice in open tabs |

### 9.2 Real-environment scenarios

<a id="dh-e-host-1"></a>
#### DH-E-HOST-1 A real browser is signed in by `dsh`, a second `dsh` reuses the host, and other origins are refused

- **Proves.** DH-D-HOST-2, DH-D-HOST-3.
- **Given.** The built binary on Windows, macOS and Linux desktops; a fresh home; each supported browser as the default in turn; a second local web server on another port that serves a page with a cross-site form and a script; a host name that resolves to `127.0.0.1`.
- **When.** (1) Run `dsh`. (2) Reload the page, then paste the printed URL into a private window. (3) Run `dsh` again in another terminal. (4) Restart the host and reload. (5) From the second server's page, submit the form and fetch a page of the UI; then load the UI through the extra host name. (6) Sign out in one browser. (7) Open the second server at `127.0.0.1` and record the cookies it receives.
- **Then.** (1) The browser shows the UI with no token in the address bar or history. (2) The reload works; the pasted URL is refused. (3) A new tab opens on the same host, no second listener exists, and the second process exits zero. (4) The page loads without a new token. (5) All three are refused with 403. (6) Only that browser is signed out. (7) The observation is recorded as the answer to the cookie question of [§10](#10-open-questions-and-limitations), item 2; it is not a pass or fail.
- **Cleanup.** Stop the host and both servers, delete the home and the browser's site data for the origin. If step 5 was not refused, keep the request log and the browser version.

<a id="dh-e-host-2"></a>
#### DH-E-HOST-2 A network listener refuses plain HTTP, serves TLS, works behind a path prefix, and picks up a new certificate

- **Proves.** DH-D-HOST-1, DH-D-HOST-2.
- **Given.** Two machines on one network; a certificate the second machine's browser trusts for the first machine's name; a real reverse proxy that strips a path prefix; the built binary and a fresh home on the first machine.
- **When.** (1) Start with a network bind and no TLS. (2) Start with TLS and the name as a trusted host; open the printed URL on the second machine. (3) Open seven tabs. (4) Replace the certificate files with a renewed pair while tabs are open. (5) Put the proxy in front and open the UI under the prefix.
- **Then.** (1) Start-up fails with a message naming the flag and nothing listens. (2) The UI loads; the cookie is `Secure`; directory browsing works; the "open in" action is absent and its route answers 404. (3) All seven update, over HTTP/2. (4) A new connection presents the new certificate without a restart. (5) Pages, assets, forms and the event stream work under the prefix and the token redirect stays inside it.
- **Cleanup.** Stop the host and proxy, remove the certificate from the trust store, delete the home. If step 1 listened, keep the configuration and the start-up output.

<a id="dh-e-host-3"></a>
#### DH-E-HOST-3 Nothing reaches the network by default, and the off-switch is shown locked by the environment

- **Proves.** DH-D-HOST-4. It needs the gate only; opting in to a sender is [DH-E-HOST-11](#dh-e-host-11) and [DH-E-HOST-12](#dh-e-host-12).
- **Given.** The built binary; a fresh home; a recording forward proxy set as the process's proxy; a model provider reached through the proxy; a real browser.
- **When.** (1) Use a session for several turns with default settings and leave the host idle for an hour. (2) Open the sharing page. (3) Restart with `DSH_SHARING_OFF=0` and open it again.
- **Then.** (1) The proxy log holds only requests to the model provider and the home has no identity file. (2) Every kind of the table is listed `off` or `unavailable`, each with its description. (3) The switch is shown on and locked, naming the variable.
- **Cleanup.** Stop the proxy, delete the home. If anything but the model provider appears in (1), keep the proxy log and the harness configuration.

<a id="dh-e-host-4"></a>
#### DH-E-HOST-4 A host that dies leaves a readable report on each operating system

- **Proves.** DH-D-HOST-7.
- **Given.** The built binary on Windows, macOS and Linux; a fresh home; a test-only component, enabled by configuration, that can fail at load, panic in a goroutine, or trigger a runtime fault on request.
- **When.** For each of the three failures: start the host, trigger it, read standard error and the crash directory, then start again.
- **Then.** Each run exits non-zero. Standard error names a report file. The file is owner-only and holds the cause and stacks. The next start prints the unclean-exit line with that path and the safe-mode option, and a start in safe mode reaches the UI. After twelve failures ten reports remain.
- **Cleanup.** Delete the home. If a failure left no report, keep standard error and the directory listing.

<a id="dh-e-host-5"></a>
#### DH-E-HOST-5 Choosing a directory of the host machine, at the machine and away from it

- **Proves.** DH-D-HOST-8.
- **Given.** A desktop session on each operating system; a home directory that contains a hidden directory (dot-named, and attribute-hidden on Windows), a link to a directory and a directory with more than 1,000 children; on Windows a second drive; a second machine with an SSH client.
- **When.** (1) In a local browser, start a new workspace and browse to a directory, crossing drives on Windows; create a child directory. (2) Start the host over SSH from the second machine with a forwarded port and repeat (1) in that machine's browser.
- **Then.** (1) The listing matches the file system, hidden entries are flagged, the large level says it is incomplete, and the new directory exists. (2) Browsing and creation work in the same way.
- **Cleanup.** Delete the created directories and the home.

<a id="dh-e-host-10"></a>
#### DH-E-HOST-10 Opening a workspace in an application works at the machine and is absent away from it

- **Proves.** DH-D-HOST-9.
- **Given.** The machines of [DH-E-HOST-5](#dh-e-host-5) with a file manager and one catalog editor installed; on macOS the editor moved out of `/Applications` into a folder Spotlight indexes; a workspace already registered.
- **When.** (1) In a local browser, use "open in" for the file manager and the editor. (2) Start the host over SSH from the second machine with a forwarded port and look for the action in that machine's browser.
- **Then.** (1) Each application opens on the workspace directory, the moved macOS editor included; its process environment holds no harness credential. (2) No "open in" action is shown, its route is not registered, and no application starts on the host.
- **Cleanup.** Close the applications and delete the home. If an application started in (2), keep the host's process list and environment.

<a id="dh-e-host-6"></a>
#### DH-E-HOST-6 The inventory shows a real plugin process from a local directory

- **Proves.** DH-D-HOST-10.
- **Given.** The built binary; a fresh home; a real plugin in a local directory whose manifest asks for two capabilities, one granted; a real browser.
- **When.** (1) Open the plugins page. (2) Kill the plugin process from the operating system. (3) Disable a built-in entry through its settings form.
- **Then.** (1) The page shows the directory, the manifest's name and version, both requested capabilities and the one grant, and `running`. (2) It shows the restart and the last exit without a reload. (3) The entry shows as disabled with the layer that now supplies the value, and the history lists the change.
- **Cleanup.** Remove the plugin directory and the home. If the process survived the host's stop, keep its process id and the host log.

<a id="dh-e-host-11"></a>
#### DH-E-HOST-11 The exporter delivers to a real collector, acknowledges each batch, and stops at the switch

- **Proves.** DH-D-HOST-5.
- **Given.** The built binary; a fresh home; a stock OpenTelemetry Collector with an OTLP/HTTP receiver and a file exporter; a test-only sender component, enabled by configuration, registered under the `telemetry` kind, that emits numbered records on request and logs every `OnBatch` outcome.
- **When.** (1) Configure the collector's URL, turn the kind on, emit 1,000 records. (2) Stop the collector, emit 100 more, start it again. (3) Set the off-switch while a batch is in flight.
- **Then.** (1) The collector's file holds the 1,000 records and the sender logged `Delivered` for exact, contiguous sequence ranges. (2) The sender logged `Failed` for the batches sent while the collector was down and no batch reported `Delivered` for them. (3) The request in flight is cancelled, the sender logged `Cancelled`, and no further connection reaches the collector.
- **Cleanup.** Stop the collector, delete its file and the home.

<a id="dh-e-host-12"></a>
#### DH-E-HOST-12 Opting in to analytics sends exactly the declared catalog and nothing else

- **Proves.** DH-D-HOST-6.
- **Given.** The setup of [DH-E-HOST-11](#dh-e-host-11) without the test-only sender; a recording forward proxy; a real browser.
- **When.** (1) Turn analytics on in the settings page and use the UI for ten minutes. (2) Turn it off and use the UI again.
- **Then.** (1) The collector's file holds only declared events with enumerated fields and the five resource attributes; the identity file was created at the first accepted record. (2) No further record arrives.
- **Cleanup.** Stop the collector and proxy, delete the collector's file and the home. If an undeclared field arrived, keep the file and the harness configuration.

<a id="dh-e-host-7"></a>
#### DH-E-HOST-7 Linux: a command run for the model cannot make itself the owner

- **Proves.** DH-D-HOST-2, DH-D-HOST-3, with [DH-D-SANDBOX-7](sandbox-ssh.md#dh-d-sandbox-7) and [DH-D-EXEC-2](execution.md#dh-d-exec-2).
- **Given.** The built binary on three Linux machines: (a) bubblewrap; (b) Landlock only, with a desktop session bus; (c) as (a) but the test user's control group is not writable and no user service manager runs, so the process-group rung is used. On each: a fresh home holding one stored key `K` in `file` protection; a running web host with one signed-in real browser; a session whose one tool always asks; a fixture script `takeover` that the shell tool runs. The script tries, in order: read `<home>/secrets` and the session table; connect to the control socket and call each method; request `/` and post an answer to the pending approval, first with no cookie and then with the browser's cookie value planted for the test; exchange a launch token planted for the test; add a session row to the home's database and a line to a config layer; read the host's memory and descriptors through `/proc`; ask the user service manager to run the same steps from outside; call `dsh tool call` for the asking tool.
- **When.** Run `takeover` under `workspace-write`, again with `network: denied`, and again under `danger-full-access`.
- **Then.** Confined, on every machine: no write lands in the home; no control-socket reply contains a link or a cookie; the `/proc` reads fail; the tool call reaches the host and the approval stays pending for the person. On (a) and (b) the exchange and both posts are refused with `auth/untrusted-peer` and `PeerRule()` is `Exact`; on (c) it is `BestEffort` and each outcome is recorded, not assumed. On (a) `secrets/` and `run/` are empty and the service manager cannot be reached. On (b) the stored key file is readable and the result lists `secret-read`, the control socket answers without a credential, and the service-manager step is listed as `delegated-execution` with its recorded outcome. Under `danger-full-access` the run is reported `unconfined`; whatever succeeds is recorded as the residual risk of [DH-ADR-HOST-12](#dh-adr-host-12), and in no run did a harness interface return a credential.
- **Cleanup.** Sign out every browser, stop the host, delete the home, remove any unit the service-manager step created. If a confined step that the Then clause refuses succeeded, keep the script's log, the sandbox arguments and the host log; the scenario has failed.

<a id="dh-e-host-8"></a>
#### DH-E-HOST-8 macOS: the same attempt under Seatbelt

- **Proves.** DH-D-HOST-2, DH-D-HOST-3, with [DH-D-SANDBOX-7](sandbox-ssh.md#dh-d-sandbox-7).
- **Given.** A macOS desktop session with the built binary; the home, host, browser, session and `takeover` script of [DH-E-HOST-7](#dh-e-host-7), with `K` once in `file` and once in `keychain` protection. The macOS steps replace the Linux ones: read the keychain item with `/usr/bin/security`; attach a debugger to the host; submit a job to `launchd` that repeats the steps from outside; send the browser an Apple Event that opens the approval page.
- **When.** Run `takeover` under `workspace-write`, with `network: denied`, and under `danger-full-access`.
- **Then.** Confined: no write lands in the home; the read of `<home>/secrets` and the connection to the control socket are refused by the profile; the debugger cannot attach; no reply contains a credential. `PeerRule()` and the outcome of the cookie replay, the keychain read, the `launchd` job and the Apple Event are recorded as observed and shown by `dsh sandbox status`; each one that succeeds is listed as an effect the backend does not govern, never as a pass. Under `danger-full-access` the result is as in [DH-E-HOST-7](#dh-e-host-7).
- **Cleanup.** As [DH-E-HOST-7](#dh-e-host-7); remove the submitted job and the keychain item; if cleanup fails record their labels.

<a id="dh-e-host-9"></a>
#### DH-E-HOST-9 Windows: the same attempt under the restricted token

- **Proves.** DH-D-HOST-2, DH-D-HOST-3, with [DH-D-SANDBOX-7](sandbox-ssh.md#dh-d-sandbox-7) and [DH-D-EXEC-2](execution.md#dh-d-exec-2).
- **Given.** Windows 11 and Windows Server 2022 with the built binary; the home, host, browser, session and `takeover` script of [DH-E-HOST-7](#dh-e-host-7), written for PowerShell, with `K` once in `file` and once in `keychain` protection. The Windows steps: read `<home>\secrets` and the credential-manager item; open the control socket; open the host process for memory read; register a scheduled task and start an out-of-process COM server that repeat the steps from outside; break away from the job.
- **When.** Run `takeover` under `workspace-write` and under `danger-full-access`.
- **Then.** Confined: no write lands in the home; the reads of `secrets\` and the connection to the control socket fail because of the directory label; the host process cannot be opened; the job cannot be left; the exchange and both posts are refused with `auth/untrusted-peer` and `PeerRule()` is `Exact`. The outcome of the credential-manager read, the scheduled task and the COM server is recorded as observed and listed as ungoverned where it succeeds. `dsh tool call` either reaches the host or reports `unavailable`, and the result says which. Under `danger-full-access` the result is as in [DH-E-HOST-7](#dh-e-host-7).
- **Cleanup.** As [DH-E-HOST-7](#dh-e-host-7); delete the scheduled task; run `dsh sandbox cleanup`; if a label on `secrets\` or `run\` cannot be read back, keep the security descriptors.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Decided by the owner, and one thing nobody decided.** The running host shows the sign-in link itself and the remaining gaps are accepted and reported ([DH-ADR-28](../design.md#dh-adr-28)); the off-switch stops senders only ([DH-ADR-27](../design.md#dh-adr-27)); the update check and the client version are kinds that are off until turned on ([DH-ADR-32](../design.md#dh-adr-32)). Not decided, because it is organisational: nobody is named to operate a collector for this project, and until someone is, every flow on the exporter is unavailable in a stock installation.
2. **To be learned by running; each is tied to a scenario or stays open.** Cookies ignore ports: a browser sends the cookie for `127.0.0.1:3080` to any server on `127.0.0.1`, including a development server the agent started. The peer rule refuses a replay from inside a tree; whether serving the UI under a `.localhost` name scopes the cookie in every supported browser is recorded by [DH-E-HOST-1](#dh-e-host-1) step 7. Whether a Unix domain socket under the home works on every supported Windows version and reports its peer. Whether a loopback TCP peer can be mapped to a process on macOS without native code; if not, the rule is `NotApplied` there. Whether marking the host non-dumpable hides anything the crash report needs. Whether the runtime's crash output is complete on Windows when the console is closed. The cost of the certificate file check per handshake. Collector compatibility beyond the stock OpenTelemetry Collector.
3. **Limits kept.** No certificate is issued or renewed. No "open this file" and no "reveal in file manager". No application icons. Analytics and telemetry are best effort. A crash report can contain private paths. The residual risk stated in [DH-ADR-HOST-12](#dh-adr-host-12).

## 11. Units dispositioned by this spec

63 assigned units, all with a row in `trace/fragments/A18.jsonl`: 12 `UP-PKG` (5 ported, 3 replaced, 1 merged, 3 dropped), 35 `UP-DEBT` (31 resolved, 1 not-applicable, 3 carried), 5 `UP-EVT` (1 ported, 4 replaced), 7 `UP-CFG` (5 ported, 1 replaced, 1 dropped), 4 `UP-SVC` (2 ported, 2 replaced). The same fragment holds final verdicts for the 5 `UP-DEC` notes (all ported, three with changes) and declares 12 decisions (`DH-ADR-HOST-1` to `DH-ADR-HOST-12`), 10 deliverables (`DH-D-HOST-1` to `DH-D-HOST-10`), 31 local tests (`DH-T-HOST-1` to `DH-T-HOST-31`) and 12 scenarios (`DH-E-HOST-1` to `DH-E-HOST-12`).
