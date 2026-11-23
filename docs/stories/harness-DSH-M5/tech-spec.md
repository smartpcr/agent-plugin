---
title: "dsh M5 -- later scope"
storyId: "harness:DSH-M5"
---

# harness:DSH-M5 -- Technical specification (problem, scope, constraints, risks)

> Story `harness:DSH-M5`: milestone **M5 "Later scope"** of `dsh`, the Go
> re-implementation of deepseek-harness. It covers impl.md stages **DH-S-49 to
> DH-S-59**. It starts when milestone M4 is reached ([DH-S-48](../../harness/impl.md#dh-s-48)
> closed). No story-point estimate is given here (see open question M5-Q-1).

This document says **what problem this story solves, what is in and out of its scope,
what it must never do, which constraints are hard, how its real-environment acceptance
is run, and which risks can sink it**. It follows the structure and the IDs of the M1
story's [tech-spec.md](../harness-DSH-M1/tech-spec.md). Where an M1 ID still applies
(non-goals `NG-*`, layout `RM-*`, gates `QG-*`, evidence `EV-*`, spine invariants
`SP-*`, environment `EN-*`, change control `DC-*`), it is cited and not repeated. IDs
new to this story carry the prefix `M5-`.

Source documents. This story uses them as given and does not re-decide them:
[design.md](../../harness/design.md) (outcomes `DH-O-1..16`, owner decisions
`DH-ADR-1..40`), [impl.md](../../harness/impl.md) (stages, steps, rules, scenario
index), the area and library specifications under `docs/harness/areas/` and
`docs/harness/libs/`, and the two debt registers
[debt/carried-host-ui-experimental.md](../../harness/debt/carried-host-ui-experimental.md)
and [debt/carried-core.md](../../harness/debt/carried-core.md).

---

## 0. Vocabulary used in this document

The terms below come from design.md sec. 2 and impl.md sec. 1.3. They are restated
here because every later section depends on them.

| Term | Meaning | Why it matters in M5 |
|---|---|---|
| **Experimental capability** | A feature under `src/harness/internal/x/...`, one sub-package per feature, mounted as a **default-off entry** in configuration ([design.md sec. 4.2](../../harness/design.md#42-go-module-layout)). "Default-off" means a stock installation does not compose it; an operator turns it on in a configuration layer of one installation | Most of M5 is experimental (worktrees, hooks, inspector, sharing, translation, browser and computer use). The milestone makes them *available to turn on*, never *on* |
| **Sender** | A component that sends something off the machine **on the harness's own initiative**: telemetry, analytics, session-log upload, the automatic update check, notice polling ([design.md sec. 2](../../harness/design.md#2-vocabulary)) | Each sender is a visible setting that is off until the **user** turns it on. One **off-switch** stops every sender at once ([DH-ADR-9](../../harness/design.md#dh-adr-9), [DH-ADR-27](../../harness/design.md#dh-adr-27)) |
| **Explicit request** | Something the user asked for directly: running `dsh update`, creating a live share, a code-host status read the user configured | Not a sender. It still works when the off-switch is set, and it says where it connects first ([DH-ADR-27](../../harness/design.md#dh-adr-27)). The off-switch is therefore not a network kill switch |
| **Build tag** | A Go compile-time switch. Code behind a tag is absent from the default binary | The four cloud sign-in components are behind one; the default binary does not contain them ([DH-S-51](../../harness/impl.md#dh-s-51) exit evidence) |
| **Guarded scenario** | A real-environment scenario whose defect could delete, restore over or take ownership of something. It runs **inside its build stage**, on scratch data and test accounts, and that stage cannot close without it ([impl.md sec. 7.4](../../harness/impl.md#74-guarded-scenarios)) | 17 of this story's 63 scenarios are guarded: the importer, uploads, the binary swap, cloud grants, plugin attachment, hosted authorisations, worktrees, the chat import |
| **Built / done** | Built: code merged, local tests pass, manual check recorded. Done: built, every held-open dependency closed and every proving scenario passed ([impl.md sec. 1.3](../../harness/impl.md#13-vocabulary)) | A build stage closes as *built*; deliverables become *done* in DH-S-58 or in their guarded step. The two are never merged (EV-2) |
| **Held open (`H-n`)** | A deliverable built earlier against a later dependency's contract; done only at a named closing step | Five M4 leftovers (H-15 to H-18, H-20) close in this story; at M5 none remain ([impl.md sec. 10.3](../../harness/impl.md#103-held-open-dependencies)) |
| **Known limit** | A carried ledger row, either a **platform limit** (a fact of an OS, browser or other party's program) or a **scope boundary** (something not done by recorded decision), published for operators ([impl.md sec. 8](../../harness/impl.md#8-known-limits-and-remaining-tech-debt-by-stage)) | DH-S-59 publishes 207 of them for the host, UI and experimental areas |
| **Ownership vs access** | Ownership is who may remove a thing; access is who may use it ([x-forge-worktree.md sec. 6.3](../../harness/areas/x-forge-worktree.md)) | Cleanup in M5 (worktrees, the previous binary, shares, grants) may undo only what the harness owns |

---

## 1. Problem statement

### 1.1 What was asked, read precisely

| Phrase in the request | Meaning adopted in this story | Source that fixes the meaning |
|---|---|---|
| "milestone M5" | The milestone row of impl.md sec. 3.3: reached when [DH-S-58](../../harness/impl.md#dh-s-58) and [DH-S-59](../../harness/impl.md#dh-s-59) are closed | [impl.md sec. 3.3](../../harness/impl.md#33-milestones-and-when-a-feature-may-be-enabled), row `M5` |
| "stages DH-S-49 to DH-S-59" | Nine build stages (S49..S57), one acceptance stage (S58), one known-limits stage (S59). All P2 | [impl.md sec. 5](../../harness/impl.md#5-stage-index) |
| "later scope" | Cloud sign-ins, external agent runtimes, browser and computer use, worktrees, hooks, the inspector, live sharing, translation, the upstream session importer, `dsh update` | impl.md sec. 3.3, M5 "What is usable"; sec. 4 (P2, 85 deliverables) |
| "M4 closed as entry" | This story starts only after DH-S-48 is closed, even where an impl.md entry line would allow an earlier start | Story instruction; see M5-Q-2 |
| "executes real-environment scenarios" | Unlike M1, this story **runs** scenarios: 17 guarded ones in build stages and 46 in DH-S-58, including the M4 holdovers | impl.md sec. 7.4, sec. 7.5 rows S50, S51, S52, S54, S56, S58 |
| "enabling rule" | Quoted exactly from impl.md: *"Every experimental capability stays a default-off entry ([design.md sec. 4.2]); an operator turns one on per installation. No scenario is placed after M5."* | impl.md sec. 3.3, row `M5` |

### 1.2 The problem in one paragraph

At M4 `dsh` is a signed public release with parity on the main paths. What is left is
the long tail upstream offers as "under evaluation", plus a few features deferred by
owner decision. Many of them **send data off the machine** (uploads, analytics,
telemetry, cloud speech, web translation, the update check), **act on things the user
owns** (worktrees and branches in the user's repository, the program file, sessions
imported into a home, authorisations at hosted services and code hosts), or **hand
work to software the project does not control** (Claude Code, Codex, ACP agents, OAR
runtimes, Cua Driver, Chrome). The problem of this story is: *build these capabilities
so that each is off until an operator or user deliberately turns it on, prove each
against the real external thing it depends on, undo only what the harness itself
created, and publish every remaining limit, without weakening any M1..M4 guarantee.*

### 1.3 Why this cut, and why it is hard

- **It is the last milestone.** impl.md places no scenario after M5. Every deliverable
  still "built, not done" at M4 must be done here, or stay visibly open.
- **The proof is mostly external.** 46 of the 63 scenarios need accounts, installed
  programs, logged-in desktops, a second person or a code host. impl.md sec. 7 says
  "This product has no lab": each environment must be obtained by the team.
- **Several operations are destructive or irreversible.** A grant left at a cloud
  issuer is a live credential; an upload cannot be recalled; a swap killed between two
  renames leaves no binary at the original path; a wrong cleanup deletes the user's
  work. These are exactly the guarded scenarios.
- **It touches the framework again.** DH-S-49 changes `src/go` (cordis, loader) once
  more, after a public release (M1 risk R-04 applies again).

### 1.4 Starting point in the repository (observed on `main`, 2026-11-22)

| Fact | Value today | Consequence |
|---|---|---|
| Last merged work | M1 closure (`dd3f78e`, "[e2e] Context Pipeline and M1 Closure") | M2..M4 are sibling stories not yet done; this story cannot start (entry M5-A-1) |
| `src/harness/internal/` | `agent archtest boot cli context delegate host human llm schema session storage testkit tools util world` | No `internal/x/`, no `internal/update`, no `internal/media`, no `internal/connect` yet |
| `.github/workflows/` | `ci.yml`, `release.yml`, `harness-scenarios.yml`, `harness-manual-checks.yml`, per-stage `e2e-*.yml` | A scenario workflow exists from M1; this story adds its required IDs to the credentialed workflow and never adds a fork-PR trigger (QG-10) |
| `docs/stories/harness-DSH-M1/evidence/` | One evidence record per M1 stage | The same record format (impl.md sec. 7.3) is used here |

---

## 2. In scope

### 2.1 Capabilities, linked from goal to deliverables to proof (A-B-C)

Each row is one thing a person can use at M5 (part A), the impl.md steps that build it
(part B) and the scenarios that prove it (part C), with its switch. "G" marks a
guarded scenario run in its build stage; the others run in DH-S-58. Every scenario ID
is linked from [impl.md sec. 7.5](../../harness/impl.md#75-index-of-all-scenarios).

| ID | Capability (what becomes usable) | Built by (steps) | Outcomes | Proven by | How it is switched on |
|---|---|---|---|---|---|
| M5-CAP-1 | Framework pieces and small tools: fiber-owned subscriptions, inspector accessors, three-way merge, mounted group files, `str_replace_editor`, compaction read-back, damaged-image set-aside, remote watching, terminal location, mention search, MCP prompt commands, model-backed search, worksheet image, TypeScript client, plugin-owned events | 49.1..49.15 | DH-O-1, -2, -4, -5, -10 | `DH-E-CONTEXT-6`, `DH-E-CONNECT-8`, `DH-E-MEDIA-6` (58.19); the rest local tests only | Terminal-location context stays P2 and off (impl.md sec. 9.1); the others follow their area's entry |
| M5-CAP-2 | Senders with consent: product and operational events, shell analytics, session upload after confirming exactly what is sent, feedback with a confirmed upload | 50.1..50.4, 50.8 (H-15) | DH-O-5, -7, -14 | G: `DH-E-STORE-4`, `DH-E-VIEW-4` (50.9); `DH-E-HOST-3`, `DH-E-UISET-2`, `DH-E-HUMAN-5` (58.2); `DH-E-HOST-12` (58.18) | Each data kind off until the **user** opts in; the off-switch stops all (M5-OPT-2) |
| M5-CAP-3 | Upstream session importer: a separate, one-way command for upstream format 4 | 50.5 | DH-O-5 | G: `DH-E-STORE-3` (50.9) | An explicit command; refuses a file it cannot map, whole ([DH-ADR-11](../../harness/design.md#dh-adr-11)) |
| M5-CAP-4 | `dsh update` and the update notice | 50.7 (CLI-8, WEBAPP-10) | DH-O-14, -12 | G: [`DH-E-ELECTRONUPDATER-2`](../../harness/libs/electron-updater.md#dh-e-electronupdater-2), [`DH-E-WEBAPP-3`](../../harness/areas/app-web.md#dh-e-webapp-3) (50.9); `DH-E-ELECTRONUPDATER-1` (58.2) | `dsh update` runs only when the user runs it; the automatic check is a sender, off ([DH-ADR-32](../../harness/design.md#dh-adr-32)) |
| M5-CAP-5 | Workspace hand-off: "open in" an editor, terminal or file manager; choosing a host directory; taking a file out of the page | 50.6, 50.8 (H-20), 57.10 | DH-O-12 | `DH-E-HOST-5`, `DH-E-UISIDE-4`, `DH-E-UICHAT-6` (58.3); `DH-E-HOST-10` (58.18) | Local owner only; absent away from the machine |
| M5-CAP-6 | Cloud sign-ins: Bedrock, Vertex, Azure Entra, OAuth with a stored refreshable grant; vendor-account sign-in from settings | 51.1..51.5 | DH-O-8, -12 | G: [`DH-E-HUMAN-4`](../../harness/areas/interaction-credentials.md#dh-e-human-4), `DH-E-LLM-10` (51.6); `DH-E-LLM-7`, `-8`, `-9` (58.1) | Behind a build tag, absent from the default binary (M5-OPT-4) |
| M5-CAP-7 | External runtimes and extension points: `dsh`/ACP/Claude Code/Codex children, OAR, client-declared tools (Go and Python), protected MCP accounts, connectors, language servers, a model-written plugin attached to its own session, background workflow jobs | 52.1..52.13 (H-16..H-18), 55.7 | DH-O-2, -6, -7, -9, -10, -11, -13 | G: `DH-E-BOOT-5`, `DH-E-EXT-3`, `DH-E-CONNECT-3`, `DH-E-UISET-5` (52.14); `DH-E-CONNECT-2`, `-4` (58.4); `DH-E-PTC-6`, [`DH-E-UIWORK-2`](../../harness/areas/ui-work.md#dh-e-uiwork-2) (58.5); `DH-E-PYSDK-4` (58.6); `DH-E-SUBAGENT-4`, `-5`, `DH-E-ACPSDK-2`, `DH-E-CLAUDEAGENTSDK-1`, `DH-E-CODEX-1` (58.7); `DH-E-OAR-1`, `DH-E-XHOOKS-4` (58.8) | Client tools off until accepted; attach only to own session ([DH-ADR-30](../../harness/design.md#dh-adr-30)); OAR runs outside the sandbox and says so |
| M5-CAP-8 | Browser use, computer use and dictation | 53.1..53.7 | DH-O-1, -4, -7, -10, -12 | `DH-E-MEDIA-4`, `DH-E-XCUA-2`, `-3` (58.9); `DH-E-MEDIA-5`, `DH-E-XCUA-1` (58.10); `DH-E-XCUA-4` (58.11) | Default-off entries; computer use only through an operator-installed MCP provider ([DH-ADR-19](../../harness/design.md#dh-adr-19)); a cloud speech endpoint is a sender |
| M5-CAP-9 | Worktrees, `isolation: worktree` for a delegated child, and read-only code-host status | 54.1..54.6, 54.8 | DH-O-1, -3, -5, -7, -9, -10 | G: `DH-E-XFORGE-1`, [`-2`](../../harness/areas/x-forge-worktree.md#dh-e-xforge-2), `-3`, `-4`, `DH-E-SUBAGENT-7` (54.7) | `x.worktree` entry off; `isolation: worktree` off by default; UI shows status only ([DH-ADR-38](../../harness/design.md#dh-adr-38)) |
| M5-CAP-10 | Hooks, automatic review, webhooks, bounded agent loops, agent terminals | 55.1..55.6, 55.8 | DH-O-3, -4, -6, -7, -9, -12, -13 | `DH-E-XHOOKS-1`, `-2` (58.12); `DH-E-XHOOKS-3` (58.13); `DH-E-XHOOKS-5` (58.14) | Hooks confined unless `confine: none` per source ([DH-ADR-31](../../harness/design.md#dh-adr-31)); webhook listener serves nothing else |
| M5-CAP-11 | Inspector, profiles, event reader, timeline, recall, vendor chat import | 56.1..56.9 | DH-O-2, -4, -5, -6, -12 | G: `DH-E-XINSPECT-4` (56.10); `DH-E-XINSPECT-1`, `-2`, `-3` (58.15) | Default-off entries; timeline reports have a second visible switch (unconfirmed default) |
| M5-CAP-12 | Live sharing: one invited browser watches one session, read-only, no relay | 57.1..57.3 | DH-O-7, -12 | `DH-E-XSHARE-1`, `-2` (58.16) | `x.session-share` entry off; the owner creates each share ([DH-ADR-37](../../harness/design.md#dh-adr-37)) |
| M5-CAP-13 | Translation, whole-conversation titles, feature recommendations, image viewer, restart-needing settings in the browser, six more UI languages | 57.4..57.12 | DH-O-2, -4, -6, -8, -10, -16 | `DH-E-XMISC-1` to `-4` (58.17) | Two optional bundles; web translation not shipped and a sender if an operator adds it ([DH-ADR-36](../../harness/design.md#dh-adr-36)) |
| M5-CAP-14 | Known limits of the host, UI and experimental areas | 59.1..59.15 | DH-O-14, -16 | No scenario; a 207-row table against pages and surfaces | Documentation; nothing changes behaviour |

Count check: 17 guarded + 46 in DH-S-58 = **63 scenarios executed in this story**
(of 246 in the plan). Every one appears in exactly one row above, and every step of
S49..S59 appears in exactly one row.

### 2.2 Stages

Each stage enters only when its entry is met (impl.md sec. 1.4 rule 1) and closes on
the evidence record of impl.md sec. 7.3. No stage exceeds 20 steps; the largest is
DH-S-58 with 19.

| Stage | Result (impl.md, condensed) | Entry (impl.md) | Pri | Steps | Scenarios | Exit evidence |
|---|---|---|---|---|---|---|
| [DH-S-49](../../harness/impl.md#dh-s-49) | Four `src/go` changes as one tagged change, then eleven small P2 pieces | S48 closed | P2 | 15 | 3 authored for S58 | Evidence record; existing `src/go` tests still pass |
| [DH-S-50](../../harness/impl.md#dh-s-50) | Opt-in senders, confirmed uploads, the importer, `dsh update` and its notice, "open in"; closes H-15, H-20 | S48 closed. External: vendor upload endpoint and opted-in test account; real upstream sessions; a published release to update from | P2 | 9 | 5 guarded (50.9); others authored for S58 | Proxy capture before/after consent; home before/after import; binary checksums before/after update; H-15, H-20 struck |
| [DH-S-51](../../harness/impl.md#dh-s-51) | Four cloud provider components behind a build tag; vendor sign-in panel | S35, S43. External: real cloud accounts | P2 | 6 | 2 guarded (51.6); 3 authored for S58 | Proof the default binary lacks the four components; active sign-ins at vendor and issuer after 51.6 |
| [DH-S-52](../../harness/impl.md#dh-s-52) | Process children, installed runtimes, client tools, MCP accounts, connectors, language servers, authored plugins, background workflows; closes H-16..H-18 | S25, S29, S31, S33, S34, S37, S51. External: Claude Code, Codex, an ACP agent, language servers, a hosted MCP service | P2 | 14 | 4 guarded (52.14); 10 authored for S58 | Version of each runtime; plugin directories before/after 52.14; authorisations listed by each service; H-16..H-18 struck |
| [DH-S-53](../../harness/impl.md#dh-s-53) | Browser-use and computer-use seams and providers; dictation | S33, S41. External: Chrome; Cua Driver on a logged-in desktop; a speech recogniser | P2 | 7 | 6 authored for S58 | Process list after each run |
| [DH-S-54](../../harness/impl.md#dh-s-54) | Worktrees in and out, cleanup of owned checkouts only, status registry and GitHub/GitLab providers, `isolation: worktree` | S13, S26, S41, S42, S52. External: Git; `gh`, `glab` signed in; scratch repositories | P2 | 8 | 5 guarded (54.7) | `git worktree list` and branch lists before/after; PRs and MRs closed and branches deleted at the hosts |
| [DH-S-55](../../harness/impl.md#dh-s-55) | Hook runner and hook files, auto review, webhooks, loops, OAR, agent terminals | S27, S29, S30, S42, S43, S52. External: OAR with Node and one runtime; a GitHub repository that sends deliveries | P2 | 8 | 6 authored for S58 | Hook audit events of the manual check |
| [DH-S-56](../../harness/impl.md#dh-s-56) | Inspector, profiles, event reader, two work views, task tree, reports, recall, chat import | S38, S44, S49, S50, S52. External: a real vendor chat export | P2 | 10 | 1 guarded (56.10); 3 authored for S58 | Session list of the scratch home before/after each import |
| [DH-S-57](../../harness/impl.md#dh-s-57) | Live sharing, translation seam and provider, titles, recommendations, remaining UI | S21, S23, S41, S42, S43. External: a second person's browser on another machine | P2 | 12 | 6 authored for S58 | Access-check table for the viewer principal |
| [DH-S-58](../../harness/impl.md#dh-s-58) | Every later capability shown against the real thing it depends on | S49..S57 built; guarded steps 50.9, 51.6, 52.14, 54.7, 56.10 passed; S48 closed | P2 | 19 | 46 executed | One record per scenario; version of every external program; account class used, never the credential |
| [DH-S-59](../../harness/impl.md#dh-s-59) | 207 carried rows published as known limits; second run of `DH-D-TOOLCHAIN-10` | S58 closed | P2 | 15 | None | Table of the 207 unit IDs against page, product surface and asserting test |

Size facts: **123 steps**; the build stages hold **83 P2 deliverables** (the other two
P2 deliverables of the 85, TOOLS-6 and UISIDE-9, were pulled forward into P1 stages)
plus four held-open closures; **63 scenario executions**; **207 carried rows**.

**Order and parallelism.** S49 and S50 start first (they need only S48). S51 runs
beside them. S52 needs S51 (for `DH-E-UISET-5`). S53 and S57 need only M4 stages and
can run beside S52. S54 and S55 need S52. S56 needs S49, S50 and S52. S58 batches all
of them; S59 follows S58. Implementation dependencies (what must be built first) are
these entry lines. **Runtime prerequisites** (impl.md sec. 3.4: Git, Node for OAR,
Chrome, `gh`/`glab`, installed runtimes) never block a build stage; they block only the
scenario that needs them, which then stays open.

### 2.3 What M5 lifts from M4's enabling restrictions

impl.md's M4 row lists features kept disabled "because a scenario that proves them
runs later". These become eligible for enabling only here, and only on the platforms
where their scenarios passed (impl.md sec. 1.4 rule 7). "Eligible" does not mean
switched on: experimental entries stay default-off and senders stay off until the
user opts in (M5-OPT-1, M5-OPT-2).

| ID | Restriction at M4 | Lifted by | Deliverables that become done |
|---|---|---|---|
| M5-L-1 | Vendor-account sign-in | `DH-E-HUMAN-4`, step 51.6 | DH-D-HUMAN-8, DH-D-HUMAN-9 |
| M5-L-2 | The host-status page's update path | `DH-E-WEBAPP-3`, step 50.9 | DH-D-WEBAPP-5 (and DH-D-WEBAPP-1, see M5-Q-9) |
| M5-L-3 | P1 deliverables held open on P2 work | Closing steps 50.8 (H-15, H-20) and 52.13 (H-16, H-17, H-18) | DH-D-HUMAN-10, DH-D-UISIDE-9, DH-D-UISET-7, DH-D-UIWORK-6 |
| M5-L-4 | P1 parts proven only together with P2 work | `DH-E-HOST-3`, `DH-E-UISET-2`, `DH-E-HUMAN-5` (58.2), `DH-E-UIWORK-2` (58.5) | DH-D-HOST-4, -5 and -6 (impl.md sec. 10.4, S-4), DH-D-UISET-3, DH-D-HUMAN-10, DH-D-UIWORK-4, -6, -7 |
| M5-L-5 | P2 features "not part of M4": a model-attached plugin, `isolation: worktree`, `dsh update` | `DH-E-BOOT-5` (52.14); `DH-E-SUBAGENT-7` (54.7); `DH-E-ELECTRONUPDATER-2` (50.9) and `-1` (58.2) | DH-D-BOOT-10; DH-D-SUBAGENT-10 and the P1 DH-D-SUBAGENT-6; DH-D-CLI-8, DH-D-WEBAPP-10 |
| M5-L-6 | P0 deliverables whose last scenario runs in S58 (impl.md sec. 10.4, S-1) | S58 batch | DH-D-HOST-4, DH-D-HOST-8, DH-D-UISIDE-8 |

### 2.4 Work products in scope

1. **M5-W-1 Code** for the 83 deliverables of steps 49.1 .. 57.12 and the four
   held-open closures, each under the package family design.md sec. 4.2 names
   (`internal/x/...` for X1..X6; `internal/llm/provider/...` behind the build tag;
   `internal/update`; the area's own package otherwise).
2. **M5-W-2 Local tests** named in each deliverable's "Done when" line, under the gate
   runner, with the 85% coverage floor (QG-3) and no exemption list (QG-2).
3. **M5-W-3 Manual-check records**, one per step, labelled `manual` (EV-3). The named
   checks of each stage (for example: with the sender off, attempt each upload; run
   `dsh update` beside a running host; delete a session whose worktree has uncommitted
   work) are recorded verbatim. A manual check never ticks a scenario.
4. **M5-W-4 Guarded runs**: the 17 guarded scenarios executed in steps 50.9, 51.6,
   52.14, 54.7 and 56.10, on scratch homes, scratch repositories and test accounts,
   each with its cleanup evidence.
5. **M5-W-5 Authored scenarios**: the 46 scenarios of DH-S-58, written against the
   scenario helper (`DH-D-UTIL-8`), reviewed, and listed in the credentialed workflow's
   required list (impl.md sec. 1.4 rule 4).
6. **M5-W-6 The DH-S-58 batch**: one report, one record per scenario, run per
   environment group (sec. 4).
7. **M5-W-7 Known-limits pages** for the 207 rows of S59, regenerated into the
   documentation website by the second run of `DH-D-TOOLCHAIN-10`.
8. **M5-W-8 Ledger upkeep**: statuses move to *built* in build stages and to *done*
   only on a passed scenario; H-15..H-18 and H-20 are struck; `tools/harness-trace
   verify` passes on every commit (QG-9).

### 2.5 Statuses kept apart

impl.md sec. 1.2 keeps five statuses apart. For this story, today:

| Status | Meaning here | Today |
|---|---|---|
| Design | docs/harness specifications and this file exist and the ledger verifies | Done, as documents |
| Implementation | Code of S49..S57 and S59 merged | Not started (blocked on M4) |
| Test authoring | Local tests and the 63 scenarios written and reviewed | Not started |
| Local passes | Local tests pass under the gate runner | Not started |
| Real-environment acceptance | The 17 guarded and 46 batch scenarios passed | Not started |

### 2.6 Definition of "done" for this story

The story is done when **all** of the following hold at one commit:

- Every step 49.1 .. 59.15 is ticked; every build stage has its evidence record.
- All 17 guarded scenarios passed in their build stages and all 46 scenarios of
  DH-S-58 passed. A missing, skipped, blocked or failed scenario keeps DH-S-58 open and
  is recorded as such, never as a pass (impl.md sec. 1.4 rule 6).
- No held-open item remains (impl.md sec. 10.3: "At M5: none").
- The S59 table maps all 207 carried rows to a page; every row of
  `trace/fragments/K2.jsonl` is found on a page by its unit ID.
- A default installation built from this commit composes no experimental entry and
  sends nothing (checked by `DH-E-HOST-3` and the S51 exit evidence).

---

## 3. Out of scope

| Excluded | Why | Where it lives |
|---|---|---|
| Everything of M1..M4 (DH-S-1..48) | Entry prerequisite, not work of this story | Sibling stories |
| Turning any capability on for users | The milestone makes a capability eligible; the operator (entries) or the user (senders) decides | M5-OPT-1, -2 |
| A plugin registry, download or auto-update | No deliverable and no stage ([DH-ADR-13](../../harness/design.md#dh-adr-13)) | Not built |
| A native computer-use helper, the vendor release publisher, a mandatory-update service, Stagehand, a desktop application | Dropped by owner decision (impl.md sec. 4 "Not built"; DH-ADR-18, -19, -24) | Not built |
| Claude Code mods; action cards, personalised pages, the badge skill | Recommended defaults to drop (I5 Q8, I5 Q10); not built unless the owner reverses them | impl.md sec. 9.1 |
| A relay for live sharing; viewers that act | [DH-ADR-37](../../harness/design.md#dh-adr-37) | Not built |
| Code-host writes from the UI | [DH-ADR-38](../../harness/design.md#dh-adr-38); the agent's own `gh`/`glab` under the pipeline is the only write path | Not built |
| Shipping the web translation provider | [DH-ADR-36](../../harness/design.md#dh-adr-36): specified and tested, not in the shipped bundle | An operator layer |
| Fixing carried limits | S59 publishes them; it changes no behaviour | Known-limits pages |
| Re-auditing docs/harness | Inconsistencies found are recorded (DC-3) and raised (sec. 8) | Owner |

---

## 4. Real-environment acceptance

### 4.1 How a run proceeds (impl.md sec. 7.2, applied)

1. **Identify the build.** Record the source commit, the binary's checksum and
   embedded version. A scenario never builds its own binary. Scenarios that need the
   cloud components use the **tagged** build and record its checksum separately.
2. **Preflight.** The credentialed workflow fails before any scenario starts when a
   credential is missing, an environment variant is absent or an external program is
   not found. The required list is `DSH_REQUIRE_SCENARIOS`; a listed scenario that
   skips or does not run fails the job.
3. **Run.** From the repository root on the environment's machine (the controller may
   be a developer machine reaching it over SSH):
   `DSH_BIN=<built dsh> DSH_REQUIRE_SCENARIOS=<ids> DSH_SCENARIO_REPORT=<file> go test -tags scenario -run '<pattern>' -timeout <job limit> -count=1 ./test/scenario/...`
   (variable names as fixed by the scenario helper in DH-S-16; impl.md marks them as a
   proposal). Each scenario creates its own scratch home and never touches the
   tester's.
4. **Deadlines.** A scenario that reaches its deadline has failed, not skipped.
   Starting values (impl.md: "Inference: not measured"): accounts and model APIs 15 min
   per scenario / 2 h per job; external programs 15 min / 2 h; browsers 10 min per
   browser / 2 h; OS machines 20 min / 3 h per variant; other parties' software and
   clean machines 30 min / 4 h.
5. **Report.** One record per scenario (impl.md sec. 7.3), one report per acceptance
   stage; guarded runs attach to their build stage's record.
6. **Cleanup and recovery.** Each scenario runs its own Cleanup. If cleanup fails the
   run keeps the scratch home, the fatal report, the process list and the session
   exports, marks the scenario failed, and restores the baseline by the environment's
   own means (sec. 4.2, last column).

Credentialed runs happen on a schedule, on pushes to `main` and on manual dispatch,
never on a pull request from a fork (QG-10).

### 4.2 Environment requirements

DH-S-58 runs **by environment group** so that each account or program is set up once
(impl.md DH-S-58 "Sequence"): cloud accounts and senders, installed runtimes, desktop
automation, code hosts, two-person scenarios. impl.md environment IDs (E1..E8) are in
brackets.

| ID | Environment | Needed by | Target discovery | Supplied at run time (never stored) | Preflight fails when | Cleanup and recovery |
|---|---|---|---|---|---|---|
| M5-ENV-1 | Cloud accounts and issuers (E1, E6): AWS with model access, a Google Cloud project, an Azure OpenAI deployment with an Entra identity, an OAuth issuer, the vendor account | 51.6, 58.1 | Route ids in the scenario's configuration layer; credentials found by each standard chain | AWS chain, application-default credentials, Entra identity, test-account sign-in | A chain yields nothing; the tagged binary is absent | Sign out; delete the scratch home; list active sign-ins at vendor and issuer. If a grant survives, the run is failed and the grant is revoked by hand from the account console before anything else runs |
| M5-ENV-2 | Senders and collectors (E6): recording collector, TLS-intercepting proxy with a per-run certificate, vendor upload endpoint with an opted-in test account | 50.9, 58.2, 58.18 | Collector and proxy started by the scenario on the test machine | Proxy address and per-run certificate; test-account credentials; a model key | The proxy cannot see the host's traffic | Delete the proxy certificate, the capture (it holds session text) and the home. An upload already accepted by the vendor cannot be recalled: test sessions carry synthetic text only |
| M5-ENV-3 | Update channel (E2): two signed candidates N and N+1 with a test update key; a private feed with request logging; hosted Linux, macOS, Windows runners; a read-only copy of N | 50.9, 58.2 | Feed address in configuration; binary path given to the scenario | Test update key (release environment only) | A candidate is unsigned; a runner OS is missing | Stop the host; remove directories, feed and test key. If a swap was killed between its renames, the previous binary is beside it under its known name and is renamed back; the listing is kept |
| M5-ENV-4 | Installed runtimes and servers (E4, E8): Claude Code, Codex, an ACP agent, OAR with Node and one runtime, language servers, a hosted MCP service with a test account, Python with a wheel installer, LibreOffice, tmux | 52.14, 58.4..58.8, 58.19 | The product's own discovery; versions recorded | The tester's own sign-ins already on the machine; hosted-service test account | A program is not found by discovery | Delete scratch home and plugin directories; revoke the hosted authorisation and list authorisations; process list shows no child left |
| M5-ENV-5 | Desktop automation (E5, E8): installed Chrome; Cua Driver on a logged-in, unlocked macOS and Windows desktop; a speech recogniser | 58.9..58.11 | Provider profile; browser channel | Browser channel and profile directory | Desktop locked; driver permissions not granted | Fresh browser profile deleted; process list after host stop shows no browser; desktop session logged out |
| M5-ENV-6 | Code hosts and repositories (E2, E8): Git on Linux, macOS, Windows; `gh` and `glab` signed in by the tester; scratch repositories locally, at github.com and gitlab.com; a GitHub repository whose webhook reaches the ingress port through a TLS tunnel | 54.7, 58.13 | Remote addresses of the scratch repositories; tunnel address | Webhook secret created per run (stored as a credential); the tester's sign-ins | A CLI is not signed in; the tunnel is down | `git worktree list` and branch lists compared with before; PRs/MRs closed, branches deleted at the hosts; webhook and tunnel removed; reported Git commands run for any `uncertain` row |
| M5-ENV-7 | Browsers and a second person (E5): real Chromium for owner and viewer on two machines, a test certificate, Firefox and Safari for engine-sensitive steps (a manual checklist until the second driver exists) | 50.9 (`DH-E-WEBAPP-3`), 58.3, 58.5, 58.14, 58.16, 58.17 | The launch link read from the host's own terminal | Launch link; test certificate | The second machine cannot reach the host | Stop or let expire every share; fresh profiles deleted; viewer browser-session rows shown gone |
| M5-ENV-8 | Foreign data (E8): real upstream sessions; a real vendor chat export | 50.9 (`DH-E-STORE-3`), 56.10 | Files placed by the tester | None | Files absent | The scratch home and its session list before and after import are kept; the home is deleted |

### 4.3 Ownership, failure, rollback and cleanup where it matters

What cleanup may undo depends on who owns the thing. **Created** means the harness
made it in this run; **shared** or **entered** means another session or the user owns
it; **uncertain** means a crash left an operation half-done and nobody owns it until
a person decides.

| Resource | Harness owns | Harness does not own | On failure | Never |
|---|---|---|---|---|
| Worktree and branch (XFORGE-2, -7) | A checkout whose session's own log holds `worktree/created` | A worktree another session created and this one entered; anything that existed before; any branch | A journal row is written before the first Git command; rollback undoes only what its row created; a dead host's `running` rows become `uncertain` and are reported with the two Git commands, never acted on | A forced remove; deleting a dirty checkout; deleting a branch (except an isolated child's branch still at its base commit) |
| Program file (`dsh update`, electron-updater sec. 6) | The downloaded archive (deleted at command end) and the kept previous copy (removed after the new version first starts) | A binary a package manager installed, or one in a read-only location: refused | A failed swap renames the previous file back; a new binary that reports the wrong version is replaced by the previous one; a running host keeps its old code and shows "restart to finish updating" | Swapping on a host's request (no HTTP route can start a download); going back across a database layout change without restoring the automatic backup |
| Live share (XSHARE-1..3) | The share binding, viewer browser-session rows, audit rows | What the viewer already saw | Stop or expiry removes the binding, ends the viewer's streams and deletes the viewer's row; a host crash ends every share and the next start deletes all viewer rows before admitting a request | A share without a visible owner badge; a viewer that prompts, answers or uploads |
| Cloud tokens and grants (LLM-7..10, HUMAN-8, -9) | A refreshable grant stored under a cross-process lock, revoked on sign-out | The cloud account and its resources (nothing is created there) | An expired token is refreshed once under the lock; a missing credential fails with `llm/missing-credential` before any I/O; no other route is tried (SP-2) | A grant left at an issuer after cleanup (guarded); a credential in a log, page or record (SP-11) |
| Hosted authorisations (CONNECT-3, -4, UISET-5) | The authorisation this run created | The hosted service's data | Revoked in Cleanup; the service's own list is the evidence | Leaving an authorisation behind silently |
| Imported sessions (STORE-10, XINSPECT-7) | Sessions written into the scratch home | The source files | A file the importer cannot map is refused whole and nothing is written | Importing into a tester's real home |
| Model-written plugin (EXT-7, BOOT-10) | The approved copy and the session attachment | The profile and other sessions | Detach drops the attachment; the approved copy is dropped in Cleanup | A durable profile change by the model (NG-9) |

---

## 5. Non-goals

The M1 non-goals [NG-1 to NG-12](../harness-DSH-M1/tech-spec.md#4-non-goals) hold
unchanged. The ones this story is most likely to drift into:

| ID | Non-goal | Decision | What it rules out in S49..S59 |
|---|---|---|---|
| NG-4 | cgo in the shipped binary | DH-ADR-15 | Cloud credential chains, the Windows publisher check and the speech provider must be pure Go or external processes |
| NG-6 | An embedded interpreter | DH-ADR-8 | OAR's Node and Playwright MCP run as operator-installed external programs, never inside `dsh` |
| NG-7 | Data leaving the machine by default | DH-ADR-9, -27, -32 | Every sender of M5 ships off; no milestone, bundle or recommendation turns one on (M5-OPT-2) |
| NG-9 | Durable configuration changed by the model | DH-ADR-30 | `plugin_manager` attaches to the own session only; the recommendations tool names a feature and an owner switches it on |
| NG-10 | A stronger security claim than design.md sec. 6 | design.md sec. 3.3 | OAR runs outside the sandbox and an external run reports `confined: by-product`; `confine: none` hooks are stated as unconfined |
| NG-12 | A desktop shell | DH-ADR-18 | Computer use and "open in" never add a native window toolkit |
| M5-NG-1 | A milestone that switches a feature on | impl.md sec. 3.3 | No M5 step changes a shipped default; S59 changes no behaviour |
| M5-NG-2 | A relay, a viewer that acts, or a UI that writes to a code host | DH-ADR-37, -38 | No relay protocol or gateway; no merge, comment or create button |
| M5-NG-3 | An update applied without the user running `dsh update` | DH-ADR-CLI-8, DH-ADR-32 | No background install, no mandatory-update service, no swap of a package-manager binary |

---

## 6. Hard constraints

### 6.1 Inherited from M1

RM-1..RM-10, QG-1..QG-10, EV-1..EV-5 and EV-7, SP-1..SP-14, EN-1..EN-6 and DC-1..DC-6
of the [M1 tech-spec sec. 5](../harness-DSH-M1/tech-spec.md#5-hard-constraints) hold.
Three apply with M5 specifics: **RM-1** -- the four S49 changes to `src/go` are one
tagged change of that module (impl.md S49 "Sequence"); **SP-12** -- every external
runtime, hook, `gh`/`glab` and language server starts through the one spawn path and
is stopped as a tree; **EN-3** -- every credential, not only the model key, is supplied
at run time and never written to a record. **EV-6** (real API only in manual checks)
was an M1 rule and does not apply: here real APIs are reached by scenarios.

### 6.2 Default-off and opt-in rules

| ID | Rule | Source | Checked by |
|---|---|---|---|
| M5-OPT-1 | Every experimental capability is a default-off entry; an operator turns it on per installation in a configuration layer. A stock profile composes none | impl.md sec. 3.3 M5; design.md sec. 4.2 | Composition test per profile; `DH-E-HOST-3` |
| M5-OPT-2 | A sender is never enabled by a milestone. Each data kind (telemetry, operational events, analytics, session upload, feedback upload, the automatic update check, the client version, a cloud speech endpoint, an operator-added web translation provider) is a visible setting off until the user turns it on; one off-switch stops them all | impl.md sec. 3.3; DH-ADR-9, -27, -32, -36; I5 Q13 | `DH-T-CLI-15`; `DH-E-HOST-3`, `DH-E-UISET-2`, `DH-E-STORE-4`, `DH-E-HOST-12` |
| M5-OPT-3 | Explicit requests are not senders: `dsh update` and `--check` run with the off-switch set and print the address first; a share the owner created stays up; a configured status read still runs. The settings page says the switch is not a network kill switch | DH-ADR-27 | `DH-T-CLI-15`; `DH-E-XSHARE-2` |
| M5-OPT-4 | Cloud sign-in components are behind a build tag and absent from the default binary; consumer-subscription OAuth is built per vendor only where its terms permit a third-party client | DH-ADR-25; I1 Q7, Q8 | S51 exit evidence |
| M5-OPT-5 | Client-declared tools are off until accepted; a connector's skill set comes from a local directory only | I3 Q5, I2 Q11 | S52 local tests |
| M5-OPT-6 | A hook is confined unless the operator writes `confine: none` for that hook source; the OAR bridge needs Node, runs outside the sandbox and says so | DH-ADR-31; I5 Q14 | S55 manual check; `DH-E-XHOOKS-1` |
| M5-OPT-7 | A webhook is accepted only after its signature verifies, on a listener that serves nothing else; the session it starts uses the configured preset | DH-D-XHOOKS-4, -5 | `DH-E-XHOOKS-3` |
| M5-OPT-8 | A live share is created by the owner for one session, ends on time, is always visible to the owner, and gives the viewer read-only access | DH-ADR-37 | `DH-E-XSHARE-1`, `-2`; S57 access table |
| M5-OPT-9 | `isolation: worktree` is off by default; nothing is merged, fetched or pushed automatically | DH-ADR-40 (U-K1-3); DH-ADR-XFORGE-8 | `DH-E-SUBAGENT-7` |

### 6.3 Evidence rules specific to M5

| ID | Rule | Source |
|---|---|---|
| M5-EV-1 | A guarded scenario runs in its build stage on scratch homes, scratch repositories and test accounts only; the build stage cannot close without it, and its feature is not enabled before it passes | impl.md sec. 7.4 |
| M5-EV-2 | In DH-S-58 no manual check counts | impl.md DH-S-58 |
| M5-EV-3 | Each record names the version of every external program and the account class used, never the credential | impl.md DH-S-58 exit evidence |
| M5-EV-4 | A scenario whose environment is not obtained stays open and keeps its feature disabled. It is reported "not run" with the reason | impl.md DH-S-58 entry; sec. 1.4 rule 6 |
| M5-EV-5 | Scope of the guarantee: a pass proves the tested build, inputs, OS, browser, runtime version and account class only. Untested OAR runtimes are listed as untested in the known limits (fact F-10) | impl.md sec. 1.4 rule 7; sec. 9.3 |
| M5-EV-6 | Before each stage starts, the owner confirms or changes the unconfirmed defaults impl.md sec. 9.1 names for it (U-K1-11 at S50; I1 Q7/Q8 at S51; I3 Q5, I2 Q11, U-K1-4 at S52; I5 Q13 at S53; U-K2-12 at S54; I5 Q8, Q14 at S55; I5 Q12 at S56; I5 Q10, U-K2-13 at S57) | impl.md sec. 9.1; DC-4 |

---

## 7. Identified risks

L = likelihood, I = impact (High / Medium / Low). No response relaxes a gate.

| ID | Risk | L | I | Trigger | Mitigation (before) | Response (after) |
|---|---|---|---|---|---|---|
| M5-R-1 | **Environments not obtained.** 63 scenarios need about eight environment groups (cloud accounts, logged-in desktops, installed runtimes, a second person, code-host accounts, a GitHub tunnel); one missing keeps DH-S-58 open | H | H | A preflight failure that repeats across runs | Inventory and request every account and machine at story start (sec. 4.2 is the list); dedicate an E8 machine; run S58 by environment group | The scenario stays open and its feature disabled (M5-EV-4); M5 is not reached. Escalate to the owner which capability to defer rather than mark a pass |
| M5-R-2 | **Live credentials left at third parties**: an OAuth grant, cloud token, hosted MCP authorisation, webhook secret or a code-host branch survives a failed cleanup | M | H | The post-run list of sign-ins or authorisations is not empty | Guarded steps 51.6, 52.14, 54.7 with test accounts only; cleanup lists are exit evidence; SP-11 redaction tests | Revoke by hand before any further run; rotate the account; record the leak and add a regression test |
| M5-R-3 | **`dsh update` leaves a machine without a working binary**, or replaces a file another tool owns | M | H | `DH-E-ELECTRONUPDATER-2` fails on one OS; a package-manager path is not recognised | Swap within one directory; restore on every failure; refuse unrecognised or unwritable locations; the macOS and Windows publisher check needs the signed release of S46 | Fail the stage; the previous binary is kept under its known name; the path patterns (R9) are corrected |
| M5-R-4 | **Worktree cleanup removes user work** or acts on a worktree the harness did not create | L | H | A diff between before/after `git worktree list` not explained by an owned checkout | Ownership from the session's own log; non-force remove; branches never deleted; `uncertain` rows only reported | Stop; restore from the scratch repository; fix and re-run 54.7 |
| M5-R-5 | **Data leaves before consent** through a new sender, the version string, cloud speech or the web translation provider | M | H | Proxy capture shows a request before opt-in | Every sender registers through egress (SP-13); `DH-T-CLI-15`; S50 manual check with the sender off | Fail the stage; remove the sender's default; the off-switch test is extended |
| M5-R-6 | **Other parties' software moves** (Claude Code, Codex, OAR runtimes, Cua Driver, Chrome, `gh`/`glab`, the chat export format) between authoring and S58 | H | M | A scenario fails on a version not seen before | Record versions in every run; pin the tested versions in the known limits | Record the version as a limit or fix the adapter; untested runtimes stay listed as untested |
| M5-R-7 | **Live share exposes more than intended**: the host reachable on a network, a first-come viewer, a route the viewer can call | M | H | The S57 access table has a non-refusal | One access decision draws and enforces; every state-changing route tested against the viewer principal | Fail the stage; the share stays default-off |
| M5-R-8 | **Size.** 123 steps, 83 deliverables, 63 scenario runs and 207 known-limit rows | H | H | Half the budget spent before S54 is built | Stages are independent where entry lines allow (sec. 2.2); each closes on its own record | Re-plan the remainder as a follow-up story; never mark a partial stage built (M5-Q-1) |
| M5-R-9 | **Framework regression.** The S49 `src/go` changes break M1..M4 behaviour after a public release | M | H | Existing `src/go` tests or the TC-02b golden change | One tagged change; existing tests under race; golden diff reviewed | Revert; a patch release follows the M4 release process |
| M5-R-10 | **Unconfirmed defaults or spec conflicts surface mid-build** (U-K1-11, U-K2-13, sec. 8) | M | M | Two specifications give different contracts for one seam | Ask the owner before the stage starts (M5-EV-6) | Record both citations and the choice in the evidence record (DC-3) |
| M5-R-11 | **Inbound exposure for webhooks.** The TLS tunnel makes a listener reachable from the internet | M | M | A request other than a delivery reaches the ingress | The ingress serves nothing else (UI URL gets 404); the tunnel lives only for the run | Close the tunnel; fail the scenario; review the ingress routes |

---

## 8. Assumptions, open questions and inconsistencies found

### 8.1 Assumptions

| ID | Assumption | If false |
|---|---|---|
| M5-A-1 | M4 is reached: DH-S-48 closed, a signed release exists, release hosting is decided (needed for update metadata, S50 entry) | The story does not start; S50's update steps wait for hosting |
| M5-A-2 | The team can obtain every environment of sec. 4.2, including a second person and logged-in desktops | M5-R-1 response |
| M5-A-3 | A test update key and its storage are decided by the release owner | `DH-E-ELECTRONUPDATER-1`, `-2` cannot run |
| M5-A-4 | Each vendor's terms are checked before DH-D-LLM-10 is built for it | LLM-10 is built for no vendor; `DH-E-LLM-10` cannot pass and stays open |

### 8.2 Open questions

| ID | Question | Why it matters |
|---|---|---|
| M5-Q-1 | **Estimate.** What is this story's size? Facts: 11 stages, 123 steps, 83 deliverables plus 4 closures, 63 scenario runs across about 8 environment groups, 207 known-limit rows. M1 (11 stages, about 106 steps, roughly 100 deliverables, no scenario runs) was a 13-point story and its tech-spec flagged size as its top risk | M5-R-8; possibly split (for example S49..S53 and S54..S59) |
| M5-Q-2 | **Entry strictness.** impl.md entry lines of S51, S52, S53, S54, S55 and S57 do not name DH-S-48, so impl.md would let them start before M4. This story waits for M4 as instructed. Confirm, or allow early build (never early enabling) | Schedule |
| M5-Q-3 | **Who builds and signs the tagged cloud binary?** M5 lists cloud sign-ins as usable, but they are not in the default binary, and no stage names a tagged build among the release assets that DH-S-46 signs and publishes | Without a distributed tagged build, operators cannot use M5-CAP-6 |
| M5-Q-4 | **Scope of the enabling rule.** It names "experimental capabilities". Non-experimental P2 work (`dsh update`, the importer, uploads, analytics, cloud sign-ins) has its own switch (command, sender setting, build tag). Confirm that is intended, and whether `dsh update` exists in the M4 binary at all | Operator documentation of what is on |
| M5-Q-5 | Is a test upload accepted by the vendor deletable? `DH-E-STORE-4` cleans up locally only | Test data stays at the vendor; synthetic text only until answered |

### 8.3 Inconsistencies found in docs/harness (recorded, not fixed; DC-2, DC-3)

| ID | Inconsistency | Citations |
|---|---|---|
| M5-Q-6 | **S59 row counts.** The step counts of 59.1..59.14 add to 206, the stage says 207. Step 59.6 says 11 rows for ui-core and ui-kit; the register gives 12 (C1 3 + C2 9; the status mark became a scope boundary in the re-sweep). impl.md says the step counts are "those of the first review" | impl.md DH-S-59; [register sec. 2](../../harness/debt/carried-host-ui-experimental.md#2-summary) |
| M5-Q-7 | **Fallback for an oversized upload event (U-K1-11).** carried-core.md: if the vendor rejects an elision record, "the upload component is not shipped". impl.md fact F-9: "the upload stops at the oversized event, as upstream, and says so" | [carried-core.md U-K1-11](../../harness/debt/carried-core.md#u-k1-11); [impl.md sec. 9.3](../../harness/impl.md#93-facts-that-only-a-run-can-establish) |
| M5-Q-8 | **Translation into a session held elsewhere (U-K2-13, step 57.4).** x-recommendations-misc.md refuses with `translate/session-held-elsewhere`; core-session.md (`DH-D-SESSION-9`) says it succeeds. One must change before S57 | [register sec. 3.1a](../../harness/debt/carried-host-ui-experimental.md#31a-loose-ends-from-the-convergence-round) |
| M5-Q-9 | **When DH-D-WEBAPP-1 is done.** impl.md sec. 10.4 S-1 says at DH-S-48; step 50.9 says `DH-E-WEBAPP-3` also proves it, which would move its done status to S50 | impl.md sec. 10.4; DH-S-50 step 50.9 |
| M5-Q-10 | **M4's "still disabled" list omits DH-D-SUBAGENT-6** (P1), although step 54.7 says `DH-E-SUBAGENT-7` proves it | impl.md sec. 3.3 M4 row; DH-S-54 step 54.7 |
| M5-Q-11 | **Environment table.** impl.md sec. 7.1 row E2 (real OS machines) lists stage 54 but not 50, though `DH-E-ELECTRONUPDATER-2` at 50.9 needs hosted Linux, macOS and Windows runners | impl.md sec. 7.1; electron-updater.md `DH-E-ELECTRONUPDATER-2` |
| M5-Q-12 | **Update signing is not named as a gate of S50.** The update key's owner and storage are open (electron-updater.md sec. 7 item 1), and the macOS/Windows publisher check needs the signing certificates impl.md sec. 9.2 leaves open for S46; S50's entry names only update-metadata hosting | impl.md DH-S-50 entry, sec. 9.2 |
| M5-Q-13 | **Step order in S54.** Step 54.8 is listed before 54.7 (54.7 is the guarded batch that proves 54.8). Harmless, but a checklist reader may tick out of order | impl.md DH-S-54 |

---

## 9. Coverage of the request

| Element of the request | Where handled |
|---|---|
| Stages DH-S-49..59, results, entry, priority, steps, exit evidence | sec. 2.2 |
| Cloud sign-ins, external runtimes, browser/computer use, worktrees, hooks, inspector, sharing, translation, importer, `dsh update` | sec. 2.1 (M5-CAP-1..14) |
| Data off the machine, binary updates; NG-7, DH-ADR-9, -27, -32 | sec. 0, sec. 5, sec. 6.2 |
| Real-environment execution, credentials, preflight, timeouts, reports, recovery, cleanup | sec. 4 |
| Update rollback, worktree cleanup, share revocation, cloud tokens | sec. 4.3 |
| M4 restrictions lifted here | sec. 2.3 |
| M5 enabling rule, exact | sec. 1.1 |
| Statuses; built versus done | sec. 0, sec. 2.5, sec. 2.6 |
| Carried debt as known limits (S59) | M5-CAP-14, sec. 2.2, M5-Q-6 |
| No story-point number | M5-Q-1 |
