# Build-only and test-only dependencies: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Owner: agent F7. Nothing here has been built or run.
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The owner keeps
> the documentation website, so `DH-LIB-DOCSSITE` is no longer dropped: it is a
> static-site generator written in Go, pinned as a docs-only tool, and the ten
> dependencies of upstream's site have new dispositions (section 2, "Documentation
> site").

| | |
|---|---|
| **Library IDs** | `DH-LIB-TESTRUNNER`, `DH-LIB-COVERAGE`, `DH-LIB-PROPTEST`, `DH-LIB-GOLDEN`, `DH-LIB-DOMTEST`, `DH-LIB-BUNDLER`, `DH-LIB-SCRIPTRUNNER`, `DH-LIB-LINTER`, `DH-LIB-ANALYSIS`, `DH-LIB-VULNCHECK`, `DH-LIB-TYPEDECLS`, `DH-LIB-PKGMANAGER`, `DH-LIB-NOTICES`, `DH-LIB-GITHOOKS`, `DH-LIB-DOCSSITE`, `DH-LIB-LSPFIXTURE`, `DH-LIB-RELEASER` |
| **Upstream units** | 58 `UP-DEP` units and 3 `UP-DEBT-pin` units, listed in section 9 |
| **Upstream version** | Per row in section 2, as resolved in `pnpm-lock.yaml` |
| **Kind** | Build-only and test-only. Five are also declared as runtime dependencies of test-support or tooling packages (`vitest`, `@vitest/spy`, `@testing-library/dom`, `@testing-library/react`, `tsx`); none is loaded by the shipped product |
| **Used by** | The workspace root, `scripts/`, `website/`, and test directories of most package groups |
| **Go choice** | The standard Go toolchain for most groups; seven verified modules used as tools or test helpers, one of them the documentation-site generator, which is kept apart from the product; the declaration packages are not needed |
| **Related areas** | [../upstream/toolchain.md](../upstream/toolchain.md) holds the decisions (`DH-ADR-TOOLCHAIN-n`) these choices implement |

## 1. What they are and why upstream needs them

A JavaScript project does not come with a build. Upstream assembles one from separate npm packages: a type checker, bundlers that turn many source files into loadable output, a test runner, a coverage instrument, a linter with plugins, a duplication detector, a package manager with its own policy file, a git hook manager, and a documentation site generator. The 58 dependencies in this document are those parts. None of them runs inside the shipped `dsh` product.

They matter to the Go design for one reason: each one stands for a job that must still be done, be done by the Go toolchain for free, or be deliberately dropped. This document groups them by job. Each group is one library item (`DH-LIB-<KEY>`) with one Go answer, so 58 rows collapse into 17 choices.

Two terms used below. A **tool** is a program run during development or CI; it is never linked into `dsh`. A **test helper** is a package imported only by `_test.go` files. Both are kept out of the product's dependency graph where possible ([DH-ADR-TOOLCHAIN-11](../upstream/toolchain.md#dh-adr-toolchain-11)).

## 2. How upstream uses them

One row per dependency. "Row" is the ledger disposition written for the unit.

### Test runner, fakes and coverage

| Dependency (version) | Upstream use (`path:line`) | Go answer | Row |
|---|---|---|---|
| `vitest` (4.1.11) | Runs every lane through eight configuration files; test API imported by every spec (`vitest.config.ts:6`, `package.json:61-85`) | `go test` and the `testing` package: `DH-LIB-TESTRUNNER` | replaced |
| `@vitest/spy` (4.1.11) | Mock functions inside the remote-service test double (`packages/test-support/remote-mock/src/remote-mock.ts:4`) | Hand-written fakes that implement Go interfaces: `DH-LIB-TESTRUNNER` | replaced |
| `@vitest/coverage-v8` (4.1.11) | V8 coverage provider for the per-file 100% gate (`vitest.config.ts:268`) | `go test -cover` and `go build -cover`: `DH-LIB-COVERAGE` | replaced |
| `istanbul-lib-report` (3.0.1) | Base class of a custom reporter that prints uncovered locations (`scripts/coverage-uncovered-locations.cjs:18`) | `go tool cover` and `go tool covdata`: `DH-LIB-COVERAGE` | replaced |
| `fast-check` (4.9.0) | Property-based tests in five suites (`packages/core/agent-loop/tests/properties.spec.ts:25`, `packages/core/session/tests/properties.spec.ts:11`, `packages/core/tools/tests/properties.spec.ts:9`, `packages/llm/llm/tests/properties.spec.ts:11`, `packages/schedule/schedule/tests/recurrence.spec.ts:2`) | Native fuzz targets and `testing/quick`: `DH-LIB-PROPTEST` | replaced |

### Browser-less user-interface tests

| Dependency (version) | Upstream use | Go answer | Row |
|---|---|---|---|
| `jsdom` (29.1.1) | A simulated browser document for client component specs, selected per file (`vitest.config.ts:219`, `apps/desktop/tests/preload-app.spec.ts:3`) | Render the handler with `net/http/httptest`, parse with `golang.org/x/net/html`: `DH-LIB-DOMTEST` | replaced |
| `@testing-library/dom` (10.4.2) | Queries by role and text over that document (`packages/test-support/client-runtime/src/index.ts:24`, `packages/client/shortcuts/tests/dom.client.spec.ts:3`) | Small query helpers over the parsed tree: `DH-LIB-DOMTEST` | replaced |
| `@testing-library/react` (16.3.3) | Mounts React components in tests; a development dependency of 33 packages (`package.json:259`) | No React components exist ([DH-ADR-3](../design.md#dh-adr-3)); templates are rendered and asserted the same way: `DH-LIB-DOMTEST` | replaced |

### Bundlers and script runner

| Dependency (version) | Upstream use | Go answer | Row |
|---|---|---|---|
| `tsdown` (0.22.2) | Bundles each package's compiled JavaScript into `lib/` in a Host pass and a Client pass (`tsdown.config.ts:21-36`, `package.json:28`) | `go build` links one binary: `DH-LIB-BUNDLER` | replaced |
| `vite` (5.4.21, 6.4.3, 8.2.2) | Web and desktop front-end build and development server, and the engine under vitest and VitePress (`apps/web/package.json:26`, `website/package.json:19`, `package.json:301`); three major versions are installed at once | Static assets are embedded with the `embed` package: `DH-LIB-BUNDLER` | replaced |
| `vite-tsconfig-paths` (6.1.1) | Makes tests resolve workspace imports to source instead of built output (`vitest.config.ts:18-22`) | Not needed: Go resolves imports from the module graph | not-applicable |
| `lightningcss` (1.33.0) | Hashes CSS-module class names and rewrites `url()` inside client bundles (`packages/client/tsdown.client.ts:26`, `apps/web/katex-fonts.ts:3`) | Not needed: plain stylesheets are embedded and served as files | not-applicable |
| `tsx` (4.23.13) | Runs TypeScript directly: every repository script and the source launch of the CLI (`package.json:20-23`, `package.json:215`) | `go run`: `DH-LIB-SCRIPTRUNNER` | replaced |

### Linters

| Dependency (version) | Upstream use | Go answer | Row |
|---|---|---|---|
| `oxlint` (1.81.0) | The linter, wrapped by `scripts/run-oxlint.ts` (`package.json:57`) | `gofmt`, `go vet` and staticcheck: `DH-LIB-LINTER` | replaced |
| `oxlint-tsgolint` (7.0.2001) | Type-aware rule backend (`.oxlintrc.json:9`) | `go vet` and staticcheck are type-aware by construction: `DH-LIB-LINTER` | replaced |
| `@stylistic/eslint-plugin` (5.10.0) | Formatting rules such as a 140-column limit (`.oxlintrc.json:321`) | `gofmt` fixes layout; no line-length rule: `DH-LIB-LINTER` | replaced |
| `eslint-plugin-sonarjs` (4.2.0) | Duplicate-branch and identical-expression rules (`.oxlintrc.json:240-250`) | staticcheck has checks of the same kind (for example `SA4000`, identical expressions on both sides of an operator): `DH-LIB-LINTER` | replaced |
| `jscpd` (5.1.2) | Cross-file clone detection over `packages` and `scripts` (`package.json:60`) | Dropped by [DH-ADR-TOOLCHAIN-8](../upstream/toolchain.md#dh-adr-toolchain-8) | dropped |

### Package management and publication checks

| Dependency (version) | Upstream use | Go answer | Row |
|---|---|---|---|
| `pnpm` (11.28.5) | The package manager, itself pinned as a development dependency (`package.json:7`, `package.json:292`) | Go modules (`go.mod`, `go.sum`, `go.work`): `DH-LIB-PKGMANAGER` | replaced |
| `@yarnpkg/cli-dist` (4.18.0) | Declared at `package.json:271`; no import was found. An archived note says it supplies a modern Yarn CLI to a generated-project test (`.agents/notes/archived/process/2026-07-26-pnpm-action-setup-for-symmetric-ci-caching.md:14`) | Not needed: there is no second package manager to test against | not-applicable |
| `publint` (0.3.24) | Lints the published form of every npm package (`scripts/publint-all.ts:11`) | Not needed: nothing is published to npm | not-applicable |
| `smol-toml` (1.9.0) | Reads `pyproject.toml` files in the notices generator (`scripts/gen-third-party-notices.ts:14`) | `go-licenses` reads the module graph: `DH-LIB-NOTICES` | replaced |
| `spdx-expression-parse` (5.0.0) | Parses licence expressions in the same generator (`scripts/gen-third-party-notices.ts:15`) | `DH-LIB-NOTICES` | replaced |

### Git hooks

| Dependency (version) | Upstream use | Go answer | Row |
|---|---|---|---|
| `lefthook` (2.1.12) | Runs staged-file checks before commit and a type check before push; installed by a `postinstall` script (`lefthook.yml:1-3`, `package.json:222`) | One checked-in hook script, enabled by choice: `DH-LIB-GITHOOKS` | replaced |

### Documentation site

The owner keeps the documentation website
([DH-ADR-TOOLCHAIN-15](../upstream/toolchain.md#dh-adr-toolchain-15), as amended on
2026-11-18). The library item is `DH-LIB-DOCSSITE`: Hugo, a static-site generator
written in Go, run as a docs-only tool. Each of the ten dependencies of upstream's site
is judged against that.

| Dependency (version) | Upstream use | Go answer | Row |
|---|---|---|---|
| `vitepress` (1.6.4) | Static site generator that publishes selected pages of `docs/` (`website/package.json:7`, `website/AGENTS.md:7`) | Hugo generates the site from the same Markdown: `DH-LIB-DOCSSITE` | replaced |
| `vitepress-plugin-mermaid` (2.0.17) | Renders Mermaid diagrams in the site (`website/.vitepress/config.ts:8`) | One template of the site (a code-block render hook) that turns a `mermaid` fence into an element the Mermaid script draws: `DH-LIB-DOCSSITE` | replaced |
| `vue` (3.5.42) | The site theme's components (`website/.vitepress/theme/index.ts:4`) | The site's layouts are Go templates, the generator's own template language: `DH-LIB-DOCSSITE` | replaced |
| `mermaid` (11.17.2) | Site rendering, and the `verify-mermaid` gate that parses every diagram (`scripts/verify-mermaid.ts:82`) | Kept for site rendering: its published browser file is vendored into the site sources and served only by the site. The syntax gate is not kept ([DH-ADR-TOOLCHAIN-15](../upstream/toolchain.md#dh-adr-toolchain-15)) | ported |
| `@panzoom/panzoom` (4.6.2) | Pan and zoom in the diagram and image viewer (`website/.vitepress/theme/media-viewer.ts:2`) | The viewer is not carried over in the first version of the site (item 3 of DH-ADR-TOOLCHAIN-15); the browser's own zoom is the substitute | dropped |
| `@braintree/sanitize-url` (7.1.2), `cytoscape` (3.34.2), `cytoscape-cose-bilkent` (4.1.0), `dayjs` (1.11.23), `debug` (4.4.3) | Declared with exact versions at `website/package.json:12-17`; no import in `website/` was found. Inference: they pin Mermaid's own dependencies for the site bundler. The npm manifest of `mermaid` 11.17.2 lists the first four among its dependencies (read on 2026-11-18); `debug` is not in that list | Not needed: the Go site has no bundler and no npm resolution, and Mermaid's vendored browser file already contains its dependencies | not-applicable |

### Language-server test fixture

| Dependency (version) | Upstream use | Go answer | Row |
|---|---|---|---|
| `typescript-language-server` (5.3.0) | The real server driven by the keyless Language Server Protocol (LSP) test (`packages/lsp/lsp-stdio/tests/typescript-server.e2e.ts:2`) | `gopls` as the real server in the same kind of scenario: `DH-LIB-LSPFIXTURE` | replaced |

### TypeScript declaration packages

A `@types/<name>` package holds type declarations for a JavaScript library that ships none. Go packages carry their own types, so all 23 rows are **not-applicable**; the library item is `DH-LIB-TYPEDECLS`. The library each one describes is dispositioned by its own owner.

| Dependency (version) | Describes | Declared by |
|---|---|---|
| `@types/babel__code-frame` (7.27.0) | `@babel/code-frame` | `packages/boot/hmr`, `vendor/hmr` |
| `@types/big.js` (6.2.2) | `big.js` | `packages/client/ui-settings-account` |
| `@types/compression` (1.8.1) | `compression` | `packages/host/webserver` |
| `@types/d3-time` (3.0.4) | `d3-time` | `packages/experimental/timeline` |
| `@types/estree` (1.0.9) | The JavaScript syntax-tree node types | `packages/experimental/webworker-runtime` |
| `@types/hosted-git-info` (3.0.5) | `hosted-git-info` | `apps/plugin-market-generator`, `packages/boot/plugin-manager`, `packages/boot/plugin-market` |
| `@types/js-yaml` (4.0.9) | `js-yaml` | root, `apps/cli`, `apps/desktop`, `packages/test-support/session-snapshot` |
| `@types/jsdom` (28.0.3) | `jsdom` | root |
| `@types/mdast` (4.0.4) | The Markdown syntax-tree node types | root, `packages/client/ui-primitives` |
| `@types/mime-types` (3.0.1) | `mime-types` | `packages/api/session-controller` |
| `@types/negotiator` (0.6.5) | `negotiator` | `packages/host/webserver` |
| `@types/node` (22.20.1, 26.4.1) | The Node.js standard library | root, `apps/desktop`, `apps/web`, `native/system` |
| `@types/papaparse` (5.5.2) | `papaparse` | root, `packages/client/ui-sidebar-documentpreview` |
| `@types/picomatch` (3.0.2, 4.0.3) | `picomatch` | six packages in `boot`, `client`, `experimental`, `vendor` |
| `@types/react` (18.3.31) | `react` | root |
| `@types/react-dom` (18.3.7) | `react-dom` | root |
| `@types/readable-stream` (4.0.24) | `readable-stream` | `packages/experimental/webworker-runtime` |
| `@types/semver` (7.8.0) | `semver` | root and seven packages |
| `@types/serve-static` (2.2.0) | `serve-static` | `packages/experimental/inspector` |
| `@types/spdx-expression-parse` (4.0.0) | `spdx-expression-parse` | root |
| `@types/turndown` (5.0.6) | `turndown` | `packages/web/tool-web` |
| `@types/use-sync-external-store` (1.7.0) | `use-sync-external-store` | `packages/client/ui-renderer` |
| `@types/ws` (8.18.1) | `ws` | seven packages in `apps`, `api`, `experimental` |

## 3. Tech debt upstream carries

Search performed: every entry of `pnpm-workspace.yaml` (`overrides`, `allowBuilds`, `minimumReleaseAgeExclude`, `patchedDependencies`) was matched against the 58 dependencies, and the baseline files under `scripts/` were read. None of the 58 is patched (`pnpm-workspace.yaml:112-124` lists 12 patches, all on runtime or packaging dependencies owned by other agents).

| Debt ID | What it is | Evidence | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-pin.allowBuilds.lefthook` | pnpm refuses to run a dependency's install script unless it is allow-listed; lefthook's is allowed because it installs the hook binary | `pnpm-workspace.yaml:26-33` | **Not applicable.** Downloading a Go module never runs code. The hook script of `DH-LIB-GITHOOKS` is a file in the repository, enabled by an explicit `git config` |
| `UP-DEBT-pin.allowBuilds.msgpackr-extract` | The same allow-list, here a denial: the optional native addon's install script is switched off because only the portable codec is needed | `pnpm-workspace.yaml:54-55` | **Not applicable.** The mechanism is npm lifecycle scripts; Go has no equivalent |
| `UP-DEBT-pin.minimumReleaseAgeExclude.pnpm@11.28.5` | pnpm blocks packages younger than a release-age window; the package manager's own pinned version must be exempted from the rule that guards everything else | `pnpm-workspace.yaml:105-107` | **Not applicable.** The Go toolchain version is selected by the `go` and `toolchain` lines of `go.mod` and verified through the checksum database, with no exemption list. The useful idea, a cooling-off period for new versions, is kept as a dependency-update setting (upstream uses 30 days, `.github/dependabot.yml:13-14`) |
| `UP-DEBT-pin.overrides.extract-zip_yauzl` | A transitive dependency forced to one version with no stated reason | `pnpm-workspace.yaml:18` | **Resolved** by [DH-ADR-TOOLCHAIN-11](../upstream/toolchain.md#dh-adr-toolchain-11): every pin carries a reason, checked by a test |
| The four `UP-DEBT-baseline.*` units and `UP-DEBT-marker.scripts/gen-doc-graphs.ts:1256` | Recorded exceptions to repository rules, and a generator limitation | See [../upstream/toolchain.md](../upstream/toolchain.md) section 10 | Resolved there |

Debt that is visible in the dependency list itself, not as inventory units: three major versions of `vite` and two of `@types/node` and `@types/picomatch` are installed side by side. Inference: each consumer (site generator, test runner, application build) pins the bundler major it was written for. In Go, minimal version selection gives one version of each module per build, so this cannot occur.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-TESTRUNNER-R1` | One command runs every local test of a module, in parallel across packages, with packages isolated in separate processes | `package.json:61`, `docs/testing.md:21` |
| `DH-LIB-TESTRUNNER-R2` | A boundary (model, network, clock) can be faked without a mocking library, and everything behind the fake stays real | `docs/testing.md:29` |
| `DH-LIB-COVERAGE-R1` | Statement coverage is measured across in-process tests and child processes and merged into one report | `vitest.config.ts:95-99` (what upstream cannot do) |
| `DH-LIB-PROPTEST-R1` | Parsers at trust boundaries are exercised with generated inputs; every failing input found is kept as a permanent case | The five property suites in section 2 |
| `DH-LIB-GOLDEN-R1` | A failing expected-output comparison shows a readable line difference, and one switch rewrites the expected file | `package.json:70`, `docs/testing.md:12` |
| `DH-LIB-DOMTEST-R1` | A test can assert on the elements, attributes and accessible names of rendered HTML without starting a browser | `docs/testing.md:15`, section 2 |
| `DH-LIB-BUNDLER-R1` | The build yields one self-contained executable per platform with its static assets inside | `docs/development.md:79-90` |
| `DH-LIB-LINTER-R1` | Formatting is automatic, and common bug patterns fail CI | `package.json:56-59` |
| `DH-LIB-ANALYSIS-R1` | A repository rule can be written as a checker with a self-test, and an exception is written at the site with a reason | `AGENTS.md:180` |
| `DH-LIB-VULNCHECK-R1` | Known vulnerabilities in code the product actually calls fail CI | `.github/dependabot.yml:1-14` (upstream relies on update pull requests) |
| `DH-LIB-PKGMANAGER-R1` | Dependency resolution is reproducible and content-verified, and no dependency code runs at install time | `pnpm-workspace.yaml:26-33`, `docs/development.md:27` |
| `DH-LIB-NOTICES-R1` | A third-party licence notice is generated from the dependency graph and a check fails when it is stale | `scripts/gen-third-party-notices.ts:1-8` |
| `DH-LIB-GITHOOKS-R1` | A contributor can opt into a fast pre-commit check; CI stays the authority | `docs/development.md:152` |
| `DH-LIB-DOCSSITE-R1` | A static website is generated from the Markdown under `docs/`, which stays the single source: no page is copied or edited into a site tree, and every relative link and anchor between documents works on the site as it does in the repository | `website/AGENTS.md:7-9`; owner decision OD-4 |
| `DH-LIB-DOCSSITE-R2` | Building, testing and releasing `dsh` does not need the site, its generator or any of its files. The generator is not in the product's module graph, and the site adds no Node, npm package or JavaScript bundler to the repository | [DH-ADR-TOOLCHAIN-11](../upstream/toolchain.md#dh-adr-toolchain-11); [DH-ADR-3](../design.md#dh-adr-3) |
| `DH-LIB-DOCSSITE-R3` | Mermaid diagrams in the documents are drawn on the site. A file the site serves that the project did not write is pinned by version, source, licence and SHA-256 | `website/.vitepress/config.ts:8`; `go-ui-stack.md`, `DH-LIB-GOUISTACK-R5` |
| `DH-LIB-LSPFIXTURE-R1` | The language-server scenario runs against a real, pinned server | `packages/lsp/lsp-stdio/tests/typescript-server.e2e.ts:2` |
| `DH-LIB-RELEASER-R1` | A version tag yields archives and a checksum file for six platform targets | [../upstream/toolchain.md](../upstream/toolchain.md) section 8 |

## 5. Go choice

**Decision.** Use the standard toolchain wherever it does the job, add seven verified modules as tools or test helpers, write one small hook script, and drop the declaration packages. One of the seven is the documentation-site generator, which the owner's decision to keep the website brings in; it is the only one that never touches the product.

**Why.** Every requirement in section 4 except eight is met by a `go` subcommand or a standard-library package. Five of those (bug-pattern linting, vulnerability scanning, HTML parsing, readable differences, licence classification) are solved problems with small, maintained modules. The three documentation-site requirements are met by one generator.

Every module below was checked on 2026-11-18 with `go list -m -json <module>@latest` (path, version, release time) and `go mod download -json <module>@<version>` followed by reading the licence file in the module cache.

| Library ID | Choice | What is used | Verified facts |
|---|---|---|---|
| `DH-LIB-TESTRUNNER` | toolchain | `go test`, package `testing` (including `t.TempDir`, `t.Cleanup`, `t.Context`), `testing/synctest` | `go doc testing/synctest` and `go help testflag` on Go 1.27.4 |
| `DH-LIB-COVERAGE` | toolchain | `go test -coverprofile -coverpkg`, `go build -cover` with `GOCOVERDIR`, `go tool cover`, `go tool covdata` | `go help build` lists `-cover`; `go help testflag` lists `-coverpkg` |
| `DH-LIB-PROPTEST` | stdlib | `testing.F` fuzz targets, `testing/quick` | `go help testflag` lists `-fuzz` |
| `DH-LIB-GOLDEN` | module | `github.com/google/go-cmp` v0.7.0, test-only, for line differences | BSD-3-Clause; released 2025-01-14 |
| `DH-LIB-DOMTEST` | module | `golang.org/x/net` v0.63.0, package `html`, with `net/http/httptest` from the standard library | BSD-3-Clause; released 2026-11-17 |
| `DH-LIB-BUNDLER` | toolchain | `go build`, package `embed` | Already used by this repository's release ([.goreleaser.yaml](../../../.goreleaser.yaml)) |
| `DH-LIB-SCRIPTRUNNER` | toolchain | `go run`, `go generate` | Already used for `tools/harness-trace` |
| `DH-LIB-LINTER` | module | `honnef.co/go/tools` v0.8.2 (`cmd/staticcheck`), with `gofmt` and `go vet` from the toolchain | MIT, with third-party notices in `LICENSE-THIRD-PARTY`; released 2026-10-12; the `//lint:ignore` directive is implemented in `lintcmd/directives.go` |
| `DH-LIB-ANALYSIS` | module | `golang.org/x/tools` v0.53.0: `go/analysis` (with `multichecker` and `analysistest`) for own checkers, `txtar` for multi-file fixtures | BSD-3-Clause; released 2026-11-17; both package directories present in the module |
| `DH-LIB-VULNCHECK` | module | `golang.org/x/vuln` v1.10.0 (`cmd/govulncheck`) | BSD-3-Clause; released 2026-11-17 |
| `DH-LIB-TYPEDECLS` | toolchain | The Go compiler; packages carry their own types | Not a module |
| `DH-LIB-PKGMANAGER` | toolchain | Go modules: `go.mod`, `go.sum`, `go.work`, `go mod tidy -diff`, `go mod verify`, `tool` directives | `go help mod tidy` lists `-diff`; `go help mod edit` lists `-tool` |
| `DH-LIB-NOTICES` | module | `github.com/google/go-licenses/v2` v2.0.1, run as a tool | Apache-2.0; released 2025-09-08 |
| `DH-LIB-GITHOOKS` | own | A checked-in `pre-commit` script (format and vet of staged Go files), enabled with `git config core.hooksPath` | Not a module |
| `DH-LIB-DOCSSITE` | module | `github.com/gohugoio/hugo` v0.170.1, run as a docs-only tool from a module of its own, never linked into `dsh`; with one vendored browser file, `mermaid` 11.17.2 `dist/mermaid.min.js` | Apache-2.0 (`LICENSE`); released 2026-11-17; `go 1.27.0` in its `go.mod`; a text search of the module for `import "C"` found none. Mermaid: MIT in the npm manifest of 11.17.2, and `dist/mermaid.min.js` (3,572,661 bytes) in the package's file listing on unpkg, both read on 2026-11-18; file bytes not vendored yet, so no checksum is recorded. Details below |
| `DH-LIB-LSPFIXTURE` | module | `golang.org/x/tools/gopls` v0.24.0, installed in the scenario environment, not linked | BSD-3-Clause; released 2026-10-14 |
| `DH-LIB-RELEASER` | module | `github.com/goreleaser/goreleaser/v2` v2.20.2, run through its GitHub Action as today | MIT; released 2026-11-12 |

Alternatives considered:

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| `github.com/golangci/golangci-lint/v2` v2.15.0 as the single lint front end | Bundles many linters, including clone detection | A large configuration surface and a second suppression syntax; most bundled linters are not needed. Rejected in favour of three direct tools | GPL-3.0; released 2026-10-16 |
| `github.com/kisielk/errcheck` v1.30.0 for discarded errors | Mature | No inline reasoned exception; an own `go/analysis` checker gives the form [DH-ADR-TOOLCHAIN-8](../upstream/toolchain.md#dh-adr-toolchain-8) requires | MIT; released 2026-10-10 |
| `pgregory.net/rapid` v1.3.0 for property tests | Structured generators and shrinking | A second input-generation model beside native fuzzing; adopt only if a state-machine property needs it | MPL-2.0; released 2026-03-30 |
| `github.com/evilmartians/lefthook/v2` v2.2.2 (the same hook manager; it is a Go program) | Familiar to upstream contributors | A manager for one hook is more than the job needs | MIT; released 2026-10-13 |

**The documentation site: a Go generator compared with keeping a non-Go one.** The owner keeps the website. The constraint is that it must not bring a JavaScript bundler back into the product build; a docs-only tool is acceptable when it is isolated from the product.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Hugo, a static-site generator written in Go** (chosen) | One Go program, pinned by `go.sum` and the checksum database like every other tool; no Node, package manager or lock file. Its Markdown parser is goldmark, the parser the product itself uses (`DH-LIB-MICROMARK` in [micromark.md](micromark.md)), so a document renders on the site by the same rules as in the product. Directory mounts let `docs/` be the content without moving a file | A large dependency graph and a `go 1.27.0` floor, so it must live in a module of its own. It is version 0.x with frequent releases. It has no Mermaid support and no search of its own. Its module includes a JavaScript bundler as a library, which the site must not call | `github.com/gohugoio/hugo` v0.170.1, Apache-2.0, released 2026-11-17 (`go list -m -json @latest`, `go mod download -json`, `LICENSE`). Read in the module cache on 2026-11-18: `go.mod` has 187 lines and requires `github.com/yuin/goldmark` v1.8.7, `github.com/alecthomas/chroma/v2` v2.27.0 and `github.com/evanw/esbuild` v0.28.2 (the bundler behind its `js.Build` template function, `tpl/js/js.go:55`); raw HTML in Markdown is off by default (`Unsafe: false`, `markup/goldmark/goldmark_config/config.go:86`); an embedded link render hook rewrites a relative link to the target page's address (`tpl/tplimpl/embedded/templates/_markup/render-link.html`), switched by a setting with the values `always`, `auto`, `fallback`, `never` (`config.go:21-24`); module mounts exist (`modules/config.go:269`); the only embedded diagram hook is for GoAT (`render-codeblock-goat.html`). Not compiled or run |
| An own generator on goldmark and `html/template` | No new dependency at all: goldmark is already chosen for the product. Full control of links and anchors. A few hundred lines | The project writes and maintains navigation, layout, a development preview and anything else a generator already has. A second small product | `github.com/yuin/goldmark` v1.8.7, MIT, released 2026-10-30 (`go list -m -json @latest` on 2026-11-18; licence as recorded by [micromark.md](micromark.md)). The fallback if a trial build shows Hugo cannot map the layout of `docs/` |
| Keep VitePress (not Go) | Upstream's configuration, theme extensions, local search and Mermaid plugin exist and are tested | Node, pnpm, a lock file and Vite, which is a JavaScript bundler, return to the repository, with the install-script and release-age policy this design removes. Contributors need Node to preview a document. Acceptable only if confined to its own directory and workflow | Upstream declares `vitepress` ^1.6.4, `vite` ^5.4.21 and `vue` 3.5.42 (`website/package.json:19-22`); upstream already installs three majors of Vite at once (section 3). Not evaluated further |
| No site: read the Markdown on the code-hosting site | Nothing to build | The earlier form of DH-ADR-TOOLCHAIN-15; overruled by the owner | n/a |

Inference: Hugo is chosen over the own generator because a documentation site is a solved problem and the tool is isolated, so its size costs the product nothing; it is chosen over VitePress because it adds no second ecosystem. If the trial build fails on the link or anchor rules of `DH-LIB-DOCSSITE-R1`, the own generator is the next choice and needs no new verification.

**Tradeoff.** No clone detection and no Mermaid syntax gate. The documentation site adds one large pinned tool to keep current. HTML assertions without a browser cannot see layout or script behaviour; those need real-browser scenarios, which other specifications own.

## 6. Design notes

**Where tools are pinned.** staticcheck, govulncheck and go-licenses are recorded as `tool` directives in a separate tools module, so the product's `go.mod` lists only what `dsh` links. `go-cmp`, `golang.org/x/net/html` and `golang.org/x/tools/txtar` are imported by test files and therefore appear in `src/harness/go.mod`; Go does not link test-only imports into the product binary. The own analyzer binary lives in the tools module and imports `go/analysis`.

**The documentation site.** Three things keep it apart from the product.

- *Its own module.* Hugo is recorded as a `tool` directive in a module used for nothing else (working name `tools/docs-site`). It is not the tools module above, because Hugo's `go 1.27.0` floor and its dependency graph would otherwise apply to the linters, and it is not joined into the workspace the product builds in. Inference: with that, `go mod tidy`, `go build` and `go test` of `src/harness` never resolve or download it.
- *Its own sources.* The site configuration, the layout templates (including the Mermaid code-block template and a link template if one is needed), the stylesheet and the vendored `mermaid.min.js` with its manifest record and licence text live in one directory. The content is `docs/`, mounted, not copied. The configuration sets what the documents need: raw HTML in Markdown allowed, because the design documents carry `<a id="...">` anchors and are the project's own reviewed text; the link render hook set explicitly rather than left to its default; one language. The templates call none of the generator's asset-pipeline functions, so its built-in bundler is never run.
- *Its own workflow.* A docs workflow builds the site for a pull request that changes `docs/` or the site sources, and publishes it from the main branch, as upstream's `docs-pages.yml` does. It is not a gate of `tools/harness-gate` and not a step of the release.

Ownership and cleanup: the build owns its output directory, which it creates fresh and which is ignored by git; the published site is owned by the hosting service's deployment and is replaced as a whole by the next one. A failed build publishes nothing and leaves the previous deployment in place.

**Shared test helpers.** One internal package, `internal/testkit` (area A23 owns it, [../areas/util-test-support.md](../areas/util-test-support.md), `DH-D-UTIL-6`), holds the golden-file helper, the HTML query helpers, the scripted model, and the builder that compiles the `dsh` binary once per test run. It mirrors `src/go/internal/testkit` in the cordis module.

**Ownership and cleanup.** Tools create nothing at run time. A test helper that creates a resource is its owner and releases it: temporary directories through `t.TempDir`, child processes and listeners through `t.Cleanup` bound to `t.Context`, so a failed or timed-out test still releases them. A fake is a plain value owned by the test. Access is separate: the product under test uses the temporary home and workspace but never deletes them.

**Failure and retry.** The golden helper writes an updated file to a temporary name and renames it, so an interrupted update leaves the old file. A coverage directory is created fresh for each run and removed by the test that created it; a killed child may write no counter file, which lowers the measured coverage of that run but cannot affect another run. No tool is retried automatically: a flaky check is a defect ([DH-ADR-TOOLCHAIN-9](../upstream/toolchain.md#dh-adr-toolchain-9)). govulncheck needs network access to the vulnerability database; when the database is unreachable the gate fails and is re-run, it does not pass.

## 7. Risks and open questions

1. **Race detector and cgo.** `go test -race` needs a C compiler. CI has one on Linux; Windows and macOS coverage of races depends on runner images.
2. **Go version.** `testing/synctest` needs a `go` line of 1.25 or later; the cordis module declares 1.24. The harness module's floor is `go 1.26.0`, because ten of the modules it requires declare that line themselves; the evidence is recorded with CF-4 in [../design.md](../design.md) §8. The tools in this document's tools module and the documentation-site module have floors of their own (Hugo, gopls and GoReleaser declare 1.27) and do not raise the product's.
3. **Tools-module invocation.** Whether tools are run with `go tool -modfile=<tools go.mod>` or from the tools module's directory is an implementation detail to settle when the module is created.
4. **`go-licenses` coverage.** It classifies Go module licences. If the Python SDK or JavaScript islands bring non-Go dependencies, their notices need another source; agents L16 and L18 own those dependencies.
5. **Real-browser test driver.** `DH-LIB-DOMTEST` deliberately stops at parsed HTML. The driver for browser scenarios is chosen by the UI stack and browser-automation specifications (agents L16 and L13).
6. **Clone detection.** If duplicated code becomes a review burden, a detector can be added later as one more tool without changing any decision here.
7. **Documentation site: what only a build can settle.** Whether Hugo's embedded link hook resolves the `name.md#anchor` links the documents use, or a small own link template is needed; whether headings get the same anchors the code-hosting site gives them; how links that leave `docs/` (to source files) are rewritten. None of this was run.
8. **Documentation site: hosting and extras.** Where the site is published is not decided; upstream uses a pages deployment and a preview service. Search, the raw-Markdown twin of each page with `llms.txt`, and the viewer and table extensions of upstream's theme are not in the first version ([DH-ADR-TOOLCHAIN-15](../upstream/toolchain.md#dh-adr-toolchain-15), item 3).
9. **Mermaid without a gate.** Upstream parses every diagram in CI with the `mermaid` package under Node. The Go design has no Node, so a diagram that does not parse is found by a reader. A real-browser check that loads each page and looks for Mermaid's error marker could be added with the browser driver other specifications choose.
10. **Generator upgrades.** Hugo releases often and is at version 0.x; an upgrade is a reviewed change that rebuilds the site and compares the page list, not an automatic bump.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-TOOLCHAIN-2` | local | `DH-LIB-TESTRUNNER-R1` | A seeded data race fails the unit command; a virtual-time test finishes without waiting |
| `DH-T-TOOLCHAIN-3` | local | `DH-LIB-PROPTEST-R1` | A committed crasher input is run by plain `go test` |
| `DH-T-TOOLCHAIN-4` | local | `DH-LIB-GOLDEN-R1`, `DH-LIB-DOMTEST-R1` | The golden helper shows a difference, updates only on request and refuses under CI; an HTML query helper finds an element by role and name in a rendered fixture |
| `DH-T-TOOLCHAIN-7` | local | `DH-LIB-COVERAGE-R1` | Lines run only in a child process appear in the merged profile |
| `DH-T-TOOLCHAIN-8` | local | `DH-LIB-LINTER-R1`, `DH-LIB-ANALYSIS-R1` | Each own checker rejects a seeded violation and accepts a reasoned exception |
| `DH-T-TOOLCHAIN-11` | local | `DH-LIB-PKGMANAGER-R1`, `DH-LIB-NOTICES-R1`, `DH-LIB-VULNCHECK-R1` | `go mod tidy -diff` is empty, every pin has a reason, the regenerated notice equals the committed one, and the vulnerability check ran |
| `DH-T-TOOLCHAIN-13` | local | `DH-LIB-DOCSSITE-R1`, `DH-LIB-DOCSSITE-R2`, `DH-LIB-DOCSSITE-R3` | The site builds from `docs/` with Go and no Node; every document has a page and every internal link resolves; the product's module graph holds neither the generator nor a bundler; the vendored Mermaid file equals its manifest record and each page with a diagram references it |
| `DH-E-TOOLCHAIN-2` | real environment | `DH-LIB-BUNDLER-R1`, `DH-LIB-RELEASER-R1` | Release rehearsal on three operating systems |

The tests and scenarios are defined in [../upstream/toolchain.md](../upstream/toolchain.md) section 12. `DH-LIB-LSPFIXTURE-R1` is proven by the language-server area's own scenario; `DH-LIB-GITHOOKS-R1` by review, since the hook is optional. That a browser actually draws the diagrams on the published site is seen by a reader; no scenario is declared for it (open question 9). Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

- **Replaced (23):** `UP-DEP-vitest`, `UP-DEP-@vitest/spy`, `UP-DEP-@vitest/coverage-v8`, `UP-DEP-istanbul-lib-report`, `UP-DEP-fast-check`, `UP-DEP-jsdom`, `UP-DEP-@testing-library/dom`, `UP-DEP-@testing-library/react`, `UP-DEP-tsdown`, `UP-DEP-vite`, `UP-DEP-tsx`, `UP-DEP-oxlint`, `UP-DEP-oxlint-tsgolint`, `UP-DEP-@stylistic/eslint-plugin`, `UP-DEP-eslint-plugin-sonarjs`, `UP-DEP-pnpm`, `UP-DEP-smol-toml`, `UP-DEP-spdx-expression-parse`, `UP-DEP-lefthook`, `UP-DEP-typescript-language-server`, `UP-DEP-vitepress`, `UP-DEP-vitepress-plugin-mermaid`, `UP-DEP-vue`.
- **Ported (1):** `UP-DEP-mermaid` (kept as a vendored browser file of the documentation site only).
- **Dropped (2):** `UP-DEP-jscpd`, `UP-DEP-@panzoom/panzoom`.
- **Not applicable (32):** `UP-DEP-vite-tsconfig-paths`, `UP-DEP-lightningcss`, `UP-DEP-@yarnpkg/cli-dist`, `UP-DEP-publint`, `UP-DEP-@braintree/sanitize-url`, `UP-DEP-cytoscape`, `UP-DEP-cytoscape-cose-bilkent`, `UP-DEP-dayjs`, `UP-DEP-debug`, and the 23 `UP-DEP-@types/*` units listed in section 2.
- **Tech debt:** `UP-DEBT-pin.allowBuilds.lefthook`, `UP-DEBT-pin.allowBuilds.msgpackr-extract`, `UP-DEBT-pin.minimumReleaseAgeExclude.pnpm@11.28.5` (not applicable); `UP-DEBT-pin.overrides.extract-zip_yauzl` (resolved; row points at [../upstream/toolchain.md](../upstream/toolchain.md)).
