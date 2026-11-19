# harness:DSH -- Technical specification (problem, scope, constraints, risks)

> Story `harness:DSH` ("dsh", 13 points). Operator prompt, verbatim:
> "clone deepseek harness, given context in docs\harness of this repo, and design and
> impl doc, i want you to follow impl".

This document states **what problem this story solves, what is in and out of its
scope, what it will never do, which constraints are hard, and which risks can sink
it**. It is the contract the sibling plan documents build against:

| Sibling | Owns | Relationship to this file |
|---|---|---|
| [architecture.md](architecture.md) | Components C01..C30, data model, interfaces, flows F1..F7, the **Test & Environment Contract** (TC-01a .. TC-X2) | This file adopts its M1 slice and its TC ids; it does not repeat component tables or signatures |
| [implementation-plan.md](implementation-plan.md) | Ordered work items and commits | Must stay inside the scope of sec. 2 and obey every constraint of sec. 5 |
| [e2e-scenarios.md](e2e-scenarios.md) | `DH-E-*` scenario scripts | Must honour sec. 3.2 (authored, not executed) and constraint EV-4 |

Source documents (the requirements; this story never re-decides them):
[docs/harness/README.md](../../harness/README.md),
[design.md](../../harness/design.md) (outcomes `DH-O-1..16`, owner decisions
`DH-ADR-1..40`), [impl.md](../../harness/impl.md) (stages `DH-S-1..59`), the area and
library specifications under `docs/harness/areas/` and `docs/harness/libs/`, and the
foundation documents under `docs/harness/upstream/`. Upstream is
`deepseek-ai/deepseek-harness` at commit `2e39e70496aa02986ca6a820c5d497e816b69d7e`
(2026-11-18).

---

## 1. Problem statement

### 1.1 What the operator asked for, read precisely

| Phrase in the prompt | Meaning adopted in this story | Source that fixes the meaning |
|---|---|---|
| "clone deepseek harness" | Build `dsh`, a **Go re-implementation** of the TypeScript deepseek-harness. Not a fork, not a source port, not a wire-compatible drop-in. Upstream is the *requirements source*, not a contract | design.md sec. 3.1; DH-ADR-2 ("Go-native redesign"); non-goal "compatibility with upstream formats, protocols, profiles, clients or fixtures" (design.md sec. 3.3) |
| "given context in docs\harness of this repo" | The 39 area specs, ~100 library specs, 9 foundation docs, debt registers and the traceability ledger under `docs/harness/` are the **only** design input. No fresh upstream reading replaces them | docs/harness/README.md ("Every upstream fact in these documents was read at that commit") |
| "and design and impl doc" | `design.md` fixes *what* and the owner decisions; `impl.md` fixes *order*, *entry conditions* and *exit evidence* | impl.md sec. 1.1 |
| "i want you to follow impl" | Execute impl.md **stage by stage, in its order, with its step IDs, its rules of sec. 1.4 and its evidence record (sec. 7.3)**. Where impl.md and an area spec disagree on placement, impl.md's scenario index (sec. 7.5) governs | impl.md sec. 1.4 rules 1-9; sec. 6 preamble |
| 13 story points | One milestone, not the product. impl.md has 59 stages, 395 deliverables and 246 scenarios; this story takes the first milestone, **M1 "A turn runs"**, reached when DH-S-11 is built | impl.md sec. 3.3 (M1 row); architecture.md sec. 1.1 |

### 1.2 The problem in one paragraph

Upstream is a sound product built on an expensive mechanism: 391 npm packages, a
vendored framework carrying 21 local patches, five schema systems joined by four
converters, configuration that evaluates arbitrary JavaScript, in-host plugins with
full authority, and 51 verify scripts across 35 workflows (design.md sec. 3.1). The
design replaces that mechanism with one pure-Go binary composed from the cordis port
that already lives in `src/go`. **Nothing of that binary exists yet** (README "Status":
implementation, test authoring, local passes and acceptance are all "Not started").
The concrete problem of this story is therefore: *turn a finished, audited paper design
into the first running slice -- an agent that answers one prompt against the real
DeepSeek API with a tool call through the guarded pipeline, every step logged and
rebuildable -- without breaking the existing cordis port, and with the quality gates
in force from the first commit so that later stages never need a baseline of
exceptions.*

### 1.3 Why M1 is the right cut, and why it is hard

- **It is the first point where the spine proves its central promise.** DH-O-4
  ("model-visible means logged") and DH-O-3 ("every turn has the same guarantees") are
  decided in DH-S-5 and DH-S-11. Every later carrier (CLI launchers DH-S-16, SDK server
  DH-S-15, web UI DH-S-19..21) depends on them; an error found after the carriers exist
  costs a rewrite of the carriers too.
- **It is mostly foundations.** Of the eleven stages, seven (DH-S-1..4, 6..8) build
  things no user sees: modules, gates, cordis and loader changes, leaf packages, boot,
  store. impl.md sec. 3.2 explains why: the test kit (DH-D-UTIL-6) is built from the
  session contracts, so DH-S-5 precedes DH-S-6.
- **It changes code other people depend on.** DH-S-2 and DH-S-3 modify the public
  `src/go/cordis` and `src/go/loader` packages (DH-ADR-7: "changed, not wrapped"). The
  .NET port under `src/net` is not changed and will diverge (impl.md sec. 9.2).

### 1.4 Starting point in the repository (verified on this branch)

| Fact | Value today | Consequence |
|---|---|---|
| `src/go/go.mod` | module `github.com/smartpcr/agent-plugin/src/go`, `go 1.24`, standard library only | Raised to `go 1.26.0` in step 1.1; must stay stdlib-only |
| `tools/harness-trace/go.mod` | `go 1.24`; `verify` passes (README "Coverage") | Becomes one gate of `tools/harness-gate`; must keep passing after every commit |
| `go.work`, `src/sdk`, `src/harness`, `tools/harness-gate` | Do not exist | Created in DH-S-1 |
| `.github/workflows/` | `ci.yml` (gofmt, vet, `go test -race` on Linux, `go test` on Windows), `release.yml` (GoReleaser, `src/go/vX.Y.Z` tag) | Extended, not replaced (upstream/toolchain.md sec. 4 preamble) |
| `src/go/cmd/cordis-sample` | Example program of the port | Its output becomes the golden baseline TC-02b before DH-S-2 edits anything |
| Local toolchain on the authoring host | `go1.27.4 windows/amd64` | Newer than the 1.26.0 floor; see risk R-05 |
| Gate host | No docker, no pre-provisioned services | Binding: architecture.md "Test & Environment Contract" |

---

## 2. In scope

### 2.1 Stages

The story builds impl.md stages **DH-S-1 through DH-S-11, in that order, each entered
only when every stage on its Entry line is built** (impl.md sec. 1.4 rule 1). The
deliverable list per stage is architecture.md sec. 1.2; it is not repeated here. The
stage-level obligations are:

| Stage | Result this story must deliver (impl.md wording, condensed) | Outcomes served | Closes on (impl.md "Exit evidence") |
|---|---|---|---|
| DH-S-1 | Three modules build together; a fixed gate list passes on Linux, macOS, Windows; release rehearsal compiles one pure-Go binary per platform, publishes nothing | DH-O-14, DH-O-15 | Gate-runner output on three OSes + seeded-violation checks |
| DH-S-2 | cordis: final unload, per-fiber cancellation context, scoped dispatch, contained `Notify`, `AroundKey`, raw/effective/candidate config, strict decode, chosen log exporters | DH-O-1 | `DH-T-CORDIS-*` under the race detector; existing `src/go` tests still pass |
| DH-S-3 | loader: positions, entry schema, expression seam, layer composition + ownership index, write-back to one layer, nested loader, restart one entry, settled-reload notice, write guard | DH-O-2 | `DH-T-LOADER-*` pass |
| DH-S-4 | Leaf packages: `src/sdk/jsonrpc`, `src/sdk/protocol`, `internal/schema`, `internal/llm/vocab`, `fsx`, `textdiff`, `search`, watcher, YAML codec + `yamlx`, expression engine, logging component, catalog | DH-O-4, DH-O-13 | Import-direction test: `src/sdk` imports neither cordis nor the harness |
| DH-S-5 | Spine contracts in memory: event vocabulary, live session, folds, rebuildable request, brackets, store contract + in-memory reference, LLM runtime, agent contract, interaction registry, fold registry | DH-O-3, DH-O-4 | Store conformance suite passes against the in-memory reference; persisted-type note test in place |
| DH-S-6 | Boot, launch facts, home layout, secrets by reference, profiles/bundles/layers, CLI tree + output contract, fatal path, bounded shutdown, preview, validated reload, settings edit path, egress off-switch and policy, test kit | DH-O-1, DH-O-2, DH-O-7, DH-O-14 | All 17 deliverables built **at one commit** (dependency circle C-1, impl.md sec. 10.2) |
| DH-S-7 | SQLite store: one DB per home, atomic appends, flush barrier, one writer per session with safe takeover, checkpoints, crash recovery, export/import, follow, duties, delete/check/repair | DH-O-5 | Conformance suite against SQLite, the **driver measurement** (fact F-2), crash-injection tests |
| DH-S-8 | Typed machine state, views never ahead of the log, built-in views, listing/following, spill store, attachments | DH-O-5, DH-O-6 | Refold-equals-stored test for every built-in view |
| DH-S-9 | First provider `deepseek-official` (Anthropic Messages wire), bounded retry outside the adapter, usage fold | DH-O-8 | Request captured at the fake transport equals the request rebuilt from the log |
| DH-S-10 | Tool declaration + registry, deterministic prompt assembly, approvals with one closed outcome, the 8-stage guarded pipeline, deadlines, result budget, durable inbox, questions, human commands, session grants | DH-O-6, DH-O-7 | Denial enforced through the executor, not by hiding the tool |
| DH-S-11 | Agent driver: logged model selection, fixed step tool set, bracketed/bounded turns and steps, cancellation with cause, lifecycle transaction, delegation seam, logged context pipeline -- **milestone M1** | DH-O-3, DH-O-4, DH-O-9 | Property test: no tool call left unanswered on any exit path |

### 2.2 Work products in scope

1. **Code** for every deliverable named in architecture.md sec. 1.2, including the
   deliverables impl.md marks "(P1, pulled forward)" (for example DH-D-LOADER-8, -11,
   -13; DH-D-UTIL-3; DH-D-BOOT-6, -7; DH-D-STORAGE-8; DH-D-STORE-6, -8; DH-D-VIEW-3;
   DH-D-STORAGE-3, -7; DH-D-LLM-6; DH-D-HUMAN-3, -5, -6; DH-D-SUBAGENT-1). Pulled forward
   does not lower their evidence bar (impl.md sec. 4).
2. **Local tests** named in each deliverable's "Done when" line (`DH-T-*`), proven by
   the TC rows of architecture.md's Test & Environment Contract.
3. **Goldens** committed with the code that produces them; TC-02b committed **before**
   the first DH-S-2 edit.
4. **Authored scenarios**: every `DH-E-*` indexed (impl.md sec. 7.5) to a stage this
   story builds is written against the scenario contract, reviewed, and listed in the
   acceptance workflow's required list (impl.md sec. 1.4 rule 4).
5. **One manual-check record per step**, labelled `manual`, carrying the commit
   (impl.md sec. 1.4 rule 3). The DH-S-11 manual check is the M1 demonstration: one
   prompt to the real DeepSeek API with one test tool, the bracketed log and the
   rebuilt request recorded; a second run cancelled mid-stream with the cause on
   `turn/end`.
6. **One evidence record per stage** in the format of impl.md sec. 7.3.
7. **CI**: the keyless `harness` job on Linux, macOS and Windows and the separate
   credentialed workflow with its preflight (step 1.8). The credentialed workflow is
   created; it is not required to pass in this story because no scenario executes
   here (sec. 3.2).
8. **Ledger upkeep**: `tools/harness-trace verify` keeps passing; deliverables move to
   status *built* only.

### 2.3 Definition of "done" for this story

The story is done when **all** of the following hold at one commit on the story branch:

- Every step 1.1 .. 11.7 of impl.md is ticked as *built* (impl.md sec. 1.3 "Built").
  None is claimed *done* unless its proof is local tests only.
- `tools/harness-gate` passes on the three CI operating systems.
- Every stage DH-S-1 .. DH-S-11 has its evidence record.
- The M1 manual check is recorded.
- The held-open item H-21 (DH-D-BOOT-6's `dsh config` commands) is listed as open
  with its closing step in DH-S-16.

---

## 3. Out of scope

### 3.1 Later stages (scheduled by impl.md, not by this story)

| Excluded | First stage that owns it | Why it cannot be pulled in |
|---|---|---|
| Working directory, filesystem seam, policy resolver, file tools | DH-S-12 | Entry needs DH-S-11 built; it is M2 work |
| Sandbox launch, Linux backends (bubblewrap, Landlock), shell tools | DH-S-13 | Needs DH-S-12; a runtime prerequisite on Linux only |
| Agent presets and bundles | DH-S-14 | Needs DH-S-11 |
| Listener, sign-in, stdio, **SDK server** (first live transport of the `jsonrpc` codec) | DH-S-15 | Needs DH-S-12, DH-S-14 |
| Profiles, launchers (`dsh headless`, `dsh web`), admin tree, replay runner, titles (DH-D-VIEW-4) | DH-S-16 | Needs DH-S-12..15 |
| Every acceptance stage (DH-S-17, -18, -22, -32, -39, -48, -58) | as named | They execute scenarios; see 3.2 |
| Web UI, HTML routes, event stream | DH-S-19 .. DH-S-22 | M3 |
| Additional providers, compaction, skills, jobs, terminals, macOS/Windows sandboxes, SSH, subagents beyond the seam, schedules, goals, plan mode, workflows | DH-S-23 .. DH-S-31 | P1 parity |
| MCP, web access, plugin processes, OS keychain, vendor account, office documents, ACP, Python SDK, admin commands, watchdog | DH-S-33 .. DH-S-38 | P1 parity |
| Signed release, documentation website, operator lifecycle | DH-S-46 | Blocked on open owner decisions (signing keys, hosting) |
| All P2 / experimental (`internal/x/...`) | DH-S-49 .. DH-S-59 | M5 |

### 3.2 Scenario execution

No `DH-E-*` scenario is **executed** in this story. Each is authored here and executes
at the stage impl.md assigns (architecture.md, contract rule 2, holds the schedule).
In particular the guarded store scenarios DH-E-STORE-1, -2 and -6 run at step 16.8;
until then **the SQLite store built here is used on scratch homes only**. A skipped,
blocked or missing scenario is never reported as a pass (DH-O-15, impl.md rule 6).

### 3.3 Other exclusions

- **Held open:** H-21 -- the `dsh config` preview/explain/check commands of
  DH-D-BOOT-6 wait for the admin tree of DH-S-16; in this story they exist as a Go API.
- **Not done, only built:** every deliverable whose proof includes a scenario. "Built"
  and "done" are never merged (impl.md sec. 1.3).
- **The .NET port** (`src/net`): untouched. Whether it follows the `src/go` changes is
  an open owner matter that blocks nothing in this plan (impl.md sec. 9.2).
- **Enabling anything for users.** At M1 "nothing is enabled" (impl.md sec. 3.3): no
  launcher, no sandbox, no carrier.
- **Publishing.** The release rehearsal (step 1.9) publishes nothing; no module tag,
  no GitHub release asset, no signing.
- **Re-auditing docs/harness.** The three audits' fixes "have not been re-audited"
  (README "Audit results"). This story records any inconsistency it trips over
  (constraint DC-3) but does not run a new audit.

---

## 4. Non-goals

Out-of-scope items (sec. 3) are *later*. Non-goals are *never*, for this story or the
product, each by a recorded decision. A work item or scenario that drifts into one of
these is rejected at review, not deferred.

| # | Non-goal | Decision | What it rules out concretely in DH-S-1..11 |
|---|---|---|---|
| NG-1 | Compatibility with upstream formats, protocols, profiles, clients or fixtures | DH-ADR-2; PERSISTENCE-5 | No upstream JSONL session reader, no upstream event names kept "for compatibility", no reuse of upstream test fixtures as goldens. Persisted types are this design's (DH-D-SESSION-1, DH-D-STORE-5) |
| NG-2 | Translating the TypeScript source file by file | DH-ADR-2 | Each mechanism is re-derived for Go from the area spec; upstream `path:line` citations are evidence, not templates |
| NG-3 | Wrapping cordis instead of changing it | DH-ADR-7 | No adapter layer over `src/go/cordis`; the eight DH-D-CORDIS changes land in the port itself |
| NG-4 | cgo in the shipped binary | DH-ADR-15 | `cmd/dsh` builds with `CGO_ENABLED=0`; `github.com/mattn/go-sqlite3` and any cgo-only module are excluded (libs/sqlite.md) |
| NG-5 | Loading code into the running host or hot-reloading code | DH-ADR-4; LOADER-8 | The loader restarts entries of compiled-in components only; configuration never evaluates a general-purpose language (the expression engine of DH-D-LOADER-5 is the only evaluator) |
| NG-6 | An embedded interpreter for model-written programs | DH-ADR-8 | Nothing in DH-S-4..11 embeds a JS/Python engine |
| NG-7 | Any data leaving the machine by default | DH-ADR-9, DH-ADR-27, DH-ADR-32 | The only outbound kind exercised at M1 is the model request to the configured route, through egress (DH-D-UTIL-2) under an explicit permit; no telemetry, upload or update check is wired |
| NG-8 | Approval rules stored across sessions | DH-ADR-16 | Approvals are one-shot; session grants live with the session and are never copied to resume, fork or subagent (DH-D-HUMAN-3); no machine-state table stores a rule or grant |
| NG-9 | A change to durable configuration made by the model | DH-ADR-30 | No tool registered at M1 can write a configuration layer; the settings edit path (DH-D-STORAGE-8) is an owner path |
| NG-10 | A security boundary stronger than design.md sec. 6 | design.md sec. 3.3 | M1 has no sandbox; it claims none. The harness is stated unaudited (DH-ADR-SECURITY-12) |
| NG-11 | Re-deciding design or impl | Story prompt ("follow impl") | Owner decisions (DH-ADR-1..40) and stage order are inputs. A disagreement becomes an open question to the owner, not a code change (constraint DC-2) |
| NG-12 | A desktop app, webview, or any UI | DH-ADR-18, DH-ADR-3 | No UI code in this story at all; the UI is HTML from the host and starts at DH-S-19 |

---

## 5. Hard constraints

A hard constraint is one whose violation fails a gate, fails review, or makes a later
stage unbuildable. Each has an ID so the implementation plan and reviews can cite it.

### 5.1 Repository and module layout

| ID | Constraint | Source | Enforced by |
|---|---|---|---|
| RM-1 | Exactly three Go modules: `src/go` (cordis, loader, timer), `src/sdk` (jsonrpc, protocol; the Go client arrives later), `src/harness` (the product). One version number, three tags on one commit | DH-ADR-26; design.md sec. 4.2 | Step 1.1; `DH-T-TOOLCHAIN-11` |
| RM-2 | All three modules state `go 1.26.0`. `tools/harness-gate` and `tools/harness-trace` may keep their own lines | design.md sec. 4.2; fact F-1 | Gate: cross-compile with the floor toolchain (see R-05) |
| RM-3 | `src/go` and `src/sdk` import the standard library only and never each other | DH-ADR-26 | Import-direction test (DH-S-4 exit) |
| RM-4 | `src/harness` requires `src/go` and `src/sdk` **without `replace`**; the root `go.work` is for development and is off in the release build (`GOWORK=off`) | design.md sec. 4.2 | Release rehearsal (step 1.9) builds with `GOWORK=off` |
| RM-5 | Every implementation package of `src/harness` sits under `internal/`; `cmd/dsh` holds `main` only | design.md sec. 4.2 | Import-direction test |
| RM-6 | Leaf packages (`internal/schema`, `internal/llm/vocab`, `internal/util/*`) import no area; `herr` and `ident` import the standard library only; `internal/llm/vocab` holds **no request type** | areas/util-test-support.md; areas/llm.md DH-D-LLM-11; DH-ADR-SESSION-7 | Import-direction test (TC-05) |
| RM-7 | The spine (session, llm, tools, agent, human, storage, delegate, context) never imports a carrier; a consumer never imports a provider sub-package | design.md sec. 4.1 (ARCH-1, ARCH-6) | Import-direction test |
| RM-8 | `internal/context` is a parent directory with no Go package (the standard library's `context` is never shadowed) | design.md sec. 4.2 | Import-direction test |
| RM-9 | Package names below a family are the area spec's; architecture.md's names are indicative | architecture.md sec. 3.1 | Review |
| RM-10 | Third-party modules only where a library spec justifies them (`docs/harness/libs/`); each pinned with a reason. `modernc.org/sqlite` v1.62.0 is pinned together with the exact `modernc.org/libc` it was validated with (v1.80.1) | libs/sqlite.md; DH-ADR-TOOLCHAIN-11 | Review of every `go.mod` change |

### 5.2 Build and quality gates

| ID | Constraint | Source |
|---|---|---|
| QG-1 | One command, `tools/harness-gate`, runs a **fixed sequential** gate list and names the first failing gate; exit status non-zero on failure | DH-D-TOOLCHAIN-2; DH-ADR-TOOLCHAIN-9 |
| QG-2 | **No baseline file and no exemption list** for any gate, from the first commit | DH-O-15; DH-ADR-TOOLCHAIN-8 |
| QG-3 | Coverage floor **85% of statements per package**, merged across processes (re-exec'd child processes count) | DH-ADR-14; DH-ADR-TOOLCHAIN-7 |
| QG-4 | Unit tests run with `-race -shuffle=on -count=1` wherever the race detector is available, and time-dependent tests use virtual time (`testing/synctest` or the test kit's clock), never sleeps | DH-ADR-TOOLCHAIN-2; upstream/toolchain.md (layers 1-4) |
| QG-5 | Every test that starts a goroutine, listener or child process registers its release with `t.Cleanup` | upstream/toolchain.md DH-ADR-TOOLCHAIN-2 |
| QG-6 | Goldens use one helper with **one update switch**; every parser of untrusted bytes (`jsonrpc`, `yamlx`, SSE decode, schema validation input) has a fuzz target whose seed corpus runs as a unit test | DH-D-TOOLCHAIN-3; DH-ADR-TOOLCHAIN-3, -4 |
| QG-7 | Performance budgets are tests that ignore environment overrides | DH-ADR-TOOLCHAIN-14 |
| QG-8 | `cmd/dsh` cross-compiles with `CGO_ENABLED=0` for Linux, macOS and Windows on amd64 and arm64 | DH-ADR-TOOLCHAIN-13; DH-ADR-15 |
| QG-9 | `tools/harness-trace verify` is a gate and passes on every commit | DH-ADR-6; impl.md DH-S-1 |
| QG-10 | CI: a keyless `harness` job on three OSes runs the gate runner; credentialed scenarios live in a **separate** workflow, never triggered by `pull_request_target`, whose preflight fails when a secret or a required scenario is missing | DH-ADR-TOOLCHAIN-12, -6 |

### 5.3 Evidence and status discipline

| ID | Constraint | Source |
|---|---|---|
| EV-1 | Five kinds of evidence are kept apart: unit, composition through the real loader, golden, keyless replay, real-environment scenario. A test with a fake is never counted as a scenario | design.md sec. 3.4; TOOLCHAIN-1 |
| EV-2 | A build stage closes as **built**; a deliverable is **done** only when every scenario proving it passed (or it is proven by local tests only) | impl.md sec. 1.3, 1.4 rule 5 |
| EV-3 | Every step has a recorded manual check labelled `manual` with the commit. A manual check never ticks a scenario | impl.md sec. 1.4 rule 3 |
| EV-4 | Scenarios are authored, reviewed and listed in this story; executed at the impl.md-assigned stage; a skipped scenario stays open | impl.md sec. 1.4 rules 4, 6, 8 |
| EV-5 | Proof strategies are those of architecture.md's Test & Environment Contract: `in-process` means zero external I/O; a `service:*` row (filesystem, os-process, go-toolchain, loopback-http, sqlite) is never proved with a mock in its place | architecture.md, contract |
| EV-6 | The real DeepSeek API is reached only by the manual checks of DH-S-9 and DH-S-11; it is **never** a gate criterion | impl.md DH-S-9, DH-S-11; architecture.md TC-09b, TC-11b |
| EV-7 | Each stage ends with the evidence record of impl.md sec. 7.3: source commit, build identity, environment, exact commands, run ids, results, cleanup proof | impl.md sec. 7.3 |

### 5.4 Behavioural invariants of the spine (non-negotiable at M1)

These are the outcomes this story makes real. Each is a property test or a
conformance test, not a review item.

| ID | Invariant | Outcome / deliverable |
|---|---|---|
| SP-1 | **Model-visible means logged.** Only the session constructs a `session.Request` (`NextRequest` for loop steps, `RequestFrom(seq)` for auxiliary calls); every settlement stores `requestAsOf` and `requestDigest`; the bytes sent equal the request rebuilt from the log | DH-O-4; DH-D-SESSION-4; DH-D-LLM-1 |
| SP-2 | **One attempt settles as exactly one logged event** (`assistant/message` or `assistant/attempt`); retry is a bounded policy outside the adapter; an auth failure is never retried and never falls back to another route | DH-O-8; DH-D-LLM-2, -3 |
| SP-3 | **Brackets always balance.** `turn/start`..`turn/end` and `step/start`..`step/end` are checked on append and closed on every exit path; `interrupted` is written only by the session core at write-open/recovery | DH-O-3; DH-D-SESSION-5; DH-D-AGENT-3 |
| SP-4 | **No tool call is left unanswered** in history on any exit path (cancel, budget, error, lease loss) | DH-O-3; DH-S-11 exit evidence |
| SP-5 | **One accepted message is at most one turn**; inbox items are durable before `Send` returns and run at most once | DH-O-3; DH-D-AGENT-2 |
| SP-6 | **Arguments that fail the tool's schema never reach the body**; policy listeners, deny-only guards, approval, checkpoint flush, body, shaping run in one fixed order; logged arguments are never rewritten | DH-O-6; DH-D-TOOLS-2 |
| SP-7 | **Nobody to ask means no.** An approval with no answerer is a denial; denial is enforced in the executor, not by hiding the tool | DH-O-7; DH-D-HUMAN-2; DH-S-10 exit evidence |
| SP-8 | **Checkpoint before irreversible work**: flush before a provider attempt, before a tool body and at step end; a failed flush means the work does not run | DH-O-5; DH-D-STORE-4 |
| SP-9 | **One writer per session across processes**; the lease epoch is checked on every append; nothing expires by time; takeover is explicit (`Force`) or provably safe (holder's liveness lock acquired inside the write transaction) | DH-O-5; DH-ADR-12; DH-D-STORE-3 |
| SP-10 | **Unload is final**: a fiber's context is cancelled by unload and `Wait` returns only after every effect has stopped; dependents unload first | DH-O-1; DH-D-CORDIS-1, -2 |
| SP-11 | **Secrets are referenced, not held**: config names a secret; the value is resolved per use behind one store and never enters config, log, error text, or a child's environment | DH-O-7; DH-D-HUMAN-7 |
| SP-12 | **One spawn path**: every program starts from an argument list through `internal/world/subprocess`; no shell-string spawn; a process tree is stopped and awaited as a whole | DH-O-1; DH-D-EXEC-1, -2 |
| SP-13 | **Nothing leaves without a permit**: every outbound connection goes through `egress`; each data kind has one visible setting and the off-switch stops all senders | DH-O-14; DH-D-UTIL-2; DH-D-HOST-4; DH-ADR-27 |
| SP-14 | **Stored state never ahead of the log**; a changed fold version rebuilds its stored state | DH-O-5; DH-D-VIEW-2 |

### 5.5 Configuration values and error-code families pinned for M1

architecture.md sec. 4 and 5 defer field-level values to this file. Values below are
copied from the owning area spec; **this story does not choose numbers**. Rows marked
*unconfirmed* are recommended defaults the owner has not confirmed (impl.md sec. 9.1)
and must be confirmed or changed **before the named stage starts** (constraint DC-4).

| Setting (owner) | Value | Status | Confirm before |
|---|---|---|---|
| Harness home resolution (`homedir`, DH-D-UTIL-5) | explicit path > `DSH_HOME` > `~/.dsh`; blank value = unset | Spec (DH-T-UTIL-8) | -- |
| Home on a network filesystem (DH-D-STORE-2) | Refused (`store/unsupported-filesystem`) | Spec | -- |
| Profile limits (boot.md sec. 6.2) | `StartupTimeout` 60 s; `ShutdownTimeout` 20 s (the one exit bound); `FatalReleaseTimeout` 2 s; `CleanupWarnAfter` 10 s | Spec; "Inference: proposals" | DH-S-6 |
| Process exit statuses (app-cli.md sec. 6.2.4) | 0 clean; 5 unknown profile; 70 fatal with report; 78 configuration not composable / required entry failed; 124 forced after the shutdown bound | Spec | -- |
| Tool registry (`tools.registry`, tools.md sec. 6.2) | `maxParallel` 10 (the one overlap cap); `defaultTimeout` 5m; `grace` 5s; `maxArgumentBytes` 1 MiB; `maxResultBytes` 256 KiB; `forceExclusive` empty | *Unconfirmed* ("tool defaults", I1 Q10) | DH-S-10 |
| Spill budget (DH-D-STORAGE-5/-6) | As stated in areas/storage.md; must stay below `maxResultBytes` (load-time check) | *Unconfirmed* (I1 Q10) | DH-S-8 |
| Turn budget (`driver.Config`, agent-loop.md) | `budget.maxSteps` 500 (0 = unlimited, must be written explicitly); `budget.maxAttemptsPerStep` 8; `budget.maxDuration` none; `finalFlushTimeout` 10 s; `drainWarnAfter` 10 s | *Unconfirmed* (I1 Q3) | DH-S-11 |
| Default model route (bundle-preset.md, llm.md) | `deepseek-official`, Anthropic Messages wire, key by reference (`DEEPSEEK_API_KEY` supplied at run time for manual checks) | Spec | -- |
| Safe mode (I3 Q4) | Switches off plugin processes and optional bundles | *Unconfirmed* | DH-S-6 |
| Volatile reference type (I4 Q2) | Not built | *Unconfirmed* | DH-S-2 |

**Error codes.** Every error carries a `herr.Reason` of the form `<domain>/<reason>`,
declared as a constant by the package that owns the failure; `src/sdk/jsonrpc` reads
it through a `Reason() string` method without importing the harness
(areas/util-test-support.md `herr`; areas/sdk-acp-typert.md). Families exercised at
M1, each owned and enumerated by its area spec:

| Family | Owner spec | Examples (not exhaustive) |
|---|---|---|
| `session/*`, `scope/*` | areas/core-session.md sec. 6, areas/session-store.md sec. 6.2 | `session/lease-lost`, `session/seq-conflict`, `session/deleted`, `session/unbalanced`, `session/unknown-type`, `session/newer-payload` |
| `store/*` | areas/session-store.md sec. 6.2 | `store/busy`, `store/corrupt`, `store/newer-layout`, `store/unsupported-filesystem`, `store/disk-full` |
| `llm/*` | areas/llm.md | Permanent: `llm/auth`, `llm/invalid-request`, `llm/content-refused`, `llm/missing-credential`, `llm/no-route`; transient classes retried by DH-D-LLM-3 only |
| `tools/*` | areas/tools.md | `tools/not-started`, `tools/outcome-unknown`, `tools/timeout`, `tools/aborted-before-start`, `tools/limit` |
| `egress/*` | areas/util-test-support.md | `egress/not-permitted` |
| `extension/*` | areas/agent-loop.md | `extension/failed` (names the listener) |

A reason not of the form `<domain>/<reason>` fails a declaration test (DH-T-UTIL-8).

### 5.6 Environment and secrets

| ID | Constraint |
|---|---|
| EN-1 | The gate host has **no docker and no services**. Every gate-run test uses only the five `service:*` provisions of architecture.md's contract; SQLite is embedded (pure Go) with files under `t.TempDir()` |
| EN-2 | Tests never touch the developer's real home; every home is a temp home |
| EN-3 | A real model key is supplied **at run time** for manual checks only; it is never committed, never written to a layer file, never echoed into a manual-check record, and is absent from the keyless CI job |
| EN-4 | Child processes in tests are the test binary re-executing itself (no external program is installed) |
| EN-5 | Linux exact process-tree ownership needs a user service manager or writable cgroup (register U-K1-1, DH-ADR-40); without it the fallback is process groups and the fallback is reported, not hidden |

### 5.7 Documentation and change control

| ID | Constraint |
|---|---|
| DC-1 | Every commit names the step (`1.1` .. `11.7`) and the `DH-D-*` deliverable it builds, and stays within one stage. DH-S-6 additionally has one closing commit at which all 17 of its deliverables are built together (circle C-1) |
| DC-2 | `docs/harness/` is the requirements baseline. A needed change to a design decision is raised to the owner and recorded; it is never made silently in code. Mechanical doc fixes (a broken link, a stale count) are allowed and noted in the evidence record |
| DC-3 | An inconsistency found between docs/harness documents is recorded in the stage's evidence record with both citations and the choice taken; impl.md's sec. 7.5 index governs scenario placement |
| DC-4 | An *unconfirmed* default (sec. 5.5) is not baked into a persisted format or a public type before the owner confirms it; it lives in configuration with the documented value |
| DC-5 | IDs are never renumbered; this story mints no new `DH-*` IDs except where a spec explicitly leaves one to the implementer |
| DC-6 | Plan documents and evidence records are ASCII-only |

---

## 6. Identified risks

Likelihood (L) and impact (I) are High / Medium / Low. "Trigger" is the first
observable sign; "Response" is what the plan does when the trigger fires. Every
response stays inside the constraints of sec. 5 -- no risk is mitigated by relaxing a
gate.

| ID | Risk | L | I | Trigger | Mitigation (before) | Response (after) |
|---|---|---|---|---|---|---|
| R-01 | **Size versus points.** 11 stages, about 106 steps, roughly 100 deliverables for a 13-point story; the story stalls mid-foundation with nothing runnable | H | H | DH-S-6 not built when half the story budget is spent | Land stage by stage; each stage is independently reviewable and closes on its own evidence record; nothing later depends on an unclosed stage | Stop at the last *built* stage and re-plan the remainder as a follow-up story; never mark a partial stage built. Open question `dsh-story-slice` |
| R-02 | **SQLite driver fails measurement** (fact F-2): `modernc.org/sqlite` is provisional and about 3x slower than C on large rows -- assistant messages and tool results are large rows (libs/sqlite.md sec. 5) | M | H | Step 7.1 measurement misses a libs/sqlite.md sec. 5.3 threshold | Measure first in step 7.1, before the rest of DH-S-7; keep every driver-specific string in the one driver package; the store contract (C10) and conformance suite isolate the choice | Switch to the named fallback `github.com/ncruces/go-sqlite3` (pure Go) and re-run the suite; a cgo driver is not an option (NG-4). If both fail, escalate to the owner |
| R-03 | **Dependency circle C-1 in DH-S-6.** 14 deliverables depend on each other; partial landing leaves tests that cannot pass | H | M | A DH-S-6 step's local tests need a later step's real implementation | Contract-first order exactly as impl.md DH-S-6 "Sequence"; hand-written fakes until the test kit (6.17) replaces them | The stage closes only when all 17 are built at one commit (DC-1); no gate is relaxed meanwhile -- a step whose tests need a not-yet-built circle member stays unmerged on its work branch until that member lands |
| R-04 | **Breaking the existing cordis port.** DH-S-2/-3 change public behaviour of `src/go/cordis` and `src/go/loader` that `cmd/cordis-sample`, existing tests and external importers rely on | M | H | TC-02b golden diff that is not explained by the change | Commit the TC-02b golden **before** the first edit; one deliverable per commit; existing `src/go` tests stay green under `-race` | Revert the offending commit; re-record the golden only for an intended diff, with the step ID in the commit |
| R-05 | **Toolchain floor drift.** The authoring host runs `go1.27.4`; code may use a 1.27-only API while the modules claim `go 1.26.0` (fact F-1) | M | M | CI on a 1.26.0 toolchain fails to compile | CI's `harness` job pins Go 1.26.0 exactly; third-party modules resolved once at `go 1.26.0` in step 1.1 / 4.x (fact F-1) | Remove the 1.27 usage; if a chosen module needs >1.26 the floor rises by owner decision and design.md sec. 4.2 is revised |
| R-06 | **Race detector not available on every gate OS.** `go test -race` needs cgo on Windows (and a C toolchain), while upstream/toolchain.md runs race "wherever cgo is available (Linux in CI)"; architecture.md's gate sequence implies race everywhere | M | M | Windows `harness` job fails at the race gate for lack of a C compiler | Gate runner detects race availability per OS and **reports** where race ran; Linux is the race-authoritative lane. The shipped binary stays `CGO_ENABLED=0` regardless (tests may use cgo for `-race`) | Record per-OS race coverage in the evidence record; never silently drop race on Linux. Open question `dsh-race-matrix` |
| R-07 | **Coverage floor with re-exec'd processes.** 85% per package must merge coverage from child processes (os-process tests, two-process lease tests); naive `-coverprofile` misses child coverage and drives packages under the floor | M | M | `internal/world/subprocess` or `internal/session/store/sqlite` under 85% though tests exist | Use Go's integration coverage (`GOCOVERDIR`) for re-exec'd children and merge in the gate (TC-01a covers the merge arithmetic) | Add tests, never exemptions (QG-2) |
| R-08 | **Windows semantics** of process trees (job objects), file locking (`LockFileEx`), file replacement and private-directory checks differ from POSIX; the authoring host *is* Windows | M | H | TC-06c / TC-07 pass on Linux and fail on Windows | Each OS-specific path has its own test file and runs in the three-OS CI job from DH-S-1 on | Fix per OS; a platform limit discovered here is recorded with a register entry, not hidden |
| R-09 | **Unconfirmed defaults harden into formats.** Turn budget, tool defaults and spill budget get baked into persisted events or public types before the owner confirms them | M | M | A persisted payload or exported const carries one of the sec. 5.5 *unconfirmed* values | DC-4: values live in configuration; confirm before DH-S-8, DH-S-10, DH-S-11 | Owner confirms or changes the holding spec; the config default changes, no migration needed |
| R-10 | **Vendor-side unknowns in DH-S-9.** The DeepSeek business error codes (`protocol.ts:245`) and the effort field of the model listing are carried-unresolved debt (README "Tech debt" items 1-2); the Anthropic-Messages mapping may differ on the live endpoint | M | M | Manual check of step 9 sees an unmapped status/body | Recorded SSE fixtures (success, overloaded + retry-after, bad key, mid-stream drop) drive the gate; unknown codes map to the generic class with the raw body kept | Record the observed code in the evidence record and a feedback note to areas/llm.md; never retry an unknown permanent class |
| R-11 | **Key leakage during manual checks.** The M1 manual check uses a real `DEEPSEEK_API_KEY`; a debug log, a fatal report or an error message may print it | L | H | Key substring appears in a log, report or record | SP-11 + EN-3; redaction tests in TC-06a; manual-check records reviewed before commit | Rotate the key, purge the record, add a regression test at the leaking site |
| R-12 | **Docs/harness inconsistency surfaces mid-build.** The audit fix pass was not re-audited (1 blocker, 13 major findings before fixes); ~17-70 "resolved" debt rows may be resolved in name only | M | M | Two specs give different contracts for the same seam | DC-2/DC-3: record both citations and the choice in the stage's evidence record; impl.md sec. 7.5 governs scenario placement | Raise to the owner when the choice changes an outcome; otherwise proceed with the area spec that owns the seam |
| R-13 | **Upstream moves.** deepseek-harness keeps changing after commit `2e39e70` | H | L | A newer upstream behaviour is proposed in review | The pinned commit is the requirements source (README); NG-1, NG-2 | Out of scope; a later re-trace updates the ledger, not this story |
| R-14 | **.NET port divergence.** After DH-S-2 the Go and .NET cordis ports no longer match | H | L | A consumer expects parity | Port README lists each change as a harness requirement (impl.md sec. 9.2) | Owner decision; nothing in this story blocks on it |
| R-15 | **Linux cgroup prerequisite absent** on CI runners (U-K1-1) | M | L | Exact-ownership tests cannot obtain a delegated scope | EN-5: fall back to process groups and report it | Record the fallback in the evidence record; exact ownership is proven later in DH-S-18 |

---

## 7. Assumptions and dependencies

| ID | Assumption | If false |
|---|---|---|
| A-1 | The story branch may hold the three modules and the gate runner before `main` does; merging to `main` happens per closed stage | Merge cadence changes; constraints unchanged |
| A-2 | GitHub-hosted runners for Linux, macOS and Windows are available to the `harness` job | DH-S-1 cannot close (its exit evidence needs three OSes) |
| A-3 | An operator can supply a DeepSeek API key at run time for the DH-S-9 and DH-S-11 manual checks | M1 is built but its manual check is recorded as *not run*; the stage does not close (EV-3) |
| A-4 | Third-party modules named in the library specs resolve together at `go 1.26.0` (fact F-1) | R-05 response |
| A-5 | `tools/harness-trace verify` stays green as deliverable status moves to *built* (the ledger tracks dispositions, not build status) | Ledger tooling change is a separate item, raised per DC-2 |

External dependencies of this story: the Go 1.26 toolchain; `modernc.org/sqlite`
v1.62.0 with `modernc.org/libc` v1.80.1 (or the ncruces fallback); the YAML, expression
and watcher modules named in their library specs; GitHub Actions; the DeepSeek
Messages endpoint (manual checks only).

---

## 8. Coverage of the story description

| Story element | Where this document handles it |
|---|---|
| "clone deepseek harness" | sec. 1.1 (Go re-implementation, not a fork), sec. 1.2, NG-1, NG-2, R-13 |
| "given context in docs\harness" | Source-document list (header), sec. 1.1, DC-2, DC-3, R-12 |
| "design and impl doc" | sec. 2.1 (stage table from impl.md), sec. 4 (non-goals from design.md sec. 3.3), sec. 5 (constraints from design.md and owner ADRs) |
| "follow impl" | sec. 1.1, sec. 2.3 (step-level done), EV-1..7 (impl.md sec. 1.4 rules), DC-1 |
| 13 points | sec. 1.1 (M1 cut), R-01, open question `dsh-story-slice` |
