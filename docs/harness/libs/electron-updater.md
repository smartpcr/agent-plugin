# electron-updater (application update): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Nothing here was built, run or tested.
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. There is no desktop
> application, so the only thing to update is the `dsh` binary. The owner decided:
> "ordinary optional update checks only", the mandatory-update service is dropped, and
> everything that leaves the machine is opt-in. The desktop update flow of the first
> version of this document is replaced by a command (`dsh update`) and an optional check
> that only notifies.

| | |
|---|---|
| **Library ID** | `DH-LIB-ELECTRONUPDATER` |
| **Upstream unit** | `UP-DEP-electron-updater` |
| **Upstream version** | 6.8.9 (`apps/desktop/package.json:51`) |
| **Kind** | Runtime, desktop application only |
| **Used by** | `apps/desktop` |
| **Go choice** | **Own code** on the standard library: one signed manifest format and its verifier, an optional update check, and the single-file swap behind `dsh update`. No module |
| **Related areas** | [electron.md](electron.md) (why there is no desktop application), [electron-builder.md](electron-builder.md) (signing and release), [cos-nodejs-sdk-v5.md](cos-nodejs-sdk-v5.md) (where releases are hosted). Owning areas: command line (A24) for `dsh update`; web host and web UI (A18, A25) for the optional check and its notice |

## 1. What it is and why upstream needs it

An installed desktop application must be able to replace itself with a newer release
without the user finding a download page. `electron-updater` is the library that does
this for Electron: it reads a small **feed** file from a web server (the latest version
and the digest of its installer), downloads the installer, checks it, and hands over to
a platform installer when the application quits.

Upstream does not use the library's automatic mode. It wraps it in its own
**coordinator** (`apps/desktop/src/update-coordinator.ts:18`), because three product
rules matter more than the download: the user authorises the download, the user
separately authorises the restart, and a restart that would interrupt running agents
must say so first (`apps/desktop/README.md:290`).

The Go product has one thing to update: the `dsh` binary, which upstream distributes
through npm and therefore never updates itself. Two questions remain from upstream's
feature. How does a user **learn** that a release exists, and how is it **applied**?
The answers are an optional check that shows a notice, and a command the user runs.

Terms. The **manifest** is a small signed file that names the newest release of a
channel and the digest of each platform's archive. A **channel** is a named stream of
releases (`stable`, `nightly`). The **swap** is the replacement of the binary file on
disk.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `apps/desktop/src/update-coordinator.ts:79`, `:80` | Turns off automatic download and install-on-quit | `autoDownload`, `autoInstallOnAppQuit` |
| `apps/desktop/src/update-coordinator.ts:273` | Check the feed | `checkForUpdates` |
| `apps/desktop/src/update-coordinator.ts:186` | Download after the user asked, cancellable | `downloadUpdate(cancellationToken)` |
| `apps/desktop/src/update-coordinator.ts:82` to `:84` | Progress, completion and error reporting | events `download-progress`, `update-downloaded`, `error` |
| `apps/desktop/src/update-coordinator.ts:218` | Hand over to the installer and restart | `quitAndInstall(true, true)` |
| `apps/desktop/src/updates/electron-updater/channel.ts:10` to `:13` | Select the `stable` or `nightly` feed; never downgrade | `channel`, `allowDowngrade`, `allowPrerelease` |
| `apps/desktop/src/updates/electron-updater/http-executor.ts:2` | Replace the HTTP transport to add an inactivity deadline | Internal module `electron-updater/out/electronHttpExecutor.js` |
| `apps/desktop/src/updates/electron-updater/discard.ts:17` | Forget a downloaded release when the channel changes | Private fields of the updater object |
| `apps/desktop/scripts/installed-update-signature.mjs:54` | Qualify that Windows refuses an installer from another publisher | `NsisUpdater` |
| `apps/desktop/scripts/electron-builder-config.mjs:274` | The feed address compiled into the build | `publish: generic` |

Behaviour of upstream's coordinator and release process, each judged:

| Upstream behaviour | Evidence | Verdict |
|---|---|---|
| Checks at start, then every ten minutes with 20% jitter; failures double the delay up to one hour; a manual check runs now and joins a check in flight. An automatic check never downloads and never opens a dialog | `apps/desktop/README.md:286`, `:288` | **Change**: the automatic check is optional and off until the operator turns it on, runs at most daily, and still never downloads (`R2`) |
| The user starts the download; the application stays usable; the result is verified (SHA-512) before it is offered | `apps/desktop/README.md:290`, `:371` | **Keep** as `dsh update` (`R3`, `R4`) |
| Restart needs a second approval, warns when agents, queued input or jobs would be interrupted, then locks new requests, drains, re-checks, and refuses to install if work cannot be stopped cleanly | `apps/desktop/README.md:290` | **Change**: the update never stops or restarts a running host (`R5`). Stopping the host keeps its own question ([electron.md](electron.md), `DH-LIB-ELECTRON-R3`) |
| A failed install restores the current version's host and keeps the download for a retry | `apps/desktop/README.md:292` | **Keep** the restore (`R6`) |
| Two channels, chosen in settings; switching retires any pending download and never downgrades | `apps/desktop/README.md:316`, `:318` | **Keep** the channels and the no-downgrade rule (`R7`); no download is ever kept pending |
| Windows accepts only an installer whose certificate names the expected publisher; the expectation is in the installed application, not in the feed | `apps/desktop/windows-signing/README.md:69` | **Keep and generalise** (`R1`) |
| A connection silent for 60 seconds fails; there is no total deadline | `apps/desktop/README.md:363` | **Keep** (`R3`) |
| A staged rollout: the feed carries a percentage, raised in steps by a publication workflow | `apps/desktop/publishing/ci/README.md:72`, `:86` | **Keep as a manifest field** ([DH-ADR-ELECTRONUPDATER-4](#dh-adr-electronupdater-4)) |
| A mandatory-update policy fetched from the vendor's own service can block the application until it updates | `apps/desktop/README.md:330` to `:359` | **Dropped by the owner** (DH-ADR-ELECTRONUPDATER-4) |
| Update dialogs and a blocking overlay inside the application | `apps/desktop/README.md:158`, `:282`, `:353` | **Change**: one notice in the web UI ([DH-ADR-ELECTRONUPDATER-2](#dh-adr-electronupdater-2)) |
| Differential downloads through a block map | `apps/desktop/publishing/README.md:66` | **Drop** ([electron-builder.md](electron-builder.md#dh-adr-electronbuilder-3)) |

## 3. Tech debt upstream carries

Search: `patches/` (no patch for this package), `pnpm-workspace.yaml` (no pin), the
adapter directory `apps/desktop/src/updates/electron-updater/`, and
`apps/desktop/README.md` "Known limitations".

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L14.1` | The adapter imports an internal module of the library and reads and clears its private fields; the code says so and relies on tests against the pinned version | `apps/desktop/src/updates/electron-updater/discard.ts:11`, `:17`; `apps/desktop/src/updates/electron-updater/http-executor.ts:2`; `apps/desktop/src/update-coordinator.ts:71` | **Resolved.** There is no update library. The `dsh update` command owns the download and its inactivity deadline, and no downloaded release is kept between runs, so there is no hidden state to clear |
| (context) | Update hosting and installed-update qualification "require the production release environment" | `apps/desktop/README.md:389` | The manifest is a file beside the release assets; the scenarios in section 8 run against a private copy |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-ELECTRONUPDATER-R1` | An update is installed only if (a) the manifest's signature verifies under the public key compiled into the running binary, (b) the archive's digest equals the manifest's, (c) its version is higher than the running one, and (d) on macOS and Windows the new binary's platform signature names the same publisher as the running one. Any failure deletes the download and installs nothing | section 2; `apps/desktop/windows-signing/README.md:69` |
| `DH-LIB-ELECTRONUPDATER-R2` | The automatic update check is a visible setting that is **off until the operator turns it on**. When on, only a long-running web host checks: once after start and then at most daily, with jitter and exponential back-off on failure. A check fetches the manifest and its signature and nothing else; it never downloads an archive. With the setting off, and in every other profile (headless, SDK, ACP), `dsh` contacts no release server unless the user runs `dsh update`. The automatic check is a sender (kind `update-check`): the one off-switch stops it. An explicit `dsh update` is something the user asked for: it runs whatever the off-switch says, and names the address before it connects | `apps/desktop/README.md:286`; owner decisions "optional update checks only", [DH-ADR-32](../design.md#dh-adr-32) (the check counts as data leaving the machine and is off until turned on) and [DH-ADR-27](../design.md#dh-adr-27) (the off-switch stops senders only) |
| `DH-LIB-ELECTRONUPDATER-R3` | A download starts only when the user runs `dsh update`. It reports progress, can be cancelled, and fails after a configurable silence without a total deadline | `apps/desktop/README.md:290`, `:363` |
| `DH-LIB-ELECTRONUPDATER-R4` | `dsh update` shows the running and the new version and asks before it replaces the binary; a flag answers yes for scripts. No other code path replaces the binary | `apps/desktop/README.md:290` |
| `DH-LIB-ELECTRONUPDATER-R5` | `dsh update` never stops, drains or restarts a running host. A host that was started from the replaced file keeps running the old version, and its UI says that a restart finishes the update. The swap records that fact in the harness home, and the host's `update.Status` carries it as `RestartPending` for the notice ([../areas/app-cli.md](../areas/app-cli.md) §6.1) | Changed from `apps/desktop/README.md:290`, `:292` |
| `DH-LIB-ELECTRONUPDATER-R6` | If the swap fails, or the new binary does not report the expected version when the command runs it once, the previous file is put back and the failure is shown. The previous file is kept beside the new one until the new version has started once | `apps/desktop/README.md:292` |
| `DH-LIB-ELECTRONUPDATER-R7` | The channel is a setting and a flag of `dsh update`. A channel change never offers a version lower than the running one | `apps/desktop/README.md:318` |
| `DH-LIB-ELECTRONUPDATER-R8` | `dsh update --check` reports and changes nothing. `dsh update` replaces the running binary under the checks of R1 | [DH-ADR-ELECTRONUPDATER-3](#dh-adr-electronupdater-3) |
| `DH-LIB-ELECTRONUPDATER-R9` | The updater replaces only the file the running process was started from, and only when it owns it: `dsh update` refuses when the file is not writable or sits where a package manager installed it, and says how that installation is updated | `apps/desktop/README.md:70`, `:127` |
| `DH-LIB-ELECTRONUPDATER-R10` | Update never touches the harness home. A database layout change is applied by the new version at its first start, after the automatic backup the storage design requires | [../design.md](../design.md) section 5 |

## 5. Go choice

**Decision.** Own code: a manifest verifier, an optional check that produces a notice,
and a single-file swap for `dsh`.

**Why.** The valuable part of upstream's update code is its coordinator, which is
already own code, and most of it existed to stop a desktop application safely. What the
library adds (fetch a feed, compare a digest, replace files) is small in Go, and the
trust rule in R1 is the project's own.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Own verifier, optional check and single-file swap on the standard library** (chosen) | No dependency in the pure-Go core binary. Uses `crypto/ed25519`, `crypto/sha256`, `net/http`, `archive/zip`, `archive/tar`, `os` | The file swap on Windows is subtle; the tests in section 8 carry that weight | Standard library of Go 1.27.4 (the installed toolchain); not a module |
| `github.com/creativeprojects/go-selfupdate` | Maintained; checksum, ECDSA and PGP validators; several release sources | Its direct requirements include GitHub, GitLab and Gitea client libraries, an OpenPGP library and an `xz` library: a large graph for one command in the core binary. No Ed25519 validator in `validate.go` | v1.7.1, MIT, released 2026-11-10; `go.mod` and `validate.go` read |
| `github.com/minio/selfupdate` for the swap | Small; does the rename, rollback and hidden old file on Windows; verifies a minisign signature | Last tagged release is four years old; pins an old `aead.dev/minisign` | v0.6.0, Apache-2.0, released 2022-10-19; `go.mod` and `apply.go` read |
| No updater at all; the user downloads each release by hand | Nothing to build | Leaves every installation on the version it started with, and the verification of R1 to the user | n/a |

The first version of this document also weighed an update engine that ships inside the
desktop shell module. It swaps application bundles and is reachable only from a shell
window, so it has no use here; see [go-ui-stack.md](go-ui-stack.md#55-considered-and-not-used-a-desktop-shell).

<a id="dh-adr-electronupdater-1"></a>
#### DH-ADR-ELECTRONUPDATER-1 One signed manifest and one compiled-in key anchor every update

- **Decision.** Each channel has one small JSON manifest: version, publication time, and per platform a file name, size and SHA-256 digest, plus the optional fields of DH-ADR-ELECTRONUPDATER-4. The release workflow signs the manifest and the checksum file with an Ed25519 key that exists only in the protected release environment. The matching public key is compiled into every binary. An updater trusts nothing else: not the server, not TLS alone, not a key named in the manifest.
- **Why.** The release host is a third party. A pinned key means a compromised or impersonated host can withhold updates but cannot install one. The platform-signature check in R1 (d) is a second, independent lock on macOS and Windows.
- **Tradeoff.** Losing the private key ends the ability to update existing installations; rotating it needs a release signed by the old key that carries the new one. The manifest is a new file the release must produce.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-ELECTRONUPDATER-1`, `DH-E-ELECTRONUPDATER-1`, `DH-E-ELECTRONUPDATER-2`.

<a id="dh-adr-electronupdater-2"></a>
#### DH-ADR-ELECTRONUPDATER-2 The web UI reports an available update and never installs one

- **Decision.** When the optional check finds a higher version, the host records it and the web UI shows one notice: the new version, a link to the release notes, and the command that installs it (`dsh update`). The UI has a "check now" action in settings. It has no download button, no install button and no restart button. Installing is always the command of [DH-ADR-ELECTRONUPDATER-3](#dh-adr-electronupdater-3).
- **Why.** The owner asked for ordinary optional checks only. An install started from a page would have to replace the file of the process that serves the page and then restart it, which is the whole stop-drain-swap-relaunch machine upstream needed for its desktop application. Keeping the swap in a command the user runs in a terminal removes that machine, and keeps a remote browser peer from ever triggering a change to the binary on the host machine.
- **Tradeoff.** Updating takes two steps the user performs by hand: run the command, then restart the host. A recorded change from upstream's in-application flow.
- **Failure.** A check that fails is logged, retried with back-off and shows nothing. A manifest that fails verification is treated as "no update" and logged with the reason; it never produces a notice.
- **Serves.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-ELECTRONUPDATER-2`, `DH-T-ELECTRONUPDATER-3`, `DH-E-ELECTRONUPDATER-1`.

<a id="dh-adr-electronupdater-3"></a>
#### DH-ADR-ELECTRONUPDATER-3 The binary is replaced only by `dsh update`, and nothing checks unless asked or opted in

- **Decision.** `dsh update` checks and, after confirmation, replaces the binary; `dsh update --check` only reports. The automatic check of `R2` exists only in a long-running web host and only when the operator has turned it on. No other command or profile contacts the release server.
- **Why.** `dsh` is also the headless runner, the SDK server and the remote helper. A process that a script started must behave the same tomorrow as today and must not make network calls nobody asked for. The owner's rule that everything leaving the machine is opt-in covers a request that tells a third-party host the installation exists. Inference: the owner's list names session-log upload, analytics and telemetry; applying the same default to the update check is this document's reading and is listed for confirmation in section 7.
- **Tradeoff.** With the default setting a user learns about a release only by asking. An installation that never asks stays on its version, with whatever defects it has.
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-ELECTRONUPDATER-2`, `DH-T-ELECTRONUPDATER-4`, `DH-E-ELECTRONUPDATER-2`.

<a id="dh-adr-electronupdater-4"></a>
#### DH-ADR-ELECTRONUPDATER-4 Staged rollout is a manifest field; the mandatory-update service is not built

- **Decision.** Confirmed by the owner on 2026-11-18: the mandatory-update service is dropped, and update checks are ordinary and optional. What remains is advisory. The manifest may carry `rollout` (0 to 100): an installation shows the notice of an automatic check only if a stable random number it drew once and stored in its own settings falls below the percentage; `dsh update` and "check now" ignore the percentage. The manifest may carry `minimumVersion`: an older installation that checks shows a notice that it is no longer supported. It is never blocked.
- **Why.** Staged rollout is a cheap, client-side safety valve. Upstream's blocking policy depends on a vendor service with its own authentication (`apps/desktop/README.md:336`, `:338`), and it gives the publisher a remote switch over a local tool; this project has no such service and no reason to hold that switch.
- **Tradeoff.** A recorded exception to [DH-ADR-1](../design.md#dh-adr-1): there is no way to force an installation off a version with a known defect, and with the check off by default most installations will not even see the advisory notice. The release notes are the only lever.
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-ELECTRONUPDATER-2`.

**Tradeoff of the whole choice.** The project owns security-relevant code that a library
would otherwise own. It is small, and its inputs (manifest, signature, archive) are
boundary parsers that get fuzz targets under the toolchain rules.

## 6. Design notes

**Shape.** An `update` package with no dependency on the web host:

- `Manifest` and `Verify(manifest, signature, publicKey)`: pure functions.
- `Source`: fetches the manifest, its signature and an archive stream for a channel. The default reads release assets over HTTPS; a test or a private mirror supplies another. The address is configuration ([cos-nodejs-sdk-v5.md](cos-nodejs-sdk-v5.md#dh-adr-cos-1)).
- `Checker`: the optional schedule. It runs as a component of the web host only, holds the last verified result in memory, and publishes a change as a live event the UI renders as the notice.
- `Swap(archive) error`: used only by the `dsh update` command.

**Single-file swap.** Unpack the new binary beside the old one, flush it, rename the old
one aside, rename the new one into place, run the new one once with `--version`, and
delete the old one at the next start of the new version (a running executable cannot be
deleted on Windows, but it can be renamed). Every rename is within one directory, so
each is atomic.

**Ownership, failure and cleanup.**

| Situation | What happens | What is undone; what stays uncertain |
|---|---|---|
| The automatic check cannot reach the release host | Logged; the next attempt waits longer, up to a cap | Nothing changed; no notice |
| The download is cut off, cancelled or silent too long | The partial file is deleted; the command exits with an error | Nothing else changed |
| The signature, digest, version or publisher check fails | The file is deleted; nothing is installed | Nothing else changed |
| The swap fails half-way | The command renames the previous file back and reports the failure | **Uncertain:** if the command itself is killed between its two renames, no binary exists at the original path. The previous version is still beside it under a known name; the user renames it back or downloads a release |
| The new binary does not report the expected version | The command restores the previous file | Nothing else changed: the new binary was run only with `--version`, which opens no harness home |
| A host is running from the replaced file | It keeps running the old version and shows "restart to finish updating" | **Uncertain:** nothing. The old code is in memory; the session log is untouched |
| The new version changes the database layout at its first start | The storage design's automatic backup is taken first | **Uncertain:** going back to the previous version afterwards means restoring that backup ([../design.md](../design.md) section 5) |
| `dsh update` is run on a binary it does not own | Refused with a message naming the reason (read-only location, package-manager directory) | Nothing |

The updater owns what it created: the downloaded archive, deleted when the command
ends, and the "previous" copy, removed after the new version has started once. It has
access to, but does not own, a binary a package manager installed, and never replaces
it.

## 7. Risks and open questions

1. **The update key** needs an owner and a storage decision (DH-ADR-ELECTRONUPDATER-1). The owner has left signing keys open as an organisational matter.
2. **Default of the automatic check.** Decided by the owner: the check counts as data leaving the machine and is off until turned on ([DH-ADR-32](../design.md#dh-adr-32)), which confirms DH-ADR-ELECTRONUPDATER-3. An explicit `dsh update` still runs with the off-switch set and names the address first ([DH-ADR-27](../design.md#dh-adr-27)).
3. **Manifest location for the pre-release channel.** A hosting service's "latest release" address usually excludes pre-releases, so the nightly manifest needs a fixed address; see [cos-nodejs-sdk-v5.md](cos-nodejs-sdk-v5.md).
4. **Recognising a package-manager installation** (`R9`) is a list of path patterns per platform that must be kept current; a wrong guess either refuses a legitimate update or replaces a file another tool owns. The second is the worse error, so an unwritable or unrecognised location is refused.
5. **Quarantine attributes on a swapped macOS binary**, and **whether the publisher check can be done without spawning a tool on Windows**, can only be settled on real machines.
6. **For the command-line area (A24) and the web host and UI (A18, A25):** the `update` package, the `dsh update` command, the settings for the check and the channel, and the notice are deliverables of those areas.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-ELECTRONUPDATER-1` | local | `DH-LIB-ELECTRONUPDATER-R1` | Table and fuzz tests of the verifier: a valid manifest; one changed byte in manifest, signature or archive; a signature by another key; an equal or lower version; a missing platform entry. Each failure leaves no downloaded file |
| `DH-T-ELECTRONUPDATER-2` | local | `DH-LIB-ELECTRONUPDATER-R2`, `DH-LIB-ELECTRONUPDATER-R7` | Under a fake clock and a recording transport: with the setting off no request is made; in the headless, SDK and ACP compositions no request is made with the setting on; in the web composition the schedule keeps its jitter bounds and back-off cap, a manual check joins a check in flight, and a check requests only the manifest and its signature; a channel switch never offers a lower version; the rollout percentage is honoured by automatic checks and ignored by a manual one |
| `DH-T-ELECTRONUPDATER-3` | local | `DH-LIB-ELECTRONUPDATER-R3`, `DH-LIB-ELECTRONUPDATER-R4`, `DH-LIB-ELECTRONUPDATER-R5` | Against a local test server: no archive is requested by a check; `dsh update` without confirmation replaces nothing; cancel removes the partial file; a stalled response fails after the silence limit; no HTTP route of the host can start a download or a swap; a fixture host started from the replaced file keeps answering and reports that a restart is pending |
| `DH-T-ELECTRONUPDATER-4` | local | `DH-LIB-ELECTRONUPDATER-R6`, `DH-LIB-ELECTRONUPDATER-R8`, `DH-LIB-ELECTRONUPDATER-R9`, `DH-LIB-ELECTRONUPDATER-R10` | In a temporary directory with stand-in files: the swap leaves exactly the new file and the kept previous one; an injected failure at each step restores the old one; a read-only directory and a package-manager path are refused; no file under a stand-in harness home is opened; `--check` changes nothing |

Real-environment scenarios (not run; a local server with stand-in files is not a substitute):

<a id="dh-e-electronupdater-1"></a>
#### DH-E-ELECTRONUPDATER-1 The web host's optional check: silent when off, a notice when on, and no notice for a tampered or foreign-signed manifest

- **Given** two signed release candidates, N and N+1, built by the release workflow with a test update key; a private feed (a release or static directory only the testers can reach) holding N+1 and its manifest, with request logging; a machine running the web host of N from a temporary harness home, configured to use the private feed, with the automatic check at its default (off); a real browser on the UI.
- **When** the host runs for longer than one check interval; the tester then turns the check on in settings and waits; then repeats with (a) the manifest replaced by one signed with a different key and (b) one byte of the manifest changed.
- **Then** with the check off the feed's request log shows no request from the host; after it is turned on the log shows requests for the manifest and its signature only, the UI shows the notice naming N+1 and the `dsh update` command, and the host still reports N; in runs (a) and (b) no notice appears and the host log names the verification failure. Scope: proves these two builds on the tested operating system and browser.
- **Cleanup.** Stop the host, delete the temporary home, the private feed and the test key. On failure keep the host log and the feed's request log.

<a id="dh-e-electronupdater-2"></a>
#### DH-E-ELECTRONUPDATER-2 `dsh update` replaces the binary on three operating systems and refuses a tampered or unowned one

- **Given** hosted Linux, macOS and Windows runners; `dsh` N unpacked from its release archive into a writable directory; the same private feed and test key as above; a second copy of `dsh` N placed in a read-only directory; a web host of N running from the writable copy.
- **When** the controller runs `dsh update --check`, then `dsh update`, then `dsh --version`; asks the running host for its version and status; repeats against a feed whose archive was altered; and runs `dsh update` from the read-only copy.
- **Then** `--check` reports N+1 and changes nothing; `update` leaves a binary that reports N+1 and passes the platform signature check on macOS and Windows; the running host still answers, reports N and says a restart is pending; the altered archive is refused and the binary still reports its version; the read-only copy is refused with a message that names the reason; no run writes outside the binary's directory and the temporary directory.
- **Cleanup.** Stop the host, remove the directories and the feed. On failure keep the command's output and a directory listing.

Nothing here has been run.

## 9. Units dispositioned by this spec

- `UP-DEP-electron-updater`: replaced (`DH-LIB-ELECTRONUPDATER`).
- `UP-DEBT-found.L14.1`: resolved.
