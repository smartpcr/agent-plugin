# Upstream toolchain: build, test, gates, CI and release

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md) and are cited as `path:line` relative to the upstream root.
> Statements that are reasoning and not read from source open with "Inference". Nothing
> described for Go has been built or run. Owner: agent F7, ID key `TOOLCHAIN`.
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The owner decided
> to **keep the documentation website** and to drop bilingual documents with their
> pairing checks, governance automation and contributor skills.
> [DH-ADR-TOOLCHAIN-15](#dh-adr-toolchain-15) is amended accordingly. The owner also
> removed the desktop application, so there are no desktop installers among the release
> channels, and set the coverage floor to 85%.

## 1. Purpose

This document answers two questions. First: how does upstream deepseek-harness (`dsh`) turn source into a tested, released product, and which checks stop a bad change? Second: what should the Go project do instead? It also owns the dispositions of upstream's build-only and test-only dependencies, which are listed in [../libs/build-test-toolchain.md](../libs/build-test-toolchain.md).

The short version. Upstream is a pnpm workspace of several hundred TypeScript packages. It needs a type checker, two bundlers, a test runner with eight configurations, a gate scheduler with 19 named aggregates, 51 `verify-*` scripts, 17 generators, 35 GitHub workflows and four release channels (npm, single executable, desktop installers, Python wheels). Most of that machinery exists because JavaScript has no compiler that produces one checked artifact. A Go module gets type checking, building, testing, race detection, fuzzing and coverage from one toolchain. The Go design keeps upstream's test *policy*, which is good, and drops most of its *machinery*.

## 2. Vocabulary

| Term | Meaning | Why it exists |
|---|---|---|
| **Lane** (upstream: "tier") | A named group of tests with one runner configuration, one cost and one kind of evidence | So a contributor runs only the evidence that fits the change (`docs/testing.md:9-15`) |
| **Gate** | One command whose non-zero exit blocks a merge | To make an invariant mechanical: "Wire mechanically checkable invariants into an executed top-level gate" (`AGENTS.md:180`) |
| **Aggregate** | A named list of gates with dependencies between them, such as `ci-static` | One name per CI job (`scripts/run-gates.ts:19-38`) |
| **Keyless / with-key** | Whether a test needs a real model API key | Keyless tests run on every pull request; with-key tests prove the agent works against a real model (`docs/testing.md:25`) |
| **Self-skip** | A test that reports "skipped" when its key or operating-system feature is missing | Keeps keyless CI green (`docs/testing.md:11`); it can also hide a missing proof (`.github/workflows/sandbox.yml:38-42`) |
| **Recorded session** | A committed session log (JSON Lines) that holds the user input, the model's replies and the expected persisted result | The input and the oracle of a replay test (`snapshots/AGENTS.md:3`) |
| **Replay / record / refresh** | Replay runs the product against recorded model replies and compares, writing nothing. Record calls the real model and rewrites fixtures. Refresh replays the recorded replies and rewrites only the expected outputs | Three modes of the same test (`vitest.snapshot.config.ts:28-32`) |
| **Expected output** | A committed file a test compares its output with, where no recorded session is involved. Go calls this a golden file; upstream bans the word "golden" in file names (`scripts/check-expected-filenames.sh:19-25`) | Catches unintended changes to text a person or a model sees |
| **Baseline** | A committed list of existing violations of a rule that the gate tolerates while rejecting new ones | Lets a rule be switched on before old code is fixed (`scripts/baseline-inventory.ts:1-10`) |
| **Coverage partition** | One of several test processes that each run part of the suite with coverage instrumentation; their reports are merged before thresholds apply | The per-file 100% rule needs the whole inventory, and one process is too slow (`scripts/coverage-partitions.ts:1-5`) |
| **Source plane / artifact plane** | Source plane: tests and static checks import TypeScript source through a path map. Artifact plane: a check runs the built `lib/` JavaScript | Mixing them loads two copies of a module (`AGENTS.md:150`, `docs/testing.md:45`) |
| **Compiler face** | The Host program or the Client program; upstream type-checks them separately | Both sides extend the same cordis `Context` interface with different services, which collides inside one program (`docs/development.md:71`) |
| **Built-bin smoke** | A test that runs the published entry point under plain Node | The source runner (tsx) hides failures the shipped artifact has (`docs/testing.md:41`) |
| **Release family** | A set of packages published together on one version line: `dsh`, `vendor`, `native` | Releasing one never republishes another (`scripts/release/families.ts:1-10`) |

## 3. Test lanes

| Lane | Command | Selection | What it proves | What it does not prove |
|---|---|---|---|---|
| Unit | `pnpm run test` (`package.json:61`) | `packages/*/*/tests/**/*.spec.ts(x)`, `apps/*/tests`, `scripts/**/*.spec.ts`, `website/tests` (`vitest.config.ts:139-144`) | Package behaviour in one process, with source imports. Every registry gets a test that disposes the contributing fiber and asserts cleanup (`docs/testing.md:9`) | That the built product works |
| Coverage gate | `pnpm run test:coverage` (`package.json:62`) | Same inventory, instrumented | Every file under `packages/*/*/src` has 100% statement, branch, function and line coverage (`vitest.config.ts:435-443`) | That the feature works: "necessary, never sufficient" (`docs/testing.md:10`) |
| Real-API e2e | `pnpm run test:e2e` (`package.json:65`) | `**/*.e2e.ts` (`vitest.e2e.config.ts:45`), 120 s timeout, 2 retries (`vitest.e2e.config.ts:53-55`) | The agent works against a live provider | Nothing without a key: each suite self-skips |
| Expected output | `pnpm run test:expected` (`package.json:69`) | `apps/cli/tests/**/*.expected.e2e.ts` (`vitest.expected.config.ts:12-14`) | Assembled CLI and process output without a recorded session | Model-driven behaviour |
| Snapshot (recorded session) | `pnpm run test:snapshot` (`package.json:74`) | `snapshots/**/*.snapshot.ts` (`vitest.snapshot.config.ts:51-57`) | The shipped profile, started through `dsh`, produces the committed session log from recorded model replies | Behaviour of a live model |
| Web browser snapshot | `pnpm run test:web` (`package.json:79`) | `apps/web/tests/**/*.e2e.ts`, `*.snapshot.ts` (`vitest.web.config.ts:26-30`) | The built client renders recorded sessions as committed accessibility trees, in Chromium (one case also in WebKit) (`docs/testing.md:15`) | Hardware presentation |
| Performance | `pnpm run test:bench` (`package.json:66`) | `benchmarks/**/*.bench.ts` (`vitest.bench.config.ts:20-23`) | Time, memory and scaling budgets on synthetic inputs, run under plain Node (`benchmarks/AGENTS.md:7-9`) | Real-user workloads |
| Node compatibility | `pnpm run check:node-compat` (`package.json:104`) | A fixed list of `*.compat.spec.ts` files (`scripts/run-gates.ts:480-513`) | Source launch and loader behaviour on Node 24.9 and 26 (`.github/workflows/ci.yml:562-566`) | Exists only because of Node |

Rules upstream attaches to the lanes, each with a verdict for Go (keep, change or drop):

| Upstream rule | Evidence | Verdict | Reason |
|---|---|---|---|
| Mock only the expensive or non-deterministic boundary (model, network, clock); keep the rest real | `docs/testing.md:29` | **Keep** | It is the reason a green suite means something |
| Assert on the world, not the agent's report: re-read the file, re-run the command | `docs/testing.md:35` | **Keep** | A keyword probe on model output can be satisfied by a wrong run |
| Product-visible plugins need a test that boots the real composition through the loader | `docs/testing.md:39` | **Keep** | Hand-wired tests missed a real defect (upstream postmortem 0001, cited at `docs/testing.md:25`) |
| Test the published artifact, not the source runner | `docs/testing.md:41` | **Change** | Go has no source runner that differs from the artifact; the rule becomes "replay and scenario lanes run the built binary" ([DH-ADR-TOOLCHAIN-1](#dh-adr-toolchain-1)) |
| Every non-trivial model-, protocol- or human-visible change adds a keyless recorded-session scenario | `docs/testing.md:55` | **Keep** | It is the only keyless proof of the assembled transcript |
| Tests own every port, temporary path and child process; a test that passes only alone is defective | `docs/testing.md:21`, `scripts/AGENTS.md:5` | **Keep** | Same hazard under `go test -p` |
| Per-file 100% coverage | `vitest.config.ts:431-443` | **Change** | See [DH-ADR-TOOLCHAIN-7](#dh-adr-toolchain-7) |
| With-key suites self-skip without a key | `docs/testing.md:11` | **Change** | A skip is not a pass ([DH-ADR-TOOLCHAIN-6](#dh-adr-toolchain-6)) |
| All tests run in forked processes because Node 24 worker threads crashed in the CommonJS lexer | `vitest.config.ts:231-234` | **Drop** | A Node defect workaround |
| Suites excluded on Windows by an explicit list | `vitest.config.ts:24-64` | **Change** | Go uses build constraints (`//go:build`) on the file, next to the code |

## 4. The recorded-session replay system

**What it does.** A scenario is a directory. It holds one or more session logs (`session[.vN].jsonl` for the parent, `session.<ordinal>[.vN].jsonl` for child agents), a `snapshot.yml` manifest (profile, composition, recording policy, workspace facts), a configuration overlay (`cordis.snapshot.yml`), readable sidecars for the system prompt and tool schemas, and optionally a workspace seed and a `workspace.expected/` tree (`snapshots/AGENTS.md:7-15`; example `snapshots/session/advanced-toolchain/`). There are 201 entries under `snapshots/session`, 79 under `snapshots/web`, 44 under `snapshots/sdk` and 10 under `snapshots/acp`.

**How a replay runs.** The test starts the real `dsh` CLI with a shipped profile (`snapshots/AGENTS.md:5`). A replay model provider answers from the recorded log, so no key is needed. The test drives the product through one of four surfaces: headless one-shot, the JSON-RPC SDK, the Agent Client Protocol (ACP), or the browser. It then normalises the session log the product wrote and compares it with the committed one. The same file is therefore both the input and the expected result (`docs/testing.md:14`).

**Why normalisation matters.** Identifiers, times and paths differ between runs. Upstream replaces them with typed tokens that preserve relationships (the same parent ID gets the same token everywhere), and stores each distinct system prompt and tool schema once as a sidecar (`snapshots/AGENTS.md:11`). A committed session must be a fixed point of normalisation. Upstream's rule is "fix fixtures, not normalizers" (`AGENTS.md:162`).

**The independent oracle.** A scenario that changes files commits the complete resulting tree under `workspace.expected/`. Record and refresh never rewrite it, because "model prose and tool-result text do not prove the external effect" (`snapshots/AGENTS.md:15`).

**Format generations.** Several generations of one log may sit side by side (`session.v2.jsonl`, `session.v3.jsonl`, `session.v4.jsonl`); replay picks the highest, and older ones are kept to prove migrations (`snapshots/AGENTS.md:9`). This is persistence compatibility evidence, owned by the persistence documents, not by this one.

**Failure behaviour.** Replay writes nothing to the source tree. Record and refresh run serially because record spends API quota and refresh reads values from files it is about to rewrite (`vitest.snapshot.config.ts:58-65`). Inference: a record run that fails half-way leaves a partly rewritten fixture directory, recovered with `git checkout`; upstream relies on review of every resulting diff (`snapshots/AGENTS.md:17`).

**Verdict: keep the system, change the formats.** See [DH-ADR-TOOLCHAIN-5](#dh-adr-toolchain-5). Upstream fixtures are not reused ([DH-ADR-2](../design.md#dh-adr-2)).

## 5. Gates

`scripts/run-gates.ts` (78 KB) is an in-process scheduler. A gate is a command with `needs` (must pass first), `after` (must finish first), optional retries and an `allowFailure` flag (`scripts/run-gates.ts:44-64`). It validates the graph, runs gates with bounded concurrency, and kills whole process trees on abort (`scripts/run-gates.ts:961-1017`, `scripts/run-gates.ts:1468-1715`). It exposes 19 aggregates (`scripts/run-gates.ts:19-38`); the documentation aggregate alone has 43 leaves (`scripts/run-gates.ts:838-912`).

| Protects | Upstream gates (examples) | Evidence | Go verdict |
|---|---|---|---|
| Types and style | `typecheck`, `lint` (oxlint, type-aware), `duplication` (jscpd) | `package.json:54-60` | **Change**: compiler, `gofmt`, `go vet`, staticcheck ([DH-ADR-TOOLCHAIN-8](#dh-adr-toolchain-8)); clone detection dropped |
| Behaviour | `coverage`, `coverage-exempt-heavy`, `snapshot`, `expected-output`, `web-snapshot`, `built-bin-smoke`, `bench` | `scripts/run-gates.ts:729-798`, `scripts/run-gates.ts:923-955` | **Keep** as `go test` lanes ([DH-ADR-TOOLCHAIN-1](#dh-adr-toolchain-1)) |
| Architecture boundaries | `constraints`, `verify-package-dependencies`, `verify-module-graph`, `verify-client-domain-graph`, `verify-default-product-isolation`, `verify-product-use`, `verify-application-entrypoints`, `verify-runtime-closure`, `verify-cordis-config`, `verify-plugin-entry-exports` | `scripts/run-gates.ts:389-407` | **Change**: `internal/` packages and import-rule tests replace most; npm-manifest gates do not apply |
| Banned source forms | `verify-no-unknown-casts`, `verify-empty-catch`, `verify-cordis-root` (these three use baselines), `verify-no-top-level-await`, `verify-no-bare-dispatcher`, `verify-optional-dependency-imports`, `verify-client-route-resolution`, `verify-config-source-ownership` | `scripts/run-gates.ts:417-434` | **Change**: own analyzers, no baselines ([DH-ADR-TOOLCHAIN-8](#dh-adr-toolchain-8)) |
| Publication form | `publint`, `verify-node-next-types`, `verify-npm-install-layout`, `verify-package-meta`, `verify-dsh-package-licenses`, `verify-version-sync`, `rescope-vendor:check` | `package.json:105`, `package.json:145-151`, `scripts/run-gates.ts:817-836` | **Drop**: npm packaging only |
| Generated files are current (`--check`) | CLI help, cordis catalog and API, inspect catalog, client catalog, tool catalog, config catalog, plugin packages, dependency catalog, doc graphs, persistence catalog, session format catalog, third-party notices, module graph, tsconfig paths, workflow guest, official bundle catalog, provider data | `package.json:166-204`, `package.json:223-226` | **Keep** the idea, one mechanism ([DH-ADR-TOOLCHAIN-10](#dh-adr-toolchain-10)) |
| Persistence compatibility | `verify-persistence-changes`, `verify-persistence-releases`, `verify-persistence-formats`, `verify-v3-event-vocabulary`, the snapshot corpus policy | `scripts/run-gates.ts:874-878` | Owned by the persistence documents; the gate form is a Go test |
| Documentation quality | `doc-typecheck`, `verify-type-equiv`, `verify-md-links`, `verify-md-wrap`, `verify-mermaid`, `verify-doc-refs`, `verify-doc-budgets`, `verify-export-jsdoc`, README section gates, `docs:build` | `scripts/run-gates.ts:852-910` | **Change**: link checking is already done by `tools/harness-trace`; `docs:build` becomes the build of the documentation site, which the owner keeps; the TypeScript, pairing, wrap, budget and Mermaid-syntax gates are dropped ([DH-ADR-TOOLCHAIN-15](#dh-adr-toolchain-15)) |
| Localisation | `verify-installer-locales`, `verify-locale-metadata`, `verify-client-ui-i18n`, locale dictionary tests, `verify-translation-pairing`, `verify-translation-prompt` | `scripts/run-gates.ts:409-415` | UI copy rules belong to the UI specifications; bilingual document pairing dropped |
| Contributor process | Agent Note format and archive gates, skill metadata, weighted-approval, issue and stale-pull-request policy tests | `scripts/run-gates.ts:889-892`, `package.json:71-73` | **Drop** ([DH-ADR-TOOLCHAIN-15](#dh-adr-toolchain-15)) |

**Baselines.** Three gates share one mechanism (`scripts/baseline-inventory.ts`): each finding is keyed by file path and a SHA-256 of its syntax tokens, and the gate fails on a finding the baseline does not list. "No command adds an allowance" (`scripts/baseline-inventory.ts:9`); the only automated change is pruning. Section 9 treats each baseline file as tech debt.

**Git hooks.** lefthook runs staged-file lint with auto-fix, a whitespace check, two scoped document checks and the vendor manifest guard before commit, and a type check before push (`lefthook.yml:5-49`). Hooks deliberately run no tests (`docs/development.md:152`).

## 6. Code generation

| Generator | Output | Source of truth | Evidence |
|---|---|---|---|
| Typert (`generate-typert.ts`) | Reflection metadata and Host-for-Client remote declarations | TypeScript types of the Host program | `docs/development.md:109` |
| `gen-tool-catalog` | `docs/tool-catalog.md` | Schemas collected by booting each tool plugin | `scripts/gen-tool-catalog.ts:2-4` |
| `gen-config-catalog` | `docs/config-catalog.md` | Package entry points, config types, static schemas | `scripts/gen-config-catalog.ts:2-5` |
| `gen-cordis-catalog`, `gen-cordis-api`, `gen-cordis-inspect-catalog` | Service and event references, per subsystem page and model-visible | The Typert type graph | `scripts/gen-cordis-catalog.ts:2-5` |
| `gen-persistence-catalog`, `gen-session-format-catalog` | Durable event vocabulary, format catalog | Every `SessionEventMap` declaration merge | `scripts/gen-persistence-catalog.ts:2-6` |
| `gen-cli-help` | CLI reference | The shipped launcher's own help output | `scripts/gen-cli-help.ts:1` |
| `gen-doc-graphs`, `gen-module-graph` | Relationship diagrams | Source plus hand-kept manifests | `scripts/gen-doc-graphs.ts:1-6` |
| `gen-third-party-notices`, `gen-dependency-catalog` | Licence notices, dependency list | Workspace manifests and the installed store | `scripts/gen-third-party-notices.ts:1-8` |
| `gen-tsconfig-paths`, `gen-client-catalog`, `gen-workflow-guest`, `gen-plugin-packages`, `gen-official-bundle-catalog` | Path map, client slot catalog, bundled guest source, package lists | Workspace manifests and source | `package.json:166-224` |

Each generator has a `--check` twin wired as a gate. Two different sources of truth are in use: some catalogs come from **booting the product** (tool catalog, CLI help), others from **static type analysis** (cordis catalog, persistence catalog). The static ones carry the cost of the split compiler faces: the event relationship graph is built from the Host program only, so Client-side listeners are under-reported (`scripts/gen-doc-graphs.ts:1256-1261`, the assigned `TODO` debt unit).

## 7. Continuous integration

35 workflow files live under `.github/workflows`. Grouped by purpose:

| Group | Workflows | Notes |
|---|---|---|
| Required pull-request checks | `ci.yml`, `ci-windows.yml`, `ci-python.yml` | Linux jobs: static, coverage, lint, benchmarks, "snapshots and artifacts", Node compatibility (`.github/workflows/ci.yml:77-534`). One job, "all checks passed", is the single required verdict (`.github/workflows/ci.yml:649-666`) |
| Main-branch only | `ci-master.yml`, `sandbox.yml` | Serial reference runs on macOS, Wine and self-hosted standby runners; real-kernel sandbox proofs that assert no test was skipped (`.github/workflows/sandbox.yml:38-42`) |
| Credentialed tests | `e2e.yml`, `pi-ai-provider-e2e.yml` | Nightly and on trusted events; a preflight fails the job when the secret is missing so a self-skipping suite cannot report a false pass (`.github/workflows/e2e.yml:15-16`); never `pull_request_target` (`.github/workflows/e2e.yml:20-24`) |
| npm release | `release.yml`, `release-publish.yml`, `release-vendor.yml`, `release-vendor-publish.yml`, `node-addon-system.yml`, `node-addon-system-release.yml` | Pack is rehearsed on every pull request; publication is manual |
| Other artifacts | `build-exe-for-python-sdk.yml`, `python-release.yml`, `build-exe-for-ssh-helper.yml`, `publish-ssh-helper.yml`, `cua-helper-package-*.yml`, `macos-package.yml`, `macos-intel-package.yml`, `windows-package.yml`, `desktop-publish.yml`, `desktop-rollout.yml` | Single executables, wheels, helper archives, desktop installers |
| Documentation | `docs-pages.yml`, `build-preview-cloudflare.yml`, `expected-filenames.yml` | Site deployment, pull-request preview, naming rule |
| Repository governance | `pr-check-*.yml`, `weighted-approval*.yml`, `issue-lifecycle.yml`, `issue-policy.yml`, `stale-pull-requests.yml` | Merge policy and approval weighting, with their own code under `.github/review-ownership` and `.github/issue-management` |

Notable properties. A new push cancels the superseded run (`.github/workflows/ci.yml:17-19`). A repository variable reroutes Linux jobs to a self-hosted or third-party pool without a merge (`.github/workflows/ci.yml:63-76`). Gate aggregates derive their worker counts from the runner's CPU count and split one budget between coverage processes (`scripts/run-gates.ts:224-256`). `.gitlab-ci.yml` is a second, tag-triggered pipeline that builds and publishes the Python wheels to a GitLab package registry (`.gitlab-ci.yml:1-4`, `.gitlab-ci.yml:187-191`).

## 8. Packaging and release

| Channel | How upstream builds it | Evidence | Failure and retry |
|---|---|---|---|
| **npm packages** | `tsc` emits `lib/types`, tsdown bundles `lib/` per package (`tsdown.config.ts:21-36`); `release:pack` packs every member of a family; `release:verify-packed-install` installs the tarballs into a throwaway consumer and runs the installed executable | `.github/workflows/release.yml:155-183` | Publication decides per package against the registry: missing is published, identical integrity is skipped, different content fails. That makes a re-run safe. Transient registry codes get 4 attempts (`scripts/release/publish.ts:1-13`, `scripts/release/publish.ts:28-36`). Only one job can write to the registry, behind an approval environment (`.github/workflows/release-publish.yml:143-152`) |
| **Single executable** | `scripts/build-exe-for-python-sdk.ts` deploys a dependency closure and packs it with `@yao-pkg/pkg --sea` (a patched dependency) into one file per platform | `scripts/build-exe-for-python-sdk.ts:1-7`, `pnpm-workspace.yaml:118` | Owned by the packaging library specification (agent L14) |
| **Python wheels** | Six wheels: one pure-Python SDK wheel and five platform wheels that carry the single executable | `.github/workflows/python-release.yml:27`, `.gitlab-ci.yml:180-186` | A registry never overwrites a version; a failed publish needs a new tag (`.gitlab-ci.yml:191`). Owned by agent L18 |
| **Desktop installers** | Electron packaged per platform (macOS arm64 and x64, Windows x64 through NSIS), then published and rolled out by manual workflows with approval environments | `package.json:34-51`, `pnpm-workspace.yaml:50-53`, `.github/AGENTS.md:9-11` | Owned by agent L14 |

A build also writes a "client build record" that binds version, commit and a dirty marker to the outputs; release packing rejects artifacts changed by a later partial build (`docs/development.md:111`). Inference: this record exists because many independent build steps write into shared output directories and nothing else proves they belong together.

## 9. What exists only because of JavaScript, TypeScript or npm

| Upstream mechanism | Why it exists | In Go |
|---|---|---|
| Two aggregate type-check programs (Host, Client), seven split packages | Declaration merging of one `Context` interface collides in one program (`docs/development.md:71-77`) | One module, one type-checked program; the UI is rendered by the host ([DH-ADR-3](../design.md#dh-adr-3)) |
| Source plane versus artifact plane | Tests import source through a path map; shipped code is bundled output | `go test` compiles the same packages the binary links |
| Built-bin smokes, NodeNext consumer check, publint | The published package form differs from the source form | One binary; nothing to mis-declare |
| Node version matrix and loader-shape checks | Node's internal loader changed between 24.9 and 24.12 (`.github/workflows/ci.yml:560-563`) | The `go` and `toolchain` lines in `go.mod` select one toolchain |
| pnpm strict linker, `allowBuilds`, release-age exclusions, 12 patched dependencies | npm packages can run install scripts and are resolved by a hoisting-prone installer (`pnpm-workspace.yaml:26-33`, `pnpm-workspace.yaml:112-124`) | Modules never run code when downloaded; `go.sum` and the checksum database verify content |
| Typert code generation before every type check or lint | The Client needs generated declarations of Host methods | No Client program |
| 8 GiB compiler heap limits, worker budgets, coverage partitions | Type-checking and instrumenting a large TypeScript program is slow and memory-hungry (`docs/development.md:69`, `docs/development.md:156`) | The Go build cache and test cache |
| Publish ordering, registry retry, packed-install verification | Hundreds of interdependent packages are published one by one | One archive per platform and one module tag |

## 10. Tech debt assigned to this document

| Unit | The rule it records exceptions to | Size at the pinned commit | How Go avoids it |
|---|---|---|---|
| `UP-DEBT-baseline.no-unknown-casts.baseline.json` | No new type assertions to `unknown` (the TypeScript way to defeat the type checker, `AGENTS.md:149`) | 1,204 recorded assertions in 477 files | Resolved by [DH-ADR-TOOLCHAIN-8](#dh-adr-toolchain-8): the Go analogue (unchecked type assertions and `unsafe`) is an analyzer that is on from the first commit, with inline reasoned exceptions |
| `UP-DEBT-baseline.cordis-root.baseline.json` | No new reads of the cordis root context (`scripts/verify-cordis-root.ts:1-20`) | 42 recorded reads in 27 files; 11 of the files are product source | Resolved by [DH-ADR-TOOLCHAIN-8](#dh-adr-toolchain-8): the Go port has one accessor, `Context.Root()`; an analyzer restricts its callers from day one |
| `UP-DEBT-baseline.empty-catch.baseline.json` | An empty `catch` must name the error, explain itself and guard one statement (`scripts/verify-empty-catch.ts:1-20`) | Empty (`{}`): upstream has paid this debt | Resolved by [DH-ADR-TOOLCHAIN-8](#dh-adr-toolchain-8): the Go analogue is a discarded `error`; same analyzer rule, no baseline |
| `UP-DEBT-baseline.coverage-exempt.ts` | Per-file 100% coverage: nine heavy suites run uninstrumented (`scripts/coverage-exempt.ts:29-64`), beside about 150 lines of file exclusions (`vitest.config.ts:275-430`) | 9 suite entries | Resolved by [DH-ADR-TOOLCHAIN-7](#dh-adr-toolchain-7): compile-time coverage counters are cheap and cover child processes, so no exemption list is needed |
| `UP-DEBT-marker.scripts/gen-doc-graphs.ts:1256` | Event graph under-reports Client listeners because one program cannot hold both faces | One `TODO` | Resolved by [DH-ADR-TOOLCHAIN-10](#dh-adr-toolchain-10): catalogs come from the running registries of one program |
| `UP-DEBT-pin.allowBuilds.lefthook`, `UP-DEBT-pin.allowBuilds.msgpackr-extract`, `UP-DEBT-pin.minimumReleaseAgeExclude.pnpm@11.28.5` | pnpm install-script allow-list and release-age exemptions | Three entries | Not applicable: see [../libs/build-test-toolchain.md](../libs/build-test-toolchain.md) section 3 |
| `UP-DEBT-pin.overrides.extract-zip_yauzl` | A transitive dependency forced to a version with no stated reason (`pnpm-workspace.yaml:18`) | One entry | Resolved by [DH-ADR-TOOLCHAIN-11](#dh-adr-toolchain-11): every pin carries a reason, checked by a test |

## 11. Recommendations for the Go project

Existing practice in this repository is the starting point: [ci.yml](../../../.github/workflows/ci.yml) runs `gofmt`, `go vet` and `go test -race` on Linux and `go test` on Windows; [release.yml](../../../.github/workflows/release.yml) runs CI first, creates the `src/go/vX.Y.Z` module tag and runs GoReleaser ([.goreleaser.yaml](../../../.goreleaser.yaml)). The decisions below extend that practice to the harness module `src/harness` ([DH-ADR-5](../design.md#dh-adr-5)). Tool and module facts are recorded in [../libs/build-test-toolchain.md](../libs/build-test-toolchain.md).

<a id="dh-adr-toolchain-1"></a>
### DH-ADR-TOOLCHAIN-1 Five test layers, all run by `go test`

- **Decision.** (1) *Unit*: package tests with fakes only at the model, network and clock boundaries. (2) *Composition*: boots a real configuration through the Go cordis loader in-process with a scripted model. (3) *Golden*: compares rendered text (prompts, tool schemas, CLI help, HTML fragments) with committed files. (4) *Replay*: runs the built `dsh` binary against recorded sessions. (5) *Scenario*: a real model API, a real sandbox, a real SSH host or a real browser. Layers 1 to 3 run under plain `go test ./...`. Layers 4 and 5 live in their own packages behind build tags (`replay`, `scenario`) and always run the built binary, never `go run`.
- **Why.** Upstream's lanes map to evidence kinds that remain valid. Eight runner configurations do not.
- **Tradeoff.** Build tags hide files from a plain `go vet`; CI must vet with the tags set.
- **Serves.** [DH-O-15](../design.md#dh-o-15).
- **Verified by.** `DH-T-TOOLCHAIN-1`.

<a id="dh-adr-toolchain-2"></a>
### DH-ADR-TOOLCHAIN-2 Race detector, shuffled order and virtual time by default

- **Decision.** Layers 1 to 4 run with `-race -shuffle=on -count=1` wherever cgo is available (Linux in CI, as today); the binary that replay starts is built with `-race` as well. Tests of timers, retries and timeouts use `testing/synctest` (standard library) instead of sleeping. Every test that starts a goroutine, listener or child process registers its release with `t.Cleanup`.
- **Why.** The harness is concurrent by nature (streaming, tools, sub-agents, fibers). Upstream has no race detector and needs a dedicated reliability skill (`.agents/skills/dsh-ci-test-reliability/SKILL.md`) and forked processes to contain shared state.
- **Tradeoff.** The race detector needs a C compiler and slows tests several times over; Windows and macOS jobs run without it unless a compiler is present. `testing/synctest` requires a `go` directive of 1.25 or later in `src/harness/go.mod`.
- **Serves.** [DH-O-15](../design.md#dh-o-15).
- **Verified by.** `DH-T-TOOLCHAIN-2`.

<a id="dh-adr-toolchain-3"></a>
### DH-ADR-TOOLCHAIN-3 Native fuzzing at every trust boundary

- **Decision.** Each parser of untrusted bytes gets a fuzz target (`func FuzzXxx(f *testing.F)`): session log lines, configuration files, tool-call JSON from the model, streamed model responses, JSON-RPC and MCP frames, patch text, shell-command splitting. Seed inputs and every crasher found are committed under `testdata/fuzz` and run as ordinary cases in the unit layer. A scheduled CI job fuzzes each target for a bounded time.
- **Why.** Upstream uses a property-testing library in five suites (`packages/core/session/tests/properties.spec.ts:11` and four others). Go has coverage-guided fuzzing in the toolchain, which finds inputs a hand-written generator does not.
- **Tradeoff.** Fuzz targets accept only basic types; structured generators need a decoder from bytes. Scheduled fuzzing is not a merge gate.
- **Serves.** [DH-O-15](../design.md#dh-o-15).
- **Verified by.** `DH-T-TOOLCHAIN-3`.

<a id="dh-adr-toolchain-4"></a>
### DH-ADR-TOOLCHAIN-4 Golden files with one update switch

- **Decision.** Expected text lives beside its test under `testdata/` with a `.golden` suffix. One shared helper compares and prints a line diff; the same helper rewrites the file when the test binary is run with `-update`. The helper refuses to update when `CI` is set. Multi-file fixtures use the `txtar` archive format.
- **Why.** Go tooling ignores `testdata/`, and the convention is known to Go engineers. Upstream's ban on the word "golden" is a naming preference, not a requirement.
- **Tradeoff.** Golden tests detect change, not correctness; every updated file is reviewed.
- **Serves.** [DH-O-15](../design.md#dh-o-15).
- **Verified by.** `DH-T-TOOLCHAIN-4`.

<a id="dh-adr-toolchain-5"></a>
### DH-ADR-TOOLCHAIN-5 Keep recorded-session replay, in the Go design's own formats

- **Decision.** Keep: scenarios start the real binary with a shipped profile; a replay model provider answers from the recorded log; the committed log is input and oracle; a file-changing scenario commits an independent `workspace.expected/` tree; three modes (replay is the default and read-only, record needs a key, refresh rewrites expected outputs only). Change: fixtures are recorded from the Go product in the Go session format; the manifest is a small typed file; normalisation tokens are defined once in the replay package. Drop: retained historical generations, until the Go format has a second version.
- **Why.** It is the only keyless proof that the assembled product produces the right transcript and the right files.
- **Tradeoff.** No fixture can be taken from upstream ([DH-ADR-2](../design.md#dh-adr-2)); the first corpus needs a real key to record.
- **Failure and cleanup.** Each scenario owns a temporary home and workspace (`t.TempDir`), its child process (killed when the test context ends) and its listener (port 0). Replay never writes to the repository. Record writes to a temporary directory and renames into place only after the run and its comparison finish, so a failed record leaves the committed fixture untouched; spent API quota is the one thing not undone.
- **Serves.** [DH-O-15](../design.md#dh-o-15).
- **Verified by.** `DH-T-TOOLCHAIN-5`, `DH-E-TOOLCHAIN-1`.

<a id="dh-adr-toolchain-6"></a>
### DH-ADR-TOOLCHAIN-6 A skipped scenario is never a pass

- **Decision.** Scenario tests may skip when a credential or operating-system feature is absent, so contributors without keys are not blocked. The acceptance workflow sets `DSH_REQUIRE_SCENARIOS` to a list of scenario IDs; a listed scenario that skips or does not run fails the job. Credentialed workflows fail in a preflight step when a secret is missing.
- **Why.** Upstream found that self-skipping suites report false passes and added preflights and "no skips" assertions workflow by workflow (`.github/workflows/e2e.yml:15-16`, `.github/workflows/sandbox.yml:38-42`). One rule is simpler.
- **Tradeoff.** The acceptance workflow needs every environment it lists.
- **Tech debt answered.** With [DH-ADR-TOOLCHAIN-12](#dh-adr-toolchain-12), this answers `UP-DEBT-found.Q3.5` (upstream's macOS reference job is switched off, `.github/workflows/ci-master.yml:194`) and `UP-DEBT-found.Q3.6` (whole packages excluded from tests on Windows by a list, `vitest.config.ts:24-64`): macOS and Windows are required lanes of the `harness` job, a file that cannot run on one system carries a build constraint beside the code instead of an entry in a list, and a listed scenario that does not run fails the job.
- **Serves.** [DH-O-15](../design.md#dh-o-15).
- **Verified by.** `DH-T-TOOLCHAIN-6`.

> **DC-3 override note (M1 story, 2026-11-22).** "macOS and Windows are required lanes of
> the `harness` job" is overridden for macOS by owner decision `platforms-windows-wsl` in
> [the M1 decision log](../../stories/harness-DSH-M1/decisions.md). For M1 the required
> lanes are Windows (native) and Linux under WSL; macOS is out of scope, is never recorded
> as a pass or a skip, and has no return stage yet (`macos-return-stage`, open). The rule
> that a skipped scenario is never a pass is unchanged. The decision text above is
> unchanged (DC-2).

<a id="dh-adr-toolchain-7"></a>
### DH-ADR-TOOLCHAIN-7 Coverage across processes, one floor, no exemption list

- **Decision.** Layers 1 to 4 produce one merged profile: `go test -coverprofile` for in-process tests and a binary built with `go build -cover` (counters written to `GOCOVERDIR`) for replay. One uniform statement-coverage floor applies to every package under `src/harness/internal`. Exclusions are by rule (generated files, `main` packages), never by a list of files or suites.
- **Why.** Upstream's per-file 100% rule produced about 150 lines of file exclusions and a second list of suites that run uninstrumented, largely because V8 coverage cannot see child processes or browser code (`vitest.config.ts:95-99`, `scripts/coverage-exempt.ts:1-11`). Go coverage is compiled in and covers child processes.
- **Tradeoff.** A floor below 100% lets some untested lines in. The floor's value was settled by the owner at 85% ([DH-ADR-14](../design.md#dh-adr-14); section 13).
- **Tech debt answered.** `UP-DEBT-found.Q3.4`: upstream's about 150 lines of per-file coverage exclusions, six marked TODO (`vitest.config.ts:275-430`). There is no list of files to keep or to work off; the floor applies to every package, and an exclusion is a rule (generated files, `main` packages) checked by `DH-T-TOOLCHAIN-7`.
- **Serves.** [DH-O-15](../design.md#dh-o-15).
- **Verified by.** `DH-T-TOOLCHAIN-7`.

<a id="dh-adr-toolchain-8"></a>
### DH-ADR-TOOLCHAIN-8 Static checks from the first commit, with no baseline files

- **Decision.** Required checks: `gofmt -l`, `go vet`, staticcheck, govulncheck, and one own analyzer binary built on `go/analysis` and run as `go vet -vettool`. An analyzer joins the required set only in a change that leaves the tree clean under it. An exception is written at the site with a reason (`//lint:ignore <check> <reason>` for staticcheck; the same form for own analyzers); a test fails on an exception without a reason and prints the total. No file of recorded violations exists. Initial own analyzers, each owned by the area that states the rule: callers of `Context.Root()`; discarded `error` values; unchecked type assertions and `unsafe` imports; package import rules; HTTP clients built outside the proxy-aware factory. Clone detection (jscpd) is not carried over.
- **Why.** Upstream's three baselines exist because rules arrived after the code. New Go code has no legacy, so the cheapest moment to enforce a rule is before the first violation.
- **Tradeoff.** A rule discovered late must be fixed everywhere in one change, or stay advisory until it is. Own analyzers are code to maintain.
- **Serves.** [DH-O-15](../design.md#dh-o-15).
- **Verified by.** `DH-T-TOOLCHAIN-8`.

> **DC-3 note (M1 story, 2026-11-22; M1-D-GATE-23, plan 13.5.8).** Tech-spec QG-2 asks
> for "no baseline file and no exemption list". A `//lint:ignore <check> <reason>`
> directive at a site, as this decision requires, is not an exemption list. It sits
> beside the one statement it accepts and states why, and the `lintignore` analyzer
> rejects any directive with no reason, no check or an unknown check. A
> `//lint:file-ignore` is always rejected, and no file outside the code records a
> violation. The vet gate prints the total (`exceptions: N`). The own analyzers of
> M1 are `rootcall`, `typeassert` and `lintignore`, run by vet. `errdiscard` is built
> but pending until its narrowed sites are fixed. The rule above, that an analyzer
> joins only in a change that leaves the tree clean, holds. The decision text above is
> unchanged (DC-2).

<a id="dh-adr-toolchain-9"></a>
### DH-ADR-TOOLCHAIN-9 A gate is a Go test or one of a short fixed command list

- **Decision.** Structural rules (import layering, catalog freshness, module hygiene, suppression reasons) are written as Go tests, so `go test ./...` runs them. The remaining gates are a fixed list in one small runner (`tools/harness-gate`): format, vet, own analyzers, staticcheck, module tidy and verify, vulnerability check, cross-compile, unit tests, replay. The runner is sequential, prints one line per gate and has no dependency graph.
- **Why.** `go test` already schedules packages in parallel, caches results and isolates packages in processes. Upstream's scheduler exists to order steps that write shared output directories.
- **Tradeoff.** No per-gate retry or "allowed to fail" flag; a flaky gate must be fixed.
- **Serves.** [DH-O-15](../design.md#dh-o-15).
- **Verified by.** `DH-T-TOOLCHAIN-9`.

<a id="dh-adr-toolchain-10"></a>
### DH-ADR-TOOLCHAIN-10 Catalogs are generated from the running registries

- **Decision.** One generator boots each shipped composition in-process and writes the tool, configuration, event, service, CLI and persisted-event catalogs from what was actually registered. `go generate` runs it; a test regenerates in memory and fails on a difference from the committed files. There is no static type-graph extraction.
- **Why.** Upstream's boot-based catalogs are complete; its type-graph catalogs are not (section 6). Registration through effects is the single source of truth in cordis.
- **Tradeoff.** A capability registered only under a condition appears only in a composition that meets it; the generator must enumerate compositions.
- **Serves.** [DH-O-14](../design.md#dh-o-14).
- **Verified by.** `DH-T-TOOLCHAIN-10`.

<a id="dh-adr-toolchain-11"></a>
### DH-ADR-TOOLCHAIN-11 One module graph, pinned tools, reasoned pins

- **Decision.** `src/harness/go.mod` requires a tagged version of the cordis module; a root `go.work` joins the two modules for development and CI. Development tools are pinned in a separate tools module, not in the product's `go.mod`. Every `replace`, `exclude`, version floor and tool pin carries a comment giving its reason. Gates: `go mod tidy -diff` prints nothing, `go mod verify` passes, the product builds with `GOWORK=off` before a release.
- **Why.** It removes upstream's installer policy files, and it prevents the unexplained override recorded as debt. A tools module keeps linters out of the product's dependency graph.
- **Tradeoff.** A cordis change reaches a released harness only after a new `src/go` tag, as already documented for the e2e binding in [ci.yml](../../../.github/workflows/ci.yml).
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-15](../design.md#dh-o-15).
- **Verified by.** `DH-T-TOOLCHAIN-11`.

<a id="dh-adr-toolchain-12"></a>
### DH-ADR-TOOLCHAIN-12 CI: keyless required jobs, a separate credentialed workflow

- **Decision.** Add to `ci.yml` a `harness` job on Linux, Windows and macOS that runs the gate runner, and a `harness-replay` job. Credentialed scenarios run in a separate workflow on a schedule, on pushes to `main` and on manual dispatch, never on `pull_request_target`. The release workflow keeps calling CI first.
- **Why.** It matches this repository's practice and upstream's split between keyless and with-key evidence.
- **Tradeoff.** Scenario failures are seen after merge, not before.
- **Tech debt answered.** With [DH-ADR-TOOLCHAIN-6](#dh-adr-toolchain-6): `UP-DEBT-found.Q3.5` and `UP-DEBT-found.Q3.6`.
- **Serves.** [DH-O-15](../design.md#dh-o-15).
- **Verified by.** `DH-E-TOOLCHAIN-1`, `DH-E-TOOLCHAIN-2`.

> **DC-3 override note (M1 story, 2026-11-22).** "A `harness` job on Linux, Windows and
> macOS" is overridden for macOS by owner decision `platforms-windows-wsl` in
> [the M1 decision log](../../stories/harness-DSH-M1/decisions.md): the M1 gate of record
> runs on two local lanes, Windows (native) and Linux under WSL. Which hosted lanes stay
> in `ci.yml` is decision M1-D-GATE-21 / M1-D-TOOL-5 (open; the plan's default keeps the
> hosted ubuntu and windows lanes and removes macOS). No stage schedules the
> `harness-replay` job yet; proposal `replay-gate-step-dh-s-16` (open) adds it to DH-S-16.
> The decision text above is unchanged (DC-2).

<a id="dh-adr-toolchain-13"></a>
### DH-ADR-TOOLCHAIN-13 Release: one binary per platform from GoReleaser, plus a module tag

- **Decision.** A `vX.Y.Z` tag builds `dsh` for Linux, macOS and Windows on amd64 and arm64, archives it with checksums, and creates the `src/harness/vX.Y.Z` module tag, the same way the `src/go` tag is created today. Version and commit are read from the build information the Go linker embeds. npm publication, the single-executable packer and the client build record are dropped. There are no desktop installers: the owner removed the desktop application on 2026-11-18, and signing and notarisation of this one binary are specified in [../libs/electron-builder.md](../libs/electron-builder.md). Python wheels consume this binary and are specified by their own agent.
- **Why.** One static binary replaces three of upstream's four channels.
- **Tradeoff.** `CGO_ENABLED=0` cross-compilation requires every harness dependency to be pure Go; a dependency that needs cgo forces per-platform build jobs.
- **Failure and retry.** Tag creation checks for an existing tag first and is safe to repeat. A failed GoReleaser run is re-run with `--clean`. A module version already served by the Go module proxy cannot be replaced; a bad release is superseded by the next patch version and marked with a `retract` line.
- **Serves.** [DH-O-14](../design.md#dh-o-14).
- **Verified by.** `DH-E-TOOLCHAIN-2`.

<a id="dh-adr-toolchain-14"></a>
### DH-ADR-TOOLCHAIN-14 Performance budgets as tests

- **Decision.** Micro-benchmarks use `testing.B`. User-path budgets (session open, long session fold, terminal throughput) are tests in a `bench` build tag that run the built binary in a fresh child process with a private temporary root and assert wall-clock and peak-memory budgets held as reviewed constants in source. Inputs are synthetic.
- **Why.** Upstream's rules are sound: measure the shipped artifact, never recorded user data, and never let an environment variable loosen a budget (`benchmarks/AGENTS.md:7-13`).
- **Workloads carried over from upstream's benchmark notes.** The budget numbers are measured again for Go; the workload shapes are kept.

  | Upstream note | Workload kept | Go form |
  |---|---|---|
  | `.agents/notes/implemented/testing/2026-09-04-session-open-performance-gate.md` | Opening one session of more than 100,000 events, after upstream regressed from 35 ms to 5 s | A required budget test; the store's own measurements are M2 and `DH-T-SQLITE-8` in [../libs/sqlite.md](../libs/sqlite.md) |
  | `…/2026-09-06-backend-continuation-performance.md` | Continuing a long, tool-heavy session: preparing each model request over a long history, running another tool-heavy turn, discovering inactive fork children | Three budget tests over one synthetic history |
  | `…/2026-09-28-session-corpus-performance.md` | Listing, content search and fork over a synthetic collection whose sizes follow the heavy-tailed distribution measured on 1,650 real sessions | The distribution is reused as test data; listing and search latency are M4 in [../libs/sqlite.md](../libs/sqlite.md) |
  | `…/2026-10-07-memory-limited-benchmarks.md` | The heaviest cases run again in a Linux control group limited to 4 GiB and to 8 GiB of memory with no swap; a case fails if it misses its budget or the kernel kills a process | The same rerun, in the scheduled workflow on Linux; the Go garbage collector reacts to a memory limit, so the rerun is as informative as upstream's |
  | `…/2026-10-07-scaled-benchmark-report.md` | An HTML report that scales each time to older laptops and typical cloud machines | **Changed.** Each budget test writes its measured time, peak memory and the runner's description to the run's artifact directory as a plain record. No scaling to other hardware and no HTML page: the scaling factors were upstream's estimates, and a number measured on the memory-limited rerun says more than a scaled one |

  The budget tests for the session workloads are written by the areas that own the paths measured (A05 and A06 for open, list, search and fork; A02 for continuation); this decision fixes how they run and what they may not do.
- **Tradeoff.** Wall-clock budgets on shared runners need headroom and can still flake. The memory-limited rerun needs a Linux runner that allows control groups.
- **Serves.** [DH-O-15](../design.md#dh-o-15).
- **Verified by.** `DH-T-TOOLCHAIN-12`.

<a id="dh-adr-toolchain-15"></a>
### DH-ADR-TOOLCHAIN-15 A documentation website generated from `docs/` by a docs-only tool; no document pairing, governance automation or contributor skills

This decision first dropped four things. The owner answered on 2026-11-18 (open decision OD-4): **keep the documentation website, drop the other three.** The decision is amended in place; its number is unchanged.

- **Decision.**
  1. **Kept: a documentation website.** A static site is generated from the Markdown under `docs/`. The Markdown files stay the single source: they are not copied or moved into a site tree, which is upstream's own rule for its site (`website/AGENTS.md:7-9`), and they stay readable on the code-hosting site. API reference is still `go doc`. The generator is Hugo, a static-site generator written in Go; the comparison with keeping a non-Go generator, and the verified facts, are in [../libs/build-test-toolchain.md](../libs/build-test-toolchain.md#documentation-site) under `DH-LIB-DOCSSITE`.
  2. **The site is isolated from the product.** The generator is pinned in a module of its own, outside the product's `go.mod` and outside the tools module of [DH-ADR-TOOLCHAIN-11](#dh-adr-toolchain-11), and runs in a workflow of its own. Building, testing or releasing `dsh` never runs it. No Node, npm package or JavaScript bundler is installed for it. Mermaid diagrams are drawn in the reader's browser by one vendored script file that only the site serves; it is not embedded in `dsh`.
  3. **Not carried over from upstream's site in the first version:** the second locale; the pan-and-zoom viewer for diagrams and images; the table filter; the code-group adapter; the copy-as-Markdown actions with the raw-Markdown twin of each page and the `llms.txt` index; local search; pull-request previews (`website/AGENTS.md:15-25`; `.github/workflows/build-preview-cloudflare.yml`). Each is presentation on top of the pages and can be added later without changing this decision.
  4. **Dropped, confirmed by the owner:** the English/Chinese pairing gates and word budgets; Agent Note gates, weighted approval, issue lifecycle and stale-pull-request automation; and upstream's contributor skills (`.agents/skills`), which are not copied. The engineering rules worth keeping from the skills (test isolation, cleanup ownership) are restated in [DH-ADR-TOOLCHAIN-2](#dh-adr-toolchain-2) and [DH-ADR-TOOLCHAIN-5](#dh-adr-toolchain-5).
- **Why.** The owner wants a site readers can browse. A generator written in Go gives one without bringing back the ecosystem this design removes: no second package manager, lock file or bundler in the repository. The three dropped parts are not product behaviour, and each is a toolchain to maintain.
- **Tradeoff.** One more pinned tool, with a large dependency graph of its own and frequent releases, to keep current. No Mermaid syntax gate: a diagram that does not parse is seen on the page, not in CI. The features in item 3 are missing until someone adds them. The three dropped parts stay a recorded exception to "covers all of upstream" ([DH-ADR-1](../design.md#dh-adr-1)).
- **Failure and retry.** The site build writes into a fresh output directory. If it fails, the docs job fails and nothing is published; the previously published site stays as it was. Publishing replaces the whole site in one deployment, so there is no half-updated site, and a re-run is safe. A failing site build blocks a change to `docs/`, never a product release, because the workflows are separate.
- **Serves.** [DH-O-16](../design.md#dh-o-16), [DH-O-14](../design.md#dh-o-14).
- **Verified by.** `DH-T-TOOLCHAIN-13`. `tools/harness-trace verify` already fails on a broken relative link between design documents.

### What each decision serves

The purpose in words. Each record's "Serves" line names the outcome (`DH-O-n`) as mapped in [../design.md](../design.md) §7.2.

| Decisions | Serves |
|---|---|
| `DH-ADR-TOOLCHAIN-1`, `-4`, `-5`, `-6` | A passing run means the assembled product behaves as recorded, and a missing proof is visible |
| `DH-ADR-TOOLCHAIN-2`, `-3` | Concurrency defects and hostile inputs are found before release |
| `DH-ADR-TOOLCHAIN-7`, `-8`, `-9` | Rules are enforced mechanically from the first commit, with no inherited exceptions |
| `DH-ADR-TOOLCHAIN-10` | The model-visible and operator-visible catalogs always match the running product |
| `DH-ADR-TOOLCHAIN-11`, `-12`, `-13` | A reproducible, verified build and a release that can be repeated safely |
| `DH-ADR-TOOLCHAIN-14` | Performance regressions on user paths block a merge |
| `DH-ADR-TOOLCHAIN-15` | Readers get a browsable documentation site without a second ecosystem in the build; a recorded scope exception for the three dropped parts |

### Deliverables

The decisions above say how the project is built and checked; the deliverables below are
the pieces of work that make them true, so that the implementation plan can schedule
each one and the traceability tool can check that it is scheduled. They were added after
the audit ([../audit/q1-consistency.md](../audit/q1-consistency.md), finding Q1-3, and
plan problems G-1 and G-7). Signing of the one binary is delivered in
[../libs/electron-builder.md](../libs/electron-builder.md). Each deliverable is done only
when every proof it names **has passed**; a manual check never closes one.

<a id="dh-d-toolchain-1"></a>
#### DH-D-TOOLCHAIN-1 Three modules, one workspace, reasoned pins

- **Result.** `src/go`, `src/sdk` and `src/harness` build together through a root
  `go.work`, and each also builds alone, as a release does. Every later deliverable
  compiles against this.
- **Outcome.** [DH-O-15](../design.md#dh-o-15), [DH-O-14](../design.md#dh-o-14).
  **Priority.** P0.
- **How.** Raise the `go` line of `src/go` to 1.26.0; create the two new modules; add
  `go.work`; put development tools in a separate tools module; give every `replace`,
  `exclude`, version floor and tool pin a reason comment
  ([DH-ADR-26](../design.md#dh-adr-26), [DH-ADR-TOOLCHAIN-11](#dh-adr-toolchain-11)).
- **Components and files.** `src/go/go.mod`, `src/sdk/go.mod`, `src/harness/go.mod`,
  `go.work`, `tools/go.mod`.
- **Depends on.** Nothing.
- **Done when.** `DH-T-TOOLCHAIN-11` has passed.

> **DC-3 override note (M1 story, 2026-11-22).** "`tools/go.mod`" in Components is
> overridden by decision M1-D-TOOL-2 (a) in
> [the M1 decision log](../../stories/harness-DSH-M1/decisions.md): the tools module is
> `tools/harness-gate/go.mod`, the gate runner's own module, which is outside `go.work`
> and run with `GOWORK=off`. It pins staticcheck (`honnef.co/go/tools` v0.8.2) and
> govulncheck (`golang.org/x/vuln` v1.10.0) as `tool` lines, each with a reason comment
> (gap-closure plan step 13.4.1). Under M1-D-TOOL-4 (A) the cordis module's Gherkin
> suites are a nested test-only module, `src/go/test/e2e/go.mod`, outside `go.work` and
> outside the three product modules of RM-1, so `src/go/go.mod` requires nothing (step
> 13.4.2). The decision text above is unchanged (DC-2).

<a id="dh-d-toolchain-2"></a>
#### DH-D-TOOLCHAIN-2 The gate runner and the gates it calls

- **Result.** One command runs every required check in a fixed order and names the one
  that failed: format, vet, own analyzers, staticcheck, module tidy and verify,
  vulnerability check, cross-compile, unit tests with the race detector in shuffled
  order, coverage against the 85% floor, golden files and performance budgets.
- **Outcome.** [DH-O-15](../design.md#dh-o-15). **Priority.** P0.
- **How.** A small sequential runner `tools/harness-gate`
  ([DH-ADR-TOOLCHAIN-9](#dh-adr-toolchain-9)); the analyzer binary on `go/analysis`
  with no baseline file ([DH-ADR-TOOLCHAIN-8](#dh-adr-toolchain-8)); race, shuffle and
  `testing/synctest` defaults ([DH-ADR-TOOLCHAIN-2](#dh-adr-toolchain-2)); merged
  coverage across processes ([DH-ADR-TOOLCHAIN-7](#dh-adr-toolchain-7)); the golden
  helper ([DH-ADR-TOOLCHAIN-4](#dh-adr-toolchain-4)); budget tests
  ([DH-ADR-TOOLCHAIN-14](#dh-adr-toolchain-14)).
- **Components and files.** `tools/harness-gate`, `tools/analyzers`, the shared test
  helpers under `src/harness/internal/testkit`.
- **Depends on.** [DH-D-TOOLCHAIN-1](#dh-d-toolchain-1).
- **Done when.** `DH-T-TOOLCHAIN-2`, `DH-T-TOOLCHAIN-4`, `DH-T-TOOLCHAIN-7`,
  `DH-T-TOOLCHAIN-8`, `DH-T-TOOLCHAIN-9` and `DH-T-TOOLCHAIN-12` have passed.

<a id="dh-d-toolchain-3"></a>
#### DH-D-TOOLCHAIN-3 Test layers: replay, scenarios and fuzz seeds

- **Result.** Replay and scenario tests start the built binary, a recorded session can be
  replayed without a key, a committed fuzz crasher runs as an ordinary test, and a
  required scenario that skips fails the run.
- **Outcome.** [DH-O-15](../design.md#dh-o-15). **Priority.** P0.
- **How.** Build tags `replay` and `scenario`
  ([DH-ADR-TOOLCHAIN-1](#dh-adr-toolchain-1)); the replay provider and fixture format
  ([DH-ADR-TOOLCHAIN-5](#dh-adr-toolchain-5)); `DSH_REQUIRE_SCENARIOS`
  ([DH-ADR-TOOLCHAIN-6](#dh-adr-toolchain-6)); fuzz targets and `testdata/fuzz`
  ([DH-ADR-TOOLCHAIN-3](#dh-adr-toolchain-3)).
- **Components and files.** `src/harness/test/replay`, `src/harness/test/scenario`,
  `testdata/fuzz` beside each parser.
- **Depends on.** [DH-D-TOOLCHAIN-2](#dh-d-toolchain-2).
- **Done when.** `DH-T-TOOLCHAIN-1`, `DH-T-TOOLCHAIN-3`, `DH-T-TOOLCHAIN-5` and
  `DH-T-TOOLCHAIN-6` have passed, and [DH-E-TOOLCHAIN-1](#dh-e-toolchain-1) has passed.

<a id="dh-d-toolchain-4"></a>
#### DH-D-TOOLCHAIN-4 Continuous integration on three operating systems

- **Result.** Every change runs the gate runner on Linux, macOS and Windows with no key;
  credentialed scenarios run in a separate workflow whose preflight fails when a secret
  or a required scenario is missing. No platform lane can be switched off without a
  failing job (`UP-DEBT-found.Q3.5`, `UP-DEBT-found.Q3.6`).
- **Outcome.** [DH-O-15](../design.md#dh-o-15). **Priority.** P0.
- **How.** A `harness` job and a `harness-replay` job in `ci.yml`; a scheduled and
  manually dispatched scenario workflow, never on `pull_request_target`
  ([DH-ADR-TOOLCHAIN-12](#dh-adr-toolchain-12), [DH-ADR-TOOLCHAIN-6](#dh-adr-toolchain-6)).
- **Components and files.** `.github/workflows/ci.yml`, a new
  `.github/workflows/harness-scenarios.yml`.
- **Depends on.** [DH-D-TOOLCHAIN-2](#dh-d-toolchain-2),
  [DH-D-TOOLCHAIN-3](#dh-d-toolchain-3).
- **Done when.** [DH-E-TOOLCHAIN-1](#dh-e-toolchain-1) and
  [DH-E-TOOLCHAIN-2](#dh-e-toolchain-2) have passed in these workflows.

> **DC-3 override note (M1 story, 2026-11-22).** "No platform lane can be switched off
> without a failing job" is overridden for macOS by owner decision
> `platforms-windows-wsl` in [the M1 decision log](../../stories/harness-DSH-M1/decisions.md).
> The macOS lane leaves by that recorded decision, not by switching it off. For the
> Windows (native) and Linux (WSL) lanes the rule still holds. "Every change runs the gate
> runner on Linux, macOS and Windows" reads, for M1, "on Windows (native) and Linux under
> WSL". The text above is unchanged (DC-2).

<a id="dh-d-toolchain-5"></a>
#### DH-D-TOOLCHAIN-5 Release rehearsal

- **Result.** A snapshot release builds one pure-Go `dsh` per platform and architecture
  with checksums, publishes nothing, and each archive that matches a runner starts and
  answers a keyless replay.
- **Outcome.** [DH-O-14](../design.md#dh-o-14). **Priority.** P0.
- **How.** GoReleaser in snapshot mode, `CGO_ENABLED=0`, version read from the build
  information ([DH-ADR-TOOLCHAIN-13](#dh-adr-toolchain-13),
  [DH-ADR-15](../design.md#dh-adr-15)).
- **Components and files.** `.goreleaser.yaml` (a `dsh` build added beside the existing
  one), `.github/workflows/release.yml`.
- **Depends on.** [DH-D-TOOLCHAIN-1](#dh-d-toolchain-1),
  [DH-D-TOOLCHAIN-4](#dh-d-toolchain-4).
- **Done when.** [DH-E-TOOLCHAIN-2](#dh-e-toolchain-2) has passed.

<a id="dh-d-toolchain-6"></a>
#### DH-D-TOOLCHAIN-6 Published release

- **Result.** A `vX.Y.Z` tag publishes the archives, the checksum file and the licence
  notices as GitHub release assets and creates the three module tags on one commit; a
  re-run against an existing tag creates nothing twice.
- **Outcome.** [DH-O-14](../design.md#dh-o-14). **Priority.** P1.
- **How.** The same configuration as [DH-D-TOOLCHAIN-5](#dh-d-toolchain-5) with
  publishing on, the vulnerability check before publishing, and the tag-exists check of
  [DH-ADR-TOOLCHAIN-13](#dh-adr-toolchain-13) ([DH-ADR-24](../design.md#dh-adr-24),
  [DH-ADR-26](../design.md#dh-adr-26)).
- **Components and files.** `.goreleaser.yaml`, `.github/workflows/release.yml`.
- **Depends on.** [DH-D-TOOLCHAIN-5](#dh-d-toolchain-5).
- **Done when.** `DH-T-TOOLCHAIN-14` and [DH-E-TOOLCHAIN-2](#dh-e-toolchain-2) have
  passed.

<a id="dh-d-toolchain-7"></a>
#### DH-D-TOOLCHAIN-7 Generated catalogs

- **Result.** The catalogs of tools, events, configuration, services, commands and
  persisted types are written from the running registries and always equal the
  committed files.
- **Outcome.** [DH-O-14](../design.md#dh-o-14). **Priority.** P1.
- **How.** One generator that boots each shipped composition in-process, run by
  `go generate` ([DH-ADR-TOOLCHAIN-10](#dh-adr-toolchain-10),
  [DH-ADR-ARCH-12](architecture.md#dh-adr-arch-12)).
- **Components and files.** `src/harness/tools/catalog`, `docs/reference/`.
- **Depends on.** [DH-D-TOOLCHAIN-2](#dh-d-toolchain-2).
- **Done when.** `DH-T-TOOLCHAIN-10` has passed.

<a id="dh-d-toolchain-8"></a>
#### DH-D-TOOLCHAIN-8 Documentation website

- **Result.** A static site is built from the Markdown under `docs/` by a Go-only tool,
  in a workflow of its own, and publishes the generated catalogs.
- **Outcome.** [DH-O-16](../design.md#dh-o-16), [DH-O-14](../design.md#dh-o-14).
  **Priority.** P1.
- **How.** Hugo pinned in its own module, one vendored diagram script, no Node
  ([DH-ADR-TOOLCHAIN-15](#dh-adr-toolchain-15), [DH-ADR-10](../design.md#dh-adr-10)).
- **Components and files.** `website/` (configuration and templates only),
  `.github/workflows/docs.yml`.
- **Depends on.** [DH-D-TOOLCHAIN-7](#dh-d-toolchain-7).
- **Done when.** `DH-T-TOOLCHAIN-13` has passed.

<a id="dh-d-toolchain-9"></a>
#### DH-D-TOOLCHAIN-9 Operator guide and safety page

- **Result.** An operator can install, start, upgrade and uninstall `dsh` from the guide
  alone, knows which software to install for which capability, and reads on the safety
  page and in the first-start section that the harness is unaudited
  ([DH-ADR-SECURITY-12](security.md#dh-adr-security-12), `UP-DEBT-found.Q3.1`).
- **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7).
  **Priority.** P1.
- **How.** Pages under `docs/operator/` and `docs/safety.md`, published by the website;
  every command line in them is taken from the generated command catalog.
- **Components and files.** `docs/operator/`, `docs/safety.md`.
- **Depends on.** [DH-D-TOOLCHAIN-7](#dh-d-toolchain-7),
  [DH-D-TOOLCHAIN-8](#dh-d-toolchain-8).
- **Done when.** `DH-T-TOOLCHAIN-15` and `DH-T-SECURITY-1` have passed.

<a id="dh-d-toolchain-10"></a>
#### DH-D-TOOLCHAIN-10 Known-limits pages

- **Result.** Every platform limit and every scope boundary that the ledger carries is
  written for operators on a known-limits page, with the decision that states it, so a
  limit is a stated contract and not a surprise.
- **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-16](../design.md#dh-o-16).
  **Priority.** P1.
- **How.** One page per area family under `docs/limits/`, copied from the debt
  registers ([../debt/carried-core.md](../debt/carried-core.md),
  [../debt/carried-host-ui-experimental.md](../debt/carried-host-ui-experimental.md));
  where the product has a surface that can show a limit, the area that owns the surface
  adds the text and its test.
- **Components and files.** `docs/limits/`.
- **Depends on.** [DH-D-TOOLCHAIN-8](#dh-d-toolchain-8).
- **Done when.** `DH-T-TOOLCHAIN-16` has passed.

## 12. Proof

Local tests (none written or run; each is a requirement on the future test suite):

| ID | Proves | Asserts |
|---|---|---|
| `DH-T-TOOLCHAIN-1` | `DH-ADR-TOOLCHAIN-1` | Every `_test.go` file under `test/replay` and `test/scenario` carries its build tag, and no test in those packages starts the product other than through the built binary path |
| `DH-T-TOOLCHAIN-2` | `DH-ADR-TOOLCHAIN-2` | A seeded data race in a fixture package makes the unit command exit non-zero; a timer test under `testing/synctest` finishes without real waiting |
| `DH-T-TOOLCHAIN-3` | `DH-ADR-TOOLCHAIN-3` | Each listed boundary parser has a fuzz target, and a committed crasher file is executed by plain `go test` |
| `DH-T-TOOLCHAIN-4` | `DH-ADR-TOOLCHAIN-4` | The golden helper fails on a difference, rewrites only with `-update`, and refuses `-update` when `CI` is set |
| `DH-T-TOOLCHAIN-5` | `DH-ADR-TOOLCHAIN-5` | Two replays of one fixture give byte-identical normalised logs, and the fixture tree is unchanged afterwards; a failed record leaves the committed fixture unchanged |
| `DH-T-TOOLCHAIN-6` | `DH-ADR-TOOLCHAIN-6` | With a scenario listed as required and its credential absent, the run fails instead of skipping |
| `DH-T-TOOLCHAIN-7` | `DH-ADR-TOOLCHAIN-7` | Lines executed only inside the replayed child process appear in the merged profile; a package below the floor fails the check |
| `DH-T-TOOLCHAIN-8` | `DH-ADR-TOOLCHAIN-8` | Each own analyzer rejects a seeded violation and accepts a reasoned exception (`analysistest`); an exception without a reason fails |
| `DH-T-TOOLCHAIN-9` | `DH-ADR-TOOLCHAIN-9` | The gate runner exits non-zero when any gate fails and names it; the list of gates in CI equals the runner's list |
| `DH-T-TOOLCHAIN-10` | `DH-ADR-TOOLCHAIN-10` | Regenerated catalogs equal the committed files; a tool registered in a fixture composition appears in the output |
| `DH-T-TOOLCHAIN-11` | `DH-ADR-TOOLCHAIN-11` | `go mod tidy -diff` is empty; every `replace`, `exclude` and tool pin has a reason comment; the product builds with `GOWORK=off` |
| `DH-T-TOOLCHAIN-12` | `DH-ADR-TOOLCHAIN-14` | A budget test fails when a fixture workload exceeds its constant; setting an environment variable does not change the budget |
| `DH-T-TOOLCHAIN-13` | `DH-ADR-TOOLCHAIN-15` | The site builds from `docs/` on a runner with Go and no Node, with warnings treated as errors; every Markdown file under `docs/` that the site configuration does not exclude has a page; an internal link in the built pages resolves to a built page or anchor; the module graph of `src/harness` contains neither the generator nor a bundler, and no workflow step for `dsh` invokes the site build; the site sources contain no `package.json` and call no asset-bundling function |
| `DH-T-TOOLCHAIN-14` | `DH-ADR-TOOLCHAIN-13` | Run against a local fake release target: the release configuration yields six archives, one checksum file and the licence notices; the vulnerability check runs before any publish step; a second run against an existing tag creates no tag and no release; the three module tags point at one commit |
| `DH-T-TOOLCHAIN-15` | `DH-ADR-TOOLCHAIN-15` | The operator guide has sections for install, first start, upgrade and uninstall and lists the runtime prerequisites; every `dsh` command line it quotes parses against the generated command catalog |
| `DH-T-TOOLCHAIN-16` | `DH-ADR-TOOLCHAIN-15`, `DH-ADR-40` | Every carried ledger row of class `platform-limit` or `scope-boundary` appears by unit ID on exactly one known-limits page together with its decision ID, and a page entry that names no carried row fails |

Real-environment scenarios (not run; a local mock is not a substitute):

<a id="dh-e-toolchain-1"></a>
#### DH-E-TOOLCHAIN-1 Record against the real model, then replay without a key

- **Given** a clean checkout on a hosted Linux runner, a `dsh` binary built from that commit, a model API key supplied at run time as a secret, and one scenario directory that holds only a manifest and a workspace seed.
- **When** the controller runs the replay package in `record` mode for that scenario, then removes the key from the environment and runs it in `replay` mode twice.
- **Then** record writes a session log and, for a file-changing scenario, the product's workspace equals `workspace.expected/`; both replays pass with no network access to the model host; the fixture directory has no differences after the replays; the run report lists the scenario as executed, not skipped.
- **Cleanup.** Temporary home and workspace directories are removed by the test. On failure, keep the child's standard error and the unnormalised log as job artifacts.

<a id="dh-e-toolchain-2"></a>
#### DH-E-TOOLCHAIN-2 A release rehearsal yields a runnable binary on every platform

- **Given** hosted Linux, Windows and macOS runners and a commit on which the `harness` and `harness-replay` jobs pass.
- **When** the release workflow runs GoReleaser in snapshot mode (nothing published), and each runner downloads the archive for its platform, verifies it against the checksum file and runs `dsh --version` and one keyless headless replay scenario.
- **Then** six archives and one checksum file exist; each binary reports the commit it was built from and no "modified" marker; the scenario passes on all three systems. Scope: this proves the three archives that match the runners' processors; the other three are built and checksummed but not executed.
- **Cleanup.** Snapshot artifacts expire with the workflow run; no tag or release is created. If a platform fails, keep its archive and log.

> **DC-3 override note (M1 story, 2026-11-22): restated for two executed systems.** Under
> owner decision `platforms-windows-wsl` in
> [the M1 decision log](../../stories/harness-DSH-M1/decisions.md), and until
> `macos-return-stage` names a stage, this scenario reads:
> **Given** a Linux runner and a Windows runner (hosted, or the local Linux (WSL) and
> Windows lanes) and a commit on which the gate passes on both. **When** the snapshot
> runs and each system verifies the archive for its platform against the checksum file
> and runs `dsh --version` and one keyless headless replay scenario. **Then** six
> archives and one checksum file exist; each binary reports its commit and no "modified"
> marker; the scenario passes on both systems. **Scope:** this proves the two archives
> that match the two systems' processors; the other four, the macOS archives included,
> are built and checksummed but not executed. macOS is not recorded as a pass or a skip.
> The scenario text above is unchanged (DC-2).

## 13. Open questions

1. **Coverage floor value** for [DH-ADR-TOOLCHAIN-7](#dh-adr-toolchain-7). Settled by the owner on 2026-11-18 (OD-8): 85% statement coverage per package, with no exemption list.
2. **Go version.** The cordis module declares `go 1.24`; `testing/synctest` needs 1.25. The harness module's `go` line is `go 1.26.0`: ten of the modules it requires declare that line in their own `go.mod` (read on 2026-11-19 with `go mod download -json`), and nothing the design uses needs more. The evidence table is recorded with CF-4 in [../design.md](../design.md) §8. The tools module and the documentation-site module have floors of their own and do not raise it ([DH-ADR-TOOLCHAIN-11](#dh-adr-toolchain-11)).
3. **Browser scenarios.** Upstream compares 79 entries under `snapshots/web` plus UI-only expected output in Chromium (`docs/testing.md:15`). With server-rendered HTML ([DH-ADR-3](../design.md#dh-adr-3)), most of it becomes golden HTML tests without a browser. Settled: the remaining island scenarios run on chromedp against a Chromium-family browser, and Firefox and Safari are driven through W3C WebDriver ([../libs/playwright.md](../libs/playwright.md), `DH-ADR-PLAYWRIGHT-1` and `-2`).
4. **cgo.** [DH-ADR-TOOLCHAIN-13](#dh-adr-toolchain-13) assumes pure-Go dependencies, and the owner made that a rule for the core binary (OD-9). With the desktop shell gone there is no webview choice; the SQLite and pseudo-terminal choices (agents L5, L17) decide whether the assumption holds.
5. **Signing, the optional update check and Python wheels** are specified by agents L14 and L18; this document only fixes what they consume. Desktop installers no longer exist.
6. **Persistence gates** (`verify-persistence-*`, the snapshot corpus policy) guard format compatibility; their Go form belongs to the persistence area (agent F5).
7. **Upstream contributor skills** (`.agents/skills`, 22 skills). Their test-isolation rules are restated in the decisions above; the error-handling and session-projection skills describe product rules that the owning area specifications must judge; the rest is upstream contributor workflow and is dropped by [DH-ADR-TOOLCHAIN-15](#dh-adr-toolchain-15), as the owner confirmed. Whether the Go repository wants its own agent instructions is not decided here.
8. **Documentation site details** left to the build: where the site is hosted, whether Hugo's own link resolution handles the `.md` links the documents use or a small link template is needed, and whether search or the Markdown twins are added. See [../libs/build-test-toolchain.md](../libs/build-test-toolchain.md), section 7.

## 14. Units dispositioned by this document

Packages and directories: `UP-PKG-root`, `UP-PKG-scripts`, `UP-PKG-.github`, `UP-PKG-snapshots`, `UP-PKG-benchmarks`, `UP-PKG-website`, `UP-PKG-.agents.skills`. Tech debt: the four `UP-DEBT-baseline.*` units, `UP-DEBT-marker.scripts/gen-doc-graphs.ts:1256` and `UP-DEBT-pin.overrides.extract-zip_yauzl`. The 58 dependency units and the three remaining pin units are dispositioned by [../libs/build-test-toolchain.md](../libs/build-test-toolchain.md).
