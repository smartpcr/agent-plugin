# @trycua/cua-driver: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Nothing here was built, run or tested.
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The owner decided
> that computer use works only through an external MCP provider and that the project
> ships no native helper ([cua-helper.md](cua-helper.md)). The external provider
> specified here is therefore the only route to computer use, on every platform. The
> owner also settled approvals as one-shot plus session grants, which rule (c) below
> relied on.
>
> Revised on 2026-11-19 to conform to the areas that own the seam. Area A16 fixed one
> harness-owned tool catalogue for computer use
> ([DH-ADR-MEDIA-1](../areas/document-browser-computer.md#dh-adr-media-1)), and area X4
> reaches Cua Driver through an adapter behind it
> ([DH-ADR-XCUA-1](../areas/x-cua-browser-voice.md#dh-adr-xcua-1)). Requirement R2 and
> rule (h) below were rewritten: the driver's own tool names never reach the model, and
> approval is an application grant for every provider.

| | |
|---|---|
| **Library ID** | `DH-LIB-CUADRIVER` |
| **Upstream unit** | `UP-DEP-@trycua/cua-driver` |
| **Upstream version** | 0.28.0, with six per-platform binary packages of the same version (`pnpm-lock.yaml:23539` to `:23572`) |
| **Kind** | runtime, native: compiled code loaded into the host process |
| **Used by** | `experimental` (`packages/experimental/computer-use-cua-driver-native`) |
| **Go choice** | own code: a recipe (launch arguments plus a mapping onto the harness's fixed computer-use catalogue) for the external `cua-driver` program over MCP, which the operator installs. The in-process form is not kept. No Go module. This is the only computer-use recipe the Go design carries |
| **Related areas** | A16, which owns the computer-use seam, its tool catalogue, application grants, the refused-target list and the desktop lease ([../areas/document-browser-computer.md](../areas/document-browser-computer.md), `DH-D-MEDIA-6`); X4, which owns the adapter and the `cua-driver` recipe ([../areas/x-cua-browser-voice.md](../areas/x-cua-browser-voice.md), `DH-D-XCUA-1`); A17 for session grants ([../areas/interaction-credentials.md](../areas/interaction-credentials.md)); [cua-helper.md](cua-helper.md) for the drop of the first-party helper and what the operator installs; MCP client [modelcontextprotocol.md](modelcontextprotocol.md) |

## 1. What it is and why upstream needs it

**Computer use** lets the model observe and operate the local desktop: list applications
and windows, read a window's **accessibility tree** (the structured description of
buttons, fields and text that screen readers use), take screenshots, and send mouse and
keyboard input (`docs/subsystems/computer-use.md:5`). As with browser use, a shared
service only records which one provider is mounted; each provider owns its tools
(`docs/subsystems/computer-use.md:37`).

**Cua Driver** is a third-party desktop-automation runtime from the `trycua` project.
Upstream offers it in two forms. The MCP form starts an already installed `cua-driver`
program and talks to it as an MCP server
(`packages/experimental/computer-use-cua-driver-mcp/README.md:28`). The native form,
which is this dependency, installs the runtime from npm and loads it **into the host
process**, so that no separate installation is needed
(`.agents/notes/implemented/architecture/2026-09-12-computer-use-provider-registration.md:25`).

Upstream also has two first-party providers with their own helper programs (Swift on
macOS, .NET on Windows). They do not use this package, and the Go project does not port
them: see [cua-helper.md](cua-helper.md).

## 2. How upstream uses it

All in `packages/experimental/computer-use-cua-driver-native/src/index.ts`.

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `:92` | Load the native code only when the provider activates | dynamic `import('@trycua/cua-driver')` |
| `:96` | Create one runtime with the library's same-process defaults | `CuaDriver.create(undefined)` |
| `:97` | Ask the runtime for its tool catalogue as JSON | `listToolsJson({signal})` |
| `:100` to `:128` | Register each tool as `cua_driver_native__<name>`; reject a name outside the model API's alphabet or a duplicate | catalogue fields `name`, `description`, `inputSchema`, `outputSchema` |
| `:116`, `:122` | Run one tool and parse its raw JSON result (text, structured output, image bytes) | `callTool(name, jsonArgs, {signal})`, `result.rawJson` |
| `:70` to `:72` | Shut the runtime down and free the native handle on unload | `shutdown()`, `uniffiDestroy()` |

The library is therefore used as an in-process tool server with three calls: list, call,
shut down. The harness adds a name prefix, a prompt section (`:41`) and the shared image
handling of its MCP result adapter (`README.md:68`).

Which binary runs. The npm package has six optional platform packages: macOS arm64 and
x64, Linux arm64 and x64 (glibc), Windows arm64 and x64 (`pnpm-lock.yaml:30988` to
`:30994`), and two binding packages, `@ubjs/core` and `@ubjs/node`
(`pnpm-lock.yaml:30986`). No process is started: the code runs inside the Node host.
Two consequences are stated by upstream. Operating-system desktop permissions belong to
**whatever application launched the harness**, such as a terminal, and a native crash
ends the host (`README.md:43`).

## 3. Tech debt upstream carries

Search: `patches/` (none), `pnpm-workspace.yaml` for `cua` (seven exemptions), the
provider source and README. The README's "Known Limitations" bullets are units owned by
the providers area.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-pin.minimumReleaseAgeExclude.@trycua/cua-driver@0.28.0` and the six platform units `...cua-driver-darwin-arm64@0.28.0`, `-darwin-x64`, `-linux-arm64-gnu`, `-linux-x64-gnu`, `-win32-arm64-msvc`, `-win32-x64-msvc` | Seven exact versions are exempted from the installer's waiting period for new releases, so that a reviewed set of prebuilt native binaries can be installed at once. Every bump needs all seven lines changed. | `pnpm-workspace.yaml:97` to `:104` | No native binary is installed with the harness or loaded into it ([DH-ADR-CUADRIVER-1](#dh-adr-cuadriver-1)). The driver is a program the operator installs; its version is recorded when it connects. |
| `UP-DEBT-found.L13.3` | Third-party native code shares the host process. A crash in it ends every session, and the desktop permission it uses is the launching terminal's, which then extends to everything started from that terminal. | `README.md:43` of the native provider | Desktop drivers run only in a separate process ([DH-ADR-CUADRIVER-1](#dh-adr-cuadriver-1)). |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-CUADRIVER-R1` | Cua Driver is reached only as an external program over MCP on stdio, started by the harness process service with the scrubbed environment, and found by explicit configuration (`command`, `args`; default `cua-driver mcp`). If it is missing the provider is unavailable with a message naming the program; nothing is downloaded. | `computer-use-cua-driver-mcp/README.md:38`; DH-ADR-PLAYWRIGHTMCP-1 |
| `DH-LIB-CUADRIVER-R2` | The model never sees the driver's own tool names, descriptions or schemas. The adapter maps the harness's fixed `computer_*` catalogue onto the driver tools the recipe lists (`allowTools`) and calls no other driver tool. At start-up it checks the driver's tool list against the recipe; a missing or changed tool makes the provider `unavailable` and names the tool. Screenshots become stored attachments, not inline bytes. | [DH-ADR-MEDIA-1](../areas/document-browser-computer.md#dh-adr-media-1); `src/index.ts:39`, `:100` to `:106`; `README.md:116`, `:120` |
| `DH-LIB-CUADRIVER-R3` | One computer-use provider per composition. Start-up failure releases the registration; a failed shutdown keeps it and is reported. | `README.md:37`, `:136` |
| `DH-LIB-CUADRIVER-R4` | Cancelling a call or unloading the provider never claims that delivered input was undone; the result says the outcome is unknown and tells the model to observe before retrying. | `README.md:135`; `src/index.ts:41` |
| `DH-LIB-CUADRIVER-R5` | The approval and confinement rules of [DH-ADR-CUADRIVER-2](#dh-adr-cuadriver-2) apply to every computer-use provider, whoever supplies it. | `upstream/security.md` DH-ADR-SECURITY-4, -5 |
| `DH-LIB-CUADRIVER-R6` | The version the driver reports at connection is recorded and compared with the version the recipe was tested with; a difference is a warning, not a failure. | `README.md:137`; DH-ADR-PLAYWRIGHTMCP-1 |

## 5. Go choice

**Decision.** The Go harness keeps one Cua Driver provider, the external one. The
native npm provider is **merged** into it: same driver, different transport. The
harness ships a recipe and no driver, and the model sees the harness's own computer-use
tools whichever driver answers them. By the owner's decision of 2026-11-18
this external provider is also the **only** computer-use provider: no first-party helper
exists beside it ([cua-helper.md](cua-helper.md#dh-adr-cuahelper-1)).

**Why.** The npm package is a Node addon. A Go binary cannot load it, and the Go ways of
loading the same native code all fail a requirement.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **A. External `cua-driver` program over MCP** (chosen) | No native code in the host. A crash fails pending calls only. The driver's own application owns the desktop permission. Already an upstream provider. | The operator installs and updates the driver. The harness cannot guarantee its version. | Nothing to link. Upstream pin 0.28.0 read from `pnpm-lock.yaml`. |
| B. Link the driver's native library with cgo | One installation. | Needs cgo, which the release plan excludes, and a prebuilt library per platform. Keeps `UP-DEBT-found.L13.3`. Inference: the npm packages contain Node addons, so a C library would have to come from the driver's own releases; not checked. | Not examined further. |
| C. Load a shared library without cgo | Keeps `CGO_ENABLED=0`. | Same crash and permission problems as B; depends on a C interface the driver may not publish. | `github.com/ebitengine/purego` v0.11.3, 2026-11-01, Apache-2.0. It loads shared libraries without cgo; its `internal/cgo` files are used only in cgo builds. |
| D. Drop Cua Driver | Nothing to maintain. | With no first-party helper in the Go project, this would remove computer use altogether. | n/a |

Module facts were checked on 2026-11-18 with `go list -m -json` and `go mod download
-json`, and by reading the licence file in the module cache.

**Tradeoff.** Upstream added the native provider precisely to avoid a separate
installation. The Go design gives that convenience up and does not restore it: no
helper ships with the harness, so an operator who wants computer use installs the
driver, grants it the operating-system permissions and enables the entry
([cua-helper.md](cua-helper.md), section 6).

<a id="dh-adr-cuadriver-1"></a>
### DH-ADR-CUADRIVER-1 A desktop driver never runs inside the host process

- **Decision.** Code that reads or controls the desktop runs in a process of its own: a
  program the operator installs, never one the harness ships. The host talks to it over
  MCP on the program's standard input and output. Verdict on upstream's in-process
  native provider: **drop the mechanism, keep the capability** through option A.
- **Why.** Three reasons. The host holds every session, so native code that can crash
  must not share it (`DH-O-11` applies the same rule to plugins). Operating-system
  permissions attach to a program's identity, so a separate program gives the user one
  named thing to grant and revoke. And the owner requires a pure-Go core binary.
- **Tradeoff.** One more process and a protocol to version. A round trip per call.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-11](../design.md#dh-o-11),
  [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-CUADRIVER-1`,
  `DH-T-CUADRIVER-2`.

<a id="dh-adr-cuadriver-2"></a>
### DH-ADR-CUADRIVER-2 Desktop control is unconfined and is gated by application grants

Desktop control is the most powerful tool the harness has. The file sandbox does not
cover it: a click in a terminal window runs any command as the user, and a click in a
file manager deletes files outside every write root. The design says so and gates it.

| Rule | Why | Tradeoff |
|---|---|---|
| (a) **Off by default.** Computer use is an entry an operator enables in a layer. It is not part of subagent presets, and a model-authored plugin can never be granted it. | Enabling it changes what the security model can promise. | A user must find the switch. |
| (b) **Reported as unconfined.** The status the UI shows next to the sandbox mode says that computer use bypasses file confinement while it is enabled. | "No false reassurance" is the reason enforcement is reported at all. | A permanent warning. |
| (c) **An application grant comes first.** Before any tree read, title read, screenshot or input for an application, the user grants access to that application. The grant is asked through the interaction registry, is recorded in the session log with the answering principal and channel, covers one session and one application identity, and can be revoked. With no answerer, or with approval policy `never`, it is denied. | Kept from upstream's first-party providers (`packages/experimental/cua/README.md:50`). A grant per call would make the feature unusable; a grant per application is the smallest scope a person can reason about. | A grant is wider than a one-shot approval. The owner settled this on 2026-11-18 (open decision OD-10): approvals are one-shot, except that the user may grant a named scope, such as one application, until the session ends; grants are logged, listed and revocable and nothing is stored across sessions. An application grant is exactly such a session grant. An application identity is a name, not proof of a publisher (`packages/experimental/cua-macos/README.md:90`). The rule is enforceable for every provider because the application is always an argument of the harness's own tools ([DH-ADR-MEDIA-1](../areas/document-browser-computer.md#dh-adr-media-1)); rule (h) covers a server that cannot target one application. A grant is never copied: it does not survive a session resume and does not pass to a fork or a subagent ([DH-ADR-16](../design.md#dh-adr-16), confirmed by the owner on 2026-11-19). |
| (d) **Some targets are refused before any prompt.** The harness's own user interface; the operating system's privacy and credential prompts. Terminals and password managers are refused unless the operator lists them. | Otherwise the model could click "approve" on its own approval prompt, grant itself more permissions, or leave the sandbox through a terminal. | The harness UI is now always a tab or an installed window of the user's own browser, which cannot be recognised reliably among other browser windows. This gap is larger than when a desktop application was planned, and it is stated to the operator. |
| (e) **Only the owner answers.** A grant can be answered only by the owner principal on a local channel, never by a remote non-owner peer, a plugin or an automation bridge. An automation profile may pre-grant named applications in configuration. | Session sharing plus computer use is otherwise remote control of the owner's desktop. Upstream's MCP profile grants unattended or forwards the question (`docs/subsystems/computer-use.md:24`). | Shared sessions cannot unblock a waiting grant. |
| (f) **Moving the real pointer, or stealing the foreground, is a separate decision** for the session. Subagents cannot ask for it. | Kept from upstream (`packages/experimental/cua-macos/README.md:66`, `:249`): it interrupts the person at the keyboard. | A second prompt. |
| (g) **Screenshots are of one named window**, never the whole screen, and are stored as attachments. The UI marks them as possibly containing secrets, because they are sent to the model provider. | A screen shows other applications' content. | Tasks that need the whole screen are not supported. |
| (h) **A server that cannot target one application offers only a whole-desktop grant.** Rules (c) and (d) are enforced by the seam, not by the driver: every call except the application list carries the application and the window as arguments, and the seam checks them against the grant and the refused-target list before the provider is called. A server that offers only whole-screen capture and global input registers with `Targeting: none`; its single entry is `desktop`, and the only grant that can be asked for is shown as "the entire desktop, every application". There is no per-call approval mode and no "trusted provider" switch. | Area A16 owns the rule ([../areas/document-browser-computer.md](../areas/document-browser-computer.md) §6.2, `DH-D-MEDIA-6`). A harness-owned catalogue is the only place where approval can read the target from the arguments. | Each MCP-based server needs a mapping someone maintains, and a driver's special abilities are out of reach until the catalogue grows. A whole-desktop grant is as wide as a grant can be, and is worded so. |

- **Residual risk, stated to the operator.** Screen content is untrusted text and
  images; it can instruct the model. Other programs and other sessions share the
  desktop, so an observation can be stale by the time input arrives. Input that was
  delivered cannot be undone.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-6](../design.md#dh-o-6).
  **Verified by.** `DH-T-CUADRIVER-3`, `DH-T-CUADRIVER-4`, and the real-desktop
  scenarios the providers area declares.

## 6. Design notes

The recipe is data plus one function, as for the browser MCP servers: the provider
name, the default command and arguments, the tested version, the driver tools the
adapter may call and how each harness tool maps onto them. Area X4 owns the recipe type
and the adapter (`computermcp`); the MCP package of area A15 owns the connection, which
exposes no `mcp__` name to the model.

| Resource | Owner (releases it) | Who may use it |
|---|---|---|
| The `cua-driver` process | The provider's fiber, through the harness process service | Every agent the provider's tools are mounted for |
| The desktop | Nobody in the harness. It is shared with the user, other sessions and other programs | Whoever the operating system lets in |
| An application grant | The session that asked; removed by revocation or with the session | That session's agents |

**Failure, retry, cleanup.** A missing program fails activation before any process
starts and releases the registration. A crash fails pending calls with one error kind;
the fiber restarts the program under a budget, and no call is replayed. A cancelled call
is answered "outcome unknown". On unload: remove the tools, cancel and await running
calls, ask the program to stop, close its input, then terminate the tree. Revoking a
grant cancels that session's running calls for the application and awaits them before
it returns; it replays nothing. Nothing on the desktop is rolled back in any case.

**Harness code this implies, and who delivers it.** The application-grant check, the
refused-target list, the desktop lease and the "unconfined" status are delivered by
area A16 ([DH-D-MEDIA-6](../areas/document-browser-computer.md#dh-d-media-6)); the
grant store and its prompt by area A17; the adapter and recipe by area X4
([DH-D-XCUA-1](../areas/x-cua-browser-voice.md#dh-d-xcua-1)). This specification
declares no deliverable.

## 7. Risks and open questions

1. **Application grants versus `allowed-once`.** Settled by the owner on 2026-11-18:
   one-shot approvals plus session grants of a named scope; rule (c) uses that. The
   scope for a server that cannot target an application is the whole desktop, named as
   such (rule h, area A16).
2. **Rule (d) and the browser.** The harness UI is always in the user's own browser now,
   so it is always a possible target that cannot be identified. Options for the security
   owner: require a confirmation gesture that synthetic input cannot produce, or refuse
   browser windows whose title carries the harness origin. Not settled, and more urgent
   than before.
3. **Does the driver report its version in the MCP handshake?** Needed for
   `DH-LIB-CUADRIVER-R6`; to be confirmed against the real program.
4. **Platform coverage.** Computer use works on exactly the systems the installed driver
   supports. The project ships nothing that widens or guarantees that.
5. **Does Cua Driver meet the provider profile?** Whether its tools take a window
   identifier for observation and input, so that it registers with application
   targeting and not as `Targeting: none`, is taken from the driver's published
   reference and must be confirmed by area X4's real-desktop scenario
   ([DH-E-XCUA-1](../areas/x-cua-browser-voice.md#dh-e-xcua-1)).

## 8. Verification

Local tests use a Go fixture MCP server that imitates the driver. Nothing here has been
run. A scenario on a real desktop is a different kind of evidence and belongs to the
providers area; it needs a logged-in graphical session on each operating system and
stays open when skipped.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-CUADRIVER-1` | local | `DH-LIB-CUADRIVER-R1`, `DH-LIB-CUADRIVER-R3`, `DH-LIB-CUADRIVER-R6` | Through the loader: with no program at the configured path the provider is unavailable, names it, starts nothing, downloads nothing and holds no registration; the embedded-asset manifest of the binary lists no helper program; with the fixture as the command, a second provider fails to mount; the fixture's reported version is recorded and a differing version produces one warning. |
| `DH-T-CUADRIVER-2` | local | `DH-LIB-CUADRIVER-R2`, `DH-LIB-CUADRIVER-R4` | The fixture's tool list lacks one tool the recipe needs, then lists it with a changed schema: each time the provider is `unavailable` and names the tool. With a matching list, the model's tool schemas are the harness catalogue and contain no driver tool name; a driver tool outside `allowTools` is never called. An image result becomes an attachment reference with no base64 in model history. The fixture exits mid-call: the call fails, other sessions continue, the host process is alive. A cancelled call returns "outcome unknown". |
| `DH-T-CUADRIVER-3` | local | `DH-LIB-CUADRIVER-R5` | With a fixture provider that names its target application and a fake answerer: no answerer gives a denial and the provider receives no request; a grant for application A does not admit application B; revocation cancels a running call and the next call asks again; the grant record in the log names the principal and channel; a non-owner peer's answer is rejected. |
| `DH-T-CUADRIVER-4` | local | `DH-LIB-CUADRIVER-R5` | With the same fixture provider: a request that targets the harness's own process or a listed system surface is refused before any prompt; a terminal is refused until the operator lists it. With a fixture server that offers only whole-screen capture and global input, the provider registers with `Targeting: none`, the application list is the single entry `desktop`, the only grant offered is the whole desktop, and that grant is gone in a new session, after a resume and in a fork. |

## 9. Units dispositioned by this spec

| Unit | Disposition |
|---|---|
| `UP-DEP-@trycua/cua-driver` | merged into the external Cua Driver provider: `DH-LIB-CUADRIVER` under DH-ADR-CUADRIVER-1 |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@trycua/cua-driver@0.28.0` | resolved by DH-ADR-CUADRIVER-1 |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@trycua/cua-driver-darwin-arm64@0.28.0` | resolved by DH-ADR-CUADRIVER-1 |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@trycua/cua-driver-darwin-x64@0.28.0` | resolved by DH-ADR-CUADRIVER-1 |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@trycua/cua-driver-linux-arm64-gnu@0.28.0` | resolved by DH-ADR-CUADRIVER-1 |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@trycua/cua-driver-linux-x64-gnu@0.28.0` | resolved by DH-ADR-CUADRIVER-1 |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@trycua/cua-driver-win32-arm64-msvc@0.28.0` | resolved by DH-ADR-CUADRIVER-1 |
| `UP-DEBT-pin.minimumReleaseAgeExclude.@trycua/cua-driver-win32-x64-msvc@0.28.0` | resolved by DH-ADR-CUADRIVER-1 |
| `UP-DEBT-found.L13.3` | resolved by DH-ADR-CUADRIVER-1 |
