# electron-builder family (packaging, signing, notarisation, installers): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Nothing here was built, signed, notarised or run.
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. There is no desktop
> application, so there is no application bundle, no disk image and no installer. What
> is left to package, sign and release is **one `dsh` binary per platform**. The sections
> on bundles and installers of the first version are replaced by that.

| | |
|---|---|
| **Library ID** | `DH-LIB-ELECTRONBUILDER` |
| **Upstream units** | `UP-DEP-electron-builder`, `UP-DEP-app-builder-lib`, `UP-DEP-@electron/notarize`, `UP-DEP-@electron/get` |
| **Upstream versions** | electron-builder 26.17.0, app-builder-lib 26.17.0 (patched), `@electron/notarize` 2.5.0, `@electron/get` 5.1.0 (`apps/desktop/package.json:69` to `:77`); `@electron/osx-sign` 1.3.3 (patched, transitive) |
| **Kind** | Build-only. None is loaded by the shipped application |
| **Used by** | `apps/desktop` packaging scripts |
| **Go choice** | **Toolchain.** GoReleaser builds, archives, signs and notarises the `dsh` binary (already `DH-LIB-RELEASER` in [build-test-toolchain.md](build-test-toolchain.md)), calling the operating systems' signing tools. No packaging engine, no installer, and no Go module linked into the product |
| **Related areas** | [electron.md](electron.md) (why there is no desktop application), [electron-updater.md](electron-updater.md) (update), [cos-nodejs-sdk-v5.md](cos-nodejs-sdk-v5.md) (where releases are published), [../upstream/toolchain.md](../upstream/toolchain.md#dh-adr-toolchain-13) (release decision). Owning area: the toolchain (F7) and the command line (A24) |

## 1. What it is and why upstream needs it

A desktop application is not a file a user can run until four things have happened to
it. It is **packaged** (laid out as a macOS `.app` bundle or a Windows program
directory). It is **signed** (the publisher's certificate vouches for every executable
file, so the operating system does not warn or refuse). On macOS it is **notarised**
(Apple scans the signed build and issues a ticket, which is **stapled** to the artifact
so it works offline). And it is wrapped in an **installer** (a `.dmg` disk image on
macOS, a setup program on Windows).

`electron-builder` does all four for Electron applications, and `app-builder-lib` is its
engine. `@electron/osx-sign` is the macOS signing step inside it, `@electron/notarize`
submits to Apple, and `@electron/get` downloads the Electron runtime that is being
packaged. Upstream drives them through one configuration function
(`apps/desktop/scripts/electron-builder-config.mjs:47`) and 135 script files around it
(`apps/desktop/scripts/`, not counting type-declaration files).

Most of that volume exists because an Electron release is a tree of thousands of files
from many vendors: every executable in it must be found, signed and verified
(`apps/desktop/README.md:108`). The Go release is one executable per platform, and with
no desktop application it needs no packaging and no installer at all. Signing and
notarisation remain, because an operating system treats an unsigned downloaded
executable the same way whether it is an application or a command.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `apps/desktop/scripts/electron-builder-config.mjs:109` to `:275` | The whole packaging configuration: application id, the `dsh` URL scheme, files, signing, notarisation, targets, update feed | electron-builder configuration object and hooks (`beforeBuild`, `beforePack`, `afterPack`, `afterSign`, `artifactBuildCompleted`) |
| `apps/desktop/scripts/electron-builder-config.mjs:166` to `:189` | macOS: Developer ID identity, hardened runtime, entitlements, paths not to re-sign, notarise, targets `dmg` and `zip`, DMG format `ULMO` (LZMA compression) | `mac`, `dmg` options |
| `apps/desktop/scripts/electron-builder-config.mjs:247` to `:272` | Windows: forced signing through a custom signer, SHA-256, publisher name taken from the certificate, per-user NSIS installer with differential package | `win.signtoolOptions`, `nsis` options |
| `apps/desktop/scripts/electron-builder-config.mjs:257` | Linux `AppImage` is configured but not released: release targets are `mac-arm64`, `mac-x64`, `win-x64` (`apps/desktop/publishing/README.md:34`; `apps/desktop/packaging/README.md:100`) | `linux` option |
| `apps/desktop/scripts/build-installed-update-worker.mjs:20`, `apps/desktop/scripts/test-windows-installer.mjs:20` | Programmatic builds for qualification fixtures | `build`, `Platform`, `Arch` |
| `apps/desktop/scripts/windows-directory-installer.mjs:58` to `:60` | Replaces a method on the builder's NSIS target class to change the installer script | Internal modules `app-builder-lib/out/targets/nsis/NsisTarget.js`, `out/toolsets/7zip.js` |
| `apps/desktop/scripts/windows-sign.mjs:8`; `apps/desktop/scripts/test-windows-update-signature.mjs:17` | Signing helpers | Internal modules `out/vm/WineVm.js`, `out/codeSign/windowsSignToolManager.js` |
| `apps/desktop/scripts/verify-runtime-archive.ts:5`, `windows-asar-unpack.mjs:4`, `installed-update-package-content.ts:7` | Reading the ASAR archive (Electron's packed application file) to verify its content | Internal module `out/asar/asar.js` |
| `apps/desktop/scripts/notarize-macos.mjs:3` | Submits the app and the DMG to Apple, waits, staples | `notarize` |
| `apps/desktop/scripts/prepare-runtime.ts:8` | Downloads the pinned Electron distribution for the target | `downloadArtifact` |

What upstream's signing and release process does beyond the libraries, and which the Go
design must judge:

| Upstream behaviour | Evidence | Verdict |
|---|---|---|
| A release without a valid signature is a failed release, never an unsigned artifact | `apps/desktop/windows-signing/README.md:81`; `electron-builder-config.mjs:176`, `:249` | **Keep** (`R1`) |
| macOS: a private temporary keychain per run, import of the certificate file, a signing probe, deletion afterwards | `apps/desktop/packaging/README.md:185` | **Keep the rule** (`R3`): the credential lives only for the run. The mechanism is the release tool's |
| macOS: the app and the DMG are notarised and stapled in two parallel lanes | `apps/desktop/packaging/README.md:185` | **Change**: there is no app bundle and no disk image. The binary is signed and notarised; a bare executable cannot carry a stapled ticket |
| Windows: the private key is on a USB token; a supervised run; an interlock file blocks every later attempt after one failure; timestamping is retried at most three times, the key operation never | `apps/desktop/windows-signing/README.md:65`, `:83`, `:89`, `:93` | **Keep the rule** (never retry a key operation automatically); **change the mechanism** ([DH-ADR-ELECTRONBUILDER-2](#dh-adr-electronbuilder-2)) |
| Windows: every executable in the runtime tree is discovered by reading file headers, signed, and audited; signatures are cached per account | `apps/desktop/README.md:108`; `apps/desktop/windows-signing/README.md:99` | **Drop**: there is no tree. The files to sign are a fixed list of binaries |
| A packaging journal per run (`run.json`, `events.jsonl`, `result.json`); a missing result means "not complete" | `apps/desktop/windows-signing/README.md:91`; `apps/desktop/packaging/README.md:191` | **Keep the principle** (`R6`); the CI job log and the release's own asset list are the record |
| Packaging needs no upload credentials and publishing is a separate, approved step | `apps/desktop/publishing/README.md:64` | **Keep** (`R5`) |
| A per-user Windows installer that registers the `dsh` URL scheme and an uninstall entry; a macOS disk image | `electron-builder-config.mjs:261` to `:272`, `:111`, `:166` to `:189` | **Drop** ([DH-ADR-ELECTRONBUILDER-3](#dh-adr-electronbuilder-3)): installing is unpacking an archive |

## 3. Tech debt upstream carries

Search: `patches/` for the five packages, the comments beside their entries in
`pnpm-workspace.yaml`, imports of builder internals, and the three operation guides
under `apps/desktop`.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-patch.app-builder-lib@26.17.0` | A two-file patch that adds `ULMO` to the list of DMG formats the builder's validator and type declarations accept. Without it the builder rejects the LZMA-compressed disk image upstream wants | `patches/app-builder-lib@26.17.0.patch:10`, `:24`; `apps/desktop/packaging/README.md:102` | **Resolved.** No disk image is built and no packaging engine with a format validator is used ([DH-ADR-ELECTRONBUILDER-3](#dh-adr-electronbuilder-3)) |
| `UP-DEBT-patch.@electron__osx-sign@1.3.3` | A one-line patch in both module builds: the signer's directory walk uses `lstat` instead of `stat`, so symbolic-link aliases inside a framework bundle are not followed and the same files are not signed twice | `patches/@electron__osx-sign@1.3.3.patch:10`, `:23`; `.agents/notes/implemented/architecture/2026-08-25-electron-desktop-packaging-and-updates.md:86` | **Resolved.** There is no bundle and no directory walk. The files to sign are named explicitly, and the rule "never follow a symbolic link while signing" is requirement `R4` |
| `UP-DEBT-pin.allowBuilds.electron-winstaller` | pnpm is told not to run the install script of a Squirrel.Windows helper that electron-builder pulls in but upstream does not use | `pnpm-workspace.yaml:50` to `:53` | **Not applicable.** Go modules run no install scripts, and the package does not exist in the Go design |
| `UP-DEBT-found.L14.2` | Packaging scripts import the builder's internal modules and replace a method on one of its classes at run time. Any builder upgrade can break them silently | `apps/desktop/scripts/windows-directory-installer.mjs:58` to `:66`; `apps/desktop/scripts/windows-sign.mjs:8` | **Resolved.** No installer is built, so there is no installer script and no packaging engine whose internals could be imported |
| `UP-DEBT-found.L14.3` | Windows signing runs on a shared packaging runner where branch code under the same account can reach the token; the isolation risk is accepted, not enforced | `apps/desktop/windows-signing/README.md:27` | **Resolved** by [DH-ADR-ELECTRONBUILDER-2](#dh-adr-electronbuilder-2): signing happens only in the release workflow, in a protected environment that pull-request code cannot run in |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-ELECTRONBUILDER-R1` | A tagged release fails if any binary that must be signed is not signed, timestamped and verified. A build marked unsigned carries that word in its file name and is never uploaded to a release | `electron-builder-config.mjs:123`, `:176`; `apps/desktop/windows-signing/README.md:81` |
| `DH-LIB-ELECTRONBUILDER-R2` | On macOS the `dsh` binary for each architecture is signed with a Developer ID certificate, the hardened runtime and a secure timestamp, and is notarised. A browser-downloaded copy runs on a machine that has never seen it without a "cannot be verified" refusal | `electron-builder-config.mjs:177`, `:182`; `apps/desktop/packaging/README.md:185` |
| `DH-LIB-ELECTRONBUILDER-R3` | Signing credentials are supplied at run time, live in a private location for the run, are removed afterwards, are never passed to build steps that do not sign, and never appear in logs | `apps/desktop/packaging/README.md:185`; `apps/desktop/windows-signing/README.md:67` |
| `DH-LIB-ELECTRONBUILDER-R4` | The set of files to sign is an explicit list derived from the build (one binary per signed target), signing never follows a symbolic link, and a built binary that is missing from the list fails the build | the two patches; `apps/desktop/README.md:108` |
| `DH-LIB-ELECTRONBUILDER-R5` | Building and signing need no publication credential. Publication is a separate step that verifies name, size and digest of every artifact before it uploads | `apps/desktop/publishing/README.md:64` |
| `DH-LIB-ELECTRONBUILDER-R6` | A private-key operation is never retried automatically. A timestamp request may be retried a bounded number of times. A failed run leaves a record that says which step failed | `apps/desktop/windows-signing/README.md:83`, `:89` |
| `DH-LIB-ELECTRONBUILDER-R7` | Installing is unpacking one archive and needs no elevation. `dsh` registers nothing with the operating system: no URL scheme, no uninstall entry, no file association, no start-menu entry. Removing the binary is the whole uninstall and never touches the harness home | Changed from the installer rules at `electron-builder-config.mjs:261` to `:272`; `apps/desktop/README.md:262`, `:270` |
| `DH-LIB-ELECTRONBUILDER-R8` | A released `dsh` reports one version and one commit, read from the Go build information, equal to the release tag | `apps/desktop/README.md:124` |

## 5. Go choice

**Decision.** No packaging library and no installer. The release is produced by
GoReleaser, which this repository already uses, calling the operating systems' signing
tools, under the decisions below.

**Why.** The requirements are about operating-system tools (`codesign`, `notarytool`,
`signtool`) and about discipline (fail closed, separate credentials). A general
packaging engine was the source of both patches and of the internal-module coupling,
and with one executable per platform there is nothing left for it to lay out.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **GoReleaser (free edition) with operating-system signing tools** (chosen) | Already used by this repository (`.goreleaser.yaml`). Cross-compiles six targets on one runner. Signs and notarises macOS command-line binaries from any runner. One pipeline | Signing a Windows executable from that one runner needs a signing command that runs there, or a second job (open, [DH-ADR-ELECTRONBUILDER-2](#dh-adr-electronbuilder-2)) | `github.com/goreleaser/goreleaser/v2` v2.20.2, MIT, released 2026-11-12: `pkg/config/config.go` has `notarize.macos` (sign with a certificate file, notarise with an API key), `signs`, `binary_signs`, `universal_binaries`, `release.draft`, `release.extra_files`, `blobs`; `internal/pipe/notary/macos.go:91` to `:138` signs and notarises only darwin binaries and universal binaries. Read in the module cache on 2026-11-18 |
| GoReleaser's paid edition | May split a release across runners in one tool | Not in the free source read, so nothing could be verified; a licence cost and a closed dependency in the release path | Not verified |
| Sign with Go signing libraries instead of the platform tools | No dependency on `codesign` or `signtool` | A second implementation of Apple's and Microsoft's formats in the release path; the operating system's own assessment still needs the real platform | `github.com/anchore/quill` v0.7.1, Apache-2.0, released 2026-03-10 (GoReleaser's notarise step imports `github.com/goreleaser/quill`, `internal/pipe/notary/macos.go:16`); `github.com/sassoftware/relic/v8` v8.2.0, Apache-2.0, released 2025-01-17. `go list -m -json` and `LICENSE` only; capabilities not read |
| Installers (a macOS package, a Windows setup program) around the binary | Start-menu entry, `PATH` set for the user | Brings back installer scripts, a second signing surface and per-platform runners for one file. Nothing the owner asked for needs it | Not evaluated; the installer templates read in the first version of this document belonged to the desktop shell that is not used ([go-ui-stack.md](go-ui-stack.md#55-considered-and-not-used-a-desktop-shell)) |

<a id="dh-adr-electronbuilder-1"></a>
#### DH-ADR-ELECTRONBUILDER-1 One artifact per platform, one build path, one draft release published only after verification

- **Decision.** A `vX.Y.Z` tag produces the `dsh` archives for six targets from GoReleaser, cross-compiled with cgo off, as [DH-ADR-TOOLCHAIN-13](../upstream/toolchain.md#dh-adr-toolchain-13) says. There is no second artifact and no desktop job. GoReleaser creates the GitHub release as a **draft**; a final job downloads every asset back, verifies it against the signed checksum file and the update manifest, and only then publishes the release.
- **Why.** The owner decided that the product is one binary and a web UI, and that the core binary is pure Go, so one runner can build every target. A draft keeps a half-built release invisible to users and to the update check.
- **Tradeoff.** No installer means the user unpacks an archive and puts `dsh` on `PATH` by hand, or uses a package manager channel once one exists.
- **Failure and retry.** Every job can be re-run: assets are replaced in the draft by name. A published release is never changed; a bad one is superseded by the next patch version. Nothing is published if the final verification fails.
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-ELECTRONBUILDER-1`, `DH-E-ELECTRONBUILDER-1`, `DH-E-ELECTRONBUILDER-2`.

<a id="dh-adr-electronbuilder-2"></a>
#### DH-ADR-ELECTRONBUILDER-2 Signing fails closed, and keys are reachable only from the release workflow

- **Decision.**
  - **macOS (arm64, amd64):** the `dsh` binary is signed with the Developer ID Application certificate and a secure timestamp, and notarised, by GoReleaser's `notarize.macos` step, which sets Apple's timestamp server and takes an optional entitlements file (`internal/pipe/notary/macos.go:108`, `:109`). Inference: notarisation requires the hardened runtime; whether the signing library that step uses turns it on by default was not read, so `R2` states it as a requirement and `DH-E-ELECTRONBUILDER-1` checks it on the result. Inference (platform knowledge, not checked here): a bare executable cannot carry a stapled ticket, so the first run of a downloaded copy needs network access to Apple. No entitlements file is needed: the binary hosts no webview and no script engine.
  - **Windows (amd64, arm64):** `dsh.exe` is signed with SHA-256 and an RFC 3161 timestamp, before it is archived, through GoReleaser's `binary_signs` hook. The private key stays in hardware. **Open for the owner:** a cloud signing service whose client runs on the release runner, or a hardware token on a dedicated self-hosted Windows runner that signs the built executable, as upstream has. No service or tool is named here because none was verified.
  - **Linux:** no platform signature exists. The signed checksum file is the proof.
  - **All platforms:** the checksum file is signed with the release's Ed25519 update key ([electron-updater.md](electron-updater.md#dh-adr-electronupdater-1)), so every archive can be verified without a platform certificate.
  - Signing steps run only in the tag-triggered release workflow, inside a protected environment with required approval. Pull-request code never runs there.
- **Why.** Upstream's rule that a release is signed or does not exist is right. Its mechanism (a USB token on a shared runner, with caches and interlocks to make thousands of signatures bearable) is a consequence of the Electron file tree.
- **Tradeoff.** The project needs an Apple Developer ID and a Windows code-signing certificate; neither can be created by code, and the owner has left where they come from and where keys are kept open as an organisational matter. Until they exist, releases are explicitly unsigned development builds and `R1` is not met: macOS and Windows will warn about, or refuse, a downloaded `dsh`.
- **Failure and retry.** A failed key operation stops the job and is never retried by the workflow (`R6`). A timestamp request is retried at most three times. A notarisation that times out is resubmitted by re-running the job; the earlier submission is harmless. Credentials are removed in a step that runs even on failure; on a hosted runner the machine is discarded anyway.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-ELECTRONBUILDER-2`, `DH-E-ELECTRONBUILDER-1`, `DH-E-ELECTRONBUILDER-2`.

<a id="dh-adr-electronbuilder-3"></a>
#### DH-ADR-ELECTRONBUILDER-3 Distribution format: one archive per target, no installer

- **Decision.**

  | Platform | What is released | How it is installed |
  |---|---|---|
  | macOS arm64, amd64 | `tar.gz` with the signed, notarised binary | Unpack; move `dsh` to a directory on `PATH` |
  | Windows amd64, arm64 | `zip` with the signed `dsh.exe` | Unpack; add the directory to `PATH` |
  | Linux amd64, arm64 | `tar.gz` | Unpack; move `dsh` to a directory on `PATH` |

  Each release also carries one checksum file, its signature and the update manifest. There is no disk image, no setup program, no application bundle, no URL-scheme registration and no uninstaller.
- **Why.** Every installer feature upstream needed served the desktop application: a place in the Applications folder or Start menu, a registered deep link, a webview runtime check. None of them exists now. The differential "blockmap" download (`apps/desktop/publishing/README.md:66`) is dropped with them. Inference: it saves transfer on a release that carries a browser engine and two language runtimes, and is not worth a second artifact format for one executable; the size of the Go binary is not measured.
- **Tradeoff.** No one-click install and no entry in the system's installed-applications list. Package-manager channels (Homebrew, winget, Scoop) are not in the first version; GoReleaser's free edition has configuration for them (`homebrew_casks`, `winget`, `scoops`), so adding them later changes no decision here. A package manager then owns the binary it installed, which `dsh update` must respect ([electron-updater.md](electron-updater.md), `DH-LIB-ELECTRONUPDATER-R9`).
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-E-ELECTRONBUILDER-1`, `DH-E-ELECTRONBUILDER-2`.

**Tradeoff of the whole choice.** The simplest possible release, at the price of a
manual install step and of first-run friction on macOS when the machine is offline.

## 6. Design notes

- **No runtime to download.** `@electron/get` fetched the engine being packaged. The Go release has no runtime prerequisite: the user interface runs in a browser the user already has.
- **What is signed.** macOS: the two `dsh` binaries. Windows: the two `dsh.exe` files. Everything else is covered by the signed checksum file. The list is generated by the build and checked by `DH-T-ELECTRONBUILDER-2`; there is no directory walk.
- **Ownership.** The user owns the place they unpacked `dsh` into. `dsh` owns the harness home and nothing else on the machine, so removing the binary leaves no registration behind and removing the harness home is a separate, explicit act of the user.
- **Half-finished install.** Unpacking either yields the file or does not; there is no state between. A half-written file from an interrupted download fails the checksum the user can verify, and a truncated executable does not start.
- **Version identity.** Version and commit come from the Go build information (`R8`).

<a id="dh-d-electronbuilder-1"></a>
#### DH-D-ELECTRONBUILDER-1 Signed and notarised release binaries

Added after the audit (finding Q1-3, plan problem G-1), so that signing has a deliverable
the plan can schedule.

- **Result.** The published macOS binaries are signed and notarised and the Windows
  binaries are signed; signing keys are reachable only from the protected release
  environment; a signing failure stops the release.
- **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7).
  **Priority.** P1.
- **How.** GoReleaser's signing and notarisation steps over a generated sign list, with
  no retry of a key operation ([DH-ADR-ELECTRONBUILDER-1](#dh-adr-electronbuilder-1) to
  [-3](#dh-adr-electronbuilder-3)).
- **Components and files.** `.goreleaser.yaml`, `.github/workflows/release.yml`, the
  release environment's secrets.
- **Depends on.** [DH-D-TOOLCHAIN-6](../upstream/toolchain.md#dh-d-toolchain-6).
  External: the code-signing certificates and where the keys are kept, still open.
- **Done when.** `DH-T-ELECTRONBUILDER-1` and `DH-T-ELECTRONBUILDER-2` have passed, and
  [DH-E-ELECTRONBUILDER-1](#dh-e-electronbuilder-1) and
  [DH-E-ELECTRONBUILDER-2](#dh-e-electronbuilder-2) have passed.

## 7. Risks and open questions

1. **Certificates.** An Apple Developer ID and a Windows code-signing certificate are prerequisites outside the repository. The owner has left this open as an organisational matter.
2. **Where the Windows key lives, and which command signs** (DH-ADR-ELECTRONBUILDER-2). Owner decision; no service or tool is named here because none was verified.
3. **First run on macOS without a network.** A notarised bare binary has no stapled ticket. Whether that matters enough to ship the macOS binary inside a signed, stapled container (a package or disk image) can only be judged from `DH-E-ELECTRONBUILDER-1`; doing so would reopen DH-ADR-ELECTRONBUILDER-3 for macOS only.
4. **Hardened runtime on a pure-Go binary** was not tried. Inference: nothing in a Go executable needs an entitlement; a notarised build confirms it.
5. **Third-party notices** travel inside the archive: `DH-LIB-NOTICES` in [build-test-toolchain.md](build-test-toolchain.md) generates the file.
6. **For the toolchain (F7):** the release workflow's build, verify and publish jobs and the GoReleaser signing configuration are deliverables of that area.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-ELECTRONBUILDER-1` | local | `DH-LIB-ELECTRONBUILDER-R1`, `DH-LIB-ELECTRONBUILDER-R5`, `DH-LIB-ELECTRONBUILDER-R8` | `goreleaser check` passes on the committed configuration; a test reads the release workflow and fails if the publish step can run without the verification step, or if a signing step lacks the protected environment; the version printed by a snapshot build equals the tag |
| `DH-T-ELECTRONBUILDER-2` | local | `DH-LIB-ELECTRONBUILDER-R3`, `DH-LIB-ELECTRONBUILDER-R4`, `DH-LIB-ELECTRONBUILDER-R6` | With a fake signer: the sign list for a fixture build equals the expected binaries; a symbolic link in the output directory is not followed; a signer failure stops the run with no retry; a timestamp failure is retried at most three times; no credential value appears in captured output |

Real-environment scenarios (not run; a fake signer is not a substitute):

<a id="dh-e-electronbuilder-1"></a>
#### DH-E-ELECTRONBUILDER-1 A signed and notarised macOS `dsh` runs on a clean machine after a browser download

- **Given** a release-candidate tag, the Developer ID certificate and the notarisation API key supplied as run-time secrets to the protected release environment, and a second macOS machine (one per architecture) that has never run `dsh` and has default security settings.
- **When** the release workflow runs to the draft stage; then, on the clean machine, a tester downloads the `dsh` archive through a browser (so the file carries the quarantine attribute), unpacks it and runs `dsh --version` from a terminal with the network connected; then repeats the first run on a second clean machine with the network disconnected.
- **Then** with the network connected `dsh` runs with no warning that it cannot be checked and prints the tag and commit; `codesign --verify --strict` accepts the binary and reports the expected Developer ID, the hardened runtime and a timestamp; the workflow log shows no credential value. The outcome of the disconnected first run is recorded as the answer to open question 3; it does not decide pass or fail. Scope: proves the tested build on the tested macOS versions.
- **Cleanup.** Delete the draft release and the tag if the candidate is rejected; remove the binary from the clean machines. If the check fails, keep the artifacts, the notarisation log and the submission identifier.

<a id="dh-e-electronbuilder-2"></a>
#### DH-E-ELECTRONBUILDER-2 A signed Windows `dsh.exe` runs for a standard user, and removing it leaves the harness home untouched

- **Given** the same release candidate, the Windows signing credential supplied at run time, and a clean Windows machine with a standard (non-administrator) account and an existing harness home that holds one session.
- **When** the tester downloads the `dsh` archive through a browser, unpacks it into a directory in the user's profile, runs `dsh --version`, starts the web UI once, stops it, and then deletes the unpacked directory.
- **Then** the operating system shows the expected publisher and no unknown-publisher warning; no elevation prompt appears; `signtool verify /pa` accepts `dsh.exe` and reports a timestamp; after deletion no registry entry, Start-menu entry or scheme registration created by `dsh` exists, and the harness home and its session are unchanged.
- **Cleanup.** Remove the test account's data; restore the machine snapshot. If anything outside the harness home is left behind, keep a listing of it.

Nothing here has been run.

## 9. Units dispositioned by this spec

- `UP-DEP-electron-builder`, `UP-DEP-app-builder-lib`, `UP-DEP-@electron/notarize`, `UP-DEP-@electron/get`: replaced (`DH-LIB-ELECTRONBUILDER`).
- `UP-DEBT-patch.app-builder-lib@26.17.0`, `UP-DEBT-patch.@electron__osx-sign@1.3.3`, `UP-DEBT-found.L14.2`, `UP-DEBT-found.L14.3`: resolved.
- `UP-DEBT-pin.allowBuilds.electron-winstaller`: not applicable.
