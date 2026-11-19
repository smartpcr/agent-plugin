# semver: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". No harness code exists.
>
> **Unverified research notes.** Where this document says "probe", it reports an
> observation made during library research on 2026-11-18 by running a throwaway program
> against the candidate module outside this repository. That was against the project's
> rule that nothing is built or run in the design task, and the program was not kept, so
> the observation cannot be repeated from this repository. Every such statement is
> therefore **unverified and to be measured** by `DH-T-SEMVER-1` to `-3` (section 8); if
> a test contradicts an observation, the choice is reopened.

| | |
|---|---|
| **Library ID** | `DH-LIB-SEMVER` |
| **Upstream unit** | `UP-DEP-semver` |
| **Upstream version** | 7.8.5 (`pnpm-lock.yaml`) |
| **Kind** | runtime; also used by build and release scripts |
| **Used by** | apps, boot, llm, util; root scripts |
| **Go choice** | named Go module `github.com/Masterminds/semver/v3`, behind one harness helper |
| **Related areas** | A21, boot (plugin compatibility; plugins are installed from local directories and not updated by the harness, [DH-ADR-13](../design.md#dh-adr-13)); A04, the model provider (catalog gate); A24, the update check of the command line ([../areas/app-cli.md](../areas/app-cli.md)); release tooling ([../upstream/toolchain.md](../upstream/toolchain.md)) |

## 1. What it is and why upstream needs it

**Semantic Versioning** (SemVer) is the convention `MAJOR.MINOR.PATCH`, optionally
followed by a *prerelease* (`-rc.3`, which sorts before the release) and *build metadata*
(`+build.5`, which does not affect order). A **range** such as `>=1.2.0 <2.0.0` or `^1.2.0`
names a set of versions. `semver` is the npm library that validates, compares and sorts
versions and tests a version against a range.

The harness asks four questions with it: is this text a version at all; which of two
versions is newer; is this version a prerelease; and does the running harness fall inside
the range a plugin or a model listing says it needs.

## 2. How upstream uses it

| Use site (`path:line`) | Question asked | API used | In Go |
|---|---|---|---|
| `packages/boot/app-boot/src/plugin-compatibility.ts:25`, `:52` | Does the running harness satisfy the range a plugin declares (`engines.dsh`, or its harness peer dependencies)? Prereleases of the harness count. | `valid`, `satisfies` with `includePrerelease: true` | kept: a plugin manifest states the harness range it supports (R4). Peer dependencies are npm-only and go. |
| `packages/boot/app-boot/src/profile-compatibility.ts:24-27` | Is an exemption written for one exact version, spelled canonically, build metadata included? | `parse` | kept (R1) |
| `packages/boot/plugin-manager/src/versions.ts:46-54`, `packages/boot/plugin-manager/src/index.ts:196`, `:760`, `packages/boot/plugin-manager/src/bundle-detail.ts:48` | Which published versions are newer than the installed one, newest first, and which are prereleases? | `valid`, `gt`, `rcompare`, `prerelease` | kept (R2, R3), over whatever plugin source [OD-7](../design.md#9-open-decisions-for-the-owner) selects |
| `packages/boot/plugin-market/src/index.ts:517-541` | Is a listed version an update? Which version is the default: the newest compatible stable one, or a prerelease when nothing stable exists? | `gt`, `prerelease`, `valid` | the functions are kept (R2, R3); their caller is not built yet: the harness downloads and updates no plugin, and a registry is deferred ([DH-ADR-13](../design.md#dh-adr-13)) |
| `packages/util/plugin-market-index/src/index.ts:216`, `:269` | Is a range in a market index well formed? Sort a plugin's versions newest first. | `validRange`, `rcompare` | kept (R5, R2) for the harness version range in a local plugin's manifest (area A22); there is no market index until a registry is designed ([DH-ADR-13](../design.md#dh-adr-13)) |
| `packages/llm/llm-deepseek/src/listed-catalog.ts:82-83` | A model listing says "needs client version N or later". Is this client new enough? If not, the model is refused as "upgrade required". | `valid`, `gte` | kept (R6); the provider area owns the gate |
| `apps/desktop/src/update-coordinator.ts:278-279`, `apps/desktop/src/mandatory-update-policy.ts:156` | Is the update feed's version valid and newer than the running one? | `valid`, `gt` | kept (R1, R2) for the optional update check and `dsh update`, which area A24 owns (`internal/update`). The mandatory-update policy is dropped ([DH-ADR-24](../design.md#dh-adr-24)) and there is no desktop updater ([DH-ADR-18](../design.md#dh-adr-18)) |
| `apps/desktop-publisher/src/desktop-publish.ts:69`, `:100` | Refuse to publish over a newer version | `valid`, `gt` | not carried: the vendor release publisher is dropped ([DH-ADR-24](../design.md#dh-adr-24)); releases are GitHub release assets created by the release workflow, where a tag cannot be published twice |
| `apps/desktop/src/release.ts:23-25`, `apps/desktop/src/runtime-tree.ts:157`, `:188` | Validate the Node and pnpm versions and the npm package versions bundled in the desktop application | `valid` | not applicable: no Node runtime or npm tree is bundled |
| `apps/plugin-market-generator/src/packages.ts:77`, `:101-109`, `apps/plugin-market-generator/src/generate.ts:87`, `:208` | Combine a plugin's harness peer ranges into one range, by taking one alternative from each range and dropping combinations no version satisfies | `validRange`, `Range`, `minVersion`, `rcompare` | sorting kept; the range combination is not applicable: it exists only to turn npm peer dependencies into one range |
| `scripts/release/publish.ts:22`, `scripts/stage-dsh-docs.ts:7`, `scripts/doc-typecheck-paths.ts:1`, `scripts/local-bundle-registry.ts:22`, five files under `apps/desktop/scripts/` | Release and build scripts | `valid`, `validRange`, `lt`, `gt`, `rcompare`, `parse` | Go release tooling uses the same helper |

## 3. Tech debt upstream carries

Search performed: every import of `semver`; `patches/` and `pnpm-workspace.yaml` (no
patch or pin); the call sites above.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L1.3` | **"Valid version" has two meanings.** Some call sites test `valid(v) !== null`, others `valid(v) === v`, in the same package. Inference from the library's documented behaviour: the first form accepts loose spellings such as `v1.2.3` and `=1.2.3` and continues with the cleaned value, the second accepts only the canonical spelling. A version can therefore pass one check and fail the next. | `packages/boot/plugin-manager/src/index.ts:196` against `packages/boot/plugin-manager/src/bundle-detail.ts:48`; `apps/plugin-market-generator/src/generate.ts:208` | Resolved by [DH-ADR-SEMVER-1](#dh-adr-semver-1): one parse function that accepts only the strict spelling. |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-SEMVER-R1` | A version is accepted only in strict SemVer 2.0.0 spelling: three numbers without leading zeros, optional prerelease, optional build metadata, no `v` prefix. The accepted text is kept exactly. "Exact version" checks compare the whole text, build metadata included. | `packages/boot/app-boot/src/profile-compatibility.ts:24-27`; `UP-DEBT-found.L1.3` |
| `DH-LIB-SEMVER-R2` | Versions are ordered by SemVer precedence: a prerelease is older than its release, and build metadata is ignored. Lists sort newest first with one comparison function. | `packages/boot/plugin-manager/src/versions.ts:46-47` |
| `DH-LIB-SEMVER-R3` | A version can be asked whether it is a prerelease, so that the default choice among several is the newest stable one. | `packages/boot/plugin-market/src/index.ts:522-523` |
| `DH-LIB-SEMVER-R4` | A range can be written with comparison operators, `^`, `~`, `x` wildcards, hyphen ranges, a space for "and" and `\|\|` for "or". Testing the running harness against a range counts prereleases: `0.3.0-rc.1` satisfies `>=0.2.0`. An empty range satisfies nothing. | `packages/boot/app-boot/src/plugin-compatibility.ts:51-53` |
| `DH-LIB-SEMVER-R5` | A malformed version or range is rejected where it is read (a manifest, an index, a listing), with the field name, not when it is first compared. | `packages/util/plugin-market-index/src/index.ts:216` |
| `DH-LIB-SEMVER-R6` | "At least version N" is one call. An unreadable requirement never counts as satisfied. | `packages/llm/llm-deepseek/src/listed-catalog.ts:82-83` |

## 5. Go choice

**Decision.** `github.com/Masterminds/semver/v3` at v3.5.0, called only through one
harness helper.

**Why.** R4 needs ranges in the syntax people already write for npm, with prereleases
included on request. The module has both, a strict parser (R1), and no dependencies.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **`github.com/Masterminds/semver/v3` (chosen)** | `StrictNewVersion` for R1; `Compare`, `Prerelease`, `Metadata`, `Original`; ranges with `^`, `~`, `x`, hyphen, space and comma for "and", `\|\|`; `Constraints.IncludePrerelease` (`constraints.go:17-21`) | Its default parser `NewVersion` is loose (accepts `v1.2.3`, `1.2`, `01.2.3`), so the helper must never call it. Ranges exclude prereleases unless the flag is set. It also accepts `!=` and a comma, which npm ranges do not have. | v3.5.0, released 2026-04-30, default branch last commit 2026-08-14, MIT (`LICENSE.txt`), `go.mod` has no requirements, pure Go. Checked 2026-11-18 with `go list -m -json`, `go mod download -json`, and the licence file in the module cache; the probe's behaviour notes are unverified. |
| `golang.org/x/mod/semver` | Maintained by the Go project; tiny | Requires a `v` prefix on every version and accepts the short forms `v1` and `v1.2` (`semver/semver.go:6-22`); has no ranges at all, so R4 would be own code | module `golang.org/x/mod` v0.43.0, released 2026-11-09, BSD-3-Clause. Same check, no probe. |
| Own code | No dependency; parse and compare are about 80 lines | The range grammar (`^`, `~`, wildcards, hyphen, "or") and its prerelease rules are where the bugs are | n/a |
| `github.com/blang/semver/v4` | Not evaluated beyond its dates | Last release 2020-05-24 | v4.0.0. Version and date checked with `go list -m -json`; licence and API not read, so it is not a candidate. |

**What the probe was reported to show (unverified, to be measured by `DH-T-SEMVER-1`).**

| Input | Result |
|---|---|
| `StrictNewVersion` on `1.2.3`, `1.2.3-rc.1+build.5` | accepted |
| `StrictNewVersion` on `v1.2.3`, `1.2`, `01.2.3`, `=1.2.3` | rejected |
| `NewVersion` (loose) on `v1.2.3`, `1.2`, `01.2.3` | accepted as `1.2.3`, `1.2.0`, `1.2.3` |
| `^0.2.3` against `0.2.9`, `0.3.0` | true, false |
| `>=1.0.0 <2.0.0` against `1.5.0-rc.1` | false by default, true with `IncludePrerelease` |
| `*` against `3.0.0-beta.1` | false by default, true with `IncludePrerelease` |
| `^1.2.0 \|\| ^2.0.0`, `1.x`, `~1.2`, `1.2 - 1.4.5` | parsed and matched as in npm |

**Tradeoff.** A range is not guaranteed to mean exactly what npm's library says for every
corner (for example npm's own rule about which prereleases a range admits when the flag
is off). The harness always sets the flag, and plugin ranges are written for this harness,
not carried over from npm manifests.

<a id="dh-adr-semver-1"></a>
### DH-ADR-SEMVER-1 One strict version type and one range dialect

- **Decision.** A helper package wraps the module: `Parse` (strict), `Compare`,
  `IsPrerelease`, `ParseRange` (prereleases always included) and `Satisfies`. No other
  package imports the module. Versions travel as the helper's type, not as strings.
- **Why.** `UP-DEBT-found.L1.3`, and the module's loose default parser. Verdict on
  upstream: keep SemVer for harness, plugin and model-listing versions; drop loose
  spellings and the peer-dependency range arithmetic.
- **Tradeoff.** A plugin author who tags `v1.2.3` must publish the manifest version as
  `1.2.3`. The error says so.
- **Serves.** DH-O-11, DH-O-8. **Verified by.** DH-T-SEMVER-1, DH-T-SEMVER-2, DH-T-SEMVER-3.

## 6. Design notes

```go
// package version (proposed: internal/util/version)
func Parse(s string) (Version, error)      // strict; Version.String() returns s unchanged
func (v Version) Compare(o Version) int
func (v Version) IsPrerelease() bool
func ParseRange(s string) (Range, error)   // "" is an error
func (r Range) Satisfies(v Version) bool   // prereleases included
func AtLeast(current, required Version) bool
```

The harness's own version is a build-time constant validated by `Parse` in a test, so the
running binary cannot carry a version its own checks reject.

Inference: for external plugins the first compatibility check is the **protocol version**
exchanged in the plugin handshake ([DH-ADR-WIRE-9](../upstream/wire-and-api.md#dh-adr-wire-9)).
The harness-version range in a manifest is the earlier, cheaper check that lets the
harness refuse to start, or to offer for installation, a plugin that says it needs a newer
host. Upstream's per-profile exemption for one exact plugin version and harness version
(`packages/boot/app-boot/src/profile-compatibility.ts:29-40`) is boot policy built on R1.

Failure and ownership: values only; nothing to release. A malformed version in one
plugin's manifest is that plugin's validation issue and does not affect others (R5).

## 7. Risks and open questions

1. **New helper package.** `internal/util/version` has no deliverable; the
   shared-utilities area should own it.
2. **Where plugin versions come from.** Settled: plugins are local directories
   ([DH-ADR-13](../design.md#dh-adr-13)), so there is no version list to sort. What
   remains is the range check of a manifest's harness version, the model-listing gate
   and the update check.
3. **Client version sent to the model vendor's catalog.** Whether reporting the client
   name and version to a vendor counts as data leaving the machine is a question the
   owner has not answered; the design treats it as opt-in until then
   ([DH-ADR-9](../design.md#dh-adr-9)). Area A04 owns the gate of R6.

## 8. Verification

Local Go tests on strings. Nothing here has been run; the probe observations above are
unverified until these tests pass.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-SEMVER-1` | local | `DH-LIB-SEMVER-R1`, `DH-LIB-SEMVER-R5` | The accept and reject rows of the probe table (to be measured) through `Parse`; `Parse(s).String() == s`; two versions that differ only in build metadata compare equal in order and unequal as exact text; a manifest with a bad version yields an error naming the field |
| `DH-T-SEMVER-2` | local | `DH-LIB-SEMVER-R2`, `DH-LIB-SEMVER-R3`, `DH-LIB-SEMVER-R6` | A shuffled list including prereleases sorts to the SemVer specification's published precedence example; `IsPrerelease` is right for each; `AtLeast` is false for an older or prerelease-of-the-same version |
| `DH-T-SEMVER-3` | local | `DH-LIB-SEMVER-R4` | A table of ranges and versions, including every range form in R4 and a prerelease harness version, gives the listed answer; an empty or malformed range is an error |

## 9. Units dispositioned by this spec

`UP-DEP-semver` (replaced), `UP-DEBT-found.L1.3` (resolved).
