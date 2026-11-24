---
title: "dsh M2 -- first prompt from the command line and the SDK"
storyId: "harness:DSH-M2"
---

# harness:DSH-M2 -- Technical specification (problem, scope, constraints, risks)

> **Story `harness:DSH-M2`**, operator prompt, verbatim: "clone deepseek harness, given
> context in docs\harness of this repo, and design and impl doc, i want you to follow
> impl. This story is milestone M2 of dsh (impl.md stages DH-S-12 to DH-S-18),
> following harness:DSH-M1. Its tech spec is docs/stories/harness-DSH-M2/tech-spec.md."

This document states **what problem this story solves, what is in and out of its
scope, what it will never do, which constraints are hard, and which risks can sink
it**. It is the contract the sibling plan documents build against:

| Sibling | Owns | Relationship to this file |
|---|---|---|
| [architecture.md](architecture.md) | Components C31..C57, data model, interfaces, flows F1..F7, invariants INV-1..INV-14, the **Test & Environment Contract** (TC-01a .. TC-21s5, `service:*` names) | This file adopts its component ids, TC ids and service names; it does not repeat component tables, signatures or flows |
| [implementation-plan.md](implementation-plan.md) | Ordered plan stages, steps and commits | Must stay inside sec. 3, obey every constraint of sec. 7, and give every step a TC row |
| [e2e-scenarios.md](e2e-scenarios.md) | `DH-E-*` scenario scripts and their run schedule | Must run exactly the scenarios of sec. 4.2, author those of sec. 4.5, and honour M2-C-2, M2-C-7 |
| [M1 decision log](../harness-DSH-M1/decisions.md) | Every owner, operator and lead decision in force since M1 | Rows here that a decision changed cite it by name; this story adds no row there (DC-2) |

**Source documents** (the requirements; this story never re-decides them):
[docs/harness/README.md](../../harness/README.md), [design.md](../../harness/design.md)
(outcomes `DH-O-1..16`, owner decisions `DH-ADR-1..40`),
[impl.md](../../harness/impl.md) (stages, steps, rules sec. 1.4, milestone table sec.
3.3, environments sec. 7.1, scenario index sec. 7.5, held-open list sec. 10.3), the area
and library specifications under `docs/harness/areas/` and `docs/harness/libs/`, and
the ledger [trace/ledger.jsonl](../../harness/trace/ledger.jsonl). The M1 story's
constraint ids (NG-*, RM-*, QG-*, EV-*, SP-*, EN-*, DC-*) are reused where they still
hold ([M1 tech-spec](../harness-DSH-M1/tech-spec.md) sec. 4, 5); ids new to this story
carry the prefix `M2-`.

**History of this file.** A first M2 tech-spec was written before the M1 gap closure
and removed in #96 together with the architecture rewrite. M1 documents cite its ids
(M2-DEC-1, M2-EN-1, M2-R-01, M2-A-1: [M1 gap analysis](../harness-DSH-M1/gap-analysis.md)
sec. 10 item 4, [M1 gap-closure plan](../harness-DSH-M1/gap-closure-plan.md) step
12.1.9). This rewrite **keeps every such id with the same meaning** and folds in the
amendments step 12.1.9 drafted for them, because the owner decisions they depend on
(`platforms-windows-wsl`, `models-signed-in-claude-copilot`, `go-floor-1-27-4`) are in
force. Where the old text said "DeepSeek key", "three CI operating systems" or
`built-through: DH-S-7`, it is historical.

---

## 1. Problem statement

### 1.1 What the operator asked for, read precisely

| Phrase in the prompt | Meaning adopted in this story | Source that fixes the meaning |
|---|---|---|
| "clone deepseek harness" | Continue building `dsh`, the **Go re-implementation** of the TypeScript deepseek-harness: not a fork, not a source port, not a wire-compatible drop-in. Upstream is the requirements source only | design.md sec. 3.1, DH-ADR-2, non-goals NG-1, NG-2 |
| "given context in docs\harness of this repo, and design and impl doc" | The area and library specifications, design.md and impl.md are the **only** design input; this story adds no requirement they do not state | docs/harness/README.md; DC-2 |
| "i want you to follow impl" | Execute impl.md stages **in its order, with its step ids, its rules (sec. 1.4 rules 1-9), its evidence record (sec. 7.3) and its scenario placement (sec. 7.5)** | impl.md sec. 1.1, 1.4 |
| "milestone M2 of dsh" | Reached when [DH-S-17](../../harness/impl.md#dh-s-17) and [DH-S-18](../../harness/impl.md#dh-s-18) are **closed** and the guarded scenarios of [DH-S-16](../../harness/impl.md#dh-s-16) (step 16.8) **have passed**. Not "the code exists" | impl.md sec. 3.3, M2 row |
| "first prompt from the command line and the SDK" (story title) | `dsh headless "<task>"` answers a prompt in a terminal; `dsh sdk` serves a client over stdio; a Go program drives the built binary through the public client package `src/sdk/sdk`; each against a real model, with file and shell tools running inside the fence and the Linux sandbox | impl.md DH-S-16 "Result"; sec. 3.3 M2 "What is usable" |
| "impl.md stages DH-S-12 to DH-S-18" | Five **build** stages (S12 files and policy, S13 sandbox and shell, S14 presets, S15 listener and SDK server, S16 launchers and evidence runners) and two **acceptance** stages (S17 real model, S18 real operating systems) | impl.md sec. 5, sec. 6 |
| "following harness:DSH-M1" | M1 ("A turn runs", DH-S-1..11) is this story's **entry prerequisite** (M2-DEC-1); M1's decisions stay in force; M1's open items are not silently inherited as done | impl.md sec. 1.4 rule 1; [M1 decisions](../harness-DSH-M1/decisions.md) sec. 1.1 |
| "Its tech spec is docs/stories/harness-DSH-M2/tech-spec.md" | This file. It is the contract; architecture.md is the design | This header |
| Story points 0 | No size is implied by the operator. Size facts are in sec. 3.2; the split question is M2-Q-1 | sec. 10 |

### 1.2 The problem in one paragraph

M1 leaves a spine that runs one turn when a Go test composes it: boot, the SQLite
store, the model runtime with the signed-in Claude Code and Copilot client routes, the
guarded tool pipeline, approvals and the agent loop. It has **no binary a person can
point at a task, no place where a model-chosen file edit or command may run, and no
evidence from outside a test double**. The `headless` and `sdk` profile bundles are
empty placeholders (`src/harness/internal/boot/profile/bundles/headless.yml`,
`sdk.yml`), and none of the twenty packages architecture.md sec. 3.1 adds for M2
exists. The problem of this story is therefore: *turn the spine into a binary that
answers a prompt from the command line and from an SDK program, runs model-chosen
file edits and shell commands through one fenced filesystem seam and a Linux sandbox
that reports its real enforcement, and then prove -- on real machines and against a
real model, through the user's signed-in client -- that M1's turn guarantees and the
new execution world hold*, without enabling anything for users and without letting a
defect in takeover, deletion or process-tree ownership touch anything but scratch
data.

### 1.3 Why this cut, and why it is hard

- **It is the first real evidence for the spine.** Every M1 deliverable whose proof
  includes a scenario is only *built*. S17 is the first place the turn guarantees
  (DH-O-3), logged requests (DH-O-4), honest recovery (DH-O-5) and the tool pipeline
  (DH-O-6) are shown against a real model (impl.md DH-S-17 "Result").
- **It is the first point at which model-chosen commands run** (impl.md DH-S-13
  "Result"). A fence or sandbox defect writes outside the user's roots; a process-tree
  defect leaves processes behind. These are claims about real operating systems
  (DH-O-7, DH-O-10) that only real machines can prove (fact F-6, impl.md sec. 9.3).
- **It opens the first versioned protocol and the first owner-authority surfaces**
  (DH-O-13, DH-ADR-28): the SDK server on stdio and the listener with launch-token
  admission. The wire (`src/sdk/protocol` Version 1) is fixed and must not move.
- **It contains a dependency circle.** Working directory, filesystem seam and policy
  resolver depend on each other (cycle C-3, impl.md sec. 10.2) and must land together.
- **The environments are the long pole.** S18 alone needs about a dozen Linux variants,
  hypervisor hosts that can cut a guest's power, two-account machines and network
  shares (sec. 4.3). The code of S12..S16 is ordinary work; arranging the targets is
  not.
- **It starts from an unclosed M1** (sec. 1.5), so the first obligation is not M2 work.

### 1.4 Vocabulary used in this story

| Term | Plain meaning | Why it matters here |
|---|---|---|
| Execution world | Working directory, filesystem seam, policy resolver, sandbox, shell: everything a tool needs to touch the machine | S12, S13 build it; S18 proves it |
| Fence, write roots | The filesystem seam refuses any path outside the policy's write roots, including through a symbolic link swapped during a write (`fs/sandbox-denied`) | DH-D-FS-4; INV-1 |
| Policy resolver | One component (C33) that turns the user's Permissions choice (`read-only`, `workspace-write`, `danger-full-access`) plus working directory and grants into the `Policy` for one call | DH-D-SANDBOX-1 |
| Confinement backend | The OS mechanism that confines a command. M2 has two, Linux only: **bubblewrap** (`bwrap`, a program that starts a command in a restricted file-system view) and **Landlock** (a kernel feature by which a process limits its own writes) | DH-D-SANDBOX-4; macOS and Windows backends are DH-S-27 |
| Launch stage, `ready`/`go` | The hidden `dsh sandbox-launch` mode: the child confines itself, tries a canary write, reports `ready`, and runs the command only after the host answers `go` | DH-D-SANDBOX-2; architecture.md sec. 4.2; INV-14 |
| Enforcement `Full` / `Partial` / `Unconfined` | What the host computes the command actually gets, with each missing or exposed effect named; shown by `dsh sandbox status` | Never stronger than real (INV-3) |
| Process-tree owner | What lets the host end a command and all its descendants: a delegated scope from `systemd --user`, a writable cgroup v2, or the process-group fallback, which is reported | DH-D-EXEC-2 (M1); proven by DH-E-EXEC-1, -10 |
| Profile, bundle, preset | A profile is a product shape of the one binary (the five of architecture.md C42: `admin`, `core` kernel, `web`, `headless`, `sdk`; the existing `minimal.yml` and `acp.yml` bundles are not among them); a bundle is a set of components; a preset composes one agent's tools, prompt sections and skills | S14, step 16.1 |
| Launcher, admin tree | `dsh headless`, `dsh sdk` start a profile; the admin tree runs offline commands (`dsh secret set`, `dsh config show`, `dsh sandbox status`) and constructs no agent | S16; INV-10 |
| SDK server, Go client | JSON-RPC over the host's stdin/stdout (C48, C49); `src/sdk/sdk` (C53) starts the binary and drives it | DH-O-13 |
| Signed-in route | dsh drives the user's installed official `claude` CLI or Copilot CLI as a child; the client signs in and dsh reads no token; no API key exists anywhere | Decisions `models-signed-in-claude-copilot`, M1-D-PIPE-3, M1-D-PIPE-4 |
| Gate, guarded, acceptance | The three **venues** of architecture.md's contract: the required gate run on both lanes; the step 16.8 scenarios on scratch homes inside S16; the S17/S18 runs that close through an evidence record | sec. 4.1 |
| Built / done | Built: code merged, local tests pass, manual check recorded. Done: built, held-open items closed, every proving scenario passed. Never merged | impl.md sec. 1.3 |

### 1.5 Starting point in the repository (verified on this branch)

| Fact | Value today | Consequence |
|---|---|---|
| M1 closure | **Not closed.** All eleven M1 evidence records read `Closure status: open` (`docs/stories/harness-DSH-M1/evidence/DH-S-1.md` .. `DH-S-11.md`); the DH-S-11 manual runbook says "probe built, not run". The M1 gap-closure plan has merged through Phase 17 (#93); its closing Phases 18 and 19 are pending | M2-DEC-1; risk M2-R-04 |
| [acceptance-index.yaml](../../../test/harness/acceptance-index.yaml) | `built-through: DH-S-8`. Of the 37 scenarios M2 executes, **25 are indexed and authored**: 3 of 4 for step 16.8 (STORE-1, -2, -6), 12 of 14 for S17 (all but SDK-1, SANDBOX-7), 10 of 19 for S18 (TOOLCHAIN-2, CHOKIDAR-1/-2, NODEFS-1, RIPGREP-1/-2, EXEC-1, EXEC-10, HOST-4, STORAGE-6) | 12 to author for execution here, 17 for later stages (sec. 4.2, 4.5) |
| Scenario code | `src/harness/internal/testkit/scenarios/` holds 40 files (`//go:build scenario`), with the helpers `sdkhost_test.go`, `realenv_test.go`, `binary_test.go`, `signedin_routes_test.go`, `signedin_routes_copilot_test.go` | Existing scenarios are retargeted at the built binary, not rewritten (architecture.md I-2) |
| M2 packages | Absent: `session/workdir`, `world/fs`, `world/sandbox`, `human/permission`, `world/shellexec`, `context/instructions`, `host/{listen,auth,launch,web}`, `wire/{stdio,sdkserver}`, `cli/{headless,lifecycle,hostclient}`, `boot/agentpreset`, `session/title`, `llm/replay`, `testkit/{scenario,dshbin}`, and `src/sdk/sdk` | All new work; names are architecture.md sec. 3.1's |
| Existing seams M2 extends | `world/subprocess` (the one spawn place), `llm/provider/{clientroute,claudecode,copilot,deepseek}`, `util/{fsx,search,watch,herr}`, `storage/spill`, `human/{approval,question,secret}`, `agent`, `tools`, `cli` (`tree.go` already reserves the hidden modes `sandbox-launch` and `ssh-helper`) | No second spawn path, no second error type (M2-C-9) |
| Profiles | `boot/profile/bundles/` has `acp.yml`, `core.yml`, `headless.yml`, `minimal.yml`, `sdk.yml`, `web.yml`; `headless.yml` and `sdk.yml` are empty; `admin.yml`, `base.yml`, `optional.yml` do not exist | BUNDLE-1/-2/-3/-6 fill them; golden `composed.golden` changes in those steps only |
| Toolchain | `src/harness/go.mod` states `go 1.27.4` (decision `go-floor-1-27-4`) | RM-2 holds at 1.27.4 |
| [harness-scenarios.yml](../../../.github/workflows/harness-scenarios.yml) | `preflight` on `ubuntu-latest` derives `DSH_REQUIRE_SCENARIOS`; `scenarios` job matrix `[ubuntu-latest, windows-latest]` | Hosted runners cover few S18 variants (M2-DEC-2, M2-R-02) |
| Gate lanes | `tools/harness-gate/lanes/win.ps1` (Windows native), `lanes/wsl.sh` (Linux under WSL); `wsl.sh` neither installs nor checks `bwrap` | Lane provisioning is in scope (architecture.md I-8; sec. 3.4 item 6) |

---

## 2. Outcomes (part A)

Each story outcome is observable, linked to the design outcome it serves, the stages
that produce it and the scenarios that prove it. "Done at M2" says whether its
deliverables can reach *done* here or stay *built* because a proving scenario runs
later (impl.md sec. 10.4, S-1).

| ID | Outcome (observable) | Design outcome | Produced by | Proven by (executed in this story) | Done at M2 |
|---|---|---|---|---|---|
| M2-O-1 | Every tool of a session works in one directory that only the host moves; a write outside the write roots is refused, even through a link swapped mid-write; the six file tools (`read`, `write`, `edit`, `apply_patch`, `glob`, `grep`) change real files exactly as logged | DH-O-7, DH-O-10 | S12 | DH-E-FS-1, DH-E-FS-2, DH-E-FS-3 (guarded), DH-E-VIEW-5, DH-E-SANDBOX-7 | Partly: FS-4 waits for DH-E-FS-5 (S28) |
| M2-O-2 | A model-chosen command starts under its resolved policy or does not start; on Linux it is confined by bubblewrap or Landlock with `Full`/`Partial` reported; on a machine with no backend a confined policy is refused `sandbox/unavailable`; its whole process tree is owned and ended; instruction files reach the model through the seam | DH-O-7, DH-O-1, DH-O-6 | S13 | DH-E-SANDBOX-1, DH-E-NATIVESYSTEM-1..4, DH-E-EXEC-1, DH-E-EXEC-10, DH-E-CONTEXT-2 | Partly: most S13 deliverables wait for S32/S39 |
| M2-O-3 | One binary carries five profiles and their bundles; a preset composes an agent; a coverage check fails on an unbundled component; default profiles send nothing off the machine until a route is configured | DH-O-2, DH-O-14 | S14, step 16.1 | None in M2 (local and golden tests; scenarios run in S22, S39, S48) | No (built only) |
| M2-O-4 | A Go program drives sessions over stdio -- create, resume, prompt, cancel, close, read -- and answers approvals and questions on the same connection; stdout carries frames only | DH-O-13 | S15, step 16.6 | DH-E-SDK-1 | Partly: SDK-4/-5 held open (H-1..H-4) |
| M2-O-5 | `dsh headless` and `dsh sdk` start; stopping is one bounded path; a second `dsh web` finds the running host; a crash is explained at the next start; a secret is stored by `dsh secret set`; `dsh config show`/`validate` preview without constructing components | DH-O-14, DH-O-3 | S16 | DH-E-HOST-4, DH-E-STORE-1, -2, -6 (guarded) | Partly: CLI-2/-4/-5 wait for DH-E-CLI-1, -2 (S27) |
| M2-O-6 | A recorded session replays without a credential against the built binary; a run states which evidence it holds; a required scenario that skips fails | DH-O-15, DH-O-4 | Step 16.7 | DH-E-TOOLCHAIN-1, DH-E-TOOLCHAIN-2 | Partly: UTIL-7 held open (H-10); UTIL-8 waits for DH-E-UTIL-2 (S39) |
| M2-O-7 | Against a real model on Linux, the built binary keeps the turn guarantees, logs exactly the client invocation it made, recovers honestly from a kill, titles sessions and follows a configuration edit | DH-O-2, -3, -4, -5, -6, -8 | M1 stages S2..S11 with S14..S16 | S17: DH-E-SESSION-1, -2, DH-E-AGENT-1..4, DH-E-LLM-1, -3, DH-E-TOOLS-1, DH-E-VIEW-1, DH-E-LOADER-1 | For the deliverables those scenarios finish |
| M2-O-8 | M1 foundations hold on real operating systems: release archives run, files are watched and searched, a file is replaced durably under power loss, stored data is private to its user, a dead host leaves a report | DH-O-14, DH-O-5, DH-O-1 | M1 stages S1, S4, S6 | S18: DH-E-TOOLCHAIN-2, DH-E-HOST-4, DH-E-RIPGREP-1, -2, DH-E-CHOKIDAR-1, -2, DH-E-NODEFS-1, DH-E-STORAGE-6 | For the deliverables those scenarios finish |

---

## 3. In scope (part B)

### 3.1 Entry decision

**M2-DEC-1 -- Entry.** M2 work on a stage starts only when every stage its impl.md
"Entry" line names is built, and its first scenario run starts only when M1 is
*closed* in the sense of the M1 definition of done
([M1 decisions](../harness-DSH-M1/decisions.md) sec. 1.1): all eleven M1 evidence
records closed, `built-through: DH-S-11` in the index, the M1 gap-closure plan's
Phase 18 merged. *Adopted amendment (gap-closure step 12.1.9):* "M1 closed" means the
M1 closure record exists and lists every M1 deliverable's done or built-not-done state
with reason; it does not mean every M1 scenario passed, since several M1 deliverables
are finished only by S17/S18 runs that belong to this story. Build stages S12 and S14
depend only on built M1 contracts (rule 1), so whether they may start before M1
closure is the operator's choice (M2-Q-2, architecture.md `m2-entry-m1-closure`).
Until answered, the default is: **S12..S16 may build on the built M1 contracts; no
M2 scenario run and no M2 stage closure happens before M1 is closed.**

### 3.2 Stages and size

| Stage | Kind | Steps | Entry (impl.md "Entry" line) | Delivers (P0 unless marked) |
|---|---|---|---|---|
| [DH-S-12](../../harness/impl.md#dh-s-12) Working directory, filesystem seam and policy resolver | Build | 12.1-12.5 | S10, S11, S8 | VIEW-10, FS-4, SANDBOX-1, HUMAN-4 (P1), FS-6 |
| [DH-S-13](../../harness/impl.md#dh-s-13) Sandbox and shell tools | Build | 13.1-13.8 | S12, S6, S11 | SANDBOX-2, SANDBOX-3, SANDBOX-4, EXEC-3, EXEC-4, EXEC-8, EXEC-9, CONTEXT-2 |
| [DH-S-14](../../harness/impl.md#dh-s-14) Bundles and presets | Build | 14.1-14.5 | S6, S11 | BUNDLE-1, BUNDLE-2, BUNDLE-4, BUNDLE-5, BUNDLE-6 |
| [DH-S-15](../../harness/impl.md#dh-s-15) Listener, admission, control, routes, stdio and SDK server | Build | 15.1-15.8 | S6, S8, S11, S12, S14 | HOST-1, HOST-2, HOST-3, API-1, SDK-3, SDK-4, SDK-5, SDK-6 |
| [DH-S-16](../../harness/impl.md#dh-s-16) Launchers, admin commands, Go client and evidence runners | Build | 16.1-16.10 | S12-S15 | BUNDLE-3, CLI-2, CLI-4, CLI-5, CLI-9, SDK-8 (P1, Go client `src/sdk/sdk`, C53, step 16.6), UTIL-7 (P1), UTIL-8 (P1), VIEW-4 (P1) |
| [DH-S-17](../../harness/impl.md#dh-s-17) Acceptance on a real model | Acceptance | 17.1-17.9 | S16 | No deliverable; closes evidence for M1 and M2 deliverables |
| [DH-S-18](../../harness/impl.md#dh-s-18) Acceptance on real operating systems | Acceptance | 18.1-18.11 | S16 | No deliverable; same |

Totals: **7 stages, 56 steps, 35 deliverables (30 P0, 5 P1 pulled forward), 37
scenarios executed, 17 more authored for later stages.** At most **11** deliverables
can be *done* at M2 (VIEW-10, SANDBOX-1, HUMAN-4, FS-6, SANDBOX-4, EXEC-3, BUNDLE-1,
SDK-6, CLI-9, SDK-8, VIEW-4), and only if every M2 scenario passes and the S12..S16
defaults of impl.md sec. 9.1 are confirmed. The other **24** stay *built* with a named
finishing stage or held-open item:

| Stays built until | Deliverables |
|---|---|
| S22 (web) | BUNDLE-2, BUNDLE-3, BUNDLE-6, HOST-1, HOST-2 |
| S27 (macOS, Windows, release) | CLI-2, CLI-4, CLI-5 |
| S28 | FS-4 (DH-E-FS-5) |
| S32 | CONTEXT-2 (DH-E-CONTEXT-1) |
| S39 | SANDBOX-2, SANDBOX-3, EXEC-4, EXEC-8, EXEC-9, HOST-3, SDK-3, UTIL-8 |
| S48 | BUNDLE-4, BUNDLE-5, API-1 |
| Held open | SDK-4 (H-1, H-2, H-3), SDK-5 (H-4), UTIL-7 (H-10) |

The implementation plan owns the exact per-deliverable mapping; the counts above are
the bound it must reproduce (inconsistency check M2-I-5).

### 3.3 Order and parallelism

- **Cycle C-3 lands as one unit.** Step 12.1 (working directory, `session/workdir`,
  C31), 12.2 (filesystem seam, `world/fs`, C32) and 12.3 (policy resolver,
  `world/sandbox` and `world/sandbox/policy`, C33) merge together or behind each
  other's contract without an inverted import (impl.md sec. 10.2; architecture.md
  C31-C33). Permissions (`human/permission`, C34, HUMAN-4) is step 12.4 and sits
  outside the cycle: it depends on C33, never the reverse.
- **S14 may run beside S12 and S13**: its Entry line names only S6 and S11.
- **S15 needs S12 and S14** (and S6, S8, S11), so it follows them; it may run beside
  S13.
- **S16 needs S12..S15 all built.** Its Entry line also names "a model API key" as an
  external input; under decision `models-signed-in-claude-copilot` that input is the
  signed-in Claude Code route (M2-I-1). Step 16.8 (guarded STORE/FS runs) is its last
  executable gate before S17/S18.
- **S17 and S18 both need S16 and may run in parallel**, on different environments.
- Every step follows impl.md sec. 1.4: a contract first, a fake beside it, a local
  test and golden update in the same commit, a manual check recorded in the stage's
  evidence record.

### 3.4 Work products

1. **Code** of the S12..S16 steps in the packages of architecture.md sec. 3.1, plus
   `src/sdk/sdk`.
2. **Tests**: unit, contract, golden and fake tests per step, each mapped to a TC row
   of architecture.md's contract (gate venue); the scenario files listed in sec. 4.2
   and 4.5 under `src/harness/internal/testkit/scenarios/`.
3. **Profile bundles**: fill `headless.yml`, `sdk.yml`; add `base.yml`, `admin.yml`,
   `optional.yml`; regenerate `composed.golden` in the same commits.
4. **Evidence records** `docs/stories/harness-DSH-M2/evidence/DH-S-12.md` ..
   `DH-S-18.md` in the impl.md sec. 7.3 form (environment, commit, scenario, result,
   artifacts, closure status).
5. **Index updates** in `test/harness/acceptance-index.yaml`: one entry per scenario
   authored here, `built-through` advanced only when a stage is built.
6. **Lane and workflow changes**: `tools/harness-gate/lanes/wsl.sh` provisions or
   checks `bwrap`; `harness-scenarios.yml` gains the S17/S18 jobs and inputs the plan
   names (no secret value in any file -- SP-1, M2-C-11).
7. **Runbooks** for the S18 variants hosted runners cannot provide (power-cut host,
   two accounts, network shares), as `docs/stories/harness-DSH-M2/evidence/` manual
   procedures, in the M1 DH-S-11 runbook form.

### 3.5 Definition of done for the story

| ID | Condition | Checked by |
|---|---|---|
| M2-DOD-1 | Stages S12..S16 are *built*: every step merged, local tests and goldens green on both gate lanes, each manual check recorded | Evidence records DH-S-12..16; `built-through: DH-S-16` |
| M2-DOD-2 | The four guarded scenarios of step 16.8 passed on scratch homes, on the platforms of sec. 4.2 | Evidence record DH-S-16 |
| M2-DOD-3 | S17 is *closed*: its 14 scenarios ran; every *required* leg passed; every *recorded* leg's result is kept | Evidence record DH-S-17 |
| M2-DOD-4 | S18 is *closed*: its 19 scenarios ran on every variant of sec. 4.3 that is in scope, and each out-of-scope variant is named with the owner decision that removed it. A required variant that is in scope but *not run* leaves S18 **open**; there is no partial closure | Evidence record DH-S-18; C57 close check |
| M2-DOD-5 | The 17 later-run scenarios of sec. 4.5 are authored, indexed with their stage, and compile under `-tags scenario` | Index; gate build |
| M2-DOD-6 | A closure record lists each of the 35 deliverables as *done* or *built* with its finishing stage or held-open item, and each held-open item H-1..H-4, H-10 with its state | `docs/stories/harness-DSH-M2/evidence/closure.md` |
| M2-DOD-7 | Nothing is enabled for users (sec. 4.6); no build is published | Release check; M2-C-12 |

---

## 4. Acceptance scope (part C)

### 4.1 Evidence kinds and venues

| Venue | What runs | Where | Architecture contract rows | Closes |
|---|---|---|---|---|
| Gate | Unit, contract, golden, fake and replay tests of every S12..S16 step | Both gate lanes: `win.ps1` (Windows native) and `wsl.sh` (Linux under WSL), `go 1.27.4` | TC-01a .. TC-18* | *Built* state of a step |
| Guarded | The four step 16.8 scenarios, on scratch homes, inside S16 | Linux (WSL lane plus `bwrap`) for STORE-2; platforms of sec. 4.2 for the rest | TC-19s1, TC-19s2 | Entry of S17 (impl.md DH-S-17 "Entry") |
| Acceptance | S17 (real model) and S18 (real operating systems) | Environments E1, E2 and the provided services of sec. 4.3 | TC-20s*, TC-21s* | *Done* state of the deliverables they finish |

M2 has **no lab venue row**: nothing in S12..S18 needs an E3+ laboratory.

### 4.2 Scenarios executed in this story (37)

"Authored" is the state of `test/harness/acceptance-index.yaml` on this branch. The
platform column is after decision `platforms-windows-wsl` (sec. 4.3, M2-DEC-6).

| # | Scenario | Run at | Platforms in scope | Authored | Note |
|---|---|---|---|---|---|
| 1 | DH-E-STORE-1 | 16.8 guarded | Linux (WSL), Windows | Yes | |
| 2 | DH-E-STORE-2 | 16.8 guarded | Linux (WSL) only | Yes | Claude route plus `bwrap`; `DSH_SCENARIO_STORE2_KEY` replaced by the route input (architecture.md) |
| 3 | DH-E-STORE-6 | 16.8 guarded | Linux (WSL), Windows | Yes | |
| 4 | DH-E-FS-3 | 16.8 guarded | Linux (WSL), Windows | **No** | Two `dsh` processes and an editor; 1,000 edits each |
| 5-16 | DH-E-TOOLCHAIN-1, LOADER-1, LLM-1, LLM-3, VIEW-1, TOOLS-1, SESSION-1, SESSION-2, AGENT-1, AGENT-2, AGENT-3, AGENT-4 | S17 | Linux (E1) | Yes | Retarget to the built binary; LLM-1/-3, VIEW-1: one subtest per route, `claude-code` must pass, `copilot-cli` is recorded |
| 17 | DH-E-SDK-1 | S17 | Linux (E1) | **No** | Go client over stdio against the built binary |
| 18 | DH-E-SANDBOX-7 | S17 | Linux (E1) | **No** | 12 tasks x 3 runs x 3 platform texts = 108 runs; pass needs at least 10 of 18 escalations; closes `UP-DEBT-found.Q3.2` (M2-C-6) |
| 19-28 | DH-E-TOOLCHAIN-2, CHOKIDAR-1, CHOKIDAR-2, NODEFS-1, RIPGREP-1, RIPGREP-2, EXEC-1, EXEC-10, HOST-4, STORAGE-6 | S18 | Variants of sec. 4.3 | Yes | NODEFS-1 needs 100 power cuts (M2-R-06) |
| 29-32 | DH-E-NATIVESYSTEM-1..4 | S18 | Linux variants | **No** | |
| 33 | DH-E-SANDBOX-1 | S18 | Linux variants | **No** | |
| 34 | DH-E-FS-1 | S18 | Linux ext4, Windows NTFS | **No** | 10,000 calls under a link-swap helper |
| 35 | DH-E-FS-2 | S18 | Linux, Windows | **No** | Long-running (M2-R-06) |
| 36 | DH-E-VIEW-5 | S18 | Linux | **No** | Given names "a real model API key": read as the signed-in route (M2-I-1) |
| 37 | DH-E-CONTEXT-2 | S18 | Linux, Windows | **No** | |

Totals: 4 guarded (3 authored), 14 in S17 (12 authored), 19 in S18 (10 authored);
**25 authored, 12 to author**. The mapping of each run to its TC row is
architecture.md's contract; e2e-scenarios.md owns the scripts.

### 4.3 Environments

**M2-EN-1 -- Model route (amended).** E1 is "Linux with the user's signed-in Claude
Code CLI as the required route and the signed-in Copilot CLI as the recorded second
route". No API key exists in any environment, file or workflow input (decision
`models-signed-in-claude-copilot`, M1-D-PIPE-3). Where an impl.md "Given" says "a real
model API key" it is read as this route (M2-I-1); impl.md text is not edited (DC-2).
The DeepSeek provider stays keyless and fake-tested (M1-D-PIPE-5).

**M2-EN-2 -- Platforms (amended).** Gate lanes and scenario platforms are **Windows
native and Linux under WSL**. macOS rows of S17/S18 scenarios are *out of scope by
decision `platforms-windows-wsl`* and are recorded so, not as passed and not as
failed (M2-Q-4; `macos-return-stage` stays open).

**M2-EN-3 -- E2 variants.** S18 needs the Linux variants impl.md sec. 7.1 names for E2
(distributions, kernels with and without Landlock, cgroup v2 delegated and not,
`systemd --user` present and absent, unprivileged user namespaces allowed and denied)
plus Windows native. Hosted `ubuntu-latest` and `windows-latest` cover two; the rest
need a fleet (M2-Q-3, `m2-e2-fleet`).

**M2-EN-4 -- Power-cut host.** NODEFS-1 alone needs a hypervisor that can cut a
guest's power 100 times, with an acknowledgement recorder (architecture.md TC-21s5);
no hosted runner provides it. No other M2 scenario needs it.

**M2-EN-5 -- Two accounts and shares.** STORAGE-6 (privacy to one user) needs a
second OS account; FS-1/FS-2 variants on network shares need an SMB share.

**M2-EN-6 -- Scratch homes.** Every guarded and acceptance run uses a fresh temporary
harness home; no run reads or writes a developer's real home (M2-C-1).

**M2-EN-7 -- Time limits.** impl.md sec. 7.2: E1 15 min per scenario, 2 h per job; E2
20 min per scenario, 3 h per variant. SANDBOX-7, FS-2 and NODEFS-1 exceed them
(M2-R-06, M2-Q-6).

**M2-EN-8 -- Sign-in on runners.** A hosted runner has no signed-in `claude`; S17 runs
on a self-hosted Linux runner or a developer's Linux (WSL) machine whose user is
signed in, and the evidence record names which.

### 4.4 Acceptance decisions

| ID | Decision |
|---|---|
| M2-DEC-2 | The scenario workflow keeps its `preflight` plus `scenarios` shape; the matrix stays `[ubuntu-latest, windows-latest]` for gate-venue scenarios; S17/S18 jobs run on labelled self-hosted runners or manual runbooks, and a variant without a runner is *not run*, never *passed* (M2-C-2) |
| M2-DEC-3 | A required scenario that skips fails the run (step 16.7, DH-D-UTIL-7/-8); `DSH_REQUIRE_SCENARIOS` lists every S17/S18 scenario of sec. 4.2 when their job runs |
| M2-DEC-4 | The Copilot leg of a two-route scenario is *recorded*: its failure does not fail the stage, but it must run and its result is kept (index comment on LLM-1, LLM-3, VIEW-1) |
| M2-DEC-5 | A scenario closes a deliverable only from an evidence record row with commit, environment and artifacts (impl.md sec. 7.3); a CI log alone does not |
| M2-DEC-6 | HOST-4 runs on Windows and Linux; its "reaches the UI" clause is checked by the listener answering, until S22 builds the UI (M2-Q-5, `m2-host4-ui-clause`) |

### 4.5 Scenarios authored here, executed later (17)

| Scenario | Executes at |
|---|---|
| DH-E-HOST-1, DH-E-HOST-2, DH-E-BUNDLE-3 | S22 |
| DH-E-CLI-1, DH-E-CLI-2 | Step 27.8 |
| DH-E-SANDBOX-2, DH-E-EXEC-7, DH-E-EXEC-8, DH-E-HOST-7, DH-E-HOST-8, DH-E-HOST-9, DH-E-SDK-2, DH-E-UTIL-2 | S39 |
| DH-E-BUNDLE-1, DH-E-BUNDLE-2, DH-E-BUNDLE-4, DH-E-API-5 | S48 |

Each is indexed with `stage:` set to its executing stage and compiles under
`-tags scenario`; it is not run here. DH-E-FS-5 (S28) and DH-E-CONTEXT-1 (S32) are
authored with those stages, not here. (The draft removed in #96 said "That is 18"
over this same list; the list counts 17.)

### 4.6 Enabling rule

Nothing built in M2 is enabled for users (impl.md sec. 3.3 M2 row): team pre-release
only, scratch homes, Linux. Reasons stated by impl.md: CLI-1 and CLI-2 run at S27; no
macOS or Windows backend exists before S27; BOOT-2 runs at S21; the `acp` and
`sdk-minimal` profiles are P1 (S37); builds are unsigned.

---

## 5. Out of scope

| Item | Where it belongs | Why not here |
|---|---|---|
| The web UI, browser admission end to end, `dsh web` as a product | S19..S22 | S15 builds the listener and launch-token admission only; the UI is S22 |
| macOS and Windows confinement backends (Seatbelt, restricted tokens) | S27 | impl.md G-3; on Windows M2 runs only `danger-full-access`, reported `Unconfined` (M2-C-8) |
| Release signing, installers, update channel | S27 and later | Builds are unsigned rehearsal builds (sec. 4.6) |
| `dsh acp` and the `sdk-minimal` profile | S37 (P1) | impl.md sec. 3.3 |
| SSH world, remote sessions, the `ssh-helper` mode's behaviour | S28 and later | The hidden mode name is reserved only (`cli/tree.go`) |
| Skill catalogue, instruction search beyond the files of CONTEXT-2 | S32 | DH-E-CONTEXT-1 runs there |
| Multi-process configuration editing proof | S21 (DH-E-BOOT-2) | impl.md sec. 3.3 reason |
| New SDK methods, a protocol version bump, any change to `src/sdk/protocol/schema.json` | Later protocol stages | Wire is fixed (TC-12g, M2-C-7) |
| Closing held-open items H-1..H-4, H-10 | Their own resolutions (impl.md sec. 10.3) | They gate SDK-4, SDK-5, UTIL-7 to *done*, not to *built* |
| Running the 17 later-run scenarios of sec. 4.5 | Their stages | Rule 4: a scenario runs at the stage impl.md assigns |
| Closing M1 | M1 gap-closure Phases 18, 19 | M2-DEC-1; this story consumes M1 closure, it does not perform it |
| Editing impl.md, design.md or area specifications | Owner process | DC-2 `impl-md-pointer-lines-only`: pointer lines only |

## 6. Non-goals

The M1 non-goals NG-1..NG-12 ([M1 tech-spec](../harness-DSH-M1/tech-spec.md) sec. 4)
hold unchanged. Their sharpest M2 consequences:

| Non-goal | M2 consequence |
|---|---|
| NG-1 Compatibility with upstream formats, protocols, clients or fixtures | The SDK server speaks `src/sdk/protocol` Version 1 only; no upstream client or fixture is tested against it |
| NG-2 Translating the TypeScript source file by file | Fence, sandbox and launch stage are re-derived from the FS, SANDBOX and EXEC area specs |
| NG-4 cgo in the shipped binary | Landlock and cgroup access use syscalls from pure Go; `bwrap` is an external program, not a linked library |
| NG-6 An embedded interpreter | The shell tool runs the user's shell as a confined child; nothing is embedded |
| NG-7 Any data leaving the machine by default | Default `headless`/`sdk` profiles send nothing until a route is configured (BUNDLE-6); the listener admits by source first (loopback or an allowed peer), then by launch-token cookie (C45) |
| NG-8 Approval rules stored across sessions | The SDK carries no session grant across a connection (default I2 Q5, M2-C-4) |
| NG-10 A security boundary stronger than design.md sec. 6 | `dsh sandbox status` reports `Full`/`Partial`/`Unconfined` as computed, never stronger (M2-C-8) |
| NG-11 Re-deciding design or impl | Any gap found while building is a question to the owner (sec. 10) or an inconsistency (sec. 11), never a silent design choice |

Non-goals specific to this story:

- **M2-NG-1.** No sandbox "approximation" on Windows or macOS: refuse, do not
  pretend (INV-3).
- **M2-NG-2.** No API-key path for real-model runs, even "just for CI".
- **M2-NG-3.** No second process-spawn path beside `world/subprocess` and the launch
  stage, not even in tests that run the binary (`testkit/dshbin` goes through it).
- **M2-NG-4.** No performance targets beyond impl.md's time limits; S17/S18 prove
  correctness, not speed.

## 7. Hard constraints

### 7.1 Inherited from M1

All M1 constraint families hold as written in the
[M1 tech-spec](../harness-DSH-M1/tech-spec.md) sec. 5, as amended by the
[M1 decisions](../harness-DSH-M1/decisions.md): **RM-1..10** (repository and module
layout, Go floor `go 1.27.4` per `go-floor-1-27-4`), **QG-1..10** (gate: both lanes
green, goldens in the same commit, no skipped required test), **EV-1..7** (evidence
record form), **SP-1..14** (security and privacy; SP-1 narrowed to the
client-invocation boundary on client routes by M1-D-PIPE-4), **EN-1..6**
(environments, amended by `platforms-windows-wsl`), **DC-1..6** (documents; DC-2 is
`impl-md-pointer-lines-only`).

### 7.2 New in M2

| ID | Constraint | Enforced by |
|---|---|---|
| M2-C-1 | Every guarded and acceptance run uses a scratch home and scratch workspaces; takeover, deletion and process-tree tests never touch a developer's data | `testkit/scenario` home factory; evidence record field |
| M2-C-2 | A scenario not run is recorded *not run* with a reason; *open* never counts as *passed*; a variant out of scope by decision is named with the decision; an in-scope variant *not run* keeps its stage *open* | Closure record; M2-DEC-2; C57 |
| M2-C-3 | *Built* and *done* are never merged: a deliverable is done only when every proving scenario passed and its held-open items closed | Closure record (M2-DOD-6) |
| M2-C-4 | Each unconfirmed default of impl.md sec. 9.1 is confirmed or changed by the owner **before its stage closes**: S12 full-access label (I2 Q3) and UTF-8-only text tools; S13 `partial` when delegated execution cannot be stopped (I2 Q4); S15 no SDK session grant (I2 Q5) and client time zone (U-K2-5); S16 a second `dsh web` reuses the host (I3 Q1), Windows double-click (I3 Q8), `dsh headless` progress (U-K2-4) | M2-Q-7; stage evidence record |
| M2-C-5 | Model spend is bounded: S17 runs use the signed-in subscription route; a run states its prompt count; SANDBOX-7's 108 runs are one budgeted job | Evidence record |
| M2-C-6 | A change to the sandbox policy text may not merge until DH-E-SANDBOX-7 passes on it (at least 10 of 18 escalations) | Review rule; `UP-DEBT-found.Q3.2` |
| M2-C-7 | `src/sdk/protocol/schema.json` and Version 1 are unchanged; no SDK method is added | TC-12g golden |
| M2-C-8 | Windows has no backend in M2: a confined policy is refused with `sandbox/unavailable`; only `danger-full-access` runs and is reported `Unconfined`. Linux reports `Full` or `Partial` and names each missing effect; enforcement is never reported stronger than real | INV-3; R-05 of architecture.md |
| M2-C-9 | One spawn path (`world/subprocess`) and one launch plan per command; one error type (`util/herr`) with the codes of sec. 7.3; a confined command does not run until `go` follows `ready` | INV-14; contract tests |
| M2-C-10 | `dsh sdk` writes only frames on stdout; logs go to stderr or the log file | TC rows for C48/C49 |
| M2-C-11 | Secrets enter only through stdin (`dsh secret set`) or the client's own sign-in; no secret value in argv, environment dumps, logs, workflow files or evidence records | SP-*; redaction tests |
| M2-C-12 | Platform scope is Windows native plus Linux under WSL (`platforms-windows-wsl`); macOS rows are out of scope until `macos-return-stage` is decided | M2-EN-2 |
| M2-C-13 | `dsh headless` and `dsh sdk` write no runtime record; only listening web hosts take part in host reuse; a resume of a session owned by a live writer is refused `session/owned` (lease epoch) | architecture.md sec. 4 |
| M2-C-14 | Admin commands construct no agent and no model client | INV-10 |

### 7.3 Error codes and exit statuses introduced

New codes, all through `util/herr`: `fs/sandbox-denied`, `fs/stale-version`,
`workdir/unavailable`, `sandbox/unavailable`, `sandbox/enforcement-insufficient`,
`sandbox/launch-failed`, `launch/no-channel`, `session/owned`, plus the existing `sdk/*`
family. Exit statuses of the launchers: 0 success, 124 deadline, 125 launcher failure,
70 internal error, 78 configuration error (architecture.md sec. 5.3). Exit 130 on
interrupt is an open alignment (architecture.md I-6).

---

## 8. Risks

Likelihood (L) and impact (I) are High, Medium or Low. architecture.md R-01..R-06 are
the design-level risks; these are the story-level ones.

| ID | Risk | L | I | Trigger to watch | Mitigation | Response if it fires |
|---|---|---|---|---|---|---|
| M2-R-01 | **No signed-in runner for S17** (amended from "no DeepSeek key"): hosted runners cannot sign in to `claude`, so S17 cannot run in CI | H | H | S17 job has no runner label | M2-EN-8: self-hosted Linux runner or a developer's WSL machine; record which | S17 stays *open*; deliverables stay *built* (M2-C-2) |
| M2-R-02 | **E2 fleet not provided**: hosted runners cover 2 of about a dozen S18 variants | H | H | No owner answer to M2-Q-3 by S16 built | Ask now (M2-Q-3); runbooks for manual variants (sec. 3.4 item 7) | **S18 stays open** (M2-DOD-4; architecture.md C57 refuses a stage close while a listed scenario is missing, skipped or failed); each missing variant is recorded *not run*; affected deliverables stay *built*. Only an owner decision that removes a variant from scope (as `platforms-windows-wsl` removed macOS) lets S18 close without it |
| M2-R-03 | **Fence defect writes outside the roots** during FS-1/FS-3/VIEW-5 | M | H | Hash listing of the outside directory differs | Scratch dirs only (M2-C-1); fence contract tests first (TC rows for C32) | Stop the stage; keep both listings; fix before any further S18 run |
| M2-R-04 | **M1 not closed** delays every M2 run (sec. 1.5) | H | M | Phase 18 of the M1 gap-closure plan unmerged when S16 is built | M2-DEC-1 default lets S12..S16 build meanwhile | Runs wait; no M2 stage closes before M1 closure |
| M2-R-05 | **WSL is not a real Linux kernel environment for every claim**: WSL2's kernel config, missing `systemd --user` or no delegated cgroup make EXEC-1/-10, NATIVESYSTEM and SANDBOX-1 report fallbacks | H | M | `dsh sandbox status` on the WSL lane shows `Partial` or process-group fallback | Run those scenarios on native Linux variants too (M2-EN-3); record WSL results as one variant | A WSL-only pass is not counted for variants it does not represent |
| M2-R-06 | **Long scenarios exceed impl.md time limits** (SANDBOX-7 108 runs, FS-2, NODEFS-1 100 power cuts) | H | M | Job hits 2 h / 3 h | Split into sharded jobs; ask the owner for limits (M2-Q-6) | Record as *not run (time limit)*, never *passed*; the stage stays *open* until the run completes |
| M2-R-07 | **Model behaviour varies**: SANDBOX-7 escalation threshold (10 of 18) or AGENT scenarios flake on a real model | M | M | Pass rate near threshold | Three runs per task as specified; keep transcripts | Re-run counts as a new run in the record; a policy text change waits (M2-C-6) |
| M2-R-08 | **Copilot leg drifts**: signed-out line not pinned (DH-E-COPILOTCLI-1), empty `COPILOT_HOME` may not sign out | H | L | LLM-3 Copilot leg settles differently | Leg is *recorded* (M2-DEC-4) | Note in record; no stage impact |
| M2-R-09 | **Size**: 56 steps, 12 scenarios to author, 17 more for later, plus runbooks; story points 0 gives no budget | H | M | S16 slips past the plan's estimate | Plan stages run S14 beside S12/S13 (sec. 3.3); split question M2-Q-1 | Split acceptance (S17/S18) into a follow-on story |
| M2-R-10 | **Wire drift**: a step adds an SDK method or field | L | H | `schema.json` diff in a PR | TC-12g golden; M2-C-7 | Revert; raise an owner question |
| M2-R-11 | **`bwrap` missing or blocked** on the WSL lane or a variant (user namespaces denied) | M | M | Lane check fails (architecture.md I-8) | `wsl.sh` provisions/checks `bwrap`; Landlock path tested separately | STORE-2 and SANDBOX-1 cannot run there; recorded *not run*; the stage stays *open* until they run on a variant with `bwrap` |
| M2-R-12 | **Windows unconfined path misused**: a confined policy silently runs unconfined | L | H | `Unconfined` with a policy other than `danger-full-access` | M2-C-8 contract tests; refusal `sandbox/unavailable` | Block merge |

## 9. Assumptions

| ID | Assumption | If false |
|---|---|---|
| M2-A-1 | *(Amended.)* The user's signed-in Claude Code CLI is available on the S17 machine and its subscription covers the S17 runs, including SANDBOX-7's 108 runs | M2-R-01; S17 stays open |
| M2-A-2 | The M1 contracts (store, runtime, pipeline, approvals, agent loop) are stable enough to build S12..S16 on before M1 closes | Rework in S12..S16 when M1 closure changes a contract |
| M2-A-3 | A Linux machine with `bwrap`, Landlock-capable kernel and delegated cgroup v2 is available for at least one S18 variant | SANDBOX-1, EXEC-1/-10 cannot show `Full` |
| M2-A-4 | impl.md's stage text for S12..S18 does not change during the story; if it does, only pointer lines are added (DC-2) | Re-plan the affected step |

## 10. Open questions

| ID | Question | Default until answered | Blocks |
|---|---|---|---|
| M2-Q-1 | Run M2 as one story or split acceptance (S17/S18) into a follow-on story | One story | Story sizing |
| M2-Q-2 (`m2-entry-m1-closure`) | May S12..S16 build before M1 is closed | Yes, build; no run or closure before M1 closure | First M2 commit |
| M2-Q-3 (`m2-e2-fleet`) | Who provides the S18 Linux variants, power-cut host and two-account machines | Team provides runbooks; variants without hardware are *not run* and S18 stays *open* | S18 closure |
| M2-Q-4 (`m2-macos-rows`) | macOS rows: out of scope by `platforms-windows-wsl`, or provisioned | Out of scope, recorded so | S18 closure |
| M2-Q-5 (`m2-host4-ui-clause`) | HOST-4 "reaches the UI": listener answer suffices at M2, or the clause moves to S22 | Listener answer suffices | HOST-4 result |
| M2-Q-6 (`m2-scenario-deadlines`) | Time limits for SANDBOX-7, FS-2, NODEFS-1 | Sharded jobs within impl.md limits | S17, S18 |
| M2-Q-7 (`m2-unconfirmed-defaults`) | Confirm impl.md sec. 9.1 defaults for S12..S16 (M2-C-4) | Defaults stand; stages cannot close until confirmed | S12..S16 closure |

M2-Q-8 and M2-Q-9 of the removed draft (FS-5/CONTEXT-1 authoring stage; macOS
variants) are answered: sec. 4.5 and M2-Q-4.

## 11. Inconsistencies found

| ID | Inconsistency | Resolution here |
|---|---|---|
| M2-I-1 | impl.md S16 Entry, VIEW-5 and other Givens name "a real model API key"; decision `models-signed-in-claude-copilot` removed keys (architecture.md I-1) | Read as the signed-in Claude route (M2-EN-1); impl.md unchanged (DC-2) |
| M2-I-2 | Fact F-7 (pwsh 5.1 UTF-8) names S18, but impl.md sec. 7.5 places EXEC-7 at S39 | Follow sec. 7.5: EXEC-7 authored here, run at S39 |
| M2-I-3 | impl.md says `test/scenario`; the repo uses `src/harness/internal/testkit/scenarios` (architecture.md I-2) | Use the repo path |
| M2-I-4 | Several S18 Givens and the 16.8 lead-in name three platforms (architecture.md I-5, I-7) | Two platforms by decision; macOS rows recorded out of scope |
| M2-I-5 | The deliverable-to-finishing-stage mapping of sec. 3.2 must match implementation-plan.md | Plan reproduces the 11/24 split or raises the difference |
| M2-I-6 | HOST-4's "reaches the UI" clause precedes the UI (S22) (architecture.md I-4) | M2-DEC-6, M2-Q-5 |
| M2-I-7 | Interrupt exit 130 is not in architecture.md's exit list (architecture.md I-6) | Open alignment; sec. 7.3 |
| M2-I-8 | `tools/harness-gate/lanes/wsl.sh` has no `bwrap` (architecture.md I-8) | Work product sec. 3.4 item 6 |
| M2-I-9 | The removed draft counted 18 later-run scenarios over a 17-item list | 17 (sec. 4.5) |
| M2-I-10 | The removed draft assumed `built-through: DH-S-7` and three CI operating systems | Superseded by sec. 1.5 |

## 12. Coverage of the story description

| Story phrase | Covered in |
|---|---|
| "clone deepseek harness" | sec. 1.1, sec. 6 (NG-1, NG-2) |
| "given context in docs\harness ... design and impl doc" | Header source list; sec. 7.1 DC-2; sec. 11 |
| "follow impl" | sec. 3.2 (stages and Entry lines), sec. 3.3 (order), sec. 4.2 and 4.5 (scenario placement), M2-C-4 |
| "milestone M2 of dsh" | sec. 1.1, sec. 3.5 (definition of done), sec. 4.6 (enabling rule) |
| "first prompt from the command line and the SDK" | sec. 1.2, M2-O-4, M2-O-5, M2-O-7 |
| "impl.md stages DH-S-12 to DH-S-18" | sec. 3.2, sec. 4 |
| "following harness:DSH-M1" | sec. 1.5, M2-DEC-1, M2-R-04, M2-Q-2 |
| "Its tech spec is docs/stories/harness-DSH-M2/tech-spec.md" | This file |
