# @puppeteer/browsers: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". This is a short-form specification; the browser provider it
> serves is specified in [browserbasehqstagehand.md](browserbasehqstagehand.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-PUPPETEERBROWSERS` |
| **Upstream unit** | `UP-DEP-@puppeteer/browsers` |
| **Upstream version** | 3.2.2, needs Node 22.12 or later (`pnpm-lock.yaml:23043`) |
| **Kind** | runtime |
| **Used by** | `experimental` (`packages/experimental/browser-use-stagehand-native`) |
| **Go choice** | own code on the standard library: find an installed browser, start it through the harness spawn helper. No Go module. |
| **Related areas** | X4, browser-use providers ([../areas/x-cua-browser-voice.md](../areas/x-cua-browser-voice.md)); A09, subprocesses and the spawn helper ([../areas/execution.md](../areas/execution.md)) |

## 1. What it is and why upstream needs it

`@puppeteer/browsers` is the Puppeteer project's tool for **obtaining and starting a
browser**. It can download a chosen Chrome build, work out where an installed Chrome
lives on each operating system, and start a browser process and read its output.

Upstream uses only the last two abilities. **It never downloads a browser.** The
operator must install Chrome or Chromium (`README.md:47` of
`browser-use-stagehand-native`), and the library's download function is not imported
anywhere (text search for `@puppeteer/browsers` over the checkout: one import, at
`src/launch.ts:6`).

The reason the library is there at all is ownership. Stagehand can start Chromium
itself, but then the browser inherits the host's whole environment and can outlive a
start-up that timed out. So the host starts Chromium first and owns the process and its
profile before any other code may wait or fail
(`.agents/notes/implemented/architecture/2026-09-12-browser-use-provider-registration.md:21`).

## 2. How upstream uses it

All in `packages/experimental/browser-use-stagehand-native/src/launch.ts`.

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `:26` | Locate the installed stable Chrome when no `executablePath` is configured | `computeSystemExecutablePath({browser: CHROME, channel: STABLE})` |
| `:28` | A new temporary profile directory per browser | `mkdtemp` (Node, not this library) |
| `:40` to `:49` | Start the browser with a debugging port chosen by the system, a private profile, optional new headless mode, a scrubbed environment, and no signal handlers of the library's own | `launch({executablePath, args, env, handleSIGINT: false, ...})` |
| `:54` | Wait, with the operation deadline, for the line on which Chromium prints its DevTools address | `waitForLineOutput(CDP_WEBSOCKET_ENDPOINT_REGEX, timeout)` |
| `:32` to `:36` | Kill the process, wait for it to close, remove the profile | `browser.kill()`, `nodeProcess` |

No patch or pin comment exists for this package (`patches/` and `pnpm-workspace.yaml`
searched for `puppeteer`).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-PUPPETEERBROWSERS-R1` | The browser program is resolved in this order: the provider's configured path; otherwise the well-known install locations of stable Chrome for the operating system. If none exists the provider reports itself unavailable and names what to install. Nothing is downloaded. | `src/launch.ts:26`; DH-ADR-PLAYWRIGHTMCP-1 |
| `DH-LIB-PUPPETEERBROWSERS-R2` | The browser is started by the harness spawn helper: scrubbed environment, a fresh profile directory with owner-only permissions, optional headless mode, first-run and default-browser prompts disabled, and the transport flags of DH-ADR-STAGEHAND-3. | `src/launch.ts:40` to `:49` |
| `DH-LIB-PUPPETEERBROWSERS-R3` | Start-up has a deadline and can be cancelled. On timeout, cancellation or a failed spawn, the process tree is stopped and awaited and the profile is removed before the caller sees the error. | `src/launch.ts:32`, `:57` |
| `DH-LIB-PUPPETEERBROWSERS-R4` | The session's fiber owns the process and the profile. Close stops the tree, waits for exit, then removes the profile; it is safe to call twice. | `src/launch.ts:32` to `:36` |

## 5. Go choice

**Decision.** Own code, about one small package beside the CDP provider: a table of
install locations per operating system, a call to the spawn helper, and the close
sequence. Verdict on upstream's "the host owns the browser process": **keep**. Verdict
on reading the DevTools address from the browser's output: **drop**, because the Go
provider talks over a pipe and there is no address to read
([DH-ADR-STAGEHAND-3](browserbasehqstagehand.md#dh-adr-stagehand-3)).

**Why.** What upstream takes from the library is a path lookup and a process start. The
harness already has one helper that must start every child process
(DH-ADR-SECURITY-6), and a library that starts the process itself would bypass it.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Own code on `os` and the spawn helper** (chosen) | One place builds every child environment. No dependency. The process is owned by a fiber like every other child. | The install-location table must be kept current. | Nothing to link. |
| chromedp's `ExecAllocator` | Already finds Chrome, Chromium and Edge and starts them. | Starts the process inside the library, outside the spawn helper, and the child inherits the parent environment, with configured variables added to it (`allocate.go:760` to `:766` in chromedp v0.24.0). | `github.com/chromedp/chromedp` v0.24.0, MIT; see [browserbasehqstagehand.md](browserbasehqstagehand.md). |
| rod's `launcher` | Finds or downloads a browser. | Downloads Chromium revision 1321438 from three hosts when none is found (`lib/launcher/browser.go:104`, `revision.go:6`). | `github.com/go-rod/rod` v0.116.2, 2024-07-12, MIT. |
| Download a "Chrome for Testing" build on demand | Works with no browser installed. | Runs code fetched at session start, fails offline, and adds an updater to maintain. | Not examined. |

Module facts were checked on 2026-11-18 with `go list -m -json` and `go mod download
-json`, and by reading the named files in the module cache.

**Tradeoff.** A machine with no Chromium-family browser has no browser use until the
operator installs one. Inference: widening R1's table to Chromium and Microsoft Edge
would make the provider work on a stock Windows installation, since Edge is built on
Chromium; upstream looks for stable Chrome only. Whether Edge behaves the same under
the pipe transport must be proven by the real-browser scenario before it is listed.

**Risks and open questions.** (1) An explicit `dsh` command that installs a pinned
browser into the harness home would remove the prerequisite without implicit downloads;
not designed here. (2) Chromium's command-line flags change between versions; the flag
list is a tested constant, and the scenario runs against the browser versions on the
runners.

## 8. Verification

Local tests. Nothing here has been run. The real-browser scenario is in
[browserbasehqstagehand.md](browserbasehqstagehand.md), section 8.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-PUPPETEERBROWSERS-1` | local | `DH-LIB-PUPPETEERBROWSERS-R1`, `DH-LIB-PUPPETEERBROWSERS-R2` | With a fake file system per operating system: the configured path wins; the first existing well-known path is chosen; none gives "unavailable" naming Chrome, and no process starts and no network call is made. The argument list and environment given to a fake spawn helper equal a golden list. |
| `DH-T-PUPPETEERBROWSERS-2` | local | `DH-LIB-PUPPETEERBROWSERS-R3`, `DH-LIB-PUPPETEERBROWSERS-R4` | With a fixture program that never answers: the deadline fires, the fixture's process tree is gone and the profile directory is removed before the error returns. Cancelling during start-up does the same. A second call to close does nothing and returns the first call's result. |

## 9. Units dispositioned by this spec

| Unit | Disposition |
|---|---|
| `UP-DEP-@puppeteer/browsers` | replaced by `DH-LIB-PUPPETEERBROWSERS` |
