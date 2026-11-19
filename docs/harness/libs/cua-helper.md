# scripts/cua-helper (computer-use helper packaging): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Nothing here was built, run or tested.
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The owner decided
> that computer use works **only through an external MCP provider** and that the Go
> project ships no native helper. The first version of this document left the helper
> open between four options and specified how a shipped helper would be signed and
> verified. That is replaced: the helper and its packaging are dropped by
> [DH-ADR-CUAHELPER-1](#dh-adr-cuahelper-1), and section 6 states what an operator
> installs instead.

| | |
|---|---|
| **Library ID** | `DH-LIB-CUAHELPER` |
| **Upstream unit** | `UP-PKG-scripts.cua-helper` |
| **Upstream version** | not a published package: 13 files of release scripts and their tests |
| **Kind** | build-only: release tooling for a native helper program |
| **Used by** | release workflows (`.github/workflows/cua-helper-package-macos.yml:72`, `:80`, `:94`; `release-publish.yml:99`; `macos-package.yml:153`) and the macOS provider package `packages/experimental/cua-macos` |
| **Go choice** | **dropped** by [DH-ADR-CUAHELPER-1](#dh-adr-cuahelper-1): no helper program is built, bundled, signed or started by the Go project |
| **Related areas** | computer-use providers (`../areas/x-cua-browser-voice.md`); [trycuacua-driver.md](trycuacua-driver.md) for the external provider, its launch recipe and the approval and confinement rules; [modelcontextprotocol.md](modelcontextprotocol.md) for the MCP client |

## 1. What it is and why upstream needs it

**Computer use** lets the model observe and operate the local desktop. Upstream's
first-party computer-use providers do not control the desktop from the host. Each starts
a **helper**: a small native program that calls the operating system's accessibility,
screen-capture and input interfaces and answers requests from the host. On macOS the
helper is a Swift application, `DSH Computer Use.app`
(`packages/experimental/cua-macos/native/README.md:12`). On Windows it is a .NET
program, `dsh-cua-windows.exe` (`.github/workflows/cua-helper-package-windows.yml:45`).

The helper is not `@trycua/cua-driver`. It is upstream's own code that adapts selected
behaviour of that MIT-licensed project "without loading an external driver"
(`packages/experimental/cua-macos/native/README.md:12`).

`scripts/cua-helper` is the release tooling for the **macOS** helper. It exists because
of one macOS rule: permission to read other applications' windows and to send them input
is granted by the user **to a specific signed application**. If the helper's signature
changes, the grant is lost; if the helper is not notarized by Apple, the system refuses
to open it. So the helper must be built, signed with the company certificate, notarized,
verified, and delivered byte for byte into the published package. These scripts do that
and prove at each hand-over that nothing changed.

The Go project ships no helper, so it has nothing to build, sign or verify here. The
same macOS rule still applies to whatever program the operator installs: the user
grants the permissions to that program's identity, and the harness never holds them.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for |
|---|---|
| `scripts/cua-helper/package.ts:66` to `:76` | For arm64 and x64: build the Swift application with the "Developer ID Application" identity, submit it for notarization, verify it, and seal it under a directory named by its content digest |
| `package.ts:56` to `:61` | The verification: signature check, architecture check (`lipo -verify_arch`), notarization ticket check (`stapler validate`), and a Gatekeeper assessment (`spctl --assess`) |
| `package.ts:78` to `:90` | Write the manifest `cua-helper.json`, create `cua-helper.tgz`, extract it again and repeat every check on the extracted copy |
| `package.ts:39` to `:43`, `:93` to `:97` | Keep a packaging journal, remove secrets from logged output, delete the partial archive on failure |
| `payload.ts:14` to `:19` | Manifest fields: source digest, build key, version, commit, Apple Team ID, and one content digest per architecture |
| `payload.ts:28` to `:68` | Content digest over file names, bytes and executable bits. Links and special files are rejected; four required files must exist; a directory holding any unselected build is rejected |
| `build-inputs.ts:61` to `:69`, `:142` to `:158` | Source digest over the tracked build inputs; build key that adds toolchain versions, the signing preset and a digest of the certificate file |
| `select-artifact.ts:47` to `:108` | Find an earlier workflow run within 14 days whose artifact has the same build key and whose commit has byte-identical inputs, so that building, signing and notarizing can be skipped |
| `stage.ts:12`, `verify-tarball.ts:14` | Copy the verified helper into the provider package before it is packed, and check the packed archive again before it is published |
| `smoke.py:58` to `:103` | On a logged-in Mac: start the helper, connect to its Unix socket, read capabilities and permission status, check that the reported application name is the helper's, close, and confirm the helper exits. It asks for no permission |

How the helper runs and what it needs, from the provider documents:

| | macOS | Windows |
|---|---|---|
| Program | Swift 6 application, macOS 14 or later, no Dock icon (`native/Info.plist:6`, `:20`, `:22`) | .NET 10, published self-contained for x64 (`cua-helper-package-windows.yml:37`, `:47`) |
| Operating-system interfaces | AppKit, Accessibility, CoreGraphics, ScreenCaptureKit; background input uses private SkyLight symbols (`native/README.md:12`; `cua-macos/README.md:276`) | UI Automation and Windows Graphics Capture (`docs/subsystems/computer-use.md:33`; `cua-windows/README.md:89`) |
| How it starts | Through LaunchServices as an independent application, so the system attributes permissions to the helper and not to the terminal or the harness. Nested inside another application, it first copies itself to a verified standalone location (`cua-macos/README.md:25`; `native/README.md:26`) | Started by the provider as a child process |
| How the host talks to it | JSON lines over a Unix socket in a private directory; one shared helper per user and build; it exits after its last connection (`smoke.py:63` to `:88`; `cua-macos/README.md:154`) | JSON protocol with the provider |
| Permissions the user must give | Accessibility (tree reads and input) and Screen Recording (capture), each granted by hand in System Settings to the helper application (`cua-macos/README.md:44`) | None to grant. Needs an unlocked interactive session; reports elevation status (`cua-windows/README.md:89`; `docs/subsystems/computer-use.md:17`) |
| Signing | Developer ID, hardened runtime, secure timestamp, notarized, ticket stapled (`native/build_app.py:90`; `package.ts:71` to `:73`). Development builds are signed ad hoc and lose their grants when the binary changes (`native/README.md:28`) | Published **unsigned** (`cua-helper-package-windows.yml:84`) |

## 3. Tech debt upstream carries

Search: the 13 files, the three workflows that call them, and both provider READMEs.
Limitations listed in the provider READMEs are units owned by the providers area.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L13.2` | The Windows helper is published unsigned and through a separate workflow with its own, weaker manifest; none of the digest checks in `payload.ts` cover it. | `.github/workflows/cua-helper-package-windows.yml:74` to `:84` | **Resolved**: the Go project publishes no helper for any platform ([DH-ADR-CUAHELPER-1](#dh-adr-cuahelper-1)), so no unsigned helper ships and there is no second, weaker manifest. |
| (part of the assigned unit) | Three languages and two package managers are needed to produce one helper: TypeScript scripts, a Python build script and smoke test, and Swift. The source digest must therefore cover installer settings, lock files and patches (`build-inputs.ts:11` to `:18`). | `package.ts:69`; `build-inputs.ts:11` | Gone with the helper: the Go release builds one pure-Go binary. |
| (part of the assigned unit) | A 200-line artifact-reuse protocol queries the hosting service to avoid re-signing. | `select-artifact.ts:47` | Gone with the helper. |
| (part of the assigned unit) | Source checkouts compile the helper on the user's machine at activation, needing a Swift or .NET toolchain. | `cua-macos/README.md:36`; `cua-windows/README.md:25` | Gone with the helper: the harness never compiles or downloads native code. |

## 4. Requirements for the Go implementation

None for a helper: nothing is built. The first version of this document listed eight
requirements, `DH-LIB-CUAHELPER-R1` to `-R8`, for a shipped helper (signing,
notarisation, a digest manifest, a digest check before start, a stable signing
identity, a private socket, a packaging journal). All eight are retired with the helper
and no test proves them.

What the Go design must still do for computer use is stated where the external provider
is specified: the launch recipe, the tool namespace, the single-provider rule and the
approval rules are `DH-LIB-CUADRIVER-R1` to `-R6` in
[trycuacua-driver.md](trycuacua-driver.md). One consequence belongs here because it is
the other half of this drop:

- The release contains no helper program, and no code path in the harness builds,
  downloads, verifies or starts one. Computer use with no external provider configured
  reports itself unavailable and names what to install. This is checked by
  `DH-T-CUADRIVER-1` in [trycuacua-driver.md](trycuacua-driver.md).

## 5. Go choice

**Decision.** Dropped. The owner chose the option the first version of this document
called H4.

| Option | What the Go project would build or bundle | Why it is or is not chosen |
|---|---|---|
| H1. One binary, two roles: `dsh` itself as the helper, calling system frameworks without cgo | macOS: `dsh` wrapped in a signed application bundle | Not chosen. Unproven interfaces from Go, and the helper identity would carry the whole harness |
| H2. Native-language helpers: Swift on macOS, .NET on Windows | Two helper programs with their own toolchains, bundled beside `dsh` | Not chosen. Three languages, a bundled .NET runtime, and a signing chain for a second artifact |
| H3. A separate helper written in Go, cgo on macOS only | A small second program per platform | Not chosen. It was this document's earlier recommendation; it needs cgo for one artifact, a macOS build runner and the whole chain of custody of section 2 |
| **H4. Ship no helper; computer use only through an operator-installed MCP server** (chosen by the owner) | Nothing | Chosen. No native code, no cgo anywhere in the project, no signing beyond the `dsh` binary, no helper identity to keep stable across releases |

Go modules the rejected options would have used were checked on 2026-11-18 for the
first version of this document (`go list -m -json <module>@latest`, `go mod download
-json`, licence file, search for `import "C"`). None is used. The findings are kept as a
record of what was examined, not as a choice:

| Module | Version, date | Licence | cgo | Was relevant to |
|---|---|---|---|---|
| `github.com/ebitengine/purego` | v0.11.3, 2026-11-01 | Apache-2.0 | none in normal builds | H1: calling macOS frameworks without cgo |
| `github.com/go-ole/go-ole` | v1.3.0, 2023-08-04 | MIT | none found | H1, H3: COM on Windows for UI Automation. Last release over three years old |
| `github.com/go-vgo/robotgo` | v1.3.7, 2026-10-22 | Apache-2.0 | **yes** (`robotgo.go`, `key.go`, `robotgo_mac.go`) | Rejected as a host dependency; also pulls in an OCR binding |
| `github.com/kbinani/screenshot` | v0.0.0-20250624051815-089614a94018 | MIT | **yes on macOS** (`darwin.go`) | Whole-screen capture only |
| `github.com/jezek/xgb` | v1.3.1, 2026-04-18 | BSD-style (The XGB Authors) | none found | A Linux helper on X11 |

<a id="dh-adr-cuahelper-1"></a>
### DH-ADR-CUAHELPER-1 The project ships no native computer-use helper; computer use is reached only through an external MCP server the operator installs

- **Decision.** Decided by the owner on 2026-11-18. The Go project builds, bundles,
  signs and starts no helper program on any platform. Upstream's two first-party
  providers (the Swift helper on macOS and the .NET helper on Windows) and the release
  tooling in `scripts/cua-helper` are not ported. Computer use exists in the Go product
  as a seam with one kind of provider: an MCP server that the operator has installed
  and configured, started by the harness as a child process and reached over MCP
  ([trycuacua-driver.md](trycuacua-driver.md)). With no such server configured, computer
  use is unavailable and says so.
- **Why.** The owner's rule that the core binary is pure Go, with nothing else in the
  design needing cgo, leaves no cheap way to call the macOS accessibility and capture
  frameworks. A helper would be a second artifact with its own languages or cgo, its
  own signing identity that must never change, and its own chain of custody from build
  to first start. That is a large cost for an experimental capability that an existing
  external program already provides behind a standard protocol.
- **Tradeoff.** A recorded exception to [DH-ADR-1](../design.md#dh-adr-1). Computer use
  no longer works out of the box: the operator must install a driver, and the harness
  cannot guarantee its version or vouch for its code. The harness cannot map a
  third-party driver's tool arguments to an application identity, so the fine-grained
  per-application grants of upstream's first-party providers are not available and the
  coarser rule (h) of [DH-ADR-CUADRIVER-2](trycuacua-driver.md#dh-adr-cuadriver-2)
  applies. The operating-system permissions belong to the installed driver's identity,
  which the project does not control.
- **Failure and cleanup.** There is nothing of the helper's to clean up. A driver that
  is missing, crashes or hangs is handled as any MCP server is: activation fails and
  names the program, or pending calls fail and the fiber restarts the process under a
  budget ([trycuacua-driver.md](trycuacua-driver.md), section 6).
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-14](../design.md#dh-o-14).
  **Verified by.** `DH-T-CUADRIVER-1` in [trycuacua-driver.md](trycuacua-driver.md)
  (no provider configured means unavailable with a message, and nothing is started or
  downloaded), and review of the release's asset list, which names no helper.

## 6. Design notes: what the operator must install

The harness ships a launch recipe and no driver. To use computer use an operator does
four things. Only the last is done in the harness.

| Step | What | Where it is specified |
|---|---|---|
| 1 | Install an MCP server that provides desktop control on the same machine as the host. The one recipe the design carries is for **Cua Driver**, the third-party `cua-driver` program that upstream's external provider also uses (upstream pins 0.28.0; default command `cua-driver`, arguments `mcp`: `packages/experimental/computer-use-cua-driver-mcp/README.md:38` to `:45`). Its own installation guide owns the platform setup (`README.md:28` of the same package). Any other MCP server that offers desktop tools can be configured the same way | [trycuacua-driver.md](trycuacua-driver.md), `DH-LIB-CUADRIVER-R1` |
| 2 | Give that program the operating-system permissions it asks for. On macOS these are Accessibility and Screen Recording, granted by hand in System Settings to the driver's own identity. Inference from the macOS rule in section 1: the grant belongs to the driver program and is lost if its signature changes, which is the driver vendor's concern and not the harness's | The driver's own documentation |
| 3 | Have a logged-in, unlocked graphical session on the machine. A headless server or a locked screen gives a driver nothing to observe | n/a |
| 4 | Enable the computer-use entry in a configuration layer and point it at the program (`command`, `args`). It is off by default | [trycuacua-driver.md](trycuacua-driver.md), rule (a) of DH-ADR-CUADRIVER-2 |

**Who owns what.**

| Resource | Owner (releases it) | Who may use it |
|---|---|---|
| The driver program on disk, its updates and its signature | The operator who installed it, and its vendor | The harness may start it; it never modifies or replaces it |
| The driver process the harness started | The provider's fiber, through the harness process service | Agents the provider's tools are mounted for |
| The operating-system permission grant | The user. The harness cannot grant or revoke it and never asks the system for it | The driver program |
| The desktop | Nobody in the harness. It is shared with the user and every other program | Whoever the operating system lets in |

## 7. Risks and open questions

1. **Per-call approval may be impractical.** With only a third-party driver, rule (h)
   of DH-ADR-CUADRIVER-2 asks for approval on every input or capture call unless the
   operator marks the provider as trusted for a session. The owner's decision on
   approvals (one-shot plus session grants of a named scope) allows a session grant; the
   providers and interaction areas must define the scope for a driver whose targets the
   harness cannot name.
2. **No vetted driver.** The project neither audits nor pins the driver's code. The
   recipe records the version it was tested with and warns on a difference
   (`DH-LIB-CUADRIVER-R6`); that is a notice, not a control.
3. **Platform coverage is the driver's.** Which operating systems work, and which
   private interfaces a driver depends on, are outside this project.
4. **Reversal.** If the owner later wants a first-party helper, the four options and
   the module findings in section 5, and upstream's chain of custody in section 2, are
   the starting point. Nothing in the seam prevents adding a second kind of provider.

## 8. Verification

No test or scenario is declared by this specification, because it builds nothing. The
two local tests of the first version (a helper bundle digest check, and starting only a
helper whose digest matches) are retired with the helper.

The drop itself is checked where its effect shows: `DH-T-CUADRIVER-1` in
[trycuacua-driver.md](trycuacua-driver.md) asserts that with no driver program
configured the provider is unavailable, names the program, starts no process and
downloads nothing. A real-desktop scenario with an installed driver is a different kind
of evidence and belongs to the providers area; it needs a logged-in graphical session
and stays open when skipped. Nothing here has been run.

## 9. Units dispositioned by this spec

| Unit | Disposition |
|---|---|
| `UP-PKG-scripts.cua-helper` | dropped by DH-ADR-CUAHELPER-1 (`DH-LIB-CUAHELPER`, choice dropped) |
| `UP-DEBT-found.L13.2` | resolved by DH-ADR-CUAHELPER-1: no helper is published |
