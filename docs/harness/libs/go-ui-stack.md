# The Go UI stack (templates, htmx, server-sent events, islands, reaching the UI from a browser): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Nothing here was compiled, run or opened in a browser.
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The owner decided
> that there is no desktop application: the user's browser is the only client of the UI.
> The Wails shell is removed from the chosen stack and what was learned about it is kept
> in [section 5.5](#55-considered-and-not-used-a-desktop-shell). New in this revision:
> how the user starts and reaches the UI, an installable web app manifest, and browser
> notifications ([DH-ADR-GOUISTACK-9](#dh-adr-gouistack-9)). Two further owner decisions
> are reflected: the message composer is a rich-text island, and a remote image in model
> output is loaded only after a click.
>
> **How each fact was checked, on 2026-11-18.** Go modules: `go list -m -json <module>@latest`,
> `go mod download -json`, then reading the licence, `go.mod` and source in the module
> cache (Go 1.27.4). JavaScript assets: the npm registry manifest of the exact version, the
> published file on unpkg and the project's documentation pages. A fact that only a real
> build or a real browser can settle is listed in [section 7](#7-risks-and-open-questions)
> and has a named scenario in [section 8](#8-verification).

| | |
|---|---|
| **Library ID** | `DH-LIB-GOUISTACK` (the stack as a whole), with one item per part: `DH-LIB-GOUITEMPLATES`, `DH-LIB-GOUIHTMX`, `DH-LIB-GOUISTREAM` |
| **Upstream unit** | None of its own. This document verifies the stack chosen by [DH-ADR-3](../design.md#dh-adr-3). The four dependencies of this family are in [xtermxterm.md](xtermxterm.md), [d3-time.md](d3-time.md) and [vitejsplugin-react.md](vitejsplugin-react.md) |
| **Upstream version** | n/a |
| **Kind** | Replaces the React client and its Vite build. Upstream's Electron shell has no replacement: its features are mapped in [electron.md](electron.md) |
| **Used by** | Every UI area (C1 to C7), the web application (A25), the web host (A18, A19), the command line (A24) for the launch |
| **Go choice** | `html/template` (standard library); htmx 2.0.11 as one vendored file; own stream script on the browser's `EventSource`; islands as custom elements; no desktop shell: `dsh` opens the system browser |
| **Related areas** | C1, which holds the policy value, the shell, the first-party script and the island loader ([../areas/ui-core.md](../areas/ui-core.md)); A19, whose `web` component writes every security header and owns the stream hub ([../areas/api.md](../areas/api.md)); A18 (launch); A25 (the web application and its scenario scaffold); the other UI areas C2 to C7; [electron.md](electron.md), [open.md](open.md), [ws.md](ws.md), [xterm.md](xterm.md), [serve-static.md](serve-static.md), [compression.md](compression.md), [negotiator.md](negotiator.md), [playwright.md](playwright.md) |

**The five answers in one table.**

| Part | Choice | Verified fact that decides it | Decision |
|---|---|---|---|
| Templates | `html/template`, behind typed view functions | Standard library, contextual escaping, no generator. `templ` is real and good (v0.3.1118, MIT) but is pre-1.0 and adds a code generator | [DH-ADR-GOUISTACK-1](#dh-adr-gouistack-1) |
| Page updates | htmx 2.0.11, one vendored file, evaluation features off | `latest` on npm, licence 0BSD, no dependencies; 4.0.2 exists only under the `next` tag | [DH-ADR-GOUISTACK-2](#dh-adr-gouistack-2), [-3](#dh-adr-gouistack-3) |
| Live updates | Browser `EventSource` driven by a small first-party script that calls `htmx.swap` | The htmx SSE extension has **no licence** in its manifest or repository, and loses the resume cursor when it re-creates a stream | [DH-ADR-GOUISTACK-4](#dh-adr-gouistack-4), [-5](#dh-adr-gouistack-5) |
| Islands | A custom element in an ES module, loaded on demand, no bundler | xterm.js 6.0.0 ships an ES module build; it also injects `<style>` elements, so it is framed | [DH-ADR-GOUISTACK-6](#dh-adr-gouistack-6) |
| Reaching the UI | No desktop shell. `dsh` starts the host and opens the system browser; an installable web app manifest and browser notifications stand in for a window | Owner decision. A browser tab already does everything the UI needs; what a shell added is mapped in [electron.md](electron.md) | [DH-ADR-GOUISTACK-9](#dh-adr-gouistack-9) |

## 1. What it is and why the harness needs it

Upstream's user interface is a React application of more than sixty packages, built by
Vite, with its own copy of session state, a plugin runtime in the browser and an Electron
shell that bundles a browser engine. [DH-ADR-3](../design.md#dh-adr-3) replaces all of it:
the Go host renders HTML, the browser shows it, and JavaScript is the exception.

Five terms are used throughout.

- A **view** is a Go function that turns typed data into HTML. A **fragment** is the
  output of one view used alone: the piece of a page that changes.
- **htmx** is a JavaScript library (one file) that reads attributes such as `hx-get` and
  `hx-target` from the HTML, makes the request, and puts the returned fragment into the
  page. This replacing is called a **swap**. An **out-of-band swap** is a fragment that
  names its own destination by element `id`, so one response can update several places.
- **Server-sent events (SSE)** is a browser feature (`EventSource`): one long HTTP
  response on which the server writes named events. The browser reconnects by itself and
  tells the server the last event `id` it saw in the `Last-Event-ID` header.
- An **island** is a self-contained JavaScript widget inside a server-rendered page, used
  only where the browser needs code to do the job (a terminal, a rich-text input).
- **Content-Security-Policy (CSP)** is a response header that tells the browser which
  scripts and styles a page may run. It is the second line of defence if hostile text ever
  gets into a page as markup.

Why the stack needs a specification of its own: seven UI area agents each write a
specification, and they must agree on how a fragment is rendered, how a streaming answer
reaches the page, when JavaScript is allowed and how a person gets to the UI now that
the browser is the only client. This document fixes those four things and states what
the stack cannot do.

## 2. How upstream does the same job

| Concern | Upstream mechanism | Evidence (`path:line`) |
|---|---|---|
| Rendering | React components compiled by Vite with the React plugin | `apps/web/vite.config.ts:6`, `:174` |
| Heavy renderers in the browser | KaTeX, Shiki and a micromark Markdown pipeline in a vendor chunk | `apps/web/vite.config.ts:118-138` |
| Live state | The client owns a copy of the session journal, fed by one multiplexed WebSocket | [../upstream/wire-and-api.md](../upstream/wire-and-api.md) section 4 |
| Terminal widget | xterm.js in a lazily loaded chunk | `packages/client/ui-sidebar-terminal/src/client/terminal.tsx:3-4` |
| Languages | English and Chinese are the two required baselines; eight locales ship | `packages/client/locale/src/locale-settings.ts:15`, `packages/client/locale/README.md:12` |
| Desktop | Electron, with `<webview>` guests for the sidebar browser and `electron-updater` | `apps/desktop/README.md:156`, `apps/desktop/package.json:51` |

## 3. Tech debt upstream carries

No `UP-DEBT` unit is assigned to this document. Search performed: the four dependencies of
this family in `patches/` and `pnpm-workspace.yaml` (no patch, pin or exemption). The debt
the stack removes is structural and is dispositioned by the areas that own the code: the
typed RPC gateway and client journal ([DH-ADR-WIRE-1](../upstream/wire-and-api.md#dh-adr-wire-1),
[-3](../upstream/wire-and-api.md#dh-adr-wire-3)), the client build
([build-test-toolchain.md](build-test-toolchain.md)) and the Electron shell with its
process boundary, which is not built at all ([electron.md](electron.md)).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-GOUISTACK-R1` | Every page and fragment is produced by a typed view function. The same function produces the same bytes whether its output is part of a full page, an htmx response or a stream event | [DH-ADR-WIRE-1](../upstream/wire-and-api.md#dh-adr-wire-1), [DH-ADR-WIRE-3](../upstream/wire-and-api.md#dh-adr-wire-3) ("rendering must be a pure function of the durable event") |
| `DH-LIB-GOUISTACK-R2` | Text from the model, a tool, a file or a plugin is escaped in every position (text, attribute, URL). Markup that is not escaped can enter a page only as a value of one sanitizer type | [../design.md](../design.md) section 6, "Tool output and fetched content" |
| `DH-LIB-GOUISTACK-R3` | The binary serves every script, stylesheet, font and island from files embedded in it. A page loads nothing from another origin. Building the product needs no Node, npm or bundler | [DH-ADR-3](../design.md#dh-adr-3); [DH-ADR-TOOLCHAIN-11](../upstream/toolchain.md#dh-adr-toolchain-11) |
| `DH-LIB-GOUISTACK-R4` | Every HTML response carries the base policy of [DH-ADR-GOUISTACK-3](#dh-adr-gouistack-3). No page contains an inline executable script, an inline event handler or an `hx-on` attribute, and htmx's evaluation features are off. The one inline block allowed is the shell's import map, admitted by a fixed hash source (decision 3, consequence 1) | [DH-ADR-SECURITY-7](../upstream/security.md#dh-adr-security-7) |
| `DH-LIB-GOUISTACK-R5` | Each vendored asset is pinned by version, source, licence and SHA-256, and a test fails when the embedded bytes differ from the record | [DH-ADR-TOOLCHAIN-11](../upstream/toolchain.md#dh-adr-toolchain-11) |
| `DH-LIB-GOUISTACK-R6` | A streaming answer reaches the page as stream events that carry HTML fragments, in commit order. A durable event carries the id `<sessionId>:<seq>` | [DH-ADR-WIRE-2](../upstream/wire-and-api.md#dh-adr-wire-2), [-3](../upstream/wire-and-api.md#dh-adr-wire-3) |
| `DH-LIB-GOUISTACK-R7` | After a network cut, a stream the page had to re-create, or a host restart, the transcript shows every durable event once and in order, **whether or not the browser sent `Last-Event-ID`** | [DH-O-12](../design.md#dh-o-12); section 5.3 |
| `DH-LIB-GOUISTACK-R8` | A slow or hidden tab never blocks a session or another tab. One browser holds at most a configured number of streams, so ordinary requests always have a free connection | section 5.3 (the six-connection limit) |
| `DH-LIB-GOUISTACK-R9` | An island loads only when its element is on the page, releases everything when the element is removed, holds no authoritative state, and runs under the policy it declares | [DH-ADR-3](../design.md#dh-adr-3) |
| `DH-LIB-GOUISTACK-R10` | The user reaches the UI by running `dsh`: it starts the host on a loopback listener and hands the system browser one URL that carries a one-time launch token. A second `dsh web` for the same home and address does not start a second host: it asks the running host to show a new one-time link itself, by opening the browser or printing the link on the host's own terminal, and is never handed the link ([DH-ADR-28](../design.md#dh-adr-28); [../areas/host-telemetry.md](../areas/host-telemetry.md), `DH-D-HOST-3`). With the browser launch disabled or impossible, the URL is printed instead. The binary contains no webview code and needs no cgo. The UI works in every browser the project lists as supported | Owner decision (no desktop application); [DH-ADR-WIRE-10](../upstream/wire-and-api.md#dh-adr-wire-10); [open.md](open.md) |
| `DH-LIB-GOUISTACK-R11` | Language, time zone and theme are resolved on the host for each request. English and Chinese are complete | `packages/client/locale/src/locale-settings.ts:15` |
| `DH-LIB-GOUISTACK-R12` | Streamed text does not flood a screen reader, keyboard focus survives a swap, and every control has an accessible name | Inference from what a swap does to the page; upstream states contrast targets only (`packages/client/ui-sidebar-terminal/README.md`, "Use this package") |
| `DH-LIB-GOUISTACK-R13` | A view that fails writes nothing: the response is an error, never half a fragment | Inference: half a fragment swapped into a page corrupts it |
| `DH-LIB-GOUISTACK-R14` | The host serves a web app manifest and icons, so a browser that supports installation can show the UI in a window of its own. Installation is optional: every page works the same in an ordinary tab, and no service worker caches pages or data | Owner decision (no desktop window); Inference for the manifest, see [DH-ADR-GOUISTACK-9](#dh-adr-gouistack-9) |
| `DH-LIB-GOUISTACK-R15` | When an approval or question is waiting, or a turn has finished, and no tab that shows the session is visible, the browser shows a notification, provided the user has allowed notifications for the UI. Permission is asked only after a user gesture. Without permission, or with no browser open, nothing is lost: the state is in the page at the next visit | Replaces the desktop notification and attention request ([electron.md](electron.md), row 10) |

## 5. Go choice

### 5.1 Templates

Both candidates escape by default, compose, and can render one piece alone. They differ in
when a mistake is caught and in what the build needs.

| | `html/template` (standard library) | `github.com/a-h/templ` |
|---|---|---|
| What it is | Templates parsed at start-up; data passed as a value and read by reflection | A language (`.templ` files) compiled by a generator into Go functions |
| Escaping | Contextual: the parser works out whether a value lands in text, an attribute, a URL, a script or a style and escapes for that place. Part of Go, with its security history | Text and attributes are escaped; URL attributes are sanitized. Its attribute rules were rewritten to match `html/template` after advisory GHSA-94c2-xg24-pwhv, and Go expressions inside `<script>` after GHSA-jq3r-rjqp-mwgj (`docs/adr/0004`, `0005`, `0009` in the module) |
| Mistakes caught | When the template runs: an unknown field or a missing template is a run-time error | By the Go compiler: a component is a function with typed parameters |
| Composition | Named templates called with one value; no parameters, no implicit context | Function calls with parameters and children; `context.Context` flows to every component |
| One fragment alone | `ExecuteTemplate(w, "name", data)` | `component.Render(ctx, w)`; also `templ.RenderFragments` (`fragment.go:10`) |
| Build step | None. Templates are embedded text | `templ generate` writes a `_templ.go` file per template; generator and runtime must be the same version (the generated header records it, `generator/generator.go:202`) |
| Stability | Go 1 compatibility promise | v0.3.1118 of 2026-11-12: pre-1.0, a release for almost every commit |
| What ends up in the binary | Standard library | The runtime packages import only the standard library and templ's own packages (import lists of `runtime.go`, `runtime/*.go`, `safehtml/*.go` read). The generator's dependencies (`golang.org/x/tools`, `fsnotify` and others in `go.mod`) stay out of the binary |
| Verified facts | Go 1.27.4 installed | Module `github.com/a-h/templ`, v0.3.1118, MIT (`LICENSE`), `go 1.26.0` |

A third shape, HTML written as Go function calls (for example `maragu.dev/gomponents`),
was not verified and is not proposed.

<a id="dh-adr-gouistack-1"></a>
#### DH-ADR-GOUISTACK-1 Templates are `html/template` behind typed view functions; `templ` is the verified fallback

- **Decision.** Views are `html/template` templates embedded in the binary. No handler
  calls the template engine. A handler calls a typed view function, registered once per
  template with its data type (section 6). `templ` is not adopted now.
- **Why.** The content is hostile by nature (model output, tool output, file names), so
  the escaper with the longest security record wins. The standard library needs no
  generator, so there is no generated code to drift and no tool to pin, which is what
  [DH-ADR-TOOLCHAIN-9](../upstream/toolchain.md#dh-adr-toolchain-9) and
  [-11](../upstream/toolchain.md#dh-adr-toolchain-11) ask for. In the development profile
  templates can be re-read from disk on each request, which gives a fast edit loop with no
  watcher.
- **Tradeoff.** A wrong field name is found by `go test`, not by `go build`. Three rules
  pay for that: the registry gives call sites a typed signature; a test runs every
  registered view with a fixture and compares a golden file (`DH-T-GOUISTACK-1`); and a
  nested template receives its context through one envelope type instead of ad-hoc maps.
  `html/template` does not treat `hx-on` attributes as script, which is one reason they
  are banned ([DH-ADR-GOUISTACK-3](#dh-adr-gouistack-3)).
- **When to revisit.** If the UI stages show that run-time template errors or the
  envelope boilerplate are a real cost, switch to `templ`. The switch stays inside
  `internal/ui`, because a view function and a `templ` component have the same shape:
  typed data in, HTML written to an `io.Writer`. The cost then is one pinned tool (a
  `tool` line in `go.mod`), a `go generate` step and a regenerate-and-compare test, the
  same pattern [DH-ADR-TOOLCHAIN-10](../upstream/toolchain.md#dh-adr-toolchain-10) already
  uses for catalogs.
- **Serves.** [DH-O-12](../design.md#dh-o-12); `R1`, `R2`, `R13`.
- **Verified by.** `DH-T-GOUISTACK-1`, `DH-T-GOUISTACK-2`.

### 5.2 htmx

| Asset | Version | Published | Licence | Notes |
|---|---|---|---|---|
| `htmx.org` (npm) | **2.0.11**, the `latest` tag | 2026-09-22 | 0BSD (manifest of 2.0.11) | No runtime dependencies. Browser file `dist/htmx.min.js`. 35 files, 900 kB unpacked in the package |
| `htmx.org` 4.x | 4.0.2, the `next` tag | 2026-11-18 (4.0.0 on 2026-08-28) | "BSD-0-Clause" in its manifest | Not `latest`. Not adopted; see open question 9 |
| `htmx-ext-sse` (npm) | 2.2.4 | 2025-10-18 | **None found**: no `license` field in the manifest; the repository `bigskysoftware/htmx-extensions` shows no licence file and no licence in its summary | **Not adopted**, see 5.3 |

<a id="dh-adr-gouistack-2"></a>
#### DH-ADR-GOUISTACK-2 htmx 2.0.11 is one vendored file embedded in the binary, with its evaluation features switched off

- **Decision.** The file `htmx.min.js` of htmx 2.0.11 is committed under the UI package's
  asset directory and served from the binary through `embed`
  ([serve-static.md](serve-static.md)). A manifest beside it records version, source URL,
  licence and SHA-256 (`R5`). htmx is configured by a `<meta name="htmx-config">` element,
  which is data and not script:

  | Setting | Value | Default | Reason |
  |---|---|---|---|
  | `allowEval` | `false` | `true` | Turns off everything that evaluates strings as code: `hx-on:` attributes, event filters, and the `js:` prefix in `hx-vals` and `hx-headers` (htmx documentation, "Security") |
  | `allowScriptTags` | `false` | `true` | A swapped fragment can never run a `<script>` it carries |
  | `includeIndicatorStyles` | `false` | `true` | htmx would otherwise inject a `<style>` element, which the policy forbids; the rules go in our stylesheet |
  | `allowNestedOobSwaps` | `false` | `true` | Only top-level elements of a fragment may be out-of-band. Markup nested inside untrusted content can never redirect itself to another part of the page |
  | `historyCacheSize` | `0` | `10` | htmx would otherwise keep snapshots of page HTML, which here means transcripts, in browser storage. The back button re-fetches instead |
  | `historyRestoreAsHxRequest` | `false` | not read | With no cache, the back button makes htmx request the page again. With this off, that request carries no `HX-Request` header, so the handler returns a whole page and not a fragment (htmx documentation, "History Support") |
  | `selfRequestsOnly` | `true` | `true` | Stated so that an upgrade cannot change it silently |

- **Why.** One file and no build is the whole point of [DH-ADR-3](../design.md#dh-adr-3).
  The switches remove every path by which an attribute or a fragment could become code.
- **Tradeoff.** Without `hx-on` and event filters, any behaviour htmx attributes cannot
  express is written in the first-party script (section 6) or in an island. Upgrading
  htmx is a reviewed change to the vendored file, not a version bump.
- **Untrusted markup needs two more rules**, because htmx acts on attributes wherever
  they are. The sanitizer's allow-list has no `hx-*`, `data-hx-*`, `id`, `style` or form
  elements, and every container of sanitized content carries `hx-disable`, which htmx
  honours for the whole subtree and which injected content cannot override.
- **Serves.** [DH-O-12](../design.md#dh-o-12); `R3`, `R4`, `R5`.
- **Verified by.** `DH-T-GOUISTACK-3`, `DH-T-GOUISTACK-4`; real browser: `DH-E-GOUISTACK-3`.

<a id="dh-adr-gouistack-3"></a>
#### DH-ADR-GOUISTACK-3 One Content-Security-Policy: no inline script, no evaluation, no inline style; a weaker policy only inside a frame

- **Decision.** Every HTML document the host serves carries this policy. This
  specification states the constraint; it does not write the header. Area C1 holds the
  value as `ui.BasePolicy` and area A19's `web` component writes it on every HTML
  answer, composed when the route table changes and never per response, with no nonce
  ([../areas/ui-core.md](../areas/ui-core.md) §6;
  [DH-ADR-API-9](../areas/api.md#dh-adr-api-9), which owns the header). The same
  component writes the `Permissions-Policy` header and the other response headers, and
  enforces the forgery rule that belongs with the policy: a request that is not `GET`
  or `HEAD`, and every socket upgrade, must be same-origin, and a request with a body
  must also carry the peer's form token. No view and no handler writes a security
  header.

  ```
  default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:;
  font-src 'self'; connect-src 'self'; frame-src 'self'; manifest-src 'self';
  form-action 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'
  ```

  `manifest-src 'self'` was added in the revision of 2026-11-18: with `default-src 'none'`
  a browser would otherwise refuse to load the web app manifest of
  [DH-ADR-GOUISTACK-9](#dh-adr-gouistack-9). Because the browser is now the only client,
  this one policy is the whole client-side defence; there is no shell-side navigation
  lock behind it.

  Consequences that every UI area must follow:
  1. No inline executable `<script>`, no `on...=` attribute, no `hx-on`. Scripts are
     files from the binary. A **nonce is therefore not needed** and not used; a nonce
     would have to be minted per response and would not survive a fragment rendered for
     a stream event. **One exception, and it is not a nonce:** when a component
     registers script modules, the shell's head holds a single
     `<script type="importmap">`, and `script-src` gains one source, the SHA-256 hash of
     that exact text (`script-src 'self' 'sha256-...'`). The text and the hash are
     computed once at start-up and are the same on every response, so the reason
     against a nonce does not apply. An import map is data: it can only map a module
     name to a same-origin file that `script-src 'self'` already allows, and an
     injected second map does not match the hash. Only a block of type `importmap` can
     be admitted this way: no executable script is ever allowed by hash, and
     `'unsafe-inline'`, `'unsafe-eval'`, `'unsafe-hashes'`, a nonce and a remote source
     are never in a document's policy. Area A19's `web` computes the hash from the
     registered text ([DH-ADR-API-9](../areas/api.md#dh-adr-api-9)); the composer's map
     is area C3's ([DH-ADR-UICHAT-3](../areas/ui-chat.md#dh-adr-uichat-3)). With no
     registered module the header equals the policy above. If a supported browser does
     not honour a hash for an import map, the island that needs the map is framed under
     rule 3.
  2. No inline `style="..."` and no `<style>` element. Per-user values (theme tokens, font
     choice) are served as a stylesheet route; a width or a colour set by data uses a
     class, `<progress>` or `<meter>`.
  3. An island whose third-party code needs more than this policy is mounted in a
     same-origin `<iframe>` whose own document carries the wider policy and
     `frame-ancestors 'self'`. The only known case is the terminal: xterm.js 6.0.0 creates
     `<style>` elements at run time and sets no nonce on them
     (`src/browser/renderer/dom/DomRenderer.ts`, read at tag 6.0.0), so its frame adds
     `style-src 'self' 'unsafe-inline'`.
  4. `img-src 'self'` blocks an image address in model output from being fetched. That is
     deliberate: a remote image is a known way to leak data out of a conversation. The
     owner decided on 2026-11-18 that a remote image is **loaded only after a click**:
     nothing is fetched without a user action. Inference: the policy stays as it is,
     because the click is a request to the host, which fetches the image and serves it
     from its own origin; the placeholder and the fetch rules are specified in
     [micromark.md](micromark.md) (`DH-LIB-MICROMARK-R4`) and by the conversation area.
- **Why.** The product asks a person to approve actions. Markup that reached the page
  unescaped could imitate that prompt. Escaping and the sanitizer prevent it; the policy
  is the control that still holds if one of them has a defect.
- **Tradeoff.** Framing the terminal costs a second document: focus, keyboard shortcuts
  and resize must be handled across the frame boundary. The alternative, allowing inline
  styles in the main document, is simpler and is the fallback if the frame proves
  troublesome; it would weaken the main document for every page.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-12](../design.md#dh-o-12); `R2`, `R4`, `R9`.
- **Verified by.** `DH-T-GOUISTACK-4`; real browser: `DH-E-GOUISTACK-3`.

### 5.3 Live updates

**What happens, end to end.** A model answer is produced as stream deltas inside the
host. The agent commits durable session events to the log; the web host holds one
goroutine per browser connection that receives committed events in order, renders each
with the same view function a page load uses, and writes it as a stream event. The
browser's `EventSource` hands the event to a small first-party script, which passes the
fragment to `htmx.swap`. htmx puts each top-level element where its `hx-swap-oob`
attribute says.

| Event name | Carries | `id` | Fragment rule |
|---|---|---|---|
| `session` | One rendered durable event | `<sessionId>:<seq>` | Appended to the transcript, plus any out-of-band updates the event implies (status badge, title) |
| `live` | The in-flight assistant message | none | Computed **when it is written, against what this connection has already been sent**: the finished blocks not yet sent (appended) and the current unfinished tail (replaced by id), as [DH-ADR-REACT-2](react.md#dh-adr-react-2) specifies. The first frame after a connect is the whole partial message. A frame is never queued as a difference against shared state, because queued `live` frames are coalesced |
| `interaction` | A pending approval or question | none | Replaces the interaction region by id; the connection starts with a full baseline |
| `shell` | Session list, status, counters | none | Replaces by id; baseline first |
| `reset` | Nothing | none | The script re-fetches the transcript region with an ordinary request |
| `park` | Nothing | none | The host asks this tab to release its stream ([DH-ADR-GOUISTACK-5](#dh-adr-gouistack-5)) |
| `attention` | A session id, its title and what happened (an approval or question is waiting, a turn finished) | none | Not a fragment and never swapped. Sent only on the one attention stream per browser, for notifications ([DH-ADR-GOUISTACK-9](#dh-adr-gouistack-9)) |

The names except `park` and `attention` are those of
[DH-ADR-WIRE-2](../upstream/wire-and-api.md#dh-adr-wire-2). Three facts found while
verifying change how they are delivered.

**Finding 1: the htmx SSE extension is not usable as it stands.** Its npm manifest has
no licence and its repository shows none, so it cannot be vendored. Its source
(`sse.js` of 2.2.4) also shows a functional gap: while the browser reconnects by itself
the extension does nothing, but when the `EventSource` reaches the closed state it creates
a **new** `EventSource` from the URL alone. A new object has no last event id, so the
host would see a reconnect with no cursor. Inference from the HTML standard, not from a
run: a browser closes an `EventSource` for good when a connection attempt gets an answer
other than 200 with the event-stream content type, which is what a restarting host or a
proxy in front of it may give (open question 2).

**Finding 2: htmx already exposes what the extension uses.** `htmx.swap(target, content,
swapSpec)` is a documented public method, and in 2.0.11 it processes out-of-band elements
in the content it is given (`findAndSwapOobElements` in `dist/htmx.js`).

**Finding 3: the browser limit is per browser and origin, not per tab.** Over HTTP/1.1 a
browser opens at most six connections to one origin across **all** tabs, and an open
event stream holds one of them for as long as it lives (MDN, "EventSource"; marked "won't
fix" in Chrome and Firefox). Over HTTP/2 the limit is the negotiated stream count, 100 by
default, but browsers speak HTTP/2 only over TLS (general browser behaviour, not
re-checked here), and the default listener is plain HTTP on loopback. So with one stream per tab, the sixth tab leaves **no** connection for any
click in any tab. [DH-ADR-WIRE-2](../upstream/wire-and-api.md#dh-adr-wire-2) asked this
document to confirm its inference; the confirmed fact is stricter than the inference.

<a id="dh-adr-gouistack-4"></a>
#### DH-ADR-GOUISTACK-4 Live updates are named stream events carrying HTML fragments; a first-party stream script owns the cursor

- **Decision.** The page does not use the htmx SSE extension. A first-party script
  (`stream.js`, a few dozen lines, part of the script described in section 6) does four
  things and nothing else:
  1. Opens one `EventSource` for the tab. The URL carries `after=<cursor>`, where the
     cursor is the id of the last durable event the page holds (rendered into the page by
     the server, then advanced by the script).
  2. For each event, calls `htmx.swap` with the fragment. Destinations come from
     `hx-swap-oob` in the fragment, so one event can update several regions.
  3. Drops a `session` event whose `seq` is not above the cursor. This makes a replayed
     event harmless.
  4. If the `EventSource` closes for good, opens a new one with the current cursor after
     a bounded, growing delay.
- **Host rule.** The cursor is `Last-Event-ID` when the header is present (the browser's
  own reconnect) and the `after` parameter otherwise (a stream the script created). The
  rest is [DH-ADR-WIRE-3](../upstream/wire-and-api.md#dh-adr-wire-3) unchanged: replay
  within the limit, otherwise `reset`.
- **Ordering.** One goroutine writes each connection, so events arrive in the order they
  were written. Durable events are written in commit order. When the durable message
  event for an answer is committed, any pending `live` frame for that message is
  discarded first, so a stale partial can never be drawn after the final message.
- **Backpressure.** Each connection has a bounded queue and a write deadline set through
  `http.ResponseController`. `live` frames are coalesced: only the newest waits. If
  durable events overflow the queue, or a write misses its deadline, the host closes the
  connection and the page resumes from its cursor.
- **Why.** It removes an unlicensed dependency, and it makes resume correct in the one
  case the browser does not cover. This answers open question 4 of
  [../upstream/wire-and-api.md](../upstream/wire-and-api.md): `Last-Event-ID` alone is
  not enough.
- **Tradeoff.** The project owns about one screen of JavaScript that must be tested in a
  real browser. Each `live` frame replaces the unfinished tail of the message, so a text
  selection inside that tail is lost on every frame; finished blocks are appended once
  and stay. The frame rate is bounded by coalescing and must be measured (open
  question 6).
- **Serves.** [DH-O-12](../design.md#dh-o-12); `R6`, `R7`.
- **Verified by.** `DH-T-GOUISTACK-5`, `DH-T-GOUISTACK-6`; real browser: `DH-E-GOUISTACK-1`.

<a id="dh-adr-gouistack-5"></a>
#### DH-ADR-GOUISTACK-5 A browser holds a bounded number of event streams: hidden tabs park

- **Decision.** (a) The stream script closes its `EventSource` when its tab has been
  hidden for a grace period and reopens it, with its cursor, when the tab is shown again.
  (b) The host counts open streams per browser session. When a new stream would exceed the
  limit, the host sends `park` to the session stream that has been open longest and
  closes it; that page shows "paused, select to resume". The one attention stream of
  [DH-ADR-GOUISTACK-9](#dh-adr-gouistack-9) is counted but never parked. The limit is configuration; Inference: four
  is the right default over plain HTTP, leaving two connections for requests, and the
  limit is lifted when the listener uses TLS and therefore HTTP/2. (c) The terminal
  WebSocket is outside this count; whether browsers count WebSockets against the same six
  is not verified (open question 4).
- **Why.** Without it, opening a sixth tab freezes every tab with no error anywhere:
  requests simply queue in the browser.
- **Tradeoff.** A parked tab is stale until shown: its title and counters do not move.
  Anything that must reach a person while no tab is visible (an approval is waiting, a
  turn finished) cannot come from a parked stream. With no desktop shell to raise a
  native notification, it comes from the one attention stream per browser described in
  [DH-ADR-GOUISTACK-9](#dh-adr-gouistack-9), which stays open while tabs are hidden and
  counts as one stream against the limit.
- **Serves.** [DH-O-12](../design.md#dh-o-12); `R8`.
- **Verified by.** `DH-T-GOUISTACK-7`; real browser: `DH-E-GOUISTACK-2`.

### 5.4 Islands

<a id="dh-adr-gouistack-6"></a>
#### DH-ADR-GOUISTACK-6 An island is a custom element in an ES module, loaded on demand, with no bundler

- **The rule for allowing one.** An island is allowed only when all three hold: (1) the
  behaviour needs code running in the browser between two requests (keystroke-rate
  input, pointer tracking, a canvas, a device such as the microphone); (2) HTML, CSS and
  htmx attributes cannot do it, including the built-in `<dialog>`, `<details>` and form
  controls; (3) a round trip per interaction would be wrong and not merely slower.
  Rendering data is never a reason: Markdown, diffs, tables and coloured terminal output
  are rendered by the host.
- **Packaging.** An island is a custom element (for example `<dsh-terminal>`) defined in
  one ES module. The browser runs its `connectedCallback` when htmx swaps the element in
  and its `disconnectedCallback` when a swap removes it, so the island's lifetime is the
  element's lifetime and cleanup needs no registry. Third-party code is vendored as the
  ES module file its package already publishes (xterm.js 6.0.0 has `lib/xterm.mjs`), under
  a versioned path, recorded in the same manifest as htmx. A library that publishes no
  browser-loadable file, or only React bindings, cannot be used without a bundler; that
  is a reason to choose another library or to keep the feature on the host.
- **Loading.** The page lists islands in a small server-rendered table (element name to
  module path). The first-party script imports a module the first time its element
  appears. Nothing is loaded for a page that shows no island, and a failed load is
  retried the next time the element appears.
- **Talking to the host.** Configuration comes in as attributes on the element, written
  by the server (addresses, limits, translated labels). Requests go out as ordinary
  same-origin `fetch` calls or one WebSocket, authenticated by the same cookie. An island
  tells the page something by dispatching a DOM event, which htmx can use as a trigger.
  An island never calls a Go function directly and never keeps the only copy of anything.
- **Declared policy.** Each island states the policy directives it needs beyond the
  base. An island that needs any is framed ([DH-ADR-GOUISTACK-3](#dh-adr-gouistack-3)).
- **Why.** The custom-element lifecycle is the one browser mechanism that follows what
  htmx does to the page. ES modules are loaded by every supported engine without a build.
- **Tradeoff.** No tree-shaking and no shared dependency graph: each island ships whole.
  The set must stay small.
- **Serves.** [DH-O-12](../design.md#dh-o-12); `R3`, `R9`.
- **Verified by.** `DH-T-GOUISTACK-8`; real browser: `DH-E-GOUISTACK-3`.

**Islands the product appears to need.** Inference from the upstream client packages
and their dependencies; the owning specification decides each one.

| Candidate | Upstream evidence | Island? | Specified by |
|---|---|---|---|
| Terminal | `packages/client/ui-sidebar-terminal` (xterm.js) | Yes, framed | [xtermxterm.md](xtermxterm.md) (browser side), [xterm.md](xterm.md) and [ws.md](ws.md) (host side); area C6 |
| PDF preview | `pdfjs-dist` in `ui-sidebar-documentpreview/package.json` | Yes. Its Worker is same-origin, which the base policy allows through `script-src`; anything more (for example WebAssembly decoders) must be declared, which frames it | [pdfjs-dist.md](pdfjs-dist.md); area C6 |
| Message input (mentions, commands, input methods) | `lexical` in `packages/client/ui-conversation/package.json` | **Yes.** The owner decided on 2026-11-18 that the composer is a rich-text island, so that mentions and attachments appear as chips inside the text | [lexical.md](lexical.md); area C3 |
| File drop and paste upload | `packages/client/file-upload`, `ui-file-drag` | Small | Area C3; a plain file input works without it |
| Dock layout: resizable and draggable panes | `packages/client/ui-dockkit` | Small, or CSS only | Area C2 |
| Voice input | `packages/experimental/client-ui-voice-input` | Yes (microphone) | Area X4 |
| Spreadsheet preview | `@fortune-sheet/*` | **No**: a host-rendered table | [fortune-sheet.md](fortune-sheet.md) |
| Markdown, mathematics, code highlighting, diffs, coloured output | `micromark`, `katex`, `shiki`, `diff`, `anser` | **No**: rendered and sanitized on the host | [micromark.md](micromark.md), [diff.md](diff.md) |
| Long transcripts | `@tanstack/react-virtual` in `ui-chat/package.json` | **No**: paging by `seq` | [tanstack-react-virtual.md](tanstack-react-virtual.md) |
| Client state | React stores | **No**: host state and four named places for view state | [react.md](react.md) |
| Preview of an HTML file | `packages/client/ui-sidebar-documentpreview/src/client/html/basic-document.ts` | **No**: a sandboxed `<iframe>` is a browser feature | [micromark.md](micromark.md) (`DH-ADR-DOMPURIFY-2`); area C6 |
| Separate script environments for client code | `packages/client/script-realm`, `script-realm-iframe` | **No**: they serve the client plugin runtime, which [DH-ADR-3](../design.md#dh-adr-3) does not carry | Area C1 |

### 5.5 Considered and not used: a desktop shell

The first version of this document chose a Go shell around the system webview (Wails v3)
for a desktop application. The owner decided on 2026-11-18 that there is no desktop
application, so no shell module is part of the stack and none is linked. What was
learned is kept here, because it is the evidence behind that decision's tradeoff and
the starting point if a shell is ever reconsidered. Every fact was read in the module
cache on 2026-11-18; nothing was compiled or run.

| Subject | Finding | Source |
|---|---|---|
| The module | `github.com/wailsapp/wails/v3` v3.1.0 (2026-11-01), MIT, `go 1.26.0`. v3.0.0 is declared stable for desktop; v2 is still released (v2.17.1) | `go list -m -json`; `LICENSE`, `go.mod`, `README.md`, `release_notes.md` |
| Rendering engines | Windows: WebView2 (Chromium), a runtime that must be installed. macOS: WKWebView. Linux: WebKitGTK 6.0 on GTK4. Three engines, each as old as the user's system | `release_notes.md:52` |
| Showing the host's UI | A window can load an absolute loopback address (`pkg/application/webview_window_options.go:96`, `internal/assetserver/assetserver.go:167`). The in-process handler **cannot stream on Windows**: "a streaming response cannot work on Windows today" (`internal/assetserver/webview/responsewriter_windows.go:33-43`). So a shell would have loaded the same loopback listener a browser loads | as named |
| cgo | None on Windows. Required on macOS and Linux by default; a `purego` build tag selects back ends the module calls "experimental and opt-in" (`pkg/application/PUREGO_MACOS.md`, `PUREGO_LINUX.md`). `CGO_ENABLED=0` without the tag fails by design (`internal/buildguard/guard.go`) | as named |
| What it offered | Single-instance lock, custom URL protocol events, native file and message dialogs, a tray, and an update package that swaps a binary under a pinned public key | `pkg/application/single_instance.go`, `dialog_manager.go:16-55`; `pkg/updater/doc.go` |
| What it did not offer | A second browser pane inside a window: no such API was found, only one webview per native window | search of `pkg/application` |
| Security notes | Bound Go methods are reachable from child frames, and on Linux a same-origin child frame can reach the native message handler (advisory GHSA-2p5f-2gx8-mh8m, partly fixed) | `release_notes.md`, "Known limitations" |
| The bare alternative | `github.com/webview/webview_go`: no tagged release, newest commit 2024-08-31, MIT, cgo on every platform including Windows (`webview.go:4-15`), no dialogs, single instance or deep links | `go list -m -json`; source read |

**What not using it avoids.** A second artifact beside `dsh`; cgo and native runners for
macOS and Linux, which the owner's rule that the core binary is pure Go ([DH-ADR-15](../design.md#dh-adr-15)) would
otherwise have confined to that artifact; installers and their signing; a Go 1.26 floor
and a large dependency graph; and testing on three webview engines whose versions the
project cannot choose. **What it costs** is listed feature by feature in
[electron.md](electron.md): no tray, no deep link, no native caption, no native folder
dialog.

The decision this section first carried, a shell that loads the loopback listener, is
retired with the shell; its number is not reused. Its one lasting finding stands: the
path from the UI to the host is the loopback HTTP listener and nothing else, which is
also what conflict CF-6 in [../design.md](../design.md) §8 records as closed.

### 5.6 Accessibility, languages and theming

<a id="dh-adr-gouistack-8"></a>
#### DH-ADR-GOUISTACK-8 Copy, language, time zone and theme are resolved on the host for each request

- **Languages.** Upstream requires Chinese and English for every key and ships eight
  locales (`packages/client/locale/src/locale-settings.ts:15`, `README.md:12`). In the Go
  design the message tables are Go data in the binary. A view asks its request envelope
  for a string, so a fragment is rendered in the language of the request or stream that
  asked for it. The language is the stored preference, else the best match for
  `Accept-Language`, else English; `golang.org/x/text/language` (v0.44.0, verified by
  [fuzzysort.md](fuzzysort.md)) is the candidate matcher. `<html lang>` is set by the
  server. Changing the language re-renders the page. A test replaces upstream's
  compile-time check that both baselines have every key (`DH-T-GOUISTACK-10`). An island
  has no message table: it receives its labels as attributes.
- **Time.** A server-rendered time needs the viewer's time zone, which HTTP does not
  send. The first-party script stores the browser's zone name in a cookie; until it
  exists the host's zone is used, which is the viewer's zone for a local installation.
- **Theme.** Design tokens are CSS custom properties in one stylesheet. The stored
  preference is rendered as an attribute on `<html>`; "follow the system" is a CSS media
  query. No script is involved, so there is no flash of the wrong theme. User overrides
  are a stylesheet route, because the policy forbids inline style. An island reads the
  computed tokens, as upstream's terminal does (`terminal.tsx:140-143`).
- **Accessibility, as far as the stack decides it.** Server-rendered pages give real
  links, buttons, forms and headings, which is an improvement the stack hands out for
  free. Three things it makes harder, each with a rule: (1) the streaming region is not
  announced frame by frame; a status line says the assistant is answering and the
  finished message is announced once; (2) a swap replaces elements, so anything that can
  hold focus has a stable `id` and the first-party script puts focus back on the element
  with that `id`, or where a fragment asks for it; an element that must survive a swap
  untouched (an unsent draft, a playing video) carries htmx's `hx-preserve`; (3) keyboard
  shortcuts are dispatched by the
  first-party script from a table the server renders, since `hx-on` is unavailable.
- **Where the preferences live, and what the script may do.** Language and appearance
  are host preferences stored per principal, so a second browser or device of the same
  user shows the same choice; nothing about them is kept in a per-browser cookie
  (area C1, [../areas/ui-core.md](../areas/ui-core.md)). The first-party script's jobs
  are a closed list that area C1 owns
  ([DH-ADR-UICORE-4](../areas/ui-core.md#dh-adr-uicore-4)); beside the jobs named in
  this document it holds `tick`, which keeps elapsed times current, and the "not live,
  reload" stop after the stream has failed to reconnect a fixed number of times. This
  document adds no job to that list.
- **Why.** The host already knows the preference and renders everything; a second copy
  of the copy in the browser is what [DH-ADR-3](../design.md#dh-adr-3) removes.
- **Tradeoff.** Missing translations are found by a test, not by a compiler. A language
  or theme change costs a page render.
- **Serves.** [DH-O-12](../design.md#dh-o-12); `R11`, `R12`.
- **Verified by.** `DH-T-GOUISTACK-10`, `DH-T-GOUISTACK-11`.

### 5.7 Reaching the UI without a desktop window

A desktop application gave the user three things besides the pages themselves: a way to
start it, a window that stays put, and a way to be told that something needs them while
they look elsewhere. With the browser as the only client, each needs an answer in the
web stack. The full list of desktop features and their answers is in
[electron.md](electron.md); this section specifies the three that the UI stack owns.

<a id="dh-adr-gouistack-9"></a>
#### DH-ADR-GOUISTACK-9 The browser is the only client: `dsh` opens it, an optional web app manifest gives it a window, and one attention stream feeds notifications

- **Decision.**
  1. **Start and reach.** Running `dsh` in its web profile starts the host, binds the
     loopback listener and, once it answers, hands the system browser one URL that
     carries a one-time launch token ([open.md](open.md);
     [DH-ADR-WIRE-10](../upstream/wire-and-api.md#dh-adr-wire-10)). The host exchanges the
     token for the session cookie (a random id whose hash the host stores; nothing is
     signed) and redirects, so the token does not stay in the address
     bar or the history. When opening a browser is disabled by a flag, or impossible (an
     SSH session, no display), `dsh` prints the URL instead (`DH-LIB-OPEN-R5`). Running
     `dsh` again while a host runs asks that host to open a new tab itself
     ([electron.md](electron.md), `DH-LIB-ELECTRON-R1`). The default listener port is
     fixed and configurable, not random. Inference: a browser ties the cookie, the
     notification permission and an installed web app to the origin, and the port is part
     of the origin, so a port that changed on every start would lose all three.
  2. **A window that stays put (recommended; Inference, not an owner decision).** The
     host serves a web app manifest (name, icons, `display: standalone`, a start address
     and a scope inside the UI) and every page links it. A browser that supports
     installing a site can then show the UI in a window of its own, with its own entry in
     the taskbar or dock. Nothing depends on it: the UI is the same in an ordinary tab.
     There is **no service worker**, so nothing is cached, nothing is served stale and
     there is no offline page; when the host is not running, the installed window shows
     the browser's own connection error. The installed window opens the start address
     without a launch token; it works while the session cookie is valid, and otherwise the
     host answers with a page that says to run `dsh`.
  3. **Being told.** After the user turns notifications on in settings (a click, which
     is the gesture browsers require before asking permission), the first-party script
     shows a browser notification for two events: an approval or question is waiting,
     and a turn has finished. It does so only when no tab that shows that session is
     visible. The events arrive on an **attention stream**: one `EventSource` per
     browser, not per tab, held by whichever tab holds a named lock in the browser's Web
     Locks interface; when that tab closes, another tab gets the lock and opens the
     stream. The attention stream stays open while its tab is hidden, carries only the
     session id, the session title and the kind of event, never transcript content, and
     counts as one stream against the limit of
     [DH-ADR-GOUISTACK-5](#dh-adr-gouistack-5). Clicking a notification brings that
     session's tab forward or opens it. The tab title also carries a marker while
     something is waiting. **Web Push is not used**: it needs a service worker and a
     push service operated by the browser vendor, which would send data off the machine
     against the owner's opt-in rule.
- **Why.** These are the three jobs of a window that pages alone do not do. Each uses a
  browser feature that needs no shell, no native code and no build step, so
  [DH-ADR-3](../design.md#dh-adr-3) holds as amended by the owner.
- **Tradeoff.** A notification can only come from an open browser: with every tab
  closed nobody is told, except by the line `dsh` prints in its terminal. The installed
  window exists only where the browser offers it. Inference (general browser behaviour,
  not checked here): Chromium-family browsers install a site from its manifest and
  Safari on macOS can add any page to the Dock, while Firefox on the desktop does not
  install sites. A fixed port can collide with another program; `dsh` then fails with a
  message naming the port instead of silently moving, unless the operator configured
  otherwise. Notification permission is the user's to give and the browser's to
  remember; the UI cannot grant it to itself.
- **Secure-context rule.** Inference from the web platform's secure-context rules, not
  from a run: notifications, the Web Locks interface and installation need a secure
  context, and browsers treat `http://127.0.0.1` and `http://localhost` as one. A UI
  reached over plain HTTP from another machine gets none of the three; it needs TLS on
  the listener.
- **Failure.** If the browser cannot be opened, the URL is printed and the host keeps
  running. If the manifest cannot be loaded, the page works as a tab. If permission is
  denied, the lock interface is missing or the attention stream fails, the script falls
  back to the tab-title marker and retries the stream with a growing delay; the pending
  approval itself is host state and is never lost.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14); `R10`,
  `R14`, `R15`.
- **Verified by.** `DH-T-GOUISTACK-9`, `DH-T-GOUISTACK-12`; real browsers:
  `DH-E-GOUISTACK-4`.

## 6. Design notes

**Where the code lives.** Inference: the family `internal/ui` of
[../design.md](../design.md) section 4.2 holds the view registry, the templates, the
sanitizer type, the asset manifest and the embedded files. The stream handlers,
including the attention stream, belong to the web host (`internal/host`). Starting the
host and opening the browser belong to the one `main` package, `cmd/dsh`. There is no
`internal/desktop` package and no second `main` package. Area specifications own the
final names.

**The view registry** (shape, not final API):

```go
// One per template. T is the only data the template may read.
type View[T any] struct{ /* template name */ }
func Register[T any](name string) View[T]

// Req is what every view needs and no view should fetch: language, time zone,
// theme, the principal's display name. Templates see struct{ R Req; D T }.
func (v View[T]) Render(w io.Writer, r Req, data T) error   // buffers, then writes (R13)
func (v View[T]) HTML(r Req, data T) (SafeHTML, error)      // for wrapping in a layout
```

A handler renders the fragment. If the request has no `HX-Request` header
([negotiator.md](negotiator.md), `DH-LIB-NEGOTIATOR-R3`) it wraps the result in the page
layout. The stream handler calls the same `Render`. `SafeHTML` has two constructors
only: the sanitizer, and the output of a registered view; a static check forbids any
other conversion to `template.HTML`.

**The first-party script.** One file, loaded by every page, with five jobs: the stream
(5.3), loading islands (5.4), focus and shortcut dispatch (5.6), the time-zone cookie,
and the attention stream with its notifications (5.7). It holds no feature logic and
renders nothing. Its size is capped by a test so
that it cannot grow into a client application by increments.

**The stream response.** `Content-Type: text/event-stream`, `Cache-Control: no-store`,
never compressed ([compression.md](compression.md), `DH-LIB-COMPRESSION-R3`). A fragment
with line breaks is written as one `data:` line per line. A comment line is the
heartbeat. Every write goes through `http.ResponseController` (deadline, then flush); the
server-wide write timeout must not apply to this route.

**Ownership and cleanup.**

| Resource | Owner | Released when |
|---|---|---|
| A stream connection's goroutine and queue | The web host's fiber | The client goes away, the queue overflows, a write deadline passes, the principal is revoked, or the fiber unloads. `http.Server` shutdown alone does not end it ([DH-ADR-WIRE-2](../upstream/wire-and-api.md#dh-adr-wire-2)) |
| The browser's `EventSource` | The page | The tab is hidden past the grace period, the host sends `park`, or the page unloads |
| An island's socket, observers and timers | The custom element | `disconnectedCallback`; a framed island also ends with its frame |
| The attention stream and its lock | The page that holds the lock | That tab closes or unloads; the browser then gives the lock to another tab of the UI, which opens its own stream |
| The listener and the host's runtime record | The web host's fiber | That fiber unloads. Stopping the host asks first when work would be interrupted ([electron.md](electron.md), `DH-LIB-ELECTRON-R3`) |
| The browser, its tabs, an installed web app window and the notification permission | The user and the browser. The host has access to none of them: it can ask the operating system to open a URL once, and nothing more | The user closes or removes them; the host never closes a tab |

**Failure.** A view error returns a 500 fragment and logs the template name; nothing
partial is written. A stream event that fails to render closes that connection, so the
page resumes and the error is seen once, not skipped. That rule is for a renderer that
returns an error because it could not read what it needs. A single transcript item that
cannot be drawn is not such an error: its renderer sends a "could not render this item"
fragment and the stream stays open, because a failure that repeats on every resume
would otherwise loop ([react.md](react.md) §6;
[../areas/ui-conversation.md](../areas/ui-conversation.md) §6.3). An island that fails to load shows
a server-rendered fallback inside its element and retries when next mounted. If the
system browser cannot be opened, `dsh` prints the URL and the host keeps running
([open.md](open.md), `DH-LIB-OPEN-R4`). If the host stops while a tab is open, the
stream script shows a "host not reachable" state from markup already in the page and
keeps trying to resume ([electron.md](electron.md), `DH-LIB-ELECTRON-R7`).

## 7. Risks and open questions

Only a real browser or a real build can settle items 1 to 5.

1. **`htmx.swap` with out-of-band content and no main target**, in each engine. Read in
   the 2.0.11 source; not run. `DH-E-GOUISTACK-1`.
2. **`Last-Event-ID` on the browser's own reconnect** in each supported browser engine
   (Chromium, Gecko, WebKit), and which failures close an `EventSource` for good.
   `DH-E-GOUISTACK-1`, `DH-E-GOUISTACK-4`.
3. **The launch and the three window substitutes** of
   [DH-ADR-GOUISTACK-9](#dh-adr-gouistack-9): that each supported browser keeps the
   cookie through the token redirect; treats the loopback origin as a secure context for
   notifications and the Web Locks interface; keeps the attention stream open in a
   hidden tab; and, where it installs sites, installs this one from its manifest with no
   service worker. Also open: how long a browser session lives, which decides whether an
   installed window still works the next day
   ([DH-ADR-SECURITY-7](../upstream/security.md#dh-adr-security-7)). `DH-E-GOUISTACK-4`.
4. **WebSockets and the six-connection pool**, and whether `connect-src 'self'` covers a
   same-origin `ws:` address in WebKit. `DH-E-GOUISTACK-2`, `-3`.
5. **xterm.js inside a frame**: focus, input methods, clipboard, resize.
   `DH-E-GOUISTACK-3`; detail in [xtermxterm.md](xtermxterm.md).
6. **Cost of `live` frames** for long answers (bytes and page work per frame), with the
   block-level scheme of [DH-ADR-REACT-2](react.md#dh-adr-react-2). To be measured against
   a budget ([DH-ADR-TOOLCHAIN-14](../upstream/toolchain.md#dh-adr-toolchain-14)). A
   morphing swap is **not adopted** in the first version: the htmx morph extension was
   not verified (version, licence or behaviour).
7. **Supported browsers.** The UI now runs in whatever browser the user has, not in an
   engine the project ships or selects. Decided by the owner
   ([DH-ADR-39](../design.md#dh-adr-39)): Chromium-family browsers are fully supported
   and run every scenario; current Firefox and Safari are supported with a smaller
   automated set, the engine-sensitive scenarios; the floor is releases since 2024, and
   an older browser sees a banner that names the minimum. Which web features the design
   system may use follows from that floor and is area C2's
   ([../areas/ui-kit.md](../areas/ui-kit.md), `DH-ADR-UIKIT-3`).
8. **The default port.** [DH-ADR-GOUISTACK-9](#dh-adr-gouistack-9) needs a fixed default
   port; its number, and what `dsh` does when another program holds it, belong to the
   web host and command-line areas (A18, A24).
9. **htmx 4.** Published under `next` during this work. Re-evaluate when it becomes
   `latest`; nothing here depends on an htmx extension, which keeps the move small.
10. **Third-party notices.** Vendored JavaScript is invisible to a tool that reads the Go
    module graph; the notices step must also read the asset manifest (gap in
    [../design.md](../design.md) section 8).
11. **The stream cap and replay numbers** are configuration without measured defaults
    (areas A18, A19).
12. **Own code this document implies**, for the owning areas to schedule: the view
    registry and sanitizer type (C1, C2), the first-party script (C1), the stream
    handlers including the attention stream (A19), the web app manifest and the
    notification setting (A25), the launch and host reuse (A24, A18).

### 7.1 Answers to questions other specifications addressed to this document

| Asked by | Question | Answer |
|---|---|---|
| [react.md](react.md) section 7 | Which template engine | `html/template` ([DH-ADR-GOUISTACK-1](#dh-adr-gouistack-1)) |
| [react.md](react.md) section 7 | Does an out-of-band swap ignore a target that is not on the page | Read in `dist/htmx.js` of 2.0.11: the fragment's element is removed, nothing else on the page changes, and the event `htmx:oobErrorNoTarget` is raised on `<body>`. So it is ignored, with an event the stream script must not treat as a failure. To be confirmed in a browser by `DH-E-GOUISTACK-1` |
| [react.md](react.md) section 7 | Is a morphing swap adopted | No, not in the first version (open question 6) |
| [micromark.md](micromark.md) section 7 | The exact policy, and whether htmx works with evaluation off | The policy of [DH-ADR-GOUISTACK-3](#dh-adr-gouistack-3). htmx's documentation lists what `allowEval: false` removes (`hx-on`, event filters, `js:` values); requests and swaps are not on that list. Confirmed in a browser by `DH-E-GOUISTACK-3` |
| [micromark.md](micromark.md) section 7 | The htmx mechanism that disables attribute processing in a region | `hx-disable`. Its documentation says it applies to the element and everything inside, and that injected content cannot override it |
| [micromark.md](micromark.md) section 7 | Inline `style` attributes | Not permitted in the main document. Table alignment uses classes; 24-bit colours in rendered tool output are mapped to the 256-colour palette as classes. The terminal island draws true colour itself |
| [playwright.md](playwright.md) section 7 | How WebKit engines are covered | There is no desktop window to drive any more, so its option (c) is gone. `DH-E-GOUISTACK-4` runs the stream cuts of `DH-E-GOUISTACK-1` in each supported browser, including Safari on macOS. Firefox and Safari are driven through each browser's own WebDriver program, spoken with `net/http` ([DH-ADR-PLAYWRIGHT-2](playwright.md#dh-adr-playwright-2)); until that back end exists the run is a written checklist recorded as `manual`, and the scenario stays open. No module is named here |
| [electron.md](electron.md) | Is a shell part of the stack | No, by the owner's decision. The facts read about one are in section 5.5. Its requirement that the UI never leaves the host origin and that old cookies are expired (`DH-LIB-ELECTRON-R6`) stands and is not weakened here |
| [electron-updater.md](electron-updater.md) | Does the stack carry an update engine | No. Update is the `dsh update` command and an optional notice; the UI stack only renders the notice |
| [lexical.md](lexical.md) | Rule for vendored browser files | This document adopts its rule `DH-ADR-LEXICAL-2` for every vendored asset: byte-identical to a published release, never patched |

### 7.2 What the stack cannot do, compared with the React client

For the owner. Each item is a consequence of [DH-ADR-3](../design.md#dh-adr-3) or of a
fact verified above.

1. **No code from a plugin runs in the page.** A plugin can contribute data the host
   knows how to render (a form from a schema, a card), or a sandboxed frame. It cannot
   add a component.
2. **No embedded browser pane and no embedded sign-in page.** Upstream's sidebar browser
   is an Electron `<webview>` guest with its own storage partition. The owner dropped
   both: a link or a sign-in opens in a new tab of the system browser
   ([electron.md](electron.md), `DH-ADR-ELECTRON-2`).
3. **Every interaction is a round trip.** Instant on loopback; felt over a remote link.
   Nothing is optimistic unless written by hand.
4. **State that lives only in the page is lost on a swap**: an unsent draft in a
   replaced region, scroll position, an open disclosure, a text selection in the
   unfinished tail of a streaming message. Each needs `hx-preserve`, a stable `id`, one
   of the four places named in [DH-ADR-REACT-1](react.md#dh-adr-react-1), or acceptance.
5. **No more than a few live tabs per browser** over plain HTTP; hidden tabs pause
   ([DH-ADR-GOUISTACK-5](#dh-adr-gouistack-5)).
6. **No list virtualisation.** Long transcripts are paged; behaviour at tens of
   thousands of nodes is unproven.
7. **No reuse of React-only libraries**, and no library that needs a bundler. Rich
   widgets are few, vendored whole and hand-wrapped.
8. **One rendering engine becomes whatever browser the user has.** Upstream ships one
   pinned Chromium; the Go product is tested on a list of supported browsers (open
   question 7).
9. **Template mistakes surface in tests, not at compile time**, unlike typed React
   properties ([DH-ADR-GOUISTACK-1](#dh-adr-gouistack-1)).
10. **No running without the host**: no in-browser preview runtime ([DH-ADR-17](../design.md#dh-adr-17)) and no offline
    page.
11. **No desktop application.** No tray icon, deep link, native menu, native folder
    dialog or window that the product owns. A tab that is open when the host stops shows
    "host not reachable"; the user starts `dsh` again
    ([DH-ADR-GOUISTACK-9](#dh-adr-gouistack-9); [electron.md](electron.md)).
12. **No typed browser API for third parties** ([DH-ADR-WIRE-1](../upstream/wire-and-api.md#dh-adr-wire-1)).
13. **Remote images in answers load only after a click** ([DH-ADR-GOUISTACK-3](#dh-adr-gouistack-3)).
14. **Nobody is told while no tab is open.** Notifications come from an open browser;
    there is no push service ([DH-ADR-GOUISTACK-9](#dh-adr-gouistack-9)).

## 8. Verification

Local tests need no browser. HTML is checked with `net/http/httptest` and a parser
(`DH-LIB-DOMTEST` in [build-test-toolchain.md](build-test-toolchain.md)). Nothing has
been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-GOUISTACK-1` | local | `R1`, `R13` | Every registered view is executed with a fixture and compared with a golden file; the page, htmx and stream paths give identical bytes for one fragment; a view made to fail writes zero bytes and the handler answers 500 |
| `DH-T-GOUISTACK-2` | local | `R2` | A table of hostile strings is passed through every view in text, attribute and URL positions; the parsed output contains no new element, attribute, handler, `hx-*` attribute or `javascript:` address. A static check finds no conversion to `template.HTML`, `template.JS` or `template.URL` outside the sanitizer and the registry |
| `DH-T-GOUISTACK-3` | local | `R3`, `R5` | Each embedded asset's SHA-256 equals its manifest entry, which names version, source and licence; no template names another origin; every listed asset is served by the binary |
| `DH-T-GOUISTACK-4` | local | `R4` | Every registered HTML route answers with exactly the base policy (plus the one import-map hash source when a module is registered, and nothing else), or its declared frame policy; no template contains an inline script other than that one import map, an `on...` or `hx-on` attribute, a `style` attribute or a `<style>` element; the `htmx-config` element has the locked values |
| `DH-T-GOUISTACK-5` | local | `R6` | The stream writer frames a multi-line fragment, an id, each event name and the heartbeat correctly and flushes per event; the handler returns when its owner's context ends and when the client goes away; a pending `live` frame is discarded when its durable event is written |
| `DH-T-GOUISTACK-6` | local | `R7` | Cursor table: header only, `after` only, both (the header wins), another session's id, a gap beyond the replay limit (`reset`); events committed during catch-up arrive once and in order |
| `DH-T-GOUISTACK-7` | local | `R8` | A reader that stops reading is closed at the queue limit or write deadline and the producer never blocks; the stream that exceeds the per-cookie cap causes `park` on the longest-open one |
| `DH-T-GOUISTACK-8` | local | `R9` | Every island in the table has a module file in the manifest and a declared policy; an island that declares extra directives is served only inside a frame route |
| `DH-T-GOUISTACK-9` | local | `R10` | With a fake browser launcher: starting the web profile hands it exactly one loopback URL with a one-time token, after the listener answers; with opening disabled the URL is printed and the launcher is not called; the token is refused on second use and the redirect target carries none. The import graph of the `dsh` binary contains no webview package and the binary builds with cgo off |
| `DH-T-GOUISTACK-10` | local | `R11` | Chinese and English have every message key; language choice table (stored, header, fallback); a time renders in the cookie's zone |
| `DH-T-GOUISTACK-11` | local | `R12` | In rendered pages: the streaming region and status line carry the agreed live-region attributes; every form control has an accessible name; every focusable element inside a swap target has an `id` |
| `DH-T-GOUISTACK-12` | local | `R14`, `R15` | Every page links the manifest; the manifest route answers with the fields of [DH-ADR-GOUISTACK-9](#dh-adr-gouistack-9) and icons the binary serves; no page or script registers a service worker. With a Go client on the attention stream: a waiting approval and a finished turn each produce one `attention` event that holds a session id, a title and a kind and no transcript content; the stream counts against the per-cookie cap and is never the one parked; opening a second attention stream for the same cookie closes the first |

Real-environment scenarios. Each is skipped, and therefore open, where its environment
is missing ([DH-ADR-TOOLCHAIN-6](../upstream/toolchain.md#dh-adr-toolchain-6)). The
driver is the one chosen in [playwright.md](playwright.md).

<a id="dh-e-gouistack-1"></a>
#### DH-E-GOUISTACK-1 A real browser resumes the stream in all three ways

- **Proves.** `DH-LIB-GOUISTACK-R7`, `R6`.
- **Given** the built binary with the replay model provider, a temporary harness home, a
  real Chromium, and a session whose turn is producing a long answer.
- **When** (a) the connection is cut and restored; (b) the host is made to answer 503
  to one reconnect, so the browser closes the `EventSource` and the script must create a
  new one; (c) the host process is stopped and started again.
- **Then** in each case the transcript holds every `seq` once and in order, the request
  log shows `Last-Event-ID` in (a) and `after` in (b), and the partial message continues
  or is replaced by the final one. No `htmx:oobErrorNoTarget` is raised for a region
  that is on the page.
- **Cleanup.** Close the session, stop the host, delete the home. On failure keep the
  session export and the browser's network log.

<a id="dh-e-gouistack-2"></a>
#### DH-E-GOUISTACK-2 Seven tabs over plain HTTP stay usable

- **Proves.** `DH-LIB-GOUISTACK-R8`.
- **Given** the binary on its default plain-HTTP loopback listener, a real Chromium and
  one terminal open.
- **When** seven tabs are opened on running sessions, the first is brought to the front,
  and a button is pressed in it.
- **Then** the press is answered within the UI's response budget; no more streams are
  open than the cap; parked tabs say so and resume, with no gap, when shown; the
  terminal keeps echoing throughout.
- **Cleanup.** Close the tabs and sessions, stop the host, delete the home.

<a id="dh-e-gouistack-3"></a>
#### DH-E-GOUISTACK-3 The policy holds on real pages, including the terminal

- **Proves.** `DH-LIB-GOUISTACK-R2`, `R4`, `R9`; and the browser half of
  `DH-LIB-XTERMJS-R3`, `R5`, `R6` in [xtermxterm.md](xtermxterm.md).
- **Given** the binary, a real Chromium that records every `securitypolicyviolation`
  event, and a replayed session whose model and tool output contain script elements,
  handler attributes, `hx-get` and `hx-swap-oob` attributes, an element whose `id` equals
  a real region's, a style element and a remote image.
- **When** the session, settings and terminal views are opened; the terminal is typed
  into; a program in it asks for the cursor position; the theme is changed; the terminal
  element is removed.
- **Then** the hostile content is shown as text or inert markup: no request it asked
  for is made, no region it named changes, and the only violations recorded are the ones
  the hostile content caused. The terminal draws, fits its pane and accepts input inside
  its frame; the program receives one reply to its query; the theme change keeps the
  shell and the colours the program set; removing the element closes its WebSocket.
- **Cleanup.** As above.

<a id="dh-e-gouistack-4"></a>
#### DH-E-GOUISTACK-4 The `dsh` command opens the system browser on the same UI, in each supported browser on each operating system

- **Proves.** `DH-LIB-GOUISTACK-R10`, `R14`, `R15`; and the browser half of
  `DH-LIB-ELECTRON-R1` and `R2` in [electron.md](electron.md).
- **Given** the built `dsh` binary on Windows, macOS and Linux, with the replay provider
  and a temporary harness home; on each system the browsers on the supported list
  (assumed until open question 7 is settled: a Chromium-family browser and Firefox
  everywhere, Safari on macOS), each with a fresh profile and set in turn as the
  system's default browser.
- **When** `dsh` is run from a terminal; a turn is run; the tab is left for longer than
  every idle timeout in the path; the three stream cuts of
  [DH-E-GOUISTACK-1](#dh-e-gouistack-1) are repeated; a terminal is opened in the UI;
  notifications are turned on in settings, the tab is hidden and a turn that needs an
  approval is run; `dsh` is run a second time; in a browser that installs sites, the UI
  is installed and opened from its own window; finally `dsh` is run with the browser
  launch disabled while no host runs.
- **Then** the default browser opens one tab that shows the session after the token
  exchange, with no token left in the address or the history; the answer streams and
  the stream is still open after the wait; after each cut the transcript holds every
  `seq` once and in order; the terminal echoes; with the tab hidden a notification
  appears that names the session and the waiting approval and shows no transcript
  content, and selecting it brings the tab forward; the second run opens a tab on the
  same host, starts no second listener and exits; the installed window shows the same
  UI under the same cookie; the last run prints the URL and opens nothing. Scope: proves
  the tested build on the tested browser and system versions.
- **Cleanup.** Stop the host, confirm no process remains, remove the installed web app,
  delete the home and the browser profiles, restore the default browser. On failure keep
  the host log and the system, browser and browser-version names.

## 9. Units dispositioned by this spec

None directly. The ledger rows for `UP-DEP-@xterm/xterm`, `UP-DEP-@xterm/addon-fit`,
`UP-DEP-d3-time` and `UP-DEP-@vitejs/plugin-react` are explained in
[xtermxterm.md](xtermxterm.md), [d3-time.md](d3-time.md) and
[vitejsplugin-react.md](vitejsplugin-react.md). Go items declared here:
`DH-LIB-GOUISTACK`, `DH-LIB-GOUITEMPLATES`, `DH-LIB-GOUIHTMX`, `DH-LIB-GOUISTREAM`;
`DH-ADR-GOUISTACK-1` to `-6`, `-8` and `-9`; `DH-T-GOUISTACK-1` to `-12`;
`DH-E-GOUISTACK-1` to `-4`. The library item for a desktop shell and the seventh
decision, which chose one, were retired on 2026-11-18 with the desktop application.

| Library item | Choice | What it is |
|---|---|---|
| `DH-LIB-GOUISTACK` | own | The conventions of this document plus the first-party script |
| `DH-LIB-GOUITEMPLATES` | standard library | `html/template` behind the view registry |
| `DH-LIB-GOUIHTMX` | vendored asset | `htmx.org` 2.0.11, 0BSD, one file |
| `DH-LIB-GOUISTREAM` | own | The stream script and stream handlers on the browser's `EventSource`, including the attention stream; replaces `htmx-ext-sse`, which has no verifiable licence |
