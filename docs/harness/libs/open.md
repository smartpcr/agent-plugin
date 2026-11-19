# open: Go specification

> **Status: design, not built.** Short form with the debt table. Upstream facts were read
> at the pinned commit recorded in [../README.md](../README.md). Statements that are
> reasoning and not read from source open with "Inference". Go module facts were checked
> on 2026-11-18.

| | |
|---|---|
| **Library ID** | `DH-LIB-OPEN` |
| **Upstream unit** | `UP-DEP-open` |
| **Upstream version** | 11.0.4 |
| **Kind** | runtime, two packages |
| **Used by** | bundle (`packages/bundle/web-app`), experimental (`packages/experimental/inspector`) |
| **Go choice** | own code on the standard library `os/exec` |
| **Related areas** | A18, which owns the launch and calls the hand-off ([../areas/host-telemetry.md](../areas/host-telemetry.md), `DH-D-HOST-3`); A09, whose host process starter runs the launcher ([../areas/execution.md](../areas/execution.md)); A17, which opens sign-in pages the same way ([../areas/interaction-credentials.md](../areas/interaction-credentials.md)); A24, which supplies `--no-open` |

## 1. What it is and why upstream needs it

`open` is a Node library that asks the operating system to open a URL or file with the
default application, hiding the differences between Windows, macOS, Linux and Windows
Subsystem for Linux.

Upstream uses it so that `dsh --profile web` ends with the browser showing the UI. The
URL it opens is the **entry URL**, which carries the launch token that the browser
exchanges for its login cookie (`packages/bundle/web-app/src/index.ts:243`). Two
consequences make this small feature security-relevant: the URL is a credential, and
the program that receives it (the browser, through an operating-system launcher) should
not also inherit the harness's environment, which holds API keys.

The experimental inspector uses the same library to open Chrome specifically, because
its DevTools page only works there.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/bundle/web-app/src/index.ts:85` to `:114` | A tiny program, run in a **separate Node process**, that imports the library and opens one URL | `open(url)` |
| `packages/bundle/web-app/src/index.ts:134` | That process is started with a scrubbed copy of the environment and its error output captured | `scrubbedParentEnv()` |
| `packages/bundle/web-app/src/index.ts:91` | On Windows, wait for the launcher's exit code, because the library returns as soon as PowerShell starts | child `close` event |
| `packages/bundle/web-app/src/index.ts:146` | Cancel on shutdown (kill the launcher); report a non-zero exit as a failure | abort signal |
| `packages/bundle/web-app/src/index.ts:212` | Never open a browser when the harness was started through SSH; honour `openBrowser: false` (`--no-open`) | none |
| `packages/bundle/web-app/src/index.ts:245` | On failure print a warning with the URL replaced by `[login URL]` and tell the user to use the printed URL | none |
| `packages/experimental/inspector/src/host/plugin.ts:173` | Open the DevTools page in Chrome when `--inspect` is passed; log on failure | `open(url, { app: { name: apps.chrome }, wait: false })` |

## 3. Tech debt upstream carries

Search performed: imports of `open` (static, dynamic and `import.meta.resolve`);
`patches/` and `pnpm-workspace.yaml` (no patch, pin or exemption); the web-app source
and README.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L7.7` | To keep credentials out of the browser's environment, upstream starts a whole second Node runtime that evaluates a program held in a string, which loads the library, which starts PowerShell on Windows. The parent then has to know the library's internals (it resolves before the launcher has finished) and work around them per platform. | `packages/bundle/web-app/src/index.ts:87`, `:92`, `:134` | Resolved by `DH-LIB-OPEN`: the host starts the operating system's launcher directly with a scrubbed environment and waits for its exit code. One process, no intermediate runtime. |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-OPEN-R1` | After the web UI is ready, the host can hand one URL to the operating system's default browser on Windows, macOS and Linux. | `packages/bundle/web-app/src/index.ts:146` |
| `DH-LIB-OPEN-R2` | The launcher process, and therefore the browser if it has to be started, receives a scrubbed environment: no harness credentials and no `DSH_*` variables. | `packages/bundle/web-app/src/index.ts:133` |
| `DH-LIB-OPEN-R3` | The URL is passed as one argument to a program, never through a shell, so characters such as `&` or `%` in the token cannot be interpreted. | Inference: the entry URL contains a query string |
| `DH-LIB-OPEN-R4` | A failure (no launcher found, non-zero exit) never stops the host. It prints one warning that does not contain the URL and points to the printed entry URL. | `packages/bundle/web-app/src/index.ts:245` |
| `DH-LIB-OPEN-R5` | No browser is opened when the host was started through SSH, when the operator disabled it, or when there is no display to open it on. | `packages/bundle/web-app/src/index.ts:212` |
| `DH-LIB-OPEN-R6` | Shutting down while the launcher is still running ends it and waits for it. | `packages/bundle/web-app/src/index.ts:153` |

## 5. Go choice

**Decision.** Own code, about 60 lines, used by the launch component of area A18: pick
the launcher by operating system and run it through area A09's host process starter
(the one place in the harness that starts a process), with a scrubbed environment and
the caller's context. With no desktop application ([DH-ADR-18](../design.md#dh-adr-18))
this hand-off is the only way the harness shows a page: the launch URL, a sign-in page
and a link from the UI all open in the user's own browser.

| System | Launcher | Argument |
|---|---|---|
| Windows | `rundll32.exe url.dll,FileProtocolHandler` from the system directory | the URL |
| macOS | `/usr/bin/open` | the URL |
| Linux and other Unix | `xdg-open`, found on `PATH` | the URL |

**Why.** The two existing Go modules for this job were read and neither meets
`DH-LIB-OPEN-R2`: on Windows they call the `ShellExecute` API inside the harness
process, which has no environment parameter, so a browser started that way inherits the
harness's environment. They also offer no context for cancellation (`DH-LIB-OPEN-R6`).
Starting a named launcher as a child process with a context gives both, and the argument
vector gives `DH-LIB-OPEN-R3`.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** own code on the harness process starter (`os/exec` underneath) | Scrubbed environment; cancellable; exit code observed; no dependency | Three launchers to keep correct; Windows Subsystem for Linux needs a decision (below) | Go 1.27.4 standard library |
| `github.com/pkg/browser` | Small and well known | No tagged release (pseudo-version of 2024-01-02); Windows path is `windows.ShellExecute` in-process (`browser_windows.go`); no context | v0.0.0-20240102092130-5ac0b6a4141c, BSD 2-Clause; `go list -m -json`, `go mod download -json`, licence and platform files read in the module cache, 2026-11-18; requires `golang.org/x/sys` |
| `github.com/cli/browser` | Tagged fork of the above | Same design: `windows.ShellExecute` in-process, `OpenURL(url)` without a context; last release 2023-10-05 | v1.3.0, BSD 2-Clause; `go list -m -json`, licence, `go.mod`, `browser.go` and `browser_windows.go` read in the module cache, 2026-11-18 |

**Tradeoff.** Upstream's library knows more environments (Windows Subsystem for Linux,
choosing a specific browser by name). The Go code covers the three desktop systems; the
rest is listed below.

Design notes:

- The call is `Open(ctx, url) error`. The component that calls it (the launch component
  of area A18, or the sign-in flow of area A17) owns it as an effect on its fiber: unloading cancels `ctx`, which kills the launcher and waits
  (`DH-LIB-OPEN-R6`). Nothing else is created, so nothing else needs cleanup.
- A launcher that exits zero only means the operating system accepted the URL, not that
  a page loaded. The printed URL is the fallback in every case, as upstream.
- Only an `http` or `https` URL is handed over. Revealing a file in the file manager
  and listing the applications that can open a path are not built: the host refuses any
  file target ([../areas/host-telemetry.md](../areas/host-telemetry.md),
  `DH-ADR-HOST-9`).

Open questions for the owning areas: (1) under Windows Subsystem for Linux, `xdg-open`
is often absent; whether to call the Windows launcher through interop or just print the
URL. (2) Settled: the inspector's "open in Chrome" is not built, because the Go harness has
no DevTools endpoint
([DH-ADR-XINSPECT-1](../areas/x-inspector-timeline.md#dh-adr-xinspect-1)), so no code
finds a browser by name here.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-OPEN-1` | local | `DH-LIB-OPEN-R2`, `DH-LIB-OPEN-R3` | With the launcher replaced by a test program that records its arguments and environment: the URL (containing `&`, `%` and `=`) arrives as exactly one argument, and no credential or `DSH_*` variable set in the parent is present. |
| `DH-T-OPEN-2` | local | `DH-LIB-OPEN-R4`, `DH-LIB-OPEN-R5`, `DH-LIB-OPEN-R6` | A missing launcher and a launcher that exits 1 each produce one warning without the URL and no host failure; under an SSH launch environment or `openBrowser: false` no process is started; cancelling the context ends a launcher that hangs. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.
`DH-LIB-OPEN-R1` needs a real graphical session and belongs to area A18's launch scenario.
Its shape: **Given** a desktop session on each of the three systems with a default
browser set; **When** the operator starts the web profile; **Then** that browser shows
the UI, already signed in, and its process environment holds no harness credential;
**Cleanup:** stop the host and close the tab.

Units dispositioned by this spec: `UP-DEP-open`, `UP-DEBT-found.L7.7`.
