# chrome-devtools-mcp: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". This is a short-form specification; the shared reasoning is in
> [playwrightmcp.md](playwrightmcp.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-CHROMEDEVTOOLSMCP` |
| **Upstream unit** | `UP-DEP-chrome-devtools-mcp` |
| **Upstream version** | 1.9.0 (`pnpm-lock.yaml:24601`) |
| **Kind** | runtime, but not a library: a Node program that upstream starts as a child process |
| **Used by** | `experimental` (`packages/experimental/browser-use-chrome-devtools-mcp`) |
| **Go choice** | own code: a launch recipe for an external program that the operator installs. Not a Go dependency and not shipped in the binary. |
| **Related areas** | browser-use providers (`../areas/x-cua-browser-voice.md`); MCP client ([modelcontextprotocol.md](modelcontextprotocol.md)) |

## 1. What it is and why upstream needs it

`chrome-devtools-mcp` is the Chrome DevTools team's **MCP server for inspecting and
controlling Chromium**. It drives a browser through the Chrome DevTools Protocol (CDP,
the debugging protocol built into Chromium) and offers the result as MCP tools: open a
page, read the console, take a screenshot, record a performance trace.

Upstream uses it as a second browser-use provider beside Playwright MCP
(`docs/subsystems/browser-use.md:14`). As with Playwright MCP, upstream writes no browser
code: it starts the server and its MCP client exposes the tools as
`mcp__chrome-devtools-mcp__<tool>`
(`packages/experimental/browser-use-chrome-devtools-mcp/README.md:84`). Nothing is
imported from the package; it is a dependency only so that a fixed version is on disk.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/experimental/browser-use-chrome-devtools-mcp/src/index.ts:28` | Find the installed server's entry file | `import.meta.resolve('chrome-devtools-mcp/build/src/bin/chrome-devtools-mcp.js')` |
| `src/index.ts:39` | Start it with the harness's own Node program | `command: process.execPath` |
| `src/index.ts:29` | Turn off the server's usage statistics | `--no-usage-statistics`, always |
| `src/index.ts:30` to `:35` | Choose the browser mode | Attach: `--ws-endpoint <url>` for a `ws:` or `wss:` endpoint, otherwise `--browser-url <url>`. Launch: `--isolated --headless=<true or false>`, plus `--executable-path` when configured |
| `src/index.ts:36` | One server process per live session | `mountSessionMcp`; `exclusive` is true in attach mode |

No patch or pin comment exists for this package (`patches/` and `pnpm-workspace.yaml`
searched for `devtools`). The limits in the provider README are units owned by the
browser-use area.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-CHROMEDEVTOOLSMCP-R1` | The provider builds the server's arguments from its config exactly as the table in section 2 states, choosing `--ws-endpoint` or `--browser-url` from the endpoint's scheme. | `src/index.ts:30` |
| `DH-LIB-CHROMEDEVTOOLSMCP-R2` | Usage statistics are always disabled; no config can turn them on. | `src/index.ts:29`; `README.md:106` |
| `DH-LIB-CHROMEDEVTOOLSMCP-R3` | Process ownership, exclusive attachment, program discovery, version recording and use of the harness MCP client follow `DH-LIB-PLAYWRIGHTMCP-R3` to `DH-LIB-PLAYWRIGHTMCP-R5`. | [playwrightmcp.md](playwrightmcp.md) |

## 5. Go choice

**Decision.** Same as for Playwright MCP, under
[DH-ADR-PLAYWRIGHTMCP-1](playwrightmcp.md#dh-adr-playwrightmcp-1): the Go harness ships a
launch recipe; the server and Node are prerequisites the operator installs; the harness
never downloads the server by itself.

**Why.** The server needs Node (`engines: ^20.19.0 || ^22.12.0 || >=23`,
`pnpm-lock.yaml:24603`), which a Go binary does not contain.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **External program the operator installs** (chosen) | Same tool catalogue as upstream, including performance tracing; no browser code | Needs Node on the machine | Nothing to link. Pin 1.9.0 read from `pnpm-lock.yaml`. |
| A native Go provider over CDP | No Node. Inference: this server is the easier of the two to replace natively, because it is itself a thin layer over CDP | A tool catalogue to design and maintain; DevTools-specific analysis (performance insights) would be missing | `github.com/chromedp/chromedp` v0.24.0, MIT, checked 2026-11-18 as recorded in [playwrightmcp.md](playwrightmcp.md). Not chosen here. |

**Tradeoff.** No browser tools from this provider until the operator installs Node and
the server. The Node version range is narrower than Playwright MCP's, so one Node
installation may satisfy one server and not the other; the provider's "unavailable"
message must state the required range.

## 8. Verification

Local test. Nothing here has been run. A scenario against a real Chromium belongs to the
owning area.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-CHROMEDEVTOOLSMCP-1` | local | `DH-LIB-CHROMEDEVTOOLSMCP-R1`, `DH-LIB-CHROMEDEVTOOLSMCP-R2`, `DH-LIB-CHROMEDEVTOOLSMCP-R3` | Table test of the recipe: launch with and without headless and executable path; attach with `http:`, `https:`, `ws:` and `wss:` endpoints; `--no-usage-statistics` is present in every case. With a Go fixture MCP server as the configured command, the provider mounts through the same per-session path as the Playwright recipe. |

## 9. Units dispositioned by this spec

| Unit | Disposition |
|---|---|
| `UP-DEP-chrome-devtools-mcp` | replaced by `DH-LIB-CHROMEDEVTOOLSMCP` under DH-ADR-PLAYWRIGHTMCP-1 |
