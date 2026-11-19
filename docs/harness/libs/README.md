# Library specifications: index

> **Status: design, not built.** This index lists the `DH-LIB` rows of the
> ledger fragments under [../trace/fragments](../trace/fragments) as they stood on
> 2026-11-19; it must be brought up to date whenever a `DH-LIB` row changes. It
> lists every library item the Go design declares: what it is for (the row's own title,
> which names the upstream library it replaces where there is one), the Go choice, the
> module and version where a module is named, its licence, and the specification that
> explains it. Where this index and a specification differ, the specification governs.

**How to read the "Go choice" column.** *Standard library*: nothing is added. *Own code*:
harness code, specified in the linked document. *Named module*: a third-party Go module,
verified to exist with the version and licence shown. *Go toolchain*: a command or
feature of the Go distribution. *Dropped*: deliberately not built, by the decision the
specification records. A choice marked **provisional** is accepted only after the
measurement or test named beside it; one marked **recommended default** was applied on
the owner's instruction without the owner confirming it, and is listed in
[../design.md](../design.md) §9.2 so that it can be overturned.

## Framework: cordis, loader and companions

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-LOADER` | cordis loader, include and group | own code |  |  | [libs/cordis-loader.md](cordis-loader.md) |  |
| `DH-LIB-LOADEREXPR` | Config expression engine that replaces !!js | named module | `github.com/expr-lang/expr` v1.17.8 | MIT | [libs/cordis-loader.md](cordis-loader.md) |  |
| `DH-LIB-CORDISPLUGINS` | cordis hmr, logger-console and timer | own code |  |  | [libs/cordis-plugins.md](cordis-plugins.md) |  |
| `DH-LIB-CORDIS` | cordis framework core: the existing Go port in src/go/cordis, extended | own code |  |  | [libs/cordis.md](cordis.md) |  |
| `DH-LIB-COSMOKIT` | cosmokit utilities: the Go standard library; the volatile reference type is not built | standard library |  |  | [libs/cosmokit.md](cosmokit.md) | **recommended default** (`DH-ADR-COSMOKIT-2`) |

## Schemas and validation

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-AJV` | JSON Schema validation of values, defaults and external documents (replaces ajv) | named module | `github.com/google/jsonschema-go` v0.4.3 | MIT | [libs/ajv.md](ajv.md) |  |
| `DH-LIB-GENMAPPING` | Declaration source maps (not needed: no generated declarations) | dropped |  |  | [libs/jridgewell-gen-mapping.md](jridgewell-gen-mapping.md) |  |
| `DH-LIB-SCHEMASTERY` | Plugin config description: defaults, constraints, live fields, UI metadata (replaces schemastery) | own code |  |  | [libs/schemastery.md](schemastery.md) |  |
| `DH-LIB-STANDARDSCHEMA` | Schema-library-neutral config check: the cordis.Schema interface (replaces @standard-schema/spec) | own code |  |  | [libs/standard-schema-spec.md](standard-schema-spec.md) |  |
| `DH-LIB-TYPESCRIPT` | Go toolchain, reflect and go/ast in place of the TypeScript compiler and compiler API | Go toolchain |  |  | [libs/typescript.md](typescript.md) |  |
| `DH-LIB-ZOD` | Schemas, validation and strict decoding from Go types (replaces zod) | named module | `github.com/google/jsonschema-go` v0.4.3 | MIT | [libs/zod.md](zod.md) |  |

## Build, test and release toolchain

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-ANALYSIS` | Own repository-rule checkers (go/analysis) and txtar fixtures | named module | `golang.org/x/tools` v0.53.0 | BSD-3-Clause | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-BUNDLER` | Build and asset embedding: go build and the embed package | Go toolchain |  |  | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-COVERAGE` | Coverage: go test -cover, go build -cover, go tool cover and covdata | Go toolchain |  |  | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-DOCSSITE` | Documentation website generator: Hugo run as a docs-only tool from a module of its own, never linked into dsh, with Mermaid 11.17.2 as one vendored browser file of the site (replaces vitepress and its plugins) | named module | `github.com/gohugoio/hugo` v0.170.1 | Apache-2.0 | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-DOMTEST` | HTML assertions without a browser: net/http/httptest plus an HTML parser | named module | `golang.org/x/net` v0.63.0 | BSD-3-Clause | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-GITHOOKS` | Optional pre-commit hook: one checked-in script, no hook manager | own code |  |  | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-GOLDEN` | Readable differences for golden-file tests | named module | `github.com/google/go-cmp` v0.7.0 | BSD-3-Clause | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-LINTER` | Bug-pattern linting: staticcheck, with gofmt and go vet from the toolchain | named module | `honnef.co/go/tools` v0.8.2 | MIT | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-LSPFIXTURE` | Real language server for the language-server scenario: gopls | named module | `golang.org/x/tools/gopls` v0.24.0 | BSD-3-Clause | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-NOTICES` | Third-party licence notices from the module graph: go-licenses | named module | `github.com/google/go-licenses/v2` v2.0.1 | Apache-2.0 | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-PKGMANAGER` | Dependency management: Go modules, go.sum, go.work and tool directives | Go toolchain |  |  | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-PROPTEST` | Generated-input tests: native fuzzing and testing/quick | standard library |  |  | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-RELEASER` | Release archives and checksums: GoReleaser | named module | `github.com/goreleaser/goreleaser/v2` v2.20.2 | MIT | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-SCRIPTRUNNER` | Running repository tools from source: go run and go generate | Go toolchain |  |  | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-TESTRUNNER` | Test runner and fakes: go test and the testing package | Go toolchain |  |  | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-TYPEDECLS` | TypeScript declaration packages: not needed, Go packages carry their types | Go toolchain |  |  | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |
| `DH-LIB-VULNCHECK` | Vulnerability scanning of reachable code: govulncheck | named module | `golang.org/x/vuln` v1.10.0 | BSD-3-Clause | [libs/build-test-toolchain.md](build-test-toolchain.md) |  |

## Configuration files, command line, globs and versions

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-COMMANDER` | Command tree, help and shell completion for cmd/dsh (replaces commander) | named module | `github.com/spf13/cobra` v1.10.2 | Apache-2.0 | [libs/commander.md](commander.md) |  |
| `DH-LIB-PFLAG` | Option parsing for the launcher and for app command lines | named module | `github.com/spf13/pflag` v1.1.0 | BSD-3-Clause | [libs/commander.md](commander.md) |  |
| `DH-LIB-HOSTEDGITINFO` | Repository address parsing with net/url (replaces hosted-git-info) | standard library |  |  | [libs/hosted-git-info.md](hosted-git-info.md) |  |
| `DH-LIB-JSYAML` | No second YAML parser: hand-written YAML through DH-LIB-YAML, machine-written files as JSON (replaces js-yaml) | named module | `go.yaml.in/yaml/v4` v4.0.0 | Apache-2.0, with MIT for the files ported from libyaml | [libs/js-yaml.md](js-yaml.md) |  |
| `DH-LIB-PICOMATCH` | Glob matching with one dialect (replaces picomatch) | named module | `github.com/bmatcuk/doublestar/v4` v4.10.4 | MIT | [libs/picomatch.md](picomatch.md) |  |
| `DH-LIB-RESOLVEEXPORTS` | Plugin manifest paths confined to the plugin directory with os.Root (replaces resolve.exports) | standard library |  |  | [libs/resolve.exports.md](resolve.exports.md) |  |
| `DH-LIB-SEMVER` | Strict versions, ordering and ranges (replaces semver) | named module | `github.com/Masterminds/semver/v3` v3.5.0 | MIT | [libs/semver.md](semver.md) |  |
| `DH-LIB-YAML` | YAML parsing, strict dialect and comment-preserving write-back (replaces yaml) | named module | `go.yaml.in/yaml/v4` v4.0.0 | Apache-2.0, with MIT for the files ported from libyaml | [libs/yaml.md](yaml.md) |  |
| `DH-LIB-YARNPKGPARSERS` | Shell grammar for the browser Preview's emulated shell (not carried; mvdan.cc/sh/v3 v3.14.1 recorded if OD-11 keeps the Preview) | dropped |  |  | [libs/yarnpkgparsers.md](yarnpkgparsers.md) |  |

## Model providers

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-ANTHROPICSDK` | Anthropic API client: not needed (npm peer dependency of the Agent SDK) | dropped |  |  | [libs/anthropic-aisdk.md](anthropic-aisdk.md) |  |
| `DH-LIB-PIAI` | Model-provider wire drivers and model catalogue (replaces the pi-ai family) | own code |  |  | [libs/earendil-works-pi.md](earendil-works-pi.md) |  |
| `DH-LIB-EVENTSOURCEPARSER` | Server-sent-events reader for provider response streams (replaces eventsource-parser) | own code |  |  | [libs/eventsource-parser.md](eventsource-parser.md) |  |

## Model Context Protocol and browser MCP servers

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-CHROMEDEVTOOLSFRONTEND` | Chrome DevTools interface source used by the experimental inspector: not built and not shipped | dropped |  |  | [libs/chrome-devtools-frontend.md](chrome-devtools-frontend.md) |  |
| `DH-LIB-CHROMEDEVTOOLSMCP` | Chrome DevTools MCP browser server: a launch recipe for an operator-installed external program (replaces the chrome-devtools-mcp npm dependency) | own code |  |  | [libs/chrome-devtools-mcp.md](chrome-devtools-mcp.md) |  |
| `DH-LIB-MCP` | Model Context Protocol client, server and test fixtures (replaces the @modelcontextprotocol packages) | named module | `github.com/modelcontextprotocol/go-sdk` v1.8.0 | Apache-2.0 for new code; MIT for earlier contributions not yet relicensed | [libs/modelcontextprotocol.md](modelcontextprotocol.md) |  |
| `DH-LIB-PLAYWRIGHTMCP` | Playwright MCP browser server: a launch recipe for an operator-installed external program (replaces the @playwright/mcp npm dependency) | own code |  |  | [libs/playwrightmcp.md](playwrightmcp.md) |  |

## Agent protocols and external agent runtimes

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-ACPSDK` | Agent Client Protocol framing and message types, agent role and client role (replaces @agentclientprotocol/sdk) | named module | `github.com/coder/acp-go-sdk` v0.20.0 | Apache-2.0 | [libs/agentclientprotocol-sdk.md](agentclientprotocol-sdk.md) |  |
| `DH-LIB-CLAUDEAGENTSDK` | Claude Code delegation: own driver for the user's installed claude executable (replaces @anthropic-ai/claude-agent-sdk) | own code |  |  | [libs/anthropic-ai-claude-agent-sdk.md](anthropic-ai-claude-agent-sdk.md) |  |
| `DH-LIB-OAR` | OAR runtime bridge: own Go provider that drives the operator-installed OAR (Node 24 or later) as a host-owned child process; nothing of OAR is linked, bundled or patched | own code |  |  | [libs/botiverse-oar.md](botiverse-oar.md) |  |
| `DH-LIB-CODEX` | Codex delegation: own client for the user's installed codex app-server (replaces @openai/codex) | own code |  |  | [libs/openai-codex.md](openai-codex.md) |  |

## Processes, terminals and shell

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-CROSSSPAWN` | Windows command starting: os/exec lookup plus the process package's batch-file rule | standard library |  |  | [libs/cross-spawn.md](cross-spawn.md) |  |
| `DH-LIB-EXECA` | Run-and-capture helper on the harness process package, built on os/exec | standard library |  |  | [libs/execa.md](execa.md) |  |
| `DH-LIB-NODEPROC` | Process starting, line input and interpreter processes: os/exec, os/signal, context and bufio in one harness process package, with golang.org/x/sys for tree owners | standard library |  |  | [libs/node-process-builtins.md](node-process-builtins.md) |  |
| `DH-LIB-NODEPTY` | Pseudo-terminal and ConPTY allocation: own harness package on golang.org/x/sys, no cgo, no helper executable | named module | `golang.org/x/sys` v0.50.0 | BSD-3-Clause | [libs/node-pty.md](node-pty.md) |  |
| `DH-LIB-SHELLQUOTE` | Reading POSIX shell command lines with mvdan.cc/sh/v3 syntax; quoting with an own single-quote function | named module | `mvdan.cc/sh/v3` v3.14.1 | BSD-3-Clause | [libs/shell-quote.md](shell-quote.md) |  |
| `DH-LIB-WHICH` | Program lookup against the child's explicit environment: own function modelled on os/exec | own code |  |  | [libs/which.md](which.md) |  |
| `DH-LIB-XTERM` | Host-side terminal emulator behind a harness Screen interface (provisional choice, gated by DH-T-XTERM-9); serializer and marks are own code | named module | `github.com/danielgatis/go-headless-term` v1.0.9 | MIT | [libs/xterm.md](xterm.md) | **provisional**, gated by `DH-T-XTERM-9` |

## Native code and system calls

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-KOFFI` | Operating-system calls without a foreign-function layer (replaces koffi) | named module | `golang.org/x/sys` v0.50.0 | BSD-3-Clause | [libs/koffi.md](koffi.md) |  |
| `DH-LIB-REQUIREBUILTIN` | Access to runtime-internal module loaders (not needed: a Go binary has no run-time module loader) | dropped |  |  | [libs/node-addon-require-builtin.md](node-addon-require-builtin.md) |  |
| `DH-LIB-GOLANDLOCK` | Landlock ruleset construction | named module | `github.com/landlock-lsm/go-landlock` v0.11.2 | MIT | [libs/node-addon-system.md](node-addon-system.md) |  |
| `DH-LIB-NATIVESYSTEM` | Sandbox launch stage and POSIX process lock as harness code (replaces the native/system launcher and flock addon) | own code |  |  | [libs/node-addon-system.md](node-addon-system.md) |  |

## Network and HTTP

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-COMPRESSION` | Opt-in gzip response compression (replaces compression) | named module | `github.com/klauspost/compress` v1.20.4 | BSD-3-Clause; Apache-2.0 for gzhttp | [libs/compression.md](compression.md) |  |
| `DH-LIB-GOT` | Cancellable telemetry export with bounded retry on net/http (replaces got) | standard library |  |  | [libs/got.md](got.md) |  |
| `DH-LIB-HTTPSERVER` | Static preview server for a client bundle: not needed, the host renders the UI | dropped |  |  | [libs/http-server.md](http-server.md) |  |
| `DH-LIB-IPADDRJS` | Bind-address parsing and classification with net/netip (replaces ipaddr.js) | standard library |  |  | [libs/ipaddr.js.md](ipaddr.js.md) |  |
| `DH-LIB-MIMETYPES` | Content-Type of served workspace files from the one media-type table (replaces mime-types) | own code |  |  | [libs/mime-types.md](mime-types.md) |  |
| `DH-LIB-MIME` | One compiled-in media-type table, both directions (replaces mime) | own code |  |  | [libs/mime.md](mime.md) |  |
| `DH-LIB-NEGOTIATOR` | Accept-Encoding negotiation inside the compression handler (replaces negotiator) | named module | `github.com/klauspost/compress` v1.20.4 | BSD-3-Clause; Apache-2.0 for gzhttp | [libs/negotiator.md](negotiator.md) |  |
| `DH-LIB-NODENET` | Listeners, local streams, TLS, URLs: net/http, crypto/tls, net, net/netip, net/url (replaces the Node network built-ins) | standard library |  |  | [libs/node-net-builtins.md](node-net-builtins.md) |  |
| `DH-LIB-OPEN` | Default-browser hand-off by the operating system's launcher with a scrubbed environment (replaces open) | own code |  |  | [libs/open.md](open.md) |  |
| `DH-LIB-SELFSIGNED` | Test-only self-signed certificate fixture with crypto/x509 (replaces selfsigned) | standard library |  |  | [libs/selfsigned.md](selfsigned.md) |  |
| `DH-LIB-SERVESTATIC` | Static file serving from an embedded file system with net/http (replaces serve-static) | standard library |  |  | [libs/serve-static.md](serve-static.md) |  |
| `DH-LIB-HTTPPROXY` | Proxy bypass matching for the outbound client factory | named module | `golang.org/x/net` v0.63.0 | BSD-3-Clause | [libs/undici.md](undici.md) |  |
| `DH-LIB-UNDICI` | Outbound HTTP client: net/http and crypto/tls behind one client factory (replaces undici and global fetch) | standard library |  |  | [libs/undici.md](undici.md) |  |
| `DH-LIB-WS` | WebSocket for the terminal socket and outbound WebSocket clients (replaces ws) | named module | `github.com/coder/websocket` v1.8.15 | ISC (permission notice in LICENSE.txt) | [libs/ws.md](ws.md) |  |

## Telemetry

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-OTEL` | OTLP logs export: own OTLP/HTTP JSON exporter on the Go standard library (replaces the @opentelemetry/* family) | own code |  |  | [libs/opentelemetry.md](opentelemetry.md) |  |

## Files, search, diff and archives

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-CHOKIDAR` | File watching: one harness watch service over OS notifications with a polling fallback (replaces chokidar) | named module | `github.com/fsnotify/fsnotify` v1.10.1 | BSD-3-Clause | [libs/chokidar.md](chokidar.md) |  |
| `DH-LIB-DIFF` | Line diff with context hunks (replaces jsdiff structuredPatch) | named module | `github.com/aymanbagabas/go-udiff` v0.4.2 | BSD-3-Clause and MIT | [libs/diff.md](diff.md) |  |
| `DH-LIB-FFLATE` | ZIP archives: streamed writing and bounded reading (replaces fflate; extract-zip has no counterpart) | standard library |  |  | [libs/fflate.md](fflate.md) |  |
| `DH-LIB-FUZZYSORT` | Tiered fuzzy filtering of in-memory pick-lists (replaces fuzzysort) | own code |  |  | [libs/fuzzysort.md](fuzzysort.md) |  |
| `DH-LIB-FUZZYSORTNORM` | Unicode normalisation (NFKC, NFKD) for search folding | named module | `golang.org/x/text` v0.44.0 | BSD-3-Clause | [libs/fuzzysort.md](fuzzysort.md) |  |
| `DH-LIB-MINISEARCH` | Text search of the plugin market index (MiniSearch): not built, because there is no plugin market | dropped |  |  | [libs/minisearch.md](minisearch.md) |  |
| `DH-LIB-NODEFS` | Files, paths and host facts on the Go standard library (replaces node:fs, node:path, node:os) | standard library |  |  | [libs/node-fs-path-os-zlib.md](node-fs-path-os-zlib.md) |  |
| `DH-LIB-NODEFSXSYS` | System calls the standard library lacks: write-through rename, no-replace rename, file locks, file identity, drive type, access-control lists, OS version | named module | `golang.org/x/sys` v0.50.0 | BSD-3-Clause | [libs/node-fs-path-os-zlib.md](node-fs-path-os-zlib.md) |  |
| `DH-LIB-NODEZLIB` | gzip, DEFLATE and CRC-32 on the Go standard library (replaces node:zlib except Zstandard) | standard library |  |  | [libs/node-fs-path-os-zlib.md](node-fs-path-os-zlib.md) |  |
| `DH-LIB-NODEZLIBZSTD` | Zstandard for the optional stored-payload codec and for reading .zst input | named module | `github.com/klauspost/compress` v1.20.4 | BSD-3-Clause for the zstd package; MIT for the xxhash copy inside it | [libs/node-fs-path-os-zlib.md](node-fs-path-os-zlib.md) |  |
| `DH-LIB-PLIMIT` | Bounded concurrency for fan-out work (replaces p-limit) | named module | `golang.org/x/sync` v0.25.0 | BSD-3-Clause | [libs/p-limit.md](p-limit.md) |  |
| `DH-LIB-TAR` | Tar and gzip archives: strict streamed reading and reproducible writing (replaces node-tar) | standard library |  |  | [libs/tar.md](tar.md) |  |
| `DH-LIB-RIPGREP` | File content search and file listing in process, in pure Go (replaces the bundled ripgrep executable) | own code |  |  | [libs/vscode-ripgrep.md](vscode-ripgrep.md) |  |
| `DH-LIB-RIPGREPGLOB` | Glob pattern matching for search includes, file listing and ignore rules | named module | `github.com/bmatcuk/doublestar/v4` v4.10.4 | MIT | [libs/vscode-ripgrep.md](vscode-ripgrep.md) |  |

## Time, recurrence and cryptography

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-TEMPORAL` | Zone-aware time: instants, IANA zones, local-time resolution and calendar stepping (replaces @js-temporal/polyfill) | standard library |  |  | [libs/js-temporal-polyfill.md](js-temporal-polyfill.md) |  |
| `DH-LIB-NOBLEHASHES` | Synchronous SHA-1, SHA-256 and SHA-512 from the standard library; no counterpart of the browser hash library is needed | standard library |  |  | [libs/noble-hashes.md](noble-hashes.md) |  |
| `DH-LIB-NODECRYPTO` | Random identifiers and secrets, content digests, cookie signing and constant-time comparison from the standard library (replaces node:crypto) | standard library |  |  | [libs/node-crypto-timers-perf-hooks.md](node-crypto-timers-perf-hooks.md) |  |
| `DH-LIB-NODEPERFHOOKS` | Monotonic elapsed-time measurement and benchmarks from the standard library (replaces node:perf_hooks) | standard library |  |  | [libs/node-crypto-timers-perf-hooks.md](node-crypto-timers-perf-hooks.md) |  |
| `DH-LIB-NODETIMERS` | Owned, cancellable waits and deadlines on time, context and the cordis timer service (replaces node:timers and global timers) | standard library |  |  | [libs/node-crypto-timers-perf-hooks.md](node-crypto-timers-perf-hooks.md) |  |
| `DH-LIB-RRULE` | Bounded RFC 5545 recurrence evaluator in wall time (replaces rrule) | own code |  |  | [libs/rrule.md](rrule.md) |  |

## Programmatic tool calling and JavaScript tooling

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-ACORN` | JavaScript parser and tree walker used to rewrite guest programs: no parser and no engine in the Go harness | dropped |  |  | [libs/acorn.md](acorn.md) |  |
| `DH-LIB-CODEFRAME` | Source excerpt with a caret for positioned errors: one own plain-text function | own code |  |  | [libs/babelcode-frame.md](babelcode-frame.md) |  |
| `DH-LIB-BIRPC` | Call channel between the dsh tool command and the host's tool endpoint: the harness NDJSON JSON-RPC codec | own code |  |  | [libs/birpc.md](birpc.md) |  |
| `DH-LIB-ESBUILD` | TypeScript and JavaScript compiler used by development tooling: replaced by the Go toolchain | Go toolchain |  |  | [libs/esbuild.md](esbuild.md) |  |
| `DH-LIB-REGEXPP` | Regular-expression parsing for exported schema patterns: regexp and regexp/syntax | standard library |  |  | [libs/eslint-communityregexpp.md](eslint-communityregexpp.md) |  |
| `DH-LIB-ESLINTSCOPE` | JavaScript scope analysis for the ES-module-to-CommonJS transform: nothing to rewrite in Go | dropped |  |  | [libs/eslint-scope.md](eslint-scope.md) |  |
| `DH-LIB-NODEINSPECTOR` | V8 inspector session: Go profiler and execution tracer behind owner-only loopback endpoints | standard library |  |  | [libs/node-builtins-vm-worker-module.md](node-builtins-vm-worker-module.md) |  |
| `DH-LIB-NODEMODULE` | Module-loader interface: embedded assets, explicit program lookup and build-time version | standard library |  |  | [libs/node-builtins-vm-worker-module.md](node-builtins-vm-worker-module.md) |  |
| `DH-LIB-NODEWORKER` | Worker threads: goroutines with context for background work, child processes where a hard stop or isolation is needed | standard library |  |  | [libs/node-builtins-vm-worker-module.md](node-builtins-vm-worker-module.md) |  |
| `DH-LIB-PTCGUEST` | Tools from scripts (replaces the PTC guest runtime): own dsh tool command and host endpoint behind the guarded tool pipeline; no embedded interpreter and no JavaScript engine (owner decision OD-2) | own code |  |  | [libs/ptc-guest-runtime.md](ptc-guest-runtime.md) |  |

## Documents and media

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-BIGJS` | Exact decimal comparison and rounding of balances with math/big (replaces big.js) | standard library |  |  | [libs/big.js.md](big.js.md) |  |
| `DH-LIB-LIBREOFFICEKIT` | Office engine for PDF, format conversion, recalculation and page images (replaces @deepseek-ai/libreoffice-kit): own driver for an operator-installed LibreOffice run headless as a child process; nothing of LibreOffice is linked or bundled | own code |  |  | [libs/deepseek-ai-libreoffice-kit.md](deepseek-ai-libreoffice-kit.md) |  |
| `DH-LIB-EXCELJS` | Workbook reading in the host for the spreadsheet preview (replaces exceljs) | named module | `github.com/xuri/excelize/v2` v2.11.1 | BSD-3-Clause | [libs/exceljs.md](exceljs.md) |  |
| `DH-LIB-FASTXMLPARSER` | Small XML reads of workbook parts with encoding/xml (replaces fast-xml-parser) | standard library |  |  | [libs/fast-xml-parser.md](fast-xml-parser.md) |  |
| `DH-LIB-FORTUNESHEET` | Spreadsheet grid widget: not needed, the host renders an HTML table (replaces @fortune-sheet/core and @fortune-sheet/react) | dropped |  |  | [libs/fortune-sheet.md](fortune-sheet.md) |  |
| `DH-LIB-PAPAPARSE` | CSV and TSV parsing with encoding/csv (replaces papaparse) | standard library |  |  | [libs/papaparse.md](papaparse.md) |  |
| `DH-LIB-PDFJSDIST` | PDF preview island that loads vendored prebuilt pdf.js files embedded in the binary (keeps pdfjs-dist as a browser asset) | own code |  |  | [libs/pdfjs-dist.md](pdfjs-dist.md) |  |
| `DH-LIB-SHARP` | Image decoding, scaling and re-encoding for attachments with the standard library and golang.org/x/image (replaces sharp) | named module | `golang.org/x/image` v0.48.0 | BSD-3-Clause | [libs/sharp.md](sharp.md) |  |
| `DH-LIB-SHERPAONNX` | Local speech recognition as a supervised child process running an operator-installed program (example: the prebuilt sherpa-onnx runtime), plus an opt-in remote HTTP provider (replaces sherpa-onnx-node) | own code |  |  | [libs/sherpa-onnx-node.md](sherpa-onnx-node.md) |  |
| `DH-LIB-TURNDOWN` | HTML to Markdown conversion for web_fetch (replaces turndown and its GFM plugin) | named module | `github.com/JohannesKaufmann/html-to-markdown/v2` v2.5.2 | MIT | [libs/turndown.md](turndown.md) |  |
| `DH-LIB-XLSX` | Legacy .xls reading: no parser; converted by the Office engine (replaces SheetJS xlsx) | dropped |  |  | [libs/xlsx.md](xlsx.md) |  |

## Browser use and computer use

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-CDPROTO` | Generated Go types for the Chrome DevTools Protocol domains the provider calls directly (accessibility, fetch, input, target) | named module | `github.com/chromedp/cdproto` v0.160.5 | MIT | [libs/browserbasehqstagehand.md](browserbasehqstagehand.md) |  |
| `DH-LIB-STAGEHAND` | Native browser-use provider over the Chrome DevTools Protocol (replaces @browserbasehq/stagehand; the AI layer is not kept) | named module | `github.com/chromedp/chromedp` v0.24.0 | MIT | [libs/browserbasehqstagehand.md](browserbasehqstagehand.md) |  |
| `DH-LIB-CUAHELPER` | Native computer-use helper program and its packaging: not shipped; computer use is reached only through an operator-installed MCP server (drops scripts/cua-helper) | dropped |  |  | [libs/cua-helper.md](cua-helper.md) |  |
| `DH-LIB-PLAYWRIGHT` | Real-browser test driver for UI scenarios (replaces playwright) | named module | `github.com/chromedp/chromedp` v0.24.0 | MIT | [libs/playwright.md](playwright.md) |  |
| `DH-LIB-PUPPETEERBROWSERS` | Finding and starting an installed Chromium-family browser (replaces @puppeteer/browsers) | own code |  |  | [libs/puppeteerbrowsers.md](puppeteerbrowsers.md) |  |
| `DH-LIB-CUADRIVER` | Recipe (launch arguments and a mapping onto the fixed computer-use catalogue) for the external, operator-installed Cua Driver program over MCP (replaces the in-process @trycua/cua-driver runtime) | own code |  |  | [libs/trycuacua-driver.md](trycuacua-driver.md) |  |

## Desktop application, packaging and release

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-COS` | Release hosting: release assets published by GoReleaser and the release workflow (replaces cos-nodejs-sdk-v5) | Go toolchain |  |  | [libs/cos-nodejs-sdk-v5.md](cos-nodejs-sdk-v5.md) |  |
| `DH-LIB-TEO` | Content-delivery cache purge (not needed: no asset is replaced in place) | dropped |  |  | [libs/cos-nodejs-sdk-v5.md](cos-nodejs-sdk-v5.md) |  |
| `DH-LIB-ELECTRONBUILDER` | Signing, notarisation and archiving of the one dsh binary per platform: GoReleaser with the operating systems' signing tools; no bundle and no installer (replaces the electron-builder family) | Go toolchain |  |  | [libs/electron-builder.md](electron-builder.md) |  |
| `DH-LIB-ELECTRONUPDATER` | Update of the one dsh binary: own signed-manifest verifier, optional notify-only check and single-file swap on the standard library (replaces electron-updater) | own code |  |  | [libs/electron-updater.md](electron-updater.md) |  |
| `DH-LIB-ELECTRON` | No desktop shell: the dsh command opens the web UI in the system browser, and each Electron feature has a web or command-line equivalent or a recorded drop (replaces electron) | own code |  |  | [libs/electron.md](electron.md) |  |
| `DH-LIB-OCTOKITWEBHOOKS` | GitHub webhook signature check: HMAC-SHA-256 on the standard library (replaces @octokit/webhooks) | standard library |  |  | [libs/octokit-webhooks.md](octokit-webhooks.md) |  |
| `DH-LIB-NODESEA` | Location of the running program: os.Executable behind one function (replaces node:sea) | standard library |  |  | [libs/yao-pkg-pkg.md](yao-pkg-pkg.md) |  |
| `DH-LIB-YAOPKG` | Single-file executable: go build with cross-compilation and the embed package (replaces @yao-pkg/pkg) | Go toolchain |  |  | [libs/yao-pkg-pkg.md](yao-pkg-pkg.md) |  |

## User interface: rendering

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-LEXICAL` | Message composer: own JavaScript island that loads lexical 0.49.0 and its @lexical packages as vendored, unmodified browser files embedded in the binary (not a Go module; files not yet fetched, so licence and hashes are unrecorded) | own code |  |  | [libs/lexical.md](lexical.md) |  |
| `DH-LIB-ANSER` | Terminal colour and line replay for command output, as own code (replaces anser) | own code |  |  | [libs/micromark.md](micromark.md) |  |
| `DH-LIB-DOMPURIFY` | Allow-list HTML filtering on the host: the transcript gate, the MathML policy and the HTML file preview filter (replaces dompurify) | named module | `github.com/microcosm-cc/bluemonday` v1.0.27 | BSD-3-Clause | [libs/micromark.md](micromark.md) |  |
| `DH-LIB-KATEX` | Typesetting math on the host as filtered MathML behind one interface; provisional until DH-T-KATEX-3 passes (replaces katex) | named module | `github.com/wyatt915/treeblood` v0.1.16 | MIT | [libs/micromark.md](micromark.md) | **provisional** until `DH-T-KATEX-3` passes |
| `DH-LIB-MICROMARK` | Markdown parsing and rendering on the host (replaces the mdast-util and micromark packages) | named module | `github.com/yuin/goldmark` v1.8.7 | MIT | [libs/micromark.md](micromark.md) |  |
| `DH-LIB-MICROMARKMATH` | Recognising math delimiters in Markdown (replaces micromark-extension-math and mdast-util-math) | named module | `github.com/gohugoio/hugo-goldmark-extensions/passthrough` v0.6.0 | Apache-2.0 | [libs/micromark.md](micromark.md) |  |
| `DH-LIB-SHIKI` | Source code highlighting on the host, tokeniser only (replaces shiki and @shikijs/langs) | named module | `github.com/alecthomas/chroma/v2` v2.27.0 | MIT | [libs/micromark.md](micromark.md) |  |
| `DH-LIB-REACT` | Rendering and client state: host-rendered fragments, state on the host, a small own script (replaces react, react-dom, zustand, immer, use-sync-external-store, clsx) | own code |  |  | [libs/react.md](react.md) |  |
| `DH-LIB-SIMPLEICONS` | Site marks for well-known link hosts: a fixed host table and an embedded sprite of copied marks (replaces simple-icons) | own code |  |  | [libs/simple-icons.md](simple-icons.md) |  |
| `DH-LIB-REACTVIRTUAL` | Long lists: host paging by sequence number, lazy fold bodies and CSS layout skipping (replaces @tanstack/react-virtual) | own code |  |  | [libs/tanstack-react-virtual.md](tanstack-react-virtual.md) |  |

## User interface: stack

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-D3TIME` | Round calendar tick marks for a time axis: the time package plus a fixed ladder of steps (replaces d3-time) | standard library |  |  | [libs/d3-time.md](d3-time.md) |  |
| `DH-LIB-GOUIHTMX` | htmx, one vendored JavaScript file embedded in the binary (an npm package, not a Go module) | named module | `npm:htmx.org` 2.0.11 | 0BSD | [libs/go-ui-stack.md](go-ui-stack.md) | a vendored browser file, not a Go module |
| `DH-LIB-GOUISTACK` | The Go UI stack as a whole: conventions for views, fragments, stream events, islands and policy, plus one first-party script | own code |  |  | [libs/go-ui-stack.md](go-ui-stack.md) |  |
| `DH-LIB-GOUISTREAM` | Live updates and the attention stream for notifications: own stream script on the browser EventSource and htmx.swap, and own stream handlers on net/http (htmx-ext-sse 2.2.4 not adopted: no licence in its manifest or repository) | own code |  |  | [libs/go-ui-stack.md](go-ui-stack.md) |  |
| `DH-LIB-GOUITEMPLATES` | HTML templates: html/template behind a typed view registry (templ v0.3.1118 verified as the fallback, not adopted) | standard library |  |  | [libs/go-ui-stack.md](go-ui-stack.md) |  |
| `DH-LIB-XTERMJS` | Browser terminal widget: xterm.js 6.0.0 with @xterm/addon-fit 0.11.0, vendored ES modules in one framed island (npm packages, not Go modules) | named module | `npm:@xterm/xterm (with npm:@xterm/addon-fit 0.11.0)` 6.0.0 | MIT | [libs/xtermxterm.md](xtermxterm.md) | a vendored browser file, not a Go module |

## Storage and platform

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-NODEPLATFORM` | Streams, events, bytes, text decoding and per-call state: the Go standard library, cordis typed events and one incremental UTF-8 decoder | standard library |  |  | [libs/node-platform.md](node-platform.md) |  |
| `DH-LIB-SQLITE` | SQLite driver for the session store, search index and machine state: modernc.org/sqlite through database/sql, pure Go, provisional until measured | named module | `modernc.org/sqlite` v1.62.0 | BSD-3-Clause (embeds SQLite, public domain, and sqlite-vec, MIT) | [libs/sqlite.md](sqlite.md) | **provisional** until measured |

## Python SDK

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-PYSDK` | Python SDK dependencies: generated standard-library dataclasses, a Go wheel writer and unittest (replaces pydantic, hatchling, pytest, Pillow and the separate runtime wheel) | own code |  |  | [libs/python-sdk.md](python-sdk.md) |  |

## Declared by area specifications

| Library ID | What it is for | Go choice | Module and version | Licence | Specification | Note |
|---|---|---|---|---|---|---|
| `DH-LIB-HUMANKEYRING` | Operating-system keychain access (Windows Credential Manager, macOS Keychain, Linux Secret Service) for the one data key of the secret store | named module | `github.com/zalando/go-keyring` v0.2.8 | MIT | [areas/interaction-credentials.md](../areas/interaction-credentials.md) |  |
| `DH-LIB-CONNECT` | Character-set detection for fetched pages (header, byte-order mark, HTML meta prescan) with golang.org/x/net/html/charset | named module | `golang.org/x/net` v0.63.0 | BSD-3-Clause | [areas/mcp-lsp-web-connector.md](../areas/mcp-lsp-web-connector.md) |  |
| `DH-LIB-SANDBOXMUX` | Stream multiplexer over the one SSH channel (replaces forwarded Unix sockets and TLS pre-shared keys) | named module | `github.com/hashicorp/yamux` v0.1.2 | MPL-2.0 | [areas/sandbox-ssh.md](../areas/sandbox-ssh.md) | **recommended default**: MPL-2.0 (file-level copyleft); the MIT alternative is `github.com/xtaci/smux` |
| `DH-LIB-UICORE` | Language matching and plural selection for the message catalogue (golang.org/x/text: language, feature/plural) | named module | `golang.org/x/text` v0.44.0 | BSD-3-Clause | [areas/ui-core.md](../areas/ui-core.md) |  |

## Totals

152 library items: 34 standard library, 40 own code, 54 named module, 11 Go toolchain, 13 dropped.

One version is used for each module that more than one item names: `golang.org/x/net`
v0.63.0, `golang.org/x/sys` v0.50.0, `golang.org/x/text` v0.44.0,
`github.com/klauspost/compress` v1.20.4, `github.com/chromedp/chromedp` v0.24.0,
`github.com/google/jsonschema-go` v0.4.3, `go.yaml.in/yaml/v4` v4.0.0 and
`github.com/bmatcuk/doublestar/v4` v4.10.4. The minimum Go version these modules imply
for the harness module is recorded with conflict CF-4 in [../design.md](../design.md) §8.
