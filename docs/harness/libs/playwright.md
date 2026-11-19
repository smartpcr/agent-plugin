# playwright: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Nothing here was built, run or tested.

| | |
|---|---|
| **Library ID** | `DH-LIB-PLAYWRIGHT` |
| **Upstream unit** | `UP-DEP-playwright` |
| **Upstream version** | declared `^1.49.0` in three manifests, resolved to 1.61.1 (`apps/web/package.json:62`; `pnpm-lock.yaml:27058`) |
| **Kind** | test-only |
| **Used by** | `apps` (`apps/web` tests and stress tests), `benchmarks`, `experimental` (`packages/experimental/inspector` tests) |
| **Go choice** | named Go module `github.com/chromedp/chromedp`, used from test code only |
| **Related areas** | toolchain ([../upstream/toolchain.md](../upstream/toolchain.md), open question 3); the scenario scaffold `test/scenario/web` of area A25 ([../areas/app-web.md](../areas/app-web.md)); the UI stack ([go-ui-stack.md](go-ui-stack.md)); the runtime use of the same module by the `cdp` browser provider ([../areas/x-cua-browser-voice.md](../areas/x-cua-browser-voice.md), [browserbasehqstagehand.md](browserbasehqstagehand.md)) |

## 1. What it is and why upstream needs it

Playwright is Microsoft's library for driving real browsers from test code. It starts a
browser it downloaded itself, opens pages, finds elements, sends input, intercepts
network traffic and takes screenshots.

Upstream's user interface is a React application that runs in the browser, so most of
what a user sees can only be checked in a browser. Playwright is the driver for that
whole evidence layer: the "web browser snapshot" lane is a required gate on every pull
request (`docs/testing.md:15`). It is never part of the shipped product.

A different Playwright, an alpha build pulled in by the Playwright MCP server, also sits
in the lock file. That one is covered by [playwrightmcp.md](playwrightmcp.md).

## 2. How upstream uses it

Counts are from a text search of the checkout on 2026-11-18.

| Use site | What it is used for | API used |
|---|---|---|
| `apps/web/tests/*.e2e.ts`, `*.snapshot.ts`, `*.perf.ts` and `apps/web/stress-tests` (247 files import it) | Start Chromium, open the web client served by a real host, act as a user and assert on what is rendered | `chromium.launch`, `Browser`, `Page`, `Locator` |
| 25 of those files | The same checks in WebKit, for behaviour that differs between engines; the model and reasoning picker is the named case (`docs/testing.md:15`) | `webkit.launch` |
| 28 files; 9 files | Replace or fail HTTP requests; intercept the WebSocket to simulate a lost connection (`apps/web/tests/connection-recovery.e2e.ts:4`) | `page.route`, `WebSocketRoute` |
| 27 files | Screenshots as evidence or for pixel comparison | `page.screenshot` |
| 13 files, and `benchmarks/long-session-browser/long-session.bench.ts:63` | Raw DevTools Protocol calls, for example browser performance counters | `CDPSession`, `Performance.getMetrics` |
| `packages/experimental/inspector/tests/client-browser.e2e.ts:7` | One browser test of the inspector client | `chromium` |
| `.github/workflows/ci.yml:519`, `:460`; `ci-master.yml:178` | Download Chromium and WebKit with their system packages on each runner and cache them by lock-file hash | `playwright install --with-deps chromium webkit` |

## 3. Tech debt upstream carries

No patch, pin or exemption exists for this package (`patches/` and
`pnpm-workspace.yaml` searched for `playwright`). No `UP-DEBT` unit is assigned. Two
costs are worth recording because the Go design removes their cause:

- The browser lane needs a browser download and operating-system packages on every
  runner, and a cache to make that bearable (`ci.yml:416`, `:460`).
- Browser code is invisible to upstream's coverage tool, one of the reasons for its
  coverage exemption lists (`../upstream/toolchain.md`, DH-ADR-TOOLCHAIN-7).

## 4. Requirements for the Go implementation

With the interface rendered on the server ([DH-ADR-3](../design.md#dh-adr-3)), most of
upstream's browser checks become comparisons of HTML produced by the host, with no
browser (`../upstream/toolchain.md`, open question 3). A browser is still needed for
what only a browser does: the JavaScript islands, live updates over server-sent events,
reconnection, focus and keyboard behaviour, and the authentication cookie.

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-PLAYWRIGHT-R1` | A real-browser scenario is Go test code that drives the UI of the built `dsh` binary in a real Chromium-family browser. The runner needs Go and a browser, and no Node. | DH-ADR-TOOLCHAIN-1; DH-ADR-5 |
| `DH-LIB-PLAYWRIGHT-R2` | A scenario can: open a URL; wait, with a deadline, for an element or text; click, type and press keys; read text, attributes and accessibility roles; set the viewport; fail or drop a network connection; and read the page's console errors. | Uses in section 2 |
| `DH-LIB-PLAYWRIGHT-R3` | Each scenario owns its browser process and profile directory, and both are gone when the test ends, including after a failure or a timeout. No scenario uses a fixed port. | `docs/testing.md` "How specs execute" |
| `DH-LIB-PLAYWRIGHT-R4` | When a scenario fails it leaves a screenshot, the page's HTML, the console log and the host's log in the run's artifact directory. | `page.screenshot` uses |
| `DH-LIB-PLAYWRIGHT-R5` | A runner without a browser does not pass: the scenario is reported as not run and stays open. | DH-ADR-TOOLCHAIN-6 |
| `DH-LIB-PLAYWRIGHT-R6` | The driver is imported only by `test/scenario` (behind its build tag) and by the browser provider package; no other harness package may import it. | DH-ADR-ARCH-6 import rule |
| `DH-LIB-PLAYWRIGHT-R7` | The user interface has one kind of client, the user's own browser ([DH-ADR-18](../design.md#dh-adr-18)), so a scenario that a UI area marks as engine-sensitive also runs in Firefox and in Safari. Those runs use the same scenario helper surface (open, wait-for, act, collect artifacts) through a second driver that adds no Go module and no Node. A runner without the browser or its driver program reports the scenario as not run, and it stays open. | DH-ADR-18; `webkit.launch` uses in section 2; [../areas/ui-kit.md](../areas/ui-kit.md) (`DH-E-UIKIT-5`) |

## 5. Go choice

**Decision.** The real-browser test driver is `github.com/chromedp/chromedp`, the same
module the browser-use provider uses, driving the Chrome or Edge already on the runner.

**Why.** It is the only candidate that needs no Node, and it adds no dependency the
product does not already have.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **chromedp** (chosen) | Pure Go; one module for tests and product; raw protocol access for performance counters and network failure. Inference: hosted CI runners usually carry Chrome already; the runner image must be checked. | Chromium family only. No built-in auto-waiting locators or pixel comparison: a small scenario helper supplies waiting. | `github.com/chromedp/chromedp` v0.24.0, 2026-11-18, MIT, no `import "C"` found. Details in [browserbasehqstagehand.md](browserbasehqstagehand.md). |
| Go binding to Playwright | Closest to upstream's tests; Chromium, Firefox and WebKit; auto-waiting; request and WebSocket interception. | Puts Node back on every runner: it executes `node cli.js` and downloads the driver and a Node binary (`run.go:99`, `:153`, `:232`), then downloads browsers (`run.go:316`). | `github.com/playwright-community/playwright-go` v0.6401.0, 2026-11-04, MIT, no `import "C"` found. The `go.mod` in the module cache declares `module github.com/mxschmitt/playwright-go`. |
| rod | Pure Go; friendlier waiting API. | Newest tag is from 2024-07-12; downloads a browser by default. A second CDP library beside the product's. | `github.com/go-rod/rod` v0.116.2, MIT. |
| WebDriver over plain HTTP | Standard protocol; works with Safari's built-in driver. | Needs a driver program per browser whose version must match the browser; weaker network control. | No module examined; the protocol can be spoken with `net/http`. |

Checked on 2026-11-18 with `go list -m -json <module>@latest`,
`go mod download -json`, and by reading `LICENSE`, `go.mod` and the named files in the
module cache.

<a id="dh-adr-playwright-1"></a>
### DH-ADR-PLAYWRIGHT-1 One real-browser test driver: chromedp against Chromium

- **Decision.** Real-browser scenarios are written in Go on chromedp, behind a small
  scenario helper (open, wait-for, act, collect artifacts). Verdict on upstream's
  Playwright lane: **change** the driver, and shrink the lane to what needs a browser.
  Verdict on upstream's WebKit runs: **kept, by a second driver**; see
  [DH-ADR-PLAYWRIGHT-2](#dh-adr-playwright-2).
- **Why.** DH-ADR-TOOLCHAIN-1 keeps real-environment scenarios as their own evidence
  layer run against the built binary. Requiring Node for that layer would bring back
  the installer, the driver download and the cache this redesign removes. Using the
  product's own CDP module means one thing to pin and review.
- **Tradeoff.** No WebKit or Firefox evidence from this driver
  ([DH-ADR-PLAYWRIGHT-2](#dh-adr-playwright-2) supplies it). Test authors lose
  Playwright's locators and write explicit waits; the helper must make a missing
  element fail with a useful message and never hang. Pixel comparison is not offered;
  assertions are on structure and text.
- **Serves.** [DH-O-15](../design.md#dh-o-15), [DH-O-12](../design.md#dh-o-12).
  **Verified by.** `DH-T-PLAYWRIGHT-1`, `DH-T-PLAYWRIGHT-2`, and the scenario in
  section 8.

<a id="dh-adr-playwright-2"></a>
### DH-ADR-PLAYWRIGHT-2 Firefox and Safari are driven through W3C WebDriver, spoken with `net/http`

- **Decision.** The scenario helper has a second back end for the engines chromedp
  cannot reach. It speaks the W3C WebDriver protocol (JSON over HTTP) with `net/http`
  to the driver program that belongs to the browser: `safaridriver`, which macOS ships
  with Safari, and `geckodriver` for Firefox, which the runner image supplies. The
  harness adds no Go module for it and downloads neither program. A scenario states
  which engines it must run in; the default is Chromium only.
- **Why.** There is no desktop application ([DH-ADR-18](../design.md#dh-adr-18)): the
  rendering engine is whatever browser the user has, so the UI areas ask for Firefox
  and Safari evidence ([../areas/ui-kit.md](../areas/ui-kit.md) `DH-E-UIKIT-5`,
  [../areas/ui-chat.md](../areas/ui-chat.md),
  [../areas/ui-conversation.md](../areas/ui-conversation.md)). The alternative that
  covers both engines with one API, the Go binding to Playwright, starts Node and
  downloads a driver and browsers (table above), which is what
  [DH-ADR-PLAYWRIGHT-1](#dh-adr-playwright-1) removes.
- **Tradeoff.** Two back ends behind one helper, and the WebDriver one is weaker: no
  network failure injection and no raw protocol access, so a scenario that needs those
  is Chromium only and says so. The driver program's version must match its browser;
  the run report records both versions. Safari's driver exists only on macOS, so
  WebKit evidence needs a macOS runner. Until the back end is built, an
  engine-sensitive scenario is a written checklist a person follows, recorded as
  `manual`, and it stays open as a scenario.
- **Serves.** [DH-O-15](../design.md#dh-o-15), [DH-O-12](../design.md#dh-o-12).
  **Verified by.** `DH-T-PLAYWRIGHT-3`, and the engine-sensitive scenarios of the UI
  areas. Which browsers the product supports is the owner's decision
  ([DH-ADR-39](../design.md#dh-adr-39)): Chromium-family browsers run every scenario
  through chromedp; current Firefox and Safari run the engine-sensitive scenarios
  through this back end; releases since 2024 are supported, and an older browser sees
  a banner that names the minimum.

## 6. Design notes

The scenario helper lives in `test/scenario`. It starts the browser through the same
launch code as the provider ([puppeteerbrowsers.md](puppeteerbrowsers.md)), so tests
exercise the product's launch path, with a profile under the test's temporary directory.
The browser path comes from an environment variable or the well-known locations.

| Resource | Owner (releases it) |
|---|---|
| The `dsh` process under test and its harness home | The test, through its cleanup |
| The browser process and its profile | The test, through its cleanup; a process-tree stop, then removal |
| Failure artifacts | The run's artifact directory; kept, never cleaned by the test |

Simulating a lost connection does not need request interception in the common case: the
test owns the host process, so it can stop and restart the real host, which is stronger
evidence than a simulated drop. The protocol's network-failure commands cover the rest.

## 7. Risks and open questions

1. **WebDriver without a module is unproven.** Inference: the protocol is small
   enough to speak with `net/http`, but no Go client was examined and nothing was run.
   If the hand-written client proves too costly, the fallback is the Playwright binding
   in a separate, test-only module used for Firefox and WebKit runs only; it puts Node
   on those runners and nowhere else.
2. **Which upstream checks still need a browser** is decided per page by the UI areas.
3. **Coverage of island JavaScript** is outside Go's coverage tool; whether it is
   measured at all is for the UI stack specification.
4. **Module path of the Playwright binding.** The declared path differs from the path
   it was fetched by; this matters only if the fallback in item 1 is taken.

## 8. Verification

Local tests. Nothing here has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-PLAYWRIGHT-1` | local | `DH-LIB-PLAYWRIGHT-R2`, `DH-LIB-PLAYWRIGHT-R4` | The scenario helper against a fake CDP peer: waiting for an element that never appears fails at its deadline with the selector in the message and writes the four artifacts; a click on a present element sends the expected input commands. |
| `DH-T-PLAYWRIGHT-3` | local | `DH-LIB-PLAYWRIGHT-R7` | The WebDriver back end against a fake driver served by `net/http/httptest`: open, wait-for and click send the documented requests; a wait that never succeeds fails at its deadline and writes the artifacts; a session is deleted and the driver program stopped after a failing test; with no driver program configured the run report lists the scenario as not run for that engine. |
| `DH-T-PLAYWRIGHT-2` | local | `DH-LIB-PLAYWRIGHT-R3`, `DH-LIB-PLAYWRIGHT-R5`, `DH-LIB-PLAYWRIGHT-R6` | With a fixture program as the browser: after a test that fails and one that times out, no fixture process and no profile directory remain. With no browser path, the run report lists the scenario as not run. An import-direction test fails when a package outside the two allowed ones imports chromedp. |

**Real-browser scenario handed to the UI areas** (a skipped run stays open).
**Given** a runner with Go, a stable Chrome or Edge and no Node on `PATH`; the built
`dsh` binary with the replay provider and a recorded session; a fresh harness home.
**When** the test starts `dsh`, opens the launch URL in the browser, sends a message,
waits for the answer, stops and restarts `dsh`, and waits again. **Then** the
transcript shows the recorded answer exactly once after the restart; the page logged no
console error; the terminal island accepted a keystroke. **Cleanup.** The test stops
`dsh` and the browser and removes the harness home and profile; on failure it keeps the
artifacts and the host log.

## 9. Units dispositioned by this spec

| Unit | Disposition |
|---|---|
| `UP-DEP-playwright` | replaced by `DH-LIB-PLAYWRIGHT` under DH-ADR-PLAYWRIGHT-1 |
