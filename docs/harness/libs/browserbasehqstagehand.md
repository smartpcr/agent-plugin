# @browserbasehq/stagehand: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Nothing here was built, run or tested.
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The owner confirmed
> the drop of the Stagehand AI layer that this document recommended: the harness's own
> model drives plain browser actions, all logged
> ([DH-ADR-STAGEHAND-1](#dh-adr-stagehand-1)). It is no longer a recommendation awaiting
> a decision. Two other owner decisions touch this document: there is no embedded
> browser pane in the UI, so the browser this provider drives is always a separate
> browser process, and approvals are one-shot plus session grants (risk 7).

| | |
|---|---|
| **Library ID** | `DH-LIB-STAGEHAND` (with `DH-LIB-CDPROTO` for the protocol types it needs) |
| **Upstream unit** | `UP-DEP-@browserbasehq/stagehand` |
| **Upstream version** | 4.4.0, needs Node 22.18 or later (`pnpm-lock.yaml:21182`) |
| **Kind** | runtime; a Node library that drives Chromium and makes its own model calls |
| **Used by** | `experimental` (`packages/experimental/browser-use-stagehand-native`) |
| **Go choice** | named Go module `github.com/chromedp/chromedp` plus own code. The AI layer is not kept: confirmed by the owner on 2026-11-18. |
| **Related areas** | browser-use seam (`../areas/document-browser-computer.md`) and providers (`../areas/x-cua-browser-voice.md`); siblings [puppeteerbrowsers.md](puppeteerbrowsers.md), [playwrightmcp.md](playwrightmcp.md), [chrome-devtools-mcp.md](chrome-devtools-mcp.md), [playwright.md](playwright.md) |

## 1. What it is and why upstream needs it

**Browser use** lets the model inspect and operate web pages. The harness keeps the task
loop; a **provider** supplies the browser operations and keeps one browser per live
session (`docs/subsystems/browser-use.md:5`, `:21`). Upstream ships three providers. Two
start a ready-made MCP server (see [playwrightmcp.md](playwrightmcp.md)). The third,
"native Stagehand", calls a library in the host process, and that library is this
dependency.

Stagehand does two different jobs. First, it is a browser driver: it connects to
Chromium over the **Chrome DevTools Protocol** (CDP, the debugging protocol built into
Chromium) and can navigate, list tabs and take screenshots. Second, it is an **AI
layer**: `act`, `observe` and `extract` take an instruction in plain language, send the
page and the instruction to a model that Stagehand calls itself, and turn the answer
into browser actions or structured data. Upstream uses both jobs: three of its six tools
are plain driver calls and three are AI-assisted (`src/index.ts:168`).

The second job is the reason this specification is long. A tool that calls a model of
its own sits outside everything the harness guarantees about model calls. Section 5
decides what the Go design does about it.

## 2. How upstream uses it

All paths are under `packages/experimental/browser-use-stagehand-native/`.

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `src/native.ts:94` to `:98` | Connect to an already running Chromium and create the SDK object | `localBrowser.connect({cdpUrl})`, `Stagehand.create({browser, model, logging})` |
| `src/native.ts:103` to `:108` | `stagehand_navigate`: open a URL in a tab, return tab id, URL and title | `page.goto(url, {timeout})`, `page.url()`, `page.title()` |
| `src/native.ts:109` to `:119` | `stagehand_tabs`: list, open, select, close | `context.newPage`, `setActivePage`, `pages`, `activePage`, `page.close` |
| `src/native.ts:120` to `:128` | `stagehand_screenshot`: PNG of the viewport or the full page | `page.screenshot({type: 'png', fullPage})` |
| `src/native.ts:129` to `:136` | `stagehand_act`: one action described in words | `stagehand.act(instruction, {page, timeout})` |
| `src/native.ts:137` to `:142` | `stagehand_observe`: list candidate actions for an instruction | `stagehand.observe(instruction, ...)` |
| `src/native.ts:143` to `:150` | `stagehand_extract`: page data, optionally shaped by a JSON Schema | `stagehand.extract(instruction, schema?, ...)`, with `z.fromJSONSchema` |
| `src/native.ts:27` | Check the configured model against the SDK's catalogue | `StagehandClientCreateConfigSchema` |
| `src/worker-client.ts:19` to `:31` | Run the SDK in a worker thread with an empty environment | `node:worker_threads` |

What the model-facing tools need from the library, summarised:

| Need | Upstream source | Kind |
|---|---|---|
| Navigate, tab list and tab control | Stagehand's driver half | plain CDP |
| Screenshot | Stagehand's driver half | plain CDP |
| Click, type, press keys | only through `act` | AI layer |
| A description of the page the model can act on | only through `observe` and `extract` | AI layer |
| Input events and an accessibility tree as tools | **not offered** | n/a |

Process model. The host starts Chromium itself and owns the process and its temporary
profile; the SDK runs in a worker and only connects over CDP (`README.md:90`; see
[puppeteerbrowsers.md](puppeteerbrowsers.md)). In attach mode nothing is started: the
provider connects to an endpoint from configuration and reserves it for one session
(`README.md:63`). Tool arguments can never choose the endpoint or the model
(`README.md:63`).

Model calls. `model` is required even for navigation-only use, and only OpenAI,
Anthropic, Google, Groq and Cerebras models are accepted; DeepSeek endpoints and custom
base URLs are not (`README.md:43`, `:156`). The API key and optional headers are passed
through the worker into a browser extension that Stagehand installs in Chromium, and
that extension sends the requests (`README.md:45`, `:94`).

## 3. Tech debt upstream carries

Search: `patches/` and `pnpm-workspace.yaml` for `stagehand` (one exemption, no patch),
every import of the package, and the provider's source for launch flags. The bullets
under the provider README's "Known Limitations and Deferred Work" are units owned by the
providers area; they are referred to here, not dispositioned.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-pin.minimumReleaseAgeExclude.@browserbasehq/stagehand@4.4.0` | The package is pinned exactly and exempted from the installer's waiting period for new releases, because only this release agrees with the workspace's reviewed `zod` version. The provider must pin `zod` too, because it hands schema objects to the SDK. | `pnpm-workspace.yaml:110`; `package.json:49`, `:54`; `README.md:94` | The dependency is gone ([DH-ADR-STAGEHAND-1](#dh-adr-stagehand-1)). No schema object crosses into a third-party library, so there is no version coupling. See risk 1 for the Go form of the waiting-period concern. |
| `UP-DEBT-found.L13.1` | The launched Chromium opens a DevTools port on TCP with every origin allowed and unsafe extension debugging switched on, because Stagehand's extension must connect to it. Any local process that finds the port can drive the browser. | `src/launch.ts:43` | The launched browser is driven over an inherited pipe with no listening port, and neither flag is passed ([DH-ADR-STAGEHAND-3](#dh-adr-stagehand-3)). |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-STAGEHAND-R1` | A browser-use provider works with no Node runtime and no operator-installed server: the harness binary and an installed Chromium-family browser are enough. | Risk 1 of [playwrightmcp.md](playwrightmcp.md); DH-ADR-5 |
| `DH-LIB-STAGEHAND-R2` | The provider offers navigate, tab list and control, and screenshot with the behaviour of section 2: an unknown tab is an error that tells the model to list tabs; a screenshot is stored as an attachment before the event that references it. | `src/native.ts:103` to `:128`, `:162` |
| `DH-LIB-STAGEHAND-R3` | The model can read a bounded text description of the page built from the browser's accessibility tree, in which each actionable element carries an opaque reference. A reference is valid until the next description of that tab; a stale one is an error, never a guess. | Replaces `observe`; rule taken from `docs/subsystems/computer-use.md:20` |
| `DH-LIB-STAGEHAND-R4` | The model can click, type, press a key, scroll and wait, addressing an element by reference or a point by coordinates in the last screenshot. A result reports that input was delivered, not that the intended effect happened. | Replaces `act`; `README.md:121` |
| `DH-LIB-STAGEHAND-R5` | No provider code calls a model. Every model request caused by browser use is a request of the session's own agent or of a logged subagent. | `DH-O-4`, `DH-O-8` |
| `DH-LIB-STAGEHAND-R6` | Each live session owns one browser. Calls for one session run one at a time; the browser survives across turns and is closed with the session. A fork or a resumed session starts with a fresh browser, and the model is told browser state is not restored. | `docs/subsystems/browser-use.md:21`; `README.md:155` |
| `DH-LIB-STAGEHAND-R7` | Attach mode connects to an endpoint named only in configuration, reserves it for one session, and on teardown disconnects without closing the browser. A second session gets a "busy" error. | `README.md:63`; `docs/subsystems/browser-use.md:23` |
| `DH-LIB-STAGEHAND-R8` | Cancelling a call returns promptly, never claims the action was undone, and leaves the browser usable. Failed cleanup keeps the reservation and is reported. | `README.md:157`, `:159` |
| `DH-LIB-STAGEHAND-R9` | The confinement rules of [DH-ADR-STAGEHAND-3](#dh-adr-stagehand-3) hold: no listening debug port for a launched browser, a private profile, a scrubbed environment, a navigation guard, and an approval before the first use of an attached browser. | `upstream/security.md` DH-ADR-SECURITY-5, -6, -10 |

## 5. Go choice

**Decision.** The Go harness ships a **native CDP provider**: harness code that drives
Chromium through `github.com/chromedp/chromedp` and exposes plain browser actions to the
session's own model. Stagehand is not used, and nothing replaces its AI layer inside the
provider. The two MCP recipes of [playwrightmcp.md](playwrightmcp.md) stay as optional
providers for operators who have Node.

**Why.** It is the only option that meets `DH-LIB-STAGEHAND-R1` and
`DH-LIB-STAGEHAND-R5` together.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **A. chromedp, a pure-Go CDP client** (chosen) | No Node, no driver process, no cgo. Ships in the one binary. Can use a pipe so no port is opened. Exposes an `Allocator` interface, so the harness can start the browser with its own spawn helper. | Chromium family only. A tool catalogue to design and keep stable. No ready-made "page snapshot": the harness builds it from the accessibility domain. | `github.com/chromedp/chromedp` v0.24.0, published 2026-11-18; previous release v0.23.0 on 2026-11-05. MIT (`LICENSE`). `go.mod` requires only `github.com/chromedp/cdproto` and `github.com/go-json-experiment/json` (BSD-3-Clause), and declares `go 1.25`. Text search for `import "C"` in all three module directories found nothing. Read in source: `NewAllocatorContext`, the `Allocator` and `Transport` interfaces, `NewPipeConn`, `NewBrowserTransport`, and package `remote` with its own WebSocket client. |
| B. rod, another pure-Go CDP client | Higher-level page API. | Newest tagged release is over two years old. Its launcher downloads a Chromium build by default when it finds none (`lib/launcher/browser.go:104`), which the harness forbids. | `github.com/go-rod/rod` v0.116.2, 2024-07-12, MIT, no `import "C"` found. |
| C. Go binding to Playwright | Chromium, Firefox and WebKit. Auto-waiting locators. | Not Node-free: it runs `node cli.js` from a driver directory and downloads both the driver and a Node binary (`run.go:99`, `:153`, `:232`). | `github.com/playwright-community/playwright-go` v0.6401.0, 2026-11-04, MIT, no `import "C"` found. The `go.mod` in the module cache declares `module github.com/mxschmitt/playwright-go`; both paths resolve to this version. |
| D. Only the MCP recipes | No browser code in the harness. | Needs Node and a server the operator installs; no browser use out of the box ([playwrightmcp.md](playwrightmcp.md), tradeoff). | See that specification. |
| E. Keep Stagehand as an external Node program | Same six tools as upstream. | Needs Node 22.18; keeps the second model, its key and its unlogged requests. | Not examined further. |

All module facts were checked on 2026-11-18 with `go list -m -json <module>@latest`,
`go mod download -json <module>@<version>`, and by reading `LICENSE`, `go.mod` and the
named source files in the module cache. `github.com/mafredri/cdp` was seen with
`go list` only (v0.35.0, 2024-09-01) and is not a candidate.

`DH-LIB-CDPROTO` records the second module the provider imports directly:
`github.com/chromedp/cdproto` v0.160.5 (2026-11-18), MIT, no `import "C"` found. It
holds the generated Go types for every CDP domain; chromedp v0.24.0 requires v0.160.1 or
later. The harness needs it by name because the page description and the navigation
guard call the `accessibility` and `fetch` domains, which chromedp does not wrap. It is
generated from the protocol definition and released often, so its version is pinned
together with chromedp's and moved only with it.

**Tradeoff.** The harness now owns a model-facing tool catalogue and a page-description
format, and must keep both stable. Firefox and WebKit are out of reach, as they are
upstream (`README.md:154`). Without the AI layer, the session's model reads page
descriptions itself, which costs main-conversation tokens (see DH-ADR-STAGEHAND-1).

<a id="dh-adr-stagehand-1"></a>
### DH-ADR-STAGEHAND-1 Browser tools are plain actions; no tool runs a model of its own

- **Decision.** Confirmed by the owner on 2026-11-18: the Stagehand AI browser layer is
  dropped, and the harness's own model drives plain browser actions, all logged. Verdict
  on upstream's AI-assisted `act`, `observe` and `extract`:
  **drop**, and **replace** their purpose with a page description plus plain input
  actions that the session's own model uses. If AI-assisted extraction is wanted later,
  it is built as a subagent or a second request through the harness's LLM runtime, never
  inside a provider.
- **Why: "model-visible means logged".** `DH-O-4` promises that every model request can
  be rebuilt from the session log. With Stagehand the session model's requests still
  can, because tool input and output are logged. But a second model also runs. Its
  prompts contain page content; its answers decide which element is clicked. None of it
  is in the log: upstream lists request and response capture as deferred
  (`README.md:160`). The log can then say *that* "click the login button" succeeded, but
  not *what* was clicked or why. A replay cannot reproduce it.
- **Why: failures and billing.** `DH-O-8` requires one logged event per provider attempt
  and no silent crossing of a billing boundary. Stagehand's requests are a second
  billing boundary with no events, outside the session's usage accounting
  (`README.md:142`), with a key that is not in the harness credential store, sent to a
  vendor the user did not choose for the session, and limited to five vendors that
  exclude the one this product is built around (`README.md:43`).
- **Why: cancellation.** `DH-O-3` requires cancellation to reach every operation.
  Stagehand's inference has no abort signal; cancelling waits for it to drain
  (`README.md:157`).
- **Cost.** Upstream pays twice: the session model's tokens, plus at least one Stagehand
  request carrying page content for every AI-assisted call, the second not shown to the
  user. Go pays once, visibly, but in the main conversation: a page description is
  larger than `act`'s one-line result. The cost is bounded by a node and depth limit on
  the description, by listing only actionable and named elements by default, and by
  the existing result-size bound of the tool pipeline. A long browsing task can be given
  to a subagent, whose requests are logged like any other.
- **Tradeoff.** Weaker models act less reliably from a raw page description than through
  a purpose-built layer. Inference: this is acceptable because the two MCP providers
  already work this way (`README.md:84` of `browser-use-playwright-mcp`).
- **Serves.** [DH-O-4](../design.md#dh-o-4), [DH-O-8](../design.md#dh-o-8),
  [DH-O-3](../design.md#dh-o-3). **Verified by.** `DH-T-STAGEHAND-3`.

<a id="dh-adr-stagehand-2"></a>
### DH-ADR-STAGEHAND-2 A Node-free CDP provider is the default browser-use provider

- **Decision.** The harness contains one compiled-in provider, named `cdp`, built on
  chromedp. It is the provider a bundle selects when browser use is enabled. The
  Playwright MCP and Chrome DevTools MCP recipes remain selectable. As upstream, one
  provider per composition (`docs/subsystems/browser-use.md:17`). Verdict on upstream's
  "no common browser operations, three provider-owned catalogues": **keep** the seam as
  registration plus provider-owned tools; **change** the default from none to `cdp`.
- **Why.** It answers the open question raised by the MCP specifications: without it a
  Go installation has no browser tools until the operator installs Node.
- **Tradeoff.** Tool names differ between providers, so switching provider changes what
  the model sees. Inference: `DH-O-10` asks that a provider change not alter the tool
  schema; that holds only if the area defines a common catalogue. This specification
  does not settle it (risk 4).
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-14](../design.md#dh-o-14).
  **Verified by.** `DH-T-STAGEHAND-1`, `DH-T-STAGEHAND-2`, and the real-browser scenario
  in section 8.

<a id="dh-adr-stagehand-3"></a>
### DH-ADR-STAGEHAND-3 How a harness-driven browser is confined and approved

A browser is not covered by the file sandbox: Chromium runs its own sandbox and cannot
be nested inside bubblewrap or Seatbelt without disabling one of them. So the harness
states separate rules.

| Rule | Why | Tradeoff |
|---|---|---|
| (a) A launched browser is driven over an inherited pipe. No debugging port is opened; `--remote-allow-origins` and `--enable-unsafe-extension-debugging` are never passed. | Removes `UP-DEBT-found.L13.1`: nothing else on the machine can attach. | A developer cannot attach DevTools to the session's browser. |
| (b) It gets a new private profile directory and the scrubbed child environment, through the one spawn helper. | Upstream rule (`README.md:90`); DH-ADR-SECURITY-6. No cookies or logins from the user's own browser. | The user logs in again inside the session's browser. |
| (c) Navigation accepts only `http` and `https`. A top-level request to a loopback, link-local, private or metadata address is refused unless the operator allows it, by the same switch as the fetch tool. The check is made on the request the browser sends, including redirects, by intercepting document requests. | DH-ADR-SECURITY-10 refuses these for fetch; a browser is a second way to reach them. `file:` would bypass the file tools' fence. | Local development servers need the switch. Sub-resource requests are not checked in the first version; this is a stated gap. |
| (d) Attach mode needs an approval before the first call in a session, answered through the interaction registry; with no answerer it is denied. The endpoint comes from configuration only and must be a loopback address unless the operator says otherwise. | An attached browser carries the user's logins; an action there is an action as the user. | One prompt per session. Whether it can be remembered depends on the open decision about remembered approvals. |
| (e) File upload and download are not offered in the first version. | A download writes outside the write roots; an upload reads any file the user can read. | Sites that need a file cannot be completed. |
| (f) There is no "run JavaScript in the page" tool in the first version. | It turns injected page text into arbitrary script with the user's cookies. | Some pages cannot be operated by input events alone. |

- **Residual risk, stated to the operator.** Page content is untrusted text that
  reaches the model; it can ask the model to do anything its other tools allow.
  Launch-mode pages can still send data anywhere on the public internet.
- **Serves.** [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-STAGEHAND-4`,
  `DH-T-STAGEHAND-5`, and the real-browser scenario in section 8.

## 6. Design notes

The provider belongs to the experimental family `internal/x/...` behind the browser-use
contract in `internal/media`; the owning areas fix the names.

**Seam between harness and chromedp.** The harness implements chromedp's `Allocator`
interface. Its `Allocate` starts the browser through the spawn helper described in
[puppeteerbrowsers.md](puppeteerbrowsers.md) and returns
`NewBrowserTransport(ctx, NewPipeConn(read, write))`. chromedp's own `ExecAllocator` is
not used, because it calls `exec.Command` itself and would escape the single spawn
helper of DH-ADR-SECURITY-6. Attach mode uses `remote.NewAllocator(ctx, endpoint)`.
Only the provider package imports chromedp.

**Proposed tool set** (the providers area owns names and schemas): `navigate`, `tabs`,
`snapshot` (the page description), `screenshot`, `click`, `type`, `press_key`, `scroll`,
`wait_for`. CDP domains used, all present in `github.com/chromedp/cdproto` v0.160.5:
`page`, `target`, `accessibility`, `dom`, `input`, `fetch`.

| Resource | Owner (releases it) | Who may use it |
|---|---|---|
| Launched browser process, its pipe and its profile directory | The session's fiber | Only that session's agents, one call at a time |
| Connection to an attached browser | The session's fiber | The one session holding the reservation |
| The attached browser itself | Its external owner; the harness only disconnects | Shared: its user may act in it at the same time |

**Failure, retry, cleanup.**

- *Start fails or is cancelled.* The process tree is stopped and the profile removed
  before the error is returned; nothing is reserved.
- *The browser crashes or the pipe closes.* Pending calls fail with one error kind. The
  next call starts a fresh browser and its result says that earlier pages and logins are
  gone. Nothing is retried automatically.
- *A call is cancelled.* The CDP command is abandoned and the result says the outcome is
  unknown: input may have been delivered. The browser stays.
- *Session ends.* Stop admitting calls, wait for the running one, close the transport,
  stop the process tree, then remove the profile. If the process cannot be confirmed
  dead or the profile cannot be removed, the reservation is kept, the path is logged,
  and the state is reported as uncertain (kept from `README.md:159`).
- *Host dies.* A job object on Windows ends the browser. Inference: on POSIX the browser
  exits when its end of the pipe closes; this must be confirmed by the real-browser
  scenario, with a process-group kill by the next host start as the fallback. The
  orphaned profile directory remains; start-up sweeps directories with the harness
  prefix that no live process holds.

## 7. Risks and open questions

1. **Freshness of the pin.** chromedp v0.24.0 and cdproto v0.160.5 were published on the
   day of this check. Upstream's installer refuses releases younger than a set age
   unless exempted; Go's checksum database proves a version never changes, not that a
   new one is sound. Recommendation for the toolchain owner: adopt a version only after
   a stated age or a recorded review. Not decided here.
2. **Pipe-default API is new.** The pipe transport and `NewBrowserTransport` were read
   in v0.24.0 only. Whether they work with a browser started outside chromedp on
   Windows (handle inheritance) can only be learned by running it. Fallback: a port
   bound to loopback with a random browser path, which reopens part of
   `UP-DEBT-found.L13.1`.
3. **Stale nested module.** The proxy also serves a separately tagged module
   `github.com/chromedp/chromedp/remote` v0.2.0 (2026-10-05) that depends on
   `github.com/gobwas/ws`, while v0.24.0 contains package `remote` itself. Whether a
   build could resolve the import ambiguously must be confirmed when `go.mod` is written.
4. **Common catalogue.** Whether `cdp` and the MCP recipes present one tool catalogue
   (`DH-O-10`) is for `../areas/document-browser-computer.md`.
5. **Go version.** chromedp declares `go 1.25`; the harness module's `go` line must be
   at least that (open in `../upstream/toolchain.md`).
6. **Navigation guard depth.** Rule (c) checks document requests. Whether sub-resources
   and WebSockets are checked too is for the network policy owner.
7. **Remembered attach approval.** The owner settled open decision OD-10 on 2026-11-18:
   approvals are one-shot, except that the user may grant a named scope until the
   session ends, and nothing is stored across sessions. An attach approval can therefore
   be remembered for the rest of a session at most; whether "this browser" is offered as
   a named scope is for the interaction area.

## 8. Verification

Local tests use a fake CDP peer: a Go test double that speaks the protocol over an
in-memory pipe. Nothing here has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-STAGEHAND-1` | local | `DH-LIB-STAGEHAND-R2`, `DH-LIB-STAGEHAND-R3`, `DH-LIB-STAGEHAND-R4` | Against the fake peer: navigate returns id, URL and title; an unknown tab id gives the "list tabs" error; a description lists only actionable and named nodes up to the limit and marks truncation; a click with a reference from an older description is refused before any input command is sent. |
| `DH-T-STAGEHAND-2` | local | `DH-LIB-STAGEHAND-R6`, `DH-LIB-STAGEHAND-R7`, `DH-LIB-STAGEHAND-R8` | Through the loader: two sessions get two allocations; two calls of one session never overlap; a second session attaching to the same endpoint gets "busy"; cancelling mid-call returns "outcome unknown" and the next call succeeds; unloading the provider ends every fake process and removes every profile directory. |
| `DH-T-STAGEHAND-3` | local | `DH-LIB-STAGEHAND-R5` | An import-direction test fails if the provider package imports the LLM runtime or any HTTP client package; a replay of a recorded browsing session rebuilds every model request from the log alone. |
| `DH-T-STAGEHAND-4` | local | `DH-LIB-STAGEHAND-R9` | The argument list given to the spawn helper contains `--remote-debugging-pipe` and a profile path, and never a debugging port, `--remote-allow-origins` or `--enable-unsafe-extension-debugging`; the child environment has no credential-shaped name. |
| `DH-T-STAGEHAND-5` | local | `DH-LIB-STAGEHAND-R9` | The fake peer announces a document request to `http://169.254.169.254/` reached by redirect: the provider fails the request and the tool result names the rule. `file:` and `javascript:` URLs are refused before any command. Attach with no answerer is denied and no connection is opened. |

**Real-browser scenario handed to the providers area** (a different kind of evidence; a
skipped run stays open). **Given** a Linux, a macOS and a Windows runner, each with a
stable Chrome or Edge, the built `dsh` binary, the replay provider scripted to call the
browser tools, and a local page served by the test on an allowed loopback address.
**When** the session navigates to the page, takes a description, clicks a button by
reference, types into a field, takes a screenshot, then the session is closed.
**Then** the page's own server saw the click and the text; the screenshot attachment is
a PNG of the page; while the browser ran, no TCP port was listening in the browser
process; after close the browser process and its profile directory are gone.
**Cleanup.** The test stops its page server and removes its harness home; if the browser
process survives, the test fails and keeps the process list and the profile path.

## 9. Units dispositioned by this spec

| Unit | Disposition |
|---|---|
| `UP-DEP-@browserbasehq/stagehand` | replaced by `DH-LIB-STAGEHAND` under DH-ADR-STAGEHAND-1 and DH-ADR-STAGEHAND-2 |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@browserbasehq/stagehand@4.4.0` | resolved by DH-ADR-STAGEHAND-1 |
| `UP-DEBT-found.L13.1` | resolved by DH-ADR-STAGEHAND-3 |
