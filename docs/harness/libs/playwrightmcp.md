# @playwright/mcp: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-PLAYWRIGHTMCP` |
| **Upstream unit** | `UP-DEP-@playwright/mcp` |
| **Upstream version** | 0.0.80, which brings `playwright` 1.63.0-alpha-2026-08-31 (`pnpm-lock.yaml:30577`) |
| **Kind** | runtime, but not a library: a Node program that upstream starts as a child process |
| **Used by** | `experimental` (`packages/experimental/browser-use-playwright-mcp`) |
| **Go choice** | own code: a launch recipe for an external program that the operator installs. Not a Go dependency and not shipped in the binary. |
| **Related areas** | browser-use providers (`../areas/x-cua-browser-voice.md`); MCP client ([modelcontextprotocol.md](modelcontextprotocol.md)); sibling [chrome-devtools-mcp.md](chrome-devtools-mcp.md) |

## 1. What it is and why upstream needs it

`@playwright/mcp` is Microsoft's **MCP server for browser control**. MCP (Model Context
Protocol) is the protocol through which a separate program offers tools to an agent; see
[modelcontextprotocol.md](modelcontextprotocol.md). This server starts or attaches to a
Chromium browser and offers tools such as "navigate", "click" and "take a screenshot".

Upstream uses it as one of three **browser-use providers**: back ends that let the model
operate web pages (`docs/subsystems/browser-use.md:11`). Upstream writes no browser code
for this provider. It only starts the server and lets its own MCP client turn the
server's tools into harness tools named `mcp__playwright-mcp__<tool>`
(`packages/experimental/browser-use-playwright-mcp/README.md:84`).

It is an npm dependency only so that a fixed version is installed next to the harness
and can be found on disk. Nothing is imported from it.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/experimental/browser-use-playwright-mcp/src/index.ts:29` | Find the installed server's entry file | `import.meta.resolve('@playwright/mcp/package.json')`, then `cli.js` beside it |
| `src/index.ts:46` | Start it with the same Node program that runs the harness | `command: process.execPath` |
| `src/index.ts:35` to `:42` | Choose the browser mode | Launch: `--browser chromium --isolated`, plus `--headless` and `--executable-path` when configured. Attach: `--cdp-endpoint <url>` |
| `src/index.ts:32` | Stop inherited settings from overriding the configured mode | Every inherited `PLAYWRIGHT_MCP_*` environment variable is set to empty |
| `src/index.ts:43` | One server process per live session | `mountSessionMcp` from the shared browser runtime; `exclusive` is true in attach mode |

The server is started in the session's working directory, keeps its browser across turns,
and is shut down with the session. An attached browser is owned by someone else: cleanup
disconnects and leaves it running
(`packages/experimental/browser-use-playwright-mcp/README.md:38`, `:62`).

## 3. Tech debt upstream carries

No patch or pin comment exists for this package (`patches/` and `pnpm-workspace.yaml`
searched for `playwright`). The limits in the provider README are units owned by the
browser-use area. One library-level debt was found:

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L3.6` | A package numbered 0.0.x is pinned exactly, and it installs a second, alpha build of Playwright beside the 1.61.1 the workspace uses elsewhere. Its tool schemas reach the model unchanged and "carry no DSH stability promise". | `pnpm-lock.yaml:30577`, `:27058`, `:27063`; `packages/experimental/browser-use-playwright-mcp/README.md:105` | The server is no longer part of the harness's dependency graph. It is an external program with its own dependencies, and its version is checked when it connects (DH-ADR-PLAYWRIGHTMCP-1, `DH-LIB-PLAYWRIGHTMCP-R4`). |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-PLAYWRIGHTMCP-R1` | The provider builds the server's arguments from its config: launch mode gives `--browser chromium --isolated` with optional `--headless` and `--executable-path`; attach mode gives `--cdp-endpoint`. | `src/index.ts:35` |
| `DH-LIB-PLAYWRIGHTMCP-R2` | The child's environment is the harness's scrubbed environment with every inherited `PLAYWRIGHT_MCP_*` variable removed. | `src/index.ts:32` |
| `DH-LIB-PLAYWRIGHTMCP-R3` | One server process per live session, started in the session's working directory, owned by the session and stopped with it. Attach mode is exclusive to one session; teardown never closes a browser the harness did not start. | `README.md:38`, `:62` |
| `DH-LIB-PLAYWRIGHTMCP-R4` | The program is found before any session depends on it. If the Node runtime or the server is missing, the provider reports itself unavailable with a message that names what to install; nothing is downloaded implicitly. The connected server's reported version is recorded and compared with the tested version. | DH-ADR-PLAYWRIGHTMCP-1 |
| `DH-LIB-PLAYWRIGHTMCP-R5` | The server is reached only through the harness MCP client, so naming, deadlines, cancellation, image results and reconnect behave as for any MCP server. | `README.md:60` |

## 5. Go choice

**Decision.** The Go harness does not depend on `@playwright/mcp`. It keeps the
provider as a **launch recipe**: a small piece of harness code that knows the server's
arguments and environment rules and starts it through the generic MCP client. The
server and the Node runtime it needs are prerequisites on the operator's machine.

**Why.** The server is a Node program (`engines: node >=18`, `pnpm-lock.yaml:23006`).
A Go binary contains neither Node nor a `node_modules` folder, so the two things
upstream relies on, `process.execPath` and `import.meta.resolve`, do not exist.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **A. External program the operator installs** (chosen) | Same tools the model sees upstream; no browser code to write; exactly the plugin model of DH-ADR-4 | Browser use through this provider needs Node on the machine; the harness cannot guarantee the version | Nothing to link. Upstream pin 0.0.80 read from `pnpm-lock.yaml`. |
| B. A native Go provider that speaks the Chrome DevTools Protocol directly | No Node; one binary | A new tool catalogue to design, test and keep stable; none of Playwright's page-snapshot tools | Candidate module `github.com/chromedp/chromedp` exists: v0.24.0, 2026-11-18, MIT licence file, no `import "C"` (text search). Checked 2026-11-18 with `go list -m -json` and `go mod download -json`. Not chosen here: it belongs to the browser-automation library specification. |
| C. Ship Node and the package inside the release | Works out of the box | Adds a second runtime, its licences and its security updates to every release; contradicts the single pure-Go binary | Not examined further. |
| D. Drop the provider | Nothing to maintain | Loses a capability upstream ships; DH-ADR-1 requires a recorded reason | — |

**Tradeoff.** Out of the box, a Go installation has no browser tools from this provider
until the operator installs Node and the server. Upstream users never notice this,
because upstream itself runs on Node.

<a id="dh-adr-playwrightmcp-1"></a>
### DH-ADR-PLAYWRIGHTMCP-1 Browser MCP servers are operator-installed programs, not shipped dependencies

- **Decision.** `@playwright/mcp` and `chrome-devtools-mcp` are external MCP servers.
  The harness ships a recipe for each and resolves the program in this order: an
  explicit `command` and `args` in the provider's config; otherwise a `node` program on
  `PATH` together with a server entry file in a directory the operator configured. The
  harness never runs `npx` or any other downloader on its own. An operator may still
  configure `npx` as the explicit command. Verdict on upstream's "install it with the
  harness": **change**.
- **Why.** DH-ADR-4 already places tool-only plugins in separate processes that speak
  MCP, in any language. Downloading and executing a package at session start would run
  code the operator never approved, would make session creation depend on a network
  fetch, and would fail offline.
- **Tradeoff.** Upstream's exact pin becomes a check instead of a guarantee: the
  provider records the version the server reports and warns when it differs from the
  version the recipe was tested with. Inference: the server reports its package version
  in the MCP handshake; this must be confirmed against the real server.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-11](../design.md#dh-o-11); consistent with DH-ADR-1 and DH-ADR-4.
- **Verified by.** `DH-T-PLAYWRIGHTMCP-2`, `DH-T-CHROMEDEVTOOLSMCP-1`, and the
  real-browser scenario the owning area declares.

## 6. Design notes

A recipe is data plus one function: the provider name (also the tool namespace), a
function from provider config to arguments, the environment variables to remove, and
whether attachment is exclusive. The browser-use area owns the recipe type and the
per-session mounting; the MCP package owns the connection.

| Resource | Owner (releases it) | Who may use it |
|---|---|---|
| Server process, and the browser it launched | The session's fiber, through the harness process service | Only that session's agents |
| An attached, pre-existing browser | Its external owner; the harness only disconnects | One session at a time within one provider |

Failure and cleanup: if the program cannot be found, activation fails before any process
starts. If the server starts but the handshake fails, the process service stops the
process tree and the session is told why. Cancelling a tool call does not undo a click
or a navigation already delivered to the browser
(`packages/experimental/browser-use-playwright-mcp/README.md:104`); the Go design cannot
change that and must say so in the tool's failure text.

## 7. Risks and open questions

1. **A Node-free default.** Whether the Go product should offer a native browser
   provider (option B) so that browser use works without Node is a product question
   for the browser-use area and the browser-automation library specification.
2. **Where an operator-installed server lives.** A harness-managed tools directory with
   an explicit install command would make option A easier to use. It is not designed
   here; the owning area decides.
3. **Session creation on failure.** Upstream rejects session creation when the server
   cannot start (`README.md:101`). Whether Go keeps that or continues without browser
   tools is the owning area's verdict.

## 8. Verification

Local tests. Nothing here has been run. A scenario against a real Chromium is a
different kind of evidence and belongs to the owning area.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-PLAYWRIGHTMCP-1` | local | `DH-LIB-PLAYWRIGHTMCP-R1`, `DH-LIB-PLAYWRIGHTMCP-R2` | Table test of the recipe: each config gives the exact argument list, and an environment containing `PLAYWRIGHT_MCP_BROWSER` and a secret-shaped name gives a child environment with neither. |
| `DH-T-PLAYWRIGHTMCP-2` | local | `DH-LIB-PLAYWRIGHTMCP-R3`, `DH-LIB-PLAYWRIGHTMCP-R4`, `DH-LIB-PLAYWRIGHTMCP-R5` | With no `node` on `PATH`, the provider reports unavailable, names the missing program, and starts no process. With a Go fixture MCP server as the configured command, two sessions get two processes in their own working directories, the reported version is recorded, and ending a session ends only its process. |

## 9. Units dispositioned by this spec

| Unit | Disposition |
|---|---|
| `UP-DEP-@playwright/mcp` | replaced by `DH-LIB-PLAYWRIGHTMCP` under DH-ADR-PLAYWRIGHTMCP-1 |
| `UP-DEBT-found.L3.6` | resolved by DH-ADR-PLAYWRIGHTMCP-1 |
