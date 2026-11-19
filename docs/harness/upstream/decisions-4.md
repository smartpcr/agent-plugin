# Upstream decisions, part 4: feature, process, simplification and testing notes

> **Status: design only, first verdicts.** This document classifies 116 of upstream's
> 464 current decision notes. Nothing described here was built, run or tested. Upstream
> facts are read at the commit recorded in [../README.md](../README.md) and cited as
> `path:line` relative to the upstream root.
>
> The verdict column is the first classification, made before the owner's decisions of
> 2026-11-18 ([../design.md](../design.md) §7.1) and before the area specifications
> existed. The final disposition of each note is the ledger row written by the area
> that owns it ([../trace/README.md](../trace/README.md)); where that row differs from
> a verdict here, the ledger governs. The section "Open questions for the area owners"
> was brought up to date with the owner's decisions on 2026-11-19.

## What an agent note is

Upstream deepseek-harness records each lasting design decision in a file it calls an
**Agent Note**: "the *why* and *what we gave up*, the parts code and docs can't carry"
(`.agents/notes/README.md:5`). A note's path encodes two things
(`.agents/notes/README.md:9`):

- **Lifecycle**, the top-level folder. `implemented/` means the decision shipped and the
  note is kept current with the code. `proposed/` means it was reviewed but is not
  built, or only partly. `rejected/` means it was considered and declined, and is kept
  only while its reasoning prevents a tempting mistake
  (`.agents/notes/README.md:12-14`).
- **Class**, the nested folder. Upstream calls it the class; the Go ledger calls it the
  note's category. This batch holds four of the six classes
  (`.agents/notes/README.md:27-32`):

| Class | Upstream's definition | Notes here |
|---|---|---:|
| `feature` | A new user- or model-facing capability | 13 |
| `process` | Tooling, policy or workflow around the code, "not runtime behavior" | 47 |
| `simplification` | Removes code, behaviour or surface area without adding a capability | 36 |
| `testing` | Test infrastructure and strategy | 20 |

The notes matter to the Go redesign because they hold the reasons behind upstream's
structure. [DH-ADR-2](../design.md#dh-adr-2) makes upstream the source of requirements
and not a contract, so each decision has to be judged: keep it, reach the same goal
another way, or drop it. The traceability check ([DH-ADR-6](../design.md#dh-adr-6))
requires a verdict for every note.

This batch differs from the architecture notes in [decisions-1.md](decisions-1.md) in
one way that shapes the counts: 47 of its notes are in the `process` class, which by
upstream's own definition is not about runtime behaviour. Most of the `not-applicable`
verdicts come from there.

## How to read the tables

Each row is one note. The columns are:

| Column | Meaning |
|---|---|
| # | Position of the note in the assignment file `trace/assignments/D4.jsonl`. It is only a handle for cross-references inside this document. |
| Note | The note's title, linked to the note at the pinned upstream commit. |
| Summary | The decision in one or two plain sentences. For a proposed or rejected note it starts with "Proposal:" or "Rejected proposal:". |
| Kind | What sort of decision it is, judged from its content: product architecture, product feature, bug fix, simplification, testing practice, development process or documentation. This is independent of upstream's class. |
| Area | The upstream path the decision is mainly about. "(cross-cutting)" means it applies across the product or is about process. |
| First verdict | The ledger disposition proposed for the Go redesign, with any global decision it rests on. |
| Why | The reason for the verdict. |

The verdicts are the ledger dispositions defined in [../trace/README.md](../trace/README.md):

| Verdict | Meaning here |
|---|---|
| **ported** | The Go design should keep this decision, expressed in Go. |
| **replaced** | The goal stands, but the Go design reaches it by a different mechanism. The link names the global decision that forces the change: [DH-ADR-2](../design.md#dh-adr-2) (Go-native formats and package boundaries), [DH-ADR-3](../design.md#dh-adr-3) (HTML rendered by the Go host instead of the React client), [DH-ADR-4](../design.md#dh-adr-4) (compiled-in components and plugin processes instead of npm plugin loading and hot reload) or [DH-ADR-5](../design.md#dh-adr-5) (the Go cordis port and one library specification per third-party module, instead of vendored source and npm dependencies). |
| **dropped** | The Go design should not do this. The link names the decision that explains why. |
| **not-applicable** | The note is about upstream's development process, repository mechanics, TypeScript or npm tooling, documentation process, review or continuous-integration (CI) practice. The "Why" says which. This verdict is final. |

**These are first verdicts.** A `ported`, `replaced` or `dropped` verdict here is a
recommendation. The agent that designs the affected area writes the final row and links
it to the deliverable or decision that carries it. A `ported` row with no link is
therefore expected, not an omission. Inference: where a "Why" says how Go would do
something, that is this document's reasoning, not something read from upstream.

Three reading rules were applied to every note:

1. **A `process` note is not automatically not-applicable.** The test is the subject. If
   the subject is upstream's repository, its CI runners, its review flow, npm or the
   TypeScript compiler, the verdict is `not-applicable`. If the subject is something the
   Go product also ships or must guarantee (a signed release, a licence disclosure, a
   reviewed change to a persisted format, what the default product contains, a
   generated command reference), the note gets a product verdict even though upstream
   filed it under `process`. Fifteen `process` notes fall in the second group.
2. **For a proposed note** the verdict answers "should the Go design adopt the idea?".
   `ported` means adopt it. Several proposals only remove an unused upstream API; for
   those, "adopt" means the Go design does not build that API in the first place, and
   the "Why" says "adopt by omission".
3. **For a rejected note** the verdict answers "does upstream's rejection still hold in
   Go?". `ported` means the rejection holds, so the Go design keeps what upstream kept.
   `replaced` means the goal behind the rejection holds but its argument or mechanism
   does not transfer.

## Counts

By first verdict and lifecycle:

| Verdict | Implemented | Proposed | Rejected | Total |
|---|---:|---:|---:|---:|
| ported | 36 | 13 | 3 | 52 |
| replaced | 18 | 3 | 3 | 24 |
| dropped | 0 | 1 | 0 | 1 |
| not-applicable | 31 | 7 | 1 | 39 |
| **Total** | **85** | **24** | **7** | **116** |

Of the 24 `replaced` rows, 10 rest on DH-ADR-3 (the user interface and the desktop
shell), 6 on DH-ADR-4 (plugins and packaging), 4 on DH-ADR-2 (Go-native formats and
package boundaries) and 4 on DH-ADR-5 (dependencies). The one `dropped` row rests on
DH-ADR-3. One `ported` row (#97) carries a link to DH-ADR-4 because that global decision
already adopts the proposal.

By upstream class and first verdict:

| Class | ported | replaced | dropped | not-applicable | Total |
|---|---:|---:|---:|---:|---:|
| feature | 9 | 3 | 1 | 0 | 13 |
| process | 4 | 11 | 0 | 32 | 47 |
| simplification | 30 | 3 | 0 | 3 | 36 |
| testing | 9 | 7 | 0 | 4 | 20 |
| **Total** | **52** | **24** | **1** | **39** | **116** |

By kind of decision:

| Kind | Notes |
|---|---:|
| development process | 40 |
| simplification | 30 |
| testing practice | 18 |
| product architecture | 15 |
| product feature | 10 |
| documentation | 3 |
| bug fix | 0 |
| **Total** | **116** |

Why the 39 `not-applicable` notes are not applicable:

| Cause | Notes | Rows |
|---|---:|---|
| npm, TypeScript, JavaScript or Node tooling | 19 | #21, #23, #34, #35, #36, #37, #39, #41, #42, #43, #44, #45, #46, #47, #60, #89, #91, #105, #115 |
| CI runner and workflow practice | 5 | #11, #12, #18, #24, #25 |
| Review, merge and issue-tracker practice | 5 | #17, #19, #20, #48, #92 |
| Upstream test maintenance or test tooling | 4 | #79, #80, #81, #109 |
| Documentation and writing process | 3 | #14, #22, #94 |
| Repository mechanics | 2 | #15, #93 |
| Upstream build process | 1 | #32 |

## Category: feature

Upstream's `feature` class: thirteen notes, ten implemented and three proposed.

### Implemented upstream (10 notes)

| # | Note | Summary | Kind | Area | First verdict | Why |
|---:|---|---|---|---|---|---|
| 1 | [Shipped filesystem mutation defaults][n1] | The `apply_patch` file-editing tool is on by default in the base profiles, and the read-before-write guard (the filesystem observation policy, which refuses a write to a file the model has not read or that changed since the read) ships switched off; a deployment turns it back on with one setting. | product feature | `packages/fs/fs-observation-policy` | **ported** | These are shipped defaults chosen from benchmark runs, not a JavaScript mechanism. The Go default configuration should start from the same defaults and keep the guard as an optional component; the filesystem-tools area confirms the values. |
| 2 | [LiveShare plugin method catalog][n2] | The list of plugin methods a share may expose to a viewer is read from the host's live method registry instead of being declared by each plugin. A share stores only per-method policies, and the host intersects each policy with that list before a call is dispatched; core methods and LiveShare's own methods are never in the list. | product architecture | `packages/experimental/liveshare` | **ported** | One source of truth for what a share can grant closes the gap where a plugin's own declaration widened a viewer's reach. The rule is independent of the language: the Go host enumerates its own registered remote methods in place of the upstream Typert registry. |
| 3 | [Pull request comment drafts in the code host's pending review][n3] | A pull request comment the user keeps for later is stored in the user's pending review on the code host, not in the browser. The changes view shows pending comments with the host's threads, and every write re-checks the head commit and is never retried. | product feature | `packages/experimental/client-ui-forge` | **ported** | Keeping drafts on the code host removes a second store and makes drafts started elsewhere visible. It also suits a server-rendered UI, which has no browser-side store; the small remainder upstream keeps in the browser (review summary, publication records) needs a host-side home that the forge area must choose. |
| 4 | [Session panel in the Chat View's end gutter][n4] | Facts about the current session (project directory, change requests, outputs) are shown in one floating panel beside the transcript. The panel docks only while the gutter next to the transcript is wide enough; without room, the header button opens it as a temporary overlay. | product feature | `packages/client/ui-session-panel` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The goal, one home for session facts that does not take width from the transcript, stands. Upstream reaches it with React slots and a gutter width published by the Chat View; the Go host renders the panel as an HTML fragment. A small script is still needed, because the note itself records that docked and overlay differ in behaviour (focus, Escape, outside press) and not only in size. |
| 5 | [Import DeepSeek website chat exports][n5] | An experimental plugin imports the ZIP produced by the DeepSeek website's "export all chats" action. The host receives the upload, turns the most recent conversations into stored sessions in a chosen workspace one at a time, and streams progress; the user can then continue any of them. | product feature | `packages/experimental/deepseek-chat-import` | **ported** | The import is host-side work on a documented export format and is in scope under DH-ADR-1. The upload dialog becomes a plain HTML form with a progress stream; the mapping rules from export to session events must be restated for the Go session format by the area that owns it. |
| 6 | [Forge provider command rules][n6] | Each code-host provider registers a declarative rule that describes its command-line tool (`gh`, `glab`): which subcommands create or reference a change request and how to map a parsed command to a URL. The shared package keeps the tokenizer and the projection, and the projection's version is derived from the set of registered rules so that a checkpoint built under other rules is discarded. | product architecture | `packages/experimental/forge-session` | **ported** | Declarative, pure rules keep recognition deterministic for replay and let a new code host be added without editing the shared package. Both properties are wanted in Go, where each provider is a compiled-in component that registers its rule. |
| 7 | [Code review page][n7] | A Code review page lists the open change requests that ask for the user's review, that the user created, or that involve the user, and shows one of them in full without needing a session. The lists are read only while the page is open, and only the shown change request is polled. | product feature | `packages/experimental/client-ui-forge` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The feature and its host-side rules (optional provider capability, one search per list, reads by URL limited to callers allowed to use the host's code-host credentials) stand. The page itself is rendered by the Go host instead of being a React page. |
| 8 | [one host claims the LiveShare owner state][n8] | When two host processes start on the same home directory, exactly one claims the LiveShare owner state by writing a small claim file. The holder registers and changes shares; every other host can read them, and its attempts to change them get a typed refusal instead of silently stealing the relay registrations. | product architecture | `packages/experimental/liveshare` | **ported** | Two Go host processes on one home directory have the same problem, and the rules (no expiry, contention is a typed answer, a claim left by a dead process is reclaimed) are the ones the session write lease already uses. |
| 9 | [Live subagent model-selection setting][n9] | The list of models a subagent may be routed to is read from the live setting at each call, instead of being copied into each session when it is created. A change to the setting applies to the next call in every running, restored and child session. | product feature | `packages/subagent/tool-subagent` | **ported** | A setting that takes effect immediately is easier to explain than one whose meaning depends on when a session was created. The Go design keeps the live read and, because it does not read upstream session logs (DH-ADR-2), does not need the retired session event upstream still declares. |
| 10 | [The sidebar scrollbar's delay and fade live in a CSS transition][n10] | The sidebar scrollbar thumb stays visible for 500 ms after the pointer leaves and then fades over 200 ms. The delay and fade are a CSS transition on a registered custom property, not a JavaScript timer, and reduced-motion users get the hold followed by an instant switch. | product feature | `packages/client/ui-sidebar` | **ported** | Expressing timing in CSS is exactly what a server-rendered UI with little JavaScript wants, and the technique does not depend on React. The Go sidebar can carry the same style rules. |

### Proposed upstream (3 notes)

| # | Note | Summary | Kind | Area | First verdict | Why |
|---:|---|---|---|---|---|---|
| 86 | [Event-triggered automation tasks][n86] | Proposal: an automation task becomes an instruction plus a source and a session mode, and time is only one source. A source provider (code, no model) reports changes; a shared trigger compares each observation with stored state and filters it, still without a model call; only a change that passes is delivered to a session, where the event content is quoted as data from the source and not as the user's words. | product feature | `packages/schedule/schedule` | **ported** | Adopt the layering, not necessarily the build order. The two problems it solves, a model turn per poll and untrusted event text reaching the model as if the user wrote it, are independent of the language, and source providers fit compiled-in components or plugin processes. The schedule area should shape task storage so that time is one source among several, and may give the event sources a later priority. |
| 87 | [Chat history prefetch and safe consumption][n87] | Proposal: older chat history is fetched ahead of need but shown only at a safe moment. The client holds at most one fetched page, and the chat view decides when to insert it, so a page that arrives during a touch drag or an animation does not make the transcript jump. | product feature | `packages/client/ui-chat` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | Adopt the behaviour: fetch earlier history before the reader reaches the top, and never move what they are reading. The mechanism changes because the page is an HTML fragment fetched from the Go host (DH-ADR-3); a small script island must hold the fragment and insert it with the reading position preserved. |
| 88 | [Chat history consumption classes and interactions][n88] | Proposal: the class-level design behind history prefetch, saying which client object owns the network request, which owns the decision to show the page, and how they call each other. | product architecture | `packages/api/session-controller` | **dropped** ([DH-ADR-3](../design.md#dh-adr-3)) | The Go design should not carry this over: it describes interactions between client-side session and chat classes that do not exist when the Go host renders the HTML (DH-ADR-3). The behaviour it implements is kept through the companion note on history prefetch. |

## Category: process

Upstream's `process` class: forty-seven notes, forty implemented and seven proposed.
Thirty-two are `not-applicable`; the other fifteen are the ones whose subject the Go
product shares (reading rule 1 above).

### Implemented upstream (40 notes)

| # | Note | Summary | Kind | Area | First verdict | Why |
|---:|---|---|---|---|---|---|
| 11 | [Serial cross-platform CI reference][n11] | Pull requests run the parallel required checks; pushes to the main branch additionally run a slower serial run on standby runners as an independent reference, so a mistake in the parallel job inventory cannot hide skipped work. | development process | (cross-cutting) | **not-applicable** | Continuous-integration (CI) practice of the upstream repository: it describes GitHub workflow topology and runner pools, not product behaviour. |
| 12 | [CI failover runbook — hosted pools → in-house pool][n12] | A runbook for moving required CI jobs from the hosted runner pools to an in-house pool by setting a repository variable, per platform, without a merge. | development process | (cross-cutting) | **not-applicable** | CI practice of the upstream repository: runner routing for its GitHub workflows. |
| 13 | [Prefer maintained dependencies over hand-rolling][n13] | Replacing hand-written infrastructure with a well-maintained external package is a legitimate simplification when it truly shrinks what the project owns, the package is healthy, and it fits the contract at the boundary. Settled seams are not reopened by this policy. | development process | (cross-cutting) | **replaced** ([DH-ADR-5](../design.md#dh-adr-5)) | The Go design needs a dependency policy too, but sets its own: the framework module uses the standard library only, and each third-party module in the harness module is justified, verified and licensed in a library specification (DH-ADR-5). Upstream's bar (net deletion, health, fit) is a useful checklist for those specifications. |
| 14 | [Freeze low-future-value Agent Notes outside the active corpus][n14] | Implemented notes of low future value are moved to a frozen archive or deleted, so that only notes worth keeping current remain in the active set. | documentation | (cross-cutting) | **not-applicable** | Upstream documentation process: it governs how upstream maintains its own decision notes and their translations. |
| 15 | [Make Lefthook installation worktree-local][n15] | Git hooks are installed per worktree, so installing dependencies in one worktree cannot rewrite the hooks used by another. | development process | `scripts` | **not-applicable** | Repository mechanics: Lefthook installation triggered by the npm `postinstall` step. |
| 16 | [Generated third-party notices][n16] | The third-party notices file is generated from the dependency manifests and lock files, and a test fails when the committed file is stale, so the licence disclosure cannot drift from the real dependencies. | development process | `scripts` | **replaced** ([DH-ADR-5](../design.md#dh-adr-5)) | The obligation to disclose third-party software applies to the Go binary as well. The mechanism changes: each Go module is recorded with its version and licence in a library row that the trace tool checks (DH-ADR-5), and a notices file can be generated from `go.mod` at release. The generator described here reads npm manifests and does not carry over. |
| 17 | [Native GitHub stacks and optional PR rebases][n17] | Chains of dependent pull requests use GitHub's native stack object and are landed with the stack command; rebasing a reviewed branch is allowed. | development process | (cross-cutting) | **not-applicable** | Review and merge practice of the upstream repository. |
| 18 | [Wine and native Windows CI][n18] | Every pull request runs build, coverage and native tests on real Windows, because Wine on Linux cannot prove NTFS, access-control, pseudo-terminal or process behaviour; the Wine run stays as a post-merge check. | development process | (cross-cutting) | **not-applicable** | CI practice. The lesson for Go is only that Windows behaviour must be tested on real Windows, which the Go design states as real-environment scenarios in the areas concerned. |
| 19 | [Unified GitHub label taxonomy][n19] | Every pull request carries exactly one `kind/*` label and at least one `area/*` label from a fixed taxonomy. | development process | (cross-cutting) | **not-applicable** | Review practice: GitHub label policy of the upstream repository. |
| 20 | [Event-directed PR review status commands][n20] | Review webhooks are treated as commands that move an issue between "In review" and "In progress" on the project board. | development process | (cross-cutting) | **not-applicable** | Review practice: issue-tracker automation of the upstream repository. |
| 21 | [Private npm publication as three independent sequences][n21] | The product packages, the vendored cordis packages and the native packages are published to npm as three independent version sequences with separate workflows and tags. | development process | (cross-cutting) | **not-applicable** | npm tooling: publication of npm packages. The Go design ships Go modules and binaries, and the two Go modules are versioned under DH-ADR-5. |
| 22 | [Ban an ambiguous origin label][n22] | One overloaded word for "origin" is banned from all maintained text and identifiers, and a check enforces it; each use must name the exact concept instead. | development process | (cross-cutting) | **not-applicable** | Upstream writing and naming policy enforced by a repository script. The Go documents follow their own plain-English rule to name the exact concept. |
| 23 | [Published dependency faces and bounded peer relays][n23] | A package that has both a browser part and a host part declares its dependencies by which part uses them, so that installing the published command-line tool does not make npm resolve deep chains of peer dependencies. | development process | `scripts` | **not-applicable** | npm tooling: the note exists because of npm peer-dependency resolution cost. A Go binary has no install-time dependency resolution. |
| 24 | [Windows Python runtime CI stays on GitHub-hosted Windows][n24] | The Windows build of the Python SDK runtime always runs on GitHub-hosted Windows runners and no longer follows the failover switch to the in-house pool. | development process | (cross-cutting) | **not-applicable** | CI practice: runner selection for one upstream workflow. |
| 25 | [Blacksmith as a failover leg for CI pools][n25] | A third-party runner provider can be selected as a CI failover target by a repository variable, never as the default. | development process | (cross-cutting) | **not-applicable** | CI practice: runner routing for the upstream GitHub workflows. |
| 26 | [Parallel macOS notarization from isolated App copies][n26] | The macOS desktop release signs one application bundle, makes two isolated copies, and notarizes the update archive and the disk image in parallel, because stapling modifies the bundle and the two Apple submissions would otherwise run one after the other. | development process | `apps/desktop` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | A macOS desktop release still has to be signed and notarized, so the goal stands. The steps described are specific to electron-builder; the desktop shell is a Go webview application (DH-ADR-3) with its own packaging, and the desktop area decides whether two parallel submissions are still worth the complexity. |
| 27 | [In-tree persistence-type history][n27] | The types of all persisted session events are normalized into one model with a digest per root type. A change to any nested type changes the digest, and the change must be acknowledged in a committed persistence-change record before it can merge. | development process | `docs/persistence-changes` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2)) | The goal stands for any durable format: a change to what is written to disk must not land unnoticed. The mechanism is tied to the TypeScript compiler's type model. Inference: the Go design can reach it with a committed schema snapshot generated from the Go event types and compared in a test; the session area decides. |
| 28 | [Keep experimental packages outside the default product][n28] | A check proves that nothing in the default product depends on, imports, or loads by configuration any experimental package; the bundles the launcher names as optional are the one declared exception. | development process | `scripts` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | The rule stands: an experimental capability must not be part of the default product by accident. Upstream follows npm dependency edges and configuration files; with compiled-in components (DH-ADR-4) the Go check is that the default configuration enables no experimental component and that the default build does not import one. |
| 29 | [Generate the CLI reference from command help][n29] | The command-line reference is generated by capturing the help text of the launcher, the plugin forwarder and every shipped profile, and following each listed subcommand, so it cannot omit a command or keep an old default. | documentation | `apps/cli` | **ported** | A reference generated from the real command tree is equally easy in Go and removes the same drift. The command-line area owns the generator. |
| 30 | [Ship optional bundles with the installation][n30] | The installation ships a named list of optional bundles (official but experimental layers such as Agent Teams) that are switched off until the user turns them on in the plugin page; they can never be removed, and they are the one declared exception to the rule that the default product contains nothing experimental. | product feature | `packages/util/official-bundles` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | The goal stands: a user can switch on an official optional capability without finding and installing it by name. With compiled-in components (DH-ADR-4) the capability is already in the binary, so "shipped but off" becomes a component that the default configuration leaves disabled and the plugin page can enable; the npm runtime-dependency mechanics do not carry over. |
| 31 | [Persistence schema policy and review][n31] | The persistence schema inventory records, beside each affected property, the compatibility promise a reader makes for it (for example, that unknown kinds are preserved), so a reviewer can tell a declared promise from an assumption. Promises are declared in source annotations and must be established and tested by the runtime owner first. | development process | `scripts` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2)) | Recording reader promises next to the persisted field is worth keeping for the Go session format, but the mechanism here is an extractor over TypeScript declarations and comment annotations. Inference: Go struct tags or a hand-written schema file can carry the same promises; the session area decides the form. |
| 32 | [Windows runtime signature cache][n32] | Windows packaging caches the signed copies of unchanged bundled runtime files (Python, LibreOffice), keyed by content, certificate and signing tool, so that a build does not repeat hardware-token signing and timestamping for files that did not change. | development process | `apps/desktop/windows-signing` | **not-applicable** | Upstream build process: a speed optimisation of upstream's Windows packaging pipeline for the third-party runtimes it re-signs on every build. It carries no product behaviour; a Go packaging pipeline can add a cache later if it re-signs the same files. |
| 33 | [Separate Windows hardware signing from timestamp completion][n33] | Windows code signing is done in two separate phases: sign a private copy once with the hardware token and no timestamp, then add the timestamp without credentials, retrying only the timestamp at most three times. A hardware or verification failure keeps the interlock and is never recovered automatically. | development process | `apps/desktop/windows-signing` | **ported** | A Go Windows binary and installer are signed with the same tool, certificate and hardware token, so the failure analysis holds unchanged: a timestamp-server error must not cause a second use of the private key. The desktop and release area should keep the two-phase rule. |
| 34 | [Prohibit new assertions to unknown][n34] | New type assertions through `unknown` are forbidden in first-party code; existing ones are held in a baseline that may only shrink. | development process | `scripts` | **not-applicable** | TypeScript tooling: the rule is about a TypeScript type-assertion form that has no Go counterpart. |
| 35 | [Reject silent empty catch clauses][n35] | An empty `catch` clause must name the error, say why discarding it is safe, and wrap a single statement; a checker with a shrinking baseline enforces the form. | development process | `scripts` | **not-applicable** | TypeScript tooling: a syntax checker for JavaScript `catch` clauses. Go has no exceptions; the matching Go habit (never discard an error without a stated reason) is a lint setting, not a design decision. |
| 36 | [Dual-release install layout work budget][n36] | The check that installs two releases side by side with npm decides pass or fail by a work budget computed from the dependency graph, not by a wall-clock timeout that depends on the runner. | development process | `scripts` | **not-applicable** | npm tooling: it measures npm's dependency resolution of the published packages. |
| 37 | [PR artifact builds and aggregate TypeScript checks][n37] | Pull-request jobs that only need compiled artifacts build them without re-running the repository-wide TypeScript checks for tests and scripts, which the required build jobs still run. | development process | `scripts` | **not-applicable** | TypeScript and CI tooling: scheduling of `tsc` programs in upstream's workflows. |
| 38 | [Require product use or an explicit package classification][n38] | Every non-experimental package must either be used by a shipped profile, a runtime import or an optional capability, or carry an explicit classification (optional integration, SDK, build tool, test tool and so on) that records its role. A check fails on a package that is neither. | development process | `scripts` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | The goal, no dormant component that ships without a stated role, stands, and Go's compiler does not catch a component that is registered but never configured. Inference: the Go check compares the registry of compiled-in components (DH-ADR-4) with the shipped configurations, instead of walking npm packages. |
| 39 | [Published source, maps, and documentation][n39] | Every published npm package includes its TypeScript source, source maps and declaration maps, and bundles are compressed without renaming identifiers, so a plugin author can read the implementation and a stack trace names the original code. | development process | `scripts` | **not-applicable** | npm tooling: the content of published npm tarballs. A Go module is published as source by construction. |
| 40 | [Session versions follow reader compatibility][n40] | The session format version number changes only when existing readers would misread the new data. A change keeps the same version when older readers resume correctly, safely ignore an optional feature while preserving its records, or refuse a required feature before they execute, change or repair anything. | product architecture | `packages/session/session-format` | **ported** | The criterion (reader behaviour, not structural difference) is independent of the language and avoids a version bump and migration for every optional field. The Go session format should adopt it from its first release; "already released readers" then means released Go binaries. |
| 41 | [Split build:artifacts and build:check, with build running both concurrently][n41] | The build is split into a command that produces artifacts and a command that only type-checks the aggregate test and script programs; the full build runs both concurrently. | development process | `scripts` | **not-applicable** | TypeScript tooling: scheduling of `tsc` and the bundler. `go build` and `go vet` have no such split to manage. |
| 42 | [Build speed-ups that keep outputs unchanged][n42] | Eight changes that make a cold build faster without changing any output byte or diagnostic, such as declaring React type packages only at the root and making the remote-method generator parse package configuration lazily. | development process | `scripts` | **not-applicable** | TypeScript and npm tooling: all eight items concern `tsc`, pnpm symlinks or upstream's code generator. |
| 43 | [Local gate, hook, and test speed-ups][n43] | Eight changes that shorten local Git hooks, checks and test runs, such as generating declarations without bundling and capping the default number of local workers. | development process | `scripts` | **not-applicable** | Upstream development process: hook and check scheduling for its TypeScript toolchain. |
| 44 | [Experimental packages follow the same dependency declaration rules][n44] | Experimental packages follow the same rules as release packages for which npm dependency section a dependency belongs in; the group-wide exemption is removed. | development process | `scripts` | **not-applicable** | npm tooling: `dependencies`, `devDependencies` and `peerDependencies` placement. |
| 45 | [Package sources must not use top-level await][n45] | Package source may not use top-level `await`; a child-process entry uses an explicit `main()` that reports its own failure on stderr with a fixed exit code. | development process | `scripts` | **not-applicable** | JavaScript tooling: module evaluation order is a JavaScript concern. A Go program always has an explicit `main`, so the checked rule has nothing to check; the readiness and exit-code conventions of each child process are restated by the area that owns that process. |
| 46 | [Node runtime floor and compatibility coverage][n46] | The minimum supported Node.js version is 24.9, with CI jobs pinned to the minimum and to the newest major version. | development process | (cross-cutting) | **not-applicable** | Node tooling: the runtime floor of the JavaScript product. The Go counterpart is the `go` directive of each module, set by the toolchain decisions. |
| 47 | [Generate metadata before package compilation][n47] | Remote-method metadata is generated from source before packages are compiled, instead of during bundling, so the build does not need a second dependency graph to order it. | development process | `scripts` | **not-applicable** | TypeScript tooling: placement of upstream's code-generation step in its build. |
| 48 | [Weighted approval keeps its commit status gate and publishes the decision label from an App][n48] | The weighted-approval merge gate stays a commit status, and a separate check run written by a GitHub App shows "action required" while approvals are missing. | development process | (cross-cutting) | **not-applicable** | Review practice: merge-gate automation of the upstream repository. |
| 49 | [Plugin entries must not add value exports][n49] | A plugin's entry module may export only what the loader loads (name, dependencies, config schema, apply function, or the service class) plus the service contracts and error types other packages extend or throw. A checker enforces it for both plugin forms. | product architecture | `scripts` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | The goal, a plugin whose public surface is its registration and its contracts and nothing else, stands. In Go it is reached by package layout: a compiled-in component (DH-ADR-4) exports its constructor, config type and interfaces, and keeps everything else unexported or under `internal/`, so the compiler enforces what upstream needs a checker for. |
| 50 | [Prohibit new reads of the Cordis root context][n50] | New code may not read the framework's root context. Reading a service through the root skips the declared-dependency check, a listener on the root skips scope filtering, and the root realm is not the caller's realm; each legitimate use has a declared replacement. Existing reads are held in a baseline that may only shrink. | product architecture | `vendor/cordis` | **ported** | The Go cordis port exposes the same accessor (`Context.Root()`), so the same shortcut is available to a Go component. The rule should hold from the first line of harness code, which means no baseline is needed; a test or lint over the harness module can enforce it. |

### Proposed upstream (7 notes)

| # | Note | Summary | Kind | Area | First verdict | Why |
|---:|---|---|---|---|---|---|
| 89 | [API extractor reports][n89] | Proposal: generate a checked-in public API report per package so that every change to an exported type shows up as a diff a reviewer must see. | development process | (cross-cutting) | **not-applicable** | TypeScript tooling: it proposes api-extractor or a declaration dump for npm packages, and was not built upstream. What a Go plugin author may rely on is the plugin protocol, which the stable API policy row covers. |
| 90 | [Supply chain checks and vendor drift verification][n90] | Proposal: check nightly that the vendored framework equals its upstream at the recorded commit plus the logged local changes, scan the lock file for security advisories, and assert the licence inventory. | development process | `vendor` | **replaced** ([DH-ADR-5](../design.md#dh-adr-5)) | Vendor drift disappears because the Go design builds on its own cordis port instead of vendored JavaScript source (DH-ADR-5). The other two parts still apply to Go modules: each module's version and licence is recorded and checked in a library row, and an advisory scan of the Go dependency list belongs to the toolchain decisions. |
| 91 | [Discover package inventories instead of maintaining static lists][n91] | Proposal: derive package lists from the directory layout and manifests instead of maintaining static lists, in particular the TypeScript project references. | development process | `scripts` | **not-applicable** | TypeScript and npm tooling: the lists in question are `tsconfig` references and npm workspace inventories. Go discovers packages from the module itself. |
| 92 | [Periodic human-review maintenance for dsh-code-review][n92] | Proposal: a private tool periodically scans merged pull requests for human review feedback and proposes updates to the code-review checklist used by coding agents. | development process | (cross-cutting) | **not-applicable** | Review practice of the upstream repository. |
| 93 | [Remove the packed-session fixture branch migrator][n93] | Proposal: delete the temporary command that converted session test fixtures on in-flight branches to the current layout, once no open branch needs it. | development process | `scripts` | **not-applicable** | Repository mechanics: clean-up of a one-time migration script for upstream's test fixtures. |
| 94 | [Audience-first documentation quality criteria][n94] | Proposal: define "brief, intuitive and friendly" as reviewable outcomes for upstream's documentation, because the existing checks prove structure and freshness but not that the prose still names the live mechanism. | documentation | (cross-cutting) | **not-applicable** | Upstream documentation process. The Go documents have their own writing rules in the research brief. |
| 95 | [Stable API policy][n95] | Proposal: mark individual APIs as stable in source, version each capability seam on its own, generate one API report per release, and let CI fail when a stable entry changes incompatibly without a deprecation period and a major seam version. | product architecture | (cross-cutting) | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | Plugin authors need to know what they can rely on in the Go design too. What they rely on changes: an external plugin sees only the plugin protocol and MCP (DH-ADR-4), not in-process exports, so the stability promise attaches to protocol methods and events, each capability with its own version. Source markers and an export report are not the mechanism. |

## Category: simplification

Upstream's `simplification` class: thirty-six notes, seventeen implemented, twelve
proposed and seven rejected. A simplification that shipped is usually the easiest kind
of decision to carry over, because the Go design simply starts from the simpler shape.

### Implemented upstream (17 notes)

| # | Note | Summary | Kind | Area | First verdict | Why |
|---:|---|---|---|---|---|---|
| 51 | [Unify the agent id and the session id][n51] | A live agent and its session share one identifier. There is no separate agent id to translate; the configuration keeps a stable label, and a fresh start mints the identifier from the label plus a random part. | simplification | `packages/core/agent` | **ported** | One identity for routing, event sourcing and persistence removes a mapping no production path needs. Nothing about it depends on JavaScript. |
| 52 | [Remove implicit batching from ordinary sends][n52] | Each call to send a message creates its own queue item and, if it runs, its own turn. Two sends are never silently combined into one turn because they happened to be waiting at the same moment, and the second turn starts only after the first has ended and its durability checkpoint has settled. | simplification | `packages/core/agent-loop` | **ported** | Grouping by timing made behaviour depend on scheduling accidents. Go's goroutine scheduling would make timing-based batching even less predictable, so the explicit rule is the right one to keep. |
| 53 | [ACP as an automation-only protocol][n53] | The Agent Client Protocol (ACP) bridge is an automation transport only: sessions, one prompt at a time, model configuration, MCP mounting, committed updates, cancellation and one-shot permission requests. It no longer renders editor cards, pickers or other presentation. | simplification | `packages/acp/acp` | **ported** | Keeping presentation out of an automation transport prevents a third user interface from growing beside the web UI and the terminal. The scope decision is independent of the language. |
| 54 | [Remove synthetic turns for log-only events][n54] | A plugin that records a log-only event while no turn is running appends it directly to the session and, if it promised durability, flushes explicitly. No artificial turn is opened just to get a durability checkpoint. | simplification | `packages/core/session` | **ported** | A turn identifier should mean one model-loop execution and nothing else. The Go session log can allow extension events between turns in the same way. |
| 55 | [Explicit Schedule time-zone boundary][n55] | The browser's time zone is sampled with every prompt and logged on that user message; it is never stored as session state. The schedule tool accepts only a time with an explicit offset or an explicit zone, and the model is told to ask the user when the zone is mixed or missing. | simplification | `packages/schedule/schedule` | **ported** | Making the zone explicit removed session headers, migrations and a confirmation protocol. The rule carries over; the server-rendered UI needs one line of script to put the browser's zone into the prompt form. |
| 56 | [JSONL-only first-party Session persistence][n56] | The project owns and tests one authoritative physical session store. The storage contract stays neutral so that someone else could implement another backend, but a second first-party backend is not maintained. The full-text search index and the key-value store are separate, rebuildable or unrelated data. | simplification | `packages/session/session-persistence-jsonl` | **ported** | One authoritative store halves every contract, recovery and migration test matrix. The Go design should keep "one first-party store behind one interface"; which physical format that store uses is free under DH-ADR-2. Note that the note says the SQLite provider is absent, while the pinned checkout still contains `packages/session/session-persistence-sqlite`; the session area must settle which is current. |
| 57 | [Keep Loader mutations non-transactional][n57] | The plugin loader applies a configuration change eagerly and does not roll back: a failed edit can leave a partly applied tree, failures are logged, and each application consumer (command-line start-up, directory chooser, preset mounting) checks for itself that what it mounted is ready. A transactional reload was built and then reverted. | simplification | `vendor/cordis` | **ported** | Keeping the loader eager and making consumers own their completion checks is the simpler contract. One upstream reason no longer applies: the revert was partly to stay close to the pinned vendored sources, and the Go port is owned code. The loader area should therefore confirm this verdict and must describe what a half-applied reload looks like to an operator. |
| 58 | [Remove the E2B execution providers][n58] | The three providers that ran files, commands and terminals in an E2B cloud sandbox were removed. Remote execution stays an intended capability: the filesystem and subprocess contracts remain asynchronous and provider-neutral so that a remote backend can be added without changing tools. | simplification | `packages/subprocess` | **ported** | The Go design should likewise ship no E2B backend and keep the contracts remote-capable, which the SSH providers already require. The removal still holds in Go: the blocker was a missing control channel in the vendor SDK, not anything in JavaScript. |
| 59 | [Unified subagent activations][n59] | Every way of running a subagent goes through one entry point that returns a handle with the child's identity, a result that settles only after cleanup, and disposal of exactly that activation. Local providers only contribute preparation data; the subagent service owns agent creation and lifetime. | simplification | `packages/subagent/subagent` | **ported** | One owner for creation, cancellation, result and cleanup removes the separate paths that used to disagree. In Go the handle maps onto a value with a context for cancellation and a result that is delivered after cleanup. |
| 60 | [Remove the Desktop production profile core-package cleanup][n60] | The desktop application no longer deletes or prunes packages in the profile's `node_modules` before starting the host; resolution order alone decides which copy of a package runs. | simplification | `apps/desktop` | **not-applicable** | npm tooling: the note is about `node_modules`, pnpm overrides and lock files inside a profile. A Go profile holds configuration only, because capabilities are compiled in or run as plugin processes. |
| 61 | [One tool invocation form per agent][n61] | Each agent offers its tools to the model in exactly one form: either as native function schemas, or as a single `run_code` tool with a generated SDK (programmatic tool calling, PTC). Offering both at once is rejected in configuration. | simplification | `packages/core/agent-tool-presentation` | **ported** | The mixed form multiplied schema ordering, admission and test combinations without adding a capability. The rule is a configuration constraint and carries over unchanged. |
| 62 | [Preset-owned subagent composition][n62] | A subagent's persona and tool set come from the Agent Preset it selects, not from separate persona and tool-filter settings on the subagent tool. The removed settings are rejected at load instead of being silently ignored. | simplification | `packages/subagent/tool-subagent` | **ported** | One place to compose an agent is easier to reason about than two. The Go design keeps preset-owned composition and the rule that an unknown configuration key is an error; it does not need the parsing upstream retains for child descriptors written by older releases (DH-ADR-2). |
| 63 | [Recognize message quotes from Markdown text][n63] | A quoted passage in a user message is ordinary Markdown blockquote text. The UI recognises it by matching the text; no separate quote index is stored with the message, so a hand-typed quote and a quote made with the composer behave the same. | simplification | `packages/session/message-quotes` | **ported** | Deriving presentation from the text removes durable metadata that had to be kept in step across prompt, echo and queue. A server-rendered UI benefits directly: the Go renderer recognises the blockquote when it renders the message. |
| 64 | [Inbox-only Agent waits][n64] | The `wait_agent` tool ends only when a message arrives in its caller's own inbox (or on timeout or cancellation). Task-board edits and other agents' activity do not wake it; a caller who wants a team member to act sends an explicit message. | simplification | `packages/subagent/tool-subagent-control` | **ported** | A wait that ends without a message leaves the model with nothing to act on. The narrower rule also keeps team coordination out of ordinary subagent controls, and maps directly onto a Go channel or subscription on one inbox. |
| 65 | [Team projection retains only tasks and mode][n65] | The team state derived from the session log holds only the task list and the saved mode. Older member and mailbox records are ignored instead of being replayed through retired validators that could fail the current task board. | simplification | `packages/team/agent-team` | **ported** | The Go team projection should contain only tasks and mode from the start. The other half of the note, ignoring historical member and message records, has nothing to ignore in Go because upstream session logs are not read (DH-ADR-2). |
| 66 | [Expose DeepSeek deferred-only request failures][n66] | When every tool in a DeepSeek request is declared as deferred, the adapter sends the request unchanged and reports the provider's rejection. It no longer inserts a placeholder tool to make the request acceptable. | simplification | `packages/llm/llm-deepseek` | **ported** | A placeholder hid invalid tool-update state, changed the cached request prefix and advertised a tool nothing could execute. Surfacing the provider error is the honest behaviour in any language. |
| 67 | [TypeScript-only PTC tool execution][n67] | Programmatic tool calling accepts programs in one language only, TypeScript. Python rendering, language dispatch and Python-specific naming restrictions were removed; the runtime service itself stays independent of tools. | simplification | `packages/core/tool-executor-ptc` | **ported** | Supporting one program language keeps one SDK generator, one set of naming rules and one test matrix. The Go design should also commit to a single language. Inference: TypeScript stays the natural choice because the model writes it, but the Go host then has to run it in a separate runtime process, which the PTC area must specify. |

### Proposed upstream (12 notes)

| # | Note | Summary | Kind | Area | First verdict | Why |
|---:|---|---|---|---|---|---|
| 96 | [Defer Office background scheduling until a consumer needs it][n96] | Proposal: remove foreground and background priorities from the Office document conversion queue, because nothing in the product ever submits a background request; keep the queue's general resource limits. | simplification | `packages/document/office-kit` | **ported** | Adopt: the Go conversion queue should be built with resource limits only, and gain priorities when a real background consumer exists. Building the unused scheduling machinery again would repeat the debt the note identifies. |
| 97 | [Limit DSH HMR to live profile configuration][n97] | Proposal: keep live refresh of profile configuration, and remove replacement of running JavaScript modules; a source or framework change requires a process restart. | simplification | `packages/boot/hmr` | **ported** ([DH-ADR-4](../design.md#dh-adr-4)) | Adopt: this is where the Go design already stands. Go does not load code into a running process the way Node loads a module, so code replacement becomes a restart of the plugin process (DH-ADR-4), while watching and re-applying configuration remains a loader feature. |
| 98 | [Read instruction files only through a filesystem provider][n98] | Proposal: instruction files (such as `AGENTS.md`) are found and read only through the filesystem provider; the second code path that reads the local disk directly, used only by tests, is removed. | simplification | `packages/context/agent-instructions` | **ported** | Adopt: one read path through the filesystem interface means instruction discovery works the same for local, sandboxed and SSH workspaces, and needs one set of error and cancellation tests. |
| 99 | [Prune unused stream visibility helpers][n99] | Proposal: remove five exported helper functions that classify whether a streamed chunk is visible, because no product code calls them. | simplification | `packages/llm/llm` | **ported** | Adopt by omission: the Go model package should contain the first-token reader that session statistics and the trajectory view use, and not these five queries. |
| 100 | [Remove the Python SDK's unused reverse RPC direction][n100] | Proposal: the Python SDK client only sends requests and receives responses and notifications; its unused ability to receive requests from the server and answer them is removed. | simplification | `python/sdk` | **ported** | Adopt: the SDK server never sends a request to the client, so the Go design's Python client can be one-directional from the start. The SDK wire protocol is free to change under DH-ADR-2, which makes this the cheapest moment to drop the unused direction. |
| 101 | [Retire app-boot helpers used only by fixtures][n101] | Proposal: remove two start-up helpers (an older configuration-path resolver and a single-file environment loader) that only test fixtures still call; the real launcher uses the layered environment loader and an absolute profile path. | simplification | `packages/boot/app-boot` | **ported** | Adopt by omission: the Go boot package should expose one way to locate configuration and one way to load the environment, the ones the launcher uses. |
| 102 | [Retire the prompt registry change event][n102] | Proposal: stop emitting an event when a system-prompt provider is registered or removed, because nothing in the product listens to it; the prompt is rebuilt for every model step anyway. | simplification | `packages/core/system-prompt` | **ported** | Adopt: the Go system-prompt registry should not publish a change event until a consumer needs one. An advertised event is a public promise with tests and ordering rules to maintain. |
| 103 | [Keep shell-environment declarations focused on key ownership][n103] | Proposal: a plugin that contributes shell environment variables declares only the names it owns; the per-variable descriptions and the method that lists them are removed because nothing reads them. | simplification | `packages/shell/shell-env` | **ported** | Adopt: key ownership, conflict detection and refusal of undeclared output are the parts with consumers. The Go registry can start with a set of keys per contributor. |
| 104 | [Trim unused SessionQuery convenience methods][n104] | Proposal: remove four convenience read methods and one result type from the session query service, because no product code calls them. | simplification | `packages/session-query/session-query` | **ported** | Adopt by omission: the Go query interface should offer the operations with real consumers (observations, batch titles, surface reads and traces) and add others on demand. |
| 105 | [Borrow Auto review inputs until serialization][n105] | Proposal: the auto-review risk classifier stops copying and deep-freezing its input snapshot, because the only caller serializes it immediately. | simplification | `packages/experimental/auto-review` | **not-applicable** | Exists only because of JavaScript: the saving is one array copy and one `Object.freeze` traversal. Go has no freeze step; whether the classifier copies or borrows a slice is an implementation detail of that component. |
| 106 | [Parse SSH policies once at request admission][n106] | Proposal: the SSH helper validates a sandbox policy once, when the request arrives, and passes the typed result on; the inner resolver no longer validates it a second time. | simplification | `packages/ssh/ssh` | **ported** | Adopt: decoding once at the wire boundary into a typed value is the ordinary Go shape, and the typed parameter states the precondition. |
| 107 | [Canonicalize each sandbox workspace once][n107] | Proposal: resolve the real path of a sandbox workspace once per confinement, in the code that derives the roots, instead of once in common preprocessing and again in each backend. | simplification | `packages/sandbox/sandbox-local` | **ported** | Adopt: one canonical workspace path per call avoids repeated filesystem work and, more importantly, avoids two resolutions that could disagree if the path changes between them. |

### Rejected upstream (7 notes)

| # | Note | Summary | Kind | Area | First verdict | Why |
|---:|---|---|---|---|---|---|
| 110 | [Drop durable step boundary events][n110] | Rejected proposal: remove the `step/start` and `step/end` events from the session log. Upstream keeps them, because `step/end` is the durable sign that a model request finished, and the pair makes crash repair, invariants and reading a transcript clearer. | product architecture | `packages/core/session` | **ported** | The rejection still holds in Go. Knowing that a step finished without inferring it from neighbouring events is a property of the log, not of the language, and crash repair depends on it. |
| 111 | [Fold the persistence interface into dsh-session][n111] | Rejected proposal: move the storage contract and the shared write coordinator into the session package. Upstream keeps a separate contract package so that the boundary between session and storage backend stays clean. | product architecture | `packages/session/session-persistence` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2)) | The goal of the rejection, a clean boundary between the session and its storage backends, still holds. The argument for a separate package does not transfer: upstream backends extend an abstract class and so must import the package that defines it, whereas a Go backend satisfies an interface without importing it. Package boundaries are free to change (DH-ADR-2), so the session area decides where the interface and the write coordinator live. |
| 112 | [Truncate interrupted final turns on load][n112] | Rejected proposal: on load, throw away everything after the last completed turn. Upstream instead keeps an interrupted final turn and closes it with synthetic records (error results for unanswered tool calls, a step end, and a turn end marked interrupted). | product architecture | `packages/session/session-persistence` | **ported** | The rejection still holds in Go. One turn can contain many steps and large tool output, and silently dropping it after a crash loses real work; the Go session must define the same repair, including what the synthetic records say. |
| 113 | [Prune unused skill registry API][n113] | Rejected proposal: remove the method that lets a plugin register a skill directly at run time, because no shipped code calls it. Upstream keeps it as an intended extension path for third-party plugins. | product architecture | `packages/skill/skill` | **replaced** ([DH-ADR-4](../design.md#dh-adr-4)) | The reason for the rejection still holds: a plugin must be able to contribute a skill while the host runs. The mechanism changes, because a third-party plugin is a separate process (DH-ADR-4); run-time skill registration has to be a call in the plugin protocol rather than a method on an in-process service. |
| 114 | [Fold the single compaction backend into its service package][n114] | Rejected proposal: merge the compaction contract and its only complete backend into one package. Upstream keeps them apart because more compaction backends are planned. | product architecture | `packages/compaction/compaction` | **ported** | The rejection still holds in Go and costs less there: a small interface for compaction with the basic strategy as one implementation is ordinary Go, and the upstream tree already contains further compaction packages (image offload, tool-result pruning) beside the basic one. |
| 115 | [Use node:timers/promises for hand-rolled cancellable sleeps][n115] | Rejected proposal: replace three hand-written cancellable sleeps with Node's built-in promise timers. Rejected because the test framework's fake clock does not intercept the built-in, so tests would become slow or non-deterministic. | simplification | `packages/llm/llm-retry` | **not-applicable** | Exists only because of Node and Vitest: both the proposal and its rejection are about a Node built-in and a JavaScript test clock. A cancellable wait in Go is a `select` on a timer and the context. |
| 116 | [Dependency swaps rejected by the 2026-07 NIH audit][n116] | Rejected proposal: more than thirty swaps of hand-written code for npm packages or Node built-ins. Each was rejected with evidence, and each entry names what the hand-written code does that the package cannot, such as a size bound on incoming messages, start-time identity for process trees, or wire faults in the mock model server. | development process | (cross-cutting) | **replaced** ([DH-ADR-5](../design.md#dh-adr-5)) | The named npm packages are irrelevant to Go, so the individual rejections do not transfer. The goal does: a recorded reason for each hand-written component. The Go design reaches it through library specifications (DH-ADR-5), and each "what the package cannot do" entry is a requirement the matching Go library choice must be checked against. |

## Category: testing

Upstream's `testing` class: twenty notes, eighteen implemented and two proposed. A
testing decision is `ported` when the method is independent of the language, `replaced`
when the method stays but what it measures or the fixtures it uses must change, and
`not-applicable` when the note repairs specific upstream test files.

### Implemented upstream (18 notes)

| # | Note | Summary | Kind | Area | First verdict | Why |
|---:|---|---|---|---|---|---|
| 68 | [Real-API e2e in CI against the external DeepSeek API][n68] | Tests against the real model API run in their own workflow, separate from the keyless checks that any fork can run. A missing credential is a loud failure, never a silent pass. | testing practice | (cross-cutting) | **ported** | This is the same separation the Go design requires between local tests and real-environment scenarios, and the same rule that a skipped or unconfigured required scenario is not a pass. The practice is independent of the language. |
| 69 | [Keyless browser e2e lane for the web GUI][n69] | The web UI has a deterministic browser test lane that needs no API key: recorded sessions are replayed as model output against the real assembled host, a real browser drives the page, and the visible states are compared with normalized accessibility-tree snapshots. | testing practice | `apps/web` | **ported** | Replaying recorded model output into the real host while a real browser drives the real page is the right keyless evidence for a server-rendered UI too. The recordings must be made in the Go session format (DH-ADR-2) and the snapshots re-taken for the new HTML, so nothing is reused except the method. |
| 70 | [Frame-coalesced reasoning-chunk publication and browser stress validation][n70] | Every live chunk of a streaming reply is folded into state at once, but the UI is redrawn at most once per browser animation frame; the durable settlement publishes immediately. An opt-in browser stress test validates it. | product architecture | `packages/client/ui-conversation` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The goal stands: a long reasoning stream must not cause one redraw per chunk. With HTML streamed over server-sent events (DH-ADR-3) the coalescing moves to the Go host, which should batch live updates to a bounded rate before sending them; the React publication mechanism does not exist. |
| 71 | [Installed-wheel Python runtime pull-request validation][n71] | The Python SDK is tested as a user would install it: the built wheels are installed into a fresh virtual environment outside the repository, and the test first proves that the installed files, not the source checkout, are what runs. | testing practice | `python/sdk-runtime` | **ported** | The Python SDK is in scope (DH-ADR-1), and testing the installed artifact with an identity check before any behaviour test is independent of what the runtime inside the wheel is written in. |
| 72 | [Session-log snapshot corpus][n72] | One top-level corpus holds recorded sessions used as tests. Each scenario starts a shipped profile through the real launcher, replays the recorded model output, drives the product through a public interface, and requires the normalized saved session to equal the recording. | testing practice | `snapshots` | **replaced** ([DH-ADR-2](../design.md#dh-adr-2)) | The goal, a deterministic whole-product regression suite that needs no API key, stands. Upstream's recordings are in its session format and cannot be used as they are (DH-ADR-2); the Go design records its own corpus in the Go format and keeps the method. |
| 73 | [Required CI performance gate for opening large Sessions][n73] | A required benchmark job fails a pull request when opening a very large session exceeds a time or memory budget. Benchmarks are grouped by the user path they measure and run compiled code in fresh processes. | testing practice | `benchmarks` | **ported** | Opening a session with more than a hundred thousand events is a user path the Go host must keep fast, and upstream learned it by regressing from 35 ms to 5 s. The budget values must be measured again for Go; the gate and its workload shape carry over. |
| 74 | [Performance baselines for tool-heavy backend continuation][n74] | Benchmarks measure the repeated cost of continuing a long, tool-heavy session: preparing each model request over a long history, running another tool-heavy turn, and discovering inactive fork children. | testing practice | `benchmarks/agent-continuation` | **ported** | Request preparation that grows with history length is a risk in any implementation. The Go design should keep the three scenario groups and the synthetic history shape, with budgets measured for Go. |
| 75 | [Frontend large-session performance budgets][n75] | Two browser-side budgets cover long sessions: reconnecting to an active stream, and one workflow that opens a long session cold, pages back, switches views and receives a paced reply while the user types. | testing practice | `benchmarks/long-session-browser` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The user paths stay and need budgets, but what is measured changes: there is no client-side fold or React paint. The Go benchmarks measure server render time, fragment size and browser responsiveness for the same workflow. |
| 76 | [Qualify Desktop update downloads with an isolated local server][n76] | Desktop update downloads are qualified locally by running the real updater code against a loopback server that serves a test feed and inert bytes, with the installer call replaced by an observation, so no cloud storage or hardware signing is needed for a feedback cycle. | testing practice | `apps/desktop` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | The goal, exercising the real update path without release infrastructure, stands. The mechanism is built on Electron's updater classes; the Go desktop shell (DH-ADR-3) has a different updater and needs its own loopback qualification. |
| 77 | [Preserve installed Desktop update evidence across restarts][n77] | When a qualification run sets a journal directory, each desktop process writes a small log of whitelisted update states and actions to it, so that evidence survives the installer replacing the application. Raw errors, addresses and chat content are never written. | testing practice | `apps/desktop` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | An installed update ends the process that observed it in any desktop technology, so the evidence problem remains. The journal belongs to the Go desktop shell's updater (DH-ADR-3) and is redesigned with it; the whitelisting rule should be kept. |
| 78 | [Isolate installed-update materials from releases and user data][n78] | The two versions needed for an installed-update walkthrough are prepared under a random test identity and a separate distribution namespace, with their own user-data directory, so that test builds can never be offered to real clients or touch real user data. | testing practice | `apps/desktop` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | Isolation of test update material from real releases and user data is still required. The preparation steps operate on upstream's Electron runtime descriptor and inventory, which the Go desktop packaging (DH-ADR-3) does not have. |
| 79 | [Windows-lane observations wait on the state they read][n79] | Two flaky Windows test cases were changed to wait for the state they assert on instead of reading it after a fixed delay, and to print a timeline when they fail. | testing practice | `packages/boot/plugin-manager` | **not-applicable** | Upstream test maintenance: a repair of two specific test files in its suite. The lesson (a test waits on the observed state, not on the clock) is a general habit for Go tests, not a design decision. |
| 80 | [Plugin-manager Git children isolate the host's command-line configuration][n80] | Tests that start Git remove Git's command-line configuration variables from their environment first, so a CI host that exports them cannot change the result. | testing practice | `packages/boot/plugin-manager` | **not-applicable** | Upstream test maintenance: a repair of two test files after a CI host leaked configuration. The lesson for Go tests that start Git is to build the child environment explicitly. |
| 81 | [Web lane moves spreadsheet selections by keyboard before reflow assertions][n81] | A browser test of the spreadsheet preview selects a cell with the keyboard instead of a mouse click before resizing, because a click could be re-applied against a changed layout and select the wrong cell. | testing practice | `apps/web` | **not-applicable** | Upstream test maintenance: a repair of one browser test of a third-party spreadsheet widget. |
| 82 | [Performance baselines for large Session corpora][n82] | Benchmarks measure listing, content search and fork over a synthetic collection of sessions whose sizes follow the heavy-tailed distribution measured on a real user's 1,650 sessions. | testing practice | `benchmarks/session-corpus` | **ported** | Listing and search scale with the whole collection, and a uniform synthetic collection would understate the work. The measured size distribution is data the Go benchmarks can reuse directly. |
| 83 | [Memory-limited benchmark reruns][n83] | The heaviest benchmarks are run again inside a Linux control group limited to 4 or 8 GiB of memory with no swap; a case fails if it misses its normal budget or the kernel kills a process. | testing practice | `scripts` | **ported** | Behaviour on a small machine cannot be read from a 16 GB runner in any language. The Go runtime's garbage collector also reacts to a memory limit, so the rerun is as informative for Go as for Node. |
| 84 | [Scaled machine and memory-pressure benchmark report][n84] | Every benchmark run writes a self-contained HTML report that scales each measured time to older laptops and typical cloud machines and shows whether the working set fits in 4 or 8 GB, so a number can be read as user impact. | testing practice | `benchmarks` | **ported** | The report is independent of what is measured and helps a reviewer judge a budget. It is a low-priority aid: the Go benchmark area may keep it as a small tool or defer it with a stated reason. |
| 85 | [Linear credential matching and performance gates for user text][n85] | Credentials in user text shown in the UI are found with a scan that is linear in the text length: one literal-only search finds where a known credential format starts, and only then is the format checked. Performance gates keep text without credentials nearly free and hostile input linear. | product architecture | `packages/client/ui-secret-mask` | **replaced** ([DH-ADR-3](../design.md#dh-adr-3)) | Masking credentials in displayed user text stays a requirement, but it moves to the Go host that renders the HTML (DH-ADR-3). Inference: Go's standard `regexp` package guarantees linear-time matching, so most of the hand-built protection against backtracking is unnecessary; the list of credential formats and the performance gate are what carry over. |

### Proposed upstream (2 notes)

| # | Note | Summary | Kind | Area | First verdict | Why |
|---:|---|---|---|---|---|---|
| 108 | [Deterministic tests, the replay invariant fixture, and race stress][n108] | Proposal: tests never sleep for a fixed time but wait for an event; after every agent-loop test the session log is replayed into a fresh session and must produce the same message history; and a nightly job repeats the loop and inbox tests many times in random order to expose races. | testing practice | `packages/core/agent-loop` | **ported** | Adopt all three. Replay equality is the core promise of an event-sourced session and is cheap to assert in every test, and Go's test runner already offers repetition, shuffling and a data-race detector for the stress run. |
| 109 | [Mutation testing as the coverage counterweight][n109] | Proposal: run a mutation-testing tool that injects small bugs and checks that some test fails, as a counterweight to a line-coverage gate. | testing practice | (cross-cutting) | **not-applicable** | Upstream test-process tooling: the proposal is to adopt a JavaScript tool (Stryker with the Vitest runner) and was not built upstream. Whether the Go project uses mutation testing is a quality-gate choice for the test strategy, not an upstream decision to account for. |

## Patterns worth carrying into the Go design

These nine groups are the decisions in this batch that matter most. Each states what
upstream decided, why it matters, and what the Go design should take from it. Row
numbers refer to the tables above.

### 1. One send is at most one turn, and a turn means one thing

Each successful `send()` "creates one independent FIFO queue item. If that item runs, it
is the only ordinary message in its turn"
(`.agents/notes/implemented/simplification/2026-07-17-one-send-one-turn.md:17`), and the
next turn starts only after the previous one has ended and its durability checkpoint has
settled (#52). A plugin that records a log-only event appends it between turns instead
of opening an artificial turn (#54), and a live agent and its session share one
identifier (#51).

Why it matters: before these three notes, what a turn contained depended on which
messages happened to be queued at the same instant, and a turn identifier could mean
either a model-loop run or a bookkeeping write. Inference: Go schedules goroutines
pre-emptively, so any grouping by timing would be less predictable in Go than in
JavaScript. The agent-loop design should state the rule as an invariant (one queue item,
at most one turn, strict order) and test it under the race detector.

### 2. A format version changes only when a reader would misread the data

Upstream "chose reader behavior as the version criterion": a change keeps the same
session format version when older readers resume correctly, safely ignore an optional
feature while preserving its records, or refuse a required feature before they execute,
change or repair anything
(`.agents/notes/implemented/process/2026-10-08-session-reader-compatibility-review.md:13`,
#40). Two process notes supply the review evidence: every persisted type has a digest
that changes when any nested type changes, and the change must be acknowledged in a
committed record (#27); each field can carry a declared reader promise, such as
"unknown kinds are preserved" (#31).

Why it matters: the alternative, a version bump for every structural difference, forces
a migration for every optional field. Inference: the Go session format is new
([DH-ADR-2](../design.md#dh-adr-2)), which makes the first release the cheapest moment
to adopt the criterion and a schema snapshot test. The TypeScript type extractor does
not carry over; the rule and the review step do.

### 3. After a crash, keep the work and say what happened

Two rejected simplifications define crash recovery. Upstream refused to drop the
`step/start` and `step/end` events, because "`step/end` is the durable indication that a
model step finished"
(`.agents/notes/rejected/simplification/2026-06-20-drop-durable-step-boundaries.md:3`,
#110). It also refused to truncate an interrupted final turn on load, because "a single
turn can contain substantial real work"
(`.agents/notes/rejected/simplification/2026-06-20-truncate-interrupted-turns.md:3`,
#112); the interrupted turn is instead closed with synthetic records.

Why it matters: these are the two points where a simpler log was available and upstream
judged that it would lose information a user or a repair routine needs. Both rejections
hold in Go. The session design must therefore specify the repair on load: which
synthetic records are appended, what they say to the model, and that the repair is made
durable before the session is used.

### 4. One of each, until a second one has a consumer

Six notes remove a second way of doing the same thing:

| Row | The one thing kept | The second thing removed |
|---|---|---|
| #56 | One first-party session store behind a backend-neutral contract | A second first-party storage backend |
| #61 | One tool invocation form per agent, native or programmatic (`.agents/notes/implemented/simplification/2026-10-05-native-or-ptc-presentation.md:13`) | Offering both forms at once |
| #67 | One program language for programmatic tool calling | A second language path with no shipped runtime |
| #62 | Agent Presets as the one place that composes a subagent | Separate persona and tool-filter settings |
| #53 | The web UI and terminal as the user interfaces | The Agent Client Protocol bridge acting as "a second interactive product UI" (`.agents/notes/implemented/simplification/2026-07-23-acp-automation-only-protocol.md:11`) |
| #58 | Provider-neutral filesystem and subprocess contracts | An E2B cloud backend that could not meet them |

The proposals in rows #96, #99 and #101 to #104 apply the same rule to unused queue
priorities, helper functions, events and query methods. Why it matters: each second path
doubled a test matrix without a user. Inference: a redesign is the one moment when "do
not build it" is free, so the Go design should adopt every one of these by omission and
record the condition under which the second path returns (a real consumer).

### 5. One owner for each lifetime, and non-owners get a typed answer

`startActivation()` "is the consumer entry point for every provider" of subagents; the
handle it returns carries the child's identity, a result that settles only after
cleanup, and disposal of exactly that activation
(`.agents/notes/implemented/simplification/2026-09-17-unified-subagent-activations.md:13`,
#59). The LiveShare owner state follows the session write lease: "ownership carries no
expiry, contention is a typed answer rather than a crash, and a record left by a process
that is gone is reclaimed"
(`.agents/notes/implemented/feature/2026-11-11-liveshare-owner-state-host-claim.md:15`,
#8). A host that does not hold the claim can still read the shares; only changes are
refused.

Why it matters: both notes separate ownership (who creates, changes and releases a
resource) from access (who may read it), and both make losing ownership an ordinary
result instead of an error path. Inference: this maps directly onto a Go value that owns
a `context.Context` and a result channel, and onto a claim file with a typed "held by
another host" error. Each area that has a shared on-disk resource should name its owner
the same way.

### 6. Read settings live and derive presentation, instead of storing copies

The list of models a subagent may use is read "at each call" from the live setting
instead of being copied into each session at creation
(`.agents/notes/implemented/feature/2026-11-17-live-subagent-model-selection-setting.md:13`,
#9). The browser's time zone travels with each prompt and is never session state (#55).
A quote in a message is recognised from its Markdown text, not from a stored index
(#63). The team projection keeps only tasks and mode (#65).

Why it matters: every stored copy needed creation, resume and fork rules, a migration,
and an answer to "which copy is right". Inference: a server-rendered UI
([DH-ADR-3](../design.md#dh-adr-3)) makes derivation cheaper still, because the Go host
that renders a message can recognise a quote or mask a credential (#85) at render time
with no client state to keep in step.

### 7. Filter before the model, and label text that the user did not write

The proposal for event-triggered tasks passes each observation through three layers,
"and only the last one calls a model": a source provider reports a change, a shared
trigger compares it with stored state and filters it, and only then is a candidate
delivered to a session
(`.agents/notes/proposed/feature/2026-10-29-event-triggered-automation-tasks.md:18`,
#86). The delivered message quotes the event "as event data from the source, not as the
user's words"
(`.agents/notes/proposed/feature/2026-10-29-event-triggered-automation-tasks.md:45`).
The implemented `wait_agent` rule is the same idea in small: a wait ends only when a
message arrives in the caller's own inbox, never on unrelated activity (#64).

Why it matters: a model turn per poll is the main cost of any automation, and event text
written by strangers is the main injection path. Inference: the feature is not built
upstream, so the Go design does not owe it in a first stage, but the schedule design
should treat time as one source among several from the start so that the storage does
not have to be migrated later.

### 8. Three kinds of test evidence, kept apart

- **Keyless replay.** Recorded sessions are replayed as model output against the real
  assembled product, through the real launcher (#72) and through a real browser (#69):
  "the recorded session remains the primary input and, for current-generation scenarios,
  the expected output"
  (`.agents/notes/implemented/testing/2026-08-24-session-log-snapshot-corpus.md:19`).
  The proposal in #108 adds a replay-equality check after every agent-loop test.
- **Real API.** Tests against the real model run in their own workflow, with "a
  preflight that converts a missing secret into a loud failure instead of a false green"
  (`.agents/notes/implemented/testing/2026-06-19-real-api-e2e-ci.md:15`, #68).
- **Performance budgets by user path.** Opening one very large session (#73), continuing
  a long tool-heavy session (#74), listing and searching a heavy-tailed collection
  measured from 1,650 real sessions
  (`.agents/notes/implemented/testing/2026-09-28-session-corpus-performance.md:17`, #82),
  and the same under a 4 or 8 GiB memory limit (#83). The first gate exists because
  first open "grew from about 35 ms to about 5 s" unnoticed
  (`.agents/notes/implemented/testing/2026-09-04-session-open-performance-gate.md:9`).

Why it matters: these are the same three kinds of evidence the Go design must keep
separate (local tests with a replayed model, real-environment scenarios, and measured
budgets). Upstream's recordings and budget numbers cannot be reused
([DH-ADR-2](../design.md#dh-adr-2)); the methods, the workload shapes and the measured
size distribution can.

### 9. Every hand-written component has a recorded reason, and nothing reaches around the framework

The rejected audit note lists more than thirty npm packages and built-ins that were not adopted and, for
each, what the hand-written code does that the package cannot; "a future proposal for
any item must beat its recorded reason"
(`.agents/notes/rejected/simplification/2026-07-26-dependency-swaps-rejected-by-nih-audit.md:13`,
#116). Examples that bind a Go library choice just as much: the language-server
transport must bound the size of an incoming message; retry is a decision returned to
the agent loop, not a function that is called again; an atomic write needs a private
staging directory and a flush of the parent directory; the terminal sanitizer is a
streaming state machine that also extracts the prompt marker; a process tree needs
start-time identity against reuse of process IDs; the mock model server must be able to
break the connection mid-stream.

The companion rule for the framework is #50: reading a service through the root context
"skips the `inject` check"
(`.agents/notes/implemented/process/2026-11-13-no-new-cordis-root-reads.md:9`), so new
reads are forbidden. The Go cordis port has the same accessor
(`Context.Root()`, `src/go/cordis/context.go:66` in this repository).

Why it matters: Inference: each "what the package cannot do" entry is a requirement
that the matching Go library specification ([DH-ADR-5](../design.md#dh-adr-5)) should be
checked against, and the root-context rule costs nothing if it holds from the first line
of harness code, because then no baseline of existing violations is needed.

## Decisions that exist only because of JavaScript or npm

Nineteen notes exist only because the product is written in TypeScript, runs on Node and
is distributed through npm. They have no Go counterpart and are `not-applicable`.

| Cause | Rows | What the note is about | Why Go does not have the problem |
|---|---|---|---|
| npm publication and dependency sections | #21, #23, #36, #39, #44 | Three npm release sequences; which dependency section a browser or host import belongs in, because "npm installs peers automatically" and resolving them is slow (`.agents/notes/implemented/process/2026-08-26-published-dependency-faces.md:9`); a work budget for an npm install check; publishing source and source maps | A Go binary has no install-time dependency resolution, and a Go module is published as source |
| Packages installed into a profile | #60 | Cleaning `node_modules` inside a desktop profile | A Go profile holds configuration only ([DH-ADR-4](../design.md#dh-adr-4)) |
| TypeScript compiler and build scheduling | #37, #41, #42, #43, #47, #91 | Splitting type-checking from artifact builds, eight build speed-ups, eight local-check speed-ups, generating remote-method metadata before compilation, deriving `tsconfig` reference lists | `go build` compiles and type-checks in one step with a build cache; there are no project references or generated declaration files to order |
| TypeScript and JavaScript language rules | #34, #35, #45, #105 | No assertions through `unknown`; no silent empty `catch`; no top-level `await`; skipping one `Object.freeze` pass | Go has no type assertion that bypasses checking in that way, no exceptions, an explicit `main`, and no freeze step |
| Node runtime | #46, #115 | Minimum Node version 24.9; hand-written cancellable sleeps kept because the test clock "does not intercept `node:timers/promises`" (`.agents/notes/rejected/simplification/2026-07-26-builtin-timer-promises-for-hand-rolled-sleeps.md:3`) | The Go counterpart of a runtime floor is the `go` directive; a cancellable wait is a `select` on a timer and a context |
| TypeScript API reporting | #89 | A generated public API report per npm package | External plugins see a protocol, not exported symbols (#95) |

A second group of notes has a goal that survives in Go but a mechanism that existed only
because of JavaScript or npm. They are `replaced` or `ported`, not `not-applicable`, and
the area owners should take the goal and leave the mechanism:

| Row | Goal that survives | JavaScript-only mechanism that does not |
|---|---|---|
| #28, #38 | The default product contains nothing experimental by accident, and nothing ships without a stated role | Walking npm dependency edges and `package.json` sections |
| #30 | A user can switch on an official optional capability | The capability being an npm runtime dependency of the command-line package |
| #49 | A plugin's public surface is its registration and its contracts | A checker over what `index.ts` exports; Go package visibility does this |
| #97 | Configuration is refreshed live | Replacing running JavaScript modules in process |
| #57 | A simple loader whose consumers check their own readiness | Staying close to pinned vendored JavaScript sources, which was part of the reason for the revert (`.agents/notes/implemented/simplification/2026-09-09-nontransactional-loader.md:9`) |
| #27, #31 | A reviewed record of every change to a persisted type | An extractor over TypeScript declarations and comment annotations |
| #16, #90 | Licence disclosure and advisory monitoring for dependencies | Generators and scanners that read npm manifests and the pnpm lock file; vendor drift checks |
| #111, #113 | A clean storage boundary; run-time skill registration by plugins | An abstract class in its own npm package; a method on an in-process service |
| #70, #85 | Bounded redraw rate for streamed replies; linear-time credential masking | React publication per animation frame; hand-built protection against regular-expression backtracking |
| #13, #116 | A stated reason for every dependency and every hand-written component | The specific npm packages evaluated |

The remaining twenty `not-applicable` notes are not about JavaScript: they describe
upstream's CI runners, review flow, documentation process, repository mechanics and
repairs to individual test files, and are listed by cause under [Counts](#counts).

## Open questions for the area owners

These are points where a first verdict depends on a choice this document cannot make.

1. **Is the SQLite session store present or absent (#56)?** The note says the
   `dsh-session-persistence-sqlite` package is absent
   (`.agents/notes/implemented/simplification/2026-08-30-jsonl-only-session-persistence.md:17`),
   but the pinned checkout contains `packages/session/session-persistence-sqlite` with a
   README that describes it as an explicitly chosen provider
   (`packages/session/session-persistence-sqlite/README.md:12`). Settled by area
   A05: the note's factual claim is stale, and the Go design has one first-party store,
   which is SQLite ([../areas/session-store.md](../areas/session-store.md),
   `DH-ADR-STORE-1`).
2. **Should a failed configuration reload roll back (#57)?** Upstream's loader does not:
   "Neither restores a previous plugin or configuration"
   (`.agents/notes/implemented/simplification/2026-09-09-nontransactional-loader.md:15`).
   Settled. A reload is still not a transaction across entries: an entry that fails to
   load is reported on its own and its siblings keep their new state
   ([DH-D-LOADER-7](../libs/cordis-loader.md#dh-d-loader-7)). What can be known before
   anything runs is checked first, so a static issue rejects the whole edit and the
   running tree is unchanged ([../areas/boot.md](../areas/boot.md), `DH-ADR-BOOT-7`).
   The half-applied state an operator can see is the per-entry report in
   `loader.Reloaded`.
3. **What runs model-written programs (#67)?** Settled by the owner: no runtime. A
   script in any language already on the machine calls tools through `dsh tool call`
   ([DH-ADR-8](../design.md#dh-adr-8)).
4. **Who owns release signing and update qualification (#26, #33, #76, #77, #78)?** These
   five notes assume an Electron desktop build, which does not exist in the Go design
   ([DH-ADR-18](../design.md#dh-adr-18)). What remains is signing the one `dsh` binary
   per platform and qualifying `dsh update`, specified in
   [../libs/electron-builder.md](../libs/electron-builder.md) and
   [../libs/electron-updater.md](../libs/electron-updater.md) with area A24.
   Certificates and where signing keys are kept are an organisational matter the owner
   left open.
5. **Where do browser-held remainders go (#3, #4, #87)?** Three notes keep a small amount
   of state or behaviour in the browser. Under [DH-ADR-3](../design.md#dh-adr-3) each
   needs either a host-side home or a named script island.
6. **Which document owns dependency hygiene (#13, #16, #90, #116)?** Settled: the
   toolchain specification. The notices file is `DH-LIB-NOTICES` and the advisory scan
   is `DH-LIB-VULNCHECK` in
   [../libs/build-test-toolchain.md](../libs/build-test-toolchain.md); licence records
   are indexed in [../libs/README.md](../libs/README.md).
7. **Is event-triggered automation in the first plan (#86)?** The verdict is to adopt the
   layering; the priority is for the schedule area and the implementation plan.
8. **Keep or defer the scaled benchmark report (#84)?** Settled: replaced. Each budget
   test writes a plain record of its measured time, peak memory and runner; nothing is
   scaled to other hardware ([DH-ADR-TOOLCHAIN-14](toolchain.md#dh-adr-toolchain-14)).

[n1]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/feature/2026-10-29-shipped-fs-mutation-defaults.md
[n2]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/feature/2026-11-02-liveshare-plugin-method-catalog.md
[n3]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/feature/2026-11-05-forge-pending-review-drafts.md
[n4]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/feature/2026-11-05-session-panel.md
[n5]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/feature/2026-11-11-deepseek-chat-export-import.md
[n6]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/feature/2026-11-11-forge-provider-command-rules.md
[n7]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/feature/2026-11-11-forge-review-page.md
[n8]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/feature/2026-11-11-liveshare-owner-state-host-claim.md
[n9]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/feature/2026-11-17-live-subagent-model-selection-setting.md
[n10]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/feature/2026-11-17-sidebar-scrollbar-fade-in-css.md
[n11]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-07-21-serial-cross-platform-ci-reference.md
[n12]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-07-26-ci-failover-runbook.md
[n13]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-07-26-dependencies-over-hand-rolling.md
[n14]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-07-26-frozen-agent-note-archive.md
[n15]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-07-27-worktree-local-lefthook.md
[n16]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-07-30-generated-third-party-notices.md
[n17]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-08-02-native-github-stacks-and-optional-rebases.md
[n18]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-08-08-native-windows-pull-request-ci.md
[n19]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-08-08-unified-github-label-taxonomy.md
[n20]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-08-10-event-directed-pr-review-status.md
[n21]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-08-10-npm-release-sequences.md
[n22]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-08-26-ban-ambiguous-origin-label.md
[n23]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-08-26-published-dependency-faces.md
[n24]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-09-06-python-runtime-windows-hosted.md
[n25]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-09-09-blacksmith-failover-leg.md
[n26]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-09-09-parallel-macos-notarization.md
[n27]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-09-11-persistence-type-history.md
[n28]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-09-12-default-product-experimental-isolation.md
[n29]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-09-14-generated-cli-help-reference.md
[n30]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-09-15-shipped-optional-bundles.md
[n31]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-09-17-persistence-schema-review.md
[n32]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-09-17-windows-runtime-signature-cache.md
[n33]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-09-17-windows-signature-completion.md
[n34]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-09-19-no-unknown-casts.md
[n35]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-09-21-reject-silent-empty-catch.md
[n36]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-09-28-dual-release-install-layout-work-budget.md
[n37]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-10-05-pr-artifact-typechecks.md
[n38]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-10-05-product-package-classification.md
[n39]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-10-08-published-source-and-docs.md
[n40]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-10-08-session-reader-compatibility-review.md
[n41]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-10-11-build-artifacts-check-split.md
[n42]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-10-11-lossless-build-speedups.md
[n43]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-10-12-local-gate-speedups.md
[n44]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-10-14-experimental-dependency-policy.md
[n45]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-10-14-no-top-level-await-in-packages.md
[n46]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-10-15-node-24-runtime-floor.md
[n47]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-10-18-source-first-metadata-builds.md
[n48]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-10-20-weighted-approval-status-gate-with-check-run-label.md
[n49]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-11-04-plugin-entry-value-exports.md
[n50]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/process/2026-11-13-no-new-cordis-root-reads.md
[n51]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-06-20-unify-agent-and-session-id.md
[n52]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-07-17-one-send-one-turn.md
[n53]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-07-23-acp-automation-only-protocol.md
[n54]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-07-28-remove-synthetic-log-only-turns.md
[n55]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-08-09-explicit-schedule-time-zone.md
[n56]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-08-30-jsonl-only-session-persistence.md
[n57]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-09-09-nontransactional-loader.md
[n58]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-09-11-remove-e2b-providers.md
[n59]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-09-17-unified-subagent-activations.md
[n60]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-09-19-remove-desktop-profile-core-cleanup.md
[n61]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-10-05-native-or-ptc-presentation.md
[n62]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-10-09-preset-owned-subagent-composition.md
[n63]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-10-13-text-derived-message-quotes.md
[n64]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-10-29-inbox-only-agent-wait.md
[n65]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-10-29-team-task-only-projection.md
[n66]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-11-02-deepseek-deferred-tool-failures.md
[n67]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/simplification/2026-11-12-typescript-only-ptc.md
[n68]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-06-19-real-api-e2e-ci.md
[n69]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-07-24-web-gui-browser-e2e-lane.md
[n70]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-08-03-opt-in-reasoning-chunk-browser-stress.md
[n71]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-08-23-installed-python-wheel-black-box-ci.md
[n72]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-08-24-session-log-snapshot-corpus.md
[n73]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-09-04-session-open-performance-gate.md
[n74]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-09-06-backend-continuation-performance.md
[n75]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-09-06-frontend-performance-budgets.md
[n76]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-09-10-desktop-local-updater-qualification.md
[n77]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-09-14-desktop-installed-update-journal.md
[n78]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-09-14-desktop-installed-update-materials.md
[n79]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-09-27-observation-waits-on-observed-state.md
[n80]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-09-27-plugin-manager-git-child-environment.md
[n81]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-09-27-spreadsheet-selection-by-keyboard-before-reflow.md
[n82]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-09-28-session-corpus-performance.md
[n83]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-10-07-memory-limited-benchmarks.md
[n84]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-10-07-scaled-benchmark-report.md
[n85]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/implemented/testing/2026-10-11-user-text-credential-matching.md
[n86]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/feature/2026-10-29-event-triggered-automation-tasks.md
[n87]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/feature/2026-11-06-chat-history-consumption.md
[n88]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/feature/2026-11-06-chat-history-consumption-details.md
[n89]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/process/2026-06-11-api-extractor-reports.md
[n90]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/process/2026-06-11-supply-chain-and-vendor-drift.md
[n91]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/process/2026-06-20-discover-package-inventory.md
[n92]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/process/2026-07-13-human-review-skill-maintenance.md
[n93]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/process/2026-07-26-remove-packed-session-fixture-migrator.md
[n94]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/process/2026-08-20-audience-first-documentation-quality.md
[n95]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/process/2026-11-05-stable-api-policy.md
[n96]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/simplification/2026-09-17-defer-office-background-scheduling.md
[n97]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/simplification/2026-09-19-config-only-hmr.md
[n98]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/simplification/2026-09-19-provider-only-instruction-reads.md
[n99]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/simplification/2026-09-19-prune-stream-visibility-helpers.md
[n100]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/simplification/2026-09-19-python-sdk-directional-client.md
[n101]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/simplification/2026-09-19-retire-app-bin-boot-helpers.md
[n102]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/simplification/2026-09-19-retire-prompt-registry-change-event.md
[n103]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/simplification/2026-09-19-shell-env-key-declarations.md
[n104]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/simplification/2026-09-19-trim-session-query-convenience-api.md
[n105]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/simplification/2026-10-18-borrow-auto-review-inputs.md
[n106]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/simplification/2026-10-18-parse-ssh-policy-once.md
[n107]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/simplification/2026-10-18-single-workspace-canonicalization.md
[n108]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/testing/2026-06-11-deterministic-and-stress-testing.md
[n109]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/proposed/testing/2026-06-11-mutation-testing.md
[n110]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/rejected/simplification/2026-06-20-drop-durable-step-boundaries.md
[n111]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/rejected/simplification/2026-06-20-fold-session-persistence-interface.md
[n112]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/rejected/simplification/2026-06-20-truncate-interrupted-turns.md
[n113]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/rejected/simplification/2026-07-12-prune-unused-skill-registry-api.md
[n114]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/rejected/simplification/2026-07-19-fold-compaction-package-split.md
[n115]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/rejected/simplification/2026-07-26-builtin-timer-promises-for-hand-rolled-sleeps.md
[n116]: https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/.agents/notes/rejected/simplification/2026-07-26-dependency-swaps-rejected-by-nih-audit.md
