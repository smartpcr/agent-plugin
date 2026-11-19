# js-yaml: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". The YAML module, the dialect and the comparison of candidates are
> in [yaml.md](yaml.md).

| | |
|---|---|
| **Library ID** | `DH-LIB-JSYAML` |
| **Upstream unit** | `UP-DEP-js-yaml` |
| **Upstream version** | 4.3.2 (`pnpm-lock.yaml`) |
| **Kind** | runtime in two packages (release tooling, test support); build-only and test-only everywhere else |
| **Used by** | apps, test-support at run time; root, apps and experimental at development time |
| **Go choice** | no second YAML library: the module of [yaml.md](yaml.md) for hand-written YAML, the standard library `encoding/json` for machine-written files |
| **Related areas** | A24, the update command that replaces the desktop update feed ([../areas/app-cli.md](../areas/app-cli.md)); A23, test support ([../areas/util-test-support.md](../areas/util-test-support.md)); the toolchain ([../upstream/toolchain.md](../upstream/toolchain.md)) |

## 1. What it is and why upstream needs it

`js-yaml` is the older of upstream's two YAML libraries. It turns YAML text into
JavaScript values and back (`load`, `dump`) and nothing more: it has no document tree, so
it cannot change a file while keeping its comments.

It was the loader's parser until release 0.2.2-rc.3, when the loader moved to `yaml`
(`docs/upgrade-guide/v0.2.2-rc.3/cordis-loader-entry-validation/guide.md:14`). Today it
parses no harness configuration. It remains where nobody migrated it: release tooling
that reads and writes the desktop application's update feed, a test-support manifest, and
about forty build scripts and tests. The duplication is recorded as
`UP-DEBT-found.L1.1` in [yaml.md](yaml.md).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used | In Go |
|---|---|---|---|
| `apps/desktop-publisher/src/desktop-publish.ts:67`, `apps/desktop-publisher/src/desktop-rollout.ts:22`, `:67`, `:116` | Read the published update feed (version, file list with sizes and SHA-512 digests, rollout percentage) and write it back with a changed percentage. The feed is machine-written. | `load`, `dump` | a JSON feed, `encoding/json` (R1) |
| `packages/test-support/session-snapshot/src/manifest.ts:204` | Read a snapshot test's `snapshot.yml` manifest with the JSON-compatible rule set, "without admitting JavaScript YAML tags" (`:195`) | `load` with `JSON_SCHEMA` | strict parse of [yaml.md](yaml.md) (R2) |
| `apps/desktop/scripts/` (8 files, for example `apps/desktop/scripts/desktop-upload-plan.ts:9`) | Build and release scripts for the Electron application | `load`, `dump` | not applicable: the Electron build is not ported ([DH-ADR-3](../design.md#dh-adr-3)) |
| `scripts/` (27 files, for example `scripts/persistence-formats.ts:6`, `scripts/ci-workflow.spec.ts:5`) | Repository gates: read CI workflow files, persistence catalogs and documentation metadata | `load`, `dump`, `JSON_SCHEMA` | Go tests and commands of the toolchain; any YAML they read goes through the same package |
| `apps/cli/package.json:101` | Declared as a runtime dependency; imported only by tests (`apps/cli/tests/profiles/packed-installation.ts:9`) | none | nothing to port |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-JSYAML-R1` | Files that only programs write (update feed, release metadata, generated catalogs) are JSON, read and written with the standard library, and decoded strictly: an unknown field or a wrong type is an error. | the publisher use sites; [DH-ADR-LOADER-1](cordis-loader.md#dh-adr-loader-1) |
| `DH-LIB-JSYAML-R2` | A hand-written YAML file read by a tool or a test (a scenario manifest, a CI file) is read by the one harness YAML package under its strict dialect: no language-specific tags, no anchors, located errors. | `packages/test-support/session-snapshot/src/manifest.ts:195-208` |
| `DH-LIB-JSYAML-R3` | The harness module imports exactly one YAML module. | `UP-DEBT-found.L1.1` |

## 5. Go choice

**Decision.** There is no Go counterpart of `js-yaml`. Its hand-written-YAML job is
merged into `DH-LIB-YAML`; its machine-written job moves to `encoding/json`.

**Why.** Two parsers for one format is the debt
([DH-ADR-YAML-1](yaml.md#dh-adr-yaml-1)). The update feed has no human author, so it
gains nothing from YAML and loses a dependency by being JSON.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Merge into `DH-LIB-YAML`; JSON for machine-written files (chosen)** | One parser, one dialect; release tooling needs no third-party code | The update feed format differs from upstream's, which [DH-ADR-2](../design.md#dh-adr-2) allows | `go.yaml.in/yaml/v4` v4.0.0, Apache-2.0 with MIT for the files ported from libyaml, verified in [yaml.md](yaml.md) on 2026-11-18 |
| A second, simpler YAML module for load-only uses | Smaller API at those call sites | Recreates `UP-DEBT-found.L1.1` | not evaluated further |

**Tradeoff.** The update feed is not interchangeable with upstream's YAML feed. Nothing
reads upstream's: there is no desktop application and no desktop updater
([DH-ADR-18](../design.md#dh-adr-18)), and the one update path, the `dsh update`
command, reads a small signed JSON manifest per channel
([electron-updater.md](electron-updater.md) §6).

## 7. Risks and open questions

- The update manifest format belongs to [electron-updater.md](electron-updater.md) and
  area A24 (`internal/update`), and the scenario manifest format to area A23. This
  specification only fixes which parser they use.

## 8. Verification

Local Go tests. Nothing here has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-JSYAML-1` | local | `DH-LIB-JSYAML-R3`, `DH-LIB-JSYAML-R1` | A test lists the packages the harness module imports (`go list -deps`) and fails if any YAML module other than the one in `DH-LIB-YAML` appears, or if a package outside the YAML helper imports it. A feed document with an unknown field fails to decode. |
| `DH-T-YAML-2` (defined in [yaml.md](yaml.md)) | local | `DH-LIB-JSYAML-R2` | The strict dialect rejects tags and anchors with positions |

## 9. Units dispositioned by this spec

`UP-DEP-js-yaml` (merged).
