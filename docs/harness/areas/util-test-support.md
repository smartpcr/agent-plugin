# Shared utilities and test support: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in [../README.md](../README.md). Statements that are reasoning and not read from source open with "Inference". IDs in this document use the area key `UTIL` (cluster `A23`). Nothing here has been built, run or tested.

| | |
|---|---|
| **Area key** | `UTIL` (cluster `A23`) |
| **Upstream packages** | 32 in two groups: `util` (24), `test-support` (8) |
| **Serves outcomes** | [DH-O-15](../design.md#dh-o-15) first; also [DH-O-1](../design.md#dh-o-1), [DH-O-2](../design.md#dh-o-2), [DH-O-4](../design.md#dh-o-4), [DH-O-5](../design.md#dh-o-5), [DH-O-6](../design.md#dh-o-6), [DH-O-7](../design.md#dh-o-7), [DH-O-9](../design.md#dh-o-9), [DH-O-14](../design.md#dh-o-14) |
| **Depends on areas** | A04 (adapter seam, `llm.StreamAround`), A01 and A05 (export format, `surface.Rebuild`, an in-memory store), A12 (child lineage in a session header), A03 (the direct-call entry of the pipeline), A08 (`fsx`), A09 (the one process starter and its tree ownership), A18 (the sharing permit), A21 (the launch environment `launch.Env`, the composition function), A24 (commands), F7 (gate runner, analyzer binary) |
| **Priority** | P0 for `schema`, `egress`, the leaf packages and the test kit, because every other area's first test and first tool needs them. P1 for `watch`, recurrence, replay and scenario accounting. Nothing here is P2 |

## 1. Purpose

This area holds the pieces every other area leans on and nobody else owns. The first half is four shared mechanisms (how a data shape is described and checked, how the host makes an outbound connection, how it notices a file changed, how it reads a glob or a version) and a handful of small leaf packages. Without them each area would grow its own, which is how upstream arrived at five schema systems and three HTTP stacks ([../libs/zod.md](../libs/zod.md) §3, [../libs/undici.md](../libs/undici.md) §1).

The second half is what makes [DH-O-15](../design.md#dh-o-15) checkable: the test kits, the recorded-session replay tool, and the rule that decides when a real-environment scenario counts. Without it a green run would not say which kind of evidence stands behind it.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|
| **Schema, document, subset** | A schema is the description of a data shape, derived from a Go type. Its document is the JSON Schema text. The model subset is the fixed list of keywords a tool schema may use. | One declaration serves the model, validation, forms and catalogs ([DH-ADR-SCHEMA-1](../libs/zod.md#dh-adr-schema-1)). |
| **Fingerprint** | A SHA-256 hash of a persisted type's wire shape. | A test notices that a saved format changed. |
| **Egress, proxy policy** | Egress is every connection the host opens to another machine. The policy says, per URL, which proxy to use or none. | An operator behind a proxy needs one answer for all traffic. |
| **Permit, audience** | A permit is area A18's "yes, for now" to send data to a vendor or collector. An audience says who a child process's environment is built for: a trusted program or a command run for the model. | [DH-ADR-9](../design.md#dh-adr-9) is enforced where the client is made; a proxy password is not handed to model-run code. |
| **Watch, settle window** | A watch tells its owner "re-read this path". The settle window is the quiet period that turns a burst of changes into one call. | Editors save in several steps. |
| **Seam fake, conformance suite** | A fake is an in-process stand-in for the far side of a seam. A conformance suite is the set of tests every provider of that seam, fake or real, must pass. | A fake that drifts from the real provider proves nothing. |
| **Scripted model** | A fake model adapter that plays a written list of replies and records every request. | The model is the one boundary that is always expensive and never deterministic. |
| **Composition harness** | A test helper that boots real components through the real loader from real layers. | Hand-wired tests miss load-order and ownership defects ([../upstream/toolchain.md](../upstream/toolchain.md) §3). |
| **Recorded session, normalisation token** | A committed session export used as both input and expected result. A token such as `{{session:1}}` replaces a value that differs between runs and keeps equal values equal. | Keyless proof of the assembled transcript. |
| **Virtual time, bubble** | Inside `testing/synctest` the clock is fake and advances only when every goroutine of the test (the bubble) is blocked. | Timer tests finish instantly and deterministically. |
| **Trust boundary, fuzz target** | A boundary is a function that parses bytes the harness did not write. A fuzz target feeds it generated input. | Hostile input is found before release. |
| **Scenario, required list, evidence report** | A scenario is a test against something real. The required list names scenarios that must pass in a given run. The report has one row per scenario with its result. | A skip is recorded and can never be read as a pass. |

## 3. What upstream does

### 3.1 Packages

Unit IDs are `UP-PKG-packages.` plus the first column. Paths are relative to `packages/util/` or `packages/test-support/`. The last column is the placement decided here ([DH-ADR-UTIL-3](#dh-adr-util-3)).

| Upstream unit | Role | What it does | Go placement |
|---|---|---|---|
| `util.atomic-write` | other | Replaces a file by exclusive-create temporary plus rename; a `.lock` sibling holding a process ID serialises writers (`atomic-write/README.md:83-85`) | **Merged** into `fsx.Replace` and the `fsx` kernel lock (A08) |
| `util.brand` | definition | Nominal string and number types by a compile-time-only symbol (`brand/README.md:12`, `brand/src/index.ts:18-37`) | **Gone**: a defined Go type per identifier ([DH-ADR-UTIL-4](#dh-adr-util-4)) |
| `util.chunked-list` | other | Persistent append-only list in 64-value chunks for frozen projection state (`chunked-list/README.md:12`, `:48`) | **Standard library**: an append-only slice handed out as a capped view `s[:n:n]` |
| `util.code-language` | definition | File-extension to highlighting-language table shared by the client and the read card (`code-language/README.md:12`) | **Merged** into `mediatype.Language` |
| `util.crypto` | other | A v4 UUID from `getRandomValues`, because `randomUUID` is missing on plain-HTTP pages; a base64 helper (`crypto/README.md:12`) | **Harness package** `ident`; `encoding/base64` |
| `util.deque` | other | Circular deque so that draining a long queue is not quadratic (`deque/README.md:12`) | **Standard library**: a bounded channel, or a slice ring inside its one owner |
| `util.error` | definition | `HarnessError`, a base class with a stable `code`, kept dependency-free so every package can extend it (`error/README.md:12`, `:54`) | **Harness package** `herr` |
| `util.file-drag` | other | Safe export file names from a declared media type; native drag requests for the desktop application (`file-drag/README.md:23-27`) | **Merged**: naming into `mediatype.SafeName`; desktop drag gone ([DH-ADR-18](../design.md#dh-adr-18)) |
| `util.home-paths` | other | One resolver for the harness home (explicit, `$DSH_HOME`, `~/.dsh`), category paths, tilde expansion, watch-path canonicalisation (`home-paths/README.md:39`, `:53`, `:78`) | **Harness package** `homedir` (resolver, tilde, display); layout to `home` (A07); canonicalisation into `watch` |
| `util.http-proxy` | provider | One outbound proxy policy from the launch snapshot, installed as the process-wide dispatcher (`http-proxy/README.md:28`, `:51-57`) | **Harness package** `egress` |
| `util.launch-environment` | definition | Frozen snapshot of three environment layers that remembers which supplied each value (`launch-environment/README.md:38-52`) | **Merged** into `launch.Env` (A21, [boot.md](boot.md) §6.1) |
| `util.lazy-require` | other | Loads a CommonJS dependency on first use (`lazy-require/README.md:12`) | **Gone**: Go links statically; lazy initialisation is `sync.OnceValue` |
| `util.native-command` | other | Shell-free `execFile` wrapper; open, reveal and list applications for a path, with WSL translation (`native-command/README.md:12`, `:50-54`) | **Merged**: the runner into the one process package (A09); only the default-browser hand-off of `DH-LIB-OPEN` is kept; reveal and the application list are not built |
| `util.official-bundles` | definition | Compiled-in lists: profile templates, optional, on-demand and retired bundles, protected plugins (`official-bundles/README.md:12`, `:31-44`) | **Merged** into embedded bundles and profiles in `internal/boot` (A27, A21) |
| `util.output-retention` | other | Item and byte-window retainers with an omission notice (`output-retention/README.md:12`, `:56`, `:83`) | **Harness package** `retain` |
| `util.package-manifest` | definition | TypeScript types for the `package.json` fields a plugin declares (`package-manifest/README.md:12`, `:46-56`) | **Merged** into the plugin manifest struct (A22) |
| `util.plugin-detail` | other | Chooses a plugin's long description (locale `meta.detail`, README, repository README) from a directory or npm file CDNs (`plugin-detail/README.md:32-36`, `:50-52`) | **Merged** into A22 over the local plugin directory; CDN sources gone |
| `util.plugin-market-index` | definition | Validator for market index format 1 and pinned install arguments (`plugin-market-index/README.md:12`, `:29-35`) | **Dropped** ([DH-ADR-13](../design.md#dh-adr-13)) |
| `util.ripgrep` | other | Resolves and spawns the packaged `rg` and streams its records (`ripgrep/README.md:12`) | **Gone**: search runs in process (A08, [DH-ADR-RIPGREP-1](../libs/vscode-ripgrep.md#dh-adr-ripgrep-1)) |
| `util.text-search` | other | Tiered multilingual fuzzy filter for in-memory lists (`text-search/README.md:12`, `:61-66`) | **Harness package** `textsearch` |
| `util.time` | definition | Strict instant and zone parsers, a millisecond `Duration`, bounded cron and RRULE (`time/README.md:12`, `:32`) | **Harness packages** `caltime`, `recur` |
| `util.timeout` | other | `clampTimeout`, `deadline`, `timeoutOf`, `idleWatchdog`: timing and classification, never termination (`timeout/README.md:12`, `:55-59`, `:74`) | **Standard library** `context`; the idle timer is `egress.IdleBody` |
| `util.values` | other | Lossless-JSON checks, snapshots, deep freeze and equality, `assertNever`, the lazy `PartialArguments` reader (`values/README.md:29`, `:42`, `:48-54`) | **Standard library**, except the reader: **harness package** `jsonscan` |
| `util.workspace-path` | other | Browser-safe lexical joins, display titles, the `dsh-resource://file/` address grammar (`workspace-path/README.md:12`, `:25-29`) | **Merged** into host rendering (A07 titles, A19 file routes); lexical helpers gone |
| `test-support.agent-loop-testkit` | other | Mounts the loop's prerequisite services, drives a production agent, offers two Inbox stubs (`agent-loop-testkit/README.md:12`, `:34`, `:61`) | `testkit/compose`; no Inbox stub |
| `test-support.client-runtime` | other | jsdom bench for React slots and a whole-client tier over a Remote mock (`client-runtime/README.md:14`, `:77`) | Replaced by golden HTML and query helpers; there is no client program |
| `test-support.llm-mock-server` | other | Scriptable Messages HTTP and SSE server: resets, stalls, malformed chunks, 429 and 5xx, a seeded random mix (`llm-mock-server/README.md:12`, `:54-72`) | `testkit/faultserver` |
| `test-support.llm-replay` | provider | Answers `llm/stream` from a recorded session log; scripts bound to sessions in first-call order; an override sidecar (`llm-replay/README.md:12`, `:34`, `:70-80`) | `internal/llm/replay` |
| `test-support.loader-smoke` | other | Boots a fixture's real bin and `cordis.yml` in an isolated directory, from source or built output (`loader-smoke/README.md:12`, `:32`, `:56`) | `testkit/dshbin` |
| `test-support.remote-mock` | other | Endpoint-named mock of Typert Remote traffic for browser specs (`remote-mock/README.md:12`) | Not applicable: no Typert, no client |
| `test-support.session-snapshot` | other | Manifests, identity tokens, normalisation, workspace comparison and protocol adapters for recorded-session tests (`session-snapshot/README.md:12`, `:71-73`, `:85`) | `test/replay` runner, `testkit/fixture` |
| `test-support.tool-testkit` | other | Fixture tool definitions and direct pipeline execution (`tool-testkit/README.md:12`, `:41`) | `testkit` tool fixture and conformance helper |

### 3.2 Behaviour

**Nothing here is a service.** Each utility states that it is "a library, not a service or plugin": no context, no registration, no state (`crypto/README.md:27`, `time/README.md:30`, `http-proxy/README.md:28`). The group exists so that capability packages share mechanical primitives instead of copies (`README.md:12`). Two units touch the tree: the launch snapshot sits in the context slot `launchEnvironment` (`launch-environment/src/index.ts:106`), and the replay plugin injects `llm` (`llm-replay/src/index.ts:1113`). No utility emits an event, persists data or exposes a tool.

**Launch and network.** The launcher freezes three layers (inherited environment, the invoking directory's `.env`, `$DSH_HOME/.env`) and ranks them in a fixed trust order; a caller that must never trust a layer leaves it out of the lookup (`launch-environment/README.md:38-48`). The proxy policy is resolved from that snapshot once and installed before the first plugin loads (`http-proxy/README.md:28`). A project `.env` may not name a proxy, an unusable value is reported and its scheme connects directly (`:51-57`), and a spawned child gets the user's own values plus the resolved ones (`:70`). [../libs/undici.md](../libs/undici.md) §2 holds the full rule list.

**Time and deadlines.** Boundaries call strict parsers that answer "accepted" or "rejected" with a stable reason, and each caller maps the reason to its own error (`time/README.md:51`). Cron takes five fields (`time/src/cron.ts:277`). `deadline` fuses a timeout and an upstream cancellation into one signal; the caller must attach its own termination, because racing a promise against a timer would leak the child or socket (`timeout/README.md:55`).

**Test support** gives three ways to run the real product without a key (`README.md:12`). (1) `agent-loop-testkit` mounts the prerequisite services by hand and returns real agents; it mounts no model adapter (`agent-loop-testkit/README.md:79`). (2) `loader-smoke` starts a fixture's real entry point in a child process (`loader-smoke/README.md:32`). (3) Recorded sessions: `session-snapshot` starts the shipped profile, `llm-replay` answers model calls from the committed log, and the kit compares the normalised log the product wrote with the committed one. The fixture is the persisted log without `seq` and `time`, with identities replaced by typed first-seen tokens (`session-snapshot/README.md:73`, `session-snapshot/src/identity.ts:66`). The replay script is derived from the log's own events (`llm-replay/src/index.ts:468`); what a log cannot hold (a throw before any chunk, a hang) comes from an override sidecar (`llm-replay/README.md:80`). Three modes exist: replay, record, refresh (`session-snapshot/src/manifest.ts:128`). Guards reject orphan directories, unscrubbed prompt text and duplicate sidecars before any comparison runs (`session-snapshot/README.md:102`).

### 3.3 Failure, retry, rollback and cleanup upstream

| Situation | What upstream does | What is left |
|---|---|---|
| An atomic write fails | The temporary file is removed and the target is untouched; Windows retries a blocked rename for a bounded time (`atomic-write/README.md:39`) | Nothing. After a crash the rename may be seen undone, because nothing is synced (`:124`) |
| A lock holder dies | The next writer takes the lock over when the recorded process ID is gone (`atomic-write/README.md:63`) | A lock with an empty record, or a reused process ID, stays until an operator deletes it (`:126`) |
| A smoke child hangs | The deadline kills the direct child and reports both streams (`loader-smoke/README.md:60`) | A process tree the fixture spawned (`:120`); up to 100 MB of captured output (`:119`) |
| Replay meets a call it has no script for, or ends with script left | Fails loudly and says to re-record (`llm-replay/README.md:86-87`) | Nothing |
| A record or refresh run stops half-way | Completed generations are never renamed or deleted (`session-snapshot/README.md:85`) | Inference: a partly rewritten fixture directory, recovered with the version-control tool |

## 4. Upstream decisions dissected

Notes are under `.agents/notes/`; the first 13 rows are the notes handed to this area, the rest are visible only in code or READMEs. Long reasoning is in the ledger rows.

| Upstream decision | What was decided and why | Verdict | Reason | Go decision |
|---|---|---|---|---|
| `implemented/architecture/2026-07-06-timeout-deadline-library` | Pure functions own timeout timing and classification; each capability keeps its hard kill; no timeout service (`:19`) | change | `context.WithTimeoutCause` is that library. The split (timing shared, kill local) is kept | [DH-ADR-UTIL-4](#dh-adr-util-4) |
| `implemented/architecture/2026-07-24-single-harness-home-resolver` | One resolver: explicit path, `$DSH_HOME`, `~/.dsh`; blank is unset; one root (`:18-24`) | keep | Components must not disagree about where data lives; the default stays `~/.dsh` | [DH-D-UTIL-5](#dh-d-util-5) (`homedir`) |
| `implemented/architecture/2026-08-27-outbound-proxy-policy` | One policy from the launch snapshot, installed as the global dispatcher before any entry mounts (`:17-19`) | change | Go has no global to patch; every client is asked for | [DH-ADR-UTIL-5](#dh-adr-util-5), [DH-ADR-UNDICI-1](../libs/undici.md#dh-adr-undici-1) |
| `implemented/architecture/2026-09-10-public-package-manifest` | One public type for the manifest fields; each reader parses and validates for itself (`:13-19`) | change | A Go struct is declaration and validator at once, so there is one reader | [DH-ADR-UTIL-3](#dh-adr-util-3); A22 |
| `implemented/architecture/2026-09-21-experimental-capabilities-as-optional-bundles` | Experimental capabilities ship as default-off bundles, admitted by install cost (`:13-15`) | change | Every component is already in the binary; "optional" is a default-off entry | [DH-ADR-UTIL-3](#dh-adr-util-3); A27 |
| `implemented/architecture/2026-09-24-preparing-tool-arguments` | A lazy reader indexes streamed argument text and reads only requested fields; short fields come first in schemas (`:26-32`) | keep | Re-parsing a growing text per delta is quadratic in any language | [DH-D-UTIL-5](#dh-d-util-5) (`jsonscan`) |
| `implemented/architecture/2026-09-24-shared-time-vocabulary` | One package owns the time forms boundaries accept; parsers return reasons, callers own error text (`:13-15`) | keep | Three private parsers had drifted | [DH-D-UTIL-4](#dh-d-util-4) |
| `implemented/architecture/2026-10-05-official-on-demand-bundles` | Heavy integrations are listed offline and installed on request (`:13-15`) | change | Nothing is installed; the entry reports whether the operator-installed runtime is present | [DH-ADR-UTIL-3](#dh-adr-util-3), [DH-ADR-23](../design.md#dh-adr-23) |
| `implemented/architecture/2026-11-05-harness-error-dependency-free-base` | The error base lives in a package with no dependencies and is re-exported so `instanceof` agrees (`:15-17`) | keep, simplify | The leaf is kept; Go needs no re-export because `errors.As` matches by type | [DH-ADR-UTIL-2](#dh-adr-util-2), `herr` |
| `implemented/feature/2026-10-02-plugin-market-index-format` | A market is one JSON index reusing npm names, versions and ranges (`:17-19`) | drop | Nothing downloads or discovers plugins | [DH-ADR-13](../design.md#dh-adr-13) |
| `implemented/feature/2026-10-15-plugin-detail-documents` | A fixed, localised fallback order for a plugin's long description (`:15`) | change | The order is kept over the local plugin directory; CDN and repository reads go | [DH-ADR-UTIL-3](#dh-adr-util-3); A22 |
| `proposed/architecture/2026-11-04-dsh-home-path-categories` | Proposal: group home paths as `config`, `data`, `state`, `cache`, `backup` (`:13-19`) | change | The purpose (what may be deleted, what must be backed up) is met by A07's layout, which is new and needs no migration | [DH-ADR-UTIL-3](#dh-adr-util-3); A07 |
| `implemented/process/2026-09-15-shipped-optional-bundles` | A named list of shipped, switched-off bundles (`:13`) | change | As the optional-bundles row | [DH-ADR-UTIL-3](#dh-adr-util-3); A27 |
| The `util` group may be imported by every group and imports none (`2026-08-27-outbound-proxy-policy.md:21-23`) | A place for shared primitives | keep | It is what keeps the import graph acyclic | [DH-ADR-UTIL-2](#dh-adr-util-2) |
| Transport policy is a library because it has one answer per process (`http-proxy/README.md:28`) | Nothing to swap | change | A service owned by the root fiber gives tests a seam and connections an owner | [DH-ADR-UTIL-5](#dh-adr-util-5) |
| Replay binds a session to its script by first-call order (`llm-replay/README.md:74`) | Live ids are random | change | Order is not deterministic once children run concurrently | [DH-ADR-UTIL-7](#dh-adr-util-7) |
| Fixture guards run before comparisons; "fix fixtures, not normalizers" (`session-snapshot/README.md:102`) | A fixture must be a fixed point | keep | A normaliser that hides a difference hides a defect | [DH-ADR-UTIL-7](#dh-adr-util-7) |
| Stubs for spine internals (two Inbox stubs) (`agent-loop-testkit/README.md:61`) | Cheap consumer tests | drop | A real agent on an in-memory store is as cheap in Go and cannot drift | [DH-ADR-UTIL-6](#dh-adr-util-6) |
| An unstubbed call fails loudly (`client-runtime/README.md:108`, `remote-mock/README.md:90`) | A missing stub must not pass silently | keep | Same rule for every fake | [DH-ADR-UTIL-6](#dh-adr-util-6) |

## 5. Tech debt and its resolution

86 units, grouped by cause. Each unit has its own ledger row with the per-unit reason.

| Group (count) | Units | How the Go design answers |
|---|---|---|
| **A. The cause is JavaScript, Node, npm or the browser client** (28, not applicable) | `client-runtime` 1 to 5; `remote-mock` 1 to 5; `loader-smoke` 1; `session-snapshot` 2; `lazy-require` 1, 2; `chunked-list` 1, 2; `values` 1 to 3; `workspace-path` 1, 2; `time` 3, 4, 6; `plugin-detail` 1; `plugin-market-index` 1, 2; `native-command` 2 | There is no React client or Typert layer to mock ([DH-ADR-3](../design.md#dh-adr-3)), no source-versus-built split, no module loader, no frozen object graph ([DH-ADR-ARCH-11](../upstream/architecture.md#dh-adr-arch-11)), no npm source for plugins ([DH-ADR-13](../design.md#dh-adr-13)); `time.Duration` and typed values are the language's own |
| **B. A primitive left durability, bounds or cleanup to every caller** (23, resolved) | `atomic-write` 1 to 4 and its marker; `deque` 1; `output-retention` 1, 2; `native-command` 1; `loader-smoke` 2, 3; `home-paths` 2; `package-manifest` 1, 2; `official-bundles` 1; `ripgrep` 1, 2; `launch-environment` 1; `timeout` 2; `time` 5; `crypto` 1; `code-language` 1, 3 | One owning package does the hard part: `fsx.Replace` syncs and its lock is the kernel's (A08); every queue states its bound ([DH-ADR-UTIL-4](#dh-adr-util-4)); `retain` cuts items and text at both ends; the test runner caps output and ends the process tree; a manifest is a validated struct; the launch environment is never written into the process environment (A21) |
| **C. Outbound traffic that escaped the one policy** (6: 3 resolved, 3 carried) | `http-proxy` 1 to 6 | Resolved: configured certificate authorities, telemetry through `egress`, a stated rule for model-run commands. Carried: no PAC or system proxy reading (it needs cgo or WinHTTP); a Node child obeys only if its runtime does; a source check cannot see inside a dependency |
| **D. Test doubles and fixtures that drift from the real path** (15: 12 resolved, 3 carried) | `agent-loop-testkit` 1 to 4; `tool-testkit` 1 to 3; `llm-mock-server` 1 to 3; `llm-replay` 1, 2 and its marker; `session-snapshot` 1, 3 | Fakes exist only at seams and pass the seam's suite; composition and teardown belong to the harness ([DH-ADR-UTIL-6](#dh-adr-util-6)); replay binds by lineage and harvests through the export command ([DH-ADR-UTIL-7](#dh-adr-util-7)). Carried: a fault mix is test pressure, a hang cannot be recorded, a protocol-driven behaviour is tested through that protocol |
| **E. An answer depended on the runtime or the active language** (2, resolved) | `time` 1; `text-search` 2 | Zone names are kept as given ([DH-ADR-TEMPORAL-2](../libs/js-temporal-polyfill.md#dh-adr-temporal-2)); lists are filtered on the host, which holds every language of the message catalogue |
| **F. A limit kept on purpose** (12, carried) | `time` 2; `timeout` 1, 3, 4; `crypto` 2; `file-drag` 1; `home-paths` 1; `launch-environment` 2; `code-language` 2; `text-search` 1, 3; `native-command` 3 | Each is a rule, not a defect: the recurrence subset is bounded; cancellation is cooperative and records the first cause; random identifiers are probabilistic; names are never guessed from content; a workspace cannot change the harness environment. The ledger row gives each justification |

Carried in total: 18. None is carried for lack of design; each names the constraint that keeps it.

## 6. Go design

### 6.1 Shape

**Packages.** Everything is in module `src/harness` under `internal/` ([DH-ADR-UTIL-1](#dh-adr-util-1)). `internal/schema` is its own family, as [../design.md §4.2](../design.md#42-go-module-layout) lists it. `internal/util/<name>` holds the leaf packages. `internal/llm/replay` holds the replay component, in the family the design names for it. `internal/testkit/...` is imported only by test files and by `test/...`. These two names are final: the description of data shapes is `internal/schema` and nothing else (not `src/harness/schema`), and test support is `internal/testkit` and nothing else (not `internal/harnesstest`). Area A08 owns `util/fsx`, `util/search` and `util/textdiff` inside this family ([filesystem.md](filesystem.md) §6.1): confirmed.

| Package | Kind | Provides | Replaces |
|---|---|---|---|
| `internal/schema` | library, no cordis import | derive, validate, defaults, fingerprint | zod and four other systems ([../libs/zod.md](../libs/zod.md)) |
| `util/egress` | component `util.egress` (named by the rule of [boot.md](boot.md), `DH-ADR-BOOT-13`), service key `egress.Key`, root fiber | every outbound `http.Client`, the resolver, child environments | `http-proxy`, `timeout`'s idle watchdog |
| `util/sysproxy` | library, a leaf: `Read(ctx) (Setting, error)` with one implementation per operating system | the operating system's static proxy setting, read once at launch by boot ([DH-ADR-UTIL-10](#dh-adr-util-10)) | nothing: upstream reads environment variables only (`L:util.http-proxy.1`) |
| `util/watch` | component `util.watch`, key `watch.Key`; sub-package `watchtest` | file and directory watches | five private chokidar watchers ([../libs/chokidar.md](../libs/chokidar.md)) |
| `util/glob`, `util/version` | libraries | one glob dialect; strict versions and ranges | picomatch, semver |
| `util/caltime`, `util/recur` | libraries | instants, zones, wall-time resolution; cron and RRULE rules | `time` |
| `util/herr`, `ident`, `homedir`, `mediatype`, `retain`, `textsearch`, `jsonscan` | libraries | see the table below | `error`, `crypto`, `home-paths`, `code-language`, `file-drag`, `output-retention`, `text-search`, `values` |

**`schema`.** The interface below is what areas A01, A03, A04, A05, A07, A18, A19 and A20 asked for. The module `github.com/google/jsonschema-go` (`DH-LIB-ZOD`) is imported by this package only.

```go
package schema // src/harness/internal/schema

func Of[T any](opts ...Option) (*Type[T], error) // derive once per Go type and option set; cached; immutable
func Open() Option     // the root accepts unknown keys; the default is closed (tools, wire messages, forms)
func Tolerant() Option // decoding ignores unknown fields: a persisted record an older build must read

func (t *Type[T]) Document() Document                      // JSON Schema 2020-12, properties in declaration order
func (t *Type[T]) Decode(raw []byte, into *T) Issues       // structural check, strict decode on top of *into, then into.Validate()
func (t *Type[T]) DecodeForm(v url.Values, into *T) Issues // same checks for an HTML form body (A19)
func (t *Type[T]) Fingerprint() [32]byte                   // SHA-256 of the canonical wire shape (A01, A05)
func (t *Type[T]) Fields() iter.Seq[Field]                 // path, kind, constraints and the live, secret and ui metadata
func Changed[T any](t *Type[T], old, new T) (paths []string, liveOnly bool) // called inside cordis.AcceptUpdate (CF-3)
func Union[T any](discriminator string, variants ...Variant) (*Type[T], error)

func FromJSON(doc []byte) (Document, error)             // a schema supplied at run time by a plugin or MCP server: inert data
func (d Document) CheckSubset(s Subset) Issues          // ModelSubset: every out-of-subset keyword with its path (A03)
func (d Document) Project(p Profile) (Document, []Loss) // one provider's dialect; A04 owns the profiles
func (d Document) Validate(value any) Issues
type Issue struct{ Path, Code, Message string }         // Issues lists every violation, not the first
func Locate(raw []byte, path string) (line, col int, ok bool) // where a JSON input holds the value at Path (A21, A22)
```

`into` arrives holding the defaults, so absent fields keep them ([DH-ADR-SCHEMA-3](../libs/zod.md#dh-adr-schema-3)). A `time.Duration` field derives to a string in Go duration form (`"30s"`), so no caller multiplies a bare number by a unit. Struct tags, the two "required" rules and the keyword subset are as [../libs/zod.md](../libs/zod.md) §6 and [../libs/schemastery.md](../libs/schemastery.md) state; they are not repeated here.

**`egress`.** The shape of [../libs/undici.md](../libs/undici.md) §6, with four additions other areas asked for: an owner, a sharing permit, an audience for child environments, and the idle body.

```go
package egress // src/harness/internal/util/egress

func Resolve(lookup func(name string) (string, bool), c Config) (*Service, []Diagnostic, error) // once, before any component loads
type Options struct {
    Redirect   func(req *http.Request, via []*http.Request) error // nil: refuse every redirect
    Guard      func(addr netip.AddrPort) error                    // nil: allow; checked on the address actually dialled
    HeaderWait time.Duration
    Sharing    *Sharing // nil: a request the product needs to work. Set: data sent to a vendor or collector
}
type Sharing struct{ Label string; Permit interface{ Context() context.Context } } // A18's sharing.Permit satisfies it
func (s *Service) Client(owner *cordis.Context, o Options) (*http.Client, error) // the release is an effect of owner's fiber
func (s *Service) ProxyFor(u *url.URL) *url.URL          // the same function the transport calls
func (s *Service) ChildEnv(a Audience) map[string]*string // Trusted, or ModelRun: no value that carries a password
func (s *Service) Handed() []ClientInfo                  // owner, purpose and label of every live client
func ClearedEnv() map[string]*string
func ReadCapped(resp *http.Response, max int64) (body []byte, truncated bool, err error)
func IdleBody(body io.ReadCloser, idle time.Duration) io.ReadCloser // a read that waits longer fails with ErrIdle and closes
func Classify(err error) FailureKind
```

`lookup` is supplied by boot from area A21's `launch.Env`, restricted to the process and home-file sources; `egress` never reads the process environment itself. What a client sends is the request its caller built plus one header, `User-Agent: dsh`, set by the transport after every other header so that no listener can replace it (area A04's rule, [llm.md](llm.md) §6.2). It keeps no cookie jar and adds no identifier. Anything beyond the request itself is opt-in in two steps: the caller's own setting adds the field, and a client whose purpose is to send data to a vendor or collector is created only with a permit. Each request of such a client runs under a context joined with the permit's, so the off-switch cancels it in flight. The service owns the `net.Resolver` as well as the dialler, which is what lets a test see every name lookup.

**`watch`.** The service of [../libs/chokidar.md](../libs/chokidar.md) §6 is adopted as written, with one change: `Watch(owner *cordis.Context, t Target, changed func(err error)) (Watch, error)` takes the owning context explicitly, so the watch is an effect of the caller's fiber. Targets are local paths. A file reached through a non-local filesystem provider is not watched; its consumer probes on use (area A14 already does). `watchtest.Manual` implements the same key with `Fire(path)` and `Degrade(path, err)` for tests.

**`glob` and `version`** are the helpers of [../libs/picomatch.md](../libs/picomatch.md) §6 and [../libs/semver.md](../libs/semver.md) §6 without change, at `util/glob` and `util/version`. They wrap `DH-LIB-PICOMATCH` and `DH-LIB-SEMVER`; no other package imports those modules.

**`caltime` and `recur`.** `caltime` is the package of [../libs/js-temporal-polyfill.md](../libs/js-temporal-polyfill.md) §6. `recur` holds every rule kind the schedule area stores behind one interface, so "latest at or before" has one implementation per kind and no caller reads a clock:

```go
package recur // on util/caltime; pure functions, no timer, no state
type Rule interface {
    Expression() string // the stored text, authoritative
    Zone() string
    Next(after time.Time) (time.Time, bool)
    Resolve(committed, decision time.Time) (Occurrence, error) // latest due at or before decision, never before committed
}
func ParseCron(expr, zone string) (Rule, *Rejection)            // five fields: minute hour day-of-month month day-of-week
func ParseRRule(text string, authoring bool) (Rule, *Rejection) // ../libs/rrule.md R1 to R10
func Interval(anchor time.Time, every time.Duration) Rule       // elapsed time
func WallTime(zone string, weekdays []time.Weekday, at caltime.LocalTime) (Rule, *Rejection) // daily and weekly
```

The cron rule that area A13 asked to have stated: when the hour field is one or more fixed hours, an occurrence is a wall time in the rule's zone, a skipped wall time gives no occurrence and a repeated one fires once at the earlier instant. When the hour field is `*` or a step (`*/n`), each real minute is matched against its own local fields, so nothing is skipped and both passes of a repeated hour fire ([jobs-schedule-goal-plan.md](jobs-schedule-goal-plan.md) §6.2).

**Leaf packages.**

| Package | Interface in one line | Notes |
|---|---|---|
| `herr` | `type Reason string` (`"<domain>/<reason>"`, a constant declared by the package that owns the failure); `herr.New(r, msg)`, `herr.Wrap(r, err, msg)`, `herr.ReasonOf(err) (Reason, bool)`; the error type has a method `Reason() string` | The in-process value behind the one error table. It imports nothing, so every package can return it. The wire codec lives in the module `src/sdk`, which cannot import the harness ([sdk-acp-typert.md §6.1](sdk-acp-typert.md)), so it does not call `ReasonOf`: it writes `error.data.code` from any error in the chain that has a `Reason() string` method, which every `herr` error has |
| `homedir` | `Resolve(explicit string, lookup) (string, error)`, `Default()`, `ExpandTilde(path)`, `Display(path)` | Explicit path, then `DSH_HOME`, then `~/.dsh`; a blank value is unset; only `~` and `~/` expand. Boot (A21) and the command line (A24) call it; the layout below the root is A07's |
| `ident` | `ident.New() string` | [DH-ADR-NODECRYPTO-2](../libs/node-crypto-timers-perf-hooks.md#dh-adr-nodecrypto-2); opaque, never parsed |
| `mediatype` | `ByPath`, `Extensions(type)`, `SafeName(name, type, ext)`, `Language(path)` | The table of [../libs/mime.md](../libs/mime.md) plus the highlighting-language table. `Language` also matches whole file names (`Dockerfile`, `Makefile`) |
| `retain` | `retain.Text(mode, budget)` and `retain.Items[T](mode, n)` with `Head`, `Tail`, `HeadTail`; `Notice(omitted, unit)` | Cuts on rune boundaries; reports an exact omitted count; "truncated" means only "a budget cut this" |
| `textsearch` | `New(items, keys)`, `Index.Update`, `Index.Search(query)` | [../libs/fuzzysort.md](../libs/fuzzysort.md) §5 |
| `jsonscan` | `Reader.Append(delta)`; `Reader.String(key, max)`, `Reader.Complete()` | Indexes top-level keys of streamed argument text; decodes only what a card asks for |

**Test kit.** One rule decides where a fake lives ([DH-ADR-UTIL-6](#dh-adr-util-6)): the owner of a seam ships the seam's conformance suite and an in-memory fake beside its contract package; `internal/testkit` holds what crosses areas.

| `testkit` package | What it gives | Used by |
|---|---|---|
| `compose` | `compose.Start(t, Options{Bundle, Layers, Home})`: a temporary home, the real loader and composition function, a scripted model on a default route, a recording egress, a fake answerer; unloads the tree in `t.Cleanup` and fails the test if an effect or goroutine is left | every area's composition tests |
| `model` | The scripted model: an `llm.Adapter` whose script yields chunks, a `*llm.Failure`, or blocks until cancelled; records every request; fails on a call with no script and on script left over | A02, A03, A11 to A14 |
| `egresstest` | A recording transport and resolver behind `egress.Key`; scripted dial failures (refused, reset, timeout); fails on any request or lookup the test did not allow | A04, A15, A17, A18 |
| `faultserver` | A real HTTP and SSE server with upstream's behaviour list; cursors per key function, so concurrent callers do not share one script | A04 |
| `clock` | `clock.Run(t, fn)` runs `fn` in a `synctest` bubble; `Advance(d)`; `Until(date)` | timers, retries, schedules |
| `golden` | `golden.Equal(t, name, got)`, `-update`, refusal under `CI`, `txtar` for trees; HTML query helpers | prompts, schemas, fragments |
| `conform` | `ToolCancels(t, decl)`: a tool body returns in time when its context ends (A03); `Leaf(t, pkg)`: the import rule | A03, this area |
| `fixture` | Normalisation tokens, the scenario manifest type, workspace tree comparison, `Verify(export)`: every request rebuilt from the log equals what was sent | replay, A01, A14 |
| `dshbin` | Builds `dsh` once per run with coverage and the race detector; `Run` with a private home, capped output, a deadline, and the whole process tree ended; builds fixture programs the same way | replay, scenarios, A15, A22, A12 |
| `scenario` | `scenario.Run(t, id, Needs{...}, body)`; the evidence report; the required list | every `DH-E` scenario |

What each seam owner must provide beside its contract, and what the kit expects of it:

| Seam (owner) | Fake and suite | Must provide |
|---|---|---|
| Session storage (A05) | an in-memory store that passes the storage suite | fault injection per contract clause; the export writer |
| Filesystem (A08) | `fstest` and the in-memory provider, already specified | nothing more |
| Process starter and terminal (A09) | a fake starter and terminal | scripted exit, output and tree behaviour; it never starts a process |
| Sandbox stage and backend (A10) | a fake stage | every status record and truncation |
| Answerer, secret store, keychain (A17) | fakes for each | allow, deny and never answer; a record of what was asked |
| MCP and language servers (A15), plugin process (A22), external runtimes (A12), office engine (A16) | fixture programs: real child processes built by `dshbin` from Go sources under `testkit/fixtures` | the behaviours their owner's tests script |
| JSON-RPC peer (A20) | the fake peer in module `src/sdk` | nothing more |
| Browser and computer providers (A16) | `browsertest` and a fake provider | nothing more |

**Replay.** A scenario is a directory under `test/replay/testdata/<name>/`:

| File | Content |
|---|---|
| `scenario.json` | Typed manifest: profile, surface driver (`headless`, `sdk`, `acp`, `http`), input, recording (`live` or `authored`), needs (operating system, shell), permission mode |
| `session.jsonl`, `session.<n>.jsonl` | The product's own export format ([DH-ADR-PERSISTENCE-12](../upstream/session-and-persistence.md#dh-adr-persistence-12)), normalised; children numbered in lineage order |
| `overlay.yaml` | An optional config layer for the run |
| `workspace/`, `workspace.expected/` | The seed, and the independent oracle that record and refresh never rewrite |
| `system-prompt.golden`, `tool-schemas.golden` | Each distinct text stored once |
| `override.json` | Only for what a log cannot hold: a hang, or an authored script |

The component `llm.replay` (`internal/llm/replay`) listens on `llm.StreamAround` and answers `Serve(stream, "replay")` from the script derived from `assistant/attempt` events; a failed attempt is an event too, so a failure before the first chunk needs no sidecar. For a recorded route that no loaded provider registers, it registers a stub adapter built from the recorded header, so binding a call succeeds without a key. A live child session is bound to its script by its lineage (parent script and the ordinal of the creating tool call), never by arrival order. Before serving, the component compares the normalised live request with the recorded one and fails with `replay/request-mismatch` and a diff. The runner in `test/replay` starts the built binary, drives it through one surface, harvests with the export command, normalises and compares. Modes: `replay` (default, writes nothing), `record` (needs a key; writes to a temporary directory and renames after the comparison), `refresh` (plays recorded replies and rewrites expected logs only).

### 6.2 Contracts

- **Events.** None declared. Replay uses area A04's `llm.StreamAround` (form: around). A watch reports through its callback, not an event.
- **Config.**

| Go struct | Fields and defaults | Replaces |
|---|---|---|
| `egress.Config` | `ExtraCACerts []string` (none); read at launch, not live | new ([DH-ADR-UNDICI-3](../libs/undici.md#dh-adr-undici-3)) |
| `watch.Config` | `MaxWatches` 4,096; `MaxDirEntries` 2,000; `PollFile` 250 ms; `PollDir` 1 s; `VerifyEvery` 5 s | per-consumer polling switches |
| `replay.Config` | `Scenario` (directory, required); `Mode` (`replay`); `Pace` (0: burst) | `UP-CFG-dsh-llm-replay`: the three file paths become one directory, the provider catalog comes from the recorded headers, and no environment variable supplies a default |

- **Persisted data.** None. The fingerprint inventory file belongs to area A05. Fixtures are test data in the export format.
- **Tools exposed to the model.** None.
- **Errors.** `egress/no-permit`, `egress/refused-proxy-value`, `egress/ca-unreadable`; `watch/limit`; `replay/unscripted`, `replay/unconsumed`, `replay/request-mismatch`, `replay/unbound-child`; `scenario/required-not-run`.
- **Static checks** (own analyzers under [DH-ADR-TOOLCHAIN-8](../upstream/toolchain.md#dh-adr-toolchain-8), no baseline): an HTTP client, transport, dialler or resolver built outside `egress`; `time.Sleep` outside the sleep helper; `mime.TypeByExtension` outside `mediatype`; a `util` package importing another harness family; a product package importing `internal/testkit`; a scenario package importing a fake or calling `t.Skip` directly; a function marked `//dsh:boundary` that no fuzz target calls.

### 6.3 Ownership, failure, retry, rollback and cleanup

| Resource | Owner (releases it) | Access | On failure |
|---|---|---|---|
| The egress service, its transports and resolver | The root fiber | Any component may ask for a client | A bad proxy value is a diagnostic and that scheme is direct; an unreadable certificate file stops the launch. Nothing reloads: a new proxy needs a restart |
| One client's idle connections | The fiber that asked | Only that component | Unload closes idle connections and lets a request in flight finish. A cancelled request returns at once; **uncertain:** whether the remote side acted |
| A sharing client | The sender's fiber; the permit belongs to A18 | Only the permit holder | A withdrawn permit cancels the request in flight; nothing is retried |
| A watch | The fiber that asked; the notifier and timers belong to the `watch` fiber | The caller may close only its own watch | A lost path moves the watch to its nearest ancestor; a notifier failure degrades to polling and calls back once with the reason. `Close` returns after the callback can no longer run |
| A schema | Nobody: an immutable value cached for the process. A run-time schema belongs to the plugin's registration | Any reader | A derivation error fails the registration and names type and field; nothing is retried |
| A test's home, workspace, listener, child process tree, coverage directory | The kit helper that created it, through `t.Cleanup` | The product under test uses them and never deletes them | A timed-out child's tree is ended and its output kept as an artefact |
| A fixture directory | The repository | Replay reads only | A failed record leaves the committed fixture untouched; spent quota is not undone |
| The evidence report | The run that wrote it | The gate reads it | A scenario that crashes the test binary leaves no row, and the checker fails a required one that has no row |

A cleanup that fails is reported with the test's name and does not stop the remaining cleanups. If recovery of a real environment fails, the scenario retains what its Cleanup clause names.

## 7. Decisions

<a id="dh-adr-util-1"></a>
### DH-ADR-UTIL-1 `schema` lives at `src/harness/internal/schema`, and it is the only importer of its module
- **Decision.** The package path is `internal/schema`. [../libs/zod.md](../libs/zod.md) §6 wrote `src/harness/schema`; [../design.md §4.2](../design.md#42-go-module-layout) and CF-13 put every implementation package under `internal/`. The design document is followed.
- **Why.** Every consumer is inside module `src/harness`, including the protocol-schema generator ([sdk-acp-typert.md](sdk-acp-typert.md) §6.1). Plugin authors and other languages receive JSON Schema files, not a Go package. The coverage floor applies to `internal/` ([DH-ADR-14](../design.md#dh-adr-14)).
- **Tradeoff.** A Go program outside the module cannot derive harness schemas. A public package for plugin authors stays a separate, later decision.
- **Serves.** [DH-O-2](../design.md#dh-o-2), [DH-O-6](../design.md#dh-o-6). **Verified by.** `DH-T-UTIL-1`, `DH-T-UTIL-11`.

<a id="dh-adr-util-2"></a>
### DH-ADR-UTIL-2 The `util` family is a leaf, and a helper needs two consumers to enter it
- **Decision.** A package under `internal/util` imports the standard library, the cordis module, `internal/schema` and other `util` packages, and nothing else in the harness. A helper is added only when two areas need the same thing; otherwise it stays with its one user. `herr` and `ident` import the standard library only.
- **Why.** Upstream keeps its error base and its utilities dependency-free so that any package may use them without a cycle (`error/README.md:54`). The same rule is what lets `egress` and `watch` be injected everywhere.
- **Tradeoff.** `egress` cannot name area A18's permit type, so it accepts a one-method interface that the permit satisfies.
- **Serves.** [DH-O-1](../design.md#dh-o-1). **Verified by.** `DH-T-UTIL-11`.

<a id="dh-adr-util-3"></a>
### DH-ADR-UTIL-3 Each upstream utility gets one of four placements, and merged ones name their owner
- **Decision.** The last column of [§3.1](#31-packages): the standard library covers it; it is a small harness package here; it merges into an owner area's package; or it is gone. The merges are: file replacement and locks to `fsx` (A08); the home layout to A07; the launch environment to `launch.Env` (A21); run-and-capture to the process package (A09); bundle lists to embedded bundles and the optional list (A27, A21); the manifest type and the detail-document order to A22; workspace display and file addresses to A07 and A19. The market index is dropped by [DH-ADR-13](../design.md#dh-adr-13). Revealing a file and listing the applications that can open it are not built: the web UI has no native shell integration ([DH-ADR-18](../design.md#dh-adr-18); [app-web.md](app-web.md) reaches the same verdict).
- **Why.** A shared package with one consumer is indirection; a behaviour with no named owner is lost.
- **Tradeoff.** Five areas receive something. Each is listed under [§10](#10-open-questions-and-limitations) so that the receiving specification can confirm it.
- **Serves.** [DH-O-16](../design.md#dh-o-16), [DH-O-14](../design.md#dh-o-14). **Verified by.** The ledger rows of the 24 packages; `DH-T-UTIL-11`.

<a id="dh-adr-util-4"></a>
### DH-ADR-UTIL-4 The language replaces helper packages: distinct types, bounded queues, context deadlines, plain values
- **Decision.** (a) An identifier that could be confused with another is a defined Go type declared by its owner (`type SessionID string`), with its own decoding where it has a grammar. This replaces every upstream branded type; no helper exists. (b) An in-process queue is a bounded channel or a bounded buffer inside one owner, and its declaration states what happens when it is full. (c) A deadline is `context.WithTimeoutCause`; "no deadline" is the absence of one, never a zero or negative number; a configured timeout is a `time.Duration` with schema bounds. Termination stays with the capability. (d) Data is compared and copied as typed values; nothing is frozen and nothing is loaded lazily. Scope boundary of this record (unit `util.timeout.4`): `egress.IdleBody` bounds silence on a response body, not its total length, because a model's streamed answer may legitimately run for many minutes while bytes keep arriving; a caller that needs a total limit sets its own context deadline, which (c) makes the one way to state one.
- **Why.** `brand`, `deque`, `chunked-list`, `timeout`, `lazy-require` and most of `values` exist because TypeScript types are structural and erased, arrays shift in linear time, and objects are shared by reference.
- **Tradeoff.** A defined type needs an explicit conversion at each boundary. Cancellation stays cooperative: a body that ignores its context is not stopped.
- **Serves.** [DH-O-3](../design.md#dh-o-3), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-ZOD-7`, `DH-T-UTIL-12`, `DH-T-NODETIMERS-1`.

<a id="dh-adr-util-5"></a>
### DH-ADR-UTIL-5 `egress` is a service that adds one header, asks for a permit, and builds child environments by audience
- **Decision.** As [§6.1](#61-shape). It extends [DH-ADR-UNDICI-1](../libs/undici.md#dh-adr-undici-1) to `-3` with: an owning context per client; a permit for any sharing purpose; `User-Agent: dsh` as the only thing added to a request; a resolver owned by the service; and `ChildEnv(ModelRun)`, which omits a proxy value that carries a password and reports the omission once.
- **Why.** [DH-ADR-9](../design.md#dh-adr-9) needs a point where "this client sends data off the machine" is declared; the client factory is the one place every sender passes. Upstream withholds proxy settings from model-authored programs because the URL may hold credentials (`http-proxy/README.md:114`); with [DH-ADR-8](../design.md#dh-adr-8) such a program is an ordinary command, so the rule is restated for that command.
- **Tradeoff.** A model-run command behind an authenticating proxy has no proxy and must be given one by the operator. A third-party module that cannot take an `http.Client` cannot be used.
- **The import-graph gate (debt entry U-K2-3).** A static check over this repository's source cannot see a client built inside a dependency, and upstream's reason for stopping there ("a source analyzer cannot follow calls into third-party modules") is not true in Go, where every dependency is source in the build. `DH-T-UTIL-22` therefore lists every package in the `dsh` binary's import graph, dependencies included, that imports a package able to open a connection (`net`, `net/http`, `crypto/tls`, `golang.org/x/net/...` and the like) and compares the list with a reviewed table kept beside `egress`, in which each entry says how that package's client is supplied by `egress` or why it never dials. A dependency update that adds such a package fails the gate until someone reviews it. The gate proves which packages *can* dial, not that they do, so the recording transport of `egresstest` stays. The table is a list of reviewed facts, not a baseline of tolerated violations ([DH-ADR-TOOLCHAIN-8](../upstream/toolchain.md#dh-adr-toolchain-8)): an entry without a stated supply path fails the same test.
- **Serves.** [DH-O-7](../design.md#dh-o-7), [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-UTIL-3` to `DH-T-UTIL-5`, `DH-T-UTIL-22`, [DH-E-UTIL-1](#dh-e-util-1).

<a id="dh-adr-util-6"></a>
### DH-ADR-UTIL-6 Fakes exist only at seams, pass the seam's own suite, and fail on anything unscripted
- **Decision.** A test may replace the model, the network, the clock, a child process, the filesystem provider, the answerer and the secret store. Everything else is the real component, composed by the real loader. A fake lives beside its contract and runs the same conformance suite as the real provider. A fake fails the test on a call it has no script for. There is no stub of a spine internal such as the inbox.
- **Why.** It is upstream's own rule, "mock only the expensive or non-deterministic boundary" ([../upstream/toolchain.md](../upstream/toolchain.md) §3), made structural. Upstream's inbox stubs and Remote mock each carry a list of ways they differ from production.
- **Tradeoff.** A consumer test builds a real agent on an in-memory store, which is more set-up than a stub; `compose` hides it. Seam owners carry a fake and a suite.
- **Serves.** [DH-O-15](../design.md#dh-o-15), [DH-O-1](../design.md#dh-o-1). **Verified by.** `DH-T-UTIL-13`, `DH-T-UTIL-14`.

<a id="dh-adr-util-7"></a>
### DH-ADR-UTIL-7 A fixture is a session export; replay is a stream listener bound by lineage that checks the request
- **Decision.** As [§6.1](#61-shape). The fixture format is the export format, so there is no second log format to keep. The replay component is a `llm.StreamAround` listener, as [llm.md](llm.md) §6.2 writes, plus a stub registration so that a route with no provider can still be bound, as [agent-loop.md](agent-loop.md) requires. Children bind by lineage. A request that differs from the recorded one fails the run.
- **Why.** Upstream binds by first-call order and marks concurrent children as unsolved (`llm-replay/src/index.ts:835`). Upstream replay answers whatever request arrives; comparing the request turns every scenario into a check of [DH-O-4](../design.md#dh-o-4).
- **Tradeoff.** A wording change in a prompt fails every scenario until `refresh` is run and its diff reviewed. No upstream fixture is reusable ([DH-ADR-2](../design.md#dh-adr-2)). A hang still needs an authored override.
- **Serves.** [DH-O-15](../design.md#dh-o-15), [DH-O-4](../design.md#dh-o-4). **Verified by.** `DH-T-UTIL-16` to `DH-T-UTIL-18`, `DH-T-TOOLCHAIN-5`, [DH-E-TOOLCHAIN-1](../upstream/toolchain.md#dh-e-toolchain-1).

<a id="dh-adr-util-8"></a>
### DH-ADR-UTIL-8 A scenario is declared through one helper, every outcome is a report row, and only a pass counts
- **Decision.** Every real-environment scenario is a Go test behind the `scenario` build tag that calls `scenario.Run(t, id, needs, body)` with its `DH-E` ID. The helper checks the needs (environment variables, programs, operating system). Unmet needs give `skipped` with the reason, or a failure when the ID is on `DSH_REQUIRE_SCENARIOS`. Each outcome is appended to the evidence report: ID, result (`passed`, `failed`, `skipped`), reason, build identity of the binary under test, operating system, duration. A checker fails when a required ID has no `passed` row, which also catches a scenario that never ran. A scenario package may not import a fake or call `t.Skip` itself.
- **Why.** [DH-ADR-TOOLCHAIN-6](../upstream/toolchain.md#dh-adr-toolchain-6) states the rule; this is its mechanism. `go test` prints a skip as `ok`, so the report, not the exit code, is the record.
- **Tradeoff.** One more file per run to keep. The required list is maintained by hand in the acceptance workflow.
- **Serves.** [DH-O-15](../design.md#dh-o-15), [DH-O-16](../design.md#dh-o-16). **Verified by.** `DH-T-UTIL-19`, `DH-T-TOOLCHAIN-6`, [DH-E-UTIL-2](#dh-e-util-2).

<a id="dh-adr-util-9"></a>
### DH-ADR-UTIL-9 The small helpers stay narrow on purpose
- **Decision.** Recommended default, not confirmed by the owner; it records as scope what [DH-D-UTIL-5](#dh-d-util-5) already builds. (a) `mediatype` decides a file's language and an export's file name from the name alone, never from content. (b) `homedir` expands only a leading `~` and `~/`; `~user`, environment variables and globs in a path are left as written. (c) `textsearch`, the pick-list filter, matches the typed characters in order and tolerates no typing error. (d) The file-reference ranker is a separate package with its own rules and is not folded into `textsearch`.
- **Why.** Each rule keeps one property. (a) The same file renders the same way on every machine, and nothing reads an untrusted file to label it. (b) A path is never evaluated outside the expression language of [boot.md](boot.md) (`DH-ADR-BOOT-5`), so a configuration value cannot run a lookup the dump does not show. (c) A person can see why a row matched. (d) Ranking files by path segments and ranking menu rows by typed order are different problems, and one function would serve both badly.
- **Tradeoff.** A file with a misleading name is labelled wrongly; `~alice/x` is not expanded; a typing error finds nothing; two matchers exist. These are the upstream limits `L:util.code-language.2`, `L:util.file-drag.1`, `L:util.home-paths.1`, `L:util.text-search.1` and `L:util.text-search.3`, kept as boundaries. Each would be reopened by a user-visible need, not by convenience.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-16](../design.md#dh-o-16). **Verified by.** `DH-T-UTIL-8` to `DH-T-UTIL-11`.

<a id="dh-adr-util-10"></a>
### DH-ADR-UTIL-10 The operating system's static proxy setting is read once at launch, below every environment variable; a PAC address is reported, never evaluated
- **Decision.** Debt entry U-K2-2, resolved as [DH-ADR-40](../design.md#dh-adr-40) directs (the recommended option). `util/sysproxy` has one function, `Read`, and one implementation per operating system. Windows: the static proxy values of the user's Internet Settings (enable flag, server list, bypass list, auto-configuration address), read from the registry by system calls. macOS: the output of the system program `scutil --proxy`, started through area A09's process package with a fixed argument list, no shell and a two-second bound. Every other system: nothing. Boot calls `Read` in step 1 and freezes the result with the launch facts as the source `system`, which supplies proxy names only and has the lowest precedence ([boot.md](boot.md), `DH-ADR-BOOT-4`); `egress.Resolve` sees it through the same lookup as the other sources, so validation, the bypass list and the password rule of `ChildEnv` apply unchanged. An auto-configuration (PAC) address is never fetched or evaluated: the launch prints "a proxy auto-configuration address is set in the system settings and is not supported; export HTTPS_PROXY" and continues without a system proxy.
- **Why.** On a network whose proxy is set only in the system settings every model request fails to connect until the user discovers the variable. The static setting is the common case; PAC needs a script engine, which the design does not have ([DH-ADR-8](../design.md#dh-adr-8)).
- **Failure and the pure-Go rule.** The reader is best effort and can only add a value: a read that fails, times out, returns something that does not validate, or does not exist on a platform yields no setting and one diagnostic line, and never stops a launch. The design therefore holds whichever way the unverified fact turns out ([§10](#10-open-questions-and-limitations), item 3): if a platform's reader could only be built with cgo, that reader is not built ([DH-ADR-15](../design.md#dh-adr-15)), the platform behaves as "every other system", and the diagnostic says that system proxy settings are not read there.
- **Tradeoff.** A setting changed after launch is not followed until restart, like every launch fact. One child process at every macOS launch. A PAC user still has to export the variables, but is now told so.
- **Serves.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Verified by.** `DH-T-UTIL-21`, [DH-E-UTIL-4](#dh-e-util-4).

## 8. Deliverables

<a id="dh-d-util-1"></a>
### DH-D-UTIL-1 One package describes, checks and fingerprints every data shape
- **Result and why it matters.** A Go type is written once and the model's tool schema, argument checking, config defaults, form decoding and the persisted-type fingerprint all follow from it. Six areas cannot register a tool, a config or an event type without it.
- **How it is implemented.** Reflection over the type and its tags into the module's schema model; a cache keyed by type and options; strict decoding with `encoding/json` onto the defaults, then `Validate()`; the fingerprint as a hash of a canonical form that ignores order and descriptions. The model-subset check walks the document and reports every path. `Project` applies a keyword table and reports what it removed.
- **Components and files.** `internal/schema`. **Depends on.** `DH-LIB-ZOD`, [DH-D-CORDIS-8](../libs/cordis.md#dh-d-cordis-8) (the port's strict config decoding, CF-1 row 8). Runtime: none.
- **Priority.** P0. **Outcome.** [DH-O-2](../design.md#dh-o-2), [DH-O-5](../design.md#dh-o-5), [DH-O-6](../design.md#dh-o-6). **Done when.** `DH-T-ZOD-1`, `DH-T-ZOD-2`, `DH-T-ZOD-3`, `DH-T-ZOD-4`, `DH-T-ZOD-5`, `DH-T-ZOD-6`, `DH-T-ZOD-7`, `DH-T-ZOD-8`, `DH-T-ZOD-9`, `DH-T-ZOD-10`, `DH-T-UTIL-1` and `DH-T-UTIL-2` pass.

<a id="dh-d-util-2"></a>
### DH-D-UTIL-2 Every outbound connection follows one policy, and data leaves only with a permit
- **Result and why it matters.** An operator sets a proxy and a certificate authority once and all traffic follows; a user who opted into nothing sends nothing beyond the requests the product needs.
- **How it is implemented.** `egress.Resolve` reads proxy names through the lookup boot gives it, validates proxy values and certificate files, then builds transports on `net/http` with the matcher of `DH-LIB-HTTPPROXY`. Clients with equal options share a transport. The guard runs in the dialler's control hook. The identity header and the permit check are a `RoundTripper` around the transport. `egress.Wrap(owner, func(purpose Purpose, next http.RoundTripper) http.RoundTripper)` registers one further transport wrapper as an effect of its owner; it is applied inside the identity header to every client `egress` builds and is told each client's purpose (area X3's diagnostics capture is its one user). A second registration is refused.
- **Components and files.** `util/egress`, `testkit/egresstest`. **Depends on.** `DH-LIB-UNDICI`, `DH-LIB-HTTPPROXY`, [DH-D-CORDIS-2](../libs/cordis.md#dh-d-cordis-2), [DH-D-HOST-4](host-telemetry.md#dh-d-host-4) (the permit), [DH-D-BOOT-2](boot.md#dh-d-boot-2) (`launch.Env` and its view limited to the process and home-file sources, which already refuses a proxy name in a project file). Runtime: none.
- **Priority.** P0. **Outcome.** [DH-O-7](../design.md#dh-o-7), [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-UNDICI-1`, `DH-T-UNDICI-2`, `DH-T-UNDICI-3`, `DH-T-UNDICI-4`, `DH-T-UNDICI-5`, `DH-T-UNDICI-6`, `DH-T-UNDICI-7`, `DH-T-UNDICI-8`, `DH-T-UNDICI-9`, `DH-T-UTIL-3`, `DH-T-UTIL-4` and `DH-T-UTIL-5` pass and [DH-E-UTIL-1](#dh-e-util-1) has passed. The system proxy source and the import-graph gate are [DH-D-UTIL-9](#dh-d-util-9) and [DH-D-UTIL-10](#dh-d-util-10).

<a id="dh-d-util-9"></a>
### DH-D-UTIL-9 A proxy set only in the operating system's settings is used
- **Result and why it matters.** On a network whose proxy is set in the Windows or macOS settings, model requests connect without the user exporting a variable, and a proxy auto-configuration address is named instead of failing silently (debt entry U-K2-2, [DH-ADR-UTIL-10](#dh-adr-util-10)).
- **How it is implemented.** `util/sysproxy` with one reader per operating system, called by boot in step 1 and frozen as the launch source `system`, which supplies proxy names only and has the lowest precedence; `egress.Resolve` sees it through the same lookup. Best effort: a failed, slow or missing reader yields nothing and one diagnostic line.
- **Components and files.** `util/sysproxy`; the `system` source of `launch.Env` in `internal/boot/launch`. **Depends on.** [DH-D-UTIL-2](#dh-d-util-2), [DH-D-BOOT-2](boot.md#dh-d-boot-2), `DH-LIB-KOFFI` (`golang.org/x/sys`, system calls without cgo), [DH-D-EXEC-1](execution.md#dh-d-exec-1) (the one package that starts `scutil`). Runtime: on macOS the system program `scutil`.
- **Priority.** P1. **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Done when.** `DH-T-UTIL-21` passes and [DH-E-UTIL-4](#dh-e-util-4) has passed.

<a id="dh-d-util-10"></a>
### DH-D-UTIL-10 Every package in the binary that can open a connection is accounted for
- **Result and why it matters.** A dependency update that brings a package able to dial past `egress` is stopped for review before release (debt entry U-K2-3, [DH-ADR-UTIL-5](#dh-adr-util-5)).
- **How it is implemented.** A Go test lists the `dsh` binary's import graph, dependencies included, finds every package that imports a dial-capable package, and compares the list with a reviewed table kept beside `egress`, each entry naming the supply path of its client or why it never dials.
- **Components and files.** The test and the table under `util/egress`. **Depends on.** [DH-D-UTIL-2](#dh-d-util-2).
- **Priority.** P1. **Outcome.** [DH-O-14](../design.md#dh-o-14), [DH-O-7](../design.md#dh-o-7). **Done when.** `DH-T-UTIL-22` passes.

<a id="dh-d-util-3"></a>
### DH-D-UTIL-3 A component is told when a file or directory changed, on every filesystem
- **Result and why it matters.** Credentials, skills, instruction files and the file tree refresh without a restart, including on network drives, and no consumer owns watcher code.
- **How it is implemented.** [../libs/chokidar.md](../libs/chokidar.md) §6: notifications on the parent directory as hints, a comparison of identity, size and time before each callback, a readiness reconcile, and stat polling as the fallback. Path identity comes from `fsx` (A08).
- **Components and files.** `util/watch`, `util/watch/watchtest`. **Depends on.** `DH-LIB-CHOKIDAR`, [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1), [DH-D-FS-1](filesystem.md#dh-d-fs-1) (`fsx.ID` and the filesystem-type probe).
- **Priority.** P1. **Outcome.** [DH-O-1](../design.md#dh-o-1), [DH-O-2](../design.md#dh-o-2). **Done when.** `DH-T-CHOKIDAR-1`, `DH-T-CHOKIDAR-2`, `DH-T-CHOKIDAR-3`, `DH-T-CHOKIDAR-4`, `DH-T-CHOKIDAR-5`, `DH-T-CHOKIDAR-6` and `DH-T-UTIL-6` pass and `DH-E-CHOKIDAR-1` and `DH-E-CHOKIDAR-2` have passed.

<a id="dh-d-util-4"></a>
### DH-D-UTIL-4 Globs and versions each have one meaning
- **Result and why it matters.** A pattern in a config file and a version range in a plugin manifest are read by one parser each, and a malformed one is refused where it is read.
- **How it is implemented.** `glob` and `version` wrap their modules as the library specifications state.
- **Components and files.** `util/glob`, `util/version`. **Depends on.** `DH-LIB-PICOMATCH`, `DH-LIB-SEMVER`.
- **Priority.** P0. **Outcome.** [DH-O-2](../design.md#dh-o-2). **Done when.** `DH-T-PICOMATCH-1`, `DH-T-PICOMATCH-2`, `DH-T-PICOMATCH-3`, `DH-T-SEMVER-1`, `DH-T-SEMVER-2` and `DH-T-SEMVER-3` pass.

<a id="dh-d-util-11"></a>
### DH-D-UTIL-11 Instants and recurrence rules each have one meaning
- **Result and why it matters.** A schedule rule and a time in a zone are read by one parser each, resolve the same way across clock changes, and work on a machine with no zone database. It was the P1 part of [DH-D-UTIL-4](#dh-d-util-4).
- **How it is implemented.** `caltime` adds strict parsers and the gap and overlap resolver to `time`. `recur` lists candidate wall times period by period, converts each with that resolver, and answers `Next` and `Resolve` by bounded search; the cron evaluator is ported on the same resolver with the hour rule of [§6.1](#61-shape).
- **Components and files.** `util/caltime`, `util/recur`. **Depends on.** `DH-LIB-TEMPORAL`, `DH-LIB-RRULE`. Runtime: zone data, embedded in the binary ([DH-ADR-TEMPORAL-1](../libs/js-temporal-polyfill.md#dh-adr-temporal-1)).
- **Priority.** P1. **Outcome.** [DH-O-9](../design.md#dh-o-9). **Done when.** `DH-T-TEMPORAL-1`, `DH-T-TEMPORAL-2`, `DH-T-TEMPORAL-3`, `DH-T-TEMPORAL-4`, `DH-T-TEMPORAL-5`, `DH-T-RRULE-1`, `DH-T-RRULE-2`, `DH-T-RRULE-3`, `DH-T-RRULE-4`, `DH-T-RRULE-5`, `DH-T-RRULE-6` and `DH-T-UTIL-7` pass and [DH-E-UTIL-3](#dh-e-util-3) has passed.

<a id="dh-d-util-5"></a>
### DH-D-UTIL-5 Small leaf packages give errors, identifiers, the home location, media types and bounded output one form
- **Result and why it matters.** A failure carries the same code in a tool result, a log line and a wire error; every command agrees where the home is; a tool result is cut the same way everywhere; a configuration error shows its source line.
- **How it is implemented.** Five packages of the leaf table in [§6.1](#61-shape). `herr` is an error type found with `errors.As`. `homedir` is one pure function over `os.UserHomeDir` and a lookup. `mediatype` is compiled-in data read in both directions. `retain` keeps a head buffer and a tail ring and counts what passed between. `herr` also carries the plain-text source excerpt of [../libs/babelcode-frame.md](../libs/babelcode-frame.md) (`DH-LIB-CODEFRAME`): the lines around a `file:line:column` position with a caret, which configuration and schema diagnostics print; `DH-T-CODEFRAME-1` is its completion test. `textsearch` and `jsonscan` are [DH-D-UTIL-12](#dh-d-util-12); `sysproxy` is [DH-D-UTIL-9](#dh-d-util-9).
- **Components and files.** `util/herr`, `ident`, `homedir`, `mediatype`, `retain`. **Depends on.** `DH-LIB-MIME`, `DH-LIB-NODECRYPTO`, `DH-LIB-CODEFRAME`. Runtime: none.
- **Priority.** P0. **Outcome.** [DH-O-6](../design.md#dh-o-6), [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-MIME-1`, `DH-T-MIME-2`, `DH-T-NODECRYPTO-3`, `DH-T-CODEFRAME-1`, `DH-T-UTIL-8`, `DH-T-UTIL-9` and `DH-T-UTIL-11` pass.

<a id="dh-d-util-12"></a>
### DH-D-UTIL-12 Pick lists filter one way, and a tool card reads arguments while they arrive
- **Result and why it matters.** Every menu and completion list matches typed text by the same rule, and a tool card can show a path while the model is still writing the call's arguments. It was the P1 part of [DH-D-UTIL-5](#dh-d-util-5).
- **How it is implemented.** `textsearch` matches the typed characters in order with the scoring of `DH-LIB-FUZZYSORT` ([DH-ADR-UTIL-9](#dh-adr-util-9)). `jsonscan` tracks quotes and nesting across fragments and remembers key offsets, decoding only requested fields.
- **Components and files.** `util/textsearch`, `util/jsonscan`. **Depends on.** `DH-LIB-FUZZYSORT`. Runtime: none.
- **Priority.** P1. **Outcome.** [DH-O-12](../design.md#dh-o-12), [DH-O-14](../design.md#dh-o-14). **Done when.** `DH-T-FUZZYSORT-1`, `DH-T-FUZZYSORT-2`, `DH-T-FUZZYSORT-3` and `DH-T-UTIL-10` pass.

<a id="dh-d-util-6"></a>
### DH-D-UTIL-6 Any area can test real components against scripted boundaries, in virtual time, and leave nothing behind
- **Result and why it matters.** A composition test is a few lines, uses the production loader, and fails if a fiber leaks an effect. This is the unit, composition and golden evidence of [DH-O-15](../design.md#dh-o-15).
- **How it is implemented.** The `testkit` packages of [§6.1](#61-shape) except `fixture`, `dshbin` and `scenario`. `compose` builds layers in memory, calls the same composition function as `dsh`, and registers the scripted model and the recording egress as ordinary entries. Leak detection is the tree's own effect list after unload plus the bubble's rule that every goroutine has ended.
- **Components and files.** `internal/testkit/{compose,model,egresstest,faultserver,clock,golden,conform}`. **Depends on.** [DH-D-UTIL-1](#dh-d-util-1), [DH-D-UTIL-2](#dh-d-util-2), [DH-D-CORDIS-1](../libs/cordis.md#dh-d-cordis-1), `DH-LIB-GOLDEN`, `DH-LIB-DOMTEST`, and the contracts its fakes stand in for: [DH-D-LLM-1](llm.md#dh-d-llm-1) (the adapter interface), [DH-D-STORE-1](session-store.md#dh-d-store-1) (the in-memory store), [DH-D-HUMAN-1](interaction-credentials.md#dh-d-human-1) (the answerer), [DH-D-BOOT-3](boot.md#dh-d-boot-3) (the composition function). Runtime: a Go version with `testing/synctest`.
- **Priority.** P0. **Outcome.** [DH-O-15](../design.md#dh-o-15), [DH-O-1](../design.md#dh-o-1). **Done when.** `DH-T-UTIL-12`, `DH-T-UTIL-13`, `DH-T-UTIL-14`, `DH-T-UTIL-15`, `DH-T-TOOLCHAIN-2` and `DH-T-TOOLCHAIN-4` pass.

<a id="dh-d-util-7"></a>
### DH-D-UTIL-7 A recorded session replays against the built binary without a key and proves the transcript and the files
- **Result and why it matters.** The assembled product is shown to produce the committed log, the committed workspace and the same model requests, on every pull request, with no credential.
- **How it is implemented.** The replay component, the fixture helpers, the binary builder and runner, and the runner package with one driver per surface. Normalisation is a fixed table of typed tokens applied after export; a committed fixture must be unchanged by it.
- **Components and files.** `internal/llm/replay`, `internal/testkit/{fixture,dshbin}`, `test/replay`. **Depends on.** [DH-D-UTIL-6](#dh-d-util-6), [DH-D-LLM-1](llm.md#dh-d-llm-1) (`llm.StreamAround` and stub registration), [DH-D-STORE-7](session-store.md#dh-d-store-7) (the export command) and [DH-D-SESSION-4](core-session.md#dh-d-session-4) (`surface.Rebuild`), [DH-D-SUBAGENT-2](subagent-team.md#dh-d-subagent-2) (a child's header names its creator, and the root's `subagent/started` event names the creating tool call, which is where replay reads the lineage), [DH-D-EXEC-2](execution.md#dh-d-exec-2) (ending a process tree). Runtime prerequisite: a built `dsh`; a model key only for `record`.
- **Priority.** P1. **Outcome.** [DH-O-15](../design.md#dh-o-15), [DH-O-4](../design.md#dh-o-4). **Done when.** `DH-T-UTIL-16`, `DH-T-UTIL-17`, `DH-T-UTIL-18` and `DH-T-TOOLCHAIN-5` pass and [DH-E-TOOLCHAIN-1](../upstream/toolchain.md#dh-e-toolchain-1) has passed.

<a id="dh-d-util-8"></a>
### DH-D-UTIL-8 A run states which evidence it holds: scenarios are counted only when they passed, boundaries only when fuzzed
- **Result and why it matters.** A reviewer reads one report and knows which scenarios ran against what build, and no parser of untrusted bytes ships without a fuzz target.
- **How it is implemented.** The `scenario` helper and its checker; the analyzer rules of [§6.2](#62-contracts) added to the one analyzer binary; a seed corpus per fuzz target under `testdata/fuzz`. Boundaries this area marks: `schema.Decode` and `FromJSON`, `glob.Compile`, `version.Parse` and `ParseRange`, `recur.ParseCron` and `ParseRRule`, `caltime` parsers, `jsonscan`, proxy value resolution, fixture parsing.
- **Components and files.** `internal/testkit/scenario`, `test/scenario`, analyzers in the tools module. **Depends on.** [DH-D-UTIL-6](#dh-d-util-6), `DH-LIB-ANALYSIS`, `DH-LIB-PROPTEST`. The toolchain's gate runner ([../upstream/toolchain.md](../upstream/toolchain.md)) consumes this deliverable: it calls the checker last.
- **Priority.** P1. **Outcome.** [DH-O-15](../design.md#dh-o-15), [DH-O-16](../design.md#dh-o-16). **Done when.** `DH-T-UTIL-19`, `DH-T-UTIL-20`, `DH-T-TOOLCHAIN-3` and `DH-T-TOOLCHAIN-6` pass and [DH-E-UTIL-2](#dh-e-util-2) has passed.

## 9. Acceptance

### 9.1 Local tests

Tests declared by library specifications (`DH-T-ZOD`, `-UNDICI`, `-CHOKIDAR`, `-PICOMATCH`, `-SEMVER`, `-MIME`, `-TEMPORAL`, `-RRULE`, `-FUZZYSORT`, `-TOOLCHAIN`) are listed in each deliverable and not repeated. The rows below are what this area adds. A test with a fake is not a scenario.

| Test ID | Proves | What is asserted |
|---|---|---|
| `DH-T-UTIL-1` | DH-D-UTIL-1 | The root is closed by default and `Open` admits unknown keys; `CheckSubset` lists every offending path; `Decode` returns two issues for two errors; no package but `internal/schema` imports the schema module |
| `DH-T-UTIL-2` | DH-D-UTIL-1 | `DecodeForm` refuses an unknown field and a wrong type; `Changed` reports paths and whether all are tagged live; `Project` removes exactly the profile's keywords and lists each loss; a `time.Duration` field round-trips as `"30s"` |
| `DH-T-UTIL-3` | DH-D-UTIL-2 | A client sends `User-Agent: dsh` even when the caller or a listener set another; no cookie is stored; `Handed` lists each live client and drops it when its owner unloads |
| `DH-T-UTIL-4` | DH-D-UTIL-2 | A sharing client without a permit is refused with `egress/no-permit`; ending the permit's context cancels a request in flight and the next request fails before any dial |
| `DH-T-UTIL-5` | DH-D-UTIL-2 | `ChildEnv(Trusted)` keeps the user's values; `ChildEnv(ModelRun)` omits a value with a password and reports it once; `Resolve` sees only what its lookup returns, also when the process environment names another proxy; `IdleBody` fails a stalled read with `ErrIdle` in virtual time |
| `DH-T-UTIL-6` | DH-D-UTIL-3 | A watch closes when its owner's fiber unloads; a second component cannot close it; `watchtest.Manual` passes the same behavioural table as the real service |
| `DH-T-UTIL-7` | DH-D-UTIL-11 | Cron: a fixed-hour rule skips a gap and fires once in an overlap; a wildcard-hour rule fires in both passes of a repeated hour; `Resolve` returns the latest due occurrence and never one before the committed instant, for every rule kind; an impossible date ends in "exhausted" within a bound |
| `DH-T-UTIL-8` | DH-D-UTIL-5 | `herr.ReasonOf` finds the reason through wrapped errors, and a `herr` error satisfies the one-method interface the `src/sdk` codec looks for; a reason not of the form `<domain>/<reason>` fails a declaration test; `herr` and `ident` import only the standard library. `homedir`: explicit beats `DSH_HOME` beats `~/.dsh`; a blank value is unset; `~alice/x` is left alone; `Display` never prints an absolute machine path. `mediatype.Language` knows `Dockerfile`, and every language id is one the highlighter accepts |
| `DH-T-UTIL-9` | DH-D-UTIL-5 | `retain`: head, tail and head-and-tail windows never split a rune; the omitted count is exact; items keep order |
| `DH-T-UTIL-10` | DH-D-UTIL-12 | `jsonscan`: a field split across fragments at every byte position reads the same as the whole text; unrequested values are not decoded; a fuzz target finds no panic |
| `DH-T-UTIL-11` | DH-D-UTIL-5, DH-ADR-UTIL-2 | Import rules: no `util` package imports another harness family; no product package imports `internal/testkit`; each own analyzer rejects a seeded violation and accepts a reasoned exception |
| `DH-T-UTIL-12` | DH-D-UTIL-6 | A full bounded queue behaves as its declaration says; a tool body that ignores cancellation fails `conform.ToolCancels` |
| `DH-T-UTIL-13` | DH-D-UTIL-6 | `compose.Start` loads a shipped bundle through the real loader with a fresh home; after the test the tree has no effect left; a component that leaks a goroutine fails the test |
| `DH-T-UTIL-14` | DH-D-UTIL-6 | The scripted model fails on an unscripted call and on script left over; it passes area A04's adapter conformance table; a blocked script ends when the context is cancelled |
| `DH-T-UTIL-15` | DH-D-UTIL-6 | `faultserver`: each behaviour gives its wire result; two keys advance separate cursors; a scripted refusal at the egress seam is deterministic; `egresstest` fails on an unlisted request or name lookup |
| `DH-T-UTIL-16` | DH-D-UTIL-7 | A committed fixture is a fixed point of normalisation; equal values get equal tokens; a fixture with an unscrubbed path or identity fails its guard |
| `DH-T-UTIL-17` | DH-D-UTIL-7 | With two children started concurrently in either order, each live child receives its own recorded script; a child with no recorded lineage fails with `replay/unbound-child` |
| `DH-T-UTIL-18` | DH-D-UTIL-7 | A request that differs from the recorded one fails with `replay/request-mismatch`; a recorded failed attempt replays as that failure with no sidecar; `fixture.Verify` rebuilds every request from an export and matches it; `Run` ends a child's whole process tree and caps its output |
| `DH-T-UTIL-19` | DH-D-UTIL-8 | Unmet needs give a `skipped` row with the reason; the same scenario on the required list fails; the checker fails when a required ID has no `passed` row or no row at all |
| `DH-T-UTIL-20` | DH-D-UTIL-8 | A function marked as a boundary with no fuzz target fails the analyzer; a committed crasher is run by plain `go test` |
| `DH-T-UTIL-21` | DH-D-UTIL-9, DH-ADR-UTIL-10 | `sysproxy` (U-K2-2) against recorded Windows registry values and recorded `scutil --proxy` output: enabled and disabled settings, per-scheme servers, a SOCKS entry, a bypass list; a PAC address yields no proxy and the named diagnostic, and nothing is fetched; a value that does not validate is dropped with a diagnostic that names the source and not a password; a reader that errs or exceeds its bound yields nothing and the launch continues. Through `launch.Env`: any environment variable or home-file value wins over the `system` source, and the launch diagnostics name each value's source |
| `DH-T-UTIL-22` | DH-D-UTIL-10, DH-ADR-UTIL-5 | The import-graph gate (U-K2-3): every package in the `dsh` binary's import graph that imports a dial-capable package is in the reviewed table with a supply path; a fixture module that adds one fails the gate naming the package; a table entry with no supply path, or for a package no longer in the graph, fails too |

### 9.2 Real-environment scenarios

<a id="dh-e-util-1"></a>
#### DH-E-UTIL-1 All traffic goes through a real TLS-intercepting proxy
- **Proves.** DH-D-UTIL-2.
- **Given.** A Linux and a Windows machine; the built `dsh`; a real forward proxy that re-signs TLS with a private authority and logs each request; `HTTPS_PROXY` exported; the authority's certificate file configured in `ExtraCACerts`; a real model API key supplied at run time; a fresh home with every sharing setting off.
- **When.** (1) Run one headless prompt that calls the model, fetches a public page and starts one MCP server over HTTP. (2) Remove the certificate setting and repeat. (3) Restore it, set `HTTPS_PROXY` to a `pac+http` value and repeat.
- **Then.** In (1) the proxy log shows the model, fetch and MCP requests, each with `User-Agent: dsh`, and a packet capture shows no direct connection and no request to any other host. In (2) every request fails with a certificate error and nothing is sent unverified. In (3) the launch prints a diagnostic that names the variable and not its value, and HTTPS connects directly.
- **Cleanup.** Unset the variables, stop the proxy, delete the temporary home. On failure keep the proxy log and the capture.

<a id="dh-e-util-2"></a>
#### DH-E-UTIL-2 A required scenario that cannot run fails the acceptance job
- **Proves.** DH-D-UTIL-8.
- **Given.** The repository's credentialed workflow on a hosted runner; `DSH_REQUIRE_SCENARIOS` lists [DH-E-UTIL-1](#dh-e-util-1) and one scenario of another area; the model key secret deliberately absent in one run and present in a second.
- **When.** The workflow runs the scenario packages and then the checker, once per run.
- **Then.** Without the key the job fails, the report has a `failed` row for the required scenario whose reason names the missing variable, and no row says `passed`. With the key the job passes only if both rows say `passed` and carry the commit of the binary under test.
- **Cleanup.** Reports expire with the workflow run. Nothing is changed outside the runner.

<a id="dh-e-util-3"></a>
#### DH-E-UTIL-3 Zone rules resolve on a machine with no zone database
- **Proves.** DH-D-UTIL-11.
- **Given.** A clean Windows machine with no Go installation and no `ZONEINFO` variable; the released `dsh` archive for that platform.
- **When.** Create a schedule with the cron rule `30 2 * * *` in `America/New_York` and ask for its next occurrences across the next spring and autumn clock changes.
- **Then.** The rule is accepted; the spring date has no occurrence, the autumn date has one at the earlier instant; a diagnostic names the embedded zone data as the source.
- **Cleanup.** Delete the schedule and the temporary home.

<a id="dh-e-util-4"></a>
#### DH-E-UTIL-4 A proxy set only in the system settings is used, and a PAC address is named
- **Proves.** DH-D-UTIL-9, DH-ADR-UTIL-10.
- **Given.** A Windows 11 machine and a macOS machine; the built `dsh`; a real forward proxy that logs each request; no proxy variable in the environment or in the home's `.env`; the proxy's address entered in the operating system's network settings; a real model API key supplied at run time; a fresh temporary home.
- **When.** (1) Run one headless prompt. (2) Export `HTTPS_PROXY` with the address of a second logging proxy and repeat. (3) Unset it, replace the system setting by an auto-configuration (PAC) address and repeat.
- **Then.** In (1) the model request is in the first proxy's log and the launch diagnostics name the proxy with source `system`. In (2) the request is in the second proxy's log only. In (3) the launch prints the "not supported; export HTTPS_PROXY" line, no request is made to the PAC address, and the model request goes out directly or fails to connect, as the network allows. On macOS no `scutil` process remains after the launch.
- **Cleanup.** Restore the system proxy setting recorded before the run; stop both proxies; delete the home. If the setting cannot be restored, record its previous value in the report and stop the run.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

1. **Clauses relied on that their owners have not yet confirmed in their own text** (each sent as a request). A09: `dshbin` ends a tree through the process package, and the spawn helper calls `ChildEnv` with the right audience. A18: `sharing.Permit` keeps a `Context()` method. A03 and C4: tool cards read streaming arguments through `jsonscan`, and a tool declares short descriptive fields first. A22: the manifest carries a harness version range checked with `version.ParseRange`. Confirmed by their owners since: a served stream may end in a recorded failure and any adapter, a stub included, can be registered (A04); `memstore` passes the storage suite (A05); `subagent/started` records the creating tool call (A12); each home directory is classified (A07).
2. **Two things test authors must know.** The kit gives virtual time (`testing/synctest`), not a clock object: no component takes a clock interface ([DH-ADR-NODETIMERS-1](../libs/node-crypto-timers-perf-hooks.md#dh-adr-nodetimers-1)). A program the model writes is an ordinary command ([DH-ADR-8](../design.md#dh-adr-8)), so it gets the proxy settings of the `ModelRun` audience, not none.
3. **Facts that need a run.** Whether the schema module's validator returns every violation or only the first; if only the first, the model-subset validator is written in `schema`, and the `Issues` interface does not change. Whether a `net.Pipe` connection counts as durably blocked inside a `synctest` bubble, which decides if `faultserver` can be used in virtual time. The macOS directory-entry cap for `watch`. For `sysproxy` ([DH-ADR-UTIL-10](#dh-adr-util-10)): that the Windows values can be read with `golang.org/x/sys/windows/registry` in a `CGO_ENABLED=0` build (the reviewer of U-K2-2 doubts the earlier claim that this needs cgo, and nobody has built it); that the same registry values are what a per-user proxy set through the settings application writes; and that the output of `scutil --proxy` has the same keys on the supported macOS versions. Whichever way these turn out, the design stands: a platform whose reader cannot be built without cgo simply has no reader.
4. **Minimum Go version.** The project has one floor, `go 1.26.0`, stated in all three `go.mod` files; `testing/synctest`, which the kit's virtual time needs, is available from Go 1.25 and so is covered.
5. **Limits kept.** `watch` watches paths of the local filesystem only; a consumer that follows files behind a non-local filesystem provider polls metadata through that provider, as A19's `wsfiles` does. A PAC file is never evaluated, and the operating system's proxy setting is read on Windows and macOS only, once, at launch. A Node child follows the proxy only if its runtime does. The import-graph gate shows which packages can dial, not that one did; `egresstest` catches a real request only on a path a test exercises. Cancellation is cooperative. A hang is authored, never recorded. Replay proves the product against recorded replies, not against a live model.
6. **Scope of the guarantee.** The kits make evidence comparable; they do not make it sufficient. A passing replay proves the recorded scenarios for the tested build and nothing about an unrecorded path.

## 11. Units dispositioned by this spec

| Unit type | Count | Dispositions |
|---|---|---|
| `UP-PKG` | 32 | 11 ported, 9 replaced, 9 merged, 1 dropped, 2 not applicable |
| `UP-DEBT` | 86 | 40 resolved, 28 not applicable, 18 carried |
| `UP-CFG` | 1 | ported (`replay.Config`) |
| `UP-DEC` (handed over) | 13 | 4 ported, 8 replaced, 1 dropped |

Go items declared: 12 deliverables, 10 decisions, 22 local tests, 4 scenarios. Rows are in [../trace/fragments/A23.jsonl](../trace/fragments/A23.jsonl).
